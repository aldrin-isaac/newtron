// platform.go implements SONiC platform.json parsing and port validation.
package sonic

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// SonicPlatformConfig represents the parsed SONiC platform.json from a device.
// Located at /usr/share/sonic/device/<platform>/platform.json on the device.
type SonicPlatformConfig struct {
	Interfaces map[string]*PortDefinition `json:"interfaces"`
}

// PortDefinition represents a single port's capabilities from platform.json.
type PortDefinition struct {
	Index          int      `json:"index"`
	Lanes          string   `json:"lanes"`                    // Comma-separated lane numbers
	Alias          string   `json:"alias,omitempty"`          // Port alias (e.g., "fortyGigE0/0")
	DefaultSpeed   string   `json:"speed,omitempty"`          // Default speed
	SupportedSpeeds []string `json:"supported_speeds,omitempty"` // All supported speeds
	BreakoutModes  []string `json:"breakout_modes,omitempty"` // e.g., ["4x25G", "2x50G"]
}

// CreatePortConfig holds options for port creation.
type CreatePortConfig struct {
	Name        string // Port name (e.g., "Ethernet0")
	Speed       string // Speed (e.g., "100000" for 100G)
	Lanes       string // Lane assignment (must match platform.json)
	FEC         string // Forward error correction (e.g., "rs", "none")
	MTU         int    // MTU (default 9100)
	AdminStatus string // Initial admin status (default "up")
	Alias       string // Port alias
	Index       string // Port index
}

// BreakoutConfig holds options for port breakout.
type BreakoutConfig struct {
	ParentPort string // Parent port name (e.g., "Ethernet0")
	Mode       string // Breakout mode (e.g., "4x25G", "2x50G")
}

