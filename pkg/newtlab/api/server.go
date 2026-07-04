package api

import (
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// Config is the construction-time configuration for the newtlab server.
type Config struct {
	// NetworksBase is the directory under which topology spec
	// directories live. Defaults to "networks" relative to
	// the working directory. This path is sent to newtron-server during
	// registration so newtron knows where to read the spec files
	// (DESIGN_PRINCIPLES §27 — newtron owns spec files).
	NetworksBase string

	// Logger is the logger the server uses. Defaults to log.Default().
	Logger *log.Logger

	// NewtronClientFor returns a newtron client configured for the
	// named network ID. newtlab uses one client per lab — by the #116
	// convention, the network ID equals the lab name, so each lab has
	// its own newtron registration slot. Composed in by cmd/newt-server,
	// typically wrapping newtronclient.New(baseURL, networkID).
	//
	// Required for topology lifecycle operations (deploy / status /
	// start / stop) and provisioning (reconcile) — newtlab reaches both
	// spec data and device state through newtron's HTTP API per §27.
	NewtronClientFor func(networkID string) newtlab.NewtronClient

	// OrchestratorURL is the publicly-reachable base URL of newt-server.
	// It is written into the bridge config sent to each newtlink worker
	// so the worker can push BridgeStats back here (#118). Composed in
	// by cmd/newt-server from its own listen address (with a publicly-
	// reachable host substitution when the listener is bound to
	// 0.0.0.0). Required for Deploy — empty causes the setupBridges
	// path inside newtlab.Lab.Deploy to fail rather than
	// spawn workers that push to nowhere.
	OrchestratorURL string

	// TLSConfig enables inter-service mTLS on the TCP listener
	// (auth-design.md L2a). Build with httputil.LoadServerTLSConfig
	// from the operator's --tls-cert / --tls-key / --client-ca flags.
	// nil keeps the default plain-HTTP listener — the disabled state.
	TLSConfig *tls.Config
}

// Server is the newtlab HTTP server. The HTTP listener lifecycle
// (Start / Stop) comes from the embedded *httputil.Server; this type
// holds only newtlab-specific state.
type Server struct {
	*httputil.Server
	cfg        Config
	logger     *log.Logger
	broker     *httputil.Broker[Event]
	registry   *LabOpRegistry
	statsStore *BridgeStatsStore
	// tokenFor returns the per-lab telemetry token used to authenticate a
	// newtlink BridgeStats push (handlePushBridgeStats). Defaults to the
	// on-disk LabState.TelemetryToken; injectable so handler tests don't
	// touch ~/.newtlab.
	tokenFor func(lab string) (string, error)
}

// NewServer constructs a server with the given config. The HTTP
// listener is not started until Start is called.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.NetworksBase == "" {
		cfg.NetworksBase = "networks"
	}
	s := &Server{
		cfg:        cfg,
		logger:     cfg.Logger,
		broker:     httputil.NewBroker[Event](),
		registry:   NewLabOpRegistry(),
		statsStore: NewBridgeStatsStore(),
		tokenFor:   defaultTelemetryTokenLookup,
	}
	s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
		httputil.ServerLabel("newtlab-server"),
		// SSE-friendly: no per-request write deadline.
		httputil.WriteTimeout(0),
		httputil.TLSConfig(cfg.TLSConfig),
		// On shutdown, cancel every in-flight deploy with a 5s drain
		// window before the HTTP listener closes.
		httputil.OnShutdown(func() {
			s.registry.CancelAll(5 * time.Second)
		}),
	)
	return s
}

// Handler returns the fully-wired http.Handler. Tests mount this into
// httptest.Server without needing to bind a real port.
func (s *Server) Handler() http.Handler {
	return s.HTTPServer().Handler
}
