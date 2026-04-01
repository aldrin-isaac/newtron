package node

import (
	"strings"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

func TestNodeIntentAccessors(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Initially empty
	if got := n.GetIntent("interface|Ethernet0"); got != nil {
		t.Fatalf("expected nil intent, got %v", got)
	}
	if got := n.Intents(); got != nil {
		t.Fatalf("expected nil intents, got %v", got)
	}

	// Write an intent directly to configDB (the single source)
	n.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"state":        "actuated",
		"operation":    "apply-service",
		"service_name": "transit",
	}

	// Get it back
	got := n.GetIntent("interface|Ethernet0")
	if got == nil {
		t.Fatal("expected intent, got nil")
	}
	if got.Operation != "apply-service" {
		t.Errorf("Operation = %q, want apply-service", got.Operation)
	}
	if got.Params["service_name"] != "transit" {
		t.Errorf("service_name = %q, want transit", got.Params["service_name"])
	}

	// ServiceIntents should include it
	svcIntents := n.ServiceIntents()
	if len(svcIntents) != 1 {
		t.Fatalf("ServiceIntents() = %d, want 1", len(svcIntents))
	}

	// Add a non-service intent (device root — not a service)
	n.configDB.NewtronIntent["device"] = map[string]string{
		"state":     "actuated",
		"operation": "setup-device",
	}

	// ServiceIntents should still be 1
	svcIntents = n.ServiceIntents()
	if len(svcIntents) != 1 {
		t.Fatalf("ServiceIntents() = %d after adding device, want 1", len(svcIntents))
	}

	// All intents should be 2
	all := n.Intents()
	if len(all) != 2 {
		t.Fatalf("Intents() = %d, want 2", len(all))
	}

	// Remove directly from configDB
	delete(n.configDB.NewtronIntent, "interface|Ethernet0")
	if got := n.GetIntent("interface|Ethernet0"); got != nil {
		t.Fatalf("expected nil after remove, got %v", got)
	}
	if len(n.Intents()) != 1 {
		t.Fatalf("Intents() = %d after remove, want 1", len(n.Intents()))
	}
}

func TestNodeLoadIntentsFromConfigDB(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Simulate CONFIG_DB with NEWTRON_INTENT entries
	n.configDB.NewtronIntent = map[string]map[string]string{
		"interface|Ethernet0": {
			"state":        "actuated",
			"operation":    "apply-service",
			"service_name": "transit",
			"service_type": "routed",
			"ip_address":   "10.1.1.1/30",
			"vrf_name":     "Vrf_TRANSIT",
		},
		"interface|Ethernet4": {
			"state":        "actuated",
			"operation":    "apply-service",
			"service_name": "customer",
			"service_type": "evpn-bridged",
			"vlan_id":      "100",
		},
	}

	// Intents are readable directly — no LoadIntents() call needed
	intents := n.Intents()
	if len(intents) != 2 {
		t.Fatalf("Intents() = %d, want 2", len(intents))
	}

	eth0 := n.GetIntent("interface|Ethernet0")
	if eth0 == nil {
		t.Fatal("expected interface|Ethernet0 intent")
	}
	if eth0.Params["service_name"] != "transit" {
		t.Errorf("service_name = %q, want transit", eth0.Params["service_name"])
	}
	if eth0.State != sonic.IntentActuated {
		t.Errorf("State = %q, want actuated", eth0.State)
	}
	if eth0.Params["vrf_name"] != "Vrf_TRANSIT" {
		t.Errorf("vrf_name = %q, want Vrf_TRANSIT", eth0.Params["vrf_name"])
	}

	eth4 := n.GetIntent("interface|Ethernet4")
	if eth4 == nil {
		t.Fatal("expected interface|Ethernet4 intent")
	}
	if eth4.Params["service_name"] != "customer" {
		t.Errorf("service_name = %q, want customer", eth4.Params["service_name"])
	}
	if eth4.Params["vlan_id"] != "100" {
		t.Errorf("vlan_id = %q, want 100", eth4.Params["vlan_id"])
	}
}

