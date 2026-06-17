package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAlreadyInitialized is returned by Scaffold when specDir already contains
// at least one of the three seed spec files. The caller should map this to
// 409 Conflict on HTTP; the existing topology is not overwritten.
var ErrAlreadyInitialized = errors.New("spec directory already initialized")

// Scaffold creates an empty topology layout at specDir: the directory itself
// (mkdir -p), an empty profiles/ subdirectory, and three zero-valued spec
// files — topology.json, platforms.json, network.json — that newtron's
// Loader requires.
//
// Returns ErrAlreadyInitialized if any of the three spec files already
// exists. The check is intentionally narrow: a pre-existing empty specDir
// (or one containing only unrelated files) is fine to scaffold into. This
// lets operators pre-create the directory with their preferred permissions
// or alongside other artifacts.
//
// description seeds topology.json's Description field so a fresh listing
// carries authoring context. Pass "" to omit.
func Scaffold(specDir, description string) error {
	if specDir == "" {
		return fmt.Errorf("spec_dir is required")
	}

	// Refuse to overwrite existing specs. Check all three before doing any
	// work so we don't leave a half-scaffolded directory on conflict.
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err == nil {
			return fmt.Errorf("%w: %s already exists at %s", ErrAlreadyInitialized, name, specDir)
		}
	}

	profilesDir := filepath.Join(specDir, "nodes")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}

	if err := writeSeed(specDir, "topology.json", &TopologySpecFile{
		Version:     "1.0",
		Description: description,
		Devices:     map[string]*TopologyDevice{},
		Links:       []*TopologyLink{},
	}); err != nil {
		return err
	}
	if err := writeSeed(specDir, "platforms.json", &PlatformSpecFile{
		Version:   "1.0",
		Platforms: map[string]*PlatformSpec{},
	}); err != nil {
		return err
	}
	if err := writeSeed(specDir, "network.json", &NetworkSpecFile{
		Version: "1.0",
		Zones:   map[string]*ZoneSpec{},
	}); err != nil {
		return err
	}
	return nil
}

// writeSeed marshals v with indent and writes it under specDir/name. Indented
// output is intentional — early authoring happens by hand before newtron's
// topology CRUD takes over the rewrites.
func writeSeed(specDir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	data = append(data, '\n')
	path := filepath.Join(specDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
