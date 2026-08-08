package appstore

import (
	"testing"
	"time"
)

func TestPruneSpentInvites(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100000)
	mod, _ := s.CreateAccount(RoleModerator, "")

	// Live (no expiry, unredeemed) — kept.
	s.MintInvite(MintParams{Role: RoleMember, TTL: 0, IssuedBy: mod.ID})
	// Expired, unredeemed — prunable.
	s.MintInvite(MintParams{Role: RoleMember, TTL: time.Second, IssuedBy: mod.ID})
	// Revoked, unredeemed — prunable.
	revCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0, IssuedBy: mod.ID})
	if err := s.RevokeInvite(revCode); err != nil {
		t.Fatal(err)
	}
	// Redeemed — KEPT (provenance record).
	redCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0, IssuedBy: mod.ID})
	if _, err := s.RedeemInvite(redCode); err != nil {
		t.Fatal(err)
	}

	// 40 days later, prune anything dead > 30 days.
	clockAt(s, 100000+int64((40*24*time.Hour)/time.Second))
	cutoff := s.now().Add(-30 * 24 * time.Hour)
	n, err := s.PruneSpentInvites(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("pruned %d, want 2 (expired + revoked, both unredeemed)", n)
	}
	remaining, _ := s.Invites()
	if len(remaining) != 2 {
		t.Errorf("remaining = %d, want 2 (live + redeemed kept)", len(remaining))
	}
	// The redeemed one must still be present (provenance).
	var redeemedKept bool
	for _, inv := range remaining {
		if !inv.RedeemedAt.IsZero() {
			redeemedKept = true
		}
	}
	if !redeemedKept {
		t.Error("redeemed invite was pruned — provenance lost")
	}
}

func TestPruneRejectedComments(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	clockAt(s, 1000)
	rej, _ := s.AddComment(a.ID, "/p", "", "spam")
	s.SetCommentStatus(rej.ID, CommentRejected) // updated_at = 1000
	pend, _ := s.AddComment(a.ID, "/p", "", "waiting")
	ap, _ := s.AddComment(a.ID, "/p", "", "good")
	s.SetCommentStatus(ap.ID, CommentApproved)

	n, err := s.PruneRejectedComments(time.Unix(2000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d rejected, want 1", n)
	}
	if _, ok, _ := s.CommentByID(rej.ID); ok {
		t.Error("rejected comment not pruned")
	}
	if _, ok, _ := s.CommentByID(pend.ID); !ok {
		t.Error("pending comment was deleted")
	}
	if _, ok, _ := s.CommentByID(ap.ID); !ok {
		t.Error("approved comment was deleted")
	}
}

func TestPruneOrphanedComments(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	clockAt(s, 1000)
	live, _ := s.AddComment(a.ID, "/live", "", "on a live page")
	oldOrphan, _ := s.AddComment(a.ID, "/gone", "", "old orphan")
	clockAt(s, 6000)
	recentOrphan, _ := s.AddComment(a.ID, "/gone", "", "recent orphan")

	// nil livePaths is a hard no-op (never wipe when the page list can't be read).
	if n, _ := s.PruneOrphanedComments(nil, time.Unix(9000, 0)); n != 0 {
		t.Errorf("nil livePaths pruned %d, want 0", n)
	}

	// /live exists; /gone doesn't. Only orphans older than the cutoff go.
	n, err := s.PruneOrphanedComments(map[string]bool{"/live": true}, time.Unix(5000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1 (old orphan only)", n)
	}
	if _, ok, _ := s.CommentByID(live.ID); !ok {
		t.Error("live-page comment was deleted")
	}
	if _, ok, _ := s.CommentByID(oldOrphan.ID); ok {
		t.Error("old orphan not pruned")
	}
	if _, ok, _ := s.CommentByID(recentOrphan.ID); !ok {
		t.Error("recent orphan (within retention) was deleted")
	}
}

func TestVacuum(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	s.AddComment(a.ID, "/p", "", "x")
	if err := s.Vacuum(); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	// Store still usable after vacuum.
	if _, ok, err := s.AccountByID(a.ID); err != nil || !ok {
		t.Errorf("store unusable after vacuum: ok=%v err=%v", ok, err)
	}
}
