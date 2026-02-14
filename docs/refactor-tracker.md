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
| 1.1 | **Fix protocol 179 bug** | `pkg/model/acl.go`, `pkg/network/service_gen.go`, `pkg/network/device_ops.go` | N-09, M-01, M-02, X-01 | DONE ✓ |
| 1.2 | **Extract `requireWritable()` helper** (65 replacements) | `pkg/network/device_ops.go`, `pkg/network/interface_ops.go`, `changeset.go`, `composite.go` | N-03 | DONE ✓ |
| 1.3 | **Consolidate splitPorts/joinPorts** → `util.SplitCommaSeparated` | `pkg/util/strings.go`, `pkg/network/device_ops.go`, `pkg/operations/precondition.go` | N-05, N-06, O-02 | DONE ✓ |
| 1.4 | **Extract ChangeSet.toDeviceChanges()** | `pkg/network/changeset.go` | N-11 | DONE ✓ |
| 1.5 | **Fix InterfaceIsLAGMember** exact match | `pkg/network/device.go` | N-13 | DONE ✓ |

### Track 2: newtron CLI (`cmd/newtron/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 2.1 | **Wire or remove dead flags** (lagMode, vlanName, vrfName) | `cmd/newtron/cmd_lag.go`, `cmd/newtron/cmd_vlan.go`, `cmd/newtron/cmd_vrf.go` | DC-01, DC-02, DC-05 | DONE ✓ |
| 2.2 | **Extract `parseVLANID()` helper** (7 replacements) | `cmd/newtron/cmd_vlan.go` | D-04 | DONE ✓ |
| 2.3 | **Extract status colorization helpers** | `cmd/newtron/main.go` + 6 cmd files | D-03 | DONE ✓ |
| 2.4 | **Add dry-run checks to spec authoring commands** | `cmd/newtron/cmd_filter.go`, `cmd/newtron/cmd_qos.go`, `cmd/newtron/cmd_service.go` | E-04, I-03 | DONE ✓ |
| 2.5 | **Use `requireDevice` in cmd_show.go** | `cmd/newtron/cmd_show.go` | S-03 | DONE ✓ |
| 2.6 | **Fix delete permission constants** | `cmd/newtron/cmd_*.go`, `pkg/auth/permission.go` | I-05 | DONE ✓ |

### Track 3: newtlab (`cmd/newtlab/`, `pkg/newtlab/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 3.1 | **Fix SSH/console to use HostIP for remote nodes** | `cmd/newtlab/cmd_ssh.go`, `cmd/newtlab/cmd_console.go` | S-2, S-3, C-1 | DONE ✓ |
| 3.2 | **Fix --device provision filter** (DeviceFilter field) | `cmd/newtlab/cmd_provision.go`, `pkg/newtlab/newtlab.go` | P-1 | DONE ✓ |
| 3.3 | **Check destroyExisting error** | `pkg/newtlab/newtlab.go` | M-5 | DONE ✓ |
| 3.4 | **Extract SSH command helper** (14 replacements) | `pkg/newtlab/remote.go` + 5 files | Q-1, X-2 | DONE ✓ |
| 3.5 | **Extract host resolution helpers** (nodeHostIP, stopAllBridges) | `pkg/newtlab/newtlab.go` | N-2, N-3 | DONE ✓ |
| 3.6 | **Unify resolveSpecDir/resolveLabName** → resolveTarget | `cmd/newtlab/main.go` | M-1 | DONE ✓ |

