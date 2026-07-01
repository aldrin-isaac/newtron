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
// itself (mkdir -p), empty nodes/ and zones/ subdirectories (the
// per-file spec homes), and the zero-valued spec files —
// topology.json and network.json — that newtron's Loader requires.
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

	// Refuse to overwrite existing specs. Check both before doing any
	// work so we don't leave a half-created directory on conflict.
	// platforms.json is no longer per-network — the global registry
	// at --platforms-base owns platforms.
	for _, name := range []string{"topology.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err == nil {
			return fmt.Errorf("%w: %s already exists at %s", ErrAlreadyExists, name, specDir)
		}
	}

	// Scaffold the per-file spec directories: nodes/ and zones/ (both empty —
	// nodes and zones are added later via their CRUD paths, each to its own
	// file).
	for _, sub := range []string{"nodes", "zones"} {
		if err := os.MkdirAll(filepath.Join(specDir, sub), 0o755); err != nil {
			return fmt.Errorf("create %s dir: %w", sub, err)
		}
	}

	if err := writeSeed(specDir, "topology.json", &TopologySpecFile{
		Version:     "1.0",
		Description: description,
		Nodes:       map[string]*TopologyNode{},
		Links:       []*TopologyLink{},
	}); err != nil {
		return err
	}
	if err := writeSeed(specDir, "network.json", &NetworkSpecFile{
		Version: "1.0",
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
