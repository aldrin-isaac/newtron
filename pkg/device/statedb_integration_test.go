//go:build integration

package device_test

import (
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
)

func TestStateDBPortTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if d.StateDB == nil {
		t.Fatal("StateDB is nil")
	}

	if len(d.StateDB.PortTable) != 8 {
		t.Fatalf("expected 8 ports in PortTable, got %d", len(d.StateDB.PortTable))
	}

	expectedPorts := []string{
		"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3",
		"Ethernet4", "Ethernet5", "Ethernet6", "Ethernet7",
	}
	for _, name := range expectedPorts {
		port, ok := d.StateDB.PortTable[name]
		if !ok {
			t.Errorf("port %s not found in PortTable", name)
			continue
		}
		if port.Speed != "40000" {
			t.Errorf("port %s speed = %q, want %q", name, port.Speed, "40000")
		}
		if port.MTU != "9100" {
			t.Errorf("port %s mtu = %q, want %q", name, port.MTU, "9100")
		}
		if port.AdminStatus == "" {
			t.Errorf("port %s admin_status is empty", name)
		}
		if port.OperStatus == "" {
			t.Errorf("port %s oper_status is empty", name)
		}
	}
}

func TestStateDBPortOperStatus(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// First 6 ports should be up
	upPorts := []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3", "Ethernet4", "Ethernet5"}
	for _, name := range upPorts {
		port, ok := d.StateDB.PortTable[name]
		if !ok {
			t.Errorf("port %s not found", name)
			continue
		}
		if port.OperStatus != "up" {
			t.Errorf("port %s oper_status = %q, want %q", name, port.OperStatus, "up")
		}
		if port.AdminStatus != "up" {
			t.Errorf("port %s admin_status = %q, want %q", name, port.AdminStatus, "up")
		}
	}

	// Last 2 ports should be down
	downPorts := []string{"Ethernet6", "Ethernet7"}
	for _, name := range downPorts {
		port, ok := d.StateDB.PortTable[name]
		if !ok {
			t.Errorf("port %s not found", name)
			continue
		}
		if port.OperStatus != "down" {
			t.Errorf("port %s oper_status = %q, want %q", name, port.OperStatus, "down")
		}
		if port.AdminStatus != "down" {
			t.Errorf("port %s admin_status = %q, want %q", name, port.AdminStatus, "down")
		}
	}
}

func TestStateDBLAGTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.StateDB.LAGTable) != 1 {
		t.Fatalf("expected 1 LAG in LAGTable, got %d", len(d.StateDB.LAGTable))
	}

	lag, ok := d.StateDB.LAGTable["PortChannel100"]
	if !ok {
		t.Fatal("PortChannel100 not found in LAGTable")
	}
	if lag.OperStatus != "up" {
		t.Errorf("PortChannel100 oper_status = %q, want %q", lag.OperStatus, "up")
	}
	if lag.Speed != "80000" {
		t.Errorf("PortChannel100 speed = %q, want %q", lag.Speed, "80000")
	}
	if lag.MTU != "9100" {
		t.Errorf("PortChannel100 mtu = %q, want %q", lag.MTU, "9100")
	}
}

func TestStateDBLAGMemberTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.StateDB.LAGMemberTable) != 2 {
		t.Fatalf("expected 2 LAG members, got %d", len(d.StateDB.LAGMemberTable))
	}

	members := []string{
		"PortChannel100|Ethernet4",
		"PortChannel100|Ethernet5",
	}
	for _, key := range members {
		member, ok := d.StateDB.LAGMemberTable[key]
		if !ok {
			t.Errorf("LAG member %s not found", key)
			continue
		}
		if member.OperStatus != "up" {
			t.Errorf("LAG member %s oper_status = %q, want %q", key, member.OperStatus, "up")
		}
		if member.CollectingDist != "true" {
			t.Errorf("LAG member %s collecting_distributing = %q, want %q", key, member.CollectingDist, "true")
		}
		if member.Selected != "true" {
			t.Errorf("LAG member %s selected = %q, want %q", key, member.Selected, "true")
		}
	}
}

func TestStateDBBGPNeighborTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.StateDB.BGPNeighborTable) != 2 {
		t.Fatalf("expected 2 BGP neighbors in StateDB, got %d", len(d.StateDB.BGPNeighborTable))
	}

	// BGP neighbor keys are "default|ip"
	neighbors := []string{"default|10.0.0.1", "default|10.0.0.2"}
	for _, key := range neighbors {
		neighbor, ok := d.StateDB.BGPNeighborTable[key]
		if !ok {
			t.Errorf("BGP neighbor %s not found in StateDB", key)
			continue
		}
		if neighbor.State != "Established" {
			t.Errorf("BGP neighbor %s state = %q, want %q", key, neighbor.State, "Established")
		}
		if neighbor.RemoteAS != "13908" {
			t.Errorf("BGP neighbor %s remote_asn = %q, want %q", key, neighbor.RemoteAS, "13908")
		}
		if neighbor.LocalAS != "13908" {
			t.Errorf("BGP neighbor %s local_asn = %q, want %q", key, neighbor.LocalAS, "13908")
		}
	}

	// Verify specific fields for first neighbor
	n1 := d.StateDB.BGPNeighborTable["default|10.0.0.1"]
	if n1.PfxRcvd != "150" {
		t.Errorf("neighbor 10.0.0.1 prefixes_received = %q, want %q", n1.PfxRcvd, "150")
	}
	if n1.PfxSent != "100" {
		t.Errorf("neighbor 10.0.0.1 prefixes_sent = %q, want %q", n1.PfxSent, "100")
	}
	if n1.Uptime != "1d2h30m" {
		t.Errorf("neighbor 10.0.0.1 uptime = %q, want %q", n1.Uptime, "1d2h30m")
	}
}

func TestStateDBVXLANTunnelTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.StateDB.VXLANTunnelTable) != 1 {
		t.Fatalf("expected 1 VXLAN tunnel in StateDB, got %d", len(d.StateDB.VXLANTunnelTable))
	}

	vtep, ok := d.StateDB.VXLANTunnelTable["vtep1"]
	if !ok {
		t.Fatal("vtep1 not found in VXLANTunnelTable")
	}
	if vtep.SrcIP != "10.0.0.10" {
		t.Errorf("vtep1 src_ip = %q, want %q", vtep.SrcIP, "10.0.0.10")
	}
	if vtep.OperStatus != "up" {
		t.Errorf("vtep1 operstatus = %q, want %q", vtep.OperStatus, "up")
	}
}

func TestStateDBEmptyTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// VLAN_TABLE and VRF_TABLE have no entries in the seed data
	if len(d.StateDB.VLANTable) != 0 {
		t.Errorf("expected VLANTable to be empty, got %d entries", len(d.StateDB.VLANTable))
	}
	if len(d.StateDB.VRFTable) != 0 {
		t.Errorf("expected VRFTable to be empty, got %d entries", len(d.StateDB.VRFTable))
	}
	if len(d.StateDB.NeighTable) != 0 {
		t.Errorf("expected NeighTable to be empty, got %d entries", len(d.StateDB.NeighTable))
	}
	if len(d.StateDB.FDBTable) != 0 {
		t.Errorf("expected FDBTable to be empty, got %d entries", len(d.StateDB.FDBTable))
	}
}

func TestStateDBReload(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Verify initial state of Ethernet0
	port, ok := d.StateDB.PortTable["Ethernet0"]
	if !ok {
		t.Fatal("Ethernet0 not found in PortTable")
	}
	if port.OperStatus != "up" {
		t.Fatalf("Ethernet0 initial oper_status = %q, want %q", port.OperStatus, "up")
	}

	// Modify Ethernet0 oper_status directly in Redis (simulating external state change)
	addr := testutil.RedisAddr()
	testutil.WriteSingleEntry(t, addr, 6, "PORT_TABLE", "Ethernet0", map[string]string{
		"admin_status": "up",
		"oper_status":  "down",
		"speed":        "40000",
		"mtu":          "9100",
	})

	// Device should still see old state
	if d.StateDB.PortTable["Ethernet0"].OperStatus != "up" {
		t.Error("oper_status should still be 'up' before RefreshState")
	}

	// Refresh state
	ctx := testutil.Context(t)
	if err := d.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState failed: %v", err)
	}

	// Now it should see the updated state
	port, ok = d.StateDB.PortTable["Ethernet0"]
	if !ok {
		t.Fatal("Ethernet0 not found in PortTable after RefreshState")
	}
	if port.OperStatus != "down" {
		t.Errorf("Ethernet0 oper_status = %q after RefreshState, want %q", port.OperStatus, "down")
	}
}
