# newtlab Code Quality Audit

**Date**: 2026-02-13
**Scope**: `cmd/newtlab/` (11 files) and `pkg/newtlab/` (15 source files + 5 test files)
**Coverage**: `pkg/newtlab/` 33.8%, `cmd/newtlab/` 0.0%

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 7     |
| MEDIUM   | 22    |
| LOW      | 12    |

---

## cmd/newtlab/main.go

### M-1. Code duplication: `resolveSpecDir` and `resolveLabName` share 80% logic
- **Line**: 118-188
- **Category**: Code duplication
- **Severity**: MEDIUM
- Both functions resolve topology from -S flag, positional arg, or auto-detect. The core resolution flow (check specDir global, check args, list labs, auto-detect single) is nearly identical. The only difference is the return type (spec directory path vs lab name).
- **Remediation**: Extract a shared `resolveTarget` returning both lab name and spec dir, then have `resolveSpecDir` and `resolveLabName` wrap it.

### M-2. `resolveLabName` creates a full Lab object just to get its name
- **Line**: 152-154, 169-173
- **Category**: Structural issues
- **Severity**: LOW
- `NewLab(specDir)` loads topology.json, platforms.json, all profiles, resolves all nodes, allocates links -- all to derive a name that is simply `filepath.Base(specDir)` (or its parent). This is expensive for a name lookup.
- **Remediation**: Extract the lab name derivation logic from `NewLab` into a standalone `LabNameFromSpecDir(dir string) string` function.

### M-3. `resolveLabName` ignores ListLabs error
- **Line**: 161
- **Category**: Missing error handling
- **Severity**: LOW
- `labs, _ := newtlab.ListLabs()` silently discards the error. If the labs directory is unreadable (permissions), this silently falls through.
- **Remediation**: Check the error and return it.

### M-4. `topoCounts` uses inline anonymous struct instead of spec types
- **Line**: 261-275
- **Category**: Inconsistent patterns
- **Severity**: LOW
- Defines a local anonymous struct for JSON parsing instead of using `spec.TopologySpecFile`. This duplicates knowledge of the topology JSON structure.
- **Remediation**: Use `spec.TopologySpecFile` or at minimum reference the spec type comment.

