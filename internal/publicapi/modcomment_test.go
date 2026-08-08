package publicapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

func TestModeratorCanComment(t *testing.T) {
	h, app := newMemberAPI(t)
	ck := seedModeratorSession(t, app) // a moderator session, not a member

	rec := modPostAuthed(t, h, "/_pbc/comments", map[string]string{"path": "/p", "alias": "ModName", "body": "a moderator note"}, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderator comment: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := app.CommentsByPage("/p", "")
	if len(got) != 1 {
		t.Fatalf("comments = %d, want 1", len(got))
	}
	if got[0].AuthorRole != appstore.RoleModerator {
		t.Errorf("author_role = %q, want moderator (drives the MOD badge)", got[0].AuthorRole)
	}
}

func TestCommentsGETReportsModBadge(t *testing.T) {
	h, app := newMemberAPI(t)
	mem, _ := app.CreateAccount(appstore.RoleMember, "")
	mc, _ := app.AddComment(mem.ID, "/p", "member1", "hi")
	app.SetCommentStatus(mc.ID, appstore.CommentApproved)
	mod, _ := app.CreateAccount(appstore.RoleModerator, "")
	modc, _ := app.AddComment(mod.ID, "/p", "staff1", "notice")
	app.SetCommentStatus(modc.ID, appstore.CommentApproved)

	req := httptest.NewRequest(http.MethodGet, "/_pbc/comments?path=/p", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Comments []struct {
			Alias string `json:"alias"`
			Mod   bool   `json:"mod"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byAlias := map[string]bool{}
	for _, c := range out.Comments {
		byAlias[c.Alias] = c.Mod
	}
	if !byAlias["staff1"] {
		t.Error("moderator-authored comment should report mod:true")
	}
	if byAlias["member1"] {
		t.Error("member-authored comment should report mod:false")
	}
}
