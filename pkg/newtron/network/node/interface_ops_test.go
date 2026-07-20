package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
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

	// ConfigDB state: identity record + service binding sub-resource + VRF +
	// IP + INTERFACE base. The binding (interface|<name>|service) is a child
	// of the identity record (interface|<name>).
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": sonic.OpInterfaceInit,
		"state":     "actuated",
		"_children": "interface|Ethernet0|service",
	}
	d.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"vrf_type":     spec.VRFTypeInterface,
		"ip_address":   "10.1.0.0/31",
		"vrf_name":     "Vrf_CUSTOMER_L3_ETH0",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
		"_parents":     "interface|Ethernet0",
	}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUSTOMER_L3_ETH0"}
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	d.configDB.VRF["Vrf_CUSTOMER_L3_ETH0"] = sonic.VRFEntry{}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// IP address removed
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.1.0.0/31", ChangeDelete)
	// INTERFACE base entry deleted (routed service — full cleanup)
	assertChange(t, cs, "INTERFACE", "Ethernet0", ChangeDelete)
	// Per-interface VRF deleted (derived name: SERVICE_INTF)
	assertChange(t, cs, "VRF", "Vrf_CUSTOMER_L3_ETH0", ChangeDelete)
	// Service binding removed, then the childless identity record
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|service", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0", ChangeDelete)
}

// TestRemoveService_InterfaceMode_KeepsSharedIPVPN guards the §15
// reference-aware reverse: removing an interface-mode service that references an
// IP-VPN whose L3VNI is bound to a DIFFERENT VRF (a shared-mode peer holds it;
// this service's BindIPVPN no-op'd on the one-L3VNI-per-device guard) must NOT
// unbind that IP-VPN. The recorded vrf_name is the reference check — the ipvpn
// intent's Vrf_IPVPN differs from the per-interface Vrf_CUSTOMER_L3_ETH0 we
// destroy, so it survives (else the shared peer is stranded — the bug an
// interface-mode + ipvpn apply first exposed).
func TestRemoveService_InterfaceMode_KeepsSharedIPVPN(t *testing.T) {
	d, intf := testInterface()
	ctx := context.Background()

	d.SpecProvider.(*testSpecProvider).services["CUSTOMER_L3"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeEVPNRouted,
		VRFType:     spec.VRFTypeInterface,
		IPVPN:       "IPVPN",
	}

	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": sonic.OpInterfaceInit,
		"state":     "actuated",
		"_children": "interface|Ethernet0|service",
	}
	// Interface-mode binding: its own per-interface VRF, but referencing the
	// shared IP-VPN (BindIPVPN no-op'd, so this VRF never carried the L3VNI).
	d.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"vrf_type":     spec.VRFTypeInterface,
		"ip_address":   "10.1.0.0/31",
		"vrf_name":     "Vrf_CUSTOMER_L3_ETH0",
		"ipvpn":        "IPVPN",
		"l3vni":        "10001",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
		"_parents":     "interface|Ethernet0",
	}
	// The shared peer's IP-VPN binding — bound to Vrf_IPVPN, NOT our VRF.
	d.configDB.NewtronIntent["ipvpn|IPVPN"] = map[string]string{
		"ipvpn":     "IPVPN",
		"vrf_name":  "Vrf_IPVPN",
		"l3vni":     "10001",
		"operation": "bind-ipvpn",
		"state":     "actuated",
		"_parents":  "vrf|Vrf_IPVPN",
	}
	d.configDB.Interface["Ethernet0"] = sonic.InterfaceEntry{VRFName: "Vrf_CUSTOMER_L3_ETH0"}
	d.configDB.Interface["Ethernet0|10.1.0.0/31"] = sonic.InterfaceEntry{}
	d.configDB.VRF["Vrf_CUSTOMER_L3_ETH0"] = sonic.VRFEntry{}
	d.configDB.VRF["Vrf_IPVPN"] = sonic.VRFEntry{VNI: "10001"}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Our per-interface VRF is destroyed...
	assertChange(t, cs, "VRF", "Vrf_CUSTOMER_L3_ETH0", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|service", ChangeDelete)
	// ...but the shared peer's IP-VPN binding and its VRF are untouched.
	assertNoChange(t, cs, "NEWTRON_INTENT", "ipvpn|IPVPN")
	assertNoChange(t, cs, "VRF", "Vrf_IPVPN")
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
		"operation": sonic.OpInterfaceInit,
		"state":     "actuated",
		"_children": "interface|Ethernet0|service",
	}
	d.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"ingress_acl":  "CUSTOMER_L3_IN",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
		"_parents":     "interface|Ethernet0",
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Last user → rules + table deleted (the service-created ACL has no
	// interface binding children, so this interface is the last user).
	assertChange(t, cs, "ACL_RULE", "CUSTOMER_L3_IN|RULE_10", ChangeDelete)
	assertChange(t, cs, "ACL_TABLE", "CUSTOMER_L3_IN", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|service", ChangeDelete)
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
	// Identity record parents the binding and the standalone ACL binding.
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": sonic.OpInterfaceInit,
		"state":     "actuated",
		"_parents":  "device",
		"_children": "interface|Ethernet0|service,interface|Ethernet0|acl|ingress",
	}
	d.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{
		"service_name": "CUSTOMER_L3",
		"service_type": spec.ServiceTypeEVPNRouted,
		"ingress_acl":  "CUSTOMER_L3_IN",
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "CUSTOMER_L3",
		"_parents":     "interface|Ethernet0",
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
	// Binding and (once childless) the identity are removed.
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|service", ChangeDelete)
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
	// VRF() reads from intent DB (Phase 2: intent-based reads).
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"vrf":       "Vrf_CUST1",
	}
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
	// VRF intent required for the VRFExists check (intent-first)
	d.configDB.NewtronIntent["vrf|Vrf_CUST1"] = map[string]string{"op": "create-vrf", "name": "Vrf_CUST1"}
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
	// Existing binding intent for Ethernet4 (already bound to this ACL)
	d.configDB.NewtronIntent["interface|Ethernet4|acl|ingress"] = map[string]string{
		"operation": "bind-acl",
		"acl_name":  "EDGE_IN",
		"direction": "ingress",
		"state":     "actuated",
	}
	ctx := context.Background()

	cs, err := intf.BindACL(ctx, "EDGE_IN", "ingress")
	if err != nil {
		t.Fatalf("BindACL: %v", err)
	}

	c := assertChange(t, cs, "ACL_TABLE", "EDGE_IN", ChangeModify)
	assertField(t, c, "ports", "Ethernet0,Ethernet4")
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
	// IPAddresses reads from intent DB (Phase 2: intent-based reads).
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"ip":        "10.1.0.0/31",
	}
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
	intf.node.actuatedIntent = true // online mode: connected checks are enforced
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
	// Make Ethernet0 a PortChannel member (via intent DB — Phase 2: intent-based reads).
	d.configDB.NewtronIntent["portchannel|PortChannel100|Ethernet0"] = map[string]string{
		"operation": "add-portchannel-member",
		"state":     "actuated",
		"name":      "Ethernet0",
	}
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
	d.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{"service_name": "EXISTING_SERVICE", "state": "actuated", "operation": "apply-service", "name": "EXISTING_SERVICE"}
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

