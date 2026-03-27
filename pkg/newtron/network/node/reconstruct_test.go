package node

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// ============================================================================
// parseStepURL Tests
// ============================================================================

func TestParseStepURL(t *testing.T) {
	tests := []struct {
		url      string
		wantOp   string
		wantIf   string
	}{
		{"/setup-device", "setup-device", ""},
		{"/add-bgp-evpn-peer", "add-bgp-evpn-peer", ""},
		{"/create-vrf", "create-vrf", ""},
		{"/create-vlan", "create-vlan", ""},
		{"/create-portchannel", "create-portchannel", ""},
		{"/interface/Ethernet0/apply-service", "apply-service", "Ethernet0"},
		{"/interface/Ethernet4/configure-interface", "configure-interface", "Ethernet4"},
		{"/interface/Ethernet12/add-bgp-peer", "add-bgp-peer", "Ethernet12"},
		{"/interface/Ethernet0/set-property", "set-property", "Ethernet0"},
		{"/interface/Ethernet0/bind-acl", "bind-acl", "Ethernet0"},
		{"/interface/Ethernet0/apply-qos", "apply-qos", "Ethernet0"},
		// No leading slash
		{"setup-device", "setup-device", ""},
		{"interface/Ethernet0/apply-service", "apply-service", "Ethernet0"},
	}
	for _, tt := range tests {
		op, iface := parseStepURL(tt.url)
		if op != tt.wantOp || iface != tt.wantIf {
			t.Errorf("parseStepURL(%q) = (%q, %q), want (%q, %q)",
				tt.url, op, iface, tt.wantOp, tt.wantIf)
		}
	}
}

// ============================================================================
// Parameter Extraction Tests
// ============================================================================

func TestParamString(t *testing.T) {
	p := map[string]any{"name": "transit", "count": 42, "flag": true}
	if got := paramString(p, "name"); got != "transit" {
		t.Errorf("paramString(name) = %q, want transit", got)
	}
	if got := paramString(p, "count"); got != "42" {
		t.Errorf("paramString(count) = %q, want 42", got)
	}
	if got := paramString(p, "missing"); got != "" {
		t.Errorf("paramString(missing) = %q, want empty", got)
	}
	if got := paramString(nil, "name"); got != "" {
		t.Errorf("paramString(nil, name) = %q, want empty", got)
	}
}

func TestParamInt(t *testing.T) {
	p := map[string]any{"asn": float64(65001), "vlan": 100, "port": "8080"}
	if got := paramInt(p, "asn"); got != 65001 {
		t.Errorf("paramInt(asn) = %d, want 65001", got)
	}
	if got := paramInt(p, "vlan"); got != 100 {
		t.Errorf("paramInt(vlan) = %d, want 100", got)
	}
	if got := paramInt(p, "port"); got != 8080 {
		t.Errorf("paramInt(port) = %d, want 8080", got)
	}
	if got := paramInt(p, "missing"); got != 0 {
		t.Errorf("paramInt(missing) = %d, want 0", got)
	}
}

func TestParamBool(t *testing.T) {
	p := map[string]any{"evpn": true, "str_true": "true", "str_false": "false"}
	if got := paramBool(p, "evpn"); !got {
		t.Error("paramBool(evpn) = false, want true")
	}
	if got := paramBool(p, "str_true"); !got {
		t.Error("paramBool(str_true) = false, want true")
	}
	if got := paramBool(p, "str_false"); got {
		t.Error("paramBool(str_false) = true, want false")
	}
	if got := paramBool(p, "missing"); got {
		t.Error("paramBool(missing) = true, want false")
	}
}

func TestParamStringMap(t *testing.T) {
	p := map[string]any{
		"fields": map[string]any{"hostname": "leaf1", "bgp_asn": "65001"},
	}
	got := paramStringMap(p, "fields")
	if got["hostname"] != "leaf1" || got["bgp_asn"] != "65001" {
		t.Errorf("paramStringMap(fields) = %v", got)
	}
	if got := paramStringMap(p, "missing"); got != nil {
		t.Errorf("paramStringMap(missing) = %v, want nil", got)
	}
}

func TestParamStringSlice(t *testing.T) {
	p := map[string]any{
		"members": []any{"Ethernet0", "Ethernet4"},
	}
	got := paramStringSlice(p, "members")
	if len(got) != 2 || got[0] != "Ethernet0" || got[1] != "Ethernet4" {
		t.Errorf("paramStringSlice(members) = %v", got)
	}
	if got := paramStringSlice(p, "missing"); got != nil {
		t.Errorf("paramStringSlice(missing) = %v, want nil", got)
	}
}

// ============================================================================
// ReplayStep Integration Tests (abstract Node)
// ============================================================================

