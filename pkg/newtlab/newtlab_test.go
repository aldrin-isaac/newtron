package newtlab

import (
	"os"
	"path/filepath"
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

	// First link
	if result[0].Port != 20000 {
		t.Errorf("link[0].Port = %d, want 20000", result[0].Port)
	}
	if result[0].A.Device != "spine1" || result[0].A.Interface != "Ethernet0" {
		t.Errorf("link[0].A = %s:%s, want spine1:Ethernet0", result[0].A.Device, result[0].A.Interface)
	}
	if result[0].A.NICIndex != 1 {
		t.Errorf("link[0].A.NICIndex = %d, want 1", result[0].A.NICIndex)
	}

	// Second link
	if result[1].Port != 20001 {
		t.Errorf("link[1].Port = %d, want 20001", result[1].Port)
	}
	if result[1].Z.NICIndex != 2 {
		t.Errorf("link[1].Z.NICIndex = %d, want 2", result[1].Z.NICIndex)
	}

	// Check NICs were attached to nodes
	// spine1 should have mgmt + 2 data NICs
	if len(nodes["spine1"].NICs) != 3 {
		t.Errorf("spine1 NICs = %d, want 3", len(nodes["spine1"].NICs))
	}
	// spine1 NIC[1] should be listen side
	if !nodes["spine1"].NICs[1].Listen {
		t.Error("spine1 NIC[1].Listen should be true (A side listens)")
	}
	// leaf1 NIC[1] should be connect side
	if nodes["leaf1"].NICs[1].Listen {
		t.Error("leaf1 NIC[1].Listen should be false (Z side connects)")
	}
	if nodes["leaf1"].NICs[1].RemoteIP != "127.0.0.1" {
		t.Errorf("leaf1 NIC[1].RemoteIP = %q, want 127.0.0.1", nodes["leaf1"].NICs[1].RemoteIP)
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
		want := 25000 + i
		if lc.Port != want {
			t.Errorf("link[%d].Port = %d, want %d", i, lc.Port, want)
		}
	}
}

// ============================================================================
// State Tests
// ============================================================================

func TestSaveAndLoadState(t *testing.T) {
	// Use a temp dir to avoid polluting ~/.newtlab
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	state := &LabState{
		Name:    "test-lab",
		SpecDir: "/tmp/specs",
		Nodes: map[string]*NodeState{
			"spine1": {PID: 1234, Status: "running", SSHPort: 40000, ConsolePort: 30000},
		},
		Links: []*LinkState{
			{A: "spine1:Ethernet0", Z: "leaf1:Ethernet0", Port: 20000},
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
	if len(loaded.Links) != 1 || loaded.Links[0].Port != 20000 {
		t.Error("links not preserved correctly")
	}
}

func TestLoadState_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadState("nonexistent")
	if err == nil {
		t.Error("LoadState should error for nonexistent lab")
	}
}

func TestListLabs(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

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
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

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
