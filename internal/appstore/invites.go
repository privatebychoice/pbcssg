package appstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Invite code / lineage entropy. The code is distributed out-of-band by the
// operator and stored only as a hash; the lineage is an opaque id recorded on the
// account the code creates (never who the code was sent to — §2.4).
const (
	inviteCodeBytes = 24
	lineageBytes    = 16

	// ModeratorInviteTTL is the fixed lifetime of a member invite a moderator mints,
	// so a moderator cannot issue long-lived codes (anti-abuse; the creator asked for a
	// 30-day cap).
	ModeratorInviteTTL = 30 * 24 * time.Hour
	// ModeratorOutstandingInviteCap bounds how many live (unredeemed) invites a single
	// moderator may hold at once — the structural limit that stops a rogue moderator
	// mass-minting bot invites.
	ModeratorOutstandingInviteCap = 10
)

// Invite redemption failure reasons. A public registration handler may collapse
// these into one generic message to avoid an enumeration oracle; an admin flow can
// surface the specific reason.
var (
	ErrInviteInvalid = errors.New("appstore: invite invalid")
	ErrInviteUsed    = errors.New("appstore: invite already redeemed")
	ErrInviteExpired = errors.New("appstore: invite expired")
	ErrInviteRevoked = errors.New("appstore: invite revoked")
	// ErrInviteQuota is returned when a moderator is already at ModeratorOutstandingInviteCap.
	ErrInviteQuota = errors.New("appstore: outstanding invite quota reached")
)

// Invite is a single-use registration code. Only its hash is stored. Timestamp
// fields use 0 to mean "unset" (no expiry / not redeemed / not revoked).
type Invite struct {
	CodeHash   string
	Lineage    string
	Role       string
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero = never expires
	RedeemedAt time.Time // zero = unredeemed
	RedeemedBy *int64    // account id, nil until redeemed
	RevokedAt  time.Time // zero = live
	IssuedBy   *int64    // minting operator's account id, nil when system-issued (e.g. bootstrap)
	Label      string    // creator's private note at mint; seeds a staff account's label on redeem
}

// MintParams configures a new invite.
type MintParams struct {
	Role     string        // account role granted on redemption ("" → member)
	TTL      time.Duration // <= 0 → no expiry
	IssuedBy int64         // minting operator's account id (0 = system, e.g. bootstrap); recorded for provenance
	Label    string        // operator's private note; seeds a STAFF account's label on redeem (ignored for members, §2.4)
}

