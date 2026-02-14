// Package settings manages persistent user settings for the newtron CLI.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultSpecDir is the default specification directory used when no override is configured.
const DefaultSpecDir = "/etc/newtron"

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

	// AuditLogPath overrides the default audit log path
	AuditLogPath string `json:"audit_log_path,omitempty"`

	// AuditMaxSizeMB is the max audit log size in MB before rotation (default: 10)
	AuditMaxSizeMB int `json:"audit_max_size_mb,omitempty"`

	// AuditMaxBackups is the max number of rotated audit log files (default: 10)
	AuditMaxBackups int `json:"audit_max_backups,omitempty"`
}

const (
	// DefaultAuditMaxSizeMB is the default maximum audit log size in megabytes.
	DefaultAuditMaxSizeMB = 10

	// DefaultAuditMaxBackups is the default maximum number of rotated audit log files.
	DefaultAuditMaxBackups = 10
)

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

// GetSpecDir returns the spec directory (with fallback)
func (s *Settings) GetSpecDir() string {
	if s.SpecDir != "" {
		return s.SpecDir
	}
	return DefaultSpecDir
}

// GetAuditLogPath returns the audit log path with a fallback default.
// The default depends on specDir: if non-empty, uses specDir/audit.log;
// otherwise uses /var/log/newtron/audit.log.
func (s *Settings) GetAuditLogPath(specDir string) string {
	if s.AuditLogPath != "" {
		return s.AuditLogPath
	}
	if specDir != "" {
		return specDir + "/audit.log"
	}
	return "/var/log/newtron/audit.log"
}

// GetAuditMaxSizeMB returns the audit max size in MB with a default of 10.
func (s *Settings) GetAuditMaxSizeMB() int {
	if s.AuditMaxSizeMB > 0 {
		return s.AuditMaxSizeMB
	}
	return DefaultAuditMaxSizeMB
}

// GetAuditMaxBackups returns the audit max backups with a default of 10.
func (s *Settings) GetAuditMaxBackups() int {
	if s.AuditMaxBackups > 0 {
		return s.AuditMaxBackups
	}
	return DefaultAuditMaxBackups
}

// Clear resets all settings to defaults
func (s *Settings) Clear() {
	*s = Settings{}
}
