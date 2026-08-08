package reveal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// openEncoded decrypts an Encoded payload the way the client Web Crypto code does:
// import the shipped key and AES-GCM-decrypt the ciphertext with the nonce.
func openEncoded(t *testing.T, e Encoded) string {
	t.Helper()
	dec := base64.StdEncoding
	key, err := dec.DecodeString(e.Key)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	nonce, err := dec.DecodeString(e.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	ct, err := dec.DecodeString(e.Ciphertext)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	return string(pt)
}

func TestEncodeARoundTrip(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	for _, pt := range []string{"hi@example.com", "The butler did it.", "", "unicode: café — 🔒"} {
		enc, err := EncodeA(pageKey, 0, pt)
		if err != nil {
			t.Fatalf("EncodeA(%q): %v", pt, err)
		}
		if got := openEncoded(t, enc); got != pt {
			t.Errorf("round-trip: got %q, want %q", got, pt)
		}
	}
}

func TestEncodeADeterministic(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, err := EncodeA(pageKey, 2, "spoiler text")
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeA(pageKey, 2, "spoiler text")
	if err != nil {
		t.Fatal(err)
	}
	// Same key + index + plaintext must reproduce byte-identical output, so reveal
	// blocks never churn build.json hashes (SPEC §6.9).
	if a != b {
		t.Errorf("EncodeA not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

func TestEncodeADistinctPerIndex(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, _ := EncodeA(pageKey, 0, "same text")
	b, _ := EncodeA(pageKey, 1, "same text")
	// Different block indices derive different keys, so identical plaintext yields
	// different ciphertext/key material — two blocks never share a key.
	if a.Key == b.Key {
		t.Errorf("blocks at different indices share a key: %q", a.Key)
	}
	if a.Ciphertext == b.Ciphertext {
		t.Errorf("blocks at different indices share ciphertext")
	}
}

func TestEncodeANonceVariesWithPlaintext(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, _ := EncodeA(pageKey, 0, "before")
	b, _ := EncodeA(pageKey, 0, "after")
	// The nonce is derived from the plaintext, so editing a block's content changes
	// the nonce — the (key, nonce) pair is never reused for two different messages.
	if a.Nonce == b.Nonce {
		t.Errorf("nonce did not change with plaintext: %q", a.Nonce)
	}
}

func TestEncodeAEmptyKeyRejected(t *testing.T) {
	if _, err := EncodeA(nil, 0, "x"); err == nil {
		t.Error("EncodeA(nil key) should error")
	}
}

// openWithCode decrypts a Mode B payload the way the client does: PBKDF2-derive the
// key from the code + shipped salt/iters, then AES-GCM-decrypt.
func openWithCode(t *testing.T, e Encoded, code string) ([]byte, error) {
	t.Helper()
	dec := base64.StdEncoding
	salt, err := dec.DecodeString(e.Salt)
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	nonce, _ := dec.DecodeString(e.Nonce)
	ct, _ := dec.DecodeString(e.Ciphertext)
	dk, err := pbkdf2.Key(sha256.New, code, salt, e.Iters, 32)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	block, err := aes.NewCipher(dk)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return gcm.Open(nil, nonce, ct, nil)
}

func TestEncodeBRoundTripAndGate(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	const code = "hunter2"
	const secret = "members-only: the vault code is 4417"
	enc, err := EncodeB(pageKey, 0, code, secret)
	if err != nil {
		t.Fatalf("EncodeB: %v", err)
	}
	// Mode B ships a salt + iteration count and NO key (the code is required).
	if enc.Key != "" {
		t.Error("Mode B must not ship a key")
	}
	if enc.Salt == "" || enc.Iters != PBKDF2Iters {
		t.Errorf("Mode B salt/iters wrong: salt=%q iters=%d", enc.Salt, enc.Iters)
	}
	// The right code decrypts.
	pt, err := openWithCode(t, enc, code)
	if err != nil {
		t.Fatalf("correct code failed to decrypt: %v", err)
	}
	if string(pt) != secret {
		t.Errorf("round-trip: got %q, want %q", pt, secret)
	}
	// A wrong code fails GCM authentication (free code validation — no stored hash).
	if _, err := openWithCode(t, enc, "wrong"); err == nil {
		t.Error("wrong code should fail to decrypt")
	}
}

func TestEncodeBDeterministic(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	a, err := EncodeB(pageKey, 1, "code", "text")
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeB(pageKey, 1, "code", "text")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("EncodeB not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

func TestEncodeBEmptyCodeRejected(t *testing.T) {
	if _, err := EncodeB([]byte("0123456789abcdef0123456789abcdef"), 0, "", "x"); err == nil {
		t.Error("EncodeB with empty code should error")
	}
}

func TestEncodeATamperDetected(t *testing.T) {
	pageKey := []byte("0123456789abcdef0123456789abcdef")
	enc, err := EncodeA(pageKey, 0, "authentic")
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte of the ciphertext; GCM authentication must reject it on open.
	raw, _ := base64.StdEncoding.DecodeString(enc.Ciphertext)
	raw[0] ^= 0xff
	enc.Ciphertext = base64.StdEncoding.EncodeToString(raw)

	key, _ := base64.StdEncoding.DecodeString(enc.Key)
	nonce, _ := base64.StdEncoding.DecodeString(enc.Nonce)
	ct, _ := base64.StdEncoding.DecodeString(enc.Ciphertext)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	if _, err := gcm.Open(nil, nonce, ct, nil); err == nil {
		t.Error("tampered ciphertext should fail GCM authentication")
	}
}
