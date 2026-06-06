package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNetworkEntity_ReadsRunConcurrently pins the parallelism contract from
// issue #99: multiple read closures must be able to run simultaneously under
// ne.read(). The probe blocks each closure on a barrier so the test fails if
// the closures are serialized — under sync.Mutex.Lock, the first closure
// would hold the lock while waiting on the barrier and no other closure
// could enter; under sync.RWMutex.RLock, all N closures enter in parallel
// and the barrier releases them at once.
func TestNetworkEntity_ReadsRunConcurrently(t *testing.T) {
	s := newTestServer(t)
	ne := s.getNetwork("default")
	if ne == nil {
		t.Fatal("default network not registered")
	}

	const N = 8
	var inside atomic.Int32
	allInside := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = ne.read(context.Background(), func() (any, error) {
				if inside.Add(1) == N {
					close(allInside)
				}
				<-release
				return nil, nil
			})
		}()
	}

	select {
	case <-allInside:
		// All N closures got inside ne.read concurrently — parallelism works.
	case <-time.After(2 * time.Second):
		t.Fatalf("only %d of %d read closures entered concurrently — ne.read is serializing", inside.Load(), N)
	}
	close(release)
	wg.Wait()
}

// TestNetworkEntity_WritesSerializeAgainstWrites pins the writer-exclusion
// contract: two concurrent write closures must not overlap. Counts the
// number of writers in flight at any moment; if it ever exceeds 1, the
// mutual exclusion is broken.
func TestNetworkEntity_WritesSerializeAgainstWrites(t *testing.T) {
	s := newTestServer(t)
	ne := s.getNetwork("default")
	if ne == nil {
		t.Fatal("default network not registered")
	}

	const N = 8
	var inFlight atomic.Int32
	var maxObserved atomic.Int32

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = ne.write(context.Background(), func() (any, error) {
				now := inFlight.Add(1)
				for {
					prev := maxObserved.Load()
					if now <= prev || maxObserved.CompareAndSwap(prev, now) {
						break
					}
				}
				// Hold the lock briefly so any concurrent writer's Add()
				// would land before our Add(-1) — gives the race a chance.
				time.Sleep(2 * time.Millisecond)
				inFlight.Add(-1)
				return nil, nil
			})
		}()
	}
	wg.Wait()

	if got := maxObserved.Load(); got != 1 {
		t.Errorf("maximum concurrent writers = %d, want 1 (writer exclusion broken)", got)
	}
}

// TestNetworkEntity_WriterExcludesReaders pins the writer-vs-reader exclusion:
// while a writer holds Lock, no reader can enter RLock. Verified by holding
// the writer for a measurable duration and confirming the reader does not
// complete until the writer releases.
func TestNetworkEntity_WriterExcludesReaders(t *testing.T) {
	s := newTestServer(t)
	ne := s.getNetwork("default")
	if ne == nil {
		t.Fatal("default network not registered")
	}

	writerHolding := make(chan struct{})
	writerRelease := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		_, _ = ne.write(context.Background(), func() (any, error) {
			close(writerHolding)
			<-writerRelease
			return nil, nil
		})
		close(writerDone)
	}()

	<-writerHolding

	readerEntered := make(chan struct{})
	go func() {
		_, _ = ne.read(context.Background(), func() (any, error) {
			close(readerEntered)
			return nil, nil
		})
	}()

	// The reader must NOT enter while the writer holds the lock.
	select {
	case <-readerEntered:
		t.Fatal("reader entered RLock while a writer held Lock")
	case <-time.After(50 * time.Millisecond):
		// Expected — reader is blocked.
	}

	close(writerRelease)
	<-writerDone

	select {
	case <-readerEntered:
		// Expected — reader runs once the writer releases.
	case <-time.After(time.Second):
		t.Fatal("reader did not enter RLock after writer released")
	}
}
