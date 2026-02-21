package newtlab

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// GenerateLabSSHKey generates an Ed25519 key pair for passwordless lab SSH access.
// The private key is saved to dir/lab.key (mode 0600).
// Returns the private key file path and the public key in authorized_keys format.
func GenerateLabSSHKey(dir string) (keyPath, pubKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate lab SSH key: %w", err)
	}

	// MarshalPrivateKey produces the OpenSSH PEM block that ssh -i expects.
	privBlock, err := ssh.MarshalPrivateKey(priv, "newtlab")
	if err != nil {
		return "", "", fmt.Errorf("marshal lab SSH key: %w", err)
	}

	keyPath = filepath.Join(dir, "lab.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(privBlock), 0600); err != nil {
		return "", "", fmt.Errorf("write lab SSH key: %w", err)
	}

	pubSSH, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("marshal lab SSH public key: %w", err)
	}
	// MarshalAuthorizedKey includes a trailing newline; strip it and add a comment.
	pubKey = strings.TrimRight(string(ssh.MarshalAuthorizedKey(pubSSH)), "\n") + " newtlab"
	return keyPath, pubKey, nil
}

// injectSSHKeyViaSSH adds a public key to the user's authorized_keys over SSH.
// Used for host VMs where console-based injection is unavailable.
func injectSSHKeyViaSSH(host string, port int, user, pass, pubKey string) error {
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)
	if err != nil {
		return fmt.Errorf("SSH dial for key injection: %w", err)
	}
	defer client.Close()

	home := "/root"
	if user != "root" {
		home = "/home/" + user
	}
	cmd := fmt.Sprintf(
		"mkdir -p %s/.ssh && echo %s >> %s/.ssh/authorized_keys && chmod 700 %s/.ssh && chmod 600 %s/.ssh/authorized_keys",
		home, singleQuote(pubKey), home, home, home,
	)
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("SSH session for key injection: %w", err)
	}
	defer session.Close()
	if _, err := session.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("inject SSH key: %w", err)
	}
	return nil
}

// WaitForSSH polls SSH connectivity to host:port with the given credentials.
// Returns nil when SSH login succeeds, or error if timeout is reached or context
// is cancelled. Polls every 5 seconds.
func WaitForSSH(ctx context.Context, host string, port int, user, pass string, timeout time.Duration) error {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: SSH wait cancelled for %s: %w", addr, ctx.Err())
		default:
		}

		client, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("newtlab: SSH wait cancelled for %s: %w", addr, ctx.Err())
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// Verify we can run a command
		session, err := client.NewSession()
		if err != nil {
			client.Close()
			select {
			case <-ctx.Done():
				return fmt.Errorf("newtlab: SSH wait cancelled for %s: %w", addr, ctx.Err())
			case <-time.After(5 * time.Second):
			}
			continue
		}

		_, err = session.CombinedOutput("echo ready")
		session.Close()
		client.Close()

		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: SSH wait cancelled for %s: %w", addr, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}

	return fmt.Errorf("newtlab: SSH timeout after %s for %s", timeout, addr)
}

// BootstrapHostNetwork connects to the serial console of a host VM (e.g., Alpine Linux)
// and waits for the login prompt to confirm boot completion. Unlike BootstrapNetwork,
// it does not log in, run DHCP, or create users — Alpine auto-starts dhcpcd and sshd.
// SSH readiness is confirmed by the subsequent WaitForSSH() call.
func BootstrapHostNetwork(ctx context.Context, consoleHost string, consolePort int, consoleUser, consolePass string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Poll until we can connect to the serial console
	var conn net.Conn
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: host bootstrap cancelled for %s:%d: %w", consoleHost, consolePort, ctx.Err())
		default:
		}

		var err error
		conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", consoleHost, consolePort), 5*time.Second)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: host bootstrap cancelled for %s:%d: %w", consoleHost, consolePort, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if conn == nil {
		return fmt.Errorf("newtlab: serial console connect timeout for host %s:%d", consoleHost, consolePort)
	}
	defer conn.Close()

	// Wait for login prompt — confirms the VM has booted.
	// We don't actually log in; Alpine's init scripts handle dhcpcd and sshd.
	remaining := time.Until(deadline)
	if remaining < 30*time.Second {
		remaining = 30 * time.Second
	}

	var buf []byte
	tmp := make([]byte, 4096)
	dl := time.Now().Add(remaining)
	for time.Now().Before(dl) {
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if strings.Contains(string(buf), "login:") {
				return nil
			}
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send newline periodically to trigger prompt
				conn.Write([]byte("\r\n"))
				continue
			}
			return fmt.Errorf("newtlab: host bootstrap: serial read error: %w", err)
		}
	}

	return fmt.Errorf("newtlab: host bootstrap: timeout waiting for login prompt on %s:%d", consoleHost, consolePort)
}

