package newtlab

import "testing"

// TestResolveLabNetworkID pins the precedence — explicit -N > persisted
// LabState.NetworkID > lab name — and the fallbacks for a not-yet-deployed lab
// and a lab whose state predates the NetworkID field.
func TestResolveLabNetworkID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetHomeCache(t)

	// (1) No state at all (not deployed) → lab name.
	if got := ResolveLabNetworkID("mylab", ""); got != "mylab" {
		t.Errorf("no state, no override = %q, want lab name mylab", got)
	}

	// Lab was deployed against a DISTINCT network id.
	if err := SaveState(&LabState{Name: "mylab", NetworkID: "distinctnet"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// (2) Persisted NetworkID wins over the lab name (this is the #300-class fix).
	if got := ResolveLabNetworkID("mylab", ""); got != "distinctnet" {
		t.Errorf("persisted state = %q, want distinctnet (not the lab name)", got)
	}

	// (3) Explicit override wins over persisted state.
	if got := ResolveLabNetworkID("mylab", "override"); got != "override" {
		t.Errorf("explicit override = %q, want override", got)
	}

	// State predating the NetworkID field (empty) → fall back to the lab name.
	if err := SaveState(&LabState{Name: "legacylab"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if got := ResolveLabNetworkID("legacylab", ""); got != "legacylab" {
		t.Errorf("empty NetworkID in state = %q, want lab name legacylab (fallback)", got)
	}
}

// TestLabState_NetworkID_RoundTrips pins that NetworkID survives Save→Load, so a
// lab deployed against a distinct network keeps that binding across a restart.
func TestLabState_NetworkID_RoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetHomeCache(t)
	if err := SaveState(&LabState{Name: "rt", NetworkID: "netX"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState("rt")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.NetworkID != "netX" {
		t.Errorf("loaded NetworkID = %q, want netX", got.NetworkID)
	}
}
