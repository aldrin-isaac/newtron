# Plan: Validate at Shadow Entry + Eliminate CompositeConfig

## Context

Two related problems in the write/delivery pipeline:

**Problem 1 — Late validation.** Schema validation runs lazily inside
`cs.Apply()` (physical path) and `DeliverComposite()` (delivery path). The
abstract path has NO validation. If a config function produces an invalid entry,
the shadow silently absorbs it. The error surfaces at delivery time — or never.

**Problem 2 — CompositeConfig is a redundant representation.** The delivery
pipeline serializes the abstract node's configDB into `[]Entry`
(`ExportEntries`), copies entries into a `map[table][key][field]value`
(`CompositeBuilder`), wraps it in `CompositeConfig`, then serializes it BACK
to `[]Entry` (`ToEntries`) for Redis. Five steps where one suffices.
`CompositeConfig` is a degraded copy of what the abstract node's configDB
already holds — same data, no types, no validation history.

**Root principle:** The abstract node is the unified model. ALL operations —
online or offline — pass through it. The abstract node's configDB flows
directly to delivery. No intermediate representations.

**Fix:**
1. Validate entries as they enter the shadow (`applyShadow`), not as they leave
2. Pass `*sonic.ConfigDB` to delivery, not `CompositeConfig`
3. Delete `CompositeConfig`, `CompositeBuilder`, `CompositeMetadata`, and all
   serialization/deserialization methods

## Part A: Validate at Shadow Entry

### A1. `applyShadow` returns error (`node.go:271`)

```go
func (n *Node) applyShadow(cs *ChangeSet) error {
    if cs == nil { return nil }
    if err := cs.Validate(); err != nil { return err }
    // ...existing hydration logic...
    return nil
}
```

### A2. `op()` propagates applyShadow error (`changeset.go:169`)

```go
if err := n.applyShadow(cs); err != nil {
    return nil, fmt.Errorf("%s: schema validation failed: %w", name, err)
}
```

### A3. Update all 19 existing applyShadow callers to check error

| File | Line | Function |
|------|------|----------|
| changeset.go | 169 | `op()` |
| node.go | 264 | `SetDeviceMetadata` |
| baseline_ops.go | 154 | `ConfigureLoopback` |
| vlan_ops.go | 292 | `UnconfigureIRB` |
| portchannel_ops.go | 95 | `CreatePortChannel` |
| bgp_ops.go | 423 | `ConfigureBGP` |
| bgp_ops.go | 472 | `AddBGPEVPNPeer` |
| interface_ops.go | 68 | `SetIP` |
| interface_ops.go | 119 | `SetVRF` |
| interface_ops.go | 208 | `ConfigureInterface` |
| interface_ops.go | 316 | `UnconfigureInterface` |
| interface_ops.go | 509 | `ClearProperty` |
| evpn_ops.go | 188 | `UnbindMACVPN` |
| evpn_ops.go | 273 | `SetupVTEP` |
| evpn_ops.go | 385 | `ConfigureRouteReflector` |
| qos_ops.go | 101 | `RemoveQoS` |
| service_ops.go | 580 | `ApplyService` |
| service_ops.go | 1456 | `RemoveService` |
| service_ops.go | 1560 | `RefreshService` (stale cleanup) |

### A4. `applyIntentToShadow` returns error (`intent_ops.go:121`)

```go
func (n *Node) applyIntentToShadow(entry sonic.Entry) error {
    if err := sonic.ValidateChanges([]sonic.ConfigChange{
        {Table: entry.Table, Key: entry.Key, Type: sonic.ChangeTypeAdd, Fields: entry.Fields},
    }); err != nil {
        return err
    }
    n.configDB.ApplyEntries([]sonic.Entry{entry})
    return nil
}
```

### A5. Update 4 callers of applyIntentToShadow

| Line | Function | Context |
|------|----------|---------|
| 47 | `writeIntent` | Idempotent update |
| 68 | `writeIntent` | Parent registration |
| 81 | `writeIntent` | New intent creation |
| 108 | `deleteIntent` | Parent deregistration |

### A6. Remove late validation from `cs.Apply()` (`changeset.go:224`)

Delete the `cs.Validate()` call inside `Apply`. Already validated at `applyShadow`.

Safety verification — all 4 callers of `cs.Apply()` receive validated ChangeSets:

| File | Line | Caller | ChangeSet source |
|------|------|--------|------------------|
| `node/node.go` | 645 | `ExecuteOp` | `fn()` — operation that called `applyShadow` |
| `pkg/newtron/node.go` | 429 | `ExecuteOperation` | operation dispatcher, calls `applyShadow` |
| `pkg/newtron/node.go` | 732 | `RollbackOperation` | reverse operation, calls `applyShadow` |
| `pkg/newtron/node.go` | 1017 | `RevertOperations` | reverse operation, calls `applyShadow` |

