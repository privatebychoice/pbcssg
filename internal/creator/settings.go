package creator

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
	"go.privatebychoice.com/pbcssg/internal/theme"
)

// localTestURL returns the operator's saved loopback base URL for testing gate
// links against a locally-served build (§6.10), or "" when none is set. It is
// editor-only state — never a build input — so it lives outside build.Config.
func (c *Creator) localTestURL() string {
	if v, ok, err := c.store.Setting(keyLocalTestURL); err == nil && ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// normalizeLocalTestURL validates and canonicalizes the local test base URL. Empty
// is valid (the feature is simply off). A non-empty value must be an absolute
// http(s) URL with a host; the trailing slash and any path are dropped so it can be
// concatenated with a page path cleanly (it is a base origin, e.g.
// http://127.0.0.1:8080).
func normalizeLocalTestURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("enter an absolute http(s) URL with a host, e.g. http://127.0.0.1:8080")
	}
	return u.Scheme + "://" + u.Host, nil
}

// parseNav parses the nav-links setting: one "Label | /path" per line. A line
// without a "|" separator (or with no destination) is skipped.
func parseNav(s string) []render.NavLink {
	var out []render.NavLink
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, "|")
		if i < 0 {
			continue
		}
		label := strings.TrimSpace(line[:i])
		href := strings.TrimSpace(line[i+1:])
		if href == "" {
			continue
		}
		if label == "" {
			label = href
		}
		out = append(out, render.NavLink{Label: label, Href: href})
	}
	return out
}

// navToText serializes nav links back to the "Label | /path" textarea form.
func navToText(nav []render.NavLink) string {
	var b strings.Builder
	for i, n := range nav {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(n.Label)
		b.WriteString(" | ")
		b.WriteString(n.Href)
	}
	return b.String()
}

// parseFeeds parses the feed-rules setting: "name | /glob/* | Optional Title |
// list" per line. The name is slugified; a line without a name or glob is
// skipped. The optional 4th field ("list"/"listed"/"yes"/"1"/"true") marks the
// feed to appear on the browsable /feeds/ index page (§6.5); to list a feed
// without a custom title, leave the title field empty ("name | /glob/* | | list").
func parseFeeds(s string) []build.FeedRule {
	var out []build.FeedRule
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 2 {
			continue
		}
		name := slugify(strings.TrimSpace(parts[0]))
		glob := strings.TrimSpace(parts[1])
		if name == "" || glob == "" {
			continue
		}
		fr := build.FeedRule{Name: name, Glob: glob}
		if len(parts) >= 3 {
			fr.Title = strings.TrimSpace(parts[2])
		}
		if len(parts) >= 4 {
			fr.Listed = parseFeedListed(strings.TrimSpace(parts[3]))
		}
		out = append(out, fr)
	}
	return out
}

// parseFeedListed reads the feed "listed" flag from its textarea column.
func parseFeedListed(s string) bool {
	switch strings.ToLower(s) {
	case "list", "listed", "yes", "y", "1", "true", "show":
		return true
	default:
		return false
	}
}

// feedsToText serializes feed rules back to the textarea form. A listed feed
// always emits the title column (empty if unset) so the trailing "list" flag
// stays in the 4th position.
func feedsToText(feeds []build.FeedRule) string {
	var b strings.Builder
	for i, f := range feeds {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(f.Name)
		b.WriteString(" | ")
		b.WriteString(f.Glob)
		if f.Title != "" || f.Listed {
			b.WriteString(" | ")
			b.WriteString(f.Title)
		}
		if f.Listed {
			b.WriteString(" | list")
		}
	}
	return b.String()
}

