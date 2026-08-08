package creator

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

type harness struct {
	c        *Creator
	st       *store.Store
	out      string
	releases string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	out := t.TempDir()
	releases := t.TempDir()
	c, err := New(st, Config{OutDir: out, ReleaseDir: releases, Build: build.Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{c: c, st: st, out: out, releases: releases}
}

func (h *harness) get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.c.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func (h *harness) post(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.c.ServeHTTP(rec, req)
	return rec
}

// form builds form values with the CSRF token included.
func (h *harness) form(kv map[string]string) url.Values {
	v := url.Values{"csrf": {h.c.csrf}}
	for k, val := range kv {
		v.Set(k, val)
	}
	return v
}

func TestDashboardAndCreate(t *testing.T) {
	h := newHarness(t)

	if rec := h.get("/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No pages yet") {
		t.Fatalf("empty dashboard: code=%d", rec.Code)
	}

	rec := h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Hello"}))
	if rec.Code != 303 {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}
	pages, _ := h.st.Pages()
	if len(pages) != 1 || pages[0].Title != "Home" {
		t.Fatalf("page not created: %+v", pages)
	}
}

func TestReservedPathRejected(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/tags", "/media", "/media/x.png", "/assets/a", "/external/youtube/z", "/manifest", "/version", "/build.json", "/.well-known/gpc.json"} {
		rec := h.post("/pages", h.form(map[string]string{"title": "X", "path": p, "body": "# X"}))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "reserved by pbcssg") {
			t.Errorf("path %q should be rejected inline, got code=%d", p, rec.Code)
		}
		if pages, _ := h.st.Pages(); len(pages) != 0 {
			t.Fatalf("reserved path %q must not create a page", p)
		}
	}

	// A normal path still works, and a duplicate is reported inline.
	if rec := h.post("/pages", h.form(map[string]string{"title": "OK", "path": "/blog/ok", "body": "# ok"})); rec.Code != 303 {
		t.Fatalf("valid path should create: %d", rec.Code)
	}
	dup := h.post("/pages", h.form(map[string]string{"title": "Dup", "path": "/blog/ok", "body": "# dup"}))
	if dup.Code != 200 || !strings.Contains(dup.Body.String(), "already used") {
		t.Errorf("duplicate path should be reported inline, got %d", dup.Code)
	}
}

func TestCSRFRequired(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/pages", url.Values{"title": {"X"}, "path": {"/x"}}) // no csrf
	if rec.Code != 403 {
		t.Errorf("missing CSRF should be 403, got %d", rec.Code)
	}
}

func TestEditSaveAndPreview(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Original"}))
	id := "1"

	if rec := h.get("/pages/" + id); rec.Code != 200 || !strings.Contains(rec.Body.String(), "# Original") {
		t.Fatalf("edit form missing content: code=%d", rec.Code)
	}

	if rec := h.post("/pages/"+id, h.form(map[string]string{"title": "Home", "path": "/", "body": "# Updated"})); rec.Code != 303 {
		t.Fatalf("save status = %d", rec.Code)
	}
	rev, _, _ := h.st.LatestRevision(1)
	if !strings.Contains(rev.ContentJSON, "Updated") {
		t.Errorf("save did not persist: %s", rev.ContentJSON)
	}

	// Live preview renders the posted content through the real pipeline.
	prec := h.post("/preview", h.form(map[string]string{"body": "# Live **preview**"}))
	if prec.Code != 200 || !strings.Contains(prec.Body.String(), ">Live <strong>preview</strong></h1>") {
		t.Errorf("preview wrong: code=%d\n%s", prec.Code, prec.Body.String())
	}
}

