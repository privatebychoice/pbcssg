// Package render turns a page's stored content (SPEC §4.2: a markdown body plus
// an optional repeatable block list — decision #1) into a full HTML page.
//
// Markdown is rendered with goldmark in *safe* mode — raw HTML is not emitted and
// dangerous URLs (javascript:, …) are neutralized, so the output is sanitized by
// construction (SPEC §6) — with GFM (tables, strikethrough, task lists, autolinks) +
// footnotes and auto heading IDs enabled (SPEC §6.12). The extensions add markdown
// syntax, not raw-HTML passthrough, so safe mode is preserved. External links/images
// authored in markdown — including a GFM-autolinked bare URL — still appear in the
// output and are handled downstream by the hygiene and link-scan passes. The rendered content is wrapped in a minimal, semantic,
// accessible base layout that carries the build number in its footer (SPEC §6).
//
// The youtube block (SPEC §5.8) is consent-gated: on the host page it renders a
// self-hosted card that contacts no third party and links to a separate
// /external/youtube/<name> page (produced by ExternalYouTube), where a
// click-to-load facade loads youtube-nocookie only on an explicit second action.
// The generic embed block applies the same two-stage consent pattern to any
// provider: its card links to /external/<provider>/<name> (produced by
// ExternalEmbed), whose facade frames the authored embed URL only on that second
// action. The embed URL's host must be allowlisted in Settings so it is permitted
// by the built site's CSP frame-src.
package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"go.privatebychoice.com/pbcssg/internal/reveal"
)

// mediaRefRE matches a same-site, content-addressed media URL (/media/<sha256>.<ext>)
// as authored into content or rendered into an element attribute (an <img>/<video>/
// <audio> src, a poster, etc.). It is the single definition of a local media
// reference, shared by the build (to emit exactly the referenced assets) and the
// editor (to flag broken references).
var mediaRefRE = regexp.MustCompile(`/media/([0-9a-f]{64})\.([a-z0-9]+)`)

// MediaRef is one same-site, content-addressed media reference found in rendered
// HTML: the content address (SHA-256) plus the URL/file extension.
type MediaRef struct {
	SHA string
	Ext string
}

// Path is the same-site URL an author references (/media/<sha>.<ext>).
func (m MediaRef) Path() string { return "/media/" + m.SHA + "." + m.Ext }

// Rel is the output-relative path the build writes the asset to (media/<sha>.<ext>).
func (m MediaRef) Rel() string { return "media/" + m.SHA + "." + m.Ext }

// MediaRefs extracts every same-site, content-addressed media reference from
// rendered HTML, de-duplicated and in first-seen order. Both the build and the
// editor use it, so there is one source of truth for what a local media
// reference is (SPEC §6.1).
func MediaRefs(html []byte) []MediaRef {
	matches := mediaRefRE.FindAllSubmatch(html, -1)
	out := make([]MediaRef, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		ref := MediaRef{SHA: string(m[1]), Ext: string(m[2])}
		key := ref.Rel()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out
}

// Content is a page's stored content (the opaque content_json in the store).
type Content struct {
	Body     string   `json:"body"`               // markdown lead/body
	Blocks   []Block  `json:"blocks,omitempty"`   // the one repeatable block list (v1)
	Summary  string   `json:"summary,omitempty"`  // author excerpt/description (SEO + search)
	Tags     []string `json:"tags,omitempty"`     // page tags
	Keywords []string `json:"keywords,omitempty"` // extra search keywords (decision #11)

	// IsIndex allows index blocks to render on this page (the gate). ListExclude
	// hides this page from index-block listings. The two are independent: an index
	// page still appears in a parent listing unless it also sets ListExclude.
	IsIndex     bool `json:"isIndex,omitempty"`
	ListExclude bool `json:"listExclude,omitempty"`
	// NoIndex emits <meta name="robots" content="noindex"> and drops the page from
	// sitemap.xml (SPEC §6.3). Default false — pages are indexable unless opted out.
	NoIndex bool `json:"noIndex,omitempty"`
	// Unlisted marks the page as hidden (SPEC §6.16): it is built and served, but
	// removed from every generated listing/manifest — sitemap, search, page-index,
	// related-posts, tags pages, feeds, and the published privacy manifest — and it
	// implies NoIndex. Paired with an unguessable path + gated blocks (§6.10) it is
	// a capability-URL members page. The in-page External References section stays.
	Unlisted bool `json:"unlisted,omitempty"`
	// IsPost marks the page as a dated post/article (SPEC §6.13). It gates the
	// post-only features — reading time and the related-posts block — and does not
	// change routing. Ordinary pages leave it false.
	IsPost bool `json:"isPost,omitempty"`
	// OGImage is an optional per-page social-preview image (SPEC §6.3): a same-site
	// /media/<sha>.<ext> path. The build resolves it to an absolute URL for the og:image
	// tag; a page without one falls back to the Settings site default.
	OGImage string `json:"ogImage,omitempty"`
}

// Parse decodes a page's content_json.
func Parse(contentJSON string) (Content, error) {
	var c Content
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		return Content{}, fmt.Errorf("render: parse content: %w", err)
	}
	return c, nil
}

// Block is one entry in the block list.
type Block struct {
	Type     string    `json:"type"` // "" or "markdown" | "youtube" | "embed" | "image" | "media" | "callout" | "citation" | "code" | "details" | "toc" | "related" | "gallery" | "share" | "index" | "reveal" | "comments"
	Markdown string    `json:"markdown,omitempty"`
	YouTube  *YouTube  `json:"youtube,omitempty"`
	Embed    *Embed    `json:"embed,omitempty"`
	Image    *Image    `json:"image,omitempty"`
	Media    *Media    `json:"media,omitempty"`
	Callout  *Callout  `json:"callout,omitempty"`
	Citation *Citation `json:"citation,omitempty"`
	Code     *Code     `json:"code,omitempty"`
	Details  *Details  `json:"details,omitempty"`
	TOC      *TOC      `json:"toc,omitempty"`
	Related  *Related  `json:"related,omitempty"`
	Gallery  *Gallery  `json:"gallery,omitempty"`
	Share    *Share    `json:"share,omitempty"`
	Index    *Index    `json:"index,omitempty"`
	Reveal   *Reveal   `json:"reveal,omitempty"`
	// Groups lists the key-group aliases authorized to unlock this block (SPEC §6.10,
	// group-gated content). Empty ⇒ public (not gated). Aliases are normalized at the
	// editor boundary (store.NormalizeAlias), so build-time matching is a plain
	// compare. Only the gateable subset honors it (see IsGateable); it is ignored on
	// other block types. Any-of (OR): holding any listed group's key unlocks the block.
	Groups []string `json:"groups,omitempty"`
	// Gate is a build-internal payload, never authored or persisted: the build's
	// PrepareGated pass renders a gated block to hardened inner HTML and stows it here
	// (with the block's Type switched to "gate"), so the render pass can envelope-
	// encrypt it. It is excluded from JSON so it never round-trips through the editor.
	Gate *Gate `json:"-"`
}

// Gate is the build-internal, pre-rendered form of a group-gated block (SPEC §6.10).
// HTML is the block's goldmark-safe, hygiene-hardened inner HTML — the plaintext the
// render pass encrypts under a per-block DEK (it is never emitted in the clear).
// Groups are the authorized aliases (resolved to KEKs at render time); NoScript is
// the optional fallback shown to no-JS/non-holder visitors.
type Gate struct {
	HTML     string
	Groups   []string
	NoScript string
}

// IsGateable reports whether a block type may be group-gated (SPEC §6.10): the
// text-shaped subset plus (caveated) image/media/gallery/index. youtube/embed are
// excluded — their linked/external targets are public, so gating the on-page element
// hides nothing real. The reveal block is excluded here because it carries its own
// encryption; it instead honors group aliases as a native "Mode C" keyring unlock
// (renderReveal), so it is never double-encrypted by the gate pass.
//
// Caveats for the newer members: a gated code block's Copy button does not work (the
// content is injected client-side after the copy script has wired the page); a gated
// gallery/index still links publicly-fetchable /media bytes / public pages — gating
// hides placement, not the underlying targets.
func IsGateable(blockType string) bool {
	switch blockType {
	case "", "markdown", "callout", "citation", "image", "media", "code", "details", "gallery", "index":
		return true
	default:
		return false
	}
}

// Reveal is the deferred-reveal ("hidden") block (SPEC §6.9): a short piece of
// first-party content that is encrypted at build time and absent from the served
// HTML until the visitor clicks to show it — keeping it out of view-source,
// find-in-page, search crawlers, and naive scrapers. This is obfuscation, not
// security: in the obfuscation mode (Mode A) the decoding key ships in the page.
type Reveal struct {
	Content  string `json:"content"`            // the plaintext to hide (never emitted in the clear)
	Label    string `json:"label"`              // visible reveal-button text (required)
	Kind     string `json:"kind,omitempty"`     // "text" (default) | "email" (mailto:) | "markdown" (rendered)
	NoScript string `json:"noscript,omitempty"` // fallback text shown when JavaScript is off
	// Code, when set, switches the block to Mode B (the code gate, §6.9): the AES key
	// is derived from this code via PBKDF2, so the visitor must enter it to decode.
	// It is a soft gate (only as strong as the code's entropy), never a login. The
	// code is used only at build time to encrypt; it is never emitted into the page.
	Code string `json:"code,omitempty"`
}

// RevealKind normalizes an authored reveal kind to the fixed allowlist, so the
// stored value and the rendered behaviour share one source of truth. "email"
// reveals as a mailto: link; "markdown" reveals rendered (goldmark-safe) HTML via
// innerHTML; anything else is plain "text".
func RevealKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "email":
		return "email"
	case "markdown":
		return "markdown"
	default:
		return "text"
	}
}

// RevealMarkdownHTML renders a reveal block's markdown body to goldmark-safe HTML
// (no raw HTML, dangerous URLs neutralized), for the "markdown" kind. The build
// hardens the result (hygiene) and classifies its links before encrypting it; the
// client injects the decrypted HTML with innerHTML (SPEC §6.9).
func RevealMarkdownHTML(markdown string) (string, error) {
	return toHTML(markdown)
}

// DefaultRevealNoScript is the fallback shown inside <noscript> when the author
// supplies none: the payload is deliberately absent from the HTML, so a no-JS
// visitor cannot read it (SPEC §6.9).
const DefaultRevealNoScript = "This content is hidden and requires JavaScript to reveal."

// DefaultGateNoScript is the fallback shown inside <noscript> for a group-gated
// block (SPEC §6.10): the payload is absent from the HTML and only a keyring holder
// (which needs JavaScript) can decrypt it.
const DefaultGateNoScript = "This content is available to members and requires JavaScript to unlock."

// MaxRevealCode caps the length (in runes) of a reveal block's optional Mode B
// gate code. A gate code is a short token or passphrase, not a document; the cap
// is generous enough for a strong multi-word passphrase. The editor mirrors it as
// a maxlength on the code field; the creator enforces it server-side on save.
const MaxRevealCode = 128

// Index is a route-based page-list block: placed in a page's block stack, it
// renders a list of published pages under a base route (SPEC §6.7). It renders
// only when the host page is marked as an index page.
type Index struct {
	Base  string `json:"base,omitempty"`  // base route; defaults to the host page's path
	Depth int    `json:"depth,omitempty"` // 1 = direct children; 0 = all descendants
	Title string `json:"title,omitempty"` // optional heading above the list
	Sort  string `json:"sort,omitempty"`  // date-desc (default) | date-asc | path | title
	Style string `json:"style,omitempty"` // titles (default) | detailed (title + date + summary)
	Limit int    `json:"limit,omitempty"` // max items shown; 0 = default cap
}

// PageRef is one published page in the site-wide index the build/editor passes to
// the renderer so index blocks can list matching pages.
type PageRef struct {
	Path    string
	Title   string
	Summary string
	Date    string // display date (YYYY-MM-DD)
	Time    time.Time
	IsIndex bool
	Exclude bool
	// IsPost, Tags, and NoIndex support the related-posts block (SPEC §6.13): it lists
	// other posts (IsPost) sharing tags, dropping noindex/excluded ones.
	IsPost  bool
	Tags    []string
	NoIndex bool
}

// Embed is the generic consent-gated embed fieldblock: the youtube two-stage
// pattern (SPEC §5.8) applied to any provider. All fields are manually authored;
// pbcssg never fetches from the provider at build. The host page shows a
// self-hosted consent card linking to /external/<Provider>/<Name>, whose facade
// frames EmbedURL only after an explicit second click. EmbedURL's host must be
// allowlisted in Settings (build-enforced + served-site CSP frame-src).
type Embed struct {
	Provider         string   `json:"provider"` // slug: /external/<provider>/<name> + shown label
	Name             string   `json:"name"`     // slug for the external page
	Title            string   `json:"title"`
	EmbedURL         string   `json:"embedUrl"` // https iframe src the visitor consents to load
	Transcript       string   `json:"transcript,omitempty"`
	DescriptionLinks []string `json:"descriptionLinks,omitempty"`
	Poster           string   `json:"poster,omitempty"`   // self-hosted poster path
	Keywords         []string `json:"keywords,omitempty"` // contributed to the page's search index (§6.2)
}

// Citation is a quotation block: a markdown quote with an optional source and a
// link to it, rendered as <figure><blockquote> + <figcaption>.
type Citation struct {
	Quote  string `json:"quote"`
	Source string `json:"source,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Image is a figure block: a self-hosted (or external) image with alt text and
// an optional caption, rendered as a semantic <figure>.
type Image struct {
	Src     string `json:"src"`               // e.g. /media/<sha>.<ext>
	Alt     string `json:"alt"`               // accessibility text
	Caption string `json:"caption,omitempty"` // optional visible caption
	// Align floats the figure so text wraps beside it: "left" or "right"; empty is
	// a full-width block (the default). MaxWidth caps the figure to a preset:
	// "small" | "medium" | "large"; empty keeps the default (full breakout for a
	// block image, a sensible default for a float). Both are from a fixed allowlist
	// (rendered as CSS classes), so no operator value reaches CSS directly.
	Align    string `json:"align,omitempty"`
	MaxWidth string `json:"maxWidth,omitempty"`
}

// NormImageAlign / NormImageSize normalize authored image layout options to the
// fixed allowlist, so both the editor (stored JSON) and the renderer (CSS class)
// share one source of truth. Unknown values collapse to the default ("").
func NormImageAlign(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "left":
		return "left"
	case "right":
		return "right"
	default:
		return ""
	}
}

func NormImageSize(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "small":
		return "small"
	case "medium":
		return "medium"
	case "large":
		return "large"
	default:
		return ""
	}
}

// Media is a self-hosted audio/video figure block, rendered with a native
// <video>/<audio> element sourced from a same-site /media/<sha>.<ext> path (the
// file is metadata-stripped at ingest; nothing third-party is loaded).
type Media struct {
	Src     string `json:"src"`               // /media/<sha>.<ext>
	Kind    string `json:"kind"`              // video | audio
	Caption string `json:"caption,omitempty"` // optional visible caption
	Poster  string `json:"poster,omitempty"`  // optional poster image (video)
}

// Callout is an admonition block: a styled note/warning/tip/info box with an
// optional title and a markdown body.
type Callout struct {
	Variant  string `json:"variant"` // note | tip | warning | info
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown"`
}

