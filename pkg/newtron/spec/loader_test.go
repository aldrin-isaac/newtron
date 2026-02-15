package spec

import (
	"os"
	"path/filepath"
	"testing"
)

// Helper to create test spec directory with files
func createTestSpecDir(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create network.json
	networkJSON := `{
		"version": "1.0",
		"super_users": ["admin"],
		"zones": {
			"amer": {
				"as_number": 65000
			}
		},
		"prefix_lists": {
			"rfc1918": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
		},
		"filters": {
			"test-filter": {
				"description": "Test filter",
				"type": "ipv4",
				"rules": [
					{"seq": 100, "action": "permit"}
				]
			}
		},
		"ipvpns": {
			"customer-vpn": {
				"l3vni": 10001,
				"vrf": "Vrf_customer",
				"route_targets": ["65000:100"]
			}
		},
		"macvpns": {
			"server-vlan": {
				"vlan_id": 100,
				"vni": 1100
			}
		},
		"services": {
			"customer-l3": {
				"description": "Customer L3 service",
				"service_type": "evpn-routed",
				"ipvpn": "customer-vpn",
				"vrf_type": "interface"
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Create platforms.json
	platformsJSON := `{
		"version": "1.0",
		"platforms": {
			"as7726": {
				"hwsku": "Accton-AS7726-32X",
				"port_count": 32,
				"default_speed": "100G"
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(platformsJSON), 0644); err != nil {
		t.Fatalf("Failed to write platforms.json: %v", err)
	}

	// Create profiles directory
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		t.Fatalf("Failed to create profiles dir: %v", err)
	}

	// Create test profile
	profileJSON := `{
		"mgmt_ip": "192.168.1.10",
		"loopback_ip": "10.0.0.10",
		"zone": "amer",
		"platform": "as7726",
		"evpn": {
			"peers": ["spine1-ny", "spine2-ny"]
		}
	}`
	if err := os.WriteFile(filepath.Join(profilesDir, "leaf1-ny.json"), []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	// Create spine profile for EVPN peer lookup
	spineProfileJSON := `{
		"mgmt_ip": "192.168.1.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"evpn": {
			"route_reflector": true
		}
	}`
	if err := os.WriteFile(filepath.Join(profilesDir, "spine1-ny.json"), []byte(spineProfileJSON), 0644); err != nil {
		t.Fatalf("Failed to write spine1 profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "spine2-ny.json"), []byte(spineProfileJSON), 0644); err != nil {
		t.Fatalf("Failed to write spine2 profile: %v", err)
	}

	return tmpDir
}

func TestLoader_Load(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Check network spec loaded
	network := loader.GetNetwork()
	if network == nil {
		t.Fatal("GetNetwork() returned nil")
	}
	if network.Version != "1.0" {
		t.Errorf("Network version = %q, want %q", network.Version, "1.0")
	}
	if len(network.Zones) != 1 {
		t.Errorf("Expected 1 zone, got %d", len(network.Zones))
	}

	// Check platforms loaded
	platforms := loader.GetPlatforms()
	if platforms == nil {
		t.Fatal("GetPlatforms() returned nil")
	}
	if len(platforms.Platforms) != 1 {
		t.Errorf("Expected 1 platform, got %d", len(platforms.Platforms))
	}
}

func TestLoader_LoadProfile(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	profile, err := loader.LoadProfile("leaf1-ny")
	if err != nil {
		t.Fatalf("LoadProfile() failed: %v", err)
	}

	if profile.MgmtIP != "192.168.1.10" {
		t.Errorf("MgmtIP = %q, want %q", profile.MgmtIP, "192.168.1.10")
	}
	if profile.LoopbackIP != "10.0.0.10" {
		t.Errorf("LoopbackIP = %q, want %q", profile.LoopbackIP, "10.0.0.10")
	}
	if profile.Zone != "amer" {
		t.Errorf("Zone = %q, want %q", profile.Zone, "amer")
	}
}

func TestLoader_LoadProfile_Caching(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Load twice, should get same pointer (cached)
	p1, _ := loader.LoadProfile("leaf1-ny")
	p2, _ := loader.LoadProfile("leaf1-ny")

	if p1 != p2 {
		t.Error("LoadProfile should return cached profile")
	}
}

func TestLoader_LoadProfile_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.LoadProfile("nonexistent")
	if err == nil {
		t.Error("LoadProfile() should fail for nonexistent profile")
	}
}

func TestLoader_GetService(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	svc, err := loader.GetService("customer-l3")
	if err != nil {
		t.Fatalf("GetService() failed: %v", err)
	}
	if svc.ServiceType != "evpn-routed" {
		t.Errorf("ServiceType = %q, want %q", svc.ServiceType, "evpn-routed")
	}
}

func TestLoader_GetService_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.GetService("nonexistent")
	if err == nil {
		t.Error("GetService() should fail for nonexistent service")
	}
}

func TestLoader_GetFilter(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	filter, err := loader.GetFilter("test-filter")
	if err != nil {
		t.Fatalf("GetFilter() failed: %v", err)
	}
	if filter.Type != "ipv4" {
		t.Errorf("Filter type = %q, want %q", filter.Type, "ipv4")
	}
}

func TestLoader_GetPrefixList(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	list, err := loader.GetPrefixList("rfc1918")
	if err != nil {
		t.Fatalf("GetPrefixList() failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected 3 prefixes, got %d", len(list))
	}
}

func TestLoader_ListServices(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	services := loader.ListServices()
	if len(services) != 1 {
		t.Errorf("Expected 1 service, got %d", len(services))
	}
}

func TestLoader_DefaultSpecDir(t *testing.T) {
	// Test that empty string uses default
	loader := NewLoader("")
	if loader.specDir != SpecDir {
		t.Errorf("Empty specDir should use default %q, got %q", SpecDir, loader.specDir)
	}
}

func TestLoader_ValidationErrors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create network.json with invalid service reference
	networkJSON := `{
		"version": "1.0",
		"zones": {},
		"services": {
			"bad-service": {
				"description": "Bad service",
				"service_type": "routed",
				"ingress_filter": "nonexistent-filter"
			}
		},
		"filters": {}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Create platforms.json
	platformsJSON := `{"version": "1.0", "platforms": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(platformsJSON), 0644); err != nil {
		t.Fatalf("Failed to write platforms.json: %v", err)
	}

	loader := NewLoader(tmpDir)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail with invalid filter reference")
	}
}

