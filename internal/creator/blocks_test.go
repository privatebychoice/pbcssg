package creator

import (
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/render"
)

func TestParseBlocks(t *testing.T) {
	// Empty / null / [] all yield no blocks.
	for _, raw := range []string{"", "  ", "[]", "null", "not json"} {
		if b := parseBlocks(raw); b != nil {
			t.Errorf("parseBlocks(%q) = %v, want nil", raw, b)
		}
	}

	raw := `[
		{"type":"markdown","markdown":"## Hi"},
		{"type":"markdown","markdown":"   "},
		{"type":"youtube","youtube":{"videoId":"abc123","title":"My Talk"}},
		{"type":"youtube","youtube":{}},
		{"type":"bogus"}
	]`
	got := parseBlocks(raw)
	// Empty markdown, empty youtube, and unknown-type blocks are pruned.
	if len(got) != 2 {
		t.Fatalf("want 2 kept blocks, got %d: %+v", len(got), got)
	}
	if got[0].Type != "markdown" || got[0].Markdown != "## Hi" {
		t.Errorf("block 0 wrong: %+v", got[0])
	}
	yt := got[1].YouTube
	if got[1].Type != "youtube" || yt == nil || yt.VideoID != "abc123" {
		t.Fatalf("block 1 wrong: %+v", got[1])
	}
	// The slug is derived from the title when not supplied.
	if yt.Name != "my-talk" {
		t.Errorf("youtube name = %q, want my-talk", yt.Name)
	}
}

func TestSanitizeCommentsBlock(t *testing.T) {
	// A comments block is config-less and survives sanitize with just its type.
	got := parseBlocks(`[{"type":"comments"}]`)
	if len(got) != 1 || got[0].Type != "comments" {
		t.Fatalf("want a single comments block, got %+v", got)
	}
	// It is not group-gateable, so any authored groups are dropped.
	gated := parseBlocks(`[{"type":"comments","groups":["members"]}]`)
	if len(gated) != 1 || len(gated[0].Groups) != 0 {
		t.Errorf("comments block must not carry groups, got %+v", gated)
	}
	// At most one per page: a second comments block is dropped, others are kept.
	multi := parseBlocks(`[
		{"type":"comments"},
		{"type":"markdown","markdown":"between"},
		{"type":"comments"}
	]`)
	n := 0
	for _, b := range multi {
		if b.Type == "comments" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want exactly one comments block kept, got %d: %+v", n, multi)
	}
	if len(multi) != 2 {
		t.Errorf("want 2 blocks total (one comments, one markdown), got %d: %+v", len(multi), multi)
	}
}

