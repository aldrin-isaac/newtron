package node

import (
	"strings"
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

	// ACL name is content-hashed from filter spec (Principle 35).
	// Compute dynamically to avoid fragile hardcoded hash values.
	hash := computeFilterHash(sp.filterSpecs["TEST_FILTER"])
	expectedACL := "TEST_FILTER_IN_" + hash

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

// ============================================================================
// Route Policy Content-Hashed Naming Tests (Principle 35)
// ============================================================================

func TestCreateRoutePolicy_ContentHashedNames(t *testing.T) {
	sp := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"ALLOW_CUST": {
				Rules: []*spec.RoutePolicyRule{
					{Sequence: 10, Action: "permit", PrefixList: "RFC1918"},
					{Sequence: 20, Action: "deny"},
				},
			},
		},
		prefixLists: map[string][]string{
			"RFC1918": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		},
	}
	iface := newTestInterface(sp, "Ethernet0")

	entries, rmName := iface.createRoutePolicy("TRANSIT", "import", "ALLOW_CUST", "", "")

	// Route-map name must contain content hash
	if rmName == "TRANSIT_IMPORT" {
		t.Fatal("route-map name should include content hash, got bare name without hash")
	}
	if len(rmName) <= len("TRANSIT_IMPORT_") {
		t.Fatalf("route-map name too short (no hash?): %s", rmName)
	}

	// Verify entries exist
	var foundPrefixSet, foundRouteMap bool
	for _, e := range entries {
		if e.Table == "PREFIX_SET" {
			foundPrefixSet = true
			// PREFIX_SET key should contain a hash
			if len(e.Key) <= len("TRANSIT_IMPORT_PL_10|10") {
				t.Errorf("PREFIX_SET key too short (no hash?): %s", e.Key)
			}
		}
		if e.Table == "ROUTE_MAP" {
			foundRouteMap = true
			// Route-map key should use the hashed name
			if e.Key[:len(rmName)] != rmName {
				t.Errorf("ROUTE_MAP key %q doesn't start with rmName %q", e.Key, rmName)
			}
		}
	}
	if !foundPrefixSet {
		t.Error("no PREFIX_SET entries generated")
	}
	if !foundRouteMap {
		t.Error("no ROUTE_MAP entries generated")
	}
}

func TestCreateRoutePolicy_DifferentContentDifferentHash(t *testing.T) {
	sp1 := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{{Sequence: 10, Action: "permit"}}},
		},
		prefixLists: map[string][]string{},
	}
	sp2 := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{{Sequence: 10, Action: "deny"}}},
		},
		prefixLists: map[string][]string{},
	}

	iface1 := newTestInterface(sp1, "Ethernet0")
	iface2 := newTestInterface(sp2, "Ethernet0")

	_, name1 := iface1.createRoutePolicy("SVC", "import", "POL", "", "")
	_, name2 := iface2.createRoutePolicy("SVC", "import", "POL", "", "")

	if name1 == name2 {
		t.Errorf("different policy content should produce different hashes, both got %s", name1)
	}
}

func TestCreateRoutePolicy_SameContentSameHash(t *testing.T) {
	sp := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{{Sequence: 10, Action: "permit"}}},
		},
		prefixLists: map[string][]string{},
	}

	iface := newTestInterface(sp, "Ethernet0")

	_, name1 := iface.createRoutePolicy("SVC", "import", "POL", "", "")
	_, name2 := iface.createRoutePolicy("SVC", "import", "POL", "", "")

	if name1 != name2 {
		t.Errorf("same content should produce same hash: %s != %s", name1, name2)
	}
}

func TestCreateRoutePolicy_MerkleHashCascade(t *testing.T) {
	// Changing the prefix list content should change the PREFIX_SET hash,
	// which changes the ROUTE_MAP match_prefix_set field, which changes
	// the ROUTE_MAP hash — bottom-up Merkle cascade.
	sp1 := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{{Sequence: 10, Action: "permit", PrefixList: "PL1"}}},
		},
		prefixLists: map[string][]string{
			"PL1": {"10.0.0.0/8"},
		},
	}
	sp2 := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{{Sequence: 10, Action: "permit", PrefixList: "PL1"}}},
		},
		prefixLists: map[string][]string{
			"PL1": {"10.0.0.0/8", "172.16.0.0/12"},
		},
	}

	iface1 := newTestInterface(sp1, "Ethernet0")
	iface2 := newTestInterface(sp2, "Ethernet0")

	_, rmName1 := iface1.createRoutePolicy("SVC", "import", "POL", "", "")
	_, rmName2 := iface2.createRoutePolicy("SVC", "import", "POL", "", "")

	if rmName1 == rmName2 {
		t.Errorf("changing prefix list content should cascade to different route-map hash, both got %s", rmName1)
	}
}