// Share is a privacy-preserving share block (SPEC §6.15): first-party controls only,
// no third-party scripts/buttons/pixels. Copy-link and the Mastodon intent are wired
// by the self-hosted ShareJS (they read the live URL at click); Email is a plain
// mailto: link; RSS is an optional feed pointer. Nothing loads on page view, so the
// block introduces no external request and needs no classification entry.
type Share struct {
	Title    string `json:"title,omitempty"`
	CopyLink bool   `json:"copyLink,omitempty"`
	Email    bool   `json:"email,omitempty"`
	Mastodon bool   `json:"mastodon,omitempty"`
	RSS      string `json:"rss,omitempty"` // optional feed URL/path
}

// Gallery is a responsive image grid (SPEC §6.14) with a CSS-only :target lightbox
// (no JavaScript). Mode is "manual" (Items authored directly) or "tag" (Items resolved
// at build from every image carrying Tag, in Sort order). Items always holds the
// concrete images at render time; the build's PrepareGallery pass fills them in for
// tag mode. Columns (2–4) sets the grid width. All images are self-hosted /media
// paths, so the gallery adds no third-party request.
type Gallery struct {
	Mode    string        `json:"mode,omitempty"` // "manual" (default) | "tag"
	Tag     string        `json:"tag,omitempty"`  // media tag to gather (mode=tag)
	Sort    string        `json:"sort,omitempty"` // "newest" (default) | "oldest" | "name"
	Columns int           `json:"columns,omitempty"`
	Items   []GalleryItem `json:"items,omitempty"`
}

// GalleryItem is one image in a gallery: a self-hosted /media/<sha>.<ext> source with
// alt text and an optional caption.
type GalleryItem struct {
	Src     string `json:"src"`
	Alt     string `json:"alt,omitempty"`
	Caption string `json:"caption,omitempty"`
}

// Related is a related-posts block (SPEC §6.13). At build it lists other posts that
// share the most tags with the current page, ranked by shared-tag overlap then
// recency, capped at Count. Title is an optional heading. It renders internal links
// only (no external requests) and is omitted entirely when nothing matches.
type Related struct {
	Title string `json:"title,omitempty"`
	Count int    `json:"count,omitempty"`
}

// TOC is a table-of-contents block (SPEC §6.12). It renders a placeholder at author
// position that the build-time AnchorsAndTOC pass fills with a nested list of the
// page's h2–h4 headings. Depth caps how many levels deep the list goes (1 → h2 only,
// 3 → h2–h4, the default); Title is an optional heading above the list.
type TOC struct {
	Title string `json:"title,omitempty"`
	Depth int    `json:"depth,omitempty"`
}

// Details is a disclosure / FAQ block (SPEC §6.12): a native <details>/<summary>
// collapsible with a plain-text summary (the question/label) and a markdown body.
// It needs no JavaScript and is keyboard-accessible by default. Unlike the reveal
// block (§6.9), the body is present in the page source and indexable — it is
// visible-but-collapsed, not hidden — so it is deliberately not group-gateable.
// Open renders the block expanded by default.
type Details struct {
	Summary  string `json:"summary"`
	Markdown string `json:"markdown"`
	Open     bool   `json:"open,omitempty"`
}

// Code is a verbatim code-listing block (SPEC §6.12): a semantic <pre><code> with,
// deliberately, no syntax highlighting (so it stays dependency-free). The code text
// is HTML-escaped and never interpreted. Filename shows as a caption bar; Language is
// a display label only (it never drives a highlighter); Comment is an optional note
// under the listing; LineNumbers toggles a non-selectable line-number gutter (CSS
// counters, so the numbers never enter a copy/paste). A self-hosted copy button
// (CodeCopyJS) copies the raw code — no third party.
type Code struct {
	Text        string `json:"text"`
	Filename    string `json:"filename,omitempty"`
	Language    string `json:"language,omitempty"`
	Comment     string `json:"comment,omitempty"`
	LineNumbers bool   `json:"lineNumbers,omitempty"`
}

// YouTube is the consent-gated video fieldblock (SPEC §5.8). All fields are
// manually authored; pbcssg never fetches from YouTube at build.
type YouTube struct {
	VideoID          string   `json:"videoId"`
	Name             string   `json:"name"`  // slug for /external/youtube/<name>
	Title            string   `json:"title"` //
	Transcript       string   `json:"transcript,omitempty"`
	DescriptionLinks []string `json:"descriptionLinks,omitempty"`
	Poster           string   `json:"poster,omitempty"`   // self-hosted poster path
	Keywords         []string `json:"keywords,omitempty"` // contributed to the page's search index (§6.2)
}

// NavLink is one entry in the site's primary navigation bar (configured in
// Settings, rendered in the base layout header on every built page).
type NavLink struct {
	Label string
	Href  string
}

// FeedLink is an <head> feed auto-discovery link for a page that belongs to one
// or more configured feeds.
type FeedLink struct {
	Title string
	Href  string
	Type  string // application/rss+xml | application/atom+xml
}

// FeedInfo describes one syndication feed shown on the browsable /feeds/ index
// page (§6.5): its channel title plus the RSS and Atom URLs.
type FeedInfo struct {
	Title    string
	RSSHref  string
	AtomHref string
}

// Brand is the site's header brand (SPEC §6.4): a text wordmark, a self-hosted
// logo image, or both ("lockup"), optionally centred. Text is already resolved
// (an operator override, else the site name) by the caller, so the renderer just
// displays it. The brand, when shown, is a link to the home page.
type Brand struct {
	Mode        string // "none" | "text" | "logo" | "logotext"
	Align       string // "start" (default) | "center"
	Text        string // wordmark text (text / logotext)
	LogoSrc     string // /media/<sha>.<ext> (logo / logotext) — the light/default logo
	LogoSrcDark string // optional /media/<sha>.<ext> shown in dark mode; empty = use LogoSrc for both
	LogoAlt     string // logo alt text (used only in logo-only mode; see below)
	LogoHeight  string // "small" | "medium" | "large"
}

// ShowText / ShowLogo / Show gate what the header renders. Text needs non-empty
// Text; logo needs a source.
func (b Brand) ShowText() bool {
	return (b.Mode == "text" || b.Mode == "logotext") && strings.TrimSpace(b.Text) != ""
}
func (b Brand) ShowLogo() bool {
	return (b.Mode == "logo" || b.Mode == "logotext") && strings.TrimSpace(b.LogoSrc) != ""
}

// HasDarkLogo reports whether a separate dark-mode logo is configured. When true
// the header emits both logos and CSS shows the right one per theme (§6.4);
// otherwise the single LogoSrc is used in both themes.
func (b Brand) HasDarkLogo() bool {
	return b.ShowLogo() && strings.TrimSpace(b.LogoSrcDark) != ""
}
func (b Brand) Show() bool     { return b.ShowText() || b.ShowLogo() }
func (b Brand) Centered() bool { return b.Align == "center" }

// ImgAlt is the logo image's alt text as rendered. When a wordmark is also shown
// (lockup), the image is decorative (alt="") so the link's accessible name is not
// duplicated; logo-only mode uses the operator's alt so the link is still named.
func (b Brand) ImgAlt() string {
	if b.ShowText() {
		return ""
	}
	return b.LogoAlt
}

// Options carries the page metadata used by the base layout.
type Options struct {
	Title       string
	SiteName    string
	Description string
	BuildNumber string
	Brand       Brand      // header brand (wordmark / logo), SPEC §6.4
	Lang        string     // defaults to "en"
	Search      bool       // include the client-side search widget (§6.2)
	Nav         []NavLink  // primary navigation links (site-wide header)
	FooterNav   []NavLink  // footer navigation links (pipe-separated, centered)
	Year        int        // copyright year (stamped by the caller; 0 omits it)
	FeedLinks   []FeedLink // feed auto-discovery links for this page (§6.5)

	// Index-block context: the site-wide page list, the host page's own path (the
	// default base for an index block), and whether the host page is marked as an
	// index page (index blocks render only then). (§6.7)
	PageIndex   []PageRef
	HostPath    string
	IsIndexPage bool
	// ShowReadingTime renders "~N min read" for a post (Content.IsPost), placed after
	// the page's first heading by the AnchorsAndTOC pass (SPEC §6.13).
	ShowReadingTime bool

	// Fingerprinted asset hrefs, supplied by the build engine. Empty hrefs omit
	// the corresponding link/script.
	CSSHref        string // theme stylesheet
	SearchJSHref   string // client-side search script (used when Search)
	FacadeJSHref   string // youtube facade script (external youtube pages)
	ThemeJSHref    string // light/dark theme script (every page; blocking in <head>)
	RevealJSHref   string // deferred-reveal decode script (pages with a reveal block, §6.9)
	GateJSHref     string // group-gate keyring/decode script (pages with a gated block, §6.10)
	CodeCopyJSHref string // code-block copy-button script (pages with a code block, §6.12)
	ShareJSHref    string // share-block script (pages with a share block, §6.15)

	// Comments, when true, links the self-hosted comments widget (CommentsJSPath +
	// CommentsCSSPath) from this page — set by the build for a page that carries a
	// comments block (§7.3). The block's on-page placeholder is always rendered; this
	// flag only controls the widget's script/stylesheet links, so the editor preview
	// (which cannot serve the live /_pbc endpoints) leaves it false and shows the
	// placeholder alone.
	Comments bool

	// SplashAlias, when set, marks this page as a key group's splash/deposit page
	// (SPEC §6.10): the gate script reads the KEK from the URL fragment and stores it
	// in the keyring under this alias. The page is otherwise a normal authored page.
	SplashAlias string

	// RevealKey is the page's stored reveal-block key (SPEC §6.9). The build sets it
	// so each reveal block on the page encrypts deterministically under a per-page
	// key; when empty (editor preview / scan) render falls back to a fixed key. The
	// same key seeds each gated block's per-block DEK (SPEC §6.10) — server-only, it
	// is never shipped for a gated block (unlike reveal Mode A).
	RevealKey []byte

	// GateKEKs maps key-group alias → KEK for group-gated content (SPEC §6.10). The
	// build supplies the whole site's groups so a gated block can be wrapped for each
	// alias it authorizes. Empty/nil in editor preview, where GatePreview is set
	// instead so the block renders visibly (for the operator) rather than encrypting.
	GateKEKs map[string][]byte
	// GatePreview renders gated blocks visibly with a "gated" label instead of
	// encrypting them — used only by the editor preview (the operator's own view),
	// never by the build. When false (the build), a gated block whose groups resolve
	// to no known KEK is a hard error, so plaintext is never published by accident.
	GatePreview bool

	// Back link for the external youtube page: a same-origin path and a label for
	// the page that referenced the video, so the visitor can return.
	BackHref  string
	BackLabel string

	// SEO metadata. CanonicalURL → the canonical link + og:url (an absolute URL
	// supplied by the build). OpenGraph toggles the og: tags. Tags are the page's
	// tags, rendered as chips linking to their tag pages (keywords are search-only
	// and intentionally not emitted as meta).
	CanonicalURL string
	OpenGraph    bool
	OGImage      string // absolute social-preview image URL (§6.3); build-resolved
	Tags         []string

	// NoIndex emits <meta name="robots" content="noindex"> for this page. For
	// content pages Render sets it from the page's Content.NoIndex; other page
	// types (tags/feeds/classification/external) leave it false.
	NoIndex bool

	// Favicon declares which site favicon/app-icon links to emit in <head> (SPEC
	// §6.11). The build sets it from the operator's uploaded favicon set; a link is
	// emitted only for an asset that is actually present. Identical on every page.
	Favicon FaviconLinks
}

// FaviconLinks records which favicon assets exist so the layout emits only the
// matching <head> links (SPEC §6.11). Their paths are the fixed, browser-expected
// site-root names, so no per-asset URL is threaded here.
type FaviconLinks struct {
	SVG        bool   // /favicon.svg
	ICO        bool   // /favicon.ico
	AppleTouch bool   // /apple-touch-icon.png
	Manifest   bool   // /site.webmanifest
	ThemeColor string // <meta name="theme-color">, optional
}

// Any reports whether any favicon link would be emitted.
func (f FaviconLinks) Any() bool {
	return f.SVG || f.ICO || f.AppleTouch || f.Manifest || f.ThemeColor != ""
}

// Rendered is a rendered page plus any consent-gated blocks it contains, so the
// build can generate their /external/... pages.
type Rendered struct {
	HTML    []byte
	YouTube []YouTube
	Embed   []Embed
}

// FacadeJSPath is where the self-hosted click-to-load facade script is served.
const FacadeJSPath = "assets/pbcssg-youtube.js"

// SearchJSPath is where the self-hosted client-side search script is served.
const SearchJSPath = "assets/pbcssg-search.js"

// ThemeJSPath is where the self-hosted theme (light/dark) script is served.
const ThemeJSPath = "assets/pbcssg-theme.js"

// RevealJSPath is where the self-hosted deferred-reveal decode script is served.
const RevealJSPath = "assets/pbcssg-reveal.js"

// GateJSPath is where the self-hosted group-gate keyring/decode script is served.
const GateJSPath = "assets/pbcssg-gate.js"

// CodeCopyJSPath is where the self-hosted code-block copy-button script is served.
const CodeCopyJSPath = "assets/pbcssg-codecopy.js"

// ShareJSPath is where the self-hosted share-block script is served.
const ShareJSPath = "assets/pbcssg-share.js"

// CommentsJSPath / CommentsCSSPath are the fixed, same-origin URLs where the public
// dynamic layer (internal/publicapi, SPEC §7.3) serves the self-hosted comments
// widget. Unlike the bundled per-block scripts (fingerprinted and written into the
// static bundle), these are served live by the pbcssg server, so a page carrying a
// comments block links these fixed paths rather than a hashed bundle asset. They are
// same-origin, so the built site's strict CSP (default-src 'self') already allows the
// script, its fetches, and the stylesheet. A static-only deploy with no dynamic layer
// simply 404s them, and the block falls back to its no-JavaScript placeholder.
const (
	CommentsJSPath  = "/_pbc/assets/comments.js"
	CommentsCSSPath = "/_pbc/assets/comments.css"
)

// ShareJS wires the privacy-preserving share block (SPEC §6.15). Copy-link copies the
// live page URL to the clipboard; the Mastodon control reads the visitor's own
// instance and opens that instance's /share intent in a new tab — only on click, so no
// fixed third party is embedded and nothing loads on page view. Email is a plain
// mailto: link needing no script. No third party and no inline code, so a strict
// script-src 'self' CSP allows it; only pages with a share block link it.
const ShareJS = `(function () {
  function flash(btn, msg, restore) {
    if (btn._t) { clearTimeout(btn._t); }
    btn.textContent = msg;
    btn._t = setTimeout(function () { btn.textContent = restore; btn._t = null; }, 1500);
  }
  var copyBtns = document.querySelectorAll('[data-pbcssg-share-copy]');
  for (var i = 0; i < copyBtns.length; i++) {
    (function (btn) {
      var label = btn.textContent;
      btn.addEventListener('click', function () {
        var url = window.location.href;
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(url).then(
            function () { flash(btn, 'Copied', label); },
            function () { flash(btn, 'Press ⌘/Ctrl+C', label); }
          );
        } else {
          flash(btn, 'Press ⌘/Ctrl+C', label);
        }
      });
    })(copyBtns[i]);
  }
  var forms = document.querySelectorAll('[data-pbcssg-share-mastodon]');
  for (var j = 0; j < forms.length; j++) {
    (function (f) {
      f.addEventListener('submit', function (e) {
        e.preventDefault();
        var input = f.querySelector('input');
        var inst = ((input && input.value) || '').trim().replace(/^https?:\/\//, '').replace(/\/+$/, '');
        if (!inst) { if (input) { input.focus(); } return; }
        var text = document.title + ' ' + window.location.href;
        window.open('https://' + inst + '/share?text=' + encodeURIComponent(text), '_blank', 'noopener');
      });
    })(forms[j]);
  }
})();
`