// Settings keys in the store's key/value settings table.
const (
	keySiteName         = "site.name"
	keyBaseURL          = "site.baseURL"
	keyFirstParty       = "site.firstParty"
	keyVersion          = "site.version"
	keyBuildNumber      = "site.buildNumber"
	keyGPC              = "site.gpcLastUpdate"
	keyLang             = "site.lang"
	keySearch           = "search.enabled"
	keySearchFull       = "search.fullText"
	keyOpenGraph        = "seo.openGraph"          // emit Open Graph tags
	keyOGImageDefault   = "seo.ogImageDefault"     // site-default social-preview image (§6.3)
	keySitemap          = "seo.sitemap"            // emit sitemap.xml + robots.txt (§6.3)
	keySitemapListings  = "seo.sitemapListings"    // include tag/feeds/classification pages in sitemap
	keyReadingTime      = "posts.readingTime"      // show "~N min read" on posts (§6.13)
	keyMetrics          = "metrics.enabled"        // opt into the server-mode private metrics dashboard (§7.7)
	keySecContact       = "security.contact"       // security.txt Contact lines (§7.6)
	keySecExpires       = "security.expires"       // security.txt Expires (RFC 3339)
	keySecEncryption    = "security.encryption"    // security.txt Encryption URL
	keySecPolicy        = "security.policy"        // security.txt Policy URL
	keySecAck           = "security.ack"           // security.txt Acknowledgments URL
	keySecLangs         = "security.langs"         // security.txt Preferred-Languages
	keyEmbedHosts       = "embed.hosts"            // allowlist of hosts embed blocks may frame (§5.8)
	keyNav              = "site.nav"               // primary nav links ("Label | /path" per line)
	keyFooterNav        = "site.footerNav"         // footer nav links ("Label | /path" per line)
	keyFeeds            = "feeds.rules"            // syndication feed rules ("name | /glob/* | Title")
	keyThemeVars        = "theme.vars"             // JSON of CSS-variable overrides (§6.4)
	keyThemeCustom      = "theme.customCSS"        // raw operator CSS block (§6.4)
	keyClassifyData     = "classify.domainsJSON"   // custom pbc-classification dataset (§5.7)
	keyClassifyReport   = "classify.report"        // publish /classification report details + JSON (§5.7)
	keyClassifyDataRepo = "classify.dataRepo"      // optional dataset repo URL shown on the report (§5.7)
	keySeeded           = "install.seeded"         // "1" once the first-run starter pages are created
	keyHeaderBrand      = "header.brand"           // none|text|logo|logotext (§6.4)
	keyHeaderAlign      = "header.align"           // start|center
	keyBrandText        = "header.brandText"       // wordmark override (blank = site name)
	keyLogoSrc          = "header.logoSrc"         // /media/<sha>.<ext>
	keyLogoSrcDark      = "header.logoSrcDark"     // optional dark-mode logo /media/<sha>.<ext> (§6.4)
	keyLogoAlt          = "header.logoAlt"         // logo alt text
	keyLogoHeight       = "header.logoHeight"      // small|medium|large
	keyFont             = "theme.font"             // body-font ID from theme.Fonts (§6.4)
	keyLocalTestURL     = "editor.localTestURL"    // editor-only: loopback base for the Key-groups "Local Test" gate link (§6.10)
	keyFaviconTheme     = "favicon.themeColor"     // <meta name=theme-color> / manifest colour for the favicon set (§6.11)
	keyKeepReleases     = "release.keep"           // how many versioned release dirs Publish retains (0 = keep all) (§7.4)
	keyTrustedProxies   = "metrics.trustedProxies" // CIDR allowlist whose XFF is trusted for metrics client-IP resolution (§7.7)
)

// loadBuildConfig overlays stored settings onto the seed (CLI) config for the
// editor's own runtime.
// LoadTrustedProxies returns the stored metrics trusted-proxy CIDR allowlist (entries
// separated by commas, whitespace, or newlines), or nil when unset — in which case the
// server's resolver falls back to loopback defaults. Exported for the server command,
// which builds the metrics client-IP resolver from it (§7.7).
func LoadTrustedProxies(st *store.Store) []string {
	if v, ok, err := st.Setting(keyTrustedProxies); err == nil && ok {
		return splitCIDRs(v)
	}
	return nil
}

