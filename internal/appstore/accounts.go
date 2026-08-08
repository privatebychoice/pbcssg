package appstore

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// handleBytes is the length of a generated WebAuthn user handle. 32 bytes (256
// bits) is well within the spec's 1–64 byte range and makes handle collisions
// negligible. The handle is opaque and carries no PII (§2.4).
const handleBytes = 32

// ErrNotFound is returned by lookups when no matching row exists. Callers that
// need to distinguish "absent" from "error" check for it; most use the (_, ok,
// err) form instead.
var ErrNotFound = errors.New("appstore: not found")

// ErrAliasTaken is returned by SetAccountAlias when another account already holds the
// requested display name (compared case-insensitively). The empty alias ("anonymous") is
// shared and is never "taken".
var ErrAliasTaken = errors.New("appstore: alias already in use")

// ErrAliasRateLimited is returned by SetAccountAlias when the account has already changed its
// display name the configured maximum number of times today (anti-churn cap, §F3).
var ErrAliasRateLimited = errors.New("appstore: alias change rate limit reached")

// Account is one Community Member, moderator, or creator. Identity is a passkey;
// the struct holds no PII — only the opaque handle, role, status, and timestamps.
type Account struct {
	ID            int64
	UserHandle    string // base64url (no padding) of random bytes
	Role          string // member | moderator | creator
	Status        string // active | banned
	InviteLineage string // opaque lineage id of the creating invite
	CanInvite     bool   // moderator elevated grant: may mint member invites (creator-granted)
	CanBan        bool   // moderator elevated grant: may soft-ban members (creator-granted)
	Label         string // creator's private staff label; set only for moderators/creators (§2.4), never public
	Alias         string // account-level PUBLIC display name ("" = anonymous); unique case-insensitively when non-empty
	CreatedAt     time.Time
	LastSeenAt    time.Time
}

// validRole reports whether r is a known role.
func validRole(r string) bool {
	switch r {
	case RoleMember, RoleModerator, RoleCreator:
		return true
	}
	return false
}

// randB64URL returns n cryptographically-random bytes as base64url (no padding),
// a form safe for both user handles and cookie values.
func randB64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("appstore: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newHandle returns a fresh opaque user handle (base64url, no padding).
func newHandle() (string, error) { return randB64URL(handleBytes) }

// CreateAccount creates an account with the given role and invite lineage, mints a
// random user handle, and returns the stored Account. An empty role defaults to
// member; an unknown role is rejected. lineage may be empty (e.g. the bootstrap
// creator account) but is normally the lineage id of the redeemed invite.
func (s *Store) CreateAccount(role, lineage string) (Account, error) {
	if role == "" {
		role = RoleMember
	}
	if !validRole(role) {
		return Account{}, fmt.Errorf("appstore: invalid role %q", role)
	}
	handle, err := newHandle()
	if err != nil {
		return Account{}, err
	}
	now := s.now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO accounts (user_handle, role, status, invite_lineage, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		handle, role, StatusActive, lineage, now, now,
	)
	if err != nil {
		return Account{}, fmt.Errorf("appstore: create account: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	return Account{
		ID: id, UserHandle: handle, Role: role, Status: StatusActive,
		InviteLineage: lineage, CreatedAt: time.Unix(now, 0), LastSeenAt: time.Unix(now, 0),
	}, nil
}

// scanAccount scans one account row in the column order used by the queries below.
func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var created, seen, canInvite, canBan int64
	if err := row.Scan(&a.ID, &a.UserHandle, &a.Role, &a.Status, &a.InviteLineage, &canInvite, &canBan, &a.Label, &a.Alias, &created, &seen); err != nil {
		return Account{}, err
	}
	a.CanInvite = canInvite != 0
	a.CanBan = canBan != 0
	a.CreatedAt = time.Unix(created, 0)
	a.LastSeenAt = time.Unix(seen, 0)
	return a, nil
}

const accountCols = `id, user_handle, role, status, invite_lineage, can_invite, can_ban, label, alias, created_at, last_seen_at`

// AccountByID returns the account with the given id. ok is false (nil error) when
// no such account exists.
func (s *Store) AccountByID(id int64) (Account, bool, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM accounts WHERE id = ?`, id))
	switch {
	case err == sql.ErrNoRows:
		return Account{}, false, nil
	case err != nil:
		return Account{}, false, fmt.Errorf("appstore: account %d: %w", id, err)
	}
	return a, true, nil
}

// AccountByHandle returns the account with the given opaque user handle. This is
// the lookup a WebAuthn assertion uses: a usernameless (discoverable) credential
// returns the user handle, which resolves to the account here.
func (s *Store) AccountByHandle(handle string) (Account, bool, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM accounts WHERE user_handle = ?`, handle))
	switch {
	case err == sql.ErrNoRows:
		return Account{}, false, nil
	case err != nil:
		return Account{}, false, fmt.Errorf("appstore: account by handle: %w", err)
	}
	return a, true, nil
}

