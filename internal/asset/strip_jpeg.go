package asset

import "fmt"

// stripJPEG returns a copy of a JPEG with metadata application segments (EXIF,
// XMP, IPTC, …) and comments removed. It keeps the JFIF (APP0), ICC-profile
// (APP2) and Adobe (APP14) colour segments and all structural/image data. It is
// lossless: kept segments and the entropy-coded scan are copied verbatim.
func stripJPEG(data []byte) ([]byte, []string, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, nil, fmt.Errorf("not a JPEG (missing SOI)")
	}
	out := make([]byte, 0, len(data))
	out = append(out, 0xFF, 0xD8) // SOI
	var removed []string

	for i := 2; i+1 < len(data); {
		if data[i] != 0xFF {
			return nil, nil, fmt.Errorf("invalid marker at offset %d", i)
		}
		marker := data[i+1]

		// Skip 0xFF fill bytes between markers.
		if marker == 0xFF {
			i++
			continue
		}
		// Start of scan / end of image: copy the remainder verbatim.
		if marker == 0xDA || marker == 0xD9 {
			out = append(out, data[i:]...)
			return out, removed, nil
		}
		// Standalone markers with no payload (RSTn, TEM).
		if (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			out = append(out, 0xFF, marker)
			i += 2
			continue
		}
		// Length-prefixed segment (length includes its own 2 bytes).
		if i+4 > len(data) {
			return nil, nil, fmt.Errorf("truncated segment header at offset %d", i)
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 || i+2+segLen > len(data) {
			return nil, nil, fmt.Errorf("bad segment length %d at offset %d", segLen, i)
		}
		segEnd := i + 2 + segLen
		if keepJPEGSegment(marker) {
			out = append(out, data[i:segEnd]...)
		} else {
			removed = append(removed, jpegMarkerName(marker))
		}
		i = segEnd
	}
	return out, removed, nil
}

// keepJPEGSegment reports whether a marker segment is kept. Metadata application
// segments and comments are dropped; colour and structural segments are kept.
func keepJPEGSegment(marker byte) bool {
	switch marker {
	case 0xE0, 0xE2, 0xEE: // APP0 (JFIF), APP2 (ICC), APP14 (Adobe)
		return true
	}
	if marker >= 0xE1 && marker <= 0xEF { // other APPn: EXIF/XMP/IPTC/vendor metadata
		return false
	}
	if marker == 0xFE { // COM (comment)
		return false
	}
	return true // DQT, DHT, SOF, DRI, … structural
}

func jpegMarkerName(marker byte) string {
	switch marker {
	case 0xE1:
		return "APP1 (EXIF/XMP)"
	case 0xED:
		return "APP13 (IPTC)"
	case 0xFE:
		return "COM (comment)"
	}
	if marker >= 0xE0 && marker <= 0xEF {
		return fmt.Sprintf("APP%d", marker-0xE0)
	}
	return fmt.Sprintf("marker 0x%02X", marker)
}
