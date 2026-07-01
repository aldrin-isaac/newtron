package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// writeSpecJSON marshals v (indented, trailing newline) to path, failing the
// test on error. The test-side mirror of the loader's atomic writer — enough to
// author a spec file a real NewNetwork load then reads back.
func writeSpecJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// loadResolveTest builds a Network from disk (network.json + one zones/<name>.json
// per zones entry) and returns it, so buildResolvedSpecs runs against the real
// loader-backed zone store — the same path production takes. base seeds the
// network-scope OverridableSpecs; each zones entry seeds one zone's override
// bucket. platforms is passed through to NewNetwork (nil for none).
//
// Names are stored canonically after load (hyphens → underscores, uppercased),
// so fixtures here use canonical keys and lookups match what production sees.
func loadResolveTest(t *testing.T, base spec.OverridableSpecs, zones map[string]spec.OverridableSpecs, platforms map[string]*spec.PlatformSpec) *Network {
	t.Helper()
	dir := t.TempDir()
	writeSpecJSON(t, filepath.Join(dir, "network.json"), &spec.NetworkSpecFile{
		Version:          "1.0",
		OverridableSpecs: base,
	})
	if len(zones) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "zones"), 0o755); err != nil {
			t.Fatalf("mkdir zones: %v", err)
		}
		for name, ov := range zones {
			writeSpecJSON(t, filepath.Join(dir, "zones", name+".json"), &spec.ZoneSpec{OverridableSpecs: ov})
		}
	}
	n, err := NewNetwork(dir, "", nil, nil, platforms)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	return n
}

// ============================================================================
// Network ListServices/ListFilters Tests (minimal)
// ============================================================================

func TestNetwork_ListServicesEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			OverridableSpecs: spec.OverridableSpecs{
				Services: make(map[string]*spec.ServiceSpec),
			},
		},
	}
	services := n.ListServices()
	if len(services) != 0 {
		t.Errorf("ListServices() = %v, want empty", services)
	}
}

func TestNetwork_ListFiltersEmpty(t *testing.T) {
	// Test with minimal network (no specs loaded)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			OverridableSpecs: spec.OverridableSpecs{
				Filters: make(map[string]*spec.FilterSpec),
			},
		},
	}
	filters := n.ListFilters()
	if len(filters) != 0 {
		t.Errorf("ListFilters() = %v, want empty", filters)
	}
}

// ============================================================================
// ResolvedSpecs Hierarchical Merge Tests
// ============================================================================

func TestResolvedSpecs_MergeNodeWins(t *testing.T) {
	// Node-level service overrides network-level service with same name
	netSvc := &spec.ServiceSpec{Description: "network-level", ServiceType: "routed"}
	nodeSvc := &spec.ServiceSpec{Description: "node-level", ServiceType: "routed"}

	n := loadResolveTest(t, spec.OverridableSpecs{
		Services: map[string]*spec.ServiceSpec{"SVC": netSvc},
	}, map[string]spec.OverridableSpecs{"amer": {}}, nil)

	nodeSpec := &spec.NodeSpec{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{"SVC": nodeSvc},
		},
	}

	rs := n.buildResolvedSpecs(nodeSpec)
	got, err := rs.GetService("SVC")
	if err != nil {
		t.Fatalf("GetService() failed: %v", err)
	}
	if got.Description != "node-level" {
		t.Errorf("got description %q, want %q (node should win)", got.Description, "node-level")
	}
}

func TestResolvedSpecs_MergeZoneWinsOverNetwork(t *testing.T) {
	// Zone-level filter overrides network-level filter with same name
	netFilter := &spec.FilterSpec{Description: "network-level"}
	zoneFilter := &spec.FilterSpec{Description: "zone-level"}

	n := loadResolveTest(t, spec.OverridableSpecs{
		Filters: map[string]*spec.FilterSpec{"F1": netFilter},
	}, map[string]spec.OverridableSpecs{
		"amer": {Filters: map[string]*spec.FilterSpec{"F1": zoneFilter}},
	}, nil)

	nodeSpec := &spec.NodeSpec{Zone: "amer"}

	rs := n.buildResolvedSpecs(nodeSpec)
	got, err := rs.GetFilter("F1")
	if err != nil {
		t.Fatalf("GetFilter() failed: %v", err)
	}
	if got.Description != "zone-level" {
		t.Errorf("got description %q, want %q (zone should win over network)", got.Description, "zone-level")
	}
}

