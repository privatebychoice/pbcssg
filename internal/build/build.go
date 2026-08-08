// Package build is the pbcssg build engine (SPEC §6): it turns the published
// content in the store into a static bundle. For each published page it renders
// the content, applies linking hygiene + auto-rewrites, scans and classifies the
// external references, and writes the page HTML plus its privacy manifest. It
// then writes the site-level manifest, the GPC declaration, and build.json (the
// self-describing bundle metadata: version, build number, and per-file hashes).
//
// This is where the privacy pipeline (linkscan → hygiene → manifest) composes
// with the content store and renderer. The build is deterministic and fully
// offline: no third-party URL is fetched (SPEC §5.4).
package build

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/asset"
	"go.privatebychoice.com/pbcssg/internal/feed"
	"go.privatebychoice.com/pbcssg/internal/hygiene"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
	"go.privatebychoice.com/pbcssg/internal/manifest"
	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/search"
	"go.privatebychoice.com/pbcssg/internal/store"
	"go.privatebychoice.com/pbcssg/internal/theme"
)

// Config is the site-level build configuration (sourced from store settings by
// the caller).
type Config struct {
	SiteName      string   // shown in <title>/footer
	BaseURL       string   // absolute site URL, e.g. https://theuntrackedlife.example
	FirstParty    []string // the operator's *other* own domains (base host is implicit)
	Version       string   // semantic version, e.g. "1.0"
	BuildNumber   string   // incremented per deploy (SPEC §6)
	GPCLastUpdate string   // YYYY-MM-DD for /.well-known/gpc.json
	Lang          string   // default "en"

	// security.txt fields (RFC 9116, §7.6). SecurityContacts (mailto:/tel:/https URIs)
	// is required to emit /.well-known/security.txt; SecurityExpires is an RFC 3339
	// timestamp (the editor defaults it to +1 year). The rest are optional.
	SecurityContacts        []string
	SecurityExpires         string
	SecurityEncryption      string
	SecurityPolicy          string
	SecurityAcknowledgments string
	SecurityLanguages       string // Preferred-Languages, e.g. "en, de"

	Search         bool // emit the client-side search index + widget (§6.2)
	SearchFullText bool // index full body text, not just headings + summary
	OpenGraph      bool // emit Open Graph (og:) tags on pages (SEO/link previews)
	// OGImageDefault is the site-wide social-preview image (a /media/<sha>.<ext> path or
	// absolute URL) used for a page that sets none (§6.3). Needs a BaseURL to become the
	// absolute og:image URL; empty means no default.
	OGImageDefault string
	// Sitemap emits /sitemap.xml and /robots.txt (the latter pointing at the
	// sitemap). Needs a BaseURL for absolute <loc>s; enabled but unset base is
	// skipped with a warning. SitemapListings additionally includes the generated
	// listing pages (tags, feeds index, classification); content pages are always
	// included (minus any marked noindex). (§6.3)
	Sitemap         bool
	SitemapListings bool

	// ShowReadingTime renders "~N min read" under the title of pages marked as posts
	// (Content.IsPost), estimated from word count. Default off. (§6.13)
	ShowReadingTime bool

	// Metrics writes the "metrics" master switch into build.json, opting the bundle
	// into server mode's private, loopback-only metrics dashboard (§7.7). Default
	// off. It changes no page output — only what server mode may collect and serve.
	Metrics bool

	// ErrorPages holds per-page operator Markdown for the themed error pages (§7.8),
	// keyed by ErrorPage.Name (e.g. "404"). A missing or blank entry falls back to
	// the built-in default, so a headless build always emits complete pages.
	ErrorPages map[string]string

	// EmbedHosts is the operator's allowlist of hosts that generic embed blocks may
	// frame (e.g. "peertube.example"). The build only renders an embed whose URL
	// host is on this list, and writes the corresponding https origins into
	// build.json so server mode's CSP frame-src permits exactly those (SPEC §5.8).
	EmbedHosts []string

	// Nav is the site-wide primary navigation, rendered in the header of every
	// built page (configured in Settings). FooterNav is the footer link row.
	Nav       []render.NavLink
	FooterNav []render.NavLink

	// Header brand (§6.4): a text wordmark, a self-hosted logo image, or both.
	// HeaderBrand is one of none|text|logo|logotext (empty defaults to "text");
	// HeaderAlign is start|center. BrandText overrides the wordmark (blank = site
	// name). LogoSrc is a Media-library path (/media/<sha>.<ext>); LogoAlt is its
	// alt text; LogoHeight is small|medium|large. LogoSrcDark is an optional second
	// logo shown in dark mode (empty = LogoSrc is used for both themes).
	HeaderBrand string
	HeaderAlign string
	BrandText   string
	LogoSrc     string
	LogoSrcDark string
	LogoAlt     string
	LogoHeight  string

	// FaviconThemeColor is the optional <meta name="theme-color"> / manifest colour
	// for the site's favicon set (§6.11), e.g. "#0d9488". Blank omits the meta tag
	// and defaults the manifest colours to white. The favicon assets themselves are
	// read from the store at build time (not carried in Config).
	FaviconThemeColor string

	// Year is the copyright year shown in the footer, stamped by the caller (the
	// editor / CLI) at build time so the build package itself stays deterministic.
	Year int

	// Feeds are the operator's syndication-feed rules (configured in Settings): a
	// name + a path glob; published pages whose path matches the glob are emitted
	// into /feeds/<name>.rss and /feeds/<name>.atom (SPEC §6.5).
	Feeds []FeedRule

	// Font is the operator's body-font choice — an ID from theme.Fonts (empty =
	// "system"). The build layers the chosen stack over the built-in --font-sans;
	// only allowlisted stacks are ever emitted, so no operator text reaches the CSS.
	Font string

	// ThemeOverride is operator CSS layered over the built-in theme (§6.4). It is
	// appended after theme.CSS so the default remains the fallback baseline. The
	// caller (editor) validates it forbids external url()/@import before setting it.
	ThemeOverride string

	// ClassifyData is an optional custom pbc-classification dataset (domains.json
	// bytes) supplied by the operator. It is merged over the library's embedded
	// defaults (later entries win), so the operator can add or override domain
	// classifications. Empty means the library defaults only. When present it is
	// also published into the bundle at /.well-known/pbc-classification/domains.json
	// for transparency (§5.7).
	ClassifyData []byte

	// ClassifyReport, when true, publishes the *details* on the /classification
	// report page — the per-domain "Classifications used" listing and a link to the
	// dataset — and publishes the raw domains.json. When false the report page
	// still explains the rating system and carries the disclaimer, but exposes no
	// dataset. ClassifyDataRepoURL is an optional link to the operator's dataset
	// repository, shown on the report. (§5.7)
	ClassifyReport      bool
	ClassifyDataRepoURL string
}

// Brand resolves the operator's header-brand settings into a render.Brand: it
// normalizes the mode/alignment/height to known values (empty mode defaults to a
// text wordmark, §6.4), and resolves the wordmark text to the BrandText override
// or, failing that, the site name.
func (c Config) Brand() render.Brand {
	mode := strings.ToLower(strings.TrimSpace(c.HeaderBrand))
	switch mode {
	case "none", "text", "logo", "logotext":
	default:
		mode = "text" // default: a wordmark from the site name
	}
	align := "start"
	if strings.EqualFold(strings.TrimSpace(c.HeaderAlign), "center") {
		align = "center"
	}
	height := strings.ToLower(strings.TrimSpace(c.LogoHeight))
	switch height {
	case "small", "medium", "large":
	default:
		height = "medium"
	}
	text := strings.TrimSpace(c.BrandText)
	if text == "" {
		text = c.SiteName
	}
	return render.Brand{
		Mode: mode, Align: align, Text: text,
		LogoSrc: strings.TrimSpace(c.LogoSrc), LogoSrcDark: strings.TrimSpace(c.LogoSrcDark),
		LogoAlt: strings.TrimSpace(c.LogoAlt), LogoHeight: height,
	}
}

// FeedRule defines one syndication feed: pages whose path matches Glob are
// syndicated to /feeds/<Name>.rss and /feeds/<Name>.atom. Glob is a prefix match
// when it ends in "*" (e.g. "/blog/*" matches everything under /blog/), otherwise
// an exact path match.
type FeedRule struct {
	Name   string
	Glob   string
	Title  string // optional channel title; defaults to "<SiteName> — <Name>"
	Listed bool   // list this feed on the browsable /feeds/ index page (§6.5)
}

// matches reports whether a page path belongs to this feed rule.
func (fr FeedRule) matches(p string) bool {
	if strings.HasSuffix(fr.Glob, "*") {
		return strings.HasPrefix(p, strings.TrimSuffix(fr.Glob, "*"))
	}
	return fr.Glob == p
}

// feedPage is the minimal per-page info collected during the build for feeds.
type feedPage struct {
	path, title, summary, canonical string
	updated                         time.Time
}

// PageReport summarizes one built page.
type PageReport struct {
	Path       string
	OutputFile string
	WorstGrade string
	Externals  int
	Rewrites   int
	Warnings   []string
}

