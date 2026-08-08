package appstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Comment is a visitor comment on a page. Once approved it is public by design.
// AccountID is an internal link to the author, kept for moderation and erasure; it
// is nil after the author is anonymized or deleted (§2.4). Alias is the
// comment-scoped public display name, never a login identifier.
type Comment struct {
	ID         int64
	AccountID  *int64 // nil once anonymized/author-deleted
	PagePath   string
	Alias      string
	Body       string
	Status     string    // pending | approved | rejected
	AuthorRole string    // role snapshot at post time (member|moderator|creator); drives the staff badge
	ParentID   *int64    // nil = root comment; set = reply (one level deep, parent is always a root)
	DeletedAt  time.Time // zero unless this is a tombstone (author deleted a root that still had replies)
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Deleted reports whether the comment is a tombstone — a root the author removed while it
// still had replies, kept in place (body and alias blanked) so the thread stays coherent.
func (c Comment) Deleted() bool { return !c.DeletedAt.IsZero() }

// validCommentStatus reports whether st is a known moderation status.
func validCommentStatus(st string) bool {
	switch st {
	case CommentPending, CommentApproved, CommentRejected:
		return true
	}
	return false
}

// AddComment records a new root comment authored by an account. Member comments start
// pending; staff (moderator/creator) comments are auto-approved (see addComment). The
// account must exist (enforced by the foreign key); body must be non-empty.
func (s *Store) AddComment(accountID int64, pagePath, alias, body string) (Comment, error) {
	return s.addComment(accountID, pagePath, nil, alias, body)
}

// AddReply records a reply to an existing root comment. Threading is one level deep: the
// parent must itself be a root (replying to a reply is rejected), and the reply inherits the
// parent's page path so a client cannot attach it to a different page. Like AddComment, staff
// replies are auto-approved and member replies start pending.
func (s *Store) AddReply(accountID, parentID int64, alias, body string) (Comment, error) {
	if parentID == 0 {
		return Comment{}, fmt.Errorf("appstore: add reply: parent id required")
	}
	var (
		parentPage   string
		parentParent sql.NullInt64
	)
	switch err := s.db.QueryRow(`SELECT page_path, parent_id FROM comments WHERE id = ?`, parentID).Scan(&parentPage, &parentParent); {
	case err == sql.ErrNoRows:
		return Comment{}, fmt.Errorf("appstore: add reply: parent %d not found", parentID)
	case err != nil:
		return Comment{}, fmt.Errorf("appstore: add reply: %w", err)
	}
	if parentParent.Valid {
		// Parent is already a reply — one level of threading only.
		return Comment{}, fmt.Errorf("appstore: add reply: cannot reply to a reply (threading is one level deep)")
	}
	return s.addComment(accountID, parentPage, &parentID, alias, body)
}

// isStaffRole reports whether role posts as approved without review (moderator/creator).
func isStaffRole(role string) bool { return role == RoleModerator || role == RoleCreator }

// addComment is the shared insert behind AddComment and AddReply. It snapshots the author's
// current role — fixed at authorship so the staff badge survives later anonymization — and uses
// it to set the initial status: staff post approved (they already hold moderation power), members
// post pending. parentID is nil for a root, set for a reply. The role lookup also confirms the
// account exists (the FK would too, but this yields a clearer error).
func (s *Store) addComment(accountID int64, pagePath string, parentID *int64, alias, body string) (Comment, error) {
	if accountID == 0 {
		return Comment{}, fmt.Errorf("appstore: add comment: account id required")
	}
	if pagePath == "" || body == "" {
		return Comment{}, fmt.Errorf("appstore: add comment: page path and body required")
	}
	var role string
	switch err := s.db.QueryRow(`SELECT role FROM accounts WHERE id = ?`, accountID).Scan(&role); {
	case err == sql.ErrNoRows:
		return Comment{}, fmt.Errorf("appstore: add comment: account %d not found", accountID)
	case err != nil:
		return Comment{}, fmt.Errorf("appstore: add comment: %w", err)
	}
	status := CommentPending
	if isStaffRole(role) {
		status = CommentApproved
	}
	now := s.now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO comments (account_id, page_path, alias, body, status, author_role, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, pagePath, alias, body, status, role, nullableID(parentID), now, now,
	)
	if err != nil {
		return Comment{}, fmt.Errorf("appstore: add comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Comment{}, err
	}
	aid := accountID
	return Comment{
		ID: id, AccountID: &aid, PagePath: pagePath, Alias: alias, Body: body,
		Status: status, AuthorRole: role, ParentID: parentID,
		CreatedAt: time.Unix(now, 0), UpdatedAt: time.Unix(now, 0),
	}, nil
}

// nullableID renders an optional foreign key for a bound INSERT/UPDATE: nil → SQL NULL.
func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

const commentCols = `id, account_id, page_path, alias, body, status, author_role, parent_id, deleted_at, created_at, updated_at`

// scanComment scans one comment row in commentCols order, mapping NULL account_id/parent_id
// to nil pointers and a zero deleted_at to a zero time (a live, non-tombstone comment).
func scanComment(row interface{ Scan(...any) error }) (Comment, error) {
	var c Comment
	var acct, parent sql.NullInt64
	var deleted, created, updated int64
	if err := row.Scan(&c.ID, &acct, &c.PagePath, &c.Alias, &c.Body, &c.Status, &c.AuthorRole, &parent, &deleted, &created, &updated); err != nil {
		return Comment{}, err
	}
	if acct.Valid {
		id := acct.Int64
		c.AccountID = &id
	}
	if parent.Valid {
		id := parent.Int64
		c.ParentID = &id
	}
	if deleted != 0 {
		c.DeletedAt = time.Unix(deleted, 0)
	}
	c.CreatedAt = time.Unix(created, 0)
	c.UpdatedAt = time.Unix(updated, 0)
	return c, nil
}

// CommentByID returns a single comment. ok is false (nil error) when absent.
func (s *Store) CommentByID(id int64) (Comment, bool, error) {
	c, err := scanComment(s.db.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id = ?`, id))
	switch {
	case err == sql.ErrNoRows:
		return Comment{}, false, nil
	case err != nil:
		return Comment{}, false, fmt.Errorf("appstore: comment %d: %w", id, err)
	}
	return c, true, nil
}

// SetCommentStatus moves a comment through moderation (pending|approved|rejected).
func (s *Store) SetCommentStatus(id int64, status string) error {
	if !validCommentStatus(status) {
		return fmt.Errorf("appstore: invalid comment status %q", status)
	}
	res, err := s.db.Exec(
		`UPDATE comments SET status = ?, updated_at = ? WHERE id = ?`,
		status, s.now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("appstore: set comment %d status: %w", id, err)
	}
	return mustAffectOne(res, "comment", id)
}

// DeleteComment removes a single comment (a moderator taking one down). Returns
// ErrNotFound if it does not exist.
func (s *Store) DeleteComment(id int64) error {
	res, err := s.db.Exec(`DELETE FROM comments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("appstore: delete comment %d: %w", id, err)
	}
	return mustAffectOne(res, "comment", id)
}

// DeleteOwnComment lets an author remove one of their own comments. Ownership is enforced:
// the comment must currently be linked to accountID (a tombstoned/anonymized comment is no
// longer "owned" and cannot be deleted again), otherwise ErrNotFound is returned — the same
// result as a missing id, so the endpoint never reveals another member's comment ids.
//
// A leaf comment (no replies) is hard-deleted. A root that still has replies is instead
// tombstoned — account link nulled, alias and body blanked, deleted_at set — so the replies
// keep their context instead of being silently removed with it. Returns whether the comment
// was tombstoned (true) rather than hard-deleted (false).
func (s *Store) DeleteOwnComment(accountID, commentID int64) (tombstoned bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var owner sql.NullInt64
	switch err := tx.QueryRow(`SELECT account_id FROM comments WHERE id = ?`, commentID).Scan(&owner); {
	case err == sql.ErrNoRows:
		return false, fmt.Errorf("appstore: delete own comment %d: %w", commentID, ErrNotFound)
	case err != nil:
		return false, fmt.Errorf("appstore: delete own comment lookup: %w", err)
	}
	if !owner.Valid || owner.Int64 != accountID {
		return false, fmt.Errorf("appstore: delete own comment %d: %w", commentID, ErrNotFound)
	}

	var replies int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM comments WHERE parent_id = ?`, commentID).Scan(&replies); err != nil {
		return false, fmt.Errorf("appstore: delete own comment count replies: %w", err)
	}
	if replies > 0 {
		if _, err := tx.Exec(
			`UPDATE comments SET account_id = NULL, alias = '', body = '', deleted_at = ?, updated_at = ? WHERE id = ?`,
			s.now().Unix(), s.now().Unix(), commentID,
		); err != nil {
			return false, fmt.Errorf("appstore: tombstone comment %d: %w", commentID, err)
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if _, err := tx.Exec(`DELETE FROM comments WHERE id = ?`, commentID); err != nil {
		return false, fmt.Errorf("appstore: delete own comment %d: %w", commentID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// CommentsByPage returns a page's comments oldest first. A non-empty status filters
// to that status (e.g. CommentApproved for public rendering); an empty status
// returns all statuses (the moderator's per-page view).
func (s *Store) CommentsByPage(pagePath, status string) ([]Comment, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.Query(`SELECT `+commentCols+` FROM comments WHERE page_path = ? ORDER BY created_at, id`, pagePath)
	} else {
		if !validCommentStatus(status) {
			return nil, fmt.Errorf("appstore: invalid comment status %q", status)
		}
		rows, err = s.db.Query(`SELECT `+commentCols+` FROM comments WHERE page_path = ? AND status = ? ORDER BY created_at, id`, pagePath, status)
	}
	if err != nil {
		return nil, fmt.Errorf("appstore: comments for %q: %w", pagePath, err)
	}
	return collectComments(rows)
}

// CommentTotals is the site-wide comment tally by moderation status.
type CommentTotals struct {
	Total, Pending, Approved, Rejected int
}

// CommentTotals returns the site-wide comment counts (total plus per-status) — a quick
// dashboard metric of the moderation backlog and published volume.
func (s *Store) CommentTotals() (CommentTotals, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM comments GROUP BY status`)
	if err != nil {
		return CommentTotals{}, fmt.Errorf("appstore: comment totals: %w", err)
	}
	defer rows.Close()
	var t CommentTotals
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return CommentTotals{}, err
		}
		t.Total += n
		switch status {
		case CommentPending:
			t.Pending = n
		case CommentApproved:
			t.Approved = n
		case CommentRejected:
			t.Rejected = n
		}
	}
	return t, rows.Err()
}

