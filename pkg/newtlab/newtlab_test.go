package newtlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/newtron-network/newtron/pkg/spec"
)

// ============================================================================
// Interface Map Tests
// ============================================================================

func TestResolveNICIndex_Stride4(t *testing.T) {
	tests := []struct {
		iface   string
		want    int
		wantErr bool
	}{
		{"Ethernet0", 1, false},
		{"Ethernet4", 2, false},
		{"Ethernet8", 3, false},
		{"Ethernet12", 4, false},
		{"Ethernet1", 0, true},  // not divisible by 4
		{"Ethernet3", 0, true},  // not divisible by 4
		{"Loopback0", 0, true},  // not Ethernet
	}

	for _, tt := range tests {
		got, err := ResolveNICIndex("stride-4", tt.iface, nil)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ResolveNICIndex(stride-4, %q) = %d, want error", tt.iface, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveNICIndex(stride-4, %q) error: %v", tt.iface, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ResolveNICIndex(stride-4, %q) = %d, want %d", tt.iface, got, tt.want)
		}
	}
}

func TestResolveNICIndex_Sequential(t *testing.T) {
	tests := []struct {
		iface string
		want  int
	}{
		{"Ethernet0", 1},
		{"Ethernet1", 2},
		{"Ethernet2", 3},
		{"Ethernet3", 4},
	}

	for _, tt := range tests {
		got, err := ResolveNICIndex("sequential", tt.iface, nil)
		if err != nil {
			t.Errorf("ResolveNICIndex(sequential, %q) error: %v", tt.iface, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ResolveNICIndex(sequential, %q) = %d, want %d", tt.iface, got, tt.want)
		}
	}
}

func TestResolveNICIndex_Custom(t *testing.T) {
	customMap := map[string]int{
		"Ethernet0": 3,
		"Ethernet4": 1,
	}

	got, err := ResolveNICIndex("custom", "Ethernet0", customMap)
	if err != nil {
		t.Fatalf("ResolveNICIndex(custom, Ethernet0) error: %v", err)
	}
	if got != 3 {
		t.Errorf("ResolveNICIndex(custom, Ethernet0) = %d, want 3", got)
	}

	_, err = ResolveNICIndex("custom", "Ethernet8", customMap)
	if err == nil {
		t.Error("ResolveNICIndex(custom, Ethernet8) should error for missing key")
	}

	_, err = ResolveNICIndex("custom", "Ethernet0", nil)
	if err == nil {
		t.Error("ResolveNICIndex(custom, Ethernet0, nil) should error for nil map")
	}
}

func TestResolveInterfaceName(t *testing.T) {
	tests := []struct {
		mapType string
		nic     int
		want    string
	}{
		{"stride-4", 1, "Ethernet0"},
		{"stride-4", 2, "Ethernet4"},
		{"stride-4", 3, "Ethernet8"},
		{"sequential", 1, "Ethernet0"},
		{"sequential", 2, "Ethernet1"},
		{"sequential", 3, "Ethernet2"},
	}

	for _, tt := range tests {
		got := ResolveInterfaceName(tt.mapType, tt.nic, nil)
		if got != tt.want {
			t.Errorf("ResolveInterfaceName(%s, %d) = %q, want %q", tt.mapType, tt.nic, got, tt.want)
		}
	}
}

func TestResolveInterfaceName_Custom(t *testing.T) {
	customMap := map[string]int{"Ethernet0": 3, "Ethernet4": 1}
	got := ResolveInterfaceName("custom", 3, customMap)
	if got != "Ethernet0" {
		t.Errorf("ResolveInterfaceName(custom, 3) = %q, want Ethernet0", got)
	}
}

func TestParseEthernetIndex(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"Ethernet0", 0},
		{"Ethernet4", 4},
		{"Ethernet12", 12},
		{"Loopback0", -1},
		{"PortChannel100", -1},
		{"", -1},
	}

	for _, tt := range tests {
		got := parseEthernetIndex(tt.name)
		if got != tt.want {
			t.Errorf("parseEthernetIndex(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// ============================================================================
// Node Resolution Tests
// ============================================================================

func TestResolveNodeConfig_ProfileOverridesPlatform(t *testing.T) {
	profile := &spec.DeviceProfile{
		Platform: "sonic-vs",
		VMImage:  "/override/image.qcow2",
		VMMemory: 8192,
		VMCPUs:   4,
		SSHUser:  "myuser",
		SSHPass:  "mypass",
		VMHost:   "server-a",
	}
	platform := &spec.PlatformSpec{
		VMImage:   "/platform/image.qcow2",
		VMMemory:  4096,
		VMCPUs:    2,
		VMCredentials: &spec.VMCredentials{User: "admin", Pass: "admin"},
	}

	nc, err := ResolveNodeConfig("leaf1", profile, platform)
	if err != nil {
		t.Fatalf("ResolveNodeConfig error: %v", err)
	}

	if nc.Image != "/override/image.qcow2" {
		t.Errorf("Image = %q, want /override/image.qcow2", nc.Image)
	}
	if nc.Memory != 8192 {
		t.Errorf("Memory = %d, want 8192", nc.Memory)
	}
	if nc.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", nc.CPUs)
	}
	if nc.SSHUser != "myuser" {
		t.Errorf("SSHUser = %q, want myuser", nc.SSHUser)
	}
	if nc.SSHPass != "mypass" {
		t.Errorf("SSHPass = %q, want mypass", nc.SSHPass)
	}
	if nc.Host != "server-a" {
		t.Errorf("Host = %q, want server-a", nc.Host)
	}
}

func TestResolveNodeConfig_PlatformDefaults(t *testing.T) {
	profile := &spec.DeviceProfile{Platform: "sonic-vs"}
	platform := &spec.PlatformSpec{
		VMImage:       "/platform/image.qcow2",
		VMMemory:      4096,
		VMCPUs:        2,
		VMNICDriver:   "virtio-net-pci",
		VMInterfaceMap: "sequential",
		VMCPUFeatures: "+sse4.2",
		VMBootTimeout: 300,
		VMCredentials: &spec.VMCredentials{User: "admin", Pass: "admin"},
	}

	nc, err := ResolveNodeConfig("spine1", profile, platform)
	if err != nil {
		t.Fatalf("ResolveNodeConfig error: %v", err)
	}

	if nc.Image != "/platform/image.qcow2" {
		t.Errorf("Image = %q, want /platform/image.qcow2", nc.Image)
	}
	if nc.Memory != 4096 {
		t.Errorf("Memory = %d, want 4096", nc.Memory)
	}
	if nc.NICDriver != "virtio-net-pci" {
		t.Errorf("NICDriver = %q, want virtio-net-pci", nc.NICDriver)
	}
	if nc.InterfaceMap != "sequential" {
		t.Errorf("InterfaceMap = %q, want sequential", nc.InterfaceMap)
	}
	if nc.CPUFeatures != "+sse4.2" {
		t.Errorf("CPUFeatures = %q, want +sse4.2", nc.CPUFeatures)
	}
	if nc.BootTimeout != 300 {
		t.Errorf("BootTimeout = %d, want 300", nc.BootTimeout)
	}
	if nc.SSHUser != "admin" {
		t.Errorf("SSHUser = %q, want admin", nc.SSHUser)
	}
}

func TestResolveNodeConfig_BuiltInDefaults(t *testing.T) {
	profile := &spec.DeviceProfile{
		Platform: "sonic-vs",
		VMImage:  "/some/image.qcow2",
	}
	platform := &spec.PlatformSpec{}

	nc, err := ResolveNodeConfig("leaf1", profile, platform)
	if err != nil {
		t.Fatalf("ResolveNodeConfig error: %v", err)
	}

	if nc.Memory != 4096 {
		t.Errorf("Memory = %d, want 4096 (default)", nc.Memory)
	}
	if nc.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2 (default)", nc.CPUs)
	}
	if nc.NICDriver != "e1000" {
		t.Errorf("NICDriver = %q, want e1000 (default)", nc.NICDriver)
	}
	if nc.InterfaceMap != "stride-4" {
		t.Errorf("InterfaceMap = %q, want stride-4 (default)", nc.InterfaceMap)
	}
	if nc.BootTimeout != 180 {
		t.Errorf("BootTimeout = %d, want 180 (default)", nc.BootTimeout)
	}
}

func TestResolveNodeConfig_NoImage(t *testing.T) {
	profile := &spec.DeviceProfile{Platform: "sonic-vs"}
	platform := &spec.PlatformSpec{}

	_, err := ResolveNodeConfig("leaf1", profile, platform)
	if err == nil {
		t.Error("ResolveNodeConfig should error with no vm_image")
	}
}

func TestResolveNodeConfig_MgmtNIC(t *testing.T) {
	profile := &spec.DeviceProfile{
		Platform: "sonic-vs",
		VMImage:  "/img.qcow2",
	}

	nc, err := ResolveNodeConfig("leaf1", profile, &spec.PlatformSpec{})
	if err != nil {
		t.Fatalf("ResolveNodeConfig error: %v", err)
	}

	if len(nc.NICs) != 1 {
		t.Fatalf("NICs len = %d, want 1 (mgmt)", len(nc.NICs))
	}
	if nc.NICs[0].NetdevID != "mgmt" {
		t.Errorf("NICs[0].NetdevID = %q, want mgmt", nc.NICs[0].NetdevID)
	}
}

// ============================================================================
// Link Allocation Tests
// ============================================================================

func TestAllocateLinks(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"spine1": {
			Name:         "spine1",
			InterfaceMap: "stride-4",
			NICs:         []NICConfig{{Index: 0, NetdevID: "mgmt", Interface: "mgmt"}},
		},
		"leaf1": {
			Name:         "leaf1",
			InterfaceMap: "stride-4",
			NICs:         []NICConfig{{Index: 0, NetdevID: "mgmt", Interface: "mgmt"}},
		},
	}

	links := []*spec.TopologyLink{
		{A: "spine1:Ethernet0", Z: "leaf1:Ethernet0"},
		{A: "spine1:Ethernet4", Z: "leaf1:Ethernet4"},
	}

	config := &VMLabConfig{
		LinkPortBase:    20000,
		ConsolePortBase: 30000,
		SSHPortBase:     40000,
	}

	result, err := AllocateLinks(links, nodes, config)
	if err != nil {
		t.Fatalf("AllocateLinks error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("AllocateLinks returned %d links, want 2", len(result))
	}

	// First link: APort = base + 0*2 = 20000, ZPort = 20001
	if result[0].APort != 20000 {
		t.Errorf("link[0].APort = %d, want 20000", result[0].APort)
	}
	if result[0].ZPort != 20001 {
		t.Errorf("link[0].ZPort = %d, want 20001", result[0].ZPort)
	}
	if result[0].A.Device != "spine1" || result[0].A.Interface != "Ethernet0" {
		t.Errorf("link[0].A = %s:%s, want spine1:Ethernet0", result[0].A.Device, result[0].A.Interface)
	}
	if result[0].A.NICIndex != 1 {
		t.Errorf("link[0].A.NICIndex = %d, want 1", result[0].A.NICIndex)
	}

	// Second link: APort = base + 1*2 = 20002, ZPort = 20003
	if result[1].APort != 20002 {
		t.Errorf("link[1].APort = %d, want 20002", result[1].APort)
	}
	if result[1].ZPort != 20003 {
		t.Errorf("link[1].ZPort = %d, want 20003", result[1].ZPort)
	}
	if result[1].Z.NICIndex != 2 {
		t.Errorf("link[1].Z.NICIndex = %d, want 2", result[1].Z.NICIndex)
	}

	// Check NICs were attached to nodes
	// spine1 should have mgmt + 2 data NICs
	if len(nodes["spine1"].NICs) != 3 {
		t.Errorf("spine1 NICs = %d, want 3", len(nodes["spine1"].NICs))
	}
	// Both sides now use ConnectAddr (connect to bridge worker)
	if nodes["spine1"].NICs[1].ConnectAddr != "127.0.0.1:20000" {
		t.Errorf("spine1 NIC[1].ConnectAddr = %q, want 127.0.0.1:20000", nodes["spine1"].NICs[1].ConnectAddr)
	}
	if nodes["leaf1"].NICs[1].ConnectAddr != "127.0.0.1:20001" {
		t.Errorf("leaf1 NIC[1].ConnectAddr = %q, want 127.0.0.1:20001", nodes["leaf1"].NICs[1].ConnectAddr)
	}
}

func TestAllocateLinks_PortSequence(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"a": {Name: "a", InterfaceMap: "sequential", NICs: []NICConfig{{Index: 0, NetdevID: "mgmt"}}},
		"b": {Name: "b", InterfaceMap: "sequential", NICs: []NICConfig{{Index: 0, NetdevID: "mgmt"}}},
	}

	links := []*spec.TopologyLink{
		{A: "a:Ethernet0", Z: "b:Ethernet0"},
		{A: "a:Ethernet1", Z: "b:Ethernet1"},
		{A: "a:Ethernet2", Z: "b:Ethernet2"},
	}

	config := &VMLabConfig{LinkPortBase: 25000}

	result, err := AllocateLinks(links, nodes, config)
	if err != nil {
		t.Fatalf("AllocateLinks error: %v", err)
	}

	for i, lc := range result {
		wantA := 25000 + (i * 2)
		wantZ := 25000 + (i * 2) + 1
		if lc.APort != wantA {
			t.Errorf("link[%d].APort = %d, want %d", i, lc.APort, wantA)
		}
		if lc.ZPort != wantZ {
			t.Errorf("link[%d].ZPort = %d, want %d", i, lc.ZPort, wantZ)
		}
	}
}

