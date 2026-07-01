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
	"os/user"
	"path/filepath"
	"slices"
	"sort"
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
	netpkg "github.com/aldrin-isaac/newtron/pkg/newtron/network"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	newtrunapi "github.com/aldrin-isaac/newtron/pkg/newtrun/api"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

const defaultListen = "127.0.0.1:18080"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	idleTimeout := flag.Duration("idle-timeout", 0, "SSH connection idle timeout for newtron (default 5m, negative to disable caching)")
	networksBase := flag.String("networks-base", "networks", "directory containing per-network subdirectories. Each network owns its own spec files (network.json, topology.json, nodes/<name>.json) plus suites (<base>/<name>/suites/<suite>/). At boot, every <base>/<name>/topology.json triggers an auto-registration of <name> as a network; newtlab deploys read specs from the same tree; newtrun resolves suite names by scanning across networks.")
	platformsBase := flag.String("platforms-base", "platforms", "directory containing one file per platform (<filename basename>.json). Loaded once at startup; every network reads from the same registry. Platforms describe hardware/image-level identities (HWSKU, port count, VM image, dataplane) — shared across networks by definition. Filename basename must equal the file's name field.")
	auditLog := flag.String("audit-log", "", "file path for the mutation audit log; empty disables audit emission entirely (default). (auth-design.md L1)")
	auditCallerHeader := flag.String("audit-caller-header", "", "HTTP header read by caller-extraction middleware on TCP listeners (typical: X-Newtron-Caller); empty disables self-attested header identity (Unix socket peer creds still work if --unix-socket is set). (auth-design.md L1)")
	unixSocket := flag.String("unix-socket", "", "Unix-domain socket path for a verified-identity listener alongside TCP; empty disables (TCP only). (auth-design.md L1)")
	secretStore := flag.String("secret-store", "", "file path for the operator-managed secret store (JSON map, mode 0600). When set, ${secret:KEY} references in spec values are resolved at network load. Empty disables resolution. (auth-design.md L0)")
	enforceAuthz := flag.Bool("enforce-authorization", false, "enforce the network.json permissions map at runtime for the newtron engine; denials surface as HTTP 403. Off (default) preserves pre-enforcement behavior — checkPermission call sites are no-ops; identity is recorded but no decisions are made. (auth-design.md L3)")
	superUsers := flag.String("super-users", "", "comma-separated usernames that are super-users across EVERY network and function of the newtron engine — they bypass all permission checks on all networks without being named in any network.json's super_users. Falls back to $NEWTRON_SUPER_USERS when empty. Only effective with --enforce-authorization. (auth-design.md L3)")
	devSuperUser := flag.Bool("dev-superuser", true, "when newt-server runs from a newtron source checkout (a developer's work-in-progress repo), auto-grant the current OS user global super-user across all networks — a local-dev convenience. No effect for an installed/production binary (one not running from a repo). Set false to opt out even inside a checkout. Only effective with --enforce-authorization. (auth-design.md L3)")
	enforceWriteControl := flag.Bool("enforce-write-control", false, "require a per-network write-control reservation for every executing mutation on the newtron engine: a caller must POST .../control/request before any write, else 409. Default-closed when on (a write with no holder is refused). Off (default) keeps the reservation endpoints working but enforcement inert, so existing clients that don't claim are unchanged.")
	specWatch := flag.Bool("spec-watch", false, "watch every registered network's network directory for file changes on the newtron engine; on settled change (1s debounce) automatically reload the network so revoked grants take effect without an explicit /reload call. Off (default) preserves pre-watcher behavior. (auth-design.md L6)")
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
	if *superUsers == "" {
		*superUsers = os.Getenv("NEWTRON_SUPER_USERS")
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

	// auth-design.md L2b/L2c: build the PAM authenticator and session-key store
	// up front — before the engine servers — because the engines reach each
	// other through this same PAM-gated listener (the newtron→newtlab and
	// newtrun→newtlab port resolvers, and newtlab→newtron spec reads are all
	// loopback HTTP). Under PAM those internal calls carry no user credential, so
	// PAMMiddleware would 401 them. We mint one process-lifetime service key for
	// the internal "newt-server" identity and hand it to every internal client;
	// the identity is a global super-user so the server's own infrastructure
	// calls are never blocked by a network's user-facing authorization. Without
	// PAM there is no gate: the key is empty (a no-op on the clients).
	var pamAuth httputil.Authenticator
	if *authPAMService != "" {
		pamAuth = &pamauth.PAMAuthenticator{ServiceName: *authPAMService}
	}
	var sessionKeys *sessionkey.Store
	if pamAuth != nil && *sessionKeyTTL >= 0 {
		ttl := *sessionKeyTTL
		if ttl == 0 {
			ttl = sessionkey.DefaultTTL
		}
		sessionKeys = sessionkey.NewStore(ttl)
	}
	const internalServiceUser = "newt-server"
	serviceKey := ""
	if sessionKeys != nil {
		serviceKey, err = sessionKeys.MintService(internalServiceUser)
		if err != nil {
			logger.Fatalf("minting internal service key: %v", err)
		}
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
	newtlabClient := newtlabclient.New("http://"+*listen, newtlabclient.WithBearer(serviceKey))
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

	platforms, err := spec.LoadPlatformsFromDir(*platformsBase)
	if err != nil {
		logger.Fatalf("loading platforms from %s: %v", *platformsBase, err)
	}
	if err := netpkg.ResolvePlatformSecrets(platforms, store); err != nil {
		logger.Fatalf("resolving platform secrets: %v", err)
	}
	if len(platforms) == 0 {
		logger.Printf("platforms: no platforms loaded from %s (empty or missing dir)", *platformsBase)
	} else {
		names := make([]string, 0, len(platforms))
		for n := range platforms {
			names = append(names, n)
		}
		sort.Strings(names)
		logger.Printf("platforms: loaded %d from %s: %v", len(platforms), *platformsBase, names)
	}

	// Global super-users: the explicit --super-users / env list, plus — when
	// this binary is running from a newtron source checkout — the developer's
	// own OS user, so any instance they spin up from their work-in-progress repo
	// grants them super-user without hand-listing themselves. Never fires for an
	// installed/production binary (not in a repo); opt out with --dev-superuser=false.
	globalSuperUsers := parseCommaList(*superUsers)
	if *devSuperUser {
		if u := devRepoUser(); u != "" && !slices.Contains(globalSuperUsers, u) {
			globalSuperUsers = append(globalSuperUsers, u)
			logger.Printf("dev mode: running from a newtron source checkout — auto-granting OS user %q global super-user (disable with --dev-superuser=false)", u)
		}
	}
	// The internal service identity (its key minted above) is a global
	// super-user: the server's own cross-engine infrastructure calls must never
	// be blocked by a network's user-facing authorization. Only meaningful under
	// PAM (serviceKey is empty otherwise).
	if serviceKey != "" && !slices.Contains(globalSuperUsers, internalServiceUser) {
		globalSuperUsers = append(globalSuperUsers, internalServiceUser)
	}

	newtronSrv := newtronapi.NewServer(newtronapi.Config{
		Logger:               logger,
		IdleTimeout:          *idleTimeout,
		PortResolver:         newtronPortResolver,
		NetworksBase:         *networksBase,
		AuditCallerHeader:    *auditCallerHeader,
		UnixSocketPath:       *unixSocket,
		SecretStore:          store,
		Platforms:            platforms,
		AuditLogPath:         *auditLog,
		EnforceAuthorization: *enforceAuthz,
		GlobalSuperUsers:     globalSuperUsers,
		EnforceWriteControl:  *enforceWriteControl,
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
	// binds id "alice-lab" to a dir under one of the templates here).
	// In that case Server.RegisterNetwork is idempotent on a matching
	// dir, so the deploy path doesn't conflict with the pre-registered
	// template slot.
	discoverAndRegisterNetworks(newtronSrv, *networksBase, logger)
	// newtrun reaches newtlab via HTTP (§27 — newtlab owns LabState).
	// In the composed binary the call is an in-process loopback to
	// the newtlab handler mounted on the same mux; in standalone
	// newtrun-server it's cross-process. Either way newtrun's runner
	// stays a client of newtlab, never a co-writer.
	newtrunSrv := newtrunapi.NewServer(newtrunapi.Config{
		NetworksBase:  *networksBase,
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
		NetworksBase: *networksBase,
		Logger:       logger,
		NewtronClientFor: func(networkID string) newtlab.SpecClient {
			return newtronclient.New(newtronURL, networkID, newtronclient.WithBearer(serviceKey))
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

// parseCommaList splits a comma-separated flag value into trimmed, non-empty
// entries (e.g. "--super-users alice, svc-admin" → ["alice","svc-admin"]).
func parseCommaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// newtronModulePath is the go.mod module line that identifies a directory as the
// root of a newtron source checkout (used to detect a developer's WIP repo).
const newtronModulePath = "github.com/aldrin-isaac/newtron"

// devRepoUser returns the current OS user's name when newt-server is running from
// a newtron source checkout — a developer's work-in-progress repo — and "" when
// it is not (an installed/production binary) or the user can't be determined.
// This is the signal for the dev-convenience auto-grant of global super-user.
func devRepoUser() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if findNewtronRepoRoot(filepath.Dir(exe)) == "" {
		return ""
	}
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Username)
}

// findNewtronRepoRoot walks up from dir to the nearest ancestor that is a newtron
// git checkout (has a .git entry and a go.mod declaring the newtron module),
// returning that root or "" if none — i.e. the binary is not under the source repo.
func findNewtronRepoRoot(dir string) string {
	for {
		if isNewtronRepoRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isNewtronRepoRoot reports whether dir is a newtron checkout root: it has a .git
// entry (dir for a normal clone, file for a worktree) and a go.mod whose module
// line is the newtron module path.
func isNewtronRepoRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module"); ok {
			return strings.TrimSpace(rest) == newtronModulePath
		}
	}
	return false
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
