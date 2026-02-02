package model

import (
	"testing"
)

// ===================== Interface Tests =====================

func TestInterface_IsPhysical(t *testing.T) {
	tests := []struct {
		name     string
		ifName   string
		expected bool
	}{
		{"Ethernet uppercase", "Ethernet0", true},
		{"ethernet lowercase", "ethernet0", true},
		{"PortChannel", "PortChannel100", false},
		{"Vlan", "Vlan100", false},
		{"Loopback", "Loopback0", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Interface{Name: tt.ifName}
			if got := i.IsPhysical(); got != tt.expected {
				t.Errorf("IsPhysical() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInterface_IsLAG(t *testing.T) {
	tests := []struct {
		name     string
		ifName   string
		expected bool
	}{
		{"PortChannel uppercase", "PortChannel100", true},
		{"portchannel lowercase", "portchannel100", true},
		{"Ethernet", "Ethernet0", false},
		{"Vlan", "Vlan100", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Interface{Name: tt.ifName}
			if got := i.IsLAG(); got != tt.expected {
				t.Errorf("IsLAG() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInterface_IsVLAN(t *testing.T) {
	tests := []struct {
		name     string
		ifName   string
		expected bool
	}{
		{"Vlan uppercase", "Vlan100", true},
		{"vlan lowercase", "vlan100", true},
		{"Ethernet", "Ethernet0", false},
		{"PortChannel", "PortChannel100", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Interface{Name: tt.ifName}
			if got := i.IsVLAN(); got != tt.expected {
				t.Errorf("IsVLAN() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInterface_IsLoopback(t *testing.T) {
	tests := []struct {
		name     string
		ifName   string
		expected bool
	}{
		{"Loopback uppercase", "Loopback0", true},
		{"loopback lowercase", "loopback0", true},
		{"Ethernet", "Ethernet0", false},
		{"Vlan", "Vlan100", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Interface{Name: tt.ifName}
			if got := i.IsLoopback(); got != tt.expected {
				t.Errorf("IsLoopback() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInterface_IsLAGMember(t *testing.T) {
	t.Run("is member", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", LAG: "PortChannel100"}
		if !i.IsLAGMember() {
			t.Error("Expected IsLAGMember() to be true")
		}
	})

	t.Run("not member", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0"}
		if i.IsLAGMember() {
			t.Error("Expected IsLAGMember() to be false")
		}
	})
}

func TestInterface_HasService(t *testing.T) {
	t.Run("has service", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Service: "customer-l3"}
		if !i.HasService() {
			t.Error("Expected HasService() to be true")
		}
	})

	t.Run("no service", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0"}
		if i.HasService() {
			t.Error("Expected HasService() to be false")
		}
	})
}

func TestInterface_HasIPAddress(t *testing.T) {
	t.Run("has IPv4", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", IPv4Addrs: []string{"10.1.1.1/30"}}
		if !i.HasIPAddress() {
			t.Error("Expected HasIPAddress() to be true")
		}
	})

	t.Run("has IPv6", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", IPv6Addrs: []string{"2001:db8::1/64"}}
		if !i.HasIPAddress() {
			t.Error("Expected HasIPAddress() to be true")
		}
	})

	t.Run("no address", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0"}
		if i.HasIPAddress() {
			t.Error("Expected HasIPAddress() to be false")
		}
	})
}

func TestInterface_IsRouted(t *testing.T) {
	t.Run("explicit routed mode", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Mode: "routed"}
		if !i.IsRouted() {
			t.Error("Expected IsRouted() to be true")
		}
	})

	t.Run("no mode but has IP", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", IPv4Addrs: []string{"10.1.1.1/30"}}
		if !i.IsRouted() {
			t.Error("Expected IsRouted() to be true for interface with IP")
		}
	})

	t.Run("access mode", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Mode: "access"}
		if i.IsRouted() {
			t.Error("Expected IsRouted() to be false for access mode")
		}
	})
}

func TestInterface_IsSwitched(t *testing.T) {
	t.Run("access mode", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Mode: "access"}
		if !i.IsSwitched() {
			t.Error("Expected IsSwitched() to be true")
		}
	})

	t.Run("trunk mode", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Mode: "trunk"}
		if !i.IsSwitched() {
			t.Error("Expected IsSwitched() to be true")
		}
	})

	t.Run("routed mode", func(t *testing.T) {
		i := &Interface{Name: "Ethernet0", Mode: "routed"}
		if i.IsSwitched() {
			t.Error("Expected IsSwitched() to be false")
		}
	})
}

// ===================== LAG Tests =====================

