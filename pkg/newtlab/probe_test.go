package newtlab

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
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

	if err := ProbeAllPorts(allocs, ""); err != nil {
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

	err = ProbeAllPorts(allocs, "")
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

	err = ProbeAllPorts(allocs, "")
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

// seedLabState writes a state.json under the test HOME so attribution can
// find it. Callers must `t.Setenv("HOME", tmpDir)` and `resetHomeCache(t)`
// before seeding. Returns the SSH/console/link/stats ports it allocated so
// tests can probe them.
func seedLabState(t *testing.T, name string, sshPID, bridgePID int, sshPort, consolePort, linkA, linkZ, statsPort int) {
	t.Helper()
	state := &LabState{
		Name: name,
		Nodes: map[string]*NodeState{
			"node1": {
				PID:         sshPID,
				SSHPort:     sshPort,
				ConsolePort: consolePort,
			},
		},
		Links: []*LinkState{
			{A: "node1:Eth0", Z: "node2:Eth0", APort: linkA, ZPort: linkZ, WorkerHost: ""},
		},
		Bridges: map[string]*BridgeState{
			"": {PID: bridgePID, StatsAddr: fmt.Sprintf("127.0.0.1:%d", statsPort)},
		},
	}
	if err := SaveState(state); err != nil {
		t.Fatalf("seed lab %q: %v", name, err)
	}
}

// resetHomeCache clears the once-cached HOME so a t.Setenv earlier in the
// same test takes effect for LabDir / ListLabs. Thin wrapper over the
// package's existing resetHomeDir helper.
func resetHomeCache(t *testing.T) {
	t.Helper()
	resetHomeDir()
}

// TestProbeAllPorts_AttributesSSHPort verifies the conflict error names the
// holding lab and PID when the conflict is on a peer lab's SSH port.
func TestProbeAllPorts_AttributesSSHPort(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetHomeCache(t)

	// Bind a port to simulate the peer lab's qemu holding it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	sshPort := ln.Addr().(*net.TCPAddr).Port

	// Seed the peer lab's state.json claiming the same port as its SSH.
	seedLabState(t, "peer-lab", 42424, 0, sshPort, 30001, 20002, 20003, 19999)

	allocs := []PortAllocation{{Port: sshPort, Purpose: "switch1 SSH"}}
	err = ProbeAllPorts(allocs, "self-lab")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"peer-lab", "PID 42424", "SSH", "newtlab destroy peer-lab"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// TestProbeAllPorts_AttributesBridgeStatsPort verifies the bridge-stats port
// is attributed via the BridgeState.StatsAddr parsing path.
func TestProbeAllPorts_AttributesBridgeStatsPort(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetHomeCache(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	statsPort := ln.Addr().(*net.TCPAddr).Port

	seedLabState(t, "peer-lab", 100, 200, 40000, 30000, 20000, 20001, statsPort)

	allocs := []PortAllocation{{Port: statsPort, Purpose: "bridge stats (local)"}}
	err = ProbeAllPorts(allocs, "self-lab")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"peer-lab", "PID 200", "bridge stats", "newtlab destroy peer-lab"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// TestProbeAllPorts_FallsBackForExternalProcess verifies that ports held by
// processes outside newtlab's purview keep the bare error format (no fake
// lab attribution).
func TestProbeAllPorts_FallsBackForExternalProcess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetHomeCache(t)
	// No seeded lab state. The conflict has no newtlab owner.

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	allocs := []PortAllocation{{Port: port, Purpose: "switch1 SSH"}}
	err = ProbeAllPorts(allocs, "self-lab")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "in use") {
		t.Errorf("expected bare 'in use' fallback, got: %s", msg)
	}
	if strings.Contains(msg, "held by lab") {
		t.Errorf("should not falsely attribute external port, got: %s", msg)
	}
}

// TestProbeAllPorts_ExcludesSelfLab verifies the lab being deployed doesn't
// attribute its own ports to itself when its prior state.json still lists
// them — covers the redeploy/--force flow.
func TestProbeAllPorts_ExcludesSelfLab(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetHomeCache(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	seedLabState(t, "self-lab", 999, 0, port, 30001, 20002, 20003, 19999)

	allocs := []PortAllocation{{Port: port, Purpose: "switch1 SSH"}}
	err = ProbeAllPorts(allocs, "self-lab")
	if err == nil {
		t.Fatal("expected conflict error (port is held externally), got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, "held by lab \"self-lab\"") {
		t.Errorf("self-lab should not be attributed as its own owner; got: %s", msg)
	}
}

// TestAttributePortOwners_CorruptStateSkipped verifies a corrupt state.json
// in one lab doesn't poison attribution of unrelated labs.
func TestAttributePortOwners_CorruptStateSkipped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	resetHomeCache(t)

	// Healthy peer lab.
	seedLabState(t, "good-lab", 111, 0, 40000, 30000, 20000, 20001, 19999)

	// Corrupt peer lab — invalid JSON in state.json.
	badDir := filepath.Join(tmp, ".newtlab", "labs", "bad-lab")
	if err := writeRaw(badDir, "state.json", "not valid json"); err != nil {
		t.Fatalf("seed corrupt lab: %v", err)
	}

	owners := attributePortOwners("")
	if owner, ok := owners[40000]; !ok {
		t.Errorf("good-lab attribution missing")
	} else if owner.Lab != "good-lab" {
		t.Errorf("port 40000 owner: got %q, want good-lab", owner.Lab)
	}
}

// writeRaw is the test helper for seeding malformed state files.
func writeRaw(dir, name, body string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
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

