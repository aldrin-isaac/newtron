package newtrun

import (
	"testing"
	"time"
)

// TestResultsFromRunState_NilStateReturnsNil keeps the helper safe to
// call directly on a Client.GetRun return value without a preceding
// nil guard at every call site.
func TestResultsFromRunState_NilStateReturnsNil(t *testing.T) {
	if got := ResultsFromRunState(nil); got != nil {
		t.Errorf("ResultsFromRunState(nil): got %v, want nil", got)
	}
}

// TestResultsFromRunState_PreservesScenarioFields locks in the
// field-level mapping from ScenarioState → ScenarioResult that the
// `newtrun report` CLI relies on.
func TestResultsFromRunState_PreservesScenarioFields(t *testing.T) {
	state := &RunState{
		Topology: "1node-vs",
		Platform: "sonic-vs",
		Scenarios: []ScenarioState{
			{
				Name:       "boot",
				Status:     string(StepStatusPassed),
				Duration:   "5s",
				SkipReason: "",
				Steps: []StepState{
					{Name: "ssh", Action: "newtron-cli", Status: string(StepStatusPassed), Duration: "1s", Message: "ok"},
				},
			},
			{
				Name:       "verify",
				Status:     string(StepStatusSkipped),
				Duration:   "<1s",
				SkipReason: "depends on boot",
			},
		},
	}

	results := ResultsFromRunState(state)
	if len(results) != 2 {
		t.Fatalf("results count: got %d, want 2", len(results))
	}

	// Suite-level Topology/Platform propagate to every scenario — the
	// in-memory ReportGenerator expects them per-scenario but the wire
	// shape only carries them once.
	for _, r := range results {
		if r.Topology != "1node-vs" || r.Platform != "sonic-vs" {
			t.Errorf("scenario %q: topology=%q platform=%q, want 1node-vs / sonic-vs",
				r.Name, r.Topology, r.Platform)
		}
	}

	if results[0].Status != StepStatusPassed {
		t.Errorf("results[0].Status: got %q, want %q", results[0].Status, StepStatusPassed)
	}
	if results[0].Duration != 5*time.Second {
		t.Errorf("results[0].Duration: got %v, want 5s", results[0].Duration)
	}
	if len(results[0].Steps) != 1 || results[0].Steps[0].Name != "ssh" {
		t.Errorf("results[0].Steps: got %+v, want one step named ssh", results[0].Steps)
	}
	if results[0].Steps[0].Duration != time.Second {
		t.Errorf("results[0].Steps[0].Duration: got %v, want 1s", results[0].Steps[0].Duration)
	}

	if results[1].Status != StepStatusSkipped {
		t.Errorf("results[1].Status: got %q, want %q", results[1].Status, StepStatusSkipped)
	}
	if results[1].SkipReason != "depends on boot" {
		t.Errorf("results[1].SkipReason: got %q, want %q", results[1].SkipReason, "depends on boot")
	}
}

// TestParseReportDuration_Sentinels exercises the edge cases the on-
// disk Duration string carries (the "<1s" form the console reporter
// emits for sub-second steps).
func TestParseReportDuration_Sentinels(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"<1s", 0},
		{"5s", 5 * time.Second},
		{"1m30s", 90 * time.Second},
		{"not-a-duration", 0}, // garbage → zero, not panic
	}
	for _, tc := range cases {
		got := parseReportDuration(tc.in)
		if got != tc.want {
			t.Errorf("parseReportDuration(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}
}
