// Package asset ingests uploaded media and strips its metadata (SPEC §6.1), so
// nothing but pixels/samples (or safe vector markup) is ever stored or served.
//
// Image formats: JPEG and PNG get a hand-rolled, lossless metadata strip (image
// data is copied verbatim; EXIF/GPS, XMP, IPTC, text, and timestamp chunks are
// dropped; JFIF/ICC/Adobe colour data is kept). SVG is delegated to the
// pbcsvgsanitize module (deny-by-default allowlist; unsanitizable SVGs are
// rejected). WebP is allowed with a warning and a best-effort chunk strip.
//
// Audio/video formats: ISO-BMFF (MP4/M4A/MOV) has its user-data and metadata
// boxes (udta, meta — GPS, iTunes tags, XMP) removed by an atom-tree rewrite,
// keeping the media/sample data verbatim; MP3 has its ID3v2 and ID3v1 tags
// stripped. Container formats we cannot yet fully strip (WebM/Matroska, Ogg,
// WAV) are rejected rather than stored with metadata intact.
//
// Ingestion is fail-closed (an unrecognized or unhandleable file is an error)
// and idempotent (re-ingesting clean data returns it unchanged), which Verify
// uses as a build-time re-check.
package asset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	svgsanitize "go.privatebychoice.com/pbcsvgsanitize"
)

// Kind classifies a cleaned asset by how it should be stored and rendered:
// images are small and go in the SQLite BLOB store; audio/video are large and
// are filesystem-backed (SPEC §6.1).
const (
	KindImage = "image"
	KindVideo = "video"
	KindAudio = "audio"
)