// ============================================================================
// State Tests
// ============================================================================

func TestSaveAndLoadState(t *testing.T) {
	// Use a temp dir to avoid polluting ~/.newtlab
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	state := &LabState{
		Name:    "test-lab",
		SpecDir: "/tmp/specs",
		Nodes: map[string]*NodeState{
			"spine1": {PID: 1234, Status: "running", SSHPort: 40000, ConsolePort: 30000},
		},
		Links: []*LinkState{
			{A: "spine1:Ethernet0", Z: "leaf1:Ethernet0", APort: 20000, ZPort: 20001, WorkerHost: "host-a"},
		},
		Bridges: map[string]*BridgeState{
			"":       {PID: 5678, StatsAddr: "127.0.0.1:19999"},
			"host-a": {PID: 9012, HostIP: "10.0.0.2", StatsAddr: "10.0.0.2:19998"},
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}

	// Verify file exists
	stateFile := filepath.Join(tmpDir, ".newtlab", "labs", "test-lab", "state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state.json not found: %v", err)
	}

	loaded, err := LoadState("test-lab")
	if err != nil {
		t.Fatalf("LoadState error: %v", err)
	}

	if loaded.Name != "test-lab" {
		t.Errorf("Name = %q, want test-lab", loaded.Name)
	}
	if loaded.SpecDir != "/tmp/specs" {
		t.Errorf("SpecDir = %q, want /tmp/specs", loaded.SpecDir)
	}
	if loaded.Nodes["spine1"].PID != 1234 {
		t.Errorf("spine1 PID = %d, want 1234", loaded.Nodes["spine1"].PID)
	}
	if len(loaded.Links) != 1 || loaded.Links[0].APort != 20000 || loaded.Links[0].ZPort != 20001 {
		t.Error("links not preserved correctly")
	}
	if loaded.Links[0].WorkerHost != "host-a" {
		t.Errorf("WorkerHost = %q, want host-a", loaded.Links[0].WorkerHost)
	}
	if len(loaded.Bridges) != 2 {
		t.Fatalf("Bridges len = %d, want 2", len(loaded.Bridges))
	}
	if loaded.Bridges[""].PID != 5678 {
		t.Errorf("Bridges[\"\"].PID = %d, want 5678", loaded.Bridges[""].PID)
	}
	if loaded.Bridges[""].StatsAddr != "127.0.0.1:19999" {
		t.Errorf("Bridges[\"\"].StatsAddr = %q, want 127.0.0.1:19999", loaded.Bridges[""].StatsAddr)
	}
	if loaded.Bridges["host-a"].HostIP != "10.0.0.2" {
		t.Errorf("Bridges[\"host-a\"].HostIP = %q, want 10.0.0.2", loaded.Bridges["host-a"].HostIP)
	}
	if loaded.Bridges["host-a"].StatsAddr != "10.0.0.2:19998" {
		t.Errorf("Bridges[\"host-a\"].StatsAddr = %q, want 10.0.0.2:19998", loaded.Bridges["host-a"].StatsAddr)
	}
}

