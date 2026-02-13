# newtest Code Quality Audit

**Date**: 2026-02-13
**Scope**: `cmd/newtest/` (9 files) and `pkg/newtest/` (11 files)
**Method**: Line-by-line manual review of every file

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 6     |
| MEDIUM   | 18    |
| LOW      | 11    |

The most significant structural issue is the extreme code duplication in `steps.go`, where 25+ executor implementations follow an identical boilerplate pattern. The second major concern is duplicated logic between the `run` and `start` CLI commands. Error handling has several instances where errors are silently discarded.

---

## Findings by File

### cmd/newtest/main.go

No issues. Clean, minimal root command setup.

---

### cmd/newtest/cmd_run.go

**R-01. Dead code / unreachable branch** (lines 60-65)
- Category: Dead code
- Severity: LOW
- The `errors.As` check at line 61 and the `else` at line 63 both set `hasInfraError = true`. The entire `if/else` block is equivalent to `hasInfraError = true`. The `errors.As` check has no effect.
- Remediation: Simplify to `hasInfraError = true` or remove the `errors.As` distinction.

**R-02. Swallowed errors on report generation** (lines 48-49, 52-53)
- Category: Missing error handling
- Severity: MEDIUM
- `gen.WriteMarkdown(...)` and `gen.WriteJUnit(...)` return errors that are discarded with `_ =`. If the report write fails, the user gets no feedback.
- Remediation: Log a warning on failure (e.g., `fmt.Fprintf(os.Stderr, "warning: write report: %v\n", err)`).

**R-03. Hardcoded report path** (line 48)
- Category: Configuration/magic numbers
- Severity: LOW
- `"newtest/.generated/report.md"` is hardcoded. Not configurable.
- Remediation: Derive from `--dir` or add a `--report` flag.

**R-04. Code duplication with cmd_start.go** (lines 25-81 vs cmd_start.go lines 41-173)
- Category: Code duplication
- Severity: HIGH
- The `run` command and `start` command duplicate: verbose setup, directory resolution, runner construction, result analysis, report generation, and exit code logic. The `run` command is marked as deprecated but still maintained in parallel.
- Remediation: Either remove `run` entirely (since it is `Hidden: true` and deprecated) or extract the shared logic into a helper function that both commands call.

---

### cmd/newtest/cmd_start.go

**S-01. Indentation error** (line 130)
- Category: Structural issues
- Severity: LOW
- `fmt.Fprintf(os.Stderr, ...)` at line 130 is indented with a single tab instead of two, breaking visual alignment with the surrounding if-block.
- Remediation: Fix indentation.

**S-02. Swallowed errors on state persistence** (lines 128, 136, 156)
- Category: Missing error handling
- Severity: MEDIUM
- `_ = newtest.SaveRunState(state)` is called three times with errors discarded. State persistence failures are invisible to the user.
- Remediation: At minimum log a warning when state save fails.

**S-03. Swallowed errors on report generation** (lines 160, 162)
- Category: Missing error handling
- Severity: MEDIUM
- Same issue as R-02. Report write errors discarded silently.
- Remediation: Log a warning.

**S-04. `os.Exit()` inside RunE** (lines 167-170)
- Category: Structural issues
- Severity: MEDIUM
- Calling `os.Exit()` inside a Cobra `RunE` bypasses deferred cleanup (line 110: `defer func() { _ = newtest.ReleaseLock(state) }()`). The lock release will not execute when `os.Exit()` is called.
- Remediation: Return a typed error and handle exit codes in `main()`, or move `os.Exit()` after `defer` cleanup in `main()`.

---

### cmd/newtest/cmd_pause.go

**P-01. Duplicate function: `isProcAlive`** (lines 95-100)
- Category: Code duplication
- Severity: MEDIUM
- `isProcAlive` in `cmd/newtest/cmd_pause.go` is functionally identical to `isProcessAlive` in `pkg/newtest/state.go`. Both use `syscall.Kill(pid, 0)`.
- Remediation: Export `IsProcessAlive` from `pkg/newtest` and use it in the CLI. Delete the CLI copy.

---

### cmd/newtest/cmd_stop.go

**T-01. Inconsistent suite resolver** (lines 80-97)
- Category: Code duplication
- Severity: MEDIUM
- `resolveSuiteForStop` duplicates logic from `resolveSuiteForControl` (in `cmd_pause.go`) with slightly different filtering. Both resolve suites from `ListSuiteStates()`, one filters for active status, the other does not.
- Remediation: Unify into a single `resolveSuite(filter func(RunStatus) bool)` helper, or parameterize `resolveSuiteForControl` with a filter predicate.