func TestPublishGate(t *testing.T) {
	h := newHarness(t)

	// A clean page (no external links) publishes directly.
	h.post("/pages", h.form(map[string]string{"title": "Clean", "path": "/clean", "body": "# Clean\n\nNo links."}))
	if rec := h.post("/pages/1/publish", h.form(nil)); rec.Code != 303 {
		t.Fatalf("clean publish = %d, want 303", rec.Code)
	}
	if p, _ := h.st.Page(1); p.Status != store.StatusPublished {
		t.Errorf("clean page not published: %s", p.Status)
	}

	// A page with an external (unclassified) link is gated: publish shows the
	// acknowledgement screen and does NOT publish yet.
	h.post("/pages", h.form(map[string]string{"title": "Ext", "path": "/ext", "body": "[t](https://tracker.example/x)"}))
	rec := h.post("/pages/2/publish", h.form(nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "acknowledgement") {
		t.Fatalf("gated publish should show confirm: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if p, _ := h.st.Page(2); p.Status == store.StatusPublished {
		t.Errorf("gated page should not be published before acknowledgement")
	}
	// Acknowledge → publishes.
	if rec := h.post("/pages/2/publish", h.form(map[string]string{"ack": "1"})); rec.Code != 303 {
		t.Fatalf("acknowledged publish = %d", rec.Code)
	}
	if p, _ := h.st.Page(2); p.Status != store.StatusPublished {
		t.Errorf("acknowledged page should be published")
	}
}

func TestUnpublishFromEditor(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home"}))
	h.post("/pages/1/publish", h.form(nil))
	if p, _ := h.st.Page(1); p.Status != store.StatusPublished {
		t.Fatalf("precondition: page not published")
	}

	// The edit page offers Unpublish while published.
	if ed := h.get("/pages/1"); !strings.Contains(ed.Body.String(), "/pages/1/unpublish") {
		t.Errorf("edit page should show an Unpublish action while published")
	}

	if rec := h.post("/pages/1/unpublish", h.form(nil)); rec.Code != 303 {
		t.Fatalf("unpublish = %d, want 303", rec.Code)
	}
	if p, _ := h.st.Page(1); p.Status != store.StatusDraft {
		t.Errorf("page should be a draft after unpublish, got %s", p.Status)
	}

	// CSRF is required.
	h.post("/pages/1/publish", h.form(nil))
	if rec := h.post("/pages/1/unpublish", url.Values{}); rec.Code != 403 {
		t.Errorf("unpublish without CSRF should be 403, got %d", rec.Code)
	}
}

func TestBuildFromEditor(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Home"}))
	h.post("/pages/1/publish", h.form(nil))

	rec := h.post("/build", h.form(nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Site generated") {
		t.Fatalf("build = %d\n%s", rec.Code, rec.Body.String())
	}
	if _, err := os.ReadFile(filepath.Join(h.out, "index.html")); err != nil {
		t.Errorf("build did not write index.html: %v", err)
	}
	body := rec.Body.String()
	// The report links a content page to its editor...
	if !strings.Contains(body, `<a href="/pages/1" title="Edit this page"><code>/</code></a>`) {
		t.Errorf("Home page should link to its editor:\n%s", body)
	}
	// ...but an engine-generated page (no editor ID) stays plain text.
	if !strings.Contains(body, `<td><code>/classification</code></td>`) {
		t.Errorf("engine-generated /classification should not be a link:\n%s", body)
	}
}

func TestAssetsServed(t *testing.T) {
	h := newHarness(t)
	for _, a := range []string{"admin.css", "admin.js", "theme.css", "theme-toggle.js"} {
		if rec := h.get("/admin/assets/" + a); rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("asset %s: code=%d len=%d", a, rec.Code, rec.Body.Len())
		}
	}
	if rec := h.get("/admin/assets/nope.css"); rec.Code != 404 {
		t.Errorf("unknown asset should 404, got %d", rec.Code)
	}
	// The editor chrome carries its own Auto/Light/Dark control: a blocking head
	// script and a nav toggle, keyed independently of the preview.
	top := h.get("/").Body.String()
	if !strings.Contains(top, `<script src="/admin/assets/theme-toggle.js"></script>`) {
		t.Errorf("admin layout should load the theme-toggle script in <head>:\n%s", top)
	}
	if !strings.Contains(top, `class="theme-toggle" data-pbcssg-theme-toggle hidden`) {
		t.Errorf("admin nav should carry the hidden theme toggle button:\n%s", top)
	}
	if tj := h.get("/admin/assets/theme-toggle.js").Body.String(); !strings.Contains(tj, "pbcssg-admin-theme") {
		t.Errorf("theme-toggle.js should use the admin-specific storage key, keeping it independent of the preview")
	}
	// The editor re-renders the preview when shown again (bfcache back-nav or a
	// tab switch), so it reflects out-of-band media changes instead of a stale
	// image. Guard the two triggers against accidental removal.
	js := h.get("/admin/assets/admin.js").Body.String()
	for _, want := range []string{"pageshow", "visibilitychange"} {
		if !strings.Contains(js, want) {
			t.Errorf("admin.js should refresh the preview on %q so it does not show stale media", want)
		}
	}
}

func TestDashboardSortAndPagination(t *testing.T) {
	h := newHarness(t)
	// One more than a full page → exactly two dashboard pages (the last partial),
	// derived from the page size so the test tracks pagesPageSize.
	const created = pagesPageSize + 1
	totalPages := (created + pagesPageSize - 1) / pagesPageSize
	for i := 0; i < created; i++ {
		h.post("/pages", h.form(map[string]string{
			"title": fmt.Sprintf("Page %02d", i),
			"path":  fmt.Sprintf("/p%d", i),
			"body":  "# x",
		}))
	}

	// Default view: sortable headers + pagination present.
	body := h.get("/").Body.String()
	for _, want := range []string{
		`href="/?sort=title`, `href="/?sort=status`, "sort=updated",
		fmt.Sprintf("Page 1 of %d", totalPages), "Next →",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	// The last page is reachable and shows the final pager status.
	lastStatus := fmt.Sprintf("Page %d of %d", totalPages, totalPages)
	if last := h.get(fmt.Sprintf("/?page=%d", totalPages)).Body.String(); !strings.Contains(last, lastStatus) {
		t.Errorf("last page missing pager status %q", lastStatus)
	}
	// Sorting by title ascending puts an active arrow on the Title header and the
	// header link now offers to flip to descending.
	ts := h.get("/?sort=title&dir=asc").Body.String()
	if !strings.Contains(ts, "Title ▲") || !strings.Contains(ts, `href="/?dir=desc&amp;sort=title"`) {
		t.Errorf("title-asc sort state not reflected in headers")
	}
}

func TestCopyMarkdownLinkButtons(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "My Post", "path": "/blog/my-post", "body": "# x"}))

	// Dashboard row offers a "Copy MD" button with a data-copy markdown link.
	dash := h.get("/").Body.String()
	if !strings.Contains(dash, `data-copy="[My Post](/blog/my-post)"`) {
		t.Errorf("dashboard missing copy-markdown button")
	}
	if !strings.Contains(dash, `<script src="/admin/assets/copy.js"`) {
		t.Errorf("dashboard should load copy.js")
	}
	// The edit page offers the same for the saved page.
	ed := h.get("/pages/1").Body.String()
	if !strings.Contains(ed, `data-copy="[My Post](/blog/my-post)"`) {
		t.Errorf("edit page missing copy-markdown button")
	}
	// The shared copy script is served.
	if js := h.get("/admin/assets/copy.js"); js.Code != 200 || !strings.Contains(js.Body.String(), "data-copy") {
		t.Errorf("copy.js not served")
	}
}

