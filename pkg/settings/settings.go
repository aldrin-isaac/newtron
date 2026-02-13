// Package settings manages persistent user settings for the newtron CLI.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings holds persistent user preferences
type Settings struct {
	// DefaultNetwork is the network to use when -n is not specified
	DefaultNetwork string `json:"default_network,omitempty"`

	// SpecDir overrides the default specification directory
	SpecDir string `json:"spec_dir,omitempty"`

	// DefaultSuite is the default --dir for newtest run
	DefaultSuite string `json:"default_suite,omitempty"`

	// TopologiesDir is the base directory for newtest topologies
	TopologiesDir string `json:"topologies_dir,omitempty"`
}

// DefaultSettingsPath returns the default path for the settings file
func DefaultSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "newtron_settings.json"
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

// SetNetwork sets the default network
func (s *Settings) SetNetwork(network string) {
	s.DefaultNetwork = network
}

// SetSpecDir sets the specification directory
func (s *Settings) SetSpecDir(dir string) {
	s.SpecDir = dir
}

// GetSpecDir returns the spec directory (with fallback)
func (s *Settings) GetSpecDir() string {
	if s.SpecDir != "" {
		return s.SpecDir
	}
	return "/etc/newtron"
}

// SetDefaultSuite sets the default newtest suite directory
func (s *Settings) SetDefaultSuite(dir string) {
	s.DefaultSuite = dir
}

// SetTopologiesDir sets the base directory for topologies
func (s *Settings) SetTopologiesDir(dir string) {
	s.TopologiesDir = dir
}

// Clear resets all settings to defaults
func (s *Settings) Clear() {
	*s = Settings{}
}
