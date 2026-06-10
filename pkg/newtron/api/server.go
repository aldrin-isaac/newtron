package api

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// PortResolver is newtron's public contract for resolving runtime
// SSH port allocations at Device.Connect time. It is a type alias
// for the internal sonic.PortResolver so external callers (cmd,
// tests) reference only newtron's public API surface (DESIGN_PRINCIPLES
// §33). The newtlab-backed implementation lives in pkg/newtlab/client
// and satisfies this contract structurally.
type PortResolver = sonic.PortResolver

// Server is the HTTP API server for newtron. The HTTP listener
// lifecycle (Start / Stop) comes from the embedded *httputil.Server;
// this type holds only newtron-specific state.
type Server struct {
	*httputil.Server

	mu       sync.RWMutex
	networks map[string]*networkEntity

	idleTimeout time.Duration
	logger      *log.Logger

	// portResolver supplies per-device SSH port allocations at
	// Connect time. Composed in from cmd/ (the only layer that knows
	// which engine provides the implementation — newtlab today).
	// Nil disables resolver consultation (tests, real-hardware).
	portResolver PortResolver

	// scaffoldRoot is the on-disk root under which derived-spec_dir
	// scaffolds land (#122). Set via the --scaffold-root flag on
	// newtron-server / newt-server. Empty means "this server doesn't
	// derive paths" — POST /newtron/v1/networks with scaffold=true and
	// no spec_dir returns 400 in that mode. The derived layout is
	// filepath.Join(scaffoldRoot, id); collision handling matches the
	// explicit-path case (existing spec.ErrAlreadyInitialized → 409).
	//
	// Operator-language alignment (§33): a UI client should not have
	// to know newtron's on-disk layout to scaffold a topology. When
	// the server is configured with this root, the client's intent
	// "create topology named X" suffices — newtron picks the path and
	// returns it in the response.
	scaffoldRoot string

	// auditCallerHeader is the TCP-fallback HTTP header name for
	// self-attested caller identity (auth-design.md L1). Read by
	// callerMiddleware. Empty disables header-based identity.
	auditCallerHeader string
}


// Config carries every knob NewServer accepts. Uses a struct rather
// than positional params so the auth-design.md layered work (L1
// audit log + Unix socket + header; L2a mTLS; L2b PAM; L3 enforce)
// can grow the surface without each layer's PR resignaturing
// NewServer. Mirrors the existing newtlab/api.Config pattern.
//
// All fields are optional. Zero values give the pre-L1 behavior:
// no audit log, no Unix socket, TCP-only, no auth enforcement.
type Config struct {
	// Logger is the server's structured logger. nil → log.Default().
	Logger *log.Logger

	// IdleTimeout controls how long SSH connections to devices
	// are cached between requests. 0 → DefaultIdleTimeout (5m).
	// Negative → disable caching (connect per request).
	IdleTimeout time.Duration

	// PortResolver supplies per-device SSH port allocations at
	// Device.Connect time. nil disables resolver consultation
	// (real-hardware deployments, tests). The newtlab-backed
	// implementation is constructed in cmd/ and injected here;
	// the api package itself does not know about newtlab
	// (DESIGN_PRINCIPLES §33, §34).
	PortResolver PortResolver

	// ScaffoldRoot enables the derived-spec_dir mode of POST
	// /newtron/v1/networks (issue #122). When set, requests with
	// scaffold:true and no spec_dir scaffold into
	// filepath.Join(ScaffoldRoot, id). Empty keeps the explicit-
	// path-only behavior — the derived mode then returns 400
	// rather than guessing a default.
	ScaffoldRoot string

	// AuditCallerHeader is the HTTP header name read by
	// callerMiddleware on TCP listeners to extract the
	// self-attested caller identity (auth-design.md L1). Empty
	// disables header-based identity — Unix socket peer creds
	// still work if UnixSocketPath is configured. Recommended
	// value when enabled: "X-Newtron-Caller".
	AuditCallerHeader string

	// UnixSocketPath enables a Unix-domain socket listener
	// alongside the TCP one (auth-design.md L1). When set,
	// requests on the Unix listener carry verified peer
	// credentials extracted via SO_PEERCRED; the
	// caller-extraction middleware tags them with
	// VerificationUnixPeerCreds. Empty disables the Unix listener.
	UnixSocketPath string
}

// NewServer creates a new API server with the given Config. Zero-
// valued Config preserves the pre-L1 behavior (TCP-only, no audit
// log, no enforcement).
func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	s := &Server{
		networks:          make(map[string]*networkEntity),
		idleTimeout:       idleTimeout,
		logger:            logger,
		portResolver:      cfg.PortResolver,
		scaffoldRoot:      cfg.ScaffoldRoot,
		auditCallerHeader: cfg.AuditCallerHeader,
	}
	s.Server = httputil.NewServer(s.buildMux(), logger,
		httputil.ServerLabel("newtron-server"),
		// newtron handlers can do long device-facing operations; a
		// finite write timeout caps them. Different from newtrun /
		// newtlab which keep WriteTimeout=0 for SSE.
		httputil.WriteTimeout(5*time.Minute),
		httputil.UnixSocketPath(cfg.UnixSocketPath),
		httputil.OnShutdown(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, entity := range s.networks {
				entity.stop()
			}
			s.networks = make(map[string]*networkEntity)
		}),
	)
	return s
}

