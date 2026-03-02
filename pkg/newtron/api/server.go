package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// Server is the HTTP API server for newtron.
type Server struct {
	mu       sync.RWMutex
	networks map[string]*networkEntry

	httpServer *http.Server
	logger     *log.Logger
}

// networkEntry pairs a NetworkActor with its registration metadata.
type networkEntry struct {
	actor   *NetworkActor
	specDir string
}

// NewServer creates a new API server.
func NewServer(logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{
		networks: make(map[string]*networkEntry),
		logger:   logger,
	}
	mux := s.buildMux()
	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Start begins listening on the given address.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	s.logger.Printf("newtron-server listening on %s", addr)
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	for _, entry := range s.networks {
		entry.actor.stop()
	}
	s.networks = make(map[string]*networkEntry)
	s.mu.Unlock()

	return s.httpServer.Shutdown(ctx)
}

// RegisterNetwork loads a Network from specDir and registers it under the given ID.
func (s *Server) RegisterNetwork(id, specDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.networks[id]; exists {
		return fmt.Errorf("network '%s' already registered", id)
	}

	net, err := newtron.LoadNetwork(specDir)
	if err != nil {
		return fmt.Errorf("loading network from %s: %w", specDir, err)
	}

	s.networks[id] = &networkEntry{
		actor:   newNetworkActor(net, specDir),
		specDir: specDir,
	}
	s.logger.Printf("registered network '%s' from %s", id, specDir)
	return nil
}

// UnregisterNetwork removes a registered network.
// Fails if any NodeActors are active.
func (s *Server) UnregisterNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.networks[id]
	if !exists {
		return fmt.Errorf("network '%s' not registered", id)
	}

	if count := entry.actor.activeNodeCount(); count > 0 {
		return fmt.Errorf("network '%s' has %d active node(s) — stop them first", id, count)
	}

	entry.actor.stop()
	delete(s.networks, id)
	s.logger.Printf("unregistered network '%s'", id)
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
			Nodes:       entry.actor.net.ListNodes(),
		})
	}
	return result
}
