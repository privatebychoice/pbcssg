package asset

import "bytes"

// WebM/Matroska are EBML: a tree of elements, each <id><size><data> where id and
// size are variable-length integers. Metadata lives in the Tags element and in
// Info's Title/DateUTC. Matroska is riddled with absolute byte offsets (SeekHead,
// Cues) relative to the Segment, so *removing* bytes would break seeking. Instead
// we overwrite each metadata element in place with a Void element (0xEC) of the
// exact same length and zero its payload: byte offsets stay valid, players ignore
// Void, and the identifying text is physically scrubbed from the file.

func isEBML(d []byte) bool {
	return len(d) >= 4 && d[0] == 0x1A && d[1] == 0x45 && d[2] == 0xDF && d[3] == 0xA3
}

// ebmlDrop are the elements (by full ID incl. marker bits) removed on ingest.
var ebmlDrop = map[uint64]bool{
	0x1254C367: true, // Tags (all TITLE/ARTIST/… metadata)
	0x7BA9:     true, // Info > Title
	0x4461:     true, // Info > DateUTC (muxing timestamp)
}

// ebmlRecurse are the container elements we descend into to reach the drops.
var ebmlRecurse = map[uint64]bool{
	0x18538067: true, // Segment
	0x1549A966: true, // Info
}

func ebmlName(id uint64) string {
	switch id {
	case 0x1254C367:
		return "Tags"
	case 0x7BA9:
		return "Title"
	case 0x4461:
		return "DateUTC"
	default:
		return "element"
	}
}

// ebmlVint reads a variable-length integer at i. raw keeps the leading marker bit
// (used for element IDs); sval strips it (used for data sizes). unknown reports an
// all-ones size (a streamed element that runs to the end of its parent).
func ebmlVint(d []byte, i int) (raw, sval uint64, width int, unknown, ok bool) {
	if i < 0 || i >= len(d) || d[i] == 0 {
		return 0, 0, 0, false, false
	}
	b := d[i]
	mask := byte(0x80)
	width = 1
	for b&mask == 0 {
		width++
		mask >>= 1
		if width > 8 {
			return 0, 0, 0, false, false
		}
	}
	if i+width > len(d) {
		return 0, 0, 0, false, false
	}
	raw = uint64(b)
	sval = uint64(b &^ mask)
	for k := 1; k < width; k++ {
		raw = (raw << 8) | uint64(d[i+k])
		sval = (sval << 8) | uint64(d[i+k])
	}
	unknown = sval == (uint64(1)<<(7*width))-1
	return raw, sval, width, unknown, true
}

func stripEBML(data []byte) ([]byte, []string, error) {
	buf := make([]byte, len(data))
	copy(buf, data)
	var removed []string
	ebmlWalk(buf, 0, len(buf), &removed)
	return buf, dedupStrings(removed), nil
}

// ebmlWalk processes the sibling elements in buf[start:end], voiding drops and
// recursing into containers. It tolerates truncation/garbage by stopping rather
// than erroring (the file still verifies clean if no metadata remains).
func ebmlWalk(buf []byte, start, end int, removed *[]string) {
	for i := start; i < end; {
		idRaw, _, idW, _, ok := ebmlVint(buf, i)
		if !ok {
			return
		}
		_, sz, szW, unknown, ok := ebmlVint(buf, i+idW)
		if !ok {
			return
		}
		dataStart := i + idW + szW
		dataEnd := end
		if !unknown {
			dataEnd = dataStart + int(sz)
			if dataEnd > end || dataEnd < dataStart {
				dataEnd = end // clamp defensively
			}
		}
		switch {
		case ebmlDrop[idRaw]:
			*removed = append(*removed, ebmlName(idRaw))
			voidElement(buf, i, dataEnd-i)
		case ebmlRecurse[idRaw]:
			ebmlWalk(buf, dataStart, dataEnd, removed)
		}
		if dataEnd <= i {
			return // no forward progress: stop
		}
		i = dataEnd
	}
}

// voidElement overwrites buf[start:start+total] with a Void element (0xEC) of the
// same total length and zeros its payload, so byte offsets are preserved and the
// metadata bytes are scrubbed.
func voidElement(buf []byte, start, total int) {
	if start < 0 || start+total > len(buf) {
		return
	}
	if total < 2 {
		for k := start; k < start+total; k++ {
			buf[k] = 0
		}
		return
	}
	w := 1
	for {
		payload := total - 1 - w
		if payload >= 0 && uint64(payload) <= (uint64(1)<<(7*w))-2 {
			break
		}
		if w >= 8 {
			for k := start; k < start+total; k++ {
				buf[k] = 0
			}
			return
		}
		w++
	}
	payload := total - 1 - w
	buf[start] = 0xEC
	putEBMLSize(buf, start+1, uint64(payload), w)
	for k := start + 1 + w; k < start+total; k++ {
		buf[k] = 0
	}
}

// putEBMLSize writes val as a width-byte EBML size vint (marker bit set).
func putEBMLSize(buf []byte, off int, val uint64, width int) {
	for k := width - 1; k >= 0; k-- {
		buf[off+k] = byte(val)
		val >>= 8
	}
	buf[off] |= 1 << (8 - width)
}

// ebmlProfile classifies a WebM/Matroska file (video vs audio, and webm vs the
// browser-incompatible matroska) for storage and rendering.
func ebmlProfile(d []byte) (kind, format, mime string, warnings []string) {
	head := d
	if len(head) > 4096 {
		head = head[:4096]
	}
	matroska := !bytes.Contains(head, []byte("webm")) && bytes.Contains(head, []byte("matroska"))

	// TrackType (0x83, 1-byte size 0x81, 1-byte value): 1=video, 2=audio.
	video, audio := false, false
	for i := 0; i+3 <= len(d); i++ {
		if d[i] == 0x83 && d[i+1] == 0x81 {
			switch d[i+2] {
			case 0x01:
				video = true
			case 0x02:
				audio = true
			}
		}
	}
	isAudio := audio && !video

	switch {
	case matroska && isAudio:
		return KindAudio, "mka", "audio/x-matroska", []string{"Matroska (.mka) accepted; most browsers can't play it natively — WebM, MP3, or M4A recommended"}
	case matroska:
		return KindVideo, "mkv", "video/x-matroska", []string{"Matroska (.mkv) accepted; most browsers can't play it natively — WebM or MP4 recommended"}
	case isAudio:
		return KindAudio, "weba", "audio/webm", nil
	default:
		return KindVideo, "webm", "video/webm", nil
	}
}
