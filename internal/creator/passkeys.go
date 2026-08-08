package creator

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
	"go.privatebychoice.com/pbcssg/internal/webauthn"
)

// This file is the creator's passkey manager (SPEC §2.4): a signed-in
// creator registers additional authenticators (the ≥2-key survival rule), labels them,
// and removes one — with a "keep at least one" guard so an account can never delete its
// way out of every credential. Adding a key is an authenticated WebAuthn registration
// ceremony with no invite: the new credential is bound to the account's existing user
// handle (so it belongs to the same discoverable account) and the account's current
// credentials go in excludeCredentials (so the same authenticator can't double-register).
//
// These ceremony endpoints live under /admin/passkeys/ (not /admin/auth/, which is
// exempt from the session gate) so they require a session — you must already be signed
// in to add or drop a key.

// addKeyCeremony is the challenge-store context for an authenticated add-a-passkey
// ceremony: the account the key is being added to and the label chosen for it.
type addKeyCeremony struct {
	accountID int64
	label     string
}

// credentialView is one row in the passkey manager.
type credentialView struct {
	ID         int64
	Label      string
	Transports string
	Created    string
	LastUsed   string // "never" until first assertion
}

// handlePasskeys renders the passkey manager for the signed-in creator.
func (c *Creator) handlePasskeys(w http.ResponseWriter, r *http.Request) {
	c.renderPasskeys(w, r, http.StatusOK, "", "")
}

