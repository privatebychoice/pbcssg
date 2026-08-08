package asset

import (
	"bytes"
	"fmt"
)

// stripMP3 removes ID3 metadata from an MP3 stream, keeping the audio frames
// verbatim. It drops any leading ID3v2 tag(s) (which can carry cover art, GPS,
// arbitrary comments) and a trailing ID3v1 tag, so nothing identifying survives.
// It is idempotent: a stream with no tags is returned byte-for-byte unchanged.
func stripMP3(data []byte) ([]byte, []string, error) {
	var removed []string
	b := data

	// Leading ID3v2 tag(s): "ID3" + ver(2) + flags(1) + syncsafe size(4), then
	// `size` bytes of body, plus a 10-byte footer when the footer flag is set.
	for len(b) >= 10 && b[0] == 'I' && b[1] == 'D' && b[2] == '3' {
		if b[3] == 0xFF || b[4] == 0xFF { // invalid version bytes
			break
		}
		size := synchsafe(b[6:10])
		total := 10 + size
		if b[5]&0x10 != 0 { // footer present
			total += 10
		}
		if total > len(b) {
			return nil, nil, fmt.Errorf("ID3v2 tag length %d exceeds file", total)
		}
		b = b[total:]
		removed = append(removed, "ID3v2")
	}

	// Trailing ID3v1 tag: exactly 128 bytes beginning with "TAG".
	if len(b) >= 128 && bytes.Equal(b[len(b)-128:len(b)-125], []byte("TAG")) {
		b = b[:len(b)-128]
		removed = append(removed, "ID3v1")
	}

	if len(b) == 0 {
		return nil, nil, fmt.Errorf("no audio data after stripping tags")
	}

	// Copy so the result never aliases the caller's slice (and keeps idempotency
	// stable: a clean stream copies to an equal, independent buffer).
	out := make([]byte, len(b))
	copy(out, b)
	return out, removed, nil
}

// synchsafe decodes a 4-byte ID3 synchsafe integer (7 bits per byte, high bit 0).
func synchsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}
