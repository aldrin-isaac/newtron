// Package main is newt-server — the aggregated HTTP entry point for
// the newtron-project service set.
//
// newt-server runs every engine (newtron, newtrun, newtlab) in one
// process on one port. Each engine is mounted under its
// service-prefixed routes:
//
//	/newtron/v1/...   → newtron engine handler
//	/newtrun/v1/...   → newtrun engine handler
//	/newtlab/v1/...   → newtlab engine handler
//	/newt-server/v1/health → newt-server's own health probe
//
// Dispatch is by-prefix in net/http.ServeMux. No HTTP between engines
// (same process, same goroutine call stack), no registration
// protocol, no proxy.
//
// newt-server is the only entry point for the three engines. The
// engine implementations live in pkg/{newtron,newtrun,newtlab}/api
// as plain api.Server types; this file composes them into one
// listener and route table. Per-engine standalone binaries were
// retired — every operational flow routes through newt-server.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/httputil/pamauth"
	"github.com/aldrin-isaac/newtron/pkg/httputil/sessionkey"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	newtlabapi "github.com/aldrin-isaac/newtron/pkg/newtlab/api"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	newtronapi "github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	newtrunapi "github.com/aldrin-isaac/newtron/pkg/newtrun/api"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

const defaultListen = "127.0.0.1:18080"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	idleTimeout := flag.Duration("idle-timeout", 0, "SSH connection idle timeout for newtron (default 5m, negative to disable caching)")
	networksBase := flag.String("networks-base", "networks", "directory containing per-network subdirectories. Each network owns its own spec files (network.json, topology.json, platforms.json, nodes/<name>.json) plus suites (<base>/<name>/suites/<suite>/). At boot, every <base>/<name>/topology.json triggers an auto-registration of <name> as a network; newtlab deploys read specs from the same tree; newtrun resolves suite names by scanning across networks.")
	scaffoldRoot := flag.String("scaffold-root", "", "on-disk root for derived-spec_dir scaffolds on newtron (#122); empty disables the derived-path mode of POST /newtron/v1/networks. When set, scaffold:true with no spec_dir lays out <root>/<id>")
	auditLog := flag.String("audit-log", "", "file path for the mutation audit log; empty disables audit emission entirely (default). (auth-design.md L1)")
	auditCallerHeader := flag.String("audit-caller-header", "", "HTTP header read by caller-extraction middleware on TCP listeners (typical: X-Newtron-Caller); empty disables self-attested header identity (Unix socket peer creds still work if --unix-socket is set). (auth-design.md L1)")
	unixSocket := flag.String("unix-socket", "", "Unix-domain socket path for a verified-identity listener alongside TCP; empty disables (TCP only). (auth-design.md L1)")
	secretStore := flag.String("secret-store", "", "file path for the operator-managed secret store (JSON map, mode 0600). When set, ${secret:KEY} references in spec values are resolved at network load. Empty disables resolution. (auth-design.md L0)")
	enforceAuthz := flag.Bool("enforce-authorization", false, "enforce the network.json permissions map at runtime for the newtron engine; denials surface as HTTP 403. Off (default) preserves pre-enforcement behavior — checkPermission call sites are no-ops; identity is recorded but no decisions are made. (auth-design.md L3)")
	specWatch := flag.Bool("spec-watch", false, "watch every registered network's spec directory for file changes on the newtron engine; on settled change (1s debounce) automatically reload the network so revoked grants take effect without an explicit /reload call. Off (default) preserves pre-watcher behavior. (auth-design.md L6)")
	auditIntegrity := flag.Bool("audit-log-integrity", false, "populate each audit-log entry with a hash chain so tampering with any past entry is detectable via `bin/newtron audit verify`. Off (default) leaves IDs empty. Requires --audit-log to be set. (auth-design.md L6)")
	authPAMService := flag.String("auth-pam-service", "", "PAM service name under /etc/pam.d/ that authenticates TCP user requests to the newtron engine via HTTP Basic. Empty disables PAM authentication — TCP requests are not user-authenticated; Unix socket peer creds still work where configured. (auth-design.md L2b)")
	sessionKeyTTL := flag.Duration("session-key-ttl", sessionkey.DefaultTTL, "absolute lifetime of session keys minted at POST /newt-server/v1/auth/login. Engaged only when --auth-pam-service is also set (no PAM credential, no session key). Negative disables L2c entirely — /auth/login returns 404 and Bearer tokens are not recognized. (auth-design.md L2c)")
	tlsCert := flag.String("tls-cert", "", "server certificate (PEM) for the TCP listener. When set together with --tls-key, the TCP listener serves HTTPS instead of HTTP. Falls back to $NEWTRON_TLS_CERT when the flag is empty. Empty (no flag + no env) disables listener-side TLS (operators terminate TLS at a reverse proxy in front of newt-server, or rely on the Unix socket where configured). (auth-design.md L2a)")
	tlsKey := flag.String("tls-key", "", "server private key (PEM) matching --tls-cert. Required when --tls-cert is set. Falls back to $NEWTRON_TLS_KEY. (auth-design.md L2a)")
	tlsCA := flag.String("tls-ca", "", "client-CA PEM bundle. When set together with --tls-cert/--tls-key, the listener requires every client to present a certificate that verifies against this pool (mTLS); the peer cert's Subject CN flows through ServiceCertCNFromRequest into the caller-middleware identity slot with priority over PAM and the self-attested header (auth-design.md L2a). Falls back to $NEWTRON_TLS_CA. Empty leaves TLS one-way (server-auth only); client identity continues to come from PAM / Unix peer creds / the header.")
	flag.Parse()

	logger := log.New(os.Stderr, "newt-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	// auth-design.md L2a: each TLS flag falls back to the matching
	// NEWTRON_TLS_* env var when the flag is empty, so the same
	// `export` line that configures the CLI clients also drives
	// the server. Flag value wins on conflict; both unset → plain
	// HTTP (pre-L2a default).
	if *tlsCert == "" {
		*tlsCert = os.Getenv(httputil.EnvTLSCert)
	}
	if *tlsKey == "" {
		*tlsKey = os.Getenv(httputil.EnvTLSKey)
	}
	if *tlsCA == "" {
		*tlsCA = os.Getenv(httputil.EnvTLSCA)
	}
	// LoadServerTLSConfig returns nil + nil when *tlsCert is empty
	// (the disabled state); the resulting nil flows into
	// httputil.TLSConfig and the Server starts plain HTTP. mTLS
	// engages only when *tlsCA is also set (LoadServerTLSConfig
	// sets ClientAuth=RequireAndVerifyClientCert in that path).
	// Fail fast at startup: a bad cert path or mismatched cert/key
	// is an operator misconfiguration the audit posture must not
	// silently swallow.
	tlsConfig, err := httputil.LoadServerTLSConfig(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		logger.Fatalf("--tls-cert/--tls-key/--tls-ca: %v", err)
	}

	// Construct the three engine servers. Each owns its own state
	// (network actors, run registry, deploy registry, brokers); we
	// don't share state across engines — they communicate only via
	// HTTP requests that travel through this process's mux.
	//
	// newtron consults newtlab at this same listen address (the engines
	// share one process; the URL is the in-process loopback). The
	// composition happens here in cmd — newtron's api package never
	// sees newtlab; newtlab's client never sees newtron.
	newtlabClient := newtlabclient.New("http://" + *listen)
	newtronPortResolver := newtlabclient.NewPortResolver(newtlabClient)

	// auth-design.md L1: install the audit logger when --audit-log
	// is set. The audit middleware in pkg/newtron/api/ reads via
	// audit.Log; an unset logger makes Log a silent no-op.
	// auth-design.md L6: --audit-log-integrity engages the hash chain.
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
	// is set. Nil store is the L0 disabled state.
	var store secret.Store
	if *secretStore != "" {
		fs, err := secret.NewFileStore(*secretStore)
		if err != nil {
			logger.Fatalf("opening secret store %s: %v", *secretStore, err)
		}
		store = fs
	}

	// auth-design.md L2b: construct the PAM authenticator when a
	// service name is configured. Empty disables — TCP requests
	// pass through the newtron engine's PAMMiddleware unchanged.
	// auth-design.md L2c: with --auth-pam-service set, the
	// /auth/login and /auth/logout routes auto-engage; the TTL
	// flag tunes session-key lifetime.
	var pamAuth httputil.Authenticator
	if *authPAMService != "" {
		pamAuth = &pamauth.PAMAuthenticator{ServiceName: *authPAMService}
	}

	newtronSrv := newtronapi.NewServer(newtronapi.Config{
		Logger:               logger,
		IdleTimeout:          *idleTimeout,
		PortResolver:         newtronPortResolver,
		ScaffoldRoot:         *scaffoldRoot,
		AuditCallerHeader:    *auditCallerHeader,
		UnixSocketPath:       *unixSocket,
		SecretStore:          store,
		AuditLogPath:         *auditLog,
		EnforceAuthorization: *enforceAuthz,
		SpecWatch:            *specWatch,
	})
	// Auto-discovery: every <networks-base>/<name>/topology.json
	// on disk is pre-registered as a network with id=<name>. Mirrors the
	// long-standing newtlab behavior (cmd/newtlab/main.go:282 scans for
	// the same shape to resolve deploy targets) — newtron's registry was
	// previously the only consumer of this layout that didn't auto-load,
	// requiring an explicit --spec-dir or POST /networks for every slot.
	// After auto-discovery, `bin/newt-server &` (no flags) makes every
	// network in the tree addressable as /networks/<name>/... immediately.
	//
	// Explicit POST /newtron/v1/networks remains for the lab-as-network
	// case (network id ≠ basename, e.g. `bin/newtlab deploy alice-lab`
	// binds id "alice-lab" to a spec_dir under one of the templates here).
	// In that case Server.RegisterNetwork is idempotent on a matching
	// spec_dir, so the deploy path doesn't conflict with the pre-registered
	// template slot.
	discoverAndRegisterNetworks(newtronSrv, *networksBase, logger)
	// newtrun reaches newtlab via HTTP (§27 — newtlab owns LabState).
	// In the composed binary the call is an in-process loopback to
	// the newtlab handler mounted on the same mux; in standalone
	// newtrun-server it's cross-process. Either way newtrun's runner
	// stays a client of newtlab, never a co-writer.
	newtrunSrv := newtrunapi.NewServer(newtrunapi.Config{
		NetworksBase: *networksBase,
		Logger:         logger,
		NewtlabClient:  newtlabClient,
	})
	// newtlab consumes spec data via newtron's HTTP API (§27 — newtron
	// owns spec files). In the composed binary this is an in-process
	// loopback call to the newtron handler mounted on the same mux. Each
	// lab gets its own newtron client configured for its own network ID
	// (#116 — the network ID equals the lab name).
	newtronURL := "http://" + *listen
	newtlabSrv := newtlabapi.NewServer(newtlabapi.Config{
		NetworksBase: *networksBase,
		Logger:         logger,
		NewtronClientFor: func(networkID string) newtlab.SpecClient {
			return newtronclient.New(newtronURL, networkID)
		},
		// In the composed newt-server, newtlab-server routes are
		// mounted on the same listener as newtron — so the
		// orchestrator URL newtlink pushes BridgeStats to is the same
		// base as newtronURL (#118).
		OrchestratorURL: newtronURL,
	})

	// auth-design.md L2c: build the server-wide session-key store
	// when L2b (PAM) is also configured. Without an authenticator
	// there is no credential to derive a session key from, and
	// /auth/login refuses to mount. SessionKeyTTL < 0 lets an
	// operator suppress L2c even when PAM is on.
	var sessionKeys *sessionkey.Store
	if pamAuth != nil && *sessionKeyTTL >= 0 {
		ttl := *sessionKeyTTL
		if ttl == 0 {
			ttl = sessionkey.DefaultTTL
		}
		sessionKeys = sessionkey.NewStore(ttl)
	}

	// Compose the route tree. Each engine's Handler() already returns
	// a fully-wired mux + middleware chain serving its own /<name>/v1/
	// routes, so we mount on the bare prefix without path rewriting.
	// /newt-server/v1/auth/{login,logout} live at the server boundary
	// (auth-design.md L2c) — they are not engine concerns.
	mux := http.NewServeMux()
	mux.Handle("/newtron/v1/", newtronSrv.Handler())
	mux.Handle("/newtrun/v1/", newtrunSrv.Handler())
	mux.Handle("/newtlab/v1/", newtlabSrv.Handler())
	mux.HandleFunc("GET /newt-server/v1/health", func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version.Version,
		})
	})
	mux.HandleFunc("POST /newt-server/v1/auth/login", sessionkey.LoginHandler(sessionKeys))
	mux.HandleFunc("POST /newt-server/v1/auth/logout", sessionkey.LogoutHandler(sessionKeys))

	// auth-design.md L2b + L2c: outer identity middleware sits
	// between requestID and the engine muxes. sessionkey.Middleware
	// recognizes Authorization: Bearer first; on success it skips
	// the PAM Basic challenge for the same request. PAMMiddleware
	// then verifies any Basic credentials. Both layers attach the
	// verified username to the request context; the engines'
	// callerMiddleware reads it without caring which layer set it.
	var handler http.Handler = mux
	handler = httputil.PAMMiddleware(pamAuth)(handler)
	handler = sessionkey.Middleware(sessionKeys)(handler)

	srv := httputil.NewServer(handler, logger,
		httputil.ServerLabel("newt-server"),
		// auth-design.md L2a: pass the loaded TLS config through.
		// tlsConfig is nil when --tls-cert is empty — the Server
		// stays on plain HTTP, preserving pre-L2a behavior.
		httputil.TLSConfig(tlsConfig),
		// SSE-friendly: no per-request write deadline — long-lived
		// SSE streams from newtrun (run events) and newtlab (deploy
		// events) must not be killed by the server.
		httputil.WriteTimeout(0),
		// On shutdown, drain each engine's in-flight work before
		// closing the HTTP listener. The order matters only for
		// graceful shutdown reporting — they're independent.
		httputil.OnShutdown(func() { _ = newtlabSrv.Stop(context.Background()) }),
		httputil.OnShutdown(func() { _ = newtrunSrv.Stop(context.Background()) }),
		httputil.OnShutdown(func() { _ = newtronSrv.Stop(context.Background()) }),
		httputil.OnShutdown(func() {
			if sessionKeys != nil {
				sessionKeys.Stop()
			}
		}),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(*listen) }()

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
	logger.Printf("WARNING: --listen=%s binds to a non-loopback address; newt-server has no built-in authentication.", listen)
	logger.Printf("WARNING: wrap with a reverse proxy (TLS + auth) before exposing on a shared network.")
	return nil
}
