package render

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	gohtml "html"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderHTML(t *testing.T, content string, opts Options) string {
	t.Helper()
	r, err := Render(content, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(r.HTML)
}

func TestRenderBodyAndMetadata(t *testing.T) {
	content := `{"body":"# Hi\n\nA [link](https://ext.example) and ![pic](https://cdn.example/i.png)."}`
	s := renderHTML(t, content, Options{Title: "My Page", SiteName: "TUL", BuildNumber: "42"})
	for _, want := range []string{
		`<html lang="en">`,
		"<title>My Page · TUL</title>",
		">Hi</h1>", // h1 now carries a goldmark auto heading ID (SPEC §6.12)
		`href="https://ext.example"`,
		`src="https://cdn.example/i.png"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n%s", want, s)
		}
	}
}

func TestRenderGFMExtensions(t *testing.T) {
	// GFM (tables, strikethrough, task lists, autolinks) + footnotes are enabled in
	// safe mode (SPEC §6.12). Verify each surface renders without raw-HTML passthrough.
	md := "" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n\n" + // table
		"~~gone~~\n\n" + // strikethrough
		"- [x] done\n- [ ] todo\n\n" + // task list
		"See https://ext.example/page here.\n\n" + // autolink (Linkify)
		"A claim.[^1]\n\n[^1]: The footnote." // footnote
	content, err := json.Marshal(map[string]string{"body": md})
	if err != nil {
		t.Fatal(err)
	}
	s := renderHTML(t, string(content), Options{Title: "T"})
	for _, want := range []string{
		"<table>", "<th>A</th>", "<td>1</td>", // table
		"<del>gone</del>",                        // strikethrough
		`type="checkbox"`, `checked`, `disabled`, // task list (inert)
		`<a href="https://ext.example/page">`, // autolink -> real anchor (hardened downstream)
		`id="fnref:1"`, `id="fn:1"`,           // footnote refs/defs
	} {
		if !strings.Contains(s, want) {
			t.Errorf("GFM output missing %q:\n%s", want, s)
		}
	}
	// Safe mode is preserved: the task-list checkbox must stay disabled (no raw HTML).
	if strings.Contains(s, "<script") {
		t.Errorf("unexpected raw script in GFM output:\n%s", s)
	}
}

func TestRenderReadingTime(t *testing.T) {
	// ~250 words of body → 2 min at 200 wpm (ceil). Shown only for a post with the
	// setting on; the marker attr lets the build pass relocate it after the title.
	words := strings.Repeat("word ", 250)
	post := `{"body":"# Title\n\n` + strings.TrimSpace(words) + `","isPost":true}`

	on := renderHTML(t, post, Options{Title: "T", ShowReadingTime: true})
	if !strings.Contains(on, `<p class="pbcssg-post-meta" data-pbcssg-readingtime>~2 min read</p>`) {
		t.Errorf("post with reading-time on should show ~2 min read:\n%s", on)
	}
	// A post without the setting shows nothing.
	if off := renderHTML(t, post, Options{Title: "T"}); strings.Contains(off, "min read") {
		t.Errorf("reading time must be off unless the setting is on:\n%s", off)
	}
	// A non-post never shows it, even with the setting on.
	nonPost := `{"body":"# Title\n\nshort"}`
	if s := renderHTML(t, nonPost, Options{Title: "T", ShowReadingTime: true}); strings.Contains(s, "min read") {
		t.Errorf("non-post must not show reading time:\n%s", s)
	}
	// A tiny post rounds up to 1 min (never 0).
	tiny := renderHTML(t, `{"body":"# T\n\nhi","isPost":true}`, Options{Title: "T", ShowReadingTime: true})
	if !strings.Contains(tiny, "~1 min read") {
		t.Errorf("tiny post should be ~1 min read:\n%s", tiny)
	}
}

func TestRenderShareBlock(t *testing.T) {
	opts := Options{Title: "My Post", CanonicalURL: "https://ex.example/p"}
	content := `{"blocks":[{"type":"share","share":{"title":"Share this","copyLink":true,"email":true,"mastodon":true,"rss":"/feeds/blog.rss"}}]}`
	s := renderHTML(t, content, opts)
	for _, want := range []string{
		`<nav class="pbcssg-share" aria-label="Share this">`,
		`<p class="pbcssg-share-title">Share this</p>`,
		`data-pbcssg-share-copy`,
		`mailto:?subject=My`,                // page title as subject (query-escaped; + shown as &#43;)
		`body=https%3A%2F%2Fex.example%2Fp`, // canonical URL as body (query-escaped)
		`data-pbcssg-share-mastodon`,
		`aria-label="Your Mastodon instance"`,
		`href="/feeds/blog.rss" rel="alternate"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("share block missing %q:\n%s", want, s)
		}
	}

	// No CanonicalURL → mailto carries only the subject, no body.
	only := renderHTML(t, `{"blocks":[{"type":"share","share":{"email":true}}]}`, Options{Title: "T"})
	if strings.Contains(only, "body=") {
		t.Errorf("mailto must omit body when there is no canonical URL:\n%s", only)
	}

	// A share block with nothing enabled renders nothing.
	if s := renderHTML(t, `{"blocks":[{"type":"share","share":{}}]}`, opts); strings.Contains(s, "pbcssg-share") {
		t.Errorf("empty share block should render nothing:\n%s", s)
	}
}

func TestRenderCommentsBlock(t *testing.T) {
	content := `{"blocks":[{"type":"comments"}]}`

	// The placeholder always renders, keyed by the host page path, and carries the
	// no-JavaScript fallback the widget replaces on load.
	base := renderHTML(t, content, Options{Title: "T", HostPath: "/posts/hello"})
	for _, want := range []string{
		`<section class="pbc-comments" data-pbc-comments="/posts/hello">`,
		`<h2 class="pbc-comments-title">Comments</h2>`,
		`Comments require JavaScript.`,
	} {
		if !strings.Contains(base, want) {
			t.Errorf("comments placeholder missing %q:\n%s", want, base)
		}
	}
	// Options.Comments is off here, so the live widget assets are NOT linked.
	if strings.Contains(base, CommentsJSPath) || strings.Contains(base, CommentsCSSPath) {
		t.Errorf("widget assets must not be linked when Options.Comments is false:\n%s", base)
	}

	// With Options.Comments set (the build path) the page links the fixed, same-origin
	// widget script and stylesheet.
	linked := renderHTML(t, content, Options{Title: "T", HostPath: "/posts/hello", Comments: true})
	if !strings.Contains(linked, `<link rel="stylesheet" href="`+CommentsCSSPath+`">`) {
		t.Errorf("comments stylesheet link missing:\n%s", linked)
	}
	if !strings.Contains(linked, `<script src="`+CommentsJSPath+`" defer></script>`) {
		t.Errorf("comments script link missing:\n%s", linked)
	}

	// A page with no comments block never links the widget, even with the flag set.
	none := renderHTML(t, `{"body":"hi"}`, Options{Title: "T", Comments: false})
	if strings.Contains(none, "pbc-comments") || strings.Contains(none, CommentsJSPath) {
		t.Errorf("page without a comments block should not reference the widget:\n%s", none)
	}
}

