package main

import (
	"testing"
)

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}
	for _, tt := range tests {
		got := humanBytes(tt.input)
		if got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveTopologyDir(t *testing.T) {
	t.Run("simple name uses base dir", func(t *testing.T) {
		t.Setenv("NEWTRUN_TOPOLOGIES", "/tmp/topos")
		got := resolveTopologyDir("foo")
		want := "/tmp/topos/foo"
		if got != want {
			t.Errorf("resolveTopologyDir(%q) = %q, want %q", "foo", got, want)
		}
	})

	t.Run("path with slash returned as-is", func(t *testing.T) {
		got := resolveTopologyDir("path/to/dir")
		if got != "path/to/dir" {
			t.Errorf("resolveTopologyDir(%q) = %q, want %q", "path/to/dir", got, "path/to/dir")
		}
	})
}
