// Package main is the newtlab HTTP server entry point.
//
// newtlab-server hosts the long-lived HTTP API that the newtcon browser
// frontend consumes to deploy and observe lab topologies. It mirrors
// newtrun-server's shape: loopback default, --listen flag for explicit
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

	"github.com/aldrin-isaac/newtron/pkg/newtlab/api"
)

const defaultListen = "127.0.0.1:18082"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	topologiesBase := flag.String("topologies-base", "newtrun/topologies", "directory containing topology subdirectories")
	flag.Parse()

	logger := log.New(os.Stderr, "newtlab-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	srv := api.NewServer(api.Config{
		TopologiesBase: *topologiesBase,
		Logger:         logger,
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
