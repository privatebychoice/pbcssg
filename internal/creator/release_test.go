package creator

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageRelease(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home"}))
	h.post("/pages/1/publish", h.form(nil))

	rec := h.post("/admin/release", h.form(nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Release packaged") {
		t.Fatalf("release: code=%d\n%s", rec.Code, rec.Body.String())
	}

	// Build number auto-incremented from the seed "1" -> "2" and persisted.
	if bn := h.c.state().build.BuildNumber; bn != "2" {
		t.Errorf("build number = %q, want 2", bn)
	}
	if v, _, _ := h.st.Setting(keyBuildNumber); v != "2" {
		t.Errorf("persisted build number = %q, want 2", v)
	}

	// The versioned tarball exists and contains the built bundle.
	tarPath := filepath.Join(h.releases, "pbcssg-v1.0-build2.tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		t.Fatalf("tarball not written: %v", err)
	}
	names := tarEntries(t, tarPath)
	for _, want := range []string{"index.html", "build.json"} {
		if !contains(names, want) {
			t.Errorf("tarball missing %s; has %v", want, names)
		}
	}
}

func tarEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