func TestCreateRoutePolicy_WithCommunity(t *testing.T) {
	sp := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{
				{Sequence: 10, Action: "permit", Community: "65000:100"},
			}},
		},
		prefixLists: map[string][]string{},
	}
	iface := newTestInterface(sp, "Ethernet0")

	entries, rmName := iface.createRoutePolicy("SVC", "import", "POL", "", "")

	if rmName == "" {
		t.Fatal("expected non-empty route-map name")
	}

	var foundCS bool
	for _, e := range entries {
		if e.Table == "COMMUNITY_SET" {
			foundCS = true
			// COMMUNITY_SET key should contain a hash
			if e.Fields["community_member"] != "65000:100" {
				t.Errorf("unexpected community_member: %s", e.Fields["community_member"])
			}
		}
	}
	if !foundCS {
		t.Error("no COMMUNITY_SET entry generated")
	}
}

func TestCreateInlineRoutePolicy_ContentHashedNames(t *testing.T) {
	sp := &testSpecProvider{
		prefixLists: map[string][]string{
			"ALLOWED": {"10.1.0.0/16", "10.2.0.0/16"},
		},
	}
	iface := newTestInterface(sp, "Ethernet0")

	entries, rmName := iface.createInlineRoutePolicy("SVC", "import", "", "ALLOWED")

	if rmName == "SVC_IMPORT" {
		t.Fatal("inline route-map name should include content hash")
	}

	var foundPL, foundRM bool
	for _, e := range entries {
		if e.Table == "PREFIX_SET" {
			foundPL = true
		}
		if e.Table == "ROUTE_MAP" {
			foundRM = true
		}
	}
	if !foundPL {
		t.Error("no PREFIX_SET entries generated")
	}
	if !foundRM {
		t.Error("no ROUTE_MAP entries generated")
	}
}

func TestCreateHashedPrefixSet_ContentHash(t *testing.T) {
	sp := &testSpecProvider{
		prefixLists: map[string][]string{
			"PL1": {"10.0.0.0/8", "172.16.0.0/12"},
		},
	}
	iface := newTestInterface(sp, "Ethernet0")

	entries, name := iface.createHashedPrefixSet("TEST_PL", "PL1")

	if name == "TEST_PL" {
		t.Fatal("prefix set name should include content hash")
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 PREFIX_SET entries, got %d", len(entries))
	}

	// Verify determinism
	_, name2 := iface.createHashedPrefixSet("TEST_PL", "PL1")
	if name != name2 {
		t.Errorf("same content should produce same hash: %s != %s", name, name2)
	}
}

func TestCreateRoutePolicy_ExtraCommunityAndPrefixList(t *testing.T) {
	sp := &testSpecProvider{
		routePolicies: map[string]*spec.RoutePolicy{
			"POL": {Rules: []*spec.RoutePolicyRule{
				{Sequence: 10, Action: "permit"},
			}},
		},
		prefixLists: map[string][]string{
			"EXTRA_PL": {"192.168.0.0/16"},
		},
	}
	iface := newTestInterface(sp, "Ethernet0")

	entries, rmName := iface.createRoutePolicy("SVC", "export", "POL", "65000:200", "EXTRA_PL")

	if rmName == "" {
		t.Fatal("expected non-empty route-map name")
	}

	// Should have: PREFIX_SET (extra), COMMUNITY_SET (extra), ROUTE_MAP entries
	tables := map[string]int{}
	for _, e := range entries {
		tables[e.Table]++
	}
	if tables["COMMUNITY_SET"] == 0 {
		t.Error("expected COMMUNITY_SET entry for extra community")
	}
	if tables["PREFIX_SET"] == 0 {
		t.Error("expected PREFIX_SET entry for extra prefix list")
	}
	// ROUTE_MAP: rule 10 + extra community (9000) + extra prefix (9100) = 3
	if tables["ROUTE_MAP"] != 3 {
		t.Errorf("expected 3 ROUTE_MAP entries, got %d", tables["ROUTE_MAP"])
	}
}

