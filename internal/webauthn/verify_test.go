package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

var testVerifier = Verifier{RPID: testRPID, Origin: testOrigin}

// --- fixture builders ---------------------------------------------------------

func clientDataJSON(t *testing.T, typ, origin string, challenge []byte) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"type":      typ,
		"challenge": base64.RawURLEncoding.EncodeToString(challenge),
		"origin":    origin,
	})
	if err != nil {
		t.Fatalf("marshal clientData: %v", err)
	}
	return b
}

func attestedCredData(t *testing.T, aaguid, credID, coseKey []byte) []byte {
	t.Helper()
	b := append([]byte(nil), aaguid...)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(credID)))
	b = append(b, l[:]...)
	b = append(b, credID...)
	b = append(b, coseKey...)
	return b
}

func buildAuthData(t *testing.T, rpID string, flags byte, signCount uint32, attested []byte) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(rpID))
	b := append([]byte(nil), h[:]...)
	b = append(b, flags)
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], signCount)
	b = append(b, sc[:]...)
	return append(b, attested...)
}

func buildAttestationObject(t *testing.T, authData []byte) []byte {
	t.Helper()
	b, err := cbor.Marshal(map[string]any{
		"fmt":      "none",
		"authData": authData,
		"attStmt":  map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal attestationObject: %v", err)
	}
	return b
}

// es256Cred returns a P-256 key and its COSE encoding.
func es256Cred(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	cose, err := marshalCOSEKey(algES256, &priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal cose: %v", err)
	}
	return priv, cose
}

// signAssertionES256 produces a valid assertion signature over authData ‖ H(clientData).
func signAssertionES256(t *testing.T, priv *ecdsa.PrivateKey, authData, clientData []byte) []byte {
	t.Helper()
	cd := sha256.Sum256(clientData)
	signed := append(append([]byte(nil), authData...), cd[:]...)
	h := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, h[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

// --- registration -------------------------------------------------------------

func TestVerifyRegistrationHappyPathES256(t *testing.T) {
	priv, cose := es256Cred(t)
	challenge := []byte("registration-challenge-xyz")
	credID := []byte("credential-id-1")
	aaguid := make([]byte, 16) // attestation:none -> zero aaguid

	ad := buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, attestedCredData(t, aaguid, credID, cose))
	resp := RegistrationResponse{
		ClientDataJSON:    clientDataJSON(t, typeCreate, testOrigin, challenge),
		AttestationObject: buildAttestationObject(t, ad),
	}
	got, err := testVerifier.VerifyRegistration(challenge, resp)
	if err != nil {
		t.Fatalf("VerifyRegistration: %v", err)
	}
	if string(got.CredID) != string(credID) {
		t.Errorf("credID = %q, want %q", got.CredID, credID)
	}
	if got.Alg != algES256 {
		t.Errorf("alg = %d, want ES256", got.Alg)
	}
	// The returned COSE key must reload and match the original public key.
	_, pub, _, err := parseCOSEKey(got.COSEPublicKey)
	if err != nil {
		t.Fatalf("reload cose: %v", err)
	}
	if !pub.(*ecdsa.PublicKey).Equal(&priv.PublicKey) {
		t.Error("reloaded public key does not match")
	}
}

func TestVerifyRegistrationHappyPathEd25519(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cose, err := marshalCOSEKey(algEdDSA, pub)
	if err != nil {
		t.Fatalf("marshal ed25519 cose: %v", err)
	}
	challenge := []byte("chal")
	ad := buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, attestedCredData(t, make([]byte, 16), []byte("cid"), cose))
	resp := RegistrationResponse{
		ClientDataJSON:    clientDataJSON(t, typeCreate, testOrigin, challenge),
		AttestationObject: buildAttestationObject(t, ad),
	}
	got, err := testVerifier.VerifyRegistration(challenge, resp)
	if err != nil {
		t.Fatalf("VerifyRegistration ed25519: %v", err)
	}
	if got.Alg != algEdDSA {
		t.Errorf("alg = %d, want EdDSA", got.Alg)
	}
}

func TestVerifyRegistrationRejects(t *testing.T) {
	_, cose := es256Cred(t)
	challenge := []byte("the-challenge")
	good := attestedCredData(t, make([]byte, 16), []byte("cid"), cose)

	cases := []struct {
		name string
		resp RegistrationResponse
	}{
		{"wrong challenge", RegistrationResponse{
			clientDataJSON(t, typeCreate, testOrigin, []byte("different")),
			buildAttestationObject(t, buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, good))}},
		{"wrong origin", RegistrationResponse{
			clientDataJSON(t, typeCreate, "https://evil.example", challenge),
			buildAttestationObject(t, buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, good))}},
		{"wrong type (get on create)", RegistrationResponse{
			clientDataJSON(t, typeGet, testOrigin, challenge),
			buildAttestationObject(t, buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, good))}},
		{"wrong rp id", RegistrationResponse{
			clientDataJSON(t, typeCreate, testOrigin, challenge),
			buildAttestationObject(t, buildAuthData(t, "attacker.com", flagUP|flagUV|flagAT, 0, good))}},
		{"user verification not performed", RegistrationResponse{
			clientDataJSON(t, typeCreate, testOrigin, challenge),
			buildAttestationObject(t, buildAuthData(t, testRPID, flagUP|flagAT, 0, good))}},
		{"user not present", RegistrationResponse{
			clientDataJSON(t, typeCreate, testOrigin, challenge),
			buildAttestationObject(t, buildAuthData(t, testRPID, flagUV|flagAT, 0, good))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testVerifier.VerifyRegistration(challenge, tc.resp); err == nil {
				t.Errorf("%s: expected rejection, got nil error", tc.name)
			}
		})
	}
}

