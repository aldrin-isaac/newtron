package api

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DeployRegistry tracks in-flight deploy operations, one per lab name.
// Concurrent deploy requests for the same lab are rejected with
// AlreadyDeployingError (mapped to 409 Conflict). Different labs deploy
// concurrently.
//
// The registry only tracks the async Deploy operation — synchronous
// operations (status, start, stop, destroy, provision) bypass it. This
// matches newtrun-server's RunRegistry pattern: only the long-running
// thing needs registry-level mutual exclusion.
//
// `destroy` is treated as synchronous: it runs short enough (seconds to
// tens of seconds) that operators expect to block on the response. If
// that assumption breaks, destroy can move to async with its own
// registry slot in a follow-up.
type DeployRegistry struct {
	mu      sync.Mutex
	entries map[string]*deployEntry
}

type deployEntry struct {
	Lab     string
	Started time.Time
	cancel  context.CancelFunc
}

// AlreadyDeployingError is returned by Acquire when another deploy is
// already in flight for the lab. Handlers map it to 409 Conflict.
type AlreadyDeployingError struct {
	Lab     string
	Started time.Time
}

func (e *AlreadyDeployingError) Error() string {
	return fmt.Sprintf("deploy of %q is already in flight (started %s ago)",
		e.Lab, time.Since(e.Started).Round(time.Second))
}

// NewDeployRegistry constructs an empty registry.
func NewDeployRegistry() *DeployRegistry {
	return &DeployRegistry{
		entries: make(map[string]*deployEntry),
	}
}

// Acquire registers a deploy for the lab. Returns the per-deploy
// context (cancellable by the registry) and an unregister function the
// caller invokes on completion. Returns AlreadyDeployingError if the
// lab already has an in-flight deploy.
func (r *DeployRegistry) Acquire(ctx context.Context, lab string) (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[lab]; ok {
		return nil, nil, &AlreadyDeployingError{
			Lab:     lab,
			Started: existing.Started,
		}
	}
	deployCtx, cancel := context.WithCancel(ctx)
	entry := &deployEntry{
		Lab:     lab,
		Started: time.Now(),
		cancel:  cancel,
	}
	r.entries[lab] = entry
	release := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if current, ok := r.entries[lab]; ok && current == entry {
			delete(r.entries, lab)
		}
		cancel()
	}
	return deployCtx, release, nil
}

// CancelAll cancels every in-flight deploy and waits up to maxWait for
// the goroutines to drain. Called on server shutdown.
func (r *DeployRegistry) CancelAll(maxWait time.Duration) {
	r.mu.Lock()
	for _, entry := range r.entries {
		entry.cancel()
	}
	r.mu.Unlock()

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		empty := len(r.entries) == 0
		r.mu.Unlock()
		if empty {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

