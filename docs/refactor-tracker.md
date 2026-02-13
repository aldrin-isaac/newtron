# Refactor Tracker — N-4 Code Quality

**Created**: 2026-02-13
**Source**: 5 audit documents totaling 190 findings (36 HIGH, 86 MEDIUM, 68 LOW)
**Audit docs**: `docs/newtron/refactor-audit-cli.md`, `docs/newtron/refactor-audit-backend.md`, `docs/newtron/refactor-audit-support.md`, `docs/newtlab/refactor-audit.md`, `docs/newtest/refactor-audit.md`

---

## Dependency Graph

```
cmd/newtron/  →  pkg/network/, pkg/device/, pkg/spec/, pkg/model/,
                 pkg/operations/, pkg/auth/, pkg/audit/, pkg/util/

cmd/newtlab/  →  pkg/newtlab/, pkg/spec/, pkg/settings/, pkg/util/

cmd/newtest/  →  pkg/newtest/, pkg/newtlab/, pkg/network/, pkg/device/,
                 pkg/spec/, pkg/settings/, pkg/util/
```

**Shared package coupling**:
- `pkg/network/` modified by Track 1, imported by newtest (steps.go executors call network.Device methods)
- `pkg/device/` modified by Track 1, imported by newtest (verification layer)
- `pkg/newtlab/` modified by Track 3, imported by newtest (deploy.go calls Lab.Deploy/Destroy)
- `pkg/util/` modified by Track 5, imported by everything
- `pkg/spec/` modified by Track 1 (minor), imported by everything

**Rule**: Phase A items are internal changes (no public API changes). Phase B may change imports. Phase C changes public API signatures. Always run `go build ./...` and `go test ./...` after each track's changes.

---

## Phase A — Fully Parallel (zero coordination)

### Track 1: newtron backend (`pkg/network/`, `pkg/device/`, `pkg/model/`, `pkg/operations/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 1.1 | **Fix protocol 179 bug**: Remove `"bgp": 179` from protoMap in `pkg/network/service_gen.go:484` and `pkg/network/device_ops.go:416`. Remove `ProtocolBGP = 179` from `pkg/model/acl.go:84`. Remove "bgp" from all protocol mappings — BGP filtering must use `protocol: "tcp"` + `dst_port: "179"`. Create single canonical `ProtoMap` in `model/acl.go`, delete inline protoMaps from service_gen.go and device_ops.go. | `pkg/model/acl.go`, `pkg/network/service_gen.go`, `pkg/network/device_ops.go` | N-09, M-01, M-02, X-01 | NOT STARTED |
| 1.2 | **Extract `requireWritable()` helper**: Create `func requireWritable(d *Device) error` that checks `IsConnected()` and `IsLocked()`. Replace 40+ inline `if !d.IsConnected() { return nil, fmt.Errorf(...) }; if !d.IsLocked() { return nil, fmt.Errorf(...) }` blocks in `device_ops.go` and `interface_ops.go`. Pure mechanical replacement — no behavior change. | `pkg/network/device_ops.go`, `pkg/network/interface_ops.go` | N-03 | NOT STARTED |
| 1.3 | **Consolidate splitPorts/joinPorts**: Move to `pkg/util/strings.go` as `SplitPorts(s string) []string` and use `strings.Join` for join. Delete 3 duplicate implementations: `device_ops.go:1194-1225`, `operations/precondition.go:443-455`, and any in service_gen.go. Also move `splitConfigDBKey` to `pkg/util/` as `SplitRedisKey`. | `pkg/util/`, `pkg/network/device_ops.go`, `pkg/operations/precondition.go` | N-05, N-06, O-02 | NOT STARTED |
| 1.4 | **Extract ChangeSet.toDeviceChanges()**: The network.Change → device.ConfigChange conversion is duplicated in `changeset.go` Apply() and Verify(). Extract a shared `toDeviceChanges()` method. | `pkg/network/changeset.go` | N-11 | NOT STARTED |
| 1.5 | **Fix InterfaceIsLAGMember**: Change from suffix match to `splitConfigDBKey` + exact match on parts[1], consistent with `GetInterfaceLAG`. | `pkg/network/device.go:505-511` | N-13 | NOT STARTED |

