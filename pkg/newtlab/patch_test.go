package newtlab

import (
	"strings"
	"testing"

	"github.com/newtron-network/newtron/pkg/spec"
)

func TestQEMUPCIAddrs(t *testing.T) {
	tests := []struct {
		dataNICs int
		want     []string
	}{
		{0, []string{}},
		{1, []string{"0000:00:04.0"}},
		{2, []string{"0000:00:04.0", "0000:00:05.0"}},
		{4, []string{"0000:00:04.0", "0000:00:05.0", "0000:00:06.0", "0000:00:07.0"}},
	}

	for _, tt := range tests {
		got := QEMUPCIAddrs(tt.dataNICs)
		if len(got) != len(tt.want) {
			t.Errorf("QEMUPCIAddrs(%d) = %v, want %v", tt.dataNICs, got, tt.want)
			continue
		}
		for i, addr := range got {
			if addr != tt.want[i] {
				t.Errorf("QEMUPCIAddrs(%d)[%d] = %q, want %q", tt.dataNICs, i, addr, tt.want[i])
			}
		}
	}
}

func TestResolveBootPatches_VPP(t *testing.T) {
	patches, err := ResolveBootPatches("vpp", "")
	if err != nil {
		t.Fatalf("ResolveBootPatches(vpp, '') error: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("ResolveBootPatches(vpp, '') returned %d patches, want 2", len(patches))
	}

	// Verify ordering: 01-disable-factory-hook before 02-port-config
	if !strings.Contains(patches[0].Description, "factory hook") {
		t.Errorf("patch[0] description = %q, want to contain 'factory hook'", patches[0].Description)
	}
	if !strings.Contains(patches[1].Description, "port_config") {
		t.Errorf("patch[1] description = %q, want to contain 'port_config'", patches[1].Description)
	}

	// Verify first patch has disable_files
	if len(patches[0].DisableFiles) != 1 {
		t.Errorf("patch[0] disable_files = %v, want 1 entry", patches[0].DisableFiles)
	}

	// Verify second patch has files and redis
	if len(patches[1].Files) != 3 {
		t.Errorf("patch[1] files = %d, want 3", len(patches[1].Files))
	}
	if len(patches[1].Redis) != 1 {
		t.Errorf("patch[1] redis = %d, want 1", len(patches[1].Redis))
	}
}

func TestResolveBootPatches_NoDataplane(t *testing.T) {
	// Empty dataplane returns nil
	patches, err := ResolveBootPatches("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patches != nil {
		t.Errorf("expected nil, got %d patches", len(patches))
	}

	// Unknown dataplane returns nil (no error)
	patches, err = ResolveBootPatches("nonexistent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("expected 0 patches for unknown dataplane, got %d", len(patches))
	}
}

func TestResolveBootPatches_WithRelease(t *testing.T) {
	// Non-existent release dir just returns always patches
	patches, err := ResolveBootPatches("vpp", "999999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 2 {
		t.Errorf("expected 2 patches (always only), got %d", len(patches))
	}
}

func TestBuildPatchVars(t *testing.T) {
	node := &NodeConfig{
		Name:     "leaf1",
		Platform: "sonic-vpp",
		NICs: []NICConfig{
			{Index: 0, NetdevID: "mgmt"},
			{Index: 1, NetdevID: "eth1"},
			{Index: 2, NetdevID: "eth2"},
		},
	}
	platform := &spec.PlatformSpec{
		HWSKU:          "Force10-S6000",
		DefaultSpeed:   "25000",
		Dataplane:      "vpp",
		VMImageRelease: "202405",
	}

	vars := buildPatchVars(node, platform)

	if vars.NumPorts != 2 {
		t.Errorf("NumPorts = %d, want 2", vars.NumPorts)
	}
	if len(vars.PCIAddrs) != 2 {
		t.Errorf("PCIAddrs len = %d, want 2", len(vars.PCIAddrs))
	}
	if vars.PCIAddrs[0] != "0000:00:04.0" {
		t.Errorf("PCIAddrs[0] = %q, want '0000:00:04.0'", vars.PCIAddrs[0])
	}
	if vars.HWSkuDir != "/usr/share/sonic/device/x86_64-kvm_x86_64-r0/Force10-S6000" {
		t.Errorf("HWSkuDir = %q", vars.HWSkuDir)
	}
	if vars.PortSpeed != 25000 {
		t.Errorf("PortSpeed = %d, want 25000", vars.PortSpeed)
	}
	if vars.Dataplane != "vpp" {
		t.Errorf("Dataplane = %q, want 'vpp'", vars.Dataplane)
	}
	if vars.Release != "202405" {
		t.Errorf("Release = %q, want '202405'", vars.Release)
	}
}

func TestRenderTemplate_PortConfig(t *testing.T) {
	vars := &PatchVars{
		NumPorts:  2,
		PCIAddrs:  []string{"0000:00:03.0", "0000:00:04.0"},
		HWSkuDir:  "/usr/share/sonic/device/x86_64-kvm_x86_64-r0/Force10-S6000",
		PortSpeed: 25000,
	}

	content, err := renderTemplate("port_config.ini.tmpl", "patches/vpp/always", vars)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	// Should contain header
	if !strings.Contains(content, "# name  lanes  alias  index  speed") {
		t.Error("missing header line")
	}

	// Should contain port entries
	if !strings.Contains(content, "Ethernet0  0  Ethernet0  0  25000") {
		t.Errorf("missing Ethernet0 entry, got:\n%s", content)
	}
	if !strings.Contains(content, "Ethernet4  4  Ethernet4  1  25000") {
		t.Errorf("missing Ethernet4 entry, got:\n%s", content)
	}
}

func TestRenderTemplate_SyncdVPPEnv(t *testing.T) {
	vars := &PatchVars{
		NumPorts: 2,
		PCIAddrs: []string{"0000:00:03.0", "0000:00:04.0"},
	}

	content, err := renderTemplate("syncd_vpp_env.tmpl", "patches/vpp/always", vars)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	if !strings.Contains(content, "DPDK_DISABLE=n") {
		t.Error("missing DPDK_DISABLE")
	}
	if !strings.Contains(content, "VPP_DPDK_PORTS=0000:00:03.0,0000:00:04.0") {
		t.Errorf("wrong VPP_DPDK_PORTS, got:\n%s", content)
	}
	if !strings.Contains(content, "SONIC_NUM_PORTS=2") {
		t.Error("missing SONIC_NUM_PORTS")
	}
	if !strings.Contains(content, "VPP_PORT_LIST=eth1,eth2") {
		t.Errorf("wrong VPP_PORT_LIST, got:\n%s", content)
	}
	if !strings.Contains(content, "NO_LINUX_NL=y") {
		t.Error("missing NO_LINUX_NL")
	}
}

func TestRenderTemplate_IFMap(t *testing.T) {
	vars := &PatchVars{
		NumPorts: 3,
		PCIAddrs: []string{"0000:00:03.0", "0000:00:04.0", "0000:00:05.0"},
	}

	content, err := renderTemplate("sonic_vpp_ifmap.ini.tmpl", "patches/vpp/always", vars)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	if !strings.Contains(content, "Ethernet0 bobm0") {
		t.Errorf("missing Ethernet0 mapping, got:\n%s", content)
	}
	if !strings.Contains(content, "Ethernet4 bobm1") {
		t.Errorf("missing Ethernet4 mapping, got:\n%s", content)
	}
	if !strings.Contains(content, "Ethernet8 bobm2") {
		t.Errorf("missing Ethernet8 mapping, got:\n%s", content)
	}
}

func TestRenderTemplate_PortEntries(t *testing.T) {
	vars := &PatchVars{
		NumPorts:  2,
		PCIAddrs:  []string{"0000:00:03.0", "0000:00:04.0"},
		PortSpeed: 25000,
	}

	content, err := renderTemplate("port_entries.tmpl", "patches/vpp/always", vars)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	if !strings.Contains(content, `HSET "PORT|Ethernet0" admin_status up alias Ethernet0 index 0 lanes 0 speed 25000`) {
		t.Errorf("missing Ethernet0 redis entry, got:\n%s", content)
	}
	if !strings.Contains(content, `HSET "PORT|Ethernet4" admin_status up alias Ethernet4 index 1 lanes 4 speed 25000`) {
		t.Errorf("missing Ethernet4 redis entry, got:\n%s", content)
	}
}

func TestRenderString_WithTemplate(t *testing.T) {
	vars := &PatchVars{
		HWSkuDir: "/usr/share/sonic/device/x86_64-kvm_x86_64-r0/Force10-S6000",
	}

	got, err := renderString("{{.HWSkuDir}}/port_config.ini", vars)
	if err != nil {
		t.Fatalf("renderString error: %v", err)
	}
	want := "/usr/share/sonic/device/x86_64-kvm_x86_64-r0/Force10-S6000/port_config.ini"
	if got != want {
		t.Errorf("renderString = %q, want %q", got, want)
	}
}

func TestRenderString_Plain(t *testing.T) {
	vars := &PatchVars{}

	got, err := renderString("/etc/sonic/vpp/syncd_vpp_env", vars)
	if err != nil {
		t.Fatalf("renderString error: %v", err)
	}
	if got != "/etc/sonic/vpp/syncd_vpp_env" {
		t.Errorf("renderString = %q, want plain path", got)
	}
}
