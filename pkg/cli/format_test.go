package cli

import (
	"strings"
	"testing"
)

func TestDotPad(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		width    int
		expected string
	}{
		{
			name:     "normal case",
			input:    "boot-ssh",
			width:    30,
			expected: "boot-ssh " + strings.Repeat(".", 21),
		},
		{
			name:     "short name",
			input:    "ok",
			width:    10,
			expected: "ok " + strings.Repeat(".", 7),
		},
		{
			name:     "name equals width minus one",
			input:    "abcde",
			width:    6,
			expected: "abcde",
		},
		{
			name:     "name equals width",
			input:    "abcdef",
			width:    6,
			expected: "abcdef",
		},
		{
			name:     "name longer than width",
			input:    "very-long-name",
			width:    5,
			expected: "very-long-name",
		},
		{
			name:     "empty string",
			input:    "",
			width:    10,
			expected: " " + strings.Repeat(".", 9),
		},
		{
			name:     "width of 1",
			input:    "",
			width:    1,
			expected: "",
		},
		{
			name:     "width of 2 with empty string",
			input:    "",
			width:    2,
			expected: " .",
		},
		{
			name:     "single char name width 5",
			input:    "x",
			width:    5,
			expected: "x " + strings.Repeat(".", 3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DotPad(tt.input, tt.width)
			if got != tt.expected {
				t.Errorf("DotPad(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.expected)
			}
		})
	}
}

func TestDotPad_ResultLength(t *testing.T) {
	result := DotPad("test", 20)
	if len(result) != 20 {
		t.Errorf("DotPad(%q, 20) len = %d, want 20", "test", len(result))
	}
}

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
