package publicapi

import (
	"html/template"
	"log"
	"net/http"
	"strconv"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the moderator's account-moderation page (§2.4), gated by the can_ban
// grant. A moderator can soft-ban a MEMBER (flag + revoke sessions) and lift the ban —
// nothing heavier. Moderators and creators are never listed or actionable here: a
// moderator cannot ban another moderator or the operator, and the "final" erase stays
// creator-only. The soft ban is reversible; the creator's ban/erase (with invite-burn,
// post-removal, or deletion) remains a separate, higher-privilege surface.

type modAccountView struct {
	ID       int64
	Handle   string
	Alias    string // public comment display name ("" = anonymous) — de-blinds which account is which
	Banned   bool
	Comments int
	Created  string
	LastSeen string
}

type modAccountsData struct {
	Label    string
	Accounts []modAccountView
	Notice   string
	Error    string
}

func (a *api) registerModAccountRoutes() {
	a.mux.HandleFunc("GET /_pbc/mod/accounts", a.handleModAccounts)
	a.mux.HandleFunc("POST /_pbc/mod/accounts/{id}/ban", a.handleModAccountBan)
	a.mux.HandleFunc("POST /_pbc/mod/accounts/{id}/unban", a.handleModAccountUnban)
}

// handleShort trims the opaque user handle to a short, display-only prefix.
func handleShort(h string) string {
	if len(h) > 10 {
		return h[:10] + "…"
	}
	return h
}

// requireBanner resolves the moderator session and confirms the can_ban grant, writing
// the response itself on failure (redirect to sign in, or a 403 page).
func (a *api) requireBanner(w http.ResponseWriter, r *http.Request) (appstore.Account, bool) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Redirect(w, r, "/_pbc/moderate", http.StatusSeeOther)
		return appstore.Account{}, false
	}
	if !acc.CanBan {
		setModeratePageHeaders(w, "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_ = modAccountsTmpl.Execute(w, modAccountsData{Label: acc.Label,
			Error: "You don't have permission to ban members. Ask the site owner to enable it for your account."})
		return appstore.Account{}, false
	}
	return acc, true
}

func (a *api) handleModAccounts(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.requireBanner(w, r)
	if !ok {
		return
	}
	a.renderModAccounts(w, acc, http.StatusOK, "", "")
}

func (a *api) renderModAccounts(w http.ResponseWriter, acc appstore.Account, code int, notice, errMsg string) {
	accounts, err := a.app.Accounts()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	counts, err := a.app.CommentCountsByAccount()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	views := make([]modAccountView, 0, len(accounts))
	for _, m := range accounts {
		if m.Role != appstore.RoleMember {
			continue // moderators may act on members only, never on staff (§2.4)
		}
		views = append(views, modAccountView{
			ID: m.ID, Handle: handleShort(m.UserHandle), Alias: m.Alias,
			Banned: m.Status == appstore.StatusBanned, Comments: counts[m.ID],
			Created: m.CreatedAt.Format("2006-01-02"), LastSeen: m.LastSeenAt.Format("2006-01-02"),
		})
	}
	setModeratePageHeaders(w, "text/html; charset=utf-8")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_ = modAccountsTmpl.Execute(w, modAccountsData{Label: acc.Label, Accounts: views, Notice: notice, Error: errMsg})
}

// modBanTarget loads the {id} target and confirms it is an actionable MEMBER — never a
// moderator or creator (a moderator cannot ban staff), writing the error itself on failure.
func (a *api) modBanTarget(w http.ResponseWriter, r *http.Request, actor appstore.Account) (appstore.Account, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad account id", http.StatusBadRequest)
		return appstore.Account{}, false
	}
	target, ok, err := a.app.AccountByID(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return appstore.Account{}, false
	}
	if !ok {
		a.renderModAccounts(w, actor, http.StatusNotFound, "", "That account no longer exists.")
		return appstore.Account{}, false
	}
	if target.Role != appstore.RoleMember {
		// A moderator may act on members only — never another moderator or the creator.
		http.Error(w, "only member accounts can be moderated here", http.StatusForbidden)
		return appstore.Account{}, false
	}
	return target, true
}

func (a *api) handleModAccountBan(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.requireBanner(w, r)
	if !ok {
		return
	}
	target, ok := a.modBanTarget(w, r, actor)
	if !ok {
		return
	}
	if err := a.app.SoftBanAccount(target.ID); err != nil {
		a.renderModAccounts(w, actor, http.StatusBadRequest, "", "Could not ban the account.")
		return
	}
	log.Printf("INFO publicapi moderator: account %d soft-banned member %d", actor.ID, target.ID)
	a.renderModAccounts(w, actor, http.StatusOK, "Member banned — their sessions are revoked and they can't sign in. Their comments are untouched; the creator can erase them.", "")
}

func (a *api) handleModAccountUnban(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.requireBanner(w, r)
	if !ok {
		return
	}
	target, ok := a.modBanTarget(w, r, actor)
	if !ok {
		return
	}
	if err := a.app.SetAccountStatus(target.ID, appstore.StatusActive); err != nil {
		a.renderModAccounts(w, actor, http.StatusBadRequest, "", "Could not un-ban the account.")
		return
	}
	log.Printf("INFO publicapi moderator: account %d un-banned member %d", actor.ID, target.ID)
	a.renderModAccounts(w, actor, http.StatusOK, "Member un-banned — they can sign in again.", "")
}

var modAccountsTmpl = template.Must(template.New("modaccounts").Parse(modAccountsHTML))
