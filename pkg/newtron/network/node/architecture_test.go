package node

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// ============================================================================
// Architecture compliance tests — unified pipeline architecture
//
// These tests verify structural invariants of the intent-first architecture:
//   - Intent DB is primary state; projection is derived (§1)
//   - RebuildProjection re-derives from intents, not cumulative (§1, §8)
//   - ConnectTransport does not overwrite projection (§7)
//   - Lock is no-op without transport (§8)
//   - Dry-run snapshot/restore preserves intent DB (§8)
//   - Mode properties: actuatedIntent, unsavedIntents (§3)
//
// Transport-dependent tests (drift guard in Lock, full Drift/Reconcile,
// Save to topology.json, Reload/Clear) require mock transport or integration
// testing and are noted as TODOs at the end of this file.
// ============================================================================

// newRawAbstractNode creates a bare abstract node without SetupDevice.
// Use this when testing pre-intent properties (empty projection, no intents).
func newRawAbstractNode() *Node {
	sp := &testSpecProvider{
		services:      map[string]*spec.ServiceSpec{},
		filterSpecs:   map[string]*spec.FilterSpec{},
		ipvpn:         map[string]*spec.IPVPNSpec{},
		macvpn:        map[string]*spec.MACVPNSpec{},
		qosPolicies:   map[string]*spec.QoSPolicy{},
		platforms:     map[string]*spec.PlatformSpec{},
		prefixLists:   map[string][]string{},
		routePolicies: map[string]*spec.RoutePolicy{},
	}
	profile := &spec.DeviceProfile{
		UnderlayASN: 65001,
		EVPN:        &spec.EVPNConfig{},
	}
	resolved := &spec.ResolvedProfile{
		UnderlayASN: 65001,
		RouterID:    "10.0.0.1",
		LoopbackIP:  "10.0.0.1",
		DeviceName:  "test-leaf",
	}
	n := NewAbstract(sp, "test-leaf", profile, resolved)
	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up", "speed": "100G"})
	n.RegisterPort("Ethernet4", map[string]string{"admin_status": "up", "speed": "100G"})
	return n
}

// newTestAbstractNode creates an abstract node with SetupDevice already called.
// The root "device" intent is present, enabling all child intents (VLANs, VRFs, etc.).
// unsavedIntents is cleared so tests start from a known clean state.
func newTestAbstractNode() *Node {
	n := newRawAbstractNode()
	ctx := context.Background()
	if _, err := n.SetupDevice(ctx, SetupDeviceOpts{}); err != nil {
		panic("SetupDevice in test helper: " + err.Error())
	}
	n.ClearUnsavedIntents()
	return n
}

// ============================================================================
// M1: RebuildProjection freshness guarantee
// Architecture §1, §8: "The projection is derived from intent replay."
// ============================================================================

func TestRebuildProjection_PreservesIntentDB(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Create two intents.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN 100: %v", err)
	}
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	// Count intents before rebuild.
	intentsBefore := len(n.configDB.NewtronIntent)

	if err := n.RebuildProjection(ctx); err != nil {
		t.Fatalf("RebuildProjection: %v", err)
	}

	// Intent count should be the same — RebuildProjection replays from the
	// same intent DB it read.
	intentsAfter := len(n.configDB.NewtronIntent)
	if intentsAfter != intentsBefore {
		t.Errorf("intent count changed: before=%d, after=%d", intentsBefore, intentsAfter)
	}

	// Verify specific intents still exist.
	if n.GetIntent("vlan|100") == nil {
		t.Error("vlan|100 intent should survive rebuild")
	}
	if n.GetIntent("vrf|Vrf_CUST1") == nil {
		t.Error("vrf|Vrf_CUST1 intent should survive rebuild")
	}
	if n.GetIntent("device") == nil {
		t.Error("device intent should survive rebuild")
	}
}

func TestRebuildProjection_PreservesPorts(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	if err := n.RebuildProjection(ctx); err != nil {
		t.Fatalf("RebuildProjection: %v", err)
	}

	// Ports should be preserved after rebuild.
	if _, ok := n.configDB.Port["Ethernet0"]; !ok {
		t.Fatal("Ethernet0 should be preserved after RebuildProjection")
	}
	if _, ok := n.configDB.Port["Ethernet4"]; !ok {
		t.Fatal("Ethernet4 should be preserved after RebuildProjection")
	}
}

