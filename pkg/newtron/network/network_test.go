package network

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

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
	netSvc := &spec.ServiceSpec{Description: "network-level"}
	nodeSvc := &spec.ServiceSpec{Description: "node-level"}

	n := &Network{
		spec: &spec.NetworkSpecFile{
			Zones: map[string]*spec.ZoneSpec{
				"amer": {},
			},
			OverridableSpecs: spec.OverridableSpecs{
				Services: map[string]*spec.ServiceSpec{"svc": netSvc},
			},
		},
		platforms: &spec.PlatformSpecFile{
			Platforms: map[string]*spec.PlatformSpec{},
		},
	}

	profile := &spec.DeviceProfile{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{"svc": nodeSvc},
		},
	}

	rs := n.buildResolvedSpecs(profile)
	got, err := rs.GetService("svc")
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

	n := &Network{
		spec: &spec.NetworkSpecFile{
			Zones: map[string]*spec.ZoneSpec{
				"amer": {
					OverridableSpecs: spec.OverridableSpecs{
						Filters: map[string]*spec.FilterSpec{"f1": zoneFilter},
					},
				},
			},
			OverridableSpecs: spec.OverridableSpecs{
				Filters: map[string]*spec.FilterSpec{"f1": netFilter},
			},
		},
		platforms: &spec.PlatformSpecFile{
			Platforms: map[string]*spec.PlatformSpec{},
		},
	}

	profile := &spec.DeviceProfile{Zone: "amer"}

	rs := n.buildResolvedSpecs(profile)
	got, err := rs.GetFilter("f1")
	if err != nil {
		t.Fatalf("GetFilter() failed: %v", err)
	}
	if got.Description != "zone-level" {
		t.Errorf("got description %q, want %q (zone should win over network)", got.Description, "zone-level")
	}
}

func TestResolvedSpecs_MergeUnion(t *testing.T) {
	// Specs from different levels are all visible (union)
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Zones: map[string]*spec.ZoneSpec{
				"amer": {
					OverridableSpecs: spec.OverridableSpecs{
						IPVPNs: map[string]*spec.IPVPNSpec{
							"zone-vpn": {VRF: "Vrf_zone", L3VNI: 20001},
						},
					},
				},
			},
			OverridableSpecs: spec.OverridableSpecs{
				IPVPNs: map[string]*spec.IPVPNSpec{
					"net-vpn": {VRF: "Vrf_net", L3VNI: 10001},
				},
				Services: map[string]*spec.ServiceSpec{
					"net-svc": {Description: "from network"},
				},
			},
		},
		platforms: &spec.PlatformSpecFile{
			Platforms: map[string]*spec.PlatformSpec{},
		},
	}

	profile := &spec.DeviceProfile{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"node-svc": {Description: "from node"},
			},
		},
	}

	rs := n.buildResolvedSpecs(profile)

	// Network-level IPVPN should be visible
	if _, err := rs.GetIPVPN("net-vpn"); err != nil {
		t.Errorf("network-level ipvpn should be visible: %v", err)
	}
	// Zone-level IPVPN should be visible
	if _, err := rs.GetIPVPN("zone-vpn"); err != nil {
		t.Errorf("zone-level ipvpn should be visible: %v", err)
	}
	// Network-level service should be visible
	if _, err := rs.GetService("net-svc"); err != nil {
		t.Errorf("network-level service should be visible: %v", err)
	}
	// Node-level service should be visible
	if _, err := rs.GetService("node-svc"); err != nil {
		t.Errorf("node-level service should be visible: %v", err)
	}
}

func TestResolvedSpecs_FindMACVPNByVNI(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Zones: map[string]*spec.ZoneSpec{
				"amer": {},
			},
			OverridableSpecs: spec.OverridableSpecs{
				MACVPNs: map[string]*spec.MACVPNSpec{
					"net-mac": {VNI: 1000, VlanID: 100},
				},
			},
		},
		platforms: &spec.PlatformSpecFile{
			Platforms: map[string]*spec.PlatformSpec{},
		},
	}

	profile := &spec.DeviceProfile{
		Zone: "amer",
		OverridableSpecs: spec.OverridableSpecs{
			MACVPNs: map[string]*spec.MACVPNSpec{
				"node-mac": {VNI: 2000, VlanID: 200},
			},
		},
	}

	rs := n.buildResolvedSpecs(profile)

	// Find network-level by VNI
	name, def := rs.FindMACVPNByVNI(1000)
	if name != "net-mac" || def == nil {
		t.Errorf("FindMACVPNByVNI(1000) = %q, want %q", name, "net-mac")
	}

	// Find node-level by VNI
	name, def = rs.FindMACVPNByVNI(2000)
	if name != "node-mac" || def == nil {
		t.Errorf("FindMACVPNByVNI(2000) = %q, want %q", name, "node-mac")
	}

	// Not found
	name, def = rs.FindMACVPNByVNI(9999)
	if name != "" || def != nil {
		t.Errorf("FindMACVPNByVNI(9999) should return empty, got %q", name)
	}
}

func TestResolvedSpecs_GetPlatformDelegatesToNetwork(t *testing.T) {
	n := &Network{
		spec: &spec.NetworkSpecFile{
			Zones: map[string]*spec.ZoneSpec{
				"amer": {},
			},
		},
		platforms: &spec.PlatformSpecFile{
			Platforms: map[string]*spec.PlatformSpec{
				"as7726": {HWSKU: "Accton-AS7726-32X"},
			},
		},
	}

	profile := &spec.DeviceProfile{Zone: "amer"}
	rs := n.buildResolvedSpecs(profile)

	p, err := rs.GetPlatform("as7726")
	if err != nil {
		t.Fatalf("GetPlatform() failed: %v", err)
	}
	if p.HWSKU != "Accton-AS7726-32X" {
		t.Errorf("HWSKU = %q, want %q", p.HWSKU, "Accton-AS7726-32X")
	}
}
