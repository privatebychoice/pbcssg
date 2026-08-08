package creator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// authedForm issues a form POST carrying a valid creator session cookie and the CSRF
// token, mirroring what the admin UI submits.
func authedForm(t *testing.T, c *Creator, accID int64, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := c.appDB.CreateSession(accID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", c.csrf)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

// seedPending creates a member and a pending comment on pagePath, returning its id.
func seedPending(t *testing.T, app *appstore.Store, pagePath, alias, body string) int64 {
	t.Helper()
	m, err := app.CreateAccount(appstore.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}
	cm, err := app.AddComment(m.ID, pagePath, alias, body)
	if err != nil {
		t.Fatal(err)
	}
	return cm.ID
}

func TestModerationQueueLists(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	seedPending(t, app, "/posts/a", "raven", "first pending body")
	seedPending(t, app, "/posts/b", "", "second pending body") // blank alias → Anonymous

	// An approved comment is filtered out of the default (pending) view.
	other := seedPending(t, app, "/posts/c", "mallory", "already approved")
	if err := app.SetCommentStatus(other, appstore.CommentApproved); err != nil {
		t.Fatal(err)
	}

	// Default view: the pending queue.
	rec := authedGet(t, c, app, creatorID, "/admin/moderation")
	if rec.Code != http.StatusOK {
		t.Fatalf("moderation GET status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"first pending body", "second pending body", "raven", "Anonymous",
		"/posts/a", "/posts/b",
		"/admin/moderation/comments/", "/approve", "/reject", "/delete",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pending view missing %q", want)
		}
	}
	if strings.Contains(body, "already approved") {
		t.Error("approved comment must not appear in the default pending view")
	}

	// Approved filter: the approved comment shows with the after-the-fact Unpublish action.
	recA := authedGet(t, c, app, creatorID, "/admin/moderation?status=approved")
	if recA.Code != http.StatusOK {
		t.Fatalf("approved-filter GET status %d", recA.Code)
	}
	bodyA := recA.Body.String()
	if !strings.Contains(bodyA, "already approved") {
		t.Error("approved filter must list the approved comment")
	}
	if !strings.Contains(bodyA, "Unpublish") {
		t.Error("approved row must offer an Unpublish action")
	}
	if strings.Contains(bodyA, "first pending body") {
		t.Error("approved filter must not list pending comments")
	}
}

func TestModerationSearchSortPaginate(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	// 30 pending comments across two pages so the default page size (25) paginates.
	for i := 0; i < 30; i++ {
		page := "/alpha"
		if i%2 == 0 {
			page = "/beta"
		}
		seedPending(t, app, page, "member", "comment number needle-"+itoa(int64(i)))
	}

	// Page 1 of the pending view paginates (25/page) and offers a Next link.
	rec := authedGet(t, c, app, creatorID, "/admin/moderation")
	body := rec.Body.String()
	if !strings.Contains(body, "of 30 pending comments") {
		t.Errorf("missing total-count summary for 30 comments")
	}
	if !strings.Contains(body, "p=2") {
		t.Errorf("page 1 must link to page 2")
	}

	// Page substring search narrows to one page's comments (15 of them → single page,
	// no pager) and preserves the filter in the summary.
	recF := authedGet(t, c, app, creatorID, "/admin/moderation?q_page=/beta")
	bodyF := recF.Body.String()
	if !strings.Contains(bodyF, "of 15 pending comments") {
		t.Errorf("page filter /beta should total 15")
	}
	if strings.Contains(bodyF, ">Page 1 of ") && strings.Contains(bodyF, "p=2") {
		t.Errorf("15 results should fit one page (no page 2 link)")
	}
}

func TestModerationApproveRejectDelete(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	approveID := seedPending(t, app, "/p/a", "a", "approve me")
	rejectID := seedPending(t, app, "/p/b", "b", "reject me")
	deleteID := seedPending(t, app, "/p/c", "c", "delete me")

	// Approve → becomes public immediately (read live by the widget, no rebuild).
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+itoa(approveID)+"/approve", nil); rec.Code != http.StatusOK {
		t.Fatalf("approve status %d", rec.Code)
	}
	if got, _ := app.CommentsByPage("/p/a", appstore.CommentApproved); len(got) != 1 {
		t.Errorf("approved comment not public: got %d approved on /p/a", len(got))
	}

	// Reject → leaves the queue, record retained as rejected.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+itoa(rejectID)+"/reject", nil); rec.Code != http.StatusOK {
		t.Fatalf("reject status %d", rec.Code)
	}
	if cm, ok, _ := app.CommentByID(rejectID); !ok || cm.Status != appstore.CommentRejected {
		t.Errorf("rejected comment status = %q, want rejected (present=%v)", cm.Status, ok)
	}

	// Delete → gone.
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+itoa(deleteID)+"/delete", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete status %d", rec.Code)
	}
	if _, ok, _ := app.CommentByID(deleteID); ok {
		t.Errorf("deleted comment still present")
	}

	// The queue is now empty.
	if pending, _ := app.PendingComments(); len(pending) != 0 {
		t.Errorf("queue not empty after moderation: %d pending", len(pending))
	}
}

func TestModerationRequiresCSRF(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	id := seedPending(t, app, "/p", "x", "body")

	token, _, _ := app.CreateSession(creatorID, time.Hour)
	// POST without the CSRF field.
	req := httptest.NewRequest(http.MethodPost, "/admin/moderation/comments/"+itoa(id)+"/approve", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: c.cookieName, Value: token})
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF-less approve status %d, want 403", rec.Code)
	}
	// The comment stays pending.
	if cm, _, _ := app.CommentByID(id); cm.Status != appstore.CommentPending {
		t.Errorf("comment status changed despite CSRF failure: %q", cm.Status)
	}
}

func TestModerationGateRequiresSession(t *testing.T) {
	c, _ := newAuthCreator(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/moderation", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated moderation GET status %d, want 303", rec.Code)
	}
}

func TestModerationDisabledWithoutStore(t *testing.T) {
	// A creator with no runtime store shows the disabled notice, not a crash, and the
	// gate is a no-op (auth off), so the page is reachable directly.
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := New(st, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c.moderationEnabled() {
		t.Fatal("moderation should be disabled without an app store")
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/moderation", nil)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderation GET (no store) status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "needs the runtime store") {
		t.Errorf("expected the disabled notice:\n%s", rec.Body.String())
	}
}
