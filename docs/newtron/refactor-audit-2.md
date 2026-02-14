# Newtron Refactor Audit (Round 2)

**Date**: 2026-02-14
**Scope**: `cmd/newtron/`, `pkg/network/`, `pkg/device/`, `pkg/spec/`, `pkg/auth/`, `pkg/audit/`, `pkg/cli/`, `pkg/util/`, `pkg/settings/`, `pkg/operations/`, `pkg/health/`, `pkg/configlet/`, `pkg/version/`

---

## HIGH

### NT-H1: Verify executors in steps.go duplicate executeForDevices pattern manually

**Files**: `pkg/newtest/steps.go` — 7 executors at lines 358, 477, 566, 666, 995 + others
**Issue**: `executeForDevices()` (line 118) provides a clean pattern for running ops across devices. But 7 executors manually reimplement the same boilerplate: `var details []DeviceResult`, `allPassed := true`, device loop, error append, final status computation. These are `verifyConfigDB`, `verifyStateDB`, `verifyBGP`, `verifyHealth`, `sshCommand`, `verifyProvisioning`, and `verifyRoute`.
**Note**: This is listed in newtest's audit since steps.go is in `pkg/newtest/`, but it affects newtron's verification layer. Cross-reference with `docs/newtest/refactor-audit-2.md` NT-H1.

### NT-H2: Network.GetXXX boilerplate — 12 identical map-lookup methods

**File**: `pkg/network/network.go` lines 69-220
**Issue**: 12 methods (GetService, GetFilterSpec, GetRegion, GetSite, GetPlatform, GetPrefixList, GetPolicer, GetQoSPolicy, GetQoSProfile, GetIPVPN, GetMACVPN, GetRoutePolicy) all follow the exact same pattern: RLock, map lookup, "not found" error, return. ~150 lines of pure boilerplate.
**Fix**: A single generic helper:
```go
func getFromMap[V any](mu *sync.RWMutex, m map[string]V, kind, name string) (V, error) {
    mu.RLock()
    defer mu.RUnlock()
    v, ok := m[name]
    if !ok {
        var zero V
        return zero, fmt.Errorf("%s '%s' not found", kind, name)
    }
    return v, nil
}
```
Each method becomes a one-liner. Reduces 150 lines to ~36.
**Note**: This was previously flagged as N-16 and marked over-engineering. The difference: N-16 proposed generics for `Get*` methods _on Device_. These Network methods are truly identical — same lock, same map, same error — making the generic version a strict improvement with zero abstraction cost.

---

## MEDIUM

### NT-M1: Shell command dispatch uses switch statement

**File**: `cmd/newtron/shell.go` lines 59-83
**Issue**: 12-case switch for shell commands. Adding a new command means modifying the switch. A command map would be more extensible and self-documenting.
**Fix**: `commands map[string]func(args []string)` initialized in `NewShell()`.

### NT-M2: `connectAddr` has confusing nested conditionals for host resolution

**File**: `pkg/newtlab/link.go` lines 156-176
**Issue**: 4-level nesting to resolve a worker host IP. The empty-string-means-local convention requires a separate fallback to check `config.Hosts["local"]`. Hard to follow.
**Fix**: Consolidate with `resolveWorkerHostIP()` which already handles the same logic — call it here instead of reimplementing inline.

### NT-M3: `destroyExisting` silently drops all errors

**File**: `pkg/newtlab/newtlab.go` lines 785-808
**Issue**: Every operation in `destroyExisting()` logs to stderr but never returns errors (except `RemoveState`). Compare with `Destroy()` which collects and returns errors. If QEMU processes fail to stop, the caller has no idea.
**Fix**: Collect errors and return them. Callers can choose to warn-and-continue.

### NT-M4: `bootstrapNodes` goroutines duplicate the same wg+mu+firstErr pattern

**File**: `pkg/newtlab/newtlab.go` lines 342-399
**Issue**: Two nearly identical goroutine blocks (console bootstrap + SSH wait) with the same wg/mu/firstErr scaffolding. This pattern appears in `startNodes` too.
**Fix**: Extract a `parallelForNodes(nodes, fn)` helper that handles the wg/mu/firstErr boilerplate.

### NT-M5: `refreshBGP` silently ignores all SSH errors

**File**: `pkg/newtlab/newtlab.go` lines 697-730
**Issue**: Every SSH error is silently swallowed (`continue`). If all BGP refreshes fail, the user gets no feedback. The function doesn't even log which devices succeeded or failed.
**Fix**: Log per-device success/failure via `util.Logger.Infof`/`Warnf`.

---

## LOW

### NT-L1: Placeholder dash assignment repeated ~30 times in CLI

**Files**: Multiple `cmd/newtron/cmd_*.go` files
**Issue**: Pattern `if v == "" { v = "-" }` appears everywhere in list/show commands.
**Fix**: Add `func displayOrDash(s string) string` to `cmd/newtron/main.go`.

### NT-L2: `splitConfigDBKey` is unexported but used across multiple ops files

**File**: `pkg/network/device.go` line ~484
**Issue**: Used in `portchannel_ops.go`, `vlan_ops.go`, etc. It works fine since they're all in the same package, but the function's location in `device.go` isn't intuitive.
**Fix**: Move to `changeset.go` or a dedicated `keys.go` — it's a config key utility, not a device method.

### NT-L3: Section separator comments throughout ops files

**Files**: `pkg/network/*_ops.go`, `pkg/newtest/steps.go`
**Issue**: Heavy `// ============` separator bars add visual noise. Modern editors and Go tooling make navigation easy without them.
**Fix**: Replace with simple `// --- Section Name ---` or remove entirely.

### NT-L4: `configdb_parsers.go` — 42 entries with no validation coverage

**File**: `pkg/device/configdb_parsers.go`
**Issue**: If a new ConfigDB field is added to the struct but the parser isn't registered, the field will silently be empty. No build-time or test-time check.
**Fix**: Add a test that uses reflection to verify every map field in `ConfigDB` has a parser entry.

### NT-L5: Inconsistent log context: `util.Logger.Infof` vs `util.WithDevice().Infof`

**Files**: Various `pkg/network/*_ops.go`
**Issue**: Some ops use `util.WithDevice(d.name).Infof(...)`, others use bare `util.Logger.Infof(...)`. The contextual form is better for debugging.
**Fix**: Standardize to `util.WithDevice()` in all ops methods.
