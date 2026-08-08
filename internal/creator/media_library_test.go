package creator

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

func TestMediaLibraryTabsSearchPaginationCopy(t *testing.T) {
	h := newHarness(t)

	// sha pads a short id to a realistic 64-char content address (the library
	// template shortens it with slice .SHA256 0 12).
	sha := func(s string) string { return s + strings.Repeat("0", 64-len(s)) }

	// 25 images (one distinctively named) → two pages at pageSize 24.
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("photo-%02d.png", i)
		if i == 7 {
			name = "unique-kitten.png"
		}
		id := sha(fmt.Sprintf("img%02d", i))
		if err := h.st.PutAsset(store.AssetData{
			Asset: store.Asset{SHA256: id, Filename: name, Format: "png", MIME: "image/png"},
			Data:  []byte(id),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One audio + one video for the tab counts.
	if err := h.st.PutMedia(store.MediaFile{SHA256: sha("aud1"), Filename: "song.mp3", Format: "mp3", MIME: "audio/mpeg", Kind: "audio"}, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := h.st.PutMedia(store.MediaFile{SHA256: sha("vid1"), Filename: "clip.mp4", Format: "mp4", MIME: "video/mp4", Kind: "video"}, []byte("v")); err != nil {
		t.Fatal(err)
	}

	// Default = Images tab: tab counts, pagination, and image copy buttons.
	body := h.get("/admin/media").Body.String()
	for _, want := range []string{
		`media-count">25`,           // Images tab count
		`media-count">1`,            // Video/Audio counts (both 1)
		"Page 1 of 2",               // pagination over 25 at pageSize 24
		"page=2",                    // a next-page link exists
		`data-copy="![](/media/img`, // Markdown copy button for an image
		`data-copy="/media/img`,     // Path copy button
		`<script src="/admin/assets/copy.js"`,
		// The preview thumbnail links to the full-size file (opens in a new tab) so
		// a small thumbnail can be inspected at native resolution — an accessible
		// native anchor named for screen readers (the <img> alt is decorative).
		`<a class="thumb-link" href="/media/img`,
		`target="_blank" rel="noopener"`,
		`aria-label="View full-size image: photo-`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("images tab missing %q", want)
		}
	}

	// Page 2 shows the remaining item.
	p2 := h.get("/admin/media?type=image&page=2").Body.String()
	if !strings.Contains(p2, "Page 2 of 2") {
		t.Errorf("page 2 missing pager status")
	}

	// Search filters within the type by filename.
	sr := h.get("/admin/media?type=image&q=kitten").Body.String()
	if !strings.Contains(sr, "unique-kitten.png") || strings.Contains(sr, "photo-00.png") {
		t.Errorf("search did not filter to the matching filename")
	}

	// Audio tab: the mp3, a Path button but NO Markdown button (markdown image
	// syntax doesn't embed audio).
	au := h.get("/admin/media?type=audio").Body.String()
	if !strings.Contains(au, "song.mp3") || !strings.Contains(au, `data-copy="/media/aud1`) {
		t.Errorf("audio tab missing the file or its Path button")
	}
	if strings.Contains(au, "data-copy=\"![](") {
		t.Errorf("audio rows must not offer a Markdown copy button")
	}
	// Non-image rows use a glyph placeholder, not an image, so there is no
	// full-size preview link to offer.
	if strings.Contains(au, "thumb-link") {
		t.Errorf("audio rows must not render a full-size preview link")
	}
}

func TestUploadLandsOnItemTab(t *testing.T) {
	h := newHarness(t)
	clean := append([]byte{0xFF, 0xFB, 0x90, 0x00}, make([]byte, 300)...)
	rec := h.uploadFile(t, "tune.mp3", clean)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Stored mp3") {
		t.Fatalf("upload: code=%d", rec.Code)
	}
	// After uploading audio, the Audio tab is active so the operator sees it.
	if !strings.Contains(body, `class="media-tab active" href="/admin/media?type=audio"`) {
		t.Errorf("upload should land on the audio tab:\n%s", body)
	}
	if !strings.Contains(body, "tune.mp3") {
		t.Errorf("uploaded file should be listed")
	}
}

// TestMediaNoteSaveClearAndShow covers the per-item admin note: it is saved via
// the inline form, shown pre-filled on reload, cleared by an empty submit, and
// works for audio/video as well as images. A note for a nonexistent item 404s.
func TestMediaNoteSaveClearAndShow(t *testing.T) {
	h := newHarness(t)
	sha := func(s string) string { return s + strings.Repeat("0", 64-len(s)) }
	img := sha("img1")
	aud := sha("aud1")
	if err := h.st.PutAsset(store.AssetData{
		Asset: store.Asset{SHA256: img, Filename: "hero.png", Format: "png", MIME: "image/png"},
		Data:  []byte(img),
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.PutMedia(store.MediaFile{SHA256: aud, Filename: "clip.mp3", Format: "mp3", MIME: "audio/mpeg", Kind: "audio"}, []byte("a")); err != nil {
		t.Fatal(err)
	}

	// The library renders the editable note form (accessible label + input).
	body := h.get("/admin/media?type=image").Body.String()
	for _, want := range []string{
		`action="/admin/media/` + img + `/note"`,
		`class="media-note-input"`,
		`Note for hero.png`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("library missing note UI %q", want)
		}
	}

	// Save a note → redirect back to the same tab, and it shows pre-filled.
	rec := h.post("/admin/media/"+img+"/note", h.form(map[string]string{"note": "hero image for the privacy page", "type": "image"}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save note status = %d, want 303", rec.Code)
	}
	if got, _ := h.st.MediaNote(img); got != "hero image for the privacy page" {
		t.Errorf("note not stored: %q", got)
	}
	if b := h.get("/admin/media?type=image").Body.String(); !strings.Contains(b, "hero image for the privacy page") {
		t.Errorf("saved note not shown in library")
	}

	// Audio note works and lands back on the audio tab (Location preserves type).
	rec = h.post("/admin/media/"+aud+"/note", h.form(map[string]string{"note": "theme music", "type": "audio"}))
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "type=audio") {
		t.Errorf("audio note redirect lost the tab: %q", loc)
	}
	if got, _ := h.st.MediaNote(aud); got != "theme music" {
		t.Errorf("audio note not stored: %q", got)
	}

	// Empty submit clears the note.
	h.post("/admin/media/"+img+"/note", h.form(map[string]string{"note": "   ", "type": "image"}))
	if got, _ := h.st.MediaNote(img); got != "" {
		t.Errorf("empty submit should clear the note, got %q", got)
	}

	// A note for an unknown address is a 404 (no orphan note is written).
	miss := sha("nope1")
	if rec := h.post("/admin/media/"+miss+"/note", h.form(map[string]string{"note": "x", "type": "image"})); rec.Code != http.StatusNotFound {
		t.Errorf("note for missing item = %d, want 404", rec.Code)
	}
	if got, _ := h.st.MediaNote(miss); got != "" {
		t.Errorf("orphan note written for missing item: %q", got)
	}
}
