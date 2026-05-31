package api

import (
	"sync"
)

// EventBroker multiplexes deploy / destroy progress events from running
// async operations to SSE subscribers. Keyed by topology name: each
// in-flight deploy publishes phase events to the broker under its
// topology key; SSE handlers subscribe by the same key.
//
// Drop-on-full semantics match newtrun-server's broker: if a slow SSE
// consumer fills its 64-event buffer, additional events are dropped for
// that consumer. Other subscribers still receive every event. SSE is
// best-effort delivery; the canonical state on disk
// (`Lab.Status()` / `~/.newtlab/labs/<name>/state.json`) is the source
// of truth — clients that miss events can always poll status.
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

// Subscribe registers a subscriber for the given topology key. Returns
// the event channel and an unsubscribe function. Buffer size 64 — enough
// to absorb a deploy's phase bursts (boot, bootstrap, patch, ready)
// without dropping under normal load; too-slow consumers shed gracefully.
func (b *EventBroker) Subscribe(topology string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	subs := b.subscribers[topology]
	if subs == nil {
		subs = make(map[chan Event]struct{})
		b.subscribers[topology] = subs
	}
	subs[ch] = struct{}{}
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[topology]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subscribers, topology)
			}
		}
		close(ch)
	}
	return ch, unsub
}

// Publish broadcasts an event to all subscribers of the given topology
// key. If no subscribers are registered, the call is a no-op. Full
// subscriber buffers cause that event to be dropped for that subscriber
// (others still receive it).
func (b *EventBroker) Publish(topology string, ev Event) {
	b.mu.RLock()
	subs := b.subscribers[topology]
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
			// Buffer full; drop this event for this subscriber.
		}
	}
}

// SubscriberCount returns the number of active subscribers for a
// topology key. Used by tests to assert subscribe/unsubscribe semantics.
func (b *EventBroker) SubscriberCount(topology string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[topology])
}
