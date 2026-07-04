package api

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LabOpRegistry tracks the one in-flight long-running operation per lab —
// deploy or provision. A second long-running op on the same lab (from any
// caller — every HTTP endpoint is reachable by anyone) is rejected with
// LabBusyError (mapped to 409 Conflict); different labs run concurrently.
// Sharing one slot across deploy and provision is what prevents a provision
// from racing a deploy on the same lab.
//
// Only the long-running, progress-streamed operations take the slot. Short
// synchronous operations (status, start, stop, destroy, resync) bypass it and
// block on the response; destroy runs short enough (seconds to tens of seconds)
// that operators expect to block. If a short op ever needs to exclude against
// an in-flight deploy/provision, it can take the slot too — the mechanism is
// already general.
type LabOpRegistry struct {
	mu      sync.Mutex
	entries map[string]*labOpEntry
}

type labOpEntry struct {
	Lab     string
	Op      string // "deploy" | "provision" — the operation holding the slot
	Started time.Time
	cancel  context.CancelFunc
}

// LabBusyError is returned by Acquire when a long-running operation is already
// in flight for the lab. Op names what is running so the 409 is accurate for
// whichever operation holds the slot. Handlers map it to 409 Conflict.
type LabBusyError struct {
	Lab     string
	Op      string
	Started time.Time
}

func (e *LabBusyError) Error() string {
	return fmt.Sprintf("lab %q is busy: %s in flight (started %s ago)",
		e.Lab, e.Op, time.Since(e.Started).Round(time.Second))
}

// NewLabOpRegistry constructs an empty registry.
func NewLabOpRegistry() *LabOpRegistry {
	return &LabOpRegistry{
		entries: make(map[string]*labOpEntry),
	}
}

// Acquire registers a deploy for the lab. Returns the per-deploy
// context (cancellable by the registry) and an unregister function the
// caller invokes on completion. Returns LabBusyError if the lab already has
// an in-flight long-running operation. op names this operation ("deploy" /
// "provision") so a rejection reports what is actually running.
func (r *LabOpRegistry) Acquire(ctx context.Context, lab, op string) (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[lab]; ok {
		return nil, nil, &LabBusyError{
			Lab:     lab,
			Op:      existing.Op,
			Started: existing.Started,
		}
	}
	opCtx, cancel := context.WithCancel(ctx)
	entry := &labOpEntry{
		Lab:     lab,
		Op:      op,
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
	return opCtx, release, nil
}

// CancelAll cancels every in-flight operation and waits up to maxWait for
// the goroutines to drain. Called on server shutdown.
func (r *LabOpRegistry) CancelAll(maxWait time.Duration) {
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