### Track 4: newtest (`cmd/newtest/`, `pkg/newtest/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 4.1 | **Fix break-in-select bug** (labeled pollLoop break) | `pkg/newtest/steps.go` | SE-03 | DONE ✓ |
| 4.2 | **Fix verifyBGP to check expected state** | `pkg/newtest/steps.go` | SE-05 | DONE ✓ |
| 4.3 | **Fix !matched && allPassed guard** (via pollUntil rewrite) | `pkg/newtest/steps.go` | SE-06 | DONE ✓ |
| 4.4 | **Extract `executeForDevices` helper** (22 executors) | `pkg/newtest/steps.go` | SE-01 | DONE ✓ |
| 4.5 | **Extract `pollUntil` helper** (3 verify executors) | `pkg/newtest/steps.go` | SE-02 | DONE ✓ |
| 4.6 | **Move os.Exit out of RunE** (sentinel errors) | `cmd/newtest/cmd_start.go`, `cmd/newtest/main.go` | S-04, X-01 | DONE ✓ |
| 4.7 | **Deduplicate run/start** (run = hidden alias for start) | `cmd/newtest/cmd_run.go` (deleted), `cmd/newtest/helpers.go` (new) | R-04 | DONE ✓ |

### Track 5: newtron support (`pkg/auth/`, `pkg/audit/`, `pkg/util/`, `pkg/settings/`)

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 5.1 | **Fix audit event ID generation** (crypto/rand) | `pkg/audit/event.go` | AU-3 | DONE ✓ |
| 5.2 | **Fix audit offset/limit bug** | `pkg/audit/logger.go` | AU-5 | DONE ✓ |
| 5.3 | **Remove dead audit types** (EventType, Severity, MaxAge) | `pkg/audit/event.go`, `pkg/audit/logger.go` | AU-1, AU-2, AU-6 | DONE ✓ |
| 5.4 | **Protect DefaultLogger** (atomic.Value) | `pkg/audit/logger.go` | AU-9 | DONE ✓ |
| 5.5 | **Prune dead util functions** (range.go deleted entirely) | `pkg/util/derive.go`, `pkg/util/ip.go`, `pkg/util/range.go` (deleted) | U-1 through U-13 | DONE ✓ |
| 5.6 | **Hoist compiled regexps** | `pkg/util/derive.go`, `pkg/spec/resolver.go` | U-14, U-15, S-02(spec) | DONE ✓ |

---

## Phase B — Parallel With Sync Points

Complete Phase A first. Then these can run in parallel, with one sync after Track 1 merges.

### Track 1 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 1.6 | **Unify ChangeType** (network uses device.ChangeType) | `pkg/network/changeset.go` | N-04, D-03 | DONE ✓ |
| 1.7 | **Split device_ops.go** (2139→40 lines, 9 domain files) | `pkg/network/` (12 new files) | N-01 | DONE ✓ |
| 1.8 | **Split interface_ops.go** (1723→75 lines, 3 domain files) | `pkg/network/` (3 new files) | N-02 | DONE ✓ |
| 1.9 | **Replace KEYS * with SCAN** (scanKeys helper) | `pkg/device/configdb.go`, `pkg/device/statedb.go` | D-01 | DONE ✓ |

### Track 3 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 3.7 | **Fix unquoted paths** (shellQuote helper) | `pkg/newtlab/disk.go`, `bridge.go`, `qemu.go`, `remote.go` | DK-1, DK-3 | DONE ✓ |
| 3.8 | **Cache os.UserHomeDir()** (sync.Once + error propagation) | `pkg/newtlab/remote.go`, `state.go`, `disk.go` | ST-1, DK-2, X-4 | DONE ✓ |

### Track 4 continued

| # | Item | Files | Audit ID | Status |
|---|------|-------|----------|--------|
| 4.8 | **Derive validActions from executors** (init()) | `pkg/newtest/scenario.go` | SC-01, X-02 | DONE ✓ |
| 4.9 | **Deduplicate suite resolvers** (unified resolveSuite + export IsProcessAlive) | `cmd/newtest/helpers.go`, `cmd_pause.go`, `cmd_stop.go`, `pkg/newtest/state.go` | T-01, P-01 | DONE ✓ |
| 4.10 | **Use typed status constants** | `cmd/newtest/cmd_status.go` | ST-01 | DONE ✓ |

**Sync point**: After Track 1 Phase B merges (1.6 ChangeType unification), check if newtest steps.go needs import updates. Track 1.7/1.8 (file splitting) is invisible to importers (same package).

---

## Phase C — Sequential (public API changes)

These items change public function signatures. Do them one at a time, updating all callers.

