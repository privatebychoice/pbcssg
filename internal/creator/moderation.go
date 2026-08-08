package creator

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// This file is the comment moderation view (SPEC §7.3): the admin view over
// the comments recorded by the public comment endpoint (POST /_pbc/comments). One
// filterable, sortable, paginated table lets a creator work the pending queue (approve
// / reject / delete) and manage already-decided comments after the fact (unpublish an
// approved one, or restore a rejected one). Comments are served live from the runtime
// store by the public widget, so a moderation action takes effect immediately — there
// is no rebuild step. All state lives in app.db; the page requires it and degrades to a
// notice otherwise.

// commentRow is one rendered row of the moderation table.
type commentRow struct {
	ID           int64
	PagePath     string
	PageURL      string // absolute link to the live page ("" when no base URL is configured)
	Alias        string // author's display alias; "Anonymous" when blank
	Body         string
	Created      string // formatted post time
	Status       string // pending|approved|rejected — drives which actions the row offers
	IsReply      bool   // a reply (has a parent) — not itself a reply target (one level deep)
	ReplyToAlias string // parent's display name, for the "in reply to …" annotation on a reply row
	Deleted      bool   // tombstone (author deleted a root that still had replies)
	ReplyCount   int    // number of replies, for the cascade-delete warning
	AuthorID     int64  // the author's account id (0 when anonymized) — target of "Ban author"
	CanBan       bool   // the author is a live, non-creator account, so "Ban author" applies
}

// commentDisplayAlias is how a comment's author reads in a moderation row: the alias, or
// "Anonymous" when blank, or "[deleted]" for a tombstone.
func commentDisplayAlias(c appstore.Comment) string {
	if c.Deleted() {
		return "[deleted]"
	}
	if a := strings.TrimSpace(c.Alias); a != "" {
		return a
	}
	return "Anonymous"
}

// HasReplies reports whether deleting this comment would cascade to replies.
func (r commentRow) HasReplies() bool { return r.ReplyCount > 0 }

// dateLayout is the <input type=date> exchange format used by the posted-date filters.
const dateLayout = "2006-01-02"

// moderationEnabled reports whether the runtime store backing comments is wired. It is
// the same condition as authEnabled (both require app.db); comment moderation is
// meaningless without it.
func (c *Creator) moderationEnabled() bool { return c.appDB != nil }

// handleModeration renders the moderation table for the current filters.
func (c *Creator) handleModeration(w http.ResponseWriter, r *http.Request) {
	c.renderModeration(w, r, http.StatusOK, "", "")
}

// modFilter is the parsed, validated moderation filter/sort/paginate state, echoed back
// into the form and used to build both the store query and the pagination links.
type modFilter struct {
	Status string // pending|approved|rejected (defaults to pending)
	Page   string // page_path substring
	Author string // alias substring
	Body   string // body substring
	From   string // posted-on-or-after, yyyy-mm-dd (raw, as typed)
	To     string // posted-on-or-before, yyyy-mm-dd (raw, as typed)
	Sort   string // posted|page|author
	Dir    string // asc|desc
	PageNo int    // 1-based page number
}

// validModStatus is the set of statuses the moderation filter accepts.
func validModStatus(s string) bool {
	switch s {
	case appstore.CommentPending, appstore.CommentApproved, appstore.CommentRejected:
		return true
	}
	return false
}

