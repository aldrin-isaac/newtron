package sessionkey

import (
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// LoginResponse is the wire shape returned by POST /auth/login on
// success (auth-design.md §L2c). The key is the opaque session key
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

// LoginHandler returns an http.HandlerFunc that mints a new session
// key for the caller verified by PAMMiddleware (auth-design.md
// §L2b → §L2c). The handler chain that mounts this route MUST run
// PAMMiddleware upstream so the verified username is on the
// request context by the time control reaches the handler.
//
// Middleware runs upstream of PAMMiddleware in the chain (it has
// to — successful Bearer lookups must short-circuit PAM's Basic
// challenge). A /auth/login request has no Bearer header by
// definition, so Middleware passes through and PAM authenticates.
// LoginHandler then reads the PAM-verified username.
//
// Flow:
//
//  1. Reach this handler only after PAMMiddleware accepted Basic
//     auth and attached the verified username to context.
//  2. Read the verified username via httputil.PAMUsernameFromContext.
//  3. Mint a key in the store.
//  4. Return JSON via the standard {data, error} envelope so
//     consumers like newtron-client and newtrun's response-capture
//     read it the same way every other endpoint's payload reads.
//
// Failure modes:
//
//   - store == nil → 404. L2c is disabled; the route is mounted
//     unconditionally so an enable/disable flip needs no route-
//     table reshuffling, but the handler refuses without a store.
//   - No PAM username on context → 500. PAMMiddleware should have
//     rejected the request with 401 before reaching this handler;
//     a 500 here means the middleware chain drifted out of order.
//   - rand.Read failure → 500. No fallback that wouldn't
//     compromise the security property.
func LoginHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "session keys not enabled", http.StatusNotFound)
			return
		}
		user := httputil.PAMUsernameFromContext(r.Context())
		if user == "" {
			http.Error(w, "no authenticated identity on request", http.StatusInternalServerError)
			return
		}
		key, expiresAt, err := store.Mint(user)
		if err != nil {
			http.Error(w, "mint session key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		httputil.WriteJSON(w, http.StatusOK, LoginResponse{
			Key:       key,
			ExpiresAt: expiresAt,
			User:      user,
		})
	}
}

// LogoutHandler returns an http.HandlerFunc that revokes the
// session key carried on the request. Idempotent — a Bearer that
// resolves to no entry still gets 204, so a client whose key
// already expired sees the same response as one whose key was
// live. Reduces information leak ("did my key exist?") and matches
// REST idempotency expectations.
//
// Failure modes:
//
//   - store == nil → 404. Same enable/disable contract as
//     LoginHandler.
//   - No Authorization: Bearer on request → 401. The handler can't
//     know which key to revoke without one.
func LogoutHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "session keys not enabled", http.StatusNotFound)
			return
		}
		key, ok := bearerToken(r)
		if !ok {
			http.Error(w, "Authorization: Bearer required", http.StatusUnauthorized)
			return
		}
		store.Revoke(key)
		w.WriteHeader(http.StatusNoContent)
	}
}
