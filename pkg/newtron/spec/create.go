package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAlreadyExists is returned by CreateEmpty when specDir already
// contains at least one of the three seed spec files. The caller
// decides whether to surface this as an error or treat it as an
// idempotent no-op signal.
var ErrAlreadyExists = errors.New("network specs already exist")

// CreateEmpty lays out an empty network at specDir: the directory
// itself (mkdir -p), an empty nodes/ subdirectory, and three
// zero-valued spec files — topology.json, platforms.json,
// network.json — that newtron's Loader requires.
//
// Returns ErrAlreadyExists if any of the three spec files already
// exists. The check is intentionally narrow: a pre-existing empty
// specDir (or one containing only unrelated files) is fine to
// create into. This lets operators pre-create the directory with
// their preferred permissions or alongside other artifacts.
//
// description seeds topology.json's Description field so a fresh
// listing carries authoring context. Pass "" to omit.
func CreateEmpty(specDir, description string) error {
	if specDir == "" {
		return fmt.Errorf("dir is required")
	}

	// Refuse to overwrite existing specs. Check all three before
	// doing any work so we don't leave a half-created directory
	// on conflict.
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err == nil {
			return fmt.Errorf("%w: %s already exists at %s", ErrAlreadyExists, name, specDir)
		}
	}

	nodesDir := filepath.Join(specDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		return fmt.Errorf("create nodes dir: %w", err)
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

// writeSeed marshals v with indent and writes it under specDir/name.
// Indented output is intentional — early authoring happens by hand
// before newtron's topology CRUD takes over the rewrites.
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