func TestResolvedSpecs_MergeUnion(t *testing.T) {
	// Specs from different levels are all visible (union)
	n := loadResolveTest(t, spec.OverridableSpecs{
		IPVPNs: map[string]*spec.IPVPNSpec{
			"NET": {L3VNI: 10001},
		},
		Services: map[string]*spec.ServiceSpec{
			"NET_SVC": {Description: "from network", ServiceType: "routed"},
		},
	}, map[string]spec.OverridableSpecs{
		"amer": {IPVPNs: map[string]*spec.IPVPNSpec{"ZONE": {L3VNI: 20001}}},
	}, nil)

	nodeSpec := &spec.NodeSpec{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"NODE_SVC": {Description: "from node", ServiceType: "routed"},
			},
		},
	}

	rs := n.buildResolvedSpecs(nodeSpec)

	// Network-level IPVPN should be visible
	if _, err := rs.GetIPVPN("NET"); err != nil {
		t.Errorf("network-level ipvpn should be visible: %v", err)
	}
	// Zone-level IPVPN should be visible
	if _, err := rs.GetIPVPN("ZONE"); err != nil {
		t.Errorf("zone-level ipvpn should be visible: %v", err)
	}
	// Network-level service should be visible
	if _, err := rs.GetService("NET_SVC"); err != nil {
		t.Errorf("network-level service should be visible: %v", err)
	}
	// Node-level service should be visible
	if _, err := rs.GetService("NODE_SVC"); err != nil {
		t.Errorf("node-level service should be visible: %v", err)
	}
}

func TestResolvedSpecs_FindMACVPNByVNI(t *testing.T) {
	n := loadResolveTest(t, spec.OverridableSpecs{
		MACVPNs: map[string]*spec.MACVPNSpec{
			"NET_MAC": {VNI: 1000, VlanID: 100},
		},
	}, map[string]spec.OverridableSpecs{"amer": {}}, nil)

	nodeSpec := &spec.NodeSpec{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			MACVPNs: map[string]*spec.MACVPNSpec{
				"NODE_MAC": {VNI: 2000, VlanID: 200},
			},
		},
	}

	rs := n.buildResolvedSpecs(nodeSpec)

	// Find network-level by VNI
	name, def := rs.FindMACVPNByVNI(1000)
	if name != "NET_MAC" || def == nil {
		t.Errorf("FindMACVPNByVNI(1000) = %q, want %q", name, "NET_MAC")
	}

	// Find node-level by VNI
	name, def = rs.FindMACVPNByVNI(2000)
	if name != "NODE_MAC" || def == nil {
		t.Errorf("FindMACVPNByVNI(2000) = %q, want %q", name, "NODE_MAC")
	}

	// Not found
	name, def = rs.FindMACVPNByVNI(9999)
	if name != "" || def != nil {
		t.Errorf("FindMACVPNByVNI(9999) should return empty, got %q", name)
	}
}

func TestResolvedSpecs_FindMACVPNByVNI_DynamicFallback(t *testing.T) {
	// §39: MACVPNs added after ResolvedSpecs snapshot must be visible
	// through the live fallback in FindMACVPNByVNI.
	n := loadResolveTest(t, spec.OverridableSpecs{
		MACVPNs: map[string]*spec.MACVPNSpec{
			"EXISTING": {VNI: 1000, VlanID: 100},
		},
	}, map[string]spec.OverridableSpecs{"amer": {}}, nil)

	nodeSpec := &spec.NodeSpec{Zone: "amer"}
	rs := n.buildResolvedSpecs(nodeSpec)

	// Pre-existing MACVPN should be found
	name, def := rs.FindMACVPNByVNI(1000)
	if name != "EXISTING" || def == nil {
		t.Fatalf("FindMACVPNByVNI(1000) = %q, want EXISTING", name)
	}

	// Dynamically add a MACVPN after snapshot build
	n.spec.MACVPNs["DYNAMIC"] = &spec.MACVPNSpec{VNI: 2000, VlanID: 200}

	// Dynamic MACVPN should be visible via live fallback
	name, def = rs.FindMACVPNByVNI(2000)
	if name != "DYNAMIC" || def == nil {
		t.Errorf("FindMACVPNByVNI(2000) should find DYNAMIC via fallback, got %q", name)
	}

	// Non-existent VNI should still return empty
	name, def = rs.FindMACVPNByVNI(9999)
	if name != "" || def != nil {
		t.Errorf("FindMACVPNByVNI(9999) should return empty, got %q", name)
	}
}

