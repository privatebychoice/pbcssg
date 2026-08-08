package creator

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/metrics"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func seededMetrics() *metrics.Registry {
	r := metrics.New()
	r.Record(metrics.Sample{Path: "/", Known: true, Status: 200, Bytes: 1200, Method: "GET", UAClass: "firefox", Net: metrics.NetV4, Cell: uint16(203)<<8 | 0})
	return r
}

func newMetricsHarness(t *testing.T, reg *metrics.Registry) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := New(st, Config{
		OutDir: t.TempDir(), ReleaseDir: t.TempDir(),
		Build:   build.Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"},
		Metrics: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{c: c, st: st}
}

// TestMetricsAdminPage: with a registry wired in, the Metrics page renders inside the
// admin chrome (header + nav link) and shows the aggregate content.
func TestMetricsAdminPage(t *testing.T) {
	h := newMetricsHarness(t, seededMetrics())
	rec := h.get("/admin/metrics")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`class="admin-header`, "<h1>Metrics</h1>", "/admin/metrics/heatmap.png", "203.0.0.0/16"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics page missing %q", want)
		}
	}
	if !strings.Contains(body, `href="/admin/metrics"`) {
		t.Errorf("no Metrics nav link in the admin chrome")
	}
	if strings.Contains(body, "Mozilla") {
		t.Errorf("metrics page leaked a raw user-agent")
	}
}

func TestMetricsHeatmapAndJSON(t *testing.T) {
	h := newMetricsHarness(t, seededMetrics())
	if png := h.get("/admin/metrics/heatmap.png"); png.Code != 200 || png.Header().Get("Content-Type") != "image/png" {
		t.Errorf("heatmap.png = %d ct=%q", png.Code, png.Header().Get("Content-Type"))
	}
	js := h.get("/admin/metrics/metrics.json")
	if js.Code != 200 || !strings.HasPrefix(js.Header().Get("Content-Type"), "application/json") {
		t.Errorf("metrics.json = %d ct=%q", js.Code, js.Header().Get("Content-Type"))
	}
}

// TestMetricsHiddenWithoutRegistry: standalone editor (no registry) 404s the metrics
// routes and shows no nav link.
func TestMetricsHiddenWithoutRegistry(t *testing.T) {
	h := newHarness(t)
	if rec := h.get("/admin/metrics"); rec.Code != http.StatusNotFound {
		t.Errorf("metrics page without registry = %d, want 404", rec.Code)
	}
	if body := h.get("/").Body.String(); strings.Contains(body, `href="/admin/metrics"`) {
		t.Errorf("Metrics nav link shown without a registry")
	}
}

func TestTrustedProxiesSetting(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example",
		"trustedProxies": "127.0.0.0/8, 10.0.0.0/8",
	}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Settings saved") {
		t.Fatalf("save = %d:\n%s", rec.Code, rec.Body.String())
	}
	got := LoadTrustedProxies(h.st)
	if len(got) != 2 || got[0] != "127.0.0.0/8" || got[1] != "10.0.0.0/8" {
		t.Errorf("LoadTrustedProxies = %v, want [127.0.0.0/8 10.0.0.0/8]", got)
	}

	bad := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example",
		"trustedProxies": "not-a-cidr",
	}))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("invalid trusted proxy = %d, want 400", bad.Code)
	}
}
