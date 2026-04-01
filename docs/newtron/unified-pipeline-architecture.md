# Unified Pipeline Architecture

How data flows through newtron. Every operation — topology provisioning,
interactive CLI command, drift detection, reconciliation — follows the
single pipeline described here.

```
                                  ┌───────────────────────────┐
                                  │                           │
                                  │     Abstract Topology     │
                                  │      (topology.json)      │
                                  │                           │   ◀┐
                                  └───────────────────────────┘    │
                                    │                              │
                                    │ ReplayStep                   │
                                    │ TOPOLOGY MODE                │ Save
                                    │ (topology authoritative)     │
                                    ▼                              │
                                ┌−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−┐
                                ╎                                   Abstract Node                      ╎
                                ╎                                                                      ╎
                                ╎ ┌──────────────────────────────────────────────────────┐             ╎             ┌───────────┐
                                ╎ │                                                      │             ╎             │           │
                                ╎ │                      Intent DB                       │             ╎             │ API / CLI │
                                ╎ │                   (NEWTRON_INTENT)                   │             ╎  Tree       │           │
  ┌───────────────────────────▶ ╎ │                                                      │             ╎ ──────────▶ │           │
  │                             ╎ └──────────────────────────────────────────────────────┘             ╎             └───────────┘
  │                             ╎   │                                                                  ╎
  │                             ╎   │                            −−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−┘
  │                             ╎   │                           ╎                      ▲   config method               │
  │                             ╎   │                           ╎                      │   (mutation, both modes)      │
  │                             ╎   │ render                    ╎                      └───────────────────────────────┘
  │                             ╎   ▼                           ╎
  │                             ╎ ┌───────────────────────────┐ ╎
  │                             ╎ │                           │ ╎
  │                             ╎ │        Projection         │ ╎
  │ IntentsToSteps + ReplayStep ╎ │     (typed CONFIG_DB)     │ ╎
  │ ACTUATED MODE               ╎ │                           │ ╎ ─┐
  │ (device authoritative)      ╎ └───────────────────────────┘ ╎  │
  │                             ╎                               ╎  │
  │                             └−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−┘  │
  │                                 │                              │
  │                                 │ Reconcile (full)             │
  │                                 │ Apply (incremental)          │ Drift (compare)
  │                                 ▼                              │
  │                             ┌−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−┐  │
  │                             ╎        Physical Device        ╎  │
  │                             ╎                               ╎  │
  │                             ╎ ┌───────────────────────────┐ ╎  │
  │                             ╎ │                           │ ╎  │
  │                             ╎ │     Actual CONFIG_DB      │ ╎  │
  │                             ╎ │       (Redis DB 4)        │ ╎  │
  └──────────────────────────── ╎ │                           │ ╎ ◀┘
                                ╎ └───────────────────────────┘ ╎
                                ╎                               ╎
                                └−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−−┘
```

Three data stores, one pipeline, two authority modes. The Abstract Node
holds **intent** (what should be configured) and **projection** (the
CONFIG_DB that intents produce). The Physical Device holds **actual**
(what exists in Redis). All paths flow through the Abstract Node — there
is no direct path between the Abstract Topology and the Physical Device.

**Two authority modes (same pipeline, different intent source):**
- **Topology mode** (top-down) — topology.json is authoritative. Intents flow down via ReplayStep. Reconcile pushes the projection to the device. Save persists API-authored intents back to topology.json.
- **Actuated mode** (bottom-up) — device intents are authoritative. Intents flow up via IntentsToSteps + ReplayStep. Mutations are applied incrementally. Save persists device intents to topology.json. Drift guard refuses writes if projection ≠ device.

**The unified pipeline (inside the Abstract Node):**
- **render** — every intent change produces entries that update the projection. One mechanism, both modes, all paths.

**Write paths into the Abstract Node (intent sources):**
- **ReplayStep** — replay topology.json steps into the intent DB (topology mode)
- **IntentsToSteps + ReplayStep** — read device NEWTRON_INTENT, replay into the intent DB (actuated mode)
- **config method** — API/CLI creates new intents interactively (both modes)

**Write paths out of the Abstract Node (delivery):**
- **Reconcile** (full) — export entire projection, ReplaceAll to device
- **Apply** (incremental) — write a single ChangeSet to Redis

**Read paths:**
- **Tree** — read the intent DB, return the intent DAG
- **Drift** — compare projection against actual CONFIG_DB, return differences
- **Save** — read the intent DB, persist to topology.json

**Reset paths (topology mode only):**
- **Reload** — discard unsaved changes, rebuild from topology.json
- **Clear** — discard all intents, empty node (ports only)

---

## 1. The Core Abstraction: Intent DB

The Node's primary state is its **intent collection** — the set of
NEWTRON_INTENT records in `configDB.NewtronIntent`. These intents declare
what should be configured on the device: which VLANs, VRFs, BGP peers,
services, ACLs, and so on.

The typed CONFIG_DB tables (`configDB.VLAN`, `configDB.VRF`,
`configDB.BGPGlobals`, etc.) are a **rendered projection** — derived
by replaying intents through config functions. They are not a "shadow" of
the device, not a cache of Redis, not a copy of anything. They are the
consequence of what the intents declare.

```
Intent DB (NEWTRON_INTENT records)
    │
    │  replay each intent through its config function
    ▼
Projection (typed CONFIG_DB tables)
    │
    │  ExportRaw() / ExportEntries()
    ▼
Wire format ([]Entry / RawConfigDB)
    │
    │  PipelineSet / ReplaceAll
    ▼
Device (Redis CONFIG_DB)
```

