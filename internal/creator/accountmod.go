package creator

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the account-moderation UI (SPEC §2.4): the second tab of
// the moderation section, over the member/moderator accounts in the runtime store. It
// lists them and composes the erasure/ban primitives — ban (flag + revoke sessions +
// optionally remove posts + burn the creating invite), un-ban, or erase (forget-me:
// anonymize or delete the account outright). Creator accounts are deliberately not
// listed or actionable here — they are admins, not a moderated population — so the UI
// cannot lock the operator out; the handlers enforce that server-side too.

// accountView is one row in the account-moderation list.
type accountView struct {
	ID          int64
	Handle      string // shortened opaque handle (display only)
	Alias       string // account's public comment display name ("" = anonymous) — ties a name to the account
	Role        string
	IsModerator bool   // drives the moderator-only controls (label, capabilities, revoke invites)
	Label       string // creator's private staff label (moderators only; never members)
	CanInvite   bool   // moderator elevated grant
	CanBan      bool   // moderator elevated grant
	InvitedBy   string // who invited this member (issuer's label / "owner"); "" when unknown or staff
	Banned      bool
	Comments    int    // comments still authored (anonymized ones are not counted)
	Created     string // YYYY-MM-DD
	LastSeen    string // YYYY-MM-DD
}

// handleShort trims the opaque user handle to a short, display-only prefix (it is not
// an identifier the moderator types; the full value is meaningless to a human).
func handleShort(h string) string {
	if len(h) > 10 {
		return h[:10] + "…"
	}
	return h
}

// handleModAccounts renders the account-moderation list.
func (c *Creator) handleModAccounts(w http.ResponseWriter, r *http.Request) {
	c.renderModAccounts(w, http.StatusOK, "", "")
}

