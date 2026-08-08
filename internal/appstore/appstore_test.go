package appstore

import (
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestOpenEnforcesForeignKeyCascade guards that Open leaves foreign keys ON for the
// connection(s) it hands out, so the schema's ON DELETE CASCADE / SET NULL rules that
// back ban and erasure (§2.4) actually fire. Before Open set the pragma per-connection
// (via the DSN) and pinned the pool to one connection, a pooled connection with
// foreign_keys OFF would delete an account and silently orphan its credentials and
// sessions and leave its comments attributed — a privacy/integrity failure.
func TestMigrateBackfillsColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	// Simulate a pre-migration database: an accounts table WITHOUT the new columns.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_handle TEXT NOT NULL UNIQUE,
		role TEXT NOT NULL DEFAULT 'member', status TEXT NOT NULL DEFAULT 'active',
		invite_lineage TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	// Opening through appstore backfills can_invite/can_ban/label onto the old table.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on old db (should migrate): %v", err)
	}
	a, err := s.CreateAccount(RoleModerator, "")
	if err != nil {
		t.Fatalf("CreateAccount after migrate: %v", err)
	}
	if err := s.SetAccountCapabilities(a.ID, true, true); err != nil {
		t.Fatalf("SetAccountCapabilities uses a backfilled column: %v", err)
	}
	got, _, _ := s.AccountByID(a.ID)
	if !got.CanInvite || !got.CanBan {
		t.Errorf("backfilled columns not usable: %+v", got)
	}
	s.Close()

	// Reopening runs migrate again: it must be idempotent and preserve data.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (idempotent migrate): %v", err)
	}
	defer s2.Close()
	if _, ok, _ := s2.AccountByID(a.ID); !ok {
		t.Error("account lost across reopen")
	}
}

func TestOpenEnforcesForeignKeyCascade(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateAccount(RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCredential(Credential{AccountID: a.ID, CredID: "cred-1", PublicKey: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSession(a.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	cm, err := s.AddComment(a.ID, "/p", "raven", "hello")
	if err != nil {
		t.Fatal(err)
	}

	// A raw account delete fires the schema's ON DELETE rules only when foreign_keys is
	// ON for this connection — the regression this test guards.
	if _, err := s.db.Exec(`DELETE FROM accounts WHERE id = ?`, a.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}

	// CASCADE: the account's credentials and sessions are gone.
	if creds, _ := s.CredentialsForAccount(a.ID); len(creds) != 0 {
		t.Errorf("credentials not cascaded: %d remain (foreign_keys off?)", len(creds))
	}
	var nSessions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE account_id = ?`, a.ID).Scan(&nSessions); err != nil {
		t.Fatal(err)
	}
	if nSessions != 0 {
		t.Errorf("sessions not cascaded: %d remain", nSessions)
	}

	// SET NULL: the comment survives but is detached from the deleted author.
	got, ok, err := s.CommentByID(cm.ID)
	if err != nil || !ok {
		t.Fatalf("comment gone or error after author delete: ok=%v err=%v", ok, err)
	}
	if got.AccountID != nil {
		t.Errorf("comment.account_id not SET NULL after author delete: got %d", *got.AccountID)
	}
}

// newTestStore opens an in-memory runtime store for a test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// tableNames returns the user tables present in the database (excluding SQLite's
// internal bookkeeping tables).
func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(names)
	return names
}

func TestOpenAppliesSchema(t *testing.T) {
	s := newTestStore(t)
	got := tableNames(t, s)
	want := []string{"accounts", "comments", "credentials", "invites", "runtime_settings", "sessions"}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables = %v, want %v", got, want)
		}
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s := newTestStore(t)
	// A credential referencing a non-existent account must be rejected: this proves
	// PRAGMA foreign_keys is on, which the ban/erasure cascades rely on.
	_, err := s.db.Exec(
		`INSERT INTO credentials (account_id, cred_id, public_key, created_at) VALUES (?, ?, ?, ?)`,
		999, "nope", []byte{0x01}, 1,
	)
	if err == nil {
		t.Fatal("insert with dangling account_id should violate the foreign key, but succeeded")
	}
}

func TestCascadeDeletesCredentials(t *testing.T) {
	s := newTestStore(t)
	// Insert an account and a credential, then delete the account: the credential
	// must go with it (ON DELETE CASCADE — the shape ban-removal depends on).
	res, err := s.db.Exec(
		`INSERT INTO accounts (user_handle, created_at, last_seen_at) VALUES (?, ?, ?)`, "h1", 1, 1)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	aid, _ := res.LastInsertId()
	if _, err := s.db.Exec(
		`INSERT INTO credentials (account_id, cred_id, public_key, created_at) VALUES (?, ?, ?, ?)`,
		aid, "c1", []byte{0x01}, 1,
	); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM accounts WHERE id = ?`, aid); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE account_id = ?`, aid).Scan(&n); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if n != 0 {
		t.Fatalf("credentials remaining after account delete = %d, want 0", n)
	}
}

func TestCommentAccountLinkSetNull(t *testing.T) {
	s := newTestStore(t)
	// Deleting an account anonymizes its comments (account_id -> NULL), the
	// "anonymize" branch of forget-me — the comment survives, the link does not.
	res, _ := s.db.Exec(`INSERT INTO accounts (user_handle, created_at, last_seen_at) VALUES (?, ?, ?)`, "h2", 1, 1)
	aid, _ := res.LastInsertId()
	if _, err := s.db.Exec(
		`INSERT INTO comments (account_id, page_path, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		aid, "/post", "hello", 1, 1,
	); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM accounts WHERE id = ?`, aid); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var linked sql.NullInt64
	if err := s.db.QueryRow(`SELECT account_id FROM comments WHERE page_path = '/post'`).Scan(&linked); err != nil {
		t.Fatalf("scan comment: %v", err)
	}
	if linked.Valid {
		t.Fatalf("comment account_id = %d after account delete, want NULL", linked.Int64)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	// Applying the schema twice against a real file must not error (CREATE TABLE IF
	// NOT EXISTS) and must preserve rows across a reopen.
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := s1.db.Exec(`INSERT INTO accounts (user_handle, created_at, last_seen_at) VALUES (?, ?, ?)`, "keep", 1, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE user_handle = 'keep'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("account count after reopen = %d, want 1", n)
	}
}