**The device should match the projection. Drift is when it doesn't.**

The three data stores:
- Intent DB = what should exist (primary)
- Projection = what the intents produce (derived)
- Device = what actually exists (observed)
- Drift = projection ≠ device

### Intent DB is the decision substrate

All operational logic reads the intent DB — not the projection.
Preconditions, idempotency guards, reference counting, membership
checks, and query methods all read intent records. The projection
exists for exactly two purposes:

1. **Device delivery** — `ExportEntries()` / `ExportRaw()` for
   Reconcile and Apply
2. **Drift detection** — comparing expected CONFIG_DB against actual

No operational decision reads the projection. This is not a guideline
for preconditions alone — it is a universal rule. Every piece of
information in the projection came from an intent record; reading the
projection for decisions couples operational logic to SONiC CONFIG_DB
table structure. Reading intents keeps SONiC knowledge contained to
two places: config generators (forward path) and schema validation
(render path).

Concrete consequences:
- **Preconditions** check `GetIntent(resource)`, not `configDB.VLAN`
- **Idempotency** checks `GetIntent(resource)` at the top of each
  config method
- **Reference counting** (shared resources like SAG_GLOBAL, QoS
  policies, route maps) scans intents for consumers, not projection
  tables
- **Membership queries** (PortChannel members, VRF interfaces) read
  intent params, not `configDB.PortChannelMember` or
  `configDB.Interface`
- **Query/display methods** (`GetVLAN`, `GetVRF`, `ListVLANs`) build
  responses from intent records and params, not from typed configDB
  structs

The intent record is the domain-level description. The projection is
its SONiC-specific rendering. Operational logic speaks domain; only
the delivery and drift layers speak SONiC.

### Abstract Topology — Abstract Nodes

The Abstract Topology (`topology.json`) is to the real deployed network
as the Abstract Node is to the real device. It declares what the network
*should* look like — which devices exist, what ports they have, and what
intents (steps) each device should have. It is network-level intent.

`topology.json` is the persistence of the Abstract Topology, just as
`config_db.json` is the persistence of SONiC configuration. The Abstract
Topology is the runtime object; `topology.json` is its durable store.

The Abstract Topology contains Abstract Nodes. Each device entry in
`topology.json` specifies ports and steps. When the topology provisioner
processes a device, it creates an Abstract Node (`NewAbstract()`),
registers ports, and replays the topology steps. The Abstract Node's
intent DB and projection are populated entirely from the Abstract
Topology — no device contact needed.

```
Abstract Topology (topology.json)
    │
    │  for each device
    ▼
Abstract Node
    ├─ Intent DB:   populated from topology steps (via ReplayStep)
    └─ Projection:  rendered CONFIG_DB tables
```

This is not just naming — it's the object model. The Abstract Topology
is the container of Abstract Nodes, just as a real network contains real
devices. Topology operations (`intent tree --topology`, `intent drift
--topology`, `intent reconcile --topology`) operate on Abstract Nodes
derived from the Abstract Topology. Actuated operations (`intent tree`,
`intent drift`, `intent reconcile`) operate on Abstract Nodes
reconstructed from device intents.

Same node type, different intent source. The abstraction is the same in
both cases — the node never holds actual device state. It holds intents
and their projection.

### Topology Mode: API-Authored Intents

Device intents in the Abstract Topology should be created via the API,
though hand-authoring `topology.json` steps directly is also supported.
In topology mode, the same API commands that operate on live devices
operate on Abstract Nodes instead:

```
newtron switch1 --topology evpn setup
newtron switch1 --topology vlan create --vlan-id 100
newtron switch1 --topology intent save
newtron switch1 --topology intent reconcile -x
```

The `--topology` flag selects how the node is constructed
(`ensureTopologyIntent` vs `ensureActuatedIntent`), not what the
operation does. The handler code is identical in both modes — it calls
`fn(node)` on whichever node the actor provides. The pipeline is the
same: Intent → Replay → Render → [Deliver]. Only the Deliver step
differs: skipped in topology mode (rendering is the point), executed
in online mode.

This unifies day-1 and day-2 into the same workflow:

```
Create intents (API) → Save (topology.json) → Reconcile (device)
                                                    │
                                              API mutations
                                                    │
                                              Save (topology.json)
```

Day-1 starts from an empty abstract node. Day-2 starts from a device's
existing intents. Same API, same commands, different mode.

**RMA recovery** follows naturally: Reconcile from the saved
`topology.json` replays intents through the replacement device's profile.
If the replacement is a different platform, the intents are identical —
only the rendering differs.

---

## 2. One Pipeline

At the node level, data flows through one pipeline:

```
Intent → Replay → Render → [Deliver]
```

**Node construction** (both topology and online modes):

1. **Intent source**: Intents come from topology.json steps or device
   NEWTRON_INTENT records.
2. **Replay**: `IntentsToSteps` → `ReplayStep` calls config methods. Each
   config method generates entries, writes its intent record, and
   renders entries into the projection.
3. **Render**: `render(cs)` validates entries against the schema and
   updates the typed CONFIG_DB tables.
4. **Deliver** (optional): `cs.Apply(n)` for interactive, `ReplaceAll()`
   for Reconcile. Skipped during replay — rendering was the point.

