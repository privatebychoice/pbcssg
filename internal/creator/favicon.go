package creator

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/asset"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// This file is the editor's favicon / app-icon manager (SPEC §6.11). Unlike the
// content-addressed media library, favicons have fixed, browser-expected names and
// are served from the site root, so they get their own store table + panel. Uploads
// are sanitized (SVG) / metadata-stripped (PNG) via asset.Ingest before storage; the
// .ico is validated by its magic bytes and stored as-is. The build emits the present
// assets at their root paths, generates site.webmanifest, and injects the <head>
// links on every page.

const maxFaviconUpload = 4 << 20 // favicons are tiny; 4 MiB is plenty

// faviconSlot describes one upload target.
type faviconSlot struct {
	Field  string // form field name
	Name   string // canonical filename (also the store key + served path)
	Label  string
	Hint   string
	Accept string // <input accept>
}

var faviconSlots = []faviconSlot{
	{"svg", "favicon.svg", "SVG icon", "Scalable, modern browsers. Sanitized on upload.", ".svg,image/svg+xml"},
	{"ico", "favicon.ico", "favicon.ico", "Legacy fallback; also the auto-requested /favicon.ico. Multi-size ICO.", ".ico,image/x-icon"},
	{"apple", "apple-touch-icon.png", "Apple touch icon", "180×180 PNG, square/full-bleed (iOS home screen).", ".png,image/png"},
	{"icon192", "icon-192.png", "PWA icon 192", "192×192 PNG. Enables the web manifest.", ".png,image/png"},
	{"icon512", "icon-512.png", "PWA icon 512", "512×512 PNG. Enables the web manifest.", ".png,image/png"},
}

var faviconThemeRE = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
var icoMagic = []byte{0x00, 0x00, 0x01, 0x00}

// ingestFavicon validates and cleans an uploaded favicon for a slot, returning the
// MIME type and the bytes to store. SVG/PNG go through the same sanitizer as the
// media library; the .ico is validated by magic bytes and kept as-is (it is a
// container of already-clean PNGs from the branding kit).
func ingestFavicon(slot faviconSlot, filename string, data []byte) (mime string, out []byte, err error) {
	if slot.Field == "ico" {
		if !bytes.HasPrefix(data, icoMagic) {
			return "", nil, errFavicon("not a valid .ico file")
		}
		return "image/x-icon", data, nil
	}
	a, err := asset.Ingest(filename, data)
	if err != nil {
		return "", nil, err
	}
	want := "png"
	if slot.Field == "svg" {
		want = "svg"
	}
	if a.Format != want {
		return "", nil, errFavicon("expected a " + strings.ToUpper(want) + " file")
	}
	return a.MIME, a.Data, nil
}

type faviconErr string

func (e faviconErr) Error() string { return string(e) }
func errFavicon(s string) error    { return faviconErr(s) }

// slotView is one row rendered in the panel.
type slotView struct {
	faviconSlot
	Present bool
	SrcURL  string // preview URL when present
}

func (c *Creator) handleFavicon(w http.ResponseWriter, r *http.Request) {
	c.renderFavicon(w, http.StatusOK, "", "")
}

func (c *Creator) renderFavicon(w http.ResponseWriter, code int, notice, errMsg string) {
	names, err := c.store.FaviconNames()
	if err != nil {
		http.Error(w, "favicons: "+err.Error(), http.StatusInternalServerError)
		return
	}
	present := make(map[string]bool, len(names))
	for _, n := range names {
		present[n] = true
	}
	rows := make([]slotView, len(faviconSlots))
	for i, s := range faviconSlots {
		v := slotView{faviconSlot: s, Present: present[s.Name]}
		if v.Present {
			v.SrcURL = "/admin/favicon/" + s.Name
		}
		rows[i] = v
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "favicon", map[string]any{
		"CSRF": c.csrf, "Slots": rows, "ThemeColor": c.faviconThemeColor(),
		"HasManifest": present["icon-192.png"] || present["icon-512.png"],
		"Notice":      notice, "Error": errMsg,
	})
}

// handleFaviconUpload saves the theme colour and any uploaded slot files in one
// submit; each file is validated/cleaned before storage.
func (c *Creator) handleFaviconUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFaviconUpload*int64(len(faviconSlots)+1))
	if err := r.ParseMultipartForm(maxFaviconUpload); err != nil {
		c.renderFavicon(w, http.StatusRequestEntityTooLarge, "", "Upload too large or malformed.")
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}

	// Theme colour (optional; blank clears it).
	color := strings.TrimSpace(r.FormValue("themeColor"))
	if color != "" && !faviconThemeRE.MatchString(color) {
		c.renderFavicon(w, http.StatusBadRequest, "", "Theme colour must be a hex value like #0d9488 (or blank).")
		return
	}
	if err := c.store.SetSetting(keyFaviconTheme, color); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Refresh the runtime config so the next build picks up the new theme colour
	// (the favicon assets are read from the store at build time, but the colour
	// flows through build.Config).
	if err := c.applyConfig(c.loadBuildConfig(c.cfg.Build)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var saved []string
	for _, slot := range faviconSlots {
		file, hdr, err := r.FormFile(slot.Field)
		if err != nil {
			continue // no file for this slot this time
		}
		data, rerr := io.ReadAll(file)
		file.Close()
		if rerr != nil {
			c.renderFavicon(w, http.StatusBadRequest, "", slot.Label+": read failed.")
			return
		}
		mime, clean, verr := ingestFavicon(slot, hdr.Filename, data)
		if verr != nil {
			c.renderFavicon(w, http.StatusBadRequest, "", slot.Label+": "+verr.Error())
			return
		}
		if err := c.store.PutFavicon(slot.Name, mime, clean); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		saved = append(saved, slot.Name)
	}

	notice := "Saved."
	if len(saved) > 0 {
		notice = "Saved " + strings.Join(saved, ", ") + "."
	}
	c.renderFavicon(w, http.StatusOK, notice, "")
}

func (c *Creator) handleFaviconDelete(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	name := r.PathValue("name")
	if !isFaviconName(name) {
		http.NotFound(w, r)
		return
	}
	if err := c.store.DeleteFavicon(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.renderFavicon(w, http.StatusOK, "Removed "+name+".", "")
}

// handleServeFavicon serves a stored favicon for the panel preview.
func (c *Creator) handleServeFavicon(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !isFaviconName(name) {
		http.NotFound(w, r)
		return
	}
	f, ok, err := c.store.Favicon(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", f.MIME)
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasSuffix(name, ".svg") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	_, _ = w.Write(f.Data)
}

// faviconThemeColor returns the operator's saved manifest/theme colour, or "".
func (c *Creator) faviconThemeColor() string {
	if v, ok, err := c.store.Setting(keyFaviconTheme); err == nil && ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// isFaviconName reports whether name is one of the canonical favicon slots.
func isFaviconName(name string) bool {
	for _, s := range faviconSlots {
		if s.Name == name {
			return true
		}
	}
	return false
}

// faviconThemeFromStore reads the saved favicon theme colour from a store (used by
// LoadBuildConfig, which has no *Creator).
func faviconThemeFromStore(st *store.Store) string {
	if v, ok, err := st.Setting(keyFaviconTheme); err == nil && ok {
		return strings.TrimSpace(v)
	}
	return ""
}
