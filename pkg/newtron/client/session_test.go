package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeNewtron is a minimal HTTP stand-in that mimics newtron's
// auth-related behaviors: POST /auth/login returns a session key
// when Basic auth is presented (otherwise 401); other paths require
// Authorization: Bearer <key> and return 401 with a Body otherwise.
// Test harness records request counts so callers can assert refresh
// behavior.
type fakeNewtron struct {
	mu          sync.Mutex
	logins      int
	calls       int
	validKey    string
	invalidate  bool   // when true, return 401 even with the valid key (forcing a refresh)
	keyToIssue  string // the next key /auth/login will mint
}

func (f *fakeNewtron) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			user, pass, ok := r.BasicAuth()
			if !ok || user == "" || pass == "" {
				w.WriteHeader(401)
				return
			}
			f.logins++
			f.validKey = f.keyToIssue
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"data":{"key":"`+f.validKey+`","expires_at":"2026-12-31T00:00:00Z","user":"`+user+`"},"error":""}`)
			return
		}

		// Every other endpoint demands Bearer.
		f.calls++
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, "Bearer ") {
			w.WriteHeader(401)
			return
		}
		key := strings.TrimPrefix(got, "Bearer ")
		if f.invalidate || key != f.validKey {
			f.invalidate = false // after one rejection, stop forcing
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"data":{"ok":true}}`)
	})
}

// TestWithSession_LazyLoginOnFirstCall pins that the session
// authentication doesn't fire until the first non-auth request.
// Constructing a client must not call /auth/login.
func TestWithSession_LazyLoginOnFirstCall(t *testing.T) {
	f := &fakeNewtron{keyToIssue: "key-1"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))

	if got := f.logins; got != 0 {
		t.Fatalf("login fired during construction: %d", got)
	}

	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("RawRequest: %v", err)
	}
	if got := f.logins; got != 1 {
		t.Errorf("logins after first call = %d, want 1", got)
	}
	if got := f.calls; got != 1 {
		t.Errorf("non-auth calls = %d, want 1", got)
	}
}

// TestWithSession_KeyCachedAcrossCalls pins that subsequent calls
// reuse the cached key without re-logging in. The login surface is
// rare-call by design.
func TestWithSession_KeyCachedAcrossCalls(t *testing.T) {
	f := &fakeNewtron{keyToIssue: "key-1"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))
	for i := 0; i < 5; i++ {
		if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := f.logins; got != 1 {
		t.Errorf("logins after 5 calls = %d, want 1 (cache)", got)
	}
}

// TestWithSession_RefreshOn401 pins the on-expiry refresh behavior:
// when the server rejects the cached key with 401, the round-tripper
// re-logs in and retries. The caller sees the successful response.
func TestWithSession_RefreshOn401(t *testing.T) {
	f := &fakeNewtron{keyToIssue: "key-1"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))

	// First call: lazy login → key-1.
	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("call 1: %v", err)
	}

	// Force the server to reject the next request and have login
	// mint a different key. The round-tripper should detect the
	// 401, re-login, and retry transparently.
	f.mu.Lock()
	f.invalidate = true
	f.keyToIssue = "key-2"
	f.mu.Unlock()

	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("call 2 (after forced 401): %v", err)
	}

	if got := f.logins; got != 2 {
		t.Errorf("logins = %d, want 2 (initial + refresh)", got)
	}
}

// TestWithSession_NoInfiniteLoopOnPersistent401 pins that a
// persistently-401ing server (e.g., wrong credentials, server-side
// account disabled) does NOT cause the round-tripper to loop. The
// caller gets a 401 response, not a hang.
func TestWithSession_NoInfiniteLoopOnPersistent401(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			// Always issue a key so refresh "succeeds" from the
			// round-tripper's POV.
			_, _ = io.WriteString(w, `{"data":{"key":"x","expires_at":"2026-12-31T00:00:00Z","user":"y"},"error":""}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))
	_, _ = c.RawRequest("GET", "/newtron/v1/networks", nil)

	// Two non-auth attempts (initial + retry) + two logins
	// (initial + refresh). If the round-tripper looped infinitely
	// we'd have far more.
	if got := atomic.LoadInt32(&reqCount); got > 4 {
		t.Errorf("request count = %d, want at most 4 (no infinite loop)", got)
	}
}

// TestWithSession_AuthLoginNotIntercepted pins that the round-
// tripper bypasses itself on POSTs to /auth/login — the login wire
// call carries Basic auth and must NOT have its Authorization
// header overwritten by the cached Bearer.
func TestWithSession_AuthLoginNotIntercepted(t *testing.T) {
	// Capture the auth header the server sees on /auth/login.
	var (
		mu             sync.Mutex
		loginAuthHdr   string
		gotLoginCall   bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			mu.Lock()
			loginAuthHdr = r.Header.Get("Authorization")
			gotLoginCall = true
			mu.Unlock()
			_, _ = io.WriteString(w, `{"data":{"key":"k","expires_at":"2026-12-31T00:00:00Z","user":"u"},"error":""}`)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"data":null}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))
	// Force the lazy login by making a non-auth call first.
	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("priming call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotLoginCall {
		t.Fatal("server never saw /auth/login")
	}
	if !strings.HasPrefix(loginAuthHdr, "Basic ") {
		t.Errorf("login Authorization = %q, want Basic …", loginAuthHdr)
	}
}

// TestSessionAuth_RefreshConcurrent pins that concurrent ensureKey
// callers do not each fire their own login wire-call — the mutex
// serializes the refresh so the server sees one wire-call per
// refresh event regardless of caller fan-in.
func TestSessionAuth_RefreshConcurrent(t *testing.T) {
	f := &fakeNewtron{keyToIssue: "key-1"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(srv.URL, "net-1", WithSession("alice", "pw"))
	ctx := context.Background()
	_ = ctx

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.RawRequest("GET", "/newtron/v1/networks", nil)
		}()
	}
	wg.Wait()

	if got := f.logins; got != 1 {
		t.Errorf("logins after 8 concurrent calls = %d, want 1", got)
	}
}
