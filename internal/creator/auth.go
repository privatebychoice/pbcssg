package creator

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
)

const (
	// challengeTTL bounds how long a registration/login ceremony may stay in flight —
	// long enough for the user-verification gesture, short enough to limit replay.
	challengeTTL = 2 * time.Minute
	// sessionTTL is how long a creator session lasts before a fresh WebAuthn re-tap is
	// required (§2.4: short TTL + re-tap, not a long-lived refresh token).
	sessionTTL = 12 * time.Hour

	// Session cookie names. The `__Host-` prefix (HTTPS/admin origin) hardens the cookie
	// to a single origin with Path=/ and Secure; the dev name is used over http://localhost
	// where Secure/`__Host-` cannot apply.
	sessionCookieHost = "__Host-pbcssg_session"
	sessionCookieDev  = "pbcssg_session"
)

// initAuth wires creator passkey auth (§2.4) when a runtime store is configured. It
// derives the WebAuthn RP ID from the admin origin and prepares the verifier and the
// in-memory challenge store. When Config.AppStore is nil (standalone creator, or a
// unified launch without -app-db) auth stays off and the admin is protected by its
// network controls only (§7.9).
func (c *Creator) initAuth(cfg Config) error {
	if cfg.AppStore == nil {
		return nil
	}
	rpID, err := deriveRPID(cfg.AdminOrigin)
	if err != nil {
		return fmt.Errorf("creator: admin origin for passkey auth: %w", err)
	}
	c.appDB = cfg.AppStore

	// Cookie hardening follows the origin scheme (validated in deriveRPID).
	c.cookieSecure = strings.HasPrefix(cfg.AdminOrigin, "https://")
	c.cookieName = sessionCookieDev
	if c.cookieSecure {
		c.cookieName = sessionCookieHost
	}

	// The shared ceremony/session core, parameterized for the creator role + admin origin.
	// rp.name is a display label only (rp.id binds the credential), so fixing it at init
	// from the site name is fine.
	c.flow = authflow.New(authflow.Config{
		Store:        cfg.AppStore,
		Verifier:     authflow.NewVerifier(rpID, cfg.AdminOrigin),
		Role:         appstore.RoleCreator,
		RPName:       c.siteName(),
		UserName:     "creator",
		UserDisplay:  "Creator",
		CookieName:   c.cookieName,
		CookieSecure: c.cookieSecure,
		SessionTTL:   sessionTTL,
		ChallengeTTL: challengeTTL,
		LogPrefix:    "creator",
	})
	return nil
}

// setSessionCookie / clearSessionCookie delegate to the shared flow (HttpOnly +
// SameSite=Strict always, Secure + `__Host-` on an https admin origin — §2.4).
func (c *Creator) setSessionCookie(w http.ResponseWriter, token string) { c.flow.SetCookie(w, token) }
func (c *Creator) clearSessionCookie(w http.ResponseWriter)             { c.flow.ClearCookie(w) }

// authEnabled reports whether creator passkey auth is wired (a runtime store was
// provided). Ceremony endpoints and, later, the login gate are active only then.
func (c *Creator) authEnabled() bool { return c.appDB != nil }

// deriveRPID extracts the WebAuthn RP ID (the registrable host, no scheme or port)
// from the configured admin origin. The RP ID must be a host, never an IP or a bare
// port, and the origin must be an absolute https URL (or http://localhost… for local
// development, which browsers treat as a secure context).
func deriveRPID(origin string) (string, error) {
	if origin == "" {
		return "", fmt.Errorf("admin origin is required when a runtime store is configured")
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", origin, err)
	}
	host := u.Hostname() // strips any :port
	if u.Scheme == "" || host == "" {
		return "", fmt.Errorf("admin origin %q must be an absolute URL like https://admin.example.com", origin)
	}
	isLocalhost := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if u.Scheme != "https" && !(u.Scheme == "http" && isLocalhost) {
		return "", fmt.Errorf("admin origin %q must be https (http is allowed only for localhost)", origin)
	}
	return host, nil
}