func TestRebuildProjection_ClearsStaleProjection(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Manually inject a stale entry into the projection that has NO intent backing.
	n.configDB.VLAN["Vlan999"] = sonic.VLANEntry{VLANID: "999"}

	// Verify stale entry exists.
	if _, ok := n.configDB.VLAN["Vlan999"]; !ok {
		t.Fatal("stale entry should exist before rebuild")
	}

	// RebuildProjection should discard the stale entry.
	if err := n.RebuildProjection(ctx); err != nil {
		t.Fatalf("RebuildProjection: %v", err)
	}

	// Vlan999 should be gone (no intent backing).
	if _, ok := n.configDB.VLAN["Vlan999"]; ok {
		t.Fatal("Vlan999 should be gone after rebuild (no intent backing)")
	}
}

// ============================================================================
// M2: Projection never loaded from device
// Architecture §11: "configDB = projection from intents (never loaded from device)"
// ============================================================================

func TestNewAbstract_EmptyProjection(t *testing.T) {
	n := newRawAbstractNode()

	// NewAbstract starts with empty projection — not loaded from any device.
	if len(n.configDB.VLAN) != 0 {
		t.Errorf("VLAN should be empty, got %d entries", len(n.configDB.VLAN))
	}
	if len(n.configDB.VRF) != 0 {
		t.Errorf("VRF should be empty, got %d entries", len(n.configDB.VRF))
	}
	if len(n.configDB.BGPGlobals) != 0 {
		t.Errorf("BGPGlobals should be empty, got %d entries", len(n.configDB.BGPGlobals))
	}
	if len(n.configDB.BGPNeighbor) != 0 {
		t.Errorf("BGPNeighbor should be empty, got %d entries", len(n.configDB.BGPNeighbor))
	}
	if len(n.configDB.NewtronIntent) != 0 {
		t.Errorf("NewtronIntent should be empty, got %d entries", len(n.configDB.NewtronIntent))
	}

	// Ports ARE populated (via RegisterPort — pre-intent infrastructure).
	if len(n.configDB.Port) != 2 {
		t.Errorf("Port should have 2 entries (from RegisterPort), got %d", len(n.configDB.Port))
	}
}

func TestNewAbstract_NoTransport(t *testing.T) {
	n := newRawAbstractNode()

	// NewAbstract has no transport connection.
	if n.conn != nil {
		t.Error("conn should be nil for abstract node")
	}
	if n.connected {
		t.Error("connected should be false for abstract node")
	}
	if n.locked {
		t.Error("locked should be false for abstract node")
	}
	if n.actuatedIntent {
		t.Error("actuatedIntent should be false for abstract node (topology mode)")
	}
}

// ============================================================================
// M4 (partial): Lock is no-op without transport
// Architecture §8: "Lock, Apply, Unlock are no-ops without transport."
// ============================================================================

func TestLock_NoOpWithoutTransport(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Lock on an abstract node (no transport) should be a no-op.
	err := n.Lock(ctx)
	if err != nil {
		t.Errorf("Lock should be no-op without transport: %v", err)
	}

	// locked should still be false — no-op means no state change.
	if n.locked {
		t.Error("locked should remain false after no-op Lock")
	}
}

// ============================================================================
// M5: Mode properties — actuatedIntent, unsavedIntents
// Architecture §3: "Three states differ only in intent source"
// ============================================================================

func TestModeProperties_TopologyMode(t *testing.T) {
	n := newTestAbstractNode()

	// Topology mode: actuatedIntent = false.
	if n.HasActuatedIntent() {
		t.Error("abstract node should NOT have actuated intent")
	}
	if n.HasUnsavedIntents() {
		t.Error("fresh abstract node should NOT have unsaved intents")
	}
}

func TestUnsavedIntents_SetByWriteIntent(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Initially no unsaved intents.
	if n.HasUnsavedIntents() {
		t.Fatal("should not have unsaved intents initially")
	}

	// Create a VLAN — this calls writeIntent internally.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Now should have unsaved intents.
	if !n.HasUnsavedIntents() {
		t.Error("should have unsaved intents after CreateVLAN")
	}
}