func TestNewPortChannel(t *testing.T) {
	pc := NewPortChannel("PortChannel100", []string{"Ethernet0", "Ethernet4"})

	if pc.Name != "PortChannel100" {
		t.Errorf("Name = %q, want %q", pc.Name, "PortChannel100")
	}
	if len(pc.Members) != 2 {
		t.Errorf("Members count = %d, want %d", len(pc.Members), 2)
	}
	if pc.MinLinks != 1 {
		t.Errorf("MinLinks = %d, want %d", pc.MinLinks, 1)
	}
	if pc.Mode != string(LACPModeActive) {
		t.Errorf("Mode = %q, want %q", pc.Mode, LACPModeActive)
	}
	if !pc.FastRate {
		t.Error("FastRate should be true by default")
	}
	if pc.MTU != 9100 {
		t.Errorf("MTU = %d, want %d", pc.MTU, 9100)
	}
	if pc.AdminStatus != "up" {
		t.Errorf("AdminStatus = %q, want %q", pc.AdminStatus, "up")
	}
}

func TestPortChannel_AddMember(t *testing.T) {
	pc := NewPortChannel("PortChannel100", []string{"Ethernet0"})

	pc.AddMember("Ethernet4")
	if len(pc.Members) != 2 {
		t.Errorf("Members count = %d, want %d", len(pc.Members), 2)
	}

	// Adding duplicate should not add again
	pc.AddMember("Ethernet4")
	if len(pc.Members) != 2 {
		t.Errorf("Duplicate add should not increase count, got %d", len(pc.Members))
	}
}

func TestPortChannel_RemoveMember(t *testing.T) {
	pc := NewPortChannel("PortChannel100", []string{"Ethernet0", "Ethernet4"})

	removed := pc.RemoveMember("Ethernet0")
	if !removed {
		t.Error("RemoveMember() should return true")
	}
	if len(pc.Members) != 1 {
		t.Errorf("Members count = %d, want %d", len(pc.Members), 1)
	}

	// Removing non-existent should return false
	removed = pc.RemoveMember("Ethernet0")
	if removed {
		t.Error("RemoveMember() should return false for non-existent")
	}
}

func TestPortChannel_HasMember(t *testing.T) {
	pc := NewPortChannel("PortChannel100", []string{"Ethernet0", "Ethernet4"})

	if !pc.HasMember("Ethernet0") {
		t.Error("HasMember() should return true for Ethernet0")
	}
	if pc.HasMember("Ethernet8") {
		t.Error("HasMember() should return false for non-member")
	}
}

func TestPortChannel_MemberCount(t *testing.T) {
	pc := NewPortChannel("PortChannel100", []string{"Ethernet0", "Ethernet4", "Ethernet8"})
	if pc.MemberCount() != 3 {
		t.Errorf("MemberCount() = %d, want %d", pc.MemberCount(), 3)
	}
}

// ===================== VLAN Tests =====================

func TestNewVLAN(t *testing.T) {
	vlan := NewVLAN(100, "Servers")

	if vlan.ID != 100 {
		t.Errorf("ID = %d, want %d", vlan.ID, 100)
	}
	if vlan.Name != "Servers" {
		t.Errorf("Name = %q, want %q", vlan.Name, "Servers")
	}
	if vlan.AdminStatus != "up" {
		t.Errorf("AdminStatus = %q, want %q", vlan.AdminStatus, "up")
	}
}

func TestVLAN_HasSVI(t *testing.T) {
	t.Run("has IPv4 address", func(t *testing.T) {
		vlan := &VLAN{ID: 100, IPv4Address: "10.1.100.1/24"}
		if !vlan.HasSVI() {
			t.Error("Expected HasSVI() to be true")
		}
	})

	t.Run("has anycast gateway", func(t *testing.T) {
		vlan := &VLAN{ID: 100, AnycastGateway: "10.1.100.1/24"}
		if !vlan.HasSVI() {
			t.Error("Expected HasSVI() to be true")
		}
	})

	t.Run("no SVI", func(t *testing.T) {
		vlan := &VLAN{ID: 100}
		if vlan.HasSVI() {
			t.Error("Expected HasSVI() to be false")
		}
	})
}

func TestVLAN_HasEVPN(t *testing.T) {
	t.Run("has L2VNI", func(t *testing.T) {
		vlan := &VLAN{ID: 100, L2VNI: 10100}
		if !vlan.HasEVPN() {
			t.Error("Expected HasEVPN() to be true")
		}
	})

	t.Run("no L2VNI", func(t *testing.T) {
		vlan := &VLAN{ID: 100}
		if vlan.HasEVPN() {
			t.Error("Expected HasEVPN() to be false")
		}
	})
}

