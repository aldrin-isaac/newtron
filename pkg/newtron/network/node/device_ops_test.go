package node

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// testSpecProvider implements SpecProvider for unit tests, using simple maps
// for each spec type. Methods return a "not found" error when a key is absent.
type testSpecProvider struct {
	services      map[string]*spec.ServiceSpec
	filterSpecs   map[string]*spec.FilterSpec
	ipvpn         map[string]*spec.IPVPNSpec
	macvpn        map[string]*spec.MACVPNSpec
	qosPolicies   map[string]*spec.QoSPolicy
	platforms     map[string]*spec.PlatformSpec
	prefixLists   map[string][]string
	routePolicies map[string]*spec.RoutePolicy
}

func (sp *testSpecProvider) GetService(name string) (*spec.ServiceSpec, error) {
	if s, ok := sp.services[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("service %q not found", name)
}

func (sp *testSpecProvider) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	if s, ok := sp.ipvpn[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("ipvpn %q not found", name)
}

func (sp *testSpecProvider) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	if s, ok := sp.macvpn[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("macvpn %q not found", name)
}

func (sp *testSpecProvider) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	if s, ok := sp.qosPolicies[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("qos policy %q not found", name)
}

func (sp *testSpecProvider) GetFilter(name string) (*spec.FilterSpec, error) {
	if s, ok := sp.filterSpecs[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("filter spec %q not found", name)
}

func (sp *testSpecProvider) GetPlatform(name string) (*spec.PlatformSpec, error) {
	if s, ok := sp.platforms[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("platform %q not found", name)
}

func (sp *testSpecProvider) GetPrefixList(name string) ([]string, error) {
	if s, ok := sp.prefixLists[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("prefix list %q not found", name)
}

func (sp *testSpecProvider) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	if s, ok := sp.routePolicies[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("route policy %q not found", name)
}

func (sp *testSpecProvider) FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec) {
	for name, m := range sp.macvpn {
		if m.VNI == vni {
			return name, m
		}
	}
	return "", nil
}

// ============================================================================
// Test Helpers
// ============================================================================

// testDevice builds a Device with common test state. The device is marked as
// connected and locked so that operations pass precondition checks. The
// configDB is pre-populated with two physical interfaces and empty maps for
// all tables that operations inspect.
func testDevice() *Node {
	n := &Node{
		SpecProvider: &testSpecProvider{
			services:      map[string]*spec.ServiceSpec{},
			filterSpecs:   map[string]*spec.FilterSpec{},
			ipvpn:         map[string]*spec.IPVPNSpec{},
			macvpn:        map[string]*spec.MACVPNSpec{},
			qosPolicies:   map[string]*spec.QoSPolicy{},
			platforms:     map[string]*spec.PlatformSpec{},
			prefixLists:   map[string][]string{},
			routePolicies: map[string]*spec.RoutePolicy{},
		},
		name:      "test-dev",
		connected: true,
		locked:    true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:   "10.255.0.1",
			LoopbackIP: "10.255.0.1",
			Zone:     "us-east",
		},
		interfaces: map[string]*Interface{},
		configDB: &sonic.ConfigDB{
			DeviceMetadata:        map[string]map[string]string{},
			Port:                  map[string]sonic.PortEntry{"Ethernet0": {}, "Ethernet4": {}},
			VLAN:                  map[string]sonic.VLANEntry{},
			VLANMember:            map[string]sonic.VLANMemberEntry{},
			VLANInterface:         map[string]map[string]string{},
			Interface:             map[string]sonic.InterfaceEntry{},
			PortChannel:           map[string]sonic.PortChannelEntry{},
			PortChannelMember:     map[string]map[string]string{},
			LoopbackInterface:     map[string]map[string]string{},
			VRF:                   map[string]sonic.VRFEntry{},
			VXLANTunnel:           map[string]sonic.VXLANTunnelEntry{},
			VXLANTunnelMap:        map[string]sonic.VXLANMapEntry{},
			VXLANEVPNNVO:          map[string]sonic.EVPNNVOEntry{},
			SuppressVLANNeigh:     map[string]map[string]string{},
			SAG:                   map[string]map[string]string{},
			SAGGlobal:             map[string]map[string]string{},
			BGPNeighbor:           map[string]sonic.BGPNeighborEntry{},
			BGPNeighborAF:         map[string]sonic.BGPNeighborAFEntry{},
			BGPGlobals:            map[string]sonic.BGPGlobalsEntry{},
			BGPGlobalsAF:          map[string]sonic.BGPGlobalsAFEntry{},
			BGPEVPNVNI:            map[string]sonic.BGPEVPNVNIEntry{},
			RouteTable:            map[string]sonic.StaticRouteEntry{},
			ACLTable:              map[string]sonic.ACLTableEntry{},
			ACLRule:               map[string]sonic.ACLRuleEntry{},
			RouteRedistribute:     map[string]sonic.RouteRedistributeEntry{},
			NewtronIntent: map[string]map[string]string{
				"device": {"operation": "setup-device", "state": "actuated"},
			},
			BGPGlobalsEVPNRT:      map[string]sonic.BGPGlobalsEVPNRTEntry{},
			BGPPeerGroup:          map[string]sonic.BGPPeerGroupEntry{},
			BGPPeerGroupAF:        map[string]sonic.BGPPeerGroupAFEntry{},
			RouteMap:              map[string]sonic.RouteMapEntry{},
			PrefixSet:             map[string]sonic.PrefixSetEntry{},
			CommunitySet:          map[string]sonic.CommunitySetEntry{},
		},
	}
	// Populate interfaces map from Port entries (mirrors RegisterPort behavior).
	// InterfaceExists checks n.interfaces for physical ports.
	for portName := range n.configDB.Port {
		n.interfaces[portName] = &Interface{node: n, name: portName}
	}
	return n
}

// assertChange searches the ChangeSet for a change matching the given table,
// key, and change type. Returns the Change on success, calls t.Fatal on failure.
func assertChange(t *testing.T, cs *ChangeSet, table, key string, ct ChangeType) *Change {
	t.Helper()
	for i := range cs.Changes {
		c := &cs.Changes[i]
		if c.Table == table && c.Key == key && c.Type == ct {
			return c
		}
	}
	t.Fatalf("expected change [%s] %s|%s not found in ChangeSet (%d changes)", ct, table, key, len(cs.Changes))
	return nil
}

// assertNoChange verifies that no change exists for the given table and key.
func assertNoChange(t *testing.T, cs *ChangeSet, table, key string) {
	t.Helper()
	for _, c := range cs.Changes {
		if c.Table == table && c.Key == key {
			t.Fatalf("unexpected change [%s] %s|%s found in ChangeSet", c.Type, table, key)
		}
	}
}

// assertField checks that a change's Fields contains the expected field/value pair.
func assertField(t *testing.T, c *Change, field, value string) {
	t.Helper()
	if c.Fields == nil {
		t.Fatalf("change %s|%s has nil Fields, expected field %q=%q", c.Table, c.Key, field, value)
	}
	got, ok := c.Fields[field]
	if !ok {
		t.Fatalf("change %s|%s missing field %q (have %v)", c.Table, c.Key, field, c.Fields)
	}
	if got != value {
		t.Errorf("change %s|%s field %q = %q, want %q", c.Table, c.Key, field, got, value)
	}
}

// ============================================================================
// VLAN Operation Tests
// ============================================================================

func TestCreateVLAN_Basic(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateVLAN(ctx, 100, VLANConfig{Description: "test-vlan"})
	if err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	c := assertChange(t, cs, "VLAN", "Vlan100", ChangeAdd)
	assertField(t, c, "vlanid", "100")
	assertField(t, c, "description", "test-vlan")

	// No VXLAN_TUNNEL_MAP when L2VNI is 0
	assertNoChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|VNI0_Vlan100")
	assertChange(t, cs, "NEWTRON_INTENT", "vlan|100", ChangeAdd)
}

func TestCreateVLAN_WithL2VNI(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateVLAN(ctx, 200, VLANConfig{L2VNI: 20200})
	if err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	assertChange(t, cs, "VLAN", "Vlan200", ChangeAdd)
	c := assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|VNI20200_Vlan200", ChangeAdd)
	assertField(t, c, "vlan", "Vlan200")
	assertField(t, c, "vni", "20200")
	assertChange(t, cs, "NEWTRON_INTENT", "vlan|200", ChangeAdd)
}

func TestCreateVLAN_IntentIdempotent(t *testing.T) {
	d := testDevice()
	// Set up existing intent — CreateVLAN should return empty ChangeSet (no-op)
	d.configDB.NewtronIntent["vlan|100"] = map[string]string{
		"operation": "create-vlan",
		"vlan_id":   "100",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.CreateVLAN(ctx, 100, VLANConfig{})
	if err != nil {
		t.Fatalf("CreateVLAN with existing intent: %v", err)
	}
	if !cs.IsEmpty() {
		t.Errorf("expected empty ChangeSet for idempotent call, got %d changes", len(cs.Changes))
	}
}

func TestDeleteVLAN_WithMembers(t *testing.T) {
	d := testDevice()
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	// VLAN intent with no DAG children — deleteIntent will succeed
	d.configDB.NewtronIntent["vlan|100"] = map[string]string{
		"operation": "create-vlan",
		"vlan_id":   "100",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.DeleteVLAN(ctx, 100)
	if err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}

	// VLAN itself deleted
	assertChange(t, cs, "VLAN", "Vlan100", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "vlan|100", ChangeDelete)
}

// ============================================================================
// PortChannel Operation Tests
// ============================================================================

func TestCreatePortChannel_Basic(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreatePortChannel(ctx, "PortChannel100", PortChannelConfig{
		MTU:      9100,
		MinLinks: 2,
		Members:  []string{"Ethernet0", "Ethernet4"},
	})
	if err != nil {
		t.Fatalf("CreatePortChannel: %v", err)
	}

	c := assertChange(t, cs, "PORTCHANNEL", "PortChannel100", ChangeAdd)
	assertField(t, c, "admin_status", "up")
	assertField(t, c, "mtu", "9100")
	assertField(t, c, "min_links", "2")

	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet0", ChangeAdd)
	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet4", ChangeAdd)
	assertChange(t, cs, "NEWTRON_INTENT", "portchannel|PortChannel100", ChangeAdd)
}

func TestAddPortChannelMember(t *testing.T) {
	d := testDevice()
	d.configDB.PortChannel["PortChannel100"] = sonic.PortChannelEntry{AdminStatus: "up"}
	d.configDB.NewtronIntent["portchannel|PortChannel100"] = map[string]string{
		"operation": "create-portchannel",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.AddPortChannelMember(ctx, "PortChannel100", "Ethernet0")
	if err != nil {
		t.Fatalf("AddPortChannelMember: %v", err)
	}

	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet0", ChangeAdd)
	assertChange(t, cs, "NEWTRON_INTENT", "portchannel|PortChannel100|Ethernet0", ChangeAdd)
}

func TestRemovePortChannelMember(t *testing.T) {
	d := testDevice()
	d.configDB.PortChannel["PortChannel100"] = sonic.PortChannelEntry{AdminStatus: "up"}
	d.configDB.PortChannelMember["PortChannel100|Ethernet0"] = map[string]string{}
	d.configDB.NewtronIntent["portchannel|PortChannel100"] = map[string]string{
		"operation": "create-portchannel", "state": "actuated",
		"_children": "portchannel|PortChannel100|Ethernet0",
	}
	d.configDB.NewtronIntent["portchannel|PortChannel100|Ethernet0"] = map[string]string{
		"operation": "add-pc-member", "state": "actuated",
		"_parents": "portchannel|PortChannel100",
	}
	ctx := context.Background()

	cs, err := d.RemovePortChannelMember(ctx, "PortChannel100", "Ethernet0")
	if err != nil {
		t.Fatalf("RemovePortChannelMember: %v", err)
	}

	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "portchannel|PortChannel100|Ethernet0", ChangeDelete)
}

// ============================================================================
// VRF Operation Tests
// ============================================================================

func TestCreateVRF_Basic(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	// CreateVRF creates a plain VRF entry without vni.  L3VNI binding is done
	// separately by BindIPVPN (via VXLAN_TUNNEL_MAP), not by CreateVRF.
	cs, err := d.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{})
	if err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	assertChange(t, cs, "VRF", "Vrf_CUST1", ChangeAdd)
	assertChange(t, cs, "NEWTRON_INTENT", "vrf|Vrf_CUST1", ChangeAdd)

	// No VXLAN_TUNNEL_MAP or vni field should be emitted by CreateVRF.
	for _, ch := range cs.Changes {
		if ch.Table == "VXLAN_TUNNEL_MAP" {
			t.Errorf("CreateVRF should not emit VXLAN_TUNNEL_MAP; got %+v", ch)
		}
	}
}

func TestDeleteVRF_NoInterfaces(t *testing.T) {
	d := testDevice()
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{}
	d.configDB.NewtronIntent["vrf|Vrf_CUST1"] = map[string]string{
		"operation": "create-vrf", "state": "actuated",
	}
	ctx := context.Background()

	cs, err := d.DeleteVRF(ctx, "Vrf_CUST1")
	if err != nil {
		t.Fatalf("DeleteVRF: %v", err)
	}

	assertChange(t, cs, "VRF", "Vrf_CUST1", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "vrf|Vrf_CUST1", ChangeDelete)
}

func TestDeleteVRF_BoundInterfacesBlocks(t *testing.T) {
	d := testDevice()
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{VNI: "30001"}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	ctx := context.Background()

	_, err := d.DeleteVRF(ctx, "Vrf_CUST1")
	if err == nil {
		t.Fatal("expected error when VRF has interfaces bound")
	}
}

// ============================================================================
// ACL Operation Tests
// ============================================================================

func TestCreateACL_Basic(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateACL(ctx, "EDGE_IN", ACLConfig{
		Type:        "L3",
		Stage:       "ingress",
		Description: "Edge ingress filter",
		Ports:       "Ethernet0",
	})
	if err != nil {
		t.Fatalf("CreateACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeAdd)
	assertField(t, c, "type", "L3")
	assertField(t, c, "stage", "ingress")
	assertField(t, c, "policy_desc", "Edge ingress filter")
	assertField(t, c, "ports", "Ethernet0")
	assertChange(t, cs, "NEWTRON_INTENT", "acl|EDGE_IN", ChangeAdd)
}

func TestDeleteACL_RemovesRules(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{Type: "L3", Stage: "ingress"}
	d.configDB.ACLRule["EDGE_IN|RULE_10"] = sonic.ACLRuleEntry{Priority: "10", PacketAction: "FORWARD"}
	d.configDB.ACLRule["EDGE_IN|RULE_20"] = sonic.ACLRuleEntry{Priority: "20", PacketAction: "DROP"}
	// A rule in a different table — should NOT be deleted
	d.configDB.ACLRule["OTHER_TABLE|RULE_10"] = sonic.ACLRuleEntry{Priority: "10"}
	// ACL intent with no DAG children — deleteIntent will succeed
	d.configDB.NewtronIntent["acl|EDGE_IN"] = map[string]string{
		"operation": "create-acl",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.DeleteACL(ctx, "EDGE_IN")
	if err != nil {
		t.Fatalf("DeleteACL: %v", err)
	}

	assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeDelete)
	assertNoChange(t, cs, "ACL_RULE", "OTHER_TABLE|RULE_10")
	assertChange(t, cs, "NEWTRON_INTENT", "acl|EDGE_IN", ChangeDelete)
}

func TestUnbindACLFromInterface(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Stage: "ingress",
		Ports: "Ethernet0,Ethernet4",
	}
	// ACL intent required for RequireACLTableExists precondition
	d.configDB.NewtronIntent["acl|EDGE_IN"] = map[string]string{
		"operation": "create-acl", "state": "actuated",
	}
	// Intent records from BindACL — one per bound interface
	d.configDB.NewtronIntent["interface|Ethernet0|acl|ingress"] = map[string]string{
		"operation": "bind-acl",
		"acl_name":  "EDGE_IN",
		"direction": "ingress",
		"state":     "actuated",
	}
	d.configDB.NewtronIntent["interface|Ethernet4|acl|ingress"] = map[string]string{
		"operation": "bind-acl",
		"acl_name":  "EDGE_IN",
		"direction": "ingress",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.UnbindACLFromInterface(ctx, "EDGE_IN", "Ethernet0")
	if err != nil {
		t.Fatalf("UnbindACLFromInterface: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	// After removing Ethernet0, only Ethernet4 should remain
	assertField(t, c, "ports", "Ethernet4")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|acl|ingress", ChangeDelete)
}

func TestAddACLRule(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{Type: "L3", Stage: "ingress"}
	d.configDB.NewtronIntent["acl|EDGE_IN"] = map[string]string{
		"operation": "create-acl",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.AddACLRule(ctx, "EDGE_IN", "RULE_10", ACLRuleConfig{
		Priority: 10,
		Action:   "permit",
		SrcIP:    "10.0.0.0/8",
		Protocol: "tcp",
		DstPort:  "179",
	})
	if err != nil {
		t.Fatalf("AddACLRule: %v", err)
	}

	c := assertChange(t, cs, "ACL_RULE", "EDGE_IN|RULE_10", ChangeAdd)
	assertField(t, c, "PRIORITY", "10")
	assertField(t, c, "PACKET_ACTION", "FORWARD")
	assertField(t, c, "SRC_IP", "10.0.0.0/8")
	assertField(t, c, "IP_PROTOCOL", "6") // tcp = 6
	assertField(t, c, "L4_DST_PORT", "179")
	assertChange(t, cs, "NEWTRON_INTENT", "acl|EDGE_IN|RULE_10", ChangeAdd)
}

// ============================================================================
// EVPN/VXLAN Operation Tests
// ============================================================================

func TestBindMACVPN(t *testing.T) {
	d := testDevice()
	d.configDB.VXLANTunnel["vtep1"] = sonic.VXLANTunnelEntry{SrcIP: "10.255.0.1"}
	// VTEP check reads "device" intent's source_ip param.
	d.configDB.NewtronIntent["device"] = map[string]string{"operation": "setup-device", "source_ip": "10.255.0.1"}
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	d.configDB.NewtronIntent["vlan|100"] = map[string]string{
		"operation": "create-vlan",
		"state":     "actuated",
	}
	d.SpecProvider.(*testSpecProvider).macvpn["TEST_MACVPN"] = &spec.MACVPNSpec{VNI: 20100}
	ctx := context.Background()

	cs, err := d.BindMACVPN(ctx, 100, "TEST_MACVPN")
	if err != nil {
		t.Fatalf("BindMACVPN: %v", err)
	}

	c := assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|VNI20100_Vlan100", ChangeAdd)
	assertField(t, c, "vlan", "Vlan100")
	assertField(t, c, "vni", "20100")
	assertChange(t, cs, "NEWTRON_INTENT", "macvpn|100", ChangeAdd)
}

func TestConfigureIRB(t *testing.T) {
	d := testDevice()
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{VNI: "30001"}
	d.configDB.NewtronIntent["vlan|100"] = map[string]string{
		"operation": "create-vlan",
		"state":     "actuated",
	}
	d.configDB.NewtronIntent["vrf|Vrf_CUST1"] = map[string]string{
		"operation": "create-vrf",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := d.ConfigureIRB(ctx, 100, IRBConfig{
		VRF:        "Vrf_CUST1",
		IPAddress:  "10.1.100.1/24",
		AnycastMAC: "00:00:00:00:01:01",
	})
	if err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}

	// Base VLAN_INTERFACE with VRF binding
	baseC := assertChange(t, cs, "VLAN_INTERFACE", "Vlan100", ChangeAdd)
	assertField(t, baseC, "vrf_name", "Vrf_CUST1")

	// IP address entry
	assertChange(t, cs, "VLAN_INTERFACE", "Vlan100|10.1.100.1/24", ChangeAdd)

	// SAG global
	sagC := assertChange(t, cs, "SAG_GLOBAL", "IPv4", ChangeAdd)
	assertField(t, sagC, "gwmac", "00:00:00:00:01:01")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Vlan100", ChangeAdd)
}

// ============================================================================
// Precondition Tests
// ============================================================================

func TestDevice_NotConnected(t *testing.T) {
	d := testDevice()
	d.connected = false
	d.actuatedIntent = true // online mode: connected/locked checks are enforced
	ctx := context.Background()

	ops := []struct {
		name string
		fn   func() error
	}{
		{"CreateVLAN", func() error { _, err := d.CreateVLAN(ctx, 100, VLANConfig{}); return err }},
		{"DeleteVLAN", func() error { _, err := d.DeleteVLAN(ctx, 100); return err }},
		{"CreatePortChannel", func() error {
			_, err := d.CreatePortChannel(ctx, "PortChannel100", PortChannelConfig{})
			return err
		}},
		{"CreateVRF", func() error { _, err := d.CreateVRF(ctx, "Vrf_TEST", VRFConfig{}); return err }},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			if err == nil {
				t.Fatal("expected error")
			}
			// Single-error preconditions wrap ErrPreconditionFailed;
			// multi-error results (e.g., disconnected + missing resource)
			// wrap ErrValidationFailed. Both are acceptable.
			if !errors.Is(err, util.ErrPreconditionFailed) && !errors.Is(err, util.ErrValidationFailed) {
				t.Errorf("error = %q, want ErrPreconditionFailed or ErrValidationFailed", err.Error())
			}
		})
	}
}

func TestDevice_NotLocked(t *testing.T) {
	d := testDevice()
	d.locked = false
	d.actuatedIntent = true // online mode: connected/locked checks are enforced
	ctx := context.Background()

	ops := []struct {
		name string
		fn   func() error
	}{
		{"CreateVLAN", func() error { _, err := d.CreateVLAN(ctx, 100, VLANConfig{}); return err }},
		{"CreatePortChannel", func() error {
			_, err := d.CreatePortChannel(ctx, "PortChannel100", PortChannelConfig{})
			return err
		}},
		{"CreateVRF", func() error { _, err := d.CreateVRF(ctx, "Vrf_TEST", VRFConfig{}); return err }},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, util.ErrPreconditionFailed) {
				t.Errorf("error = %q, want ErrPreconditionFailed", err.Error())
			}
		})
	}
}

func TestCreateVLAN_InvalidID(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	tests := []struct {
		name   string
		vlanID int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too_high", 4095},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.CreateVLAN(ctx, tt.vlanID, VLANConfig{})
			if err == nil {
				t.Fatal("expected error for invalid VLAN ID")
			}
		})
	}
}

