# Newtron High-Level Design (HLD)

## 1. Purpose

Newtron defines architectural primitives for SONiC networks and automates any network built from them. It treats SONiC as what it is — a Redis database with daemons that react to table changes — and interacts with it accordingly. Where other tools SSH in and parse CLI output, newtron reads and writes CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB directly through an SSH-tunneled Redis client.

Newtron is intent-first. Each device's primary state is its **intent DB** — the set of NEWTRON_INTENT records declaring what should be configured. The **projection** (expected CONFIG_DB) is derived by replaying intents through config functions. It is not a cache of Redis, not a copy of anything — it is the consequence of what the intents declare. The device's actual CONFIG_DB should match the projection; when it doesn't, that's drift.

In actuated mode, intents come from the device's own NEWTRON_INTENT records — "intent is reality" means "device intents are reality," not "specs override the device." External CONFIG_DB edits are drift from what the device's own intents declare. The drift guard refuses writes on a drifted foundation; Reconcile fixes it by pushing the full projection to the device.

For the architectural principles behind newtron, newtlab, and newtrun — including the object hierarchy, verification ownership, and DRY design — see [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md). For the full pipeline specification with end-to-end traces, see [Unified Pipeline Architecture](unified-pipeline-architecture.md).

## 2. System Architecture

```
                                                  ┌─────────────────────────────────┐
                                                  │                                 │
                                                  │               CLI               │
                                                  │          (cmd/newtron)          │
                                                  │           HTTP client           │
                                                  │                                 │
                                                  └─────────────────────────────────┘
                                                    │
                                                    │ HTTP
                                                    │ (REST API)
                                                    ▼
┌─────────────────────────┐                       ┌─────────────────────────────────┐
│                         │                       │                                 │
│         newtrun         │                       │         newtron-server          │
│     (pkg/newtrun/)      │  HTTP                 │       (pkg/newtron/api/)        │
│       HTTP client       │  (REST API)           │                                 │
│                         │ ────────────────────▶ │                                 │
└─────────────────────────┘                       └─────────────────────────────────┘
                                                    │
                                                    │
                                                    ▼
┌─────────────────────────┐                       ┌─────────────────────────────────┐
│                         │                       │                                 │
│        NodeActor        │                       │          networkEntity          │
│     (1 per device)      │                       │         (1 per network)         │
│ caches *Node (SSH conn) │  manages              │          owns *Network          │
│                         │ ◀──────────────────── │                                 │
└─────────────────────────┘                       └─────────────────────────────────┘
  │                                                 │
  │                                                 │
  │                                                 ▼
  │                                               ┌─────────────────────────────────┐     ┌─────────────────────┐
  │                                               │                                 │     │                     │
  │                                               │          Network Layer          │     │                     │
  │                                               │     (pkg/newtron/network/)      │     │     Spec Layer      │
  │                                               │        Spec resolution,         │     │ (pkg/newtron/spec/) │
  │                                               │      topology provisioning      │     │                     │
  │                                               │                                 │ ──▶ │                     │
  │                                               └─────────────────────────────────┘     └─────────────────────┘
  │                                                 │
  │                                                 │
  │                                                 ▼
  │                                               ┌─────────────────────────────────┐
  │                                               │                                 │
  │                                               │           Node Layer            │
  │                                               │   (pkg/newtron/network/node/)   │
  │                                               │        Node, Interface,         │
  │                         ConnectTransport()    │       ChangeSet, *_ops.go       │
  └─────────────────────────────────────────────▶ │                                 │
                                                  └─────────────────────────────────┘
                                                    │
                                                    │
                                                    ▼
                                                  ┌─────────────────────────────────┐
                                                  │                                 │
                                                  │          Device Layer           │
                                                  │   (pkg/newtron/device/sonic/)   │
                                                  │    SSH Tunnel > ConfigDB(4)     │
                                                  │ StateDB(6), AppDB(0), AsicDB(1) │
                                                  │                                 │
                                                  └─────────────────────────────────┘
                                                    │
                                                    │
                                                    ▼
                                                  ┌─────────────────────────────────┐
                                                  │                                 │
                                                  │          SONiC Switch           │
                                                  │             (Redis)             │
                                                  │                                 │
                                                  └─────────────────────────────────┘
```

### 2.1 Layer Responsibilities

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **Public API** | `pkg/newtron/` | Entry point for all consumers; wraps internal types in domain vocabulary |
| **Network** | `pkg/newtron/network/` | Spec loading, spec resolution (network→zone→node inheritance), topology provisioning |
| **Node** | `pkg/newtron/network/node/` | Node, Interface, ChangeSet, all `*_ops.go` operations |
| **Spec** | `pkg/newtron/spec/` | JSON file I/O for specs (network.json, profiles, platforms, topology) |
| **Device** | `pkg/newtron/device/sonic/` | SSH tunnel, Redis clients (ConfigDB, StateDB, AppDB, AsicDB). Pure connection infrastructure — no domain logic |

### 2.2 Object Hierarchy

The governing principle: **a method belongs to the smallest object that has all the context to execute it.**

```
┌────────────────────┐
│                    │
│      Network       │
│  (owns all specs)  │
│                    │
└────────────────────┘
  │
  │ creates
  │ (parent ref)
  ▼
┌────────────────────┐
│                    │
│        Node        │
│  (device handle)   │
│                    │
└────────────────────┘
  │
  │ creates
  │ (parent ref)
  ▼
┌────────────────────┐
│                    │
│     Interface      │
│ (interface handle) │
│                    │
└────────────────────┘
```

