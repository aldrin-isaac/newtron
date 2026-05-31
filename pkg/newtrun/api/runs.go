package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	if req.Suite == "" && req.SuiteDir == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite or suite_dir is required"))
		return
	}
	if req.Suite != "" && req.SuiteDir != "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite and suite_dir are mutually exclusive"))
		return
	}
	// Default: All=true when neither Scenario nor Target is set, matching
	// the CLI's default behavior.
	if req.Scenario == "" && req.Target == "" && !req.All {
		req.All = true
	}

	// Resolve the suite directory and the run key. When suite_dir is
	// supplied, the run key is the basename of that path (mirrors the
	// original CLI's newtrun.SuiteName behavior). When suite is supplied,
	// the run key is the suite name and the directory is resolved under
	// SuitesBase.
	var suiteDir, suiteKey string
	if req.SuiteDir != "" {
		suiteDir = req.SuiteDir
		suiteKey = filepath.Base(filepath.Clean(req.SuiteDir))
	} else {
		suiteDir = filepath.Join(s.cfg.SuitesBase, req.Suite)
		suiteKey = req.Suite
	}
	if !isDirectory(suiteDir) {
		writeError(w, http.StatusNotFound, fmt.Errorf("suite directory not found: %s", suiteDir))
		return
	}

	// Reserve the suite key. Same-suite re-run rejected as 409.
	entry, err := s.registry.Acquire(suiteKey)
	if err != nil {
		var already *AlreadyRunningError
		if errors.As(err, &already) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
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
		JUnitPath: req.JUnitPath,
		Suite:     suiteKey,
		Keep:      true,
	}

	// Resume from paused state: if a previous run was paused, populate
	// opts.Resume and opts.Completed so the runner skips already-passed
	// scenarios. Mirrors the original CLI behavior — the source of truth
	// is the on-disk state.json, which survives server restarts.
	if existing, err := newtrun.LoadRunState(suiteKey); err == nil && existing != nil {
		if existing.Status == newtrun.SuiteStatusPaused {
			opts.Resume = true
			opts.Completed = make(map[string]newtrun.StepStatus, len(existing.Scenarios))
			for _, sc := range existing.Scenarios {
				if sc.Status != "" {
					opts.Completed[sc.Name] = newtrun.StepStatus(sc.Status)
				}
			}
		}
	}

	// Construct the persistent state record.
	state := &newtrun.RunState{
		Suite:    suiteKey,
		SuiteDir: suiteDir,
		Platform: req.Platform,
		Target:   req.Target,
		Status:   newtrun.SuiteStatusRunning,
		Started:  entry.Started,
	}
	if err := newtrun.SaveRunState(state); err != nil {
		s.registry.Release(suiteKey, &RunResult{Err: err})
		writeError(w, http.StatusInternalServerError, fmt.Errorf("persisting initial state: %w", err))
		return
	}

	// Resolve newtron-server URL: request body wins, else server default.
	newtronURL := req.NewtronServer
	if newtronURL == "" {
		newtronURL = s.cfg.NewtronServer
	}
	// Resolve network ID: request body wins, else server default.
	networkID := req.NetworkID
	if networkID == "" {
		networkID = s.cfg.NetworkID
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
	httpReporter := NewHTTPReporter(s.broker, suiteKey, stateReporter)

	// Construct the runner.
	runner := newtrun.NewRunner(suiteDir)
	runner.ServerURL = newtronURL
	runner.NetworkID = networkID
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
		s.registry.Release(suiteKey, &RunResult{Scenarios: results, Err: runErr})
	}()

	writeJSON(w, http.StatusAccepted, StartRunResponse{
		Suite:   suiteKey,
		Started: entry.Started,
	})
}

