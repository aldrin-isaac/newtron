// Package main is the standalone newtlab-server entry point.
//
// Use this binary when iterating on newtlab code in isolation. For
// production / aggregated deployment, see cmd/newt-server/, which
// mounts every engine on one port.
//
// Conventions: loopback default, --listen flag for explicit
// non-loopback exposure, no built-in authentication (operators wrap
// with a reverse proxy if they need TLS or auth).
//
// The server exposes one HTTP endpoint per CLI lifecycle command —
// deploy, destroy, status, provision, start, stop — plus an SSE stream
// for deploy progress. See docs/newtlab/api.md for the route table.
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

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	"github.com/aldrin-isaac/newtron/pkg/newtlab/api"
	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// defaultListen — loopback-only; newt-server fronts external traffic on :18080.
const defaultListen = "127.0.0.1:19082"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	topologiesBase := flag.String("topologies-base", "newtrun/topologies", "directory containing topology subdirectories")
	newtronServer := flag.String("newtron-server", "http://127.0.0.1:18080", "newtron-server URL (newtlab consumes specs via /newtron/v1)")
	flag.Parse()

	logger := log.New(os.Stderr, "newtlab-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	// newtlab consumes spec data via newtron's HTTP API (§27). Each lab
	// gets its own newtron client configured for its own network ID
	// (#116 — the network ID equals the lab name).
	newtronURL := *newtronServer

	srv := api.NewServer(api.Config{
		TopologiesBase: *topologiesBase,
		Logger:         logger,
		NewtronClientFor: func(networkID string) newtlab.SpecClient {
			return newtronclient.New(newtronURL, networkID)
		},
	})

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

// warnIfNonLoopback validates the listen address shape and emits an
// explicit acknowledgment in the startup log when the operator binds to
// a non-loopback interface. v0 has no built-in authentication;
// non-loopback exposure is the operator's deliberate choice.
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
