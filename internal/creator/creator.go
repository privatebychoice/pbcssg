// Package creator is pbcssg's local, single-operator editor admin (SPEC §2.1,
// docs/CREATOR.md). It is served by `pbcssg creator` on loopback only and is the
// only mode that opens the SQLite editing store. It authors content, runs the
// real privacy pipeline for preview and the pre-publish gate, and triggers
// builds — reusing the tested render/hygiene/linkscan/gate/build packages.
//
// v1 (MVP): page tree, page editor (markdown body + tags/keywords/summary), live
// preview, publish (with the gate), and build. Blocks/media/settings follow.
package creator

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/gate"
	"go.privatebychoice.com/pbcssg/internal/hygiene"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
	"go.privatebychoice.com/pbcssg/internal/metrics"
	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// Publisher swaps the public listener's served bundle in-process — the server's
// Reload (§7.9). It is nil in standalone `pbcssg creator`, where there is no running
// public site to cut over; the editor then offers Build/Release but not Publish.
type Publisher interface {
	Reload(contentDir string) error
}

// Config configures the editor.
type Config struct {
	OutDir     string       // where builds are written
	ReleaseDir string       // where release tarballs / versioned release dirs are written
	Build      build.Config // seed site parameters (overlaid by stored settings)

	// Publisher and ContentLink enable the unified in-process Publish (§7.9): Publish
	// builds a versioned release dir under ReleaseDir, atomically repoints the
	// ContentLink `current` symlink to it, and calls Publisher.Reload. Both are set
	// only in a unified launch (`pbcssg server -admin-addr`); nil/empty in standalone.
	Publisher   Publisher
	ContentLink string // the `current` symlink the public listener serves from

	// Metrics, when set (unified launch with metrics enabled), is the shared registry
	// the public server's middleware records into; the editor renders it as an admin
	// page (§7.7) and shows a Metrics nav link. Nil in standalone creator.
	Metrics *metrics.Registry

	// AppStore and AdminOrigin enable creator passkey auth (§2.4). AppStore is the
	// runtime store (app.db) holding accounts, credentials, sessions, and invites;
	// AdminOrigin is the exact origin the admin listener is served on behind the TLS
	// proxy (e.g. "https://admin.example.com"), from which the WebAuthn RP ID is
	// derived. Both are set only in a unified launch with -app-db; nil/empty in
	// standalone creator, which has no accounts and stays network-gated only.
	AppStore    *appstore.Store
	AdminOrigin string
}

// runtimeState is the editor's effective site config and the pipeline objects
// derived from it. It is swapped atomically when settings change, so request
// handlers read a consistent snapshot without locking.
type runtimeState struct {
	build      build.Config
	base       *url.URL
	classifier *classify.Classifier
	scanner    *linkscan.Scanner
	hcfg       hygiene.Config
}

// Creator is the editor HTTP handler.
type Creator struct {
	store *store.Store
	cfg   Config
	mux   *http.ServeMux
	tmpl  *template.Template
	csrf  string
	rt    atomic.Pointer[runtimeState]

	// Small cache for the metrics heat-map PNG (§7.7), so rapid polling doesn't
	// re-encode on every request.
	metricsMu  sync.Mutex
	metricsPNG []byte
	metricsAt  time.Time

	// Creator passkey auth (§2.4), set only when Config.AppStore is provided. appDB is
	// the runtime store; flow is the shared WebAuthn ceremony/session core (register,
	// login, session cookie, challenge store) parameterized for the creator role and
	// admin origin. authEnabled() reports whether these are wired. cookieName/cookieSecure
	// derive from the admin origin scheme: an https origin uses a `__Host-` Secure cookie;
	// http (localhost dev only) uses a plain-named cookie since Secure/`__Host-` require
	// HTTPS. They mirror the flow's cookie config for the session gate and tests.
	appDB        *appstore.Store
	flow         *authflow.Flow
	cookieName   string
	cookieSecure bool
}

// New builds a Creator over an open store. The effective site config is the CLI
// seed overlaid with any settings previously saved in the store.
func New(st *store.Store, cfg Config) (*Creator, error) {
	c := &Creator{
		store: st,
		cfg:   cfg,
		csrf:  randToken(),
		tmpl:  adminTemplates(),
	}
	if err := c.applyConfig(c.loadBuildConfig(cfg.Build)); err != nil {
		return nil, err
	}
	if err := c.initAuth(cfg); err != nil {
		return nil, err
	}
	c.routes()
	return c, nil
}

