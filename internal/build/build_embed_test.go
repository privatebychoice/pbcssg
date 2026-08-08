package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

// publishEmbedPage creates and publishes a page carrying a single generic embed
// block, returning the store it lives in.
func publishEmbedPage(t *testing.T, embedURL string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pid, _ := s.CreatePage(store.Page{Path: "/watch", Slug: "watch", Title: "Watch"})
	cj := `{"body":"# Watch","blocks":[{"type":"embed","embed":{"provider":"PeerTube","name":"talk","title":"My Talk","embedUrl":"` + embedURL + `"}}]}`
	rid, _ := s.SaveRevision(pid, cj, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	return s
}

func buildJSONFrameSrc(t *testing.T, outDir string) []string {
	t.Helper()
	var bi struct {
		FrameSrc []string `json:"frameSrc"`
	}
	b, err := os.ReadFile(filepath.Join(outDir, "build.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &bi); err != nil {
		t.Fatal(err)
	}
	return bi.FrameSrc
}

// TestBuildEmbedAllowlisted: an embed whose host is on the allowlist gets its
// /external/<provider>/<name> page, and build.json carries the frame-src origin.
func TestBuildEmbedAllowlisted(t *testing.T) {
	s := publishEmbedPage(t, "https://peertube.example/videos/embed/abc")
	out := t.TempDir()
	rep, err := Run(s, Config{
		BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		EmbedHosts: []string{"peertube.example"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}
	html := read(t, out, "external/peertube/talk/index.html")
	if !strings.Contains(html, `data-embed-url="https://peertube.example/videos/embed/abc"`) {
		t.Errorf("embed page missing facade embed URL:\n%s", html)
	}
	if got := buildJSONFrameSrc(t, out); len(got) != 1 || got[0] != "https://peertube.example" {
		t.Errorf("build.json frameSrc = %v, want [https://peertube.example]", got)
	}
}

// TestBuildEmbedNotAllowlisted: an embed whose host is NOT on the allowlist is
// refused — no external page is written, and the build reports a warning.
func TestBuildEmbedNotAllowlisted(t *testing.T) {
	s := publishEmbedPage(t, "https://tracker.evil/embed/x")
	out := t.TempDir()
	rep, err := Run(s, Config{
		BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		EmbedHosts: []string{"peertube.example"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "external", "peertube", "talk", "index.html")); !os.IsNotExist(err) {
		t.Errorf("non-allowlisted embed must not produce an external page")
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "not in the Settings embed allowlist") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an allowlist warning, got %v", rep.Warnings)
	}
	// The host page still builds, and its consent card still links to the (now
	// absent) external page — the card contacts no third party regardless.
	if !strings.Contains(read(t, out, "watch/index.html"), `href="/external/peertube/talk"`) {
		t.Errorf("host page should still carry the consent card")
	}
}

func TestEmbedOriginsAndNormalizeHost(t *testing.T) {
	got := embedOrigins([]string{"https://Peertube.Example/x", "peertube.example", "player.vimeo.com/y", ""})
	want := []string{"https://peertube.example", "https://player.vimeo.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("embedOrigins = %v, want %v", got, want)
	}
}