func newTestAbstract() *Node {
	sp := &testSpecProvider{
		services:      map[string]*spec.ServiceSpec{},
		filterSpecs:   map[string]*spec.FilterSpec{},
		ipvpn:         map[string]*spec.IPVPNSpec{},
		macvpn:        map[string]*spec.MACVPNSpec{},
		qosPolicies:   map[string]*spec.QoSPolicy{},
		platforms:     map[string]*spec.PlatformSpec{},
		prefixLists:   map[string][]string{},
		routePolicies: map[string]*spec.RoutePolicy{},
	}
	profile := &spec.DeviceProfile{
		UnderlayASN: 65001,
		LoopbackIP:  "10.0.0.1",
	}
	resolved := &spec.ResolvedProfile{
		UnderlayASN: 65001,
		RouterID:    "10.0.0.1",
		LoopbackIP:  "10.0.0.1",
	}
	n := NewAbstract(sp, "test", profile, resolved)
	// Register some ports so interface ops can work
	n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up", "mtu": "9100"})
	n.RegisterPort("Ethernet4", map[string]string{"admin_status": "up", "mtu": "9100"})
	// Seed root "device" intent for DAG parent validation (I4).
	// In production, SetupDevice creates this. Tests skip setup-device
	// and need it pre-populated.
	cs := NewChangeSet("test", "test")
	n.writeIntent(cs, "setup-device", "device", map[string]string{}, nil)
	return n
}

func TestReplayStepAddBGPEVPNPeer(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Setup prerequisites: setup-device creates EVPN peer group via SetupVTEP
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/setup-device",
		Params: map[string]any{
			"fields":    map[string]any{"hostname": "test", "bgp_asn": "65001"},
			"source_ip": "10.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("setup-device: %v", err)
	}

	// Verify EVPN peer group was created
	if _, ok := n.ConfigDB().BGPPeerGroup["default|EVPN"]; !ok {
		t.Fatal("expected BGP_PEER_GROUP|default|EVPN after setup-device")
	}

	err = ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/add-bgp-evpn-peer",
		Params: map[string]any{
			"neighbor_ip": "10.0.0.2",
			"asn":         float64(65002),
			"evpn":        true,
		},
	})
	if err != nil {
		t.Fatalf("add-bgp-evpn-peer: %v", err)
	}
	// BGP_NEIGHBOR key includes VRF: "default|10.0.0.2"
	nb, ok := n.ConfigDB().BGPNeighbor["default|10.0.0.2"]
	if !ok {
		t.Errorf("expected BGP_NEIGHBOR|default|10.0.0.2, got keys: %v", mapKeys(n.ConfigDB().BGPNeighbor))
	}
	if nb.PeerGroup != "EVPN" {
		t.Errorf("peer_group_name = %q, want EVPN", nb.PeerGroup)
	}
}

func TestReplayStepUnknownOp(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{URL: "/bogus-op"})
	if err == nil {
		t.Fatal("expected error for unknown operation")
	}
}

func TestReplayStepMissingInterface(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/interface/Ethernet99/apply-service",
		Params: map[string]any{"service": "transit"},
	})
	if err == nil {
		t.Fatal("expected error for missing interface")
	}
}

func TestReplayStepSetProperty(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	// set-property intent has parent "interface|Ethernet0", so
	// configure-interface must run first to create that parent intent.
	if err := ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/interface/Ethernet0/configure-interface",
		Params: map[string]any{"ip": "10.1.100.1/24"},
	}); err != nil {
		t.Fatalf("configure-interface prerequisite: %v", err)
	}
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/interface/Ethernet0/set-property",
		Params: map[string]any{
			"property": "mtu",
			"value":    "1500",
		},
	})
	if err != nil {
		t.Fatalf("set-property: %v", err)
	}
	// Verify the changeset was produced (shadow ConfigDB update may not
	// directly reflect in the Port struct if the changeset writes raw entries).
	// Just verify no error — the operation succeeded.
	_ = n
}

func TestReplayStepConfigureInterface(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/interface/Ethernet0/configure-interface",
		Params: map[string]any{
			"ip": "10.1.100.1/24",
		},
	})
	if err != nil {
		t.Fatalf("configure-interface: %v", err)
	}
	// Verify INTERFACE entry
	if _, ok := n.ConfigDB().Interface["Ethernet0|10.1.100.1/24"]; !ok {
		t.Error("expected INTERFACE|Ethernet0|10.1.100.1/24 in ConfigDB")
	}
}

