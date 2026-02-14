# newtlab Refactor Audit (Round 2)

**Date**: 2026-02-14
**Scope**: `cmd/newtlab/`, `pkg/newtlab/`

---

## HIGH

### NL-H1: Three host-IP resolution functions with overlapping but inconsistent behavior

**Files**: `pkg/newtlab/newtlab.go` lines 842-878, `pkg/newtlab/link.go` lines 156-176
**Issue**: Three functions resolve host IPs with subtly different conventions:
- `nodeHostIP(ns)` — returns HostIP or "127.0.0.1" (runtime state → IP)
- `resolveHostIP(node, config)` — returns "" for local, resolved IP for remote (config → IP at allocation)
- `resolveWorkerHostIP(host, config)` — returns "127.0.0.1" for local, resolved IP for remote (host string → IP)

Plus `connectAddr()` in link.go reimplements the same logic inline with 4-level nesting. The empty-string-means-local convention is implicit and error-prone.
**Fix**: Consolidate into a single `resolveIP(host string, config *VMLabConfig) string` that always returns a usable IP ("127.0.0.1" for local). Callers never need to handle empty strings. `connectAddr` calls it directly.

### NL-H2: `destroyExisting` silently drops errors while `Destroy` collects them

**File**: `pkg/newtlab/newtlab.go` lines 785-808 vs 471-515
**Issue**: `Destroy()` properly collects errors into a slice and reports them. `destroyExisting()` (used during force-redeploy) prints to stderr and ignores everything except `RemoveState`. If a QEMU process can't be killed, the stale state is still removed, leaving orphan processes.
**Fix**: Collect errors like `Destroy()` does. Return them so `Deploy()` can decide whether to warn or abort.

---

## MEDIUM

### NL-M1: Parallel goroutine scaffolding duplicated 3 times

**File**: `pkg/newtlab/newtlab.go` — `startNodes` (lines 293-339), `bootstrapNodes` (lines 342-399), `applyNodePatches` (lines 401-450)
**Issue**: All three have the same pattern:
```go
var wg sync.WaitGroup
var mu sync.Mutex
var firstErr error
for name, node := range l.Nodes {
    if ns.Status != "running" { continue }
    wg.Add(1)
    go func(name string, ...) {
        defer wg.Done()
        err := doWork(...)
        if err != nil {
            mu.Lock()
            l.State.Nodes[name].Status = "error"
            if firstErr == nil { firstErr = err }
            mu.Unlock()
        }
    }(name, ...)
}
wg.Wait()
```
**Fix**: Extract `parallelForNodes(l *Lab, fn func(name string, node *NodeConfig, ns *NodeState) error) error` that handles wg/mu/firstErr/status-update boilerplate.

### NL-M2: `refreshBGP` swallows all errors silently

**File**: `pkg/newtlab/newtlab.go` lines 697-730
**Issue**: SSH dial failures and session errors are all `continue`'d. No logging. If BGP refresh fails on all devices, the user sees nothing — then wonders why routes aren't converging.
**Fix**: Log per-device results. At minimum: `util.Logger.Warnf("refreshBGP %s: %v", name, err)` on failure, `util.Logger.Infof("refreshBGP %s: done", name)` on success.

### NL-M3: `connectAddr` reimplements host resolution inline

**File**: `pkg/newtlab/link.go` lines 156-176
**Issue**: 4-level nested conditionals to resolve worker host IP. `resolveWorkerHostIP()` already does this — but `connectAddr` can't call it because it also needs the "same-host → 127.0.0.1" optimization. The inline logic is confusing.
**Fix**: Refactor to: `if vmHost == workerHost { return local } else { return resolveWorkerHostIP(workerHost, config) }`. Two lines instead of 20.

### NL-M4: Shell quoting logic split across files

**Files**: `pkg/newtlab/qemu.go` (quoteArgs), `pkg/newtlab/disk.go` (shellQuote/singleQuote), `pkg/newtlab/remote.go` (sshCommand)
**Issue**: Three separate quoting implementations. `quoteArgs` uses one escaping strategy, `shellQuote` uses single-quote wrapping with `'\''` escaping. They serve different purposes (QEMU args vs shell commands) but the inconsistency is fragile.
**Fix**: Consolidate into a single `shell.go` file with `ShellQuote(s string) string` and `JoinArgs(args []string) string`. Document when to use which.

---

## LOW

### NL-L1: Magic port arithmetic undocumented

**Files**: `pkg/newtlab/newtlab.go` line ~108, `pkg/newtlab/probe.go` line 94
**Issue**: Link ports use `LinkPortBase + i*2`, stats ports use `LinkPortBase - 1 - i`. The allocation scheme is correct but nowhere documented. A maintainer could accidentally overlap.
**Fix**: Add a block comment at the top of `probe.go` or in `VMLabConfig` documenting the full port layout.

### NL-L2: `StopNode` / `StopNodeRemote` have similar retry loops

**File**: `pkg/newtlab/qemu.go` lines ~163-217
**Issue**: Both functions have a 10-second retry loop polling for process exit. The loop is identical except for the check method (local `kill -0` vs SSH).
**Fix**: Extract `waitForExit(checkFn func() bool, timeout time.Duration)`. Minor cleanup.

### NL-L3: Verbose template rendering in `ApplyBootPatches`

**File**: `pkg/newtlab/patch.go` lines ~204-222
**Issue**: Template rendering is split across `renderTemplate()`, `renderString()`, and inline logic. The function is long but each piece is simple.
**Fix**: Extract `RenderPatch()` to encapsulate all template logic. Low priority since the code is correct and readable.
