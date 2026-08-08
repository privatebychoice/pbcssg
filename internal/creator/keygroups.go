package creator

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// This file is the editor's key-group manager (SPEC §6.10): the named groups whose
// KEK unlocks group-gated content blocks. It is managed state like the classification
// dataset (§6.8) — create/rename/delete/rotate a group, associate a splash page, and
// copy the group's gate link (<splash-path>#k=<KEK>). The KEK is server-only here; it
// reaches a visitor only via the gate-link fragment their browser deposits.

// keyGroupView is one group rendered in the manager: its identity, the optional
// splash page, and the ready-to-copy gate link.
type keyGroupView struct {
	ID         int64
	Alias      string
	SplashPath string // path of the associated splash page, "" if none
	SplashID   int64  // 0 if none (drives the <select> current value)
	GateLink   string // full gate link (always available: splash page, or the generic /unlock/<alias> fallback)
	LocalLink  string // same link against the local server test URL, "" when none is configured
	Generic    bool   // true when the link targets the generic fallback (no custom splash set)
}

// gateKEKFragment encodes a KEK as the URL fragment carried in a gate link. It uses
// unpadded base64url so the value is fragment-safe (no +,/,= that need escaping); the
// client's decoder accepts both base64url and standard base64.
func gateKEKFragment(kek []byte) string {
	return "#k=" + base64.RawURLEncoding.EncodeToString(kek)
}

// keyGroupViews builds the manager rows from the store, resolving each group's splash
// page path and gate link. pagePaths maps page id → path for splash resolution.
func (c *Creator) keyGroupViews() ([]keyGroupView, map[int64]string, error) {
	groups, err := c.store.KeyGroups()
	if err != nil {
		return nil, nil, err
	}
	pages, err := c.store.Pages()
	if err != nil {
		return nil, nil, err
	}
	pagePaths := make(map[int64]string, len(pages))
	for _, p := range pages {
		pagePaths[p.ID] = p.Path
	}
	base := c.state().build.BaseURL
	localBase := c.localTestURL() // "" when the operator hasn't configured a local server
	out := make([]keyGroupView, 0, len(groups))
	for _, g := range groups {
		v := keyGroupView{ID: g.ID, Alias: g.Alias}
		frag := gateKEKFragment(g.KEK)
		// The path is the same whether the link targets production or the local server;
		// only the base origin differs, so the local test link exercises the same flow.
		var path string
		if g.SplashPageID != nil && pagePaths[*g.SplashPageID] != "" {
			v.SplashID = *g.SplashPageID
			v.SplashPath = pagePaths[*g.SplashPageID]
			path = v.SplashPath
		} else {
			// No authored splash: the build emits a generic deposit page at this path,
			// so a gate link is always available (§6.10).
			v.Generic = true
			path = build.GateFallbackPath(g.Alias)
		}
		v.GateLink = base + path + frag
		if localBase != "" {
			v.LocalLink = localBase + path + frag
		}
		out = append(out, v)
	}
	return out, pagePaths, nil
}

// handleKeyGroups renders the key-group manager.
func (c *Creator) handleKeyGroups(w http.ResponseWriter, r *http.Request) {
	c.renderKeyGroups(w, http.StatusOK, "", "")
}

// renderKeyGroups renders the manager page with the current groups and page list.
func (c *Creator) renderKeyGroups(w http.ResponseWriter, code int, notice, errMsg string) {
	views, _, err := c.keyGroupViews()
	if err != nil {
		http.Error(w, "key groups: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pages, err := c.store.Pages()
	if err != nil {
		http.Error(w, "key groups: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "keygroups", map[string]any{
		"CSRF": c.csrf, "Groups": views, "Pages": pages,
		"LocalTestURL": c.localTestURL(), "Notice": notice, "Error": errMsg,
	})
}

// handleKeyGroupCreate creates a new group from a (normalized) alias.
func (c *Creator) handleKeyGroupCreate(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	alias := store.NormalizeAlias(r.FormValue("alias"))
	if alias == "" {
		c.renderKeyGroups(w, http.StatusBadRequest, "", "Enter an alias (letters, digits, hyphens).")
		return
	}
	if _, err := c.store.CreateKeyGroup(alias); err != nil {
		c.renderKeyGroups(w, http.StatusBadRequest, "", keyGroupCreateError(err, alias))
		return
	}
	c.renderKeyGroups(w, http.StatusOK, "Created key group “"+alias+"”.", "")
}

// keyGroupCreateError maps a create failure to a friendly message (the common case
// being a duplicate alias).
func keyGroupCreateError(err error, alias string) string {
	if err != nil && containsUnique(err.Error()) {
		return "A key group named “" + alias + "” already exists."
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func containsUnique(s string) bool {
	for i := 0; i+6 <= len(s); i++ {
		if s[i:i+6] == "UNIQUE" {
			return true
		}
	}
	return false
}

// handleKeyGroupRename renames a group. Existing gate links keep working (only the
// label changes), but blocks referencing the old alias no longer match.
func (c *Creator) handleKeyGroupRename(w http.ResponseWriter, r *http.Request) {
	id, ok := c.keyGroupID(w, r)
	if !ok {
		return
	}
	alias := store.NormalizeAlias(r.FormValue("alias"))
	if alias == "" {
		c.renderKeyGroups(w, http.StatusBadRequest, "", "Enter an alias (letters, digits, hyphens).")
		return
	}
	if err := c.store.RenameKeyGroup(id, alias); err != nil {
		c.renderKeyGroups(w, http.StatusBadRequest, "", keyGroupCreateError(err, alias))
		return
	}
	c.renderKeyGroups(w, http.StatusOK, "Renamed to “"+alias+"”. Update any blocks that referenced the old alias.", "")
}

// handleKeyGroupRotate replaces a group's KEK. Every outstanding gate link dies and
// must be re-issued; the next build re-wraps that group's blocks under the new KEK.
func (c *Creator) handleKeyGroupRotate(w http.ResponseWriter, r *http.Request) {
	id, ok := c.keyGroupID(w, r)
	if !ok {
		return
	}
	if _, err := c.store.RotateKeyGroup(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.renderKeyGroups(w, http.StatusOK, "Rotated the key — old gate links no longer work. Rebuild and re-share the new link.", "")
}

// handleKeyGroupDelete removes a group. Blocks authorizing only this group become
// unreadable until re-authorized, so the form confirms first.
func (c *Creator) handleKeyGroupDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := c.keyGroupID(w, r)
	if !ok {
		return
	}
	if err := c.store.DeleteKeyGroup(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.renderKeyGroups(w, http.StatusOK, "Deleted the key group.", "")
}

// handleKeyGroupSplash associates (or clears) the group's splash/deposit page.
func (c *Creator) handleKeyGroupSplash(w http.ResponseWriter, r *http.Request) {
	id, ok := c.keyGroupID(w, r)
	if !ok {
		return
	}
	var pageID *int64
	if v := r.FormValue("splash"); v != "" && v != "0" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			c.renderKeyGroups(w, http.StatusBadRequest, "", "Invalid page selection.")
			return
		}
		pageID = &n
	}
	if err := c.store.SetKeyGroupSplash(id, pageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.renderKeyGroups(w, http.StatusOK, "Updated the splash page.", "")
}

// keyGroupID parses the {id} path value and checks CSRF for a mutation, writing the
// error response itself when either fails.
func (c *Creator) keyGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, fmt.Sprintf("bad key group id: %v", err), http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
