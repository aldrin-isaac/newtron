package newtron

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/settings"
)

// LoadSettings loads user settings from the default path.
func LoadSettings() (*UserSettings, error) {
	s, err := settings.Load()
	if err != nil {
		return nil, err
	}
	return &UserSettings{
		DefaultNetwork:  s.DefaultNetwork,
		Dir:         s.Dir,
		DefaultSuite:    s.DefaultSuite,
		NetworksDir:   s.NetworksDir,
		ServerURL:       s.ServerURL,
		NetworkID:       s.NetworkID,
	}, nil
}

// SaveSettings saves user settings to the default path.
func SaveSettings(us *UserSettings) error {
	s := &settings.Settings{
		DefaultNetwork:  us.DefaultNetwork,
		Dir:         us.Dir,
		DefaultSuite:    us.DefaultSuite,
		NetworksDir:   us.NetworksDir,
		ServerURL:       us.ServerURL,
		NetworkID:       us.NetworkID,
	}
	return s.Save()
}

// SettingsPath returns the path to the settings file.
func SettingsPath() string {
	return settings.DefaultSettingsPath()
}