// handlePauseRun signals a graceful pause for the given run. Returns 200
// immediately; the runner picks up the pause signal at the next scenario
// boundary via newtrun.CheckPausing. Works uniformly for suite-keyed runs
// and inline UUID-keyed runs via the unified LoadAnyRunState helper.
func (s *Server) handlePauseRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("suite")
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("run id path parameter required"))
		return
	}
	if s.registry.Get(id) == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no active run %q", id))
		return
	}
	state, err := newtrun.LoadAnyRunState(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if state == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no state file for run %q", id))
		return
	}
	state.Status = newtrun.SuiteStatusPausing
	// Persist back to whichever namespace it came from.
	saveErr := persistAnyRunState(id, state)
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, saveErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pausing"})
}

// persistAnyRunState saves state back to whichever namespace the id
// belongs to (inline if state lives under _inline/<id>/, else suite).
func persistAnyRunState(id string, state *newtrun.RunState) error {
	if dir, err := newtrun.InlineStateDir(id); err == nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return newtrun.SaveInlineRunState(state)
		}
	}
	return newtrun.SaveRunState(state)
}

// handleStopRun cancels the runner's context, terminating the run more
// aggressively than pause. Works uniformly for suite and inline runs;
// the path parameter is treated as an opaque run identifier.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("suite")
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("run id path parameter required"))
		return
	}
	entry := s.registry.Get(id)
	if entry == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no active run %q", id))
		return
	}
	if entry.Cancel != nil {
		entry.Cancel()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// handleDeleteRun removes the persistent state for a run. The run must
// be in a terminal state (not active in the registry). Live runs are
// rejected with 409 — clients must stop first. Works uniformly across
// the suite and inline namespaces via RemoveAnyRunState.
func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("suite")
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("run id path parameter required"))
		return
	}
	if s.registry.Get(id) != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("run %q is active; stop it first", id))
		return
	}
	if err := newtrun.RemoveAnyRunState(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// finalizeRunState writes the terminal status to state.json after the
// runner returns. Delegates the status calculation to
// newtrun.SuiteStatusFromOutcome so the disk-persisted status is
// guaranteed to match the SuiteEnd event the Runner emitted on the
// wire — there is exactly one source of truth for this mapping.
func finalizeRunState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error) {
	state.Finished = time.Now()
	state.Status = newtrun.SuiteStatusFromOutcome(runErr, results)
	// The runner discovers the topology from the connected newtron-server
	// after handleStartRun returns; mirror it into the persisted state so
	// `newtrun status` and `GET /run/{id}` can report it.
	if state.Topology == "" && len(results) > 0 {
		state.Topology = results[0].Topology
	}
	if err := newtrun.SaveRunState(state); err != nil {
		// Best-effort: log via the package-level logger if needed.
		_ = err
	}
}

