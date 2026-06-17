// Suite-level HTTP handlers: list / create / delete a suite directory.
// Scenario-level CRUD (the operations *inside* a suite) lives in
// scenarios.go — split by feature per §28 (File-Level Feature Cohesion).
// The two files share nameRE; it lives here because suite-name
// validation is the gate every other operation goes through (a
// scenario URL contains a suite name).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// writeSuiteManifest writes a minimal suite.yaml manifest to dir. All
// suite-creating call sites — file-backed POST /suites and the inline
// run staging — go through this helper so the encoding is uniformly
// yaml.Marshal. Hand-rolled fmt.Sprintf into YAML is a metacharacter
// hazard (newlines, colons, leading dashes inside the topology string
// could smuggle additional top-level fields past LoadSuite).
//
// Callers must validate name + topology against nameRE before calling
// — writeSuiteManifest does not re-validate.
func writeSuiteManifest(dir, name, topology string) error {
	body, err := yaml.Marshal(&newtrun.Suite{Name: name, Network: topology})
	if err != nil {
		return fmt.Errorf("marshal suite.yaml: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "suite.yaml"), body, 0o644)
}

// nameRE constrains suite and scenario identifiers to a safe subset.
// Allowed: alphanumeric, hyphen, underscore. No path separators, no
// dots (which would let a caller traverse with "."/"..") — operators
// who want lexical ordering use a "NN-" prefix that fits this charset.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// CreateSuiteRequest is the body for POST /api/suites. Topology is
// required — every suite declares its target topology on creation so
// the runner can guard against topology/scenario mismatches.
type CreateSuiteRequest struct {
	Name     string `json:"name"`
	Network   string          `json:"network"`
}

// CreateSuiteResponse is the body returned by POST /api/suites.
// Typed (rather than an ad-hoc map) so adding fields to either side of
// the create-suite / create-topology pair is a compile-time change for
// clients — mirrors the typed CreateTopologyResponse shape (§13 same
// concept = same name; §23 new code matches the codebase idiom).
type CreateSuiteResponse struct {
	Name string `json:"name"`
}

// handleListSuites returns every suite name discoverable under
// NetworksBase by scanning <base>/*/suites/*. Suite ownership lives
// in the topology subtree (§27); the API surface stays flat — callers
// see a single list of suite names regardless of which topology each
// belongs to.
func (s *Server) handleListSuites(w http.ResponseWriter, r *http.Request) {
	names, err := newtrun.ListAllSuites(s.cfg.NetworksBase)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, SuitesResponse{Suites: names})
}

// handleCreateSuite creates an empty suite directory. Used by newtcon
// to bootstrap a new test suite before populating it with scenarios.
// Returns 201 on create, 409 if the suite already exists, 400 on
// invalid name. If the suite manifest write fails after the directory
// was created, the directory is rolled back so the operator doesn't
// inherit an orphaned dir.
func (s *Server) handleCreateSuite(w http.ResponseWriter, r *http.Request) {
	var req CreateSuiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if !nameRE.MatchString(req.Name) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q: must match %s", req.Name, nameRE))
		return
	}
	if !nameRE.MatchString(req.Network) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid topology %q: must match %s", req.Network, nameRE))
		return
	}
	// Refuse to create a suite whose name already exists under any
	// topology — suite names are globally unique across the tree
	// (see resolveSuiteDir). Catching the conflict at create time
	// gives the operator a 409 with a clear message rather than a
	// later ambiguous-resolution 500.
	if existing, err := newtrun.ResolveSuiteDir(s.cfg.NetworksBase, req.Name); err == nil {
		httputil.WriteError(w, http.StatusConflict, fmt.Errorf("suite %q already exists at %s", req.Name, existing))
		return
	}
	dir := filepath.Join(s.cfg.NetworksBase, req.Network, "suites", req.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("create suite dir: %w", err))
		return
	}
	if err := writeSuiteManifest(dir, req.Name, req.Network); err != nil {
		_ = os.RemoveAll(dir)
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, CreateSuiteResponse{Name: req.Name})
}

// handleDeleteSuite removes an empty suite directory. Refuses to
// delete a suite that still contains scenario files — newtcon's
// browser UX is expected to delete scenarios individually first so
// the destructive action is explicit at the scenario level rather
// than masked behind a directory rmdir.
func (s *Server) handleDeleteSuite(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if !nameRE.MatchString(suite) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q", suite))
		return
	}
	dir, err := newtrun.ResolveSuiteDir(s.cfg.NetworksBase, suite)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), fmt.Errorf("suite %q not found", suite))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	// Empty-suite check ignores suite.yaml — the suite manifest is part
	// of the suite-create handshake, not user content. Any other entry
	// (scenario YAMLs, subdirectories with operator-authored content)
	// blocks deletion so we never silently destroy user files. The
	// reverse of "create suite + suite.yaml" is "remove dir + suite.yaml"
	// — anything beyond that requires the operator to clear it first.
	for _, e := range entries {
		if !e.IsDir() && e.Name() == "suite.yaml" {
			continue
		}
		httputil.WriteError(w, http.StatusConflict, fmt.Errorf("suite %q still has content (%s); delete it first", suite, e.Name()))
		return
	}
	if err := os.Remove(filepath.Join(dir, "suite.yaml")); err != nil && !os.IsNotExist(err) {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("remove suite.yaml: %w", err))
		return
	}
	if err := os.Remove(dir); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("remove suite dir: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListSuiteScenarios returns the scenarios in the named suite as
// summaries (name, topology, step count, requires). The browser suite
// picker and `newtrun list <suite>` both render from this shape.
//
// This handler lives with the suite-level operations because it's a
// listing of the suite's contents; per-scenario CRUD (GET/PUT/DELETE on
// a single scenario name) lives in scenarios.go.
func (s *Server) handleListSuiteScenarios(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if !nameRE.MatchString(suite) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q", suite))
		return
	}
	dir, err := newtrun.ResolveSuiteDir(s.cfg.NetworksBase, suite)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), fmt.Errorf("suite %q not found", suite))
		return
	}
	loaded, err := newtrun.LoadSuite(dir)
	if err != nil {
		// LoadSuite wraps the underlying file-open error via fmt.Errorf,
		// so os.IsNotExist (which doesn't unwrap) reports false. Use
		// errors.Is against os.ErrNotExist instead — that follows the
		// %w chain and correctly distinguishes "suite directory not
		// found" (404) from "suite.yaml malformed" (400).
		if errors.Is(err, os.ErrNotExist) {
			httputil.WriteError(w, http.StatusNotFound, fmt.Errorf("suite %q not found", suite))
			return
		}
		httputil.WriteError(w, http.StatusBadRequest, err)
		return
	}
	resp := SuiteScenariosResponse{
		Suite:    suite,
		Network: loaded.Network,
	}
	// Topology and Platform are on the SuiteScenariosResponse envelope,
	// not on per-scenario summaries — repeating them would diverge once
	// suite.yaml changes.
	resp.Platform = loaded.Platform
	resp.Scenarios = make([]ScenarioSummary, len(loaded.Scenarios))
	for i, sc := range loaded.Scenarios {
		resp.Scenarios[i] = ScenarioSummary{
			Name:        sc.Name,
			Description: sc.Description,
			StepCount:   len(sc.Steps),
			Requires:    sc.Requires,
		}
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}
