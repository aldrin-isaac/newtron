package api

import (
	"net/http"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// withSessionKey returns middleware that recognizes
// `Authorization: Bearer <key>` and, on a successful store lookup,
// attaches the verified username to the request context and signals
// downstream Basic-auth middleware to skip its challenge
// (auth-design.md L2c).
//
// Behavior matrix:
//
//   - No Authorization header / non-Bearer scheme → passthrough. The
//     request is unauthenticated from L2c's perspective; PAM Basic
//     auth (L2b) will challenge it next if configured.
//   - Bearer with empty / whitespace key → 401. The client tried to
//     use L2c but presented nothing.
//   - Bearer with a key not in the store, or expired → 401 with
//     `invalid_token`. Client should call /auth/login again.
//   - Bearer with a valid key → attach username + skip-Basic-auth
//     sentinel; passthrough.
//
// When store is nil, the middleware is a transparent passthrough —
// L2c disabled. Mirrors the PAMMiddleware nil-passthrough contract
// for the §2.4 "every layer enable/disable-able" guarantee.
func withSessionKey(store *sessionKeyStore) func(http.Handler) http.Handler {
	if store == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearerToken(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if key == "" {
				http.Error(w, "empty session key", http.StatusUnauthorized)
				return
			}
			user, found := store.Lookup(key)
			if !found {
				http.Error(w, "invalid or expired session key", http.StatusUnauthorized)
				return
			}
			ctx := withSessionKeyUsername(r.Context(), user)
			ctx = httputil.WithSkipBasicAuth(ctx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from `Authorization: Bearer <key>`.
// Returns (token, true) when the scheme is exactly "Bearer"
// case-insensitively, (empty, false) otherwise. Whitespace around the
// token is trimmed so a request with a trailing space doesn't 401.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}