// CommentCountsByPage returns the number of comments (any status) per page path — the
// creator's "how many comments would be orphaned if I delete this page" view. Comments
// are keyed by path with no link to the page tree, so a deleted page's comments persist
// here until removed.
func (s *Store) CommentCountsByPage() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT page_path, COUNT(*) FROM comments GROUP BY page_path`)
	if err != nil {
		return nil, fmt.Errorf("appstore: comment counts by page: %w", err)
	}
	defer rows.Close()
	m := make(map[string]int)
	for rows.Next() {
		var path string
		var n int
		if err := rows.Scan(&path, &n); err != nil {
			return nil, err
		}
		m[path] = n
	}
	return m, rows.Err()
}

// CommentCountByPage returns how many comments (any status) exist for one page path.
func (s *Store) CommentCountByPage(path string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE page_path = ?`, path).Scan(&n); err != nil {
		return 0, fmt.Errorf("appstore: comment count for %q: %w", path, err)
	}
	return n, nil
}

// PruneRejectedComments deletes rejected comments last touched before cutoff — hidden
// content whose keep-for-dedup value has lapsed. Pending and approved comments are never
// touched. Returns how many rows were removed.
func (s *Store) PruneRejectedComments(cutoff time.Time) (int, error) {
	res, err := s.db.Exec(`DELETE FROM comments WHERE status = ? AND updated_at < ?`, CommentRejected, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("appstore: prune rejected comments: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneOrphanedComments deletes comments whose page path is NOT among livePaths and were
// created before cutoff — comments left behind when a page (or its comment block) is
// removed. livePaths is the set of current site page paths; passing nil deletes nothing
// (a guard so a failure to read the page list can never wipe live comments). Comments are
// deleted per-orphaned-path, bounded by the number of distinct comment paths, not by page
// count. Returns how many rows were removed.
func (s *Store) PruneOrphanedComments(livePaths map[string]bool, cutoff time.Time) (int, error) {
	if livePaths == nil {
		return 0, nil
	}
	rows, err := s.db.Query(`SELECT DISTINCT page_path FROM comments`)
	if err != nil {
		return 0, fmt.Errorf("appstore: prune orphaned: list paths: %w", err)
	}
	var orphans []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if !livePaths[p] {
			orphans = append(orphans, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, p := range orphans {
		res, err := s.db.Exec(`DELETE FROM comments WHERE page_path = ? AND created_at < ?`, p, cutoff.Unix())
		if err != nil {
			return total, fmt.Errorf("appstore: prune orphaned %q: %w", p, err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// ReplyCounts returns, for each of the given comment ids, how many replies it has (0 for ids
// with none). It backs the moderation UI's cascade warning: deleting a root that has replies
// takes the replies with it (ON DELETE CASCADE), so the operator is told the count first. An
// empty input returns an empty map without querying.
func (s *Store) ReplyCounts(parentIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(parentIDs))
	args := make([]any, len(parentIDs))
	for i, id := range parentIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT parent_id, COUNT(*) FROM comments WHERE parent_id IN (` + strings.Join(placeholders, ",") + `) GROUP BY parent_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("appstore: reply counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var parent int64
		var n int
		if err := rows.Scan(&parent, &n); err != nil {
			return nil, err
		}
		out[parent] = n
	}
	return out, rows.Err()
}

// CommentsByIDs returns the requested comments keyed by id (absent ids are simply missing from
// the map). It backs the moderation UIs' reply-aware rows: a reply row needs its parent's
// display name for the "in reply to …" annotation without an N+1 query. An empty input returns
// an empty map without querying.
func (s *Store) CommentsByIDs(ids []int64) (map[int64]Comment, error) {
	out := make(map[int64]Comment, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM comments WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("appstore: comments by ids: %w", err)
	}
	cs, err := collectComments(rows)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}

// PruneChildlessTombstones deletes tombstones (author-deleted roots kept as "[deleted]" for
// reply context) that no longer have any replies and were deleted before cutoff (§F4). A
// tombstone that still has replies is never touched — removing it would orphan them. This
// reclaims the empty deleted-stubs left behind once a thread's replies are themselves gone.
// Returns how many rows were removed.
func (s *Store) PruneChildlessTombstones(cutoff time.Time) (int, error) {
	res, err := s.db.Exec(
		`DELETE FROM comments
		 WHERE deleted_at != 0 AND deleted_at < ?
		   AND NOT EXISTS (SELECT 1 FROM comments child WHERE child.parent_id = comments.id)`,
		cutoff.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: prune childless tombstones: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PendingComments returns the moderation queue — every pending comment across all
// pages, oldest first.
func (s *Store) PendingComments() ([]Comment, error) {
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM comments WHERE status = ? ORDER BY created_at, id`, CommentPending)
	if err != nil {
		return nil, fmt.Errorf("appstore: pending comments: %w", err)
	}
	return collectComments(rows)
}

// Comment listing page sizes for the moderation UI. DefaultCommentPageSize applies
// when a query asks for none; MaxCommentPageSize caps a caller-supplied limit so a
// crafted request cannot ask for an unbounded page.
const (
	DefaultCommentPageSize = 25
	MaxCommentPageSize     = 200
)

// CommentSort names the whitelisted ORDER BY columns for a moderation listing. Only
// these constants ever reach the SQL — never a raw client string — so the sort is
// injection-safe.
type CommentSort string

const (
	CommentSortPosted CommentSort = "posted" // created_at (default)
	CommentSortPage   CommentSort = "page"   // page_path
	CommentSortAuthor CommentSort = "author" // alias
)

// column maps a sort to its physical column; an unknown sort falls back to created_at.
func (cs CommentSort) column() string {
	switch cs {
	case CommentSortPage:
		return "page_path"
	case CommentSortAuthor:
		return "alias"
	default:
		return "created_at"
	}
}

// CommentQuery filters, sorts, and paginates the moderation comment listing (§7.3).
// All text filters are substring (LIKE) matches with wildcards escaped, so a literal
// '%' or '_' in the term matches itself. The zero value selects every comment,
// newest-first, first page.
type CommentQuery struct {
	Status string    // pending|approved|rejected; "" = any status
	Page   string    // substring match on page_path; "" = any
	Author string    // substring match on alias; "" = any
	Body   string    // substring match on body; "" = any
	Since  time.Time // created_at >= Since; zero = no lower bound
	Until  time.Time // created_at <= Until; zero = no upper bound
	Sort   CommentSort
	Desc   bool // sort descending (newest/Z-A first)
	Limit  int  // page size; <=0 → DefaultCommentPageSize, capped at MaxCommentPageSize
	Offset int  // rows to skip (page - 1) * Limit
}

// likePattern wraps a term for a substring LIKE with the backslash escape, so the
// operator's literal '%'/'_'/'\' match themselves rather than acting as wildcards.
func likePattern(term string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
	return "%" + esc + "%"
}

// where builds the parameterized WHERE clause shared by QueryComments and
// CountComments (empty when the query has no filters). Every value is bound, never
// interpolated.
func (q CommentQuery) where() (string, []any) {
	var conds []string
	var args []any
	if q.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, q.Status)
	}
	if q.Page != "" {
		conds = append(conds, `page_path LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(q.Page))
	}
	if q.Author != "" {
		conds = append(conds, `alias LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(q.Author))
	}
	if q.Body != "" {
		conds = append(conds, `body LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(q.Body))
	}
	if !q.Since.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		conds = append(conds, "created_at <= ?")
		args = append(args, q.Until.Unix())
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// QueryComments returns one page of comments matching q, sorted and paginated. It is
// the backing query for the moderation listing; CountComments gives the total for the
// same filters so the UI can paginate.
func (s *Store) QueryComments(q CommentQuery) ([]Comment, error) {
	where, args := q.where()
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	// The column and direction are whitelisted (CommentSort / q.Desc), never client
	// text; id breaks ties for a stable order under equal sort keys.
	order := " ORDER BY " + q.Sort.column() + " " + dir + ", id " + dir
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultCommentPageSize
	}
	if limit > MaxCommentPageSize {
		limit = MaxCommentPageSize
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM comments`+where+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("appstore: query comments: %w", err)
	}
	return collectComments(rows)
}

// CountComments returns how many comments match q's filters (Sort/Limit/Offset are
// ignored) — the total the moderation UI paginates over.
func (s *Store) CountComments(q CommentQuery) (int, error) {
	where, args := q.where()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM comments`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("appstore: count comments: %w", err)
	}
	return n, nil
}

// CommentCountsByAccount returns, per account id, how many comments it still authors.
// Anonymized comments (null account link) are not counted. It backs the account-
// moderation list so a moderator sees each account's footprint without an N+1 query.
func (s *Store) CommentCountsByAccount() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT account_id, COUNT(*) FROM comments WHERE account_id IS NOT NULL GROUP BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("appstore: comment counts by account: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// collectComments drains a comment result set.
func collectComments(rows *sql.Rows) ([]Comment, error) {
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
