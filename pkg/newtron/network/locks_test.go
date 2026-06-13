package network

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test-local keys. Real key constants land in the PR that adds
// lockManager's first non-test caller.
const (
	keyTestA lockKey = "test:a"
	keyTestB lockKey = "test:b"
)

// TestLockManager_SameKeyReturnsSameLock pins the core invariant: the
// same key resolves to the same *sync.RWMutex on every call. Without
// this, atomicity claims built on lockManager would silently break —
// two callers asking for the "services" lock would each get a different
// mutex and serialization would disappear.
func TestLockManager_SameKeyReturnsSameLock(t *testing.T) {
	lm := &lockManager{}
	a1 := lm.lock(keyTestA)
	a2 := lm.lock(keyTestA)
	if a1 != a2 {
		t.Errorf("same key returned different locks: %p vs %p", a1, a2)
	}
}

// TestLockManager_DistinctKeysGetDistinctLocks pins the other half of
// the invariant: distinct keys never share a lock. Without this, the
// per-key parallelism (the whole point of the manager) would collapse —
// every key would alias to one mutex.
func TestLockManager_DistinctKeysGetDistinctLocks(t *testing.T) {
	lm := &lockManager{}
	a := lm.lock(keyTestA)
	b := lm.lock(keyTestB)
	if a == b {
		t.Error("distinct keys returned the same lock")
	}
}

// TestLockManager_ConcurrentLookupsAreSafe pins the racing-callers
// contract: N goroutines calling lock(X) simultaneously must all receive
// the same lock without corrupting the internal map. The `-race` detector
// catches the corruption case; the equality assertion catches the case
// where lookups serialize correctly but emit different locks.
func TestLockManager_ConcurrentLookupsAreSafe(t *testing.T) {
	lm := &lockManager{}
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	seen := make([]*sync.RWMutex, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			seen[i] = lm.lock(keyTestA)
		}(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if seen[i] != seen[0] {
			t.Errorf("goroutine %d got lock %p, want %p", i, seen[i], seen[0])
		}
	}
}

// TestLockManager_KeyedLockIsAUsableRWMutex is a smoke test that the
// lock returned by lockManager actually behaves like an RWMutex — a held
// writer excludes new readers. Not testing sync.RWMutex itself (Go does
// that); testing that lockManager.lock returns a usable RWMutex rather
// than something weird.
func TestLockManager_KeyedLockIsAUsableRWMutex(t *testing.T) {
	lm := &lockManager{}
	l := lm.lock(keyTestA)
	l.Lock()

	var readerEntered atomic.Bool
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		otherL := lm.lock(keyTestA)
		otherL.RLock()
		readerEntered.Store(true)
		otherL.RUnlock()
	}()

	// Give the reader goroutine a chance to attempt RLock. 50 ms is
	// generous; on a healthy machine the scheduler wakes the goroutine
	// in microseconds.
	time.Sleep(50 * time.Millisecond)
	if readerEntered.Load() {
		t.Fatal("reader entered RLock while writer held Lock — lockManager returned wrong lock")
	}
	l.Unlock()

	select {
	case <-readerDone:
		if !readerEntered.Load() {
			t.Error("reader did not record entry even after writer released")
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not complete after writer released — lock semantics broken")
	}
}
