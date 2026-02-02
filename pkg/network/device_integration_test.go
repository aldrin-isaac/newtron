//go:build integration

package network_test

import (
	"sort"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
)

// Seed data summary (from configdb.json):
//
//   PORT: Ethernet0..Ethernet7  (8 ports)
//   PORTCHANNEL: PortChannel100
//   VLAN: Vlan100 ("Servers"), Vlan200 ("Storage")
//   VRF: Vrf_CUST1 (vni=10001)
//   BGP_NEIGHBOR: 10.0.0.1, 10.0.0.2
//   PORTCHANNEL_MEMBER: PortChannel100|Ethernet4, PortChannel100|Ethernet5

// ---------------------------------------------------------------------------
// TestConnectDevice connects via the Network object and verifies connectivity.
// ---------------------------------------------------------------------------
func TestConnectDevice(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	if !dev.IsConnected() {
		t.Error("device should be connected")
	}
}

// ---------------------------------------------------------------------------
// TestDisconnectDevice verifies disconnect works.
// ---------------------------------------------------------------------------
func TestDisconnectDevice(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	if err := dev.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error: %v", err)
	}
	if dev.IsConnected() {
		t.Error("device should not be connected after disconnect")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceProperties checks resolved profile properties.
// ---------------------------------------------------------------------------
func TestDeviceProperties(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	if dev.Name() != "test-leaf1" {
		t.Errorf("Name() = %q, want %q", dev.Name(), "test-leaf1")
	}

	// MgmtIP is the Redis container IP (patched by TestNetwork)
	if dev.MgmtIP() == "" {
		t.Error("MgmtIP() should not be empty")
	}

	if dev.LoopbackIP() != "10.0.0.10" {
		t.Errorf("LoopbackIP() = %q, want %q", dev.LoopbackIP(), "10.0.0.10")
	}

	if dev.ASNumber() != 13908 {
		t.Errorf("ASNumber() = %d, want %d", dev.ASNumber(), 13908)
	}

	if dev.RouterID() != "10.0.0.10" {
		t.Errorf("RouterID() = %q, want %q", dev.RouterID(), "10.0.0.10")
	}

	if dev.Region() == "" {
		t.Error("Region() should not be empty")
	}

	if dev.Site() == "" {
		t.Error("Site() should not be empty")
	}
}

// ---------------------------------------------------------------------------
// TestListInterfaces lists all interfaces (8 ports + 1 PortChannel = 9).
// ---------------------------------------------------------------------------
func TestListInterfaces(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	names := dev.ListInterfaces()

	// 8 physical ports + 1 PortChannel
	if len(names) != 9 {
		t.Errorf("ListInterfaces() count = %d, want 9; got %v", len(names), names)
	}

	// Verify Ethernet0 and PortChannel100 are present
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["Ethernet0"] {
		t.Error("Ethernet0 should be in interface list")
	}
	if !nameSet["PortChannel100"] {
		t.Error("PortChannel100 should be in interface list")
	}
}

// ---------------------------------------------------------------------------
// TestGetInterface retrieves Ethernet0 and verifies its properties.
// ---------------------------------------------------------------------------
func TestGetInterface(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	intf, err := dev.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface(Ethernet0) error: %v", err)
	}
	if intf.Name() != "Ethernet0" {
		t.Errorf("Name() = %q, want %q", intf.Name(), "Ethernet0")
	}
	if intf.AdminStatus() != "up" {
		t.Errorf("AdminStatus() = %q, want %q", intf.AdminStatus(), "up")
	}
}

// ---------------------------------------------------------------------------
// TestGetInterfaceNotFound verifies error on a non-existent interface.
// ---------------------------------------------------------------------------
func TestGetInterfaceNotFound(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	_, err := dev.GetInterface("Ethernet99")
	if err == nil {
		t.Fatal("GetInterface(Ethernet99) should return error")
	}
}

// ---------------------------------------------------------------------------
// TestGetVLAN retrieves VLAN 100 and checks ID, description, and ports.
// ---------------------------------------------------------------------------
func TestGetVLAN(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	vlan, err := dev.GetVLAN(100)
	if err != nil {
		t.Fatalf("GetVLAN(100) error: %v", err)
	}
	if vlan.ID != 100 {
		t.Errorf("ID = %d, want 100", vlan.ID)
	}
	if vlan.Name != "Servers" {
		t.Errorf("Name = %q, want %q", vlan.Name, "Servers")
	}
	// Vlan100 has members: Ethernet2 (untagged), Ethernet3 (tagged)
	if len(vlan.Ports) != 2 {
		t.Errorf("Ports count = %d, want 2; got %v", len(vlan.Ports), vlan.Ports)
	}
}

