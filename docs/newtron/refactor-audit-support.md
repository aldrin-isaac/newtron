# Refactor Audit: Support Packages

Audit of `pkg/auth/`, `pkg/settings/`, `pkg/audit/`, `pkg/util/`, `pkg/cli/`, `pkg/version/`.

Date: 2026-02-13

---

## 1. pkg/auth/

**Files**: `permission.go`, `checker.go`, `auth_test.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| A-1 | `checker.go` | 87-106, 108-123 | Code duplication | MEDIUM | `checkServicePermission` and `checkGlobalPermission` share identical logic: check "all" group first, then check specific permission string. The only difference is the source map (`svc.Permissions` vs `c.network.Permissions`). | Extract a shared `checkPermissionMap(username, permission, permMap)` method and call it from both. |
| A-2 | `permission.go` | 51-53 | Dead code | LOW | `PermPortCreate`, `PermPortDelete`, `PermBGPConfigure` (v3 permissions) are only referenced in `permission.go` itself and documentation/CLI files that list them. They are never checked in any `auth.Check()` call path. Same for `PermCompositeDeliver`, `PermTopologyProvision` (v4). Whether they are truly dead depends on whether CLI commands use `RequirePermission` -- currently `RequirePermission` has zero call sites outside tests and the auth package itself. | Confirm which permissions are actually enforced. If `RequirePermission` is not called from any CLI command, the entire permission enforcement layer is currently inert. Either wire it up or document that it is aspirational. |
| A-3 | `checker.go` | 210-215 | Dead code | HIGH | `RequirePermission` is defined as a convenience helper but has **zero call sites** outside `auth_test.go`. No CLI command invokes it. This means the entire auth system is tested but never enforced at runtime. | Wire `RequirePermission` into CLI write commands, or document that auth enforcement is deferred. |
| A-4 | `permission.go` | 89-180 | Dead code | MEDIUM | `StandardCategories` is only referenced in `auth_test.go` (existence test). No CLI command uses it for display or help text. | Use it in a `newtron auth list-permissions` command or remove it. |
| A-5 | `checker.go` | 150-169 | Dead code | LOW | `ListPermissions` / `ListPermissionsForUser` have no call sites outside tests. | Same as A-3 -- wire up or remove. |
| A-6 | `checker.go` | 172-183 | Dead code | LOW | `GetUserGroups` has no call sites outside tests. | Same as above. |
| A-7 | `permission.go` | 82-86 | Structural issues | LOW | `PermissionCategory` defines `Description` but it is never consumed programmatically. The categories could be auto-derived from permission string prefixes (e.g., `"service.apply"` -> category `"service"`). | Consider deriving categories from permission prefixes if you keep them. |
| A-8 | `permission.go` | 120-129 | Inconsistent patterns | LOW | The `StandardCategories` slice does not include the newer v5 permissions (`PermQoSCreate`, `PermQoSDelete`) in the `qos` category. The `bgp` category does not include `PermBGPConfigure`. | Keep `StandardCategories` in sync with all declared permissions, or auto-generate it. |
| A-9 | `checker.go` | 78-85, 125-142 | Configuration / magic numbers | LOW | Superuser and group membership checks use linear scans over slices. Acceptable for small user bases but O(n*m) for `userInGroups`. | No action needed now; note for future if user/group counts grow large. |
| A-10 | `auth_test.go` | 494-505 | Code duplication | LOW | Custom `contains` and `containsHelper` functions in test file replicate `strings.Contains`. | Replace with `strings.Contains`. |
| A-11 | `auth_test.go` | 11-16 | Missing tests | LOW | `TestPermission_IsReadOnly` does not include the v5 read-only permissions (`PermVRFView`, `PermFilterView`) that were added to `IsReadOnly()` at line 224. | Add the newer view permissions to the test's `readOnlyPerms` slice. |
| A-12 | `permission.go` | 13 | Naming inconsistency | LOW | `PermInterfaceConfig` uses "config" while all other write permissions use "create"/"modify"/"delete". The permission string is `"interface.configure"` but the constant name uses `Config`. | Rename to `PermInterfaceConfigure` for consistency with its string value, or to `PermInterfaceCreate`/`PermInterfaceModify` to match the CRUD pattern. |

---

## 2. pkg/settings/

**Files**: `settings.go`, `settings_test.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| S-1 | `settings.go` | 81-88 | Dead code | LOW | `SetNetwork` and `SetSpecDir` are trivial setters that add no value over direct field assignment (`s.DefaultNetwork = "..."` is equally clear). Same for `SetDefaultSuite` and `SetTopologiesDir`. These exist for symmetry but none are called from production code -- only tests use them. | Remove the setters and assign fields directly, or keep them if you plan to add validation (e.g., path normalization). |
| S-2 | `settings.go` | 95 | Configuration / magic numbers | MEDIUM | `GetSpecDir()` has a hardcoded fallback `/etc/newtron`. This path should be a named constant. | Extract `const DefaultSpecDir = "/etc/newtron"` at package level. |
| S-3 | `settings.go` | 29 | Missing error handling | LOW | `DefaultSettingsPath` silently falls back to a relative path `"newtron_settings.json"` when `os.UserHomeDir()` fails. This relative path is almost certainly wrong in production (depends on CWD). | Return an error instead of falling back to a relative path, or use a well-known fallback like `/tmp/newtron_settings.json`. |
| S-4 | `settings.go` | 68, 77 | Configuration / magic numbers | LOW | File permissions `0755` (directory) and `0644` (file) are hardcoded. These are reasonable defaults but could be constants for clarity. | Extract named constants, or leave as-is (standard Unix conventions). |
| S-5 | `settings.go` | -- | Missing tests | LOW | No test validates thread safety. `Settings` has no mutex, so concurrent `Save`/`Load` calls could race. For a CLI tool this is acceptable (single-threaded), but worth documenting. | Add a comment that `Settings` is not goroutine-safe. |

