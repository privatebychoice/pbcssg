package publicapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

func modPostAuthed(t *testing.T, h http.Handler, path string, body any, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Origin", testMemberOrigin)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func modFormPost(t *testing.T, h http.Handler, path string, form url.Values, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// buildRegVerifyCred is buildRegVerify with a caller-chosen credential id, so a test can
// add more than one distinct passkey (cred_id is unique across accounts).
func buildRegVerifyCred(t *testing.T, id, origin string, challenge []byte, credID string) map[string]any {
	t.Helper()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cose := coseES256(t, &priv.PublicKey)
	ad := regAuthData(t, "localhost", cose, []byte(credID))
	cd := clientData(t, "webauthn.create", origin, challenge)
	return map[string]any{
		"id": id,
		"response": map[string]string{
			"clientDataJSON":    rawURL(cd),
			"attestationObject": rawURL(attObj(t, ad)),
		},
		"transports": []string{"internal"},
	}
}

// addModPasskey runs the authenticated add-a-key ceremony for the signed-in moderator.
func addModPasskey(t *testing.T, h http.Handler, ck *http.Cookie, credID string) {
	t.Helper()
	rec := modPostAuthed(t, h, "/_pbc/mod/passkeys/add/options", map[string]string{"label": "Laptop"}, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("add options: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID        string `json:"id"`
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	ch, _ := base64.RawURLEncoding.DecodeString(out.PublicKey.Challenge)
	body := buildRegVerifyCred(t, out.ID, testMemberOrigin, ch, credID)
	if rec := modPostAuthed(t, h, "/_pbc/mod/passkeys/add/verify", body, ck); rec.Code != http.StatusOK {
		t.Fatalf("add verify: %d %s", rec.Code, rec.Body.String())
	}
}

func hasLabel(creds []appstore.Credential, label string) bool {
	for _, c := range creds {
		if c.Label == label {
			return true
		}
	}
	return false
}

func credIDOf(creds []appstore.Credential, credID string) int64 {
	for _, c := range creds {
		if c.CredID == rawURL([]byte(credID)) {
			return c.ID
		}
	}
	return 0
}

func TestModPasskeyAddLabelRemove(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app) // one credential "mod-session-cred", label "Mo"
	seed, ok, _ := app.CredentialByCredID(rawURL([]byte("mod-session-cred")))
	if !ok {
		t.Fatal("seed credential missing")
	}
	modID := seed.AccountID

	// Add a second passkey via the authenticated ceremony.
	addModPasskey(t, h, ck, "mod-second-cred")
	creds, _ := app.CredentialsForAccount(modID)
	if len(creds) != 2 {
		t.Fatalf("credentials = %d, want 2 after add", len(creds))
	}

	// The manager page lists the added key.
	pkReq := httptest.NewRequest(http.MethodGet, "/_pbc/mod/passkeys", nil)
	pkReq.AddCookie(ck)
	pkRec := httptest.NewRecorder()
	h.ServeHTTP(pkRec, pkReq)
	if pkRec.Code != http.StatusOK {
		t.Fatalf("passkeys page: %d", pkRec.Code)
	}
	if !strings.Contains(pkRec.Body.String(), "Laptop") {
		t.Error("passkeys page missing the added key's label")
	}

	second := credIDOf(creds, "mod-second-cred")
	// Rename it.
	if rec := modFormPost(t, h, "/_pbc/mod/passkeys/"+strconv.FormatInt(second, 10)+"/label", url.Values{"label": {"Work laptop"}}, ck); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d", rec.Code)
	}
	if got, _ := app.CredentialsForAccount(modID); !hasLabel(got, "Work laptop") {
		t.Error("label not updated")
	}

	// Remove it → one key left.
	if rec := modFormPost(t, h, "/_pbc/mod/passkeys/"+strconv.FormatInt(second, 10)+"/remove", url.Values{}, ck); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d", rec.Code)
	}
	if got, _ := app.CredentialsForAccount(modID); len(got) != 1 {
		t.Errorf("after remove: %d creds, want 1", len(got))
	}

	// Removing the last key is refused (keep-at-least-one guard).
	rec := modFormPost(t, h, "/_pbc/mod/passkeys/"+strconv.FormatInt(seed.ID, 10)+"/remove", url.Values{}, ck)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("remove last key: status %d, want 400", rec.Code)
	}
	if got, _ := app.CredentialsForAccount(modID); len(got) != 1 {
		t.Errorf("last key removed despite guard: %d creds", len(got))
	}
}

func TestModPasskeysRequiresSession(t *testing.T) {
	h, _ := newMemberAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/_pbc/mod/passkeys", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("no session: status %d, want 303 (redirect to sign in)", rec.Code)
	}
}

func TestModPasskeyAddRequiresSession(t *testing.T) {
	h, _ := newMemberAPI(t)
	rec := modPostAuthed(t, h, "/_pbc/mod/passkeys/add/options", map[string]string{"label": "x"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("add without session: status %d, want 401", rec.Code)
	}
}
