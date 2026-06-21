package main

import (
	"os"
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

// TestNewtlabURL verifies the orchestrator/stats URL is sourced consistently:
// the explicit --newtlab-server flag and NEWTLAB_SERVER env take precedence
// (the multi-host override), but when neither is set the value falls back to
// --newtron-server — the server this CLI already talks to — not an independent
// hard-coded default. This keeps a single provided server URL honored across
// the baked orchestrator_url (deploy) and the `newtlab status` read path, since
// both resolve through this one helper.
func TestNewtlabURL(t *testing.T) {
	origFlag, origNewtron := newtlabServer, newtronServer
	origEnv, hadEnv := os.LookupEnv("NEWTLAB_SERVER")
	t.Cleanup(func() {
		newtlabServer, newtronServer = origFlag, origNewtron
		if hadEnv {
			os.Setenv("NEWTLAB_SERVER", origEnv)
		} else {
			os.Unsetenv("NEWTLAB_SERVER")
		}
	})

	tests := []struct {
		name        string
		newtlabFlag string // --newtlab-server
		env         string // NEWTLAB_SERVER ("" = unset)
		newtronFlag string // --newtron-server
		want        string
	}{
		{"explicit flag wins over env and newtron-server", "http://flag:1", "http://env:2", "http://newtron:3", "http://flag:1"},
		{"env wins when flag unset", "", "http://env:2", "http://newtron:3", "http://env:2"},
		{"falls back to --newtron-server when flag+env unset", "", "", "http://newtron:3", "http://newtron:3"},
		{"honors a non-default --newtron-server (the fix)", "", "", "http://127.0.0.1:19000", "http://127.0.0.1:19000"},
		{"default newtron-server when nothing overridden", "", "", "http://127.0.0.1:18080", "http://127.0.0.1:18080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newtlabServer = tt.newtlabFlag
			newtronServer = tt.newtronFlag
			if tt.env == "" {
				os.Unsetenv("NEWTLAB_SERVER")
			} else {
				os.Setenv("NEWTLAB_SERVER", tt.env)
			}
			if got := newtlabURL(); got != tt.want {
				t.Errorf("newtlabURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