// Report summarizes a build.
type Report struct {
	Pages    []PageReport
	Files    []string // every file written, relative to the output dir, sorted
	Warnings []string // build-wide warnings (e.g. referenced media not in the store)
}

// Run builds the published content in s into a static bundle under outDir.
func Run(s *store.Store, cfg Config, outDir string) (*Report, error) {
	if cfg.Lang == "" {
		cfg.Lang = "en"
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("build: base URL %q: %w", cfg.BaseURL, err)
	}
	copts := []classify.Option{classify.WithFirstParty(cfg.FirstParty...)}
	if len(cfg.ClassifyData) > 0 {
		// Merge the operator's custom dataset over the library's embedded defaults.
		copts = append(copts, classify.WithDataBytes(cfg.ClassifyData))
	}
	classifier, err := classify.New(copts...)
	if err != nil {
		return nil, fmt.Errorf("build: classifier: %w", err)
	}

	// The output directory is fully managed by the build: clear it first so
	// unpublished, deleted, or renamed pages leave no stale files behind (which
	// would otherwise be served and packaged into the release tarball).
	if err := cleanOutputDir(outDir); err != nil {
		return nil, err
	}

	b := &builder{
		cfg:        cfg,
		store:      s,
		outDir:     outDir,
		classifier: classifier,
		scanner:    linkscan.NewScanner(classifier, base),
		hcfg:       hygiene.Config{Base: base, FirstParty: firstPartyPred(cfg.FirstParty)},
		sb:         manifest.NewBuilder(),
		hashes:     map[string]string{},
		media:      map[string]*mediaUsage{},
		tags:       map[string]*tagInfo{},
		embedHosts: hostSet(cfg.EmbedHosts),
		frameSrc:   embedOrigins(cfg.EmbedHosts),
		brand:      cfg.Brand(),
		report:     &Report{},
	}

	// Fingerprinted shared assets, emitted before pages so pages reference their
	// hashed URLs. The operator's theme override (if any) is layered over the
	// built-in theme, which stays the fallback baseline (§6.4).
	// theme.CSS holds the built-in --font-sans default; the operator's font choice
	// layers over it (allowlisted stack only), then their custom CSS wins last.
	themeCSS := theme.CSS + theme.FontCSS(cfg.Font)
	if strings.TrimSpace(cfg.ThemeOverride) != "" {
		themeCSS += "\n/* --- operator theme override (settings §6.4) --- */\n" + cfg.ThemeOverride + "\n"
	}
	themeName, themeHref := fingerprint(theme.Path, []byte(themeCSS))
	if err := b.write(themeName, []byte(themeCSS)); err != nil {
		return nil, err
	}
	b.cssHref = themeHref
	// The light/dark theme script ships on every page (blocking in <head> so a
	// stored choice applies before first paint), so emit it unconditionally.
	themeJSName, themeJSHref := fingerprint(render.ThemeJSPath, []byte(render.ThemeJS))
	if err := b.write(themeJSName, []byte(render.ThemeJS)); err != nil {
		return nil, err
	}
	b.themeJSHref = themeJSHref
	if cfg.Search {
		name, href := fingerprint(render.SearchJSPath, []byte(search.ClientJS))
		if err := b.write(name, []byte(search.ClientJS)); err != nil {
			return nil, err
		}
		b.searchJSHref = href
	}

	// Load the key groups once so gated blocks resolve their aliases to KEKs without
	// a per-page query (SPEC §6.10). nil when there are no groups.
	keks, err := s.KEKsByAlias()
	if err != nil {
		return nil, fmt.Errorf("build: load key groups: %w", err)
	}
	b.keks = keks

	// Favicon set (§6.11): emit the operator's uploaded icons at their canonical root
	// paths + a generated manifest, and record which <head> links every page emits.
	if err := b.emitFavicons(s); err != nil {
		return nil, err
	}

	pages, err := s.Published()
	if err != nil {
		return nil, fmt.Errorf("build: load pages: %w", err)
	}
	// First pass: build the site-wide page index so index blocks can list pages.
	for _, p := range pages {
		c, _ := render.Parse(p.ContentJSON)
		b.pageIndex = append(b.pageIndex, render.PageRef{
			Path: p.Path, Title: p.Title, Summary: c.Summary,
			Date: p.UpdatedAt.Format("2006-01-02"), Time: p.UpdatedAt,
			// An unlisted page (§6.16) implies both noindex and list-exclude, so it
			// drops out of page-index blocks and related-posts automatically.
			IsIndex: c.IsIndex, Exclude: c.ListExclude || c.Unlisted,
			IsPost: c.IsPost, Tags: c.Tags, NoIndex: c.NoIndex || c.Unlisted,
		})
	}
	for _, p := range pages {
		if err := b.buildPage(p); err != nil {
			return nil, err
		}
	}

	// Emit a generic deposit page for each key group without an authored splash page,
	// so every group has a working gate link even before a custom splash is set (§6.10).
	if err := b.emitGateFallbacks(); err != nil {
		return nil, err
	}

	// Emit only the media actually referenced by the built pages, re-verifying
	// each is metadata-clean before it is published (SPEC §6.1).
	if err := b.emitMedia(s); err != nil {
		return nil, err
	}

	// Emit the browsable tag pages (/tags/ and /tags/<slug>/).
	if err := b.emitTags(); err != nil {
		return nil, err
	}

	// Emit syndication feeds (/feeds/<name>.rss + .atom) from the collected pages.
	if err := b.emitFeeds(); err != nil {
		return nil, err
	}

	// Emit the user-facing /classification report (always; details are gated).
	if err := b.emitClassificationReport(); err != nil {
		return nil, err
	}

	// Emit the themed error pages (404/403/429/50x/…) at the bundle root (§7.8).
	if err := b.emitErrorPages(); err != nil {
		return nil, err
	}

	// Warn about dangling local nav/footer links (validated against real output).
	b.validateNavLinks()

	if cfg.Search {
		idx, err := search.Encode(b.docs)
		if err != nil {
			return nil, err
		}
		// The index is fetched by the client at a fixed path, so it is not
		// fingerprinted (the search script is).
		if err := b.write(search.IndexPath, idx); err != nil {
			return nil, err
		}
	}

	siteJSON, err := manifest.Encode(b.sb.Site())
	if err != nil {
		return nil, err
	}
	if err := b.write("manifest/site.json", siteJSON); err != nil {
		return nil, err
	}
	if err := b.write(".well-known/gpc.json", gpcJSON(cfg.GPCLastUpdate)); err != nil {
		return nil, err
	}
	// security.txt (RFC 9116, §7.6): emitted only when a Contact is set.
	if sec := securityTxt(cfg); sec != nil {
		if err := b.write(".well-known/security.txt", sec); err != nil {
			return nil, err
		}
	}
	// Publish the operator's custom classification dataset for transparency, so the
	// data behind the per-page external-references grades is inspectable — gated by
	// the same report-details toggle that shows it on /classification (§5.7).
	if cfg.ClassifyReport && len(cfg.ClassifyData) > 0 {
		if err := b.write(".well-known/pbc-classification/domains.json", cfg.ClassifyData); err != nil {
			return nil, err
		}
	}
	// sitemap.xml + robots.txt, after every indexable page has been collected.
	if err := b.writeSitemap(); err != nil {
		return nil, err
	}
	if err := b.writeBuildJSON(); err != nil {
		return nil, err
	}

	sort.Strings(b.report.Files)
	return b.report, nil
}

type builder struct {
	cfg         Config
	store       *store.Store
	outDir      string
	classifier  *classify.Classifier
	scanner     *linkscan.Scanner
	hcfg        hygiene.Config
	sb          *manifest.Builder
	hashes      map[string]string
	media       map[string]*mediaUsage // bundle-relative media path -> content hash + referencing pages
	tags        map[string]*tagInfo    // tag slug -> tag + pages carrying it
	embedHosts  map[string]bool        // allowlisted hosts generic embeds may frame
	frameSrc    []string               // https origins written to build.json for CSP frame-src
	feedPages   []feedPage             // per-page info for syndication feeds
	pageIndex   []render.PageRef       // site-wide page list for index blocks
	sitemap     []sitemapURL           // indexable page URLs for sitemap.xml (§6.3)
	report      *Report
	wroteFacade bool
	docs        []search.Document

	wroteReveal   bool
	wroteGate     bool
	wroteCodeCopy bool
	wroteShare    bool

	keks    map[string][]byte   // key-group alias -> KEK, loaded once for group-gated blocks (§6.10)
	favicon render.FaviconLinks // which favicon <head> links to emit on every page (§6.11)

	brand          render.Brand // resolved header brand (every page)
	cssHref        string       // fingerprinted theme stylesheet
	themeJSHref    string       // fingerprinted light/dark theme script (every page)
	searchJSHref   string       // fingerprinted search script
	facadeHref     string       // fingerprinted youtube facade script
	revealJSHref   string       // fingerprinted deferred-reveal decode script (§6.9)
	gateJSHref     string       // fingerprinted group-gate keyring/decode script (§6.10)
	codeCopyJSHref string       // fingerprinted code-block copy-button script (§6.12)
	shareJSHref    string       // fingerprinted share-block script (§6.15)
}