func TestRenderGalleryBlock(t *testing.T) {
	content := `{"blocks":[{"type":"gallery","gallery":{"mode":"manual","columns":2,"items":[` +
		`{"src":"/media/a.png","alt":"A cat","caption":"My cat"},` +
		`{"src":"/media/b.jpg","alt":"A dog"}]}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	for _, want := range []string{
		`<div class="pbcssg-gallery pbcssg-gallery--cols-2">`,
		`<img src="/media/a.png" alt="A cat" loading="lazy">`,
		`<figcaption class="pbcssg-gallery-caption">My cat</figcaption>`,
		`id="pbcssg-lb-0-0"`, `id="pbcssg-lb-0-1"`, // unique per-item lightbox ids
		`<a class="pbcssg-lightbox-backdrop" href="#pbcssg-lb-0-0-x"`, // close clears :target
		`<img class="pbcssg-lightbox-img" src="/media/a.png" alt="A cat">`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gallery missing %q:\n%s", want, s)
		}
	}

	// A gallery with no items renders nothing (e.g. a tag mode that matched nothing).
	if s := renderHTML(t, `{"blocks":[{"type":"gallery","gallery":{"mode":"tag","tag":"none"}}]}`, Options{Title: "T"}); strings.Contains(s, "pbcssg-gallery") {
		t.Errorf("empty gallery should render nothing:\n%s", s)
	}
}

func TestRenderRelatedBlock(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }
	idx := []PageRef{
		{Path: "/a", Title: "Post A", IsPost: true, Tags: []string{"go", "privacy"}, Time: d(3), Date: "2026-01-03"}, // score 2
		{Path: "/b", Title: "Post B", IsPost: true, Tags: []string{"go"}, Time: d(9), Date: "2026-01-09"},            // score 1, newer
		{Path: "/c", Title: "Post C", IsPost: true, Tags: []string{"privacy"}, Time: d(5), Date: "2026-01-05"},       // score 1, older
		{Path: "/current", Title: "Self", IsPost: true, Tags: []string{"go", "privacy"}, Time: d(10)},                // excluded: self
		{Path: "/d", Title: "Page D", IsPost: false, Tags: []string{"go"}, Time: d(8)},                               // excluded: not a post
		{Path: "/e", Title: "Post E", IsPost: true, NoIndex: true, Tags: []string{"go"}, Time: d(8)},                 // excluded: noindex
		{Path: "/f", Title: "Post F", IsPost: true, Exclude: true, Tags: []string{"go"}, Time: d(8)},                 // excluded: list-excluded
		{Path: "/g", Title: "Post G", IsPost: true, Tags: []string{"rust"}, Time: d(8)},                              // excluded: no shared tag
	}
	opts := Options{Title: "T", HostPath: "/current", Tags: []string{"go", "privacy"}, PageIndex: idx}
	s := renderHTML(t, `{"blocks":[{"type":"related","related":{"count":5}}]}`, opts)

	for _, gone := range []string{`href="/current"`, `href="/d"`, `href="/e"`, `href="/f"`, `href="/g"`, "Self", "Page D", "Post E", "Post F", "Post G"} {
		if strings.Contains(s, gone) {
			t.Errorf("related list must exclude %q:\n%s", gone, s)
		}
	}
	// Ranking: A (2 shared) before B and C (1 each); B (newer) before C.
	ai := strings.Index(s, `href="/a"`)
	bi := strings.Index(s, `href="/b"`)
	ci := strings.Index(s, `href="/c"`)
	if ai < 0 || bi < 0 || ci < 0 || !(ai < bi && bi < ci) {
		t.Errorf("expected order A<B<C (overlap then recency): a=%d b=%d c=%d\n%s", ai, bi, ci, s)
	}
	if !strings.Contains(s, `<p class="pbcssg-related-title">Related posts</p>`) {
		t.Errorf("default related title missing:\n%s", s)
	}

	// Count cap.
	capped := renderHTML(t, `{"blocks":[{"type":"related","related":{"count":1}}]}`, opts)
	if strings.Contains(capped, `href="/b"`) || strings.Contains(capped, `href="/c"`) {
		t.Errorf("count=1 should list only the top match:\n%s", capped)
	}

	// Omitted entirely when the current page has no tags, or nothing matches.
	if s := renderHTML(t, `{"blocks":[{"type":"related","related":{}}]}`, Options{Title: "T", HostPath: "/current", PageIndex: idx}); strings.Contains(s, "pbcssg-related") {
		t.Errorf("no current tags → related block omitted:\n%s", s)
	}
}

func TestRenderRevealModeC(t *testing.T) {
	kek := []byte(strings.Repeat("k", 32))
	pageKey := []byte(strings.Repeat("p", 32))
	content := `{"blocks":[{"type":"reveal","reveal":{"content":"secret text","label":"Show","kind":"text"},"groups":["members-a"]}]}`

	// Build path: encrypted, keyring-unlock markup — no plaintext, no Mode A key/Mode B gate.
	s := renderHTML(t, content, Options{Title: "T", RevealKey: pageKey, GateKEKs: map[string][]byte{"members-a": kek}})
	if strings.Contains(s, "secret text") {
		t.Errorf("Mode C reveal leaked plaintext:\n%s", s)
	}
	for _, want := range []string{`data-group="1"`, `<span class="pbcssg-reveal-key" data-w=`} {
		if !strings.Contains(s, want) {
			t.Errorf("Mode C markup missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "data-key=") || strings.Contains(s, "data-gated=") {
		t.Errorf("Mode C must not ship a Mode A key or Mode B gate:\n%s", s)
	}

	// A group with no known KEK is a hard render error (plaintext never published).
	if _, err := Render(content, Options{Title: "T", RevealKey: pageKey}); err == nil {
		t.Errorf("Mode C reveal with no resolvable KEK should error, not publish")
	}

	// Editor preview shows the content visibly with a members-only label.
	p := renderHTML(t, content, Options{Title: "T", GatePreview: true})
	if !strings.Contains(p, "Members-only reveal") || !strings.Contains(p, "secret text") {
		t.Errorf("Mode C preview should label + show the content:\n%s", p)
	}

	// The reveal script carries the Mode C keyring-unlock logic.
	for _, want := range []string{"data-group", "pbcssg-keyring", "revealGroup"} {
		if !strings.Contains(RevealJS, want) {
			t.Errorf("RevealJS missing Mode C piece %q", want)
		}
	}
}

func TestRenderNoIndex(t *testing.T) {
	// A page marked noindex in its content emits the robots meta; the directive
	// comes from Content, so it applies in both the build and the editor preview.
	if s := renderHTML(t, `{"body":"# Hidden","noIndex":true}`, Options{Title: "H"}); !strings.Contains(s, `<meta name="robots" content="noindex">`) {
		t.Errorf("noindex page should emit the robots meta:\n%s", s)
	}
	// A normal page does not.
	if s := renderHTML(t, `{"body":"# Public"}`, Options{Title: "P"}); strings.Contains(s, "noindex") {
		t.Errorf("indexable page must not emit a noindex meta:\n%s", s)
	}
}

func TestRenderBlocks(t *testing.T) {
	content := `{"body":"lead","blocks":[{"type":"markdown","markdown":"**bold**"},{"markdown":"_ital_"}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	if !strings.Contains(s, "<strong>bold</strong>") || !strings.Contains(s, "<em>ital</em>") {
		t.Errorf("blocks not rendered:\n%s", s)
	}
}

func TestRenderIsSafeByDefault(t *testing.T) {
	s := renderHTML(t, `{"body":"<script>alert(1)</script>\n\n[x](javascript:alert(2))"}`, Options{Title: "T"})
	if strings.Contains(s, "<script>") {
		t.Errorf("raw <script> should not be emitted:\n%s", s)
	}
	if strings.Contains(s, "javascript:") {
		t.Errorf("javascript: URL should be neutralized:\n%s", s)
	}
}

func TestRenderEscapesMetadata(t *testing.T) {
	s := renderHTML(t, `{"body":"x"}`, Options{Title: `A & B <c>`})
	if strings.Contains(s, "<title>A & B <c>") {
		t.Errorf("title must be HTML-escaped:\n%s", s)
	}
	if !strings.Contains(s, "A &amp; B") {
		t.Errorf("expected escaped title:\n%s", s)
	}
}

func TestRenderImageBlock(t *testing.T) {
	content := `{"blocks":[{"type":"image","image":{"src":"/media/abc.png","alt":"A cat","caption":"My cat"}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	if !strings.Contains(s, `<figure class="pbcssg-figure">`) {
		t.Errorf("figure wrapper missing:\n%s", s)
	}
	if !strings.Contains(s, `<img src="/media/abc.png" alt="A cat" loading="lazy">`) {
		t.Errorf("img markup wrong:\n%s", s)
	}
	if !strings.Contains(s, `<figcaption>My cat</figcaption>`) {
		t.Errorf("caption missing:\n%s", s)
	}

	// No caption -> no figcaption; alt still required markup.
	s2 := renderHTML(t, `{"blocks":[{"type":"image","image":{"src":"/media/x.jpg","alt":"x"}}]}`, Options{Title: "T"})
	if strings.Contains(s2, "<figcaption") {
		t.Errorf("empty caption should be omitted:\n%s", s2)
	}
	// Alt is escaped, not injected.
	s3 := renderHTML(t, `{"blocks":[{"type":"image","image":{"src":"/media/x.jpg","alt":"a\"><script>"}}]}`, Options{Title: "T"})
	if strings.Contains(s3, "<script>") {
		t.Errorf("alt must be escaped:\n%s", s3)
	}
}

func TestRenderImageLayoutClasses(t *testing.T) {
	// Float left + medium max-width → both modifier classes.
	s := renderHTML(t, `{"blocks":[{"type":"image","image":{"src":"/media/a.png","alt":"a","align":"left","maxWidth":"medium"}}]}`, Options{Title: "T"})
	if !strings.Contains(s, `<figure class="pbcssg-figure pbcssg-figure--left pbcssg-figure--md">`) {
		t.Errorf("float-left + medium classes missing:\n%s", s)
	}
	// Default image (no align/size) stays a plain figure — no modifier classes.
	s2 := renderHTML(t, `{"blocks":[{"type":"image","image":{"src":"/media/b.png","alt":"b"}}]}`, Options{Title: "T"})
	if !strings.Contains(s2, `<figure class="pbcssg-figure">`) || strings.Contains(s2, "pbcssg-figure--") {
		t.Errorf("default image should have no modifier classes:\n%s", s2)
	}
	// Unknown/malicious values never reach a class (allowlist only).
	s3 := renderHTML(t, `{"blocks":[{"type":"image","image":{"src":"/media/c.png","alt":"c","align":"left\" onload=x","maxWidth":"9999px"}}]}`, Options{Title: "T"})
	if strings.Contains(s3, "pbcssg-figure--") || strings.Contains(s3, "onload") {
		t.Errorf("unknown/injected layout values must not emit a class:\n%s", s3)
	}
}

func TestRenderCalloutBlock(t *testing.T) {
	content := `{"blocks":[{"type":"callout","callout":{"variant":"warning","title":"Heads up","markdown":"Be **careful**."}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	if !strings.Contains(s, `<aside class="pbcssg-callout pbcssg-callout-warning">`) {
		t.Errorf("callout wrapper/variant wrong:\n%s", s)
	}
	if !strings.Contains(s, `<p class="pbcssg-callout-title">Heads up</p>`) {
		t.Errorf("callout title missing:\n%s", s)
	}
	if !strings.Contains(s, "Be <strong>careful</strong>.") {
		t.Errorf("callout markdown body not rendered:\n%s", s)
	}

	// Unknown/empty variant falls back to note (fixed allowlist, no injection).
	s2 := renderHTML(t, `{"blocks":[{"type":"callout","callout":{"variant":"evil\" onload=x","markdown":"hi"}}]}`, Options{Title: "T"})
	if !strings.Contains(s2, "pbcssg-callout-note") || strings.Contains(s2, "onload") {
		t.Errorf("callout variant must be a safe allowlist value:\n%s", s2)
	}
}

func TestRenderCodeBlock(t *testing.T) {
	content := `{"blocks":[{"type":"code","code":{"text":"line1\nline2","filename":"main.go","language":"go","comment":"a note","lineNumbers":true}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	for _, want := range []string{
		`<figure class="pbcssg-code pbcssg-code--numbered">`,
		`<span class="pbcssg-code-filename">main.go</span>`,
		`<span class="pbcssg-code-lang">go</span>`,
		`<button type="button" class="pbcssg-code-copy" data-pbcssg-copy>Copy</button>`,
		`<code class="pbcssg-code-el"><span class="pbcssg-code-line">line1</span>` + "\n" + `<span class="pbcssg-code-line">line2</span></code>`,
		`<p class="pbcssg-code-comment">a note</p>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("code block missing %q:\n%s", want, s)
		}
	}

	// No caption fields → no caption bar; no line numbers → no --numbered modifier.
	s2 := renderHTML(t, `{"blocks":[{"type":"code","code":{"text":"x=1"}}]}`, Options{Title: "T"})
	if strings.Contains(s2, "pbcssg-code-caption") || strings.Contains(s2, "pbcssg-code--numbered") {
		t.Errorf("plain code block should have no caption/numbers:\n%s", s2)
	}

	// The code is HTML-escaped and never interpreted (no raw tags, no autolink).
	s3 := renderHTML(t, `{"blocks":[{"type":"code","code":{"text":"<script>alert(1)</script> see https://ex.example"}}]}`, Options{Title: "T"})
	if strings.Contains(s3, "<script>alert") {
		t.Errorf("code must be HTML-escaped:\n%s", s3)
	}
	if strings.Contains(s3, `<a href="https://ex.example"`) {
		t.Errorf("a URL inside a code block must not be autolinked:\n%s", s3)
	}
	if !strings.Contains(s3, "&lt;script&gt;") {
		t.Errorf("expected escaped code:\n%s", s3)
	}
}

func TestRenderDetailsBlock(t *testing.T) {
	content := `{"blocks":[{"type":"details","details":{"summary":"What is GPC?","markdown":"Global **Privacy** Control.","open":true}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	for _, want := range []string{
		`<details class="pbcssg-details" open>`,
		`<summary class="pbcssg-details-summary">What is GPC?</summary>`,
		"Global <strong>Privacy</strong> Control.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("details block missing %q:\n%s", want, s)
		}
	}

	// Not open by default → no open attribute; summary is required (defaulted in the
	// editor), body markdown still renders. The body is present in source (indexable).
	s2 := renderHTML(t, `{"blocks":[{"type":"details","details":{"summary":"Q","markdown":"answer"}}]}`, Options{Title: "T"})
	if strings.Contains(s2, "<details class=\"pbcssg-details\" open>") {
		t.Errorf("details must be closed by default:\n%s", s2)
	}
	if !strings.Contains(s2, "answer") {
		t.Errorf("details body must be in the page source (indexable):\n%s", s2)
	}

	// The summary is plain text (escaped), never interpreted as markup.
	s3 := renderHTML(t, `{"blocks":[{"type":"details","details":{"summary":"a<script>b","markdown":"x"}}]}`, Options{Title: "T"})
	if strings.Contains(s3, "<script>b") {
		t.Errorf("summary must be escaped:\n%s", s3)
	}
}

func TestRenderCitationBlock(t *testing.T) {
	content := `{"blocks":[{"type":"citation","citation":{"quote":"Privacy is **power**.","source":"Someone","url":"https://ex.example/q"}}]}`
	s := renderHTML(t, content, Options{Title: "T"})
	if !strings.Contains(s, `<figure class="pbcssg-citation">`) || !strings.Contains(s, "<blockquote>") {
		t.Errorf("citation structure missing:\n%s", s)
	}
	if !strings.Contains(s, "Privacy is <strong>power</strong>.") {
		t.Errorf("quote markdown not rendered:\n%s", s)
	}
	if !strings.Contains(s, "<cite>Someone</cite>") || !strings.Contains(s, `<a href="https://ex.example/q">`) {
		t.Errorf("source/link missing:\n%s", s)
	}

	// No source/url -> no figcaption.
	s2 := renderHTML(t, `{"blocks":[{"type":"citation","citation":{"quote":"Just a quote."}}]}`, Options{Title: "T"})
	if strings.Contains(s2, "<figcaption") {
		t.Errorf("empty attribution should omit figcaption:\n%s", s2)
	}
}

func TestRenderRevealBlock(t *testing.T) {
	secret := "agent@example.com"
	content := `{"blocks":[{"type":"reveal","reveal":{"content":"` + secret + `","label":"Reveal email","kind":"email"}}]}`
	key := []byte("0123456789abcdef0123456789abcdef")
	s := renderHTML(t, content, Options{Title: "T", RevealKey: key, RevealJSHref: "/assets/pbcssg-reveal.abc.js"})

	// The plaintext must never appear in the output — only ciphertext does.
	if strings.Contains(s, secret) {
		t.Errorf("reveal plaintext leaked into HTML:\n%s", s)
	}
	// Accessible, encrypted markup: a real button with aria-expanded, an aria-live
	// output, the data-* attributes, and the kind.
	for _, want := range []string{
		`data-pbcssg-reveal`,
		`data-kind="email"`,
		`data-ct="`, `data-iv="`, `data-key="`,
		`<button type="button" class="pbcssg-reveal-btn" aria-expanded="false">Reveal email</button>`,
		`<span class="pbcssg-reveal-out" aria-live="polite">`,
		`<noscript>`,
		`<script src="/assets/pbcssg-reveal.abc.js" defer></script>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("reveal markup missing %q:\n%s", want, s)
		}
	}

	// The shipped ciphertext must actually decrypt back to the plaintext with the
	// shipped key (round-trip through the same primitives the client uses).
	if got := decodeRevealFromHTML(t, s); got != secret {
		t.Errorf("reveal round-trip: got %q, want %q", got, secret)
	}

	// An empty label falls back to a real (non-empty) label so the a11y contract holds.
	s2 := renderHTML(t, `{"blocks":[{"type":"reveal","reveal":{"content":"x"}}]}`, Options{Title: "T", RevealKey: key})
	if strings.Contains(s2, `aria-expanded="false"></button>`) {
		t.Errorf("empty reveal label left the button unlabeled:\n%s", s2)
	}
}

func TestRenderRevealMarkdownKind(t *testing.T) {
	// The caller (build/preview) pre-renders markdown to HTML; render encrypts that
	// verbatim and tags kind=markdown so the client injects it with innerHTML.
	fragment := "<p>hi <strong>there</strong></p>"
	content := `{"blocks":[{"type":"reveal","reveal":{"content":` + jsonStr(fragment) + `,"label":"Show","kind":"markdown"}}]}`
	key := []byte("0123456789abcdef0123456789abcdef")
	s := renderHTML(t, content, Options{Title: "T", RevealKey: key})

	if strings.Contains(s, "there") { // the HTML payload must be encrypted, not inline
		t.Errorf("markdown reveal leaked payload:\n%s", s)
	}
	if !strings.Contains(s, `data-kind="markdown"`) {
		t.Errorf("markdown kind not set:\n%s", s)
	}
	if got := decodeRevealFromHTML(t, s); got != fragment {
		t.Errorf("markdown reveal round-trip: got %q, want %q", got, fragment)
	}
	// The decode script injects markdown-kind payloads with innerHTML.
	if !strings.Contains(RevealJS, "innerHTML") {
		t.Errorf("RevealJS must inject markdown via innerHTML")
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRenderRevealGated(t *testing.T) {
	secret := "vault code 4417"
	content := `{"blocks":[{"type":"reveal","reveal":{"content":"` + secret + `","label":"Members only","code":"hunter2"}}]}`
	key := []byte("0123456789abcdef0123456789abcdef")
	s := renderHTML(t, content, Options{Title: "T", RevealKey: key})

	// Neither the plaintext nor the code may appear in the output.
	if strings.Contains(s, secret) {
		t.Errorf("gated reveal leaked plaintext:\n%s", s)
	}
	if strings.Contains(s, "hunter2") {
		t.Errorf("gated reveal leaked the code:\n%s", s)
	}
	// Mode B ships salt + iters + gated flag and a labelled code prompt, and does
	// NOT ship a decode key.
	for _, want := range []string{
		`data-gated="1"`, `data-salt="`, `data-iters="600000"`,
		`<input class="pbcssg-reveal-code"`, `type="password"`,
		`<label class="pbcssg-reveal-code-label"`, `<button type="button" class="pbcssg-reveal-unlock">Unlock</button>`,
		`class="pbcssg-reveal-error"`, `role="alert"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gated reveal markup missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "data-key=") {
		t.Errorf("gated reveal must not ship a decode key:\n%s", s)
	}
	// The input, its label, and the error region must be wired by matching ids.
	if !strings.Contains(s, `for="pbcssg-reveal-code-0"`) || !strings.Contains(s, `id="pbcssg-reveal-code-0"`) ||
		!strings.Contains(s, `aria-describedby="pbcssg-reveal-error-0"`) {
		t.Errorf("gated reveal label/input/error ids not wired:\n%s", s)
	}
}

// revealAttrRE pulls a base64 data-* attribute value out of the rendered block.
var revealAttrRE = map[string]*regexp.Regexp{
	"ct":  regexp.MustCompile(`data-ct="([^"]*)"`),
	"iv":  regexp.MustCompile(`data-iv="([^"]*)"`),
	"key": regexp.MustCompile(`data-key="([^"]*)"`),
}

// decodeRevealFromHTML decrypts a rendered reveal block the way the client script
// does: pull the base64 ciphertext/nonce/key from the data-* attributes and
// AES-GCM-decrypt. It proves the shipped material actually round-trips.
func decodeRevealFromHTML(t *testing.T, html string) string {
	t.Helper()
	grab := func(name string) []byte {
		m := revealAttrRE[name].FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("reveal data-%s attribute not found", name)
		}
		// html/template encodes base64's '+' as &#43; in attribute context; the
		// browser decodes it via getAttribute, so undo it before base64-decoding.
		b, err := base64.StdEncoding.DecodeString(gohtml.UnescapeString(m[1]))
		if err != nil {
			t.Fatalf("decode data-%s: %v", name, err)
		}
		return b
	}
	block, err := aes.NewCipher(grab("key"))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := gcm.Open(nil, grab("iv"), grab("ct"), nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	return string(pt)
}

var gateAttrRE = map[string]*regexp.Regexp{
	"ct":  regexp.MustCompile(`data-ct="([^"]*)"`),
	"iv":  regexp.MustCompile(`data-iv="([^"]*)"`),
	"w":   regexp.MustCompile(`data-w="([^"]*)"`),
	"wiv": regexp.MustCompile(`data-wiv="([^"]*)"`),
}

// gateOpen AES-GCM-decrypts base64 ct/iv (unescaping html/template's &#43;) under key.
func gateOpen(t *testing.T, key []byte, ctAttr, ivAttr string) ([]byte, bool) {
	t.Helper()
	dec := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(gohtml.UnescapeString(s))
		if err != nil {
			t.Fatalf("base64: %v", err)
		}
		return b
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := gcm.Open(nil, dec(ivAttr), dec(ctAttr), nil)
	return pt, err == nil
}

// decodeGateFromHTML trial-unwraps a rendered gate block with kek, mirroring the
// client keyring: unwrap a wrapped-DEK, then decrypt the block content with the DEK.
func decodeGateFromHTML(t *testing.T, html string, kek []byte) (string, bool) {
	t.Helper()
	ct := gateAttrRE["ct"].FindStringSubmatch(html)
	iv := gateAttrRE["iv"].FindStringSubmatch(html)
	if ct == nil || iv == nil {
		t.Fatalf("gate data-ct/data-iv not found:\n%s", html)
	}
	ws := gateAttrRE["w"].FindAllStringSubmatch(html, -1)
	wivs := gateAttrRE["wiv"].FindAllStringSubmatch(html, -1)
	for i := range ws {
		dek, ok := gateOpen(t, kek, ws[i][1], wivs[i][1])
		if !ok {
			continue
		}
		pt, ok := gateOpen(t, dek, ct[1], iv[1])
		if !ok {
			t.Fatal("DEK unwrapped but content failed to decrypt")
		}
		return string(pt), true
	}
	return "", false
}

func TestRenderGateBlock(t *testing.T) {
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = 0x11
	}
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	inner := "<p>Members only: <strong>4417</strong></p>"
	c := Content{Blocks: []Block{{Type: "gate", Gate: &Gate{HTML: inner, Groups: []string{"members-a"}}}}}

	r, err := RenderContent(c, Options{
		Title:     "T",
		RevealKey: pageKey,
		GateKEKs:  map[string][]byte{"members-a": kek},
	})
	if err != nil {
		t.Fatalf("RenderContent: %v", err)
	}
	s := string(r.HTML)

	// The plaintext must be absent; only ciphertext + one unlabeled wrapped-DEK ship.
	if strings.Contains(s, "4417") || strings.Contains(s, "Members only") {
		t.Errorf("gate leaked plaintext:\n%s", s)
	}
	for _, want := range []string{`data-pbcssg-gate`, `data-ct="`, `data-iv="`,
		`class="pbcssg-gate-key"`, `data-w="`, `data-wiv="`,
		`<div class="pbcssg-gate-out" aria-live="polite">`, `<noscript>`} {
		if !strings.Contains(s, want) {
			t.Errorf("gate markup missing %q:\n%s", want, s)
		}
	}
	// No alias/group name leaks into the markup.
	if strings.Contains(s, "members-a") {
		t.Errorf("gate markup leaked a group alias:\n%s", s)
	}
	// The KEK trial-unwraps to the original inner HTML.
	if got, ok := decodeGateFromHTML(t, s, kek); !ok || got != inner {
		t.Errorf("gate round-trip: ok=%v got=%q want=%q", ok, got, inner)
	}
}