// Asset is a cleaned, content-addressed media file.
type Asset struct {
	Data     []byte   // cleaned bytes
	Kind     string   // "image" | "video" | "audio"
	Format   string   // "jpeg" | "png" | "svg" | "webp" | "mp4" | "mov" | "m4a" | "mp3"
	MIME     string   // media type
	SHA256   string   // hex content hash of Data (the content address)
	Removed  []string // metadata segments/chunks/elements removed (informational)
	Warnings []string // non-fatal warnings (e.g. discouraged format)
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// Ingest detects the format of data (by content, not filename) and returns a
// cleaned Asset. filename is used only for error messages.
func Ingest(filename string, data []byte) (*Asset, error) {
	if len(data) == 0 {
		return nil, errors.New("asset: empty file")
	}
	switch {
	case isJPEG(data):
		cleaned, removed, err := stripJPEG(data)
		if err != nil {
			return nil, fmt.Errorf("asset: jpeg: %w", err)
		}
		return finalize(cleaned, KindImage, "jpeg", "image/jpeg", removed, nil), nil

	case isPNG(data):
		cleaned, removed, err := stripPNG(data)
		if err != nil {
			return nil, fmt.Errorf("asset: png: %w", err)
		}
		return finalize(cleaned, KindImage, "png", "image/png", removed, nil), nil

	case isWebP(data):
		cleaned, removed, err := stripWebP(data)
		if err != nil {
			return nil, fmt.Errorf("asset: webp: %w", err)
		}
		return finalize(cleaned, KindImage, "webp", "image/webp", removed,
			[]string{"WebP accepted; JPEG, PNG, or SVG is recommended"}), nil

	case isISOBMFF(data):
		cleaned, removed, err := stripISOBMFF(data)
		if err != nil {
			return nil, fmt.Errorf("asset: mp4/mov: %w", err)
		}
		kind, format, mime := isobmffProfile(cleaned)
		return finalize(cleaned, kind, format, mime, removed, nil), nil

	case isMP3(data):
		cleaned, removed, err := stripMP3(data)
		if err != nil {
			return nil, fmt.Errorf("asset: mp3: %w", err)
		}
		return finalize(cleaned, KindAudio, "mp3", "audio/mpeg", removed, nil), nil

	case isWAV(data):
		cleaned, removed, err := stripWAV(data)
		if err != nil {
			return nil, fmt.Errorf("asset: wav: %w", err)
		}
		return finalize(cleaned, KindAudio, "wav", "audio/wav", removed, nil), nil

	case isEBML(data):
		cleaned, removed, err := stripEBML(data)
		if err != nil {
			return nil, fmt.Errorf("asset: webm/matroska: %w", err)
		}
		kind, format, mime, warnings := ebmlProfile(cleaned)
		return finalize(cleaned, kind, format, mime, removed, warnings), nil

	case isOgg(data):
		cleaned, removed, err := stripOgg(data)
		if err != nil {
			return nil, fmt.Errorf("asset: ogg: %w", err)
		}
		kind, format, mime := oggProfile(cleaned)
		return finalize(cleaned, kind, format, mime, removed, nil), nil

	case looksLikeSVG(data):
		cleaned, res, err := svgsanitize.Sanitize(data)
		if err != nil {
			return nil, fmt.Errorf("asset: svg: %w", err)
		}
		var removed []string
		removed = append(removed, res.RemovedElements...)
		removed = append(removed, res.RemovedAttributes...)
		return finalize(cleaned, KindImage, "svg", "image/svg+xml", removed, nil), nil

	case isUnsupportedContainer(data):
		return nil, fmt.Errorf("asset: %q uses a container (FLAC, AVI, or FLV) whose metadata pbcssg cannot yet strip; convert to MP4/M4A, MP3, WebM, Ogg, or WAV first", filename)

	default:
		return nil, fmt.Errorf("asset: unsupported or unrecognized media format for %q (want JPEG, PNG, SVG, WebP, MP4/M4A/MOV, MP3, WebM, Ogg, or WAV)", filename)
	}
}

// Verify reports an error unless data is already clean. Because Ingest is
// idempotent, re-ingesting clean data returns identical bytes; anything else
// means metadata is still present. The build uses this to fail-closed.
func Verify(data []byte) error {
	a, err := Ingest("", data)
	if err != nil {
		return err
	}
	if !bytes.Equal(a.Data, data) {
		return fmt.Errorf("asset: not clean (%d metadata item(s) present: %v)", len(a.Removed), a.Removed)
	}
	return nil
}

func finalize(data []byte, kind, format, mime string, removed, warnings []string) *Asset {
	sum := sha256.Sum256(data)
	return &Asset{
		Data:     data,
		Kind:     kind,
		Format:   format,
		MIME:     mime,
		SHA256:   hex.EncodeToString(sum[:]),
		Removed:  removed,
		Warnings: warnings,
	}
}

func isJPEG(d []byte) bool { return len(d) >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF }
func isPNG(d []byte) bool  { return bytes.HasPrefix(d, pngMagic) }
func isWebP(d []byte) bool {
	return len(d) >= 12 && bytes.Equal(d[0:4], []byte("RIFF")) && bytes.Equal(d[8:12], []byte("WEBP"))
}

// isMP3 reports whether data is an MP3 stream: either a leading ID3v2 tag or a
// raw MPEG-audio frame sync (0xFFEx). ISO-BMFF is checked earlier, so an .m4a is
// never misdetected here.
func isMP3(d []byte) bool {
	if bytes.HasPrefix(d, []byte("ID3")) {
		return true
	}
	return len(d) >= 2 && d[0] == 0xFF && d[1]&0xE0 == 0xE0
}

// isUnsupportedContainer reports whether data is an audio/video container whose
// metadata pbcssg does not yet strip, so Ingest can reject it with a precise
// message instead of the generic "unrecognized" error.
func isUnsupportedContainer(d []byte) bool {
	switch {
	case bytes.HasPrefix(d, []byte("fLaC")):
		return true // FLAC (metadata blocks with Vorbis comments — not yet stripped)
	case len(d) >= 12 && bytes.Equal(d[0:4], []byte("RIFF")) && bytes.Equal(d[8:12], []byte("AVI ")):
		return true // AVI
	case bytes.HasPrefix(d, []byte("FLV")):
		return true // Flash Video
	default:
		return false
	}
}

// looksLikeSVG reports whether data appears to be an SVG/XML document. The
// pbcsvgsanitize step validates that the root really is <svg>.
func looksLikeSVG(d []byte) bool {
	s := bytes.TrimLeft(bytes.TrimPrefix(d, []byte{0xEF, 0xBB, 0xBF}), " \t\r\n")
	if len(s) > 512 {
		s = s[:512]
	}
	s = bytes.ToLower(s)
	return bytes.HasPrefix(s, []byte("<?xml")) || bytes.Contains(s, []byte("<svg"))
}
