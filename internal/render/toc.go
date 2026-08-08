package render

// Heading anchors + table of contents (SPEC §6.12). This is a build-time post-pass
// over a page's already-rendered, hygiene-normalized HTML: it assigns stable slug
// ids to the content headings (h2–h4), appends a self-anchor permalink to each, and
// fills any toc block placeholder with an auto-generated nested list of those
// headings. It is pure presentation — no JavaScript — and runs after hygiene but
// before the external-references listing is injected, so the chrome heading the
// build adds later ("External references") is never anchored or listed.

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// tocMaxDepth caps how many heading levels a toc block may list, aligned with the
// anchored range (h2–h4): depth 1 → h2 only, 3 → h2–h4 (the default).
const tocMaxDepth = 3

// heading is one anchored content heading, in document order.
type heading struct {
	level int // 2, 3, or 4
	id    string
	text  string
}

// AnchorsAndTOC assigns ids + self-anchor permalinks to the h2–h4 headings in a
// rendered page and fills any toc block placeholders with a nested list of them. in
// is a full HTML document (the hygiene output); the returned bytes are the
// transformed document. Headings that already carry an id (e.g. goldmark's auto IDs
// for markdown headings) keep it; block-generated headings without one get a
// slugified, de-duplicated id.
func AnchorsAndTOC(in []byte) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(in))
	if err != nil {
		return nil, fmt.Errorf("render: toc parse: %w", err)
	}

	// First pass: gather every existing id so generated slugs never collide (footnote
	// ids, existing heading ids, etc.), and collect the heading nodes in order.
	seen := map[string]bool{}
	var headingNodes []*html.Node
	var tocNodes []*html.Node
	var firstH1, readingTime *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id := getAttrNode(n, "id"); id != "" {
				seen[id] = true
			}
			switch n.DataAtom {
			case atom.H1:
				if firstH1 == nil {
					firstH1 = n
				}
			case atom.H2, atom.H3, atom.H4:
				headingNodes = append(headingNodes, n)
			}
			if hasBoolAttr(n, "data-pbcssg-toc") {
				tocNodes = append(tocNodes, n)
			}
			if readingTime == nil && hasBoolAttr(n, "data-pbcssg-readingtime") {
				readingTime = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Relocate the reading-time meta (§6.13) to just after the page's first heading.
	// Without an h1 it stays where render placed it (top of content).
	if readingTime != nil {
		dropAttrNode(readingTime, "data-pbcssg-readingtime")
		if firstH1 != nil && firstH1.Parent != nil {
			if readingTime.Parent != nil {
				readingTime.Parent.RemoveChild(readingTime)
			}
			firstH1.Parent.InsertBefore(readingTime, firstH1.NextSibling)
		}
	}

	// Assign ids + append the self-anchor to each heading, and record it for the TOC.
	var headings []heading
	for _, h := range headingNodes {
		text := textOf(h)
		id := getAttrNode(h, "id")
		if id == "" {
			id = uniqueSlug(text, seen)
			setAttrNode(h, "id", id)
		}
		appendAnchor(h, id, text)
		headings = append(headings, heading{level: headingLevel(h), id: id, text: text})
	}

	// Fill each toc placeholder with a nested list scoped to its depth.
	for _, tn := range tocNodes {
		fillTOC(tn, headings)
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return nil, fmt.Errorf("render: toc render: %w", err)
	}
	return buf.Bytes(), nil
}

func headingLevel(n *html.Node) int {
	switch n.DataAtom {
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	}
	return 0
}

// appendAnchor adds a self-anchor permalink as the heading's last child. The "#"
// glyph is decorative; the accessible name carries the heading text so the link is
// meaningful to a screen reader and keyboard user.
func appendAnchor(h *html.Node, id, text string) {
	a := &html.Node{
		Type:     html.ElementNode,
		Data:     "a",
		DataAtom: atom.A,
		Attr: []html.Attribute{
			{Key: "class", Val: "pbcssg-anchor"},
			{Key: "href", Val: "#" + id},
			{Key: "aria-label", Val: "Permalink to “" + text + "”"},
		},
	}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: "#"})
	h.AppendChild(a)
}

