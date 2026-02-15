package node_test

import (
	"strings"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/util"
)

// testNode creates a minimal Node for precondition testing.
func testNode(configDB *sonic.ConfigDB, connected, locked bool) *node.Node {
	return node.NewTestNode("test-leaf", configDB, connected, locked)
}

// emptyConfigDB creates a ConfigDB with all maps initialized to empty.
func emptyConfigDB() *sonic.ConfigDB {
	return &sonic.ConfigDB{
		DeviceMetadata:        make(map[string]map[string]string),
		Port:                  make(map[string]sonic.PortEntry),
		VLAN:                  make(map[string]sonic.VLANEntry),
		VLANMember:            make(map[string]sonic.VLANMemberEntry),
		VLANInterface:         make(map[string]map[string]string),
		Interface:             make(map[string]sonic.InterfaceEntry),
		PortChannel:           make(map[string]sonic.PortChannelEntry),
		PortChannelMember:     make(map[string]map[string]string),
		LoopbackInterface:     make(map[string]map[string]string),
		VRF:                   make(map[string]sonic.VRFEntry),
		VXLANTunnel:           make(map[string]sonic.VXLANTunnelEntry),
		BGPNeighbor:           make(map[string]sonic.BGPNeighborEntry),
		ACLTable:              make(map[string]sonic.ACLTableEntry),
		NewtronServiceBinding: make(map[string]sonic.ServiceBindingEntry),
		BGPPeerGroup:          make(map[string]sonic.BGPPeerGroupEntry),
	}
}

// ============================================================================
// PreconditionChecker Tests
// ============================================================================

func TestPreconditionChecker_RequireConnected_Pass(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireConnected().
		Result()
	if err != nil {
		t.Errorf("RequireConnected should pass when connected: %v", err)
	}
}

func TestPreconditionChecker_RequireConnected_Fail(t *testing.T) {
	dev := testNode(emptyConfigDB(), false, false)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireConnected().
		Result()
	if err == nil {
		t.Error("RequireConnected should fail when not connected")
	}
	if !strings.Contains(err.Error(), "connected") {
		t.Errorf("error should mention 'connected': %v", err)
	}
}

func TestPreconditionChecker_RequireLocked_Pass(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, true)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireLocked().
		Result()
	if err != nil {
		t.Errorf("RequireLocked should pass when locked: %v", err)
	}
}

func TestPreconditionChecker_RequireLocked_Fail(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireLocked().
		Result()
	if err == nil {
		t.Error("RequireLocked should fail when not locked")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error should mention 'locked': %v", err)
	}
}

func TestPreconditionChecker_ChainedChecks_AllPass(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, true)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireConnected().
		RequireLocked().
		Result()
	if err != nil {
		t.Errorf("chained passing checks should not error: %v", err)
	}
}

func TestPreconditionChecker_ChainedChecks_MultipleFailures(t *testing.T) {
	dev := testNode(emptyConfigDB(), false, false)
	checker := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireConnected().
		RequireLocked()

	if !checker.HasErrors() {
		t.Error("HasErrors should be true")
	}

	errs := checker.Errors()
	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %d", len(errs))
	}

	err := checker.Result()
	if err == nil {
		t.Error("Result should return error for multiple failures")
	}
}

func TestPreconditionChecker_RequireVLANExists_Pass(t *testing.T) {
	db := emptyConfigDB()
	db.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVLANExists(100).
		Result()
	if err != nil {
		t.Errorf("RequireVLANExists should pass: %v", err)
	}
}

func TestPreconditionChecker_RequireVLANExists_Fail(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVLANExists(100).
		Result()
	if err == nil {
		t.Error("RequireVLANExists should fail for missing VLAN")
	}
}

func TestPreconditionChecker_RequireVLANNotExists_Pass(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVLANNotExists(100).
		Result()
	if err != nil {
		t.Errorf("RequireVLANNotExists should pass: %v", err)
	}
}