// Handler returns the fully-wired http.Handler. Used by newt-server
// to mount newtron under /newtron/v1/ in the aggregated process and
// by tests that mount the server into httptest.Server without
// binding a real port.
func (s *Server) Handler() http.Handler {
	return s.HTTPServer().Handler
}


// RegisterNetwork loads a Network from specDir and registers it under the given ID.
func (s *Server) RegisterNetwork(id, specDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, exists := s.networks[id]; exists {
		return &alreadyRegisteredError{id: id, existingSpecDir: existing.specDir}
	}

	net, err := newtron.LoadNetwork(specDir, topologyName(specDir), s.portResolver)
	if err != nil {
		return fmt.Errorf("loading network from %s: %w", specDir, err)
	}

	s.networks[id] = newNetworkEntity(net, specDir, s.idleTimeout)
	s.logger.Printf("registered network '%s' from %s", id, specDir)
	return nil
}

// ScaffoldAndRegister creates an empty spec layout at specDir (the three
// zero-valued spec files newtron's Loader requires plus an empty profiles/
// subdirectory), then registers the resulting network under id. description
// flows into topology.json.
//
// Returns spec.ErrAlreadyInitialized if specDir already contains spec files;
// the caller maps this to 409 Conflict. The scaffold step is a no-op for
// other failure modes — RegisterNetwork's normal errors apply.
func (s *Server) ScaffoldAndRegister(id, specDir, description string) error {
	if err := spec.Scaffold(specDir, description); err != nil {
		return err
	}
	return s.RegisterNetwork(id, specDir)
}

// UnregisterNetwork removes a registered network. Stops all NodeActors
// (draining in-flight requests and closing SSH connections) before removing.
func (s *Server) UnregisterNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entity, exists := s.networks[id]
	if !exists {
		return fmt.Errorf("network '%s' not registered", id)
	}

	entity.stop()
	delete(s.networks, id)
	s.logger.Printf("unregistered network '%s'", id)
	return nil
}

// ReloadNetwork stops the existing networkEntity, reloads specs from disk,
// and creates a fresh networkEntity. SSH connections reconnect lazily on
// next request.
func (s *Server) ReloadNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entity, exists := s.networks[id]
	if !exists {
		return &notRegisteredError{id}
	}

	// Stop old entity (drains all NodeActors and SSH connections)
	entity.stop()

	// Reload specs from disk
	net, err := newtron.LoadNetwork(entity.specDir, topologyName(entity.specDir), s.portResolver)
	if err != nil {
		return fmt.Errorf("reloading specs from %s: %w", entity.specDir, err)
	}

	// Replace with new entity
	s.networks[id] = newNetworkEntity(net, entity.specDir, s.idleTimeout)
	s.logger.Printf("reloaded network '%s' from %s", id, entity.specDir)
	return nil
}

// getNetwork returns the networkEntity for the given ID, or nil.
func (s *Server) getNetwork(id string) *networkEntity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.networks[id]
}

// listNetworks returns info about all registered networks.
func (s *Server) listNetworks() []NetworkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]NetworkInfo, 0, len(s.networks))
	for id, entity := range s.networks {
		result = append(result, networkInfoFor(id, entity))
	}
	return result
}

// getNetworkInfo returns NetworkInfo for the registered id, or nil
// when no network is registered under that id. Used by the
// register-network handler to return the canonical NetworkInfo on 201
// (§46) so the client learns the resolved spec_dir even when the
// server picked it (#122).
func (s *Server) getNetworkInfo(id string) *NetworkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entity, ok := s.networks[id]
	if !ok {
		return nil
	}
	info := networkInfoFor(id, entity)
	return &info
}

// networkInfoFor projects a single registered networkEntity into the
// canonical wire shape. Single source of truth for the projection so
// the list path and the per-id path never diverge.
func networkInfoFor(id string, entity *networkEntity) NetworkInfo {
	return NetworkInfo{
		ID:          id,
		SpecDir:     entity.specDir,
		HasTopology: entity.net.HasTopology(),
		Topology:    topologyName(entity.specDir),
		Nodes:       entity.net.ListNodes(),
	}
}

// topologyName derives the topology name from a spec directory path.
// Convention: specDir ends with "/specs", topology name is the parent directory.
// e.g. "newtrun/topologies/1node-vs/specs" → "1node-vs"
func topologyName(specDir string) string {
	dir := filepath.Base(filepath.Dir(filepath.Clean(specDir)))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}