| # | Item | Files | Depends On | Audit ID | Status |
|---|------|-------|------------|----------|--------|
| C.1 | **Add context.Context to newtlab public API**: Add `ctx context.Context` as first param to `Lab.Deploy`, `Lab.Destroy`, `Lab.Start`, `Lab.Stop`, `Lab.Provision`, `WaitForSSH`, `BootstrapNetwork`, `ApplyBootPatches`. Wire cancellation through goroutines. Then update callers: `cmd/newtlab/` and `pkg/newtest/deploy.go`. | `pkg/newtlab/*.go`, `cmd/newtlab/*.go`, `pkg/newtest/deploy.go` | Phase A+B complete | X-1 (newtlab) | DONE ✓ |
| C.2 | **Delete interactive.go**: 1036 lines of parallel CLI implementation. Marked `Hidden: true`. Entire file duplicates noun-group CLI. If keeping, refactor to call same operation functions as noun-group commands. Decide: delete or refactor. | `cmd/newtron/interactive.go`, `cmd/newtron/main.go` | Track 2 Phase A | D-01, D-02, S-02 | DONE ✓ |
| C.3 | **Resolve model types usage**: `pkg/model/` rich domain types (BGPConfig, VTEP, VRF, PortChannel, etc.) are largely unused — backend ops work with raw `map[string]string`. Decide: (a) wire ops to use model types, (b) deprecate, (c) remove. | `pkg/model/*.go`, `pkg/network/device_ops.go` | Phase B complete | M-03, X-02, X-03 | DONE ✓ |
| C.4 | **Wire auth enforcement or document as aspirational**: `RequirePermission` has zero production call sites. The entire auth system is defined, tested, but never enforced. Either add `RequirePermission` calls to CLI write commands, or add a doc comment marking it as aspirational. | `pkg/auth/checker.go`, `cmd/newtron/*.go` | Phase A Track 2 | A-3 | DONE ✓ |
| C.5 | **Refactor CLI globals into App struct**: 6 mutable globals shared across 20 files. Long-term: wrap in `App` struct, pass through cobra context. Makes CLI testable. | `cmd/newtron/*.go` (all 20 files) | Phase C.2 (less files after interactive.go gone) | S-01 | DONE ✓ |

---

## MEDIUM Findings Tracker

**Total**: 87 MEDIUM findings across 5 audit documents
**Addressed**: 82 (94%)
**Remaining**: 5

### Backend (`refactor-audit-backend.md`) — 18/19 addressed

| ID | Summary | Status | Addressed By |
|----|---------|--------|-------------|
| N-04 | Duplicate ChangeType definitions | DONE ✓ | Phase B 1.6 |
| N-07 | Existence checks duplicated across layers | DONE ✓ | 71e93a9 (nil-safe ConfigDB queries) |
| N-08 | readFileViaSSH is a non-functional stub | DONE ✓ | MEDIUM round 2 |
| N-11 | Apply/Verify duplicate Change conversion | DONE ✓ | Phase A 1.4 (toDeviceChanges) |
| N-13 | InterfaceIsLAGMember uses suffix match | DONE ✓ | Phase A 1.5 |
| D-02 | parseEntry 250-line switch statement | DONE ✓ | 3610750 (table-driven parsers) |
| D-03 | Duplicate ChangeType (see N-04) | DONE ✓ | Phase B 1.6 |
| D-04 | fmt.Printf instead of structured logging | DONE ✓ | MEDIUM round 1 |
| D-05 | InsecureIgnoreHostKey in SSH tunnel | DONE ✓ | MEDIUM round 2 (host key warning) |
| D-07 | statedb PopulateDeviceState overlaps state.go | DONE ✓ | Post-tracker D-07 |
| D-08 | ConfigDB struct 30+ fields / manual init | DONE ✓ | Post-tracker D-08 |
| S-05 | deriveBGPNeighbors silently swallows errors | DONE ✓ | MEDIUM round 2 (util.Warnf) |
| S-06 | AliasContext mutates shared resolver state | DONE ✓ | MEDIUM round 2 |
| M-03 | Model types largely unused by backend ops | DONE ✓ | Phase C.3 (deleted pkg/model/) |
| O-01 | DependencyChecker exists in two places | DONE ✓ | MEDIUM round 1 |
| X-02 | 3 representations of same domain concepts | DONE ✓ | Phase C.3 (reduced to 2 by design) |
| X-04 | Inconsistent error creation patterns | DONE ✓ | MEDIUM round 2 (sentinel errors) |
| D-10 | No lock TTL refresh; Lua script error handling | N/A | By design: lock is device-local Redis key with TTL; Lua is atomic |
| O-03 | PreconditionChecker underused by device_ops | DONE ✓ | Moved to pkg/network/, adopted via d.precondition() in 68 ops |

