package creator

import (
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/theme"
)

func TestValidateThemeCSS(t *testing.T) {
	ok := []string{
		"",
		":root{--accent:#0b5cad}",
		".x{background:url(/media/bg.png)}",
		".x{background:url('/media/bg.png')}",
		".y{background:url(data:image/png;base64,AAAA)}",
		".z{background:url(bg.svg)}",
	}
	for _, css := range ok {
		if err := validateThemeCSS(css); err != nil {
			t.Errorf("validateThemeCSS(%q) unexpected error: %v", css, err)
		}
	}

	bad := []string{
		"@import 'https://evil.example/x.css';",
		"@import url(evil.css);",
		".x{background:url(https://cdn.evil.example/bg.png)}",
		".x{background:url(//cdn.evil.example/bg.png)}",
		".x{background:url(http://evil.example/a)}",
		// CSS-escape bypasses (Finding #1): the browser resolves these to a scheme
		// or protocol-relative prefix at parse time, so a raw-text scan would miss
		// them — decodeCSSEscapes closes the gap.
		`.x{background:url(https\3a\2f\2fevil.example/bg.png)}`, // \3a=":" hides the scheme
		`.x{background:url(https\3a //evil.example/bg.png)}`,    // \3a + one trailing space
		`.x{background:url(\00002f\00002fevil.example/bg.png)}`, // 6-digit \2f\2f → protocol-relative //
		`@\69mport url(https://evil.example/x.css)`,             // \69="i" hides @import
		`.x{background:url(javascript\3avoid)}`,                 // escaped scheme
	}
	for _, css := range bad {
		if err := validateThemeCSS(css); err == nil {
			t.Errorf("validateThemeCSS(%q) should be rejected", css)
		}
	}
}

func TestDecodeCSSEscapes(t *testing.T) {
	cases := map[string]string{
		``:                  ``,
		`no escapes here`:   `no escapes here`,
		`https\3a\2f\2fx`:   `https://x`, // \2fx → "/" then literal x (x is not a hex digit)
		`https\3a //x`:      `https://x`, // one trailing space terminates the hex escape
		`\41\42\43`:         `ABC`,
		`\:`:                `:`,              // escaped literal
		`a\\b`:              `a\b`,            // escaped backslash
		`/media/a\3a b.png`: `/media/a:b.png`, // the space after \3a is consumed by the escape
		"line\\\ncont":      "linecont",       // backslash-newline continuation
		`trailing\`:         `trailing`,       // lone trailing backslash dropped
	}
	for in, want := range cases {
		if got := decodeCSSEscapes(in); got != want {
			t.Errorf("decodeCSSEscapes(%q) = %q, want %q", in, got, want)
		}
	}
	// A same-site escaped path stays allowed (colon inside a filename is not a scheme).
	if err := validateThemeCSS(`.x{background:url(/media/a\3a b.png)}`); err != nil {
		t.Errorf("same-site escaped path should pass: %v", err)
	}
}

func TestValidateThemeVars(t *testing.T) {
	// Plain values — including legitimate modern CSS — pass.
	for _, v := range []map[string]string{
		{"--accent": "#0b5cad"},
		{"--bg": "rgb(11 92 173)"},
		{"--measure": "clamp(40rem, 90vw, 60rem)"},
		{"--fg": "var(--brand)"},
		{"--card-bg": "light-dark(#fff, #111)"},
		{},
		{"--accent": "   "}, // blank after trim is ignored
	} {
		if err := validateThemeVars(v); err != nil {
			t.Errorf("validateThemeVars(%v) unexpected error: %v", v, err)
		}
	}
	// Breakout / external-resource attempts in a structured field are rejected.
	for _, v := range []map[string]string{
		{"--accent": "red; } body{display:none} :root{--x:y"}, // declaration breakout
		{"--bg": "url(/media/x.png)"},                         // url() belongs in Custom CSS
		{"--measure": "44rem; @import 'x'"},                   // @ / ;
		{"--fg": "#000\n--bg: #fff"},                          // newline
		{"--border": `#000\3a`},                               // backslash escape
	} {
		if err := validateThemeVars(v); err == nil {
			t.Errorf("validateThemeVars(%v) should be rejected", v)
		}
	}
}

// TestMeasureDefaultMatchesStylesheet guards Finding #2: the Content-width
// placeholder shown in Settings must match the built-in --measure default.
func TestMeasureDefaultMatchesStylesheet(t *testing.T) {
	if got := themeDefaults["--measure"]; !strings.Contains(theme.CSS, "--measure: "+got) {
		t.Errorf("themeDefaults[--measure]=%q not found as a default in theme.CSS", got)
	}
}

func TestComposeThemeOverride(t *testing.T) {
	got := composeThemeOverride(map[string]string{"--accent": "#123456", "--bg": ""}, ".x{color:red}")
	if !strings.Contains(got, "--accent: #123456;") {
		t.Errorf("missing accent var:\n%s", got)
	}
	if strings.Contains(got, "--bg") {
		t.Errorf("empty var should be omitted:\n%s", got)
	}
	if !strings.Contains(got, ".x{color:red}") {
		t.Errorf("missing custom css:\n%s", got)
	}
}

func TestThemeSettingsSaveAndReject(t *testing.T) {
	h := newHarness(t)

	// Valid theme override is stored and reflected in the effective config +
	// the preview stylesheet.
	rec := h.post("/admin/settings", h.form(map[string]string{
		"baseURL": "https://tul.example", "var_accent": "#ff8800", "customCSS": ".lead{font-weight:700}",
	}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Settings saved") {
		t.Fatalf("save theme: code=%d\n%s", rec.Code, rec.Body.String())
	}
	ov := h.c.state().build.ThemeOverride
	if !strings.Contains(ov, "--accent: #ff8800;") || !strings.Contains(ov, ".lead{font-weight:700}") {
		t.Errorf("override not applied: %q", ov)
	}
	// Preview stylesheet includes the built-in theme AND the override.
	css := h.get("/admin/assets/theme.css").Body.String()
	if !strings.Contains(css, "--accent") || !strings.Contains(css, ".lead{font-weight:700}") {
		t.Errorf("preview theme.css missing override")
	}

	// An external url() is rejected; nothing is persisted.
	bad := h.post("/admin/settings", h.form(map[string]string{
		"baseURL": "https://tul.example", "customCSS": ".x{background:url(https://cdn.evil.example/b.png)}",
	}))
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "rejected") {
		t.Fatalf("external url should be rejected: code=%d\n%s", bad.Code, bad.Body.String())
	}
	if got := h.c.storedCustomCSS(); strings.Contains(got, "evil.example") {
		t.Errorf("rejected CSS must not be persisted, got %q", got)
	}
	// The previously-saved good override is still intact.
	if !strings.Contains(h.c.state().build.ThemeOverride, "#ff8800") {
		t.Errorf("rejecting a bad save must not clobber the good override")
	}

	// A structured colour/width field that isn't a plain value is rejected before
	// it reaches the :root block (Finding #3), and nothing is persisted.
	badVar := h.post("/admin/settings", h.form(map[string]string{
		"baseURL": "https://tul.example", "var_bg": "#fff; } body{display:none}",
	}))
	if badVar.Code != 400 || !strings.Contains(badVar.Body.String(), "rejected") {
		t.Fatalf("breakout theme var should be rejected: code=%d\n%s", badVar.Code, badVar.Body.String())
	}
	if strings.Contains(h.c.state().build.ThemeOverride, "display:none") {
		t.Errorf("rejected theme var must not be applied")
	}
}
