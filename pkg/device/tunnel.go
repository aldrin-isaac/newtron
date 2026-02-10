package device

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
	localAddr string // "127.0.0.1:<port>"
	sshClient *ssh.Client
	listener  net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewSSHTunnel dials SSH on host:port and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port is 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
	if port == 0 {
		port = 22
	}
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		// Lab/test environment — production would verify host keys.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s@%s: %w", user, addr, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("local listen: %w", err)
	}

	t := &SSHTunnel{
		localAddr: listener.Addr().String(),
		sshClient: sshClient,
		listener:  listener,
		done:      make(chan struct{}),
	}

	t.wg.Add(1)
	go t.acceptLoop()

	return t, nil
}

// LocalAddr returns the local address (e.g. "127.0.0.1:54321") that forwards
// to Redis inside the SSH host.
func (t *SSHTunnel) LocalAddr() string {
	return t.localAddr
}

// Close stops the listener, closes the SSH connection, and waits for
// all forwarding goroutines to finish.
func (t *SSHTunnel) Close() error {
	close(t.done)
	t.listener.Close()
	// Close SSH client first to tear down all forwarded connections,
	// unblocking any io.Copy goroutines waiting on remote reads.
	t.sshClient.Close()
	t.wg.Wait()
	return nil
}

func (t *SSHTunnel) acceptLoop() {
	defer t.wg.Done()
	for {
		local, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
				continue
			}
		}
		t.wg.Add(1)
		go t.forward(local)
	}
}

// SSHClient returns the underlying ssh.Client for opening command sessions.
// Used by newtest's verifyPingExecutor and sshCommandExecutor to run commands
// inside the device (e.g., "ping", "show interfaces status") via ssh.Session.
func (t *SSHTunnel) SSHClient() *ssh.Client { return t.sshClient }

// ExecCommand runs a command on the remote device via SSH and returns the combined output.
// The SSH session is created per-call (stateless).
func (t *SSHTunnel) ExecCommand(cmd string) (string, error) {
	session, err := t.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), fmt.Errorf("SSH exec '%s': %w", cmd, err)
	}
	return string(output), nil
}

func (t *SSHTunnel) forward(local net.Conn) {
	defer t.wg.Done()
	defer local.Close()

	remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
	if err != nil {
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(remote, local)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(local, remote)
		done <- struct{}{}
	}()
	<-done
}
