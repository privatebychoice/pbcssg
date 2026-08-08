package creator

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
	"go.privatebychoice.com/pbcssg/internal/store"
)

const testAdminOrigin = "http://localhost:8085" // http+localhost -> dev cookie, RP ID "localhost"

func newAuthCreator(t *testing.T) (*Creator, *appstore.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	app, err := appstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(st, Config{AppStore: app, AdminOrigin: testAdminOrigin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); app.Close() })
	return c, app
}

func postJSON(t *testing.T, c *Creator, path string, body any, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

// --- WebAuthn fixture builders (registration) --------------------------------

func rawURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func coseES256(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	pad := func(b []byte) []byte {
		out := make([]byte, 32)
		copy(out[32-len(b):], b)
		return out
	}
	// COSE_Key: kty=EC2(2), alg=ES256(-7), crv=P-256(1), x(-2), y(-3).
	m := map[int]any{1: 2, 3: -7, -1: 1, -2: pad(pub.X.Bytes()), -3: pad(pub.Y.Bytes())}
	b, err := cbor.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func regAuthData(t *testing.T, rpID string, cose, credID []byte) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(rpID))
	b := append([]byte(nil), h[:]...)
	b = append(b, 0x45) // flags: UP(0x01)|UV(0x04)|AT(0x40)
	var sc [4]byte
	b = append(b, sc[:]...)            // signCount 0
	b = append(b, make([]byte, 16)...) // aaguid (zero, attestation:none)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(credID)))
	b = append(b, l[:]...)
	b = append(b, credID...)
	return append(b, cose...)
}

func attestationObject(t *testing.T, authData []byte) []byte {
	t.Helper()
	b, err := cbor.Marshal(map[string]any{"fmt": "none", "authData": authData, "attStmt": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func clientData(t *testing.T, typ, origin string, challenge []byte) []byte {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"type": typ, "challenge": rawURL(challenge), "origin": origin})
	return b
}

// buildRegistration produces a verify request body for a fresh ES256 credential
// answering the given options, optionally overriding the clientData origin.
func buildRegistration(t *testing.T, opts authflow.CreationOptions, origin string) map[string]any {
	t.Helper()
	challenge, err := base64.RawURLEncoding.DecodeString(opts.PublicKey.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cose := coseES256(t, &priv.PublicKey)
	ad := regAuthData(t, "localhost", cose, []byte("cred-abc"))
	cd := clientData(t, "webauthn.create", origin, challenge)
	return map[string]any{
		"id": opts.ID,
		"response": map[string]string{
			"clientDataJSON":    rawURL(cd),
			"attestationObject": rawURL(attestationObject(t, ad)),
		},
		"transports": []string{"usb"},
	}
}

func mustOptions(t *testing.T, c *Creator, invite string) authflow.CreationOptions {
	t.Helper()
	rec := postJSON(t, c, "/admin/auth/register/options", map[string]string{"invite": invite}, c.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("options: status %d, body %s", rec.Code, rec.Body.String())
	}
	var opts authflow.CreationOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	return opts
}

// --- tests --------------------------------------------------------------------

func TestRegisterFlowHappyPath(t *testing.T) {
	c, app := newAuthCreator(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})

	opts := mustOptions(t, c, code)
	if opts.ID == "" || opts.PublicKey.RP["id"] != "localhost" {
		t.Fatalf("unexpected options: %+v", opts)
	}

	rec := postJSON(t, c, "/admin/auth/register/verify", buildRegistration(t, opts, testAdminOrigin), c.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: status %d, body %s", rec.Code, rec.Body.String())
	}

	// A creator account now exists.
	if n, _ := app.CountAccountsByRole(appstore.RoleCreator); n != 1 {
		t.Errorf("creator accounts = %d, want 1", n)
	}
	// A session cookie was set and resolves to a live session.
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("no session cookie set")
	}
	if cookie.Name != sessionCookieDev || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie attrs = %+v", cookie)
	}
	if _, ok, _ := app.SessionByToken(cookie.Value); !ok {
		t.Error("session cookie does not resolve to a live session")
	}
}

func TestRegisterRejectsBadCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})
	rec := postJSON(t, c, "/admin/auth/register/options", map[string]string{"invite": code}, "wrong-token")
	if rec.Code != http.StatusForbidden {
		t.Errorf("bad csrf: status %d, want 403", rec.Code)
	}
}

func TestRegisterRejectsWrongOrigin(t *testing.T) {
	c, app := newAuthCreator(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})
	opts := mustOptions(t, c, code)
	// clientData claims a different origin than the verifier expects.
	rec := postJSON(t, c, "/admin/auth/register/verify", buildRegistration(t, opts, "https://evil.example"), c.csrf)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong origin: status %d, want 400", rec.Code)
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleCreator); n != 0 {
		t.Errorf("an account was created on a failed verify: %d", n)
	}
}

func TestRegisterCeremonyIsSingleUse(t *testing.T) {
	c, app := newAuthCreator(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})
	opts := mustOptions(t, c, code)
	body := buildRegistration(t, opts, testAdminOrigin)

	if rec := postJSON(t, c, "/admin/auth/register/verify", body, c.csrf); rec.Code != http.StatusOK {
		t.Fatalf("first verify: %d %s", rec.Code, rec.Body.String())
	}
	// Replaying the same ceremony id must fail (challenge consumed).
	if rec := postJSON(t, c, "/admin/auth/register/verify", body, c.csrf); rec.Code == http.StatusOK {
		t.Error("replayed ceremony id should not succeed")
	}
}

func TestRegisterRejectsUsedInvite(t *testing.T) {
	c, app := newAuthCreator(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})

	// First registration consumes the invite.
	opts := mustOptions(t, c, code)
	if rec := postJSON(t, c, "/admin/auth/register/verify", buildRegistration(t, opts, testAdminOrigin), c.csrf); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	// A second, fresh ceremony with the same (now-used) invite must be rejected at verify.
	opts2 := mustOptions(t, c, code)
	rec := postJSON(t, c, "/admin/auth/register/verify", buildRegistration(t, opts2, testAdminOrigin), c.csrf)
	if rec.Code != http.StatusConflict {
		t.Errorf("used invite: status %d, want 409", rec.Code)
	}
}

func TestAuthEndpointsAbsentWithoutStore(t *testing.T) {
	// A creator without an app store must not expose the ceremony endpoints.
	st, _ := store.Open(":memory:")
	defer st.Close()
	c, _ := New(st, Config{})
	rec := postJSON(t, c, "/admin/auth/register/options", map[string]string{"invite": "x"}, c.csrf)
	// The ceremony route is not registered when auth is off, so it never reaches the
	// handler: the mux returns 404 (no match) or 405 (matched only the "GET /" subtree
	// with the wrong method). Either way it must not be served (not 200).
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 404/405 when auth is off", rec.Code)
	}
}

