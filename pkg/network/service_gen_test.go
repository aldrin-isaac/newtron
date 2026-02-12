package network

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/spec"
)

func TestGenerateServiceEntries_L2(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"customer-l2": {
					ServiceType: spec.ServiceTypeL2,
					MACVPN:      "cust-mac",
				},
			},
			MACVPN: map[string]*spec.MACVPNSpec{
				"cust-mac": {VLAN: 100, L2VNI: 20100, ARPSuppression: true},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "customer-l2",
		InterfaceName: "Ethernet0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEntry(t, entries, "VLAN", "Vlan100", "vlanid", "100")
	assertEntry(t, entries, "VXLAN_TUNNEL_MAP", "vtep1|map_20100_Vlan100", "vni", "20100")
	assertEntry(t, entries, "SUPPRESS_VLAN_NEIGH", "Vlan100", "suppress", "on")
	assertEntry(t, entries, "VLAN_MEMBER", "Vlan100|Ethernet0", "tagging_mode", "untagged")
	assertEntry(t, entries, "NEWTRON_SERVICE_BINDING", "Ethernet0", "service_name", "customer-l2")
}

func TestGenerateServiceEntries_L3_NoVRF(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"fabric-underlay": {
					ServiceType: spec.ServiceTypeL3,
				},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "fabric-underlay",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must always emit base INTERFACE entry (fix #3)
	var baseFound, ipFound bool
	for _, e := range entries {
		if e.Table != "INTERFACE" {
			continue
		}
		switch e.Key {
		case "Ethernet0":
			baseFound = true
			if len(e.Fields) != 0 {
				t.Errorf("base INTERFACE entry should have empty fields, got %v", e.Fields)
			}
		case "Ethernet0|10.1.0.0/31":
			ipFound = true
		}
	}
	if !baseFound {
		t.Error("missing base INTERFACE|Ethernet0 entry — intfmgrd requires this before processing IP entries")
	}
	if !ipFound {
		t.Error("missing INTERFACE|Ethernet0|10.1.0.0/31 entry")
	}
}

func TestGenerateServiceEntries_L3_WithVRF(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"customer-l3": {
					ServiceType: spec.ServiceTypeL3,
					VRFType:     spec.VRFTypeInterface,
					IPVPN:       "customer-vpn",
				},
			},
			IPVPN: map[string]*spec.IPVPNSpec{
				"customer-vpn": {L3VNI: 10001},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "customer-l3",
		InterfaceName: "Ethernet4",
		IPAddress:     "10.2.0.1/30",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VRF creation — DeriveVRFName shortens Ethernet4 → Eth4
	assertEntry(t, entries, "VRF", "customer-l3-Eth4", "vni", "10001")

	// L3VNI mapping
	assertEntry(t, entries, "VXLAN_TUNNEL_MAP", "vtep1|map_10001_customer-l3-Eth4", "vni", "10001")

	// Base INTERFACE entry should have vrf_name
	var baseFound, ipFound bool
	for _, e := range entries {
		if e.Table != "INTERFACE" {
			continue
		}
		switch e.Key {
		case "Ethernet4":
			baseFound = true
			vrfName, ok := e.Fields["vrf_name"]
			if !ok || vrfName == "" {
				t.Error("base INTERFACE entry should have vrf_name for VRF-bound service")
			}
		case "Ethernet4|10.2.0.1/30":
			ipFound = true
		}
	}
	if !baseFound {
		t.Error("missing base INTERFACE|Ethernet4 entry")
	}
	if !ipFound {
		t.Error("missing INTERFACE|Ethernet4|10.2.0.1/30 entry")
	}
}

func TestGenerateServiceEntries_IRB(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"customer-irb": {
					ServiceType:    spec.ServiceTypeIRB,
					VRFType:        spec.VRFTypeInterface,
					IPVPN:          "cust-vpn",
					MACVPN:         "cust-mac",
					AnycastGateway: "10.1.100.1/24",
					AnycastMAC:     "00:00:00:01:02:03",
				},
			},
			IPVPN: map[string]*spec.IPVPNSpec{
				"cust-vpn": {L3VNI: 10001},
			},
			MACVPN: map[string]*spec.MACVPNSpec{
				"cust-mac": {VLAN: 100, L2VNI: 20100},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "customer-irb",
		InterfaceName: "Ethernet8",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEntry(t, entries, "VLAN", "Vlan100", "vlanid", "100")
	assertEntry(t, entries, "VLAN_MEMBER", "Vlan100|Ethernet8", "tagging_mode", "tagged")
	assertEntry(t, entries, "VLAN_INTERFACE", "Vlan100", "", "")
	assertEntry(t, entries, "SAG_GLOBAL", "IPv4", "gwmac", "00:00:00:01:02:03")

	// Check anycast gateway IP on VLAN_INTERFACE
	found := false
	for _, e := range entries {
		if e.Table == "VLAN_INTERFACE" && e.Key == "Vlan100|10.1.100.1/24" {
			found = true
		}
	}
	if !found {
		t.Error("missing VLAN_INTERFACE|Vlan100|10.1.100.1/24 entry for anycast gateway")
	}
}

func TestGenerateServiceEntries_ACL_WithCoS(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"svc-with-acl": {
					ServiceType:   spec.ServiceTypeL3,
					IngressFilter: "test-filter",
				},
			},
			FilterSpecs: map[string]*spec.FilterSpec{
				"test-filter": {
					Rules: []*spec.FilterRule{
						{
							Sequence: 10,
							SrcIP:    "10.0.0.0/8",
							Protocol: "tcp",
							DstPort:  "80",
							Action:   "permit",
							CoS:      "ef",
						},
						{
							Sequence: 20,
							Action:   "deny",
						},
					},
				},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "svc-with-acl",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ACL table exists
	assertEntry(t, entries, "ACL_TABLE", "svc-with-acl-in", "stage", "ingress")

	// Verify CoS→TC mapping present (fix #4)
	for _, e := range entries {
		if e.Table == "ACL_RULE" && e.Key == "svc-with-acl-in|RULE_10" {
			if e.Fields["TC"] != "5" {
				t.Errorf("ACL rule with CoS=ef should have TC=5, got %q", e.Fields["TC"])
			}
			if e.Fields["SRC_IP"] != "10.0.0.0/8" {
				t.Errorf("ACL rule SRC_IP = %q, want 10.0.0.0/8", e.Fields["SRC_IP"])
			}
			if e.Fields["L4_DST_PORT"] != "80" {
				t.Errorf("ACL rule L4_DST_PORT = %q, want 80", e.Fields["L4_DST_PORT"])
			}
			return
		}
	}
	t.Error("missing ACL_RULE|svc-with-acl-in|RULE_10 entry")
}

func TestGenerateServiceEntries_BGP_UnderlayASN(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"transit": {
					ServiceType: spec.ServiceTypeL3,
					Routing: &spec.RoutingSpec{
						Protocol: spec.RoutingProtocolBGP,
						PeerAS:   "65001",
					},
				},
			},
		},
	}

	// Bug fix #2: UnderlayASN should be used when set
	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "transit",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.1.0.0/31",
		LocalAS:       64512,
		UnderlayASN:   65100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR" {
			if e.Fields["local_asn"] != "65100" {
				t.Errorf("BGP_NEIGHBOR local_asn = %q, want 65100 (UnderlayASN)", e.Fields["local_asn"])
			}
			if e.Fields["asn"] != "65001" {
				t.Errorf("BGP_NEIGHBOR asn = %q, want 65001", e.Fields["asn"])
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR entry")
}