// ============================================================================
// Round-Trip Tests (Forward + Reverse = Clean)
// ============================================================================

func TestRoundTrip_CreateDeleteVLAN(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Forward
	cs1, err := n.CreateVLAN(ctx, 100, VLANConfig{L2VNI: 20100})
	if err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "vlan|100", ChangeAdd)

	// Verify forward state via intent (projection tracks adds via op())
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("expected vlan|100 intent after CreateVLAN")
	}
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("expected vlan|100 intent after CreateVLAN (second check)")
	}

	// Reverse
	cs2, err := n.DeleteVLAN(ctx, 100)
	if err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "vlan|100", ChangeDelete)
	assertChange(t, cs2, "VLAN", "Vlan100", ChangeDelete)

	// Verify intent is cleaned up (intent DAG is updated by deleteIntent)
	if n.GetIntent("vlan|100") != nil {
		t.Error("vlan|100 intent should be removed after DeleteVLAN")
	}
}

func TestRoundTrip_CreateDeleteVRF(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	cs1, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{})
	if err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "vrf|Vrf_CUST1", ChangeAdd)

	if n.GetIntent("vrf|Vrf_CUST1") == nil {
		t.Fatal("expected vrf|Vrf_CUST1 intent")
	}

	cs2, err := n.DeleteVRF(ctx, "Vrf_CUST1")
	if err != nil {
		t.Fatalf("DeleteVRF: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "vrf|Vrf_CUST1", ChangeDelete)
	assertChange(t, cs2, "VRF", "Vrf_CUST1", ChangeDelete)

	if n.GetIntent("vrf|Vrf_CUST1") != nil {
		t.Error("vrf intent should be removed")
	}
}

func TestRoundTrip_CreateDeleteACL(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Create ACL
	cs1, err := n.CreateACL(ctx, "EDGE_IN", ACLConfig{
		Type: "L3", Stage: "ingress", Description: "test",
	})
	if err != nil {
		t.Fatalf("CreateACL: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "acl|EDGE_IN", ChangeAdd)

	// Add a rule
	cs2, err := n.AddACLRule(ctx, "EDGE_IN", "RULE_10", ACLRuleConfig{
		Priority: 10, Action: "permit", SrcIP: "10.0.0.0/8",
	})
	if err != nil {
		t.Fatalf("AddACLRule: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "acl|EDGE_IN|RULE_10", ChangeAdd)

	// Delete rule first (children before parent)
	cs3, err := n.DeleteACLRule(ctx, "EDGE_IN", "RULE_10")
	if err != nil {
		t.Fatalf("DeleteACLRule: %v", err)
	}
	assertChange(t, cs3, "NEWTRON_INTENT", "acl|EDGE_IN|RULE_10", ChangeDelete)
	assertChange(t, cs3, "ACL_RULE", "EDGE_IN|RULE_10", ChangeDelete)

	// Delete ACL table
	cs4, err := n.DeleteACL(ctx, "EDGE_IN")
	if err != nil {
		t.Fatalf("DeleteACL: %v", err)
	}
	assertChange(t, cs4, "NEWTRON_INTENT", "acl|EDGE_IN", ChangeDelete)
	assertChange(t, cs4, "ACL_TABLE", "EDGE_IN", ChangeDelete)

	// Verify clean
	if n.GetIntent("acl|EDGE_IN") != nil {
		t.Error("acl intent should be removed")
	}
	if n.GetIntent("acl|EDGE_IN|RULE_10") != nil {
		t.Error("acl rule intent should be removed")
	}
}

func TestRoundTrip_CreateDeletePortChannel(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	cs1, err := n.CreatePortChannel(ctx, "PortChannel100", PortChannelConfig{
		MTU:     9100,
		Members: []string{"Ethernet0"},
	})
	if err != nil {
		t.Fatalf("CreatePortChannel: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "portchannel|PortChannel100", ChangeAdd)

	// Add second member (so we can test RemovePortChannelMember separately)
	cs2, err := n.AddPortChannelMember(ctx, "PortChannel100", "Ethernet4")
	if err != nil {
		t.Fatalf("AddPortChannelMember: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "portchannel|PortChannel100|Ethernet4", ChangeAdd)

	// Remove member (children before parent)
	cs3, err := n.RemovePortChannelMember(ctx, "PortChannel100", "Ethernet4")
	if err != nil {
		t.Fatalf("RemovePortChannelMember: %v", err)
	}
	assertChange(t, cs3, "NEWTRON_INTENT", "portchannel|PortChannel100|Ethernet4", ChangeDelete)

	// Delete port channel
	cs4, err := n.DeletePortChannel(ctx, "PortChannel100")
	if err != nil {
		t.Fatalf("DeletePortChannel: %v", err)
	}
	assertChange(t, cs4, "NEWTRON_INTENT", "portchannel|PortChannel100", ChangeDelete)
	assertChange(t, cs4, "PORTCHANNEL", "PortChannel100", ChangeDelete)

	if n.GetIntent("portchannel|PortChannel100") != nil {
		t.Error("portchannel intent should be removed")
	}
}

func TestRoundTrip_AddRemoveBGPEVPNPeer(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Prerequisites: setup-device creates EVPN peer group + BGP globals
	if err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/setup-device",
		Params: map[string]any{
			"fields":    map[string]any{"hostname": "test", "bgp_asn": "65001"},
			"source_ip": "10.0.0.1",
		},
	}); err != nil {
		t.Fatalf("setup-device prerequisite: %v", err)
	}

	cs1, err := n.AddBGPEVPNPeer(ctx, "10.0.0.2", 65002, "", true)
	if err != nil {
		t.Fatalf("AddBGPEVPNPeer: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "evpn-peer|10.0.0.2", ChangeAdd)

	cs2, err := n.RemoveBGPEVPNPeer(ctx, "10.0.0.2")
	if err != nil {
		t.Fatalf("RemoveBGPEVPNPeer: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "evpn-peer|10.0.0.2", ChangeDelete)
	assertChange(t, cs2, "BGP_NEIGHBOR", "default|10.0.0.2", ChangeDelete)

	if n.GetIntent("evpn-peer|10.0.0.2") != nil {
		t.Error("evpn-peer intent should be removed")
	}
}

func TestRoundTrip_ConfigureUnconfigureIRB(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Prerequisites
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN prerequisite: %v", err)
	}
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF prerequisite: %v", err)
	}

	cs1, err := n.ConfigureIRB(ctx, 100, IRBConfig{
		VRF: "Vrf_CUST1", IPAddress: "10.1.100.1/24", AnycastMAC: "00:00:00:00:01:01",
	})
	if err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "interface|Vlan100", ChangeAdd)

	cs2, err := n.UnconfigureIRB(ctx, 100)
	if err != nil {
		t.Fatalf("UnconfigureIRB: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "interface|Vlan100", ChangeDelete)
	assertChange(t, cs2, "VLAN_INTERFACE", "Vlan100|10.1.100.1/24", ChangeDelete)
	assertChange(t, cs2, "VLAN_INTERFACE", "Vlan100", ChangeDelete)
	assertChange(t, cs2, "SAG_GLOBAL", "IPv4", ChangeDelete)

	if n.GetIntent("interface|Vlan100") != nil {
		t.Error("irb intent should be removed")
	}
}

func TestRoundTrip_AddRemoveStaticRoute(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF prerequisite: %v", err)
	}

	cs1, err := n.AddStaticRoute(ctx, "Vrf_CUST1", "0.0.0.0/0", "10.1.0.1", 0)
	if err != nil {
		t.Fatalf("AddStaticRoute: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "route|Vrf_CUST1|0.0.0.0/0", ChangeAdd)

	cs2, err := n.RemoveStaticRoute(ctx, "Vrf_CUST1", "0.0.0.0/0")
	if err != nil {
		t.Fatalf("RemoveStaticRoute: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "route|Vrf_CUST1|0.0.0.0/0", ChangeDelete)
	assertChange(t, cs2, "STATIC_ROUTE", "Vrf_CUST1|0.0.0.0/0", ChangeDelete)

	if n.GetIntent("route|Vrf_CUST1|0.0.0.0/0") != nil {
		t.Error("route intent should be removed")
	}
}

func TestRoundTrip_BindUnbindMACVPN(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Prerequisites: VLAN + VTEP (BindMACVPN requires RequireVTEPConfigured)
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN prerequisite: %v", err)
	}
	// Seed device intent with source_ip for VTEPSourceIP and RequireVTEPConfigured
	n.configDB.NewtronIntent["device"]["source_ip"] = "10.0.0.1"
	n.SpecProvider.(*testSpecProvider).macvpn["TEST_MACVPN"] = &spec.MACVPNSpec{VNI: 20100}

	cs1, err := n.BindMACVPN(ctx, 100, "TEST_MACVPN")
	if err != nil {
		t.Fatalf("BindMACVPN: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "macvpn|100", ChangeAdd)

	cs2, err := n.UnbindMACVPN(ctx, 100)
	if err != nil {
		t.Fatalf("UnbindMACVPN: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "macvpn|100", ChangeDelete)

	if n.GetIntent("macvpn|100") != nil {
		t.Error("macvpn intent should be removed")
	}
}