// BootstrapNetwork connects to the serial console and prepares the VM for SSH access.
//
// Steps:
//  1. Wait for the login prompt (VM may still be booting).
//  2. Log in using consoleUser/consolePass (the user baked into the image).
//  3. Bring up eth0 with DHCP (QEMU user-mode networking requires this).
//  4. If sshUser differs from consoleUser, create the SSH user with sudo + bash access.
//  5. Log out.
func BootstrapNetwork(ctx context.Context, consoleHost string, consolePort int, consoleUser, consolePass, sshUser, sshPass string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Poll until we can connect to the serial console
	var conn net.Conn
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: bootstrap cancelled for %s:%d: %w", consoleHost, consolePort, ctx.Err())
		default:
		}

		var err error
		conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", consoleHost, consolePort), 5*time.Second)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("newtlab: bootstrap cancelled for %s:%d: %w", consoleHost, consolePort, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if conn == nil {
		return fmt.Errorf("newtlab: serial console connect timeout for %s:%d", consoleHost, consolePort)
	}
	defer conn.Close()

	// Helper to read from console until a string appears or timeout
	readUntil := func(expected string, timeout time.Duration) (string, error) {
		var buf []byte
		tmp := make([]byte, 4096)
		dl := time.Now().Add(timeout)
		for time.Now().Before(dl) {
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, err := conn.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), expected) {
					return string(buf), nil
				}
			}
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return string(buf), err
			}
		}
		return string(buf), fmt.Errorf("timeout waiting for %q", expected)
	}

	// Wait for login prompt (VM may still be booting)
	remaining := time.Until(deadline)
	if remaining < 30*time.Second {
		remaining = 30 * time.Second
	}

	// Send a newline to trigger prompt
	conn.Write([]byte("\r\n"))
	_, err := readUntil("login:", remaining)
	if err != nil {
		return fmt.Errorf("newtlab: bootstrap: waiting for login prompt: %w", err)
	}

	// Send console username
	conn.Write([]byte(consoleUser + "\r\n"))
	_, err = readUntil("Password:", 10*time.Second)
	if err != nil {
		return fmt.Errorf("newtlab: bootstrap: waiting for password prompt: %w", err)
	}

	// Send console password
	conn.Write([]byte(consolePass + "\r\n"))
	resp, err := readUntil("$", 15*time.Second)
	if err != nil || strings.Contains(resp, "Login incorrect") {
		return fmt.Errorf("newtlab: bootstrap: login failed (user=%s)", consoleUser)
	}

	// Bring up eth0
	conn.Write([]byte("sudo ip link set eth0 up\r\n"))
	readUntil("$", 5*time.Second)

	// Run DHCP client
	conn.Write([]byte("sudo dhclient eth0\r\n"))
	readUntil("$", 15*time.Second)

	// Create SSH user if it differs from the console user
	if sshUser != consoleUser {
		// Create user with sudo+docker access and bash shell, set password non-interactively
		cmd := fmt.Sprintf("sudo useradd -m -s /bin/bash -G sudo,docker %s 2>/dev/null; echo '%s:%s' | sudo chpasswd\r\n",
			sshUser, sshUser, sshPass)
		conn.Write([]byte(cmd))
		readUntil("$", 10*time.Second)
	}

	// Logout
	conn.Write([]byte("exit\r\n"))
	time.Sleep(500 * time.Millisecond)

	return nil
}
