package webauthn

import (
	"testing"
	"time"
)

func TestChallengeIssueConsume(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	id, ch, err := s.Issue(nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if id == "" || len(ch) != challengeBytes {
		t.Fatalf("issued id=%q challenge len=%d", id, len(ch))
	}
	got, _, ok := s.Consume(id)
	if !ok {
		t.Fatal("Consume: not found")
	}
	if string(got) != string(ch) {
		t.Error("consumed challenge does not match issued")
	}
}

func TestChallengeCarriesContext(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	type regCtx struct {
		invite string
		handle string
	}
	want := regCtx{invite: "inv-code", handle: "user-handle"}
	id, _, _ := s.Issue(want)

	_, ctx, ok := s.Consume(id)
	if !ok {
		t.Fatal("consume failed")
	}
	got, isReg := ctx.(regCtx)
	if !isReg || got != want {
		t.Errorf("ctx = %#v, want %#v", ctx, want)
	}
}

func TestChallengeSingleUse(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	id, _, _ := s.Issue(nil)
	if _, _, ok := s.Consume(id); !ok {
		t.Fatal("first consume should succeed")
	}
	if _, _, ok := s.Consume(id); ok {
		t.Error("second consume of the same id must fail (single-use)")
	}
}

func TestChallengeUnknownID(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	if _, _, ok := s.Consume("never-issued"); ok {
		t.Error("unknown id must not resolve")
	}
}

func TestChallengeExpiry(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	base := time.Unix(1000, 0)
	s.now = func() time.Time { return base }
	id, _, _ := s.Issue(nil) // expires at 1060

	s.now = func() time.Time { return base.Add(59 * time.Second) }
	if _, _, ok := s.Consume(id); !ok {
		t.Error("challenge should be valid before expiry")
	}

	// Re-issue and let it expire.
	s.now = func() time.Time { return base }
	id2, _, _ := s.Issue(nil)
	s.now = func() time.Time { return base.Add(time.Minute) } // == expiry boundary
	if _, _, ok := s.Consume(id2); ok {
		t.Error("challenge at/after expiry must be treated as absent")
	}
}

func TestChallengeDistinctIDs(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, _, err := s.Issue(nil)
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate ceremony id: %q", id)
		}
		seen[id] = true
	}
}

func TestChallengeGCOnIssue(t *testing.T) {
	s := NewChallengeStore(time.Minute)
	base := time.Unix(1000, 0)
	s.now = func() time.Time { return base }
	// Abandon a ceremony (never consumed).
	s.Issue(nil)
	if len(s.m) != 1 {
		t.Fatalf("map size = %d, want 1", len(s.m))
	}
	// Later Issue past the abandoned one's expiry should sweep it.
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	s.Issue(nil)
	if len(s.m) != 1 {
		t.Errorf("map size = %d after gc, want 1 (abandoned entry swept)", len(s.m))
	}
}
