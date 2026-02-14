package version

import "testing"

func TestDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("default Version = %q, want %q", Version, "dev")
	}
	if GitCommit != "unknown" {
		t.Errorf("default GitCommit = %q, want %q", GitCommit, "unknown")
	}
	if BuildDate != "unknown" {
		t.Errorf("default BuildDate = %q, want %q", BuildDate, "unknown")
	}
}

func TestInfo(t *testing.T) {
	s := Info()
	if s == "" {
		t.Error("Info() should return non-empty string")
	}
}
