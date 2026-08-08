package appstore

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestAddCredentialAndLookup(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 500)
	a, _ := s.CreateAccount(RoleMember, "")

	id, err := s.AddCredential(Credential{
		AccountID: a.ID, CredID: "cred-A", PublicKey: []byte{0xA1, 0xB2},
		SignCount: 3, AAGUID: "", Transports: "usb,nfc", Label: "YubiKey 5",
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if id == 0 {
		t.Fatal("no row id returned")
	}

	got, ok, err := s.CredentialByCredID("cred-A")
	if err != nil || !ok {
		t.Fatalf("CredentialByCredID: ok=%v err=%v", ok, err)
	}
	if got.AccountID != a.ID || !bytes.Equal(got.PublicKey, []byte{0xA1, 0xB2}) {
		t.Errorf("credential = %+v", got)
	}
	if got.SignCount != 3 || got.Transports != "usb,nfc" || got.Label != "YubiKey 5" {
		t.Errorf("credential fields = %+v", got)
	}
	if !got.CreatedAt.Equal(time.Unix(500, 0)) {
		t.Errorf("created_at = %v, want 500", got.CreatedAt)
	}
	if !got.LastUsedAt.IsZero() {
		t.Errorf("last_used = %v, want zero before first use", got.LastUsedAt)
	}

	if _, ok, err := s.CredentialByCredID("absent"); err != nil || ok {
		t.Errorf("missing credential: ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestAddCredentialValidation(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	cases := []struct {
		name string
		c    Credential
	}{
		{"no account", Credential{CredID: "x", PublicKey: []byte{1}}},
		{"no cred id", Credential{AccountID: a.ID, PublicKey: []byte{1}}},
		{"no public key", Credential{AccountID: a.ID, CredID: "x"}},
	}
	for _, tc := range cases {
		if _, err := s.AddCredential(tc.c); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
	// Dangling account id is rejected by the foreign key.
	if _, err := s.AddCredential(Credential{AccountID: 4242, CredID: "y", PublicKey: []byte{1}}); err == nil {
		t.Error("dangling account_id should violate the foreign key")
	}
}

func TestDuplicateCredIDRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	if _, err := s.AddCredential(Credential{AccountID: a.ID, CredID: "dup", PublicKey: []byte{1}}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// The same credential ID must be globally unique, even under a different account.
	b, _ := s.CreateAccount(RoleMember, "")
	if _, err := s.AddCredential(Credential{AccountID: b.ID, CredID: "dup", PublicKey: []byte{2}}); err == nil {
		t.Error("duplicate cred_id should be rejected")
	}
}

func TestMultipleCredentialsPerAccount(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100)
	a, _ := s.CreateAccount(RoleCreator, "")

	s.AddCredential(Credential{AccountID: a.ID, CredID: "k1", PublicKey: []byte{1}, Label: "primary"})
	clockAt(s, 200)
	s.AddCredential(Credential{AccountID: a.ID, CredID: "k2", PublicKey: []byte{2}, Label: "backup"})

	creds, err := s.CredentialsForAccount(a.ID)
	if err != nil {
		t.Fatalf("CredentialsForAccount: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("count = %d, want 2 (>=2 authenticators for elevated roles)", len(creds))
	}
	if creds[0].CredID != "k1" || creds[1].CredID != "k2" {
		t.Errorf("order = %q,%q, want k1,k2 (oldest first)", creds[0].CredID, creds[1].CredID)
	}
}

func TestUpdateSignCount(t *testing.T) {
	s := newTestStore(t)
	clockAt(s, 100)
	a, _ := s.CreateAccount(RoleMember, "")
	s.AddCredential(Credential{AccountID: a.ID, CredID: "k", PublicKey: []byte{1}, SignCount: 5})

	clockAt(s, 900)
	if err := s.UpdateSignCount("k", 6); err != nil {
		t.Fatalf("UpdateSignCount: %v", err)
	}
	got, _, _ := s.CredentialByCredID("k")
	if got.SignCount != 6 {
		t.Errorf("sign_count = %d, want 6", got.SignCount)
	}
	if !got.LastUsedAt.Equal(time.Unix(900, 0)) {
		t.Errorf("last_used = %v, want 900", got.LastUsedAt)
	}
	if err := s.UpdateSignCount("missing", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v, want ErrNotFound", err)
	}
}

func TestSetCredentialLabelScoped(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleCreator, "")
	b, _ := s.CreateAccount(RoleCreator, "")
	idA, _ := s.AddCredential(Credential{AccountID: a.ID, CredID: "ka", PublicKey: []byte{1}, Label: "old"})

	if err := s.SetCredentialLabel(idA, a.ID, "Laptop"); err != nil {
		t.Fatalf("SetCredentialLabel: %v", err)
	}
	if got, _, _ := s.CredentialByCredID("ka"); got.Label != "Laptop" {
		t.Errorf("label = %q, want Laptop", got.Label)
	}
	// Scoped: relabeling a's key as if it were b's does not match.
	if err := s.SetCredentialLabel(idA, b.ID, "Nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account relabel = %v, want ErrNotFound", err)
	}
	if got, _, _ := s.CredentialByCredID("ka"); got.Label != "Laptop" {
		t.Errorf("label changed across accounts: %q", got.Label)
	}
}

func TestDeleteCredentialScoped(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateAccount(RoleMember, "")
	b, _ := s.CreateAccount(RoleMember, "")
	idA, _ := s.AddCredential(Credential{AccountID: a.ID, CredID: "ka", PublicKey: []byte{1}})
	s.AddCredential(Credential{AccountID: b.ID, CredID: "kb", PublicKey: []byte{2}})

	// Deleting A's credential under B's id must not touch it.
	if err := s.DeleteCredential(idA, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete = %v, want ErrNotFound", err)
	}
	if _, ok, _ := s.CredentialByCredID("ka"); !ok {
		t.Error("credential was deleted by a mismatched account id")
	}
	// Correct owner deletes it.
	if err := s.DeleteCredential(idA, a.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, ok, _ := s.CredentialByCredID("ka"); ok {
		t.Error("credential still present after owner delete")
	}
	// B's key is untouched.
	if _, ok, _ := s.CredentialByCredID("kb"); !ok {
		t.Error("unrelated account's credential was removed")
	}
}