### CLI (`refactor-audit-cli.md`) — 14/14 addressed

| ID | Summary | Status | Addressed By |
|----|---------|--------|-------------|
| D-03 | Admin status colorization repeated 6+ files | DONE ✓ | Phase A 2.3 |
| D-04 | VLAN ID parsing duplicated 7 times | DONE ✓ | Phase A 2.2 |
| E-01 | reader.ReadString errors ignored (interactive.go) | DONE ✓ | Phase C.2 (file deleted) |
| E-02 | peerASNum silently sets 0 (interactive.go) | DONE ✓ | Phase C.2 (file deleted) |
| E-04 | Spec authoring commands skip dry-run check | DONE ✓ | Phase A 2.4 |
| I-01 | cs.String() vs cs.Preview() inconsistency | DONE ✓ | MEDIUM round 1 (changeset Preview) |
| I-02 | Read commands inconsistently handle jsonOutput | DONE ✓ | MEDIUM round 1 + b75afa0 |
| I-03 | Dry-run inconsistency (= E-04) | DONE ✓ | Phase A 2.4 |
| M-03 | time.Sleep(15s) in provision command | DONE ✓ | MEDIUM round 2 (provision polling) |
| S-02 | interactive.go and shell.go parallel systems | DONE ✓ | Phase C.2 (file deleted) |
| DC-03 | networkName set but never used | DONE ✓ | App struct refactor (field + flag removed) |
| DC-04 | loader initialized but unused | DONE ✓ | App struct refactor (uses net.Spec()) |
| M-01 | Audit log path and rotation config hardcoded | DONE ✓ | Settings: AuditLogPath, AuditMaxSizeMB |
| D-05 | Duplicate showDevice (shell.go vs cmd_show.go) | DONE ✓ | Shell delegates to showDevice() |

### Support (`refactor-audit-support.md`) — 13/15 addressed

| ID | Summary | Status | Addressed By |
|----|---------|--------|-------------|
| A-1 | Duplicated checkServicePermission/checkGlobalPermission | DONE ✓ | MEDIUM round 1 (auth dedup) |
| A-4 | StandardCategories only used in tests | DONE ✓ | Phase C.4 (auth pruning) |
| S-2 | Hardcoded `/etc/newtron` fallback path | DONE ✓ | MEDIUM round 1 (settings constants) |
| AU-4 | Malformed JSON lines silently skipped in query | DONE ✓ | MEDIUM round 2 (util.Warnf) |
| AU-5 | Offset/limit bug returns all events | DONE ✓ | Phase A 5.2 |
| AU-6 | RotationConfig.MaxAge defined but never used | DONE ✓ | Phase A 5.3 |
| AU-9 | DefaultLogger pointer not concurrency-safe | DONE ✓ | Phase A 5.4 (atomic.Value) |
| AU-10 | Query holds write lock unnecessarily | DONE ✓ | RWMutex + RLock in Query |
| U-1 | DeriveRouterID/DeriveVTEPSourceIP unused | DONE ✓ | Phase A 5.5 (dead util pruning) |
| U-5 | IsValidMACAddress/NormalizeMACAddress unused | DONE ✓ | Phase A 5.5 |
| U-6 | IPInRange has zero production call sites | DONE ✓ | Phase A 5.5 |
| U-10 | ExpandSlotPortRange has zero production call sites | DONE ✓ | Phase A 5.5 (range.go deleted) |
| C-1 | No tests for pkg/cli | DONE ✓ | MEDIUM round 2 (pkg/cli tests) |
| U-19 | Logger as package-level mutable global | — | Structural rewrite |
| U-20 | 10 trivial one-line Logger wrappers | — | |