func TestSnapshot(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Simulate loaded intents via configDB (the single source)
	n.configDB.NewtronIntent = map[string]map[string]string{
		"interface|Ethernet0": {
			"state":        "actuated",
			"operation":    "apply-service",
			"service_name": "transit",
			"ip_address":   "10.1.1.1/30",
			"vrf_name":     "Vrf_TRANSIT",
			"l3vni":        "1001",
		},
		"interface|Ethernet4": {
			"state":        "actuated",
			"operation":    "apply-service",
			"service_name": "customer",
			"vlan_id":      "100",
		},
		// Non-service intent — now included (all actuated intents appear)
		"bgp": {
			"state":     "actuated",
			"operation": "configure-bgp",
		},
		// Non-actuated intent — should NOT appear
		"interface|Ethernet8": {
			"state":     "pending",
			"operation": "apply-service",
		},
	}

	snap := n.Tree()
	// All 3 actuated intents become steps; non-actuated Ethernet8 is filtered out.
	if len(snap.Steps) != 3 {
		t.Fatalf("Snapshot steps = %d, want 3", len(snap.Steps))
	}

	// Build a map from URL for lookup
	stepsByURL := map[string]spec.TopologyStep{}
	for _, step := range snap.Steps {
		stepsByURL[step.URL] = step
	}

	eth0, ok := stepsByURL["/interface/Ethernet0/apply-service"]
	if !ok {
		t.Fatal("expected Ethernet0 apply-service step in snapshot")
	}
	if eth0.Params["service"] != "transit" {
		t.Errorf("Ethernet0 service = %q, want transit", eth0.Params["service"])
	}
	if eth0.Params["ip_address"] != "10.1.1.1/30" {
		t.Errorf("Ethernet0 ip_address = %q, want 10.1.1.1/30", eth0.Params["ip_address"])
	}

	eth4, ok := stepsByURL["/interface/Ethernet4/apply-service"]
	if !ok {
		t.Fatal("expected Ethernet4 apply-service step in snapshot")
	}
	if eth4.Params["service"] != "customer" {
		t.Errorf("Ethernet4 service = %q, want customer", eth4.Params["service"])
	}

	// configure-bgp is now a node-level step
	if _, ok := stepsByURL["/configure-bgp"]; !ok {
		t.Error("expected configure-bgp step in snapshot")
	}

	// Non-actuated Ethernet8 should NOT appear
	if _, ok := stepsByURL["/interface/Ethernet8/apply-service"]; ok {
		t.Error("non-actuated Ethernet8 should not appear in snapshot")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	type expected struct {
		service string
		ip      string
	}
	originalExpected := map[string]expected{
		"Ethernet0": {service: "transit", ip: "10.1.1.1/30"},
		"Ethernet4": {service: "customer"},
	}

	// Simulate CONFIG_DB with NEWTRON_INTENT entries
	n.configDB.NewtronIntent = map[string]map[string]string{
		"interface|Ethernet0": {
			"state": "actuated", "operation": "apply-service",
			"service_name": "transit", "ip_address": "10.1.1.1/30",
			"vrf_name": "Vrf_TRANSIT", "l3vni": "1001",
		},
		"interface|Ethernet4": {
			"state": "actuated", "operation": "apply-service",
			"service_name": "customer", "vlan_id": "100",
		},
	}

	// Snapshot should match original topology (service + IP as step params)
	snap := n.Tree()
	stepsByIntf := map[string]spec.TopologyStep{}
	for _, step := range snap.Steps {
		_, ifaceName := parseStepURL(step.URL)
		stepsByIntf[ifaceName] = step
	}
	for intfName, orig := range originalExpected {
		got, ok := stepsByIntf[intfName]
		if !ok {
			t.Fatalf("snapshot missing %s", intfName)
		}
		if got.Params["service"] != orig.service {
			t.Errorf("%s service = %q, want %q", intfName, got.Params["service"], orig.service)
		}
		ipGot, _ := got.Params["ip_address"].(string)
		if ipGot != orig.ip {
			t.Errorf("%s ip_address = %q, want %q", intfName, ipGot, orig.ip)
		}
	}
}

func TestWriteIntentRecordsToProjection(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	cs := NewChangeSet("test", "test")
	n.writeIntent(cs, sonic.OpCreateVRF, "vrf|Vrf_TRANSIT", map[string]string{
		"name": "Vrf_TRANSIT",
	}, nil)

	// Intent should be in projection
	fields, ok := n.configDB.NewtronIntent["vrf|Vrf_TRANSIT"]
	if !ok {
		t.Fatal("intent not written to projection")
	}
	if fields["operation"] != sonic.OpCreateVRF {
		t.Errorf("operation = %q, want %q", fields["operation"], sonic.OpCreateVRF)
	}
	if fields["state"] != string(sonic.IntentActuated) {
		t.Errorf("state = %q, want %q", fields["state"], string(sonic.IntentActuated))
	}
	if fields["name"] != "Vrf_TRANSIT" {
		t.Errorf("name param = %q, want Vrf_TRANSIT", fields["name"])
	}

	// ChangeSet should have the intent prepended
	if len(cs.Changes) != 1 {
		t.Fatalf("ChangeSet has %d changes, want 1", len(cs.Changes))
	}
	if cs.Changes[0].Table != "NEWTRON_INTENT" {
		t.Errorf("ChangeSet[0].Table = %q, want NEWTRON_INTENT", cs.Changes[0].Table)
	}
	if cs.Changes[0].Key != "vrf|Vrf_TRANSIT" {
		t.Errorf("ChangeSet[0].Key = %q, want vrf|Vrf_TRANSIT", cs.Changes[0].Key)
	}
}

func TestWriteIntentPrepends(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Add a config entry first, then writeIntent — intent should be first.
	cs := NewChangeSet("test", "test")
	cs.Add("VLAN", "Vlan100", map[string]string{"vlanid": "100"})
	n.writeIntent(cs, sonic.OpCreateVLAN, "vlan|100", map[string]string{
		"vlan_id": "100",
	}, nil)

	if len(cs.Changes) != 2 {
		t.Fatalf("ChangeSet has %d changes, want 2", len(cs.Changes))
	}
	// Intent should be prepended (first)
	if cs.Changes[0].Table != "NEWTRON_INTENT" {
		t.Errorf("ChangeSet[0].Table = %q, want NEWTRON_INTENT (prepend)", cs.Changes[0].Table)
	}
	if cs.Changes[1].Table != "VLAN" {
		t.Errorf("ChangeSet[1].Table = %q, want VLAN", cs.Changes[1].Table)
	}
}

func TestDeleteIntentRemovesFromProjection(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Pre-populate an intent in projection
	n.configDB.NewtronIntent["vrf|Vrf_TRANSIT"] = map[string]string{
		"state":     "actuated",
		"operation": "create-vrf",
		"name":      "Vrf_TRANSIT",
	}

	cs := NewChangeSet("test", "test")
	n.deleteIntent(cs, "vrf|Vrf_TRANSIT")

	// Should be removed from projection
	if _, ok := n.configDB.NewtronIntent["vrf|Vrf_TRANSIT"]; ok {
		t.Error("intent not removed from projection")
	}

	// ChangeSet should have a delete
	if len(cs.Changes) != 1 {
		t.Fatalf("ChangeSet has %d changes, want 1", len(cs.Changes))
	}
	if cs.Changes[0].Type != ChangeDelete {
		t.Errorf("change type = %v, want ChangeDelete", cs.Changes[0].Type)
	}
}

func TestIntentToStep_NodeLevel(t *testing.T) {
	step := IntentToStep("Vrf_TRANSIT", map[string]string{
		"state":     "actuated",
		"operation": "create-vrf",
		"name":      "Vrf_TRANSIT",
	})

	if step.URL != "/create-vrf" {
		t.Errorf("URL = %q, want /create-vrf", step.URL)
	}
	if step.Params["name"] != "Vrf_TRANSIT" {
		t.Errorf("Params[name] = %v, want Vrf_TRANSIT", step.Params["name"])
	}
}

func TestIntentToStep_InterfaceLevel(t *testing.T) {
	step := IntentToStep("interface|Ethernet0", map[string]string{
		"state":                  "actuated",
		"operation":              "apply-service",
		"service_name":           "transit",
		"ip_address":             "10.1.1.1/30",
		"bgp_peer_as":            "65002",
		"vlan_id":                "100",
		"route_reflector_client": "true",
		"next_hop_self":          "true",
		// Teardown-only fields — must NOT appear in step
		"vrf_name":    "Vrf_TRANSIT",
		"l3vni":       "1001",
		"ingress_acl": "ACL_IN",
	})

	if step.URL != "/interface/Ethernet0/apply-service" {
		t.Errorf("URL = %q, want /interface/Ethernet0/apply-service", step.URL)
	}
	// apply-service maps service_name → service
	if step.Params["service"] != "transit" {
		t.Errorf("Params[service] = %v, want transit", step.Params["service"])
	}
	if step.Params["ip_address"] != "10.1.1.1/30" {
		t.Errorf("Params[ip_address] = %v, want 10.1.1.1/30", step.Params["ip_address"])
	}
	// bgp_peer_as → peer_as
	if step.Params["peer_as"] != "65002" {
		t.Errorf("Params[peer_as] = %v, want 65002", step.Params["peer_as"])
	}
	// User params that must round-trip
	if step.Params["vlan_id"] != "100" {
		t.Errorf("Params[vlan_id] = %v, want 100", step.Params["vlan_id"])
	}
	if step.Params["route_reflector_client"] != "true" {
		t.Errorf("Params[route_reflector_client] = %v, want true", step.Params["route_reflector_client"])
	}
	if step.Params["next_hop_self"] != "true" {
		t.Errorf("Params[next_hop_self] = %v, want true", step.Params["next_hop_self"])
	}
	// Internal/teardown params should NOT appear in step
	if _, ok := step.Params["service_name"]; ok {
		t.Error("service_name should not appear in step params (mapped to 'service')")
	}
	if _, ok := step.Params["vrf_name"]; ok {
		t.Error("vrf_name should not appear in step params (teardown-only)")
	}
	if _, ok := step.Params["l3vni"]; ok {
		t.Error("l3vni should not appear in step params (teardown-only)")
	}
	if _, ok := step.Params["ingress_acl"]; ok {
		t.Error("ingress_acl should not appear in step params (teardown-only)")
	}
}

func TestIntentToStep_SetupDevice(t *testing.T) {
	step := IntentToStep("device", map[string]string{
		"state":      "actuated",
		"operation":  "setup-device",
		"source_ip":  "10.0.0.1",
		"hostname":   "leaf1",
		"bgp_asn":    "65001",
	})

	if step.URL != "/setup-device" {
		t.Errorf("URL = %q, want /setup-device", step.URL)
	}
	// source_ip stays at top level
	if step.Params["source_ip"] != "10.0.0.1" {
		t.Errorf("Params[source_ip] = %v, want 10.0.0.1", step.Params["source_ip"])
	}
	// Other params nested under "fields"
	fields, ok := step.Params["fields"].(map[string]any)
	if !ok {
		t.Fatalf("Params[fields] missing or wrong type: %v", step.Params["fields"])
	}
	if fields["hostname"] != "leaf1" {
		t.Errorf("fields[hostname] = %v, want leaf1", fields["hostname"])
	}
	if fields["bgp_asn"] != "65001" {
		t.Errorf("fields[bgp_asn] = %v, want 65001", fields["bgp_asn"])
	}
}

func TestIntentToStep_SetupDeviceWithRR(t *testing.T) {
	step := IntentToStep("device", map[string]string{
		"state":         "actuated",
		"operation":     "setup-device",
		"source_ip":     "10.0.0.1",
		"hostname":      "spine1",
		"rr_cluster_id": "1.1.1.1",
		"rr_local_asn":  "65000",
		"rr_clients":    "10.0.0.2:65001,10.0.0.3:65002",
	})

	if step.URL != "/setup-device" {
		t.Errorf("URL = %q, want /setup-device", step.URL)
	}
	rr, ok := step.Params["route_reflector"].(map[string]any)
	if !ok {
		t.Fatalf("Params[route_reflector] missing or wrong type")
	}
	if rr["cluster_id"] != "1.1.1.1" {
		t.Errorf("rr.cluster_id = %v, want 1.1.1.1", rr["cluster_id"])
	}
	clients, ok := rr["clients"].([]any)
	if !ok || len(clients) != 2 {
		t.Fatalf("rr.clients = %v, want 2 entries", rr["clients"])
	}
	c0 := clients[0].(map[string]any)
	if c0["ip"] != "10.0.0.2" || c0["asn"] != "65001" {
		t.Errorf("client[0] = %v, want ip=10.0.0.2 asn=65001", c0)
	}
	// hostname should be under "fields"
	fields, ok := step.Params["fields"].(map[string]any)
	if !ok {
		t.Fatalf("Params[fields] missing")
	}
	if fields["hostname"] != "spine1" {
		t.Errorf("fields[hostname] = %v, want spine1", fields["hostname"])
	}
}

func TestIntentToStep_ConfigureInterface(t *testing.T) {
	step := IntentToStep("interface|Ethernet0", map[string]string{
		"state":     "actuated",
		"operation": "configure-interface",
		"vrf":       "Vrf_TRANSIT",
		"ip":        "10.1.1.1/30",
	})

	if step.URL != "/interface/Ethernet0/configure-interface" {
		t.Errorf("URL = %q, want /interface/Ethernet0/configure-interface", step.URL)
	}
	if step.Params["vrf"] != "Vrf_TRANSIT" {
		t.Errorf("Params[vrf] = %v, want Vrf_TRANSIT", step.Params["vrf"])
	}
	if step.Params["ip"] != "10.1.1.1/30" {
		t.Errorf("Params[ip] = %v, want 10.1.1.1/30", step.Params["ip"])
	}
}

func TestIntentToStep_CreatePortChannel(t *testing.T) {
	step := IntentToStep("portchannel|PortChannel1", map[string]string{
		"state":     "actuated",
		"operation": "create-portchannel",
		"name":      "PortChannel1",
		"members":   "Ethernet0,Ethernet4",
		"mtu":       "9100",
		"min_links": "2",
		"fallback":  "true",
		"fast_rate": "true",
	})

	if step.URL != "/create-portchannel" {
		t.Errorf("URL = %q, want /create-portchannel", step.URL)
	}
	if step.Params["name"] != "PortChannel1" {
		t.Errorf("Params[name] = %v, want PortChannel1", step.Params["name"])
	}
	// Members must be a slice, not a string
	members, ok := step.Params["members"].([]any)
	if !ok {
		t.Fatalf("Params[members] should be []any, got %T: %v", step.Params["members"], step.Params["members"])
	}
	if len(members) != 2 || members[0] != "Ethernet0" || members[1] != "Ethernet4" {
		t.Errorf("members = %v, want [Ethernet0 Ethernet4]", members)
	}
	// All PortChannel config fields must round-trip
	if step.Params["mtu"] != "9100" {
		t.Errorf("Params[mtu] = %v, want 9100", step.Params["mtu"])
	}
	if step.Params["min_links"] != "2" {
		t.Errorf("Params[min_links] = %v, want 2", step.Params["min_links"])
	}
	if step.Params["fallback"] != "true" {
		t.Errorf("Params[fallback] = %v, want true", step.Params["fallback"])
	}
	if step.Params["fast_rate"] != "true" {
		t.Errorf("Params[fast_rate] = %v, want true", step.Params["fast_rate"])
	}
}

func TestIntentToStep_SetProperty(t *testing.T) {
	// Resource key is "interface|Ethernet0|mtu" — kind-prefixed, multi-property support
	step := IntentToStep("interface|Ethernet0|mtu", map[string]string{
		"state":     "actuated",
		"operation": "set-property",
		"property":  "mtu",
		"value":     "9100",
	})

	if step.URL != "/interface/Ethernet0/set-property" {
		t.Errorf("URL = %q, want /interface/Ethernet0/set-property", step.URL)
	}
	if step.Params["property"] != "mtu" {
		t.Errorf("Params[property] = %v, want mtu", step.Params["property"])
	}
}

func TestIntentToStep_CreateVLAN(t *testing.T) {
	step := IntentToStep("vlan|100", map[string]string{
		"state":       "actuated",
		"operation":   "create-vlan",
		"vlan_id":     "100",
		"description": "customer-vlan",
		"vni":         "10100",
	})

	if step.URL != "/create-vlan" {
		t.Errorf("URL = %q, want /create-vlan", step.URL)
	}
	if step.Params["vlan_id"] != "100" {
		t.Errorf("Params[vlan_id] = %v, want 100", step.Params["vlan_id"])
	}
	if step.Params["description"] != "customer-vlan" {
		t.Errorf("Params[description] = %v, want customer-vlan", step.Params["description"])
	}
	if step.Params["vni"] != "10100" {
		t.Errorf("Params[vni] = %v, want 10100", step.Params["vni"])
	}
}

func TestIntentToStep_CreateACL(t *testing.T) {
	step := IntentToStep("acl|PROTECT_RE", map[string]string{
		"state":       "actuated",
		"operation":   "create-acl",
		"name":        "PROTECT_RE",
		"type":        "L3",
		"stage":       "ingress",
		"ports":       "Ethernet0,Ethernet4",
		"description": "protect routing engine",
	})

	if step.URL != "/create-acl" {
		t.Errorf("URL = %q, want /create-acl", step.URL)
	}
	if step.Params["name"] != "PROTECT_RE" {
		t.Errorf("Params[name] = %v, want PROTECT_RE", step.Params["name"])
	}
	if step.Params["type"] != "L3" {
		t.Errorf("Params[type] = %v, want L3", step.Params["type"])
	}
	if step.Params["stage"] != "ingress" {
		t.Errorf("Params[stage] = %v, want ingress", step.Params["stage"])
	}
	if step.Params["ports"] != "Ethernet0,Ethernet4" {
		t.Errorf("Params[ports] = %v, want Ethernet0,Ethernet4", step.Params["ports"])
	}
	if step.Params["description"] != "protect routing engine" {
		t.Errorf("Params[description] = %v, want 'protect routing engine'", step.Params["description"])
	}
}

func TestIntentToStep_AddBGPPeer(t *testing.T) {
	step := IntentToStep("interface|Ethernet0", map[string]string{
		"state":       "actuated",
		"operation":   "add-bgp-peer",
		"neighbor_ip": "10.1.1.2",
		"remote_as":   "65002",
		"description": "to-spine1",
		"multihop":    "255",
	})

	if step.URL != "/interface/Ethernet0/add-bgp-peer" {
		t.Errorf("URL = %q, want /interface/Ethernet0/add-bgp-peer", step.URL)
	}
	if step.Params["neighbor_ip"] != "10.1.1.2" {
		t.Errorf("Params[neighbor_ip] = %v, want 10.1.1.2", step.Params["neighbor_ip"])
	}
	if step.Params["remote_as"] != "65002" {
		t.Errorf("Params[remote_as] = %v, want 65002", step.Params["remote_as"])
	}
	if step.Params["description"] != "to-spine1" {
		t.Errorf("Params[description] = %v, want to-spine1", step.Params["description"])
	}
	if step.Params["multihop"] != "255" {
		t.Errorf("Params[multihop] = %v, want 255", step.Params["multihop"])
	}
}

func TestIntentToStep_BindACL(t *testing.T) {
	// Resource key for bind-acl is "interface|Ethernet0|acl|ingress"
	step := IntentToStep("interface|Ethernet0|acl|ingress", map[string]string{
		"state":     "actuated",
		"operation": "bind-acl",
		"acl_name":  "PROTECT_RE",
		"direction": "ingress",
	})

	if step.URL != "/interface/Ethernet0/bind-acl" {
		t.Errorf("URL = %q, want /interface/Ethernet0/bind-acl", step.URL)
	}
	if step.Params["acl_name"] != "PROTECT_RE" {
		t.Errorf("Params[acl_name] = %v, want PROTECT_RE", step.Params["acl_name"])
	}
}

func TestIntentsToSteps_Ordering(t *testing.T) {
	intents := map[string]map[string]string{
		"interface|Ethernet0": {
			"state": "actuated", "operation": "apply-service",
			"service_name": "transit",
			"_parents":     "vrf|Vrf_TRANSIT",
		},
		"vrf|Vrf_TRANSIT": {
			"state": "actuated", "operation": "create-vrf",
			"name":      "Vrf_TRANSIT",
			"_parents":  "device",
			"_children": "interface|Ethernet0",
		},
		"device": {
			"state": "actuated", "operation": "setup-device",
			"hostname":  "leaf1",
			"_children": "vrf|Vrf_TRANSIT",
		},
	}

	steps := IntentsToSteps(intents)
	if len(steps) != 3 {
		t.Fatalf("IntentsToSteps returned %d steps, want 3", len(steps))
	}

	// Order should be: setup-device (10), create-vrf (20), apply-service (90)
	ops := make([]string, len(steps))
	for i, s := range steps {
		op, _ := parseStepURL(s.URL)
		ops[i] = op
	}
	if ops[0] != "setup-device" {
		t.Errorf("step[0] = %q, want setup-device", ops[0])
	}
	if ops[1] != "create-vrf" {
		t.Errorf("step[1] = %q, want create-vrf", ops[1])
	}
	if ops[2] != "apply-service" {
		t.Errorf("step[2] = %q, want apply-service", ops[2])
	}
}

func TestIntentsToSteps_FiltersNonActuated(t *testing.T) {
	intents := map[string]map[string]string{
		"interface|Ethernet0": {
			"state": "actuated", "operation": "apply-service",
			"service_name": "transit",
		},
		"interface|Ethernet4": {
			"state": "pending", "operation": "apply-service",
		},
	}

	steps := IntentsToSteps(intents)
	if len(steps) != 1 {
		t.Fatalf("IntentsToSteps returned %d steps, want 1 (non-actuated filtered)", len(steps))
	}
}

func TestNodeServiceIntentsFiltersState(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Write non-actuated intents directly to configDB
	n.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"state":     "pending",
		"operation": "apply-service",
	}
	n.configDB.NewtronIntent["interface|Ethernet4"] = map[string]string{
		"state":     "failed",
		"operation": "apply-service",
	}

	svc := n.ServiceIntents()
	if len(svc) != 0 {
		t.Fatalf("ServiceIntents() = %d for non-actuated, want 0", len(svc))
	}
}

