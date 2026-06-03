package api

import (
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
	// it on GET /newtrun/v1/suites and validates file-backed suite names against
	// it when handling POST /newtrun/v1/runs.
	SuitesBase string

	// TopologiesBase is the directory under which topology directories live.
	// Defaults to "newtrun/topologies". Returned by GET /newtrun/v1/topologies.
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

// Server is the newtrun HTTP server. The HTTP listener lifecycle
// (Start / Stop) comes from the embedded *httputil.Server.
type Server struct {
	*httputil.Server
	cfg      Config
	logger   *log.Logger
	broker   *httputil.Broker[Event]
	registry *RunRegistry
}

// NewServer constructs a server with the given config. The HTTP
// listener is not started until Start is called.
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
	s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
		httputil.ServerLabel("newtrun-server"),
		// SSE-friendly: no per-request write deadline.
		httputil.WriteTimeout(0),
		httputil.OnShutdown(func() {
			s.registry.CancelAll(5 * time.Second)
		}),
	)
	return s
}

// Broker exposes the server's event broker.
func (s *Server) Broker() *httputil.Broker[Event] {
	return s.broker
}

// Registry exposes the run registry.
func (s *Server) Registry() *RunRegistry {
	return s.registry
}

// Handler returns the fully-wired http.Handler. Used by external CLI
// E2E tests to mount the real server into an httptest.Server without
// spawning a subprocess.
func (s *Server) Handler() http.Handler {
	return s.HTTPServer().Handler
}

// buildHandler wires the mux with middleware.
//
// All routes live under /newtrun/v1/. The version prefix is the breaking-
// change escape hatch: v2/ ships alongside v1/ when the wire shape
// changes. Per DESIGN_PRINCIPLES_NEWTRON §40 (Greenfield), version
// segments are reserved for external HTTP contracts — newtcon and
// other browser/script consumers — not for internal use.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /newtrun/v1/health", s.handleHealth)
	mux.HandleFunc("GET /newtrun/v1/runs", s.handleListRuns)
	mux.HandleFunc("POST /newtrun/v1/runs", s.handleStartRun)
	mux.HandleFunc("POST /newtrun/v1/runs/inline", s.handleStartInlineRun)
	mux.HandleFunc("GET /newtrun/v1/runs/{suite}", s.handleGetRun)
	mux.HandleFunc("DELETE /newtrun/v1/runs/{suite}", s.handleDeleteRun)
	mux.HandleFunc("POST /newtrun/v1/runs/{suite}/pause", s.handlePauseRun)
	mux.HandleFunc("POST /newtrun/v1/runs/{suite}/stop", s.handleStopRun)
	mux.HandleFunc("GET /newtrun/v1/runs/{suite}/events", s.handleRunEvents)
	mux.HandleFunc("GET /newtrun/v1/topologies", s.handleListTopologies)
	mux.HandleFunc("POST /newtrun/v1/topologies", s.handleCreateTopology)
	mux.HandleFunc("GET /newtrun/v1/suites", s.handleListSuites)
	mux.HandleFunc("POST /newtrun/v1/suites", s.handleCreateSuite)
	mux.HandleFunc("DELETE /newtrun/v1/suites/{suite}", s.handleDeleteSuite)
	mux.HandleFunc("GET /newtrun/v1/suites/{suite}/scenarios", s.handleListSuiteScenarios)
	mux.HandleFunc("GET /newtrun/v1/suites/{suite}/scenarios/{name}", s.handleGetScenario)
	mux.HandleFunc("PUT /newtrun/v1/suites/{suite}/scenarios/{name}", s.handlePutScenario)
	mux.HandleFunc("DELETE /newtrun/v1/suites/{suite}/scenarios/{name}", s.handleDeleteScenario)

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
