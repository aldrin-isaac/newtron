package network

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// loadScopedTestNetwork builds a Network with one zone "amer" and no specs, so
// scoped-write tests can author network bases and zone overrides from scratch.
func loadScopedTestNetwork(t *testing.T) *Network {
	t.Helper()
	dir, err := os.MkdirTemp("", "scoped-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if err := os.WriteFile(filepath.Join(dir, "network.json"),
		[]byte(`{"schema_version":"1.0","zones":{"amer":{}}}`), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "platforms.json"),
		[]byte(`{"schema_version":"1.0","platforms":{}}`), 0o644); err != nil {
		t.Fatalf("write platforms.json: %v", err)
	}
	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	return n
}

// TestScopedWrite_FloorInvariant_OverrideRequiresBase pins the network-floor
// invariant: a zone override may be created only if a network-scope definition
// already exists. The bare zone create is rejected with a *spec.ReferenceError
// (→ 400); once the network base exists, the same zone override succeeds.
func TestScopedWrite_FloorInvariant_OverrideRequiresBase(t *testing.T) {
	n := loadScopedTestNetwork(t)

	// No network base yet → zone override rejected.
	err := n.CreateService(spec.ScopeZone, "amer", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed"})
	var refErr *spec.ReferenceError
	if !errors.As(err, &refErr) {
		t.Fatalf("zone override without a network base: got %v, want *spec.ReferenceError (400)", err)
	}

	// Create the network base, then the zone override resolves.
	if err := n.CreateService(spec.ScopeNetwork, "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed"}); err != nil {
		t.Fatalf("create network base: %v", err)
	}
	if err := n.CreateService(spec.ScopeZone, "amer", "TRANSIT", &spec.ServiceSpec{ServiceType: "bridged"}); err != nil {
		t.Fatalf("zone override with a network base present: %v", err)
	}

	// The override landed in the zone container, not the network one.
	z := n.spec.Zones["amer"]
	if got := z.Services["TRANSIT"]; got == nil || got.ServiceType != "bridged" {
		t.Errorf("zone override = %+v, want service_type=bridged", got)
	}
	if got := n.spec.Services["TRANSIT"]; got == nil || got.ServiceType != "routed" {
		t.Errorf("network base = %+v, want service_type=routed (unchanged)", got)
	}
}

// TestScopedWrite_PerZoneIPVPNOverride pins the motivating use case: one
// network IP-VPN base, overridden per zone with a different L3VNI so each zone
// is its own overlay instance. The floor (network base) makes every node
// resolve at least the base; the zone override refines it for that zone.
func TestScopedWrite_PerZoneIPVPNOverride(t *testing.T) {
	n := loadScopedTestNetwork(t)

	// Network base: the floor every node resolves by default.
	if err := n.CreateIPVPN(spec.ScopeNetwork, "", "VRF_BLUE", &spec.IPVPNSpec{L3VNI: 1000}); err != nil {
		t.Fatalf("create network IP-VPN base: %v", err)
	}
	// Zone override: amer gets its own overlay instance (distinct L3VNI).
	if err := n.CreateIPVPN(spec.ScopeZone, "amer", "VRF_BLUE", &spec.IPVPNSpec{L3VNI: 2000}); err != nil {
		t.Fatalf("create zone IP-VPN override: %v", err)
	}

	if got := n.spec.IPVPNs["VRF_BLUE"]; got == nil || got.L3VNI != 1000 {
		t.Errorf("network base L3VNI = %v, want 1000", got)
	}
	if got := n.spec.Zones["amer"].IPVPNs["VRF_BLUE"]; got == nil || got.L3VNI != 2000 {
		t.Errorf("zone override L3VNI = %v, want 2000", got)
	}

	// The inventory shows both definitions (locating, not resolving).
	inv, err := n.ListScopedSpecs()
	if err != nil {
		t.Fatalf("ListScopedSpecs: %v", err)
	}
	var network, zone bool
	for _, s := range inv {
		if s.Kind == "IPVPNSpec" && s.Name == "VRF_BLUE" {
			switch s.Scope {
			case spec.ScopeNetwork:
				network = true
			case spec.ScopeZone:
				zone = true
			}
		}
	}
	if !network || !zone {
		t.Errorf("inventory missing a Vrf_BLUE definition: network=%v zone=%v", network, zone)
	}
}