---

## 3. pkg/audit/

**Files**: `event.go`, `logger.go`, `audit_test.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| AU-1 | `event.go` | 32-41 | Dead code | HIGH | `EventType` constants (`EventTypeConnect`, `EventTypeDisconnect`, `EventTypeLock`, `EventTypeUnlock`, `EventTypePreview`, `EventTypeExecute`, `EventTypeRollback`) are defined but **never used** anywhere in the codebase outside `audit_test.go`. The `Event` struct has no `EventType` field -- events use a free-form `Operation string` instead. | Either add an `EventType` field to `Event` and use these constants, or remove them entirely. |
| AU-2 | `event.go` | 44-50 | Dead code | HIGH | `Severity` type and constants (`SeverityInfo`, `SeverityWarning`, `SeverityError`) are defined but **never used** anywhere. The `Event` struct has no `Severity` field. | Same as AU-1: add the field or remove the type. |
| AU-3 | `event.go` | 124-126 | Structural issues | HIGH | `generateID()` uses `time.Now().UnixNano()` as the event ID. This is not unique under concurrent logging (two events in the same nanosecond get the same ID) and is not a standard format (not UUID, not ULID). | Use `crypto/rand` or a UUID library to generate unique IDs. Alternatively, use a monotonic counter protected by the logger's mutex. |
| AU-4 | `logger.go` | 93-94 | Missing error handling | MEDIUM | In `Query`, malformed JSON lines are silently skipped (`continue`). This swallows corruption without any logging or metric. A corrupted audit log could silently lose events. | Log a warning (at minimum) when a line cannot be parsed, including the line number and content. |
| AU-5 | `logger.go` | 103-108 | Structural issues | MEDIUM | Offset/limit logic has a bug: if `filter.Offset >= len(events)`, the condition `filter.Offset < len(events)` is false, so no slicing happens and ALL events are returned. The test at line 607-618 acknowledges this with a comment. | Fix: when `offset >= len(events)`, return empty slice. |
| AU-6 | `logger.go` | 30-33 | Dead code | MEDIUM | `RotationConfig.MaxAge` is defined but never read. The `cleanupOldFiles` method only uses `MaxBackups`; it never checks file age against `MaxAge`. | Implement age-based cleanup or remove the field. |
| AU-7 | `logger.go` | 186-227 | Structural issues | LOW | `cleanupOldFiles` uses a hand-rolled bubble sort (O(n^2)). For a small number of backup files this is fine, but `sort.Slice` would be cleaner and idiomatic. | Replace with `sort.Slice`. |
| AU-8 | `logger.go` | 224 | Missing error handling | LOW | `os.Remove(files[i].path)` in `cleanupOldFiles` ignores the error. If removal fails (permissions, file locked), old files accumulate silently. | Log a warning on removal failure. |
| AU-9 | `logger.go` | 229-230 | Structural issues | MEDIUM | `DefaultLogger` is a package-level mutable global. This is a concurrency hazard: `SetDefaultLogger` and `Log`/`Query` can race. The `FileLogger` has its own mutex, but the global pointer itself is unprotected. | Protect `DefaultLogger` with a `sync.RWMutex`, or use `atomic.Value`. |
| AU-10 | `logger.go` | 76-111 | Structural issues | MEDIUM | `Query` holds the write mutex for the entire duration of reading the file. This blocks logging while a query is in progress. For a CLI tool with infrequent queries this is acceptable, but a read/write lock (`sync.RWMutex`) would be more appropriate. | Change `mu sync.Mutex` to `mu sync.RWMutex`; use `RLock` in `Query`, `Lock` in `Log`. Note: `Log` also needs to hold the lock for rotation checks, so this needs care. |
| AU-11 | `event.go` | 20-21 | Inconsistent patterns | LOW | `Event.Changes` uses `network.Change` type, creating a dependency from `pkg/audit` -> `pkg/network`. This couples audit logging to the network layer. If other tools (e.g., newtlab) want to emit audit events, they must import `pkg/network`. | Consider making `Changes` a `[]map[string]interface{}` or defining a standalone `audit.Change` type. |
| AU-12 | `logger.go` | 155-183 | Missing error handling | LOW | In `rotate()`, if `os.Rename` fails (line 165), the old file was already closed (line 157) and the new file is not yet opened. The logger is left in a broken state with `l.file` pointing to a closed file. | On rename failure, attempt to reopen the original file before returning the error. |

---

## 4. pkg/util/

**Files**: `errors.go`, `ip.go`, `range.go`, `derive.go`, `log.go`, `errors_test.go`, `ip_test.go`, `range_test.go`, `derive_test.go`, `log_test.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| U-1 | `derive.go` | 77-79, 82-84 | Dead code | MEDIUM | `DeriveRouterID` and `DeriveVTEPSourceIP` are identity functions (return their input unchanged). They are only called in their own test file. No production code references them. | Remove. If the intent is documentation/naming, a comment on the call site suffices. |
| U-2 | `derive.go` | 87-89 | Dead code | LOW | `DeriveRouteDistinguisher` is a trivial wrapper around `FormatRouteDistinguisher`. Only called in tests. | Remove or inline. |
| U-3 | `ip.go` | 208-215, derive.go 87-89 | Code duplication | LOW | `FormatRouteDistinguisher` (ip.go:208) and `DeriveRouteDistinguisher` (derive.go:87) do the same thing. `FormatRouteDistinguisher` and `FormatRouteTarget` (ip.go:213) are only called from `derive.go` and tests -- never from production code. | Remove the derive.go wrappers. Keep `FormatRouteDistinguisher`/`FormatRouteTarget` in ip.go if needed by future code, otherwise remove. |
| U-4 | `ip.go` | 72-92 | Dead code | LOW | `ComputeBroadcastAddr` has no call sites outside tests. `ComputeNetworkAddr` is only called from `derive.go:DeriveFromInterface` (which itself is only called from one CLI command). | Keep if planned for use; otherwise mark with a comment. |
| U-5 | `ip.go` | 113-125 | Dead code | MEDIUM | `IsValidMACAddress` and `NormalizeMACAddress` have zero call sites outside tests. | Remove or document as future-use. |
| U-6 | `ip.go` | 128-138 | Dead code | MEDIUM | `IPInRange` has zero call sites outside tests. | Same. |
| U-7 | `ip.go` | 148-154 | Dead code | LOW | `ValidateVNI` has zero call sites outside tests. | Same. |
| U-8 | `ip.go` | 141-146 | Dead code | LOW | `ValidateVLANID` is called only from `range.go:ExpandVLANRange`, which itself has zero production call sites. | Same. |
| U-9 | `ip.go` | 173-205 | Dead code | LOW | `ParsePortRange` has zero call sites outside tests. | Same. |
| U-10 | `range.go` | 72-96 | Dead code | MEDIUM | `ExpandSlotPortRange` has zero call sites outside tests. | Remove or document as future-use. |
| U-11 | `range.go` | 149-180 | Dead code | LOW | `ExpandInterfaceRange` (the range.go version) has zero call sites outside tests. Note: `derive.go` also has `ExpandInterfaceName` which is different (short-to-long name conversion). | Remove or document. |
| U-12 | `range.go` | 183-198 | Dead code | LOW | `ExpandVLANRange` has zero call sites outside tests. | Same. |
| U-13 | `range.go` | 98-127 | Dead code | LOW | `CompactRange` has zero call sites outside tests. | Same. |
| U-14 | `derive.go` | 47-54 | Configuration / magic numbers | LOW | `SanitizeForName` compiles a regexp (`regexp.MustCompile`) on every call. This allocates a new regexp each time. | Hoist the compiled regexp to a package-level `var`. |
| U-15 | `derive.go` | 107 | Configuration / magic numbers | LOW | `ParseInterfaceName` also compiles a regexp on every call. | Same fix: hoist to package-level `var`. |
| U-16 | `derive.go` | 157-179 | Inconsistent patterns | LOW | `NormalizeInterfaceName` iterates `shortToLong` map in undefined order. If an input matches multiple prefixes (e.g., "vl100" matches both "vl" and "vlan"), the result depends on map iteration order. Currently "vl100" would match "vl" -> "Vlan100" correctly, but if both keys happen to be checked, either mapping produces the same result ("Vlan"), so it works by coincidence. | Sort prefix matches by length (longest first) to ensure deterministic behavior, or document the expected input format. |
| U-17 | `derive.go` | 155-159 | Code duplication | LOW | `ExpandInterfaceName` is a one-line alias for `NormalizeInterfaceName`. Neither `ShortenInterfaceName` nor `ExpandInterfaceName` are called from outside `pkg/util` (only from `derive.go` internal helpers and tests). | Remove `ExpandInterfaceName` or inline it. |
| U-18 | `derive.go` | 182-189, 192-200 | Dead code | LOW | `CoalesceInt` is only called from `pkg/network` and `pkg/spec` via `util.MergeMaps`/`util.MergeStringSlices`. Actually: `CoalesceInt` has zero external call sites. | Remove if unused. |
| U-19 | `log.go` | 11 | Structural issues | MEDIUM | `Logger` is a package-level mutable global (`var Logger = logrus.New()`). All packages that import `util` share the same logger state. This is standard for logrus but makes it impossible to test logging in isolation without global state manipulation (as the tests demonstrate with `saveLoggerState`/`restoreLoggerState`). | Consider accepting a `*logrus.Logger` parameter in functions that log, or use logrus's `WithField` pattern consistently. This is a broader architectural decision. |
| U-20 | `log.go` | 65-112 | Code duplication | MEDIUM | Ten functions (`Debug`, `Debugf`, `Info`, `Infof`, `Warn`, `Warnf`, `Error`, `Errorf`, `Fatal`, `Fatalf`) are trivial one-line wrappers around `Logger.X()`. They add no value over callers using `util.Logger.Info()` directly, and they prevent callers from using structured logging (fields). | Consider whether these wrappers are needed. If the goal is to hide the logrus dependency, they succeed but at the cost of limiting structured logging. If callers already use `util.WithDevice(...).Info()`, the plain wrappers are redundant. |
| U-21 | `errors.go` | -- | Structural issues | LOW | `pkg/util` contains error types, IP utilities, range utilities, name derivation, logging, and generic helpers (CoalesceInt, MergeMaps). This is a "grab bag" package. While each file is focused, the package itself has low cohesion. | Consider splitting into `pkg/util/netaddr` (IP/MAC), `pkg/util/naming` (interface names, sanitization), and keeping `pkg/util` for truly generic helpers (errors, merge, coalesce). This is a large refactor and only worth doing if the package grows further. |
| U-22 | `ip.go` | 157-162 | Missing error handling | LOW | `ValidateASN` uses `int` type but compares against `4294967295` (max uint32). On 64-bit systems `int` is 64-bit so this works, but the comparison `asn > 4294967295` would always be false on a 32-bit system where `int` is 32 bits. | Use `int64` for ASN validation or document the 64-bit requirement. |
| U-23 | `derive.go` | 36-41 | Inconsistent patterns | LOW | `DeriveFromInterface` generates both `VRFName` and `ACLNameBase` from the same formula (`serviceName + "-" + shortIntf`). The `ACLNameBase` field in `DerivedValues` is misleading because `DeriveACLName` (line 72) uses a different pattern (`serviceName + "-" + direction`, no interface name). The two derivation strategies are inconsistent. | Either remove `ACLNameBase` from `DerivedValues` (since `DeriveACLName` is the canonical ACL name generator), or align the naming strategies. |
| U-24 | `ip.go` | 12-19 | Inconsistent patterns | LOW | `ParseIPWithMask` returns `(net.IP, int, error)` while `SplitIPMask` returns `(string, int)` with no error. Both parse CIDR notation but with different return types and error handling. | Consolidate: `SplitIPMask` could call `ParseIPWithMask` internally and convert, or document that `SplitIPMask` is a fast-path that does not validate. |

