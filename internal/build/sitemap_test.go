package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

// sitemapFixture publishes a small site exercising every sitemap case: a plain
// content page, a tagged page (→ tag pages), a noindex page, and a page with a
// youtube block (→ an /external/ facade). The caller supplies the Config toggles.
func sitemapFixture(t *testing.T, out string, cfg Config) *Report {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publishRaw(t, s, "/", "home", "Home", `{"body":"# Home\n\n[about](/about)"}`)
	publishRaw(t, s, "/about", "about", "About", `{"body":"# About","tags":["privacy"]}`)
	publishRaw(t, s, "/secret", "secret", "Secret", `{"body":"# Secret","noIndex":true}`)
	publishRaw(t, s, "/watch", "watch", "Watch",
		`{"body":"# Watch","blocks":[{"type":"youtube","youtube":{"videoId":"abc123","name":"degoogle","title":"A video"}}]}`)

	rep, err := Run(s, cfg, out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func fileAbsent(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Errorf("expected %s to be absent, stat err = %v", rel, err)
	}
}

func TestSitemapAndRobots(t *testing.T) {
	out := t.TempDir()
	sitemapFixture(t, out, Config{
		SiteName: "TUL", BaseURL: "https://tul.example/", Version: "1.0", BuildNumber: "1",
		Sitemap: true, SitemapListings: true,
	})

	sm := read(t, out, "sitemap.xml")
	// Content pages are listed with absolute locs, and content pages carry a lastmod.
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
		"<loc>https://tul.example/</loc>",
		"<loc>https://tul.example/about</loc>",
		"<loc>https://tul.example/watch</loc>",
		"<lastmod>",
		// Listing pages (SitemapListings on): tag pages + index + classification.
		"<loc>https://tul.example/tags</loc>",
		"<loc>https://tul.example/tags/privacy</loc>",
		"<loc>https://tul.example/classification</loc>",
	} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap.xml missing %q:\n%s", want, sm)
		}
	}
	// A noindex page and the /external/ consent facades are excluded.
	for _, bad := range []string{"/secret", "/external/"} {
		if strings.Contains(sm, bad) {
			t.Errorf("sitemap.xml must not list %q:\n%s", bad, sm)
		}
	}

	// robots.txt allows crawling and advertises the sitemap.
	robots := read(t, out, "robots.txt")
	for _, want := range []string{"User-agent: *", "Allow: /", "Sitemap: https://tul.example/sitemap.xml"} {
		if !strings.Contains(robots, want) {
			t.Errorf("robots.txt missing %q:\n%s", want, robots)
		}
	}

	// The noindex directive lands in the page HTML (page still exists, just noindex).
	// The build re-serializes HTML via x/net/html, so the void tag self-closes;
	// match the attribute, not the closing bracket.
	if secret := read(t, out, "secret/index.html"); !strings.Contains(secret, `name="robots" content="noindex"`) {
		t.Errorf("secret page missing noindex meta:\n%s", secret)
	}
	// A normal page is not noindexed.
	if home := read(t, out, "index.html"); strings.Contains(home, `content="noindex"`) {
		t.Errorf("home page must not carry noindex")
	}
}

func TestSitemapDisabledByDefault(t *testing.T) {
	out := t.TempDir()
	// Sitemap toggle off (zero value) → neither file is written.
	sitemapFixture(t, out, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"})
	fileAbsent(t, out, "sitemap.xml")
	fileAbsent(t, out, "robots.txt")
}

func TestSitemapListingsExcluded(t *testing.T) {
	out := t.TempDir()
	sitemapFixture(t, out, Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		Sitemap: true, SitemapListings: false,
	})
	sm := read(t, out, "sitemap.xml")
	// Content pages remain.
	if !strings.Contains(sm, "<loc>https://tul.example/about</loc>") {
		t.Errorf("content page dropped when only listings were excluded:\n%s", sm)
	}
	// Generated listing pages are gone.
	for _, bad := range []string{"/tags", "/classification", "/feeds"} {
		if strings.Contains(sm, bad) {
			t.Errorf("listing page %q must be excluded when SitemapListings is off:\n%s", bad, sm)
		}
	}
}

func TestSitemapSkippedWithoutBaseURL(t *testing.T) {
	out := t.TempDir()
	rep := sitemapFixture(t, out, Config{
		SiteName: "TUL", Version: "1.0", BuildNumber: "1", Sitemap: true, SitemapListings: true,
	})
	fileAbsent(t, out, "sitemap.xml")
	fileAbsent(t, out, "robots.txt")
	var warned bool
	for _, w := range rep.Warnings {
		if strings.Contains(w, "Sitemap is enabled but no Base URL") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a warning that the sitemap was skipped for lack of a base URL, got %v", rep.Warnings)
	}
}
