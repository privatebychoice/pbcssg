package webauthn

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// challengeBytes is the entropy of a ceremony challenge (256 bits). The challenge
// binds a registration/assertion response to a specific, server-issued attempt.
const challengeBytes = 32

// idBytes is the entropy of the opaque handle that correlates the two ceremony
// steps (options request → verify request) without a cookie.
const idBytes = 16

// ChallengeStore holds pending ceremony challenges in memory, each single-use and
// short-lived. It correlates the two ceremony steps by an opaque id returned to the
// client and echoed back on verify — no cookie needed for the in-flight ceremony
// (the session cookie is set only on success). Safe for concurrent use.
type ChallengeStore struct {
	mu  sync.Mutex
	m   map[string]pending
	ttl time.Duration
	now func() time.Time
}

type pending struct {
	challenge []byte
	ctx       any // opaque per-ceremony context (e.g. invite code + candidate handle); nil for login
	expires   time.Time
}

// NewChallengeStore returns a store whose challenges expire after ttl (a couple of
// minutes is typical — long enough for the user-verification gesture, short enough
// to bound replay).
func NewChallengeStore(ttl time.Duration) *ChallengeStore {
	return &ChallengeStore{m: make(map[string]pending), ttl: ttl, now: time.Now}
}

// Issue mints a new (id, challenge) pair, recording it with an opaque ceremony
// context ctx (returned unchanged by Consume; pass nil when none is needed, e.g.
// login). The id is returned to the client alongside the challenge; the challenge
// goes into the WebAuthn options.
func (s *ChallengeStore) Issue(ctx any) (id string, challenge []byte, err error) {
	idb := make([]byte, idBytes)
	if _, err := rand.Read(idb); err != nil {
		return "", nil, errors.New("webauthn: read random id")
	}
	challenge = make([]byte, challengeBytes)
	if _, err := rand.Read(challenge); err != nil {
		return "", nil, errors.New("webauthn: read random challenge")
	}
	id = base64.RawURLEncoding.EncodeToString(idb)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.m[id] = pending{challenge: challenge, ctx: ctx, expires: s.now().Add(s.ttl)}
	return id, challenge, nil
}

// Consume returns the challenge and ceremony context for id and removes it
// (single-use). ok is false if the id is unknown or expired — the caller treats both
// as a failed ceremony.
func (s *ChallengeStore) Consume(id string) (challenge []byte, ctx any, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, found := s.m[id]
	if !found {
		return nil, nil, false
	}
	delete(s.m, id) // single-use: gone whether or not it had expired
	if !s.now().Before(p.expires) {
		return nil, nil, false
	}
	return p.challenge, p.ctx, true
}

// gcLocked drops expired entries. Called under the lock on each Issue so the map
// cannot grow without bound from abandoned ceremonies.
func (s *ChallengeStore) gcLocked() {
	now := s.now()
	for id, p := range s.m {
		if !now.Before(p.expires) {
			delete(s.m, id)
		}
	}
}
