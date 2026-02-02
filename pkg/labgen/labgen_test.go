package labgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testTopology returns a minimal valid topology for testing.
func testTopology() *Topology {
	return &Topology{
		Name: "test-topo",
		Defaults: TopologyDefaults{
			Image:    "vrnetlab/vr-sonic:202411",
			Username: "cisco",
			Password: "cisco123",
			Platform: "vs-platform",
			Site:     "lab-site",
			HWSKU:    "cisco-8101-p4-32x100-vs",
		},
		Network: TopologyNetwork{
			ASNumber: 65000,
			Region:   "lab-region",
		},
		Nodes: map[string]NodeDef{
			"spine1":  {Role: "spine", LoopbackIP: "10.0.0.1"},
			"spine2":  {Role: "spine", LoopbackIP: "10.0.0.2"},
			"leaf1":   {Role: "leaf", LoopbackIP: "10.0.0.11"},
			"leaf2":   {Role: "leaf", LoopbackIP: "10.0.0.12"},
			"server1": {Role: "server", Image: "nicolaka/netshoot:latest"},
			"server2": {Role: "server", Image: "nicolaka/netshoot:latest"},
		},
		Links: []LinkDef{
			{Endpoints: []string{"spine1:Ethernet0", "leaf1:Ethernet0"}},
			{Endpoints: []string{"spine1:Ethernet4", "leaf2:Ethernet0"}},
			{Endpoints: []string{"spine2:Ethernet0", "leaf1:Ethernet4"}},
			{Endpoints: []string{"spine2:Ethernet4", "leaf2:Ethernet4"}},
			{Endpoints: []string{"leaf1:Ethernet8", "server1:eth1"}},
			{Endpoints: []string{"leaf2:Ethernet8", "server2:eth1"}},
		},
	}
}

// =============================================================================
// Topology Parsing & Validation
// =============================================================================

func TestLoadTopology(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")

	data, err := yaml.Marshal(testTopology())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	topo, err := LoadTopology(path)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	if topo.Name != "test-topo" {
		t.Errorf("name = %q, want %q", topo.Name, "test-topo")
	}
	if len(topo.Nodes) != 6 {
		t.Errorf("node count = %d, want 6", len(topo.Nodes))
	}
	if len(topo.Links) != 6 {
		t.Errorf("link count = %d, want 6", len(topo.Links))
	}
}

func TestLoadTopology_FileNotFound(t *testing.T) {
	_, err := LoadTopology("/nonexistent/path.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidation_MissingName(t *testing.T) {
	topo := testTopology()
	topo.Name = ""
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidation_NoNodes(t *testing.T) {
	topo := testTopology()
	topo.Nodes = map[string]NodeDef{}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for no nodes")
	}
}

func TestValidation_MissingImage(t *testing.T) {
	topo := testTopology()
	topo.Defaults.Image = ""
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestValidation_MissingASNumber(t *testing.T) {
	topo := testTopology()
	topo.Network.ASNumber = 0
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for missing AS number")
	}
}

func TestValidation_MissingRegion(t *testing.T) {
	topo := testTopology()
	topo.Network.Region = ""
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for missing region")
	}
}

func TestValidation_InvalidRole(t *testing.T) {
	topo := testTopology()
	topo.Nodes["bad"] = NodeDef{Role: "access", LoopbackIP: "10.0.0.99"}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestValidation_MissingLoopbackIP(t *testing.T) {
	topo := testTopology()
	topo.Nodes["spine1"] = NodeDef{Role: "spine", LoopbackIP: ""}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for missing loopback IP")
	}
}

func TestValidation_InvalidLoopbackIP(t *testing.T) {
	topo := testTopology()
	topo.Nodes["spine1"] = NodeDef{Role: "spine", LoopbackIP: "not-an-ip"}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

func TestValidation_ServerRole(t *testing.T) {
	topo := testTopology()
	// "server" is already in testTopology — just verify it validates
	if err := validateTopology(topo); err != nil {
		t.Fatalf("server role should be valid: %v", err)
	}
}

func TestValidation_ServerNoLoopbackRequired(t *testing.T) {
	topo := testTopology()
	// Server nodes should not require loopback_ip
	topo.Nodes["server1"] = NodeDef{Role: "server", Image: "nicolaka/netshoot:latest"}
	if err := validateTopology(topo); err != nil {
		t.Fatalf("server without loopback_ip should be valid: %v", err)
	}
}

func TestValidation_LinkBadEndpointCount(t *testing.T) {
	topo := testTopology()
	topo.Links = []LinkDef{{Endpoints: []string{"spine1:Ethernet0"}}}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for link with 1 endpoint")
	}
}

func TestValidation_LinkUndefinedNode(t *testing.T) {
	topo := testTopology()
	topo.Links = []LinkDef{{Endpoints: []string{"spine1:Ethernet0", "missing:Ethernet0"}}}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for undefined node in link")
	}
}

func TestValidation_LinkBadFormat(t *testing.T) {
	topo := testTopology()
	topo.Links = []LinkDef{{Endpoints: []string{"spine1:Ethernet0", "no-colon"}}}
	if err := validateTopology(topo); err == nil {
		t.Fatal("expected error for bad endpoint format")
	}
}

