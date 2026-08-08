// Package appstore is the pbcssg runtime store (SPEC §2.4): the third data store,
// separate from the authoring content.db (internal/store) and from the immutable
// published bundles. It holds the dynamic public-side state that a build must never
// destroy and the editing store must never see — accounts, WebAuthn credentials,
// sessions, single-use invites, and comments.
//
// Design constraints carried from the spec:
//   - Identity is a passkey, not PII: an account stores only an opaque, random user
//     handle plus per-credential public material. No password, email, or real name.
//   - Session tokens and invite codes are stored HASHED (SHA-256 of high-entropy
//     random values), so a database or backup leak yields no usable secret.
//   - Comments are public-by-design once approved, but keep an internal account link
//     so moderation, ban-removal, and "forget me" erasure can act on them (§2.4).
//
// Like internal/store it uses the pure-Go modernc.org/sqlite driver (no cgo) so the
// tool stays a single cross-compiled binary.
package appstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schema is applied idempotently on every Open (CREATE TABLE IF NOT EXISTS), the
// same lightweight approach internal/store uses. Timestamps are Unix seconds.
const schema = `
-- accounts — one per Community Member, moderator, or creator. Identity is a
-- passkey; no PII is stored. The user_handle is the opaque, random WebAuthn user
-- handle (never an identifier). invite_lineage records which invite created the
-- account (an opaque id, never who the code was sent to) so bans can burn a lineage
-- without linking to a person.
CREATE TABLE IF NOT EXISTS accounts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_handle    TEXT NOT NULL UNIQUE,            -- base64url of random bytes
  role           TEXT NOT NULL DEFAULT 'member',  -- member | moderator | creator
  status         TEXT NOT NULL DEFAULT 'active',  -- active | banned
  invite_lineage TEXT NOT NULL DEFAULT '',        -- opaque lineage id of the creating invite
  can_invite     INTEGER NOT NULL DEFAULT 0,      -- moderator elevated grant: may mint member invites (creator-granted)
  can_ban        INTEGER NOT NULL DEFAULT 0,      -- moderator elevated grant: may soft-ban members (creator-granted)
  label          TEXT NOT NULL DEFAULT '',        -- creator's private STAFF label (moderators/creators only; never members — §2.4)
  alias          TEXT NOT NULL DEFAULT '',         -- account-level PUBLIC display name ('' = anonymous); case-insensitively unique across accounts when non-empty
  alias_day      INTEGER NOT NULL DEFAULT 0,       -- unix-day (unix/86400) of the last alias change, for the per-day change cap
  alias_changes  INTEGER NOT NULL DEFAULT 0,       -- alias changes made on alias_day, compared against the configured daily cap
  created_at     INTEGER NOT NULL,
  last_seen_at   INTEGER NOT NULL
);
-- One holder per name at a time: a non-empty alias is unique case-insensitively so a
-- member cannot impersonate another. '' (anonymous) is exempt via the partial predicate,
-- so any number of accounts may be anonymous. Built in migrate() (not here) because on an
-- existing database the alias column is added after this schema runs; see migrateAccountAlias.

-- credentials — WebAuthn public-key credentials; MANY per account (creators/mods
-- register >=2). cred_id is the raw credential ID (assertion lookup key). The COSE
-- public key is not secret. aaguid is empty for attestation:none (members); it may
-- be populated for admin/moderator enrolment that requests attestation.
CREATE TABLE IF NOT EXISTS credentials (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  cred_id      TEXT NOT NULL UNIQUE,        -- base64url of the raw credential ID
  public_key   BLOB NOT NULL,              -- COSE public key (not secret)
  sign_count   INTEGER NOT NULL DEFAULT 0, -- clone-detection counter
  aaguid       TEXT NOT NULL DEFAULT '',   -- empty under attestation:none
  transports   TEXT NOT NULL DEFAULT '',   -- comma-joined hints, display only
  label        TEXT NOT NULL DEFAULT '',   -- user-facing "which key" label
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_credentials_account ON credentials(account_id);

-- sessions — server-side session records. The cookie carries only the opaque
-- token; id_hash is SHA-256 of that token, so a DB/backup leak cannot mint a
-- session. SHA-256 (not a slow KDF) is deliberate: the token is 128+ bits of
-- crypto/rand, not password-like.
CREATE TABLE IF NOT EXISTS sessions (
  id_hash      TEXT PRIMARY KEY,           -- sha256 of the opaque session token
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_account ON sessions(account_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- invites — single-use registration codes, stored HASHED and redeemed atomically.
-- lineage is recorded on the account the code creates. revoked_at supports
-- burn-on-ban. expires_at/redeemed_at/revoked_at use 0 to mean "unset".
CREATE TABLE IF NOT EXISTS invites (
  code_hash   TEXT PRIMARY KEY,            -- sha256 of the single-use code
  lineage     TEXT NOT NULL UNIQUE,        -- opaque id recorded on the created account
  role        TEXT NOT NULL DEFAULT 'member',
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL DEFAULT 0,  -- 0 = no expiry
  redeemed_at INTEGER NOT NULL DEFAULT 0,  -- 0 = unredeemed
  redeemed_by INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  revoked_at  INTEGER NOT NULL DEFAULT 0,  -- 0 = live
  issued_by   INTEGER REFERENCES accounts(id) ON DELETE SET NULL, -- staff who minted it (provenance + anti-abuse attribution)
  label       TEXT NOT NULL DEFAULT ''     -- creator's note at mint; seeds a STAFF account's label on redeem
);

-- comments — public-by-design once approved. account_id is an internal link kept
-- for moderation, ban-removal, and erasure (§2.4); ON DELETE SET NULL implements
-- the "anonymize" branch of forget-me (a hard delete removes the row instead).
-- alias is the comment-scoped public display name, never a login identifier.
CREATE TABLE IF NOT EXISTS comments (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id  INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  page_path   TEXT NOT NULL,
  alias       TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'pending', -- pending | approved | rejected
  author_role TEXT NOT NULL DEFAULT 'member',  -- role snapshot at post time, drives the staff badge; survives anonymization
  parent_id   INTEGER REFERENCES comments(id) ON DELETE CASCADE, -- reply target; NULL = root. One level deep (a reply's parent is always a root)
  deleted_at  INTEGER NOT NULL DEFAULT 0,       -- 0 = live; >0 = tombstone (a root the author deleted while replies remained; body/alias blanked, slot kept for thread context)
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_comments_page ON comments(page_path, status);
CREATE INDEX IF NOT EXISTS idx_comments_account ON comments(account_id);
-- idx_comments_parent is built in migrate() (not here): on an existing database parent_id is
-- added after this schema runs, so the index over it can't be declared at this point.

-- runtime_settings — a small KV for settings the PUBLIC origin must read at request time and
-- so cannot keep in the authoring content.db (which the public path never opens). The creator
-- Settings UI (which has both stores) writes them; the public handlers read them. Today: the
-- per-account daily alias-change cap.
CREATE TABLE IF NOT EXISTS runtime_settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// Role values for an account.
const (
	RoleMember    = "member"
	RoleModerator = "moderator"
	RoleCreator   = "creator"
)

// Account status values.
const (
	StatusActive = "active"
	StatusBanned = "banned"
)

// Comment moderation status values.
const (
	CommentPending  = "pending"
	CommentApproved = "approved"
	CommentRejected = "rejected"
)

// Store is a handle to the runtime database. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the runtime SQLite database at path and applies
// the schema. Use ":memory:" for an ephemeral database in tests. Foreign keys are
// enforced so the ON DELETE CASCADE / SET NULL rules that back ban and erasure
// actually fire.
func Open(path string) (*Store, error) {
	// foreign_keys is a per-connection setting in SQLite, not persisted in the file, so
	// it must be applied to every connection database/sql opens — a one-off
	// db.Exec("PRAGMA foreign_keys=ON") would bind only the connection that ran it and
	// leave other pooled connections with FK OFF, silently skipping the ON DELETE
	// CASCADE / SET NULL rules that ban and erasure rely on (§2.4). Setting it in the DSN
	// makes the driver apply it on connect, every time. busy_timeout lets a writer wait
	// for the lock instead of failing with SQLITE_BUSY under the shared admin+public load.
	pragmas := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	dsn := "file:" + path + "?" + pragmas
	if path == ":memory:" {
		dsn = "file::memory:?" + pragmas
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("appstore: open: %w", err)
	}
	// SQLite is single-writer; pin the pool to one connection so writes from the shared
	// admin and public listeners serialize here rather than racing for the file lock, and
	// so an in-memory test database is one stable database rather than a fresh one per
	// pooled connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("appstore: migrate: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

// columnAdd is one additive schema evolution: a column absent from the original CREATE
// TABLE that Open backfills onto an existing database. Fresh databases already have it
// from the schema above, so migrate skips it there.
type columnAdd struct{ table, column, ddl string }

// migrate applies additive column changes idempotently. SQLite has no
// "ADD COLUMN IF NOT EXISTS", so each add is guarded by a PRAGMA table_info check. New
// columns are nullable or NOT NULL with a constant default, the only forms ADD COLUMN
// allows on a populated table; the FK columns default NULL, which ADD COLUMN also
// requires for a REFERENCES clause.
func migrate(db *sql.DB) error {
	adds := []columnAdd{
		{"accounts", "can_invite", `ALTER TABLE accounts ADD COLUMN can_invite INTEGER NOT NULL DEFAULT 0`},
		{"accounts", "can_ban", `ALTER TABLE accounts ADD COLUMN can_ban INTEGER NOT NULL DEFAULT 0`},
		{"accounts", "label", `ALTER TABLE accounts ADD COLUMN label TEXT NOT NULL DEFAULT ''`},
		{"invites", "issued_by", `ALTER TABLE invites ADD COLUMN issued_by INTEGER REFERENCES accounts(id) ON DELETE SET NULL`},
		{"invites", "label", `ALTER TABLE invites ADD COLUMN label TEXT NOT NULL DEFAULT ''`},
		{"comments", "author_role", `ALTER TABLE comments ADD COLUMN author_role TEXT NOT NULL DEFAULT 'member'`},
		{"comments", "parent_id", `ALTER TABLE comments ADD COLUMN parent_id INTEGER REFERENCES comments(id) ON DELETE CASCADE`},
		{"comments", "deleted_at", `ALTER TABLE comments ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`},
		{"accounts", "alias_day", `ALTER TABLE accounts ADD COLUMN alias_day INTEGER NOT NULL DEFAULT 0`},
		{"accounts", "alias_changes", `ALTER TABLE accounts ADD COLUMN alias_changes INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range adds {
		has, err := hasColumn(db, a.table, a.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.Exec(a.ddl); err != nil {
			return fmt.Errorf("appstore: migrate %s.%s: %w", a.table, a.column, err)
		}
	}
	// parent_id exists now (added above or already present), so its index can be built. Kept
	// out of the schema const for the same reason as the alias index: on an existing database
	// the column arrives after that schema runs.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_comments_parent ON comments(parent_id)`); err != nil {
		return fmt.Errorf("appstore: migrate idx_comments_parent: %w", err)
	}
	// accounts.alias needs a seed + a unique index, so it can't use the plain add loop.
	return migrateAccountAlias(db)
}

