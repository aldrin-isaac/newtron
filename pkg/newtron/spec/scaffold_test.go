package spec

import "testing"

// TestScaffoldTopologyNode_Fields pins the setup-device bring-up shape the
// scaffold derives from a node definition — the single-owner replacement for
// newtcon's former client-side derivation.
func TestScaffoldTopologyNode_Fields(t *testing.T) {
	node := &NodeSpec{Platform: "Force10-S6000", UnderlayASN: 65001}
	dev := ScaffoldTopologyNode("switch1", node, "Force10-S6000", false)

	if len(dev.Steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(dev.Steps))
	}
	step := dev.Steps[0]
	if step.URL != "/setup-device" {
		t.Errorf("step URL = %q, want /setup-device", step.URL)
	}
	fields, ok := step.Params["fields"].(map[string]any)
	if !ok {
		t.Fatalf("params.fields not a map: %T", step.Params["fields"])
	}
	want := map[string]any{
		"hostname":                   "switch1",
		"type":                       "LeafRouter",
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
		"hwsku":                      "Force10-S6000",
		"bgp_asn":                    "65001",
	}
	if len(fields) != len(want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%q] = %v, want %v", k, fields[k], v)
		}
	}
	// No ports scaffolded — ports are added on demand later.
	if dev.Ports != nil {
		t.Errorf("scaffold should not create ports, got %v", dev.Ports)
	}
}

// TestScaffoldTopologyNode_OmitsUnset confirms hwsku is dropped when the node
// has no platform HWSKU, and bgp_asn is dropped when UnderlayASN is 0 — the
// field is absent, not empty (setup-device then infers a default).
func TestScaffoldTopologyNode_OmitsUnset(t *testing.T) {
	dev := ScaffoldTopologyNode("host1", &NodeSpec{}, "", false)
	fields := dev.Steps[0].Params["fields"].(map[string]any)
	if _, ok := fields["hwsku"]; ok {
		t.Errorf("hwsku should be omitted when unresolved, got %v", fields["hwsku"])
	}
	if _, ok := fields["bgp_asn"]; ok {
		t.Errorf("bgp_asn should be omitted when UnderlayASN is 0, got %v", fields["bgp_asn"])
	}
	// The constant bring-up fields are always present.
	if fields["type"] != "LeafRouter" || fields["hostname"] != "host1" {
		t.Errorf("constant fields missing: %v", fields)
	}
}
