# Theming pbcssg

pbcssg ships one self-hosted stylesheet (no web fonts, no third-party requests).
It defines a **light** and a **dark** palette, follows each visitor's operating
system preference by default, and gives visitors a **footer toggle** to override
that choice. This document covers the colour model, the toggle, and how an
operator customises colours.

The built-in theme is always the baseline; operator customisation layers on top
of it and is validated to stay self-hosted (no external `url()` / `@import`).

---

## Colour model

Every colour is a CSS custom property. There are two layers:

1. **Palette constants** — the raw colours of each scheme, defined once:
   `--pbc-light-*` and `--pbc-dark-*`.
2. **Semantic tokens** — what the rest of the stylesheet actually uses:
   `--bg`, `--fg`, `--muted`, `--accent`, `--border`, `--card-bg`, `--focus`.
   These are *mapped* from one palette depending on the active mode.

The active mode is chosen like this:

| Mode | Selector | When it applies |
|------|----------|-----------------|
| **Light** (default) | `:root` | No OS dark preference and no visitor choice |
| **Auto → Dark** | `@media (prefers-color-scheme: dark) :root` | The OS/browser reports a dark preference and the visitor hasn't chosen |
| **Forced Light** | `:root[data-theme="light"]` | The visitor picked Light in the footer toggle |
| **Forced Dark** | `:root[data-theme="dark"]` | The visitor picked Dark in the footer toggle |

`color-scheme` is set per mode (`light dark` for auto, `light`/`dark` when forced)
so native form controls, scrollbars, and the caret match the page.

### The colour tokens

| Token | Light default | Dark default | Used for |
|-------|---------------|--------------|----------|
| `--bg` | `#ffffff` | `#14161a` | Page background |
| `--fg` | `#1a1a1a` | `#e7e9ec` | Body text |
| `--muted` | `#5a5a5a` | `#a2a8b0` | Secondary text (footer, captions, reasons) |
| `--accent` | `#0b5cad` | `#6cb2f0` | Links, focus ring, active states |
| `--border` | `#d8d8d8` | `#2b2f36` | Rules, card and input borders |
| `--card-bg` | `#f6f7f9` | `#1c1f25` | Code blocks, callouts, cards, the toggle |
| `--focus` | `#0b5cad` | `#6cb2f0` | `:focus-visible` outline |

Two non-colour tokens control width: `--measure` (reading column, `44rem`) and
`--measure-wide` (breakout width for images/media/tables/code, `64rem`).

---

## The light/dark toggle (visitor-facing)

A small **`◐ Auto` button appears in the footer** of every page. Activating it
cycles **Auto → Light → Dark → Auto**:

- **Auto** (the default): the page follows the visitor's OS/browser preference
  via `@media (prefers-color-scheme)` — exactly the behaviour before the toggle
  existed. Nothing is stored.
- **Light / Dark**: the visitor's explicit choice overrides the OS, and is
  remembered for next time.

### How it works

- A tiny self-hosted script (`assets/pbcssg-theme.js`) loads **blocking in
  `<head>`, before the stylesheet**, so a stored choice is applied to
  `<html data-theme="…">` *before first paint* — no flash of the wrong theme.
- The choice is stored in **first-party `localStorage`** (`pbcssg-theme` =
  `light` | `dark`; absent = Auto). It is **never sent to a server** — a static
  site has nothing to read it, and a cookie would only add request bytes.
- **Progressive enhancement**: the button is rendered `hidden` and revealed by
  the script, so visitors without JavaScript never see a dead control — they get
  Auto (the OS preference), which is the correct default anyway.
- **Hardened-browser safe**: all storage access is wrapped in `try/catch`. If a
  browser blocks `localStorage` (private mode, lockdown settings), the script
  degrades to Auto instead of erroring.
- **CSP**: the script is same-origin, so `default-src 'self'` (script-src falls
  back to it) already allows it — no inline code, no hash, no policy change.

### Why the toggle matters

Some privacy-hardened browsers **force `prefers-color-scheme` to report light**
regardless of the OS or the site — most notably Firefox with
`privacy.resistFingerprinting` enabled (also LibreWolf, the Tor Browser, and
`arkenfox`-style setups). Those visitors can never get Auto-dark; the manual
toggle is the only way for them to choose dark. (See "Browser quirks" below.)

---

## Customising colours (operator)