// migrateAccountAlias adds the account-level alias column, seeds it from existing comment
// aliases when first introduced, and builds the case-insensitive uniqueness index. Kept out
// of the generic add loop because the index must be created only AFTER seeding (so a legacy
// database's pre-existing duplicate names can't fail the unique build) and the seed must run
// once, only when the column is first added. All three steps are idempotent across reopens.
func migrateAccountAlias(db *sql.DB) error {
	has, err := hasColumn(db, "accounts", "alias")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN alias TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("appstore: migrate accounts.alias: %w", err)
		}
		if err := seedAccountAliases(db); err != nil {
			return err
		}
	}
	// Non-empty aliases are unique case-insensitively; '' (anonymous) is exempt. IF NOT
	// EXISTS makes this a no-op on every reopen once built.
	if _, err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_alias ON accounts(alias COLLATE NOCASE) WHERE alias <> ''`,
	); err != nil {
		return fmt.Errorf("appstore: migrate accounts.alias index: %w", err)
	}
	return nil
}

// seedAccountAliases back-fills the freshly added accounts.alias from each account's most
// recent non-empty comment alias, so members carry their existing display name into the new
// account-level model. Names are claimed oldest-account-first: if two accounts historically
// used the same name (which the old per-comment model allowed), the earliest account keeps it
// and later accounts stay anonymous, upholding the one-holder-per-name invariant the unique
// index then enforces. Runs once, right after the column is added, before the index exists.
func seedAccountAliases(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT a.id, (
			SELECT c.alias FROM comments c
			WHERE c.account_id = a.id AND c.alias <> ''
			ORDER BY c.created_at DESC, c.id DESC LIMIT 1
		)
		FROM accounts a
		ORDER BY a.created_at ASC, a.id ASC`)
	if err != nil {
		return fmt.Errorf("appstore: seed alias query: %w", err)
	}
	type claim struct {
		id    int64
		alias string
	}
	var claims []claim
	for rows.Next() {
		var id int64
		var alias sql.NullString
		if err := rows.Scan(&id, &alias); err != nil {
			rows.Close()
			return fmt.Errorf("appstore: seed alias scan: %w", err)
		}
		if alias.Valid && alias.String != "" {
			claims = append(claims, claim{id, alias.String})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(claims))
	for _, c := range claims {
		key := strings.ToLower(c.alias)
		if seen[key] {
			continue // contested name already claimed by an older account
		}
		seen[key] = true
		if _, err := db.Exec(`UPDATE accounts SET alias = ? WHERE id = ?`, c.alias, c.id); err != nil {
			return fmt.Errorf("appstore: seed alias account %d: %w", c.id, err)
		}
	}
	return nil
}

// hasColumn reports whether table already has the named column.
func hasColumn(db *sql.DB, table, column string) (bool, error) {
	// table is an internal constant (never user input), so interpolating it into the
	// PRAGMA — which does not accept bound parameters — is safe.
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("appstore: table_info(%s): %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Vacuum rebuilds the database file, reclaiming space freed by deletions — SQLite keeps
// freed pages on a freelist and does not shrink the file on its own. VACUUM cannot run
// inside a transaction; with the single pooled connection it serializes cleanly and is
// fast on a small database.
func (s *Store) Vacuum() error {
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("appstore: vacuum: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }
