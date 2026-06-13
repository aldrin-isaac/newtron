package sessionkey

import (
	"testing"
	"time"
)

// TestStore_MintLookupRevoke pins the happy path: a minted
// key is found by Lookup, a revoked key isn't (auth-design.md L2c).
func TestStore_MintLookupRevoke(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	key, expiresAt, err := s.Mint("alice")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if key == "" {
		t.Fatal("Mint returned empty key")
	}
	if expiresAt.Before(time.Now()) {
		t.Fatalf("Mint returned past expiry: %v", expiresAt)
	}

	user, ok := s.Lookup(key)
	if !ok {
		t.Fatal("Lookup of minted key returned !ok")
	}
	if user != "alice" {
		t.Errorf("Lookup user = %q, want alice", user)
	}

	s.Revoke(key)
	if _, ok := s.Lookup(key); ok {
		t.Error("Lookup after Revoke returned ok; expected the key to be gone")
	}
}

// TestStore_LookupExpired pins that an expired key fails
// Lookup even when the sweeper hasn't run yet. The TTL property is
// enforced at Lookup time, not just at sweep time — a slow sweeper
// must not enable expired keys.
func TestStore_LookupExpired(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	// Pin "now" so we can manipulate clock without sleeping.
	base := time.Now()
	s.now = func() time.Time { return base }

	key, _, err := s.Mint("bob")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// Advance virtual time past expiry.
	s.now = func() time.Time { return base.Add(2 * time.Hour) }

	if _, ok := s.Lookup(key); ok {
		t.Error("Lookup of expired key returned ok; expected false")
	}
}

// TestStore_LookupUnknownKey pins that a key that was
// never minted fails Lookup (401-able).
func TestStore_LookupUnknownKey(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	if _, ok := s.Lookup("never-minted"); ok {
		t.Error("Lookup of never-minted key returned ok")
	}
	if _, ok := s.Lookup(""); ok {
		t.Error("Lookup of empty key returned ok")
	}
}

// TestStore_RevokeIdempotent pins that revoking a key
// twice (or revoking a never-minted key) is not an error. Matches
// the /auth/logout idempotency property.
func TestStore_RevokeIdempotent(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	key, _, err := s.Mint("alice")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	s.Revoke(key)
	s.Revoke(key)       // second revoke
	s.Revoke("unknown") // never-minted
	s.Revoke("")        // empty
	// Reaches here without panicking → pass.
}

// TestStore_Sweep pins that the sweeper drops expired
// entries. Tested by calling sweep directly with manipulated now()
// so the test doesn't have to wait for the minute ticker.
func TestStore_Sweep(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	base := time.Now()
	s.now = func() time.Time { return base }

	keyAlive, _, err := s.Mint("alice")
	if err != nil {
		t.Fatalf("Mint alice: %v", err)
	}

	// Mint a second key that we'll let expire.
	s.now = func() time.Time { return base.Add(30 * time.Minute) }
	keyDying, _, err := s.Mint("bob")
	if err != nil {
		t.Fatalf("Mint bob: %v", err)
	}

	// Advance past alice's expiry but before bob's.
	s.now = func() time.Time { return base.Add(75 * time.Minute) }
	s.sweep()

	if _, ok := s.Lookup(keyAlive); ok {
		t.Error("alice's key should have been swept")
	}
	if _, ok := s.Lookup(keyDying); !ok {
		t.Error("bob's key should still be alive")
	}
}

// TestStore_MintIsUnique pins that two Mints for the same
// user produce different keys — keys are random 256-bit values, not
// derived from the username.
func TestStore_MintIsUnique(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()

	k1, _, err := s.Mint("alice")
	if err != nil {
		t.Fatalf("Mint 1: %v", err)
	}
	k2, _, err := s.Mint("alice")
	if err != nil {
		t.Fatalf("Mint 2: %v", err)
	}
	if k1 == k2 {
		t.Error("two Mints for the same user produced identical keys")
	}
}

// TestStore_StopIdempotent pins that Stop can be called
// more than once without panicking. The Server's OnShutdown calls
// Stop; tests may also call Stop in their cleanup; the two must not
// collide.
func TestStore_StopIdempotent(t *testing.T) {
	s := NewStore(time.Hour)
	s.Stop()
	s.Stop() // second Stop must be safe
}