func TestLoadState_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	_, err := LoadState("nonexistent")
	if err == nil {
		t.Error("LoadState should error for nonexistent lab")
	}
}

func TestListLabs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	// No labs yet
	labs, err := ListLabs()
	if err != nil {
		t.Fatalf("ListLabs error: %v", err)
	}
	if len(labs) != 0 {
		t.Errorf("ListLabs = %v, want empty", labs)
	}

	// Create some lab dirs
	os.MkdirAll(filepath.Join(tmpDir, ".newtlab", "labs", "lab-a"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, ".newtlab", "labs", "lab-b"), 0755)

	labs, err = ListLabs()
	if err != nil {
		t.Fatalf("ListLabs error: %v", err)
	}
	if len(labs) != 2 {
		t.Errorf("ListLabs returned %d labs, want 2", len(labs))
	}
}

func TestRemoveState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	labDir := filepath.Join(tmpDir, ".newtlab", "labs", "rm-test")
	os.MkdirAll(labDir, 0755)
	os.WriteFile(filepath.Join(labDir, "state.json"), []byte("{}"), 0644)

	if err := RemoveState("rm-test"); err != nil {
		t.Fatalf("RemoveState error: %v", err)
	}

	if _, err := os.Stat(labDir); !os.IsNotExist(err) {
		t.Error("lab directory should be removed")
	}
}

