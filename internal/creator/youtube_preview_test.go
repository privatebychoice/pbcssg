package creator

import (
	"strings"
	"testing"
)

// TestExternalYouTubePreview covers the reported gap: the consent card's
// "Open video page" link (/external/youtube/<name>) 404'd in creator mode because
// those pages only existed in the built bundle. The creator now renders Stage 2.
func TestExternalYouTubePreview(t *testing.T) {
	h := newHarness(t)

	blocks := `[{"type":"youtube","youtube":{"videoId":"dQw4w9WgXcQ","title":"My Talk","transcript":"Hello **world**."}}]`
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home", "blocks": blocks}))

	// The block's slug is derived from the title ("My Talk" -> "my-talk").
	rec := h.get("/external/youtube/my-talk")
	if rec.Code != 200 {
		t.Fatalf("external youtube preview: code=%d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"My Talk",                               // title
		"pbcssg-youtube-facade",                 // the click-to-load facade
		`data-video-id="dQw4w9WgXcQ"`,           // the video id wired into the facade
		"Transcript",                            // transcript section
		"<strong>world</strong>",                // transcript markdown rendered
		`src="/admin/assets/pbcssg-youtube.js"`, // self-hosted facade script
		`<a href="/">← Back to Home</a>`,        // back link to the host page
	} {
		if !strings.Contains(body, want) {
			t.Errorf("external youtube page missing %q", want)
		}
	}
	// Nothing from YouTube is embedded in the static HTML — no iframe exists until
	// the facade script builds one on click. (The fine-print copy may *name*
	// youtube-nocookie.com, but there must be no iframe loading it.)
	if strings.Contains(body, "<iframe") {
		t.Errorf("external page must not contain an iframe in static HTML")
	}

	// The facade script is served self-hosted by the editor.
	if js := h.get("/admin/assets/pbcssg-youtube.js"); js.Code != 200 || js.Body.Len() == 0 {
		t.Errorf("facade script not served: code=%d", js.Code)
	}

	// An unknown slug 404s.
	if rec := h.get("/external/youtube/nope"); rec.Code != 404 {
		t.Errorf("unknown video slug should 404, got %d", rec.Code)
	}
}
