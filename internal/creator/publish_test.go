package creator

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// fakePublisher records Reload calls, standing in for the running public server.
type fakePublisher struct {
	reloads int
	lastArg string
}

func (f *fakePublisher) Reload(contentDir string) error {
	f.reloads++
	f.lastArg = contentDir
	return nil
}

// setupPublishHarness builds a creator wired for the unified Publish: a deploy-like
// releases/ dir + a `current` symlink (pointing at an empty seed release) and a fake
// public server. It returns the harness, the releases dir, the current link, and the
// fake publisher.
func setupPublishHarness(t *testing.T) (*harness, string, string, *fakePublisher) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	seed := filepath.Join(releases, "v1.0-build0")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(seed, current); err != nil {
		t.Fatal(err)
	}

	fp := &fakePublisher{}
	c, err := New(st, Config{
		OutDir: filepath.Join(root, "staging"), ReleaseDir: releases,
		Build:     build.Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"},
		Publisher: fp, ContentLink: current,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{c: c, st: st}, releases, current, fp
}

// TestSitePublishReloadsInProcess exercises the unified Publish (§7.9): it builds a
// versioned release dir, atomically repoints the `current` symlink to it, and reloads
// the public listener in-process.
func TestSitePublishReloadsInProcess(t *testing.T) {
	h, releases, current, fp := setupPublishHarness(t)

	// A published page to build.
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home"}))
	h.post("/pages/1/publish", h.form(nil))

	rec := h.post("/admin/publish", h.form(nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Published live") {
		t.Fatalf("publish: code=%d\n%s", rec.Code, rec.Body.String())
	}

	// Build number auto-incremented 1 -> 2; a versioned release dir was built.
	if bn := h.c.state().build.BuildNumber; bn != "2" {
		t.Errorf("build number = %q, want 2", bn)
	}
	relDir := filepath.Join(releases, "v1.0-build2")
	for _, f := range []string{"index.html", "build.json"} {
		if _, err := os.Stat(filepath.Join(relDir, f)); err != nil {
			t.Errorf("release dir missing %s: %v", f, err)
		}
	}

	// `current` now resolves to the new release (atomic repoint)...
	got, err := os.Readlink(current)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(relDir) {
		t.Errorf("current -> %q, want %q", got, relDir)
	}
	// ...and the public listener was reloaded once, with the empty arg (re-open the
	// configured content path — the symlink we just repointed).
	if fp.reloads != 1 || fp.lastArg != "" {
		t.Errorf("Reload called %d time(s) with arg %q, want 1 with \"\"", fp.reloads, fp.lastArg)
	}
}

// TestSitePublishPrunesOldReleases: with a retention count set, repeated publishes
// leave only the newest release directories, and the live one is always kept (§7.4).
func TestSitePublishPrunesOldReleases(t *testing.T) {
	h, releases, current, _ := setupPublishHarness(t)
	if err := h.st.SetSetting(keyKeepReleases, "1"); err != nil {
		t.Fatal(err)
	}
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home"}))
	h.post("/pages/1/publish", h.form(nil))

	for i := 0; i < 3; i++ {
		if rec := h.post("/admin/publish", h.form(nil)); rec.Code != 200 {
			t.Fatalf("publish %d: code=%d\n%s", i, rec.Code, rec.Body.String())
		}
	}

	// keep=1 → only the live release directory remains (seed + older builds pruned).
	var dirs []string
	entries, err := os.ReadDir(releases)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("after 3 publishes with keep=1, release dirs = %v, want exactly 1", dirs)
	}
	// ...and it is the one `current` resolves to.
	live, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(live) != dirs[0] {
		t.Errorf("surviving dir %q is not the live release %q", dirs[0], filepath.Base(live))
	}
}

// TestSitePublishRejectedWithoutPublisher: standalone `pbcssg creator` (no Publisher)
// has no running public site, so Publish is refused (use Release instead).
func TestSitePublishRejectedWithoutPublisher(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/publish", h.form(nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("standalone publish = %d, want 400", rec.Code)
	}
}
