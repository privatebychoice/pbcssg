package appstore

import (
	"errors"
	"testing"
	"time"
)

// clockAt pins a store's clock to a fixed instant for deterministic timestamps.
func clockAt(s *Store, unix int64) {
	s.now = func() time.Time { return time.Unix(unix, 0) }
}

func TestInviterLabelByAccount(t *testing.T) {
	s := newTestStore(t)
	// A labeled moderator (created by a creator-issued, labeled invite) invites a member.
	modCode, _, _ := s.MintInvite(MintParams{Role: RoleModerator, Label: "Alice"})
	mod, _ := s.RedeemInvite(modCode)
	memCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, IssuedBy: mod.ID})
	mem, _ := s.RedeemInvite(memCode)
	// A member with no recorded issuer.
	orphanCode, _, _ := s.MintInvite(MintParams{Role: RoleMember})
	orphan, _ := s.RedeemInvite(orphanCode)

	m, err := s.InviterLabelByAccount()
	if err != nil {
		t.Fatal(err)
	}
	if m[mem.ID] != "Alice" {
		t.Errorf("member's inviter = %q, want Alice", m[mem.ID])
	}
	if _, ok := m[orphan.ID]; ok {
		t.Errorf("unattributed member should be absent, got %q", m[orphan.ID])
	}
	if _, ok := m[mod.ID]; ok {
		t.Errorf("moderator (no issuer on its own invite) should be absent")
	}
}

func TestSoftBanAccount(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	token, _, err := s.CreateSession(a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SoftBanAccount(a.ID); err != nil {
		t.Fatalf("SoftBanAccount: %v", err)
	}
	got, _, _ := s.AccountByID(a.ID)
	if got.Status != StatusBanned {
		t.Errorf("status = %q, want banned", got.Status)
	}
	if _, ok, _ := s.SessionByToken(token); ok {
		t.Error("soft-ban did not revoke the account's sessions")
	}
	// Reversible: un-ban restores active without deleting anything.
	if err := s.SetAccountStatus(a.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.AccountByID(a.ID); got.Status != StatusActive {
		t.Errorf("un-ban status = %q, want active", got.Status)
	}
}

func TestSetAccountCapabilitiesAndLabel(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleModerator, "")
	if a.CanInvite || a.CanBan || a.Label != "" {
		t.Fatalf("new account should start with no grants/label: %+v", a)
	}
	if err := s.SetAccountCapabilities(a.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountLabel(a.ID, "  Alice  "); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.AccountByID(a.ID)
	if !ok {
		t.Fatal("account gone")
	}
	if !got.CanInvite || got.CanBan {
		t.Errorf("capabilities = (%v,%v), want (true,false)", got.CanInvite, got.CanBan)
	}
	if got.Label != "Alice" {
		t.Errorf("label = %q, want trimmed %q", got.Label, "Alice")
	}
	if err := s.SetAccountCapabilities(9999, true, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("capabilities on missing account = %v, want ErrNotFound", err)
	}
	if err := s.SetAccountLabel(9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("label on missing account = %v, want ErrNotFound", err)
	}
}

func TestCreateAccountAndLookup(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)

	a, err := s.CreateAccount(RoleModerator, "lin-1")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == 0 || a.UserHandle == "" {
		t.Fatalf("account not populated: %+v", a)
	}
	if a.Role != RoleModerator || a.Status != StatusActive || a.InviteLineage != "lin-1" {
		t.Errorf("fields = %+v", a)
	}
	if !a.CreatedAt.Equal(time.Unix(1000, 0)) || !a.LastSeenAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("timestamps = %v / %v", a.CreatedAt, a.LastSeenAt)
	}

	got, ok, err := s.AccountByID(a.ID)
	if err != nil || !ok {
		t.Fatalf("AccountByID: ok=%v err=%v", ok, err)
	}
	if got.UserHandle != a.UserHandle {
		t.Errorf("by id handle = %q, want %q", got.UserHandle, a.UserHandle)
	}
	byH, ok, err := s.AccountByHandle(a.UserHandle)
	if err != nil || !ok || byH.ID != a.ID {
		t.Errorf("AccountByHandle: id=%d ok=%v err=%v", byH.ID, ok, err)
	}

	if _, ok, err := s.AccountByID(99999); err != nil || ok {
		t.Errorf("missing AccountByID: ok=%v err=%v, want false,nil", ok, err)
	}
	if _, ok, err := s.AccountByHandle("nope"); err != nil || ok {
		t.Errorf("missing AccountByHandle: ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestAccountsListNewestFirst(t *testing.T) {
	s := newTestStore(t)
	if got, err := s.Accounts(); err != nil || len(got) != 0 {
		t.Fatalf("empty Accounts() = %v (err %v), want none", got, err)
	}
	clockAt(s, 1000)
	first, _ := s.CreateAccount(RoleMember, "lin-1")
	clockAt(s, 2000)
	second, _ := s.CreateAccount(RoleModerator, "lin-2")

	got, err := s.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(got))
	}
	// Newest first.
	if got[0].ID != second.ID || got[1].ID != first.ID {
		t.Errorf("order = [%d %d], want newest-first [%d %d]", got[0].ID, got[1].ID, second.ID, first.ID)
	}
}

