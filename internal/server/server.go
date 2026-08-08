// Package server is pbcssg's server mode (SPEC §7): it serves a built static
// bundle over HTTP with a small, privacy-preserving dynamic surface (/version
// and the GPC declaration). It never opens the SQLite editing store — it reads
// only the immutable, self-describing bundle produced by the build engine.
//
// It is designed to run behind a TLS-terminating reverse proxy (SPEC §7.5): it
// binds loopback (the cmd's concern), does not terminate TLS, and does not log
// client IPs. GPC (SPEC §7.2) is declared via the static /.well-known/gpc.json
// in the bundle; because the site sells/shares nothing, honoring the Sec-GPC
// signal is a no-op with nothing to switch off.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// cspHead is the fixed prefix of the self-hosted-first Content-Security-Policy
// applied to HTML responses, up to (but excluding) the frame-src directive. It is
// deliberately strict but not so narrow that it breaks extension-injected
// placeholder UI (e.g. Privacy Badger click-to-activate widgets): style-src is not
// 'none', and img-src/frame-src are not over-narrowed.
const cspHead = "default-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'self'; " +
	"object-src 'none'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; "

// frameSrcBase is the always-permitted frame-src allowlist: self plus the
// youtube-nocookie facade domain (SPEC §5.5/§5.8). The build appends the
// operator's embed-host allowlist to this via build.json.
const frameSrcBase = "frame-src 'self' https://www.youtube-nocookie.com"

// DefaultCSP is the CSP for a bundle with no extra embed hosts declared.
const DefaultCSP = cspHead + frameSrcBase

