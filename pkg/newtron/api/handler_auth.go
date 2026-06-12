package api

import (
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// LoginResponse is the wire shape returned by POST /auth/login on
// success (auth-design.md L2c). The key is the opaque session key
// the client carries on subsequent requests as
// `Authorization: Bearer <key>` — Bearer is the HTTP scheme, not a
// second name for the credential. ExpiresAt is absolute; using the
// key does not extend it. User echoes the PAM-verified username so
// the client knows which identity its key resolves to without
// parsing the key itself (the key is opaque).
type LoginResponse struct {
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expires_at"`
	User      string    `json:"user"`
}

// handleAuthLogin mints a new session key for the caller verified by
// PAMMiddleware. The route is mounted only when an authenticator
// (L2b) AND a session-key store (L2c) are both configured; without
// PAM there is no credential to derive the key from, and without the
// store there is nowhere to put the key.
//
// Flow:
//
//  1. Reach this handler only after PAMMiddleware accepted Basic
//     auth and attached the verified username to context. (The
//     handler chain runs withSessionKey before PAM, but a Basic-auth
//     request has no Bearer header so withSessionKey passes through.)
//  2. Read the verified username from context.
//  3. Mint a key in the store.
//  4. Return JSON with key + expiry + username.
//
// On rand failure: 500. There is no fallback that wouldn't
// compromise the security property — surface the failure honestly.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.sessionKeys == nil {
		http.Error(w, "session keys not enabled", http.StatusNotFound)
		return
	}
	user := pamUsernameForLogin(r)
	if user == "" {
		// Should not happen: PAMMiddleware rejects unauthenticated
		// requests with 401 before reaching this handler. Guard
		// rather than panic — visible 500 is the right failure mode
		// if the middleware chain ever drifts out of order.
		http.Error(w, "no authenticated identity on request", http.StatusInternalServerError)
		return
	}
	key, expiresAt, err := s.sessionKeys.Mint(user)
	if err != nil {
		http.Error(w, "mint session key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Use the {data, error} envelope every other newtron endpoint
	// uses — consumers like newtron-client unwrap to `data`, so a
	// bare-shape response collapses to nil at the client and breaks
	// downstream JQ-based extraction (newtrun's response-capture
	// against `.key`). A curl recipe reads `jq -r .data.key` —
	// consistent with the rest of the API.
	httputil.WriteJSON(w, http.StatusOK, LoginResponse{
		Key:       key,
		ExpiresAt: expiresAt,
		User:      user,
	})
}

// handleAuthLogout revokes the session key carried on the request.
// Idempotent — a Bearer that resolves to no entry still gets 204, so
// a client whose key already expired sees the same response as one
// whose key was live. Reduces information leak ("did my key exist?")
// and matches REST idempotency expectations.
//
// The route exists only when L2c is configured; otherwise 404.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.sessionKeys == nil {
		http.Error(w, "session keys not enabled", http.StatusNotFound)
		return
	}
	key, ok := bearerToken(r)
	if !ok {
		http.Error(w, "Authorization: Bearer required", http.StatusUnauthorized)
		return
	}
	s.sessionKeys.Revoke(key)
	w.WriteHeader(http.StatusNoContent)
}

// pamUsernameForLogin extracts the verified PAM username from the
// request context. Wrapped in a function so the test for /auth/login
// can fake it by attaching a username via WithPAMUsernameForTest
// without standing up a real PAM stack.
func pamUsernameForLogin(r *http.Request) string {
	return httputil.PAMUsernameFromContext(r.Context())
}