func TestVLAN_IsIRB(t *testing.T) {
	t.Run("L2VNI and SVI", func(t *testing.T) {
		vlan := &VLAN{ID: 100, L2VNI: 10100, IPv4Address: "10.1.100.1/24"}
		if !vlan.IsIRB() {
			t.Error("Expected IsIRB() to be true")
		}
	})

	t.Run("only L2VNI", func(t *testing.T) {
		vlan := &VLAN{ID: 100, L2VNI: 10100}
		if vlan.IsIRB() {
			t.Error("Expected IsIRB() to be false without SVI")
		}
	})

	t.Run("only SVI", func(t *testing.T) {
		vlan := &VLAN{ID: 100, IPv4Address: "10.1.100.1/24"}
		if vlan.IsIRB() {
			t.Error("Expected IsIRB() to be false without L2VNI")
		}
	})
}

func TestVLAN_AddTaggedPort(t *testing.T) {
	vlan := NewVLAN(100, "Test")

	vlan.AddTaggedPort("Ethernet0")
	if len(vlan.TaggedPorts) != 1 {
		t.Errorf("TaggedPorts count = %d, want %d", len(vlan.TaggedPorts), 1)
	}

	// Duplicate should not add again
	vlan.AddTaggedPort("Ethernet0")
	if len(vlan.TaggedPorts) != 1 {
		t.Errorf("Duplicate add should not increase count, got %d", len(vlan.TaggedPorts))
	}
}

func TestVLAN_AddUntaggedPort(t *testing.T) {
	vlan := NewVLAN(100, "Test")

	vlan.AddUntaggedPort("Ethernet0")
	if len(vlan.UntaggedPorts) != 1 {
		t.Errorf("UntaggedPorts count = %d, want %d", len(vlan.UntaggedPorts), 1)
	}

	// Duplicate should not add again
	vlan.AddUntaggedPort("Ethernet0")
	if len(vlan.UntaggedPorts) != 1 {
		t.Errorf("Duplicate add should not increase count, got %d", len(vlan.UntaggedPorts))
	}
}

func TestVLAN_RemovePort(t *testing.T) {
	vlan := NewVLAN(100, "Test")
	vlan.AddTaggedPort("Ethernet0")
	vlan.AddUntaggedPort("Ethernet4")

	if !vlan.RemovePort("Ethernet0") {
		t.Error("RemovePort() should return true for tagged port")
	}
	if len(vlan.TaggedPorts) != 0 {
		t.Errorf("TaggedPorts count = %d, want %d", len(vlan.TaggedPorts), 0)
	}

	if !vlan.RemovePort("Ethernet4") {
		t.Error("RemovePort() should return true for untagged port")
	}

	if vlan.RemovePort("Ethernet8") {
		t.Error("RemovePort() should return false for non-member")
	}
}

func TestVLAN_HasPort(t *testing.T) {
	vlan := NewVLAN(100, "Test")
	vlan.AddTaggedPort("Ethernet0")
	vlan.AddUntaggedPort("Ethernet4")

	if !vlan.HasPort("Ethernet0") {
		t.Error("HasPort() should return true for tagged port")
	}
	if !vlan.HasPort("Ethernet4") {
		t.Error("HasPort() should return true for untagged port")
	}
	if vlan.HasPort("Ethernet8") {
		t.Error("HasPort() should return false for non-member")
	}
}

// ===================== VRF Tests =====================

func TestNewVRF(t *testing.T) {
	vrf := NewVRF("customer-l3")

	if vrf.Name != "customer-l3" {
		t.Errorf("Name = %q, want %q", vrf.Name, "customer-l3")
	}
	if vrf.VRFType != "interface" {
		t.Errorf("VRFType = %q, want %q", vrf.VRFType, "interface")
	}
}

func TestNewSharedVRF(t *testing.T) {
	vrf := NewSharedVRF("Vrf_CUST1", 10001, []string{"65000:1"}, []string{"65000:2"})

	if vrf.Name != "Vrf_CUST1" {
		t.Errorf("Name = %q, want %q", vrf.Name, "Vrf_CUST1")
	}
	if vrf.L3VNI != 10001 {
		t.Errorf("L3VNI = %d, want %d", vrf.L3VNI, 10001)
	}
	if vrf.VRFType != "shared" {
		t.Errorf("VRFType = %q, want %q", vrf.VRFType, "shared")
	}
	if len(vrf.ImportRT) != 1 || vrf.ImportRT[0] != "65000:1" {
		t.Errorf("ImportRT = %v", vrf.ImportRT)
	}
	if len(vrf.ExportRT) != 1 || vrf.ExportRT[0] != "65000:2" {
		t.Errorf("ExportRT = %v", vrf.ExportRT)
	}
}

