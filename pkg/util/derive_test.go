package util

import (
	"reflect"
	"testing"
)

func TestDeriveFromInterface(t *testing.T) {
	tests := []struct {
		name        string
		intf        string
		ipWithMask  string
		serviceName string // expected to be pre-normalized (uppercase, underscores)
		wantErr     bool
		checkVRF    string
		checkACL    string
	}{
		{
			name:        "basic interface",
			intf:        "Ethernet0",
			ipWithMask:  "10.1.1.1/30",
			serviceName: "CUSTOMER",
			wantErr:     false,
			checkVRF:    "CUSTOMER_ETH0",
			checkACL:    "CUSTOMER_ETH0",
		},
		{
			name:        "port channel",
			intf:        "PortChannel100",
			ipWithMask:  "10.1.1.1/30",
			serviceName: "TRANSIT",
			wantErr:     false,
			checkVRF:    "TRANSIT_PO100",
			checkACL:    "TRANSIT_PO100",
		},
		{
			name:        "no IP",
			intf:        "Ethernet0",
			ipWithMask:  "",
			serviceName: "L2_ONLY",
			wantErr:     false,
			checkVRF:    "L2_ONLY_ETH0",
		},
		{
			name:        "invalid IP",
			intf:        "Ethernet0",
			ipWithMask:  "invalid",
			serviceName: "TEST",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveFromInterface(tt.intf, tt.ipWithMask, tt.serviceName)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeriveFromInterface() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.VRFName != tt.checkVRF {
					t.Errorf("DeriveFromInterface() VRFName = %q, want %q", got.VRFName, tt.checkVRF)
				}
				if tt.checkACL != "" && got.ACLPrefix != tt.checkACL {
					t.Errorf("DeriveFromInterface() ACLPrefix = %q, want %q", got.ACLPrefix, tt.checkACL)
				}
			}
		})
	}
}

func TestSanitizeForName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "Ethernet0", "Ethernet0"},
		{"with dot", "Ethernet0.100", "Ethernet0_100"},
		{"with slash", "Ethernet0/1", "Ethernet0_1"},
		{"special chars", "test@#$%123", "test123"},
		{"already clean", "PortChannel100", "PortChannel100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeForName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeForName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeriveVRFName(t *testing.T) {
	tests := []struct {
		vrfType       string
		serviceName   string // pre-normalized
		interfaceName string
		want          string
	}{
		{"interface", "CUSTOMER", "Ethernet0", "CUSTOMER_ETH0"},
		{"shared", "CUSTOMER", "Ethernet0", "CUSTOMER"},
		{"", "CUSTOMER", "Ethernet0", "CUSTOMER_ETH0"}, // default to interface
		{"interface", "TRANSIT", "PortChannel100", "TRANSIT_PO100"},
	}

	for _, tt := range tests {
		t.Run(tt.vrfType+"-"+tt.interfaceName, func(t *testing.T) {
			got := DeriveVRFName(tt.vrfType, tt.serviceName, tt.interfaceName)
			if got != tt.want {
				t.Errorf("DeriveVRFName(%q, %q, %q) = %q, want %q",
					tt.vrfType, tt.serviceName, tt.interfaceName, got, tt.want)
			}
		})
	}
}

func TestContentHash(t *testing.T) {
	// Deterministic: same fields → same hash
	entries := []map[string]string{
		{"PRIORITY": "9990", "PACKET_ACTION": "FORWARD", "SRC_IP": "10.0.0.0/8"},
	}
	h1 := ContentHash(entries)
	h2 := ContentHash(entries)
	if h1 != h2 {
		t.Errorf("ContentHash not deterministic: %q != %q", h1, h2)
	}

	// 8 hex characters
	if len(h1) != 8 {
		t.Errorf("ContentHash length = %d, want 8", len(h1))
	}

	// Different content → different hash
	entries2 := []map[string]string{
		{"PRIORITY": "9990", "PACKET_ACTION": "DROP", "SRC_IP": "10.0.0.0/8"},
	}
	h3 := ContentHash(entries2)
	if h1 == h3 {
		t.Errorf("different content should produce different hash")
	}

	// Field order doesn't matter (sorted keys)
	entries3 := []map[string]string{
		{"SRC_IP": "10.0.0.0/8", "PACKET_ACTION": "FORWARD", "PRIORITY": "9990"},
	}
	h4 := ContentHash(entries3)
	if h1 != h4 {
		t.Errorf("ContentHash should be order-independent: %q != %q", h1, h4)
	}

	// Empty entries
	h5 := ContentHash(nil)
	if len(h5) != 8 {
		t.Errorf("ContentHash(nil) length = %d, want 8", len(h5))
	}
}

