package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// Config is the construction-time configuration for the newtlab server.
type Config struct {
	// TopologiesBase is the directory under which topology spec
	// directories live. Defaults to "newtrun/topologies" relative to
	// the working directory.
	TopologiesBase string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger
}

// Server is the newtlab HTTP server.
type Server struct {
	cfg        Config
	logger     *log.Logger
	httpServer *http.Server
	broker     *httputil.Broker[Event]
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
		broker:   httputil.NewBroker[Event](),
		registry: NewDeployRegistry(),
	}
	s.httpServer = &http.Server{
		Handler:      s.buildHandler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no per-request write deadline (SSE friendly)
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Broker exposes the server's event broker. Tests use this to assert
// that deploy goroutines publish events as expected.
func (s *Server) Broker() *httputil.Broker[Event] {
	return s.broker
}

// Registry exposes the deploy registry.
func (s *Server) Registry() *DeployRegistry {
	return s.registry
}

// Handler returns the fully-wired http.Handler. Tests mount this into
// httptest.Server without needing to bind a real port.
func (s *Server) Handler() http.Handler {
	return s.buildHandler()
}

// Start begins listening on addr. Blocks until the server stops.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	s.logger.Printf("newtlab-server listening on %s", addr)
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop gracefully shuts down the server. Cancels every in-flight
// deploy, waits up to 5s for goroutines to drain, then shuts down the
// HTTP listener.
func (s *Server) Stop(ctx context.Context) error {
	s.registry.CancelAll(5 * time.Second)
	return s.httpServer.Shutdown(ctx)
}
