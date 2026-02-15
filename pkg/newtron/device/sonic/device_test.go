package sonic

import (
	"encoding/json"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device"
)

func TestConfigDB_JSONSerialization(t *testing.T) {
	// Test that ConfigDB can be serialized and deserialized
	cfg := &ConfigDB{
		Port: map[string]PortEntry{
			"Ethernet0": {
				AdminStatus: "up",
				MTU:         "9100",
				Speed:       "100G",
			},
		},
		VLAN: map[string]VLANEntry{
			"Vlan100": {
				VLANID:      "100",
				Description: "Test VLAN",
			},
		},
		VRF: map[string]VRFEntry{
			"Vrf_CUST1": {
				VNI: "10001",
			},
		},
	}

	// Serialize
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Deserialize
	var decoded ConfigDB
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify
	if port, ok := decoded.Port["Ethernet0"]; !ok {
		t.Error("Port Ethernet0 not found")
	} else if port.MTU != "9100" {
		t.Errorf("Port MTU = %q, want %q", port.MTU, "9100")
	}

	if vlan, ok := decoded.VLAN["Vlan100"]; !ok {
		t.Error("VLAN Vlan100 not found")
	} else if vlan.VLANID != "100" {
		t.Errorf("VLAN ID = %q, want %q", vlan.VLANID, "100")
	}

	if vrf, ok := decoded.VRF["Vrf_CUST1"]; !ok {
		t.Error("VRF Vrf_CUST1 not found")
	} else if vrf.VNI != "10001" {
		t.Errorf("VRF VNI = %q, want %q", vrf.VNI, "10001")
	}
}

func TestPortEntry_Structure(t *testing.T) {
	port := PortEntry{
		AdminStatus: "up",
		Alias:       "eth0",
		Description: "Uplink to spine",
		FEC:         "rs",
		Index:       "0",
		Lanes:       "1,2,3,4",
		MTU:         "9100",
		Speed:       "100000",
		Autoneg:     "off",
	}

	if port.AdminStatus != "up" {
		t.Errorf("AdminStatus = %q", port.AdminStatus)
	}
	if port.FEC != "rs" {
		t.Errorf("FEC = %q", port.FEC)
	}
	if port.Speed != "100000" {
		t.Errorf("Speed = %q", port.Speed)
	}
}

func TestVLANEntry_Structure(t *testing.T) {
	vlan := VLANEntry{
		VLANID:      "100",
		Description: "Servers",
		MTU:         "9000",
		AdminStatus: "up",
		DHCPServers: "10.1.1.1,10.1.1.2",
	}

	if vlan.VLANID != "100" {
		t.Errorf("VLANID = %q", vlan.VLANID)
	}
	if vlan.DHCPServers != "10.1.1.1,10.1.1.2" {
		t.Errorf("DHCPServers = %q", vlan.DHCPServers)
	}
}

func TestVLANMemberEntry_Structure(t *testing.T) {
	member := VLANMemberEntry{
		TaggingMode: "tagged",
	}

	if member.TaggingMode != "tagged" {
		t.Errorf("TaggingMode = %q", member.TaggingMode)
	}

	member2 := VLANMemberEntry{
		TaggingMode: "untagged",
	}

	if member2.TaggingMode != "untagged" {
		t.Errorf("TaggingMode = %q", member2.TaggingMode)
	}
}

func TestInterfaceEntry_Structure(t *testing.T) {
	intf := InterfaceEntry{
		VRFName:     "Vrf_CUST1",
		NATZone:     "0",
		ProxyArp:    "enabled",
		MPLSEnabled: "false",
	}

	if intf.VRFName != "Vrf_CUST1" {
		t.Errorf("VRFName = %q", intf.VRFName)
	}
}

func TestPortChannelEntry_Structure(t *testing.T) {
	pc := PortChannelEntry{
		AdminStatus: "up",
		MTU:         "9100",
		MinLinks:    "1",
		Fallback:    "true",
		FastRate:    "true",
		LACPKey:     "auto",
		Description: "MLAG peer link",
	}

	if pc.MinLinks != "1" {
		t.Errorf("MinLinks = %q", pc.MinLinks)
	}
	if pc.FastRate != "true" {
		t.Errorf("FastRate = %q", pc.FastRate)
	}
}

