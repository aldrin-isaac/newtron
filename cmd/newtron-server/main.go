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

	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
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
	flag.Parse()

	logger := log.New(os.Stderr, "newtron-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	// cmd is the composition layer: it knows which engine provides
	// the port-resolver implementation. newtron's api package sees
	// only the contract (api.PortResolver); newtlab's client package
	// supplies the concrete satisfier.
	var portResolver api.PortResolver
	if *newtlabServer != "" {
		portResolver = newtlabclient.NewPortResolver(newtlabclient.New(*newtlabServer))
	}

	srv := api.NewServer(logger, *idleTimeout, portResolver, *scaffoldRoot)

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
