package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Config is the construction-time configuration for the newtlab server.
type Config struct {
	// TopologiesBase is the directory under which topology spec
	// directories live. Defaults to "newtrun/topologies" relative to
	// the working directory. Used by GET /api/topologies/{name}/...
	// handlers when they need to resolve "{name}" to a spec dir for
	// newtlab.NewLab(). Direct spec-dir paths (operator-provided via
	// future flags) bypass this base.
	TopologiesBase string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger
}

// Server is the newtlab HTTP server.
type Server struct {
	cfg        Config
	logger     *log.Logger
	httpServer *http.Server
	broker     *EventBroker
	registry   *DeployRegistry
}

// NewServer constructs a server with the given config. The HTTP
// listener is not started until Start is called.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.TopologiesBase == "" {
		cfg.TopologiesBase = "newtrun/topologies"
	}
	s := &Server{
		cfg:      cfg,
		logger:   cfg.Logger,
		broker:   NewEventBroker(),
		registry: NewDeployRegistry(),
	}
	s.httpServer = &http.Server{
		Handler: s.buildHandler(),
		// SSE connections can be long-lived; the server-wide
		// WriteTimeout must accommodate this. Matches newtrun-server's
		// rationale.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no per-request write deadline (SSE friendly)
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Broker exposes the server's EventBroker. Tests use this to assert
// that deploy goroutines publish events as expected.
func (s *Server) Broker() *EventBroker {
	return s.broker
}

// Registry exposes the deploy registry. Tests use this to assert
// per-topology mutual exclusion.
func (s *Server) Registry() *DeployRegistry {
	return s.registry
}

// Handler returns the fully-wired http.Handler. Tests mount this into
// an httptest.Server without needing to bind a real port.
func (s *Server) Handler() http.Handler {
	return s.buildHandler()
}

// Start begins listening on the given address. Blocks until the server
// stops.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	s.logger.Printf("newtlab-server listening on %s", addr)
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop gracefully shuts down the server. Cancels every in-flight deploy
// and waits up to 5 seconds for them to drain, then shuts down the HTTP
// listener.
func (s *Server) Stop(ctx context.Context) error {
	s.registry.CancelAll(5 * time.Second)
	return s.httpServer.Shutdown(ctx)
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

// decodeJSON decodes the request body into v, returning a 400 Bad
// Request error envelope if the body is malformed. Empty bodies are
// allowed — handlers that need a body should check the zero-value of
// their decoded struct.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("malformed JSON body: %w", err)
	}
	return nil
}