// CodeCopyJS wires the copy button on every code block (SPEC §6.12). On click it
// copies the block's raw code — the <code> element's textContent, which excludes the
// non-selectable line-number pseudo-elements — to the clipboard, then flashes
// feedback on the button. It uses the async Clipboard API where available (needs a
// secure context) and falls back to a temporary textarea + execCommand('copy') so it
// still works over plain http. No inline code and no third party, so a strict
// script-src 'self' CSP allows it; only pages with a code block link it.
const CodeCopyJS = `(function () {
  function flash(btn, msg) {
    if (btn._t) { clearTimeout(btn._t); }
    btn.textContent = msg;
    btn._t = setTimeout(function () { btn.textContent = 'Copy'; btn._t = null; }, 1500);
  }
  function fallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'absolute';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    return ok;
  }
  function copy(text, btn) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(
        function () { flash(btn, 'Copied'); },
        function () { flash(btn, fallbackCopy(text) ? 'Copied' : 'Press ⌘/Ctrl+C'); }
      );
    } else {
      flash(btn, fallbackCopy(text) ? 'Copied' : 'Press ⌘/Ctrl+C');
    }
  }
  var btns = document.querySelectorAll('[data-pbcssg-copy]');
  for (var i = 0; i < btns.length; i++) {
    (function (btn) {
      btn.addEventListener('click', function () {
        var fig = btn.closest('.pbcssg-code');
        var code = fig ? fig.querySelector('.pbcssg-code-el') : null;
        copy(code ? code.textContent : '', btn);
      });
    })(btns[i]);
  }
})();
`

// RevealJS is the self-hosted decode script for the deferred-reveal block (SPEC
// §6.9). Mode A (obfuscation): on click it AES-GCM-decrypts with the per-block key
// that shipped in the page. Mode B (data-gated): the click reveals a code prompt,
// and on submit it derives the key from the typed code via PBKDF2 (over the shipped
// salt + iteration count) and decrypts — a wrong code fails GCM authentication and
// is announced as "Incorrect code." Either way the plaintext is injected — as a
// mailto: link for kind="email", otherwise as text — into an aria-live region so a
// screen reader announces it. No inline code and no third party, so a strict
// script-src 'self' CSP allows it; crypto.subtle needs a secure context (https or
// localhost), and if it is unavailable the <noscript> fallback stands in.
const RevealJS = `(function () {
  var subtle = (window.crypto && window.crypto.subtle) || null;
  function b64(s) {
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
    return out;
  }
  function attr(el, n) { return el.getAttribute(n) || ''; }
  function payload(el) { return subtle.decrypt({ name: 'AES-GCM', iv: b64(attr(el, 'data-iv')) }, this, b64(attr(el, 'data-ct'))); }
  function show(el, text) {
    var out = el.querySelector('.pbcssg-reveal-out');
    if (!out) { return; }
    var kind = el.getAttribute('data-kind');
    if (kind === 'markdown') {
      // The decrypted payload is goldmark-safe, build-hardened HTML (no scripts,
      // no raw HTML) authenticated by GCM, so innerHTML is safe here (SPEC §6.9).
      out.innerHTML = text;
      return;
    }
    out.textContent = '';
    if (kind === 'email') {
      var a = document.createElement('a');
      a.href = 'mailto:' + text;
      a.textContent = text;
      out.appendChild(a);
    } else {
      out.appendChild(document.createTextNode(text));
    }
  }
  function deliver(el, buf) { show(el, new TextDecoder().decode(buf)); }

  // Mode A: decrypt with the shipped per-block key.
  function revealPlain(el, btn) {
    var key = attr(el, 'data-key');
    if (!subtle || !attr(el, 'data-ct') || !key) {
      var out = el.querySelector('.pbcssg-reveal-out');
      if (out) { out.textContent = 'This content could not be revealed in this browser.'; }
      return;
    }
    subtle.importKey('raw', b64(key), { name: 'AES-GCM' }, false, ['decrypt'])
      .then(function (ck) { return payload.call(ck, el); })
      .then(function (buf) { deliver(el, buf); btn.setAttribute('aria-expanded', 'true'); btn.hidden = true; })
      .catch(function () {
        var out = el.querySelector('.pbcssg-reveal-out');
        if (out) { out.textContent = 'This content could not be revealed.'; }
      });
  }

  // Mode B: reveal the code prompt, then derive the key from the code via PBKDF2.
  function openGate(el, btn) {
    var gate = el.querySelector('.pbcssg-reveal-gate');
    if (!gate) { return; }
    gate.hidden = false;
    btn.setAttribute('aria-expanded', 'true');
    btn.hidden = true;
    var input = gate.querySelector('.pbcssg-reveal-code');
    var unlock = gate.querySelector('.pbcssg-reveal-unlock');
    if (unlock) { unlock.addEventListener('click', function () { tryCode(el, gate); }); }
    if (input) {
      input.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); tryCode(el, gate); } });
      input.focus();
    }
  }
  function tryCode(el, gate) {
    var input = gate.querySelector('.pbcssg-reveal-code');
    var err = gate.querySelector('.pbcssg-reveal-error');
    var code = input ? input.value : '';
    var salt = attr(el, 'data-salt');
    var iters = parseInt(attr(el, 'data-iters'), 10);
    if (!subtle || !salt || !iters) { if (err) { err.textContent = 'This content could not be revealed in this browser.'; } return; }
    if (!code) { if (err) { err.textContent = 'Enter the code to unlock.'; } return; }
    if (err) { err.textContent = ''; }
    subtle.importKey('raw', new TextEncoder().encode(code), 'PBKDF2', false, ['deriveKey'])
      .then(function (base) {
        return subtle.deriveKey({ name: 'PBKDF2', salt: b64(salt), iterations: iters, hash: 'SHA-256' },
          base, { name: 'AES-GCM', length: 256 }, false, ['decrypt']);
      })
      .then(function (ck) { return payload.call(ck, el); })
      .then(function (buf) { deliver(el, buf); gate.hidden = true; })
      .catch(function () {
        if (err) { err.textContent = 'Incorrect code.'; }
        if (input) { input.value = ''; input.focus(); }
      });
  }

  // Mode C: unlock with a keyring group key (members-only). Reads the first-party
  // keyring, trial-unwraps each shipped wrapped-DEK with each held KEK, and on a GCM
  // success decrypts the content — reusing the §6.10 keyring the visitor arrived with.
  function readRing() {
    try { var raw = localStorage.getItem('pbcssg-keyring'); if (!raw) { return {}; } var o = JSON.parse(raw); return (o && typeof o === 'object') ? o : {}; }
    catch (e) { return {}; }
  }
  function revealGroup(el, btn) {
    var out = el.querySelector('.pbcssg-reveal-out');
    var ring = readRing(), aliases = Object.keys(ring), keks = [];
    for (var a = 0; a < aliases.length; a++) { try { keks.push(b64(ring[aliases[a]])); } catch (e) {} }
    if (!subtle || !keks.length) { if (out) { out.textContent = 'This content is members-only — no access key found on this device.'; } return; }
    var wraps = el.querySelectorAll('.pbcssg-reveal-key');
    var ct = b64(attr(el, 'data-ct')), iv = b64(attr(el, 'data-iv'));
    var chain = Promise.resolve(null);
    keks.forEach(function (kek) {
      for (var i = 0; i < wraps.length; i++) {
        (function (w) {
          chain = chain.then(function (text) {
            if (text != null) { return text; }
            return subtle.importKey('raw', kek, { name: 'AES-GCM' }, false, ['decrypt'])
              .then(function (kk) { return subtle.decrypt({ name: 'AES-GCM', iv: b64(attr(w, 'data-wiv')) }, kk, b64(attr(w, 'data-w'))); })
              .then(function (dekBuf) {
                return subtle.importKey('raw', new Uint8Array(dekBuf), { name: 'AES-GCM' }, false, ['decrypt'])
                  .then(function (dk) { return subtle.decrypt({ name: 'AES-GCM', iv: iv }, dk, ct); })
                  .then(function (buf) { return new TextDecoder().decode(buf); });
              })
              .catch(function () { return null; });
          });
        })(wraps[i]);
      }
    });
    chain.then(function (text) {
      if (text != null) { show(el, text); btn.setAttribute('aria-expanded', 'true'); btn.hidden = true; }
      else if (out) { out.textContent = 'This content is members-only — your access key does not unlock it.'; }
    });
  }

  var blocks = document.querySelectorAll('[data-pbcssg-reveal]');
  for (var i = 0; i < blocks.length; i++) {
    (function (el) {
      var btn = el.querySelector('.pbcssg-reveal-btn');
      if (!btn) { return; }
      btn.addEventListener('click', function () {
        if (el.getAttribute('data-group')) { revealGroup(el, btn); }
        else if (el.getAttribute('data-gated')) { openGate(el, btn); }
        else { revealPlain(el, btn); }
      });
    })(blocks[i]);
  }
})();
`

// GateJS is the self-hosted keyring/decode script for group-gated content (SPEC
// §6.10). It does three things, all first-party and CSP-clean (Web Crypto is a JS
// API, not a network request; no inline code, no third party, no unsafe-eval):
//
//   - Deposit: on a group's splash page (marked data-pbcssg-splash="<alias>") it
//     reads the KEK carried in the URL fragment (#k=<base64url>), stores it in the
//     first-party keyring under that alias, and strips the fragment from the address
//     bar — so no server, proxy, log, or crawler ever sees the key.
//   - Unlock: for each gated block it trial-unwraps every shipped wrapped-DEK with
//     every keyring KEK; a GCM authentication success yields the DEK, which decrypts
//     the block and injects the (goldmark-safe, build-hardened, GCM-authenticated)
//     HTML. No keyring match ⇒ the block stays absent. Nothing about which/how-many
//     groups can unlock a block leaks, because the wrapped-DEKs are unlabeled.
//   - Lock: a data-pbcssg-lock control clears the whole keyring (essential on a
//     shared machine) and reloads.
//
// The keyring lives in first-party localStorage (per origin) and is never sent
// anywhere. A group KEK is a shared bearer key, not per-user auth (SPEC §6.10).
const GateJS = `(function () {
  var subtle = (window.crypto && window.crypto.subtle) || null;
  var RING = 'pbcssg-keyring';
  function b64(s) {
    s = String(s).replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) { s += '='; }
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
    return out;
  }
  function attr(el, n) { return el.getAttribute(n) || ''; }
  function readRing() {
    try { var raw = localStorage.getItem(RING); if (!raw) { return {}; } var o = JSON.parse(raw); return (o && typeof o === 'object') ? o : {}; }
    catch (e) { return {}; }
  }
  function writeRing(r) { try { localStorage.setItem(RING, JSON.stringify(r)); } catch (e) {} }

  // Splash key deposit: store a KEK carried in the fragment, then strip the fragment.
  function deposit() {
    var marker = document.querySelector('[data-pbcssg-splash]');
    if (!marker) { return; }
    var alias = attr(marker, 'data-pbcssg-splash');
    var m = (location.hash || '').match(/[#&]k=([^&]+)/);
    if (!alias || !m) { return; }
    var ring = readRing();
    ring[alias] = decodeURIComponent(m[1]);
    writeRing(ring);
    try { history.replaceState(null, '', location.pathname + location.search); }
    catch (e) { location.hash = ''; }
    marker.setAttribute('data-pbcssg-splash-done', '1');
  }

  function importKey(bytes) { return subtle.importKey('raw', bytes, { name: 'AES-GCM' }, false, ['decrypt']); }
  function gcm(ck, iv, ct) { return subtle.decrypt({ name: 'AES-GCM', iv: iv }, ck, ct); }

  // Trial-unwrap a block: for each keyring KEK, try each shipped wrapped-DEK; the
  // first GCM success yields the DEK, which decrypts the content. Resolves to the
  // decrypted HTML string, or null if no keyring key unlocks the block.
  function tryUnlock(el, keks) {
    var wraps = el.querySelectorAll('.pbcssg-gate-key');
    var ct = b64(attr(el, 'data-ct')), iv = b64(attr(el, 'data-iv'));
    var chain = Promise.resolve(null);
    keks.forEach(function (kek) {
      for (var i = 0; i < wraps.length; i++) {
        (function (w) {
          chain = chain.then(function (html) {
            if (html != null) { return html; }
            return importKey(kek)
              .then(function (kk) { return gcm(kk, b64(attr(w, 'data-wiv')), b64(attr(w, 'data-w'))); })
              .then(function (dekBuf) {
                return importKey(new Uint8Array(dekBuf))
                  .then(function (dk) { return gcm(dk, iv, ct); })
                  .then(function (buf) { return new TextDecoder().decode(buf); });
              })
              .catch(function () { return null; });
          });
        })(wraps[i]);
      }
    });
    return chain;
  }
  function open(el, html) {
    var out = el.querySelector('.pbcssg-gate-out');
    if (out) { out.innerHTML = html; }
    var keys = el.querySelectorAll('.pbcssg-gate-key');
    for (var i = 0; i < keys.length; i++) { if (keys[i].parentNode) { keys[i].parentNode.removeChild(keys[i]); } }
    el.setAttribute('data-pbcssg-gate-open', '1');
  }

  // Lock control: forget every held key (shared-machine hygiene), then reload.
  function wireLock(hasKeys) {
    var btn = document.querySelector('[data-pbcssg-lock]');
    if (!btn) { return; }
    if (hasKeys) { btn.hidden = false; }
    btn.addEventListener('click', function () {
      try { localStorage.removeItem(RING); } catch (e) {}
      location.reload();
    });
  }

  deposit();
  var ring = readRing(), aliases = Object.keys(ring), keks = [];
  for (var a = 0; a < aliases.length; a++) { try { keks.push(b64(ring[aliases[a]])); } catch (e) {} }
  wireLock(keks.length > 0);
  if (!subtle || !keks.length) { return; }
  var blocks = document.querySelectorAll('[data-pbcssg-gate]');
  for (var i = 0; i < blocks.length; i++) {
    (function (el) { tryUnlock(el, keks).then(function (html) { if (html != null) { open(el, html); } }); })(blocks[i]);
  }
})();
`

// ThemeJS is the self-hosted light/dark theme script. It runs blocking in <head>
// (before first paint) so a stored choice is applied without a flash, then wires
// the footer toggle on DOMContentLoaded. The default — no stored choice — leaves
// no data-theme attribute, so the page follows the OS via
// @media (prefers-color-scheme). The toggle cycles Auto → Light → Dark → Auto and
// persists only an explicit choice, in first-party localStorage (never sent to a
// server). Storage access is wrapped in try/catch so a hardened browser that
// blocks it degrades to Auto instead of erroring. No inline code / no third party,
// so a strict script-src 'self' CSP allows it (SPEC §6.4).
const ThemeJS = `(function () {
  var KEY = 'pbcssg-theme';
  var root = document.documentElement;
  function stored() {
    try { var v = localStorage.getItem(KEY); return (v === 'light' || v === 'dark') ? v : null; }
    catch (e) { return null; }
  }
  function apply(v) {
    if (v) { root.setAttribute('data-theme', v); } else { root.removeAttribute('data-theme'); }
  }
  // Before paint: honour any stored choice so there is no light/dark flash.
  apply(stored());
  function label(v) { return v === 'dark' ? 'Dark' : v === 'light' ? 'Light' : 'Auto'; }
  function icon(v) { return v === 'dark' ? '☾' : v === 'light' ? '☀' : '◐'; }
  function wire() {
    var btn = document.querySelector('[data-pbcssg-theme-toggle]');
    if (!btn) { return; }
    var cur = stored(); // null = Auto (follow the OS)
    function render() {
      btn.textContent = icon(cur) + ' ' + label(cur);
      btn.setAttribute('aria-label', 'Colour theme: ' + label(cur) + ' (following your device when Auto). Activate to change.');
    }
    render();
    btn.hidden = false; // progressive enhancement: only shown when the script runs
    btn.addEventListener('click', function () {
      cur = cur === null ? 'light' : cur === 'light' ? 'dark' : null;
      apply(cur);
      try { if (cur) { localStorage.setItem(KEY, cur); } else { localStorage.removeItem(KEY); } } catch (e) {}
      render();
    });
  }
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', wire); } else { wire(); }
})();
`

