package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// TestRenderSuiteStatus_EmptyScenariosOnTerminalStatus pins issue #93's
// fix: when a suite finalizes with a terminal status (failed/aborted/
// complete/etc.) but no scenario ever updated its own Status, the
// renderer must replace the table-of-dashes with a single explanatory
// line. The pre-fix behavior was a "failed" header above 22 rows of "—",
// which looked self-contradictory.
func TestRenderSuiteStatus_EmptyScenariosOnTerminalStatus(t *testing.T) {
	state := &newtrun.RunState{
		Suite:    "test-suite",
		Status:   newtrun.SuiteStatusFailed,
		Started:  time.Now().Add(-time.Minute),
		Finished: time.Now(),
		Scenarios: []newtrun.ScenarioState{
			{Name: "boot-ssh", TotalSteps: 3},
			{Name: "setup-device", TotalSteps: 19},
			{Name: "bridged", TotalSteps: 15},
		},
	}

	var buf bytes.Buffer
	if err := renderSuiteStatus(&buf, "test-suite", state, false); err != nil {
		t.Fatalf("renderSuiteStatus: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "suite terminated before any scenarios were attempted") {
		t.Errorf("output missing the empty-scenarios explanatory line\n--- got ---\n%s", out)
	}
	// The summary line ("progress: N/M passed") signals the table-render
	// path. If it appears, the early-return guard didn't fire and the
	// fix regressed.
	if strings.Contains(out, "progress:") {
		t.Errorf("output contains 'progress:' — the table-render path ran when it should have been skipped\n--- got ---\n%s", out)
	}
}

// TestRenderSuiteStatus_RunningStateRendersNormally guards the other
// direction: a still-running suite with empty scenarios (because it
// hasn't gotten to them yet) must NOT trigger the empty-scenarios
// branch — the operator wants to see the upcoming work in the table.
func TestRenderSuiteStatus_RunningStateRendersNormally(t *testing.T) {
	state := &newtrun.RunState{
		Suite:   "test-suite",
		Status:  newtrun.SuiteStatusRunning,
		Started: time.Now(),
		Scenarios: []newtrun.ScenarioState{
			{Name: "boot-ssh", TotalSteps: 3},
		},
	}

	var buf bytes.Buffer
	if err := renderSuiteStatus(&buf, "test-suite", state, false); err != nil {
		t.Fatalf("renderSuiteStatus: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "suite terminated before any scenarios were attempted") {
		t.Errorf("output incorrectly shows the terminated message for a Running suite\n--- got ---\n%s", out)
	}
}

// TestRenderSuiteStatus_CompletedScenariosRenderNormally guards the
// happy path: a terminal status with scenarios that DID update their
// status must show the normal table, not the empty-scenarios branch.
func TestRenderSuiteStatus_CompletedScenariosRenderNormally(t *testing.T) {
	state := &newtrun.RunState{
		Suite:    "test-suite",
		Status:   newtrun.SuiteStatusComplete,
		Started:  time.Now().Add(-time.Minute),
		Finished: time.Now(),
		Scenarios: []newtrun.ScenarioState{
			{Name: "boot-ssh", Status: "PASS", TotalSteps: 3, Duration: "1s"},
			{Name: "setup-device", Status: "PASS", TotalSteps: 19, Duration: "9s"},
		},
	}

	var buf bytes.Buffer
	if err := renderSuiteStatus(&buf, "test-suite", state, false); err != nil {
		t.Fatalf("renderSuiteStatus: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "suite terminated before any scenarios were attempted") {
		t.Errorf("output incorrectly shows the terminated message when scenarios completed\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "progress:") {
		t.Errorf("output missing the normal progress line for completed scenarios\n--- got ---\n%s", out)
	}
}