func TestNodeInterfaces(t *testing.T) {
	topo := testTopology()

	spine1Ifaces := NodeInterfaces(topo, "spine1")
	if len(spine1Ifaces) != 2 {
		t.Errorf("spine1 interface count = %d, want 2", len(spine1Ifaces))
	}

	leaf1Ifaces := NodeInterfaces(topo, "leaf1")
	if len(leaf1Ifaces) != 3 {
		t.Errorf("leaf1 interface count = %d, want 3", len(leaf1Ifaces))
	}

	// Node with no links should return empty
	noLinks := NodeInterfaces(topo, "nonexistent")
	if len(noLinks) != 0 {
		t.Errorf("nonexistent node should have 0 interfaces, got %d", len(noLinks))
	}
}

// =============================================================================
// MAC Generation
// =============================================================================

func TestNodeMAC_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		mac := nodeMAC(i)
		if seen[mac] {
			t.Fatalf("duplicate MAC at index %d: %s", i, mac)
		}
		seen[mac] = true
	}
}

func TestNodeMAC_LocallyAdministered(t *testing.T) {
	mac := nodeMAC(0)
	// First octet must have bit 1 set (locally administered)
	if mac[:2] != "02" {
		t.Errorf("MAC %s does not start with 02 (locally administered)", mac)
	}
}

func TestNodeMAC_Format(t *testing.T) {
	mac := nodeMAC(0)
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		t.Errorf("MAC %s has %d octets, want 6", mac, len(parts))
	}
	for _, p := range parts {
		if len(p) != 2 {
			t.Errorf("MAC octet %q should be 2 hex chars", p)
		}
	}
}

func TestNodeMAC_HighIndex(t *testing.T) {
	// Index 256 should use the second-to-last octet
	mac := nodeMAC(256)
	if mac != "02:42:f0:ab:01:00" {
		t.Errorf("nodeMAC(256) = %s, want 02:42:f0:ab:01:00", mac)
	}
}

// =============================================================================
// Config DB Generation
// =============================================================================

func TestGenerateMinimalStartupConfigs(t *testing.T) {
	topo := testTopology()

	dir := t.TempDir()
	if err := GenerateMinimalStartupConfigs(topo, dir); err != nil {
		t.Fatalf("GenerateMinimalStartupConfigs: %v", err)
	}

	// Each SONiC node should have a config_db.json
	for _, name := range []string{"spine1", "spine2", "leaf1", "leaf2"} {
		path := filepath.Join(dir, name, "config_db.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		var configDB map[string]map[string]map[string]string
		if err := json.Unmarshal(data, &configDB); err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		// Verify DEVICE_METADATA.localhost.mac exists and is non-empty
		mac := configDB["DEVICE_METADATA"]["localhost"]["mac"]
		if mac == "" {
			t.Errorf("%s: MAC address not set in DEVICE_METADATA", name)
		}

		// Verify unified routing mode
		mode := configDB["DEVICE_METADATA"]["localhost"]["docker_routing_config_mode"]
		if mode != "unified" {
			t.Errorf("%s: docker_routing_config_mode = %q, want %q", name, mode, "unified")
		}

		// Verify PORT table has at least 8 entries
		if len(configDB["PORT"]) < 8 {
			t.Errorf("%s: PORT count = %d, want >= 8", name, len(configDB["PORT"]))
		}

		// Verify LOOPBACK_INTERFACE exists
		if _, ok := configDB["LOOPBACK_INTERFACE"]; !ok {
			t.Errorf("%s: LOOPBACK_INTERFACE missing", name)
		}
	}

	// Server nodes should NOT have config_db.json
	for _, name := range []string{"server1", "server2"} {
		path := filepath.Join(dir, name, "config_db.json")
		if _, err := os.Stat(path); err == nil {
			t.Errorf("server node %s should not have config_db.json", name)
		}
	}
}

func TestGenerateMinimalStartupConfigs_UniqueMACs(t *testing.T) {
	topo := testTopology()

	dir := t.TempDir()
	if err := GenerateMinimalStartupConfigs(topo, dir); err != nil {
		t.Fatalf("GenerateMinimalStartupConfigs: %v", err)
	}

	macs := make(map[string]string) // mac -> node name
	for _, name := range []string{"spine1", "spine2", "leaf1", "leaf2"} {
		data, _ := os.ReadFile(filepath.Join(dir, name, "config_db.json"))
		var configDB map[string]map[string]map[string]string
		json.Unmarshal(data, &configDB)

		mac := configDB["DEVICE_METADATA"]["localhost"]["mac"]
		if other, exists := macs[mac]; exists {
			t.Errorf("duplicate MAC %s on %s and %s", mac, name, other)
		}
		macs[mac] = name
	}
}

func TestGenerateMinimalStartupConfigs_DeterministicMACs(t *testing.T) {
	topo := testTopology()

	// Generate twice and verify MACs are identical
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if err := GenerateMinimalStartupConfigs(topo, dir1); err != nil {
		t.Fatal(err)
	}
	if err := GenerateMinimalStartupConfigs(topo, dir2); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"spine1", "spine2", "leaf1", "leaf2"} {
		data1, _ := os.ReadFile(filepath.Join(dir1, name, "config_db.json"))
		data2, _ := os.ReadFile(filepath.Join(dir2, name, "config_db.json"))

		var db1, db2 map[string]map[string]map[string]string
		json.Unmarshal(data1, &db1)
		json.Unmarshal(data2, &db2)

		mac1 := db1["DEVICE_METADATA"]["localhost"]["mac"]
		mac2 := db2["DEVICE_METADATA"]["localhost"]["mac"]
		if mac1 != mac2 {
			t.Errorf("%s: MAC not deterministic: %s vs %s", name, mac1, mac2)
		}
	}
}