func TestScanExistingRoutePolicies_OfflineMode(t *testing.T) {
	// Verify that scanExistingRoutePolicies in offline mode correctly
	// delegates to deleteRoutePoliciesConfig (reads shadow configDB).
	n := testDevice()
	n.offline = true
	n.configDB.RouteMap["SVC_IMPORT_A1B2C3D4|10"] = sonic.RouteMapEntry{}
	n.configDB.RouteMap["SVC_IMPORT_A1B2C3D4|20"] = sonic.RouteMapEntry{}
	n.configDB.PrefixSet["SVC_IMPORT_PL_10_FFFF|10"] = sonic.PrefixSetEntry{}
	n.configDB.CommunitySet["SVC_IMPORT_CS_10_EEEE"] = sonic.CommunitySetEntry{}
	// Unrelated entries for other services should NOT be returned
	n.configDB.RouteMap["OTHER_IMPORT_DEADBEEF|10"] = sonic.RouteMapEntry{}

	entries, err := n.scanExistingRoutePolicies("SVC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tables := map[string]int{}
	for _, e := range entries {
		tables[e.Table]++
	}
	if tables["ROUTE_MAP"] != 2 {
		t.Errorf("expected 2 ROUTE_MAP entries, got %d", tables["ROUTE_MAP"])
	}
	if tables["PREFIX_SET"] != 1 {
		t.Errorf("expected 1 PREFIX_SET entry, got %d", tables["PREFIX_SET"])
	}
	if tables["COMMUNITY_SET"] != 1 {
		t.Errorf("expected 1 COMMUNITY_SET entry, got %d", tables["COMMUNITY_SET"])
	}
	// Total should be 4 (not 5 — OTHER_IMPORT should be excluded)
	if len(entries) != 4 {
		t.Errorf("expected 4 total entries, got %d", len(entries))
	}
}

func TestRefreshService_CleansUpStaleRoutePolicies(t *testing.T) {
	// Simulate: interface has service with old-hash route policies, spec changes,
	// RefreshService should create new-hash objects and delete old ones.
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol:     spec.RoutingProtocolBGP,
					PeerAS:       "65002",
					ImportPolicy: "ALLOW_ALL",
				},
			},
		},
		routePolicies: map[string]*spec.RoutePolicy{
			"ALLOW_ALL": {
				Rules: []*spec.RoutePolicyRule{
					{Sequence: 10, Action: "permit"},
				},
			},
		},
		prefixLists: map[string][]string{},
	}

	n := &Node{
		SpecProvider: sp,
		name:         "test-dev",
		offline:      true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:    "10.255.0.1",
			LoopbackIP:  "10.255.0.1",
		},
		interfaces: make(map[string]*Interface),
		configDB:   sonic.NewEmptyConfigDB(),
	}

	// Register a port and set up the interface
	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
	iface, _ := n.GetInterface("Ethernet0")

	// Step 1: Apply the service — creates route policies with hash v1
	ctx := t.Context()
	applyCS, err := iface.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{
		IPAddress: "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("ApplyService failed: %v", err)
	}

	// Verify route map was created
	var originalRMName string
	for _, c := range applyCS.Changes {
		if c.Table == "ROUTE_MAP" {
			originalRMName = strings.SplitN(c.Key, "|", 2)[0]
			break
		}
	}
	if originalRMName == "" {
		t.Fatal("ApplyService did not create any ROUTE_MAP entries")
	}

	// Verify route_map_in is stored in binding
	binding := n.configDB.NewtronServiceBinding["Ethernet0"]
	if binding.RouteMapIn == "" {
		t.Fatal("binding.RouteMapIn is empty — route map name not stored in binding")
	}

	// Step 2: Change the route policy spec (different content → different hash)
	sp.routePolicies["ALLOW_ALL"] = &spec.RoutePolicy{
		Rules: []*spec.RoutePolicyRule{
			{Sequence: 10, Action: "permit", Set: &spec.RoutePolicySet{LocalPref: 200}},
		},
	}

	// Step 3: RefreshService — should create new-hash objects and clean up old ones
	refreshCS, err := iface.RefreshService(ctx)
	if err != nil {
		t.Fatalf("RefreshService failed: %v", err)
	}

	// Find new route map name (from add operations)
	var newRMName string
	for _, c := range refreshCS.Changes {
		if c.Table == "ROUTE_MAP" && c.Type == sonic.ChangeTypeAdd {
			newRMName = strings.SplitN(c.Key, "|", 2)[0]
			break
		}
	}
	if newRMName == "" {
		t.Fatal("RefreshService did not create any new ROUTE_MAP entries")
	}
	if newRMName == originalRMName {
		t.Fatal("route map name did not change after spec change — hash should differ")
	}

	// Verify old route map is being deleted
	var oldRMDeleted bool
	for _, c := range refreshCS.Changes {
		if c.Table == "ROUTE_MAP" && c.Type == sonic.ChangeTypeDelete {
			if strings.HasPrefix(c.Key, originalRMName) {
				oldRMDeleted = true
				break
			}
		}
	}
	if !oldRMDeleted {
		t.Errorf("stale route map %s was not deleted by RefreshService", originalRMName)
	}
}

