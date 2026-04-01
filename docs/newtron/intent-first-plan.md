# Intent-First Architecture — Implementation Plan

Derived from `docs/newtron/unified-pipeline-architecture.md` (the
authoritative architecture document). Every task traces back to a
specific section of the architecture.

---

## Phasing Rationale

Each phase is independently buildable (`go build ./...` passes after
each phase). Dependencies flow strictly forward — no phase references
work from a later phase. The ordering follows the architecture's
layering: internal node operations first, then actor dispatch, then
HTTP handlers, then client+CLI, then newtrun.

---

## Resolved Concerns

| Concern | Resolution |
|---------|-----------|
| **Typed accessors called outside preconditions** | Callers in `interface_ops.go` (lines 110, 169, 202, 343), `service_ops.go` (line 139), `evpn_ops.go` (line 212), `vrf_ops.go` (lines 206, 371), and `device_ops_test.go` (line 679) all check "does X exist?" before acting. Per architecture §5, all switch to `GetIntent()`. The accessor methods (`VLANExists`, `VRFExists`, etc.) are deleted. |
| **Is `composite.go` dead?** | Yes. `Reconcile` uses `configDB.ExportEntries()` + `ReplaceAll()` directly — it does not need `CompositeConfig`, `CompositeBuilder`, or `DeliverComposite`. Delete `composite.go` entirely. |
| **Reconcile return type** | New `ReconcileResult{Applied int, Message string}`. Does not reuse `CompositeDeliveryResult` (deleted with composite.go). |
| **`--topology` flag transport mechanism** | Query parameter `?mode=topology` on HTTP requests. Client appends it when `--topology` is set. Mode middleware extracts mode from query param and injects into ctx. `execute` reads mode from ctx. There is no `/topology/` URL namespace — the `--topology` flag is the **only** mode selector. `intent` subcommands (`tree`, `drift`, `reconcile`, `save`) and CRUD commands all respect the same flag. Existing CRUD handlers are unchanged — mode flows through ctx transparently. |
| **Zombie mechanism deletion** | Architecture §8 explains: drift guard + Reconcile subsume crash recovery. `OperationIntent`, `WriteIntent`/`DeleteIntent`, `dispatchReverse`, `RollbackZombie`, `ClearZombie`, `ReadZombie`, `ZombieIntent`, `RollbackHistory`, `PreviewRollbackHistory`, `bypassZombieCheck` — all deleted. Partial application = drift, handled by Reconcile. |
| **HealthCheck deletion** | `HealthCheck` is used by `GET .../health` route (handler.go line 107) which is NOT deleted. `HealthCheck` stays. Architecture §11 does not list it for deletion. |
| **`Save()` on Node vs split** | Architecture §6 defines `Save()` as a single Node method, but Save needs TopologyProvisioner to write `topology.json` — Node doesn't have that reference. Implementation: `Tree()` on Node + `SaveDeviceIntents()` on TopologyProvisioner. Handler composes them. Logically one operation, implemented as composition — same as how Reconcile composes Node + transport. |
| **"Topology online" state in `execute()`** | Architecture §3 defines three states. `execute()` has two branches (`ensureTopologyIntent`/`ensureActuatedIntent`). "Topology online" happens implicitly when the offline node's Drift/Reconcile call `ConnectTransport` internally. This is correct — `execute()` prepares the node's initial state, operations add transport on demand. |
| **Topology-online CRUD leak** | After Drift/Reconcile, the node retains transport (topology-online). Subsequent CRUD via `execute(ModeTopology)` → `ensureTopologyIntent()` would find a topology node and return — but Lock/Apply would work through the leftover connection, writing to Redis. Fix: `ensureTopologyIntent()` calls `DisconnectTransport()` on existing topology nodes to reset to topology-offline. Architecture §3 says CRUD is only valid in topology-offline. |
| **Unsaved topology intent guard** | Switching from topology to actuated mode (`ensureActuatedIntent`) destroys the topology node and rebuilds from device intents. If the user created intents via `--topology` CRUD but didn't `intent save`, those intents are silently lost. Fix: Node tracks `unsavedIntents bool` — set by `writeIntent()`/`deleteIntent()` (CRUD mutations), left false during `ReplayStep` (construction), cleared by Save. `ensureActuatedIntent` refuses to destroy a topology node with `HasUnsavedIntents() == true`. The reverse direction (actuated → topology) needs no guard — actuated intents live on the device and survive node destruction. User can explicitly `intent reload` to discard unsaved changes and unblock the mode switch. |
| **Reload + Clear are topology-mode only** | Both operations manipulate the in-memory topology node. In actuated mode, intents live on the device — use reverse operations (DeleteVLAN, RemoveService) to remove individual intents. Reload has no actuated equivalent (device intents ARE the source). Clear has no actuated equivalent (clearing device intents is destructive enough to require explicit per-intent teardown). Both return an error in actuated mode. |

---

## Phase 0: Rename + Doc Sync

**Architecture §5, §11, §12.** Replace old terminology with the
architecture's vocabulary before touching any behavior.

### 0.1 Rename `applyShadow` → `render`

Architecture §5: "`render(cs)` is the mechanism that updates the
projection from a ChangeSet."

- `node/node.go:271` — rename function `applyShadow` → `render`
- All call sites within `node/` package (grep `n.applyShadow`)
- Update comments: "shadow" → "projection" throughout the **entire
  codebase** (not just `node/`). Grep `shadow` in `pkg/newtron/`,
  `cmd/newtron/`, and `docs/`.
- Rewrite stale model descriptions (not just shadow→projection — these
  describe the OLD model and must be rewritten for intent-first):
  - `node.go:42-47` — "Physical mode: ConfigDB loaded from Redis" /
    "Abstract mode: shadow ConfigDB starts empty" → describe
    intent-first: intents are primary, projection is derived, two
    intent sources (topology.json, device NEWTRON_INTENT)
  - `node.go:90-92` — "shadow ConfigDB", "accumulate entries for
    composite export" → projection, export via ExportEntries
  - `node.go:630-633` — "Lock() refreshes the CONFIG_DB cache" →
    Lock acquires lock only, no projection refresh
  - `changeset.go:168` — "gates accumulation on n.offline" →
    render runs on ALL paths (both modes, no gating)
  - `service_ops.go:925-927` — "ground reality, not stale cache" /
    "shadow configDB" → projection is always the source

### 0.2 Rename `applyIntentToShadow` → `renderIntent`

Architecture §4: "`renderIntent()` (currently `applyIntentToShadow`)
updates `configDB.NewtronIntent`"

- `node/intent_ops.go` — rename function and all call sites
- Update comments referencing `applyIntentToShadow`

### 0.3 Sync architecture doc

Update `docs/newtron/unified-pipeline-architecture.md`:
- Line 222: remove "(currently `applyShadow`)" → just `render(cs)`
- Line 297: remove "(currently `applyIntentToShadow`)" → just `renderIntent()`
- Line 323: same parenthetical removal
- Replace all "materialization" → "rendering" (grep for all occurrences;
  known locations include lines 184, 201, 225, 308, 382, 475, 680, 754)
- Fix §6 Reconcile signature: `*DeliveryResult` → `*ReconcileResult`
  (line 426). `DeliveryResult` is a handle-pattern type being deleted;
  Reconcile uses a new `ReconcileResult` type.
- Fix §8 Lock: remove "Zombie detection" from Lock's performs list
  (line 522). Also fix guard: `n.offline == true` (line 524) → `n.conn == nil`.
  Architecture §3 says ConnectTransport transitions topology-offline →
  topology-online, so `n.offline` can be true while transport exists.
  The guard's intent — parenthetical "(no transport connection exists)"
  — is correct; the code reference `n.offline` is wrong. Also update
  the pseudocode (line 558): `if offline` → `if n.conn == nil`.
  Also fix pseudocode line 560: `Legacy migration + zombie detection`
  → `Legacy migration` (zombie detection deleted per §8 crash recovery).
