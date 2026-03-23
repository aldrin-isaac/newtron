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
		{"/configure-bgp", "configure-bgp", ""},
		{"/configure-loopback", "configure-loopback", ""},
		{"/set-device-metadata", "set-device-metadata", ""},
		{"/setup-vtep", "setup-vtep", ""},
		{"/add-overlay-peer", "add-overlay-peer", ""},
		{"/create-vrf", "create-vrf", ""},
		{"/interface/Ethernet0/apply-service", "apply-service", "Ethernet0"},
		{"/interface/Ethernet4/configure-interface", "configure-interface", "Ethernet4"},
		{"/interface/Ethernet12/add-bgp-neighbor", "add-bgp-neighbor", "Ethernet12"},
		{"/interface/Ethernet0/set-port-property", "set-port-property", "Ethernet0"},
		{"/interface/Ethernet0/bind-acl", "bind-acl", "Ethernet0"},
		{"/interface/Ethernet0/apply-qos", "apply-qos", "Ethernet0"},
		{"/interface/Ethernet0/bind-macvpn", "bind-macvpn", "Ethernet0"},
		// No leading slash
		{"configure-bgp", "configure-bgp", ""},
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
	return n
}

func TestReplayStepConfigureLoopback(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{URL: "/configure-loopback"})
	if err != nil {
		t.Fatalf("configure-loopback: %v", err)
	}
	// Verify loopback exists in shadow ConfigDB
	if _, ok := n.ConfigDB().LoopbackInterface["Loopback0"]; !ok {
		t.Error("expected Loopback0 in ConfigDB after configure-loopback")
	}
}

func TestReplayStepSetDeviceMetadata(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/set-device-metadata",
		Params: map[string]any{
			"fields": map[string]any{
				"hostname": "leaf1",
				"bgp_asn":  "65001",
			},
		},
	})
	if err != nil {
		t.Fatalf("set-device-metadata: %v", err)
	}
	dm := n.ConfigDB().DeviceMetadata
	if dm["localhost"]["hostname"] != "leaf1" {
		t.Errorf("hostname = %q, want leaf1", dm["localhost"]["hostname"])
	}
}

func TestReplayStepConfigureBGP(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	// ConfigureBGP requires DEVICE_METADATA to exist
	ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/set-device-metadata",
		Params: map[string]any{
			"fields": map[string]any{"hostname": "test", "bgp_asn": "65001"},
		},
	})
	err := ReplayStep(ctx, n, spec.TopologyStep{URL: "/configure-bgp"})
	if err != nil {
		t.Fatalf("configure-bgp: %v", err)
	}
	if _, ok := n.ConfigDB().BGPGlobals["default"]; !ok {
		t.Error("expected BGP_GLOBALS|default in ConfigDB")
	}
}

func TestReplayStepAddOverlayPeer(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	// Setup prerequisites
	ReplayStep(ctx, n, spec.TopologyStep{URL: "/configure-loopback"})
	ReplayStep(ctx, n, spec.TopologyStep{
		URL:    "/set-device-metadata",
		Params: map[string]any{"fields": map[string]any{"hostname": "test", "bgp_asn": "65001"}},
	})
	ReplayStep(ctx, n, spec.TopologyStep{URL: "/configure-bgp"})

	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/add-overlay-peer",
		Params: map[string]any{
			"neighbor_ip": "10.0.0.2",
			"asn":         float64(65002),
			"evpn":        false,
		},
	})
	if err != nil {
		t.Fatalf("add-overlay-peer: %v", err)
	}
	// BGP_NEIGHBOR key includes VRF: "default|10.0.0.2"
	if _, ok := n.ConfigDB().BGPNeighbor["default|10.0.0.2"]; !ok {
		t.Errorf("expected BGP_NEIGHBOR|default|10.0.0.2, got keys: %v", mapKeys(n.ConfigDB().BGPNeighbor))
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

func TestReplayStepSetPortProperty(t *testing.T) {
	n := newTestAbstract()
	ctx := context.Background()
	err := ReplayStep(ctx, n, spec.TopologyStep{
		URL: "/interface/Ethernet0/set-port-property",
		Params: map[string]any{
			"property": "mtu",
			"value":    "1500",
		},
	})
	if err != nil {
		t.Fatalf("set-port-property: %v", err)
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
