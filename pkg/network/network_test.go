package network

import (
	"strings"
	"testing"
	"time"

	"github.com/newtron-network/newtron/pkg/spec"
)

// ============================================================================
// ChangeType Tests
// ============================================================================

func TestChangeTypeConstants(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		expected string
	}{
		{ChangeAdd, "add"},
		{ChangeModify, "modify"},
		{ChangeDelete, "delete"},
	}

	for _, tt := range tests {
		if string(tt.ct) != tt.expected {
			t.Errorf("ChangeType %v = %q, want %q", tt.ct, string(tt.ct), tt.expected)
		}
	}
}

// ============================================================================
// Change Tests
// ============================================================================

func TestChange_Structure(t *testing.T) {
	c := Change{
		Table:    "PORT",
		Key:      "Ethernet0",
		Type:     ChangeModify,
		OldValue: map[string]string{"mtu": "1500"},
		NewValue: map[string]string{"mtu": "9100"},
	}

	if c.Table != "PORT" {
		t.Errorf("Table = %q, want %q", c.Table, "PORT")
	}
	if c.Key != "Ethernet0" {
		t.Errorf("Key = %q, want %q", c.Key, "Ethernet0")
	}
	if c.Type != ChangeModify {
		t.Errorf("Type = %q, want %q", c.Type, ChangeModify)
	}
	if c.OldValue["mtu"] != "1500" {
		t.Errorf("OldValue[mtu] = %q, want %q", c.OldValue["mtu"], "1500")
	}
	if c.NewValue["mtu"] != "9100" {
		t.Errorf("NewValue[mtu] = %q, want %q", c.NewValue["mtu"], "9100")
	}
}

func TestChange_NilValues(t *testing.T) {
	c := Change{
		Table: "VRF",
		Key:   "Vrf_CUST1",
		Type:  ChangeDelete,
	}

	if c.OldValue != nil {
		t.Error("OldValue should be nil")
	}
	if c.NewValue != nil {
		t.Error("NewValue should be nil")
	}
}

// ============================================================================
// ChangeSet Tests
// ============================================================================