// fillTOC replaces a toc placeholder's contents with an optional title and a nested
// list of the headings within the node's depth (data-depth, default tocMaxDepth).
// The data-* config attributes are dropped so the emitted <nav> is clean.
func fillTOC(nav *html.Node, headings []heading) {
	depth := tocMaxDepth
	if d := getAttrNode(nav, "data-depth"); d != "" {
		if v := atoiClamp(d, 1, tocMaxDepth); v > 0 {
			depth = v
		}
	}
	title := getAttrNode(nav, "data-title")

	// Reset the nav to just an aria-label; drop the data-* build hints.
	nav.Attr = []html.Attribute{
		{Key: "class", Val: "pbcssg-toc"},
		{Key: "aria-label", Val: "Table of contents"},
	}
	for c := nav.FirstChild; c != nil; {
		next := c.NextSibling
		nav.RemoveChild(c)
		c = next
	}

	maxLevel := 1 + depth // depth 3 → h2..h4
	list := buildTOCList(headings, maxLevel)
	if list == nil {
		return // no headings in range: leave an empty <nav>
	}
	if title != "" {
		p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P,
			Attr: []html.Attribute{{Key: "class", Val: "pbcssg-toc-title"}}}
		p.AppendChild(&html.Node{Type: html.TextNode, Data: title})
		nav.AppendChild(p)
	}
	nav.AppendChild(list)
}

// buildTOCList builds a nested <ol> from the flat, in-order heading list, including
// only headings at or above maxLevel. Deeper headings start a sub-list under the
// current item; shallower ones pop back up. Returns nil when nothing qualifies.
func buildTOCList(headings []heading, maxLevel int) *html.Node {
	var hs []heading
	for _, h := range headings {
		if h.level <= maxLevel {
			hs = append(hs, h)
		}
	}
	if len(hs) == 0 {
		return nil
	}
	root := ol("pbcssg-toc-list")
	type frame struct {
		level int
		list  *html.Node
	}
	stack := []frame{{level: hs[0].level, list: root}}
	for _, h := range hs {
		for len(stack) > 1 && h.level < stack[len(stack)-1].level {
			stack = stack[:len(stack)-1]
		}
		top := &stack[len(stack)-1]
		if h.level > top.level {
			// Nest a new list under the current list's last item.
			parentLi := top.list.LastChild
			if parentLi == nil {
				parentLi = &html.Node{Type: html.ElementNode, Data: "li", DataAtom: atom.Li}
				top.list.AppendChild(parentLi)
			}
			sub := ol("")
			parentLi.AppendChild(sub)
			stack = append(stack, frame{level: h.level, list: sub})
			top = &stack[len(stack)-1]
		}
		li := &html.Node{Type: html.ElementNode, Data: "li", DataAtom: atom.Li}
		a := &html.Node{Type: html.ElementNode, Data: "a", DataAtom: atom.A,
			Attr: []html.Attribute{{Key: "href", Val: "#" + h.id}}}
		a.AppendChild(&html.Node{Type: html.TextNode, Data: h.text})
		li.AppendChild(a)
		top.list.AppendChild(li)
	}
	return root
}

func ol(class string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: "ol", DataAtom: atom.Ol}
	if class != "" {
		n.Attr = []html.Attribute{{Key: "class", Val: class}}
	}
	return n
}

// --- small helpers ---

func getAttrNode(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttrNode(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func dropAttrNode(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func hasBoolAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

// textOf returns the concatenated text content of a node (recursively), collapsing
// runs of whitespace to single spaces and trimming — the accessible heading label.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// uniqueSlug slugifies s and de-duplicates against seen (marking the result used).
func uniqueSlug(s string, seen map[string]bool) string {
	base := slugify(s)
	if base == "" {
		base = "section"
	}
	slug := base
	for i := 2; seen[slug]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	seen[slug] = true
	return slug
}

// slugify lowercases s and replaces each run of non-alphanumeric characters with a
// single dash, trimming leading/trailing dashes.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
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
	return strings.TrimRight(b.String(), "-")
}

// atoiClamp parses a small non-negative integer and clamps it to [lo, hi]; returns 0
// on a parse failure so the caller can fall back to its default.
func atoiClamp(s string, lo, hi int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1000 {
			break
		}
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
