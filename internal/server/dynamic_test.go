package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// markerHandler records that it was reached and echoes the method — a stand-in for
// the real dynamic layer.
type markerHandler struct {
	hit    bool
	method string
}

func (m *markerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.hit = true
	m.method = r.Method
	w.WriteHeader(http.StatusNoContent)
}

func TestReservedPrefixRoutesToDynamic(t *testing.T) {
	m := &markerHandler{}
	srv, err := New(Config{ContentDir: buildBundle(t), Dynamic: m})
	if err != nil {
		t.Fatal(err)
	}
	// A POST under the reserved prefix reaches the dynamic handler (static is GET/HEAD
	// only, but the dynamic layer owns its own methods).
	req := httptest.NewRequest(http.MethodPost, "/_pbc/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if !m.hit || m.method != http.MethodPost {
		t.Fatalf("dynamic not reached: hit=%v method=%q", m.hit, m.method)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 from the dynamic handler", rec.Code)
	}
}

func TestReservedPrefixExactPathRoutes(t *testing.T) {
	m := &markerHandler{}
	srv, _ := New(Config{ContentDir: buildBundle(t), Dynamic: m})
	// The bare "/_pbc" (no trailing slash) is also reserved.
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_pbc", nil))
	if !m.hit {
		t.Error("bare /_pbc did not route to the dynamic handler")
	}
}

func TestReservedPrefix404WithoutDynamic(t *testing.T) {
	srv, err := New(Config{ContentDir: buildBundle(t)}) // no Dynamic
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_pbc/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("reserved path without a dynamic layer: status %d, want 404", rec.Code)
	}
}

func TestStaticStillGetHeadOnly(t *testing.T) {
	m := &markerHandler{}
	srv, _ := New(Config{ContentDir: buildBundle(t), Dynamic: m})
	// A POST to a static path is still 405 — the dynamic layer must not have widened
	// methods for the static site.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: status %d, want 405", rec.Code)
	}
	if m.hit {
		t.Error("a static request reached the dynamic handler")
	}
}

func TestStaticServesWithDynamicConfigured(t *testing.T) {
	m := &markerHandler{}
	srv, _ := New(Config{ContentDir: buildBundle(t), Dynamic: m})
	// A normal page still serves from the bundle; the dynamic handler is untouched.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", rec.Code)
	}
	if m.hit {
		t.Error("a static GET reached the dynamic handler")
	}
}
