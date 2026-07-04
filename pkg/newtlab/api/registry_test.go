package api

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryAcquireReleaseRoundTrip(t *testing.T) {
	r := NewLabOpRegistry()
	ctx, release, err := r.Acquire(context.Background(), "topo1", "deploy")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if ctx == nil {
		t.Fatal("Acquire returned nil context")
	}
	release()
}

func TestRegistryRejectsConcurrentOpOnSameLab(t *testing.T) {
	r := NewLabOpRegistry()
	// A deploy holds the slot; a provision on the same lab must be rejected,
	// and the rejection must name the operation actually in flight (deploy).
	_, release, err := r.Acquire(context.Background(), "topo1", "deploy")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer release()

	_, _, err = r.Acquire(context.Background(), "topo1", "provision")
	if err == nil {
		t.Fatal("second op on same lab should fail")
	}
	var busy *LabBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("err type = %T, want *LabBusyError", err)
	}
	if busy.Lab != "topo1" {
		t.Errorf("Lab = %q, want %q", busy.Lab, "topo1")
	}
	if busy.Op != "deploy" {
		t.Errorf("Op = %q, want the in-flight op %q (not the rejected caller's)", busy.Op, "deploy")
	}
}

func TestRegistryAllowsConcurrentDeploysOfDifferentTopologies(t *testing.T) {
	r := NewLabOpRegistry()
	_, r1, err := r.Acquire(context.Background(), "topo-a", "deploy")
	if err != nil {
		t.Fatalf("Acquire topo-a: %v", err)
	}
	defer r1()
	_, r2, err := r.Acquire(context.Background(), "topo-b", "deploy")
	if err != nil {
		t.Fatalf("Acquire topo-b: %v", err)
	}
	defer r2()
}

func TestRegistryReleaseTwiceIsSafe(t *testing.T) {
	r := NewLabOpRegistry()
	_, release, err := r.Acquire(context.Background(), "topo1", "deploy")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // must not panic or affect a re-acquired entry
}

func TestRegistryCancelAllSignalsAllInFlightContexts(t *testing.T) {
	r := NewLabOpRegistry()
	ctxA, _, err := r.Acquire(context.Background(), "topo-a", "deploy")
	if err != nil {
		t.Fatalf("Acquire topo-a: %v", err)
	}
	ctxB, _, err := r.Acquire(context.Background(), "topo-b", "deploy")
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
