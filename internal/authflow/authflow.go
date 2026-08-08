// Package authflow is the shared WebAuthn ceremony core for pbcssg's passkey auth
// (SPEC §2.4). The creator (admin origin) and the Community-Member public origin run
// the same register/login/logout/session logic against distinct credential domains —
// the per-origin RP-ID split — so it lives here once, parameterized by store, verifier,
// role, cookie config, and RP/user labels. Each caller keeps only its thin HTTP wrapper
// (route wiring, its CSRF style, and any divergent response such as the creator's logout
// redirect); the ceremony bodies, session cookie, and helpers are shared. Extracting
// this removes the duplication that had grown between internal/creator and
// internal/publicapi with no behavior change.
package authflow

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"crypto/rand"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/webauthn"
)

// maxCeremonyBody caps a ceremony request body — WebAuthn payloads are small.
const maxCeremonyBody = 64 << 10

// handleBytes is the length of a generated WebAuthn user handle / account handle.
const handleBytes = 32

// Config parameterizes a Flow for one origin + role.
type Config struct {
	Store        *appstore.Store   // the runtime store
	Verifier     webauthn.Verifier // RP ID + exact origin
	Role         string            // appstore.RoleCreator | RoleMember | …
	RPName       string            // rp.name shown in the passkey chooser
	UserName     string            // WebAuthn user.name (non-identifying label)
	UserDisplay  string            // WebAuthn user.displayName
	CookieName   string            // session cookie name (__Host-…/dev)
	CookieSecure bool              // Secure attribute (https origin)
	SessionTTL   time.Duration     // session lifetime
	ChallengeTTL time.Duration     // per-ceremony challenge lifetime
	LogPrefix    string            // log tag, e.g. "creator" / "publicapi"
}

// Flow runs the ceremonies for one Config. It owns the challenge store, so the same
// Flow backs register, login, and any authenticated add-credential ceremony a caller
// layers on top (via Issue/Consume).
type Flow struct {
	cfg        Config
	challenges *webauthn.ChallengeStore
}

// New builds a Flow with a fresh challenge store sized to the configured TTL.
func New(cfg Config) *Flow {
	return &Flow{cfg: cfg, challenges: webauthn.NewChallengeStore(cfg.ChallengeTTL)}
}

// NewVerifier builds a webauthn.Verifier from an RP ID and the exact origin, so callers
// need not import internal/webauthn just to construct Config.Verifier.
func NewVerifier(rpID, origin string) webauthn.Verifier {
	return webauthn.Verifier{RPID: rpID, Origin: origin}
}

// Verifier exposes the configured verifier for a caller's own ceremonies (e.g. the
// creator's authenticated add-a-passkey flow).
func (f *Flow) Verifier() webauthn.Verifier { return f.cfg.Verifier }

// Issue / Consume expose the shared challenge store so a caller can run an additional
// ceremony (with its own context type) on the same Flow.
func (f *Flow) Issue(ctx any) (id string, challenge []byte, err error) {
	return f.challenges.Issue(ctx)
}
func (f *Flow) Consume(id string) (challenge []byte, ctx any, ok bool) {
	return f.challenges.Consume(id)
}

// --- option / verify payload shapes (base64url binary for JSON) ----------------

// PubKeyCredParam is one allowed COSE algorithm in the creation options.
type PubKeyCredParam struct {
	Type string `json:"type"`
	Alg  int    `json:"alg"`
}

// CreationOptions is the PublicKeyCredentialCreationOptions handed to the browser: a
// discoverable (resident) credential, user verification required, attestation "none".
type CreationOptions struct {
	ID        string `json:"id"`
	PublicKey struct {
		Challenge              string            `json:"challenge"`
		RP                     map[string]string `json:"rp"`
		User                   map[string]string `json:"user"`
		PubKeyCredParams       []PubKeyCredParam `json:"pubKeyCredParams"`
		Timeout                int               `json:"timeout"`
		Attestation            string            `json:"attestation"`
		AuthenticatorSelection map[string]string `json:"authenticatorSelection"`
		ExcludeCredentials     []string          `json:"excludeCredentials"`
	} `json:"publicKey"`
}