**Within a single config method**, intent recording and entry generation
are fused — not sequential pipeline stages. The method-level contract:
by return, the intent DB and projection are both updated, and the
ChangeSet contains intent records (prepended) + config entries.

The caller decides what happens with the ChangeSet:
- **Replay** (`ReplayStep`): ChangeSet discarded. Rendering was
  the point.
- **Interactive** (`Execute`): `cs.Apply(n)` writes to Redis.
- **Reconcile**: Full projection delivered via `ReplaceAll()`.

---

## 3. Three States, One Pipeline

The node exists in one of three states. The pipeline is the same in all
three — only the intent source and authority direction differ.

| State | Intent source | Authority | Operations |
|-------|--------------|-----------|------------|
| **Topology offline** | topology.json | topology → device | Tree, Drift (auto-connects), Reconcile (auto-connects), Save, Reload, Clear, Create/modify intents |
| **Topology online** | topology.json | topology → device | Tree, Drift, Reconcile, Save, Reload, Clear |
| **Actuated online** | device NEWTRON_INTENT | device → topology | Tree, Drift, Reconcile (drift repair), Save, Mutate intents (drift guard: refuse if drifted) |

**Topology offline**: The abstract node is initialized from the
persisted Abstract Topology (`topology.json`). New intents are created
via the API against the abstract node's intent DB. Save persists them
back to `topology.json`. No device connection.

**Topology online**: The same topology-sourced abstract node, now with
transport connected. Reconcile delivers the full projection to the
device. Drift compares projection against device. The topology is
authoritative — the device should match it.

**Actuated online**: The abstract node is initialized from the device's
actuated NEWTRON_INTENT records. API mutations create/modify intents
which are delivered to the device. Save persists the device's current
intents to `topology.json`, overwriting whatever was previously there.
The device intents are authoritative — `topology.json` is updated to
match.

State transitions:
- **Topology offline → topology online**: `ConnectTransport` adds the
  wire. Intent DB and projection unchanged. Enables Deliver/Reconcile.
- **Topology offline → actuated online**: `ensureActuatedIntent` closes
  the offline node, creates a new one from device intents. **Guarded**:
  if the topology node has unsaved intents (CRUD mutations not yet
  persisted via Save), the transition is refused. The user must `intent
  save --topology` first or the unsaved intents are lost.
- **Actuated online → topology offline**: `ensureTopologyIntent` closes
  the online node, creates a new one from `topology.json`. No guard
  needed — actuated intents live on the device and survive node
  destruction.

Node construction:

| Source | Construction |
|--------|--------------|
| topology.json | `New()` → `RegisterPort()` → `ReplayStep()` for each step |
| Device intents | `New()` → `ConnectTransport()` → read PORT + NEWTRON_INTENT from `sonic.Device.ConfigDB` → `RegisterPort()` → `IntentsToSteps()` → `ReplayStep()` for each step |

Both paths produce a Node whose intent DB and projection are populated.
The only difference is the source of steps fed into the replay loop.
After replay, operations, Save, Tree, Drift, and Reconcile all work
identically regardless of which source initialized the node.

---

## 4. Config Methods: Intent + Entry Generation

Each config method (CreateVLAN, ConfigureBGP, ApplyService, etc.) does two
things on the same ChangeSet:

1. **Writes intent**: `writeIntent(cs, op, resource, params, parents)` →
   `cs.Prepend()` puts the intent record first in the ChangeSet, and
   `renderIntent()` updates `configDB.NewtronIntent` immediately so
   subsequent intents can see parents.
2. **Generates entries**: `op()` runs preconditions → calls the config
   generator → `render(cs)` updates the projection.

In Redis delivery order, intent records arrive BEFORE config entries
(because `Prepend`). This means even the wire order is intent-first.

The config generators (`createVlanConfig`, `createSviConfig`, etc.) are
pure functions — they take parameters and return `[]sonic.Entry`. They have
no side effects. The intent management and rendering happen in the wrapping methods
and `op()`.

Intent-idempotency: each config method checks `n.GetIntent(resource)` at
the top. If the intent already exists, the method returns an empty ChangeSet.
This is checking the intent DB, not the projection or the device.

### Intent Records on the ChangeSet

NEWTRON_INTENT is newtron bookkeeping. Intent writes do NOT flow through
`op()`. Instead, `writeIntent`/`deleteIntent` directly call
`cs.Prepend`/`cs.Add`/`cs.Delete` on the same ChangeSet that the config
operation builds. Intent records are prepended so they reach Redis before
the CONFIG_DB entries they describe.

`renderIntent` updates `configDB.NewtronIntent` so subsequent
operations in the same episode can
read intent state (parent checks, idempotency guards, child enumeration).

Golden rule: every param that affects CONFIG_DB output must be stored
in the intent record. If it's not in the intent, the reverse operation
can't find it, and reconstruction produces different state than the
original operation.

---

## 5. Rendering

`render(cs)` is the mechanism that updates the projection from a
ChangeSet:

```go
func (n *Node) render(cs *ChangeSet) error {
    if err := cs.Validate(); err != nil {  // schema check every entry
        return err
    }
    for _, c := range cs.Changes {
        if c.Type == sonic.ChangeTypeDelete {
            n.configDB.DeleteEntry(c.Table, c.Key)
        } else {
            n.configDB.ApplyEntries([]sonic.Entry{{Table: c.Table, Key: c.Key, Fields: c.Fields}})
        }
    }
    return nil
}
```

