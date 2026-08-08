package publicapi

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the moderator review surface on the public origin (SPEC §7.3). Unlike
// the rest of the public dynamic layer — strictly JSON endpoints — this is a
// deliberate, session-gated exception: a server-rendered page at /_pbc/moderate, never
// part of the static bundle, reachable only with a live moderator session. It reuses
// the shared comment store queries (QueryComments / CountComments) so a moderator gets
// the same status filter, search, sort, and pagination the creator has, with
// status-aware actions (approve / reject / delete / unpublish / restore). Elevated
// powers (issue invites, ban members) are added in later slices, gated per moderator.

const moderatePageSize = appstore.DefaultCommentPageSize

// dateLayout is the <input type=date> exchange format for the posted-date filters.
const dateLayout = "2006-01-02"

// moderateRow is one rendered comment row.
type moderateRow struct {
	ID           int64
	PagePath     string
	Alias        string
	Body         string
	Created      string
	Status       string
	ReplyCount   int    // replies that a delete would cascade away (§7.3)
	Staff        bool   // author is a moderator/creator — visible for context, but not moderatable here
	Badge        string // "Moderator" | "Author" | "" — staff badge for the row
	AuthorID     int64  // member author's account id (0 for staff/anonymized) — target of "Ban author"
	IsReply      bool   // a reply row — carries the "in reply to …" annotation
	ReplyToAlias string // parent's display name for that annotation
}

// HasReplies reports whether deleting this comment would also remove replies.
func (r moderateRow) HasReplies() bool { return r.ReplyCount > 0 }

// moderateDisplayAlias is how a comment's author reads in a row: the alias, or "Anonymous"
// when blank, or "[deleted]" for a tombstone.
func moderateDisplayAlias(c appstore.Comment) string {
	if c.Deleted() {
		return "[deleted]"
	}
	if a := strings.TrimSpace(c.Alias); a != "" {
		return a
	}
	return "Anonymous"
}

// moderateFilter is the parsed, validated filter/sort/paginate state.
type moderateFilter struct {
	Status, Page, Author, Body, From, To, Sort, Dir string
	PageNo                                          int
}

func validModerateStatus(s string) bool {
	switch s {
	case appstore.CommentPending, appstore.CommentApproved, appstore.CommentRejected:
		return true
	}
	return false
}

// parseModerateFilter reads the filter controls from the request (GET query on the page
// and pagination links; the encoded "ctx" field on an action POST). Unknown values fall
// back to safe defaults; the direction defaults to oldest-first for the pending queue.
func parseModerateFilter(r *http.Request) moderateFilter {
	get := r.FormValue
	if ctx := r.FormValue("ctx"); ctx != "" {
		if vals, err := url.ParseQuery(ctx); err == nil {
			get = func(k string) string {
				if k == "p" {
					return r.FormValue("p")
				}
				return vals.Get(k)
			}
		}
	}
	f := moderateFilter{
		Status: get("status"),
		Page:   strings.TrimSpace(get("q_page")),
		Author: strings.TrimSpace(get("q_author")),
		Body:   strings.TrimSpace(get("q_body")),
		From:   strings.TrimSpace(get("from")),
		To:     strings.TrimSpace(get("to")),
		Sort:   get("sort"),
		Dir:    get("dir"),
	}
	if !validModerateStatus(f.Status) {
		f.Status = appstore.CommentPending
	}
	switch appstore.CommentSort(f.Sort) {
	case appstore.CommentSortPage, appstore.CommentSortAuthor, appstore.CommentSortPosted:
	default:
		f.Sort = string(appstore.CommentSortPosted)
	}
	if f.Dir != "asc" && f.Dir != "desc" {
		if f.Status == appstore.CommentPending {
			f.Dir = "asc"
		} else {
			f.Dir = "desc"
		}
	}
	if n, err := strconv.Atoi(r.FormValue("p")); err == nil && n > 1 {
		f.PageNo = n
	} else {
		f.PageNo = 1
	}
	return f
}

func (f moderateFilter) query(limit int) appstore.CommentQuery {
	q := appstore.CommentQuery{
		Status: f.Status, Page: f.Page, Author: f.Author, Body: f.Body,
		Sort: appstore.CommentSort(f.Sort), Desc: f.Dir == "desc",
		Limit: limit, Offset: (f.PageNo - 1) * limit,
	}
	if t, err := time.ParseInLocation(dateLayout, f.From, time.Local); err == nil {
		q.Since = t
	}
	if t, err := time.ParseInLocation(dateLayout, f.To, time.Local); err == nil {
		q.Until = t.Add(24*time.Hour - time.Second)
	}
	return q
}

