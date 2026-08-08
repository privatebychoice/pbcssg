package appstore

import (
	"errors"
	"testing"
	"time"
)

func TestCommentCountsAndTotals(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	// /p: two pending + one approved (3 total). /q: one rejected.
	s.AddComment(a.ID, "/p", "", "a")
	ap, _ := s.AddComment(a.ID, "/p", "", "b")
	s.SetCommentStatus(ap.ID, CommentApproved)
	s.AddComment(a.ID, "/p", "", "c")
	rj, _ := s.AddComment(a.ID, "/q", "", "d")
	s.SetCommentStatus(rj.ID, CommentRejected)

	byPage, err := s.CommentCountsByPage()
	if err != nil {
		t.Fatal(err)
	}
	if byPage["/p"] != 3 || byPage["/q"] != 1 {
		t.Errorf("counts by page = %v, want /p:3 /q:1", byPage)
	}
	if n, _ := s.CommentCountByPage("/p"); n != 3 {
		t.Errorf("count /p = %d, want 3", n)
	}
	if n, _ := s.CommentCountByPage("/missing"); n != 0 {
		t.Errorf("count /missing = %d, want 0", n)
	}
	tot, err := s.CommentTotals()
	if err != nil {
		t.Fatal(err)
	}
	if tot != (CommentTotals{Total: 4, Pending: 2, Approved: 1, Rejected: 1}) {
		t.Errorf("totals = %+v, want {4,2,1,1}", tot)
	}
}

func TestAddCommentSnapshotsAuthorRole(t *testing.T) {
	s := newTestStore(t)
	mem, _ := s.CreateAccount(RoleMember, "")
	mod, _ := s.CreateAccount(RoleModerator, "")

	mc, err := s.AddComment(mem.ID, "/p", "m", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if mc.AuthorRole != RoleMember {
		t.Errorf("member comment author_role = %q, want member", mc.AuthorRole)
	}
	modc, err := s.AddComment(mod.ID, "/p", "ModName", "notice")
	if err != nil {
		t.Fatal(err)
	}
	if modc.AuthorRole != RoleModerator {
		t.Errorf("moderator comment author_role = %q, want moderator", modc.AuthorRole)
	}

	// The snapshot persists through the read path (drives the "MOD:" badge).
	got, _ := s.CommentsByPage("/p", "")
	roles := map[int64]string{}
	for _, c := range got {
		roles[c.ID] = c.AuthorRole
	}
	if roles[mc.ID] != RoleMember || roles[modc.ID] != RoleModerator {
		t.Errorf("persisted author_role = %v, want member/moderator", roles)
	}
}

func TestAddCommentStartsPending(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")

	c, err := s.AddComment(a.ID, "/post", "raven", "first!")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c.Status != CommentPending {
		t.Errorf("status = %q, want pending (nothing public until approved)", c.Status)
	}
	if c.AccountID == nil || *c.AccountID != a.ID {
		t.Errorf("account link = %v, want %d", c.AccountID, a.ID)
	}
	if c.Alias != "raven" || c.Body != "first!" {
		t.Errorf("fields = %+v", c)
	}
	if !c.CreatedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("created_at = %v, want 1000", c.CreatedAt)
	}
}