This runs on every path — replay, interactive, online, offline. It is the
single mechanism that keeps the projection in sync with the intent DB.

### `op()` internals

1. **Precondition check** — reads the intent DB to verify assumptions.
   Domain checks (`RequireVLANExists`, `RequireVRFNotExists`,
   `RequireVTEPConfigured`) check `n.GetIntent(resource)`, not the
   projection. Shared-resource checks (reference counting, membership)
   also scan intents. All operational decisions read the intent DB
   (§1 "Intent DB is the decision substrate").
2. **Generate entries** — calls the config function's `gen()`. Returns
   `[]sonic.Entry`.
3. **Build ChangeSet** — wraps entries with metadata (device, operation,
   timestamp).
4. **render(cs)** — calls `cs.Validate()` first (schema checks
   every entry, rejects invalid before touching projection). Then for each
   entry: add/modify updates configDB; delete removes from configDB.
5. **Returns ChangeSet** — not yet applied to Redis.

After `op()` returns, the caller decides what happens:

- **Replay** (`ReplayStep`): nothing more. `render` already updated
  the projection. The ChangeSet is discarded — rendering was the point.
- **Interactive** (`Execute`): calls `cs.Apply(n)` — Redis HSET/DEL
  for each entry (entries were pre-validated at render time).

---

## 6. Six Operations on Expected State

Every interaction with the Node's expected state is one of six operations.
Tree, Drift, Reconcile, and Save work in both topology and actuated modes.
Reload and Clear are topology-mode only.

### Tree

Read the intent DB → build intent DAG.

```go
func (n *Node) Tree() *spec.TopologyDevice
```

No device interaction. Works in both modes — intents exist from topology
replay (offline) or device intent replay (online). Returns the ordered
intent steps that built the current expected state.

### Drift

Compare projection vs device → drift entries.

```go
func (n *Node) Drift(ctx context.Context) ([]sonic.DriftEntry, error)
```

1. If not connected: `ConnectTransport(ctx)`
2. `expected := n.configDB.ExportRaw()` — the projection
3. `actual := n.conn.Client().GetRawOwnedTables(ctx)` — transient read
4. `return sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())`

No reconstruction. The projection IS expected state — export it directly.
Actual state is read transiently from Redis, never stored on the Node.

### Reconcile

Deliver the full projection to the device.

```go
func (n *Node) Reconcile(ctx context.Context) (*ReconcileResult, error)
```

1. If not connected: `ConnectTransport(ctx)`
2. Config reload (best-effort — reset to factory baseline)
3. `PingWithRetry` — poll Redis until available after reload
4. Lock
5. `configDB.ExportEntries()` → `ReplaceAll()` — atomic delivery
6. SaveConfig → persist to `/etc/sonic/config_db.json`
7. `EnsureUnifiedConfigMode` → restart bgp if switching to frrcfgd
8. Unlock

No separate "generate composite" step. The projection IS expected state,
already validated entry-by-entry at render time during intent replay.
`ExportEntries` + `ReplaceAll` deliver it to the device.

Factory fields (mac, platform, hwsku) survive because:
- ConfigReload restores them from `/etc/sonic/config_db.json`
- `ReplaceAll` only DELs keys for tables the Node manages

### Save

Persist the device's current intent DB back to the Abstract Topology.

Implemented as a composition: `Tree()` on the Node produces the intent
steps, then `SaveDeviceIntents()` on the TopologyProvisioner persists
them to `topology.json`. The handler composes these two calls.

