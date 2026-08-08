package appstore

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// TestSetAccountAliasUniqueness covers the one-holder-per-name rule: a non-empty alias is
// unique case-insensitively, "" (anonymous) is shared, and changing a name frees the old one.
func TestSetAccountAliasUniqueness(t *testing.T) {
	s := newTestStore(t)
	s.SetAliasDailyCap(0) // disable the per-day change cap; this test isn't about it
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")

	if _, err := s.SetAccountAlias(a.ID, "Nova"); err != nil {
		t.Fatalf("first claim of Nova: %v", err)
	}
	// Case-insensitive collision — b may not take "nova".
	if _, err := s.SetAccountAlias(b.ID, "nova"); !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("case-insensitive collision: got %v, want ErrAliasTaken", err)
	}
	// Anonymous is shared: both accounts may be "".
	if _, err := s.SetAccountAlias(b.ID, ""); err != nil {
		t.Fatalf("b -> anonymous: %v", err)
	}
	if _, err := s.SetAccountAlias(a.ID, ""); err != nil {
		t.Fatalf("a -> anonymous (second anonymous): %v", err)
	}
	// Re-setting an account to the same name it already holds is not a self-collision.
	if _, err := s.SetAccountAlias(a.ID, "Nova"); err != nil {
		t.Fatalf("a re-claims Nova: %v", err)
	}
	if _, err := s.SetAccountAlias(a.ID, "Nova"); err != nil {
		t.Fatalf("a sets its own current name again: %v", err)
	}
	// Changing a frees "Nova" for b to claim.
	if _, err := s.SetAccountAlias(a.ID, "Star"); err != nil {
		t.Fatalf("a -> Star: %v", err)
	}
	if _, err := s.SetAccountAlias(b.ID, "Nova"); err != nil {
		t.Fatalf("b claims freed Nova: %v", err)
	}
	if got, _, _ := s.AccountByID(b.ID); got.Alias != "Nova" {
		t.Errorf("b.Alias = %q, want Nova", got.Alias)
	}
}

// TestSetAccountAliasBackfillsAllStatuses proves a rename rewrites the member's name on every
// comment, pending and approved alike — the "can't fool the moderator with two names" property.
func TestSetAccountAliasBackfillsAllStatuses(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	approved, _ := s.AddComment(a.ID, "/p", "Old", "one")
	s.SetCommentStatus(approved.ID, CommentApproved)
	pending, _ := s.AddComment(a.ID, "/p", "Old", "two")

	n, err := s.SetAccountAlias(a.ID, "New")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("back-filled %d comments, want 2", n)
	}
	for _, id := range []int64{approved.ID, pending.ID} {
		c, _, _ := s.CommentByID(id)
		if c.Alias != "New" {
			t.Errorf("comment %d alias = %q, want New (status %s)", id, c.Alias, c.Status)
		}
	}
}

// TestSetAccountAliasReleasedOnDelete confirms deleting an account frees its name back to the
// pool (the index is on live accounts, so the row's removal is the release).
func TestSetAccountAliasReleasedOnDelete(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")
	if _, err := s.SetAccountAlias(a.ID, "Ghost"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAccountAlias(b.ID, "Ghost"); !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("Ghost should be taken while a holds it: %v", err)
	}
	if err := s.ForgetAccount(a.ID, false); err != nil {
		t.Fatalf("forget a: %v", err)
	}
	if _, err := s.SetAccountAlias(b.ID, "Ghost"); err != nil {
		t.Fatalf("b claims Ghost after a is forgotten: %v", err)
	}
}

// TestSeedAliasesDedupe drives the legacy migration: an old database with the per-comment
// alias model, where two accounts historically used the same name, must seed the name onto the
// OLDER account and leave the newer one anonymous so the new unique index can build.
func TestSeedAliasesDedupe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Pre-migration schema: accounts without the alias column, comments carrying per-comment
	// aliases (two different accounts both used "Guest").
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		if _, err := raw.Exec(q, args...); err != nil {
			t.Fatalf("legacy exec %q: %v", q, err)
		}
	}
	mustExec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_handle TEXT NOT NULL UNIQUE,
		role TEXT NOT NULL DEFAULT 'member', status TEXT NOT NULL DEFAULT 'active',
		invite_lineage TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL)`)
	mustExec(`CREATE TABLE comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER, page_path TEXT NOT NULL,
		alias TEXT NOT NULL DEFAULT '', body TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`)
	// Account 1 is older (created_at 100), account 2 newer (created_at 200); both used "Guest".
	mustExec(`INSERT INTO accounts (id, user_handle, created_at, last_seen_at) VALUES (1,'h1',100,100),(2,'h2',200,200)`)
	mustExec(`INSERT INTO comments (account_id, page_path, alias, body, status, created_at, updated_at)
		VALUES (1,'/p','Guest','a','approved',100,100), (2,'/p','Guest','b','approved',200,200)`)
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy db (migrate+seed): %v", err)
	}
	defer s.Close()

	a1, _, _ := s.AccountByID(1)
	a2, _, _ := s.AccountByID(2)
	if a1.Alias != "Guest" {
		t.Errorf("older account alias = %q, want Guest (oldest wins the contested name)", a1.Alias)
	}
	if a2.Alias != "" {
		t.Errorf("newer account alias = %q, want empty (contested name went to the older account)", a2.Alias)
	}
	// The unique index is live: account 2 cannot now take the seeded name.
	if _, err := s.SetAccountAlias(2, "Guest"); !errors.Is(err, ErrAliasTaken) {
		t.Errorf("post-seed uniqueness: got %v, want ErrAliasTaken", err)
	}
}
