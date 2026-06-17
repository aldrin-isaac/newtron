package newtrun

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunner_ConnectToServer_UsesTLSConfig pins the auth-design.md
// L2a contract for the runner: when r.NewtronClientTLS is set, the
// outbound HTTP client honors it and an mTLS-only newtron-server
// accepts the connection. Without this plumbing, the runner would
// have been silently broken against any L2a-enforced deployment.
func TestRunner_ConnectToServer_UsesTLSConfig(t *testing.T) {
	server := newTLSStubServer(t)
	defer server.Close()

	r := NewRunner(t.TempDir())
	r.ServerURL = server.URL
	r.NetworkID = "default"
	r.NewtronClientTLS = &tls.Config{
		RootCAs:            certPool(t, server.TLS.Certificates[0]),
		InsecureSkipVerify: true, // test cert is self-signed; CA check is L2a's job in production
	}

	if err := r.connectToServer(); err != nil {
		t.Fatalf("connectToServer with TLS: %v", err)
	}
	if r.Network != "test-topo" {
		t.Errorf("Topology = %q, want test-topo (stub server's response)", r.Network)
	}
}

// TestRunner_ConnectToServer_NoTLSFailsAgainstTLSServer pins the
// other half: without NewtronClientTLS, the plain-HTTP client cannot
// dial a TLS-only server. This is the bug pre-fix: the runner with
// nil NewtronClientTLS produces a transport error, NOT a silent
// "everything's fine" pass-through.
func TestRunner_ConnectToServer_NoTLSFailsAgainstTLSServer(t *testing.T) {
	server := newTLSStubServer(t)
	defer server.Close()

	r := NewRunner(t.TempDir())
	r.ServerURL = server.URL
	r.NetworkID = "default"
	// NewtronClientTLS deliberately nil — pre-fix runner behavior.

	err := r.connectToServer()
	if err == nil {
		t.Fatal("connectToServer succeeded against a TLS-only server with nil NewtronClientTLS — TLS plumbing is bypassed")
	}
	// The transport error mentions HTTP/HTTPS scheme or TLS; the exact
	// message varies by Go version but always carries one of these
	// substrings. Loose match keeps the test stable across upgrades.
	msg := err.Error()
	if !(strings.Contains(msg, "HTTP") || strings.Contains(msg, "tls") || strings.Contains(msg, "TLS")) {
		t.Errorf("error = %q, want transport-level TLS mismatch", msg)
	}
}

// TestRunner_ConnectToServer_NoTLS_PlainServerWorks pins the
// disabled-state path (auth-design.md §2.4): with NewtronClientTLS
// nil and a plain-HTTP server, the runner connects normally. This is
// the pre-L2a behavior the fix MUST preserve.
func TestRunner_ConnectToServer_NoTLS_PlainServerWorks(t *testing.T) {
	server := newPlainStubServer(t)
	defer server.Close()

	r := NewRunner(t.TempDir())
	r.ServerURL = server.URL
	r.NetworkID = "default"
	// NewtronClientTLS deliberately nil — disabled state.

	if err := r.connectToServer(); err != nil {
		t.Fatalf("connectToServer over plain HTTP with nil TLS: %v", err)
	}
}

// newTLSStubServer returns an httptest.NewTLSServer that responds to
// GET /newtron/v1/networks/{id} with a minimal NetworkInfo body the
// runner's connectToServer parses. The server's auto-generated cert
// is self-signed; tests use it via RootCAs + InsecureSkipVerify.
func newTLSStubServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(networkInfoHandler))
}

// newPlainStubServer returns the same handler over plain HTTP for the
// disabled-state test.
func newPlainStubServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(networkInfoHandler))
}

// networkInfoHandler returns a one-entry NetworkInfo list on the
// path the client's GetNetworkInfo() hits (which delegates to
// ListNetworks() — GET /newtron/v1/networks). Other paths 404 so a
// test that strays surfaces it immediately.
func networkInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/newtron/v1/networks" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": []map[string]any{
			{
				"id":       "default",
				"dir": "/tmp/test-specs",
				"topology": "test-topo",
				"nodes":    []string{"switch1"},
			},
		},
	})
}

// certPool builds an x509 cert pool from one cert; used to make the
// test's RootCAs trust the stub server's self-signed cert.
func certPool(t *testing.T, cert tls.Certificate) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	for _, raw := range cert.Certificate {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			t.Fatalf("parse stub cert: %v", err)
		}
		pool.AddCert(c)
	}
	return pool
}