func TestVRFEntry_Structure(t *testing.T) {
	vrf := VRFEntry{
		VNI:      "10001",
		Fallback: "false",
	}

	if vrf.VNI != "10001" {
		t.Errorf("VNI = %q", vrf.VNI)
	}
}

func TestVXLANTunnelEntry_Structure(t *testing.T) {
	vtep := VXLANTunnelEntry{
		SrcIP: "10.0.0.1",
	}

	if vtep.SrcIP != "10.0.0.1" {
		t.Errorf("SrcIP = %q", vtep.SrcIP)
	}
}

func TestVXLANMapEntry_Structure(t *testing.T) {
	mapping := VXLANMapEntry{
		VLAN: "Vlan100",
		VNI:  "10100",
	}

	if mapping.VLAN != "Vlan100" {
		t.Errorf("VLAN = %q", mapping.VLAN)
	}

	mapping2 := VXLANMapEntry{
		VRF: "Vrf_CUST1",
		VNI: "10001",
	}

	if mapping2.VRF != "Vrf_CUST1" {
		t.Errorf("VRF = %q", mapping2.VRF)
	}
}

func TestEVPNNVOEntry_Structure(t *testing.T) {
	nvo := EVPNNVOEntry{
		SourceVTEP: "vtep1",
	}

	if nvo.SourceVTEP != "vtep1" {
		t.Errorf("SourceVTEP = %q", nvo.SourceVTEP)
	}
}

func TestBGPGlobalsEntry_Structure(t *testing.T) {
	bgp := BGPGlobalsEntry{
		RouterID:        "10.0.0.1",
		LocalASN:        "65000",
		GracefulRestart: "true",
	}

	if bgp.RouterID != "10.0.0.1" {
		t.Errorf("RouterID = %q", bgp.RouterID)
	}
	if bgp.LocalASN != "65000" {
		t.Errorf("LocalASN = %q", bgp.LocalASN)
	}
}

func TestBGPNeighborEntry_Structure(t *testing.T) {
	neighbor := BGPNeighborEntry{
		LocalAddr:     "10.0.0.1",
		Name:          "spine1",
		ASN:           "65000",
		HoldTime:      "180",
		KeepaliveTime: "60",
		AdminStatus:   "up",
	}

	if neighbor.ASN != "65000" {
		t.Errorf("ASN = %q", neighbor.ASN)
	}
	if neighbor.HoldTime != "180" {
		t.Errorf("HoldTime = %q", neighbor.HoldTime)
	}
}

func TestACLTableEntry_JSON(t *testing.T) {
	aclJSON := `{
		"policy_desc": "Data plane ACL",
		"type": "L3",
		"stage": "ingress",
		"ports": "Ethernet0,Ethernet4"
	}`

	var acl ACLTableEntry
	if err := json.Unmarshal([]byte(aclJSON), &acl); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if acl.Type != "L3" {
		t.Errorf("Type = %q", acl.Type)
	}
	if acl.Stage != "ingress" {
		t.Errorf("Stage = %q", acl.Stage)
	}
	if acl.Ports != "Ethernet0,Ethernet4" {
		t.Errorf("Ports = %q", acl.Ports)
	}
}

func TestServiceBindingEntry_Structure(t *testing.T) {
	binding := ServiceBindingEntry{
		ServiceName: "customer-l3",
		IPAddress:   "10.1.1.1/30",
		VRFName:     "customer-l3-Ethernet0",
		IPVPN:       "customer-vpn",
		IngressACL:  "customer-l3-Ethernet0-in",
		EgressACL:   "customer-l3-Ethernet0-out",
		AppliedAt:   "2024-01-15T10:30:00Z",
		AppliedBy:   "admin",
	}

	if binding.ServiceName != "customer-l3" {
		t.Errorf("ServiceName = %q", binding.ServiceName)
	}
	if binding.VRFName != "customer-l3-Ethernet0" {
		t.Errorf("VRFName = %q", binding.VRFName)
	}
	if binding.IngressACL != "customer-l3-Ethernet0-in" {
		t.Errorf("IngressACL = %q", binding.IngressACL)
	}
}

