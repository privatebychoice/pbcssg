// Package webauthn implements the slice of WebAuthn (§2.4) pbcssg needs:
// verifying registration and assertion responses for usernameless, discoverable
// credentials with user verification required and attestation "none". Signature
// verification uses only the Go standard library (crypto/ecdsa, crypto/ed25519,
// crypto/sha256); the sole external dependency is a CBOR decoder
// (github.com/fxamacker/cbor/v2) for the attestation object and COSE public keys.
//
// This is security-critical code. The verification checklist (type, challenge,
// origin, RP-ID hash, user-present/user-verified flags, signature, and signature
// counter) is implemented explicitly and covered by adversarial tests.
package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"errors"
	"fmt"
	"math/big"

	"github.com/fxamacker/cbor/v2"
)

// COSE algorithm identifiers (RFC 9053) that we support. ES256 is the near-universal
// default for security keys and platform authenticators; Ed25519 (EdDSA) is
// supported by some keys. Other algorithms are rejected with a clear error rather
// than silently accepted.
const (
	algES256 = -7 // ECDSA w/ SHA-256 on P-256
	algEdDSA = -8 // EdDSA (Ed25519)
)

// COSE key-type and curve identifiers.
const (
	coseKtyOKP = 1 // Octet Key Pair (Ed25519)
	coseKtyEC2 = 2 // two-coordinate elliptic curve (P-256)

	coseCrvP256    = 1
	coseCrvEd25519 = 6
)

// coseKey is the subset of a COSE_Key map we parse. Struct tags are the integer
// map labels. For EC2, -1/-2/-3 are crv/x/y; for OKP, -1/-2 are crv/x. RSA (kty 3)
// reuses -1/-2 for n/e and is intentionally not modelled here — it is rejected.
type coseKey struct {
	Kty int    `cbor:"1,keyasint"`
	Alg int    `cbor:"3,keyasint"`
	Crv int    `cbor:"-1,keyasint"`
	X   []byte `cbor:"-2,keyasint"`
	Y   []byte `cbor:"-3,keyasint"`
}

// parseCOSEKey decodes a COSE_Key from the front of b and returns the algorithm, a
// stdlib public key, and any bytes after the key (authenticator-data extensions,
// which we ignore). It rejects unsupported key types, curves, algorithms, and
// malformed coordinates.
func parseCOSEKey(b []byte) (alg int, pub crypto.PublicKey, rest []byte, err error) {
	var k coseKey
	rest, err = cbor.UnmarshalFirst(b, &k)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("webauthn: decode COSE key: %w", err)
	}
	switch k.Kty {
	case coseKtyEC2:
		if k.Alg != algES256 {
			return 0, nil, nil, fmt.Errorf("webauthn: EC2 key with unsupported alg %d (want ES256)", k.Alg)
		}
		if k.Crv != coseCrvP256 {
			return 0, nil, nil, fmt.Errorf("webauthn: EC2 key with unsupported curve %d (want P-256)", k.Crv)
		}
		if len(k.X) != 32 || len(k.Y) != 32 {
			return 0, nil, nil, errors.New("webauthn: EC2 key coordinates must be 32 bytes")
		}
		x := new(big.Int).SetBytes(k.X)
		y := new(big.Int).SetBytes(k.Y)
		if !elliptic.P256().IsOnCurve(x, y) {
			return 0, nil, nil, errors.New("webauthn: EC2 public key is not on P-256")
		}
		return algES256, &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, rest, nil

	case coseKtyOKP:
		if k.Alg != algEdDSA {
			return 0, nil, nil, fmt.Errorf("webauthn: OKP key with unsupported alg %d (want EdDSA)", k.Alg)
		}
		if k.Crv != coseCrvEd25519 {
			return 0, nil, nil, fmt.Errorf("webauthn: OKP key with unsupported curve %d (want Ed25519)", k.Crv)
		}
		if len(k.X) != ed25519.PublicKeySize {
			return 0, nil, nil, fmt.Errorf("webauthn: Ed25519 key must be %d bytes", ed25519.PublicKeySize)
		}
		return algEdDSA, ed25519.PublicKey(append([]byte(nil), k.X...)), rest, nil

	default:
		return 0, nil, nil, fmt.Errorf("webauthn: unsupported COSE key type %d (want EC2 or OKP)", k.Kty)
	}
}

// marshalCOSEKey re-encodes a supported public key as a COSE_Key. This is used to
// store the credential public key in a stable, self-describing form (the same COSE
// encoding WebAuthn delivered), so assertion verification can reload it.
func marshalCOSEKey(alg int, pub crypto.PublicKey) ([]byte, error) {
	switch alg {
	case algES256:
		ec, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("webauthn: ES256 requires an ecdsa public key")
		}
		k := coseKey{Kty: coseKtyEC2, Alg: algES256, Crv: coseCrvP256,
			X: leftPad(ec.X.Bytes(), 32), Y: leftPad(ec.Y.Bytes(), 32)}
		return cbor.Marshal(k)
	case algEdDSA:
		ed, ok := pub.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("webauthn: EdDSA requires an ed25519 public key")
		}
		k := coseKey{Kty: coseKtyOKP, Alg: algEdDSA, Crv: coseCrvEd25519, X: []byte(ed)}
		return cbor.Marshal(k)
	default:
		return nil, fmt.Errorf("webauthn: cannot marshal unsupported alg %d", alg)
	}
}

// leftPad returns b left-padded with zeroes to length n (big-endian coordinates
// can be shorter than the field size when the high byte is zero).
func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}
