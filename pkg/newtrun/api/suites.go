// Suite-level HTTP handlers: list / create / delete a suite directory.
// Scenario-level CRUD (the operations *inside* a suite) lives in
// scenarios.go — split by feature per §28 (File-Level Feature Cohesion).
// The two files share nameRE; it lives here because suite-name
// validation is the gate every other operation goes through (a
// scenario URL contains a suite name).
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

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
	Topology string `json:"topology"`
}

// handleListSuites returns the suite names discoverable under SuitesBase.
func (s *Server) handleListSuites(w http.ResponseWriter, r *http.Request) {
	names, err := listSubdirs(s.cfg.SuitesBase)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, SuitesResponse{Suites: names})
}

// handleCreateSuite creates an empty suite directory. Used by newtcon
// to bootstrap a new test suite before populating it with scenarios.
// Returns 201 on create, 409 if the suite already exists, 400 on
// invalid name.
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
	if req.Topology == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("topology is required"))
		return
	}
	dir := filepath.Join(s.cfg.SuitesBase, req.Name)
	if _, err := os.Stat(dir); err == nil {
		httputil.WriteError(w, http.StatusConflict, fmt.Errorf("suite %q already exists", req.Name))
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("create suite dir: %w", err))
		return
	}
	// Write the minimal suite.yaml — name + topology, no targets/
	// parameters block. Operators add targets/parameters later by
	// editing the file directly (no API to edit suite.yaml in v1;
	// scenarios go through PUT, but suite metadata is rarely-
	// changed config).
	suiteYAML := fmt.Sprintf("name: %s\ntopology: %s\n", req.Name, req.Topology)
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(suiteYAML), 0644); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("write suite.yaml: %w", err))
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
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
	dir := filepath.Join(s.cfg.SuitesBase, suite)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			httputil.WriteError(w, http.StatusNotFound, fmt.Errorf("suite %q not found", suite))
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	// Empty-suite check ignores suite.yaml — the suite manifest is part
	// of the suite-create handshake, not user content. A "still has
	// scenarios" check looks for any other .yaml file.
	for _, e := range entries {
		if e.IsDir() || e.Name() == "suite.yaml" {
			continue
		}
		httputil.WriteError(w, http.StatusConflict, fmt.Errorf("suite %q still has scenarios; delete them first", suite))
		return
	}
	if err := os.RemoveAll(dir); err != nil {
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
	if suite == "" {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	dir := filepath.Join(s.cfg.SuitesBase, suite)
	loaded, err := newtrun.LoadSuite(dir)
	if err != nil {
		if os.IsNotExist(err) {
			httputil.WriteError(w, http.StatusNotFound, fmt.Errorf("suite %q not found", suite))
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}
	resp := SuiteScenariosResponse{
		Suite:    suite,
		Topology: loaded.Topology,
	}
	resp.Scenarios = make([]ScenarioSummary, len(loaded.Scenarios))
	for i, sc := range loaded.Scenarios {
		resp.Scenarios[i] = ScenarioSummary{
			Name:        sc.Name,
			Description: sc.Description,
			Topology:    loaded.Topology, // inherited from suite
			Platform:    loaded.Platform,
			StepCount:   len(sc.Steps),
			Requires:    sc.Requires,
		}
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}