func TestInterfaceState_Structure(t *testing.T) {
	intf := device.InterfaceState{
		Name:        "Ethernet0",
		AdminStatus: "up",
		OperStatus:  "up",
		Speed:       "100G",
		MTU:         9100,
		VRF:         "Vrf_CUST1",
		IPAddresses: []string{"10.1.1.1/30"},
		LAGMember:   "",
		IngressACL:  "ACL1",
		EgressACL:   "ACL2",
		Service:     "customer-l3",
	}

	if intf.Name != "Ethernet0" {
		t.Errorf("Name = %q", intf.Name)
	}
	if len(intf.IPAddresses) != 1 {
		t.Errorf("IPAddresses count = %d", len(intf.IPAddresses))
	}
	if intf.Service != "customer-l3" {
		t.Errorf("Service = %q", intf.Service)
	}
}

func TestPortChannelState_Structure(t *testing.T) {
	pc := device.PortChannelState{
		Name:          "PortChannel100",
		AdminStatus:   "up",
		Members:       []string{"Ethernet0", "Ethernet4"},
		ActiveMembers: []string{"Ethernet0", "Ethernet4"},
	}

	if pc.Name != "PortChannel100" {
		t.Errorf("Name = %q", pc.Name)
	}
	if len(pc.Members) != 2 {
		t.Errorf("Members count = %d", len(pc.Members))
	}
}

func TestVLANState_Structure(t *testing.T) {
	vlan := device.VLANState{
		ID:         100,
		Name:       "Vlan100",
		OperStatus: "up",
		SVIStatus:  "configured",
		L2VNI:      10100,
		Members:    []string{"Ethernet0", "Ethernet4(t)"},
	}

	if vlan.ID != 100 {
		t.Errorf("ID = %d", vlan.ID)
	}
	if vlan.L2VNI != 10100 {
		t.Errorf("L2VNI = %d", vlan.L2VNI)
	}
}

func TestVRFState_Structure(t *testing.T) {
	vrf := device.VRFState{
		Name:       "Vrf_CUST1",
		State:      "up",
		L3VNI:      10001,
		Interfaces: []string{"Ethernet0", "Vlan100"},
	}

	if vrf.Name != "Vrf_CUST1" {
		t.Errorf("Name = %q", vrf.Name)
	}
	if vrf.L3VNI != 10001 {
		t.Errorf("L3VNI = %d", vrf.L3VNI)
	}
	if len(vrf.Interfaces) != 2 {
		t.Errorf("Interfaces count = %d", len(vrf.Interfaces))
	}
}

func TestInterfaceSummary_Structure(t *testing.T) {
	summary := device.InterfaceSummary{
		Name:        "Ethernet0",
		AdminStatus: "up",
		Speed:       "100G",
		IPAddress:   "10.1.1.1/30",
		VRF:         "Vrf_CUST1",
		Service:     "customer-l3",
		LAGMember:   "",
	}

	if summary.Name != "Ethernet0" {
		t.Errorf("Name = %q", summary.Name)
	}
	if summary.Service != "customer-l3" {
		t.Errorf("Service = %q", summary.Service)
	}
}

