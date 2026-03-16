package node

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

func TestNodeIntentAccessors(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Initially empty
	if got := n.GetIntent("Ethernet0"); got != nil {
		t.Fatalf("expected nil intent, got %v", got)
	}
	if got := n.Intents(); got != nil {
		t.Fatalf("expected nil intents, got %v", got)
	}

	// Set an intent
	intent := &sonic.Intent{
		Resource:  "Ethernet0",
		Operation: "apply-service",
		Name:      "transit",
		State:     sonic.IntentActuated,
		Params:    map[string]string{"service_name": "transit"},
	}
	n.SetIntent(intent)

	// Get it back
	got := n.GetIntent("Ethernet0")
	if got == nil {
		t.Fatal("expected intent, got nil")
	}
	if got.Name != "transit" {
		t.Errorf("Name = %q, want transit", got.Name)
	}

	// ServiceIntents should include it
	svcIntents := n.ServiceIntents()
	if len(svcIntents) != 1 {
		t.Fatalf("ServiceIntents() = %d, want 1", len(svcIntents))
	}

	// Add a non-service intent
	bgpIntent := &sonic.Intent{
		Resource:  "bgp",
		Operation: "configure-bgp",
		State:     sonic.IntentActuated,
	}
	n.SetIntent(bgpIntent)

	// ServiceIntents should still be 1
	svcIntents = n.ServiceIntents()
	if len(svcIntents) != 1 {
		t.Fatalf("ServiceIntents() = %d after adding bgp, want 1", len(svcIntents))
	}

	// All intents should be 2
	all := n.Intents()
	if len(all) != 2 {
		t.Fatalf("Intents() = %d, want 2", len(all))
	}

	// Remove
	n.RemoveIntent("Ethernet0")
	if got := n.GetIntent("Ethernet0"); got != nil {
		t.Fatalf("expected nil after remove, got %v", got)
	}
	if len(n.Intents()) != 1 {
		t.Fatalf("Intents() = %d after remove, want 1", len(n.Intents()))
	}
}

func TestNodeLoadIntents(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Simulate CONFIG_DB with NEWTRON_INTENT entries
	n.configDB.NewtronIntent = map[string]map[string]string{
		"Ethernet0": {
			"state":        "actuated",
			"operation":    "apply-service",
			"name":         "transit",
			"service_name": "transit",
			"service_type": "routed",
			"ip_address":   "10.1.1.1/30",
			"vrf_name":     "Vrf_TRANSIT",
		},
		"Ethernet4": {
			"state":        "actuated",
			"operation":    "apply-service",
			"name":         "customer",
			"service_name": "customer",
			"service_type": "evpn-bridged",
			"vlan_id":      "100",
		},
	}

	n.LoadIntents()

	intents := n.Intents()
	if len(intents) != 2 {
		t.Fatalf("Intents() = %d, want 2", len(intents))
	}

	eth0 := n.GetIntent("Ethernet0")
	if eth0 == nil {
		t.Fatal("expected Ethernet0 intent")
	}
	if eth0.Name != "transit" {
		t.Errorf("Ethernet0 Name = %q, want transit", eth0.Name)
	}
	if eth0.State != sonic.IntentActuated {
		t.Errorf("Ethernet0 State = %q, want actuated", eth0.State)
	}
	if eth0.Params["vrf_name"] != "Vrf_TRANSIT" {
		t.Errorf("Ethernet0 vrf_name = %q, want Vrf_TRANSIT", eth0.Params["vrf_name"])
	}

	eth4 := n.GetIntent("Ethernet4")
	if eth4 == nil {
		t.Fatal("expected Ethernet4 intent")
	}
	if eth4.Name != "customer" {
		t.Errorf("Ethernet4 Name = %q, want customer", eth4.Name)
	}
	if eth4.Params["vlan_id"] != "100" {
		t.Errorf("Ethernet4 vlan_id = %q, want 100", eth4.Params["vlan_id"])
	}
}

