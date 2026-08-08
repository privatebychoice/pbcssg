package creator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/store"
)

// themeVar is one CSS custom property the settings form exposes for theming
// (§6.4). Values are optional; an empty value falls back to the built-in theme.
type themeVar struct {
	Key   string // CSS custom property, e.g. "--accent"
	Field string // form field suffix, e.g. "accent" (name = var_accent)
	Label string
}

var themeVars = []themeVar{
	{"--accent", "accent", "Accent colour"},
	{"--bg", "bg", "Background"},
	{"--fg", "fg", "Text colour"},
	{"--border", "border", "Border"},
	{"--card-bg", "card_bg", "Card background"},
	{"--muted", "muted", "Muted text"},
	{"--measure", "measure", "Content width (measure)"},
}

// themeDefaults are the built-in theme's values, shown as placeholders.
var themeDefaults = map[string]string{
	"--accent": "#0b5cad", "--bg": "#ffffff", "--fg": "#1a1a1a",
	"--border": "#d8d8d8", "--card-bg": "#f6f7f9", "--muted": "#5a5a5a",
	"--measure": "44rem", // matches the built-in --measure in theme.CSS
}

var (
	importRE = regexp.MustCompile(`(?i)@import`)
	urlRE    = regexp.MustCompile(`(?i)url\(\s*(['"]?)([^'")]*)['"]?\s*\)`)
	schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

// validateThemeCSS enforces the §6.4 privacy guardrail: operator CSS must not
// pull in external resources. It rejects @import and any url() that points
// off-site (an absolute scheme other than data:, or a protocol-relative //host).
// Same-site relative/rooted paths (e.g. /media/…) and data: URIs are allowed.
//
// The scan runs on the CSS with backslash escapes decoded first (decodeCSSEscapes):
// a browser resolves "\3a" to ":" and "\2f" to "/" at parse time, so a raw-text
// scan alone could be bypassed with url(https\3a\2f\2fevil.example/x) or an
// escaped @\69mport. Decoding reveals the characters the browser will actually
// see, closing that bypass. Only the served CSS is escaped; validation uses the
// decoded copy.
func validateThemeCSS(css string) error {
	css = decodeCSSEscapes(css)
	if importRE.MatchString(css) {
		return fmt.Errorf("@import is not allowed (it would load an external stylesheet)")
	}
	for _, m := range urlRE.FindAllStringSubmatch(css, -1) {
		target := strings.TrimSpace(m[2])
		if target == "" {
			continue
		}
		if strings.HasPrefix(target, "//") {
			return fmt.Errorf("external url(%s) is not allowed", target)
		}
		if schemeRE.MatchString(target) {
			scheme := strings.ToLower(target[:strings.IndexByte(target, ':')])
			if scheme != "data" {
				return fmt.Errorf("external url(%s) is not allowed — use a same-site path or a data: URI", target)
			}
		}
	}
	return nil
}

// decodeCSSEscapes resolves CSS backslash escapes so the external-resource scan
// sees the characters the browser will parse, not the source bytes. Per the CSS
// syntax spec, "\" followed by 1–6 hex digits (plus one optional trailing
// whitespace) is a code point, "\" + newline is a line continuation (removed),
// and "\" + any other character is that literal character. It is used only to
// build the string that validateThemeCSS scans — never to transform served CSS.
func decodeCSSEscapes(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s // fast path: nothing to decode
	}
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' || i+1 >= len(rs) {
			if rs[i] != '\\' { // a trailing lone backslash is dropped
				b.WriteRune(rs[i])
			}
			continue
		}
		next := rs[i+1]
		switch {
		case isHexDigit(next):
			j := i + 1
			for j < len(rs) && j-(i+1) < 6 && isHexDigit(rs[j]) {
				j++
			}
			if cp, err := strconv.ParseInt(string(rs[i+1:j]), 16, 32); err == nil && cp > 0 && cp <= 0x10FFFF {
				b.WriteRune(rune(cp))
			}
			i = j - 1
			if i+1 < len(rs) && isCSSWhitespace(rs[i+1]) {
				i++ // consume one trailing whitespace that terminates the hex escape
			}
		case next == '\n' || next == '\r' || next == '\f':
			i++ // line continuation: drop the backslash and the newline
		default:
			b.WriteRune(next) // escaped literal, e.g. "\:" → ":"
			i++
		}
	}
	return b.String()
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isCSSWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
}

