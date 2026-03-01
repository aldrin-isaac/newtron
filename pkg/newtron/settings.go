package newtron

import (
	"github.com/newtron-network/newtron/pkg/newtron/settings"
)

// LoadSettings loads user settings from the default path.
func LoadSettings() (*UserSettings, error) {
	s, err := settings.Load()
	if err != nil {
		return nil, err
	}
	return &UserSettings{
		DefaultNetwork:  s.DefaultNetwork,
		SpecDir:         s.SpecDir,
		DefaultSuite:    s.DefaultSuite,
		TopologiesDir:   s.TopologiesDir,
		AuditLogPath:    s.AuditLogPath,
		AuditMaxSizeMB:  s.AuditMaxSizeMB,
		AuditMaxBackups: s.AuditMaxBackups,
	}, nil
}

// SaveSettings saves user settings to the default path.
func SaveSettings(us *UserSettings) error {
	s := &settings.Settings{
		DefaultNetwork:  us.DefaultNetwork,
		SpecDir:         us.SpecDir,
		DefaultSuite:    us.DefaultSuite,
		TopologiesDir:   us.TopologiesDir,
		AuditLogPath:    us.AuditLogPath,
		AuditMaxSizeMB:  us.AuditMaxSizeMB,
		AuditMaxBackups: us.AuditMaxBackups,
	}
	return s.Save()
}

// SettingsPath returns the path to the settings file.
func SettingsPath() string {
	return settings.DefaultSettingsPath()
}
