package node

import (
	"context"
	"errors"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// testInterface builds a Device + Interface pair ready for interface-level
// operation tests. The device is connected and locked; configDB has Ethernet0
// in the PORT table with an Interface entry.
func testInterface() (*Node, *Interface) {
	d := testDevice()
	intf := &Interface{
		node: d,
		name:   "Ethernet0",
	}
	d.interfaces["Ethernet0"] = intf
	return d, intf
}

// ============================================================================
// RemoveService Tests
// ============================================================================

func TestRemoveService_L3_Basic(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	// Register service in network spec
	d.SpecProvider.(*testSpecProvider).services["CUSTOMER_L3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
		VRFType:     spec.VRFTypeInterface,
	}

	// ConfigDB state: service binding + VRF + IP + INTERFACE base
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"vrf_type":     spec.VRFTypeInterface,
		"ip_address":   "10.1.0.0/31",
		"vrf_name":     "CUSTOMER_L3_ETH0",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
	}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "CUSTOMER_L3_ETH0"}
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	d.configDB.VRF["CUSTOMER_L3_ETH0"] = sonic.VRFEntry{}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// IP address removed
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeDelete)
	// INTERFACE base entry deleted (routed service — full cleanup)
	assertChange(t, cs, "INTERFACE", "Ethernet0", ChangeDelete)
	// Per-interface VRF deleted (derived name: SERVICE_INTF)
	assertChange(t, cs, "VRF", "CUSTOMER_L3_ETH0", ChangeDelete)
	// Service binding removed
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0", ChangeDelete)
}

func TestRemoveService_SharedACL_LastUser(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	d.SpecProvider.(*testSpecProvider).services["CUSTOMER_L3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}

	// ACL only bound to this interface → last user
	d.configDB.ACLTable["CUSTOMER_L3_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0",
	}
	d.configDB.ACLRule["CUSTOMER_L3_IN|RULE_10"] = sonic.ACLRuleEntry{Priority: "10"}
	// ACL intent with rule as child intent (DAG format)
	d.configDB.NewtronIntent["acl|CUSTOMER_L3_IN"] = map[string]string{
		"operation":  sonic.OpCreateACL,
		"state":      "actuated",
		"_children":  "acl|CUSTOMER_L3_IN|RULE_10",
	}
	d.configDB.NewtronIntent["acl|CUSTOMER_L3_IN|RULE_10"] = map[string]string{
		"operation": sonic.OpAddACLRule,
		"state":     "actuated",
		"_parents":  "acl|CUSTOMER_L3_IN",
	}
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"ingress_acl":  "CUSTOMER_L3_IN",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Last user → rules + table deleted
	assertChange(t, cs, "ACL_RULE", "CUSTOMER_L3_IN|RULE_10", ChangeDelete)
	assertChange(t, cs, "ACL_TABLE", "CUSTOMER_L3_IN", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "acl|CUSTOMER_L3_IN", ChangeDelete)
}

func TestRemoveService_SharedACL_NotLastUser(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	d.SpecProvider.(*testSpecProvider).services["CUSTOMER_L3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}

	// ACL bound to both Ethernet0 and Ethernet4 → not last user
	d.configDB.ACLTable["CUSTOMER_L3_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0,Ethernet4",
	}
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"ingress_acl":  "CUSTOMER_L3_IN",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
		"_parents":     "device",
		"_children":    "interface|Ethernet0|acl|ingress",
	}
	// ACL intent with two binding children (Ethernet0 and Ethernet4)
	d.configDB.NewtronIntent["acl|CUSTOMER_L3_IN"] = map[string]string{
		"operation": "create-acl",
		"state":     "actuated",
		"_parents":  "device",
		"_children": "interface|Ethernet0|acl|ingress,interface|Ethernet4|acl|ingress",
	}
	d.configDB.NewtronIntent["interface|Ethernet0|acl|ingress"] = map[string]string{
		"operation": "bind-acl",
		"state":     "actuated",
		"_parents":  "interface|Ethernet0,acl|CUSTOMER_L3_IN",
	}
	d.configDB.NewtronIntent["interface|Ethernet4|acl|ingress"] = map[string]string{
		"operation": "bind-acl",
		"state":     "actuated",
		"_parents":  "interface|Ethernet4,acl|CUSTOMER_L3_IN",
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Not last user → ACL_TABLE modified (interface removed), NOT deleted
	c := assertChange(t, cs, "ACL_TABLE", "CUSTOMER_L3_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet4")
	assertNoChange(t, cs, "ACL_RULE", "CUSTOMER_L3_IN|RULE_10")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0", ChangeDelete)
}

// ============================================================================
// Interface Configuration Tests
// ============================================================================

func TestSetIP(t *testing.T) {
	_, intf := testInterface()
	ctx := context.Background()

	cs, err := intf.SetIP(ctx, "10.1.0.0/31")
	if err != nil {
		t.Fatalf("SetIP: %v", err)
	}

	assertChange(t, cs, "INTERFACE", "Ethernet0", ChangeAdd)
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeAdd)
	if len(cs.Changes) != 2 {
		t.Errorf("expected 2 changes (base + IP), got %d", len(cs.Changes))
	}
}

func TestSetIP_VRFBound(t *testing.T) {
	d, intf := testInterface()
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	ctx := context.Background()

	cs, err := intf.SetIP(ctx, "10.1.0.0/31")
	if err != nil {
		t.Fatalf("SetIP: %v", err)
	}

	// When VRF-bound, only the IP subentry is written (no enableIpRouting base entry).
	// The base INTERFACE entry already exists with vrf_name; re-writing it with NULL
	// disrupts intfmgrd on CiscoVS (RCA-037).
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeAdd)
	if len(cs.Changes) != 1 {
		t.Errorf("expected 1 change (IP only, no base entry), got %d", len(cs.Changes))
	}
}

