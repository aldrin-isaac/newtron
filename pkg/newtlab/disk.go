package newtlab

import (
	"fmt"
	"os"
	"os/exec"
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

// RemoveOverlay deletes an overlay disk file.
func RemoveOverlay(overlayPath string) error {
	if err := os.Remove(overlayPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("newtlab: remove overlay %s: %w", overlayPath, err)
	}
	return nil
}
