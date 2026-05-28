package api

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRegistryAcquireReleaseRoundTrip(t *testing.T) {
	r := NewRunRegistry()
	entry, err := r.Acquire("suite-a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if entry.Key != "suite-a" {
		t.Errorf("Key: got %q, want %q", entry.Key, "suite-a")
	}
	if entry.Done == nil {
		t.Error("Done channel is nil")
	}
	r.Release("suite-a", &RunResult{})

	// After release, the key should be free again.
	_, err = r.Acquire("suite-a")
	if err != nil {
		t.Errorf("re-Acquire after Release should succeed; got: %v", err)
	}
}

func TestRegistrySameKeyRejected(t *testing.T) {
	r := NewRunRegistry()
	_, err := r.Acquire("suite-a")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	_, err = r.Acquire("suite-a")
	if err == nil {
		t.Fatal("second Acquire of same key: expected error, got nil")
	}
	var already *AlreadyRunningError
	if !errors.As(err, &already) {
		t.Errorf("error type: got %T, want *AlreadyRunningError", err)
	}
	if already.Key != "suite-a" {
		t.Errorf("AlreadyRunning.Key: got %q, want %q", already.Key, "suite-a")
	}
}

func TestRegistryDifferentKeysCoexist(t *testing.T) {
	r := NewRunRegistry()
	if _, err := r.Acquire("suite-a"); err != nil {
		t.Fatalf("Acquire(suite-a): %v", err)
	}
	if _, err := r.Acquire("suite-b"); err != nil {
		t.Errorf("Acquire(suite-b) should succeed alongside suite-a; got: %v", err)
	}
	if got := len(r.Keys()); got != 2 {
		t.Errorf("Keys len: got %d, want 2", got)
	}
}

func TestRegistryReleaseClosesDone(t *testing.T) {
	r := NewRunRegistry()
	entry, _ := r.Acquire("suite-a")

	released := make(chan struct{})
	go func() {
		<-entry.Done
		close(released)
	}()

	r.Release("suite-a", &RunResult{})

	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("Done channel was not closed by Release")
	}
}

func TestRegistryReleaseStoresResult(t *testing.T) {
	r := NewRunRegistry()
	entry, _ := r.Acquire("suite-a")

	expected := &RunResult{Err: errors.New("boom")}
	r.Release("suite-a", expected)

	if entry.Result != expected {
		t.Errorf("Result pointer mismatch: got %v, want %v", entry.Result, expected)
	}
}

func TestRegistryCancelAllInvokesEachCancel(t *testing.T) {
	r := NewRunRegistry()
	const n = 5
	cancels := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		key := string(rune('a' + i))
		entry, _ := r.Acquire(key)
		i := i // capture
		entry.Cancel = func() { cancels[i] = true }
		// Each entry's done channel closes only when the test releases it,
		// simulating runs that exit cleanly after cancellation.
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond)
			r.Release(key, &RunResult{})
		}()
	}

	r.CancelAll(2 * time.Second)
	wg.Wait()

	for i, c := range cancels {
		if !c {
			t.Errorf("cancels[%d] was not invoked", i)
		}
	}
}

func TestRegistryGetReturnsActiveOrNil(t *testing.T) {
	r := NewRunRegistry()
	if r.Get("suite-a") != nil {
		t.Error("Get on empty registry should return nil")
	}
	r.Acquire("suite-a")
	if r.Get("suite-a") == nil {
		t.Error("Get on active key should return non-nil")
	}
	r.Release("suite-a", &RunResult{})
	if r.Get("suite-a") != nil {
		t.Error("Get after Release should return nil")
	}
}