// splitCIDRs splits a trusted-proxies field on commas/whitespace/newlines.
func splitCIDRs(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// invalidCIDRs returns the entries that are not valid CIDR prefixes.
func invalidCIDRs(list []string) []string {
	var bad []string
	for _, c := range list {
		if _, err := netip.ParsePrefix(c); err != nil {
			bad = append(bad, c)
		}
	}
	return bad
}

// trustedProxiesText returns the stored trusted-proxies field for the settings form
// (empty when unset — the placeholder then shows the loopback default).
func (c *Creator) trustedProxiesText() string {
	if v, ok, err := c.store.Setting(keyTrustedProxies); err == nil && ok {
		return v
	}
	return ""
}

// defaultKeepReleases is the retention default when the operator hasn't set one:
// keep the three most recent versioned release directories (§7.4).
const defaultKeepReleases = 3

// keepReleases returns how many versioned release directories a Publish retains on
// the host; 0 means keep all (pruning disabled). Defaults to defaultKeepReleases.
func (c *Creator) keepReleases() int {
	if v, ok, err := c.store.Setting(keyKeepReleases); err == nil && ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return defaultKeepReleases
}

func (c *Creator) loadBuildConfig(seed build.Config) build.Config {
	return LoadBuildConfig(c.store, seed)
}

// LoadBuildConfig overlays a store's saved settings onto the seed (CLI) config; a
// key absent from the store keeps the seed value, so first run uses CLI flags and
// later runs use whatever the operator saved. Exported so the headless
// `pbcssg build` produces the same effective config as the editor's Build button
// (embed-host allowlist, first-party domains, theme override, SEO/search toggles).
func LoadBuildConfig(st *store.Store, seed build.Config) build.Config {
	get := func(k, def string) string {
		if v, ok, err := st.Setting(k); err == nil && ok {
			return v
		}
		return def
	}
	bc := seed
	bc.SiteName = get(keySiteName, seed.SiteName)
	bc.BaseURL = get(keyBaseURL, seed.BaseURL)
	if v, ok, err := st.Setting(keyFirstParty); err == nil && ok {
		bc.FirstParty = splitList(v)
	}
	bc.Version = get(keyVersion, seed.Version)
	bc.BuildNumber = get(keyBuildNumber, seed.BuildNumber)
	bc.GPCLastUpdate = get(keyGPC, seed.GPCLastUpdate)
	bc.Lang = get(keyLang, seed.Lang)
	bc.Search = parseBool(get(keySearch, boolStr(seed.Search)))
	bc.SearchFullText = parseBool(get(keySearchFull, boolStr(seed.SearchFullText)))
	bc.OpenGraph = parseBool(get(keyOpenGraph, boolStr(seed.OpenGraph)))
	bc.OGImageDefault = get(keyOGImageDefault, seed.OGImageDefault)
	bc.Sitemap = parseBool(get(keySitemap, boolStr(seed.Sitemap)))
	bc.ShowReadingTime = parseBool(get(keyReadingTime, boolStr(seed.ShowReadingTime)))
	bc.Metrics = parseBool(get(keyMetrics, boolStr(seed.Metrics)))
	bc.ErrorPages = loadErrorPages(st)
	if v := get(keySecContact, strings.Join(seed.SecurityContacts, "\n")); strings.TrimSpace(v) != "" {
		bc.SecurityContacts = splitLines(v)
	}
	bc.SecurityExpires = get(keySecExpires, seed.SecurityExpires)
	bc.SecurityEncryption = get(keySecEncryption, seed.SecurityEncryption)
	bc.SecurityPolicy = get(keySecPolicy, seed.SecurityPolicy)
	bc.SecurityAcknowledgments = get(keySecAck, seed.SecurityAcknowledgments)
	bc.SecurityLanguages = get(keySecLangs, seed.SecurityLanguages)
	// Listing pages default to included when sitemap is on; a stored value overrides.
	bc.SitemapListings = parseBool(get(keySitemapListings, boolStr(true)))
	if v, ok, err := st.Setting(keyEmbedHosts); err == nil && ok {
		bc.EmbedHosts = splitList(v)
	}
	if v, ok, err := st.Setting(keyNav); err == nil && ok {
		bc.Nav = parseNav(v)
	}
	if v, ok, err := st.Setting(keyFooterNav); err == nil && ok {
		bc.FooterNav = parseNav(v)
	}
	if v, ok, err := st.Setting(keyFeeds); err == nil && ok {
		bc.Feeds = parseFeeds(v)
	}
	if v, ok, err := st.Setting(keyClassifyData); err == nil && ok && strings.TrimSpace(v) != "" {
		bc.ClassifyData = []byte(v)
	}
	bc.ClassifyReport = parseBool(get(keyClassifyReport, boolStr(seed.ClassifyReport)))
	bc.ClassifyDataRepoURL = get(keyClassifyDataRepo, seed.ClassifyDataRepoURL)
	bc.HeaderBrand = get(keyHeaderBrand, seed.HeaderBrand)
	bc.HeaderAlign = get(keyHeaderAlign, seed.HeaderAlign)
	bc.BrandText = get(keyBrandText, seed.BrandText)
	bc.LogoSrc = get(keyLogoSrc, seed.LogoSrc)
	bc.LogoSrcDark = get(keyLogoSrcDark, seed.LogoSrcDark)
	bc.LogoAlt = get(keyLogoAlt, seed.LogoAlt)
	bc.LogoHeight = get(keyLogoHeight, seed.LogoHeight)
	bc.Font = get(keyFont, seed.Font)
	bc.FaviconThemeColor = faviconThemeFromStore(st)
	bc.ThemeOverride = themeOverride(st)
	return bc
}

// saveBuildConfig persists the site config to the settings table.
func (c *Creator) saveBuildConfig(bc build.Config) error {
	pairs := [][2]string{
		{keySiteName, bc.SiteName},
		{keyBaseURL, bc.BaseURL},
		{keyFirstParty, strings.Join(bc.FirstParty, ", ")},
		{keyVersion, bc.Version},
		{keyBuildNumber, bc.BuildNumber},
		{keyGPC, bc.GPCLastUpdate},
		{keyLang, bc.Lang},
		{keySearch, boolStr(bc.Search)},
		{keySearchFull, boolStr(bc.SearchFullText)},
		{keyOpenGraph, boolStr(bc.OpenGraph)},
		{keyOGImageDefault, bc.OGImageDefault},
		{keySitemap, boolStr(bc.Sitemap)},
		{keySitemapListings, boolStr(bc.SitemapListings)},
		{keyReadingTime, boolStr(bc.ShowReadingTime)},
		{keyMetrics, boolStr(bc.Metrics)},
		{keySecContact, strings.Join(bc.SecurityContacts, "\n")},
		{keySecExpires, bc.SecurityExpires},
		{keySecEncryption, bc.SecurityEncryption},
		{keySecPolicy, bc.SecurityPolicy},
		{keySecAck, bc.SecurityAcknowledgments},
		{keySecLangs, bc.SecurityLanguages},
		{keyEmbedHosts, strings.Join(bc.EmbedHosts, ", ")},
		{keyNav, navToText(bc.Nav)},
		{keyFooterNav, navToText(bc.FooterNav)},
		{keyFeeds, feedsToText(bc.Feeds)},
		{keyClassifyReport, boolStr(bc.ClassifyReport)},
		{keyClassifyDataRepo, bc.ClassifyDataRepoURL},
		{keyHeaderBrand, bc.HeaderBrand},
		{keyHeaderAlign, bc.HeaderAlign},
		{keyBrandText, bc.BrandText},
		{keyLogoSrc, bc.LogoSrc},
		{keyLogoSrcDark, bc.LogoSrcDark},
		{keyLogoAlt, bc.LogoAlt},
		{keyLogoHeight, bc.LogoHeight},
		{keyFont, bc.Font},
	}
	for _, p := range pairs {
		if err := c.store.SetSetting(p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// invalidEmbedHosts returns the operator embed-host entries that are not clean
// hostnames — used to reject a settings save before a bad value can reach the
// served-site CSP frame-src (§5.8).
func invalidEmbedHosts(hosts []string) []string {
	var bad []string
	for _, h := range hosts {
		if strings.TrimSpace(h) == "" {
			continue
		}
		if !build.ValidEmbedHost(h) {
			bad = append(bad, strings.TrimSpace(h))
		}
	}
	return bad
}

// headerBrandError validates the header-brand settings on save: a logo style
// needs a Media-library logo that exists, and a logo shown on its own needs alt
// text (accessibility). Returns "" when the config is fine.
func (c *Creator) headerBrandError(bc build.Config) string {
	b := bc.Brand()
	if b.Mode == "logo" || b.Mode == "logotext" {
		if b.LogoSrc == "" {
			return "Pick a logo image from the Media library for the header (or set Brand style to Text or None)."
		}
		refs := render.MediaRefs([]byte(b.LogoSrc))
		if len(refs) == 0 {
			return "The logo image must be a Media-library path, e.g. /media/<sha>.<ext>."
		}
		if ok, err := c.store.MediaExists(refs[0].SHA); err == nil && !ok {
			return "That logo image is not in the Media library — upload it first, then use its /media/… path."
		}
		// The dark-mode logo is optional, but when set it must be a real media path.
		if b.LogoSrcDark != "" {
			dr := render.MediaRefs([]byte(b.LogoSrcDark))
			if len(dr) == 0 {
				return "The dark-mode logo must be a Media-library path, e.g. /media/<sha>.<ext>."
			}
			if ok, err := c.store.MediaExists(dr[0].SHA); err == nil && !ok {
				return "That dark-mode logo is not in the Media library — upload it first, then use its /media/… path."
			}
		}
	}
	if b.Mode == "logo" && b.LogoAlt == "" {
		return "Add alt text for the logo — a logo shown on its own needs a text alternative for screen readers."
	}
	return ""
}

// configFromForm reads a build.Config from the settings form.
func configFromForm(r formValuer) build.Config {
	return build.Config{
		SiteName:   strings.TrimSpace(r.FormValue("siteName")),
		BaseURL:    strings.TrimSpace(r.FormValue("baseURL")),
		FirstParty: splitList(r.FormValue("firstParty")),
		Version:    strings.TrimSpace(r.FormValue("version")),
		// BuildNumber is not an editable settings field (it auto-increments on
		// Package release); handleSaveSettings preserves the current value.
		GPCLastUpdate:           strings.TrimSpace(r.FormValue("gpc")),
		Lang:                    strings.TrimSpace(r.FormValue("lang")),
		Search:                  r.FormValue("search") != "",
		SearchFullText:          r.FormValue("searchFullText") != "",
		OpenGraph:               r.FormValue("openGraph") != "",
		OGImageDefault:          strings.TrimSpace(r.FormValue("ogImageDefault")),
		Sitemap:                 r.FormValue("sitemap") != "",
		SitemapListings:         r.FormValue("sitemapListings") != "",
		ShowReadingTime:         r.FormValue("readingTime") != "",
		Metrics:                 r.FormValue("metrics") != "",
		SecurityContacts:        splitLines(r.FormValue("secContact")),
		SecurityExpires:         securityExpires(r.FormValue("secExpires"), splitLines(r.FormValue("secContact"))),
		SecurityEncryption:      strings.TrimSpace(r.FormValue("secEncryption")),
		SecurityPolicy:          strings.TrimSpace(r.FormValue("secPolicy")),
		SecurityAcknowledgments: strings.TrimSpace(r.FormValue("secAck")),
		SecurityLanguages:       strings.TrimSpace(r.FormValue("secLangs")),
		EmbedHosts:              splitLines(r.FormValue("embedHosts")),
		Nav:                     parseNav(r.FormValue("nav")),
		FooterNav:               parseNav(r.FormValue("footerNav")),
		Feeds:                   parseFeeds(r.FormValue("feeds")),
		ClassifyReport:          r.FormValue("classifyReport") != "",
		ClassifyDataRepoURL:     strings.TrimSpace(r.FormValue("classifyDataRepo")),
		HeaderBrand:             strings.TrimSpace(r.FormValue("headerBrand")),
		HeaderAlign:             strings.TrimSpace(r.FormValue("headerAlign")),
		BrandText:               strings.TrimSpace(r.FormValue("brandText")),
		LogoSrc:                 strings.TrimSpace(r.FormValue("logoSrc")),
		LogoSrcDark:             strings.TrimSpace(r.FormValue("logoSrcDark")),
		LogoAlt:                 strings.TrimSpace(r.FormValue("logoAlt")),
		LogoHeight:              strings.TrimSpace(r.FormValue("logoHeight")),
		Font:                    normalizeFont(r.FormValue("font")),
	}
}

// securityExpires normalizes the security.txt Expires field (§7.6): a provided value
// is normalized to RFC 3339 (an invalid one is returned as-is so handleSaveSettings
// rejects it); a blank one is defaulted to one year out when at least one Contact is
// set, so the emitted file is RFC 9116-valid without the operator tracking a date. The
// default is stamped here (at save) so the build stays deterministic (no wall clock).
func securityExpires(raw string, contacts []string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if len(contacts) > 0 {
			return time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339)
		}
		return ""
	}
	if norm, ok := build.NormalizeSecurityExpires(raw); ok {
		return norm
	}
	return raw
}

// normalizeFont maps a submitted body-font value to a known allowlist ID,
// defaulting to "system" — so only a recognized ID is ever stored/applied.
func normalizeFont(s string) string {
	s = strings.TrimSpace(s)
	if theme.ValidFont(s) {
		return s
	}
	return "system"
}

// formValuer is satisfied by *http.Request; declared so config parsing is
// trivially unit-testable.
type formValuer interface{ FormValue(string) string }

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func parseBool(s string) bool {
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "on")
}
