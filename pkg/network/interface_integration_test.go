//go:build integration

package network_test

import (
	"sort"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
)

// Seed data relevant to interfaces (from configdb.json + statedb.json):
//
//   Ethernet0: admin_status=up, speed=40000, mtu=9100, no VRF, no service
//   Ethernet1: admin_status=up, speed=40000, mtu=9100,
//              VRF=Vrf_CUST1, IP=10.1.1.1/30,
//              service_name=customer-l3, ipvpn=customer-vpn,
//              ingress_acl=customer-l3-in, egress_acl=customer-l3-out,
//              serviceIP=10.1.1.1/30, serviceVRF=Vrf_CUST1
//   Ethernet4: admin_status=up, LAG member of PortChannel100
//   Ethernet5: admin_status=up, LAG member of PortChannel100
//   Ethernet6: admin_status=down
//   Ethernet7: admin_status=down
//   PortChannel100: admin_status=up, members=[Ethernet4, Ethernet5]

// ---------------------------------------------------------------------------
// TestInterfaceProperties verifies basic properties on Ethernet0.
// ---------------------------------------------------------------------------
func TestInterfaceProperties(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet0) error: %v", err)
	}

	if intf.AdminStatus() != "up" {
		t.Errorf("AdminStatus() = %q, want %q", intf.AdminStatus(), "up")
	}

	if intf.Speed() != "40000" {
		t.Errorf("Speed() = %q, want %q", intf.Speed(), "40000")
	}

	if intf.MTU() != 9100 {
		t.Errorf("MTU() = %d, want 9100", intf.MTU())
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceVRFAndIP verifies Ethernet1 is bound to Vrf_CUST1 with an IP.
// ---------------------------------------------------------------------------
func TestInterfaceVRFAndIP(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet1")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet1) error: %v", err)
	}

	if intf.VRF() != "Vrf_CUST1" {
		t.Errorf("VRF() = %q, want %q", intf.VRF(), "Vrf_CUST1")
	}

	ips := intf.IPAddresses()
	if len(ips) == 0 {
		t.Fatal("IPAddresses() should not be empty")
	}

	// The seed has "Ethernet1|10.1.1.1/30" in the INTERFACE table
	found := false
	for _, ip := range ips {
		if ip == "10.1.1.1/30" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("IPAddresses() = %v, want to contain 10.1.1.1/30", ips)
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceServiceBinding verifies Ethernet1 has service "customer-l3".
// ---------------------------------------------------------------------------
func TestInterfaceServiceBinding(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet1")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet1) error: %v", err)
	}

	if !intf.HasService() {
		t.Fatal("HasService() should be true for Ethernet1")
	}
	if intf.ServiceName() != "customer-l3" {
		t.Errorf("ServiceName() = %q, want %q", intf.ServiceName(), "customer-l3")
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceServiceDetails checks the full service binding fields on Ethernet1.
// ---------------------------------------------------------------------------
func TestInterfaceServiceDetails(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet1")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet1) error: %v", err)
	}

	if intf.ServiceIP() != "10.1.1.1/30" {
		t.Errorf("ServiceIP() = %q, want %q", intf.ServiceIP(), "10.1.1.1/30")
	}
	if intf.ServiceVRF() != "Vrf_CUST1" {
		t.Errorf("ServiceVRF() = %q, want %q", intf.ServiceVRF(), "Vrf_CUST1")
	}
	if intf.ServiceIPVPN() != "customer-vpn" {
		t.Errorf("ServiceIPVPN() = %q, want %q", intf.ServiceIPVPN(), "customer-vpn")
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceLAGMembership verifies Ethernet4 is a member of PortChannel100.
// ---------------------------------------------------------------------------
func TestInterfaceLAGMembership(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet4")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet4) error: %v", err)
	}

	if !intf.IsLAGMember() {
		t.Fatal("Ethernet4 should be a LAG member")
	}
	if intf.LAGParent() != "PortChannel100" {
		t.Errorf("LAGParent() = %q, want %q", intf.LAGParent(), "PortChannel100")
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceACLBindings verifies Ethernet1 has both ingress and egress ACLs.
// ---------------------------------------------------------------------------
func TestInterfaceACLBindings(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet1")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet1) error: %v", err)
	}

	if intf.IngressACL() != "customer-l3-in" {
		t.Errorf("IngressACL() = %q, want %q", intf.IngressACL(), "customer-l3-in")
	}
	if intf.EgressACL() != "customer-l3-out" {
		t.Errorf("EgressACL() = %q, want %q", intf.EgressACL(), "customer-l3-out")
	}
}

// ---------------------------------------------------------------------------
// TestInterfaceTypeDetection verifies IsPhysical, IsPortChannel, IsVLAN,
// and IsLoopback for various interface types.
// ---------------------------------------------------------------------------
func TestInterfaceTypeDetection(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("physical", func(t *testing.T) {
		intf, err := dev.GetInterface("Ethernet0")
		if err != nil {
			t.Fatalf("GetInterface error: %v", err)
		}
		if !intf.IsPhysical() {
			t.Error("Ethernet0 should be physical")
		}
		if intf.IsPortChannel() {
			t.Error("Ethernet0 should not be PortChannel")
		}
		if intf.IsVLAN() {
			t.Error("Ethernet0 should not be VLAN")
		}
		if intf.IsLoopback() {
			t.Error("Ethernet0 should not be Loopback")
		}
	})

	t.Run("port_channel", func(t *testing.T) {
		intf, err := dev.GetInterface("PortChannel100")
		if err != nil {
			t.Fatalf("GetInterface error: %v", err)
		}
		if intf.IsPhysical() {
			t.Error("PortChannel100 should not be physical")
		}
		if !intf.IsPortChannel() {
			t.Error("PortChannel100 should be PortChannel")
		}
		if intf.IsVLAN() {
			t.Error("PortChannel100 should not be VLAN")
		}
		if intf.IsLoopback() {
			t.Error("PortChannel100 should not be Loopback")
		}
	})
}

// ---------------------------------------------------------------------------
// TestPortChannelMembers verifies PortChannel100 LAGMembers returns
// Ethernet4 and Ethernet5.
// ---------------------------------------------------------------------------
func TestPortChannelMembers(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("PortChannel100")
	if err != nil {
		t.Fatalf("GetInterface(PortChannel100) error: %v", err)
	}

	if !intf.IsPortChannel() {
		t.Fatal("PortChannel100 should be a PortChannel")
	}

	members := intf.LAGMembers()
	sort.Strings(members)

	if len(members) != 2 {
		t.Fatalf("LAGMembers() count = %d, want 2; got %v", len(members), members)
	}
	if members[0] != "Ethernet4" || members[1] != "Ethernet5" {
		t.Errorf("LAGMembers() = %v, want [Ethernet4, Ethernet5]", members)
	}
}