// ============================================================================
// FilterHost Tests
// ============================================================================

func TestFilterHost_ClearsLocalHost(t *testing.T) {
	lab := &Lab{
		Nodes: map[string]*NodeConfig{
			"spine1": {Name: "spine1", Host: "server-a"},
			"spine2": {Name: "spine2", Host: "server-a"},
			"leaf1":  {Name: "leaf1", Host: "server-b"},
			"leaf2":  {Name: "leaf2", Host: "server-b"},
		},
		Links: []*LinkConfig{
			{A: LinkEndpoint{Device: "spine1"}, Z: LinkEndpoint{Device: "leaf1"}},
			{A: LinkEndpoint{Device: "spine1"}, Z: LinkEndpoint{Device: "spine2"}},
		},
	}

	lab.FilterHost("server-a")

	// server-a nodes kept, server-b nodes removed
	if _, ok := lab.Nodes["spine1"]; !ok {
		t.Error("spine1 should be kept")
	}
	if _, ok := lab.Nodes["spine2"]; !ok {
		t.Error("spine2 should be kept")
	}
	if _, ok := lab.Nodes["leaf1"]; ok {
		t.Error("leaf1 should be removed")
	}

	// Nodes on the current host should have Host cleared (local)
	if lab.Nodes["spine1"].Host != "" {
		t.Errorf("spine1.Host = %q, want empty (local)", lab.Nodes["spine1"].Host)
	}
	if lab.Nodes["spine2"].Host != "" {
		t.Errorf("spine2.Host = %q, want empty (local)", lab.Nodes["spine2"].Host)
	}

	// Only spine1↔spine2 link kept (both nodes exist), spine1↔leaf1 removed
	if len(lab.Links) != 1 {
		t.Fatalf("Links len = %d, want 1", len(lab.Links))
	}
}

