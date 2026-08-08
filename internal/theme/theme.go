// Package theme provides pbcssg's default, self-hosted stylesheet. It uses only
// system fonts (no web fonts, no third-party requests — SPEC §8), supports light
// and dark colour schemes, keeps visible focus states for accessibility, and
// styles the class hooks the renderer emits (search widget, youtube consent card
// and facade, and general content).
package theme

// Path is the base (pre-fingerprint) location of the stylesheet in the bundle.
const Path = "assets/theme.css"

// CSS is the default stylesheet.
//
// Colour model (SPEC §6.4, see docs/THEME.md): each scheme's palette is defined
// once as --pbc-light-* / --pbc-dark-* constants, and the active semantic tokens
// (--bg, --fg, …) are mapped from one palette per mode:
//   - default            → light palette
//   - @media dark         → dark palette (the OS-preference "Auto" default)
//   - :root[data-theme=…] → the visitor's explicit override from the footer toggle
//
// The media rule stays plain :root (0,0,1 specificity) so an operator variable
// override (appended as :root{…}, §6.4) still wins in Auto mode as before; the
// data-theme rules are attribute-qualified so an explicit toggle choice wins over
// both. Operators who want to re-colour a forced mode use a
// :root[data-theme="dark"]{…} rule in their Custom CSS.
const CSS = `:root {
  color-scheme: light dark;
  /* Light palette */
  --pbc-light-bg: #ffffff;
  --pbc-light-fg: #1a1a1a;
  --pbc-light-muted: #5a5a5a;
  --pbc-light-accent: #0b5cad;
  --pbc-light-border: #d8d8d8;
  --pbc-light-card-bg: #f6f7f9;
  --pbc-light-focus: #0b5cad;
  /* Dark palette */
  --pbc-dark-bg: #14161a;
  --pbc-dark-fg: #e7e9ec;
  --pbc-dark-muted: #a2a8b0;
  --pbc-dark-accent: #6cb2f0;
  --pbc-dark-border: #2b2f36;
  --pbc-dark-card-bg: #1c1f25;
  --pbc-dark-focus: #6cb2f0;
  /* Active semantic tokens — default to the light palette */
  --bg: var(--pbc-light-bg);
  --fg: var(--pbc-light-fg);
  --muted: var(--pbc-light-muted);
  --accent: var(--pbc-light-accent);
  --border: var(--pbc-light-border);
  --card-bg: var(--pbc-light-card-bg);
  --focus: var(--pbc-light-focus);
  --measure: 44rem;       /* reading measure for flowing text (~80–85 chars/line) */
  --measure-wide: 64rem;  /* breakout width for images, media, tables, code */
  /* Font stacks — system fonts only (no web fonts, no downloads). The operator's
     "Body font" choice (§6.4) overrides --font-sans from a fixed allowlist. */
  --font-sans: system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
/* Auto: follow the OS when the visitor has not chosen a theme. */
@media (prefers-color-scheme: dark) {
  :root {
    --bg: var(--pbc-dark-bg);
    --fg: var(--pbc-dark-fg);
    --muted: var(--pbc-dark-muted);
    --accent: var(--pbc-dark-accent);
    --border: var(--pbc-dark-border);
    --card-bg: var(--pbc-dark-card-bg);
    --focus: var(--pbc-dark-focus);
  }
}
/* Explicit override from the footer toggle (wins over Auto and OS). */
:root[data-theme="light"] {
  color-scheme: light;
  --bg: var(--pbc-light-bg);
  --fg: var(--pbc-light-fg);
  --muted: var(--pbc-light-muted);
  --accent: var(--pbc-light-accent);
  --border: var(--pbc-light-border);
  --card-bg: var(--pbc-light-card-bg);
  --focus: var(--pbc-light-focus);
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --bg: var(--pbc-dark-bg);
  --fg: var(--pbc-dark-fg);
  --muted: var(--pbc-dark-muted);
  --accent: var(--pbc-dark-accent);
  --border: var(--pbc-dark-border);
  --card-bg: var(--pbc-dark-card-bg);
  --focus: var(--pbc-dark-focus);
}

* { box-sizing: border-box; }

html { -webkit-text-size-adjust: 100%; }

body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font-family: var(--font-sans);
  font-size: 1.05rem;
  line-height: 1.6;
}

main { max-width: var(--measure-wide); margin-inline: auto; padding-inline: 1.25rem; padding-block: 1rem 3rem; }
/* Header/footer align their content with the text column: a width-based gutter
   (not padding) so their content edge matches the centred flow text, whose left
   edge sits inside the wider main column rather than at its padding edge. */
header, footer { width: calc(100% - 2.5rem); max-width: var(--measure); margin-inline: auto; }
/* Flowing content sits at a comfortable reading measure, centred within the wider
   page column; rich blocks (images, media, tables, code) break out to the wide
   measure so they use the space without stretching text lines. */
main > * { max-width: var(--measure); margin-inline: auto; }
main > figure.pbcssg-figure, main > figure.pbcssg-media, main > figure.pbcssg-code, main > table, main > pre { max-width: var(--measure-wide); }
/* A standalone image on its own line (Markdown image, optionally linked) is media
   too: break it out like a figure, and centre it so a smaller-than-column image
   doesn't float to one side. Inline images mixed with text stay in the column. */
main > p:has(> img:only-child), main > p:has(> a:only-child > img:only-child) { max-width: var(--measure-wide); }
main > p:has(> img:only-child) > img, main > p:has(> a:only-child > img:only-child) img { display: block; margin-inline: auto; }
header { padding-block: 1rem; }
footer { padding-block: 1.5rem 2.5rem; color: var(--muted); font-size: 0.9rem; border-top: 1px solid var(--border); margin-top: 2rem; }
.pbcssg-footer { text-align: center; }
.pbcssg-footer-nav { display: flex; flex-wrap: wrap; justify-content: center; gap: 0.25rem 0.6rem; margin-bottom: 0.5rem; }
.pbcssg-footer-nav a { color: var(--fg); text-decoration: none; }
.pbcssg-footer-nav a:hover, .pbcssg-footer-nav a:focus-visible { color: var(--accent); text-decoration: underline; }
.pbcssg-copyright { margin: 0; }
/* Light/dark toggle: hidden until the theme script reveals it (progressive
   enhancement — no dead control when JavaScript or storage is unavailable). */
.pbcssg-theme-toggle {
  margin-top: 0.75rem;
  font: inherit;
  font-size: 0.85rem;
  color: var(--muted);
  background: var(--card-bg);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 0.25rem 0.8rem;
  cursor: pointer;
}
.pbcssg-theme-toggle:hover, .pbcssg-theme-toggle:focus-visible { color: var(--fg); border-color: var(--accent); }
.pbcssg-theme-toggle[hidden] { display: none; }

h1, h2, h3, h4 { line-height: 1.25; margin-block: 1.6em 0.5em; }
h1 { font-size: 1.9rem; }
h2 { font-size: 1.45rem; }
h3 { font-size: 1.2rem; }
p { margin-block: 0 1em; }

a { color: var(--accent); text-decoration: underline; text-underline-offset: 2px; }
a:hover { text-decoration-thickness: 2px; }

:focus-visible { outline: 3px solid var(--focus); outline-offset: 2px; border-radius: 2px; }

img { max-width: 100%; height: auto; }

pre, code { font-family: var(--font-mono); font-size: 0.92em; }
pre { background: var(--card-bg); padding: 1rem; border-radius: 8px; overflow-x: auto; }
code { background: var(--card-bg); padding: 0.15em 0.35em; border-radius: 4px; }
pre code { background: none; padding: 0; }

blockquote { margin-inline: auto; padding-inline-start: 1rem; border-inline-start: 3px solid var(--border); color: var(--muted); }

/* Search widget (§6.2) */
/* Site header + primary navigation */
.pbcssg-header { display: flex; flex-wrap: wrap; gap: 0.75rem 1.5rem; align-items: center; justify-content: space-between; margin-block-end: 1.5rem; padding-block-end: 0.75rem; border-bottom: 1px solid var(--border); }
/* Header brand (§6.4): a wordmark, a self-hosted logo, or both. It pushes the
   nav/search to the opposite edge; centred mode stacks it above the nav. */
.pbcssg-brand { margin-inline-end: auto; display: inline-flex; align-items: center; gap: 0.6rem; text-decoration: none; color: var(--fg); }
.pbcssg-brand-text { font-size: 1.2rem; font-weight: 700; }
.pbcssg-brand:hover .pbcssg-brand-text, .pbcssg-brand:focus-visible .pbcssg-brand-text { color: var(--accent); }
.pbcssg-logo { display: block; width: auto; }
.pbcssg-logo--small { height: 24px; }
.pbcssg-logo--medium { height: 32px; }
.pbcssg-logo--large { height: 44px; }
/* Optional dark-mode logo (§6.4): when both --light and --dark logos are present,
   show one per theme. Mirrors the token dual-signal — @media follows the OS in
   Auto mode, and the attribute-qualified data-theme rules let the footer toggle
   win over the OS. (Single-logo headers carry neither class, so are unaffected.) */
.pbcssg-logo--dark { display: none; }
@media (prefers-color-scheme: dark) {
  .pbcssg-logo--light { display: none; }
  .pbcssg-logo--dark { display: block; }
}
:root[data-theme="light"] .pbcssg-logo--light { display: block; }
:root[data-theme="light"] .pbcssg-logo--dark { display: none; }
:root[data-theme="dark"] .pbcssg-logo--light { display: none; }
:root[data-theme="dark"] .pbcssg-logo--dark { display: block; }
.pbcssg-header--center { flex-direction: column; justify-content: center; text-align: center; }
.pbcssg-header--center .pbcssg-brand { margin-inline-end: 0; }
.pbcssg-header--center .pbcssg-nav { justify-content: center; }
.pbcssg-nav { display: flex; flex-wrap: wrap; gap: 0.25rem 1.15rem; }
.pbcssg-nav a { text-decoration: none; color: var(--fg); font-weight: 600; }
.pbcssg-nav a:hover, .pbcssg-nav a:focus-visible { color: var(--accent); text-decoration: underline; }
.pbcssg-search { position: relative; margin: 0; }
/* Label sits to the left of the box (visible + persistent — clearest for
   low-vision / magnifier users, who would otherwise lose an in-box placeholder
   on typing or to low contrast). */
.pbcssg-search-row { display: flex; align-items: center; gap: 0.5rem; }
.pbcssg-search-label { font-weight: 600; font-size: 0.9rem; white-space: nowrap; }
.pbcssg-search input[type="search"] {
  flex: 1; padding: 0.55rem 0.75rem; font-size: 1rem;
  color: var(--fg); background: var(--bg);
  border: 1px solid var(--border); border-radius: 8px;
}
/* Keep placeholder text at label-grade contrast (browsers default to a faint
   grey that fails WCAG 1.4.3); opacity:1 stops Firefox dimming it further. */
.pbcssg-search input[type="search"]::placeholder { color: var(--muted); opacity: 1; }
.pbcssg-search-results { list-style: none; margin: 0.35rem 0 0; padding: 0; }
.pbcssg-search-results:not(:empty) { border: 1px solid var(--border); border-radius: 8px; background: var(--card-bg); }
.pbcssg-search-results li { margin: 0; }
.pbcssg-search-results a { display: block; padding: 0.5rem 0.75rem; text-decoration: none; border-bottom: 1px solid var(--border); }
.pbcssg-search-results li:last-child a { border-bottom: 0; }
.pbcssg-search-results a:hover, .pbcssg-search-results a:focus-visible { background: var(--accent); color: var(--bg); }

/* Callout / admonition block */
.pbcssg-callout { border: 1px solid var(--border); border-left-width: 4px; border-radius: 8px; background: var(--card-bg); padding: 0.85rem 1.1rem; margin-block: 1.5rem; }
.pbcssg-callout > :last-child { margin-bottom: 0; }
.pbcssg-callout-title { font-weight: 600; margin: 0 0 0.4rem; }
.pbcssg-callout-note { border-left-color: var(--accent); }
.pbcssg-callout-tip { border-left-color: #1a7f37; }
.pbcssg-callout-warning { border-left-color: #c05621; }
.pbcssg-callout-info { border-left-color: #0b5cad; }
.pbcssg-callout-note .pbcssg-callout-title::before { content: "ℹ️ "; }
.pbcssg-callout-tip .pbcssg-callout-title::before { content: "💡 "; }
.pbcssg-callout-warning .pbcssg-callout-title::before { content: "⚠️ "; }
.pbcssg-callout-info .pbcssg-callout-title::before { content: "ℹ️ "; }

/* Code block (§6.12) — verbatim <pre><code>, no highlighting. Caption bar, copy
   button, and an optional non-selectable line-number gutter (CSS counters). */
.pbcssg-code { margin-block: 1.5rem; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.pbcssg-code-caption { display: flex; justify-content: space-between; align-items: center; gap: 0.75rem; padding: 0.4rem 0.75rem; background: var(--card-bg); border-bottom: 1px solid var(--border); font-family: var(--font-mono); font-size: 0.85rem; }
.pbcssg-code-filename { color: var(--fg); font-weight: 600; }
.pbcssg-code-lang { color: var(--muted); text-transform: uppercase; letter-spacing: 0.03em; font-size: 0.8em; }
.pbcssg-code-wrap { position: relative; }
.pbcssg-code-copy { position: absolute; top: 0.5rem; right: 0.5rem; z-index: 1; font: inherit; font-size: 0.8rem; cursor: pointer; color: var(--fg); background: var(--card-bg); border: 1px solid var(--border); border-radius: 6px; padding: 0.25rem 0.6rem; opacity: 0; transition: opacity 0.12s; }
.pbcssg-code-wrap:hover .pbcssg-code-copy, .pbcssg-code-copy:focus-visible { opacity: 1; }
.pbcssg-code-copy:hover { border-color: var(--accent); }
.pbcssg-code .pbcssg-code-pre { margin: 0; border-radius: 0; }
/* Line-number spans stay inline in both modes: the literal newline between them (kept
   for copy/paste fidelity) does the line breaking, so a numbered block is single-spaced
   — making the spans display:block would add a second break per line. The ::before
   counter increments regardless of display and renders a non-selectable gutter. */
.pbcssg-code-line { display: inline; }
.pbcssg-code--numbered .pbcssg-code-el { counter-reset: pbcssg-line; }
.pbcssg-code--numbered .pbcssg-code-line::before { counter-increment: pbcssg-line; content: counter(pbcssg-line); display: inline-block; width: 2.5em; margin-right: 1em; text-align: right; color: var(--muted); user-select: none; -webkit-user-select: none; }
.pbcssg-code-comment { margin: 0; padding: 0.4rem 0.75rem; background: var(--card-bg); border-top: 1px solid var(--border); color: var(--muted); font-size: 0.9rem; }

/* Disclosure / FAQ block (§6.12) — native <details>/<summary>, visible-but-collapsed,
   no JavaScript. The body is in the page source and indexable. */
.pbcssg-details { margin-block: 1.5rem; border: 1px solid var(--border); border-radius: 8px; padding: 0 0.9rem; }
.pbcssg-details-summary { cursor: pointer; font-weight: 600; padding: 0.6rem 0; }
.pbcssg-details-summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 4px; }
.pbcssg-details[open] .pbcssg-details-summary { border-bottom: 1px solid var(--border); }
.pbcssg-details-body { padding: 0.6rem 0; }
.pbcssg-details-body > :last-child { margin-bottom: 0; }

/* Post meta — reading time under the title (§6.13). margin-block (not the margin
   shorthand) so it stays centred at the reading measure like other main > * children. */
.pbcssg-post-meta { margin-block: -0.5rem 1.25rem; color: var(--muted); font-size: 0.9rem; }

/* Related posts (§6.13) — internal-links list, no external requests */
.pbcssg-related { margin-block: 1.5rem; border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 1rem; background: var(--card-bg); }
.pbcssg-related-title { margin: 0 0 0.4rem; font-weight: 600; }
.pbcssg-related-list { list-style: none; margin: 0; padding: 0; }
.pbcssg-related-list li { margin: 0.2rem 0; }
.pbcssg-related-list a { text-decoration: none; }
.pbcssg-related-list a:hover, .pbcssg-related-list a:focus-visible { text-decoration: underline; }
.pbcssg-related-date { color: var(--muted); font-size: 0.85rem; margin-inline-start: 0.4rem; }

/* Heading anchors + table of contents (§6.12). Anchors are build-added permalinks on
   h2–h4, revealed on hover/focus so they don't clutter the reading view. */
.pbcssg-anchor { margin-inline-start: 0.4rem; color: var(--muted); text-decoration: none; opacity: 0; transition: opacity 0.12s; }
:is(h2, h3, h4):hover .pbcssg-anchor, .pbcssg-anchor:focus-visible { opacity: 1; }
.pbcssg-anchor:hover { color: var(--accent); }
.pbcssg-toc { margin-block: 1.5rem; border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 1rem; background: var(--card-bg); }
.pbcssg-toc-title { margin: 0 0 0.4rem; font-weight: 600; }
.pbcssg-toc-list, .pbcssg-toc ol { list-style: none; margin: 0; padding: 0; }
.pbcssg-toc-list ol { padding-inline-start: 1.1rem; margin-top: 0.2rem; }
.pbcssg-toc li { margin: 0.15rem 0; }
.pbcssg-toc a { text-decoration: none; }
.pbcssg-toc a:hover, .pbcssg-toc a:focus-visible { text-decoration: underline; }

/* Deferred-reveal (hidden) block — obfuscation, not security (§6.9) */
.pbcssg-reveal { margin-block: 1.5rem; }
.pbcssg-reveal-btn { font: inherit; cursor: pointer; color: var(--accent); background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; padding: 0.4rem 0.9rem; }
.pbcssg-reveal-btn:hover { border-color: var(--accent); }
.pbcssg-reveal-out:not(:empty) { display: inline-block; margin-inline-start: 0.5rem; }
.pbcssg-reveal-noscript { color: var(--muted); font-style: italic; }
.pbcssg-reveal-gate:not([hidden]) { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; margin-top: 0.5rem; }
.pbcssg-reveal-code-label { font-weight: 600; }
.pbcssg-reveal-code { font: inherit; padding: 0.35rem 0.5rem; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); color: var(--fg); }
.pbcssg-reveal-error:not(:empty) { color: #c05621; flex-basis: 100%; }
/* Editor-preview only: a members-only (Mode C) reveal shown to the operator with a group label. */
.pbcssg-reveal-preview { margin-block: 1.5rem; border: 1px dashed var(--border); border-radius: 8px; padding: 0.5rem 0.9rem; }
.pbcssg-reveal-preview-label { margin: 0 0 0.4rem; font-size: 0.85rem; color: var(--muted); }
.pbcssg-reveal-preview-body > :last-child { margin-bottom: 0; }

/* Group-gated content — keyring-unlocked blocks (§6.10). A non-holder sees nothing
   (the block's payload is absent from the DOM); a holder gets the injected content. */
.pbcssg-gate { margin-block: 1.5rem; }
.pbcssg-gate-out:empty { display: none; }
.pbcssg-gate-noscript { color: var(--muted); font-style: italic; }
/* Editor-preview only: gated content shown to the operator with a group label. */
.pbcssg-gate-preview { margin-block: 1.5rem; border: 1px dashed var(--border); border-radius: 8px; padding: 0.5rem 0.9rem; }
.pbcssg-gate-preview-label { margin: 0 0 0.4rem; font-size: 0.85rem; color: var(--muted); }
/* Lock / forget-my-keys control (shared-machine hygiene); shown only when keys are held. */
.pbcssg-lock { font: inherit; cursor: pointer; color: var(--accent); background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; padding: 0.3rem 0.7rem; margin-inline-start: 0.5rem; }
.pbcssg-lock:hover { border-color: var(--accent); }

/* Citation / quotation block */
.pbcssg-citation { margin-block: 1.5rem; }
.pbcssg-citation blockquote { margin: 0; padding: 0.25rem 0 0.25rem 1rem; border-left: 3px solid var(--border); font-style: italic; }
.pbcssg-citation blockquote > :last-child { margin-bottom: 0; }
.pbcssg-citation figcaption { color: var(--muted); font-size: 0.9rem; margin-top: 0.4rem; }
.pbcssg-citation figcaption cite { font-style: normal; }

/* Share block (§6.15) — first-party controls only, no third-party buttons/pixels. */
.pbcssg-share { margin-block: 1.5rem; border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 1rem; background: var(--card-bg); }
.pbcssg-share-title { margin: 0 0 0.5rem; font-weight: 600; }
.pbcssg-share-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; }
.pbcssg-share-btn { font: inherit; font-size: 0.9rem; cursor: pointer; color: var(--fg); background: var(--bg); border: 1px solid var(--border); border-radius: 6px; padding: 0.35rem 0.7rem; text-decoration: none; display: inline-block; }
.pbcssg-share-btn:hover { border-color: var(--accent); }
.pbcssg-share-mastodon { display: flex; gap: 0.35rem; }
.pbcssg-share-mastodon input { font: inherit; font-size: 0.9rem; padding: 0.35rem 0.5rem; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); color: var(--fg); min-width: 9rem; }

/* Gallery block (§6.14) — responsive image grid + CSS-only :target lightbox, no JS. */
.pbcssg-gallery { display: grid; gap: 0.5rem; margin-block: 1.5rem; grid-template-columns: repeat(3, 1fr); }
.pbcssg-gallery--cols-2 { grid-template-columns: repeat(2, 1fr); }
.pbcssg-gallery--cols-3 { grid-template-columns: repeat(3, 1fr); }
.pbcssg-gallery--cols-4 { grid-template-columns: repeat(4, 1fr); }
@media (max-width: 40rem) { .pbcssg-gallery, .pbcssg-gallery--cols-3, .pbcssg-gallery--cols-4 { grid-template-columns: repeat(2, 1fr); } }
.pbcssg-gallery-item { margin: 0; }
.pbcssg-gallery-thumb { display: block; }
.pbcssg-gallery-thumb img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; border-radius: 6px; display: block; }
.pbcssg-gallery-thumb:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.pbcssg-gallery-caption { color: var(--muted); font-size: 0.85rem; margin-top: 0.25rem; }
.pbcssg-lightbox { display: none; position: fixed; inset: 0; z-index: 50; align-items: center; justify-content: center; padding: 2rem; background: rgba(0, 0, 0, 0.85); }
.pbcssg-lightbox:target { display: flex; }
.pbcssg-lightbox-backdrop { position: absolute; inset: 0; cursor: zoom-out; }
.pbcssg-lightbox-img { position: relative; max-width: 95vw; max-height: 90vh; border-radius: 6px; }
.pbcssg-lightbox-x { position: absolute; top: 0.75rem; right: 1.25rem; color: #fff; font-size: 2.25rem; line-height: 1; text-decoration: none; }
.pbcssg-lightbox-x:focus-visible { outline: 2px solid #fff; outline-offset: 4px; }

/* Image / figure block */
.pbcssg-figure { margin-block: 1.5rem; }
.pbcssg-figure img { max-width: 100%; height: auto; display: block; border-radius: 8px; }
.pbcssg-figure figcaption { color: var(--muted); font-size: 0.9rem; margin-top: 0.4rem; }

/* Image layout options (§4.2): preset max-widths + optional float with text wrap.
   These override the default breakout (main > figure.pbcssg-figure) — same
   specificity, later in source. A block-sized image stays centred via main > *'s
   auto margins; a floated image opts out of centring and is offset so its outer
   edge lines up with the reading column, so body text wraps cleanly beside it
   (the column is centred inside the wider main). Floats collapse to full-width
   blocks on narrow screens. */
/* A definite width (capped to the container) so a float — which is shrink-to-fit —
   takes the intended size regardless of the image's intrinsic dimensions. */
main > figure.pbcssg-figure--sm { width: 12rem; max-width: 100%; }
main > figure.pbcssg-figure--md { width: 20rem; max-width: 100%; }
main > figure.pbcssg-figure--lg { width: 32rem; max-width: 100%; }
main > figure.pbcssg-figure--left, main > figure.pbcssg-figure--right { width: 18rem; max-width: 100%; margin-block: 0.35rem 0.75rem; }
main > figure.pbcssg-figure--left {
  float: left;
  margin-left: max(0px, calc((100% - var(--measure)) / 2));
  margin-right: 1.5rem;
}
main > figure.pbcssg-figure--right {
  float: right;
  margin-right: max(0px, calc((100% - var(--measure)) / 2));
  margin-left: 1.5rem;
}
/* Explicit sizes win over the float default (declared after, same specificity). */
main > figure.pbcssg-figure--left.pbcssg-figure--sm, main > figure.pbcssg-figure--right.pbcssg-figure--sm { width: 12rem; }
main > figure.pbcssg-figure--left.pbcssg-figure--md, main > figure.pbcssg-figure--right.pbcssg-figure--md { width: 20rem; }
main > figure.pbcssg-figure--left.pbcssg-figure--lg, main > figure.pbcssg-figure--right.pbcssg-figure--lg { width: 32rem; }
@media (max-width: 34rem) {
  main > figure.pbcssg-figure--left, main > figure.pbcssg-figure--right {
    float: none; max-width: 100%; margin-inline: auto; margin-block: 1.5rem;
  }
}

/* Tags — chips on pages, and the /tags/ browse pages */
.pbcssg-tags { margin-block: 1.75rem 0; display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
.pbcssg-tags-label { color: var(--muted); font-size: 0.9rem; }
.pbcssg-tag { display: inline-block; padding: 0.15rem 0.65rem; border: 1px solid var(--border); border-radius: 999px; text-decoration: none; font-size: 0.85rem; }
.pbcssg-tag:hover, .pbcssg-tag:focus-visible { background: var(--accent); color: var(--bg); border-color: var(--accent); }
.pbcssg-tag-list, .pbcssg-page-list, .pbcssg-feed-list { line-height: 1.9; }
.pbcssg-tag-count { color: var(--muted); font-size: 0.85rem; }
.pbcssg-feed-title { font-weight: 600; }

/* External-references listing — shown before the footer when a page references
   external domains (§5.7). Mirrors the editor's per-domain privacy annotations:
   each domain's grade letter + name + the classifier's reasons. The grade meaning
   is carried by the letter and name; the chip colour is decorative reinforcement. */
.pbcssg-extref { margin-block: 2.5rem 0; padding-top: 1.25rem; border-top: 1px solid var(--border); }
.pbcssg-extref-heading { font-size: 1rem; margin: 0 0 0.7rem; }
.pbcssg-extref-list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.55rem; font-size: 0.92rem; }
.pbcssg-extref-item code { background: transparent; padding: 0; }
.pbcssg-extref-name { color: var(--muted); }
.pbcssg-extref-reason { color: var(--muted); }
.pbcssg-extref-more { margin: 0.6rem 0 0; font-size: 0.9rem; }

/* Classification report page (/classification, §5.7) */
.pbcssg-report-intro, .pbcssg-report-disclaimer { color: var(--muted); }
.pbcssg-report-disclaimer { border-left: 3px solid var(--border); padding-left: 0.85rem; }
/* margin-block (not the margin:X 0 shorthand, which would zero margin-inline) so
   main > * margin-inline:auto still centres these lists with the rest of the
   report; the report list re-asserts auto to override the extref list's margin:0. */
.pbcssg-legend { list-style: none; padding: 0; margin-block: 0.5rem 1rem; display: flex; flex-direction: column; gap: 0.4rem; }
.pbcssg-legend li { display: flex; align-items: center; gap: 0.5rem; }
.pbcssg-report-summary { margin-top: 0.5rem; }
.pbcssg-report-list { margin: 0.75rem auto 0; }
.pbcssg-grade {
  display: inline-block; min-width: 1.5em; text-align: center; font-weight: 700;
  border-radius: 5px; padding: 0.05em 0.4em; color: #fff;
}
.pbcssg-grade-a { background: #1a7f37; } .pbcssg-grade-b { background: #4a8f1a; } .pbcssg-grade-c { background: #9a7d0a; }
.pbcssg-grade-d { background: #c05621; } .pbcssg-grade-e { background: #b23b3b; } .pbcssg-grade-f { background: #b00020; }
.pbcssg-grade-unknown { background: #555; }

/* YouTube consent card — Stage 1 (§5.8) */
.pbcssg-consent-card {
  border: 1px solid var(--border); border-radius: 12px;
  background: var(--card-bg); padding: 1.1rem 1.25rem; margin-block: 1.5rem;
}
.pbcssg-consent-label { font-weight: 600; margin-top: 0; }
.pbcssg-consent-poster { border-radius: 8px; display: block; margin-bottom: 0.75rem; }
.pbcssg-consent-open {
  display: inline-block; margin-top: 0.35rem; padding: 0.5rem 0.9rem;
  background: var(--accent); color: var(--bg); border-radius: 8px; text-decoration: none; font-weight: 600;
}

/* YouTube / generic embed facade — Stage 2 (§5.8) */
.pbcssg-back { margin: 0 0 0.5rem; }
.pbcssg-back a { color: var(--muted); text-decoration: none; }
.pbcssg-back a:hover, .pbcssg-back a:focus-visible { color: var(--accent); text-decoration: underline; }
.pbcssg-video-intro { color: var(--muted); }
.pbcssg-youtube-facade, .pbcssg-embed-facade {
  position: relative; display: grid; place-items: center; gap: 0.75rem;
  aspect-ratio: 16 / 9; width: 100%;
  background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px;
  padding: 1rem; text-align: center;
}
.pbcssg-youtube-facade iframe, .pbcssg-embed-facade iframe { position: absolute; inset: 0; width: 100%; height: 100%; border: 0; border-radius: 12px; }
.pbcssg-facade-poster { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; border-radius: 12px; }
.pbcssg-facade-play {
  position: relative; font-size: 1.05rem; font-weight: 600; cursor: pointer;
  padding: 0.7rem 1.15rem; color: var(--bg); background: var(--accent);
  border: 0; border-radius: 999px;
}
.pbcssg-facade-note { position: relative; margin: 0; max-width: 32rem; font-size: 0.85rem; color: var(--muted); }
.pbcssg-video-links ul { padding-inline-start: 1.2rem; }

/* Route-based page-index block */
.pbcssg-index { margin-block: 1.5rem; }
.pbcssg-index-title { margin-block-end: 0.5rem; }
.pbcssg-index-list { padding-inline-start: 1.2rem; }
.pbcssg-index-detailed { list-style: none; padding-inline-start: 0; }
.pbcssg-index-detailed > li { margin-block: 0.9rem; }
.pbcssg-index-date { color: var(--muted); font-size: 0.85rem; margin-inline-start: 0.4rem; }
.pbcssg-index-summary { margin: 0.2rem 0 0; color: var(--muted); }
.pbcssg-index-more, .pbcssg-index-empty { color: var(--muted); font-size: 0.9rem; }

/* Self-hosted audio/video figure block */
.pbcssg-media { margin-block: 1.5rem; }
.pbcssg-media-el { display: block; width: 100%; border-radius: 12px; background: #000; }
.pbcssg-media-audio .pbcssg-media-el { background: transparent; }
.pbcssg-media figcaption { color: var(--muted); font-size: 0.9rem; margin-top: 0.4rem; }
`

