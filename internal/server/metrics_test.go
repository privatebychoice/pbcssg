package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

// fakeHandler serves a fixed status + body so the middleware can be tested without
// a real bundle on disk.
type fakeHandler struct {
	status int
	body   string
}

func (h fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.status != 0 && h.status != http.StatusOK {
		w.WriteHeader(h.status)
	}
	if h.body != "" {
		w.Write([]byte(h.body))
	}
}

// instrument wraps an arbitrary handler with the same recording logic as
// Server.Instrument, so the middleware can be exercised without constructing a
// Server. It mirrors Instrument exactly.
func instrument(next http.Handler, reg *metrics.Registry, classify NetClassifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rr, r)
		net, cell := metrics.NetOther, uint16(0)
		if classify != nil {
			net, cell = classify(r)
		}
		reg.Record(metrics.Sample{
			Path: r.URL.Path, Known: rr.status >= 200 && rr.status < 400,
			Status: rr.status, Bytes: rr.bytes, Method: r.Method,
			UAClass: classifyUA(r.Header.Get("User-Agent")), Net: net, Cell: cell,
		})
	})
}

func TestInstrumentRecordsStatusAndBytes(t *testing.T) {
	reg := metrics.New()
	h := instrument(fakeHandler{status: 200, body: "hello world"}, reg, nil)

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Firefox/128.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	s := reg.Snapshot(10)
	if s.Hits != 1 {
		t.Fatalf("hits = %d, want 1", s.Hits)
	}
	if s.Bytes != uint64(len("hello world")) {
		t.Errorf("bytes = %d, want %d", s.Bytes, len("hello world"))
	}
	if s.Status["2xx"] != 1 {
		t.Errorf("status = %v, want 2xx:1", s.Status)
	}
	if s.Method["GET"] != 1 {
		t.Errorf("method = %v, want GET:1", s.Method)
	}
	if s.UAClass["firefox"] != 1 {
		t.Errorf("uaClass = %v, want firefox:1", s.UAClass)
	}
	if len(s.TopPaths) != 1 || s.TopPaths[0].Path != "/about" {
		t.Errorf("topPaths = %v, want [/about]", s.TopPaths)
	}
}

func TestInstrumentDefaultStatusIsOK(t *testing.T) {
	reg := metrics.New()
	// Body written with no explicit WriteHeader must record as 200.
	h := instrument(fakeHandler{body: "x"}, reg, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got := reg.Snapshot(10).Status["2xx"]; got != 1 {
		t.Errorf("status 2xx = %d, want 1 (implicit 200)", got)
	}
}

func TestInstrumentNotFoundBucket(t *testing.T) {
	reg := metrics.New()
	h := instrument(fakeHandler{status: 404, body: "nope"}, reg, nil)
	for _, p := range []string{"/wp-admin", "/xmlrpc.php", "/.env"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	s := reg.Snapshot(10)
	if len(s.TopPaths) != 1 || s.TopPaths[0].Path != "(not found)" || s.TopPaths[0].Count != 3 {
		t.Errorf("topPaths = %v, want single (not found):3", s.TopPaths)
	}
	if s.Status["4xx"] != 3 {
		t.Errorf("status 4xx = %d, want 3", s.Status["4xx"])
	}
}

func TestInstrumentClassifierCell(t *testing.T) {
	reg := metrics.New()
	classify := func(*http.Request) (metrics.NetClass, uint16) {
		return metrics.NetV4, uint16(203)<<8 | 0
	}
	h := instrument(fakeHandler{status: 200, body: "y"}, reg, classify)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := reg.GridSnapshot()[uint16(203)<<8|0]; got != 1 {
		t.Errorf("grid[203.0] = %d, want 1", got)
	}
}

func TestClassifyUA(t *testing.T) {
	cases := map[string]string{
		"":                          "none",
		"Googlebot/2.1":             "bot",
		"curl/8.4.0":                "bot",
		"python-requests/2.31":      "bot",
		"Mozilla/5.0 Firefox/128.0": "firefox",
		"Mozilla/5.0 (Windows) Chrome/120 Safari/537 Edg/120": "edge",
		"Mozilla/5.0 (X11) Chrome/120 Safari/537.36":          "chrome",
		"Mozilla/5.0 (Macintosh) Version/17 Safari/605":       "safari",
		"SomethingElse/1.0": "other",
	}
	for ua, want := range cases {
		if got := classifyUA(ua); got != want {
			t.Errorf("classifyUA(%q) = %q, want %q", ua, got, want)
		}
	}
}
