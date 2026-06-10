// Package main is the standalone newtrun-server entry point.
//
// Use this binary when iterating on newtrun code in isolation. For
// production / aggregated deployment, see cmd/newt-server/, which
// mounts every engine on one port.
//
// Conventions: loopback default, --listen flag for explicit
// non-loopback exposure, no built-in authentication (operators wrap
// with a reverse proxy if they need TLS or auth).
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
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// defaultListen — loopback-only; newt-server fronts external traffic on :18080.
const defaultListen = "127.0.0.1:19081"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	suitesBase := flag.String("suites-base", "newtrun/suites", "directory containing suite subdirectories")
	newtlabServer := flag.String("newtlab-server", "http://127.0.0.1:18080", "newtlab-server base URL; deploy/destroy/status route through this HTTP surface (§27 — newtlab owns LabState). Empty disables newtlab calls (CLI-only / real-hardware deployments).")
	tlsCert := flag.String("tls-cert", "", "PEM-encoded TLS certificate for the TCP listener (also used as client cert when calling newtlab-server). Empty disables TLS — plain HTTP. (auth-design.md L2a)")
	tlsKey := flag.String("tls-key", "", "PEM-encoded private key for --tls-cert. (auth-design.md L2a)")
	tlsCA := flag.String("tls-ca", "", "PEM-encoded CA bundle used both to verify incoming peer client certs AND to verify newtlab-server's cert when calling it. Empty: TLS-only (no mTLS). (auth-design.md L2a)")
	authPAMService := flag.String("auth-pam-service", "", "PAM service name under /etc/pam.d/ that authenticates TCP user requests via HTTP Basic. Empty disables PAM authentication. (auth-design.md L2b)")
	flag.Parse()

	logger := log.New(os.Stderr, "newtrun-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	// auth-design.md L2a: server cert + identical client cert
	// (typical service-mesh pattern). nil from either Load means the
	// corresponding flag set was empty — L2a disabled on that
	// direction.
	serverTLS, err := httputil.LoadServerTLSConfig(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		logger.Fatalf("server TLS: %v", err)
	}
	clientTLS, err := httputil.LoadClientTLSConfig(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		logger.Fatalf("client TLS: %v", err)
	}

	// auth-design.md L2b: install PAM authenticator when configured.
	var pamAuth httputil.Authenticator
	if *authPAMService != "" {
		pamAuth = &pamauth.PAMAuthenticator{ServiceName: *authPAMService}
	}

	// Compose the newtlab HTTP client at the entry point — newtrun's
	// api package sees only the LabClient contract (pkg/newtrun.
	// LabClient), the client package supplies the concrete satisfier.
	// Empty --newtlab-server leaves the client nil; Runner.Run rejects
	// deploy in that case with a clear error.
	cfg := api.Config{
		SuitesBase:    *suitesBase,
		Logger:        logger,
		TLSConfig:     serverTLS,
		Authenticator: pamAuth,
	}
	if *newtlabServer != "" {
		cfg.NewtlabClient = newtlabclient.New(*newtlabServer, newtlabclient.WithTLS(clientTLS))
	}
	srv := api.NewServer(cfg)

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

// warnIfNonLoopback validates the listen address shape and emits an explicit
// acknowledgment in the startup log when the operator binds to a
// non-loopback interface. v0 has no built-in authentication; non-loopback
// exposure is the operator's deliberate choice.
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
