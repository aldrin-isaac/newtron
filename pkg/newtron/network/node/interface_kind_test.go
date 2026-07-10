package node

import (
	"context"
	"testing"
)

func TestInterfaceKindOf(t *testing.T) {
	tests := []struct {
		name string
		kind InterfaceKind
	}{
		{"Ethernet0", KindEthernet},
		{"Ethernet124", KindEthernet},
		{"PortChannel1", KindPortChannel},
		{"Vlan100", KindIRB},
		{"Loopback0", KindLoopback},
		{"eth0", KindUnknown},
		{"", KindUnknown},
	}
	for _, tt := range tests {
		if got := interfaceKindOf(tt.name); got != tt.kind {
			t.Errorf("interfaceKindOf(%q) = %v, want %v", tt.name, got, tt.kind)
		}
	}
}

// TestKindCapabilityMatrix pins every cell of the capability matrix. A cell
// change is a deliberate design decision (with its api.md justification),
// never a side effect — this test makes a silent flip impossible.
func TestKindCapabilityMatrix(t *testing.T) {
	allCapabilities := []InterfaceCapability{
		CapabilityRouting, CapabilityVLANMembership, CapabilityACLBinding,
		CapabilityQoSBinding, CapabilityBGPPeering, CapabilityPortProperties,
	}
	want := map[InterfaceKind]map[InterfaceCapability]bool{
		KindEthernet: {
			CapabilityRouting: true, CapabilityVLANMembership: true,
			CapabilityACLBinding: true, CapabilityQoSBinding: true,
			CapabilityBGPPeering: true, CapabilityPortProperties: true,
		},
		KindPortChannel: {
			CapabilityRouting: true, CapabilityVLANMembership: true,
			CapabilityACLBinding: true,
			// QoS ✗ — PORT_QOS_MAP ifname is "global"|leafref-to-PORT
			// (sonic-port-qos-map.yang); LAG QoS is per-member in SONiC.
			CapabilityQoSBinding: false,
			CapabilityBGPPeering: true, CapabilityPortProperties: true,
		},
		KindIRB: {
			// Routing is the IRB's nature; authored via configure-irb.
			CapabilityRouting:        true,
			CapabilityVLANMembership: false, // an SVI IS the VLAN's L3 face
			CapabilityACLBinding:     false, // sonic-acl.yang ports: PORT ∪ PORTCHANNEL only
			CapabilityQoSBinding:     false,
			CapabilityBGPPeering:     true,
			CapabilityPortProperties: false, // no PORT/PORTCHANNEL row
		},
		KindLoopback: {}, // baseline-owned; no interface-op capabilities
		KindUnknown:  {},
	}
	for kind, caps := range want {
		for _, c := range allCapabilities {
			if got := kind.HasCapability(c); got != caps[c] {
				t.Errorf("%v.HasCapability(%v) = %v, want %v", kind, c, got, caps[c])
			}
		}
	}
}

// TestAuthoringOwnerRedirect pins the one authoring split: the IRB provides
// routing but configure-irb (vlan noun) authors it — refusal messages must
// redirect, not deny the capability.
func TestAuthoringOwnerRedirect(t *testing.T) {
	if owner := authoringOwner(KindIRB, CapabilityRouting); owner != "configure-irb (vlan noun)" {
		t.Errorf("authoringOwner(KindIRB, CapabilityRouting) = %q, want the configure-irb redirect", owner)
	}
	if owner := authoringOwner(KindEthernet, CapabilityRouting); owner != "" {
		t.Errorf("authoringOwner(KindEthernet, CapabilityRouting) = %q, want \"\" (interface ops own it)", owner)
	}
}