func TestAddPortEntries_MinimumPorts(t *testing.T) {
	topo := testTopology()
	configDB := map[string]map[string]map[string]string{}

	addPortEntries(topo, "leaf1", configDB)

	// Should have at least 8 ports (Ethernet0 through Ethernet28, step 4)
	if len(configDB["PORT"]) < 8 {
		t.Errorf("PORT count = %d, want >= 8", len(configDB["PORT"]))
	}

	// Linked interfaces should be present
	if _, ok := configDB["PORT"]["Ethernet0"]; !ok {
		t.Error("Ethernet0 missing from PORT (should be added from link)")
	}
	if _, ok := configDB["PORT"]["Ethernet4"]; !ok {
		t.Error("Ethernet4 missing from PORT (should be added from link)")
	}
	if _, ok := configDB["PORT"]["Ethernet8"]; !ok {
		t.Error("Ethernet8 missing from PORT (should be added from link to server1)")
	}
}

func TestNodeMAC_Deterministic(t *testing.T) {
	// nodeMAC should produce deterministic, unique MACs
	mac0 := nodeMAC(0)
	mac1 := nodeMAC(1)
	mac0Again := nodeMAC(0)

	if mac0 == mac1 {
		t.Errorf("nodeMAC(0) == nodeMAC(1) = %q, expected unique MACs", mac0)
	}
	if mac0 != mac0Again {
		t.Errorf("nodeMAC(0) = %q, then %q; expected deterministic", mac0, mac0Again)
	}
	// Should be locally administered (02: prefix)
	if !strings.HasPrefix(mac0, "02:") {
		t.Errorf("nodeMAC(0) = %q, expected locally-administered 02: prefix", mac0)
	}
}

// =============================================================================
// Containerlab YAML Generation
// =============================================================================

func TestGenerateClabTopology_SonicVM(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()

	if err := GenerateClabTopology(topo, dir); err != nil {
		t.Fatalf("GenerateClabTopology: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-topo.clab.yml"))
	if err != nil {
		t.Fatalf("reading clab YAML: %v", err)
	}

	var clab ClabTopology
	if err := yaml.Unmarshal(data, &clab); err != nil {
		t.Fatalf("parsing clab YAML: %v", err)
	}

	if clab.Name != "test-topo" {
		t.Errorf("clab name = %q, want %q", clab.Name, "test-topo")
	}

	if len(clab.Topology.Nodes) != 6 {
		t.Errorf("clab node count = %d, want 6", len(clab.Topology.Nodes))
	}

	// Verify server nodes get kind: linux
	for _, srvName := range []string{"server1", "server2"} {
		srv := clab.Topology.Nodes[srvName]
		if srv == nil {
			t.Fatalf("%s missing from clab topology", srvName)
		}
		if srv.Kind != "linux" {
			t.Errorf("%s kind = %q, want %q", srvName, srv.Kind, "linux")
		}
		if srv.Image != "nicolaka/netshoot:latest" {
			t.Errorf("%s image = %q, want %q", srvName, srv.Image, "nicolaka/netshoot:latest")
		}
		if srv.Cmd != "sleep infinity" {
			t.Errorf("%s cmd = %q, want %q", srvName, srv.Cmd, "sleep infinity")
		}
		if srv.CPU != 0 {
			t.Errorf("%s should not have CPU tuning", srvName)
		}
		if srv.Healthcheck != nil {
			t.Errorf("%s should not have healthcheck", srvName)
		}
		if len(srv.Binds) != 0 {
			t.Errorf("%s should not have binds", srvName)
		}
		if srv.StartupConfig != "" {
			t.Errorf("%s should not have startup-config", srvName)
		}
	}

	// Check a specific SONiC node has correct settings
	leaf1 := clab.Topology.Nodes["leaf1"]
	if leaf1 == nil {
		t.Fatal("leaf1 node missing from clab topology")
	}

	if leaf1.Kind != "sonic-vm" {
		t.Errorf("leaf1 kind = %q, want %q", leaf1.Kind, "sonic-vm")
	}
	if leaf1.CPU != 2 {
		t.Errorf("leaf1 cpu = %d, want 2", leaf1.CPU)
	}
	if leaf1.Memory != "6144mib" {
		t.Errorf("leaf1 memory = %q, want %q", leaf1.Memory, "6144mib")
	}
	if leaf1.Env["QEMU_ADDITIONAL_ARGS"] != "-cpu host" {
		t.Errorf("leaf1 QEMU_ADDITIONAL_ARGS = %q, want %q", leaf1.Env["QEMU_ADDITIONAL_ARGS"], "-cpu host")
	}
	if leaf1.Env["USERNAME"] != "cisco" {
		t.Errorf("leaf1 USERNAME = %q, want %q", leaf1.Env["USERNAME"], "cisco")
	}
	if leaf1.Env["PASSWORD"] != "cisco123" {
		t.Errorf("leaf1 PASSWORD = %q, want %q", leaf1.Env["PASSWORD"], "cisco123")
	}
	if leaf1.StartupConfig != "leaf1/config_db.json" {
		t.Errorf("leaf1 startup-config = %q, want %q", leaf1.StartupConfig, "leaf1/config_db.json")
	}

	// Check healthcheck
	if leaf1.Healthcheck == nil {
		t.Fatal("leaf1 healthcheck is nil")
	}
	if leaf1.Healthcheck.StartPeriod != 600 {
		t.Errorf("healthcheck start-period = %d, want 600", leaf1.Healthcheck.StartPeriod)
	}

	// Check links are converted to sequential eth names
	if len(clab.Topology.Links) != 6 {
		t.Errorf("link count = %d, want 6", len(clab.Topology.Links))
	}
}

