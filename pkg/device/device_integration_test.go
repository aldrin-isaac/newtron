//go:build integration

package device_test

import (
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/device"
)

func TestConnect(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	profile := testutil.TestProfile()
	d := device.NewDevice("test-leaf1", profile)

	ctx := testutil.Context(t)
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer d.Disconnect()

	if !d.IsConnected() {
		t.Error("expected IsConnected to be true after Connect")
	}
}

func TestDisconnect(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	profile := testutil.TestProfile()
	d := device.NewDevice("test-leaf1", profile)

	ctx := testutil.Context(t)
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := d.Disconnect(); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	if d.IsConnected() {
		t.Error("expected IsConnected to be false after Disconnect")
	}
}

func TestReconnect(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	profile := testutil.TestProfile()
	d := device.NewDevice("test-leaf1", profile)

	ctx := testutil.Context(t)
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("initial Connect failed: %v", err)
	}

	if err := d.Disconnect(); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	if d.IsConnected() {
		t.Error("expected IsConnected to be false after Disconnect")
	}

	if err := d.Connect(ctx); err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
	defer d.Disconnect()

	if !d.IsConnected() {
		t.Error("expected IsConnected to be true after reconnect")
	}
}

func TestConfigDBLoaded(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if d.ConfigDB == nil {
		t.Fatal("ConfigDB is nil after Connect")
	}
	if len(d.ConfigDB.Port) == 0 {
		t.Error("ConfigDB.Port is empty")
	}
	if len(d.ConfigDB.VLAN) == 0 {
		t.Error("ConfigDB.VLAN is empty")
	}
	if len(d.ConfigDB.VRF) == 0 {
		t.Error("ConfigDB.VRF is empty")
	}
	if len(d.ConfigDB.PortChannel) == 0 {
		t.Error("ConfigDB.PortChannel is empty")
	}
	if len(d.ConfigDB.BGPNeighbor) == 0 {
		t.Error("ConfigDB.BGPNeighbor is empty")
	}
	if len(d.ConfigDB.VXLANTunnel) == 0 {
		t.Error("ConfigDB.VXLANTunnel is empty")
	}
	if len(d.ConfigDB.ACLTable) == 0 {
		t.Error("ConfigDB.ACLTable is empty")
	}
	if len(d.ConfigDB.NewtronServiceBinding) == 0 {
		t.Error("ConfigDB.NewtronServiceBinding is empty")
	}
}

func TestPortTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.Port) != 8 {
		t.Fatalf("expected 8 ports, got %d", len(d.ConfigDB.Port))
	}

	expectedPorts := []string{
		"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3",
		"Ethernet4", "Ethernet5", "Ethernet6", "Ethernet7",
	}
	for _, name := range expectedPorts {
		port, ok := d.ConfigDB.Port[name]
		if !ok {
			t.Errorf("port %s not found", name)
			continue
		}
		if port.MTU != "9100" {
			t.Errorf("port %s MTU = %q, want %q", name, port.MTU, "9100")
		}
		if port.Speed != "40000" {
			t.Errorf("port %s Speed = %q, want %q", name, port.Speed, "40000")
		}
	}

	// Verify admin_status: first 6 up, last 2 down
	for _, name := range []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3", "Ethernet4", "Ethernet5"} {
		if d.ConfigDB.Port[name].AdminStatus != "up" {
			t.Errorf("port %s admin_status = %q, want %q", name, d.ConfigDB.Port[name].AdminStatus, "up")
		}
	}
	for _, name := range []string{"Ethernet6", "Ethernet7"} {
		if d.ConfigDB.Port[name].AdminStatus != "down" {
			t.Errorf("port %s admin_status = %q, want %q", name, d.ConfigDB.Port[name].AdminStatus, "down")
		}
	}
}

func TestPortChannelTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	pc, ok := d.ConfigDB.PortChannel["PortChannel100"]
	if !ok {
		t.Fatal("PortChannel100 not found in ConfigDB.PortChannel")
	}
	if pc.AdminStatus != "up" {
		t.Errorf("PortChannel100 admin_status = %q, want %q", pc.AdminStatus, "up")
	}
	if pc.MTU != "9100" {
		t.Errorf("PortChannel100 mtu = %q, want %q", pc.MTU, "9100")
	}
	if pc.MinLinks != "1" {
		t.Errorf("PortChannel100 min_links = %q, want %q", pc.MinLinks, "1")
	}
	if pc.Fallback != "true" {
		t.Errorf("PortChannel100 fallback = %q, want %q", pc.Fallback, "true")
	}
}

func TestPortChannelMemberTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.PortChannelMember) != 2 {
		t.Fatalf("expected 2 port channel members, got %d", len(d.ConfigDB.PortChannelMember))
	}

	if _, ok := d.ConfigDB.PortChannelMember["PortChannel100|Ethernet4"]; !ok {
		t.Error("PortChannel100|Ethernet4 not found")
	}
	if _, ok := d.ConfigDB.PortChannelMember["PortChannel100|Ethernet5"]; !ok {
		t.Error("PortChannel100|Ethernet5 not found")
	}
}

func TestVLANTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.VLAN) != 2 {
		t.Fatalf("expected 2 VLANs, got %d", len(d.ConfigDB.VLAN))
	}

	vlan100, ok := d.ConfigDB.VLAN["Vlan100"]
	if !ok {
		t.Fatal("Vlan100 not found")
	}
	if vlan100.VLANID != "100" {
		t.Errorf("Vlan100 vlanid = %q, want %q", vlan100.VLANID, "100")
	}
	if vlan100.Description != "Servers" {
		t.Errorf("Vlan100 description = %q, want %q", vlan100.Description, "Servers")
	}

	vlan200, ok := d.ConfigDB.VLAN["Vlan200"]
	if !ok {
		t.Fatal("Vlan200 not found")
	}
	if vlan200.VLANID != "200" {
		t.Errorf("Vlan200 vlanid = %q, want %q", vlan200.VLANID, "200")
	}
	if vlan200.Description != "Storage" {
		t.Errorf("Vlan200 description = %q, want %q", vlan200.Description, "Storage")
	}
}

func TestVLANMemberTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.VLANMember) != 3 {
		t.Fatalf("expected 3 VLAN members, got %d", len(d.ConfigDB.VLANMember))
	}

	m1, ok := d.ConfigDB.VLANMember["Vlan100|Ethernet2"]
	if !ok {
		t.Fatal("Vlan100|Ethernet2 not found")
	}
	if m1.TaggingMode != "untagged" {
		t.Errorf("Vlan100|Ethernet2 tagging_mode = %q, want %q", m1.TaggingMode, "untagged")
	}

	m2, ok := d.ConfigDB.VLANMember["Vlan100|Ethernet3"]
	if !ok {
		t.Fatal("Vlan100|Ethernet3 not found")
	}
	if m2.TaggingMode != "tagged" {
		t.Errorf("Vlan100|Ethernet3 tagging_mode = %q, want %q", m2.TaggingMode, "tagged")
	}

	m3, ok := d.ConfigDB.VLANMember["Vlan200|PortChannel100"]
	if !ok {
		t.Fatal("Vlan200|PortChannel100 not found")
	}
	if m3.TaggingMode != "tagged" {
		t.Errorf("Vlan200|PortChannel100 tagging_mode = %q, want %q", m3.TaggingMode, "tagged")
	}
}

func TestVLANInterfaceTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	svi, ok := d.ConfigDB.VLANInterface["Vlan100"]
	if !ok {
		t.Fatal("Vlan100 not found in VLANInterface")
	}
	if svi["vrf_name"] != "Vrf_CUST1" {
		t.Errorf("Vlan100 vrf_name = %q, want %q", svi["vrf_name"], "Vrf_CUST1")
	}
}

func TestVRFTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.VRF) != 1 {
		t.Fatalf("expected 1 VRF, got %d", len(d.ConfigDB.VRF))
	}

	vrf, ok := d.ConfigDB.VRF["Vrf_CUST1"]
	if !ok {
		t.Fatal("Vrf_CUST1 not found")
	}
	if vrf.VNI != "10001" {
		t.Errorf("Vrf_CUST1 vni = %q, want %q", vrf.VNI, "10001")
	}
}

func TestInterfaceTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Ethernet0 has an entry with empty fields
	if _, ok := d.ConfigDB.Interface["Ethernet0"]; !ok {
		t.Error("Ethernet0 not found in INTERFACE table")
	}

	// Ethernet1 has vrf_name set
	eth1, ok := d.ConfigDB.Interface["Ethernet1"]
	if !ok {
		t.Fatal("Ethernet1 not found in INTERFACE table")
	}
	if eth1.VRFName != "Vrf_CUST1" {
		t.Errorf("Ethernet1 vrf_name = %q, want %q", eth1.VRFName, "Vrf_CUST1")
	}
}

