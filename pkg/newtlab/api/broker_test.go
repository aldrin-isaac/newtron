package api

import (
	"testing"
	"time"
)

func TestBrokerSubscribeReceivesPublishedEvents(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("topo1")
	defer unsub()

	b.Publish("topo1", Event{Type: EventPhase, Payload: PhasePayload{Phase: "boot", Detail: "switch1"}})

	select {
	case ev := <-events:
		if ev.Type != EventPhase {
			t.Errorf("Type = %q, want %q", ev.Type, EventPhase)
		}
		payload, ok := ev.Payload.(PhasePayload)
		if !ok {
			t.Fatalf("Payload = %T, want PhasePayload", ev.Payload)
		}
		if payload.Phase != "boot" {
			t.Errorf("Phase = %q, want %q", payload.Phase, "boot")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerPublishToUnsubscribedTopologyIsNoop(t *testing.T) {
	b := NewEventBroker()
	b.Publish("topo-missing", Event{Type: EventPhase})
	if got := b.SubscriberCount("topo-missing"); got != 0 {
		t.Errorf("SubscriberCount = %d, want 0", got)
	}
}

func TestBrokerSubscribersAreIsolatedByTopology(t *testing.T) {
	b := NewEventBroker()
	evA, unsubA := b.Subscribe("topo-a")
	defer unsubA()
	evB, unsubB := b.Subscribe("topo-b")
	defer unsubB()

	b.Publish("topo-a", Event{Type: EventComplete})

	select {
	case <-evA:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("topo-a subscriber did not receive event")
	}

	select {
	case ev := <-evB:
		t.Fatalf("topo-b subscriber received unexpected event %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected: topo-b did not see topo-a's event
	}
}

func TestBrokerUnsubscribeRemovesSubscriber(t *testing.T) {
	b := NewEventBroker()
	_, unsub := b.Subscribe("topo1")
	if got := b.SubscriberCount("topo1"); got != 1 {
		t.Errorf("SubscriberCount after Subscribe = %d, want 1", got)
	}
	unsub()
	if got := b.SubscriberCount("topo1"); got != 0 {
		t.Errorf("SubscriberCount after unsub = %d, want 0", got)
	}
}

func TestBrokerDropsEventsWhenSubscriberBufferFull(t *testing.T) {
	b := NewEventBroker()
	_, unsub := b.Subscribe("topo1") // buffer 64, we never read
	defer unsub()

	// Publish 200 events; the consumer never drains, so events 65+
	// get dropped silently. Test passes if Publish doesn't block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			b.Publish("topo1", Event{Type: EventPhase})
		}
		close(done)
	}()

	select {
	case <-done:
		// expected: Publish never blocks
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked when subscriber buffer was full")
	}
}
