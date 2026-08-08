package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// getAuthed does a GET with a session cookie so the response can be viewer-aware (mine).
func getAuthed(t *testing.T, h http.Handler, path string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// deleteOwnComment posts to the self-delete endpoint with the Origin header and a cookie.
func deleteOwnComment(t *testing.T, h http.Handler, id int64, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_pbc/comments/"+strconv.FormatInt(id, 10)+"/delete", bytes.NewReader([]byte("{}")))
	req.Header.Set("Origin", testMemberOrigin)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeComments(t *testing.T, rec *httptest.ResponseRecorder) []commentView {
	t.Helper()
	var out struct {
		Comments []commentView `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode comments: %v (%s)", err, rec.Body.String())
	}
	return out.Comments
}

// TestReplyEndpointAndThreadedGET: a reply posted via the endpoint attaches to its root,
// surfaces in the GET with parentId set, and replying to a reply is refused (one level deep).
func TestReplyEndpointAndThreadedGET(t *testing.T) {
	h, app := newMemberAPI(t)
	_, _, _, aid := seedMember(t, app)
	ck := memberCookie(t, app, aid)

	root, _ := app.AddComment(aid, "/p", "", "root")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)

	if rec := postComment(t, h, map[string]any{"parentId": root.ID, "body": "a reply"}, testMemberOrigin, ck); rec.Code != http.StatusOK {
		t.Fatalf("reply post: %d %s", rec.Code, rec.Body.String())
	}
	// The member reply is pending; approve it so the GET returns it.
	var replyID int64
	all, _ := app.CommentsByPage("/p", "")
	for _, c := range all {
		if c.ParentID != nil {
			replyID = c.ID
			if *c.ParentID != root.ID {
				t.Errorf("reply parent = %d, want %d", *c.ParentID, root.ID)
			}
		}
	}
	if replyID == 0 {
		t.Fatal("reply not stored")
	}
	app.SetCommentStatus(replyID, appstore.CommentApproved)

	cs := decodeComments(t, getAuthed(t, h, "/_pbc/comments?path=/p", nil))
	if len(cs) != 2 {
		t.Fatalf("GET returned %d comments, want 2", len(cs))
	}
	var sawReply bool
	for _, c := range cs {
		if c.ParentID == root.ID {
			sawReply = true
		}
	}
	if !sawReply {
		t.Error("reply not threaded under its root in GET payload")
	}

	// Replying to the reply is rejected (one level deep).
	if rec := postComment(t, h, map[string]any{"parentId": replyID, "body": "nested"}, testMemberOrigin, ck); rec.Code != http.StatusBadRequest {
		t.Errorf("reply-to-reply: status %d, want 400", rec.Code)
	}
}

// TestSelfDeleteEndpoint: a leaf is hard-deleted, a root with replies is tombstoned, and a
// member cannot delete a comment that isn't theirs (404, not an ownership leak).
func TestSelfDeleteEndpoint(t *testing.T) {
	h, app := newMemberAPI(t)
	am, _ := app.CreateAccount(appstore.RoleMember, "")
	bm, _ := app.CreateAccount(appstore.RoleMember, "")
	aid, bid := am.ID, bm.ID
	ckA := memberCookie(t, app, aid)

	// Leaf → hard delete.
	leaf, _ := app.AddComment(aid, "/p", "", "leaf")
	app.SetCommentStatus(leaf.ID, appstore.CommentApproved)
	rec := deleteOwnComment(t, h, leaf.ID, ckA)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete leaf: %d %s", rec.Code, rec.Body.String())
	}
	if got := tombstonedFlag(t, rec); got {
		t.Error("leaf report tombstoned=true, want false")
	}
	if _, ok, _ := app.CommentByID(leaf.ID); ok {
		t.Error("leaf still present after self-delete")
	}

	// Root with a reply → tombstone.
	root, _ := app.AddComment(aid, "/q", "", "root")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)
	reply, _ := app.AddReply(bid, root.ID, "", "reply")
	app.SetCommentStatus(reply.ID, appstore.CommentApproved)

	rec = deleteOwnComment(t, h, root.ID, ckA)
	if rec.Code != http.StatusOK || !tombstonedFlag(t, rec) {
		t.Fatalf("delete root: code %d tombstoned=%v, want 200/true", rec.Code, tombstonedFlag(t, rec))
	}
	if got, ok, _ := app.CommentByID(root.ID); !ok || !got.Deleted() || got.Body != "" {
		t.Errorf("root not tombstoned: %+v", got)
	}
	if _, ok, _ := app.CommentByID(reply.ID); !ok {
		t.Error("reply lost when root tombstoned")
	}

	// A member deleting someone else's comment → 404 (reply belongs to b, not a).
	if rec := deleteOwnComment(t, h, reply.ID, ckA); rec.Code != http.StatusNotFound {
		t.Errorf("cross-member delete: status %d, want 404", rec.Code)
	}
}

// tombstonedFlag reads the {tombstoned} field of a delete response.
func tombstonedFlag(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	var out struct {
		Tombstoned bool `json:"tombstoned"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Tombstoned
}

// TestAliasUniquenessEndpoint: a taken name (case-insensitively) is a 409; a free one succeeds.
func TestAliasUniquenessEndpoint(t *testing.T) {
	h, app := newMemberAPI(t)
	am, _ := app.CreateAccount(appstore.RoleMember, "")
	bm, _ := app.CreateAccount(appstore.RoleMember, "")
	ckA := memberCookie(t, app, am.ID)
	ckB := memberCookie(t, app, bm.ID)

	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "Nova"}, testMemberOrigin, ckA); rec.Code != http.StatusOK {
		t.Fatalf("a claims Nova: %d %s", rec.Code, rec.Body.String())
	}
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "nova"}, testMemberOrigin, ckB); rec.Code != http.StatusConflict {
		t.Errorf("b claims nova: status %d, want 409", rec.Code)
	}
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "Star"}, testMemberOrigin, ckB); rec.Code != http.StatusOK {
		t.Errorf("b claims free name Star: status %d, want 200", rec.Code)
	}
}