// TestApplyService_RoutingRejectedOnL2 pins the routing-is-L3-only
// precondition: a bridged service carrying a routing block is rejected
// (the server back-stop behind the schema applies_when). A VLAN is
// supplied so the per-type "bridged requires a VLAN" check passes first
// — the routing guard fires immediately after the type switch.
func TestApplyService_RoutingRejectedOnL2(t *testing.T) {
	d, intf := testInterface()
	d.SpecProvider.(*testSpecProvider).services["L2_WITH_ROUTING"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeBridged,
		Routing:     &spec.RoutingSpec{Protocol: spec.RoutingProtocolBGP},
	}
	ctx := context.Background()

	_, err := intf.ApplyService(ctx, "L2_WITH_ROUTING", ApplyServiceOpts{VLAN: 100})
	if err == nil {
		t.Fatal("expected error: routing block on an L2 (bridged) service")
	}
	if got := err.Error(); !strings.Contains(got, "L2-only") || !strings.Contains(got, "routing") {
		t.Errorf("error = %q; want it to name L2-only + routing", got)
	}
}

// TestApplyService_RoutingAllowedOnL3 is the positive counterpart: a
// routed service with a routing block passes the L3-only precondition
// (it fails later for an unrelated reason — no device/BGP set up in the
// fixture — but NOT with the L2-only routing rejection).
func TestApplyService_RoutingAllowedOnL3(t *testing.T) {
	d, intf := testInterface()
	d.SpecProvider.(*testSpecProvider).services["L3_WITH_ROUTING"] = &spec.ServiceSpec{
		ServiceType: spec.ServiceTypeRouted,
		Routing:     &spec.RoutingSpec{Protocol: spec.RoutingProtocolBGP},
	}
	ctx := context.Background()

	_, err := intf.ApplyService(ctx, "L3_WITH_ROUTING", ApplyServiceOpts{IPAddress: "10.0.0.1/30"})
	if err != nil && strings.Contains(err.Error(), "L2-only") {
		t.Errorf("routed service wrongly rejected by the L2-only guard: %v", err)
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
	// (AddBGPPeer uses buildChangeSet without render — correct for connected
	// nodes where ChangeSets are applied to Redis separately.)
	d, intf := testInterface()
	ctx := context.Background()

	// IPAddresses reads from intent DB (Phase 2: intent-based reads).
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"ip":        "10.1.0.0/31",
	}
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

// ============================================================================
// UpdateBGPPeer (#227) — atomic per-peer mutation
// ============================================================================

// bgpPeerSetup builds a node with an existing BGP peer on Ethernet0.
func bgpPeerSetup(t *testing.T) (*Node, *Interface) {
	t.Helper()
	d, intf := testInterface()
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"ip":        "10.1.0.0/31",
	}
	d.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}
	ctx := context.Background()
	if _, err := intf.AddBGPPeer(ctx, DirectBGPPeerConfig{
		RemoteAS: 65002, Description: "old-peer",
	}); err != nil {
		t.Fatalf("seed AddBGPPeer: %v", err)
	}
	// Simulate apply so subsequent reads see the BGP_NEIGHBOR row.
	d.configDB.BGPNeighbor["default|10.1.0.1"] = sonic.BGPNeighborEntry{ASN: "65002", LocalAddr: "10.1.0.0"}
	return d, intf
}