// FontOption is a selectable body-font stack. Every option is composed of
// system fonts only — nothing is downloaded — so choosing one adds zero
// third-party requests, keeping the self-hosted posture (SPEC §8).
type FontOption struct {
	ID    string // stored setting value
	Label string // shown in the Settings dropdown
	Stack string // the CSS font-family value applied to --font-sans
}

// Fonts is the fixed allowlist of body fonts. The operator picks an ID; the
// stack is never operator-supplied text, so no value can inject into the CSS.
// "system" is the built-in default already in CSS (no override emitted).
var Fonts = []FontOption{
	{"system", "System (default)", `system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif`},
	{"humanist", "Humanist sans", `Seravek, "Gill Sans Nova", Ubuntu, Calibri, "DejaVu Sans", source-sans-pro, sans-serif`},
	{"geometric", "Geometric sans", `Avenir, Montserrat, Corbel, "URW Gothic", source-sans-pro, sans-serif`},
	{"grotesque", "Neo-grotesque sans", `"Helvetica Neue", Helvetica, "Arial Nova", "Nimbus Sans", Arial, sans-serif`},
	{"oldstyle", "Old-style serif", `"Iowan Old Style", "Palatino Linotype", "URW Palladio L", P052, Georgia, serif`},
	{"transitional", "Transitional serif", `Charter, "Bitstream Charter", "Sitka Text", Cambria, Georgia, serif`},
}

// fontByID returns the option for id and whether it was a known ID (unknown
// falls back to the system default).
func fontByID(id string) (FontOption, bool) {
	for _, f := range Fonts {
		if f.ID == id {
			return f, true
		}
	}
	return Fonts[0], false
}

// ValidFont reports whether id names a known font option — used to normalize the
// setting on save.
func ValidFont(id string) bool { _, ok := fontByID(id); return ok }

// FontCSS returns a :root override that sets --font-sans for the chosen body
// font, or "" when the choice is the built-in system default (already in CSS).
// The value comes only from the Fonts allowlist, never operator free-text, so it
// cannot inject CSS. It is appended after theme.CSS by the build and the editor
// preview so both show the same font.
func FontCSS(id string) string {
	f, ok := fontByID(id)
	if !ok || f.ID == "system" {
		return ""
	}
	return "\n/* body font (settings §6.4) */\n:root { --font-sans: " + f.Stack + "; }\n"
}
