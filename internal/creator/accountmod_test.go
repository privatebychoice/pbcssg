package creator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

func TestAccountModListExcludesCreator(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, creatorHandle, creatorID := seedCreator(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "lin-m")
	mod, _ := app.CreateAccount(appstore.RoleModerator, "lin-x")
	if _, err := app.AddComment(mem.ID, "/p", "raven", "hi"); err != nil {
		t.Fatal(err)
	}

	rec := authedGet(t, c, app, creatorID, "/admin/moderation/accounts")
	if rec.Code != http.StatusOK {
		t.Fatalf("accounts GET status %d", rec.Code)
	}
	body := rec.Body.String()
	// Member and moderator are listed (by short handle + id); their roles show.
	for _, want := range []string{"#" + itoa(mem.ID), "#" + itoa(mod.ID), "member", "moderator"} {
		if !strings.Contains(body, want) {
			t.Errorf("accounts list missing %q", want)
		}
	}
	// The comment count is surfaced.
	if !strings.Contains(body, ">1<") {
		t.Errorf("expected the member's comment count (1) in the list:\n%s", body)
	}
	// The creator account is not listed.
	if strings.Contains(body, "#"+itoa(creatorID)) || strings.Contains(body, handleShort(creatorHandle)) {
		t.Errorf("creator account must not appear in the moderation list:\n%s", body)
	}
}

func TestAccountBanAndUnban(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "lin-m")
	cm, _ := app.AddComment(mem.ID, "/p", "raven", "keep me")
	token, _, _ := app.CreateSession(mem.ID, time.Hour)

	// Ban without removing posts.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mem.ID)+"/ban", nil); rec.Code != http.StatusOK {
		t.Fatalf("ban status %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(mem.ID); got.Status != appstore.StatusBanned {
		t.Errorf("account status = %q, want banned", got.Status)
	}
	// The ban revokes sessions.
	if _, ok, _ := app.SessionByToken(token); ok {
		t.Error("ban should have revoked the member's session")
	}
	// Posts kept (no remove flag).
	if _, ok, _ := app.CommentByID(cm.ID); !ok {
		t.Error("comment should remain when 'remove posts' is not ticked")
	}

	// Un-ban restores active.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mem.ID)+"/unban", nil); rec.Code != http.StatusOK {
		t.Fatalf("unban status %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(mem.ID); got.Status != appstore.StatusActive {
		t.Errorf("account status = %q, want active after un-ban", got.Status)
	}
}

func TestAccountBanRemovesPosts(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "lin-m")
	cm, _ := app.AddComment(mem.ID, "/p", "raven", "delete me")

	rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mem.ID)+"/ban", url.Values{"remove": {"1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("ban+remove status %d", rec.Code)
	}
	if _, ok, _ := app.CommentByID(cm.ID); ok {
		t.Error("comment should be removed when 'remove posts' is ticked")
	}
}

func TestAccountEraseAnonymizeVsDelete(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)

	// Erase keeping (anonymizing) comments.
	keep, _ := app.CreateAccount(appstore.RoleMember, "lin-1")
	keepC, _ := app.AddComment(keep.ID, "/p", "raven", "anon me")
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(keep.ID)+"/erase", nil); rec.Code != http.StatusOK {
		t.Fatalf("erase(anon) status %d", rec.Code)
	}
	if _, ok, _ := app.AccountByID(keep.ID); ok {
		t.Error("account should be gone after erase")
	}
	got, ok, _ := app.CommentByID(keepC.ID)
	if !ok {
		t.Fatal("anonymized comment should still exist")
	}
	if got.AccountID != nil || got.Alias != "" || got.Body != "anon me" {
		t.Errorf("anonymized comment = %+v, want body kept, link/alias cleared", got)
	}

	// Erase deleting comments.
	del, _ := app.CreateAccount(appstore.RoleMember, "lin-2")
	delC, _ := app.AddComment(del.ID, "/p", "mallory", "delete me")
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(del.ID)+"/erase", url.Values{"delete": {"1"}}); rec.Code != http.StatusOK {
		t.Fatalf("erase(delete) status %d", rec.Code)
	}
	if _, ok, _ := app.CommentByID(delC.ID); ok {
		t.Error("comment should be deleted with the account when 'delete comments' is ticked")
	}
}

