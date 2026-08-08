package hygiene

import (
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

func apply(t *testing.T, html string, cfg Config) Result {
	t.Helper()
	r, err := Apply([]byte(html), cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return r
}

func hasChange(cs []Change, kind, detailSub string) bool {
	for _, c := range cs {
		if c.Kind == kind && strings.Contains(c.Detail, detailSub) {
			return true
		}
	}
	return false
}

func hasWarning(ws []Warning, host string) bool {
	for _, w := range ws {
		if w.Host == host {
			return true
		}
	}
	return false
}

func TestApplyFragment(t *testing.T) {
	base := mustURL(t, "https://tul.example/")
	// A bare fragment (as from a reveal block's rendered markdown): no <html>/<body>.
	in := `<p>See <a href="https://third.example/x">this</a> and <img src="https://cdn.example/p.gif"> and <a href="/local">home</a>.</p>`
	r, err := ApplyFragment([]byte(in), Config{Base: base})
	if err != nil {
		t.Fatalf("ApplyFragment: %v", err)
	}
	out := string(r.HTML)
	// No document wrapper is added to a fragment.
	if strings.Contains(out, "<html") || strings.Contains(out, "<body") || strings.Contains(out, "<head") {
		t.Errorf("fragment output must not gain a document wrapper:\n%s", out)
	}
	// External anchor hardened; external image lazy + referrerpolicy.
	if !strings.Contains(out, `rel="noopener noreferrer"`) || !strings.Contains(out, `referrerpolicy=`) {
		t.Errorf("external anchor not hardened:\n%s", out)
	}
	if !strings.Contains(out, `loading="lazy"`) {
		t.Errorf("external image not lazied:\n%s", out)
	}
	// The same-origin link is left untouched (no rel injected).
	if strings.Contains(out, `href="/local" rel=`) {
		t.Errorf("same-origin link should be untouched:\n%s", out)
	}
}

func TestRewriteYouTubeAndLazy(t *testing.T) {
	base := mustURL(t, "https://example.org")
	r := apply(t, `<iframe src="https://www.youtube.com/embed/abc"></iframe>`, Config{Base: base})

	out := string(r.HTML)
	if !strings.Contains(out, "www.youtube-nocookie.com/embed/abc") {
		t.Errorf("expected rewritten nocookie host:\n%s", out)
	}
	if strings.Contains(out, "www.youtube.com/embed") {
		t.Errorf("original youtube.com host should be gone:\n%s", out)
	}
	if !strings.Contains(out, `loading="lazy"`) {
		t.Errorf("expected loading=lazy:\n%s", out)
	}
	if !strings.Contains(out, `referrerpolicy="no-referrer"`) {
		t.Errorf("expected referrerpolicy:\n%s", out)
	}
	if !hasChange(r.Changes, "rewrite", "youtube.com -> youtube-nocookie.com") {
		t.Errorf("missing rewrite change: %+v", r.Changes)
	}
}

func TestRewriteBoundarySafe(t *testing.T) {
	base := mustURL(t, "https://example.org")
	// notyoutube.com must NOT be rewritten (boundary), but is still third-party.
	r := apply(t, `<iframe src="https://notyoutube.com/x"></iframe>`, Config{Base: base})
	out := string(r.HTML)
	if !strings.Contains(out, "notyoutube.com/x") {
		t.Errorf("notyoutube.com should be untouched by rewrite:\n%s", out)
	}
	if hasChange(r.Changes, "rewrite", "") {
		t.Errorf("no rewrite should have applied: %+v", r.Changes)
	}
	if !strings.Contains(out, `loading="lazy"`) {
		t.Errorf("third-party iframe should still be lazied:\n%s", out)
	}
}

func TestExternalAnchorHardeningAndRelMerge(t *testing.T) {
	base := mustURL(t, "https://example.org")
	r := apply(t, `<a href="https://third.example/x" rel="nofollow">t</a>`, Config{Base: base})
	out := string(r.HTML)
	if !strings.Contains(out, `rel="nofollow noopener noreferrer"`) {
		t.Errorf("expected merged, sorted rel:\n%s", out)
	}
	if !strings.Contains(out, `referrerpolicy="no-referrer"`) {
		t.Errorf("expected referrerpolicy on external anchor:\n%s", out)
	}
	if !hasChange(r.Changes, "rel", "noopener noreferrer") {
		t.Errorf("missing rel change: %+v", r.Changes)
	}
}

func TestSameOriginAndFirstPartyUntouched(t *testing.T) {
	base := mustURL(t, "https://example.org")
	cfg := Config{
		Base:       base,
		FirstParty: func(host string) bool { return host == "owned.example" },
	}
	r := apply(t, `
		<a href="/local">rel</a>
		<a href="https://example.org/abs">same</a>
		<a href="https://owned.example/x">owned</a>
		<img src="/pic.png">`, cfg)

	if len(r.Changes) != 0 {
		t.Errorf("first-party/same-origin refs should not be modified, got: %+v", r.Changes)
	}
}

func TestExternalImageLazy(t *testing.T) {
	base := mustURL(t, "https://example.org")
	r := apply(t, `<img src="https://cdn.example/i.png"><img src="/local.png">`, Config{Base: base})
	out := string(r.HTML)
	// The external image gets lazy; the local one does not add a second loading attr.
	if strings.Count(out, `loading="lazy"`) != 1 {
		t.Errorf("expected exactly one lazied image:\n%s", out)
	}
}

func TestRemoveThirdPartyFaviconAndPreconnect(t *testing.T) {
	base := mustURL(t, "https://example.org")
	r := apply(t, `
		<link rel="icon" href="https://evil.example/f.ico">
		<link rel="icon" href="/favicon.ico">
		<link rel="preconnect" href="https://pre.example">`, Config{Base: base})
	out := string(r.HTML)
	if strings.Contains(out, "evil.example") {
		t.Errorf("third-party favicon should be removed:\n%s", out)
	}
	if !strings.Contains(out, "/favicon.ico") {
		t.Errorf("same-origin favicon should be kept:\n%s", out)
	}
	if strings.Contains(out, "pre.example") {
		t.Errorf("third-party preconnect should be removed:\n%s", out)
	}
	if !hasChange(r.Changes, "remove-favicon", "evil.example") {
		t.Errorf("missing remove-favicon change: %+v", r.Changes)
	}
	if !hasChange(r.Changes, "remove-preconnect", "pre.example") {
		t.Errorf("missing remove-preconnect change: %+v", r.Changes)
	}
}

func TestWarnThirdPartyScriptAndStylesheet(t *testing.T) {
	base := mustURL(t, "https://example.org")
	r := apply(t, `
		<script src="https://s.example/x.js"></script>
		<link rel="stylesheet" href="https://css.example/s.css">`, Config{Base: base})
	out := string(r.HTML)
	// Scripts/stylesheets are not neutralized; they are warned about and kept.
	if !strings.Contains(out, "s.example/x.js") {
		t.Errorf("script should be kept (only warned):\n%s", out)
	}
	if !hasWarning(r.Warnings, "s.example") {
		t.Errorf("expected third-party script warning: %+v", r.Warnings)
	}
	if !hasWarning(r.Warnings, "css.example") {
		t.Errorf("expected third-party stylesheet warning: %+v", r.Warnings)
	}
}