func TestCreateAccountRoleValidation(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateAccount("", "")
	if err != nil {
		t.Fatalf("empty role should default to member: %v", err)
	}
	if a.Role != RoleMember {
		t.Errorf("default role = %q, want member", a.Role)
	}
	if _, err := s.CreateAccount("wizard", ""); err == nil {
		t.Error("invalid role should be rejected")
	}
}

func TestDistinctHandles(t *testing.T) {
	s := newTestStore(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		a, err := s.CreateAccount(RoleMember, "")
		if err != nil {
			t.Fatalf("CreateAccount %d: %v", i, err)
		}
		if seen[a.UserHandle] {
			t.Fatalf("duplicate handle generated: %q", a.UserHandle)
		}
		seen[a.UserHandle] = true
	}
}

func TestSetStatusAndRole(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")

	if err := s.SetAccountStatus(a.ID, StatusBanned); err != nil {
		t.Fatalf("ban: %v", err)
	}
	got, _, _ := s.AccountByID(a.ID)
	if got.Status != StatusBanned {
		t.Errorf("status = %q, want banned", got.Status)
	}
	if err := s.SetAccountStatus(a.ID, "frozen"); err == nil {
		t.Error("invalid status should be rejected")
	}
	if err := s.SetAccountRole(a.ID, RoleModerator); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, _, _ = s.AccountByID(a.ID)
	if got.Role != RoleModerator {
		t.Errorf("role = %q, want moderator", got.Role)
	}

	if err := s.SetAccountStatus(99999, StatusBanned); !errors.Is(err, ErrNotFound) {
		t.Errorf("status on missing account = %v, want ErrNotFound", err)
	}
	if err := s.SetAccountRole(99999, RoleMember); !errors.Is(err, ErrNotFound) {
		t.Errorf("role on missing account = %v, want ErrNotFound", err)
	}
}

func TestCountAccountsByRole(t *testing.T) {
	s := newTestStore(t)
	if n, _ := s.CountAccountsByRole(RoleCreator); n != 0 {
		t.Fatalf("fresh store creators = %d, want 0 (bootstrap gate)", n)
	}
	s.CreateAccount(RoleCreator, "")
	s.CreateAccount(RoleMember, "")
	s.CreateAccount(RoleMember, "")

	if n, _ := s.CountAccountsByRole(RoleCreator); n != 1 {
		t.Errorf("creators = %d, want 1", n)
	}
	if n, _ := s.CountAccountsByRole(RoleMember); n != 2 {
		t.Errorf("members = %d, want 2", n)
	}
	if n, _ := s.CountAccountsByRole(RoleModerator); n != 0 {
		t.Errorf("moderators = %d, want 0", n)
	}
}

func TestTouchAccount(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	a, _ := s.CreateAccount(RoleMember, "")

	clockAt(s, 2000)
	if err := s.TouchAccount(a.ID); err != nil {
		t.Fatalf("TouchAccount: %v", err)
	}
	got, _, _ := s.AccountByID(a.ID)
	if !got.LastSeenAt.Equal(time.Unix(2000, 0)) {
		t.Errorf("last_seen = %v, want 2000", got.LastSeenAt)
	}
	if !got.CreatedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("created_at moved to %v, want 1000", got.CreatedAt)
	}
}