func TestNewChangeSet(t *testing.T) {
	cs := NewChangeSet("leaf1-ny", "service.apply")

	if cs.Device != "leaf1-ny" {
		t.Errorf("Device = %q, want %q", cs.Device, "leaf1-ny")
	}
	if cs.Operation != "service.apply" {
		t.Errorf("Operation = %q, want %q", cs.Operation, "service.apply")
	}
	if len(cs.Changes) != 0 {
		t.Errorf("Changes count = %d, want %d", len(cs.Changes), 0)
	}
	if cs.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestChangeSet_Timestamp(t *testing.T) {
	before := time.Now()
	cs := NewChangeSet("test", "test")
	after := time.Now()

	if cs.Timestamp.Before(before) || cs.Timestamp.After(after) {
		t.Errorf("Timestamp %v should be between %v and %v", cs.Timestamp, before, after)
	}
}

func TestChangeSet_Add(t *testing.T) {
	cs := NewChangeSet("test", "test")

	cs.Add("PORT", "Ethernet0", ChangeAdd, nil, map[string]string{"mtu": "9100"})

	if len(cs.Changes) != 1 {
		t.Fatalf("Changes count = %d, want %d", len(cs.Changes), 1)
	}

	c := cs.Changes[0]
	if c.Table != "PORT" {
		t.Errorf("Table = %q, want %q", c.Table, "PORT")
	}
	if c.Key != "Ethernet0" {
		t.Errorf("Key = %q, want %q", c.Key, "Ethernet0")
	}
	if c.Type != ChangeAdd {
		t.Errorf("Type = %q, want %q", c.Type, ChangeAdd)
	}
	if c.OldValue != nil {
		t.Error("OldValue should be nil")
	}
	if c.NewValue == nil {
		t.Error("NewValue should not be nil")
	}
	if c.NewValue["mtu"] != "9100" {
		t.Errorf("NewValue[mtu] = %q, want %q", c.NewValue["mtu"], "9100")
	}
}

func TestChangeSet_AddMultiple(t *testing.T) {
	cs := NewChangeSet("leaf1-ny", "service.apply")

	// Add typical service apply changes
	cs.Add("VRF", "customer-l3-Ethernet0", ChangeAdd, nil, map[string]string{
		"vni": "10001",
	})
	cs.Add("INTERFACE", "Ethernet0", ChangeModify, nil, map[string]string{
		"vrf_name": "customer-l3-Ethernet0",
	})
	cs.Add("INTERFACE", "Ethernet0|10.1.1.1/30", ChangeAdd, nil, nil)
	cs.Add("ACL_TABLE", "customer-l3-in", ChangeAdd, nil, map[string]string{
		"type":  "L3",
		"stage": "ingress",
		"ports": "Ethernet0",
	})
	cs.Add("ACL_RULE", "customer-l3-in|RULE_100", ChangeAdd, nil, map[string]string{
		"packet_action": "FORWARD",
	})

	if len(cs.Changes) != 5 {
		t.Errorf("Changes count = %d, want %d", len(cs.Changes), 5)
	}

	// Verify ordering is preserved
	if cs.Changes[0].Table != "VRF" {
		t.Error("First change should be VRF")
	}
	if cs.Changes[4].Table != "ACL_RULE" {
		t.Error("Last change should be ACL_RULE")
	}
}

func TestChangeSet_IsEmpty(t *testing.T) {
	cs := NewChangeSet("test", "test")

	if !cs.IsEmpty() {
		t.Error("New ChangeSet should be empty")
	}

	cs.Add("PORT", "Ethernet0", ChangeAdd, nil, nil)

	if cs.IsEmpty() {
		t.Error("ChangeSet with changes should not be empty")
	}
}

func TestChangeSet_String_Empty(t *testing.T) {
	cs := NewChangeSet("test", "test")
	str := cs.String()

	if str != "No changes" {
		t.Errorf("String() = %q, want %q", str, "No changes")
	}
}

func TestChangeSet_String_WithChanges(t *testing.T) {
	cs := NewChangeSet("test", "test")
	cs.Add("PORT", "Ethernet0", ChangeAdd, nil, map[string]string{"mtu": "9100"})
	cs.Add("VLAN", "Vlan100", ChangeModify, nil, map[string]string{"vlanid": "100"})
	cs.Add("VRF", "Vrf_CUST", ChangeDelete, nil, nil)

	str := cs.String()

	if !strings.Contains(str, "[ADD]") {
		t.Error("String should contain [ADD]")
	}
	if !strings.Contains(str, "[MOD]") {
		t.Error("String should contain [MOD]")
	}
	if !strings.Contains(str, "[DEL]") {
		t.Error("String should contain [DEL]")
	}
	if !strings.Contains(str, "PORT|Ethernet0") {
		t.Error("String should contain PORT|Ethernet0")
	}
	if !strings.Contains(str, "VLAN|Vlan100") {
		t.Error("String should contain VLAN|Vlan100")
	}
	if !strings.Contains(str, "VRF|Vrf_CUST") {
		t.Error("String should contain VRF|Vrf_CUST")
	}
}

func TestChangeSet_String_ShowsNewValue(t *testing.T) {
	cs := NewChangeSet("test", "test")
	cs.Add("PORT", "Ethernet0", ChangeAdd, nil, map[string]string{"mtu": "9100"})

	str := cs.String()

	// Should show new value in the output
	if !strings.Contains(str, "mtu") {
		t.Error("String should show new value contents")
	}
}

func TestChangeSet_Preview(t *testing.T) {
	cs := NewChangeSet("leaf1-ny", "vlan.create")
	cs.Add("VLAN", "Vlan100", ChangeAdd, nil, map[string]string{"vlanid": "100"})

	preview := cs.Preview()

	if !strings.Contains(preview, "Operation: vlan.create") {
		t.Error("Preview should contain operation")
	}
	if !strings.Contains(preview, "Device: leaf1-ny") {
		t.Error("Preview should contain device")
	}
	if !strings.Contains(preview, "[ADD]") {
		t.Error("Preview should contain change details")
	}
	if !strings.Contains(preview, "VLAN|Vlan100") {
		t.Error("Preview should contain VLAN|Vlan100")
	}
}

// ============================================================================
// splitConfigDBKey Tests
// ============================================================================

func TestSplitConfigDBKey(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"PortChannel100|Ethernet0", []string{"PortChannel100", "Ethernet0"}},
		{"Vlan100|Ethernet4", []string{"Vlan100", "Ethernet4"}},
		{"INTERFACE|Ethernet0|10.1.1.1/30", []string{"INTERFACE", "Ethernet0|10.1.1.1/30"}},
		{"Ethernet0", []string{"Ethernet0"}},
		{"", []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitConfigDBKey(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitConfigDBKey(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.expected, len(tt.expected))
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("splitConfigDBKey(%q)[%d] = %q, want %q",
						tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// ============================================================================
// VLANInfo Tests
// ============================================================================

func TestVLANInfo_Structure(t *testing.T) {
	info := VLANInfo{
		ID:        100,
		Name:      "ServerVLAN",
		Members:   []string{"Ethernet0", "Ethernet4(t)"},
		SVIStatus: "up",
	}

	if info.ID != 100 {
		t.Errorf("ID = %d, want %d", info.ID, 100)
	}
	if info.Name != "ServerVLAN" {
		t.Errorf("Name = %q, want %q", info.Name, "ServerVLAN")
	}
	if len(info.Members) != 2 {
		t.Errorf("Members count = %d, want %d", len(info.Members), 2)
	}
	if info.SVIStatus != "up" {
		t.Errorf("SVIStatus = %q, want %q", info.SVIStatus, "up")
	}
}

func TestVLANInfo_L2VNI(t *testing.T) {
	t.Run("with MACVPNInfo", func(t *testing.T) {
		info := VLANInfo{
			ID: 100,
			MACVPNInfo: &MACVPNInfo{
				L2VNI: 10100,
			},
		}
		if info.L2VNI() != 10100 {
			t.Errorf("L2VNI() = %d, want %d", info.L2VNI(), 10100)
		}
	})

	t.Run("without MACVPNInfo", func(t *testing.T) {
		info := VLANInfo{ID: 100}
		if info.L2VNI() != 0 {
			t.Errorf("L2VNI() = %d, want %d", info.L2VNI(), 0)
		}
	})
}

// ============================================================================
// MACVPNInfo Tests
// ============================================================================

func TestMACVPNInfo_Structure(t *testing.T) {
	info := MACVPNInfo{
		Name:           "server-vlan-evpn",
		L2VNI:          10100,
		ARPSuppression: true,
	}

	if info.Name != "server-vlan-evpn" {
		t.Errorf("Name = %q, want %q", info.Name, "server-vlan-evpn")
	}
	if info.L2VNI != 10100 {
		t.Errorf("L2VNI = %d, want %d", info.L2VNI, 10100)
	}
	if !info.ARPSuppression {
		t.Error("ARPSuppression should be true")
	}
}

// ============================================================================
// VRFInfo Tests
// ============================================================================

func TestVRFInfo_Structure(t *testing.T) {
	info := VRFInfo{
		Name:       "Vrf_CUST1",
		L3VNI:      10001,
		Interfaces: []string{"Ethernet0", "Ethernet4", "Vlan100"},
	}

	if info.Name != "Vrf_CUST1" {
		t.Errorf("Name = %q, want %q", info.Name, "Vrf_CUST1")
	}
	if info.L3VNI != 10001 {
		t.Errorf("L3VNI = %d, want %d", info.L3VNI, 10001)
	}
	if len(info.Interfaces) != 3 {
		t.Errorf("Interfaces count = %d, want %d", len(info.Interfaces), 3)
	}
}

// ============================================================================
// PortChannelInfo Tests
// ============================================================================

func TestPortChannelInfo_Structure(t *testing.T) {
	info := PortChannelInfo{
		Name:          "PortChannel100",
		Members:       []string{"Ethernet0", "Ethernet4"},
		ActiveMembers: []string{"Ethernet0", "Ethernet4"},
		AdminStatus:   "up",
	}

	if info.Name != "PortChannel100" {
		t.Errorf("Name = %q, want %q", info.Name, "PortChannel100")
	}
	if len(info.Members) != 2 {
		t.Errorf("Members count = %d, want %d", len(info.Members), 2)
	}
	if len(info.ActiveMembers) != 2 {
		t.Errorf("ActiveMembers count = %d, want %d", len(info.ActiveMembers), 2)
	}
	if info.AdminStatus != "up" {
		t.Errorf("AdminStatus = %q, want %q", info.AdminStatus, "up")
	}
}

// ============================================================================
// ACLTableInfo Tests
// ============================================================================

func TestACLTableInfo_Structure(t *testing.T) {
	info := ACLTableInfo{
		Name:            "customer-edge-in",
		Type:            "L3",
		Stage:           "ingress",
		BoundInterfaces: "Ethernet0,Ethernet4",
		Policy:          "Customer edge ingress filter",
	}

	if info.Name != "customer-edge-in" {
		t.Errorf("Name = %q, want %q", info.Name, "customer-edge-in")
	}
	if info.Type != "L3" {
		t.Errorf("Type = %q, want %q", info.Type, "L3")
	}
	if info.Stage != "ingress" {
		t.Errorf("Stage = %q, want %q", info.Stage, "ingress")
	}
	if info.BoundInterfaces != "Ethernet0,Ethernet4" {
		t.Errorf("BoundInterfaces = %q, want %q", info.BoundInterfaces, "Ethernet0,Ethernet4")
	}
	if info.Policy != "Customer edge ingress filter" {
		t.Errorf("Policy = %q, want %q", info.Policy, "Customer edge ingress filter")
	}
}

// ============================================================================
// Interface Type Detection Tests (using minimal mock)
// ============================================================================

func TestInterface_TypeDetection(t *testing.T) {
	tests := []struct {
		name          string
		isPhysical    bool
		isPortChannel bool
		isVLAN        bool
		isLoopback    bool
	}{
		{"Ethernet0", true, false, false, false},
		{"Ethernet48", true, false, false, false},
		{"PortChannel100", false, true, false, false},
		{"PortChannel1", false, true, false, false},
		{"Vlan100", false, false, true, false},
		{"Vlan1", false, false, true, false},
		{"Loopback0", false, false, false, true},
		{"Loopback1", false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal interface with just the name
			intf := &Interface{name: tt.name}

			if intf.IsPhysical() != tt.isPhysical {
				t.Errorf("IsPhysical() = %v, want %v", intf.IsPhysical(), tt.isPhysical)
			}
			if intf.IsPortChannel() != tt.isPortChannel {
				t.Errorf("IsPortChannel() = %v, want %v", intf.IsPortChannel(), tt.isPortChannel)
			}
			if intf.IsVLAN() != tt.isVLAN {
				t.Errorf("IsVLAN() = %v, want %v", intf.IsVLAN(), tt.isVLAN)
			}
			if intf.IsLoopback() != tt.isLoopback {
				t.Errorf("IsLoopback() = %v, want %v", intf.IsLoopback(), tt.isLoopback)
			}
		})
	}
}

func TestInterface_Name(t *testing.T) {
	intf := &Interface{name: "Ethernet0"}
	if intf.Name() != "Ethernet0" {
		t.Errorf("Name() = %q, want %q", intf.Name(), "Ethernet0")
	}
}

func TestInterface_Properties(t *testing.T) {
	intf := &Interface{
		name:        "Ethernet0",
		adminStatus: "up",
		operStatus:  "up",
		speed:       "100G",
		mtu:         9100,
		vrf:         "Vrf_CUST1",
		ipAddresses: []string{"10.1.1.1/30"},
	}

	if intf.AdminStatus() != "up" {
		t.Errorf("AdminStatus() = %q, want %q", intf.AdminStatus(), "up")
	}
	if intf.OperStatus() != "up" {
		t.Errorf("OperStatus() = %q, want %q", intf.OperStatus(), "up")
	}
	if intf.Speed() != "100G" {
		t.Errorf("Speed() = %q, want %q", intf.Speed(), "100G")
	}
	if intf.MTU() != 9100 {
		t.Errorf("MTU() = %d, want %d", intf.MTU(), 9100)
	}
	if intf.VRF() != "Vrf_CUST1" {
		t.Errorf("VRF() = %q, want %q", intf.VRF(), "Vrf_CUST1")
	}
	if len(intf.IPAddresses()) != 1 || intf.IPAddresses()[0] != "10.1.1.1/30" {
		t.Errorf("IPAddresses() = %v, want [10.1.1.1/30]", intf.IPAddresses())
	}
}

// ============================================================================
// Interface Service Binding Tests
// ============================================================================

func TestInterface_HasService(t *testing.T) {
	t.Run("with service", func(t *testing.T) {
		intf := &Interface{serviceName: "customer-l3"}
		if !intf.HasService() {
			t.Error("HasService() should be true")
		}
		if intf.ServiceName() != "customer-l3" {
			t.Errorf("ServiceName() = %q, want %q", intf.ServiceName(), "customer-l3")
		}
	})

	t.Run("without service", func(t *testing.T) {
		intf := &Interface{}
		if intf.HasService() {
			t.Error("HasService() should be false")
		}
		if intf.ServiceName() != "" {
			t.Errorf("ServiceName() = %q, want empty", intf.ServiceName())
		}
	})
}

func TestInterface_ServiceBindingProperties(t *testing.T) {
	intf := &Interface{
		serviceName:   "customer-l3",
		serviceIP:     "10.1.1.1/30",
		serviceVRF:    "customer-l3-Ethernet0",
		serviceIPVPN:  "mgmt-spoke-global",
		serviceMACVPN: "server-vlan",
		ingressACL:    "customer-edge-in",
		egressACL:     "customer-edge-out",
	}

	if intf.ServiceIP() != "10.1.1.1/30" {
		t.Errorf("ServiceIP() = %q, want %q", intf.ServiceIP(), "10.1.1.1/30")
	}
	if intf.ServiceVRF() != "customer-l3-Ethernet0" {
		t.Errorf("ServiceVRF() = %q, want %q", intf.ServiceVRF(), "customer-l3-Ethernet0")
	}
	if intf.ServiceIPVPN() != "mgmt-spoke-global" {
		t.Errorf("ServiceIPVPN() = %q, want %q", intf.ServiceIPVPN(), "mgmt-spoke-global")
	}
	if intf.ServiceMACVPN() != "server-vlan" {
		t.Errorf("ServiceMACVPN() = %q, want %q", intf.ServiceMACVPN(), "server-vlan")
	}
	if intf.IngressACL() != "customer-edge-in" {
		t.Errorf("IngressACL() = %q, want %q", intf.IngressACL(), "customer-edge-in")
	}
	if intf.EgressACL() != "customer-edge-out" {
		t.Errorf("EgressACL() = %q, want %q", intf.EgressACL(), "customer-edge-out")
	}
}

// ============================================================================
// Interface LAG Membership Tests
// ============================================================================

func TestInterface_LAGMembership(t *testing.T) {
	t.Run("is member", func(t *testing.T) {
		intf := &Interface{
			name:      "Ethernet0",
			lagMember: "PortChannel100",
		}
		if !intf.IsLAGMember() {
			t.Error("IsLAGMember() should be true")
		}
		if intf.LAGParent() != "PortChannel100" {
			t.Errorf("LAGParent() = %q, want %q", intf.LAGParent(), "PortChannel100")
		}
	})

	t.Run("not member", func(t *testing.T) {
		intf := &Interface{name: "Ethernet0"}
		if intf.IsLAGMember() {
			t.Error("IsLAGMember() should be false")
		}
		if intf.LAGParent() != "" {
			t.Errorf("LAGParent() = %q, want empty", intf.LAGParent())
		}
	})
}

// ============================================================================
// Interface extractServiceFromACL Tests
// ============================================================================

func TestInterface_extractServiceFromACL(t *testing.T) {
	intf := &Interface{}

	tests := []struct {
		aclName  string
		expected string
	}{
		{"customer-edge-in", "customer-edge"},
		{"customer-edge-out", "customer-edge"},
		{"transit-protect-in", "transit-protect"},
		{"simple-out", "simple"},
		{"no-suffix", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.aclName, func(t *testing.T) {
			got := intf.extractServiceFromACL(tt.aclName)
			if got != tt.expected {
				t.Errorf("extractServiceFromACL(%q) = %q, want %q",
					tt.aclName, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// Interface String Tests
// ============================================================================

func TestInterface_String(t *testing.T) {
	t.Run("basic up interface", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "up",
		}
		str := intf.String()
		if !strings.Contains(str, "Ethernet0") {
			t.Error("String should contain interface name")
		}
		if !strings.Contains(str, "up") {
			t.Error("String should contain status")
		}
	})

	t.Run("interface with service", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "up",
			serviceName: "customer-l3",
		}
		str := intf.String()
		if !strings.Contains(str, "[service: customer-l3]") {
			t.Error("String should contain service info")
		}
	})

	t.Run("interface with IP", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "up",
			ipAddresses: []string{"10.1.1.1/30"},
		}
		str := intf.String()
		if !strings.Contains(str, "[ip: 10.1.1.1/30]") {
			t.Error("String should contain IP info")
		}
	})

	t.Run("interface with VRF", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "up",
			vrf:         "Vrf_CUST1",
		}
		str := intf.String()
		if !strings.Contains(str, "[vrf: Vrf_CUST1]") {
			t.Error("String should contain VRF info")
		}
	})

	t.Run("LAG member", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "up",
			lagMember:   "PortChannel100",
		}
		str := intf.String()
		if !strings.Contains(str, "[member of: PortChannel100]") {
			t.Error("String should contain LAG membership info")
		}
	})

	t.Run("admin up oper down", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "up",
			operStatus:  "down",
		}
		str := intf.String()
		if !strings.Contains(str, "admin-up/oper-down") {
			t.Error("String should show admin-up/oper-down status")
		}
	})

	t.Run("down interface", func(t *testing.T) {
		intf := &Interface{
			name:        "Ethernet0",
			adminStatus: "down",
			operStatus:  "down",
		}
		str := intf.String()
		if !strings.Contains(str, "(down)") {
			t.Error("String should show down status")
		}
	})
}