### A7. Validate `RegisterPort` entries (`node.go:236`)

`RegisterPort` calls `configDB.ApplyEntries` directly — bypassing `applyShadow`
and validation. Add validation before hydration:

```go
func (n *Node) RegisterPort(name string, fields map[string]string) error {
    if fields == nil {
        fields = map[string]string{}
    }
    entry := sonic.Entry{Table: "PORT", Key: name, Fields: fields}
    if err := sonic.ValidateChanges([]sonic.ConfigChange{
        {Table: entry.Table, Key: entry.Key, Type: sonic.ChangeTypeAdd, Fields: entry.Fields},
    }); err != nil {
        return err
    }
    n.configDB.ApplyEntries([]sonic.Entry{entry})
    n.interfaces[name] = &Interface{node: n, name: name}
    return nil
}
```

Callers (topology.go:84, reconstruct.go:512) must check the returned error.

### A8. Fix 11 functions missing applyShadow (pre-existing bugs)

| File | Function | Line | Entries not shadowed |
|------|----------|------|---------------------|
| `interface_bgp_ops.go` | `AddBGPPeer` | 83 | BGP_NEIGHBOR, BGP_NEIGHBOR_AF |
| `interface_ops.go` | `RemoveIP` | 84 | INTERFACE deletes |
| `interface_ops.go` | `BindACL` | 336 | ACL_TABLE ports update |
| `interface_ops.go` | `UnbindACL` | 381 | ACL_TABLE ports update |
| `interface_ops.go` | `SetProperty` | 415 | PORT/PORTCHANNEL field update |
| `baseline_ops.go` | `RemoveLoopback` | 166 | LOOPBACK_INTERFACE deletes |
| `bgp_ops.go` | `RemoveBGPGlobals` | 514 | BGP_GLOBALS, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE |
| `evpn_ops.go` | `TeardownVTEP` | 287 | VXLAN_TUNNEL, VXLAN_EVPN_NVO, BGP_NEIGHBOR |
| `vrf_ops.go` | `DeleteVRF` | 184 | VRF, BGP_GLOBALS |
| `vrf_ops.go` | `UnbindIPVPN` | 334 | VRF update, VNI map, transit VLAN |
| `qos_ops.go` | `ApplyQoS` | 25 | QoS maps, PORT_QOS_MAP, QUEUE |

## Part B: Eliminate CompositeConfig

### B1. Add `ConfigDB.ExportRaw()` method (`configdb.go`)

```go
func (db *ConfigDB) ExportRaw() RawConfigDB {
    raw := make(RawConfigDB)
    for _, e := range db.ExportEntries() {
        if raw[e.Table] == nil {
            raw[e.Table] = make(map[string]map[string]string)
        }
        raw[e.Table][e.Key] = e.Fields
    }
    return raw
}
```

Drift detection needs `RawConfigDB`. This replaces the cast
`sonic.RawConfigDB(composite.Tables)`.

### B2. Rename delivery types and methods

| Old | New | Rationale |
|-----|-----|-----------|
| `CompositeMode` | `DeliveryMode` | Mode describes delivery, not the data |
| `CompositeOverwrite` | `DeliveryOverwrite` | |
| `CompositeMerge` | `DeliveryMerge` | |
| `CompositeDeliveryResult` | `DeliveryResult` | Drop "Composite" prefix |
| `DeliverComposite(cc *CompositeConfig, mode)` | `Deliver(db *sonic.ConfigDB, mode)` | Accepts configDB directly |
| `VerifyComposite(ctx, cc *CompositeConfig)` | `VerifyExpected(ctx, db *sonic.ConfigDB)` | Verifies expected configDB against live |

### B3. Rewrite `Node.Deliver()` (`composite.go:143`)

```go
func (n *Node) Deliver(expected *sonic.ConfigDB, mode DeliveryMode) (*DeliveryResult, error) {
    if err := n.precondition("deliver", string(mode)).Result(); err != nil {
        return nil, err
    }
    // No schema validation here — entries were validated at applyShadow
    entries := expected.ExportEntries()
    result := &DeliveryResult{Mode: mode}
    client := n.conn.Client()

    switch mode {
    case DeliveryOverwrite:
        if err := client.ReplaceAll(entries); err != nil {
            result.Error = err
            result.Failed = len(entries)
            return result, err
        }
        result.Applied = len(entries)
    case DeliveryMerge:
        if err := n.validateMergeFromDB(expected); err != nil {
            return nil, err
        }
        if err := client.PipelineSet(entries); err != nil {
            result.Error = err
            result.Failed = len(entries)
            return result, err
        }
        result.Applied = len(entries)
    default:
        return nil, fmt.Errorf("unknown delivery mode: %s", mode)
    }
    return result, nil
}
```

