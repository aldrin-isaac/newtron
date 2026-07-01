// Package settings manages persistent user settings for the newtron CLI.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultDir is the default specification directory used when no override is configured.
const DefaultDir = "/etc/newtron"

// Settings holds persistent user preferences.
// Settings is not goroutine-safe; callers must synchronize concurrent access.
type Settings struct {
	// DefaultNetwork is the network to use when -n is not specified
	DefaultNetwork string `json:"default_network,omitempty"`

	// Dir overrides the default specification directory
	Dir string `json:"dir,omitempty"`

	// DefaultSuite is the default --dir for newtrun start
	DefaultSuite string `json:"default_suite,omitempty"`

	// NetworksDir is the base directory for newtrun topologies
	NetworksDir string `json:"networks_dir,omitempty"`

	// ServerURL is the newtron-server HTTP address
	ServerURL string `json:"server_url,omitempty"`

	// NetworkID identifies which registered network to operate on
	NetworkID string `json:"network_id,omitempty"`
}

// DefaultSettingsPath returns the default path for the settings file
func DefaultSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/newtron_settings.json"
	}
	return filepath.Join(home, ".newtron", "settings.json")
}

// Load reads settings from the default location
func Load() (*Settings, error) {
	return LoadFrom(DefaultSettingsPath())
}

// LoadFrom reads settings from a specific path
func LoadFrom(path string) (*Settings, error) {
	s := &Settings{}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty settings if file doesn't exist
			return s, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}

	return s, nil
}

// Save writes settings to the default location
func (s *Settings) Save() error {
	return s.SaveTo(DefaultSettingsPath())
}

// SaveTo writes settings to a specific path
func (s *Settings) SaveTo(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetDir returns the network directory (with fallback)
func (s *Settings) GetDir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return DefaultDir
}

