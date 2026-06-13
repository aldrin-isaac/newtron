// Package main is the standalone newtron-server entry point.
//
// Use this binary when iterating on newtron code in isolation
// (rebuild + restart only newtron without disturbing other engines'
// in-memory state, e.g. SSH-tunnel caches in a different newtron
// instance). For production / aggregated deployment, see
// cmd/newt-server/, which mounts every engine on one port.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/httputil/pamauth"
	"github.com/aldrin-isaac/newtron/pkg/httputil/sessionkey"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
)

// defaultListen — loopback-only; newt-server fronts external traffic on :18080.
const defaultListen = "127.0.0.1:19080"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	specDir := flag.String("spec-dir", "", "spec directory to auto-register as 'default' network")
	netID := flag.String("net-id", "default", "network ID for auto-registered spec directory")
	idleTimeout := flag.Duration("idle-timeout", 0, "SSH connection idle timeout (default 5m, negative to disable caching)")
	newtlabServer := flag.String("newtlab-server", "http://127.0.0.1:18080", "newtlab-server base URL; empty disables newtlab consultation (real-hardware deployments)")
	scaffoldRoot := flag.String("scaffold-root", "", "on-disk root for derived-spec_dir scaffolds (#122); empty disables the derived-path mode of POST /newtron/v1/networks. When set, scaffold:true with no spec_dir lays out <root>/<id>")
	auditLog := flag.String("audit-log", "", "file path for the mutation audit log; empty disables audit emission entirely (default). (auth-design.md L1)")
	auditCallerHeader := flag.String("audit-caller-header", "", "HTTP header read by caller-extraction middleware on TCP listeners (typical: X-Newtron-Caller); empty disables self-attested header identity (Unix socket peer creds still work if --unix-socket is set). (auth-design.md L1)")
	unixSocket := flag.String("unix-socket", "", "Unix-domain socket path for a verified-identity listener alongside TCP; empty disables (TCP only). (auth-design.md L1)")
	secretStore := flag.String("secret-store", "", "file path for the operator-managed secret store (JSON map, mode 0600). When set, ${secret:KEY} references in spec values are resolved at network load. Empty disables resolution — plaintext spec values keep working; references in spec become hard errors. (auth-design.md L0)")
	tlsCert := flag.String("tls-cert", "", "PEM-encoded TLS certificate for the TCP listener (both this server's identity to peers AND its client cert when calling newtlab-server). Empty disables TLS — plain HTTP. (auth-design.md L2a)")
	tlsKey := flag.String("tls-key", "", "PEM-encoded private key for --tls-cert. (auth-design.md L2a)")
	tlsCA := flag.String("tls-ca", "", "PEM-encoded CA bundle used both to verify incoming peer client certs (mTLS on the listener) AND to verify newtlab-server's cert when calling it. Empty: TLS-only (no mTLS); inter-service trust is undefined. (auth-design.md L2a)")
	authPAMService := flag.String("auth-pam-service", "", "PAM service name under /etc/pam.d/ that authenticates TCP user requests via HTTP Basic. Empty disables PAM authentication — TCP requests are not user-authenticated; Unix socket peer creds and mTLS cert CN still work where configured. (auth-design.md L2b)")
	sessionKeyTTL := flag.Duration("session-key-ttl", sessionkey.DefaultTTL, "absolute lifetime of session keys minted at POST /newtron/v1/auth/login. Engaged only when --auth-pam-service is also set (no PAM credential, no session key). Negative disables L2c entirely — /auth/login returns 404 and Bearer tokens are not recognized. (auth-design.md L2c)")
	enforceAuthz := flag.Bool("enforce-authorization", false, "enforce the network.json permissions map at runtime: every spec/profile mutation checks the verified caller against the spec's grant table; denials return HTTP 403 with a typed AuthorizationError payload. Off (default) preserves pre-enforcement behavior — checks are no-ops; verified identity is still recorded in the audit log when configured. (auth-design.md L3)")
	specWatch := flag.Bool("spec-watch", false, "watch every registered network's spec directory for file changes; on settled change (1s debounce) automatically reload the network so revoked grants take effect without an explicit /reload call. Off (default) preserves pre-watcher behavior — operators POST /networks/<id>/reload to make changes observable. (auth-design.md L6)")
	auditIntegrity := flag.Bool("audit-log-integrity", false, "populate each audit-log entry with a hash chain (Event.ID = SHA256(prev_hash || canonical_json), Event.PrevHash = previous entry's ID) so tampering with any past entry is detectable via `bin/newtron audit verify`. Off (default) leaves IDs empty — pre-integrity behavior. Requires --audit-log to be set. (auth-design.md L6)")
	flag.Parse()

	logger := log.New(os.Stderr, "newtron-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	// auth-design.md L2a: load TLS config once; reuse for both
	// directions. The same cert/key acts as this server's identity
	// to incoming peers AND as its client cert when dialing
	// newtlab-server. nil from either Load means the corresponding
	// path is empty — the L2a disabled state on that direction.
	serverTLS, err := httputil.LoadServerTLSConfig(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		logger.Fatalf("server TLS: %v", err)
	}
	clientTLS, err := httputil.LoadClientTLSConfig(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		logger.Fatalf("client TLS: %v", err)
	}

	// cmd is the composition layer: it knows which engine provides
	// the port-resolver implementation. newtron's api package sees
	// only the contract (api.PortResolver); newtlab's client package
	// supplies the concrete satisfier.
	var portResolver api.PortResolver
	if *newtlabServer != "" {
		portResolver = newtlabclient.NewPortResolver(
			newtlabclient.New(*newtlabServer, newtlabclient.WithTLS(clientTLS)),
		)
	}

	// auth-design.md L1: install the audit logger when --audit-log
	// is set. The audit middleware in pkg/newtron/api/ reads via
	// audit.Log; an unset logger makes Log a silent no-op.
	// auth-design.md L6: when --audit-log-integrity is set, the
	// FileLogger hash-chains every entry so tampering is detectable
	// via `bin/newtron audit verify`.
	if *auditLog != "" {
		var al *audit.FileLogger
		var err error
		if *auditIntegrity {
			al, err = audit.NewFileLoggerWithIntegrity(*auditLog, audit.RotationConfig{})
		} else {
			al, err = audit.NewFileLogger(*auditLog, audit.RotationConfig{})
		}
		if err != nil {
			logger.Fatalf("failed to open audit log %s: %v", *auditLog, err)
		}
		audit.SetDefaultLogger(al)
	} else if *auditIntegrity {
		logger.Println("WARNING: --audit-log-integrity has no effect without --audit-log")
	}

	// auth-design.md L0: open the secret store when --secret-store
	// is set. Nil store is the L0 disabled state (plaintext spec
	// values work; references would error at load).
	var store secret.Store
	if *secretStore != "" {
		fs, err := secret.NewFileStore(*secretStore)
		if err != nil {
			logger.Fatalf("opening secret store %s: %v", *secretStore, err)
		}
		store = fs
	}

	// auth-design.md L2b: install the PAM authenticator when a
	// service name is configured. Empty disables — TCP requests
	// pass through PAMMiddleware unchanged.
	var pamAuth httputil.Authenticator
	if *authPAMService != "" {
		pamAuth = &pamauth.PAMAuthenticator{ServiceName: *authPAMService}
	}

	srv := api.NewServer(api.Config{
		Logger:               logger,
		IdleTimeout:          *idleTimeout,
		PortResolver:         portResolver,
		ScaffoldRoot:         *scaffoldRoot,
		AuditCallerHeader:    *auditCallerHeader,
		UnixSocketPath:       *unixSocket,
		SecretStore:          store,
		TLSConfig:            serverTLS,
		Authenticator:        pamAuth,
		SessionKeyTTL:        *sessionKeyTTL,
		EnforceAuthorization: *enforceAuthz,
		SpecWatch:            *specWatch,
	})

	if *specDir != "" {
		if err := srv.RegisterNetwork(*netID, *specDir); err != nil {
			logger.Fatalf("failed to register network '%s' from %s: %v", *netID, *specDir, err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(*listen)
	}()

	select {
	case err := <-errCh:
		logger.Fatalf("server error: %v", err)
	case <-ctx.Done():
		logger.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Stop(shutdownCtx); err != nil {
			logger.Fatalf("shutdown error: %v", err)
		}
		logger.Println("shutdown complete")
	}
}

// warnIfNonLoopback emits an explicit acknowledgment in the startup
// log when the operator binds to a non-loopback interface. newtron-server
// has no built-in authentication; non-loopback exposure is the
// operator's deliberate choice.
func warnIfNonLoopback(listen string, logger *log.Logger) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return nil
	}
	logger.Printf("WARNING: --listen=%s binds to a non-loopback address; this server has no built-in authentication.", listen)
	logger.Printf("WARNING: wrap with a reverse proxy (TLS + auth) before exposing on a shared network.")
	return nil
}