// TestScopedWrite_UnknownZoneRejected pins that an override targeting a
// nonexistent zone is a not-found error, not a silent network write.
func TestScopedWrite_UnknownZoneRejected(t *testing.T) {
	n := loadScopedTestNetwork(t)
	if err := n.CreateService(spec.ScopeNetwork, "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed"}); err != nil {
		t.Fatalf("create network base: %v", err)
	}
	err := n.CreateService(spec.ScopeZone, "nosuchzone", "TRANSIT", &spec.ServiceSpec{ServiceType: "bridged"})
	var nf *newtronErrors
	if !errors.As(err, &nf) || !nf.IsNotFound() {
		t.Fatalf("override into unknown zone: got %v, want not-found", err)
	}
}

// TestScopedDelete_OverrideIsFree pins that deleting a zone override is always
// allowed — its consumers fall back to the network base the floor guarantees.
func TestScopedDelete_OverrideIsFree(t *testing.T) {
	n := loadScopedTestNetwork(t)
	mustCreate := func(scope, instance, name, typ string) {
		t.Helper()
		if err := n.CreateService(scope, instance, name, &spec.ServiceSpec{ServiceType: typ}); err != nil {
			t.Fatalf("create %s/%s %s: %v", scope, instance, name, err)
		}
	}
	mustCreate(spec.ScopeNetwork, "", "TRANSIT", "routed")
	mustCreate(spec.ScopeZone, "amer", "TRANSIT", "bridged")

	if err := n.DeleteService(spec.ScopeZone, "amer", "TRANSIT"); err != nil {
		t.Fatalf("delete zone override: %v", err)
	}
	if _, ok := n.spec.Zones["amer"].Services["TRANSIT"]; ok {
		t.Error("zone override still present after delete")
	}
	if _, ok := n.spec.Services["TRANSIT"]; !ok {
		t.Error("network base wrongly removed by an override delete")
	}
}

// TestNetworkDelete_BlockedByOverrideBelow pins that deleting the network base
// is refused while a zone override still sits below it (removing the base would
// strand the override) — a *util.ConflictError (→ 409). Deleting the override
// first then unblocks the base delete (bottom-up, §15).
func TestNetworkDelete_BlockedByOverrideBelow(t *testing.T) {
	n := loadScopedTestNetwork(t)
	if err := n.CreateService(spec.ScopeNetwork, "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed"}); err != nil {
		t.Fatalf("create network base: %v", err)
	}
	if err := n.CreateService(spec.ScopeZone, "amer", "TRANSIT", &spec.ServiceSpec{ServiceType: "bridged"}); err != nil {
		t.Fatalf("create zone override: %v", err)
	}

	err := n.DeleteService(spec.ScopeNetwork, "", "TRANSIT")
	var conflict *util.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("delete network base with an override below: got %v, want *util.ConflictError (409)", err)
	}

	// Remove the override, then the base delete succeeds.
	if err := n.DeleteService(spec.ScopeZone, "amer", "TRANSIT"); err != nil {
		t.Fatalf("delete zone override: %v", err)
	}
	if err := n.DeleteService(spec.ScopeNetwork, "", "TRANSIT"); err != nil {
		t.Fatalf("delete network base after removing the override: %v", err)
	}
}

// TestNetworkDelete_BlockedByScopedConsumer pins the cross-scope reverse check:
// a network base referenced by a ZONE-scoped consumer cannot be deleted, even
// though no network-scope consumer exists. (A network IP-VPN referenced by a
// zone-scoped service override.)
func TestNetworkDelete_BlockedByScopedConsumer(t *testing.T) {
	n := loadScopedTestNetwork(t)
	// Network bases: an IP-VPN and a service that the zone will override.
	if err := n.CreateIPVPN("", "", "VRF_BLUE", &spec.IPVPNSpec{L3VNI: 1001}); err != nil {
		t.Fatalf("create network ipvpn: %v", err)
	}
	if err := n.CreateService(spec.ScopeNetwork, "", "OVERLAY", &spec.ServiceSpec{ServiceType: "evpn-irb", IPVPN: "VRF_BLUE"}); err != nil {
		t.Fatalf("create network service base: %v", err)
	}
	// Zone override of the service still references the network IP-VPN.
	if err := n.CreateService(spec.ScopeZone, "amer", "OVERLAY", &spec.ServiceSpec{ServiceType: "evpn-irb", IPVPN: "VRF_BLUE"}); err != nil {
		t.Fatalf("create zone service override: %v", err)
	}

	// Deleting the network IP-VPN must see the zone-scoped consumer.
	err := n.DeleteIPVPN("", "", "VRF_BLUE")
	var conflict *util.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("delete network ipvpn referenced by a zone override: got %v, want *util.ConflictError (409)", err)
	}
}
