package creator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// postJSONAuthed posts a JSON ceremony body carrying a session cookie (the add-passkey
// endpoints are session-gated, unlike the public register/login ceremonies).
func postJSONAuthed(t *testing.T, c *Creator, path string, body any, csrf, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

func TestPasskeyAddFlow(t *testing.T) {
	c, app := newAuthCreator(t)
	_, existingCred, handle, accID := seedCreator(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)

	// Options: bound to the account's handle, existing key excluded.
	rec := postJSONAuthed(t, c, "/admin/passkeys/add/options", map[string]string{"label": "Backup key"}, c.csrf, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("add/options status %d, body %s", rec.Code, rec.Body.String())
	}
	var opts authflow.CreationOptions
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	if opts.PublicKey.User["id"] != handle {
		t.Errorf("new key user.id = %q, want the account handle %q (same discoverable account)", opts.PublicKey.User["id"], handle)
	}
	foundExcluded := false
	for _, id := range opts.PublicKey.ExcludeCredentials {
		if id == existingCred {
			foundExcluded = true
		}
	}
	if !foundExcluded {
		t.Errorf("excludeCredentials = %v, want the existing key %q", opts.PublicKey.ExcludeCredentials, existingCred)
	}

	// Verify: stores a second credential with the chosen label.
	rec = postJSONAuthed(t, c, "/admin/passkeys/add/verify", buildRegistration(t, opts, testAdminOrigin), c.csrf, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("add/verify status %d, body %s", rec.Code, rec.Body.String())
	}
	creds, _ := app.CredentialsForAccount(accID)
	if len(creds) != 2 {
		t.Fatalf("account has %d credentials, want 2", len(creds))
	}
	if creds[1].Label != "Backup key" {
		t.Errorf("new credential label = %q, want %q", creds[1].Label, "Backup key")
	}
}

func TestPasskeyAddRequiresSession(t *testing.T) {
	c, _ := newAuthCreator(t)
	// No session cookie: the gate refuses the POST.
	rec := postJSON(t, c, "/admin/passkeys/add/options", map[string]string{"label": "x"}, c.csrf)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated add/options status %d, want 401", rec.Code)
	}
}

func TestPasskeyAddRejectsBadCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	token, _, _ := app.CreateSession(accID, time.Hour)
	rec := postJSONAuthed(t, c, "/admin/passkeys/add/options", map[string]string{"label": "x"}, "wrong", token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad-csrf add/options status %d, want 403", rec.Code)
	}
}

func TestPasskeyListAndRename(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, accID := seedCreator(t, app)
	id2, _ := app.AddCredential(appstore.Credential{AccountID: accID, CredID: "k2", PublicKey: []byte{2}, Label: "Second"})

	// The list shows both keys' labels.
	rec := authedGet(t, c, app, accID, "/admin/passkeys")
	if rec.Code != http.StatusOK {
		t.Fatalf("passkeys GET status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Second") || !strings.Contains(body, "Add a passkey") {
		t.Errorf("passkeys page missing expected content:\n%s", body)
	}

	// Rename the second key.
	if rec := authedForm(t, c, accID, "/admin/passkeys/"+itoa(id2)+"/label", url.Values{"label": {"Phone"}}); rec.Code != http.StatusOK {
		t.Fatalf("rename status %d", rec.Code)
	}
	if got, _, _ := app.CredentialByCredID("k2"); got.Label != "Phone" {
		t.Errorf("label = %q, want Phone", got.Label)
	}
}

func TestPasskeyDeleteKeepsLastKey(t *testing.T) {
	c, app := newAuthCreator(t)
	_, existingCred, _, accID := seedCreator(t, app)
	creds, _ := app.CredentialsForAccount(accID)
	onlyID := creds[0].ID

	// Removing the only key is refused.
	rec := authedForm(t, c, accID, "/admin/passkeys/"+itoa(onlyID)+"/delete", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete-last status %d, want 400", rec.Code)
	}
	if cs, _ := app.CredentialsForAccount(accID); len(cs) != 1 {
		t.Fatalf("last key removed despite guard: %d left", len(cs))
	}

	// With a second key, one can be removed.
	id2, _ := app.AddCredential(appstore.Credential{AccountID: accID, CredID: "k2", PublicKey: []byte{2}})
	if rec := authedForm(t, c, accID, "/admin/passkeys/"+itoa(id2)+"/delete", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete status %d", rec.Code)
	}
	cs, _ := app.CredentialsForAccount(accID)
	if len(cs) != 1 || cs[0].CredID != existingCred {
		t.Errorf("after delete: %d creds, want just the original %q", len(cs), existingCred)
	}
}

func TestPasskeyDisabledWithoutStore(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := New(st, Config{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/passkeys", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("passkeys GET (no store) status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "needs the runtime store") {
		t.Errorf("expected the disabled notice:\n%s", rec.Body.String())
	}
}
