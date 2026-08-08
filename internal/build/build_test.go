package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// publish creates a page at path with the given markdown body and publishes it.
func publish(t *testing.T, s *store.Store, path, title, body string) {
	t.Helper()
	pid, err := s.CreatePage(store.Page{Path: path, Slug: strings.Trim(path, "/"), Title: title})
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	content, _ := json.Marshal(map[string]string{"body": body})
	rid, err := s.SaveRevision(pid, string(content), "editor")
	if err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatalf("publish %s: %v", path, err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// readAsset reads a fingerprinted asset by its base name (e.g. "pbcssg-search"
// matches assets/pbcssg-search.<hash>.js).
func readAsset(t *testing.T, dir, base string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, "assets", base+".*"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one asset for %q, got %v", base, matches)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func buildFixture(t *testing.T, outDir string) *Report {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publish(t, s, "/", "Home",
		"# Home\n\n[Tracker](https://tracker.example/x) and ![pixel](https://cdn.example/p.gif) and [about](/about).")
	publish(t, s, "/about", "About", "# About\n\nNothing external here.")

	rep, err := Run(s, Config{
		SiteName:      "TUL",
		BaseURL:       "https://tul.example",
		Version:       "1.0",
		BuildNumber:   "7",
		GPCLastUpdate: "2026-07-27",
	}, outDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func TestBuildBundle(t *testing.T) {
	out := t.TempDir()
	rep := buildFixture(t, out)

	// --- page HTML ---
	index := read(t, out, "index.html")
	if !strings.Contains(index, ">Home</h1>") { // h1 carries a goldmark auto ID (SPEC §6.12)
		t.Errorf("home not rendered:\n%s", index)
	}
	if !strings.Contains(index, "© TUL") || !strings.Contains(index, "<title>Home · TUL</title>") {
		t.Errorf("layout/footer missing:\n%s", index)
	}
	// Default header brand (text) lands in the built header as a wordmark home link.
	if !strings.Contains(index, `<a class="pbcssg-brand" href="/">`) || !strings.Contains(index, `<span class="pbcssg-brand-text">TUL</span>`) {
		t.Errorf("default text brand not rendered in header:\n%s", index)
	}
	// External anchor hardened, external image lazied, same-origin /about untouched.
	if strings.Count(index, `rel="noopener noreferrer"`) != 1 {
		t.Errorf("expected exactly one hardened external anchor:\n%s", index)
	}
	if !strings.Contains(index, `loading="lazy"`) {
		t.Errorf("external image should be lazied:\n%s", index)
	}
	if !strings.Contains(index, `href="/about"`) {
		t.Errorf("same-origin link should survive:\n%s", index)
	}
	if !strings.Contains(read(t, out, "about/index.html"), ">About</h1>") {
		t.Errorf("about page not rendered")
	}

	// --- theme: fingerprinted stylesheet, linked on the page ---
	if !strings.Contains(index, `<link rel="stylesheet" href="/assets/theme.`) {
		t.Errorf("theme stylesheet not linked:\n%s", index)
	}
	if css := readAsset(t, out, "theme"); !strings.Contains(css, "pbcssg-consent-card") {
		t.Errorf("theme css not emitted with expected content")
	}

	// --- light/dark theme script: fingerprinted, loaded on every page, and the
	// footer toggle rendered ---
	if !strings.Contains(index, `<script src="/assets/pbcssg-theme.`) {
		t.Errorf("theme script not loaded on the page:\n%s", index)
	}
	if !strings.Contains(index, "data-pbcssg-theme-toggle") {
		t.Errorf("footer theme toggle not rendered:\n%s", index)
	}
	if js := readAsset(t, out, "pbcssg-theme"); !strings.Contains(js, "pbcssg-theme") || !strings.Contains(js, "localStorage") {
		t.Errorf("theme script not emitted with expected content")
	}

	// --- per-page manifest ---
	var pm struct {
		Summary struct {
			External   int    `json:"external"`
			Domains    int    `json:"domains"`
			WorstGrade string `json:"worstGrade"`
		} `json:"summary"`
		Domains []struct {
			Domain string `json:"domain"`
		} `json:"domains"`
	}
	if err := json.Unmarshal([]byte(read(t, out, "manifest/index.json")), &pm); err != nil {
		t.Fatalf("index manifest invalid: %v", err)
	}
	if pm.Summary.External != 2 || pm.Summary.Domains != 2 {
		t.Errorf("home manifest summary = %+v, want external=2 domains=2", pm.Summary)
	}
	if pm.Summary.WorstGrade != "?" { // tracker.example/cdn.example are unclassified
		t.Errorf("worstGrade = %q, want ?", pm.Summary.WorstGrade)
	}

	// --- site manifest, gpc, build.json ---
	var site struct {
		Pages   []any `json:"pages"`
		Domains []any `json:"domains"`
	}
	if err := json.Unmarshal([]byte(read(t, out, "manifest/site.json")), &site); err != nil {
		t.Fatalf("site manifest invalid: %v", err)
	}
	// 2 content pages + the /classification report (whose module link adds github.com
	// as a third external domain — the report is transparent about its own link).
	if len(site.Pages) != 3 || len(site.Domains) != 3 {
		t.Errorf("site manifest: pages=%d domains=%d, want 3/3", len(site.Pages), len(site.Domains))
	}

	gpc := read(t, out, ".well-known/gpc.json")
	if !strings.Contains(gpc, `"gpc": true`) || !strings.Contains(gpc, "2026-07-27") {
		t.Errorf("gpc.json wrong:\n%s", gpc)
	}

	var bj struct {
		Version     string            `json:"version"`
		BuildNumber string            `json:"buildNumber"`
		Files       map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(read(t, out, "build.json")), &bj); err != nil {
		t.Fatalf("build.json invalid: %v", err)
	}
	if bj.Version != "1.0" || bj.BuildNumber != "7" {
		t.Errorf("build.json meta = %+v", bj)
	}
	if _, ok := bj.Files["index.html"]; !ok {
		t.Errorf("build.json files missing index.html: %v", bj.Files)
	}
	if _, ok := bj.Files["build.json"]; ok {
		t.Errorf("build.json must not hash itself")
	}

	// --- report --- (2 content pages + the always-emitted /classification page)
	if len(rep.Pages) != 3 {
		t.Errorf("report pages = %d, want 3", len(rep.Pages))
	}
}

func TestBuildDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	buildFixture(t, a)
	buildFixture(t, b)

	for _, f := range []string{"index.html", "about/index.html", "manifest/index.json", "manifest/site.json", "build.json"} {
		if read(t, a, f) != read(t, b, f) {
			t.Errorf("non-deterministic output for %s", f)
		}
	}
}

// publishRaw publishes a page with a raw content_json payload.
func publishRaw(t *testing.T, s *store.Store, path, slug, title, contentJSON string) {
	t.Helper()
	pid, err := s.CreatePage(store.Page{Path: path, Slug: slug, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	rid, err := s.SaveRevision(pid, contentJSON, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
}

func hasDomain(t *testing.T, dir, manifestRel, domain string) bool {
	t.Helper()
	var m struct {
		Domains []struct {
			Domain string `json:"domain"`
		} `json:"domains"`
	}
	if err := json.Unmarshal([]byte(read(t, dir, manifestRel)), &m); err != nil {
		t.Fatalf("%s invalid: %v", manifestRel, err)
	}
	for _, d := range m.Domains {
		if d.Domain == domain {
			return true
		}
	}
	return false
}

func TestBuildYouTubeFieldblock(t *testing.T) {
	out := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/watch", "watch", "Watch",
		`{"body":"# Watch","blocks":[{"type":"youtube","youtube":{`+
			`"videoId":"abc123","name":"degoogle","title":"How I degoogled",`+
			`"transcript":"Today we cover privacy.","descriptionLinks":["https://privacyguides.example"]}}]}`)

	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example",
		Version: "1.0", BuildNumber: "1", GPCLastUpdate: "2026-07-27",
	}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// --- host page: consent card, no third party ---
	host := read(t, out, "watch/index.html")
	if !strings.Contains(host, "External video · How I degoogled") ||
		!strings.Contains(host, `href="/external/youtube/degoogle"`) {
		t.Errorf("host page missing consent card:\n%s", host)
	}
	for _, bad := range []string{"<iframe", "youtube-nocookie", "youtube.com"} {
		if strings.Contains(host, bad) {
			t.Errorf("host page must not reference %q:\n%s", bad, host)
		}
	}
	if hasDomain(t, out, "manifest/watch.json", "youtube-nocookie.com") {
		t.Errorf("host manifest must not list youtube-nocookie (nothing loads on the host page)")
	}

	// --- external page: facade + honest manifest ---
	ext := read(t, out, "external/youtube/degoogle/index.html")
	for _, want := range []string{
		`data-video-id="abc123"`,
		"▶ Play — loads YouTube",
		"<h1>How I degoogled</h1>",
		`href="https://privacyguides.example"`,
		`src="/assets/pbcssg-youtube.`, // fingerprinted facade script
	} {
		if !strings.Contains(ext, want) {
			t.Errorf("external page missing %q:\n%s", want, ext)
		}
	}
	if strings.Contains(ext, "<iframe") {
		t.Errorf("external page must not embed before playback:\n%s", ext)
	}
	if !hasDomain(t, out, "manifest/external/youtube/degoogle.json", "youtube-nocookie.com") {
		t.Errorf("external manifest must honestly list youtube-nocookie (the facade target)")
	}
	if !hasDomain(t, out, "manifest/external/youtube/degoogle.json", "privacyguides.example") {
		t.Errorf("external manifest must list the classified description link")
	}

	// --- facade script emitted once ---
	if js := readAsset(t, out, "pbcssg-youtube"); !strings.Contains(js, "youtube-nocookie.com/embed/") {
		t.Errorf("facade script not written correctly")
	}
}

func TestBuildSearch(t *testing.T) {
	out := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Welcome\n\nA post about privacy and degoogling.")
	publish(t, s, "/about", "About", "# About us")

	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", Search: true,
	}, out); err != nil {
		t.Fatal(err)
	}

	raw := read(t, out, "search/index.json")
	var idx struct {
		Docs []struct {
			URL, Title, Text string
		} `json:"docs"`
	}
	if err := json.Unmarshal([]byte(raw), &idx); err != nil {
		t.Fatalf("index invalid: %v", err)
	}
	if len(idx.Docs) != 2 {
		t.Fatalf("index docs = %d, want 2", len(idx.Docs))
	}
	if !strings.Contains(raw, "privacy") || !strings.Contains(raw, "Welcome") {
		t.Errorf("index missing content:\n%s", raw)
	}
	if js := readAsset(t, out, "pbcssg-search"); !strings.Contains(js, "index.json") {
		t.Errorf("search client script not emitted")
	}
	home := read(t, out, "index.html")
	for _, want := range []string{`role="search"`, "data-pbcssg-search", `src="/assets/pbcssg-search.`} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing search widget %q:\n%s", want, home)
		}
	}
}

