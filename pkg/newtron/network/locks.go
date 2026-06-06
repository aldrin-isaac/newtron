package network

import "sync"

// lockCategory names a slice of Network state that has its own lock. Use
// typed constants (defined alongside the first call site that needs each
// category) — never raw strings — so the compiler catches typos.
type lockCategory string

// lockManager hands out one *sync.RWMutex per category on first request
// and returns the same lock on every subsequent request. The locks
// outlive any individual operation; lockManager guarantees there is
// exactly one lock per category for the lifetime of a Network.
//
// Callers take the returned mutex with direct Lock / RLock + defer, the
// same way Network's existing sync.RWMutex fields are used today
// (DESIGN_PRINCIPLES_NEWTRON §23 — code pattern consistency). No
// closure-wrapping helpers are exposed; if a future caller demonstrates
// a real need for one, it lives next to that caller, not here.
type lockManager struct {
	mu    sync.Mutex
	locks map[lockCategory]*sync.RWMutex
}

func newLockManager() *lockManager {
	return &lockManager{locks: make(map[lockCategory]*sync.RWMutex)}
}

// lock returns the *sync.RWMutex for the given category. The same category
// always yields the same lock; distinct categories yield distinct locks.
func (lm *lockManager) lock(category lockCategory) *sync.RWMutex {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if l, ok := lm.locks[category]; ok {
		return l
	}
	l := &sync.RWMutex{}
	lm.locks[category] = l
	return l
}