func TestPopulateDeviceState_NilStateDB(t *testing.T) {
	configDB := newEmptyConfigDB()
	configDB.Port["Ethernet0"] = PortEntry{AdminStatus: "up", Speed: "100000", MTU: "9100"}
	configDB.Port["Ethernet4"] = PortEntry{AdminStatus: "down", Speed: "25000"}
	configDB.Interface["Ethernet0"] = InterfaceEntry{VRFName: "Vrf_CUST1"}
	configDB.Interface["Ethernet0|10.1.1.1/30"] = InterfaceEntry{VRFName: "Vrf_CUST1"}
	configDB.PortChannel["PortChannel100"] = PortChannelEntry{AdminStatus: "up"}
	configDB.PortChannelMember["PortChannel100|Ethernet4"] = map[string]string{}
	configDB.VLAN["Vlan100"] = VLANEntry{VLANID: "100", AdminStatus: "up"}
	configDB.VLANMember["Vlan100|Ethernet0"] = VLANMemberEntry{TaggingMode: "tagged"}
	configDB.VXLANTunnelMap["vtep1|map_100"] = VXLANMapEntry{VLAN: "Vlan100", VNI: "10100"}
	configDB.VLANInterface["Vlan100"] = map[string]string{"vrf_name": "Vrf_CUST1"}
	configDB.VRF["Vrf_CUST1"] = VRFEntry{VNI: "10001"}
	configDB.NewtronServiceBinding["Ethernet0"] = ServiceBindingEntry{ServiceName: "svc1"}
	configDB.ACLTable["ACL1"] = ACLTableEntry{Stage: "ingress", Ports: "Ethernet0"}
	configDB.BGPGlobals["default"] = BGPGlobalsEntry{LocalASN: "65000", RouterID: "10.0.0.1"}

	state := &device.DeviceState{
		Interfaces:   make(map[string]*device.InterfaceState),
		PortChannels: make(map[string]*device.PortChannelState),
		VLANs:        make(map[int]*device.VLANState),
		VRFs:         make(map[string]*device.VRFState),
	}

	PopulateDeviceState(state, nil, configDB)

	// Interfaces
	if len(state.Interfaces) != 2 {
		t.Fatalf("Interfaces count = %d, want 2", len(state.Interfaces))
	}
	eth0 := state.Interfaces["Ethernet0"]
	if eth0 == nil {
		t.Fatal("Ethernet0 not in state")
	}
	if eth0.AdminStatus != "up" {
		t.Errorf("Ethernet0 AdminStatus = %q", eth0.AdminStatus)
	}
	if eth0.MTU != 9100 {
		t.Errorf("Ethernet0 MTU = %d", eth0.MTU)
	}
	if eth0.VRF != "Vrf_CUST1" {
		t.Errorf("Ethernet0 VRF = %q", eth0.VRF)
	}
	if len(eth0.IPAddresses) != 1 || eth0.IPAddresses[0] != "10.1.1.1/30" {
		t.Errorf("Ethernet0 IPs = %v", eth0.IPAddresses)
	}
	if eth0.Service != "svc1" {
		t.Errorf("Ethernet0 Service = %q", eth0.Service)
	}
	if eth0.IngressACL != "ACL1" {
		t.Errorf("Ethernet0 IngressACL = %q", eth0.IngressACL)
	}
	eth4 := state.Interfaces["Ethernet4"]
	if eth4.LAGMember != "PortChannel100" {
		t.Errorf("Ethernet4 LAGMember = %q", eth4.LAGMember)
	}

	// PortChannels
	pc := state.PortChannels["PortChannel100"]
	if pc == nil {
		t.Fatal("PortChannel100 not in state")
	}
	if pc.AdminStatus != "up" {
		t.Errorf("PortChannel100 AdminStatus = %q", pc.AdminStatus)
	}
	if len(pc.Members) != 1 || pc.Members[0] != "Ethernet4" {
		t.Errorf("PortChannel100 Members = %v", pc.Members)
	}

	// VLANs
	vlan := state.VLANs[100]
	if vlan == nil {
		t.Fatal("Vlan100 not in state")
	}
	if vlan.L2VNI != 10100 {
		t.Errorf("Vlan100 L2VNI = %d", vlan.L2VNI)
	}
	if vlan.SVIStatus != "configured" {
		t.Errorf("Vlan100 SVIStatus = %q", vlan.SVIStatus)
	}
	if len(vlan.Members) != 1 {
		t.Errorf("Vlan100 Members = %v", vlan.Members)
	}

	// VRFs
	vrf := state.VRFs["Vrf_CUST1"]
	if vrf == nil {
		t.Fatal("Vrf_CUST1 not in state")
	}
	if vrf.L3VNI != 10001 {
		t.Errorf("Vrf_CUST1 L3VNI = %d", vrf.L3VNI)
	}
	if len(vrf.Interfaces) == 0 {
		t.Error("Vrf_CUST1 has no interfaces")
	}

	// BGP
	if state.BGP == nil {
		t.Fatal("BGP state is nil")
	}
	if state.BGP.LocalAS != 65000 {
		t.Errorf("BGP LocalAS = %d", state.BGP.LocalAS)
	}
	if state.BGP.RouterID != "10.0.0.1" {
		t.Errorf("BGP RouterID = %q", state.BGP.RouterID)
	}

	// EVPN
	if state.EVPN == nil {
		t.Fatal("EVPN state is nil")
	}
}

