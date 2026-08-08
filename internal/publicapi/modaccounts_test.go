package publicapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// seedBannerSession creates a moderator with the can_ban grant and a live session.
func seedBannerSession(t *testing.T, app *appstore.Store) (*http.Cookie, int64) {
	t.Helper()
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Banner"})
	acc, err := app.RedeemInviteAndRegister(code, "banner-handle", appstore.Credential{
		CredID: rawURL([]byte("banner-cred")), PublicKey: []byte("k"),
	}, appstore.RoleModerator)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetAccountCapabilities(acc.ID, false, true); err != nil {
		t.Fatal(err)
	}
	token, _, _ := app.CreateSession(acc.ID, time.Hour)
	return &http.Cookie{Name: modCookieDev, Value: token}, acc.ID
}

func TestModAccountBanUnban(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, _ := seedBannerSession(t, app)
	member, _ := app.CreateAccount(appstore.RoleMember, "")
	mToken, _, _ := app.CreateSession(member.ID, time.Hour)

	if body := getWithCookie(t, h, "/_pbc/mod/accounts", ck).Body.String(); !strings.Contains(body, ">Ban<") {
		t.Fatal("accounts page missing the ban control")
	}

	// Ban → flagged, sessions revoked.
	if rec := modFormPost(t, h, "/_pbc/mod/accounts/"+strconv.FormatInt(member.ID, 10)+"/ban", url.Values{}, ck); rec.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", rec.Code, rec.Body.String())
	}
	if got, _, _ := app.AccountByID(member.ID); got.Status != appstore.StatusBanned {
		t.Errorf("member not banned")
	}
	if _, ok, _ := app.SessionByToken(mToken); ok {
		t.Error("member session not revoked by ban")
	}

	// Un-ban → active again.
	if rec := modFormPost(t, h, "/_pbc/mod/accounts/"+strconv.FormatInt(member.ID, 10)+"/unban", url.Values{}, ck); rec.Code != http.StatusOK {
		t.Fatalf("unban: %d", rec.Code)
	}
	if got, _, _ := app.AccountByID(member.ID); got.Status != appstore.StatusActive {
		t.Errorf("member not un-banned")
	}
}

func TestModAccountRequiresCapability(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app) // a moderator WITHOUT can_ban
	member, _ := app.CreateAccount(appstore.RoleMember, "")

	if rec := getWithCookie(t, h, "/_pbc/mod/accounts", ck); rec.Code != http.StatusForbidden {
		t.Errorf("accounts page without grant: status %d, want 403", rec.Code)
	}
	if rec := modFormPost(t, h, "/_pbc/mod/accounts/"+strconv.FormatInt(member.ID, 10)+"/ban", url.Values{}, ck); rec.Code != http.StatusForbidden {
		t.Errorf("ban without grant: status %d, want 403", rec.Code)
	}
	if got, _, _ := app.AccountByID(member.ID); got.Status != appstore.StatusActive {
		t.Error("member banned despite no grant")
	}
}

func TestModAccountRefusesNonMember(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, _ := seedBannerSession(t, app)
	// A moderator must not be able to ban another moderator (or the creator).
	other, _ := app.CreateAccount(appstore.RoleModerator, "")
	rec := modFormPost(t, h, "/_pbc/mod/accounts/"+strconv.FormatInt(other.ID, 10)+"/ban", url.Values{}, ck)
	if rec.Code != http.StatusForbidden {
		t.Errorf("ban a moderator: status %d, want 403", rec.Code)
	}
	if got, _, _ := app.AccountByID(other.ID); got.Status != appstore.StatusActive {
		t.Error("a moderator was banned by another moderator")
	}
}
