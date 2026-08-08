package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

func newTestAPI(t *testing.T) (http.Handler, *appstore.Store) {
	t.Helper()
	app, err := appstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	h, err := New(app, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return h, app
}

// seedComment adds a comment on path and sets its status; returns nothing.
func seedComment(t *testing.T, app *appstore.Store, path, alias, body, status string) {
	t.Helper()
	acc, err := app.CreateAccount(appstore.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := app.AddComment(acc.ID, path, alias, body)
	if err != nil {
		t.Fatal(err)
	}
	if status != appstore.CommentPending {
		if err := app.SetCommentStatus(c.ID, status); err != nil {
			t.Fatal(err)
		}
	}
}

func get(t *testing.T, h http.Handler, path string, gpc bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if gpc {
		req.Header.Set("Sec-GPC", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	h, _ := newTestAPI(t)
	rec := get(t, h, "/_pbc/health", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["gpc"] != false {
		t.Errorf("health body = %v", body)
	}
	// The GPC signal is detected and reflected.
	rec = get(t, h, "/_pbc/health", true)
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["gpc"] != true {
		t.Errorf("GPC not reflected: %v", body)
	}
}

func TestCommentsApprovedOnly(t *testing.T) {
	h, app := newTestAPI(t)
	seedComment(t, app, "/post", "raven", "approved one", appstore.CommentApproved)
	seedComment(t, app, "/post", "mallory", "pending one", appstore.CommentPending)
	seedComment(t, app, "/other", "raven", "elsewhere", appstore.CommentApproved)

	rec := get(t, h, "/_pbc/comments?path=/post", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("comments status %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("cache-control = %q, want no-cache", cc)
	}
	var body struct {
		Path     string        `json:"path"`
		Comments []commentView `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Path != "/post" || len(body.Comments) != 1 {
		t.Fatalf("comments = %+v, want 1 approved on /post", body)
	}
	if body.Comments[0].Alias != "raven" || body.Comments[0].Body != "approved one" {
		t.Errorf("comment = %+v", body.Comments[0])
	}
	if body.Comments[0].CreatedAt == 0 {
		t.Error("createdAt not set")
	}
}

func TestCommentsEmptyForUnknownPage(t *testing.T) {
	h, _ := newTestAPI(t)
	rec := get(t, h, "/_pbc/comments?path=/nothing-here", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Comments []commentView `json:"comments"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Comments) != 0 {
		t.Errorf("expected no comments, got %d", len(body.Comments))
	}
}

func TestCommentsRejectsBadPath(t *testing.T) {
	h, _ := newTestAPI(t)
	for _, p := range []string{"/_pbc/comments", "/_pbc/comments?path=", "/_pbc/comments?path=relative"} {
		if rec := get(t, h, p, false); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", p, rec.Code)
		}
	}
}

func TestUnknownEndpoint404(t *testing.T) {
	h, _ := newTestAPI(t)
	if rec := get(t, h, "/_pbc/nope", false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown endpoint: status %d, want 404", rec.Code)
	}
}

func TestCommentsWidgetAssetsServe(t *testing.T) {
	h, _ := newTestAPI(t) // assets serve even without member auth
	js := get(t, h, "/_pbc/assets/comments.js", false)
	if js.Code != http.StatusOK || js.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("comments.js: status %d ct %q", js.Code, js.Header().Get("Content-Type"))
	}
	for _, want := range []string{"data-pbc-comments", "textContent", "navigator.credentials.get", "'/_pbc'", "/comments?path="} {
		if !bytes.Contains(js.Body.Bytes(), []byte(want)) {
			t.Errorf("comments.js missing %q", want)
		}
	}
	css := get(t, h, "/_pbc/assets/comments.css", false)
	if css.Code != http.StatusOK || css.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("comments.css: status %d ct %q", css.Code, css.Header().Get("Content-Type"))
	}
}
