package build

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/store"
)

var (
	gateCtRE   = regexp.MustCompile(`data-pbcssg-gate="" data-ct="([^"]*)" data-iv="([^"]*)"`)
	gateWrapRE = regexp.MustCompile(`data-w="([^"]*)" data-wiv="([^"]*)"`)
)

// gcmOpen decrypts base64 ciphertext+nonce under key. ok reports GCM auth success.
func gcmOpen(t *testing.T, key []byte, ctB64, ivB64 string) (pt []byte, ok bool) {
	t.Helper()
	ct, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		t.Fatalf("decode ct: %v", err)
	}
	iv, err := base64.StdEncoding.DecodeString(ivB64)
	if err != nil {
		t.Fatalf("decode iv: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	out, err := gcm.Open(nil, iv, ct, nil)
	return out, err == nil
}

// decodeBuiltGate trial-unwraps a gated block from built HTML the way the client
// keyring does: for each shipped wrapped-DEK, try to unwrap it with the KEK; the
// first GCM success yields the DEK, which decrypts the block content. Returns the
// decrypted HTML and whether the KEK unlocked it.
func decodeBuiltGate(t *testing.T, html string, kek []byte) (string, bool) {
	t.Helper()
	m := gateCtRE.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("gate data-ct/data-iv not found:\n%s", html)
	}
	ctB64, ivB64 := m[1], m[2]
	for _, w := range gateWrapRE.FindAllStringSubmatch(html, -1) {
		dek, ok := gcmOpen(t, kek, w[1], w[2])
		if !ok {
			continue
		}
		pt, ok := gcmOpen(t, dek, ctB64, ivB64)
		if !ok {
			t.Fatalf("DEK unwrapped but content failed to decrypt")
		}
		return string(pt), true
	}
	return "", false
}

// countGateWraps returns the number of wrapped-DEK blobs shipped for the first (only)
// gated block in html.
func countGateWraps(html string) int {
	return len(gateWrapRE.FindAllStringSubmatch(html, -1))
}