func TestPropertyApplicability(t *testing.T) {
	tests := []struct {
		property string
		kind     InterfaceKind
		ok       bool
	}{
		{"mtu", KindEthernet, true},
		{"mtu", KindPortChannel, true},
		{"admin_status", KindPortChannel, true},
		{"admin-status", KindPortChannel, true},
		{"speed", KindEthernet, true},
		{"speed", KindPortChannel, false}, // PORTCHANNEL row has no speed (sonic-portchannel.yang)
		{"description", KindPortChannel, false},
		{"mtu", KindIRB, false},
		{"speed", KindIRB, false},
	}
	for _, tt := range tests {
		if got := propertyAppliesTo(tt.property, tt.kind); got != tt.ok {
			t.Errorf("propertyAppliesTo(%q, %v) = %v, want %v", tt.property, tt.kind, got, tt.ok)
		}
	}
}

// TestL3TablePerKind pins the delivery-side table split: INTERFACE keys are
// leafrefs to PORT only (sonic-interface.yang); LAG L3 lives in
// PORTCHANNEL_INTERFACE (sonic-portchannel.yang). A verbatim INTERFACE write
// for a LAG passes intfmgrd today but fails yang-strict config reload.
func TestL3TablePerKind(t *testing.T) {
	if got := l3Table("Ethernet0"); got != "INTERFACE" {
		t.Errorf("l3Table(Ethernet0) = %q, want INTERFACE", got)
	}
	if got := l3Table("PortChannel1"); got != "PORTCHANNEL_INTERFACE" {
		t.Errorf("l3Table(PortChannel1) = %q, want PORTCHANNEL_INTERFACE", got)
	}

	// The generators must follow the split end to end.
	for _, e := range bindVrfConfig("PortChannel1", "Vrf_X") {
		if e.Table != "PORTCHANNEL_INTERFACE" {
			t.Errorf("bindVrfConfig(PortChannel1) table = %q, want PORTCHANNEL_INTERFACE", e.Table)
		}
	}
	for _, e := range assignIpAddressConfig("PortChannel1", "10.30.0.0/31") {
		if e.Table != "PORTCHANNEL_INTERFACE" || e.Key != "PortChannel1|10.30.0.0/31" {
			t.Errorf("assignIpAddressConfig(PortChannel1) = %s|%s, want PORTCHANNEL_INTERFACE|PortChannel1|10.30.0.0/31", e.Table, e.Key)
		}
	}
	for _, e := range assignIpAddressConfig("Ethernet0", "10.1.0.0/31") {
		if e.Table != "INTERFACE" {
			t.Errorf("assignIpAddressConfig(Ethernet0) table = %q, want INTERFACE", e.Table)
		}
	}
	// Forward and reverse must target the same table (§15).
	for _, e := range deleteInterfaceIPConfig("PortChannel1", "10.30.0.0/31") {
		if e.Table != "PORTCHANNEL_INTERFACE" {
			t.Errorf("deleteInterfaceIPConfig(PortChannel1) table = %q, want PORTCHANNEL_INTERFACE", e.Table)
		}
	}
	if got := propertyTable("PortChannel1"); got != "PORTCHANNEL" {
		t.Errorf("propertyTable(PortChannel1) = %q, want PORTCHANNEL", got)
	}
	if got := propertyTable("Ethernet0"); got != "PORT" {
		t.Errorf("propertyTable(Ethernet0) = %q, want PORT", got)
	}
}

// TestSVIExistsAndIsListed pins §24 parity: an interface that exists is also
// listed. SVIs exist once their VLAN does — and must appear in
// ListInterfaces (they were operable-but-invisible before this change).
func TestSVIExistsAndIsListed(t *testing.T) {
	n, _ := testInterface()

	if n.InterfaceExists("Vlan100") {
		t.Fatal("Vlan100 exists before its VLAN — existence must be intent-derived")
	}

	if _, err := n.CreateVLAN(context.Background(), 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}

	if !n.InterfaceExists("Vlan100") {
		t.Fatal("Vlan100 must exist once VLAN 100 does")
	}
	listed := false
	for _, name := range n.ListInterfaces() {
		if name == "Vlan100" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("Vlan100 exists but is not listed — §24 write-reaches-read parity broken")
	}
}