### B4. Rewrite `validateMerge` → `validateMergeFromDB`

```go
func (n *Node) validateMergeFromDB(expected *sonic.ConfigDB) error {
    configDB := n.ConfigDB()
    if configDB == nil {
        return fmt.Errorf("config_db not loaded")
    }
    for resource := range expected.NewtronIntent {
        if existing, exists := configDB.NewtronIntent[resource]; exists {
            return fmt.Errorf("interface %s already has service '%s' bound",
                resource, existing["service_name"])
        }
    }
    return nil
}
```

Typed map access (`expected.NewtronIntent`) replaces string-keyed
`composite.Tables["NEWTRON_INTENT"]`.

### B5. Rewrite `VerifyExpected` (`changeset.go:377`)

```go
func (n *Node) VerifyExpected(ctx context.Context, expected *sonic.ConfigDB) (*sonic.VerificationResult, error) {
    entries := expected.ExportEntries()
    var changes []sonic.ConfigChange
    for _, e := range entries {
        changes = append(changes, sonic.ConfigChange{
            Table: e.Table, Key: e.Key, Type: sonic.ChangeTypeAdd, Fields: e.Fields,
        })
    }
    return n.verifyConfigChanges(changes)
}
```

### B6. Delete `BuildComposite()` from Node

Delete internal `BuildComposite()` (node.go:245-252) — callers use
`n.ConfigDB()` directly. The shadow configDB IS the result.

Delete public `Node.BuildComposite()` (pkg/newtron/node.go:1524-1527) —
no production callers. Test callers use `n.ConfigDB()` or the topology
provisioner path instead.

### B7. Rewrite `GenerateDeviceComposite` (`topology.go:54`)

Returns `*sonic.ConfigDB` instead of `*CompositeConfig`:

```go
func (tp *TopologyProvisioner) GenerateDeviceComposite(deviceName string) (*sonic.ConfigDB, error) {
    // ...existing setup (profile, resolved, abstract node, RegisterPort, ReplayStep)...
    return n.ConfigDB(), nil
}
```

### B8. Update `ProvisionDevice` (`topology.go:114`)

```go
result, err := dev.Deliver(configDB, node.DeliveryOverwrite)
```

### B9. Update `VerifyDeviceHealth` (`topology.go:207`)

```go
configDB, err := tp.GenerateDeviceComposite(deviceName)
...
configResult, err := dev.VerifyExpected(ctx, configDB)
```

### B10. Update `DetectTopologyDrift` (`topology.go:277`)

```go
configDB, err := tp.GenerateDeviceComposite(deviceName)
expectedRaw := configDB.ExportRaw()
```

### B11. Update `DetectDrift` (`node.go:220-222`)

```go
expectedRaw := expected.ConfigDB().ExportRaw()
```

Replaces `expected.BuildComposite()` → `sonic.RawConfigDB(composite.Tables)`.

### B12. Update public API boundary (`pkg/newtron/node.go`)

`CompositeInfo.internal` holds `*sonic.ConfigDB` instead of `*node.CompositeConfig`.

```go
func wrapConfigDB(deviceName string, db *sonic.ConfigDB) *CompositeInfo {
    entries := db.ExportEntries()
    tables := make(map[string]int)
    for _, e := range entries {
        tables[e.Table]++
    }
    return &CompositeInfo{
        DeviceName: deviceName,
        EntryCount: len(entries),
        Tables:     tables,
        internal:   db,
    }
}
```

`DeliverComposite` and `VerifyComposite` at the public level extract
`*sonic.ConfigDB` instead of `*node.CompositeConfig`:

```go
func (n *Node) DeliverComposite(ctx context.Context, ci *CompositeInfo, mode CompositeMode) (*DeliveryResult, error) {
    db, ok := ci.internal.(*sonic.ConfigDB)
    ...
    result, err := n.internal.Deliver(db, node.DeliveryMode(mode))
    ...
}
```

### B13. Rename public API types and methods (`pkg/newtron/types.go`, `node.go`)

Per ai-instructions §12 (Same Concept = Same Name), internal and public names
must match. The concept is "delivery," not "composite."

| Old (public) | New (public) | Matches internal |
|--------------|-------------|------------------|
| `CompositeMode` | `DeliveryMode` | `node.DeliveryMode` |
| `CompositeOverwrite` | `DeliveryOverwrite` | `node.DeliveryOverwrite` |
| `CompositeMerge` | `DeliveryMerge` | `node.DeliveryMerge` |
| `CompositeInfo` | `ProvisioningInfo` | (public-only opaque handle) |
| `Node.DeliverComposite()` | `Node.Deliver()` | `node.Node.Deliver()` |
| `Node.VerifyComposite()` | `Node.VerifyExpected()` | `node.Node.VerifyExpected()` |