Whatever configuration can be right-shifted to the interface level, should be. eBGP neighbors are interface-specific — they derive from the interface's IP and the service's peer AS — so they are created by `Interface.ApplyService()`. Overlay peering is device-specific — it derives from the device's profile and EVPN peers — so it lives on `Node.SetupEVPN()`.

### 2.3 Per-Network Registration and Per-Device Actors

**newtron-server** is the central process. It manages network specs and device connections, and exposes all operations as an HTTP REST API. The CLI and newtrun are HTTP clients — they import `pkg/newtron/client/` and shared types, never internal packages.

| Type | Scope | Owns | One Per |
|-------|-------|------|---------|
| `networkEntity` | Per-network registration | `*newtron.Network`, the NodeActor cache | Registered network |
| `NodeActor` | Device operations | Cached `*newtron.Node`, `*Network` ref from parent | Device name |

`networkEntity` is the per-network record in the API server. It owns the engine `*Network` and a NodeActor cache. It is **not an actor** — it has no goroutine, no message channel, no isolated state for spec serialization. Spec atomicity is the engine layer's responsibility (see §8 Concurrency model). The API layer holds no spec lock of its own; handlers call `ne.net.X()` directly.

`NodeActor` is a real actor: it owns a cached `*Node` (an SSH session + Redis tunnels) and an idle timer. Cached connections are reused across requests with a configurable idle timeout (default 5 minutes), eliminating ~200ms SSH overhead per request. Operations on the same device serialize through the actor's goroutine — essential when each operation involves multi-step SSH/Redis round-trips that must not interleave.

Every device-operation request goes through `NodeActor.execute()`, which calls `RebuildProjection` before the operation function. This re-reads NEWTRON_INTENT from the device (when connected), rebuilds the projection from scratch, and ensures every operation sees fresh, authoritative state.

Two dispatch patterns:

| Pattern | Lifecycle | Used By |
|---------|----------|---------|
| `connectAndRead` | execute → RebuildProjection → Ping → fn | Show, list, status, health, drift |
| `connectAndExecute` | execute → RebuildProjection → Execute(Lock→snapshot→fn→commit-or-restore→Unlock) | All mutating operations |

HTTP handlers follow a two-step resolution: server → networkEntity (by network ID) → NodeActor (by device name). Spec operations call `ne.net.X()` directly. Device operations dispatch to the NodeActor.

## 3. Intent Pipeline

At the node level, all data flows through one pipeline:

```
Intent → Replay → Render → [Deliver]
```

1. **Intent source**: Intents come from topology.json steps (topology mode) or device NEWTRON_INTENT records (actuated mode).
2. **Replay**: `IntentsToSteps` → `ReplayStep` calls config methods. Each config method writes its intent record and generates CONFIG_DB entries.
3. **Render**: `render(cs)` validates entries against the YANG-derived schema and updates the typed CONFIG_DB tables (the projection).
4. **Deliver** (optional): `cs.Apply(n)` for interactive writes, `ReplaceAll()` for Reconcile. Skipped during replay — rendering is the point.

### 3.1 Three Data Stores

| Store | What it holds | Authoritative? |
|-------|--------------|----------------|
| **Intent DB** | NEWTRON_INTENT records — what should be configured | Yes — primary state, decision substrate for all operational logic |
| **Projection** | Typed CONFIG_DB tables derived from intent replay | Derived — exists for device delivery and drift detection only |
| **Device** | Actual CONFIG_DB in Redis | Observed — transient reads, never stored on the Node |

All operational decisions — preconditions, idempotency guards, reference counting, membership checks — read the intent DB, not the projection. The projection is the SONiC-specific rendering of domain-level intents. Operational logic speaks domain; only the delivery and drift layers speak SONiC.

### 3.2 Config Methods

Each config method (CreateVLAN, ConfigureBGP, ApplyService, etc.) does two things on the same ChangeSet:

1. **Writes intent**: `writeIntent(cs, op, resource, params, parents)` → `cs.Prepend()` puts the intent record first, `renderIntent()` updates the intent DB immediately so subsequent intents can see parents.
2. **Generates entries**: `op()` runs preconditions → calls the config generator → `render(cs)` validates and updates the projection.

By return, the intent DB and projection are both updated, and the ChangeSet contains intent records (prepended) + config entries. The caller decides what happens with the ChangeSet:

- **Replay** (`ReplayStep`): ChangeSet discarded — rendering was the point.
- **Interactive** (`Execute`): `cs.Apply(n)` writes to Redis.
- **Reconcile**: Full projection delivered via `ExportEntries()` + `ReplaceAll()`.

Config generators themselves are pure functions — they take parameters and return `[]sonic.Entry` with no side effects. Intent management and rendering happen in the wrapping methods. Intent-idempotency: each config method checks `n.GetIntent(resource)` at the top; if the intent already exists, the method returns an empty ChangeSet.

### 3.3 Three Node States

The Node exists in one of three states. The pipeline is the same in all three — only the intent source and authority direction differ.

