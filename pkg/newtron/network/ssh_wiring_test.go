package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// capturingSSHServer is an in-process SSH server on 127.0.0.1 that records the
// (user, password) of the first authentication attempt. It lets the wiring test
// observe exactly which credentials a Device offers when it dials — the link the
// resolution unit tests never reach (they stop at resolveSSHLogin's return value,
// not the actual SSH connection).
type capturingSSHServer struct {
	port   int
	authed chan struct{}
	mu     sync.Mutex
	user   string
	pass   string
}

func newCapturingSSHServer(t *testing.T) *capturingSSHServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	s := &capturingSSHServer{authed: make(chan struct{}, 1)}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			s.mu.Lock()
			s.user, s.pass = c.User(), string(pass)
			s.mu.Unlock()
			select {
			case s.authed <- struct{}{}:
			default:
			}
			// Accept: the handshake completes so the Device builds its tunnel. The
			// Device then fails reading Redis behind the tunnel — but the login is
			// already captured, which is all this test asserts on.
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					_ = conn.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					_ = nc.Reject(ssh.Prohibited, "test server")
				}
				_ = sc.Close()
			}()
		}
	}()
	return s
}

// fixedPortResolver satisfies sonic.PortResolver, returning one port so the
// Device dials the in-process test server instead of a real device port.
type fixedPortResolver struct{ port int }

func (r fixedPortResolver) SSHPort(_ context.Context, _, _ string) (int, error) {
	return r.port, nil
}

// TestSSHLoginWiring_ResolvedLoginReachesDial is the end-to-end test the
// resolution unit tests don't provide: it stands up an SSH server, builds a Node
// from a network whose login is set at NETWORK scope with a NODE override,
// connects, and asserts the server received the NODE-override credentials. This
// exercises the whole mechanism — spec → resolveSSHLogin → ResolvedNodeSpec →
// Device → NewSSHTunnel → dial — so a break at ANY link fails the test:
//   - resolution not applied → dials with the wrong/empty login;
//   - override not winning → dials "netuser" instead of "nodeuser";
//   - resolved spec not passed to the Device, or the tunnel reading a stale
//     field → dials with the wrong login or never dials at all.
func TestSSHLoginWiring_ResolvedLoginReachesDial(t *testing.T) {
	srv := newCapturingSSHServer(t)

	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Network-scope login — the base; the node MUST override it.
	write("network.json", `{"version":"1.0","ssh_user":"netuser","ssh_pass":"netpass"}`)
	write("zones/amer.json", `{}`)
	// Node overrides both fields and points mgmt at the loopback host the test
	// server runs on.
	write("nodes/sw.json", `{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.1","zone":"amer","platform":"p1","ssh_user":"nodeuser","ssh_pass":"nodepass","underlay_asn":65001}`)
	write("topology.json", `{"version":"1.0","nodes":{"sw":{}},"links":[]}`)

	n, err := NewNetwork(dir, "t", fixedPortResolver{port: srv.port}, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	dev, err := n.GetNode("sw")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Authenticates (capturing the login), then fails reading Redis behind the
	// tunnel — we ignore that error and assert only on what the server saw.
	_ = dev.ConnectTransport(ctx)

	select {
	case <-srv.authed:
	case <-time.After(5 * time.Second):
		t.Fatal("SSH server never saw an auth attempt — the Device never dialed; the resolved login is not wired to the tunnel")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.user != "nodeuser" || srv.pass != "nodepass" {
		t.Errorf("device dialed as %q/%q; want the node-override login nodeuser/nodepass — the resolveSSHLogin → ResolvedNodeSpec → tunnel wiring is broken", srv.user, srv.pass)
	}
}
