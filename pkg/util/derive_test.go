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
		serviceName string
		wantErr     bool
		checkVRF    string
		checkACL    string
	}{
		{
			name:        "basic interface",
			intf:        "Ethernet0",
			ipWithMask:  "10.1.1.1/30",
			serviceName: "customer",
			wantErr:     false,
			checkVRF:    "customer-Eth0",
			checkACL:    "customer-Eth0",
		},
		{
			name:        "port channel",
			intf:        "PortChannel100",
			ipWithMask:  "10.1.1.1/30",
			serviceName: "transit",
			wantErr:     false,
			checkVRF:    "transit-Po100",
			checkACL:    "transit-Po100",
		},
		{
			name:        "no IP",
			intf:        "Ethernet0",
			ipWithMask:  "",
			serviceName: "l2-only",
			wantErr:     false,
			checkVRF:    "l2-only-Eth0",
		},
		{
			name:        "invalid IP",
			intf:        "Ethernet0",
			ipWithMask:  "invalid",
			serviceName: "test",
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
		serviceName   string
		interfaceName string
		want          string
	}{
		{"interface", "customer", "Ethernet0", "customer-Eth0"},
		{"shared", "customer", "Ethernet0", "customer"},
		{"", "customer", "Ethernet0", "customer-Eth0"}, // default to interface
		{"interface", "transit", "PortChannel100", "transit-Po100"},
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

func TestDeriveACLName(t *testing.T) {
	tests := []struct {
		serviceName string
		direction   string
		want        string
	}{
		{"customer-edge", "in", "customer-edge-in"},
		{"customer-edge", "out", "customer-edge-out"},
		{"transit", "in", "transit-in"},
	}

	for _, tt := range tests {
		got := DeriveACLName(tt.serviceName, tt.direction)
		if got != tt.want {
			t.Errorf("DeriveACLName(%q, %q) = %q, want %q", tt.serviceName, tt.direction, got, tt.want)
		}
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
