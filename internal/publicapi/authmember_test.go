package publicapi

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
)

const testMemberOrigin = "http://localhost:9000" // http+localhost -> dev cookie, RP ID "localhost"

func newMemberAPI(t *testing.T) (http.Handler, *appstore.Store) {
	t.Helper()
	app, err := appstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	h, err := New(app, Options{MemberOrigin: testMemberOrigin})
	if err != nil {
		t.Fatal(err)
	}
	return h, app
}

func memberPost(t *testing.T, h http.Handler, path string, body any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- fixtures (ES256) ---------------------------------------------------------

func rawURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func coseES256(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	pad := func(b []byte) []byte { out := make([]byte, 32); copy(out[32-len(b):], b); return out }
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
	b = append(b, 0x45)                // UP|UV|AT
	b = append(b, make([]byte, 4)...)  // signCount 0
	b = append(b, make([]byte, 16)...) // aaguid
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(credID)))
	b = append(b, l[:]...)
	b = append(b, credID...)
	return append(b, cose...)
}

func attObj(t *testing.T, authData []byte) []byte {
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

// registerOptions runs the options step and returns the ceremony id + challenge.
func registerOptions(t *testing.T, h http.Handler, invite string) (id string, challenge []byte) {
	t.Helper()
	rec := memberPost(t, h, "/_pbc/auth/register/options", map[string]string{"invite": invite}, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("register options: %d %s", rec.Code, rec.Body.String())
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

func buildRegVerify(t *testing.T, id, origin string, challenge []byte) (map[string]any, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	credID := []byte("member-cred")
	cose := coseES256(t, &priv.PublicKey)
	ad := regAuthData(t, "localhost", cose, credID)
	cd := clientData(t, "webauthn.create", origin, challenge)
	body := map[string]any{
		"id": id,
		"response": map[string]string{
			"clientDataJSON":    rawURL(cd),
			"attestationObject": rawURL(attObj(t, ad)),
		},
		"transports": []string{"internal"},
	}
	return body, priv, credID
}

// --- registration tests -------------------------------------------------------

func TestMemberRegisterHappyPath(t *testing.T) {
	h, app := newMemberAPI(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: 0})

	id, challenge := registerOptions(t, h, code)
	body, _, _ := buildRegVerify(t, id, testMemberOrigin, challenge)
	rec := memberPost(t, h, "/_pbc/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("register verify: %d %s", rec.Code, rec.Body.String())
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleMember); n != 1 {
		t.Errorf("member accounts = %d, want 1", n)
	}
	ck := findCookie(rec, memberCookieDev)
	if ck == nil {
		t.Fatal("no member session cookie")
	}
	if _, ok, _ := app.SessionByToken(ck.Value); !ok {
		t.Error("member session cookie does not resolve")
	}
}

func TestMemberRegisterRejectsCreatorInvite(t *testing.T) {
	h, app := newMemberAPI(t)
	// A creator invite must not be redeemable on the public/member origin.
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: 0})
	id, challenge := registerOptions(t, h, code)
	body, _, _ := buildRegVerify(t, id, testMemberOrigin, challenge)
	rec := memberPost(t, h, "/_pbc/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusConflict {
		t.Errorf("creator invite on member origin: status %d, want 409", rec.Code)
	}
	if n, _ := app.CountAccountsByRole(appstore.RoleCreator); n != 0 {
		t.Errorf("creator account created on member origin: %d", n)
	}
}

func TestMemberRegisterBadOrigin(t *testing.T) {
	h, app := newMemberAPI(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: 0})
	// No Origin header -> CSRF rejected.
	if rec := memberPost(t, h, "/_pbc/auth/register/options", map[string]string{"invite": code}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("missing origin: status %d, want 403", rec.Code)
	}
	// Wrong Origin -> rejected.
	if rec := memberPost(t, h, "/_pbc/auth/register/options", map[string]string{"invite": code}, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("wrong origin: status %d, want 403", rec.Code)
	}
}

func TestMemberRegisterWrongCeremonyOrigin(t *testing.T) {
	h, app := newMemberAPI(t)
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: 0})
	id, challenge := registerOptions(t, h, code)
	// clientData claims a different origin than the verifier expects.
	body, _, _ := buildRegVerify(t, id, "https://evil.example", challenge)
	rec := memberPost(t, h, "/_pbc/auth/register/verify", body, testMemberOrigin)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong ceremony origin: status %d, want 400", rec.Code)
	}
}

// --- login tests --------------------------------------------------------------