func TestSetIP_Invalid(t *testing.T) {
	_, intf := testInterface()
	ctx := context.Background()

	_, err := intf.SetIP(ctx, "not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

func TestSetVRF(t *testing.T) {
	d, intf := testInterface()
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{}
	ctx := context.Background()

	cs, err := intf.SetVRF(ctx, "Vrf_CUST1")
	if err != nil {
		t.Fatalf("SetVRF: %v", err)
	}

	c := assertChange(t, cs, "INTERFACE", "Ethernet0", ChangeModify)
	assertField(t, c, "vrf_name", "Vrf_CUST1")
}

func TestSetVRF_NotFound(t *testing.T) {
	_, intf := testInterface()
	ctx := context.Background()

	_, err := intf.SetVRF(ctx, "NonExistentVRF")
	if err == nil {
		t.Fatal("expected error for nonexistent VRF")
	}
}

func TestBindACL(t *testing.T) {
	d, intf := testInterface()
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet4",
	}
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "add-bgp-peer",
		"state":     "actuated",
	}
	d.configDB.NewtronIntent["acl|EDGE_IN"] = map[string]string{
		"operation": "create-acl",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := intf.BindACL(ctx, "EDGE_IN", "ingress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet4,Ethernet0")
	assertField(t, c, "stage", "ingress")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|acl|ingress", ChangeAdd)
}

func TestBindACL_EmptyBindingList(t *testing.T) {
	d, intf := testInterface()
	// ACL exists but has no interfaces bound yet
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{
		Type: "L3",
	}
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "add-bgp-peer",
		"state":     "actuated",
	}
	d.configDB.NewtronIntent["acl|EDGE_IN"] = map[string]string{
		"operation": "create-acl",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := intf.BindACL(ctx, "EDGE_IN", "egress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet0")
	assertField(t, c, "stage", "egress")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|acl|egress", ChangeAdd)
}

// ============================================================================
// BGP Peer Tests
// ============================================================================

func TestAddBGPPeer(t *testing.T) {
	d, intf := testInterface()
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	// BGP must be configured
	d.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "64512"}
	ctx := context.Background()

	cs, err := intf.AddBGPPeer(ctx, DirectBGPPeerConfig{
		RemoteAS:    64513,
		Description: "peer-leaf1",
	})
	if err != nil {
		t.Fatalf("AddBGPPeer: %v", err)
	}

	// Neighbor IP auto-derived from 10.1.0.0/31 → 10.1.0.1
	nc := assertChange(t, cs, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeAdd)
	assertField(t, nc, "asn", "64513")
	assertField(t, nc, "admin_status", "up")
	assertField(t, nc, "local_addr", "10.1.0.0")
	assertField(t, nc, "name", "peer-leaf1")

	// IPv4 unicast AF activated (frrcfgd uses admin_status:true to activate the neighbor in AF)
	afC := assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|ipv4_unicast", ChangeAdd)
	assertField(t, afC, "admin_status", "true")
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0", ChangeAdd)
}