// composeThemeOverride builds the override stylesheet (a :root block from the
// variable map plus the raw custom CSS) that is layered over the built-in theme.
func composeThemeOverride(vars map[string]string, customCSS string) string {
	var b strings.Builder
	keys := make([]string, 0, len(vars))
	for k, v := range vars {
		if strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		b.WriteString(":root {\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s;\n", k, strings.TrimSpace(vars[k]))
		}
		b.WriteString("}\n")
	}
	if strings.TrimSpace(customCSS) != "" {
		b.WriteString(customCSS)
		if !strings.HasSuffix(customCSS, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// validateThemeVars checks each structured theme-var value is a single CSS value,
// not a rule. The colour/width fields feed a ":root { --x: <value>; }" block, so a
// ";", "{", "}", or "@" would break out of the declaration, and "url(" / "\" /
// a newline reopen the external-resource surface the Custom CSS box is validated
// against. Rejecting these keeps the fields to plain values and steers arbitrary
// CSS to the Custom CSS box (Finding #3). It does not type-check colour vs length:
// legitimate modern values (var(), clamp(), light-dark()) must still pass.
func validateThemeVars(vars map[string]string) error {
	for _, v := range themeVars {
		val := strings.TrimSpace(vars[v.Key])
		if val == "" {
			continue
		}
		if strings.ContainsAny(val, ";{}@\\\n\r") || strings.Contains(strings.ToLower(val), "url(") {
			return fmt.Errorf("%s (%s): %q is not a plain CSS value — remove ; { } @ \\ or url(), and put full rules in Custom CSS", v.Label, v.Key, val)
		}
	}
	return nil
}

// themeVarsFromForm collects the CSS-variable overrides the operator entered.
func themeVarsFromForm(r formValuer) map[string]string {
	m := map[string]string{}
	for _, v := range themeVars {
		if val := strings.TrimSpace(r.FormValue("var_" + v.Field)); val != "" {
			m[v.Key] = val
		}
	}
	return m
}

// storedThemeVars reads the persisted CSS-variable overrides from a store.
func storedThemeVars(st *store.Store) map[string]string {
	m := map[string]string{}
	if v, ok, err := st.Setting(keyThemeVars); err == nil && ok && v != "" {
		_ = json.Unmarshal([]byte(v), &m)
	}
	return m
}

// storedCustomCSS reads the persisted raw custom-CSS block from a store.
func storedCustomCSS(st *store.Store) string {
	v, _, _ := st.Setting(keyThemeCustom)
	return v
}

// themeOverride composes the persisted theme override, re-validating it so a
// hand-edited or rejected value falls back to the built-in theme (§6.4). It is
// store-based so the headless build applies the same override the editor does.
func themeOverride(st *store.Store) string {
	override := composeThemeOverride(storedThemeVars(st), storedCustomCSS(st))
	if validateThemeCSS(override) != nil {
		return "" // fall back to the built-in theme baseline
	}
	return override
}

func (c *Creator) storedThemeVars() map[string]string { return storedThemeVars(c.store) }
func (c *Creator) storedCustomCSS() string            { return storedCustomCSS(c.store) }
func (c *Creator) themeOverride() string              { return themeOverride(c.store) }

// saveThemeSettings persists the theme override pieces.
func (c *Creator) saveThemeSettings(vars map[string]string, customCSS string) error {
	b, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	if err := c.store.SetSetting(keyThemeVars, string(b)); err != nil {
		return err
	}
	return c.store.SetSetting(keyThemeCustom, customCSS)
}
