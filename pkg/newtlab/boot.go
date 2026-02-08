package newtlab

import (
	"fmt"
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
