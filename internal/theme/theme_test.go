package theme

import (
	"strings"
	"testing"
)

// TestCSSColourModel guards the light/dark colour model: palettes defined once as
// constants, an Auto (OS-preference) mapping, and explicit data-theme overrides
// for the footer toggle. A regression here would break the theme switch.
func TestCSSColourModel(t *testing.T) {
	for _, want := range []string{
		"color-scheme: light dark",                                        // Auto default declares both
		"--pbc-light-bg: #ffffff",                                         // light palette constant
		"--pbc-dark-bg: #14161a",                                          // dark palette constant
		"@media (prefers-color-scheme: dark)",                             // Auto follows the OS
		`:root[data-theme="light"]`,                                       // explicit light override
		`:root[data-theme="dark"]`,                                        // explicit dark override
		"--bg: var(--pbc-dark-bg)",                                        // tokens mapped from a palette
		".pbcssg-theme-toggle",                                            // toggle button styled
		".pbcssg-theme-toggle[hidden]",                                    // hidden until JS reveals it
		".pbcssg-logo--dark { display: none; }",                           // dark logo hidden by default
		`:root[data-theme="dark"] .pbcssg-logo--dark { display: block; }`, // toggle wins for the logo swap
	} {
		if !strings.Contains(CSS, want) {
			t.Errorf("theme CSS missing %q", want)
		}
	}
	// The CSS is a Go raw-string const: a backtick inside would break the build.
	if strings.Contains(CSS, "`") {
		t.Errorf("theme CSS must not contain a backtick (it terminates the raw string)")
	}
}

func TestFonts(t *testing.T) {
	// The stylesheet drives fonts through variables the operator choice can override.
	for _, want := range []string{"--font-sans:", "--font-mono:", "font-family: var(--font-sans)", "font-family: var(--font-mono)"} {
		if !strings.Contains(CSS, want) {
			t.Errorf("theme CSS missing %q", want)
		}
	}
	// System is the built-in default → no override emitted.
	if FontCSS("system") != "" || FontCSS("bogus") != "" {
		t.Errorf("system/unknown font should emit no override")
	}
	// A real choice emits a :root override setting --font-sans to its stack only.
	css := FontCSS("transitional")
	if !strings.Contains(css, "--font-sans:") || !strings.Contains(css, "Charter") {
		t.Errorf("transitional font override wrong: %q", css)
	}
	// Security: every allowlisted stack is free of CSS-breaking characters, so
	// injecting it into ":root { --font-sans: <stack>; }" can't break out of the
	// declaration (no ";", "{", "}", "@", or url()).
	for _, f := range Fonts {
		if strings.ContainsAny(f.Stack, ";{}@") || strings.Contains(strings.ToLower(f.Stack), "url(") {
			t.Errorf("font stack %q (%s) contains a CSS-breaking character", f.ID, f.Label)
		}
	}
	if !ValidFont("system") || !ValidFont("transitional") || ValidFont("nope") {
		t.Errorf("ValidFont wrong")
	}
}
