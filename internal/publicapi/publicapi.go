// Package publicapi implements the public-origin dynamic endpoints (SPEC §7.3) served
// under the reserved /_pbc/ prefix: an explicitly-enumerated, input-validated,
// GPC-aware set of handlers that read/write the runtime store (app.db). The static
// bundle path never routes here, so the public serving path stays DB-free (§7.1);
// these are strictly additive endpoints, never live-rendered pages.
//
// B1 (scaffold) ships two read endpoints — a health check and approved-comment
// retrieval — proving the reserved-prefix routing and runtime-store access from the
// public listener. Member auth (register/login) and comment posting attach here next.
package publicapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// maxPathLen bounds the ?path= query parameter (a URL path of a content page).
const maxPathLen = 512

// Options configures the public dynamic layer.
type Options struct {
	// MemberOrigin, when non-empty, enables Community Member WebAuthn auth
	// (register/login/logout) on the public origin — the exact origin the site is
	// served on (e.g. https://example.com), from which the member RP ID is derived.
	// Empty leaves only the read-only endpoints (health, comments).
	MemberOrigin string
}

// api is the public dynamic handler. It holds the runtime store; the static site is
// served elsewhere and never reaches this handler.
type api struct {
	app     *appstore.Store
	mux     *http.ServeMux
	auth    *memberAuth    // nil when member auth is disabled (MemberOrigin empty)
	modAuth *moderatorAuth // nil when member auth is disabled; moderators share the public origin (§2.4)
}

// New builds the public dynamic handler over the runtime store. It is mounted under
// server.ReservedPrefix; app must be non-nil (the caller wires it only when a runtime
// store is configured). When opts.MemberOrigin is set, member auth endpoints are added
// and the origin is validated (an error is returned for a bad origin).
func New(app *appstore.Store, opts Options) (http.Handler, error) {
	a := &api{app: app, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /_pbc/health", a.handleHealth)
	a.mux.HandleFunc("GET /_pbc/comments", a.handleComments)
	a.registerAssetRoutes()
	if opts.MemberOrigin != "" {
		ma, err := newMemberAuth(app, opts.MemberOrigin)
		if err != nil {
			return nil, err
		}
		a.auth = ma
		a.registerMemberRoutes()

		// Moderators share the public origin with members (the RP-ID split, §2.4). The
		// auth endpoints are always present when the public origin is up — they grant
		// nothing without a moderator account, which only a creator-minted moderator
		// invite creates.
		mod, err := newModeratorAuth(app, opts.MemberOrigin)
		if err != nil {
			return nil, err
		}
		a.modAuth = mod
		a.registerModeratorAuthRoutes()
		a.registerModerateRoutes()
		a.registerModPasskeyRoutes()
		a.registerModInviteRoutes()
		a.registerModAccountRoutes()
	}
	return a, nil
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }

// gpcRequested reports whether the request carries the Global Privacy Control signal
// (Sec-GPC: 1). The site sells/shares nothing, so honoring it is a no-op — there is
// nothing to switch off — but handlers detect it per the standing GPC posture (§7.2).
func gpcRequested(r *http.Request) bool { return r.Header.Get("Sec-GPC") == "1" }

// handleHealth is a DB-free liveness check for the dynamic layer; it also reflects
// whether GPC was detected, so the signal path is observable.
func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "gpc": gpcRequested(r)})
}

// commentView is the public shape of a comment (approved, public-by-design). The internal
// account link is never exposed. ID and ParentID let the widget thread replies one level deep
// and target a reply/delete. Role carries the author's role snapshot ("moderator"/"creator")
// so the widget can badge staff (Moderator / Author); Mod is the same fact as a convenience
// boolean. Mine is true only for the requester's own comments — it is per-viewer, computed from
// the caller's session, never a stored property — and gates the reply-to-mine/delete controls.
// Deleted marks a tombstone (a root the author removed while replies remained); its body and
// alias are blank and the widget renders it as "[deleted]".
type commentView struct {
	ID        int64  `json:"id"`
	ParentID  int64  `json:"parentId,omitempty"` // 0 = root comment
	Alias     string `json:"alias"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`      // unix seconds
	Role      string `json:"role,omitempty"` // "moderator" | "creator"; omitted for members
	Mod       bool   `json:"mod"`
	Mine      bool   `json:"mine,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
}

// handleComments returns the approved comments for a content page (roots and their approved
// replies, flat — the widget builds the one-level tree from parentId). The body is
// user-generated: it is JSON-string-encoded here (safe in transit); any HTML display must
// escape it (the widget uses textContent), never inject it as markup. When the request carries
// a member/moderator session the caller's own comments are flagged mine, so the read is
// viewer-aware; it stays a safe GET (no state change, no Origin requirement).
func (a *api) handleComments(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" || !strings.HasPrefix(p, "/") || len(p) > maxPathLen {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	cs, err := a.app.CommentsByPage(p, appstore.CommentApproved)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	viewer := a.viewerAccountID(r) // 0 when signed out or auth disabled
	out := make([]commentView, 0, len(cs))
	for _, c := range cs {
		v := commentView{
			ID: c.ID, Alias: c.Alias, Body: c.Body,
			CreatedAt: c.CreatedAt.Unix(), Deleted: c.Deleted(),
		}
		if c.ParentID != nil {
			v.ParentID = *c.ParentID
		}
		if !c.Deleted() {
			if c.AuthorRole == appstore.RoleModerator || c.AuthorRole == appstore.RoleCreator {
				v.Mod = true
				v.Role = c.AuthorRole
			}
			if viewer != 0 && c.AccountID != nil && *c.AccountID == viewer {
				v.Mine = true
			}
		}
		out = append(out, v)
	}
	writeJSON(w, map[string]any{"path": p, "comments": out})
}

// viewerAccountID resolves the request's session (member first, then moderator) to an account
// id for marking "mine", or 0 when signed out or auth is disabled. It never fails the request —
// an anonymous read simply sees no comment as its own.
func (a *api) viewerAccountID(r *http.Request) int64 {
	if a.auth != nil {
		if acc, ok := a.resolveMemberSession(r); ok {
			return acc.ID
		}
	}
	if a.modAuth != nil {
		if acc, ok := a.resolveModeratorSession(r); ok {
			return acc.ID
		}
	}
	return 0
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache") // dynamic; never cached like the bundle
	_ = json.NewEncoder(w).Encode(v)
}