func TestCommentCountsByAccount(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")
	if _, err := s.AddComment(a.ID, "/p", "", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(a.ID, "/q", "", "two"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(b.ID, "/p", "", "solo"); err != nil {
		t.Fatal(err)
	}

	counts, err := s.CommentCountsByAccount()
	if err != nil {
		t.Fatalf("CommentCountsByAccount: %v", err)
	}
	if counts[a.ID] != 2 || counts[b.ID] != 1 {
		t.Errorf("counts = %v, want {%d:2, %d:1}", counts, a.ID, b.ID)
	}

	// Anonymized comments (null account link) drop out of the counts.
	if _, err := s.AnonymizeCommentsByAccount(a.ID); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.CommentCountsByAccount()
	if _, present := counts[a.ID]; present {
		t.Errorf("anonymized account should not be counted, got %v", counts)
	}
	if counts[b.ID] != 1 {
		t.Errorf("other account count changed: %v", counts)
	}
}

func TestAddCommentValidation(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	if _, err := s.AddComment(0, "/p", "x", "b"); err == nil {
		t.Error("missing account should error")
	}
	if _, err := s.AddComment(a.ID, "", "x", "b"); err == nil {
		t.Error("missing page path should error")
	}
	if _, err := s.AddComment(a.ID, "/p", "x", ""); err == nil {
		t.Error("empty body should error")
	}
	// Dangling account id violates the foreign key.
	if _, err := s.AddComment(9999, "/p", "x", "b"); err == nil {
		t.Error("dangling account id should violate the foreign key")
	}
}

func TestModerationLifecycle(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	c, _ := s.AddComment(a.ID, "/post", "raven", "hi")

	if err := s.SetCommentStatus(c.ID, CommentApproved); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _, _ := s.CommentByID(c.ID)
	if got.Status != CommentApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}
	if err := s.SetCommentStatus(c.ID, "bogus"); err == nil {
		t.Error("invalid status should be rejected")
	}
	if err := s.SetCommentStatus(9999, CommentApproved); !errors.Is(err, ErrNotFound) {
		t.Errorf("status on missing comment = %v, want ErrNotFound", err)
	}
}

func TestCommentsByPageFilter(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100)
	a, _ := s.CreateAccount(RoleMember, "")

	c1, _ := s.AddComment(a.ID, "/post", "one", "a")
	clockAt(s, 200)
	c2, _ := s.AddComment(a.ID, "/post", "two", "b")
	clockAt(s, 300)
	s.AddComment(a.ID, "/other", "three", "c")

	s.SetCommentStatus(c1.ID, CommentApproved)
	s.SetCommentStatus(c2.ID, CommentRejected)

	// Public render: approved only, on this page, oldest first.
	approved, err := s.CommentsByPage("/post", CommentApproved)
	if err != nil {
		t.Fatalf("CommentsByPage: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != c1.ID {
		t.Errorf("approved for /post = %+v, want just c1", approved)
	}
	// Moderator view: all statuses on the page.
	all, _ := s.CommentsByPage("/post", "")
	if len(all) != 2 {
		t.Errorf("all for /post = %d, want 2", len(all))
	}
	if all[0].ID != c1.ID || all[1].ID != c2.ID {
		t.Errorf("order = %d,%d, want oldest first", all[0].ID, all[1].ID)
	}
	if _, err := s.CommentsByPage("/post", "bogus"); err == nil {
		t.Error("invalid status filter should error")
	}
}

func TestPendingCommentsQueue(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100)
	a, _ := s.CreateAccount(RoleMember, "")

	p1, _ := s.AddComment(a.ID, "/a", "x", "1")
	clockAt(s, 200)
	s.AddComment(a.ID, "/b", "y", "2") // stays pending
	clockAt(s, 300)
	done, _ := s.AddComment(a.ID, "/c", "z", "3")
	s.SetCommentStatus(done.ID, CommentApproved)

	queue, err := s.PendingComments()
	if err != nil {
		t.Fatalf("PendingComments: %v", err)
	}
	if len(queue) != 2 {
		t.Fatalf("queue = %d, want 2 pending across pages", len(queue))
	}
	if queue[0].ID != p1.ID {
		t.Errorf("queue not oldest-first: first = %d, want %d", queue[0].ID, p1.ID)
	}
}

