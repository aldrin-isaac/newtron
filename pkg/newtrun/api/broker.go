package api

import (
	"sync"
)

// EventBroker multiplexes ProgressReporter events from running runs to SSE
// subscribers. Per-run reporters publish events keyed by run ID (suite name
// for file-backed runs; UUID for inline runs in PR 3); per-suite SSE handlers
// subscribe by the same key.
//
// The broker is the central point where in-process Runner execution meets the
// HTTP server's SSE endpoints. In PR 1 there are no in-server runs, so the
// publish path has no callers and subscribers receive no events. PR 2 wires
// the server-side Runner to publish into the broker.
type EventBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
}

// NewEventBroker creates an empty broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe registers a subscriber for the given run key. Returns the event
// channel and an unsubscribe function. The channel is buffered; if a slow
// consumer fills the buffer, additional events are dropped silently — per
// the issue spec for #22, "sinks that cannot forward MAY drop events." SSE
// is best-effort delivery.
//
// Buffer size: 64. Enough to absorb a burst (a fast-running scenario can
// emit several events per second); too-slow consumers shed load gracefully.
func (b *EventBroker) Subscribe(runKey string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	subs := b.subscribers[runKey]
	if subs == nil {
		subs = make(map[chan Event]struct{})
		b.subscribers[runKey] = subs
	}
	subs[ch] = struct{}{}
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[runKey]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subscribers, runKey)
			}
		}
		close(ch)
	}
	return ch, unsub
}

// Publish broadcasts an event to all subscribers of the given run key. If
// no subscribers are registered, the call is a no-op. If a subscriber's
// channel buffer is full, the event is dropped for that subscriber (other
// subscribers still receive it). See Subscribe for the rationale.
func (b *EventBroker) Publish(runKey string, ev Event) {
	b.mu.RLock()
	subs := b.subscribers[runKey]
	// Copy the channel set so we don't hold the lock during sends. Sends
	// to a full buffer are non-blocking (select with default).
	channels := make([]chan Event, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- ev:
		default:
			// Subscriber buffer full; drop this event for this subscriber.
		}
	}
}

// SubscriberCount returns the number of active subscribers for a run key.
// Used by tests to assert subscribe/unsubscribe semantics.
func (b *EventBroker) SubscriberCount(runKey string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[runKey])
}
