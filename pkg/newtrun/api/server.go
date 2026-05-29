package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// Config is the construction-time configuration for the newtrun server.
type Config struct {
	// SuitesBase is the directory under which suite directories live. Defaults
	// to "newtrun/suites" relative to the working directory. The server reads
	// it on GET /api/suites and validates file-backed suite names against it
	// when handling POST /api/runs.
	SuitesBase string

	// TopologiesBase is the directory under which topology directories live.
	// Defaults to "newtrun/topologies". Returned by GET /api/topologies.
	TopologiesBase string

	// NewtronServer is the newtron-server URL the server-side runners
	// connect to for topology discovery. Per-run NewtronServer in the
	// StartRunRequest overrides this. Defaults to http://127.0.0.1:8080.
	NewtronServer string

	// NetworkID is the network identifier server-side runners pass to
	// newtron-server. Defaults to "default".
	NetworkID string

	// InlineURLPrefix restricts the URLs that the `newtron` action in an
	// inline-submitted scenario may call. Defaults to NewtronServer's
	// base URL — inline scenarios can only call the configured
	// newtron-server. Empty string disables URL restriction (used in
	// tests). The inline-runs safety spec mandates this guardrail.
	InlineURLPrefix string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger
}

// Server is the newtrun HTTP server.
type Server struct {
	cfg        Config
	logger     *log.Logger
	httpServer *http.Server
	broker     *EventBroker
	registry   *RunRegistry
}

// NewServer constructs a server with the given config. The HTTP listener is
// not started until Start is called.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.SuitesBase == "" {
		cfg.SuitesBase = "newtrun/suites"
	}
	if cfg.TopologiesBase == "" {
		cfg.TopologiesBase = "newtrun/topologies"
	}
	if cfg.NewtronServer == "" {
		cfg.NewtronServer = "http://127.0.0.1:8080"
	}
	if cfg.NetworkID == "" {
		cfg.NetworkID = "default"
	}
	s := &Server{
		cfg:      cfg,
		logger:   cfg.Logger,
		broker:   NewEventBroker(),
		registry: NewRunRegistry(),
	}
	s.httpServer = &http.Server{
		Handler: s.buildHandler(),
		// SSE connections can be long-lived; the server-wide WriteTimeout
		// must accommodate this. Per-request handler timeouts apply to
		// non-SSE endpoints via http.TimeoutHandler in buildHandler if
		// needed; the simpler approach for v0 is a generous server-wide
		// WriteTimeout and rely on context cancellation from the client.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no per-request write deadline (SSE friendly)
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Broker exposes the server's EventBroker. PR 2 wires server-side Runner
// invocations to publish events through this broker; PR 1 leaves it idle.
func (s *Server) Broker() *EventBroker {
	return s.broker
}

// Start begins listening on the given address. Blocks until the server stops.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	s.logger.Printf("newtrun-server listening on %s", addr)
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the server. Cancels every in-flight run,
// waits up to 5 seconds for them to drain, then shuts down the HTTP
// listener.
func (s *Server) Stop(ctx context.Context) error {
	s.registry.CancelAll(5 * time.Second)
	return s.httpServer.Shutdown(ctx)
}

// Registry exposes the run registry. Tests use this to inspect in-flight
// state; PR 3's inline-runs handler will use it directly.
func (s *Server) Registry() *RunRegistry {
	return s.registry
}

// buildHandler wires the mux with middleware.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("POST /api/runs/inline", s.handleStartInlineRun)
	mux.HandleFunc("GET /api/runs/{suite}", s.handleGetRun)
	mux.HandleFunc("DELETE /api/runs/{suite}", s.handleDeleteRun)
	mux.HandleFunc("POST /api/runs/{suite}/pause", s.handlePauseRun)
	mux.HandleFunc("POST /api/runs/{suite}/stop", s.handleStopRun)
	mux.HandleFunc("GET /api/runs/{suite}/events", s.handleRunEvents)
	mux.HandleFunc("GET /api/topologies", s.handleListTopologies)
	mux.HandleFunc("GET /api/suites", s.handleListSuites)
	mux.HandleFunc("POST /api/suites", s.handleCreateSuite)
	mux.HandleFunc("DELETE /api/suites/{suite}", s.handleDeleteSuite)
	mux.HandleFunc("GET /api/suites/{suite}/scenarios", s.handleListSuiteScenarios)
	mux.HandleFunc("GET /api/suites/{suite}/scenarios/{name}", s.handleGetScenario)
	mux.HandleFunc("PUT /api/suites/{suite}/scenarios/{name}", s.handlePutScenario)
	mux.HandleFunc("DELETE /api/suites/{suite}/scenarios/{name}", s.handleDeleteScenario)

	var handler http.Handler = mux
	handler = withLogger(s.logger)(handler)
	handler = withRequestID(handler)
	handler = withRecovery(s.logger)(handler)
	return handler
}