func TestBuildSearchExcludesNoIndex(t *testing.T) {
	out := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Welcome\n\npublicwordZZZ here.")
	publishRaw(t, s, "/secret", "secret", "Secret",
		`{"body":"# SecretPage\n\nsecretcontentZZZ hidden here.","noIndex":true}`)

	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", Search: true,
	}, out); err != nil {
		t.Fatal(err)
	}

	raw := read(t, out, "search/index.json")
	var idx struct {
		Docs []struct{ URL, Text string } `json:"docs"`
	}
	if err := json.Unmarshal([]byte(raw), &idx); err != nil {
		t.Fatalf("index invalid: %v", err)
	}
	for _, d := range idx.Docs {
		if d.URL == "/secret" {
			t.Errorf("noindex page must not appear in the search index: %+v", d)
		}
	}
	if strings.Contains(raw, "secretcontentZZZ") {
		t.Errorf("noindex page content leaked into search/index.json:\n%s", raw)
	}
	if !strings.Contains(raw, "publicwordZZZ") {
		t.Errorf("indexable page missing from search/index.json:\n%s", raw)
	}
}

func TestBuildErrorPages(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")

	out := t.TempDir()
	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Search: true, Sitemap: true,
		ErrorPages: map[string]string{"403": "# Custom403ZZZ\n\nmy own words."},
	}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// All curated pages are emitted at the bundle root.
	for _, ep := range ErrorPages {
		if _, err := os.Stat(filepath.Join(out, ep.Name+".html")); err != nil {
			t.Errorf("missing error page %s.html: %v", ep.Name, err)
		}
	}

	// The 404 page: default heading, ROOT-ABSOLUTE assets (so it styles at any
	// depth), noindex, and a home link.
	nf := read(t, out, "404.html")
	for _, want := range []string{"Page not found", `href="/assets/`, "noindex", "Return home"} {
		if !strings.Contains(nf, want) {
			t.Errorf("404.html missing %q:\n%s", want, nf)
		}
	}

	// A configured message overrides the default body; the default body text is gone.
	forbidden := read(t, out, "403.html")
	if !strings.Contains(forbidden, "Custom403ZZZ") {
		t.Errorf("403.html did not use the configured message:\n%s", forbidden)
	}
	if strings.Contains(forbidden, "permission to view") {
		t.Errorf("403.html still shows the default message body despite an override")
	}

	// Error pages are not page-tree pages: absent from sitemap and search index.
	if sm := read(t, out, "sitemap.xml"); strings.Contains(sm, "/404.html") {
		t.Errorf("error page leaked into sitemap.xml:\n%s", sm)
	}
	if idx := read(t, out, "search/index.json"); strings.Contains(idx, "Page not found") {
		t.Errorf("error page content leaked into the search index:\n%s", idx)
	}
}

