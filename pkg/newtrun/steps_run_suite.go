// run-suite step composition (issue #27).
//
// A run-suite step invokes another suite as if it were a single step in
// the parent scenario. The child suite runs through the same Runner
// pipeline as a top-level run — same goroutine pattern, same state
// machine, same ProgressReporter chain — but with NoDeploy=true (the
// parent already deployed the topology) and the parent's connections
// reused (Client, NewtlabClient, HostConns).
//
// Composition primitives live here (§28 file-level feature cohesion);
// the depth-counter context value and the public RunSuite entry-point
// guard against unbounded recursion. Lock-collision detection across
// concurrent external runs is deferred (it requires registry injection
// from pkg/newtrun/api into pkg/newtrun, which would invert the import
// direction) — within-process recursion is caught by the depth limit.
package newtrun

import (
	"context"
	"fmt"
	"time"
)

// MaxRunSuiteDepth is the default depth limit for run-suite recursion.
// Each call to a child suite increments the depth counter on the
// request context; if the next call would exceed this cap, the
// executor fails the step with a clear error rather than blowing the
// goroutine stack.
//
// Five is the default the issue spec recommends; it's deep enough for
// realistic composition (setup → service → verify → drift-check →
// reconcile) without permitting accidental towers.
const MaxRunSuiteDepth = 5

// runSuiteDepthKey is the context key carrying the current recursion
// depth. Unexported with a typed key per Go context conventions —
// prevents collisions with other packages' context values.
type runSuiteDepthKeyType struct{}

var runSuiteDepthKey runSuiteDepthKeyType

// runSuiteDepth reads the recursion depth from ctx. Returns 0 when no
// run-suite call is in progress (top-level run).
func runSuiteDepth(ctx context.Context) int {
	if v, ok := ctx.Value(runSuiteDepthKey).(int); ok {
		return v
	}
	return 0
}

// withRunSuiteDepth returns a child context with the depth incremented
// by one. Called inside the executor before invoking the child Runner.
func withRunSuiteDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, runSuiteDepthKey, depth)
}

// runSuiteExecutor implements ActionRunSuite. It resolves the called
// suite under Runner.NetworksBase (globbing
// <base>/<topology>/suites/<name>/), constructs a child Runner that
// inherits the parent's connections, and runs the child's scenarios
// inside the same goroutine as the parent step.
//
// Failure modes the executor surfaces as step errors (not crashes):
//   - NetworksBase not configured (top-level entry didn't wire it)
//   - depth exceeds MaxRunSuiteDepth
//   - called suite missing or ambiguous across topologies
//   - any child scenario fails (step status mirrors the worst child status)
type runSuiteExecutor struct{}

func (e *runSuiteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	start := time.Now()
	result := &StepResult{
		Name:     step.Name,
		Action:   step.Action,
		Duration: 0,
	}

	if r.NetworksBase == "" {
		result.Status = StepStatusError
		result.Message = "run-suite requires Runner.NetworksBase to be configured (top-level entry must wire it)"
		result.Duration = time.Since(start)
		return &StepOutput{Result: result}
	}

	depth := runSuiteDepth(ctx) + 1
	if depth > MaxRunSuiteDepth {
		result.Status = StepStatusError
		result.Message = fmt.Sprintf("run-suite recursion limit (%d) exceeded calling suite %q",
			MaxRunSuiteDepth, step.Suite)
		result.Duration = time.Since(start)
		return &StepOutput{Result: result}
	}

	childDir, err := ResolveSuiteDir(r.NetworksBase, step.Suite)
	if err != nil {
		result.Status = StepStatusError
		result.Message = fmt.Sprintf("run-suite %q: %v", step.Suite, err)
		result.Duration = time.Since(start)
		return &StepOutput{Result: result}
	}
	child := &Runner{
		SuiteDir:           childDir,
		NetworksBase:     r.NetworksBase,
		ServerURL:          r.ServerURL,
		NetworkID:          r.NetworkID,
		Client:             r.Client,
		NewtlabURL:         r.NewtlabURL,
		NewtlabClient:      r.NewtlabClient,
		HostConns:          r.HostConns,
		Progress:           r.Progress,
		Network:            r.Network,
		Dir:            r.Dir,
		discoveredPlatform: r.discoveredPlatform,
	}

	childCtx := withRunSuiteDepth(ctx, depth)
	results, err := child.Run(childCtx, RunOptions{
		All:        true,
		NoDeploy:   true, // parent already deployed
		Targets:    step.Targets,
		Parameters: step.Parameters,
	})
	result.Duration = time.Since(start)
	if err != nil {
		result.Status = StepStatusError
		result.Message = fmt.Sprintf("run-suite %q: %v", step.Suite, err)
		return &StepOutput{Result: result}
	}

	// Aggregate child scenario outcomes. The step succeeds iff every
	// child scenario completed cleanly; any failure or skip degrades
	// the step. Status precedence (worst-wins): error > failed > skipped
	// > passed — matches the parent run's aggregation in iterateScenarios.
	worst := StepStatusPassed
	failed := 0
	for _, sc := range results {
		switch sc.Status {
		case StepStatusError:
			worst = StepStatusError
			failed++
		case StepStatusFailed:
			if worst != StepStatusError {
				worst = StepStatusFailed
			}
			failed++
		case StepStatusSkipped:
			if worst == StepStatusPassed {
				worst = StepStatusSkipped
			}
		}
	}
	result.Status = worst
	if failed > 0 {
		result.Message = fmt.Sprintf("run-suite %q: %d of %d scenarios failed", step.Suite, failed, len(results))
	} else {
		result.Message = fmt.Sprintf("run-suite %q: %d scenarios passed", step.Suite, len(results))
	}
	return &StepOutput{Result: result}
}