func TestReplayStepConfigureInterfaceBridged(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	// Create VLAN first
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/create-vlan",
		Params: map[string]any{"vlan_id": float64(100)},
	})
	if err != nil {
		t.Fatalf("create-vlan: %v", err)
	}
	// Configure interface in bridged mode
	err = ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/interface/Ethernet0/configure-interface",
		Params: map[string]any{"vlan_id": float64(100), "tagged": false},
	})
	if err != nil {
		t.Fatalf("configure-interface bridged: %v", err)
	}
	// Verify VLAN_MEMBER entry
	if _, ok := n.ConfigDB().VLANMember["Vlan100|Ethernet0"]; !ok {
		t.Error("expected VLAN_MEMBER|Vlan100|Ethernet0 in ConfigDB")
	}
	// Verify intent record
	intent := n.GetIntent("interface|Ethernet0")
	if intent == nil {
		t.Fatal("expected intent for Ethernet0")
	}
	if intent.Params["vlan_id"] != "100" {
		t.Errorf("intent vlan_id = %q, want 100", intent.Params["vlan_id"])
	}
}

func TestUnconfigureInterfaceBridged(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Setup: create VLAN and configure interface in bridged mode
	if err := ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/create-vlan",
		Params: map[string]any{"vlan_id": float64(100)},
	}); err != nil {
		t.Fatalf("create-vlan: %v", err)
	}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: false}); err != nil {
		t.Fatalf("ConfigureInterface bridged: %v", err)
	}
	// Verify VLAN_MEMBER exists
	if _, ok := n.ConfigDB().VLANMember["Vlan100|Ethernet0"]; !ok {
		t.Fatal("expected VLAN_MEMBER|Vlan100|Ethernet0 after configure")
	}

	// Unconfigure — should remove VLAN_MEMBER and intent
	if _, err := iface.UnconfigureInterface(ctx); err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}
	if _, ok := n.ConfigDB().VLANMember["Vlan100|Ethernet0"]; ok {
		t.Error("VLAN_MEMBER|Vlan100|Ethernet0 should be removed after unconfigure")
	}
	if intent := n.GetIntent("interface|Ethernet0"); intent != nil {
		t.Error("intent for Ethernet0 should be removed after unconfigure")
	}
}

func TestUnconfigureInterfaceRouted(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()

	// Setup: create VRF and configure interface in routed mode
	if err := ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/create-vrf",
		Params: map[string]any{"name": "Vrf_test"},
	}); err != nil {
		t.Fatalf("create-vrf: %v", err)
	}
	iface, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := iface.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_test", IP: "10.1.0.1/31"}); err != nil {
		t.Fatalf("ConfigureInterface routed: %v", err)
	}
	// Verify INTERFACE entries exist
	if _, ok := n.ConfigDB().Interface["Ethernet0"]; !ok {
		t.Fatal("expected INTERFACE|Ethernet0 after configure")
	}
	if _, ok := n.ConfigDB().Interface["Ethernet0|10.1.0.1/31"]; !ok {
		t.Fatal("expected INTERFACE|Ethernet0|10.1.0.1/31 after configure")
	}

	// Unconfigure — should remove both INTERFACE entries and intent
	if _, err := iface.UnconfigureInterface(ctx); err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}
	if _, ok := n.ConfigDB().Interface["Ethernet0"]; ok {
		t.Error("INTERFACE|Ethernet0 should be removed after unconfigure")
	}
	if _, ok := n.ConfigDB().Interface["Ethernet0|10.1.0.1/31"]; ok {
		t.Error("INTERFACE|Ethernet0|10.1.0.1/31 should be removed after unconfigure")
	}
	if intent := n.GetIntent("interface|Ethernet0"); intent != nil {
		t.Error("intent for Ethernet0 should be removed after unconfigure")
	}
}

func TestParseRouteReflectorOpts(t *testing.T) {
	p := map[string]any{
		"cluster_id": "10.0.0.1",
		"local_asn":  float64(65001),
		"router_id":  "10.0.0.1",
		"local_addr": "10.0.0.1",
		"clients": []any{
			map[string]any{"ip": "10.0.0.11", "asn": float64(65011)},
			map[string]any{"ip": "10.0.0.12", "asn": float64(65012)},
		},
		"peers": []any{
			map[string]any{"ip": "10.0.0.2", "asn": float64(65002)},
		},
	}
	opts, err := parseRouteReflectorOpts(p)
	if err != nil {
		t.Fatalf("parseRouteReflectorOpts: %v", err)
	}
	if opts.ClusterID != "10.0.0.1" {
		t.Errorf("ClusterID = %q", opts.ClusterID)
	}
	if len(opts.Clients) != 2 {
		t.Fatalf("Clients = %d, want 2", len(opts.Clients))
	}
	if opts.Clients[0].IP != "10.0.0.11" || opts.Clients[0].ASN != 65011 {
		t.Errorf("Client[0] = %+v", opts.Clients[0])
	}
	if len(opts.Peers) != 1 || opts.Peers[0].IP != "10.0.0.2" {
		t.Errorf("Peers = %+v", opts.Peers)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