// MintInvite creates a single-use invite per p. It returns the plaintext code — the ONLY
// time it exists outside the operator's hands — plus the stored Invite. Recipient
// distribution is still tracked out-of-band (no recipient identity enters the database);
// IssuedBy records only which operator minted it, for accountability.
func (s *Store) MintInvite(p MintParams) (code string, inv Invite, err error) {
	role := p.Role
	if role == "" {
		role = RoleMember
	}
	if !validRole(role) {
		return "", Invite{}, fmt.Errorf("appstore: mint invite: invalid role %q", role)
	}
	code, err = randB64URL(inviteCodeBytes)
	if err != nil {
		return "", Invite{}, err
	}
	lineage, err := randB64URL(lineageBytes)
	if err != nil {
		return "", Invite{}, err
	}
	now := s.now()
	var expires int64
	if p.TTL > 0 {
		expires = now.Add(p.TTL).Unix()
	}
	var issuedBy any // NULL when system-issued
	if p.IssuedBy != 0 {
		issuedBy = p.IssuedBy
	}
	label := strings.TrimSpace(p.Label)
	if _, err := s.db.Exec(
		`INSERT INTO invites (code_hash, lineage, role, created_at, expires_at, issued_by, label)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(code), lineage, role, now.Unix(), expires, issuedBy, label,
	); err != nil {
		return "", Invite{}, fmt.Errorf("appstore: mint invite: %w", err)
	}
	inv = Invite{CodeHash: hashToken(code), Lineage: lineage, Role: role, CreatedAt: now, Label: label}
	if expires != 0 {
		inv.ExpiresAt = time.Unix(expires, 0)
	}
	if p.IssuedBy != 0 {
		id := p.IssuedBy
		inv.IssuedBy = &id
	}
	return code, inv, nil
}

// MintMemberInviteByModerator mints a member invite attributed to a moderator, at the
// fixed 30-day cap, after enforcing the per-moderator outstanding-invite limit. This is
// the anti-bot path: a moderator can never hold more than ModeratorOutstandingInviteCap
// live invites, and each is member-role and short-lived. Returns ErrInviteQuota at the cap.
func (s *Store) MintMemberInviteByModerator(moderatorID int64) (string, Invite, error) {
	if moderatorID == 0 {
		return "", Invite{}, fmt.Errorf("appstore: mint member invite: moderator id required")
	}
	// Soft cap: reads and writes serialize on the single pooled connection, so a single
	// moderator's sequential mints are counted correctly; it is not a hard transactional
	// guard against the same moderator double-submitting concurrently (an acceptable gap
	// for a soft anti-abuse limit).
	n, err := s.CountOutstandingInvitesBy(moderatorID)
	if err != nil {
		return "", Invite{}, err
	}
	if n >= ModeratorOutstandingInviteCap {
		return "", Invite{}, ErrInviteQuota
	}
	return s.MintInvite(MintParams{Role: RoleMember, TTL: ModeratorInviteTTL, IssuedBy: moderatorID})
}

// CountOutstandingInvitesBy returns how many of an operator's invites are still live —
// unredeemed, unrevoked, and unexpired. It backs the per-moderator mint cap and the
// creator's accountability view.
func (s *Store) CountOutstandingInvitesBy(issuerID int64) (int, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM invites
		 WHERE issued_by = ? AND redeemed_at = 0 AND revoked_at = 0
		   AND (expires_at = 0 OR expires_at > ?)`,
		issuerID, s.now().Unix(),
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("appstore: count outstanding invites by %d: %w", issuerID, err)
	}
	return n, nil
}