func TestRefreshService_NoStaleCleanupWhenHashUnchanged(t *testing.T) {
	// When the spec hasn't changed, RefreshService should NOT produce
	// any stale cleanup deletes for route policies.
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol:     spec.RoutingProtocolBGP,
					PeerAS:       "65002",
					ImportPolicy: "ALLOW_ALL",
				},
			},
		},
		routePolicies: map[string]*spec.RoutePolicy{
			"ALLOW_ALL": {
				Rules: []*spec.RoutePolicyRule{
					{Sequence: 10, Action: "permit"},
				},
			},
		},
		prefixLists: map[string][]string{},
	}

	n := &Node{
		SpecProvider: sp,
		name:         "test-dev",
		offline:      true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:    "10.255.0.1",
			LoopbackIP:  "10.255.0.1",
		},
		interfaces: make(map[string]*Interface),
		configDB:   sonic.NewEmptyConfigDB(),
	}

	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
	iface, _ := n.GetInterface("Ethernet0")

	// Apply the service
	ctx := t.Context()
	_, err := iface.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{
		IPAddress: "10.1.0.0/31",
	})
	if err != nil {
		t.Fatalf("ApplyService failed: %v", err)
	}

	// Refresh WITHOUT changing spec — same hash, no stale cleanup needed
	refreshCS, err := iface.RefreshService(ctx)
	if err != nil {
		t.Fatalf("RefreshService failed: %v", err)
	}

	// Count route policy deletes that aren't matched by a subsequent add
	// (i.e., truly stale deletes, not the remove+add cycle)
	adds := make(map[string]bool)
	deletes := make(map[string]bool)
	for _, c := range refreshCS.Changes {
		switch c.Table {
		case "ROUTE_MAP", "PREFIX_SET", "COMMUNITY_SET":
			key := c.Table + "|" + c.Key
			if c.Type == sonic.ChangeTypeAdd {
				adds[key] = true
			} else if c.Type == sonic.ChangeTypeDelete {
				deletes[key] = true
			}
		}
	}

	// Every delete should have a matching add (remove+apply cycle).
	// No orphaned deletes = no stale cleanup.
	for key := range deletes {
		if !adds[key] {
			t.Errorf("stale delete without matching add: %s — spec didn't change, no cleanup should occur", key)
		}
	}
}

