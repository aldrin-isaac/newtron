package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_Defaults(t *testing.T) {
	s := &Settings{}

	// Test default spec dir
	if got := s.GetSpecDir(); got != "/etc/newtron" {
		t.Errorf("GetSpecDir() default = %q, want %q", got, "/etc/newtron")
	}

	// Test empty defaults
	if s.DefaultNetwork != "" {
		t.Errorf("DefaultNetwork should be empty, got %q", s.DefaultNetwork)
	}
	if s.DefaultDevice != "" {
		t.Errorf("DefaultDevice should be empty, got %q", s.DefaultDevice)
	}
}

func TestSettings_SettersGetters(t *testing.T) {
	s := &Settings{}

	s.SetNetwork("production")
	if s.DefaultNetwork != "production" {
		t.Errorf("SetNetwork() failed, got %q", s.DefaultNetwork)
	}

	s.SetDevice("leaf1-ny")
	if s.DefaultDevice != "leaf1-ny" {
		t.Errorf("SetDevice() failed, got %q", s.DefaultDevice)
	}

	s.SetSpecDir("/custom/path")
	if s.GetSpecDir() != "/custom/path" {
		t.Errorf("SetSpecDir() failed, got %q", s.GetSpecDir())
	}
}

func TestSettings_Clear(t *testing.T) {
	s := &Settings{
		DefaultNetwork: "test",
		DefaultDevice:  "device",
		SpecDir:        "/path",
		LastDevice:     "last",
	}

	s.Clear()

	if s.DefaultNetwork != "" || s.DefaultDevice != "" || s.SpecDir != "" || s.LastDevice != "" {
		t.Error("Clear() should reset all fields to empty")
	}
}

func TestSettings_SaveLoad(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")

	// Create settings
	original := &Settings{
		DefaultNetwork: "production",
		DefaultDevice:  "leaf1-ny",
		SpecDir:        "/etc/newtron",
		LastDevice:     "spine1-ny",
	}

	// Save
	if err := original.SaveTo(path); err != nil {
		t.Fatalf("SaveTo() failed: %v", err)
	}

	// Load
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() failed: %v", err)
	}

	// Compare
	if loaded.DefaultNetwork != original.DefaultNetwork {
		t.Errorf("DefaultNetwork mismatch: got %q, want %q", loaded.DefaultNetwork, original.DefaultNetwork)
	}
	if loaded.DefaultDevice != original.DefaultDevice {
		t.Errorf("DefaultDevice mismatch: got %q, want %q", loaded.DefaultDevice, original.DefaultDevice)
	}
	if loaded.SpecDir != original.SpecDir {
		t.Errorf("SpecDir mismatch: got %q, want %q", loaded.SpecDir, original.SpecDir)
	}
	if loaded.LastDevice != original.LastDevice {
		t.Errorf("LastDevice mismatch: got %q, want %q", loaded.LastDevice, original.LastDevice)
	}
}

func TestSettings_LoadNonExistent(t *testing.T) {
	// Load from non-existent path should return empty settings
	s, err := LoadFrom("/nonexistent/path/settings.json")
	if err != nil {
		t.Fatalf("LoadFrom() non-existent should not error: %v", err)
	}
	if s == nil {
		t.Fatal("LoadFrom() should return non-nil Settings")
	}
	if s.DefaultNetwork != "" || s.DefaultDevice != "" {
		t.Error("LoadFrom() non-existent should return empty settings")
	}
}

