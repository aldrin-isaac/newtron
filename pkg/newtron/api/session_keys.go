package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// DefaultSessionKeyTTL is the absolute lifetime of a session key
// minted at /auth/login when --session-key-ttl is unset
// (auth-design.md L2c). Eight hours is a typical operator-shift
// window — long enough to cover continuous use, short enough that a
// leaked key's blast radius is bounded.
const DefaultSessionKeyTTL = 8 * time.Hour

// sessionKeyStore holds in-memory mappings from opaque server-issued
// keys to the verified Unix username the original PAM authentication
// resolved (auth-design.md L2c). The store has three operations:
// Mint (post-PAM-success), Lookup (every Bearer-authenticated
// request), Revoke (logout). A background sweeper runs every minute
// to drop expired entries so the map doesn't grow without bound when
// clients never call /auth/logout.
//
// In-memory by design — a server restart invalidates every active
// session. Persistence would require treating the store as a
// credential file (the same protection class as --secret-store) and
// is not in scope; operators who need cross-restart sessions are
// using the wrong primitive and should look at PAM directly.
type sessionKeyStore struct {
	mu      sync.RWMutex
	entries map[string]sessionEntry
	ttl     time.Duration
	now     func() time.Time

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// sessionEntry is what the store remembers per key. Username is the
// PAM-verified identity at the moment of /auth/login; everything
// downstream (audit, authorization) reads from this value, not from
// the request. ExpiresAt is absolute — using the key does not extend
// it (auth-design.md L2c "TTL and rotation").
type sessionEntry struct {
	Username  string
	ExpiresAt time.Time
}

// newSessionKeyStore builds a store with the given absolute TTL and
// starts the background sweeper. The sweeper runs every minute; it
// is not exposed as a knob because the TTL granularity is already
// minute-scale or larger and a finer sweep would waste cycles.
func newSessionKeyStore(ttl time.Duration) *sessionKeyStore {
	s := &sessionKeyStore{
		entries: make(map[string]sessionEntry),
		ttl:     ttl,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.sweepLoop()
	return s
}

// Stop halts the sweeper goroutine. Idempotent: calling Stop on an
// already-stopped store is a no-op.
func (s *sessionKeyStore) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
	s.wg.Wait()
}

// Mint issues a new session key for username. The returned key is
// 256 bits of entropy URL-safe base64; ExpiresAt is now+TTL.
// crypto/rand failure is fatal at process scope — there is no
// fallback that wouldn't compromise the security property.
func (s *sessionKeyStore) Mint(username string) (key string, expiresAt time.Time, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", time.Time{}, err
	}
	key = base64.RawURLEncoding.EncodeToString(buf[:])
	expiresAt = s.now().Add(s.ttl)

	s.mu.Lock()
	s.entries[key] = sessionEntry{Username: username, ExpiresAt: expiresAt}
	s.mu.Unlock()
	return key, expiresAt, nil
}

// Lookup returns the username for key when the key exists and has
// not expired. The empty string + false signals "no valid session
// for this key" — caller maps this to 401.
//
// Lookups are read-locked; the sweeper writer-locks. Concurrent
// reads scale with mu's RLock contention, which is per-bucket in
// Go's sync.RWMutex — fine for any reasonable request rate.
func (s *sessionKeyStore) Lookup(key string) (string, bool) {
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

// Revoke removes key from the store unconditionally. Idempotent:
// revoking a key that doesn't exist is not an error — the operator's
// intent ("this key must not work") is satisfied either way.
func (s *sessionKeyStore) Revoke(key string) {
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
func (s *sessionKeyStore) sweepLoop() {
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

// sweep is the per-tick reap. Writes-lock the store; iterate; delete
// expired entries. Cost is O(N) per minute where N is the live key
// count — bounded by max concurrent users, not request rate.
func (s *sessionKeyStore) sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.entries {
		if !now.Before(e.ExpiresAt) {
			delete(s.entries, k)
		}
	}
}

// sessionKeyUsernameKey is the request-context key under which a
// session-key-verified username is attached by withSessionKey
// middleware. Unexported — callerMiddleware reads it via the
// package-level sessionKeyUsernameFromContext helper.
type sessionKeyUsernameKey struct{}

// withSessionKeyUsername attaches a session-key-verified username to
// ctx. Used only by withSessionKey middleware on a successful Bearer
// lookup; never set by handler code.
func withSessionKeyUsername(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, sessionKeyUsernameKey{}, u)
}

// sessionKeyUsernameFromContext returns the username verified by a
// session-key Bearer lookup on this request, or empty when no such
// lookup ran or the lookup failed.
func sessionKeyUsernameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	u, _ := ctx.Value(sessionKeyUsernameKey{}).(string)
	return u
}