func TestRenderGatePreview(t *testing.T) {
	inner := "<p>Preview me</p>"
	c := Content{Blocks: []Block{{Type: "gate", Gate: &Gate{HTML: inner, Groups: []string{"members-a", "members-b"}}}}}
	// Preview mode (the editor's own view) shows the content with a group label and
	// does NOT encrypt — no ciphertext attributes.
	r, err := RenderContent(c, Options{Title: "T", GatePreview: true})
	if err != nil {
		t.Fatalf("RenderContent preview: %v", err)
	}
	s := string(r.HTML)
	if !strings.Contains(s, "Preview me") || !strings.Contains(s, "members-a, members-b") {
		t.Errorf("preview should show content + group label:\n%s", s)
	}
	if strings.Contains(s, "data-pbcssg-gate=") || strings.Contains(s, "data-ct=") {
		t.Errorf("preview must not emit encrypted gate markup:\n%s", s)
	}
}

func TestRenderGateUnknownGroupErrors(t *testing.T) {
	c := Content{Blocks: []Block{{Type: "gate", Gate: &Gate{HTML: "<p>x</p>", Groups: []string{"ghost"}}}}}
	// A build (not preview) where the block's group resolves to no KEK must error, so
	// plaintext is never published by accident.
	if _, err := RenderContent(c, Options{Title: "T", GateKEKs: map[string][]byte{}}); err == nil {
		t.Error("a gated block with no resolvable KEK should error in a build")
	}
}