// ============================================================================
// ValidateIntentDAG Tests
// ============================================================================

func TestValidateIntentDAG_Healthy(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
			"_children": "vlan|100,vrf|CUST",
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			"_parents": "device", "_children": "interface|Ethernet0",
		},
		"vrf|CUST": {
			"operation": "create-vrf", "state": "actuated",
			"_parents": "device",
		},
		"interface|Ethernet0": {
			"operation": "apply-service", "state": "actuated",
			"_parents": "vlan|100",
		},
	}
	violations := ValidateIntentDAG(db)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}

func TestValidateIntentDAG_BrokenBidirectional(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
			"_children": "vlan|100", // lists vlan|100 as child
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			// MISSING _parents: "device" → bidirectional violation
		},
	}
	violations := ValidateIntentDAG(db)
	found := false
	for _, v := range violations {
		if v.Kind == "bidirectional" {
			found = true
		}
	}
	if !found {
		t.Error("expected bidirectional violation, got none")
	}
}

func TestValidateIntentDAG_DanglingParent(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			"_parents": "device", // device doesn't exist
		},
	}
	violations := ValidateIntentDAG(db)
	found := false
	for _, v := range violations {
		if v.Kind == "dangling_parent" {
			found = true
		}
	}
	if !found {
		t.Error("expected dangling_parent violation, got none")
	}
}