// mediaUsage records a referenced media asset's content hash and every page path
// that references it, so a broken reference can name the offending page(s).
type mediaUsage struct {
	sha   string
	pages []string
}

// tagInfo accumulates the pages carrying one tag, for its /tags/<slug>/ page.
type tagInfo struct {
	name  string
	pages []render.PageLink
}

// pageOpts builds the shared render options for engine-generated pages (tag
// pages, etc.) so they match the site's theme, search, and SEO settings.
func (b *builder) pageOpts(title, path string) render.Options {
	return render.Options{
		Title:        title,
		SiteName:     b.cfg.SiteName,
		BuildNumber:  b.cfg.BuildNumber,
		Lang:         b.cfg.Lang,
		Search:       b.cfg.Search,
		CSSHref:      b.cssHref,
		ThemeJSHref:  b.themeJSHref,
		Brand:        b.brand,
		SearchJSHref: b.searchJSHref,
		OpenGraph:    b.cfg.OpenGraph,
		OGImage:      b.ogImageURL(""), // listing pages use the site-default preview image
		CanonicalURL: b.canonical(path),
		Nav:          b.cfg.Nav,
		FooterNav:    b.cfg.FooterNav,
		Year:         b.cfg.Year,
		Favicon:      b.favicon,
	}
}

// emitTags writes the browsable tag pages: /tags/<slug>/ for each tag and a
// /tags/ index. Tag pages carry only internal links, so they pass the hygiene
// and scan steps cleanly.
func (b *builder) emitTags() error {
	if len(b.tags) == 0 {
		return nil
	}
	slugs := make([]string, 0, len(b.tags))
	for slug := range b.tags {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	index := make([]render.TagLink, 0, len(slugs))
	for _, slug := range slugs {
		ti := b.tags[slug]
		sort.Slice(ti.pages, func(i, j int) bool { return ti.pages[i].Title < ti.pages[j].Title })
		tagPath := "/tags/" + slug
		html, err := render.TagPage(ti.name, ti.pages, b.pageOpts("Tag: "+ti.name, tagPath))
		if err != nil {
			return err
		}
		if err := b.emitPage(tagPath, html, nil); err != nil {
			return err
		}
		b.addSitemapListing(tagPath)
		index = append(index, render.TagLink{Name: ti.name, Href: "/tags/" + slug + "/", Count: len(ti.pages)})
	}
	sort.Slice(index, func(i, j int) bool { return index[i].Name < index[j].Name })

	html, err := render.TagsIndex(index, b.pageOpts("Tags", "/tags"))
	if err != nil {
		return err
	}
	if err := b.emitPage("/tags", html, nil); err != nil {
		return err
	}
	b.addSitemapListing("/tags")
	return nil
}

// collectFeedPage records a page for every feed rule whose glob it matches and
// returns the feed auto-discovery links for that page's <head>.
func (b *builder) collectFeedPage(p store.PublishedPage, summary string) []render.FeedLink {
	// An unlisted page (§6.16) is never syndicated, even if it matches a feed glob —
	// a public feed would otherwise disclose its title and URL.
	if c, _ := render.Parse(p.ContentJSON); c.Unlisted {
		return nil
	}
	var links []render.FeedLink
	matched := false
	for _, fr := range b.cfg.Feeds {
		if fr.Name == "" || !fr.matches(p.Path) {
			continue
		}
		matched = true
		links = append(links,
			render.FeedLink{Title: b.feedTitle(fr), Href: "/feeds/" + fr.Name + ".rss", Type: "application/rss+xml"},
			render.FeedLink{Title: b.feedTitle(fr) + " (Atom)", Href: "/feeds/" + fr.Name + ".atom", Type: "application/atom+xml"},
		)
	}
	if matched {
		b.feedPages = append(b.feedPages, feedPage{
			path: p.Path, title: p.Title, summary: summary,
			canonical: b.canonical(p.Path), updated: p.UpdatedAt,
		})
	}
	return links
}

func (b *builder) feedTitle(fr FeedRule) string {
	if strings.TrimSpace(fr.Title) != "" {
		return fr.Title
	}
	if b.cfg.SiteName != "" {
		return b.cfg.SiteName + " — " + fr.Name
	}
	return fr.Name
}

// emitFeeds writes an RSS 2.0 and an Atom 1.0 file per feed rule from the pages
// collected during the build. Feeds need absolute URLs, so they are skipped (with
// a warning) when no base URL is configured. Items are capped and sorted
// newest-first by the feed package.
func (b *builder) emitFeeds() error {
	if len(b.cfg.Feeds) == 0 {
		return nil
	}
	base := strings.TrimRight(b.cfg.BaseURL, "/")
	if base == "" {
		b.report.Warnings = append(b.report.Warnings, "feeds skipped: no base URL configured (feeds need absolute links)")
		return nil
	}
	const maxItems = 50
	var listed []render.FeedInfo
	for _, fr := range b.cfg.Feeds {
		if fr.Name == "" {
			continue
		}
		if fr.Listed {
			listed = append(listed, render.FeedInfo{
				Title:    b.feedTitle(fr),
				RSSHref:  "/feeds/" + fr.Name + ".rss",
				AtomHref: "/feeds/" + fr.Name + ".atom",
			})
		}
		var items []feed.Item
		for _, fp := range b.feedPages {
			if fr.matches(fp.path) {
				items = append(items, feed.Item{
					Title: fp.title, Link: fp.canonical, Description: fp.summary, Published: fp.updated,
				})
			}
		}
		// Cap after sorting newest-first (the feed package sorts).
		ch := feed.Channel{
			Title:       b.feedTitle(fr),
			Link:        base + "/",
			Description: b.feedTitle(fr),
			Items:       items,
		}
		sortNewestFirst(ch.Items)
		if len(ch.Items) > maxItems {
			ch.Items = ch.Items[:maxItems]
		}

		ch.SelfLink = base + "/feeds/" + fr.Name + ".rss"
		rssBytes, err := feed.RSS(ch)
		if err != nil {
			return err
		}
		if err := b.write("feeds/"+fr.Name+".rss", rssBytes); err != nil {
			return err
		}
		ch.SelfLink = base + "/feeds/" + fr.Name + ".atom"
		atomBytes, err := feed.Atom(ch)
		if err != nil {
			return err
		}
		if err := b.write("feeds/"+fr.Name+".atom", atomBytes); err != nil {
			return err
		}
	}

	// A browsable /feeds/ index listing the feeds the operator chose to surface.
	// Emitted here so it shares the base-URL guard above (the links it points at
	// only exist once the feeds are written).
	if len(listed) > 0 {
		html, err := render.FeedsIndex(listed, b.pageOpts("Feeds", "/feeds"))
		if err != nil {
			return err
		}
		if err := b.emitPage("/feeds", html, nil); err != nil {
			return err
		}
		b.addSitemapListing("/feeds")
	}
	return nil
}

func sortNewestFirst(items []feed.Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Published.After(items[j].Published) })
}

// classifyGradeOrder is the rating scale, best→worst, for the report legend and
// the summary tallies.
var classifyGradeOrder = []classify.Grade{
	classify.GradeA, classify.GradeB, classify.GradeC, classify.GradeD, classify.GradeF, classify.GradeUnclassified,
}

// emitClassificationReport writes the user-facing /classification page. It is
// always emitted (the lite form explains the rating system + disclaimer); the
// per-domain "Classifications used" listing, the dataset summary, and the
// domains.json link are included only when ClassifyReport is enabled (§5.7).
func (b *builder) emitClassificationReport() error {
	rep := render.ClassifyReport{
		Details:     b.cfg.ClassifyReport,
		DataRepoURL: strings.TrimSpace(b.cfg.ClassifyDataRepoURL),
		Legend:      classifyLegend(),
	}
	if b.cfg.ClassifyReport && len(b.cfg.ClassifyData) > 0 {
		rep.JSONHref = "/.well-known/pbc-classification/domains.json"
		rep.Entries, rep.Counts, rep.Total = b.classifyDatasetEntries()
	}
	html, err := render.ClassificationReport(rep, b.pageOpts("How we rate external links", "/classification"))
	if err != nil {
		return err
	}
	if err := b.emitPage("/classification", html, nil); err != nil {
		return err
	}
	b.addSitemapListing("/classification")
	return nil
}

// classifyLegend returns the rating scale (letter + name + glyph) straight from
// pbc-classification, so the report's scale can never drift from the library.
func classifyLegend() []render.ClassifyGrade {
	out := make([]render.ClassifyGrade, 0, len(classifyGradeOrder))
	for _, g := range classifyGradeOrder {
		out = append(out, render.ClassifyGrade{Letter: g.Letter(), Name: g.Name(), Icon: g.Icon()})
	}
	return out
}