func TestRenderErrors(t *testing.T) {
	if _, err := Render(`{not json`, Options{}); err == nil {
		t.Errorf("invalid JSON should error")
	}
	if _, err := Render(`{"blocks":[{"type":"mystery"}]}`, Options{}); err == nil {
		t.Errorf("unknown block type should error")
	}
	if _, err := Render(`{"blocks":[{"type":"youtube"}]}`, Options{}); err == nil {
		t.Errorf("youtube block without data should error")
	}
}

func TestRenderYouTubeCardStage1(t *testing.T) {
	content := `{"blocks":[{"type":"youtube","youtube":{"videoId":"abc123","name":"my-video","title":"How I degoogled"}}]}`
	r, err := Render(content, Options{Title: "Post"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(r.HTML)

	// The card links out and is explicit...
	for _, want := range []string{
		"External video · How I degoogled",
		`href="/external/youtube/my-video"`,
		"Open video page →",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("card missing %q:\n%s", want, s)
		}
	}
	// ...but contacts no third party on the host page.
	for _, bad := range []string{"<iframe", "youtube-nocookie", "youtube.com", "googlevideo"} {
		if strings.Contains(s, bad) {
			t.Errorf("stage-1 card must not reference %q:\n%s", bad, s)
		}
	}
	if len(r.YouTube) != 1 || r.YouTube[0].Name != "my-video" {
		t.Errorf("expected one youtube block returned, got %+v", r.YouTube)
	}
}

func TestExternalYouTubeStage2(t *testing.T) {
	yt := YouTube{
		VideoID: "abc123", Name: "my-video", Title: "How I degoogled",
		Transcript:       "So **today** we cover privacy.",
		DescriptionLinks: []string{"https://privacyguides.example", "https://gnu.example"},
	}
	out, err := ExternalYouTube(yt, Options{Title: yt.Title, BuildNumber: "5", FacadeJSHref: "/assets/pbcssg-youtube.deadbeef01.js"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<h1>How I degoogled</h1>",
		`data-video-id="abc123"`,
		"▶ Play — loads YouTube",
		"Pressing play loads youtube-nocookie.com",
		"<h2>Transcript</h2>",
		"<strong>today</strong>",
		`href="https://privacyguides.example"`,
		`<script src="/assets/pbcssg-youtube.deadbeef01.js" defer></script>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("external page missing %q:\n%s", want, s)
		}
	}
	// The embed itself must not be present until the user clicks play.
	if strings.Contains(s, "<iframe") {
		t.Errorf("external page must not embed an iframe before playback:\n%s", s)
	}
}

func TestSEOMetadata(t *testing.T) {
	s := renderHTML(t, `{"body":"hi"}`, Options{
		Title: "My Post", SiteName: "TUL",
		Description:  "A short summary.",
		CanonicalURL: "https://tul.example/blog/post",
		OpenGraph:    true,
	})
	for _, want := range []string{
		`<meta name="description" content="A short summary.">`,
		`<link rel="canonical" href="https://tul.example/blog/post">`,
		`<meta property="og:title" content="My Post">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:site_name" content="TUL">`,
		`<meta property="og:description" content="A short summary.">`,
		`<meta property="og:url" content="https://tul.example/blog/post">`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing SEO meta %q:\n%s", want, s)
		}
	}
	// Keywords are search-only — never emitted as a meta tag.
	if strings.Contains(s, `name="keywords"`) {
		t.Errorf("keywords should not be emitted as meta:\n%s", s)
	}

	// Open Graph off: no og: tags, but description/canonical still present.
	noOG := renderHTML(t, `{"body":"hi"}`, Options{Title: "T", Description: "d", CanonicalURL: "https://x/", OpenGraph: false})
	if strings.Contains(noOG, "og:") {
		t.Errorf("og: tags should be omitted when OpenGraph is off:\n%s", noOG)
	}
	if !strings.Contains(noOG, `name="description"`) || !strings.Contains(noOG, "canonical") {
		t.Errorf("description/canonical are independent of OpenGraph:\n%s", noOG)
	}

	// Nothing set → no description/canonical/keywords tags.
	bare := renderHTML(t, `{"body":"hi"}`, Options{Title: "T"})
	if strings.Contains(bare, `name="description"`) || strings.Contains(bare, `name="keywords"`) || strings.Contains(bare, "canonical") {
		t.Errorf("empty metadata should be omitted:\n%s", bare)
	}
}

func TestTagChipsAndPages(t *testing.T) {
	// Tag chips on a page link to the tag's slug page.
	s := renderHTML(t, `{"body":"hi"}`, Options{Title: "T", Tags: []string{"Self Hosting", "GPC"}})
	if !strings.Contains(s, `<a class="pbcssg-tag" href="/tags/self-hosting/">Self Hosting</a>`) {
		t.Errorf("tag chip/slug wrong:\n%s", s)
	}
	if !strings.Contains(s, `href="/tags/gpc/">GPC</a>`) {
		t.Errorf("second tag chip missing:\n%s", s)
	}

	// Tag index + tag page renderers.
	idx, err := TagsIndex([]TagLink{{Name: "GPC", Href: "/tags/gpc/", Count: 2}}, Options{Title: "Tags"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), `<a href="/tags/gpc/">GPC</a>`) {
		t.Errorf("tags index wrong:\n%s", idx)
	}
	page, err := TagPage("GPC", []PageLink{{Title: "A Post", Href: "/blog/a"}}, Options{Title: "Tag: GPC"})
	if err != nil {
		t.Fatal(err)
	}
	ps := string(page)
	if !strings.Contains(ps, "<h1>Tag: GPC</h1>") || !strings.Contains(ps, `<a href="/blog/a">A Post</a>`) || !strings.Contains(ps, `href="/tags/"`) {
		t.Errorf("tag page wrong:\n%s", ps)
	}
}

func TestTagSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Self Hosting": "self-hosting",
		"  GPC  ":      "gpc",
		"C++ & Rust!":  "c-rust",
		"---":          "",
	} {
		if got := TagSlug(in); got != want {
			t.Errorf("TagSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExternalYouTubeBackLink(t *testing.T) {
	yt := YouTube{VideoID: "abc", Name: "v", Title: "My Video"}
	// With a back target, a real (same-origin) back link is rendered.
	out, err := ExternalYouTube(yt, Options{Title: yt.Title, BackHref: "/blog/post", BackLabel: "My Post"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<a href="/blog/post">← Back to My Post</a>`) {
		t.Errorf("back link not rendered:\n%s", out)
	}
	// Without a back target, no back link (and no dangling "Back to").
	out2, _ := ExternalYouTube(yt, Options{Title: yt.Title})
	if strings.Contains(string(out2), "pbcssg-back") {
		t.Errorf("no back link should be rendered without BackHref:\n%s", out2)
	}
}

func TestSearchBoxHasLabelAndPlaceholder(t *testing.T) {
	s := renderHTML(t, `{"body":"x"}`, Options{Title: "T", Search: true})
	// A real, associated, VISIBLE label is the accessible name (best for
	// low-vision / magnifier users)...
	if !strings.Contains(s, `<label for="pbcssg-search-input" class="pbcssg-search-label">Search</label>`) {
		t.Errorf("search should have a visible associated label:\n%s", s)
	}
	// ...so the redundant aria-label is dropped (the visible label names it).
	if strings.Contains(s, "aria-label=") {
		t.Errorf("visible label makes aria-label redundant; it should be removed:\n%s", s)
	}
	// A placeholder remains as a supplementary in-box hint.
	if !strings.Contains(s, `placeholder="Search…"`) {
		t.Errorf("search input should have a placeholder:\n%s", s)
	}
}

func TestExternalYouTubeLabeledLinks(t *testing.T) {
	yt := YouTube{
		VideoID: "abc", Name: "v", Title: "T",
		DescriptionLinks: []string{
			"Test https://fool.com/",     // labeled
			"https://bare.example/",      // bare URL -> label is the URL
			"just some text with no url", // dropped
			"javascript:alert(1)",        // dropped (unsafe scheme)
		},
	}
	out, err := ExternalYouTube(yt, Options{Title: yt.Title})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `<a href="https://fool.com/">Test</a>`) {
		t.Errorf("labeled link not rendered:\n%s", s)
	}
	if !strings.Contains(s, `<a href="https://bare.example/">https://bare.example/</a>`) {
		t.Errorf("bare link should use the URL as its label:\n%s", s)
	}
	// Invalid entries never reach the template (no #ZgotmplZ sentinel).
	if strings.Contains(s, "ZgotmplZ") {
		t.Errorf("invalid link leaked to html/template:\n%s", s)
	}
	if strings.Contains(s, "javascript:") {
		t.Errorf("unsafe scheme should have been dropped:\n%s", s)
	}
}

func TestParseDescriptionLink(t *testing.T) {
	cases := []struct {
		in         string
		label, url string
		ok         bool
	}{
		{"Test https://fool.com/", "Test", "https://fool.com/", true},
		{"https://x.example/", "https://x.example/", "https://x.example/", true},
		{"Read the docs https://docs.example/x", "Read the docs", "https://docs.example/x", true},
		{"no url here", "", "", false},
		{"ftp://x.example", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		label, u, ok := ParseDescriptionLink(c.in)
		if ok != c.ok || label != c.label || u != c.url {
			t.Errorf("ParseDescriptionLink(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, label, u, ok, c.label, c.url, c.ok)
		}
	}
}

func TestFacadeJSReferrerPolicy(t *testing.T) {
	// The facade must send the origin (not strip the referrer entirely): YouTube
	// validates the embedding origin and returns "Error 153" with no-referrer.
	if strings.Contains(FacadeJS, "no-referrer") {
		t.Errorf("facade must not use referrerpolicy no-referrer (breaks the embed with Error 153)")
	}
	if !strings.Contains(FacadeJS, "strict-origin-when-cross-origin") {
		t.Errorf("facade should send only the origin via strict-origin-when-cross-origin")
	}
	if !strings.Contains(FacadeJS, "youtube-nocookie.com/embed/") {
		t.Errorf("facade should load the youtube-nocookie embed")
	}
}

func TestEmbedCardStage1(t *testing.T) {
	content := `{"blocks":[{"type":"embed","embed":{"provider":"PeerTube","name":"my-talk","title":"My Talk","embedUrl":"https://peertube.example/videos/embed/abc"}}]}`
	r, err := Render(content, Options{Title: "Post"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(r.HTML)
	// The card is explicit, links to the provider-slugged external page, and
	// contacts no third party on the host page.
	for _, want := range []string{
		"External embed · My Talk",
		`href="/external/peertube/my-talk"`,
		"embedded from peertube",
		"Open embed page →",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("embed card missing %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{"<iframe", "peertube.example/videos/embed"} {
		if strings.Contains(s, bad) {
			t.Errorf("stage-1 embed card must not reference %q:\n%s", bad, s)
		}
	}
	if len(r.Embed) != 1 || r.Embed[0].Name != "my-talk" {
		t.Errorf("expected one embed block returned, got %+v", r.Embed)
	}
}

func TestExternalEmbedStage2(t *testing.T) {
	e := Embed{
		Provider: "PeerTube", Name: "my-talk", Title: "My Talk",
		EmbedURL:         "https://peertube.example/videos/embed/abc",
		Transcript:       "Some **notes** here.",
		DescriptionLinks: []string{"Docs https://docs.example"},
	}
	out, err := ExternalEmbed(e, Options{Title: e.Title, BuildNumber: "5", FacadeJSHref: "/assets/pbcssg-youtube.deadbeef01.js"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<h1>My Talk</h1>",
		`class="pbcssg-embed-facade" data-embed-url="https://peertube.example/videos/embed/abc"`,
		"▶ Load — loads peertube",
		"frames peertube.example",
		"<strong>notes</strong>",
		`href="https://docs.example"`,
		`<script src="/assets/pbcssg-youtube.deadbeef01.js" defer></script>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("embed page missing %q:\n%s", want, s)
		}
	}
}

func TestFacadeJSHandlesEmbeds(t *testing.T) {
	// One shared facade script drives both the youtube and generic embed pages.
	for _, want := range []string{
		"pbcssg-youtube-facade",
		"data-video-id",
		"pbcssg-embed-facade",
		"data-embed-url",
	} {
		if !strings.Contains(FacadeJS, want) {
			t.Errorf("facade script missing %q", want)
		}
	}
}

func TestProviderLabelAndEmbedHost(t *testing.T) {
	if got := ProviderLabel(" Peer Tube "); got != "peer-tube" {
		t.Errorf("ProviderLabel = %q, want peer-tube", got)
	}
	if got := ProviderLabel(""); got != "embed" {
		t.Errorf("ProviderLabel(empty) = %q, want embed", got)
	}
	if got := EmbedHost("https://peertube.example/x"); got != "peertube.example" {
		t.Errorf("EmbedHost = %q, want peertube.example", got)
	}
}

func TestMediaBlockRendersNativePlayers(t *testing.T) {
	content := `{"blocks":[
		{"type":"media","media":{"kind":"video","src":"/media/aaa.mp4","poster":"/media/p.png","caption":"A clip"}},
		{"type":"media","media":{"kind":"audio","src":"/media/bbb.mp3"}}
	]}`
	r, err := Render(content, Options{Title: "P"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(r.HTML)
	for _, want := range []string{
		`<video class="pbcssg-media-el" controls preload="metadata" playsinline poster="/media/p.png" src="/media/aaa.mp4">`,
		"<figcaption>A clip</figcaption>",
		`<audio class="pbcssg-media-el" controls preload="metadata" src="/media/bbb.mp3">`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("media render missing %q:\n%s", want, s)
		}
	}
	// Self-hosted only — no third-party anything.
	if strings.Contains(s, "http://") || strings.Contains(s, "https://") {
		t.Errorf("self-hosted media must not reference an absolute URL:\n%s", s)
	}
}

func TestMediaKind(t *testing.T) {
	for in, want := range map[string]string{"audio": "audio", "AUDIO": "audio", "video": "video", "": "video", "x": "video"} {
		if got := MediaKind(in); got != want {
			t.Errorf("MediaKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNavBarRendered(t *testing.T) {
	opts := Options{Title: "Home", Nav: []NavLink{{Label: "Home", Href: "/"}, {Label: "Blog", Href: "/blog/"}}}
	r, err := Render(`{"body":"# Hi"}`, opts)
	if err != nil {
		t.Fatal(err)
	}
	s := string(r.HTML)
	for _, want := range []string{
		`<header class="pbcssg-header">`,
		`<nav class="pbcssg-nav" aria-label="Primary">`,
		`<a href="/">Home</a>`,
		`<a href="/blog/">Blog</a>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("nav render missing %q:\n%s", want, s)
		}
	}
	// No nav configured → no header at all (unless search is on).
	plain, _ := Render(`{"body":"# Hi"}`, Options{Title: "Home"})
	if strings.Contains(string(plain.HTML), "pbcssg-header") {
		t.Errorf("no header should render when nav and search are both off")
	}
}

func indexOpts(host string, isIndex bool) Options {
	tm := func(d int) time.Time { return time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC) }
	return Options{
		Title:       "Blog",
		HostPath:    host,
		IsIndexPage: isIndex,
		PageIndex: []PageRef{
			{Path: "/blog", Title: "Blog", IsIndex: true, Time: tm(1)},
			// Apple is itself an index page, but that no longer hides it from a
			// parent listing — the "is index" and "exclude" flags are decoupled.
			{Path: "/blog/apple", Title: "Apple", Summary: "about apples", Date: "2026-07-10", IsIndex: true, Time: tm(10)},
			{Path: "/blog/zebra", Title: "Zebra", Summary: "about zebras", Date: "2026-07-20", Time: tm(20)},
			{Path: "/blog/deep/sub", Title: "Deep Sub", Time: tm(15)},
			{Path: "/blog/hidden", Title: "Hidden", Exclude: true, Time: tm(25)},
			{Path: "/about", Title: "About", Time: tm(5)},
		},
	}
}

func TestIndexBlockGate(t *testing.T) {
	content := `{"blocks":[{"type":"index","index":{"depth":1}}]}`
	// Not marked as an index page → the block renders nothing.
	r, _ := Render(content, indexOpts("/blog", false))
	if strings.Contains(string(r.HTML), "pbcssg-index-list") {
		t.Errorf("index block must not render when the host page isn't an index page")
	}
}

func TestIndexBlockDepthAndFilters(t *testing.T) {
	// Depth 1: only direct children, sorted newest-first; excludes the host page,
	// manually-excluded pages, and non-descendants. An index child (apple) is still
	// listed — the "is index" flag no longer auto-excludes.
	content := `{"blocks":[{"type":"index","index":{"depth":1,"sort":"date-desc"}}]}`
	r, _ := Render(content, indexOpts("/blog", true))
	s := string(r.HTML)
	iZ := strings.Index(s, "/blog/zebra")
	iA := strings.Index(s, "/blog/apple")
	if iZ < 0 || iA < 0 || iZ > iA {
		t.Errorf("depth-1 list wrong/order not newest-first (zebra=%d apple=%d)", iZ, iA)
	}
	for _, absent := range []string{"/blog/deep/sub", "/blog/hidden", "/about", `href="/blog"`} {
		if strings.Contains(s, absent) {
			t.Errorf("depth-1 list should not contain %q", absent)
		}
	}
	// Depth 2 picks up the grandchild.
	r2, _ := Render(`{"blocks":[{"type":"index","index":{"depth":2}}]}`, indexOpts("/blog", true))
	if !strings.Contains(string(r2.HTML), "/blog/deep/sub") {
		t.Errorf("depth-2 list should include the grandchild")
	}
}

func TestIndexBlockStyleAndSort(t *testing.T) {
	// Detailed style shows date + summary; title sort orders alphabetically.
	content := `{"blocks":[{"type":"index","index":{"depth":1,"sort":"title","style":"detailed","title":"Posts"}}]}`
	r, _ := Render(content, indexOpts("/blog", true))
	s := string(r.HTML)
	if !strings.Contains(s, "<h2 class=\"pbcssg-index-title\">Posts</h2>") {
		t.Errorf("index title heading missing")
	}
	if !strings.Contains(s, "pbcssg-index-detailed") || !strings.Contains(s, "about apples") || !strings.Contains(s, "2026-07-10") {
		t.Errorf("detailed style should show summary + date:\n%s", s)
	}
	if strings.Index(s, "Apple") > strings.Index(s, "Zebra") {
		t.Errorf("title sort should put Apple before Zebra")
	}
}

func TestIndexBlockCap(t *testing.T) {
	content := `{"blocks":[{"type":"index","index":{"depth":1,"limit":1}}]}`
	r, _ := Render(content, indexOpts("/blog", true))
	s := string(r.HTML)
	if !strings.Contains(s, "Showing the first 1 of 2.") {
		t.Errorf("cap should note truncation (2 direct children, limit 1):\n%s", s)
	}
}

func TestFooterAndSearchLayout(t *testing.T) {
	opts := Options{
		Title: "Home", SiteName: "TUL", BuildNumber: "9", Year: 2026, Search: true,
		FooterNav: []NavLink{{Label: "Privacy", Href: "/privacy/"}, {Label: "RSS", Href: "/feeds/blog.rss"}},
	}
	s := renderHTML(t, `{"body":"# Hi"}`, opts)
	// Footer: row 1 = pipe-separated centered nav; row 2 = copyright with auto year
	// + Release number.
	for _, want := range []string{
		`<footer class="pbcssg-footer">`,
		`<nav class="pbcssg-footer-nav" aria-label="Footer"><a href="/privacy/">Privacy</a> | <a href="/feeds/blog.rss">RSS</a></nav>`,
		`<p class="pbcssg-copyright">© 2026 TUL</p>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("footer missing %q:\n%s", want, s)
		}
	}
	// Search: label sits in a row wrapper immediately before the input (left of it).
	if !strings.Contains(s, `<div class="pbcssg-search-row">`) {
		t.Errorf("search label/input should share a row wrapper")
	}
	li := strings.Index(s, `class="pbcssg-search-label"`)
	ii := strings.Index(s, `id="pbcssg-search-input"`)
	if li < 0 || ii < 0 || li > ii {
		t.Errorf("search label should come before (left of) the input")
	}
}

func TestFooterYearOmittedWhenZero(t *testing.T) {
	// No year stamped → the "©" line still renders without a bogus year.
	s := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL", BuildNumber: "1"})
	if !strings.Contains(s, `© TUL`) {
		t.Errorf("year-less footer wrong:\n%s", s)
	}
}

func TestLayoutHasExtRefSlot(t *testing.T) {
	// Every rendered page carries the ExtRefSlot placeholder just before the footer,
	// so the build has a stable anchor to replace with (or strip in place of) the
	// external-references badge. This guards against template/const drift.
	s := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL"})
	if !strings.Contains(s, ExtRefSlot) {
		t.Fatalf("layout is missing the ExtRefSlot placeholder %q:\n%s", ExtRefSlot, s)
	}
	if si, fi := strings.Index(s, ExtRefSlot), strings.Index(s, "pbcssg-footer"); si < 0 || fi < 0 || si > fi {
		t.Errorf("ExtRefSlot must sit before the footer (slot=%d footer=%d)", si, fi)
	}
}

func TestExternalRefList(t *testing.T) {
	// An empty list renders nothing (fully self-hosted page shows no listing).
	if got := ExternalRefList(nil); got != "" {
		t.Errorf("empty list should render nothing, got %q", got)
	}

	got := ExternalRefList([]ExtRef{
		{Domain: "youtube.com", Grade: "F", GradeName: "Invasive", Count: 2,
			Reasons: []string{"Sets ad/tracking cookies", "Does not honour Global Privacy Control"}},
		{Domain: "meta.com", Grade: "?", GradeName: "Unclassified", Count: 1,
			Reasons: []string{"No classification on record for this domain"}},
	})
	for _, want := range []string{
		`<h2 id="pbcssg-extref-heading" class="pbcssg-extref-heading">External references</h2>`,
		`<span class="pbcssg-grade pbcssg-grade-f" title="Privacy grade F">F</span> <code>youtube.com</code>`,
		`<span class="pbcssg-extref-name">Invasive · 2 refs</span>`,
		`<small class="pbcssg-extref-reason">Sets ad/tracking cookies</small>`,
		`<small class="pbcssg-extref-reason">Does not honour Global Privacy Control</small>`,
		`<span class="pbcssg-grade pbcssg-grade-unknown" title="Privacy grade ?">?</span> <code>meta.com</code>`,
		`<span class="pbcssg-extref-name">Unclassified</span>`, // Count 1 → no "· N refs"
		`<small class="pbcssg-extref-reason">No classification on record for this domain</small>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing missing %q:\n%s", want, got)
		}
	}
}

func TestClassificationReportRender(t *testing.T) {
	legend := []ClassifyGrade{{"A", "Clean", "✓✓"}, {"F", "Invasive", "✕"}, {"?", "Unclassified", "?"}}

	// Lite form: legend + methodology, but no dataset details.
	lite, err := ClassificationReport(ClassifyReport{Legend: legend}, Options{SiteName: "TUL"})
	if err != nil {
		t.Fatal(err)
	}
	ls := string(lite)
	if !strings.Contains(ls, "The rating scale") || !strings.Contains(ls, "Clean") || !strings.Contains(ls, "pbc-classification") {
		t.Errorf("lite report missing legend/methodology:\n%s", ls)
	}
	if strings.Contains(ls, "Classifications used") || strings.Contains(ls, "domains.json") {
		t.Errorf("lite report must not expose the dataset")
	}

	// Details form: summary + listing + JSON link + optional dataset repo link.
	full, err := ClassificationReport(ClassifyReport{
		Details: true, JSONHref: "/.well-known/pbc-classification/domains.json",
		DataRepoURL: "https://example.com/data", Legend: legend, Total: 1,
		Counts:  []ClassifyCount{{"A", 1}},
		Entries: []ExtRef{{Domain: "a.example", Grade: "A", GradeName: "Clean", Reasons: []string{"clean"}}},
	}, Options{SiteName: "TUL"})
	if err != nil {
		t.Fatal(err)
	}
	fs := string(full)
	for _, want := range []string{
		"Classifications used", "<code>a.example</code>", "1 A",
		`href="/.well-known/pbc-classification/domains.json"`, `href="https://example.com/data"`,
	} {
		if !strings.Contains(fs, want) {
			t.Errorf("details report missing %q", want)
		}
	}
}

func TestExtRefListLinksToReport(t *testing.T) {
	s := ExternalRefList([]ExtRef{{Domain: "x.example", Grade: "F", GradeName: "Invasive", Count: 1}})
	if !strings.Contains(s, `href="/classification"`) || !strings.Contains(s, "How we rate these") {
		t.Errorf("external-references listing should link to /classification:\n%s", s)
	}
}

func TestMediaRefs(t *testing.T) {
	sha := strings.Repeat("a", 64)
	sha2 := strings.Repeat("b", 64)
	html := []byte(`<img src="/media/` + sha + `.png">` +
		`<video><source src="/media/` + sha2 + `.mp4"></video>` +
		`<img src="/media/` + sha + `.png">` + // duplicate, deduped
		`<a href="/media/notahash.png">x</a>` + // non-hash, ignored
		`<img src="https://cdn.example/media/` + sha + `.png">`) // external path still matches the /media/<hash> tail

	refs := MediaRefs(html)
	if len(refs) != 2 {
		t.Fatalf("want 2 unique refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].SHA != sha || refs[0].Ext != "png" {
		t.Errorf("ref[0] = %+v, want %s.png", refs[0], sha)
	}
	if refs[1].SHA != sha2 || refs[1].Ext != "mp4" {
		t.Errorf("ref[1] = %+v, want %s.mp4", refs[1], sha2)
	}
	if got := refs[0].Path(); got != "/media/"+sha+".png" {
		t.Errorf("Path() = %q", got)
	}
	if got := refs[0].Rel(); got != "media/"+sha+".png" {
		t.Errorf("Rel() = %q", got)
	}
	if len(MediaRefs([]byte("no media here"))) != 0 {
		t.Errorf("expected no refs in plain text")
	}
}

func TestThemeToggleWiring(t *testing.T) {
	// With a ThemeJSHref, every page carries the blocking head script and the
	// hidden footer toggle button (progressive enhancement: JS reveals it).
	s := renderHTML(t, `{"body":"# Hi"}`, Options{
		Title: "T", CSSHref: "/assets/theme.abc.css", ThemeJSHref: "/assets/pbcssg-theme.abc.js",
	})
	if !strings.Contains(s, `<script src="/assets/pbcssg-theme.abc.js"></script>`) {
		t.Errorf("theme script should load blocking in <head>:\n%s", s)
	}
	// It must precede the stylesheet so data-theme is set before styles apply.
	if strings.Index(s, "/assets/pbcssg-theme.abc.js") > strings.Index(s, "/assets/theme.abc.css") {
		t.Errorf("theme script should come before the stylesheet")
	}
	if !strings.Contains(s, `data-pbcssg-theme-toggle`) || !strings.Contains(s, `class="pbcssg-theme-toggle"`) {
		t.Errorf("footer should carry the toggle button:\n%s", s)
	}
	if !strings.Contains(s, `<button type="button" class="pbcssg-theme-toggle" data-pbcssg-theme-toggle hidden>`) {
		t.Errorf("toggle button should start hidden:\n%s", s)
	}

	// Without a ThemeJSHref (e.g. a caller that opts out), neither appears.
	s2 := renderHTML(t, `{"body":"# Hi"}`, Options{Title: "T"})
	if strings.Contains(s2, "pbcssg-theme") {
		t.Errorf("no theme script/toggle without ThemeJSHref:\n%s", s2)
	}
}

func TestHeaderBrand(t *testing.T) {
	// Text wordmark (A): a home link with the wordmark, no image.
	a := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "text", Text: "TUL", Align: "start"}})
	if !strings.Contains(a, `<a class="pbcssg-brand" href="/">`) || !strings.Contains(a, `<span class="pbcssg-brand-text">TUL</span>`) {
		t.Errorf("text brand should render a wordmark home link:\n%s", a)
	}
	if strings.Contains(a, "<img") {
		t.Errorf("text brand should not render an image")
	}

	// Logo-only (B): an image with the operator's alt (so the link is named).
	b := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "logo", LogoSrc: "/media/abc.svg", LogoAlt: "TUL logo", LogoHeight: "small"}})
	if !strings.Contains(b, `<img class="pbcssg-logo pbcssg-logo--small" src="/media/abc.svg" alt="TUL logo">`) {
		t.Errorf("logo brand should render the image with alt:\n%s", b)
	}
	if strings.Contains(b, "pbcssg-brand-text") {
		t.Errorf("logo-only should not render wordmark text")
	}

	// Lockup (C): image is decorative (alt="") because the wordmark names the link.
	c := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "logotext", Text: "TUL", LogoSrc: "/media/abc.svg", LogoAlt: "ignored", LogoHeight: "medium"}})
	if !strings.Contains(c, `src="/media/abc.svg" alt=""`) {
		t.Errorf("lockup logo should have empty alt (decorative):\n%s", c)
	}
	if !strings.Contains(c, `<span class="pbcssg-brand-text">TUL</span>`) {
		t.Errorf("lockup should render the wordmark")
	}

	// Centered (D): header carries the centred modifier class.
	d := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "text", Text: "TUL", Align: "center"}})
	if !strings.Contains(d, `class="pbcssg-header pbcssg-header--center"`) {
		t.Errorf("centered brand should add the modifier class:\n%s", d)
	}

	// None: no brand, and with no nav/search there is no header at all.
	none := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL", Brand: Brand{Mode: "none", Text: "TUL"}})
	if strings.Contains(none, "pbcssg-brand") || strings.Contains(none, "<header") {
		t.Errorf("none mode should render no brand/header:\n%s", none)
	}
}

