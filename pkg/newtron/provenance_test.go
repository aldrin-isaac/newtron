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
			name:     "apply-service (interface-scoped URL)",
			url:      "/interfaces/Ethernet0/apply-service",
			params:   map[string]any{"service": "transit", "ip_address": "10.1.0.0/31"},
			wantKind: "service", wantName: "transit",
		},
		{
			name:     "bind-ipvpn (node-scoped URL)",
			url:      "/bind-ipvpn",
			params:   map[string]any{"ipvpn": "IRB"},
			wantKind: "ipvpn", wantName: "IRB",
		},
		{
			name:     "bind-macvpn",
			url:      "/interfaces/Ethernet4/bind-macvpn",
			params:   map[string]any{"macvpn": "blue", "vlan_id": "400"},
			wantKind: "macvpn", wantName: "blue",
		},
		{
			name:     "bind-qos (param key is 'policy')",
			url:      "/interfaces/Ethernet0/bind-qos",
			params:   map[string]any{"policy": "gold"},
			wantKind: "qos", wantName: "gold",
		},
		{
			name:     "primitive create-vrf has no source spec",
			url:      "/create-vrf",
			params:   map[string]any{"name": "Vrf_CUST1"},
			wantKind: "", wantName: "",
		},
		{
			name:     "deferred kind: create-acl returns empty rather than a misleading name",
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