func TestBuildUnlistedPage(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A normal post and an unlisted members post — both tagged "members", both under
	// the feed glob, both with an external link.
	publishRaw(t, s, "/members/public", "public", "Public Post",
		`{"body":"# Public\n\n[x](https://public-ext.example/a)","tags":["members"],"isPost":true}`)
	publishRaw(t, s, "/members/secretmemberpost", "secretmemberpost", "SecretTitleZZZ",
		`{"body":"# Secret\n\n[t](https://tracker.example/x)","tags":["members"],"isPost":true,"unlisted":true}`)

	out := t.TempDir()
	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Search: true, Sitemap: true,
		Feeds: []FeedRule{{Name: "members", Glob: "/members/*", Listed: true}},
	}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The unlisted page IS built and served, and KEEPS its in-page External
	// References section (members still see the privacy ratings) + a noindex meta.
	page := read(t, out, "members/secretmemberpost/index.html")
	if !strings.Contains(page, "tracker.example") {
		t.Errorf("unlisted page lost its in-page External References section:\n%s", page)
	}
	if !strings.Contains(page, "noindex") {
		t.Errorf("unlisted page should carry a noindex meta")
	}

	// ...but it appears in NO generated listing or manifest.
	if sm := read(t, out, "sitemap.xml"); strings.Contains(sm, "/members/secretmemberpost") {
		t.Errorf("unlisted page leaked into sitemap.xml")
	}
	if idx := read(t, out, "search/index.json"); strings.Contains(idx, "SecretTitleZZZ") {
		t.Errorf("unlisted page leaked into the search index")
	}
	tagPage := read(t, out, "tags/members/index.html")
	if strings.Contains(tagPage, "SecretTitleZZZ") {
		t.Errorf("unlisted page listed on its /tags/ page")
	}
	if !strings.Contains(tagPage, "Public Post") {
		t.Errorf("tag page should still list the public post (control)")
	}
	if feed := read(t, out, "feeds/members.rss"); strings.Contains(feed, "SecretTitleZZZ") {
		t.Errorf("unlisted page syndicated into the feed")
	}
	site := read(t, out, "manifest/site.json")
	if strings.Contains(site, "/members/secretmemberpost") || strings.Contains(site, "tracker.example") {
		t.Errorf("unlisted page (or its external domain) leaked into manifest/site.json:\n%s", site)
	}
	if !strings.Contains(site, "/members/public") {
		t.Errorf("public post should be in the site manifest (control)")
	}
	// No per-page manifest for the unlisted page (its path would reveal it); the
	// public post does get one (control).
	if _, err := os.Stat(filepath.Join(out, "manifest", "members", "secretmemberpost.json")); !os.IsNotExist(err) {
		t.Errorf("unlisted page emitted a per-page manifest (path disclosure): %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest", "members", "public.json")); err != nil {
		t.Errorf("public post missing its per-page manifest: %v", err)
	}
}

func TestBuildNoSearchByDefault(t *testing.T) {
	out := t.TempDir()
	buildFixture(t, out) // Config.Search is false
	if _, err := os.Stat(filepath.Join(out, "search", "index.json")); !os.IsNotExist(err) {
		t.Errorf("search index should not exist when Search is off")
	}
	if strings.Contains(read(t, out, "index.html"), "data-pbcssg-search") {
		t.Errorf("search widget should be absent when Search is off")
	}
}

func TestBuildEmitsFeeds(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub := func(path, title, summary string) {
		pid, _ := s.CreatePage(store.Page{Path: path, Slug: "s", Title: title})
		rid, _ := s.SaveRevision(pid, `{"body":"# `+title+`","summary":"`+summary+`"}`, "")
		if err := s.Publish(pid, rid); err != nil {
			t.Fatal(err)
		}
	}
	pub("/blog/first", "First Post", "About privacy")
	pub("/blog/second", "Second Post", "More privacy")
	pub("/about", "About", "not in the feed")

	out := t.TempDir()
	rep, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Feeds: []FeedRule{{Name: "blog", Glob: "/blog/*", Title: "TUL Blog"}},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}

	rss := read(t, out, "feeds/blog.rss")
	for _, want := range []string{
		"<title>TUL Blog</title>",
		"https://tul.example/blog/first",
		"https://tul.example/blog/second",
		"About privacy",
	} {
		if !strings.Contains(rss, want) {
			t.Errorf("blog.rss missing %q", want)
		}
	}
	if strings.Contains(rss, "/about") {
		t.Errorf("non-matching page must not be in the feed")
	}
	if atom := read(t, out, "feeds/blog.atom"); !strings.Contains(atom, `xmlns="http://www.w3.org/2005/Atom"`) {
		t.Errorf("blog.atom not an Atom feed")
	}
	// A matching page carries feed auto-discovery links; a non-matching one doesn't.
	if !strings.Contains(read(t, out, "blog/first/index.html"), `rel="alternate" type="application/rss+xml" href="/feeds/blog.rss"`) {
		t.Errorf("blog post missing feed auto-discovery link")
	}
	if strings.Contains(read(t, out, "about/index.html"), "application/rss+xml") {
		t.Errorf("non-feed page should not advertise a feed")
	}
}

