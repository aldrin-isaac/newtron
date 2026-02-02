package util

import (
	"testing"
)

func TestParseIPWithMask(t *testing.T) {
	tests := []struct {
		name        string
		cidr        string
		wantIP      string
		wantMask    int
		wantErr     bool
	}{
		{
			name:     "valid /24",
			cidr:     "192.168.1.100/24",
			wantIP:   "192.168.1.100",
			wantMask: 24,
			wantErr:  false,
		},
		{
			name:     "valid /30",
			cidr:     "10.1.1.1/30",
			wantIP:   "10.1.1.1",
			wantMask: 30,
			wantErr:  false,
		},
		{
			name:     "valid /31",
			cidr:     "10.1.1.0/31",
			wantIP:   "10.1.1.0",
			wantMask: 31,
			wantErr:  false,
		},
		{
			name:     "valid /32",
			cidr:     "10.0.0.1/32",
			wantIP:   "10.0.0.1",
			wantMask: 32,
			wantErr:  false,
		},
		{
			name:    "invalid - no mask",
			cidr:    "192.168.1.100",
			wantErr: true,
		},
		{
			name:    "invalid - bad IP",
			cidr:    "999.999.999.999/24",
			wantErr: true,
		},
		{
			name:    "invalid - empty",
			cidr:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, mask, err := ParseIPWithMask(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseIPWithMask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ip.String() != tt.wantIP {
					t.Errorf("ParseIPWithMask() IP = %v, want %v", ip.String(), tt.wantIP)
				}
				if mask != tt.wantMask {
					t.Errorf("ParseIPWithMask() mask = %v, want %v", mask, tt.wantMask)
				}
			}
		})
	}
}

func TestComputeNeighborIP(t *testing.T) {
	tests := []struct {
		name    string
		localIP string
		maskLen int
		want    string
	}{
		// /31 tests (RFC 3021)
		{
			name:    "/31 first IP",
			localIP: "10.1.1.0",
			maskLen: 31,
			want:    "10.1.1.1",
		},
		{
			name:    "/31 second IP",
			localIP: "10.1.1.1",
			maskLen: 31,
			want:    "10.1.1.0",
		},
		// /30 tests
		{
			name:    "/30 first host",
			localIP: "10.1.1.1",
			maskLen: 30,
			want:    "10.1.1.2",
		},
		{
			name:    "/30 second host",
			localIP: "10.1.1.2",
			maskLen: 30,
			want:    "10.1.1.1",
		},
		{
			name:    "/30 network address",
			localIP: "10.1.1.0",
			maskLen: 30,
			want:    "", // Network address has no neighbor
		},
		{
			name:    "/30 broadcast address",
			localIP: "10.1.1.3",
			maskLen: 30,
			want:    "", // Broadcast address has no neighbor
		},
		// Non point-to-point
		{
			name:    "/24 not point-to-point",
			localIP: "10.1.1.1",
			maskLen: 24,
			want:    "",
		},
		{
			name:    "/29 not point-to-point",
			localIP: "10.1.1.1",
			maskLen: 29,
			want:    "",
		},
		// Invalid input
		{
			name:    "invalid IP",
			localIP: "invalid",
			maskLen: 30,
			want:    "",
		},
		{
			name:    "empty IP",
			localIP: "",
			maskLen: 30,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeNeighborIP(tt.localIP, tt.maskLen)
			if got != tt.want {
				t.Errorf("ComputeNeighborIP(%q, %d) = %q, want %q", tt.localIP, tt.maskLen, got, tt.want)
			}
		})
	}
}

func TestComputeNetworkAddr(t *testing.T) {
	tests := []struct {
		name    string
		ipStr   string
		maskLen int
		want    string
	}{
		{
			name:    "/24 network",
			ipStr:   "192.168.1.100",
			maskLen: 24,
			want:    "192.168.1.0",
		},
		{
			name:    "/30 network",
			ipStr:   "10.1.1.2",
			maskLen: 30,
			want:    "10.1.1.0",
		},
		{
			name:    "/16 network",
			ipStr:   "172.16.50.100",
			maskLen: 16,
			want:    "172.16.0.0",
		},
		{
			name:    "/32 host",
			ipStr:   "10.0.0.1",
			maskLen: 32,
			want:    "10.0.0.1",
		},
		{
			name:    "invalid IP",
			ipStr:   "invalid",
			maskLen: 24,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeNetworkAddr(tt.ipStr, tt.maskLen)
			if got != tt.want {
				t.Errorf("ComputeNetworkAddr(%q, %d) = %q, want %q", tt.ipStr, tt.maskLen, got, tt.want)
			}
		})
	}
}

