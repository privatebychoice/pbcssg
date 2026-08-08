package publicapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// banAuthorForm posts the moderator "ban author" action for a comment id.
func banAuthorForm(t *testing.T, h http.Handler, id int64, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return modFormPost(t, h, "/_pbc/moderate/comments/"+strconv.FormatInt(id, 10)+"/ban-author", url.Values{"ctx": {"status=pending"}, "p": {"1"}}, ck)
}

// TestModBanAuthorFromComment: a Can-ban moderator bans a member author from the table;
// a moderator without the grant is refused, and a staff author is never bannable.
func TestModBanAuthorFromComment(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, _ := seedBannerSession(t, app) // moderator WITH can_ban

	m, _ := app.CreateAccount(appstore.RoleMember, "")
	app.SetAccountAlias(m.ID, "Spammer")
	cm, _ := app.AddComment(m.ID, "/p", "Spammer", "spam")

	if rec := banAuthorForm(t, h, cm.ID, ck); rec.Code != http.StatusSeeOther {
		t.Fatalf("ban author: %d %s", rec.Code, rec.Body.String())
	}
	if got, _, _ := app.AccountByID(m.ID); got.Status != appstore.StatusBanned {
		t.Errorf("author status = %q, want banned", got.Status)
	}

	// A moderator without the can_ban grant can't use it.
	ckNoBan := seedModeratorSession(t, app)
	m2, _ := app.CreateAccount(appstore.RoleMember, "")
	cm2, _ := app.AddComment(m2.ID, "/p", "", "x")
	if rec := banAuthorForm(t, h, cm2.ID, ckNoBan); rec.Code != http.StatusForbidden {
		t.Errorf("no can_ban: status %d, want 403", rec.Code)
	}

	// A staff comment's author is not bannable even with the grant.
	cr, _ := app.CreateAccount(appstore.RoleCreator, "")
	crc, _ := app.AddComment(cr.ID, "/p", "Author", "owner note")
	if rec := banAuthorForm(t, h, crc.ID, ck); rec.Code != http.StatusForbidden {
		t.Errorf("ban staff author: status %d, want 403", rec.Code)
	}
	if got, _, _ := app.AccountByID(cr.ID); got.Status == appstore.StatusBanned {
		t.Error("creator was banned via ban-author")
	}
}

// TestModAccountsShowAlias: the moderator accounts tab shows each member's display name.
func TestModAccountsShowAlias(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, _ := seedBannerSession(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	app.SetAccountAlias(m.ID, "VisibleName")

	if body := getWithCookie(t, h, "/_pbc/mod/accounts", ck).Body.String(); !strings.Contains(body, "VisibleName") {
		t.Error("moderator accounts tab should show the member alias")
	}
}