func TestVRF_HasEVPN(t *testing.T) {
	t.Run("has L3VNI", func(t *testing.T) {
		vrf := &VRF{Name: "test", L3VNI: 10001}
		if !vrf.HasEVPN() {
			t.Error("Expected HasEVPN() to be true")
		}
	})

	t.Run("no L3VNI", func(t *testing.T) {
		vrf := &VRF{Name: "test"}
		if vrf.HasEVPN() {
			t.Error("Expected HasEVPN() to be false")
		}
	})
}

func TestVRF_AddInterface(t *testing.T) {
	vrf := NewVRF("test")

	vrf.AddInterface("Ethernet0")
	if len(vrf.Interfaces) != 1 {
		t.Errorf("Interfaces count = %d, want %d", len(vrf.Interfaces), 1)
	}

	// Duplicate should not add again
	vrf.AddInterface("Ethernet0")
	if len(vrf.Interfaces) != 1 {
		t.Errorf("Duplicate add should not increase count, got %d", len(vrf.Interfaces))
	}
}

func TestVRF_RemoveInterface(t *testing.T) {
	vrf := NewVRF("test")
	vrf.AddInterface("Ethernet0")
	vrf.AddInterface("Ethernet4")

	if !vrf.RemoveInterface("Ethernet0") {
		t.Error("RemoveInterface() should return true")
	}
	if len(vrf.Interfaces) != 1 {
		t.Errorf("Interfaces count = %d, want %d", len(vrf.Interfaces), 1)
	}

	if vrf.RemoveInterface("Ethernet0") {
		t.Error("RemoveInterface() should return false for non-member")
	}
}

func TestVRF_HasInterface(t *testing.T) {
	vrf := NewVRF("test")
	vrf.AddInterface("Ethernet0")

	if !vrf.HasInterface("Ethernet0") {
		t.Error("HasInterface() should return true")
	}
	if vrf.HasInterface("Ethernet4") {
		t.Error("HasInterface() should return false for non-member")
	}
}

func TestVRF_AddVLAN(t *testing.T) {
	vrf := NewVRF("test")

	vrf.AddVLAN(100)
	if len(vrf.VLANs) != 1 {
		t.Errorf("VLANs count = %d, want %d", len(vrf.VLANs), 1)
	}

	// Duplicate should not add again
	vrf.AddVLAN(100)
	if len(vrf.VLANs) != 1 {
		t.Errorf("Duplicate add should not increase count, got %d", len(vrf.VLANs))
	}
}

func TestVRF_IsEmpty(t *testing.T) {
	t.Run("empty VRF", func(t *testing.T) {
		vrf := NewVRF("test")
		if !vrf.IsEmpty() {
			t.Error("Expected IsEmpty() to be true")
		}
	})

	t.Run("has interface", func(t *testing.T) {
		vrf := NewVRF("test")
		vrf.AddInterface("Ethernet0")
		if vrf.IsEmpty() {
			t.Error("Expected IsEmpty() to be false with interface")
		}
	})

	t.Run("has VLAN", func(t *testing.T) {
		vrf := NewVRF("test")
		vrf.AddVLAN(100)
		if vrf.IsEmpty() {
			t.Error("Expected IsEmpty() to be false with VLAN")
		}
	})
}

func TestVRF_IsShared(t *testing.T) {
	t.Run("shared VRF", func(t *testing.T) {
		vrf := NewSharedVRF("test", 10001, nil, nil)
		if !vrf.IsShared() {
			t.Error("Expected IsShared() to be true")
		}
	})

	t.Run("interface VRF", func(t *testing.T) {
		vrf := NewVRF("test")
		if vrf.IsShared() {
			t.Error("Expected IsShared() to be false")
		}
	})
}

// ===================== ACL Tests =====================

func TestProtocolFromName(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		{"icmp", ProtocolICMP},
		{"tcp", ProtocolTCP},
		{"udp", ProtocolUDP},
		{"ospf", ProtocolOSPF},
		{"vrrp", ProtocolVRRP},
		{"unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProtocolFromName(tt.name)
			if got != tt.expected {
				t.Errorf("ProtocolFromName(%q) = %d, want %d", tt.name, got, tt.expected)
			}
		})
	}
}

func TestNewACLTable(t *testing.T) {
	table := NewACLTable("DATA-ACL", ACLTypeL3, ACLStageIngress)

	if table.Name != "DATA-ACL" {
		t.Errorf("Name = %q, want %q", table.Name, "DATA-ACL")
	}
	if table.Type != ACLTypeL3 {
		t.Errorf("Type = %q, want %q", table.Type, ACLTypeL3)
	}
	if table.Stage != ACLStageIngress {
		t.Errorf("Stage = %q, want %q", table.Stage, ACLStageIngress)
	}
}

