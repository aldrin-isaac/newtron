package newtser

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// Config is the construction-time configuration for newtser.
type Config struct {
	// EvictionInterval is how often the eviction loop runs. Default 30s.
	EvictionInterval time.Duration

	// EvictionMaxAge is how stale a registration may be before
	// eviction. Default 90s — three heartbeat intervals of 30s.
	EvictionMaxAge time.Duration

	// Logger is the logger newtser uses. Defaults to log.Default().
	Logger *log.Logger
}

// Server is the newtser HTTP front. It composes httputil.Server for
// HTTP listener lifecycle, a Registry for service tracking, and a
// Proxy for path-based dispatch.
type Server struct {
	*httputil.Server
	cfg      Config
	logger   *log.Logger
	registry *Registry
	proxy    *Proxy

	// evictCancel stops the background eviction loop on Stop().
	evictCancel context.CancelFunc
}

// NewServer constructs a newtser server.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.EvictionInterval == 0 {
		cfg.EvictionInterval = 30 * time.Second
	}
	if cfg.EvictionMaxAge == 0 {
		cfg.EvictionMaxAge = 90 * time.Second
	}
	registry := NewRegistry()
	proxy := NewProxy(registry, cfg.Logger)
	s := &Server{
		cfg:      cfg,
		logger:   cfg.Logger,
		registry: registry,
		proxy:    proxy,
	}
	s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
		httputil.ServerLabel("newtser"),
		// SSE-friendly: the proxy must hold long-lived streaming
		// connections, so no per-request write deadline.
		httputil.WriteTimeout(0),
		httputil.OnShutdown(func() {
			if s.evictCancel != nil {
				s.evictCancel()
			}
		}),
	)
	return s
}

// Registry exposes the in-memory service registry. Tests use it to
// assert registration / deregistration / eviction behavior.
func (s *Server) Registry() *Registry { return s.registry }

// Handler returns the fully-wired http.Handler. Tests mount this into
// httptest.Server.
func (s *Server) Handler() http.Handler {
	return s.HTTPServer().Handler
}

// Start begins listening and starts the eviction loop.
func (s *Server) Start(addr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.evictCancel = cancel
	go s.registry.RunEvictionLoop(ctx, s.cfg.EvictionInterval, s.cfg.EvictionMaxAge, func(name string) {
		s.logger.Printf("evicted stale registration %q (no heartbeat for %s)", name, s.cfg.EvictionMaxAge)
	})
	return s.Server.Start(addr)
}

// buildHandler wires the mux with middleware. Two route classes:
//
//  1. newtser's own routes under /newtser/v1/ — health, services
//     CRUD. These are handled directly without proxying.
//  2. The catch-all "/" route — every other path goes through the
//     reverse-proxy, dispatched by first path segment via the
//     registry.
//
// The /newtser/v1/ prefix is reserved (validateRegister rejects
// "newtser" as a service name), so there is no conflict between the
// meta-routes and proxied routes.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	// newtser's own routes (meta).
	mux.HandleFunc("GET /newtser/v1/health", s.handleHealth)
	mux.HandleFunc("GET /newtser/v1/services", s.handleListServices)
	mux.HandleFunc("POST /newtser/v1/services", s.handleRegister)
	mux.HandleFunc("POST /newtser/v1/services/{name}/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("DELETE /newtser/v1/services/{name}", s.handleDeregister)

	// Catch-all: every other path proxies to a registered backend.
	mux.Handle("/", s.proxy)

	var handler http.Handler = mux
	handler = httputil.Logger(s.logger)(handler)
	handler = httputil.RequestID(handler)
	handler = httputil.Recovery(s.logger)(handler)
	return handler
}