// ---------------------------------------------------------------------------
// TestGetVLANNotFound verifies error for a non-existent VLAN.
// ---------------------------------------------------------------------------
func TestGetVLANNotFound(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	_, err := dev.GetVLAN(999)
	if err == nil {
		t.Fatal("GetVLAN(999) should return error")
	}
}

// ---------------------------------------------------------------------------
// TestGetVRF retrieves Vrf_CUST1 and checks name and L3VNI.
// ---------------------------------------------------------------------------
func TestGetVRF(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	vrf, err := dev.GetVRF("Vrf_CUST1")
	if err != nil {
		t.Fatalf("GetVRF(Vrf_CUST1) error: %v", err)
	}
	if vrf.Name != "Vrf_CUST1" {
		t.Errorf("Name = %q, want %q", vrf.Name, "Vrf_CUST1")
	}
	if vrf.L3VNI != 10001 {
		t.Errorf("L3VNI = %d, want 10001", vrf.L3VNI)
	}
}

// ---------------------------------------------------------------------------
// TestGetPortChannel retrieves PortChannel100, checks members.
// ---------------------------------------------------------------------------
func TestGetPortChannel(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	pc, err := dev.GetPortChannel("PortChannel100")
	if err != nil {
		t.Fatalf("GetPortChannel(PortChannel100) error: %v", err)
	}
	if pc.Name != "PortChannel100" {
		t.Errorf("Name = %q, want %q", pc.Name, "PortChannel100")
	}

	// Members: Ethernet4, Ethernet5
	sort.Strings(pc.Members)
	if len(pc.Members) != 2 {
		t.Fatalf("Members count = %d, want 2; got %v", len(pc.Members), pc.Members)
	}
	if pc.Members[0] != "Ethernet4" || pc.Members[1] != "Ethernet5" {
		t.Errorf("Members = %v, want [Ethernet4, Ethernet5]", pc.Members)
	}
}

// ---------------------------------------------------------------------------
// TestListVLANs returns the two configured VLANs: 100 and 200.
// ---------------------------------------------------------------------------
func TestListVLANs(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	ids := dev.ListVLANs()
	sort.Ints(ids)

	if len(ids) != 2 {
		t.Fatalf("ListVLANs() count = %d, want 2; got %v", len(ids), ids)
	}
	if ids[0] != 100 || ids[1] != 200 {
		t.Errorf("ListVLANs() = %v, want [100, 200]", ids)
	}
}

// ---------------------------------------------------------------------------
// TestListVRFs returns the single VRF: Vrf_CUST1.
// ---------------------------------------------------------------------------
func TestListVRFs(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	names := dev.ListVRFs()

	if len(names) != 1 {
		t.Fatalf("ListVRFs() count = %d, want 1; got %v", len(names), names)
	}
	if names[0] != "Vrf_CUST1" {
		t.Errorf("ListVRFs() = %v, want [Vrf_CUST1]", names)
	}
}

// ---------------------------------------------------------------------------
// TestListPortChannels returns the single PortChannel: PortChannel100.
// ---------------------------------------------------------------------------
func TestListPortChannels(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	names := dev.ListPortChannels()

	if len(names) != 1 {
		t.Fatalf("ListPortChannels() count = %d, want 1; got %v", len(names), names)
	}
	if names[0] != "PortChannel100" {
		t.Errorf("ListPortChannels() = %v, want [PortChannel100]", names)
	}
}

// ---------------------------------------------------------------------------
// TestListBGPNeighbors returns the two BGP neighbor IPs.
// ---------------------------------------------------------------------------
func TestListBGPNeighbors(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	neighbors := dev.ListBGPNeighbors()
	sort.Strings(neighbors)

	if len(neighbors) != 2 {
		t.Fatalf("ListBGPNeighbors() count = %d, want 2; got %v", len(neighbors), neighbors)
	}
	if neighbors[0] != "10.0.0.1" || neighbors[1] != "10.0.0.2" {
		t.Errorf("ListBGPNeighbors() = %v, want [10.0.0.1, 10.0.0.2]", neighbors)
	}
}