func TestVXLANTunnel(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	vtep, ok := d.ConfigDB.VXLANTunnel["vtep1"]
	if !ok {
		t.Fatal("vtep1 not found in VXLANTunnel")
	}
	if vtep.SrcIP != "10.0.0.10" {
		t.Errorf("vtep1 src_ip = %q, want %q", vtep.SrcIP, "10.0.0.10")
	}
}

func TestBGPNeighbor(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.BGPNeighbor) != 2 {
		t.Fatalf("expected 2 BGP neighbors, got %d", len(d.ConfigDB.BGPNeighbor))
	}

	n1, ok := d.ConfigDB.BGPNeighbor["10.0.0.1"]
	if !ok {
		t.Fatal("BGP neighbor 10.0.0.1 not found")
	}
	if n1.ASN != "13908" {
		t.Errorf("neighbor 10.0.0.1 asn = %q, want %q", n1.ASN, "13908")
	}
	if n1.Name != "spine1-ny" {
		t.Errorf("neighbor 10.0.0.1 name = %q, want %q", n1.Name, "spine1-ny")
	}

	n2, ok := d.ConfigDB.BGPNeighbor["10.0.0.2"]
	if !ok {
		t.Fatal("BGP neighbor 10.0.0.2 not found")
	}
	if n2.ASN != "13908" {
		t.Errorf("neighbor 10.0.0.2 asn = %q, want %q", n2.ASN, "13908")
	}
	if n2.Name != "spine2-ny" {
		t.Errorf("neighbor 10.0.0.2 name = %q, want %q", n2.Name, "spine2-ny")
	}
}

func TestACLTable(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if len(d.ConfigDB.ACLTable) != 2 {
		t.Fatalf("expected 2 ACL tables, got %d", len(d.ConfigDB.ACLTable))
	}

	aclIn, ok := d.ConfigDB.ACLTable["customer-l3-in"]
	if !ok {
		t.Fatal("ACL table customer-l3-in not found")
	}
	if aclIn.Type != "L3" {
		t.Errorf("customer-l3-in type = %q, want %q", aclIn.Type, "L3")
	}
	if aclIn.Stage != "ingress" {
		t.Errorf("customer-l3-in stage = %q, want %q", aclIn.Stage, "ingress")
	}

	aclOut, ok := d.ConfigDB.ACLTable["customer-l3-out"]
	if !ok {
		t.Fatal("ACL table customer-l3-out not found")
	}
	if aclOut.Type != "L3" {
		t.Errorf("customer-l3-out type = %q, want %q", aclOut.Type, "L3")
	}
	if aclOut.Stage != "egress" {
		t.Errorf("customer-l3-out stage = %q, want %q", aclOut.Stage, "egress")
	}
}

func TestServiceBinding(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	binding, ok := d.ConfigDB.NewtronServiceBinding["Ethernet1"]
	if !ok {
		t.Fatal("service binding for Ethernet1 not found")
	}
	if binding.ServiceName != "customer-l3" {
		t.Errorf("Ethernet1 service_name = %q, want %q", binding.ServiceName, "customer-l3")
	}
	if binding.IPAddress != "10.1.1.1/30" {
		t.Errorf("Ethernet1 ip_address = %q, want %q", binding.IPAddress, "10.1.1.1/30")
	}
	if binding.VRFName != "Vrf_CUST1" {
		t.Errorf("Ethernet1 vrf_name = %q, want %q", binding.VRFName, "Vrf_CUST1")
	}
	if binding.IngressACL != "customer-l3-in" {
		t.Errorf("Ethernet1 ingress_acl = %q, want %q", binding.IngressACL, "customer-l3-in")
	}
	if binding.EgressACL != "customer-l3-out" {
		t.Errorf("Ethernet1 egress_acl = %q, want %q", binding.EgressACL, "customer-l3-out")
	}
}

func TestStateDB(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if d.StateDB == nil {
		t.Fatal("StateDB is nil after Connect")
	}
	if len(d.StateDB.PortTable) == 0 {
		t.Error("StateDB.PortTable is empty")
	}
	if len(d.StateDB.LAGTable) == 0 {
		t.Error("StateDB.LAGTable is empty")
	}
	if len(d.StateDB.LAGMemberTable) == 0 {
		t.Error("StateDB.LAGMemberTable is empty")
	}
	if len(d.StateDB.BGPNeighborTable) == 0 {
		t.Error("StateDB.BGPNeighborTable is empty")
	}
	if len(d.StateDB.VXLANTunnelTable) == 0 {
		t.Error("StateDB.VXLANTunnelTable is empty")
	}
}

func TestInterfaceExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Positive: physical port exists
	if !d.InterfaceExists("Ethernet0") {
		t.Error("InterfaceExists(Ethernet0) = false, want true")
	}

	// Positive: port channel exists
	if !d.InterfaceExists("PortChannel100") {
		t.Error("InterfaceExists(PortChannel100) = false, want true")
	}

	// Negative: non-existent interface
	if d.InterfaceExists("Ethernet99") {
		t.Error("InterfaceExists(Ethernet99) = true, want false")
	}
}

func TestVLANExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Positive
	if !d.VLANExists(100) {
		t.Error("VLANExists(100) = false, want true")
	}
	if !d.VLANExists(200) {
		t.Error("VLANExists(200) = false, want true")
	}

	// Negative
	if d.VLANExists(999) {
		t.Error("VLANExists(999) = true, want false")
	}
}

func TestVRFExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Positive
	if !d.VRFExists("Vrf_CUST1") {
		t.Error("VRFExists(Vrf_CUST1) = false, want true")
	}

	// Negative
	if d.VRFExists("Vrf_NONEXISTENT") {
		t.Error("VRFExists(Vrf_NONEXISTENT) = true, want false")
	}
}

func TestPortChannelExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Positive
	if !d.PortChannelExists("PortChannel100") {
		t.Error("PortChannelExists(PortChannel100) = false, want true")
	}

	// Negative
	if d.PortChannelExists("PortChannel999") {
		t.Error("PortChannelExists(PortChannel999) = true, want false")
	}
}

func TestVTEPExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if !d.VTEPExists() {
		t.Error("VTEPExists() = false, want true")
	}
}

func TestBGPConfigured(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	if !d.BGPConfigured() {
		t.Error("BGPConfigured() = false, want true")
	}
}

func TestACLTableExists(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Positive
	if !d.ACLTableExists("customer-l3-in") {
		t.Error("ACLTableExists(customer-l3-in) = false, want true")
	}
	if !d.ACLTableExists("customer-l3-out") {
		t.Error("ACLTableExists(customer-l3-out) = false, want true")
	}

	// Negative
	if d.ACLTableExists("nonexistent-acl") {
		t.Error("ACLTableExists(nonexistent-acl) = true, want false")
	}
}

func TestInterfaceIsLAGMember(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Ethernet4 is a LAG member
	if !d.InterfaceIsLAGMember("Ethernet4") {
		t.Error("InterfaceIsLAGMember(Ethernet4) = false, want true")
	}

	// Ethernet5 is a LAG member
	if !d.InterfaceIsLAGMember("Ethernet5") {
		t.Error("InterfaceIsLAGMember(Ethernet5) = false, want true")
	}

	// Ethernet0 is not a LAG member
	if d.InterfaceIsLAGMember("Ethernet0") {
		t.Error("InterfaceIsLAGMember(Ethernet0) = true, want false")
	}
}

func TestGetInterfaceLAG(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	lag := d.GetInterfaceLAG("Ethernet4")
	if lag != "PortChannel100" {
		t.Errorf("GetInterfaceLAG(Ethernet4) = %q, want %q", lag, "PortChannel100")
	}

	lag = d.GetInterfaceLAG("Ethernet5")
	if lag != "PortChannel100" {
		t.Errorf("GetInterfaceLAG(Ethernet5) = %q, want %q", lag, "PortChannel100")
	}

	lag = d.GetInterfaceLAG("Ethernet0")
	if lag != "" {
		t.Errorf("GetInterfaceLAG(Ethernet0) = %q, want empty string", lag)
	}
}

func TestLockUnlock(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	ctx := testutil.Context(t)

	if d.IsLocked() {
		t.Error("expected IsLocked to be false before Lock")
	}

	if err := d.Lock(ctx); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	if !d.IsLocked() {
		t.Error("expected IsLocked to be true after Lock")
	}

	if err := d.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	if d.IsLocked() {
		t.Error("expected IsLocked to be false after Unlock")
	}
}

func TestInterfaceHasService(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Ethernet0 does not have a service
	if d.InterfaceHasService("Ethernet0") {
		t.Error("InterfaceHasService(Ethernet0) = true, want false")
	}

	// A non-existent interface should return false
	if d.InterfaceHasService("Ethernet99") {
		t.Error("InterfaceHasService(Ethernet99) = true, want false")
	}
}
