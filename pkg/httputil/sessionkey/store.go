// Package sessionkey owns the L2c session-key infrastructure
// (auth-design.md §L2c) as a transport-level concern: in-memory
// store, /auth/login + /auth/logout HTTP handlers, and the Bearer-
// recognition middleware. The package lives under pkg/httputil
// rather than under pkg/newtron/api because authentication is a
// property of the server boundary (cmd/newt-server's outer
// middleware), not of any individual engine.
//
// DPN §27 (single owner): this package is the sole owner of every
// session-key data object — the per-process key→username map, the
// background sweeper, the context-key under which the verified
// username travels downstream. No engine package owns auth state.
//
// DPN §28 (file-level cohesion): all session-key code lives in this
// package — store.go (lifecycle + key resolution), middleware.go
// (Bearer header handling), handlers.go (HTTP routes). Consumers
// import the package as a unit.
//
// DPN §40 (greenfield): callers that previously lived inside
// pkg/newtron/api/{session_keys,session_key_middleware,handler_auth}
// are migrated in one commit; the old files are deleted in the
// same change. No compat shim.
package sessionkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// DefaultTTL is the absolute lifetime of a session key minted at
// /auth/login when the operator has not set --session-key-ttl on
// the binding cmd binary. Eight hours is a typical operator-shift
// window — long enough to cover continuous use, short enough that
// a leaked key's blast radius is bounded.
const DefaultTTL = 8 * time.Hour

// Store holds in-memory mappings from opaque server-issued keys to
// the verified Unix username the original PAM authentication
// resolved (auth-design.md §L2c). Three operations: Mint
// (post-PAM-success), Lookup (every Bearer-authenticated request),
// Revoke (logout). A background sweeper runs every minute to drop
// expired entries so the map doesn't grow without bound when
// clients never call /auth/logout.
//
// In-memory by design — a server restart invalidates every active
// session. Persistence would require treating the store as a
// credential file (the same protection class as --secret-store)
// and is not in scope; operators who need cross-restart sessions
// are using the wrong primitive and should look at PAM directly.
type Store struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
	now     func() time.Time

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// entry is what the store remembers per key. Username is the
// PAM-verified identity at the moment of /auth/login; everything
// downstream (audit, authorization) reads from this value, not
// from the request. ExpiresAt is absolute — using the key does
// not extend it (auth-design.md §L2c "TTL and rotation").
type entry struct {
	Username  string
	ExpiresAt time.Time
}

// NewStore builds a store with the given absolute TTL and starts
// the background sweeper. The sweeper runs every minute; it is not
// exposed as a knob because the TTL granularity is already
// minute-scale or larger and a finer sweep would waste cycles.
func NewStore(ttl time.Duration) *Store {
	s := &Store{
		entries: make(map[string]entry),
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
	s.entries[key] = entry{Username: username, ExpiresAt: expiresAt}
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
	e, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if !s.now().Before(e.ExpiresAt) {
		return "", false
	}
	return e.Username, true
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
	for k, e := range s.entries {
		if !now.Before(e.ExpiresAt) {
			delete(s.entries, k)
		}
	}
}

// usernameContextKey is the request-context key under which a
// session-key-verified username is attached by Middleware.
// Unexported — UsernameFromContext is the only public reader; no
// downstream code should peek at the raw key.
type usernameContextKey struct{}

// withUsername attaches a session-key-verified username to ctx.
// Used only by Middleware on a successful Bearer lookup; never
// set by handler code.
func withUsername(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, usernameContextKey{}, u)
}

// UsernameFromContext returns the username verified by a session-
// key Bearer lookup on this request, or empty when no such lookup
// ran or the lookup failed. Read by downstream caller-extraction
// middleware (e.g. newtron's callerMiddleware) to populate the
// audit.Caller with verification_source=session_key.
func UsernameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	u, _ := ctx.Value(usernameContextKey{}).(string)
	return u
}

// WithUsernameForTest attaches u to ctx using the same internal
// key UsernameFromContext reads. Test-only: production code
// receives the verified session-key username through Middleware's
// successful Lookup path. Exposed so tests in sibling packages
// (e.g. newtron's callerMiddleware tests) can simulate "the
// session-key middleware resolved this Bearer to this username"
// without standing up a store.
func WithUsernameForTest(ctx context.Context, u string) context.Context {
	return withUsername(ctx, u)
}
