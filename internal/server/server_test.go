package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func buildBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pub := func(path, slug, title, body string) {
		pid, _ := s.CreatePage(store.Page{Path: path, Slug: slug, Title: title})
		c, _ := json.Marshal(map[string]string{"body": body})
		rid, _ := s.SaveRevision(pid, string(c), "editor")
		if err := s.Publish(pid, rid); err != nil {
			t.Fatal(err)
		}
	}
	pub("/", "home", "Home", "# Home\n\n[an external site](https://external.example)")
	pub("/privacy", "privacy", "Privacy", "# Privacy\n\nWe honour GPC.")

	if _, err := build.Run(s, build.Config{
		SiteName: "TUL", BaseURL: "https://tul.example",
		Version: "1.0", BuildNumber: "9", GPCLastUpdate: "2026-07-27",
	}, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// buildBundleN builds a minimal single-page bundle with a chosen build number and
// home-page heading, so a test can tell two bundles apart across a Reload.
func buildBundleN(t *testing.T, buildNo, homeHeading string) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	c, _ := json.Marshal(map[string]string{"body": "# " + homeHeading})
	rid, _ := s.SaveRevision(pid, string(c), "editor")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(s, build.Config{
		SiteName: "TUL", BaseURL: "https://tul.example",
		Version: "1.0", BuildNumber: buildNo,
	}, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newServer(t *testing.T) *Server {
	t.Helper()
	srv, err := New(Config{ContentDir: buildBundle(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func do(srv *Server, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestServeSecurityTxt(t *testing.T) {
	// A bundle with a security contact emits /.well-known/security.txt; the server must
	// serve it as text/plain (§7.6).
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Home"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(s, build.Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		SecurityContacts: []string{"mailto:sec@tul.example"}, SecurityExpires: "2027-01-01T00:00:00Z"}, dir); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(srv, "GET", "/.well-known/security.txt", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("security.txt: code=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "Contact: mailto:sec@tul.example") {
		t.Errorf("security.txt body wrong:\n%s", rec.Body.String())
	}
}

func TestServeHomeHeaders(t *testing.T) {
	srv := newServer(t)
	rec := do(srv, "GET", "/", nil)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), ">Home</h1>") { // h1 carries a goldmark auto ID (SPEC §6.12)
		t.Errorf("home not served:\n%s", rec.Body.String())
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("content-type = %q", h.Get("Content-Type"))
	}
	if h.Get("Content-Security-Policy") == "" {
		t.Errorf("missing CSP header")
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing nosniff")
	}
	if h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("missing referrer-policy")
	}
	if h.Get("Cache-Control") != "no-cache" {
		t.Errorf("html cache-control = %q, want no-cache", h.Get("Cache-Control"))
	}
	if h.Get("ETag") == "" {
		t.Errorf("missing ETag")
	}
}

func TestPrettyURLsAndStaticFiles(t *testing.T) {
	srv := newServer(t)
	cases := []struct {
		target      string
		wantStatus  int
		wantCTPre   string
		wantBodySub string
	}{
		{"/privacy", 200, "text/html", ">Privacy</h1>"},
		{"/manifest/site.json", 200, "application/json", `"pages"`},
		{"/.well-known/gpc.json", 200, "application/json", `"gpc": true`},
		{"/build.json", 404, "", ""}, // internal metadata: never served (§7.1)
		{"/missing", 404, "", ""},
		{"/manifest", 404, "", ""}, // directory, never listed
	}
	for _, c := range cases {
		rec := do(srv, "GET", c.target, nil)
		if rec.Code != c.wantStatus {
			t.Errorf("%s: status = %d, want %d", c.target, rec.Code, c.wantStatus)
		}
		if c.wantCTPre != "" && !strings.HasPrefix(rec.Header().Get("Content-Type"), c.wantCTPre) {
			t.Errorf("%s: content-type = %q, want prefix %q", c.target, rec.Header().Get("Content-Type"), c.wantCTPre)
		}
		if c.wantBodySub != "" && !strings.Contains(rec.Body.String(), c.wantBodySub) {
			t.Errorf("%s: body missing %q:\n%s", c.target, c.wantBodySub, rec.Body.String())
		}
	}
}

// On its own miss, pbcssg server serves the bundle's themed /404.html with a 404
// status (§7.8), the same page a reverse proxy would map via error_page.
func TestServerServesThemed404(t *testing.T) {
	srv := newServer(t)
	rec := do(srv, "GET", "/no/such/page", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "Page not found") {
		t.Errorf("themed 404 body missing:\n%s", rec.Body.String())
	}
	// HEAD gets the 404 status but no body.
	head := do(srv, "HEAD", "/no/such/page", nil)
	if head.Code != http.StatusNotFound || head.Body.Len() != 0 {
		t.Errorf("HEAD 404 = %d with %d-byte body, want 404 + empty", head.Code, head.Body.Len())
	}
}

// A bundle with no 404.html (e.g. built before §7.8) falls back to a plain 404.
func TestServer404FallsBackToPlain(t *testing.T) {
	dir := buildBundle(t)
	if err := os.Remove(filepath.Join(dir, "404.html")); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(srv, "GET", "/no/such/page", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "404 page not found") {
		t.Errorf("expected the plain-text fallback, got:\n%s", rec.Body.String())
	}
}

// build.json is read at startup (New succeeds) but must never be served — its file
// map would enumerate every path, including unlisted pages (§6.16, §7.1).
func TestBuildJSONNotServed(t *testing.T) {
	srv := newServer(t) // New read build.json fine, proving it's still on disk
	rec := do(srv, "GET", "/build.json", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /build.json = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "buildNumber") {
		t.Errorf("build.json contents were served:\n%s", rec.Body.String())
	}
}

func TestVersionEndpoint(t *testing.T) {
	srv := newServer(t)
	rec := do(srv, "GET", "/version", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var v map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v["version"] != "1.0" || v["buildNumber"] != "9" {
		t.Errorf("version payload = %v", v)
	}
}

func TestConditionalGet304(t *testing.T) {
	srv := newServer(t)
	etag := do(srv, "GET", "/", nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	rec := do(srv, "GET", "/", map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET status = %d, want 304", rec.Code)
	}
}

// TestServeThroughCurrentSymlink mirrors the README deploy: -content points at a
// `current` symlink → releases/<version>. os.Root resolves the root symlink when
// the root is opened, so serving works normally.
func TestServeThroughCurrentSymlink(t *testing.T) {
	release := buildBundle(t)
	current := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(release, current); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: current})
	if err != nil {
		t.Fatalf("New via current symlink: %v", err)
	}
	rec := do(srv, "GET", "/", nil)
	if rec.Code != 200 {
		t.Fatalf("GET / via symlink root = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Home") {
		t.Errorf("home not served through the current symlink:\n%s", rec.Body.String())
	}
}

// TestServeRefusesEscapingSymlink is the reason for the os.Root hardening: a
// symlink inside the bundle that points outside it must never be served (the old
// prefix check would have followed it).
func TestServeRefusesEscapingSymlink(t *testing.T) {
	release := buildBundle(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET-XYZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(release, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: release})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(srv, "GET", "/leak.txt", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("escaping symlink served: status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "TOPSECRET-XYZ") {
		t.Errorf("escaping symlink leaked its target content:\n%s", rec.Body.String())
	}
}

func TestPathTraversalRejected(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/../../etc/passwd", "/../build.json", "/%2e%2e/build.json"} {
		rec := do(srv, "GET", p, nil)
		if rec.Code == 200 && strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("%s served system file", p)
		}
	}
}

func TestMethodAndGPC(t *testing.T) {
	srv := newServer(t)

	if rec := do(srv, "POST", "/", nil); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
		t.Errorf("POST should be 405 with Allow header, got %d", rec.Code)
	}
	if rec := do(srv, "HEAD", "/", nil); rec.Code != 200 {
		t.Errorf("HEAD should be 200, got %d", rec.Code)
	}
	// A Sec-GPC signal changes nothing (no sale to suppress) but must not break serving.
	if rec := do(srv, "GET", "/", map[string]string{"Sec-GPC": "1"}); rec.Code != 200 {
		t.Errorf("Sec-GPC request should serve normally, got %d", rec.Code)
	}
}

func TestNewRejectsNonBundle(t *testing.T) {
	if _, err := New(Config{ContentDir: t.TempDir()}); err == nil {
		t.Errorf("New should fail without build.json")
	}
}

func TestFingerprintedAssetImmutableCache(t *testing.T) {
	srv := newServer(t)
	home := do(srv, "GET", "/", nil).Body.String()
	i := strings.Index(home, `href="/assets/theme.`)
	if i < 0 {
		t.Fatal("no fingerprinted theme link on home page")
	}
	href := home[i+len(`href="`):]
	href = href[:strings.IndexByte(href, '"')]

	rec := do(srv, "GET", href, nil)
	if rec.Code != 200 {
		t.Fatalf("asset %s status = %d", href, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("fingerprinted asset cache-control = %q, want immutable", cc)
	}
}

func TestCSPIncludesEmbedHosts(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Home"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(s, build.Config{
		SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1",
		EmbedHosts: []string{"peertube.example"},
	}, dir); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	csp := do(srv, "GET", "/", nil).Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src 'self' https://www.youtube-nocookie.com https://peertube.example") {
		t.Errorf("CSP frame-src missing allowlisted embed origin:\n%s", csp)
	}
}

