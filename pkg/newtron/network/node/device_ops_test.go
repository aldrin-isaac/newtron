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
	qosProfiles   map[string]*spec.QoSProfile
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

func (sp *testSpecProvider) GetQoSProfile(name string) (*spec.QoSProfile, error) {
	if s, ok := sp.qosProfiles[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("qos profile %q not found", name)
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
	return &Node{
		SpecProvider: &testSpecProvider{
			services:    map[string]*spec.ServiceSpec{},
			filterSpecs: map[string]*spec.FilterSpec{},
			ipvpn:       map[string]*spec.IPVPNSpec{},
			macvpn:      map[string]*spec.MACVPNSpec{},
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
		interfaces: make(map[string]*Interface),
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
			ACLTableType:          map[string]sonic.ACLTableTypeEntry{},
			RouteRedistribute:     map[string]sonic.RouteRedistributeEntry{},
			NewtronServiceBinding: map[string]sonic.ServiceBindingEntry{},
		},
	}
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

// assertField checks that a change's NewValue contains the expected field/value pair.
func assertField(t *testing.T, c *Change, field, value string) {
	t.Helper()
	if c.NewValue == nil {
		t.Fatalf("change %s|%s has nil NewValue, expected field %q=%q", c.Table, c.Key, field, value)
	}
	got, ok := c.NewValue[field]
	if !ok {
		t.Fatalf("change %s|%s missing field %q (have %v)", c.Table, c.Key, field, c.NewValue)
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
	assertNoChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_0_Vlan100")
}

func TestCreateVLAN_WithL2VNI(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateVLAN(ctx, 200, VLANConfig{L2VNI: 20200})
	if err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	assertChange(t, cs, "VLAN", "Vlan200", ChangeAdd)
	c := assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_20200_Vlan200", ChangeAdd)
	assertField(t, c, "vlan", "Vlan200")
	assertField(t, c, "vni", "20200")
}

func TestCreateVLAN_AlreadyExists(t *testing.T) {
	d := testDevice()
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	ctx := context.Background()

	_, err := d.CreateVLAN(ctx, 100, VLANConfig{})
	if err == nil {
		t.Fatal("expected error for existing VLAN")
	}
	if !errors.Is(err, util.ErrPreconditionFailed) {
		t.Errorf("error = %q, want ErrPreconditionFailed", err.Error())
	}
}

func TestDeleteVLAN_WithMembers(t *testing.T) {
	d := testDevice()
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	d.configDB.VLANMember["Vlan100|Ethernet0"] = sonic.VLANMemberEntry{TaggingMode: "untagged"}
	d.configDB.VLANMember["Vlan100|Ethernet4"] = sonic.VLANMemberEntry{TaggingMode: "tagged"}
	d.configDB.VXLANTunnelMap["vtep1|map_20100_Vlan100"] = sonic.VXLANMapEntry{VLAN: "Vlan100", VNI: "20100"}
	ctx := context.Background()

	cs, err := d.DeleteVLAN(ctx, 100)
	if err != nil {
		t.Fatalf("DeleteVLAN: %v", err)
	}

	// VLAN members deleted
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet4", ChangeDelete)
	// VNI mapping deleted
	assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_20100_Vlan100", ChangeDelete)
	// VLAN itself deleted
	assertChange(t, cs, "VLAN", "Vlan100", ChangeDelete)
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
}

func TestAddPortChannelMember(t *testing.T) {
	d := testDevice()
	d.configDB.PortChannel["PortChannel100"] = sonic.PortChannelEntry{AdminStatus: "up"}
	ctx := context.Background()

	cs, err := d.AddPortChannelMember(ctx, "PortChannel100", "Ethernet0")
	if err != nil {
		t.Fatalf("AddPortChannelMember: %v", err)
	}

	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet0", ChangeAdd)
	if len(cs.Changes) != 1 {
		t.Errorf("expected 1 change, got %d", len(cs.Changes))
	}
}

func TestRemovePortChannelMember(t *testing.T) {
	d := testDevice()
	d.configDB.PortChannel["PortChannel100"] = sonic.PortChannelEntry{AdminStatus: "up"}
	d.configDB.PortChannelMember["PortChannel100|Ethernet0"] = map[string]string{}
	ctx := context.Background()

	cs, err := d.RemovePortChannelMember(ctx, "PortChannel100", "Ethernet0")
	if err != nil {
		t.Fatalf("RemovePortChannelMember: %v", err)
	}

	assertChange(t, cs, "PORTCHANNEL_MEMBER", "PortChannel100|Ethernet0", ChangeDelete)
}

// ============================================================================
// VRF Operation Tests
// ============================================================================

func TestCreateVRF_WithL3VNI(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{L3VNI: 30001})
	if err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}

	c := assertChange(t, cs, "VRF", "Vrf_CUST1", ChangeAdd)
	assertField(t, c, "vni", "30001")

	mapC := assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_30001_Vrf_CUST1", ChangeAdd)
	assertField(t, mapC, "vrf", "Vrf_CUST1")
	assertField(t, mapC, "vni", "30001")
}

func TestDeleteVRF_NoInterfaces(t *testing.T) {
	d := testDevice()
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{VNI: "30001"}
	d.configDB.VXLANTunnelMap["vtep1|map_30001_Vrf_CUST1"] = sonic.VXLANMapEntry{VRF: "Vrf_CUST1", VNI: "30001"}
	ctx := context.Background()

	cs, err := d.DeleteVRF(ctx, "Vrf_CUST1")
	if err != nil {
		t.Fatalf("DeleteVRF: %v", err)
	}

	// VNI mapping deleted
	assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_30001_Vrf_CUST1", ChangeDelete)
	// VRF deleted
	assertChange(t, cs, "VRF", "Vrf_CUST1", ChangeDelete)
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

func TestCreateACLTable_Basic(t *testing.T) {
	d := testDevice()
	ctx := context.Background()

	cs, err := d.CreateACLTable(ctx, "EDGE_IN", ACLTableConfig{
		Type:        "L3",
		Stage:       "ingress",
		Description: "Edge ingress filter",
		Ports:       "Ethernet0",
	})
	if err != nil {
		t.Fatalf("CreateACLTable: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeAdd)
	assertField(t, c, "type", "L3")
	assertField(t, c, "stage", "ingress")
	assertField(t, c, "policy_desc", "Edge ingress filter")
	assertField(t, c, "ports", "Ethernet0")
}

func TestDeleteACLTable_RemovesRules(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{Type: "L3", Stage: "ingress"}
	d.configDB.ACLRule["EDGE_IN|RULE_10"] = sonic.ACLRuleEntry{Priority: "10", PacketAction: "FORWARD"}
	d.configDB.ACLRule["EDGE_IN|RULE_20"] = sonic.ACLRuleEntry{Priority: "20", PacketAction: "DROP"}
	// A rule in a different table — should NOT be deleted
	d.configDB.ACLRule["OTHER_TABLE|RULE_10"] = sonic.ACLRuleEntry{Priority: "10"}
	ctx := context.Background()

	cs, err := d.DeleteACLTable(ctx, "EDGE_IN")
	if err != nil {
		t.Fatalf("DeleteACLTable: %v", err)
	}

	assertChange(t, cs, "ACL_RULE", "EDGE_IN|RULE_10", ChangeDelete)
	assertChange(t, cs, "ACL_RULE", "EDGE_IN|RULE_20", ChangeDelete)
	assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeDelete)
	assertNoChange(t, cs, "ACL_RULE", "OTHER_TABLE|RULE_10")
}

func TestUnbindACLFromInterface(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0,Ethernet4",
	}
	ctx := context.Background()

	cs, err := d.UnbindACLFromInterface(ctx, "EDGE_IN", "Ethernet0")
	if err != nil {
		t.Fatalf("UnbindACLFromInterface: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	// After removing Ethernet0, only Ethernet4 should remain
	assertField(t, c, "ports", "Ethernet4")
}

func TestAddACLRule(t *testing.T) {
	d := testDevice()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{Type: "L3", Stage: "ingress"}
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
}

// ============================================================================
// EVPN/VXLAN Operation Tests
// ============================================================================

func TestMapL2VNI(t *testing.T) {
	d := testDevice()
	d.configDB.VXLANTunnel["vtep1"] = sonic.VXLANTunnelEntry{SrcIP: "10.255.0.1"}
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	ctx := context.Background()

	cs, err := d.MapL2VNI(ctx, 100, 20100)
	if err != nil {
		t.Fatalf("MapL2VNI: %v", err)
	}

	c := assertChange(t, cs, "VXLAN_TUNNEL_MAP", "vtep1|map_20100_Vlan100", ChangeAdd)
	assertField(t, c, "vlan", "Vlan100")
	assertField(t, c, "vni", "20100")
}

func TestConfigureSVI(t *testing.T) {
	d := testDevice()
	d.configDB.VLAN["Vlan100"] = sonic.VLANEntry{VLANID: "100"}
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{VNI: "30001"}
	ctx := context.Background()

	cs, err := d.ConfigureSVI(ctx, 100, SVIConfig{
		VRF:        "Vrf_CUST1",
		IPAddress:  "10.1.100.1/24",
		AnycastMAC: "00:00:00:00:01:01",
	})
	if err != nil {
		t.Fatalf("ConfigureSVI: %v", err)
	}

	// Base VLAN_INTERFACE with VRF binding
	baseC := assertChange(t, cs, "VLAN_INTERFACE", "Vlan100", ChangeAdd)
	assertField(t, baseC, "vrf_name", "Vrf_CUST1")

	// IP address entry
	assertChange(t, cs, "VLAN_INTERFACE", "Vlan100|10.1.100.1/24", ChangeAdd)

	// SAG global
	sagC := assertChange(t, cs, "SAG_GLOBAL", "IPv4", ChangeAdd)
	assertField(t, sagC, "gwmac", "00:00:00:00:01:01")
}

// ============================================================================
// Precondition Tests
// ============================================================================

func TestDevice_NotConnected(t *testing.T) {
	d := testDevice()
	d.connected = false
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
