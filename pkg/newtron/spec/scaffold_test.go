package spec

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScaffold_FreshDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "specs")

	if err := Scaffold(dir, "demo description"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// All three files exist.
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}

	// profiles/ exists as a directory.
	info, err := os.Stat(filepath.Join(dir, "nodes"))
	if err != nil {
		t.Fatalf("expected profiles/ to exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("profiles/ should be a directory")
	}

	// Description threads into topology.json.
	data, err := os.ReadFile(filepath.Join(dir, "topology.json"))
	if err != nil {
		t.Fatalf("read topology.json: %v", err)
	}
	var topo TopologySpecFile
	if err := json.Unmarshal(data, &topo); err != nil {
		t.Fatalf("parse topology.json: %v", err)
	}
	if topo.Description != "demo description" {
		t.Errorf("description = %q, want %q", topo.Description, "demo description")
	}
	if topo.Version != "1.0" {
		t.Errorf("version = %q, want %q", topo.Version, "1.0")
	}
}

func TestScaffold_AlreadyInitialized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-seed one of the three spec files so Scaffold should refuse.
	if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := Scaffold(dir, "")
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("err = %v, want ErrAlreadyInitialized", err)
	}

	// The pre-existing file was not clobbered.
	data, err := os.ReadFile(filepath.Join(dir, "topology.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("topology.json was overwritten: %s", data)
	}
	// And we didn't half-scaffold (platforms.json should not exist).
	if _, err := os.Stat(filepath.Join(dir, "platforms.json")); !os.IsNotExist(err) {
		t.Errorf("platforms.json should not exist after conflict: err=%v", err)
	}
}

func TestScaffold_EmptyPreExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := Scaffold(dir, ""); err != nil {
		t.Fatalf("Scaffold on empty pre-existing dir: %v", err)
	}
}

func TestScaffold_EmptySpecDir(t *testing.T) {
	if err := Scaffold("", ""); err == nil {
		t.Fatalf("expected error for empty dir")
	}
}
