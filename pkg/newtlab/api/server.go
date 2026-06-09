package api

import (
	"log"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// Config is the construction-time configuration for the newtlab server.
type Config struct {
	// TopologiesBase is the directory under which topology spec
	// directories live. Defaults to "newtrun/topologies" relative to
	// the working directory. This path is sent to newtron-server during
	// registration so newtron knows where to read the spec files
	// (DESIGN_PRINCIPLES §27 — newtron owns spec files).
	TopologiesBase string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger

	// NewtronClientFor returns a newtron SpecClient configured for the
	// named network ID. newtlab uses one client per lab — by the #116
	// convention, the network ID equals the lab name, so each lab has
	// its own newtron registration slot. Composed in by cmd/newtlab-
	// server or cmd/newt-server, typically wrapping
	// newtronclient.New(baseURL, networkID).
	//
	// Required for topology lifecycle operations (deploy / status /
	// start / stop) — newtlab no longer reads spec JSON files directly
	// per §27.
	NewtronClientFor func(networkID string) newtlab.SpecClient

	// OrchestratorURL is the publicly-reachable base URL of this
	// newtlab-server (or the composed newt-server). It is written into
	// the bridge config sent to each newtlink worker so the worker
	// can push BridgeStats back here (#118). Composed in by cmd/newt-
	// server or cmd/newtlab-server from its own listen address (with
	// a publicly-reachable host substitution when the listener is
	// bound to 0.0.0.0). Required for Deploy — empty causes the
	// setupBridges path inside newtlab.Lab.Deploy to fail rather than
	// spawn workers that push to nowhere.
	OrchestratorURL string
}

// Server is the newtlab HTTP server. The HTTP listener lifecycle
// (Start / Stop) comes from the embedded *httputil.Server; this type
// holds only newtlab-specific state.
type Server struct {
	*httputil.Server
	cfg        Config
	logger     *log.Logger
	broker     *httputil.Broker[Event]
	registry   *DeployRegistry
	statsStore *BridgeStatsStore
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
		cfg:        cfg,
		logger:     cfg.Logger,
		broker:     httputil.NewBroker[Event](),
		registry:   NewDeployRegistry(),
		statsStore: NewBridgeStatsStore(),
	}
	s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
		httputil.ServerLabel("newtlab-server"),
		// SSE-friendly: no per-request write deadline.
		httputil.WriteTimeout(0),
		// On shutdown, cancel every in-flight deploy with a 5s drain
		// window before the HTTP listener closes.
		httputil.OnShutdown(func() {
			s.registry.CancelAll(5 * time.Second)
		}),
	)
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
	return s.HTTPServer().Handler
}