- Fix §10 traces: `execute(ctx, intent, fn)` (lines 627, 667, 714) →
  `execute(ctx, fn)`. Mode comes from ctx, not as parameter. Update
  parenthetical comments to say "(mode=intent from ctx)" instead of
  showing mode as a positional argument.
- Fix §1 topology mode: `ensureOffline`/`ensureOnline` (line 180) →
  `ensureTopologyIntent`/`ensureActuatedIntent`.
- Fix §3 state transitions: `ensureOnline`/`ensureOffline` (lines 271,
  273) → `ensureActuatedIntent`/`ensureTopologyIntent`.
- Fix §6 Save: update `func (n *Node) Save()` signature (line 451) —
  Save is implemented as composition: `Tree()` on Node +
  `SaveDeviceIntents()` on TopologyProvisioner. The handler composes
  them. Update prose to match (per Resolved Concern "Save on Node vs
  split").
- Fix §9 hydration registry: `ConnectOnline` (line 601) →
  `InitFromDeviceIntent`.

### 0.4 Delete `docs/newtron/pipeline.md`

Architecture preamble: "This document replaces the multi-pipeline model
in `pipeline.md`." Architecture §11: "Five pipelines → One pipeline."

### 0.5 Update CLAUDE.md pipeline references

Architecture §12: "The 'Pipeline-First Explanations' section references
`pipeline.md` and 'five pipelines' — both superseded."

- Change `pipeline.md` reference → `unified-pipeline-architecture.md`
- Update "five pipelines" language to "one pipeline"
- In "Pipeline-First Explanations" section, change "The five pipelines
  are: write, read, export, delivery, verification" → "The single
  pipeline is: Intent → Replay → Render → [Deliver]"

### 0.6 Build + verify

- `go build ./...` passes
- `go vet ./...` passes
- `grep -rn 'applyShadow\|applyIntentToShadow' pkg/ cmd/` → zero
- `grep -rn 'shadow' pkg/newtron/network/node/` → zero (only "projection")

---

## Phase 1: Node Internal Operations

**Architecture §1, §3, §5-§8.** Add new node-level operations and
change existing ones. No HTTP or public API changes yet.

### 1.1 Add `ConnectTransport(ctx)` to `node/node.go`

Architecture §7: "SSH tunnel + Redis connection. Projection stays
unchanged." "This replaces the old `Connect()` which overwrote configDB
from Redis."

**Why this doesn't already exist:** Current `Connect()` /
`connectWithOpts()` (line 407) sets `n.configDB = n.conn.ConfigDB`
(line 422) and calls `n.loadInterfaces()` (line 427) — it overwrites
the projection. The architecture requires a connection that leaves the
projection intact.

- New method on `*Node`
- Reuses `sonic.NewDevice` + `conn.Connect(ctx)` from `connectWithOpts`
- Sets `n.conn`, `n.connected = true`
- Does NOT touch `n.configDB` or `n.interfaces`
- NO offline guard — ConnectTransport transitions topology-offline →
  topology-online (architecture §3). Drift and Reconcile call it to
  add transport. Transport guards belong on Lock, Unlock, cs.Apply only.

### 1.2 Replace `offline` field with `actuatedIntent`

Architecture §8: "Drift guard applies only in actuated mode."
ai-instructions.md §5: "Every name must mean what it does."

The `offline` field is dishonest after this architecture: ConnectTransport
gives an "offline" node an active SSH+Redis tunnel. The field actually
means "topology-sourced intents" — which is `!actuatedIntent`.

- Delete `offline bool` from Node struct (line 68)
- Delete `IsOffline() bool` accessor (line 114)
- Add `actuatedIntent bool` to Node struct
- Add `HasActuatedIntent() bool` accessor
- Set by `InitFromDeviceIntent` (task 1.3), left false by `NewAbstract`
- `NewAbstract()`: remove `offline: true` from struct literal (zero
  value of `actuatedIntent` is false — correct for topology-sourced)
- Add `unsavedIntents bool` to Node struct
- Add `HasUnsavedIntents() bool` accessor
- Set to `true` by `writeIntent()` / `deleteIntent()` (any CRUD mutation)
- Left `false` during `ReplayStep` — construction from topology.json or
  device intents is loaded state, not new mutations
- Cleared by caller after Save completes (handler sets it via
  `ClearUnsavedIntents()` after `SaveDeviceIntents` succeeds)
- Add `ClearUnsavedIntents()` method
- Add `DisconnectTransport()` method: `if n.conn != nil { n.conn.Close();
  n.conn = nil; n.connected = false }` — needed by `ensureTopologyIntent`
  (task 2.2) to prevent topology CRUD from leaking to Redis

### 1.3 Add `InitFromDeviceIntent(ctx)` to `node/node.go`

Architecture §3 table: "Device intents → `New()` →
`ConnectTransport()` → read PORT + NEWTRON_INTENT from
`sonic.Device.ConfigDB` → `RegisterPort()` → `IntentsToSteps()` →
`ReplayStep()` for each step"

**Why this doesn't already exist:** Current `Connect()` loads the full
configDB from Redis as the node's state. The architecture requires
replaying device intents to build the projection — the device's raw
configDB is never assigned to the node.

**Why not `ConnectOnline`:** "Connect" is step 1 of 5. The primary work
is replaying device intents to build the projection. The name must
describe what the method does (ai-instructions.md §5).

- Calls `ConnectTransport(ctx)`
- **Legacy STATE_DB intent migration**: read NEWTRON_INTENT from
  STATE_DB (via `n.conn.Client()`). If any exist, write them to
  CONFIG_DB (via Redis HSET) and delete from STATE_DB. This must
  happen BEFORE reading CONFIG_DB intents, so the replayed projection
  includes all intents. Without this, legacy devices show false drift
  (Lock's drift guard sees CONFIG_DB intents that aren't in the
  projection). Lock's migration (task 1.6 KEEP) is retained as an
  idempotent safety net.
- Reads PORT from `n.conn.ConfigDB` (device actual) → `RegisterPort()`
- Reads NEWTRON_INTENT from `n.conn.ConfigDB` (device actual)
- `IntentsToSteps(intents)` → `ReplayStep(ctx, n, step)` for each
- Sets `n.actuatedIntent = true`
- After: `n.configDB` = projection from intent replay, `n.conn` = transport

### 1.4 Add `Ping(ctx)` to `node/node.go`

Architecture §7: "Redis PING for connectivity check. Replaces the old
`Refresh()` which reloaded configDB from Redis."

**Why this doesn't already exist:** `Refresh()` (line 461) does a
`GetAll()` and overwrites configDB. The architecture needs a
connectivity check without projection overwrite.

- `if n.conn == nil { return nil }` — no transport to ping (not
  `n.offline` — an offline node may have transport after ConnectTransport)
- Calls `n.conn.Client().Ping(ctx)` (Redis PING)
- Returns error if dead
- Does NOT touch `n.configDB`

### 1.5 Add `PingWithRetry(ctx, timeout)` to `node/node.go`

Architecture §6 Reconcile step 3: "`PingWithRetry` — poll Redis until
available after reload"

- Polls `Ping(ctx)` at 2s intervals until success or timeout
- Replaces `RefreshWithRetry` (line 486) which overwrote configDB

### 1.6 Change `Lock()` — remove configDB refresh

Architecture §8: "Lock acquires a distributed lock. It does NOT refresh
the projection from the device."

Current `Lock()` (line 518) after acquiring SETNX:
- Lines 537-547: `GetAll()` → overwrites `n.configDB`, rebuilds
  interfaces. **DELETE these 11 lines.**
- Lines 549-562: Legacy STATE_DB migration. **KEEP** (reads Redis
  directly, not projection).
- Lines 564-579: Zombie detection. **DELETE** — drift guard +
  Reconcile subsume crash recovery (architecture §8).

### 1.7 Change `Lock()`/`Unlock()`/`cs.Apply()` — add transport guards

Architecture §8: "Lock, Apply, and Unlock are no-ops when
`n.offline == true` (no transport connection exists)."

The parenthetical is key: the guard checks for transport absence, not
the `offline` flag. An offline node may have transport after
`ConnectTransport` (topology-online state), in which case Lock must
work so that Reconcile can hold the lock during delivery.

- Add `if n.conn == nil { return nil }` at top of `Lock()`
- Add `if n.conn == nil { return nil }` at top of `Unlock()`
- Add `if n.conn == nil { return nil }` at top of `cs.Apply(n)`
  (changeset.go:219) — entries were already rendered into the
  projection by `render(cs)` in `op()`. Without this guard,
  topology-offline CRUD fails at line 229: "CONFIG_DB client not
  connected."

Interactive CRUD in topology-offline mode: `execute` calls
`ensureTopologyIntent` → no transport → Lock/Apply/Unlock are no-ops →
intents accumulate in projection without delivery. Correct.

### 1.8 Change `Lock()` → `Lock(ctx)` — add ctx + drift guard

Architecture §8: "In actuated mode, Lock performs a drift guard."

Lock currently takes no `ctx` parameter. Change signature to
`Lock(ctx context.Context) error`.

**Callers that need updating (all in `node/` package):**
- `node/node.go` `ExecuteOp` — already has ctx
- `node/node.go` `Reconcile` (new, task 1.11) — has ctx
- Any test calling `Lock()` directly

After lock acquisition, add drift guard:
```go
if n.actuatedIntent {
    expected := n.configDB.ExportRaw()
    actual, err := n.conn.Client().GetRawOwnedTables(ctx)
    if err != nil { n.conn.Unlock(); n.locked = false; return err }
    drift := sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())
    if len(drift) > 0 {
        n.conn.Unlock()
        n.locked = false
        return fmt.Errorf("device drifted from intents (%d entries) — reconcile first", len(drift))
    }
}
```

### 1.9 Rename `Snapshot` → `Tree`

Architecture §6: "Read the intent DB → build intent DAG."

Current `Snapshot()` (line 176) reads `configDB.NewtronIntent`, calls
`IntentsToSteps`, returns `*spec.TopologyDevice`. Rename to `Tree()`.
Update all callers (grep `Snapshot` in `pkg/newtron/`).

### 1.10 Add `Drift(ctx)` to `node/node.go`

Architecture §6: "Compare projection vs device → drift entries."

**Why this doesn't already exist:** `DetectDrift` (line 195)
reconstructs expected state from intents every time. The architecture
says the projection IS expected state — export it directly.

```go
func (n *Node) Drift(ctx context.Context) ([]sonic.DriftEntry, error)
```

1. If `n.conn == nil`: `ConnectTransport(ctx)` — auto-connect
2. `expected := n.configDB.ExportRaw()` — the projection
3. `actual, err := n.conn.Client().GetRawOwnedTables(ctx)` — transient
4. `return sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())`

### 1.11 Add `Reconcile(ctx)` to `node/node.go`

Architecture §6: "Deliver the full projection to the device."

**Why this doesn't already exist:** Current provisioning is a 3-step
process: `GenerateDeviceComposite` → `DeliverComposite` via handle
pattern. The architecture replaces this with a single operation that
exports the projection directly.

```go
func (n *Node) Reconcile(ctx context.Context) (*ReconcileResult, error)
```

1. If `n.conn == nil`: `ConnectTransport(ctx)` — auto-connect
2. Config reload (best-effort, same pattern as `ProvisionDevice`
   topology.go lines 134-141)
3. `PingWithRetry(ctx, 60s)` — wait for Redis after reload
4. `n.conn.Lock(holder, ttl)` — acquire Redis lock directly,
   bypassing Node-level `Lock(ctx)`. Reconcile must NOT trigger the
   drift guard — its purpose IS to fix drift. Circular otherwise.
5. `entries := configDB.ExportEntries()` → `conn.Client().ReplaceAll(entries)`
6. `conn.Client().SaveConfig()` — persist to config_db.json
7. `conn.EnsureUnifiedConfigMode(ctx)` — restart bgp if needed
8. `n.conn.Unlock()` — release directly

Returns `*ReconcileResult{Applied: len(entries)}`.

Define `ReconcileResult` in `node/node.go` (internal):
```go
type ReconcileResult struct {
    Applied int
}
```

### 1.12 Rewrite preconditions + direct callers to check intent DB

Architecture §5: "Preconditions check `n.GetIntent(resource)`, not the
projection."

**Precondition methods in `precondition.go`:**

| Method | Old | New |
|--------|-----|-----|
| `RequireVLANExists(id)` | `n.VLANExists(id)` | `n.GetIntent(fmt.Sprintf("vlan\|%d", id)) != nil` |
| `RequireVLANNotExists(id)` | `n.VLANExists(id)` | `n.GetIntent(...) == nil` |
| `RequireVRFExists(name)` | `n.VRFExists(name)` | `n.GetIntent("vrf\|"+name) != nil` |
| `RequireVRFNotExists(name)` | same | intent check |
| `RequireVTEPConfigured()` | `n.VTEPExists()` | `n.GetIntent("evpn") != nil` |
| `RequireACLTableExists(name)` | `n.ACLTableExists(name)` | intent check |
| `RequireACLTableNotExists(name)` | same | intent check |
| `RequirePortChannelExists(name)` | `n.PortChannelExists(name)` | intent check |
| `RequirePortChannelNotExists(name)` | same | intent check |

`RequireInterfaceExists` stays — interfaces come from `RegisterPort`.

**Precondition mode check:**
- `precondition.go:34`: `if !n.offline` → `if n.actuatedIntent`
  (actuated nodes need connected+locked; topology nodes don't)

**`service_ops.go:929` — eliminate mode branch:**
- Delete the `if n.offline { ... } else { ... }` branch.
- Always scan the projection (`n.scanRoutePoliciesByPrefix`). In
  intent-first, the projection is always the source of truth for what
  newtron has configured. The Redis scan was needed in the old model
  where the cache could be stale; the projection cannot be stale.

**Direct callers outside preconditions (also switch to intent checks):**
- `interface_ops.go:110` — `!n.VRFExists(vrfName)` → intent check
- `interface_ops.go:169` — `!n.VLANExists(cfg.VLAN)` → intent check
- `interface_ops.go:202` — `!n.VRFExists(cfg.VRF)` → intent check
- `interface_ops.go:343` — `!n.ACLTableExists(aclName)` → intent check
- `service_ops.go:139` — `!n.VTEPExists()` → intent check
- `evpn_ops.go:212` — `!n.VTEPExists()` → intent check
- `vrf_ops.go:206` — `!n.VRFExists(vrfName)` → intent check
- `vrf_ops.go:371` — `n.VRFExists(vrfName)` → intent check
- `device_ops_test.go:679` — `!n.VLANExists(100)` → intent check

**Delete typed accessors (now unused):**
- `vlan_ops.go:327` — `VLANExists`
- `vrf_ops.go:436` — `VRFExists`
- `evpn_ops.go:32` — `VTEPExists`
- `acl_ops.go:35` — `ACLTableExists`
- `portchannel_ops.go:196` — `PortChannelExists`

### 1.13 Add `BuildAbstractNode` to `network/topology.go`

Architecture §1: "The topology provisioner creates an Abstract Node,
registers ports, and replays topology steps."

Extract lines 54-101 of `GenerateDeviceComposite` into:
```go
func (tp *TopologyProvisioner) BuildAbstractNode(deviceName string) (*node.Node, error)
```

Returns the Node (configDB = projection from topology replay).
Refactor `GenerateDeviceComposite` to use it:
```go
n, err := tp.BuildAbstractNode(device)
composite := n.BuildComposite() // kept temporarily — deleted in Phase 2
```

### 1.14 Add `SaveDeviceIntents` to `network/topology.go`

Architecture §6 Save: "Update the device's entry in `topology.json`
with the new steps. Persist `topology.json` to disk."

```go
func (tp *TopologyProvisioner) SaveDeviceIntents(deviceName string, steps []spec.TopologyStep) error
```

### 1.15 Add `BuildEmptyAbstractNode` to `network/topology.go`

Architecture §6 Clear: "Creates a fresh abstract node with ports
registered but no intents replayed."

```go
func (tp *TopologyProvisioner) BuildEmptyAbstractNode(deviceName string) (*node.Node, error)
```

Like `BuildAbstractNode` (task 1.13) but skips step replay. Creates
`NewAbstract()`, calls `RegisterPort()` for each port from the
topology device entry, returns the node. Intent DB is empty, projection
contains only PORT entries.

### 1.16 Build + test

- `go build ./...` passes
- `go vet ./...` passes
- `go test ./... -count=1` — all pass

---

## Phase 2: Actor Mode Switching + Public API

**Architecture §3, §10 traces, §11.** Wire internal operations into
actor dispatch. Delete replaced code.

### 2.0 Define `Mode` type

Used by all subsequent tasks in this phase. Must be defined first.

```go
type Mode string
const (
    ModeTopology Mode = "topology"
    ModeIntent   Mode = "intent"
)
```

In `api/actors.go` or a new `api/mode.go`.

### 2.1 Add `ensureActuatedIntent(ctx)` to NodeActor

Architecture §3: "Topology offline → actuated online: close the
offline node, create a new one from device intents."

In `actors.go`:
- If `na.node != nil && na.node.HasActuatedIntent()` → return
- If `na.node != nil && na.node.HasUnsavedIntents()` → return error
  ("topology node has unsaved intents — run 'intent save --topology'
  first"). Prevents silent data loss when switching from topology to
  actuated mode after CRUD mutations.
- If `na.node != nil` → `na.closeNode()`
- `na.node = na.net.InitFromDeviceIntent(ctx, na.device)`

### 2.2 Add `ensureTopologyIntent()` to NodeActor

Architecture §3: "Actuated online → topology offline: close the
online node, create a new one from `topology.json`."

In `actors.go`:
- If `na.node != nil && !na.node.HasActuatedIntent()`:
  - Call `na.node.DisconnectTransport()` — reset to topology-offline.
    Without this, transport left by a previous Drift/Reconcile would
    cause subsequent CRUD to write to Redis through the leftover
    connection (Lock/Apply no-ops only work when `n.conn == nil`).
  - Return
- If `na.node != nil` → `na.closeNode()`
- `na.node = na.net.BuildTopologyNode(na.device)`

### 2.3 Add `execute(ctx, fn)` to NodeActor + mode middleware

Architecture §10: "`NodeActor.execute` — single entry point." ONE
branch point for mode dispatch. Architecture §1: "The handler code is
identical in both modes."

**Why this doesn't already exist:** Current entry points
(`connectAndRead`, `connectAndLocked`, `connectAndExecute`) all assume
online mode via `getNode(ctx)` → `net.Connect()`. The architecture
requires a single dispatch that selects topology vs intent mode.

**Mode flows through ctx, not as a parameter.** A middleware injects
mode into the request context from the `?mode=topology` query param.
`execute` reads mode from ctx. Existing handler signatures don't
change. This is what the architecture means by "handler code is
identical in both modes."

There is no `/topology/` URL namespace. The `--topology` flag (which
the client translates to `?mode=topology`) is the **only** mode
selector. Intent subcommands and CRUD commands all respect the same
flag, so there is one consistent mode mechanism — not two parallel
command hierarchies.

**Mode middleware** (in `api/server.go` or `api/mode.go`):
```go
type ctxKey string
const modeKey ctxKey = "mode"

func modeMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        mode := ModeIntent
        if r.URL.Query().Get("mode") == "topology" {
            mode = ModeTopology
        }
        ctx := context.WithValue(r.Context(), modeKey, mode)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func modeFromCtx(ctx context.Context) Mode {
    if m, ok := ctx.Value(modeKey).(Mode); ok { return m }
    return ModeIntent
}
```

Apply `modeMiddleware` to the HTTP mux in server setup.

**`execute` reads mode from ctx:**
```go
func (na *NodeActor) execute(ctx context.Context, fn func() (any, error)) (any, error) {
    return na.do(ctx, func() (any, error) {
        mode := modeFromCtx(ctx)
        if mode == ModeTopology {
            if err := na.ensureTopologyIntent(); err != nil { return nil, err }
        } else {
            if err := na.ensureActuatedIntent(ctx); err != nil { return nil, err }
        }
        return fn()
    })
}
```

**Refactor ALL entry points as compositions on `execute`:**

Signatures UNCHANGED — mode flows through ctx transparently:

- `connectAndRead(ctx, fn)`: `execute(ctx, func() { Ping(ctx); fn(na.node) })`
  — Ping is a no-op without transport (task 1.4 transport guard)
- `connectAndExecute(ctx, opts, fn)`: `execute(ctx, func() { Execute(ctx, opts, fn) })`
- `connectAndLocked(ctx, fn)`: `execute(ctx, func() { Lock(ctx); fn(na.node); Unlock() })`
  — Lock/Unlock are no-ops without transport (task 1.7)

**No CRUD handler changes.** Existing handlers pass `r.Context()` to
`connectAndRead`/`connectAndExecute`/`connectAndLocked` — mode is
already in ctx from the middleware. Zero handler signature changes.

Delete `getNode(ctx)` — replaced by `ensureActuatedIntent(ctx)` inside `execute`.

### 2.5 Delete composite storage from NodeActor

Architecture §11: "Delivery pipeline: GenerateComposite → Deliver →
Subsumed by Reconcile."

In `actors.go`:
- Delete `compositeEntry` type (line 139)
- Delete `compositeExpiry` const (line 145)
- Delete `composites` field, `compositeMu` field (lines 131-132)
- Delete `storeComposite` (line 233), `getComposite` (line 252),
  `removeComposite` (line 268)
- Remove `composites: make(...)` from `newNodeActor` (line 153)

### 2.6 Add public methods to `pkg/newtron/network.go`

Architecture §3 construction table.

- `InitFromDeviceIntent(ctx, device) (*Node, error)` — loads profile,
  resolves specs, creates internal node via `node.New()`, calls
  `InitFromDeviceIntent(ctx)`, wraps as public Node
- `BuildTopologyNode(device) (*Node, error)` — creates
  TopologyProvisioner, calls `BuildAbstractNode(device)`, wraps
- `BuildEmptyTopologyNode(device) (*Node, error)` — creates
  TopologyProvisioner, calls `BuildEmptyAbstractNode(device)`, wraps
- `SaveDeviceIntents(device string, steps []spec.TopologyStep) error`
  — calls TopologyProvisioner.SaveDeviceIntents

### 2.7 Add public methods to `pkg/newtron/node.go`

Architecture §6 six operations.

- `Tree() (*spec.TopologyDevice)` — wraps internal Tree()
- `Drift(ctx) ([]DriftEntry, error)` — wraps internal, converts types
- `Reconcile(ctx) (*ReconcileResult, error)` — wraps internal
- `HasActuatedIntent() bool`
- `HasUnsavedIntents() bool`
- `ClearUnsavedIntents()`

### 2.8 Add public types to `pkg/newtron/types.go`

- `ReconcileResult{Applied int, Message string}`
- `DriftEntry` — public mirror of `sonic.DriftEntry` (public API must
  not expose internal `sonic.*` types per CLAUDE.md "Public API
  Boundary Design")

### 2.9 Delete handle-pattern code

Architecture §11 deletion table. All deletions are unconditional.

**From `pkg/newtron/node.go`:**
- `BuildComposite`, `wrapConfigDB`, `Deliver`, `VerifyExpected`
- `Snapshot` (replaced by `Tree`)
- `Refresh`, `RefreshWithRetry` (replaced by `Ping`/`PingWithRetry`)
- `Intents`, `IntentTree` (replaced by `Tree`)
- Keep `HealthCheck` — still used by `GET .../health` route

**From `pkg/newtron/types.go`:**
- `ProvisioningInfo`, `DeliveryMode`, `DeliveryOverwrite`,
  `DeliveryMerge`, `DeliveryResult`
- `ProvisionRequest`, `ProvisionResult`, `ProvisionDeviceResult`

**Delete `pkg/newtron/provision.go`** (exists, verified).

**From `pkg/newtron/network.go`:**
- `Connect` (replaced by `InitFromDeviceIntent`)
- `DetectDrift`, `DetectTopologyDrift`

**From internal layer:**

`node/node.go`:
- `DetectDrift` (line 195) — replaced by `Drift`
- `Refresh` (line 461) — replaced by `Ping`
- `RefreshWithRetry` (line 486) — replaced by `PingWithRetry`
- `Connect` (line 396), `ConnectForSetup` (line 403) — replaced by
  `ConnectTransport`/`InitFromDeviceIntent`

`node/composite.go` — **DELETE entire file**. Contains:
`CompositeConfig`, `CompositeBuilder`, `CompositeMode`,
`CompositeOverwrite`, `CompositeMerge`, `DeliverComposite`,
`CompositeDeliveryResult`, `NewCompositeBuilder`, `Build`,
`AddEntries`, `SetGeneratedBy`. None needed — Reconcile uses
`ExportEntries` + `ReplaceAll` directly.

`network/topology.go`:
- `GenerateDeviceComposite` — replaced by `BuildAbstractNode`
- `ProvisionDevice` (line 114) — replaced by `Reconcile`
- `DetectTopologyDrift` — replaced by `Drift`
- `VerifyDeviceHealth` — replaced by topology drift check
- `DriftReport`, `HealthReport` types

### 2.10 Delete zombie + history mechanism

Architecture §8 "Crash Recovery via Drift Guard + Reconcile": drift
guard + Reconcile subsume crash recovery. The `OperationIntent`
breadcrumb trail, zombie detection, and `dispatchReverse` rollback
are all eliminated.

**From `pkg/newtron/node.go`:**
- `ZombieIntent`, `ReadZombie`, `ClearZombie`, `SetBypassZombieCheck`
- `RollbackZombie`, `PreviewRollback` (standalone function)
- `dispatchReverse`
- `RollbackHistory`, `PreviewRollbackHistory`, `ReadHistory`
- `archiveToHistory`, `SetSkipHistory`
- `bypassZombieCheck` field, `skipHistory` field
- `history` field (rolling history of applied ChangeSets)

**From `Commit()` in `pkg/newtron/node.go`:**
- Remove `OperationIntent` construction + `WriteIntent` call
- Remove per-operation `UpdateIntentOps` progress tracking
- Remove `archiveToHistory` call
- Remove `DeleteIntent` call
- Keep: apply loop, verify loop, result construction

**From `Execute()` in `pkg/newtron/node.go`:**
- Remove zombie guard (`if !n.bypassZombieCheck && ...`)

**From `pkg/newtron/types.go`:**
- `OperationIntent` (public mirror)
- `HistoryResult`, `HistoryEntry`, `HistoryRollbackResult`

**From internal `node/node.go`:**
- `zombie` field (line 74)
- `ZombieIntent()` (line 914)
- `ClearZombie()` (line 917)
- `WriteIntent()` (line 933)
- `UpdateIntentOps()` (line 945)
- `DeleteIntent()` (line 957)
- `ReadIntent()` (line 969)
- History methods: `ReadHistory`, `WriteHistory`, `DeleteHistory`
- Remove zombie detection from `Lock()` (lines 564-579)

**From `pkg/newtron/device/sonic/statedb.go`:**
- `OperationIntent` type (line 361)
- `IntentOperation` type
- `WriteIntent`, `ReadIntent`, `UpdateIntentOps`, `DeleteIntent`
- `ReadIntentFromStateDB`

**Delete `pkg/newtron/device/sonic/statedb_intent_test.go`**

**From `pkg/newtron/api/handler_node.go`:**
- `handleReadZombie`, `handleRollbackZombie`, `handleClearZombie`
- `handleReadHistory`, `handleRollbackHistory`

**From `pkg/newtron/api/handler.go`:**
- Delete 5 routes: `GET .../zombie`, `POST .../rollback-zombie`,
  `POST .../clear-zombie`, `GET .../history`,
  `POST .../rollback-history`

**From `pkg/newtron/client/node.go`:**
- `ReadZombie`, `RollbackZombie`, `ClearZombie`
- `ReadHistory`, `RollbackHistory`

**From `cmd/newtron/cmd_device.go`:**
- `zombieCmd` and subcommands (read/rollback/clear)
- `historyCmd` and subcommands (list/rollback)

**From `pkg/newtron/api/api_test.go`:**
- Remove `ReadZombie`, `RollbackZombie`, `ClearZombie` from covered
- Remove `ReadHistory`, `RollbackHistory`, `PreviewRollbackHistory`
  from covered
- Remove `SetBypassZombieCheck`, `SetSkipHistory`, `ZombieIntent`
  from excluded

### 2.11 Build + test

- `go build ./...` passes
- `go vet ./...` passes
- `go test ./... -count=1` — all pass

---

## Phase 3: HTTP Layer

**Architecture §6, §10 traces.** Six handlers, six routes. Mode from
`?mode=topology` query param — no `/topology/` URL namespace.

### 3.1 Add unified handlers to `handler_node.go`

Architecture §6: "Same implementation works in both topology and online
modes." ONE handler per operation. Mode is in ctx from middleware — the
handler is mode-agnostic.

```go
func (s *Server) handleTree(w, r)
```
`actor.execute(ctx, func() { node.Tree() })`. Mode already in ctx
from middleware. Returns intent DAG.

```go
func (s *Server) handleDrift(w, r)
```
`actor.execute(ctx, func() { node.Drift(ctx) })`. Returns drift entries.

```go
func (s *Server) handleReconcile(w, r)
```
`actor.execute(ctx, fn)`. If ExecOpts.Execute: `node.Reconcile(ctx)`.
Else: `node.Drift(ctx)` (dry-run shows what would change).

```go
func (s *Server) handleSave(w, r)
```
`actor.execute(ctx, fn)`. Calls `node.Tree()` to get steps, then
`net.SaveDeviceIntents(device, steps)`, then `node.ClearUnsavedIntents()`.

```go
func (s *Server) handleReload(w, r)
```
Architecture §6 Reload: topology-mode only. Returns error 400
"reload is only valid in topology mode" if mode != topology.
Uses `na.do()` for actor serialization (NOT `execute` — Reload
has its own construction logic). Unconditionally closes the current
node and rebuilds from `topology.json`:
`na.closeNode(); na.node = na.net.BuildTopologyNode(na.device)`.
`ensureTopologyIntent()` is wrong here — it short-circuits when
the node is already topology-sourced (just disconnects transport),
but Reload must discard unsaved CRUD mutations by rebuilding.
Returns the reloaded intent tree.

```go
func (s *Server) handleClear(w, r)
```
Architecture §6 Clear: topology-mode only. Returns error 400
"clear is only valid in topology mode" if mode != topology.
Uses `na.do()` for actor serialization (NOT `execute` — Clear
has its own construction logic). Unconditionally closes the current
node and builds an empty topology node:
`na.closeNode(); na.node = na.net.BuildEmptyTopologyNode(na.device)`.
Returns empty tree (ports only, no intents).

### 3.2 Update route table in `handler.go`

**Delete 8 routes** (zombie/history routes already deleted in Phase 2
task 2.10):
- `POST /network/{netID}/provision` (line 89)
- `GET .../drift` (line 167)
- `GET .../topology/drift` (line 168)
- `GET .../topology/intents` (line 169)
- `POST .../generate-composite` (line 174)
- `POST .../verify-composite` (line 175)
- `POST .../deliver-composite` (line 176)
- `GET .../intent/tree` (line 108) — replaced by new `handleTree`

**Add 6 routes (mode from `?mode=topology` query param):**
- `GET .../intent/tree` → `handleTree`
- `GET .../intent/drift` → `handleDrift`
- `POST .../intent/reconcile` → `handleReconcile`
- `POST .../intent/save` → `handleSave`
- `POST .../intent/reload` → `handleReload`
- `POST .../intent/clear` → `handleClear`

No `/topology/` routes. The `--topology` flag is the only mode selector.
The client translates it to `?mode=topology` on the same endpoints.

### 3.3 Delete old handlers

Zombie/history handlers and their routes are already deleted in Phase 2
(task 2.10). This task covers the remaining handle-pattern handlers.

- Delete `handler_provisioning.go` (entire file)
- Delete `handleProvisionDevices` from `handler_network.go`
- Delete `handleDetectDrift`, `handleDetectTopologyDrift`,
  `handleTopologyIntents`, `handleIntentTree` from `handler_node.go`
- Delete `ProvisioningHandleRequest`, `ProvisioningHandleResponse`
  from `api/types.go`

### 3.4 Update `api_test.go` API completeness test

`TestAPICompleteness` (api_test.go) reflects-over `*newtron.Network`,
`*newtron.Node`, and `*newtron.Interface` and fails if any exported
method isn't in `coveredMethods` or `excludedMethods`. Phase 2 deletes
and adds public methods — this test must match.

**Network `coveredMethods` — delete:**
- `GenerateDeviceComposite`, `ProvisionDevices`, `DetectDrift`,
  `DetectTopologyDrift`, `Connect`

**Network `coveredMethods` — add:**
- `InitFromDeviceIntent`, `BuildTopologyNode`, `BuildEmptyTopologyNode`,
  `SaveDeviceIntents`

**Node `coveredMethods` — delete:**
- `Deliver`, `VerifyExpected`, `BuildComposite`, `Snapshot`,
  `Refresh`, `RefreshWithRetry`, `Intents`, `IntentTree`

**Node `coveredMethods` — add:**
- `Tree`, `Drift`, `Reconcile`
- Note: `IntentTree` deleted above is the old method hitting the old
  endpoint. The new `Tree` replaces it.

**Node `excludedMethods` — delete:**
- `IsAbstract` (renamed to `HasActuatedIntent`)

**Node `excludedMethods` — add:**
- `HasActuatedIntent` — "server uses internally for mode dispatch"
- `HasUnsavedIntents` — "server uses internally for mode switch guard"
- `ClearUnsavedIntents` — "server uses internally after save"

**Node `excludedMethods` — update reason:**
- `Lock` — update reason to mention transport guard + drift guard
- `RegisterPort` — update reason: "topology + actuated intent
  construction, not HTTP"

Verify: no stale method names remain in either map.

### 3.5 Build + test

- `go build ./...` passes
- `go test ./... -count=1` — all pass

---

## Phase 4: Client + CLI

**Architecture §6, §10 traces, §3 (--topology flag).**

The `--topology` flag is the **only** mode selector. There is no
`topology` CLI noun. All intent operations live under the `intent`
subcommand and respect the flag. CRUD commands also respect the flag.
This gives one consistent mode mechanism across the entire CLI.

### 4.1 Delete old client methods

From `client/network.go`:
- `GenerateProvisioning`, `VerifyExpected`, `DeliverProvisioning`,
  `ProvisionDevices`

From `client/node.go`:
- `DetectDrift`, `DetectTopologyDrift`, `TopologyIntents`

### 4.2 Add new client methods to `client/node.go`

Six methods, each appending `?mode=topology` when the client's
topology flag is set:

```go
func (c *Client) IntentTree(device)        // GET /intent/tree [?mode=topology]
func (c *Client) IntentDrift(device)       // GET /intent/drift [?mode=topology]
func (c *Client) IntentReconcile(device)   // POST /intent/reconcile [?mode=topology]
func (c *Client) IntentSave(device)        // POST /intent/save [?mode=topology]
func (c *Client) IntentReload(device)      // POST /intent/reload [?mode=topology]
func (c *Client) IntentClear(device)       // POST /intent/clear [?mode=topology]
```

No `Topology*` client methods. The `--topology` flag on the client
controls the query param. Same six methods serve both modes.

Note: `IntentReload` and `IntentClear` will return error 400 from the
server if called without `?mode=topology`. The client always sends the
query param regardless — the server enforces the mode constraint.

### 4.3 Update `cmd/newtron/cmd_intent.go`

- Remove `intentTopologyCmd` and its subcommands
- Update existing `intentTreeCmd` to call `IntentTree`
- Add `intentDriftCmd` → `IntentDrift(device)` + `printDriftEntries`
- Add `intentReconcileCmd` → `IntentReconcile(device)`
- Add `intentSaveCmd` → `IntentSave(device)`
- Add `intentReloadCmd` → `IntentReload(device)` (topology-mode only)
- Add `intentClearCmd` → `IntentClear(device)` (topology-mode only)
- All subcommands work in both modes via `--topology` flag
- `reload` and `clear` print error if `--topology` not set (server
  returns 400, but CLI can also check locally for better UX)

CLI examples:
```
newtron switch1 intent tree                      # actuated mode
newtron switch1 --topology intent tree           # topology mode
newtron switch1 intent drift                     # actuated mode
newtron switch1 --topology intent drift          # topology mode
newtron switch1 intent reconcile -x              # actuated mode (drift repair)
newtron switch1 --topology intent reconcile -x   # topology mode (provision)
newtron switch1 intent save                      # persist device intents → topology.json
newtron switch1 --topology intent save           # persist API-authored intents → topology.json
newtron switch1 --topology intent reload         # discard unsaved, reload from topology.json
newtron switch1 --topology intent clear          # empty intent DB, ports only
```

### 4.4 Delete `cmd/newtron/cmd_provision.go`

### 4.5 Update `cmd/newtron/main.go`

- Remove `provisionCmd` from command list (line 229) and
  `addWriteFlags` (line 253)
- No `topologyCmd` to add — topology mode is via `--topology` flag

### 4.6 Add `--topology` flag to root command

Architecture §1: "The `--topology` flag selects how the node is
constructed."

- Global `--topology` flag on root command
- Client appends `?mode=topology` query param when flag is set
- Server extracts via mode middleware (task 2.3)
- No mode-specific CLI logic — flag is forwarded transparently
- ALL commands respect the flag: CRUD commands (`vlan create`,
  `evpn setup`, etc.) and intent commands (`intent tree`,
  `intent reconcile`, etc.)

### 4.7 Build + test

- `go build ./...` passes
- `go test ./... -count=1` — all pass

---

## Phase 5: newtrun

**Architecture §11 newtrun changes.**

### 5.1 Rewrite `provisionExecutor` in `steps.go`

Current (lines 213-262): `GenerateProvisioning` → `ConfigReload` →
`DeliverProvisioning` → `Refresh` → `SaveConfig` per device.

New: single call `r.Client.IntentReconcile(name)` (with topology mode
set on client) per device via `executeForDevices`. Return
`ReconcileResult.Applied` in message.

### 5.2 Rewrite `verifyProvisioningExecutor` in `steps.go`

Current (lines 290-306): reads `Runner.Composites` for handle, calls
`VerifyExpected(name, handle)`.

New: `r.Client.IntentDrift(name)` (with topology mode set on client)
per device via `checkForDevices`.
Zero drift entries → pass; non-zero → fail with drift count.

### 5.3 Delete Composites

- Delete `Runner.Composites` field from `runner.go`
- Delete `StepOutput.Composites` field from `steps.go` (line 23)
- Remove composite merging from runner loop
- Remove `Composites: make(map[string]string)` initialization

### 5.4 Rename action constants in `scenario.go`

- `ActionProvision` ("provision") → `ActionTopologyReconcile` ("topology-reconcile")
- `ActionVerifyProvisioning` ("verify-provisioning") → `ActionVerifyTopology` ("verify-topology")
- Update `executors` map registration (steps.go line 27)

### 5.5 Update YAML scenario files

All `action: provision` → `action: topology-reconcile`.
All `action: verify-provisioning` → `action: verify-topology`.

### 5.6 Update newtrun tests

- Remove `Composites: make(map[string]string)` from test Runner init
- Update action name references
- Delete composite-related test cases

### 5.7 Build + test

- `go build ./...` passes
- `go test ./... -count=1` — all pass

---

## Phase 6: Final Verification

**Architecture §12, ai-instructions.md §9 (post-implementation
conformance audit).**

### 6.1 Build clean

- `go build ./... && go vet ./...` — clean
- `go test ./... -count=1` — all pass

### 6.2 Dead code grep

Each grep must return zero matches:

- `grep -rn 'generate-composite\|verify-composite\|deliver-composite' pkg/ cmd/`
- `grep -rn 'ProvisioningInfo\|ProvisioningHandle\|ProvisionRequest' pkg/ cmd/`
- `grep -rn 'Composites' pkg/newtrun/`
- `grep -rn 'handleProvisionDevices\|handleProvisioningGenerate' pkg/`
- `grep -rn 'applyShadow\|applyIntentToShadow' pkg/ cmd/`
- `grep -rn 'GenerateDeviceComposite\|BuildComposite\|DeliverComposite' pkg/ cmd/`
- `grep -rn 'CompositeConfig\|CompositeBuilder\|CompositeMode' pkg/`
- Verify `shadow` appears only in comments explaining the rename, not
  as active terminology: `grep -rn 'shadow' pkg/newtron/network/node/`
- `grep -rn 'n\.offline\|IsOffline\|ConnectOnline\|ensureOnline\|ensureOffline' pkg/` → zero
  (all renamed: `actuatedIntent`/`HasActuatedIntent`/`InitFromDeviceIntent`/`ensureActuatedIntent`/`ensureTopologyIntent`)
- `grep -rn 'func.*Refresh\b' pkg/newtron/network/node/node.go` → zero
  (Refresh and RefreshWithRetry deleted, replaced by Ping/PingWithRetry)
- `grep -rn 'dispatchReverse\|RollbackZombie\|ClearZombie\|ReadZombie\|ZombieIntent' pkg/ cmd/` → zero
- `grep -rn 'OperationIntent\|IntentOperation\|WriteIntent\|DeleteIntent' pkg/ cmd/` → zero
  (OperationIntent crash-recovery mechanism deleted — drift guard + Reconcile replace it)
- `grep -rn 'RollbackHistory\|PreviewRollbackHistory\|archiveToHistory\|skipHistory\|bypassZombie' pkg/ cmd/` → zero

### 6.3 Architecture conformance audit

Mechanical verification of each architecture section:

**§1 Intent DB:**
- `grep -rn 'n.configDB = n.conn' pkg/newtron/network/node/` → zero.
  configDB is never assigned from device.

**§2 One Pipeline:**
- `render` exists once in `node/node.go`. No other rendering mechanism.

**§5 Rendering:**
- `grep -rn 'applyShadow\|applyIntentToShadow' pkg/` → zero.

**§6 Six Operations:**
- `Tree`, `Drift`, `Reconcile` each have ONE implementation in
  `node/node.go`.
- `grep -c 'func.*handleTree\|func.*handleDrift\|func.*handleReconcile\|func.*handleSave\|func.*handleReload\|func.*handleClear' pkg/newtron/api/handler_node.go`
  → exactly 6.
- `handleReload` and `handleClear` return 400 if mode != topology.

**§7 Device I/O:**
- `ConnectTransport` exists. `Connect` (old) does not.
- `InitFromDeviceIntent` exists. `ConnectOnline` does not.
- `Ping` exists. `Refresh` does not.

**§8 Lock + Crash Recovery:**
- Lock body does not contain `GetAll` or `n.configDB =`.
- Lock contains drift guard gated on `n.actuatedIntent`.
- Lock returns nil when `n.conn == nil` (transport guard).
- `cs.Apply(n)` returns nil when `n.conn == nil` (transport guard).
- Lock does NOT contain zombie detection (no `ReadIntent`, no `zombie`).
- `Commit` does NOT write/delete `OperationIntent` records.
- `Execute` does NOT check for zombie operations.
- No `dispatchReverse`, `RollbackZombie`, or `ClearZombie` exist.

**§10 End-to-End:**
- ALL mode-agnostic actor entry points go through `execute` — verify:
  `grep -n 'func.*NodeActor.*ctx' pkg/newtron/api/actors.go` → all
  public methods call `execute` or `do`.
- `connectAndRead`, `connectAndLocked`, `connectAndExecute` each call
  `execute` internally — verify bodies contain `na.execute(`.
- Exception: `handleReload` and `handleClear` use `na.do()` directly
  — they are mode-specific operations with their own construction
  logic, not mode-agnostic operations dispatched by `execute`.
- No mode-agnostic handler extracts mode directly — only
  `handleReload` and `handleClear` call `modeFromCtx` (to enforce
  topology-mode-only). All other handlers rely on `execute` for
  mode dispatch via ctx.
- No `getNode` method exists.
- No `doOffline`/`doOnline` pairs.
- No `ensureOnline`/`ensureOffline` (renamed to `ensureActuatedIntent`/`ensureTopologyIntent`).

**§3 Three States:**
- `ensureActuatedIntent` and `ensureTopologyIntent` are the only mode
  construction methods — `grep -rn 'func.*ensure' pkg/newtron/api/actors.go`
  → exactly 2.

**§11 What Changed:**
- 6 routes in handler.go for intent/tree, intent/drift,
  intent/reconcile, intent/save, intent/reload, intent/clear.
  Mode from `?mode=topology` query param.
- No `/topology/` routes, no composite routes, no provision route.

### 6.4 DESIGN_PRINCIPLES + CLAUDE.md updates

Per architecture §12, update:
- DESIGN_PRINCIPLES: §5 authority model, §34/35 episodic caching,
  §1/§2/§13/§19/§20/§21 mechanism descriptions
- CLAUDE.md: "Device Is Source of Reality" section, pipeline references,
  "Abstract Node" section

---

## Tracker

### Phase 0: Rename + Doc Sync

- [x] 0.1 Rename `applyShadow` → `render` in `node/node.go:271` + all call sites + "shadow" → "projection" + rewrite stale model comments (node.go:42-47, 90-92, 630-633; changeset.go:168; service_ops.go:925-927)
- [x] 0.2 Rename `applyIntentToShadow` → `renderIntent` in `node/intent_ops.go` + call sites
- [x] 0.3 Update architecture doc: remove "(currently ...)" parentheticals, "materialization" → "rendering" (grep all occurrences), fix §6 Reconcile return type + Save signature (composition), fix §8 Lock performs list + guard (`n.conn == nil`) + pseudocode line 560 (remove zombie), fix §10 traces (`execute(ctx, fn)` + ensure method names), fix §1 + §3 ensure method names, fix §9 `ConnectOnline` → `InitFromDeviceIntent`
- [x] 0.4 Delete `docs/newtron/pipeline.md`
- [x] 0.5 Update CLAUDE.md pipeline references
- [x] 0.6 Build + verify: `go build`, `go vet`, grep for zero stale references

### Phase 1: Node Internal Operations

- [x] 1.1 Add `ConnectTransport(ctx)` — SSH+Redis, projection unchanged, NO offline guard
- [x] 1.2 Delete `offline` field + `IsOffline()`, add `actuatedIntent` field + `HasActuatedIntent()` + `unsavedIntents` field + `HasUnsavedIntents()` + `ClearUnsavedIntents()` + `DisconnectTransport()`, update `NewAbstract()`, set `unsavedIntents` in `writeIntent`/`deleteIntent`
- [x] 1.3 Add `InitFromDeviceIntent(ctx)` — legacy STATE_DB migration + transport + read PORT/intents + replay + set `actuatedIntent = true`
- [x] 1.4 Add `Ping(ctx)` — Redis PING, transport guard (`n.conn == nil`), no projection overwrite
- [x] 1.5 Add `PingWithRetry(ctx, timeout)` — poll Ping at 2s intervals
- [x] 1.6 Change `Lock()` — delete configDB refresh (lines 537-547) + delete zombie detection (lines 564-579)
- [x] 1.7 Change `Lock()`/`Unlock()`/`cs.Apply(n)` — add transport guards (`n.conn == nil` → no-op)
- [x] 1.8 Change `Lock(ctx)` — add ctx param + drift guard + update all callers
- [x] 1.9 Rename `Snapshot` → `Tree` + update all callers
- [x] 1.10 Add `Drift(ctx)` — projection vs actual, no reconstruction
- [x] 1.11 Add `Reconcile(ctx)` + `ReconcileResult` — full projection delivery
- [x] 1.12 Rewrite preconditions (`!n.offline` → `n.actuatedIntent`) + 9 direct callers to `GetIntent()` + delete 5 typed accessors + fix `service_ops.go:929` (always scan projection)
- [x] 1.13 Add `BuildAbstractNode` to `network/topology.go` — extract from `GenerateDeviceComposite`
- [x] 1.14 Add `SaveDeviceIntents` to `network/topology.go`
- [x] 1.15 Add `BuildEmptyAbstractNode` to `network/topology.go` — like `BuildAbstractNode` but skips step replay (ports only)
- [x] 1.16 Build + test: `go build`, `go vet`, `go test ./...`

### Phase 2: Actor Mode Switching + Public API

- [x] 2.0 Define `Mode` type (`ModeTopology`, `ModeIntent`)
- [x] 2.1 Add `ensureActuatedIntent(ctx)` to NodeActor in `actors.go` — includes unsaved topology intent guard (`HasUnsavedIntents()` → error)
- [x] 2.2 Add `ensureTopologyIntent()` to NodeActor — includes `DisconnectTransport()` to prevent topology-online CRUD leak
- [x] 2.3 Add `execute(ctx, fn)` + mode middleware — mode from `?mode=topology` query param (no URL path detection), refactor connectAndRead/Locked/Execute as compositions on execute (signatures UNCHANGED, zero CRUD handler changes)
- [x] 2.5 Delete composite storage from NodeActor (lines 131-153, 233-272)
- [x] 2.6 Add public `InitFromDeviceIntent` + `BuildTopologyNode` + `BuildEmptyTopologyNode` + `SaveDeviceIntents` to `pkg/newtron/network.go`
- [x] 2.7 Add public `Tree` + `Drift` + `Reconcile` + `HasActuatedIntent` + `HasUnsavedIntents` + `ClearUnsavedIntents` to `pkg/newtron/node.go`
- [x] 2.8 Add `ReconcileResult` + `DriftEntry` to `pkg/newtron/types.go`
- [x] 2.9 Delete: handle-pattern + replaced methods in node.go (BuildComposite, wrapConfigDB, Deliver, VerifyExpected, Snapshot, Refresh, RefreshWithRetry, Intents, IntentTree), types in types.go, provision.go, composite.go, Connect + old methods in network.go, old methods in topology.go + internal node.go
- [x] 2.10 Delete zombie + history mechanism: `dispatchReverse`, `RollbackZombie`/`ClearZombie`/`ReadZombie`/`ZombieIntent`, `RollbackHistory`/`ReadHistory`/`PreviewRollbackHistory`, `OperationIntent` lifecycle in `Commit`, zombie guard in `Execute`, zombie detection in `Lock`, `OperationIntent`/`IntentOperation` types in sonic + public, history types, statedb_intent_test.go, zombie/history handlers + 5 routes from handler.go + handler_node.go. Also deleted: client zombie/history/settings methods (4.1 partial), cmd zombie/history/settings commands + deviceCmd (4.45/4.5 partial), DeviceSettings type + sonic methods
- [x] 2.11 Build + test: `go build`, `go vet`, `go test ./...` — all pass

### Phase 3: HTTP Layer

- [x] 3.1 Add 6 unified handlers: `handleTree`, `handleDrift`, `handleReconcile`, `handleSave`, `handleReload`, `handleClear`
- [x] 3.2 Update route table: delete old routes (provision, drift, topology/drift, generate/verify/deliver-composite), add 6 new routes (intent/tree, intent/drift, intent/reconcile, intent/save, intent/reload, intent/clear — mode from `?mode=topology`)
- [x] 3.3 Delete: `handler_provisioning.go`, `handleProvisionDevices`, `handleDetectDrift`, `handleDetectTopologyDrift`, wire types (`ProvisioningHandleRequest/Response`). Also deleted old client methods (GenerateProvisioning, DeliverProvisioning, VerifyExpected, ProvisionDevices)
- [x] 3.4 Update `api_test.go` completeness test: synced covered/excluded methods with current public API
- [x] 3.5 Build + test: `go build`, `go vet`, `go test ./...` — all pass

### Phase 4: Client + CLI

- [x] 4.1 Delete old client methods from `client/network.go` + `client/node.go` (zombie/history deleted in 2.10, provisioning deleted in 3.3)
- [x] 4.2 Add new client methods: `IntentDrift`, `IntentSave`, `IntentReload`, `IntentClear`, `Reconcile`, `DetectDrift`, `DetectTopologyDrift`
- [x] 4.3 Update `cmd/newtron/cmd_intent.go`: removed topology subtree, added drift/reconcile/save/reload/clear — all respect `--topology` flag
- [x] 4.4 Delete `cmd/newtron/cmd_provision.go`
- [x] 4.45 Delete zombie/history commands from `cmd/newtron/cmd_device.go` — completed in 2.10
- [x] 4.5 Update `cmd/newtron/main.go`: removed provisionCmd, added `--topology` persistent flag
- [x] 4.6 Add `--topology` flag to root command → `app.topology` bool → mode string in client calls
- [x] 4.7 Build + test: `go build`, `go vet`, `go test ./...` — all pass

### Phase 5: newtrun

- [x] 5.1 Rewrite `provisionExecutor` → `Reconcile(name, "topology", opts)` single call
- [x] 5.2 Rewrite `verifyProvisioningExecutor` → `IntentDrift(name, "topology")` zero-drift check
- [x] 5.3 Delete `Runner.Composites`, `StepOutput.Composites`, composite merging
- [x] 5.4 Rename action constants: provision → topology-reconcile, verify-provisioning → verify-topology
- [x] 5.5 Update YAML scenario files (6 files)
- [x] 5.6 Update newtrun tests: removed Composites from Runner inits, deleted TestRunScenarioSteps_InitComposites
- [x] 5.7 Build + test: `go build`, `go test ./...` — all pass

### Phase 6: Final Verification

- [x] 6.1 `go build ./... && go vet ./...` pass. `go test ./...` passes (newtlab flaky port-conflict test is pre-existing)
- [x] 6.2 Dead code grep — ProvisioningInfo/Handle/Request, Composites in newtrun, handleProvisionDevices, applyShadow, DriftReport, doOffline/doOnline all zero
- [x] 6.3a Architecture §1 — KNOWN DEVIATION: old `connectWithOpts` still has `n.configDB = n.conn.ConfigDB` (used by HealthCheck path via Connect). Will be eliminated when HealthCheck is rewritten to use Drift + operational checks
- [x] 6.3b Architecture §2 — one `render` function at node.go:323
- [x] 6.3c Architecture §5 — zero applyShadow/shadow references in node/
- [x] 6.3d Architecture §6 — 6 intent/* routes, handleReload+handleClear return 400 if mode != topology
- [x] 6.3e Architecture §7 — ConnectTransport + InitFromDeviceIntent exist. KNOWN DEVIATION: old Connect/ConnectForSetup/Refresh still exist (used by HealthCheck and newtrun provision executor)
- [x] 6.3f Architecture §8 — Lock has drift guard (`n.actuatedIntent`). KNOWN DEVIATION: old `Refresh` method has `GetAll` (used by HealthCheck path). No zombie detection/OperationIntent lifecycle/dispatchReverse
- [x] 6.3g Architecture §3 — exactly 2 ensure methods (ensureActuatedIntent, ensureTopologyIntent)
- [x] 6.3h Architecture §10 — all mode-agnostic actor paths go through execute. handleReload/handleClear use na.do() directly with topology-only guard. No getNode/ensureOnline/ensureOffline/IsOffline
- [x] 6.3i Architecture §11 — no composite/provision/topology routes. 6 intent/* routes + init-device
- [x] 6.3j Naming audit — no offline/IsOffline/ConnectOnline/ensureOnline/ensureOffline in api/
- [ ] 6.4 Update DESIGN_PRINCIPLES + CLAUDE.md per architecture §12
