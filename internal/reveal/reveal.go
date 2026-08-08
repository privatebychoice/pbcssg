// Package reveal is the build-time encoder for the deferred-reveal ("hidden")
// content block (SPEC §6.9). It encrypts a block's short first-party plaintext
// with AES-256-GCM so the value is absent from the served HTML until the visitor
// clicks to reveal it — keeping it out of view-source, find-in-page, search
// crawlers, and naive scrapers.
//
// Honesty (SPEC §5.4): this is *obfuscation, not security*. In Mode A the AES key
// travels with the page (shipped as the block's data-key), so any client that
// runs the JS can decode it. It deliberately provides no confidentiality; it only
// keeps the plaintext out of the static markup until an explicit action.
//
// Determinism: the per-block key is HKDF-derived from the page's stored key and
// the block index, and the GCM nonce is HMAC-derived from the plaintext. Given the
// same stored key and content the output is byte-identical build to build, so
// reveal blocks never churn build.json hashes (SPEC §6.9). Because the nonce is a
// function of the plaintext, editing a block's content changes the nonce — so the
// same (key, nonce) pair is never reused for two different messages, which keeps
// the construction sound for the future code-gated mode (Mode B) where the key is
// secret.
package reveal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
)

const (
	// keyLen is the AES-256 key length; nonceLen is the standard 96-bit GCM nonce.
	keyLen   = 32
	nonceLen = 12
	// saltLen is the per-block PBKDF2 salt length (Mode B).
	saltLen = 16
	// PBKDF2Iters is the PBKDF2-SHA256 iteration count for the Mode B code gate. It
	// ships in the page (data-iters) so the client uses the same count; kept in one
	// place so build and client never drift. Sized to slow brute force of a
	// low-entropy code (OWASP-order guidance) while staying a sub-second unlock.
	PBKDF2Iters = 600000
	// info/label namespaces keep the derivations domain-separated and versioned, so
	// a future scheme change can bump the version without colliding with old output.
	keyInfo   = "pbcssg/reveal/v1/key/"
	saltInfo  = "pbcssg/reveal/v1/salt/"
	nonceInfo = "pbcssg/reveal/v1/nonce/"
	// gateInfo namespaces the per-block data-encryption key (DEK) for group-gated
	// content (SPEC §6.10), kept separate from the reveal key derivations above.
	gateInfo = "pbcssg/gate/v1/dek/"
)

// Encoded is the transport form of an encrypted reveal payload: base64 (standard,
// padded) strings placed into the block's data-* attributes. Key is the per-block
// AES key that ships in Mode A so the client can decode; it is empty for the
// code-gated Mode B, where the client derives the key from the visitor's code via
// PBKDF2 over the shipped Salt and Iters (Key stays empty so the code is required).
type Encoded struct {
	Ciphertext string // base64 of AES-GCM ciphertext with the appended tag
	Nonce      string // base64 of the 96-bit GCM nonce
	Key        string // base64 of the per-block AES key (Mode A only)
	Salt       string // base64 of the per-block PBKDF2 salt (Mode B only)
	Iters      int    // PBKDF2 iteration count (Mode B only; 0 in Mode A)
}

// EncodeA encrypts plaintext for Mode A (obfuscation): it derives a per-block key
// from the page key and the block index, seals the plaintext, and returns the
// ciphertext, nonce, and the per-block key (which ships in the page). pageKey must
// be non-empty; callers hold a stable per-page key from the store.
func EncodeA(pageKey []byte, index int, plaintext string) (Encoded, error) {
	if len(pageKey) == 0 {
		return Encoded{}, fmt.Errorf("reveal: empty page key")
	}
	blockKey, err := deriveKey(pageKey, index)
	if err != nil {
		return Encoded{}, err
	}
	ct, nonce, err := seal(blockKey, []byte(plaintext))
	if err != nil {
		return Encoded{}, err
	}
	enc := base64.StdEncoding
	return Encoded{
		Ciphertext: enc.EncodeToString(ct),
		Nonce:      enc.EncodeToString(nonce),
		Key:        enc.EncodeToString(blockKey),
	}, nil
}

// EncodeB encrypts plaintext for Mode B (the code gate): the AES key is derived
// from the visitor's code via PBKDF2 over a per-block salt, so without the code
// there is nothing to decode. The code is NOT returned or shipped — only the salt
// and iteration count are, so the client can re-derive the key from the code the
// visitor types. Security rests entirely on the code's entropy; the page key acts
// only as a public per-page salt source (SPEC §6.9). code must be non-empty.
func EncodeB(pageKey []byte, index int, code, plaintext string) (Encoded, error) {
	if len(pageKey) == 0 {
		return Encoded{}, fmt.Errorf("reveal: empty page key")
	}
	if code == "" {
		return Encoded{}, fmt.Errorf("reveal: empty code")
	}
	salt, err := deriveSalt(pageKey, index)
	if err != nil {
		return Encoded{}, err
	}
	dk, err := pbkdf2.Key(sha256.New, code, salt, PBKDF2Iters, keyLen)
	if err != nil {
		return Encoded{}, fmt.Errorf("reveal: pbkdf2: %w", err)
	}
	ct, nonce, err := seal(dk, []byte(plaintext))
	if err != nil {
		return Encoded{}, err
	}
	enc := base64.StdEncoding
	return Encoded{
		Ciphertext: enc.EncodeToString(ct),
		Nonce:      enc.EncodeToString(nonce),
		Salt:       enc.EncodeToString(salt),
		Iters:      PBKDF2Iters,
	}, nil
}

