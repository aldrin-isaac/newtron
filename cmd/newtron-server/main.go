package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtser"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19080", "listen address — loopback-only by default since newtser fronts external traffic on :18080")
	specDir := flag.String("spec-dir", "", "spec directory to auto-register as 'default' network")
	netID := flag.String("net-id", "default", "network ID for auto-registered spec directory")
	idleTimeout := flag.Duration("idle-timeout", 0, "SSH connection idle timeout (default 5m, negative to disable caching)")
	newtserURL := flag.String("newtser", "", "register with newtser at this URL (e.g., http://127.0.0.1:18080); empty = standalone, no registration")
	flag.Parse()

	logger := log.New(os.Stderr, "newtron-server: ", log.LstdFlags|log.Lmsgprefix)

	srv := api.NewServer(logger, *idleTimeout)

	if *specDir != "" {
		if err := srv.RegisterNetwork(*netID, *specDir); err != nil {
			logger.Fatalf("failed to register network '%s' from %s: %v", *netID, *specDir, err)
		}
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional: register with newtser. The keepalive goroutine retries
	// on failure, so newtser-not-up-yet at startup is fine — we'll
	// reconnect when it comes online. Close() sends a best-effort
	// deregister during graceful shutdown.
	var registration *newtser.Registration
	if *newtserURL != "" {
		registration = newtser.Register(ctx, newtser.Registration{
			URL:      *newtserURL,
			Name:     "newtron",
			Version:  "v1",
			Upstream: "http://" + *addr,
			Logger:   logger,
		})
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(*addr)
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
