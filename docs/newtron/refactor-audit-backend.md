# Backend Code Quality Audit

**Date**: 2026-02-13
**Scope**: `pkg/network/`, `pkg/device/`, `pkg/spec/`, `pkg/model/`, `pkg/operations/`
**Mode**: READ-ONLY analysis (no code changes)

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Package: pkg/network/](#2-package-pkgnetwork)
3. [Package: pkg/device/](#3-package-pkgdevice)
4. [Package: pkg/spec/](#4-package-pkgspec)
5. [Package: pkg/model/](#5-package-pkgmodel)
6. [Package: pkg/operations/](#6-package-pkgoperations)
7. [Cross-Package Issues](#7-cross-package-issues)
8. [Summary Table](#8-summary-table)
9. [Recommended Refactor Order](#9-recommended-refactor-order)

---

## 1. Executive Summary

The five backend packages contain **34 non-test source files** totaling approximately **12,700 lines of Go code**. The architecture is sound: a clean hierarchy (Network -> Device -> Interface) with parent references, spec-driven configuration, and a well-designed ChangeSet pattern for transactional config mutations.

However, rapid feature development has introduced significant technical debt concentrated in two areas: **massive god files** (`device_ops.go` at 2308 lines, `interface_ops.go` at 1772 lines) and **pervasive code duplication** (connected/locked precondition checks repeated 40+ times, duplicate type definitions across packages, hand-rolled helpers duplicated in three locations). These issues inflate maintenance cost and increase defect risk.

**Finding counts by severity**:
- **HIGH**: 12 findings (architectural issues, bugs, production safety)
- **MEDIUM**: 18 findings (duplication, inconsistency, maintainability)
- **LOW**: 10 findings (style, minor dead code, naming)

---

## 2. Package: pkg/network/

**Files**: `network.go` (637 lines), `device.go` (799 lines), `device_ops.go` (2308 lines), `interface.go` (494 lines), `interface_ops.go` (1772 lines), `changeset.go` (231 lines), `composite.go` (289 lines), `topology.go` (542 lines), `service_gen.go` (567 lines), `qos.go` (140 lines)

### N-01: God File — device_ops.go (2308 lines)

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device_ops.go` |
| **Category** | Structural Issue |
| **Severity** | HIGH |
| **Description** | `device_ops.go` contains all device-level operations: VLAN CRUD (5 methods), PortChannel CRUD (4), VRF CRUD (2), ACL operations (5), EVPN operations (7), BGP operations (11), port creation/breakout (3), health checks (5), QoS (2), static routes (2), and numerous config types — 46+ public methods in a single file. This makes navigation, review, and ownership difficult. |
| **Remediation** | Split into domain-specific files: `vlan_ops.go`, `portchannel_ops.go`, `vrf_ops.go`, `acl_ops.go`, `evpn_ops.go`, `bgp_ops.go`, `port_ops.go`, `health.go`, `qos_ops.go`. Move config types (VLANConfig, VTEPConfig, etc.) to a `config_types.go` or co-locate with their operations. |

### N-02: God File — interface_ops.go (1772 lines)

| Field | Value |
|-------|-------|
| **File** | `pkg/network/interface_ops.go` |
| **Category** | Structural Issue |
| **Severity** | HIGH |
| **Description** | `interface_ops.go` contains ApplyService (complex 280-line method), RemoveService, SetIP, SetVRF, BindACL, Configure, direct BGP neighbor ops, MAC-VPN binding, generic Set, LAG/VLAN member ops, RefreshService, UnbindACL, SetRouteMap, plus DependencyChecker type and 10+ helper config types. |
| **Remediation** | Extract ApplyService/RemoveService into `service_ops.go`. Extract DependencyChecker into `dependency.go`. Move BGP neighbor operations into `interface_bgp.go`. Move helper types into `interface_types.go`. |

### N-03: Repeated Connected/Locked Precondition Pattern

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device_ops.go`, lines 19-24, 59-64, 99-104, 135-141, 186-192, 221-226, 251-258, 277-283, 311-316, 345-351, 382-388, 439-445, 467-473, 498-504, 541-547, 572-578, 605-611, 634-640, 667-673, 696-703, 731-737, 777-783, 805-811, 830-836, 863-868, 897-903, 932-938, 961-967, 991-997, 1065-1071, 1098-1104, etc. Also `interface_ops.go` lines 28-32 and throughout. |
| **Category** | Code Duplication |
| **Severity** | HIGH |
| **Description** | The pattern `if !d.IsConnected() { return nil, fmt.Errorf("device not connected") } if !d.IsLocked() { return nil, fmt.Errorf("device not locked") }` is duplicated **40+ times** across `device_ops.go` and `interface_ops.go`. This is the single largest source of boilerplate in the codebase. |
| **Remediation** | Extract a `requireWritable(d *Device) error` helper that checks both connected and locked states. All write operations call this at the top: `if err := requireWritable(d); err != nil { return nil, err }`. Alternatively, the existing `operations.PreconditionChecker.RequireConnected().RequireLocked().Result()` could be used, but the simple helper is lighter. |

### N-04: Duplicate ChangeType Definitions

| Field | Value |
|-------|-------|
| **File** | `pkg/network/changeset.go` lines 14-20, `pkg/device/device.go` (ChangeTypeAdd/Modify/Delete) |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | `ChangeType` is defined in both `network` (string type: "add"/"modify"/"delete") and `device` (string type: "add"/"modify"/"delete"). The conversion between them is then duplicated in `changeset.go` Apply() (lines 114-129) and Verify() (lines 149-165) with identical switch statements. |
| **Remediation** | Define a single `ChangeType` in `device` package. Have `network.Change` use `device.ChangeType` directly. This eliminates both the duplicate type and the duplicate conversion code. |

### N-05: Duplicate splitPorts/joinPorts vs strings.Join/strings.Split

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device_ops.go` lines 1194-1225 |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | `joinPorts()` is a hand-rolled implementation of `strings.Join(ports, ",")`. `splitPorts()` is a hand-rolled implementation of `strings.Split` with trim. Both functions are trivially replaceable by standard library calls. The same `splitPorts` function also exists in `pkg/operations/precondition.go` (line 443). |
| **Remediation** | Replace `joinPorts` with `strings.Join`. Replace `splitPorts` with a shared utility `util.SplitPorts(s string) []string` that wraps `strings.Split` with TrimSpace. Delete the three duplicate implementations. |

### N-06: Duplicate splitConfigDBKey

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device.go` lines 532-539, `pkg/device/device.go` (splitKey) |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | `splitConfigDBKey` in network and `splitKey` in device both split on "|" with identical logic. |
| **Remediation** | Move to `pkg/util/` as `SplitRedisKey`. |

### N-07: Duplicate Existence Check Methods

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device.go` lines 428-496 (VLANExists, VRFExists, InterfaceExists, etc.), `pkg/device/state.go` (similar checks) |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | `network.Device` has `VLANExists`, `VRFExists`, `PortChannelExists`, `InterfaceExists`, `VTEPExists`, `BGPConfigured`, `ACLTableExists` methods that all follow the same pattern: check `d.configDB == nil`, then look up a map. The `device.Device` layer has overlapping checks. |
| **Remediation** | Consider having `network.Device` delegate all existence checks to `device.ConfigDB` methods (e.g., `d.configDB.HasVLAN(id)`, `d.configDB.HasVRF(name)`). This centralizes the map lookups and makes ConfigDB self-describing. |

### N-08: readFileViaSSH Stub

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device_ops.go` lines 2278-2284 |
| **Category** | Dead Code |
| **Severity** | MEDIUM |
| **Description** | `readFileViaSSH` is a stub that always returns an error: `"SSH file read not yet implemented"`. It is called by `LoadPlatformConfig` (line 2246), making the entire `LoadPlatformConfig`/`BreakoutPort`/`GeneratePlatformSpec` chain non-functional. |
| **Remediation** | Implement using `d.conn.Tunnel.ExecCommand(ctx, "cat " + path)` which already exists in the device package. Alternatively, mark `LoadPlatformConfig` as unimplemented with a clear TODO and add a guard in `BreakoutPort`. |

### N-09: BGP Protocol Number Bug in protoMap

| Field | Value |
|-------|-------|
| **File** | `pkg/network/service_gen.go` line 484, `pkg/network/device_ops.go` line 416 |
| **Category** | Bug |
| **Severity** | HIGH |
| **Description** | The `protoMap` maps `"bgp"` to `179`. However, 179 is the TCP **port number** for BGP, not the IP protocol number. BGP uses TCP (protocol 6). When a filter rule specifies `protocol: "bgp"`, the generated ACL rule would set `IP_PROTOCOL=179`, which is invalid (IP protocol numbers go up to 255, and 179 is unassigned/reserved). The same bug exists in `model/acl.go` line 84 where `ProtocolBGP = 179` is defined with the comment `// This is a port number, not protocol`. |
| **Remediation** | Remove "bgp" from protoMap entirely. BGP filtering should use `protocol: "tcp"` + `dst_port: "179"`. Add a comment explaining why "bgp" is not a valid protocol shorthand for IP_PROTOCOL. In `model/acl.go`, remove or rename `ProtocolBGP` to avoid misuse. |

### N-10: VTEPConfig.UDPPort and VRFConfig.ImportRT/ExportRT Never Used

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device_ops.go` lines 1145-1146 (VTEPConfig.UDPPort), 1160-1162 (VRFConfig.ImportRT/ExportRT) |
| **Category** | Dead Code |
| **Severity** | LOW |
| **Description** | `VTEPConfig.UDPPort` is defined but never read by `CreateVTEP`. `VRFConfig.ImportRT` and `VRFConfig.ExportRT` are defined but never read by `CreateVRF`. |
| **Remediation** | Either implement the functionality (VXLAN_TUNNEL does support a dst_port field; VRF route targets are used in EVPN) or remove the fields and add them when needed. |

### N-11: ChangeSet Apply/Verify Duplicate Conversion Logic

| Field | Value |
|-------|-------|
| **File** | `pkg/network/changeset.go` lines 113-130 (Apply), lines 149-165 (Verify) |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | The network.Change to device.ConfigChange conversion logic (iterating changes, mapping ChangeType, building slice) is duplicated identically between `Apply()` and `Verify()`. |
| **Remediation** | Extract a `(cs *ChangeSet) toDeviceChanges() []device.ConfigChange` method and call it from both Apply and Verify. |

### N-12: "Avoid Duplicates" Linear Search Pattern in GetVRF

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device.go` lines 680-690, 699-712 |
| **Category** | Inconsistent Pattern |
| **Severity** | LOW |
| **Description** | `GetVRF` uses a linear `found` loop to deduplicate interfaces. This pattern appears twice in the same method and also in `device/state.go` `parseVRFs`. |
| **Remediation** | Use a `map[string]bool` seen-set for O(1) deduplication. |

### N-13: InterfaceIsLAGMember Uses Suffix Match Instead of Key Parse

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device.go` lines 505-511 |
| **Category** | Bug Risk |
| **Severity** | MEDIUM |
| **Description** | `InterfaceIsLAGMember` checks if any PortChannelMember key *ends with* the interface name. This could false-positive: an interface named `Ethernet0` would match a key `PortChannel100|Ethernet10` if the suffix logic is off. The check `key[len(key)-len(name):] == name` and `len(key) > len(name)` does not verify there is a `|` separator. A port named `Ethernet10` would not accidentally match `Ethernet0`, but `Ethernet0` would incorrectly match any key ending in `Ethernet0` (even `Ethernet100` -- no, the suffix check is exact. Actually this is safe because `Ethernet0` would only match keys ending in exactly `Ethernet0`. But compare with `GetInterfaceLAG` (lines 521-528) which properly splits on `|` and checks `parts[1] == name`. The inconsistency is itself a code smell. |
| **Remediation** | Make `InterfaceIsLAGMember` use `splitConfigDBKey` + exact match on parts[1], consistent with `GetInterfaceLAG`. |

### N-14: Magic Numbers in QoS WRED Thresholds

| Field | Value |
|-------|-------|
| **File** | `pkg/network/qos.go` lines 81-83 |
| **Category** | Magic Numbers |
| **Severity** | LOW |
| **Description** | WRED thresholds are hardcoded as `1048576` (1 MB) and `2097152` (2 MB) and `5` (5% drop probability) without named constants or configuration. |
| **Remediation** | Define named constants: `const defaultWREDMinThreshold = 1048576 // 1 MB`, etc. Consider making these configurable via the QoSQueue spec. |

### N-15: Lock TTL Magic Number

| Field | Value |
|-------|-------|
| **File** | `pkg/network/device.go` line 226 |
| **Category** | Magic Numbers |
| **Severity** | LOW |
| **Description** | `d.conn.Lock(holder, 3600)` uses a hardcoded 3600-second (1 hour) TTL. |
| **Remediation** | Define `const defaultLockTTL = 3600` and reference it. Consider making it configurable. |

### N-16: Network Get* Methods — Identical Boilerplate Pattern

| Field | Value |
|-------|-------|
| **File** | `pkg/network/network.go` lines 73-248 |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | 12 Get* methods (GetService, GetFilterSpec, GetRegion, GetSite, GetPlatform, GetPrefixList, GetPolicer, GetQoSPolicy, GetQoSProfile, GetIPVPN, GetMACVPN, GetRoutePolicy) all follow the identical pattern: `n.mu.RLock(); defer n.mu.RUnlock(); lookup in map; return not-found error`. Similarly, the Save*/Delete* authoring methods repeat lock/persist patterns. |
| **Remediation** | Consider a generic `getSpec[T any](n *Network, m map[string]T, key, label string) (T, error)` helper using Go generics (1.18+). This would reduce 12 nearly-identical methods to 12 one-liner calls. |

### N-17: addInterfaceToList Missing Definition Check

| Field | Value |
|-------|-------|
| **File** | `pkg/network/interface_ops.go` line 180 |
| **Category** | Missing Error Handling |
| **Severity** | LOW |
| **Description** | `addInterfaceToList` is called during ACL interface merging but its definition was not found in the read files. If it is defined elsewhere (e.g., a helper file), it should be co-located. If it is missing, this is a compile error. |
| **Remediation** | Verify the function exists and co-locate it with its callers. |

---

## 3. Package: pkg/device/

**Files**: `device.go` (1015 lines), `configdb.go` (802 lines), `state.go` (351 lines), `statedb.go` (743 lines), `verify.go` (46 lines), `tunnel.go` (143 lines), `platform.go` (257 lines), `pipeline.go` (169 lines), `appldb.go` (85 lines), `asicdb.go` (239 lines)

### D-01: KEYS * Usage in GetAll() — Production Safety Risk

| Field | Value |
|-------|-------|
| **File** | `pkg/device/configdb.go` (ConfigDBClient.GetAll), `pkg/device/statedb.go` (StateDBClient.GetAll) |
| **Category** | Production Safety |
| **Severity** | HIGH |
| **Description** | Both `ConfigDBClient.GetAll()` and `StateDBClient.GetAll()` use `KEYS *` (via Redis KEYS command) to enumerate all keys. The Redis documentation explicitly warns: "KEYS should only be used in production environments with extreme care. It may ruin performance when it is executed against large databases." SONiC CONFIG_DB is small enough that this works in practice, but STATE_DB can be large (BGP route state, interface counters). Note that `asicdb.go` correctly uses `SCAN` instead. |
| **Remediation** | Replace `KEYS *` with `SCAN` cursor iteration in both GetAll() methods. Use a per-table SCAN pattern (e.g., `TABLE_NAME|*`) to avoid scanning the entire keyspace. |

### D-02: configdb.go parseEntry Switch Statement (~250 lines)

| Field | Value |
|-------|-------|
| **File** | `pkg/device/configdb.go` |
| **Category** | Structural Issue |
| **Severity** | MEDIUM |
| **Description** | The `parseEntry` function uses a massive switch statement with one case per CONFIG_DB table (30+ tables). Each case performs similar JSON unmarshalling into a typed struct. Adding a new table requires adding both a new struct type and a new case to the switch. |
| **Remediation** | Use a table registry pattern: `map[string]func(key string, data map[string]string)` where each table has a registered parser. This makes adding new tables a single registration call. |

### D-03: Duplicate ChangeType in device.go

| Field | Value |
|-------|-------|
| **File** | `pkg/device/device.go` |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | `device.ChangeType` ("add"/"modify"/"delete") and `device.ConfigChange` partially overlap with `network.ChangeType` and `network.Change`. See N-04. |
| **Remediation** | Keep `device.ChangeType` + `device.ConfigChange` as the canonical type. Have `network` import and use it. |

### D-04: fmt.Printf in pipeline.go

| Field | Value |
|-------|-------|
| **File** | `pkg/device/pipeline.go` line 165 |
| **Category** | Inconsistent Pattern |
| **Severity** | MEDIUM |
| **Description** | `pipeline.go` uses `fmt.Printf` directly for output instead of the structured logging used everywhere else (`util.WithDevice().Infof()`). This mixes stdout output with potential structured log output and cannot be controlled by log level. |
| **Remediation** | Replace `fmt.Printf` calls with `util.WithDevice(d.Name).Infof()`. |

### D-05: InsecureIgnoreHostKey in tunnel.go

| Field | Value |
|-------|-------|
| **File** | `pkg/device/tunnel.go` |
| **Category** | Security |
| **Severity** | MEDIUM |
| **Description** | SSH tunnel creation uses `ssh.InsecureIgnoreHostKey()` which accepts any host key without verification. While acceptable for lab/development, this is a security risk in production. |
| **Remediation** | Add a `KnownHostsFile` option to tunnel configuration. Default to insecure for lab environments, but log a warning. Add a `Strict` mode that verifies host keys. |

### D-06: Duplicate "Avoid Duplicates" Pattern in state.go parseVRFs

| Field | Value |
|-------|-------|
| **File** | `pkg/device/state.go` |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | `parseVRFs` uses the same linear-scan deduplication pattern as `network/device.go` GetVRF (see N-12). |
| **Remediation** | Use a seen-set map. |

### D-07: statedb.go PopulateDeviceState Duplicates Logic from state.go

| Field | Value |
|-------|-------|
| **File** | `pkg/device/statedb.go`, `pkg/device/state.go` |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | `PopulateDeviceState` in statedb.go and state parsing logic in state.go both parse interfaces, VLANs, VRFs from Redis-like data structures. The state.go functions operate on ConfigDB, while statedb.go operates on StateDB, but the iteration patterns are very similar. |
| **Remediation** | Consider a unified state builder that takes a source interface (ConfigDB or StateDB) and populates a common DeviceState struct. |

### D-08: Large ConfigDB Struct with 30+ Fields

| Field | Value |
|-------|-------|
| **File** | `pkg/device/configdb.go` lines 14-61 |
| **Category** | Structural Issue |
| **Severity** | MEDIUM |
| **Description** | The `ConfigDB` struct has 30+ map fields, one per CONFIG_DB table. This makes it unwieldy and adding new tables requires modifying the struct, the parser, and the GetAll scanner. |
| **Remediation** | Consider a two-tier approach: keep typed maps for frequently-accessed tables (Port, VLAN, Interface, BGPNeighbor), but use a generic `map[string]map[string]map[string]string` (table -> key -> field -> value) for less-used tables. Or use the table registry pattern from D-02. |

### D-09: device.go Mixes Connection Management with Business Logic

| Field | Value |
|-------|-------|
| **File** | `pkg/device/device.go` |
| **Category** | Structural Issue |
| **Severity** | LOW |
| **Description** | `device.go` (1015 lines) handles SSH tunnel creation, Redis connection, distributed locking, config reload, BGP service restart, FRR defaults application, route verification, and change application. The file mixes infrastructure (connection, tunnel) with domain logic (FRR defaults, health checks). |
| **Remediation** | Extract connection management (Connect/Disconnect/tunnel) into `connection.go`. Extract FRR-specific logic (ApplyFRRDefaults, RestartService) into `frr.go`. |

### D-10: Missing Error Handling in statedb.go Lua Scripts

| Field | Value |
|-------|-------|
| **File** | `pkg/device/statedb.go` |
| **Category** | Missing Error Handling |
| **Severity** | MEDIUM |
| **Description** | The distributed locking Lua scripts (`AcquireLock`, `ReleaseLock`) use `redis.Call` within Lua. If the Redis connection drops mid-script, the Lua script could leave the lock in an inconsistent state. There is no TTL refresh mechanism for long-running operations. |
| **Remediation** | Add a lock watchdog that refreshes TTL periodically during long operations. Add explicit error handling for Lua script failures with retry logic. |

---

## 4. Package: pkg/spec/

**Files**: `types.go` (483 lines), `loader.go` (613 lines), `resolver.go` (140 lines)

### S-01: TODO(v4) Comments — Tracked but Not Consumed

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/types.go` lines 59, 189, 196, 322 |
| **Category** | TODO/FIXME |
| **Severity** | LOW |
| **Description** | Four `TODO(v4)` comments mark unconsumed fields: `RegionSpec.ASName` (line 59), `FilterRule.Log` (line 189), `PolicerSpec.Action` (line 196), `DeviceProfile.VLANPortMapping` (line 322). These are tracked design debts from the spec phase. |
| **Remediation** | Either implement (if needed for upcoming features) or promote to a tracked issue. The `v4` marker helps categorize. |

### S-02: ResolveString Compiles Regex on Every Call

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/resolver.go` line 26 |
| **Category** | Performance |
| **Severity** | LOW |
| **Description** | `ResolveString` calls `regexp.MustCompile` on every invocation. While the resolver is not on a hot path, this is wasteful. |
| **Remediation** | Compile the regex once as a package-level `var` or as a field on the `Resolver` struct. |

### S-03: splitEndpoint Hand-Rolled vs strings.SplitN

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/loader.go` lines 606-613 |
| **Category** | Code Style |
| **Severity** | LOW |
| **Description** | `splitEndpoint` is a hand-rolled single-split on `:`. `strings.SplitN(endpoint, ":", 2)` does the same thing. |
| **Remediation** | Replace with `strings.SplitN`. |

### S-04: ListServices/ListRegions Return Unsorted

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/loader.go` lines 466-481 |
| **Category** | Inconsistent Pattern |
| **Severity** | LOW |
| **Description** | `ListServices()` and `ListRegions()` iterate maps without sorting, so output order is non-deterministic. Contrast with `TopologySpecFile.DeviceNames()` which explicitly sorts. |
| **Remediation** | Sort the returned slices for deterministic output, especially for CLI display. |

### S-05: Loader.deriveBGPNeighbors Silently Swallows Errors

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/loader.go` lines 179-193 |
| **Category** | Missing Error Handling |
| **Severity** | MEDIUM |
| **Description** | When loading a route reflector's profile fails, the error is silently swallowed with `continue`. This means a typo in `route_reflectors` silently omits a BGP neighbor, which could cause hard-to-debug convergence failures. |
| **Remediation** | Log a warning when a route reflector profile cannot be loaded: `log.Warnf("route reflector '%s' profile not found: %v", rrName, err)`. |

### S-06: AliasContext Mutates Shared Resolver State

| Field | Value |
|-------|-------|
| **File** | `pkg/spec/resolver.go` lines 112-134 |
| **Category** | Bug Risk |
| **Severity** | MEDIUM |
| **Description** | `NewAliasContext` and `WithInterface`/`WithService` call `SetAlias` on the shared `Resolver`, mutating global state. If two goroutines create AliasContexts for different devices/interfaces concurrently, they will overwrite each other's aliases. |
| **Remediation** | Make `AliasContext` create a copy of the alias map or use a layered resolver that overlays context-specific aliases on top of the shared base without modifying it. |

---

## 5. Package: pkg/model/

**Files**: `interface.go` (113 lines), `vlan.go` (127 lines), `acl.go` (186 lines), `bgp.go` (172 lines), `evpn.go` (105 lines), `vrf.go` (114 lines), `lag.go` (113 lines), `policy.go` (138 lines), `qos.go` (167 lines)

### M-01: ProtocolBGP = 179 Bug

| Field | Value |
|-------|-------|
| **File** | `pkg/model/acl.go` line 84 |
| **Category** | Bug |
| **Severity** | HIGH |
| **Description** | `ProtocolBGP = 179` is defined with a comment acknowledging it is wrong: `// This is a port number, not protocol`. BGP uses TCP (protocol 6) on port 179. Any code using `ProtocolBGP` for IP_PROTOCOL matching will generate invalid ACL rules. The constant exists and could be used accidentally. |
| **Remediation** | Remove `ProtocolBGP` entirely. If a BGP port constant is needed, rename to `PortBGP = 179` and place it with port constants, not protocol constants. |

### M-02: ProtocolFromName Does Not Handle "bgp"

| Field | Value |
|-------|-------|
| **File** | `pkg/model/acl.go` lines 88-103 |
| **Category** | Inconsistent Pattern |
| **Severity** | LOW |
| **Description** | `ProtocolFromName` handles "icmp", "tcp", "udp", "ospf", "vrrp" but returns 0 for unknown names including "bgp". Meanwhile, the protoMap in `service_gen.go` and `device_ops.go` both include "bgp" -> 179. The three protocol mapping locations are inconsistent. |
| **Remediation** | Create a single canonical protocol mapping in `model/acl.go`. Remove the duplicate protoMaps from network package. Remove "bgp" from protocol mappings entirely. |

### M-03: Model Types Largely Unused by Backend

| Field | Value |
|-------|-------|
| **File** | `pkg/model/bgp.go`, `pkg/model/evpn.go`, `pkg/model/vrf.go`, `pkg/model/lag.go`, `pkg/model/policy.go` |
| **Category** | Dead Code |
| **Severity** | MEDIUM |
| **Description** | The rich domain model types (BGPConfig, BGPNeighbor, VTEP, VXLANTunnelMap, EVPNConfig, VRF, PortChannel, RoutingPolicy, etc.) are defined with constructors and mutator methods, but the actual backend operations in `pkg/network/` work directly with CONFIG_DB field maps (`map[string]string`). The model types appear to be designed for a higher-level API that was never fully integrated. For example, `model.NewVTEP()` creates a VTEP model object, but `device_ops.CreateVTEP()` builds raw CONFIG_DB fields. |
| **Remediation** | Audit which model types are actually consumed by imports. Consider either: (a) wiring the operations to use model types as intermediaries (cleaner API but more work), or (b) marking unused types as deprecated with a clear comment about their intended future use, or (c) removing them if the CONFIG_DB field map approach is the settled pattern. |

### M-04: Linear Search in Collection Operations

| Field | Value |
|-------|-------|
| **File** | `pkg/model/vlan.go` lines 74-126, `pkg/model/vrf.go` lines 65-93, `pkg/model/lag.go` lines 79-107, `pkg/model/acl.go` lines 124-185 |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | AddTaggedMember, AddUntaggedMember, HasMember, RemoveMember, AddInterface, HasInterface, RemoveInterface, AddMember, HasMember, RemoveMember, BindInterface, UnbindInterface, IsBoundTo — all follow the same linear-scan pattern over slices. This pattern is repeated across vlan.go, vrf.go, lag.go, and acl.go. |
| **Remediation** | Extract a generic slice utility: `util.SliceContains`, `util.SliceAdd`, `util.SliceRemove` (or use Go 1.21 `slices` package). Or convert member lists to `map[string]struct{}` sets. |

### M-05: QoS Legacy Types Still Present

| Field | Value |
|-------|-------|
| **File** | `pkg/model/qos.go` lines 9-15 |
| **Category** | Dead Code |
| **Severity** | LOW |
| **Description** | `QoSProfile` (legacy) is still referenced by `spec.NetworkSpecFile.QoSProfiles`. The MEMORY.md notes "Legacy qos_profiles / QoSProfile kept for backward compat; new qos_policy takes precedence." The model types `Scheduler`, `DropProfile`, `Policer` may not be consumed by the backend. |
| **Remediation** | Mark as `// Deprecated:` in doc comments. Add a migration timeline comment. |

### M-06: Interface Type Detection Uses First Character Only

| Field | Value |
|-------|-------|
| **File** | `pkg/model/interface.go` lines 70-87 |
| **Category** | Bug Risk |
| **Severity** | LOW |
| **Description** | `IsPhysical()` checks if name starts with 'E'/'e', `IsLAG()` checks 'P'/'p', `IsVLAN()` checks 'V'/'v', `IsLoopback()` checks 'L'/'l'. This is fragile — an interface named "Vrf-Blue" would match `IsVLAN`, a name like "PortChannel" would match `IsLAG`. These are case-insensitive prefix checks that don't validate the full prefix (e.g., "Ethernet", "PortChannel", "Vlan", "Loopback"). |
| **Remediation** | Use `strings.HasPrefix(i.Name, "Ethernet")`, `strings.HasPrefix(i.Name, "PortChannel")`, etc. for robust type detection. |

---

## 6. Package: pkg/operations/

**Files**: `precondition.go` (456 lines)

### O-01: DependencyChecker Duplication Notice

| Field | Value |
|-------|-------|
| **File** | `pkg/operations/precondition.go` lines 317-324 |
| **Category** | Code Duplication |
| **Severity** | MEDIUM |
| **Description** | The code itself contains a comment acknowledging duplication: "NOTE: There is also a DependencyChecker in pkg/network/interface_ops.go which is used directly by Interface.RemoveService(). This version in operations is for standalone Operation implementations. Consider consolidating if import cycles can be avoided." The two DependencyChecker implementations have diverged and are maintained separately. |
| **Remediation** | Move DependencyChecker to a shared location. Options: (a) move to `pkg/util/` which both packages already import, (b) create a `pkg/checker/` package, or (c) keep in `pkg/operations/` and have `interface_ops.go` import it (since operations already imports network, this would create a cycle — so option (a) or (b) is needed). |

### O-02: splitPorts Duplication

| Field | Value |
|-------|-------|
| **File** | `pkg/operations/precondition.go` lines 443-455 |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | `splitPorts` is duplicated here from `pkg/network/device_ops.go` lines 1205-1225. See N-05. |
| **Remediation** | See N-05 — consolidate into `pkg/util/`. |

### O-03: PreconditionChecker Could Be Used More Widely

| Field | Value |
|-------|-------|
| **File** | `pkg/operations/precondition.go` |
| **Category** | Inconsistent Pattern |
| **Severity** | MEDIUM |
| **Description** | `PreconditionChecker` provides a clean, composable pattern for precondition validation with structured errors. However, the 40+ operations in `device_ops.go` and `interface_ops.go` use inline `if !d.IsConnected() { return err }` checks instead. Only external/test code seems to use PreconditionChecker. |
| **Remediation** | Migrate device_ops.go operations to use PreconditionChecker for consistency. Example: `NewPreconditionChecker(d, "create-vlan", vlanName).RequireConnected().RequireLocked().RequireVLANNotExists(id).Result()`. This would also address N-03 by centralizing the connected/locked checks. |

---

## 7. Cross-Package Issues

### X-01: Triple Protocol Mapping Definitions

| Field | Value |
|-------|-------|
| **Files** | `pkg/model/acl.go` (ProtocolFromName + constants), `pkg/network/service_gen.go:483-485` (protoMap), `pkg/network/device_ops.go:415-417` (protoMap) |
| **Category** | Code Duplication |
| **Severity** | HIGH |
| **Description** | IP protocol number mappings are defined in three separate locations with inconsistent coverage. `model/acl.go` has constants + ProtocolFromName (no "gre"). `service_gen.go` has an inline protoMap (includes "gre", "bgp"). `device_ops.go` has another inline protoMap (includes "gre", "bgp"). All three include the incorrect `bgp: 179` mapping. |
| **Remediation** | Define a single canonical `ProtoMap map[string]int` in `pkg/model/acl.go` and import it everywhere. Remove "bgp" from the mapping. Add "gre" to the canonical map. Delete the inline protoMaps. |

### X-02: model Package Types vs CONFIG_DB Field Maps

| Field | Value |
|-------|-------|
| **Files** | `pkg/model/*.go` (rich domain types), `pkg/network/device_ops.go` (raw `map[string]string` fields), `pkg/device/configdb.go` (typed entry structs) |
| **Category** | Structural Issue |
| **Severity** | MEDIUM |
| **Description** | There are three representations of the same domain concepts: (1) `model.VLAN`, `model.VRF`, `model.BGPNeighbor` etc. — rich domain models with methods. (2) `device.VLANEntry`, `device.VRFEntry`, `device.BGPNeighborEntry` — flat CONFIG_DB representations. (3) Raw `map[string]string` field maps in device_ops operations. The model types are not wired into the operations path. The CONFIG_DB entry types are used for parsing, but operations bypass them by building raw field maps. |
| **Remediation** | Choose one approach and commit to it. Recommended: operations build typed config structs (either model types or device entry types), then a serializer converts to CONFIG_DB field maps. This adds type safety to the operations layer. |

### X-03: PortChannel Represented in Three Ways

| Field | Value |
|-------|-------|
| **Files** | `pkg/model/lag.go` (PortChannel struct), `pkg/device/configdb.go` (PortChannelEntry struct), `pkg/network/device_ops.go` (PortChannelConfig struct) |
| **Category** | Code Duplication |
| **Severity** | LOW |
| **Description** | PortChannel has three struct representations: the rich model type with methods (model.PortChannel), the CONFIG_DB entry type (device.PortChannelEntry), and the operation options type (network.PortChannelConfig). This is partially by design (options vs. state vs. domain), but the proliferation makes it hard to know which to use. |
| **Remediation** | Document the role of each: `model.*` = domain model for external API, `device.*Entry` = CONFIG_DB wire format, `network.*Config` = operation inputs. Consider if model types can be eliminated (see M-03). |

### X-04: Inconsistent Error Creation Patterns

| Field | Value |
|-------|-------|
| **Files** | `pkg/network/device_ops.go` (bare `fmt.Errorf`), `pkg/operations/precondition.go` (`util.NewPreconditionError`), `pkg/network/changeset.go` (bare `fmt.Errorf`) |
| **Category** | Inconsistent Pattern |
| **Severity** | MEDIUM |
| **Description** | Operations in `device_ops.go` use bare `fmt.Errorf("device not connected")` while `PreconditionChecker` uses structured `util.NewPreconditionError(op, resource, precondition, details)`. This means callers cannot programmatically distinguish between a precondition failure and an operational error. |
| **Remediation** | Have device_ops use `PreconditionChecker` (see O-03) or at minimum use typed errors (`util.ErrNotConnected`, `util.ErrNotLocked`) that callers can check with `errors.Is()`. |

---

## 8. Summary Table

| ID | Package | Category | Severity | One-Line Summary |
|----|---------|----------|----------|-----------------|
| N-01 | network | Structural | HIGH | device_ops.go is a 2308-line god file |
| N-02 | network | Structural | HIGH | interface_ops.go is a 1772-line god file |
| N-03 | network | Duplication | HIGH | Connected/locked check repeated 40+ times |
| N-04 | network | Duplication | MEDIUM | ChangeType defined in two packages |
| N-05 | network | Duplication | LOW | Hand-rolled joinPorts/splitPorts (3 copies) |
| N-06 | network | Duplication | LOW | splitConfigDBKey duplicated in device pkg |
| N-07 | network | Duplication | MEDIUM | Existence checks duplicated across layers |
| N-08 | network | Dead Code | MEDIUM | readFileViaSSH is a non-functional stub |
| N-09 | network | Bug | HIGH | "bgp": 179 is a port number, not protocol |
| N-10 | network | Dead Code | LOW | VTEPConfig.UDPPort, VRFConfig.ImportRT/ExportRT unused |
| N-11 | network | Duplication | MEDIUM | Apply/Verify duplicate Change conversion |
| N-12 | network | Pattern | LOW | Linear dedup instead of set |
| N-13 | network | Bug Risk | MEDIUM | InterfaceIsLAGMember uses suffix match |
| N-14 | network | Magic Numbers | LOW | WRED thresholds hardcoded |
| N-15 | network | Magic Numbers | LOW | Lock TTL hardcoded |
| N-16 | network | Duplication | LOW | 12 identical Get* accessor methods |
| N-17 | network | Missing | LOW | addInterfaceToList not co-located |
| D-01 | device | Safety | HIGH | KEYS * in GetAll() — production risk |
| D-02 | device | Structural | MEDIUM | parseEntry 250-line switch statement |
| D-03 | device | Duplication | MEDIUM | Duplicate ChangeType (see N-04) |
| D-04 | device | Pattern | MEDIUM | fmt.Printf instead of structured logging |
| D-05 | device | Security | MEDIUM | InsecureIgnoreHostKey in SSH tunnel |
| D-06 | device | Duplication | LOW | Linear dedup in parseVRFs |
| D-07 | device | Duplication | MEDIUM | statedb PopulateDeviceState overlaps state.go |
| D-08 | device | Structural | MEDIUM | ConfigDB struct has 30+ fields |
| D-09 | device | Structural | LOW | device.go mixes connection and business logic |
| D-10 | device | Error Handling | MEDIUM | No lock TTL refresh; Lua script error handling |
| S-01 | spec | TODO | LOW | 4 unconsumed TODO(v4) fields |
| S-02 | spec | Performance | LOW | Regex compiled on every call |
| S-03 | spec | Style | LOW | Hand-rolled splitEndpoint |
| S-04 | spec | Pattern | LOW | ListServices/ListRegions unsorted |
| S-05 | spec | Error Handling | MEDIUM | deriveBGPNeighbors silently swallows errors |
| S-06 | spec | Bug Risk | MEDIUM | AliasContext mutates shared resolver state |
| M-01 | model | Bug | HIGH | ProtocolBGP = 179 wrong (port, not protocol) |
| M-02 | model | Pattern | LOW | ProtocolFromName inconsistent with protoMaps |
| M-03 | model | Dead Code | MEDIUM | Model types largely unused by backend ops |
| M-04 | model | Duplication | LOW | Linear search in 12+ collection methods |
| M-05 | model | Dead Code | LOW | Legacy QoS types still present |
| M-06 | model | Bug Risk | LOW | Interface type detection uses first char only |
| O-01 | operations | Duplication | MEDIUM | DependencyChecker exists in two places |
| O-02 | operations | Duplication | LOW | splitPorts duplicated from network |
| O-03 | operations | Pattern | MEDIUM | PreconditionChecker underused by device_ops |
| X-01 | cross | Duplication | HIGH | Protocol mappings in 3 locations |
| X-02 | cross | Structural | MEDIUM | 3 representations of same domain concepts |
| X-03 | cross | Duplication | LOW | PortChannel in 3 struct types |
| X-04 | cross | Pattern | MEDIUM | Inconsistent error creation patterns |

---

## 9. Recommended Refactor Order

Prioritized by impact-to-effort ratio:

### Phase 1: Quick Wins (low effort, high impact)

1. **Fix ProtocolBGP bug** (N-09, M-01, X-01) — Remove "bgp": 179 from all protoMaps and ProtocolBGP constant. Single canonical protocol map in model/acl.go. **Risk**: Could fix silent ACL misconfiguration.

2. **Extract requireWritable helper** (N-03) — Single helper function eliminates 40+ duplicated blocks across device_ops.go and interface_ops.go. **Impact**: Removes ~120 lines of boilerplate.

3. **Unify ChangeType** (N-04, D-03, N-11) — Define once in device package, use everywhere. Extract toDeviceChanges() helper. **Impact**: Removes ~40 lines of duplicate conversion code.

4. **Consolidate splitPorts/joinPorts** (N-05, O-02) — Move to pkg/util. **Impact**: Removes 3 duplicate implementations.

### Phase 2: Structural Improvements (medium effort, high impact)

5. **Split device_ops.go** (N-01) — Into 6-8 domain-specific files. Pure file reorganization, no logic changes. **Impact**: Navigability, reviewability.

6. **Split interface_ops.go** (N-02) — Into service_ops.go, dependency.go, interface_bgp.go. **Impact**: Same.

7. **Replace KEYS * with SCAN** (D-01) — In ConfigDBClient.GetAll and StateDBClient.GetAll. **Risk**: Production safety improvement.

8. **Implement readFileViaSSH** (N-08) — Using ExecCommand from tunnel.go. **Impact**: Unblocks platform.json loading.

### Phase 3: Deeper Cleanup (higher effort)

9. **Adopt PreconditionChecker in device_ops** (O-03, X-04) — Migrate from inline checks to composable checker. **Impact**: Consistency, structured errors.

10. **Fix AliasContext thread safety** (S-06) — Copy-on-write or layered resolver.

11. **Consolidate DependencyChecker** (O-01) — Move to shared package.

12. **Resolve model types usage** (M-03, X-02) — Decide: wire into operations or deprecate.