### Track 2: newtron CLI (`cmd/newtron/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 2.1 | **Wire or remove dead flags**: (a) `lagMode` — declared at cmd_lag.go:263, flag registered at :394, never read. Either wire into `PortChannelConfig{}` at :297 or remove flag. (b) `vlanName` — declared at cmd_vlan.go:248, flag at :512, never read. Either wire into `VLANConfig{}` or remove. (c) `_ = vrfName` dead assignment at cmd_vrf.go:422 — just delete it. | `cmd/newtron/cmd_lag.go`, `cmd/newtron/cmd_vlan.go`, `cmd/newtron/cmd_vrf.go` | DC-01, DC-02, DC-05 | NOT STARTED |
| 2.2 | **Extract `parseVLANID()` helper**: Pattern `var vlanID int; if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil { ... }` is repeated 7 times in cmd_vlan.go. Extract to a shared function in main.go. | `cmd/newtron/cmd_vlan.go`, `cmd/newtron/main.go` | D-04 | NOT STARTED |
| 2.3 | **Extract status colorization helpers**: `formatAdminStatus(s string) string` and `formatOperStatus(s string) string`. Replace 12+ ad-hoc colorization locations across cmd_interface.go, cmd_lag.go, cmd_bgp.go, cmd_evpn.go, cmd_vrf.go, cmd_vlan.go. | `cmd/newtron/main.go` + 6 cmd files | D-03 | NOT STARTED |
| 2.4 | **Add dry-run checks to spec authoring commands**: `filter create/delete/add-rule/remove-rule`, `qos create/delete/add-queue/remove-queue`, `service create/delete` all skip `executeMode` check. Add `if !executeMode { printDryRunNotice(); return nil }` before the save/delete call, matching `evpn ipvpn create/delete` pattern. | `cmd/newtron/cmd_filter.go`, `cmd/newtron/cmd_qos.go`, `cmd/newtron/cmd_service.go` | E-04, I-03 | NOT STARTED |
| 2.5 | **Use `requireDevice` in cmd_show.go**: Lines 26-29 manually check `deviceName == ""` instead of calling `requireDevice(ctx)` like all other device-requiring commands. | `cmd/newtron/cmd_show.go` | S-03 | NOT STARTED |
| 2.6 | **Fix delete permission constants**: `vlanDeleteCmd` uses `PermVLANCreate`, `lagDeleteCmd` uses `PermLAGCreate`, `vrfDeleteCmd` uses `PermVRFCreate`. Create proper `PermVLANDelete`, `PermLAGDelete`, `PermVRFDelete` in `pkg/auth/permission.go` and use them. | `cmd/newtron/cmd_vlan.go`, `cmd/newtron/cmd_lag.go`, `cmd/newtron/cmd_vrf.go`, `pkg/auth/permission.go` | I-05 | NOT STARTED |

