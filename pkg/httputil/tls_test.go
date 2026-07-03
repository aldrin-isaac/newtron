package httputil

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServer_TLSListener_RoundTrip pins the L2a happy path:
// Start binds a TLS listener; a client presenting a verified cert
// reaches the handler; the handler sees the cert CN on the request
// context via ServiceCertCNFromContext.
func TestServer_TLSListener_RoundTrip(t *testing.T) {
	cert, ca := generateTestCertPair(t, "newtron-server", "newtlab-server")
	serverCfg, err := LoadServerTLSConfig(cert.certFile, cert.keyFile, cert.caFile)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}

	var sawCN string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCN = string(ServiceCertCNFromRequest(r))
		w.WriteHeader(http.StatusOK)
	})
	s := NewServer(handler, log.New(io.Discard, "", 0),
		TLSConfig(serverCfg), ServerLabel("test"))

	addr := pickFreeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start(addr) }()
	waitForTCP(t, addr, 2*time.Second)

	clientCfg := &tls.Config{
		Certificates: []tls.Certificate{ca.clientCert},
		RootCAs:      ca.pool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   "newtron-server",
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientCfg}}
	resp, err := client.Get("https://" + addr + "/x")
	if err != nil {
		t.Fatalf("mTLS GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if sawCN != "newtlab-server" {
		t.Errorf("ServiceCertCN = %q, want newtlab-server", sawCN)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	<-serveErr
}

// TestServer_TLSListener_RejectsUntrustedClient pins that a client
// presenting no cert (or one not signed by the trusted CA) is
// rejected at the handshake — the handler never runs. This is the
// inter-service guarantee L2a buys: a rogue process cannot
// impersonate a peer engine even by knowing the listener address.
func TestServer_TLSListener_RejectsUntrustedClient(t *testing.T) {
	cert, ca := generateTestCertPair(t, "newtron-server", "newtlab-server")
	serverCfg, err := LoadServerTLSConfig(cert.certFile, cert.keyFile, cert.caFile)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}

	handlerRan := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	})
	s := NewServer(handler, log.New(io.Discard, "", 0),
		TLSConfig(serverCfg), ServerLabel("test"))
	addr := pickFreeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start(addr) }()
	waitForTCP(t, addr, 2*time.Second)

	// No client cert: should fail at handshake.
	noClientCfg := &tls.Config{
		RootCAs:    ca.pool,
		MinVersion: tls.VersionTLS12,
		ServerName: "newtron-server",
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: noClientCfg}}
	_, err = client.Get("https://" + addr + "/x")
	if err == nil {
		t.Error("expected handshake error when client presents no cert; got nil")
	}
	if handlerRan {
		t.Error("handler ran for a client with no cert — server failed open")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	<-serveErr
}