// renderModAccounts lists the member/moderator accounts with an optional banner. When
// the runtime store is absent it renders the disabled notice instead of querying.
func (c *Creator) renderModAccounts(w http.ResponseWriter, code int, notice, errMsg string) {
	if !c.moderationEnabled() {
		if code != http.StatusOK {
			w.WriteHeader(code)
		}
		c.render(w, "modaccounts", map[string]any{"Disabled": true, "Notice": notice, "Error": errMsg})
		return
	}
	accounts, err := c.appDB.Accounts()
	if err != nil {
		http.Error(w, "accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	counts, err := c.appDB.CommentCountsByAccount()
	if err != nil {
		http.Error(w, "accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	invitedBy, err := c.appDB.InviterLabelByAccount()
	if err != nil {
		http.Error(w, "accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]accountView, 0, len(accounts))
	for _, a := range accounts {
		if a.Role == appstore.RoleCreator {
			continue // creators are admins, not a moderated population (§2.4)
		}
		isMod := a.Role == appstore.RoleModerator
		v := accountView{
			ID: a.ID, Handle: handleShort(a.UserHandle), Alias: a.Alias, Role: a.Role,
			IsModerator: isMod, Label: a.Label, CanInvite: a.CanInvite, CanBan: a.CanBan,
			Banned: a.Status == appstore.StatusBanned, Comments: counts[a.ID],
			Created: a.CreatedAt.Format("2006-01-02"), LastSeen: a.LastSeenAt.Format("2006-01-02"),
		}
		if !isMod {
			v.InvitedBy = invitedBy[a.ID] // provenance is shown for members
		}
		views = append(views, v)
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "modaccounts", map[string]any{"Accounts": views, "Notice": notice, "Error": errMsg})
}

// modAccount validates a mutation (store present, CSRF valid, {id} parses), loads the
// target account, and refuses a missing or creator account — writing the error
// response itself when any check fails. It returns the loaded account on success.
func (c *Creator) modAccount(w http.ResponseWriter, r *http.Request) (appstore.Account, bool) {
	if !c.moderationEnabled() {
		http.Error(w, "account moderation requires the runtime store", http.StatusNotFound)
		return appstore.Account{}, false
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return appstore.Account{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad account id", http.StatusBadRequest)
		return appstore.Account{}, false
	}
	acc, ok, err := c.appDB.AccountByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return appstore.Account{}, false
	}
	if !ok {
		http.Error(w, "no such account", http.StatusNotFound)
		return appstore.Account{}, false
	}
	if acc.Role == appstore.RoleCreator {
		// Belt-and-braces: the UI never offers this, but a crafted request must not be
		// able to ban or erase a creator and lock the operator out.
		http.Error(w, "creator accounts cannot be moderated here", http.StatusForbidden)
		return appstore.Account{}, false
	}
	return acc, true
}

// handleAccountBan bans an account: flags it, revokes its sessions, optionally removes
// its posts, and burns the invite that created it (§2.4). The "remove" checkbox chooses
// whether its comments are deleted too.
func (c *Creator) handleAccountBan(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.modAccount(w, r)
	if !ok {
		return
	}
	removeContent := r.FormValue("remove") != ""
	if err := c.appDB.BanAccount(acc.ID, removeContent); err != nil {
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not ban the account: "+err.Error())
		return
	}
	msg := "Account banned — its sessions are revoked and its invite is burned."
	if removeContent {
		msg = "Account banned and its comments removed — sessions revoked and invite burned."
	}
	c.renderModAccounts(w, http.StatusOK, msg, "")
}

// handleAccountUnban lifts a ban (status back to active). It does not restore the
// burned invite or any removed content — it only re-permits the account to sign in.
func (c *Creator) handleAccountUnban(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.modAccount(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetAccountStatus(acc.ID, appstore.StatusActive); err != nil {
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not un-ban the account: "+err.Error())
		return
	}
	c.renderModAccounts(w, http.StatusOK, "Account un-banned — it can sign in again. Its old invite stays burned.", "")
}

// handleAccountErase performs a "forget me" erasure: the account is deleted (its
// credentials and sessions cascade). The "delete" checkbox chooses whether its comments
// are deleted too or anonymized (link nulled, alias blanked, body kept).
func (c *Creator) handleAccountErase(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.modAccount(w, r)
	if !ok {
		return
	}
	deleteComments := r.FormValue("delete") != ""
	if err := c.appDB.ForgetAccount(acc.ID, !deleteComments); err != nil {
		if errors.Is(err, appstore.ErrNotFound) {
			c.renderModAccounts(w, http.StatusNotFound, "", "That account no longer exists.")
			return
		}
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not erase the account: "+err.Error())
		return
	}
	msg := "Account erased — its comments were anonymized (kept, but no longer linked)."
	if deleteComments {
		msg = "Account erased and its comments deleted."
	}
	c.renderModAccounts(w, http.StatusOK, msg, "")
}

// requireModeratorTarget is modAccount plus a moderator-only check — the label,
// capability, and revoke-invites controls apply only to moderator accounts.
func (c *Creator) requireModeratorTarget(w http.ResponseWriter, r *http.Request) (appstore.Account, bool) {
	acc, ok := c.modAccount(w, r)
	if !ok {
		return appstore.Account{}, false
	}
	if acc.Role != appstore.RoleModerator {
		http.Error(w, "these controls apply to moderator accounts only", http.StatusBadRequest)
		return appstore.Account{}, false
	}
	return acc, true
}

// handleAccountCapabilities sets a moderator's elevated grants (issue member invites,
// soft-ban members) from the two checkboxes — the per-moderator delegation, default off.
func (c *Creator) handleAccountCapabilities(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.requireModeratorTarget(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetAccountCapabilities(acc.ID, r.FormValue("can_invite") != "", r.FormValue("can_ban") != ""); err != nil {
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not update the moderator's permissions: "+err.Error())
		return
	}
	c.renderModAccounts(w, http.StatusOK, "Updated the moderator's permissions.", "")
}

// handleAccountLabel sets a moderator's private staff label (how you tell moderators
// apart in this list). It is operator-only metadata, never shown publicly (§2.4).
func (c *Creator) handleAccountLabel(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.requireModeratorTarget(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetAccountLabel(acc.ID, r.FormValue("label")); err != nil {
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not set the label: "+err.Error())
		return
	}
	c.renderModAccounts(w, http.StatusOK, "Updated the moderator's label.", "")
}

// handleAccountRevokeInvites burns all of a moderator's outstanding invites — the cleanup
// when a moderator is caught farming accounts (§2.4). Accounts already created from those
// invites are handled separately (ban/erase).
func (c *Creator) handleAccountRevokeInvites(w http.ResponseWriter, r *http.Request) {
	acc, ok := c.requireModeratorTarget(w, r)
	if !ok {
		return
	}
	n, err := c.appDB.RevokeInvitesIssuedBy(acc.ID)
	if err != nil {
		c.renderModAccounts(w, http.StatusBadRequest, "", "Could not revoke the moderator's invites: "+err.Error())
		return
	}
	c.renderModAccounts(w, http.StatusOK, fmt.Sprintf("Revoked %d outstanding invite(s) from this moderator.", n), "")
}
