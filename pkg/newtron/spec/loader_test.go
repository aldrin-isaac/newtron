package spec

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Helper to create test network directory with files
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
			"CUSTOMER": {
				"l3vni": 10001,
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
				"ipvpn": "CUSTOMER",
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
	profilesDir := filepath.Join(tmpDir, "nodes")
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

	loader := NewLoader(tmpDir, nil)
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

	// Platforms loading moved out of the per-network Loader — the
	// global registry is owned by cmd/newt-server (LoadPlatformsFromDir
	// + ResolvePlatformSecrets, covered by their own tests).
}

func TestLoader_LoadProfile(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
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

	loader := NewLoader(tmpDir, nil)
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

// TestLoader_LoadProfile_ConcurrentSameKey pins the cache mutex: N goroutines
// calling LoadProfile for the same key under the race detector must complete
// without "concurrent map read and map write" panics. Pre-mutex this test
// reliably failed under `go test -race` because the cache write in
// LoadProfile mutated l.profiles without coordination.
func TestLoader_LoadProfile_ConcurrentSameKey(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	got := make([]*DeviceProfile, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			p, err := loader.LoadProfile("leaf1-ny")
			if err != nil {
				t.Errorf("goroutine %d: LoadProfile failed: %v", i, err)
				return
			}
			got[i] = p
		}(i)
	}
	wg.Wait()

	// All goroutines should observe the same cached pointer once the dust
	// settles — the double-check pattern guarantees exactly one publish.
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Errorf("goroutine %d saw a different pointer than goroutine 0 — cache double-check failed", i)
		}
	}
}

// TestLoader_LoadProfile_ConcurrentMixedKeys exercises the cache mutex with
// different keys racing simultaneously. Same regression target — concurrent
// map writes panic under -race without the mutex.
func TestLoader_LoadProfile_ConcurrentMixedKeys(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Profiles present in createTestSpecDir; if more get added there, this
	// test trivially still passes.
	keys := []string{"leaf1-ny", "leaf2-ny", "spine1-ny"}
	const rounds = 8
	var wg sync.WaitGroup
	for r := 0; r < rounds; r++ {
		for _, k := range keys {
			wg.Add(1)
			go func(k string) {
				defer wg.Done()
				if _, err := loader.LoadProfile(k); err != nil {
					// Profile may not exist in fixture; that's fine — we
					// only care about the absence of races, not that every
					// key resolves.
					_ = err
				}
			}(k)
		}
	}
	wg.Wait()
}

func TestLoader_LoadProfile_NotFound(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	_, err := loader.LoadProfile("nonexistent")
	if err == nil {
		t.Error("LoadProfile() should fail for nonexistent profile")
	}
}

func TestLoader_DefaultDir(t *testing.T) {
	// Test that empty string uses default
	loader := NewLoader("", nil)
	if loader.specDir != Dir {
		t.Errorf("Empty specDir should use default %q, got %q", Dir, loader.specDir)
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

	loader := NewLoader(tmpDir, nil)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail with invalid filter reference")
	}
}

// TestLoader_LoadEmptyDir pins that a directory with neither network.json nor
// topology.json is rejected — it is not a network. (network.json alone is a
// scaffolded/offline network; topology.json alone is a lab-only network; both
// load. Only the empty-directory case errors.)
func TestLoader_LoadEmptyDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err == nil {
		t.Error("Load() should fail when neither network.json nor topology.json is present")
	}
}

