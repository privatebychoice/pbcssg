package creator

import (
	"fmt"

	"go.privatebychoice.com/pbcssg/internal/render"
)

// mediaKind classifies a media reference's extension into a human word (image,
// video, audio, or the generic "file") for a friendly broken-reference message.
func mediaKind(ext string) string {
	switch ext {
	case "jpg", "jpeg", "png", "svg", "webp", "gif":
		return "image"
	case "mp4", "mov", "webm", "mkv", "ogv":
		return "video"
	case "mp3", "m4a", "wav", "weba", "oga", "mka":
		return "audio"
	default:
		return "file"
	}
}

// mediaRefLabel renders a short, verifiable label for a broken media reference —
// its kind and a shortened content address — e.g. "video /media/abcd1234…ef.mp4".
func mediaRefLabel(ref render.MediaRef) string {
	sha := ref.SHA
	if len(sha) > 12 {
		sha = sha[:8] + "…" + sha[len(sha)-2:]
	}
	return fmt.Sprintf("%s /media/%s.%s", mediaKind(ref.Ext), sha, ref.Ext)
}

// blockMediaRefs returns every same-site /media/<sha>.<ext> reference a block
// carries, across its media-bearing fields (image/media/poster paths) and its
// markdown-bearing fields (markdown/callout/citation/transcript bodies), so a
// broken reference can be attributed to the exact block.
func blockMediaRefs(b render.Block) []render.MediaRef {
	var buf []byte
	add := func(s string) {
		if s != "" {
			buf = append(buf, ' ')
			buf = append(buf, s...)
		}
	}
	switch b.Type {
	case "image":
		if b.Image != nil {
			add(b.Image.Src)
		}
	case "media":
		if b.Media != nil {
			add(b.Media.Src)
			add(b.Media.Poster)
		}
	case "youtube":
		if b.YouTube != nil {
			add(b.YouTube.Poster)
			add(b.YouTube.Transcript)
		}
	case "embed":
		if b.Embed != nil {
			add(b.Embed.Poster)
			add(b.Embed.Transcript)
		}
	case "callout":
		if b.Callout != nil {
			add(b.Callout.Markdown)
		}
	case "citation":
		if b.Citation != nil {
			add(b.Citation.Quote)
		}
	case "index":
		// no media
	default: // markdown / ""
		add(b.Markdown)
	}
	return render.MediaRefs(buf)
}

// mediaScan is the editor's broken-media analysis of a draft: the human labels
// for the panel, plus which sources — the markdown body and each content block
// (by index) — carry a broken reference, so the editor UI can flag the exact
// field a visitor's broken image/video/audio comes from.
type mediaScan struct {
	Labels []string     // deduped "kind /media/…" labels for the panel
	Body   bool         // the markdown body references broken media
	Blocks map[int]bool // block index -> references broken media
}

// scanBrokenMedia checks every local media reference in a draft against the
// store, grouped by source (body / block) for per-source UI attribution. A
// reference not in the library is "broken" — advisory, never blocking, since a
// file may be referenced before it is uploaded (SPEC §6.1).
func (c *Creator) scanBrokenMedia(content render.Content) (mediaScan, error) {
	res := mediaScan{Blocks: map[int]bool{}}
	seen := map[string]bool{} // dedupe labels across sources

	// check reports whether any of refs is missing from the store, collecting a
	// deduped label for each distinct broken reference.
	check := func(refs []render.MediaRef) (bool, error) {
		broken := false
		for _, ref := range refs {
			ok, err := c.store.MediaExists(ref.SHA)
			if err != nil {
				return false, err
			}
			if !ok {
				broken = true
				if lbl := mediaRefLabel(ref); !seen[lbl] {
					seen[lbl] = true
					res.Labels = append(res.Labels, lbl)
				}
			}
		}
		return broken, nil
	}

	body, err := check(render.MediaRefs([]byte(content.Body)))
	if err != nil {
		return res, err
	}
	res.Body = body

	for i, blk := range content.Blocks {
		refs := blockMediaRefs(blk)
		if len(refs) == 0 {
			continue
		}
		broken, err := check(refs)
		if err != nil {
			return res, err
		}
		if broken {
			res.Blocks[i] = true
		}
	}
	return res, nil
}

// brokenMedia returns just the human labels for a draft's broken media references
// (the save-time advisory list). It renders no HTML; scanBrokenMedia does the work.
func (c *Creator) brokenMedia(cj string) ([]string, error) {
	content, err := render.Parse(cj)
	if err != nil {
		return nil, err
	}
	ms, err := c.scanBrokenMedia(content)
	if err != nil {
		return nil, err
	}
	return ms.Labels, nil
}
