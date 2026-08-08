package appstore

import (
	"errors"
	"testing"
	"time"
)

func TestRedeemCopiesLabelToStaffNotMembers(t *testing.T) {
	s := newTestStore(t)

	// A moderator invite's label seeds the account label at redemption, so the operator
	// can identify the moderator the instant they register.
	modCode, inv, _ := s.MintInvite(MintParams{Role: RoleModerator, Label: "  Alice  "})
	if inv.Label != "Alice" {
		t.Errorf("minted invite label = %q, want trimmed Alice", inv.Label)
	}
	modAcc, err := s.RedeemInvite(modCode)
	if err != nil {
		t.Fatal(err)
	}
	if modAcc.Label != "Alice" {
		t.Errorf("moderator account label = %q, want Alice", modAcc.Label)
	}

	// A member invite's label must NOT propagate — members stay unlabeled (§2.4).
	memCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, Label: "should-not-stick"})
	memAcc, err := s.RedeemInvite(memCode)
	if err != nil {
		t.Fatal(err)
	}
	if memAcc.Label != "" {
		t.Errorf("member account label = %q, want empty (members stay anonymous)", memAcc.Label)
	}
}

func TestMintMemberInviteByModerator(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)
	mod, _ := s.CreateAccount(RoleModerator, "")

	// Mint up to the cap; each is member-role, attributed, and 30-day.
	for i := 0; i < ModeratorOutstandingInviteCap; i++ {
		_, inv, err := s.MintMemberInviteByModerator(mod.ID)
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		if inv.Role != RoleMember {
			t.Errorf("role = %q, want member", inv.Role)
		}
		if inv.IssuedBy == nil || *inv.IssuedBy != mod.ID {
			t.Errorf("issued_by = %v, want %d", inv.IssuedBy, mod.ID)
		}
		if want := int64(1000) + int64(ModeratorInviteTTL/time.Second); inv.ExpiresAt.Unix() != want {
			t.Errorf("expiry = %d, want %d (30 days)", inv.ExpiresAt.Unix(), want)
		}
	}
	// The (cap+1)th is refused.
	if _, _, err := s.MintMemberInviteByModerator(mod.ID); !errors.Is(err, ErrInviteQuota) {
		t.Errorf("over-cap mint = %v, want ErrInviteQuota", err)
	}
	if n, _ := s.CountOutstandingInvitesBy(mod.ID); n != ModeratorOutstandingInviteCap {
		t.Errorf("outstanding = %d, want %d", n, ModeratorOutstandingInviteCap)
	}

	// Burning the moderator's invite tree frees the whole cap.
	revoked, err := s.RevokeInvitesIssuedBy(mod.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != ModeratorOutstandingInviteCap {
		t.Errorf("revoked %d, want %d", revoked, ModeratorOutstandingInviteCap)
	}
	if n, _ := s.CountOutstandingInvitesBy(mod.ID); n != 0 {
		t.Errorf("outstanding after revoke = %d, want 0", n)
	}
	if _, _, err := s.MintMemberInviteByModerator(mod.ID); err != nil {
		t.Errorf("mint after revoke should succeed: %v", err)
	}
}

