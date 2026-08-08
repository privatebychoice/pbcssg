package creator

import (
	"net/http"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the admin invite manager (SPEC §2.4): a signed-in creator
// mints single-use registration invites (choosing the granted role and an expiry),
// sees the outstanding ones, and revokes a live one — the editor equivalent of the
// `pbcssg admin bootstrap` CLI. The plaintext code exists only once, at mint, and is
// shown then and never again (only its hash is stored); the list identifies an invite
// by its opaque lineage, which is what Revoke acts on.

// inviteTTLs maps the mint form's expiry choice to a duration (0 = no expiry). The
// order here is the order rendered in the <select>.
var inviteTTLs = []struct {
	Key   string
	Label string
	TTL   time.Duration
}{
	{"1h", "1 hour", time.Hour},
	{"24h", "1 day", 24 * time.Hour},
	{"168h", "1 week", 7 * 24 * time.Hour},
	{"720h", "30 days", 30 * 24 * time.Hour},
	{"0", "No expiry", 0},
}

// inviteTTL resolves a form expiry key to a duration, defaulting to 1 day for an
// unknown value.
func inviteTTL(key string) time.Duration {
	for _, o := range inviteTTLs {
		if o.Key == key {
			return o.TTL
		}
	}
	return 24 * time.Hour
}

// inviteRoles is the set of roles a creator may grant. Members register on the public
// origin, moderators/creators on the admin origin; the invite carries the role.
var inviteRoles = []string{appstore.RoleMember, appstore.RoleModerator, appstore.RoleCreator}

// inviteView is one row in the invite list.
type inviteView struct {
	Lineage string
	Role    string
	Status  string // active | used | expired | revoked
	Live    bool   // active and revocable
	Created string
	Expires string // "never" when no expiry
}

// inviteStatus derives an invite's lifecycle status at time now.
func inviteStatus(inv appstore.Invite, now time.Time) (status string, live bool) {
	switch {
	case !inv.RevokedAt.IsZero():
		return "revoked", false
	case !inv.RedeemedAt.IsZero():
		return "used", false
	case !inv.ExpiresAt.IsZero() && !inv.ExpiresAt.After(now):
		return "expired", false
	default:
		return "active", true
	}
}

// handleInvites renders the invite manager.
func (c *Creator) handleInvites(w http.ResponseWriter, r *http.Request) {
	c.renderInvites(w, http.StatusOK, "", "", "")
}

// renderInvites lists invites with an optional banner. mintedCode, when non-empty, is a
// just-minted plaintext code shown once in a copy box.
func (c *Creator) renderInvites(w http.ResponseWriter, code int, notice, errMsg, mintedCode string) {
	if !c.authEnabled() {
		if code != http.StatusOK {
			w.WriteHeader(code)
		}
		c.render(w, "invites", map[string]any{"Disabled": true, "Notice": notice, "Error": errMsg})
		return
	}
	invites, err := c.appDB.Invites()
	if err != nil {
		http.Error(w, "invites: "+err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	views := make([]inviteView, 0, len(invites))
	for _, inv := range invites {
		status, live := inviteStatus(inv, now)
		expires := "never"
		if !inv.ExpiresAt.IsZero() {
			expires = inv.ExpiresAt.Format("2006-01-02 15:04")
		}
		views = append(views, inviteView{
			Lineage: inv.Lineage, Role: inv.Role, Status: status, Live: live,
			Created: inv.CreatedAt.Format("2006-01-02"), Expires: expires,
		})
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	// Where a moderator signs in — the public site's /_pbc/moderate. Built from the
	// configured base URL so it names this site (never hardcoded), with a relative
	// fallback when no base URL is set.
	moderateURL := "/_pbc/moderate"
	if base := strings.TrimRight(c.state().build.BaseURL, "/"); base != "" {
		moderateURL = base + "/_pbc/moderate"
	}
	c.render(w, "invites", map[string]any{
		"CSRF": c.csrf, "Invites": views, "Roles": inviteRoles, "TTLs": inviteTTLs,
		"MintedCode": mintedCode, "ModerateURL": moderateURL, "Notice": notice, "Error": errMsg,
	})
}

// handleInviteMint mints a single-use invite for the chosen role and expiry, then
// re-renders with the plaintext code shown once.
func (c *Creator) handleInviteMint(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	role := r.FormValue("role")
	if !validInviteRole(role) {
		c.renderInvites(w, http.StatusBadRequest, "", "Choose a role for the invite.", "")
		return
	}
	// Attribute the invite to the creator (provenance) and carry a label — the label
	// seeds a moderator/creator account on redeem so staff are identifiable (members
	// stay unlabeled; §2.4).
	code, _, err := c.appDB.MintInvite(appstore.MintParams{
		Role: role, TTL: inviteTTL(r.FormValue("ttl")),
		IssuedBy: acc.ID, Label: r.FormValue("label"),
	})
	if err != nil {
		c.renderInvites(w, http.StatusBadRequest, "", "Could not mint the invite: "+err.Error(), "")
		return
	}
	c.renderInvites(w, http.StatusOK,
		"Invite minted for a "+role+" account. Copy the code now — it will not be shown again.", "", code)
}

// handleInviteRevoke revokes a live invite by its lineage (the non-secret id shown in
// the list; the code itself is not recoverable). Revoking a non-live invite is a no-op.
func (c *Creator) handleInviteRevoke(w http.ResponseWriter, r *http.Request) {
	if !c.authEnabled() {
		http.NotFound(w, r)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	lineage := r.FormValue("lineage")
	if lineage == "" {
		c.renderInvites(w, http.StatusBadRequest, "", "Missing invite reference.", "")
		return
	}
	if err := c.appDB.RevokeInviteByLineage(lineage); err != nil {
		c.renderInvites(w, http.StatusBadRequest, "", "Could not revoke the invite: "+err.Error(), "")
		return
	}
	c.renderInvites(w, http.StatusOK, "Invite revoked — the code can no longer be redeemed.", "", "")
}

// validInviteRole reports whether role is one a creator may grant.
func validInviteRole(role string) bool {
	for _, r := range inviteRoles {
		if r == role {
			return true
		}
	}
	return false
}
