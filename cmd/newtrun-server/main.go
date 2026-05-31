// Package main is the newtrun HTTP server entry point.
//
// newtrun-server hosts the long-lived HTTP API that the newtcon browser
// frontend and the newtrun CLI consume. It mirrors newtron-server's shape:
// loopback default, --listen flag for explicit non-loopback exposure, no
// built-in authentication (operators wrap with a reverse proxy if they need
// TLS or auth).
//
// In PR 1 the server exposes read-only endpoints over existing newtrun
// state files. PR 2 adds server-side scenario execution; PR 3 adds the
// inline compose-and-run write surface.
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

	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
	"github.com/aldrin-isaac/newtron/pkg/newtser"
)

// defaultListen — loopback-only since newtser fronts external traffic on :18080.
const defaultListen = "127.0.0.1:19081"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	suitesBase := flag.String("suites-base", "newtrun/suites", "directory containing suite subdirectories")
	topologiesBase := flag.String("topologies-base", "newtrun/topologies", "directory containing topology subdirectories")
	newtserURL := flag.String("newtser", "", "register with newtser at this URL (e.g., http://127.0.0.1:18080); empty = standalone, no registration")
	flag.Parse()

	logger := log.New(os.Stderr, "newtrun-server: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	srv := api.NewServer(api.Config{
		SuitesBase:     *suitesBase,
		TopologiesBase: *topologiesBase,
		Logger:         logger,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional: register with newtser. Keepalive goroutine retries on
	// failure; Close() sends best-effort deregister on graceful shutdown.
	var registration *newtser.Registration
	if *newtserURL != "" {
		registration = newtser.Register(ctx, newtser.Registration{
			URL:      *newtserURL,
			Name:     "newtrun",
			Version:  "v1",
			Upstream: "http://" + *listen,
			Logger:   logger,
		})
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(*listen)
	}()

	select {
	case err := <-errCh:
		logger.Fatalf("server error: %v", err)
	case <-ctx.Done():
		logger.Println("shutting down...")
		if registration != nil {
			registration.Close()
		}
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
