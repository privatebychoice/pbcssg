package webauthn

import (
	"crypto"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Authenticator-data flag bits (WebAuthn §6.1).
const (
	flagUP = 0x01 // User Present
	flagUV = 0x04 // User Verified
	flagAT = 0x40 // Attested credential data included
	flagED = 0x80 // Extension data included
)

// Ceremony types carried in clientDataJSON.
const (
	typeCreate = "webauthn.create"
	typeGet    = "webauthn.get"
)

// collectedClientData is the subset of clientDataJSON we validate. Challenge is
// base64url (no padding) per the WebAuthn encoding.
type collectedClientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

// verifyClientData parses clientDataJSON and checks the three things that bind a
// ceremony to this server and this attempt: the ceremony type, the challenge
// (compared in constant time), and the origin. A mismatch is a hard failure — this
// is the anti-phishing / anti-replay core.
func verifyClientData(clientDataJSON []byte, wantType, wantOrigin string, wantChallenge []byte) error {
	var cd collectedClientData
	if err := json.Unmarshal(clientDataJSON, &cd); err != nil {
		return fmt.Errorf("webauthn: parse clientDataJSON: %w", err)
	}
	if cd.Type != wantType {
		return fmt.Errorf("webauthn: clientData type %q, want %q", cd.Type, wantType)
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil {
		return fmt.Errorf("webauthn: decode clientData challenge: %w", err)
	}
	if subtle.ConstantTimeCompare(got, wantChallenge) != 1 {
		return errors.New("webauthn: challenge mismatch")
	}
	if cd.Origin != wantOrigin {
		return fmt.Errorf("webauthn: origin %q, want %q", cd.Origin, wantOrigin)
	}
	return nil
}

// authData is a parsed authenticatorData structure. attestedCredentialData is
// present only when the AT flag is set (i.e. on registration).
type authData struct {
	rpIDHash  []byte // 32 bytes
	flags     byte
	signCount uint32

	// Present only when flagAT is set:
	aaguid     []byte // 16 bytes
	credID     []byte
	credAlg    int
	credKey    crypto.PublicKey
	credKeyDER []byte // COSE re-encoding of credKey, for storage
}

func (a authData) userPresent() bool     { return a.flags&flagUP != 0 }
func (a authData) userVerified() bool    { return a.flags&flagUV != 0 }
func (a authData) hasAttestedCred() bool { return a.flags&flagAT != 0 }

// parseAuthData parses authenticatorData. The fixed header is 37 bytes
// (rpIdHash[32] ‖ flags[1] ‖ signCount[4]); if the AT flag is set, attested
// credential data (aaguid[16] ‖ credIdLen[2] ‖ credId ‖ COSE key) follows.
func parseAuthData(b []byte) (authData, error) {
	if len(b) < 37 {
		return authData{}, fmt.Errorf("webauthn: authenticatorData too short: %d bytes", len(b))
	}
	a := authData{
		rpIDHash:  b[0:32],
		flags:     b[32],
		signCount: binary.BigEndian.Uint32(b[33:37]),
	}
	if !a.hasAttestedCred() {
		return a, nil
	}
	rest := b[37:]
	if len(rest) < 18 {
		return authData{}, errors.New("webauthn: attested credential data truncated")
	}
	a.aaguid = rest[0:16]
	credIDLen := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if credIDLen == 0 || credIDLen > len(rest) {
		return authData{}, fmt.Errorf("webauthn: invalid credential id length %d", credIDLen)
	}
	a.credID = rest[:credIDLen]
	rest = rest[credIDLen:]

	alg, pub, _, err := parseCOSEKey(rest)
	if err != nil {
		return authData{}, err
	}
	a.credAlg = alg
	a.credKey = pub
	der, err := marshalCOSEKey(alg, pub)
	if err != nil {
		return authData{}, err
	}
	a.credKeyDER = der
	return a, nil
}
