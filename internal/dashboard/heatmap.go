// Package dashboard renders the private, loopback-only metrics dashboard
// (SPEC §7.7): a read-only view of the aggregate counters plus a /16 network
// heat map. It is self-hosted and pure stdlib (image/png, html/template).
package dashboard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// HeatmapSize is the heat map's edge length in pixels: x = second octet, y =
// first octet, so the whole public IPv4 space is one 256×256 raster at /16
// granularity. First octet on the vertical axis means the reserved/multicast
// range (224+) reads as a grey bar along the bottom.
const HeatmapSize = 256

const gridCells = HeatmapSize * HeatmapSize

var (
	// colEmpty is a /16 with no traffic in the window (near-black backdrop).
	colEmpty = color.NRGBA{R: 18, G: 18, B: 20, A: 255}
	// colReserved masks a non-routable /16 (private, loopback, CGNAT, multicast,
	// reserved) so the map reads honestly; distinct mid-grey, never "hot".
	colReserved = color.NRGBA{R: 64, G: 64, B: 72, A: 255}
)

// RenderHeatmap renders the summed /16 counts (length gridCells, indexed by
// octet1<<8 | octet2, as returned by metrics.GridSnapshot) into a 256×256 PNG.
// Traffic is log-scaled (a linear ramp would leave one hot pixel and 65k dark
// ones) onto a luminance-increasing ramp — so the map stays legible in grayscale
// and for color-vision deficiencies, not relying on hue alone. Reserved /
// non-routable cells are masked grey regardless of any stray count.
func RenderHeatmap(grid []uint32) ([]byte, error) {
	if len(grid) != gridCells {
		return nil, fmt.Errorf("dashboard: heatmap grid has %d cells, want %d", len(grid), gridCells)
	}

	var max uint32
	for _, v := range grid {
		if v > max {
			max = v
		}
	}
	logMax := math.Log1p(float64(max))

	img := image.NewNRGBA(image.Rect(0, 0, HeatmapSize, HeatmapSize))
	for o0 := 0; o0 < HeatmapSize; o0++ {
		for o1 := 0; o1 < HeatmapSize; o1++ {
			var c color.NRGBA
			switch {
			case reservedCell(byte(o0), byte(o1)):
				c = colReserved
			case grid[o0<<8|o1] == 0 || logMax == 0:
				c = colEmpty
			default:
				t := math.Log1p(float64(grid[o0<<8|o1])) / logMax
				c = heatColor(t)
			}
			// Transpose to pixel space: x = second octet, y = first octet, so the
			// first octet runs top→bottom and the reserved 224+ range sits at the bottom.
			img.SetNRGBA(o1, o0, c)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// heatColor maps a normalized intensity t in (0,1] to a black-body-like ramp
// (dim red → red → yellow → white) whose luminance rises monotonically with t, so
// intensity survives grayscale conversion. The lowest hit is floored to a clearly
// visible dim red rather than fading into the backdrop.
func heatColor(t float64) color.NRGBA {
	if t < 0 {
		t = 0
	}
	tt := 0.15 + 0.85*t // floor so a single hit is still visible
	return color.NRGBA{
		R: lerp8(tt * 3),
		G: lerp8((tt - 1.0/3) * 3),
		B: lerp8((tt - 2.0/3) * 3),
		A: 255,
	}
}

func lerp8(t float64) uint8 {
	switch {
	case t <= 0:
		return 0
	case t >= 1:
		return 255
	default:
		return uint8(t*255 + 0.5)
	}
}

// reservedCell reports whether the /16 (o0.o1.0.0) is entirely non-routable and
// should be masked. Only whole-/16-or-coarser reservations are masked; sub-/16
// documentation blocks (e.g. 203.0.113.0/24) sit inside otherwise-public cells
// and are left alone.
func reservedCell(o0, o1 byte) bool {
	switch {
	case o0 == 0, o0 == 10, o0 == 127: // this-network, private 10/8, loopback
		return true
	case o0 >= 224: // 224/4 multicast + 240/4 reserved/future
		return true
	case o0 == 100 && o1 >= 64 && o1 <= 127: // 100.64/10 CGNAT
		return true
	case o0 == 169 && o1 == 254: // 169.254/16 link-local
		return true
	case o0 == 172 && o1 >= 16 && o1 <= 31: // 172.16/12 private
		return true
	case o0 == 192 && o1 == 168: // 192.168/16 private
		return true
	case o0 == 198 && (o1 == 18 || o1 == 19): // 198.18/15 benchmarking
		return true
	}
	return false
}