func TestComputeBroadcastAddr(t *testing.T) {
	tests := []struct {
		name    string
		ipStr   string
		maskLen int
		want    string
	}{
		{
			name:    "/24 broadcast",
			ipStr:   "192.168.1.100",
			maskLen: 24,
			want:    "192.168.1.255",
		},
		{
			name:    "/30 broadcast",
			ipStr:   "10.1.1.1",
			maskLen: 30,
			want:    "10.1.1.3",
		},
		{
			name:    "/16 broadcast",
			ipStr:   "172.16.50.100",
			maskLen: 16,
			want:    "172.16.255.255",
		},
		{
			name:    "invalid IP",
			ipStr:   "invalid",
			maskLen: 24,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeBroadcastAddr(tt.ipStr, tt.maskLen)
			if got != tt.want {
				t.Errorf("ComputeBroadcastAddr(%q, %d) = %q, want %q", tt.ipStr, tt.maskLen, got, tt.want)
			}
		})
	}
}

func TestIsValidIPv4(t *testing.T) {
	tests := []struct {
		name  string
		ipStr string
		want  bool
	}{
		{"valid IP", "192.168.1.1", true},
		{"valid loopback", "127.0.0.1", true},
		{"valid zero", "0.0.0.0", true},
		{"valid broadcast", "255.255.255.255", true},
		{"invalid - out of range", "256.1.1.1", false},
		{"invalid - text", "invalid", false},
		{"invalid - empty", "", false},
		{"invalid - IPv6", "::1", false},
		{"invalid - partial", "192.168.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidIPv4(tt.ipStr)
			if got != tt.want {
				t.Errorf("IsValidIPv4(%q) = %v, want %v", tt.ipStr, got, tt.want)
			}
		})
	}
}

func TestIsValidIPv4CIDR(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want bool
	}{
		{"valid /24", "192.168.1.0/24", true},
		{"valid /32", "10.0.0.1/32", true},
		{"valid /0", "0.0.0.0/0", true},
		{"invalid - no mask", "192.168.1.1", false},
		{"invalid - bad IP", "999.1.1.1/24", false},
		{"invalid - bad mask", "192.168.1.0/33", false},
		{"invalid - empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidIPv4CIDR(tt.cidr)
			if got != tt.want {
				t.Errorf("IsValidIPv4CIDR(%q) = %v, want %v", tt.cidr, got, tt.want)
			}
		})
	}
}

func TestIsValidMACAddress(t *testing.T) {
	tests := []struct {
		name string
		mac  string
		want bool
	}{
		{"valid colon format", "00:11:22:33:44:55", true},
		{"valid dash format", "00-11-22-33-44-55", true},
		{"valid lowercase", "aa:bb:cc:dd:ee:ff", true},
		{"valid mixed case", "AA:bb:CC:dd:EE:ff", true},
		{"invalid - too short", "00:11:22:33:44", false},
		{"invalid - too long", "00:11:22:33:44:55:66", false},
		{"invalid - bad char", "00:11:22:33:44:gg", false},
		{"invalid - empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidMACAddress(tt.mac)
			if got != tt.want {
				t.Errorf("IsValidMACAddress(%q) = %v, want %v", tt.mac, got, tt.want)
			}
		})
	}
}

func TestNormalizeMACAddress(t *testing.T) {
	tests := []struct {
		name    string
		mac     string
		want    string
		wantErr bool
	}{
		{
			name: "uppercase to lowercase",
			mac:  "AA:BB:CC:DD:EE:FF",
			want: "aa:bb:cc:dd:ee:ff",
		},
		{
			name: "dash to colon",
			mac:  "00-11-22-33-44-55",
			want: "00:11:22:33:44:55",
		},
		{
			name:    "invalid MAC",
			mac:     "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeMACAddress(tt.mac)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizeMACAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("NormalizeMACAddress(%q) = %q, want %q", tt.mac, got, tt.want)
			}
		})
	}
}