// state returns the current runtime snapshot.
func (c *Creator) state() *runtimeState { return c.rt.Load() }

// buildConfig returns the effective build config with the copyright year stamped
// to now, so the build package stays deterministic (year is an input, not read
// from the wall clock inside it).
func (c *Creator) buildConfig() build.Config {
	bc := c.state().build
	bc.Year = time.Now().Year()
	return bc
}

// applyConfig rebuilds the derived pipeline (base URL, classifier, scanner,
// hygiene) from bc and swaps it in atomically.
func (c *Creator) applyConfig(bc build.Config) error {
	base, err := url.Parse(bc.BaseURL)
	if err != nil {
		return fmt.Errorf("creator: base URL %q: %w", bc.BaseURL, err)
	}
	copts := []classify.Option{classify.WithFirstParty(bc.FirstParty...)}
	if len(bc.ClassifyData) > 0 {
		copts = append(copts, classify.WithDataBytes(bc.ClassifyData))
	}
	classifier, err := classify.New(copts...)
	if err != nil {
		// A stored custom dataset that no longer parses must not brick the editor:
		// fall back to the library defaults so the operator can fix it in the UI.
		// The save path validates before persisting, so this is a defensive backstop.
		if len(bc.ClassifyData) == 0 {
			return fmt.Errorf("creator: classifier: %w", err)
		}
		classifier, err = classify.New(classify.WithFirstParty(bc.FirstParty...))
		if err != nil {
			return fmt.Errorf("creator: classifier: %w", err)
		}
	}
	c.rt.Store(&runtimeState{
		build:      bc,
		base:       base,
		classifier: classifier,
		scanner:    linkscan.NewScanner(classifier, base),
		hcfg:       hygiene.Config{Base: base, FirstParty: firstPartyPred(bc.FirstParty)},
	})
	return nil
}

// ServeHTTP applies the creator session gate (§2.4) when passkey auth is enabled: an
// unauthenticated request to a protected route is redirected to /admin/login (GET) or
// refused (other methods). The auth ceremony endpoints, the login/register pages, the
// logout endpoint, and the self-hosted assets stay public so a visitor can actually
// sign in. When auth is off (standalone creator, or a unified launch without -app-db)
// the gate is a no-op — the admin is protected by its network controls only (§7.9).
func (c *Creator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.authEnabled() && !isPublicAdminPath(r.URL.Path) {
		if _, ok := c.resolveSession(r); !ok {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			} else {
				http.Error(w, "authentication required", http.StatusUnauthorized)
			}
			return
		}
	}
	c.mux.ServeHTTP(w, r)
}

// isPublicAdminPath reports whether a path is reachable without a creator session —
// the sign-in/registration surface and the self-hosted assets that render it.
func isPublicAdminPath(p string) bool {
	switch p {
	case "/admin/login", "/admin/register", "/admin/logout":
		return true
	}
	return strings.HasPrefix(p, "/admin/auth/") || strings.HasPrefix(p, "/admin/assets/")
}

// resolveSession resolves the session cookie to a live creator account. It returns
// false for a missing/expired session or an account that is banned or not a creator.
func (c *Creator) resolveSession(r *http.Request) (appstore.Account, bool) {
	return c.flow.Resolve(r)
}

