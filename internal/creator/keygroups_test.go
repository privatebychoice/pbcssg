package creator

import (
	"strconv"
	"strings"
	"testing"
)

// itoa builds a request-path segment from an int64 id.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSanitizeGroupsField(t *testing.T) {
	// Groups on a gateable block are normalized, de-duplicated, and emptied-out.
	raw := `[{"type":"callout","callout":{"title":"T","markdown":"body"},"groups":["Members A"," members-a ","","Members B"]}]`
	got := parseBlocks(raw)
	if len(got) != 1 || got[0].Callout == nil {
		t.Fatalf("callout dropped: %+v", got)
	}
	g := got[0].Groups
	if len(g) != 2 || g[0] != "members-a" || g[1] != "members-b" {
		t.Errorf("groups not normalized/deduped: %v", g)
	}

	// A markdown block carries groups too (the default case).
	md := parseBlocks(`[{"type":"markdown","markdown":"hi","groups":["Team X"]}]`)
	if len(md) != 1 || len(md[0].Groups) != 1 || md[0].Groups[0] != "team-x" {
		t.Errorf("markdown groups wrong: %+v", md)
	}

	// A public block (no groups) has nil Groups.
	pub := parseBlocks(`[{"type":"citation","citation":{"quote":"q"}}]`)
	if len(pub) != 1 || pub[0].Groups != nil {
		t.Errorf("public block should have nil groups: %+v", pub)
	}
}

func TestKeyGroupManagerCRUD(t *testing.T) {
	h := newHarness(t)

	// The manager page loads.
	if rec := h.get("/admin/keygroups"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Key groups") {
		t.Fatalf("keygroups page: code=%d", rec.Code)
	}

	// Create normalizes the alias.
	rec := h.post("/admin/keygroups", h.form(map[string]string{"alias": "Members A"}))
	if rec.Code != 200 {
		t.Fatalf("create: code=%d", rec.Code)
	}
	groups, _ := h.st.KeyGroups()
	if len(groups) != 1 || groups[0].Alias != "members-a" {
		t.Fatalf("group not created/normalized: %+v", groups)
	}
	id := groups[0].ID

	// Duplicate alias is rejected with a friendly message.
	if rec := h.post("/admin/keygroups", h.form(map[string]string{"alias": "members-a"})); !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("duplicate alias not reported: %d\n%s", rec.Code, rec.Body.String())
	}

	// Rotate changes the KEK.
	before := groups[0].KEK
	h.post("/admin/keygroups/"+itoa(id)+"/rotate", h.form(nil))
	after, _, _ := h.st.KeyGroup("members-a")
	if string(after.KEK) == string(before) {
		t.Error("rotate did not change the KEK")
	}

	// Rename.
	h.post("/admin/keygroups/"+itoa(id)+"/rename", h.form(map[string]string{"alias": "Members B"}))
	if _, ok, _ := h.st.KeyGroup("members-b"); !ok {
		t.Error("rename did not take effect")
	}

	// Delete.
	h.post("/admin/keygroups/"+itoa(id)+"/delete", h.form(nil))
	if gs, _ := h.st.KeyGroups(); len(gs) != 0 {
		t.Errorf("group not deleted: %+v", gs)
	}
}

func TestKeyGroupSplashAndGateLink(t *testing.T) {
	h := newHarness(t)

	// A page to use as the splash, and a group.
	h.post("/pages", h.form(map[string]string{"title": "Members", "path": "/members", "body": "# Members"}))
	h.post("/admin/keygroups", h.form(map[string]string{"alias": "members-a"}))
	pages, _ := h.st.Pages()
	groups, _ := h.st.KeyGroups()
	pid, gid := pages[0].ID, groups[0].ID

	// Before a splash is set, the manager still shows a working gate link, pointing at
	// the generic /unlock/<alias> fallback deposit page.
	body := h.get("/admin/keygroups").Body.String()
	if !strings.Contains(body, "https://tul.example/unlock/members-a#k=") {
		t.Errorf("generic fallback gate link missing before splash:\n%s", body)
	}

	// Associate the splash page.
	if rec := h.post("/admin/keygroups/"+itoa(gid)+"/splash", h.form(map[string]string{"splash": itoa(pid)})); rec.Code != 200 {
		t.Fatalf("set splash: code=%d", rec.Code)
	}
	g, _, _ := h.st.KeyGroup("members-a")
	if g.SplashPageID == nil || *g.SplashPageID != pid {
		t.Fatalf("splash not associated: %+v", g.SplashPageID)
	}

	// Now the manager renders a full gate link containing the base URL, the splash
	// path, and the #k= fragment.
	body = h.get("/admin/keygroups").Body.String()
	if !strings.Contains(body, "https://tul.example/members#k=") {
		t.Errorf("gate link missing/incorrect:\n%s", body)
	}

	// Clearing the splash removes the association.
	h.post("/admin/keygroups/"+itoa(gid)+"/splash", h.form(map[string]string{"splash": "0"}))
	if g2, _, _ := h.st.KeyGroup("members-a"); g2.SplashPageID != nil {
		t.Error("splash association not cleared")
	}
}