func TestInvitesIssuedByAndRevokeOwn(t *testing.T) {
	s := newTestStore(t)
	modA, _ := s.CreateAccount(RoleModerator, "")
	modB, _ := s.CreateAccount(RoleModerator, "")

	_, a1, _ := s.MintMemberInviteByModerator(modA.ID)
	_, _, _ = s.MintMemberInviteByModerator(modA.ID)
	_, _, _ = s.MintMemberInviteByModerator(modB.ID)

	got, err := s.InvitesIssuedBy(modA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("modA issued %d invites, want 2", len(got))
	}

	// A moderator cannot revoke another moderator's invite.
	if err := s.RevokeOwnInvite(a1.Lineage, modB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-moderator revoke = %v, want ErrNotFound", err)
	}
	if n, _ := s.CountOutstandingInvitesBy(modA.ID); n != 2 {
		t.Errorf("modA outstanding after cross-revoke = %d, want 2 (untouched)", n)
	}
	// The owner can revoke their own.
	if err := s.RevokeOwnInvite(a1.Lineage, modA.ID); err != nil {
		t.Fatalf("own revoke: %v", err)
	}
	if n, _ := s.CountOutstandingInvitesBy(modA.ID); n != 1 {
		t.Errorf("modA outstanding after own revoke = %d, want 1", n)
	}
	// Revoking a no-longer-live invite is ErrNotFound.
	if err := s.RevokeOwnInvite(a1.Lineage, modA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-revoke = %v, want ErrNotFound", err)
	}
}

func TestMintAndRedeemInvite(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)

	code, inv, err := s.MintInvite(MintParams{Role: RoleModerator, TTL: time.Hour})
	if err != nil {
		t.Fatalf("MintInvite: %v", err)
	}
	if code == "" || inv.Lineage == "" {
		t.Fatalf("invite not populated: %+v", inv)
	}
	if inv.CodeHash == code {
		t.Fatal("stored code_hash must not equal the plaintext code")
	}
	if inv.Role != RoleModerator {
		t.Errorf("role = %q, want moderator", inv.Role)
	}

	acc, err := s.RedeemInvite(code)
	if err != nil {
		t.Fatalf("RedeemInvite: %v", err)
	}
	// The account inherits the invite's role and lineage.
	if acc.Role != RoleModerator {
		t.Errorf("account role = %q, want moderator", acc.Role)
	}
	if acc.InviteLineage != inv.Lineage {
		t.Errorf("account lineage = %q, want %q", acc.InviteLineage, inv.Lineage)
	}
	if acc.Status != StatusActive {
		t.Errorf("account status = %q, want active", acc.Status)
	}
	// It is a real, resolvable account.
	if _, ok, _ := s.AccountByID(acc.ID); !ok {
		t.Error("redeemed account not found")
	}
}

func TestInvitesList(t *testing.T) {
	s := newTestStore(t)
	if got, err := s.Invites(); err != nil || len(got) != 0 {
		t.Fatalf("empty Invites() = %v (err %v)", got, err)
	}

	clockAt(s, 1000)
	code1, inv1, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: time.Hour}) // will be redeemed
	clockAt(s, 2000)
	_, inv2, _ := s.MintInvite(MintParams{Role: RoleCreator, TTL: 0}) // no expiry, stays live
	clockAt(s, 3000)
	_, inv3, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: time.Hour}) // will be revoked

	if _, err := s.RedeemInvite(code1); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInviteByLineage(inv3.Lineage); err != nil {
		t.Fatal(err)
	}

	got, err := s.Invites()
	if err != nil {
		t.Fatalf("Invites: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 invites, got %d", len(got))
	}
	// Newest first.
	if got[0].Lineage != inv3.Lineage || got[1].Lineage != inv2.Lineage || got[2].Lineage != inv1.Lineage {
		t.Errorf("order not newest-first: %q,%q,%q", got[0].Lineage, got[1].Lineage, got[2].Lineage)
	}
	// Lifecycle fields survive the round-trip.
	if got[2].RedeemedAt.IsZero() || got[2].RedeemedBy == nil {
		t.Errorf("redeemed invite missing redemption fields: %+v", got[2])
	}
	if got[0].RevokedAt.IsZero() {
		t.Errorf("revoked invite missing revoked_at: %+v", got[0])
	}
	if !got[1].ExpiresAt.IsZero() {
		t.Errorf("no-expiry invite should have zero ExpiresAt, got %v", got[1].ExpiresAt)
	}
	// The redeemable code never appears — only its hash.
	for _, inv := range got {
		if inv.CodeHash == "" {
			t.Error("code hash missing")
		}
	}
}

