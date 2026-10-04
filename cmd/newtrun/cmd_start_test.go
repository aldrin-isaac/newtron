package main

import (
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// TestSuiteOutcome_PreflightFailureIsInfrastructureError pins #508: a run that
// could not start emits no scenario events, only a SuiteEnd whose results are
// ERROR with a deploy error. That must exit as an infrastructure error and keep
// the cause for the report — not exit 0 with nothing to show.
func TestSuiteOutcome_PreflightFailureIsInfrastructureError(t *testing.T) {
	p := api.SuiteEndPayload{
		Status: newtrun.SuiteStatusFailed,
		Results: []api.ScenarioEndPayload{
			{Name: "boot-ssh", Status: newtrun.StepStatusError, DeployError: "deploy: lab 2node-vs-service not found"},
			{Name: "provision", Status: newtrun.StepStatusError, DeployError: "deploy: lab 2node-vs-service not found"},
		},
	}

	results, failed, errored := suiteOutcome(p)
	if !errored {
		t.Error("errored = false; a run that could not start must exit as an infrastructure error")
	}
	if failed {
		t.Error("failed = true; nothing ran, so no test failed")
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 for the report", len(results))
	}
	if results[0].DeployError == nil || results[0].DeployError.Error() != "deploy: lab 2node-vs-service not found" {
		t.Errorf("DeployError = %v, want the cause carried into the report", results[0].DeployError)
	}
}

// TestSuiteOutcome_TestFailureIsNotInfrastructureError keeps the two exit codes
// apart: a scenario that ran and failed is exit 1, not 2.
func TestSuiteOutcome_TestFailureIsNotInfrastructureError(t *testing.T) {
	p := api.SuiteEndPayload{
		Status: newtrun.SuiteStatusFailed,
		Results: []api.ScenarioEndPayload{
			{Name: "a", Status: newtrun.StepStatusPassed},
			{Name: "b", Status: newtrun.StepStatusFailed},
		},
	}
	_, failed, errored := suiteOutcome(p)
	if !failed || errored {
		t.Errorf("failed=%v errored=%v, want failed=true errored=false", failed, errored)
	}
}
