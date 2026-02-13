package newtlab

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/newtron-network/newtron/pkg/version"
)

// sshCommand creates an exec.Cmd for running a command on a remote host via SSH.
// Standard options (StrictHostKeyChecking=no, ConnectTimeout=10) are always included.
func sshCommand(hostIP string, remoteCmd string) *exec.Cmd {
	return exec.Command("ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		hostIP,
		remoteCmd,
	)
}

// parseUname parses "uname -s -m" output to Go's GOOS/GOARCH.
// Examples: "Linux x86_64" → ("linux", "amd64"), "Linux aarch64" → ("linux", "arm64"),
// "Darwin arm64" → ("darwin", "arm64").
func parseUname(output string) (goos, goarch string, err error) {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("unexpected uname output: %q", output)
	}

	switch strings.ToLower(fields[0]) {
	case "linux":
		goos = "linux"
	case "darwin":
		goos = "darwin"
	default:
		return "", "", fmt.Errorf("unsupported OS: %q", fields[0])
	}

	switch fields[1] {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported architecture: %q", fields[1])
	}

	return goos, goarch, nil
}

// detectRemoteArch runs "uname -s -m" on a remote host via SSH.
func detectRemoteArch(hostIP string) (goos, goarch string, err error) {
	cmd := sshCommand(hostIP, "uname -s -m")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("detect remote arch on %s: %w", hostIP, err)
	}
	return parseUname(stdout.String())
}

// newtlinkBinaryName returns the platform-specific newtlink binary name.
func newtlinkBinaryName(goos, goarch string) string {
	return fmt.Sprintf("newtlink-%s-%s", goos, goarch)
}

// findNewtlinkBinary locates the cross-compiled newtlink for the given target.
// Search order:
//  1. $NEWTLAB_BIN_DIR/{newtlink-goos-goarch}
//  2. {dir-of-current-executable}/{newtlink-goos-goarch}
//  3. ~/.newtlab/bin/{newtlink-goos-goarch}
func findNewtlinkBinary(goos, goarch string) (string, error) {
	name := newtlinkBinaryName(goos, goarch)

	// 1. Environment override
	if binDir := os.Getenv("NEWTLAB_BIN_DIR"); binDir != "" {
		p := filepath.Join(binDir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// 2. Next to current executable
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// 3. ~/.newtlab/bin/
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".newtlab", "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("newtlink binary %q not found (set $NEWTLAB_BIN_DIR or run 'make install')", name)
}

// uploadNewtlink uploads the newtlink binary to a remote host if needed.
// It skips the upload if the remote version matches the local version.
// Returns the remote binary path.
func uploadNewtlink(hostIP string) (string, error) {
	remotePath := "~/.newtlab/bin/newtlink"

	// Check if remote version matches local
	checkCmd := fmt.Sprintf("%s --version 2>/dev/null", remotePath)
	cmd := sshCommand(hostIP, checkCmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil {
		// Parse remote version: "newtlink <version> (<commit>)"
		remoteOut := strings.TrimSpace(stdout.String())
		localVersion := fmt.Sprintf("newtlink %s (%s)", version.Version, version.GitCommit)
		if remoteOut == localVersion {
			return remotePath, nil
		}
	}

	// Detect remote architecture
	goos, goarch, err := detectRemoteArch(hostIP)
	if err != nil {
		return "", err
	}

	// Find local binary
	localPath, err := findNewtlinkBinary(goos, goarch)
	if err != nil {
		return "", err
	}

	// Create remote directory
	mkdirCmd := sshCommand(hostIP, "mkdir -p ~/.newtlab/bin")
	if out, err := mkdirCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create remote bin dir on %s: %w\n%s", hostIP, err, out)
	}

	// Upload via scp
	scpCmd := exec.Command("scp", "-o", "StrictHostKeyChecking=no", localPath, hostIP+":~/.newtlab/bin/newtlink")
	if out, err := scpCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("upload newtlink to %s: %w\n%s", hostIP, err, out)
	}

	// Make executable
	chmodCmd := sshCommand(hostIP, "chmod +x ~/.newtlab/bin/newtlink")
	if out, err := chmodCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("chmod newtlink on %s: %w\n%s", hostIP, err, out)
	}

	return remotePath, nil
}
