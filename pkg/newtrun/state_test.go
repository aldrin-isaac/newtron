package newtrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSuiteName(t *testing.T) {
	tests := []struct {
		dir  string
		want string
	}{
		{"newtrun/topologies/2node-ngdp/suites/2node-ngdp-incremental", "2node-ngdp-incremental"},
		{"/home/user/newtrun/topologies/4node-ngdp/suites/4node-ngdp-fabric", "4node-ngdp-fabric"},
		{".", "."},
	}
	for _, tt := range tests {
		got := SuiteName(tt.dir)
		if got != tt.want {
			t.Errorf("SuiteName(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestSaveLoadRunState(t *testing.T) {
	// Use a temp dir to avoid polluting ~/.newtron
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	state := &RunState{
		Suite:    "test-suite",
		Topology: "2node-ngdp",
		Platform: "sonic-vpp",
		Status:   SuiteStatusRunning,
		Started:  time.Now().Truncate(time.Second),
		Scenarios: []ScenarioState{
			{Name: "boot-ssh", Status: "PASS", Duration: "2s"},
			{Name: "provision", Status: "", Duration: ""},
		},
	}

	// Save
	if err := SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	// Verify file exists
	statePath := filepath.Join(tmpDir, ".newtron", "newtrun", "test-suite", "state.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state.json not created: %v", err)
	}

	// Load
	loaded, err := LoadRunState("test-suite")
	if err != nil {
		t.Fatalf("LoadRunState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadRunState returned nil")
	}

	if loaded.Suite != "test-suite" {
		t.Errorf("Suite = %q, want %q", loaded.Suite, "test-suite")
	}
	if loaded.Status != SuiteStatusRunning {
		t.Errorf("Status = %q, want %q", loaded.Status, SuiteStatusRunning)
	}
	if len(loaded.Scenarios) != 2 {
		t.Errorf("Scenarios count = %d, want 2", len(loaded.Scenarios))
	}
	if loaded.Scenarios[0].Name != "boot-ssh" {
		t.Errorf("Scenarios[0].Name = %q, want %q", loaded.Scenarios[0].Name, "boot-ssh")
	}
}

func TestLoadRunState_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state, err := LoadRunState("nonexistent")
	if err != nil {
		t.Fatalf("LoadRunState: %v", err)
	}
	if state != nil {
		t.Error("expected nil state for nonexistent suite")
	}
}

func TestRemoveRunState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state := &RunState{Suite: "removable", Status: SuiteStatusComplete}
	if err := SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	if err := RemoveRunState("removable"); err != nil {
		t.Fatalf("RemoveRunState: %v", err)
	}

	loaded, err := LoadRunState("removable")
	if err != nil {
		t.Fatalf("LoadRunState after remove: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil state after removal")
	}
}

func TestListSuiteStates(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Point NEWTRUN_TOPOLOGIES_BASE at a temp directory so the
	// suite-dir filter (via ResolveSuiteDir glob) resolves seeded
	// suites under <base>/<topology>/suites/<name>/.
	topologiesBase := filepath.Join(tmpDir, "topologies")
	t.Setenv("NEWTRUN_TOPOLOGIES_BASE", topologiesBase)

	// No suites yet
	names, err := ListSuiteStates()
	if err != nil {
		t.Fatalf("ListSuiteStates: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected 0 suites, got %d", len(names))
	}

	// Create two suites — both the state file and the matching suite
	// directory at its per-topology location.
	suitesRoot := filepath.Join(topologiesBase, "test-topo", "suites")
	for _, suite := range []string{"suite-a", "suite-b"} {
		if err := os.MkdirAll(filepath.Join(suitesRoot, suite), 0755); err != nil {
			t.Fatalf("MkdirAll suite dir(%s): %v", suite, err)
		}
		state := &RunState{Suite: suite, Status: SuiteStatusComplete}
		if err := SaveRunState(state); err != nil {
			t.Fatalf("SaveRunState(%s): %v", suite, err)
		}
	}

	names, err = ListSuiteStates()
	if err != nil {
		t.Fatalf("ListSuiteStates: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 suites, got %d", len(names))
	}
}

// AcquireLock/ReleaseLock/IsProcessAlive were CLI-process-mode lock
// helpers — used by the old monolithic runner to hold the suite via
// PID + process-alive probe. Server-mode runs are goroutines under
// the registry, so the PID lock is obsolete and the helpers were
// retired. Tests for them deleted with the functions.

func TestCheckPausing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Not found → false
	if CheckPausing("nope") {
		t.Error("expected false for nonexistent suite")
	}

	// Running → false
	state := &RunState{Suite: "pause-test", Status: SuiteStatusRunning}
	if err := SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}
	if CheckPausing("pause-test") {
		t.Error("expected false for running suite")
	}

	// Pausing → true
	state.Status = SuiteStatusPausing
	if err := SaveRunState(state); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}
	if !CheckPausing("pause-test") {
		t.Error("expected true for pausing suite")
	}
}