// FacadeJS is the self-hosted click-to-load facade script shared by the youtube
// and generic embed pages. It contacts no third party until the user clicks play,
// at which point it swaps the facade for the provider iframe. It uses no inline
// code so a strict script-src 'self' CSP allows it. A youtube facade builds the
// youtube-nocookie URL from its data-video-id; a generic embed facade frames its
// data-embed-url verbatim (its host is CSP-allowlisted by the build).
const FacadeJS = `(function () {
  function activate(el, src, title) {
    var iframe = document.createElement('iframe');
    iframe.setAttribute('src', src);
    iframe.setAttribute('title', title);
    iframe.setAttribute('loading', 'lazy');
    // Send only the origin (not the full URL) once the user has consented by
    // clicking play. Stripping the referrer entirely breaks some embeds (e.g.
    // YouTube "Error 153") because the provider validates the embedding origin.
    iframe.setAttribute('referrerpolicy', 'strict-origin-when-cross-origin');
    iframe.setAttribute('allow', 'autoplay; fullscreen; picture-in-picture');
    iframe.setAttribute('allowfullscreen', '');
    iframe.setAttribute('width', '560');
    iframe.setAttribute('height', '315');
    el.replaceChildren(iframe);
  }
  function wire(el, srcFn, title) {
    var btn = el.querySelector('.pbcssg-facade-play');
    if (!btn) return;
    btn.addEventListener('click', function () {
      var src = srcFn(el);
      if (src) activate(el, src, title);
    });
  }
  var yts = document.querySelectorAll('.pbcssg-youtube-facade');
  for (var i = 0; i < yts.length; i++) {
    wire(yts[i], function (el) {
      var id = el.getAttribute('data-video-id') || '';
      return 'https://www.youtube-nocookie.com/embed/' + encodeURIComponent(id) + '?autoplay=1';
    }, 'YouTube video player');
  }
  var embeds = document.querySelectorAll('.pbcssg-embed-facade');
  for (var j = 0; j < embeds.length; j++) {
    wire(embeds[j], function (el) { return el.getAttribute('data-embed-url') || ''; }, 'Embedded content');
  }
})();
`

var (
	// md renders markdown in goldmark's default *safe* mode (no raw HTML, dangerous
	// URLs neutralized) with GFM (tables, strikethrough, task lists, autolinks) +
	// footnotes enabled, plus auto-generated heading IDs (SPEC §6.12). Safe mode is
	// preserved: the extensions add block/inline syntax, not raw-HTML passthrough, so
	// every external reference an author writes — including a GFM-autolinked bare URL —
	// still flows through the downstream hygiene + classification passes unchanged.
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	base              = template.Must(template.New("page").Parse(baseLayout))
	cardTmpl          = template.Must(template.New("card").Parse(cardTemplate))
	externalTmpl      = template.Must(template.New("external").Parse(externalTemplate))
	embedCardTmpl     = template.Must(template.New("embedcard").Parse(embedCardTemplate))
	embedExternalTmpl = template.Must(template.New("embedexternal").Parse(embedExternalTemplate))
	imageTmpl         = template.Must(template.New("image").Parse(imageTemplate))
	mediaTmpl         = template.Must(template.New("media").Parse(mediaTemplate))
	calloutTmpl       = template.Must(template.New("callout").Parse(calloutTemplate))
	codeTmpl          = template.Must(template.New("code").Parse(codeTemplate))
	detailsTmpl       = template.Must(template.New("details").Parse(detailsTemplate))
	tocTmpl           = template.Must(template.New("toc").Parse(tocTemplate))
	relatedTmpl       = template.Must(template.New("related").Parse(relatedTemplate))
	galleryTmpl       = template.Must(template.New("gallery").Parse(galleryTemplate))
	shareTmpl         = template.Must(template.New("share").Parse(shareTemplate))
	citationTmpl      = template.Must(template.New("citation").Parse(citationTemplate))
	indexTmpl         = template.Must(template.New("index").Parse(indexTemplate))
	revealTmpl        = template.Must(template.New("reveal").Parse(revealTemplate))
	gateTmpl          = template.Must(template.New("gate").Parse(gateTemplate))
)

// gateTemplate renders a group-gated block (SPEC §6.10). The payload is present
// only as base64 ciphertext (data-ct/data-iv); the plaintext never appears. Each
// wrapped-DEK ships as an unlabeled <span data-w/data-wiv> — no alias, no order that
// maps to a group — so a crawler learns nothing about which or how many groups can
// unlock it. The client trial-unwraps with each keyring KEK and, on a match, injects
// the decrypted (goldmark-safe, hardened) HTML into the aria-live output. A visitor
// without a matching key sees only the <noscript> note (and nothing in the DOM).
const gateTemplate = `<div class="pbcssg-gate" data-pbcssg-gate data-ct="{{.Ciphertext}}" data-iv="{{.Nonce}}">
{{- range .Wrapped}}
<span class="pbcssg-gate-key" data-w="{{.Ciphertext}}" data-wiv="{{.Nonce}}"></span>
{{- end}}
<div class="pbcssg-gate-out" aria-live="polite"></div>
<noscript><span class="pbcssg-gate-noscript">{{.NoScript}}</span></noscript>
</div>
`

// revealTemplate renders the deferred-reveal block (SPEC §6.9). The payload is
// present only as base64 ciphertext in data-* attributes; the plaintext never
// appears (nor does the Mode B code). The trigger is a real <button> with
// aria-expanded; the revealed value is injected by the client script into the
// aria-live output span. Mode A ships data-key (the decode key). Mode B instead
// ships data-gated + data-salt + data-iters and a code prompt: the client derives
// the key from the visitor's code via PBKDF2, and a wrong code fails GCM
// authentication (announced via the aria-live error). The <noscript> fallback
// covers the no-JS case. Label/NoScript are plain text (html/template escapes them).
const revealTemplate = `<div class="pbcssg-reveal" data-pbcssg-reveal data-kind="{{.Kind}}" data-ct="{{.Ciphertext}}" data-iv="{{.Nonce}}"{{if eq .Mode "b"}} data-gated="1" data-salt="{{.Salt}}" data-iters="{{.Iters}}"{{else if eq .Mode "c"}} data-group="1"{{else}} data-key="{{.Key}}"{{end}}>
<button type="button" class="pbcssg-reveal-btn" aria-expanded="false"{{if eq .Mode "b"}} aria-controls="pbcssg-reveal-gate-{{.ID}}"{{end}}>{{.Label}}</button>
{{- if eq .Mode "b"}}
<div class="pbcssg-reveal-gate" id="pbcssg-reveal-gate-{{.ID}}" hidden>
<label class="pbcssg-reveal-code-label" for="pbcssg-reveal-code-{{.ID}}">Code</label>
<input class="pbcssg-reveal-code" id="pbcssg-reveal-code-{{.ID}}" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" aria-describedby="pbcssg-reveal-error-{{.ID}}">
<button type="button" class="pbcssg-reveal-unlock">Unlock</button>
<span class="pbcssg-reveal-error" id="pbcssg-reveal-error-{{.ID}}" role="alert" aria-live="assertive"></span>
</div>
{{- end}}
{{- if eq .Mode "c"}}
{{- range .Wrapped}}
<span class="pbcssg-reveal-key" data-w="{{.Ciphertext}}" data-wiv="{{.Nonce}}"></span>
{{- end}}
{{- end}}
<span class="pbcssg-reveal-out" aria-live="polite"></span>
<noscript><span class="pbcssg-reveal-noscript">{{.NoScript}}</span></noscript>
</div>
`

// revealGroupPreviewTmpl shows a members-only (Mode C) reveal visibly in the editor
// preview with a group label — the operator's own view, never the published output.
var revealGroupPreviewTmpl = template.Must(template.New("reveal-group-preview").Parse(
	`<div class="pbcssg-reveal-preview" data-pbcssg-reveal-preview>` +
		`<p class="pbcssg-reveal-preview-label">🔒 Members-only reveal — unlocks for: {{.Groups}}</p>` +
		`<div class="pbcssg-reveal-preview-body">{{.Body}}</div></div>`))

// citationTemplate renders a quotation. The quote body is goldmark-safe HTML;
// the source is plain text (escaped); the URL is validated same-site/http(s) by
// the editor before it reaches here.
const citationTemplate = `<figure class="pbcssg-citation">
<blockquote>
{{.Body}}
</blockquote>
{{- if or .Source .URL}}
<figcaption>— {{if .Source}}<cite>{{.Source}}</cite>{{end}}{{if .URL}} <a href="{{.URL}}">{{.URL}}</a>{{end}}</figcaption>
{{- end}}
</figure>
`

// calloutTemplate renders an admonition. The variant is a fixed-allowlist class;
// the title is plain text (escaped); the body is goldmark-safe HTML.
const calloutTemplate = `<aside class="pbcssg-callout pbcssg-callout-{{.Variant}}">
{{- if .Title}}
<p class="pbcssg-callout-title">{{.Title}}</p>
{{- end}}
{{.Body}}
</aside>
`

// tocTemplate renders a table-of-contents placeholder (SPEC §6.12). It carries the
// author's depth/title as data-* hints; the build-time AnchorsAndTOC pass reads them,
// replaces the placeholder with the nested heading list, and drops the hints. Emitted
// as an empty <nav> so a page rendered without the pass (a raw Render) degrades to an
// empty, harmless landmark rather than broken markup.
const tocTemplate = `<nav class="pbcssg-toc" data-pbcssg-toc data-depth="{{.Depth}}"{{if .Title}} data-title="{{.Title}}"{{end}} aria-label="Table of contents"></nav>`

// galleryTemplate renders a responsive image grid with a CSS-only :target lightbox
// (SPEC §6.14). Each thumbnail links to its overlay (#ID); the overlay's backdrop and
// × close by linking to a non-existent #ID-x, which clears :target without a scroll
// jump. No JavaScript. Images are self-hosted /media paths; alt text is escaped.
const galleryTemplate = `<div class="pbcssg-gallery pbcssg-gallery--cols-{{.Columns}}">
{{- range .Items}}
<figure class="pbcssg-gallery-item">
<a class="pbcssg-gallery-thumb" href="#{{.ID}}"{{if .Alt}} aria-label="Enlarge: {{.Alt}}"{{end}}><img src="{{.Src}}" alt="{{.Alt}}" loading="lazy"></a>
{{- if .Caption}}
<figcaption class="pbcssg-gallery-caption">{{.Caption}}</figcaption>
{{- end}}
<div class="pbcssg-lightbox" id="{{.ID}}" role="dialog" aria-modal="true"{{if .Alt}} aria-label="{{.Alt}}"{{end}}>
<a class="pbcssg-lightbox-backdrop" href="#{{.ID}}-x" aria-label="Close"></a>
<img class="pbcssg-lightbox-img" src="{{.Src}}" alt="{{.Alt}}">
<a class="pbcssg-lightbox-x" href="#{{.ID}}-x" aria-label="Close">&times;</a>
</div>
</figure>
{{- end}}
</div>
`

// shareTemplate renders the privacy-preserving share block (SPEC §6.15). Copy-link and
// the Mastodon form are wired by ShareJS; Email is a plain mailto: (no JS); RSS is an
// optional feed link. No third party is embedded — nothing loads on page view.
const shareTemplate = `<nav class="pbcssg-share" aria-label="{{.Title}}">
<p class="pbcssg-share-title">{{.Title}}</p>
<div class="pbcssg-share-actions">
{{- if .CopyLink}}
<button type="button" class="pbcssg-share-btn" data-pbcssg-share-copy>Copy link</button>
{{- end}}
{{- if .Email}}
<a class="pbcssg-share-btn" href="{{.MailtoHref}}">Email</a>
{{- end}}
{{- if .Mastodon}}
<form class="pbcssg-share-mastodon" data-pbcssg-share-mastodon>
<input type="text" inputmode="url" autocomplete="off" spellcheck="false" placeholder="your.instance" aria-label="Your Mastodon instance">
<button type="submit" class="pbcssg-share-btn">Share on Mastodon</button>
</form>
{{- end}}
{{- if .RSS}}
<a class="pbcssg-share-btn" href="{{.RSS}}" rel="alternate">RSS</a>
{{- end}}
</div>
</nav>
`

// relatedTemplate renders the related-posts list (SPEC §6.13). All links are
// internal (same-site page paths); titles/dates are plain text (escaped).
const relatedTemplate = `<nav class="pbcssg-related" aria-label="{{.Title}}">
<p class="pbcssg-related-title">{{.Title}}</p>
<ul class="pbcssg-related-list">
{{- range .Items}}
<li><a href="{{.Path}}">{{.Title}}</a>{{if .Date}} <time class="pbcssg-related-date">{{.Date}}</time>{{end}}</li>
{{- end}}
</ul>
</nav>
`

// detailsTemplate renders a disclosure / FAQ block (SPEC §6.12) as a native
// <details>/<summary>. The summary is plain text (escaped); the body is
// goldmark-safe HTML. No JavaScript; keyboard-accessible by default.
const detailsTemplate = `<details class="pbcssg-details"{{if .Open}} open{{end}}>
<summary class="pbcssg-details-summary">{{.Summary}}</summary>
<div class="pbcssg-details-body">
{{.Body}}
</div>
</details>
`

// codeTemplate renders a verbatim code listing (SPEC §6.12). Filename/Language/
// Comment are plain text (html/template escapes them). Lines is pre-escaped,
// per-line-wrapped HTML built by renderCode; the <pre><code> stays on one source
// line so no template whitespace leaks into the significant <pre> content. The copy
// button carries data-pbcssg-copy for the self-hosted CodeCopyJS to wire.
const codeTemplate = `<figure class="pbcssg-code{{if .LineNumbers}} pbcssg-code--numbered{{end}}">
{{- if or .Filename .Language}}
<figcaption class="pbcssg-code-caption">
{{- if .Filename}}<span class="pbcssg-code-filename">{{.Filename}}</span>{{end}}
{{- if .Language}}<span class="pbcssg-code-lang">{{.Language}}</span>{{end}}
</figcaption>
{{- end}}
<div class="pbcssg-code-wrap">
<button type="button" class="pbcssg-code-copy" data-pbcssg-copy>Copy</button>
<pre class="pbcssg-code-pre"><code class="pbcssg-code-el">{{.Lines}}</code></pre>
</div>
{{- if .Comment}}
<p class="pbcssg-code-comment">{{.Comment}}</p>
{{- end}}
</figure>
`

// imageTemplate renders a figure block. loading=lazy defers off-screen images;
// hygiene adds referrerpolicy to any external src. The caption is plain text
// (html/template escapes it); an empty caption is omitted.
const imageTemplate = `<figure class="{{.Class}}">
<img src="{{.Src}}" alt="{{.Alt}}" loading="lazy">
{{- if .Caption}}
<figcaption>{{.Caption}}</figcaption>
{{- end}}
</figure>
`

