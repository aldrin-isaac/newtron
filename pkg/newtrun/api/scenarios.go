// Scenario CRUD over HTTP. Enables browser-side scenario authoring
// (newtcon) without filesystem access to the host running
// newtrun-server. Per issue #33: every write is validated through
// newtrun.ParseScenarioBytes — the server is the single point that
// knows the parser's accept set, so a bad YAML cannot reach the
// suites tree and surface later as a confusing run failure.
//
// Persistence: <topologies-base>/<topology>/suites/<suite>/<name>.yaml.
// Writes go through a same-directory tempfile + rename, which is atomic
// on POSIX filesystems — a partially-written scenario can never be
// observed by a concurrent suite-loader read.
package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"os"
	"path/filepath"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// handleGetScenario returns one scenario's raw YAML body. Resolves
// the on-disk file by either exact <name>.yaml or *-<name>.yaml,
// matching the convention `newtrun list` already uses for files
// written outside the API.
func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := requireScenarioParams(w, r)
	if !ok {
		return
	}
	suiteDir, err := newtrun.ResolveSuiteDir(s.cfg.TopologiesBase, suite)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), err)
		return
	}
	path, err := resolveScenarioPath(suiteDir, name)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), err)
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("read scenario: %w", err))
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
// readers (suite-loader, GET requests) never observe a partially-
// written scenario.
//
// File naming: a fresh scenario lands at <name>.yaml. An update to an
// existing scenario keeps its original on-disk name (preserving any
// "NN-" lexical prefix authored outside the API), since the URL
// already addresses the canonical scenario name regardless.
func (s *Server) handlePutScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := requireScenarioParams(w, r)
	if !ok {
		return
	}
	suiteDir, err := newtrun.ResolveSuiteDir(s.cfg.TopologiesBase, suite)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), fmt.Errorf("suite %q not found", suite))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}
	parsed, err := newtrun.ParseScenarioBytes(body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid scenario YAML: %w", err))
		return
	}
	if parsed.Name != name {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Errorf("body name field %q does not match URL name %q", parsed.Name, name))
		return
	}
	// File-backed scenarios live inside a suite; topology and platform
	// are declared on suite.yaml, not on individual scenarios.
	// Accepting them here would write a file LoadSuite later refuses,
	// silently breaking the next run of that suite.
	if parsed.Topology != "" {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Errorf("scenario must not set topology: — that is declared on suite.yaml"))
		return
	}
	if parsed.Platform != "" {
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Errorf("scenario must not set platform: — that is declared on suite.yaml or via --platform"))
		return
	}

	// Pick the destination filename: existing file's basename if
	// any (preserves operator-authored lexical prefix), else
	// <name>.yaml for a fresh scenario.
	destPath, status, err := resolveDestPath(suiteDir, name)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	if err := atomicWriteFile(destPath, body); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("write scenario: %w", err))
		return
	}
	// Response carries the abstract handle (suite + name) only. Whether
	// the server stored it as <name>.yaml or NN-<name>.yaml is a
	// storage detail that does not leak through the wire.
	httputil.WriteJSON(w, status, map[string]string{
		"suite": suite,
		"name":  name,
	})
}

// handleDeleteScenario removes a scenario file. 404 if it doesn't
// exist; 204 on success. Matches the same name-resolution rule as
// GET so on-disk files with a "NN-" prefix can be deleted by their
// canonical name.
func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	suite, name, ok := requireScenarioParams(w, r)
	if !ok {
		return
	}
	suiteDir, err := newtrun.ResolveSuiteDir(s.cfg.TopologiesBase, suite)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), err)
		return
	}
	path, err := resolveScenarioPath(suiteDir, name)
	if err != nil {
		httputil.WriteError(w, mapFSErrorToStatus(err), err)
		return
	}
	if err := os.Remove(path); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Errorf("remove scenario: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireScenarioParams asserts that the URL's suite and name path
// parameters are well-formed (per nameRE). On failure, writes a 400
// response and returns ok=false so the caller bails. Verb is "require"
// — the function GUARDS rather than just parses; the side-effect
// response writing is part of its job.
func requireScenarioParams(w http.ResponseWriter, r *http.Request) (suite, name string, ok bool) {
	suite = r.PathValue("suite")
	name = r.PathValue("name")
	if !nameRE.MatchString(suite) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid suite name %q", suite))
		return "", "", false
	}
	if !nameRE.MatchString(name) {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid scenario name %q", name))
		return "", "", false
	}
	return suite, name, true
}

// resolveScenarioPath finds the on-disk file for a scenario inside
// the given suite directory, matching either exact <name>.yaml or
// *-<name>.yaml (the existing lexical prefix convention). Returns
// os.ErrNotExist when no candidate matches. The caller passes the
// fully-resolved suite directory (from resolveSuiteDir) so the
// scenario lookup is decoupled from the per-topology layout.
func resolveScenarioPath(suiteDir, name string) (string, error) {
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
	return "", fmt.Errorf("scenario %q not found in %s: %w", name, suiteDir, os.ErrNotExist)
}

// resolveDestPath chooses the write destination for PUT. Returns the
// existing file's path (for in-place update, preserving any "NN-"
// prefix) or the fresh-create path <name>.yaml. The status return
// distinguishes 200 (update) from 201 (create) for the response.
//
// Signature mirrors resolveScenarioPath — both take (suiteDir, name)
// so the caller resolves the suite once and reuses the result.
func resolveDestPath(suiteDir, name string) (string, int, error) {
	path, err := resolveScenarioPath(suiteDir, name)
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

// mapFSErrorToStatus translates a filesystem-level error into the
// HTTP status the handler should write. Used by GET/DELETE so a
// missing file produces 404, not 500. Verb-first ("map") rather than
// the descriptor "statusFor…" per §32 — the helper performs a
// translation, not a property lookup.
func mapFSErrorToStatus(err error) int {
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