func TestPopulateDeviceState_WithStateDB(t *testing.T) {
	configDB := newEmptyConfigDB()
	configDB.PortChannel["PortChannel100"] = PortChannelEntry{AdminStatus: "up"}
	configDB.VRF["Vrf_CUST1"] = VRFEntry{VNI: "10001"}
	configDB.BGPGlobals["default"] = BGPGlobalsEntry{LocalASN: "65000"}

	stateDB := &StateDB{
		PortTable: map[string]PortStateEntry{
			"Ethernet0": {AdminStatus: "up", OperStatus: "up", Speed: "100G", MTU: "9100"},
		},
		LAGTable:          map[string]LAGStateEntry{"PortChannel100": {OperStatus: "up"}},
		LAGMemberTable:    map[string]LAGMemberStateEntry{},
		VLANTable:         map[string]VLANStateEntry{"Vlan200": {OperStatus: "up"}},
		VRFTable:          map[string]VRFStateEntry{"Vrf_CUST1": {State: "active"}},
		VXLANTunnelTable:  map[string]VXLANTunnelStateEntry{},
		BGPNeighborTable:  map[string]BGPNeighborStateEntry{"default|10.0.0.2": {State: "Established", RemoteAS: "65001"}},
		InterfaceTable:    map[string]InterfaceStateEntry{},
		NeighTable:        map[string]NeighStateEntry{},
		FDBTable:          map[string]FDBStateEntry{},
		RouteTable:        map[string]RouteStateEntry{},
		TransceiverInfo:   map[string]TransceiverInfoEntry{},
		TransceiverStatus: map[string]TransceiverStatusEntry{},
	}

	state := &device.DeviceState{
		Interfaces:   make(map[string]*device.InterfaceState),
		PortChannels: make(map[string]*device.PortChannelState),
		VLANs:        make(map[int]*device.VLANState),
		VRFs:         make(map[string]*device.VRFState),
	}

	PopulateDeviceState(state, stateDB, configDB)

	// Interfaces from StateDB
	eth0 := state.Interfaces["Ethernet0"]
	if eth0 == nil {
		t.Fatal("Ethernet0 not populated from StateDB")
	}
	if eth0.OperStatus != "up" {
		t.Errorf("Ethernet0 OperStatus = %q", eth0.OperStatus)
	}

	// PortChannels from StateDB
	if state.PortChannels["PortChannel100"] == nil {
		t.Fatal("PortChannel100 not populated from StateDB")
	}

	// VLANs from StateDB
	if state.VLANs[200] == nil {
		t.Fatal("Vlan200 not populated from StateDB")
	}

	// VRFs from StateDB with ConfigDB enrichment
	vrf := state.VRFs["Vrf_CUST1"]
	if vrf == nil {
		t.Fatal("Vrf_CUST1 not populated")
	}
	if vrf.State != "active" {
		t.Errorf("Vrf_CUST1 State = %q", vrf.State)
	}
	if vrf.L3VNI != 10001 {
		t.Errorf("Vrf_CUST1 L3VNI = %d", vrf.L3VNI)
	}

	// BGP neighbors from StateDB
	if state.BGP.Neighbors["10.0.0.2"] == nil {
		t.Fatal("BGP neighbor 10.0.0.2 not populated")
	}
	if state.BGP.Neighbors["10.0.0.2"].State != "Established" {
		t.Errorf("BGP neighbor state = %q", state.BGP.Neighbors["10.0.0.2"].State)
	}
}