// classifyDatasetEntries classifies every domain in the operator's dataset and
// returns them sorted alphabetically, plus per-grade tallies (best→worst) and the
// total. Only the operator's published dataset is enumerable here; the library's
// built-in defaults live in the module (linked from the report).
func (b *builder) classifyDatasetEntries() ([]render.ExtRef, []render.ClassifyCount, int) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b.cfg.ClassifyData, &m); err != nil {
		return nil, nil, 0
	}
	domains := make([]string, 0, len(m))
	for d := range m {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	byGrade := map[string]int{}
	entries := make([]render.ExtRef, 0, len(domains))
	for _, d := range domains {
		c := b.classifier.Classify("https://" + d + "/")
		letter := c.Grade.Letter()
		byGrade[letter]++
		entries = append(entries, render.ExtRef{
			Domain: d, Grade: letter, GradeName: c.Grade.Name(), Reasons: c.Reasons,
		})
	}
	var counts []render.ClassifyCount
	for _, g := range classifyGradeOrder {
		if n := byGrade[g.Letter()]; n > 0 {
			counts = append(counts, render.ClassifyCount{Letter: g.Letter(), Count: n})
		}
	}
	return entries, counts, len(domains)
}

// validateNavLinks warns for any local (same-site, rooted) nav or footer link that
// doesn't resolve to a file actually emitted in the bundle — a typo'd or dangling
// menu link. External links, anchors, and mailto: are ignored. Validating against
// the real output means generated routes (tags, feeds) never false-warn.
func (b *builder) validateNavLinks() {
	emitted := make(map[string]bool, len(b.hashes))
	for rel := range b.hashes {
		emitted[rel] = true
	}
	seen := map[string]bool{}
	check := func(kind string, l render.NavLink) {
		href := strings.TrimSpace(l.Href)
		if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") {
			return // external / anchor / mailto / protocol-relative
		}
		if seen[href] {
			return
		}
		seen[href] = true
		if file := linkTargetFile(href); file != "" && !emitted[file] {
			b.report.Warnings = append(b.report.Warnings,
				fmt.Sprintf("%s link %q → %s has no matching built page", kind, l.Label, href))
		}
	}
	for _, l := range b.cfg.Nav {
		check("nav", l)
	}
	for _, l := range b.cfg.FooterNav {
		check("footer", l)
	}
}

// linkTargetFile maps a same-site path to the bundle file it should resolve to,
// using the same pretty-URL rule as page output: a path whose last segment has an
// extension (e.g. /feeds/blog.rss) maps to that file; otherwise to
// <path>/index.html ("/" → index.html).
func linkTargetFile(href string) string {
	if i := strings.IndexAny(href, "?#"); i >= 0 {
		href = href[:i]
	}
	p := strings.Trim(href, "/")
	if p == "" {
		return "index.html"
	}
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	if strings.Contains(base, ".") {
		return p
	}
	return p + "/index.html"
}

// cleanOutputDir removes the output directory so each build reflects exactly the
// current published state. It refuses obviously dangerous targets (empty, the
// filesystem root, or the current working directory) to avoid wiping something
// that isn't a dedicated build output.
func cleanOutputDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("build: output directory must not be empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("build: resolve output dir: %w", err)
	}
	if abs == "/" || abs == filepath.Dir(abs) {
		return fmt.Errorf("build: refusing to build into the filesystem root %q", abs)
	}
	if wd, err := os.Getwd(); err == nil && abs == wd {
		return fmt.Errorf("build: refusing to build into the current working directory; use a dedicated -out dir")
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("build: clean output %s: %w", dir, err)
	}
	return nil
}

// fingerprint returns a content-hashed filename (e.g. assets/theme.<hash>.css)
// and its absolute href, so hashed assets can be cached immutably.
func fingerprint(rel string, data []byte) (name, href string) {
	sum := sha256.Sum256(data)
	h := fmt.Sprintf("%x", sum[:5]) // 10 hex chars
	ext := filepath.Ext(rel)
	name = strings.TrimSuffix(rel, ext) + "." + h + ext
	return name, "/" + name
}

// canonical returns the absolute URL for a page path (for the canonical link and
// og:url). It is empty when no base URL is configured.
func (b *builder) canonical(pagePath string) string {
	base := strings.TrimRight(b.cfg.BaseURL, "/")
	if base == "" {
		return ""
	}
	if !strings.HasPrefix(pagePath, "/") {
		pagePath = "/" + pagePath
	}
	return base + pagePath
}

// ogImageURL resolves a page's social-preview image (SPEC §6.3) to an absolute URL:
// the page's own OGImage, else the site default. A same-site /media path is made
// absolute against the Base URL (so it is skipped when there is no Base URL); an
// already-absolute http(s) URL is used as-is. Returns "" when there is nothing to emit.
func (b *builder) ogImageURL(pageOG string) string {
	ref := strings.TrimSpace(pageOG)
	if ref == "" {
		ref = strings.TrimSpace(b.cfg.OGImageDefault)
	}
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	return b.canonical(ref) // "" when no Base URL — og:image is then omitted
}

// buildPage renders and emits a published page, then generates the consent-gated
// /external/youtube/<name> page for each youtube block it contains (SPEC §5.8).
func (b *builder) buildPage(p store.PublishedPage) error {
	// The author summary and keywords drive the page's SEO metadata.
	meta, _ := render.Parse(p.ContentJSON)
	// Collect this page for any feeds it belongs to, and add their auto-discovery
	// links to its <head>.
	feedLinks := b.collectFeedPage(p, meta.Summary)
	// Deferred-reveal (§6.9) and group-gated (§6.10) blocks both need the page's
	// stored key and pre-processing (render + harden + classify their hidden HTML
	// before encryption), plus their own decode scripts — but only when the page
	// actually carries such a block.
	var err error
	var revealKey []byte
	var revealJSHref, gateJSHref, codeCopyJSHref, shareJSHref, splashAlias string
	var hiddenRefs []linkscan.Result // options B/C refs from reveal + gated blocks
	content := meta
	hasReveal := hasRevealBlock(meta.Blocks)
	hasGated := hasGatedBlock(meta.Blocks)
	// A page may also be a key group's splash/deposit page (§6.10) even with no gated
	// block of its own, in which case it still needs the gate script to deposit the
	// KEK the visitor arrives with.
	splashAlias, isSplash, err := b.store.SplashAliasForPage(p.ID)
	if err != nil {
		return fmt.Errorf("build: splash lookup %s: %w", p.Path, err)
	}
	if hasReveal || hasGated {
		revealKey, err = b.store.PageKey(p.ID)
		if err != nil {
			return fmt.Errorf("build: page key %s: %w", p.Path, err)
		}
	}
	if hasGated || isSplash {
		if err := b.ensureGate(); err != nil {
			return err
		}
		gateJSHref = b.gateJSHref
	}
	if !isSplash {
		splashAlias = "" // only mark the page when it is actually a group's splash
	}
	if hasCodeBlock(meta.Blocks) {
		if err := b.ensureCodeCopy(); err != nil {
			return err
		}
		codeCopyJSHref = b.codeCopyJSHref
	}
	if hasShareBlock(meta.Blocks) {
		if err := b.ensureShare(); err != nil {
			return err
		}
		shareJSHref = b.shareJSHref
	}
	if hasReveal {
		if err := b.ensureReveal(); err != nil {
			return err
		}
		revealJSHref = b.revealJSHref
		// Render + harden markdown reveal fragments (B) and classify their external
		// references (C) before the content is rendered and encrypted (§6.9).
		var refs []linkscan.Result
		content, refs, err = PrepareReveal(content, b.hcfg, b.scanner)
		if err != nil {
			return fmt.Errorf("build: reveal %s: %w", p.Path, err)
		}
		hiddenRefs = append(hiddenRefs, refs...)
	}
	// Resolve any tag-mode gallery block into its concrete images before rendering
	// (§6.14) — and before the gate pass, so a *gated* tag-gallery is resolved before it
	// is encrypted. The resolved <img src="/media/…"> are then emitted like any other
	// media (the gate pass registers those inside a gated gallery too).
	content, err = PrepareGallery(content, b.store)
	if err != nil {
		return fmt.Errorf("build: gallery %s: %w", p.Path, err)
	}
	if hasGated {
		// The self-hosted keyring/decode script and its wiring land in the next phase
		// (§6.10); this phase does the deterministic build-side work: render + harden
		// each gated block's inner HTML (B), classify its external references (C), and
		// envelope-encrypt it. Also register any media it references so the bytes are
		// still emitted (the reference is encrypted out of the scanned page HTML —
		// SPEC §6.10, the image/media caveat). opts carries the page index/host so a
		// gated index block renders its list.
		gopts := render.Options{PageIndex: b.pageIndex, HostPath: p.Path, IsIndexPage: meta.IsIndex}
		var refs []linkscan.Result
		var mediaRefs []render.MediaRef
		content, refs, mediaRefs, err = PrepareGated(content, gopts, b.hcfg, b.scanner)
		if err != nil {
			return fmt.Errorf("build: gate %s: %w", p.Path, err)
		}
		hiddenRefs = append(hiddenRefs, refs...)
		for _, ref := range mediaRefs {
			rel := ref.Rel()
			u := b.media[rel]
			if u == nil {
				u = &mediaUsage{sha: ref.SHA}
				b.media[rel] = u
			}
			u.pages = append(u.pages, p.Path)
		}
	}
	rendered, err := render.RenderContent(content, render.Options{
		Title:           p.Title,
		SiteName:        b.cfg.SiteName,
		BuildNumber:     b.cfg.BuildNumber,
		Lang:            b.cfg.Lang,
		Search:          b.cfg.Search,
		CSSHref:         b.cssHref,
		ThemeJSHref:     b.themeJSHref,
		Brand:           b.brand,
		SearchJSHref:    b.searchJSHref,
		Description:     meta.Summary,
		Tags:            meta.Tags,
		OpenGraph:       b.cfg.OpenGraph,
		OGImage:         b.ogImageURL(meta.OGImage),
		CanonicalURL:    b.canonical(p.Path),
		Nav:             b.cfg.Nav,
		FooterNav:       b.cfg.FooterNav,
		Year:            b.cfg.Year,
		FeedLinks:       feedLinks,
		PageIndex:       b.pageIndex,
		HostPath:        p.Path,
		IsIndexPage:     meta.IsIndex,
		ShowReadingTime: b.cfg.ShowReadingTime,
		RevealJSHref:    revealJSHref,
		GateJSHref:      gateJSHref,
		CodeCopyJSHref:  codeCopyJSHref,
		ShareJSHref:     shareJSHref,
		Comments:        hasCommentsBlock(meta.Blocks),
		SplashAlias:     splashAlias,
		RevealKey:       revealKey,
		GateKEKs:        b.keks,
		Favicon:         b.favicon,
	})
	if err != nil {
		return fmt.Errorf("build: render %s: %w", p.Path, err)
	}
	// Collect this page under each of its tags for the browsable tag pages — unless
	// it is unlisted (§6.16), which is never listed on a /tags/ page even if tagged.
	if !meta.Unlisted {
		for _, tag := range meta.Tags {
			slug := render.TagSlug(tag)
			if slug == "" {
				continue
			}
			ti := b.tags[slug]
			if ti == nil {
				ti = &tagInfo{name: strings.TrimSpace(tag)}
				b.tags[slug] = ti
			}
			ti.pages = append(ti.pages, render.PageLink{Title: p.Title, Href: p.Path})
		}
	}
	// hiddenRefs (option C): external references inside reveal and group-gated blocks
	// are encrypted out of the scanned page HTML, so pass them as extra scan results
	// to keep the page's privacy manifest honest (§6.9, §6.10). An unlisted page keeps
	// its in-page External References section but publishes no separate manifest file
	// (whose path would reveal it) and is left out of the site aggregate (§6.16).
	if err := b.emitPageManifest(p.Path, rendered.HTML, hiddenRefs, !meta.Unlisted); err != nil {
		return err
	}
	// A noindex or unlisted page is excluded from BOTH the sitemap (external engines)
	// and the on-site search index — "hide from search" applies to the site's own
	// search too, so the page's content is not readable via search/index.json (§6.2).
	if !meta.NoIndex && !meta.Unlisted {
		b.addSitemap(p.Path, p.UpdatedAt)
		if b.cfg.Search {
			doc, err := search.BuildDocument(p.Path, p.Title, p.UpdatedAt.Format("2006-01-02"),
				p.ContentJSON, search.Options{FullText: b.cfg.SearchFullText})
			if err != nil {
				return fmt.Errorf("build: search index %s: %w", p.Path, err)
			}
			b.docs = append(b.docs, doc)
		}
	}
	for _, yt := range rendered.YouTube {
		if err := b.buildYouTube(yt, p.Path, p.Title); err != nil {
			return err
		}
	}
	for _, em := range rendered.Embed {
		if err := b.buildEmbed(em, p.Path, p.Title); err != nil {
			return err
		}
	}
	return nil
}

