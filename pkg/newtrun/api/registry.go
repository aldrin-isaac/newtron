package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// RunRegistry tracks server-side runs in flight. It is the in-memory
// authority for "is this run active?" — replacing the PID-based liveness
// check that worked when each run was its own OS process.
//
// Concurrency rules per the operator's v0 directive:
//   - Same suite key cannot have two simultaneous runs. The second
//     request returns an "already running" error referencing the active
//     run's identity.
//   - Different suite keys run concurrently with no contention.
//   - Inline runs in PR 3 each get their own UUID key and are inherently
//     non-conflicting.
//
// The lock model is a simple map[key]*entry guarded by a single mutex.
// For the expected scale of in-flight runs (handful to dozens), this is
// vastly simpler than per-suite mutexes and has no practical contention
// cost.
type RunRegistry struct {
	mu      sync.Mutex
	entries map[string]*RegistryEntry
}

// RegistryEntry tracks one in-flight run.
type RegistryEntry struct {
	Key     string             // suite name for v0; UUID for PR 3 inline runs
	Started time.Time          // when the run began
	Cancel  context.CancelFunc // cancels the runner's context (used by stop endpoint)
	Done    chan struct{}      // closed when the run goroutine exits
	Result  *RunResult         // populated after the run finishes (read while Done is closed)
}

// RunResult captures the terminal outcome of a finished run.
type RunResult struct {
	Scenarios []*newtrun.ScenarioResult
	Err       error
}

// NewRunRegistry constructs an empty registry.
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{
		entries: make(map[string]*RegistryEntry),
	}
}

// Acquire reserves the given key for a new run. Returns an error if a run
// is already active for that key. The returned entry's Cancel and Done
// fields are the caller's to populate before they release the lock.
//
// Caller's responsibility:
//  1. Acquire(key) — registers the placeholder entry under the lock
//  2. Populate entry.Cancel and entry.Done with the run's cancellation func
//     and done channel
//  3. Spawn the run goroutine; the goroutine calls Release(key) on exit
//
// This separation lets the caller construct the run's context AFTER taking
// the registry lock (so the concurrency check is atomic with the lock
// acquisition) but before exposing the entry to the rest of the server.
func (r *RunRegistry) Acquire(key string) (*RegistryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[key]; ok {
		return nil, &AlreadyRunningError{Key: key, Started: existing.Started}
	}
	entry := &RegistryEntry{
		Key:     key,
		Started: time.Now(),
		Done:    make(chan struct{}),
	}
	r.entries[key] = entry
	return entry, nil
}

// Release removes the entry for the given key. Called by the run goroutine
// when execution completes. The Done channel is closed here so subscribers
// waiting on it are notified atomically with the registry update.
func (r *RunRegistry) Release(key string, result *RunResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[key]; ok {
		entry.Result = result
		close(entry.Done)
		delete(r.entries, key)
	}
}

// Get returns the active entry for a key, or nil if none.
func (r *RunRegistry) Get(key string) *RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[key]
}

// Keys returns the keys of all in-flight runs. Used by graceful shutdown
// to cancel everything.
func (r *RunRegistry) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	return keys
}

// CancelAll cancels every in-flight run and waits up to the given timeout
// for each to drain. Used by Server.Stop for graceful shutdown.
func (r *RunRegistry) CancelAll(timeout time.Duration) {
	r.mu.Lock()
	entries := make([]*RegistryEntry, 0, len(r.entries))
	for _, e := range r.entries {
		entries = append(entries, e)
	}
	r.mu.Unlock()

	// Issue cancellations first so all runs start draining in parallel.
	for _, e := range entries {
		if e.Cancel != nil {
			e.Cancel()
		}
	}

	deadline := time.Now().Add(timeout)
	for _, e := range entries {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		select {
		case <-e.Done:
		case <-time.After(remaining):
			return
		}
	}
}

// AlreadyRunningError is returned by Acquire when a run is already in
// flight for the given key. Mapped to HTTP 409 Conflict at the handler
// layer.
type AlreadyRunningError struct {
	Key     string
	Started time.Time
}

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("run %q is already in flight (started %s ago)",
		e.Key, time.Since(e.Started).Round(time.Second))
}
