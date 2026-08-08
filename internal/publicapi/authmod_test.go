package publicapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// modRegisterOptions runs the moderator register-options step and returns the ceremony
// id + challenge (mirrors registerOptions, but on the /_pbc/mod/ path).
func modRegisterOptions(t *testing.T, h http.Handler, invite string) (id string, challenge []byte) {
	t.Helper()
	rec := memberPost(t, h, "/_pbc/mod/auth/register/options", map[string]string{"invite": invite}, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("mod register options: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID        string `json:"id"`
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	ch, _ := base64.RawURLEncoding.DecodeString(out.PublicKey.Challenge)
	return out.ID, ch
}

func TestModeratorRegisterHappyPath(t *testing.T) {
	h, app := newMemberAPI(t)
	// A moderator invite the creator would mint, labeled so the account is identifiable.
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Alice"})

	id, challenge := modRegisterOptions(t, h, code)
	body, _, _ := buildRegVerify(t, id, testMemberOrigin, challenge)
	rec := memberPost(t, h, "/_pbc/mod/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("mod register verify: %d %s", rec.Code, rec.Body.String())
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleModerator); n != 1 {
		t.Errorf("moderator accounts = %d, want 1", n)
	}
	// The moderator gets its own cookie, distinct from the member one.
	if findCookie(rec, modCookieDev) == nil {
		t.Fatal("no moderator session cookie")
	}
	if findCookie(rec, memberCookieDev) != nil {
		t.Error("moderator registration must not set a member cookie")
	}
}

func TestModeratorRegisterRejectsMemberInvite(t *testing.T) {
	h, app := newMemberAPI(t)
	// A member invite must not create a moderator on the moderator endpoints.
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: 0})
	id, challenge := modRegisterOptions(t, h, code)
	body, _, _ := buildRegVerify(t, id, testMemberOrigin, challenge)
	rec := memberPost(t, h, "/_pbc/mod/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusConflict {
		t.Errorf("member invite on moderator origin: status %d, want 409", rec.Code)
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleModerator); n != 0 {
		t.Errorf("moderator account created from a member invite: %d", n)
	}
}

func TestMemberRegisterRejectsModeratorInvite(t *testing.T) {
	h, app := newMemberAPI(t)
	// The reverse: a moderator invite must not create a member on the member endpoints.
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, TTL: 0})
	id, challenge := registerOptions(t, h, code)
	body, _, _ := buildRegVerify(t, id, testMemberOrigin, challenge)
	rec := memberPost(t, h, "/_pbc/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusConflict {
		t.Errorf("moderator invite on member origin: status %d, want 409", rec.Code)
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleModerator); n != 0 {
		t.Errorf("moderator account created on member origin: %d", n)
	}
}

func TestModeratorMeReportsCapabilities(t *testing.T) {
	h, app := newMemberAPI(t)
	// Seed a moderator account (label from the invite) and grant it one elevated power.
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, Label: "Alice"})
	acc, err := app.RedeemInviteAndRegister(code, "mod-handle-1", appstore.Credential{
		CredID: rawURL([]byte("mod-me-cred")), PublicKey: []byte("k"),
	}, appstore.RoleModerator)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Label != "Alice" {
		t.Fatalf("moderator label not seeded from invite: %q", acc.Label)
	}
	if err := app.SetAccountCapabilities(acc.ID, true, false); err != nil {
		t.Fatal(err)
	}
	token, _, _ := app.CreateSession(acc.ID, time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/_pbc/mod/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: modCookieDev, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var me struct {
		Authenticated bool   `json:"authenticated"`
		Label         string `json:"label"`
		CanInvite     bool   `json:"canInvite"`
		CanBan        bool   `json:"canBan"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if !me.Authenticated {
		t.Fatalf("me not authenticated: %s", rec.Body.String())
	}
	if me.Label != "Alice" {
		t.Errorf("label = %q, want Alice", me.Label)
	}
	if !me.CanInvite || me.CanBan {
		t.Errorf("capabilities = (%v,%v), want (true,false)", me.CanInvite, me.CanBan)
	}
}

func TestModeratorBadOrigin(t *testing.T) {
	h, app := newMemberAPI(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleModerator, TTL: 0})
	if rec := memberPost(t, h, "/_pbc/mod/auth/register/options", map[string]string{"invite": code}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("missing origin: status %d, want 403", rec.Code)
	}
	if rec := memberPost(t, h, "/_pbc/mod/auth/register/options", map[string]string{"invite": code}, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("wrong origin: status %d, want 403", rec.Code)
	}
}

// TestModeratorLoginRejectsMemberCredential proves the role-gated login on the shared
// public RP ID: a member's passkey cannot open a moderator session.
func TestModeratorLoginRejectsMemberCredential(t *testing.T) {
	h, app := newMemberAPI(t)
	priv, credID, handle, _ := seedMember(t, app) // a member credential on the public RP ID

	rec := memberPost(t, h, "/_pbc/mod/auth/login/options", map[string]any{}, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("mod login options: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID        string `json:"id"`
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	ch, _ := base64.RawURLEncoding.DecodeString(out.PublicKey.Challenge)

	body := buildLoginVerify(t, priv, out.ID, credID, handle, testMemberOrigin, ch, 1)
	rec = memberPost(t, h, "/_pbc/mod/auth/login/verify", body, testMemberOrigin)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("member credential on moderator login: status %d, want 401", rec.Code)
	}
}