func TestSettings_LoadInvalidJSON(t *testing.T) {
	// Create temp file with invalid JSON
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")
	if err := os.WriteFile(path, []byte("invalid json {"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = LoadFrom(path)
	if err == nil {
		t.Error("LoadFrom() with invalid JSON should error")
	}
}

func TestSettings_SaveCreatesDirectory(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Path with non-existent directory
	path := filepath.Join(tmpDir, "subdir", "nested", "settings.json")

	s := &Settings{DefaultNetwork: "test"}
	if err := s.SaveTo(path); err != nil {
		t.Fatalf("SaveTo() should create directories: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("SaveTo() should have created the file")
	}
}

func TestDefaultSettingsPath(t *testing.T) {
	path := DefaultSettingsPath()
	if path == "" {
		t.Error("DefaultSettingsPath() should not be empty")
	}
	if !filepath.IsAbs(path) && path != "newtron_settings.json" {
		t.Errorf("DefaultSettingsPath() should be absolute or fallback, got %q", path)
	}
}

func TestSettings_ExecuteByDefault(t *testing.T) {
	s := &Settings{ExecuteByDefault: true}

	if !s.ExecuteByDefault {
		t.Error("ExecuteByDefault should be true")
	}

	// Test save/load preserves this dangerous setting
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "settings.json")
	if err := s.SaveTo(path); err != nil {
		t.Fatalf("SaveTo() failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() failed: %v", err)
	}
	if !loaded.ExecuteByDefault {
		t.Error("ExecuteByDefault should be preserved after save/load")
	}
}

func TestLoad(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Create temp directory to use as HOME
	tmpDir, err := os.MkdirTemp("", "newtron-test-home-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set HOME to temp directory
	os.Setenv("HOME", tmpDir)

	// Test Load() with non-existent settings (should return empty)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() with non-existent file should not error: %v", err)
	}
	if s == nil {
		t.Fatal("Load() should return non-nil Settings")
	}
	if s.DefaultNetwork != "" {
		t.Error("Load() with non-existent file should return empty settings")
	}

	// Create .newtron directory and settings file
	newtronDir := filepath.Join(tmpDir, ".newtron")
	if err := os.MkdirAll(newtronDir, 0755); err != nil {
		t.Fatalf("Failed to create .newtron dir: %v", err)
	}

	settingsPath := filepath.Join(newtronDir, "settings.json")
	testSettings := `{"default_network":"test-network","default_device":"test-device"}`
	if err := os.WriteFile(settingsPath, []byte(testSettings), 0644); err != nil {
		t.Fatalf("Failed to write test settings: %v", err)
	}

	// Test Load() with existing settings
	s, err = Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if s.DefaultNetwork != "test-network" {
		t.Errorf("Load() DefaultNetwork = %q, want %q", s.DefaultNetwork, "test-network")
	}
	if s.DefaultDevice != "test-device" {
		t.Errorf("Load() DefaultDevice = %q, want %q", s.DefaultDevice, "test-device")
	}
}

func TestSave(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Create temp directory to use as HOME
	tmpDir, err := os.MkdirTemp("", "newtron-test-home-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set HOME to temp directory
	os.Setenv("HOME", tmpDir)

	// Create settings and save
	s := &Settings{
		DefaultNetwork: "saved-network",
		DefaultDevice:  "saved-device",
	}

	if err := s.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify file was created at default path
	expectedPath := filepath.Join(tmpDir, ".newtron", "settings.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("Save() did not create file at %s", expectedPath)
	}

	// Load and verify contents
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}
	if loaded.DefaultNetwork != "saved-network" {
		t.Errorf("After Save(), DefaultNetwork = %q, want %q", loaded.DefaultNetwork, "saved-network")
	}
	if loaded.DefaultDevice != "saved-device" {
		t.Errorf("After Save(), DefaultDevice = %q, want %q", loaded.DefaultDevice, "saved-device")
	}
}

func TestDefaultSettingsPath_NoHome(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Unset HOME to trigger fallback path
	os.Unsetenv("HOME")

	path := DefaultSettingsPath()
	if path != "newtron_settings.json" {
		t.Errorf("DefaultSettingsPath() with no HOME = %q, want %q", path, "newtron_settings.json")
	}
}

func TestLoadFrom_ReadError(t *testing.T) {
	// Create a directory with the name of the settings file (causes read error)
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory where the file should be (causes "is a directory" error)
	dirAsFile := filepath.Join(tmpDir, "settings.json")
	if err := os.Mkdir(dirAsFile, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	_, err = LoadFrom(dirAsFile)
	if err == nil {
		t.Error("LoadFrom() should error when path is a directory")
	}
}

func TestSaveTo_MkdirError(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "newtron-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file where we want a directory to be (causes MkdirAll to fail)
	blockingFile := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("blocking"), 0644); err != nil {
		t.Fatalf("Failed to create blocking file: %v", err)
	}

	// Try to save under the blocking file (requires creating a directory named "blocker")
	path := filepath.Join(blockingFile, "subdir", "settings.json")
	s := &Settings{DefaultNetwork: "test"}

	err = s.SaveTo(path)
	if err == nil {
		t.Error("SaveTo() should fail when directory creation fails")
	}
}