func TestFilterHost_KeepsUnhostNodes(t *testing.T) {
	lab := &Lab{
		Nodes: map[string]*NodeConfig{
			"spine1": {Name: "spine1", Host: ""},        // no host (single-host mode)
			"leaf1":  {Name: "leaf1", Host: "server-a"},
		},
		Links: nil,
	}

	lab.FilterHost("server-a")

	// Unhosted node kept
	if _, ok := lab.Nodes["spine1"]; !ok {
		t.Error("spine1 (no host) should be kept")
	}
	// server-a node kept and made local
	if lab.Nodes["leaf1"].Host != "" {
		t.Errorf("leaf1.Host = %q, want empty (local)", lab.Nodes["leaf1"].Host)
	}
}

// ============================================================================
// PlaceWorkers Tests
// ============================================================================

func TestPlaceWorkers_SameHost(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"a": {Name: "a", Host: ""},
		"b": {Name: "b", Host: ""},
	}
	links := []*LinkConfig{
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "b"}},
	}

	PlaceWorkers(links, nodes)

	if links[0].WorkerHost != "" {
		t.Errorf("WorkerHost = %q, want empty (local)", links[0].WorkerHost)
	}
}

func TestPlaceWorkers_CrossHost_Balanced(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"a": {Name: "a", Host: "host-a"},
		"b": {Name: "b", Host: "host-b"},
	}
	links := []*LinkConfig{
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "b"}},
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "b"}},
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "b"}},
	}

	PlaceWorkers(links, nodes)

	// First link: tie → alphabetical → host-a
	if links[0].WorkerHost != "host-a" {
		t.Errorf("link[0].WorkerHost = %q, want host-a", links[0].WorkerHost)
	}
	// Second link: host-a has 1, host-b has 0 → host-b
	if links[1].WorkerHost != "host-b" {
		t.Errorf("link[1].WorkerHost = %q, want host-b", links[1].WorkerHost)
	}
	// Third link: tie again (1 each) → alphabetical → host-a
	if links[2].WorkerHost != "host-a" {
		t.Errorf("link[2].WorkerHost = %q, want host-a", links[2].WorkerHost)
	}
}

func TestPlaceWorkers_MixedLocalAndCrossHost(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"a": {Name: "a", Host: "host-a"},
		"b": {Name: "b", Host: "host-a"},
		"c": {Name: "c", Host: "host-b"},
	}
	links := []*LinkConfig{
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "b"}}, // same host
		{A: LinkEndpoint{Device: "a"}, Z: LinkEndpoint{Device: "c"}}, // cross host
	}

	PlaceWorkers(links, nodes)

	// Same-host link → stays on host-a
	if links[0].WorkerHost != "host-a" {
		t.Errorf("link[0].WorkerHost = %q, want host-a (same host)", links[0].WorkerHost)
	}
	// Cross-host: tie → alphabetical → host-a
	if links[1].WorkerHost != "host-a" {
		t.Errorf("link[1].WorkerHost = %q, want host-a", links[1].WorkerHost)
	}
}

// ============================================================================
// BridgeWorker Tests
// ============================================================================

func TestBridgeWorker_BidirectionalData(t *testing.T) {
	links := []*LinkConfig{
		{APort: 0, ZPort: 0, ABind: "127.0.0.1", ZBind: "127.0.0.1"},
	}

	// Use port 0 to let OS assign free ports.
	bridge, err := StartBridgeWorkers(links)
	if err != nil {
		t.Fatalf("StartBridgeWorkers error: %v", err)
	}
	defer bridge.Stop()

	// Retrieve the actual ports from the listeners via the LinkConfig.
	// Since we used port 0, we need to get the ports differently.
	// StartBridgeWorkers opens listeners but doesn't expose them.
	// For testing, we re-start with specific free ports.
	bridge.Stop() // stop the port-0 workers

	// Find two free port pairs
	aPort := getFreePort(t)
	zPort := getFreePort(t)

	links = []*LinkConfig{
		{APort: aPort, ZPort: zPort, ABind: "127.0.0.1", ZBind: "127.0.0.1"},
	}

	bridge, err = StartBridgeWorkers(links)
	if err != nil {
		t.Fatalf("StartBridgeWorkers error: %v", err)
	}
	defer bridge.Stop()

	// Connect A side
	aConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", aPort))
	if err != nil {
		t.Fatalf("dial A side: %v", err)
	}
	defer aConn.Close()

	// Connect Z side
	zConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", zPort))
	if err != nil {
		t.Fatalf("dial Z side: %v", err)
	}
	defer zConn.Close()

	// A → Z
	msg := []byte("hello from A")
	if _, err := aConn.Write(msg); err != nil {
		t.Fatalf("write to A: %v", err)
	}
	buf := make([]byte, 64)
	n, err := zConn.Read(buf)
	if err != nil {
		t.Fatalf("read from Z: %v", err)
	}
	if string(buf[:n]) != "hello from A" {
		t.Errorf("Z received %q, want %q", buf[:n], "hello from A")
	}

	// Z → A
	msg = []byte("hello from Z")
	if _, err := zConn.Write(msg); err != nil {
		t.Fatalf("write to Z: %v", err)
	}
	n, err = aConn.Read(buf)
	if err != nil {
		t.Fatalf("read from A: %v", err)
	}
	if string(buf[:n]) != "hello from Z" {
		t.Errorf("A received %q, want %q", buf[:n], "hello from Z")
	}
}

