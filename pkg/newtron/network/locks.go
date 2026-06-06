package network

import "sync"

// lockKey identifies one independently-lockable slice of state. What a
// key represents is up to the caller — a category like "services", a
// single resource like "service:foo", a shared document like "topology",
// a subsystem like "persist". The lockManager treats every key as opaque
// and guarantees only that distinct keys yield distinct locks.
//
// Use typed constants (defined alongside the first call site that needs
// each key) — never raw strings — so the compiler catches typos.
type lockKey string

// lockManager hands out one *sync.RWMutex per key on first request and
// returns the same lock on every subsequent request. The locks outlive
// any individual operation; lockManager guarantees there is exactly one
// lock per key for the lifetime of a Network.
//
// Callers take the returned mutex with direct Lock / RLock + defer, the
// same way Network's existing sync.RWMutex fields are used today
// (DESIGN_PRINCIPLES_NEWTRON §23 — code pattern consistency). No
// closure-wrapping helpers are exposed; if a future caller demonstrates
// a real need for one, it lives next to that caller, not here.
type lockManager struct {
	mu    sync.Mutex
	locks map[lockKey]*sync.RWMutex
}

func newLockManager() *lockManager {
	return &lockManager{locks: make(map[lockKey]*sync.RWMutex)}
}

// lock returns the *sync.RWMutex for the given key. The same key always
// yields the same lock; distinct keys yield distinct locks.
func (lm *lockManager) lock(key lockKey) *sync.RWMutex {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if l, ok := lm.locks[key]; ok {
		return l
	}
	l := &sync.RWMutex{}
	lm.locks[key] = l
	return l
}
