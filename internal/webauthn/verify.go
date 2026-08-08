package webauthn

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Verifier verifies WebAuthn ceremonies for one relying party. RPID is the relying
// party identifier (a registrable domain, e.g. "example.com"); Origin is the exact
// origin the ceremony must have come from (e.g. "https://example.com"). Both are
// checked on every ceremony.
type Verifier struct {
	RPID   string
	Origin string
}

// rpIDHash is the SHA-256 of the RP ID, compared against authenticatorData.
func (v Verifier) rpIDHash() [32]byte { return sha256.Sum256([]byte(v.RPID)) }

// RegistrationResponse is the authenticator's reply to a create() ceremony.
type RegistrationResponse struct {
	ClientDataJSON    []byte
	AttestationObject []byte
}

// VerifiedCredential is the credential material to persist after a successful
// registration (maps onto appstore.Credential).
type VerifiedCredential struct {
	CredID        []byte
	COSEPublicKey []byte // stable COSE re-encoding, reloaded for assertion
	Alg           int
	SignCount     uint32
	AAGUID        []byte
}

// attestationObject is the CBOR structure wrapping the authenticator data.
type attestationObject struct {
	Fmt      string          `cbor:"fmt"`
	AuthData []byte          `cbor:"authData"`
	AttStmt  cbor.RawMessage `cbor:"attStmt"`
}

// VerifyRegistration checks a registration response against the issued challenge and
// returns the credential to store. It enforces: clientData (type/challenge/origin),
// attestation format "none" (no AAGUID fingerprint — §2.4), presence of attested
// credential data, RP-ID hash match, and User-Present + User-Verified (userVerification
// required). Attestation statements are not verified because we request none.
func (v Verifier) VerifyRegistration(challenge []byte, resp RegistrationResponse) (*VerifiedCredential, error) {
	if err := verifyClientData(resp.ClientDataJSON, typeCreate, v.Origin, challenge); err != nil {
		return nil, err
	}
	var ao attestationObject
	if err := cbor.Unmarshal(resp.AttestationObject, &ao); err != nil {
		return nil, fmt.Errorf("webauthn: decode attestationObject: %w", err)
	}
	if ao.Fmt != "none" {
		return nil, fmt.Errorf("webauthn: attestation fmt %q, want \"none\"", ao.Fmt)
	}
	ad, err := parseAuthData(ao.AuthData)
	if err != nil {
		return nil, err
	}
	if !ad.hasAttestedCred() {
		return nil, errors.New("webauthn: registration authData has no attested credential")
	}
	if err := v.checkAuthData(ad); err != nil {
		return nil, err
	}
	return &VerifiedCredential{
		CredID:        append([]byte(nil), ad.credID...),
		COSEPublicKey: ad.credKeyDER,
		Alg:           ad.credAlg,
		SignCount:     ad.signCount,
		AAGUID:        append([]byte(nil), ad.aaguid...),
	}, nil
}

// AssertionResponse is the authenticator's reply to a get() ceremony.
type AssertionResponse struct {
	ClientDataJSON    []byte
	AuthenticatorData []byte
	Signature         []byte
}

// StoredCredential is the persisted material an assertion is checked against.
type StoredCredential struct {
	COSEPublicKey []byte
	SignCount     uint32
}

// VerifyAssertion checks an assertion response against the issued challenge and the
// stored credential, returning the authenticator's new signature counter (to persist).
// It enforces clientData (type/challenge/origin), RP-ID hash match, User-Present +
// User-Verified, the signature over authenticatorData ‖ SHA-256(clientDataJSON), and
// signature-counter monotonicity for keys that maintain a counter.
func (v Verifier) VerifyAssertion(challenge []byte, cred StoredCredential, resp AssertionResponse) (uint32, error) {
	if err := verifyClientData(resp.ClientDataJSON, typeGet, v.Origin, challenge); err != nil {
		return 0, err
	}
	ad, err := parseAuthData(resp.AuthenticatorData)
	if err != nil {
		return 0, err
	}
	if err := v.checkAuthData(ad); err != nil {
		return 0, err
	}

	alg, pub, _, err := parseCOSEKey(cred.COSEPublicKey)
	if err != nil {
		return 0, fmt.Errorf("webauthn: reload stored key: %w", err)
	}
	cdHash := sha256.Sum256(resp.ClientDataJSON)
	signed := append(append([]byte(nil), resp.AuthenticatorData...), cdHash[:]...)
	if err := verifySignature(alg, pub, signed, resp.Signature); err != nil {
		return 0, err
	}

	// Clone detection: if either counter is non-zero the authenticator maintains one,
	// so the new value must strictly exceed the stored one. Both zero means a synced
	// passkey with no counter — accepted (§2.4).
	if ad.signCount != 0 || cred.SignCount != 0 {
		if ad.signCount <= cred.SignCount {
			return 0, fmt.Errorf("webauthn: signature counter did not increase (%d <= %d): possible cloned authenticator", ad.signCount, cred.SignCount)
		}
	}
	return ad.signCount, nil
}

// checkAuthData applies the checks common to both ceremonies: RP-ID hash match (in
// constant time) and the User-Present + User-Verified flags. UV is required because
// every ceremony requests userVerification "required" (§2.4).
func (v Verifier) checkAuthData(ad authData) error {
	want := v.rpIDHash()
	if subtle.ConstantTimeCompare(ad.rpIDHash, want[:]) != 1 {
		return errors.New("webauthn: RP ID hash mismatch")
	}
	if !ad.userPresent() {
		return errors.New("webauthn: user-present flag not set")
	}
	if !ad.userVerified() {
		return errors.New("webauthn: user-verified flag not set (userVerification required)")
	}
	return nil
}

// verifySignature checks sig over signed using the credential's algorithm. ES256
// signatures are ASN.1 DER over SHA-256(signed); Ed25519 signs the message directly.
func verifySignature(alg int, pub any, signed, sig []byte) error {
	switch alg {
	case algES256:
		ec, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("webauthn: ES256 with non-ecdsa key")
		}
		h := sha256.Sum256(signed)
		if !ecdsa.VerifyASN1(ec, h[:], sig) {
			return errors.New("webauthn: ES256 signature invalid")
		}
		return nil
	case algEdDSA:
		ed, ok := pub.(ed25519.PublicKey)
		if !ok {
			return errors.New("webauthn: EdDSA with non-ed25519 key")
		}
		if !ed25519.Verify(ed, signed, sig) {
			return errors.New("webauthn: Ed25519 signature invalid")
		}
		return nil
	default:
		return fmt.Errorf("webauthn: unsupported algorithm %d", alg)
	}
}
