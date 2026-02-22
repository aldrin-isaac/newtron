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
	d.SpecProvider.(*testSpecProvider).services["customer-l3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
		VRFType:     spec.VRFTypeInterface,
	}

	// ConfigDB state: service binding + VRF + IP + INTERFACE base
	d.configDB.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{
		ServiceName: "customer-l3",
		IPAddress:   "10.1.0.0/31",
		VRFName:     "Vrf_CUST1",
	}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUST1"}
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	d.configDB.VRF["Vrf_CUST1"] = sonic.VRFEntry{}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// IP address removed
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeDelete)
	// INTERFACE base entry deleted (routed service — full cleanup)
	assertChange(t, cs, "INTERFACE", "Ethernet0", ChangeDelete)
	// Per-interface VRF deleted (derived name: service-interface)
	assertChange(t, cs, "VRF", "customer-l3-Eth0", ChangeDelete)
	// Service binding removed
	assertChange(t, cs, "NEWTRON_SERVICE_BINDING", "Ethernet0", ChangeDelete)
}

func TestRemoveService_SharedACL_LastUser(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	d.SpecProvider.(*testSpecProvider).services["customer-l3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}

	// ACL only bound to this interface → last user
	d.configDB.ACLTable["ACL_CUST_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0",
	}
	d.configDB.ACLRule["ACL_CUST_IN|RULE_10"] = sonic.ACLRuleEntry{Priority: "10"}
	d.configDB.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{
		ServiceName: "customer-l3",
		IngressACL:  "ACL_CUST_IN",
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Last user → rules + table deleted
	assertChange(t, cs, "ACL_RULE", "ACL_CUST_IN|RULE_10", ChangeDelete)
	assertChange(t, cs, "ACL_TABLE", "ACL_CUST_IN", ChangeDelete)
}

func TestRemoveService_SharedACL_NotLastUser(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	d.SpecProvider.(*testSpecProvider).services["customer-l3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}

	// ACL bound to both Ethernet0 and Ethernet4 → not last user
	d.configDB.ACLTable["ACL_CUST_IN"] = sonic.ACLTableEntry{
		Type:  "L3",
		Ports: "Ethernet0,Ethernet4",
	}
	d.configDB.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{
		ServiceName: "customer-l3",
		IngressACL:  "ACL_CUST_IN",
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Not last user → ACL_TABLE modified (interface removed), NOT deleted
	c := assertChange(t, cs, "ACL_TABLE", "ACL_CUST_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet4")
	assertNoChange(t, cs, "ACL_RULE", "ACL_CUST_IN|RULE_10")
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
	ctx := context.Background()

	cs, err := intf.BindACL(ctx, "EDGE_IN", "ingress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet4,Ethernet0")
	assertField(t, c, "stage", "ingress")
}

func TestBindACL_EmptyBindingList(t *testing.T) {
	d, intf := testInterface()
	// ACL exists but has no interfaces bound yet
	d.configDB.ACLTable["EDGE_IN"] = sonic.ACLTableEntry{
		Type: "L3",
	}
	ctx := context.Background()

	cs, err := intf.BindACL(ctx, "EDGE_IN", "egress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet0")
	assertField(t, c, "stage", "egress")
}

// ============================================================================
// BGP Neighbor Tests
// ============================================================================

func TestAddBGPNeighbor(t *testing.T) {
	d, intf := testInterface()
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	// BGP must be configured
	d.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "64512"}
	ctx := context.Background()

	cs, err := intf.AddBGPNeighbor(ctx, DirectBGPNeighborConfig{
		RemoteAS:    64513,
		Description: "peer-leaf1",
	})
	if err != nil {
		t.Fatalf("AddBGPNeighbor: %v", err)
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
}

func TestRemoveBGPNeighbor(t *testing.T) {
	d, intf := testInterface()
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	// Pre-existing neighbor
	d.configDB.BGPNeighbor["default|10.1.0.1"] = sonic.BGPNeighborEntry{
		ASN: "64513", LocalAddr: "10.1.0.0",
	}
	ctx := context.Background()

	cs, err := intf.RemoveBGPNeighbor(ctx, "10.1.0.1")
	if err != nil {
		t.Fatalf("RemoveBGPNeighbor: %v", err)
	}

	// AF entries removed first
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|ipv4_unicast", ChangeDelete)
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|ipv6_unicast", ChangeDelete)
	assertChange(t, cs, "BGP_NEIGHBOR_AF", "default|10.1.0.1|l2vpn_evpn", ChangeDelete)
	// Then neighbor
	assertChange(t, cs, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeDelete)
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
		{"AddBGPNeighbor", func() error {
			_, err := intf.AddBGPNeighbor(ctx, DirectBGPNeighborConfig{RemoteAS: 65000})
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
	d.configDB.NewtronServiceBinding["Ethernet0"] = sonic.ServiceBindingEntry{ServiceName: "existing-service"}
	d.SpecProvider.(*testSpecProvider).services["new-service"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
	}
	ctx := context.Background()

	_, err := intf.ApplyService(ctx, "new-service", ApplyServiceOpts{IPAddress: "10.0.0.1/30"})
	if err == nil {
		t.Fatal("expected error when interface already has service")
	}
	if got := err.Error(); got != "interface Ethernet0 already has service 'existing-service' - remove it first" {
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