func TestLoader_LoadMissingNetworkSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail when network.json is missing")
	}
}

func TestLoader_LoadMissingPlatformSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create network.json only
	networkJSON := `{"version": "1.0", "zones": {}, "services": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	loader := NewLoader(tmpDir)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail when platforms.json is missing")
	}
}

func TestLoader_LoadInvalidJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name     string
		file     string
		content  string
		setup    func()
	}{
		{
			name:    "invalid network.json",
			file:    "network.json",
			content: "invalid json {",
		},
		{
			name:    "invalid platforms.json",
			file:    "platforms.json",
			content: "invalid json {",
			setup: func() {
				os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(`{"version": "1.0", "zones": {}, "services": {}}`), 0644)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean directory
			os.RemoveAll(tmpDir)
			os.MkdirAll(tmpDir, 0755)

			// Setup prerequisite files
			if tt.setup != nil {
				tt.setup()
			}

			// Write invalid JSON
			if err := os.WriteFile(filepath.Join(tmpDir, tt.file), []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write %s: %v", tt.file, err)
			}

			loader := NewLoader(tmpDir)
			err := loader.Load()
			if err == nil {
				t.Errorf("Load() should fail with invalid %s", tt.file)
			}
		})
	}
}

func TestLoader_LoadProfile_InvalidJSON(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Create profile with invalid JSON
	profilePath := filepath.Join(tmpDir, "profiles", "bad-profile.json")
	if err := os.WriteFile(profilePath, []byte("invalid json {"), 0644); err != nil {
		t.Fatalf("Failed to write bad profile: %v", err)
	}

	_, err := loader.LoadProfile("bad-profile")
	if err == nil {
		t.Error("LoadProfile() should fail with invalid JSON")
	}
}

func TestLoader_ValidateAllServiceErrors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name        string
		networkJSON string
		expectErr   bool
	}{
		{
			name: "invalid egress filter",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "routed",
						"egress_filter": "nonexistent-filter"
					}
				},
				"filters": {}
			}`,
			expectErr: true,
		},
		{
			name: "invalid qos profile",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "routed",
						"qos_profile": "nonexistent-qos"
					}
				},
				"qos_profiles": {}
			}`,
			expectErr: true,
		},
		{
			name: "invalid ipvpn reference",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "evpn-routed",
						"ipvpn": "nonexistent-vpn"
					}
				},
				"ipvpns": {}
			}`,
			expectErr: true,
		},
		{
			name: "invalid macvpn reference",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "evpn-bridged",
						"macvpn": "nonexistent-vpn"
					}
				},
				"macvpns": {}
			}`,
			expectErr: true,
		},
		{
			name: "evpn-bridged service without macvpn",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "evpn-bridged"
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "evpn-routed service without ipvpn",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "evpn-routed"
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "evpn-irb service without macvpn",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-service": {
						"service_type": "evpn-irb",
						"ipvpn": "some-vpn"
					}
				},
				"ipvpns": {
					"some-vpn": {"l3vni": 10001}
				}
			}`,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean and setup
			os.RemoveAll(tmpDir)
			os.MkdirAll(tmpDir, 0755)

			if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(tt.networkJSON), 0644); err != nil {
				t.Fatalf("Failed to write network.json: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644); err != nil {
				t.Fatalf("Failed to write platforms.json: %v", err)
			}

			loader := NewLoader(tmpDir)
			err := loader.Load()
			if tt.expectErr && err == nil {
				t.Error("Load() should fail with validation error")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Load() unexpected error: %v", err)
			}
		})
	}
}

