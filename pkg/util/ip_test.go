package util

import (
	"testing"
)

func Test_parseIPWithMask(t *testing.T) {
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
			ip, mask, err := parseIPWithMask(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIPWithMask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ip.String() != tt.wantIP {
					t.Errorf("parseIPWithMask() IP = %v, want %v", ip.String(), tt.wantIP)
				}
				if mask != tt.wantMask {
					t.Errorf("parseIPWithMask() mask = %v, want %v", mask, tt.wantMask)
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