// parseModFilter reads the filter/sort/page controls from the request. It reads via
// FormValue so it works for both the GET filter form and the action POSTs (which carry
// the same fields as hidden inputs to preserve context). Unknown values fall back to
// safe defaults; the sort direction defaults to oldest-first for the pending queue
// (work the backlog in order) and newest-first otherwise.
func parseModFilter(r *http.Request) modFilter {
	// GET (filter form / pagination link) carries the fields individually. The action
	// POSTs carry the whole filter in one encoded "ctx" field instead, so a row's
	// approve/reject/delete re-renders the same filtered, paginated view. The page
	// number rides alongside as "p" in both cases.
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
	f := modFilter{
		Status: get("status"),
		Page:   strings.TrimSpace(get("q_page")),
		Author: strings.TrimSpace(get("q_author")),
		Body:   strings.TrimSpace(get("q_body")),
		From:   strings.TrimSpace(get("from")),
		To:     strings.TrimSpace(get("to")),
		Sort:   get("sort"),
		Dir:    get("dir"),
	}
	if !validModStatus(f.Status) {
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

// query turns the parsed filter into an appstore.CommentQuery for the given page size.
// A blank/malformed date is simply no bound; the To date is taken as the whole day
// (inclusive through 23:59:59 local).
func (f modFilter) query(limit int) appstore.CommentQuery {
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

// values renders the filter as URL query values (page number omitted — the caller adds
// it), for building pagination links and action-form context that survive a round-trip.
func (f modFilter) values() url.Values {
	v := url.Values{}
	v.Set("status", f.Status)
	v.Set("sort", f.Sort)
	v.Set("dir", f.Dir)
	if f.Page != "" {
		v.Set("q_page", f.Page)
	}
	if f.Author != "" {
		v.Set("q_author", f.Author)
	}
	if f.Body != "" {
		v.Set("q_body", f.Body)
	}
	if f.From != "" {
		v.Set("from", f.From)
	}
	if f.To != "" {
		v.Set("to", f.To)
	}
	return v
}

// renderModeration renders the moderation table for the request's filters, with an
// optional notice/error banner. When the runtime store is absent it renders the
// disabled notice instead of querying.
func (c *Creator) renderModeration(w http.ResponseWriter, r *http.Request, code int, notice, errMsg string) {
	if !c.moderationEnabled() {
		if code != http.StatusOK {
			w.WriteHeader(code)
		}
		c.render(w, "moderation", map[string]any{"Disabled": true, "Notice": notice, "Error": errMsg})
		return
	}
	f := parseModFilter(r)
	limit := appstore.DefaultCommentPageSize

	total, err := c.appDB.CountComments(f.query(limit))
	if err != nil {
		http.Error(w, "moderation: "+err.Error(), http.StatusInternalServerError)
		return
	}
	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	if f.PageNo > totalPages {
		f.PageNo = totalPages
	}

	rows, err := c.appDB.QueryComments(f.query(limit))
	if err != nil {
		http.Error(w, "moderation: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// The pending-count badge is independent of the current filter, so the operator
	// always sees the size of the backlog.
	pendingCount, err := c.appDB.CountComments(appstore.CommentQuery{Status: appstore.CommentPending})
	if err != nil {
		http.Error(w, "moderation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	base := strings.TrimRight(c.state().build.BaseURL, "/")
	views := c.commentViews(rows, base)

	// The creator's own display name for the "comment as the author" composer (blank until set).
	var creatorAlias string
	if acc, ok := c.resolveSession(r); ok {
		creatorAlias = acc.Alias
	}

	// Pagination links carry the active filter forward; the action forms carry it as a
	// single encoded field (FilterCtx, page-less) so an approve/reject/delete re-renders
	// the same view. Full prev/next URLs are built here to avoid template URL escaping.
	filterCtx := f.values().Encode()
	linkFor := func(page int) string {
		v := f.values()
		v.Set("p", strconv.Itoa(page))
		return "/admin/moderation?" + v.Encode()
	}
	prevURL, nextURL := "", ""
	if f.PageNo > 1 {
		prevURL = linkFor(f.PageNo - 1)
	}
	if f.PageNo < totalPages {
		nextURL = linkFor(f.PageNo + 1)
	}
	rangeStart, rangeEnd := 0, 0
	if total > 0 {
		rangeStart = (f.PageNo-1)*limit + 1
		rangeEnd = rangeStart + len(views) - 1
	}

	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "moderation", map[string]any{
		"Rows": views, "Count": pendingCount,
		"Filter": f, "FilterCtx": filterCtx,
		"Total": total, "PageNo": f.PageNo, "TotalPages": totalPages,
		"PrevURL": prevURL, "NextURL": nextURL,
		"RangeStart": rangeStart, "RangeEnd": rangeEnd,
		"CreatorAlias": creatorAlias,
		"Notice":       notice, "Error": errMsg,
	})
}

// commentViews maps store comments to the template row shape, defaulting a blank alias to
// "Anonymous" and attaching an absolute page link when a base URL is set. It also fetches the
// reply count for each row so the delete control can warn that removing a root cascades to its
// replies (ON DELETE CASCADE). A tombstone (author-deleted root) is labelled rather than shown
// with a blank alias/body.
func (c *Creator) commentViews(comments []appstore.Comment, base string) []commentRow {
	ids := make([]int64, len(comments))
	for i, cm := range comments {
		ids[i] = cm.ID
	}
	// Best-effort: a count failure must not break moderation, so fall back to no-warning.
	replyCounts, err := c.appDB.ReplyCounts(ids)
	if err != nil {
		replyCounts = map[int64]int{}
	}
	// Parents of any reply rows, for the "in reply to …" annotation (best-effort).
	var parentIDs []int64
	for _, cm := range comments {
		if cm.ParentID != nil {
			parentIDs = append(parentIDs, *cm.ParentID)
		}
	}
	parents, err := c.appDB.CommentsByIDs(parentIDs)
	if err != nil {
		parents = map[int64]appstore.Comment{}
	}
	views := make([]commentRow, 0, len(comments))
	for _, cm := range comments {
		alias := strings.TrimSpace(cm.Alias)
		if alias == "" {
			alias = "Anonymous"
		}
		body := cm.Body
		if cm.Deleted() {
			alias, body = "[deleted]", "(comment deleted by author; kept for reply context)"
		}
		v := commentRow{
			ID: cm.ID, PagePath: cm.PagePath, Alias: alias, Body: body,
			Created: cm.CreatedAt.Format("2006-01-02 15:04"), Status: cm.Status,
			IsReply: cm.ParentID != nil, Deleted: cm.Deleted(), ReplyCount: replyCounts[cm.ID],
		}
		if cm.ParentID != nil {
			if p, ok := parents[*cm.ParentID]; ok {
				v.ReplyToAlias = commentDisplayAlias(p)
			} else {
				v.ReplyToAlias = "a removed comment"
			}
		}
		// "Ban author" targets a live, non-creator author (anonymized comments and the
		// creator's own posts are not bannable from here).
		if cm.AccountID != nil && cm.AuthorRole != appstore.RoleCreator {
			v.AuthorID, v.CanBan = *cm.AccountID, true
		}
		if base != "" {
			v.PageURL = base + cm.PagePath
		}
		views = append(views, v)
	}
	return views
}

// moderationID validates a mutation request (store present, CSRF valid, {id} parses),
// writing the error response itself when any check fails.
func (c *Creator) moderationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if !c.moderationEnabled() {
		http.Error(w, "comment moderation requires the runtime store", http.StatusNotFound)
		return 0, false
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad comment id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// handleCommentApprove publishes a pending comment. Approved comments are read live by
// the public widget, so it appears on the page immediately (no rebuild).
func (c *Creator) handleCommentApprove(w http.ResponseWriter, r *http.Request) {
	id, ok := c.moderationID(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetCommentStatus(id, appstore.CommentApproved); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not approve the comment: "+err.Error())
		return
	}
	c.renderModeration(w, r, http.StatusOK, "Comment approved — it is now visible on the page.", "")
}

// handleCommentReject hides a pending comment without deleting it: it leaves the queue
// but the record is kept (rejected), so the same content is not re-reviewed if re-posted.
func (c *Creator) handleCommentReject(w http.ResponseWriter, r *http.Request) {
	id, ok := c.moderationID(w, r)
	if !ok {
		return
	}
	if err := c.appDB.SetCommentStatus(id, appstore.CommentRejected); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not reject the comment: "+err.Error())
		return
	}
	c.renderModeration(w, r, http.StatusOK, "Comment rejected — hidden from the page and removed from the queue.", "")
}

// handleCommentDelete removes a comment outright. Deleting a root cascades to its replies
// (ON DELETE CASCADE); the UI warns first when the row has replies.
func (c *Creator) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := c.moderationID(w, r)
	if !ok {
		return
	}
	if err := c.appDB.DeleteComment(id); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not delete the comment: "+err.Error())
		return
	}
	c.renderModeration(w, r, http.StatusOK, "Comment deleted.", "")
}

// handleCommentBanAuthor bans the author of a comment straight from the moderation table
// (§7.3, de-blind the ban): the row shows the alias, so the operator bans a name they can see
// rather than an opaque handle on the Accounts tab. It resolves the comment → its account and
// applies the creator ban (flag + revoke sessions + burn the creating invite; posts are left
// in place — erase separately if wanted). An anonymized comment (no author) or the creator's
// own post is refused.
func (c *Creator) handleCommentBanAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := c.moderationID(w, r)
	if !ok {
		return
	}
	cm, found, err := c.appDB.CommentByID(id)
	if err != nil {
		c.renderModeration(w, r, http.StatusInternalServerError, "", "Could not load the comment.")
		return
	}
	if !found || cm.AccountID == nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "That comment has no author to ban (anonymized or deleted).")
		return
	}
	author, ok, err := c.appDB.AccountByID(*cm.AccountID)
	if err != nil || !ok {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not find the comment's author.")
		return
	}
	if author.Role == appstore.RoleCreator {
		c.renderModeration(w, r, http.StatusForbidden, "", "You cannot ban your own (creator) account.")
		return
	}
	if err := c.appDB.BanAccount(author.ID, false); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not ban the author: "+err.Error())
		return
	}
	name := strings.TrimSpace(cm.Alias)
	if name == "" {
		name = "the author"
	}
	c.renderModeration(w, r, http.StatusOK, "Banned "+name+" — their sessions end, the creating invite is burned, and they can't sign in. Their comments are kept; erase them from the Accounts tab if needed.", "")
}

