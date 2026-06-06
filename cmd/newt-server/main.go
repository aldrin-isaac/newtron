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
	newtlabapi "github.com/aldrin-isaac/newtron/pkg/newtlab/api"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	newtronapi "github.com/aldrin-isaac/newtron/pkg/newtron/api"
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
	topologiesBase := flag.String("topologies-base", "newtrun/topologies", "directory containing topology subdirectories (newtrun + newtlab)")
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
	newtronSrv := newtronapi.NewServer(logger, *idleTimeout, newtronPortResolver)
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
		SuitesBase:     *suitesBase,
		TopologiesBase: *topologiesBase,
		Logger:         logger,
		NewtlabClient:  newtlabClient,
	})
	// newtlab consumes spec data via newtron (§27 — newtron owns spec
	// files). In the composed binary newtlab and newtron share a process,
	// so we wire an in-process accessor instead of looping back through
	// HTTP. The loopback path deadlocked the NetworkActor when one of
	// newtron's actor closures triggered the cycle — see issue #97.
	// Split-process deployments (bin/newtlab-server + bin/newtron-server)
	// continue to use the HTTP client.
	newtlabSrv := newtlabapi.NewServer(newtlabapi.Config{
		TopologiesBase: *topologiesBase,
		Logger:         logger,
		NewtronClient:  &inprocSpecClient{server: newtronSrv, netID: *netID},
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