// Accounts returns every account, newest first. It backs the account-moderation list
// (§2.4); the store holds no PII, so this exposes only opaque handles, roles, statuses,
// and timestamps.
func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT ` + accountCols + ` FROM accounts ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("appstore: list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// InviterLabelByAccount maps each account to a human descriptor of who invited it — the
// issuing operator's staff label (or "owner" / "a moderator" when the issuer is unlabeled)
// — for the creator's accountability view of which moderator let which members in (§2.4,
// anti-bot provenance). Accounts whose creating invite records no issuer are absent.
func (s *Store) InviterLabelByAccount() (map[int64]string, error) {
	rows, err := s.db.Query(`
		SELECT a.id, iss.label, iss.role
		FROM accounts a
		JOIN invites inv ON inv.lineage = a.invite_lineage
		JOIN accounts iss ON iss.id = inv.issued_by`)
	if err != nil {
		return nil, fmt.Errorf("appstore: inviter labels: %w", err)
	}
	defer rows.Close()
	m := make(map[int64]string)
	for rows.Next() {
		var id int64
		var label, role string
		if err := rows.Scan(&id, &label, &role); err != nil {
			return nil, err
		}
		switch {
		case label != "":
			m[id] = label
		case role == RoleCreator:
			m[id] = "owner"
		default:
			m[id] = "a moderator"
		}
	}
	return m, rows.Err()
}

// CountAccountsByRole returns how many accounts hold the given role. The admin
// bootstrap uses CountAccountsByRole(RoleCreator) to warn when a creator already
// exists (§2.4).
func (s *Store) CountAccountsByRole(role string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE role = ?`, role).Scan(&n); err != nil {
		return 0, fmt.Errorf("appstore: count accounts by role %q: %w", role, err)
	}
	return n, nil
}

// TouchAccount updates the account's last-seen timestamp (called on activity). It
// is a no-op error if the account does not exist.
func (s *Store) TouchAccount(id int64) error {
	if _, err := s.db.Exec(`UPDATE accounts SET last_seen_at = ? WHERE id = ?`, s.now().Unix(), id); err != nil {
		return fmt.Errorf("appstore: touch account %d: %w", id, err)
	}
	return nil
}

// SetAccountStatus sets an account's status (active | banned). Banning does not by
// itself revoke sessions or remove content — the moderation flow composes those
// steps (§2.4); this is the account-state primitive.
func (s *Store) SetAccountStatus(id int64, status string) error {
	if status != StatusActive && status != StatusBanned {
		return fmt.Errorf("appstore: invalid status %q", status)
	}
	res, err := s.db.Exec(`UPDATE accounts SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("appstore: set status account %d: %w", id, err)
	}
	return mustAffectOne(res, "account", id)
}

// SetAccountRole changes an account's role (e.g. promoting a member to moderator).
func (s *Store) SetAccountRole(id int64, role string) error {
	if !validRole(role) {
		return fmt.Errorf("appstore: invalid role %q", role)
	}
	res, err := s.db.Exec(`UPDATE accounts SET role = ? WHERE id = ?`, role, id)
	if err != nil {
		return fmt.Errorf("appstore: set role account %d: %w", id, err)
	}
	return mustAffectOne(res, "account", id)
}

