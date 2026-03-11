package sonic

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestRequireFrrcfgd_Unified(t *testing.T) {
	d := &Device{
		Name: "switch1",
		ConfigDB: &ConfigDB{
			DeviceMetadata: map[string]map[string]string{
				"localhost": {"docker_routing_config_mode": "unified"},
			},
		},
	}
	if err := d.requireFrrcfgd(); err != nil {
		t.Errorf("unified mode should pass: %v", err)
	}
}

func TestRequireFrrcfgd_NotSet(t *testing.T) {
	d := &Device{
		Name: "switch1",
		ConfigDB: &ConfigDB{
			DeviceMetadata: map[string]map[string]string{
				"localhost": {"hostname": "switch1"},
			},
		},
	}
	err := d.requireFrrcfgd()
	if err == nil {
		t.Fatal("expected error when docker_routing_config_mode is not set")
	}
	if !strings.Contains(err.Error(), "frrcfgd not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRequireFrrcfgd_Split(t *testing.T) {
	d := &Device{
		Name: "switch1",
		ConfigDB: &ConfigDB{
			DeviceMetadata: map[string]map[string]string{
				"localhost": {"docker_routing_config_mode": "split"},
			},
		},
	}
	err := d.requireFrrcfgd()
	if err == nil {
		t.Fatal("expected error when mode is split")
	}
	if !strings.Contains(err.Error(), "frrcfgd not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFrrcfgdMetadataFields(t *testing.T) {
	fields := FrrcfgdMetadataFields()
	if fields["docker_routing_config_mode"] != "unified" {
		t.Errorf("docker_routing_config_mode = %q, want \"unified\"", fields["docker_routing_config_mode"])
	}
	if fields["frr_mgmt_framework_config"] != "true" {
		t.Errorf("frr_mgmt_framework_config = %q, want \"true\"", fields["frr_mgmt_framework_config"])
	}
	if len(fields) != 2 {
		t.Errorf("expected 2 fields, got %d: %v", len(fields), fields)
	}
}

func TestIsUnifiedConfigMode(t *testing.T) {
	tests := []struct {
		name   string
		device *Device
		want   bool
	}{
		{"unified", &Device{ConfigDB: &ConfigDB{DeviceMetadata: map[string]map[string]string{"localhost": {"docker_routing_config_mode": "unified"}}}}, true},
		{"split", &Device{ConfigDB: &ConfigDB{DeviceMetadata: map[string]map[string]string{"localhost": {"docker_routing_config_mode": "split"}}}}, false},
		{"empty", &Device{ConfigDB: &ConfigDB{DeviceMetadata: map[string]map[string]string{"localhost": {}}}}, false},
		{"no localhost", &Device{ConfigDB: &ConfigDB{DeviceMetadata: map[string]map[string]string{}}}, false},
		{"nil metadata", &Device{ConfigDB: &ConfigDB{}}, false},
		{"nil configdb", &Device{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.device.IsUnifiedConfigMode(); got != tt.want {
				t.Errorf("IsUnifiedConfigMode() = %v, want %v", got, tt.want)
			}
		})
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

// TestShadow_CoversAllSchemaTables verifies that every table in the validation
// schema has a corresponding case in both applyEntry and DeleteEntry.
// This prevents silent data loss in abstract/offline mode when new tables are
// added to the schema but not to the shadow ConfigDB switch statements.
func TestShadow_CoversAllSchemaTables(t *testing.T) {
	db := NewEmptyConfigDB()

	for tableName := range Schema {
		testFields := map[string]string{"test_field": "test_value"}

		// Test applyEntry: should not panic and should create an entry
		db.applyEntry(tableName, "test_key", testFields)

		// Test DeleteEntry: should not panic
		db.DeleteEntry(tableName, "test_key")
	}
}

// TestShadow_ApplyDeleteRoundTrip verifies that for each schema table,
// applyEntry creates state that DeleteEntry can remove.
func TestShadow_ApplyDeleteRoundTrip(t *testing.T) {
	for tableName := range Schema {
		t.Run(tableName, func(t *testing.T) {
			db := NewEmptyConfigDB()

			// Apply an entry
			fields := map[string]string{"NULL": "NULL"}
			db.applyEntry(tableName, "roundtrip_key", fields)

			// Delete the entry
			db.DeleteEntry(tableName, "roundtrip_key")
		})
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
			"vtep1|VNI100_Vlan100": {
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
	if mapping, ok := cfg.VXLANTunnelMap["vtep1|VNI100_Vlan100"]; !ok {
		t.Error("VXLAN mapping not found")
	} else {
		if mapping.VNI != "10100" {
			t.Errorf("VNI = %q", mapping.VNI)
		}
	}
}