`ProvisioningInfo` replaces `CompositeInfo` — it holds a reference to a
generated ConfigDB for provisioning. JSON tags stay the same (wire compat).

### B14. Update HTTP handlers (`api/handler_composite.go`)

Rename file to `handler_provisioning.go`. Update method calls to new names.
Wire types (`CompositeHandleRequest`/`CompositeHandleResponse`) renamed to
`ProvisioningHandleRequest`/`ProvisioningHandleResponse`. JSON field names
preserved for wire compatibility.

### B15. Update client library (`client/network.go`)

Rename methods: `GenerateComposite` → `GenerateProvisioning`,
`DeliverComposite` → `Deliver`, `VerifyComposite` → `VerifyExpected`.

### B16. Update CLI (`cmd/newtron/cmd_provision.go`)

Minimal — uses client methods which return the same public types.

### B17. Update newtrun (`pkg/newtrun/steps.go`)

Same — uses client methods.

### B18. Delete dead code from `composite.go`

Delete: `CompositeConfig`, `CompositeMetadata`, `CompositeBuilder`,
`NewCompositeBuilder`, `SetGeneratedBy`, `AddEntries`, `AddEntry`, `Build`,
`ToConfigChanges`, `ToEntries`.

Keep (renamed): `DeliveryMode`, `DeliveryOverwrite`, `DeliveryMerge`,
`DeliveryResult`, `Deliver`, `validateMergeFromDB`.

### B19. Update pipeline.md §3, §5, §6, §9

- §3: Validate at applyShadow (not at Apply). Remove Validate from Apply box.
- §5: Export pipeline now feeds delivery directly (no CompositeBuilder step)
- §6: Delivery accepts `*ConfigDB`, calls `ExportEntries` internally
- §9: Validation happens at shadow entry, not apply/delivery time

## Files Changed

| File | Changes |
|------|---------|
| `node/node.go` | A1: applyShadow returns error; A3: SetDeviceMetadata error check; A7: RegisterPort validates+returns error; B6: delete BuildComposite; B11: DetectDrift uses ConfigDB().ExportRaw() |
| `node/changeset.go` | A2: op() error check; A6: remove Validate from Apply; B5: VerifyExpected replaces VerifyComposite |
| `node/composite.go` | B2-B4,B18: delete CompositeConfig/Builder/Metadata; rename types; rewrite Deliver to accept *ConfigDB |
| `node/intent_ops.go` | A4-A5: applyIntentToShadow returns error |
| `node/baseline_ops.go` | A3: ConfigureLoopback; A8: RemoveLoopback |
| `node/bgp_ops.go` | A3: ConfigureBGP, AddBGPEVPNPeer; A8: RemoveBGPGlobals |
| `node/evpn_ops.go` | A3: UnbindMACVPN, SetupVTEP, ConfigureRouteReflector; A8: TeardownVTEP |
| `node/interface_ops.go` | A3: SetIP, SetVRF, ConfigureInterface, UnconfigureInterface, ClearProperty; A8: RemoveIP, BindACL, UnbindACL, SetProperty |
| `node/interface_bgp_ops.go` | A8: AddBGPPeer |
| `node/portchannel_ops.go` | A3: CreatePortChannel |
| `node/qos_ops.go` | A3: RemoveQoS; A8: ApplyQoS |
| `node/service_ops.go` | A3: ApplyService, RemoveService, RefreshService |
| `node/vlan_ops.go` | A3: UnconfigureIRB |
| `node/vrf_ops.go` | A8: DeleteVRF, UnbindIPVPN |
| `sonic/configdb.go` | B1: add ExportRaw() |
| `sonic/configdb_diff.go` | No change — RawConfigDB type stays |
| `network/topology.go` | A7: RegisterPort error check; B7-B10: GenerateDeviceComposite returns *ConfigDB; ProvisionDevice/VerifyDeviceHealth/DetectTopologyDrift updated |
| `pkg/newtron/node.go` | B6: delete public BuildComposite; B12: wrapConfigDB, Deliver/VerifyExpected boundary |
| `pkg/newtron/types.go` | B13: rename CompositeMode → DeliveryMode |
| `pkg/newtron/provision.go` | B12: use wrapConfigDB |
| `api/handler_composite.go` | B14: renamed to `handler_provisioning.go`; updated method calls |
| `api/types.go` | B14: rename wire types (preserve JSON field names) |
| `client/network.go` | B15: rename methods |
| `cmd/newtron/cmd_provision.go` | B16: minimal updates |
| `pkg/newtrun/steps.go` | B17: minimal updates |
| `node/device_ops_test.go` | B6: replace `BuildComposite()` + `.Tables` access with `ConfigDB()` access |
| `node/reconstruct.go` | A7: `RegisterPort` error check in `ReconstructExpected` |
| `sonic/configdb_diff_test.go` | B1: add `ExportRaw()` round-trip test |
| `docs/newtron/pipeline.md` | B19: update §3, §5, §6, §9 |

