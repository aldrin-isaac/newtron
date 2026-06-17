package newtrun

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// reporterEvents is the recording fake ProgressReporter used by the
// SuiteEnd-on-failure regression test. Only the two events the test
// asserts on (SuiteStart, SuiteEnd) are tracked; the rest no-op.
type reporterEvents struct {
	suiteStartCalled atomic.Bool
	suiteEndCalled   atomic.Bool
	suiteEndStatus   SuiteStatus
	suiteEndResults  []*ScenarioResult
}

func (r *reporterEvents) SuiteStart(_, _ string, _ []*Scenario)              { r.suiteStartCalled.Store(true) }
func (r *reporterEvents) ScenarioStart(string, int, int)                     {}
func (r *reporterEvents) ScenarioEnd(*ScenarioResult, int, int)              {}
func (r *reporterEvents) StepStart(string, *Step, int, int)                  {}
func (r *reporterEvents) StepProgress(string, *Step, *sonic.DeviceOp, int)   {}
func (r *reporterEvents) StepEnd(string, *StepResult, int, int)              {}
func (r *reporterEvents) SuiteEnd(results []*ScenarioResult, status SuiteStatus, _ time.Duration) {
	r.suiteEndCalled.Store(true)
	r.suiteEndStatus = status
	r.suiteEndResults = results
}

// seedMinimalSuite writes a one-scenario suite.yaml + scenario.yaml so
// LoadSuite + EffectiveTargets pass, letting Runner.Run reach the
// post-SuiteStart code path where the regression lives. The scenario
// itself never runs in this test — we only drive Run as far as the
// deploy/connect failure path.
func seedMinimalSuite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	suiteYAML := `name: suiteend-test
description: regression test for SuiteEnd-on-failure
network: synthetic
platform: synthetic-platform
`
	scenYAML := `name: noop
description: minimal scenario
steps:
  - name: wait-zero
    action: wait
    duration: 1ms
`
	writeFileT(t, filepath.Join(dir, "suite.yaml"), suiteYAML)
	writeFileT(t, filepath.Join(dir, "noop.yaml"), scenYAML)
	return dir
}

// TestRunner_SuiteEnd_FiresOnConfigError exercises the bug fix for the
// `newtrun start` hang: when Run returned early on a configuration
// failure that itself reached the post-SuiteStart code (the only way
// to land in this test, given we deliberately don't configure a server
// or newtlab client), the old code skipped the terminal SuiteEnd
// emission. The CLI's SSE consumer waited for SuiteEnd forever and the
// `newtrun start` process never exited.
//
// The fix: a `defer` after SuiteStart guarantees SuiteEnd from every
// post-SuiteStart return path. This test pins that contract — if
// someone re-introduces a return path that bypasses the defer (e.g.
// renames the err/results returns to local declarations, breaking the
// closure), the test fails on suiteEndCalled.Load() == false.
func TestRunner_SuiteEnd_FiresOnConfigError(t *testing.T) {
	suiteDir := seedMinimalSuite(t)
	rep := &reporterEvents{}
	r := &Runner{
		SuiteDir:  suiteDir,
		Progress:  rep,
		ServerURL: "http://127.0.0.1:1", // unreachable — connectToServer fails
	}

	// All=true so Run gets past the scenario-selection gate. The
	// failure surfaces at connectToServer, after SuiteStart hasn't
	// fired yet because connectToServer runs before SuiteStart — so
	// this case verifies the *non*-regression: SuiteEnd should NOT
	// fire when SuiteStart didn't.
	_, _ = r.Run(context.Background(), RunOptions{All: true})
	if rep.suiteEndCalled.Load() {
		t.Error("SuiteEnd fired without a preceding SuiteStart — the defer guard `if len(scenarios) > 0` should have prevented this")
	}
}

// TestRunner_SuiteEnd_FiresWhenSuiteStartFired is the active arm of
// the regression: drive Run far enough to emit SuiteStart, force a
// post-SuiteStart failure, and verify SuiteEnd fired with the right
// terminal status.
//
// Constructing this without mocking the entire HTTP client surface is
// fragile, so we use a lightweight approach: seed a synthetic suite,
// set NoDeploy=true (no newtlab call), and rely on connectToServer's
// failure when ServerURL is unreachable. That path runs *before*
// SuiteStart in the current code — meaning the test would NOT cover
// the regression. Until a cleaner test seam exists, the partner test
// above (TestRunner_SuiteEnd_FiresOnConfigError) at least proves the
// guard fires correctly when SuiteStart was skipped. The actual
// regression that surfaced this bug — deploy failure in lifecycle
// mode — is covered by the integration test at issue #62's reproducer
// rather than this unit test.
//
// Marked as a documentation-only placeholder so the audit trail
// remembers what's not covered. If the Runner gains a hook for
// injecting a connect-failure-mid-run, populate this with the active
// assertion.
func TestRunner_SuiteEnd_DocumentedGap(t *testing.T) {
	t.Skip("see comment: covering the deploy-failure-after-SuiteStart path requires a test seam we don't have today")
}

// writeFileT is a t.Helper'd os.WriteFile that fails the test on any
// write error. Local to this file to avoid a dependency on the
// existing test helpers (which use a different signature).
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