// ----- handlers -----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: "0.1.0-dev",
	})
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
		// Same server-restart-honesty reconciliation as handleGetRun:
		// a state file that says running but isn't in the live registry
		// belongs to a previous server instance that died unfinalized.
		if (state.Status == newtrun.SuiteStatusRunning || state.Status == newtrun.SuiteStatusPausing) &&
			s.registry.Get(suite) == nil {
			state.Status = newtrun.SuiteStatusAborted
		}
		infos = append(infos, runInfoFrom(state))
	}
	writeJSON(w, http.StatusOK, infos)
}

// handleGetRun returns the full RunState for one run. Accepts either a
// suite name or an inline UUID; LoadAnyRunState resolves both.
//
// Server-restart honesty: if state.json claims the run is running but
// the registry has no entry for it, the previous server instance
// crashed or was killed before it could finalize the state. Reconcile
// on the fly to "aborted" rather than returning a stale running
// signal — the registry is the live source of truth, and an
// unreconciled running state would mislead the CLI's status display
// indefinitely.
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
	if (state.Status == newtrun.SuiteStatusRunning || state.Status == newtrun.SuiteStatusPausing) &&
		s.registry.Get(id) == nil {
		state.Status = newtrun.SuiteStatusAborted
	}
	// Per §46 the wire shape mirrors the substrate: RunState has JSON tags
	// already; serialize it directly without a wrapper type.
	writeJSON(w, http.StatusOK, state)
}

// handleRunEvents opens a Server-Sent Events stream for the given suite. The
// connection stays open until the client disconnects or the server shuts
// down. In PR 1 no events are published (no server-side runs exist yet);
// the connection is held open and clients receive nothing until PR 2 wires
// up server-side execution.
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

	// Heartbeat every 30s so intermediaries (proxies, browsers) don't time
	// out the connection during quiet periods.
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

// handleListTopologies returns the topology names discoverable under
// TopologiesBase. v0 implementation: list immediate subdirectories.
func (s *Server) handleListTopologies(w http.ResponseWriter, r *http.Request) {
	names, err := listSubdirs(s.cfg.TopologiesBase)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, TopologiesResponse{Topologies: names})
}

// handleListSuites returns the suite names discoverable under SuitesBase.
func (s *Server) handleListSuites(w http.ResponseWriter, r *http.Request) {
	names, err := listSubdirs(s.cfg.SuitesBase)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, SuitesResponse{Suites: names})
}

// handleListSuiteScenarios returns the scenarios in the named suite as
// summaries (name, topology, step count, requires). The browser suite
// picker and `newtrun list <suite>` both use this. Scenario authoring
// (full CRUD via PUT/DELETE) is tracked separately in issue #33.
func (s *Server) handleListSuiteScenarios(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	dir := filepath.Join(s.cfg.SuitesBase, suite)
	scenarios, err := newtrun.ParseAllScenarios(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Errorf("suite %q not found", suite))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if newtrun.HasRequires(scenarios) {
		if sorted, sortErr := newtrun.ValidateDependencyGraph(scenarios); sortErr == nil {
			scenarios = sorted
		}
	}
	resp := SuiteScenariosResponse{Suite: suite}
	if len(scenarios) > 0 {
		resp.Topology = scenarios[0].Topology
	}
	resp.Scenarios = make([]ScenarioSummary, len(scenarios))
	for i, sc := range scenarios {
		resp.Scenarios[i] = ScenarioSummary{
			Name:        sc.Name,
			Description: sc.Description,
			Topology:    sc.Topology,
			Platform:    sc.Platform,
			StepCount:   len(sc.Steps),
			Requires:    sc.Requires,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// listSubdirs returns the names of immediate subdirectories. Missing base
// directories return an empty slice rather than an error — the server may
// run in deployments without topology/suite trees yet.
func listSubdirs(base string) ([]string, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", base, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, filepath.Base(e.Name()))
	}
	return names, nil
}

// ----- response helpers -----

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{Data: data})
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{Error: err.Error()})
}