// Canonical favicon slot names (SPEC §6.11), each served at "/<name>".
const (
	faviconSVG     = "favicon.svg"
	faviconICO     = "favicon.ico"
	faviconApple   = "apple-touch-icon.png"
	faviconIcon192 = "icon-192.png"
	faviconIcon512 = "icon-512.png"
)

// emitFavicons writes the operator's uploaded favicon/app-icon assets to their
// canonical site-root paths, generates a web manifest when PWA icons are present,
// and records which <head> links every page should emit (SPEC §6.11). Icons are
// stored pre-cleaned (SVG sanitized, PNG stripped) so no re-processing is needed.
func (b *builder) emitFavicons(s *store.Store) error {
	names, err := s.FaviconNames()
	if err != nil {
		return fmt.Errorf("build: favicons: %w", err)
	}
	present := make(map[string]bool, len(names))
	for _, n := range names {
		f, ok, err := s.Favicon(n)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := b.write(n, f.Data); err != nil { // canonical root path (e.g. favicon.svg)
			return err
		}
		present[n] = true
	}
	if len(present) == 0 {
		return nil // no favicons configured; emit no links
	}
	b.favicon.SVG = present[faviconSVG]
	b.favicon.ICO = present[faviconICO]
	b.favicon.AppleTouch = present[faviconApple]
	b.favicon.ThemeColor = strings.TrimSpace(b.cfg.FaviconThemeColor)
	if present[faviconIcon192] || present[faviconIcon512] {
		if err := b.write("site.webmanifest", webManifest(b.cfg.SiteName, b.cfg.FaviconThemeColor, present)); err != nil {
			return err
		}
		b.favicon.Manifest = true
	}
	return nil
}

// webManifest builds site.webmanifest from the site name, theme colour, and which
// PWA icons are present. Icons are marked "any maskable" — the generated icons are
// full-bleed tiles, so they mask cleanly on Android (SPEC §6.11).
func webManifest(siteName, themeColor string, present map[string]bool) []byte {
	color := strings.TrimSpace(themeColor)
	if color == "" {
		color = "#ffffff"
	}
	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	}
	var icons []icon
	if present[faviconIcon192] {
		icons = append(icons, icon{"/icon-192.png", "192x192", "image/png", "any maskable"})
	}
	if present[faviconIcon512] {
		icons = append(icons, icon{"/icon-512.png", "512x512", "image/png", "any maskable"})
	}
	m := map[string]any{
		"name":             siteName,
		"short_name":       siteName,
		"icons":            icons,
		"theme_color":      color,
		"background_color": color,
		"display":          "standalone",
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	return append(out, '\n')
}

// ensureReveal emits the fingerprinted self-hosted deferred-reveal decode script
// once, before any page references it (§6.9). Only pages carrying a reveal block
// link it, so a site without hidden content never ships the script.
func (b *builder) ensureReveal() error {
	if b.wroteReveal {
		return nil
	}
	name, href := fingerprint(render.RevealJSPath, []byte(render.RevealJS))
	if err := b.write(name, []byte(render.RevealJS)); err != nil {
		return err
	}
	b.revealJSHref = href
	b.wroteReveal = true
	return nil
}

// ensureCodeCopy emits the fingerprinted self-hosted code-block copy-button script
// once, before any page references it (§6.12). Only pages carrying a code block link
// it, so a site with no code blocks never ships the script.
func (b *builder) ensureCodeCopy() error {
	if b.wroteCodeCopy {
		return nil
	}
	name, href := fingerprint(render.CodeCopyJSPath, []byte(render.CodeCopyJS))
	if err := b.write(name, []byte(render.CodeCopyJS)); err != nil {
		return err
	}
	b.codeCopyJSHref = href
	b.wroteCodeCopy = true
	return nil
}

// ensureShare emits the fingerprinted self-hosted share-block script once, before any
// page references it (§6.15). Only pages carrying a share block link it.
func (b *builder) ensureShare() error {
	if b.wroteShare {
		return nil
	}
	name, href := fingerprint(render.ShareJSPath, []byte(render.ShareJS))
	if err := b.write(name, []byte(render.ShareJS)); err != nil {
		return err
	}
	b.shareJSHref = href
	b.wroteShare = true
	return nil
}

// ensureGate emits the fingerprinted self-hosted group-gate keyring/decode script
// once, before any page references it (§6.10). Only pages carrying a gated block or
// serving as a group's splash link it, so a site with no group-gated content never
// ships the script.
func (b *builder) ensureGate() error {
	if b.wroteGate {
		return nil
	}
	name, href := fingerprint(render.GateJSPath, []byte(render.GateJS))
	if err := b.write(name, []byte(render.GateJS)); err != nil {
		return err
	}
	b.gateJSHref = href
	b.wroteGate = true
	return nil
}

// GateFallbackPath is the built path of the generic deposit page for a key group
// with no authored splash page (SPEC §6.10): /unlock/<alias>. The editor builds the
// group's gate link against this same path, so the two never drift.
func GateFallbackPath(alias string) string { return "/unlock/" + alias }