func TestNewACLRule(t *testing.T) {
	rule := NewACLRule("RULE_100", 9999, ACLActionForward)

	if rule.Name != "RULE_100" {
		t.Errorf("Name = %q, want %q", rule.Name, "RULE_100")
	}
	if rule.Priority != 9999 {
		t.Errorf("Priority = %d, want %d", rule.Priority, 9999)
	}
	if rule.PacketAction != ACLActionForward {
		t.Errorf("PacketAction = %q, want %q", rule.PacketAction, ACLActionForward)
	}
}

func TestACLTable_AddRule(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)

	// Add rules in non-priority order
	table.AddRule(NewACLRule("RULE_100", 100, ACLActionForward))
	table.AddRule(NewACLRule("RULE_200", 200, ACLActionForward))
	table.AddRule(NewACLRule("RULE_150", 150, ACLActionForward))

	if len(table.Rules) != 3 {
		t.Errorf("Rules count = %d, want %d", len(table.Rules), 3)
	}

	// Check priority order (highest first)
	if table.Rules[0].Priority != 200 {
		t.Errorf("First rule priority = %d, want %d", table.Rules[0].Priority, 200)
	}
	if table.Rules[1].Priority != 150 {
		t.Errorf("Second rule priority = %d, want %d", table.Rules[1].Priority, 150)
	}
	if table.Rules[2].Priority != 100 {
		t.Errorf("Third rule priority = %d, want %d", table.Rules[2].Priority, 100)
	}
}

func TestACLTable_RemoveRule(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)
	table.AddRule(NewACLRule("RULE_100", 100, ACLActionForward))

	if !table.RemoveRule("RULE_100") {
		t.Error("RemoveRule() should return true")
	}
	if len(table.Rules) != 0 {
		t.Errorf("Rules count = %d, want %d", len(table.Rules), 0)
	}

	if table.RemoveRule("RULE_100") {
		t.Error("RemoveRule() should return false for non-existent")
	}
}

func TestACLTable_GetRule(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)
	table.AddRule(NewACLRule("RULE_100", 100, ACLActionForward))

	rule := table.GetRule("RULE_100")
	if rule == nil {
		t.Error("GetRule() should not return nil")
	}
	if rule.Name != "RULE_100" {
		t.Errorf("Rule name = %q, want %q", rule.Name, "RULE_100")
	}

	if table.GetRule("NONEXISTENT") != nil {
		t.Error("GetRule() should return nil for non-existent")
	}
}

func TestACLTable_BindPort(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)

	table.BindPort("Ethernet0")
	if len(table.Ports) != 1 {
		t.Errorf("Ports count = %d, want %d", len(table.Ports), 1)
	}

	// Duplicate should not add again
	table.BindPort("Ethernet0")
	if len(table.Ports) != 1 {
		t.Errorf("Duplicate bind should not increase count, got %d", len(table.Ports))
	}
}

func TestACLTable_UnbindPort(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)
	table.BindPort("Ethernet0")

	if !table.UnbindPort("Ethernet0") {
		t.Error("UnbindPort() should return true")
	}
	if len(table.Ports) != 0 {
		t.Errorf("Ports count = %d, want %d", len(table.Ports), 0)
	}

	if table.UnbindPort("Ethernet0") {
		t.Error("UnbindPort() should return false for non-bound")
	}
}

func TestACLTable_IsBoundTo(t *testing.T) {
	table := NewACLTable("TEST", ACLTypeL3, ACLStageIngress)
	table.BindPort("Ethernet0")

	if !table.IsBoundTo("Ethernet0") {
		t.Error("IsBoundTo() should return true")
	}
	if table.IsBoundTo("Ethernet4") {
		t.Error("IsBoundTo() should return false for non-bound")
	}
}

// ===================== BGP Tests =====================

func TestNewBGPConfig(t *testing.T) {
	config := NewBGPConfig(65000, "10.0.0.1")

	if config.LocalAS != 65000 {
		t.Errorf("LocalAS = %d, want %d", config.LocalAS, 65000)
	}
	if config.RouterID != "10.0.0.1" {
		t.Errorf("RouterID = %q, want %q", config.RouterID, "10.0.0.1")
	}
	if len(config.AddressFamilies) != 2 {
		t.Errorf("AddressFamilies count = %d, want %d", len(config.AddressFamilies), 2)
	}
}

