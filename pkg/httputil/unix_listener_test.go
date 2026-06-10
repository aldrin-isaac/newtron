package httputil

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServer_UnixSocketListener_Lifecycle pins the L1 Unix-socket
// path end-to-end: Start binds the socket, requests over the socket
// reach the handler with PeerCred on context, Stop cleans up the
// socket file.
func TestServer_UnixSocketListener_Lifecycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "newtron-test.sock")

	gotPeerCred := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pc := PeerCredFromContext(r.Context()); pc != nil {
			gotPeerCred = true
		}
		w.WriteHeader(http.StatusOK)
	})

	s := NewServer(handler, log.New(io.Discard, "", 0),
		UnixSocketPath(sockPath),
		ServerLabel("test"),
	)

	tcpAddr := pickFreeAddr(t)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.Start(tcpAddr)
	}()

	// Wait for the Unix socket to exist — Start binds it before
	// blocking on TCP, but the goroutine schedule may take a
	// moment to surface it. Bounded by a short deadline so the
	// test fails loudly if startup is broken.
	waitForFile(t, sockPath, 2*time.Second)

	// Issue a request through the Unix socket using a custom
	// dialer; verify the handler ran and saw verified peer creds.
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}}
	resp, err := client.Get("http://unix/x")
	if err != nil {
		t.Fatalf("Unix-socket GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !gotPeerCred {
		t.Error("handler did not see PeerCred on context for Unix-socket request")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop: %v", err)
	}
	// Drain the Start goroutine's return.
	select {
	case err := <-serveErr:
		if err != nil {
			t.Errorf("Start returned err after Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Start did not return within 2s of Stop")
	}

	// The socket file should be gone after Stop — leaving it
	// behind would break the next Start (or require operators to
	// `rm` between runs). The leading os.Remove in Start handles
	// the worst case but Stop is the canonical cleanup path.
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file still exists after Stop: %v", err)
	}
}

// TestServer_UnixSocketDisabled_NoListener pins the L1 disabled
// state: UnixSocketPath empty means TCP only — no socket file is
// created, the connContext hook isn't even installed.
func TestServer_UnixSocketDisabled_NoListener(t *testing.T) {
	gotPeerCred := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pc := PeerCredFromContext(r.Context()); pc != nil {
			gotPeerCred = true
		}
		w.WriteHeader(http.StatusOK)
	})

	s := NewServer(handler, log.New(io.Discard, "", 0), ServerLabel("test"))
	tcpAddr := pickFreeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start(tcpAddr) }()
	waitForTCP(t, tcpAddr, 2*time.Second)

	resp, err := http.Get("http://" + tcpAddr + "/x")
	if err != nil {
		t.Fatalf("TCP GET: %v", err)
	}
	resp.Body.Close()
	if gotPeerCred {
		t.Error("TCP request unexpectedly carried PeerCred — connContext should not be installed when UnixSocketPath is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	<-serveErr
}

// TestServer_UnixSocketStaleFile_GetsReplaced pins that Start
// removes a stale socket file from a previous run so the bind
// succeeds. Without this, operators have to rm the socket between
// crashes — annoying and easy to forget. The end-state assertion is
// "a Unix-socket request through sockPath succeeds" — proves the
// stale file was replaced by a working socket.
func TestServer_UnixSocketStaleFile_GetsReplaced(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "stale.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := NewServer(handler, log.New(io.Discard, "", 0),
		UnixSocketPath(sockPath), ServerLabel("test"))
	tcpAddr := pickFreeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start(tcpAddr) }()

	// Poll the socket by trying actual Unix-socket connections.
	// Once one succeeds, the stale regular file is gone and a real
	// socket is in its place.
	waitForUnixSocket(t, sockPath, 2*time.Second)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}}
	resp, err := client.Get("http://unix/x")
	if err != nil {
		t.Fatalf("Unix-socket GET (post-stale): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Stop(ctx)
	<-serveErr
}

// pickFreeAddr returns an OS-assigned free TCP address as host:port.
// Used by the Unix listener tests above because they also bind a TCP
// listener for the http.Server.
func pickFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen :0: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// waitForFile polls for a path to exist, bounded by deadline.
func waitForFile(t *testing.T, path string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within %s", path, deadline)
}

// waitForUnixSocket polls for a Unix socket to accept a connection,
// bounded by deadline.
func waitForUnixSocket(t *testing.T, path string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		c, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Unix socket at %s not ready within %s", path, deadline)
}

// waitForTCP polls for a TCP address to accept a connection, bounded
// by deadline.
func waitForTCP(t *testing.T, addr string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		if !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("unexpected dial err: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("TCP listener at %s not ready within %s", addr, deadline)
}