// RequestOptions is the PublicKeyCredentialRequestOptions for a login (assertion):
// usernameless, so allowCredentials is empty and the authenticator offers its
// discoverable credentials for this RP.
type RequestOptions struct {
	ID        string `json:"id"`
	PublicKey struct {
		Challenge        string   `json:"challenge"`
		RPID             string   `json:"rpId"`
		Timeout          int      `json:"timeout"`
		UserVerification string   `json:"userVerification"`
		AllowCredentials []string `json:"allowCredentials"`
	} `json:"publicKey"`
}

// RegisterVerifyRequest is the client's reply after navigator.credentials.create.
type RegisterVerifyRequest struct {
	ID       string `json:"id"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AttestationObject string `json:"attestationObject"`
	} `json:"response"`
	Transports []string `json:"transports"`
}

// loginVerifyRequest is the client's reply after navigator.credentials.get.
type loginVerifyRequest struct {
	ID           string `json:"id"`
	CredentialID string `json:"credentialId"`
	UserHandle   string `json:"userHandle"`
	Response     struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
	} `json:"response"`
}

// regCeremony carries the invite and the freshly minted handle from register options
// to verify (the handle becomes the WebAuthn user.id and the account's user_handle).
type regCeremony struct{ invite, handle string }

// RegistrationOptions builds creation options for an account handle, with the given
// excludeCredentials (base64url). It is the shared shape used by both the invite
// register flow (fresh handle, empty exclude) and a caller's authenticated add-a-key
// flow (existing handle, the account's current creds excluded).
func (f *Flow) RegistrationOptions(id, handle string, challenge []byte, exclude []string) CreationOptions {
	if exclude == nil {
		exclude = []string{}
	}
	var o CreationOptions
	o.ID = id
	o.PublicKey.Challenge = B64(challenge)
	o.PublicKey.RP = map[string]string{"id": f.cfg.Verifier.RPID, "name": f.cfg.RPName}
	o.PublicKey.User = map[string]string{"id": handle, "name": f.cfg.UserName, "displayName": f.cfg.UserDisplay}
	o.PublicKey.PubKeyCredParams = []PubKeyCredParam{{"public-key", -7}, {"public-key", -8}}
	o.PublicKey.Timeout = int(f.cfg.ChallengeTTL.Milliseconds())
	o.PublicKey.Attestation = "none"
	o.PublicKey.AuthenticatorSelection = map[string]string{"residentKey": "required", "userVerification": "required"}
	o.PublicKey.ExcludeCredentials = exclude
	return o
}

// WriteRegisterOptions decodes the invite, issues a challenge, and writes the creation
// options for a new discoverable account. The caller does CSRF/enable checks first. The
// invite is not validated here (that is atomic on verify), so this is not an invite
// oracle.
func (f *Flow) WriteRegisterOptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Invite string `json:"invite"`
	}
	if !DecodeJSON(w, r, &req) {
		return
	}
	// Trim before both the emptiness check and storage: the code is redeemed by exact
	// hash at verify time, so any stray surrounding whitespace (e.g. a copy-paste
	// newline from a non-browser client) would hash to a miss and surface as the
	// generic "invite is not valid" 409 — a confusing failure with a clean cause.
	invite := strings.TrimSpace(req.Invite)
	if invite == "" {
		http.Error(w, "invite code required", http.StatusBadRequest)
		return
	}
	handle, err := NewHandle()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	id, challenge, err := f.challenges.Issue(regCeremony{invite: invite, handle: handle})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	WriteJSON(w, f.RegistrationOptions(id, handle, challenge, nil))
}

// RegisterVerify verifies the attestation, atomically redeems the invite for the
// configured role, and opens a session. The caller does CSRF/enable checks first. All
// failure modes create nothing.
func (f *Flow) RegisterVerify(w http.ResponseWriter, r *http.Request) {
	var req RegisterVerifyRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	challenge, ctxAny, ok := f.challenges.Consume(req.ID)
	if !ok {
		http.Error(w, "ceremony expired or unknown", http.StatusBadRequest)
		return
	}
	ctx, ok := ctxAny.(regCeremony)
	if !ok {
		http.Error(w, "wrong ceremony type", http.StatusBadRequest)
		return
	}
	clientData, e1 := Unb64(req.Response.ClientDataJSON)
	attObj, e2 := Unb64(req.Response.AttestationObject)
	if e1 != nil || e2 != nil {
		http.Error(w, "malformed credential encoding", http.StatusBadRequest)
		return
	}
	vc, err := f.cfg.Verifier.VerifyRegistration(challenge, webauthn.RegistrationResponse{
		ClientDataJSON: clientData, AttestationObject: attObj,
	})
	if err != nil {
		log.Printf("WARN %s: registration verification failed: %v", f.cfg.LogPrefix, err)
		http.Error(w, "registration verification failed", http.StatusBadRequest)
		return
	}
	// The role check keeps a creator invite off the member origin and vice-versa (§2.4).
	acc, err := f.cfg.Store.RedeemInviteAndRegister(ctx.invite, ctx.handle, appstore.Credential{
		CredID:     B64(vc.CredID),
		PublicKey:  vc.COSEPublicKey,
		SignCount:  vc.SignCount,
		AAGUID:     AAGUIDString(vc.AAGUID),
		Transports: SanitizeTransports(req.Transports),
	}, f.cfg.Role)
	if err != nil {
		if IsInviteErr(err) {
			http.Error(w, "invite is not valid", http.StatusConflict)
			return
		}
		log.Printf("ERROR %s: register account: %v", f.cfg.LogPrefix, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f.OpenSession(w, acc.ID)
}

// WriteLoginOptions issues an assertion challenge and writes the request options. The
// caller does CSRF/enable checks first.
func (f *Flow) WriteLoginOptions(w http.ResponseWriter, r *http.Request) {
	id, challenge, err := f.challenges.Issue(nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var o RequestOptions
	o.ID = id
	o.PublicKey.Challenge = B64(challenge)
	o.PublicKey.RPID = f.cfg.Verifier.RPID
	o.PublicKey.Timeout = int(f.cfg.ChallengeTTL.Milliseconds())
	o.PublicKey.UserVerification = "required"
	o.PublicKey.AllowCredentials = []string{}
	WriteJSON(w, o)
}

// LoginVerify verifies an assertion against the stored credential and opens a session.
// Failures are uniform 401s so the endpoint is not an oracle for which credential ids
// or handles exist. The caller does CSRF/enable checks first.
func (f *Flow) LoginVerify(w http.ResponseWriter, r *http.Request) {
	var req loginVerifyRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	challenge, _, ok := f.challenges.Consume(req.ID)
	if !ok {
		http.Error(w, "ceremony expired or unknown", http.StatusBadRequest)
		return
	}
	cred, found, err := f.cfg.Store.CredentialByCredID(req.CredentialID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	acc, ok, err := f.cfg.Store.AccountByID(cred.AccountID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// A banned or wrong-role account cannot hold a session on this origin. The RP-ID
	// split already keeps other origins' creds out; this is defense in depth.
	if !ok || acc.Status == appstore.StatusBanned || acc.Role != f.cfg.Role {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	if req.UserHandle != "" && req.UserHandle != acc.UserHandle {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	clientData, e1 := Unb64(req.Response.ClientDataJSON)
	authData, e2 := Unb64(req.Response.AuthenticatorData)
	sig, e3 := Unb64(req.Response.Signature)
	if e1 != nil || e2 != nil || e3 != nil {
		http.Error(w, "malformed credential encoding", http.StatusBadRequest)
		return
	}
	newCount, err := f.cfg.Verifier.VerifyAssertion(challenge,
		webauthn.StoredCredential{COSEPublicKey: cred.PublicKey, SignCount: cred.SignCount},
		webauthn.AssertionResponse{ClientDataJSON: clientData, AuthenticatorData: authData, Signature: sig})
	if err != nil {
		log.Printf("WARN %s: assertion verification failed for account %d: %v", f.cfg.LogPrefix, acc.ID, err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	if err := f.cfg.Store.UpdateSignCount(req.CredentialID, newCount); err != nil {
		log.Printf("WARN %s: update sign count: %v", f.cfg.LogPrefix, err)
	}
	if err := f.cfg.Store.TouchAccount(acc.ID); err != nil {
		log.Printf("WARN %s: touch account: %v", f.cfg.LogPrefix, err)
	}
	f.OpenSession(w, acc.ID)
}

// --- session cookie + resolution ----------------------------------------------

// OpenSession mints a session for accID, sets the cookie, and writes {"ok":true}.
func (f *Flow) OpenSession(w http.ResponseWriter, accID int64) {
	token, _, err := f.cfg.Store.CreateSession(accID, f.cfg.SessionTTL)
	if err != nil {
		log.Printf("ERROR %s: create session: %v", f.cfg.LogPrefix, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f.SetCookie(w, token)
	log.Printf("INFO %s: account %d authenticated", f.cfg.LogPrefix, accID)
	WriteJSON(w, map[string]bool{"ok": true})
}

// SetCookie writes the session cookie: HttpOnly + SameSite=Strict always, Secure +
// `__Host-` on an https origin (§2.4).
func (f *Flow) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: f.cfg.CookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: f.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: int(f.cfg.SessionTTL.Seconds()),
	})
}

// ClearCookie expires the session cookie (logout).
func (f *Flow) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: f.cfg.CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: f.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: -1,
	})
}

// Resolve resolves the session cookie to a live account of the configured role.
// Returns false for a missing/expired session or a banned or wrong-role account.
func (f *Flow) Resolve(r *http.Request) (appstore.Account, bool) {
	ck, err := r.Cookie(f.cfg.CookieName)
	if err != nil || ck.Value == "" {
		return appstore.Account{}, false
	}
	sess, ok, err := f.cfg.Store.SessionByToken(ck.Value)
	if err != nil || !ok {
		return appstore.Account{}, false
	}
	acc, ok, err := f.cfg.Store.AccountByID(sess.AccountID)
	if err != nil || !ok || acc.Status == appstore.StatusBanned || acc.Role != f.cfg.Role {
		return appstore.Account{}, false
	}
	return acc, true
}

// RevokeCurrent revokes the session named by the request's cookie (logout). A missing
// or already-gone session is a no-op.
func (f *Flow) RevokeCurrent(r *http.Request) {
	if ck, err := r.Cookie(f.cfg.CookieName); err == nil && ck.Value != "" {
		if err := f.cfg.Store.RevokeSession(ck.Value); err != nil {
			log.Printf("WARN %s: revoke session on logout: %v", f.cfg.LogPrefix, err)
		}
	}
}

// --- shared helpers -----------------------------------------------------------

// NewHandle returns a fresh opaque WebAuthn user handle (base64url, no padding).
func NewHandle() (string, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("authflow: generate handle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// B64 / Unb64 are the base64url (no padding) conversions WebAuthn binary fields cross
// the wire in.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func Unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// AAGUIDString renders a credential AAGUID for storage: empty when all-zero (the
// attestation:none case — no device fingerprint), hex otherwise.
func AAGUIDString(a []byte) string {
	for _, x := range a {
		if x != 0 {
			return hex.EncodeToString(a)
		}
	}
	return ""
}

// SanitizeTransports keeps only short alphabetic transport hints, comma-joined for
// display; it never lets arbitrary client strings into storage unbounded.
func SanitizeTransports(in []string) string {
	var out []string
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > 16 {
			continue
		}
		ok := true
		for _, r := range t {
			if r < 'a' || r > 'z' {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

// DecodeJSON decodes a size-capped JSON ceremony body, writing a 400 on malformed
// input and reporting whether decoding succeeded.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxCeremonyBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return false
	}
	return true
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// IsInviteErr reports whether err is one of the invite redemption failures, which a
// public handler collapses into a single generic message (no enumeration oracle).
func IsInviteErr(err error) bool {
	return errors.Is(err, appstore.ErrInviteInvalid) || errors.Is(err, appstore.ErrInviteUsed) ||
		errors.Is(err, appstore.ErrInviteExpired) || errors.Is(err, appstore.ErrInviteRevoked)
}