// ============================================================================
// Device Property Tests (minimal, no connection required)
// ============================================================================

func TestDevice_Name(t *testing.T) {
	dev := &Device{name: "leaf1-ny"}
	if dev.Name() != "leaf1-ny" {
		t.Errorf("Name() = %q, want %q", dev.Name(), "leaf1-ny")
	}
}

func TestDevice_IsConnected_NotConnected(t *testing.T) {
	dev := &Device{connected: false}
	if dev.IsConnected() {
		t.Error("IsConnected() should be false")
	}
}

func TestDevice_IsLocked_NotLocked(t *testing.T) {
	dev := &Device{locked: false}
	if dev.IsLocked() {
		t.Error("IsLocked() should be false")
	}
}

// ============================================================================
// Network ListServices/ListFilterSpecs Tests (minimal)
// ============================================================================

func TestNetwork_ListServicesEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: make(map[string]*spec.ServiceSpec),
		},
	}
	services := n.ListServices()
	if len(services) != 0 {
		t.Errorf("ListServices() = %v, want empty", services)
	}
}

func TestNetwork_ListFilterSpecsEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			FilterSpecs: make(map[string]*spec.FilterSpec),
		},
	}
	filters := n.ListFilterSpecs()
	if len(filters) != 0 {
		t.Errorf("ListFilterSpecs() = %v, want empty", filters)
	}
}

