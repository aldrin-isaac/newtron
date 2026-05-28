package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// handleStartRun creates a server-side run of a file-backed suite. The
// runner executes in a goroutine; the HTTP response returns immediately
// with the run's identity. Per-suite concurrency is enforced by the
// registry: same-suite re-runs return 409 with the active run's age in
// the error message; different suites run concurrently.
func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req StartRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite is required"))
		return
	}
	// Default: All=true when neither Scenario nor Target is set, matching
	// the CLI's default behavior.
	if req.Scenario == "" && req.Target == "" && !req.All {
		req.All = true
	}

	// Reserve the suite key. Same-suite re-run rejected as 409.
	entry, err := s.registry.Acquire(req.Suite)
	if err != nil {
		var already *AlreadyRunningError
		if errors.As(err, &already) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Resolve suite directory under the configured base.
	suiteDir := filepath.Join(s.cfg.SuitesBase, req.Suite)
	if !directoryExists(suiteDir) {
		s.registry.Release(req.Suite, &RunResult{Err: fmt.Errorf("suite directory not found: %s", suiteDir)})
		writeError(w, http.StatusNotFound, fmt.Errorf("suite %q not found at %s", req.Suite, suiteDir))
		return
	}

	// Build the run options. The server's runner uses its own context;
	// the SuiteOptions.Suite hooks the file-based pause check that the
	// existing CLI uses.
	opts := newtrun.RunOptions{
		Scenario:  req.Scenario,
		Target:    req.Target,
		All:       req.All,
		Platform:  req.Platform,
		NoDeploy:  req.NoDeploy,
		Verbose:   req.Verbose,
		Suite:     req.Suite,
		Keep:      true,
	}

	// Construct the persistent state record.
	state := &newtrun.RunState{
		Suite:    req.Suite,
		SuiteDir: suiteDir,
		Platform: req.Platform,
		Target:   req.Target,
		Status:   newtrun.SuiteStatusRunning,
		Started:  entry.Started,
	}
	if err := newtrun.SaveRunState(state); err != nil {
		s.registry.Release(req.Suite, &RunResult{Err: err})
		writeError(w, http.StatusInternalServerError, fmt.Errorf("persisting initial state: %w", err))
		return
	}

	// Resolve newtron-server URL: request body wins, else server default.
	newtronURL := req.NewtronServer
	if newtronURL == "" {
		newtronURL = s.cfg.NewtronServer
	}

	// Set up the reporter chain. HTTPReporter publishes to the broker
	// (which feeds SSE subscribers); StateReporter persists per-step
	// state to disk; no console reporter on the server side (the CLI's
	// --monitor mode and the browser frontend both consume events via
	// the broker or the state file).
	stateReporter := &newtrun.StateReporter{
		Inner: nil,
		State: state,
	}
	httpReporter := NewHTTPReporter(s.broker, req.Suite, stateReporter)

	// Construct the runner.
	runner := newtrun.NewRunner(suiteDir)
	runner.ServerURL = newtronURL
	runner.NetworkID = s.cfg.NetworkID
	runner.Progress = httpReporter

	// Cancellable context for the run. Stop endpoints call entry.Cancel.
	// Server.Stop calls registry.CancelAll which calls every entry.Cancel.
	runCtx, cancel := context.WithCancel(context.Background())
	entry.Cancel = cancel

	// Spawn the run.
	go func() {
		defer cancel()
		results, runErr := runner.Run(runCtx, opts)
		finalizeRunState(state, results, runErr)
		s.registry.Release(req.Suite, &RunResult{Scenarios: results, Err: runErr})
	}()

	writeJSON(w, http.StatusAccepted, StartRunResponse{
		Suite:   req.Suite,
		Started: entry.Started,
	})
}

// handlePauseRun signals a graceful pause for the given suite. Returns 200
// immediately; the runner picks up the pause signal at the next scenario
// boundary via newtrun.CheckPausing.
func (s *Server) handlePauseRun(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	if s.registry.Get(suite) == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no active run for suite %q", suite))
		return
	}
	state, err := newtrun.LoadRunState(suite)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if state == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no state file for suite %q", suite))
		return
	}
	state.Status = newtrun.SuiteStatusPausing
	if err := newtrun.SaveRunState(state); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pausing"})
}

// handleStopRun cancels the runner's context, terminating the run more
// aggressively than pause. The active scenario's in-flight HTTP calls
// see the cancellation; subsequent scenarios are skipped.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	entry := s.registry.Get(suite)
	if entry == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no active run for suite %q", suite))
		return
	}
	if entry.Cancel != nil {
		entry.Cancel()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// handleDeleteRun removes the persistent state for a suite. The run must
// be in a terminal state (not active in the registry). Live runs are
// rejected with 409 — clients must stop first.
func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	if s.registry.Get(suite) != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("run for suite %q is active; stop it first", suite))
		return
	}
	if err := newtrun.RemoveRunState(suite); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// finalizeRunState writes the terminal status to state.json after the
// runner returns. Mirrors the existing CLI's finalizeRunState in
// cmd/newtrun/cmd_start.go (kept in sync — same logic, different home).
func finalizeRunState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error) {
	state.Finished = time.Now()
	switch {
	case runErr != nil:
		var pauseErr *newtrun.PauseError
		if errors.As(runErr, &pauseErr) {
			state.Status = newtrun.SuiteStatusPaused
		} else if errors.Is(runErr, context.Canceled) {
			state.Status = newtrun.SuiteStatusAborted
		} else {
			state.Status = newtrun.SuiteStatusFailed
		}
	default:
		// Inspect per-scenario results: any failure → failed; else complete.
		state.Status = newtrun.SuiteStatusComplete
		for _, r := range results {
			if r != nil && (r.Status == newtrun.StepStatusFailed || r.Status == newtrun.StepStatusError) {
				state.Status = newtrun.SuiteStatusFailed
				break
			}
		}
	}
	if err := newtrun.SaveRunState(state); err != nil {
		// Best-effort: log via the package-level logger if needed.
		_ = err
	}
}

// directoryExists returns true if the path is a directory.
func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