### Track 3: newtlab (`cmd/newtlab/`, `pkg/newtlab/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 3.1 | **Fix SSH/console to use HostIP for remote nodes**: (a) `cmd_ssh.go:44` hardcodes `admin@127.0.0.1` — use `node.HostIP` when non-empty. (b) `cmd_console.go:35-41` hardcodes `127.0.0.1` — same fix. (c) `cmd_ssh.go:44` hardcodes `admin` — read from node profile or state. | `cmd/newtlab/cmd_ssh.go`, `cmd/newtlab/cmd_console.go` | S-2, S-3, C-1 | NOT STARTED |
| 3.2 | **Fix --device provision filter**: `cmd_provision.go:32-45` loads and filters state, but `lab.Provision()` internally calls `LoadState()` again, ignoring the filter. Either pass filtered device list to Provision or add a device filter parameter. | `cmd/newtlab/cmd_provision.go`, `pkg/newtlab/newtlab.go` | P-1 | NOT STARTED |
| 3.3 | **Check destroyExisting error**: `newtlab.go:136` discards `l.destroyExisting(existing)` return value. Check and return it (or at minimum log warning). | `pkg/newtlab/newtlab.go` | M-5 | NOT STARTED |
| 3.4 | **Extract SSH command helper**: `exec.Command("ssh", "-o", "StrictHostKeyChecking=no", hostIP, ...)` appears 15+ times across qemu.go, disk.go, bridge.go, remote.go, probe.go with inconsistent options (some have ConnectTimeout, some don't). Create `sshCommand(hostIP, cmd string) *exec.Cmd` that always includes standard options. | `pkg/newtlab/` (new helper + 5 files) | Q-1, X-2 | NOT STARTED |
| 3.5 | **Extract host resolution helpers**: (a) `nodeHostIP(ns *NodeState) string` — pattern `host := "127.0.0.1"; if ns.HostIP != "" { host = ns.HostIP }` appears 5+ times. (b) `stopAllBridges(state *LabState) error` — bridge shutdown logic duplicated between Destroy() and destroyExisting(). | `pkg/newtlab/newtlab.go` | N-2, N-3 | NOT STARTED |
| 3.6 | **Unify resolveSpecDir/resolveLabName**: Both share 80% logic. Extract `resolveTarget(args) (labName, specDir, error)` wrapper. | `cmd/newtlab/main.go` | M-1 | NOT STARTED |

### Track 4: newtest (`cmd/newtest/`, `pkg/newtest/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 4.1 | **Fix break-in-select bug**: In `steps.go`, verifyStateDB (lines 467-477) and verifyBGP (lines 571-579), `break` inside `case <-ctx.Done()` only breaks the `select`, not the outer `for` loop. Use labeled break: `break pollLoop`. | `pkg/newtest/steps.go` | SE-03 | NOT STARTED |
| 4.2 | **Fix verifyBGP to check expected state**: `steps.go:558-562` computes `expectedState` but never checks it — only checks if health check status == "pass". Must actually verify the reported BGP peer state matches `step.Expect.State`. | `pkg/newtest/steps.go` | SE-05 | NOT STARTED |
| 4.3 | **Fix !matched && allPassed guard**: `steps.go` verifyBGP line 582-583 and verifyStateDB line 479 — the guard should be `!matched` alone. When an earlier device errored (allPassed=false), subsequent device timeouts go unreported. | `pkg/newtest/steps.go` | SE-06 | NOT STARTED |
| 4.4 | **Extract `executeForDevices` helper**: 20+ mutating executors follow identical pattern: resolveDevices → init details/changeSets/allPassed → for each device: GetDevice → ExecuteOp → append result → compute status → return StepOutput. Extract generic helper, reduce steps.go from ~2562 to ~800 lines. Signature: `func (r *Runner) executeForDevices(step *Step, fn func(dev *network.Device) (*network.ChangeSet, string, error)) *StepOutput`. | `pkg/newtest/steps.go` | SE-01 | NOT STARTED |
| 4.5 | **Extract `pollUntil` helper**: verifyStateDB, verifyBGP, verifyRoute all implement same poll-until-timeout loop. Extract: `func pollUntil(ctx context.Context, timeout, interval time.Duration, fn func() (done bool, err error)) error`. | `pkg/newtest/steps.go` | SE-02 | NOT STARTED |
| 4.6 | **Move os.Exit out of RunE**: Both cmd_run.go and cmd_start.go call `os.Exit()` inside Cobra RunE, bypassing deferred cleanup (lock release). Return sentinel error, handle exit codes in main(). | `cmd/newtest/cmd_run.go`, `cmd/newtest/cmd_start.go`, `cmd/newtest/main.go` | S-04, X-01 | NOT STARTED |
| 4.7 | **Deduplicate run/start commands**: `run` is deprecated+hidden. Either delete it entirely, or extract shared logic (runner construction, result analysis, report generation) into a helper both commands call. | `cmd/newtest/cmd_run.go`, `cmd/newtest/cmd_start.go` | R-04 | NOT STARTED |

### Track 5: newtron support (`pkg/auth/`, `pkg/audit/`, `pkg/util/`, `pkg/settings/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 5.1 | **Fix audit event ID generation**: `event.go:124-126` uses `time.Now().UnixNano()` — not unique under concurrency. Replace with `crypto/rand` or monotonic counter protected by mutex. | `pkg/audit/event.go` | AU-3 | NOT STARTED |
| 5.2 | **Fix audit offset/limit bug**: `logger.go:103-108` — when `filter.Offset >= len(events)`, all events are returned instead of empty slice. | `pkg/audit/logger.go` | AU-5 | NOT STARTED |
| 5.3 | **Remove dead audit types**: `EventType` constants (event.go:32-41) and `Severity` type (event.go:44-50) are defined but Event struct has no corresponding fields. Either add fields or remove types. Also `RotationConfig.MaxAge` (logger.go:30-33) defined but never implemented. | `pkg/audit/event.go`, `pkg/audit/logger.go` | AU-1, AU-2, AU-6 | NOT STARTED |
| 5.4 | **Protect DefaultLogger**: `logger.go:229-230` — global `DefaultLogger` pointer is not concurrency-safe. Wrap with `sync.RWMutex` or `atomic.Value`. | `pkg/audit/logger.go` | AU-9 | NOT STARTED |
| 5.5 | **Prune dead util functions**: 12+ exported functions with zero production call sites: `DeriveRouterID`, `DeriveVTEPSourceIP`, `DeriveRouteDistinguisher`, `IsValidMACAddress`, `NormalizeMACAddress`, `IPInRange`, `ValidateVNI`, `ValidateVLANID`, `ParsePortRange`, `ExpandSlotPortRange`, `ExpandInterfaceRange`, `ExpandVLANRange`, `CompactRange`, `ComputeBroadcastAddr`. Verify each has zero callers with `grep -r`, then remove. Keep test files in sync. | `pkg/util/derive.go`, `pkg/util/ip.go`, `pkg/util/range.go` | U-1 through U-13 | NOT STARTED |
| 5.6 | **Hoist compiled regexps**: `derive.go:47` `SanitizeForName` and `derive.go:107` `ParseInterfaceName` both compile regexp on every call. Move to package-level `var`. Also `spec/resolver.go:26` `ResolveString`. | `pkg/util/derive.go`, `pkg/spec/resolver.go` | U-14, U-15, S-02(spec) | NOT STARTED |

---

## Phase B — Parallel With Sync Points

Complete Phase A first. Then these can run in parallel, with one sync after Track 1 merges.

### Track 1 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 1.6 | **Unify ChangeType**: Define single `ChangeType` in `pkg/device/` (canonical). Have `pkg/network/changeset.go` use `device.ChangeType` directly instead of its own duplicate. Delete `network.ChangeTypeAdd/Modify/Delete`. This changes imports in changeset.go only — no public API change. | `pkg/network/changeset.go`, `pkg/device/device.go` | N-04, D-03 | NOT STARTED |
| 1.7 | **Split device_ops.go**: Into `vlan_ops.go`, `portchannel_ops.go`, `vrf_ops.go`, `acl_ops.go`, `evpn_ops.go`, `bgp_ops.go`, `port_ops.go`, `health.go`, `qos_ops.go`. Move config types to co-locate with operations. Pure file reorganization — zero logic changes. | `pkg/network/device_ops.go` → 8+ files | N-01 | NOT STARTED |
| 1.8 | **Split interface_ops.go**: Into `service_ops.go` (ApplyService/RemoveService/RefreshService), `dependency.go` (DependencyChecker), `interface_bgp.go` (BGP neighbor ops). | `pkg/network/interface_ops.go` → 3+ files | N-02 | NOT STARTED |
| 1.9 | **Replace KEYS * with SCAN**: In `pkg/device/configdb.go` ConfigDBClient.GetAll() and `pkg/device/statedb.go` StateDBClient.GetAll(). Use cursor-based SCAN with per-table patterns. | `pkg/device/configdb.go`, `pkg/device/statedb.go` | D-01 | NOT STARTED |

### Track 3 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 3.7 | **Fix unquoted paths in remote commands**: `disk.go:30-31` doesn't quote paths in qemu-img create. `disk.go:63-64` uses unquoted path in `rm -rf`. Use `quoteArgs` or single-quote paths. | `pkg/newtlab/disk.go` | DK-1, DK-3 | NOT STARTED |
| 3.8 | **Cache os.UserHomeDir()**: Called in 5 places with error silently discarded (state.go:51, state.go:96, disk.go:74, disk.go:82, remote.go:86). Cache in package-level var with proper error handling. | `pkg/newtlab/state.go`, `pkg/newtlab/disk.go`, `pkg/newtlab/remote.go` | ST-1, DK-2, X-4 | NOT STARTED |

### Track 4 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 4.8 | **Derive validActions from executors**: `scenario.go:107-146` `validActions` map must be manually synced with `steps.go` executors map. Instead: `func init() { for k := range executors { validActions[k] = true } }`. | `pkg/newtest/scenario.go`, `pkg/newtest/steps.go` | SC-01, X-02 | NOT STARTED |
| 4.9 | **Deduplicate suite resolvers**: `resolveSuiteForStop` (cmd_stop.go:80-97) and `resolveSuiteForControl` (cmd_pause.go) share logic. Unify into `resolveSuite(filter func(RunStatus) bool)`. Also export `IsProcessAlive` from pkg/newtest and delete CLI duplicate `isProcAlive`. | `cmd/newtest/cmd_stop.go`, `cmd/newtest/cmd_pause.go`, `pkg/newtest/state.go` | T-01, P-01 | NOT STARTED |
| 4.10 | **Use typed status constants in cmd_status.go**: Lines 137-147 compare against raw strings "PASS"/"FAIL"/"ERROR"/"SKIP" instead of `newtest.StatusPassed` etc. | `cmd/newtest/cmd_status.go` | ST-01 | NOT STARTED |

**Sync point**: After Track 1 Phase B merges (1.6 ChangeType unification), check if newtest steps.go needs import updates. Track 1.7/1.8 (file splitting) is invisible to importers (same package).

---

## Phase C — Sequential (public API changes)

These items change public function signatures. Do them one at a time, updating all callers.

| # | Item | Files | Depends On | Audit ID | Status |
|---|------|-------|------------|----------|--------|
| C.1 | **Add context.Context to newtlab public API**: Add `ctx context.Context` as first param to `Lab.Deploy`, `Lab.Destroy`, `Lab.Start`, `Lab.Stop`, `Lab.Provision`, `WaitForSSH`, `BootstrapNetwork`, `ApplyBootPatches`. Wire cancellation through goroutines. Then update callers: `cmd/newtlab/` and `pkg/newtest/deploy.go`. | `pkg/newtlab/*.go`, `cmd/newtlab/*.go`, `pkg/newtest/deploy.go` | Phase A+B complete | X-1 (newtlab) | NOT STARTED |
| C.2 | **Delete interactive.go**: 1036 lines of parallel CLI implementation. Marked `Hidden: true`. Entire file duplicates noun-group CLI. If keeping, refactor to call same operation functions as noun-group commands. Decide: delete or refactor. | `cmd/newtron/interactive.go`, `cmd/newtron/main.go` | Track 2 Phase A | D-01, D-02, S-02 | NOT STARTED |
| C.3 | **Resolve model types usage**: `pkg/model/` rich domain types (BGPConfig, VTEP, VRF, PortChannel, etc.) are largely unused — backend ops work with raw `map[string]string`. Decide: (a) wire ops to use model types, (b) deprecate, (c) remove. | `pkg/model/*.go`, `pkg/network/device_ops.go` | Phase B complete | M-03, X-02, X-03 | NOT STARTED |
| C.4 | **Wire auth enforcement or document as aspirational**: `RequirePermission` has zero production call sites. The entire auth system is defined, tested, but never enforced. Either add `RequirePermission` calls to CLI write commands, or add a doc comment marking it as aspirational. | `pkg/auth/checker.go`, `cmd/newtron/*.go` | Phase A Track 2 | A-3 | NOT STARTED |
| C.5 | **Refactor CLI globals into App struct**: 6 mutable globals shared across 20 files. Long-term: wrap in `App` struct, pass through cobra context. Makes CLI testable. | `cmd/newtron/*.go` (all 20 files) | Phase C.2 (less files after interactive.go gone) | S-01 | NOT STARTED |

---

## Completion Checklist

After each track/phase:
- [ ] `go build ./cmd/newtron && go build ./cmd/newtlab && go build ./cmd/newtest`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] Commit per logical unit (not per track — each item should be one commit)

---

## Notes for Future Sessions

1. **Audit docs are NOT committed yet** — they're untracked files at `docs/*/refactor-audit*.md`. Commit them before starting work.
2. **Don't change public method signatures in Phase A** — that's what makes parallel execution safe.
3. **The protocol 179 bug (1.1) is the highest-value single fix** — it's an actual bug that generates invalid ACL rules.
4. **The break-in-select bug (4.1) is the second-highest** — context cancellation is silently ignored in verify loops.
5. **Item 4.4 (executeForDevices)** is the highest-value refactor — eliminates ~1700 lines of boilerplate from steps.go.
6. **Item 1.2 (requireWritable)** is the second-highest — eliminates ~120 lines of boilerplate from device_ops.go + interface_ops.go.
7. **Items 1.7/1.8 (file splitting)** should be done AFTER 1.2 (requireWritable) to avoid splitting first and then touching every split file.
8. **Interactive.go deletion (C.2)** should be a user decision — it's 1036 lines that could be useful as a TUI if someone wants to invest in it.