func TestBuildCSP(t *testing.T) {
	if buildCSP(nil) != DefaultCSP {
		t.Errorf("buildCSP(nil) should equal DefaultCSP")
	}
	got := buildCSP([]string{"https://a.example", "https://b.example"})
	if !strings.HasSuffix(got, "frame-src 'self' https://www.youtube-nocookie.com https://a.example https://b.example") {
		t.Errorf("buildCSP frame-src wrong:\n%s", got)
	}
}

func TestSearchIndexIsNoCache(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/enya", Slug: "enya", Title: "Enya Song"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Enya"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(s, build.Config{
		BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", Search: true,
	}, dir); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{ContentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(srv, "GET", "/search/index.json", nil)
	if rec.Code != 200 {
		t.Fatalf("index status = %d", rec.Code)
	}
	// The index changes every rebuild, so it must revalidate every request — a
	// stale cached index is why newly built pages don't appear in search.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("search index Cache-Control = %q, want no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("search index should carry an ETag for cheap revalidation")
	}
	if !strings.Contains(rec.Body.String(), "Enya Song") {
		t.Errorf("index should contain the page")
	}
}

func TestBuildCSPSkipsMalformedOrigins(t *testing.T) {
	csp := buildCSP([]string{"https://player.vimeo.com", "https://evil.com; default-src *", "not-an-origin"})
	if !strings.Contains(csp, "https://player.vimeo.com") {
		t.Errorf("valid origin should be in the CSP:\n%s", csp)
	}
	if strings.Contains(csp, "default-src *") || strings.Contains(csp, "not-an-origin") {
		t.Errorf("malformed frame origins must be skipped:\n%s", csp)
	}
}

