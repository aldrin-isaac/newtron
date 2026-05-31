package httputil

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

// Server is the embedded base type each newtron-project HTTP server
// composes into its own Server struct. It owns the *http.Server, the
// shared listen log line, the lifecycle (Start / Stop), and the
// pre-shutdown hook each engine uses to cancel its in-flight work
// (newtrun's RunRegistry.CancelAll, newtlab's DeployRegistry.CancelAll).
//
// Engine-specific Server types embed *httputil.Server and pass the
// fully-composed http.Handler (mux + middleware chain) at construction
// time:
//
//	type Server struct {
//	    *httputil.Server
//	    cfg Config
//	    broker *httputil.Broker[Event]
//	    registry *DeployRegistry
//	}
//
//	func NewServer(cfg Config) *Server {
//	    s := &Server{ ... }
//	    s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
//	        httputil.ServerLabel("newtlab-server"),
//	        httputil.OnShutdown(func() {
//	            s.registry.CancelAll(5 * time.Second)
//	        }),
//	    )
//	    return s
//	}
//
// Why embed instead of compose: every server's Start / Stop forwarders
// would be identical wrappers; embedding makes them disappear. Engine-
// specific fields and methods live on the outer type; lifecycle is
// inherited.
type Server struct {
	httpServer *http.Server
	logger     *log.Logger
	label      string // log-prefix label, e.g. "newtrun-server"
	onShutdown []func()
}

// ServerOption tunes the base server at construction time.
type ServerOption func(*serverConfig)

type serverConfig struct {
	label         string
	readTimeout   time.Duration
	writeTimeout  time.Duration
	idleTimeout   time.Duration
	onShutdown    []func()
}

// ServerLabel sets the prefix used in the startup log line. Default is
// "http-server"; engines pass their own ("newtrun-server", etc.) so
// "<label> listening on <addr>" reads correctly.
func ServerLabel(s string) ServerOption {
	return func(c *serverConfig) { c.label = s }
}

// ReadTimeout overrides the *http.Server ReadTimeout. Default 30s,
// matching the existing newtron/newtrun/newtlab settings.
func ReadTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.readTimeout = d }
}

// WriteTimeout overrides the *http.Server WriteTimeout. Default 0
// (no per-request write deadline) so SSE handlers can hold long-lived
// connections without the server-wide timeout killing them. Engines
// that need a finite write timeout (newtron-server uses 5min) pass
// it explicitly.
func WriteTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.writeTimeout = d }
}

// IdleTimeout overrides the *http.Server IdleTimeout. Default 120s.
func IdleTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.idleTimeout = d }
}

// OnShutdown registers a function to run before the HTTP listener
// shuts down. Engines use it to cancel in-flight goroutines they own
// (RunRegistry.CancelAll, DeployRegistry.CancelAll). Multiple hooks
// run in registration order. The HTTP listener then takes whatever
// time remains in the context to drain.
func OnShutdown(fn func()) ServerOption {
	return func(c *serverConfig) { c.onShutdown = append(c.onShutdown, fn) }
}

// NewServer constructs a base server. handler is the
// already-middleware-wrapped http.Handler the engine wants to serve.
// logger may be nil; if so, log.Default() is used.
func NewServer(handler http.Handler, logger *log.Logger, opts ...ServerOption) *Server {
	cfg := serverConfig{
		label:        "http-server",
		readTimeout:  30 * time.Second,
		writeTimeout: 0,
		idleTimeout:  120 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		logger:     logger,
		label:      cfg.label,
		onShutdown: cfg.onShutdown,
		httpServer: &http.Server{
			Handler:      handler,
			ReadTimeout:  cfg.readTimeout,
			WriteTimeout: cfg.writeTimeout,
			IdleTimeout:  cfg.idleTimeout,
		},
	}
}

// Start begins listening on addr. Blocks until the server stops.
// Returns nil on graceful shutdown (http.ErrServerClosed), or the
// underlying listener error otherwise.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	s.logger.Printf("%s listening on %s", s.label, addr)
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop runs every OnShutdown hook in registration order, then
// gracefully shuts down the HTTP listener with the given context as
// the drain deadline.
func (s *Server) Stop(ctx context.Context) error {
	for _, fn := range s.onShutdown {
		fn()
	}
	return s.httpServer.Shutdown(ctx)
}

// HTTPServer exposes the underlying *http.Server. Tests use it to read
// configured timeouts; engines should not need it.
func (s *Server) HTTPServer() *http.Server { return s.httpServer }