func TestGenerateClabTopology_SonicVS(t *testing.T) {
	topo := testTopology()
	topo.Defaults.Image = "ghcr.io/sonic-net/sonic-vs:latest"
	topo.Defaults.Username = ""
	topo.Defaults.Password = ""
	dir := t.TempDir()

	if err := GenerateClabTopology(topo, dir); err != nil {
		t.Fatalf("GenerateClabTopology: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-topo.clab.yml"))
	if err != nil {
		t.Fatalf("reading clab YAML: %v", err)
	}

	var clab ClabTopology
	if err := yaml.Unmarshal(data, &clab); err != nil {
		t.Fatalf("parsing clab YAML: %v", err)
	}

	leaf1 := clab.Topology.Nodes["leaf1"]
	if leaf1 == nil {
		t.Fatal("leaf1 missing")
	}

	if leaf1.Kind != "sonic-vs" {
		t.Errorf("kind = %q, want %q", leaf1.Kind, "sonic-vs")
	}
	if leaf1.CPU != 0 {
		t.Errorf("sonic-vs should not have CPU tuning, got %d", leaf1.CPU)
	}
	if leaf1.Healthcheck != nil {
		t.Error("sonic-vs should not have healthcheck")
	}
	if leaf1.StartupConfig == "" {
		t.Error("sonic-vs should have startup-config")
	}
	if len(leaf1.Binds) != 0 {
		t.Error("sonic-vs should not have bind mounts (uses startup-config)")
	}
}

func TestResolveKind_AutoDetect(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"vrnetlab/vr-sonic:202411", "sonic-vm"},
		{"vrnetlab/cisco_sonic:202411", "sonic-vm"},
		{"ghcr.io/sonic-net/sonic-vs:latest", "sonic-vs"},
		{"sonic-vs:local", "sonic-vs"},
	}

	for _, tt := range tests {
		got := kindFromImage(tt.image)
		if got != tt.want {
			t.Errorf("kindFromImage(%q) = %q, want %q", tt.image, got, tt.want)
		}
	}
}

func TestResolveKind_ExplicitOverride(t *testing.T) {
	topo := testTopology()
	topo.Defaults.Kind = "sonic-vs"

	got := resolveKind(topo)
	if got != "sonic-vs" {
		t.Errorf("resolveKind with explicit kind = %q, want %q", got, "sonic-vs")
	}
}

func TestBuildSequentialIfaceMaps(t *testing.T) {
	topo := testTopology()
	maps := buildSequentialIfaceMaps(topo)

	// spine1 has Ethernet0 and Ethernet4 in links
	spine1Map := maps["spine1"]
	if spine1Map == nil {
		t.Fatal("spine1 interface map is nil")
	}
	if spine1Map["Ethernet0"] != "eth1" {
		t.Errorf("spine1 Ethernet0 = %q, want eth1", spine1Map["Ethernet0"])
	}
	if spine1Map["Ethernet4"] != "eth2" {
		t.Errorf("spine1 Ethernet4 = %q, want eth2", spine1Map["Ethernet4"])
	}
}

func TestSonicIfaceToClabIface(t *testing.T) {
	tests := []struct {
		sonic string
		want  string
	}{
		{"Ethernet0", "eth1"},
		{"Ethernet4", "eth5"},
		{"Ethernet28", "eth29"},
		{"Loopback0", "Loopback0"}, // non-Ethernet passes through
	}

	for _, tt := range tests {
		got := SonicIfaceToClabIface(tt.sonic)
		if got != tt.want {
			t.Errorf("SonicIfaceToClabIface(%q) = %q, want %q", tt.sonic, got, tt.want)
		}
	}
}

