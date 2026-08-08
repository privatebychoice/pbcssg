package creator

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/asset"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// mediaPageSize is how many library rows show per page; the library is split by
// type (image/video/audio), searched by filename, and paginated server-side so
// it stays fast as the library grows.
const mediaPageSize = 24

// maxUpload caps a single media upload. It is generous enough for audio/video
// (which are filesystem-backed); images are naturally far smaller. The limit
// also bounds memory during metadata-stripping ingestion.
const maxUpload = 512 << 20 // 512 MiB

// extFor maps a stored format to the URL/file extension used in /media/<sha>.<ext>.
func extFor(format string) string {
	switch format {
	case "jpeg":
		return "jpg"
	case "png", "svg", "webp", "mp4", "mov", "m4a", "mp3", "wav", "webm", "weba", "mkv", "mka", "oga", "ogv":
		return format
	default:
		return "bin"
	}
}

// mediaRefURL is the stable, content-addressed URL an author references; it is
// identical in the editor preview and the built site.
func mediaRefURL(sha, format string) string { return "/media/" + sha + "." + extFor(format) }

// mediaView is one media-library row, enriched for the template with its
// reference URL, a ready-to-copy Markdown snippet (images only), and a display
// date.
type mediaView struct {
	SHA256   string
	Filename string
	Format   string
	Kind     string // image | video | audio
	Size     int64
	Date     string // upload date (YYYY-MM-DD)
	Ref      string // /media/<sha>.<ext>
	Markdown string // ![](/media/<sha>.<ext>) — images only; empty for audio/video
	Note     string // admin context for this file (editable in the library)
	Tags     string // comma-separated media tags (editable in the library, §6.14)
}

func imageView(a store.Asset) mediaView {
	ref := mediaRefURL(a.SHA256, a.Format)
	return mediaView{
		SHA256: a.SHA256, Filename: a.Filename, Format: a.Format, Kind: asset.KindImage,
		Size: a.Size, Date: a.CreatedAt.Format("2006-01-02"), Ref: ref, Markdown: "![](" + ref + ")",
	}
}

func avView(m store.MediaFile) mediaView {
	return mediaView{
		SHA256: m.SHA256, Filename: m.Filename, Format: m.Format, Kind: m.Kind,
		Size: m.Size, Date: m.CreatedAt.Format("2006-01-02"), Ref: mediaRefURL(m.SHA256, m.Format),
	}
}

// validMediaType normalizes a requested library tab to a known type.
func validMediaType(t string) string {
	switch t {
	case asset.KindImage, asset.KindVideo, asset.KindAudio:
		return t
	default:
		return asset.KindImage
	}
}

// handleMedia renders the media library (type tab + search + page from the query).
func (c *Creator) handleMedia(w http.ResponseWriter, r *http.Request) {
	c.renderMediaLibrary(w, r, http.StatusOK, "", "", "", nil)
}

