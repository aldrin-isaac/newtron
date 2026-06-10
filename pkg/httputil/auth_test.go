package httputil

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockAuth is a test-only Authenticator. The middleware tests never
// stand up a real PAM stack — the cgo PAMAuthenticator lives in
// pkg/httputil/pamauth and is only built when an operator wires it.
type mockAuth struct {
	expectUser, expectPass string
	calls                  int
}

func (m *mockAuth) Authenticate(u, p string) error {
	m.calls++
	if u != m.expectUser || p != m.expectPass {
		return errors.New("mock: bad credentials")
	}
	return nil
}

// TestPAMMiddleware_AcceptsValidBasicAuth pins the L2b happy path:
// a request with HTTP Basic credentials the Authenticator accepts
// reaches the handler with the verified username on its context.
func TestPAMMiddleware_AcceptsValidBasicAuth(t *testing.T) {
	auth := &mockAuth{expectUser: "alice", expectPass: "s3cret"}
	var gotUsername string
	handler := PAMMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername = PAMUsernameFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.SetBasicAuth("alice", "s3cret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if gotUsername != "alice" {
		t.Errorf("PAMUsernameFromContext = %q, want alice", gotUsername)
	}
	if auth.calls != 1 {
		t.Errorf("Authenticate called %d times, want 1", auth.calls)
	}
}

// TestPAMMiddleware_RejectsMissingCreds pins that a request with no
// Authorization header gets 401 + WWW-Authenticate so the client
// knows to prompt. The handler never runs — the L2b enforcement
// guarantee.
func TestPAMMiddleware_RejectsMissingCreds(t *testing.T) {
	handlerRan := false
	handler := PAMMiddleware(&mockAuth{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic") {
		t.Errorf("WWW-Authenticate = %q, want Basic realm=...", got)
	}
	if handlerRan {
		t.Error("handler ran without credentials — middleware failed open")
	}
}

// TestPAMMiddleware_RejectsBadCreds pins that wrong credentials
// produce 401 *without* a WWW-Authenticate header — repeating the
// challenge would invite a guessing attempt, and the L2b
// expectation is that the client stops after one rejection.
func TestPAMMiddleware_RejectsBadCreds(t *testing.T) {
	auth := &mockAuth{expectUser: "alice", expectPass: "s3cret"}
	handler := PAMMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler ran with bad credentials")
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.SetBasicAuth("alice", "wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q on failed auth, want empty (no retry challenge)", got)
	}
	if auth.calls != 1 {
		t.Errorf("Authenticate called %d times, want 1", auth.calls)
	}
}

// TestPAMMiddleware_NilAuthIsPassthrough pins the L2b disabled
// state: when no Authenticator is configured, the middleware is a
// transparent passthrough. This preserves the pre-L2b behavior for
// any deployment that doesn't set --auth-pam-service.
func TestPAMMiddleware_NilAuthIsPassthrough(t *testing.T) {
	handlerRan := false
	handler := PAMMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !handlerRan {
		t.Error("handler did not run when Authenticator is nil — L2b disabled state must passthrough")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestPAMMiddleware_SkipsWhenUnixSocketIdentityPresent pins the
// priority rule: a request that already has verified peer creds
// (Unix-socket / L1) doesn't have to also present Basic auth.
// PAM is the TCP fallback, not a universal gate.
func TestPAMMiddleware_SkipsWhenUnixSocketIdentityPresent(t *testing.T) {
	auth := &mockAuth{}
	handlerRan := false
	handler := PAMMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req = req.WithContext(WithPeerCredForTest(req.Context(), &PeerCred{UID: 0, PID: 1}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !handlerRan {
		t.Error("handler did not run — PAM should skip when PeerCred is present")
	}
	if auth.calls != 0 {
		t.Errorf("Authenticate called %d times for Unix-socket request, want 0", auth.calls)
	}
}

// TestPAMMiddleware_SkipsWhenServiceCertCNPresent pins that mTLS-
// verified callers (L2a) skip PAM. Inter-service traffic with a
// verified peer cert doesn't get a second factor demanded.
func TestPAMMiddleware_SkipsWhenServiceCertCNPresent(t *testing.T) {
	auth := &mockAuth{}
	handler := PAMMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	// Real PAM-skip check reads ServiceCertCNFromRequest, which
	// inspects r.TLS. The test simulates by attaching a TLS
	// connection state with a verified chain — same shape the
	// server's http stack provides on real mTLS connections.
	req.TLS = synthesizeVerifiedTLSState("newtlab-server")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (mTLS-verified should skip PAM)", rec.Code)
	}
	if auth.calls != 0 {
		t.Errorf("Authenticate called %d times for mTLS request, want 0", auth.calls)
	}
}

// synthesizeVerifiedTLSState builds a *tls.ConnectionState carrying
// a single VerifiedChain whose leaf has the given CN. Used by the
// PAM-skip test to simulate "this request arrived over verified
// mTLS" without standing up a real TLS handshake. The shape matches
// what Go's net/http populates on r.TLS for a verified mTLS
// connection.
func synthesizeVerifiedTLSState(cn string) *tls.ConnectionState {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	return &tls.ConnectionState{
		HandshakeComplete: true,
		VerifiedChains:    [][]*x509.Certificate{{tmpl}},
	}
}