func TestVerifyRegistrationRejectsAttestationFmt(t *testing.T) {
	_, cose := es256Cred(t)
	challenge := []byte("c")
	ad := buildAuthData(t, testRPID, flagUP|flagUV|flagAT, 0, attestedCredData(t, make([]byte, 16), []byte("cid"), cose))
	// fmt "packed" instead of "none" — we only accept none.
	obj, _ := cbor.Marshal(map[string]any{"fmt": "packed", "authData": ad, "attStmt": map[string]any{}})
	resp := RegistrationResponse{ClientDataJSON: clientDataJSON(t, typeCreate, testOrigin, challenge), AttestationObject: obj}
	if _, err := testVerifier.VerifyRegistration(challenge, resp); err == nil {
		t.Error("non-none attestation fmt should be rejected")
	}
}

// --- assertion ----------------------------------------------------------------

func TestVerifyAssertionHappyPath(t *testing.T) {
	priv, cose := es256Cred(t)
	challenge := []byte("assertion-challenge")
	authData := buildAuthData(t, testRPID, flagUP|flagUV, 6, nil)
	cd := clientDataJSON(t, typeGet, testOrigin, challenge)
	sig := signAssertionES256(t, priv, authData, cd)

	newCount, err := testVerifier.VerifyAssertion(challenge, StoredCredential{COSEPublicKey: cose, SignCount: 5},
		AssertionResponse{ClientDataJSON: cd, AuthenticatorData: authData, Signature: sig})
	if err != nil {
		t.Fatalf("VerifyAssertion: %v", err)
	}
	if newCount != 6 {
		t.Errorf("new sign count = %d, want 6", newCount)
	}
}

func TestVerifyAssertionSyncedPasskeyZeroCounter(t *testing.T) {
	priv, cose := es256Cred(t)
	challenge := []byte("c")
	authData := buildAuthData(t, testRPID, flagUP|flagUV, 0, nil)
	cd := clientDataJSON(t, typeGet, testOrigin, challenge)
	sig := signAssertionES256(t, priv, authData, cd)

	// stored 0, new 0 — a synced passkey with no counter must be accepted.
	if _, err := testVerifier.VerifyAssertion(challenge, StoredCredential{COSEPublicKey: cose, SignCount: 0},
		AssertionResponse{ClientDataJSON: cd, AuthenticatorData: authData, Signature: sig}); err != nil {
		t.Errorf("zero-counter synced passkey should be accepted: %v", err)
	}
}

