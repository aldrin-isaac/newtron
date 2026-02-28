package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_Defaults(t *testing.T) {
	s := &Settings{}

	// Test default spec dir
	if got := s.GetSpecDir(); got != DefaultSpecDir {
		t.Errorf("GetSpecDir() default = %q, want %q", got, DefaultSpecDir)
	}

	// Test empty defaults
	if s.DefaultNetwork != "" {
		t.Errorf("DefaultNetwork should be empty, got %q", s.DefaultNetwork)
	}
}

func TestSettings_FieldAssignment(t *testing.T) {
	s := &Settings{}

	s.DefaultNetwork = "production"
	if s.DefaultNetwork != "production" {
		t.Errorf("DefaultNetwork = %q, want %q", s.DefaultNetwork, "production")
	}

	s.SpecDir = "/custom/path"
	if s.GetSpecDir() != "/custom/path" {
		t.Errorf("GetSpecDir() = %q, want %q", s.GetSpecDir(), "/custom/path")
	}

	s.DefaultSuite = "newtrun/suites/2node-incremental"
	if s.DefaultSuite != "newtrun/suites/2node-incremental" {
		t.Errorf("DefaultSuite = %q", s.DefaultSuite)
	}

	s.TopologiesDir = "newtrun/topologies"
	if s.TopologiesDir != "newtrun/topologies" {
		t.Errorf("TopologiesDir = %q", s.TopologiesDir)
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
		SpecDir:        "/etc/newtron",
		DefaultSuite:   "newtrun/suites/2node-incremental",
		TopologiesDir:  "newtrun/topologies",
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
	if loaded.SpecDir != original.SpecDir {
		t.Errorf("SpecDir mismatch: got %q, want %q", loaded.SpecDir, original.SpecDir)
	}
	if loaded.DefaultSuite != original.DefaultSuite {
		t.Errorf("DefaultSuite mismatch: got %q, want %q", loaded.DefaultSuite, original.DefaultSuite)
	}
	if loaded.TopologiesDir != original.TopologiesDir {
		t.Errorf("TopologiesDir mismatch: got %q, want %q", loaded.TopologiesDir, original.TopologiesDir)
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
	if s.DefaultNetwork != "" {
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
	testSettings := `{"default_network":"test-network"}`
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
}

func TestDefaultSettingsPath(t *testing.T) {
	path := DefaultSettingsPath()
	if path == "" {
		t.Error("DefaultSettingsPath() should not be empty")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("DefaultSettingsPath() should be absolute, got %q", path)
	}
}

func TestDefaultSettingsPath_NoHome(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Unset HOME to trigger fallback path
	os.Unsetenv("HOME")

	path := DefaultSettingsPath()
	if path != "/tmp/newtron_settings.json" {
		t.Errorf("DefaultSettingsPath() with no HOME = %q, want %q", path, "/tmp/newtron_settings.json")
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
