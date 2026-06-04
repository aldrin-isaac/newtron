// Topology-level HTTP handlers: list / create a topology directory.
// Per-topology mutation (devices, links) is owned by newtron at
// /newtron/v1/network/{netID}/topology/... — newtrun only bootstraps
// the directory so the operator can register it as a network without
// shelling out. See issue #76.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// handleListTopologies returns the topology names discoverable under
// TopologiesBase. v0 implementation: list immediate subdirectories.
func (s *Server) handleListTopologies(w http.ResponseWriter, r *http.Request) {
	names, err := listSubdirs(s.cfg.TopologiesBase)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, TopologiesResponse{Topologies: names})
}

// handleCreateTopology bootstraps a new topology directory under
// TopologiesBase. The directory ships with the three spec files
// newtron's Loader requires — topology.json, platforms.json,
// network.json — each in its zero-valued form, plus an empty
// profiles/ subdirectory. The operator (or newtcon) then chains into
// POST /newtron/v1/network with the returned spec_dir to register
// the topology as a newtron Network.
//
// 201 on create, 409 if the topology already exists, 400 on invalid
// name. Uses the shared nameRE so topology and suite identifiers
// share one validation gate.
func (s *Server) handleCreateTopology(w http.ResponseWriter, r *http.Request) {
	var req CreateTopologyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if !nameRE.MatchString(req.Name) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid topology name %q: must match %s", req.Name, nameRE))
		return
	}
	topoDir := filepath.Join(s.cfg.TopologiesBase, req.Name)
	if _, err := os.Stat(topoDir); err == nil {
		httputil.WriteError(w, http.StatusConflict, fmt.Errorf("topology %q already exists", req.Name))
		return
	}
	specDir := filepath.Join(topoDir, "specs")
	profilesDir := filepath.Join(specDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("create topology dir: %w", err))
		return
	}
	if err := writeSeedSpec(specDir, "topology.json", &spec.TopologySpecFile{
		Version:     "1.0",
		Description: req.Description,
		Devices:     map[string]*spec.TopologyDevice{},
		Links:       []*spec.TopologyLink{},
	}); err != nil {
		_ = os.RemoveAll(topoDir)
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	if err := writeSeedSpec(specDir, "platforms.json", &spec.PlatformSpecFile{
		Version:   "1.0",
		Platforms: map[string]*spec.PlatformSpec{},
	}); err != nil {
		_ = os.RemoveAll(topoDir)
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	if err := writeSeedSpec(specDir, "network.json", &spec.NetworkSpecFile{
		Version: "1.0",
		Zones:   map[string]*spec.ZoneSpec{},
	}); err != nil {
		_ = os.RemoveAll(topoDir)
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, CreateTopologyResponse{
		Name:    req.Name,
		SpecDir: specDir,
	})
}

// writeSeedSpec marshals v with indent and writes it under dir/name.
// Indented output is intentional — the operator will edit these files
// by hand in early authoring, before newtron's topology CRUD handlers
// take over the rewrites.
func writeSeedSpec(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
