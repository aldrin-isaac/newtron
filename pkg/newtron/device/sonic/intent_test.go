package sonic

import (
	"testing"
	"time"
)

func TestNewIntent(t *testing.T) {
	fields := map[string]string{
		"state":        "actuated",
		"operation":    "apply-service",
		"name":         "transit",
		"service_name": "transit",
		"service_type": "routed",
		"ip_address":   "10.1.1.1/30",
		"vrf_name":     "Vrf_TRANSIT",
		"vrf_type":     "shared",
		"ipvpn":        "IPVPN_TRANSIT",
		"l3vni":        "1001",
		"l3vni_vlan":   "3001",
		"bgp_neighbor": "10.1.1.2",
		"bgp_peer_as":  "65002",
		"peer_group":   "TRANSIT",
		"applied_at":   "2026-03-15T10:00:00Z",
		"applied_by":   "aldrin@host",
	}

	intent := NewIntent("Ethernet0", fields)

	if intent.Resource != "Ethernet0" {
		t.Errorf("Resource = %q, want Ethernet0", intent.Resource)
	}
	if intent.Operation != "apply-service" {
		t.Errorf("Operation = %q, want apply-service", intent.Operation)
	}
	if intent.Name != "transit" {
		t.Errorf("Name = %q, want transit", intent.Name)
	}
	if intent.State != IntentActuated {
		t.Errorf("State = %q, want actuated", intent.State)
	}
	if intent.AppliedBy != "aldrin@host" {
		t.Errorf("AppliedBy = %q, want aldrin@host", intent.AppliedBy)
	}
	if intent.AppliedAt == nil {
		t.Fatal("AppliedAt is nil, want non-nil")
	}
	if intent.AppliedAt.Year() != 2026 {
		t.Errorf("AppliedAt year = %d, want 2026", intent.AppliedAt.Year())
	}

	// Verify params exclude identity fields
	if _, ok := intent.Params["state"]; ok {
		t.Error("Params should not contain 'state' (identity field)")
	}
	if _, ok := intent.Params["operation"]; ok {
		t.Error("Params should not contain 'operation' (identity field)")
	}

	// Verify params include resolved values
	checks := map[string]string{
		"service_name": "transit",
		"service_type": "routed",
		"ip_address":   "10.1.1.1/30",
		"vrf_name":     "Vrf_TRANSIT",
		"vrf_type":     "shared",
		"ipvpn":        "IPVPN_TRANSIT",
		"l3vni":        "1001",
		"l3vni_vlan":   "3001",
		"bgp_neighbor": "10.1.1.2",
		"bgp_peer_as":  "65002",
		"peer_group":   "TRANSIT",
	}
	for k, want := range checks {
		if got := intent.Params[k]; got != want {
			t.Errorf("Params[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestIntentToFields(t *testing.T) {
	now := time.Now().UTC()
	intent := &Intent{
		Resource:  "Ethernet4",
		Operation: "apply-service",
		Name:      "customer",
		State:     IntentActuated,
		AppliedAt: &now,
		AppliedBy: "test@host",
		Params: map[string]string{
			"service_name": "customer",
			"service_type": "evpn-bridged",
			"vlan_id":      "100",
			"l2vni":        "10100",
		},
	}

	fields := intent.ToFields()

	if fields["service_name"] != "customer" {
		t.Errorf("service_name = %q, want customer", fields["service_name"])
	}
	if fields["service_type"] != "evpn-bridged" {
		t.Errorf("service_type = %q, want evpn-bridged", fields["service_type"])
	}
	if fields["state"] != "actuated" {
		t.Errorf("state = %q, want actuated", fields["state"])
	}
	if fields["operation"] != "apply-service" {
		t.Errorf("operation = %q, want apply-service", fields["operation"])
	}
	if fields["applied_by"] != "test@host" {
		t.Errorf("applied_by = %q, want test@host", fields["applied_by"])
	}
	if fields["applied_at"] == "" {
		t.Error("applied_at is empty, want non-empty")
	}
}

func TestIntentRoundTrip(t *testing.T) {
	// Create fields, convert to intent, convert back to fields
	original := map[string]string{
		"state":           "actuated",
		"operation":       "apply-service",
		"name":            "transit",
		"service_name":    "transit",
		"service_type":    "routed",
		"ip_address":      "10.1.1.1/30",
		"vrf_name":        "Vrf_TRANSIT",
		"vrf_type":        "shared",
		"macvpn":          "MACVPN_OFFICE",
		"l2vni":           "10100",
		"anycast_ip":      "10.100.0.1",
		"anycast_mac":     "00:11:22:33:44:55",
		"arp_suppression": "true",
		"route_map_in":    "ALLOW_CUST_IMPORT_A1B2C3D4",
		"route_map_out":   "ALLOW_CUST_EXPORT_E5F6A7B8",
	}

	intent := NewIntent("Ethernet0", original)
	fields := intent.ToFields()

	// Params should round-trip
	paramChecks := map[string]string{
		"service_name":    "transit",
		"service_type":    "routed",
		"ip_address":      "10.1.1.1/30",
		"vrf_name":        "Vrf_TRANSIT",
		"vrf_type":        "shared",
		"macvpn":          "MACVPN_OFFICE",
		"l2vni":           "10100",
		"anycast_ip":      "10.100.0.1",
		"anycast_mac":     "00:11:22:33:44:55",
		"arp_suppression": "true",
		"route_map_in":    "ALLOW_CUST_IMPORT_A1B2C3D4",
		"route_map_out":   "ALLOW_CUST_EXPORT_E5F6A7B8",
	}
	for k, want := range paramChecks {
		if got := fields[k]; got != want {
			t.Errorf("round-trip [%q] = %q, want %q", k, got, want)
		}
	}

	// Identity fields should round-trip
	if fields["state"] != "actuated" {
		t.Errorf("state = %q, want actuated", fields["state"])
	}
	if fields["operation"] != "apply-service" {
		t.Errorf("operation = %q, want apply-service", fields["operation"])
	}
	if fields["name"] != "transit" {
		t.Errorf("name = %q, want transit", fields["name"])
	}
}

func TestNewIntentDefaultState(t *testing.T) {
	// Fields without explicit state default to actuated
	fields := map[string]string{
		"operation":    "apply-service",
		"service_name": "transit",
	}
	intent := NewIntent("Ethernet0", fields)
	if intent.State != IntentActuated {
		t.Errorf("State = %q, want actuated (default)", intent.State)
	}
}

func TestIntentStateHelpers(t *testing.T) {
	svc := &Intent{Operation: "apply-service", State: IntentActuated}
	bgp := &Intent{Operation: "configure-bgp", State: IntentInFlight}
	unr := &Intent{Operation: "setup-evpn", State: IntentUnrealized}

	if !svc.IsService() {
		t.Error("apply-service should be service")
	}
	if bgp.IsService() {
		t.Error("configure-bgp should not be service")
	}
	if !svc.IsActuated() {
		t.Error("actuated intent should report IsActuated")
	}
	if !bgp.IsInFlight() {
		t.Error("in-flight intent should report IsInFlight")
	}
	if unr.IsActuated() || unr.IsInFlight() {
		t.Error("unrealized intent should not be actuated or in-flight")
	}
}