func TestHeaderBrandDarkLogo(t *testing.T) {
	// With a dark logo set: two images, tagged --light / --dark, CSS swaps them.
	two := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "logo", LogoSrc: "/media/light.svg", LogoSrcDark: "/media/dark.svg", LogoAlt: "TUL logo", LogoHeight: "small"}})
	if !strings.Contains(two, `<img class="pbcssg-logo pbcssg-logo--small pbcssg-logo--light" src="/media/light.svg" alt="TUL logo">`) {
		t.Errorf("expected the light logo img:\n%s", two)
	}
	if !strings.Contains(two, `<img class="pbcssg-logo pbcssg-logo--small pbcssg-logo--dark" src="/media/dark.svg" alt="TUL logo">`) {
		t.Errorf("expected the dark logo img:\n%s", two)
	}

	// Without a dark logo: the single, unclassed logo (unchanged output).
	one := renderHTML(t, `{"body":"# Hi"}`, Options{SiteName: "TUL",
		Brand: Brand{Mode: "logo", LogoSrc: "/media/light.svg", LogoHeight: "small"}})
	if strings.Contains(one, "pbcssg-logo--light") || strings.Contains(one, "pbcssg-logo--dark") {
		t.Errorf("single-logo header must not carry theme-swap classes:\n%s", one)
	}
	if strings.Count(one, "<img") != 1 {
		t.Errorf("single-logo header should render exactly one image:\n%s", one)
	}

	// A dark logo is ignored unless the mode actually shows a logo.
	if (Brand{Mode: "text", Text: "TUL", LogoSrcDark: "/media/dark.svg"}).HasDarkLogo() {
		t.Errorf("HasDarkLogo must be false when the mode shows no logo")
	}
}