| State | Intent source | Authority | Operations |
|-------|--------------|-----------|------------|
| **Topology offline** | topology.json | topology → device | Tree, Drift (auto-connects), Reconcile (auto-connects), Save, Reload, Clear, CRUD |
| **Topology online** | topology.json | topology → device | Tree, Drift, Reconcile, Save, Reload, Clear |
| **Actuated online** | device NEWTRON_INTENT | device → topology | Tree, Drift, Reconcile (drift repair), Save, CRUD (drift guard: refuse if drifted) |

**Topology offline**: The abstract node is initialized from `topology.json`. New intents are created via the API. Save persists them back. No device connection.

**Topology online**: The same topology-sourced node, now with transport connected via `ConnectTransport()`. Reconcile delivers the full projection to the device. The topology is authoritative — the device should match it.

**Actuated online**: The abstract node is initialized from the device's NEWTRON_INTENT records via `InitFromDeviceIntent()`. API mutations create/modify intents which are delivered to the device. Save persists the device's current intents to `topology.json`. The device intents are authoritative.

Node construction:

| Source | Construction |
|--------|--------------|
| topology.json | `NewAbstract()` → `RegisterPort()` → `ReplayStep()` for each step |
| Device intents | `NewAbstract()` → `ConnectTransport()` → read PORT + NEWTRON_INTENT → `RegisterPort()` → `IntentsToSteps()` → `ReplayStep()` |

Both paths produce a Node whose intent DB and projection are populated. After replay, all operations work identically regardless of which source initialized the node.

State transitions:
- **Topology offline → topology online**: `ConnectTransport` adds the wire. Intent DB and projection unchanged.
- **Topology offline → actuated online**: `ensureActuatedIntent` closes the offline node, creates a new one from device intents. Guarded: refuses if topology node has unsaved intents.
- **Actuated online → topology offline**: `ensureTopologyIntent` closes the online node, creates a new one from `topology.json`.

### 3.4 Six Operations on Expected State

| Operation | What it does | Requires device? |
|-----------|-------------|-----------------|
| **Tree** | Read the intent DB → return intent DAG | No |
| **Drift** | Compare projection vs actual CONFIG_DB → return differences | Yes (auto-connects) |
| **Reconcile** | Deliver the projection to the device. Two modes: **full** (config reload + `ExportEntries` + `ReplaceAll`); **delta** (`DiffConfigDB` + `ApplyDrift`, patches only drifted entries). Default: full for topology nodes, delta for actuated nodes. | Yes (auto-connects) |
| **Save** | `Tree()` → `SaveDeviceIntents()` — persist intent DB to `topology.json` | No |
| **Reload** | Discard unsaved changes, rebuild from `topology.json` (topology mode only) | No |
| **Clear** | Delete all intents, produce empty node with ports only (topology mode only) | No |

Reconcile is the delivery mechanism for both initial provisioning and drift repair. Two modes serve different contexts: **full** reloads CONFIG_DB from disk (factory baseline) then delivers the complete projection via `ReplaceAll()` — factory fields (mac, platform, hwsku) survive because `ReplaceAll` only DELs keys for tables the Node manages; **delta** computes the diff between projection and actual device CONFIG_DB via `DiffConfigDB` then patches only the drifted entries via `ApplyDrift`, leaving the rest untouched. The default is full for topology nodes (day-1 delivery) and delta for actuated nodes (drift repair). Both modes can be overridden via `--full`/`--delta` CLI flags or `?reconcile=full|delta` query parameter.

### 3.5 Topology Provisioning

The topology provisioner (`TopologyProvisioner`) creates abstract nodes from `topology.json` and delivers them via Reconcile:

```
topology.json → BuildAbstractNode(device) → Abstract Node
                                              ├─ Intent DB: populated from topology steps
                                              └─ Projection: rendered CONFIG_DB
                                                    │
                                              Reconcile → Device
```

`BuildAbstractNode` calls the same methods the CLI uses (`SetupDevice`, `iface.ApplyService`, etc.) on the abstract node. `Reconcile` delivers the accumulated projection atomically.

This unifies day-1 and day-2 into the same workflow:

```
Create intents (API) → Save (topology.json) → Reconcile (device)
                                                    │
                                              API mutations (day-2)
                                                    │
                                              Save (topology.json)
```

**RMA recovery** follows naturally: Reconcile from the saved `topology.json` replays intents through the replacement device's profile. Intents are platform-agnostic; only the rendering differs.

## 4. Spec Resolution

Specs describe **what you want** — declarative, abstract, policy-driven. They name service types, VPN references, filter references, and routing intent. They do not contain concrete device values (peer IPs, VRF names, ACL rule numbers) — those are derived at runtime by combining a spec with device context.

| In Spec (Declarative) | Derived at Runtime |
|-----------------------|--------------------|
| Service type (routed, bridged, irb, evpn-*) | VRF name |
| VPN reference (ipvpn, macvpn) | Peer IP (from interface IP) |
| Routing protocol (bgp, static) | ACL table name |
| Peer AS policy ("request" or fixed) | ACL rule sequence numbers |
| Filter-spec reference | Local AS (from device profile) |
| Route policy references | Router ID (from loopback IP) |

Translation follows a three-layer pattern in the Node layer:

1. **Config functions** — pure functions in each `*_ops.go` file that return `[]sonic.Entry`. No side effects.
2. **`service_gen.go`** — translates a service spec into CONFIG_DB entries by calling config functions from owning `*_ops.go` files.
3. **Operations** — methods on Interface/Node that run preconditions, call generators, and wrap results in a ChangeSet.

### 4.1 Hierarchical Spec Resolution