func TestPreconditionChecker_RequireVLANNotExists_Fail(t *testing.T) {
	db := emptyConfigDB()
	db.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVLANNotExists(100).
		Result()
	if err == nil {
		t.Error("RequireVLANNotExists should fail for existing VLAN")
	}
}

func TestPreconditionChecker_RequireVRFExists(t *testing.T) {
	db := emptyConfigDB()
	db.VRF["Vrf_CUST1"] = sonic.VRFEntry{VNI: "10001"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVRFExists("Vrf_CUST1").
		Result()
	if err != nil {
		t.Errorf("RequireVRFExists should pass: %v", err)
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVRFExists("Vrf_MISSING").
		Result()
	if err == nil {
		t.Error("RequireVRFExists should fail for missing VRF")
	}
}

func TestPreconditionChecker_RequirePortChannelExists(t *testing.T) {
	db := emptyConfigDB()
	db.PortChannel["PortChannel100"] = sonic.PortChannelEntry{AdminStatus: "up"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequirePortChannelExists("PortChannel100").
		Result()
	if err != nil {
		t.Errorf("RequirePortChannelExists should pass: %v", err)
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequirePortChannelExists("PortChannel999").
		Result()
	if err == nil {
		t.Error("RequirePortChannelExists should fail for missing PortChannel")
	}
}

func TestPreconditionChecker_RequireACLTableExists(t *testing.T) {
	db := emptyConfigDB()
	db.ACLTable["CUSTOMER-IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Stage: "ingress",
		Ports: "Ethernet0",
	}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireACLTableExists("CUSTOMER-IN").
		Result()
	if err != nil {
		t.Errorf("RequireACLTableExists should pass: %v", err)
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireACLTableExists("NONEXISTENT").
		Result()
	if err == nil {
		t.Error("RequireACLTableExists should fail for missing ACL")
	}
}

func TestPreconditionChecker_RequireVTEPConfigured(t *testing.T) {
	db := emptyConfigDB()
	db.VXLANTunnel["vtep1"] = sonic.VXLANTunnelEntry{SrcIP: "10.0.0.1"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireVTEPConfigured().
		Result()
	if err != nil {
		t.Errorf("RequireVTEPConfigured should pass: %v", err)
	}

	devNoVTEP := testNode(emptyConfigDB(), true, false)
	err = node.NewPreconditionChecker(devNoVTEP, "test-op", "test-res").
		RequireVTEPConfigured().
		Result()
	if err == nil {
		t.Error("RequireVTEPConfigured should fail when no VTEP")
	}
}

func TestPreconditionChecker_RequireBGPConfigured(t *testing.T) {
	db := emptyConfigDB()
	db.BGPNeighbor["10.0.0.2"] = sonic.BGPNeighborEntry{ASN: "65002"}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireBGPConfigured().
		Result()
	if err != nil {
		t.Errorf("RequireBGPConfigured should pass with BGP neighbor: %v", err)
	}

	// Test via device metadata ASN
	db2 := emptyConfigDB()
	db2.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}
	dev2 := testNode(db2, true, false)
	err = node.NewPreconditionChecker(dev2, "test-op", "test-res").
		RequireBGPConfigured().
		Result()
	if err != nil {
		t.Errorf("RequireBGPConfigured should pass with device metadata ASN: %v", err)
	}

	devNoBGP := testNode(emptyConfigDB(), true, false)
	err = node.NewPreconditionChecker(devNoBGP, "test-op", "test-res").
		RequireBGPConfigured().
		Result()
	if err == nil {
		t.Error("RequireBGPConfigured should fail when no BGP")
	}
}

func TestPreconditionChecker_RequireInterfaceNotLAGMember(t *testing.T) {
	db := emptyConfigDB()
	db.PortChannelMember["PortChannel100|Ethernet0"] = map[string]string{}
	dev := testNode(db, true, false)

	// Ethernet0 IS a LAG member — should fail
	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireInterfaceNotLAGMember("Ethernet0").
		Result()
	if err == nil {
		t.Error("RequireInterfaceNotLAGMember should fail for LAG member")
	}

	// Ethernet4 is NOT a LAG member — should pass
	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireInterfaceNotLAGMember("Ethernet4").
		Result()
	if err != nil {
		t.Errorf("RequireInterfaceNotLAGMember should pass: %v", err)
	}
}

func TestPreconditionChecker_RequireNoExistingService(t *testing.T) {
	db := emptyConfigDB()
	db.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{
		ServiceName: "customer-l3",
	}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireNoExistingService("Ethernet0").
		Result()
	if err == nil {
		t.Error("RequireNoExistingService should fail for bound interface")
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequireNoExistingService("Ethernet4").
		Result()
	if err != nil {
		t.Errorf("RequireNoExistingService should pass for unbound interface: %v", err)
	}
}

func TestPreconditionChecker_RequirePeerGroupExists(t *testing.T) {
	db := emptyConfigDB()
	db.BGPPeerGroup["FABRIC"] = sonic.BGPPeerGroupEntry{}
	dev := testNode(db, true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequirePeerGroupExists("FABRIC").
		Result()
	if err != nil {
		t.Errorf("RequirePeerGroupExists should pass: %v", err)
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		RequirePeerGroupExists("NONEXISTENT").
		Result()
	if err == nil {
		t.Error("RequirePeerGroupExists should fail for missing peer group")
	}
}

func TestPreconditionChecker_CustomCheck(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)

	err := node.NewPreconditionChecker(dev, "test-op", "test-res").
		Check(true, "must be true", "").
		Result()
	if err != nil {
		t.Errorf("Check(true) should pass: %v", err)
	}

	err = node.NewPreconditionChecker(dev, "test-op", "test-res").
		Check(false, "must be true", "condition was false").
		Result()
	if err == nil {
		t.Error("Check(false) should fail")
	}
}

func TestPreconditionChecker_NoErrors(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, true)
	checker := node.NewPreconditionChecker(dev, "test-op", "test-res")

	if checker.HasErrors() {
		t.Error("new checker should not have errors")
	}

	err := checker.Result()
	if err != nil {
		t.Errorf("Result should be nil with no checks: %v", err)
	}

	errs := checker.Errors()
	if len(errs) != 0 {
		t.Errorf("Errors should be empty: %v", errs)
	}
}

// ============================================================================
// DependencyChecker Tests
// ============================================================================

func TestDependencyChecker_IsLastACLUser_OnlyPort(t *testing.T) {
	db := emptyConfigDB()
	db.ACLTable["CUST-IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0",
	}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastACLUser("CUST-IN") {
		t.Error("IsLastACLUser should be true when Ethernet0 is the only port")
	}
}

func TestDependencyChecker_IsLastACLUser_MultiplePorts(t *testing.T) {
	db := emptyConfigDB()
	db.ACLTable["CUST-IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0,Ethernet4",
	}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if dc.IsLastACLUser("CUST-IN") {
		t.Error("IsLastACLUser should be false when other ports remain")
	}
}

func TestDependencyChecker_IsLastACLUser_NonexistentACL(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastACLUser("NONEXISTENT") {
		t.Error("IsLastACLUser should be true for nonexistent ACL")
	}
}

func TestDependencyChecker_IsLastVLANMember_OnlyMember(t *testing.T) {
	db := emptyConfigDB()
	db.VLANMember["Vlan100|Ethernet0"] = sonic.VLANMemberEntry{TaggingMode: "untagged"}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastVLANMember(100) {
		t.Error("IsLastVLANMember should be true when Ethernet0 is only member")
	}
}

func TestDependencyChecker_IsLastVLANMember_MultipleMembers(t *testing.T) {
	db := emptyConfigDB()
	db.VLANMember["Vlan100|Ethernet0"] = sonic.VLANMemberEntry{TaggingMode: "untagged"}
	db.VLANMember["Vlan100|Ethernet4"] = sonic.VLANMemberEntry{TaggingMode: "tagged"}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if dc.IsLastVLANMember(100) {
		t.Error("IsLastVLANMember should be false when other members remain")
	}
}

func TestDependencyChecker_IsLastVRFUser_OnlyUser(t *testing.T) {
	db := emptyConfigDB()
	db.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastVRFUser("Vrf_CUST1") {
		t.Error("IsLastVRFUser should be true when Ethernet0 is only user")
	}
}

func TestDependencyChecker_IsLastVRFUser_MultipleUsers(t *testing.T) {
	db := emptyConfigDB()
	db.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	db.Interface["Ethernet4"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if dc.IsLastVRFUser("Vrf_CUST1") {
		t.Error("IsLastVRFUser should be false when other interfaces use VRF")
	}
}

func TestDependencyChecker_IsLastVRFUser_SkipsCompositeKeys(t *testing.T) {
	db := emptyConfigDB()
	db.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	// Composite key (IP binding) — should be skipped
	db.Interface["Ethernet0|10.1.1.1/30"] = sonic.InterfaceEntry{}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastVRFUser("Vrf_CUST1") {
		t.Error("IsLastVRFUser should skip composite keys")
	}
}

func TestDependencyChecker_IsLastServiceUser_OnlyUser(t *testing.T) {
	db := emptyConfigDB()
	db.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{
		ServiceName: "customer-l3",
	}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if !dc.IsLastServiceUser("customer-l3") {
		t.Error("IsLastServiceUser should be true when Ethernet0 is only user")
	}
}

func TestDependencyChecker_IsLastServiceUser_MultipleUsers(t *testing.T) {
	db := emptyConfigDB()
	db.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{ServiceName: "customer-l3"}
	db.NewtronServiceBinding["Ethernet4"] = sonic.ServiceBindingEntry{ServiceName: "customer-l3"}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	if dc.IsLastServiceUser("customer-l3") {
		t.Error("IsLastServiceUser should be false when other interfaces use service")
	}
}

func TestDependencyChecker_GetACLRemainingInterfaces(t *testing.T) {
	db := emptyConfigDB()
	db.ACLTable["CUST-IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0,Ethernet4,Ethernet8",
	}
	dev := testNode(db, true, false)

	dc := node.NewDependencyChecker(dev, "Ethernet0")
	remaining := dc.GetACLRemainingInterfaces("CUST-IN")
	if remaining != "Ethernet4,Ethernet8" {
		t.Errorf("GetACLRemainingInterfaces = %q, want %q", remaining, "Ethernet4,Ethernet8")
	}
}

func TestDependencyChecker_GetACLRemainingInterfaces_NonexistentACL(t *testing.T) {
	dev := testNode(emptyConfigDB(), true, false)
	dc := node.NewDependencyChecker(dev, "Ethernet0")
	remaining := dc.GetACLRemainingInterfaces("NONEXISTENT")
	if remaining != "" {
		t.Errorf("GetACLRemainingInterfaces for nonexistent ACL = %q, want empty", remaining)
	}
}

func TestDependencyChecker_NilConfigDB(t *testing.T) {
	dev := node.NewTestNode("test-leaf", nil, true, false)
	dc := node.NewDependencyChecker(dev, "Ethernet0")

	if !dc.IsLastACLUser("any") {
		t.Error("IsLastACLUser with nil configDB should return true")
	}
	if !dc.IsLastVLANMember(100) {
		t.Error("IsLastVLANMember with nil configDB should return true")
	}
	if !dc.IsLastVRFUser("any") {
		t.Error("IsLastVRFUser with nil configDB should return true")
	}
	if !dc.IsLastServiceUser("any") {
		t.Error("IsLastServiceUser with nil configDB should return true")
	}
	if dc.GetACLRemainingInterfaces("any") != "" {
		t.Error("GetACLRemainingInterfaces with nil configDB should return empty")
	}
}

// ============================================================================
// SplitCommaSeparated Tests (moved to pkg/util/strings.go)
// ============================================================================

func TestSplitCommaSeparated(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"Ethernet0", 1},
		{"Ethernet0,Ethernet4", 2},
		{"Ethernet0, Ethernet4, Ethernet8", 3},
	}

	for _, tt := range tests {
		got := util.SplitCommaSeparated(tt.input)
		if len(got) != tt.want {
			t.Errorf("SplitCommaSeparated(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