// emitGateFallbacks writes a generic, self-hosted deposit page at /unlock/<alias>
// for every key group that has no authored splash page, so each group always has a
// working gate link. The page is an ordinary themed page carrying the splash marker
// and the gate script; opened from a gate link its fragment deposits the KEK into the
// keyring, and opened without one it is a neutral, key-free public page (noindex).
func (b *builder) emitGateFallbacks() error {
	groups, err := b.store.KeyGroups()
	if err != nil {
		return fmt.Errorf("build: gate fallbacks: %w", err)
	}
	for _, g := range groups {
		if g.SplashPageID != nil {
			continue // this group has an authored splash page; no generic fallback needed
		}
		if err := b.ensureGate(); err != nil {
			return err
		}
		path := GateFallbackPath(g.Alias)
		body := "# Members access\n\nIf you opened this page from a members access link, your key " +
			"for this group has been saved to this browser, and members-only content will now appear " +
			"across the site. You can return to the [home page](/).\n\nThis page stores nothing until " +
			"you open it from a valid access link, and the key travels only in the link — never to the server."
		content := render.Content{Body: body, NoIndex: true}
		opts := b.pageOpts("Members access", path)
		opts.GateJSHref = b.gateJSHref
		opts.SplashAlias = g.Alias
		rendered, err := render.RenderContent(content, opts)
		if err != nil {
			return fmt.Errorf("build: gate fallback %s: %w", path, err)
		}
		if err := b.emitPage(path, rendered.HTML, nil); err != nil {
			return err
		}
	}
	return nil
}

// hasRevealBlock reports whether any block on the page is a deferred-reveal block,
// so the build only fetches the page key and links reveal.js when needed.
func hasRevealBlock(blocks []render.Block) bool {
	for _, bl := range blocks {
		if bl.Type == "reveal" {
			return true
		}
	}
	return false
}

// hasCodeBlock reports whether any block on the page is a code block, so the build
// only links the copy-button script on pages that carry one (§6.12).
func hasCodeBlock(blocks []render.Block) bool {
	for _, bl := range blocks {
		if bl.Type == "code" {
			return true
		}
	}
	return false
}

// hasShareBlock reports whether any block on the page is a share block, so the build
// only links the share script on pages that carry one (§6.15).
func hasShareBlock(blocks []render.Block) bool {
	for _, bl := range blocks {
		if bl.Type == "share" {
			return true
		}
	}
	return false
}

// hasCommentsBlock reports whether any block on the page is a comments block, so the
// build only links the self-hosted comments widget on pages that carry one (§7.3).
// The widget is served live by the dynamic layer at render.CommentsJSPath, not written
// into the bundle, so there is no asset to emit here — only the link is gated.
func hasCommentsBlock(blocks []render.Block) bool {
	for _, bl := range blocks {
		if bl.Type == "comments" {
			return true
		}
	}
	return false
}

// PrepareReveal pre-processes a page's markdown reveal blocks (SPEC §6.9, kind
// "markdown") before the content is rendered and encrypted. For each such block it
// renders the markdown to goldmark-safe HTML, hardens that fragment with the same
// linking hygiene the build applies to page HTML (rel/referrerpolicy/lazy on
// external refs — option B), and swaps the block's Content to the hardened HTML so
// the reveal payload that gets encrypted is already hygienic. It also scans the
// fragment and returns the classified external references it contains (option C),
// so the caller can fold them into the page's privacy manifest — a hidden markdown
// link can never smuggle an unclassified third-party request past the model.
//
// It returns a copy of c (the stored content is untouched — the DB keeps the
// author's markdown) with only markdown reveal blocks rewritten, plus the
// aggregated references. Non-markdown blocks are returned unchanged. The scanner
// and hcfg are the caller's (build or editor), so results match that context.
func PrepareReveal(c render.Content, hcfg hygiene.Config, scanner *linkscan.Scanner) (render.Content, []linkscan.Result, error) {
	var refs []linkscan.Result
	blocks := make([]render.Block, len(c.Blocks))
	copy(blocks, c.Blocks)
	for i := range blocks {
		if blocks[i].Type != "reveal" || blocks[i].Reveal == nil || render.RevealKind(blocks[i].Reveal.Kind) != "markdown" {
			continue
		}
		frag, err := render.RevealMarkdownHTML(blocks[i].Reveal.Content)
		if err != nil {
			return c, nil, fmt.Errorf("build: reveal markdown: %w", err)
		}
		// Option B: harden external links/images in the fragment before it is encrypted.
		hres, err := hygiene.ApplyFragment([]byte(frag), hcfg)
		if err != nil {
			return c, nil, fmt.Errorf("build: reveal hygiene: %w", err)
		}
		// Option C: classify the fragment's external references so the caller can add
		// them to the page manifest (they are encrypted out of the scanned page HTML).
		frefs, err := scanner.Scan(bytes.NewReader(hres.HTML))
		if err != nil {
			return c, nil, fmt.Errorf("build: reveal scan: %w", err)
		}
		refs = append(refs, frefs...)
		rv := *blocks[i].Reveal
		rv.Content = string(hres.HTML) // encrypt the hardened HTML, not the raw markdown
		blocks[i].Reveal = &rv
	}
	c.Blocks = blocks
	return c, refs, nil
}

// hasGatedBlock reports whether any block on the page is group-gated (SPEC §6.10):
// a gateable block type carrying one or more authorized group aliases. Non-gateable
// types ignore any stray Groups, so they never trigger the gate path.
func hasGatedBlock(blocks []render.Block) bool {
	for _, bl := range blocks {
		if len(bl.Groups) > 0 && render.IsGateable(bl.Type) {
			return true
		}
	}
	return false
}

// PrepareGated pre-processes a page's group-gated blocks (SPEC §6.10) before the
// content is rendered and envelope-encrypted. For each gateable block carrying group
// aliases it renders the block to its inner HTML, hardens that HTML with the build's
// linking hygiene (option B), classifies its external references (option C), and
// replaces the block with a synthetic "gate" block whose payload is the hardened HTML
// — so the render pass encrypts already-hygienic, already-classified content and a
// hidden gated link can never smuggle an unclassified third-party request past the
// model. It returns a copy of c (the stored content is untouched), the aggregated
// external references, and the same-site media references found in the gated HTML —
// those bytes must still be emitted even though the reference is encrypted out of the
// scanned page HTML (the §6.10 image/media caveat: gating hides the placement, not
// the file).
func PrepareGated(c render.Content, opts render.Options, hcfg hygiene.Config, scanner *linkscan.Scanner) (render.Content, []linkscan.Result, []render.MediaRef, error) {
	var refs []linkscan.Result
	var mediaRefs []render.MediaRef
	blocks := make([]render.Block, len(c.Blocks))
	copy(blocks, c.Blocks)
	for i := range blocks {
		if len(blocks[i].Groups) == 0 || !render.IsGateable(blocks[i].Type) {
			continue
		}
		// opts carries PageIndex/HostPath/IsIndexPage so a gated index block renders its
		// list; other gateable types ignore it.
		inner, err := render.RenderBlockInner(blocks[i], i, opts)
		if err != nil {
			return c, nil, nil, fmt.Errorf("build: gate render: %w", err)
		}
		// Option B: harden external links/images before the HTML is encrypted.
		hres, err := hygiene.ApplyFragment([]byte(inner), hcfg)
		if err != nil {
			return c, nil, nil, fmt.Errorf("build: gate hygiene: %w", err)
		}
		// Option C: classify the fragment's external references for the page manifest.
		frefs, err := scanner.Scan(bytes.NewReader(hres.HTML))
		if err != nil {
			return c, nil, nil, fmt.Errorf("build: gate scan: %w", err)
		}
		refs = append(refs, frefs...)
		mediaRefs = append(mediaRefs, render.MediaRefs(hres.HTML)...)
		groups := append([]string(nil), blocks[i].Groups...)
		blocks[i] = render.Block{
			Type:   "gate",
			Groups: groups,
			Gate:   &render.Gate{HTML: string(hres.HTML), Groups: groups},
		}
	}
	c.Blocks = blocks
	return c, refs, mediaRefs, nil
}

// ensureFacade emits the fingerprinted self-hosted facade script once (shared by
// the youtube and generic embed external pages), before any page references it.
func (b *builder) ensureFacade() error {
	if b.wroteFacade {
		return nil
	}
	name, href := fingerprint(render.FacadeJSPath, []byte(render.FacadeJS))
	if err := b.write(name, []byte(render.FacadeJS)); err != nil {
		return err
	}
	b.facadeHref = href
	b.wroteFacade = true
	return nil
}

