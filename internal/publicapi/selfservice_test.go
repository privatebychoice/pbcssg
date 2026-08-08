package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// memberPostAuthed posts JSON with an Origin header and a member session cookie.
func memberPostAuthed(t *testing.T, h http.Handler, path string, body any, origin string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func memberMe(t *testing.T, h http.Handler, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/_pbc/auth/me", nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMemberMeReportsAlias(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)

	// A fresh member is anonymous → authenticated, empty alias.
	var me struct {
		Authenticated bool   `json:"authenticated"`
		Alias         string `json:"alias"`
	}
	json.Unmarshal(memberMe(t, h, ck).Body.Bytes(), &me)
	if !me.Authenticated || me.Alias != "" {
		t.Fatalf("me before naming = %+v, want authenticated with empty alias", me)
	}

	// The name is set on the account (not per post). /auth/me then reports it.
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "raven"}, testMemberOrigin, ck); rec.Code != http.StatusOK {
		t.Fatalf("set alias: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(memberMe(t, h, ck).Body.Bytes(), &me)
	if me.Alias != "raven" {
		t.Errorf("me.alias = %q, want raven", me.Alias)
	}

	// A posted comment carries the account alias, regardless of any alias field sent.
	if rec := postComment(t, h, map[string]string{"path": "/p", "alias": "ignored", "body": "hi"}, testMemberOrigin, ck); rec.Code != http.StatusOK {
		t.Fatalf("post: %d %s", rec.Code, rec.Body.String())
	}
	if cs, _ := app.CommentsByPage("/p", ""); len(cs) != 1 || cs[0].Alias != "raven" {
		t.Errorf("posted comment alias = %+v, want raven (account alias, not the per-post field)", cs)
	}

	// Signed out → not authenticated.
	json.Unmarshal(memberMe(t, h, nil).Body.Bytes(), &me)
	if me.Authenticated {
		t.Error("no cookie should report unauthenticated")
	}
}

func TestMemberSetAlias(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)
	// Two comments under the old alias.
	postComment(t, h, map[string]string{"path": "/a", "alias": "raven", "body": "one"}, testMemberOrigin, ck)
	postComment(t, h, map[string]string{"path": "/b", "alias": "raven", "body": "two"}, testMemberOrigin, ck)

	rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "nightbird"}, testMemberOrigin, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("set alias: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK      bool   `json:"ok"`
		Updated int    `json:"updated"`
		Alias   string `json:"alias"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || out.Updated != 2 || out.Alias != "nightbird" {
		t.Fatalf("response = %+v, want ok/updated=2/nightbird", out)
	}
	// Both comments now carry the new alias.
	for _, p := range []string{"/a", "/b"} {
		cs, _ := app.CommentsByPage(p, "")
		if len(cs) != 1 || cs[0].Alias != "nightbird" {
			t.Errorf("%s alias = %+v, want nightbird", p, cs)
		}
	}
}

func TestMemberSetAliasGuards(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)

	// No session → 401.
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "x"}, testMemberOrigin, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", rec.Code)
	}
	// Bad origin → 403.
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "x"}, "https://evil.example", ck); rec.Code != http.StatusForbidden {
		t.Errorf("bad origin: status %d, want 403", rec.Code)
	}
	// Over-long alias → 400.
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": strings.Repeat("a", 65)}, testMemberOrigin, ck); rec.Code != http.StatusBadRequest {
		t.Errorf("long alias: status %d, want 400", rec.Code)
	}
}

func TestMemberForgetAnonymizes(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)
	postComment(t, h, map[string]string{"path": "/p", "alias": "raven", "body": "keep me"}, testMemberOrigin, ck)

	rec := memberPostAuthed(t, h, "/_pbc/account/forget", map[string]bool{"deleteComments": false}, testMemberOrigin, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("forget: %d %s", rec.Code, rec.Body.String())
	}
	// The cookie is cleared.
	if c := findCookie(rec, memberCookieDev); c == nil || c.MaxAge >= 0 {
		t.Errorf("forget should clear the member cookie, got %+v", c)
	}
	// The account is gone; the comment is kept but anonymized.
	if _, ok, _ := app.AccountByID(accID); ok {
		t.Error("account should be erased")
	}
	cs, _ := app.CommentsByPage("/p", "")
	if len(cs) != 1 || cs[0].AccountID != nil || cs[0].Alias != "" || cs[0].Body != "keep me" {
		t.Errorf("comment after anonymize = %+v, want body kept, link/alias cleared", cs)
	}
}

func TestMemberForgetDeletesComments(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)
	postComment(t, h, map[string]string{"path": "/p", "alias": "raven", "body": "bye"}, testMemberOrigin, ck)

	rec := memberPostAuthed(t, h, "/_pbc/account/forget", map[string]bool{"deleteComments": true}, testMemberOrigin, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("forget+delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok, _ := app.AccountByID(accID); ok {
		t.Error("account should be erased")
	}
	if cs, _ := app.CommentsByPage("/p", ""); len(cs) != 0 {
		t.Errorf("comments should be deleted, got %d", len(cs))
	}
}

func TestMemberForgetGuards(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	// No session → 401.
	if rec := memberPostAuthed(t, h, "/_pbc/account/forget", map[string]bool{}, testMemberOrigin, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", rec.Code)
	}
	// Bad origin → 403 (and the account survives).
	if rec := memberPostAuthed(t, h, "/_pbc/account/forget", map[string]bool{}, "https://evil.example", memberCookie(t, app, accID)); rec.Code != http.StatusForbidden {
		t.Errorf("bad origin: status %d, want 403", rec.Code)
	}
	if _, ok, _ := app.AccountByID(accID); !ok {
		t.Error("a rejected forget must not erase the account")
	}
}

func TestSelfServiceWidgetAssetsPresent(t *testing.T) {
	h, _ := newMemberAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/_pbc/assets/comments.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, want := range []string{"/account/alias", "/account/forget", "Delete my account"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("comments.js missing self-service hook %q", want)
		}
	}
}