## Resolved Concerns

| Concern | Resolution |
|---------|------------|
| ExportEntries round-trip fidelity | `structToFields` reads json tags + `.String()` — no lossy conversion. Validated entries in → valid entries out. Verified by `TestExportEntries_RoundTrip`. |
| ExportRaw fidelity vs CompositeConfig.Tables | Both built from `ExportEntries()`. CompositeBuilder copies fields one-by-one; ExportRaw assigns fresh maps from ExportEntries. Both produce identical `RawConfigDB`. New test verifies. |
| Intent records (NEWTRON_INTENT) | Schema entry has `AllowExtra: true`. `applyIntentToShadow` validates via `ValidateChanges` before hydrating. |
| Delete entries in validation | `ValidateChanges` handles deletes: validates key format only, skips field checks. No change needed. |
| RegisterPort bypass | Fixed in A7 — validates PORT entries before `ApplyEntries`. |
| 11 functions missing applyShadow | Pre-existing bugs. All 11 fixed in A8. |
| cs.Apply() safety after removing Validate | All 4 callers verified — each receives ChangeSets that were validated at `applyShadow`. Documented in A6. |
| Double-shadowing from writeIntent/deleteIntent | `writeIntent`/`deleteIntent` call `applyIntentToShadow` internally. When `applyShadow(cs)` also runs on the full ChangeSet, intent entries are re-applied. Harmless — `ApplyEntries` is idempotent (overwrites with same data). `DeleteEntry` on non-existent key is a no-op. |
| Performance of ExportRaw vs type cast | Current: `RawConfigDB(composite.Tables)` is O(1) type cast. New: `ExportRaw()` is O(n) iteration. Negligible vs Redis round-trips in `GetRawOwnedTables`. |
| ConfigDB ownership after GenerateDeviceComposite returns | `n.ConfigDB()` returns a heap pointer. The abstract Node `n` is local and GC'd, but the ConfigDB survives via the returned pointer. Callers only read — no mutation risk. |
| Merge operations | `cs.Merge(other)` appends changes. Sub-operations validate their own entries via `applyShadow` before merge. |

## Verification

1. `go build ./... && go vet ./...`
2. `go test ./... -count=1`
3. `grep -rn 'CompositeConfig\|CompositeBuilder\|CompositeMetadata' pkg/` — zero hits
4. `grep -rn 'applyShadow' pkg/newtron/network/node/ | grep -v _test.go | grep -v '//'` — 30 call sites (19 existing + 11 new)
5. `grep -rn 'cs.Validate()' pkg/newtron/network/node/changeset.go` — only method definition, no call inside Apply
6. Verify no function creates a ChangeSet with entries and returns without calling applyShadow
7. `grep -rn 'ExportRaw' pkg/newtron/device/sonic/` — definition + test, no other callers except drift paths
8. Run `TestExportRaw_RoundTrip` (new) — verifies `ExportRaw()` matches old `CompositeConfig.Tables` behavior

## Execution Order

1. Part A first (validate at applyShadow) — all _ops.go files + changeset.go + node.go + intent_ops.go
2. Part B second (eliminate CompositeConfig) — composite.go + topology.go + public API + clients
3. Build + test after each part
4. pipeline.md updates last

---

## Tracker

### Phase A: Validate at Shadow Entry

#### A1. `applyShadow` returns error
- [x] Change signature `func (n *Node) applyShadow(cs *ChangeSet)` → `func (n *Node) applyShadow(cs *ChangeSet) error` at `node/node.go:271`
- [x] Add `if cs == nil { return nil }` guard
- [x] Add `if err := cs.Validate(); err != nil { return err }` before hydration
- [x] Change return from void to `return nil` at end
- [x] **Verify**: `grep -n 'func.*applyShadow' pkg/newtron/network/node/node.go` shows `error` return

#### A2. `op()` propagates applyShadow error
- [x] At `changeset.go:169`, change `n.applyShadow(cs)` → `if err := n.applyShadow(cs); err != nil { return nil, fmt.Errorf("%s: schema validation failed: %w", name, err) }`
- [x] **Verify**: `grep -n 'applyShadow' pkg/newtron/network/node/changeset.go` shows error check

#### A3. Update 19 existing applyShadow callers to check error
Each caller currently calls `n.applyShadow(cs)` as a void statement. Change to `if err := n.applyShadow(cs); err != nil { return ..., err }`.