// SoftBanAccount is the moderator's ban: it flags the account banned and revokes its
// live sessions, in one transaction — nothing more. Unlike BanAccount (the creator's
// fuller ban) it does not burn the creating invite or remove content, and unlike
// ForgetAccount it deletes nothing: a moderator can block a member, but the "final"
// erase stays with the creator (§2.4). It is reversible via SetAccountStatus(active).
// The caller restricts this to member accounts.
func (s *Store) SoftBanAccount(accountID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE accounts SET status = ? WHERE id = ?`, StatusBanned, accountID); err != nil {
		return fmt.Errorf("appstore: soft-ban %d: %w", accountID, err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("appstore: soft-ban revoke sessions %d: %w", accountID, err)
	}
	return tx.Commit()
}

// SetAccountCapabilities sets a moderator's elevated grants — may issue member invites
// (canInvite) and may soft-ban members (canBan). Both default off (least privilege); the
// creator toggles them per moderator in the accounts UI. The store does not restrict this
// to moderators, but the grants only matter for a moderator session — the caller gates
// which accounts are offered the toggles.
func (s *Store) SetAccountCapabilities(id int64, canInvite, canBan bool) error {
	res, err := s.db.Exec(`UPDATE accounts SET can_invite = ?, can_ban = ? WHERE id = ?`, b2i(canInvite), b2i(canBan), id)
	if err != nil {
		return fmt.Errorf("appstore: set capabilities account %d: %w", id, err)
	}
	return mustAffectOne(res, "account", id)
}

// SetAccountLabel sets the creator's private staff label for an account — how the
// operator tells moderators apart in the admin UI. It is operator-only metadata, never
// shown publicly. Callers set it only on staff accounts so members stay unlabeled (§2.4).
func (s *Store) SetAccountLabel(id int64, label string) error {
	res, err := s.db.Exec(`UPDATE accounts SET label = ? WHERE id = ?`, strings.TrimSpace(label), id)
	if err != nil {
		return fmt.Errorf("appstore: set label account %d: %w", id, err)
	}
	return mustAffectOne(res, "account", id)
}

// SetAccountAlias sets an account's single public display name and, in the same
// transaction, back-fills that name onto every comment the account still authors — including
// pending ones. Back-filling all statuses is deliberate: it means a member can never show a
// moderator one name on an approved comment and a different name on a pending one, so a rename
// cannot be used to slip past review under a fresh identity (§2.4).
//
// A non-empty alias must be unique across accounts, compared case-insensitively, so no two
// members can hold the same name at once (anti-impersonation); ErrAliasTaken is returned when
// it is already held by another account. The empty alias is "anonymous" and is always allowed,
// shared by any number of accounts. Because a name lives only on the account, changing it frees
// the previous name immediately for anyone else to claim — there is no separate reservation to
// expire. Returns the number of comments updated.
func (s *Store) SetAccountAlias(id int64, alias string) (int, error) {
	alias = strings.TrimSpace(alias)
	// Read the cap BEFORE opening the transaction: the pool is pinned to one connection
	// (SetMaxOpenConns(1)), so a query issued while the tx holds that connection would deadlock.
	dailyCap := s.AliasDailyCap()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Current name + this account's change tally for today, for the no-op short-circuit and the
	// daily cap.
	var current string
	var aliasDay, aliasChanges int64
	switch err := tx.QueryRow(`SELECT alias, alias_day, alias_changes FROM accounts WHERE id = ?`, id).Scan(&current, &aliasDay, &aliasChanges); {
	case err == sql.ErrNoRows:
		return 0, fmt.Errorf("appstore: set alias account %d: %w", id, ErrNotFound)
	case err != nil:
		return 0, fmt.Errorf("appstore: load alias account %d: %w", id, err)
	}
	// Re-saving the same name is a no-op — it consumes no daily change and needs no back-fill.
	if alias == current {
		return 0, nil
	}

	// Daily change cap (anti-churn). A cap <= 0 disables it. Only an actual change counts.
	today := s.now().Unix() / 86400
	if dailyCap > 0 && aliasDay == today && int(aliasChanges) >= dailyCap {
		return 0, ErrAliasRateLimited
	}

	if alias != "" {
		// The pool is pinned to a single connection (see Open), so this check-then-set is
		// serialized against other writers; the unique index is the backstop.
		var other int64
		switch err := tx.QueryRow(
			`SELECT id FROM accounts WHERE alias <> '' AND alias = ? COLLATE NOCASE AND id <> ? LIMIT 1`,
			alias, id,
		).Scan(&other); {
		case err == nil:
			return 0, ErrAliasTaken
		case err != sql.ErrNoRows:
			return 0, fmt.Errorf("appstore: check alias: %w", err)
		}
	}

	// Advance the daily tally: continue today's count, or start a new day at 1.
	newCount := int64(1)
	if aliasDay == today {
		newCount = aliasChanges + 1
	}
	if _, err := tx.Exec(
		`UPDATE accounts SET alias = ?, alias_day = ?, alias_changes = ? WHERE id = ?`,
		alias, today, newCount, id,
	); err != nil {
		if isUniqueViolation(err) {
			return 0, ErrAliasTaken
		}
		return 0, fmt.Errorf("appstore: set alias account %d: %w", id, err)
	}
	res, err := tx.Exec(
		`UPDATE comments SET alias = ?, updated_at = ? WHERE account_id = ?`,
		alias, s.now().Unix(), id,
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: back-fill alias account %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("appstore: set alias commit: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// isUniqueViolation reports whether err is a SQLite UNIQUE-constraint failure — the alias
// index tripping under a concurrent claim that slipped past the explicit pre-check.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// b2i maps a bool to the 0/1 SQLite stores for it.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mustAffectOne turns a zero-rows UPDATE/DELETE into ErrNotFound, so callers can
// tell "no such row" from a database error.
func mustAffectOne(res sql.Result, kind string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("appstore: %s %d: %w", kind, id, ErrNotFound)
	}
	return nil
}