func TestDeriveACLName(t *testing.T) {
	tests := []struct {
		filterName  string // pre-normalized
		direction   string
		contentHash string
		want        string
	}{
		{"PROTECT_RE", "in", "1ED5F2C7", "PROTECT_RE_IN_1ED5F2C7"},
		{"PROTECT_RE", "out", "A1B2C3D4", "PROTECT_RE_OUT_A1B2C3D4"},
		{"EDGE_FILTER", "in", "5F2A8B3E", "EDGE_FILTER_IN_5F2A8B3E"},
	}

	for _, tt := range tests {
		got := DeriveACLName(tt.filterName, tt.direction, tt.contentHash)
		if got != tt.want {
			t.Errorf("DeriveACLName(%q, %q, %q) = %q, want %q", tt.filterName, tt.direction, tt.contentHash, got, tt.want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"transit", "TRANSIT"},
		{"customer-edge", "CUSTOMER_EDGE"},
		{"PROTECT-RE", "PROTECT_RE"},
		{"l2-only", "L2_ONLY"},
		{"already_UPPER", "ALREADY_UPPER"},
		{"mixed-Case_Name", "MIXED_CASE_NAME"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeVRFName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Vrf_irb", "Vrf_IRB"},
		{"Vrf_l3evpn", "Vrf_L3EVPN"},
		{"Vrf_CUST1", "Vrf_CUST1"},
		{"Vrf_customer-edge", "Vrf_CUSTOMER_EDGE"},
		{"myVrf", "Vrf_MYVRF"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeVRFName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeVRFName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInterfaceName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantType   string
		wantNum    string
		wantSubint string
	}{
		{"ethernet", "Ethernet0", "Ethernet", "0", ""},
		{"ethernet with subint", "Ethernet0.100", "Ethernet", "0", "100"},
		{"port channel", "PortChannel100", "PortChannel", "100", ""},
		{"loopback", "Loopback0", "Loopback", "0", ""},
		{"vlan", "Vlan100", "Vlan", "100", ""},
		{"with slot", "Ethernet0/1", "Ethernet", "0/1", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotNum, gotSubint := ParseInterfaceName(tt.input)
			if gotType != tt.wantType || gotNum != tt.wantNum || gotSubint != tt.wantSubint {
				t.Errorf("ParseInterfaceName(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.input, gotType, gotNum, gotSubint, tt.wantType, tt.wantNum, tt.wantSubint)
			}
		})
	}
}

func TestShortenInterfaceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Ethernet0", "Eth0"},
		{"Ethernet0.100", "Eth0.100"},
		{"PortChannel100", "Po100"},
		{"Loopback0", "Lo0"},
		{"Vlan100", "Vl100"},
		{"Management0", "Mgmt0"},
		{"Unknown0", "Unknown0"}, // No mapping, return sanitized
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ShortenInterfaceName(tt.input)
			if got != tt.want {
				t.Errorf("ShortenInterfaceName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeInterfaceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"eth0", "Ethernet0"},
		{"Eth0", "Ethernet0"},
		{"ETH0", "Ethernet0"},
		{"po100", "PortChannel100"},
		{"Po100", "PortChannel100"},
		{"lo0", "Loopback0"},
		{"vl100", "Vlan100"},
		{"vlan100", "Vlan100"},
		{"mgmt0", "Management0"},
		{"Ethernet0", "Ethernet0"}, // Already normalized
		{"PortChannel100", "PortChannel100"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeInterfaceName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeInterfaceName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMergeMaps(t *testing.T) {
	tests := []struct {
		name string
		maps []map[string]string
		want map[string]string
	}{
		{
			name: "override",
			maps: []map[string]string{
				{"a": "1", "b": "2"},
				{"b": "3", "c": "4"},
			},
			want: map[string]string{"a": "1", "b": "3", "c": "4"},
		},
		{
			name: "nil map",
			maps: []map[string]string{
				{"a": "1"},
				nil,
				{"b": "2"},
			},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "empty",
			maps: []map[string]string{},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeMaps(tt.maps...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeMaps() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseInterfaceName_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantType   string
		wantNum    string
		wantSubint string
	}{
		{"invalid no number", "Ethernet", "Ethernet", "", ""},
		{"just numbers", "123", "123", "", ""},
		{"empty", "", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotNum, gotSubint := ParseInterfaceName(tt.input)
			if gotType != tt.wantType || gotNum != tt.wantNum || gotSubint != tt.wantSubint {
				t.Errorf("ParseInterfaceName(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.input, gotType, gotNum, gotSubint, tt.wantType, tt.wantNum, tt.wantSubint)
			}
		})
	}
}