func TestBuildInjectsExtRefList(t *testing.T) {
	out := t.TempDir()
	buildFixture(t, out)

	// The home page references 2 external domains (tracker.example, cdn.example),
	// both unclassified, so it carries the per-domain external-references listing
	// before the footer with the classifier's reason, and the slot is consumed.
	index := read(t, out, "index.html")
	for _, want := range []string{
		`class="pbcssg-extref-heading">External references</h2>`,
		`<code>tracker.example</code>`,
		`<code>cdn.example</code>`,
		`<span class="pbcssg-grade pbcssg-grade-unknown" title="Privacy grade ?">?</span>`,
		"Unclassified",
		`<small class="pbcssg-extref-reason">No classification on record for this domain</small>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("home page missing external-references listing fragment %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, render.ExtRefSlot) {
		t.Errorf("layout ExtRefSlot should have been replaced by the listing")
	}
	// The listing sits between the page content and the footer.
	if li, fi := strings.Index(index, "pbcssg-extref"), strings.Index(index, "pbcssg-footer"); li < 0 || fi < 0 || li > fi {
		t.Errorf("listing should appear before the footer (list=%d footer=%d)", li, fi)
	}

	// The about page has no external references: no listing, and the slot is removed.
	about := read(t, out, "about/index.html")
	if strings.Contains(about, "pbcssg-extref") {
		t.Errorf("about page has no externals; it must not carry the listing:\n%s", about)
	}
	if strings.Contains(about, render.ExtRefSlot) {
		t.Errorf("about page still has the unreplaced ExtRefSlot:\n%s", about)
	}
}

// TestBuildAutolinkHardenedAndClassified proves the privacy-pipeline guarantee for
// GFM Linkify (SPEC §6.12): a bare URL written as plain prose is promoted to a real
// anchor by goldmark, then flows through the SAME hygiene + classification passes as
// a hand-authored link — so it is hardened (rel + referrer policy) and listed in the
// page's external-references section. A URL inside a code span stays literal (no
// anchor, no external-reference entry), giving the author editorial control.
func TestBuildAutolinkHardenedAndClassified(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publish(t, s, "/", "Home",
		"# Home\n\nVisit https://autolink.example/x for details.\n\nBut `https://incode.example/y` stays literal.")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	index := read(t, out, "index.html")

	// The prose URL became a hardened external anchor (attribute order is not asserted —
	// the anchors/toc round-trip alphabetizes attributes deterministically).
	if !strings.Contains(index, `<a href="https://autolink.example/x"`) ||
		!strings.Contains(index, `rel="noopener noreferrer"`) ||
		!strings.Contains(index, `referrerpolicy="no-referrer"`) {
		t.Errorf("autolinked URL not hardened:\n%s", index)
	}
	// ...and it is classified in the external-references listing.
	if !strings.Contains(index, `<code>autolink.example</code>`) {
		t.Errorf("autolinked host missing from external references:\n%s", index)
	}
	// The in-code URL is inert: rendered as <code>, never an anchor, and not classified.
	if strings.Contains(index, `href="https://incode.example/y"`) {
		t.Errorf("URL inside a code span must not be autolinked:\n%s", index)
	}
	if strings.Contains(index, `<code>incode.example</code>`) {
		t.Errorf("in-code URL host must not appear in external references:\n%s", index)
	}
}

// TestBuildCodeBlockCopyScript verifies the code block (SPEC §6.12): a page with a
// code block links the fingerprinted, self-hosted copy script and emits it once; a
// page without one never ships it.
func TestBuildCodeBlockCopyScript(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A page carrying a code block (published via a raw content_json so we can attach
	// a block), and a plain page with none.
	publishRaw(t, s, "/snippet", "snippet", "Snippet",
		`{"body":"# Snippet","blocks":[{"type":"code","code":{"text":"package main","filename":"main.go","lineNumbers":true}}]}`)
	publish(t, s, "/plain", "Plain", "# Plain\n\nNo code here.")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	page := read(t, out, "snippet/index.html")
	if !strings.Contains(page, `<figure class="pbcssg-code pbcssg-code--numbered">`) {
		t.Errorf("code block markup missing:\n%s", page)
	}
	// Serialization-agnostic (the HTML serializer emits defer=""): assert the src.
	if !strings.Contains(page, `<script src="/assets/pbcssg-codecopy.`) {
		t.Errorf("code page should link the fingerprinted copy script:\n%s", page)
	}
	if js := readAsset(t, out, "pbcssg-codecopy"); !strings.Contains(js, "data-pbcssg-copy") || !strings.Contains(js, "clipboard") {
		t.Errorf("pbcssg-codecopy.js content unexpected")
	}
	// The plain page ships no copy script.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "pbcssg-codecopy") {
		t.Errorf("a page with no code block must not link the copy script:\n%s", plain)
	}
}

// TestBuildAnchorsAndTOC verifies heading anchors + the toc block (SPEC §6.12) in a
// real build: content headings get ids + permalink anchors, a toc block is filled
// with their nested list, and the build-injected "External references" chrome heading
// is neither anchored nor listed (the pass runs before that heading is injected).
func TestBuildAnchorsAndTOC(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A page with a toc block, two markdown headings, and an external link (so the
	// external-references chrome heading is present on the page).
	publishRaw(t, s, "/guide", "guide", "Guide",
		`{"body":"# Guide\n\n## First\n\ntext with https://ext.example here\n\n## Second\n\nmore",`+
			`"blocks":[{"type":"toc","toc":{"depth":3,"title":"On this page"}}]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "guide/index.html")

	// Content headings are anchored.
	if !strings.Contains(page, `<h2 id="first">First<a class="pbcssg-anchor" href="#first"`) {
		t.Errorf("h2 First not anchored:\n%s", page)
	}
	// The toc block is filled with the content headings.
	for _, want := range []string{
		`<p class="pbcssg-toc-title">On this page</p>`,
		`<a href="#first">First</a>`,
		`<a href="#second">Second</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("TOC missing %q:\n%s", want, page)
		}
	}
	// The external-references chrome heading is present but NOT anchored, and NOT in
	// the TOC (its slug would be "external-references").
	if !strings.Contains(page, `<h2 id="pbcssg-extref-heading" class="pbcssg-extref-heading">External references</h2>`) {
		t.Errorf("extref heading should be untouched:\n%s", page)
	}
	if strings.Contains(page, `href="#external-references"`) {
		t.Errorf("the external-references chrome heading must not be in the TOC:\n%s", page)
	}
}

// TestBuildReadingTime verifies reading time (SPEC §6.13): with the setting on, a
// post shows "~N min read" relocated to just after its title; an ordinary page and a
// build with the setting off show nothing.
func TestBuildReadingTime(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/post", "post", "Post", `{"body":"# My Post\n\nSome words here.","isPost":true}`)
	publishRaw(t, s, "/page", "page", "Page", `{"body":"# My Page\n\nSome words here."}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", ShowReadingTime: true}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	post := read(t, out, "post/index.html")
	// Relocated after the h1, marker stripped.
	if !strings.Contains(post, `<h1 id="my-post">My Post`) ||
		!strings.Contains(post, `<p class="pbcssg-post-meta">~1 min read</p>`) {
		t.Errorf("post should show reading time after the title:\n%s", post)
	}
	if strings.Contains(post, "data-pbcssg-readingtime") {
		t.Errorf("reading-time marker attr should be stripped:\n%s", post)
	}
	// An ordinary page (not a post) shows nothing.
	if page := read(t, out, "page/index.html"); strings.Contains(page, "min read") {
		t.Errorf("non-post page must not show reading time:\n%s", page)
	}

	// With the setting off, even a post shows nothing.
	out2 := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out2); err != nil {
		t.Fatal(err)
	}
	if post := read(t, out2, "post/index.html"); strings.Contains(post, "min read") {
		t.Errorf("reading time must be off unless enabled:\n%s", post)
	}
}

// TestBuildRelatedPosts verifies the related-posts block end-to-end (SPEC §6.13):
// two posts sharing a tag each list the other; a non-post sharing the tag is excluded.
func TestBuildRelatedPosts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	relBlock := `[{"type":"related","related":{"count":5}}]`
	publishRaw(t, s, "/one", "one", "Post One", `{"body":"# One","isPost":true,"tags":["go"],"blocks":`+relBlock+`}`)
	publishRaw(t, s, "/two", "two", "Post Two", `{"body":"# Two","isPost":true,"tags":["go"],"blocks":`+relBlock+`}`)
	// A non-post sharing the tag must never appear in a related list.
	publishRaw(t, s, "/page", "page", "Plain Page", `{"body":"# Plain","tags":["go"]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	one := read(t, out, "one/index.html")
	if !strings.Contains(one, `<nav class="pbcssg-related"`) || !strings.Contains(one, `<a href="/two">Post Two</a>`) {
		t.Errorf("post one should list post two as related:\n%s", one)
	}
	if strings.Contains(one, `href="/one"`) || strings.Contains(one, "Plain Page") {
		t.Errorf("related list must exclude self and non-posts:\n%s", one)
	}
	if two := read(t, out, "two/index.html"); !strings.Contains(two, `<a href="/one">Post One</a>`) {
		t.Errorf("post two should list post one as related:\n%s", two)
	}
}

// TestBuildGalleryByTag verifies the gallery block's by-tag mode (SPEC §6.14): the
// build resolves the tag to the library images carrying it (alt from each image's
// note), renders the grid, and emits the referenced images.
func TestBuildGalleryByTag(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	put := func(sha, name string) {
		if err := s.PutAsset(store.AssetData{Asset: store.Asset{SHA256: sha, Filename: name, Format: "png", MIME: "image/png"}, Data: []byte(sha)}); err != nil {
			t.Fatal(err)
		}
	}
	put("aa11", "cat.png")
	put("bb22", "dog.png")
	put("cc33", "car.png")
	if err := s.SetMediaTags("aa11", []string{"pets"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaTags("bb22", []string{"pets"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaTags("cc33", []string{"vehicles"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaNote("aa11", "A cat"); err != nil {
		t.Fatal(err)
	}

	publishRaw(t, s, "/gallery", "gallery", "Gallery",
		`{"body":"# Pics","blocks":[{"type":"gallery","gallery":{"mode":"tag","tag":"pets","sort":"name","columns":3}}]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "gallery/index.html")

	// Both tagged images are present; the non-tagged one is not. Alt comes from the note.
	if !strings.Contains(page, `<img src="/media/aa11.png" alt="A cat" loading="lazy"`) { // self-close-agnostic
		t.Errorf("tagged image with note-alt missing:\n%s", page)
	}
	if !strings.Contains(page, `src="/media/bb22.png"`) {
		t.Errorf("second tagged image missing:\n%s", page)
	}
	if strings.Contains(page, "cc33") {
		t.Errorf("non-tagged image must not appear:\n%s", page)
	}
	// name sort → cat.png before dog.png.
	if strings.Index(page, "aa11.png") > strings.Index(page, "bb22.png") {
		t.Errorf("name sort should put cat.png before dog.png:\n%s", page)
	}
	// (Byte emission of the referenced /media/<sha> images is the standard media path,
	// exercised by the image-block media tests; the gallery renders identical <img>.)
}

// TestBuildShareBlock verifies the share block (SPEC §6.15): a page with one links the
// fingerprinted, self-hosted share script and renders the controls; a page without one
// ships nothing. The block introduces no external reference.
func TestBuildShareBlock(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/post", "post", "Post",
		`{"body":"# Post","blocks":[{"type":"share","share":{"copyLink":true,"email":true,"mastodon":true}}]}`)
	publish(t, s, "/plain", "Plain", "# Plain")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "post/index.html")
	if !strings.Contains(page, `<nav class="pbcssg-share"`) || !strings.Contains(page, "data-pbcssg-share-copy") {
		t.Errorf("share controls missing:\n%s", page)
	}
	if !strings.Contains(page, `<script src="/assets/pbcssg-share.`) {
		t.Errorf("share page should link the fingerprinted share script:\n%s", page)
	}
	if js := readAsset(t, out, "pbcssg-share"); !strings.Contains(js, "data-pbcssg-share-mastodon") {
		t.Errorf("pbcssg-share.js content unexpected")
	}
	// The block adds no external reference (mailto: has no host; the Mastodon intent is
	// built client-side on click), so the post carries no external-references listing.
	if strings.Contains(page, "pbcssg-extref") {
		t.Errorf("share block must not introduce an external reference:\n%s", page)
	}
	// A plain page ships no share script.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "pbcssg-share") {
		t.Errorf("a page with no share block must not link the share script:\n%s", plain)
	}
}

// TestBuildCommentsBlock verifies the comments placement block (SPEC §7.3): a page
// carrying one renders the widget mount point keyed by its path and links the fixed,
// same-origin widget assets (served live by the dynamic layer, not bundled). A page
// without the block links nothing, and the widget is never written into the bundle.
func TestBuildCommentsBlock(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/post", "post", "Post",
		`{"body":"# Post","blocks":[{"type":"comments"}]}`)
	publish(t, s, "/plain", "Plain", "# Plain")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "post/index.html")
	// Substrings, not full tags: the build re-serializes HTML, so void tags self-close
	// (<link .../>) and boolean attrs expand (defer="").
	for _, want := range []string{
		`<section class="pbc-comments" data-pbc-comments="/post">`,
		`href="/_pbc/assets/comments.css"`,
		`src="/_pbc/assets/comments.js"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("comments page missing %q:\n%s", want, page)
		}
	}
	// The widget is served live by the dynamic layer, so it is never fingerprinted into
	// the bundle under /assets (a bundled path would be ="/assets/comments…, not the
	// fixed ="/_pbc/assets/… above).
	if strings.Contains(page, `="/assets/comments`) {
		t.Errorf("comments widget must be linked at the fixed /_pbc path, not a bundled asset:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(out, "_pbc")); !os.IsNotExist(err) {
		t.Errorf("the build must not write the dynamic /_pbc widget into the bundle (err=%v)", err)
	}
	// A plain page links nothing.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "pbc-comments") || strings.Contains(plain, "/_pbc/assets/comments") {
		t.Errorf("a page with no comments block must not reference the widget:\n%s", plain)
	}
}

// TestBuildOGImage verifies the social-preview image (SPEC §6.3): a per-page image
// wins and is emitted as an absolute og:image + twitter tags; a page without one falls
// back to the Settings site default; both are omitted when Open Graph is off.
func TestBuildOGImage(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/post", "post", "Post", `{"body":"# Post","ogImage":"/media/aa.png"}`)
	publishRaw(t, s, "/plain", "plain", "Plain", `{"body":"# Plain"}`)

	cfg := Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		OpenGraph: true, OGImageDefault: "/media/default.png"}
	out := t.TempDir()
	if _, err := Run(s, cfg, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Per-page image, absolute, with twitter tags.
	post := read(t, out, "post/index.html")
	// Self-close-agnostic (the HTML serializer emits void tags as <meta .../>).
	for _, want := range []string{
		`<meta property="og:image" content="https://tul.example/media/aa.png"`,
		`<meta name="twitter:card" content="summary_large_image"`,
		`<meta name="twitter:image" content="https://tul.example/media/aa.png"`,
	} {
		if !strings.Contains(post, want) {
			t.Errorf("post og:image missing %q:\n%s", want, post)
		}
	}
	// The tag-less page falls back to the site default (also absolute).
	if plain := read(t, out, "plain/index.html"); !strings.Contains(plain, `<meta property="og:image" content="https://tul.example/media/default.png"`) {
		t.Errorf("plain page should use the site-default og:image:\n%s", plain)
	}

	// With Open Graph off, no og:image is emitted even when set.
	cfg.OpenGraph = false
	out2 := t.TempDir()
	if _, err := Run(s, cfg, out2); err != nil {
		t.Fatal(err)
	}
	if post := read(t, out2, "post/index.html"); strings.Contains(post, "og:image") {
		t.Errorf("og:image must not be emitted when Open Graph is off:\n%s", post)
	}

	// With no Base URL the /media path cannot be made absolute, so og:image is omitted.
	cfg.OpenGraph, cfg.BaseURL = true, ""
	out3 := t.TempDir()
	if _, err := Run(s, cfg, out3); err != nil {
		t.Fatal(err)
	}
	if post := read(t, out3, "post/index.html"); strings.Contains(post, "og:image") {
		t.Errorf("og:image needs a Base URL to be absolute; omit otherwise:\n%s", post)
	}
}

// TestBuildSecurityTxt verifies /.well-known/security.txt (RFC 9116, §7.6): emitted
// with a Contact set (fields in a fixed order) and omitted otherwise.
func TestBuildSecurityTxt(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")

	cfg := Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		SecurityContacts: []string{"mailto:security@tul.example", "https://tul.example/contact"},
		SecurityExpires:  "2027-01-01T00:00:00Z", SecurityEncryption: "https://tul.example/pgp.txt",
		SecurityPolicy: "https://tul.example/policy", SecurityLanguages: "en, de"}
	out := t.TempDir()
	if _, err := Run(s, cfg, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sec := read(t, out, ".well-known/security.txt")
	for _, want := range []string{
		"Contact: mailto:security@tul.example\n",
		"Contact: https://tul.example/contact\n",
		"Expires: 2027-01-01T00:00:00Z\n",
		"Encryption: https://tul.example/pgp.txt\n",
		"Policy: https://tul.example/policy\n",
		"Preferred-Languages: en, de\n",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("security.txt missing %q:\n%s", want, sec)
		}
	}
	// Contact comes before Expires (fixed field order).
	if strings.Index(sec, "Contact:") > strings.Index(sec, "Expires:") {
		t.Errorf("Contact should precede Expires:\n%s", sec)
	}

	// With no Contact, the file is not emitted.
	out2 := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out2, ".well-known", "security.txt")); !os.IsNotExist(err) {
		t.Errorf("security.txt must not be emitted without a Contact")
	}
}

func TestSecurityValidators(t *testing.T) {
	for _, ok := range []string{"mailto:a@b.example", "tel:+15551234", "https://ex.example/c"} {
		if err := ValidateSecurityContact(ok); err != nil {
			t.Errorf("ValidateSecurityContact(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"http://ex.example/c", "ex.example", "javascript:x", ""} {
		if err := ValidateSecurityContact(bad); err == nil {
			t.Errorf("ValidateSecurityContact(%q) should fail", bad)
		}
	}
	// Expires: a date is promoted to RFC 3339; a timestamp is normalized to UTC; junk fails.
	if got, ok := NormalizeSecurityExpires("2027-01-02"); !ok || got != "2027-01-02T00:00:00Z" {
		t.Errorf("NormalizeSecurityExpires(date) = %q,%v", got, ok)
	}
	if got, ok := NormalizeSecurityExpires("2027-01-02T15:04:05Z"); !ok || got != "2027-01-02T15:04:05Z" {
		t.Errorf("NormalizeSecurityExpires(rfc3339) = %q,%v", got, ok)
	}
	if _, ok := NormalizeSecurityExpires("not a date"); ok {
		t.Errorf("NormalizeSecurityExpires(junk) should fail")
	}
}

func TestBuildEmitsFeedsIndex(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub := func(path, title string) {
		pid, _ := s.CreatePage(store.Page{Path: path, Slug: "s", Title: title})
		rid, _ := s.SaveRevision(pid, `{"body":"# `+title+`"}`, "")
		if err := s.Publish(pid, rid); err != nil {
			t.Fatal(err)
		}
	}
	pub("/blog/first", "First Post")
	pub("/news/one", "News One")

	out := t.TempDir()
	rep, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Feeds: []FeedRule{
			{Name: "blog", Glob: "/blog/*", Title: "TUL Blog", Listed: true},
			{Name: "news", Glob: "/news/*"}, // not listed
		},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}

	idx := read(t, out, "feeds/index.html")
	for _, want := range []string{
		"<h1>Feeds</h1>", "TUL Blog",
		`href="/feeds/blog.rss"`, `href="/feeds/blog.atom"`,
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("feeds index missing %q", want)
		}
	}
	// An unlisted feed still emits its files but is not shown on the index page.
	if strings.Contains(idx, "/feeds/news.rss") {
		t.Errorf("unlisted feed must not appear on the /feeds/ index")
	}
	if _, err := os.Stat(filepath.Join(out, "feeds", "news.rss")); err != nil {
		t.Errorf("unlisted feed should still be emitted: %v", err)
	}
}

func TestBuildNoFeedsIndexWhenNoneListed(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/blog/x", Slug: "x", Title: "X"})
	rid, _ := s.SaveRevision(pid, `{"body":"# X"}`, "")
	s.Publish(pid, rid)

	out := t.TempDir()
	if _, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Feeds: []FeedRule{{Name: "blog", Glob: "/blog/*"}},
	}, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "feeds", "index.html")); !os.IsNotExist(err) {
		t.Errorf("no feed marked listed; /feeds/ index should not be emitted")
	}
}

