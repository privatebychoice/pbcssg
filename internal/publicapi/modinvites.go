package publicapi

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the moderator's member-invite page (§2.4). A moderator granted the
// can_invite capability mints member invites — always member-role, a fixed 30-day
// expiry, capped per moderator, and attributed to them (issued_by) — and sees/revokes
// their own outstanding ones. The cap and attribution are the anti-bot controls: a
// moderator can only hold ModeratorOutstandingInviteCap live invites, and every account
// they let in traces back to them for the creator's accountability view.

type modInviteView struct {
	Lineage string
	Status  string // active | used | expired | revoked
	Live    bool
	Created string
	Expires string
}

type modInvitesData struct {
	Label       string
	Outstanding int
	Cap         int
	AtCap       bool
	Invites     []modInviteView
	MintedCode  string
	Notice      string
	Error       string
}

func (a *api) registerModInviteRoutes() {
	a.mux.HandleFunc("GET /_pbc/mod/invites", a.handleModInvites)
	a.mux.HandleFunc("POST /_pbc/mod/invites/mint", a.handleModInviteMint)
	a.mux.HandleFunc("POST /_pbc/mod/invites/revoke", a.handleModInviteRevoke)
}

// modInviteStatus derives an invite's lifecycle status at now.
func modInviteStatus(inv appstore.Invite, now time.Time) (string, bool) {
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

// requireInviter resolves the moderator session and confirms the can_invite grant,
// writing the response itself on failure: a redirect to sign in when there's no session,
// a 403 page when the moderator lacks the grant.
func (a *api) requireInviter(w http.ResponseWriter, r *http.Request) (appstore.Account, bool) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Redirect(w, r, "/_pbc/moderate", http.StatusSeeOther)
		return appstore.Account{}, false
	}
	if !acc.CanInvite {
		setModeratePageHeaders(w, "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_ = modInvitesTmpl.Execute(w, modInvitesData{Label: acc.Label, Cap: appstore.ModeratorOutstandingInviteCap,
			Error: "You don't have permission to issue invites. Ask the site owner to enable it for your account."})
		return appstore.Account{}, false
	}
	return acc, true
}

func (a *api) handleModInvites(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.requireInviter(w, r)
	if !ok {
		return
	}
	a.renderModInvites(w, acc, http.StatusOK, "", "", "")
}

// renderModInvites lists the moderator's own invites with an optional banner. mintedCode,
// when set, is a just-minted code shown once.
func (a *api) renderModInvites(w http.ResponseWriter, acc appstore.Account, code int, notice, errMsg, mintedCode string) {
	invites, err := a.app.InvitesIssuedBy(acc.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	outstanding := 0
	views := make([]modInviteView, 0, len(invites))
	for _, inv := range invites {
		status, live := modInviteStatus(inv, now)
		if live {
			outstanding++
		}
		expires := "never"
		if !inv.ExpiresAt.IsZero() {
			expires = inv.ExpiresAt.Format("2006-01-02 15:04")
		}
		views = append(views, modInviteView{
			Lineage: inv.Lineage, Status: status, Live: live,
			Created: inv.CreatedAt.Format("2006-01-02"), Expires: expires,
		})
	}
	setModeratePageHeaders(w, "text/html; charset=utf-8")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_ = modInvitesTmpl.Execute(w, modInvitesData{
		Label: acc.Label, Outstanding: outstanding, Cap: appstore.ModeratorOutstandingInviteCap,
		AtCap:   outstanding >= appstore.ModeratorOutstandingInviteCap,
		Invites: views, MintedCode: mintedCode, Notice: notice, Error: errMsg,
	})
}

func (a *api) handleModInviteMint(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.requireInviter(w, r)
	if !ok {
		return
	}
	code, _, err := a.app.MintMemberInviteByModerator(acc.ID)
	if err != nil {
		if errors.Is(err, appstore.ErrInviteQuota) {
			a.renderModInvites(w, acc, http.StatusBadRequest, "", "You've reached your limit of outstanding invites. Revoke an unused one first.", "")
			return
		}
		log.Printf("ERROR publicapi moderator: mint invite: %v", err)
		a.renderModInvites(w, acc, http.StatusInternalServerError, "", "Could not mint the invite.", "")
		return
	}
	log.Printf("INFO publicapi moderator: account %d minted a member invite", acc.ID)
	a.renderModInvites(w, acc, http.StatusOK,
		"Member invite created — it expires in 30 days. Copy the code now; it won't be shown again.", "", code)
}

func (a *api) handleModInviteRevoke(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.requireInviter(w, r)
	if !ok {
		return
	}
	lineage := r.FormValue("lineage")
	if lineage == "" {
		a.renderModInvites(w, acc, http.StatusBadRequest, "", "Missing invite reference.", "")
		return
	}
	if err := a.app.RevokeOwnInvite(lineage, acc.ID); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			a.renderModInvites(w, acc, http.StatusNotFound, "", "That invite is no longer live.", "")
			return
		}
		a.renderModInvites(w, acc, http.StatusBadRequest, "", "Could not revoke the invite.", "")
		return
	}
	a.renderModInvites(w, acc, http.StatusOK, "Invite revoked — the code can no longer be redeemed.", "", "")
}

var modInvitesTmpl = template.Must(template.New("modinvites").Parse(modInvitesHTML))