func TestRedeemInviteSingleUse(t *testing.T) {
	s := newTestStore(t)
	code, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0}) // 0 ttl = no expiry

	if _, err := s.RedeemInvite(code); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	// A second redemption of the same code must fail and create no second account.
	before := accountCount(t, s)
	if _, err := s.RedeemInvite(code); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("second redeem = %v, want ErrInviteUsed", err)
	}
	if after := accountCount(t, s); after != before {
		t.Errorf("account count changed %d -> %d on failed redeem", before, after)
	}
}

func TestRedeemInviteErrors(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 1000)

	if _, err := s.RedeemInvite("never-minted"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("unknown code = %v, want ErrInviteInvalid", err)
	}

	// Expired.
	expCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: time.Minute}) // expires 1060
	clockAt(s, 2000)
	if _, err := s.RedeemInvite(expCode); !errors.Is(err, ErrInviteExpired) {
		t.Errorf("expired code = %v, want ErrInviteExpired", err)
	}

	// Revoked (unredeemed) code.
	revCode, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: time.Hour})
	if err := s.RevokeInvite(revCode); err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}
	if _, err := s.RedeemInvite(revCode); !errors.Is(err, ErrInviteRevoked) {
		t.Errorf("revoked code = %v, want ErrInviteRevoked", err)
	}
}

func TestRevokeInviteUnknown(t *testing.T) {
	s := newTestStore(t)
	if err := s.RevokeInvite("nope"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("revoke unknown = %v, want ErrInviteInvalid", err)
	}
}

func TestRevokeInviteByLineageBurnsOnBan(t *testing.T) {
	s := newTestStore(t)
	code, inv, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0})
	acc, _ := s.RedeemInvite(code)

	// Ban flow: burn the creating invite by the account's lineage.
	if err := s.RevokeInviteByLineage(acc.InviteLineage); err != nil {
		t.Fatalf("RevokeInviteByLineage: %v", err)
	}
	// Idempotent: burning again is a no-op.
	if err := s.RevokeInviteByLineage(acc.InviteLineage); err != nil {
		t.Errorf("second burn should be a no-op: %v", err)
	}
	// A lineage that never existed is also a no-op (not an error).
	if err := s.RevokeInviteByLineage("no-such-lineage"); err != nil {
		t.Errorf("burning unknown lineage: %v", err)
	}
	_ = inv
}

func TestMintInviteRoleValidation(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.MintInvite(MintParams{Role: "overlord", TTL: time.Hour}); err == nil {
		t.Error("invalid role should be rejected")
	}
	code, inv, err := s.MintInvite(MintParams{Role: "", TTL: time.Hour})
	if err != nil {
		t.Fatalf("empty role: %v", err)
	}
	if inv.Role != RoleMember {
		t.Errorf("default role = %q, want member", inv.Role)
	}
	acc, _ := s.RedeemInvite(code)
	if acc.Role != RoleMember {
		t.Errorf("redeemed default role = %q, want member", acc.Role)
	}
}

func TestRedeemInviteAndRegister(t *testing.T) {
	s := newTestStore(t)
	code, inv, _ := s.MintInvite(MintParams{Role: RoleCreator, TTL: 0})

	acc, err := s.RedeemInviteAndRegister(code, "chosen-handle", Credential{
		CredID: "cred-x", PublicKey: []byte{0x01, 0x02}, Label: "YubiKey",
	}, RoleCreator)
	if err != nil {
		t.Fatalf("RedeemInviteAndRegister: %v", err)
	}
	// The account carries the caller-supplied handle (== WebAuthn user.id) and the
	// invite's role/lineage.
	if acc.UserHandle != "chosen-handle" {
		t.Errorf("handle = %q, want chosen-handle", acc.UserHandle)
	}
	if acc.Role != RoleCreator || acc.InviteLineage != inv.Lineage {
		t.Errorf("account = %+v", acc)
	}
	// The credential was stored and links to the new account, in the same tx.
	cred, ok, _ := s.CredentialByCredID("cred-x")
	if !ok || cred.AccountID != acc.ID {
		t.Errorf("credential not linked: ok=%v cred=%+v", ok, cred)
	}
	// The account resolves by its handle (the assertion lookup path).
	if byH, ok, _ := s.AccountByHandle("chosen-handle"); !ok || byH.ID != acc.ID {
		t.Errorf("AccountByHandle failed: ok=%v", ok)
	}
}

