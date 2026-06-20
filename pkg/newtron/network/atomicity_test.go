package network

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// loadTestNetwork builds a Network from a fresh network dir seeded with a
// minimal network.json. Each test gets its own dir so file persistence
// stays isolated.
func loadTestNetwork(t *testing.T) *Network {
	t.Helper()
	dir, err := os.MkdirTemp("", "atomicity-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// network.json — minimal but valid: schema_version + an empty zones map.
	if err := os.WriteFile(filepath.Join(dir, "network.json"),
		[]byte(`{"schema_version":"1.0","zones":{}}`), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	// platforms.json — minimal.
	if err := os.WriteFile(filepath.Join(dir, "platforms.json"),
		[]byte(`{"schema_version":"1.0","platforms":{}}`), 0o644); err != nil {
		t.Fatalf("write platforms.json: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	return n
}

// TestCreateService_AtomicAgainstConcurrentCreates pins the core
// atomicity contract from #101 PR B: N goroutines call CreateService("X")
// simultaneously; exactly one returns nil and the rest return
// "service 'X' already exists". Pre-refactor the public layer composed
// internal.GetService + internal.SaveService as separate critical sections
// and two creators could both pass the existence check.
func TestCreateService_AtomicAgainstConcurrentCreates(t *testing.T) {
	n := loadTestNetwork(t)

	const N = 16
	var wg sync.WaitGroup
	wg.Add(N)
	var successes atomic.Int32
	var conflicts atomic.Int32
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			err := n.CreateService("shared", &spec.ServiceSpec{ServiceType: "routed"})
			errs[i] = err
			if err == nil {
				successes.Add(1)
			} else {
				conflicts.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Errorf("successes = %d, want exactly 1", successes.Load())
	}
	if conflicts.Load() != N-1 {
		t.Errorf("conflicts = %d, want %d", conflicts.Load(), N-1)
	}
	// NormalizeName uppercases the key for canonical form before lookup;
	// the error message therefore reports the normalized name.
	wantMsg := "service 'SHARED' already exists"
	for i, err := range errs {
		if err != nil && err.Error() != wantMsg {
			t.Errorf("goroutine %d unexpected error: %v", i, err)
		}
	}
}

// TestCreateIPVPN_AtomicAgainstConcurrentCreates mirrors the service test
// for IPVPNs — verifies the atomic contract is uniform across categories,
// not specific to one method's implementation.
func TestCreateIPVPN_AtomicAgainstConcurrentCreates(t *testing.T) {
	n := loadTestNetwork(t)

	const N = 16
	var wg sync.WaitGroup
	wg.Add(N)
	var successes atomic.Int32
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := n.CreateIPVPN("Vrf_shared", &spec.IPVPNSpec{L3VNI: 5000}); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Errorf("successes = %d, want exactly 1", successes.Load())
	}
}

// TestSnapshot_DoesNotRaceWithCreate runs CreateFilter and FiltersSnapshot
// concurrently under the race detector. Pre-refactor public ListIPVPNs
// iterated the raw Spec() map without holding any lock — a concurrent
// SaveFilter mutating that map would panic the runtime. The Snapshot
// methods take RLock and return a fresh copy, so iteration on the copy
// can't race any writer.
func TestSnapshot_DoesNotRaceWithCreate(t *testing.T) {
	n := loadTestNetwork(t)

	const writers = 8
	const readers = 8
	const opsPerGoroutine = 20

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: keep creating distinct filters.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				_ = n.CreateFilter(fmt.Sprintf("f-%d-%d", w, i), &spec.FilterSpec{})
				select {
				case <-stop:
					return
				default:
				}
			}
		}(w)
	}

	// Readers: keep iterating snapshots.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				snap := n.FiltersSnapshot()
				for name := range snap {
					_ = name
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
}

// TestAddFilterRule_AtomicAgainstConcurrentAdds pins the read-modify-write
// contract for the Add* family: concurrent AddFilterRule calls inserting
// distinct sequence numbers must all land, not silently overwrite each
// other. Pre-refactor public composed internal.GetFilter + mutate +
// internal.SaveFilter as separate critical sections — two concurrent
// adders could each load a copy of the same filter, append their rule,
// and last-writer-wins on save.
func TestAddFilterRule_AtomicAgainstConcurrentAdds(t *testing.T) {
	n := loadTestNetwork(t)

	// Seed a parent filter.
	if err := n.CreateFilter("parent", &spec.FilterSpec{}); err != nil {
		t.Fatalf("seed CreateFilter: %v", err)
	}

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(seq int) {
			defer wg.Done()
			_ = n.AddFilterRule("parent", &spec.FilterRule{Sequence: seq, Action: "permit"})
		}(i + 1)
	}
	wg.Wait()

	got, err := n.GetFilter("parent")
	if err != nil {
		t.Fatalf("GetFilter: %v", err)
	}
	if len(got.Rules) != N {
		t.Errorf("got %d rules, want %d — rules were lost to a TOCTOU race", len(got.Rules), N)
	}
}

// TestUpdateFilterRule_AtomicAgainstConcurrentUpdates pins the same
// read-modify-write contract for UpdateFilterRule. Concurrent updates
// to distinct rules must all land; concurrent updates to the SAME rule
// must serialize (one wins, the other reads the updated state). Issue #209.
func TestUpdateFilterRule_AtomicAgainstConcurrentUpdates(t *testing.T) {
	n := loadTestNetwork(t)
	if err := n.CreateFilter("parent", &spec.FilterSpec{}); err != nil {
		t.Fatalf("seed CreateFilter: %v", err)
	}
	const N = 32
	for i := 1; i <= N; i++ {
		if err := n.AddFilterRule("parent", &spec.FilterRule{Sequence: i, Action: "permit"}); err != nil {
			t.Fatalf("seed AddFilterRule: %v", err)
		}
	}
	// Each goroutine updates a distinct sequence number to flip action to "deny".
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 1; i <= N; i++ {
		go func(seq int) {
			defer wg.Done()
			_ = n.UpdateFilterRule("parent", seq, &spec.FilterRule{Sequence: seq, Action: "deny"})
		}(i)
	}
	wg.Wait()
	got, err := n.GetFilter("parent")
	if err != nil {
		t.Fatalf("GetFilter: %v", err)
	}
	if len(got.Rules) != N {
		t.Errorf("got %d rules after concurrent updates, want %d", len(got.Rules), N)
	}
	for _, r := range got.Rules {
		if r.Action != "deny" {
			t.Errorf("rule seq=%d: action=%q, want 'deny' (concurrent update was lost)", r.Sequence, r.Action)
		}
	}
}
