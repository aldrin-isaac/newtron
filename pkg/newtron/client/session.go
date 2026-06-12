package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Session-key authentication for outbound newtron HTTP calls
// (auth-design.md L2c). When a client is constructed with the
// WithSession option, it lazy-logs-in at first request via
// POST /auth/login with the configured Basic credentials, caches
// the returned key, and attaches Authorization: Bearer <key> on
// every subsequent outbound request.
//
// Intended consumer: long-running services that authenticate as a
// system identity rather than impersonating per-operator
// credentials — newtrun-server's runner being the first such case.
// The runner can't pass through operator identity because it
// originates internal requests (network listing, deploy probes)
// outside any operator's session; without its own credentials, a
// PAM-protected newtron rejects every newtrun-internal call before
// any suite scenario runs.
//
// Refresh behavior: a 401 response triggers exactly one re-login
// attempt + retry. The login surface is rare-call (typically once
// per process lifetime, again on key expiry every --session-key-ttl
// hours); the single retry handles the steady-state expiry case
// without exposing the operator to spurious 401s. Multiple
// concurrent refresh attempts coalesce under a mutex — one wire
// call per refresh event regardless of caller fan-in.
//
// Login itself goes through a separate inner *http.Client that
// does NOT carry the session round-tripper — using the wrapped
// client for login would deadlock the lazy-init path and recurse
// indefinitely on refresh.

// sessionAuth holds the credentials and the current session key.
// All key access is mutex-protected: readers via RLock get
// concurrent-safe reads of the cached value; the refresher takes
// the write lock to mint and store a new key.
type sessionAuth struct {
	baseURL string
	user    string
	pass    string
	inner   *http.Client

	mu  sync.RWMutex
	key string
}

// currentKey returns the cached session key, or empty when none is
// minted yet. The caller decides whether empty triggers a login.
func (a *sessionAuth) currentKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.key
}

// ensureKey returns the current key, minting one via POST
// /auth/login if none is cached. Concurrent first-callers coalesce
// — only one login wire-call regardless of fan-in (the
// double-checked locking inside refresh handles the race where N
// goroutines all find the key empty under RLock and serialize on
// Lock). Returns an error if the login fails (bad credentials,
// network failure, /auth/login not mounted).
func (a *sessionAuth) ensureKey(ctx context.Context) (string, error) {
	if k := a.currentKey(); k != "" {
		return k, nil
	}
	return a.mintIfEmpty(ctx)
}

// mintIfEmpty acquires the write lock, re-checks whether another
// goroutine raced ahead and minted, and only then calls login.
// This is the lazy-first-mint path; the on-401-retry path uses
// refresh() which forces a new login regardless of cache state.
func (a *sessionAuth) mintIfEmpty(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key != "" {
		return a.key, nil
	}
	return a.loginLocked(ctx)
}

// refresh forces a new login wire-call and replaces the cached
// key. Used on 401 retry — the existing key is known invalid, so
// the check-then-mint pattern doesn't apply. Concurrent callers
// still coalesce (only one login wire-call at a time) but each
// will see a fresh-from-server key on return.
func (a *sessionAuth) refresh(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loginLocked(ctx)
}

// loginLocked drives POST /auth/login. Must be called with
// a.mu held in write mode. Returns the new key (and stores it in
// a.key on success) or an error.
func (a *sessionAuth) loginLocked(ctx context.Context) (string, error) {
	loginURL := strings.TrimRight(a.baseURL, "/") + "/newtron/v1/auth/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, nil)
	if err != nil {
		return "", fmt.Errorf("build login request: %w", err)
	}
	req.SetBasicAuth(a.user, a.pass)
	resp, err := a.inner.Do(req)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login: HTTP %s", resp.Status)
	}
	// /auth/login uses the standard {data, error} envelope every
	// other newtron endpoint uses (so newtron-client and JQ-based
	// scenario capture consume it identically). The LoginResponse
	// is inside .data.
	var env struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}
	if env.Error != "" {
		return "", fmt.Errorf("login: %s", env.Error)
	}
	if env.Data.Key == "" {
		return "", fmt.Errorf("login: server returned empty key")
	}
	a.key = env.Data.Key
	return env.Data.Key, nil
}

