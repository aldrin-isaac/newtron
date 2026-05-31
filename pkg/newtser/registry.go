package newtser

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Registry holds the live set of registered services, keyed by service
// name. Registration is idempotent — re-registering an existing name
// overwrites the prior entry (this is how backend restarts work: same
// name, same upstream, refreshed LastSeen).
//
// Concurrency: Registry is safe to share across goroutines. Reads and
// writes are guarded by a single RWMutex. The map is small (one
// entry per backend, three or four total today) so contention is not
// a concern.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*Service
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		services: make(map[string]*Service),
	}
}

// Register adds or refreshes a service. Returns the stored copy.
func (r *Registry) Register(name, version, upstream string) *Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	svc := &Service{
		Name:     name,
		Version:  version,
		Upstream: upstream,
		LastSeen: time.Now(),
	}
	r.services[name] = svc
	return svc
}

// Heartbeat updates the LastSeen timestamp without changing other
// fields. Returns true if the service was present, false otherwise —
// a backend that gets `false` should re-Register.
func (r *Registry) Heartbeat(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	svc, ok := r.services[name]
	if !ok {
		return false
	}
	svc.LastSeen = time.Now()
	return true
}

// Deregister removes a service. Returns true if it was present.
// Called by backends on graceful shutdown.
func (r *Registry) Deregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.services[name]; !ok {
		return false
	}
	delete(r.services, name)
	return true
}

// Get returns a copy of the service entry for name, or nil if not
// registered.
func (r *Registry) Get(name string) *Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]
	if !ok {
		return nil
	}
	copy := *svc
	return &copy
}

// List returns every registered service, sorted by name.
func (r *Registry) List() []*Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Service, 0, len(r.services))
	for _, svc := range r.services {
		copy := *svc
		out = append(out, &copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EvictStale removes services whose LastSeen is older than maxAge.
// Returns the names of evicted services. Called periodically by the
// eviction loop — backends that crash without deregistering are
// cleaned up after maxAge.
func (r *Registry) EvictStale(maxAge time.Duration) []string {
	cutoff := time.Now().Add(-maxAge)
	r.mu.Lock()
	defer r.mu.Unlock()
	var evicted []string
	for name, svc := range r.services {
		if svc.LastSeen.Before(cutoff) {
			delete(r.services, name)
			evicted = append(evicted, name)
		}
	}
	sort.Strings(evicted)
	return evicted
}

// RunEvictionLoop runs EvictStale on every tick of interval. Exits
// when ctx is canceled. Logged evictions go through the provided
// callback so the caller can log them as it likes.
func (r *Registry) RunEvictionLoop(ctx context.Context, interval, maxAge time.Duration, onEvict func(name string)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, name := range r.EvictStale(maxAge) {
				if onEvict != nil {
					onEvict(name)
				}
			}
		}
	}
}