// TestDashboardStatusToggle covers the listing's clickable status pill: it posts
// to the publish/unpublish endpoints, flips the page's state, and returns to the
// listing view (rejecting a non-local return). It also confirms the "Link" label.
func TestDashboardStatusToggle(t *testing.T) {
	h := newHarness(t)
	h.post("/pages", h.form(map[string]string{"title": "Home", "path": "/", "body": "# Hi"}))
	pages, _ := h.st.Pages()
	id := pages[0].ID

	// A draft page renders a publish toggle + the return field + the "Link" button.
	body := h.get("/").Body.String()
	if !strings.Contains(body, fmt.Sprintf(`action="/pages/%d/publish"`, id)) || !strings.Contains(body, `class="status draft"`) {
		t.Fatalf("draft status toggle not rendered:\n%s", body)
	}
	if !strings.Contains(body, `name="return"`) || strings.Contains(body, "Copy MD") || !strings.Contains(body, ">Link</button>") {
		t.Errorf("expected a return field and a Link button (not Copy MD)")
	}

	// Clicking it publishes and returns to the supplied listing view.
	rec := h.post(fmt.Sprintf("/pages/%d/publish", id), h.form(map[string]string{"return": "/?page=1&sort=path"}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?page=1&sort=path" {
		t.Fatalf("publish toggle: code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	if p, _ := h.st.Page(id); p.Status != store.StatusPublished {
		t.Errorf("page not published: %q", p.Status)
	}
	// Now the listing offers an unpublish toggle.
	if b := h.get("/").Body.String(); !strings.Contains(b, fmt.Sprintf(`action="/pages/%d/unpublish"`, id)) {
		t.Errorf("published page should show an unpublish toggle")
	}
	// Toggling back to draft also honors the return.
	rec2 := h.post(fmt.Sprintf("/pages/%d/unpublish", id), h.form(map[string]string{"return": "/"}))
	if rec2.Code != http.StatusSeeOther || rec2.Header().Get("Location") != "/" {
		t.Fatalf("unpublish toggle: code=%d loc=%q", rec2.Code, rec2.Header().Get("Location"))
	}
	if p, _ := h.st.Page(id); p.Status != store.StatusDraft {
		t.Errorf("page not unpublished: %q", p.Status)
	}
	// Open-redirect guard: a protocol-relative return falls back to the page.
	rec3 := h.post(fmt.Sprintf("/pages/%d/unpublish", id), h.form(map[string]string{"return": "//evil.example"}))
	if loc := rec3.Header().Get("Location"); loc == "//evil.example" {
		t.Errorf("open redirect not blocked: %q", loc)
	}
}
