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
	release()
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
		t.Fatal("second Acquire on same lab should fail")
	}
	var already *AlreadyDeployingError
	if !errors.As(err, &already) {
		t.Errorf("err type = %T, want *AlreadyDeployingError", err)
	}
	if already.Lab != "topo1" {
		t.Errorf("Lab = %q, want %q", already.Lab, "topo1")
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
}

func TestRegistryReleaseTwiceIsSafe(t *testing.T) {
	r := NewDeployRegistry()
	_, release, err := r.Acquire(context.Background(), "topo1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // must not panic or affect a re-acquired entry
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