---

### cmd/newtest/cmd_list.go

**L-01. Anonymous struct duplication for topology parsing** (lines 23-26)
- Category: Code duplication
- Severity: LOW
- `topoCounts` at line 23 and `newTopologiesCmd` at line 40 both define anonymous structs with `Devices map[string]json.RawMessage` and `Links []json.RawMessage`. These are near-identical inline struct definitions.
- Remediation: Extract a shared `topoSummary` struct or a parsing helper.

---

### cmd/newtest/cmd_topologies.go

**TO-01. Hardcoded description truncation** (line 51)
- Category: Configuration/magic numbers
- Severity: LOW
- `if len(desc) > 50` with truncation to `47 + "..."` is hardcoded.
- Remediation: Minor. Could be a constant but low impact.

---

### cmd/newtest/cmd_status.go

**ST-01. Hardcoded status strings** (lines 137-147)
- Category: Inconsistent patterns
- Severity: MEDIUM
- Scenario status is compared against raw strings `"PASS"`, `"FAIL"`, `"ERROR"`, `"SKIP"` instead of using the `Status` constants from `pkg/newtest`. The `ScenarioState.Status` field is `string` rather than `Status`, creating a type mismatch.
- Remediation: Either change `ScenarioState.Status` to `newtest.Status` type, or at minimum use `string(newtest.StatusPassed)` etc. for comparisons.

**ST-02. No-op assignment** (lines 130-132)
- Category: Dead code
- Severity: LOW
- `dur := sc.Duration; if dur == "" { dur = "" }` -- the if-body is a no-op.
- Remediation: Remove the dead if-block.

---

### pkg/newtest/scenario.go

**SC-01. `validActions` map redundant with `executors` map** (lines 107-146)
- Category: Code duplication
- Severity: MEDIUM
- `validActions` is a `map[StepAction]bool` that must be kept in sync with the `executors` map in `steps.go`. Every time a new action is added, both maps must be updated. If they diverge, a valid action could pass parsing but fail at runtime, or vice versa.
- Remediation: Derive `validActions` from `executors` at init time: `func init() { for k := range executors { validActions[k] = true } }`, or just check `executors` directly in the validator.

---

### pkg/newtest/parser.go

**PA-01. `requireDevices` used inconsistently** (lines 108-211 vs 212-406)
- Category: Inconsistent patterns
- Severity: MEDIUM
- The first 7 action cases in `validateStepFields` use inline `if !step.Devices.All && len(step.Devices.Devices) == 0` checks, while all subsequent cases (from `ActionApplyFRRDefaults` onward) use the `requireDevices()` helper.
- Remediation: Convert all inline device checks to use `requireDevices()`.

**PA-02. `validateStepFields` is a 300-line switch statement** (lines 104-409)
- Category: Structural issues
- Severity: MEDIUM
- The switch statement has 39 cases and grows with every new action. Each case is a sequence of `requireDevices` + `requireParam` calls.
- Remediation: Define a declarative validation table: `map[StepAction]struct{ needsDevices bool; requiredParams []string; needsInterface bool; ... }` and validate from the table. Add an escape hatch for actions with non-standard validation (e.g., `verify-route` requiring exactly one device).

**PA-03. `HasRequiresExported` / `TopologicalSortExported` naming** (lines 487-498)
- Category: Naming inconsistencies
- Severity: LOW
- These exported wrappers have the suffix `Exported` which is not idiomatic Go. They exist solely to expose internal functions for use in `cmd/newtest/cmd_list.go`.
- Remediation: Either export the underlying functions directly (rename `hasRequires` to `HasRequires`, `topologicalSort` to `TopologicalSort`) or restructure so the CLI calls a higher-level API instead.

**PA-04. Redundant topological sort in `TopologicalSortExported`** (lines 493-498)
- Category: Code duplication
- Severity: LOW
- `TopologicalSortExported` calls `validateDependencyGraph` (which itself calls `topologicalSort` internally for cycle detection) and then calls `topologicalSort` again.
- Remediation: Have `validateDependencyGraph` return the sorted result, or cache/skip the double sort.

**PA-05. Default timeout/interval values are magic numbers** (lines 517-551)
- Category: Configuration/magic numbers
- Severity: LOW
- Default timeouts (120s, 60s, 30s) and poll intervals (5s) are hardcoded in `applyDefaults`. They appear again as fallbacks in the BGP executor (steps.go lines 516-522).
- Remediation: Define named constants: `const DefaultBGPTimeout = 120 * time.Second` etc.

---

### pkg/newtest/runner.go

