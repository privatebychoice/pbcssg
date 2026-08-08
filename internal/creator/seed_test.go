package creator

import (
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

func TestSeedDefaults(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// First run seeds the three starter pages as drafts.
	n, err := SeedDefaults(st)
	if err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if n != 3 {
		t.Fatalf("seeded %d pages, want 3", n)
	}

	pages, err := st.Pages()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("want 3 pages, got %d", len(pages))
	}
	byPath := map[string]store.Page{}
	for _, p := range pages {
		byPath[p.Path] = p
		if p.Status != store.StatusDraft {
			t.Errorf("page %q status = %q, want draft", p.Path, p.Status)
		}
	}
	for _, want := range []struct{ path, title string }{
		{"/", "Home"}, {"/about", "About"}, {"/privacy", "Privacy Policy"},
	} {
		p, ok := byPath[want.path]
		if !ok {
			t.Errorf("missing seeded page %q", want.path)
			continue
		}
		if p.Title != want.title {
			t.Errorf("page %q title = %q, want %q", want.path, p.Title, want.title)
		}
		if rev, has, _ := st.LatestRevision(p.ID); !has || len(rev.ContentJSON) == 0 {
			t.Errorf("page %q has no content revision", want.path)
		}
	}
	// The Privacy Policy carries the starter content (incl. the GPC note) and a
	// default "privacy" tag, so /tags has an entry once it is published.
	if p := byPath["/privacy"]; p.ID != 0 {
		rev, _, _ := st.LatestRevision(p.ID)
		if !strings.Contains(rev.ContentJSON, "Global Privacy Control") {
			t.Errorf("privacy page missing GPC starter content")
		}
		if !strings.Contains(rev.ContentJSON, "strictly necessary") {
			t.Errorf("privacy page missing strictly-necessary cookies/storage disclosure")
		}
		if !strings.Contains(rev.ContentJSON, `"tags":["privacy"]`) {
			t.Errorf("privacy page should carry the default \"privacy\" tag: %s", rev.ContentJSON)
		}
	}

	// A second run is a no-op (marker set) — no duplicate pages.
	n2, err := SeedDefaults(st)
	if err != nil {
		t.Fatalf("second SeedDefaults: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second run seeded %d pages, want 0", n2)
	}
	if pages2, _ := st.Pages(); len(pages2) != 3 {
		t.Errorf("second run changed page count to %d, want 3", len(pages2))
	}
	if v, ok, _ := st.Setting(keySeeded); !ok || v != "1" {
		t.Errorf("seeded marker = %q (ok=%v), want \"1\"", v, ok)
	}

	// Default navigation is seeded and parses to the expected links.
	if nav, ok, _ := st.Setting(keyNav); !ok || !strings.Contains(nav, "Home | /") {
		t.Errorf("default nav not seeded: %q", nav)
	}
	fn, ok, _ := st.Setting(keyFooterNav)
	if !ok {
		t.Fatal("footer nav not seeded")
	}
	links := parseNav(fn)
	want := []struct{ label, href string }{
		{"Privacy", "/privacy"}, {"Classification", "/classification"},
		{"About", "/about"}, {"Tags", "/tags"},
	}
	if len(links) != len(want) {
		t.Fatalf("footer nav = %d links, want %d (%q)", len(links), len(want), fn)
	}
	for i, w := range want {
		if links[i].Label != w.label || links[i].Href != w.href {
			t.Errorf("footer nav[%d] = %q->%q, want %q->%q", i, links[i].Label, links[i].Href, w.label, w.href)
		}
	}
}
