package newtest

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
		{"newtest/suites/2node-incremental", "2node-incremental"},
		{"/home/user/newtest/suites/4node-fabric", "4node-fabric"},
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
		SuiteDir: "/tmp/test",
		Topology: "2node",
		Platform: "sonic-vpp",
		LabName:  "2node",
		PID:      12345,
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
	statePath := filepath.Join(tmpDir, ".newtron", "newtest", "test-suite", "state.json")
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

	// No suites yet
	names, err := ListSuiteStates()
	if err != nil {
		t.Fatalf("ListSuiteStates: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected 0 suites, got %d", len(names))
	}

	// Create two suites
	for _, suite := range []string{"suite-a", "suite-b"} {
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

func TestAcquireLock_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state := &RunState{
		Suite:  "lock-test",
		Status: SuiteStatusRunning,
	}

	if err := AcquireLock(state); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	if state.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", state.PID, os.Getpid())
	}
}

func TestAcquireLock_StalePID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Save state with a definitely-dead PID
	old := &RunState{
		Suite:  "stale-lock",
		PID:    999999999, // won't exist
		Status: SuiteStatusRunning,
	}
	if err := SaveRunState(old); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	// Should succeed — stale PID
	state := &RunState{Suite: "stale-lock", Status: SuiteStatusRunning}
	if err := AcquireLock(state); err != nil {
		t.Fatalf("AcquireLock with stale PID: %v", err)
	}
}

func TestAcquireLock_ActivePID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Save state with our own PID (definitely alive)
	old := &RunState{
		Suite:  "active-lock",
		PID:    os.Getpid(),
		Status: SuiteStatusRunning,
	}
	if err := SaveRunState(old); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	// Should fail — PID is alive
	state := &RunState{Suite: "active-lock", Status: SuiteStatusRunning}
	err := AcquireLock(state)
	if err == nil {
		t.Fatal("expected error for active PID lock")
	}
}

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

func TestIsProcessAlive(t *testing.T) {
	// Our own PID is alive
	if !IsProcessAlive(os.Getpid()) {
		t.Error("own PID should be alive")
	}

	// Zero or negative PIDs are not alive
	if IsProcessAlive(0) {
		t.Error("PID 0 should not be alive")
	}
	if IsProcessAlive(-1) {
		t.Error("PID -1 should not be alive")
	}

	// Very large PID likely doesn't exist
	if IsProcessAlive(999999999) {
		t.Error("PID 999999999 should not be alive")
	}
}