func TestGenerateServiceEntries_BGP_FallbackToLocalAS(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"transit": {
					ServiceType: spec.ServiceTypeL3,
					Routing: &spec.RoutingSpec{
						Protocol: spec.RoutingProtocolBGP,
						PeerAS:   "65001",
					},
				},
			},
		},
	}

	// When UnderlayASN is 0, should fall back to LocalAS
	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "transit",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.1.0.0/31",
		LocalAS:       64512,
		UnderlayASN:   0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR" {
			if e.Fields["local_asn"] != "64512" {
				t.Errorf("BGP_NEIGHBOR local_asn = %q, want 64512 (LocalAS fallback)", e.Fields["local_asn"])
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR entry")
}

func TestGenerateServiceEntries_BGP_AdminStatus(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"transit": {
					ServiceType: spec.ServiceTypeL3,
					Routing: &spec.RoutingSpec{
						Protocol: spec.RoutingProtocolBGP,
						PeerAS:   "65001",
					},
				},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "transit",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.1.0.0/31",
		LocalAS:       64512,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Bug fix #1: BGP_NEIGHBOR_AF must use "admin_status", not "activate"
	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR_AF" {
			if _, ok := e.Fields["activate"]; ok {
				t.Error("BGP_NEIGHBOR_AF should NOT have 'activate' field — frrcfgd uses 'admin_status'")
			}
			if e.Fields["admin_status"] != "true" {
				t.Errorf("BGP_NEIGHBOR_AF admin_status = %q, want 'true'", e.Fields["admin_status"])
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR_AF entry")
}

func TestGenerateServiceEntries_RouteTargets(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"customer-l3": {
					ServiceType: spec.ServiceTypeL3,
					VRFType:     spec.VRFTypeInterface,
					IPVPN:       "customer-vpn",
				},
			},
			IPVPN: map[string]*spec.IPVPNSpec{
				"customer-vpn": {
					L3VNI:    10001,
					ImportRT: []string{"64512:10001"},
					ExportRT: []string{"64512:10001", "64512:10002"},
				},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "customer-l3",
		InterfaceName: "Ethernet4",
		IPAddress:     "10.2.0.1/30",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// BGP_GLOBALS_AF with route targets — VRF name uses shortened interface
	for _, e := range entries {
		if e.Table == "BGP_GLOBALS_AF" && e.Key == "customer-l3-Eth4|l2vpn_evpn" {
			if e.Fields["route_target_import_evpn"] != "64512:10001" {
				t.Errorf("route_target_import_evpn = %q, want 64512:10001", e.Fields["route_target_import_evpn"])
			}
			if e.Fields["route_target_export_evpn"] != "64512:10001,64512:10002" {
				t.Errorf("route_target_export_evpn = %q, want 64512:10001,64512:10002", e.Fields["route_target_export_evpn"])
			}
			return
		}
	}
	t.Error("missing BGP_GLOBALS_AF route target entry for VRF")
}

func TestGenerateServiceEntries_SharedVRF(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Services: map[string]*spec.ServiceSpec{
				"shared-l3": {
					ServiceType: spec.ServiceTypeL3,
					VRFType:     spec.VRFTypeShared,
					IPVPN:       "shared-vpn",
				},
			},
			IPVPN: map[string]*spec.IPVPNSpec{
				"shared-vpn": {L3VNI: 20001},
			},
		},
	}

	entries, err := GenerateServiceEntries(n, ServiceEntryParams{
		ServiceName:   "shared-l3",
		InterfaceName: "Ethernet0",
		IPAddress:     "10.3.0.0/31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VRF name should be the ipvpn name
	assertEntry(t, entries, "VRF", "shared-vpn", "vni", "20001")

	// INTERFACE should have vrf_name = shared-vpn
	for _, e := range entries {
		if e.Table == "INTERFACE" && e.Key == "Ethernet0" {
			if e.Fields["vrf_name"] != "shared-vpn" {
				t.Errorf("INTERFACE vrf_name = %q, want shared-vpn", e.Fields["vrf_name"])
			}
			return
		}
	}
	t.Error("missing base INTERFACE entry")
}

// assertEntry checks that an entry with the given table and key exists,
// and optionally checks a field value (pass empty field to skip field check).
func assertEntry(t *testing.T, entries []CompositeEntry, table, key, field, value string) {
	t.Helper()
	for _, e := range entries {
		if e.Table == table && e.Key == key {
			if field != "" {
				if e.Fields[field] != value {
					t.Errorf("%s|%s: field %q = %q, want %q", table, key, field, e.Fields[field], value)
				}
			}
			return
		}
	}
	t.Errorf("missing entry %s|%s", table, key)
}
