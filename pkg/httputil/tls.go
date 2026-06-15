package httputil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Env-var names every binary in this repo reads to compose TLS
// behavior automatically (auth-design.md L2a operator-experience
// note). Set them once in the operator's shell / systemd unit and
// every binary picks them up: `cmd/newt-server` as its server cert
// + client-CA pool, the three CLIs (`cmd/newtron`, `cmd/newtrun`,
// `cmd/newtlab`) as their client cert + trust pool. Unset = plain
// HTTP everywhere (the pre-L2a default).
const (
	EnvTLSCert = "NEWTRON_TLS_CERT"
	EnvTLSKey  = "NEWTRON_TLS_KEY"
	EnvTLSCA   = "NEWTRON_TLS_CA"
)

// LoadClientTLSConfigFromEnv reads NEWTRON_TLS_CERT / NEWTRON_TLS_KEY /
// NEWTRON_TLS_CA from the environment and delegates to
// LoadClientTLSConfig. Returns nil + nil when NEWTRON_TLS_CA is
// unset — the disabled-state signal that the caller should dial
// plain HTTP. Used by every in-repo CLI client construction so an
// operator's `export` is enough to enable TLS across all of them.
func LoadClientTLSConfigFromEnv() (*tls.Config, error) {
	return LoadClientTLSConfig(os.Getenv(EnvTLSCert), os.Getenv(EnvTLSKey), os.Getenv(EnvTLSCA))
}

// LoadServerTLSConfig builds a *tls.Config suitable for use on an
// httputil.Server's listener (auth-design.md L2a inter-service
// mTLS). Returns nil + nil err when certFile is empty — the
// disabled-state signal that Start should use plain HTTP.
//
// When certFile + keyFile are set: TLS-only listener (no client
// cert required). Suitable for browser/operator-facing endpoints.
//
// When clientCAFile is also set: mTLS. The listener requires every
// client to present a certificate that verifies against the CA
// pool loaded from clientCAFile; identity for the audit log is the
// peer cert's Subject Common Name. This is the inter-service
// posture: every engine running with its own cert from a shared CA.
//
// Missing files, malformed PEM, or mismatched cert/key pairs are
// errors. The intent is "fail fast at startup" so the operator
// fixes the configuration before any connection is accepted.
func LoadServerTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	if certFile == "" {
		return nil, nil
	}
	if keyFile == "" {
		return nil, fmt.Errorf("httputil: TLS cert %q provided but key file is empty", certFile)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("httputil: loading TLS cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if clientCAFile != "" {
		pool, err := loadCAPool(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("httputil: loading client CA pool: %w", err)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// LoadClientTLSConfig builds a *tls.Config for an HTTP client
// dialing a TLS-protected server (auth-design.md L2a). Returns nil
// + nil err when caFile is empty — the disabled-state signal that
// the client should dial plain HTTP.
//
// When caFile alone is set: the client verifies the server cert
// against the CA pool. Used when only the server is authenticated.
//
// When certFile + keyFile are also set: the client presents its own
// cert (mTLS). This is the inter-service posture for an engine
// dialing another engine.
func LoadClientTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	if caFile == "" {
		return nil, nil
	}
	pool, err := loadCAPool(caFile)
	if err != nil {
		return nil, fmt.Errorf("httputil: loading CA pool: %w", err)
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("httputil: mTLS requires both client cert and key (got cert=%q key=%q)", certFile, keyFile)
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("httputil: loading client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// loadCAPool reads a PEM-encoded CA bundle from path and returns it
// as a x509.CertPool. Empty file or no PEM blocks → error, on the
// principle that loading an empty trust anchor is almost always a
// misconfiguration the operator wants to know about at startup.
func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%q contains no valid PEM certificates", path)
	}
	return pool, nil
}
