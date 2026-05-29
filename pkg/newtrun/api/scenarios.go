// Scenario CRUD over HTTP. Enables browser-side scenario authoring
// (newtcon) without filesystem access to the host running
// newtrun-server. Per issue #33: every write is validated through
// newtrun.ParseScenarioBytes — the server is the single point that
// knows the parser's accept set, so a bad YAML cannot reach the
// suites tree and surface later as a confusing run failure.
//
// Persistence: <suites-base>/<suite>/<name>.yaml. Writes go through a
// same-directory tempfile + rename, which is atomic on POSIX
// filesystems — a partially-written scenario can never be observed by
// a concurrent ParseAllScenarios.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// nameRE constrains suite and scenario identifiers to a safe subset.
// Allowed: alphanumeric, hyphen, underscore. No path separators, no
// dots (which would let a caller traverse with "."/"..") — operators
// who want lexical ordering use a "NN-" prefix that fits this charset.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// CreateSuiteRequest is the body for POST /api/suites.
type CreateSuiteRequest struct {
	Name string `json:"name"`
}

// handleCreateSuite creates an empty suite directory. Used by newtcon
// to bootstrap a new test suite before populating it with scenarios.
// Returns 201 on create, 409 if the suite already exists, 400 on
// invalid name.
func (s *Server) handleCreateSuite(w http.ResponseWriter, r *http.Request) {
	var req CreateSuiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if !nameRE.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q: must match %s", req.Name, nameRE))
		return
	}
	dir := filepath.Join(s.cfg.SuitesBase, req.Name)
	if _, err := os.Stat(dir); err == nil {
		writeError(w, http.StatusConflict, fmt.Errorf("suite %q already exists", req.Name))
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("create suite dir: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

// handleDeleteSuite removes an empty suite directory. Refuses to
// delete a suite that still contains scenario files — newtcon's
// browser UX is expected to delete scenarios individually first so
// the destructive action is explicit at the scenario level rather
// than masked behind a directory rmdir.
func (s *Server) handleDeleteSuite(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if !nameRE.MatchString(suite) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q", suite))
		return
	}
	dir := filepath.Join(s.cfg.SuitesBase, suite)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Errorf("suite %q not found", suite))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(entries) > 0 {
		writeError(w, http.StatusConflict, fmt.Errorf("suite %q is not empty (%d entries); delete scenarios first", suite, len(entries)))
		return
	}
	if err := os.Remove(dir); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("remove suite dir: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetScenario returns one scenario's raw YAML body. Resolves
// the on-disk file by either exact <name>.yaml or *-<name>.yaml,
// matching the convention `newtrun list` already uses for files
// written outside the API.
func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := scenarioPathParams(w, r)
	if !ok {
		return
	}
	path, err := resolveScenarioPath(s.cfg.SuitesBase, suite, name)
	if err != nil {
		writeError(w, statusForFSError(err), err)
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("read scenario: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handlePutScenario creates or updates a scenario. The body is raw
// YAML (Content-Type ignored — operators send what they have, the
// server's accept-set is YAML). ParseScenarioBytes is the single
// validation gate; if the body doesn't parse, the file is never
// touched.
//
// Atomicity: the new content is written to a same-directory tempfile
// and renamed into place. rename(2) is atomic on POSIX so concurrent
// readers (ParseAllScenarios, GET requests) never observe a
// partially-written scenario.
//
// File naming: a fresh scenario lands at <name>.yaml. An update to an
// existing scenario keeps its original on-disk name (preserving any
// "NN-" lexical prefix authored outside the API), since the URL
// already addresses the canonical scenario name regardless.
func (s *Server) handlePutScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := scenarioPathParams(w, r)
	if !ok {
		return
	}
	suiteDir := filepath.Join(s.cfg.SuitesBase, suite)
	if _, err := os.Stat(suiteDir); err != nil {
		writeError(w, statusForFSError(err), fmt.Errorf("suite %q not found", suite))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}
	parsed, err := newtrun.ParseScenarioBytes(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid scenario YAML: %w", err))
		return
	}
	if parsed.Name != name {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("body name field %q does not match URL name %q", parsed.Name, name))
		return
	}

	// Pick the destination filename: existing file's basename if
	// any (preserves operator-authored lexical prefix), else
	// <name>.yaml for a fresh scenario.
	destPath, status, err := resolveDestPath(suiteDir, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if err := atomicWriteFile(destPath, body); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write scenario: %w", err))
		return
	}
	writeJSON(w, status, map[string]string{
		"suite": suite,
		"name":  name,
		"path":  filepath.Base(destPath),
	})
}

// handleDeleteScenario removes a scenario file. 404 if it doesn't
// exist; 204 on success. Matches the same name-resolution rule as
// GET so on-disk files with a "NN-" prefix can be deleted by their
// canonical name.
func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := scenarioPathParams(w, r)
	if !ok {
		return
	}
	path, err := resolveScenarioPath(s.cfg.SuitesBase, suite, name)
	if err != nil {
		writeError(w, statusForFSError(err), err)
		return
	}
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("remove scenario: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scenarioPathParams pulls and validates the suite and name path
// parameters. Writes the appropriate error response and returns
// ok=false if either is invalid.
func scenarioPathParams(w http.ResponseWriter, r *http.Request) (suite, name string, ok bool) {
	suite = r.PathValue("suite")
	name = r.PathValue("name")
	if !nameRE.MatchString(suite) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q", suite))
		return "", "", false
	}
	if !nameRE.MatchString(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid scenario name %q", name))
		return "", "", false
	}
	return suite, name, true
}

// resolveScenarioPath finds the on-disk file for a scenario, matching
// either exact <name>.yaml or *-<name>.yaml (the existing lexical
// prefix convention). Returns os.ErrNotExist when no candidate matches.
func resolveScenarioPath(suitesBase, suite, name string) (string, error) {
	suiteDir := filepath.Join(suitesBase, suite)
	entries, err := os.ReadDir(suiteDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".yaml")
		if base == name || strings.HasSuffix(base, "-"+name) {
			return filepath.Join(suiteDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("scenario %q not found in suite %q: %w", name, suite, os.ErrNotExist)
}

// resolveDestPath chooses the write destination for PUT. Returns the
// existing file's path (for in-place update, preserving any "NN-"
// prefix) or the fresh-create path <name>.yaml. The status return
// distinguishes 200 (update) from 201 (create) for the response.
func resolveDestPath(suiteDir, name string) (string, int, error) {
	path, err := resolveScenarioPath(filepath.Dir(suiteDir), filepath.Base(suiteDir), name)
	switch {
	case err == nil:
		return path, http.StatusOK, nil
	case errors.Is(err, os.ErrNotExist):
		return filepath.Join(suiteDir, name+".yaml"), http.StatusCreated, nil
	default:
		return "", 0, err
	}
}

// atomicWriteFile writes body to a tempfile in the same directory as
// dest and renames it into place. Same-directory tempfile is the
// requirement that makes rename(2) atomic; a tmpdir-then-rename would
// fall back to a copy across filesystems and lose the atomicity.
func atomicWriteFile(dest string, body []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".scenario-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return err
	}
	return nil
}

// statusForFSError maps a filesystem-level error to its HTTP status.
// Used by GET/DELETE so a missing file produces 404, not 500.
func statusForFSError(err error) int {
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
