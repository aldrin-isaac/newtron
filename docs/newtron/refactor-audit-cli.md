# Newtron CLI Layer Code Quality Audit

**Scope:** All 20 `.go` files in `cmd/newtron/`
**Date:** 2026-02-13
**Methodology:** Line-by-line read of every file, cross-referenced for patterns

---

## Table of Contents

1. [Summary](#summary)
2. [Findings by Category](#findings-by-category)
   - [Code Duplication](#1-code-duplication)
   - [Naming Inconsistencies](#2-naming-inconsistencies)
   - [Dead Code](#3-dead-code)
   - [Missing or Inadequate Error Handling](#4-missing-or-inadequate-error-handling)
   - [Structural Issues](#5-structural-issues)
   - [Inconsistent Patterns](#6-inconsistent-patterns)
   - [TODO/FIXME/HACK Comments](#7-todofixmehack-comments)
   - [Configuration/Magic Numbers](#8-configurationmagic-numbers)
3. [Finding Details](#finding-details)

---

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | 7     |
| MEDIUM   | 16    |
| LOW      | 10    |
| **Total**| **33**|

The CLI layer is generally well-structured. The `withDeviceWrite` helper eliminated much of the connect/lock/changeset boilerplate for noun-group write commands. The main systemic issues are:

- **interactive.go is 1036 lines of duplicated lock/preview/confirm/apply patterns** that exist in parallel with the noun-group CLI and share no code with it.
- **Status formatting (up/down colorization)** is copy-pasted across 6+ files with minor variations.
- **Package-level mutable globals** for all flags and state, making the CLI untestable and creating hidden coupling between files.
- **Several dead variables** (declared and flag-bound but never read in logic).
- **Inconsistent changeset rendering** (`cs.String()` in noun-group commands vs `cs.Preview()` in interactive mode).

---

## Findings by Category

### 1. Code Duplication

#### D-01: Interactive mode duplicates entire noun-group CLI
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Lines:** 1-1036 (entire file)
- **Severity:** HIGH
- **Description:** `interactive.go` reimplements every operation available in the noun-group CLI (service apply/remove, LAG create/add-member/remove-member, VLAN create/add-member, BGP setup/add-neighbor/remove-neighbor, health checks, ACL listing, EVPN status) using a menu-driven TUI. Each operation has its own lock/unlock, changeset preview, confirm prompt, and apply logic -- none of which shares code with `withDeviceWrite`, `executeAndSave`, or the noun-group commands. This is ~1000 lines of parallel implementation.
- **Remediation:** The interactive mode is marked `Hidden: true` and appears to be a legacy precursor to the noun-group CLI. Consider (a) deleting it entirely, or (b) refactoring it to call the same operation functions the noun-group commands use, with a `confirmBeforeExecute` wrapper.

#### D-02: Lock/preview/confirm/apply pattern repeated 10 times in interactive.go
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Lines:** 162-200, 211-245, 394-423, 438-465, 476-507, 566-596, 615-644, 900-931, 958-984, 996-1022
- **Severity:** HIGH
- **Description:** The exact same 15-line pattern appears 10 times:
  ```go
  if err := dev.Lock(); err != nil { ... }
  cs, err := <operation>
  dev.Unlock()
  if err != nil { ... }
  fmt.Println(cs.Preview())
  fmt.Print("\nExecute? [y/N]: ")
  confirm, _ := reader.ReadString('\n')
  if confirm == "y" || confirm == "yes" {
      if err := cs.Apply(dev); err != nil { ... }
  }
  ```
- **Remediation:** Extract an `interactiveWriteOp(reader, dev, fn func() (*network.ChangeSet, error)) error` helper that encapsulates the lock/preview/confirm/apply cycle.

#### D-03: Admin status colorization repeated across 6+ files
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_interface.go` (lines 63-69, 141-155), `cmd_lag.go` (lines 68-73, 122-127, 222-237), `cmd_bgp.go` (lines 93-101, 136-140), `cmd_evpn.go` (lines 157-163), `cmd_vrf.go` (lines 194-199), `cmd_vlan.go` (lines 227-229)
- **Severity:** MEDIUM
- **Description:** The pattern of coloring "up" green and non-empty other values red is implemented ad hoc in at least 12 locations with minor variations (some check for empty strings, some don't; some color "down" specifically, others color any non-"up" value).
- **Remediation:** Add a `formatAdminStatus(status string) string` and `formatOperStatus(status string) string` helper to `main.go` (near the existing `formatStatus` in health, or as a shared utility). All files should call these instead of inlining the logic.

#### D-04: VLAN ID parsing duplicated 7 times
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_vlan.go`
- **Lines:** 104-107, 263-266, 298-300, 329-331, 358-360, 402-404, 437-439, 485-488
- **Severity:** MEDIUM
- **Description:** The exact pattern `var vlanID int; if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil { return fmt.Errorf("invalid VLAN ID: %s", args[0]) }` is repeated 7 times in cmd_vlan.go and once in cmd_qos.go (for queue-id parsing).
- **Remediation:** Extract a `parseVLANID(s string) (int, error)` helper function.

#### D-05: Duplicate `showDevice` functions in shell.go and cmd_show.go
- **File:** `/home/aldrin/src/newtron/cmd/newtron/shell.go` (lines 107-121) and `/home/aldrin/src/newtron/cmd/newtron/cmd_show.go` (lines 40-63)
- **Severity:** MEDIUM
- **Description:** Two different `showDevice` functions exist: `Shell.showDevice()` and the top-level `showDevice(dev)`. They display overlapping but different information (the shell version shows BGP AS/RouterID inline; cmd_show shows MgmtIP/Platform/Site/VTEPSourceIP). Neither calls the other.
- **Remediation:** Unify into a single `showDevice(dev)` function that both the shell and the `show` command invoke. The shell can call it and add any shell-specific context afterward.

#### D-06: "No device connected" guard repeated 18 times in interactive.go
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Lines:** 136, 207, 287, 314, 359, 379, 425, 467, 529, 553, 599, 660, 670, 706, 736, 790, 822, 866
- **Severity:** LOW
- **Description:** The `if dev == nil { fmt.Println(red("No device connected")); continue }` guard is repeated at the start of almost every interactive menu action.
- **Remediation:** Extract to a `requireInteractiveDev(dev) bool` helper that prints the message and returns false if nil, allowing `if !requireInteractiveDev(dev) { continue }`.

---

### 2. Naming Inconsistencies

#### N-01: `defaultStr` helper in cmd_acl.go is generic but file-local
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_acl.go`
- **Lines:** 364-369
- **Severity:** LOW
- **Description:** `defaultStr(s, def string) string` is defined in cmd_acl.go but also used in cmd_filter.go (lines 108-114). Since both files are in `package main`, this works, but it is not obvious where the function lives. It belongs alongside the other helpers in main.go.
- **Remediation:** Move `defaultStr` to `main.go` near the other output helpers.

#### N-02: Mixed `intfArg` vs `intfName` naming for interface positional argument
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_service.go` (lines 225, 283, 365), `/home/aldrin/src/newtron/cmd/newtron/cmd_interface.go` (line 122: `intfName`), `/home/aldrin/src/newtron/cmd/newtron/cmd_vrf.go` (line 283: `intfName`)
- **Severity:** LOW
- **Description:** The variable name for the interface positional argument is `intfArg` in service commands but `intfName` in interface and VRF commands. Inconsistent within the same package.
- **Remediation:** Standardize on `intfName` throughout (it is the more descriptive name).

#### N-03: Inconsistent naming for VPN variables (`ipvpnDescription` vs `macvpnDescription`)
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_evpn.go`
- **Lines:** 267-272, 450-454
- **Severity:** LOW
- **Description:** Flag variables for IPVPN and MACVPN create commands use a flat naming scheme (`ipvpnL3VNI`, `macvpnL2VNI`) but the general pattern in other files uses a prefix indicating the parent command (e.g., `svcCreateType` in cmd_service.go, `filterCreateType` in cmd_filter.go). The evpn file uses no `Create` infix.
- **Remediation:** Rename to `ipvpnCreateL3VNI`, `macvpnCreateL2VNI`, etc. for consistency with `svcCreateType` and `filterCreateType`.

---

### 3. Dead Code

#### DC-01: `lagMode` variable declared and flag-registered but never used
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_lag.go`
- **Lines:** 263 (declaration), 394 (flag registration)
- **Severity:** HIGH
- **Description:** `lagMode` is declared as a package-level `string`, registered as `--mode` flag on `lagCreateCmd`, but is never read anywhere. It is not passed to `network.PortChannelConfig{}` on line 297. Users can set `--mode active` but the value is silently ignored.
- **Remediation:** Either wire `lagMode` into `PortChannelConfig` and pass it through to CONFIG_DB, or remove the flag entirely to avoid misleading users.

#### DC-02: `vlanName` variable declared and flag-registered but never used
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_vlan.go`
- **Lines:** 248 (declaration), 512 (flag registration)
- **Severity:** HIGH
- **Description:** `vlanName` is registered as `--name` flag on `vlanCreateCmd` but is never read. The VLAN create command passes only `vlanDescription` (line 273) to `VLANConfig{}`. Users can specify `--name Frontend` but it does nothing.
- **Remediation:** Wire `vlanName` into `VLANConfig` (e.g., as a distinct `Name` field), or remove the flag.

#### DC-03: `networkName` is set but never used beyond defaulting
- **File:** `/home/aldrin/src/newtron/cmd/newtron/main.go`
- **Lines:** 50, 140-141, 190
- **Severity:** MEDIUM
- **Description:** `networkName` is populated from `-n` flag or settings default, but it is never passed to `network.NewNetwork()` or used anywhere in the codebase. The `NewNetwork` call on line 161 takes only `specDir`. The `-n` flag creates a false promise that multi-network selection works.
- **Remediation:** Either implement network selection (passing `networkName` to the spec loader/network constructor) or remove the flag to avoid confusion.

#### DC-04: `loader` is initialized but unused outside initialization
- **File:** `/home/aldrin/src/newtron/cmd/newtron/main.go`
- **Lines:** 62 (declaration), 155-158 (initialization), 167 (used once for `permChecker`)
- **Severity:** MEDIUM
- **Description:** `loader` is a package-level `*spec.Loader` that is loaded and used only to create the `permChecker` (line 167). Every command that needs specs accesses them through `net` (the `*network.Network`). The `loader` variable is an unnecessary second path to specs.
- **Remediation:** If `permChecker` can get its `NetworkSpec` from `net.Spec()` instead of `loader.GetNetwork()`, the `loader` global can be removed entirely.

#### DC-05: `_ = vrfName` dead assignment in vrfAddNeighborCmd
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_vrf.go`
- **Line:** 422
- **Severity:** LOW
- **Description:** `_ = vrfName` is a blank assignment that exists purely to suppress the "unused variable" error. The VRF name is used later on line 435 to verify the interface's VRF, but the blank assignment on line 422 is confusing dead code that suggests `vrfName` might be intentionally unused.
- **Remediation:** Remove the `_ = vrfName` line. The variable is in fact used on line 435.

---

### 4. Missing or Inadequate Error Handling

#### E-01: `reader.ReadString` errors silently ignored in interactive.go
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Lines:** 80, 122, 141-142, 146, 149, etc. (at least 30 locations)
- **Severity:** MEDIUM
- **Description:** Every `reader.ReadString('\n')` call discards its error via `_, _ = reader.ReadString(...)`. If stdin is piped and reaches EOF, the loop continues with an empty string rather than exiting cleanly.
- **Remediation:** Check the error return. On `io.EOF`, break out of the menu loop gracefully.

#### E-02: `peerASNum, _ = strconv.Atoi(peerASStr)` silently sets 0 on parse failure
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Line:** 159
- **Severity:** MEDIUM
- **Description:** If the user enters a non-numeric string for peer AS number, `Atoi` returns 0 with a non-nil error, but the error is discarded. This silently passes `PeerAS: 0` to `ApplyService`, which may create a BGP neighbor with AS 0.
- **Remediation:** Check the error and print a message like `"Invalid peer AS number"` if it fails.

#### E-03: `dev.Unlock()` called without checking if `Lock()` succeeded in interactive.go
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`
- **Lines:** 180 (`Lock` on 164, `Unlock` on 180), etc.
- **Severity:** LOW
- **Description:** In interactive mode, when `dev.Lock()` fails, control skips to `continue`. But there are some code paths (e.g., case "3" on line 900+) where `dev.Unlock()` is called without using `defer`, so if the operation between Lock and Unlock panics or returns early, the device remains locked. This is not a critical issue in the current codebase since panics are unlikely, but `defer dev.Unlock()` would be safer.
- **Remediation:** Use `defer dev.Unlock()` after successful `Lock()` in interactive mode, matching the pattern in `withDeviceWrite`.

#### E-04: Spec authoring commands (filter create, filter add-rule, filter delete) skip dry-run check
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_filter.go`
- **Lines:** 139-172 (filterCreateCmd), 234-292 (filterAddRuleCmd), 185-204 (filterDeleteCmd)
- **Severity:** MEDIUM
- **Description:** The `filter create`, `filter add-rule`, `filter remove-rule`, and `filter delete` commands do not check `executeMode`. They always write to network.json immediately, bypassing the dry-run semantics that other write commands respect. Compare with `evpn ipvpn create` (cmd_evpn.go line 320-323) which does check `!executeMode` and calls `printDryRunNotice()`. The same issue exists for `qos create`, `qos delete`, `qos add-queue`, `qos remove-queue`, `service create`, and `service delete`.
- **Remediation:** Add `if !executeMode { printDryRunNotice(); return nil }` before the actual save/delete call in all spec authoring commands, matching the ipvpn/macvpn pattern.

---

### 5. Structural Issues

#### S-01: All state stored in package-level globals
- **File:** `/home/aldrin/src/newtron/cmd/newtron/main.go`
- **Lines:** 48-65
- **Severity:** HIGH
- **Description:** Six mutable globals (`networkName`, `deviceName`, `specDir`, `executeMode`, `saveMode`, `verbose`, `jsonOutput`, `userSettings`, `loader`, `net`, `permChecker`) are shared across all 20 files. Every command implicitly depends on this state being initialized by `PersistentPreRunE`. This makes the CLI impossible to unit test (you cannot run two commands in the same process without resetting globals) and creates invisible coupling. Additionally, all flag variables for all subcommands (e.g., `lagMode`, `aclType`, `rulePriority`, `sviVRF`, `addQueueType`, etc.) are package-level, meaning they persist between subcommand invocations in interactive scenarios.
- **Remediation:** Long-term: wrap all state in an `App` struct and pass it through cobra's context or as a closure. Short-term: at minimum, document that globals are set during `PersistentPreRunE` and are not safe for concurrent use.

#### S-02: interactive.go and shell.go are parallel interactive systems
- **File:** `/home/aldrin/src/newtron/cmd/newtron/interactive.go`, `/home/aldrin/src/newtron/cmd/newtron/shell.go`
- **Severity:** MEDIUM
- **Description:** Two separate interactive systems exist: `interactive.go` (number-based menu TUI, 1036 lines) and `shell.go` (command-based REPL, 483 lines). Both are hidden commands. They share no code and have different capabilities: the shell has composite build mode and dirty tracking; interactive has health/ACL/EVPN menus. Neither has been updated to match the current noun-group CLI surface.
- **Remediation:** Decide which interactive mode to keep. The shell (REPL) is the more natural fit for the noun-group CLI's design. Consider deleting interactive.go and extending shell.go as needed, or deleting both since the non-interactive noun-group CLI is the primary UX.

#### S-03: `cmd_show.go` duplicates device-checking logic from `requireDevice`
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_show.go`
- **Lines:** 26-29
- **Severity:** LOW
- **Description:** `showCmd.RunE` manually checks `if deviceName == ""` and calls `net.ConnectDevice` directly, duplicating what `requireDevice(ctx)` does. All other device-requiring commands use `requireDevice`.
- **Remediation:** Replace lines 26-33 with `dev, err := requireDevice(ctx)`.

---

### 6. Inconsistent Patterns

#### I-01: `cs.String()` vs `cs.Preview()` for changeset rendering
- **File:** `/home/aldrin/src/newtron/cmd/newtron/main.go` (line 345), `/home/aldrin/src/newtron/cmd/newtron/shell.go` (lines 264, 301), `/home/aldrin/src/newtron/cmd/newtron/interactive.go` (10 locations using `cs.Preview()`)
- **Severity:** MEDIUM
- **Description:** The `withDeviceWrite` helper and shell use `changeSet.String()` to render changesets, while interactive.go uses `cs.Preview()` everywhere. These may produce different output. The codebase should use one rendering method consistently.
- **Remediation:** Standardize on one method. If `String()` and `Preview()` have different semantics (e.g., Preview is a summary while String is detailed), document why each site chose its method. Otherwise, pick one.

#### I-02: Read commands inconsistently handle jsonOutput
- **Files:** `cmd_vrf.go` (vrfListCmd, vrfShowCmd -- no jsonOutput), `cmd_interface.go` (interfaceListCmd -- has jsonOutput), `cmd_lag.go` (lagListCmd -- has jsonOutput), `cmd_vlan.go` (vlanListCmd -- has jsonOutput), `cmd_bgp.go` (bgpStatusCmd -- no jsonOutput), `cmd_evpn.go` (evpnStatusCmd, evpnIpvpnListCmd -- no jsonOutput)
- **Severity:** MEDIUM
- **Description:** Some read commands support `--json` output and some do not. The `vrf list`, `vrf show`, `vrf status`, `bgp status`, `evpn status`, `evpn ipvpn list`, `evpn macvpn list` commands do not check `jsonOutput`. The `interface list`, `lag list`, `lag show`, `lag status`, `vlan list`, `vlan show`, `vlan status`, `service list`, `service show`, `service get`, `health check`, `filter list`, `filter show`, `qos list`, `qos show`, `audit list` commands do. This is inconsistent UX.
- **Remediation:** Add `--json` support to all read commands. The simplest approach: define a JSON-serializable struct for each display and `json.NewEncoder(os.Stdout).Encode(it)` when `jsonOutput` is true.

#### I-03: Spec authoring commands inconsistently check dry-run
- **File:** Multiple files
- **Severity:** MEDIUM (duplicate of E-04, listed here for pattern context)
- **Description:** `evpn ipvpn create/delete` and `evpn macvpn create/delete` check `!executeMode` before writing. `filter create/delete/add-rule/remove-rule`, `qos create/delete/add-queue/remove-queue`, `service create/delete` do not. The `-x` flag is registered on these parent commands but some subcommands ignore it.
- **Remediation:** Make all spec authoring commands respect dry-run consistently.

#### I-04: Permission check uses PermACLModify for device cleanup
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_device.go`
- **Line:** 47
- **Severity:** LOW
- **Description:** `deviceCleanupCmd` checks `auth.PermACLModify` permission, but cleanup can delete VRFs and VNI mappings too, not just ACLs. This appears to be a placeholder permission.
- **Remediation:** Create a `auth.PermDeviceCleanup` permission or use a broader permission like `auth.PermDeviceModify`.

#### I-05: Delete commands use Create permission instead of Delete
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_vlan.go` (line 364), `/home/aldrin/src/newtron/cmd/newtron/cmd_lag.go` (line 379), `/home/aldrin/src/newtron/cmd/newtron/cmd_vrf.go` (line 259)
- **Severity:** LOW
- **Description:** `vlanDeleteCmd` uses `auth.PermVLANCreate`, `lagDeleteCmd` uses `auth.PermLAGCreate`, `vrfDeleteCmd` uses `auth.PermVRFCreate`. Delete operations should use their own permission constants (e.g., `auth.PermVLANDelete`).
- **Remediation:** Create `PermVLANDelete`, `PermLAGDelete`, `PermVRFDelete` constants and use them in the corresponding delete commands.

#### I-06: `withDeviceWrite` used by some write commands but not `showCmd`
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_show.go`
- **Severity:** LOW
- **Description:** `showCmd` manually handles connect/disconnect/error rather than using a hypothetical `withDeviceRead` helper. All read-only device commands (show, list, status, get) independently call `requireDevice` + `defer dev.Disconnect()`. This is a minor inconsistency but is understandable since read commands don't need locking.
- **Remediation:** Consider a `withDeviceRead(fn func(ctx, dev) error) error` helper for read commands to eliminate the repeated `requireDevice` + `defer Disconnect` 3-line pattern. Low priority since the pattern is simple.

---

### 7. TODO/FIXME/HACK Comments

#### T-01: No TODO/FIXME/HACK markers found
- **Severity:** N/A
- **Description:** A `grep` for `TODO`, `FIXME`, `HACK`, `XXX`, and `WORKAROUND` across all 20 files returned zero results. This is good -- no acknowledged debt markers in the CLI layer.

---

### 8. Configuration/Magic Numbers

#### M-01: Audit log path and rotation config hardcoded
- **File:** `/home/aldrin/src/newtron/cmd/newtron/main.go`
- **Lines:** 170-177
- **Severity:** MEDIUM
- **Description:** The audit log path is hardcoded to `/var/log/newtron/audit.log` (or `specDir + "/audit.log"`), and the rotation config is hardcoded to `MaxSize: 10MB, MaxBackups: 10`. These should be configurable via settings or flags.
- **Remediation:** Add `audit_log_path`, `audit_max_size_mb`, `audit_max_backups` to `settings.Settings` with sensible defaults.

#### M-02: `getConfigletDir` searches hardcoded paths
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_baseline.go`
- **Lines:** 31-45
- **Severity:** LOW
- **Description:** `getConfigletDir()` searches `specDir + "/../configlets"`, `./configlets"`, and `"/etc/newtron/configlets"` in order. The fallback path `/etc/newtron/configlets` is a hardcoded system path. The `./configlets` relative path is fragile (depends on CWD).
- **Remediation:** Allow configlet dir to be set via `settings.Settings` or a `--configlets` flag, with the current search as a fallback.

#### M-03: `time.Sleep(15 * time.Second)` in provision command
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_provision.go`
- **Line:** 121
- **Severity:** MEDIUM
- **Description:** After restarting the BGP container, the provisioner sleeps for a hardcoded 15 seconds to wait for FRR + frrcfgd to render initial config. This is a fragile heuristic that may be too short on slow devices or wastefully long on fast ones.
- **Remediation:** Replace the fixed sleep with a polling loop that checks for BGP container readiness (e.g., waiting for `vtysh -c "show version"` to succeed or for frrcfgd to write expected config).

#### M-04: ACL rule priority default of 9999
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_acl.go`
- **Line:** 377
- **Severity:** LOW
- **Description:** `--priority` flag for `acl add-rule` defaults to 9999. This is a high priority value that could unexpectedly override most other rules. A lower default (e.g., 1000) or requiring the flag explicitly would be safer.
- **Remediation:** Either make `--priority` required (no default) or document that 9999 means "highest priority evaluated first" in SONiC's inverted priority model.

#### M-05: LAG defaults: `--fast-rate true`, `--mtu 9100`, `--min-links 1`
- **File:** `/home/aldrin/src/newtron/cmd/newtron/cmd_lag.go`
- **Lines:** 393-396
- **Severity:** LOW
- **Description:** LAG create defaults are hardcoded. The MTU 9100 is a SONiC-specific jumbo frame default, which is reasonable. `--fast-rate true` as default is aggressive (1-second LACP timeout). These are reasonable operational defaults but should be documented.
- **Remediation:** Document these defaults in the `--help` text. No code change needed unless configurability through settings is desired.

---

## Prioritized Remediation Plan

### Phase 1 -- Quick Wins (Low effort, high value)

1. **DC-01, DC-02**: Remove or wire dead variables `lagMode`, `vlanName` (5 min each)
2. **DC-05**: Remove `_ = vrfName` dead assignment (1 min)
3. **S-03**: Use `requireDevice` in `cmd_show.go` (2 min)
4. **N-01**: Move `defaultStr` to main.go (2 min)
5. **I-05**: Create proper Delete permission constants (15 min)

### Phase 2 -- Consistency (Medium effort)

6. **E-04 / I-03**: Add dry-run checks to all spec authoring commands (30 min)
7. **D-03**: Extract `formatAdminStatus`/`formatOperStatus` helpers (20 min)
8. **D-04**: Extract `parseVLANID` helper (10 min)
9. **I-01**: Standardize changeset rendering to one method (10 min)
10. **I-02**: Add `--json` support to vrf, bgp, evpn read commands (1 hr)

### Phase 3 -- Structural (High effort, high value)

11. **D-01 / D-02 / S-02**: Delete interactive.go (or refactor to share code) (2 hr)
12. **D-05**: Unify `showDevice` implementations (15 min)
13. **DC-03**: Remove or implement `networkName` (30 min)
14. **DC-04**: Eliminate `loader` global if possible (15 min)
15. **M-03**: Replace `time.Sleep(15s)` with polling in provision (1 hr)

### Phase 4 -- Architecture (High effort, long-term)

16. **S-01**: Refactor globals into an `App` struct (4+ hr, touches all 20 files)
17. **M-01**: Make audit config configurable via settings (30 min)
