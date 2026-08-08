package publicapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// modCommentAction POSTs a moderator comment action (approve/reject/delete) with a cookie.
func modCommentAction(t *testing.T, h http.Handler, id int64, action string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"ctx": {"status=pending&sort=posted&dir=asc"}, "p": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/_pbc/moderate/comments/"+strconv.FormatInt(id, 10)+"/"+action, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestModeratorCannotModerateStaffComments: a moderator may act on member comments only;
// approve/reject/delete on a creator's or another moderator's comment is refused (403), and
// the staff comment survives. Cascade collateral (deleting a member root) is unaffected.
func TestModeratorCannotModerateStaffComments(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)

	cr, _ := app.CreateAccount(appstore.RoleCreator, "")
	creatorC, _ := app.AddComment(cr.ID, "/p", "Author", "owner note") // staff auto-approve
	mod2, _ := app.CreateAccount(appstore.RoleModerator, "")
	modC, _ := app.AddComment(mod2.ID, "/p", "Mod", "mod note")
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	memberC, _ := app.AddComment(mem.ID, "/p", "m", "member note")

	// Delete: staff comments refused, member comment allowed.
	if rec := modCommentAction(t, h, creatorC.ID, "delete", ck); rec.Code != http.StatusForbidden {
		t.Errorf("mod delete creator comment: status %d, want 403", rec.Code)
	}
	if rec := modCommentAction(t, h, modC.ID, "delete", ck); rec.Code != http.StatusForbidden {
		t.Errorf("mod delete moderator comment: status %d, want 403", rec.Code)
	}
	if rec := modCommentAction(t, h, memberC.ID, "delete", ck); rec.Code != http.StatusSeeOther {
		t.Errorf("mod delete member comment: status %d, want 303", rec.Code)
	}
	// Staff comments still present; member comment gone.
	if _, ok, _ := app.CommentByID(creatorC.ID); !ok {
		t.Error("creator comment was deleted by a moderator")
	}
	if _, ok, _ := app.CommentByID(modC.ID); !ok {
		t.Error("moderator comment was deleted by a moderator")
	}
	if _, ok, _ := app.CommentByID(memberC.ID); ok {
		t.Error("member comment should have been deleted")
	}
	// Reject (unpublish) on a creator comment is also refused.
	if rec := modCommentAction(t, h, creatorC.ID, "reject", ck); rec.Code != http.StatusForbidden {
		t.Errorf("mod reject creator comment: status %d, want 403", rec.Code)
	}
	if got, _, _ := app.CommentByID(creatorC.ID); got.Status != appstore.CommentApproved {
		t.Errorf("creator comment status changed to %q by a moderator", got.Status)
	}
}

// TestModerateViewMarksStaffReadOnly: the moderator table shows a staff comment with its badge
// and a read-only note instead of action buttons.
func TestModerateViewMarksStaffReadOnly(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	cr, _ := app.CreateAccount(appstore.RoleCreator, "")
	app.AddComment(cr.ID, "/p", "Author", "owner note") // approved

	body := getModerate(t, h, "?status=approved", ck).Body.String()
	if !strings.Contains(body, "only the site owner can moderate") {
		t.Error("staff comment should render a read-only note")
	}
	if !strings.Contains(body, "mod-badge") {
		t.Error("staff comment should carry a role badge")
	}
}
