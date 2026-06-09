package api

import (
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// BridgeStatsStore holds the most recent BridgeStats snapshot received from
// each (lab, worker host) pair. Snapshots are kept in memory only — they are
// re-pushed within ≤pushInterval seconds, so a server restart is observable
// for at most that long. EvictLab clears every host's snapshot when the lab
// is destroyed (or its registration is otherwise gone).
//
// The store is the sole readable holder of stats per §27 (Single Owner) —
// today newtlink also holds the data in-process, but consumers reach it only
// through this server-side store, never by dialing newtlink directly.
type BridgeStatsStore struct {
	mu   sync.RWMutex
	labs map[string]map[string]storedBridgeStats // lab → host → snapshot
}

type storedBridgeStats struct {
	updatedAt time.Time
	stats     newtlab.BridgeStats
}

// NewBridgeStatsStore returns an empty store.
func NewBridgeStatsStore() *BridgeStatsStore {
	return &BridgeStatsStore{
		labs: make(map[string]map[string]storedBridgeStats),
	}
}

// Set records the latest snapshot for (lab, host) and stamps updated_at to
// the receive time. Concurrent Set calls for the same key are last-writer-
// wins — push cadence is per-host so the only contended case is a slow
// previous push racing the next one, in which case the newer wins.
func (s *BridgeStatsStore) Set(lab, host string, stats newtlab.BridgeStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, ok := s.labs[lab]
	if !ok {
		hosts = make(map[string]storedBridgeStats)
		s.labs[lab] = hosts
	}
	hosts[host] = storedBridgeStats{
		updatedAt: time.Now(),
		stats:     stats,
	}
}

// Get returns one snapshot per host that has pushed for the lab, sorted by
// host name for stable ordering. Each snapshot's age is computed at call
// time (not stored) so the value is always current relative to the read.
// Returns an empty slice if no host has pushed yet for the lab.
func (s *BridgeStatsStore) Get(lab string) []BridgeStatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hosts, ok := s.labs[lab]
	if !ok || len(hosts) == 0 {
		return []BridgeStatsSnapshot{}
	}
	now := time.Now()
	out := make([]BridgeStatsSnapshot, 0, len(hosts))
	for host, snap := range hosts {
		out = append(out, BridgeStatsSnapshot{
			Host:       host,
			UpdatedAt:  snap.updatedAt.UTC().Format(time.RFC3339Nano),
			AgeSeconds: now.Sub(snap.updatedAt).Seconds(),
			Stats:      snap.stats,
		})
	}
	sortByHost(out)
	return out
}

// EvictLab removes every host's snapshot for the lab. Called by the destroy
// handler so a destroyed lab's stale stats don't outlive the lab itself.
func (s *BridgeStatsStore) EvictLab(lab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.labs, lab)
}

// sortByHost sorts the snapshot slice by Host string. Defined here so the
// store has no exposed comparator and the GET handler doesn't need to know
// about sort ordering.
func sortByHost(snaps []BridgeStatsSnapshot) {
	for i := 1; i < len(snaps); i++ {
		for j := i; j > 0 && snaps[j-1].Host > snaps[j].Host; j-- {
			snaps[j-1], snaps[j] = snaps[j], snaps[j-1]
		}
	}
}