// seedMember registers a member with a known key directly; returns key, cred id, handle, id.
func seedMember(t *testing.T, app *appstore.Store) (*ecdsa.PrivateKey, string, string, int64) {
	t.Helper()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cose := coseES256(t, &priv.PublicKey)
	credID := []byte("member-login-cred")
	handle := "member-handle-1"
	code, _, _ := app.MintInvite(appstore.MintParams{Role: appstore.RoleMember, TTL: 0})
	acc, err := app.RedeemInviteAndRegister(code, handle, appstore.Credential{
		CredID: rawURL(credID), PublicKey: cose, SignCount: 0,
	}, appstore.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	return priv, rawURL(credID), handle, acc.ID
}

func loginOptions(t *testing.T, h http.Handler) (id string, challenge []byte) {
	t.Helper()
	rec := memberPost(t, h, "/_pbc/auth/login/options", map[string]any{}, testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("login options: %d %s", rec.Code, rec.Body.String())
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

func buildLoginVerify(t *testing.T, priv *ecdsa.PrivateKey, id, credID, handle, origin string, challenge []byte, signCount uint32) map[string]any {
	t.Helper()
	h := sha256.Sum256([]byte("localhost"))
	authData := append([]byte(nil), h[:]...)
	authData = append(authData, 0x05) // UP|UV
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], signCount)
	authData = append(authData, sc[:]...)
	cd := clientData(t, "webauthn.get", origin, challenge)
	cdHash := sha256.Sum256(cd)
	signed := append(append([]byte(nil), authData...), cdHash[:]...)
	dig := sha256.Sum256(signed)
	sig, _ := ecdsa.SignASN1(rand.Reader, priv, dig[:])
	return map[string]any{
		"id":           id,
		"credentialId": credID,
		"userHandle":   handle,
		"response": map[string]string{
			"clientDataJSON":    rawURL(cd),
			"authenticatorData": rawURL(authData),
			"signature":         rawURL(sig),
		},
	}
}

func TestMemberLoginHappyPath(t *testing.T) {
	h, app := newMemberAPI(t)
	priv, credID, handle, _ := seedMember(t, app)
	id, challenge := loginOptions(t, h)
	rec := memberPost(t, h, "/_pbc/auth/login/verify", buildLoginVerify(t, priv, id, credID, handle, testMemberOrigin, challenge, 1), testMemberOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("login verify: %d %s", rec.Code, rec.Body.String())
	}
	ck := findCookie(rec, memberCookieDev)
	if ck == nil {
		t.Fatal("no member cookie on login")
	}
	if _, ok, _ := app.SessionByToken(ck.Value); !ok {
		t.Error("login session does not resolve")
	}
}

func TestMemberLoginRejectsBanned(t *testing.T) {
	h, app := newMemberAPI(t)
	priv, credID, handle, accID := seedMember(t, app)
	if err := app.SetAccountStatus(accID, appstore.StatusBanned); err != nil {
		t.Fatal(err)
	}
	id, challenge := loginOptions(t, h)
	rec := memberPost(t, h, "/_pbc/auth/login/verify", buildLoginVerify(t, priv, id, credID, handle, testMemberOrigin, challenge, 1), testMemberOrigin)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("banned member login: status %d, want 401", rec.Code)
	}
}

func TestMemberLoginBadSignature(t *testing.T) {
	h, app := newMemberAPI(t)
	priv, credID, handle, _ := seedMember(t, app)
	id, challenge := loginOptions(t, h)
	body := buildLoginVerify(t, priv, id, credID, handle, testMemberOrigin, challenge, 1)
	resp := body["response"].(map[string]string)
	resp["signature"] = rawURL([]byte("garbage"))
	rec := memberPost(t, h, "/_pbc/auth/login/verify", body, testMemberOrigin)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: status %d, want 401", rec.Code)
	}
}

// --- logout / me / disabled ---------------------------------------------------

