# newtest Refactor Audit (Round 2)

**Date**: 2026-02-14
**Scope**: `cmd/newtest/`, `pkg/newtest/`

---

## HIGH

### NT-H1: 7 verify/command executors manually reimplement executeForDevices pattern

**File**: `pkg/newtest/steps.go` — lines 356, 475, 566, 666, 993 and partially 292, 727
**Issue**: `executeForDevices()` (line 118) cleanly handles device iteration, error collection, and status computation. But 7 executors reimplement the exact same boilerplate:
```go
devices := r.resolveDevices(step)
var details []DeviceResult
allPassed := true
for _, name := range devices {
    dev, err := r.Network.GetDevice(name)
    if err != nil {
        details = append(details, DeviceResult{...StatusError...})
        allPassed = false
        continue
    }
    // ... executor-specific logic ...
    details = append(details, DeviceResult{...})
    if result.status != StatusPassed { allPassed = false }
}
status := StatusPassed
if !allPassed { status = StatusFailed }
return &StepOutput{Result: &StepResult{Status: status, Details: details}}
```
The 7 executors are: `verifyConfigDB`, `verifyStateDB`, `verifyBGP`, `verifyHealth`, `sshCommand`, `verifyProvisioning`, `verifyRoute`.

**Fix**: Extend `executeForDevices` or create `executeForDevicesRaw` that takes a callback returning `(Status, string, error)` per device and handles all the wrapping. The polling executors (stateDB, BGP, health, route) can use a `pollForDevices` variant that adds timeout/interval handling. This would eliminate ~200 lines of duplicated scaffolding.

### NT-H2: `RunOptions.Parallel` field is parsed but never used

**File**: `pkg/newtest/runner.go` line 38, `cmd/newtest/cmd_start.go` line 73
**Issue**: The `--parallel` flag is wired up and stored in `RunOptions.Parallel` but the runner never reads it. Users setting `--parallel 4` get no effect — silently misleading.
**Fix**: Either implement parallel scenario execution or remove the flag and field. If removing, add a comment noting it's a future feature.

---

## MEDIUM

### NT-M1: Status vs RunStatus — two status type hierarchies

**Files**: `pkg/newtest/report.go` lines 13-19, `pkg/newtest/state.go` lines 12-24
**Issue**: `Status` (PASS/FAIL/SKIP/ERROR) is for steps/scenarios. `RunStatus` (running/pausing/paused/complete/aborted/failed) is for suite lifecycle. Both use `string` as the underlying type. The naming is confusing: `StatusPassed` vs `StatusRunning` vs `StatusRunFailed`. `StatusRunFailed` exists specifically to avoid collision with `StatusFailed`.
**Fix**: Rename `Status` → `StepStatus` and `RunStatus` → `SuiteStatus` for clarity. The collision workaround (`StatusRunFailed`) becomes unnecessary as `SuiteStatusFailed`.

### NT-M2: `idx := i` shadowing appears 3 times in iterateScenarios

**File**: `pkg/newtest/runner.go` lines 173, 194, 199
**Issue**: Each use of `idx := i` captures the loop variable for a closure. This is correct Go (needed pre-1.22, and still needed in for-range over slices with explicit index), but the repeated pattern is noisy. The variable name `idx` adds nothing over `i`.
**Fix**: With Go 1.22+ loop variable semantics, `i` can be captured directly in closures. Replace all three `idx := i` with direct `i` usage since the project uses Go 1.24.

### NT-M3: `pollLoop` label in pollUntil is unnecessary

**File**: `pkg/newtest/steps.go` line 158
**Issue**: The label `pollLoop:` with `break pollLoop` is used to break out of the for loop from within a select. However, `break` inside a `select` already breaks the select, not the for loop — so the label IS needed. But the name is misleading since it labels the `for`, not the `select`.
**Note**: On re-examination, the label is actually correct and necessary — `break` inside `select` only breaks the `select`, so the label is needed to break the `for` loop. This is NOT a bug. Keep as-is or rename to a clearer label name.
**Severity**: Downgrade to LOW — label is functionally correct, just could have a better name.

### NT-M4: Verify executors with polling duplicate timeout/interval resolution

**File**: `pkg/newtest/steps.go` — `verifyStateDB` (480-481), `verifyBGP` (570-571), `verifyHealth` (669-670), `verifyRoute` (732-733)
**Issue**: Each polling executor independently reads `step.Expect.Timeout` and `step.Expect.PollInterval`. If defaults need to change or validation needs to be added, it must be done in 4 places.
**Fix**: Extract `resolvePollingParams(step) (timeout, interval time.Duration)` that applies defaults and validation once.

---

## LOW

### NT-L1: Section separator comments in steps.go

**File**: `pkg/newtest/steps.go` — 38 instances of `// ======...`
**Issue**: Heavy separator bars before each executor. With 38 executors in 1628 lines, these add significant visual noise.
**Fix**: Replace with `// --- executorName ---` one-liners, or remove entirely and rely on IDE navigation.

### NT-L2: `checkResult` struct is local to verifyConfigDBExecutor

**File**: `pkg/newtest/steps.go` lines 398-401
**Issue**: The type `checkResult{status, message}` is only used by `verifyConfigDBExecutor.checkDevice()`. It's a reasonable local type but could be shared with other executors that do similar per-device checks.
**Fix**: If NT-H1 is addressed (executeForDevices expansion), this type becomes unnecessary. No action needed independently.

### NT-L3: Progress callback closures are verbose

**File**: `pkg/newtest/runner.go` — lines 174, 195, 200, 209
**Issue**: Every progress call uses `r.progress(func(p ProgressReporter) { p.Method(args) })`. The closure wrapping is repetitive.
**Fix**: Add convenience methods like `r.progressScenarioStart(name, idx, total)` that handle the nil check and closure. Minor cleanup.

### NT-L4: Hardcoded status strings in progress.go

**File**: `pkg/newtest/progress.go`
**Issue**: Some status comparisons use string literals instead of the `Status` constants.
**Fix**: Replace with constant references. Minor consistency fix.