Specs participate in a three-level hierarchy: **network → zone → node**. Each level can define or override any of the 7 overridable spec types (services, filters, IP-VPNs, MAC-VPNs, QoS policies, route policies, prefix lists).

Resolution is **union with lower-level-wins**: if the same spec name exists at multiple levels, the most specific level wins (node > zone > network). Specs at different levels with different names are all visible.

```
┌──────────────────────────┐
│                          │
│         Network          │
│      (network.json)      │
│                          │
└──────────────────────────┘
  │
  │ overrides
  ▼
┌──────────────────────────┐
│                          │
│           Zone           │
│      (zones.{name})      │
│                          │
└──────────────────────────┘
  │
  │ overrides
  ▼
┌──────────────────────────┐
│                          │
│          Node            │
│ (profiles/{device}.json) │
│                          │
└──────────────────────────┘
  │
  │ resolves to
  ▼
┌──────────────────────────┐
│                          │
│     ResolvedSpecs        │
│        (runtime)         │
│                          │
└──────────────────────────┘
```

At runtime, `buildResolvedSpecs()` merges all three levels into a `ResolvedSpecs` snapshot per node. This snapshot implements the `SpecProvider` interface used by all node operations — lookups fall through from node to zone to network until a match is found.

Specs are network-scoped; execution is device-scoped. A service can be defined before any device connects, and a device can consume a service defined after it connected. Operations accept spec names (strings) and resolve them internally — callers never pre-resolve specs.

### 4.2 Service Model

Services are the primary abstraction — they bundle VPN, routing, filter, and QoS intent into reusable templates applied to interfaces. Six service types span local and overlay use cases. For the full service spec structure and per-type details, see the [LLD](lld.md) §2 and [HOWTO](howto.md) §5.

- **ApplyService** — translates spec + context into CONFIG_DB entries, applying them to the interface. Creates VRF, ACL, IP, BGP neighbor, EVPN mappings as needed.
- **RemoveService** — reverse of ApplyService. Reads the intent record to determine what was applied. Uses intent DAG `_children` to protect shared resources — scans for remaining consumers before deleting shared infrastructure.
- **RefreshService** — full remove+reapply cycle. The two ChangeSets merge, preserving intermediate DEL operations (required because Redis HSET merges fields, so DEL is needed to remove stale fields).

## 5. Device Connection

### 5.1 Transport

Redis on SONiC listens only on localhost. SSH is the transport security layer — all Redis access goes through an SSH tunnel with password credentials from the device profile.

```
┌────────────────────┐          ┌────────────┐            ┌────────────────┐
│                    │          │            │            │                │
│   ConfigDBClient   │          │            │            │   sshd (:22)   │
│ 127.0.0.1:<random> │          │ SSH Tunnel │            │ 127.0.0.1:6379 │
│                    │  local   │            │  forward   │    (Redis)     │
│                    │ ───────▶ │            │ ─────────▶ │                │
└────────────────────┘          └────────────┘            └────────────────┘
```

Four Redis clients are established: ConfigDB (DB 4), StateDB (DB 6), AppDB (DB 0), AsicDB (DB 1). StateDB/AppDB/AsicDB failures are non-fatal — the system can still read/write CONFIG_DB. Without SSH credentials (integration tests), the address points directly at a standalone Redis container.

`ConnectTransport()` establishes the SSH tunnel and Redis clients. The projection stays unchanged — transport is additive, enabling device I/O without disturbing expected state.

### 5.2 Projection Freshness (RebuildProjection)

The projection is derived from intents, not loaded from Redis. Freshness is provided by `RebuildProjection(ctx)`, called in `execute()` before every operation — reads and writes alike:

```
RebuildProjection(ctx)
  ├─ if transport exists (n.conn != nil): fresh intents from Redis
  ├─ else: intents from configDB.NewtronIntent (in-memory)
  ├─ ports := configDB.ExportPorts()
  ├─ configDB = NewConfigDB()          ← fresh projection
  ├─ RegisterPort() for each port
  ├─ configDB.NewtronIntent = intents
  ├─ IntentsToSteps(intents) → topological sort
  └─ ReplayStep() for each → intent DB + projection rebuilt
```

**Invariant:** Every operation sees a projection derived from the latest intents. When connected, intents are re-read from the device, catching changes made by other actors since the last operation. When offline, in-memory intents are replayed.

The write path wraps operations with Lock/Unlock and supports dry-run via intent snapshot/restore. On dry-run, `RestoreIntentDB` puts the intent DB back; the dirty projection is cleaned by the next `execute()`'s `RebuildProjection`.

### 5.3 Drift Guard

In actuated mode, `Lock()` performs a drift guard before allowing writes: it computes drift between the projection and the actual device CONFIG_DB. If drift is non-empty, Lock returns an error — the write is refused.

Drift means the device no longer matches what its own intents declare. Writing new intents on top of a drifted foundation is unsafe: preconditions and config generators reason about the projection, but the device doesn't match. The resolution is explicit: `Reconcile()` first, then retry the write.

The drift guard applies only in actuated mode because:
- **Topology offline**: No device exists — nothing to drift from.
- **Topology online**: Topology is authoritative — drift is expected (the device may not yet match the topology).
- **Actuated online**: Device intents are authoritative — drift is unexpected and must be resolved.

### 5.4 Unified Config Mode (frrcfgd)

