package publicapi

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
	"go.privatebychoice.com/pbcssg/internal/webauthn"
)

// This file is the moderator passkey manager on the public origin — the moderator
// counterpart to the creator's /admin/passkeys (§2.4). A signed-in moderator registers
// additional authenticators (the keep-more-than-one survival rule), labels them, and
// removes one, with a "keep at least one" guard so a moderator can never delete their way
// out of every credential. Adding a key is an authenticated WebAuthn ceremony with no
// invite: the new credential is bound to the moderator's existing user handle and their
// current credentials go in excludeCredentials. It reuses the moderator flow's shared
// challenge store (Issue/Consume) and verifier.

// modAddKeyCeremony is the challenge-store context for a moderator's add-a-passkey
// ceremony: the account the key is added to and the label chosen for it.
type modAddKeyCeremony struct {
	accountID int64
	label     string
}

// modCredentialView is one row in the moderator passkey manager.
type modCredentialView struct {
	ID         int64
	Label      string
	Transports string
	Created    string
	LastUsed   string
}

type modPasskeysData struct {
	Label    string // the moderator's staff label, for the header
	Passkeys []modCredentialView
	OnlyOne  bool
	Notice   string
	Error    string
}

func (a *api) registerModPasskeyRoutes() {
	a.mux.HandleFunc("GET /_pbc/mod/passkeys", a.handleModPasskeys)
	a.mux.HandleFunc("POST /_pbc/mod/passkeys/add/options", a.handleModPasskeyAddOptions)
	a.mux.HandleFunc("POST /_pbc/mod/passkeys/add/verify", a.handleModPasskeyAddVerify)
	a.mux.HandleFunc("POST /_pbc/mod/passkeys/{id}/label", a.handleModPasskeyLabel)
	a.mux.HandleFunc("POST /_pbc/mod/passkeys/{id}/remove", a.handleModPasskeyRemove)
}

func (a *api) handleModPasskeys(w http.ResponseWriter, r *http.Request) {
	a.renderModPasskeys(w, r, http.StatusOK, "", "")
}

func (a *api) renderModPasskeys(w http.ResponseWriter, r *http.Request, code int, notice, errMsg string) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		// Not signed in — send them to the moderator surface where they can sign in.
		http.Redirect(w, r, "/_pbc/moderate", http.StatusSeeOther)
		return
	}
	creds, err := a.app.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	views := make([]modCredentialView, 0, len(creds))
	for _, cr := range creds {
		label := cr.Label
		if label == "" {
			label = "Unnamed key"
		}
		last := "never"
		if !cr.LastUsedAt.IsZero() {
			last = cr.LastUsedAt.Format("2006-01-02")
		}
		views = append(views, modCredentialView{
			ID: cr.ID, Label: label, Transports: cr.Transports,
			Created: cr.CreatedAt.Format("2006-01-02"), LastUsed: last,
		})
	}
	setModeratePageHeaders(w, "text/html; charset=utf-8")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_ = modPasskeysTmpl.Execute(w, modPasskeysData{
		Label: acc.Label, Passkeys: views, OnlyOne: len(views) == 1, Notice: notice, Error: errMsg,
	})
}

// handleModPasskeyAddOptions issues a registration challenge for a NEW credential on the
// signed-in moderator (no invite). Called via fetch, so the Origin check is reliable.
func (a *api) handleModPasskeyAddOptions(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	creds, err := a.app.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	id, challenge, err := a.modAuth.flow.Issue(modAddKeyCeremony{accountID: acc.ID, label: capLabel(req.Label)})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exclude := make([]string, 0, len(creds))
	for _, cr := range creds {
		exclude = append(exclude, cr.CredID) // already base64url
	}
	// Same user.id (acc.UserHandle) → the new key is another credential for the same
	// discoverable account; excludeCredentials stops the same authenticator enrolling twice.
	authflow.WriteJSON(w, a.modAuth.flow.RegistrationOptions(id, acc.UserHandle, challenge, exclude))
}

