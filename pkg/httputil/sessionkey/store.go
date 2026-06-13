// Package sessionkey owns the L2c PAM-issued session-key
// infrastructure (auth-design.md §L2c) as a transport-level
// concern: an in-memory key store with a background sweeper, the
// `Authorization: Bearer` recognition middleware, and the
// /auth/login + /auth/logout HTTP handlers.
//
// The package lives under pkg/httputil/ rather than under any
// engine because authentication is a property of the server
// boundary — the outer middleware chain of the server binary that
// composes engines, e.g. cmd/newt-server — not of any individual
// engine. A server binary that embeds multiple engines mounts one
// Middleware + one login/logout pair at the outer layer; engines
// downstream read the verified username via UsernameFromContext
// without each composing their own auth state.
//
// Composition contract. The composing server binary
// (cmd/newt-server) builds:
//
//   - one *Store via NewStore — process-wide L2c state
//   - one Middleware(store) wrapper, mounted in the outer chain
//     ahead of any PAM Basic-auth middleware
//   - the LoginHandler(store) and LogoutHandler(store) endpoints,
//     mounted at /newt-server/v1/auth/login + /auth/logout
//
// Downstream readers (engine handlers, audit middleware,
// authorization gates) consume UsernameFromContext to attribute
// the request without touching the store.
//
// In-memory by design. A server restart invalidates every active
// session — there is no on-disk persistence. Persistence would
// require treating the store as a credential file (same protection
// class as --secret-store) and is out of scope; operators who need
// cross-restart sessions are using the wrong primitive and should
// look at PAM directly.
package sessionkey

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// DefaultTTL is the absolute lifetime of a session key minted at
// /auth/login when the operator has not set --session-key-ttl on
// the composing server binary. Eight hours is a typical operator-
// shift window — long enough to cover continuous use, short enough
// that a leaked key's blast radius is bounded.
const DefaultTTL = 8 * time.Hour

// Store holds in-memory mappings from opaque server-issued keys to
// the verified Unix username the original PAM authentication
// resolved (auth-design.md §L2c). Three operations: Mint
// (post-PAM-success), Lookup (every Bearer-authenticated request),
// Revoke (logout). A background sweeper runs every minute to drop
// expired entries so the map doesn't grow without bound when
// clients never call /auth/logout.
type Store struct {
	mu      sync.RWMutex
	entries map[string]keyEntry
	ttl     time.Duration
	now     func() time.Time

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// keyEntry is what the store remembers per key. Username is the
// PAM-verified identity at the moment of /auth/login; everything
// downstream (audit, authorization) reads from this value, not
// from the request. ExpiresAt is absolute — using the key does
// not extend it (auth-design.md §L2c "TTL and rotation").
type keyEntry struct {
	Username  string
	ExpiresAt time.Time
}

// NewStore builds a store with the given absolute TTL and starts
// the background sweeper. The sweeper runs every minute; it is not
// exposed as a knob because the TTL granularity is already
// minute-scale or larger and a finer sweep would waste cycles.
func NewStore(ttl time.Duration) *Store {
	s := &Store{
		entries: make(map[string]keyEntry),
		ttl:     ttl,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.sweepLoop()
	return s
}

// Stop halts the sweeper goroutine. Idempotent: calling Stop on
// an already-stopped store is a no-op.
func (s *Store) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
	s.wg.Wait()
}

// Mint draws 256 bits from crypto/rand for the key. A rand.Read
// failure surfaces to the caller rather than panicking or falling
// back to a weaker source — there is no fallback that wouldn't
// compromise the security property.
func (s *Store) Mint(username string) (key string, expiresAt time.Time, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", time.Time{}, err
	}
	key = base64.RawURLEncoding.EncodeToString(buf[:])
	expiresAt = s.now().Add(s.ttl)

	s.mu.Lock()
	s.entries[key] = keyEntry{Username: username, ExpiresAt: expiresAt}
	s.mu.Unlock()
	return key, expiresAt, nil
}

// Lookup also enforces the TTL — an entry past ExpiresAt returns
// false even when the sweeper hasn't reaped it yet, so a slow
// sweeper cannot enable expired keys. Read-locked; the sweeper
// writer-locks.
func (s *Store) Lookup(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if !s.now().Before(entry.ExpiresAt) {
		return "", false
	}
	return entry.Username, true
}

// Revoke is idempotent. A revoke for a key that doesn't exist
// still satisfies the operator's intent ("this key must not
// work"), so the /auth/logout handler can return 204 without
// information leak about which keys were live.
func (s *Store) Revoke(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
}

// sweepLoop drops expired entries every minute. Decouples store
// growth from client behavior — a client that never calls /logout
// still has its key reaped within a minute of TTL expiry.
func (s *Store) sweepLoop() {
	defer s.wg.Done()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.sweep()
		}
	}
}

// sweep is the per-tick reap. Writes-lock the store; iterate;
// delete expired entries. Cost is O(N) per minute where N is the
// live key count — bounded by max concurrent users, not request
// rate.
func (s *Store) sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, entry := range s.entries {
		if !now.Before(entry.ExpiresAt) {
			delete(s.entries, k)
		}
	}
}