func TestResolvedSpecs_LiveFallback_DynamicService(t *testing.T) {
	// §39: Specs added via CreateService after ResolvedSpecs was built
	// must be visible through the live fallback to network.Get*.
	n := loadResolveTest(t, spec.OverridableSpecs{
		Services: map[string]*spec.ServiceSpec{
			"EXISTING": {Description: "pre-existing service", ServiceType: "routed"},
		},
	}, map[string]spec.OverridableSpecs{"amer": {}}, nil)

	nodeSpec := &spec.NodeSpec{Zone: "amer"}
	rs := n.buildResolvedSpecs(nodeSpec)

	// Pre-existing service should be in the merged snapshot
	if _, err := rs.GetService("EXISTING"); err != nil {
		t.Fatalf("pre-existing service should be visible: %v", err)
	}

	// Dynamically add a service (simulates CreateService writing to n.spec.Services)
	n.spec.Services["NEW_DYNAMIC"] = &spec.ServiceSpec{
		Description: "created after snapshot",
		ServiceType: "routed",
	}

	// The dynamically added service should be visible via live fallback
	svc, err := rs.GetService("NEW_DYNAMIC")
	if err != nil {
		t.Fatalf("dynamically added service should be visible via fallback: %v", err)
	}
	if svc.Description != "created after snapshot" {
		t.Errorf("got description %q, want %q", svc.Description, "created after snapshot")
	}

	// Non-existent service should still fail
	if _, err := rs.GetService("NONEXISTENT"); err == nil {
		t.Error("non-existent service should return error")
	}
}

func TestResolvedSpecs_LiveFallback_NodeSpecOverrideStillWins(t *testing.T) {
	// §39: NodeSpec-level override must still win over network-level,
	// even when the network level has been modified after snapshot build.
	n := loadResolveTest(t, spec.OverridableSpecs{
		Services: map[string]*spec.ServiceSpec{
			"SVC": {Description: "network-level", ServiceType: "routed"},
		},
	}, map[string]spec.OverridableSpecs{"amer": {}}, nil)

	nodeSpec := &spec.NodeSpec{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"SVC": {Description: "nodeSpec-level"},
			},
		},
	}

	rs := n.buildResolvedSpecs(nodeSpec)

	// NodeSpec override should win
	svc, err := rs.GetService("SVC")
	if err != nil {
		t.Fatalf("GetService failed: %v", err)
	}
	if svc.Description != "nodeSpec-level" {
		t.Errorf("nodeSpec should win, got %q", svc.Description)
	}

	// Now modify the network-level spec
	n.spec.Services["SVC"] = &spec.ServiceSpec{Description: "network-modified"}

	// NodeSpec override should STILL win (snapshot has it)
	svc, err = rs.GetService("SVC")
	if err != nil {
		t.Fatalf("GetService after network modify failed: %v", err)
	}
	if svc.Description != "nodeSpec-level" {
		t.Errorf("nodeSpec should still win after network modify, got %q", svc.Description)
	}
}

func TestResolvedSpecs_GetPlatformDelegatesToNetwork(t *testing.T) {
	n := loadResolveTest(t, spec.OverridableSpecs{}, map[string]spec.OverridableSpecs{"amer": {}},
		map[string]*spec.PlatformSpec{
			"as7726": {HWSKU: "Accton-AS7726-32X"},
		})

	nodeSpec := &spec.NodeSpec{Zone: "amer"}
	rs := n.buildResolvedSpecs(nodeSpec)

	p, err := rs.GetPlatform("as7726")
	if err != nil {
		t.Fatalf("GetPlatform() failed: %v", err)
	}
	if p.HWSKU != "Accton-AS7726-32X" {
		t.Errorf("HWSKU = %q, want %q", p.HWSKU, "Accton-AS7726-32X")
	}
}
