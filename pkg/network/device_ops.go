package network

import (
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// requireWritable checks that a device is connected and locked for write operations.
func requireWritable(d *Device) error {
	if !d.IsConnected() {
		return util.ErrNotConnected
	}
	if !d.IsLocked() {
		return util.ErrNotLocked
	}
	return nil
}

// parseBreakoutSpeed converts a breakout mode speed suffix to SONiC speed value.
// e.g., "4x25G" -> "25000", "2x50G" -> "50000"
func parseBreakoutSpeed(mode string) string {
	parts := strings.SplitN(mode, "x", 2)
	if len(parts) != 2 {
		return ""
	}
	speedStr := strings.TrimRight(parts[1], "Gg")
	speedMap := map[string]string{
		"10":  "10000",
		"25":  "25000",
		"40":  "40000",
		"50":  "50000",
		"100": "100000",
		"200": "200000",
		"400": "400000",
	}
	if speed, ok := speedMap[speedStr]; ok {
		return speed
	}
	return ""
}