func (c *Creator) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /", c.handleDashboard)
	m.HandleFunc("GET /pages/new", c.handleNew)
	m.HandleFunc("POST /pages", c.handleCreate)
	m.HandleFunc("GET /pages/{id}", c.handleEdit)
	m.HandleFunc("POST /pages/{id}", c.handleSave)
	m.HandleFunc("POST /pages/{id}/publish", c.handlePublish)
	m.HandleFunc("POST /pages/{id}/unpublish", c.handleUnpublish)
	m.HandleFunc("POST /pages/{id}/delete", c.handleDelete)
	m.HandleFunc("POST /pages/{id}/rekey", c.handleRekey)
	m.HandleFunc("GET /pages/{id}/preview", c.handlePreview)
	m.HandleFunc("GET /pages/{id}/preview/raw", c.handlePreviewRaw)
	m.HandleFunc("GET /external/youtube/{name}", c.handleExternalYouTube)
	m.HandleFunc("GET /external/{provider}/{name}", c.handleExternalEmbed)
	m.HandleFunc("POST /preview", c.handleLivePreview)
	m.HandleFunc("POST /scan", c.handleScan)
	m.HandleFunc("POST /build", c.handleBuild)
	m.HandleFunc("POST /admin/release", c.handleRelease)
	m.HandleFunc("POST /admin/publish", c.handleSitePublish)
	m.HandleFunc("GET /admin/metrics", c.handleMetrics)
	m.HandleFunc("GET /admin/metrics/heatmap.png", c.handleMetricsHeatmap)
	m.HandleFunc("GET /admin/metrics/metrics.json", c.handleMetricsJSON)
	m.HandleFunc("GET /admin/assets/{name}", c.handleAsset)
	m.HandleFunc("GET /admin/media", c.handleMedia)
	m.HandleFunc("POST /admin/media", c.handleUpload)
	m.HandleFunc("POST /admin/media/{sha}/delete", c.handleMediaDelete)
	m.HandleFunc("POST /admin/media/{sha}/note", c.handleMediaNote)
	m.HandleFunc("POST /admin/media/{sha}/tags", c.handleMediaTags)
	m.HandleFunc("GET /media/{name}", c.handleServeMedia)
	m.HandleFunc("GET /admin/settings", c.handleSettings)
	m.HandleFunc("POST /admin/settings", c.handleSaveSettings)
	m.HandleFunc("GET /admin/classification", c.handleClassification)
	m.HandleFunc("POST /admin/classification", c.handleSaveClassification)
	m.HandleFunc("POST /admin/classification/preview", c.handleClassificationPreview)
	m.HandleFunc("GET /admin/classification/export", c.handleClassificationExport)
	m.HandleFunc("POST /admin/classification/import", c.handleClassificationImport)
	m.HandleFunc("GET /admin/errorpages", c.handleErrorPages)
	m.HandleFunc("POST /admin/errorpages", c.handleSaveErrorPages)
	m.HandleFunc("GET /admin/favicon", c.handleFavicon)
	m.HandleFunc("POST /admin/favicon", c.handleFaviconUpload)
	m.HandleFunc("POST /admin/favicon/{name}/delete", c.handleFaviconDelete)
	m.HandleFunc("GET /admin/favicon/{name}", c.handleServeFavicon)
	m.HandleFunc("GET /admin/keygroups", c.handleKeyGroups)
	m.HandleFunc("POST /admin/keygroups", c.handleKeyGroupCreate)
	m.HandleFunc("POST /admin/keygroups/{id}/rename", c.handleKeyGroupRename)
	m.HandleFunc("POST /admin/keygroups/{id}/rotate", c.handleKeyGroupRotate)
	m.HandleFunc("POST /admin/keygroups/{id}/delete", c.handleKeyGroupDelete)
	m.HandleFunc("POST /admin/keygroups/{id}/splash", c.handleKeyGroupSplash)
	m.HandleFunc("GET /admin/moderation", c.handleModeration)
	m.HandleFunc("POST /admin/moderation/comments/{id}/approve", c.handleCommentApprove)
	m.HandleFunc("POST /admin/moderation/comments/{id}/reject", c.handleCommentReject)
	m.HandleFunc("POST /admin/moderation/comments/{id}/delete", c.handleCommentDelete)
	m.HandleFunc("POST /admin/moderation/comments/{id}/reply", c.handleCommentReply)
	m.HandleFunc("POST /admin/moderation/comments/{id}/ban-author", c.handleCommentBanAuthor)
	m.HandleFunc("POST /admin/moderation/comment", c.handleCommentCreate)
	m.HandleFunc("POST /admin/moderation/identity", c.handleCreatorIdentity)
	m.HandleFunc("GET /admin/moderation/accounts", c.handleModAccounts)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/ban", c.handleAccountBan)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/unban", c.handleAccountUnban)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/erase", c.handleAccountErase)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/capabilities", c.handleAccountCapabilities)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/label", c.handleAccountLabel)
	m.HandleFunc("POST /admin/moderation/accounts/{id}/revoke-invites", c.handleAccountRevokeInvites)
	m.HandleFunc("GET /admin/passkeys", c.handlePasskeys)
	m.HandleFunc("POST /admin/passkeys/add/options", c.handlePasskeyAddOptions)
	m.HandleFunc("POST /admin/passkeys/add/verify", c.handlePasskeyAddVerify)
	m.HandleFunc("POST /admin/passkeys/{id}/label", c.handlePasskeyLabel)
	m.HandleFunc("POST /admin/passkeys/{id}/delete", c.handlePasskeyDelete)
	m.HandleFunc("GET /admin/invites", c.handleInvites)
	m.HandleFunc("POST /admin/invites", c.handleInviteMint)
	m.HandleFunc("POST /admin/invites/revoke", c.handleInviteRevoke)
	// Creator passkey auth (§2.4): the WebAuthn ceremony endpoints exist only when a
	// runtime store is wired (unified launch with -app-db). The login gate over the
	// routes above is a separate, later step.
	if c.authEnabled() {
		m.HandleFunc("GET /admin/register", c.handleRegisterPage)
		m.HandleFunc("POST /admin/auth/register/options", c.handleRegisterOptions)
		m.HandleFunc("POST /admin/auth/register/verify", c.handleRegisterVerify)
		m.HandleFunc("GET /admin/login", c.handleLoginPage)
		m.HandleFunc("POST /admin/auth/login/options", c.handleLoginOptions)
		m.HandleFunc("POST /admin/auth/login/verify", c.handleLoginVerify)
		m.HandleFunc("POST /admin/logout", c.handleLogout)
	}
	c.mux = m
}