func TestAccountModRefusesCreator(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)

	// Ban and erase targeting the creator's own id are refused; it stays active.
	for _, action := range []string{"ban", "erase"} {
		rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(creatorID)+"/"+action, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s of a creator: status %d, want 403", action, rec.Code)
		}
	}
	if got, _, _ := app.AccountByID(creatorID); got.Status != appstore.StatusActive || got.Role != appstore.RoleCreator {
		t.Errorf("creator account changed: %+v", got)
	}
}

func TestAccountModRequiresCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "lin-m")

	token, _, _ := app.CreateSession(creatorID, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/admin/moderation/accounts/"+itoa(mem.ID)+"/ban", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF-less ban status %d, want 403", rec.Code)
	}
	if got, _, _ := app.AccountByID(mem.ID); got.Status != appstore.StatusActive {
		t.Errorf("account banned despite CSRF failure: %q", got.Status)
	}
}

func TestAccountCapabilitiesLabelRevoke(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	mod, _ := app.CreateAccount(appstore.RoleModerator, "")

	// Grant both capabilities.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mod.ID)+"/capabilities", url.Values{"can_invite": {"1"}, "can_ban": {"1"}}); rec.Code != http.StatusOK {
		t.Fatalf("capabilities: %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(mod.ID); !got.CanInvite || !got.CanBan {
		t.Errorf("capabilities not set: %+v", got)
	}
	// Clearing (no checkboxes) revokes them.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mod.ID)+"/capabilities", url.Values{}); rec.Code != http.StatusOK {
		t.Fatalf("clear capabilities: %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(mod.ID); got.CanInvite || got.CanBan {
		t.Errorf("capabilities not cleared: %+v", got)
	}

	// Set a label.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mod.ID)+"/label", url.Values{"label": {"Alice"}}); rec.Code != http.StatusOK {
		t.Fatalf("label: %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(mod.ID); got.Label != "Alice" {
		t.Errorf("label = %q, want Alice", got.Label)
	}

	// Revoke the moderator's outstanding invites.
	app.SetAccountCapabilities(mod.ID, true, false)
	app.MintMemberInviteByModerator(mod.ID)
	app.MintMemberInviteByModerator(mod.ID)
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mod.ID)+"/revoke-invites", url.Values{}); rec.Code != http.StatusOK {
		t.Fatalf("revoke-invites: %d", rec.Code)
	}
	if n, _ := app.CountOutstandingInvitesBy(mod.ID); n != 0 {
		t.Errorf("outstanding after revoke = %d, want 0", n)
	}
}

func TestAccountControlsModeratorOnly(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	// A capability toggle on a member account is refused.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/accounts/"+itoa(mem.ID)+"/capabilities", url.Values{"can_invite": {"1"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("capabilities on member: status %d, want 400", rec.Code)
	}
	if got, _, _ := app.AccountByID(mem.ID); got.CanInvite {
		t.Error("member account received a moderator capability")
	}
}

func TestAccountListShowsProvenanceAndModeratorControls(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	modCode, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Alice"})
	mod, _ := app.RedeemInvite(modCode)
	memCode, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, IssuedBy: mod.ID})
	app.RedeemInvite(memCode)

	body := authedGet(t, c, app, creatorID, "/admin/moderation/accounts").Body.String()
	if !strings.Contains(body, "invited by Alice") {
		t.Error("member row missing the invited-by provenance")
	}
	if !strings.Contains(body, "can_invite") || !strings.Contains(body, "/revoke-invites") {
		t.Error("moderator row missing capability / revoke-invites controls")
	}
	if !strings.Contains(body, `value="Alice"`) {
		t.Error("moderator label not pre-filled in the rename field")
	}
}
