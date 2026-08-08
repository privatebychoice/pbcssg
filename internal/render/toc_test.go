package render

import (
	"strings"
	"testing"
)

func TestAnchorsAndTOC(t *testing.T) {
	in := `<main>` +
		`<nav class="pbcssg-toc" data-pbcssg-toc data-depth="3" data-title="Contents" aria-label="Table of contents"></nav>` +
		`<h2>Alpha</h2>` +
		`<h3>Beta Bit</h3>` +
		`<h2 id="custom">Gamma</h2>` +
		`<h4>Delta</h4>` +
		`</main>`
	out, err := AnchorsAndTOC([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	// Headings get slug ids; an author-supplied id is preserved.
	for _, want := range []string{
		`<h2 id="alpha">Alpha<a class="pbcssg-anchor" href="#alpha"`,
		`<h3 id="beta-bit">Beta Bit<a class="pbcssg-anchor" href="#beta-bit"`,
		`<h2 id="custom">Gamma<a class="pbcssg-anchor" href="#custom"`,
		`<h4 id="delta">Delta<a class="pbcssg-anchor" href="#delta"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing anchored heading %q:\n%s", want, s)
		}
	}

	// The TOC placeholder is filled with a nested list and the title; the data-* hints
	// are dropped.
	for _, want := range []string{
		`<p class="pbcssg-toc-title">Contents</p>`,
		`<ol class="pbcssg-toc-list">`,
		`<a href="#alpha">Alpha</a>`,
		`<a href="#beta-bit">Beta Bit</a>`,
		`<a href="#custom">Gamma</a>`,
		`<a href="#delta">Delta</a>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("TOC missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "data-pbcssg-toc") || strings.Contains(s, "data-depth") {
		t.Errorf("TOC build hints should be dropped:\n%s", s)
	}
	// Beta (h3) nests under Alpha (h2): a sub-list appears after Alpha's link.
	ai, bi := strings.Index(s, `#alpha">Alpha</a>`), strings.Index(s, `#beta-bit"`)
	if ai < 0 || bi < 0 || ai > bi {
		t.Errorf("expected Beta nested after Alpha in the TOC:\n%s", s)
	}
}

func TestAnchorsAndTOCDepthAndDedup(t *testing.T) {
	// Depth 1 lists only h2s; deeper headings are still anchored but excluded from the
	// TOC. Two identical headings get de-duplicated ids.
	in := `<main>` +
		`<nav data-pbcssg-toc data-depth="1"></nav>` +
		`<h2>Topic</h2><h3>Sub</h3><h2>Topic</h2>` +
		`</main>`
	out, err := AnchorsAndTOC([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	if !strings.Contains(s, `<h2 id="topic">`) || !strings.Contains(s, `<h2 id="topic-2">`) {
		t.Errorf("duplicate headings not de-duplicated:\n%s", s)
	}
	// h3 is anchored...
	if !strings.Contains(s, `<h3 id="sub"><a`) && !strings.Contains(s, `<h3 id="sub">Sub<a`) {
		t.Errorf("h3 should still be anchored:\n%s", s)
	}
	// ...but not listed in a depth-1 TOC (the TOC link form is a bare <a href>, distinct
	// from the heading's own class="pbcssg-anchor" permalink).
	if strings.Contains(s, `<a href="#sub">Sub</a>`) {
		t.Errorf("depth 1 TOC must not list h3:\n%s", s)
	}
	if !strings.Contains(s, `<a href="#topic">Topic</a>`) || !strings.Contains(s, `<a href="#topic-2">Topic</a>`) {
		t.Errorf("depth 1 TOC should list both h2s:\n%s", s)
	}
}

func TestAnchorsAndTOCRelocatesReadingTime(t *testing.T) {
	// render emits the reading-time meta at the top of content; the pass moves it to
	// just after the first h1 and strips the marker attribute.
	in := `<main><p class="pbcssg-post-meta" data-pbcssg-readingtime>~3 min read</p>` +
		`<h1 id="t">Title</h1><p>Body.</p></main>`
	out, err := AnchorsAndTOC([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "data-pbcssg-readingtime") {
		t.Errorf("marker attr should be stripped:\n%s", s)
	}
	// The meta now follows the </h1>.
	want := `<h1 id="t">Title</h1><p class="pbcssg-post-meta">~3 min read</p>`
	if !strings.Contains(s, want) {
		t.Errorf("reading-time meta not relocated after the h1:\n%s", s)
	}
}

func TestAnchorsAndTOCNoHeadings(t *testing.T) {
	// A toc block on a page with no headings leaves an empty <nav> (no broken markup).
	out, err := AnchorsAndTOC([]byte(`<main><nav class="pbcssg-toc" data-pbcssg-toc data-depth="3"></nav><p>Body only.</p></main>`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `<nav class="pbcssg-toc" aria-label="Table of contents"></nav>`) {
		t.Errorf("empty TOC should render an empty nav:\n%s", s)
	}
}
