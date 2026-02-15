// platform.go implements SONiC platform.json parsing and port validation.
package sonic

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