### M-5. Unused return value from `destroyExisting`
- **Line**: 136
- **Category**: Missing error handling
- **Severity**: HIGH
- `l.destroyExisting(existing)` returns an error but the return value is discarded at the call site. If the force-redeploy teardown fails (e.g., processes can't be killed), deployment proceeds silently on top of a potentially broken state.
- **Remediation**: Check and return the error, or at minimum log it as a warning.

---

## cmd/newtlab/cmd_deploy.go

### D-1. Node summary iterates unordered map
- **Line**: 52
- **Category**: Inconsistent patterns
- **Severity**: LOW
- `for name, node := range state.Nodes` produces non-deterministic output. Other code uses `sortedNodeNames`.
- **Remediation**: Sort keys before iteration for consistent output.

---

## cmd/newtlab/cmd_destroy.go

### Y-1. Creates a partial Lab struct directly
- **Line**: 22
- **Category**: Structural issues
- **Severity**: MEDIUM
- `lab := &newtlab.Lab{Name: labName}` creates a Lab with only the Name field set. The `Destroy()` method then loads state and fills in `SpecDir`. This bypasses the `NewLab` constructor, meaning `Lab` has an implicit two-phase initialization contract that isn't enforced by the type system.
- **Remediation**: Consider a `DestroyLab(name string)` standalone function that doesn't require constructing a partial Lab, or make `Destroy` a package-level function taking a lab name.

---

## cmd/newtlab/cmd_ssh.go

### S-1. Discarded variable `_ = labName`
- **Line**: 26
- **Category**: Dead code
- **Severity**: LOW
- `_ = labName` explicitly discards the lab name returned by `findNodeState`. This was presumably needed during development but is now dead code noise.
- **Remediation**: Change `findNodeState` to only return what's needed, or remove the assignment.

### S-2. SSH hardcodes `admin@127.0.0.1` without checking HostIP
- **Line**: 44
- **Category**: Missing error handling
- **Severity**: HIGH
- Always connects to `admin@127.0.0.1` regardless of whether the node is running on a remote host (`HostIP` field). For multi-host labs, the SSH port is forwarded through the remote host's QEMU, so `127.0.0.1` won't work from the local machine.
- **Remediation**: Use `node.HostIP` when non-empty (like the rest of the codebase does for SSH connections).

### S-3. SSH hardcodes `admin` username
- **Line**: 44
- **Category**: Configuration/magic numbers
- **Severity**: MEDIUM
- The SSH user is hardcoded to `admin` instead of reading from the node's profile or state. Different platforms may use different SSH users.
- **Remediation**: Store SSH user in `NodeState` or read from the profile.

---

## cmd/newtlab/cmd_console.go

### C-1. Console hardcodes `127.0.0.1` without checking HostIP
- **Line**: 35-41
- **Category**: Missing error handling
- **Severity**: HIGH
- Same issue as S-2: console always connects to `127.0.0.1`. For remote nodes, the console port is on the remote host.
- **Remediation**: Use `node.HostIP` when set.

---

## cmd/newtlab/cmd_stop.go

### T-1. Partial Lab struct in stop/start commands
- **Line**: 24, 48
- **Category**: Structural issues
- **Severity**: MEDIUM
- Same pattern as Y-1. `&newtlab.Lab{Name: labName}` creates an incomplete Lab. `Stop()` and `Start()` reload state internally, so they work, but the pattern is fragile and undocumented.
- **Remediation**: Same as Y-1.

---

## cmd/newtlab/cmd_provision.go

### P-1. `--device` filter loads state but the filtered state is not passed to `lab.Provision`
- **Line**: 32-45
- **Category**: Missing error handling
- **Severity**: HIGH
- The code loads state, verifies the device exists, then deletes other nodes from the state map -- but `lab.Provision()` internally calls `LoadState(l.Name)` again (line 593 of newtlab.go), ignoring the filtered state entirely. The `--device` flag has no effect.
- **Remediation**: Either pass the filtered state to `Provision` or add a device filter parameter to the `Provision` method.

---

## cmd/newtlab/cmd_bridge_stats.go

### B-1. `humanBytes` reimplements a common utility
- **Line**: 58-74
- **Category**: Code duplication
- **Severity**: LOW
- This is a standard utility function. Consider whether `pkg/util` or `pkg/cli` already has one, or should.
- **Remediation**: Check if `pkg/util` has this; if not, move to a shared location since other tools may need it.

---

## cmd/newtlab/exec.go

No issues found. Clean, minimal wrapper.

---

## cmd/newtlab/ (general)

### G-1. Zero test coverage
- **Line**: N/A
- **Category**: Missing tests
- **Severity**: MEDIUM
- `cmd/newtlab/` has 0.0% test coverage. While CLI tests are harder to write, several helper functions (`resolveSpecDir`, `resolveLabName`, `topoCounts`, `humanBytes`, `findNodeState`) are pure functions that can be unit tested.
- **Remediation**: Add unit tests for resolution helpers and `humanBytes`.

---

## pkg/newtlab/newtlab.go

### N-1. God object: Lab struct has too many responsibilities
- **Line**: 21-33
- **Category**: Structural issues
- **Severity**: MEDIUM
- `Lab` serves as: spec loader, node/link resolver, QEMU orchestrator, bridge orchestrator, SSH bootstrapper, profile patcher, provisioner, and state manager. The `Deploy()` method alone is 275 lines.
- **Remediation**: Extract bridge deployment into a `BridgeOrchestrator`, SSH bootstrap into `BootstrapOrchestrator`, and provisioning into its own function. At minimum, break `Deploy()` into named helper methods.

### N-2. Duplicated "resolve local/remote host" pattern
- **Line**: 307-310, 335-338, 373-376, 576-579, 659
- **Category**: Code duplication
- **Severity**: MEDIUM
- The pattern `host := "127.0.0.1"; if ns.HostIP != "" { host = ns.HostIP }` appears 5+ times in newtlab.go. This is the same logic as `resolveWorkerHostIP` but for nodes.
- **Remediation**: Extract a `nodeHostIP(ns *NodeState) string` helper.

### N-3. Duplicated bridge process shutdown logic
- **Line**: 431-449 (Destroy) and 713-723 (destroyExisting)
- **Category**: Code duplication
- **Severity**: MEDIUM
- The bridge process shutdown code (iterate Bridges map, check HostIP, stopBridgeProcessRemote vs stopNodeLocal, legacy BridgePID fallback) is duplicated between `Destroy()` and `destroyExisting()`.
- **Remediation**: Extract a `stopAllBridges(state *LabState)` helper.

### N-4. Duplicated remote host cleanup logic
- **Line**: 452-460 (Destroy) and 725-730 (destroyExisting)
- **Category**: Code duplication
- **Severity**: LOW
- The `cleanedHosts` map pattern for deduplicating remote cleanup is repeated.
- **Remediation**: Extract into a helper or make `destroyExisting` call `Destroy`.

### N-5. `destroyExisting` returns error but errors are best-effort
- **Line**: 698-736
- **Category**: Inconsistent patterns
- **Severity**: LOW
- `destroyExisting` returns an error from `RemoveState` at line 735, but ignores errors from `stopNodeLocal`, `stopBridgeProcessRemote`, `cleanupRemoteStateDir`, and `RestoreProfiles`. The caller at line 136 also ignores the return value (see M-5 above).
- **Remediation**: Either make it consistently best-effort (no return value) or collect all errors like `Destroy()` does.

### N-6. `refreshBGP` reads profile files to get SSH credentials
- **Line**: 639-683
- **Category**: Structural issues
- **Severity**: MEDIUM
- `refreshBGP` re-reads profile JSON files to get SSH user/pass, even though `l.Profiles` already has parsed profiles. It defines its own anonymous struct for JSON parsing instead of using `spec.DeviceProfile`.
- **Remediation**: Use `l.Profiles[name].SSHUser` and `l.Profiles[name].SSHPass`, or better, use `l.Nodes[name].SSHUser`/`SSHPass` which are already resolved.

### N-7. `refreshBGP` hardcoded 5-second sleep
- **Line**: 641
- **Category**: Configuration/magic numbers
- **Severity**: LOW
- `time.Sleep(5 * time.Second)` is a non-configurable delay before BGP refresh. This may be too short or too long depending on the deployment size.
- **Remediation**: Make it configurable or at least a named constant.

### N-8. Stats port allocation counts down from LinkPortBase - 1
- **Line**: 210
- **Category**: Configuration/magic numbers
- **Severity**: MEDIUM
- Bridge stats ports are allocated by counting down from `LinkPortBase - 1`. This allocation scheme is duplicated between `Deploy()` (line 210-214) and `allocateBridgeStatsPorts()` in probe.go (line 77-99). If either changes, they will diverge.
- **Remediation**: Call `allocateBridgeStatsPorts()` from `Deploy()` instead of reimplementing the same logic.

### N-9. WaitForSSH timeout is hardcoded to 60 seconds
- **Line**: 342
- **Category**: Configuration/magic numbers
- **Severity**: LOW
- `WaitForSSH(..., 60*time.Second)` ignores `node.BootTimeout` for the SSH wait phase. The BootTimeout is only used for `BootstrapNetwork`. If the VM is slow to start SSH after bootstrap, 60s may be insufficient.
- **Remediation**: Use `node.BootTimeout` or a separate configurable value.

---

## pkg/newtlab/state.go

### ST-1. `LabDir` and `ListLabs` silently ignore `UserHomeDir` errors
- **Line**: 51, 96
- **Category**: Missing error handling
- **Severity**: MEDIUM
- `home, _ := os.UserHomeDir()` discards the error. If HOME is unset (e.g., in a container), `home` will be empty, producing paths like `"/.newtlab/labs/..."` rooted at filesystem root.
- **Remediation**: Return an error from `LabDir` or propagate it. At minimum, check for empty `home`.

### ST-2. Deprecated `BridgePID` field still in LabState
- **Line**: 18
- **Category**: Dead code
- **Severity**: LOW
- `BridgePID int` is marked as deprecated in favor of `Bridges` map, but is still read in `Destroy()` and `destroyExisting()` for backward compatibility. If no pre-v4 state files exist, this can be removed.
- **Remediation**: Document a deprecation timeline or remove if migration is complete.

---

## pkg/newtlab/node.go

### NO-1. `ResolveNodeConfig` uses switch statements for every field
- **Line**: 52-137
- **Category**: Code duplication
- **Severity**: LOW
- The "profile > platform > default" resolution pattern is repeated 8+ times with nearly identical switch/if structures. This is verbose but readable. Not a high priority, but a merge helper could reduce it.
- **Remediation**: Consider a generic `resolveField[T](profile, platform T, default T) T` helper, but only if the verbosity becomes a maintenance burden.

---

## pkg/newtlab/link.go

### L-1. `sortedNodeNames` is defined in link.go but used in newtlab.go, placement.go, and probe.go
- **Line**: 347-354
- **Category**: Structural issues
- **Severity**: LOW
- A utility function defined in `link.go` but used across multiple files. The function name doesn't relate to link allocation.
- **Remediation**: Move to a shared location (e.g., `util.go` within the package) or rename to clarify it's a general utility.

### L-2. `countingWriter` defined in link.go
- **Line**: 16-25
- **Category**: Structural issues
- **Severity**: LOW
- `countingWriter` is a type used only by `BridgeWorker.run()` in the same file. It's fine where it is, but if the bridge is ever extracted to its own package, it should move with it.

---

## pkg/newtlab/qemu.go

### Q-1. SSH command construction is duplicated across files
- **Line**: 143, 200, 216, 243
- **Category**: Code duplication
- **Severity**: MEDIUM
- `exec.Command("ssh", "-o", "StrictHostKeyChecking=no", hostIP, ...)` appears 15+ times across qemu.go, disk.go, bridge.go, remote.go, and probe.go. Each occurrence may or may not include `-o ConnectTimeout=...`.
- **Remediation**: Extract an `sshCommand(hostIP string, cmd string, opts ...string) *exec.Cmd` helper that always includes the standard SSH options.

### Q-2. `stopNodeLocal` returns nil when SIGTERM fails
- **Line**: 178-182
- **Category**: Missing error handling
- **Severity**: LOW
- If `process.Signal(SIGTERM)` fails, the function returns nil, assuming the process is dead. But the error could be a permission issue (EPERM) where the process is alive but not killable.
- **Remediation**: Check the specific error type. Only treat ESRCH (no such process) as success.

### Q-3. `StopNodeRemote` discards SIGKILL error
- **Line**: 216-217
- **Category**: Missing error handling
- **Severity**: LOW
- The force-kill via SIGKILL discards the `cmd.Run()` error. If SSH connectivity is lost, the process may remain running.
- **Remediation**: Log the error as a warning.

### Q-4. `quoteArgs` doesn't handle all shell special characters
- **Line**: 259-269
- **Category**: Missing error handling
- **Severity**: MEDIUM
- The character set check doesn't cover all shell metacharacters (e.g., `^`, `%` in some shells, newlines within arguments). Also, single-quote escaping via `'\''` is correct for POSIX sh, but fragile.
- **Remediation**: Consider always quoting arguments, or use a well-tested shell quoting library.

---

## pkg/newtlab/disk.go

### DK-1. `CreateOverlayRemote` doesn't shell-quote paths
- **Line**: 30-31
- **Category**: Missing error handling
- **Severity**: MEDIUM
- `fmt.Sprintf("qemu-img create -f qcow2 -b %s -F qcow2 %s", baseImage, overlayPath)` doesn't quote the path arguments. If `baseImage` or `overlayPath` contain spaces, the remote command will break.
- **Remediation**: Use `quoteArgs` or single-quote the paths.

### DK-2. `expandHome` and `unexpandHome` ignore `UserHomeDir` error
- **Line**: 74, 82
- **Category**: Missing error handling
- **Severity**: LOW
- Same pattern as ST-1. `home, _ := os.UserHomeDir()` silently fails.
- **Remediation**: Return an error or use a package-level cached home dir.

### DK-3. `cleanupRemoteStateDir` uses unquoted path in `rm -rf`
- **Line**: 63-64
- **Category**: Missing error handling
- **Severity**: MEDIUM
- `rm -rf %s` where `stateDir` contains `~/.newtlab/labs/%s`. If `labName` were somehow crafted with spaces or shell metacharacters, this could be dangerous. The ~ expansion also relies on shell behavior.
- **Remediation**: Quote the path in the remote command.

---

## pkg/newtlab/boot.go

### BO-1. `BootstrapNetwork` has deeply nested closure for `readUntil`
- **Line**: 83-104
- **Category**: Structural issues
- **Severity**: LOW
- The `readUntil` closure captures `conn` from the outer scope. This is fine for a single function, but makes the flow harder to follow at 150+ lines. The function does: connect, login, DHCP, user creation, logout -- five distinct phases.
- **Remediation**: Consider breaking into phases or at least adding section comments (which partially exist).

### BO-2. `readUntil` allocates 4096-byte buffer per read call
- **Line**: 88
- **Category**: Configuration/magic numbers
- **Severity**: LOW
- `make([]byte, 4096)` on every read call within the polling loop. Not performance-critical for serial console, but unnecessarily wasteful.
- **Remediation**: Allocate once outside the loop.

### BO-3. Password sent in cleartext over serial console
- **Line**: 127, 144
- **Category**: Missing error handling
- **Severity**: LOW
- The console password and `chpasswd` command are sent over a TCP connection. This is inherent to serial console operation and documented, but the `chpasswd` command writes the password to the shell history.
- **Remediation**: Consider using `echo '...' | sudo chpasswd` with `HISTCONTROL=ignorespace` or `unset HISTFILE` beforehand.

### BO-4. `remaining` override logic is confusing
- **Line**: 107-109
- **Category**: Inconsistent patterns
- **Severity**: LOW
- `if remaining < 30*time.Second { remaining = 30 * time.Second }` -- if the caller-provided timeout has almost expired, this extends it by up to 30s. This is undocumented behavior that may cause the function to exceed its contract.
- **Remediation**: Document this clearly in the function's doc comment, or remove the override.

---

## pkg/newtlab/bridge.go

### BR-1. `RunBridgeFromFile` ignores `splitLinkEndpoint` errors
- **Line**: 105-106
- **Category**: Missing error handling
- **Severity**: MEDIUM
- `aDevice, aIface, _ := splitLinkEndpoint(bl.A)` discards the parse error. If the bridge.json has a malformed endpoint label, the bridge starts with empty device/interface names.
- **Remediation**: Check the error and return it.

### BR-2. Bridge pid file write ignores error
- **Line**: 124
- **Category**: Missing error handling
- **Severity**: LOW
- `os.WriteFile(pidFile, ...)` return value is ignored. If the state directory is read-only, the PID file won't be written and there's no indication.
- **Remediation**: Check and log the error.

### BR-3. `startBridgeProcess` and `startBridgeProcessRemote` share the detach/log/pid pattern
- **Line**: 244-281, 286-322
- **Category**: Code duplication
- **Severity**: LOW
- Both spawn a process, redirect to log file, detach, and return PID. The remote version additionally uploads a binary and uses SSH. The common subprocess management pattern could be extracted.
- **Remediation**: Minor; leave as-is unless more process-spawning patterns appear.

---

## pkg/newtlab/iface_map.go

No significant issues. Clean, well-tested code.

---

## pkg/newtlab/placement.go

No significant issues. Clean, well-tested code with 7 tests covering all edge cases.

---

## pkg/newtlab/probe.go

### PR-1. `ProbeAllPorts` uses `fmt.Errorf` with `%s` for a multi-line error
- **Line**: 158
- **Category**: Inconsistent patterns
- **Severity**: LOW
- `fmt.Errorf("%s", strings.Join(errs, "\n"))` creates an error whose message contains newlines. This doesn't implement `errors.Unwrap` and loses structured error information.
- **Remediation**: Use `errors.Join` or a custom multi-error type.

### PR-2. `contains` and `containsStr` in probe_test.go reimplements `strings.Contains`
- **Line**: probe_test.go:173-184
- **Category**: Dead code / Inconsistent patterns
- **Severity**: LOW
- `containsStr` is a manual reimplementation of `strings.Contains`. The `contains` wrapper adds nothing. Both are used in tests but `strings.Contains` is already imported in other test files.
- **Remediation**: Replace with `strings.Contains`.

---

## pkg/newtlab/profile.go

### PF-1. `PatchProfiles` uses `map[string]interface{}` for JSON round-trip
- **Line**: 22
- **Category**: Inconsistent patterns
- **Severity**: MEDIUM
- Profiles are loaded as `map[string]interface{}` and re-serialized. This approach preserves unknown fields but loses type safety. All other profile loading in the codebase uses `spec.DeviceProfile`.
- **Remediation**: Use `spec.DeviceProfile` with a raw JSON fallback for unknown fields (json.RawMessage), or use `json.Decoder` with `DisallowUnknownFields: false`.

### PF-2. `PatchProfiles` only writes ssh_user/ssh_pass if not already present
- **Line**: 41-49
- **Category**: Inconsistent patterns
- **Severity**: LOW
- The `if _, exists := profile["ssh_user"]; !exists` guard prevents overwriting existing credentials, which is correct. But `RestoreProfiles` doesn't remove ssh_user/ssh_pass (only ssh_port and console_port). This means if `PatchProfiles` wrote them, they persist after `RestoreProfiles`. This is actually correct behavior (don't remove credentials the user may have set), but the asymmetry is confusing.
- **Remediation**: Add a comment explaining why ssh_user/ssh_pass are NOT removed in RestoreProfiles.

---

## pkg/newtlab/patch.go

### PA-1. `ApplyBootPatches` creates a new SSH session per command
- **Line**: 173-181
- **Category**: Structural issues
- **Severity**: MEDIUM
- The `run` closure creates a new SSH session for every command. For patches with many pre_commands + files + redis + post_commands, this could be dozens of SSH session handshakes. SSH sessions are lightweight on a persistent connection, but the connection itself could be flaky over a QEMU virtual network.
- **Remediation**: Consider using a single session with a shell (stdin pipe) for batch execution, or at least document this as a known cost.

### PA-2. `renderTemplate` re-parses template on every call
- **Line**: 281-297
- **Category**: Inconsistent patterns
- **Severity**: LOW
- Templates are read from the embedded FS and parsed each time. For a small number of patches this is fine, but if the same template is used across many nodes in a large topology, it could be cached.
- **Remediation**: Low priority. Add caching only if it becomes measurable.

---

## pkg/newtlab/remote.go

### R-1. `uploadNewtlink` makes 4 sequential SSH/SCP calls
- **Line**: 99-147
- **Category**: Structural issues
- **Severity**: LOW
- Version check, mkdir, scp, chmod are 4 separate SSH round-trips. These could be batched into fewer calls (e.g., one SSH call to check version + mkdir, then scp, then chmod).
- **Remediation**: Low priority optimization for multi-host deployments.

### R-2. `uploadNewtlink` version check parses specific format
- **Line**: 109-111
- **Category**: Missing error handling
- **Severity**: LOW
- Compares exact string `"newtlink <version> (<commit>)"`. If the version format changes (e.g., adding build metadata), the check will always re-upload.
- **Remediation**: Compare just the version string, not the full output line.

---

## pkg/newtlab/newtlab_test.go

### NT-1. Tests modify global `HOME` environment variable
- **Line**: 413-414, 479-480, 493-494, etc.
- **Category**: Inconsistent patterns
- **Severity**: MEDIUM
- Multiple tests do `os.Setenv("HOME", tmpDir)` and `defer os.Setenv("HOME", origHome)`. This is not parallel-safe -- if tests run with `t.Parallel()`, they will race on the HOME variable. Currently tests do not use `t.Parallel()`, but adding it later would break.
- **Remediation**: Use `t.Setenv()` (Go 1.17+) which automatically prevents parallel execution and restores the value. Some tests already use `t.Setenv` (in remote_test.go) while others use the manual pattern -- make this consistent.

### NT-2. `containsStr` and `contains` test helpers reimplementing stdlib
- **Line**: See PR-2 above
- **Category**: Dead code
- **Severity**: LOW

---

## Files Without Test Coverage

### Missing tests (no corresponding `_test.go` files or functions):

| File | Test Coverage | Severity |
|------|--------------|----------|
| `boot.go` | No tests | MEDIUM |
| `profile.go` | No tests | MEDIUM |
| `qemu.go` | Partial (Build tested, Start/Stop not tested) | MEDIUM |
| `bridge.go` (process management functions) | Partial (bridge workers tested, process start/stop not) | LOW |
| `disk.go` (overlay creation) | No tests (expandHome/unexpandHome tested) | LOW |
| `link.go` (connectAddr, PlaceWorkers) | Partial (PlaceWorkers tested, connectAddr not) | LOW |
| `newtlab.go` (Deploy, Destroy, Provision, etc.) | No tests | HIGH |

The overall package has 33.8% statement coverage. The tested code is the pure-logic portions (interface map, node resolution, link allocation, placement, patch resolution, bridge workers, state persistence). The untested code is the orchestration layer (Deploy, Destroy, Start, Stop, Provision) which involves QEMU processes, SSH, and filesystem operations.

**Remediation**: The orchestration methods are hard to unit test without mocking. Consider:
1. Extracting interfaces for process management (`ProcessManager`), SSH (`SSHClient`), and filesystem operations (`DiskManager`).
2. Writing integration tests that run against mock QEMU (or use the test topology).
3. At minimum, test `resolveNewtLabConfig`, `resolveHostIP`, `resolveWorkerHostIP`, `connectAddr`, and `sortedHosts` which are pure functions but currently untested.

---

## Cross-Cutting Concerns

### X-1. No context.Context support anywhere
- **Category**: Structural issues
- **Severity**: MEDIUM
- None of the public API functions accept `context.Context`. This means:
  - No way to cancel a long-running deploy mid-flight
  - No way to set overall timeout for operations
  - Signal handling is ad-hoc (bridge uses `signal.Notify` directly)
- **Remediation**: Add `context.Context` as first parameter to `Deploy`, `Destroy`, `Start`, `Stop`, `Provision`, `WaitForSSH`, `BootstrapNetwork`, and `ApplyBootPatches`. Wire cancellation through goroutines.

### X-2. Inconsistent SSH option sets
- **Category**: Inconsistent patterns
- **Severity**: MEDIUM
- Some SSH commands include `-o ConnectTimeout=10` (remote.go:46), some include `-o ConnectTimeout=5` (probe.go:189), and most include neither (qemu.go, disk.go, bridge.go). A lost-connectivity SSH call without a timeout will hang indefinitely.
- **Remediation**: Always include `ConnectTimeout` in SSH invocations. Use the `sshCommand` helper from Q-1.

### X-3. No logging throughout orchestration
- **Category**: Missing error handling
- **Severity**: MEDIUM
- The Deploy/Destroy/Provision methods use `fmt.Printf` in the CLI layer but the `pkg/newtlab` layer has no logging. When something fails mid-deploy, there's no way to see which step succeeded. The `util.SetLogLevel` is set in `cmd/newtlab/main.go` but never used in `pkg/newtlab`.
- **Remediation**: Add structured logging (using the project's `util` log package) to key orchestration steps: node start, bridge start, SSH wait, bootstrap, patch apply.

### X-4. `os.UserHomeDir()` called in 5 different places without caching
- **Category**: Code duplication
- **Severity**: LOW
- `os.UserHomeDir()` is called (with error silently discarded) in `state.go:51`, `state.go:96`, `disk.go:74`, `disk.go:82`, and `remote.go:86`. Each call is independent.
- **Remediation**: Cache the home directory in a package-level variable initialized once, with proper error handling.

---

## TODO/FIXME/HACK Comments

No TODO, FIXME, HACK, or XXX comments found in either `cmd/newtlab/` or `pkg/newtlab/`. This is either a sign of thorough development or of technical debt not being tracked in-code.

---

## Priority Remediation Order

### Correctness (must fix):
1. **P-1** (HIGH): `--device` provision filter is a no-op
2. **M-5** (HIGH): `destroyExisting` error discarded
3. **S-2** (HIGH): SSH hardcodes `127.0.0.1` for remote nodes
4. **C-1** (HIGH): Console hardcodes `127.0.0.1` for remote nodes

### Quality (should fix):
5. **Q-1** (MEDIUM): Extract SSH command helper (reduces 15+ duplications)
6. **N-2** (MEDIUM): Extract host IP resolution helper
7. **N-3** (MEDIUM): Extract bridge shutdown helper
8. **N-8** (MEDIUM): Unify stats port allocation between Deploy and probe
9. **X-2** (MEDIUM): Consistent SSH timeouts
10. **X-3** (MEDIUM): Add orchestration logging

### Polish (nice to have):
11. **M-1** (MEDIUM): Unify resolveSpecDir/resolveLabName
12. **Y-1** (MEDIUM): Partial Lab struct pattern
13. **X-1** (MEDIUM): context.Context support
14. **NT-1** (MEDIUM): t.Setenv consistency