// renderPasskeys lists the current account's credentials with an optional banner.
func (c *Creator) renderPasskeys(w http.ResponseWriter, r *http.Request, code int, notice, errMsg string) {
	if !c.authEnabled() {
		if code != http.StatusOK {
			w.WriteHeader(code)
		}
		c.render(w, "passkeys", map[string]any{"Disabled": true, "Notice": notice, "Error": errMsg})
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	creds, err := c.appDB.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "passkeys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]credentialView, 0, len(creds))
	for _, cr := range creds {
		label := cr.Label
		if label == "" {
			label = "Unnamed key"
		}
		last := "never"
		if !cr.LastUsedAt.IsZero() {
			last = cr.LastUsedAt.Format("2006-01-02")
		}
		views = append(views, credentialView{
			ID: cr.ID, Label: label, Transports: cr.Transports,
			Created: cr.CreatedAt.Format("2006-01-02"), LastUsed: last,
		})
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "passkeys", map[string]any{
		"CSRF": c.csrf, "Passkeys": views, "Count": len(views),
		"OnlyOne": len(views) == 1, "Notice": notice, "Error": errMsg,
	})
}

// handlePasskeyAddOptions issues a registration challenge for a *new* credential on the
// signed-in account (no invite). user.id is the account's existing handle so the new
// key belongs to the same discoverable account; excludeCredentials lists the account's
// current keys so the same authenticator refuses to enroll twice.
func (c *Creator) handlePasskeyAddOptions(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	creds, err := c.appDB.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	id, challenge, err := c.flow.Issue(addKeyCeremony{accountID: acc.ID, label: capLabel(req.Label)})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exclude := make([]string, 0, len(creds))
	for _, cr := range creds {
		exclude = append(exclude, cr.CredID) // already base64url
	}
	// Same user.id as the existing account (acc.UserHandle) → the new key is another
	// credential for the same discoverable account, not a second account; the account's
	// current creds are excluded so the same authenticator can't enrol twice.
	authflow.WriteJSON(w, c.flow.RegistrationOptions(id, acc.UserHandle, challenge, exclude))
}

// handlePasskeyAddVerify verifies the attestation and stores the new credential on the
// signed-in account. It re-resolves the session and checks the ceremony was issued for
// that same account, so a challenge can't be redeemed against a different account.
func (c *Creator) handlePasskeyAddVerify(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var req authflow.RegisterVerifyRequest
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	challenge, ctxAny, ok := c.flow.Consume(req.ID)
	if !ok {
		http.Error(w, "ceremony expired or unknown", http.StatusBadRequest)
		return
	}
	ctx, ok := ctxAny.(addKeyCeremony)
	if !ok || ctx.accountID != acc.ID {
		http.Error(w, "wrong ceremony", http.StatusBadRequest)
		return
	}
	clientData, err1 := authflow.Unb64(req.Response.ClientDataJSON)
	attObj, err2 := authflow.Unb64(req.Response.AttestationObject)
	if err1 != nil || err2 != nil {
		http.Error(w, "malformed credential encoding", http.StatusBadRequest)
		return
	}
	vc, err := c.flow.Verifier().VerifyRegistration(challenge, webauthn.RegistrationResponse{
		ClientDataJSON:    clientData,
		AttestationObject: attObj,
	})
	if err != nil {
		log.Printf("WARN creator: add-passkey verification failed: %v", err)
		http.Error(w, "registration verification failed", http.StatusBadRequest)
		return
	}
	if _, err := c.appDB.AddCredential(appstore.Credential{
		AccountID:  acc.ID,
		CredID:     authflow.B64(vc.CredID),
		PublicKey:  vc.COSEPublicKey,
		SignCount:  vc.SignCount,
		AAGUID:     authflow.AAGUIDString(vc.AAGUID),
		Transports: authflow.SanitizeTransports(req.Transports),
		Label:      ctx.label,
	}); err != nil {
		// A duplicate credential id (the same authenticator, despite excludeCredentials)
		// surfaces as a UNIQUE violation — report it as a benign conflict.
		if containsUnique(err.Error()) {
			http.Error(w, "that authenticator is already registered", http.StatusConflict)
			return
		}
		log.Printf("ERROR creator: add credential: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("INFO creator: account %d added a passkey", acc.ID)
	authflow.WriteJSON(w, map[string]bool{"ok": true})
}

// passkeyOwned resolves the session and the {id} path value, checks the form CSRF, and
// confirms the credential belongs to the signed-in account — writing the error response
// itself when any check fails. It returns the account and the credential id.
func (c *Creator) passkeyOwned(w http.ResponseWriter, r *http.Request) (appstore.Account, int64, bool) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return appstore.Account{}, 0, false
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return appstore.Account{}, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad credential id", http.StatusBadRequest)
		return appstore.Account{}, 0, false
	}
	return acc, id, true
}

// handlePasskeyLabel renames one of the signed-in account's credentials.
func (c *Creator) handlePasskeyLabel(w http.ResponseWriter, r *http.Request) {
	acc, id, ok := c.passkeyOwned(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetCredentialLabel(id, acc.ID, capLabel(r.FormValue("label"))); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			c.renderPasskeys(w, r, http.StatusNotFound, "", "That passkey no longer exists.")
			return
		}
		c.renderPasskeys(w, r, http.StatusBadRequest, "", "Could not rename the passkey: "+err.Error())
		return
	}
	c.renderPasskeys(w, r, http.StatusOK, "Renamed the passkey.", "")
}

// handlePasskeyDelete removes one of the signed-in account's credentials, refusing to
// remove the last one (an account must always keep at least one way in — §2.4).
func (c *Creator) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	acc, id, ok := c.passkeyOwned(w, r)
	if !ok {
		return
	}
	creds, err := c.appDB.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(creds) <= 1 {
		c.renderPasskeys(w, r, http.StatusBadRequest, "", "You can't remove your only passkey — add another first.")
		return
	}
	if err := c.appDB.DeleteCredential(id, acc.ID); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			c.renderPasskeys(w, r, http.StatusNotFound, "", "That passkey no longer exists.")
			return
		}
		c.renderPasskeys(w, r, http.StatusBadRequest, "", "Could not remove the passkey: "+err.Error())
		return
	}
	log.Printf("INFO creator: account %d removed a passkey", acc.ID)
	c.renderPasskeys(w, r, http.StatusOK, "Removed the passkey.", "")
}