// RevokeInvitesIssuedBy revokes every live invite an operator issued — the "burn a
// moderator's invite tree" cleanup when a moderator is caught farming accounts (§2.4).
// It returns how many were revoked; accounts already created from that moderator's
// invites are handled separately (ban / erase).
func (s *Store) RevokeInvitesIssuedBy(issuerID int64) (int, error) {
	res, err := s.db.Exec(
		`UPDATE invites SET revoked_at = ? WHERE issued_by = ? AND redeemed_at = 0 AND revoked_at = 0`,
		s.now().Unix(), issuerID,
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: revoke invites by %d: %w", issuerID, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// InvitesIssuedBy lists every invite an operator minted, newest first — the moderator's
// "my invites" view and (later) the creator's per-moderator accountability list.
func (s *Store) InvitesIssuedBy(issuerID int64) ([]Invite, error) {
	rows, err := s.db.Query(`SELECT `+inviteCols+` FROM invites WHERE issued_by = ? ORDER BY created_at DESC, rowid DESC`, issuerID)
	if err != nil {
		return nil, fmt.Errorf("appstore: invites issued by %d: %w", issuerID, err)
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeOwnInvite revokes a live invite by lineage only if issuerID minted it — the guard
// that lets a moderator revoke their own outstanding invites but no one else's. Returns
// ErrNotFound when no matching live invite exists.
func (s *Store) RevokeOwnInvite(lineage string, issuerID int64) error {
	res, err := s.db.Exec(
		`UPDATE invites SET revoked_at = ? WHERE lineage = ? AND issued_by = ? AND redeemed_at = 0 AND revoked_at = 0`,
		s.now().Unix(), lineage, issuerID,
	)
	if err != nil {
		return fmt.Errorf("appstore: revoke own invite: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const inviteCols = `code_hash, lineage, role, created_at, expires_at, redeemed_at, redeemed_by, revoked_at, issued_by, label`

// scanInvite scans one invite row in inviteCols order, mapping 0 timestamps to zero
// times and a NULL redeemed_by / issued_by to nil.
func scanInvite(row interface{ Scan(...any) error }) (Invite, error) {
	var inv Invite
	var created, expires, redeemed, revoked int64
	var by, issuedBy sql.NullInt64
	if err := row.Scan(&inv.CodeHash, &inv.Lineage, &inv.Role, &created, &expires, &redeemed, &by, &revoked, &issuedBy, &inv.Label); err != nil {
		return Invite{}, err
	}
	if issuedBy.Valid {
		id := issuedBy.Int64
		inv.IssuedBy = &id
	}
	inv.CreatedAt = time.Unix(created, 0)
	if expires != 0 {
		inv.ExpiresAt = time.Unix(expires, 0)
	}
	if redeemed != 0 {
		inv.RedeemedAt = time.Unix(redeemed, 0)
	}
	if by.Valid {
		id := by.Int64
		inv.RedeemedBy = &id
	}
	if revoked != 0 {
		inv.RevokedAt = time.Unix(revoked, 0)
	}
	return inv, nil
}

// Invites returns every invite, newest first — the admin invite-management list
// (§2.4). Only the code hash is stored, so a listed invite exposes its role, lifecycle
// timestamps, and opaque lineage — never the redeemable code (shown once at mint).
func (s *Store) Invites() ([]Invite, error) {
	rows, err := s.db.Query(`SELECT ` + inviteCols + ` FROM invites ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("appstore: list invites: %w", err)
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RedeemInvite atomically consumes a valid invite and creates the account it
// authorizes, with a freshly generated opaque handle. It is one of the two account
// creation paths (§2.4); the WebAuthn registration flow uses RedeemInviteAndRegister
// instead, which supplies the handle chosen at options time. Returns a specific Err*
// on an unusable code.
func (s *Store) RedeemInvite(code string) (Account, error) {
	handle, err := newHandle()
	if err != nil {
		return Account{}, err
	}
	return s.redeem(code, handle, nil, "")
}

// RedeemInviteAndRegister is the atomic WebAuthn registration primitive: it redeems
// the invite, creates the account with the caller-supplied handle (the WebAuthn
// user.id baked into the credential at options time), and stores the first
// credential — all in one transaction, so an account is never created without its
// key and the invite is never burned without a usable account. cred.AccountID is
// ignored (set to the new account); CredID and PublicKey are required.
//
// expectRole, when non-empty, requires the invite to grant exactly that role, so a
// creator invite cannot be redeemed on the member (public) origin and vice-versa — a
// mismatch returns ErrInviteInvalid and creates nothing. Pass "" to accept any role.
func (s *Store) RedeemInviteAndRegister(code, handle string, cred Credential, expectRole string) (Account, error) {
	if handle == "" {
		return Account{}, fmt.Errorf("appstore: register: handle required")
	}
	if cred.CredID == "" || len(cred.PublicKey) == 0 {
		return Account{}, fmt.Errorf("appstore: register: cred_id and public_key required")
	}
	if expectRole != "" && !validRole(expectRole) {
		return Account{}, fmt.Errorf("appstore: register: invalid expected role %q", expectRole)
	}
	return s.redeem(code, handle, &cred, expectRole)
}

// redeem runs the check-create-mark sequence (and, when cred != nil, the credential
// insert) in one transaction. The marking UPDATE is guarded on redeemed_at = 0 so two
// concurrent redemptions of the same code cannot both create an account.
func (s *Store) redeem(code, handle string, cred *Credential, expectRole string) (Account, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	var (
		lineage, role, inviteLabel string
		expires, redeemed, revoked int64
	)
	err = tx.QueryRow(
		`SELECT lineage, role, expires_at, redeemed_at, revoked_at, label FROM invites WHERE code_hash = ?`,
		hashToken(code),
	).Scan(&lineage, &role, &expires, &redeemed, &revoked, &inviteLabel)
	switch {
	case err == sql.ErrNoRows:
		return Account{}, ErrInviteInvalid
	case err != nil:
		return Account{}, fmt.Errorf("appstore: redeem lookup: %w", err)
	}
	switch {
	case revoked != 0:
		return Account{}, ErrInviteRevoked
	case redeemed != 0:
		return Account{}, ErrInviteUsed
	case expires != 0 && expires <= now.Unix():
		return Account{}, ErrInviteExpired
	case expectRole != "" && role != expectRole:
		// Wrong-origin invite (e.g. a creator invite presented on the member origin):
		// reported as invalid, not distinguished, and consumes nothing.
		return Account{}, ErrInviteInvalid
	}

	// The invite's label seeds the new account's staff label so a moderator is
	// identifiable the moment they register — but ONLY for staff roles. A member never
	// receives a label, keeping them anonymous (§2.4).
	acctLabel := ""
	if role == RoleModerator || role == RoleCreator {
		acctLabel = inviteLabel
	}
	res, err := tx.Exec(
		`INSERT INTO accounts (user_handle, role, status, invite_lineage, label, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		handle, role, StatusActive, lineage, acctLabel, now.Unix(), now.Unix(),
	)
	if err != nil {
		return Account{}, fmt.Errorf("appstore: redeem create account: %w", err)
	}
	aid, err := res.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	// Guarded UPDATE: if a concurrent redemption already consumed it, this affects
	// zero rows and we abort — only one redemption can win.
	mark, err := tx.Exec(
		`UPDATE invites SET redeemed_at = ?, redeemed_by = ? WHERE code_hash = ? AND redeemed_at = 0`,
		now.Unix(), aid, hashToken(code),
	)
	if err != nil {
		return Account{}, fmt.Errorf("appstore: redeem mark: %w", err)
	}
	if n, _ := mark.RowsAffected(); n != 1 {
		return Account{}, ErrInviteUsed
	}
	if cred != nil {
		if _, err := tx.Exec(
			`INSERT INTO credentials
			   (account_id, cred_id, public_key, sign_count, aaguid, transports, label, created_at, last_used_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			aid, cred.CredID, cred.PublicKey, cred.SignCount, cred.AAGUID, cred.Transports, cred.Label, now.Unix(),
		); err != nil {
			return Account{}, fmt.Errorf("appstore: register credential: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("appstore: redeem commit: %w", err)
	}
	return Account{
		ID: aid, UserHandle: handle, Role: role, Status: StatusActive,
		InviteLineage: lineage, Label: acctLabel, CreatedAt: now, LastSeenAt: now,
	}, nil
}

// RevokeInvite marks an outstanding (unredeemed) invite as revoked so it can no
// longer be redeemed — the operator cancelling a code they issued. Revoking an
// unknown code returns ErrInviteInvalid.
func (s *Store) RevokeInvite(code string) error {
	res, err := s.db.Exec(
		`UPDATE invites SET revoked_at = ? WHERE code_hash = ? AND revoked_at = 0`,
		s.now().Unix(), hashToken(code),
	)
	if err != nil {
		return fmt.Errorf("appstore: revoke invite: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInviteInvalid
	}
	return nil
}

// PruneSpentInvites deletes UNREDEEMED invites that went dead — revoked or expired —
// before cutoff. Redeemed invites are kept: they are provenance records (a member's
// invite_lineage joins to them for "invited by" and ban-by-lineage). Live invites are
// kept. Returns how many rows were removed. A zero cutoff (retention disabled) is handled
// by the caller, which simply does not call this.
func (s *Store) PruneSpentInvites(cutoff time.Time) (int, error) {
	c := cutoff.Unix()
	res, err := s.db.Exec(
		`DELETE FROM invites
		 WHERE redeemed_at = 0
		   AND ((revoked_at != 0 AND revoked_at < ?) OR (expires_at != 0 AND expires_at < ?))`,
		c, c,
	)
	if err != nil {
		return 0, fmt.Errorf("appstore: prune spent invites: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RevokeInviteByLineage burns the invite that created a given account (the
// "burn the creating invite" step of a ban — §2.4), located by the lineage id
// stored on the account. It is idempotent: a lineage with no live invite is a
// no-op. Supports optional invite-tree pruning later.
func (s *Store) RevokeInviteByLineage(lineage string) error {
	if _, err := s.db.Exec(
		`UPDATE invites SET revoked_at = ? WHERE lineage = ? AND revoked_at = 0`,
		s.now().Unix(), lineage,
	); err != nil {
		return fmt.Errorf("appstore: revoke invite by lineage: %w", err)
	}
	return nil
}