Two mechanisms, both in the editor under **Settings → Theme**, both validated to
forbid external `url()` and `@import` (§6.4). Validation decodes CSS backslash
escapes first, so an escaped scheme (e.g. `url(https\3a\2f\2f…)`) or escaped
`@\69mport` can't slip an external resource past the check — it is scanned as the
browser would resolve it:

### 1. Variable fields

The form exposes the common tokens (`--accent`, `--bg`, `--fg`, `--border`,
`--card-bg`, `--muted`, plus `--measure`). A value here overrides that token for
**Light and Auto-Dark**. Leave a field blank to keep the built-in value.

> Scope note: variable fields override the light/auto baseline. When a visitor
> **forces** Light or Dark with the toggle, the built-in palette for that mode is
> used unless you also override the forced mode (below). This keeps the forced
> modes predictable and is usually what you want.

### 2. Custom CSS — including per-mode overrides

The **Custom CSS** box is appended last, so it can target any selector. To
re-colour a **forced** mode, use the `data-theme` selectors:

```css
/* Brand accent everywhere */
:root { --accent: #8a3ffc; }

/* A custom dark background only when a visitor forces Dark */
:root[data-theme="dark"] { --bg: #0d1b2a; --card-bg: #142433; }

/* A warmer forced-light background */
:root[data-theme="light"] { --bg: #fbf7f0; }
```

Because these carry the same specificity as the built-in `data-theme` rules and
come later in the stylesheet, they win. You can likewise restyle the toggle
button via `.pbcssg-theme-toggle`.

---

## Accessibility

- The toggle is a real `<button>` — keyboard focusable, with a visible
  `:focus-visible` outline and an `aria-label` that states the current mode and
  that activating changes it.
- The built-in palettes meet WCAG AA contrast for body text in both schemes;
  if you override colours, re-check contrast (text on `--bg`, links on `--bg`).
- Colour is never the only signal; the toggle also shows a text label
  (`Auto`/`Light`/`Dark`) and an icon.

## Browser quirks (Firefox `resistFingerprinting`)

If a page renders light in Firefox even though the OS and Firefox theme are dark,
the cause is almost always `privacy.resistFingerprinting = true`, which forces
`prefers-color-scheme` to light for anti-fingerprinting reasons — overriding the
OS, the *Website appearance* setting, DevTools, and `about:config` overrides.

- To let a specific site follow the OS again: add its host to
  `privacy.resistFingerprinting.exemptedDomains`.
- To force dark globally while keeping RFP: create the number pref
  `ui.systemUsesDarkTheme = 1` (0 = light, 1 = dark, 2 = no-preference).

The site's footer toggle is the visitor-facing answer to this: it sets
`data-theme` directly and does not depend on `prefers-color-scheme`, so it works
even under RFP.

---

## Body font

Settings → Theme → **Body font** is a dropdown of curated **system-font stacks**
(System, Humanist sans, Geometric sans, Neo-grotesque sans, Old-style serif,
Transitional serif). It sets the `--font-sans` variable that the body text uses;
code stays monospace (`--font-mono`).

Why a fixed list and not a text field:

- **No third-party requests.** Every option is composed of fonts already on the
  visitor's device — nothing is downloaded, so choosing one can't reintroduce a
  web-font tracker (e.g. Google Fonts logging IPs) or a supply-chain vector. This
  keeps the self-hosted posture (SPEC §8).
- **No CSS injection.** The operator picks an **ID**; the build maps it to a
  hardcoded stack from `theme.Fonts` and layers `:root { --font-sans: … }` after
  the built-in default. Operator free-text never reaches the CSS, so a stray
  `;`/`{`/`url()` can't break out of the declaration.
- **Instant + accessible.** System fonts render immediately (no FOUT/FOIT), scale
  with the user's settings, and stay legible.

Want a genuinely custom, branded font? That needs a *self-hosted* `@font-face`:
upload a `woff2` and add the `@font-face` + `font-family` in the **Custom CSS**
box (a same-site `url(/media/…)` is allowed; external `url()`/`@import` are
rejected). A dedicated upload pipeline for that isn't built yet.

---

## File reference

| File | Role |
|------|------|
| `internal/theme/theme.go` | The default stylesheet (`theme.CSS`) — palettes, mode mapping, all component styles |
| `internal/render/render.go` | `ThemeJS` (the toggle script), the `<head>` script tag, and the footer button in the base layout |
| `assets/theme.<hash>.css` | The fingerprinted stylesheet in a built bundle |
| `assets/pbcssg-theme.<hash>.js` | The fingerprinted toggle script in a built bundle |