func TestValidateIntentDAG_Orphan(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			// No parents → not reachable from device
		},
	}
	violations := ValidateIntentDAG(db)
	found := false
	for _, v := range violations {
		if v.Kind == "orphan" && v.Resource == "vlan|100" {
			found = true
		}
	}
	if !found {
		t.Error("expected orphan violation for vlan|100, got none")
	}
}

// ============================================================================
// T8.2 — Intent DAG writeIntent / deleteIntent / ValidateIntentDAG Tests
// ============================================================================

// T8.2.1: writeIntent returns an error when a specified parent does not exist.
func TestWriteIntent_ParentExistence(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	cs := NewChangeSet("test", "test")
	err := n.writeIntent(cs, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100"}, []string{"device"})
	if err == nil {
		t.Fatal("expected error when parent does not exist, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error message = %q, want to contain 'does not exist'", err.Error())
	}
}

// T8.2.2: writeIntent called twice with the same resource and parents replaces
// params but preserves children.
func TestWriteIntent_IdempotentUpdate(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device root
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}

	cs := NewChangeSet("test", "test")
	// First write — vlan|100 under device
	if err := n.writeIntent(cs, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100"}, []string{"device"}); err != nil {
		t.Fatalf("first writeIntent failed: %v", err)
	}

	// Seed a child so we can verify children are preserved
	n.configDB.NewtronIntent["interface|Ethernet0"] = map[string]string{
		"operation": "apply-service", "state": "actuated",
		"_parents": "vlan|100",
	}
	// Register the child on the vlan|100 record directly (simulates a prior writeIntent for child)
	existing := n.GetIntent("vlan|100")
	existing.Children = []string{"interface|Ethernet0"}
	fields := existing.ToFields()
	n.configDB.NewtronIntent["vlan|100"] = fields

	// Second write — same parents, updated params (use description, not name, since
	// "name" is an identity field stripped by NewIntent during read-back)
	cs2 := NewChangeSet("test", "test")
	if err := n.writeIntent(cs2, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100", "description": "Servers"}, []string{"device"}); err != nil {
		t.Fatalf("second writeIntent failed: %v", err)
	}

	result := n.GetIntent("vlan|100")
	if result == nil {
		t.Fatal("intent not found after second writeIntent")
	}
	if result.Params["description"] != "Servers" {
		t.Errorf("Params[description] = %q, want Servers (params should be updated)", result.Params["description"])
	}
	if len(result.Children) != 1 || result.Children[0] != "interface|Ethernet0" {
		t.Errorf("Children = %v, want [interface|Ethernet0] (children should be preserved)", result.Children)
	}
}

