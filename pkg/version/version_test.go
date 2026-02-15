package version

import "testing"

func TestDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("default Version = %q, want %q", Version, "dev")
	}
	if GitCommit != "unknown" {
		t.Errorf("default GitCommit = %q, want %q", GitCommit, "unknown")
	}
}