// values renders the filter as URL query values (page number omitted; the caller adds it).
func (f moderateFilter) values() url.Values {
	v := url.Values{}
	v.Set("status", f.Status)
	v.Set("sort", f.Sort)
	v.Set("dir", f.Dir)
	for k, val := range map[string]string{"q_page": f.Page, "q_author": f.Author, "q_body": f.Body, "from": f.From, "to": f.To} {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v
}

func (a *api) registerModerateRoutes() {
	a.mux.HandleFunc("GET /_pbc/moderate", a.handleModerate)
	a.mux.HandleFunc("POST /_pbc/moderate/comments/{id}/approve", a.handleModCommentApprove)
	a.mux.HandleFunc("POST /_pbc/moderate/comments/{id}/reject", a.handleModCommentReject)
	a.mux.HandleFunc("POST /_pbc/moderate/comments/{id}/delete", a.handleModCommentDelete)
	a.mux.HandleFunc("POST /_pbc/moderate/comments/{id}/ban-author", a.handleModCommentBanAuthor)
	a.mux.HandleFunc("POST /_pbc/moderate/identity", a.handleModIdentity)
	a.mux.HandleFunc("GET "+moderateCSSPath, a.serveModerateCSS)
	a.mux.HandleFunc("GET "+moderateJSPath, a.serveModerateJS)
}

// moderatePageData is the template model.
type moderatePageData struct {
	Authed                                             bool
	Label                                              string
	Alias                                              string // the moderator's public comment display name (editable here)
	CanInvite, CanBan                                  bool   // gate the Invites link / ban controls
	Status, QPage, QAuthor, QBody, From, To, Sort, Dir string
	Rows                                               []moderateRow
	Total, PageNo, TotalPages, RangeStart, RangeEnd    int
	PrevURL, NextURL                                   string
	FilterCtx                                          string
	PendingCount                                       int
	Notice                                             string
}

// setModeratePageHeaders applies a self-hosted, strict header set to the moderator page
// and its assets: same-origin-only resources, no framing, no referer leakage, and the
// passkey Permissions-Policy the WebAuthn ceremony needs.
func setModeratePageHeaders(w http.ResponseWriter, contentType string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", "publickey-credentials-create=(self), publickey-credentials-get=(self)")
}

// handleModerate renders the moderator surface: a sign-in/register shell when not
// authenticated, the filterable moderation table when a moderator session is present.
func (a *api) handleModerate(w http.ResponseWriter, r *http.Request) {
	setModeratePageHeaders(w, "text/html; charset=utf-8")
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		_ = moderateTmpl.Execute(w, moderatePageData{Authed: false})
		return
	}

	f := parseModerateFilter(r)
	total, err := a.app.CountComments(f.query(moderatePageSize))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	totalPages := (total + moderatePageSize - 1) / moderatePageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if f.PageNo > totalPages {
		f.PageNo = totalPages
	}
	comments, err := a.app.QueryComments(f.query(moderatePageSize))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pending, err := a.app.CountComments(appstore.CommentQuery{Status: appstore.CommentPending})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ids := make([]int64, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
	}
	// Best-effort reply counts for the cascade-delete warning; a failure just omits the count.
	replyCounts, err := a.app.ReplyCounts(ids)
	if err != nil {
		replyCounts = map[int64]int{}
	}
	// Parents of any reply rows, for the "in reply to …" annotation (best-effort).
	var parentIDs []int64
	for _, c := range comments {
		if c.ParentID != nil {
			parentIDs = append(parentIDs, *c.ParentID)
		}
	}
	parents, err := a.app.CommentsByIDs(parentIDs)
	if err != nil {
		parents = map[int64]appstore.Comment{}
	}
	rows := make([]moderateRow, 0, len(comments))
	for _, c := range comments {
		alias := strings.TrimSpace(c.Alias)
		if alias == "" {
			alias = "Anonymous"
		}
		body := c.Body
		if c.Deleted() {
			alias, body = "[deleted]", "(comment deleted by author; kept for reply context)"
		}
		staff, badge := false, ""
		switch c.AuthorRole {
		case appstore.RoleModerator:
			staff, badge = true, "Moderator"
		case appstore.RoleCreator:
			staff, badge = true, "Author"
		}
		var authorID int64
		if !staff && !c.Deleted() && c.AccountID != nil {
			authorID = *c.AccountID // a member author a moderator with Can-ban may act on
		}
		replyTo := ""
		if c.ParentID != nil {
			if p, ok := parents[*c.ParentID]; ok {
				replyTo = moderateDisplayAlias(p)
			} else {
				replyTo = "a removed comment"
			}
		}
		rows = append(rows, moderateRow{
			ID: c.ID, PagePath: c.PagePath, Alias: alias, Body: body,
			Created: c.CreatedAt.Format("2006-01-02 15:04"), Status: c.Status,
			ReplyCount: replyCounts[c.ID], Staff: staff, Badge: badge, AuthorID: authorID,
			IsReply: c.ParentID != nil, ReplyToAlias: replyTo,
		})
	}

	link := func(page int) string {
		v := f.values()
		v.Set("p", strconv.Itoa(page))
		return "/_pbc/moderate?" + v.Encode()
	}
	prevURL, nextURL := "", ""
	if f.PageNo > 1 {
		prevURL = link(f.PageNo - 1)
	}
	if f.PageNo < totalPages {
		nextURL = link(f.PageNo + 1)
	}
	rangeStart, rangeEnd := 0, 0
	if total > 0 {
		rangeStart = (f.PageNo-1)*moderatePageSize + 1
		rangeEnd = rangeStart + len(rows) - 1
	}

	_ = moderateTmpl.Execute(w, moderatePageData{
		Authed: true, Label: acc.Label, Alias: acc.Alias, CanInvite: acc.CanInvite, CanBan: acc.CanBan,
		Status: f.Status, QPage: f.Page, QAuthor: f.Author, QBody: f.Body,
		From: f.From, To: f.To, Sort: f.Sort, Dir: f.Dir,
		Rows: rows, Total: total, PageNo: f.PageNo, TotalPages: totalPages,
		RangeStart: rangeStart, RangeEnd: rangeEnd,
		PrevURL: prevURL, NextURL: nextURL, FilterCtx: f.values().Encode(),
		PendingCount: pending, Notice: moderateNotice(r.URL.Query().Get("msg")),
	})
}

