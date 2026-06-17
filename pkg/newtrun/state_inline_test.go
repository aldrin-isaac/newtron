package newtrun

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInlineStateDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir, err := InlineStateDir("abc-123")
	if err != nil {
		t.Fatalf("InlineStateDir: %v", err)
	}
	want := filepath.Join(tmpDir, ".newtron", "newtrun", "_inline", "abc-123")
	if dir != want {
		t.Errorf("InlineStateDir: got %q, want %q", dir, want)
	}
}

func TestSaveLoadInlineRunStateRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state := &RunState{
		Suite:    "test-uuid",
		Topology: "test-topo",
		Status:   SuiteStatusRunning,
		Started:  time.Now().UTC(),
	}
	if err := SaveInlineRunState(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadInlineRunState("test-uuid")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded nil after save")
	}
	if loaded.Suite != state.Suite {
		t.Errorf("Suite: got %q, want %q", loaded.Suite, state.Suite)
	}
	if loaded.Status != state.Status {
		t.Errorf("Status: got %q, want %q", loaded.Status, state.Status)
	}
}

func TestLoadInlineRunStateMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state, err := LoadInlineRunState("does-not-exist")
	if err != nil {
		t.Errorf("Load: got error %v, want nil", err)
	}
	if state != nil {
		t.Errorf("Load: got %+v, want nil", state)
	}
}

func TestInlineStateDoesNotPolluteSuiteNamespace(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("NEWTRUN_TOPOLOGIES_BASE", filepath.Join(tmpDir, "topologies"))

	// Save an inline state with an id that matches a hypothetical suite name.
	state := &RunState{
		Suite:  "would-be-suite",
		Status: SuiteStatusRunning,
	}
	if err := SaveInlineRunState(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// ListSuiteStates filters by the matching suite directory under
	// SUITES_BASE; an inline-only key should never appear.
	names, err := ListSuiteStates()
	if err != nil {
		t.Fatalf("ListSuiteStates: %v", err)
	}
	for _, n := range names {
		if n == "would-be-suite" {
			t.Errorf("inline-namespaced run leaked into ListSuiteStates: %v", names)
		}
	}
}

func TestLoadAnyRunStateChecksBothNamespaces(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Save into inline.
	inline := &RunState{Suite: "inline-id", Status: SuiteStatusRunning}
	if err := SaveInlineRunState(inline); err != nil {
		t.Fatalf("SaveInlineRunState: %v", err)
	}
	// Save into suite.
	suite := &RunState{Suite: "suite-name", Status: SuiteStatusComplete}
	if err := SaveRunState(suite); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}

	// LoadAnyRunState resolves both.
	if got, _ := LoadAnyRunState("inline-id"); got == nil || got.Status != SuiteStatusRunning {
		t.Errorf("LoadAnyRunState(inline-id): got %+v", got)
	}
	if got, _ := LoadAnyRunState("suite-name"); got == nil || got.Status != SuiteStatusComplete {
		t.Errorf("LoadAnyRunState(suite-name): got %+v", got)
	}
	if got, _ := LoadAnyRunState("nope"); got != nil {
		t.Errorf("LoadAnyRunState(nope): got %+v, want nil", got)
	}
}

func TestRemoveAnyRunStatePicksRightNamespace(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Inline namespace.
	inline := &RunState{Suite: "inline-victim", Status: SuiteStatusComplete}
	if err := SaveInlineRunState(inline); err != nil {
		t.Fatalf("SaveInline: %v", err)
	}
	// Suite namespace.
	suite := &RunState{Suite: "suite-victim", Status: SuiteStatusComplete}
	if err := SaveRunState(suite); err != nil {
		t.Fatalf("SaveSuite: %v", err)
	}

	if err := RemoveAnyRunState("inline-victim"); err != nil {
		t.Errorf("RemoveAnyRunState(inline-victim): %v", err)
	}
	if err := RemoveAnyRunState("suite-victim"); err != nil {
		t.Errorf("RemoveAnyRunState(suite-victim): %v", err)
	}

	// Both gone.
	if state, _ := LoadAnyRunState("inline-victim"); state != nil {
		t.Errorf("inline-victim still present")
	}
	if state, _ := LoadAnyRunState("suite-victim"); state != nil {
		t.Errorf("suite-victim still present")
	}
}
