package build

import (
	"fmt"
	"sort"

	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// galleryStore is the store subset PrepareGallery needs, so the build and the editor
// preview can both drive it (SPEC §6.14).
type galleryStore interface {
	AssetsByTag(tag string) ([]store.Asset, error)
	MediaNote(sha string) (string, error)
}

// PrepareGallery resolves each tag-mode gallery block into concrete items: the images
// carrying the tag, in the requested order, with alt text taken from each image's
// media note (empty when none). Manual-mode galleries are returned unchanged. It
// returns a copy of c with only tag-mode galleries rewritten (the stored content is
// untouched). The resolved <img src="/media/…"> land in the page HTML, so the normal
// media-emission + hygiene path picks them up — the gallery adds no third-party
// request.
func PrepareGallery(c render.Content, st galleryStore) (render.Content, error) {
	blocks := make([]render.Block, len(c.Blocks))
	copy(blocks, c.Blocks)
	for i := range blocks {
		if blocks[i].Type != "gallery" || blocks[i].Gallery == nil || blocks[i].Gallery.Mode != "tag" {
			continue
		}
		g := *blocks[i].Gallery
		assets, err := st.AssetsByTag(g.Tag)
		if err != nil {
			return c, fmt.Errorf("build: gallery tag %q: %w", g.Tag, err)
		}
		// AssetsByTag returns newest-first; apply the requested order.
		switch g.Sort {
		case "oldest":
			for l, r := 0, len(assets)-1; l < r; l, r = l+1, r-1 {
				assets[l], assets[r] = assets[r], assets[l]
			}
		case "name":
			sort.SliceStable(assets, func(a, b int) bool { return assets[a].Filename < assets[b].Filename })
		}
		items := make([]render.GalleryItem, 0, len(assets))
		for _, a := range assets {
			note, _ := st.MediaNote(a.SHA256)
			items = append(items, render.GalleryItem{
				Src: "/media/" + a.SHA256 + "." + imageExt(a.Format),
				Alt: note, // the library note doubles as the image description for tag mode
			})
		}
		g.Items = items
		blocks[i].Gallery = &g
	}
	c.Blocks = blocks
	return c, nil
}

// imageExt maps a stored image format to its served file extension (galleries surface
// image assets only: jpeg|png|svg|webp).
func imageExt(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}
