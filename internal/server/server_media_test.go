package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/asset"
	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func pngData(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 9, G: 8, B: 7, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const svgData = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2 2"><rect width="2" height="2"/></svg>`

// mediaBundle builds a bundle whose home page references a PNG and an SVG stored
// in the media library, and returns the bundle dir plus the two media paths.
func mediaBundle(t *testing.T) (dir, pngPath, svgPath string) {
	t.Helper()
	dir = t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	put := func(data []byte, name string) *asset.Asset {
		a, err := asset.Ingest(name, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PutAsset(store.AssetData{
			Asset: store.Asset{SHA256: a.SHA256, Filename: name, Format: a.Format, MIME: a.MIME},
			Data:  a.Data,
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	pa := put(pngData(t), "p.png")
	sa := put([]byte(svgData), "s.svg")
	pngPath = "/media/" + pa.SHA256 + ".png"
	svgPath = "/media/" + sa.SHA256 + ".svg"

	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	c, _ := json.Marshal(map[string]string{"body": "# Home\n\n![p](" + pngPath + ") ![s](" + svgPath + ")"})
	rid, _ := s.SaveRevision(pid, string(c), "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(s, build.Config{BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, dir); err != nil {
		t.Fatal(err)
	}
	return dir, pngPath, svgPath
}

func TestServeMediaHeaders(t *testing.T) {
	dir, pngPath, svgPath := mediaBundle(t)
	srv, err := New(Config{ContentDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// PNG: correct type, immutable cache, nosniff, no sandbox CSP.
	rec := do(srv, "GET", pngPath, nil)
	if rec.Code != 200 {
		t.Fatalf("png: code=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("png content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("media should be immutable, got %q", cc)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("media must be nosniff")
	}

	// SVG: sandboxed via CSP as defense in depth.
	srec := do(srv, "GET", svgPath, nil)
	if srec.Code != 200 {
		t.Fatalf("svg: code=%d", srec.Code)
	}
	if ct := srec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("svg content-type = %q", ct)
	}
	if csp := srec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("served svg must be sandboxed, got %q", csp)
	}
}
