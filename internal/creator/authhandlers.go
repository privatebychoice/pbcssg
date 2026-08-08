package creator

import (
	"net/http"
)

// The creator WebAuthn ceremonies (§2.4) are thin wrappers over the shared
// internal/authflow core: each does the creator-specific gate (auth enabled) and CSRF
// style (a per-process token in the X-CSRF-Token header, or the form token for logout),
// then delegates the ceremony body — register/login/session — to c.flow. The admin
// origin only accepts creator invites (the per-origin RP-ID split); that role check
// lives in the flow, configured with appstore.RoleCreator.

// handleRegisterPage serves the creator registration page (invite + passkey). It is a
// standalone page (no admin nav) since the visitor is not yet authenticated.
func (c *Creator) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	c.render(w, "register", map[string]any{"CSRF": c.csrf, "ShowMetrics": false})
}

func (c *Creator) handleRegisterOptions(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	c.flow.WriteRegisterOptions(w, r)
}

func (c *Creator) handleRegisterVerify(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	c.flow.RegisterVerify(w, r)
}

// handleLogout revokes the current session and clears the cookie, then returns to the
// login page. It is a CSRF-protected POST (the nav's Sign out button, form token).
func (c *Creator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	c.flow.RevokeCurrent(r)
	c.flow.ClearCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// handleLoginPage serves the creator login page (a single "sign in with passkey"
// action — usernameless, no fields). Standalone, pre-auth.
func (c *Creator) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	c.render(w, "login", map[string]any{"CSRF": c.csrf, "ShowMetrics": false})
}

func (c *Creator) handleLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	c.flow.WriteLoginOptions(w, r)
}

func (c *Creator) handleLoginVerify(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRFHeader(r) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	c.flow.LoginVerify(w, r)
}

// checkCSRFHeader validates the per-process CSRF token supplied as a header on a JSON
// ceremony request (forms use FormValue; fetch requests use this).
func (c *Creator) checkCSRFHeader(r *http.Request) bool {
	return r.Header.Get("X-CSRF-Token") == c.csrf
}

// siteName is a display label for the RP, from the effective build config.
func (c *Creator) siteName() string {
	if n := c.state().build.SiteName; n != "" {
		return n
	}
	return "pbcssg"
}
