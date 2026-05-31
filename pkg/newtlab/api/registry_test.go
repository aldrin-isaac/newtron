package api

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryAcquireReleaseRoundTrip(t *testing.T) {
	r := NewDeployRegistry()
	ctx, release, err := r.Acquire(context.Background(), "topo1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if ctx == nil {
		t.Fatal("Acquire returned nil context")
	}
	if got := r.ActiveCount(); got != 1 {
		t.Errorf("ActiveCount after Acquire = %d, want 1", got)
	}
	release()
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after release = %d, want 0", got)
	}
}

func TestRegistryRejectsConcurrentDeployOfSameTopology(t *testing.T) {
	r := NewDeployRegistry()
	_, release, err := r.Acquire(context.Background(), "topo1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer release()

	_, _, err = r.Acquire(context.Background(), "topo1")
	if err == nil {
		t.Fatal("second Acquire on same topology should fail")
	}
	var already *AlreadyDeployingError
	if !errors.As(err, &already) {
		t.Errorf("err type = %T, want *AlreadyDeployingError", err)
	}
	if already.Topology != "topo1" {
		t.Errorf("Topology = %q, want %q", already.Topology, "topo1")
	}
}

func TestRegistryAllowsConcurrentDeploysOfDifferentTopologies(t *testing.T) {
	r := NewDeployRegistry()
	_, r1, err := r.Acquire(context.Background(), "topo-a")
	if err != nil {
		t.Fatalf("Acquire topo-a: %v", err)
	}
	defer r1()
	_, r2, err := r.Acquire(context.Background(), "topo-b")
	if err != nil {
		t.Fatalf("Acquire topo-b: %v", err)
	}
	defer r2()
	if got := r.ActiveCount(); got != 2 {
		t.Errorf("ActiveCount = %d, want 2", got)
	}
}

func TestRegistryReleaseTwiceIsSafe(t *testing.T) {
	r := NewDeployRegistry()
	_, release, err := r.Acquire(context.Background(), "topo1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // must not panic or affect a re-acquired entry
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount = %d, want 0", got)
	}
}

func TestRegistryCancelAllSignalsAllInFlightContexts(t *testing.T) {
	r := NewDeployRegistry()
	ctxA, _, err := r.Acquire(context.Background(), "topo-a")
	if err != nil {
		t.Fatalf("Acquire topo-a: %v", err)
	}
	ctxB, _, err := r.Acquire(context.Background(), "topo-b")
	if err != nil {
		t.Fatalf("Acquire topo-b: %v", err)
	}

	r.CancelAll(0) // fire cancellation; don't wait for drain in this test

	if ctxA.Err() == nil {
		t.Error("topo-a context not cancelled after CancelAll")
	}
	if ctxB.Err() == nil {
		t.Error("topo-b context not cancelled after CancelAll")
	}
}
