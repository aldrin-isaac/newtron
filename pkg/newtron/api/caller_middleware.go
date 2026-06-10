package api

import (
	"net/http"
	"os/user"
	"strconv"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// callerMiddleware builds an http.Handler that populates the
// request context with a *audit.Caller (auth-design.md L1). Two
// surfaces are handled:
//
//   - Unix-socket connections: PeerCred is on the context (set by
//     httputil.Server.connContext); the middleware resolves UID to
//     username via os/user.LookupId and tags the source as
//     VerificationUnixPeerCreds.
//   - TCP connections with a configured header: when headerName is
//     non-empty, the header value (trimmed) becomes the username
//     tagged as VerificationSelfAttestedHeader.
//
// Either yielding nothing is OK: the next handler runs with no
// Caller on context and the audit middleware records the event with
// User="" and VerificationUnknown — visible to a reviewer as
// "no identity attached."
//
// Configuration:
//
//   - When headerName is "", header-based identity is disabled — the
//     Unix-socket path still works because PeerCred extraction is
//     wired by httputil.
//   - When no Unix socket is configured at the Server level, PeerCred
//     never appears on the context; this middleware then only sees
//     the TCP path and emits self-attested or no-caller events.
//
// The middleware is always installed; the runtime behavior is
// dictated by configuration. This matches the "every layer is
// enable/disable-able" contract from auth-design.md §2.4 — operators
// adopt the verified Unix-socket path or the self-attested header
// path (or both, or neither) without recompiling.
func callerMiddleware(headerName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := resolveCaller(r, headerName)
			if caller != nil {
				r = r.WithContext(audit.WithCaller(r.Context(), caller))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveCaller returns the *audit.Caller this request should carry,
// or nil when no identity is available. Unix peer creds win over the
// TCP header — a request arriving on the Unix listener is
// kernel-verified and the header (which the client set) is ignored.
func resolveCaller(r *http.Request, headerName string) *audit.Caller {
	if pc := httputil.PeerCredFromContext(r.Context()); pc != nil {
		username := lookupUsername(pc.UID)
		return &audit.Caller{
			Username: username,
			Source:   audit.VerificationUnixPeerCreds,
		}
	}
	if headerName == "" {
		return nil
	}
	v := r.Header.Get(headerName)
	if v == "" {
		return nil
	}
	return &audit.Caller{
		Username: v,
		Source:   audit.VerificationSelfAttestedHeader,
	}
}

// lookupUsername resolves a UID to a username via os/user.LookupId.
// On lookup failure (UID not present in the local user database, NSS
// path errors), the fallback is the decimal UID as a string. The
// audit log is more useful with "uid=1234" than with no identity at
// all — a reviewer can still trace the action to a specific UID even
// if its name resolution is broken.
func lookupUsername(uid uint32) string {
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil || u == nil {
		return "uid=" + strconv.FormatUint(uint64(uid), 10)
	}
	return u.Username
}
