package httputil

import (
	"testing"
	"time"
)

type testEvent struct {
	Kind_ string
	Value int
}

func (e testEvent) Kind() string { return e.Kind_ }
func (e testEvent) Body() any    { return e.Value }

func TestBrokerSubscribePublishRoundTrip(t *testing.T) {
	b := NewBroker[testEvent]()
	events, unsub := b.Subscribe("topo1")
	defer unsub()

	b.Publish("topo1", testEvent{Kind_: "phase", Value: 42})

	select {
	case ev := <-events:
		if ev.Value != 42 {
			t.Errorf("Value = %d, want 42", ev.Value)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerSubscribersAreIsolatedByKey(t *testing.T) {
	b := NewBroker[testEvent]()
	chA, unsubA := b.Subscribe("a")
	defer unsubA()
	chB, unsubB := b.Subscribe("b")
	defer unsubB()

	b.Publish("a", testEvent{Value: 1})

	select {
	case <-chA:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("a subscriber missed event")
	}
	select {
	case ev := <-chB:
		t.Fatalf("b subscriber received unexpected event %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrokerUnsubscribeRemovesEntry(t *testing.T) {
	b := NewBroker[testEvent]()
	_, unsub := b.Subscribe("topo1")
	if got := b.SubscriberCount("topo1"); got != 1 {
		t.Errorf("SubscriberCount after Subscribe = %d, want 1", got)
	}
	unsub()
	if got := b.SubscriberCount("topo1"); got != 0 {
		t.Errorf("SubscriberCount after unsub = %d, want 0", got)
	}
}

func TestBrokerPublishDoesNotBlockOnFullBuffer(t *testing.T) {
	b := NewBroker[testEvent]()
	_, unsub := b.Subscribe("topo1")
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			b.Publish("topo1", testEvent{Value: i})
		}
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked when subscriber buffer was full")
	}
}

func TestBrokerPublishToUnsubscribedKeyIsNoop(t *testing.T) {
	b := NewBroker[testEvent]()
	b.Publish("nobody-listening", testEvent{Value: 1})
	if got := b.SubscriberCount("nobody-listening"); got != 0 {
		t.Errorf("SubscriberCount = %d, want 0", got)
	}
}
