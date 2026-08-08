package publicapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// seedInviterSession creates a moderator with the can_invite grant and a live session.
func seedInviterSession(t *testing.T, app *appstore.Store) (*http.Cookie, int64) {
	t.Helper()
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Inviter"})
	acc, err := app.RedeemInviteAndRegister(code, "inviter-handle", appstore.Credential{
		CredID: rawURL([]byte("inviter-cred")), PublicKey: []byte("k"),
	}, appstore.RoleModerator)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetAccountCapabilities(acc.ID, true, false); err != nil {
		t.Fatal(err)
	}
	token, _, _ := app.CreateSession(acc.ID, time.Hour)
	return &http.Cookie{Name: modCookieDev, Value: token}, acc.ID
}

func getWithCookie(t *testing.T, h http.Handler, path string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestModInviteMintAndList(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, modID := seedInviterSession(t, app)

	if body := getWithCookie(t, h, "/_pbc/mod/invites", ck).Body.String(); !strings.Contains(body, "Generate a member invite") {
		t.Fatal("invites page missing the mint control")
	}

	// Mint one; the page shows the code once and the store records it.
	rec := modFormPost(t, h, "/_pbc/mod/invites/mint", url.Values{}, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mod-codebox") {
		t.Error("mint response did not show the code once")
	}
	invs, _ := app.InvitesIssuedBy(modID)
	if len(invs) != 1 {
		t.Fatalf("issued invites = %d, want 1", len(invs))
	}
	inv := invs[0]
	if inv.Role != appstore.RoleMember {
		t.Errorf("minted role = %q, want member", inv.Role)
	}
	if inv.IssuedBy == nil || *inv.IssuedBy != modID {
		t.Errorf("issued_by not set to the moderator")
	}
	if inv.ExpiresAt.IsZero() {
		t.Error("moderator invite must have a 30-day expiry, got none")
	}
}

func TestModInviteRequiresCapability(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app) // a moderator WITHOUT can_invite

	if rec := getWithCookie(t, h, "/_pbc/mod/invites", ck); rec.Code != http.StatusForbidden {
		t.Errorf("invites page without grant: status %d, want 403", rec.Code)
	}
	if rec := modFormPost(t, h, "/_pbc/mod/invites/mint", url.Values{}, ck); rec.Code != http.StatusForbidden {
		t.Errorf("mint without grant: status %d, want 403", rec.Code)
	}
	if n, _ := app.CountComments(appstore.CommentQuery{}); n != 0 { // sanity: nothing else changed
		t.Errorf("unexpected comment writes: %d", n)
	}
}

func TestModInviteRevokeOwn(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, modID := seedInviterSession(t, app)
	if rec := modFormPost(t, h, "/_pbc/mod/invites/mint", url.Values{}, ck); rec.Code != http.StatusOK {
		t.Fatalf("mint: %d", rec.Code)
	}
	invs, _ := app.InvitesIssuedBy(modID)
	if len(invs) != 1 {
		t.Fatalf("issued = %d, want 1", len(invs))
	}
	rec := modFormPost(t, h, "/_pbc/mod/invites/revoke", url.Values{"lineage": {invs[0].Lineage}}, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if n, _ := app.CountOutstandingInvitesBy(modID); n != 0 {
		t.Errorf("outstanding after revoke = %d, want 0", n)
	}
}

func TestModInviteCap(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, modID := seedInviterSession(t, app)
	for i := 0; i < appstore.ModeratorOutstandingInviteCap; i++ {
		if rec := modFormPost(t, h, "/_pbc/mod/invites/mint", url.Values{}, ck); rec.Code != http.StatusOK {
			t.Fatalf("mint %d: %d", i, rec.Code)
		}
	}
	// The (cap+1)th mint is refused with the friendly limit message.
	rec := modFormPost(t, h, "/_pbc/mod/invites/mint", url.Values{}, ck)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("over-cap mint: status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "reached your limit") {
		t.Error("over-cap mint missing the limit message")
	}
	if n, _ := app.CountOutstandingInvitesBy(modID); n != appstore.ModeratorOutstandingInviteCap {
		t.Errorf("outstanding = %d, want the cap %d", n, appstore.ModeratorOutstandingInviteCap)
	}
}