func TestIPInRange(t *testing.T) {
	tests := []struct {
		name  string
		ipStr string
		cidr  string
		want  bool
	}{
		{"in range", "192.168.1.100", "192.168.1.0/24", true},
		{"at start", "192.168.1.0", "192.168.1.0/24", true},
		{"at end", "192.168.1.255", "192.168.1.0/24", true},
		{"out of range", "192.168.2.1", "192.168.1.0/24", false},
		{"different subnet", "10.0.0.1", "192.168.1.0/24", false},
		{"invalid IP", "invalid", "192.168.1.0/24", false},
		{"invalid CIDR", "192.168.1.1", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IPInRange(tt.ipStr, tt.cidr)
			if got != tt.want {
				t.Errorf("IPInRange(%q, %q) = %v, want %v", tt.ipStr, tt.cidr, got, tt.want)
			}
		})
	}
}

func TestValidateVLANID(t *testing.T) {
	tests := []struct {
		name    string
		vlanID  int
		wantErr bool
	}{
		{"valid min", 1, false},
		{"valid max", 4094, false},
		{"valid middle", 100, false},
		{"invalid zero", 0, true},
		{"invalid negative", -1, true},
		{"invalid too high", 4095, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVLANID(tt.vlanID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateVLANID(%d) error = %v, wantErr %v", tt.vlanID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateVNI(t *testing.T) {
	tests := []struct {
		name    string
		vni     int
		wantErr bool
	}{
		{"valid min", 1, false},
		{"valid max", 16777215, false},
		{"valid middle", 10000, false},
		{"invalid zero", 0, true},
		{"invalid negative", -1, true},
		{"invalid too high", 16777216, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVNI(tt.vni)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateVNI(%d) error = %v, wantErr %v", tt.vni, err, tt.wantErr)
			}
		})
	}
}

func TestValidateASN(t *testing.T) {
	tests := []struct {
		name    string
		asn     int
		wantErr bool
	}{
		{"valid 2-byte ASN", 65000, false},
		{"valid 4-byte ASN", 4200000000, false},
		{"valid min", 1, false},
		{"invalid zero", 0, true},
		{"invalid negative", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateASN(tt.asn)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateASN(%d) error = %v, wantErr %v", tt.asn, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMTU(t *testing.T) {
	tests := []struct {
		name    string
		mtu     int
		wantErr bool
	}{
		{"valid min", 68, false},
		{"valid max", 9216, false},
		{"valid standard", 1500, false},
		{"valid jumbo", 9000, false},
		{"invalid too low", 67, true},
		{"invalid too high", 9217, true},
		{"invalid zero", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMTU(tt.mtu)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMTU(%d) error = %v, wantErr %v", tt.mtu, err, tt.wantErr)
			}
		})
	}
}

func TestParsePortRange(t *testing.T) {
	tests := []struct {
		name      string
		rangeStr  string
		wantStart int
		wantEnd   int
		wantErr   bool
	}{
		{"single port", "80", 80, 80, false},
		{"port range", "1024-65535", 1024, 65535, false},
		{"small range", "80-443", 80, 443, false},
		{"same start end", "22-22", 22, 22, false},
		{"invalid - start > end", "100-50", 0, 0, true},
		{"invalid - negative", "-1-100", 0, 0, true},
		{"invalid - too high", "65536", 0, 0, true},
		{"invalid - not number", "abc", 0, 0, true},
		{"invalid - bad format", "80-90-100", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := ParsePortRange(tt.rangeStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParsePortRange(%q) error = %v, wantErr %v", tt.rangeStr, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if start != tt.wantStart || end != tt.wantEnd {
					t.Errorf("ParsePortRange(%q) = (%d, %d), want (%d, %d)", tt.rangeStr, start, end, tt.wantStart, tt.wantEnd)
				}
			}
		})
	}
}

func TestFormatRouteDistinguisher(t *testing.T) {
	tests := []struct {
		routerID string
		index    int
		want     string
	}{
		{"10.0.0.1", 1, "10.0.0.1:1"},
		{"192.168.1.1", 100, "192.168.1.1:100"},
	}

	for _, tt := range tests {
		got := FormatRouteDistinguisher(tt.routerID, tt.index)
		if got != tt.want {
			t.Errorf("FormatRouteDistinguisher(%q, %d) = %q, want %q", tt.routerID, tt.index, got, tt.want)
		}
	}
}

