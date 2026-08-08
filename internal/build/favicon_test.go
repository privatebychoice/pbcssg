package build

import (
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

func seedFavicons(t *testing.T, s *store.Store, names ...string) {
	t.Helper()
	mime := map[string]string{
		"favicon.svg": "image/svg+xml", "favicon.ico": "image/x-icon",
		"apple-touch-icon.png": "image/png", "icon-192.png": "image/png", "icon-512.png": "image/png",
	}
	for _, n := range names {
		if err := s.PutFavicon(n, mime[n], []byte("data:"+n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildFaviconsFullSet(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	publish(t, s, "/", "Home", "# Home")
	seedFavicons(t, s, "favicon.svg", "favicon.ico", "apple-touch-icon.png", "icon-192.png", "icon-512.png")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0",
		BuildNumber: "1", FaviconThemeColor: "#0d9488"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Each asset is emitted at its canonical root path.
	for _, f := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png",
		"icon-192.png", "icon-512.png", "site.webmanifest"} {
		if read(t, out, f) == "" {
			t.Errorf("missing root file %q", f)
		}
	}

	// Every page carries the matching <head> links + theme-color meta.
	page := read(t, out, "index.html")
	// Assertions are self-close-agnostic (the HTML serializer emits <link/>).
	for _, want := range []string{
		`rel="icon" href="/favicon.ico" sizes="any"`,
		`rel="icon" href="/favicon.svg" type="image/svg+xml"`,
		`rel="apple-touch-icon" href="/apple-touch-icon.png"`,
		`rel="manifest" href="/site.webmanifest"`,
		`name="theme-color" content="#0d9488"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing head tag %q:\n%s", want, page)
		}
	}

	// The generated manifest names the site and references the PWA icons.
	mani := read(t, out, "site.webmanifest")
	for _, want := range []string{`"name": "TUL"`, `"/icon-192.png"`, `"/icon-512.png"`,
		`"theme_color": "#0d9488"`, `"any maskable"`} {
		if !strings.Contains(mani, want) {
			t.Errorf("manifest missing %q:\n%s", want, mani)
		}
	}
}

func TestBuildFaviconsPartialAndNone(t *testing.T) {
	// Only an SVG + ICO: those two links, no apple/manifest/theme.
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")
	seedFavicons(t, s, "favicon.svg", "favicon.ico")

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatal(err)
	}
	page := read(t, out, "index.html")
	if !strings.Contains(page, `href="/favicon.svg"`) || !strings.Contains(page, `href="/favicon.ico"`) {
		t.Errorf("expected svg+ico links:\n%s", page)
	}
	if strings.Contains(page, "apple-touch-icon") || strings.Contains(page, "site.webmanifest") || strings.Contains(page, "theme-color") {
		t.Errorf("unexpected links for a partial set:\n%s", page)
	}

	// A site with no favicons emits none.
	s2, _ := store.Open(filepath.Join(t.TempDir(), "c2.db"))
	defer s2.Close()
	publish(t, s2, "/", "Home", "# Home")
	out2 := t.TempDir()
	if _, err := Run(s2, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out2); err != nil {
		t.Fatal(err)
	}
	if p := read(t, out2, "index.html"); strings.Contains(p, `rel="icon"`) || strings.Contains(p, "manifest") {
		t.Errorf("a site with no favicons should emit no favicon links:\n%s", p)
	}
}
