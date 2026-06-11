package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// TestWithSessionKey_NilStorePassthrough pins the L2c-disabled
// contract: when no store is configured the middleware is a
// transparent passthrough that doesn't touch the request.
func TestWithSessionKey_NilStorePassthrough(t *testing.T) {
	called := false
	h := withSessionKey(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if httputil.SkipBasicAuthFromContext(r.Context()) {
			t.Error("nil-store passthrough must not set skip-Basic-auth")
		}
		if u := sessionKeyUsernameFromContext(r.Context()); u != "" {
			t.Errorf("nil-store passthrough must not set username, got %q", u)
		}
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer some-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler was not invoked")
	}
}

// TestWithSessionKey_NoAuthHeaderPassthrough pins that a request
// without any Authorization header passes through cleanly — that's
// the PAM-Basic-auth path which the next middleware handles.
func TestWithSessionKey_NoAuthHeaderPassthrough(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()

	called := false
	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if httputil.SkipBasicAuthFromContext(r.Context()) {
			t.Error("no-Authorization passthrough must not set skip-Basic-auth")
		}
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler was not invoked")
	}
}

// TestWithSessionKey_BasicAuthPassthrough pins that an Authorization
// header with a non-Bearer scheme (e.g. Basic) passes through to
// the next middleware untouched. L2c only triggers on Bearer.
func TestWithSessionKey_BasicAuthPassthrough(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()

	called := false
	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic YWxpY2U6cHc=")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("Basic-auth request did not reach downstream handler")
	}
	if w.Code != 200 {
		t.Errorf("Basic-auth request got status %d, want 200 (passthrough)", w.Code)
	}
}

// TestWithSessionKey_ValidBearer pins the happy L2c path: a valid
// Bearer token attaches the verified username AND signals PAM to
// skip its challenge.
func TestWithSessionKey_ValidBearer(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()
	key, _, err := store.Mint("alice")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	called := false
	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if u := sessionKeyUsernameFromContext(r.Context()); u != "alice" {
			t.Errorf("username on context = %q, want alice", u)
		}
		if !httputil.SkipBasicAuthFromContext(r.Context()) {
			t.Error("skip-Basic-auth was not set after valid Bearer")
		}
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler was not invoked after valid Bearer")
	}
	if w.Code != 200 {
		t.Errorf("valid Bearer got status %d, want 200", w.Code)
	}
}

// TestWithSessionKey_InvalidBearer pins that an unknown key 401s
// before the downstream handler runs. This is L2c rejecting; PAM
// would never see the request.
func TestWithSessionKey_InvalidBearer(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()

	called := false
	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer never-issued")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if called {
		t.Error("downstream handler ran despite invalid Bearer")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid Bearer got status %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid") {
		t.Errorf("expected body to mention 'invalid'; got %q", w.Body.String())
	}
}

// TestWithSessionKey_ExpiredBearer pins that an expired key 401s
// even if the sweeper hasn't run yet. The Lookup-time check ensures
// the TTL is enforced on every request.
func TestWithSessionKey_ExpiredBearer(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()
	base := time.Now()
	store.now = func() time.Time { return base }
	key, _, err := store.Mint("alice")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Advance past expiry.
	store.now = func() time.Time { return base.Add(2 * time.Hour) }

	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler ran for expired Bearer")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired Bearer got status %d, want 401", w.Code)
	}
}

// TestWithSessionKey_EmptyBearer pins that `Authorization: Bearer ` (no key)
// is rejected with 401. A client that tried to use L2c but presented
// nothing must not silently fall through to PAM Basic auth.
func TestWithSessionKey_EmptyBearer(t *testing.T) {
	store := newSessionKeyStore(time.Hour)
	defer store.Stop()

	h := withSessionKey(store)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler ran for empty Bearer")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer   ")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty Bearer got status %d, want 401", w.Code)
	}
}

// TestBearerToken_SchemeMatching pins case-insensitive scheme
// matching and whitespace trimming around the token value.
func TestBearerToken_SchemeMatching(t *testing.T) {
	cases := []struct {
		header string
		token  string
		ok     bool
	}{
		{"", "", false},
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc  ", "abc", true},
		{"Basic abc", "", false},
		{"Token abc", "", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		tok, ok := bearerToken(req)
		if ok != tc.ok {
			t.Errorf("bearerToken(%q) ok = %v, want %v", tc.header, ok, tc.ok)
		}
		if tok != tc.token {
			t.Errorf("bearerToken(%q) token = %q, want %q", tc.header, tok, tc.token)
		}
	}
}
