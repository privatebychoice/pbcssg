package asset

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// stripWebP returns a copy of a WebP with the EXIF and XMP metadata chunks
// removed and the corresponding VP8X feature flags cleared. Image data chunks
// (VP8/VP8L/VP8X/ALPH/ANIM/ANMF/ICCP) are kept. The RIFF size is corrected.
func stripWebP(data []byte) ([]byte, []string, error) {
	if len(data) < 12 || !bytes.Equal(data[0:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return nil, nil, fmt.Errorf("not a WebP")
	}
	body := data[12:]
	var kept bytes.Buffer
	var removed []string

	for i := 0; i+8 <= len(body); {
		fourcc := string(body[i : i+4])
		size := int(binary.LittleEndian.Uint32(body[i+4 : i+8]))
		padded := size + (size & 1) // chunk payloads are padded to an even length
		chunkEnd := i + 8 + padded
		if size < 0 || chunkEnd > len(body) {
			return nil, nil, fmt.Errorf("bad WebP chunk %q at offset %d", fourcc, i)
		}

		switch fourcc {
		case "EXIF", "XMP ":
			removed = append(removed, strings.TrimSpace(fourcc))
		default:
			chunk := append([]byte(nil), body[i:chunkEnd]...)
			// VP8X flags byte is the first payload byte; clear EXIF (bit 3) and XMP (bit 2).
			if fourcc == "VP8X" && len(chunk) >= 9 {
				chunk[8] &^= 0x08 | 0x04
			}
			kept.Write(chunk)
		}
		i = chunkEnd
	}

	out := make([]byte, 12+kept.Len())
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+kept.Len())) // "WEBP" + body
	copy(out[8:12], "WEBP")
	copy(out[12:], kept.Bytes())
	return out, removed, nil
}