func TestNormalizeLocalTestURL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  ", "", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://127.0.0.1:8080/", "http://127.0.0.1:8080", true},
		{"https://localhost:8443/some/path", "https://localhost:8443", true},
		{"127.0.0.1:8080", "", false},  // no scheme
		{"ftp://x.example", "", false}, // wrong scheme
		{"not a url", "", false},
	}
	for _, tc := range cases {
		got, err := normalizeLocalTestURL(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("normalizeLocalTestURL(%q) ok=%v, want %v (err=%v)", tc.in, err == nil, tc.ok, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeLocalTestURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKeyGroupsLocalTestLink(t *testing.T) {
	h := newHarness(t)
	h.post("/admin/keygroups", h.form(map[string]string{"alias": "members-a"}))

	// Without a local test URL, no Local Test button appears — only the discovery tip.
	body := h.get("/admin/keygroups").Body.String()
	if strings.Contains(body, "local-test") {
		t.Error("Local Test button shown before a local test URL is configured")
	}
	if !strings.Contains(body, "Local server test URL") {
		t.Error("missing the tip to set a local test URL")
	}

	// Save a local test URL via the settings form (validated + normalized).
	if rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"localTestURL": "http://127.0.0.1:9090/",
	})); rec.Code != 200 {
		t.Fatalf("save settings: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if got := h.c.localTestURL(); got != "http://127.0.0.1:9090" {
		t.Fatalf("local test URL not stored/normalized: %q", got)
	}

	// Now each gate link offers a Local Test link pointing at the loopback origin,
	// same path + fragment as the production link.
	body = h.get("/admin/keygroups").Body.String()
	if !strings.Contains(body, `class="btn local-test"`) {
		t.Errorf("Local Test button missing after configuring the URL:\n%s", body)
	}
	if !strings.Contains(body, "http://127.0.0.1:9090/unlock/members-a#k=") {
		t.Errorf("Local Test link does not target the loopback origin:\n%s", body)
	}

	// An invalid local test URL is rejected without wiping the form.
	if rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"localTestURL": "127.0.0.1:9090",
	})); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Local server test URL") {
		t.Errorf("invalid local test URL should be rejected: code=%d", rec.Code)
	}
}

func TestPreviewShowsGatedBlockWithLabel(t *testing.T) {
	h := newHarness(t)
	h.post("/admin/keygroups", h.form(map[string]string{"alias": "members-a"}))

	blocks := `[{"type":"callout","callout":{"title":"Secret","markdown":"door code 4417"},"groups":["members-a"]}]`
	rec := h.post("/preview", h.form(map[string]string{"body": "# Members", "blocks": blocks}))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("preview code=%d", rec.Code)
	}
	// The operator's preview shows the gated content with a group label (their own
	// view), not the encrypted form.
	if !strings.Contains(body, "pbcssg-gate-preview") || !strings.Contains(body, "members-a") {
		t.Errorf("preview missing gated label:\n%s", body)
	}
	if !strings.Contains(body, "door code 4417") {
		t.Errorf("preview should show gated content to the operator:\n%s", body)
	}
	// It must NOT emit the encrypted markup in preview.
	if strings.Contains(body, "data-pbcssg-gate=") {
		t.Errorf("preview should not encrypt gated blocks:\n%s", body)
	}
}