func TestRemoveBGPPeer(t *testing.T) {
	d, intf := testInterface()
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	// Pre-existing neighbor
	d.configDB.BGPNeighbor["default|10.1.0.1"] = sonic.BGPNeighborEntry{
		ASN: "64513", LocalAddr: "10.1.0.0",
	}
	// Intent records: parent interface intent + bgp-peer sub-resource from AddBGPPeer
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation":  sonic.OpInterfaceInit,
		"_children":  "interface|Ethernet0|bgp-peer",
	}
	d.configDB.NewtronIntent["interface|Ethernet0|bgp-peer"] = map[string]string{
		"operation":           sonic.OpAddBGPPeer,
		"_parents":            "interface|Ethernet0",
		sonic.FieldNeighborIP: "10.1.0.1",
	}
	ctx := context.Background()

	cs, err := intf.RemoveBGPPeer(ctx)
	if err != nil {
		t.Fatalf("RemoveBGPPeer: %v", err)
	}

	// AF entries removed first
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|ipv4_unicast", ChangeDelete)
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|ipv6_unicast", ChangeDelete)
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|l2vpn_evpn", ChangeDelete)
	// Then neighbor
	assertChange(t, cs, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|bgp-peer", ChangeDelete)
}

// ============================================================================
// Precondition Tests
// ============================================================================

func TestInterface_NotConnected(t *testing.T) {
	_, intf := testInterface()
	intf.node.connected = false
	ctx := context.Background()

	ops := []struct {
		name string
		fn   func() error
	}{
		{"SetIP", func() error { _, err := intf.SetIP(ctx, "10.0.0.1/30"); return err }},
		{"SetVRF", func() error { _, err := intf.SetVRF(ctx, "default"); return err }},
		{"BindACL", func() error { _, err := intf.BindACL(ctx, "ACL1", "ingress"); return err }},
		{"AddBGPPeer", func() error {
			_, err := intf.AddBGPPeer(ctx, DirectBGPPeerConfig{RemoteAS: 65000})
			return err
		}},
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

func TestInterface_PortChannelMemberBlocksConfig(t *testing.T) {
	d, intf := testInterface()
	// Make Ethernet0 a PortChannel member
	d.configDB.PortChannelMember["PortChannel100|Ethernet0"] = map[string]string{}
	ctx := context.Background()

	// SetIP should fail for PortChannel member
	_, err := intf.SetIP(ctx, "10.0.0.1/30")
	if err == nil {
		t.Fatal("expected error for PortChannel member SetIP")
	}

	// SetVRF should fail for PortChannel member
	_, err = intf.SetVRF(ctx, "default")
	if err == nil {
		t.Fatal("expected error for PortChannel member SetVRF")
	}
}

func TestApplyService_AlreadyBound(t *testing.T) {
	d, intf := testInterface()
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{"service_name": "EXISTING_SERVICE", "state": "actuated", "operation": "apply-service", "name": "EXISTING_SERVICE"}
	d.SpecProvider.(*testSpecProvider).services["NEW_SERVICE"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}
	ctx := context.Background()

	_, err := intf.ApplyService(ctx, "NEW_SERVICE", ApplyServiceOpts{IPAddress: "10.0.0.1/30"})
	if err == nil {
		t.Fatal("expected error when interface already has service")
	}
	if got := err.Error(); got != "interface Ethernet0 already has service 'EXISTING_SERVICE' - remove it first" {
		t.Errorf("error = %q", got)
	}
}

func TestRemoveService_NoServiceBound(t *testing.T) {
	_, intf := testInterface()
	ctx := context.Background()

	_, err := intf.RemoveService(ctx)
	if err == nil {
		t.Fatal("expected error when no service bound")
	}
}

// ============================================================================
// Round-Trip Tests (Forward + Reverse = Clean)
// ============================================================================

func TestRoundTrip_ConfigureUnconfigureInterface_Routed(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Create VRF prerequisite
	n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{})

	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}

	cs1, err := iface.ConfigureInterface(ctx, InterfaceConfig{
		VRF: "Vrf_CUST1", IP: "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("ConfigureInterface: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "interface|Ethernet0", ChangeAdd)

	if n.GetIntent("interface|Ethernet0") == nil {
		t.Fatal("expected interface intent after ConfigureInterface")
	}

	cs2, err := iface.UnconfigureInterface(ctx)
	if err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "interface|Ethernet0", ChangeDelete)
	assertChange(t, cs2, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeDelete)
	assertChange(t, cs2, "INTERFACE", "Ethernet0", ChangeDelete)

	if n.GetIntent("interface|Ethernet0") != nil {
		t.Error("interface intent should be removed")
	}
}

