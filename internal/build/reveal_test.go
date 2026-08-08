package build

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

// publishReveal creates and publishes a page carrying one reveal block and returns
// its page id (needed to exercise rekey).
func publishReveal(t *testing.T, s *store.Store, path, secret string) int64 {
	t.Helper()
	pid, err := s.CreatePage(store.Page{Path: path, Slug: strings.Trim(path, "/"), Title: "Contact"})
	if err != nil {
		t.Fatal(err)
	}
	cj := `{"body":"# Contact","blocks":[{"type":"reveal","reveal":{"content":"` + secret + `","label":"Reveal email","kind":"email"}}]}`
	rid, err := s.SaveRevision(pid, cj, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func buildRevealFixture(t *testing.T, s *store.Store, out string) *Report {
	t.Helper()
	rep, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func TestBuildReveal(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const secret = "agent@example.com"
	publishReveal(t, s, "/contact", secret)
	publish(t, s, "/plain", "Plain", "# Plain\n\nNo hidden content.")

	out := t.TempDir()
	buildRevealFixture(t, s, out)

	page := read(t, out, "contact/index.html")
	// The plaintext is never in the built HTML — only ciphertext is.
	if strings.Contains(page, secret) {
		t.Errorf("reveal plaintext leaked into the built page:\n%s", page)
	}
	for _, want := range []string{`data-pbcssg-reveal`, `data-kind="email"`, `data-ct="`, `data-key="`,
		`<button type="button" class="pbcssg-reveal-btn" aria-expanded="false">Reveal email</button>`} {
		if !strings.Contains(page, want) {
			t.Errorf("built reveal page missing %q:\n%s", want, page)
		}
	}

	// reveal.js is emitted and linked from the page that uses it.
	js := readAsset(t, out, "pbcssg-reveal")
	if !strings.Contains(js, "data-pbcssg-reveal") || !strings.Contains(js, "AES-GCM") {
		t.Errorf("pbcssg-reveal.js content unexpected")
	}
	if !regexp.MustCompile(`<script src="/assets/pbcssg-reveal\.[0-9a-f]+\.js"`).MatchString(page) {
		t.Errorf("reveal page does not link the decode script:\n%s", page)
	}

	// A page without a reveal block must not ship the decode script.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "pbcssg-reveal") {
		t.Errorf("a page with no reveal block should not reference reveal.js:\n%s", plain)
	}

	// The shipped ciphertext actually decrypts back to the secret with the shipped key.
	if got := decodeBuiltReveal(t, page); got != secret {
		t.Errorf("built reveal round-trip: got %q, want %q", got, secret)
	}
}

func TestBuildRevealDeterministicAndRekey(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid := publishReveal(t, s, "/contact", "agent@example.com")

	// Two builds of the same store produce byte-identical reveal output (the key is
	// stored, not per-build), so reveal blocks never churn build.json hashes.
	out1, out2 := t.TempDir(), t.TempDir()
	buildRevealFixture(t, s, out1)
	buildRevealFixture(t, s, out2)
	p1, p2 := read(t, out1, "contact/index.html"), read(t, out2, "contact/index.html")
	if p1 != p2 {
		t.Errorf("reveal build not deterministic:\n--- build 1 ---\n%s\n--- build 2 ---\n%s", p1, p2)
	}

	// Rekey rotates the key, so the ciphertext (and thus the page) changes.
	if _, err := s.RekeyPage(pid); err != nil {
		t.Fatal(err)
	}
	out3 := t.TempDir()
	buildRevealFixture(t, s, out3)
	if p3 := read(t, out3, "contact/index.html"); p3 == p1 {
		t.Errorf("rekey did not change the built reveal ciphertext")
	}
}

func TestBuildRevealGated(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const secret = "vault code 4417"
	const code = "hunter2"
	pid, err := s.CreatePage(store.Page{Path: "/members", Slug: "members", Title: "Members"})
	if err != nil {
		t.Fatal(err)
	}
	cj := `{"body":"# Members","blocks":[{"type":"reveal","reveal":{"content":"` + secret + `","label":"Members only","code":"` + code + `"}}]}`
	rid, err := s.SaveRevision(pid, cj, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	buildRevealFixture(t, s, out)
	page := read(t, out, "members/index.html")

	// Neither the plaintext nor the gate code may reach the built page.
	if strings.Contains(page, secret) || strings.Contains(page, code) {
		t.Errorf("gated build leaked plaintext or code:\n%s", page)
	}
	for _, want := range []string{`data-gated="1"`, `data-salt="`, `data-iters="`} {
		if !strings.Contains(page, want) {
			t.Errorf("gated build missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "data-key=") {
		t.Errorf("gated build must not ship a decode key:\n%s", page)
	}
	// The right code decrypts; a wrong one does not.
	if got, err := decodeGatedReveal(t, page, code); err != nil || got != secret {
		t.Errorf("correct code failed: got %q err %v", got, err)
	}
	if _, err := decodeGatedReveal(t, page, "nope"); err == nil {
		t.Error("wrong code should not decrypt")
	}
}

func TestBuildRevealMarkdown(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A markdown reveal block whose body has formatting and an external link.
	pid, err := s.CreatePage(store.Page{Path: "/notes", Slug: "notes", Title: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	md := "Secret **bold** and a [tracker](https://tracker.example/x)."
	cj := `{"body":"# Notes","blocks":[{"type":"reveal","reveal":{"content":` + strconv.Quote(md) + `,"label":"Reveal","kind":"markdown"}}]}`
	rid, err := s.SaveRevision(pid, cj, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	buildRevealFixture(t, s, out)
	page := read(t, out, "notes/index.html")

	// The hidden text stays out of the source (the ciphertext is opaque); kind is
	// markdown. The link's *domain* legitimately appears in the external-references
	// disclosure aside — that is option C surfacing it, not a leak.
	if strings.Contains(page, "Secret") || strings.Contains(page, "<strong>bold") {
		t.Errorf("markdown reveal leaked hidden content into the page:\n%s", page)
	}
	if !strings.Contains(page, `data-kind="markdown"`) {
		t.Errorf("markdown reveal kind missing:\n%s", page)
	}

	// The decrypted payload is rendered HTML (formatting), and option B hardened its
	// external link (rel/referrerpolicy).
	html := decodeBuiltReveal(t, page)
	if !strings.Contains(html, "<strong>bold</strong>") {
		t.Errorf("markdown not rendered in reveal payload: %q", html)
	}
	if !strings.Contains(html, `rel="noopener noreferrer"`) || !strings.Contains(html, "referrerpolicy") {
		t.Errorf("option B: reveal markdown link not hardened: %q", html)
	}

	// Option C: the hidden link's domain is disclosed in the page's privacy manifest.
	if !hasDomain(t, out, "manifest/notes.json", "tracker.example") {
		t.Errorf("option C: hidden markdown link not in the page manifest")
	}
}

var builtRevealAttrRE = map[string]*regexp.Regexp{
	"ct":    regexp.MustCompile(`data-ct="([^"]*)"`),
	"iv":    regexp.MustCompile(`data-iv="([^"]*)"`),
	"key":   regexp.MustCompile(`data-key="([^"]*)"`),
	"salt":  regexp.MustCompile(`data-salt="([^"]*)"`),
	"iters": regexp.MustCompile(`data-iters="([0-9]+)"`),
}

// decodeGatedReveal decrypts a Mode B block from built HTML the way the client
// does: PBKDF2-derive the key from the code + shipped salt/iters, then AES-GCM.
func decodeGatedReveal(t *testing.T, html, code string) (string, error) {
	t.Helper()
	b64 := func(name string) []byte {
		m := builtRevealAttrRE[name].FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("reveal data-%s not found", name)
		}
		v, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			t.Fatalf("decode data-%s: %v", name, err)
		}
		return v
	}
	itersM := builtRevealAttrRE["iters"].FindStringSubmatch(html)
	if itersM == nil {
		t.Fatal("data-iters not found")
	}
	iters, _ := strconv.Atoi(itersM[1])
	dk, err := pbkdf2.Key(sha256.New, code, b64("salt"), iters, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(dk)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := gcm.Open(nil, b64("iv"), b64("ct"), nil)
	return string(pt), err
}

// decodeBuiltReveal decrypts the reveal block from built (post-hygiene) HTML, where
// the base64 attributes are literal (no HTML entities), the way a browser would.
func decodeBuiltReveal(t *testing.T, html string) string {
	t.Helper()
	grab := func(name string) []byte {
		m := builtRevealAttrRE[name].FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("reveal data-%s not found", name)
		}
		b, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			t.Fatalf("decode data-%s (%q): %v", name, m[1], err)
		}
		return b
	}
	block, err := aes.NewCipher(grab("key"))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := gcm.Open(nil, grab("iv"), grab("ct"), nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	return string(pt)
}
