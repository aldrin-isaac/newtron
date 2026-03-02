package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/api"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	specDir := flag.String("spec-dir", "", "spec directory to auto-register as 'default' network")
	netID := flag.String("net-id", "default", "network ID for auto-registered spec directory")
	flag.Parse()

	logger := log.New(os.Stderr, "newtron-api: ", log.LstdFlags|log.Lmsgprefix)

	srv := api.NewServer(logger)

	if *specDir != "" {
		if err := srv.RegisterNetwork(*netID, *specDir); err != nil {
			logger.Fatalf("failed to register network '%s' from %s: %v", *netID, *specDir, err)
		}
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(*addr)
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
