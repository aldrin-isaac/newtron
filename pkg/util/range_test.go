package util

import (
	"reflect"
	"testing"
)

func TestExpandRange(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []int
		wantErr bool
	}{
		{
			name: "single value",
			spec: "5",
			want: []int{5},
		},
		{
			name: "simple range",
			spec: "1-5",
			want: []int{1, 2, 3, 4, 5},
		},
		{
			name: "comma separated",
			spec: "1,3,5",
			want: []int{1, 3, 5},
		},
		{
			name: "mixed",
			spec: "1-3,5,7-9",
			want: []int{1, 2, 3, 5, 7, 8, 9},
		},
		{
			name: "with spaces",
			spec: "1 - 3, 5",
			want: []int{1, 2, 3, 5},
		},
		{
			name: "duplicates removed",
			spec: "1-3,2-4",
			want: []int{1, 2, 3, 4},
		},
		{
			name: "empty string",
			spec: "",
			want: nil,
		},
		{
			name:    "invalid - start > end",
			spec:    "5-1",
			wantErr: true,
		},
		{
			name:    "invalid - not a number",
			spec:    "abc",
			wantErr: true,
		},
		{
			name:    "invalid - bad range format",
			spec:    "1-2-3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandRange(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandRange(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExpandRange(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestExpandSlotPortRange(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    [][2]int
		wantErr bool
	}{
		{
			name: "single slot and port",
			spec: "0:1",
			want: [][2]int{{0, 1}},
		},
		{
			name: "slot range with port range",
			spec: "0-1:1-2",
			want: [][2]int{{0, 1}, {0, 2}, {1, 1}, {1, 2}},
		},
		{
			name: "single slot with port range",
			spec: "0:1-3",
			want: [][2]int{{0, 1}, {0, 2}, {0, 3}},
		},
		{
			name:    "invalid - no colon",
			spec:    "0-1",
			wantErr: true,
		},
		{
			name:    "invalid - bad slot range",
			spec:    "abc:1-3",
			wantErr: true,
		},
		{
			name:    "invalid - bad port range",
			spec:    "0:abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandSlotPortRange(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandSlotPortRange(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExpandSlotPortRange(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestCompactRange(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		want   string
	}{
		{
			name:   "consecutive",
			values: []int{1, 2, 3, 4, 5},
			want:   "1-5",
		},
		{
			name:   "non-consecutive",
			values: []int{1, 3, 5},
			want:   "1,3,5",
		},
		{
			name:   "mixed",
			values: []int{1, 2, 3, 5, 7, 8, 9},
			want:   "1-3,5,7-9",
		},
		{
			name:   "single value",
			values: []int{5},
			want:   "5",
		},
		{
			name:   "empty",
			values: []int{},
			want:   "",
		},
		{
			name:   "unsorted with duplicates",
			values: []int{5, 3, 1, 2, 3, 4},
			want:   "1-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompactRange(tt.values)
			if got != tt.want {
				t.Errorf("CompactRange(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestExpandInterfaceRange(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []string
		wantErr bool
	}{
		{
			name: "ethernet range",
			spec: "Ethernet0-4",
			want: []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3", "Ethernet4"},
		},
		{
			name: "ethernet comma",
			spec: "Ethernet0,4,8",
			want: []string{"Ethernet0", "Ethernet4", "Ethernet8"},
		},
		{
			name: "port channel",
			spec: "PortChannel100-102",
			want: []string{"PortChannel100", "PortChannel101", "PortChannel102"},
		},
		{
			name:    "no prefix",
			spec:    "0-4",
			wantErr: true,
		},
		{
			name:    "invalid range",
			spec:    "Ethernet5-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandInterfaceRange(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandInterfaceRange(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExpandInterfaceRange(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestExpandVLANRange(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []int
		wantErr bool
	}{
		{
			name: "valid range",
			spec: "100-105,200",
			want: []int{100, 101, 102, 103, 104, 105, 200},
		},
		{
			name: "single vlan",
			spec: "100",
			want: []int{100},
		},
		{
			name:    "invalid - vlan 0",
			spec:    "0",
			wantErr: true,
		},
		{
			name:    "invalid - vlan too high",
			spec:    "4095",
			wantErr: true,
		},
		{
			name:    "invalid - includes bad vlan",
			spec:    "100-4095",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandVLANRange(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandVLANRange(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExpandVLANRange(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// Test that ExpandRange and CompactRange are inverses
	original := "1-3,5,7-10,15"
	expanded, err := ExpandRange(original)
	if err != nil {
		t.Fatalf("ExpandRange failed: %v", err)
	}
	compacted := CompactRange(expanded)
	if compacted != original {
		t.Errorf("Round trip failed: %q -> %v -> %q", original, expanded, compacted)
	}
}

func TestExpandRange_EmptyParts(t *testing.T) {
	// Test with empty parts after comma
	got, err := ExpandRange("1, , 3")
	if err != nil {
		t.Errorf("ExpandRange() unexpected error: %v", err)
	}
	want := []int{1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandRange(\"1, , 3\") = %v, want %v", got, want)
	}
}

func TestExpandRange_InvalidEndValue(t *testing.T) {
	// Test with invalid end value in range
	_, err := ExpandRange("1-abc")
	if err == nil {
		t.Error("Expected error for invalid end value")
	}
}

func TestDedupInts_Empty(t *testing.T) {
	// Test dedup with empty slice (edge case)
	input := []int{}
	// We can't call dedupInts directly as it's private, but we can test it via ExpandRange
	got, err := ExpandRange("")
	if err != nil {
		t.Errorf("ExpandRange() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ExpandRange(\"\") = %v, want nil", got)
	}
	_ = input // just to show we're testing the empty case
}

func TestExpandVLANRange_InvalidRange(t *testing.T) {
	// Test ExpandVLANRange with invalid range format (not invalid VLAN ID)
	_, err := ExpandVLANRange("abc")
	if err == nil {
		t.Error("Expected error for invalid range format")
	}
}

func TestExpandRange_DuplicatesWithSort(t *testing.T) {
	// Specifically test dedup path in dedupInts
	// This should trigger the dedup loop
	got, err := ExpandRange("1,1,1,2,2,3")
	if err != nil {
		t.Errorf("ExpandRange() unexpected error: %v", err)
	}
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandRange(\"1,1,1,2,2,3\") = %v, want %v", got, want)
	}
}
