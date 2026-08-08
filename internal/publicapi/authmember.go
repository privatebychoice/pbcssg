package publicapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/authflow"
)

const (
	memberChallengeTTL = 2 * time.Minute
	// memberSessionTTL is generous — Community Members are low-privilege and a shorter
	// window would just mean frequent re-taps; a fresh WebAuthn assertion renews it.
	memberSessionTTL = 30 * 24 * time.Hour

	memberCookieHost = "__Host-pbcssg_member"
	memberCookieDev  = "pbcssg_member"
)

// memberAuth holds the Community Member WebAuthn state on the public origin — a
// separate credential domain from creators (the per-origin RP-ID split, §2.4). The
// ceremony/session logic lives in the shared authflow.Flow; memberAuth adds only the
// public-origin specifics: the exact origin (for the Origin-header CSRF check).
type memberAuth struct {
	flow   *authflow.Flow
	origin string
}

func newMemberAuth(app *appstore.Store, origin string) (*memberAuth, error) {
	rpID, err := rpIDFromOrigin(origin)
	if err != nil {
		return nil, fmt.Errorf("publicapi: member origin: %w", err)
	}
	secure := strings.HasPrefix(origin, "https://")
	name := memberCookieDev
	if secure {
		name = memberCookieHost
	}
	flow := authflow.New(authflow.Config{
		Store:        app,
		Verifier:     authflow.NewVerifier(rpID, origin),
		Role:         appstore.RoleMember,
		RPName:       rpID, // members see the site host as the RP label
		UserName:     "member",
		UserDisplay:  "Community Member",
		CookieName:   name,
		CookieSecure: secure,
		SessionTTL:   memberSessionTTL,
		ChallengeTTL: memberChallengeTTL,
		LogPrefix:    "publicapi member",
	})
	return &memberAuth{flow: flow, origin: origin}, nil
}

// rpIDFromOrigin extracts the WebAuthn RP ID (registrable host, no scheme/port) from
// an absolute https origin (http allowed only for localhost, a secure context).
func rpIDFromOrigin(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", origin, err)
	}
	host := u.Hostname()
	if u.Scheme == "" || host == "" {
		return "", fmt.Errorf("origin %q must be absolute like https://example.com", origin)
	}
	isLocalhost := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if u.Scheme != "https" && !(u.Scheme == "http" && isLocalhost) {
		return "", fmt.Errorf("origin %q must be https (http only for localhost)", origin)
	}
	return host, nil
}

func (a *api) registerMemberRoutes() {
	a.mux.HandleFunc("POST /_pbc/auth/register/options", a.handleMemberRegisterOptions)
	a.mux.HandleFunc("POST /_pbc/auth/register/verify", a.handleMemberRegisterVerify)
	a.mux.HandleFunc("POST /_pbc/auth/login/options", a.handleMemberLoginOptions)
	a.mux.HandleFunc("POST /_pbc/auth/login/verify", a.handleMemberLoginVerify)
	a.mux.HandleFunc("POST /_pbc/auth/logout", a.handleMemberLogout)
	a.mux.HandleFunc("GET /_pbc/auth/me", a.handleMemberMe)
	// Comment posting requires a member (or moderator) session, so it is registered with
	// member auth. The delete route lets an author remove their own comment.
	a.mux.HandleFunc("POST /_pbc/comments", a.handleCommentPost)
	a.mux.HandleFunc("POST /_pbc/comments/{id}/delete", a.handleCommentDelete)
	// Member self-service (§2.4, B4): set the account display name (unique, back-filled across
	// one's comments), and self-erase ("forget me").
	a.mux.HandleFunc("POST /_pbc/account/alias", a.handleMemberAlias)
	a.mux.HandleFunc("POST /_pbc/account/forget", a.handleMemberForget)
}

// checkOrigin is the CSRF defense for state-changing member endpoints: the request's
// Origin header must equal the configured public origin. The WebAuthn /verify steps are
// additionally bound to the origin by the ceremony itself; this guards the
// options/logout/self-service POSTs a cross-site page could otherwise trigger.
func (a *api) checkOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == a.auth.origin
}

// resolveMemberSession resolves the member session cookie to a live member account.
func (a *api) resolveMemberSession(r *http.Request) (appstore.Account, bool) {
	return a.auth.flow.Resolve(r)
}

// --- ceremonies (thin wrappers: Origin-CSRF, then the shared flow) --------------

func (a *api) handleMemberRegisterOptions(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.auth.flow.WriteRegisterOptions(w, r)
}

func (a *api) handleMemberRegisterVerify(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.auth.flow.RegisterVerify(w, r)
}

func (a *api) handleMemberLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.auth.flow.WriteLoginOptions(w, r)
}

func (a *api) handleMemberLoginVerify(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.auth.flow.LoginVerify(w, r)
}