// moderateNotice maps a redirect message code to a canned banner (never reflects raw input).
func moderateNotice(code string) string {
	switch code {
	case "approved":
		return "Comment approved — it is now visible on the page."
	case "hidden":
		return "Comment hidden from the page."
	case "deleted":
		return "Comment deleted."
	case "banned":
		return "Author banned — their sessions end and they can't sign in. Their comments are untouched."
	case "named":
		return "Your comment display name was updated."
	case "nametaken":
		return "That display name is already in use — pick another."
	case "namecap":
		return "You've changed your name too many times today — try again tomorrow."
	default:
		return ""
	}
}

// modAction validates a moderator mutation (moderator session + {id}) and returns the
// parsed comment id. CSRF is defended by the moderator cookie's SameSite=Strict
// attribute — a cross-site POST never carries it, so the session check below fails — the
// same rationale accepted for the creator per-process model. The Origin
// header is not required: browsers omit it on some same-origin form POSTs, which would
// break a moderator's own action.
func (a *api) modAction(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if _, ok := a.resolveModeratorSession(r); !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// modActionRedirect completes an action with Post/Redirect/Get, preserving the filter
// (carried in the "ctx"/"p" form fields) and a canned message.
func (a *api) modActionRedirect(w http.ResponseWriter, r *http.Request, msg string) {
	dest := "/_pbc/moderate"
	f := parseModerateFilter(r).values()
	f.Set("p", r.FormValue("p"))
	f.Set("msg", msg)
	dest += "?" + f.Encode()
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// moderatorMayAction reports whether a moderator may take a direct moderation action on the
// given comment. A moderator moderates MEMBERS only: a staff comment (another moderator's or
// the creator's) is off-limits — the creator moderates staff from /admin/moderation. It stays
// visible in the list for context; only the actions are withheld. A missing comment is a 404.
// (This is a direct-action guard; a staff reply can still be removed as ON DELETE CASCADE
// collateral when the member root it hangs under is deleted — that is an action on the member
// comment, not on the staff one.)
func (a *api) moderatorMayAction(w http.ResponseWriter, id int64) bool {
	c, ok, err := a.app.CommentByID(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	if c.AuthorRole == appstore.RoleModerator || c.AuthorRole == appstore.RoleCreator {
		http.Error(w, "a moderator cannot moderate a staff comment", http.StatusForbidden)
		return false
	}
	return true
}

// handleModIdentity sets the signed-in moderator's public comment display name (their account
// alias), reusing the same account-level uniqueness as members — a taken name is refused. It is
// how a moderator picks the name shown next to their Moderator badge, from their own surface
// rather than the public widget (§F6). Blank = anonymous.
func (a *api) handleModIdentity(w http.ResponseWriter, r *http.Request) {
	acc, ok := a.resolveModeratorSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	alias := strings.TrimSpace(r.FormValue("alias"))
	if len(alias) > 64 {
		http.Error(w, "name too long", http.StatusBadRequest)
		return
	}
	switch _, err := a.app.SetAccountAlias(acc.ID, alias); {
	case errors.Is(err, appstore.ErrAliasTaken):
		http.Redirect(w, r, "/_pbc/moderate?msg=nametaken", http.StatusSeeOther)
	case errors.Is(err, appstore.ErrAliasRateLimited):
		http.Redirect(w, r, "/_pbc/moderate?msg=namecap", http.StatusSeeOther)
	case err != nil:
		http.Error(w, "could not save name", http.StatusInternalServerError)
	default:
		http.Redirect(w, r, "/_pbc/moderate?msg=named", http.StatusSeeOther)
	}
}

func (a *api) handleModCommentApprove(w http.ResponseWriter, r *http.Request) {
	id, ok := a.modAction(w, r)
	if !ok {
		return
	}
	if !a.moderatorMayAction(w, id) {
		return
	}
	if err := a.app.SetCommentStatus(id, appstore.CommentApproved); err != nil {
		http.Error(w, "could not approve", http.StatusBadRequest)
		return
	}
	a.modActionRedirect(w, r, "approved")
}

func (a *api) handleModCommentReject(w http.ResponseWriter, r *http.Request) {
	id, ok := a.modAction(w, r)
	if !ok {
		return
	}
	if !a.moderatorMayAction(w, id) {
		return
	}
	if err := a.app.SetCommentStatus(id, appstore.CommentRejected); err != nil {
		http.Error(w, "could not reject", http.StatusBadRequest)
		return
	}
	a.modActionRedirect(w, r, "hidden")
}

// handleModCommentBanAuthor soft-bans the member author of a comment straight from the
// moderation table (§7.3, de-blind the ban) — the row shows the alias, so the moderator bans a
// name they can see. It requires the Can-ban grant and applies only to a MEMBER author
// (SoftBanAccount is members-only); a staff or anonymized comment is refused. The soft ban is
// reversible (flag + revoke sessions), never an erase.
func (a *api) handleModCommentBanAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := a.modAction(w, r)
	if !ok {
		return
	}
	actor, _ := a.resolveModeratorSession(r)
	if !actor.CanBan {
		http.Error(w, "you don't have permission to ban members", http.StatusForbidden)
		return
	}
	cm, found, err := a.app.CommentByID(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found || cm.AccountID == nil {
		http.Error(w, "that comment has no author to ban", http.StatusBadRequest)
		return
	}
	author, ok, err := a.app.AccountByID(*cm.AccountID)
	if err != nil || !ok {
		http.Error(w, "could not find the author", http.StatusBadRequest)
		return
	}
	if author.Role != appstore.RoleMember {
		http.Error(w, "only member accounts can be banned here", http.StatusForbidden)
		return
	}
	if err := a.app.SoftBanAccount(author.ID); err != nil {
		http.Error(w, "could not ban the author", http.StatusBadRequest)
		return
	}
	log.Printf("INFO publicapi moderator: account %d soft-banned member %d (from a comment)", actor.ID, author.ID)
	a.modActionRedirect(w, r, "banned")
}

func (a *api) handleModCommentDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := a.modAction(w, r)
	if !ok {
		return
	}
	if !a.moderatorMayAction(w, id) {
		return
	}
	if err := a.app.DeleteComment(id); err != nil {
		http.Error(w, "could not delete", http.StatusBadRequest)
		return
	}
	a.modActionRedirect(w, r, "deleted")
}

const (
	moderateCSSPath = "/_pbc/mod/assets/moderate.css"
	moderateJSPath  = "/_pbc/mod/assets/moderate.js"
)

func (a *api) serveModerateCSS(w http.ResponseWriter, r *http.Request) {
	setModeratePageHeaders(w, "text/css; charset=utf-8")
	_, _ = w.Write([]byte(moderateCSS))
}

func (a *api) serveModerateJS(w http.ResponseWriter, r *http.Request) {
	setModeratePageHeaders(w, "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(moderateJS))
}

var moderateTmpl = template.Must(template.New("moderate").Parse(moderateHTML))