// publishGated creates and publishes a page with a single gated markdown block
// authorizing the given comma-separated aliases, and returns its page id.
func publishGated(t *testing.T, s *store.Store, path, markdown string, aliases []string) int64 {
	t.Helper()
	pid, err := s.CreatePage(store.Page{Path: path, Slug: strings.Trim(path, "/"), Title: "Members"})
	if err != nil {
		t.Fatal(err)
	}
	groups := `"` + strings.Join(aliases, `","`) + `"`
	md, _ := json.Marshal(markdown)
	cj := `{"body":"# Members","blocks":[{"type":"markdown","markdown":` +
		string(md) + `,"groups":[` + groups + `]}]}`
	rid, err := s.SaveRevision(pid, cj, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	return pid
}

// TestBuildGatedNewBlockTypes verifies that code, details, gallery, and index blocks
// can be group-gated: their content is absent from the built page and only the group
// KEK unlocks it. Each type lives on its own page so decodeBuiltGate finds one gate.
func TestBuildGatedNewBlockTypes(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}

	publishRaw(t, s, "/gcode", "gcode", "GCode",
		`{"body":"# Code","blocks":[{"type":"code","code":{"text":"api_key=SECRET42"},"groups":["members-a"]}]}`)
	publishRaw(t, s, "/gdetails", "gdetails", "GDetails",
		`{"body":"# FAQ","blocks":[{"type":"details","details":{"summary":"Members Q","markdown":"members ANSWER"},"groups":["members-a"]}]}`)
	publishRaw(t, s, "/gindex", "gindex", "GIndex",
		`{"body":"# Index","isIndex":true,"blocks":[{"type":"index","index":{"title":"Secret list"},"groups":["members-a"]}]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cases := []struct{ path, leak, wantInner string }{
		{"gcode/index.html", "SECRET42", "pbcssg-code"},
		{"gdetails/index.html", "members ANSWER", "pbcssg-details"},
		{"gindex/index.html", "Secret list", "pbcssg-index"},
	}
	for _, c := range cases {
		page := read(t, out, c.path)
		if strings.Contains(page, c.leak) {
			t.Errorf("%s: gated content leaked into the page:\n%s", c.path, page)
		}
		if !strings.Contains(page, "data-pbcssg-gate") {
			t.Errorf("%s: gate markup missing:\n%s", c.path, page)
		}
		html, ok := decodeBuiltGate(t, page, g.KEK)
		if !ok {
			t.Errorf("%s: group KEK could not unlock the gated block", c.path)
			continue
		}
		if !strings.Contains(html, c.wantInner) {
			t.Errorf("%s: decoded inner HTML missing %q: %q", c.path, c.wantInner, html)
		}
	}
}

// TestBuildGatedTagGallery verifies the ordering fix: a *tag-mode* gallery that is
// also group-gated must be resolved (PrepareGallery) before it is encrypted
// (PrepareGated), so the decoded content contains the tagged image, not an empty grid.
func TestBuildGatedTagGallery(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutAsset(store.AssetData{Asset: store.Asset{SHA256: "aa11", Filename: "p.png", Format: "png", MIME: "image/png"}, Data: []byte("aa11")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaTags("aa11", []string{"secret"}); err != nil {
		t.Fatal(err)
	}
	publishRaw(t, s, "/g", "g", "G",
		`{"body":"# G","blocks":[{"type":"gallery","gallery":{"mode":"tag","tag":"secret"},"groups":["members-a"]}]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "g/index.html")
	if strings.Contains(page, "aa11.png") {
		t.Errorf("gated gallery image reference leaked into the page:\n%s", page)
	}
	html, ok := decodeBuiltGate(t, page, g.KEK)
	if !ok {
		t.Fatal("group KEK could not unlock the gated gallery")
	}
	if !strings.Contains(html, "/media/aa11.png") {
		t.Errorf("gated tag-gallery resolved to no images (ordering bug?): %q", html)
	}
}

var revealCModeRE = regexp.MustCompile(`data-kind="[^"]*" data-ct="([^"]*)" data-iv="([^"]*)"`)

// TestBuildRevealModeC verifies the members-only reveal (Mode C, §6.9/§6.10): a reveal
// block carrying group aliases is envelope-encrypted under the group KEK (not the Mode
// A page key, not a Mode B code) and unlocked from the keyring.
func TestBuildRevealModeC(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateKeyGroup("members-z")
	if err != nil {
		t.Fatal(err)
	}
	publishRaw(t, s, "/m", "m", "M",
		`{"body":"# M","blocks":[{"type":"reveal","reveal":{"content":"SECRET-C","label":"Show","kind":"text"},"groups":["members-a"]}]}`)

	out := t.TempDir()
	if _, err := Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	page := read(t, out, "m/index.html")

	if strings.Contains(page, "SECRET-C") {
		t.Errorf("members-only reveal leaked plaintext:\n%s", page)
	}
	// Mode C markup: keyring unlock, not Mode A key or Mode B code.
	if !strings.Contains(page, `data-group="1"`) || !strings.Contains(page, "pbcssg-reveal-key") {
		t.Errorf("Mode C markup missing:\n%s", page)
	}
	if strings.Contains(page, "data-key=") || strings.Contains(page, "data-gated=") {
		t.Errorf("Mode C must not ship a Mode A key or Mode B code gate:\n%s", page)
	}
	// The group KEK trial-unwraps back to the content; a different group's key does not.
	m := revealCModeRE.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("reveal data-ct/data-iv not found:\n%s", page)
	}
	decode := func(kek []byte) (string, bool) {
		for _, w := range gateWrapRE.FindAllStringSubmatch(page, -1) {
			dek, ok := gcmOpen(t, kek, w[1], w[2])
			if !ok {
				continue
			}
			pt, ok := gcmOpen(t, dek, m[1], m[2])
			if ok {
				return string(pt), true
			}
		}
		return "", false
	}
	if pt, ok := decode(g.KEK); !ok || pt != "SECRET-C" {
		t.Errorf("members-a KEK should unlock to SECRET-C, got %q ok=%v", pt, ok)
	}
	if _, ok := decode(other.KEK); ok {
		t.Errorf("members-z KEK must not unlock the reveal")
	}
}

