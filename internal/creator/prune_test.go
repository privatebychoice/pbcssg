package creator

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

func relName(n int) string { return "v1.0-build" + strconv.Itoa(n) }

// makeReleases creates release directories v1.0-build<n> for each n under root, plus a
// couple of unrelated entries that pruning must never touch.
func makeReleases(t *testing.T, root string, builds ...int) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range builds {
		if err := os.MkdirAll(filepath.Join(root, relName(n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Decoys: a tarball (file) and a non-release directory — both must survive.
	if err := os.WriteFile(filepath.Join(root, "pbcssg-v1.0-build9.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func dirsUnder(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)
	return got
}

func TestPruneReleasesKeepsNewest(t *testing.T) {
	root := t.TempDir()
	makeReleases(t, root, 1, 2, 3, 4, 5)
	// current -> the newest (build5), as it is right after a publish.
	current := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(filepath.Join(root, relName(5)), current); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneReleases(root, 3, current)
	if err != nil {
		t.Fatal(err)
	}
	// Newest 3 kept (build5/4/3); build2/1 removed. Decoys survive.
	sort.Strings(removed)
	if want := []string{relName(1), relName(2)}; !equal(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
	if got, want := dirsUnder(t, root), []string{"notes", relName(3), relName(4), relName(5)}; !equal(got, want) {
		t.Errorf("remaining dirs = %v, want %v", got, want)
	}
}

func TestPruneReleasesKeepAllWhenZero(t *testing.T) {
	root := t.TempDir()
	makeReleases(t, root, 1, 2, 3, 4)
	removed, err := pruneReleases(root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("keep=0 should prune nothing, removed %v", removed)
	}
}

// The live release (whatever `current` points at) must never be removed, even when the
// retention count would otherwise trim it.
func TestPruneReleasesProtectsLiveRelease(t *testing.T) {
	root := t.TempDir()
	makeReleases(t, root, 1, 2, 3, 4, 5)
	// current points at the OLDEST (build1) — an unusual state, but the guard must hold.
	current := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(filepath.Join(root, relName(1)), current); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneReleases(root, 2, current)
	if err != nil {
		t.Fatal(err)
	}
	// keep=2 → keep build5,build4; candidates build3,build2,build1; build1 is live → kept.
	sort.Strings(removed)
	if want := []string{relName(2), relName(3)}; !equal(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
	if _, err := os.Stat(filepath.Join(root, relName(1))); err != nil {
		t.Errorf("live release build1 was removed: %v", err)
	}
}

func TestKeepReleasesSetting(t *testing.T) {
	h := newHarness(t)
	if got := h.c.keepReleases(); got != defaultKeepReleases {
		t.Errorf("default keepReleases = %d, want %d", got, defaultKeepReleases)
	}
	if err := h.st.SetSetting(keyKeepReleases, "5"); err != nil {
		t.Fatal(err)
	}
	if got := h.c.keepReleases(); got != 5 {
		t.Errorf("stored keepReleases = %d, want 5", got)
	}
	// A bad stored value falls back to the default rather than returning a nonsense count.
	if err := h.st.SetSetting(keyKeepReleases, "-1"); err != nil {
		t.Fatal(err)
	}
	if got := h.c.keepReleases(); got != defaultKeepReleases {
		t.Errorf("invalid stored keepReleases = %d, want default %d", got, defaultKeepReleases)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
