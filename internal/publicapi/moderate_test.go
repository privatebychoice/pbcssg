package publicapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// seedModeratorSession creates a moderator account with a live session and returns its
// cookie.
func seedModeratorSession(t *testing.T, app *appstore.Store) *http.Cookie {
	t.Helper()
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Mo"})
	acc, err := app.RedeemInviteAndRegister(code, "mod-session-handle", appstore.Credential{
		CredID: rawURL([]byte("mod-session-cred")), PublicKey: []byte("k"),
	}, appstore.RoleModerator)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := app.CreateSession(acc.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: modCookieDev, Value: token}
}

func getModerate(t *testing.T, h http.Handler, query string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/_pbc/moderate"+query, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestModerateUnauthenticatedShowsSignIn(t *testing.T) {
	h, _ := newMemberAPI(t)
	rec := getModerate(t, h, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sign in with a passkey") {
		t.Error("unauthenticated page missing the sign-in shell")
	}
	if strings.Contains(body, "mod-filter") {
		t.Error("unauthenticated page must not render the moderation table")
	}
}

func TestModerateAuthenticatedShowsTable(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	if _, err := app.AddComment(mem.ID, "/blog/x", "raven", "hello there"); err != nil {
		t.Fatal(err)
	}
	rec := getModerate(t, h, "", ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"mod-filter", "Signed in", "Mo",
		"hello there", "raven", "/blog/x",
		"/approve", "/reject", "/delete",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("table view missing %q", want)
		}
	}
}

func TestModerateApprovedFilter(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	c, _ := app.AddComment(mem.ID, "/p", "a", "already approved")
	app.SetCommentStatus(c.ID, appstore.CommentApproved)

	// Default (pending) view excludes it; the approved filter shows it with Unpublish.
	if body := getModerate(t, h, "", ck).Body.String(); strings.Contains(body, "already approved") {
		t.Error("approved comment leaked into the default pending view")
	}
	body := getModerate(t, h, "?status=approved", ck).Body.String()
	if !strings.Contains(body, "already approved") {
		t.Error("approved filter did not list the approved comment")
	}
	if !strings.Contains(body, "Unpublish") {
		t.Error("approved row missing the Unpublish action")
	}
}

func TestModerateApproveAction(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	c, _ := app.AddComment(mem.ID, "/p", "a", "approve me")

	form := url.Values{"ctx": {"status=pending&sort=posted&dir=asc"}, "p": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/_pbc/moderate/comments/"+strconv.FormatInt(c.ID, 10)+"/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve status %d, want 303", rec.Code)
	}
	if got, _ := app.CommentsByPage("/p", appstore.CommentApproved); len(got) != 1 {
		t.Errorf("comment not approved after action")
	}
}

func TestModerateActionRequiresSession(t *testing.T) {
	h, app := newMemberAPI(t)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	c, _ := app.AddComment(mem.ID, "/p", "a", "x")
	req := httptest.NewRequest(http.MethodPost, "/_pbc/moderate/comments/"+strconv.FormatInt(c.ID, 10)+"/approve", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", rec.Code)
	}
	if cm, _, _ := app.CommentByID(c.ID); cm.Status != appstore.CommentPending {
		t.Errorf("comment changed without a moderator session: %q", cm.Status)
	}
}

func TestModerateActionRejectsMemberSession(t *testing.T) {
	h, app := newMemberAPI(t)
	// A member session (not moderator) must not drive a moderation action.
	_, _, _, memberID := seedMember(t, app)
	token, _, _ := app.CreateSession(memberID, time.Hour)
	c, _ := app.AddComment(memberID, "/p", "a", "x")

	req := httptest.NewRequest(http.MethodPost, "/_pbc/moderate/comments/"+strconv.FormatInt(c.ID, 10)+"/approve", nil)
	req.AddCookie(&http.Cookie{Name: modCookieDev, Value: token}) // member token under the mod cookie name
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("member session on moderation action: status %d, want 401", rec.Code)
	}
}

func TestModeratePagination(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	for i := 0; i < 30; i++ {
		app.AddComment(mem.ID, "/p", "a", "comment "+strconv.Itoa(i))
	}
	body := getModerate(t, h, "", ck).Body.String()
	if !strings.Contains(body, "of 30 pending comments") {
		t.Error("missing total-count summary")
	}
	if !strings.Contains(body, "p=2") {
		t.Error("page 1 should link to page 2 (30 comments, 25/page)")
	}
}

func TestModerateAssetsServed(t *testing.T) {
	h, _ := newMemberAPI(t)
	for _, p := range []string{moderateCSSPath, moderateJSPath} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", p, rec.Code)
		}
	}
}