func TestLoader_ValidateFilterRuleReferences(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name        string
		networkJSON string
		expectErr   bool
	}{
		{
			name: "invalid src prefix list in filter rule",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"filters": {
					"bad-filter": {
						"type": "ipv4",
						"rules": [{"seq": 100, "src_prefix_list": "nonexistent", "action": "permit"}]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "invalid dst prefix list in filter rule",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"filters": {
					"bad-filter": {
						"type": "ipv4",
						"rules": [{"seq": 100, "dst_prefix_list": "nonexistent", "action": "permit"}]
					}
				}
			}`,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.RemoveAll(tmpDir)
			os.MkdirAll(tmpDir, 0755)

			if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(tt.networkJSON), 0644); err != nil {
				t.Fatalf("Failed to write network.json: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644); err != nil {
				t.Fatalf("Failed to write platforms.json: %v", err)
			}

			loader := NewLoader(tmpDir)
			err := loader.Load()
			if tt.expectErr && err == nil {
				t.Error("Load() should fail with validation error")
			}
		})
	}
}

func TestLoader_ValidateProfileZoneReference(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Profile referencing unknown zone
	profileJSON := `{
		"mgmt_ip": "192.168.1.1",
		"loopback_ip": "10.0.0.1",
		"zone": "unknown-zone"
	}`
	profilePath := filepath.Join(tmpDir, "profiles", "bad-zone.json")
	if err := os.WriteFile(profilePath, []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	_, err := loader.LoadProfile("bad-zone")
	if err == nil {
		t.Error("LoadProfile() should fail when profile references unknown zone")
	}
}

func TestLoader_ValidateProfile_InvalidIPs(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	tests := []struct {
		name        string
		profileJSON string
		expectErr   bool
	}{
		{
			name:        "invalid mgmt_ip",
			profileJSON: `{"mgmt_ip": "invalid-ip", "loopback_ip": "10.0.0.1", "zone": "amer"}`,
			expectErr:   true,
		},
		{
			name:        "invalid loopback_ip",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "invalid-ip", "zone": "amer"}`,
			expectErr:   true,
		},
		{
			name:        "missing mgmt_ip",
			profileJSON: `{"loopback_ip": "10.0.0.1", "zone": "amer"}`,
			expectErr:   true,
		},
		{
			name:        "missing loopback_ip",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "zone": "amer"}`,
			expectErr:   true,
		},
		{
			name:        "missing zone",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1"}`,
			expectErr:   true,
		},
		{
			name:        "unknown zone",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1", "zone": "unknown-zone"}`,
			expectErr:   true,
		},
		{
			name:        "invalid as_number",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1", "zone": "amer", "as_number": -1}`,
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profilePath := filepath.Join(tmpDir, "profiles", "test-profile.json")
			if err := os.WriteFile(profilePath, []byte(tt.profileJSON), 0644); err != nil {
				t.Fatalf("Failed to write profile: %v", err)
			}

			// Clear cached profile
			delete(loader.profiles, "test-profile")

			_, err := loader.LoadProfile("test-profile")
			if tt.expectErr && err == nil {
				t.Error("LoadProfile() should fail with validation error")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("LoadProfile() unexpected error: %v", err)
			}
		})
	}
}