1. `Tree()` → ordered intent steps (the device's current intent DB)
2. `SaveDeviceIntents(device, steps)` → update topology.json + persist

Save is to `topology.json` what SONiC's `config save` is to
`config_db.json` — it persists runtime state (device intents) to
durable storage (Abstract Topology) so it survives device replacement.

The Abstract Topology is both the initial intent source and the durable
intent store. The full lifecycle:

```
topology.json → Reconcile → Device → API mutations → Save → topology.json
```

**RMA recovery**: When a device is replaced — possibly with a different
platform — Reconcile from `topology.json` replays the saved intents
through the new device's profile. Intents are platform-agnostic ("VLAN
100", "VRF CUSTOMER", "service transit on Ethernet0"). The config
generators produce the correct CONFIG_DB for whatever platform the
replacement device runs. The intents don't change — only the
rendering does.

### Reload (topology mode only)

Discard unsaved intent changes, reload from `topology.json`.

Analogous to SONiC's `config reload` (reload from `config_db.json`).
Destroys the current topology node and rebuilds from the persisted
Abstract Topology. Any CRUD mutations not yet saved are discarded.
The `unsavedIntents` flag is naturally false after reconstruction
(ReplayStep does not set it), which unblocks switching to actuated mode.

```
newtron switch1 --topology intent reload
```

Implementation: actor-level operation. The handler closes the current
node and rebuilds from topology.json:

```
closeNode()
node = net.BuildTopologyNode(device)
```

Returns error in actuated mode — actuated intents live on the device,
not in a discardable in-memory buffer.

### Clear (topology mode only)

Clear all intents, producing an empty node.

Creates a fresh abstract node with ports registered but no intents
replayed. The intent DB is empty, the projection contains only PORT
entries. Combined with other operations:

- **Clear + Save**: Persists empty steps to `topology.json` — the
  device's entry has no intents.
- **Clear + Reconcile**: Pushes an empty projection to the device —
  `ReplaceAll` with only PORT entries clears all other owned tables.
- **Clear + CRUD**: Build up intents from scratch on an empty node.

```
newtron switch1 --topology intent clear
newtron switch1 --topology intent save            # persist empty
newtron switch1 --topology intent reconcile -x    # wipe device
```

Implementation: actor-level operation. The handler closes the current
node and builds an empty topology node:

```
closeNode()
node = net.BuildEmptyTopologyNode(device)  // ports only, no step replay
```

Returns error in actuated mode — use reverse operations (`DeleteVLAN`,
`RemoveService`, etc.) to remove individual intents from a live device.

---

## 7. Device I/O: Transient Observation

All device interaction is layered on top of expected state via a transport
connection. Device reads are transient — they produce local values, never
modify the intent DB or projection.

### ConnectTransport

SSH tunnel + Redis connection. **Projection stays unchanged.**

```
ConnectTransport(ctx)  →  conn ✓, intent DB unchanged, projection unchanged
```

Transport is additive — it enables device I/O without disturbing expected
state.

### Device I/O Operations

| Operation | What it does | Modifies intent DB? | Modifies projection? |
|-----------|-------------|--------------------|--------------------|
| **Apply** (`cs.Apply(n)`) | Write ChangeSet to Redis (HSET/DEL) | No | No (already rendered) |
| **Verify** (`cs.Verify(n)`) | Re-read from Redis, compare against ChangeSet | No | No |
| **Drift** (`Drift(ctx)`) | Read actual CONFIG_DB, diff against projection | No | No |
| **Observe** (`GetRoute`, `CheckBGPSessions`) | Read APP_DB/STATE_DB | No | No |

### Ping

Redis PING for connectivity check. Checks the wire without touching
expected state.

---

## 8. Lock

Lock acquires a distributed lock (Redis SETNX) for exclusive write access.
It does NOT refresh the projection from the device — the projection is
derived from intents, not from Redis.

Lock also performs:
- Legacy STATE_DB intent migration (reads Redis directly, not projection)

**Transport guard**: Lock, Apply, and Unlock are no-ops when `n.conn == nil`
(no transport connection exists). This is not a dual code path — it is the
I/O boundary respecting the absence of a wire. The check is at the I/O
method level (`if n.conn == nil { return nil }`), not in callers. Callers
never branch on mode.

### Drift Guard (actuated mode)

In actuated mode, Lock performs a **drift guard** before allowing writes:
compute drift between the projection (expected CONFIG_DB derived from the
device's actuated intents) and the actual device CONFIG_DB. If drift is
non-empty, Lock returns an error — the write is refused.

Drift means the device no longer matches what its own intents declare.
Writing new intents on top of a drifted foundation is unsafe: preconditions
and config generators reason about the projection, but the device doesn't
match the projection. The new intent would be correct in theory but applied
to a device that has already diverged.

The resolution is explicit: `Reconcile()` first (pushes the projection to
the device, eliminating drift), then retry the write. This ensures every
new intent is applied on a foundation where projection = device reality.

The drift guard applies only in actuated mode because:
- **Topology offline**: No device exists — nothing to drift from.
- **Topology online**: Topology is authoritative — Reconcile overwrites
  the device entirely. Drift is expected and intentional (the device may
  not yet match the topology).
- **Actuated online**: Device intents are authoritative — the device
  SHOULD match its own intents. Drift is unexpected and must be resolved
  before mutation.

```
Lock(ctx)
  ├─ if n.conn == nil: return nil (transport guard)
  ├─ Acquire Redis SETNX
  ├─ Legacy STATE_DB intent migration
  ├─ if actuated mode:
  │    ├─ expected := configDB.ExportRaw()
  │    ├─ actual := conn.GetRawOwnedTables(ctx)
  │    ├─ drift := DiffConfigDB(expected, actual, OwnedTables())
  │    └─ if len(drift) > 0: Unlock() + return error("drift detected, reconcile first")
  └─ return nil
```

Actor serialization ensures one writer per device. After writes,
`render(cs)` keeps the projection in sync. External CONFIG_DB edits
are drift — detected by `Drift()`, fixed by `Reconcile()`.

### RebuildProjection — Projection Freshness

Lock does not refresh the projection. Instead, the actor layer calls
`RebuildProjection(ctx)` in `execute()` — the single entry point for
all operations (reads and writes). This runs BEFORE the operation
function, ensuring every operation sees fresh, authoritative state.

`RebuildProjection` re-reads NEWTRON_INTENT from the device (when
connected), creates a fresh configDB, re-registers ports, and replays
all intents to reconstruct the projection from scratch:

```
RebuildProjection(ctx)
  ├─ if connected: fresh intents := client.GetRawTable("NEWTRON_INTENT")
  ├─ else: intents := configDB.NewtronIntent (keep in-memory)
  ├─ ports := configDB.ExportPorts()
  ├─ configDB = NewConfigDB()          ← fresh projection
  ├─ RegisterPort() for each port
  ├─ configDB.NewtronIntent = intents
  ├─ IntentsToSteps(intents) → topological sort
  └─ ReplayStep() for each → intent DB + projection rebuilt
```

Expected state is derived from intents every time, which is
correct regardless of what external changes happened on the device.

RebuildProjection is idempotent and safe for both reads and writes.
For reads, it ensures the projection reflects the latest intents. For
writes, it ensures preconditions check against authoritative state.
The drift guard in Lock then compares this authoritative projection
against the actual device, catching any external CONFIG_DB mutations.

### Execute — Write Path with Dry-Run Support

`Execute()` is the public write entry point — called by `connectAndExecute`
in the actor layer after `execute()` has already called `RebuildProjection`.
It wraps the write operation with Lock/Unlock and supports dry-run via
intent snapshot/restore:

```
Execute(ctx, opts, fn)
  ├─ Lock(ctx)               ← Redis SETNX + drift guard
  ├─ snapshot := SnapshotIntentDB()
  ├─ fn(ctx)                 ← config methods: writeIntent + render
  │
  ├─ if error or dry-run:
  │    ├─ RestoreIntentDB(snapshot)   ← intent DB restored to pre-fn state
  │    └─ pending = nil               ← ChangeSet discarded
  │    (dirty projection cleaned by next execute()'s RebuildProjection)
  │
  ├─ if opts.Execute:
  │    └─ Commit(ctx)         ← cs.Apply(n) → Redis HSET/DEL
  │
  └─ Unlock()
```

**Dry-run correctness**: `fn()` modifies the intent DB (via `writeIntent`)
and projection (via `render`). On dry-run, `RestoreIntentDB` puts the
intent DB back to its pre-fn state. The projection remains dirty, but
the NEXT call to `execute()` will call `RebuildProjection`, which
rebuilds the projection from the (now-restored) intent DB. Between
operations, actor serialization ensures no concurrent access to the
dirty projection.

**Three operation flows in the actor:**

| Flow | Actor method | Pattern |
|------|-------------|---------|
| Read | `connectAndRead` | execute → RebuildProjection → Ping → fn |
| Write | `connectAndExecute` | execute → RebuildProjection → Execute(Lock → snapshot → fn → commit → Unlock) |
| Dry-run | `connectAndExecute` | execute → RebuildProjection → Execute(Lock → snapshot → fn → restore → Unlock) |

All three go through `execute()` — the ONE entry point. Mode dispatch
(`ensureActuatedIntent` / `ensureTopologyIntent`) happens once in
`execute()`, not in callers.

### Crash Recovery via Drift Guard + Reconcile

The drift guard and Reconcile handle every crash scenario:

| Crash scenario | What's on device | InitFromDeviceIntent result | Resolution |
|---|---|---|---|
| Intent written, entries partially applied | NEWTRON_INTENT exists, some CONFIG_DB entries | Projection includes intent's entries | Drift guard detects gap → Reconcile pushes full projection |
| Intent not written (crash before any Redis write) | Nothing changed | Projection unchanged | No drift, no action needed |
| All entries applied, intent exists (crash before delete) | Full CONFIG_DB entries + intent | Projection matches | No drift — intent was fully applied |

In every case:
1. `InitFromDeviceIntent` replays whatever NEWTRON_INTENT records exist
   on the device → projection reflects the declared intents
2. **Drift guard** in `Lock()` compares projection against actual
   CONFIG_DB → if they differ, Lock refuses writes
3. **Reconcile** pushes the full projection → device matches intents
4. If the user wants to undo the partially-applied intent, they call the
   normal reverse operation (`DeleteVLAN`, `RemoveService`, etc.)

NEWTRON_INTENT records serve as the persistent state that crash recovery
needs — they ARE what was applied. The projection derived from these
intents IS the expected state. Drift detects when device ≠ projection.
Reconcile fixes it.

**Prepend ordering is load-bearing for crash recovery.** `writeIntent`
uses `cs.Prepend()` to place the NEWTRON_INTENT record before config
entries in the ChangeSet. When `cs.Apply` writes to Redis, the intent
record reaches the device first. This ordering is not a wire-format
convention — it is a deliberate crash-recovery property:

- **Intent survives, entries partially applied** (crash mid-Apply):
  The NEWTRON_INTENT record exists on the device because it was written
  first. `InitFromDeviceIntent` replays it → projection includes all
  entries the intent would produce → drift shows **missing records**
  (entries the projection expects but the device doesn't have) →
  Reconcile completes delivery.

- **Intent does not survive** (crash before any Redis write): Nothing
  reached the device. Projection unchanged, no drift, no action needed.

- **Orphaned entries without intent** (impossible with Prepend ordering,
  but possible if ordering were reversed): Config entries on the device
  with no NEWTRON_INTENT claiming them → drift shows **additional
  records** (entries the device has but the projection doesn't expect)
  → Reconcile removes them.

The two drift kinds carry implicit signal: **missing records** mean an
intent exists but delivery was incomplete — Reconcile finishes the job.
**Additional records** mean no intent claims them — Reconcile cleans
them up. Prepend ordering ensures that a surviving intent always
accompanies its entries, never the reverse. This eliminates the
ambiguous case where entries exist without the intent that created them.

---

## 9. Data Representations

Data exists in three forms as it moves through the system:

| Form | Where | Purpose |
|------|-------|---------|
| Intent record | `configDB.NewtronIntent` | Primary state — what should be configured |
| Typed struct | `configDB.VLAN`, `configDB.VRF`, etc. | Projection — rendered from intent replay |
| `map[string]string` | Redis hashes, `Entry.Fields` | Wire format — what Redis speaks |

Three mechanisms bridge these:

| Mechanism | Direction | Where it runs |
|-----------|-----------|---------------|
| `configTableHydrators` | wire → struct | `render` (all paths), `GetAll` (device read) |
| `structToFields` | struct → wire | `ExportEntries` (within Drift/Reconcile) |
| `schema.Validate` | wire → pass/fail | `render` (all paths, both modes) |

### Hydration Registry

`configTableHydrators` is the central bridge. Every path that populates
a ConfigDB struct goes through this registry:

- **Render path**: `render` calls it to update configDB after each
  operation (both modes)
- **Device read**: `GetAll` calls it when loading `sonic.Device.ConfigDB`
  (the actual-state cache used by `InitFromDeviceIntent` to extract intents)

The reverse direction (`structToFields`) is reflection-based — it reads
json tags from struct definitions. It runs when `ExportEntries` or
`ExportRaw` serializes configDB for delivery or comparison.

### Schema Validation

Validation runs at render time — before any entry enters the
projection — on all paths. If a config function produces an invalid entry,
the error is caught before the projection, before Redis, before delivery.

`cs.Apply(n)` does NOT re-validate — entries were validated when they
entered the projection. `Reconcile` does not re-validate — entries were
validated during intent replay.

---

## 10. End-to-End Traces

### Interactive: `newtron -D switch1 vlan create --vlan-id 100`

```
CLI parses command
  │
  ▼
NodeActor.execute(ctx, fn)
  │
  ├─ ensureActuatedIntent(ctx)
  │    └─ InitFromDeviceIntent → intent DB + projection populated
  │
  ├─ RebuildProjection(ctx)
  │    └─ Re-read NEWTRON_INTENT from Redis → fresh configDB → ReplayStep each
  │       (projection now reflects latest device intents)
  │
  └─ fn() → connectAndExecute → node.Execute(ctx, opts, fn)
       │
       ├─ Lock(ctx)
       │    ├─ Redis SETNX
       │    └─ Drift guard (actuated): projection vs actual → must be clean
       │
       ├─ snapshot := SnapshotIntentDB()
       │
       ├─ fn(ctx) → node.CreateVLAN(100, opts)
       │    │
       │    ├─ GetIntent("vlan|100") → nil (not yet created)
       │    │
       │    ├─ op("create-vlan", ...)
       │    │    ├─ precondition: GetIntent("vlan|100") → nil ✓
       │    │    ├─ gen(): createVlanConfig(100, opts) → []Entry
       │    │    ├─ buildChangeSet(entries)
       │    │    └─ render(cs) → projection updated
       │    │
       │    ├─ writeIntent(cs, "create-vlan", "vlan|100", params, ["device"])
       │    │    ├─ cs.Prepend() → intent entry first in ChangeSet
       │    │    └─ renderIntent → intent DB updated
       │    │
       │    └─ return ChangeSet
       │
       ├─ if opts.Execute:
       │    └─ Commit(ctx) → cs.Apply(n) → Redis HSET
       │       (NEWTRON_INTENT first, then VLAN, VXLAN_TUNNEL_MAP)
       │
       ├─ if dry-run:
       │    └─ RestoreIntentDB(snapshot) → intent DB restored
       │       (dirty projection cleaned by next execute()'s RebuildProjection)
       │
       └─ Unlock()
```

### Topology Reconcile: `newtron switch1 --topology intent reconcile -x`

```
CLI parses command
  │
  ▼
NodeActor.execute(ctx, fn)
  │
  ├─ ensureTopologyIntent()
  │    │
  │    ├─ New(specs, "switch1", profile, resolved)
  │    │    intent DB = empty, projection = empty
  │    │
  │    ├─ RegisterPort("Ethernet0", fields)
  │    │    projection: PORT["Ethernet0"] populated
  │    │
  │    ├─ ReplayStep: setup-device
  │    │    └─ SetupDevice(ctx, opts)
  │    │         ├─ writeIntent → intent DB: "device" added
  │    │         ├─ op() → render → projection updated
  │    │         └─ ChangeSet discarded (replay)
  │    │
  │    └─ ReplayStep: apply-service on Ethernet0
  │         └─ iface.ApplyService(ctx, "transit", opts)
  │              ├─ writeIntent → intent DB: "interface|Ethernet0" added
  │              ├─ sub-operations each: op() → render → projection updated
  │              └─ ChangeSet discarded (replay)
  │
  ├─ RebuildProjection(ctx)
  │    └─ no conn → uses in-memory intents → rebuild projection (no-op on
  │       fresh topology node, but structurally consistent with all paths)
  │
  └─ fn() → Reconcile(ctx)
       │
       ├─ ConnectTransport(ctx) → SSH + Redis (projection unchanged)
       ├─ ConfigReload (best-effort)
       ├─ PingWithRetry
       ├─ Lock (no drift guard — topology mode)
       ├─ configDB.ExportEntries() → []Entry (pre-validated at render time)
       │    └─ ReplaceAll() → DEL stale + HSET all (MULTI/EXEC atomic)
       ├─ SaveConfig
       ├─ EnsureUnifiedConfigMode
       └─ Unlock
```

### Intent Drift: `newtron switch1 intent drift`

```
CLI parses command
  │
  ▼
NodeActor.execute(ctx, fn)
  │
  ├─ ensureActuatedIntent(ctx)
  │    └─ InitFromDeviceIntent → intent DB + projection from device intents
  │
  ├─ RebuildProjection(ctx)
  │    └─ Re-read NEWTRON_INTENT from Redis → fresh configDB → ReplayStep each
  │       (catches any intents written by other actors since last operation)
  │
  └─ fn() → connectAndRead → Ping(ctx) + Drift(ctx)
       │
       ├─ expected := configDB.ExportRaw()    ← the projection
       ├─ actual := conn.GetRawOwnedTables()  ← transient read from Redis
       └─ return DiffConfigDB(expected, actual, OwnedTables())
```

---

## 11. Deviations from DESIGN_PRINCIPLES

This architecture is authoritative. The following DESIGN_PRINCIPLES
assertions are superseded by this document.

### Authority model (Principle 5 "Specs Are Intent; The Device Is Reality")

The principle asserts "the device CONFIG_DB is ground reality" and
"newtron does not fight it or try to reconcile back to spec." The
architecture inverts this:

- **Intent DB is primary state.** The projection (expected CONFIG_DB)
  is derived from intents, not loaded from the device.
- **Preconditions check intents**, not CONFIG_DB. `RequireVLANExists`
  calls `GetIntent("vlan|100")`, not `configDB.VLAN`.
- **Idempotency checks intents.** Each config method checks
  `GetIntent(resource)` at the top, not CONFIG_DB tables.
- **A canonical desired state exists**: the projection. The principle's
  "no single canonical desired state" is replaced by "the projection
  IS the desired state."
- **Drift guard refuses writes** when device CONFIG_DB diverges from
  the projection (actuated mode). The principle's "newtron does not
  fight external edits" is replaced by "newtron detects external edits
  and refuses to operate on an inconsistent foundation."
- **Reconcile overwrites the device** to match the projection. The
  principle's "Do NOT implement a desired-state reconciler" is replaced
  by Reconcile as a first-class operation.

The nuance: in actuated mode, intents come FROM the device's own
NEWTRON_INTENT records. "Intent is reality" means "device intents are
reality" — not "specs override the device." External CONFIG_DB edits
are drift from what the device's own intents declare.

### Episodic caching (Principle 34/35)

The spirit of episodic caching is preserved — each operation sees
fresh state — but the mechanism is `RebuildProjection`:

- Lock acquires the lock only — no configDB refresh.
- `Ping()` checks connectivity only — no projection overwrite.
- **`RebuildProjection(ctx)`** in `execute()` provides freshness:
  re-reads NEWTRON_INTENT from the device (when connected), rebuilds
  the projection from scratch via intent replay. This runs before
  every operation — reads and writes alike.
- The projection is derived from intents, never overwritten from the
  device.

### Mechanism descriptions

The following DESIGN_PRINCIPLES sections describe the correct intent
but their mechanism descriptions are superseded:

| Principle | Current mechanism |
|-----------|-----------------|
| §1 "The Node" | configDB from intent replay, never from Redis |
| §2 "Three Properties" | Drift: export projection, transient Redis read, diff |
| §13 "Prevent Bad Writes" | Validation at `render()` time |
| §19 "Unified Intent" | On connect, node replays intents via IntentsToSteps + ReplayStep |
| §20 "On-Device Intent" | Projection IS expected state; no separate generation step |
| §21 "Reconstruct, Don't Record" | Projection IS expected state from construction |

### Crash recovery (Principle 15, Principle 17, Principle 19)

Crash recovery is structural: NEWTRON_INTENT records ARE the persistent
state, the drift guard detects inconsistency, Reconcile fixes it.

- **§15 "Never enter a state you can't recover from"**: The safety net
  is the drift guard, not a pre-operation breadcrumb.
- **§15 "Structural proof over heuristic detection"**: Lock in actuated
  mode structurally proves whether projection matches device.
- **§17 "Operation Granularity"**: Partial completion is handled
  uniformly by drift guard + Reconcile regardless of granularity.
- **§19 "Unified Intent Model"**: Intents have two states: they exist
  in the intent DB or they don't. A partially-applied intent is drift,
  handled by Reconcile.

---

## 12. Glossary

| Term | Definition |
|------|-----------|
| **Abstract Topology** | The topology.json spec — network-level intent declaring what devices, ports, and steps should exist. Container of Abstract Nodes. |
| **Abstract Node** | A Node whose intent DB and projection are populated from intent replay, not from a device. Created by `NewAbstract()`. Used in both topology and online modes. |
| **Intent** | A NEWTRON_INTENT record declaring what should be configured |
| **Intent DB** | The collection of intents in `configDB.NewtronIntent` |
| **Projection** | The typed CONFIG_DB tables derived from intent replay |
| **Render** | Update the projection from a ChangeSet (validate + apply entries) |
| **Replay** | Execute a config function for an intent, producing entries that get rendered |
| **Drift** | Difference between projection (expected) and device (actual) |
| **Reconcile** | Deliver the full projection to the device, eliminating drift |
| **Save** | Persist the device's current intent DB back to `topology.json` — durable intent storage |
| **Reload** | Discard unsaved topology changes, rebuild from `topology.json` — analogous to SONiC `config reload` |
| **Clear** | Delete all intents, produce an empty node with ports only — topology mode only |
| **RebuildProjection** | Re-read intents (from device when connected, from memory when offline), create fresh configDB, replay all intents. Called in `execute()` before every operation |
| **Execute** | Public write entry point: Lock → snapshot → fn → commit-or-restore → Unlock. Supports dry-run via intent snapshot/restore |
| **Transport** | SSH + Redis connection layered on top of expected state |
