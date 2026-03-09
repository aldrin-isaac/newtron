package node

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// newTestInterface creates an Interface backed by an offline Node for testing
// config-generation methods that need i.node (SpecProvider) and i.name.
func newTestInterface(sp SpecProvider, name string) *Interface {
	n := &Node{SpecProvider: sp, configDB: sonic.NewEmptyConfigDB(), offline: true}
	return &Interface{node: n, name: name}
}

func TestServiceConfig_EVPNBridged(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"CUSTOMER_L2": {
				ServiceType: spec.ServiceTypeEVPNBridged,
				MACVPN:      "CUST_MAC",
			},
		},
		macvpn: map[string]*spec.MACVPNSpec{
			"CUST_MAC": {VlanID: 100, VNI: 20100, ARPSuppression: true},
		},
	}

	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "CUSTOMER_L2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEntry(t, entries, "VLAN", "Vlan100", "vlanid", "100")
	assertEntry(t, entries, "VXLAN_TUNNEL_MAP", "vtep1|VNI20100_Vlan100", "vni", "20100")
	assertEntry(t, entries, "SUPPRESS_VLAN_NEIGH", "Vlan100", "suppress", "on")
	assertEntry(t, entries, "VLAN_MEMBER", "Vlan100|Ethernet0", "tagging_mode", "untagged")

	// NEWTRON_SERVICE_BINDING is NOT emitted by generateServiceEntries — ApplyService
	// constructs it with full self-sufficiency fields.
	assertNoEntry(t, entries, "NEWTRON_SERVICE_BINDING", "Ethernet0")
}

func TestServiceConfig_Routed_NoVRF(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"FABRIC_UNDERLAY": {
				ServiceType: spec.ServiceTypeRouted,
			},
		},
	}

	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "FABRIC_UNDERLAY",
		IPAddress:   "10.1.0.0/31",
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

func TestServiceConfig_EVPNRouted_WithVRF(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"CUSTOMER_L3": {
				ServiceType: spec.ServiceTypeEVPNRouted,
				VRFType:     spec.VRFTypeInterface,
				IPVPN:       "CUSTOMER_VPN",
			},
		},
		ipvpn: map[string]*spec.IPVPNSpec{
			"CUSTOMER_VPN": {L3VNI: 10001},
		},
	}

	iface := newTestInterface(sp, "Ethernet4")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "CUSTOMER_L3",
		IPAddress:   "10.2.0.1/30",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VRF creation — DeriveVRFName shortens Ethernet4 → ETH4.
	// VRF entry carries vni field for L3VNI.
	assertEntry(t, entries, "VRF", "CUSTOMER_L3_ETH4", "vni", "10001")

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

func TestServiceConfig_EVPNIRB(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"CUSTOMER_IRB": {
				ServiceType: spec.ServiceTypeEVPNIRB,
				VRFType:     spec.VRFTypeInterface,
				IPVPN:       "CUST_VPN",
				MACVPN:      "CUST_MAC",
			},
		},
		ipvpn: map[string]*spec.IPVPNSpec{
			"CUST_VPN": {L3VNI: 10001},
		},
		macvpn: map[string]*spec.MACVPNSpec{
			"CUST_MAC": {VlanID: 100, VNI: 20100, AnycastIP: "10.1.100.1/24", AnycastMAC: "00:00:00:01:02:03"},
		},
	}

	iface := newTestInterface(sp, "Ethernet8")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "CUSTOMER_IRB",
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

func TestServiceConfig_ACL_WithCoS(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"SVC_WITH_ACL": {
				ServiceType:   spec.ServiceTypeRouted,
				IngressFilter: "TEST_FILTER",
			},
		},
		filterSpecs: map[string]*spec.FilterSpec{
			"TEST_FILTER": {
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
	}

	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "SVC_WITH_ACL",
		IPAddress:   "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ACL name is content-hashed from filter name + direction + hash (Principle 35)
	// TEST_FILTER rules produce hash DB985A64
	expectedACL := "TEST_FILTER_IN_DB985A64"

	// Verify ACL table exists
	assertEntry(t, entries, "ACL_TABLE", expectedACL, "stage", "ingress")

	// Verify CoS→TC mapping present (fix #4)
	for _, e := range entries {
		if e.Table == "ACL_RULE" && e.Key == expectedACL+"|RULE_10" {
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
	t.Errorf("missing ACL_RULE|%s|RULE_10 entry", expectedACL)
}

func TestServiceConfig_BGP_UnderlayASN(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   "65001",
				},
			},
		},
	}

	// Bug fix #2: UnderlayASN should be used when set
	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "TRANSIT",
		IPAddress:   "10.1.0.0/31",
		UnderlayASN: 65100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR" {
			if e.Fields["asn"] != "65001" {
				t.Errorf("BGP_NEIGHBOR asn = %q, want 65001", e.Fields["asn"])
			}
			if _, hasLocalASN := e.Fields["local_asn"]; hasLocalASN {
				t.Errorf("BGP_NEIGHBOR should not set local_asn (got %q)", e.Fields["local_asn"])
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR entry")
}