// T8.2.3: writing an intent that already exists with different parents returns an error.
func TestWriteIntent_DifferentParentsError(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device and vrf|CUST roots
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}
	n.configDB.NewtronIntent["vrf|CUST"] = map[string]string{
		"operation": "create-vrf", "state": "actuated",
		"_parents": "device",
	}

	cs := NewChangeSet("test", "test")
	// First write with parent "device"
	if err := n.writeIntent(cs, "configure-interface", "interface|Ethernet0", map[string]string{}, []string{"device"}); err != nil {
		t.Fatalf("first writeIntent failed: %v", err)
	}

	// Second write with a different parent "vrf|CUST" — must error
	cs2 := NewChangeSet("test", "test")
	err := n.writeIntent(cs2, "configure-interface", "interface|Ethernet0", map[string]string{}, []string{"vrf|CUST"})
	if err == nil {
		t.Fatal("expected error for parents mismatch, got nil")
	}
}

// T8.2.4: after writing a child intent, the parent's Children contains the child.
func TestWriteIntent_ChildRegistered(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device root
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}

	cs := NewChangeSet("test", "test")
	if err := n.writeIntent(cs, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100"}, []string{"device"}); err != nil {
		t.Fatalf("writeIntent failed: %v", err)
	}

	deviceIntent := n.GetIntent("device")
	if deviceIntent == nil {
		t.Fatal("device intent not found")
	}
	found := false
	for _, child := range deviceIntent.Children {
		if child == "vlan|100" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("device Children = %v, want to contain 'vlan|100'", deviceIntent.Children)
	}
}