func TestStartBridgeWorkers_ListenFailure(t *testing.T) {
	// Occupy a port so StartBridgeWorkers fails
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	links := []*LinkConfig{
		{APort: port, ZPort: 0, ABind: "127.0.0.1", ZBind: "127.0.0.1"},
	}

	_, err = StartBridgeWorkers(links)
	if err == nil {
		t.Fatal("StartBridgeWorkers should fail when port is occupied")
	}
}

// getFreePort asks the OS for a free TCP port.
func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// ============================================================================
// CountingWriter Tests
// ============================================================================

func TestCountingWriter(t *testing.T) {
	var count atomic.Int64
	var buf bytes.Buffer
	cw := &countingWriter{w: &buf, count: &count}

	data := []byte("hello world") // 11 bytes
	n, err := cw.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 11 {
		t.Errorf("Write returned %d, want 11", n)
	}
	if count.Load() != 11 {
		t.Errorf("count = %d, want 11", count.Load())
	}
	if buf.String() != "hello world" {
		t.Errorf("buf = %q, want %q", buf.String(), "hello world")
	}

	// Write more data
	cw.Write([]byte("!"))
	if count.Load() != 12 {
		t.Errorf("count after second write = %d, want 12", count.Load())
	}
}

// ============================================================================
// Bridge Stats Tests
// ============================================================================

func TestBridgeStats(t *testing.T) {
	aPort := getFreePort(t)
	zPort := getFreePort(t)

	links := []*LinkConfig{
		{
			A:     LinkEndpoint{Device: "spine1", Interface: "Ethernet0"},
			Z:     LinkEndpoint{Device: "leaf1", Interface: "Ethernet0"},
			APort: aPort,
			ZPort: zPort,
			ABind: "127.0.0.1",
			ZBind: "127.0.0.1",
		},
	}

	bridge, err := StartBridgeWorkers(links)
	if err != nil {
		t.Fatalf("StartBridgeWorkers error: %v", err)
	}
	defer bridge.Stop()

	// Before any connections, stats should show zero
	stats := bridge.Stats()
	if len(stats.Links) != 1 {
		t.Fatalf("stats.Links len = %d, want 1", len(stats.Links))
	}
	if stats.Links[0].Sessions != 0 {
		t.Errorf("sessions before connect = %d, want 0", stats.Links[0].Sessions)
	}
	if stats.Links[0].Connected {
		t.Error("connected before connect = true, want false")
	}
	if stats.Links[0].A != "spine1:Ethernet0" {
		t.Errorf("A = %q, want spine1:Ethernet0", stats.Links[0].A)
	}
	if stats.Links[0].Z != "leaf1:Ethernet0" {
		t.Errorf("Z = %q, want leaf1:Ethernet0", stats.Links[0].Z)
	}

	// Connect both sides
	aConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", aPort))
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer aConn.Close()

	zConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", zPort))
	if err != nil {
		t.Fatalf("dial Z: %v", err)
	}
	defer zConn.Close()

	// Send data A→Z and wait for it to arrive
	aData := []byte("AAAA") // 4 bytes
	aConn.Write(aData)
	buf := make([]byte, 64)
	zConn.Read(buf)

	// Send data Z→A
	zData := []byte("ZZZZZZZZ") // 8 bytes
	zConn.Write(zData)
	aConn.Read(buf)

	stats = bridge.Stats()
	if stats.Links[0].AToZBytes != 4 {
		t.Errorf("AToZBytes = %d, want 4", stats.Links[0].AToZBytes)
	}
	if stats.Links[0].ZToABytes != 8 {
		t.Errorf("ZToABytes = %d, want 8", stats.Links[0].ZToABytes)
	}
	if stats.Links[0].Sessions != 1 {
		t.Errorf("sessions = %d, want 1", stats.Links[0].Sessions)
	}
	if !stats.Links[0].Connected {
		t.Error("connected = false, want true")
	}
}

