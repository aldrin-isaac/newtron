package newtlab

import (
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// WaitForSSH polls SSH connectivity to host:port with the given credentials.
// Returns nil when SSH login succeeds, or error if timeout is reached.
// Polls every 5 seconds.
func WaitForSSH(host string, port int, user, pass string, timeout time.Duration) error {
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
		client, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		// Verify we can run a command
		session, err := client.NewSession()
		if err != nil {
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		_, err = session.CombinedOutput("echo ready")
		session.Close()
		client.Close()

		if err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("newtlab: SSH timeout after %s for %s", timeout, addr)
}

// BootstrapNetwork connects to the serial console and prepares the VM for SSH access.
//
// Steps:
//  1. Wait for the login prompt (VM may still be booting).
//  2. Log in using consoleUser/consolePass (the user baked into the image).
//  3. Bring up eth0 with DHCP (QEMU user-mode networking requires this).
//  4. If sshUser differs from consoleUser, create the SSH user with sudo + bash access.
//  5. Log out.
func BootstrapNetwork(consoleHost string, consolePort int, consoleUser, consolePass, sshUser, sshPass string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Poll until we can connect to the serial console
	var conn net.Conn
	for time.Now().Before(deadline) {
		var err error
		conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", consoleHost, consolePort), 5*time.Second)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if conn == nil {
		return fmt.Errorf("newtlab: serial console connect timeout for %s:%d", consoleHost, consolePort)
	}
	defer conn.Close()

	// Helper to read from console until a string appears or timeout
	readUntil := func(expected string, timeout time.Duration) (string, error) {
		var buf []byte
		dl := time.Now().Add(timeout)
		for time.Now().Before(dl) {
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			tmp := make([]byte, 4096)
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