func TestRoundTrip_AddRemoveBGPPeer(t *testing.T) {
	// Use a connected node so that the BGP neighbor written by AddBGPPeer is
	// visible in configDB when RemoveBGPPeer checks preconditions.
	// (AddBGPPeer uses buildChangeSet without applyShadow — correct for connected
	// nodes where ChangeSets are applied to Redis separately.)
	d, intf := testInterface()
	ctx := context.Background()

	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	d.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}

	cs1, err := intf.AddBGPPeer(ctx, DirectBGPPeerConfig{
		RemoteAS: 65002, Description: "test-peer",
	})
	if err != nil {
		t.Fatalf("AddBGPPeer: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "interface|Ethernet0|bgp-peer", ChangeAdd)
	assertChange(t, cs1, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeAdd)

	// Simulate applying the ChangeSet to configDB (as Redis would do in production)
	d.configDB.BGPNeighbor["default|10.1.0.1"] = sonic.BGPNeighborEntry{ASN: "65002", LocalAddr: "10.1.0.0"}
	d.configDB.NewtronIntent["interface|Ethernet0|bgp-peer"] = map[string]string{
		"operation":             sonic.OpAddBGPPeer,
		"_parents":              "interface|Ethernet0",
		sonic.FieldNeighborIP:  "10.1.0.1",
		sonic.FieldRemoteAS:    "65002",
		sonic.FieldDescription: "test-peer",
	}

	cs2, err := intf.RemoveBGPPeer(ctx)
	if err != nil {
		t.Fatalf("RemoveBGPPeer: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "interface|Ethernet0|bgp-peer", ChangeDelete)
	assertChange(t, cs2, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeDelete)
}

func TestRoundTrip_BindUnbindACL(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Create the ACL first
	_, err := n.CreateACL(ctx, "EDGE_IN", ACLConfig{Type: "L3", Stage: "ingress"})
	if err != nil {
		t.Fatalf("CreateACL: %v", err)
	}

	// Create interface intent (parent for ACL binding) via AddBGPPeer
	n.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}
	n.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := iface.AddBGPPeer(ctx, DirectBGPPeerConfig{RemoteAS: 65002}); err != nil {
		t.Fatalf("AddBGPPeer (prerequisite): %v", err)
	}

	cs1, err := iface.BindACL(ctx, "EDGE_IN", "ingress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "interface|Ethernet0|acl|ingress", ChangeAdd)

	if n.GetIntent("interface|Ethernet0|acl|ingress") == nil {
		t.Fatal("expected acl binding intent after BindACL")
	}

	cs2, err := iface.UnbindACL(ctx, "EDGE_IN")
	if err != nil {
		t.Fatalf("UnbindACL: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "interface|Ethernet0|acl|ingress", ChangeDelete)

	if n.GetIntent("interface|Ethernet0|acl|ingress") != nil {
		t.Error("acl binding intent should be removed")
	}
}
