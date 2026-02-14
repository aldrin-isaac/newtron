package newtlab

import (
	"net"
	"strings"
	"testing"

	"github.com/newtron-network/newtron/pkg/spec"
)

func TestCollectAllPorts(t *testing.T) {
	lab := &Lab{
		Config: &VMLabConfig{
			LinkPortBase:    20000,
			ConsolePortBase: 30000,
			SSHPortBase:     40000,
		},
		Nodes: map[string]*NodeConfig{
			"spine1": {Name: "spine1", SSHPort: 40000, ConsolePort: 30000},
			"leaf1":  {Name: "leaf1", SSHPort: 40001, ConsolePort: 30001},
		},
		Links: []*LinkConfig{
			{
				A:          LinkEndpoint{Device: "spine1", Interface: "Ethernet0"},
				Z:          LinkEndpoint{Device: "leaf1", Interface: "Ethernet0"},
				APort:      20000,
				ZPort:      20001,
				WorkerHost: "",
			},
		},
	}

	allocs := CollectAllPorts(lab)

	// Should have: 2 SSH + 2 console + 2 link + 1 stats = 7
	purposeSet := map[string]bool{}
	for _, a := range allocs {
		purposeSet[a.Purpose] = true
	}

	expected := []string{
		"spine1 SSH",
		"spine1 console",
		"leaf1 SSH",
		"leaf1 console",
		"link spine1:Ethernet0 A-side",
		"link leaf1:Ethernet0 Z-side",
		"bridge stats (local)",
	}
	for _, e := range expected {
		if !purposeSet[e] {
			t.Errorf("missing port allocation for %q", e)
		}
	}

	if len(allocs) != len(expected) {
		t.Errorf("got %d allocations, want %d", len(allocs), len(expected))
	}
}

func TestProbeAllPorts_AllFree(t *testing.T) {
	// Get OS-assigned free ports
	p1 := getFreePort(t)
	p2 := getFreePort(t)

	allocs := []PortAllocation{
		{Port: p1, Purpose: "test SSH"},
		{Port: p2, Purpose: "test console"},
	}

	if err := ProbeAllPorts(allocs); err != nil {
		t.Fatalf("expected no conflicts, got: %v", err)
	}
}

func TestProbeAllPorts_Conflict(t *testing.T) {
	// Occupy a port
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	allocs := []PortAllocation{
		{Port: port, Purpose: "test port"},
	}

	err = ProbeAllPorts(allocs)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "test port") || !strings.Contains(got, "in use") {
		t.Errorf("error should mention purpose and 'in use', got: %v", err)
	}
}

func TestProbeAllPorts_MultipleConflicts(t *testing.T) {
	// Occupy two ports
	ln1, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln1.Close()

	ln2, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()

	port1 := ln1.Addr().(*net.TCPAddr).Port
	port2 := ln2.Addr().(*net.TCPAddr).Port

	allocs := []PortAllocation{
		{Port: port1, Purpose: "port A"},
		{Port: port2, Purpose: "port B"},
	}

	err = ProbeAllPorts(allocs)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "port A") || !strings.Contains(errMsg, "port B") {
		t.Errorf("error should report both conflicts, got: %v", err)
	}
}

func TestProbePortLocal(t *testing.T) {
	// Free port should succeed
	freePort := getFreePort(t)
	if err := probePortLocal(freePort); err != nil {
		t.Errorf("expected free port, got: %v", err)
	}

	// Occupied port should fail
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	if err := probePortLocal(port); err == nil {
		t.Error("expected error for occupied port, got nil")
	}
}

func TestResolveNewtLabConfig_WithServers(t *testing.T) {
	cfg := &spec.NewtLabConfig{
		SSHPortBase:  50000,
		LinkPortBase: 20000,
		Servers: []*spec.ServerConfig{
			{Name: "server-a", Address: "10.0.0.1", MaxNodes: 4},
			{Name: "server-b", Address: "10.0.0.2", MaxNodes: 4},
		},
	}

	resolved := resolveNewtLabConfig(cfg)

	if len(resolved.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(resolved.Servers))
	}
	if resolved.Hosts["server-a"] != "10.0.0.1" {
		t.Errorf("expected Hosts[server-a]=10.0.0.1, got %q", resolved.Hosts["server-a"])
	}
	if resolved.Hosts["server-b"] != "10.0.0.2" {
		t.Errorf("expected Hosts[server-b]=10.0.0.2, got %q", resolved.Hosts["server-b"])
	}
	if resolved.SSHPortBase != 50000 {
		t.Errorf("expected SSHPortBase=50000, got %d", resolved.SSHPortBase)
	}
}

