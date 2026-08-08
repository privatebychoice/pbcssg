package creator

import (
	"strings"
	"testing"
)

// TestStandalonePreviewMirrorsProduction covers the standalone preview: the raw
// page (shown in the iframe) now carries the External references listing inline,
// between the content and the footer, exactly as the built page does — instead of
// a separate right-side badges panel on the chrome page.
func TestStandalonePreviewMirrorsProduction(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{
		"title": "P", "path": "/", "body": "# Hi\n\n[t](https://tracker.example/x)",
	}))

	// Raw route (the iframe source): the themed page with the inline
	// external-references listing sitting before the footer, mirroring the build.
	raw := h.get("/pages/1/preview/raw")
	rb := raw.Body.String()
	if raw.Code != 200 || !strings.Contains(rb, ">Hi</h1>") { // goldmark auto heading ID (SPEC §6.12)
		t.Fatalf("raw preview wrong: %d", raw.Code)
	}
	for _, want := range []string{
		`class="pbcssg-extref-heading">External references`,
		`class="pbcssg-grade pbcssg-grade-unknown"`,
		"tracker.example",
		"No classification on record for this domain",
	} {
		if !strings.Contains(rb, want) {
			t.Errorf("raw preview missing the inline external-references listing %q:\n%s", want, rb)
		}
	}
	if li, fi := strings.Index(rb, "pbcssg-extref"), strings.Index(rb, "pbcssg-footer"); li < 0 || fi < 0 || li > fi {
		t.Errorf("listing should sit before the footer (list=%d footer=%d)", li, fi)
	}

	// Chrome page: embeds the raw iframe and no longer carries the old right-side
	// badges panel (with its editor grade pills).
	chrome := h.get("/pages/1/preview")
	cb := chrome.Body.String()
	if chrome.Code != 200 || !strings.Contains(cb, `src="/pages/1/preview/raw"`) {
		t.Fatalf("preview chrome should embed the raw iframe: %d", chrome.Code)
	}
	if strings.Contains(cb, `class="grade grade-`) {
		t.Errorf("standalone preview should no longer show the side badges panel:\n%s", cb)
	}
}

// TestPublishShowsColourBadges covers Problem 2: the publish confirmation renders
// coloured grade pills, consistent with the editor's link badges.
func TestPublishShowsColourBadges(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{
		"title": "P", "path": "/", "body": "[t](https://tracker.example/x)",
	}))
	rec := h.post("/pages/1/publish", h.form(nil)) // no ack -> confirmation screen
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "acknowledgement") {
		t.Fatalf("publish confirm expected: %d", rec.Code)
	}
	if !strings.Contains(body, `class="grade grade-`) {
		t.Errorf("publish page should show coloured grade pills:\n%s", body)
	}
}