func TestNewBGPNeighbor(t *testing.T) {
	neighbor := NewBGPNeighbor("10.0.0.2", 65000)

	if neighbor.Address != "10.0.0.2" {
		t.Errorf("Address = %q, want %q", neighbor.Address, "10.0.0.2")
	}
	if neighbor.RemoteAS != 65000 {
		t.Errorf("RemoteAS = %d, want %d", neighbor.RemoteAS, 65000)
	}
	if neighbor.HoldTime != 180 {
		t.Errorf("HoldTime = %d, want %d", neighbor.HoldTime, 180)
	}
	if neighbor.KeepaliveTime != 60 {
		t.Errorf("KeepaliveTime = %d, want %d", neighbor.KeepaliveTime, 60)
	}
	if !neighbor.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestNewEVPNNeighbor(t *testing.T) {
	neighbor := NewEVPNNeighbor("10.0.0.2", 65000, "Loopback0")

	if !neighbor.EVPNEnabled {
		t.Error("EVPNEnabled should be true")
	}
	if neighbor.UpdateSource != "Loopback0" {
		t.Errorf("UpdateSource = %q, want %q", neighbor.UpdateSource, "Loopback0")
	}
	if len(neighbor.AddressFamilies) != 1 || neighbor.AddressFamilies[0] != "l2vpn-evpn" {
		t.Errorf("AddressFamilies = %v", neighbor.AddressFamilies)
	}
}

func TestBGPConfig_AddNeighbor(t *testing.T) {
	config := NewBGPConfig(65000, "10.0.0.1")

	config.AddNeighbor(NewBGPNeighbor("10.0.0.2", 65000))
	if len(config.Neighbors) != 1 {
		t.Errorf("Neighbors count = %d, want %d", len(config.Neighbors), 1)
	}

	// Add same neighbor should update, not duplicate
	updated := NewBGPNeighbor("10.0.0.2", 65001)
	config.AddNeighbor(updated)
	if len(config.Neighbors) != 1 {
		t.Errorf("Update should not increase count, got %d", len(config.Neighbors))
	}
	if config.Neighbors[0].RemoteAS != 65001 {
		t.Errorf("Neighbor should be updated, RemoteAS = %d", config.Neighbors[0].RemoteAS)
	}
}

func TestBGPConfig_RemoveNeighbor(t *testing.T) {
	config := NewBGPConfig(65000, "10.0.0.1")
	config.AddNeighbor(NewBGPNeighbor("10.0.0.2", 65000))

	if !config.RemoveNeighbor("10.0.0.2") {
		t.Error("RemoveNeighbor() should return true")
	}
	if len(config.Neighbors) != 0 {
		t.Errorf("Neighbors count = %d, want %d", len(config.Neighbors), 0)
	}

	if config.RemoveNeighbor("10.0.0.2") {
		t.Error("RemoveNeighbor() should return false for non-existent")
	}
}

func TestBGPConfig_GetNeighbor(t *testing.T) {
	config := NewBGPConfig(65000, "10.0.0.1")
	config.AddNeighbor(NewBGPNeighbor("10.0.0.2", 65000))

	neighbor := config.GetNeighbor("10.0.0.2")
	if neighbor == nil {
		t.Error("GetNeighbor() should not return nil")
	}

	if config.GetNeighbor("10.0.0.3") != nil {
		t.Error("GetNeighbor() should return nil for non-existent")
	}
}

func TestBGPConfig_HasEVPN(t *testing.T) {
	t.Run("has EVPN", func(t *testing.T) {
		config := NewBGPConfig(65000, "10.0.0.1")
		if !config.HasEVPN() {
			t.Error("Expected HasEVPN() to be true")
		}
	})

	t.Run("no EVPN", func(t *testing.T) {
		config := &BGPConfig{
			LocalAS:         65000,
			AddressFamilies: []string{"ipv4-unicast"},
		}
		if config.HasEVPN() {
			t.Error("Expected HasEVPN() to be false")
		}
	})
}

func TestBGPNeighbor_IsIBGP(t *testing.T) {
	neighbor := NewBGPNeighbor("10.0.0.2", 65000)

	if !neighbor.IsIBGP(65000) {
		t.Error("Expected IsIBGP() to be true")
	}
	if neighbor.IsIBGP(65001) {
		t.Error("Expected IsIBGP() to be false")
	}
}

func TestBGPNeighbor_IsEBGP(t *testing.T) {
	neighbor := NewBGPNeighbor("10.0.0.2", 65001)

	if !neighbor.IsEBGP(65000) {
		t.Error("Expected IsEBGP() to be true")
	}
	if neighbor.IsEBGP(65001) {
		t.Error("Expected IsEBGP() to be false")
	}
}

// ===================== EVPN Tests =====================

func TestNewVTEP(t *testing.T) {
	vtep := NewVTEP("vtep1", "10.0.0.1")

	if vtep.Name != "vtep1" {
		t.Errorf("Name = %q, want %q", vtep.Name, "vtep1")
	}
	if vtep.SourceIP != "10.0.0.1" {
		t.Errorf("SourceIP = %q, want %q", vtep.SourceIP, "10.0.0.1")
	}
	if vtep.SourceInterface != "Loopback0" {
		t.Errorf("SourceInterface = %q, want %q", vtep.SourceInterface, "Loopback0")
	}
	if vtep.UDPPort != 4789 {
		t.Errorf("UDPPort = %d, want %d", vtep.UDPPort, 4789)
	}
}

func TestEVPNRouteTypes(t *testing.T) {
	if EVPNRouteType2 != 2 {
		t.Errorf("EVPNRouteType2 = %d, want %d", EVPNRouteType2, 2)
	}
	if EVPNRouteType3 != 3 {
		t.Errorf("EVPNRouteType3 = %d, want %d", EVPNRouteType3, 3)
	}
	if EVPNRouteType5 != 5 {
		t.Errorf("EVPNRouteType5 = %d, want %d", EVPNRouteType5, 5)
	}
}

// ===================== QoS Tests =====================

func TestNewQoSProfile(t *testing.T) {
	profile := NewQoSProfile("customer", "8q")

	if profile.Name != "customer" {
		t.Errorf("Name = %q, want %q", profile.Name, "customer")
	}
	if profile.SchedulerMap != "8q" {
		t.Errorf("SchedulerMap = %q, want %q", profile.SchedulerMap, "8q")
	}
}

func TestNewPolicer(t *testing.T) {
	policer := NewPolicer("rate-limit", 100000000, 10000000)

	if policer.Name != "rate-limit" {
		t.Errorf("Name = %q, want %q", policer.Name, "rate-limit")
	}
	if policer.CIR != 100000000 {
		t.Errorf("CIR = %d, want %d", policer.CIR, 100000000)
	}
	if policer.CBS != 10000000 {
		t.Errorf("CBS = %d, want %d", policer.CBS, 10000000)
	}
	if policer.MeterType != "sr_tcm" {
		t.Errorf("MeterType = %q, want %q", policer.MeterType, "sr_tcm")
	}
	if policer.Mode != "color-blind" {
		t.Errorf("Mode = %q, want %q", policer.Mode, "color-blind")
	}
	if policer.RedAction != "drop" {
		t.Errorf("RedAction = %q, want %q", policer.RedAction, "drop")
	}
}

func TestParseBandwidth(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"100g", 100000000000, false},
		{"10gbps", 10000000000, false},
		{"100m", 100000000, false},
		{"100mbps", 100000000, false},
		{"64k", 64000, false},
		{"64kbps", 64000, false},
		{"1000bps", 1000, false},
		{"1000", 1000, false},
		{"1.5g", 1500000000, false},
		{"", 0, true},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseBandwidth(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if got != tt.expected {
					t.Errorf("ParseBandwidth(%q) = %d, want %d", tt.input, got, tt.expected)
				}
			}
		})
	}
}