**RU-01. Massive code duplication between `runShared` and `runIndependent`** (lines 141-290 vs 294-365)
- Category: Code duplication
- Severity: HIGH
- Both methods contain nearly identical: resume-skip logic (8 lines), pause-check logic (3 lines), requires-check logic (9 lines), status-tracking logic, and progress-reporting wrappers. The only difference is that `runShared` deploys once and connects once at the top, while `runIndependent` calls `RunScenario` per iteration.
- Remediation: Extract the scenario iteration loop (pause check, resume check, requires check, progress hooks) into a shared `runScenarios(scenarios, fn)` method that takes a per-scenario callback.

**RU-02. Duplicated deploy/connect logic** (lines 145-183 in `runShared` vs 387-410 in `RunScenario`)
- Category: Code duplication
- Severity: HIGH
- The deploy logic (lifecycle vs legacy mode check, error result wrapping) is duplicated between `runShared` and `RunScenario`. Both have the same `if opts.Suite != "" { EnsureTopology } else { DeployTopology }` pattern with identical error handling.
- Remediation: Extract a `deployTopology(specDir, opts) (*newtlab.Lab, error)` helper.

**RU-03. `checkRequires` does not check for unresolved requirements** (lines 638-645)
- Category: Missing error handling
- Severity: LOW
- If a scenario requires "x" but "x" has not been run yet (not in the `status` map at all), `checkRequires` returns `""` (no skip). This means ordering bugs could silently allow a scenario to run without its prerequisite completing.
- Remediation: Consider treating a missing entry as "not yet passed" and returning a skip reason.

**RU-04. `Runner` exported fields create large API surface** (lines 17-28)
- Category: Structural issues
- Severity: LOW
- `Runner.Network`, `Runner.Lab`, `Runner.ChangeSets`, `Runner.Verbose` are all exported but appear to be used only internally by the runner itself (and test assertions). This makes the Runner's API surface larger than necessary.
- Remediation: Unexport fields that are not part of the intended public API.

---

### pkg/newtest/steps.go

**SE-01. Extreme boilerplate duplication across 25+ executors** (entire file, ~2562 lines)
- Category: Code duplication
- Severity: HIGH
- Nearly all mutating executors (createVLAN, deleteVLAN, createVRF, deleteVRF, addVLANMember, removeVLANMember, setupEVPN, addVRFInterface, removeVRFInterface, bindIPVPN, unbindIPVPN, bindMACVPN, unbindMACVPN, addStaticRoute, removeStaticRoute, applyQoS, removeQoS, configureSVI, refreshService, applyBaseline, applyFRRDefaults, restartService, cleanup) follow an identical pattern:
  1. `r.resolveDevices(step)`
  2. Init `details []DeviceResult`, `changeSets map[...]`, `allPassed := true`
  3. Extract params
  4. For each device: `r.Network.GetDevice(name)` -> error handling -> `dev.ExecuteOp(...)` -> error handling -> append result
  5. Compute `status` from `allPassed`
  6. Return `StepOutput`
- This pattern is repeated 20+ times with only the inner operation varying. The file is ~2562 lines; extracting this would reduce it to ~800.
- Remediation: Create a generic `executeForDevices` helper:
  ```go
  func executeForDevices(r *Runner, step *Step, fn func(dev *network.Device) (*network.ChangeSet, string, error)) *StepOutput
  ```
  Each executor becomes a 5-10 line function that extracts params and delegates to `executeForDevices`.

**SE-02. Polling pattern duplicated in verify executors** (verifyStateDB, verifyBGP, verifyRoute)
- Category: Code duplication
- Severity: MEDIUM
- Three executors implement the same poll-until-timeout-or-match loop: `deadline := time.Now().Add(timeout)` / `for time.Now().Before(deadline)` / `select` with context.
- Remediation: Extract a `pollUntil(ctx, timeout, interval, fn) error` helper.

**SE-03. verifyStateDB poll loop has a `break` inside `select` that does not exit outer `for`** (lines 467-477)
- Category: Missing error handling
- Severity: HIGH
- At line 476, `break` inside `case <-ctx.Done()` only breaks out of the `select`, not the enclosing `for` loop. When the context is cancelled, the loop continues to the next iteration (where `time.Now().Before(deadline)` may still be true), potentially retrying after cancellation.
- The same bug exists in verifyBGP (lines 571-579).
- Remediation: Use a labeled break (`break pollLoop`) or set a flag and check it.

