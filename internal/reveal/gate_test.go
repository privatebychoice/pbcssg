package reveal

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"testing"
)

// gcmOpen decrypts base64 ciphertext+nonce under key the way the client Web Crypto
// code does. ok reports whether GCM authentication succeeded.
func gcmOpen(t *testing.T, key []byte, ctB64, nonceB64 string) (pt []byte, ok bool) {
	t.Helper()
	dec := base64.StdEncoding
	ct, err := dec.DecodeString(ctB64)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	nonce, err := dec.DecodeString(nonceB64)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	out, err := gcm.Open(nil, nonce, ct, nil)
	return out, err == nil
}

// trialUnwrap mimics the client keyring: for each wrapped-DEK, try to unwrap it with
// the KEK; the first GCM success yields the DEK, which decrypts the block content.
// Returns the plaintext and whether any wrapped blob unwrapped with this KEK.
func trialUnwrap(t *testing.T, e GateEncoded, kek []byte) (string, bool) {
	t.Helper()
	for _, w := range e.Wrapped {
		dek, ok := gcmOpen(t, kek, w.Ciphertext, w.Nonce)
		if !ok {
			continue
		}
		pt, ok := gcmOpen(t, dek, e.Ciphertext, e.Nonce)
		if !ok {
			t.Fatalf("DEK unwrapped but content failed to decrypt")
		}
		return string(pt), true
	}
	return "", false
}

// mustKEK builds a 32-byte (AES-256) KEK filled with b — a distinct, readable key
// per test group without needing the store package.
func mustKEK(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestEncodeGateSingleGroupRoundTrip(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	kek := mustKEK(0x11)
	const secret = "members-only content: the door code is 4417"

	enc, err := EncodeGate(pageKey, 0, secret, [][]byte{kek})
	if err != nil {
		t.Fatalf("EncodeGate: %v", err)
	}
	// One authorized group ⇒ exactly one wrapped-DEK blob; neither the DEK nor the
	// KEK appears anywhere in the transport form.
	if len(enc.Wrapped) != 1 {
		t.Fatalf("want 1 wrapped DEK, got %d", len(enc.Wrapped))
	}
	// The holder of the KEK trial-unwraps and reads the content.
	got, ok := trialUnwrap(t, enc, kek)
	if !ok {
		t.Fatal("KEK holder could not unwrap the block")
	}
	if got != secret {
		t.Errorf("round-trip: got %q, want %q", got, secret)
	}
	// A non-holder (different KEK) unwraps nothing.
	if _, ok := trialUnwrap(t, enc, mustKEK(0x22)); ok {
		t.Error("a non-holder KEK should not unwrap the block")
	}
}

func TestEncodeGateMultiGroupOR(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, b, c := mustKEK(0x01), mustKEK(0x02), mustKEK(0x03)
	const secret = "visible to members A or B"

	enc, err := EncodeGate(pageKey, 1, secret, [][]byte{a, b})
	if err != nil {
		t.Fatalf("EncodeGate: %v", err)
	}
	if len(enc.Wrapped) != 2 {
		t.Fatalf("want 2 wrapped DEKs, got %d", len(enc.Wrapped))
	}
	// OR logic: holding EITHER authorized group's key unlocks the block.
	for _, kek := range [][]byte{a, b} {
		got, ok := trialUnwrap(t, enc, kek)
		if !ok || got != secret {
			t.Errorf("authorized KEK failed: ok=%v got=%q", ok, got)
		}
	}
	// A third, unauthorized group cannot unlock it.
	if _, ok := trialUnwrap(t, enc, c); ok {
		t.Error("unauthorized group unlocked a block it was not wrapped for")
	}
}

func TestEncodeGateDeterministic(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	keks := [][]byte{mustKEK(0x01), mustKEK(0x02)}
	x, err := EncodeGate(pageKey, 2, "text", keks)
	if err != nil {
		t.Fatal(err)
	}
	y, err := EncodeGate(pageKey, 2, "text", keks)
	if err != nil {
		t.Fatal(err)
	}
	// Same inputs ⇒ byte-identical output (stored keys, deterministic nonces), so
	// gated blocks never churn build.json hashes (SPEC §6.10).
	if x.Ciphertext != y.Ciphertext || x.Nonce != y.Nonce || len(x.Wrapped) != len(y.Wrapped) {
		t.Fatalf("EncodeGate not deterministic:\n x=%+v\n y=%+v", x, y)
	}
	for i := range x.Wrapped {
		if x.Wrapped[i] != y.Wrapped[i] {
			t.Errorf("wrapped[%d] differs: %+v vs %+v", i, x.Wrapped[i], y.Wrapped[i])
		}
	}
}

func TestEncodeGateWrapOrderIndependentOfKEKOrder(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, b := mustKEK(0x01), mustKEK(0x02)
	// The wrapped-DEK sequence is derived from ciphertext, not the KEK argument order,
	// so it discloses nothing about which group is which (SPEC §6.10 "unlabeled").
	x, err := EncodeGate(pageKey, 0, "text", [][]byte{a, b})
	if err != nil {
		t.Fatal(err)
	}
	y, err := EncodeGate(pageKey, 0, "text", [][]byte{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Wrapped) != len(y.Wrapped) {
		t.Fatalf("length mismatch")
	}
	for i := range x.Wrapped {
		if x.Wrapped[i] != y.Wrapped[i] {
			t.Errorf("wrap order depends on KEK order at %d: %+v vs %+v", i, x.Wrapped[i], y.Wrapped[i])
		}
	}
}

func TestEncodeGateRekeyReWrapsSameContent(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	old := mustKEK(0x01)
	rotated := mustKEK(0x99)
	const secret = "stable content"

	before, err := EncodeGate(pageKey, 0, secret, [][]byte{old})
	if err != nil {
		t.Fatal(err)
	}
	after, err := EncodeGate(pageKey, 0, secret, [][]byte{rotated})
	if err != nil {
		t.Fatal(err)
	}
	// Rotating the KEK re-wraps the DEK but leaves the content ciphertext (under the
	// DEK) unchanged — rotation is a wrapped-DEK-only operation.
	if before.Ciphertext != after.Ciphertext || before.Nonce != after.Nonce {
		t.Error("content ciphertext should be unchanged by a KEK rotation")
	}
	if before.Wrapped[0] == after.Wrapped[0] {
		t.Error("wrapped DEK should change after a KEK rotation")
	}
	// The old link no longer opens it; the new one does.
	if _, ok := trialUnwrap(t, after, old); ok {
		t.Error("rotated-out KEK should no longer unwrap")
	}
	if got, ok := trialUnwrap(t, after, rotated); !ok || got != secret {
		t.Errorf("rotated-in KEK should unwrap: ok=%v got=%q", ok, got)
	}
}

func TestEncodeGateRejectsBadInput(t *testing.T) {
	good := mustKEK(0x01)
	if _, err := EncodeGate(nil, 0, "x", [][]byte{good}); err == nil {
		t.Error("empty page key should error")
	}
	if _, err := EncodeGate([]byte("k"), 0, "x", nil); err == nil {
		t.Error("no group keys should error")
	}
	if _, err := EncodeGate([]byte("0123456789abcdef0123456789abcdef"), 0, "x", [][]byte{nil}); err == nil {
		t.Error("an empty group key should error")
	}
}