func TestUnsavedIntents_ClearedByClear(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Create a VLAN to set unsavedIntents.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if !n.HasUnsavedIntents() {
		t.Fatal("should have unsaved intents after CreateVLAN")
	}

	// Clear it.
	n.ClearUnsavedIntents()
	if n.HasUnsavedIntents() {
		t.Error("unsaved intents should be cleared after ClearUnsavedIntents")
	}
}

func TestUnsavedIntents_ClearedAfterReplay(t *testing.T) {
	// In production, BuildAbstractNode calls ReplayStep (which sets
	// unsavedIntents via writeIntent) then calls ClearUnsavedIntents.
	// This test verifies the pattern: replay sets the flag, but the
	// topology construction clears it afterward.
	n := newRawAbstractNode()
	ctx := context.Background()

	// Replay setup-device (creates "device" parent intent).
	setupStep := spec.TopologyStep{
		URL:    "/setup-device",
		Params: map[string]any{},
	}
	if err := ReplayStep(ctx, n, setupStep); err != nil {
		t.Fatalf("ReplayStep setup-device: %v", err)
	}

	// Replay create-vlan.
	vlanStep := spec.TopologyStep{
		URL: "/create-vlan",
		Params: map[string]any{
			"vlan_id": 100,
		},
	}
	if err := ReplayStep(ctx, n, vlanStep); err != nil {
		t.Fatalf("ReplayStep create-vlan: %v", err)
	}

	// writeIntent sets unsavedIntents during replay.
	if !n.HasUnsavedIntents() {
		t.Fatal("writeIntent should set unsavedIntents during replay")
	}

	// Clear after replay — mimics BuildAbstractNode behavior.
	n.ClearUnsavedIntents()
	if n.HasUnsavedIntents() {
		t.Error("unsaved intents should be cleared after ClearUnsavedIntents")
	}

	// Verify intents were created.
	if n.GetIntent("device") == nil {
		t.Error("device intent should exist after replay")
	}
	if n.GetIntent("vlan|100") == nil {
		t.Error("vlan|100 intent should exist after replay")
	}
}

func TestActuatedIntent_FlagOnConstruction(t *testing.T) {
	n := newRawAbstractNode()

	// NewAbstract creates a topology-mode node.
	if n.actuatedIntent {
		t.Error("NewAbstract should set actuatedIntent = false")
	}

	// Manually set it to simulate actuated mode construction.
	n.actuatedIntent = true
	if !n.HasActuatedIntent() {
		t.Error("HasActuatedIntent should return true when flag is set")
	}
}

// ============================================================================
// M6: Dry-run snapshot/restore — Execute preserves intent DB
// Architecture §8: "Dry-run snapshots intent DB, runs fn, then restores."
// ============================================================================

func TestSnapshotRestore_IntentDBPreserved(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Create initial intents.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Snapshot the intent DB.
	snapshot := n.SnapshotIntentDB()

	// Verify snapshot has the intent.
	if _, ok := snapshot["vlan|100"]; !ok {
		t.Fatal("snapshot should contain vlan|100")
	}

	// Add more intents after snapshot.
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	// Verify both intents exist now.
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("vlan|100 should exist")
	}
	if n.GetIntent("vrf|Vrf_CUST1") == nil {
		t.Fatal("vrf|Vrf_CUST1 should exist")
	}

	// Restore from snapshot — should discard the VRF intent.
	n.RestoreIntentDB(snapshot)

	// vlan|100 should survive (was in snapshot).
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("vlan|100 should survive restore")
	}

	// vrf|Vrf_CUST1 should be gone (added after snapshot).
	if n.GetIntent("vrf|Vrf_CUST1") != nil {
		t.Error("vrf|Vrf_CUST1 should be gone after restore")
	}
}

func TestSnapshotRestore_DeepCopy(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Snapshot.
	snapshot := n.SnapshotIntentDB()

	// Mutate the snapshot — this should NOT affect the live intent DB.
	snapshot["vlan|100"]["operation"] = "mutated"

	// Live intent should be unchanged.
	intent := n.GetIntent("vlan|100")
	if intent == nil {
		t.Fatal("intent should exist")
	}
	if intent.Operation == "mutated" {
		t.Error("snapshot mutation should NOT affect live intent DB — snapshot must be a deep copy")
	}
}

