package appstore

import (
	"database/sql"
	"fmt"
	"time"
)

// Credential is one WebAuthn public-key credential belonging to an account. An
// account may have many (creators/moderators register >=2). The COSE public key is
// not secret; the private key never leaves the authenticator.
type Credential struct {
	ID         int64
	AccountID  int64
	CredID     string // base64url of the raw credential ID
	PublicKey  []byte // COSE public key
	SignCount  uint32 // clone-detection counter
	AAGUID     string // empty under attestation:none
	Transports string // comma-joined hints, display only
	Label      string // user-facing "which key" label
	CreatedAt  time.Time
	LastUsedAt time.Time // zero until first assertion
}

// AddCredential stores a new credential for an account and returns its row id. The
// account must exist (enforced by the foreign key) and CredID must be unique across
// all accounts (a duplicate is a UNIQUE-constraint error).
func (s *Store) AddCredential(c Credential) (int64, error) {
	if c.AccountID == 0 {
		return 0, fmt.Errorf("appstore: add credential: account id required")
	}
	if c.CredID == "" || len(c.PublicKey) == 0 {
		return 0, fmt.Errorf("appstore: add credential: cred_id and public_key required")
	}
	now := s.now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO credentials
		   (account_id, cred_id, public_key, sign_count, aaguid, transports, label, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		c.AccountID, c.CredID, c.PublicKey, c.SignCount, c.AAGUID, c.Transports, c.Label, now,
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: add credential: %w", err)
	}
	return res.LastInsertId()
}

const credentialCols = `id, account_id, cred_id, public_key, sign_count, aaguid, transports, label, created_at, last_used_at`

// scanCredential scans one credential row in credentialCols order.
func scanCredential(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var count, created, used int64
	if err := row.Scan(&c.ID, &c.AccountID, &c.CredID, &c.PublicKey, &count, &c.AAGUID, &c.Transports, &c.Label, &created, &used); err != nil {
		return Credential{}, err
	}
	c.SignCount = uint32(count)
	c.CreatedAt = time.Unix(created, 0)
	if used != 0 {
		c.LastUsedAt = time.Unix(used, 0)
	}
	return c, nil
}

// CredentialByCredID looks up a credential by its raw credential ID — the lookup a
// WebAuthn assertion performs to find the stored public key and expected account.
// ok is false (nil error) when no credential matches.
func (s *Store) CredentialByCredID(credID string) (Credential, bool, error) {
	c, err := scanCredential(s.db.QueryRow(`SELECT `+credentialCols+` FROM credentials WHERE cred_id = ?`, credID))
	switch {
	case err == sql.ErrNoRows:
		return Credential{}, false, nil
	case err != nil:
		return Credential{}, false, fmt.Errorf("appstore: credential %q: %w", credID, err)
	}
	return c, true, nil
}

// CredentialsForAccount returns an account's credentials, oldest first. The count
// backs the ">=2 authenticators for elevated roles" rule (§2.4) and the "don't
// remove your last key" guard a caller may enforce.
func (s *Store) CredentialsForAccount(accountID int64) ([]Credential, error) {
	rows, err := s.db.Query(`SELECT `+credentialCols+` FROM credentials WHERE account_id = ? ORDER BY created_at, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("appstore: credentials for account %d: %w", accountID, err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateSignCount records the authenticator's new signature counter after a
// successful assertion and stamps last-used. Callers verify monotonicity for
// single-device keys before calling; synced passkeys may report 0, so this method
// stores whatever the caller passes rather than enforcing an increase itself.
func (s *Store) UpdateSignCount(credID string, newCount uint32) error {
	res, err := s.db.Exec(
		`UPDATE credentials SET sign_count = ?, last_used_at = ? WHERE cred_id = ?`,
		newCount, s.now().Unix(), credID,
	)
	if err != nil {
		return fmt.Errorf("appstore: update sign count %q: %w", credID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("appstore: credential %q: %w", credID, ErrNotFound)
	}
	return nil
}

// SetCredentialLabel renames a credential, scoped to its owning account so a caller
// cannot relabel another account's key by id. The label is a display-only "which key"
// hint (§2.4). Returns ErrNotFound if no such (id, accountID) credential exists.
func (s *Store) SetCredentialLabel(id, accountID int64, label string) error {
	res, err := s.db.Exec(`UPDATE credentials SET label = ? WHERE id = ? AND account_id = ?`, label, id, accountID)
	if err != nil {
		return fmt.Errorf("appstore: set credential %d label: %w", id, err)
	}
	return mustAffectOne(res, "credential", id)
}

// DeleteCredential removes one credential, scoped to its owning account so a caller
// cannot delete another account's key by id. Returns ErrNotFound if no such
// (id, accountID) credential exists. Any "must keep at least one key" policy is
// enforced above this primitive.
func (s *Store) DeleteCredential(id, accountID int64) error {
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return fmt.Errorf("appstore: delete credential %d: %w", id, err)
	}
	return mustAffectOne(res, "credential", id)
}
