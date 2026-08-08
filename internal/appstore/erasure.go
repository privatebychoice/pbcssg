package appstore

import (
	"database/sql"
	"fmt"
	"time"
)

// DeleteCommentsByAccount hard-deletes every comment authored by an account and
// returns the count removed. Used by the "delete" branch of erasure and by ban
// content-removal.
func (s *Store) DeleteCommentsByAccount(accountID int64) (int, error) {
	res, err := s.db.Exec(`DELETE FROM comments WHERE account_id = ?`, accountID)
	if err != nil {
		return 0, fmt.Errorf("appstore: delete comments for account %d: %w", accountID, err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AnonymizeCommentsByAccount severs an account's comments from it — nulls the
// internal author link and blanks the chosen alias — while keeping the comment
// body. This is the "anonymize" branch of forget-me: the words stay, the identity
// does not. Returns the count affected.
func (s *Store) AnonymizeCommentsByAccount(accountID int64) (int, error) {
	res, err := s.db.Exec(
		`UPDATE comments SET account_id = NULL, alias = '', updated_at = ? WHERE account_id = ?`,
		s.now().Unix(), accountID,
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: anonymize comments for account %d: %w", accountID, err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ForgetAccount performs a self-service "forget me" erasure (§2.4): it removes the
// account and everything that identifies its owner. Credentials and sessions go via
// ON DELETE CASCADE; comments are handled per keepComments — anonymized (link
// nulled, alias blanked, body kept) when true, hard-deleted when false. The whole
// operation is one transaction, so a member is either fully erased or not at all.
// Returns ErrNotFound if no such account exists.
func (s *Store) ForgetAccount(accountID int64, keepComments bool) error {
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM accounts WHERE id = ?`, accountID).Scan(&one); {
	case err == sql.ErrNoRows:
		return fmt.Errorf("appstore: account %d: %w", accountID, ErrNotFound)
	case err != nil:
		return fmt.Errorf("appstore: forget lookup: %w", err)
	}

	if keepComments {
		if _, err := tx.Exec(
			`UPDATE comments SET account_id = NULL, alias = '', updated_at = ? WHERE account_id = ?`,
			now, accountID,
		); err != nil {
			return fmt.Errorf("appstore: forget anonymize: %w", err)
		}
	} else if _, err := tx.Exec(`DELETE FROM comments WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("appstore: forget delete comments: %w", err)
	}

	// Deleting the account cascades credentials and sessions; comments were already
	// detached above (so the ON DELETE SET NULL has nothing left to touch).
	if _, err := tx.Exec(`DELETE FROM accounts WHERE id = ?`, accountID); err != nil {
		return fmt.Errorf("appstore: forget delete account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appstore: forget commit: %w", err)
	}
	return nil
}

// PurgeInactiveMembers is the lost-passkey backstop (§2.4): it erases every member
// account whose last activity predates `before`. Only members are purged — moderators
// and creators are staff, never auto-removed (a creator can be dormant for months and
// still own the site). Each purged account's comments are anonymized (keepComments,
// link nulled + alias blanked, body kept) or deleted, then the account row is removed
// (credentials and sessions cascade). The whole sweep is one transaction, so it is all
// or nothing. Returns the number of accounts purged.
func (s *Store) PurgeInactiveMembers(before time.Time, keepComments bool) (int, error) {
	cutoff := before.Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// The set of members being purged, referenced by both the comment step and the
	// account delete so they stay in lockstep.
	sel := `SELECT id FROM accounts WHERE role = ? AND last_seen_at < ?`
	if keepComments {
		if _, err := tx.Exec(
			`UPDATE comments SET account_id = NULL, alias = '', updated_at = ? WHERE account_id IN (`+sel+`)`,
			s.now().Unix(), RoleMember, cutoff,
		); err != nil {
			return 0, fmt.Errorf("appstore: purge anonymize: %w", err)
		}
	} else if _, err := tx.Exec(
		`DELETE FROM comments WHERE account_id IN (`+sel+`)`, RoleMember, cutoff,
	); err != nil {
		return 0, fmt.Errorf("appstore: purge delete comments: %w", err)
	}

	res, err := tx.Exec(`DELETE FROM accounts WHERE role = ? AND last_seen_at < ?`, RoleMember, cutoff)
	if err != nil {
		return 0, fmt.Errorf("appstore: purge accounts: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("appstore: purge commit: %w", err)
	}
	return int(n), nil
}

// ReleaseInactiveAliases frees the display name held by dormant MEMBER accounts (last activity
// before cutoff), so a squatted name returns to the pool (§F3). It clears the account's alias
// AND blanks that alias on the account's comments in one transaction — blanking the comments too
// is deliberate: a released name left on old comments could be exploited by a new claimant, so
// the name disappears everywhere at once. Staff aliases are never auto-released. Returns the
// number of accounts whose alias was released.
func (s *Store) ReleaseInactiveAliases(before time.Time) (int, error) {
	cutoff := before.Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// The dormant, still-named members — referenced by both steps so they stay in lockstep. The
	// comment blanking runs first, while the accounts still carry the alias that selects them.
	sel := `SELECT id FROM accounts WHERE role = ? AND alias <> '' AND last_seen_at < ?`
	if _, err := tx.Exec(
		`UPDATE comments SET alias = '', updated_at = ? WHERE account_id IN (`+sel+`)`,
		s.now().Unix(), RoleMember, cutoff,
	); err != nil {
		return 0, fmt.Errorf("appstore: release aliases (comments): %w", err)
	}
	res, err := tx.Exec(`UPDATE accounts SET alias = '' WHERE role = ? AND alias <> '' AND last_seen_at < ?`, RoleMember, cutoff)
	if err != nil {
		return 0, fmt.Errorf("appstore: release aliases (accounts): %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("appstore: release aliases commit: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// BanAccount performs the account-level ban composition (§2.4) atomically: flag the
// account banned, revoke all its sessions, optionally remove its posts, and burn
// the invite that created it (by lineage) so re-entry needs a fresh invite. Unlike
// erasure the account row is kept (flagged), so the ban is a durable record. WebAuthn
// is deliberately unlinkable, so there is no device to ban — this is the whole
// enforcement. Returns ErrNotFound if no such account exists.
func (s *Store) BanAccount(accountID int64, removeContent bool) error {
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lineage string
	switch err := tx.QueryRow(`SELECT invite_lineage FROM accounts WHERE id = ?`, accountID).Scan(&lineage); {
	case err == sql.ErrNoRows:
		return fmt.Errorf("appstore: account %d: %w", accountID, ErrNotFound)
	case err != nil:
		return fmt.Errorf("appstore: ban lookup: %w", err)
	}

	if _, err := tx.Exec(`UPDATE accounts SET status = ? WHERE id = ?`, StatusBanned, accountID); err != nil {
		return fmt.Errorf("appstore: ban flag: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("appstore: ban revoke sessions: %w", err)
	}
	if removeContent {
		if _, err := tx.Exec(`DELETE FROM comments WHERE account_id = ?`, accountID); err != nil {
			return fmt.Errorf("appstore: ban remove content: %w", err)
		}
	}
	if lineage != "" {
		if _, err := tx.Exec(
			`UPDATE invites SET revoked_at = ? WHERE lineage = ? AND revoked_at = 0`, now, lineage,
		); err != nil {
			return fmt.Errorf("appstore: ban burn invite: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appstore: ban commit: %w", err)
	}
	return nil
}