// frameOriginRE matches a clean https origin (optional "*." wildcard, host,
// optional ":port"). It is a defence-in-depth gate on the frame-src origins read
// from build.json: a token carrying a space or ";" is skipped rather than
// concatenated into the CSP header, so a tampered bundle can't inject directives.
var frameOriginRE = regexp.MustCompile(`^https://(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

// buildCSP composes the CSP, extending frame-src with the bundle's allowlisted
// embed origins (from build.json) so only those hosts can be framed. Malformed
// origins are skipped so they cannot inject into the header.
func buildCSP(frameSrc []string) string {
	fs := frameSrcBase
	for _, o := range frameSrc {
		if frameOriginRE.MatchString(o) {
			fs += " " + o
		}
	}
	return cspHead + fs
}

// Config configures the server.
type Config struct {
	// ContentDir is the path to a built bundle (must contain build.json).
	ContentDir string
	// CSP overrides the Content-Security-Policy sent with HTML responses. Empty
	// uses DefaultCSP.
	CSP string
	// Dynamic, when set, handles requests under the reserved prefix (§7.3) — the
	// explicitly-enumerated public dynamic endpoints (member auth, comments). It is nil
	// unless a runtime store is wired; the static bundle path never touches it, so the
	// public serving path stays DB-free (§7.1).
	Dynamic http.Handler
}

// ReservedPrefix is the path namespace for public dynamic endpoints (§7.3), kept out
// of the static page namespace so the bundle stays fully cacheable and a dynamic route
// can never shadow a content page.
const ReservedPrefix = "/_pbc/"

// Server serves a built bundle. It implements http.Handler and is safe for
// concurrent use. The served bundle is held behind an atomic pointer so it can be
// swapped in-process by Reload (the explicit Publish cutover, §7.9) with no restart
// and no torn state.
type Server struct {
	cur         atomic.Pointer[bundle]
	cspOverride string       // Config.CSP; re-applied on every (re)load. Empty = derive from build.json.
	contentDir  string       // configured bundle path; Reload("") re-opens it (re-resolving `current`).
	dynamic     http.Handler // reserved-prefix handler (§7.3); nil = no dynamic layer.
}

// bundle is an immutable snapshot of one built bundle: its traversal-confined root
// plus the metadata derived from build.json. A request loads the current bundle once
// (via Server.cur) and uses it throughout, so a concurrent Reload can never pair an
// old root with new ETags. Bundles are swapped wholesale, never mutated.
type bundle struct {
	root           *os.Root          // bundle dir, opened as a traversal-confined root
	csp            string            // Content-Security-Policy for HTML responses
	version        string            // build.json → /version
	buildNumber    string            // build.json → /version
	etags          map[string]string // bundle-relative path -> content hash
	metricsEnabled bool              // §7.7 master switch, baked into build.json
}

type buildInfo struct {
	Version     string            `json:"version"`
	BuildNumber string            `json:"buildNumber"`
	FrameSrc    []string          `json:"frameSrc"`
	Files       map[string]string `json:"files"`
	Metrics     bool              `json:"metrics"` // §7.7: opt-in private metrics dashboard
}

// loadBundle opens the built bundle at contentDir: it reads build.json (version,
// build number, embed hosts, per-file content hashes) and opens the directory as an
// os.Root so every file open is confined to it — "..", absolute paths, and escaping
// symlinks are refused by the kernel/runtime, not a string check. The root path
// itself is resolved here (following the deploy's `current` symlink to the live
// release); confinement then applies only to paths beneath it, so the atomic-symlink
// deploy is unaffected. cspOverride, when non-empty, replaces the build.json-derived CSP.
func loadBundle(contentDir, cspOverride string) (*bundle, error) {
	root, err := filepath.Abs(contentDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "build.json"))
	if err != nil {
		return nil, fmt.Errorf("server: reading build.json (is this a built bundle?): %w", err)
	}
	var bi buildInfo
	if err := json.Unmarshal(data, &bi); err != nil {
		return nil, fmt.Errorf("server: parsing build.json: %w", err)
	}
	csp := cspOverride
	if csp == "" {
		csp = buildCSP(bi.FrameSrc)
	}
	contentRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("server: opening bundle root %s: %w", root, err)
	}
	return &bundle{
		root:           contentRoot,
		csp:            csp,
		version:        bi.Version,
		buildNumber:    bi.BuildNumber,
		etags:          bi.Files,
		metricsEnabled: bi.Metrics,
	}, nil
}

// New builds a Server for the bundle at cfg.ContentDir. It reads build.json for the
// version, build number, and per-file content hashes (used as ETags).
func New(cfg Config) (*Server, error) {
	if cfg.ContentDir == "" {
		return nil, fmt.Errorf("server: ContentDir is required")
	}
	b, err := loadBundle(cfg.ContentDir, cfg.CSP)
	if err != nil {
		return nil, err
	}
	s := &Server{cspOverride: cfg.CSP, contentDir: cfg.ContentDir, dynamic: cfg.Dynamic}
	s.cur.Store(b)
	return s, nil
}

// Reload atomically swaps the served bundle to the one at contentDir — the explicit
// Publish cutover (§7.9). An empty contentDir re-opens the configured path, so a
// deploy that repointed the `current` symlink is picked up. If the new bundle fails
// to load, the current one keeps serving and the error is returned (a failed build
// never publishes). On success the old root is closed immediately: a file already
// opened through it keeps its own descriptor, so in-flight requests finish against
// the old bundle undisturbed (proven by TestOpenFileSurvivesRootClose).
func (s *Server) Reload(contentDir string) error {
	if contentDir == "" {
		contentDir = s.contentDir
	}
	b, err := loadBundle(contentDir, s.cspOverride)
	if err != nil {
		return err
	}
	old := s.cur.Swap(b)
	if old != nil {
		old.root.Close()
	}
	return nil
}

// Close releases the current bundle's root. The Server must not serve after Close.
func (s *Server) Close() error {
	if b := s.cur.Load(); b != nil {
		return b.root.Close()
	}
	return nil
}

// MetricsEnabled reports whether the current bundle opts into the private metrics
// dashboard (SPEC §7.7). It is the master switch: the operator must additionally
// supply a dashboard bind address for anything to be collected or served.
func (s *Server) MetricsEnabled() bool { return s.cur.Load().metricsEnabled }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Applies to every response: never MIME-sniff.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Reserved-prefix dynamic endpoints (§7.3) are dispatched first and own their own
	// methods (they accept POST for auth/comments). They never touch the static bundle,
	// keeping the static serving path DB-free (§7.1). A reserved path with no dynamic
	// layer configured is a plain 404 (an API namespace, not a themed page), and can
	// never fall through to a content file.
	if r.URL.Path == ReservedPrefix[:len(ReservedPrefix)-1] || strings.HasPrefix(r.URL.Path, ReservedPrefix) {
		if s.dynamic != nil {
			s.dynamic.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// The static site and /version are GET/HEAD only.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load the current bundle once per request so a concurrent Reload never mixes an
	// old root with new metadata; the whole request is served from this snapshot.
	b := s.cur.Load()

	if r.URL.Path == "/version" {
		s.serveVersion(w, r, b)
		return
	}
	s.serveStatic(w, r, b)
}

func (s *Server) serveVersion(w http.ResponseWriter, r *http.Request, b *bundle) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	body, _ := json.Marshal(map[string]string{
		"version":     b.version,
		"buildNumber": b.buildNumber,
	})
	w.Write(append(body, '\n'))
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, b *bundle) {
	rel, ok := bundlePath(r.URL.Path)
	if !ok {
		s.notFound(w, r, b)
		return
	}

	// build.json is internal build metadata read by New at startup; it has no client
	// purpose and its file map would let anyone enumerate every path, including
	// unlisted pages (§6.16, §7.1) — never serve it.
	if rel == "build.json" {
		s.notFound(w, r, b)
		return
	}

	// Open through the confined root: os.Root refuses any escape (".." was already
	// stripped by bundlePath; this also stops a symlink that points outside the
	// bundle from ever being served — a guarantee the old prefix check lacked).
	f, err := b.root.Open(filepath.FromSlash(rel))
	if err != nil {
		s.notFound(w, r, b)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() { // never list directories
		s.notFound(w, r, b)
		return
	}

	setHeaders(w, rel, b.csp)
	if tag := b.etags[rel]; tag != "" {
		w.Header().Set("ETag", `"`+tag+`"`)
	}
	http.ServeContent(w, r, fi.Name(), modTime(fi), f)
}

// notFound serves the bundle's themed /404.html (§7.8) with a 404 status when it is
// present, giving pbcssg server the same not-found page a reverse proxy would map
// via error_page (for local testing and edge-facing use). It falls back to a plain
// text 404 when the bundle has no 404.html (e.g. a bundle built before §7.8). The
// themed 404 is looked up directly (not via serveStatic), so there is no recursion.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request, b *bundle) {
	f, err := b.root.Open("404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	setHeaders(w, "404.html", b.csp) // Content-Type, CSP, Referrer-Policy, no-cache
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		io.Copy(w, f)
	}
}

// bundlePath maps a URL path to a bundle-relative file path using pretty-URL
// rules, returning ok=false for a path that escapes the root. A path whose last
// segment has no extension is treated as a directory and served its index.html.
func bundlePath(urlPath string) (string, bool) {
	clean := path.Clean("/" + urlPath)
	if strings.Contains(clean, "..") { // path.Clean removes these, but be explicit
		return "", false
	}
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		return "index.html", true
	}
	if !strings.Contains(path.Base(rel), ".") {
		rel += "/index.html"
	}
	return rel, true
}

// mediaTypes pins the Content-Type for the audio/video extensions the build can
// emit, so served media always has a correct type regardless of the host OS mime
// table (X-Content-Type-Options: nosniff means a missing type would break
// playback). Image/text types fall back to mime.TypeByExtension.
var mediaTypes = map[string]string{
	".mp4": "video/mp4", ".mov": "video/quicktime", ".m4a": "audio/mp4",
	".mp3": "audio/mpeg", ".wav": "audio/wav",
	".webm": "video/webm", ".weba": "audio/webm",
	".mkv": "video/x-matroska", ".mka": "audio/x-matroska",
	".oga": "audio/ogg", ".ogv": "video/ogg", ".ogg": "audio/ogg",
	".rss": "application/rss+xml", ".atom": "application/atom+xml",
	// Favicon set (§6.11): pinned so nosniff never blocks them.
	".ico": "image/x-icon", ".webmanifest": "application/manifest+json",
	// security.txt (§7.6): pinned so it serves as text/plain regardless of host mime table.
	".txt": "text/plain; charset=utf-8",
}

func setHeaders(w http.ResponseWriter, rel string, csp string) {
	ext := filepath.Ext(rel)
	ctype := mediaTypes[ext]
	if ctype == "" {
		ctype = mime.TypeByExtension(ext)
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	switch {
	case strings.HasSuffix(rel, ".html"):
		// HTML is not fingerprinted, so always revalidate.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Referrer-Policy", "no-referrer")
	case strings.HasPrefix(rel, "assets/"):
		// Assets are content-fingerprinted, so they can be cached immutably.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasPrefix(rel, "media/"):
		// Media is content-addressed (the filename is its hash), so immutable too.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasPrefix(rel, "search/"):
		// The search index lives at a fixed (non-fingerprinted) path and changes
		// every rebuild, so it must revalidate on every request — otherwise a
		// browser serves a stale index for up to max-age and new pages never show
		// up in search. no-cache forces an ETag revalidation (a cheap 304 when
		// unchanged) so search results are always fresh after a deploy.
		w.Header().Set("Cache-Control", "no-cache")
	default:
		// Manifests, gpc.json, root favicons: cacheable but revalidated via ETag.
		w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
	}
	// Sandbox any served SVG (media or the root /favicon.svg): no scripts, opaque
	// origin — defense in depth over the deny-by-default sanitizer at ingest.
	if strings.HasSuffix(rel, ".svg") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
}

func modTime(fi os.FileInfo) time.Time {
	// A zero-ish modtime disables Last-Modified; ETags drive conditional GETs.
	return fi.ModTime()
}