// maxCreatorCommentBody bounds the creator's comment/reply body, matching the public widget.
const maxCreatorCommentBody = 4096

// handleCommentReply posts a reply to an existing comment as the creator (§7.3, item 5). It is
// auto-approved (staff), so it appears on the page immediately. One level deep is enforced by
// the store (a reply's parent must be a root); the display name is the creator's account alias.
func (c *Creator) handleCommentReply(w http.ResponseWriter, r *http.Request) {
	id, ok := c.moderationID(w, r)
	if !ok {
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" || len(body) > maxCreatorCommentBody {
		c.renderModeration(w, r, http.StatusBadRequest, "", "A reply must be 1–4096 characters.")
		return
	}
	if _, err := c.appDB.AddReply(acc.ID, id, acc.Alias, body); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not post the reply: "+err.Error())
		return
	}
	c.renderModeration(w, r, http.StatusOK, "Reply posted as the author — it is live on the page.", "")
}

// handleCommentCreate posts a new top-level comment on a page as the creator (auto-approved).
// The page path is validated as a site path; the display name is the creator's account alias.
func (c *Creator) handleCommentCreate(w http.ResponseWriter, r *http.Request) {
	if !c.moderationEnabled() {
		http.Error(w, "comment moderation requires the runtime store", http.StatusNotFound)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	page := strings.TrimSpace(r.FormValue("page"))
	body := strings.TrimSpace(r.FormValue("body"))
	if page == "" || !strings.HasPrefix(page, "/") {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Enter the page path (e.g. /posts/hello) to comment on.")
		return
	}
	if body == "" || len(body) > maxCreatorCommentBody {
		c.renderModeration(w, r, http.StatusBadRequest, "", "A comment must be 1–4096 characters.")
		return
	}
	if _, err := c.appDB.AddComment(acc.ID, page, acc.Alias, body); err != nil {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not post the comment: "+err.Error())
		return
	}
	c.renderModeration(w, r, http.StatusOK, "Comment posted as the author on "+page+".", "")
}

// handleCreatorIdentity sets the creator's public comment display name, reusing the same
// account-level uniqueness as members/moderators (a taken name is rejected). Blank = anonymous.
func (c *Creator) handleCreatorIdentity(w http.ResponseWriter, r *http.Request) {
	if !c.moderationEnabled() {
		http.Error(w, "comment moderation requires the runtime store", http.StatusNotFound)
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	acc, ok := c.resolveSession(r)
	if !ok {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	alias := strings.TrimSpace(r.FormValue("alias"))
	if len(alias) > 64 {
		c.renderModeration(w, r, http.StatusBadRequest, "", "Display name too long (max 64).")
		return
	}
	switch _, err := c.appDB.SetAccountAlias(acc.ID, alias); {
	case errors.Is(err, appstore.ErrAliasTaken):
		c.renderModeration(w, r, http.StatusConflict, "", "That display name is already in use — pick another.")
	case errors.Is(err, appstore.ErrAliasRateLimited):
		c.renderModeration(w, r, http.StatusTooManyRequests, "", "You've changed the display name too many times today — try again tomorrow.")
	case err != nil:
		c.renderModeration(w, r, http.StatusBadRequest, "", "Could not set the display name: "+err.Error())
	default:
		c.renderModeration(w, r, http.StatusOK, "Comment display name updated.", "")
	}
}
