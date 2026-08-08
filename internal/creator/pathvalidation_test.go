package creator

import (
	"strings"
	"testing"
)

// TestPathInputValidation covers issue #1: page paths must be URL-clean slug
// segments — rejecting spaces, uppercase, dots (so "." / ".." traversal cannot
// occur), and other characters that break the editor UI or the built URL.
func TestPathInputValidation(t *testing.T) {
	valid := []string{"/", "/about", "/privacy", "/blog/my-post", "/2026/notes", "/a-b-c"}
	for _, p := range valid {
		if msg := pathInputError(p); msg != "" {
			t.Errorf("path %q should be valid, got %q", p, msg)
		}
	}
	invalid := []string{
		"/my page", "/about ", "/About", "/blog/My-Post", // spaces / uppercase
		"/../etc", "/foo/..", "/foo/./bar", // traversal / dots
		"/a_b", "/foo.bar", "/foo//bar", "/café", "/pa%20th", // other unsafe
	}
	for _, p := range invalid {
		if pathInputError(p) == "" {
			t.Errorf("path %q should be rejected", p)
		}
	}
}

// TestNormalizePath covers the leading-slash and trailing-slash canonicalization
// (so "/foo/" and "/foo" don't become distinct pages).
func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"": "/", "/": "/", "about": "/about", "/about/": "/about",
		"/blog/post//": "/blog/post", "  /x  ": "/x",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPathSpaceRejectedOnCreate: a space in the path is rejected inline on create
// (before a page is ever stored), with the format guidance.
func TestPathSpaceRejectedOnCreate(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/pages", h.form(map[string]string{"title": "X", "path": "/my page", "body": "# X"}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "only lowercase letters") {
		t.Fatalf("space in path should be rejected inline: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if pages, _ := h.st.Pages(); len(pages) != 0 {
		t.Errorf("invalid path must not create a page, got %d", len(pages))
	}
	// A clean path still creates.
	if ok := h.post("/pages", h.form(map[string]string{"title": "OK", "path": "/my-page", "body": "# ok"})); ok.Code != 303 {
		t.Errorf("clean path should create: %d", ok.Code)
	}
}
