package creator

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestCreatorReplyComposer: the creator replies to a member comment from the moderation table;
// the reply is stored as a creator-authored, auto-approved reply to that root.
func TestCreatorReplyComposer(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	rootID := seedPending(t, app, "/p", "raven", "a member root")

	form := url.Values{}
	form.Set("body", "thanks for reading!")
	rec := authedForm(t, c, creatorID, "/admin/moderation/comments/"+strconv.FormatInt(rootID, 10)+"/reply", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
	}
	cs, _ := app.CommentsByPage("/p", "")
	var reply *appstore.Comment
	for i := range cs {
		if cs[i].ParentID != nil {
			reply = &cs[i]
		}
	}
	if reply == nil {
		t.Fatal("no reply stored")
	}
	if reply.AuthorRole != appstore.RoleCreator {
		t.Errorf("reply role = %q, want creator", reply.AuthorRole)
	}
	if reply.Status != appstore.CommentApproved {
		t.Errorf("reply status = %q, want approved (staff auto-approve)", reply.Status)
	}
	if reply.Body != "thanks for reading!" || *reply.ParentID != rootID {
		t.Errorf("reply = %+v", reply)
	}
}

// TestCreatorCreateComposer: the creator posts a new top-level comment on a page path.
func TestCreatorCreateComposer(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)

	form := url.Values{}
	form.Set("page", "/posts/hello")
	form.Set("body", "welcome, everyone")
	rec := authedForm(t, c, creatorID, "/admin/moderation/comment", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	cs, _ := app.CommentsByPage("/posts/hello", "")
	if len(cs) != 1 || cs[0].AuthorRole != appstore.RoleCreator || cs[0].Status != appstore.CommentApproved {
		t.Fatalf("creator comment = %+v, want one creator/approved", cs)
	}

	// A path that isn't a site path is rejected.
	bad := url.Values{}
	bad.Set("page", "relative")
	bad.Set("body", "nope")
	if rec := authedForm(t, c, creatorID, "/admin/moderation/comment", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("bad page path: status %d, want 400", rec.Code)
	}
}

// TestCreatorIdentityUnique: the creator's display name shares the account-level uniqueness —
// a name already held by a member is refused (409); a free one is stored on the account.
func TestCreatorIdentityUnique(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	if _, err := app.SetAccountAlias(m.ID, "Nova"); err != nil {
		t.Fatal(err)
	}

	taken := url.Values{}
	taken.Set("alias", "nova") // case-insensitive collision
	if rec := authedForm(t, c, creatorID, "/admin/moderation/identity", taken); rec.Code != http.StatusConflict {
		t.Errorf("taken name: status %d, want 409", rec.Code)
	}

	free := url.Values{}
	free.Set("alias", "Byline")
	if rec := authedForm(t, c, creatorID, "/admin/moderation/identity", free); rec.Code != http.StatusOK {
		t.Fatalf("free name: %d %s", rec.Code, rec.Body.String())
	}
	if acc, _, _ := app.AccountByID(creatorID); acc.Alias != "Byline" {
		t.Errorf("creator alias = %q, want Byline", acc.Alias)
	}
}

// TestModerationCascadeWarningRendered: a row whose comment has replies carries the cascade
// warning in its delete confirm, and the composer/reply controls are present.
func TestModerationCascadeWarningRendered(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	root, _ := app.AddComment(m.ID, "/p", "", "root")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)
	app.AddReply(m.ID, root.ID, "", "a reply")

	body := authedGet(t, c, app, creatorID, "/admin/moderation?status=approved").Body.String()
	for _, want := range []string{
		"The replies are removed with it", "its 1 reply", // cascade warning, singular
		"Comment as the author", "Post reply", "Display name", // composer + reply + identity
	} {
		if !strings.Contains(body, want) {
			t.Errorf("moderation view missing %q", want)
		}
	}
}
