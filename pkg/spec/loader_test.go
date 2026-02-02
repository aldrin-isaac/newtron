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
		"lock_dir": "/var/lock",
		"super_users": ["admin"],
		"regions": {
			"amer": {
				"as_number": 65000,
				"affinity": "east"
			}
		},
		"prefix_lists": {
			"rfc1918": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
		},
		"filter_specs": {
			"test-filter": {
				"description": "Test filter",
				"type": "L3",
				"rules": [
					{"seq": 100, "action": "permit"}
				]
			}
		},
		"policers": {
			"test-policer": {
				"bandwidth": "10m",
				"burst": "1m"
			}
		},
		"ipvpn": {
			"customer-vpn": {
				"l3_vni": 10001,
				"import_rt": ["65000:100"],
				"export_rt": ["65000:100"]
			}
		},
		"macvpn": {
			"server-vlan": {
				"vlan": 100,
				"l2_vni": 1100
			}
		},
		"services": {
			"customer-l3": {
				"description": "Customer L3 service",
				"service_type": "l3",
				"ipvpn": "customer-vpn",
				"vrf_type": "interface"
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Create site.json
	siteJSON := `{
		"version": "1.0",
		"sites": {
			"ny": {
				"region": "amer",
				"route_reflectors": ["spine1-ny", "spine2-ny"]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
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
		"site": "ny",
		"platform": "as7726"
	}`
	if err := os.WriteFile(filepath.Join(profilesDir, "leaf1-ny.json"), []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	// Create spine profile for route reflector lookup
	spineProfileJSON := `{
		"mgmt_ip": "192.168.1.1",
		"loopback_ip": "10.0.0.1",
		"site": "ny",
		"is_route_reflector": true
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
	if len(network.Regions) != 1 {
		t.Errorf("Expected 1 region, got %d", len(network.Regions))
	}

	// Check site spec loaded
	site := loader.GetSite()
	if site == nil {
		t.Fatal("GetSite() returned nil")
	}
	if len(site.Sites) != 1 {
		t.Errorf("Expected 1 site, got %d", len(site.Sites))
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
	if profile.Site != "ny" {
		t.Errorf("Site = %q, want %q", profile.Site, "ny")
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

func TestLoader_ResolveProfile(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	resolved, err := loader.ResolveProfile("leaf1-ny")
	if err != nil {
		t.Fatalf("ResolveProfile() failed: %v", err)
	}

	// Check inherited values
	if resolved.ASNumber != 65000 {
		t.Errorf("ASNumber = %d, want %d (inherited from region)", resolved.ASNumber, 65000)
	}
	if resolved.Region != "amer" {
		t.Errorf("Region = %q, want %q (derived from site)", resolved.Region, "amer")
	}

	// Check derived values
	if resolved.RouterID != "10.0.0.10" {
		t.Errorf("RouterID = %q, want %q (derived from loopback)", resolved.RouterID, "10.0.0.10")
	}
	if resolved.VTEPSourceIP != "10.0.0.10" {
		t.Errorf("VTEPSourceIP = %q, want %q", resolved.VTEPSourceIP, "10.0.0.10")
	}

	// Check defaults
	if !resolved.IsRouter {
		t.Error("IsRouter should default to true")
	}
	if !resolved.IsBridge {
		t.Error("IsBridge should default to true")
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
	if svc.ServiceType != "l3" {
		t.Errorf("ServiceType = %q, want %q", svc.ServiceType, "l3")
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

func TestLoader_GetFilterSpec(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	filter, err := loader.GetFilterSpec("test-filter")
	if err != nil {
		t.Fatalf("GetFilterSpec() failed: %v", err)
	}
	if filter.Type != "L3" {
		t.Errorf("Filter type = %q, want %q", filter.Type, "L3")
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

func TestLoader_GetPolicer(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	policer, err := loader.GetPolicer("test-policer")
	if err != nil {
		t.Fatalf("GetPolicer() failed: %v", err)
	}
	if policer.Bandwidth != "10m" {
		t.Errorf("Bandwidth = %q, want %q", policer.Bandwidth, "10m")
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

func TestLoader_ListRegions(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	regions := loader.ListRegions()
	if len(regions) != 1 {
		t.Errorf("Expected 1 region, got %d", len(regions))
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
		"regions": {},
		"services": {
			"bad-service": {
				"description": "Bad service",
				"service_type": "l3",
				"ingress_filter": "nonexistent-filter"
			}
		},
		"filter_specs": {}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Create site.json
	siteJSON := `{"version": "1.0", "sites": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
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

func TestLoader_LoadMissingSiteSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create network.json only
	networkJSON := `{"version": "1.0", "regions": {}, "services": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	loader := NewLoader(tmpDir)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail when site.json is missing")
	}
}

func TestLoader_LoadMissingPlatformSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create network.json and site.json only
	networkJSON := `{"version": "1.0", "regions": {}, "services": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}
	siteJSON := `{"version": "1.0", "sites": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
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
			name:    "invalid site.json",
			file:    "site.json",
			content: "invalid json {",
			setup: func() {
				os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(`{"version": "1.0", "regions": {}, "services": {}}`), 0644)
			},
		},
		{
			name:    "invalid platforms.json",
			file:    "platforms.json",
			content: "invalid json {",
			setup: func() {
				os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(`{"version": "1.0", "regions": {}, "services": {}}`), 0644)
				os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(`{"version": "1.0", "sites": {}}`), 0644)
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

func TestLoader_ResolveProfile_MissingSite(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Create profile with non-existent site
	profileJSON := `{
		"mgmt_ip": "192.168.1.100",
		"loopback_ip": "10.0.0.100",
		"site": "nonexistent-site"
	}`
	profilePath := filepath.Join(tmpDir, "profiles", "bad-site-device.json")
	if err := os.WriteFile(profilePath, []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	_, err := loader.ResolveProfile("bad-site-device")
	if err == nil {
		t.Error("ResolveProfile() should fail with missing site")
	}
}

func TestLoader_ResolveProfile_MissingRegion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create network.json without the region
	networkJSON := `{"version": "1.0", "regions": {}, "services": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Create site.json with site referencing non-existent region
	siteJSON := `{
		"version": "1.0",
		"sites": {
			"test-site": {
				"region": "nonexistent-region",
				"route_reflectors": []
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
	}

	platformsJSON := `{"version": "1.0", "platforms": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(platformsJSON), 0644); err != nil {
		t.Fatalf("Failed to write platforms.json: %v", err)
	}

	// Create profiles directory and profile
	profilesDir := filepath.Join(tmpDir, "profiles")
	os.MkdirAll(profilesDir, 0755)
	profileJSON := `{
		"mgmt_ip": "192.168.1.100",
		"loopback_ip": "10.0.0.100",
		"site": "test-site"
	}`
	if err := os.WriteFile(filepath.Join(profilesDir, "test-device.json"), []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	loader := NewLoader(tmpDir)
	// Load will fail because site references unknown region - that's OK for this test
	// We need to bypass validation to test ResolveProfile's region check
	loader.network, _ = loader.loadNetworkSpec()
	loader.site, _ = loader.loadSiteSpec()
	loader.platforms, _ = loader.loadPlatformSpec()

	_, err = loader.ResolveProfile("test-device")
	if err == nil {
		t.Error("ResolveProfile() should fail when region is not found")
	}
}

func TestLoader_ResolveProfile_WithOverrides(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Create profile with explicit overrides
	asNum := 65100
	isRouter := false
	isBridge := false
	profileJSON := `{
		"mgmt_ip": "192.168.1.200",
		"loopback_ip": "10.0.0.200",
		"site": "ny",
		"as_number": 65100,
		"affinity": "custom-affinity",
		"is_router": false,
		"is_bridge": false,
		"is_border_router": true,
		"is_route_reflector": true
	}`
	profilePath := filepath.Join(tmpDir, "profiles", "override-device.json")
	if err := os.WriteFile(profilePath, []byte(profileJSON), 0644); err != nil {
		t.Fatalf("Failed to write profile: %v", err)
	}

	resolved, err := loader.ResolveProfile("override-device")
	if err != nil {
		t.Fatalf("ResolveProfile() failed: %v", err)
	}

	// Check that overrides are applied
	if resolved.ASNumber != asNum {
		t.Errorf("ASNumber = %d, want %d (from profile override)", resolved.ASNumber, asNum)
	}
	if resolved.Affinity != "custom-affinity" {
		t.Errorf("Affinity = %q, want %q", resolved.Affinity, "custom-affinity")
	}
	if resolved.IsRouter != isRouter {
		t.Errorf("IsRouter = %v, want %v", resolved.IsRouter, isRouter)
	}
	if resolved.IsBridge != isBridge {
		t.Errorf("IsBridge = %v, want %v", resolved.IsBridge, isBridge)
	}
	if !resolved.IsBorderRouter {
		t.Error("IsBorderRouter should be true")
	}
	if !resolved.IsRouteReflector {
		t.Error("IsRouteReflector should be true")
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
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l3",
						"egress_filter": "nonexistent-filter"
					}
				},
				"filter_specs": {}
			}`,
			expectErr: true,
		},
		{
			name: "invalid qos profile",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l3",
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
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l3",
						"ipvpn": "nonexistent-vpn"
					}
				},
				"ipvpn": {}
			}`,
			expectErr: true,
		},
		{
			name: "invalid macvpn reference",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l2",
						"macvpn": "nonexistent-vpn"
					}
				},
				"macvpn": {}
			}`,
			expectErr: true,
		},
		{
			name: "l2 service without macvpn",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l2"
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "l3 service with vrf_type but no ipvpn",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "l3",
						"vrf_type": "interface"
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "irb service without macvpn",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {
					"bad-service": {
						"service_type": "irb"
					}
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
			if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(`{"version": "1.0", "sites": {}}`), 0644); err != nil {
				t.Fatalf("Failed to write site.json: %v", err)
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
				"regions": {},
				"services": {},
				"filter_specs": {
					"bad-filter": {
						"type": "L3",
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
				"regions": {},
				"services": {},
				"filter_specs": {
					"bad-filter": {
						"type": "L3",
						"rules": [{"seq": 100, "dst_prefix_list": "nonexistent", "action": "permit"}]
					}
				}
			}`,
			expectErr: true,
		},
		{
			name: "invalid policer in filter rule",
			networkJSON: `{
				"version": "1.0",
				"regions": {},
				"services": {},
				"filter_specs": {
					"bad-filter": {
						"type": "L3",
						"rules": [{"seq": 100, "policer": "nonexistent", "action": "permit"}]
					}
				},
				"policers": {}
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
			if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(`{"version": "1.0", "sites": {}}`), 0644); err != nil {
				t.Fatalf("Failed to write site.json: %v", err)
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

func TestLoader_ValidateSiteRegionReference(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Network without the referenced region
	networkJSON := `{"version": "1.0", "regions": {}, "services": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}

	// Site referencing unknown region
	siteJSON := `{
		"version": "1.0",
		"sites": {
			"bad-site": {
				"region": "unknown-region",
				"route_reflectors": []
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644); err != nil {
		t.Fatalf("Failed to write platforms.json: %v", err)
	}

	loader := NewLoader(tmpDir)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail when site references unknown region")
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
			profileJSON: `{"mgmt_ip": "invalid-ip", "loopback_ip": "10.0.0.1", "site": "ny"}`,
			expectErr:   true,
		},
		{
			name:        "invalid loopback_ip",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "invalid-ip", "site": "ny"}`,
			expectErr:   true,
		},
		{
			name:        "missing mgmt_ip",
			profileJSON: `{"loopback_ip": "10.0.0.1", "site": "ny"}`,
			expectErr:   true,
		},
		{
			name:        "missing loopback_ip",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "site": "ny"}`,
			expectErr:   true,
		},
		{
			name:        "missing site",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1"}`,
			expectErr:   true,
		},
		{
			name:        "unknown site",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1", "site": "unknown-site"}`,
			expectErr:   true,
		},
		{
			name:        "invalid as_number",
			profileJSON: `{"mgmt_ip": "192.168.1.1", "loopback_ip": "10.0.0.1", "site": "ny", "as_number": -1}`,
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

func TestLoader_GetFilterSpec_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.GetFilterSpec("nonexistent")
	if err == nil {
		t.Error("GetFilterSpec() should fail for nonexistent filter")
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

func TestLoader_GetPolicer_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.GetPolicer("nonexistent")
	if err == nil {
		t.Error("GetPolicer() should fail for nonexistent policer")
	}
}

func TestLoader_DeriveBGPNeighbors_MissingProfile(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	// Remove one of the spine profiles to test the skip path
	os.Remove(filepath.Join(tmpDir, "profiles", "spine2-ny.json"))

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	resolved, err := loader.ResolveProfile("leaf1-ny")
	if err != nil {
		t.Fatalf("ResolveProfile() failed: %v", err)
	}

	// Should only have one BGP neighbor (spine1-ny), not spine2-ny
	if len(resolved.BGPNeighbors) != 1 {
		t.Errorf("Expected 1 BGP neighbor (missing profile skipped), got %d", len(resolved.BGPNeighbors))
	}
}

func TestLoader_DeriveBGPNeighbors_SelfPeering(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	// Modify site.json to include spine1-ny as its own route reflector
	siteJSON := `{
		"version": "1.0",
		"sites": {
			"ny": {
				"region": "amer",
				"route_reflectors": ["spine1-ny"]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "site.json"), []byte(siteJSON), 0644); err != nil {
		t.Fatalf("Failed to write site.json: %v", err)
	}

	loader := NewLoader(tmpDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Resolve spine1-ny which is listed as its own route reflector
	resolved, err := loader.ResolveProfile("spine1-ny")
	if err != nil {
		t.Fatalf("ResolveProfile() failed: %v", err)
	}

	// Should have no BGP neighbors since the only RR is itself
	if len(resolved.BGPNeighbors) != 0 {
		t.Errorf("Expected 0 BGP neighbors (self-peering excluded), got %d: %v", len(resolved.BGPNeighbors), resolved.BGPNeighbors)
	}
}
