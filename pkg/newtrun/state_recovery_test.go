package newtrun

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempHome redirects HOME so state files land in t.TempDir(). The
// sweep walks ~/.newtron/newtrun via os.UserHomeDir, so overriding HOME
// is the leanest way to make the test hermetic without mocking the dir
// helpers. Returns the temp home so callers can poke at on-disk state.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// TestSweepAbandonedRuns_NoStateDir is the empty-disk case: no
// ~/.newtron/newtrun exists yet. The sweep must be a clean no-op so a
// fresh install can boot without preconditions.
func TestSweepAbandonedRuns_NoStateDir(t *testing.T) {
	withTempHome(t)
	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep on empty home: %v", err)
	}
	if n != 0 {
		t.Errorf("marked: got %d, want 0", n)
	}
}

// TestSweepAbandonedRuns_MarksRunningSuite seeds one suite-namespace
// state.json with status "running" and verifies the sweep rewrites it
// to "abandoned". Locks in the basic recovery contract.
func TestSweepAbandonedRuns_MarksRunningSuite(t *testing.T) {
	withTempHome(t)
	state := &RunState{
		Suite:  "stale",
		Status: SuiteStatusRunning,
	}
	if err := SaveRunState(state); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("marked: got %d, want 1", n)
	}

	got, err := LoadRunState("stale")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got == nil {
		t.Fatal("state file missing after sweep")
	}
	if got.Status != SuiteStatusAbandoned {
		t.Errorf("status: got %q, want %q", got.Status, SuiteStatusAbandoned)
	}
}

// TestSweepAbandonedRuns_MarksRunningInline covers the inline-namespace
// path. Inline records live under _inline/<id>/ and use the parallel
// Save/Load helpers; the sweep must walk both directories.
func TestSweepAbandonedRuns_MarksRunningInline(t *testing.T) {
	withTempHome(t)
	state := &RunState{
		Suite:  "abc123",
		Status: SuiteStatusRunning,
	}
	if err := SaveInlineRunState(state); err != nil {
		t.Fatalf("seed inline: %v", err)
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("marked: got %d, want 1", n)
	}

	got, err := LoadInlineRunState("abc123")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != SuiteStatusAbandoned {
		t.Errorf("status: got %q, want %q", got.Status, SuiteStatusAbandoned)
	}
}

// TestSweepAbandonedRuns_MarksPausing covers the pausing case. A run
// caught mid-pause-transition by a server crash is just as stale as
// one in flight — the in-memory pause goroutine is gone with the
// process. Sweep treats pausing the same as running.
func TestSweepAbandonedRuns_MarksPausing(t *testing.T) {
	withTempHome(t)
	state := &RunState{Suite: "mid-pause", Status: SuiteStatusPausing}
	if err := SaveRunState(state); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("marked: got %d, want 1", n)
	}
	got, _ := LoadRunState("mid-pause")
	if got.Status != SuiteStatusAbandoned {
		t.Errorf("status: got %q, want %q", got.Status, SuiteStatusAbandoned)
	}
}

// TestSweepAbandonedRuns_LeavesTerminalStatesAlone seeds a mix of
// terminal states (complete/failed/aborted) plus an already-abandoned
// record and verifies the sweep leaves them untouched. The recovery
// pass must be idempotent — running it twice in a row should mark
// zero records on the second pass.
func TestSweepAbandonedRuns_LeavesTerminalStatesAlone(t *testing.T) {
	withTempHome(t)
	cases := []struct {
		suite  string
		status SuiteStatus
	}{
		{"done", SuiteStatusComplete},
		{"broken", SuiteStatusFailed},
		{"cancelled", SuiteStatusAborted},
		{"already", SuiteStatusAbandoned},
		{"snoozed", SuiteStatusPaused}, // paused is operator-driven, not stale
	}
	for _, c := range cases {
		if err := SaveRunState(&RunState{Suite: c.suite, Status: c.status}); err != nil {
			t.Fatalf("seed %s: %v", c.suite, err)
		}
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("marked: got %d, want 0 (terminal/paused states should be left alone)", n)
	}

	// Idempotency: every record retains its original status.
	for _, c := range cases {
		got, err := LoadRunState(c.suite)
		if err != nil {
			t.Fatalf("reload %s: %v", c.suite, err)
		}
		if got.Status != c.status {
			t.Errorf("%s status: got %q, want %q (sweep should not modify)", c.suite, got.Status, c.status)
		}
	}
}

// TestSweepAbandonedRuns_MixedNamespaces seeds running records in both
// the suite and inline namespaces; the sweep marks both. Walks the
// full state dir layout once.
func TestSweepAbandonedRuns_MixedNamespaces(t *testing.T) {
	withTempHome(t)
	if err := SaveRunState(&RunState{Suite: "suite-1", Status: SuiteStatusRunning}); err != nil {
		t.Fatalf("seed suite: %v", err)
	}
	if err := SaveInlineRunState(&RunState{Suite: "inline-1", Status: SuiteStatusRunning}); err != nil {
		t.Fatalf("seed inline: %v", err)
	}
	if err := SaveRunState(&RunState{Suite: "suite-done", Status: SuiteStatusComplete}); err != nil {
		t.Fatalf("seed suite-done: %v", err)
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 {
		t.Errorf("marked: got %d, want 2 (one suite + one inline)", n)
	}

	if got, _ := LoadRunState("suite-1"); got.Status != SuiteStatusAbandoned {
		t.Errorf("suite-1 status: got %q", got.Status)
	}
	if got, _ := LoadInlineRunState("inline-1"); got.Status != SuiteStatusAbandoned {
		t.Errorf("inline-1 status: got %q", got.Status)
	}
	if got, _ := LoadRunState("suite-done"); got.Status != SuiteStatusComplete {
		t.Errorf("suite-done status: got %q (should be untouched)", got.Status)
	}
}

// TestSweepAbandonedRuns_StrayFilesIgnored seeds a non-directory under
// ~/.newtron/newtrun and a directory missing state.json. Neither
// should produce an error — the sweep is best-effort and the only
// records it touches are real state.json files in proper subdirs.
func TestSweepAbandonedRuns_StrayFilesIgnored(t *testing.T) {
	home := withTempHome(t)
	base := filepath.Join(home, ".newtron", "newtrun")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Stray file in the base dir.
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	// Empty suite dir (no state.json).
	if err := os.MkdirAll(filepath.Join(base, "ghost-suite"), 0o755); err != nil {
		t.Fatalf("mkdir ghost-suite: %v", err)
	}
	// One real record so we know the sweep still works.
	if err := SaveRunState(&RunState{Suite: "real", Status: SuiteStatusRunning}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := SweepAbandonedRuns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("marked: got %d, want 1", n)
	}
}