func TestRedeemInviteAndRegisterRoleMismatch(t *testing.T) {
	s := newTestStore(t)
	// A member invite presented where a creator is expected (or vice-versa) is
	// rejected as invalid and consumes nothing.
	code, _, _ := s.MintInvite(MintParams{Role: RoleMember, TTL: 0})
	before := accountCount(t, s)
	if _, err := s.RedeemInviteAndRegister(code, "h", Credential{CredID: "c", PublicKey: []byte{1}}, RoleCreator); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("member invite as creator = %v, want ErrInviteInvalid", err)
	}
	if accountCount(t, s) != before {
		t.Error("role mismatch created an account")
	}
	// The invite is untouched — it still redeems for its actual role.
	if _, err := s.RedeemInviteAndRegister(code, "h", Credential{CredID: "c", PublicKey: []byte{1}}, RoleMember); err != nil {
		t.Errorf("member invite as member should work: %v", err)
	}
}

func TestRedeemInviteAndRegisterValidation(t *testing.T) {
	s := newTestStore(t)
	code, _, _ := s.MintInvite(MintParams{Role: RoleCreator, TTL: 0})
	if _, err := s.RedeemInviteAndRegister(code, "", Credential{CredID: "c", PublicKey: []byte{1}}, ""); err == nil {
		t.Error("empty handle should error")
	}
	if _, err := s.RedeemInviteAndRegister(code, "h", Credential{PublicKey: []byte{1}}, ""); err == nil {
		t.Error("missing cred id should error")
	}
	// The failed attempts above must not have consumed the invite: a valid one now works.
	if _, err := s.RedeemInviteAndRegister(code, "h", Credential{CredID: "c", PublicKey: []byte{1}}, ""); err != nil {
		t.Errorf("invite should still be redeemable after validation failures: %v", err)
	}
}

func TestRedeemInviteAndRegisterAtomicOnBadInvite(t *testing.T) {
	s := newTestStore(t)
	// An invalid invite creates no account and stores no credential.
	before := accountCount(t, s)
	if _, err := s.RedeemInviteAndRegister("never-minted", "h", Credential{CredID: "c", PublicKey: []byte{1}}, ""); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("bad invite = %v, want ErrInviteInvalid", err)
	}
	if accountCount(t, s) != before {
		t.Error("account created despite invalid invite")
	}
	if _, ok, _ := s.CredentialByCredID("c"); ok {
		t.Error("credential stored despite invalid invite")
	}
}

func TestRedeemInviteAndRegisterSingleUse(t *testing.T) {
	s := newTestStore(t)
	code, _, _ := s.MintInvite(MintParams{Role: RoleCreator, TTL: 0})
	if _, err := s.RedeemInviteAndRegister(code, "h1", Credential{CredID: "c1", PublicKey: []byte{1}}, ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := accountCount(t, s)
	if _, err := s.RedeemInviteAndRegister(code, "h2", Credential{CredID: "c2", PublicKey: []byte{2}}, ""); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("second = %v, want ErrInviteUsed", err)
	}
	if accountCount(t, s) != before {
		t.Error("second registration created an account")
	}
	if _, ok, _ := s.CredentialByCredID("c2"); ok {
		t.Error("second registration stored a credential")
	}
}

// accountCount is a small helper for asserting no account was created on failure.
func accountCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	return n
}
