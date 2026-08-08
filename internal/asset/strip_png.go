package asset

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// pngKeepChunks is the allowlist of PNG chunks to retain: the critical chunks
// plus colour/rendering and animation (APNG) chunks. Everything else — text
// (tEXt/zTXt/iTXt), timestamps (tIME), embedded EXIF (eXIf), and unknown
// chunks — is dropped as potential metadata (deny by default).
var pngKeepChunks = map[string]bool{
	"IHDR": true, "PLTE": true, "IDAT": true, "IEND": true,
	"tRNS": true, "gAMA": true, "cHRM": true, "iCCP": true, "sRGB": true,
	"sBIT": true, "pHYs": true, "bKGD": true, "hIST": true,
	"acTL": true, "fcTL": true, "fdAT": true, // APNG animation
}

// stripPNG returns a copy of a PNG keeping only allowlisted chunks. Kept chunks
// (including their CRCs) are copied verbatim, so the result is lossless and
// remains valid without any CRC recomputation.
func stripPNG(data []byte) ([]byte, []string, error) {
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, nil, fmt.Errorf("not a PNG")
	}
	out := make([]byte, 0, len(data))
	out = append(out, pngMagic...)
	var removed []string

	for i := 8; i+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[i : i+4]))
		ctype := string(data[i+4 : i+8])
		chunkEnd := i + 12 + length // len(4) + type(4) + data + crc(4)
		if length < 0 || chunkEnd > len(data) {
			return nil, nil, fmt.Errorf("bad chunk %q length at offset %d", ctype, i)
		}
		if pngKeepChunks[ctype] {
			out = append(out, data[i:chunkEnd]...)
		} else {
			removed = append(removed, ctype)
		}
		i = chunkEnd
		if ctype == "IEND" {
			break
		}
	}
	return out, removed, nil
}