func TestSnapshotRestore_ProjectionLeftDirty(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Create a VLAN.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Snapshot.
	snapshot := n.SnapshotIntentDB()

	// Create a VRF (adds to both intent DB and projection).
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	// Verify VRF is in projection (key = VRF name directly).
	if _, ok := n.configDB.VRF["Vrf_CUST1"]; !ok {
		t.Fatal("VRF should be in projection after CreateVRF")
	}

	// Restore intent DB — but projection is left dirty.
	n.RestoreIntentDB(snapshot)

	// VRF should still be in projection (RestoreIntentDB does NOT rebuild projection).
	// This is by design — the next execute() rebuilds it via RebuildProjection.
	if _, ok := n.configDB.VRF["Vrf_CUST1"]; !ok {
		t.Fatal("VRF should still be in projection (dirty) — RestoreIntentDB does not rebuild projection")
	}

	// VRF intent should be gone (not in snapshot).
	if n.GetIntent("vrf|Vrf_CUST1") != nil {
		t.Error("vrf|Vrf_CUST1 intent should be gone after restore")
	}

	// VLAN intent should survive (was in snapshot).
	if n.GetIntent("vlan|100") == nil {
		t.Error("vlan|100 intent should survive restore")
	}
}

// ============================================================================
// M3 (partial): DisconnectTransport preserves projection
// Architecture §7: "Transport is additive — it enables device I/O without
// disturbing expected state."
// ============================================================================

func TestDisconnectTransport_PreservesProjection(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Build up some projection state.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Simulate that transport was previously connected.
	n.conn = &sonic.Device{}
	n.connected = true

	// DisconnectTransport should close the connection but NOT touch projection.
	n.DisconnectTransport()

	// Transport should be gone.
	if n.conn != nil {
		t.Error("conn should be nil after DisconnectTransport")
	}
	if n.connected {
		t.Error("connected should be false after DisconnectTransport")
	}

	// Projection should be intact.
	if _, ok := n.configDB.VLAN["Vlan100"]; !ok {
		t.Error("VLAN should survive DisconnectTransport — projection must not be disturbed")
	}
	if n.GetIntent("vlan|100") == nil {
		t.Error("intent should survive DisconnectTransport")
	}
}

// ============================================================================
// Architecture: render(cs) updates projection on every config method
// Architecture §5: "render runs on every path — replay, interactive, online, offline"
// ============================================================================

func TestRender_UpdatesProjectionOnEveryConfigMethod(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// VLAN: create should render VLAN entry.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, ok := n.configDB.VLAN["Vlan100"]; !ok {
		t.Error("CreateVLAN should render VLAN entry into projection")
	}

	// VRF: create should render VRF entry.
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}
	if _, ok := n.configDB.VRF["Vrf_CUST1"]; !ok {
		t.Error("CreateVRF should render VRF entry into projection")
	}

	// ACL: create should render ACL_TABLE entry.
	if _, err := n.CreateACL(ctx, "TEST_ACL", ACLConfig{Type: "L3", Stage: "ingress"}); err != nil {
		t.Fatalf("CreateACL: %v", err)
	}
	if _, ok := n.configDB.ACLTable["TEST_ACL"]; !ok {
		t.Error("CreateACL should render ACL_TABLE entry into projection")
	}

	// PortChannel: create should render PORTCHANNEL entry.
	if _, err := n.CreatePortChannel(ctx, "PortChannel100", PortChannelConfig{}); err != nil {
		t.Fatalf("CreatePortChannel: %v", err)
	}
	if _, ok := n.configDB.PortChannel["PortChannel100"]; !ok {
		t.Error("CreatePortChannel should render PORTCHANNEL entry into projection")
	}

	// Delete VLAN: should remove from projection.
	if _, err := n.DeleteVLAN(ctx, 100); err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}
	if _, ok := n.configDB.VLAN["Vlan100"]; ok {
		t.Error("DeleteVLAN should remove VLAN entry from projection")
	}
}

// ============================================================================
// Architecture: Tree reads intent DB (§6)
// ============================================================================

