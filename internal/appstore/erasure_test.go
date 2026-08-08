package appstore

import (
	"errors"
	"testing"
	"time"
)

// seedMember creates an account with one credential, one session, and one comment,
// returning the account and its session token.
func seedMember(t *testing.T, s *Store, page, body string) (Account, string) {
	t.Helper()
	a, err := s.CreateAccount(RoleMember, "lineage-"+page)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := s.AddCredential(Credential{AccountID: a.ID, CredID: "cred-" + page, PublicKey: []byte{1}}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	token, _, err := s.CreateSession(a.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.AddComment(a.ID, page, "alias-"+page, body); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	return a, token
}

func TestDeleteCommentsByAccount(t *testing.T) {
	s := newTestStore(t)
	a, _ := seedMember(t, s, "/a", "keep-mine?")
	seedMember(t, s, "/b", "unrelated") // isolation check via page /b

	n, err := s.DeleteCommentsByAccount(a.ID)
	if err != nil {
		t.Fatalf("DeleteCommentsByAccount: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1", n)
	}
	if got, _ := s.CommentsByPage("/a", ""); len(got) != 0 {
		t.Errorf("account a still has %d comments", len(got))
	}
	if got, _ := s.CommentsByPage("/b", ""); len(got) != 1 {
		t.Errorf("account b comments affected: %d, want 1", len(got))
	}
}

func TestAnonymizeCommentsByAccount(t *testing.T) {
	s := newTestStore(t)
	a, _ := seedMember(t, s, "/a", "my words remain")

	n, err := s.AnonymizeCommentsByAccount(a.ID)
	if err != nil {
		t.Fatalf("AnonymizeCommentsByAccount: %v", err)
	}
	if n != 1 {
		t.Errorf("anonymized = %d, want 1", n)
	}
	got, _ := s.CommentsByPage("/a", "")
	if len(got) != 1 {
		t.Fatalf("comment should survive anonymize, got %d", len(got))
	}
	c := got[0]
	if c.AccountID != nil {
		t.Errorf("account link = %v, want nil after anonymize", c.AccountID)
	}
	if c.Alias != "" {
		t.Errorf("alias = %q, want blanked", c.Alias)
	}
	if c.Body != "my words remain" {
		t.Errorf("body = %q, want preserved", c.Body)
	}
}

func TestForgetAccountDeleteBranch(t *testing.T) {
	s := newTestStore(t)
	a, token := seedMember(t, s, "/a", "erase me")

	if err := s.ForgetAccount(a.ID, false); err != nil {
		t.Fatalf("ForgetAccount(delete): %v", err)
	}
	// Account, credentials, sessions, and comments are all gone.
	if _, ok, _ := s.AccountByID(a.ID); ok {
		t.Error("account survived erasure")
	}
	if creds, _ := s.CredentialsForAccount(a.ID); len(creds) != 0 {
		t.Errorf("credentials survived erasure: %d", len(creds))
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("session survived erasure")
	}
	if got, _ := s.CommentsByPage("/a", ""); len(got) != 0 {
		t.Errorf("comments survived delete-erasure: %d", len(got))
	}
}

func TestForgetAccountAnonymizeBranch(t *testing.T) {
	s := newTestStore(t)
	a, token := seedMember(t, s, "/a", "leave my words")

	if err := s.ForgetAccount(a.ID, true); err != nil {
		t.Fatalf("ForgetAccount(anonymize): %v", err)
	}
	// Identity is gone...
	if _, ok, _ := s.AccountByID(a.ID); ok {
		t.Error("account survived erasure")
	}
	if creds, _ := s.CredentialsForAccount(a.ID); len(creds) != 0 {
		t.Errorf("credentials survived erasure: %d", len(creds))
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("session survived erasure")
	}
	// ...but the words remain, unlinked and de-aliased.
	got, _ := s.CommentsByPage("/a", "")
	if len(got) != 1 {
		t.Fatalf("comment should survive anonymize-erasure, got %d", len(got))
	}
	if got[0].AccountID != nil || got[0].Alias != "" {
		t.Errorf("comment not fully anonymized: %+v", got[0])
	}
	if got[0].Body != "leave my words" {
		t.Errorf("body = %q, want preserved", got[0].Body)
	}
}

func TestForgetAccountMissing(t *testing.T) {
	s := newTestStore(t)
	if err := s.ForgetAccount(4242, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("forget missing = %v, want ErrNotFound", err)
	}
}

func TestBanAccountComposition(t *testing.T) {
	s := newTestStore(t)
	a, token := seedMember(t, s, "/a", "banned words")

	if err := s.BanAccount(a.ID, true); err != nil {
		t.Fatalf("BanAccount: %v", err)
	}
	// Account is flagged (kept as a durable record), sessions revoked.
	got, ok, _ := s.AccountByID(a.ID)
	if !ok {
		t.Fatal("ban must keep the account row")
	}
	if got.Status != StatusBanned {
		t.Errorf("status = %q, want banned", got.Status)
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("sessions not revoked on ban")
	}
	// Content removed.
	if cs, _ := s.CommentsByPage("/a", ""); len(cs) != 0 {
		t.Errorf("content not removed on ban: %d", len(cs))
	}
	// seedMember uses CreateAccount (no invite row), so there is no invite to burn
	// here — the burn step is covered by TestBanBurnsRealInvite.
}

func TestBanBurnsRealInvite(t *testing.T) {
	s := newTestStore(t)
	// A real invite -> redeemed account, so there is a lineage to burn.
	code, inv, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0})
	acc, _ := s.RedeemInvite(code)
	// Give the account a session to prove revocation too.
	token, _, _ := s.CreateSession(acc.ID, time.Hour)

	if err := s.BanAccount(acc.ID, true); err != nil {
		t.Fatalf("BanAccount: %v", err)
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("sessions not revoked on ban")
	}
	var revoked int64
	if err := s.db.QueryRow(`SELECT revoked_at FROM invites WHERE lineage = ?`, inv.Lineage).Scan(&revoked); err != nil {
		t.Fatalf("read invite: %v", err)
	}
	if revoked == 0 {
		t.Error("creating invite was not burned on ban")
	}
}