func TestRegisterPageAndAssetServe(t *testing.T) {
	c, _ := newAuthCreator(t)

	// The register page renders with the invite form and loads the ceremony script.
	req := httptest.NewRequest(http.MethodGet, "/admin/register", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register page: status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="invite"`, `/admin/assets/register.js`, `id="csrf"`} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("register page missing %q", want)
		}
	}

	// The register.js asset serves as JavaScript.
	areq := httptest.NewRequest(http.MethodGet, "/admin/assets/register.js", nil)
	arec := httptest.NewRecorder()
	c.ServeHTTP(arec, areq)
	if arec.Code != http.StatusOK {
		t.Fatalf("register.js: status %d", arec.Code)
	}
	if ct := arec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("register.js content-type = %q", ct)
	}
	if !bytes.Contains(arec.Body.Bytes(), []byte("navigator.credentials.create")) {
		t.Error("register.js does not drive the create ceremony")
	}
}

// --- login (assertion) fixtures & tests --------------------------------------

// seedCreator registers a creator account with a known ES256 key directly in the
// store, returning the signing key, the base64url credential id, the account handle,
// and the account id.
func seedCreator(t *testing.T, app *appstore.Store) (priv *ecdsa.PrivateKey, credIDB64, handle string, accID int64) {
	t.Helper()
	priv, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cose := coseES256(t, &priv.PublicKey)
	credID := []byte("login-cred-1")
	handle = "creator-handle-1"
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})
	acc, err := app.RedeemInviteAndRegister(code, handle, appstore.Credential{
		CredID: rawURL(credID), PublicKey: cose, SignCount: 0,
	}, appstore.RoleCreator)
	if err != nil {
		t.Fatalf("seed creator: %v", err)
	}
	return priv, rawURL(credID), handle, acc.ID
}

func mustLoginOptions(t *testing.T, c *Creator) authflow.RequestOptions {
	t.Helper()
	rec := postJSON(t, c, "/admin/auth/login/options", map[string]any{}, c.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("login options: status %d, body %s", rec.Code, rec.Body.String())
	}
	var opts authflow.RequestOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatalf("decode login options: %v", err)
	}
	return opts
}

func loginAuthData(t *testing.T, rpID string, flags byte, signCount uint32) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(rpID))
	b := append([]byte(nil), h[:]...)
	b = append(b, flags)
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], signCount)
	return append(b, sc[:]...)
}

// buildAssertion produces a login/verify body signed by priv, answering opts.
func buildAssertion(t *testing.T, priv *ecdsa.PrivateKey, credIDB64, handle, origin string, opts authflow.RequestOptions, signCount uint32) map[string]any {
	t.Helper()
	challenge, err := base64.RawURLEncoding.DecodeString(opts.PublicKey.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	authData := loginAuthData(t, "localhost", 0x05, signCount) // UP|UV
	cd := clientData(t, "webauthn.get", origin, challenge)
	cdHash := sha256.Sum256(cd)
	signed := append(append([]byte(nil), authData...), cdHash[:]...)
	h := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id":           opts.ID,
		"credentialId": credIDB64,
		"userHandle":   handle,
		"response": map[string]string{
			"clientDataJSON":    rawURL(cd),
			"authenticatorData": rawURL(authData),
			"signature":         rawURL(sig),
		},
	}
}

func TestLoginFlowHappyPath(t *testing.T) {
	c, app := newAuthCreator(t)
	priv, credID, handle, _ := seedCreator(t, app)

	opts := mustLoginOptions(t, c)
	if opts.PublicKey.RPID != "localhost" {
		t.Fatalf("login options rpId = %q", opts.PublicKey.RPID)
	}
	rec := postJSON(t, c, "/admin/auth/login/verify", buildAssertion(t, priv, credID, handle, testAdminOrigin, opts, 1), c.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("login verify: status %d, body %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)
	if cookie == nil {
		t.Fatal("no session cookie on login")
	}
	if _, ok, _ := app.SessionByToken(cookie.Value); !ok {
		t.Error("login session cookie does not resolve")
	}
}

func TestLoginRejectsBadSignature(t *testing.T) {
	c, app := newAuthCreator(t)
	priv, credID, handle, _ := seedCreator(t, app)
	opts := mustLoginOptions(t, c)
	body := buildAssertion(t, priv, credID, handle, testAdminOrigin, opts, 1)
	// Corrupt the signature.
	resp := body["response"].(map[string]string)
	resp["signature"] = rawURL([]byte("not-a-valid-signature"))
	rec := postJSON(t, c, "/admin/auth/login/verify", body, c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: status %d, want 401", rec.Code)
	}
}

func TestLoginRejectsUnknownCredential(t *testing.T) {
	c, app := newAuthCreator(t)
	priv, _, handle, _ := seedCreator(t, app)
	opts := mustLoginOptions(t, c)
	// A credential id that was never registered.
	body := buildAssertion(t, priv, rawURL([]byte("ghost-cred")), handle, testAdminOrigin, opts, 1)
	rec := postJSON(t, c, "/admin/auth/login/verify", body, c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown credential: status %d, want 401", rec.Code)
	}
}

func TestLoginRejectsBannedAccount(t *testing.T) {
	c, app := newAuthCreator(t)
	priv, credID, handle, accID := seedCreator(t, app)
	if err := app.SetAccountStatus(accID, appstore.StatusBanned); err != nil {
		t.Fatal(err)
	}
	opts := mustLoginOptions(t, c)
	rec := postJSON(t, c, "/admin/auth/login/verify", buildAssertion(t, priv, credID, handle, testAdminOrigin, opts, 1), c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("banned account: status %d, want 401", rec.Code)
	}
}

func TestLoginRejectsWrongOrigin(t *testing.T) {
	c, app := newAuthCreator(t)
	priv, credID, handle, _ := seedCreator(t, app)
	opts := mustLoginOptions(t, c)
	rec := postJSON(t, c, "/admin/auth/login/verify", buildAssertion(t, priv, credID, handle, "https://evil.example", opts, 1), c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong origin: status %d, want 401", rec.Code)
	}
}

func TestLoginPageAndAssetServe(t *testing.T) {
	c, _ := newAuthCreator(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("/admin/assets/login.js")) {
		t.Fatalf("login page: status %d", rec.Code)
	}
	arec := httptest.NewRecorder()
	c.ServeHTTP(arec, httptest.NewRequest(http.MethodGet, "/admin/assets/login.js", nil))
	if arec.Code != http.StatusOK || !bytes.Contains(arec.Body.Bytes(), []byte("navigator.credentials.get")) {
		t.Errorf("login.js not served correctly: status %d", arec.Code)
	}
}

// --- CE4: session gate & logout ----------------------------------------------

// authedGet issues a GET carrying a valid session cookie for accID.
func authedGet(t *testing.T, c *Creator, app *appstore.Store, accID int64, path string) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := app.CreateSession(accID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

func TestGateRedirectsUnauthenticatedGET(t *testing.T) {
	c, _ := newAuthCreator(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated GET /: status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("redirect to %q, want /admin/login", loc)
	}
}

func TestGateRefusesUnauthenticatedPOST(t *testing.T) {
	c, _ := newAuthCreator(t)
	rec := postJSON(t, c, "/build", map[string]string{}, c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated POST /build: status %d, want 401", rec.Code)
	}
}

func TestGatePublicPathsOpen(t *testing.T) {
	c, _ := newAuthCreator(t)
	for _, p := range []string{"/admin/login", "/admin/register", "/admin/assets/admin.css"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("public path %s: status %d, want 200", p, rec.Code)
		}
	}
}

func TestGateAllowsAuthenticated(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	rec := authedGet(t, c, app, accID, "/")
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated GET /: status %d, want 200", rec.Code)
	}
	// The nav renders a Sign out control when auth is on.
	if !bytes.Contains(rec.Body.Bytes(), []byte("/admin/logout")) {
		t.Error("dashboard missing Sign out control")
	}
}

func TestGateRejectsBannedSession(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)
	if err := app.SetAccountStatus(accID, appstore.StatusBanned); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("banned session: status %d, want 303 redirect", rec.Code)
	}
}

func TestGateOffWithoutStore(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	c, _ := New(st, Config{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("standalone creator GET /: status %d, want 200 (no gate)", rec.Code)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)

	// POST /admin/logout with the cookie + CSRF form field.
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", bytes.NewReader([]byte("csrf="+c.csrf)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: status %d, want 303", rec.Code)
	}
	// The session is revoked server-side...
	if _, ok, _ := app.SessionByToken(token); ok {
		t.Error("session still resolves after logout")
	}
	// ...and the cookie is expired.
	if ck := sessionCookie(t, rec); ck == nil || ck.MaxAge >= 0 {
		t.Errorf("logout did not expire the cookie: %+v", ck)
	}
}

func TestLogoutRejectsBadCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", bytes.NewReader([]byte("csrf=wrong")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("logout bad csrf: status %d, want 403", rec.Code)
	}
	if _, ok, _ := app.SessionByToken(token); !ok {
		t.Error("session revoked despite bad csrf")
	}
}

// sessionCookie returns the session cookie set on the response, if any.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookieDev || ck.Name == sessionCookieHost {
			return ck
		}
	}
	return nil
}