func TestSnapshot(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Simulate loaded intents (as if read from CONFIG_DB after connect)
	n.SetIntent(&sonic.Intent{
		Resource:  "Ethernet0",
		Operation: "apply-service",
		Name:      "transit",
		State:     sonic.IntentActuated,
		Params: map[string]string{
			"service_name": "transit",
			"ip_address":   "10.1.1.1/30",
			"vrf_name":     "Vrf_TRANSIT",
			"l3vni":        "1001",
		},
	})
	n.SetIntent(&sonic.Intent{
		Resource:  "Ethernet4",
		Operation: "apply-service",
		Name:      "customer",
		State:     sonic.IntentActuated,
		Params: map[string]string{
			"service_name": "customer",
			"vlan_id":      "100",
		},
	})
	// Non-service intent — should NOT appear in topology snapshot
	n.SetIntent(&sonic.Intent{
		Resource:  "bgp",
		Operation: "configure-bgp",
		State:     sonic.IntentActuated,
	})
	// Unrealized intent — should NOT appear
	n.SetIntent(&sonic.Intent{
		Resource:  "Ethernet8",
		Operation: "apply-service",
		Name:      "pending",
		State:     sonic.IntentUnrealized,
	})

	snap := n.Snapshot()
	if len(snap.Interfaces) != 2 {
		t.Fatalf("Snapshot interfaces = %d, want 2", len(snap.Interfaces))
	}

	eth0 := snap.Interfaces["Ethernet0"]
	if eth0 == nil {
		t.Fatal("expected Ethernet0 in snapshot")
	}
	if eth0.Service != "transit" {
		t.Errorf("Ethernet0 Service = %q, want transit", eth0.Service)
	}
	if eth0.IP != "10.1.1.1/30" {
		t.Errorf("Ethernet0 IP = %q, want 10.1.1.1/30", eth0.IP)
	}

	eth4 := snap.Interfaces["Ethernet4"]
	if eth4 == nil {
		t.Fatal("expected Ethernet4 in snapshot")
	}
	if eth4.Service != "customer" {
		t.Errorf("Ethernet4 Service = %q, want customer", eth4.Service)
	}

	// bgp and Ethernet8 should NOT be in snapshot
	if _, ok := snap.Interfaces["bgp"]; ok {
		t.Error("bgp should not appear in topology snapshot")
	}
	if _, ok := snap.Interfaces["Ethernet8"]; ok {
		t.Error("unrealized Ethernet8 should not appear in topology snapshot")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	// Simulate: topology.json said Ethernet0=transit(10.1.1.1/30), Ethernet4=customer
	// After provisioning + connect, intents loaded from CONFIG_DB
	// Snapshot should recover the original topology interface entries

	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Original topology input
	originalInterfaces := map[string]spec.TopologyInterface{
		"Ethernet0": {Service: "transit", IP: "10.1.1.1/30"},
		"Ethernet4": {Service: "customer"},
	}

	// Simulate what CONFIG_DB would have after provisioning
	n.configDB.NewtronIntent = map[string]map[string]string{
		"Ethernet0": {
			"state": "actuated", "operation": "apply-service", "name": "transit",
			"service_name": "transit", "ip_address": "10.1.1.1/30",
			"vrf_name": "Vrf_TRANSIT", "l3vni": "1001",
		},
		"Ethernet4": {
			"state": "actuated", "operation": "apply-service", "name": "customer",
			"service_name": "customer", "vlan_id": "100",
		},
	}
	n.LoadIntents()

	// Snapshot should match original topology (service + IP)
	snap := n.Snapshot()
	for intfName, orig := range originalInterfaces {
		got := snap.Interfaces[intfName]
		if got == nil {
			t.Fatalf("snapshot missing %s", intfName)
		}
		if got.Service != orig.Service {
			t.Errorf("%s Service = %q, want %q", intfName, got.Service, orig.Service)
		}
		if got.IP != orig.IP {
			t.Errorf("%s IP = %q, want %q", intfName, got.IP, orig.IP)
		}
	}
}

func TestNodeServiceIntentsFiltersState(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Unrealized service intent should NOT appear in ServiceIntents
	n.SetIntent(&sonic.Intent{
		Resource:  "Ethernet0",
		Operation: "apply-service",
		Name:      "transit",
		State:     sonic.IntentUnrealized,
	})

	// In-flight service intent should NOT appear in ServiceIntents
	n.SetIntent(&sonic.Intent{
		Resource:  "Ethernet4",
		Operation: "apply-service",
		Name:      "customer",
		State:     sonic.IntentInFlight,
	})

	svc := n.ServiceIntents()
	if len(svc) != 0 {
		t.Fatalf("ServiceIntents() = %d for non-actuated, want 0", len(svc))
	}
}