func TestUpdateBGPPeer_InPlaceASChange(t *testing.T) {
	d, intf := bgpPeerSetup(t)
	ctx := context.Background()

	cs, err := intf.UpdateBGPPeer(ctx, DirectBGPPeerConfig{
		RemoteAS: 65099, Description: "new-peer",
	})
	if err != nil {
		t.Fatalf("UpdateBGPPeer: %v", err)
	}

	// CONFIG_DB: BGP_NEIGHBOR row replaced in place (§48) — a single Replace,
	// never a DELete of the key, so frrcfgd does not tear the session down.
	assertNoChangeOfType(t, cs, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeDelete)
	c := assertChange(t, cs, "BGP_NEIGHBOR", "default|10.1.0.1", ChangeReplace)
	assertField(t, c, "asn", "65099")

	intent := d.GetIntent("interface|Ethernet0|bgp-peer")
	if intent == nil {
		t.Fatal("intent record missing after update")
	}
	if got := intent.Params[sonic.FieldRemoteAS]; got != "65099" {
		t.Errorf("intent remote_as = %q, want 65099", got)
	}
	if got := intent.Params[sonic.FieldNeighborIP]; got != "10.1.0.1" {
		t.Errorf("intent neighbor_ip = %q (should be unchanged)", got)
	}
}

func TestUpdateBGPPeer_NoExistingPeer(t *testing.T) {
	d, intf := testInterface()
	d.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"ip":        "10.1.0.0/31",
	}
	d.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}
	ctx := context.Background()

	_, err := intf.UpdateBGPPeer(ctx, DirectBGPPeerConfig{RemoteAS: 65002})
	if err == nil {
		t.Fatal("expected error when no BGP peer exists")
	}
}

func TestRoundTrip_BindUnbindACL(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Create the ACL first
	_, err := n.CreateACL(ctx, "EDGE_IN", ACLConfig{Type: "L3", Stage: "ingress"})
	if err != nil {
		t.Fatalf("CreateACL: %v", err)
	}

	// Create interface intent (parent for ACL binding) via AddBGPPeer.
	// IPAddresses reads from intent DB (Phase 2: intent-based reads).
	n.configDB.DeviceMetadata["localhost"] = map[string]string{"bgp_asn": "65001"}
	n.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "configure-interface",
		"state":     "actuated",
		"ip":        "10.1.0.0/31",
	}
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

// ============================================================================
// Trunk VLAN membership (#224) — per-VLAN intent records + RemoveTrunkVLAN
// ============================================================================

// trunkSetup creates an abstract node with two VLANs (100, 200) ready for
// trunk-membership tests. Saves repeating the boilerplate in every test.
func trunkSetup(t *testing.T) (*Node, *Interface) {
	t.Helper()
	n := newTestAbstractNode()
	ctx := context.Background()
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN 100: %v", err)
	}
	if _, err := n.CreateVLAN(ctx, 200, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN 200: %v", err)
	}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	return n, iface
}