func TestSonicIfaceNum(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"Ethernet0", 0},
		{"Ethernet4", 4},
		{"Ethernet28", 28},
		{"Loopback0", 0}, // non-Ethernet returns 0
	}

	for _, tt := range tests {
		got := sonicIfaceNum(tt.name)
		if got != tt.want {
			t.Errorf("sonicIfaceNum(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// =============================================================================
// Specs Generation
// =============================================================================

func TestGenerateLabSpecs(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()

	if err := GenerateLabSpecs(topo, dir); err != nil {
		t.Fatalf("GenerateLabSpecs: %v", err)
	}

	// Verify all spec files exist (SONiC nodes only)
	specFiles := []string{
		"specs/network.json",
		"specs/site.json",
		"specs/platforms.json",
		"specs/profiles/spine1.json",
		"specs/profiles/spine2.json",
		"specs/profiles/leaf1.json",
		"specs/profiles/leaf2.json",
	}
	for _, f := range specFiles {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("missing spec file: %s", f)
		}
	}

	// Server nodes should NOT have profile files
	for _, name := range []string{"server1", "server2"} {
		path := filepath.Join(dir, "specs", "profiles", name+".json")
		if _, err := os.Stat(path); err == nil {
			t.Errorf("server node %s should not have a profile file", name)
		}
	}
}

func TestGenerateNetworkSpec(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0755)

	if err := generateNetworkSpec(topo, specsDir); err != nil {
		t.Fatalf("generateNetworkSpec: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(specsDir, "network.json"))
	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parsing network.json: %v", err)
	}

	// Check region exists with correct AS number
	regions := spec["regions"].(map[string]interface{})
	region := regions["lab-region"].(map[string]interface{})
	if int(region["as_number"].(float64)) != 65000 {
		t.Errorf("region AS = %v, want 65000", region["as_number"])
	}

	// Check services include customer-l3
	services := spec["services"].(map[string]interface{})
	if _, ok := services["customer-l3"]; !ok {
		t.Error("customer-l3 service not found in network spec")
	}

	// Check filter_specs
	filters := spec["filter_specs"].(map[string]interface{})
	if _, ok := filters["customer-l3-in"]; !ok {
		t.Error("customer-l3-in filter not found")
	}

	// Check ipvpn
	ipvpn := spec["ipvpn"].(map[string]interface{})
	if _, ok := ipvpn["customer-vpn"]; !ok {
		t.Error("customer-vpn not found in ipvpn")
	}
}

func TestGenerateSiteSpec(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0755)

	if err := generateSiteSpec(topo, specsDir); err != nil {
		t.Fatalf("generateSiteSpec: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(specsDir, "site.json"))
	var spec map[string]interface{}
	json.Unmarshal(data, &spec)

	sites := spec["sites"].(map[string]interface{})
	site := sites["lab-site"].(map[string]interface{})

	if site["region"] != "lab-region" {
		t.Errorf("site region = %v, want lab-region", site["region"])
	}

	// Spine nodes should be route reflectors
	rrs := site["route_reflectors"].([]interface{})
	if len(rrs) != 2 {
		t.Errorf("route reflector count = %d, want 2", len(rrs))
	}
}

func TestGenerateProfiles(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	os.MkdirAll(profilesDir, 0755)

	if err := generateProfiles(topo, profilesDir); err != nil {
		t.Fatalf("generateProfiles: %v", err)
	}

	// Check leaf1 profile
	data, _ := os.ReadFile(filepath.Join(profilesDir, "leaf1.json"))
	var profile map[string]interface{}
	json.Unmarshal(data, &profile)

	if profile["mgmt_ip"] != "PLACEHOLDER" {
		t.Errorf("mgmt_ip = %v, want PLACEHOLDER", profile["mgmt_ip"])
	}
	if profile["loopback_ip"] != "10.0.0.11" {
		t.Errorf("loopback_ip = %v, want 10.0.0.11", profile["loopback_ip"])
	}
	if profile["site"] != "lab-site" {
		t.Errorf("site = %v, want lab-site", profile["site"])
	}
	if profile["platform"] != "vs-platform" {
		t.Errorf("platform = %v, want vs-platform", profile["platform"])
	}

	// Check spine1 has is_route_reflector
	data, _ = os.ReadFile(filepath.Join(profilesDir, "spine1.json"))
	var spineProfile map[string]interface{}
	json.Unmarshal(data, &spineProfile)

	if spineProfile["is_route_reflector"] != true {
		t.Errorf("spine1 is_route_reflector = %v, want true", spineProfile["is_route_reflector"])
	}

	// Check leaf1 does NOT have is_route_reflector
	data, _ = os.ReadFile(filepath.Join(profilesDir, "leaf1.json"))
	var leafProfile map[string]interface{}
	json.Unmarshal(data, &leafProfile)

	if _, ok := leafProfile["is_route_reflector"]; ok {
		t.Error("leaf1 should not have is_route_reflector")
	}
}

func TestGeneratePlatformsSpec(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()
	specsDir := filepath.Join(dir, "specs")
	os.MkdirAll(specsDir, 0755)

	if err := generatePlatformsSpec(topo, specsDir); err != nil {
		t.Fatalf("generatePlatformsSpec: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(specsDir, "platforms.json"))
	var spec map[string]interface{}
	json.Unmarshal(data, &spec)

	platforms := spec["platforms"].(map[string]interface{})
	platform := platforms["vs-platform"].(map[string]interface{})

	if platform["hwsku"] != "cisco-8101-p4-32x100-vs" {
		t.Errorf("hwsku = %v, want Force10-S6000", platform["hwsku"])
	}
}

func TestGenerateProfiles_PreservesRuntimeValues(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	os.MkdirAll(profilesDir, 0755)

	// Step 1: Generate initial profiles (PLACEHOLDER)
	if err := generateProfiles(topo, profilesDir); err != nil {
		t.Fatalf("first generateProfiles: %v", err)
	}

	// Verify initial state is PLACEHOLDER
	data, _ := os.ReadFile(filepath.Join(profilesDir, "leaf1.json"))
	var initial map[string]interface{}
	json.Unmarshal(data, &initial)
	if initial["mgmt_ip"] != "PLACEHOLDER" {
		t.Fatalf("initial mgmt_ip = %v, want PLACEHOLDER", initial["mgmt_ip"])
	}

	// Step 2: Patch leaf1 with runtime values (simulating setup.sh)
	initial["mgmt_ip"] = "172.20.20.5"
	initial["ssh_user"] = "admin"
	initial["ssh_pass"] = "secret123"
	patched, _ := json.MarshalIndent(initial, "", "  ")
	os.WriteFile(filepath.Join(profilesDir, "leaf1.json"), patched, 0644)

	// Step 3: Change topology values to verify they get regenerated
	topo.Nodes["leaf1"] = NodeDef{Role: "leaf", LoopbackIP: "10.0.0.99"}

	// Step 4: Regenerate profiles
	if err := generateProfiles(topo, profilesDir); err != nil {
		t.Fatalf("second generateProfiles: %v", err)
	}

	// Step 5: Verify runtime values are preserved
	data, _ = os.ReadFile(filepath.Join(profilesDir, "leaf1.json"))
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	if result["mgmt_ip"] != "172.20.20.5" {
		t.Errorf("mgmt_ip = %v, want 172.20.20.5 (should be preserved)", result["mgmt_ip"])
	}
	if result["ssh_user"] != "admin" {
		t.Errorf("ssh_user = %v, want admin (should be preserved)", result["ssh_user"])
	}
	if result["ssh_pass"] != "secret123" {
		t.Errorf("ssh_pass = %v, want secret123 (should be preserved)", result["ssh_pass"])
	}

	// Step 6: Verify topology-derived values are updated
	if result["loopback_ip"] != "10.0.0.99" {
		t.Errorf("loopback_ip = %v, want 10.0.0.99 (should be regenerated)", result["loopback_ip"])
	}

	// Step 7: Verify unpatched node still gets PLACEHOLDER
	data, _ = os.ReadFile(filepath.Join(profilesDir, "spine1.json"))
	var spine map[string]interface{}
	json.Unmarshal(data, &spine)
	if spine["mgmt_ip"] != "PLACEHOLDER" {
		t.Errorf("spine1 mgmt_ip = %v, want PLACEHOLDER (was not patched)", spine["mgmt_ip"])
	}
	if _, ok := spine["ssh_user"]; ok {
		t.Error("spine1 should not have ssh_user (was not patched)")
	}
}

func TestGenerateProfiles_DefaultSiteName(t *testing.T) {
	topo := testTopology()
	topo.Defaults.Site = "" // should fall back to "<name>-site"
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	os.MkdirAll(profilesDir, 0755)

	if err := generateProfiles(topo, profilesDir); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(profilesDir, "leaf1.json"))
	var profile map[string]interface{}
	json.Unmarshal(data, &profile)

	if profile["site"] != "test-topo-site" {
		t.Errorf("site = %v, want test-topo-site", profile["site"])
	}
}

// =============================================================================
// Topology Spec Generation
// =============================================================================

func TestGenerateTopologySpec(t *testing.T) {
	topo := testTopology()
	dir := t.TempDir()

	// GenerateTopologySpec writes to specs/ subdir
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTopologySpec(topo, dir); err != nil {
		t.Fatalf("GenerateTopologySpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "specs", "topology.json"))
	if err != nil {
		t.Fatalf("reading topology.json: %v", err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parsing topology.json: %v", err)
	}

	// Top-level fields
	if spec["version"] != "1.0" {
		t.Errorf("version = %v, want 1.0", spec["version"])
	}

	devices, ok := spec["devices"].(map[string]interface{})
	if !ok {
		t.Fatal("devices is not a map")
	}

	// Should have 4 SONiC devices (2 spines + 2 leaves), not servers
	if len(devices) != 4 {
		t.Errorf("device count = %d, want 4", len(devices))
	}
	if _, ok := devices["server1"]; ok {
		t.Error("server1 should not be in topology devices")
	}

	// Spine nodes should have device_config.route_reflector = true
	spine1 := devices["spine1"].(map[string]interface{})
	dc, ok := spine1["device_config"].(map[string]interface{})
	if !ok {
		t.Fatal("spine1 missing device_config")
	}
	if dc["route_reflector"] != true {
		t.Errorf("spine1 route_reflector = %v, want true", dc["route_reflector"])
	}

	// Leaf nodes should NOT have device_config
	leaf1 := devices["leaf1"].(map[string]interface{})
	if _, ok := leaf1["device_config"]; ok {
		t.Error("leaf1 should not have device_config")
	}

	// Spine interfaces should have RR params
	spine1Intfs := spine1["interfaces"].(map[string]interface{})
	spine1Eth0 := spine1Intfs["Ethernet0"].(map[string]interface{})
	if spine1Eth0["service"] != "fabric-underlay" {
		t.Errorf("spine1:Ethernet0 service = %v, want fabric-underlay", spine1Eth0["service"])
	}
	params := spine1Eth0["params"].(map[string]interface{})
	if params["route_reflector_client"] != "true" {
		t.Errorf("spine1:Ethernet0 route_reflector_client = %v, want true", params["route_reflector_client"])
	}
	if params["next_hop_self"] != "true" {
		t.Errorf("spine1:Ethernet0 next_hop_self = %v, want true", params["next_hop_self"])
	}
	if params["peer_as"] != "65000" {
		t.Errorf("spine1:Ethernet0 peer_as = %v, want 65000", params["peer_as"])
	}

	// Leaf interfaces should NOT have RR params
	leaf1Intfs := leaf1["interfaces"].(map[string]interface{})
	leaf1Eth0 := leaf1Intfs["Ethernet0"].(map[string]interface{})
	leafParams := leaf1Eth0["params"].(map[string]interface{})
	if _, ok := leafParams["route_reflector_client"]; ok {
		t.Error("leaf1:Ethernet0 should not have route_reflector_client")
	}
	if leafParams["peer_as"] != "65000" {
		t.Errorf("leaf1:Ethernet0 peer_as = %v, want 65000", leafParams["peer_as"])
	}

	// IP addresses should be /31 pairs
	ip := spine1Eth0["ip"].(string)
	if !strings.Contains(ip, "/31") {
		t.Errorf("spine1:Ethernet0 ip = %q, want /31 prefix", ip)
	}

	// Link field should reference peer
	link := spine1Eth0["link"].(string)
	if !strings.HasPrefix(link, "leaf1:") {
		t.Errorf("spine1:Ethernet0 link = %q, want leaf1:*", link)
	}

	// Links array should only contain inter-switch links (no server links)
	links, ok := spec["links"].([]interface{})
	if !ok {
		t.Fatal("links is not an array")
	}
	// 4 fabric links: spine1-leaf1, spine1-leaf2, spine2-leaf1, spine2-leaf2
	if len(links) != 4 {
		t.Errorf("link count = %d, want 4 (server links excluded)", len(links))
	}
	for _, l := range links {
		lm := l.(map[string]interface{})
		a := lm["a"].(string)
		z := lm["z"].(string)
		if strings.Contains(a, "server") || strings.Contains(z, "server") {
			t.Errorf("link %s - %s should not include server nodes", a, z)
		}
	}
}

func TestGenerateTopologySpec_NoFabricLinks(t *testing.T) {
	topo := &Topology{
		Name: "no-links",
		Defaults: TopologyDefaults{
			Image:    "vrnetlab/vr-sonic:202411",
			Platform: "vs-platform",
			Site:     "lab",
			HWSKU:    "cisco-8101-p4-32x100-vs",
		},
		Network: TopologyNetwork{ASNumber: 65000, Region: "lab"},
		Nodes: map[string]NodeDef{
			"leaf1": {Role: "leaf", LoopbackIP: "10.0.0.1"},
		},
		Links: []LinkDef{},
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTopologySpec(topo, dir); err != nil {
		t.Fatalf("GenerateTopologySpec: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "specs", "topology.json"))
	var spec map[string]interface{}
	json.Unmarshal(data, &spec)

	devices := spec["devices"].(map[string]interface{})
	leaf1 := devices["leaf1"].(map[string]interface{})
	if _, ok := leaf1["interfaces"]; ok {
		t.Error("leaf1 should not have interfaces when there are no fabric links")
	}
}

func TestPeerInterfaceName(t *testing.T) {
	allLinkIPs := map[string]map[string]FabricLinkIP{
		"spine1": {
			"Ethernet0": {Node: "spine1", Interface: "Ethernet0", IP: "10.1.0.1/31", PeerNode: "leaf1", PeerIP: "10.1.0.0"},
		},
		"leaf1": {
			"Ethernet0": {Node: "leaf1", Interface: "Ethernet0", IP: "10.1.0.0/31", PeerNode: "spine1", PeerIP: "10.1.0.1"},
		},
	}

	// Should find peer interface
	got := peerInterfaceName(allLinkIPs, "leaf1", "spine1", "Ethernet0")
	if got != "Ethernet0" {
		t.Errorf("peerInterfaceName = %q, want %q", got, "Ethernet0")
	}

	// Fallback when peer not found
	got = peerInterfaceName(allLinkIPs, "unknown", "spine1", "Ethernet0")
	if got != "Ethernet0" {
		t.Errorf("peerInterfaceName fallback = %q, want %q", got, "Ethernet0")
	}
}

// =============================================================================
// Underlay ASN Assignment (eBGP fabric)
// =============================================================================

func TestGenerateTopologySpec_UnderlayASN(t *testing.T) {
	topo := testTopology()
	topo.Network.UnderlayASBase = 65100
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTopologySpec(topo, dir); err != nil {
		t.Fatalf("GenerateTopologySpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "specs", "topology.json"))
	if err != nil {
		t.Fatalf("reading topology.json: %v", err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parsing topology.json: %v", err)
	}

	devices := spec["devices"].(map[string]interface{})

	// Expected ASN layout (RFC 7938 Clos):
	//   spines share 65100, leafs get 65101, 65102 (sorted alpha: leaf1, leaf2)
	//
	// spine1:Ethernet0 → leaf1 → peer_as should be leaf1's ASN = 65101
	// spine1:Ethernet4 → leaf2 → peer_as should be leaf2's ASN = 65102
	// leaf1:Ethernet0  → spine1 → peer_as should be spine ASN = 65100
	// leaf1:Ethernet4  → spine2 → peer_as should be spine ASN = 65100

	tests := []struct {
		device string
		intf   string
		wantAS string
	}{
		{"spine1", "Ethernet0", "65101"}, // spine→leaf1
		{"spine1", "Ethernet4", "65102"}, // spine→leaf2
		{"spine2", "Ethernet0", "65101"}, // spine→leaf1
		{"spine2", "Ethernet4", "65102"}, // spine→leaf2
		{"leaf1", "Ethernet0", "65100"},  // leaf→spine1
		{"leaf1", "Ethernet4", "65100"},  // leaf→spine2
		{"leaf2", "Ethernet0", "65100"},  // leaf→spine1
		{"leaf2", "Ethernet4", "65100"},  // leaf→spine2
	}

	for _, tt := range tests {
		dev := devices[tt.device].(map[string]interface{})
		intfs := dev["interfaces"].(map[string]interface{})
		intf := intfs[tt.intf].(map[string]interface{})
		params := intf["params"].(map[string]interface{})

		if params["peer_as"] != tt.wantAS {
			t.Errorf("%s:%s peer_as = %v, want %s", tt.device, tt.intf, params["peer_as"], tt.wantAS)
		}
	}
}

func TestGenerateProfiles_UnderlayASN(t *testing.T) {
	topo := testTopology()
	topo.Network.UnderlayASBase = 65100
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	os.MkdirAll(profilesDir, 0755)

	if err := generateProfiles(topo, profilesDir); err != nil {
		t.Fatalf("generateProfiles: %v", err)
	}

	// Spines share base ASN
	for _, name := range []string{"spine1", "spine2"} {
		data, _ := os.ReadFile(filepath.Join(profilesDir, name+".json"))
		var profile map[string]interface{}
		json.Unmarshal(data, &profile)

		asn := int(profile["underlay_asn"].(float64))
		if asn != 65100 {
			t.Errorf("%s underlay_asn = %d, want 65100", name, asn)
		}
	}

	// Leafs get unique ASNs (sorted alpha: leaf1=65101, leaf2=65102)
	leafExpected := map[string]int{"leaf1": 65101, "leaf2": 65102}
	for name, wantASN := range leafExpected {
		data, _ := os.ReadFile(filepath.Join(profilesDir, name+".json"))
		var profile map[string]interface{}
		json.Unmarshal(data, &profile)

		asn := int(profile["underlay_asn"].(float64))
		if asn != wantASN {
			t.Errorf("%s underlay_asn = %d, want %d", name, asn, wantASN)
		}
	}
}

func TestGenerateMinimalStartupConfigs_UnderlayASN(t *testing.T) {
	topo := testTopology()
	topo.Network.UnderlayASBase = 65100
	dir := t.TempDir()

	if err := GenerateMinimalStartupConfigs(topo, dir); err != nil {
		t.Fatalf("GenerateMinimalStartupConfigs: %v", err)
	}

	// Spines share base ASN in config_db
	for _, name := range []string{"spine1", "spine2"} {
		data, _ := os.ReadFile(filepath.Join(dir, name, "config_db.json"))
		var configDB map[string]map[string]map[string]string
		json.Unmarshal(data, &configDB)

		bgpASN := configDB["DEVICE_METADATA"]["localhost"]["bgp_asn"]
		if bgpASN != "65100" {
			t.Errorf("%s bgp_asn = %s, want 65100", name, bgpASN)
		}
	}

	// Leafs get unique ASNs
	leafExpected := map[string]string{"leaf1": "65101", "leaf2": "65102"}
	for name, wantASN := range leafExpected {
		data, _ := os.ReadFile(filepath.Join(dir, name, "config_db.json"))
		var configDB map[string]map[string]map[string]string
		json.Unmarshal(data, &configDB)

		bgpASN := configDB["DEVICE_METADATA"]["localhost"]["bgp_asn"]
		if bgpASN != wantASN {
			t.Errorf("%s bgp_asn = %s, want %s", name, bgpASN, wantASN)
		}
	}
}