// --- shared helpers ---

// content builds a render.Content from editor form values.
func contentFromForm(r *http.Request) render.Content {
	return render.Content{
		Body:        r.FormValue("body"),
		Blocks:      parseBlocks(r.FormValue("blocks")),
		Summary:     strings.TrimSpace(r.FormValue("summary")),
		Tags:        splitList(r.FormValue("tags")),
		Keywords:    splitList(r.FormValue("keywords")),
		IsIndex:     r.FormValue("isIndex") != "",
		ListExclude: r.FormValue("listExclude") != "",
		NoIndex:     r.FormValue("noIndex") != "",
		Unlisted:    r.FormValue("unlisted") != "",
		IsPost:      r.FormValue("isPost") != "",
		OGImage:     ogImagePath(r.FormValue("ogImage")),
	}
}

// pageIndex builds the site-wide published-page list the renderer uses to resolve
// index blocks (mirrors the build, so preview matches).
func (c *Creator) pageIndex() []render.PageRef {
	pages, err := c.store.Published()
	if err != nil {
		return nil
	}
	var out []render.PageRef
	for _, p := range pages {
		cc, _ := render.Parse(p.ContentJSON)
		out = append(out, render.PageRef{
			Path: p.Path, Title: p.Title, Summary: cc.Summary,
			Date: p.UpdatedAt.Format("2006-01-02"), Time: p.UpdatedAt,
			IsIndex: cc.IsIndex, Exclude: cc.ListExclude,
			IsPost: cc.IsPost, Tags: cc.Tags, NoIndex: cc.NoIndex,
		})
	}
	return out
}

func contentJSON(c render.Content) (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}

