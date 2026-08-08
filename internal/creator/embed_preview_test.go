package creator

import (
	"strings"
	"testing"
)

// TestExternalEmbedPreview covers the generic consent-gated embed: its card's
// "Open embed page" link (/external/<provider>/<name>) must resolve in creator
// mode, mirroring the built bundle, so the author can preview the facade.
func TestExternalEmbedPreview(t *testing.T) {
	h := newHarness(t)

	blocks := `[{"type":"embed","embed":{"provider":"PeerTube","title":"My Talk","embedUrl":"https://peertube.example/videos/embed/abc","transcript":"Hello **world**."}}]`
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home", "blocks": blocks}))

	// Provider is slugged for the URL ("PeerTube" -> "peertube"); the slug is
	// derived from the title ("My Talk" -> "my-talk").
	rec := h.get("/external/peertube/my-talk")
	if rec.Code != 200 {
		t.Fatalf("external embed preview: code=%d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"My Talk",
		"pbcssg-embed-facade",
		`data-embed-url="https://peertube.example/videos/embed/abc"`,
		"<strong>world</strong>",
		`src="/admin/assets/pbcssg-youtube.js"`,
		`<a href="/">← Back to Home</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("external embed page missing %q", want)
		}
	}
	// Nothing from the provider is embedded in the static HTML — no iframe until
	// the facade script builds one on click.
	if strings.Contains(body, "<iframe") {
		t.Errorf("external embed page must not contain an iframe in static HTML")
	}

	// An unknown provider/slug 404s.
	if rec := h.get("/external/peertube/nope"); rec.Code != 404 {
		t.Errorf("unknown embed slug should 404, got %d", rec.Code)
	}
	if rec := h.get("/external/other/my-talk"); rec.Code != 404 {
		t.Errorf("wrong provider should 404, got %d", rec.Code)
	}
}