func TestFormatRouteTarget(t *testing.T) {
	tests := []struct {
		asn   int
		value int
		want  string
	}{
		{65000, 100, "65000:100"},
		{4200000000, 1, "4200000000:1"},
	}

	for _, tt := range tests {
		got := FormatRouteTarget(tt.asn, tt.value)
		if got != tt.want {
			t.Errorf("FormatRouteTarget(%d, %d) = %q, want %q", tt.asn, tt.value, got, tt.want)
		}
	}
}

func TestSplitIPMask(t *testing.T) {
	tests := []struct {
		cidr     string
		wantIP   string
		wantMask int
	}{
		{"10.1.1.1/30", "10.1.1.1", 30},
		{"192.168.1.0/24", "192.168.1.0", 24},
		{"10.0.0.1/32", "10.0.0.1", 32},
		{"10.0.0.1", "10.0.0.1", 0}, // No mask
	}

	for _, tt := range tests {
		ip, mask := SplitIPMask(tt.cidr)
		if ip != tt.wantIP || mask != tt.wantMask {
			t.Errorf("SplitIPMask(%q) = (%q, %d), want (%q, %d)", tt.cidr, ip, mask, tt.wantIP, tt.wantMask)
		}
	}
}

func TestDeriveNeighborIP(t *testing.T) {
	tests := []struct {
		name            string
		localIPWithMask string
		want            string
		wantErr         bool
	}{
		{
			name:            "/30 first host",
			localIPWithMask: "10.1.1.1/30",
			want:            "10.1.1.2",
		},
		{
			name:            "/30 second host",
			localIPWithMask: "10.1.1.2/30",
			want:            "10.1.1.1",
		},
		{
			name:            "/31 first",
			localIPWithMask: "10.1.1.0/31",
			want:            "10.1.1.1",
		},
		{
			name:            "/31 second",
			localIPWithMask: "10.1.1.1/31",
			want:            "10.1.1.0",
		},
		{
			name:            "no mask",
			localIPWithMask: "10.1.1.1",
			wantErr:         true,
		},
		{
			name:            "/24 not point-to-point",
			localIPWithMask: "10.1.1.1/24",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveNeighborIP(tt.localIPWithMask)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeriveNeighborIP(%q) error = %v, wantErr %v", tt.localIPWithMask, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("DeriveNeighborIP(%q) = %q, want %q", tt.localIPWithMask, got, tt.want)
			}
		})
	}
}

func TestComputeNeighborIP_IPv6(t *testing.T) {
	// IPv6 is not supported, should return empty string
	got := ComputeNeighborIP("::1", 31)
	if got != "" {
		t.Errorf("ComputeNeighborIP(IPv6) = %q, want empty", got)
	}
}

func TestComputeNetworkAddr_IPv6(t *testing.T) {
	// IPv6 is not supported, should return empty string
	got := ComputeNetworkAddr("2001:db8::1", 64)
	if got != "" {
		t.Errorf("ComputeNetworkAddr(IPv6) = %q, want empty", got)
	}
}

func TestComputeBroadcastAddr_IPv6(t *testing.T) {
	// IPv6 is not supported, should return empty string
	got := ComputeBroadcastAddr("2001:db8::1", 64)
	if got != "" {
		t.Errorf("ComputeBroadcastAddr(IPv6) = %q, want empty", got)
	}
}

func TestSplitIPMask_InvalidMask(t *testing.T) {
	// Invalid mask should return 0
	ip, mask := SplitIPMask("10.1.1.1/abc")
	if ip != "10.1.1.1" {
		t.Errorf("SplitIPMask() IP = %q, want %q", ip, "10.1.1.1")
	}
	if mask != 0 {
		t.Errorf("SplitIPMask() mask = %d, want 0", mask)
	}
}

func TestParsePortRange_EndPortOutOfRange(t *testing.T) {
	// Test when end port is out of range
	_, _, err := ParsePortRange("100-70000")
	if err == nil {
		t.Error("Expected error for end port out of range")
	}
}

func TestParsePortRange_InvalidEndPort(t *testing.T) {
	// Test when end port is not a number
	_, _, err := ParsePortRange("100-abc")
	if err == nil {
		t.Error("Expected error for invalid end port")
	}
}