SONiC ships with **bgpcfgd** by default — a daemon that processes only a subset of CONFIG_DB tables. newtron requires **frrcfgd** (unified config mode), which translates all CONFIG_DB tables to FRR commands.

Three-layer enforcement ensures frrcfgd is always active:

| Layer | When | How |
|-------|------|-----|
| **Boot patch** | VM deploy (newtlab) | Redis HSET + bgp restart |
| **`newtron init`** | Before first use | `SetDeviceMetadata` + bgp restart + config save |
| **Connect-time check** | Every `ConnectTransport()` | Reads `DEVICE_METADATA\|localhost` `docker_routing_config_mode` |

Reconcile includes frrcfgd fields in the projection and runs `EnsureUnifiedConfigMode` after delivery, so `newtron init` is unnecessary for topology-provisioned devices.

### 5.5 Config Persistence

SONiC uses a dual-state model: Redis CONFIG_DB (runtime, immediate) and `/etc/sonic/config_db.json` (persistent, loaded at boot). Newtron writes to Redis; `config save -y` persists to disk. This runs automatically after every `-x` execution unless `--no-save` is used.

### 5.6 Crash Recovery

Crash recovery is structural: NEWTRON_INTENT records ARE the persistent state, the drift guard detects inconsistency, Reconcile fixes it.

A crash mid-apply leaves the device in a partial state. The NEWTRON_INTENT record for the operation may or may not have reached Redis — it is prepended to the ChangeSet, so it is written first.

| Crash scenario | What's on device | Recovery |
|---|---|---|
| Intent written, entries partially applied | NEWTRON_INTENT exists, some CONFIG_DB entries | Drift guard detects gap → Reconcile pushes full projection |
| Intent not written (crash before any Redis write) | Nothing changed | No drift, no action needed |
| All entries applied, intent exists | Full CONFIG_DB entries + intent | No drift — intent was fully applied |

In every case: `InitFromDeviceIntent` replays whatever NEWTRON_INTENT records exist → projection reflects the declared intents → drift guard compares against actual CONFIG_DB → Reconcile pushes the full projection if needed. To undo a partially-applied intent, call the normal reverse operation (`DeleteVLAN`, `RemoveService`, etc.).

**Prepend ordering is load-bearing.** `writeIntent` uses `cs.Prepend()` to place the NEWTRON_INTENT record before config entries. When `cs.Apply` writes to Redis, the intent record reaches the device first. If a crash occurs mid-apply, the intent survives and replay produces the full projection — drift shows missing records, Reconcile completes delivery.

## 6. Verification

**If a tool changes the state of an entity, that same tool must be able to verify the change had the intended effect.** Verification is the completion of provisioning, not a separate concern. For cross-device observations, newtron returns structured data, not verdicts — cross-device checks belong in the orchestrator (newtrun).

### 6.1 Four Tiers

| Tier | What | Owner | Method |
|------|------|-------|--------|
| **CONFIG_DB** | Redis entries match ChangeSet | newtron | `cs.Verify(n)` |
| **APP_DB/ASIC_DB** | Routes installed by FRR/ASIC | newtron | `GetRoute()`, `GetRouteASIC()` |
| **Operational state** | BGP sessions, interface health | newtron | `VerifyDeviceHealth()` |
| **Cross-device** | Route propagation, ping | newtrun | Composes newtron primitives |

### 6.2 ChangeSet Verification

Every mutating operation produces a ChangeSet. `cs.Verify(n)` re-reads CONFIG_DB and diffs against the ChangeSet — the only assertion newtron makes: checking its own writes. `Node.Execute()` runs Lock → snapshot → fn → Commit (apply + verify) → Unlock. On dry-run, `RestoreIntentDB` restores the intent DB; the dirty projection is cleaned by the next `RebuildProjection`.

### 6.3 Routing State Observation

- **`GetRoute(vrf, prefix)`** — reads APP_DB (DB 0). Returns `RouteEntry` with prefix, protocol, next-hops. Nil if not present.
- **`GetRouteASIC(vrf, prefix)`** — reads ASIC_DB (DB 1) via SAI object chain resolution. Confirms ASIC programming.

APP_DB shows what FRR computed. ASIC_DB shows what the hardware installed. The gap is orchagent processing. These are building blocks — newtron provides the read; newtrun knows what to expect.

## 7. End-to-End Walkthrough

A concrete trace of `newtron leaf1 vlan create 100 --name servers -x` from keystroke to Redis:

```
CLI (cmd/newtron)
  │  Sends POST /newtron/v1/networks/default/node/leaf1/vlan
  │  Body: {"id": 100, "name": "servers"}
  │  Query: ?execute=true
  │
  ▼
newtron-server (pkg/newtron/api/)
  │  handleVLANCreate():
  │    server.getNetwork("default")        → networkEntity
  │    networkEntity.getNodeActor("leaf1")  → NodeActor
  │
  ▼
NodeActor.execute(ctx, connectAndExecute)
  │
  ├─ ensureActuatedIntent(ctx)
  │    first request: InitFromDeviceIntent (read NEWTRON_INTENT, replay)
  │    cached node: no-op (already actuated)
  │
  ├─ RebuildProjection(ctx)
  │    re-reads NEWTRON_INTENT from Redis, rebuilds projection from scratch
  │
  └─ connectAndExecute(ctx) → node.Execute(ctx, opts, fn)
       │
       ├─ Lock(ctx)      — Redis SETNX + drift guard (projection vs actual)
       ├─ fn(ctx)        — CreateVLAN(100, "servers"):
       │                     writeIntent → intent DB: "vlan|100" added
       │                     op() → render → projection updated
       │                     returns ChangeSet
       ├─ Commit(ctx)    — cs.Apply(n): Redis HSET (NEWTRON_INTENT first, then VLAN)
       │                     cs.Verify(n): re-read CONFIG_DB, diff against ChangeSet
       ├─ Save(ctx)     — SSH `config save -y`
       ├─ Unlock
       └─ Reset idle timer

Response → CLI
  WriteResult{ChangeCount: 3, Applied: true, Verified: true}
  CLI prints: "Changes applied successfully."
```

