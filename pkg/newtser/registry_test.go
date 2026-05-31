package newtser

import (
	"context"
	"testing"
	"time"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register("newtron", "v1", "http://127.0.0.1:19080")

	got := r.Get("newtron")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != "newtron" {
		t.Errorf("Name = %q, want %q", got.Name, "newtron")
	}
	if got.Upstream != "http://127.0.0.1:19080" {
		t.Errorf("Upstream = %q, want loopback", got.Upstream)
	}
	if got.LastSeen.IsZero() {
		t.Error("LastSeen not set")
	}
}

func TestRegistryReRegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register("x", "v1", "http://a")
	r.Register("x", "v1", "http://b")
	got := r.Get("x")
	if got.Upstream != "http://b" {
		t.Errorf("Upstream = %q, want %q (overwrite)", got.Upstream, "http://b")
	}
}

func TestRegistryGetMissingReturnsNil(t *testing.T) {
	r := NewRegistry()
	if got := r.Get("nope"); got != nil {
		t.Errorf("Get(missing) = %+v, want nil", got)
	}
}

func TestRegistryHeartbeatUpdatesLastSeen(t *testing.T) {
	r := NewRegistry()
	r.Register("x", "v1", "http://a")
	before := r.Get("x").LastSeen
	time.Sleep(5 * time.Millisecond)
	if !r.Heartbeat("x") {
		t.Fatal("Heartbeat returned false for registered service")
	}
	after := r.Get("x").LastSeen
	if !after.After(before) {
		t.Errorf("LastSeen did not advance: before=%v after=%v", before, after)
	}
}

func TestRegistryHeartbeatMissingReturnsFalse(t *testing.T) {
	r := NewRegistry()
	if r.Heartbeat("nope") {
		t.Error("Heartbeat(missing) = true, want false")
	}
}

func TestRegistryDeregisterRemoves(t *testing.T) {
	r := NewRegistry()
	r.Register("x", "v1", "http://a")
	if !r.Deregister("x") {
		t.Fatal("Deregister returned false for registered service")
	}
	if r.Get("x") != nil {
		t.Error("service still in registry after Deregister")
	}
}

func TestRegistryListSortedByName(t *testing.T) {
	r := NewRegistry()
	r.Register("c", "v1", "http://c")
	r.Register("a", "v1", "http://a")
	r.Register("b", "v1", "http://b")
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	if list[0].Name != "a" || list[1].Name != "b" || list[2].Name != "c" {
		t.Errorf("List not sorted: %v %v %v", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestRegistryEvictStaleRemovesExpired(t *testing.T) {
	r := NewRegistry()
	r.Register("fresh", "v1", "http://a")
	r.Register("stale", "v1", "http://b")
	// Manually age "stale".
	r.services["stale"].LastSeen = time.Now().Add(-200 * time.Millisecond)

	evicted := r.EvictStale(100 * time.Millisecond)
	if len(evicted) != 1 || evicted[0] != "stale" {
		t.Errorf("Evicted = %v, want [stale]", evicted)
	}
	if r.Get("stale") != nil {
		t.Error("stale not removed")
	}
	if r.Get("fresh") == nil {
		t.Error("fresh erroneously removed")
	}
}

func TestRegistryRunEvictionLoopFiresOnTick(t *testing.T) {
	r := NewRegistry()
	r.Register("x", "v1", "http://a")
	r.services["x"].LastSeen = time.Now().Add(-time.Hour)

	evictions := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.RunEvictionLoop(ctx, 10*time.Millisecond, 100*time.Millisecond, func(name string) {
		evictions <- name
	})

	select {
	case got := <-evictions:
		if got != "x" {
			t.Errorf("evicted = %q, want %q", got, "x")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("eviction loop did not fire")
	}
}