// renderPreview renders content to a full themed HTML page (the real renderer),
// with the theme served by the editor for accurate styling.
func (c *Creator) renderPreview(cj, hostPath string) ([]byte, error) {
	st := c.state()
	content, _ := render.Parse(cj)
	// Render + harden markdown reveal fragments so the preview matches the build
	// (§6.9); the classified refs are surfaced separately by extRefListHTML.
	prepped, _, err := build.PrepareReveal(content, st.hcfg, st.scanner)
	if err != nil {
		return nil, err
	}
	// Resolve tag-mode gallery blocks before gating (so a gated tag-gallery previews
	// with its images), matching the build order (§6.14).
	prepped, err = build.PrepareGallery(prepped, c.store)
	if err != nil {
		return nil, err
	}
	// Group-gated blocks (§6.10): pre-process them too, then render in preview mode so
	// the operator sees the gated content with a group label (their own view), rather
	// than the encrypted, keyring-locked form a visitor would get. opts carries the page
	// index/host so a gated index block previews its list.
	gopts := render.Options{PageIndex: c.pageIndex(), HostPath: hostPath, IsIndexPage: content.IsIndex}
	prepped, _, _, err = build.PrepareGated(prepped, gopts, st.hcfg, st.scanner)
	if err != nil {
		return nil, err
	}
	r, err := render.RenderContent(prepped, render.Options{
		SiteName:        st.build.SiteName,
		BuildNumber:     st.build.BuildNumber,
		CSSHref:         "/admin/assets/theme.css",
		ThemeJSHref:     "/admin/assets/pbcssg-theme.js",
		RevealJSHref:    "/admin/assets/pbcssg-reveal.js",
		CodeCopyJSHref:  "/admin/assets/pbcssg-codecopy.js",
		ShareJSHref:     "/admin/assets/pbcssg-share.js",
		Brand:           st.build.Brand(),
		Nav:             st.build.Nav,
		FooterNav:       st.build.FooterNav,
		Year:            time.Now().Year(),
		Tags:            content.Tags,
		PageIndex:       c.pageIndex(),
		HostPath:        hostPath,
		IsIndexPage:     content.IsIndex,
		ShowReadingTime: st.build.ShowReadingTime,
		GatePreview:     true,
	})
	if err != nil {
		return nil, err
	}
	// Apply the same hygiene the build would, so previews are honest.
	h, err := hygiene.Apply(r.HTML, st.hcfg)
	if err != nil {
		return nil, err
	}
	// Heading anchors + toc fill (§6.12), mirroring the build (after hygiene, before
	// the external-references listing is injected below).
	anchored, err := render.AnchorsAndTOC(h.HTML)
	if err != nil {
		return nil, err
	}
	h.HTML = anchored
	// Inject the external-references listing into the layout's slot, mirroring what
	// the build does on the final page (§5.7), so the preview matches production
	// (the listing sits between the content and the footer, not in a side panel).
	list, err := c.extRefListHTML(cj)
	if err != nil {
		return nil, err
	}
	return bytes.Replace(h.HTML, []byte(render.ExtRefSlot), []byte(list), 1), nil
}

// extRefListHTML renders the draft's per-domain external-references listing (the
// same worst-grade-first list the editor's live badges show), so the preview's
// injected listing matches what the build emits on the final page.
func (c *Creator) extRefListHTML(cj string) (string, error) {
	badges, err := c.linkBadges(cj)
	if err != nil {
		return "", err
	}
	refs := make([]render.ExtRef, 0, len(badges))
	for _, b := range badges {
		refs = append(refs, render.ExtRef{
			Domain: b.Domain, Grade: b.Grade, GradeName: b.GradeName,
			Count: b.Count, Reasons: b.Reasons,
		})
	}
	return render.ExternalRefList(refs), nil
}

// scan renders content, applies the same hygiene the build would, and returns the
// classified third-party references — the shared basis for both the pre-publish
// gate and the in-editor privacy badges. It also includes references inside
// markdown reveal blocks (encrypted out of the page HTML), so the badges and the
// pre-publish gate disclose them exactly like the built manifest (§6.9 option C).
func (c *Creator) scan(cj string) ([]linkscan.Result, error) {
	st := c.state()
	rnd, err := render.Render(cj, render.Options{SiteName: st.build.SiteName})
	if err != nil {
		return nil, err
	}
	h, err := hygiene.Apply(rnd.HTML, st.hcfg)
	if err != nil {
		return nil, err
	}
	results, err := st.scanner.Scan(strings.NewReader(string(h.HTML)))
	if err != nil {
		return nil, err
	}
	content, err := render.Parse(cj)
	if err != nil {
		return nil, err
	}
	if _, revealRefs, err := build.PrepareReveal(content, st.hcfg, st.scanner); err == nil {
		results = append(results, revealRefs...)
	} else {
		return nil, err
	}
	return results, nil
}

