package linkscan

import (
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse base %q: %v", s, err)
	}
	return u
}

// want is the subset of Reference fields the extraction tests assert on
// (Resolved is derived and normalization-sensitive, so it is not compared here).
type want struct {
	kind    Kind
	element string
	attr    string
	raw     string
	host    string
}

func TestExtract(t *testing.T) {
	base := mustURL(t, "https://example.org/page/")

	tests := []struct {
		name string
		html string
		want []want
	}{
		{
			name: "anchor and same-origin image",
			html: `<a href="https://third.example/x">t</a><img src="/local.png">`,
			want: []want{
				{KindLink, "a", "href", "https://third.example/x", "third.example"},
				{KindImage, "img", "src", "/local.png", "example.org"},
			},
		},
		{
			name: "img srcset yields one ref per candidate",
			html: `<img srcset="/a.png 1x, https://cdn.example/b.png 2x">`,
			want: []want{
				{KindImage, "img", "srcset", "/a.png", "example.org"},
				{KindImage, "img", "srcset", "https://cdn.example/b.png", "cdn.example"},
			},
		},
		{
			name: "script and protocol-relative iframe",
			html: `<script src="https://s.example/x.js"></script><iframe src="//frame.example/e"></iframe>`,
			want: []want{
				{KindScript, "script", "src", "https://s.example/x.js", "s.example"},
				{KindFrame, "iframe", "src", "//frame.example/e", "frame.example"},
			},
		},
		{
			name: "link rels: stylesheet/favicon/preconnect recorded, canonical ignored",
			html: `<link rel="stylesheet" href="https://css.example/s.css">` +
				`<link rel="icon" href="/favicon.ico">` +
				`<link rel="preconnect" href="https://pre.example">` +
				`<link rel="canonical" href="https://canon.example/">`,
			want: []want{
				{KindStylesheet, "link", "href", "https://css.example/s.css", "css.example"},
				{KindFavicon, "link", "href", "/favicon.ico", "example.org"},
				{KindPreconnect, "link", "href", "https://pre.example", "pre.example"},
			},
		},
		{
			name: "url() in a style attribute",
			html: `<div style="background:url('https://img.example/bg.png')"></div>`,
			want: []want{
				{KindStyleURL, "div", "style", "https://img.example/bg.png", "img.example"},
			},
		},
		{
			name: "style element: @import and url()",
			html: `<style>@import "https://imp.example/a.css"; .x{background:url(https://bg.example/b.png)}</style>`,
			want: []want{
				{KindStyleURL, "style", "style", "https://bg.example/b.png", "bg.example"},
				{KindStyleURL, "style", "style", "https://imp.example/a.css", "imp.example"},
			},
		},
		{
			name: "non-network schemes carry no host",
			html: `<a href="mailto:x@example.com">m</a><a href="javascript:void(0)">j</a><img src="data:image/png;base64,AAAA">`,
			want: []want{
				{KindLink, "a", "href", "mailto:x@example.com", ""},
				{KindLink, "a", "href", "javascript:void(0)", ""},
				{KindImage, "img", "src", "data:image/png;base64,AAAA", ""},
			},
		},
		{
			name: "SVG image and use href",
			html: `<svg><image href="https://svg.example/i.png"/><use href="#local"/></svg>`,
			want: []want{
				{KindImage, "image", "href", "https://svg.example/i.png", "svg.example"},
				{KindOther, "use", "href", "#local", "example.org"}, // fragment -> base host
			},
		},
		{
			name: "media, object, and embed",
			html: `<video src="https://v.example/m.mp4" poster="/p.jpg"></video>` +
				`<object data="https://o.example/x"></object>` +
				`<embed src="https://e.example/y">`,
			want: []want{
				{KindMedia, "video", "src", "https://v.example/m.mp4", "v.example"},
				{KindImage, "video", "poster", "/p.jpg", "example.org"},
				{KindFrame, "object", "data", "https://o.example/x", "o.example"},
				{KindFrame, "embed", "src", "https://e.example/y", "e.example"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract(strings.NewReader(tc.html), base)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			assertRefs(t, got, tc.want)
		})
	}
}

func TestExtract_NilBaseLeavesRelativeUnresolved(t *testing.T) {
	got, err := Extract(strings.NewReader(`<img src="/local.png"><img src="https://abs.example/x.png">`), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRefs(t, got, []want{
		{KindImage, "img", "src", "/local.png", ""}, // relative, no base -> no host
		{KindImage, "img", "src", "https://abs.example/x.png", "abs.example"},
	})
}

func TestKindString(t *testing.T) {
	for _, tc := range []struct {
		k    Kind
		want string
	}{
		{KindLink, "link"}, {KindImage, "image"}, {KindScript, "script"},
		{KindFrame, "frame"}, {KindStylesheet, "stylesheet"}, {KindFavicon, "favicon"},
		{KindPreconnect, "preconnect"}, {KindMedia, "media"}, {KindStyleURL, "style-url"},
		{KindOther, "other"}, {Kind(200), "other"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

// assertRefs checks that got matches want as a set (order-independent, since
// html.Parse may relocate elements such as <link>/<style> into <head>).
func assertRefs(t *testing.T, got []Reference, wants []want) {
	t.Helper()
	if len(got) != len(wants) {
		t.Fatalf("got %d refs, want %d\n got: %+v", len(got), len(wants), got)
	}
	remaining := append([]Reference(nil), got...)
	for _, w := range wants {
		idx := -1
		for i, r := range remaining {
			if r.Kind == w.kind && r.Element == w.element && r.Attr == w.attr && r.RawURL == w.raw && r.Host == w.host {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("missing ref %+v\n in got: %+v", w, got)
			continue
		}
		remaining = append(remaining[:idx], remaining[idx+1:]...)
	}
}