- [x] `changeset.go:169` — `op()` → returns `(nil, err)` **(covered by A2)**
- [x] `node.go:264` — `SetDeviceMetadata` → returns `err`
- [x] `baseline_ops.go:154` — `ConfigureLoopback` → returns `(nil, err)`
- [x] `vlan_ops.go:292` — `UnconfigureIRB` → returns `(nil, err)`
- [x] `portchannel_ops.go:95` — `CreatePortChannel` → returns `(nil, err)`
- [x] `bgp_ops.go:423` — `ConfigureBGP` → returns `(nil, err)`
- [x] `bgp_ops.go:472` — `AddBGPEVPNPeer` → returns `(nil, err)`
- [x] `interface_ops.go:68` — `SetIP` → returns `(nil, err)`
- [x] `interface_ops.go:119` — `SetVRF` → returns `(nil, err)`
- [x] `interface_ops.go:208` — `ConfigureInterface` → returns `(nil, err)`
- [x] `interface_ops.go:316` — `UnconfigureInterface` → returns `(nil, err)`
- [x] `interface_ops.go:509` — `ClearProperty` → returns `(nil, err)`
- [x] `evpn_ops.go:188` — `UnbindMACVPN` → returns `(nil, err)`
- [x] `evpn_ops.go:273` — `SetupVTEP` → returns `(nil, err)`
- [x] `evpn_ops.go:385` — `ConfigureRouteReflector` → returns `(nil, err)`
- [x] `qos_ops.go:101` — `RemoveQoS` → returns `(nil, err)`
- [x] `service_ops.go:580` — `ApplyService` → returns `(nil, err)`
- [x] `service_ops.go:1456` — `RemoveService` → returns `(nil, err)`
- [x] `service_ops.go:1560` — `RefreshService` stale cleanup → returns `err`
- [x] **Verify**: `go build ./...` compiles

#### A4. `applyIntentToShadow` returns error
- [x] Change signature `func (n *Node) applyIntentToShadow(entry sonic.Entry)` → `func (n *Node) applyIntentToShadow(entry sonic.Entry) error` at `intent_ops.go:121`
- [x] Add `sonic.ValidateChanges` call before `n.configDB.ApplyEntries`
- [x] Return `nil` on success
- [x] **Verify**: `grep -n 'func.*applyIntentToShadow' pkg/newtron/network/node/intent_ops.go` shows `error` return

#### A5. Update 4 callers of applyIntentToShadow
Each caller currently calls `n.applyIntentToShadow(entry)` as void. Change to `if err := n.applyIntentToShadow(entry); err != nil { return ..., err }`.

- [x] `intent_ops.go:47` — `writeIntent` idempotent update → returns `err`
- [x] `intent_ops.go:68` — `writeIntent` parent registration loop → returns `err`
- [x] `intent_ops.go:81` — `writeIntent` new intent creation → returns `err`
- [x] `intent_ops.go:108` — `deleteIntent` parent deregistration loop → returns `err`
- [x] **Verify**: `go build ./...` compiles

#### A6. Remove late validation from `cs.Apply()`
- [x] Delete `if err := cs.Validate(); err != nil { return err }` at `changeset.go:224`
- [x] **Verify**: `grep -n 'Validate' pkg/newtron/network/node/changeset.go` shows only method definition, no call inside Apply

#### A7. Validate `RegisterPort` entries
- [x] Change signature `func (n *Node) RegisterPort(name string, fields map[string]string)` → `func (n *Node) RegisterPort(name string, fields map[string]string) error` at `node.go:236`
- [x] Add nil guard for fields
- [x] Create `sonic.Entry{Table: "PORT", ...}` and validate via `sonic.ValidateChanges`
- [x] Return `nil` on success
- [x] Update caller `topology.go:85` — check returned error
- [x] Update caller `reconstruct.go:513` — check returned error
- [x] **Verify**: `go build ./...` compiles

#### A8. Fix 11 functions missing applyShadow
Each function creates a ChangeSet but never calls `applyShadow`. Add `n.applyShadow(cs)` with error check before returning the ChangeSet.

- [x] `interface_bgp_ops.go:83` — `AddBGPPeer`: add `applyShadow(cs)` before return at line 102
- [x] `interface_ops.go:84` — `RemoveIP`: add `applyShadow(cs)` before return at line 100
- [x] `interface_ops.go:336` — `BindACL`: add `applyShadow(cs)` before return at line 353
- [x] `interface_ops.go:381` — `UnbindACL`: add `applyShadow(cs)` before return at line 396
- [x] `interface_ops.go:415` — `SetProperty`: add `applyShadow(cs)` before return at line 466
- [x] `baseline_ops.go:166` — `RemoveLoopback`: add `applyShadow(cs)` before return at line 181
- [x] `bgp_ops.go:514` — `RemoveBGPGlobals`: add `applyShadow(cs)` before return at line 526
- [x] `evpn_ops.go:287` — `TeardownVTEP`: add `applyShadow(cs)` before return at line 308
- [x] `vrf_ops.go:184` — `DeleteVRF`: add `applyShadow(cs)` before return at line 195
- [x] `vrf_ops.go:334` — `UnbindIPVPN`: add `applyShadow(cs)` before return at line 360
- [x] `qos_ops.go:25` — `ApplyQoS`: add `applyShadow(cs)` before return at line 45
- [x] **Verify**: `grep -c 'applyShadow' pkg/newtron/network/node/*.go` ≥ 30 non-comment call sites