func TestBanAccountKeepContent(t *testing.T) {
	s := newTestStore(t)
	a, _ := seedMember(t, s, "/a", "keep this")

	if err := s.BanAccount(a.ID, false); err != nil {
		t.Fatalf("BanAccount(keep): %v", err)
	}
	if cs, _ := s.CommentsByPage("/a", ""); len(cs) != 1 {
		t.Errorf("content removed despite removeContent=false: %d", len(cs))
	}
}

func TestBanAccountMissing(t *testing.T) {
	s := newTestStore(t)
	if err := s.BanAccount(9999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("ban missing = %v, want ErrNotFound", err)
	}
}

func TestPurgeInactiveMembers(t *testing.T) {
	s := newTestStore(t)

	// Old accounts (last seen at t=1000): a member, a moderator, a creator.
	clockAt(s, 1000)
	oldMem, _ := s.CreateAccount(RoleMember, "lin-old")
	oldMod, _ := s.CreateAccount(RoleModerator, "lin-mod")
	oldCreator, _ := s.CreateAccount(RoleCreator, "")
	oldC, _ := s.AddComment(oldMem.ID, "/p", "raven", "old words")

	// A recent member (last seen at t=9000).
	clockAt(s, 9000)
	recentMem, _ := s.CreateAccount(RoleMember, "lin-new")

	// Purge members idle since before t=5000, keeping (anonymizing) comments.
	n, err := s.PurgeInactiveMembers(time.Unix(5000, 0), true)
	if err != nil {
		t.Fatalf("PurgeInactiveMembers: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1 (only the old member)", n)
	}
	// Only the old member is gone.
	if _, ok, _ := s.AccountByID(oldMem.ID); ok {
		t.Error("old member should be purged")
	}
	for _, keep := range []Account{oldMod, oldCreator, recentMem} {
		if _, ok, _ := s.AccountByID(keep.ID); !ok {
			t.Errorf("account %d (role %s) should have survived the purge", keep.ID, keep.Role)
		}
	}
	// The purged member's comment is anonymized, not deleted.
	got, ok, _ := s.CommentByID(oldC.ID)
	if !ok {
		t.Fatal("comment should be kept (anonymized)")
	}
	if got.AccountID != nil || got.Alias != "" || got.Body != "old words" {
		t.Errorf("anonymized comment = %+v, want link/alias cleared, body kept", got)
	}
}

func TestPurgeInactiveMembersDeleteComments(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	mem, _ := s.CreateAccount(RoleMember, "lin")
	c, _ := s.AddComment(mem.ID, "/p", "x", "bye")

	n, err := s.PurgeInactiveMembers(time.Unix(5000, 0), false)
	if err != nil {
		t.Fatalf("PurgeInactiveMembers: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	if _, ok, _ := s.CommentByID(c.ID); ok {
		t.Error("comment should be deleted with the account when keepComments=false")
	}
}