// ============================================================================
// TCP Stats + QueryAllBridgeStats Tests
// ============================================================================

func TestQueryBridgeStats_TCP(t *testing.T) {
	aPort := getFreePort(t)
	zPort := getFreePort(t)

	links := []*LinkConfig{
		{
			A:     LinkEndpoint{Device: "spine1", Interface: "Ethernet0"},
			Z:     LinkEndpoint{Device: "leaf1", Interface: "Ethernet0"},
			APort: aPort,
			ZPort: zPort,
			ABind: "127.0.0.1",
			ZBind: "127.0.0.1",
		},
	}

	bridge, err := StartBridgeWorkers(links)
	if err != nil {
		t.Fatalf("StartBridgeWorkers error: %v", err)
	}
	defer bridge.Stop()

	// Start a TCP stats listener
	tcpPort := getFreePort(t)
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", tcpPort)
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		t.Fatalf("listen TCP stats: %v", err)
	}
	defer tcpLn.Close()

	go func() {
		for {
			conn, err := tcpLn.Accept()
			if err != nil {
				return
			}
			json.NewEncoder(conn).Encode(bridge.Stats())
			conn.Close()
		}
	}()

	stats, err := QueryBridgeStats(tcpAddr)
	if err != nil {
		t.Fatalf("QueryBridgeStats(TCP) error: %v", err)
	}
	if len(stats.Links) != 1 {
		t.Fatalf("stats.Links len = %d, want 1", len(stats.Links))
	}
	if stats.Links[0].A != "spine1:Ethernet0" {
		t.Errorf("A = %q, want spine1:Ethernet0", stats.Links[0].A)
	}
}

func TestQueryAllBridgeStats_MultiBridge(t *testing.T) {
	// Simulate two separate bridge instances (e.g., two hosts) each serving
	// different links, and verify QueryAllBridgeStats merges them.

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	// Bridge 1: one link
	aPort1 := getFreePort(t)
	zPort1 := getFreePort(t)
	bridge1, err := StartBridgeWorkers([]*LinkConfig{
		{
			A: LinkEndpoint{Device: "spine1", Interface: "Ethernet0"},
			Z: LinkEndpoint{Device: "leaf1", Interface: "Ethernet0"},
			APort: aPort1, ZPort: zPort1,
			ABind: "127.0.0.1", ZBind: "127.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("StartBridgeWorkers(1) error: %v", err)
	}
	defer bridge1.Stop()

	// Bridge 2: another link
	aPort2 := getFreePort(t)
	zPort2 := getFreePort(t)
	bridge2, err := StartBridgeWorkers([]*LinkConfig{
		{
			A: LinkEndpoint{Device: "spine1", Interface: "Ethernet4"},
			Z: LinkEndpoint{Device: "leaf2", Interface: "Ethernet0"},
			APort: aPort2, ZPort: zPort2,
			ABind: "127.0.0.1", ZBind: "127.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("StartBridgeWorkers(2) error: %v", err)
	}
	defer bridge2.Stop()

	// TCP stats listeners for each bridge
	tcpPort1 := getFreePort(t)
	tcpAddr1 := fmt.Sprintf("127.0.0.1:%d", tcpPort1)
	tcpLn1, err := net.Listen("tcp", tcpAddr1)
	if err != nil {
		t.Fatalf("listen TCP(1): %v", err)
	}
	defer tcpLn1.Close()
	go func() {
		for {
			conn, err := tcpLn1.Accept()
			if err != nil {
				return
			}
			json.NewEncoder(conn).Encode(bridge1.Stats())
			conn.Close()
		}
	}()

	tcpPort2 := getFreePort(t)
	tcpAddr2 := fmt.Sprintf("127.0.0.1:%d", tcpPort2)
	tcpLn2, err := net.Listen("tcp", tcpAddr2)
	if err != nil {
		t.Fatalf("listen TCP(2): %v", err)
	}
	defer tcpLn2.Close()
	go func() {
		for {
			conn, err := tcpLn2.Accept()
			if err != nil {
				return
			}
			json.NewEncoder(conn).Encode(bridge2.Stats())
			conn.Close()
		}
	}()

	// Create state.json with two bridge entries
	labName := "multi-bridge-test"
	state := &LabState{
		Name:    labName,
		SpecDir: "/tmp/specs",
		Nodes:   map[string]*NodeState{},
		Bridges: map[string]*BridgeState{
			"":       {PID: 1, StatsAddr: tcpAddr1},
			"host-b": {PID: 2, HostIP: "10.0.0.2", StatsAddr: tcpAddr2},
		},
	}
	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}

	// QueryAllBridgeStats should merge results from both
	merged, err := QueryAllBridgeStats(labName)
	if err != nil {
		t.Fatalf("QueryAllBridgeStats error: %v", err)
	}
	if len(merged.Links) != 2 {
		t.Fatalf("merged.Links len = %d, want 2", len(merged.Links))
	}

	// Verify both links are present (order may vary since map iteration is random)
	found := map[string]bool{}
	for _, ls := range merged.Links {
		found[ls.A] = true
	}
	if !found["spine1:Ethernet0"] {
		t.Error("missing spine1:Ethernet0 in merged stats")
	}
	if !found["spine1:Ethernet4"] {
		t.Error("missing spine1:Ethernet4 in merged stats")
	}
}

func TestQueryAllBridgeStats_LegacyFallback(t *testing.T) {
	// Test that QueryAllBridgeStats falls back to Unix socket for legacy
	// state files without Bridges field.

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetHomeDir()
	t.Cleanup(resetHomeDir)

	// Start a bridge and create a Unix socket
	aPort := getFreePort(t)
	zPort := getFreePort(t)
	bridge, err := StartBridgeWorkers([]*LinkConfig{
		{
			A: LinkEndpoint{Device: "spine1", Interface: "Ethernet0"},
			Z: LinkEndpoint{Device: "leaf1", Interface: "Ethernet0"},
			APort: aPort, ZPort: zPort,
			ABind: "127.0.0.1", ZBind: "127.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("StartBridgeWorkers error: %v", err)
	}
	defer bridge.Stop()

	// Create Unix socket in state dir
	labName := "legacy-test"
	stateDir := LabDir(labName)
	os.MkdirAll(stateDir, 0755)

	sockPath := filepath.Join(stateDir, "bridge.sock")
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer unixLn.Close()
	defer os.Remove(sockPath)

	go func() {
		for {
			conn, err := unixLn.Accept()
			if err != nil {
				return
			}
			json.NewEncoder(conn).Encode(bridge.Stats())
			conn.Close()
		}
	}()

	// Legacy state: no Bridges field, only BridgePID
	state := &LabState{
		Name:      labName,
		SpecDir:   "/tmp/specs",
		Nodes:     map[string]*NodeState{},
		BridgePID: 12345,
	}
	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}

	stats, err := QueryAllBridgeStats(labName)
	if err != nil {
		t.Fatalf("QueryAllBridgeStats error: %v", err)
	}
	if len(stats.Links) != 1 {
		t.Fatalf("stats.Links len = %d, want 1", len(stats.Links))
	}
	if stats.Links[0].A != "spine1:Ethernet0" {
		t.Errorf("A = %q, want spine1:Ethernet0", stats.Links[0].A)
	}
}

// ============================================================================
// Disk helpers
// ============================================================================

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input, want string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~notahome", "~notahome"}, // only ~/... triggers expansion
	}

	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUnexpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input, want string
	}{
		{home + "/foo/bar", "~/foo/bar"},
		{"/other/path", "/other/path"},
		{home, home}, // no trailing slash — not under home
	}

	for _, tt := range tests {
		got := unexpandHome(tt.input)
		if got != tt.want {
			t.Errorf("unexpandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================================
// QEMUCommand KVM
// ============================================================================

func TestQEMUCommand_KVM(t *testing.T) {
	node := &NodeConfig{
		Name:      "test",
		Memory:    4096,
		CPUs:      2,
		NICDriver: "e1000",
		NICs:      []NICConfig{{Index: 0, NetdevID: "mgmt"}},
	}

	// With KVM enabled
	qemu := &QEMUCommand{Node: node, StateDir: "/tmp/test", KVM: true}
	cmd := qemu.Build()
	args := fmt.Sprintf("%v", cmd.Args)
	if !containsStr(args, "-enable-kvm") {
		t.Error("KVM=true should include -enable-kvm")
	}

	// Without KVM
	qemu = &QEMUCommand{Node: node, StateDir: "/tmp/test", KVM: false}
	cmd = qemu.Build()
	args = fmt.Sprintf("%v", cmd.Args)
	if containsStr(args, "-enable-kvm") {
		t.Error("KVM=false should not include -enable-kvm")
	}
}

func TestQEMUCommand_RelativePaths(t *testing.T) {
	node := &NodeConfig{
		Name:        "spine1",
		Memory:      4096,
		CPUs:        2,
		NICDriver:   "virtio-net-pci",
		SSHPort:     40000,
		ConsolePort: 30000,
		NICs:        []NICConfig{{Index: 0, NetdevID: "mgmt"}},
	}

	// Relative state dir (used for remote)
	qemu := &QEMUCommand{Node: node, StateDir: ".", KVM: true}
	cmd := qemu.Build()
	args := fmt.Sprintf("%v", cmd.Args)

	// Paths should be relative (no leading /)
	if !containsStr(args, "disks/spine1.qcow2") {
		t.Errorf("expected relative overlay path, got args: %s", args)
	}
	if !containsStr(args, "qemu/spine1.mon") {
		t.Errorf("expected relative monitor path, got args: %s", args)
	}
}

