package publicapi

import (
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestModerateReplyAnnotation: a reply row in the moderator table shows "in reply to <parent>".
func TestModerateReplyAnnotation(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	root, _ := app.AddComment(m.ID, "/p", "Rootie", "root body")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)
	reply, _ := app.AddReply(m.ID, root.ID, "", "reply body")
	app.SetCommentStatus(reply.ID, appstore.CommentApproved)

	body := getModerate(t, h, "?status=approved", ck).Body.String()
	if !strings.Contains(body, "in reply to Rootie") {
		t.Error("moderator reply row missing the in-reply-to annotation")
	}
}
