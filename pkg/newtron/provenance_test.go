package newtron

import "testing"

func TestDeriveSpecRef(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		params   map[string]any
		wantKind string
		wantName string
	}{
		{
			// spec_name is the spec's CANONICAL identity (NormalizeName: upper +
			// hyphen→underscore), not the raw step casing — so it equals the
			// GET /services key regardless of how it was typed at apply time.
			name:     "apply-service — canonicalized (transit → TRANSIT)",
			url:      "/interfaces/Ethernet0/apply-service",
			params:   map[string]any{"service": "transit", "ip_address": "10.1.0.0/31"},
			wantKind: "service", wantName: "TRANSIT",
		},
		{
			name:     "bind-ipvpn — canonicalized (irb → IRB)",
			url:      "/bind-ipvpn",
			params:   map[string]any{"ipvpn": "irb"},
			wantKind: "ipvpn", wantName: "IRB",
		},
		{
			name:     "bind-macvpn — canonicalized (blue → BLUE)",
			url:      "/interfaces/Ethernet4/bind-macvpn",
			params:   map[string]any{"macvpn": "blue", "vlan_id": "400"},
			wantKind: "macvpn", wantName: "BLUE",
		},
		{
			name:     "bind-qos (param key 'policy') — canonicalized (gold → GOLD)",
			url:      "/interfaces/Ethernet0/bind-qos",
			params:   map[string]any{"policy": "gold"},
			wantKind: "qos", wantName: "GOLD",
		},
		{
			name:     "primitive create-vrf has no source spec",
			url:      "/create-vrf",
			params:   map[string]any{"name": "Vrf_CUST1"},
			wantKind: "", wantName: "",
		},
		{
			name:     "service-derived create-acl — source filter, canonicalized (mgmt-in → MGMT_IN)",
			url:      "/create-acl",
			params:   map[string]any{"name": "acl_a1b2c3d4", "filter": "mgmt-in"},
			wantKind: "filter", wantName: "MGMT_IN",
		},
		{
			name:     "standalone/raw create-acl (no source filter) → empty, not a misleading name",
			url:      "/create-acl",
			params:   map[string]any{"name": "mgmt-in"},
			wantKind: "", wantName: "",
		},
		{
			name:     "spec op but name param absent → no half-record",
			url:      "/bind-ipvpn",
			params:   map[string]any{},
			wantKind: "", wantName: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, name := DeriveSpecRef(tc.url, tc.params)
			if kind != tc.wantKind || name != tc.wantName {
				t.Errorf("DeriveSpecRef(%q) = (%q, %q); want (%q, %q)",
					tc.url, kind, name, tc.wantKind, tc.wantName)
			}
		})
	}
}
