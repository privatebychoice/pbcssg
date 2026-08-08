package authflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

func TestHelpers(t *testing.T) {
	// B64/Unb64 round-trip.
	in := []byte{0x00, 0x01, 0xfe, 0xff, 0x10}
	if got, err := Unb64(B64(in)); err != nil || string(got) != string(in) {
		t.Errorf("B64/Unb64 round-trip = %v (err %v)", got, err)
	}
	// AAGUIDString: all-zero → empty (attestation:none), non-zero → hex.
	if s := AAGUIDString(make([]byte, 16)); s != "" {
		t.Errorf("all-zero AAGUID = %q, want empty", s)
	}
	if s := AAGUIDString([]byte{0xab, 0xcd}); s != "abcd" {
		t.Errorf("AAGUID hex = %q, want abcd", s)
	}
	// SanitizeTransports keeps only short alphabetic hints.
	if s := SanitizeTransports([]string{"USB", " nfc ", "bad-1", "waytoolongtransport", ""}); s != "usb,nfc" {
		t.Errorf("SanitizeTransports = %q, want usb,nfc", s)
	}
	// NewHandle is non-empty and unique enough.
	h1, _ := NewHandle()
	h2, _ := NewHandle()
	if h1 == "" || h1 == h2 {
		t.Errorf("NewHandle not fresh: %q / %q", h1, h2)
	}
}

func newFlow(t *testing.T, role string) (*Flow, *appstore.Store) {
	t.Helper()
	app, err := appstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	flow := New(Config{
		Store:        app,
		Verifier:     NewVerifier("localhost", "http://localhost:9000"),
		Role:         role,
		CookieName:   "pbcssg_test",
		CookieSecure: false,
		SessionTTL:   time.Hour,
		ChallengeTTL: time.Minute,
		LogPrefix:    "test",
	})
	return flow, app
}

func TestWriteRegisterOptionsTrimsInvite(t *testing.T) {
	flow, _ := newFlow(t, appstore.RoleMember)

	// A code arriving with surrounding whitespace (e.g. a copy-paste newline) must be
	// stored trimmed, so redeem-by-hash at verify time matches the minted invite
	// instead of failing as the generic "invite is not valid".
	req := httptest.NewRequest(http.MethodPost, "/register/options", strings.NewReader("{\"invite\":\"  abc123  \\n\"}"))
	rec := httptest.NewRecorder()
	flow.WriteRegisterOptions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	var opt CreationOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &opt); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	_, ctxAny, ok := flow.Consume(opt.ID)
	if !ok {
		t.Fatal("challenge was not stored")
	}
	ctx, ok := ctxAny.(regCeremony)
	if !ok {
		t.Fatalf("ceremony ctx type = %T, want regCeremony", ctxAny)
	}
	if ctx.invite != "abc123" {
		t.Errorf("stored invite = %q, want trimmed %q", ctx.invite, "abc123")
	}

	// A whitespace-only invite is still rejected as empty (400).
	rec2 := httptest.NewRecorder()
	flow.WriteRegisterOptions(rec2, httptest.NewRequest(http.MethodPost, "/register/options", strings.NewReader(`{"invite":"   "}`)))
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("whitespace-only invite status = %d, want 400", rec2.Code)
	}
}

func TestSessionCookieAndResolve(t *testing.T) {
	flow, app := newFlow(t, appstore.RoleMember)
	acc, _ := app.CreateAccount(appstore.RoleMember, "")
	token, _, _ := app.CreateSession(acc.ID, time.Hour)

	// SetCookie writes a hardened cookie.
	rec := httptest.NewRecorder()
	flow.SetCookie(rec, token)
	ck := rec.Result().Cookies()[0]
	if ck.Name != "pbcssg_test" || ck.Value != token || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", ck)
	}

	// Resolve returns the account for a matching-role flow.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(ck)
	if got, ok := flow.Resolve(req); !ok || got.ID != acc.ID {
		t.Errorf("Resolve = (%d,%v), want the account", got.ID, ok)
	}

	// A wrong-role flow over the same store/cookie rejects it (per-origin split).
	creatorFlow := New(Config{Store: app, Role: appstore.RoleCreator, CookieName: "pbcssg_test", SessionTTL: time.Hour, ChallengeTTL: time.Minute})
	if _, ok := creatorFlow.Resolve(req); ok {
		t.Error("a member session must not resolve on a creator-role flow")
	}

	// Banned account no longer resolves.
	if err := app.SetAccountStatus(acc.ID, appstore.StatusBanned); err != nil {
		t.Fatal(err)
	}
	if _, ok := flow.Resolve(req); ok {
		t.Error("a banned account must not resolve")
	}
}

func TestRevokeAndClear(t *testing.T) {
	flow, app := newFlow(t, appstore.RoleMember)
	acc, _ := app.CreateAccount(appstore.RoleMember, "")
	token, _, _ := app.CreateSession(acc.ID, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "pbcssg_test", Value: token})

	flow.RevokeCurrent(req)
	if _, ok, _ := app.SessionByToken(token); ok {
		t.Error("RevokeCurrent should have deleted the session")
	}
	rec := httptest.NewRecorder()
	flow.ClearCookie(rec)
	if ck := rec.Result().Cookies()[0]; ck.MaxAge >= 0 {
		t.Errorf("ClearCookie should expire the cookie, got MaxAge=%d", ck.MaxAge)
	}
}