// mediaTemplate renders a self-hosted audio/video figure with a native player.
// preload="metadata" avoids fetching the whole file up front; a download link is
// the no-JS/unsupported-codec fallback. The src/poster are same-site /media paths
// validated by the editor, so html/template's URL filter never rejects them.
const mediaTemplate = `<figure class="pbcssg-media pbcssg-media-{{.Kind}}">
{{- if eq .Kind "video"}}
<video class="pbcssg-media-el" controls preload="metadata" playsinline{{if .Poster}} poster="{{.Poster}}"{{end}} src="{{.Src}}">Your browser can’t play this video. <a href="{{.Src}}">Download it</a>.</video>
{{- else}}
<audio class="pbcssg-media-el" controls preload="metadata" src="{{.Src}}">Your browser can’t play this audio. <a href="{{.Src}}">Download it</a>.</audio>
{{- end}}
{{- if .Caption}}
<figcaption>{{.Caption}}</figcaption>
{{- end}}
</figure>
`

const baseLayout = `<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{- if .NoIndex}}
<meta name="robots" content="noindex">
{{- end}}
{{- if .ThemeJSHref}}
<script src="{{.ThemeJSHref}}"></script>
{{- end}}
<title>{{.Title}}{{if .SiteName}} · {{.SiteName}}{{end}}</title>
{{- if .Description}}
<meta name="description" content="{{.Description}}">
{{- end}}
{{- if .CanonicalURL}}
<link rel="canonical" href="{{.CanonicalURL}}">
{{- end}}
{{- with .Favicon}}
{{- if .ICO}}
<link rel="icon" href="/favicon.ico" sizes="any">
{{- end}}
{{- if .SVG}}
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
{{- end}}
{{- if .AppleTouch}}
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
{{- end}}
{{- if .Manifest}}
<link rel="manifest" href="/site.webmanifest">
{{- end}}
{{- if .ThemeColor}}
<meta name="theme-color" content="{{.ThemeColor}}">
{{- end}}
{{- end}}
{{- if .OpenGraph}}
<meta property="og:title" content="{{.Title}}">
<meta property="og:type" content="website">
{{- if .SiteName}}
<meta property="og:site_name" content="{{.SiteName}}">
{{- end}}
{{- if .Description}}
<meta property="og:description" content="{{.Description}}">
{{- end}}
{{- if .CanonicalURL}}
<meta property="og:url" content="{{.CanonicalURL}}">
{{- end}}
{{- if .OGImage}}
<meta property="og:image" content="{{.OGImage}}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{{.OGImage}}">
{{- end}}
{{- end}}
{{- range .FeedLinks}}
<link rel="alternate" type="{{.Type}}" href="{{.Href}}" title="{{.Title}}">
{{- end}}
{{- if .CSSHref}}
<link rel="stylesheet" href="{{.CSSHref}}">
{{- end}}
{{- if .CommentsCSSHref}}
<link rel="stylesheet" href="{{.CommentsCSSHref}}">
{{- end}}
</head>
<body>
{{- if or .Brand.Show .Nav .Search}}
<header class="pbcssg-header{{if .Brand.Centered}} pbcssg-header--center{{end}}">
{{- if .Brand.Show}}
<a class="pbcssg-brand" href="/">
{{- if .Brand.ShowLogo}}{{if .Brand.HasDarkLogo}}<img class="pbcssg-logo pbcssg-logo--{{.Brand.LogoHeight}} pbcssg-logo--light" src="{{.Brand.LogoSrc}}" alt="{{.Brand.ImgAlt}}"><img class="pbcssg-logo pbcssg-logo--{{.Brand.LogoHeight}} pbcssg-logo--dark" src="{{.Brand.LogoSrcDark}}" alt="{{.Brand.ImgAlt}}">{{else}}<img class="pbcssg-logo pbcssg-logo--{{.Brand.LogoHeight}}" src="{{.Brand.LogoSrc}}" alt="{{.Brand.ImgAlt}}">{{end}}{{end}}
{{- if .Brand.ShowText}}<span class="pbcssg-brand-text">{{.Brand.Text}}</span>{{end}}
</a>
{{- end}}
{{- if .Nav}}
<nav class="pbcssg-nav" aria-label="Primary">
{{- range .Nav}}
<a href="{{.Href}}">{{.Label}}</a>
{{- end}}
</nav>
{{- end}}
{{- if .Search}}
<form class="pbcssg-search" role="search">
<div class="pbcssg-search-row">
<label for="pbcssg-search-input" class="pbcssg-search-label">Search</label>
<input id="pbcssg-search-input" type="search" data-pbcssg-search autocomplete="off" aria-controls="pbcssg-search-results" placeholder="Search…">
</div>
<ul id="pbcssg-search-results" class="pbcssg-search-results" aria-live="polite"></ul>
</form>
{{- end}}
</header>
{{- end}}
<main>
{{- if .SplashAlias}}
<div data-pbcssg-splash="{{.SplashAlias}}" hidden></div>
{{- end}}
{{.Content}}
{{- if .Tags}}
<nav class="pbcssg-tags" aria-label="Tags">
<span class="pbcssg-tags-label">Tagged:</span>
{{- range .Tags}}
<a class="pbcssg-tag" href="{{.Href}}">{{.Name}}</a>
{{- end}}
</nav>
{{- end}}
<!-- ExtRefSlot: the build replaces this element with the external-references
     listing (§5.7) once classification is known, or removes it when the page has
     no external references. It sits inside <main> so it inherits the page measure
     and padding. Kept as an element (not a comment) because html/template strips
     comments; see render.ExtRefSlot. -->
<div data-pbcssg-extref="1"></div>
</main>
<footer class="pbcssg-footer">
{{- if .FooterNav}}
<nav class="pbcssg-footer-nav" aria-label="Footer">{{range $i, $l := .FooterNav}}{{if $i}} | {{end}}<a href="{{$l.Href}}">{{$l.Label}}</a>{{end}}</nav>
{{- end}}
<p class="pbcssg-copyright">©{{if .Year}} {{.Year}}{{end}}{{if .SiteName}} {{.SiteName}}{{end}}</p>
{{- if .GateJSHref}}
<button type="button" class="pbcssg-lock" data-pbcssg-lock hidden>Forget my access keys</button>
{{- end}}
{{- if .ThemeJSHref}}
<button type="button" class="pbcssg-theme-toggle" data-pbcssg-theme-toggle hidden>◐ Auto</button>
{{- end}}
</footer>
{{- if and .Search .SearchJSHref}}
<script src="{{.SearchJSHref}}" defer></script>
{{- end}}
{{- if .RevealJSHref}}
<script src="{{.RevealJSHref}}" defer></script>
{{- end}}
{{- if .GateJSHref}}
<script src="{{.GateJSHref}}" defer></script>
{{- end}}
{{- if .CodeCopyJSHref}}
<script src="{{.CodeCopyJSHref}}" defer></script>
{{- end}}
{{- if .ShareJSHref}}
<script src="{{.ShareJSHref}}" defer></script>
{{- end}}
{{- if .CommentsJSHref}}
<script src="{{.CommentsJSHref}}" defer></script>
{{- end}}
</body>
</html>
`

// Stage 1: inline consent card. Contacts no third party (optional self-hosted
// poster only). SPEC §5.8 default copy.
const cardTemplate = `<aside class="pbcssg-consent-card">
{{- if .Poster}}
<img class="pbcssg-consent-poster" src="{{.Poster}}" alt="">
{{- end}}
<p class="pbcssg-consent-label">External video · {{.Title}}</p>
<p>This video is on YouTube. To keep this page private, nothing from YouTube loads here. Open the video page to watch it, read the transcript, and see the links from the description. YouTube may set cookies and track you once you choose to play.</p>
<p><a class="pbcssg-consent-open" href="/external/youtube/{{.Name}}">Open video page →</a></p>
</aside>
`

// Stage 2: the /external/youtube/<name> page body. The facade loads nothing from
// Google until the play button is pressed. SPEC §5.8 default copy.
const externalTemplate = `<article class="pbcssg-video">
{{- if .BackHref}}
<p class="pbcssg-back"><a href="{{.BackHref}}">← Back{{if .BackLabel}} to {{.BackLabel}}{{end}}</a></p>
{{- end}}
<h1>{{.Title}}</h1>
<p class="pbcssg-video-intro">You're on the video page for “{{.Title}}.” You can read the transcript and links below without loading anything from YouTube. Press play only when you're ready — that's when YouTube loads and may begin tracking you.</p>
<div class="pbcssg-youtube-facade" data-video-id="{{.VideoID}}">
{{- if .Poster}}
<img class="pbcssg-facade-poster" src="{{.Poster}}" alt="">
{{- end}}
<button type="button" class="pbcssg-facade-play">▶ Play — loads YouTube</button>
<p class="pbcssg-facade-note">Pressing play loads youtube-nocookie.com. YouTube may set cookies and track your viewing from that point.</p>
</div>
<section class="pbcssg-transcript">
<h2>Transcript</h2>
{{.TranscriptHTML}}
</section>
{{- if .Links}}
<section class="pbcssg-video-links">
<h2>Links from this video</h2>
<ul>
{{- range .Links}}
<li><a href="{{.URL}}">{{.Label}}</a></li>
{{- end}}
</ul>
</section>
{{- end}}
{{- if .FacadeJSHref}}
<script src="{{.FacadeJSHref}}" defer></script>
{{- end}}
</article>
`

// Stage 1: generic embed consent card. Contacts no third party (optional
// self-hosted poster only). Mirrors the youtube card for any provider.
const embedCardTemplate = `<aside class="pbcssg-consent-card">
{{- if .Poster}}
<img class="pbcssg-consent-poster" src="{{.Poster}}" alt="">
{{- end}}
<p class="pbcssg-consent-label">External embed · {{.Title}}</p>
<p>This content is embedded from {{.Provider}}. To keep this page private, nothing from {{.Provider}} loads here. Open the embed page to view it and read the notes. {{.Provider}} may set cookies and track you once you choose to load it.</p>
<p><a class="pbcssg-consent-open" href="/external/{{.Provider}}/{{.Name}}">Open embed page →</a></p>
</aside>
`

// Stage 2: the /external/<provider>/<name> page body. The facade loads nothing
// from the provider until the play button is pressed.
const embedExternalTemplate = `<article class="pbcssg-video">
{{- if .BackHref}}
<p class="pbcssg-back"><a href="{{.BackHref}}">← Back{{if .BackLabel}} to {{.BackLabel}}{{end}}</a></p>
{{- end}}
<h1>{{.Title}}</h1>
<p class="pbcssg-video-intro">You're on the embed page for “{{.Title}}.” You can read the notes and links below without loading anything from {{.Provider}}. Press load only when you're ready — that's when {{.Provider}} loads and may begin tracking you.</p>
<div class="pbcssg-embed-facade" data-embed-url="{{.EmbedURL}}">
{{- if .Poster}}
<img class="pbcssg-facade-poster" src="{{.Poster}}" alt="">
{{- end}}
<button type="button" class="pbcssg-facade-play">▶ Load — loads {{.Provider}}</button>
<p class="pbcssg-facade-note">Pressing load frames {{.EmbedHost}}. {{.Provider}} may set cookies and track your viewing from that point.</p>
</div>
{{- if .TranscriptHTML}}
<section class="pbcssg-transcript">
<h2>Notes</h2>
{{.TranscriptHTML}}
</section>
{{- end}}
{{- if .Links}}
<section class="pbcssg-video-links">
<h2>Links</h2>
<ul>
{{- range .Links}}
<li><a href="{{.URL}}">{{.Label}}</a></li>
{{- end}}
</ul>
</section>
{{- end}}
{{- if .FacadeJSHref}}
<script src="{{.FacadeJSHref}}" defer></script>
{{- end}}
</article>
`

// indexTemplate renders a route-based page list. Titles/summaries are plain text
// (html/template escapes them); Path is a same-site page path.
const indexTemplate = `<section class="pbcssg-index">
{{- if .Title}}
<h2 class="pbcssg-index-title">{{.Title}}</h2>
{{- end}}
{{- if .Items}}
<ul class="pbcssg-index-list{{if .Detailed}} pbcssg-index-detailed{{end}}">
{{- range .Items}}
<li><a href="{{.Path}}">{{.Title}}</a>
{{- if $.Detailed}}
{{- if .Date}} <time class="pbcssg-index-date">{{.Date}}</time>{{end}}
{{- if .Summary}}
<p class="pbcssg-index-summary">{{.Summary}}</p>
{{- end}}
{{- end}}
</li>
{{- end}}
</ul>
{{- if .Truncated}}
<p class="pbcssg-index-more">Showing the first {{.Shown}} of {{.Total}}.</p>
{{- end}}
{{- else}}
<p class="pbcssg-index-empty">No pages yet.</p>
{{- end}}
</section>
`

type tagChip struct{ Name, Href string }

type pageData struct {
	Lang            string
	Title           string
	SiteName        string
	Description     string
	CanonicalURL    string
	OpenGraph       bool
	OGImage         string
	NoIndex         bool
	Tags            []tagChip
	Nav             []NavLink
	FooterNav       []NavLink
	Year            int
	FeedLinks       []FeedLink
	BuildNumber     string
	Content         template.HTML
	Brand           Brand
	Search          bool
	CSSHref         string
	SearchJSHref    string
	ThemeJSHref     string
	RevealJSHref    string
	GateJSHref      string
	CodeCopyJSHref  string
	ShareJSHref     string
	CommentsJSHref  string
	CommentsCSSHref string
	SplashAlias     string
	Favicon         FaviconLinks
}

// Render parses contentJSON and returns the full HTML page plus any youtube
// blocks (so the build can generate their external pages).
func Render(contentJSON string, opts Options) (*Rendered, error) {
	c, err := Parse(contentJSON)
	if err != nil {
		return nil, err
	}
	return RenderContent(c, opts)
}

// RenderContent renders an already-parsed Content. The build and editor preview
// use it after pre-processing markdown reveal blocks (rendering + hardening their
// bodies, SPEC §6.9), so the reveal payload they encrypt is the hardened HTML.
func RenderContent(c Content, opts Options) (*Rendered, error) {
	inner, yts, embeds, err := renderContent(c, opts)
	if err != nil {
		return nil, err
	}
	// A content page's noindex directive comes from its own Content, so it applies
	// in both the build and the editor preview (which also render via Render). An
	// unlisted page (§6.16) implies noindex.
	opts.NoIndex = c.NoIndex || c.Unlisted
	html, err := layout(opts, inner)
	if err != nil {
		return nil, err
	}
	return &Rendered{HTML: html, YouTube: yts, Embed: embeds}, nil
}

// DescLink is a parsed description link: an optional label and its URL.
type DescLink struct {
	Label string
	URL   string
}

// ParseDescriptionLink splits one description-link line into an optional leading
// label and its http(s) URL. "Docs https://example.com" -> {"Docs",
// "https://example.com"}; a bare "https://example.com" uses the URL as its own
// label. Returns ok=false when the line contains no valid http(s) URL — so
// invalid entries are dropped rather than reaching html/template's URL filter
// (which would render the cryptic "#ZgotmplZ" sentinel).
func ParseDescriptionLink(line string) (label, rawURL string, ok bool) {
	fields := strings.Fields(line)
	for i, f := range fields {
		if u, err := url.Parse(f); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
			label = strings.TrimSpace(strings.Join(fields[:i], " "))
			if label == "" {
				label = f
			}
			return label, f, true
		}
	}
	return "", "", false
}

