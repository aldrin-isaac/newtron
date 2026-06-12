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
// For dev iteration on a single engine, use the standalone binaries
// (bin/newtron-server, bin/newtrun-server, bin/newtlab-server) — same
// engine code, different entry point, separate ports. The route paths
// are identical; only the port and process boundary differ.
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
	specDir := flag.String("spec-dir", "", "spec directory to auto-register as the 'default' network on newtron")
	netID := flag.String("net-id", "default", "network ID for auto-registered spec directory")
	idleTimeout := flag.Duration("idle-timeout", 0, "SSH connection idle timeout for newtron (default 5m, negative to disable caching)")
	suitesBase := flag.String("suites-base", "newtrun/suites", "directory containing suite subdirectories (newtrun)")
	topologiesBase := flag.String("topologies-base", "newtrun/topologies", "directory containing topology subdirectories (newtlab lab-spec resolution)")
	scaffoldRoot := flag.String("scaffold-root", "", "on-disk root for derived-spec_dir scaffolds on newtron (#122); empty disables the derived-path mode of POST /newtron/v1/networks. When set, scaffold:true with no spec_dir lays out <root>/<id>")
	auditLog := flag.String("audit-log", "", "file path for the mutation audit log; empty disables audit emission entirely (default). (auth-design.md L1)")
	auditCallerHeader := flag.String("audit-caller-header", "", "HTTP header read by caller-extraction middleware on TCP listeners (typical: X-Newtron-Caller); empty disables self-attested header identity (Unix socket peer creds still work if --unix-socket is set). (auth-design.md L1)")
	unixSocket := flag.String("unix-socket", "", "Unix-domain socket path for a verified-identity listener alongside TCP; empty disables (TCP only). (auth-design.md L1)")
	secretStore := flag.String("secret-store", "", "file path for the operator-managed secret store (JSON map, mode 0600). When set, ${secret:KEY} references in spec values are resolved at network load. Empty disables resolution. (auth-design.md L0)")
	enforceAuthz := flag.Bool("enforce-authorization", false, "enforce the network.json permissions map at runtime for the newtron engine; denials surface as HTTP 403. Off (default) preserves pre-enforcement behavior — checkPermission call sites are no-ops; identity is recorded but no decisions are made. (auth-design.md L3)")
	specWatch := flag.Bool("spec-watch", false, "watch every registered network's spec directory for file changes on the newtron engine; on settled change (1s debounce) automatically reload the network so revoked grants take effect without an explicit /reload call. Off (default) preserves pre-watcher behavior. (auth-design.md L6)")
	auditIntegrity := flag.Bool("audit-log-integrity", false, "populate each audit-log entry with a hash chain so tampering with any past entry is detectable via `bin/newtron audit verify`. Off (default) leaves IDs empty. Requires --audit-log to be set. (auth-design.md L6)")
	authPAMService := flag.String("auth-pam-service", "", "PAM service name under /etc/pam.d/ that authenticates TCP user requests to the newtron engine via HTTP Basic. Empty disables PAM authentication — TCP requests are not user-authenticated; Unix socket peer creds still work where configured. (auth-design.md L2b)")
	sessionKeyTTL := flag.Duration("session-key-ttl", newtronapi.DefaultSessionKeyTTL, "absolute lifetime of session keys minted at POST /newtron/v1/auth/login. Engaged only when --auth-pam-service is also set (no PAM credential, no session key). Negative disables L2c entirely — /auth/login returns 404 and Bearer tokens are not recognized. (auth-design.md L2c)")
	flag.Parse()

	logger := log.New(os.Stderr, "newt-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
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
		Authenticator:        pamAuth,
		SessionKeyTTL:        *sessionKeyTTL,
		EnforceAuthorization: *enforceAuthz,
		SpecWatch:            *specWatch,
	})
	if *specDir != "" {
		if err := newtronSrv.RegisterNetwork(*netID, *specDir); err != nil {
			logger.Fatalf("failed to register network '%s' from %s: %v", *netID, *specDir, err)
		}
	}
	// newtrun reaches newtlab via HTTP (§27 — newtlab owns LabState).
	// In the composed binary the call is an in-process loopback to
	// the newtlab handler mounted on the same mux; in standalone
	// newtrun-server it's cross-process. Either way newtrun's runner
	// stays a client of newtlab, never a co-writer.
	newtrunSrv := newtrunapi.NewServer(newtrunapi.Config{
		SuitesBase:    *suitesBase,
		Logger:        logger,
		NewtlabClient: newtlabClient,
	})
	// newtlab consumes spec data via newtron's HTTP API (§27 — newtron
	// owns spec files). In the composed binary this is an in-process
	// loopback call to the newtron handler mounted on the same mux. Each
	// lab gets its own newtron client configured for its own network ID
	// (#116 — the network ID equals the lab name).
	newtronURL := "http://" + *listen
	newtlabSrv := newtlabapi.NewServer(newtlabapi.Config{
		TopologiesBase: *topologiesBase,
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

	// Compose the route tree. Each engine's Handler() already returns
	// a fully-wired mux + middleware chain serving its own /<name>/v1/
	// routes, so we mount on the bare prefix without path rewriting.
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

	srv := httputil.NewServer(mux, logger,
		httputil.ServerLabel("newt-server"),
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
