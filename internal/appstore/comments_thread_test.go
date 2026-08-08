package appstore

import (
	"errors"
	"testing"
)

// TestStaffCommentsAutoApprove: a member's comment starts pending, a moderator's and a
// creator's post straight to approved (they already hold moderation power).
func TestStaffCommentsAutoApprove(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct {
		role, want string
	}{
		{RoleMember, CommentPending},
		{RoleModerator, CommentApproved},
		{RoleCreator, CommentApproved},
	} {
		a, _ := s.CreateAccount(tc.role, "")
		c, err := s.AddComment(a.ID, "/p", "", "hi")
		if err != nil {
			t.Fatalf("%s AddComment: %v", tc.role, err)
		}
		if c.Status != tc.want {
			t.Errorf("%s comment status = %q, want %q", tc.role, c.Status, tc.want)
		}
	}
}

// TestAddReplyOneLevel: a reply attaches to a root, inherits the root's page path regardless
// of any caller intent, and replying to a reply is refused (one level deep).
func TestAddReplyOneLevel(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	root, _ := s.AddComment(a.ID, "/post", "", "root")

	reply, err := s.AddReply(a.ID, root.ID, "", "a reply")
	if err != nil {
		t.Fatalf("AddReply: %v", err)
	}
	if reply.ParentID == nil || *reply.ParentID != root.ID {
		t.Errorf("reply.ParentID = %v, want %d", reply.ParentID, root.ID)
	}
	if reply.PagePath != "/post" {
		t.Errorf("reply inherited page = %q, want /post", reply.PagePath)
	}
	// Replying to the reply must be rejected.
	if _, err := s.AddReply(a.ID, reply.ID, "", "nested"); err == nil {
		t.Error("reply-to-a-reply should be rejected (one level deep)")
	}
	// Replying to a missing parent is rejected.
	if _, err := s.AddReply(a.ID, 99999, "", "orphan"); err == nil {
		t.Error("reply to nonexistent parent should error")
	}
}

// TestDeleteOwnCommentLeaf: a comment with no replies is hard-deleted by its author.
func TestDeleteOwnCommentLeaf(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	c, _ := s.AddComment(a.ID, "/p", "Nova", "bye")

	tombstoned, err := s.DeleteOwnComment(a.ID, c.ID)
	if err != nil {
		t.Fatalf("DeleteOwnComment: %v", err)
	}
	if tombstoned {
		t.Error("leaf comment should be hard-deleted, not tombstoned")
	}
	if _, ok, _ := s.CommentByID(c.ID); ok {
		t.Error("leaf comment still present after self-delete")
	}
}

// TestDeleteOwnCommentTombstonesRoot: deleting a root that still has replies keeps the row as
// a tombstone (body/alias blanked, detached) so the replies keep their context.
func TestDeleteOwnCommentTombstonesRoot(t *testing.T) {
	s := newTestStore(t)
	author, _ := s.CreateAccount(RoleMember, "")
	other, _ := s.CreateAccount(RoleMember, "")
	root, _ := s.AddComment(author.ID, "/p", "Nova", "root")
	reply, _ := s.AddReply(other.ID, root.ID, "Ivy", "a reply")

	tombstoned, err := s.DeleteOwnComment(author.ID, root.ID)
	if err != nil {
		t.Fatalf("DeleteOwnComment: %v", err)
	}
	if !tombstoned {
		t.Fatal("root with replies should be tombstoned, not hard-deleted")
	}
	got, ok, _ := s.CommentByID(root.ID)
	if !ok {
		t.Fatal("tombstoned root should still exist")
	}
	if !got.Deleted() {
		t.Error("root not marked deleted")
	}
	if got.Body != "" || got.Alias != "" || got.AccountID != nil {
		t.Errorf("tombstone not blanked/detached: body=%q alias=%q account=%v", got.Body, got.Alias, got.AccountID)
	}
	// The reply survives, still linked to its author.
	if r, ok, _ := s.CommentByID(reply.ID); !ok || r.AccountID == nil || *r.AccountID != other.ID {
		t.Error("reply lost after root tombstoned")
	}
}

// TestDeleteOwnCommentOwnership: a member cannot delete another member's comment, and the
// error is ErrNotFound (indistinguishable from a missing id, so ids aren't probed).
func TestDeleteOwnCommentOwnership(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")
	c, _ := s.AddComment(a.ID, "/p", "", "mine")

	if _, err := s.DeleteOwnComment(b.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-member delete: got %v, want ErrNotFound", err)
	}
	if _, ok, _ := s.CommentByID(c.ID); !ok {
		t.Error("comment wrongly deleted by non-owner")
	}
	if _, err := s.DeleteOwnComment(a.ID, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing id: got %v, want ErrNotFound", err)
	}
}

// TestReplyCounts: reply tallies per parent, zero for childless ids, empty for no input.
func TestReplyCounts(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	r1, _ := s.AddComment(a.ID, "/p", "", "root1")
	r2, _ := s.AddComment(a.ID, "/p", "", "root2")
	s.AddReply(a.ID, r1.ID, "", "one")
	s.AddReply(a.ID, r1.ID, "", "two")

	counts, err := s.ReplyCounts([]int64{r1.ID, r2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if counts[r1.ID] != 2 {
		t.Errorf("r1 replies = %d, want 2", counts[r1.ID])
	}
	if _, ok := counts[r2.ID]; ok {
		t.Errorf("r2 has no replies, should be absent from the map: %v", counts)
	}
	if m, _ := s.ReplyCounts(nil); len(m) != 0 {
		t.Errorf("nil ids should give empty map, got %v", m)
	}
}

// TestModeratorDeleteCascadesReplies documents the DB-level rule: a moderator hard-deleting a
// root (DeleteComment) takes its replies with it via ON DELETE CASCADE, so no reply is ever
// left pointing at a missing parent.
func TestModeratorDeleteCascadesReplies(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	root, _ := s.AddComment(a.ID, "/p", "", "root")
	reply, _ := s.AddReply(a.ID, root.ID, "", "reply")

	if err := s.DeleteComment(root.ID); err != nil {
		t.Fatalf("DeleteComment root: %v", err)
	}
	if _, ok, _ := s.CommentByID(reply.ID); ok {
		t.Error("reply not cascade-deleted with its root")
	}
}