// ExternalYouTube renders the Stage 2 /external/youtube/<name> page for a block.
func ExternalYouTube(yt YouTube, opts Options) ([]byte, error) {
	transcript, err := toHTML(yt.Transcript)
	if err != nil {
		return nil, err
	}
	var links []DescLink
	for _, l := range yt.DescriptionLinks {
		if label, u, ok := ParseDescriptionLink(l); ok {
			links = append(links, DescLink{Label: label, URL: u})
		}
	}
	var body bytes.Buffer
	err = externalTmpl.Execute(&body, struct {
		Title          string
		VideoID        string
		Poster         string
		Links          []DescLink
		TranscriptHTML template.HTML
		FacadeJSHref   string
		BackHref       string
		BackLabel      string
	}{yt.Title, yt.VideoID, yt.Poster, links, template.HTML(transcript), opts.FacadeJSHref, opts.BackHref, opts.BackLabel})
	if err != nil {
		return nil, fmt.Errorf("render: youtube page: %w", err)
	}
	return layout(opts, body.String())
}

// EmbedHost returns the host of an embed URL (for disclosure copy), or "" if it
// cannot be parsed.
func EmbedHost(embedURL string) string {
	if u, err := url.Parse(embedURL); err == nil {
		return u.Host
	}
	return ""
}

// ExternalEmbed renders the Stage 2 /external/<provider>/<name> page for a generic
// embed block.
func ExternalEmbed(e Embed, opts Options) ([]byte, error) {
	notes, err := toHTML(e.Transcript)
	if err != nil {
		return nil, err
	}
	var links []DescLink
	for _, l := range e.DescriptionLinks {
		if label, u, ok := ParseDescriptionLink(l); ok {
			links = append(links, DescLink{Label: label, URL: u})
		}
	}
	var body bytes.Buffer
	err = embedExternalTmpl.Execute(&body, struct {
		Title          string
		Provider       string
		EmbedURL       string
		EmbedHost      string
		Poster         string
		Links          []DescLink
		TranscriptHTML template.HTML
		FacadeJSHref   string
		BackHref       string
		BackLabel      string
	}{e.Title, ProviderLabel(e.Provider), e.EmbedURL, EmbedHost(e.EmbedURL), e.Poster, links, template.HTML(notes), opts.FacadeJSHref, opts.BackHref, opts.BackLabel})
	if err != nil {
		return nil, fmt.Errorf("render: embed page: %w", err)
	}
	return layout(opts, body.String())
}

func renderContent(c Content, opts Options) (string, []YouTube, []Embed, error) {
	var out strings.Builder
	var yts []YouTube
	var embeds []Embed
	// Reading time (SPEC §6.13): posts only, when the site setting is on. Emitted here
	// with a marker at the top of the content; the AnchorsAndTOC build pass relocates it
	// to just after the page's first heading (a raw Render without that pass shows it at
	// the top, which is a graceful fallback).
	if opts.ShowReadingTime && c.IsPost {
		out.WriteString(readingTimeMeta(c))
	}
	if strings.TrimSpace(c.Body) != "" {
		h, err := toHTML(c.Body)
		if err != nil {
			return "", nil, nil, err
		}
		out.WriteString(h)
	}
	for i, b := range c.Blocks {
		switch b.Type {
		case "", "markdown":
			h, err := toHTML(b.Markdown)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(h)
		case "youtube":
			if b.YouTube == nil {
				return "", nil, nil, fmt.Errorf("render: youtube block %d has no data", i)
			}
			card, err := renderCard(*b.YouTube)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(card)
			yts = append(yts, *b.YouTube)
		case "embed":
			if b.Embed == nil {
				return "", nil, nil, fmt.Errorf("render: embed block %d has no data", i)
			}
			card, err := renderEmbedCard(*b.Embed)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(card)
			embeds = append(embeds, *b.Embed)
		case "image":
			if b.Image == nil {
				return "", nil, nil, fmt.Errorf("render: image block %d has no data", i)
			}
			img, err := renderImage(*b.Image)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(img)
		case "media":
			if b.Media == nil {
				return "", nil, nil, fmt.Errorf("render: media block %d has no data", i)
			}
			mh, err := renderMedia(*b.Media)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(mh)
		case "callout":
			if b.Callout == nil {
				return "", nil, nil, fmt.Errorf("render: callout block %d has no data", i)
			}
			co, err := renderCallout(*b.Callout)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(co)
		case "citation":
			if b.Citation == nil {
				return "", nil, nil, fmt.Errorf("render: citation block %d has no data", i)
			}
			ci, err := renderCitation(*b.Citation)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(ci)
		case "code":
			if b.Code == nil {
				return "", nil, nil, fmt.Errorf("render: code block %d has no data", i)
			}
			cd, err := renderCode(*b.Code)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(cd)
		case "details":
			if b.Details == nil {
				return "", nil, nil, fmt.Errorf("render: details block %d has no data", i)
			}
			dt, err := renderDetails(*b.Details)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(dt)
		case "toc":
			if b.TOC == nil {
				return "", nil, nil, fmt.Errorf("render: toc block %d has no data", i)
			}
			tc, err := renderTOC(*b.TOC)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(tc)
		case "related":
			if b.Related == nil {
				return "", nil, nil, fmt.Errorf("render: related block %d has no data", i)
			}
			rl, err := renderRelated(*b.Related, opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(rl)
		case "gallery":
			if b.Gallery == nil {
				return "", nil, nil, fmt.Errorf("render: gallery block %d has no data", i)
			}
			gl, err := renderGallery(*b.Gallery, i)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(gl)
		case "share":
			if b.Share == nil {
				return "", nil, nil, fmt.Errorf("render: share block %d has no data", i)
			}
			sh, err := renderShare(*b.Share, opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(sh)
		case "index":
			if b.Index == nil {
				return "", nil, nil, fmt.Errorf("render: index block %d has no data", i)
			}
			ix, err := renderIndex(*b.Index, opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(ix)
		case "reveal":
			if b.Reveal == nil {
				return "", nil, nil, fmt.Errorf("render: reveal block %d has no data", i)
			}
			rv, err := renderReveal(*b.Reveal, b.Groups, i, opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(rv)
		case "gate":
			if b.Gate == nil {
				return "", nil, nil, fmt.Errorf("render: gate block %d has no data", i)
			}
			g, err := renderGate(*b.Gate, i, opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(g)
		case "comments":
			cm, err := renderComments(opts)
			if err != nil {
				return "", nil, nil, err
			}
			out.WriteString(cm)
		default:
			return "", nil, nil, fmt.Errorf("render: unknown block type %q (block %d)", b.Type, i)
		}
	}
	return out.String(), yts, embeds, nil
}

func renderCard(yt YouTube) (string, error) {
	var buf bytes.Buffer
	if err := cardTmpl.Execute(&buf, yt); err != nil {
		return "", fmt.Errorf("render: youtube card: %w", err)
	}
	return buf.String(), nil
}

// ProviderLabel is a human/URL-safe provider name for the embed card and path.
// It defaults to "embed" when the author left the provider blank.
func ProviderLabel(p string) string {
	if s := TagSlug(p); s != "" {
		return s
	}
	return "embed"
}

func renderEmbedCard(e Embed) (string, error) {
	var buf bytes.Buffer
	err := embedCardTmpl.Execute(&buf, struct {
		Provider string
		Name     string
		Title    string
		Poster   string
	}{ProviderLabel(e.Provider), e.Name, e.Title, e.Poster})
	if err != nil {
		return "", fmt.Errorf("render: embed card: %w", err)
	}
	return buf.String(), nil
}

func renderImage(img Image) (string, error) {
	data := struct {
		Src, Alt, Caption, Class string
	}{img.Src, img.Alt, img.Caption, figureClass(img)}
	var buf bytes.Buffer
	if err := imageTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render: image block: %w", err)
	}
	return buf.String(), nil
}

// figureClass builds the image figure's class list from the allowlisted align and
// max-width options, so only fixed CSS classes (never operator input) are emitted.
func figureClass(img Image) string {
	cls := "pbcssg-figure"
	switch NormImageAlign(img.Align) {
	case "left":
		cls += " pbcssg-figure--left"
	case "right":
		cls += " pbcssg-figure--right"
	}
	switch NormImageSize(img.MaxWidth) {
	case "small":
		cls += " pbcssg-figure--sm"
	case "medium":
		cls += " pbcssg-figure--md"
	case "large":
		cls += " pbcssg-figure--lg"
	}
	return cls
}

// MediaKind normalizes an authored media kind to "video" or "audio" (default
// "video"), so the rendered element and class are always from a fixed allowlist.
func MediaKind(k string) string {
	if strings.EqualFold(strings.TrimSpace(k), "audio") {
		return "audio"
	}
	return "video"
}

func renderMedia(m Media) (string, error) {
	var buf bytes.Buffer
	err := mediaTmpl.Execute(&buf, struct {
		Kind    string
		Src     string
		Poster  string
		Caption string
	}{MediaKind(m.Kind), m.Src, m.Poster, m.Caption})
	if err != nil {
		return "", fmt.Errorf("render: media block: %w", err)
	}
	return buf.String(), nil
}

// calloutVariant normalizes an authored variant to a known class, defaulting to
// "note" (so the class attribute is always from a fixed allowlist).
func calloutVariant(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "tip", "warning", "info":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "note"
	}
}

func renderCallout(c Callout) (string, error) {
	body, err := toHTML(c.Markdown)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = calloutTmpl.Execute(&buf, struct {
		Variant string
		Title   string
		Body    template.HTML
	}{calloutVariant(c.Variant), c.Title, template.HTML(body)}) //nolint:gosec // goldmark safe-mode
	if err != nil {
		return "", fmt.Errorf("render: callout block: %w", err)
	}
	return buf.String(), nil
}

// renderCode renders a verbatim code block (SPEC §6.12). The code is HTML-escaped
// and wrapped one <span> per line so a CSS counter can render line numbers as
// ::before pseudo-elements — those are excluded from text selection and the
// clipboard, so a copy of the <code> element's text stays clean whether or not
// numbers are shown. No highlighting, no third party.
func renderCode(c Code) (string, error) {
	text := strings.ReplaceAll(c.Text, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	var lines strings.Builder
	for i, ln := range strings.Split(text, "\n") {
		if i > 0 {
			lines.WriteByte('\n')
		}
		lines.WriteString(`<span class="pbcssg-code-line">`)
		lines.WriteString(template.HTMLEscapeString(ln))
		lines.WriteString(`</span>`)
	}
	var buf bytes.Buffer
	err := codeTmpl.Execute(&buf, struct {
		Filename    string
		Language    string
		Comment     string
		LineNumbers bool
		Lines       template.HTML
	}{c.Filename, c.Language, c.Comment, c.LineNumbers, template.HTML(lines.String())}) //nolint:gosec // lines are HTML-escaped above
	if err != nil {
		return "", fmt.Errorf("render: code block: %w", err)
	}
	return buf.String(), nil
}

// renderShare renders the privacy-preserving share block (SPEC §6.15). The Email
// mailto: is built from the page title (subject) and canonical URL (body), each
// query-escaped; copy-link and Mastodon read the live URL client-side via ShareJS.
// A share block with nothing enabled renders nothing.
func renderShare(s Share, opts Options) (string, error) {
	if !s.CopyLink && !s.Email && !s.Mastodon && s.RSS == "" {
		return "", nil
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = "Share"
	}
	var mailto template.URL
	if s.Email {
		m := "mailto:?subject=" + url.QueryEscape(opts.Title)
		if opts.CanonicalURL != "" {
			m += "&body=" + url.QueryEscape(opts.CanonicalURL)
		}
		mailto = template.URL(m) //nolint:gosec // mailto: built from controlled, query-escaped parts
	}
	var buf bytes.Buffer
	err := shareTmpl.Execute(&buf, struct {
		Title                     string
		CopyLink, Email, Mastodon bool
		MailtoHref                template.URL
		RSS                       string
	}{title, s.CopyLink, s.Email, s.Mastodon, mailto, s.RSS})
	if err != nil {
		return "", fmt.Errorf("render: share block: %w", err)
	}
	return buf.String(), nil
}

// commentsTmpl is the on-page mount point the self-hosted comments widget fills in.
// data-pbc-comments carries the host page's path so the widget's reads and posts
// address the same page (§7.3). It sits in a data-* attribute (not a URL context), so
// html/template attribute-escapes it without URL-mangling a normal /path. The heading
// and note are the no-JavaScript fallback; the widget clears and rebuilds the section
// on load, so on a live page they are replaced before the reader sees them.
var commentsTmpl = template.Must(template.New("comments").Parse(
	`<section class="pbc-comments" data-pbc-comments="{{.Path}}">` +
		`<h2 class="pbc-comments-title">Comments</h2>` +
		`<p class="pbc-comments-note">Comments require JavaScript.</p>` +
		"</section>\n"))

// renderComments renders the comments block's placeholder (SPEC §7.3): a semantic
// <section> keyed by the host page's path. The block always renders this mount point;
// whether the page also links the live widget script/stylesheet is controlled
// separately by Options.Comments (set by the build, not the editor preview).
func renderComments(opts Options) (string, error) {
	var buf bytes.Buffer
	if err := commentsTmpl.Execute(&buf, struct{ Path string }{opts.HostPath}); err != nil {
		return "", fmt.Errorf("render: comments block: %w", err)
	}
	return buf.String(), nil
}

// GalleryColumnsDefault is the grid column count used when a gallery leaves it unset.
const GalleryColumnsDefault = 3

// renderGallery renders an image grid + CSS lightbox (SPEC §6.14). index makes the
// per-item lightbox ids unique across the page. A gallery with no items renders
// nothing (tag mode that matched nothing, or an empty manual list).
func renderGallery(g Gallery, index int) (string, error) {
	if len(g.Items) == 0 {
		return "", nil
	}
	cols := g.Columns
	if cols < 2 || cols > 4 {
		cols = GalleryColumnsDefault
	}
	type itemView struct {
		ID, Src, Alt, Caption string
	}
	items := make([]itemView, len(g.Items))
	for i, it := range g.Items {
		items[i] = itemView{
			ID:      fmt.Sprintf("pbcssg-lb-%d-%d", index, i),
			Src:     it.Src,
			Alt:     it.Alt,
			Caption: it.Caption,
		}
	}
	var buf bytes.Buffer
	err := galleryTmpl.Execute(&buf, struct {
		Columns int
		Items   []itemView
	}{cols, items})
	if err != nil {
		return "", fmt.Errorf("render: gallery block: %w", err)
	}
	return buf.String(), nil
}

// renderTOC emits the table-of-contents placeholder (SPEC §6.12). Depth is clamped
// to the anchored range (1..3, default 3 → h2..h4); the AnchorsAndTOC pass fills it.
func renderTOC(t TOC) (string, error) {
	depth := t.Depth
	if depth < 1 || depth > tocMaxDepth {
		depth = tocMaxDepth
	}
	var buf bytes.Buffer
	err := tocTmpl.Execute(&buf, struct {
		Depth int
		Title string
	}{depth, t.Title})
	if err != nil {
		return "", fmt.Errorf("render: toc block: %w", err)
	}
	return buf.String(), nil
}

// relatedCountDefault / relatedCountMax bound the related-posts list size (SPEC §6.13).
const (
	relatedCountDefault = 5
	relatedCountMax     = 10
)

// renderRelated renders the related-posts block (SPEC §6.13): other posts sharing the
// most tags with the current page, ranked by shared-tag overlap then recency, capped
// at Count. It excludes the current page, non-posts, and noindex/list-excluded pages.
// Tags are compared by slug so matching agrees with the tag-page URLs. Returns "" (the
// block is omitted) when nothing qualifies. Renders internal links only.
func renderRelated(rel Related, opts Options) (string, error) {
	want := map[string]bool{}
	for _, t := range opts.Tags {
		if s := TagSlug(t); s != "" {
			want[s] = true
		}
	}
	if len(want) == 0 {
		return "", nil // the current page has no tags to relate on
	}
	type scored struct {
		ref   PageRef
		score int
	}
	var cands []scored
	for _, p := range opts.PageIndex {
		if !p.IsPost || p.NoIndex || p.Exclude || p.Path == opts.HostPath {
			continue
		}
		score := 0
		for _, t := range p.Tags {
			if want[TagSlug(t)] {
				score++
			}
		}
		if score > 0 {
			cands = append(cands, scored{p, score})
		}
	}
	if len(cands) == 0 {
		return "", nil
	}
	// Rank: most shared tags first, then most recent, then path for a stable order.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if !cands[i].ref.Time.Equal(cands[j].ref.Time) {
			return cands[i].ref.Time.After(cands[j].ref.Time)
		}
		return cands[i].ref.Path < cands[j].ref.Path
	})
	n := rel.Count
	if n < 1 || n > relatedCountMax {
		n = relatedCountDefault
	}
	if len(cands) > n {
		cands = cands[:n]
	}

	title := strings.TrimSpace(rel.Title)
	if title == "" {
		title = "Related posts"
	}
	type item struct{ Path, Title, Date string }
	items := make([]item, len(cands))
	for i, c := range cands {
		items[i] = item{Path: c.ref.Path, Title: c.ref.Title, Date: c.ref.Date}
	}
	var buf bytes.Buffer
	err := relatedTmpl.Execute(&buf, struct {
		Title string
		Items []item
	}{title, items})
	if err != nil {
		return "", fmt.Errorf("render: related block: %w", err)
	}
	return buf.String(), nil
}

// renderDetails renders a disclosure / FAQ block (SPEC §6.12). The summary is plain
// text; the body is rendered from markdown to goldmark-safe HTML.
func renderDetails(d Details) (string, error) {
	body, err := toHTML(d.Markdown)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = detailsTmpl.Execute(&buf, struct {
		Summary string
		Body    template.HTML
		Open    bool
	}{d.Summary, template.HTML(body), d.Open}) //nolint:gosec // goldmark safe-mode
	if err != nil {
		return "", fmt.Errorf("render: details block: %w", err)
	}
	return buf.String(), nil
}

func renderCitation(c Citation) (string, error) {
	body, err := toHTML(c.Quote)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = citationTmpl.Execute(&buf, struct {
		Body   template.HTML
		Source string
		URL    string
	}{template.HTML(body), c.Source, c.URL}) //nolint:gosec // goldmark safe-mode
	if err != nil {
		return "", fmt.Errorf("render: citation block: %w", err)
	}
	return buf.String(), nil
}

// revealFallbackKey is used only when no page key is supplied (editor preview,
// scan, tests). It keeps the block working there — the payload still decodes on
// click — without any randomness, so render stays a pure function of its inputs.
// The build always supplies the page's stored key via Options.RevealKey, so the
// deterministic build never depends on this constant.
var revealFallbackKey = func() []byte {
	sum := sha256.Sum256([]byte("pbcssg/reveal/preview-fallback"))
	return sum[:]
}()

// renderReveal encrypts a reveal block's plaintext and renders the hidden markup.
// A non-empty Code switches to Mode B (the PBKDF2 code gate); otherwise Mode A
// (obfuscation). It encodes with the page key (Options.RevealKey) when supplied —
// the build path, which keeps output deterministic — falling back to a fixed key
// otherwise so the editor preview and privacy scan still render it. Neither the
// plaintext nor the Mode B code is ever emitted; only ciphertext reaches the
// output (SPEC §6.9).
// revealData is the reveal template's payload. Mode is "a" (obfuscation, ships Key),
// "b" (code gate, ships Salt/Iters), or "c" (members-only keyring unlock, ships the
// Wrapped DEK blobs — SPEC §6.9 / §6.10).
type revealData struct {
	Mode, Kind, Ciphertext, Nonce, Key, Salt, Label, NoScript string
	Iters, ID                                                 int
	Wrapped                                                   []reveal.WrappedDEK
}

// renderReveal renders a deferred-reveal block. groups (the block's authorized key-
// group aliases) select Mode C — a members-only reveal unlocked by a keyring group key
// (envelope encryption, reusing §6.10), which takes precedence over the Mode B code
// gate. With no groups it is Mode B (a code is set) or Mode A (obfuscation). The
// content is only ever emitted as ciphertext.
func renderReveal(rv Reveal, groups []string, index int, opts Options) (string, error) {
	label := strings.TrimSpace(rv.Label)
	if label == "" {
		label = "Reveal hidden content"
	}
	noscript := strings.TrimSpace(rv.NoScript)
	if noscript == "" {
		noscript = DefaultRevealNoScript
	}
	kind := RevealKind(rv.Kind)
	key := opts.RevealKey
	if len(key) == 0 {
		key = revealFallbackKey
	}

	// Mode C: members-only keyring unlock.
	if len(groups) > 0 {
		if opts.GatePreview {
			return revealGroupPreview(rv, groups, kind)
		}
		keks := make([][]byte, 0, len(groups))
		for _, alias := range groups {
			if kek := opts.GateKEKs[alias]; len(kek) > 0 {
				keks = append(keks, kek)
			}
		}
		if len(keks) == 0 {
			return "", fmt.Errorf("render: members-only reveal block %d authorizes no known key group (groups: %s)",
				index, strings.Join(groups, ", "))
		}
		enc, err := reveal.EncodeGate(key, index, rv.Content, keks)
		if err != nil {
			return "", fmt.Errorf("render: members-only reveal block %d: %w", index, err)
		}
		return execReveal(revealData{
			Mode: "c", Kind: kind, Ciphertext: enc.Ciphertext, Nonce: enc.Nonce,
			Wrapped: enc.Wrapped, Label: label, NoScript: noscript, ID: index,
		})
	}

	// Mode A/B.
	code := strings.TrimSpace(rv.Code)
	gated := code != ""
	var enc reveal.Encoded
	var err error
	if gated {
		enc, err = reveal.EncodeB(key, index, code, rv.Content)
	} else {
		enc, err = reveal.EncodeA(key, index, rv.Content)
	}
	if err != nil {
		return "", fmt.Errorf("render: reveal block: %w", err)
	}
	mode := "a"
	if gated {
		mode = "b"
	}
	return execReveal(revealData{
		Mode: mode, Kind: kind, Ciphertext: enc.Ciphertext, Nonce: enc.Nonce,
		Key: enc.Key, Salt: enc.Salt, Iters: enc.Iters, Label: label, NoScript: noscript, ID: index,
	})
}

func execReveal(d revealData) (string, error) {
	var buf bytes.Buffer
	if err := revealTmpl.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render: reveal block: %w", err)
	}
	return buf.String(), nil
}

