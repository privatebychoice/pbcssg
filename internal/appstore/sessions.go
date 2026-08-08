package appstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// sessionTokenBytes is the entropy of a session token: 256 bits of crypto/rand.
// Because the token is high-entropy (not password-like), the stored id_hash is a
// plain SHA-256 of it — a slow KDF would add latency for no security gain (§2.4).
const sessionTokenBytes = 32

// Session is a server-side authenticated session. The cookie carries only the
// opaque token; the database stores its SHA-256 (IDHash), so a DB or backup leak
// yields no usable session secret.
type Session struct {
	IDHash     string
	AccountID  int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// hashToken returns the hex SHA-256 of a session token — the primary key stored in
// the sessions table. Lookups hash the presented token and match on this.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession mints a new session for an account valid for ttl, and returns the
// plaintext token (the ONLY time it exists outside the client) plus the stored
// Session. The caller places the token in the __Host- HttpOnly SameSite=Strict
// cookie; only its hash is persisted.
func (s *Store) CreateSession(accountID int64, ttl time.Duration) (token string, sess Session, err error) {
	if accountID == 0 {
		return "", Session{}, fmt.Errorf("appstore: create session: account id required")
	}
	if ttl <= 0 {
		return "", Session{}, fmt.Errorf("appstore: create session: ttl must be positive")
	}
	token, err = randB64URL(sessionTokenBytes)
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	sess = Session{
		IDHash:     hashToken(token),
		AccountID:  accountID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		LastSeenAt: now,
	}
	if _, err := s.db.Exec(
		`INSERT INTO sessions (id_hash, account_id, created_at, expires_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sess.IDHash, accountID, now.Unix(), sess.ExpiresAt.Unix(), now.Unix(),
	); err != nil {
		return "", Session{}, fmt.Errorf("appstore: create session: %w", err)
	}
	return token, sess, nil
}

// SessionByToken resolves a presented token to its (unexpired) session. An expired
// or unknown token returns ok=false with a nil error — the caller treats both as
// "not authenticated" and cannot distinguish them (no oracle).
func (s *Store) SessionByToken(token string) (Session, bool, error) {
	var sess Session
	var created, expires, seen int64
	err := s.db.QueryRow(
		`SELECT id_hash, account_id, created_at, expires_at, last_seen_at
		   FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		hashToken(token), s.now().Unix(),
	).Scan(&sess.IDHash, &sess.AccountID, &created, &expires, &seen)
	switch {
	case err == sql.ErrNoRows:
		return Session{}, false, nil
	case err != nil:
		return Session{}, false, fmt.Errorf("appstore: session lookup: %w", err)
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.ExpiresAt = time.Unix(expires, 0)
	sess.LastSeenAt = time.Unix(seen, 0)
	return sess, true, nil
}

// TouchSession bumps a session's last-seen timestamp (idle tracking). It matches
// only unexpired sessions and is a no-op otherwise; it does not extend expiry.
func (s *Store) TouchSession(token string) error {
	now := s.now().Unix()
	if _, err := s.db.Exec(
		`UPDATE sessions SET last_seen_at = ? WHERE id_hash = ? AND expires_at > ?`,
		now, hashToken(token), now,
	); err != nil {
		return fmt.Errorf("appstore: touch session: %w", err)
	}
	return nil
}

// RevokeSession deletes a single session (logout). Deleting an absent/expired
// session is not an error — the end state (gone) is the same.
func (s *Store) RevokeSession(token string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id_hash = ?`, hashToken(token)); err != nil {
		return fmt.Errorf("appstore: revoke session: %w", err)
	}
	return nil
}

// RevokeAccountSessions deletes every session for an account and returns how many
// were removed. This is the "revoke its sessions" step of a ban and part of
// forget-me erasure (§2.4).
func (s *Store) RevokeAccountSessions(accountID int64) (int, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE account_id = ?`, accountID)
	if err != nil {
		return 0, fmt.Errorf("appstore: revoke account %d sessions: %w", accountID, err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PruneExpiredSessions deletes sessions whose expiry has passed and returns the
// count removed. Safe to call periodically as housekeeping.
func (s *Store) PruneExpiredSessions() (int, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, s.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("appstore: prune sessions: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
