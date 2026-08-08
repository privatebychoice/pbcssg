package creator

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// pngBytes returns a small, valid, metadata-free PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngBytes2 is a distinct PNG (different pixel) so it hashes to a different
// content-addressed path than pngBytes — for tests needing two separate assets.
func pngBytes2(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, G: 100, B: 40, A: 255})
	img.Set(1, 1, color.RGBA{R: 5, G: 5, B: 5, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2 2"><rect width="2" height="2" fill="#0b5cad"/></svg>`

// upload posts a multipart file to path with the CSRF token attached.
func (h *harness) upload(path, filename string, data []byte, withCSRF bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if withCSRF {
		_ = mw.WriteField("csrf", h.c.csrf)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(data)
	mw.Close()

	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.c.ServeHTTP(rec, req)
	return rec
}

func TestMediaUploadServeAndDelete(t *testing.T) {
	h := newHarness(t)

	if rec := h.get("/admin/media"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No image media yet") {
		t.Fatalf("empty library: code=%d", rec.Code)
	}

	rec := h.upload("/admin/media", "photo.png", pngBytes(t), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Stored png") {
		t.Fatalf("upload png: code=%d\n%s", rec.Code, rec.Body.String())
	}
	assets, _ := h.st.Assets()
	if len(assets) != 1 || assets[0].Format != "png" {
		t.Fatalf("asset not stored: %+v", assets)
	}
	ref := "/media/" + assets[0].SHA256 + ".png"

	// Served with the stored MIME + nosniff.
	srec := h.get(ref)
	if srec.Code != 200 || srec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("serve png: code=%d ct=%q", srec.Code, srec.Header().Get("Content-Type"))
	}
	if srec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("media must send nosniff")
	}

	// A non-existent asset 404s.
	if rec := h.get("/media/deadbeef.png"); rec.Code != 404 {
		t.Errorf("missing media should 404, got %d", rec.Code)
	}

	// Delete removes it.
	if rec := h.post("/admin/media/"+assets[0].SHA256+"/delete", h.form(nil)); rec.Code != 303 {
		t.Fatalf("delete: code=%d", rec.Code)
	}
	if a, _ := h.st.Assets(); len(a) != 0 {
		t.Errorf("library should be empty after delete, got %d", len(a))
	}
}

func TestMediaTagsRoundTrip(t *testing.T) {
	h := newHarness(t)
	if rec := h.upload("/admin/media", "photo.png", pngBytes(t), true); rec.Code != 200 {
		t.Fatalf("upload: %d", rec.Code)
	}
	assets, _ := h.st.Assets()
	sha := assets[0].SHA256

	// Save comma-separated tags (mixed case/space) → normalized + stored.
	if rec := h.post("/admin/media/"+sha+"/tags", h.form(map[string]string{"tags": " Nature , Sunset , nature "})); rec.Code != 303 {
		t.Fatalf("save tags: %d", rec.Code)
	}
	if got, _ := h.st.MediaTags(sha); strings.Join(got, ",") != "nature,sunset" {
		t.Errorf("tags not normalized/stored: %v", got)
	}
	// The library shows the tags in the edit field.
	if body := h.get("/admin/media").Body.String(); !strings.Contains(body, `value="nature, sunset"`) {
		t.Errorf("library should show the saved tags:\n%s", body)
	}
	// Tags on a nonexistent item 404.
	if rec := h.post("/admin/media/deadbeef/tags", h.form(map[string]string{"tags": "x"})); rec.Code != 404 {
		t.Errorf("tags on missing item should 404, got %d", rec.Code)
	}
}

func TestSVGSanitizedAndIsolated(t *testing.T) {
	h := newHarness(t)
	rec := h.upload("/admin/media", "logo.svg", []byte(testSVG), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Stored svg") {
		t.Fatalf("upload svg: code=%d\n%s", rec.Code, rec.Body.String())
	}
	assets, _ := h.st.Assets()
	if len(assets) != 1 {
		t.Fatalf("svg not stored: %+v", assets)
	}
	srec := h.get("/media/" + assets[0].SHA256 + ".svg")
	if srec.Code != 200 || srec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("serve svg: code=%d ct=%q", srec.Code, srec.Header().Get("Content-Type"))
	}
	// SVG must be sandboxed as defense-in-depth on top of sanitization.
	if csp := srec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("svg CSP should sandbox, got %q", csp)
	}
}

func TestUploadRejectedAndCSRF(t *testing.T) {
	h := newHarness(t)

	// A non-image is rejected (fail-closed) and reported on the library page.
	rec := h.upload("/admin/media", "notes.txt", []byte("just text, not an image"), true)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Rejected") {
		t.Errorf("bad upload should be rejected: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if a, _ := h.st.Assets(); len(a) != 0 {
		t.Errorf("rejected upload must not be stored")
	}

	// Missing CSRF is refused.
	if rec := h.upload("/admin/media", "photo.png", pngBytes(t), false); rec.Code != 403 {
		t.Errorf("upload without CSRF should be 403, got %d", rec.Code)
	}
}
