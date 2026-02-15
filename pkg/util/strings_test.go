package util

import "testing"

func TestSplitCommaSeparated(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"Ethernet0", 1},
		{"Ethernet0,Ethernet4", 2},
		{"Ethernet0, Ethernet4, Ethernet8", 3},
	}

	for _, tt := range tests {
		got := SplitCommaSeparated(tt.input)
		if len(got) != tt.want {
			t.Errorf("SplitCommaSeparated(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
