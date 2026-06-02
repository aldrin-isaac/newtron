package newtrun

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// Coverage for the parameterized iteration path in runScenarioSteps —
// the bridge between Suite-level Targets/Parameters (P6) and the
// runner's per-scenario loop (P4). These tests exercise the runner
// directly (no HTTP server, no real device) using a no-op step action
// that the existing dispatch already supports.

func TestRunScenarioSteps_ParameterizedIteratesCrossProduct(t *testing.T) {
	r := &Runner{
		suite: &Suite{
			Targets: map[string][]string{
				"devices":    {"s1", "s2"},
				"interfaces": {"Eth0", "Eth4"},
			},
		},
		iterations: (&Suite{Targets: map[string][]string{
			"devices":    {"s1", "s2"},
			"interfaces": {"Eth0", "Eth4"},
		}}).TargetIterations(),
	}
	scenario := &Scenario{
		Name: "rollout",
		Steps: []Step{
			// Template ref in Params triggers parameterized detection
			// (CollectTemplateReferences scans URL/Command/Params/etc
			// but not Step.Name). ActionWait ignores Params.
			{Name: "noop", Action: ActionWait, Duration: 0,
				Params: map[string]any{"_": "{{target.device}}-{{target.interface}}"}},
		},
	}
	result := &ScenarioResult{Name: "rollout"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	// 1 step × 4 iterations = 4 step results.
	if len(result.Steps) != 4 {
		t.Fatalf("got %d step results, want 4", len(result.Steps))
	}
	// Every result carries its binding.
	wantBindings := []map[string]string{
		{"device": "s1", "interface": "Eth0"},
		{"device": "s1", "interface": "Eth4"},
		{"device": "s2", "interface": "Eth0"},
		{"device": "s2", "interface": "Eth4"},
	}
	for i, sr := range result.Steps {
		if !reflect.DeepEqual(sr.TargetBinding, wantBindings[i]) {
			t.Errorf("Steps[%d].TargetBinding = %v, want %v", i, sr.TargetBinding, wantBindings[i])
		}
	}
	if result.Status != StepStatusPassed {
		t.Errorf("Status = %v, want PASS", result.Status)
	}
}

func TestRunScenarioSteps_EmbeddedTargetInParameterizedSuiteSingleIteration(t *testing.T) {
	// A scenario with no template refs in a parameterized suite must
	// NOT iterate the suite's target cross-product — it stays
	// embedded-target with a single (nil) binding.
	r := &Runner{
		suite: &Suite{Targets: map[string][]string{
			"devices": {"s1", "s2", "s3"},
		}},
		iterations: []map[string]string{
			{"device": "s1"}, {"device": "s2"}, {"device": "s3"},
		},
	}
	scenario := &Scenario{
		Name: "noop",
		Steps: []Step{
			{Name: "wait", Action: ActionWait, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "noop"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 1 {
		t.Fatalf("got %d step results, want 1 (embedded-target single pass)", len(result.Steps))
	}
	if result.Steps[0].TargetBinding != nil {
		t.Errorf("TargetBinding = %v, want nil for embedded-target", result.Steps[0].TargetBinding)
	}
}

func TestRunScenarioSteps_ParameterizedContinuesOnFailure(t *testing.T) {
	// Parameterized iterations are continue-on-failure (P4) — one
	// failing binding must not skip the remaining bindings.
	r := &Runner{
		suite: &Suite{Targets: map[string][]string{
			"devices": {"s1", "s2", "s3"},
		}},
		iterations: []map[string]string{
			{"device": "s1"}, {"device": "s2"}, {"device": "s3"},
		},
	}
	scenario := &Scenario{
		Name: "bad",
		Steps: []Step{
			// Params ref triggers parameterized detection; the invalid
			// action makes every iteration error.
			{Name: "always-fail", Action: "nonexistent-action",
				Params: map[string]any{"_": "{{target.device}}"}},
		},
	}
	result := &ScenarioResult{Name: "bad"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 3 {
		t.Fatalf("got %d step results, want 3 (continue-on-failure)", len(result.Steps))
	}
	// Verify every binding got tried.
	seen := map[string]bool{}
	for _, sr := range result.Steps {
		if sr.TargetBinding == nil {
			t.Fatalf("step result has nil binding: %+v", sr)
		}
		seen[sr.TargetBinding["device"]] = true
	}
	keys := []string{}
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"s1", "s2", "s3"}) {
		t.Errorf("seen bindings = %v, want [s1 s2 s3]", keys)
	}
}

func TestRunScenarioSteps_TemplateExpansionFailureReportedAsStepError(t *testing.T) {
	// A scenario with a {{param.X}} reference but no value supplied
	// (suite has parameters declared but the runner's r.parameters is
	// empty) — the expansion fails and the runner records a structured
	// step error rather than panicking.
	r := &Runner{
		suite: &Suite{Parameters: map[string]ParameterSpec{
			"missing_value": {Type: ParameterTypeString},
		}},
		iterations: []map[string]string{nil},
		parameters: nil,
	}
	scenario := &Scenario{
		Name: "expand-fail",
		Steps: []Step{
			{Name: "ref-undefined", Action: ActionWait, Params: map[string]any{"x": "{{param.missing_value}}"}, Duration: 0},
		},
	}
	result := &ScenarioResult{Name: "expand-fail"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if len(result.Steps) != 1 {
		t.Fatalf("got %d step results, want 1", len(result.Steps))
	}
	if result.Steps[0].Status != StepStatusError {
		t.Errorf("Status = %v, want ERROR", result.Steps[0].Status)
	}
}

func TestRunScenarioSteps_RepeatTimesIterationsCrossProduct(t *testing.T) {
	// Repeat × parameterized iterations should produce
	// Repeat-count × Iteration-count step results.
	r := &Runner{
		suite: &Suite{Targets: map[string][]string{
			"devices": {"s1", "s2"},
		}},
		iterations: []map[string]string{
			{"device": "s1"}, {"device": "s2"},
		},
	}
	scenario := &Scenario{
		Name:   "rep",
		Repeat: 2,
		Steps: []Step{
			{Name: "noop", Action: ActionWait, Duration: 0,
				Params: map[string]any{"_": "{{target.device}}"}},
		},
	}
	result := &ScenarioResult{Name: "rep"}
	r.runScenarioSteps(context.Background(), scenario, RunOptions{}, result)

	if got := len(result.Steps); got != 4 {
		t.Fatalf("got %d step results, want 4 (2 repeat × 2 iterations)", got)
	}
}
