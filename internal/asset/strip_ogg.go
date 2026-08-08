package asset

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Ogg is a stream of pages, each carrying fragments of packets for one or more
// logical bitstreams. Metadata lives in each stream's comment header packet (the
// Vorbis comment "\x03vorbis" or Opus "OpusTags" packet): a vendor string plus
// user comments (TITLE/ARTIST/…, and possibly base64 cover art). We keep the
// vendor, set the user-comment count to 0, and zero the trailing comment bytes —
// keeping every byte length identical so pages need no re-pagination, only a
// recomputed CRC. Idempotent: a header already at count 0 is left untouched.

func isOgg(d []byte) bool { return bytes.HasPrefix(d, []byte("OggS")) }

type oggPage struct {
	off, total       int
	bodyOff, bodyLen int
	serial           uint32
	lacing           []byte
}

type oggSpan struct{ off, len int }

func parseOggPages(d []byte) ([]oggPage, bool) {
	var pages []oggPage
	i := 0
	for i+27 <= len(d) {
		if !bytes.Equal(d[i:i+4], []byte("OggS")) {
			return pages, false
		}
		nseg := int(d[i+26])
		if i+27+nseg > len(d) {
			return pages, false
		}
		lacing := d[i+27 : i+27+nseg]
		bodyLen := 0
		for _, s := range lacing {
			bodyLen += int(s)
		}
		bodyOff := i + 27 + nseg
		if bodyOff+bodyLen > len(d) {
			return pages, false
		}
		pages = append(pages, oggPage{
			off: i, total: 27 + nseg + bodyLen, bodyOff: bodyOff, bodyLen: bodyLen,
			serial: binary.LittleEndian.Uint32(d[i+14 : i+18]), lacing: lacing,
		})
		i = bodyOff + bodyLen
	}
	return pages, i == len(d)
}

func stripOgg(data []byte) ([]byte, []string, error) {
	pages, ok := parseOggPages(data)
	if !ok || len(pages) == 0 {
		return nil, nil, fmt.Errorf("malformed Ogg stream")
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	var removed []string
	var modRanges []oggSpan

	// Reassemble packets per logical stream (a packet spans segments until one is
	// <255), and process the first comment header of each stream.
	type acc struct {
		spans []oggSpan
		done  bool
	}
	streams := map[uint32]*acc{}
	for _, pg := range pages {
		st := streams[pg.serial]
		if st == nil {
			st = &acc{}
			streams[pg.serial] = st
		}
		pos := pg.bodyOff
		for _, seg := range pg.lacing {
			st.spans = append(st.spans, oggSpan{pos, int(seg)})
			pos += int(seg)
			if seg < 255 { // packet complete
				if !st.done {
					if mod, isComment := processOggComment(buf, st.spans, &removed); isComment {
						modRanges = append(modRanges, mod...)
						st.done = true
					}
				}
				st.spans = nil
			}
		}
	}

	for _, pg := range pages {
		if oggOverlaps(pg.off, pg.total, modRanges) {
			recomputeOggCRC(buf, pg.off, pg.total)
		}
	}
	return buf, dedupStrings(removed), nil
}

// processOggComment reassembles a packet from its body spans; if it is a comment
// header it scrubs the user comments in place and reports the modified spans. The
// second return is whether the packet was a comment header at all.
func processOggComment(buf []byte, spans []oggSpan, removed *[]string) ([]oggSpan, bool) {
	total := 0
	for _, s := range spans {
		total += s.len
	}
	if total < 12 {
		return nil, false
	}
	pkt := make([]byte, 0, total)
	for _, s := range spans {
		pkt = append(pkt, buf[s.off:s.off+s.len]...)
	}

	base, vorbis := 0, false
	switch {
	case bytes.HasPrefix(pkt, []byte("\x03vorbis")):
		base, vorbis = 7, true
	case bytes.HasPrefix(pkt, []byte("OpusTags")):
		base = 8
	default:
		return nil, false
	}

	if base+4 > total {
		return nil, false
	}
	vlen := int(binary.LittleEndian.Uint32(pkt[base : base+4]))
	q := base + 4 + vlen // offset of the user-comment count
	if vlen < 0 || q+4 > total {
		return nil, false
	}
	if binary.LittleEndian.Uint32(pkt[q:q+4]) == 0 {
		return nil, true // already stripped: it IS the comment header, no change
	}

	binary.LittleEndian.PutUint32(pkt[q:q+4], 0) // user_comment_list_length = 0
	zeroEnd := total
	if vorbis {
		zeroEnd = total - 1 // preserve the trailing framing bit
		pkt[total-1] = 0x01
	}
	for k := q + 4; k < zeroEnd; k++ {
		pkt[k] = 0
	}
	*removed = append(*removed, "audio comments")

	// Scatter the modified packet back to its (possibly page-spanning) body spans.
	var mod []oggSpan
	p := 0
	for _, s := range spans {
		copy(buf[s.off:s.off+s.len], pkt[p:p+s.len])
		mod = append(mod, s)
		p += s.len
	}
	return mod, true
}

func oggOverlaps(off, total int, ranges []oggSpan) bool {
	for _, r := range ranges {
		if r.off < off+total && off < r.off+r.len {
			return true
		}
	}
	return false
}

func recomputeOggCRC(buf []byte, off, total int) {
	for k := 0; k < 4; k++ {
		buf[off+22+k] = 0 // zero the checksum field before computing
	}
	crc := oggCRC(buf[off : off+total])
	binary.LittleEndian.PutUint32(buf[off+22:off+26], crc)
}

// oggProfile classifies an Ogg file as video (Theora) or audio (Vorbis/Opus/…).
func oggProfile(d []byte) (kind, format, mime string) {
	if bytes.Contains(d, []byte("theora")) {
		return KindVideo, "ogv", "video/ogg"
	}
	return KindAudio, "oga", "audio/ogg"
}

// Ogg CRC-32: polynomial 0x04C11DB7, no input/output reflection, zero init/xor.
var oggCRCTable [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		oggCRCTable[i] = r
	}
}

func oggCRC(p []byte) uint32 {
	var crc uint32
	for _, b := range p {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}