func TestBuildGatedSingleGroup(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "Members-only: the door code is 4417."
	publishGated(t, s, "/members", secret, []string{"members-a"})
	publish(t, s, "/plain", "Plain", "# Plain\n\nNo gated content.")

	out := t.TempDir()
	buildRevealFixture(t, s, out)

	page := read(t, out, "members/index.html")
	// The plaintext is never in the built HTML — only ciphertext is.
	if strings.Contains(page, "door code") {
		t.Errorf("gated plaintext leaked into the built page:\n%s", page)
	}
	// The gate markup is present with exactly one wrapped-DEK (one authorized group).
	if !strings.Contains(page, "data-pbcssg-gate") || !strings.Contains(page, "pbcssg-gate-out") {
		t.Errorf("gate markup missing:\n%s", page)
	}
	if n := countGateWraps(page); n != 1 {
		t.Errorf("want 1 wrapped-DEK, got %d", n)
	}
	// The group KEK trial-unwraps back to the rendered (goldmark) HTML.
	html, ok := decodeBuiltGate(t, page, g.KEK)
	if !ok {
		t.Fatal("group KEK could not unlock the gated block")
	}
	if !strings.Contains(html, "door code is 4417") {
		t.Errorf("gated round-trip wrong: %q", html)
	}
	// A different group's key does not unlock it.
	other, _ := s.CreateKeyGroup("members-z")
	if _, ok := decodeBuiltGate(t, page, other.KEK); ok {
		t.Error("an unauthorized group key unlocked the block")
	}
	// A page without a gated block ships no gate markup.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "data-pbcssg-gate") {
		t.Errorf("a page with no gated block should not ship gate markup")
	}
}

func TestBuildGatedMultiGroupOR(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a, _ := s.CreateKeyGroup("members-a")
	b, _ := s.CreateKeyGroup("members-b")
	c, _ := s.CreateKeyGroup("members-c")
	publishGated(t, s, "/members", "Shared with A or B.", []string{"members-a", "members-b"})

	out := t.TempDir()
	buildRevealFixture(t, s, out)
	page := read(t, out, "members/index.html")

	// Two authorized groups ⇒ two wrapped-DEKs; OR logic means either unlocks it.
	if n := countGateWraps(page); n != 2 {
		t.Fatalf("want 2 wrapped-DEKs, got %d", n)
	}
	for _, g := range []store.KeyGroup{a, b} {
		if html, ok := decodeBuiltGate(t, page, g.KEK); !ok || !strings.Contains(html, "Shared with A or B") {
			t.Errorf("authorized group %q failed to unlock: ok=%v", g.Alias, ok)
		}
	}
	if _, ok := decodeBuiltGate(t, page, c.KEK); ok {
		t.Error("unauthorized group C unlocked the block")
	}
}