// isDirectory returns true if the path is a directory.
func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// handleStartInlineRun accepts a scenario YAML in the request body, validates
// it against the inline safety policy, and spawns a server-side run keyed by
// a freshly-allocated UUID. The UUID is namespaced separately from the suite
// directory so ad-hoc operator-driven submissions cannot pollute the canonical
// test suite tree (per the inline-runs spec's namespace-separation
// requirement).
//
// Safety guardrails per DESIGN_PRINCIPLES_NEWTRON §13 (Prevent Bad Writes —
// validate before any device-facing operation runs). The policy is built from
// server defaults plus opt-ins on the request: ?allow_reconcile=true permits
// topology-reconcile; ?timeout=<seconds> overrides the wall-time budget.
func (s *Server) handleStartInlineRun(w http.ResponseWriter, r *http.Request) {
	var req InlineRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.ScenarioYAML == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scenario_yaml is required"))
		return
	}

	// Parse the YAML through the same parser file-backed scenarios use.
	// Structural problems (unknown action, missing fields) reject here
	// before the safety policy sees anything.
	scenario, err := newtrun.ParseScenarioBytes([]byte(req.ScenarioYAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scenario_yaml parse: %w", err))
		return
	}

	// Build the per-request safety policy.
	policy := DefaultInlineSafetyPolicy()
	if s.cfg.InlineURLPrefix != "" {
		policy.AllowedURLPrefixes = []string{s.cfg.InlineURLPrefix}
	}
	if req.AllowReconcile {
		policy.AllowReconcile = true
	}
	if req.TimeoutSeconds > 0 {
		policy.WallTimeBudget = time.Duration(req.TimeoutSeconds) * time.Second
	}

	if violation := policy.Validate(scenario); violation != nil {
		writeError(w, http.StatusBadRequest, violation)
		return
	}

	// Allocate a UUID-shaped identifier and reserve the registry key.
	runID := newRunID()
	entry, err := s.registry.Acquire(runID)
	if err != nil {
		// Collision on UUID is astronomically unlikely but handle defensively.
		writeError(w, http.StatusInternalServerError, fmt.Errorf("allocating run id: %w", err))
		return
	}

	// Inline-namespaced initial state.
	state := &newtrun.RunState{
		Suite:    runID,
		Topology: scenario.Topology,
		Status:   newtrun.SuiteStatusRunning,
		Started:  entry.Started,
	}
	if err := newtrun.SaveInlineRunState(state); err != nil {
		s.registry.Release(runID, &RunResult{Err: err})
		writeError(w, http.StatusInternalServerError, fmt.Errorf("persisting initial state: %w", err))
		return
	}

	// Resolve newtron-server URL.
	newtronURL := req.NewtronServer
	if newtronURL == "" {
		newtronURL = s.cfg.NewtronServer
	}

	// Reporter chain: HTTPReporter → StateReporter (inline-namespaced via
	// the Save field), no console output (browser frontends consume the SSE
	// stream).
	stateReporter := &newtrun.StateReporter{
		State: state,
		Save:  newtrun.SaveInlineRunState,
	}
	httpReporter := NewHTTPReporter(s.broker, runID, stateReporter)

	// Wall-time-bounded context. Cancellation falls through to Server.Stop
	// + stop endpoint paths because the entry's Cancel function delegates
	// to the cancelTimeout produced here.
	runCtx, cancelTimeout := context.WithTimeout(context.Background(), policy.WallTimeBudget)
	entry.Cancel = cancelTimeout

	// Build a synthetic scenarios directory holding only this scenario,
	// then point the Runner at it. The Runner's existing
	// ParseAllScenarios path then finds exactly one scenario to run with
	// the rest of its lifecycle (deploy / connect / iterate / report)
	// intact. Per §3 of ai-instructions: this reuses the existing Runner
	// machinery rather than introducing a parallel single-scenario
	// execution path.
	scenariosDir, err := writeInlineScenarioDir(runID, scenario, req.ScenarioYAML)
	if err != nil {
		cancelTimeout()
		s.registry.Release(runID, &RunResult{Err: err})
		writeError(w, http.StatusInternalServerError, fmt.Errorf("staging scenario: %w", err))
		return
	}

	runner := newtrun.NewRunner(scenariosDir)
	runner.ServerURL = newtronURL
	runner.NetworkID = s.cfg.NetworkID
	runner.Progress = httpReporter

	opts := newtrun.RunOptions{
		All:      true,
		Suite:    runID,
		Keep:     true,
		NoDeploy: true, // inline runs operate on already-deployed topology
	}

	go func() {
		defer cancelTimeout()
		results, runErr := runner.Run(runCtx, opts)
		finalizeInlineState(state, results, runErr)
		// Clean up the synthetic scenarios directory.
		_ = os.RemoveAll(scenariosDir)
		s.registry.Release(runID, &RunResult{Scenarios: results, Err: runErr})
	}()

	writeJSON(w, http.StatusAccepted, InlineRunResponse{
		RunID:   runID,
		Started: entry.Started,
	})
}