// TestLoadServerTLSConfig_DisabledStateReturnsNil pins the
// enable/disable contract from auth-design.md §2.4: an empty cert
// path is the explicit disabled state, and LoadServerTLSConfig
// returns (nil, nil) for it. The downstream Server then knows to
// stay on plain HTTP.
func TestLoadServerTLSConfig_DisabledStateReturnsNil(t *testing.T) {
	cfg, err := LoadServerTLSConfig("", "", "")
	if err != nil {
		t.Errorf("err = %v; want nil for disabled state", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v; want nil for disabled state", cfg)
	}
}

// TestLoadServerTLSConfig_CertWithoutKeyErrors pins that an
// incomplete TLS config (cert provided but no key) fails at startup
// rather than producing a broken listener that fails at handshake.
func TestLoadServerTLSConfig_CertWithoutKeyErrors(t *testing.T) {
	_, err := LoadServerTLSConfig("/somewhere/cert.pem", "", "")
	if err == nil {
		t.Error("expected err for cert without key; got nil")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Errorf("err message %q should mention 'key' so operator knows the fix", err)
	}
}

// TestLoadClientTLSConfig_DisabledStateReturnsNil pins the same
// enable/disable contract on the client side. An empty CA path is
// the disabled state; clients should keep dialing plain HTTP.
func TestLoadClientTLSConfig_DisabledStateReturnsNil(t *testing.T) {
	cfg, err := LoadClientTLSConfig("", "", "")
	if err != nil {
		t.Errorf("err = %v; want nil for disabled state", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v; want nil for disabled state", cfg)
	}
}

// TestLoadClientTLSConfig_PartialMTLSErrors pins that providing
// only one of cert/key for mTLS is rejected at startup. Asymmetric
// configs always look like operator mistakes; surfacing them
// loudly beats producing a broken transport.
func TestLoadClientTLSConfig_PartialMTLSErrors(t *testing.T) {
	dir := t.TempDir()
	ca, _ := generateTestCA(t)
	caPath := filepath.Join(dir, "ca.pem")
	writePEM(t, caPath, "CERTIFICATE", ca.cert.Raw)

	_, err := LoadClientTLSConfig("/some/cert.pem", "", caPath)
	if err == nil {
		t.Error("expected err for cert without key; got nil")
	}
}

// ============================================================================
// Test certificate generation. All certs use ECDSA-P256 for speed; tests
// generate fresh material per invocation so there are no committed PEM
// files to keep in sync with the test.
// ============================================================================

// testCertSet holds the materialized file paths for a server cert
// plus the CA bundle the server uses to verify clients.
type testCertSet struct {
	certFile string
	keyFile  string
	caFile   string
}

// testCAResult holds the in-memory CA + a pre-built client cert
// signed by it, suitable for use in *tls.Config.Certificates.
type testCAResult struct {
	cert       *x509.Certificate
	key        *ecdsa.PrivateKey
	pool       *x509.CertPool
	clientCert tls.Certificate
}

// generateTestCertPair returns (server materialized to files, CA
// pool + a client cert in memory). serverCN goes into the server
// cert's Subject CN; clientCN goes into the client cert's. Tests
// use clientCN to assert the value pulled out by connContext.
func generateTestCertPair(t *testing.T, serverCN, clientCN string) (testCertSet, testCAResult) {
	t.Helper()
	caRes, caBytes := generateTestCA(t)
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	writePEM(t, caPath, "CERTIFICATE", caBytes)

	// Server cert
	serverCert, serverKey := signLeafCert(t, caRes.cert, caRes.key, serverCN, true)
	serverCertPath := filepath.Join(dir, "server.crt")
	serverKeyPath := filepath.Join(dir, "server.key")
	writePEM(t, serverCertPath, "CERTIFICATE", serverCert)
	writeECKey(t, serverKeyPath, serverKey)

	// Client cert (in-memory tls.Certificate)
	clientCertDER, clientKey := signLeafCert(t, caRes.cert, caRes.key, clientCN, false)
	caRes.clientCert = tls.Certificate{
		Certificate: [][]byte{clientCertDER},
		PrivateKey:  clientKey,
	}

	return testCertSet{
		certFile: serverCertPath,
		keyFile:  serverKeyPath,
		caFile:   caPath,
	}, caRes
}

// generateTestCA returns a self-signed CA and its DER bytes.
func generateTestCA(t *testing.T) (testCAResult, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCAResult{
		cert: cert,
		key:  key,
		pool: pool,
	}, der
}

// signLeafCert produces a leaf cert (DER) signed by the given CA.
// serverAuth true → server cert with localhost + 127.0.0.1 SANs;
// false → client cert.
func signLeafCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, serverAuth bool) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	if serverAuth {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost", cn}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	return der, key
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeECKey(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	writePEM(t, path, "EC PRIVATE KEY", der)
}

// TestResolveServerURL pins the flag > env > fallback precedence every in-repo
// CLI shares. The flag wins outright; an empty flag falls to the env var; an
// empty flag+env falls to the caller-supplied fallback; all-empty yields "".
func TestResolveServerURL(t *testing.T) {
	const envVar = "NEWTRON_TEST_SERVER"

	t.Run("flag wins over env and fallback", func(t *testing.T) {
		t.Setenv(envVar, "http://env:1")
		if got := ResolveServerURL("http://flag:1", envVar, "http://fallback:1"); got != "http://flag:1" {
			t.Errorf("got %q, want the flag value", got)
		}
	})
	t.Run("empty flag falls to env", func(t *testing.T) {
		t.Setenv(envVar, "http://env:1")
		if got := ResolveServerURL("", envVar, "http://fallback:1"); got != "http://env:1" {
			t.Errorf("got %q, want the env value", got)
		}
	})
	t.Run("empty flag+env falls to fallback", func(t *testing.T) {
		t.Setenv(envVar, "")
		if got := ResolveServerURL("", envVar, "http://fallback:1"); got != "http://fallback:1" {
			t.Errorf("got %q, want the fallback", got)
		}
	})
	t.Run("all empty yields empty", func(t *testing.T) {
		t.Setenv(envVar, "")
		if got := ResolveServerURL("", envVar, ""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
