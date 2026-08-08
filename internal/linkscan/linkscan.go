// Package linkscan is the first stage of the pbcssg privacy pipeline (SPEC §5.2):
// it walks a page's rendered HTML, extracts every reference that would cause an
// off-origin network request, and (via the Scanner in scan.go) classifies each
// one's privacy impact with the pbc-classification module.
//
// Extraction is intentionally load-oriented: it records the references a browser
// fetches when the page loads (links, scripts, images, stylesheets, iframes,
// media, favicons, preconnect hints, and CSS url() references), not purely
// semantic links like rel=canonical that trigger no request.
package linkscan

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Kind describes what caused a reference — i.e. how a browser would fetch it.
// It feeds linking-hygiene rules and how a reference is reported.
type Kind uint8

const (
	KindOther      Kind = iota // anything not otherwise categorized
	KindLink                   // <a href>, <area href>
	KindImage                  // <img src|srcset>, <source srcset>, <video poster>, SVG <image>
	KindScript                 // <script src>
	KindFrame                  // <iframe src>, <embed src>, <object data>
	KindStylesheet             // <link rel=stylesheet href>
	KindFavicon                // <link rel=icon|apple-touch-icon|mask-icon href>
	KindPreconnect             // <link rel=preconnect|dns-prefetch|preload|prefetch|modulepreload>
	KindMedia                  // <audio|video|source src>, <track src>
	KindStyleURL               // url(...) / @import in a style attribute or <style> element
)

func (k Kind) String() string {
	switch k {
	case KindLink:
		return "link"
	case KindImage:
		return "image"
	case KindScript:
		return "script"
	case KindFrame:
		return "frame"
	case KindStylesheet:
		return "stylesheet"
	case KindFavicon:
		return "favicon"
	case KindPreconnect:
		return "preconnect"
	case KindMedia:
		return "media"
	case KindStyleURL:
		return "style-url"
	default:
		return "other"
	}
}

// Reference is a single URL found in a page's rendered HTML.
type Reference struct {
	Kind     Kind   // what caused the reference
	Element  string // the element it came from, e.g. "a", "img", "link"
	Attr     string // the attribute it came from, e.g. "href", "src", "srcset", "style"
	RawURL   string // the URL exactly as authored
	Resolved string // RawURL resolved against the page base URL (RawURL if base is nil)
	Host     string // lower-cased hostname of a network (http/https) reference; "" otherwise
}

// Extract parses HTML from r and returns every load-causing reference, in
// document order. base is the page's absolute URL, used to resolve relative
// references so same-origin links can be distinguished from third-party ones; it
// may be nil, in which case relative references stay unresolved with an empty
// Host.
func Extract(r io.Reader, base *url.URL) ([]Reference, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("linkscan: parse: %w", err)
	}
	var refs []Reference
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			collect(n, base, &refs)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return refs, nil
}

// collect appends the references contributed by a single element.
func collect(n *html.Node, base *url.URL, refs *[]Reference) {
	tag := strings.ToLower(n.Data)

	// A style attribute on any element can pull in resources via url().
	if style, ok := attr(n, "style"); ok {
		for _, u := range extractCSSURLs(style) {
			add(refs, KindStyleURL, tag, "style", u, base)
		}
	}

	switch tag {
	case "a", "area":
		addAttr(refs, n, KindLink, tag, "href", base)
	case "img":
		addAttr(refs, n, KindImage, tag, "src", base)
		addSrcset(refs, n, tag, base)
	case "script":
		addAttr(refs, n, KindScript, tag, "src", base)
	case "iframe":
		addAttr(refs, n, KindFrame, tag, "src", base)
	case "embed":
		addAttr(refs, n, KindFrame, tag, "src", base)
	case "object":
		addAttr(refs, n, KindFrame, tag, "data", base)
	case "source":
		addSrcset(refs, n, tag, base)
		addAttr(refs, n, KindMedia, tag, "src", base)
	case "video":
		addAttr(refs, n, KindMedia, tag, "src", base)
		addAttr(refs, n, KindImage, tag, "poster", base)
	case "audio":
		addAttr(refs, n, KindMedia, tag, "src", base)
	case "track":
		addAttr(refs, n, KindMedia, tag, "src", base)
	case "image": // SVG <image>
		addSVGHref(refs, n, KindImage, tag, base)
	case "use": // SVG <use>
		addSVGHref(refs, n, KindOther, tag, base)
	case "style":
		if css := textContent(n); css != "" {
			for _, u := range extractCSSURLs(css) {
				add(refs, KindStyleURL, tag, "style", u, base)
			}
		}
	case "link":
		collectLink(n, base, refs)
	}
}