func TestQueryComments(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100)
	a, _ := s.CreateAccount(RoleMember, "")

	// Three approved across two pages/authors at distinct times, plus decoys.
	c1, _ := s.AddComment(a.ID, "/blog/alpha", "raven", "loved this post")
	s.SetCommentStatus(c1.ID, CommentApproved)
	clockAt(s, 200)
	c2, _ := s.AddComment(a.ID, "/blog/beta", "mallory", "spam link here")
	s.SetCommentStatus(c2.ID, CommentApproved)
	clockAt(s, 300)
	c3, _ := s.AddComment(a.ID, "/about", "raven", "great work overall")
	s.SetCommentStatus(c3.ID, CommentApproved)
	clockAt(s, 400)
	s.AddComment(a.ID, "/blog/alpha", "raven", "still pending") // pending — excluded by status filter

	// Status filter, newest-first.
	got, err := s.QueryComments(CommentQuery{Status: CommentApproved, Desc: true})
	if err != nil {
		t.Fatalf("QueryComments: %v", err)
	}
	if len(got) != 3 || got[0].ID != c3.ID || got[2].ID != c1.ID {
		t.Fatalf("approved newest-first = %v, want [c3 c2 c1]", ids(got))
	}

	// Page substring.
	if got, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Page: "/blog/"}); len(got) != 2 {
		t.Errorf("page filter /blog/ = %d, want 2", len(got))
	}
	// Author exact-ish substring.
	if got, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Author: "raven"}); len(got) != 2 {
		t.Errorf("author filter raven = %d, want 2", len(got))
	}
	// Body substring.
	if got, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Body: "spam"}); len(got) != 1 || got[0].ID != c2.ID {
		t.Errorf("body filter spam = %v, want [c2]", ids(got))
	}
	// Sort by author ascending: mallory before raven.
	if got, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Sort: CommentSortAuthor}); len(got) != 3 || got[0].ID != c2.ID {
		t.Errorf("sort author asc leads with %v, want mallory (c2)", ids(got))
	}
	// Date window: only the comment posted at t=200 (c2).
	win, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Since: time.Unix(150, 0), Until: time.Unix(250, 0)})
	if len(win) != 1 || win[0].ID != c2.ID {
		t.Errorf("date window = %v, want [c2]", ids(win))
	}
	// Pagination: page size 2 over 3 approved.
	p1, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Desc: true, Limit: 2, Offset: 0})
	p2, _ := s.QueryComments(CommentQuery{Status: CommentApproved, Desc: true, Limit: 2, Offset: 2})
	if len(p1) != 2 || len(p2) != 1 {
		t.Errorf("pagination sizes = %d,%d, want 2,1", len(p1), len(p2))
	}
}

func TestQueryCommentsLikeWildcardsAreLiteral(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	s.AddComment(a.ID, "/p", "50% off", "buy now") // literal % in alias
	s.AddComment(a.ID, "/p", "ordinary", "nothing here")

	// A search for "50%" must match the literal, not act as a wildcard.
	if got, _ := s.QueryComments(CommentQuery{Author: "50%"}); len(got) != 1 {
		t.Errorf("literal %% search matched %d, want 1", len(got))
	}
	// "_rdinary" must NOT match "ordinary" — the underscore is escaped to a literal.
	if got, _ := s.QueryComments(CommentQuery{Author: "_rdinary"}); len(got) != 0 {
		t.Errorf("literal _ search matched %d, want 0", len(got))
	}
}

func TestCountComments(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	p, _ := s.AddComment(a.ID, "/x", "amy", "one")
	s.AddComment(a.ID, "/x", "amy", "two")
	ap, _ := s.AddComment(a.ID, "/y", "bea", "three")
	s.SetCommentStatus(ap.ID, CommentApproved)
	_ = p

	if n, _ := s.CountComments(CommentQuery{Status: CommentPending}); n != 2 {
		t.Errorf("pending count = %d, want 2", n)
	}
	if n, _ := s.CountComments(CommentQuery{Status: CommentApproved}); n != 1 {
		t.Errorf("approved count = %d, want 1", n)
	}
	// Count ignores Limit/Offset — it is the total for the filter.
	if n, _ := s.CountComments(CommentQuery{Status: CommentPending, Limit: 1, Offset: 5}); n != 2 {
		t.Errorf("count with paging = %d, want 2 (paging ignored)", n)
	}
	if n, _ := s.CountComments(CommentQuery{Author: "amy"}); n != 2 {
		t.Errorf("author count = %d, want 2", n)
	}
}

// ids extracts comment ids for readable failure messages.
func ids(cs []Comment) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestDeleteComment(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	c, _ := s.AddComment(a.ID, "/post", "x", "b")

	if err := s.DeleteComment(c.ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if _, ok, _ := s.CommentByID(c.ID); ok {
		t.Error("comment still present after delete")
	}
	if err := s.DeleteComment(c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing = %v, want ErrNotFound", err)
	}
}
