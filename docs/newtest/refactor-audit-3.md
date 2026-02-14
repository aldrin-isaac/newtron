# newtest Refactor Audit (Round 3)

**Date**: 2026-02-14
**Scope**: `cmd/newtest/`, `pkg/newtest/`
**Previous rounds**: round 1 (all HIGHs and most MEDIUMs resolved), round 2 (2 HIGHs, 6 MEDIUMs — all resolved)

---

## MEDIUM

### NT-M1: `intParam` silently ignores parse errors

**File**: `pkg/newtest/steps.go` lines 80-96
**Issue**: `intParam` extracts an integer from a step's YAML params map. For string values, it calls `strconv.Atoi` and silently discards the error:
```go
case string:
    n, _ := strconv.Atoi(val)
    return n
```
If a user writes `metric: "bad"` in a test scenario, the executor silently uses `0` instead of reporting a validation error. YAML numbers arrive as `float64` (handled correctly at line 88-89), so this only affects string-typed params — but that's exactly the case where validation matters most.

**Fix**: Either:
1. Change `intParam` signature to `intParam(...) (int, error)` and propagate errors, or
2. Validate numeric params at parse time in `ValidateScenario` by checking that string values for known numeric fields are valid integers.

Option 2 is cleaner since it catches errors before execution starts.

### NT-M2: `StateReporter` ignores `SaveRunState` errors

**File**: `pkg/newtest/progress.go` lines 263, 276, 289
**Issue**: The `StateReporter` wrapper calls `_ = SaveRunState(r.State)` three times (on scenario start, scenario end, and suite end), ignoring all errors. If state persistence fails (disk full, permissions, corrupt JSON), the runner doesn't know. This breaks pause/resume semantics: `newtest pause` relies on state.json being current.
```go
_ = SaveRunState(r.State)  // 3 times, all errors ignored
```
**Fix**: Log at warning level: `if err := SaveRunState(r.State); err != nil { util.Logger.Warnf("save run state: %v", err) }`. Don't fail the test run for state persistence errors, but make them visible.

### NT-M3: `resolveScenarioPath` returns first match without uniqueness check

**File**: `pkg/newtest/runner.go` lines ~604-621
**Issue**: When a scenario name doesn't match a file path or prefix glob, the function falls back to scanning all YAML files in the directory and parsing each to find a matching `name:` field. It returns the first match found. If two YAML files declare the same `name:`, the result depends on directory scan order — silently picking one without warning.

**Fix**: During the scan, collect all matches. If `len(matches) > 1`, return an error: `"ambiguous scenario name %q: found in %s and %s"`. This is a one-line check after the scan loop.

### NT-M4: `resolveDevices` can return empty slice, silently passing tests

**File**: `pkg/newtest/runner.go` lines 507-509
**Issue**: `resolveDevices` delegates to `step.Devices.Resolve(r.allDeviceNames())`. If no topology is loaded or no devices match the selector, this returns an empty slice. The three device-iteration helpers (`executeForDevices`, `checkForDevices`, `pollForDevices`) all loop over the result — with zero devices, the loop body never executes and the step reports `StepStatusPassed`. A test that intends to verify all devices silently passes with zero checks.

**Fix**: Add an early check in `executeForDevices`, `checkForDevices`, and `pollForDevices`:
```go
if len(names) == 0 {
    return &StepOutput{Result: &StepResult{
        Status:  StepStatusError,
        Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
    }}
}
```

---

## LOW

### NT-L1: Inconsistent status type usage in CLI color functions

**File**: `cmd/newtest/cmd_status.go` lines 183, 198
**Issue**: `colorRunStatus` takes `newtest.SuiteStatus` (typed constant) while `colorScenarioStatus` takes raw `string`. Both colorize status text for terminal output. The inconsistency means scenario status isn't type-checked at compile time.
**Fix**: Define `ScenarioStatus` as a typed string constant (like `SuiteStatus`), or have `colorScenarioStatus` accept `StepStatus`. Minor type safety improvement.

### NT-L2: Date format string repeated as magic constant

**Files**: `pkg/newtest/report.go` line 169, `cmd/newtest/cmd_status.go` line ~99
**Issue**: Both use `"2006-01-02 15:04:05"` as a literal string for timestamp formatting.
**Fix**: Define `const DateTimeFormat = "2006-01-02 15:04:05"` in a shared location and reference it.

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 0     |
| MEDIUM   | 4     |
| LOW      | 2     |

The newtest codebase is in good shape after rounds 1 and 2. The `checkForDevices`/`pollForDevices` dedup, dead `Parallel` removal, and previous refactors addressed the major structural issues. The remaining findings are validation gaps (`intParam`, empty device list) and observability issues (state persistence errors, ambiguous scenario names).