// collectLink handles <link>, whose meaning depends on its rel value. Only
// request-causing rels are recorded.
func collectLink(n *html.Node, base *url.URL, refs *[]Reference) {
	href, ok := attr(n, "href")
	if !ok || strings.TrimSpace(href) == "" {
		return
	}
	rels := strings.Fields(strings.ToLower(func() string { v, _ := attr(n, "rel"); return v }()))
	kind := KindOther
	record := false
	for _, rel := range rels {
		switch rel {
		case "stylesheet":
			kind, record = KindStylesheet, true
		case "icon", "shortcut", "apple-touch-icon", "mask-icon", "apple-touch-icon-precomposed":
			kind, record = KindFavicon, true
		case "preconnect", "dns-prefetch", "preload", "prefetch", "modulepreload":
			kind, record = KindPreconnect, true
		}
	}
	if record {
		add(refs, kind, "link", "href", href, base)
	}
}

// addAttr records the named attribute of n as a reference of the given kind.
func addAttr(refs *[]Reference, n *html.Node, kind Kind, element, attrName string, base *url.URL) {
	if v, ok := attr(n, attrName); ok {
		add(refs, kind, element, attrName, v, base)
	}
}

// addSrcset records each URL in an element's srcset.
func addSrcset(refs *[]Reference, n *html.Node, element string, base *url.URL) {
	if v, ok := attr(n, "srcset"); ok {
		for _, u := range parseSrcset(v) {
			add(refs, KindImage, element, "srcset", u, base)
		}
	}
}

// addSVGHref records an SVG element's href / xlink:href.
func addSVGHref(refs *[]Reference, n *html.Node, kind Kind, element string, base *url.URL) {
	if v, ok := attr(n, "href"); ok {
		add(refs, kind, element, "href", v, base)
	}
}

// add resolves raw against base and appends a Reference (skipping blank URLs).
func add(refs *[]Reference, kind Kind, element, attrName, raw string, base *url.URL) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	resolved, host := Resolve(raw, base)
	*refs = append(*refs, Reference{
		Kind:     kind,
		Element:  element,
		Attr:     attrName,
		RawURL:   raw,
		Resolved: resolved,
		Host:     host,
	})
}

// Resolve resolves raw against base and returns the resolved URL plus the
// lower-cased hostname of a *network* reference. Non-network schemes (data:,
// mailto:, javascript:, tel:, blob:, about:, ...) yield an empty host, since they
// cause no off-origin fetch. base may be nil.
func Resolve(raw string, base *url.URL) (resolved, host string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "":
		host = strings.ToLower(u.Hostname())
	default:
		host = "" // data:, mailto:, javascript:, tel:, blob:, about:, ...
	}
	return u.String(), host
}

// attr returns the first attribute of n whose (lower-cased) key matches name.
func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val, true
		}
	}
	return "", false
}

// textContent returns the concatenated direct text children of n.
func textContent(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// parseSrcset extracts the URL of each candidate in a srcset attribute. (URLs
// containing commas — rare, e.g. some data: URIs — are not fully handled.)
func parseSrcset(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.IndexAny(part, " \t\r\n"); i >= 0 {
			part = part[:i]
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// extractCSSURLs pulls the targets of url(...) and @import "..." out of a CSS
// fragment (a style attribute value or a <style> body).
func extractCSSURLs(css string) []string {
	var out []string
	lower := strings.ToLower(css)

	// url(...)
	for i := 0; ; {
		k := strings.Index(lower[i:], "url(")
		if k < 0 {
			break
		}
		start := i + k + len("url(")
		end := strings.IndexByte(css[start:], ')')
		if end < 0 {
			break
		}
		arg := strings.Trim(strings.TrimSpace(css[start:start+end]), `"'`)
		if arg = strings.TrimSpace(arg); arg != "" {
			out = append(out, arg)
		}
		i = start + end + 1
	}

	// @import "..."  (the @import url(...) form is already covered above)
	for i := 0; ; {
		k := strings.Index(lower[i:], "@import")
		if k < 0 {
			break
		}
		rest := css[i+k+len("@import"):]
		lrest := lower[i+k+len("@import"):]
		q := strings.IndexAny(rest, `"'`)
		if q < 0 {
			break
		}
		// Only treat as a quoted-string import if no url( precedes the quote.
		if u := strings.Index(lrest, "url("); u >= 0 && u < q {
			i = i + k + len("@import")
			continue
		}
		quote := rest[q]
		endRel := strings.IndexByte(rest[q+1:], quote)
		if endRel < 0 {
			break
		}
		if arg := strings.TrimSpace(rest[q+1 : q+1+endRel]); arg != "" {
			out = append(out, arg)
		}
		i = i + k + len("@import") + q + 1 + endRel + 1
	}

	return out
}
