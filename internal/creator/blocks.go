package creator

import (
	"encoding/json"
	"net/url"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// parseBlocks decodes the editor's blocks JSON field into a validated block list.
// Invalid JSON yields no blocks (the JS keeps this field well-formed); empty or
// incomplete blocks are pruned by sanitizeBlocks.
func parseBlocks(raw string) []render.Block {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var blocks []render.Block
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return nil
	}
	return sanitizeBlocks(blocks)
}

// sanitizeBlocks keeps only well-formed blocks: markdown blocks with non-empty
// text, and youtube blocks with at least a video id or title. It normalizes the
// youtube slug so the generated /external/youtube/<name> path is never empty.
func sanitizeBlocks(in []render.Block) []render.Block {
	var out []render.Block
	var seenComments bool // at most one comments widget per page (§7.3)
	for _, b := range in {
		switch b.Type {
		case "youtube":
			if b.YouTube == nil {
				continue
			}
			y := *b.YouTube
			y.VideoID = strings.TrimSpace(y.VideoID)
			y.Title = strings.TrimSpace(y.Title)
			if y.VideoID == "" && y.Title == "" {
				continue // fully empty youtube block: drop it
			}
			y.Name = strings.TrimSpace(y.Name)
			if y.Name == "" {
				if y.Name = slugify(y.Title); y.Name == "" {
					y.Name = slugify(y.VideoID)
				}
			}
			y.DescriptionLinks = validLinks(y.DescriptionLinks)
			out = append(out, render.Block{Type: "youtube", YouTube: &y})
		case "embed":
			if b.Embed == nil {
				continue
			}
			e := *b.Embed
			e.EmbedURL = strings.TrimSpace(e.EmbedURL)
			e.Title = strings.TrimSpace(e.Title)
			e.Provider = strings.TrimSpace(e.Provider)
			if !validEmbedURL(e.EmbedURL) {
				continue // no safe https embed URL: drop the block
			}
			e.Name = strings.TrimSpace(e.Name)
			if e.Name == "" {
				if e.Name = slugify(e.Title); e.Name == "" {
					e.Name = slugify(render.EmbedHost(e.EmbedURL))
				}
			}
			e.DescriptionLinks = validLinks(e.DescriptionLinks)
			out = append(out, render.Block{Type: "embed", Embed: &e})
		case "image":
			if b.Image == nil {
				continue
			}
			img := *b.Image
			img.Src = strings.TrimSpace(img.Src)
			img.Alt = strings.TrimSpace(img.Alt)
			img.Caption = strings.TrimSpace(img.Caption)
			img.Align = render.NormImageAlign(img.Align)      // "" | left | right
			img.MaxWidth = render.NormImageSize(img.MaxWidth) // "" | small | medium | large
			if !validImageSrc(img.Src) {
				continue // no usable src: drop (avoids a broken/#ZgotmplZ image)
			}
			out = append(out, render.Block{Type: "image", Image: &img, Groups: sanitizeGroups(b.Groups)})
		case "media":
			if b.Media == nil {
				continue
			}
			m := *b.Media
			m.Src = strings.TrimSpace(m.Src)
			m.Kind = render.MediaKind(m.Kind)
			m.Caption = strings.TrimSpace(m.Caption)
			m.Poster = strings.TrimSpace(m.Poster)
			if !validImageSrc(m.Src) {
				continue // no usable /media source: drop
			}
			if m.Poster != "" && !validImageSrc(m.Poster) {
				m.Poster = "" // drop an unsafe poster but keep the media
			}
			out = append(out, render.Block{Type: "media", Media: &m, Groups: sanitizeGroups(b.Groups)})
		case "callout":
			if b.Callout == nil {
				continue
			}
			co := *b.Callout
			co.Title = strings.TrimSpace(co.Title)
			if strings.TrimSpace(co.Markdown) == "" && co.Title == "" {
				continue // empty callout: drop
			}
			out = append(out, render.Block{Type: "callout", Callout: &co, Groups: sanitizeGroups(b.Groups)})
		case "citation":
			if b.Citation == nil {
				continue
			}
			ci := *b.Citation
			ci.Quote = strings.TrimSpace(ci.Quote)
			ci.Source = strings.TrimSpace(ci.Source)
			ci.URL = strings.TrimSpace(ci.URL)
			if ci.URL != "" && !validImageSrc(ci.URL) {
				ci.URL = "" // drop an unsafe source URL but keep the quote
			}
			if ci.Quote == "" {
				continue // no quote: drop
			}
			out = append(out, render.Block{Type: "citation", Citation: &ci, Groups: sanitizeGroups(b.Groups)})
		case "code":
			if b.Code == nil {
				continue
			}
			cd := *b.Code
			// Keep the code verbatim except for a trailing-newline trim (the template
			// trims too); the caption fields are single-line labels.
			cd.Text = strings.TrimRight(strings.ReplaceAll(cd.Text, "\r\n", "\n"), "\n")
			cd.Filename = capLabel(strings.TrimSpace(cd.Filename))
			cd.Language = capLabel(strings.TrimSpace(cd.Language))
			cd.Comment = strings.TrimSpace(cd.Comment)
			if strings.TrimSpace(cd.Text) == "" {
				continue // nothing to show (blank or whitespace-only): drop
			}
			out = append(out, render.Block{Type: "code", Code: &cd, Groups: sanitizeGroups(b.Groups)})
		case "details":
			if b.Details == nil {
				continue
			}
			dt := *b.Details
			dt.Summary = capLabel(strings.TrimSpace(dt.Summary))
			if strings.TrimSpace(dt.Markdown) == "" && dt.Summary == "" {
				continue // empty disclosure: drop
			}
			if dt.Summary == "" {
				dt.Summary = "Details" // a11y: <summary> must have a label
			}
			out = append(out, render.Block{Type: "details", Details: &dt, Groups: sanitizeGroups(b.Groups)})
		case "toc":
			tc := render.TOC{}
			if b.TOC != nil {
				tc = *b.TOC
			}
			tc.Title = capLabel(strings.TrimSpace(tc.Title))
			if tc.Depth < 1 || tc.Depth > 3 {
				tc.Depth = 3 // default h2..h4 (matches render.tocMaxDepth)
			}
			out = append(out, render.Block{Type: "toc", TOC: &tc})
		case "related":
			rl := render.Related{}
			if b.Related != nil {
				rl = *b.Related
			}
			rl.Title = capLabel(strings.TrimSpace(rl.Title))
			if rl.Count < 1 || rl.Count > 10 {
				rl.Count = 5 // default (matches render.relatedCountDefault)
			}
			out = append(out, render.Block{Type: "related", Related: &rl})
		case "gallery":
			g := render.Gallery{}
			if b.Gallery != nil {
				g = *b.Gallery
			}
			g.Mode = galleryMode(g.Mode)
			if g.Columns < 2 || g.Columns > 4 {
				g.Columns = render.GalleryColumnsDefault
			}
			if g.Mode == "tag" {
				g.Tag = store.NormalizeMediaTag(g.Tag)
				g.Sort = gallerySort(g.Sort)
				g.Items = nil // resolved at build; never persist stale items
				if g.Tag == "" {
					continue // tag mode needs a tag
				}
			} else {
				g.Tag, g.Sort = "", ""
				var items []render.GalleryItem
				for _, it := range g.Items {
					src := strings.TrimSpace(it.Src)
					if !validImageSrc(src) {
						continue // drop items without a usable /media source
					}
					items = append(items, render.GalleryItem{
						Src: src, Alt: strings.TrimSpace(it.Alt), Caption: strings.TrimSpace(it.Caption),
					})
				}
				if len(items) == 0 {
					continue // empty manual gallery
				}
				g.Items = items
			}
			out = append(out, render.Block{Type: "gallery", Gallery: &g, Groups: sanitizeGroups(b.Groups)})
		case "share":
			sh := render.Share{}
			if b.Share != nil {
				sh = *b.Share
			}
			sh.Title = capLabel(strings.TrimSpace(sh.Title))
			sh.RSS = strings.TrimSpace(sh.RSS)
			if sh.RSS != "" && !validFeedURL(sh.RSS) {
				sh.RSS = "" // drop an unsafe/invalid RSS pointer but keep the block
			}
			if !sh.CopyLink && !sh.Email && !sh.Mastodon && sh.RSS == "" {
				continue // a share block with nothing enabled has nothing to render
			}
			out = append(out, render.Block{Type: "share", Share: &sh})
		case "reveal":
			if b.Reveal == nil {
				continue
			}
			rv := *b.Reveal
			rv.Content = strings.TrimSpace(rv.Content)
			rv.Label = strings.TrimSpace(rv.Label)
			rv.NoScript = strings.TrimSpace(rv.NoScript)
			rv.Code = strings.TrimSpace(rv.Code) // set => Mode B code gate (§6.9)
			if rc := []rune(rv.Code); len(rc) > render.MaxRevealCode {
				rv.Code = string(rc[:render.MaxRevealCode]) // cap the gate code (mirrors the editor maxlength)
			}
			rv.Kind = render.RevealKind(rv.Kind) // "text" | "email" | "markdown"
			if rv.Content == "" {
				continue // nothing to hide: drop
			}
			if rv.Label == "" {
				rv.Label = "Reveal hidden content" // a11y: the control must have a visible label
			}
			// Groups (any) switch the reveal to Mode C (members-only keyring unlock),
			// which takes precedence over the Mode B code at render time (§6.9/§6.10).
			out = append(out, render.Block{Type: "reveal", Reveal: &rv, Groups: sanitizeGroups(b.Groups)})
		case "index":
			if b.Index == nil {
				continue
			}
			ix := *b.Index
			ix.Base = strings.TrimSpace(ix.Base)
			ix.Title = strings.TrimSpace(ix.Title)
			ix.Sort = normalizeIndexSort(ix.Sort)
			ix.Style = normalizeIndexStyle(ix.Style)
			if ix.Depth < 0 {
				ix.Depth = 0
			}
			if ix.Limit < 0 {
				ix.Limit = 0
			}
			out = append(out, render.Block{Type: "index", Index: &ix, Groups: sanitizeGroups(b.Groups)})
		case "comments":
			// A config-less mount point for the self-hosted comments widget (§7.3). It is
			// keyed by the page path at build time, so it has no author fields and is not
			// group-gateable. Keep only the first; a second would render a duplicate
			// thread for the same page.
			if seenComments {
				continue
			}
			seenComments = true
			out = append(out, render.Block{Type: "comments"})
		default: // "" or "markdown"
			if strings.TrimSpace(b.Markdown) == "" {
				continue
			}
			out = append(out, render.Block{Type: "markdown", Markdown: b.Markdown, Groups: sanitizeGroups(b.Groups)})
		}
	}
	return out
}