// buildYouTube generates the Stage-2 /external/youtube/<name> page and writes the
// self-hosted facade script once. backHref/backLabel point to the page that
// referenced the video, for the "back" link.
func (b *builder) buildYouTube(yt render.YouTube, backHref, backLabel string) error {
	if err := b.ensureFacade(); err != nil {
		return err
	}

	extPath := "/external/youtube/" + yt.Name
	html, err := render.ExternalYouTube(yt, render.Options{
		Title:        yt.Title,
		SiteName:     b.cfg.SiteName,
		BuildNumber:  b.cfg.BuildNumber,
		Lang:         b.cfg.Lang,
		Search:       b.cfg.Search,
		CSSHref:      b.cssHref,
		ThemeJSHref:  b.themeJSHref,
		Brand:        b.brand,
		SearchJSHref: b.searchJSHref,
		FacadeJSHref: b.facadeHref,
		BackHref:     backHref,
		BackLabel:    backLabel,
		OpenGraph:    b.cfg.OpenGraph,
		CanonicalURL: b.canonical(extPath),
		Nav:          b.cfg.Nav,
		FooterNav:    b.cfg.FooterNav,
		Year:         b.cfg.Year,
	})
	if err != nil {
		return fmt.Errorf("build: youtube %s: %w", yt.Name, err)
	}

	// Honestly disclose the click-to-load facade target in the manifest: the
	// facade loads youtube-nocookie via JS on play, so it is not in the static
	// HTML for the link scanner to find (SPEC §5.8).
	ytURL := "https://www.youtube-nocookie.com/embed/" + yt.VideoID
	facade := linkscan.Result{
		Ref: linkscan.Reference{
			Kind: linkscan.KindFrame, Element: "iframe", Attr: "src",
			RawURL: ytURL, Resolved: ytURL, Host: "www.youtube-nocookie.com",
		},
		Classification: b.classifier.Classify(ytURL),
	}
	return b.emitPage(extPath, html, []linkscan.Result{facade})
}

// buildEmbed generates the Stage-2 /external/<provider>/<name> page for a generic
// embed, but only if the embed URL's host is on the operator's allowlist. A
// non-allowlisted host is refused (skipped with a build warning) so no page can
// frame a host the served-site CSP would block anyway (SPEC §5.8, defense in
// depth over the CSP frame-src).
func (b *builder) buildEmbed(e render.Embed, backHref, backLabel string) error {
	host := strings.ToLower(render.EmbedHost(e.EmbedURL))
	if host == "" || !b.embedHosts[host] {
		b.report.Warnings = append(b.report.Warnings,
			fmt.Sprintf("embed %q on %s skipped: host %q is not in the Settings embed allowlist", e.Name, backHref, host))
		return nil
	}
	if err := b.ensureFacade(); err != nil {
		return err
	}

	provider := render.ProviderLabel(e.Provider)
	extPath := "/external/" + provider + "/" + e.Name
	html, err := render.ExternalEmbed(e, render.Options{
		Title:        e.Title,
		SiteName:     b.cfg.SiteName,
		BuildNumber:  b.cfg.BuildNumber,
		Lang:         b.cfg.Lang,
		Search:       b.cfg.Search,
		CSSHref:      b.cssHref,
		ThemeJSHref:  b.themeJSHref,
		Brand:        b.brand,
		SearchJSHref: b.searchJSHref,
		FacadeJSHref: b.facadeHref,
		BackHref:     backHref,
		BackLabel:    backLabel,
		OpenGraph:    b.cfg.OpenGraph,
		CanonicalURL: b.canonical(extPath),
		Nav:          b.cfg.Nav,
		FooterNav:    b.cfg.FooterNav,
		Year:         b.cfg.Year,
	})
	if err != nil {
		return fmt.Errorf("build: embed %s: %w", e.Name, err)
	}

	// Honestly disclose the click-to-load facade target in the manifest: the facade
	// frames the embed URL via JS on load, so it is not in the static HTML for the
	// link scanner to find (SPEC §5.8).
	facade := linkscan.Result{
		Ref: linkscan.Reference{
			Kind: linkscan.KindFrame, Element: "iframe", Attr: "src",
			RawURL: e.EmbedURL, Resolved: e.EmbedURL, Host: host,
		},
		Classification: b.classifier.Classify(e.EmbedURL),
	}
	return b.emitPage(extPath, html, []linkscan.Result{facade})
}

// injectExtRefList replaces the layout's ExtRefSlot with the per-domain
// external-references listing (grade + name + reasons), or removes the slot when
// the page references no external domain. Domains are ordered worst-grade-first
// (then alphabetically), matching the editor's live badge list.
func (b *builder) injectExtRefList(htmlBytes []byte, domains []manifest.DomainEntry) []byte {
	list := ""
	if len(domains) > 0 {
		refs := make([]render.ExtRef, 0, len(domains))
		for _, d := range domains {
			refs = append(refs, render.ExtRef{
				Domain: d.Domain, Grade: d.Grade, GradeName: d.GradeName,
				Count: len(d.Refs), Reasons: d.Reasons,
			})
		}
		sort.SliceStable(refs, func(i, j int) bool {
			ri, rj := gradeRankLetter(refs[i].Grade), gradeRankLetter(refs[j].Grade)
			if ri != rj {
				return ri < rj // lower rank = worse grade, shown first
			}
			return refs[i].Domain < refs[j].Domain
		})
		list = render.ExternalRefList(refs)
	}
	return bytes.Replace(htmlBytes, []byte(render.ExtRefSlot), []byte(list), 1)
}

// gradeRankLetter maps a grade letter to pbc-classification's ordinal severity
// (Unclassified "?" lowest/worst, then F, D, C, B, A), so "worst-first" ordering
// matches the library's own grade ranking (mirrors manifest's gradeRank).
func gradeRankLetter(letter string) int {
	switch letter {
	case "A":
		return int(classify.GradeA)
	case "B":
		return int(classify.GradeB)
	case "C":
		return int(classify.GradeC)
	case "D":
		return int(classify.GradeD)
	case "F":
		return int(classify.GradeF)
	default:
		return int(classify.GradeUnclassified)
	}
}

// hostSet lowercases and normalizes the allowlist into a set for membership tests.
func hostSet(hosts []string) map[string]bool {
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h = normalizeHost(h); h != "" {
			set[h] = true
		}
	}
	return set
}

// embedOrigins returns the sorted, de-duplicated https origins for the allowlisted
// embed hosts, for the served-site CSP frame-src.
func embedOrigins(hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hosts {
		if h = normalizeHost(h); h == "" {
			continue
		}
		o := "https://" + h
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

// embedHostRE matches a clean host: an optional "*." wildcard, dot-separated
// labels (letters/digits/hyphens, no leading/trailing hyphen), and an optional
// ":port". It is the gate that keeps an operator embed-host entry from carrying
// spaces, ";", quotes, or anything else that could inject directives into the
// served-site CSP frame-src when the host is concatenated into it.
var embedHostRE = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

// ValidEmbedHost reports whether a raw operator embed-host entry normalizes to a
// clean host[:port] (optionally "*."-wildcarded). The editor uses it to reject
// bad entries; the build drops them. This closes the CSP frame-src injection gap
// (a host is never emitted into the CSP unless it matches embedHostRE).
func ValidEmbedHost(raw string) bool { return normalizeHost(raw) != "" }

// normalizeHost reduces an operator-entered allowlist entry (a bare host or a full
// URL) to a lowercase host[:port], dropping any scheme or path — and returning ""
// for anything that is not a clean host, so only validated hosts reach the CSP.
func normalizeHost(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return ""
		}
		s = u.Host
	} else if i := strings.IndexByte(s, '/'); i >= 0 {
		// A bare "host/path" — keep up to the first slash.
		s = s[:i]
	}
	if !embedHostRE.MatchString(s) {
		return "" // reject spaces, ";", quotes, and other CSP-breaking input
	}
	return s
}

// emitPage applies hygiene, scans/classifies external references (plus any extra
// results the caller supplies), and writes the page HTML and its manifest.
// emitPage renders, hardens, scans, and writes a page plus its published per-page
// privacy manifest.
func (b *builder) emitPage(pagePath string, html []byte, extra []linkscan.Result) error {
	return b.emitPageManifest(pagePath, html, extra, true)
}

// emitPageManifest is emitPage with control over whether the per-page privacy
// manifest is published. An unlisted page (§6.16) passes publishManifest=false: it
// still renders its in-page External References section, but no enumerable
// manifest/<path>.json is written and it is left out of the site aggregate — so the
// page's existence and path are not disclosed by the manifest.
func (b *builder) emitPageManifest(pagePath string, html []byte, extra []linkscan.Result, publishManifest bool) error {
	hres, err := hygiene.Apply(html, b.hcfg)
	if err != nil {
		return fmt.Errorf("build: hygiene %s: %w", pagePath, err)
	}
	// Assign heading ids + self-anchor permalinks and fill any toc block (§6.12). Runs
	// after hygiene but before the external-references listing is injected below, so the
	// "External references" chrome heading is never anchored or pulled into the TOC.
	anchored, err := render.AnchorsAndTOC(hres.HTML)
	if err != nil {
		return fmt.Errorf("build: anchors/toc %s: %w", pagePath, err)
	}
	hres.HTML = anchored
	results, err := b.scanner.Scan(bytes.NewReader(hres.HTML))
	if err != nil {
		return fmt.Errorf("build: scan %s: %w", pagePath, err)
	}
	results = append(results, extra...)

	// Record the content-addressed media this page references, so the build emits
	// exactly those assets (and no orphans) from the store, and can name the pages
	// behind a broken reference (render.MediaRefs is deduped per page).
	for _, ref := range render.MediaRefs(hres.HTML) {
		rel := ref.Rel()
		u := b.media[rel]
		if u == nil {
			u = &mediaUsage{sha: ref.SHA}
			b.media[rel] = u
		}
		u.pages = append(u.pages, pagePath)
	}

	pm := manifest.BuildPage(pagePath, results)
	if publishManifest {
		b.sb.AddPage(pm)
	}

	// Surface the per-page external-references listing right before the footer, using
	// the classification just computed (the render pass ran before the scan, so the
	// grades are only known now). It replaces the layout's ExtRefSlot; a page with no
	// external references has the slot removed instead (SPEC §5.7).
	pageHTML := b.injectExtRefList(hres.HTML, pm.Domains)

	outFile := pathToFile(pagePath)
	if err := b.write(outFile, pageHTML); err != nil {
		return err
	}
	if publishManifest {
		pmJSON, err := manifest.Encode(pm)
		if err != nil {
			return err
		}
		if err := b.write(pathToManifest(pagePath), pmJSON); err != nil {
			return err
		}
	}

	var warns []string
	for _, w := range hres.Warnings {
		warns = append(warns, fmt.Sprintf("%s %s: %s", w.Element, w.Host, w.Message))
	}
	rewrites := 0
	for _, c := range hres.Changes {
		if c.Kind == "rewrite" {
			rewrites++
		}
	}
	b.report.Pages = append(b.report.Pages, PageReport{
		Path:       pagePath,
		OutputFile: outFile,
		WorstGrade: pm.Summary.WorstGrade,
		Externals:  pm.Summary.External,
		Rewrites:   rewrites,
		Warnings:   warns,
	})
	return nil
}

