package creator

import (
	"strings"
	"testing"
)

func TestScanBadges(t *testing.T) {
	h := newHarness(t)

	// Two references to the same external domain aggregate into one badge with a
	// count; the grade pill is rendered with a CSS-safe class.
	body := "# Hi\n\n[a](https://tracker.example/1) and [b](https://tracker.example/2)"
	sr := scanJSON(t, h, h.form(map[string]string{"body": body}))
	if !strings.Contains(sr.Badges, "tracker.example") {
		t.Errorf("badge missing domain:\n%s", sr.Badges)
	}
	if !strings.Contains(sr.Badges, `class="grade grade-`) {
		t.Errorf("badge missing grade pill:\n%s", sr.Badges)
	}
	if !strings.Contains(sr.Badges, "2 refs") {
		t.Errorf("badge should aggregate the two references:\n%s", sr.Badges)
	}
}

func TestScanFullySelfHosted(t *testing.T) {
	h := newHarness(t)
	// Only an internal link: nothing external to grade.
	sr := scanJSON(t, h, h.form(map[string]string{"body": "# Hi\n\n[about](/about)"}))
	if !strings.Contains(sr.Badges, "fully self-hosted") {
		t.Errorf("expected self-hosted message:\n%s", sr.Badges)
	}
}