A spec-only operation like `newtron service list` is simpler: CLI sends GET to server, the handler resolves the networkEntity for the network ID, and calls `ne.net.ListServices()` directly — no NodeActor, no API-layer lock. The engine layer's `keyNetworkSpec.RLock` is acquired inside `ListServices`, the underlying spec map is iterated under that lock, and the result is returned.

## 8. Concurrency model

Spec atomicity and runtime concurrency are owned by the layer that owns the data they protect (`DESIGN_PRINCIPLES_NEWTRON §27` — Single Owner). The API layer holds no spec lock of its own.

### 8.1 Where locks live

| Layer | Lock | Protects |
|-------|------|----------|
| `pkg/newtron/spec` | `spec.Loader.mu sync.RWMutex` | Profile cache + the `network` / `topology` pointer fields (reassigned by `SaveNetwork` / `SaveTopology`) |
| `pkg/newtron/network` | `*network.Network`'s `lockManager` (per-key dynamic `sync.RWMutex`) | The maps in `NetworkSpecFile`, the maps in `TopologySpecFile`, the runtime `n.devices` cache |
| `pkg/newtron/api` | None (for spec data); `networkEntity.nodeMu sync.Mutex` (for the NodeActor cache); per-device `NodeActor` goroutine | NodeActor registry + per-device operation serialization |

`networkEntity` carries no spec lock. Every public Network method (Create/Update/Delete/Add/Remove/List/Show/Get/Snapshot) is engine-atomic on its own: it takes its key's lock, performs the composition under that lock, releases. The API layer just calls it.

### 8.2 The three engine keys

The lockManager (`pkg/newtron/network/locks.go`) hands out one `*sync.RWMutex` per key, lazily on first request. There are three:

| Key | What it protects |
|-----|------------------|
| `keyNetworkSpec` | Everything in `network.json` — the 7 OverridableSpecs maps (Services, Filters, IPVPNs, MACVPNs, QoSPolicies, RoutePolicies, PrefixLists) plus Zones — and the `persistSpec` call that writes the file. |
| `keyTopology` | `topology.json` — Devices, Links — and the `applyTopology` call that writes the file. |
| `keyNodes` | The runtime `n.devices` cache populated by `GetNode`. Not persistent; just the API server's in-memory map of currently-built `*Node` instances. |

