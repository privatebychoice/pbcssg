package creator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
	"go.privatebychoice.com/pbcssg/internal/theme"
)

// pagesPageSize is how many pages show per dashboard page.
const pagesPageSize = 10

var pageSortHeaders = map[string]bool{"title": true, "path": true, "status": true, "updated": true}

func (c *Creator) handleDashboard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sort := q.Get("sort")
	if !pageSortHeaders[sort] {
		sort = "path"
	}
	// Default direction is ascending, except "updated" (newest first is more useful).
	asc := sort != "updated"
	if dir := q.Get("dir"); dir == "asc" {
		asc = true
	} else if dir == "desc" {
		asc = false
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	pages, total, err := c.store.PagesPage(sort, asc, pagesPageSize, (page-1)*pagesPageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totalPages := (total + pagesPageSize - 1) / pagesPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// header builds a column-header sort link that toggles direction when it is the
	// active column, and defaults sensibly otherwise.
	header := func(col string) string {
		v := url.Values{"sort": {col}}
		if col == sort && asc {
			v.Set("dir", "desc")
		} else if col == sort {
			v.Set("dir", "asc")
		} else if col == "updated" {
			v.Set("dir", "desc") // updated defaults to newest-first
		}
		return "/?" + v.Encode()
	}
	arrow := func(col string) string {
		if col != sort {
			return ""
		}
		if asc {
			return " ▲"
		}
		return " ▼"
	}
	// Comment counts per page path, so the operator sees how many comments a page holds
	// (they persist by path even if the page is deleted). Only when the runtime store is
	// wired; best-effort so a count error never blocks the listing.
	commentCounts := map[string]int{}
	var commentTotals appstore.CommentTotals
	if c.appDB != nil {
		if m, err := c.appDB.CommentCountsByPage(); err == nil {
			commentCounts = m
		}
		if t, err := c.appDB.CommentTotals(); err == nil {
			commentTotals = t
		}
	}
	data := map[string]any{
		"CSRF": c.csrf, "Pages": pages, "Sort": sort, "Asc": asc,
		"Publisher":    c.cfg.Publisher != nil, // unified launch → offer Publish
		"ShowComments": c.appDB != nil, "CommentCounts": commentCounts, "CommentTotals": commentTotals,
		"Page": page, "TotalPages": totalPages, "Total": total,
		"HTitle": header("title"), "HPath": header("path"), "HStatus": header("status"), "HUpdated": header("updated"),
		"ATitle": arrow("title"), "APath": arrow("path"), "AStatus": arrow("status"), "AUpdated": arrow("updated"),
	}
	pageURL := func(pg int) string {
		v := url.Values{"sort": {sort}, "page": {strconv.Itoa(pg)}}
		if asc {
			v.Set("dir", "asc")
		} else {
			v.Set("dir", "desc")
		}
		return "/?" + v.Encode()
	}
	if page > 1 {
		data["PrevURL"] = pageURL(page - 1)
	}
	if page < totalPages {
		data["NextURL"] = pageURL(page + 1)
	}
	// Where a publish/unpublish toggle from this listing returns to, so the operator
	// stays on the same sorted, paginated view.
	data["ReturnURL"] = pageURL(page)
	c.render(w, "dashboard", data)
}

func (c *Creator) handleNew(w http.ResponseWriter, r *http.Request) {
	c.renderEditForm(w, store.Page{}, render.Content{}, true, "", nil)
}

// renderEditForm renders the page editor, repopulated with the given page and
// content, an optional inline error (used to bounce back a bad path without
// losing the operator's input), and any advisory warnings (e.g. broken local
// media references, which do not block the save).
func (c *Creator) renderEditForm(w http.ResponseWriter, page store.Page, content render.Content, isNew bool, errMsg string, warnings []string) {
	// How many comments this page's path holds — surfaced by the Delete button so the
	// operator knows they persist under the path after the page is gone. Existing pages
	// only, and only when the runtime store is wired.
	commentCount := 0
	if !isNew && c.appDB != nil && page.Path != "" {
		commentCount, _ = c.appDB.CommentCountByPage(page.Path)
	}
	c.render(w, "edit", map[string]any{
		"CSRF": c.csrf, "Page": page, "Content": content, "IsNew": isNew,
		"BlocksJSON": blocksJSON(content), "Error": errMsg, "Warnings": warnings,
		"ShowComments": c.appDB != nil && !isNew, "CommentCount": commentCount,
	})
}

func (c *Creator) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	content := contentFromForm(r)
	slug := strings.TrimSpace(r.FormValue("slug"))
	title := strings.TrimSpace(r.FormValue("title"))
	if slug == "" {
		slug = slugify(title)
	}
	page := store.Page{Path: normalizePath(r.FormValue("path")), Slug: slug, Title: title}
	if msg := reservedPathError(page.Path); msg != "" {
		c.renderEditForm(w, page, content, true, msg, nil)
		return
	}
	if msg := pathInputError(page.Path); msg != "" {
		c.renderEditForm(w, page, content, true, msg, nil)
		return
	}
	pid, err := c.store.CreatePage(page)
	if err != nil {
		c.renderEditForm(w, page, content, true, pathErrorMessage(err), nil)
		return
	}
	cj, _ := contentJSON(content)
	if _, err := c.store.SaveRevision(pid, cj, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Give the page its reveal-block key at birth (SPEC §6.9), so a key always
	// exists by build time. Best-effort: the build get-or-creates one as a fallback.
	_, _ = c.store.PageKey(pid)
	// The page is saved; if it references media not in the library, keep the
	// operator on the editor with an advisory warning instead of redirecting.
	page.ID = pid
	if warns, err := c.brokenMedia(cj); err == nil && len(warns) > 0 {
		c.renderEditForm(w, page, content, false, "", warns)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/pages/%d", pid), http.StatusSeeOther)
}

func (c *Creator) handleEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	page, err := c.store.Page(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	content := render.Content{}
	if rev, ok, _ := c.store.LatestRevision(id); ok {
		content, _ = render.Parse(rev.ContentJSON)
	}
	c.renderEditForm(w, page, content, false, "", nil)
}

func (c *Creator) handleSave(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	page, err := c.store.Page(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	content := contentFromForm(r)
	page.Title = strings.TrimSpace(r.FormValue("title"))
	page.Path = normalizePath(r.FormValue("path"))
	if slug := strings.TrimSpace(r.FormValue("slug")); slug != "" {
		page.Slug = slug
	}
	if msg := reservedPathError(page.Path); msg != "" {
		c.renderEditForm(w, page, content, false, msg, nil)
		return
	}
	if msg := pathInputError(page.Path); msg != "" {
		c.renderEditForm(w, page, content, false, msg, nil)
		return
	}
	if err := c.store.UpdatePage(page); err != nil {
		c.renderEditForm(w, page, content, false, pathErrorMessage(err), nil)
		return
	}
	cj, _ := contentJSON(content)
	if _, err := c.store.SaveRevision(id, cj, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The revision is saved; if it references media not in the library, keep the
	// operator on the editor with an advisory warning instead of redirecting.
	if warns, err := c.brokenMedia(cj); err == nil && len(warns) > 0 {
		c.renderEditForm(w, page, content, false, "", warns)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/pages/%d", id), http.StatusSeeOther)
}

func (c *Creator) handlePublish(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	rev, has, err := c.store.LatestRevision(id)
	if err != nil || !has {
		http.Error(w, "nothing to publish", http.StatusBadRequest)
		return
	}
	report, err := c.gateFor(rev.ContentJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Warn + acknowledge: if flagged links need acknowledgement and the operator
	// has not acknowledged, show them first.
	if len(report.NeedsAcknowledgement()) > 0 && r.FormValue("ack") != "1" {
		page, _ := c.store.Page(id)
		c.render(w, "publish", map[string]any{
			"CSRF": c.csrf, "Page": page, "Flags": report.NeedsAcknowledgement(),
		})
		return
	}
	if err := c.store.Publish(id, rev.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeReturn(r, "/"), http.StatusSeeOther)
}

// safeReturn returns the request's "return" field when it is a local, same-site
// path (so a listing toggle can send the operator back to their view), falling
// back to def otherwise. It rejects absolute/protocol-relative URLs to avoid an
// open redirect.
func safeReturn(r *http.Request, def string) string {
	p := r.FormValue("return")
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return def
	}
	return p
}

func (c *Creator) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	if err := c.store.Unpublish(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeReturn(r, fmt.Sprintf("/pages/%d", id)), http.StatusSeeOther)
}

func (c *Creator) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	if err := c.store.DeletePage(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleRekey regenerates the page's reveal-block key (SPEC §6.9). On the next
// build every Hidden (reveal) block on the page re-encodes under the new key. This
// rotates the obfuscation layer; it does not revoke a shared gate code (that is a
// content change) — the editor's confirm dialog says so.
func (c *Creator) handleRekey(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	if _, err := c.store.RekeyPage(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/pages/%d", id), http.StatusSeeOther)
}

// handlePreview renders the standalone preview: the page in an iframe (via the raw
// route), which mirrors the built page — including the External references listing
// between the content and the footer (§5.7), so the preview matches production.
func (c *Creator) handlePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	page, err := c.store.Page(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c.render(w, "previewpage", map[string]any{"Page": page})
}

// handleExternalYouTube serves the consent-gated /external/youtube/<name> page in
// creator mode (Problem: these pages only existed in the built bundle before, so
// the consent card's "Open video page" link 404'd during preview). It finds the
// youtube block by name across the current draft content and renders Stage 2.
func (c *Creator) handleExternalYouTube(w http.ResponseWriter, r *http.Request) {
	yt, host, ok := c.findYouTube(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	html, err := c.renderExternalYouTube(*yt, host.Path, host.Title)
	if err != nil {
		http.Error(w, "preview: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// findYouTube locates a youtube block by its slug across every page's latest
// revision (draft or published), returning the block and the page it lives on,
// so preview resolves the same /external/youtube/<name> URL the build would
// generate — with a back link to that page.
func (c *Creator) findYouTube(name string) (*render.YouTube, store.Page, bool) {
	pages, err := c.store.Pages()
	if err != nil {
		return nil, store.Page{}, false
	}
	for _, p := range pages {
		rev, has, err := c.store.LatestRevision(p.ID)
		if err != nil || !has {
			continue
		}
		content, err := render.Parse(rev.ContentJSON)
		if err != nil {
			continue
		}
		for i := range content.Blocks {
			b := content.Blocks[i]
			if b.Type == "youtube" && b.YouTube != nil && b.YouTube.Name == name {
				return b.YouTube, p, true
			}
		}
	}
	return nil, store.Page{}, false
}

// handleExternalEmbed serves the consent-gated /external/<provider>/<name> page in
// creator mode, so the generic embed card's "Open embed page" link resolves during
// preview (the built bundle has the same page). It finds the embed block by
// provider + name across the current draft content and renders Stage 2.
func (c *Creator) handleExternalEmbed(w http.ResponseWriter, r *http.Request) {
	em, host, ok := c.findEmbed(r.PathValue("provider"), r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	html, err := c.renderExternalEmbed(*em, host.Path, host.Title)
	if err != nil {
		http.Error(w, "preview: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// findEmbed locates a generic embed block by its provider + slug across every
// page's latest revision, returning the block and the page it lives on so preview
// resolves the same /external/<provider>/<name> URL the build would generate.
func (c *Creator) findEmbed(provider, name string) (*render.Embed, store.Page, bool) {
	pages, err := c.store.Pages()
	if err != nil {
		return nil, store.Page{}, false
	}
	for _, p := range pages {
		rev, has, err := c.store.LatestRevision(p.ID)
		if err != nil || !has {
			continue
		}
		content, err := render.Parse(rev.ContentJSON)
		if err != nil {
			continue
		}
		for i := range content.Blocks {
			b := content.Blocks[i]
			if b.Type == "embed" && b.Embed != nil &&
				render.ProviderLabel(b.Embed.Provider) == provider && b.Embed.Name == name {
				return b.Embed, p, true
			}
		}
	}
	return nil, store.Page{}, false
}

// handlePreviewRaw renders the page's latest saved draft as a full themed page
// (no editor chrome) — the source for the standalone preview's iframe.
func (c *Creator) handlePreviewRaw(w http.ResponseWriter, r *http.Request) {
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	cj := `{}`
	if rev, has, _ := c.store.LatestRevision(id); has {
		cj = rev.ContentJSON
	}
	hostPath := ""
	if p, err := c.store.Page(id); err == nil {
		hostPath = p.Path
	}
	c.writePreview(w, cj, hostPath)
}

// handleLivePreview renders the content posted from the editor form (unsaved),
// for live preview. It performs no state change, so it is CSRF-exempt.
func (c *Creator) handleLivePreview(w http.ResponseWriter, r *http.Request) {
	cj, _ := contentJSON(contentFromForm(r))
	c.writePreview(w, cj, normalizePath(r.FormValue("path")))
}

// handleScan returns the in-editor privacy badge fragment for the posted content
// (unsaved). No state change, so it is CSRF-exempt like preview.
// scanResponse is the live editor's /scan payload: the two panel fragments
// (external references + broken media) plus which sources carry broken media, so
// the client can flag the body and the exact content blocks impacted.
type scanResponse struct {
	Badges string `json:"badges"` // #link-badges fragment (external references)
	Media  string `json:"media"`  // #media-warnings fragment (empty when none)
	Body   bool   `json:"body"`   // the markdown body references broken media
	Blocks []int  `json:"blocks"` // indices of content blocks with broken media
}

func (c *Creator) handleScan(w http.ResponseWriter, r *http.Request) {
	content := contentFromForm(r)
	cj, _ := contentJSON(content)
	badges, err := c.linkBadges(cj)
	if err != nil {
		http.Error(w, "scan: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Advisory: flag any local image/video/audio referenced but not in the Media
	// library, attributed to the exact source (body / block) so the author sees it.
	ms, err := c.scanBrokenMedia(content)
	if err != nil {
		http.Error(w, "scan: "+err.Error(), http.StatusBadRequest)
		return
	}
	badgesHTML, err := c.renderToString("badges", map[string]any{"Badges": badges})
	if err != nil {
		http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
		return
	}
	mediaHTML, err := c.renderToString("mediawarnings", ms.Labels)
	if err != nil {
		http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
		return
	}
	mediaHTML = strings.TrimSpace(mediaHTML) // empty -> "" so the client hides the panel
	blocks := make([]int, 0, len(ms.Blocks))
	for i := range ms.Blocks {
		blocks = append(blocks, i)
	}
	sort.Ints(blocks)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(scanResponse{
		Badges: badgesHTML, Media: mediaHTML, Body: ms.Body, Blocks: blocks,
	}); err != nil {
		http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
	}
}

func (c *Creator) writePreview(w http.ResponseWriter, cj, hostPath string) {
	html, err := c.renderPreview(cj, hostPath)
	if err != nil {
		http.Error(w, "preview: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

func (c *Creator) handleBuild(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	report, err := build.Run(c.store, c.buildConfig(), c.cfg.OutDir)
	if err != nil {
		http.Error(w, "build: "+err.Error(), http.StatusInternalServerError)
		return
	}
	c.render(w, "build", map[string]any{
		"CSRF": c.csrf, "Report": report, "OutDir": c.cfg.OutDir,
		"BuildNumber": c.state().build.BuildNumber, "Version": c.state().build.Version,
		"PageIDs": c.pageIDsByPath(), "Publisher": c.cfg.Publisher != nil,
	})
}

// pageIDsByPath maps each page path to its editor ID, so the build report can
// link real content pages to their edit form. Engine-generated pages (tags,
// feeds, /classification) are absent from the map and render as plain text.
func (c *Creator) pageIDsByPath() map[string]int64 {
	ids := map[string]int64{}
	pages, err := c.store.Pages()
	if err != nil {
		return ids
	}
	for _, p := range pages {
		ids[p.Path] = p.ID
	}
	return ids
}

func (c *Creator) handleSettings(w http.ResponseWriter, r *http.Request) {
	c.renderSettings(w, http.StatusOK, "", "")
}

// themeVarView is one theming field rendered in the settings form.
type themeVarView struct {
	Label       string
	Field       string
	Value       string
	Placeholder string
}

// aliasDailyCap returns the configured per-account daily alias-change cap for the Settings form,
// or the baked-in default when the runtime store is not wired.
func (c *Creator) aliasDailyCap() int {
	if c.appDB == nil {
		return appstore.DefaultAliasDailyCap
	}
	return c.appDB.AliasDailyCap()
}

func (c *Creator) renderSettings(w http.ResponseWriter, code int, notice, errMsg string) {
	bc := c.state().build
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	stored := c.storedThemeVars()
	views := make([]themeVarView, len(themeVars))
	for i, v := range themeVars {
		views[i] = themeVarView{
			Label: v.Label, Field: v.Field,
			Value: stored[v.Key], Placeholder: themeDefaults[v.Key],
		}
	}
	brand := bc.Brand() // normalized mode/align/height for the select defaults
	c.render(w, "settings", map[string]any{
		"CSRF":             c.csrf,
		"Cfg":              bc,
		"FirstParty":       strings.Join(bc.FirstParty, ", "),
		"EmbedHosts":       strings.Join(bc.EmbedHosts, "\n"),
		"Nav":              navToText(bc.Nav),
		"FooterNav":        navToText(bc.FooterNav),
		"Feeds":            feedsToText(bc.Feeds),
		"SecContact":       strings.Join(bc.SecurityContacts, "\n"),
		"ThemeVars":        views,
		"CustomCSS":        c.storedCustomCSS(),
		"ClassifyReport":   bc.ClassifyReport,
		"ClassifyDataRepo": bc.ClassifyDataRepoURL,
		"HeaderBrand":      brand.Mode,
		"HeaderAlign":      brand.Align,
		"LogoHeight":       brand.LogoHeight,
		"BrandText":        bc.BrandText,
		"LogoSrc":          bc.LogoSrc,
		"LogoSrcDark":      bc.LogoSrcDark,
		"LogoAlt":          bc.LogoAlt,
		"Fonts":            theme.Fonts,
		"Font":             normalizeFont(bc.Font),
		"LocalTestURL":     c.localTestURL(),
		"KeepReleases":     c.keepReleases(),
		"TrustedProxies":   c.trustedProxiesText(),
		"Maint":            c.store.Maintenance(),
		"AliasDailyCap":    c.aliasDailyCap(),
		"Notice":           notice,
		"Error":            errMsg,
	})
}

func (c *Creator) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	bc := configFromForm(r)
	// GPC lastUpdate is optional but, when set, must be a valid ISO date so the
	// published gpc.json stays spec-valid (§7.2).
	if err := build.ValidateGPCDate(bc.GPCLastUpdate); err != nil {
		c.renderSettings(w, http.StatusBadRequest, "", err.Error())
		return
	}
	// Local server test URL (editor-only; §6.10): validate before anything is saved so
	// a bad value is reported without wiping the rest of the form.
	localTest, err := normalizeLocalTestURL(r.FormValue("localTestURL"))
	if err != nil {
		c.renderSettings(w, http.StatusBadRequest, "", "Local server test URL: "+err.Error())
		return
	}
	// Release retention (§7.4): a whole number ≥ 0 (0 = keep all). Empty falls back to
	// the default. Validate before persisting anything so a bad value doesn't half-save.
	keepReleases := defaultKeepReleases
	if s := strings.TrimSpace(r.FormValue("keepReleases")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			c.renderSettings(w, http.StatusBadRequest, "", "Keep releases must be a whole number 0 or greater.")
			return
		}
		keepReleases = n
	}
	// Metrics trusted-proxy allowlist (§7.7): each entry must be a valid CIDR.
	trustedProxies := strings.TrimSpace(r.FormValue("trustedProxies"))
	if bad := invalidCIDRs(splitCIDRs(trustedProxies)); len(bad) > 0 {
		c.renderSettings(w, http.StatusBadRequest, "", "Trusted proxies must be CIDRs like 127.0.0.0/8 or 10.0.0.0/8: "+strings.Join(bad, ", "))
		return
	}
	// Runtime-store maintenance retention (days; 0 = disable that prune). Blank falls back
	// to the baked-in default. Validate before persisting so a bad value doesn't half-save.
	maintFields := []struct {
		form, key string
		def       int
	}{
		{"maintInviteDays", store.KeyMaintInviteDays, store.DefaultInviteRetentionDays},
		{"maintRejectedDays", store.KeyMaintRejectedDays, store.DefaultRejectedRetentionDays},
		{"maintOrphanDays", store.KeyMaintOrphanDays, store.DefaultOrphanRetentionDays},
		{"maintVacuumDays", store.KeyMaintVacuumDays, store.DefaultVacuumIntervalDays},
		{"maintAliasReleaseDays", store.KeyMaintAliasReleaseDays, store.DefaultAliasReleaseDays},
		{"maintTombstoneDays", store.KeyMaintTombstoneDays, store.DefaultTombstoneRetentionDays},
	}
	maintVals := make(map[string]int, len(maintFields))
	for _, f := range maintFields {
		n := f.def
		if s := strings.TrimSpace(r.FormValue(f.form)); s != "" {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 {
				c.renderSettings(w, http.StatusBadRequest, "", "Maintenance retention values must be whole numbers 0 or greater (0 disables that prune).")
				return
			}
			n = v
		}
		maintVals[f.key] = n
	}
	// The alias daily-change cap lives in app.db (the public origin enforces it live), so it is
	// parsed here but persisted to the runtime store below. 0 = unlimited.
	aliasCap := appstore.DefaultAliasDailyCap
	if s := strings.TrimSpace(r.FormValue("aliasDailyCap")); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 0 {
			c.renderSettings(w, http.StatusBadRequest, "", "Alias changes per day must be a whole number 0 or greater (0 = unlimited).")
			return
		}
		aliasCap = v
	}
	if msg := c.headerBrandError(bc); msg != "" {
		c.renderSettings(w, http.StatusBadRequest, "", msg)
		return
	}
	if bad := invalidEmbedHosts(bc.EmbedHosts); len(bad) > 0 {
		c.renderSettings(w, http.StatusBadRequest, "",
			"These embed hosts aren't valid — use a bare host like peertube.example (optionally with :port or a *. wildcard): "+strings.Join(bad, ", "))
		return
	}
	// security.txt (§7.6): each Contact must be a mailto:/tel:/https URI, and Expires
	// (when set) a date or RFC 3339 timestamp, so the emitted file stays spec-valid.
	for _, ct := range bc.SecurityContacts {
		if strings.TrimSpace(ct) == "" {
			continue
		}
		if err := build.ValidateSecurityContact(ct); err != nil {
			c.renderSettings(w, http.StatusBadRequest, "", "security.txt — "+err.Error())
			return
		}
	}
	if strings.TrimSpace(bc.SecurityExpires) != "" {
		if _, ok := build.NormalizeSecurityExpires(bc.SecurityExpires); !ok {
			c.renderSettings(w, http.StatusBadRequest, "",
				"security.txt Expires must be a date (YYYY-MM-DD) or an RFC 3339 timestamp.")
			return
		}
	}
	// The release number is not an editable settings field (it auto-increments on
	// Package release), so preserve the current value across a settings save.
	bc.BuildNumber = c.state().build.BuildNumber
	// The custom classification dataset has its own editor (§5.7), not this form,
	// so preserve it across a settings save (otherwise the empty form value wipes it).
	bc.ClassifyData = c.state().build.ClassifyData

	// Theme override (§6.4): compose and enforce the privacy guardrail before
	// anything is persisted or applied. A rejected override leaves the current
	// theme (and the built-in fallback) untouched.
	vars := themeVarsFromForm(r)
	if err := validateThemeVars(vars); err != nil {
		c.renderSettings(w, http.StatusBadRequest, "", "Theme value rejected: "+err.Error())
		return
	}
	custom := r.FormValue("customCSS")
	override := composeThemeOverride(vars, custom)
	if err := validateThemeCSS(override); err != nil {
		c.renderSettings(w, http.StatusBadRequest, "", "Theme CSS rejected: "+err.Error())
		return
	}
	bc.ThemeOverride = override

	// applyConfig validates the base URL and rebuilds the pipeline; only persist
	// once the new config is known good, so a bad value can't wedge startup.
	if err := c.applyConfig(bc); err != nil {
		c.renderSettings(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := c.saveBuildConfig(bc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.saveThemeSettings(vars, custom); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.store.SetSetting(keyLocalTestURL, localTest); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.store.SetSetting(keyKeepReleases, strconv.Itoa(keepReleases)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.store.SetSetting(keyTrustedProxies, trustedProxies); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for key, n := range maintVals {
		if err := c.store.SetSetting(key, strconv.Itoa(n)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if c.appDB != nil {
		if err := c.appDB.SetAliasDailyCap(aliasCap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	c.renderSettings(w, http.StatusOK, "Settings saved.", "")
}

func (c *Creator) handleAsset(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("name") {
	case "admin.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write([]byte(adminCSS))
	case "admin.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(adminJS))
	case "theme-toggle.js":
		// The editor chrome's Auto/Light/Dark control (loaded blocking in <head>).
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(adminThemeJS))
	case "blocks.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(blocksJS))
	case "copy.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(copyJS))
	case "classify.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(classifyJS))
	case "register.js":
		// The creator registration ceremony (§2.4): invite -> options -> passkey
		// create -> verify. Served only in a unified launch with a runtime store.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(registerJS))
	case "login.js":
		// The creator login ceremony (§2.4): options -> passkey get -> verify.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(loginJS))
	case "passkeys.js":
		// The authenticated add-a-passkey ceremony (§2.4, A1) on the Passkeys page.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(passkeysJS))
	case "pbcssg-youtube.js":
		// The click-to-load facade for the /external/youtube/<name> preview.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(render.FacadeJS))
	case "pbcssg-theme.js":
		// The light/dark theme script, so the preview honours the toggle like the
		// built site does.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(render.ThemeJS))
	case "pbcssg-reveal.js":
		// The deferred-reveal decode script, so a Hidden (reveal) block reveals in the
		// preview exactly as it will on the built site (§6.9).
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(render.RevealJS))
	case "pbcssg-codecopy.js":
		// The code-block copy-button script, so a Code block's Copy button works in the
		// preview exactly as on the built site (§6.12).
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(render.CodeCopyJS))
	case "pbcssg-share.js":
		// The share-block script, so Copy link / Mastodon work in the preview exactly as
		// on the built site (§6.15).
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(render.ShareJS))
	case "theme.css":
		// Built-in theme plus the operator's override, so the preview matches the
		// built site (§6.4). The override is validated on save; re-validated in
		// themeOverride() so a bad stored value falls back to the baseline.
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		css := themeCSS() + theme.FontCSS(c.state().build.Font)
		if ov := c.state().build.ThemeOverride; strings.TrimSpace(ov) != "" {
			css += "\n" + ov
		}
		w.Write([]byte(css))
	default:
		http.NotFound(w, r)
	}
}

// --- small helpers ---

func pageID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// reservedPrefixes and reservedExact are the URL namespaces the build owns; a
// page cannot live under them or it would collide with generated files.
var reservedPrefixes = []string{"/tags", "/media", "/assets", "/external", "/manifest", "/.well-known", "/search", "/feeds", "/unlock"}
var reservedExact = map[string]bool{"/version": true, "/build.json": true, "/classification": true,
	"/favicon.ico": true, "/favicon.svg": true, "/apple-touch-icon.png": true,
	"/icon-192.png": true, "/icon-512.png": true, "/site.webmanifest": true}

// reservedPathError returns a user-facing message if p collides with a build-
// reserved path, or "" if the path is fine.
func reservedPathError(p string) string {
	q := "/" + strings.Trim(p, "/")
	if q == "/" {
		return ""
	}
	reserved := reservedExact[q]
	if !reserved {
		for _, pre := range reservedPrefixes {
			if q == pre || strings.HasPrefix(q, pre+"/") {
				reserved = true
				break
			}
		}
	}
	if reserved {
		return "The path " + p + " is reserved by pbcssg (tags, media, assets, external, manifest, .well-known, search, feeds, unlock, classification, version, build.json). Choose another."
	}
	return ""
}

// pathErrorMessage maps a store path error to a friendly message (the SQLite
// UNIQUE violation on a duplicate path being the common case).
func pathErrorMessage(err error) string {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "That path is already used by another page."
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// pathSegmentRE matches one URL-clean path segment: lowercase letters, digits,
// and single hyphens (slug form). It rejects spaces, uppercase, dots (so "."/".."
// can never appear), and every other character that breaks URLs, the editor UI,
// or output-path safety.
var pathSegmentRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// pathInputError validates a (normalized) page path and returns a user-facing
// message when it is not URL-clean, or "" when it is fine (issue #1). The root
// "/" is allowed; otherwise every "/"-separated segment must be slug-like. This
// blocks spaces (which break several editor links and the built URL), path
// traversal ("..", via the dot ban), and control/encoding characters.
func pathInputError(p string) string {
	if p == "/" {
		return ""
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for _, seg := range segs {
		if !pathSegmentRE.MatchString(seg) {
			return "Paths may contain only lowercase letters, numbers, and hyphens, in “/”-separated segments — for example /about or /blog/my-post. Remove spaces and other characters."
		}
	}
	return ""
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// Canonicalize a trailing slash away (keeping the root "/") so "/foo/" and
	// "/foo" don't become distinct pages.
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