// TestMineFlagPerViewer: a comment is flagged mine only in its author's own view.
func TestMineFlagPerViewer(t *testing.T) {
	h, app := newMemberAPI(t)
	am, _ := app.CreateAccount(appstore.RoleMember, "")
	bm, _ := app.CreateAccount(appstore.RoleMember, "")
	aid, bid := am.ID, bm.ID
	c, _ := app.AddComment(aid, "/p", "", "hi")
	app.SetCommentStatus(c.ID, appstore.CommentApproved)

	if cs := decodeComments(t, getAuthed(t, h, "/_pbc/comments?path=/p", memberCookie(t, app, aid))); len(cs) != 1 || !cs[0].Mine {
		t.Errorf("author view mine = %+v, want mine:true", cs)
	}
	if cs := decodeComments(t, getAuthed(t, h, "/_pbc/comments?path=/p", memberCookie(t, app, bid))); len(cs) != 1 || cs[0].Mine {
		t.Errorf("other-member view mine = %+v, want mine:false", cs)
	}
	if cs := decodeComments(t, getAuthed(t, h, "/_pbc/comments?path=/p", nil)); len(cs) != 1 || cs[0].Mine {
		t.Errorf("anonymous view mine = %+v, want mine:false", cs)
	}
}

// TestWidgetThreadingHooks guards that the served widget carries the slice-4 features: reply,
// self-delete, the single-name identity flow, threaded/indented replies, and the badges.
func TestWidgetThreadingHooks(t *testing.T) {
	h, _ := newMemberAPI(t)
	js := getAuthed(t, h, "/_pbc/assets/comments.js", nil).Body.String()
	for _, want := range []string{
		"Post reply", "/comments/", "/delete", "parentId", // reply + self-delete
		"change name", "/account/alias", // single account-level name
		"Commenting as", "pbc-reply", // identity line + indented replies
		"Moderator", "Author", // staff badges
		"[deleted]", // tombstone rendering
	} {
		if !strings.Contains(js, want) {
			t.Errorf("comments.js missing %q", want)
		}
	}
	css := getAuthed(t, h, "/_pbc/assets/comments.css", nil).Body.String()
	for _, want := range []string{".pbc-comment.pbc-reply", ".pbc-comment-mod.pbc-author", ".pbc-comment-you"} {
		if !strings.Contains(css, want) {
			t.Errorf("comments.css missing %q", want)
		}
	}
}

// TestModerateCascadeWarning: the moderator UI's delete confirm warns when a comment has
// replies (deleting a root cascades to its replies).
func TestModerateCascadeWarning(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app)
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	root, _ := app.AddComment(m.ID, "/p", "", "root")
	app.SetCommentStatus(root.ID, appstore.CommentApproved)
	app.AddReply(m.ID, root.ID, "", "reply")

	body := getModerate(t, h, "?status=approved", ck).Body.String()
	if !strings.Contains(body, "The replies are removed with it") || !strings.Contains(body, "its 1 reply") {
		t.Error("moderator delete confirm missing the reply-aware cascade warning")
	}
}

// TestCreatorBadgeRole: a creator's comment reports role:"creator" (Author badge) and mod:true.
func TestCreatorBadgeRole(t *testing.T) {
	h, app := newMemberAPI(t)
	cr, _ := app.CreateAccount(appstore.RoleCreator, "")
	c, _ := app.AddComment(cr.ID, "/p", "Owner", "welcome") // staff auto-approve
	if c.Status != appstore.CommentApproved {
		t.Fatalf("creator comment status = %q, want approved", c.Status)
	}
	cs := decodeComments(t, getAuthed(t, h, "/_pbc/comments?path=/p", nil))
	if len(cs) != 1 || cs[0].Role != appstore.RoleCreator || !cs[0].Mod {
		t.Errorf("creator comment view = %+v, want role=creator mod=true", cs)
	}
}