func TestRefreshService_PreservesTopologyParams(t *testing.T) {
	// Verify that route_reflector_client and next_hop_self from topology params
	// survive the remove+reapply cycle in RefreshService (Principle 8: binding self-sufficiency).
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"OVERLAY": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   "65002",
				},
			},
		},
		routePolicies: map[string]*spec.RoutePolicy{},
		prefixLists:   map[string][]string{},
	}

	n := &Node{
		SpecProvider: sp,
		name:         "test-dev",
		offline:      true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:    "10.255.0.1",
			LoopbackIP:  "10.255.0.1",
		},
		interfaces: make(map[string]*Interface),
		configDB:   sonic.NewEmptyConfigDB(),
	}

	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
	iface, _ := n.GetInterface("Ethernet0")

	// Apply with topology params
	ctx := t.Context()
	_, err := iface.ApplyService(ctx, "OVERLAY", ApplyServiceOpts{
		IPAddress: "10.1.0.0/31",
		PeerAS:    65002,
		Params: map[string]string{
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		},
	})
	if err != nil {
		t.Fatalf("ApplyService failed: %v", err)
	}

	// Verify binding has topology params stored
	b := iface.binding()
	if b.RouteReflectorClient != "true" {
		t.Errorf("binding route_reflector_client = %q, want %q", b.RouteReflectorClient, "true")
	}
	if b.NextHopSelf != "true" {
		t.Errorf("binding next_hop_self = %q, want %q", b.NextHopSelf, "true")
	}

	// RefreshService should preserve these params
	refreshCS, err := iface.RefreshService(ctx)
	if err != nil {
		t.Fatalf("RefreshService failed: %v", err)
	}

	// Check the reapply phase wrote BGP_NEIGHBOR_AF with rrclient and nhself
	var foundRRC, foundNHS bool
	for _, c := range refreshCS.Changes {
		if c.Table == "BGP_NEIGHBOR_AF" && c.Type == sonic.ChangeTypeAdd {
			if c.Fields["rrclient"] == "true" {
				foundRRC = true
			}
			if c.Fields["nhself"] == "true" {
				foundNHS = true
			}
		}
	}
	if !foundRRC {
		t.Error("RefreshService lost route_reflector_client — BGP_NEIGHBOR_AF should have rrclient=true")
	}
	if !foundNHS {
		t.Error("RefreshService lost next_hop_self — BGP_NEIGHBOR_AF should have nhself=true")
	}

	// Verify new binding still has params
	b = iface.binding()
	if b.RouteReflectorClient != "true" {
		t.Errorf("post-refresh binding route_reflector_client = %q, want %q", b.RouteReflectorClient, "true")
	}
	if b.NextHopSelf != "true" {
		t.Errorf("post-refresh binding next_hop_self = %q, want %q", b.NextHopSelf, "true")
	}
}

