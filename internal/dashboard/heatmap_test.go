package dashboard

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func emptyGrid() []uint32 { return make([]uint32, gridCells) }

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func TestRenderHeatmapWrongSize(t *testing.T) {
	if _, err := RenderHeatmap(make([]uint32, 10)); err == nil {
		t.Fatal("expected error for wrong grid size")
	}
}

func TestRenderHeatmapDimensions(t *testing.T) {
	b, err := RenderHeatmap(emptyGrid())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != HeatmapSize || h != HeatmapSize {
		t.Errorf("bounds = %dx%d, want %dx%d", w, h, HeatmapSize, HeatmapSize)
	}
}

func TestRenderHeatmapHotVsEmpty(t *testing.T) {
	g := emptyGrid()
	g[8<<8|8] = 100 // 8.8.0.0/16 busy
	g[9<<8|9] = 1   // 9.9.0.0/16 one hit
	b, err := RenderHeatmap(g)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, b)

	empty := nrgbaAt(img, 1, 1) // 1.1.0.0/16, no traffic
	if empty != colEmpty {
		t.Errorf("empty cell = %v, want %v", empty, colEmpty)
	}
	hot := nrgbaAt(img, 8, 8) // 8.8 is on the diagonal, so unaffected by the transpose
	dim := nrgbaAt(img, 9, 9) //
	if lum(hot) <= lum(dim) {
		t.Errorf("busy cell luminance %v not greater than single-hit %v", lum(hot), lum(dim))
	}
	if lum(dim) <= lum(empty) {
		t.Errorf("single-hit cell %v not brighter than empty %v", lum(dim), lum(empty))
	}
}

func TestRenderHeatmapReservedMasked(t *testing.T) {
	g := emptyGrid()
	g[10<<8|5] = 500 // 10.5.0.0/16 is private; a stray count must still render grey
	b, err := RenderHeatmap(g)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, b)
	// Pixel space is transposed: cell o0.o1 is at (x=o1, y=o0).
	if got := nrgbaAt(img, 5, 10); got != colReserved {
		t.Errorf("reserved 10.5 cell = %v, want %v", got, colReserved)
	}
	if got := nrgbaAt(img, 168, 192); got != colReserved {
		t.Errorf("192.168 cell = %v, want %v (masked)", got, colReserved)
	}
}

// TestRenderHeatmapOrientation pins the axes: a cell o0.o1 must render at pixel
// (x=o1, y=o0), so the first octet is vertical and the reserved 224+ range is the
// bottom rows (grey bar at the bottom, not the side).
func TestRenderHeatmapOrientation(t *testing.T) {
	g := emptyGrid()
	g[203<<8|50] = 100 // 203.50.0.0/16 — public, asymmetric coordinates
	img := decode(t, mustRender(t, g))

	if got := nrgbaAt(img, 50, 203); got == colEmpty {
		t.Errorf("cell 203.50 should be at pixel (50,203); found it empty there")
	}
	if got := nrgbaAt(img, 203, 50); got != colEmpty {
		t.Errorf("pixel (203,50) should be empty (that would be cell 50.203), got %v", got)
	}
	// The reserved 240.0 cell lands on the bottom row (y=240), not the right edge.
	if got := nrgbaAt(img, 0, 240); got != colReserved {
		t.Errorf("reserved 240.0 should be at the bottom (0,240), got %v", got)
	}
}

func mustRender(t *testing.T, g []uint32) []byte {
	t.Helper()
	b, err := RenderHeatmap(g)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRenderHeatmapDeterministic(t *testing.T) {
	g := emptyGrid()
	g[203<<8|0] = 42
	a, _ := RenderHeatmap(g)
	b, _ := RenderHeatmap(g)
	if !bytes.Equal(a, b) {
		t.Error("render is not deterministic for identical input")
	}
}

func TestReservedCell(t *testing.T) {
	reserved := [][2]byte{{0, 1}, {10, 0}, {127, 5}, {100, 64}, {100, 127}, {169, 254}, {172, 16}, {172, 31}, {192, 168}, {198, 18}, {224, 0}, {240, 0}, {255, 255}}
	for _, c := range reserved {
		if !reservedCell(c[0], c[1]) {
			t.Errorf("reservedCell(%d,%d) = false, want true", c[0], c[1])
		}
	}
	public := [][2]byte{{8, 8}, {1, 2}, {172, 15}, {172, 32}, {100, 63}, {100, 128}, {203, 0}, {198, 20}}
	for _, c := range public {
		if reservedCell(c[0], c[1]) {
			t.Errorf("reservedCell(%d,%d) = true, want false", c[0], c[1])
		}
	}
}

// lum is a quick luminance proxy for ordering comparisons.
func lum(c color.NRGBA) int { return int(c.R) + int(c.G) + int(c.B) }