---

## 5. pkg/cli/

**Files**: `format.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| C-1 | `format.go` | 1-22 | Missing tests | MEDIUM | No test file exists for `pkg/cli`. Five color functions and `DotPad` have zero test coverage. `DotPad` has edge-case behavior: when `len(name) >= width-1`, it returns `name` without padding, but when `len(name) == width-2`, it returns a single dot, which may look odd. | Add `format_test.go` with tests for each function, especially `DotPad` edge cases. |
| C-2 | `format.go` | 8-12 | Structural issues | LOW | ANSI escape codes are hardcoded inline. No mechanism to disable colors (e.g., when output is piped or `NO_COLOR` env var is set). | Add a `ColorEnabled` flag or check `os.Getenv("NO_COLOR")` / `isatty` before emitting ANSI codes. |
| C-3 | `format.go` | 8-12 | Naming inconsistency | LOW | Function names `Green`, `Yellow`, `Red`, `Bold`, `Dim` are unexported-style names that happen to be exported. Go convention is fine here (short, descriptive), but they lack doc comments. | Add one-line doc comments. |
| C-4 | `format.go` | 16-22 | Missing error handling | LOW | `DotPad` does not handle negative `width` values -- it would compute a negative `dots` count, but `strings.Repeat` with a negative count returns empty string, so behavior is correct but surprising. | Add a guard: `if width <= 0 { return name }`. |

---

## 6. pkg/version/

**Files**: `version.go`

### Findings

| # | File | Line(s) | Category | Severity | Description | Remediation |
|---|------|---------|----------|----------|-------------|-------------|
| V-1 | `version.go` | 1-10 | Missing tests | LOW | No test file. The package is trivial (two ldflags variables), but a test that verifies the default values ("dev", "unknown") exist would provide coverage and catch accidental modifications. | Add a minimal `version_test.go`. |
| V-2 | `version.go` | 7-9 | Structural issues | LOW | Only `Version` and `GitCommit` are defined. Common additions include `BuildDate` and `GoVersion` (from `runtime.Version()`), which help with debugging production issues. | Consider adding `BuildDate` (ldflags) and a `Info() string` function that formats all fields. |

---

## Summary by Severity

### HIGH (5 findings)
- **A-3**: `RequirePermission` is never called from production code -- the auth system is defined but not enforced.
- **AU-1**: `EventType` constants defined but no corresponding field in `Event` struct.
- **AU-2**: `Severity` type defined but no corresponding field in `Event` struct.
- **AU-3**: Event IDs use `time.Now().UnixNano()` -- not unique under concurrency.

### MEDIUM (12 findings)
- **A-1**: Duplicated permission-check logic in `checkServicePermission`/`checkGlobalPermission`.
- **A-2**: Many permission constants are defined but never enforced.
- **A-4**: `StandardCategories` only used in tests.
- **S-2**: Hardcoded `/etc/newtron` fallback path.
- **AU-4**: Malformed JSON lines silently skipped during query.
- **AU-5**: Offset/limit bug: offset >= len(events) returns all events instead of empty.
- **AU-6**: `RotationConfig.MaxAge` field defined but never implemented.
- **AU-9**: Global `DefaultLogger` pointer is not concurrency-safe.
- **AU-10**: `Query` holds write lock unnecessarily.
- **U-1**: `DeriveRouterID` and `DeriveVTEPSourceIP` are unused identity functions.
- **U-5, U-6, U-10**: Several IP/range utility functions have zero production call sites.
- **U-19**: `Logger` as a package-level global limits testability.
- **C-1**: No tests for `pkg/cli`.

### LOW (remaining findings)
- Various dead code, naming inconsistencies, and minor structural issues as detailed above.

---

## Cross-Cutting Themes

### 1. Dead Code Accumulation
The most pervasive issue across all six packages is dead code. Many types, constants, and functions were designed ahead of their use and never wired into production paths. The most significant example is the entire `auth` enforcement layer (`RequirePermission` has zero CLI call sites). In `pkg/util`, over a dozen exported functions have no production callers.

**Recommendation**: Run `go vet` with unused analysis, or use `staticcheck` / `deadcode` tools to identify and prune unused exports. For functions that are aspirational (planned for future use), add `// TODO(future): wire into ...` comments so their intent is clear.

### 2. Global Mutable State
Both `pkg/util/log.go` (global Logger) and `pkg/audit/logger.go` (global DefaultLogger) use package-level mutable globals. This is a common Go pattern but complicates testing and makes concurrent access unsafe for `DefaultLogger`.

**Recommendation**: Protect `DefaultLogger` with `sync.RWMutex`. For the logger, consider dependency injection where feasible.

### 3. Missing Test Coverage
`pkg/cli/format.go` and `pkg/version/version.go` have no test files at all. While both are small, they set a precedent for untested code.

### 4. pkg/util Cohesion
`pkg/util` contains 5 source files spanning error types, IP math, range expansion, interface naming, logging, and generic helpers. This breadth is typical of utility packages but makes it hard to reason about the package's purpose. The naming functions in `derive.go` are arguably domain logic (SONiC interface naming conventions) rather than generic utilities.
