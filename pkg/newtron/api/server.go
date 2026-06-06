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
	networks map[string]*networkEntry

	idleTimeout time.Duration
	logger      *log.Logger

	// portResolver supplies per-device SSH port allocations at
	// Connect time. Composed in from cmd/ (the only layer that knows
	// which engine provides the implementation — newtlab today).
	// Nil disables resolver consultation (tests, real-hardware).
	portResolver PortResolver
}

// networkEntry pairs a networkScope with its registration metadata.
type networkEntry struct {
	scope   *networkScope
	specDir string
}

// NewServer creates a new API server. idleTimeout controls how long SSH
// connections to devices are cached between requests. Use 0 for the default
// (5 minutes). Use a negative value to disable caching (connect per request).
//
// portResolver supplies per-device SSH port allocations at Device.Connect
// time. Pass nil to disable (real-hardware deployments, tests). The
// newtlab-backed implementation is constructed in cmd/ and injected here;
// the api package itself does not know about newtlab (DESIGN_PRINCIPLES
// §33, §34).
func NewServer(logger *log.Logger, idleTimeout time.Duration, portResolver PortResolver) *Server {
	if logger == nil {
		logger = log.Default()
	}
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	s := &Server{
		networks:     make(map[string]*networkEntry),
		idleTimeout:  idleTimeout,
		logger:       logger,
		portResolver: portResolver,
	}
	s.Server = httputil.NewServer(s.buildMux(), logger,
		httputil.ServerLabel("newtron-server"),
		// newtron handlers can do long device-facing operations; a
		// finite write timeout caps them. Different from newtrun /
		// newtlab which keep WriteTimeout=0 for SSE.
		httputil.WriteTimeout(5*time.Minute),
		httputil.OnShutdown(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, entry := range s.networks {
				entry.scope.stop()
			}
			s.networks = make(map[string]*networkEntry)
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

	if _, exists := s.networks[id]; exists {
		return &alreadyRegisteredError{id: id}
	}

	net, err := newtron.LoadNetwork(specDir, topologyName(specDir), s.portResolver)
	if err != nil {
		return fmt.Errorf("loading network from %s: %w", specDir, err)
	}

	s.networks[id] = &networkEntry{
		scope:   newNetworkScope(net, s.idleTimeout),
		specDir: specDir,
	}
	s.logger.Printf("registered network '%s' from %s", id, specDir)
	return nil
}

// UnregisterNetwork removes a registered network. Stops all NodeActors
// (draining in-flight requests and closing SSH connections) before removing.
func (s *Server) UnregisterNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.networks[id]
	if !exists {
		return fmt.Errorf("network '%s' not registered", id)
	}

	entry.scope.stop()
	delete(s.networks, id)
	s.logger.Printf("unregistered network '%s'", id)
	return nil
}

// ReloadNetwork stops the existing networkScope, reloads specs from disk,
// and creates a fresh networkScope. SSH connections reconnect lazily on next request.
func (s *Server) ReloadNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.networks[id]
	if !exists {
		return &notRegisteredError{id}
	}

	// Stop old scope (drains all NodeActors and SSH connections)
	entry.scope.stop()

	// Reload specs from disk
	net, err := newtron.LoadNetwork(entry.specDir, topologyName(entry.specDir), s.portResolver)
	if err != nil {
		return fmt.Errorf("reloading specs from %s: %w", entry.specDir, err)
	}

	// Replace with new scope
	s.networks[id] = &networkEntry{
		scope:   newNetworkScope(net, s.idleTimeout),
		specDir: entry.specDir,
	}
	s.logger.Printf("reloaded network '%s' from %s", id, entry.specDir)
	return nil
}

// getNetwork returns the networkScope for the given ID, or nil.
func (s *Server) getNetwork(id string) *networkScope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.networks[id]
	if entry == nil {
		return nil
	}
	return entry.scope
}

// listNetworks returns info about all registered networks.
func (s *Server) listNetworks() []NetworkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]NetworkInfo, 0, len(s.networks))
	for id, entry := range s.networks {
		result = append(result, NetworkInfo{
			ID:          id,
			SpecDir:     entry.specDir,
			HasTopology: entry.scope.net.HasTopology(),
			Topology:    topologyName(entry.specDir),
			Nodes:       entry.scope.net.ListNodes(),
		})
	}
	return result
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
