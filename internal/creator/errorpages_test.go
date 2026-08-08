package creator

import (
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
)

func TestErrorPagesPanelDefaults(t *testing.T) {
	h := newHarness(t)

	// GET with nothing stored shows the built-in default for every curated page.
	rec := h.get("/admin/errorpages")
	if rec.Code != 200 {
		t.Fatalf("GET /admin/errorpages = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, ep := range build.ErrorPages {
		if !strings.Contains(body, `name="msg_`+ep.Name+`"`) {
			t.Errorf("panel missing a field for %s", ep.Name)
		}
	}
	if !strings.Contains(body, "The page you&#39;re looking for") {
		t.Errorf("404 default message not pre-populated:\n%s", body)
	}
}

func TestErrorPagesSaveRoundTrip(t *testing.T) {
	h := newHarness(t)

	form := h.form(map[string]string{"msg_404": "# Gone\n\nNot here anymore."})
	if rec := h.post("/admin/errorpages", form); rec.Code != 200 {
		t.Fatalf("POST /admin/errorpages = %d\n%s", rec.Code, rec.Body.String())
	}

	// Stored, and surfaced to the build via LoadBuildConfig.
	bc := h.c.loadBuildConfig(build.Config{})
	if bc.ErrorPages["404"] != "# Gone\n\nNot here anymore." {
		t.Errorf("404 message not overlaid into build config: %q", bc.ErrorPages["404"])
	}
	// A page left unset is absent from the map, so the build uses its default.
	if _, ok := bc.ErrorPages["403"]; ok {
		t.Errorf("unset 403 should not be in the overlay map")
	}

	// The editor re-shows the saved message.
	if body := h.get("/admin/errorpages").Body.String(); !strings.Contains(body, "Not here anymore.") {
		t.Errorf("saved 404 message not shown on reload")
	}
}