func TestTree_ReadsIntentDB(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Node has SetupDevice already — Tree should return its step.
	tree := n.Tree()
	baseSteps := len(tree.Steps) // 1 step for setup-device

	// Create some intents.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	// Tree should return more steps after creating intents.
	tree = n.Tree()
	if len(tree.Steps) <= baseSteps {
		t.Fatalf("Tree should have more steps after creating intents, got %d (base was %d)",
			len(tree.Steps), baseSteps)
	}

	// Verify specific operations appear in steps.
	ops := make(map[string]bool)
	for _, step := range tree.Steps {
		ops[step.URL] = true
	}
	if !ops["/create-vlan"] {
		t.Error("Tree should include create-vlan step")
	}
	if !ops["/create-vrf"] {
		t.Error("Tree should include create-vrf step")
	}
	if !ops["/setup-device"] {
		t.Error("Tree should include setup-device step")
	}
}

// ============================================================================
// Architecture: Intent DB is the decision substrate
// Preconditions use GetIntent, not projection.
// ============================================================================

func TestIntentDB_PreconditionsUseIntents(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Create a VLAN.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// Creating the same VLAN again should be idempotent (intent exists).
	cs, err := n.CreateVLAN(ctx, 100, VLANConfig{})
	if err != nil {
		t.Fatalf("CreateVLAN idempotent: %v", err)
	}
	// Should return an empty ChangeSet (no new changes).
	if len(cs.Changes) != 0 {
		t.Error("idempotent CreateVLAN should return empty ChangeSet")
	}

	// Deleting the VLAN should work (intent exists).
	if _, err := n.DeleteVLAN(ctx, 100); err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}

	// Deleting again should fail (intent gone — precondition uses GetIntent).
	if _, err := n.DeleteVLAN(ctx, 100); err == nil {
		t.Error("DeleteVLAN on non-existent VLAN should fail")
	}
}

// ============================================================================
// Architecture: Config methods update both intent DB and projection atomically.
// Architecture §4: "By return, the intent DB and projection are both updated."
// ============================================================================

func TestConfigMethod_UpdatesIntentAndProjection(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()

	// Before: no VLAN intent or projection.
	if n.GetIntent("vlan|100") != nil {
		t.Fatal("vlan|100 intent should not exist before CreateVLAN")
	}
	if _, ok := n.configDB.VLAN["Vlan100"]; ok {
		t.Fatal("Vlan100 projection should not exist before CreateVLAN")
	}

	// Create VLAN.
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	// After: both intent DB and projection updated.
	if n.GetIntent("vlan|100") == nil {
		t.Error("intent DB should have vlan|100 after CreateVLAN")
	}
	if _, ok := n.configDB.VLAN["Vlan100"]; !ok {
		t.Error("projection should have Vlan100 after CreateVLAN")
	}

	// Delete VLAN.
	if _, err := n.DeleteVLAN(ctx, 100); err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}

	// After delete: both intent DB and projection cleared.
	if n.GetIntent("vlan|100") != nil {
		t.Error("intent DB should not have vlan|100 after DeleteVLAN")
	}
	if _, ok := n.configDB.VLAN["Vlan100"]; ok {
		t.Error("projection should not have Vlan100 after DeleteVLAN")
	}
}

// ============================================================================
// Transport-dependent tests — deferred to integration testing
//
// M3 (full): ConnectTransport does not overwrite projection
//   Requires: real SSH + Redis connection to verify n.configDB unchanged
//   after ConnectTransport establishes n.conn.
//
// M4 (full): Drift guard in Lock refuses writes when drifted
//   Requires: mock Redis client returning divergent CONFIG_DB so
//   DiffConfigDB inside Lock finds non-empty drift.
//
// M7: Reconcile delivers full projection to device
//   Requires: mock Redis client to verify ExportEntries + ReplaceAll.
//
// M8: Drift end-to-end (Node.Drift)
//   Requires: mock Redis client returning actual CONFIG_DB for comparison.
//
// M9: Save persists intent DB to topology.json
//   Requires: topology file fixtures and SaveDeviceIntents path.
//
// M10: Reload/Clear rebuild node from topology.json
//   Requires: topology file fixtures and BuildAbstractNode/BuildEmptyAbstractNode.
// ============================================================================
