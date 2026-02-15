package main

import (
	"os"
	"path/filepath"
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

func TestTopoCounts(t *testing.T) {
	t.Run("valid topology", func(t *testing.T) {
		dir := t.TempDir()
		data := `{
			"devices": {
				"spine1": {"platform": "sonic-vpp"},
				"leaf1": {"platform": "sonic-vpp"}
			},
			"links": [
				{"a": "spine1:Ethernet0", "z": "leaf1:Ethernet0"},
				{"a": "spine1:Ethernet4", "z": "leaf1:Ethernet4"},
				{"a": "spine1:Ethernet8", "z": "leaf1:Ethernet8"}
			]
		}`
		if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		devices, links := topoCounts(dir)
		if devices != 2 || links != 3 {
			t.Errorf("topoCounts() = (%d, %d), want (2, 3)", devices, links)
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		devices, links := topoCounts("/nonexistent")
		if devices != 0 || links != 0 {
			t.Errorf("topoCounts(/nonexistent) = (%d, %d), want (0, 0)", devices, links)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte("{bad json"), 0644); err != nil {
			t.Fatal(err)
		}
		devices, links := topoCounts(dir)
		if devices != 0 || links != 0 {
			t.Errorf("topoCounts(malformed) = (%d, %d), want (0, 0)", devices, links)
		}
	})
}

func TestResolveTopologyDir(t *testing.T) {
	t.Run("simple name uses base dir", func(t *testing.T) {
		t.Setenv("NEWTEST_TOPOLOGIES", "/tmp/topos")
		got := resolveTopologyDir("foo")
		want := "/tmp/topos/foo/specs"
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