Profile files are not covered by a Network-layer key. `spec.Loader` has its own RWMutex (added in PR #100) and serializes per-profile correctly on its own — the Loader's atomic `CreateProfile` does the check + file write + cache update under a single Lock.

### 8.3 Atomicity

Every public Create/Update/Delete/Add/Remove method on `*newtron.Network` is internally atomic. The atomic method holds the appropriate key's `Lock` from existence check through in-memory mutation through disk persist:

```go
// pkg/newtron/network/network.go
func (n *Network) CreateService(name string, def *spec.ServiceSpec) error {
    mu := n.locks.lock(keyNetworkSpec)
    mu.Lock()
    defer mu.Unlock()

    name = util.NormalizeName(name)
    if _, exists := n.spec.Services[name]; exists {
        return fmt.Errorf("service '%s' already exists", name)
    }
    spec.NormalizeServiceRefs(def)
    if n.spec.Services == nil {
        n.spec.Services = make(map[string]*spec.ServiceSpec)
    }
    n.spec.Services[name] = def
    return n.persistSpec()
}
```

Two concurrent `CreateService("X")` calls cannot both succeed. The pre-#101 layout had the public layer compose internal `GetService` + `SaveService` as two separate critical sections; the gap between them was a TOCTOU race that the API-layer `networkEntity.mu` masked. With the engine layer atomic, the API-layer lock is no longer needed; PR #101 (Phase C) removed it.

### 8.4 Concurrent reads

Spec reads (List/Show/Get) take RLock on the same key and run concurrently with each other. For methods that need to iterate a whole spec map (`ListIPVPNs`, `ListMACVPNs`, etc.), the engine layer exposes `*Snapshot()` methods that build a shallow copy under RLock — callers iterate the copy with no race against any future writer.

### 8.5 Cross-key operations and lock ordering

A few engine methods touch more than one key. Examples in the current code:

- `DeleteProfile` reads `topology.Devices` (under `keyTopology.RLock`) to decide whether to cascade-delete the matching topology device before calling `Loader.DeleteProfile`.
- `DeleteTopologyDevice` and `UpdateTopologyDevice` both mutate `topology.Devices` (under `keyTopology.Lock`) and clear the matching entry from `n.devices` (under `keyNodes.Lock`).
- `GetNode` reads `n.spec.Zones` via `resolveProfile` (`keyNetworkSpec.RLock`) and writes the lazy-loaded `*Node` to `n.devices` (`keyNodes.Lock`).

The lock-ordering rule for any multi-key caller is: **acquire locks in alphabetical order of key string.** With the current three keys, alphabetical order is `keyNetworkSpec` < `keyNodes` < `keyTopology`. Every multi-key call site in `pkg/newtron/network/network.go` follows this rule; new ones must too.

### 8.6 Cycle deadlocks

The API layer carries no spec lock, so a handler cannot hold a lock across an outbound HTTP call. The cycle that drove issue #97 (newtron handler → newtlab loopback → newtron `/topology` handler → can't proceed because outer handler holds a Lock) is structurally impossible. `TestAPI_LoopbackHTTPDoesNotDeadlock` in `pkg/newtron/api/loopback_no_deadlock_test.go` pins this contract: a fake `PortResolver` makes a loopback HTTP call to `/topology` during `/hosts/host1` resolution; the chain must complete inside a 5-second deadline. If anyone reintroduces an API-layer lock-across-loopback, this test fails.

### 8.7 Per-device serialization

`NodeActor` is the one place a goroutine is genuinely needed. A device operation is a multi-step sequence — `RebuildProjection`, `Lock`, `Apply`, `Verify`, `Save`, `Unlock`, idle-timer reset — touching CONFIG_DB on a remote system over SSH. Two concurrent operations on the same device would race the device's CONFIG_DB; the NodeActor goroutine + select loop serializes them. This is a property of the device, not of newtron's internal state, so there is no way to push it lower in the stack.

The asymmetry between `networkEntity` (not an actor) and `NodeActor` (actually an actor) is deliberate — they protect different things at different layers.

## 9. Security

Redis on SONiC has no authentication and listens only on localhost. SSH is the transport security layer — all Redis access goes through an SSH tunnel with password credentials from the device profile. In integration tests, a standalone Redis container is used without SSH.

**Authorization.** The auth subsystem in `pkg/newtron/auth/` embodies an entitlement pattern: permissions declared in `network.json`, group-based grants with literal-user fallback, service-level overrides of global grants, superuser bypass. Read/view operations are not gated.

**Current enforcement status:** the entitlement pattern is wired live as of L3. L0 secret store is shipped (`${secret:KEY}` references in `profiles/*.json` / `platforms.json` resolved at network load when `--secret-store=PATH` is set; see [`secret-store.md`](secret-store.md)). L1 audit log infrastructure is shipped (mutation events recorded via `--audit-log=PATH` with caller identity from Unix peer creds, self-attested header, or none). L2a inter-service mTLS is shipped — `--tls-cert` / `--tls-key` / `--tls-ca` flags; see [`mtls-howto.md`](mtls-howto.md). L2b user-to-service PAM is shipped — `--auth-pam-service=NAME` enforces HTTP Basic + `pam_authenticate` on TCP requests; audit log records `verification_source: "pam"`; see [`pam-howto.md`](pam-howto.md). L3 authorization enforcement is shipped — `--enforce-authorization` engages `Network.EnableAuthorization` at every `RegisterNetwork`/`ReloadNetwork`, the 26 wired mutation paths in `spec_ops.go` and `profile_ops.go` become live gates, denials surface as HTTP 403 with the typed `AuthorizationError` payload, and per-decision audit events (`authcheck:<permission>`) join the L1 request events when both `--audit-log` and `--enforce-authorization` are set; see [`authorization-howto.md`](authorization-howto.md). Without the flag, pre-L3 behavior is preserved (checks return nil) per auth-design.md §2.4. L4–L6 remain proposed — Node-level write operations are not yet gated.

**Layered path to production-grade.** The design destination is the existing entitlement pattern, extended to meet audit requirements. The path is seven layers; each layer ships as one PR and closes a defined security gap independently. [`auth-design.md`](auth-design.md) is the L0 deliverable: it articulates the threat model (in-scope and out-of-scope, split by **inter-service** and **user-to-service** surfaces), the goal-state criteria a security review must be able to verify, and L1–L6 with their dependencies. L1 wires up the audit log so accountability is answerable today (with verified identity for the Unix socket path; self-attested-but-labeled for TCP until L2). L2 splits into L2a (inter-service mTLS) and L2b (user-to-service PAM); they're independent and shippable in either order. L3 turns the entitlement checks live; L4 closes coverage to Node operations; L5 adds fine-grained per-device grants; L6 closes operational gaps (revocation, secret hygiene, log integrity). Identity providers beyond PAM (OIDC, SAML, bearer tokens) are explicitly out of scope — operators integrate via PAM modules instead (`pam_ldap`, `pam_sss`, `pam_krb5`).

## 10. Testing

| Tier | How | Purpose |
|------|-----|---------|
| Unit | `go test ./...` | Pure logic: IP derivation, spec parsing, ACL expansion |
| E2E | newtrun framework | Full stack: newtlab VMs, SSH tunnel, real SONiC |

E2E testing uses the newtrun framework (see [newtrun HLD](../newtrun/hld.md) and [newtrun HOWTO](../newtrun/howto.md)).

## 11. Cross-References

| Topic | Document |
|-------|----------|
| Full pipeline specification with end-to-end traces | [Unified Pipeline Architecture](unified-pipeline-architecture.md) |
| Architectural principles and design rationale | [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md) |
| Type definitions, method signatures, HTTP API routes, CLI commands, CONFIG_DB tables | [LLD](lld.md) |
| Device-layer internals (SSH tunneling, Redis clients, write paths) | [Device LLD](device-lld.md) |
| Operational procedures (CLI usage, service apply, provisioning) | [HOWTO](howto.md) |
| Intent DAG hierarchy and intent record format | [Intent DAG Architecture](intent-dag-architecture.md) |
| SONiC pitfalls and workarounds | [RCA Index](../rca/) |

## Appendix A: Glossary

### Core

| Term | Definition |
|------|------------|
| **Spec** | Declarative intent describing what you want. JSON files, version controlled. Never contains concrete device values. |
| **Config** | Imperative device state. Redis CONFIG_DB entries, generated at runtime from specs + device context. |
| **Service** | Reusable template bundling VPN, filters, QoS. Applied to interfaces for consistent configuration. |
| **ChangeSet** | Collection of pending CONFIG_DB changes. Verification contract — `cs.Verify(n)` diffs against live CONFIG_DB. |

### Intent Pipeline

| Term | Definition |
|------|------------|
| **Intent DB** | The collection of NEWTRON_INTENT records in `configDB.NewtronIntent`. Primary state — all operational decisions read here. |
| **Projection** | The typed CONFIG_DB tables derived from intent replay. Exists for device delivery and drift detection — no operational decision reads the projection. |
| **Render** | Update the projection from a ChangeSet: validate entries against the schema, then apply to typed configDB structs. |
| **Replay** | Execute a config function for an intent, producing entries that get rendered into the projection. |
| **Drift** | Difference between projection (expected) and device (actual). Detected by comparing `ExportRaw()` against transient Redis read. |
| **Drift guard** | In actuated mode, Lock computes drift before allowing writes. Non-empty drift → Lock refuses. Resolution: `Reconcile()` first. |
| **Reconcile** | Deliver the projection to the device. Full mode: config reload → `ExportEntries()` → `ReplaceAll()`. Delta mode: `DiffConfigDB()` → `ApplyDrift()` (patches only drifted entries). Default: full for topology nodes, delta for actuated nodes. |
| **RebuildProjection** | Re-read intents (from device when connected, from memory when offline), create fresh configDB, replay all intents. Called in `execute()` before every operation. |
| **Execute** | Public write entry point: Lock → snapshot → fn → commit-or-restore → Unlock. Supports dry-run via intent snapshot/restore. |
| **Transport** | SSH + Redis connection layered on top of expected state. `ConnectTransport()` adds the wire without disturbing intent DB or projection. |

### Architecture

| Term | Definition |
|------|------------|
| **newtron-server** | Central HTTP server. Owns `networkEntity` instances; device connections owned by `NodeActor` instances within. |
| **networkEntity** | Per-network registration record that owns the engine `*newtron.Network` and a NodeActor cache. Not an actor — no goroutine, no message channel, no spec lock. Spec atomicity is the engine layer's responsibility via per-key locks. One per network. |
| **NodeActor** | Child actor that serializes device operations and caches `*newtron.Node` (SSH connection) with idle timeout. One per device. |
| **Abstract Node** | Node whose intent DB and projection are populated from intent replay, not from a device. Same code path in all three states — different intent source. |
| **Abstract Topology** | `topology.json` — network-level intent declaring what devices, ports, and steps should exist. Container of Abstract Nodes. |

### Entities

| Term | Definition |
|------|------------|
| **Network** | Top-level object. Owns all specs, provides access to devices. |
| **Node** | Device handle. Holds intent DB, projection, device profile, and optional transport connection. |
| **Interface** | Interface handle. Holds parent Node reference and interface name. Point of service delivery. |
| **Platform** | Hardware type definition (HWSKU, port count, speeds). |

### VPN

| Term | Definition |
|------|------------|
| **IPVPN** | IP-VPN definition for L3 routing. Contains L3VNI and route targets. |
| **MACVPN** | MAC-VPN definition for L2 bridging. Contains VNI, VLAN ID, anycast IP/MAC, route targets. |
| **VRF** | Virtual Routing and Forwarding instance. First-class CLI noun: owns interfaces, BGP neighbors, static routes, IP-VPN bindings. |

### Redis Databases

| DB | Name | Purpose |
|----|------|---------|
| 0 | APP_DB | Routes installed by FRR/fpmsyncd (read via `GetRoute`) |
| 1 | ASIC_DB | SAI objects programmed by orchagent (read via `GetRouteASIC`) |
| 4 | CONFIG_DB | Device configuration (read/write) |
| 6 | STATE_DB | Operational state (read-only via health checks) |

### Operations

| Term | Definition |
|------|------------|
| **Dry-Run** | Preview mode (default). Shows what would change without applying. Intent snapshot restored after. |
| **Execute (`-x`)** | Apply mode. Writes changes to CONFIG_DB, verifies, saves to disk. |
| **Save (config)** | Persist runtime CONFIG_DB to `/etc/sonic/config_db.json`. Runs automatically after `-x`. |
| **Save (intent)** | Persist device's current intent DB to `topology.json` via `Tree()` + `SaveDeviceIntents()`. |
| **Device Lock** | Distributed lock in STATE_DB with TTL. Prevents concurrent modifications. In actuated mode, Lock also performs drift guard. |
| **frrcfgd** | SONiC's FRR management framework daemon. Translates CONFIG_DB BGP tables to FRR commands. Required by newtron (unified config mode). |