#### A-BUILD. Build + test after Part A
- [x] `go build ./...` passes
- [x] `go vet ./...` passes
- [x] `go test ./... -count=1` passes (fixed pre-existing invalid test fixture: speed "100000" → "100G")

---

### Phase B: Eliminate CompositeConfig

#### B1. Add `ConfigDB.ExportRaw()` method
- [x] Add `func (db *ConfigDB) ExportRaw() RawConfigDB` to `sonic/configdb.go` after `ExportEntries` (line ~891)
- [x] Iterates `db.ExportEntries()`, builds `RawConfigDB` (nested map)
- [x] Add `TestExportRaw_RoundTrip` to `sonic/configdb_diff_test.go`
- [x] **Verify**: `go test ./pkg/newtron/device/sonic/ -run TestExportRaw -count=1` passes

#### B2. Rename delivery types in `composite.go`
- [x] `CompositeMode` → `DeliveryMode` (line 11)
- [x] `CompositeOverwrite` → `DeliveryOverwrite` (line 18)
- [x] `CompositeMerge` → `DeliveryMerge` (line 23)
- [x] `CompositeDeliveryResult` → `DeliveryResult` (line 43)
- [x] Update all internal references to renamed types

#### B3. Rewrite `DeliverComposite` → `Deliver` in `composite.go`
- [x] Rename `func (n *Node) DeliverComposite(cc *CompositeConfig, mode CompositeMode)` → `func (n *Node) Deliver(expected *sonic.ConfigDB, mode DeliveryMode)` (line 140)
- [x] Replace `cc.ToEntries()` with `expected.ExportEntries()`
- [x] Replace `cc.ToConfigChanges()` validation with comment: entries validated at applyShadow
- [x] Return `*DeliveryResult` instead of `*CompositeDeliveryResult`
- [x] Add default case in switch for unknown mode
- [x] **Verify**: function accepts `*sonic.ConfigDB`, not `*CompositeConfig`

#### B4. Rewrite `validateMerge` → `validateMergeFromDB`
- [x] Rename function at `composite.go:191`
- [x] Change parameter from `composite *CompositeConfig` to `expected *sonic.ConfigDB`
- [x] Replace `composite.Tables["NEWTRON_INTENT"]` with `expected.NewtronIntent` (typed map)
- [x] Replace `composite.Tables["NEWTRON_SERVICE_BINDING"]` check (if present) with typed access

#### B5. Rewrite `VerifyComposite` → `VerifyExpected` in `changeset.go`
- [x] Rename `func (n *Node) VerifyComposite(...)` → `func (n *Node) VerifyExpected(ctx context.Context, expected *sonic.ConfigDB)` (find exact line)
- [x] Replace `cc.ToConfigChanges()` with iteration over `expected.ExportEntries()`
- [x] Return `*sonic.VerificationResult`

#### B6. Delete `BuildComposite()` from both layers
- [x] Delete internal `BuildComposite()` at `node/node.go:247-252`
- [x] Public `BuildComposite()` at `pkg/newtron/node.go` now delegates to `wrapConfigDB(n.internal.Name(), n.internal.ConfigDB())`
- [x] `wrapComposite()` replaced with `wrapConfigDB()` at `pkg/newtron/node.go`

#### B7. Rewrite `GenerateDeviceComposite` in `topology.go`
- [x] Change return type from `*node.CompositeConfig` to `*sonic.ConfigDB` at `topology.go:54`
- [x] Replace `n.BuildComposite()` at line 96 with `n.ConfigDB()`
- [x] `sonic` import present

#### B8. Update `ProvisionDevice` in `topology.go`
- [x] Changed to `dev.Deliver(configDB, node.DeliveryOverwrite)`
- [x] Variable name updated to `configDB`

#### B9. Update `VerifyDeviceHealth` in `topology.go`
- [x] Updated variable name and method call
- [x] Changed to `dev.VerifyExpected(ctx, configDB)`

#### B10. Update `DetectTopologyDrift` in `topology.go`
- [x] Replaced with `configDB.ExportRaw()`
- [x] Variable name updated to `configDB`

#### B11. Update `DetectDrift` in `node/node.go`
- [x] Replaced with `expectedRaw := expected.ConfigDB().ExportRaw()`

