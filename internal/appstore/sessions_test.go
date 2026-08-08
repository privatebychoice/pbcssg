package appstore

import (
	"testing"
	"time"
)

func TestCreateAndResolveSession(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")

	token, sess, err := s.CreateSession(a.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	// The plaintext token is never stored — only its hash.
	if sess.IDHash == token {
		t.Fatal("id_hash must not equal the plaintext token")
	}
	if sess.IDHash != hashToken(token) {
		t.Error("stored id_hash is not sha256(token)")
	}
	if !sess.ExpiresAt.Equal(time.Unix(1000, 0).Add(time.Hour)) {
		t.Errorf("expires = %v, want now+1h", sess.ExpiresAt)
	}

	got, ok, err := s.SessionByToken(token)
	if err != nil || !ok {
		t.Fatalf("SessionByToken: ok=%v err=%v", ok, err)
	}
	if got.AccountID != a.ID {
		t.Errorf("account = %d, want %d", got.AccountID, a.ID)
	}

	if _, ok, _ := s.SessionByToken("not-a-real-token"); ok {
		t.Error("unknown token resolved to a session")
	}
}

func TestCreateSessionValidation(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	if _, _, err := s.CreateSession(0, time.Hour); err == nil {
		t.Error("zero account id should error")
	}
	if _, _, err := s.CreateSession(a.ID, 0); err == nil {
		t.Error("non-positive ttl should error")
	}
}

func TestSessionExpiryTreatedAsAbsent(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")
	token, _, _ := s.CreateSession(a.ID, time.Hour) // expires at 1000+3600 = 4600

	// Just before expiry: still valid.
	clockAt(s, 4599)
	if _, ok, _ := s.SessionByToken(token); !ok {
		t.Error("session should be valid before expiry")
	}
	// At/after expiry: resolves as absent (no error, no oracle).
	clockAt(s, 4600)
	if _, ok, err := s.SessionByToken(token); ok || err != nil {
		t.Errorf("expired session: ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestTouchSessionDoesNotExtend(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")
	token, _, _ := s.CreateSession(a.ID, time.Hour)

	clockAt(s, 1500)
	if err := s.TouchSession(token); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, ok, _ := s.SessionByToken(token)
	if !ok {
		t.Fatal("session missing after touch")
	}
	if !got.LastSeenAt.Equal(time.Unix(1500, 0)) {
		t.Errorf("last_seen = %v, want 1500", got.LastSeenAt)
	}
	if !got.ExpiresAt.Equal(time.Unix(4600, 0)) {
		t.Errorf("expiry moved to %v, want 4600 (touch must not extend)", got.ExpiresAt)
	}
}

func TestRevokeSession(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	token, _, _ := s.CreateSession(a.ID, time.Hour)

	if err := s.RevokeSession(token); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("session still resolvable after revoke")
	}
	// Revoking again is a no-op, not an error.
	if err := s.RevokeSession(token); err != nil {
		t.Errorf("double revoke should be a no-op: %v", err)
	}
}

func TestRevokeAccountSessions(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")
	ta1, _, _ := s.CreateSession(a.ID, time.Hour)
	ta2, _, _ := s.CreateSession(a.ID, time.Hour)
	tb, _, _ := s.CreateSession(b.ID, time.Hour)

	n, err := s.RevokeAccountSessions(a.ID)
	if err != nil {
		t.Fatalf("RevokeAccountSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("revoked = %d, want 2", n)
	}
	if _, ok, _ := s.SessionByToken(ta1); ok {
		t.Error("a session 1 survived account revoke")
	}
	if _, ok, _ := s.SessionByToken(ta2); ok {
		t.Error("a session 2 survived account revoke")
	}
	// Other account's session is untouched.
	if _, ok, _ := s.SessionByToken(tb); !ok {
		t.Error("unrelated account's session was revoked")
	}
}

func TestPruneExpiredSessions(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")
	short, _, _ := s.CreateSession(a.ID, time.Minute) // expires 1060
	long, _, _ := s.CreateSession(a.ID, 2*time.Hour)  // expires 8200

	clockAt(s, 2000)
	n, err := s.PruneExpiredSessions()
	if err != nil {
		t.Fatalf("PruneExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}
	if _, ok, _ := s.SessionByToken(short); ok {
		t.Error("expired session survived prune")
	}
	if _, ok, _ := s.SessionByToken(long); !ok {
		t.Error("live session was pruned")
	}
}
