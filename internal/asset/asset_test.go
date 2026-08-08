package asset

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// --- fixtures (built in-code; no real photos or metadata) ---

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 3)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// injectAPP1 inserts an APP1 (EXIF) segment right after the SOI marker.
func injectAPP1(jpg, payload []byte) []byte {
	segLen := len(payload) + 2
	seg := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	seg = append(seg, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func makePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeChunk(ctype string, data []byte) []byte {
	c := make([]byte, 0, 12+len(data))
	c = binary.BigEndian.AppendUint32(c, uint32(len(data)))
	c = append(c, ctype...)
	c = append(c, data...)
	c = binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(append([]byte(ctype), data...)))
	return c
}

// insertAt inserts ins into data at offset at (the offset just past IHDR is 33:
// 8-byte magic + 25-byte IHDR chunk).
func insertAt(data []byte, at int, ins []byte) []byte {
	out := append([]byte{}, data[:at]...)
	out = append(out, ins...)
	return append(out, data[at:]...)
}

func webpChunk(fourcc string, payload []byte) []byte {
	c := append([]byte(nil), fourcc...)
	c = binary.LittleEndian.AppendUint32(c, uint32(len(payload)))
	c = append(c, payload...)
	if len(payload)%2 == 1 {
		c = append(c, 0)
	}
	return c
}

func makeWebP(chunks ...[]byte) []byte {
	var body bytes.Buffer
	for _, c := range chunks {
		body.Write(c)
	}
	out := make([]byte, 12+body.Len())
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+body.Len()))
	copy(out[8:12], "WEBP")
	copy(out[12:], body.Bytes())
	return out
}

// --- tests ---

func TestJPEGStripsExifButStaysValid(t *testing.T) {
	base := makeJPEG(t)
	exif := append([]byte("Exif\x00\x00"), []byte("GPSFAKE-lat-long-secret")...)
	withExif := injectAPP1(base, exif)

	a, err := Ingest("photo.jpg", withExif)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if a.Format != "jpeg" {
		t.Errorf("format = %q", a.Format)
	}
	if bytes.Contains(a.Data, []byte("GPSFAKE")) || bytes.Contains(a.Data, []byte("Exif")) {
		t.Errorf("EXIF not stripped")
	}
	if !containsStr(a.Removed, "APP1 (EXIF/XMP)") {
		t.Errorf("removed = %v, want APP1", a.Removed)
	}
	if _, format, err := image.Decode(bytes.NewReader(a.Data)); err != nil || format != "jpeg" {
		t.Errorf("cleaned JPEG no longer decodes: format=%q err=%v", format, err)
	}
}

func TestPNGStripsTextAndExifKeepsColor(t *testing.T) {
	base := makePNG(t)
	extra := makeChunk("gAMA", []byte{0, 0, 0, 1}) // colour: kept
	extra = append(extra, makeChunk("tEXt", []byte("Comment\x00secret gps"))...)
	extra = append(extra, makeChunk("eXIf", []byte("fake-exif-bytes"))...)
	withMeta := insertAt(base, 33, extra)

	a, err := Ingest("pic.png", withMeta)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if bytes.Contains(a.Data, []byte("secret gps")) || bytes.Contains(a.Data, []byte("fake-exif-bytes")) {
		t.Errorf("text/exif not stripped")
	}
	if !bytes.Contains(a.Data, []byte("gAMA")) {
		t.Errorf("colour chunk gAMA should be preserved")
	}
	if !containsStr(a.Removed, "tEXt") || !containsStr(a.Removed, "eXIf") {
		t.Errorf("removed = %v, want tEXt and eXIf", a.Removed)
	}
	if _, format, err := image.Decode(bytes.NewReader(a.Data)); err != nil || format != "png" {
		t.Errorf("cleaned PNG no longer decodes: format=%q err=%v", format, err)
	}
}

func TestWebPStripsMetadataWithWarning(t *testing.T) {
	wp := makeWebP(
		webpChunk("VP8 ", []byte("fake-vp8-pixels")),
		webpChunk("EXIF", []byte("gps-meta")),
		webpChunk("XMP ", []byte("xmp-meta")),
	)
	a, err := Ingest("x.webp", wp)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(a.Warnings) == 0 {
		t.Errorf("webp should carry a discouraged-format warning")
	}
	if bytes.Contains(a.Data, []byte("gps-meta")) || bytes.Contains(a.Data, []byte("xmp-meta")) {
		t.Errorf("EXIF/XMP not stripped from webp")
	}
	if !bytes.Contains(a.Data, []byte("fake-vp8-pixels")) {
		t.Errorf("image data chunk should be kept")
	}
	if !bytes.HasPrefix(a.Data, []byte("RIFF")) || !bytes.Equal(a.Data[8:12], []byte("WEBP")) {
		t.Errorf("output is not a valid RIFF/WEBP")
	}
	if got := binary.LittleEndian.Uint32(a.Data[4:8]); int(got) != len(a.Data)-8 {
		t.Errorf("RIFF size = %d, want %d", got, len(a.Data)-8)
	}
}

func TestSVGDelegatedToSanitizer(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="1" height="1"/></svg>`)
	a, err := Ingest("logo.svg", svg)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if a.Format != "svg" || a.MIME != "image/svg+xml" {
		t.Errorf("format/mime = %q/%q", a.Format, a.MIME)
	}
	if bytes.Contains(a.Data, []byte("<script")) {
		t.Errorf("script not stripped from SVG")
	}
	if !containsStr(a.Removed, "script") {
		t.Errorf("removed = %v, want script", a.Removed)
	}
}

func TestFailClosed(t *testing.T) {
	if _, err := Ingest("notes.txt", []byte("just some plain text, not an image")); err == nil {
		t.Errorf("unsupported format should error")
	}
	if _, err := Ingest("empty", nil); err == nil {
		t.Errorf("empty input should error")
	}
	// Malformed SVG (unclosed element) must fail closed via the sanitizer.
	if _, err := Ingest("bad.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect></svg>`)); err == nil {
		t.Errorf("malformed SVG should error")
	}
}

func TestIngestIdempotentAndVerify(t *testing.T) {
	withExif := injectAPP1(makeJPEG(t), append([]byte("Exif\x00\x00"), []byte("x")...))

	first, err := Ingest("p.jpg", withExif)
	if err != nil {
		t.Fatal(err)
	}
	// Re-ingesting already-clean data returns it unchanged.
	second, err := Ingest("p.jpg", first.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Data, second.Data) {
		t.Errorf("Ingest is not idempotent")
	}

	if err := Verify(first.Data); err != nil {
		t.Errorf("Verify(clean) = %v, want nil", err)
	}
	if err := Verify(withExif); err == nil {
		t.Errorf("Verify(dirty) should error")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