**SE-04. verifyBGP re-defaults timeout/interval despite `applyDefaults`** (lines 516-522)
- Category: Inconsistent patterns
- Severity: LOW
- `applyDefaults` already sets BGP timeout to 120s and interval to 5s, but the verifyBGP executor re-checks and applies the same defaults. This is defensive but duplicative.
- Remediation: Remove the redundant defaults in the executor, or document that executors must not rely on `applyDefaults`.

**SE-05. verifyBGP ignores `step.Expect.State`** (lines 558-562)
- Category: Missing error handling
- Severity: MEDIUM
- The executor computes `expectedState` at line 559 but never actually checks BGP peer state against it. It only checks if all health checks pass (`hc.Status == "pass"`). The `expectedState` variable is used only in the success message. A scenario requesting `state: "Connect"` would still pass if BGP is Established.
- Remediation: Actually verify the reported state matches `step.Expect.State`.

**SE-06. verifyBGP `!matched && allPassed` guard** (lines 582-583)
- Category: Missing error handling
- Severity: MEDIUM
- The timeout failure branch is guarded by `!matched && allPassed`. If an earlier device errored (setting `allPassed = false`), a subsequent device's timeout will not be reported. The same pattern appears in verifyStateDB (line 479).
- Remediation: The guard should be `!matched` alone. Errors for earlier devices are already recorded in `details`.

---

### pkg/newtest/report.go

**RE-01. `StatusSkipped` not handled in `computeOverallStatus`** (lines 592-606)
- Category: Missing error handling
- Severity: LOW
- If all steps are `StatusSkipped`, `computeOverallStatus` returns `StatusPassed`. This may be intentional but is worth documenting.
- Remediation: Add a comment explaining the design choice, or return `StatusSkipped` when all steps are skipped.

---

### pkg/newtest/deploy.go

No significant issues. Clean and minimal.

---

### pkg/newtest/state.go

**ST-01. `UserHomeDir` error discarded** (lines 49, 103)
- Category: Missing error handling
- Severity: MEDIUM
- `home, _ := os.UserHomeDir()` discards the error. If `HOME` is not set and the platform lookup fails, `home` is `""`, resulting in paths like `/.newtron/newtest/...` which would fail or write to the root filesystem.
- Remediation: Return an error from `StateDir()`, or panic on failure since it indicates a fundamentally broken environment.

---

### pkg/newtest/progress.go

**PR-01. `colorStatus` duplicated between `ConsoleProgress` and `cmd/newtest/cmd_status.go`** (lines 219-232 vs cmd_status.go lines 199-229)
- Category: Code duplication
- Severity: LOW
- Two independent color-status functions exist: `ConsoleProgress.colorStatus` (for step/scenario status) and `colorRunStatus`/`colorScenarioStatus` in the CLI. They map the same concepts with minor variations.
- Remediation: Consolidate status coloring into `pkg/cli` or export from `pkg/newtest`.

**PR-02. `StepStart` method is empty** (lines 104-106)
- Category: Dead code
- Severity: LOW
- `ConsoleProgress.StepStart` has only a comment and no implementation. It is called from the runner but does nothing.
- Remediation: Add a comment to the interface documenting that implementers may choose to ignore this callback, or remove the call if unused.

---

### pkg/newtest/errors.go

No significant issues. Error types are well-structured with proper `Unwrap` methods.

---

### pkg/newtest/newtest_test.go

**TE-01. No tests for any executor implementation** (entire file)
- Category: Missing tests
- Severity: HIGH
- The 81 tests cover parsing, validation, topological sort, param helpers, report formatting, and state management. However, there are zero tests for any of the 39 executor implementations in `steps.go`. The executors contain significant logic (polling loops, route matching, SSH commands, service operations) that is completely untested at the unit level.
- Remediation: Add unit tests for executor logic. Many executors can be tested with mock `Runner` / `network.Device` implementations. At minimum, test:
  - `waitExecutor` (straightforward timer test)
  - `matchRoute` (already tested) and `parsePingSuccessRate` (already tested)
  - The polling loop logic (extracted as a helper per SE-02)
  - Error propagation in the device iteration pattern

**TE-02. No tests for `runner.go` `Run` / `RunScenario` methods**
- Category: Missing tests
- Severity: MEDIUM
- The core orchestration logic (shared vs independent mode, resume/pause, deploy/connect) has no unit tests. These would require mocking `network.Network` and `newtlab.Lab` but are critical for correctness.
- Remediation: Add integration-level tests using test doubles for the network and lab layers.

---

### pkg/newtest/state_test.go

Good coverage of state persistence, locking, and pause detection. No issues found.

---

## Cross-File Findings