// T8.2.5: deleteIntent refuses if the intent has children (I5).
func TestDeleteIntent_RefusesWithChildren(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device root
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}

	// Write vlan|100 as child of device — this registers vlan|100 in device's _children
	cs := NewChangeSet("test", "test")
	if err := n.writeIntent(cs, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100"}, []string{"device"}); err != nil {
		t.Fatalf("writeIntent failed: %v", err)
	}

	// Attempt to delete device — should fail because it has a child
	cs2 := NewChangeSet("test", "test")
	err := n.deleteIntent(cs2, "device")
	if err == nil {
		t.Fatal("expected error deleting intent with children, got nil")
	}
	if !strings.Contains(err.Error(), "children") {
		t.Errorf("error = %q, want to mention 'children'", err.Error())
	}
}

// T8.2.6: deleting a child removes it from the parent's Children.
func TestDeleteIntent_DeregistersFromParent(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device root
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}

	// Write vlan|100 as child of device
	cs := NewChangeSet("test", "test")
	if err := n.writeIntent(cs, "create-vlan", "vlan|100", map[string]string{"vlan_id": "100"}, []string{"device"}); err != nil {
		t.Fatalf("writeIntent failed: %v", err)
	}

	// Assert device has vlan|100 as child
	deviceIntent := n.GetIntent("device")
	found := false
	for _, c := range deviceIntent.Children {
		if c == "vlan|100" {
			found = true
		}
	}
	if !found {
		t.Fatalf("device Children = %v, expected vlan|100 before delete", deviceIntent.Children)
	}

	// Delete vlan|100
	cs2 := NewChangeSet("test", "test")
	if err := n.deleteIntent(cs2, "vlan|100"); err != nil {
		t.Fatalf("deleteIntent failed: %v", err)
	}

	// device should no longer list vlan|100 as a child
	deviceIntent = n.GetIntent("device")
	for _, c := range deviceIntent.Children {
		if c == "vlan|100" {
			t.Error("device still has vlan|100 in Children after deleteIntent")
		}
	}
}