func TestBlueGreenPolicyMigration_TwoInterfaces(t *testing.T) {
	// Blue-green migration: two interfaces share a service with route policies.
	// Spec changes. Refresh interface 1 → old-hash objects deleted, new-hash created,
	// peer group AF updated. Refresh interface 2 → no stale cleanup needed (already done).
	// This tests the multi-interface coexistence path from DESIGN_PRINCIPLES §35.
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol:     spec.RoutingProtocolBGP,
					PeerAS:       "65002",
					ImportPolicy: "ALLOW_ALL",
				},
			},
		},
		routePolicies: map[string]*spec.RoutePolicy{
			"ALLOW_ALL": {
				Rules: []*spec.RoutePolicyRule{
					{Sequence: 10, Action: "permit"},
				},
			},
		},
		prefixLists: map[string][]string{},
	}

	n := &Node{
		SpecProvider: sp,
		name:         "test-dev",
		offline:      true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:    "10.255.0.1",
			LoopbackIP:  "10.255.0.1",
		},
		interfaces: make(map[string]*Interface),
		configDB:   sonic.NewEmptyConfigDB(),
	}

	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
	n.RegisterPort("Ethernet4", map[string]string{"admin_status": "up"})

	ctx := t.Context()

	// Step 1: Apply service on both interfaces
	iface0, _ := n.GetInterface("Ethernet0")
	_, err := iface0.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{IPAddress: "10.1.0.0/31"})
	if err != nil {
		t.Fatalf("ApplyService on Ethernet0 failed: %v", err)
	}

	iface4, _ := n.GetInterface("Ethernet4")
	_, err = iface4.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{IPAddress: "10.1.0.2/31"})
	if err != nil {
		t.Fatalf("ApplyService on Ethernet4 failed: %v", err)
	}

	// Capture the original route map name from the binding
	b0 := n.configDB.NewtronServiceBinding["Ethernet0"]
	originalRM := b0.RouteMapIn
	if originalRM == "" {
		t.Fatal("Ethernet0 binding has no route_map_in after apply")
	}

	// Verify both interfaces share the same route map via peer group
	b4 := n.configDB.NewtronServiceBinding["Ethernet4"]
	if b4.RouteMapIn != originalRM {
		t.Fatalf("expected both interfaces to share route map %s, but Ethernet4 has %s", originalRM, b4.RouteMapIn)
	}

	// Step 2: Change the route policy spec (different content → different hash)
	sp.routePolicies["ALLOW_ALL"] = &spec.RoutePolicy{
		Rules: []*spec.RoutePolicyRule{
			{Sequence: 10, Action: "permit", Set: &spec.RoutePolicySet{LocalPref: 200}},
		},
	}

	// Step 3: Refresh Ethernet0 — should migrate to new-hash and clean up old
	refreshCS0, err := iface0.RefreshService(ctx)
	if err != nil {
		t.Fatalf("RefreshService on Ethernet0 failed: %v", err)
	}

	// Find the new route map name
	var newRM string
	for _, c := range refreshCS0.Changes {
		if c.Table == "ROUTE_MAP" && c.Type == sonic.ChangeTypeAdd {
			newRM = strings.SplitN(c.Key, "|", 2)[0]
			break
		}
	}
	if newRM == "" {
		t.Fatal("RefreshService did not create new ROUTE_MAP entries")
	}
	if newRM == originalRM {
		t.Fatal("route map hash should change after spec change")
	}

	// Verify old route map was deleted in the changeset
	var oldRMDeleted bool
	for _, c := range refreshCS0.Changes {
		if c.Table == "ROUTE_MAP" && c.Type == sonic.ChangeTypeDelete {
			if strings.HasPrefix(c.Key, originalRM) {
				oldRMDeleted = true
				break
			}
		}
	}
	if !oldRMDeleted {
		t.Errorf("stale route map %s was not cleaned up after first interface refresh", originalRM)
	}

	// Verify shadow ConfigDB no longer has old route map entries
	for key := range n.configDB.RouteMap {
		if strings.HasPrefix(key, originalRM) {
			t.Errorf("shadow ConfigDB still contains old route map entry: %s", key)
		}
	}

	// Step 4: Refresh Ethernet4 — should produce no stale cleanup (already done by Ethernet0)
	refreshCS4, err := iface4.RefreshService(ctx)
	if err != nil {
		t.Fatalf("RefreshService on Ethernet4 failed: %v", err)
	}

	// Count policy deletes that are NOT part of the remove+add cycle
	adds := make(map[string]bool)
	deletes := make(map[string]bool)
	for _, c := range refreshCS4.Changes {
		switch c.Table {
		case "ROUTE_MAP", "PREFIX_SET", "COMMUNITY_SET":
			key := c.Table + "|" + c.Key
			if c.Type == sonic.ChangeTypeAdd {
				adds[key] = true
			} else if c.Type == sonic.ChangeTypeDelete {
				deletes[key] = true
			}
		}
	}

	for key := range deletes {
		if !adds[key] {
			t.Errorf("Ethernet4 refresh produced orphan delete %s — stale cleanup should already be done", key)
		}
	}

	// Verify both interfaces now reference the new route map
	b0After := n.configDB.NewtronServiceBinding["Ethernet0"]
	b4After := n.configDB.NewtronServiceBinding["Ethernet4"]
	if b0After.RouteMapIn != newRM {
		t.Errorf("Ethernet0 binding should reference new route map %s, got %s", newRM, b0After.RouteMapIn)
	}
	if b4After.RouteMapIn != newRM {
		t.Errorf("Ethernet4 binding should reference new route map %s, got %s", newRM, b4After.RouteMapIn)
	}
}

