package asset

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// box builds one ISO-BMFF box: size(4) + type(4) + payload.
func box(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[:4], uint32(8+len(payload)))
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// hdlr builds a minimal handler box with the given handler type ("vide"/"soun").
func hdlr(handler string) []byte {
	p := make([]byte, 0, 24)
	p = append(p, 0, 0, 0, 0)         // version + flags
	p = append(p, 0, 0, 0, 0)         // pre_defined
	p = append(p, []byte(handler)...) // handler_type
	p = append(p, 0, 0, 0, 0)         // reserved[0]
	p = append(p, 0, 0, 0, 0)         // reserved[1]
	p = append(p, 0, 0, 0, 0)         // reserved[2]
	p = append(p, 0)                  // name (empty, null-terminated)
	return box("hdlr", p)
}

func TestStripISOBMFFRemovesMetadata(t *testing.T) {
	ftyp := box("ftyp", []byte("isom\x00\x00\x02\x00isommp42"))
	// udta holds a QuickTime GPS/name tag — this must be removed.
	udta := box("udta", box("\xa9nam", []byte("Home Video at 41.8,-87.6")))
	mdia := box("mdia", hdlr("vide"))
	trak := box("trak", mdia)
	moov := box("moov", cat(udta, trak))
	mdat := box("mdat", []byte("RAWVIDEOSAMPLEBYTES"))
	in := cat(ftyp, moov, mdat)

	a, err := Ingest("clip.mp4", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindVideo || a.Format != "mp4" || a.MIME != "video/mp4" {
		t.Errorf("profile wrong: kind=%s format=%s mime=%s", a.Kind, a.Format, a.MIME)
	}
	if bytes.Contains(a.Data, []byte("udta")) || bytes.Contains(a.Data, []byte("Home Video")) {
		t.Errorf("metadata not removed:\n%q", a.Data)
	}
	if !bytes.Contains(a.Data, []byte("RAWVIDEOSAMPLEBYTES")) {
		t.Errorf("media samples must be preserved verbatim")
	}
	if !bytes.Contains(a.Data, []byte("ftyp")) || !bytes.Contains(a.Data, []byte("mdat")) {
		t.Errorf("structural boxes must survive")
	}
	// Idempotent + build-time Verify passes on the cleaned bytes.
	if err := Verify(a.Data); err != nil {
		t.Errorf("cleaned mp4 should verify clean: %v", err)
	}
}

func TestStripISOBMFFCleanIsUnchanged(t *testing.T) {
	ftyp := box("ftyp", []byte("mp42\x00\x00\x00\x00mp42isom"))
	moov := box("moov", box("trak", box("mdia", hdlr("soun"))))
	mdat := box("mdat", []byte("AUDIOSAMPLES"))
	in := cat(ftyp, moov, mdat)

	a, err := Ingest("song.m4a", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindAudio || a.Format != "m4a" || a.MIME != "audio/mp4" {
		t.Errorf("audio profile wrong: kind=%s format=%s mime=%s", a.Kind, a.Format, a.MIME)
	}
	// No udta/meta present → returned byte-for-byte unchanged.
	if !bytes.Equal(a.Data, in) {
		t.Errorf("clean ISO-BMFF must be unchanged by strip")
	}
	if len(a.Removed) != 0 {
		t.Errorf("nothing should be removed from a clean file, got %v", a.Removed)
	}
}

func TestStripMP3(t *testing.T) {
	// ID3v2: "ID3" ver(2) flags(1) synchsafe-size(4) + body.
	body := []byte("TIT2\x00\x00\x00\x06\x00\x00secret") // a title frame (10 bytes body)
	id3v2 := cat([]byte("ID3\x03\x00\x00"), synchsafeBytes(len(body)), body)
	frame := append([]byte{0xFF, 0xFB, 0x90, 0x00}, bytes.Repeat([]byte{0x11}, 60)...)
	id3v1 := append([]byte("TAG"), bytes.Repeat([]byte{0x20}, 125)...) // 128 bytes total
	in := cat(id3v2, frame, id3v1)

	a, err := Ingest("song.mp3", in)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if a.Kind != KindAudio || a.Format != "mp3" || a.MIME != "audio/mpeg" {
		t.Errorf("mp3 profile wrong: %+v", a)
	}
	if !bytes.Equal(a.Data, frame) {
		t.Errorf("mp3 strip should leave exactly the audio frames:\ngot  %q\nwant %q", a.Data, frame)
	}
	if bytes.Contains(a.Data, []byte("secret")) || bytes.Contains(a.Data, []byte("TAG")) {
		t.Errorf("ID3 metadata not fully removed")
	}
	if err := Verify(a.Data); err != nil {
		t.Errorf("cleaned mp3 should verify clean: %v", err)
	}
}

func TestUnsupportedContainersRejected(t *testing.T) {
	cases := map[string][]byte{
		"flac": []byte("fLaC\x00\x00\x00\x22"),
		"avi":  []byte("RIFF\x24\x00\x00\x00AVI LIST"),
		"flv":  []byte("FLV\x01\x05\x00\x00\x00\x09"),
	}
	for name, data := range cases {
		if _, err := Ingest(name, data); err == nil {
			t.Errorf("%s should be rejected", name)
		} else if !bytes.Contains([]byte(err.Error()), []byte("cannot yet strip")) {
			t.Errorf("%s error should explain the limitation, got: %v", name, err)
		}
	}
}

// synchsafeBytes encodes n as a 4-byte ID3 synchsafe integer (test helper).
func synchsafeBytes(n int) []byte {
	return []byte{
		byte(n>>21) & 0x7f, byte(n>>14) & 0x7f, byte(n>>7) & 0x7f, byte(n) & 0x7f,
	}
}