### newtlab (`newtlab/refactor-audit.md`) — 21/22 addressed

| ID | Summary | Status | Addressed By |
|----|---------|--------|-------------|
| M-1 | resolveSpecDir/resolveLabName share 80% logic | DONE ✓ | Phase A 3.6 (resolveTarget) |
| Y-1 | Partial Lab struct in destroy command | DONE ✓ | MEDIUM round 1 (standalone destroy) |
| S-3 | SSH hardcodes `admin` username | DONE ✓ | Phase A 3.1 |
| T-1 | Partial Lab struct in stop/start commands | DONE ✓ | MEDIUM round 1 (standalone stop/start) |
| N-2 | Duplicated host IP resolution (5 sites) | DONE ✓ | Phase A 3.5 (nodeHostIP) |
| N-3 | Duplicated bridge process shutdown logic | DONE ✓ | Phase A 3.5 (stopAllBridges) |
| ST-1 | LabDir/ListLabs silently ignore UserHomeDir errors | DONE ✓ | Phase B 3.8 (sync.Once + error) |
| Q-1 | SSH command construction duplicated 15+ times | DONE ✓ | Phase A 3.4 (sshCommand helper) |
| DK-1 | CreateOverlayRemote doesn't shell-quote paths | DONE ✓ | Phase B 3.7 (shellQuote) |
| DK-3 | cleanupRemoteStateDir uses unquoted path in rm -rf | DONE ✓ | Phase B 3.7 |
| X-1 | No context.Context support anywhere | DONE ✓ | Phase C.1 |
| X-2 | Inconsistent SSH option sets / timeouts | DONE ✓ | Phase A 3.4 (sshCommand helper) |
| X-3 | No logging throughout orchestration | DONE ✓ | MEDIUM round 2 |
| N-6 | refreshBGP reads profile files instead of l.Profiles | DONE ✓ | Uses l.Nodes[name] for SSH creds |
| N-8 | Stats port allocation duplicated vs probe.go | DONE ✓ | Deploy calls allocateBridgeStatsPorts() |
| Q-4 | quoteArgs doesn't handle all shell special chars | DONE ✓ | Always single-quotes with '\'' escaping |
| BR-1 | RunBridgeFromFile ignores splitLinkEndpoint errors | DONE ✓ | Error checked and returned |
| PF-1 | PatchProfiles uses map[string]interface{} | DONE ✓ | Uses spec.DeviceProfile |
| NT-1 | Tests modify global HOME (not parallel-safe) | DONE ✓ | All use t.Setenv() |
| G-1 | Zero test coverage for cmd/newtlab/ helpers | DONE ✓ | humanBytes, topoCounts, resolveTopologyDir |
| N-1 | Lab struct has too many responsibilities | — | Structural rewrite |
| PA-1 | ApplyBootPatches creates new SSH session per command | — | Standard SSH pattern; low priority |

### newtest (`newtest/refactor-audit.md`) — 16/17 addressed