func TestBuildFeedsSkippedWithoutBaseURL(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/blog/x", Slug: "x", Title: "X"})
	rid, _ := s.SaveRevision(pid, `{"body":"# X"}`, "")
	s.Publish(pid, rid)

	out := t.TempDir()
	rep, err := Run(s, Config{Version: "1.0", BuildNumber: "1", Feeds: []FeedRule{{Name: "blog", Glob: "/blog/*"}}}, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "feeds", "blog.rss")); !os.IsNotExist(err) {
		t.Errorf("feeds need a base URL; none should be emitted")
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "feeds skipped") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'feeds skipped' warning, got %v", rep.Warnings)
	}
}

func TestBuildIndexBlock(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub := func(path, title, content string) {
		pid, _ := s.CreatePage(store.Page{Path: path, Slug: "s", Title: title})
		rid, _ := s.SaveRevision(pid, content, "")
		if err := s.Publish(pid, rid); err != nil {
			t.Fatal(err)
		}
	}
	// /blog is an index page containing an index block; two posts + one non-post.
	pub("/blog", "Blog", `{"isIndex":true,"blocks":[{"type":"index","index":{"depth":1,"style":"detailed"}}]}`)
	pub("/blog/first", "First Post", `{"summary":"first summary"}`)
	pub("/blog/second", "Second Post", `{"summary":"second summary"}`)
	pub("/about", "About", `{"body":"# About"}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatal(err)
	}
	html := read(t, out, "blog/index.html")
	for _, want := range []string{
		`href="/blog/first"`, `href="/blog/second"`, "first summary", "pbcssg-index-detailed",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("built /blog missing index content %q", want)
		}
	}
	if strings.Contains(html, `href="/about"`) {
		t.Errorf("index block must not list non-descendant pages")
	}

	// An index block on a non-index page renders nothing (the gate).
	pub("/notindex", "Not Index", `{"blocks":[{"type":"index","index":{"depth":1}}]}`)
	out2 := t.TempDir()
	if _, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, out2, "notindex/index.html"), "pbcssg-index-list") {
		t.Errorf("index block on a non-index page must render nothing")
	}
}

func TestBuildValidatesNavLinks(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")
	publish(t, s, "/about", "About", "# About")

	out := t.TempDir()
	rep, err := Run(s, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Nav: []render.NavLink{
			{Label: "Home", Href: "/"},                     // ok
			{Label: "About", Href: "/about/"},              // ok (trailing slash → about/index.html)
			{Label: "Ghost", Href: "/missing/"},            // dangling → warn
			{Label: "External", Href: "https://x.example"}, // external → ignored
		},
		FooterNav: []render.NavLink{{Label: "Nope", Href: "/also-missing"}},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rep.Warnings, "\n")
	if !strings.Contains(joined, `nav link "Ghost" → /missing/`) {
		t.Errorf("expected a dangling nav-link warning, got: %v", rep.Warnings)
	}
	if !strings.Contains(joined, `footer link "Nope" → /also-missing`) {
		t.Errorf("expected a dangling footer-link warning, got: %v", rep.Warnings)
	}
	if strings.Contains(joined, "Home") || strings.Contains(joined, "About") || strings.Contains(joined, "External") {
		t.Errorf("valid/external links must not warn: %v", rep.Warnings)
	}
}

func TestGPCJSON(t *testing.T) {
	// Empty → the optional lastUpdate is omitted, leaving only the required
	// gpc:true (never an invalid empty-string date).
	empty := string(gpcJSON(""))
	var v map[string]any
	if err := json.Unmarshal([]byte(empty), &v); err != nil {
		t.Fatalf("empty gpc.json is not valid JSON: %v\n%s", err, empty)
	}
	if v["gpc"] != true {
		t.Errorf("gpc must be true, got %v", v["gpc"])
	}
	if _, ok := v["lastUpdate"]; ok {
		t.Errorf("empty lastUpdate should be omitted, got:\n%s", empty)
	}
	// Whitespace-only is treated as empty too.
	if strings.Contains(string(gpcJSON("   ")), "lastUpdate") {
		t.Errorf("whitespace-only lastUpdate should be omitted")
	}
	// A set date is included and the result stays valid JSON.
	if err := json.Unmarshal(gpcJSON("2026-07-30"), &v); err != nil {
		t.Fatalf("gpc.json with a date is not valid JSON: %v", err)
	}
	if v["lastUpdate"] != "2026-07-30" || v["gpc"] != true {
		t.Errorf("gpc.json fields wrong: %v", v)
	}
}

func TestValidateGPCDate(t *testing.T) {
	valid := []string{"", "  ", "2026-07-30", " 2026-01-01 "}
	for _, s := range valid {
		if err := ValidateGPCDate(s); err != nil {
			t.Errorf("ValidateGPCDate(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{"July 30", "2026-7-30", "07-30-2026", "2026/07/30", "2026-13-01", "tomorrow"}
	for _, s := range invalid {
		if err := ValidateGPCDate(s); err == nil {
			t.Errorf("ValidateGPCDate(%q) = nil, want an error", s)
		}
	}
}

func TestWithinDir(t *testing.T) {
	inside := []struct{ dir, full string }{
		{"/out", "/out"},
		{"/out", "/out/index.html"},
		{"/out", "/out/blog/post/index.html"},
		{"site", "site/tags/x/index.html"},
	}
	for _, c := range inside {
		if !withinDir(c.dir, c.full) {
			t.Errorf("withinDir(%q, %q) = false, want true", c.dir, c.full)
		}
	}
	outside := []struct{ dir, full string }{
		{"/out", "/etc/passwd"},
		{"/out", "/output-sibling/x"}, // prefix-string but not nested
		{"/out", "/x"},
		{"site", "evil/x"},
	}
	for _, c := range outside {
		if withinDir(c.dir, c.full) {
			t.Errorf("withinDir(%q, %q) = true, want false", c.dir, c.full)
		}
	}
}

func TestConfigBrand(t *testing.T) {
	// Empty defaults to a text wordmark from the site name.
	b := Config{SiteName: "TUL"}.Brand()
	if b.Mode != "text" || b.Align != "start" || b.LogoHeight != "medium" || b.Text != "TUL" {
		t.Errorf("default brand = %+v, want text/start/medium/TUL", b)
	}
	// BrandText overrides the wordmark; unknown mode/height/align normalize.
	b2 := Config{SiteName: "TUL", BrandText: "Untracked", HeaderBrand: "bogus", HeaderAlign: "CENTER", LogoHeight: "huge"}.Brand()
	if b2.Text != "Untracked" || b2.Mode != "text" || b2.Align != "center" || b2.LogoHeight != "medium" {
		t.Errorf("normalized brand = %+v", b2)
	}
	// None is respected.
	if (Config{SiteName: "TUL", HeaderBrand: "none"}).Brand().Mode != "none" {
		t.Errorf("none mode should be preserved")
	}
}

func TestEmbedHostValidation(t *testing.T) {
	valid := []string{
		"peertube.example", "player.vimeo.com", "*.example.com",
		"host.example:8443", "https://peertube.example", "peertube.example/embed", "  Player.Vimeo.COM  ",
	}
	for _, h := range valid {
		if !ValidEmbedHost(h) {
			t.Errorf("ValidEmbedHost(%q) = false, want true", h)
		}
	}
	// CSP-breaking / malformed entries must be rejected.
	invalid := []string{
		"", "evil.com; default-src *", "a b.example", "e;vil", "evil.com'", "-bad.example",
		"javascript:alert(1)", "*.*.com", "host:99999999", "ex ample.com",
	}
	for _, h := range invalid {
		if ValidEmbedHost(h) {
			t.Errorf("ValidEmbedHost(%q) = true, want false", h)
		}
	}
	// embedOrigins drops invalid entries and https-prefixes the clean ones.
	got := embedOrigins([]string{"peertube.example", "evil.com; default-src *", "player.vimeo.com"})
	if len(got) != 2 || got[0] != "https://peertube.example" || got[1] != "https://player.vimeo.com" {
		t.Errorf("embedOrigins dropped/kept wrong entries: %v", got)
	}
}

func TestBuildFontChoice(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")
	out := t.TempDir()
	if _, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", Font: "transitional"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	css := readAsset(t, out, "theme")
	if !strings.Contains(css, "--font-sans: Charter") {
		t.Errorf("chosen body font not layered into the built theme.css")
	}
}

func TestBuildDarkLogo(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")

	light := "/media/" + strings.Repeat("a", 64) + ".svg"
	dark := "/media/" + strings.Repeat("b", 64) + ".svg"
	out := t.TempDir()
	if _, err := Run(s, Config{
		BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		HeaderBrand: "logo", LogoSrc: light, LogoSrcDark: dark, LogoAlt: "TUL", LogoHeight: "medium",
	}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	index := read(t, out, "index.html")
	if !strings.Contains(index, `pbcssg-logo--light" src="`+light+`"`) {
		t.Errorf("built page missing the light logo img:\n%s", index)
	}
	if !strings.Contains(index, `pbcssg-logo--dark" src="`+dark+`"`) {
		t.Errorf("built page missing the dark logo img:\n%s", index)
	}
}

func TestBuildJSONMetricsFlag(t *testing.T) {
	readMetrics := func(t *testing.T, cfg Config) (present bool, value bool) {
		t.Helper()
		s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		publish(t, s, "/", "Home", "# Home")
		out := t.TempDir()
		cfg.BaseURL, cfg.Version, cfg.BuildNumber = "https://tul.example", "1.0", "1"
		if _, err := Run(s, cfg, out); err != nil {
			t.Fatalf("Run: %v", err)
		}
		var bj map[string]any
		if err := json.Unmarshal([]byte(read(t, out, "build.json")), &bj); err != nil {
			t.Fatalf("build.json invalid: %v", err)
		}
		v, ok := bj["metrics"]
		b, _ := v.(bool)
		return ok, b
	}

	if present, value := readMetrics(t, Config{Metrics: true}); !present || !value {
		t.Errorf("metrics on: build.json metrics present=%v value=%v, want true/true", present, value)
	}
	// Default off is omitted (omitempty) so existing bundles are unchanged.
	if present, _ := readMetrics(t, Config{}); present {
		t.Errorf("metrics off: build.json should omit the metrics key")
	}
}
