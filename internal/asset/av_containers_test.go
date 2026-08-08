package asset

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// --- WAV ---

func riffChunk(id string, data []byte) []byte {
	b := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(data)))
	b = append(b, data...)
	if len(data)%2 == 1 {
		b = append(b, 0) // pad to even
	}
	return b
}

func TestStripWAV(t *testing.T) {
	fmtChunk := riffChunk("fmt ", bytes.Repeat([]byte{0x01}, 16))
	dataChunk := riffChunk("data", []byte("PCMSAMPLES"))
	// LIST/INFO with an INAM (name) carrying identifying text.
	info := append([]byte("INFO"), riffChunk("INAM", []byte("SECRET-TITLE\x00"))...)
	listChunk := riffChunk("LIST", info)
	id3Chunk := riffChunk("id3 ", []byte("ID3-JUNK"))

	body := cat(fmtChunk, listChunk, dataChunk, id3Chunk)
	in := cat([]byte("RIFF"), le32(uint32(4+len(body))), []byte("WAVE"), body)

	a, err := Ingest("clip.wav", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindAudio || a.Format != "wav" {
		t.Errorf("profile: kind=%s format=%s", a.Kind, a.Format)
	}
	if bytes.Contains(a.Data, []byte("SECRET-TITLE")) || bytes.Contains(a.Data, []byte("id3 ")) {
		t.Errorf("metadata not removed:\n%q", a.Data)
	}
	for _, keep := range []string{"fmt ", "data", "PCMSAMPLES"} {
		if !bytes.Contains(a.Data, []byte(keep)) {
			t.Errorf("audio chunk %q must survive", keep)
		}
	}
	if err := Verify(a.Data); err != nil {
		t.Errorf("cleaned wav should verify clean: %v", err)
	}
	// A WAV with no metadata is returned unchanged.
	clean := cat([]byte("RIFF"), le32(uint32(4+len(fmtChunk)+len(dataChunk))), []byte("WAVE"), fmtChunk, dataChunk)
	ca, _ := Ingest("clean.wav", clean)
	if !bytes.Equal(ca.Data, clean) || len(ca.Removed) != 0 {
		t.Errorf("clean wav should be unchanged (removed=%v)", ca.Removed)
	}
}

// --- WebM (EBML) ---

func ebmlElem(id []byte, payload []byte) []byte {
	// size as a 1-byte vint (payloads here are < 127 bytes).
	return cat(id, []byte{0x80 | byte(len(payload))}, payload)
}

func TestStripWebM(t *testing.T) {
	title := ebmlElem([]byte{0x7B, 0xA9}, []byte("SECRET")) // Info > Title
	info := ebmlElem([]byte{0x15, 0x49, 0xA9, 0x66}, title)
	tracks := ebmlElem([]byte{0x16, 0x54, 0xAE, 0x6B}, []byte{0x83, 0x81, 0x01}) // TrackType=video
	tags := ebmlElem([]byte{0x12, 0x54, 0xC3, 0x67}, []byte("TITLESECRET-TAG"))  // Segment > Tags
	segment := ebmlElem([]byte{0x18, 0x53, 0x80, 0x67}, cat(info, tracks, tags))
	header := ebmlElem([]byte{0x1A, 0x45, 0xDF, 0xA3}, ebmlElem([]byte{0x42, 0x82}, []byte("webm")))
	in := cat(header, segment)

	a, err := Ingest("clip.webm", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindVideo || a.Format != "webm" || a.MIME != "video/webm" {
		t.Errorf("profile: kind=%s format=%s mime=%s", a.Kind, a.Format, a.MIME)
	}
	if len(a.Data) != len(in) {
		t.Errorf("void-strip must preserve length: got %d want %d", len(a.Data), len(in))
	}
	if bytes.Contains(a.Data, []byte("SECRET")) {
		t.Errorf("Title/Tags metadata not scrubbed:\n%q", a.Data)
	}
	if !bytes.Contains(a.Data, []byte{0xEC}) {
		t.Errorf("expected a Void element (0xEC) where metadata was removed")
	}
	if err := Verify(a.Data); err != nil {
		t.Errorf("cleaned webm should verify clean: %v", err)
	}
}

// --- Ogg (Opus) ---

func makeOggPage(htype byte, serial, seq uint32, body []byte) []byte {
	// One packet whose length < 255 → a single lacing value.
	pg := make([]byte, 0, 27+1+len(body))
	pg = append(pg, []byte("OggS")...)
	pg = append(pg, 0, htype)           // version, header type
	pg = append(pg, make([]byte, 8)...) // granule position = 0
	pg = append(pg, le32(serial)...)
	pg = append(pg, le32(seq)...)
	pg = append(pg, 0, 0, 0, 0)      // checksum (recomputed by strip)
	pg = append(pg, 1)               // one segment
	pg = append(pg, byte(len(body))) // lacing value
	pg = append(pg, body...)
	return pg
}

func TestStripOggOpus(t *testing.T) {
	head := append([]byte("OpusHead"), append([]byte{1, 2}, make([]byte, 9)...)...) // 19 bytes
	// OpusTags: magic + vendor + one user comment carrying identifying text.
	comment := []byte("TITLE=SECRET-ARTIST")
	tags := cat(
		[]byte("OpusTags"),
		le32(6), []byte("pbcssg"), // vendor
		le32(1), // one user comment
		le32(uint32(len(comment))), comment,
	)
	in := cat(makeOggPage(0x02, 42, 0, head), makeOggPage(0x00, 42, 1, tags))

	a, err := Ingest("song.opus", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindAudio || a.Format != "oga" {
		t.Errorf("profile: kind=%s format=%s", a.Kind, a.Format)
	}
	if len(a.Data) != len(in) {
		t.Errorf("comment scrub must preserve length: got %d want %d", len(a.Data), len(in))
	}
	if bytes.Contains(a.Data, []byte("SECRET-ARTIST")) {
		t.Errorf("Opus comment not scrubbed:\n%q", a.Data)
	}
	if !bytes.Contains(a.Data, []byte("pbcssg")) {
		t.Errorf("vendor string should be preserved")
	}
	// The comment page's CRC must now be valid for its bytes.
	pages, ok := parseOggPages(a.Data)
	if !ok || len(pages) != 2 {
		t.Fatalf("stripped ogg no longer parses (%d pages, ok=%v)", len(pages), ok)
	}
	pg := pages[1]
	stored := binary.LittleEndian.Uint32(a.Data[pg.off+22 : pg.off+26])
	check := make([]byte, pg.total)
	copy(check, a.Data[pg.off:pg.off+pg.total])
	for k := 22; k < 26; k++ {
		check[k] = 0
	}
	if oggCRC(check) != stored {
		t.Errorf("comment page CRC not recomputed correctly")
	}
	if err := Verify(a.Data); err != nil {
		t.Errorf("cleaned ogg should verify clean: %v", err)
	}
}

func le32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}
