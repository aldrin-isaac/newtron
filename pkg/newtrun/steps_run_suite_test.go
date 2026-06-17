package newtrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// Parser validation for run-suite
// ============================================================================

func TestValidateStepFields_RunSuiteRequiresSuiteField(t *testing.T) {
	step := Step{Name: "child", Action: ActionRunSuite} // missing Suite
	err := validateStepFields("test", 0, &step)
	if err == nil {
		t.Fatal("expected error when run-suite step has no suite field")
	}
	if !strings.Contains(err.Error(), "run-suite requires suite") {
		t.Errorf("error text: got %q, want substring %q", err.Error(), "run-suite requires suite")
	}
}

func TestValidateStepFields_RunSuiteRejectsPathTraversal(t *testing.T) {
	cases := []string{"../escape", "with/slash", "..", ".", `with\backslash`}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			step := Step{Name: "child", Action: ActionRunSuite, Suite: name}
			err := validateStepFields("test", 0, &step)
			if err == nil {
				t.Fatalf("expected error for suite name %q", name)
			}
		})
	}
}

func TestValidateStepFields_RunSuiteAcceptsBareName(t *testing.T) {
	step := Step{Name: "child", Action: ActionRunSuite, Suite: "child-suite"}
	if err := validateStepFields("test", 0, &step); err != nil {
		t.Errorf("expected no error for plain suite name, got %v", err)
	}
}

// TestValidateStepFields_SuiteFieldOnlyOnRunSuite catches the typo case
// where an operator puts `suite:` (or `parameters:`/`targets:`) on a
// non-run-suite step. Silently dropping the fields at runtime would be
// confusing — the cross-field guard rejects them at parse time.
func TestValidateStepFields_SuiteFieldOnlyOnRunSuite(t *testing.T) {
	cases := []struct {
		desc string
		step Step
		want string // substring of the expected error
	}{
		{
			desc: "suite on wait",
			step: Step{Name: "x", Action: ActionWait, Duration: 1, Suite: "other"},
			want: "'suite' is only valid for action run-suite",
		},
		{
			desc: "parameters on newtron",
			step: Step{Name: "x", Action: ActionNewtron, URL: "/x", Parameters: map[string]any{"k": "v"}},
			want: "'parameters' is only valid for action run-suite",
		},
		{
			desc: "targets on newtron",
			step: Step{Name: "x", Action: ActionNewtron, URL: "/x", Targets: map[string][]string{"devices": {"a"}}},
			want: "'targets' is only valid for action run-suite",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			err := validateStepFields("test", 0, &tc.step)
			if err == nil {
				t.Fatalf("expected error for %s", tc.desc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error text: got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// ============================================================================
// Executor: surface errors that don't require a live Runner pipeline
// ============================================================================

// TestRunSuiteExecutor_MissingNetworksBase fails the step (rather
// than crashing) when the Runner wasn't wired with NetworksBase.
// Mirrors how pkg/newtrun is invoked from tests vs. the api server:
// the api server sets NetworksBase from Config, the CLI doesn't
// construct a Runner directly, and unit tests need the friendly error.
func TestRunSuiteExecutor_MissingNetworksBase(t *testing.T) {
	r := &Runner{}
	step := &Step{Name: "x", Action: ActionRunSuite, Suite: "child"}
	exec := &runSuiteExecutor{}
	out := exec.Execute(context.Background(), r, step)
	if out.Result.Status != StepStatusError {
		t.Errorf("status: got %v, want %v", out.Result.Status, StepStatusError)
	}
	if !strings.Contains(out.Result.Message, "NetworksBase") {
		t.Errorf("message: got %q, want substring %q", out.Result.Message, "NetworksBase")
	}
}

// TestRunSuiteExecutor_DepthLimit triggers the recursion guard by
// pre-populating the context's depth counter to one less than the cap.
// Going one deeper would put it over MaxRunSuiteDepth — the executor
// surfaces the limit as a step error.
func TestRunSuiteExecutor_DepthLimit(t *testing.T) {
	r := &Runner{NetworksBase: t.TempDir()}
	step := &Step{Name: "x", Action: ActionRunSuite, Suite: "child"}
	// depth+1 triggers the > MaxRunSuiteDepth check.
	ctx := withRunSuiteDepth(context.Background(), MaxRunSuiteDepth)
	exec := &runSuiteExecutor{}
	out := exec.Execute(ctx, r, step)
	if out.Result.Status != StepStatusError {
		t.Errorf("status: got %v, want %v", out.Result.Status, StepStatusError)
	}
	if !strings.Contains(out.Result.Message, "recursion limit") {
		t.Errorf("message: got %q, want substring %q", out.Result.Message, "recursion limit")
	}
}

// TestRunSuiteExecutor_MissingChildSuite is the natural failure path
// when the operator names a suite that doesn't exist on disk. The
// child Runner.Run surfaces the LoadSuite error; the executor wraps
// it so the step's Message names the called suite.
func TestRunSuiteExecutor_MissingChildSuite(t *testing.T) {
	base := t.TempDir()
	r := &Runner{NetworksBase: base}
	step := &Step{Name: "x", Action: ActionRunSuite, Suite: "ghost"}
	exec := &runSuiteExecutor{}
	out := exec.Execute(context.Background(), r, step)
	if out.Result.Status != StepStatusError {
		t.Errorf("status: got %v, want %v", out.Result.Status, StepStatusError)
	}
	if !strings.Contains(out.Result.Message, "ghost") {
		t.Errorf("message: got %q, want it to mention the called suite name", out.Result.Message)
	}
	// Sanity: NetworksBase wasn't touched (the failure should be in the
	// child Runner, not a side effect on disk).
	if _, err := os.Stat(filepath.Join(base, "ghost")); !os.IsNotExist(err) {
		t.Errorf("ghost suite directory should not exist after the failure path: %v", err)
	}
}

// ============================================================================
// Depth-context plumbing
// ============================================================================

func TestRunSuiteDepth_DefaultZero(t *testing.T) {
	if d := runSuiteDepth(context.Background()); d != 0 {
		t.Errorf("default depth: got %d, want 0", d)
	}
}

func TestRunSuiteDepth_Increment(t *testing.T) {
	ctx := withRunSuiteDepth(context.Background(), 3)
	if d := runSuiteDepth(ctx); d != 3 {
		t.Errorf("incremented depth: got %d, want 3", d)
	}
}
