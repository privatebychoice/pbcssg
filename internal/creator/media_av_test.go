package creator

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/asset"
)

// uploadFile posts a multipart file upload to /admin/media with the CSRF token.
func (h *harness) uploadFile(t *testing.T, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", h.c.csrf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest("POST", "/admin/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.c.ServeHTTP(rec, req)
	return rec
}

func TestAudioUploadIsFilesystemBacked(t *testing.T) {
	h := newHarness(t)
	clean := append([]byte{0xFF, 0xFB, 0x90, 0x00}, bytes.Repeat([]byte{0x22}, 400)...)
	a, err := asset.Ingest("clip.mp3", clean)
	if err != nil {
		t.Fatal(err)
	}

	rec := h.uploadFile(t, "clip.mp3", clean)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Stored mp3") {
		t.Fatalf("upload: code=%d\n%s", rec.Code, rec.Body.String())
	}

	// It landed in the filesystem media store, not the image BLOB store.
	if list, _ := h.st.MediaList(); len(list) != 1 || list[0].Kind != "audio" {
		t.Fatalf("audio not filesystem-backed: %+v", list)
	}
	if assets, _ := h.st.Assets(); len(assets) != 0 {
		t.Errorf("audio must not be stored as a BLOB asset")
	}

	// It streams back with the right type and honours a Range request (seeking).
	full := h.get("/media/" + a.SHA256 + ".mp3")
	if full.Code != 200 || full.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("serve: code=%d type=%q", full.Code, full.Header().Get("Content-Type"))
	}
	rreq := httptest.NewRequest("GET", "/media/"+a.SHA256+".mp3", nil)
	rreq.Header.Set("Range", "bytes=0-9")
	rrec := httptest.NewRecorder()
	h.c.ServeHTTP(rrec, rreq)
	if rrec.Code != 206 || rrec.Body.Len() != 10 {
		t.Errorf("Range request should yield 206 partial content, got %d len=%d", rrec.Code, rrec.Body.Len())
	}

	// Delete removes it from the filesystem store.
	if del := h.post("/admin/media/"+a.SHA256+"/delete", h.form(nil)); del.Code != 303 {
		t.Fatalf("delete: %d", del.Code)
	}
	if list, _ := h.st.MediaList(); len(list) != 0 {
		t.Errorf("media not deleted: %+v", list)
	}
}

func TestMediaBlockSanitize(t *testing.T) {
	raw := `[
		{"type":"media","media":{"kind":"AUDIO","src":"/media/abc.mp3","caption":" hi "}},
		{"type":"media","media":{"kind":"video","src":"/media/x.mp4","poster":"javascript:alert(1)"}},
		{"type":"media","media":{"kind":"video","src":""}},
		{"type":"media","media":{"kind":"video","src":"javascript:alert(1)"}}
	]`
	got := parseBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 kept media blocks, got %d: %+v", len(got), got)
	}
	if got[0].Media.Kind != "audio" || got[0].Media.Src != "/media/abc.mp3" || got[0].Media.Caption != "hi" {
		t.Errorf("first media not normalized: %+v", got[0].Media)
	}
	// Unsafe poster dropped, but the media kept.
	if got[1].Media.Poster != "" || got[1].Media.Src != "/media/x.mp4" {
		t.Errorf("unsafe poster should be cleared: %+v", got[1].Media)
	}
}
