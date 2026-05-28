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
	// it on GET /api/suites and uses it to validate file-backed suite names
	// in PR 2's run-suite endpoint.
	SuitesBase string

	// TopologiesBase is the directory under which topology directories live.
	// Defaults to "newtrun/topologies". Returned by GET /api/topologies.
	TopologiesBase string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger
}

// Server is the newtrun HTTP server.
type Server struct {
	cfg        Config
	logger     *log.Logger
	httpServer *http.Server
	broker     *EventBroker
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
	s := &Server{
		cfg:    cfg,
		logger: cfg.Logger,
		broker: NewEventBroker(),
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

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// buildHandler wires the mux with middleware.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{suite}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{suite}/events", s.handleRunEvents)
	mux.HandleFunc("GET /api/topologies", s.handleListTopologies)
	mux.HandleFunc("GET /api/suites", s.handleListSuites)

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
		infos = append(infos, runInfoFrom(state))
	}
	writeJSON(w, http.StatusOK, infos)
}

// handleGetRun returns the full RunState for one suite.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	suite := r.PathValue("suite")
	if suite == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("suite path parameter required"))
		return
	}
	state, err := newtrun.LoadRunState(suite)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if state == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no state for suite %q", suite))
		return
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
