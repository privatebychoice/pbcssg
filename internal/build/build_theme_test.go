package build

import (
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
	"go.privatebychoice.com/pbcssg/internal/theme"
)

// TestThemeOverrideAppended checks that the operator's theme override is emitted
// AFTER the built-in theme (so the default is the fallback baseline, §6.4).
func TestThemeOverrideAppended(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publish(t, s, "/", "Home", "# Home")

	out := t.TempDir()
	override := ":root{--accent:#abcdef}\n.lead{font-weight:700}"
	if _, err := Run(s, Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", ThemeOverride: override}, out); err != nil {
		t.Fatal(err)
	}

	css := readAsset(t, out, "theme")
	if !strings.Contains(css, "--accent:#abcdef") || !strings.Contains(css, ".lead{font-weight:700}") {
		t.Errorf("override not emitted into theme asset")
	}
	// The built-in theme still precedes the override (fallback baseline first).
	base := strings.Index(css, theme.CSS[:60])
	ov := strings.Index(css, "--accent:#abcdef")
	if base < 0 || ov < 0 || base > ov {
		t.Errorf("built-in theme should precede the override (base=%d override=%d)", base, ov)
	}
}