// ValidatePort checks if a port creation config is valid against this platform.
func (pc *SonicPlatformConfig) ValidatePort(cfg CreatePortConfig) error {
	if pc.Interfaces == nil {
		return fmt.Errorf("platform config has no interfaces defined")
	}

	portDef, ok := pc.Interfaces[cfg.Name]
	if !ok {
		return fmt.Errorf("port %s not defined in platform.json", cfg.Name)
	}

	// Validate lanes match platform definition
	if cfg.Lanes != "" && cfg.Lanes != portDef.Lanes {
		return fmt.Errorf("lanes %s do not match platform definition %s for port %s",
			cfg.Lanes, portDef.Lanes, cfg.Name)
	}

	// Validate speed is supported
	if cfg.Speed != "" && len(portDef.SupportedSpeeds) > 0 {
		supported := false
		for _, s := range portDef.SupportedSpeeds {
			if s == cfg.Speed {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("speed %s not supported for port %s (supported: %s)",
				cfg.Speed, cfg.Name, strings.Join(portDef.SupportedSpeeds, ", "))
		}
	}

	return nil
}

// ValidateBreakout checks if a breakout mode is valid for a port.
func (pc *SonicPlatformConfig) ValidateBreakout(cfg BreakoutConfig) error {
	if pc.Interfaces == nil {
		return fmt.Errorf("platform config has no interfaces defined")
	}

	portDef, ok := pc.Interfaces[cfg.ParentPort]
	if !ok {
		return fmt.Errorf("port %s not defined in platform.json", cfg.ParentPort)
	}

	if len(portDef.BreakoutModes) == 0 {
		return fmt.Errorf("port %s does not support breakout", cfg.ParentPort)
	}

	supported := false
	for _, mode := range portDef.BreakoutModes {
		if mode == cfg.Mode {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("breakout mode %s not supported for port %s (supported: %s)",
			cfg.Mode, cfg.ParentPort, strings.Join(portDef.BreakoutModes, ", "))
	}

	return nil
}

// GetChildPorts returns the child port names for a breakout operation.
// For example, breaking out Ethernet0 with 4x25G on a platform with 4-lane ports
// returns ["Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"].
func (pc *SonicPlatformConfig) GetChildPorts(parentPort, mode string) ([]string, error) {
	portDef, ok := pc.Interfaces[parentPort]
	if !ok {
		return nil, fmt.Errorf("port %s not defined in platform.json", parentPort)
	}

	// Parse the number of child ports from the mode (e.g., "4x25G" -> 4)
	parts := strings.SplitN(mode, "x", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid breakout mode format: %s", mode)
	}
	count, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid breakout count in mode %s: %w", mode, err)
	}

	// Parse parent port index
	lanes := strings.Split(portDef.Lanes, ",")
	if len(lanes) < count {
		return nil, fmt.Errorf("port %s has %d lanes, cannot break into %d ports",
			parentPort, len(lanes), count)
	}

	// Generate child port names based on parent port index
	var childPorts []string
	// Extract base number from parent port name (e.g., "Ethernet0" -> 0)
	portNum := extractPortNumber(parentPort)
	if portNum < 0 {
		return nil, fmt.Errorf("cannot extract port number from %s", parentPort)
	}

	for i := 0; i < count; i++ {
		childPorts = append(childPorts, fmt.Sprintf("Ethernet%d", portNum+i))
	}

	return childPorts, nil
}

// HasConflictingPorts checks if creating a port would conflict with
// existing ports due to shared lanes.
func (pc *SonicPlatformConfig) HasConflictingPorts(portName string, existingPorts map[string]PortEntry) []string {
	portDef, ok := pc.Interfaces[portName]
	if !ok {
		return nil
	}

	wantLanes := parseLanes(portDef.Lanes)
	var conflicts []string

	for existingName, existingPort := range existingPorts {
		if existingName == portName {
			continue
		}
		existingLanes := parseLanes(existingPort.Lanes)
		if lanesOverlap(wantLanes, existingLanes) {
			conflicts = append(conflicts, existingName)
		}
	}

	return conflicts
}

// GeneratePlatformSpec creates a spec.PlatformSpec from this device's platform.json.
// Used to prime the spec system on first connect to a new hardware platform.
func (pc *SonicPlatformConfig) GeneratePlatformSpec(hwsku string) *spec.PlatformSpec {
	portCount := len(pc.Interfaces)
	defaultSpeed := ""
	breakoutModes := make(map[string]bool)

	for _, portDef := range pc.Interfaces {
		if defaultSpeed == "" && portDef.DefaultSpeed != "" {
			defaultSpeed = portDef.DefaultSpeed
		}
		for _, mode := range portDef.BreakoutModes {
			breakoutModes[mode] = true
		}
	}

	var breakouts []string
	for mode := range breakoutModes {
		breakouts = append(breakouts, mode)
	}

	return &spec.PlatformSpec{
		HWSKU:        hwsku,
		PortCount:    portCount,
		DefaultSpeed: defaultSpeed,
		Breakouts:    breakouts,
	}
}

// extractPortNumber extracts the numeric suffix from a port name like "Ethernet0".
func extractPortNumber(name string) int {
	// Find the last non-digit to digit transition
	numStart := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] >= '0' && name[i] <= '9' {
			numStart = i
		} else {
			break
		}
	}
	if numStart < 0 {
		return -1
	}
	n, err := strconv.Atoi(name[numStart:])
	if err != nil {
		return -1
	}
	return n
}

// parseLanes parses a comma-separated lane string into a set of lane numbers.
func parseLanes(lanes string) map[int]bool {
	result := make(map[int]bool)
	for _, l := range strings.Split(lanes, ",") {
		l = strings.TrimSpace(l)
		if n, err := strconv.Atoi(l); err == nil {
			result[n] = true
		}
	}
	return result
}

// lanesOverlap returns true if two lane sets share any lanes.
func lanesOverlap(a, b map[int]bool) bool {
	for lane := range a {
		if b[lane] {
			return true
		}
	}
	return false
}
