package spec

import "testing"

func TestFeatureDependencies(t *testing.T) {
	// VPP platform has evpn-vxlan unsupported
	vpp := &PlatformSpec{
		UnsupportedFeatures: []string{"acl", "evpn-vxlan"},
	}

	tests := []struct {
		name     string
		platform *PlatformSpec
		feature  string
		want     bool
	}{
		// VPP: directly unsupported
		{"vpp-acl", vpp, "acl", false},
		{"vpp-evpn-vxlan", vpp, "evpn-vxlan", false},
		
		// VPP: unsupported via dependency
		{"vpp-macvpn", vpp, "macvpn", false},
		{"vpp-ipvpn", vpp, "ipvpn", false},
		
		// CiscoVS: all supported
		{"ciscovs-acl", &PlatformSpec{}, "acl", true},
		{"ciscovs-evpn-vxlan", &PlatformSpec{}, "evpn-vxlan", true},
		{"ciscovs-macvpn", &PlatformSpec{}, "macvpn", true},
		{"ciscovs-ipvpn", &PlatformSpec{}, "ipvpn", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.platform.SupportsFeature(tt.feature)
			if got != tt.want {
				t.Errorf("SupportsFeature(%q) = %v, want %v", tt.feature, got, tt.want)
			}
		})
	}
}

func TestGetUnsupportedDueTo(t *testing.T) {
	affected := GetUnsupportedDueTo("evpn-vxlan")
	
	// Should include macvpn and ipvpn (both depend on evpn-vxlan)
	expectedCount := 2
	if len(affected) != expectedCount {
		t.Errorf("GetUnsupportedDueTo(evpn-vxlan) returned %d features, want %d: %v", 
			len(affected), expectedCount, affected)
	}

	// Check that macvpn and ipvpn are in the list
	hasMAC := false
	hasIP := false
	for _, f := range affected {
		if f == "macvpn" {
			hasMAC = true
		}
		if f == "ipvpn" {
			hasIP = true
		}
	}

	if !hasMAC {
		t.Error("GetUnsupportedDueTo(evpn-vxlan) should include macvpn")
	}
	if !hasIP {
		t.Error("GetUnsupportedDueTo(evpn-vxlan) should include ipvpn")
	}
}

func TestGetFeatureDependencies(t *testing.T) {
	tests := []struct {
		feature string
		want    []string
	}{
		{"macvpn", []string{"evpn-vxlan"}},
		{"ipvpn", []string{"evpn-vxlan"}},
		{"evpn-vxlan", []string{}},
		{"acl", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.feature, func(t *testing.T) {
			got := GetFeatureDependencies(tt.feature)
			if len(got) != len(tt.want) {
				t.Errorf("GetFeatureDependencies(%q) = %v, want %v", tt.feature, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("GetFeatureDependencies(%q) = %v, want %v", tt.feature, got, tt.want)
					break
				}
			}
		})
	}
}

func TestFeatureIndependence(t *testing.T) {
	// Platform where VXLAN works but only L2 EVPN (macvpn) is broken
	platformA := &PlatformSpec{
		UnsupportedFeatures: []string{"macvpn"},
	}

	// Platform where VXLAN works but only L3 EVPN (ipvpn) is broken
	platformB := &PlatformSpec{
		UnsupportedFeatures: []string{"ipvpn"},
	}

	// Platform where VXLAN itself doesn't work (everything broken)
	vpp := &PlatformSpec{
		UnsupportedFeatures: []string{"evpn-vxlan"},
	}

	tests := []struct {
		name     string
		platform *PlatformSpec
		checks   map[string]bool
	}{
		{
			name:     "Platform A - L2 EVPN broken, L3 works",
			platform: platformA,
			checks: map[string]bool{
				"evpn-vxlan": true,  // VXLAN dataplane works
				"macvpn":     false, // L2 EVPN broken
				"ipvpn":      true,  // L3 EVPN works
			},
		},
		{
			name:     "Platform B - L3 EVPN broken, L2 works",
			platform: platformB,
			checks: map[string]bool{
				"evpn-vxlan": true,  // VXLAN dataplane works
				"macvpn":     true,  // L2 EVPN works
				"ipvpn":      false, // L3 EVPN broken
			},
		},
		{
			name:     "VPP - VXLAN broken (all cascade)",
			platform: vpp,
			checks: map[string]bool{
				"evpn-vxlan": false, // VXLAN dataplane broken
				"macvpn":     false, // Cascaded from evpn-vxlan
				"ipvpn":      false, // Cascaded from evpn-vxlan
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for feature, want := range tt.checks {
				got := tt.platform.SupportsFeature(feature)
				if got != want {
					t.Errorf("%s: SupportsFeature(%q) = %v, want %v",
						tt.name, feature, got, want)
				}
			}
		})
	}
}