// TestOpenFileSurvivesRootClose proves the assumption the Publish cutover relies on
// (§7.9): a file opened through an os.Root keeps its own descriptor, so closing the
// root does not disturb an already-open file. This is why Reload can close the old
// bundle root immediately after the atomic swap without truncating an in-flight
// response — verified in code, not assumed.
func TestOpenFileSurvivesRootClose(t *testing.T) {
	dir := t.TempDir()
	const want = "in-flight body bytes"
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := root.Open("page.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Close the root *before* reading the file — the exact ordering Reload uses.
	if err := root.Close(); err != nil {
		t.Fatalf("closing root: %v", err)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading file after root close: %v", err)
	}
	if string(got) != want {
		t.Errorf("content after root close = %q, want %q", got, want)
	}
}

// TestReloadSwapsBundle verifies Reload cuts the server over from one built bundle to
// another in-process — new page content and new /version — with no restart, the
// mechanism behind an explicit Publish (§7.9).
func TestReloadSwapsBundle(t *testing.T) {
	srv := newServer(t) // buildBundle → build number 9
	if v := do(srv, "GET", "/version", nil).Body.String(); !strings.Contains(v, `"buildNumber":"9"`) {
		t.Fatalf("pre-reload /version = %s", v)
	}

	dir2 := buildBundleN(t, "42", "Fresh Home Heading")
	if err := srv.Reload(dir2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	rec := do(srv, "GET", "/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Fresh Home Heading") {
		t.Errorf("after reload, home = %d:\n%s", rec.Code, rec.Body.String())
	}
	if v := do(srv, "GET", "/version", nil).Body.String(); !strings.Contains(v, `"buildNumber":"42"`) {
		t.Errorf("after reload, /version = %s", v)
	}
}

// TestReloadRejectsBadBundleAndKeepsServing: a Reload pointed at a non-bundle dir
// fails and the current bundle keeps serving unchanged — a failed build never
// publishes (§7.9).
func TestReloadRejectsBadBundleAndKeepsServing(t *testing.T) {
	srv := newServer(t)
	if err := srv.Reload(t.TempDir()); err == nil { // no build.json there
		t.Fatal("Reload to a non-bundle dir should fail")
	}
	rec := do(srv, "GET", "/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Home") {
		t.Errorf("after a failed reload the old bundle must still serve; got %d:\n%s", rec.Code, rec.Body.String())
	}
}