// renderExternalYouTube renders the Stage-2 /external/youtube/<name> page (SPEC
// §5.8) for preview, mirroring what the build emits: the self-hosted facade
// loads youtube-nocookie only on an explicit click. The facade script and theme
// are served by the editor's own asset routes.
func (c *Creator) renderExternalYouTube(yt render.YouTube, backHref, backLabel string) ([]byte, error) {
	st := c.state()
	html, err := render.ExternalYouTube(yt, render.Options{
		Title:        yt.Title,
		SiteName:     st.build.SiteName,
		BuildNumber:  st.build.BuildNumber,
		CSSHref:      "/admin/assets/theme.css",
		FacadeJSHref: "/admin/assets/pbcssg-youtube.js",
		ThemeJSHref:  "/admin/assets/pbcssg-theme.js",
		Brand:        st.build.Brand(),
		BackHref:     backHref,
		BackLabel:    backLabel,
		Nav:          st.build.Nav,
		FooterNav:    st.build.FooterNav,
		Year:         time.Now().Year(),
	})
	if err != nil {
		return nil, err
	}
	// Apply the same hygiene the build would, so the preview is honest.
	h, err := hygiene.Apply(html, st.hcfg)
	if err != nil {
		return nil, err
	}
	return h.HTML, nil
}

// renderExternalEmbed renders the Stage-2 /external/<provider>/<name> page for a
// generic embed, mirroring what the build emits (the facade frames the embed URL
// only on an explicit click). Preview does not enforce the host allowlist so the
// author can always see the page; the build and served-site CSP enforce it.
func (c *Creator) renderExternalEmbed(e render.Embed, backHref, backLabel string) ([]byte, error) {
	st := c.state()
	html, err := render.ExternalEmbed(e, render.Options{
		Title:        e.Title,
		SiteName:     st.build.SiteName,
		BuildNumber:  st.build.BuildNumber,
		CSSHref:      "/admin/assets/theme.css",
		FacadeJSHref: "/admin/assets/pbcssg-youtube.js",
		ThemeJSHref:  "/admin/assets/pbcssg-theme.js",
		Brand:        st.build.Brand(),
		BackHref:     backHref,
		BackLabel:    backLabel,
		Nav:          st.build.Nav,
		FooterNav:    st.build.FooterNav,
		Year:         time.Now().Year(),
	})
	if err != nil {
		return nil, err
	}
	h, err := hygiene.Apply(html, st.hcfg)
	if err != nil {
		return nil, err
	}
	return h.HTML, nil
}

// gateFor scans content and evaluates the pre-publish gate.
func (c *Creator) gateFor(cj string) (gate.Report, error) {
	results, err := c.scan(cj)
	if err != nil {
		return gate.Report{}, err
	}
	return gate.Evaluate(results, gate.Config{}), nil
}

// checkCSRF validates the per-process CSRF token on a state-changing request.
func (c *Creator) checkCSRF(r *http.Request) bool {
	return r.FormValue("csrf") == c.csrf
}

func (c *Creator) render(w http.ResponseWriter, name string, data any) {
	// Inject global nav flags so every admin page can show the Metrics link (when a
	// unified launch has metrics enabled) and the Sign out control (when passkey auth
	// is on), plus the CSRF token the nav's logout form needs — without each page's
	// own data having to set them.
	if m, ok := data.(map[string]any); ok {
		if _, set := m["ShowMetrics"]; !set {
			m["ShowMetrics"] = c.cfg.Metrics != nil
		}
		if _, set := m["AuthEnabled"]; !set {
			m["AuthEnabled"] = c.authEnabled()
		}
		if _, set := m["CSRF"]; !set {
			m["CSRF"] = c.csrf
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderToString executes a named template to a string (for fragments returned
// inside a JSON payload, e.g. the live /scan panels).
func (c *Creator) renderToString(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := c.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitLines splits a textarea value into trimmed, non-empty entries, accepting
// either newline- or comma-separated input (used for the embed-host allowlist).
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstPartyPred(domains []string) func(string) bool {
	set := make(map[string]bool, len(domains))
	for _, d := range domains {
		set[strings.ToLower(d)] = true
	}
	return func(host string) bool {
		host = strings.ToLower(host)
		if set[host] {
			return true
		}
		for d := range set {
			if strings.HasSuffix(host, "."+d) {
				return true
			}
		}
		return false
	}
}

func randToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "pbcssg-csrf" // extremely unlikely; still non-empty
	}
	return hex.EncodeToString(b)
}
