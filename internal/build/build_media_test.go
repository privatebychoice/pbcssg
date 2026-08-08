package build

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/asset"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBuildEmitsSEOMetadata checks the build wires the author summary/keywords
// and an absolute canonical URL into the page head.
func TestBuildEmitsSEOMetadata(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pid, _ := s.CreatePage(store.Page{Path: "/blog/post", Slug: "post", Title: "A Post"})
	cj := `{"body":"# Hi","summary":"All about privacy.","keywords":["gpc","tracking"]}`
	rid, _ := s.SaveRevision(pid, cj, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if _, err := Run(s, Config{BaseURL: "https://tul.example/", Version: "1.0", BuildNumber: "1", OpenGraph: true}, out); err != nil {
		t.Fatal(err)
	}
	html := read(t, out, "blog/post/index.html")
	// Substrings are bracket-agnostic: hygiene's HTML re-render emits void
	// elements as "<meta …/>".
	for _, want := range []string{
		`name="description" content="All about privacy."`,
		`rel="canonical" href="https://tul.example/blog/post"`,
		`property="og:url" content="https://tul.example/blog/post"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("built page missing %q", want)
		}
	}
	// Keywords are search-only — never a meta tag.
	if strings.Contains(html, `name="keywords"`) {
		t.Errorf("keywords should not appear as meta on the built page")
	}
}

// TestBuildEmitsTagPages checks the browsable tag pages: chips on content pages,
// per-tag pages, and the /tags/ index.
func TestBuildEmitsTagPages(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pub := func(path, title string, tags []string) {
		pid, _ := s.CreatePage(store.Page{Path: path, Slug: strings.Trim(path, "/"), Title: title})
		c, _ := json.Marshal(map[string]any{"body": "# " + title, "tags": tags})
		rid, _ := s.SaveRevision(pid, string(c), "")
		if err := s.Publish(pid, rid); err != nil {
			t.Fatal(err)
		}
	}
	pub("/a", "Post A", []string{"GPC", "Self Hosting"})
	pub("/b", "Post B", []string{"GPC"})

	out := t.TempDir()
	if _, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatal(err)
	}

	// Chip on a content page links to the tag page.
	if a := read(t, out, "a/index.html"); !strings.Contains(a, `href="/tags/gpc/"`) || !strings.Contains(a, `href="/tags/self-hosting/"`) {
		t.Errorf("content page missing tag chips:\n%s", a)
	}
	// Per-tag page lists both pages for GPC.
	gpc := read(t, out, "tags/gpc/index.html")
	if !strings.Contains(gpc, "<h1>Tag: GPC</h1>") || !strings.Contains(gpc, `href="/a"`) || !strings.Contains(gpc, `href="/b"`) {
		t.Errorf("tag page /tags/gpc/ wrong:\n%s", gpc)
	}
	// Tags index lists every tag with counts.
	idx := read(t, out, "tags/index.html")
	if !strings.Contains(idx, `href="/tags/gpc/"`) || !strings.Contains(idx, `href="/tags/self-hosting/"`) {
		t.Errorf("tags index missing tags:\n%s", idx)
	}
}

// TestBuildCleansStaleOutput checks that a rebuild reflects only the current
// published state — files from a prior build (e.g. an unpublished or deleted
// page) do not linger in the bundle.
func TestBuildCleansStaleOutput(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	out := t.TempDir()
	// Simulate a prior build that produced a page since unpublished/deleted.
	if err := os.MkdirAll(filepath.Join(out, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "gone", "index.html"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	publish(t, s, "/", "Home", "# Home")
	if _, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(out, "gone", "index.html")); !os.IsNotExist(err) {
		t.Errorf("stale page should have been removed by the rebuild")
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		t.Errorf("current page should be present: %v", err)
	}
}

// TestBuildEmitsReferencedMedia checks that the build writes exactly the media a
// page references (fetched from the store by content address), re-verifies it,
// and warns — without failing — on a reference to media not in the store.
func TestBuildEmitsReferencedMedia(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Ingest a real PNG and store it content-addressed.
	a, err := asset.Ingest("pic.png", testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutAsset(store.AssetData{
		Asset: store.Asset{SHA256: a.SHA256, Filename: "pic.png", Format: a.Format, MIME: a.MIME},
		Data:  a.Data,
	}); err != nil {
		t.Fatal(err)
	}

	ref := "/media/" + a.SHA256 + ".png"
	missing := "/media/" + strings.Repeat("a", 64) + ".png"
	publish(t, s, "/", "Home", "# Home\n\n![logo]("+ref+") and a stale ![x]("+missing+")")

	out := t.TempDir()
	rep, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The referenced asset is written with the stored bytes.
	got, err := os.ReadFile(filepath.Join(out, "media", a.SHA256+".png"))
	if err != nil {
		t.Fatalf("referenced media not emitted: %v", err)
	}
	if !bytes.Equal(got, a.Data) {
		t.Errorf("emitted media bytes differ from stored")
	}

	// The stale reference is not written and produces a warning (not a failure).
	if _, err := os.Stat(filepath.Join(out, "media", strings.Repeat("a", 64)+".png")); !os.IsNotExist(err) {
		t.Errorf("stale media reference should not be written")
	}
	var warned bool
	for _, w := range rep.Warnings {
		if strings.Contains(w, "Broken media reference (not in the Media library)") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a build-wide broken-media warning, got %v", rep.Warnings)
	}
	// The broken reference is also attributed to the offending page's report row.
	var pageWarned bool
	for _, p := range rep.Pages {
		if p.Path == "/" {
			for _, w := range p.Warnings {
				if strings.Contains(w, "Broken Media: /media/") {
					pageWarned = true
				}
			}
		}
	}
	if !pageWarned {
		t.Errorf("home page report should carry the per-page broken-media warning, got %+v", rep.Pages)
	}

	// The emitted media is listed in build.json's file hashes.
	bj := read(t, out, "build.json")
	if !strings.Contains(bj, "media/"+a.SHA256+".png") {
		t.Errorf("emitted media should be recorded in build.json")
	}
}

// TestBrokenMediaNamesReferencingPages: when several pages reference the same
// missing media, the build-wide warning appears once (without a redundant page
// list) and each referencing page's report row carries a "Broken Media:" warning.
func TestBrokenMediaNamesReferencingPages(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	missing := "/media/" + strings.Repeat("c", 64) + ".mp4"
	publish(t, s, "/blog/a", "A", "![v]("+missing+")")
	publish(t, s, "/blog/b", "B", "text then ![v]("+missing+")")
	publish(t, s, "/blog/c", "C", "# clean, no media")

	out := t.TempDir()
	rep, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exactly one build-wide broken-media warning, and it does not list the pages
	// (that is now the per-page column's job, so a page list would be redundant).
	var wide []string
	for _, w := range rep.Warnings {
		if strings.Contains(w, "Broken media reference") {
			wide = append(wide, w)
		}
	}
	if len(wide) != 1 {
		t.Fatalf("want one build-wide broken-media warning, got %v", wide)
	}
	if strings.Contains(wide[0], "referenced by") || strings.Contains(wide[0], "/blog/") {
		t.Errorf("build-wide warning should not name pages, got %q", wide[0])
	}

	// Each offending page's report row carries the warning; the clean page's does not.
	warnedPages := map[string]bool{}
	for _, p := range rep.Pages {
		for _, w := range p.Warnings {
			if strings.Contains(w, "Broken Media: /media/") {
				warnedPages[p.Path] = true
			}
		}
	}
	if !warnedPages["/blog/a"] || !warnedPages["/blog/b"] {
		t.Errorf("both referencing pages should carry the warning, got %v", warnedPages)
	}
	if warnedPages["/blog/c"] {
		t.Errorf("clean page should carry no broken-media warning")
	}
}

// TestBuildEmitsFilesystemMedia checks a page's self-hosted audio/video block
// gets its filesystem-backed bytes copied into the bundle, re-verified clean.
func TestBuildEmitsFilesystemMedia(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A clean (tag-free) MP3 frame: asset.Verify treats it as already clean.
	clean := append([]byte{0xFF, 0xFB, 0x90, 0x00}, bytes.Repeat([]byte{0x11}, 300)...)
	a, err := asset.Ingest("clip.mp3", clean)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutMedia(store.MediaFile{
		SHA256: a.SHA256, Filename: "clip.mp3", Format: "mp3", MIME: "audio/mpeg", Kind: "audio",
	}, a.Data); err != nil {
		t.Fatal(err)
	}

	pid, _ := s.CreatePage(store.Page{Path: "/listen", Slug: "listen", Title: "Listen"})
	cj := `{"body":"# Listen","blocks":[{"type":"media","media":{"kind":"audio","src":"/media/` + a.SHA256 + `.mp3"}}]}`
	rid, _ := s.SaveRevision(pid, cj, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	rep, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}
	// The page references the native <audio> element, and the media file is in the
	// bundle byte-for-byte.
	if !strings.Contains(read(t, out, "listen/index.html"), `<audio class="pbcssg-media-el"`) {
		t.Errorf("built page missing native audio element")
	}
	got, err := os.ReadFile(filepath.Join(out, "media", a.SHA256+".mp3"))
	if err != nil || !bytes.Equal(got, clean) {
		t.Errorf("bundle media mismatch: err=%v", err)
	}
}