func TestSanitizeRevealBlock(t *testing.T) {
	raw := `[
		{"type":"reveal","reveal":{"content":"  hi@example.com  ","label":"  Reveal  ","kind":"EMAIL"}},
		{"type":"reveal","reveal":{"content":"spoiler","kind":"nonsense"}},
		{"type":"reveal","reveal":{"content":"   "}},
		{"type":"reveal"}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept reveal blocks (empty/no-content dropped), got %d: %+v", len(got), got)
	}
	// Trimmed, kind normalized to the allowlist.
	r0 := got[0].Reveal
	if r0 == nil || r0.Content != "hi@example.com" || r0.Label != "Reveal" || r0.Kind != "email" {
		t.Errorf("reveal 0 not sanitized: %+v", r0)
	}
	// Unknown kind → "text"; blank label → a non-empty default (a11y contract).
	r1 := got[1].Reveal
	if r1 == nil || r1.Kind != "text" || strings.TrimSpace(r1.Label) == "" {
		t.Errorf("reveal 1 defaults wrong: %+v", r1)
	}
	// No code by default (Mode A).
	if r0.Code != "" {
		t.Errorf("reveal 0 should have no code, got %q", r0.Code)
	}

	// A set code is trimmed and preserved (Mode B gate).
	gated := parseBlocks(`[{"type":"reveal","reveal":{"content":"secret","label":"Members","code":"  hunter2  "}}]`)
	if len(gated) != 1 || gated[0].Reveal == nil || gated[0].Reveal.Code != "hunter2" {
		t.Errorf("reveal code not trimmed/kept: %+v", gated)
	}

	// An over-long code is capped to render.MaxRevealCode runes (server-side backstop
	// mirroring the editor maxlength).
	long := strings.Repeat("x", render.MaxRevealCode+50)
	capped := parseBlocks(`[{"type":"reveal","reveal":{"content":"secret","code":"` + long + `"}}]`)
	if len(capped) != 1 || capped[0].Reveal == nil {
		t.Fatalf("capped reveal dropped: %+v", capped)
	}
	if got := len([]rune(capped[0].Reveal.Code)); got != render.MaxRevealCode {
		t.Errorf("over-long code not capped: got %d runes, want %d", got, render.MaxRevealCode)
	}
}

func TestYouTubeDescriptionLinksValidated(t *testing.T) {
	raw := `[{"type":"youtube","youtube":{"videoId":"x","title":"T","descriptionLinks":[
		"Test https://fool.com.com/",
		"https://ok.example/page",
		"  ",
		"not a url",
		"ftp://x.example",
		"javascript:alert(1)",
		"  https://spaced.example/  "
	]}}]`
	got := parseBlocks(raw)
	if len(got) != 1 || got[0].YouTube == nil {
		t.Fatalf("expected one youtube block: %+v", got)
	}
	links := got[0].YouTube.DescriptionLinks
	// Labeled lines are kept verbatim (the render layer parses label + URL);
	// entries with no valid http(s) URL are dropped.
	want := []string{"Test https://fool.com.com/", "https://ok.example/page", "https://spaced.example/"}
	if len(links) != len(want) {
		t.Fatalf("kept links = %v, want %v", links, want)
	}
	for i, w := range want {
		if links[i] != w {
			t.Errorf("link %d = %q, want %q", i, links[i], w)
		}
	}
}

func TestImageBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"image","image":{"src":"/media/abc.png","alt":" A cat ","caption":" hi "}},
		{"type":"image","image":{"src":"https://cdn.example/x.jpg","alt":"ext"}},
		{"type":"image","image":{"src":"","alt":"no src"}},
		{"type":"image","image":{"src":"javascript:alert(1)","alt":"bad"}},
		{"type":"image","image":{"src":"//evil.example/x.png","alt":"proto-rel"}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept image blocks, got %d: %+v", len(got), got)
	}
	if got[0].Image.Src != "/media/abc.png" || got[0].Image.Alt != "A cat" || got[0].Image.Caption != "hi" {
		t.Errorf("first image not trimmed/kept correctly: %+v", got[0].Image)
	}
	if got[1].Image.Src != "https://cdn.example/x.jpg" {
		t.Errorf("external https image should be kept: %+v", got[1].Image)
	}
}

func TestImageBlockLayoutSanitize(t *testing.T) {
	raw := `[
		{"type":"image","image":{"src":"/media/a.png","alt":"a","align":"left","maxWidth":"medium"}},
		{"type":"image","image":{"src":"/media/b.png","alt":"b","align":"RIGHT","maxWidth":"Large"}},
		{"type":"image","image":{"src":"/media/c.png","alt":"c","align":"bogus","maxWidth":"huge"}}
	]`
	got := parseBlocks(raw)
	if len(got) != 3 {
		t.Fatalf("want 3 image blocks, got %d", len(got))
	}
	if got[0].Image.Align != "left" || got[0].Image.MaxWidth != "medium" {
		t.Errorf("row0 layout not kept: %+v", got[0].Image)
	}
	if got[1].Image.Align != "right" || got[1].Image.MaxWidth != "large" {
		t.Errorf("row1 layout not normalized case-insensitively: %+v", got[1].Image)
	}
	if got[2].Image.Align != "" || got[2].Image.MaxWidth != "" {
		t.Errorf("row2 unknown values should collapse to default: %+v", got[2].Image)
	}
}

func TestCitationBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"citation","citation":{"quote":" A quote ","source":" Me ","url":"https://ok.example/x"}},
		{"type":"citation","citation":{"quote":"Q","url":"javascript:alert(1)"}},
		{"type":"citation","citation":{"quote":"","source":"no quote"}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept citations, got %d: %+v", len(got), got)
	}
	if got[0].Citation.Quote != "A quote" || got[0].Citation.Source != "Me" || got[0].Citation.URL != "https://ok.example/x" {
		t.Errorf("citation not trimmed/kept: %+v", got[0].Citation)
	}
	// Unsafe URL dropped, but the quote-only block is still kept.
	if got[1].Citation.URL != "" || got[1].Citation.Quote != "Q" {
		t.Errorf("unsafe url should be cleared, quote kept: %+v", got[1].Citation)
	}
}

func TestCodeBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"code","code":{"text":"a=1\nb=2\n\n","filename":"  main.go  ","language":" go ","comment":" hi ","lineNumbers":true}},
		{"type":"code","code":{"text":"   \n  "}},
		{"type":"code","code":{"text":""}}
	]`
	got := parseBlocks(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 kept code block (whitespace-only + empty dropped), got %d: %+v", len(got), got)
	}
	c := got[0].Code
	if c == nil {
		t.Fatal("code block nil")
	}
	if c.Text != "a=1\nb=2" { // trailing blank lines trimmed, interior preserved
		t.Errorf("code text not trimmed correctly: %q", c.Text)
	}
	if c.Filename != "main.go" || c.Language != "go" || c.Comment != "hi" {
		t.Errorf("caption fields not trimmed: %+v", c)
	}
	if !c.LineNumbers {
		t.Errorf("lineNumbers flag lost")
	}
}

func TestDetailsBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"details","details":{"summary":"  Q  ","markdown":"a","open":true}},
		{"type":"details","details":{"summary":"","markdown":"body but no summary"}},
		{"type":"details","details":{"summary":"","markdown":""}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept details (fully empty dropped), got %d: %+v", len(got), got)
	}
	if got[0].Details.Summary != "Q" || !got[0].Details.Open {
		t.Errorf("details not trimmed/open preserved: %+v", got[0].Details)
	}
	// A body with no summary keeps the block but gets a default label (a11y).
	if got[1].Details.Summary != "Details" {
		t.Errorf("empty summary should default to a label: %+v", got[1].Details)
	}
}

func TestRelatedBlockSanitize(t *testing.T) {
	// Count is clamped to 1..10 (0/out-of-range → default 5); title is trimmed.
	raw := `[
		{"type":"related","related":{"title":"  See also  ","count":0}},
		{"type":"related","related":{"count":99}},
		{"type":"related","related":{"count":3}}
	]`
	got := parseBlocks(raw)
	if len(got) != 3 {
		t.Fatalf("want 3 related blocks, got %d", len(got))
	}
	if got[0].Related.Title != "See also" || got[0].Related.Count != 5 {
		t.Errorf("count 0 → default 5, title trimmed: %+v", got[0].Related)
	}
	if got[1].Related.Count != 5 {
		t.Errorf("out-of-range count → default 5: %+v", got[1].Related)
	}
	if got[2].Related.Count != 3 {
		t.Errorf("valid count preserved: %+v", got[2].Related)
	}
}

func TestGalleryBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"gallery","gallery":{"mode":"manual","columns":9,"items":[
			{"src":"/media/a.png","alt":" A ","caption":" cap "},
			{"src":"javascript:alert(1)","alt":"bad"},
			{"src":"  ","alt":"blank"}
		]}},
		{"type":"gallery","gallery":{"mode":"tag","tag":" Pets ","sort":"weird","items":[{"src":"/media/x.png"}]}},
		{"type":"gallery","gallery":{"mode":"tag","tag":""}},
		{"type":"gallery","gallery":{"mode":"manual","items":[]}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 galleries kept (empty tag + empty manual dropped), got %d: %+v", len(got), got)
	}
	// Manual: bad/blank srcs dropped, fields trimmed, columns clamped to default.
	m := got[0].Gallery
	if m.Mode != "manual" || len(m.Items) != 1 || m.Items[0].Src != "/media/a.png" || m.Items[0].Alt != "A" || m.Items[0].Caption != "cap" {
		t.Errorf("manual gallery sanitize wrong: %+v", m)
	}
	if m.Columns != 3 {
		t.Errorf("columns 9 should clamp to 3, got %d", m.Columns)
	}
	// Tag: tag normalized, sort clamped, stale items cleared.
	tg := got[1].Gallery
	if tg.Mode != "tag" || tg.Tag != "pets" || tg.Sort != "newest" || len(tg.Items) != 0 {
		t.Errorf("tag gallery sanitize wrong: %+v", tg)
	}
}

func TestShareBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"share","share":{"title":" Share this ","copyLink":true,"rss":"/feeds/blog.rss"}},
		{"type":"share","share":{"email":true,"rss":"javascript:alert(1)"}},
		{"type":"share","share":{}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 share blocks (all-off dropped), got %d: %+v", len(got), got)
	}
	if got[0].Share.Title != "Share this" || !got[0].Share.CopyLink || got[0].Share.RSS != "/feeds/blog.rss" {
		t.Errorf("share sanitize wrong: %+v", got[0].Share)
	}
	// Unsafe RSS dropped, but the block is kept (email is still enabled).
	if got[1].Share.RSS != "" || !got[1].Share.Email {
		t.Errorf("unsafe RSS should be cleared, email kept: %+v", got[1].Share)
	}
}

func TestRevealModeCGroupsSanitize(t *testing.T) {
	// Groups on a reveal block are normalized + preserved (Mode C members-only unlock).
	got := parseBlocks(`[{"type":"reveal","reveal":{"content":"secret","label":"Show"},"groups":[" Members-A ","members-a",""]}]`)
	if len(got) != 1 || got[0].Reveal == nil {
		t.Fatalf("reveal not kept: %+v", got)
	}
	if strings.Join(got[0].Groups, ",") != "members-a" { // normalized + deduped
		t.Errorf("reveal groups not sanitized: %v", got[0].Groups)
	}
}

func TestOGImagePath(t *testing.T) {
	cases := map[string]string{
		"  /media/a.png  ":         "/media/a.png",             // trimmed, same-site kept
		"https://ex.example/i.jpg": "https://ex.example/i.jpg", // absolute http(s) kept
		"javascript:alert(1)":      "",                         // unsafe scheme dropped
		"//evil.example/x":         "",                         // protocol-relative dropped
		"":                         "",                         // blank
	}
	for in, want := range cases {
		if got := ogImagePath(in); got != want {
			t.Errorf("ogImagePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlocksRoundTripAndPreview(t *testing.T) {
	h := newHarness(t)

	blocks := `[{"type":"markdown","markdown":"## Section"},{"type":"youtube","youtube":{"videoId":"abc123","title":"My Talk"}}]`
	rec := h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Lead", "blocks": blocks}))
	if rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	rev, _, _ := h.st.LatestRevision(1)
	if !strings.Contains(rev.ContentJSON, `"videoId":"abc123"`) || !strings.Contains(rev.ContentJSON, `"markdown":"## Section"`) {
		t.Fatalf("blocks not persisted: %s", rev.ContentJSON)
	}

	// The editor form re-serializes the stored blocks into the hidden field.
	ed := h.get("/pages/1")
	if !strings.Contains(ed.Body.String(), `id="blocks-field"`) || !strings.Contains(ed.Body.String(), "abc123") {
		t.Errorf("edit form missing block data")
	}

	// Live preview renders both the markdown block and the youtube consent card.
	prec := h.post("/preview", h.form(map[string]string{"body": "# Lead", "blocks": blocks}))
	body := prec.Body.String()
	if prec.Code != 200 || !strings.Contains(body, ">Section<") { // auto ID + appended anchor (SPEC §6.12)
		t.Errorf("preview missing markdown block: %d", prec.Code)
	}
	if !strings.Contains(body, "External video · My Talk") || !strings.Contains(body, "/external/youtube/my-talk") {
		t.Errorf("preview missing youtube consent card:\n%s", body)
	}
}

func TestEmbedBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"embed","embed":{"provider":" Peer Tube ","title":" My Talk ","embedUrl":"https://peertube.example/embed/x"}},
		{"type":"embed","embed":{"provider":"vimeo","name":"custom","title":"T","embedUrl":"https://player.vimeo.com/video/1"}},
		{"type":"embed","embed":{"provider":"x","title":"no url"}},
		{"type":"embed","embed":{"provider":"x","title":"http not https","embedUrl":"http://insecure.example/e"}},
		{"type":"embed","embed":{"provider":"x","title":"bad scheme","embedUrl":"javascript:alert(1)"}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept embed blocks, got %d: %+v", len(got), got)
	}
	// The slug is derived from the title when not supplied; provider is kept raw
	// (the render layer slugs it for the URL/label).
	if got[0].Embed.Name != "my-talk" || got[0].Embed.EmbedURL != "https://peertube.example/embed/x" {
		t.Errorf("first embed not normalized: %+v", got[0].Embed)
	}
	if got[1].Embed.Name != "custom" {
		t.Errorf("explicit slug should be preserved: %+v", got[1].Embed)
	}
}