func TestMemberLogoutAndMe(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)

	// /me reflects the session.
	meReq := httptest.NewRequest(http.MethodGet, "/_pbc/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: memberCookieDev, Value: token})
	meRec := httptest.NewRecorder()
	h.ServeHTTP(meRec, meReq)
	var me struct {
		Authenticated bool `json:"authenticated"`
	}
	json.Unmarshal(meRec.Body.Bytes(), &me)
	if !me.Authenticated {
		t.Error("/me should report authenticated with a valid session")
	}

	// logout revokes + clears.
	loReq := httptest.NewRequest(http.MethodPost, "/_pbc/auth/logout", nil)
	loReq.Header.Set("Origin", testMemberOrigin)
	loReq.AddCookie(&http.Cookie{Name: memberCookieDev, Value: token})
	loRec := httptest.NewRecorder()
	h.ServeHTTP(loRec, loReq)
	if loRec.Code != http.StatusOK {
		t.Fatalf("logout: %d", loRec.Code)
	}
	if _, ok, _ := app.SessionByToken(token); ok {
		t.Error("session still resolves after logout")
	}
	if ck := findCookie(loRec, memberCookieDev); ck == nil || ck.MaxAge >= 0 {
		t.Error("logout did not expire the cookie")
	}
}

func TestMemberAuthDisabledWithoutOrigin(t *testing.T) {
	app, _ := appstore.Open(":memory:")
	defer app.Close()
	h, err := New(app, Options{}) // no member origin
	if err != nil {
		t.Fatal(err)
	}
	rec := memberPost(t, h, "/_pbc/auth/register/options", map[string]string{"invite": "x"}, testMemberOrigin)
	// Route not registered when member auth is off.
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("member auth off: status %d, want 404/405", rec.Code)
	}
	// The read-only endpoints still work.
	hrec := httptest.NewRecorder()
	h.ServeHTTP(hrec, httptest.NewRequest(http.MethodGet, "/_pbc/health", nil))
	if hrec.Code != http.StatusOK {
		t.Errorf("health should still serve: %d", hrec.Code)
	}
}

func TestBadMemberOriginRejectedAtNew(t *testing.T) {
	app, _ := appstore.Open(":memory:")
	defer app.Close()
	if _, err := New(app, Options{MemberOrigin: "http://example.com"}); err == nil {
		t.Error("non-localhost http origin should be rejected")
	}
}

// --- comment posting ----------------------------------------------------------

// memberCookie returns a valid member session cookie for accID.
func memberCookie(t *testing.T, app *appstore.Store, accID int64) *http.Cookie {
	t.Helper()
	token, _, err := app.CreateSession(accID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: memberCookieDev, Value: token}
}

func postComment(t *testing.T, h http.Handler, body any, origin string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/_pbc/comments", bytes.NewReader(b))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCommentPostRequiresSession(t *testing.T) {
	h, _ := newMemberAPI(t)
	rec := postComment(t, h, map[string]string{"path": "/post", "body": "hi"}, testMemberOrigin, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", rec.Code)
	}
}

func TestCommentPostBadOrigin(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	rec := postComment(t, h, map[string]string{"path": "/post", "body": "hi"}, "https://evil.example", memberCookie(t, app, accID))
	if rec.Code != http.StatusForbidden {
		t.Errorf("bad origin: status %d, want 403", rec.Code)
	}
}

func TestCommentPostHappyPathIsPending(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	rec := postComment(t, h, map[string]string{"path": "/post", "alias": "raven", "body": "great read"}, testMemberOrigin, memberCookie(t, app, accID))
	if rec.Code != http.StatusOK {
		t.Fatalf("post comment: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.OK || out.Status != "pending" {
		t.Errorf("response = %+v, want ok/pending", out)
	}
	// Pending -> not returned by the public approved-only read.
	if cs, _ := app.CommentsByPage("/post", appstore.CommentApproved); len(cs) != 0 {
		t.Errorf("comment should be pending, not approved: %d", len(cs))
	}
	// But it exists in the queue.
	if q, _ := app.PendingComments(); len(q) != 1 {
		t.Errorf("pending queue = %d, want 1", len(q))
	}
}

func TestCommentPostValidation(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)
	cases := []map[string]string{
		{"path": "", "body": "b"},                                      // no path
		{"path": "relative", "body": "b"},                              // path not absolute
		{"path": "/p", "body": ""},                                     // empty body
		{"path": "/p", "body": string(make([]byte, maxCommentBody+1))}, // body too long
	}
	for i, c := range cases {
		if rec := postComment(t, h, c, testMemberOrigin, ck); rec.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, want 400", i, rec.Code)
		}
	}
}

func TestCommentPostRejectsBannedMember(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)
	if err := app.SetAccountStatus(accID, appstore.StatusBanned); err != nil {
		t.Fatal(err)
	}
	// A banned member's session no longer resolves -> 401.
	rec := postComment(t, h, map[string]string{"path": "/p", "body": "hi"}, testMemberOrigin, ck)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("banned member post: status %d, want 401", rec.Code)
	}
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}
