package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
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
	// StartRunRequest overrides this. Defaults to http://127.0.0.1:18080.
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
	broker     *httputil.Broker[Event]
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
		cfg.NewtronServer = "http://127.0.0.1:18080"
	}
	if cfg.NetworkID == "" {
		cfg.NetworkID = "default"
	}
	s := &Server{
		cfg:      cfg,
		logger:   cfg.Logger,
		broker:   httputil.NewBroker[Event](),
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

// Broker exposes the server's httputil.Broker[Event]. PR 2 wires server-side Runner
// invocations to publish events through this broker; PR 1 leaves it idle.
func (s *Server) Broker() *httputil.Broker[Event] {
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

// Handler returns the fully-wired http.Handler (mux + middleware). Used
// by external CLI E2E tests to mount the real server into an
// httptest.Server without needing to spawn a subprocess — the binary
// being tested points its client at the httptest URL via NEWTRUN_SERVER.
func (s *Server) Handler() http.Handler {
	return s.buildHandler()
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
	handler = httputil.Logger(s.logger)(handler)
	handler = httputil.RequestID(handler)
	handler = httputil.Recovery(s.logger)(handler)
	return handler
}

// ----- handlers -----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: "0.1.0-dev",
	})
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

