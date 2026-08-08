package creator

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestCreatorBanAuthorFromComment: the creator bans a comment's author straight from the
// moderation table; the creator's own post and an anonymized comment are refused.
func TestCreatorBanAuthorFromComment(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)

	m, _ := app.CreateAccount(appstore.RoleMember, "")
	app.SetAccountAlias(m.ID, "Spammer")
	cm, _ := app.AddComment(m.ID, "/p", "Spammer", "spam")

	rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+strconv.FormatInt(cm.ID, 10)+"/ban-author", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("ban author: %d %s", rec.Code, rec.Body.String())
	}
	if got, _, _ := app.AccountByID(m.ID); got.Status != appstore.StatusBanned {
		t.Errorf("author status = %q, want banned", got.Status)
	}

	// The creator can't ban their own account from their own comment.
	own, _ := app.AddComment(creatorID, "/p", "", "owner note")
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+strconv.FormatInt(own.ID, 10)+"/ban-author", url.Values{}); rec.Code != http.StatusForbidden {
		t.Errorf("ban own comment: status %d, want 403", rec.Code)
	}

	// An anonymized comment (no author) can't be banned.
	m2, _ := app.CreateAccount(appstore.RoleMember, "")
	cm2, _ := app.AddComment(m2.ID, "/p", "x", "bye")
	app.ForgetAccount(m2.ID, true) // anonymize: account gone, comment kept with NULL author
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+strconv.FormatInt(cm2.ID, 10)+"/ban-author", url.Values{}); rec.Code != http.StatusBadRequest {
		t.Errorf("ban anonymized author: status %d, want 400", rec.Code)
	}
}

// TestCreatorAccountsShowAlias: the accounts tab shows each account's display name (de-blinding
// which opaque handle is which), and the moderation rows offer "Ban author".
func TestCreatorAccountsShowAlias(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	app.SetAccountAlias(m.ID, "VisibleName")
	app.AddComment(m.ID, "/p", "VisibleName", "hi")

	acctBody := authedGet(t, c, app, creatorID, "/admin/moderation/accounts").Body.String()
	if !strings.Contains(acctBody, "VisibleName") {
		t.Error("accounts tab should show the account alias")
	}
	modBody := authedGet(t, c, app, creatorID, "/admin/moderation").Body.String()
	if !strings.Contains(modBody, "Ban author") {
		t.Error("moderation row should offer Ban author for a member comment")
	}
}
