package network

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test-local categories. Real category constants land in the PR that adds
// lockManager's first non-test caller.
const (
	catTestA lockCategory = "test:a"
	catTestB lockCategory = "test:b"
)

// TestLockManager_SameCategoryReturnsSameLock pins the core invariant:
// the same category resolves to the same *sync.RWMutex on every call.
// Without this, atomicity claims built on lockManager would silently
// break — two callers asking for the "services" lock would each get a
// different mutex and serialization would disappear.
func TestLockManager_SameCategoryReturnsSameLock(t *testing.T) {
	lm := newLockManager()
	a1 := lm.lock(catTestA)
	a2 := lm.lock(catTestA)
	if a1 != a2 {
		t.Errorf("same category returned different locks: %p vs %p", a1, a2)
	}
}

// TestLockManager_DistinctCategoriesGetDistinctLocks pins the other half
// of the invariant: distinct categories never share a lock. Without this,
// cross-category parallelism (the whole point of the manager) would
// collapse — every category would alias to one mutex.
func TestLockManager_DistinctCategoriesGetDistinctLocks(t *testing.T) {
	lm := newLockManager()
	a := lm.lock(catTestA)
	b := lm.lock(catTestB)
	if a == b {
		t.Error("distinct categories returned the same lock")
	}
}

// TestLockManager_ConcurrentLookupsAreSafe pins the racing-callers
// contract: N goroutines calling lock(X) simultaneously must all receive
// the same lock without corrupting the internal map. The `-race` detector
// catches the corruption case; the equality assertion catches the case
// where lookups serialize correctly but emit different locks.
func TestLockManager_ConcurrentLookupsAreSafe(t *testing.T) {
	lm := newLockManager()
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	seen := make([]*sync.RWMutex, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			seen[i] = lm.lock(catTestA)
		}(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if seen[i] != seen[0] {
			t.Errorf("goroutine %d got lock %p, want %p", i, seen[i], seen[0])
		}
	}
}

// TestLockManager_CategoryLockIsAUsableRWMutex is a smoke test that the
// lock returned by lockManager actually behaves like an RWMutex — a held
// writer excludes new readers. Not testing sync.RWMutex itself (Go does
// that); testing that lockManager.lock returns a usable RWMutex rather
// than something weird.
func TestLockManager_CategoryLockIsAUsableRWMutex(t *testing.T) {
	lm := newLockManager()
	l := lm.lock(catTestA)
	l.Lock()

	var readerEntered atomic.Bool
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		otherL := lm.lock(catTestA)
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
