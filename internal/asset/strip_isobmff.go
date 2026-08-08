package asset

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// ISO Base Media File Format (MP4/M4A/MOV) is a tree of boxes ("atoms"), each
// [size uint32][type 4]. Metadata lives in `udta` (user data — GPS "©xyz",
// QuickTime tags) and `meta` (iTunes ilst tags, XMP) boxes. We rewrite the tree,
// dropping every udta/meta box and copying all media/sample boxes verbatim, so
// playback is untouched but nothing identifying survives.

// isobmffContainers are the pure container boxes we recurse into. Crucially none
// of these are FullBoxes (no version/flags or leading fields before their child
// boxes), so rebuilding them from their children cannot corrupt a header. Boxes
// not listed here (incl. FullBoxes like stsd/dref/meta) are treated as leaves.
var isobmffContainers = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true,
	"edts": true, "dinf": true, "mvex": true, "moof": true, "traf": true,
	"mfra": true, "gmhd": true,
}

// isobmffDrop are the metadata boxes removed wherever they appear.
var isobmffDrop = map[string]bool{"udta": true, "meta": true}

func isISOBMFF(d []byte) bool {
	// The first box is normally ftyp; its type field sits at bytes 4:8.
	return len(d) >= 8 && bytes.Equal(d[4:8], []byte("ftyp"))
}

// stripISOBMFF rewrites the box tree with all udta/meta boxes removed. A file
// with no such boxes is returned byte-for-byte unchanged (idempotent).
func stripISOBMFF(data []byte) ([]byte, []string, error) {
	var removed []string
	out, err := processBoxes(data, &removed)
	if err != nil {
		return nil, nil, err
	}
	return out, dedupStrings(removed), nil
}

// processBoxes rewrites a sequence of sibling boxes.
func processBoxes(b []byte, removed *[]string) ([]byte, error) {
	var out bytes.Buffer
	for i := 0; i < len(b); {
		if i+8 > len(b) {
			return nil, fmt.Errorf("truncated box header at %d", i)
		}
		size32 := binary.BigEndian.Uint32(b[i : i+4])
		typ := string(b[i+4 : i+8])
		hdr := 8
		boxLen := int(size32)
		switch size32 {
		case 0:
			boxLen = len(b) - i // extends to end of file (e.g. final mdat)
		case 1:
			if i+16 > len(b) {
				return nil, fmt.Errorf("truncated 64-bit box header at %d", i)
			}
			boxLen = int(binary.BigEndian.Uint64(b[i+8 : i+16]))
			hdr = 16
		}
		if boxLen < hdr || i+boxLen > len(b) {
			return nil, fmt.Errorf("box %q length %d out of range at %d", typ, boxLen, i)
		}
		box := b[i : i+boxLen]

		switch {
		case isobmffDrop[typ]:
			*removed = append(*removed, typ)
		case isobmffContainers[typ]:
			child, err := processBoxes(box[hdr:], removed)
			if err != nil {
				return nil, err
			}
			writeBox(&out, typ, hdr, child)
		default:
			out.Write(box) // leaf: copy verbatim
		}
		i += boxLen
	}
	return out.Bytes(), nil
}

// writeBox writes a container box header for the given type and child payload,
// preserving the original header width (32- vs 64-bit) when it still fits, so an
// unchanged subtree round-trips to identical bytes.
func writeBox(out *bytes.Buffer, typ string, origHdr int, child []byte) {
	var hdr [16]byte
	if origHdr == 8 && 8+len(child) <= 0xFFFFFFFF {
		binary.BigEndian.PutUint32(hdr[0:4], uint32(8+len(child)))
		copy(hdr[4:8], typ)
		out.Write(hdr[:8])
	} else {
		// 64-bit box: size=1 marker, then a 16-byte header carrying the true length.
		binary.BigEndian.PutUint32(hdr[0:4], 1)
		copy(hdr[4:8], typ)
		binary.BigEndian.PutUint64(hdr[8:16], uint64(16+len(child)))
		out.Write(hdr[:16])
	}
	out.Write(child)
}

// isobmffProfile classifies a cleaned ISO-BMFF file as video or audio and picks
// its format/MIME, preferring video when a video track is present.
func isobmffProfile(d []byte) (kind, format, mime string) {
	video, audio := false, false
	// Scan for handler boxes: "hdlr" + ver/flags(4) + pre_defined(4) + handler(4).
	for i := 0; i+16 <= len(d); {
		j := bytes.Index(d[i:], []byte("hdlr"))
		if j < 0 {
			break
		}
		p := i + j
		if p+12+4 <= len(d) {
			switch string(d[p+12 : p+16]) {
			case "vide":
				video = true
			case "soun":
				audio = true
			}
		}
		i = p + 4
	}
	quicktime := bytes.Equal(d[8:min(12, len(d))], []byte("qt  "))
	switch {
	case video:
		if quicktime {
			return KindVideo, "mov", "video/quicktime"
		}
		return KindVideo, "mp4", "video/mp4"
	case audio:
		return KindAudio, "m4a", "audio/mp4"
	default:
		// No handler found (unusual): treat as video/mp4, the safe general default.
		return KindVideo, "mp4", "video/mp4"
	}
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