func TestConfigureInterface_Trunk_WritesPerVLANRecord(t *testing.T) {
	n, iface := trunkSetup(t)
	ctx := context.Background()

	_, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true})
	if err != nil {
		t.Fatalf("ConfigureInterface VLAN 100 tagged: %v", err)
	}

	// Per-VLAN record exists; base interface record has NO vlan_id stuffed.
	trunkIntent := n.GetIntent("interface|Ethernet0|trunk-vlan|100")
	if trunkIntent == nil {
		t.Fatal("expected interface|Ethernet0|trunk-vlan|100 intent")
	}
	if trunkIntent.Operation != "add-trunk-vlan" {
		t.Errorf("trunk intent op = %q, want add-trunk-vlan", trunkIntent.Operation)
	}
	if trunkIntent.Params["vlan_id"] != "100" {
		t.Errorf("trunk intent vlan_id = %q, want 100", trunkIntent.Params["vlan_id"])
	}
	baseIntent := n.GetIntent("interface|Ethernet0")
	if baseIntent != nil && baseIntent.Params["vlan_id"] != "" {
		t.Errorf("base interface record should not carry vlan_id for trunk; got %q", baseIntent.Params["vlan_id"])
	}
}

func TestConfigureInterface_Trunk_Accumulates(t *testing.T) {
	n, iface := trunkSetup(t)
	ctx := context.Background()

	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("trunk add 100: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err != nil {
		t.Fatalf("trunk add 200: %v", err)
	}

	// Both records present — adding the second VLAN does not clobber the first.
	if n.GetIntent("interface|Ethernet0|trunk-vlan|100") == nil {
		t.Error("trunk-vlan|100 should still exist after adding 200")
	}
	if n.GetIntent("interface|Ethernet0|trunk-vlan|200") == nil {
		t.Error("trunk-vlan|200 should exist after second add")
	}
}

func TestConfigureInterface_Trunk_Idempotent(t *testing.T) {
	_, iface := trunkSetup(t)
	ctx := context.Background()

	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("trunk add 100 (first): %v", err)
	}
	cs, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true})
	if err != nil {
		t.Fatalf("trunk add 100 (second, expected idempotent): %v", err)
	}
	// Second call should produce no CONFIG_DB changes (the record already exists).
	for _, c := range cs.Changes {
		if c.Table == "VLAN_MEMBER" {
			t.Errorf("idempotent re-add should not write VLAN_MEMBER; got change %+v", c)
		}
	}
}

func TestRemoveTrunkVLAN_StripsOneLeavesOthers(t *testing.T) {
	n, iface := trunkSetup(t)
	ctx := context.Background()

	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("trunk add 100: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err != nil {
		t.Fatalf("trunk add 200: %v", err)
	}

	cs, err := iface.RemoveTrunkVLAN(ctx, 100)
	if err != nil {
		t.Fatalf("RemoveTrunkVLAN 100: %v", err)
	}

	if n.GetIntent("interface|Ethernet0|trunk-vlan|100") != nil {
		t.Error("trunk-vlan|100 should be deleted")
	}
	if n.GetIntent("interface|Ethernet0|trunk-vlan|200") == nil {
		t.Error("trunk-vlan|200 should survive (reference-aware strip)")
	}
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "NEWTRON_INTENT", "interface|Ethernet0|trunk-vlan|100", ChangeDelete)
}

func TestRemoveTrunkVLAN_NotAMember(t *testing.T) {
	_, iface := trunkSetup(t)
	ctx := context.Background()

	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("trunk add 100: %v", err)
	}

	_, err := iface.RemoveTrunkVLAN(ctx, 200)
	if err == nil {
		t.Fatal("expected error removing VLAN 200 that was never added")
	}
}

func TestUnconfigureInterface_ClearsAllTrunkChildren(t *testing.T) {
	n, iface := trunkSetup(t)
	ctx := context.Background()

	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("trunk add 100: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err != nil {
		t.Fatalf("trunk add 200: %v", err)
	}

	cs, err := iface.UnconfigureInterface(ctx)
	if err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}

	if n.GetIntent("interface|Ethernet0|trunk-vlan|100") != nil {
		t.Error("trunk-vlan|100 should be cleared by unconfigure-interface")
	}
	if n.GetIntent("interface|Ethernet0|trunk-vlan|200") != nil {
		t.Error("trunk-vlan|200 should be cleared by unconfigure-interface")
	}
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "VLAN_MEMBER", "Vlan200|Ethernet0", ChangeDelete)
}

