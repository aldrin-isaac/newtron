package api

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// Server is the HTTP API server for newtron. The HTTP listener
// lifecycle (Start / Stop) comes from the embedded *httputil.Server;
// this type holds only newtron-specific state.
type Server struct {
	*httputil.Server

	mu       sync.RWMutex
	networks map[string]*networkEntry

	idleTimeout time.Duration
	logger      *log.Logger
}

// networkEntry pairs a NetworkActor with its registration metadata.
type networkEntry struct {
	actor   *NetworkActor
	specDir string
}

// NewServer creates a new API server. idleTimeout controls how long SSH
// connections to devices are cached between requests. Use 0 for the default
// (5 minutes). Use a negative value to disable caching (connect per request).
func NewServer(logger *log.Logger, idleTimeout time.Duration) *Server {
	if logger == nil {
		logger = log.Default()
	}
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	s := &Server{
		networks:    make(map[string]*networkEntry),
		idleTimeout: idleTimeout,
		logger:      logger,
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
				entry.actor.stop()
			}
			s.networks = make(map[string]*networkEntry)
		}),
	)
	return s
}

// RegisterNetwork loads a Network from specDir and registers it under the given ID.
func (s *Server) RegisterNetwork(id, specDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.networks[id]; exists {
		return &alreadyRegisteredError{id: id}
	}

	net, err := newtron.LoadNetwork(specDir)
	if err != nil {
		return fmt.Errorf("loading network from %s: %w", specDir, err)
	}

	s.networks[id] = &networkEntry{
		actor:   newNetworkActor(net, s.idleTimeout),
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

	entry.actor.stop()
	delete(s.networks, id)
	s.logger.Printf("unregistered network '%s'", id)
	return nil
}

// ReloadNetwork stops the existing NetworkActor, reloads specs from disk,
// and creates a fresh NetworkActor. SSH connections reconnect lazily on next request.
func (s *Server) ReloadNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.networks[id]
	if !exists {
		return &notRegisteredError{id}
	}

	// Stop old actor (drains all NodeActors and SSH connections)
	entry.actor.stop()

	// Reload specs from disk
	net, err := newtron.LoadNetwork(entry.specDir)
	if err != nil {
		return fmt.Errorf("reloading specs from %s: %w", entry.specDir, err)
	}

	// Replace with new actor
	s.networks[id] = &networkEntry{
		actor:   newNetworkActor(net, s.idleTimeout),
		specDir: entry.specDir,
	}
	s.logger.Printf("reloaded network '%s' from %s", id, entry.specDir)
	return nil
}

// getNetwork returns the NetworkActor for the given ID, or nil.
func (s *Server) getNetwork(id string) *NetworkActor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.networks[id]
	if entry == nil {
		return nil
	}
	return entry.actor
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
			HasTopology: entry.actor.net.HasTopology(),
			Topology:    topologyName(entry.specDir),
			Nodes:       entry.actor.net.ListNodes(),
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
