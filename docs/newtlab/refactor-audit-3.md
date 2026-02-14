# newtlab Refactor Audit (Round 3)

**Date**: 2026-02-14
**Scope**: `cmd/newtlab/`, `cmd/newtlink/`, `pkg/newtlab/`
**Previous rounds**: round 1 (all HIGHs and most MEDIUMs resolved), round 2 (2 HIGHs, 4 MEDIUMs, 3 LOWs — all resolved)

---

## HIGH

### NL-H1: `Start()` doesn't check if QEMU process is already running

**File**: `pkg/newtlab/newtlab.go` lines 564-610
**Issue**: When a node is in "error" state (e.g., SSH timed out on a previous Start), the QEMU process may still be running (PID is valid, just SSH was unreachable). `Start()` reads the state, sees status="error", and proceeds to call `StartNode()` without checking `IsRunning(nodeState.PID, nodeState.HostIP)`. This launches a second QEMU for the same node, which either fails on port conflicts or creates an orphan process.

**Flow when SSH times out**:
1. `StartNode()` launches QEMU → PID saved (line 597), status="running" (line 598)
2. `WaitForSSH()` times out → status changed to "error" (line 604), `SaveState()` called (line 605)
3. Next `Start()` call → doesn't check if PID is still alive → calls `StartNode()` again

**Fix**: Before calling `StartNode()`, check if the old process is still running:
```go
if nodeState.PID > 0 && IsRunning(nodeState.PID, nodeState.HostIP) {
    // Process still alive — skip QEMU launch, just wait for SSH
    goto waitSSH
}
```
Or better: if running, skip StartNode entirely and just re-try SSH. If not running, proceed with full StartNode flow.

---

## MEDIUM

### NL-M1: Shell quoting functions scattered across 4 files

**Files**: `pkg/newtlab/disk.go` (lines 70-83: `shellQuote`, `singleQuote`), `pkg/newtlab/qemu.go` (lines 255-262: `quoteArgs`), used in `bridge.go` and `remote.go`
**Issue**: Three related quoting functions live in different files:
- `shellQuote(path string) string` — wraps path for remote shell, handles `~/` prefix (disk.go)
- `singleQuote(s string) string` — single-quote wrapping with `'\\''` escaping (disk.go)
- `quoteArgs(args []string) string` — applies `singleQuote` to each arg (qemu.go)

These are called from 4 files (disk.go, qemu.go, bridge.go, remote.go). If quoting logic needs to change (e.g., handle special characters differently), updates must touch multiple files.

**Fix**: Move all three to a `pkg/newtlab/shell.go` file. No behavior change, just consolidation.

### NL-M2: `Start()` ignores `SaveState` error on SSH timeout

**File**: `pkg/newtlab/newtlab.go` line 605
**Issue**: When SSH times out during `Start()`, the node status is set to "error" and `SaveState(state)` is called — but the error return is ignored:
```go
nodeState.Status = "error"
SaveState(state)      // error dropped
return err
```
If `SaveState` fails (disk full, permissions), the error status is lost. The next `Start()` call may see stale state with status="running" and PID that no longer matches the actual process.

**Fix**: Check and wrap the error: `if saveErr := SaveState(state); saveErr != nil { return fmt.Errorf("save state after SSH failure: %w (original: %v)", saveErr, err) }`

---

## LOW

### NL-L1: Empty-string-means-local convention implicit in node host handling

**File**: `pkg/newtlab/newtlab.go` lines 318-321
**Issue**: `startNodes` sets `remoteIP := ""` and only assigns a real IP if `node.Host != ""`. The empty string is then passed to `StartNode`, `StopNode`, `IsRunning` which all use `""` to mean "local". The convention works but is undocumented — a new contributor could mistake `""` for "not set" vs "local host".
**Fix**: Add a one-line comment: `// remoteIP="" signals local execution in StartNode/StopNode/IsRunning`

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 1     |
| MEDIUM   | 2     |
| LOW      | 1     |

The newtlab codebase is in solid shape after rounds 1 and 2. The `resolveHostIP` consolidation, `parallelForNodes` helper, `destroyExisting` error collection, and `refreshBGP` logging addressed the major structural issues. The remaining `Start()` state handling and shell quoting scatter are the key findings.
