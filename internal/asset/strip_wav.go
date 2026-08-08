package asset

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// WAV is a RIFF container: "RIFF" <size> "WAVE" then a sequence of chunks
// (<id><size LE><data>, data padded to even length). Metadata rides in optional
// chunks — LIST/INFO (INAM/IART/ICMT…), an embedded id3 tag, bext (broadcast
// extension, often with author/coding history), iXML and _PMX (XMP). We drop
// those and keep the structural/audio chunks. WAV has no byte-offset cross-
// references, so rebuilding is safe.

func isWAV(d []byte) bool {
	return len(d) >= 12 && bytes.Equal(d[0:4], []byte("RIFF")) && bytes.Equal(d[8:12], []byte("WAVE"))
}

// wavDropChunks are non-audio metadata chunks removed on ingest.
var wavDropChunks = map[string]bool{
	"id3 ": true, "ID3 ": true, "bext": true, "iXML": true,
	"_PMX": true, "cart": true, "axml": true, "SMED": true,
}

func stripWAV(data []byte) ([]byte, []string, error) {
	if len(data) < 12 {
		return nil, nil, fmt.Errorf("short WAV")
	}
	var kept bytes.Buffer
	var removed []string
	for i := 12; i+8 <= len(data); {
		id := string(data[i : i+4])
		sz := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if sz < 0 || i+8+sz > len(data) {
			return nil, nil, fmt.Errorf("WAV chunk %q size out of range", id)
		}
		total := 8 + sz
		if sz%2 == 1 && i+total < len(data) {
			total++ // trailing pad byte (kept with the chunk when copied)
		}
		drop := wavDropChunks[id]
		if id == "LIST" && sz >= 4 && string(data[i+8:i+12]) == "INFO" {
			drop = true // LIST/INFO metadata; keep LIST/adtl (cue labels are content)
		}
		if drop {
			removed = append(removed, id)
		} else {
			kept.Write(data[i : i+total])
		}
		i += total
	}

	// Nothing to strip → return the input unchanged (idempotent, and it never
	// rewrites a quirky-but-valid RIFF size).
	if len(removed) == 0 {
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil, nil
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	out.Write([]byte{0, 0, 0, 0}) // size placeholder
	out.WriteString("WAVE")
	out.Write(kept.Bytes())
	b := out.Bytes()
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	return b, dedupStrings(removed), nil
}