func TestConfigDB_EmptyInit(t *testing.T) {
	// Test that empty ConfigDB can be created and used
	cfg := &ConfigDB{}

	if cfg.Port != nil {
		t.Error("Port should be nil initially")
	}

	// Initialize a map
	cfg.Port = make(map[string]PortEntry)
	cfg.Port["Ethernet0"] = PortEntry{AdminStatus: "up"}

	if cfg.Port["Ethernet0"].AdminStatus != "up" {
		t.Errorf("AdminStatus = %q", cfg.Port["Ethernet0"].AdminStatus)
	}
}

func TestConfigDB_ComplexJSON(t *testing.T) {
	// Test a more complex config_db structure
	configJSON := `{
		"PORT": {
			"Ethernet0": {
				"admin_status": "up",
				"mtu": "9100",
				"speed": "100000"
			}
		},
		"VLAN": {
			"Vlan100": {
				"vlanid": "100"
			}
		},
		"VLAN_MEMBER": {
			"Vlan100|Ethernet4": {
				"tagging_mode": "tagged"
			}
		},
		"PORTCHANNEL": {
			"PortChannel100": {
				"admin_status": "up",
				"min_links": "1"
			}
		},
		"PORTCHANNEL_MEMBER": {
			"PortChannel100|Ethernet0": {}
		},
		"VRF": {
			"Vrf_CUST1": {
				"vni": "10001"
			}
		},
		"INTERFACE": {
			"Ethernet8": {
				"vrf_name": "Vrf_CUST1"
			},
			"Ethernet8|10.1.1.1/30": {}
		},
		"VXLAN_TUNNEL": {
			"vtep1": {
				"src_ip": "10.0.0.1"
			}
		},
		"VXLAN_TUNNEL_MAP": {
			"vtep1|map_100_Vlan100": {
				"vlan": "Vlan100",
				"vni": "10100"
			}
		}
	}`

	var cfg ConfigDB
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify PORT
	if port, ok := cfg.Port["Ethernet0"]; !ok {
		t.Error("Port Ethernet0 not found")
	} else {
		if port.Speed != "100000" {
			t.Errorf("Port speed = %q", port.Speed)
		}
	}

	// Verify VLAN_MEMBER
	if member, ok := cfg.VLANMember["Vlan100|Ethernet4"]; !ok {
		t.Error("VLAN member not found")
	} else {
		if member.TaggingMode != "tagged" {
			t.Errorf("TaggingMode = %q", member.TaggingMode)
		}
	}

	// Verify PORTCHANNEL_MEMBER
	if _, ok := cfg.PortChannelMember["PortChannel100|Ethernet0"]; !ok {
		t.Error("PortChannel member not found")
	}

	// Verify INTERFACE with VRF
	if intf, ok := cfg.Interface["Ethernet8"]; !ok {
		t.Error("Interface Ethernet8 not found")
	} else {
		if intf.VRFName != "Vrf_CUST1" {
			t.Errorf("VRFName = %q", intf.VRFName)
		}
	}

	// Verify VXLAN_TUNNEL
	if vtep, ok := cfg.VXLANTunnel["vtep1"]; !ok {
		t.Error("VTEP not found")
	} else {
		if vtep.SrcIP != "10.0.0.1" {
			t.Errorf("SrcIP = %q", vtep.SrcIP)
		}
	}

	// Verify VXLAN_TUNNEL_MAP
	if mapping, ok := cfg.VXLANTunnelMap["vtep1|map_100_Vlan100"]; !ok {
		t.Error("VXLAN mapping not found")
	} else {
		if mapping.VNI != "10100" {
			t.Errorf("VNI = %q", mapping.VNI)
		}
	}
}