// handleModPasskeyAddVerify verifies the attestation and stores the new credential on the
// signed-in moderator, checking the ceremony was issued for that same account.
func (a *api) handleModPasskeyAddVerify(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	var req authflow.RegisterVerifyRequest
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	challenge, ctxAny, ok := a.modAuth.flow.Consume(req.ID)
	if !ok {
		http.Error(w, "ceremony expired or unknown", http.StatusBadRequest)
		return
	}
	ctx, ok := ctxAny.(modAddKeyCeremony)
	if !ok || ctx.accountID != acc.ID {
		http.Error(w, "wrong ceremony", http.StatusBadRequest)
		return
	}
	clientData, e1 := authflow.Unb64(req.Response.ClientDataJSON)
	attObj, e2 := authflow.Unb64(req.Response.AttestationObject)
	if e1 != nil || e2 != nil {
		http.Error(w, "malformed credential encoding", http.StatusBadRequest)
		return
	}
	vc, err := a.modAuth.flow.Verifier().VerifyRegistration(challenge, webauthn.RegistrationResponse{
		ClientDataJSON: clientData, AttestationObject: attObj,
	})
	if err != nil {
		log.Printf("WARN publicapi moderator: add-passkey verification failed: %v", err)
		http.Error(w, "registration verification failed", http.StatusBadRequest)
		return
	}
	if _, err := a.app.AddCredential(appstore.Credential{
		AccountID:  acc.ID,
		CredID:     authflow.B64(vc.CredID),
		PublicKey:  vc.COSEPublicKey,
		SignCount:  vc.SignCount,
		AAGUID:     authflow.AAGUIDString(vc.AAGUID),
		Transports: authflow.SanitizeTransports(req.Transports),
		Label:      ctx.label,
	}); err != nil {
		if containsUnique(err.Error()) {
			http.Error(w, "that authenticator is already registered", http.StatusConflict)
			return
		}
		log.Printf("ERROR publicapi moderator: add credential: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("INFO publicapi moderator: account %d added a passkey", acc.ID)
	authflow.WriteJSON(w, map[string]bool{"ok": true})
}

// modPasskeyOwned resolves the moderator session and the {id}, confirming the credential
// belongs to the signed-in moderator. CSRF is the SameSite=Strict cookie (form POST).
func (a *api) modPasskeyOwned(w http.ResponseWriter, r *http.Request) (appstore.Account, int64, bool) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return appstore.Account{}, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad credential id", http.StatusBadRequest)
		return appstore.Account{}, 0, false
	}
	return acc, id, true
}

func (a *api) handleModPasskeyLabel(w http.ResponseWriter, r *http.Request) {
	acc, id, ok := a.modPasskeyOwned(w, r)
	if !ok {
		return
	}
	if err := a.app.SetCredentialLabel(id, acc.ID, capLabel(r.FormValue("label"))); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			a.renderModPasskeys(w, r, http.StatusNotFound, "", "That passkey no longer exists.")
			return
		}
		a.renderModPasskeys(w, r, http.StatusBadRequest, "", "Could not rename the passkey.")
		return
	}
	a.renderModPasskeys(w, r, http.StatusOK, "Renamed the passkey.", "")
}

func (a *api) handleModPasskeyRemove(w http.ResponseWriter, r *http.Request) {
	acc, id, ok := a.modPasskeyOwned(w, r)
	if !ok {
		return
	}
	creds, err := a.app.CredentialsForAccount(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(creds) <= 1 {
		a.renderModPasskeys(w, r, http.StatusBadRequest, "", "You can't remove your only passkey — add another first.")
		return
	}
	if err := a.app.DeleteCredential(id, acc.ID); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			a.renderModPasskeys(w, r, http.StatusNotFound, "", "That passkey no longer exists.")
			return
		}
		a.renderModPasskeys(w, r, http.StatusBadRequest, "", "Could not remove the passkey.")
		return
	}
	log.Printf("INFO publicapi moderator: account %d removed a passkey", acc.ID)
	a.renderModPasskeys(w, r, http.StatusOK, "Removed the passkey.", "")
}

// capLabel caps and single-lines a user-supplied label (shared by the passkey manager).
func capLabel(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	const max = 120
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return strings.TrimSpace(s)
}

// containsUnique reports whether a driver error names a UNIQUE violation (a duplicate
// authenticator, despite excludeCredentials).
func containsUnique(s string) bool { return strings.Contains(s, "UNIQUE") }

var modPasskeysTmpl = template.Must(template.New("modpasskeys").Parse(modPasskeysHTML))