// sanitizeGroups normalizes an authored group-alias list for a gateable block
// (SPEC §6.10): each alias is canonicalized (store.NormalizeAlias), empties are
// dropped, and duplicates are removed while preserving first-seen order. The result
// is nil for a public block (no aliases), so the block never renders as gated.
// Unknown aliases are kept as-authored — the operator may create the group later —
// and the build surfaces any that still resolve to no key group at build time.
func sanitizeGroups(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range in {
		alias := store.NormalizeAlias(a)
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}

// capLabel normalizes a single-line caption label (a code block's filename or
// language, §6.12): it collapses any embedded newline to a space and caps the length
// so a caption bar stays a short, tidy label. html/template escapes it at render, so
// this is presentation hygiene, not a security boundary.
func capLabel(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	const max = 120
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return s
}

// validLinks keeps description-link lines that contain a valid http(s) URL,
// preserving the raw line (which may carry a leading label, e.g.
// "Docs https://example.com"). Lines with no usable URL are dropped, so nothing
// reaches html/template's URL filter to render as the cryptic "#ZgotmplZ".
func validLinks(links []string) []string {
	var out []string
	for _, l := range links {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, _, ok := render.ParseDescriptionLink(l); ok {
			out = append(out, l)
		}
	}
	return out
}

// normalizeIndexSort clamps an index block's sort to the known allowlist.
func normalizeIndexSort(s string) string {
	switch s {
	case "date-asc", "path", "title":
		return s
	default:
		return "date-desc"
	}
}

// normalizeIndexStyle clamps an index block's item style to the known allowlist.
func normalizeIndexStyle(s string) string {
	if s == "detailed" {
		return "detailed"
	}
	return "titles"
}

// galleryMode clamps a gallery block's selection mode to the allowlist (§6.14).
func galleryMode(s string) string {
	if s == "tag" {
		return "tag"
	}
	return "manual"
}

// gallerySort clamps a tag-mode gallery's ordering to the allowlist (§6.14).
func gallerySort(s string) string {
	switch s {
	case "oldest", "name":
		return s
	default:
		return "newest"
	}
}

// validEmbedURL accepts only an absolute https URL with a host — embeds are
// framed third-party content, so http (mixed content) and non-URL schemes are
// rejected. The host is separately required to be on the Settings allowlist at
// build time; this is the first, format-level guard.
func validEmbedURL(s string) bool {
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// validImageSrc accepts a same-site rooted path (e.g. /media/<sha>.<ext>) or an
// absolute http(s) URL, so nothing unsafe reaches html/template's URL filter.
func validImageSrc(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// ogImagePath normalizes a per-page social-preview image (§6.3): a trimmed same-site
// /media path or absolute http(s) URL, or "" if blank/unsafe (so an invalid value
// never reaches the og:image tag; the build then falls back to the site default).
func ogImagePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !validImageSrc(s) {
		return ""
	}
	return s
}

// validFeedURL accepts a same-site rooted path (e.g. /feeds/blog.rss) or an absolute
// http(s) URL for the share block's optional RSS pointer (§6.15), so nothing unsafe
// reaches html/template's URL filter.
func validFeedURL(s string) bool { return validImageSrc(s) }

// blocksJSON serializes a page's blocks for the editor's hidden field. An empty
// list becomes "[]" so the client JS can parse it directly.
func blocksJSON(c render.Content) string {
	if len(c.Blocks) == 0 {
		return "[]"
	}
	b, err := json.Marshal(c.Blocks)
	if err != nil {
		return "[]"
	}
	return string(b)
}
