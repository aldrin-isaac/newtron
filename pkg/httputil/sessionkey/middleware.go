package sessionkey

import (
	"context"
	"net/http"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// Middleware returns an http.Handler wrapper that recognizes
// `Authorization: Bearer <key>` and, on a successful store lookup,
// attaches the verified username to the request context (readable
// via UsernameFromContext) and signals downstream Basic-auth
// middleware to skip its challenge via httputil.WithSkipBasicAuth
// (auth-design.md §L2c).
//
// Behavior matrix:
//
//   - No Authorization header / non-Bearer scheme → passthrough.
//     The request is unauthenticated from L2c's perspective; PAM
//     Basic auth (L2b) will challenge it next if configured.
//   - Bearer with empty / whitespace key → 401. The client tried
//     to use L2c but presented nothing.
//   - Bearer with a key not in the store, or expired → 401. Client
//     should call /auth/login again.
//   - Bearer with a valid key → attach username + skip-Basic-auth
//     sentinel; passthrough.
//
// When store is nil the middleware is a transparent passthrough —
// L2c disabled. Mirrors the PAMMiddleware nil-passthrough contract
// for the auth-design.md §L2c "every layer enable/disable-able"
// guarantee.
func Middleware(store *Store) func(http.Handler) http.Handler {
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
			ctx := withUsername(r.Context(), user)
			ctx = httputil.WithSkipBasicAuth(ctx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from `Authorization: Bearer <key>`.
// Returns (token, true) when the scheme is exactly "Bearer"
// case-insensitively, (empty, false) otherwise. Whitespace around
// the token is trimmed so a request with a trailing space doesn't
// 401.
//
// Package-private. Reused by handlers.go's LogoutHandler for the
// same Bearer-header parse. Not promoted to httputil because every
// consumer of the session-key Bearer is inside this package — a
// cmd/ that mounts only Middleware uses bearerToken transitively
// through Middleware's own call.
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

// sessionUsernameKey is the request-context key under which a
// session-key-verified username is attached by Middleware.
// Unexported — UsernameFromContext is the only public reader; no
// downstream code should peek at the raw key.
type sessionUsernameKey struct{}

// withUsername attaches a session-key-verified username to ctx.
// Used only by Middleware on a successful Bearer lookup; never
// set by handler code.
func withUsername(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, sessionUsernameKey{}, u)
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
	u, _ := ctx.Value(sessionUsernameKey{}).(string)
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
