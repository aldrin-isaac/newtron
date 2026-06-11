package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// TestHandleAuthLogin_Success pins the happy path: a request whose
// context carries a PAM-verified username (as PAMMiddleware would
// have attached) gets back a LoginResponse with a non-empty key,
// the correct user, and a future expiry.
func TestHandleAuthLogin_Success(t *testing.T) {
	s := &Server{sessionKeys: newSessionKeyStore(time.Hour)}
	defer s.sessionKeys.Stop()

	req := httptest.NewRequest("POST", "/newtron/v1/auth/login", nil)
	req = req.WithContext(httputil.WithPAMUsernameForTest(req.Context(), "alice"))
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.User != "alice" {
		t.Errorf("user = %q, want alice", resp.User)
	}
	if resp.Key == "" {
		t.Error("key is empty")
	}
	if !resp.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt %v is not in the future", resp.ExpiresAt)
	}
	// Sanity: the minted key must be looked up via the store.
	if u, ok := s.sessionKeys.Lookup(resp.Key); !ok || u != "alice" {
		t.Errorf("Lookup(minted key) = (%q, %v), want (alice, true)", u, ok)
	}
}

// TestHandleAuthLogin_NoPAMContext pins that if the handler is
// reached without an upstream-verified PAM username, it returns 500.
// In production PAMMiddleware would 401 first; this test simulates a
// misconfigured middleware chain.
func TestHandleAuthLogin_NoPAMContext(t *testing.T) {
	s := &Server{sessionKeys: newSessionKeyStore(time.Hour)}
	defer s.sessionKeys.Stop()

	req := httptest.NewRequest("POST", "/newtron/v1/auth/login", nil)
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", w.Code)
	}
}

// TestHandleAuthLogin_StoreDisabled pins that the handler 404s when
// L2c is disabled (no session-key store). Matches the route-mounted-
// unconditionally / handler-decides contract.
func TestHandleAuthLogin_StoreDisabled(t *testing.T) {
	s := &Server{sessionKeys: nil}

	req := httptest.NewRequest("POST", "/newtron/v1/auth/login", nil)
	req = req.WithContext(httputil.WithPAMUsernameForTest(req.Context(), "alice"))
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 (L2c disabled)", w.Code)
	}
}

// TestHandleAuthLogout_Success pins that POSTing a valid Bearer to
// /auth/logout returns 204 and the key is gone from the store.
func TestHandleAuthLogout_Success(t *testing.T) {
	s := &Server{sessionKeys: newSessionKeyStore(time.Hour)}
	defer s.sessionKeys.Stop()
	key, _, err := s.sessionKeys.Mint("alice")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	req := httptest.NewRequest("POST", "/newtron/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()

	s.handleAuthLogout(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204", w.Code)
	}
	if _, ok := s.sessionKeys.Lookup(key); ok {
		t.Error("key still in store after /logout")
	}
}

// TestHandleAuthLogout_Idempotent pins that calling /logout twice
// (or with an unknown key) returns 204 both times. The operator's
// intent ("this key must not work") is satisfied either way.
func TestHandleAuthLogout_Idempotent(t *testing.T) {
	s := &Server{sessionKeys: newSessionKeyStore(time.Hour)}
	defer s.sessionKeys.Stop()

	req := httptest.NewRequest("POST", "/newtron/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer never-existed")
	w := httptest.NewRecorder()

	s.handleAuthLogout(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204 (idempotent)", w.Code)
	}
}

// TestHandleAuthLogout_MissingBearer pins that /logout without an
// Authorization header returns 401. We can't revoke a key we
// weren't told about.
func TestHandleAuthLogout_MissingBearer(t *testing.T) {
	s := &Server{sessionKeys: newSessionKeyStore(time.Hour)}
	defer s.sessionKeys.Stop()

	req := httptest.NewRequest("POST", "/newtron/v1/auth/logout", nil)
	w := httptest.NewRecorder()

	s.handleAuthLogout(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

// TestHandleAuthLogout_StoreDisabled pins the L2c-disabled 404.
func TestHandleAuthLogout_StoreDisabled(t *testing.T) {
	s := &Server{sessionKeys: nil}

	req := httptest.NewRequest("POST", "/newtron/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer some-key")
	w := httptest.NewRecorder()

	s.handleAuthLogout(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 (L2c disabled)", w.Code)
	}
}
