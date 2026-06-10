package httputil

import (
	"context"
	"net/http"
)

// Authenticator is the contract every user-authentication backend
// satisfies for the L2b user-to-service path (auth-design.md L2b).
// One implementation ships in-tree: PAMAuthenticator in pam.go,
// which calls into libpam via cgo. The interface exists so
// middleware tests in this package and elsewhere can mock the
// backend without standing up a real PAM stack (which would require
// libpam0g-dev at test build time and a configured PAM service).
//
// Username is whatever the operator's identity backend says is the
// authoritative name for the caller. For pam_unix that's the local
// UNIX login; for pam_ldap / pam_sss / pam_krb5 it's whatever the
// configured directory returns. The audit log records this value
// verbatim under VerificationPAM — the L2b audit criterion ("can a
// reviewer answer 'who called this?'") is met when the recorded
// name matches what the operator's identity store knows.
//
// Authenticate returns nil on a successful authentication + account
// check; any non-nil error means "reject the request with 401."
type Authenticator interface {
	Authenticate(username, password string) error
}

// pamUsernameKey is the request-context key under which a verified
// PAM username is stored by PAMMiddleware. Unexported so the only
// valid setter is the middleware (production) or
// WithPAMUsernameForTest (test mocks).
type pamUsernameKey struct{}

// PAMUsernameFromContext returns the username verified by
// PAMMiddleware, or empty when no PAM verification ran for this
// request (either the L2b middleware was disabled, or the request
// arrived via Unix socket / mTLS where PAM is skipped because a
// higher-priority verified identity is already present).
//
// Read by callers' identity-extraction middleware (e.g., the newtron
// caller_middleware) to populate audit.Caller with VerificationPAM.
func PAMUsernameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	u, _ := ctx.Value(pamUsernameKey{}).(string)
	return u
}

// WithPAMUsernameForTest attaches u to ctx using the same internal
// key PAMUsernameFromContext reads. Test-only: production code
// receives the verified PAM username through PAMMiddleware's
// successful Authenticate path. Exposed so middleware tests in
// sibling packages can simulate "PAM verified this username"
// without standing up a real PAM stack or libpam dependency.
func WithPAMUsernameForTest(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, pamUsernameKey{}, u)
}

// PAMMiddleware enforces user-to-service authentication on TCP
// requests (auth-design.md L2b). The middleware:
//
//  1. Skips authentication when an already-verified identity source
//     is present (Unix-socket peer creds — L1; mTLS peer cert CN —
//     L2a). Those paths are kernel-attested or CA-verified
//     respectively; demanding HTTP Basic auth on top would be
//     friction without security benefit.
//  2. Requires HTTP Basic credentials on otherwise-unauthenticated
//     requests. Missing or malformed Authorization header → 401
//     with WWW-Authenticate: Basic realm="newtron".
//  3. Calls auth.Authenticate(username, password). On success the
//     verified username is attached to the request context via
//     pamUsernameKey for the downstream identity-extraction
//     middleware to read. On failure → 401 (without a
//     WWW-Authenticate header — repeat-401 in the same handshake
//     would be a guess attempt and the client should stop).
//
// When auth is nil the middleware is a transparent passthrough —
// the L2b disabled state. This is the default behavior preserved
// for any deployment that doesn't configure --auth-pam-service.
func PAMMiddleware(auth Authenticator) func(http.Handler) http.Handler {
	if auth == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip when a higher-priority verified identity is
			// already on this request. PAM is the TCP fallback,
			// not a universal gate.
			if PeerCredFromContext(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}
			if ServiceCertCNFromRequest(r) != "" {
				next.ServeHTTP(w, r)
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="newtron"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			if err := auth.Authenticate(user, pass); err != nil {
				http.Error(w, "authentication failed", http.StatusUnauthorized)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), pamUsernameKey{}, user))
			next.ServeHTTP(w, r)
		})
	}
}
