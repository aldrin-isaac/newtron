package network

import (
	"os"
	"path/filepath"
	"testing"
)

// TestListScopedSpecs_AllThreeScopes pins the cross-scope inventory: a spec
// defined at the network scope, one at a zone, and one at a node nodeSpec must
// each appear once, tagged with the correct scope + instance. Uses prefix lists
// (the simplest spec kind — a []string with no nested validation) at every scope
// so the test exercises the enumeration, not per-kind spec validity.
func TestListScopedSpecs_AllThreeScopes(t *testing.T) {
	dir, err := os.MkdirTemp("", "scope-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// network.json — a network-scope prefix list plus one zone "amer" carrying a
	// zone-scope prefix list.
	netJSON := `{
		"schema_version": "1.0",
		"prefix_lists": { "NET_PL": ["10.0.0.0/8"] },
		"zones": {
			"amer": { "prefix_lists": { "ZONE_PL": ["10.1.0.0/16"] } }
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(netJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "platforms.json"),
		[]byte(`{"schema_version":"1.0","platforms":{}}`), 0o644); err != nil {
		t.Fatalf("write platforms.json: %v", err)
	}

	// nodes/leaf1.json — a switch nodeSpec in zone "amer" with a node-scope
	// prefix list. mgmt_ip/loopback_ip/zone are the required switch fields.
	nodesDir := filepath.Join(dir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("mkdir nodes: %v", err)
	}
	nodeSpecJSON := `{
		"mgmt_ip": "10.0.0.1",
		"loopback_ip": "10.255.0.1",
		"zone": "amer",
		"prefix_lists": { "NODE_PL": ["10.2.0.0/16"] }
	}`
	if err := os.WriteFile(filepath.Join(nodesDir, "leaf1.json"), []byte(nodeSpecJSON), 0o644); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}

	got, err := n.ListScopedSpecs()
	if err != nil {
		t.Fatalf("ListScopedSpecs: %v", err)
	}

	// Expect exactly the three definitions, in sorted order
	// (network < node < zone by scope token; instance/kind/name break ties).
	want := []ScopedSpec{
		{Scope: "network", Instance: "", Kind: "PrefixListSpec", Name: "NET_PL"},
		{Scope: "node", Instance: "leaf1", Kind: "PrefixListSpec", Name: "NODE_PL"},
		{Scope: "zone", Instance: "amer", Kind: "PrefixListSpec", Name: "ZONE_PL"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d instances, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("instance[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestListScopedSpecs_Empty pins the empty case: a network with no specs at any
// scope returns an empty inventory, not an error.
func TestListScopedSpecs_Empty(t *testing.T) {
	n := loadTestNetwork(t)
	got, err := n.ListScopedSpecs()
	if err != nil {
		t.Fatalf("ListScopedSpecs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d instances, want 0: %+v", len(got), got)
	}
}
