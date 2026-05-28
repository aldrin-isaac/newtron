package api

import (
	"sync"
	"testing"
	"time"
)

func TestBrokerPublishReachesSubscriber(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("suite-a")
	defer unsub()

	b.Publish("suite-a", Event{Type: EventScenarioStart, Payload: "p1"})

	select {
	case ev := <-events:
		if ev.Type != EventScenarioStart {
			t.Errorf("unexpected event type: %q", ev.Type)
		}
		if ev.Payload != "p1" {
			t.Errorf("unexpected payload: %v", ev.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestBrokerPublishRoutesByKey(t *testing.T) {
	b := NewEventBroker()
	eventsA, unsubA := b.Subscribe("suite-a")
	defer unsubA()
	eventsB, unsubB := b.Subscribe("suite-b")
	defer unsubB()

	// An event on suite-a's key reaches suite-a's subscriber but not suite-b's.
	b.Publish("suite-a", Event{Type: EventStepStart})

	select {
	case <-eventsA:
	case <-time.After(time.Second):
		t.Fatal("suite-a subscriber missed its event")
	}

	select {
	case ev := <-eventsB:
		t.Errorf("suite-b subscriber received unexpected event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event for suite-b.
	}
}

func TestBrokerMultipleSubscribersAllReceive(t *testing.T) {
	b := NewEventBroker()
	e1, u1 := b.Subscribe("suite-a")
	defer u1()
	e2, u2 := b.Subscribe("suite-a")
	defer u2()

	if got := b.SubscriberCount("suite-a"); got != 2 {
		t.Errorf("SubscriberCount: got %d, want 2", got)
	}

	b.Publish("suite-a", Event{Type: EventStepEnd})

	for i, ch := range []<-chan Event{e1, e2} {
		select {
		case ev := <-ch:
			if ev.Type != EventStepEnd {
				t.Errorf("subscriber %d got unexpected type %q", i, ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive event", i)
		}
	}
}

func TestBrokerUnsubscribeRemovesSubscriber(t *testing.T) {
	b := NewEventBroker()
	_, unsub := b.Subscribe("suite-a")
	if got := b.SubscriberCount("suite-a"); got != 1 {
		t.Errorf("after subscribe: got %d, want 1", got)
	}
	unsub()
	if got := b.SubscriberCount("suite-a"); got != 0 {
		t.Errorf("after unsubscribe: got %d, want 0", got)
	}
}

func TestBrokerPublishWithNoSubscribersIsNoop(t *testing.T) {
	b := NewEventBroker()
	// Should not panic or block.
	b.Publish("suite-a", Event{Type: EventSuiteStart})
}

func TestBrokerFullBufferDropsForSlowConsumer(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("suite-a")
	defer unsub()

	// Fill the buffer (size 64) and then push one more. The extra one
	// should be dropped silently, not block the publisher.
	for i := 0; i < 65; i++ {
		b.Publish("suite-a", Event{Type: EventStepStart, Payload: i})
	}

	count := 0
	timeout := time.After(time.Second)
	for {
		select {
		case <-events:
			count++
			if count >= 64 {
				return
			}
		case <-timeout:
			if count < 64 {
				t.Errorf("received only %d events; expected at least 64", count)
			}
			return
		}
	}
}

func TestBrokerConcurrentPublishSafe(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("suite-a")
	defer unsub()

	// Many concurrent publishers; no race or panic.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Publish("suite-a", Event{Type: EventStepEnd, Payload: i})
			}
		}(i)
	}

	// Drain the events channel concurrently so the buffer doesn't fill.
	doneDraining := make(chan struct{})
	go func() {
		for {
			select {
			case <-events:
			case <-doneDraining:
				return
			}
		}
	}()

	wg.Wait()
	close(doneDraining)
}
