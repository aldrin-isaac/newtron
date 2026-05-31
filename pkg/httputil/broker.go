package httputil

import (
	"sync"
)

// Broker multiplexes events from one producer to many subscribers,
// keyed by an arbitrary string (typically the resource being observed
// — suite name, topology name). Each Subscribe returns a buffered
// channel; full buffers shed load by dropping events for that
// subscriber only. Other subscribers still receive the event.
//
// The drop-on-full policy matches SSE's best-effort delivery contract:
// the canonical state lives elsewhere (on disk, in a Lab.Status()
// reading, in a RunState file), so clients that miss events can always
// reconcile by polling. SSE is a fast-path notification, not the
// system of record.
//
// Generic in E so each server retains its strongly-typed Event struct.
// The broker itself does no JSON encoding; that lives in the SSE
// writer (sse.go) which accepts an Eventable.
type Broker[E any] struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan E]struct{}
}

// NewBroker constructs an empty broker.
func NewBroker[E any]() *Broker[E] {
	return &Broker[E]{
		subscribers: make(map[string]map[chan E]struct{}),
	}
}

// Subscribe registers a subscriber for the given key. Returns the
// receive-only event channel and an unsubscribe function. The returned
// channel is buffered (64) — enough to absorb typical event bursts
// without dropping under normal load.
func (b *Broker[E]) Subscribe(key string) (<-chan E, func()) {
	ch := make(chan E, 64)
	b.mu.Lock()
	subs := b.subscribers[key]
	if subs == nil {
		subs = make(map[chan E]struct{})
		b.subscribers[key] = subs
	}
	subs[ch] = struct{}{}
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[key]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subscribers, key)
			}
		}
		close(ch)
	}
	return ch, unsub
}

// Publish broadcasts an event to every subscriber of key. If no
// subscribers are registered, the call is a no-op. Full subscriber
// buffers cause the event to be dropped for that subscriber only —
// other subscribers still receive it. Publish never blocks.
func (b *Broker[E]) Publish(key string, ev E) {
	b.mu.RLock()
	subs := b.subscribers[key]
	channels := make([]chan E, 0, len(subs))
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

// SubscriberCount returns the number of active subscribers for a key.
// Used by tests to assert subscribe/unsubscribe lifecycle.
func (b *Broker[E]) SubscriberCount(key string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[key])
}