func TestRoundTrip_AddRemoveTrunkVLAN(t *testing.T) {
	n, iface := trunkSetup(t)
	ctx := context.Background()

	cs1, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true})
	if err != nil {
		t.Fatalf("ConfigureInterface trunk: %v", err)
	}
	assertChange(t, cs1, "NEWTRON_INTENT", "interface|Ethernet0|trunk-vlan|100", ChangeAdd)
	assertChange(t, cs1, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeAdd)

	cs2, err := iface.RemoveTrunkVLAN(ctx, 100)
	if err != nil {
		t.Fatalf("RemoveTrunkVLAN: %v", err)
	}
	assertChange(t, cs2, "NEWTRON_INTENT", "interface|Ethernet0|trunk-vlan|100", ChangeDelete)
	assertChange(t, cs2, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)

	if n.GetIntent("interface|Ethernet0|trunk-vlan|100") != nil {
		t.Error("trunk-vlan intent should be cleared after RemoveTrunkVLAN")
	}
}

// ============================================================================
// ConfigureInterface within-mode field diff (#228) — no orphan IP subentries
// when the same VRF is kept but the IP changes or is dropped.
// ============================================================================

// routedSetup creates a node with VRF "Vrf_CUST1" and Ethernet0 already
// configured as routed {vrf:Vrf_CUST1, ip:10.0.0.1/31}. Saves the
// boilerplate for the within-mode-diff tests.
func routedSetup(t *testing.T) (*Node, *Interface) {
	t.Helper()
	n := newTestAbstractNode()
	ctx := context.Background()
	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_CUST1", IP: "10.0.0.1/31"}); err != nil {
		t.Fatalf("ConfigureInterface (prerequisite): %v", err)
	}
	return n, iface
}

func TestConfigureInterface_RoutedIPSwap_NoOrphan(t *testing.T) {
	n, iface := routedSetup(t)
	ctx := context.Background()

	// Within-mode swap: same VRF, change IP from 10.0.0.1/31 → 10.0.0.2/31.
	cs, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_CUST1", IP: "10.0.0.2/31"})
	if err != nil {
		t.Fatalf("ConfigureInterface (within-mode swap): %v", err)
	}

	// The old IP subentry must be deleted in this ChangeSet.
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.0.0.1/31", ChangeDelete)
	// And the new IP subentry must be added.
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.0.0.2/31", ChangeAdd)

	// Sanity: intent record reflects the new IP only.
	intent := n.GetIntent("interface|Ethernet0")
	if intent == nil {
		t.Fatal("interface intent missing")
	}
	if got := intent.Params["ip"]; got != "10.0.0.2/31" {
		t.Errorf("intent ip = %q, want 10.0.0.2/31", got)
	}
}

func TestConfigureInterface_RoutedIPDrop_NoOrphan(t *testing.T) {
	n, iface := routedSetup(t)
	ctx := context.Background()

	// Within-mode drop: keep VRF, omit IP. The previous IP must not orphan.
	cs, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_CUST1"})
	if err != nil {
		t.Fatalf("ConfigureInterface (IP drop): %v", err)
	}

	assertChange(t, cs, "INTERFACE", "Ethernet0|10.0.0.1/31", ChangeDelete)

	intent := n.GetIntent("interface|Ethernet0")
	if intent == nil {
		t.Fatal("interface intent missing")
	}
	if got := intent.Params["ip"]; got != "" {
		t.Errorf("intent should have no ip after drop; got %q", got)
	}
}

func TestConfigureInterface_RoutedIPAdd_NoSpuriousDelete(t *testing.T) {
	n := newTestAbstractNode()
	ctx := context.Background()

	if _, err := n.CreateVRF(ctx, "Vrf_CUST1", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	// First call binds VRF only (no IP).
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_CUST1"}); err != nil {
		t.Fatalf("ConfigureInterface VRF-only: %v", err)
	}

	// Second call adds an IP. The diff pass must not synthesize a phantom
	// delete (there was no prior IP).
	cs, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_CUST1", IP: "10.0.0.1/31"})
	if err != nil {
		t.Fatalf("ConfigureInterface (add IP): %v", err)
	}
	assertChange(t, cs, "INTERFACE", "Ethernet0|10.0.0.1/31", ChangeAdd)
	for _, c := range cs.Changes {
		if c.Table == "INTERFACE" && c.Type == ChangeDelete {
			t.Errorf("unexpected delete on pure IP add: %+v", c)
		}
	}
}
