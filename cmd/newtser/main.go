// Package main is the newtser HTTP server entry point.
//
// newtser is the front-door HTTP server for the newtron-project
// service set. Backend servers (newtron-server, newtrun-server,
// newtlab-server, and any future newtron-project app) register with
// newtser on startup; newtser routes incoming requests to the right
// backend by the first path segment in the URL.
//
// Default bind: 127.0.0.1:18080 (loopback; non-loopback exposure
// warns). Backends bind their own loopback ports (:19080, :19081,
// :19082 by convention) and connect outbound to newtser to register.
//
// See docs/newtser/hld.md for the design and docs/newtser/api.md for
// the endpoint reference.
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

	"github.com/aldrin-isaac/newtron/pkg/newtser"
)

const defaultListen = "127.0.0.1:18080"

func main() {
	listen := flag.String("listen", defaultListen, "listen address; loopback default; non-loopback requires explicit value")
	evictionInterval := flag.Duration("eviction-interval", 30*time.Second, "how often to scan for stale registrations")
	evictionMaxAge := flag.Duration("eviction-max-age", 90*time.Second, "registrations older than this are evicted")
	flag.Parse()

	logger := log.New(os.Stderr, "newtser: ", log.LstdFlags|log.Lmsgprefix)

	if err := warnIfNonLoopback(*listen, logger); err != nil {
		logger.Fatalf("invalid --listen %q: %v", *listen, err)
	}

	srv := newtser.NewServer(newtser.Config{
		EvictionInterval: *evictionInterval,
		EvictionMaxAge:   *evictionMaxAge,
		Logger:           logger,
	})

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
	logger.Printf("WARNING: --listen=%s binds to a non-loopback address; newtser has no built-in authentication.", listen)
	logger.Printf("WARNING: wrap with a reverse proxy (TLS + auth) before exposing on a shared network.")
	return nil
}