// T8.2.7: writeIntent with two parents registers the child in both parents' Children.
func TestWriteIntent_MultiParent(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	// Seed device root
	n.configDB.NewtronIntent["device"] = map[string]string{
		"operation": "setup-device", "state": "actuated",
	}

	// Write interface|Ethernet0 with parent device
	cs := NewChangeSet("test", "test")
	if err := n.writeIntent(cs, "configure-interface", "interface|Ethernet0", map[string]string{}, []string{"device"}); err != nil {
		t.Fatalf("writeIntent interface|Ethernet0 failed: %v", err)
	}

	// Write acl|EDGE_IN with parent device
	cs2 := NewChangeSet("test", "test")
	if err := n.writeIntent(cs2, "create-acl", "acl|EDGE_IN", map[string]string{"name": "EDGE_IN"}, []string{"device"}); err != nil {
		t.Fatalf("writeIntent acl|EDGE_IN failed: %v", err)
	}

	// Write ACL binding with two parents
	cs3 := NewChangeSet("test", "test")
	if err := n.writeIntent(cs3, "bind-acl", "interface|Ethernet0|acl|ingress", map[string]string{"direction": "ingress"}, []string{"interface|Ethernet0", "acl|EDGE_IN"}); err != nil {
		t.Fatalf("writeIntent acl binding failed: %v", err)
	}

	// Both parents must list the binding as a child
	ifaceIntent := n.GetIntent("interface|Ethernet0")
	if ifaceIntent == nil {
		t.Fatal("interface|Ethernet0 intent not found")
	}
	aclIntent := n.GetIntent("acl|EDGE_IN")
	if aclIntent == nil {
		t.Fatal("acl|EDGE_IN intent not found")
	}

	checkChild := func(parent *sonic.Intent, parentName, child string) {
		t.Helper()
		for _, c := range parent.Children {
			if c == child {
				return
			}
		}
		t.Errorf("%s Children = %v, want to contain %q", parentName, parent.Children, child)
	}
	checkChild(ifaceIntent, "interface|Ethernet0", "interface|Ethernet0|acl|ingress")
	checkChild(aclIntent, "acl|EDGE_IN", "interface|Ethernet0|acl|ingress")
}

// T8.2.8: IntentsToSteps produces topologically correct order (parents before children).
func TestIntentsToSteps_TopologicalOrder(t *testing.T) {
	intents := map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
			"_children": "vlan|100",
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			"_parents":  "device",
			"_children": "interface|Ethernet0",
		},
		"interface|Ethernet0": {
			"operation": "apply-service", "state": "actuated",
			"service_name": "transit",
			"_parents":     "vlan|100",
		},
	}

	steps := IntentsToSteps(intents)
	if len(steps) != 3 {
		t.Fatalf("IntentsToSteps returned %d steps, want 3", len(steps))
	}

	// Find positions of each step
	opAt := func(url string) int {
		for i, s := range steps {
			if s.URL == url {
				return i
			}
		}
		return -1
	}

	posDevice := opAt("/setup-device")
	posVlan := opAt("/create-vlan")
	posIface := opAt("/interface/Ethernet0/apply-service")

	if posDevice < 0 || posVlan < 0 || posIface < 0 {
		t.Fatalf("steps = %v; missing one of setup-device/create-vlan/apply-service", steps)
	}
	if !(posDevice < posVlan) {
		t.Errorf("setup-device (pos %d) must come before create-vlan (pos %d)", posDevice, posVlan)
	}
	if !(posVlan < posIface) {
		t.Errorf("create-vlan (pos %d) must come before apply-service (pos %d)", posVlan, posIface)
	}
}

// T8.2.9: ValidateIntentDAG detects when a parent doesn't list a child (bidirectional
// inconsistency from the child's side).
func TestValidateIntentDAG_BidirectionalInconsistency(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
			// _children intentionally omitted — does NOT list vlan|100
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			"_parents": "device", // vlan|100 claims device as parent ...
			// ... but device does not list vlan|100 in _children → bidirectional violation
		},
	}

	violations := ValidateIntentDAG(db)
	found := false
	for _, v := range violations {
		if v.Kind == "bidirectional" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected bidirectional violation, got %v", violations)
	}
}