#### B12. Update public API boundary in `pkg/newtron/node.go`
- [x] Created `wrapConfigDB(deviceName string, db *sonic.ConfigDB) *ProvisioningInfo`
- [x] `Deliver` extracts `*sonic.ConfigDB` from `pi.internal` and calls `n.internal.Deliver(db, node.DeliveryMode(mode))`
- [x] `VerifyExpected` extracts `*sonic.ConfigDB` and calls `n.internal.VerifyExpected(ctx, db)`
- [x] Public `RegisterPort` now returns `error`

#### B13. Rename public API types in `pkg/newtron/types.go` and `node.go`
- [x] `CompositeInfo` → `ProvisioningInfo` (types.go)
- [x] `CompositeMode` → `DeliveryMode` (types.go)
- [x] `CompositeOverwrite` → `DeliveryOverwrite` (types.go)
- [x] `CompositeMerge` → `DeliveryMerge` (types.go)
- [x] `Node.DeliverComposite()` → `Node.Deliver()` (node.go)
- [x] `Node.VerifyComposite()` → `Node.VerifyExpected()` (node.go)
- [x] Updated `provision.go` — `wrapConfigDB(device, configDB)`
- [x] Updated `provision.go` — return type and internal call match new signature

#### B14. Update HTTP handlers
- [x] Renamed `api/handler_composite.go` → `api/handler_provisioning.go`
- [x] Renamed `handleCompositeGenerate` → `handleProvisioningGenerate`
- [x] Renamed `handleCompositeVerify` → `handleProvisioningVerify`
- [x] Renamed `handleCompositeDeliver` → `handleProvisioningDeliver`
- [x] Updated `api/types.go` wire types: `CompositeHandleRequest` → `ProvisioningHandleRequest`, `CompositeHandleResponse` → `ProvisioningHandleResponse` (JSON field names preserved)
- [x] Updated handler registration in `handler.go`

#### B15. Update client library
- [x] `GenerateComposite` → `GenerateProvisioning`
- [x] `VerifyComposite` → `VerifyExpected`
- [x] `DeliverComposite` → `DeliverProvisioning`
- [x] Return types updated to `ProvisioningHandleResponse`/`ProvisioningHandleRequest`

#### B16. Update CLI
- [x] `GenerateComposite` → `GenerateProvisioning`
- [x] `DeliverComposite` → `DeliverProvisioning`

#### B17. Update newtrun
- [x] `GenerateComposite` → `GenerateProvisioning`
- [x] `DeliverComposite` → `DeliverProvisioning`
- [x] `VerifyComposite` → `VerifyExpected`

#### B18. Delete dead code from `composite.go`
- [x] All deleted: `CompositeConfig`, `CompositeMetadata`, `CompositeBuilder`, `NewCompositeBuilder`, `SetGeneratedBy`, `AddEntries`, `AddEntry`, `Build`, `ToConfigChanges`, `ToEntries`
- [x] **Verify**: `grep -rn 'CompositeConfig\|CompositeBuilder\|CompositeMetadata' pkg/` — zero hits (only comment in configdb.go)

#### B19. Update `device_ops_test.go`
- [x] Replaced `BuildComposite()` + `.Tables` access with `ConfigDB()` + `ExportRaw()`

#### B-BUILD. Build + test after Part B
- [x] `go build ./...` passes
- [x] `go vet ./...` passes
- [x] `go test ./... -count=1` passes

---

### Phase C: Documentation + Final Verification

#### C1. Update pipeline.md
- [x] §3: Validate at applyShadow, remove Validate from Apply box
- [x] §5: Export pipeline — no CompositeBuilder step
- [x] §6: Delivery accepts `*ConfigDB`, calls `ExportEntries` internally
- [x] §9: Validation at shadow entry, not apply/delivery time

#### C2. Final verification
- [x] `go build ./... && go vet ./...` — clean
- [x] `go test ./... -count=1` — all pass
- [x] `grep -rn 'CompositeConfig\|CompositeBuilder\|CompositeMetadata' pkg/` — zero hits (non-comment)
- [x] `grep -rn 'applyShadow' pkg/newtron/network/node/ | grep -v _test.go | grep -v '//'` — 31 call sites
- [x] `grep -rn 'cs.Validate()' pkg/newtron/network/node/changeset.go` — none found (removed from Apply)
- [x] `grep -rn 'ExportRaw' pkg/newtron/device/sonic/` — definition + test + drift callers only
- [x] No function creates a ChangeSet with entries and returns without calling applyShadow (0 bare calls)

#### C3. Post-implementation conformance audit (ai-instructions §8)
- [x] "applyShadow validates before hydration" → every `applyShadow` call has error check (0 bare calls)
- [x] "Each CONFIG_DB table has exactly one owner" → no new table writers introduced
- [x] "Delivery reads abstract node directly" → no CompositeConfig/Builder in pipeline (0 hits)
- [x] "Same concept = same name" → internal `Deliver` matches public `Deliver`
- [x] "No intermediate representations" → configDB flows directly to delivery