func TestLoader_GetFilter_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.GetFilter("nonexistent")
	if err == nil {
		t.Error("GetFilter() should fail for nonexistent filter")
	}
}

func TestLoader_GetPrefixList_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.GetPrefixList("nonexistent")
	if err == nil {
		t.Error("GetPrefixList() should fail for nonexistent prefix list")
	}
}

func TestLoader_ValidateQoSPolicies(t *testing.T) {
	tests := []struct {
		name        string
		networkJSON string
		expectErr   bool
	}{
		{
			name: "valid 2-queue policy",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"test": {
						"queues": [
							{"name": "be", "type": "dwrr", "weight": 80, "dscp": [0]},
							{"name": "nc", "type": "strict", "dscp": [48]}
						]
					}
				}
			}`,
			expectErr: false,
		},
		{
			name: "zero queues",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"empty": {
						"queues": []
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "too many queues (9)",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"big": {
						"queues": [
							{"name": "q0", "type": "dwrr", "weight": 10, "dscp": [0]},
							{"name": "q1", "type": "dwrr", "weight": 10, "dscp": [1]},
							{"name": "q2", "type": "dwrr", "weight": 10, "dscp": [2]},
							{"name": "q3", "type": "dwrr", "weight": 10, "dscp": [3]},
							{"name": "q4", "type": "dwrr", "weight": 10, "dscp": [4]},
							{"name": "q5", "type": "dwrr", "weight": 10, "dscp": [5]},
							{"name": "q6", "type": "dwrr", "weight": 10, "dscp": [6]},
							{"name": "q7", "type": "dwrr", "weight": 10, "dscp": [7]},
							{"name": "q8", "type": "dwrr", "weight": 10, "dscp": [8]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "duplicate DSCP",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"dup": {
						"queues": [
							{"name": "be", "type": "dwrr", "weight": 50, "dscp": [0, 10]},
							{"name": "nc", "type": "strict", "dscp": [10]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "DSCP out of range",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "be", "type": "dwrr", "weight": 50, "dscp": [64]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "invalid queue type",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "be", "type": "wrr", "weight": 50, "dscp": [0]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "dwrr without weight",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "be", "type": "dwrr", "dscp": [0]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "strict with weight",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "nc", "type": "strict", "weight": 10, "dscp": [48]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "duplicate queue name",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "be", "type": "dwrr", "weight": 50, "dscp": [0]},
							{"name": "be", "type": "strict", "dscp": [48]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "empty queue name",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {},
				"qos_policies": {
					"bad": {
						"queues": [
							{"name": "", "type": "dwrr", "weight": 50, "dscp": [0]}
						]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "service references nonexistent qos_policy",
			networkJSON: `{
				"version": "1.0",
				"zones": {},
				"services": {
					"bad-svc": {
						"service_type": "routed",
						"qos_policy": "nonexistent"
					}
				},
				"qos_policies": {}
			}`,
			expectErr: true,
		},
	}

	tmpDir, err := os.MkdirTemp("", "newtron-qos-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.RemoveAll(tmpDir)
			os.MkdirAll(tmpDir, 0755)

			if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(tt.networkJSON), 0644); err != nil {
				t.Fatalf("Failed to write network.json: %v", err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644); err != nil {
				t.Fatalf("Failed to write platforms.json: %v", err)
			}

			loader := NewLoader(tmpDir)
			err := loader.Load()
			if tt.expectErr && err == nil {
				t.Error("Load() should fail with validation error")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Load() unexpected error: %v", err)
			}
		})
	}
}