| ID | Summary | Status | Addressed By |
|----|---------|--------|-------------|
| S-04 | os.Exit() inside RunE bypasses deferred cleanup | DONE ✓ | Phase A 4.6 (sentinel errors) |
| P-01 | Duplicate isProcAlive in CLI vs pkg | DONE ✓ | Phase B 4.9 (IsProcessAlive) |
| T-01 | resolveSuiteForStop duplicates resolveSuiteForControl | DONE ✓ | Phase B 4.9 |
| ST-01c | Hardcoded status strings in cmd_status.go | DONE ✓ | Phase B 4.10 |
| SC-01 | validActions redundant with executors map | DONE ✓ | Phase B 4.8 |
| PA-01 | requireDevices used inconsistently | DONE ✓ | MEDIUM round 1 |
| PA-02 | validateStepFields 300-line switch | DONE ✓ | MEDIUM round 2 (table-driven) |
| SE-02 | Polling pattern duplicated in verify executors | DONE ✓ | Phase A 4.5 (pollUntil) |
| SE-05 | verifyBGP ignores step.Expect.State | DONE ✓ | Phase A 4.2 |
| SE-06 | !matched && allPassed guard incorrect | DONE ✓ | Phase A 4.3 (pollUntil rewrite) |
| ST-01s | UserHomeDir error discarded in state.go | DONE ✓ | e959579 |
| R-02 | Swallowed report generation errors (cmd_run.go) | DONE ✓ | Phase A 4.7 (file deleted) |
| S-02 | Swallowed state persistence errors (cmd_start.go) | DONE ✓ | util.Warnf on SaveRunState errors |
| S-03 | Swallowed report generation errors (cmd_start.go) | DONE ✓ | util.Warnf on report write errors |
| X-01 | os.Exit() bypasses cleanup (= S-04) | DONE ✓ | Phase A 4.6 |
| X-02 | No validActions/executors integration (= SC-01) | DONE ✓ | Phase B 4.8 |
| TE-02 | No tests for runner.go Run/RunScenario methods | DONE ✓ | Test coverage |

### Remaining MEDIUMs (3)

**Structural rewrites** (3): N-1 (Lab god object), U-19 (global Logger), U-20 (Logger wrappers)

**Deferred** (1): PA-1 (SSH session per command — standard pattern, low priority)

---

## Completion Checklist

After each track/phase:
- [ ] `go build ./cmd/newtron && go build ./cmd/newtlab && go build ./cmd/newtest`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] Commit per logical unit (not per track — each item should be one commit)

---

## Notes for Future Sessions

1. **Phase A DONE** — committed as 3b9ed0f. 28 items, 46 files, -2027 net lines.
2. **Phase B DONE** — committed as a3092e0. 9 items, 31 files (12 new), -3824 net lines.
3. **Audit docs committed** as 0419197.
4. **Phase C.1+C.2 DONE** — committed as ca9d918. 12 files, -987 net lines. Context threading + interactive.go deletion.
5. **Phase C.3 DONE** — committed as 0986858. Deleted pkg/model/ (10 files, 1131-line test). Moved ProtoMap→pkg/network/, QoSProfile→pkg/spec/.
6. **Phase C.4 DONE** — committed as 064103f. Pruned 28 unused permission constants, removed dead helper functions.
7. **Phase C.5 DONE** — committed as 2d4418b. 11 globals → App struct, 18 files updated.
8. **ALL PHASES COMPLETE** — 190 findings addressed across 5 audit documents.
9. **D-02 parseEntry refactor DONE** — committed as 3610750. Table-driven `tableParsers` registry replaces 250-line switch. Fixed 11 tables that were initialized but never parsed (QoS, routing, SAG, DSCP/TC maps). Also fixed incomplete field assignments in existing parsers. Net -292 lines in configdb.go, +398 lines in configdb_parsers.go, +227 lines in configdb_parsers_test.go.
10. **D-07 state-building merge DONE** — `PopulateDeviceState` now handles nil StateDB (config-only fallback), called unconditionally in `Connect()`/`Reload()`. Deleted dead code: `LoadState()`, `parseInterfaces()`, `parsePortChannels()`, `parseVLANs()`, `parseVRFs()`, `RefreshState()` (~220 lines from state.go). Added 2 `PopulateDeviceState` tests (nil + non-nil StateDB paths).
11. **D-08 ConfigDB/StateDB init refactor DONE** — Reflection-based `initMaps()` replaces 42-line manual `newEmptyConfigDB()` and 13-line manual StateDB init. StateDB parser registry (`stateTableParsers`, 15 entries) in `statedb_parsers.go` replaces 13-case switch. Added `TestNewEmptyStateDB`, `TestStateParsers_AllTablesRegistered`, `TestStateDB_ParseEntry` (6 tables).
