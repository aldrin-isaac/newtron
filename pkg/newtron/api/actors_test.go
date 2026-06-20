package api

import (
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestEnsureLoopbackIntent_RebuildsActuatedCache is the regression
// test for #248. The bug: a NodeActor that previously built an
// actuated node (HasActuatedIntent=true) carried that flag across
// the next loopback-mode call, and the precondition gate then
// demanded a device connection + lock that loopback mode has by
// definition disengaged. The fix: ensureLoopbackIntent destroys
// and rebuilds when the cached node is actuated, symmetric with
// ensureTopologyIntent's same-shape choice.
//
// SSH-free path: BuildTopologyNode + MarkActuatedForTest simulates
// the post-InitFromDeviceIntent state.
func TestEnsureLoopbackIntent_RebuildsActuatedCache(t *testing.T) {
	s := newTestServer(t)
	ne, ok := s.networks["default"]
	if !ok {
		t.Fatalf("network 'default' missing — fixture broken")
	}
	actor := ne.getNodeActor("switch1")

	// Seed the actor with a topology-sourced node flipped to actuated,
	// the state that triggers #248.
	seeded, err := ne.net.BuildTopologyNode("switch1")
	if err != nil {
		t.Fatalf("seed BuildTopologyNode: %v", err)
	}
	newtron.MarkActuatedForTest(seeded)
	if !seeded.HasActuatedIntent() {
		t.Fatalf("seed: HasActuatedIntent = false; MarkActuatedForTest no-op")
	}
	actor.node = seeded

	if err := actor.ensureLoopbackIntent(); err != nil {
		t.Fatalf("ensureLoopbackIntent: %v", err)
	}
	if actor.node == nil {
		t.Fatal("actor.node is nil after ensureLoopbackIntent")
	}
	if actor.node.HasActuatedIntent() {
		t.Errorf("HasActuatedIntent = true; loopback mode must rebuild from topology so the precondition gate skips connect/lock checks (#248)")
	}
}

// TestEnsureLoopbackIntent_ReusesTopologyCache pins the
// "mutations accumulate" property: when the cached node is already
// topology-sourced, ensureLoopbackIntent reuses it so successive
// loopback calls see each other's writes. Without this guarantee
// the offline-config-testing workflow loses state between requests.
func TestEnsureLoopbackIntent_ReusesTopologyCache(t *testing.T) {
	s := newTestServer(t)
	ne := s.networks["default"]
	actor := ne.getNodeActor("switch1")

	seeded, err := ne.net.BuildTopologyNode("switch1")
	if err != nil {
		t.Fatalf("seed BuildTopologyNode: %v", err)
	}
	actor.node = seeded // actuatedIntent=false by default

	if err := actor.ensureLoopbackIntent(); err != nil {
		t.Fatalf("ensureLoopbackIntent: %v", err)
	}
	if actor.node != seeded {
		t.Errorf("actor.node was rebuilt; topology-sourced node should be reused so mutations accumulate")
	}
}

// TestEnsureLoopbackIntent_BuildsFirstAccess pins the first-access
// path: when the actor has no cached node, ensureLoopbackIntent
// builds one from topology.
func TestEnsureLoopbackIntent_BuildsFirstAccess(t *testing.T) {
	s := newTestServer(t)
	ne := s.networks["default"]
	actor := ne.getNodeActor("switch1")

	if actor.node != nil {
		t.Fatalf("fresh actor unexpectedly has cached node")
	}
	if err := actor.ensureLoopbackIntent(); err != nil {
		t.Fatalf("ensureLoopbackIntent: %v", err)
	}
	if actor.node == nil {
		t.Fatal("actor.node is nil after first-access build")
	}
	if actor.node.HasActuatedIntent() {
		t.Errorf("HasActuatedIntent = true on first build; expected topology-sourced")
	}
}