// emitMedia writes every media asset referenced by the built pages, fetched from
// the store by content address. Each asset is re-verified metadata-clean before
// it is published (defense in depth over the ingest-time strip). A reference to
// an asset not in the store is a warning, not a hard failure, so a build with a
// stale reference still completes.
func (b *builder) emitMedia(s *store.Store) error {
	rels := make([]string, 0, len(b.media))
	for rel := range b.media {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		use := b.media[rel]
		sha := use.sha
		// Images are BLOB-stored; audio/video are filesystem-backed. Try the image
		// store first, then media files. Either way, re-verify metadata-clean
		// before publishing (defense in depth over the ingest-time strip).
		a, err := s.Asset(sha)
		switch {
		case err == nil:
			if err := asset.Verify(a.Data); err != nil {
				return fmt.Errorf("build: media /%s is not clean: %w", rel, err)
			}
			if err := b.write(rel, a.Data); err != nil {
				return err
			}
		case errors.Is(err, sql.ErrNoRows):
			data, _, merr := s.ReadMedia(sha)
			if errors.Is(merr, sql.ErrNoRows) {
				b.reportBrokenMedia(rel, use.pages)
				continue
			}
			if merr != nil {
				return fmt.Errorf("build: media /%s: %w", rel, merr)
			}
			if err := asset.Verify(data); err != nil {
				return fmt.Errorf("build: media /%s is not clean: %w", rel, err)
			}
			if err := b.write(rel, data); err != nil {
				return err
			}
		default:
			return fmt.Errorf("build: media /%s: %w", rel, err)
		}
	}
	return nil
}

// reportBrokenMedia records a broken local media reference (rel is not in either
// store). It is surfaced twice: once build-wide as a single line, and once per
// page in that page's report row (as "Broken Media: /path"), so the build page's
// per-page Warnings column shows exactly which pages carry the broken reference —
// which makes the referencing-page list redundant in the build-wide line.
func (b *builder) reportBrokenMedia(rel string, pages []string) {
	b.report.Warnings = append(b.report.Warnings,
		"Broken media reference (not in the Media library): /"+rel)
	pageMsg := "Broken Media: /" + rel
	for i := range b.report.Pages {
		if contains(pages, b.report.Pages[i].Path) {
			b.report.Pages[i].Warnings = append(b.report.Pages[i].Warnings, pageMsg)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// withinDir reports whether the cleaned path full is dir itself or nested under
// it — the guard used to keep build output from escaping the output directory.
func withinDir(dir, full string) bool {
	dir = filepath.Clean(dir)
	return full == dir || strings.HasPrefix(full, dir+string(filepath.Separator))
}

// write writes data to rel (a forward-slash relative path) under the output dir
// and records its content hash. rel keys stay slash-style for deterministic,
// cross-platform build.json output.
func (b *builder) write(rel string, data []byte) error {
	full := filepath.Join(b.outDir, filepath.FromSlash(rel))
	// Defense in depth against path traversal: never write outside the output dir,
	// even if a stored page path somehow contains "..". The editor validates paths
	// (issue #1), but the build must not be the weak link.
	if !withinDir(b.outDir, full) {
		return fmt.Errorf("build: refusing to write %q outside the output dir (path traversal)", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("build: mkdir for %s: %w", rel, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return fmt.Errorf("build: write %s: %w", rel, err)
	}
	b.hashes[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
	b.report.Files = append(b.report.Files, rel)
	return nil
}

// writeBuildJSON writes build.json. It is not self-hashed (it contains the
// hashes of every other file).
func (b *builder) writeBuildJSON() error {
	payload := struct {
		Version     string            `json:"version"`
		BuildNumber string            `json:"buildNumber"`
		FrameSrc    []string          `json:"frameSrc,omitempty"`
		Metrics     bool              `json:"metrics,omitempty"`
		Files       map[string]string `json:"files"`
	}{b.cfg.Version, b.cfg.BuildNumber, b.frameSrc, b.cfg.Metrics, b.hashes}

	data, err := manifest.Encode(payload)
	if err != nil {
		return err
	}
	full := filepath.Join(b.outDir, "build.json")
	if err := os.MkdirAll(b.outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return fmt.Errorf("build: write build.json: %w", err)
	}
	b.report.Files = append(b.report.Files, "build.json")
	return nil
}

// ValidateGPCDate checks the optional GPC lastUpdate value: empty is valid (the
// field is omitted from gpc.json), otherwise it must be an ISO 8601 calendar date
// (YYYY-MM-DD) so the published /.well-known/gpc.json stays spec-valid. Shared by
// the editor's Settings save and the CLI's -gpc flag.
func ValidateGPCDate(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("GPC lastUpdate must be a date in YYYY-MM-DD format (e.g. 2026-07-30), or blank")
	}
	return nil
}

// gpcJSON builds the /.well-known/gpc.json body. Per the GPC spec only "gpc" is
// required; "lastUpdate" is an optional ISO date, so when the operator has not
// set one it is omitted rather than emitted as an invalid empty-string date.
func gpcJSON(lastUpdate string) []byte {
	if strings.TrimSpace(lastUpdate) == "" {
		return []byte("{\n  \"gpc\": true\n}\n")
	}
	return []byte(fmt.Sprintf("{\n  \"gpc\": true,\n  \"lastUpdate\": %q\n}\n", lastUpdate))
}

// ValidateSecurityContact checks one RFC 9116 Contact value (§7.6): a mailto:, tel:,
// or https:// URI. Blank is rejected (callers skip blanks before validating).
func ValidateSecurityContact(s string) error {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "mailto:") || strings.HasPrefix(s, "tel:") {
		return nil
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "https" && u.Host != "" {
		return nil
	}
	return fmt.Errorf("security contact %q must be a mailto:, tel:, or https:// URI", s)
}

// NormalizeSecurityExpires accepts an RFC 3339 timestamp or a plain date (normalized
// to midnight UTC) and returns the RFC 3339 form; ok=false if it is neither. RFC 9116
// requires Expires to be a timestamp, so a date is promoted rather than emitted as-is.
func NormalizeSecurityExpires(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	return "", false
}

// securityTxt builds the /.well-known/security.txt body (RFC 9116, §7.6). It returns
// nil (the file is not emitted) when no Contact is set. Fields are written in a fixed
// order so the output is deterministic.
func securityTxt(cfg Config) []byte {
	var contacts []string
	for _, c := range cfg.SecurityContacts {
		if c = strings.TrimSpace(c); c != "" {
			contacts = append(contacts, c)
		}
	}
	if len(contacts) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("# Security contact information for this site (RFC 9116).\n")
	for _, c := range contacts {
		fmt.Fprintf(&b, "Contact: %s\n", c)
	}
	field := func(name, val string) {
		if val = strings.TrimSpace(val); val != "" {
			fmt.Fprintf(&b, "%s: %s\n", name, val)
		}
	}
	field("Expires", cfg.SecurityExpires)
	field("Encryption", cfg.SecurityEncryption)
	field("Policy", cfg.SecurityPolicy)
	field("Acknowledgments", cfg.SecurityAcknowledgments)
	field("Preferred-Languages", cfg.SecurityLanguages)
	return []byte(b.String())
}

// pathToFile maps a page path to its output HTML file (pretty URLs):
// "/" -> "index.html", "/blog/post" -> "blog/post/index.html".
func pathToFile(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return "index.html"
	}
	return p + "/index.html"
}

// pathToManifest maps a page path to its manifest file:
// "/" -> "manifest/index.json", "/blog/post" -> "manifest/blog/post.json".
func pathToManifest(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		p = "index"
	}
	return "manifest/" + p + ".json"
}

// firstPartyPred returns a predicate matching a host against the operator's own
// domains (exact host or a subdomain of one).
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
