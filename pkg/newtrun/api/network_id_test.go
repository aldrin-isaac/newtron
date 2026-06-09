package api

import "testing"

// TestResolveNetworkID pins the fallback chain that determines which newtron
// network identifier the runner connects to. The chain is the surface area of
// issue #116 — every other change in the PR routes through this function.
func TestResolveNetworkID(t *testing.T) {
	cases := []struct {
		name           string
		reqNetworkID   string
		suiteTopology  string
		cfgDefault     string
		want           string
	}{
		{
			name:          "request_override_wins",
			reqNetworkID:  "operator-override",
			suiteTopology: "from-suite",
			cfgDefault:    "default",
			want:          "operator-override",
		},
		{
			name:          "suite_topology_used_when_request_empty",
			reqNetworkID:  "",
			suiteTopology: "2node-vs-service",
			cfgDefault:    "default",
			want:          "2node-vs-service",
		},
		{
			name:          "server_default_used_when_both_empty",
			reqNetworkID:  "",
			suiteTopology: "",
			cfgDefault:    "default",
			want:          "default",
		},
		{
			name:          "request_override_wins_over_suite_topology",
			reqNetworkID:  "operator-override",
			suiteTopology: "2node-vs-service",
			cfgDefault:    "default",
			want:          "operator-override",
		},
		{
			name:          "inline_path_empty_suite_topology_falls_through",
			reqNetworkID:  "",
			suiteTopology: "",
			cfgDefault:    "running-lab",
			want:          "running-lab",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveNetworkID(tc.reqNetworkID, tc.suiteTopology, tc.cfgDefault)
			if got != tc.want {
				t.Errorf("resolveNetworkID(%q, %q, %q) = %q, want %q",
					tc.reqNetworkID, tc.suiteTopology, tc.cfgDefault, got, tc.want)
			}
		})
	}
}