func (a *api) handleMemberLogout(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	a.auth.flow.RevokeCurrent(r)
	a.auth.flow.ClearCookie(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *api) handleMemberMe(w http.ResponseWriter, r *http.Request) {
	if acc, ok := a.resolveMemberSession(r); ok {
		// The account row is the source of truth for the display name now (§2.4), so no
		// per-comment lookup is needed.
		writeJSON(w, map[string]any{"authenticated": true, "alias": acc.Alias})
		return
	}
	writeJSON(w, map[string]any{"authenticated": false})
}

// Comment input bounds.
const (
	maxCommentBody  = 4096
	maxCommentAlias = 64
)

// handleCommentPost lets an authenticated member or moderator post a comment or a reply.
// A member's comment starts pending (nothing is public until approved — §2.4); a moderator's
// is auto-approved and snapshotted with role=moderator (the staff badge). The display name is
// the poster's single account alias, never a per-post value — the request's alias field is
// ignored, so the one-name-per-account rule can't be bypassed by typing a different name on a
// post. A parentId makes it a reply (one level deep; the store derives the reply's page from
// the parent). Requires a session and the Origin CSRF check; input is length-bounded.
func (a *api) handleCommentPost(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	// The member cookie is checked first; a moderator carries only the moderator cookie.
	acc, ok := a.resolveMemberSession(r)
	if !ok {
		acc, ok = a.resolveModeratorSession(r)
	}
	if !ok {
		http.Error(w, "sign in to comment", http.StatusUnauthorized)
		return
	}
	var req struct {
		Path     string `json:"path"`
		Body     string `json:"body"`
		ParentID int64  `json:"parentId"` // 0 = root comment; >0 = reply target
	}
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || len(req.Body) > maxCommentBody {
		http.Error(w, fmt.Sprintf("comment must be 1..%d characters", maxCommentBody), http.StatusBadRequest)
		return
	}

	var created appstore.Comment
	if req.ParentID > 0 {
		// Reply: the store validates the parent (must exist and be a root) and inherits its
		// page path. A failure here is a bad/ineligible parent — a client error.
		c, err := a.app.AddReply(acc.ID, req.ParentID, acc.Alias, req.Body)
		if err != nil {
			log.Printf("INFO publicapi: add reply: %v", err)
			http.Error(w, "cannot reply to that comment", http.StatusBadRequest)
			return
		}
		created = c
	} else {
		if req.Path == "" || !strings.HasPrefix(req.Path, "/") || len(req.Path) > maxPathLen {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		c, err := a.app.AddComment(acc.ID, req.Path, acc.Alias, req.Body)
		if err != nil {
			log.Printf("ERROR publicapi: add comment: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		created = c
	}
	// Report the real status: a member sees "pending" (awaiting review), staff see "approved".
	writeJSON(w, map[string]any{"ok": true, "status": created.Status})
}

// handleCommentDelete lets an author remove their own comment (member or moderator session).
// Ownership and the leaf-vs-tombstone decision live in the store; ErrNotFound (a missing id or
// someone else's comment, indistinguishable by design) maps to 404 so ids can't be probed.
func (a *api) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	acc, ok := a.resolveMemberSession(r)
	if !ok {
		acc, ok = a.resolveModeratorSession(r)
	}
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	tombstoned, err := a.app.DeleteOwnComment(acc.ID, id)
	if errors.Is(err, appstore.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("ERROR publicapi: delete own comment: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "tombstoned": tombstoned})
}

// handleMemberAlias sets the signed-in member's (or moderator's) single account display name
// and back-fills it across all their comments (§2.4). The name must be unique across accounts,
// compared case-insensitively — a collision returns 409 so the caller can prompt for another —
// and the empty alias ("anonymous") is always allowed. Changing it frees the previous name.
func (a *api) handleMemberAlias(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	acc, ok := a.resolveMemberSession(r)
	if !ok {
		acc, ok = a.resolveModeratorSession(r)
	}
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	var req struct {
		Alias string `json:"alias"`
	}
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	req.Alias = strings.TrimSpace(req.Alias)
	if len(req.Alias) > maxCommentAlias {
		http.Error(w, "alias too long", http.StatusBadRequest)
		return
	}
	n, err := a.app.SetAccountAlias(acc.ID, req.Alias)
	if errors.Is(err, appstore.ErrAliasTaken) {
		http.Error(w, "that name is already taken", http.StatusConflict)
		return
	}
	if errors.Is(err, appstore.ErrAliasRateLimited) {
		http.Error(w, "you've changed your name too many times today — try again tomorrow", http.StatusTooManyRequests)
		return
	}
	if err != nil {
		log.Printf("ERROR publicapi: set alias: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "updated": n, "alias": req.Alias})
}

// handleMemberForget self-erases the signed-in member's account ("forget me", §2.4):
// deleteComments chooses whether their comments are deleted too or anonymized (kept,
// unlinked, alias blanked — the default). The account and its credentials/sessions are
// removed, so the member cannot sign back in; the cookie is cleared.
func (a *api) handleMemberForget(w http.ResponseWriter, r *http.Request) {
	if !a.checkOrigin(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	acc, ok := a.resolveMemberSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	var req struct {
		DeleteComments bool `json:"deleteComments"`
	}
	if !authflow.DecodeJSON(w, r, &req) {
		return
	}
	if err := a.app.ForgetAccount(acc.ID, !req.DeleteComments); err != nil && !errors.Is(err, appstore.ErrNotFound) {
		log.Printf("ERROR publicapi: member forget: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.auth.flow.ClearCookie(w)
	log.Printf("INFO publicapi: member account %d self-erased (deleteComments=%v)", acc.ID, req.DeleteComments)
	writeJSON(w, map[string]bool{"ok": true})
}