// sessionRoundTripper wraps a base RoundTripper, attaching
// Authorization: Bearer <key> to every outbound request. On 401 it
// transparently re-logs in once and retries. Login itself bypasses
// the wrapper (uses the inner client on sessionAuth).
type sessionRoundTripper struct {
	base http.RoundTripper
	auth *sessionAuth
}

// RoundTrip implements http.RoundTripper. It does NOT inject the
// Bearer header on POSTs to /auth/login or /auth/logout — those
// carry their own credentials (Basic on login, Bearer in the
// caller's hand on logout). For all other paths it lazy-mints a
// key on first call and attaches Bearer on every subsequent call.
//
// On 401 against a Bearer-carrying request, RoundTrip re-logs in
// and replays the request once. If the replay also 401s the
// caller gets that response back unmodified — there's no infinite
// loop on persistently rejected credentials.
func (r *sessionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if isAuthEndpoint(req.URL.Path) {
		return r.base.RoundTrip(req)
	}
	// Respect a caller-set Authorization header — the caller has
	// its own credential intent (a captured session key from an
	// earlier /auth/login, a Basic header for a verification
	// scenario, etc.) and the round-tripper must not clobber it.
	// Without this guard the response-capture-driven L2c round-
	// trip suite scenario couldn't exercise revocation: the
	// runner's own Bearer would replace the captured (revoked)
	// Bearer on the post-logout request, and the 401-after-revoke
	// assertion would never fire.
	if req.Header.Get("Authorization") != "" {
		return r.base.RoundTrip(req)
	}
	key, err := r.auth.ensureKey(req.Context())
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+key)
	resp, err := r.base.RoundTrip(cloned)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	// 401: drain + close the body, refresh once, retry. The retry
	// needs a fresh body reader because the first attempt consumed
	// it; req.GetBody (set by http.NewRequest when the body is a
	// bytes.Reader / strings.Reader, which is what pkg/newtron/
	// client always passes) provides exactly that.
	resp.Body.Close()
	if req.GetBody == nil && req.Body != nil {
		// Caller passed a non-rewindable body; surfacing the
		// original 401 is more honest than returning a "couldn't
		// retry" error.
		return resp, nil
	}
	newKey, err := r.auth.refresh(req.Context())
	if err != nil {
		return nil, fmt.Errorf("refresh after 401: %w", err)
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("rewind body for retry: %w", err)
		}
		retry.Body = body
	}
	retry.Header.Set("Authorization", "Bearer "+newKey)
	return r.base.RoundTrip(retry)
}

// isAuthEndpoint reports whether the request path targets one of
// the newtron auth endpoints that must not receive a Bearer
// header — login carries Basic auth instead, logout carries the
// caller's own Bearer (different from the cached one when the
// caller is revoking a specific key).
func isAuthEndpoint(path string) bool {
	return strings.HasSuffix(path, "/auth/login") || strings.HasSuffix(path, "/auth/logout")
}

// WithSession installs a session-key authentication layer
// (auth-design.md L2c) on the client. The client lazy-calls
// POST /auth/login with the given Basic credentials on the first
// outbound request, caches the returned session key, and attaches
// Authorization: Bearer <key> to every subsequent request. On 401
// it transparently re-logs in once before propagating.
//
// Used by services that hold their own startup-time credentials
// rather than impersonating per-operator identity. newtrun-server's
// runner is the canonical consumer — it issues network-list and
// per-step calls outside any operator's session and needs its own
// way to authenticate against a PAM-protected newtron-server.
//
// Order with WithTLS: WithSession wraps whatever Transport already
// exists on the client. To compose with mTLS, apply WithTLS first
// (it sets the transport's TLSClientConfig) and WithSession after
// (it wraps the resulting transport).
func WithSession(user, pass string) Option {
	return func(c *Client) {
		base := c.httpClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		auth := &sessionAuth{
			baseURL: c.baseURL,
			user:    user,
			pass:    pass,
			// The inner client used for login wire-calls must NOT
			// route through the session round-tripper, otherwise
			// the lazy-init recursion would deadlock. It can share
			// the underlying transport (TLS config, etc.) — only
			// the round-tripper wrapping is dropped.
			inner: &http.Client{Transport: base, Timeout: c.httpClient.Timeout},
		}
		c.httpClient.Transport = &sessionRoundTripper{
			base: base,
			auth: auth,
		}
	}
}