// renderMediaLibrary renders one type's page of the library. typeOverride forces
// the active tab (used after an upload to land on the item's type); otherwise the
// tab, search term, and page come from the request query.
func (c *Creator) renderMediaLibrary(w http.ResponseWriter, r *http.Request, code int, typeOverride, notice, errMsg string, warnings []string) {
	typ := validMediaType(typeOverride)
	if typeOverride == "" {
		typ = validMediaType(r.URL.Query().Get("type"))
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * mediaPageSize

	imgCount, _ := c.store.CountAssets()
	vidCount, _ := c.store.CountMedia(asset.KindVideo)
	audCount, _ := c.store.CountMedia(asset.KindAudio)

	var (
		items []mediaView
		total int
		err   error
	)
	if typ == asset.KindImage {
		var as []store.Asset
		as, total, err = c.store.AssetPage(q, mediaPageSize, offset)
		for _, a := range as {
			items = append(items, imageView(a))
		}
	} else {
		var ms []store.MediaFile
		ms, total, err = c.store.MediaPage(typ, q, mediaPageSize, offset)
		for _, m := range ms {
			items = append(items, avView(m))
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Annotate the page's rows with their admin notes in a single query.
	if len(items) > 0 {
		shas := make([]string, len(items))
		for i, it := range items {
			shas[i] = it.SHA256
		}
		if notes, nerr := c.store.MediaNotesFor(shas); nerr == nil {
			for i := range items {
				items[i].Note = notes[items[i].SHA256]
			}
		}
		if tags, terr := c.store.MediaTagsFor(shas); terr == nil {
			for i := range items {
				items[i].Tags = strings.Join(tags[items[i].SHA256], ", ")
			}
		}
	}

	totalPages := (total + mediaPageSize - 1) / mediaPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	pageURL := func(t, query string, pg int) string {
		v := url.Values{}
		v.Set("type", t)
		if query != "" {
			v.Set("q", query)
		}
		if pg > 1 {
			v.Set("page", strconv.Itoa(pg))
		}
		return "/admin/media?" + v.Encode()
	}
	data := map[string]any{
		"CSRF": c.csrf, "Type": typ, "Q": q,
		"Page": page, "TotalPages": totalPages, "Total": total,
		"Items":    items,
		"ImgCount": imgCount, "VidCount": vidCount, "AudCount": audCount,
		"Notice": notice, "Error": errMsg, "Warnings": warnings,
	}
	if page > 1 {
		data["PrevURL"] = pageURL(typ, q, page-1)
	}
	if page < totalPages {
		data["NextURL"] = pageURL(typ, q, page+1)
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "media", data)
}

// handleUpload ingests an uploaded image: it strips metadata / sanitizes the
// SVG (asset.Ingest) before anything touches the store, then content-addresses
// the cleaned bytes. The original bytes are never stored.
func (c *Creator) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	// Keep only a small part in memory; larger uploads (audio/video) spill to a
	// temp file rather than RAM.
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "upload too large or malformed (limit 512 MiB)", http.StatusRequestEntityTooLarge)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	cleaned, err := asset.Ingest(hdr.Filename, data)
	if err != nil {
		// A rejected upload (unsupported/unsanitizable) is a client error, shown
		// back on the library page.
		c.renderMediaLibrary(w, r, http.StatusBadRequest, "", "", "Rejected: "+err.Error(), nil)
		return
	}
	// Route storage by kind: small images go in the SQLite BLOB store; large
	// audio/video are filesystem-backed (only metadata is rowed).
	if cleaned.Kind == asset.KindImage {
		err = c.store.PutAsset(store.AssetData{
			Asset: store.Asset{
				SHA256:   cleaned.SHA256,
				Filename: sanitizeFilename(hdr.Filename),
				Format:   cleaned.Format,
				MIME:     cleaned.MIME,
			},
			Data: cleaned.Data,
		})
	} else {
		err = c.store.PutMedia(store.MediaFile{
			SHA256:   cleaned.SHA256,
			Filename: sanitizeFilename(hdr.Filename),
			Format:   cleaned.Format,
			MIME:     cleaned.MIME,
			Kind:     cleaned.Kind,
		}, cleaned.Data)
	}
	if err != nil {
		// A missing media directory (e.g. in-memory DB) is an operator-facing
		// configuration error, shown back on the library page.
		c.renderMediaLibrary(w, r, http.StatusBadRequest, "", "", "Could not store: "+err.Error(), nil)
		return
	}

	msg := "Stored " + cleaned.Format + " (" + cleaned.SHA256[:12] + "…)."
	if len(cleaned.Removed) > 0 {
		msg += " Stripped: " + strings.Join(cleaned.Removed, ", ") + "."
	}
	data = nil
	// Land on the uploaded item's type tab so the operator sees it (newest first).
	c.renderMediaLibrary(w, r, http.StatusOK, cleaned.Kind, msg, "", cleaned.Warnings)
}

// handleMediaDelete removes a library item — image (BLOB) or filesystem-backed
// audio/video — by content address. Both deletes are idempotent, so trying each
// safely covers whichever store holds it.
func (c *Creator) handleMediaDelete(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	sha := r.PathValue("sha")
	if err := c.store.DeleteAsset(sha); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.store.DeleteMedia(sha); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return to the tab the operator was on (preserved via a hidden form field).
	dest := "/admin/media"
	if typ := r.FormValue("type"); typ != "" {
		dest += "?type=" + url.QueryEscape(validMediaType(typ))
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleMediaNote saves (or clears) the admin note for a library item — free-text
// context such as "hero image for the privacy page". It is keyed by content
// address, so it works for images and audio/video alike; an empty note clears it.
func (c *Creator) handleMediaNote(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	sha := r.PathValue("sha")
	// Only annotate an item that exists, so a stray address can't seed an orphan note.
	ok, err := c.store.MediaExists(sha)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if rs := []rune(note); len(rs) > store.MaxMediaNote { // cap by runes, never mid-character
		note = string(rs[:store.MaxMediaNote])
	}
	if err := c.store.SetMediaNote(sha, note); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, mediaBackURL(r), http.StatusSeeOther)
}

// handleMediaTags saves (or clears) the free-form tags for a library item (§6.14).
// Tags are entered comma-separated, normalized + de-duplicated by the store, and
// keyed by content address; they never affect the served bytes.
func (c *Creator) handleMediaTags(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	sha := r.PathValue("sha")
	ok, err := c.store.MediaExists(sha)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := c.store.SetMediaTags(sha, splitList(r.FormValue("tags"))); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, mediaBackURL(r), http.StatusSeeOther)
}

// mediaBackURL rebuilds the library URL for the tab/search/page the operator was
// on, from the submitted hidden fields, so a note save returns them in place.
func mediaBackURL(r *http.Request) string {
	v := url.Values{}
	v.Set("type", validMediaType(r.FormValue("type")))
	if q := strings.TrimSpace(r.FormValue("q")); q != "" {
		v.Set("q", q)
	}
	if pg, _ := strconv.Atoi(r.FormValue("page")); pg > 1 {
		v.Set("page", strconv.Itoa(pg))
	}
	return "/admin/media?" + v.Encode()
}

// handleServeMedia serves a stored item at /media/<sha>.<ext>. Images come from
// the BLOB store; audio/video stream from the filesystem via http.ServeContent
// (so Range requests / seeking work). SVGs are served with a locked-down CSP
// (sandbox, no scripts, opaque origin) as defense in depth over the sanitizer;
// all media get nosniff.
func (c *Creator) handleServeMedia(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("name")
	if i := strings.IndexByte(sha, '.'); i >= 0 {
		sha = sha[:i] // strip the .ext; the sha is the address
	}
	a, err := c.store.Asset(sha)
	if err == nil {
		w.Header().Set("Content-Type", a.MIME)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if a.Format == "svg" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		}
		w.Write(a.Data)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Not an image — try filesystem-backed audio/video and stream it.
	f, m, err := c.store.OpenMedia(sha)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", m.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, m.SHA256+"."+extFor(m.Format), fi.ModTime(), f)
}

// sanitizeFilename keeps a display-only, path-free filename.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "upload"
	}
	return name
}
