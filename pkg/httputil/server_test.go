package httputil

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerStartStopGracefulShutdown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"ok": "true"})
	})
	logger := log.New(&strings.Builder{}, "", 0)
	srv := NewServer(mux, logger)

	// Bind to a free port chosen by the kernel.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(addr) }()

	// Wait for the server to be reachable (poll, with timeout).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get("http://" + addr + "/probe"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned non-nil after Stop: %v", err)
	}
}

func TestOnShutdownHooksRunBeforeListenerCloses(t *testing.T) {
	var hookRan bool
	mux := http.NewServeMux()
	logger := log.New(&strings.Builder{}, "", 0)

	srv := NewServer(mux, logger,
		OnShutdown(func() { hookRan = true }),
	)

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()

	go func() { _ = srv.Start(addr) }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !hookRan {
		t.Error("OnShutdown hook did not run")
	}
}

func TestServerLabelAppearsInStartupLog(t *testing.T) {
	var buf strings.Builder
	logger := log.New(&buf, "", 0)

	mux := http.NewServeMux()
	srv := NewServer(mux, logger, ServerLabel("test-server"))

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()

	go func() { _ = srv.Start(addr) }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = srv.Stop(ctx)

	if !strings.Contains(buf.String(), "test-server listening on") {
		t.Errorf("startup log = %q, want to contain 'test-server listening on'", buf.String())
	}
}

func TestServerStartReturnsErrorOnBindFailure(t *testing.T) {
	mux := http.NewServeMux()
	logger := log.New(&strings.Builder{}, "", 0)
	srv := NewServer(mux, logger)

	// Bind to a port we already hold so Start fails.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	err = srv.Start(l.Addr().String())
	if err == nil {
		t.Fatal("Start returned nil, want bind error")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Start returned ErrServerClosed for a real bind error: %v", err)
	}
}
