package creator

import (
	"net/http"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func TestDashboardShowsCommentCounts(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	if _, err := c.store.CreatePage(store.Page{Path: "/hello", Slug: "hello", Title: "Hello"}); err != nil {
		t.Fatal(err)
	}
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	if _, err := app.AddComment(mem.ID, "/hello", "x", "hi"); err != nil {
		t.Fatal(err)
	}

	rec := authedGet(t, c, app, creatorID, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Comments across the site") {
		t.Error("dashboard missing the site-wide comment summary")
	}
	if !strings.Contains(body, "1 pending") {
		t.Error("dashboard summary missing the pending count")
	}
	// The page row links to that page's comments (the path is percent-encoded in the URL).
	if !strings.Contains(body, "/admin/moderation?q_page=") {
		t.Error("dashboard missing the per-page comment count link")
	}
}

func TestEditorShowsCommentCountNearDelete(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	pid, err := c.store.CreatePage(store.Page{Path: "/post", Slug: "post", Title: "Post"})
	if err != nil {
		t.Fatal(err)
	}
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	app.AddComment(mem.ID, "/post", "x", "one")
	app.AddComment(mem.ID, "/post", "x", "two")

	body := authedGet(t, c, app, creatorID, "/pages/"+itoa(pid)).Body.String()
	if !strings.Contains(body, "2 comment(s)") {
		t.Error("editor missing the comment-count note near Delete")
	}
}