**X-01. `os.Exit()` bypasses deferred cleanup in both CLI commands** (cmd_run.go and cmd_start.go)
- Category: Structural issues
- Severity: MEDIUM
- Both `run` and `start` commands call `os.Exit(1)` or `os.Exit(2)` inside their `RunE` functions. This bypasses deferred cleanup including lock release.
- Remediation: Return a sentinel error from `RunE` and handle exit codes in `main()`.

**X-02. No integration between `validActions` and `executors`**
- Category: Inconsistent patterns
- Severity: MEDIUM
- `validActions` in `scenario.go` and `executors` in `steps.go` must be kept manually in sync. Adding a new action requires updating both maps, the `StepAction` constant list, and the `validateStepFields` switch. Four locations must stay synchronized.
- Remediation: Derive `validActions` from `executors`, and consider using a registration pattern or code generation.

**X-03. `topology` resolution duplicated in status/stop commands**
- Category: Code duplication
- Severity: LOW
- Both `printSuiteStatus` (cmd_status.go line 71-76) and `newStopCmd` (cmd_stop.go lines 43-49) have identical "infer topology from scenarios[0]" fallback logic.
- Remediation: Extract a `resolveTopologyFromState(state)` helper.

---

## File Coverage Summary

| File | Lines | Tests? | Test Coverage Notes |
|------|-------|--------|---------------------|
| `pkg/newtest/scenario.go` | 210 | Yes | DeviceSelector, ExpectBlock well tested |
| `pkg/newtest/parser.go` | 553 | Yes | Parse, validate, toposort, defaults well tested |
| `pkg/newtest/runner.go` | 686 | No | Core orchestration untested |
| `pkg/newtest/steps.go` | 2562 | Partial | Only `matchRoute`, `parsePingSuccessRate`, `matchFields`, param helpers tested. 39 executors untested. |
| `pkg/newtest/report.go` | 339 | Yes | Console, JUnit, markdown tested |
| `pkg/newtest/deploy.go` | 61 | No | Would require newtlab mock |
| `pkg/newtest/errors.go` | 46 | No | Trivial types, tested indirectly |
| `pkg/newtest/state.go` | 162 | Yes | Thorough coverage in state_test.go |
| `pkg/newtest/progress.go` | 292 | No | UI formatting, hard to unit test |
| `pkg/newtest/newtest_test.go` | 1562 | N/A | 81 test functions |
| `pkg/newtest/state_test.go` | 256 | N/A | 10 test functions |

---

## Prioritized Remediation Plan

### Phase 1 (Correctness) -- HIGH priority
1. **SE-03**: Fix `break` inside `select` in verifyStateDB and verifyBGP poll loops
2. **SE-05**: Actually verify BGP state matches `step.Expect.State`
3. **SE-06**: Fix `!matched && allPassed` guard to `!matched`
4. **S-04 / X-01**: Move `os.Exit()` out of `RunE` to preserve deferred cleanup

### Phase 2 (Duplication reduction) -- HIGH priority
5. **SE-01**: Extract `executeForDevices` helper for 20+ identical executor boilerplate
6. **SE-02**: Extract `pollUntil` helper for verify executors
7. **R-04**: Remove deprecated `run` command or extract shared logic with `start`
8. **RU-01 / RU-02**: Extract shared scenario iteration loop and deploy helper

### Phase 3 (Quality) -- MEDIUM priority
9. **SC-01 / X-02**: Derive `validActions` from `executors`; reduce four sync points to one
10. **PA-01**: Convert all inline device checks to use `requireDevices`
11. **P-01**: Export `IsProcessAlive` and delete CLI duplicate
12. **T-01**: Unify `resolveSuiteForControl` and `resolveSuiteForStop`
13. **ST-01 (state.go)**: Handle `UserHomeDir` error
14. **ST-01 (cmd_status.go)**: Use typed status constants instead of raw strings
15. **S-02 / S-03 / R-02**: Log warnings on state/report write failures

### Phase 4 (Testing) -- MEDIUM priority
16. **TE-01**: Add unit tests for executor implementations (after extraction in Phase 2)
17. **TE-02**: Add orchestration tests for `Run`/`RunScenario` with test doubles

### Phase 5 (Polish) -- LOW priority
18. **PA-02**: Consider declarative validation table for `validateStepFields`
19. **PA-03**: Rename `HasRequiresExported` / `TopologicalSortExported`
20. **PA-04**: Eliminate double topological sort
21. **PA-05**: Extract timeout/interval constants
22. **S-01**: Fix indentation in cmd_start.go
23. **ST-02**: Remove no-op `dur` assignment in cmd_status.go
24. Various minor naming and magic number cleanups