// revealGroupPreview shows a members-only (Mode C) reveal visibly for the editor
// preview — the operator's own view, labeled with its groups — since the operator
// holds no keyring. Markdown content is already hardened HTML (PrepareReveal); other
// kinds are shown as escaped text.
func revealGroupPreview(rv Reveal, groups []string, kind string) (string, error) {
	var body template.HTML
	if kind == "markdown" {
		body = template.HTML(rv.Content) //nolint:gosec // goldmark-safe, build-hardened HTML
	} else {
		body = template.HTML("<p>" + template.HTMLEscapeString(rv.Content) + "</p>") //nolint:gosec // escaped above
	}
	var buf bytes.Buffer
	err := revealGroupPreviewTmpl.Execute(&buf, struct {
		Groups string
		Body   template.HTML
	}{Groups: strings.Join(groups, ", "), Body: body})
	if err != nil {
		return "", fmt.Errorf("render: members-only reveal preview: %w", err)
	}
	return buf.String(), nil
}

// RenderBlockInner renders a single gateable block to its inner HTML — the payload
// the build hardens, classifies, and then envelope-encrypts for group-gated content
// (SPEC §6.10). It covers exactly the IsGateable subset; any other type is an error
// (the build only calls it for blocks it has already checked). index is the block's
// position, used only for error context here.
func RenderBlockInner(b Block, index int, opts Options) (string, error) {
	switch b.Type {
	case "", "markdown":
		return toHTML(b.Markdown)
	case "callout":
		if b.Callout == nil {
			return "", fmt.Errorf("render: gated callout block %d has no data", index)
		}
		return renderCallout(*b.Callout)
	case "citation":
		if b.Citation == nil {
			return "", fmt.Errorf("render: gated citation block %d has no data", index)
		}
		return renderCitation(*b.Citation)
	case "image":
		if b.Image == nil {
			return "", fmt.Errorf("render: gated image block %d has no data", index)
		}
		return renderImage(*b.Image)
	case "media":
		if b.Media == nil {
			return "", fmt.Errorf("render: gated media block %d has no data", index)
		}
		return renderMedia(*b.Media)
	case "code":
		if b.Code == nil {
			return "", fmt.Errorf("render: gated code block %d has no data", index)
		}
		return renderCode(*b.Code)
	case "details":
		if b.Details == nil {
			return "", fmt.Errorf("render: gated details block %d has no data", index)
		}
		return renderDetails(*b.Details)
	case "gallery":
		if b.Gallery == nil {
			return "", fmt.Errorf("render: gated gallery block %d has no data", index)
		}
		return renderGallery(*b.Gallery, index)
	case "index":
		if b.Index == nil {
			return "", fmt.Errorf("render: gated index block %d has no data", index)
		}
		return renderIndex(*b.Index, opts)
	default:
		return "", fmt.Errorf("render: block type %q (block %d) is not gateable", b.Type, index)
	}
}

// gatePreviewTmpl renders a gated block visibly for the editor preview only (the
// operator's own view), wrapping the plaintext HTML with a label naming the groups
// so the author sees what visitors without a key will not. It never runs in a build.
var gatePreviewTmpl = template.Must(template.New("gate-preview").Parse(
	`<div class="pbcssg-gate-preview" data-pbcssg-gate-preview>` +
		`<p class="pbcssg-gate-preview-label">🔒 Gated — unlocks for: {{.Groups}}</p>` +
		`<div class="pbcssg-gate-preview-body">{{.Body}}</div></div>`))

// renderGate envelope-encrypts a gated block and renders the keyring-decoded markup
// (SPEC §6.10). It resolves the block's group aliases to KEKs from Options.GateKEKs,
// derives a per-block DEK from the page key, seals the block's hardened inner HTML
// under it, and wraps the DEK under each KEK. Neither the plaintext, the DEK, nor
// any KEK is emitted — only ciphertext and the unlabeled wrapped-DEK blobs.
//
// In the editor preview (Options.GatePreview) it instead shows the block visibly
// with a group label — the operator's own view, not the published output. In a
// build, a gated block whose aliases resolve to no known KEK is a hard error, so
// plaintext is never published because a group was mistyped or deleted.
func renderGate(g Gate, index int, opts Options) (string, error) {
	if opts.GatePreview {
		var buf bytes.Buffer
		err := gatePreviewTmpl.Execute(&buf, struct {
			Groups string
			Body   template.HTML
		}{Groups: strings.Join(g.Groups, ", "), Body: template.HTML(g.HTML)}) //nolint:gosec // hardened, goldmark-safe HTML (SPEC §6.10)
		if err != nil {
			return "", fmt.Errorf("render: gate preview: %w", err)
		}
		return buf.String(), nil
	}

	keks := make([][]byte, 0, len(g.Groups))
	for _, alias := range g.Groups {
		if kek := opts.GateKEKs[alias]; len(kek) > 0 {
			keks = append(keks, kek)
		}
	}
	if len(keks) == 0 {
		return "", fmt.Errorf("render: gated block %d authorizes no known key group (groups: %s)",
			index, strings.Join(g.Groups, ", "))
	}
	key := opts.RevealKey
	if len(key) == 0 {
		key = revealFallbackKey
	}
	enc, err := reveal.EncodeGate(key, index, g.HTML, keks)
	if err != nil {
		return "", fmt.Errorf("render: gate block %d: %w", index, err)
	}
	noscript := strings.TrimSpace(g.NoScript)
	if noscript == "" {
		noscript = DefaultGateNoScript
	}
	var buf bytes.Buffer
	err = gateTmpl.Execute(&buf, struct {
		Ciphertext string
		Nonce      string
		Wrapped    []reveal.WrappedDEK
		NoScript   string
	}{enc.Ciphertext, enc.Nonce, enc.Wrapped, noscript})
	if err != nil {
		return "", fmt.Errorf("render: gate block %d: %w", index, err)
	}
	return buf.String(), nil
}

// indexCap bounds how many pages an index block lists when no explicit limit is set.
const indexCap = 50

// renderIndex resolves and renders a route-based page list. It renders nothing
// unless the host page is marked as an index page (the gate).
func renderIndex(idx Index, opts Options) (string, error) {
	if !opts.IsIndexPage {
		return "", nil
	}
	base := strings.TrimRight(strings.TrimSpace(idx.Base), "/")
	if base == "" {
		base = strings.TrimRight(opts.HostPath, "/")
	}
	var items []PageRef
	for _, p := range opts.PageIndex {
		if p.Path == opts.HostPath || p.Exclude {
			continue // never list the host page itself or a manually-excluded page
		}
		if indexMatch(base, p.Path, idx.Depth) {
			items = append(items, p)
		}
	}
	sortPageRefs(items, idx.Sort)

	total := len(items)
	limit := idx.Limit
	if limit <= 0 {
		limit = indexCap
	}
	truncated := false
	if total > limit {
		items = items[:limit]
		truncated = true
	}

	var buf bytes.Buffer
	err := indexTmpl.Execute(&buf, struct {
		Title     string
		Detailed  bool
		Items     []PageRef
		Truncated bool
		Shown     int
		Total     int
	}{idx.Title, idx.Style == "detailed", items, truncated, len(items), total})
	if err != nil {
		return "", fmt.Errorf("render: index block: %w", err)
	}
	return buf.String(), nil
}

// indexMatch reports whether path is a descendant of base within depth levels
// (depth 1 = direct children; depth 0 = any depth).
func indexMatch(base, path string, depth int) bool {
	var rel string
	if base == "" || base == "/" {
		if path == "" || path == "/" {
			return false
		}
		rel = strings.TrimPrefix(path, "/")
	} else {
		if !strings.HasPrefix(path, base+"/") {
			return false
		}
		rel = path[len(base)+1:]
	}
	if rel == "" {
		return false
	}
	if depth > 0 && strings.Count(rel, "/")+1 > depth {
		return false
	}
	return true
}

// sortPageRefs orders index items by the block's sort mode (default: newest first).
func sortPageRefs(items []PageRef, mode string) {
	switch mode {
	case "date-asc":
		sort.SliceStable(items, func(i, j int) bool { return items[i].Time.Before(items[j].Time) })
	case "path":
		sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	case "title":
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
	default: // date-desc
		sort.SliceStable(items, func(i, j int) bool { return items[i].Time.After(items[j].Time) })
	}
}