// TestLoader_LabOnlyTopology pins that a directory with a topology.json but no
// network.json loads as a lab-only network with an empty spec rather than
// erroring. These are deploy-only topologies — newtlab spins up the VMs from
// the topology, node profiles, and global platforms while an external system
// (e.g. the vJunos topologies configured by netconf.pl) owns device config.
// network.json is optional, symmetric with the already-optional topology.json.
func TestLoader_LabOnlyTopology(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-labonly-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// topology.json + a node profile, but deliberately NO network.json.
	topo := `{"version":"1.0","devices":{"r1":{}},"links":[]}`
	if err := os.WriteFile(filepath.Join(tmpDir, "topology.json"), []byte(topo), 0644); err != nil {
		t.Fatalf("Failed to write topology.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "nodes"), 0755); err != nil {
		t.Fatalf("Failed to create nodes dir: %v", err)
	}
	node := `{"platform":"vjunos-router","zone":"lab"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "nodes", "r1.json"), []byte(node), 0644); err != nil {
		t.Fatalf("Failed to write node profile: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() with no network.json should succeed (lab-only); got: %v", err)
	}
	net := loader.GetNetwork()
	if net == nil {
		t.Fatal("GetNetwork() returned nil; want empty NetworkSpecFile")
	}
	if len(net.Services) != 0 || len(net.Zones) != 0 {
		t.Errorf("lab-only network should have empty spec; got %d services, %d zones",
			len(net.Services), len(net.Zones))
	}
}

// TestLoader_LoadInvalidJSON pins the failure path for malformed
// network.json. Platforms.json is no longer this loader's concern
// (global registry; LoadPlatformsFromDir has its own coverage).
func TestLoader_LoadInvalidJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "newtron-spec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte("invalid json {"), 0644); err != nil {
		t.Fatalf("Failed to write network.json: %v", err)
	}
	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err == nil {
		t.Error("Load() should fail with invalid network.json")
	}
}

func TestLoader_LoadProfile_InvalidJSON(t *testing.T) {
	tmpDir := createTestSpecDir(t)
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Create profile with invalid JSON
	profilePath := filepath.Join(tmpDir, "nodes", "bad-profile.json")
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

			loader := NewLoader(tmpDir, nil)
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

			loader := NewLoader(tmpDir, nil)
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

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Profile referencing unknown zone
	profileJSON := `{
		"mgmt_ip": "192.168.1.1",
		"loopback_ip": "10.0.0.1",
		"zone": "unknown-zone"
	}`
	profilePath := filepath.Join(tmpDir, "nodes", "bad-zone.json")
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

	loader := NewLoader(tmpDir, nil)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profilePath := filepath.Join(tmpDir, "nodes", "test-profile.json")
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

			loader := NewLoader(tmpDir, nil)
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

// ============================================================================
// Hierarchical Spec Resolution Tests
// ============================================================================

func TestLoader_ZoneLevelServiceRefsNetworkFilter(t *testing.T) {
	// Zone-level service references a network-level filter — should pass validation
	tmpDir, err := os.MkdirTemp("", "newtron-hierarchy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	networkJSON := `{
		"version": "1.0",
		"zones": {
			"amer": {
				"as_number": 65000,
				"services": {
					"zone-svc": {
						"description": "Zone-level service using network filter",
						"service_type": "routed",
						"ingress_filter": "net-filter"
					}
				}
			}
		},
		"filters": {
			"net-filter": {
				"description": "Network-level filter",
				"type": "ipv4",
				"rules": [{"seq": 100, "action": "permit"}]
			}
		},
		"services": {}
	}`
	os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644)
	os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() should pass: zone service refs network filter, got: %v", err)
	}
}

func TestLoader_ZoneLevelServiceRefsMissing(t *testing.T) {
	// Zone-level service references a nonexistent filter — should fail
	tmpDir, err := os.MkdirTemp("", "newtron-hierarchy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	networkJSON := `{
		"version": "1.0",
		"zones": {
			"amer": {
				"as_number": 65000,
				"services": {
					"zone-svc": {
						"description": "Zone service with bad ref",
						"service_type": "routed",
						"ingress_filter": "nonexistent-filter"
					}
				}
			}
		},
		"services": {}
	}`
	os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644)
	os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644)

	loader := NewLoader(tmpDir, nil)
	err = loader.Load()
	if err == nil {
		t.Error("Load() should fail: zone service references nonexistent filter")
	}
}

func TestLoader_ZoneLevelFilterRefsPrefixList(t *testing.T) {
	// Zone-level filter references a network-level prefix list — should pass
	tmpDir, err := os.MkdirTemp("", "newtron-hierarchy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	networkJSON := `{
		"version": "1.0",
		"zones": {
			"amer": {
				"as_number": 65000,
				"filters": {
					"zone-filter": {
						"description": "Zone filter using network prefix list",
						"type": "ipv4",
						"rules": [{"seq": 100, "src_prefix_list": "rfc1918", "action": "deny"}]
					}
				}
			}
		},
		"prefix_lists": {
			"rfc1918": ["10.0.0.0/8"]
		},
		"services": {}
	}`
	os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644)
	os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() should pass: zone filter refs network prefix list, got: %v", err)
	}
}

func TestLoader_ZoneLevelServiceRefsZoneIPVPN(t *testing.T) {
	// Zone-level service references a zone-level IPVPN — should pass
	tmpDir, err := os.MkdirTemp("", "newtron-hierarchy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	networkJSON := `{
		"version": "1.0",
		"zones": {
			"amer": {
				"as_number": 65000,
				"ipvpns": {
					"ZONE": {
						"l3vni": 20001,
						"route_targets": ["65000:200"]
					}
				},
				"services": {
					"zone-l3": {
						"description": "Zone L3 service",
						"service_type": "evpn-routed",
						"ipvpn": "ZONE",
						"vrf_type": "interface"
					}
				}
			}
		},
		"services": {}
	}`
	os.WriteFile(filepath.Join(tmpDir, "network.json"), []byte(networkJSON), 0644)
	os.WriteFile(filepath.Join(tmpDir, "platforms.json"), []byte(`{"version": "1.0", "platforms": {}}`), 0644)

	loader := NewLoader(tmpDir, nil)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load() should pass: zone service refs zone ipvpn, got: %v", err)
	}
}
