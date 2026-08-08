package creator

import (
	"strings"
	"testing"
)

// TestPageUnlistedRoundTrip verifies the "Unlisted (hidden page)" flag persists
// through save and re-renders checked in the editor.
func TestPageUnlistedRoundTrip(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/pages", h.form(map[string]string{
		"title": "Members dispatch", "path": "/members/x", "body": "# Secret", "unlisted": "1",
	}))
	if rec.Code != 303 {
		t.Fatalf("create page = %d\n%s", rec.Code, rec.Body.String())
	}

	rev, _, _ := h.st.LatestRevision(1)
	if !strings.Contains(rev.ContentJSON, `"unlisted":true`) {
		t.Errorf("stored content did not persist the unlisted flag: %s", rev.ContentJSON)
	}

	// The editor re-shows the flag as checked, and offers the random-suffix tool.
	body := h.get("/pages/1").Body.String()
	if !strings.Contains(body, `name="unlisted" value="1" checked`) {
		t.Errorf("editor did not render the unlisted checkbox as checked")
	}
	if !strings.Contains(body, `id="path-random"`) {
		t.Errorf("editor missing the random-suffix control")
	}
}
