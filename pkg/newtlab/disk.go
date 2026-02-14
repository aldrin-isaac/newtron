package newtlab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CreateOverlay creates a QCOW2 copy-on-write overlay backed by baseImage.
// The overlay is written to overlayPath.
func CreateOverlay(baseImage, overlayPath string) error {
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2",
		"-b", baseImage,
		"-F", "qcow2",
		overlayPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("newtlab: create overlay %s: %w\n%s", overlayPath, err, output)
	}
	return nil
}

// CreateOverlayRemote creates a QCOW2 overlay on a remote host via SSH.
// Paths starting with ~/ are expanded by the remote shell.
func CreateOverlayRemote(baseImage, overlayPath, hostIP string) error {
	remoteCmd := fmt.Sprintf("qemu-img create -f qcow2 -b %s -F qcow2 %s",
		shellQuote(baseImage), shellQuote(overlayPath))
	cmd := sshCommand(hostIP, remoteCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("newtlab: create remote overlay %s on %s: %w\n%s",
			overlayPath, hostIP, err, output)
	}
	return nil
}

// RemoveOverlay deletes an overlay disk file.
func RemoveOverlay(overlayPath string) error {
	if err := os.Remove(overlayPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("newtlab: remove overlay %s: %w", overlayPath, err)
	}
	return nil
}

// setupRemoteStateDir creates the lab state directory structure on a remote host.
func setupRemoteStateDir(labName, hostIP string) error {
	stateDir := shellQuote(fmt.Sprintf("~/.newtlab/labs/%s", labName))
	mkdirCmd := fmt.Sprintf("mkdir -p %s/disks %s/qemu %s/logs", stateDir, stateDir, stateDir)
	cmd := sshCommand(hostIP, mkdirCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("newtlab: create remote state dir on %s: %w\n%s", hostIP, err, out)
	}
	return nil
}

// cleanupRemoteStateDir removes the lab state directory from a remote host.
func cleanupRemoteStateDir(labName, hostIP string) error {
	stateDir := shellQuote(fmt.Sprintf("~/.newtlab/labs/%s", labName))
	cmd := sshCommand(hostIP, fmt.Sprintf("rm -rf %s", stateDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("newtlab: remove remote state dir on %s: %w\n%s", hostIP, err, out)
	}
	return nil
}

// expandHome replaces a leading ~/ with the user's home directory.
// Uses the cached home directory from getHomeDir().
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := getHomeDir()
		if err != nil {
			return path // leave unexpanded if home dir unavailable
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// unexpandHome replaces a leading $HOME/ with ~/ for use in remote SSH commands.
// Uses the cached home directory from getHomeDir().
func unexpandHome(path string) string {
	home, err := getHomeDir()
	if err != nil {
		return path // leave as-is if home dir unavailable
	}
	if strings.HasPrefix(path, home+"/") {
		return "~/" + path[len(home)+1:]
	}
	return path
}