// T8.2.10: ValidateIntentDAG detects orphaned records not reachable from the device root.
func TestValidateIntentDAG_OrphanDetection(t *testing.T) {
	db := sonic.NewConfigDB()
	db.NewtronIntent = map[string]map[string]string{
		"device": {
			"operation": "setup-device", "state": "actuated",
			"_children": "vlan|100",
		},
		"vlan|100": {
			"operation": "create-vlan", "state": "actuated",
			"_parents": "device",
		},
		// vrf|CUST has no parents and is not listed in any _children → orphan
		"vrf|CUST": {
			"operation": "create-vrf", "state": "actuated",
		},
	}

	violations := ValidateIntentDAG(db)
	found := false
	for _, v := range violations {
		if v.Kind == "orphan" && v.Resource == "vrf|CUST" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected orphan violation for vrf|CUST, got %v", violations)
	}
}

func TestIntentsByPrefix(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	n.configDB.NewtronIntent = map[string]map[string]string{
		"device":            {"operation": "setup-device", "state": "actuated"},
		"vlan|100":          {"operation": "create-vlan", "state": "actuated", "vlan_id": "100"},
		"vlan|200":          {"operation": "create-vlan", "state": "actuated", "vlan_id": "200"},
		"vrf|CUSTOMER":      {"operation": "create-vrf", "state": "actuated"},
		"interface|Ethernet0": {"operation": "apply-service", "state": "actuated"},
	}

	// Prefix "vlan|" should match exactly 2
	vlans := n.IntentsByPrefix("vlan|")
	if len(vlans) != 2 {
		t.Fatalf("IntentsByPrefix(\"vlan|\") = %d, want 2", len(vlans))
	}
	if _, ok := vlans["vlan|100"]; !ok {
		t.Error("missing vlan|100")
	}
	if _, ok := vlans["vlan|200"]; !ok {
		t.Error("missing vlan|200")
	}

	// Prefix "vrf|" should match 1
	vrfs := n.IntentsByPrefix("vrf|")
	if len(vrfs) != 1 {
		t.Fatalf("IntentsByPrefix(\"vrf|\") = %d, want 1", len(vrfs))
	}

	// Prefix "nonexistent|" should match 0
	empty := n.IntentsByPrefix("nonexistent|")
	if len(empty) != 0 {
		t.Fatalf("IntentsByPrefix(\"nonexistent|\") = %d, want 0", len(empty))
	}

	// Nil configDB should return empty
	n2 := New(sp, "test2", &spec.DeviceProfile{}, &spec.ResolvedProfile{})
	if got := n2.IntentsByPrefix("vlan|"); len(got) != 0 {
		t.Fatalf("IntentsByPrefix on nil configDB = %d, want 0", len(got))
	}
}

func TestIntentsByParam(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	n.configDB.NewtronIntent = map[string]map[string]string{
		"interface|Ethernet0":    {"operation": "configure-interface", "state": "actuated", "vrf": "CUSTOMER"},
		"interface|Ethernet4":    {"operation": "configure-interface", "state": "actuated", "vrf": "CUSTOMER"},
		"interface|Ethernet8":    {"operation": "configure-interface", "state": "actuated", "vrf": "MGMT"},
		"interface|Vlan100":      {"operation": "configure-irb", "state": "actuated", "vrf": "CUSTOMER"},
	}

	// vrf=CUSTOMER should match 3
	cust := n.IntentsByParam("vrf", "CUSTOMER")
	if len(cust) != 3 {
		t.Fatalf("IntentsByParam(\"vrf\",\"CUSTOMER\") = %d, want 3", len(cust))
	}

	// vrf=MGMT should match 1
	mgmt := n.IntentsByParam("vrf", "MGMT")
	if len(mgmt) != 1 {
		t.Fatalf("IntentsByParam(\"vrf\",\"MGMT\") = %d, want 1", len(mgmt))
	}

	// vrf=NONEXISTENT should match 0
	empty := n.IntentsByParam("vrf", "NONEXISTENT")
	if len(empty) != 0 {
		t.Fatalf("IntentsByParam(\"vrf\",\"NONEXISTENT\") = %d, want 0", len(empty))
	}
}

func TestIntentsByOp(t *testing.T) {
	sp := &testSpecProvider{}
	n := NewAbstract(sp, "test", &spec.DeviceProfile{}, &spec.ResolvedProfile{})

	n.configDB.NewtronIntent = map[string]map[string]string{
		"device":            {"operation": "setup-device", "state": "actuated"},
		"vlan|100":          {"operation": "create-vlan", "state": "actuated"},
		"vlan|200":          {"operation": "create-vlan", "state": "actuated"},
		"vrf|CUSTOMER":      {"operation": "create-vrf", "state": "actuated"},
		"interface|Vlan100": {"operation": "configure-irb", "state": "actuated", "anycast_mac": "00:00:5e:00:01:01"},
		"interface|Vlan200": {"operation": "configure-irb", "state": "actuated"},
	}

	// "create-vlan" should match 2
	vlans := n.IntentsByOp("create-vlan")
	if len(vlans) != 2 {
		t.Fatalf("IntentsByOp(\"create-vlan\") = %d, want 2", len(vlans))
	}

	// "configure-irb" should match 2
	irbs := n.IntentsByOp("configure-irb")
	if len(irbs) != 2 {
		t.Fatalf("IntentsByOp(\"configure-irb\") = %d, want 2", len(irbs))
	}

	// "setup-device" should match 1
	devs := n.IntentsByOp("setup-device")
	if len(devs) != 1 {
		t.Fatalf("IntentsByOp(\"setup-device\") = %d, want 1", len(devs))
	}

	// nonexistent op should match 0
	empty := n.IntentsByOp("nonexistent")
	if len(empty) != 0 {
		t.Fatalf("IntentsByOp(\"nonexistent\") = %d, want 0", len(empty))
	}
}