func layout(opts Options, inner string) ([]byte, error) {
	lang := opts.Lang
	if lang == "" {
		lang = "en"
	}
	var chips []tagChip
	for _, t := range opts.Tags {
		if slug := TagSlug(t); slug != "" {
			chips = append(chips, tagChip{Name: strings.TrimSpace(t), Href: "/tags/" + slug + "/"})
		}
	}
	// A page carrying a comments block links the live, self-hosted widget from its
	// fixed same-origin paths (§7.3); the placeholder itself renders regardless.
	var commentsJSHref, commentsCSSHref string
	if opts.Comments {
		commentsJSHref, commentsCSSHref = CommentsJSPath, CommentsCSSPath
	}
	var buf bytes.Buffer
	err := base.Execute(&buf, pageData{
		Lang:            lang,
		Title:           opts.Title,
		SiteName:        opts.SiteName,
		Description:     opts.Description,
		CanonicalURL:    opts.CanonicalURL,
		OpenGraph:       opts.OpenGraph,
		OGImage:         opts.OGImage,
		NoIndex:         opts.NoIndex,
		Tags:            chips,
		Nav:             opts.Nav,
		FooterNav:       opts.FooterNav,
		Year:            opts.Year,
		FeedLinks:       opts.FeedLinks,
		BuildNumber:     opts.BuildNumber,
		Content:         template.HTML(inner), //nolint:gosec // goldmark safe-mode + trusted templates
		Brand:           opts.Brand,
		Search:          opts.Search,
		CSSHref:         opts.CSSHref,
		SearchJSHref:    opts.SearchJSHref,
		ThemeJSHref:     opts.ThemeJSHref,
		RevealJSHref:    opts.RevealJSHref,
		GateJSHref:      opts.GateJSHref,
		CodeCopyJSHref:  opts.CodeCopyJSHref,
		ShareJSHref:     opts.ShareJSHref,
		CommentsJSHref:  commentsJSHref,
		CommentsCSSHref: commentsCSSHref,
		SplashAlias:     opts.SplashAlias,
		Favicon:         opts.Favicon,
	})
	if err != nil {
		return nil, fmt.Errorf("render: layout: %w", err)
	}
	return buf.Bytes(), nil
}

// wordsPerMinute is the fixed reading-speed constant for the reading-time estimate
// (SPEC §6.13). 200 wpm is a common, conservative average for adult silent reading of
// prose; the estimate is deliberately approximate ("~N min read").
const wordsPerMinute = 200

// readingTimeMeta renders the "~N min read" line for a post, tagged with a marker the
// AnchorsAndTOC pass uses to relocate it after the first heading. The estimate counts
// the visible authored words (body + block text), excluding hidden reveal/gate content.
func readingTimeMeta(c Content) string {
	words := countWords(c.Body)
	for _, b := range c.Blocks {
		switch b.Type {
		case "", "markdown":
			words += countWords(b.Markdown)
		case "callout":
			if b.Callout != nil {
				words += countWords(b.Callout.Title) + countWords(b.Callout.Markdown)
			}
		case "citation":
			if b.Citation != nil {
				words += countWords(b.Citation.Quote) + countWords(b.Citation.Source)
			}
		case "code":
			if b.Code != nil {
				words += countWords(b.Code.Text) + countWords(b.Code.Comment)
			}
		case "details":
			if b.Details != nil {
				words += countWords(b.Details.Summary) + countWords(b.Details.Markdown)
			}
			// reveal/gate content is hidden by default, so it is excluded from the estimate.
		}
	}
	minutes := (words + wordsPerMinute - 1) / wordsPerMinute // ceil
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf(`<p class="pbcssg-post-meta" data-pbcssg-readingtime>~%d min read</p>`+"\n", minutes)
}

// countWords counts whitespace-separated tokens.
func countWords(s string) int { return len(strings.Fields(s)) }

func toHTML(markdown string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return "", fmt.Errorf("render: markdown: %w", err)
	}
	return buf.String(), nil
}

// TagSlug normalizes a tag into a URL slug (lowercase, alphanumerics, single
// dashes). It is the shared source of truth for tag URLs so chips and tag pages
// agree.
func TagSlug(tag string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(tag)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// TagLink is one entry on the tags index: a tag, its page URL, and how many
// pages carry it.
type TagLink struct {
	Name  string
	Href  string
	Count int
}

// PageLink is a linked page (title + URL) listed on a tag page.
type PageLink struct {
	Title string
	Href  string
}

var (
	tagsIndexTmpl  = template.Must(template.New("tagsindex").Parse(tagsIndexTemplate))
	tagPageTmpl    = template.Must(template.New("tagpage").Parse(tagPageTemplate))
	feedsIndexTmpl = template.Must(template.New("feedsindex").Parse(feedsIndexTemplate))
)

const tagsIndexTemplate = `<h1>Tags</h1>
{{- if .}}
<ul class="pbcssg-tag-list">
{{- range .}}
<li><a href="{{.Href}}">{{.Name}}</a> <span class="pbcssg-tag-count">{{.Count}}</span></li>
{{- end}}
</ul>
{{- else}}
<p>No tags yet.</p>
{{- end}}
`

const tagPageTemplate = `<p class="pbcssg-back"><a href="/tags/">← All tags</a></p>
<h1>Tag: {{.Name}}</h1>
<ul class="pbcssg-page-list">
{{- range .Pages}}
<li><a href="{{.Href}}">{{.Title}}</a></li>
{{- end}}
</ul>
`

const feedsIndexTemplate = `<h1>Feeds</h1>
<p>Subscribe to any of these feeds in your reader to follow new content. Each is offered in both RSS and Atom.</p>
<ul class="pbcssg-feed-list">
{{- range .}}
<li><span class="pbcssg-feed-title">{{.Title}}</span> <a href="{{.RSSHref}}">RSS</a> · <a href="{{.AtomHref}}">Atom</a></li>
{{- end}}
</ul>
`

// FeedsIndex renders the browsable /feeds/ page listing the operator's syndication
// feeds that are marked to be listed (§6.5).
func FeedsIndex(feeds []FeedInfo, opts Options) ([]byte, error) {
	var body bytes.Buffer
	if err := feedsIndexTmpl.Execute(&body, feeds); err != nil {
		return nil, fmt.Errorf("render: feeds index: %w", err)
	}
	return layout(opts, body.String())
}

// ExtRefSlot is the placeholder element the base layout emits immediately before
// the footer. The build replaces it with the external-references badge once a
// page's references have been classified (a two-phase step: classification is
// only known after render), or removes it for a page with no external references.
// It is an element rather than an HTML comment because html/template strips
// comments from output; it is matched in the post-hygiene HTML, so its value must
// equal what html.Render emits for it (a single quoted attribute is stable).
const ExtRefSlot = `<div data-pbcssg-extref="1"></div>`

// ExtRef is one external domain a page references, with its pbc-classification
// grade and the reasons behind it — the same per-domain privacy picture the editor
// shows live, now surfaced on the built page (§5.3 informs, §5.7 records).
type ExtRef struct {
	Domain    string   // registrable domain, e.g. "youtube.com"
	Grade     string   // letter A–F, or "?" (Unclassified)
	GradeName string   // human name: Clean…Invasive / Unclassified
	Count     int      // number of references to this domain on the page
	Reasons   []string // classifier reasons (why this grade)
}

const extRefListTemplate = `<aside class="pbcssg-extref" aria-labelledby="pbcssg-extref-heading">
<h2 id="pbcssg-extref-heading" class="pbcssg-extref-heading">External references</h2>
<ul class="pbcssg-extref-list">
{{- range .}}
<li class="pbcssg-extref-item"><span class="pbcssg-grade {{.Class}}" title="Privacy grade {{.Grade}}">{{.Grade}}</span> <code>{{.Domain}}</code> <span class="pbcssg-extref-name">{{.GradeName}}{{if gt .Count 1}} · {{.Count}} refs{{end}}</span>
{{- range .Reasons}}<br><small class="pbcssg-extref-reason">{{.}}</small>{{- end}}</li>
{{- end}}
</ul>
<p class="pbcssg-extref-more"><a href="/classification">How we rate these →</a></p>
</aside>`

var extRefListTmpl = template.Must(template.New("extreflist").Parse(extRefListTemplate))

// ExternalRefList renders the per-domain external-references listing injected
// before the footer, mirroring the editor's badge list. The refs should be
// pre-sorted (the build orders them worst-grade-first). The result is trusted
// internal markup with no external references of its own, so it is injected after
// the link scan and never affects the page's own classification. Returns "" for an
// empty list so a fully self-hosted page shows nothing.
func ExternalRefList(refs []ExtRef) string {
	if len(refs) == 0 {
		return ""
	}
	type view struct {
		Domain, Grade, GradeName, Class string
		Count                           int
		Reasons                         []string
	}
	views := make([]view, 0, len(refs))
	for _, r := range refs {
		views = append(views, view{
			Domain: r.Domain, Grade: r.Grade, GradeName: r.GradeName,
			Class: extRefGradeClass(r.Grade), Count: r.Count, Reasons: r.Reasons,
		})
	}
	var b bytes.Buffer
	if err := extRefListTmpl.Execute(&b, views); err != nil {
		return "" // a template error drops the listing rather than breaking the page
	}
	return b.String()
}

// extRefGradeClass maps a grade letter to a CSS-safe class token ("?" is not a
// valid selector, so it becomes pbcssg-grade-unknown). Mirrors the editor.
func extRefGradeClass(letter string) string {
	switch letter {
	case "A", "B", "C", "D", "E", "F":
		return "pbcssg-grade-" + strings.ToLower(letter)
	default:
		return "pbcssg-grade-unknown"
	}
}

// --- classification report page (§5.7) ---

// ClassifyGrade is one grade of the report's rating-scale legend (letter + name +
// shape glyph), supplied by the build from pbc-classification so the scale can't
// drift from the actual library.
type ClassifyGrade struct{ Letter, Name, Icon string }

// ClassifyCount is a per-grade tally for the report's summary line.
type ClassifyCount struct {
	Letter string
	Count  int
}

// ClassifyReport is the data for the built /classification report page. The lite
// form (Details=false) explains the rating system, links the module, and carries
// the disclaimer; the full form adds the dataset summary, the per-domain
// "Classifications used" listing, and a link to the published domains.json.
type ClassifyReport struct {
	Details     bool
	DataRepoURL string          // optional operator dataset repo (blank = omit)
	JSONHref    string          // published domains.json path (Details only)
	Legend      []ClassifyGrade // the rating scale, worst→best or best→worst as supplied
	Total       int             // dataset size (Details only)
	Counts      []ClassifyCount // per-grade tallies (Details only)
	Entries     []ExtRef        // dataset classifications, pre-sorted (Details only)
}

// moduleRepoURL is the canonical public home of the rating system (the
// pbc-classification module). Static by design; the dataset repo is configurable.
const moduleRepoURL = "https://github.com/privatebychoice/pbc-classification"

const classifyReportTemplate = `<h1>How we rate external links</h1>
<p class="pbcssg-report-intro">Every link on this site that points to another website carries a small <strong>privacy badge</strong>. The grade — from <strong>A (Clean)</strong> to <strong>F (Invasive)</strong> — is computed by the open-source <a href="` + moduleRepoURL + `">pbc-classification</a> module from a few observable signals: third-party ad/tracking cookies, whether the site honours Global Privacy Control, the density of ads &amp; trackers and third-party scripts, browser fingerprinting, session replay, and whether it sells or shares personal data. A worse signal always dominates, and anything not verified is left <em>unknown</em> — it never improves a grade.</p>

<h2>The rating scale</h2>
<ul class="pbcssg-legend">
{{- range .Legend}}
<li><span class="pbcssg-grade {{.Class}}" aria-hidden="true">{{.Letter}}</span> <strong>{{.Name}}</strong></li>
{{- end}}
</ul>
<p class="pbcssg-report-links">The rating system is the open-source <a href="` + moduleRepoURL + `">pbc-classification</a> module.{{if .DataRepoURL}} The classification dataset we use is published at <a href="{{.DataRepoURL}}">its dataset repository</a>.{{end}}</p>

<h2>About these grades</h2>
<p class="pbcssg-report-disclaimer">These privacy grades are <strong>editorial opinions, not statements of fact</strong>. They are formed from publicly observable behaviour as of each entry's verification date, and can be incomplete, out of date, or wrong — sites change. They are provided <strong>as-is, without warranty</strong> of any kind. If you operate a site listed here and believe a classification is inaccurate, corrections are welcome.</p>
{{- if .Details}}

<h2>Classifications used</h2>
<p class="pbcssg-report-summary">Our published dataset classifies <strong>{{.Total}}</strong> domain{{if ne .Total 1}}s{{end}}{{if .Counts}} —{{range $i, $c := .Counts}}{{if $i}} ·{{end}} {{$c.Count}} {{$c.Letter}}{{end}}{{end}}. The raw data is available as <a href="{{.JSONHref}}"><code>{{.JSONHref}}</code></a>.</p>
<ul class="pbcssg-extref-list pbcssg-report-list">
{{- range .Entries}}
<li class="pbcssg-extref-item"><span class="pbcssg-grade {{.Class}}" title="Privacy grade {{.Grade}}">{{.Grade}}</span> <code>{{.Domain}}</code> <span class="pbcssg-extref-name">{{.GradeName}}</span>
{{- range .Reasons}}<br><small class="pbcssg-extref-reason">{{.}}</small>{{- end}}</li>
{{- end}}
</ul>
{{- end}}
`

var classifyReportTmpl = template.Must(template.New("classifyreport").Parse(classifyReportTemplate))

// ClassificationReport renders the built /classification page.
func ClassificationReport(rep ClassifyReport, opts Options) ([]byte, error) {
	type legendView struct{ Letter, Name, Class string }
	type countView struct {
		Letter string
		Count  int
	}
	type entryView struct {
		Domain, Grade, GradeName, Class string
		Reasons                         []string
	}
	data := struct {
		Details     bool
		DataRepoURL string
		JSONHref    string
		Total       int
		Legend      []legendView
		Counts      []countView
		Entries     []entryView
	}{Details: rep.Details, DataRepoURL: rep.DataRepoURL, JSONHref: rep.JSONHref, Total: rep.Total}
	for _, g := range rep.Legend {
		data.Legend = append(data.Legend, legendView{g.Letter, g.Name, extRefGradeClass(g.Letter)})
	}
	for _, c := range rep.Counts {
		data.Counts = append(data.Counts, countView{c.Letter, c.Count})
	}
	for _, e := range rep.Entries {
		data.Entries = append(data.Entries, entryView{e.Domain, e.Grade, e.GradeName, extRefGradeClass(e.Grade), e.Reasons})
	}
	var body bytes.Buffer
	if err := classifyReportTmpl.Execute(&body, data); err != nil {
		return nil, fmt.Errorf("render: classification report: %w", err)
	}
	return layout(opts, body.String())
}

// TagsIndex renders the /tags/ page listing every tag.
func TagsIndex(tags []TagLink, opts Options) ([]byte, error) {
	var body bytes.Buffer
	if err := tagsIndexTmpl.Execute(&body, tags); err != nil {
		return nil, fmt.Errorf("render: tags index: %w", err)
	}
	return layout(opts, body.String())
}

// TagPage renders the /tags/<slug>/ page listing the pages carrying one tag.
func TagPage(name string, pages []PageLink, opts Options) ([]byte, error) {
	var body bytes.Buffer
	err := tagPageTmpl.Execute(&body, struct {
		Name  string
		Pages []PageLink
	}{name, pages})
	if err != nil {
		return nil, fmt.Errorf("render: tag page: %w", err)
	}
	return layout(opts, body.String())
}
