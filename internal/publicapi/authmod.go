package publicapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
)

const (
	modChallengeTTL = 2 * time.Minute
	// modSessionTTL is shorter than a member's: a moderator holds elevated powers, so a
	// weekly passkey re-tap is a reasonable privilege/convenience balance. A fresh
	// assertion renews it.
	modSessionTTL = 7 * 24 * time.Hour

	modCookieHost = "__Host-pbcssg_mod"
	modCookieDev  = "pbcssg_mod"
)

// moderatorAuth holds the moderator WebAuthn state on the public origin. Moderators
// share the public RP ID with members (the split puts members and moderators on the
// public origin — §2.4), but hold a distinct role, session cookie, and challenge store.
// Login is role-gated (authflow rejects a non-moderator account), so the two credential
// sets on the shared RP ID never cross: a member's passkey cannot open a moderator
// session and vice-versa.
type moderatorAuth struct {
	flow   *authflow.Flow
	origin string
}

func newModeratorAuth(app *appstore.Store, origin string) (*moderatorAuth, error) {
	rpID, err := rpIDFromOrigin(origin)
	if err != nil {
		return nil, fmt.Errorf("publicapi: moderator origin: %w", err)
	}
	secure := strings.HasPrefix(origin, "https://")
	name := modCookieDev
	if secure {
		name = modCookieHost
	}
	flow := authflow.New(authflow.Config{
		Store:        app,
		Verifier:     authflow.NewVerifier(rpID, origin),
		Role:         appstore.RoleModerator,
		RPName:       rpID,
		UserName:     "moderator",
		UserDisplay:  "Moderator",
		CookieName:   name,
		CookieSecure: secure,
		SessionTTL:   modSessionTTL,
		ChallengeTTL: modChallengeTTL,
		LogPrefix:    "publicapi moderator",
	})
	return &moderatorAuth{flow: flow, origin: origin}, nil
}

func (a *api) registerModeratorAuthRoutes() {
	a.mux.HandleFunc("POST /_pbc/mod/auth/register/options", a.handleModRegisterOptions)
	a.mux.HandleFunc("POST /_pbc/mod/auth/register/verify", a.handleModRegisterVerify)
	a.mux.HandleFunc("POST /_pbc/mod/auth/login/options", a.handleModLoginOptions)
	a.mux.HandleFunc("POST /_pbc/mod/auth/login/verify", a.handleModLoginVerify)
	a.mux.HandleFunc("POST /_pbc/mod/auth/logout", a.handleModLogout)
	a.mux.HandleFunc("GET /_pbc/mod/auth/me", a.handleModMe)
}

// checkModOrigin is the Origin-header CSRF defense for moderator POSTs — the same public
// origin members use.
func (a *api) checkModOrigin(r *http.Request) bool {
	return a.modAuth != nil && r.Header.Get("Origin") == a.modAuth.origin
}

// resolveModeratorSession resolves the moderator cookie to a live moderator account
// (authflow.Resolve rejects a banned or wrong-role account).
func (a *api) resolveModeratorSession(r *http.Request) (appstore.Account, bool) {
	if a.modAuth == nil {
		return appstore.Account{}, false
	}
	return a.modAuth.flow.Resolve(r)
}

// --- ceremonies (thin wrappers: Origin-CSRF, then the shared flow) --------------

func (a *api) handleModRegisterOptions(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.modAuth.flow.WriteRegisterOptions(w, r)
}

func (a *api) handleModRegisterVerify(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	// The flow's Role=moderator means only a moderator invite redeems here — a member or
	// creator invite is refused as invalid (no privilege crossing).
	a.modAuth.flow.RegisterVerify(w, r)
}

func (a *api) handleModLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.modAuth.flow.WriteLoginOptions(w, r)
}

func (a *api) handleModLoginVerify(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.modAuth.flow.LoginVerify(w, r)
}

func (a *api) handleModLogout(w http.ResponseWriter, r *http.Request) {
	if !a.checkModOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.modAuth.flow.RevokeCurrent(r)
	a.modAuth.flow.ClearCookie(w)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleModMe reports the signed-in moderator's identity and elevated grants so the
// moderator UI can show only the controls they hold.
func (a *api) handleModMe(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		writeJSON(w, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, map[string]any{
		"authenticated": true,
		"label":         acc.Label,
		"alias":         acc.Alias, // public display name; the comment widget shows and lets them change it
		"canInvite":     acc.CanInvite,
		"canBan":        acc.CanBan,
	})
}
