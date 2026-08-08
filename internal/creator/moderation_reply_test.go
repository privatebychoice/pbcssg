package creator

import (
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestModerationReplyAnnotation: a reply row in the creator moderation table shows "in reply
// to <parent alias>".
func TestModerationReplyAnnotation(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	root, _ := app.AddComment(m.ID, "/p", "Rootie", "root body")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)
	m2, _ := app.CreateAccount(appstore.RoleMember, "")
	reply, _ := app.AddReply(m2.ID, root.ID, "Replier", "reply body")
	app.SetCommentStatus(reply.ID, appstore.CommentApproved)

	body := authedGet(t, c, app, creatorID, "/admin/moderation?status=approved").Body.String()
	if !strings.Contains(body, "in reply to Rootie") {
		t.Error("reply row missing the in-reply-to annotation")
	}
}
