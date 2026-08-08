package creator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/store"
)

var mintedCodeRE = regexp.MustCompile(`minted-code" value="([^"]+)"`)

func TestInviteMintShowsWorkingCodeOnce(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)

	rec := authedForm(t, c, creatorID, "/admin/invites", url.Values{"role": {appstore.RoleModerator}, "ttl": {"24h"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "New invite code") || !strings.Contains(body, "not be shown again") {
		t.Errorf("mint page missing the one-time code notice:\n%s", body)
	}
	m := mintedCodeRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no minted code shown:\n%s", body)
	}
	code := m[1]

	// The shown code is the real, redeemable code — redeeming it makes a moderator.
	acc, err := app.RedeemInvite(code)
	if err != nil {
		t.Fatalf("minted code did not redeem: %v", err)
	}
	if acc.Role != appstore.RoleModerator {
		t.Errorf("redeemed role = %q, want moderator", acc.Role)
	}

	// A later list view does not reprint the code.
	list := authedGet(t, c, app, creatorID, "/admin/invites")
	if strings.Contains(list.Body.String(), code) {
		t.Error("the invite code must not reappear after it was shown once")
	}
	// The page tells the creator where moderators sign in.
	if !strings.Contains(list.Body.String(), "/_pbc/moderate") {
		t.Error("invites page should show the moderator sign-in URL")
	}
}

func TestInviteMintRejectsBadRole(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app) // seeding mints+redeems one bootstrap invite
	before, _ := app.Invites()
	rec := authedForm(t, c, creatorID, "/admin/invites", url.Values{"role": {"bogus"}, "ttl": {"24h"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-role mint status %d, want 400", rec.Code)
	}
	if after, _ := app.Invites(); len(after) != len(before) {
		t.Errorf("a bad-role mint created %d new invite(s)", len(after)-len(before))
	}
}

func TestInviteListAndRevoke(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	_, live, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: time.Hour})
	code2, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})

	// The list shows both invites and their roles/status.
	rec := authedGet(t, c, app, creatorID, "/admin/invites")
	body := rec.Body.String()
	for _, want := range []string{"member", "creator", "active", "Revoke"} {
		if !strings.Contains(body, want) {
			t.Errorf("invite list missing %q", want)
		}
	}

	// Revoke the live member invite by its lineage.
	if rec := authedForm(t, c, creatorID, "/admin/invites/revoke", url.Values{"lineage": {live.Lineage}}); rec.Code != http.StatusOK {
		t.Fatalf("revoke status %d", rec.Code)
	}
	// The revoked invite's code no longer redeems; the other still does.
	invites, _ := app.Invites()
	var revoked bool
	for _, inv := range invites {
		if inv.Lineage == live.Lineage && !inv.RevokedAt.IsZero() {
			revoked = true
		}
	}
	if !revoked {
		t.Error("member invite not marked revoked")
	}
	if _, err := app.RedeemInvite(code2); err != nil {
		t.Errorf("the un-revoked creator invite should still redeem: %v", err)
	}
}

func TestInviteRevokeRequiresCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	_, live, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: time.Hour})

	token, _, _ := app.CreateSession(creatorID, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/admin/invites/revoke", strings.NewReader("lineage="+live.Lineage))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF-less revoke status %d, want 403", rec.Code)
	}
	// The invite is untouched, so it still redeems.
	invites, _ := app.Invites()
	if !invites[0].RevokedAt.IsZero() {
		t.Error("invite revoked despite CSRF failure")
	}
}

func TestInviteGateRequiresSession(t *testing.T) {
	c, _ := newAuthCreator(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/invites", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated invites GET status %d, want 303", rec.Code)
	}
}

func TestInviteDisabledWithoutStore(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := New(st, Config{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/invites", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invites GET (no store) status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "the runtime store") {
		t.Errorf("expected the disabled notice:\n%s", rec.Body.String())
	}
}