func TestFormatBandwidth(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{100000000000, "100.0g"},
		{1000000000, "1.0g"},
		{100000000, "100.0m"},
		{1000000, "1.0m"},
		{64000, "64.0k"},
		{1000, "1.0k"},
		{500, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := FormatBandwidth(tt.input)
			if got != tt.expected {
				t.Errorf("FormatBandwidth(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNewScheduler(t *testing.T) {
	scheduler := NewScheduler("be-8q", 0, "DWRR")

	if scheduler.Name != "be-8q" {
		t.Errorf("Name = %q, want %q", scheduler.Name, "be-8q")
	}
	if scheduler.Queue != 0 {
		t.Errorf("Queue = %d, want %d", scheduler.Queue, 0)
	}
	if scheduler.Type != "DWRR" {
		t.Errorf("Type = %q, want %q", scheduler.Type, "DWRR")
	}
}

func TestNewDropProfile(t *testing.T) {
	profile := NewDropProfile("red-tcp", 1000, 2000, 5)

	if profile.Name != "red-tcp" {
		t.Errorf("Name = %q, want %q", profile.Name, "red-tcp")
	}
	if profile.MinThreshold != 1000 {
		t.Errorf("MinThreshold = %d, want %d", profile.MinThreshold, 1000)
	}
	if profile.MaxThreshold != 2000 {
		t.Errorf("MaxThreshold = %d, want %d", profile.MaxThreshold, 2000)
	}
	if profile.DropProbability != 5 {
		t.Errorf("DropProbability = %d, want %d", profile.DropProbability, 5)
	}
}

func TestDSCPConstants(t *testing.T) {
	// Verify some key DSCP values
	if DSCPBestEffort != 0 {
		t.Errorf("DSCPBestEffort = %d, want %d", DSCPBestEffort, 0)
	}
	if DSCPEf != 46 {
		t.Errorf("DSCPEf = %d, want %d", DSCPEf, 46)
	}
	if DSCPCs6 != 48 {
		t.Errorf("DSCPCs6 = %d, want %d", DSCPCs6, 48)
	}
	if DSCPCs7 != 56 {
		t.Errorf("DSCPCs7 = %d, want %d", DSCPCs7, 56)
	}
}

func TestStandard8QueueWeights(t *testing.T) {
	// Verify queue weights add up to 100
	total := 0
	for _, weight := range Standard8QueueWeights {
		total += weight
	}
	if total != 100 {
		t.Errorf("Total queue weights = %d, want %d", total, 100)
	}

	// Verify all 8 queues are defined
	if len(Standard8QueueWeights) != 8 {
		t.Errorf("Queue count = %d, want %d", len(Standard8QueueWeights), 8)
	}
}

// ===================== Policy Tests =====================

func TestNewRoutingPolicy(t *testing.T) {
	policy := NewRoutingPolicy("IMPORT-POLICY")

	if policy.Name != "IMPORT-POLICY" {
		t.Errorf("Name = %q, want %q", policy.Name, "IMPORT-POLICY")
	}
	if len(policy.Statements) != 0 {
		t.Errorf("Statements count = %d, want %d", len(policy.Statements), 0)
	}
}

func TestRoutingPolicy_AddStatement(t *testing.T) {
	policy := NewRoutingPolicy("TEST")

	// Add statements in non-sequence order
	policy.AddStatement(&PolicyStatement{Name: "STMT_200", Sequence: 200})
	policy.AddStatement(&PolicyStatement{Name: "STMT_100", Sequence: 100})
	policy.AddStatement(&PolicyStatement{Name: "STMT_150", Sequence: 150})

	if len(policy.Statements) != 3 {
		t.Errorf("Statements count = %d, want %d", len(policy.Statements), 3)
	}

	// Check sequence order (lowest first)
	if policy.Statements[0].Sequence != 100 {
		t.Errorf("First statement sequence = %d, want %d", policy.Statements[0].Sequence, 100)
	}
	if policy.Statements[1].Sequence != 150 {
		t.Errorf("Second statement sequence = %d, want %d", policy.Statements[1].Sequence, 150)
	}
	if policy.Statements[2].Sequence != 200 {
		t.Errorf("Third statement sequence = %d, want %d", policy.Statements[2].Sequence, 200)
	}
}

func TestNewPrefixList(t *testing.T) {
	pl := NewPrefixList("RFC1918", "ipv4")

	if pl.Name != "RFC1918" {
		t.Errorf("Name = %q, want %q", pl.Name, "RFC1918")
	}
	if pl.Family != "ipv4" {
		t.Errorf("Family = %q, want %q", pl.Family, "ipv4")
	}
}

func TestPrefixList_AddEntry(t *testing.T) {
	pl := NewPrefixList("TEST", "ipv4")

	// Add entries in non-sequence order
	pl.AddEntry(&PrefixListEntry{Sequence: 20, Action: "permit", Prefix: "10.0.0.0/8"})
	pl.AddEntry(&PrefixListEntry{Sequence: 10, Action: "permit", Prefix: "172.16.0.0/12"})
	pl.AddEntry(&PrefixListEntry{Sequence: 15, Action: "permit", Prefix: "192.168.0.0/16"})

	if len(pl.Entries) != 3 {
		t.Errorf("Entries count = %d, want %d", len(pl.Entries), 3)
	}

	// Check sequence order
	if pl.Entries[0].Sequence != 10 {
		t.Errorf("First entry sequence = %d, want %d", pl.Entries[0].Sequence, 10)
	}
}

func TestPrefixList_GetEntry(t *testing.T) {
	pl := NewPrefixList("TEST", "ipv4")
	pl.AddEntry(&PrefixListEntry{Sequence: 10, Action: "permit", Prefix: "10.0.0.0/8"})

	entry := pl.GetEntry(10)
	if entry == nil {
		t.Error("GetEntry() should not return nil")
	}
	if entry.Prefix != "10.0.0.0/8" {
		t.Errorf("Entry prefix = %q, want %q", entry.Prefix, "10.0.0.0/8")
	}

	if pl.GetEntry(20) != nil {
		t.Error("GetEntry() should return nil for non-existent")
	}
}
