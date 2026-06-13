package cli

import (
	"strings"
	"testing"
)

func TestColorFunctions(t *testing.T) {
	tests := []struct {
		name   string
		fn     func(string) string
		prefix string
	}{
		{"Green", Green, "\033[32m"},
		{"Yellow", Yellow, "\033[33m"},
		{"Red", Red, "\033[31m"},
		{"Bold", Bold, "\033[1m"},
		{"Dim", Dim, "\033[2m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn("hello")
			if !strings.HasPrefix(got, tt.prefix) {
				t.Errorf("%s should start with %q", tt.name, tt.prefix)
			}
			if !strings.Contains(got, "hello") {
				t.Errorf("%s should contain the input string", tt.name)
			}
			if !strings.HasSuffix(got, "\033[0m") {
				t.Errorf("%s should end with reset code", tt.name)
			}
		})

		t.Run(tt.name+"_empty", func(t *testing.T) {
			got := tt.fn("")
			if !strings.HasSuffix(got, "\033[0m") {
				t.Errorf("%s(\"\") should end with reset code", tt.name)
			}
		})
	}
}