// writeInlineScenarioDir stages the inline scenario YAML on disk in a
// dedicated directory so the existing Runner.ScenariosDir machinery loads
// exactly this scenario. The directory is removed when the run finishes.
func writeInlineScenarioDir(runID string, scenario *newtrun.Scenario, yaml string) (string, error) {
	inlineDir, err := newtrun.InlineStateDir(runID)
	if err != nil {
		return "", err
	}
	scenariosDir := filepath.Join(inlineDir, "scenarios")
	if err := os.MkdirAll(scenariosDir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(scenariosDir, "inline.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		return "", err
	}
	return scenariosDir, nil
}

// finalizeInlineState mirrors finalizeRunState but persists to the inline
// namespace via SaveInlineRunState. The status mapping (including the
// inline-budget DeadlineExceeded → aborted case) lives in
// newtrun.SuiteStatusFromOutcome so suite and inline runs share one
// source of truth.
func finalizeInlineState(state *newtrun.RunState, results []*newtrun.ScenarioResult, runErr error) {
	state.Finished = time.Now()
	state.Status = newtrun.SuiteStatusFromOutcome(runErr, results)
	_ = newtrun.SaveInlineRunState(state)
}

// newRunID returns a fresh run identifier. UUIDv4-shaped (8-4-4-4-12 hex).
// Using crypto/rand directly avoids pulling in a UUID library; the
// substantive property we need is collision-resistance + opacity.
func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is astronomical; if it does fail we'd rather
		// crash than emit a predictable ID.
		panic("crypto/rand failed: " + err.Error())
	}
	// Set version (4) and variant (RFC 4122).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b[:])
	return hexStr[0:8] + "-" + hexStr[8:12] + "-" + hexStr[12:16] + "-" + hexStr[16:20] + "-" + hexStr[20:32]
}

// ----- run-read endpoints -----
//
// Reads + their server-restart reconcile helper live alongside the
// run write/lifecycle handlers per §28 (File-Level Feature Cohesion).
// reconcileStaleStatus is shared between the list and get paths per
// §7 — the rule must not be duplicated.

// reconcileStaleStatus is the server-restart-honesty rule (HLD §9.3):
// if the on-disk state claims a run is still running or pausing but
// the live registry has no entry for it, the previous server instance
// crashed or was killed before finalizing — relabel the in-memory
// copy as aborted so the wire never carries a stale running signal.
// The state file itself is not rewritten; the next pass through the
// finalizer will persist whatever final status applies, and the wire
// only ever shows honest data.
func (s *Server) reconcileStaleStatus(state *newtrun.RunState, runKey string) {
	if state == nil {
		return
	}
	if state.Status != newtrun.SuiteStatusRunning && state.Status != newtrun.SuiteStatusPausing {
		return
	}
	if s.registry.Get(runKey) != nil {
		return
	}
	state.Status = newtrun.SuiteStatusAborted
}

// handleListRuns returns a summary of every suite-run discoverable on the
// filesystem under ~/.newtron/newtrun/<suite>/state.json.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	suites, err := newtrun.ListSuiteStates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	infos := make([]RunInfo, 0, len(suites))
	for _, suite := range suites {
		state, err := newtrun.LoadRunState(suite)
		if err != nil {
			// One bad state file shouldn't sink the whole list. Log it and
			// skip — operators expect partial results to be readable.
			s.logger.Printf("list-runs: skipping %s: %v", suite, err)
			continue
		}
		if state == nil {
			continue
		}
		s.reconcileStaleStatus(state, suite)
		infos = append(infos, runInfoFrom(state))
	}
	writeJSON(w, http.StatusOK, infos)
}

// handleGetRun returns the full RunState for one run. Accepts either a
// suite name or an inline UUID; LoadAnyRunState resolves both.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("suite")
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("run id path parameter required"))
		return
	}
	state, err := newtrun.LoadAnyRunState(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if state == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no state for run %q", id))
		return
	}
	s.reconcileStaleStatus(state, id)
	// Per §46 the wire shape mirrors the substrate: RunState has JSON tags
	// already; serialize it directly without a wrapper type.
	writeJSON(w, http.StatusOK, state)
}

// handleRunEvents opens a Server-Sent Events stream for the given suite.
// The connection stays open until the client disconnects or the server
// shuts down. Heartbeat comments every 30s keep intermediaries from
// timing out the connection during quiet periods.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Send an initial comment line so the client knows the connection is open
	// even when no events arrive. SSE comment lines start with ":".
	fmt.Fprintf(w, ": subscribed to %s\n\n", suite)
	flusher.Flush()

	events, unsub := s.broker.Subscribe(suite)
	defer unsub()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev.Payload)
			if err != nil {
				s.logger.Printf("sse marshal: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}
