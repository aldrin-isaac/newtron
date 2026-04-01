# Topology/Intent API Restructure — Mode-Switching Abstract Node

## Context

After eliminating CompositeConfig, the handle-based provisioning endpoints
(`generate-composite`, `verify-composite`, `deliver-composite`) are dead weight.
The CLI nests topology operations under `intent topology`, confusing two
fundamentally different sources of truth. newtron is NOT an orchestrator — the
multi-device `POST /network/{netID}/provision` endpoint violates this boundary.

## Design

### Core Principle: configDB Is Always Expected State

The Node's configDB is always expected state — generated from intent records,
never loaded from actual CONFIG_DB. The mode determines where intents come from:

- **Offline** (topology/*): intents from topology.json steps → replayed into
  abstract node → configDB = expected state from topology spec
- **Online** (intent/*): intents from device's NEWTRON_INTENT records → replayed
  into abstract node → configDB = expected state from actuated intents

Same mechanism, different input. Precondition checking is always against intent
records — the typed configDB tables (VLAN, VRF, BGP_NEIGHBOR, etc.) ARE
populated from intent replay via `applyShadow`. Checking `configDB.VLAN[id]` IS
checking intent-derived state, not raw CONFIG_DB records.

Actual device state is always transient — read from Redis into local variables
for drift comparison, never stored on the Node.

### ConnectTransport — Wire Without State Overwrite

Current `Connect()`: SSH + Redis + **overwrite configDB from device** (line 426).
This is wrong in the new model — Connect must not overwrite expected state.

New `ConnectTransport(ctx)`: SSH + Redis, **configDB stays unchanged**.
Used by both online and offline nodes for device access.

```
Connect(ctx)          → conn ✓, configDB = actual      ← OLD, replaced
ConnectTransport(ctx) → conn ✓, configDB unchanged     ← NEW
```

### ConnectOnline — Transport + Intent Reconstruction

For online mode, the Node needs expected state from device intents. Flow:

1. `ConnectTransport(ctx)` — establish SSH + Redis
2. Read PORT table and NEWTRON_INTENT from `n.conn.ConfigDB` (the sonic.Device's cached actual state)
3. Register ports from step 2
4. `IntentsToSteps()` → `ReplayStep()` for each step → builds expected configDB via `applyShadow`
5. Node is now: `offline=false`, `connected=true`, configDB = expected from device intents

The sonic.Device's `ConfigDB` field holds actual state (from Redis GetAll during
connect). The Node's `configDB` field holds expected state (from intent replay).
These are separate — drift is the difference between them.

### Three Unified Operations

```go
// Tree builds intent DAG from configDB.NewtronIntent.
// Works in both modes — intents exist from topology replay (offline)
// or intent replay (online). Replaces Snapshot.
func (n *Node) Tree() TopologyDevice

// Drift compares expected vs actual CONFIG_DB on device.
// expected = n.configDB.ExportRaw() (always expected state)
// actual = n.conn.Client().GetRawOwnedTables(ctx) (fresh from Redis)
// ConnectTransport called internally if not connected.
func (n *Node) Drift(ctx context.Context) ([]sonic.DriftEntry, error)

// Reconcile delivers expected CONFIG_DB to device.
// ConnectTransport if needed → config reload → lock → deliver(overwrite)
// → save → ensure unified config → unlock. Replaces ProvisionDevice.
func (n *Node) Reconcile(ctx context.Context) (*DeliveryResult, error)
```

No `ExpectedConfigDB()` method needed — configDB IS expected in both modes.

### Mode Switching on NodeActor

```go
type NodeActor struct {
    net         *newtron.Network
    device      string
    idleTimeout time.Duration
    node        *newtron.Node   // online OR offline
    requests    chan request
    done        chan struct{}
}
```

- `ensureOnline(ctx)`: if offline → close → ConnectTransport → reconstruct from device intents → `offline=false`
- `ensureOffline()`: if online → close → create abstract node from topology replay → `offline=true`
- `getNode(ctx)` now calls `ensureOnline(ctx)` (existing write operations unchanged)
- Mode switches destroy previous node. SSH reconnection cost is acceptable.

### Lock and Refresh Changes

**Lock** (node.go line 522): Remove configDB refresh (lines 541-551). Keep lock
acquisition, legacy STATE_DB migration, zombie detection. These read from Redis
directly (not from configDB), so they work unchanged.

**Refresh**: Replace with `Ping(ctx)` — Redis PING for connectivity check. No
configDB overwrite. `connectAndRead` calls Ping instead of Refresh.

**Why this is safe**: Actor serialization ensures one NodeActor per device. After
write operations, `applyShadow(cs)` updates expected state. The cached expected
state is always current for the actor's own operations. External CONFIG_DB edits
are drift — detected by Drift(), fixed by Reconcile().

### URL Structure

```
GET  /node/{device}/topology/tree       → ensureOffline  → Tree()
GET  /node/{device}/topology/drift      → ensureOffline  → Drift(ctx)
POST /node/{device}/topology/reconcile  → ensureOffline  → Reconcile(ctx)

GET  /node/{device}/intent/tree         → ensureOnline   → Tree()         (EXISTS, keep)
GET  /node/{device}/intent/drift        → ensureOnline   → Drift(ctx)     (MOVE from /drift)
POST /node/{device}/intent/reconcile    → ensureOnline   → Reconcile(ctx)
```

Six endpoints, three operations, two modes.

### CLI Structure

```
newtron <device> topology tree
newtron <device> topology drift
newtron <device> topology reconcile [-x]

newtron <device> intent list
newtron <device> intent tree [resource-kind[:resource]] [--ancestors]
newtron <device> intent drift
newtron <device> intent reconcile [-x]
```

### What Gets Deleted

| Item | Why |
|------|-----|
| `POST /node/{device}/generate-composite` | Replaced by topology/reconcile |
| `POST /node/{device}/verify-composite` | Subsumed by topology/drift |
| `POST /node/{device}/deliver-composite` | Replaced by topology/reconcile |
| `POST /network/{netID}/provision` | Multi-device (newtrun's job) |
| `GET /node/{device}/topology/intents` | Redundant with topology/tree |
| `GET /node/{device}/drift` | Moved to `/intent/drift` |
| `handler_provisioning.go` (entire file) | Handle-based handlers |
| `ProvisioningHandleRequest/Response` | Wire types for handle pattern |
| Composite storage on NodeActor | Handle storage |
| `Node.BuildComposite`, `Node.Deliver`, `Node.VerifyExpected`, `wrapConfigDB` | Handle pattern public API |
| `ProvisioningInfo`, `DeliveryMode`, `DeliveryResult` (public types) | Handle pattern types |
| `ProvisionRequest`, `ProvisionResult`, `ProvisionDeviceResult` | Batch provision types |
| `provision.go` (entire file) | Dead after above deletions |
| `DetectDrift`, `DetectTopologyDrift` (`network.go`) | Replaced by Node.Drift via actor |
| `Node.DetectDrift`, `Node.Snapshot` | Replaced by unified Drift/Tree |
| `ProvisionDevice`, `DetectTopologyDrift`, `VerifyDeviceHealth` (`topology.go`) | Replaced by unified Reconcile/Drift |
| `DriftReport`, `HealthReport` (`topology.go`) | Replaced by DriftEntry/ReconcileResult |
| All client handle methods | Client handle pattern |
| `cmd_provision.go` (entire file) | Replaced by `topology reconcile` |

### newtrun Changes

| Old | New |
|-----|-----|
| `provisionExecutor`: `GenerateProvisioning` → `DeliverProvisioning` (2-step) | `TopologyReconcile(name)` — single call |
| `verifyProvisioningExecutor`: `VerifyExpected(name, handle)` | `TopologyDrift(name)` — check drift status |
| `Runner.Composites`, `StepOutput.Composites` | DELETE |
| `ActionProvision` = `"provision"` | `ActionTopologyReconcile` = `"topology-reconcile"` |
| `ActionVerifyProvisioning` = `"verify-provisioning"` | `ActionVerifyTopology` = `"verify-topology"` |

## Resolved Concerns

| Concern | Resolution |
|---------|-----------|
| **Preconditions need actual state** | No. Preconditions check intent-derived state (configDB populated by `applyShadow` during intent replay). `RequireVLANExists(id)` checks `configDB.VLAN` which IS populated from intent replay. |
| **Lock refresh removal** | Actor serialization: one NodeActor per device. After writes, `applyShadow` keeps expected state in sync. External edits are drift. |
| **Factory entries not in expected configDB** | `RemoveLegacyBGPEntries` is called only by `newtron init`. Factory entries are harmless (frrcfgd ignores bare-IP keys). First reconcile (DeliveryOverwrite) clears all owned tables including factory entries. |
| **ConnectTransport = dual state?** | No. One state (expected in configDB). Transport is a wire for reading actual from Redis. `n.conn.ConfigDB` holds actual (on sonic.Device), `n.configDB` holds expected (on Node) — separate fields, not dual state. |
| **Mode switch cost** | Destroys SSH tunnel. Acceptable — simplicity over performance. |
| **Stale expected state between requests** | Actor serializes all requests for a device. `applyShadow` updates expected after writes. On server restart, `ensureOnline` re-reads intents from device and reconstructs. |
| **Discovery scans on teardown** | `unbindQos` scans `configDB.Queue` — populated from intent replay (only newtron-created QoS). `UnconfigureIRB` scans `configDB.VLANInterface` — populated from intent replay. Shared-resource consumer counting uses intent-derived state, which is correct: newtron manages what it intends. |

## Files Changed

| File | Changes |
|------|---------|
| **`node/node.go`** | Add `ConnectTransport`, `ConnectOnline`, `Ping`, `Tree`, `Drift`, `Reconcile`; change `Lock` (remove configDB refresh); delete `Refresh`, `DetectDrift`, `Snapshot` |
| **`network/topology.go`** | Add `BuildAbstractNode(device) (*node.Node, error)` (extracts common logic from `GenerateDeviceComposite`); refactor `GenerateDeviceComposite` to use it; delete `ProvisionDevice`, `DetectTopologyDrift`, `VerifyDeviceHealth`, `DriftReport`, `HealthReport` |
| **`api/actors.go`** | Add `ensureOnline(ctx)`/`ensureOffline()`; add `doOffline`/`doOnline` helpers; refactor `getNode` → `ensureOnline`; refactor `connectAndRead` = `doOnline` + Ping; delete composite storage |
| **`api/handler.go`** | Delete 5 old routes, add 4 new routes, move 1 route |
| **`api/handler_provisioning.go`** | DELETE entire file |
| **`api/handler_node.go`** | Delete `handleTopologyIntents`, `handleDetectDrift`, `handleDetectTopologyDrift`; add `handleTopologyTree`, `handleTopologyDrift`, `handleTopologyReconcile`, `handleIntentDrift`, `handleIntentReconcile` |
| **`api/handler_network.go`** | Delete `handleProvisionDevices` |
| **`api/types.go`** | Delete `ProvisioningHandleRequest/Response` |
| **`pkg/newtron/network.go`** | Add `ConnectOnline(ctx, device)`, `BuildTopologyNode(device)`; delete `DetectDrift`, `DetectTopologyDrift` |
| **`pkg/newtron/node.go`** | Delete `BuildComposite`, `wrapConfigDB`, `Deliver`, `VerifyExpected`, `HealthCheck`; add `Tree`, `Drift`, `Reconcile` (public wrappers) |
| **`pkg/newtron/types.go`** | Delete `ProvisioningInfo`, `DeliveryMode`, `DeliveryResult`, `ProvisionRequest/Result`; add `ReconcileResult` |
| **`pkg/newtron/provision.go`** | DELETE entire file |
| **`client/network.go`** | Delete `GenerateProvisioning`, `VerifyExpected`, `DeliverProvisioning`, `ProvisionDevices` |
| **`client/node.go`** | Delete `DetectDrift`, `DetectTopologyDrift`, `TopologyIntents`; add `TopologyTree`, `TopologyDrift`, `TopologyReconcile`, `IntentDrift`, `IntentReconcile` |
| **`cmd/newtron/cmd_provision.go`** | DELETE entire file |
| **`cmd/newtron/cmd_intent.go`** | Remove topology subtree; add `intentDriftCmd`, `intentReconcileCmd` |
| **`cmd/newtron/cmd_topology.go`** | NEW: topology tree/drift/reconcile commands |
| **`cmd/newtron/main.go`** | Remove provisionCmd; add topologyCmd |
| **`pkg/newtrun/steps.go`** | Rewrite executors; delete Composites from StepOutput |
| **`pkg/newtrun/runner.go`** | Delete Runner.Composites |
| **`pkg/newtrun/scenario.go`** | Rename action constants |
| **`pkg/newtrun/newtrun_test.go`** | Update tests |

## Execution Order

### Phase 1: Internal — ConnectTransport + ConnectOnline + unified operations

1. Add `ConnectTransport(ctx)` to `node/node.go` — SSH+Redis, configDB unchanged
2. Add `ConnectOnline(ctx)` to `node/node.go` — ConnectTransport + read intents/ports from `n.conn.ConfigDB` + RegisterPort + IntentsToSteps + ReplayStep → configDB = expected
3. Add `Ping(ctx)` to `node/node.go` — Redis PING for connectivity check
4. Change `Lock()` — remove lines 541-551 (configDB refresh + interface rebuild). Keep lock acquisition (533-537), legacy migration (553-566), zombie check (568-583)
5. Add `Tree()` to `node/node.go` — read `n.configDB.NewtronIntent` → build intent DAG. Same logic as existing `Snapshot` but returns tree structure
6. Add `Drift(ctx)` to `node/node.go` — if not connected: `ConnectTransport(ctx)`. expected = `n.configDB.ExportRaw()`. actual = `n.conn.Client().GetRawOwnedTables(ctx)`. diff via `sonic.DiffConfigDB`
7. Add `Reconcile(ctx)` to `node/node.go` — if not connected: `ConnectTransport(ctx)`. Config reload (best-effort). PingWithRetry. Lock → Deliver(n.configDB, DeliveryOverwrite) → SaveConfig → Unlock. EnsureUnifiedConfigMode
8. Add `BuildAbstractNode(device) (*node.Node, error)` to `network/topology.go` — extract from `GenerateDeviceComposite` (returns Node, not just ConfigDB). Refactor `GenerateDeviceComposite` to call it: `n, err := tp.BuildAbstractNode(device); return n.ConfigDB(), err`
9. Build + test (new methods not yet wired to HTTP)

### Phase 2: NodeActor mode switching + public API

10. Add `ensureOnline(ctx)` to `actors.go` — if offline: close → `na.net.ConnectOnline(ctx, na.device)`. If nil: same. If already online: return
11. Add `ensureOffline()` to `actors.go` — if online: close → `na.net.BuildTopologyNode(na.device)`. Requires topology loaded
12. Refactor `getNode(ctx)` → call `ensureOnline(ctx)` (no behavior change for write operations since all cached nodes were online before)
13. Change `connectAndRead` — replace `node.Refresh(ctx)` with `node.Ping(ctx)`. On failure: `na.closeNode()` + return error
14. Add `doOffline(ctx, fn)` helper — `ensureOffline()` → `fn(na.node)`
15. Delete composite storage: `compositeEntry`, `composites`, `compositeMu`, `storeComposite`, `getComposite`, `removeComposite`, `compositeExpiry`
16. Add public methods to `pkg/newtron/network.go`: `ConnectOnline(ctx, device)`, `BuildTopologyNode(device)` — wrapping internal topology/node methods
17. Add public methods to `pkg/newtron/node.go`: `Tree()`, `Drift(ctx)`, `Reconcile(ctx)` — wrapping internal node methods
18. Add `ReconcileResult` to `pkg/newtron/types.go`
19. Delete from `pkg/newtron/node.go`: `BuildComposite`, `wrapConfigDB`, `Deliver`, `VerifyExpected`, `HealthCheck`
20. Delete from `pkg/newtron/types.go`: `ProvisioningInfo`, `DeliveryMode`, `DeliveryResult`, `ProvisionRequest/Result`
21. Delete `pkg/newtron/provision.go`
22. Delete from `pkg/newtron/network.go`: `DetectDrift`, `DetectTopologyDrift`
23. Delete from internal: `node/node.go` `DetectDrift`, `Snapshot`; `topology.go` `ProvisionDevice`, `DetectTopologyDrift`, `VerifyDeviceHealth`, `DriftReport`, `HealthReport`; `node/node.go` `Refresh`
24. Build + test

### Phase 3: HTTP layer

25. Add handlers to `handler_node.go`: `handleTopologyTree` (doOffline → Tree), `handleTopologyDrift` (doOffline → Drift), `handleTopologyReconcile` (doOffline → Reconcile + ExecOpts), `handleIntentDrift` (connectAndRead → Drift), `handleIntentReconcile` (doOnline → Reconcile + ExecOpts)
26. Update route table in `handler.go`
27. Delete `handler_provisioning.go`
28. Delete `handleProvisionDevices` from `handler_network.go`
29. Delete `handleDetectDrift`, `handleDetectTopologyDrift`, `handleTopologyIntents` from `handler_node.go`
30. Delete `ProvisioningHandleRequest/Response` from `api/types.go`
31. Build + test

### Phase 4: Client + CLI

32. Delete old client methods from `client/network.go` and `client/node.go`
33. Add new client methods to `client/node.go`: `TopologyTree`, `TopologyDrift`, `TopologyReconcile`, `IntentDrift`, `IntentReconcile`
34. Create `cmd/newtron/cmd_topology.go` (tree/drift/reconcile commands). Reuse `printDriftEntries`/`printIntentTree` from `cmd_intent.go`
35. Update `cmd/newtron/cmd_intent.go`: remove topology subtree, add `intentDriftCmd`, `intentReconcileCmd`
36. Delete `cmd/newtron/cmd_provision.go`
37. Update `cmd/newtron/main.go`: remove provisionCmd, add topologyCmd
38. Build + test

### Phase 5: newtrun

39. Rewrite `provisionExecutor` → `r.Client.TopologyReconcile(name)` per device
40. Rewrite `verifyProvisioningExecutor` → `r.Client.TopologyDrift(name)` per device
41. Delete `Runner.Composites`, `StepOutput.Composites`, composite merging in runner loop
42. Rename action constants: `provision` → `topology-reconcile`, `verify-provisioning` → `verify-topology`
43. Update 6 YAML scenario files
44. Update newtrun tests
45. Build + test

### Phase 6: Final verification

46. `go build ./... && go vet ./...` — clean
47. `go test ./... -count=1` — all pass
48. `grep -rn 'generate-composite\|verify-composite\|deliver-composite' pkg/ cmd/` → zero
49. `grep -rn 'ProvisioningInfo\|ProvisioningHandle\|ProvisionRequest' pkg/ cmd/` → zero
50. `grep -rn 'Composites' pkg/newtrun/` → zero
51. `grep -rn 'handleProvisionDevices\|handleProvisioningGenerate' pkg/` → zero
52. Tree, Drift, Reconcile each have exactly ONE implementation in `node/node.go`
53. `n.configDB` is never assigned from `n.conn.ConfigDB` (except in deleted code)
54. Post-implementation conformance audit (ai-instructions.md #8)

---

## Tracker

### Phase 1: Internal — ConnectTransport + ConnectOnline + unified operations

#### 1.1. Add `ConnectTransport` to `node/node.go`
- [ ] New method: SSH tunnel + Redis connection without configDB overwrite
- [ ] Similar to `connectWithOpts` but skips line 426 (`n.configDB = n.conn.ConfigDB`) and line 431 (`n.loadInterfaces()`)
- [ ] Sets `n.conn`, `n.connected = true`
- [ ] Keeps `n.configDB` and `n.interfaces` unchanged (they hold expected state from replay)

#### 1.2. Add `ConnectOnline` to `node/node.go`
- [ ] Calls `ConnectTransport(ctx)` to establish SSH+Redis
- [ ] Reads PORT and NEWTRON_INTENT from `n.conn.ConfigDB` (sonic.Device's actual state)
- [ ] Calls `RegisterPort` for each port
- [ ] `IntentsToSteps(intents)` → `ReplayStep(ctx, n, step)` for each step
- [ ] Sets `n.offline = false` (enables Lock/precondition checks for writes)
- [ ] After this: `n.configDB` = expected state from device intents, `n.conn` = transport

#### 1.3. Add `Ping` to `node/node.go`
- [ ] Redis PING via `n.conn.Client()` for connectivity check
- [ ] Returns error if connection is dead

#### 1.4. Change `Lock()` in `node/node.go`
- [ ] Remove lines 541-551: configDB refresh (`n.conn.Client().GetAll()`, `n.conn.ConfigDB = configDB`, `n.configDB = configDB`, interface rebuild)
- [ ] Keep lines 533-537: lock acquisition (`n.conn.Lock(holder, defaultLockTTL)`, `n.locked = true`)
- [ ] Keep lines 553-566: legacy STATE_DB intent migration (reads from Redis directly)
- [ ] Keep lines 568-583: zombie detection (reads from Redis directly)

#### 1.5. Add `Tree()` to `node/node.go`
- [ ] Read `n.configDB.NewtronIntent` → build intent DAG (same logic as `Snapshot`)
- [ ] Works in both modes: intents in configDB from topology replay (offline) or device intent replay (online)
- [ ] Returns tree representation (reuse `Snapshot`'s return type)

#### 1.6. Add `Drift(ctx)` to `node/node.go`
- [ ] If not connected (`n.conn == nil`): call `ConnectTransport(ctx)`
- [ ] expected: `n.configDB.ExportRaw()`
- [ ] actual: `n.conn.Client().GetRawOwnedTables(ctx)` (fresh from Redis)
- [ ] Return `sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())`

#### 1.7. Add `Reconcile(ctx)` to `node/node.go`
- [ ] If not connected: `ConnectTransport(ctx)`
- [ ] Config reload (best-effort, same pattern as `ProvisionDevice` lines 132-139)
- [ ] `PingWithRetry(ctx, timeout)` — poll `Ping()` until Redis is reachable after config reload. Replaces `RefreshWithRetry` which overwrites configDB. Config reload restarts SONiC services but Redis stays up, so connectivity should recover quickly.
- [ ] Lock → `Deliver(n.configDB, DeliveryOverwrite)` → `SaveConfig` → Unlock
- [ ] `EnsureUnifiedConfigMode(ctx)` (same as `ProvisionDevice` line 164)
- [ ] Return `(*DeliveryResult, error)`
- [ ] Add `PingWithRetry(ctx, timeout)` method: polls `Ping()` at 2s intervals until success or timeout

#### 1.8. Add `BuildAbstractNode` to `network/topology.go`
- [ ] Extract lines 55-98 from `GenerateDeviceComposite` into `BuildAbstractNode(device) (*node.Node, error)`
- [ ] Returns the Node (with configDB = expected from topology replay), not just ConfigDB
- [ ] Refactor `GenerateDeviceComposite`: `n, err := tp.BuildAbstractNode(device); return n.ConfigDB(), err`

#### 1.9. Build + test
- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] Existing tests pass (new methods not yet called by handlers)

### Phase 2: NodeActor mode switching + public API

#### 2.1. Add `ensureOnline(ctx)` to NodeActor in `actors.go`
- [ ] If `na.node != nil && !na.node.IsOffline()` → return (already online)
- [ ] If `na.node != nil` → close it (destroy any SSH connection)
- [ ] Call `na.net.ConnectOnline(ctx, na.device)` → cache as `na.node`

#### 2.2. Add `ensureOffline()` to NodeActor in `actors.go`
- [ ] If `na.node != nil && na.node.IsOffline()` → return (already offline)
- [ ] If `na.node != nil` → close it
- [ ] Call `na.net.BuildTopologyNode(na.device)` → cache as `na.node`
- [ ] Return error if no topology loaded

#### 2.3. Refactor `getNode(ctx)` → call `ensureOnline(ctx)`
- [ ] Replace body with `return na.ensureOnline(ctx)`
- [ ] `connectAndRead`, `connectAndExecute`, `connectAndLocked` unchanged (they call getNode)

#### 2.4. Change `connectAndRead` — Ping instead of Refresh
- [ ] Replace `node.Refresh(ctx)` with `node.Ping(ctx)`
- [ ] On Ping failure: `na.closeNode()` + return error (stale SSH tunnel)

#### 2.5. Add `doOffline` and `doOnline` helpers to NodeActor
- [ ] `doOffline(ctx, fn)`: `na.do(ctx, func() { na.ensureOffline(); fn(na.node) })` — used by topology handlers
- [ ] `doOnline(ctx, fn)`: `na.do(ctx, func() { na.ensureOnline(ctx); fn(na.node) })` — used by intent reconcile
- [ ] Refactor `connectAndRead` = `doOnline` + `Ping` (connectivity check before read operations)

#### 2.6. Delete composite storage
- [ ] Delete `compositeEntry` type, `compositeExpiry` const
- [ ] Delete `composites` field, `compositeMu` field from `NodeActor`
- [ ] Delete `storeComposite`, `getComposite`, `removeComposite`
- [ ] Remove `composites: make(...)` from `newNodeActor`

#### 2.7. Add public methods to `pkg/newtron/network.go`
- [ ] `ConnectOnline(ctx, device) (*Node, error)` — loads profile, resolves specs, creates internal node, calls `ConnectOnline(ctx)`, wraps as public Node
- [ ] `BuildTopologyNode(device) (*Node, error)` — creates TopologyProvisioner, calls `BuildAbstractNode(device)`, wraps as public Node

#### 2.8. Add public methods to `pkg/newtron/node.go`
- [ ] `Tree()` — wraps internal `n.Tree()`
- [ ] `Drift(ctx) ([]DriftEntry, error)` — wraps internal `n.Drift(ctx)`, converts sonic.DriftEntry → public DriftEntry
- [ ] `Reconcile(ctx) (*ReconcileResult, error)` — wraps internal, converts DeliveryResult → ReconcileResult
- [ ] `IsOffline() bool` — exposes internal `n.offline`

#### 2.9. Add types to `pkg/newtron/types.go`
- [ ] `ReconcileResult`: `Applied int`, `Message string`

#### 2.10. Delete handle-pattern methods from `pkg/newtron/node.go`
- [ ] `BuildComposite`, `wrapConfigDB`, `Deliver`, `VerifyExpected`, `HealthCheck`

#### 2.11. Delete handle-pattern types from `pkg/newtron/types.go`
- [ ] `ProvisioningInfo`, `DeliveryMode`, `DeliveryOverwrite`, `DeliveryMerge`, `DeliveryResult`
- [ ] `ProvisionRequest`, `ProvisionResult`, `ProvisionDeviceResult`

#### 2.12. Delete `pkg/newtron/provision.go`

#### 2.13. Delete old methods from `pkg/newtron/network.go`
- [ ] `DetectDrift`, `DetectTopologyDrift`

#### 2.14. Delete replaced methods from internal layer
- [ ] `node/node.go`: `DetectDrift`, `Snapshot`, `Refresh`
- [ ] `network/topology.go`: `ProvisionDevice`, `DetectTopologyDrift`, `VerifyDeviceHealth`, `DriftReport`, `HealthReport`

#### 2.15. Build + test
- [ ] `go build ./...` passes

### Phase 3: HTTP layer

#### 3.1. Add topology handlers to `handler_node.go`
- [ ] `handleTopologyTree`: `nodeActor.doOffline(ctx, func(n) { n.Tree() })`
- [ ] `handleTopologyDrift`: `nodeActor.doOffline(ctx, func(n) { n.Drift(ctx) })`
- [ ] `handleTopologyReconcile`: `nodeActor.doOffline(ctx, fn)` — if ExecOpts.Execute: `n.Reconcile(ctx)`; else: `n.Drift(ctx)` (dry-run returns what would change)

#### 3.2. Add intent handlers to `handler_node.go`
- [ ] `handleIntentDrift`: `nodeActor.connectAndRead(ctx, func(n) { n.Drift(ctx) })`
- [ ] `handleIntentReconcile`: `nodeActor.doOnline(ctx, fn)` — if ExecOpts.Execute: `n.Reconcile(ctx)`; else: `n.Drift(ctx)` (dry-run). Note: use `doOnline` not `connectAndLocked` — Reconcile handles its own Lock/Unlock internally. Lock is re-entrant (`if n.locked { return nil }`), Unlock is idempotent (`if !n.locked { return }`).

#### 3.3. Update route table in `handler.go`
- [ ] Delete: `generate-composite`, `verify-composite`, `deliver-composite`
- [ ] Delete: `POST /network/{netID}/provision`
- [ ] Delete: `GET /node/{device}/topology/intents`
- [ ] Delete: `GET /node/{device}/drift`
- [ ] Delete: `GET /node/{device}/topology/drift` (old handler)
- [ ] Add: `GET /node/{device}/topology/tree` → `handleTopologyTree`
- [ ] Add: `GET /node/{device}/topology/drift` → `handleTopologyDrift`
- [ ] Add: `POST /node/{device}/topology/reconcile` → `handleTopologyReconcile`
- [ ] Add: `GET /node/{device}/intent/drift` → `handleIntentDrift`
- [ ] Add: `POST /node/{device}/intent/reconcile` → `handleIntentReconcile`
- [ ] Keep: `GET /node/{device}/intent/tree` (update to use new Tree())

#### 3.4. Delete old handlers
- [ ] Delete `handler_provisioning.go` (entire file)
- [ ] Delete `handleProvisionDevices` from `handler_network.go`
- [ ] Delete `handleDetectDrift`, `handleDetectTopologyDrift`, `handleTopologyIntents` from `handler_node.go`

#### 3.5. Delete wire types
- [ ] Delete `ProvisioningHandleRequest`, `ProvisioningHandleResponse` from `api/types.go`

#### 3.6. Build + test
- [ ] `go build ./...` passes

### Phase 4: Client + CLI

#### 4.1. Delete old client methods
- [ ] From `client/network.go`: `GenerateProvisioning`, `VerifyExpected`, `DeliverProvisioning`, `ProvisionDevices`
- [ ] From `client/node.go`: `DetectDrift`, `DetectTopologyDrift`, `TopologyIntents`

#### 4.2. Add new client methods to `client/node.go`
- [ ] `TopologyTree(device) → GET /topology/tree`
- [ ] `TopologyDrift(device) → GET /topology/drift`
- [ ] `TopologyReconcile(device) → POST /topology/reconcile`
- [ ] `IntentDrift(device) → GET /intent/drift`
- [ ] `IntentReconcile(device) → POST /intent/reconcile`

#### 4.3. Create `cmd/newtron/cmd_topology.go`
- [ ] `topologyCmd` — parent command
- [ ] `topologyTreeCmd` — calls `TopologyTree(device)`, renders intent tree
- [ ] `topologyDriftCmd` — calls `TopologyDrift(device)`, renders drift report
- [ ] `topologyReconcileCmd` — calls `TopologyReconcile(device)`, renders result
- [ ] Reuse `printDriftEntries` and `printIntentTree` from `cmd_intent.go`

#### 4.4. Update `cmd/newtron/cmd_intent.go`
- [ ] Remove `intentTopologyCmd` and subcommands
- [ ] Add `intentDriftCmd` — calls `IntentDrift(device)`, reuses `printDriftEntries`
- [ ] Add `intentReconcileCmd` — calls `IntentReconcile(device)`, renders result
- [ ] Update init(): remove `intentTopologyCmd`, add `intentDriftCmd`, `intentReconcileCmd`

#### 4.5. Delete `cmd/newtron/cmd_provision.go`

#### 4.6. Update `cmd/newtron/main.go`
- [ ] Remove `provisionCmd` from command list and `addWriteFlags`
- [ ] Add `topologyCmd` to command list and `addWriteFlags`

#### 4.7. Build + test
- [ ] `go build ./...` passes

### Phase 5: newtrun

#### 5.1. Rewrite `provisionExecutor`
- [ ] Single call: `r.Client.TopologyReconcile(name)` per device via `executeForDevices`
- [ ] Return `ReconcileResult.Applied` in message

#### 5.2. Rewrite `verifyProvisioningExecutor`
- [ ] `r.Client.TopologyDrift(name)` per device via `checkForDevices`
- [ ] Zero drift entries → pass; any drift → fail with details

#### 5.3. Delete Composites
- [ ] Delete `Runner.Composites` from `runner.go`
- [ ] Delete `StepOutput.Composites` from `steps.go`
- [ ] Remove composite merging from runner loop
- [ ] Remove `r.Composites = make(...)` initialization

#### 5.4. Rename action constants in `scenario.go`
- [ ] `ActionProvision` → `ActionTopologyReconcile` (`"topology-reconcile"`)
- [ ] `ActionVerifyProvisioning` → `ActionVerifyTopology` (`"verify-topology"`)
- [ ] Update executors map registration

#### 5.5. Update 6 YAML scenario files
- [ ] `newtrun/suites/2node-ngdp-service/02-provision.yaml` → `topology-reconcile`
- [ ] `newtrun/suites/2node-vs-service/02-provision.yaml` → `topology-reconcile`
- [ ] `newtrun/suites/2node-vs-drift/02-provision.yaml` → `topology-reconcile`
- [ ] `newtrun/suites/2node-vs-drift/10-reprovision.yaml` → `topology-reconcile`
- [ ] `newtrun/suites/2node-vs-zombie/02-provision.yaml` → `topology-reconcile`
- [ ] `newtrun/suites/3node-ngdp-dataplane/01-provision.yaml` → `topology-reconcile`

#### 5.6. Update newtrun tests
- [ ] Remove `Composites: make(map[string]string)` from test Runner init
- [ ] Delete `TestRunScenarioSteps_InitComposites`
- [ ] Update action name references

#### 5.7. Build + test
- [ ] `go build ./...` passes
- [ ] `go test ./... -count=1` passes

### Phase 6: Final verification

- [ ] `go build ./... && go vet ./...` — clean
- [ ] `go test ./... -count=1` — all pass
- [ ] `grep -rn 'generate-composite\|verify-composite\|deliver-composite' pkg/ cmd/` → zero
- [ ] `grep -rn 'ProvisioningInfo\|ProvisioningHandle\|ProvisionRequest' pkg/ cmd/` → zero
- [ ] `grep -rn 'Composites' pkg/newtrun/` → zero
- [ ] `grep -rn 'handleProvisionDevices\|handleProvisioningGenerate' pkg/` → zero
- [ ] Tree, Drift, Reconcile each have exactly ONE implementation in `node/node.go`
- [ ] `n.configDB` is never assigned from `n.conn.ConfigDB` (except in deleted code)
- [ ] Post-implementation conformance audit (ai-instructions.md #8):
  - [ ] "Composites MUST call owning primitives" — no composites remain
  - [ ] "Each CONFIG_DB table MUST have exactly one owner" — unchanged
  - [ ] "For every forward action there MUST be a reverse action" — Tree/Drift/Reconcile are read/compare/fix, not forward/reverse
  - [ ] No mode-specific code paths outside `ExpectedConfigDB`/`ensureOnline`/`ensureOffline`
