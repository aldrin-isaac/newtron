package newtlab

import "testing"

// TestNormalizeNodeNICs pins the positional-binding contract (RCA-050): the
// guest's Nth data NIC backs the Nth front-panel port, so a node's NICs must
// come out sorted by index with interior gaps filled by disconnected fillers.
func TestNormalizeNodeNICs(t *testing.T) {
	node := &NodeConfig{
		Name:       "sw1",
		DeviceType: "switch",
		NICs: []NICConfig{
			// Link order: E4 (nic 5) allocated before E0 (nic 1) — both the
			// gap and the mis-ordering the live incident exhibited.
			{Index: 5, NetdevID: "eth5", Interface: "Ethernet4", ConnectAddr: "127.0.0.1:20003"},
			{Index: 1, NetdevID: "eth1", Interface: "Ethernet0", ConnectAddr: "127.0.0.1:20004"},
		},
	}
	normalizeNodeNICs(map[string]*NodeConfig{"sw1": node})

	if len(node.NICs) != 5 {
		t.Fatalf("NICs = %d, want 5 (indexes 1..5, gaps filled)", len(node.NICs))
	}
	for i, nic := range node.NICs {
		if nic.Index != i+1 {
			t.Errorf("position %d has index %d — enumeration order must equal nic_index", i, nic.Index)
		}
	}
	if node.NICs[0].ConnectAddr == "" || node.NICs[4].ConnectAddr == "" {
		t.Errorf("wired NICs lost their bridge address: %+v", node.NICs)
	}
	for _, i := range []int{1, 2, 3} { // positions of nic 2,3,4
		if node.NICs[i].ConnectAddr != "" {
			t.Errorf("filler at index %d must be disconnected, got %q", node.NICs[i].Index, node.NICs[i].ConnectAddr)
		}
		if node.NICs[i].MAC == "" {
			t.Errorf("filler at index %d needs a MAC", node.NICs[i].Index)
		}
	}
}

// TestNormalizeNodeNICs_DenseUntouched: a dense, ordered topology (every
// in-repo fabric) gets no fillers and keeps its wiring.
func TestNormalizeNodeNICs_DenseUntouched(t *testing.T) {
	node := &NodeConfig{Name: "sw1", DeviceType: "switch", NICs: []NICConfig{
		{Index: 1, NetdevID: "eth1", ConnectAddr: "a"},
		{Index: 2, NetdevID: "eth2", ConnectAddr: "b"},
	}}
	normalizeNodeNICs(map[string]*NodeConfig{"sw1": node})
	if len(node.NICs) != 2 || node.NICs[0].ConnectAddr != "a" || node.NICs[1].ConnectAddr != "b" {
		t.Fatalf("dense node must be untouched: %+v", node.NICs)
	}
}