func TestVerifyAssertionCloneDetection(t *testing.T) {
	priv, cose := es256Cred(t)
	challenge := []byte("c")
	// Authenticator reports 5, but we already stored 5 (or higher) — non-increasing
	// counter signals a possible clone.
	authData := buildAuthData(t, testRPID, flagUP|flagUV, 5, nil)
	cd := clientDataJSON(t, typeGet, testOrigin, challenge)
	sig := signAssertionES256(t, priv, authData, cd)

	if _, err := testVerifier.VerifyAssertion(challenge, StoredCredential{COSEPublicKey: cose, SignCount: 5},
		AssertionResponse{ClientDataJSON: cd, AuthenticatorData: authData, Signature: sig}); err == nil {
		t.Error("non-increasing counter should be rejected as possible clone")
	}
}

func TestVerifyAssertionRejects(t *testing.T) {
	priv, cose := es256Cred(t)
	challenge := []byte("real-challenge")
	stored := StoredCredential{COSEPublicKey: cose, SignCount: 1}

	// A valid baseline, then mutate one thing per case.
	valid := func(t *testing.T) (authData, cd, sig []byte) {
		authData = buildAuthData(t, testRPID, flagUP|flagUV, 2, nil)
		cd = clientDataJSON(t, typeGet, testOrigin, challenge)
		sig = signAssertionES256(t, priv, authData, cd)
		return
	}

	t.Run("wrong challenge", func(t *testing.T) {
		ad, _, sig := valid(t)
		cd := clientDataJSON(t, typeGet, testOrigin, []byte("attacker-challenge"))
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig}); err == nil {
			t.Error("expected rejection")
		}
	})
	t.Run("wrong origin", func(t *testing.T) {
		ad, _, sig := valid(t)
		cd := clientDataJSON(t, typeGet, "https://evil.example", challenge)
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig}); err == nil {
			t.Error("expected rejection")
		}
	})
	t.Run("uv flag cleared", func(t *testing.T) {
		ad := buildAuthData(t, testRPID, flagUP, 2, nil) // no UV
		cd := clientDataJSON(t, typeGet, testOrigin, challenge)
		sig := signAssertionES256(t, priv, ad, cd)
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig}); err == nil {
			t.Error("expected rejection when UV not set")
		}
	})
	t.Run("tampered signature", func(t *testing.T) {
		ad, cd, sig := valid(t)
		sig[len(sig)-1] ^= 0xFF
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig}); err == nil {
			t.Error("expected rejection for tampered signature")
		}
	})
	t.Run("tampered authData (rp id) invalidates signature", func(t *testing.T) {
		ad, cd, sig := valid(t)
		// Flip the RP-ID hash region; the signature no longer matches and the RP-ID
		// check also fails.
		bad := append([]byte(nil), ad...)
		bad[0] ^= 0xFF
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: bad, Signature: sig}); err == nil {
			t.Error("expected rejection for tampered authenticatorData")
		}
	})
	t.Run("signature from a different key", func(t *testing.T) {
		ad, cd, _ := valid(t)
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		badSig := signAssertionES256(t, other, ad, cd)
		if _, err := testVerifier.VerifyAssertion(challenge, stored,
			AssertionResponse{ClientDataJSON: cd, AuthenticatorData: ad, Signature: badSig}); err == nil {
			t.Error("expected rejection for signature by a non-registered key")
		}
	})
}

// Guard: the COSE round-trip preserves the key for both algorithms.
func TestCOSERoundTrip(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cose, err := marshalCOSEKey(algES256, &priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	alg, pub, rest, err := parseCOSEKey(cose)
	if err != nil {
		t.Fatal(err)
	}
	if alg != algES256 || len(rest) != 0 {
		t.Fatalf("alg=%d rest=%d", alg, len(rest))
	}
	var _ crypto.PublicKey = pub
	if !pub.(*ecdsa.PublicKey).Equal(&priv.PublicKey) {
		t.Error("ES256 round-trip mismatch")
	}
}