func TestServiceConfig_BGP_FallbackToLocalAS(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   "65001",
				},
			},
		},
	}

	// When UnderlayASN is 0, should fall back to LocalAS
	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "TRANSIT",
		IPAddress:   "10.1.0.0/31",
		UnderlayASN: 64512,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR" {
			if _, hasLocalASN := e.Fields["local_asn"]; hasLocalASN {
				t.Errorf("BGP_NEIGHBOR should not set local_asn (got %q)", e.Fields["local_asn"])
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR entry")
}

func TestServiceConfig_BGP_AdminStatus(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   "65001",
				},
			},
		},
	}

	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "TRANSIT",
		IPAddress:   "10.1.0.0/31",
		UnderlayASN: 64512,
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

func TestServiceConfig_RouteTargets(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"CUSTOMER_L3": {
				ServiceType: spec.ServiceTypeEVPNRouted,
				VRFType:     spec.VRFTypeInterface,
				IPVPN:       "CUSTOMER_VPN",
			},
		},
		ipvpn: map[string]*spec.IPVPNSpec{
			"CUSTOMER_VPN": {
				L3VNI:        10001,
				RouteTargets: []string{"64512:10001"},
			},
		},
	}

	iface := newTestInterface(sp, "Ethernet4")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "CUSTOMER_L3",
		IPAddress:   "10.2.0.1/30",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// BGP_GLOBALS_AF l2vpn_evpn for the VRF — produced by ipvpn
	assertEntry(t, entries, "BGP_GLOBALS_AF", "CUSTOMER_L3_ETH4|l2vpn_evpn", "advertise-ipv4-unicast", "true")

	// Route targets are written to BGP_GLOBALS_EVPN_RT (frrcfgd watches this table)
	assertEntry(t, entries, "BGP_GLOBALS_EVPN_RT", "CUSTOMER_L3_ETH4|L2VPN_EVPN|64512:10001", "route-target-type", "both")
}

func TestServiceConfig_SharedVRF(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"SHARED_L3": {
				ServiceType: spec.ServiceTypeEVPNRouted,
				VRFType:     spec.VRFTypeShared,
				IPVPN:       "SHARED_VPN",
			},
		},
		ipvpn: map[string]*spec.IPVPNSpec{
			"SHARED_VPN": {L3VNI: 20001, VRF: "SHARED_VPN"},
		},
	}

	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "SHARED_L3",
		IPAddress:   "10.3.0.0/31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VRF name should be the ipvpn name; vni field carries the L3VNI.
	assertEntry(t, entries, "VRF", "SHARED_VPN", "vni", "20001")

	// INTERFACE should have vrf_name = SHARED_VPN
	for _, e := range entries {
		if e.Table == "INTERFACE" && e.Key == "Ethernet0" {
			if e.Fields["vrf_name"] != "SHARED_VPN" {
				t.Errorf("INTERFACE vrf_name = %q, want SHARED_VPN", e.Fields["vrf_name"])
			}
			return
		}
	}
	t.Error("missing base INTERFACE entry")
}

func TestServiceConfig_BGP_PeerASRequest(t *testing.T) {
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   spec.PeerASRequest,
				},
			},
		},
	}

	// PeerAS field should be used when service spec says peer_as:"request"
	iface := newTestInterface(sp, "Ethernet0")
	entries, err := iface.generateServiceEntries(ServiceEntryParams{
		ServiceName: "TRANSIT",
		IPAddress:   "10.1.0.0/31",
		PeerAS:      65002,
		UnderlayASN: 65001,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Table == "BGP_NEIGHBOR" {
			if e.Fields["asn"] != "65002" {
				t.Errorf("BGP_NEIGHBOR asn = %q, want 65002", e.Fields["asn"])
			}
			// Peer IP should be derived from 10.1.0.0/31 → 10.1.0.1
			if e.Key != "default|10.1.0.1" {
				t.Errorf("BGP_NEIGHBOR key = %q, want default|10.1.0.1", e.Key)
			}
			return
		}
	}
	t.Error("missing BGP_NEIGHBOR entry — peer_as:request with PeerAS should generate neighbor")
}

// assertNoEntry checks that no entry with the given table and key exists.
func assertNoEntry(t *testing.T, entries []sonic.Entry, table, key string) {
	t.Helper()
	for _, e := range entries {
		if e.Table == table && e.Key == key {
			t.Errorf("unexpected entry %s|%s (should not be emitted)", table, key)
			return
		}
	}
}

// assertEntry checks that an entry with the given table and key exists,
// and optionally checks a field value (pass empty field to skip field check).
func assertEntry(t *testing.T, entries []sonic.Entry, table, key, field, value string) {
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
