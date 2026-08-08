package creator

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestBrokenMediaOnSave: saving a page that references a /media/ file not in the
// library persists the page but keeps the operator on the editor with an advisory
// warning (a broken reference is a warning, not a blocked save — SPEC §6.1).
func TestBrokenMediaOnSave(t *testing.T) {
	h := newHarness(t)
	missing := "/media/" + strings.Repeat("a", 64) + ".png"

	// Create referencing missing media: page is created (not rejected), but the
	// editor comes back with the broken-media warning instead of redirecting.
	rec := h.post("/pages", h.form(map[string]string{
		"title": "Post", "path": "/blog/post", "body": "![alt](" + missing + ")",
	}))
	if rec.Code != 200 {
		t.Fatalf("create with broken media: code=%d (want 200 with warning, not a redirect)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not in the Media library") {
		t.Fatalf("expected broken-media warning, got:\n%s", rec.Body.String())
	}
	pages, _ := h.st.Pages()
	if len(pages) != 1 {
		t.Fatalf("page should still be created despite broken media: %+v", pages)
	}
	id := pages[0].ID

	// Upload the referenced image, then re-save: no warning, normal redirect.
	img := pngBytes(t)
	up := h.upload("/admin/media", "photo.png", img, true)
	if up.Code != 200 {
		t.Fatalf("upload: %d", up.Code)
	}
	assets, _ := h.st.Assets()
	if len(assets) != 1 {
		t.Fatalf("asset not stored: %+v", assets)
	}
	good := "/media/" + assets[0].SHA256 + ".png"

	save := h.post("/pages/"+strconv.FormatInt(id, 10), h.form(map[string]string{
		"title": "Post", "path": "/blog/post", "body": "![alt](" + good + ")",
	}))
	if save.Code != 303 {
		t.Errorf("save with present media should redirect (303), got %d\n%s", save.Code, save.Body.String())
	}
}

// scanJSON posts form values to /scan and decodes the JSON response.
func scanJSON(t *testing.T, h *harness, form url.Values) scanResponse {
	t.Helper()
	rec := h.post("/scan", form)
	if rec.Code != 200 {
		t.Fatalf("scan: %d\n%s", rec.Code, rec.Body.String())
	}
	var sr scanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("scan json: %v (%s)", err, rec.Body.String())
	}
	return sr
}

// TestBrokenMediaLiveScan: the live /scan panel flags a broken reference in the
// body, attributes it to the body, and clears once there is no missing media.
func TestBrokenMediaLiveScan(t *testing.T) {
	h := newHarness(t)
	sha := strings.Repeat("b", 64)
	body := "![v](/media/" + sha + ".mp4)"

	sr := scanJSON(t, h, url.Values{"body": {"[t](https://x.example) " + body}})
	if !strings.Contains(sr.Media, "video /media/") {
		t.Errorf("media panel should list the broken video, got %q", sr.Media)
	}
	if !sr.Body {
		t.Errorf("the body should be flagged as referencing broken media")
	}
	if len(sr.Blocks) != 0 {
		t.Errorf("no blocks present, want no block flags, got %v", sr.Blocks)
	}

	// A page with no missing media produces no broken-media panel and no flags.
	clean := scanJSON(t, h, url.Values{"body": {"just text, no media"}})
	if clean.Media != "" || clean.Body || len(clean.Blocks) != 0 {
		t.Errorf("clean content should show no broken-media warning: %+v", clean)
	}
}

// TestBrokenMediaBlockAttribution: a broken reference inside an image block is
// attributed to that block's index (not the body), so the editor can flag the
// exact block heading.
func TestBrokenMediaBlockAttribution(t *testing.T) {
	h := newHarness(t)
	missing := "/media/" + strings.Repeat("d", 64) + ".png"
	blocks := `[{"type":"markdown","markdown":"hello"},{"type":"image","image":{"src":"` + missing + `"}}]`

	sr := scanJSON(t, h, url.Values{"body": {"clean body"}, "blocks": {blocks}})
	if sr.Body {
		t.Errorf("body is clean; it should not be flagged")
	}
	if len(sr.Blocks) != 1 || sr.Blocks[0] != 1 {
		t.Errorf("image block at index 1 should be flagged, got %v", sr.Blocks)
	}
	if !strings.Contains(sr.Media, "image /media/") {
		t.Errorf("media panel should list the broken image, got %q", sr.Media)
	}
}

func TestMediaKindLabel(t *testing.T) {
	cases := map[string]string{"png": "image", "svg": "image", "mp4": "video",
		"webm": "video", "mp3": "audio", "wav": "audio", "bin": "file"}
	for ext, want := range cases {
		if got := mediaKind(ext); got != want {
			t.Errorf("mediaKind(%q) = %q, want %q", ext, got, want)
		}
	}
}
