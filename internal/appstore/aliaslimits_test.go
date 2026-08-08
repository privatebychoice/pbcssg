package appstore

import (
	"errors"
	"testing"
	"time"
)

// TestAliasDailyCap: an account may change its name up to the cap per day; re-saving the same
// name is a free no-op; a new day resets the tally.
func TestAliasDailyCap(t *testing.T) {
	s := newTestStore(t)
	s.SetAliasDailyCap(2)
	clockAt(s, 100000) // day = 100000/86400
	a, _ := s.CreateAccount(RoleMember, "")

	if _, err := s.SetAccountAlias(a.ID, "N1"); err != nil {
		t.Fatalf("change 1: %v", err)
	}
	if _, err := s.SetAccountAlias(a.ID, "N2"); err != nil {
		t.Fatalf("change 2: %v", err)
	}
	// Third change today exceeds the cap of 2.
	if _, err := s.SetAccountAlias(a.ID, "N3"); !errors.Is(err, ErrAliasRateLimited) {
		t.Fatalf("change 3: got %v, want ErrAliasRateLimited", err)
	}
	// Re-saving the current name is a no-op — no error, no slot consumed.
	if _, err := s.SetAccountAlias(a.ID, "N2"); err != nil {
		t.Errorf("no-op re-save: %v", err)
	}
	// A new day resets the tally.
	clockAt(s, 100000+2*86400)
	if _, err := s.SetAccountAlias(a.ID, "N4"); err != nil {
		t.Errorf("next day should reset the cap: %v", err)
	}
	// A cap of 0 disables the limit.
	s.SetAliasDailyCap(0)
	for i, name := range []string{"M1", "M2", "M3", "M4"} {
		if _, err := s.SetAccountAlias(a.ID, name); err != nil {
			t.Errorf("uncapped change %d (%s): %v", i, name, err)
		}
	}
}

// TestReleaseInactiveAliases: a dormant member's name is freed (account + comments blanked); an
// active member and any staff account keep theirs.
func TestReleaseInactiveAliases(t *testing.T) {
	s := newTestStore(t)
	s.SetAliasDailyCap(0)
	clockAt(s, 1000000)
	active, _ := s.CreateAccount(RoleMember, "")
	dormant, _ := s.CreateAccount(RoleMember, "")
	staff, _ := s.CreateAccount(RoleModerator, "")
	s.SetAccountAlias(active.ID, "Active")
	s.SetAccountAlias(dormant.ID, "Dormant")
	s.SetAccountAlias(staff.ID, "StaffName")
	cm, _ := s.AddComment(dormant.ID, "/p", "Dormant", "hi")

	// Bump the active member's last-seen so only the dormant one predates the cutoff.
	clockAt(s, 5000000)
	s.TouchAccount(active.ID)

	n, err := s.ReleaseInactiveAliases(time.Unix(3000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("released %d aliases, want 1 (dormant member only)", n)
	}
	if got, _, _ := s.AccountByID(dormant.ID); got.Alias != "" {
		t.Errorf("dormant alias = %q, want released", got.Alias)
	}
	if got, _, _ := s.AccountByID(active.ID); got.Alias != "Active" {
		t.Error("active member's alias was released")
	}
	if got, _, _ := s.AccountByID(staff.ID); got.Alias != "StaffName" {
		t.Error("staff alias was released (staff must be exempt)")
	}
	// The freed name is also blanked on the dormant member's comments.
	if c, _, _ := s.CommentByID(cm.ID); c.Alias != "" {
		t.Errorf("dormant comment alias = %q, want blanked on release", c.Alias)
	}
}

// TestPruneChildlessTombstones: an old tombstone with no replies is reclaimed; one that still
// has a reply, and a recent one, are kept.
func TestPruneChildlessTombstones(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")

	clockAt(s, 1000)
	// Childless tombstone: tombstone a root that had a reply, then remove the reply.
	root1, _ := s.AddComment(a.ID, "/p", "", "r1")
	rep1, _ := s.AddReply(a.ID, root1.ID, "", "reply1")
	if tomb, _ := s.DeleteOwnComment(a.ID, root1.ID); !tomb {
		t.Fatal("root1 should have tombstoned")
	}
	s.DeleteComment(rep1.ID) // now root1 is a childless tombstone (deleted_at = 1000)

	// Tombstone that still has a reply — must be kept.
	root2, _ := s.AddComment(a.ID, "/q", "", "r2")
	s.AddReply(a.ID, root2.ID, "", "reply2")
	s.DeleteOwnComment(a.ID, root2.ID)

	// A recent childless tombstone (after the cutoff) — must be kept.
	clockAt(s, 9000)
	root3, _ := s.AddComment(a.ID, "/r", "", "r3")
	rep3, _ := s.AddReply(a.ID, root3.ID, "", "reply3")
	s.DeleteOwnComment(a.ID, root3.ID)
	s.DeleteComment(rep3.ID) // childless, but deleted_at = 9000

	n, err := s.PruneChildlessTombstones(time.Unix(5000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d, want 1 (old childless tombstone only)", n)
	}
	if _, ok, _ := s.CommentByID(root1.ID); ok {
		t.Error("old childless tombstone not pruned")
	}
	if _, ok, _ := s.CommentByID(root2.ID); !ok {
		t.Error("tombstone with a live reply was pruned")
	}
	if _, ok, _ := s.CommentByID(root3.ID); !ok {
		t.Error("recent tombstone (within retention) was pruned")
	}
}