func TestBGPPeerGroup_CreateOnFirst_DeleteOnLast(t *testing.T) {
	// Principle §36: Peer groups are created on first ApplyService for a service
	// with BGP routing, and deleted when the last interface removes that service.
	sp := &testSpecProvider{
		services: map[string]*spec.ServiceSpec{
			"TRANSIT": {
				ServiceType: spec.ServiceTypeRouted,
				Routing: &spec.RoutingSpec{
					Protocol: spec.RoutingProtocolBGP,
					PeerAS:   "65002",
				},
			},
		},
		prefixLists:   map[string][]string{},
		routePolicies: map[string]*spec.RoutePolicy{},
	}

	n := &Node{
		SpecProvider: sp,
		name:         "test-dev",
		offline:      true,
		resolved: &spec.ResolvedProfile{
			UnderlayASN: 64512,
			RouterID:    "10.255.0.1",
			LoopbackIP:  "10.255.0.1",
		},
		interfaces: make(map[string]*Interface),
		configDB:   sonic.NewEmptyConfigDB(),
	}

	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
	n.RegisterPort("Ethernet4", map[string]string{"admin_status": "up"})

	ctx := t.Context()

	// Step 1: Apply service on first interface → peer group created
	iface0, _ := n.GetInterface("Ethernet0")
	cs0, err := iface0.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{IPAddress: "10.1.0.0/31"})
	if err != nil {
		t.Fatalf("ApplyService on Ethernet0 failed: %v", err)
	}

	pgKey := BGPPeerGroupKey("default", "TRANSIT")
	hasPGAdd := false
	for _, c := range cs0.Changes {
		if c.Table == "BGP_PEER_GROUP" && c.Key == pgKey && c.Type != sonic.ChangeTypeDelete {
			hasPGAdd = true
		}
	}
	if !hasPGAdd {
		t.Error("First ApplyService should create BGP_PEER_GROUP")
	}
	if _, exists := n.configDB.BGPPeerGroup[pgKey]; !exists {
		t.Error("BGP_PEER_GROUP should exist in shadow ConfigDB after first apply")
	}

	// Step 2: Apply service on second interface → peer group reused (not recreated)
	iface4, _ := n.GetInterface("Ethernet4")
	cs4, err := iface4.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{IPAddress: "10.1.0.2/31"})
	if err != nil {
		t.Fatalf("ApplyService on Ethernet4 failed: %v", err)
	}

	pgAdds := 0
	for _, c := range cs4.Changes {
		if c.Table == "BGP_PEER_GROUP" && c.Key == pgKey && c.Type != sonic.ChangeTypeDelete {
			pgAdds++
		}
	}
	if pgAdds != 0 {
		t.Errorf("Second ApplyService should NOT recreate BGP_PEER_GROUP, got %d adds", pgAdds)
	}

	// Step 3: Remove service from first interface → peer group persists
	cs0r, err := iface0.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService on Ethernet0 failed: %v", err)
	}

	pgDeletes := 0
	for _, c := range cs0r.Changes {
		if c.Table == "BGP_PEER_GROUP" && c.Key == pgKey && c.Type == sonic.ChangeTypeDelete {
			pgDeletes++
		}
	}
	if pgDeletes != 0 {
		t.Error("RemoveService on first interface should NOT delete peer group (second still uses it)")
	}
	if _, exists := n.configDB.BGPPeerGroup[pgKey]; !exists {
		t.Error("BGP_PEER_GROUP should still exist after first remove")
	}

	// Step 4: Remove service from last interface → peer group deleted
	cs4r, err := iface4.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService on Ethernet4 failed: %v", err)
	}

	hasPGDelete := false
	for _, c := range cs4r.Changes {
		if c.Table == "BGP_PEER_GROUP" && c.Key == pgKey && c.Type == sonic.ChangeTypeDelete {
			hasPGDelete = true
		}
	}
	if !hasPGDelete {
		t.Error("RemoveService on last interface should delete BGP_PEER_GROUP")
	}
}

func TestDeleteRoutePoliciesConfig_FindsHashedNames(t *testing.T) {
	// Verify that deleteRoutePoliciesConfig scans by prefix and finds
	// content-hashed route map, prefix set, and community set entries.
	n := testDevice()
	n.configDB.RouteMap["SVC_IMPORT_A1B2C3D4|10"] = sonic.RouteMapEntry{}
	n.configDB.RouteMap["SVC_IMPORT_A1B2C3D4|20"] = sonic.RouteMapEntry{}
	n.configDB.PrefixSet["SVC_IMPORT_PL_10_F1E2D3C4|10"] = sonic.PrefixSetEntry{}
	n.configDB.PrefixSet["SVC_IMPORT_PL_10_F1E2D3C4|20"] = sonic.PrefixSetEntry{}
	n.configDB.CommunitySet["SVC_IMPORT_CS_10_B7A4E9F1"] = sonic.CommunitySetEntry{}

	entries := n.deleteRoutePoliciesConfig("SVC")

	tables := map[string]int{}
	for _, e := range entries {
		tables[e.Table]++
	}
	if tables["ROUTE_MAP"] != 2 {
		t.Errorf("expected 2 ROUTE_MAP deletes, got %d", tables["ROUTE_MAP"])
	}
	if tables["PREFIX_SET"] != 2 {
		t.Errorf("expected 2 PREFIX_SET deletes, got %d", tables["PREFIX_SET"])
	}
	if tables["COMMUNITY_SET"] != 1 {
		t.Errorf("expected 1 COMMUNITY_SET delete, got %d", tables["COMMUNITY_SET"])
	}
}
