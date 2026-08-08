package creator

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// pngBytes is defined in media_test.go (a tiny clean PNG).

// postFavicon builds a multipart POST to /admin/favicon with the given file slots
// (field -> bytes) and theme colour, and returns the recorder.
func postFavicon(h *harness, files map[string][]byte, themeColor string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", h.c.csrf)
	if themeColor != "" {
		mw.WriteField("themeColor", themeColor)
	}
	for field, data := range files {
		fw, _ := mw.CreateFormFile(field, field+".bin")
		fw.Write(data)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/admin/favicon", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.c.ServeHTTP(rec, req)
	return rec
}

func TestFaviconUploadServeDelete(t *testing.T) {
	h := newHarness(t)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16"/></svg>`)
	ico := append([]byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}, make([]byte, 32)...)

	rec := postFavicon(h, map[string][]byte{
		"svg":   svg,
		"ico":   ico,
		"apple": pngBytes(t),
	}, "#0d9488")
	if rec.Code != 200 {
		t.Fatalf("upload: code=%d\n%s", rec.Code, rec.Body.String())
	}

	names, _ := h.st.FaviconNames()
	got := strings.Join(names, ",")
	for _, want := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png"} {
		if !strings.Contains(got, want) {
			t.Errorf("slot %q not stored (have %q)", want, got)
		}
	}
	// Theme colour persisted.
	if h.c.faviconThemeColor() != "#0d9488" {
		t.Errorf("theme colour not saved: %q", h.c.faviconThemeColor())
	}

	// Serve endpoint returns the stored SVG with the right content type.
	sv := h.get("/admin/favicon/favicon.svg")
	if sv.Code != 200 || sv.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("serve svg: code=%d type=%q", sv.Code, sv.Header().Get("Content-Type"))
	}
	// Unknown name is 404.
	if h.get("/admin/favicon/nope.png").Code != 404 {
		t.Error("unknown favicon name should 404")
	}

	// The panel shows the present slots + preview.
	page := h.get("/admin/favicon").Body.String()
	if !strings.Contains(page, `src="/admin/favicon/favicon.svg"`) {
		t.Errorf("panel missing svg preview")
	}

	// Delete a slot.
	del := h.post("/admin/favicon/favicon.svg/delete", h.form(nil))
	if del.Code != 200 {
		t.Fatalf("delete: %d", del.Code)
	}
	if names, _ := h.st.FaviconNames(); strings.Contains(strings.Join(names, ","), "favicon.svg") {
		t.Error("favicon.svg not deleted")
	}
}

func TestFaviconUploadValidation(t *testing.T) {
	h := newHarness(t)

	// A PNG in the SVG slot is rejected (wrong format).
	if rec := postFavicon(h, map[string][]byte{"svg": pngBytes(t)}, ""); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "SVG") {
		t.Errorf("PNG-in-SVG-slot should be rejected: code=%d", rec.Code)
	}
	// Non-ICO bytes in the ICO slot are rejected.
	if rec := postFavicon(h, map[string][]byte{"ico": []byte("not an ico")}, ""); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "ico") {
		t.Errorf("bad ICO should be rejected: code=%d", rec.Code)
	}
	// A bad theme colour is rejected.
	if rec := postFavicon(h, map[string][]byte{}, "teal"); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "hex") {
		t.Errorf("bad theme colour should be rejected: code=%d", rec.Code)
	}
}