// WrappedDEK is one block DEK sealed under one group's KEK (SPEC §6.10). It ships
// unlabeled — no alias, no order that maps to a group — so a crawler learns nothing
// about which or how many groups can unlock the block. The client trial-unwraps it
// with each keyring KEK; a GCM authentication success yields the DEK.
type WrappedDEK struct {
	Ciphertext string // base64 of AES-GCM(KEK, DEK) with the appended tag
	Nonce      string // base64 of the 96-bit GCM nonce
}

// GateEncoded is the transport form of a group-gated block (SPEC §6.10): the block
// content sealed under a per-block DEK, plus the DEK wrapped once per authorized
// group. Neither the DEK nor any KEK is ever emitted in the clear — only ciphertext
// and the wrapped-DEK blobs reach the page.
type GateEncoded struct {
	Ciphertext string       // base64 of AES-GCM(DEK, plaintext) with the appended tag
	Nonce      string       // base64 of the 96-bit GCM nonce
	Wrapped    []WrappedDEK // one per authorized group, in a content-derived stable order
}

// EncodeGate seals plaintext under a per-block DEK and wraps that DEK under each
// supplied group KEK (SPEC §6.10, envelope encryption). The DEK is derived
// deterministically from the page key and block index (HKDF) and is *server-only* —
// unlike the reveal Mode A key it never ships, so the block stays secret to anyone
// without a KEK. The wrapped blobs are returned in a stable order derived from their
// ciphertext (not the KEK order), so their sequence leaks nothing about the groups.
// At least one KEK is required (a block with no authorized group is not gated).
func EncodeGate(pageKey []byte, index int, plaintext string, keks [][]byte) (GateEncoded, error) {
	if len(pageKey) == 0 {
		return GateEncoded{}, fmt.Errorf("reveal: empty page key")
	}
	if len(keks) == 0 {
		return GateEncoded{}, fmt.Errorf("reveal: gate block has no group keys")
	}
	dek, err := hkdf.Key(sha256.New, pageKey, nil, gateInfo+strconv.Itoa(index), keyLen)
	if err != nil {
		return GateEncoded{}, fmt.Errorf("reveal: derive DEK: %w", err)
	}
	ct, nonce, err := seal(dek, []byte(plaintext))
	if err != nil {
		return GateEncoded{}, err
	}
	enc := base64.StdEncoding
	wrapped := make([]WrappedDEK, 0, len(keks))
	for _, kek := range keks {
		if len(kek) == 0 {
			return GateEncoded{}, fmt.Errorf("reveal: empty group key")
		}
		wct, wnonce, err := seal(kek, dek) // wrap the DEK: AES-GCM(KEK, DEK)
		if err != nil {
			return GateEncoded{}, fmt.Errorf("reveal: wrap DEK: %w", err)
		}
		wrapped = append(wrapped, WrappedDEK{
			Ciphertext: enc.EncodeToString(wct),
			Nonce:      enc.EncodeToString(wnonce),
		})
	}
	// Stable, group-independent order so the sequence discloses nothing about which
	// or how many groups authorize the block.
	sort.Slice(wrapped, func(i, j int) bool { return wrapped[i].Ciphertext < wrapped[j].Ciphertext })
	return GateEncoded{
		Ciphertext: enc.EncodeToString(ct),
		Nonce:      enc.EncodeToString(nonce),
		Wrapped:    wrapped,
	}, nil
}

// deriveSalt derives the per-block PBKDF2 salt via HKDF-SHA256 over the page key,
// domain-separated by the block index. It is shipped in the page (data-salt); the
// raw page key is never shipped in Mode B, so the salt is unique per block without
// exposing the page key directly.
func deriveSalt(pageKey []byte, index int) ([]byte, error) {
	s, err := hkdf.Key(sha256.New, pageKey, nil, saltInfo+strconv.Itoa(index), saltLen)
	if err != nil {
		return nil, fmt.Errorf("reveal: derive salt: %w", err)
	}
	return s, nil
}

// deriveKey derives the per-block AES key via HKDF-SHA256 over the page key,
// domain-separated by the block index so two blocks on a page never share a key.
func deriveKey(pageKey []byte, index int) ([]byte, error) {
	k, err := hkdf.Key(sha256.New, pageKey, nil, keyInfo+strconv.Itoa(index), keyLen)
	if err != nil {
		return nil, fmt.Errorf("reveal: derive key: %w", err)
	}
	return k, nil
}

// seal AES-256-GCM-encrypts plaintext under key with a deterministic,
// plaintext-derived nonce (HMAC-SHA256(key, plaintext) truncated to 96 bits). The
// nonce is a function of the message, so it is stable across builds yet unique per
// distinct plaintext under a given key — satisfying GCM's no-reuse requirement.
func seal(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("reveal: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("reveal: gcm: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nonceInfo))
	mac.Write(plaintext)
	nonce = mac.Sum(nil)[:nonceLen]
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}