func TestBuildGatedDeterministicAndRotate(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	g, _ := s.CreateKeyGroup("members-a")
	publishGated(t, s, "/members", "Stable secret.", []string{"members-a"})

	// Two builds of the same store are byte-identical (stored keys, deterministic
	// nonces), so gated blocks never churn build.json hashes.
	out1, out2 := t.TempDir(), t.TempDir()
	buildRevealFixture(t, s, out1)
	buildRevealFixture(t, s, out2)
	p1, p2 := read(t, out1, "members/index.html"), read(t, out2, "members/index.html")
	if p1 != p2 {
		t.Errorf("gated build not deterministic")
	}

	// Rotating the group KEK re-wraps the DEK, so the old key no longer unlocks and
	// the new one does; the built page changes.
	rot, err := s.RotateKeyGroup(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	out3 := t.TempDir()
	buildRevealFixture(t, s, out3)
	p3 := read(t, out3, "members/index.html")
	if p3 == p1 {
		t.Error("KEK rotation did not change the built page")
	}
	if _, ok := decodeBuiltGate(t, p3, g.KEK); ok {
		t.Error("rotated-out KEK still unlocks the block")
	}
	if _, ok := decodeBuiltGate(t, p3, rot.KEK); !ok {
		t.Error("rotated-in KEK does not unlock the block")
	}
}

func TestBuildGatedClassifiesHiddenLink(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.CreateKeyGroup("members-a")
	// A gated markdown block containing an external link: its bytes are encrypted out
	// of the page, but option C must still disclose the domain in the manifest.
	publishGated(t, s, "/members",
		"See [tracker](https://tracker.example/x) for details.", []string{"members-a"})

	out := t.TempDir()
	buildRevealFixture(t, s, out)
	page := read(t, out, "members/index.html")

	// The hidden URL text is not in the page (encrypted), but the domain is disclosed.
	if strings.Contains(page, "tracker.example/x") {
		t.Errorf("gated link target leaked into the page")
	}
	if !hasDomain(t, out, "manifest/members.json", "tracker.example") {
		t.Errorf("option C: hidden gated link not classified into the page manifest")
	}
}

func TestBuildGatedShipsScriptAndLock(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.CreateKeyGroup("members-a")
	publishGated(t, s, "/members", "Secret.", []string{"members-a"})
	publish(t, s, "/plain", "Plain", "# Plain\n\nNothing gated.")

	out := t.TempDir()
	buildRevealFixture(t, s, out)

	page := read(t, out, "members/index.html")
	// A gated page links the self-hosted gate script and offers the lock control.
	if !regexp.MustCompile(`<script src="/assets/pbcssg-gate\.[0-9a-f]+\.js"`).MatchString(page) {
		t.Errorf("gated page does not link the gate script:\n%s", page)
	}
	if !strings.Contains(page, `data-pbcssg-lock`) {
		t.Errorf("gated page missing the lock control:\n%s", page)
	}
	// The script is a real keyring/trial-unwrap decoder.
	js := readAsset(t, out, "pbcssg-gate")
	for _, want := range []string{"pbcssg-keyring", "data-pbcssg-gate", "AES-GCM", "data-pbcssg-splash"} {
		if !strings.Contains(js, want) {
			t.Errorf("pbcssg-gate.js missing %q", want)
		}
	}
	// A page with no gated block and no splash role ships neither the script nor lock.
	if plain := read(t, out, "plain/index.html"); strings.Contains(plain, "pbcssg-gate") || strings.Contains(plain, "data-pbcssg-lock") {
		t.Errorf("a plain page should not ship gate script or lock control:\n%s", plain)
	}
}

func TestBuildSplashPage(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}
	// A splash page with no gated block of its own, associated with the group.
	pid, err := s.CreatePage(store.Page{Path: "/welcome", Slug: "welcome", Title: "Welcome"})
	if err != nil {
		t.Fatal(err)
	}
	rid, err := s.SaveRevision(pid, `{"body":"# Welcome members"}`, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyGroupSplash(g.ID, &pid); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	buildRevealFixture(t, s, out)
	page := read(t, out, "welcome/index.html")

	// The splash page carries the deposit marker for its alias and links the gate
	// script, even though it has no gated block itself.
	if !strings.Contains(page, `data-pbcssg-splash="members-a"`) {
		t.Errorf("splash page missing its deposit marker:\n%s", page)
	}
	if !regexp.MustCompile(`<script src="/assets/pbcssg-gate\.[0-9a-f]+\.js"`).MatchString(page) {
		t.Errorf("splash page does not link the gate script:\n%s", page)
	}
	// Visited without a fragment the splash is a normal public page (welcome content
	// present, no key material anywhere).
	if !strings.Contains(page, "Welcome members") {
		t.Errorf("splash welcome content missing:\n%s", page)
	}
}

func TestBuildGateFallbackPage(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A group with NO splash page still gets a generic /unlock/<alias> deposit page.
	s.CreateKeyGroup("members-a")
	// A second group WITH a splash uses that page instead (no fallback for it).
	g2, _ := s.CreateKeyGroup("members-b")
	pid, _ := s.CreatePage(store.Page{Path: "/welcome-b", Slug: "welcome-b", Title: "Welcome B"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Welcome B"}`, "editor")
	s.Publish(pid, rid)
	s.SetKeyGroupSplash(g2.ID, &pid)

	out := t.TempDir()
	buildRevealFixture(t, s, out)

	// The splash-less group's generic deposit page exists and is a proper splash.
	page := read(t, out, "unlock/members-a/index.html")
	if !strings.Contains(page, `data-pbcssg-splash="members-a"`) {
		t.Errorf("fallback page missing its deposit marker:\n%s", page)
	}
	if !regexp.MustCompile(`<script src="/assets/pbcssg-gate\.[0-9a-f]+\.js"`).MatchString(page) {
		t.Errorf("fallback page does not link the gate script")
	}
	// It is noindex (a utility deposit page) and ships no key material.
	if !strings.Contains(page, `content="noindex"`) {
		t.Errorf("fallback page should be noindex:\n%s", page)
	}
	if strings.Contains(page, "#k=") {
		t.Errorf("fallback page must not contain any key material")
	}
	// The group WITH a splash gets no generic fallback page.
	if _, err := os.Stat(filepath.Join(out, "unlock", "members-b", "index.html")); !os.IsNotExist(err) {
		t.Errorf("a group with a splash should not get a generic fallback page")
	}
}

func TestBuildGatedUnknownGroupFails(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// No key group exists, so the gated block authorizes no known KEK — the build must
	// fail loudly rather than publish plaintext.
	publishGated(t, s, "/members", "Secret.", []string{"members-a"})

	_, err = Run(s, Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}, t.TempDir())
	if err == nil {
		t.Fatal("build should fail when a gated block references an unknown key group")
	}
	if !strings.Contains(err.Error(), "key group") {
		t.Errorf("unexpected error: %v", err)
	}
}
