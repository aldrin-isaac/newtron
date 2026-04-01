# Intent Reference

This document is the authoritative reference for every NEWTRON_INTENT record
type, its parameters, DAG relationships, lifecycle, and reconstruction
behavior. It covers the *what* and *how* of each intent — not the *why* behind
the DAG architecture (see `intent-dag-architecture.md` for design rationale,
`unified-pipeline-architecture.md` for the pipeline context).

Every writeIntent, deleteIntent, and ReplayStep call in the codebase must be
consistent with this document. When they diverge, the code is authoritative
and this document is stale.

## 1. Intent Record Structure

Every NEWTRON_INTENT record is a Redis hash stored in CONFIG_DB with the key
`NEWTRON_INTENT|{resource}`. The hash contains identity fields, domain
parameters, and DAG metadata:

```
NEWTRON_INTENT|interface|Ethernet0 → {
    operation:    "apply-service"
    state:        "actuated"
    name:         "TRANSIT"
    service_name: "TRANSIT"
    service_type: "routed"
    vrf_name:     "Vrf_TRANSIT"
    ip_address:   "10.10.1.1/31"
    _parents:     "vrf|Vrf_TRANSIT,service|TRANSIT"
    _children:    "interface|Ethernet0|qos"
}
```

### 1.1 Identity Fields

Identity fields are structural metadata stripped from `Params` by
`sonic.NewIntent` into dedicated struct fields. They are never part of the
domain parameter map. `ToFields` serializes them back for Redis delivery.

| Wire field | Struct field | Description |
|------------|-------------|-------------|
| `operation` | `Operation string` | The Op constant that created this intent (e.g., `"apply-service"`) |
| `state` | `State IntentState` | Lifecycle state (see §1.2) |
| `name` | `Name string` | Spec reference extracted from the resource key (e.g., `"TRANSIT"` from `"service\|TRANSIT"`) |
| `_parents` | `Parents []string` | CSV of parent resource keys |
| `_children` | `Children []string` | CSV of child resource keys |
| `holder` | `Holder string` | Lock holder identity |
| `created` | `Created time.Time` | RFC3339 creation timestamp |
| `applied_at` | `AppliedAt *time.Time` | RFC3339 last-applied timestamp |
| `applied_by` | `AppliedBy string` | Identity of the applier |
| `phase` | `Phase string` | Operation phase |

Everything else in the hash becomes `Params map[string]string` — the domain
parameters documented per-intent in §7.

### 1.2 Intent States

| State | Wire value | Meaning |
|-------|-----------|---------|
| `IntentActuated` | `"actuated"` | Active and delivered. The normal state for all operational intents. |
| `IntentInFlight` | `"in-flight"` | Operation in progress (set during write, before delivery). |
| `IntentUnrealized` | `"unrealized"` | Declared but not yet applied. |

Reconstruction (§8) filters to actuated intents only — `in-flight` and
`unrealized` intents are excluded from the replay sequence.

### 1.3 DAG Metadata

| Field | Wire format | Lifecycle |
|-------|-------------|-----------|
| `_parents` | CSV of resource keys | Set at creation, immutable until delete+recreate |
| `_children` | CSV of resource keys | Empty at creation, grows/shrinks as children register/deregister |

The underscore prefix distinguishes DAG metadata from domain parameters.
`ToFields` always includes `_parents` and `_children` in the wire hash, even
when empty — Redis `HSET` merges fields, so omitting an empty `_children`
would leave a stale value from the previous write.

## 2. DAG Invariants

The intent DAG enforces eight invariants (I1–I8), defined in
`intent-dag-architecture.md` §3. Four are relevant to this reference — two
enforced at runtime (operations fail immediately), two checked by validation:

| ID | Rule | Enforced by |
|----|------|-------------|
| **I4** | Parent existence on creation: all declared parents must exist before a child is created | `writeIntent` — new record path |
| **I5** | Child absence on deletion: a record with children cannot be deleted | `deleteIntent` — refuses if `len(Children) > 0` |
| **I2** | Bidirectional consistency: if A lists B as parent, B must list A as child, and vice versa | `ValidateIntentDAG` |
| **I3** | Referential integrity: all parent/child references point to existing records | `ValidateIntentDAG` |

The remaining four (I1 acyclicity, I6 child-only relationship writes, I7
parent immutability of children, I8 DAG metadata exclusivity) are structural
guarantees maintained by `writeIntent`/`deleteIntent` — see the architecture
doc for their formal definitions.

**Parents are immutable.** Changing an intent's parents requires delete +
recreate. `writeIntent` returns an error if the resource exists with different
parents. When parents match, it performs an idempotent update: replaces params,
preserves children.

## 3. Intent Operations

### 3.1 writeIntent

`writeIntent(cs, op, resource, params, parents)` is the sole writer for
NEWTRON_INTENT records. Two paths:

**New record** (resource does not exist):
1. Validates I4: every declared parent must exist in the intent DB.
2. For each parent: appends `resource` to `parent._children` (deduplicating),
   writes updated parent via `cs.Add`, and calls `renderIntent` to update the
   in-memory projection immediately.
3. Creates the new intent with `State: IntentActuated`, empty `Children`,
   supplied `params` and `parents`.
4. Writes via `cs.Prepend` (not `cs.Add`) — the intent record lands
   *before* the operation's CONFIG_DB writes in the ChangeSet.

**Idempotent update** (resource exists, same parents):
1. If existing parents differ from requested parents → error. Parents are
   immutable without delete+recreate.
2. Constructs a new intent with updated `params` but **preserves existing
   `Children`** — children are managed by their own writeIntent calls.
3. Writes via `cs.Prepend` + `renderIntent`.

**Ordering: Prepend vs Add.** The new intent itself uses `cs.Prepend`,
placing it at the head of the ChangeSet. Parent registration updates use
`cs.Add`. This ensures that during ChangeSet delivery, the intent record
write is ordered before the domain CONFIG_DB writes that follow.

**`renderIntent`** calls `n.configDB.ApplyEntries([]sonic.Entry{entry})` to
update the in-memory projection immediately within the same operation. This
is critical: subsequent `writeIntent` calls in the same transaction can see
freshly written parent records (required for I4 enforcement in multi-intent
operations like `SetupDevice` + `CreateVRF` + `BindIPVPN`).

### 3.2 deleteIntent

`deleteIntent(cs, resource)` removes an intent record.

1. If the resource does not exist, silently returns (idempotent).
2. **I5 check:** if `len(Children) > 0`, returns an error. Callers must
   remove children explicitly before deleting a parent.
3. For each parent: removes `resource` from `parent._children`, writes
   updated parent via `cs.Add` + `renderIntent`. Silently skips if a parent
   no longer exists (stale reference).
4. Deletes via `cs.Delete("NEWTRON_INTENT", resource)` and removes from
   the in-memory projection directly.

**No cascade deletes.** The I5 check is the sole enforcement mechanism.
There is no automatic cascade — every child must be individually deleted
by the caller in the correct order (children before parents).

### 3.3 Query Methods

| Method | Returns | Use case |
|--------|---------|----------|
| `GetIntent(resource)` | Single `*sonic.Intent` or nil | Existence checks, parameter reads |
| `IntentsByPrefix(prefix)` | All intents whose resource key starts with `prefix` | Enumerate VLANs (`"vlan\|"`), sub-resources (`"interface\|Ethernet0\|"`) |
| `IntentsByParam(key, value)` | All intents where `Params[key] == value` | Find all bindings for a VRF, IPVPN, or VLAN |
| `IntentsByOp(op)` | All intents with a given operation | Find all `configure-irb` intents |
| `Intents()` | All intents (no filter) | Full DAG traversal |
| `ServiceIntents()` | Intents where `operation == "apply-service"` and `state == "actuated"` | List active service bindings |
| `Tree()` | Ordered `[]TopologyStep` via `IntentsToSteps` | Intent export for topology persistence |

### 3.4 ValidateIntentDAG

`ValidateIntentDAG(configDB)` is a standalone validation function (not called
during normal operations). It checks three classes of violations:

1. **Dangling references (I3):** parent or child resource keys that point
   to non-existent records.
2. **Bidirectional inconsistency (I2):** A lists B as parent but B does not
   list A as child, or vice versa.
3. **Orphans:** BFS from `"device"` root — any record not reachable is an
   orphan.

Used by tests and CLI diagnostics, not by runtime operations. I4 and I5 are
the runtime guards.

## 4. Intent Persistence

Intent records are persisted differently depending on the `Node`'s mode:

### 4.1 Actuated Mode (Device-Connected)

Intents live in CONFIG_DB as `NEWTRON_INTENT|{resource}` Redis hashes. They
are read from the device on connect via `InitFromDeviceIntent`, which:

1. Connects transport (SSH + Redis).
2. Creates a fresh empty projection.
3. Reads `NEWTRON_INTENT` from CONFIG_DB.
4. Calls `IntentsToSteps` → `ReplayStep` for each step to reconstruct
   the projection.
5. Sets `actuatedIntent = true`.

The device's NEWTRON_INTENT records are the authoritative state. The
projection is derived, never persisted.

### 4.2 Abstract Mode (Topology Provisioning)

Intents live in memory during provisioning. After provisioning completes,
`Tree()` exports the intent DB as ordered `TopologyStep` entries, and
`SaveDeviceIntents` writes them to `topology.json` (atomic temp+rename).
On the next load, these steps are replayed to reconstruct the `Node`.

The `unsavedIntents` flag tracks whether mutations have occurred since the
last save. `HasUnsavedIntents()` returns it; `ClearUnsavedIntents()` resets
it after a successful save.

### 4.3 RebuildProjection

`RebuildProjection(ctx)` rebuilds the entire projection from scratch. It is
called by `execute()` **at the start of every operation** (reads and writes
alike) to ensure every operation sees fresh, authoritative state.

Steps:
1. In actuated mode: re-reads `NEWTRON_INTENT` from the device.
2. In abstract mode: uses the in-memory intent DB as-is.
3. Saves ports, creates a fresh `ConfigDB`, re-registers ports.
4. Replays all intents via `IntentsToSteps` + `ReplayStep`.

This is the mechanism that makes the intent DB the single source of truth:
the projection is never stale longer than one operation boundary.

### 4.4 Dry-Run (Snapshot/Restore)

`Execute()` supports dry-run via intent snapshot:

1. `SnapshotIntentDB()` deep-copies the intent DB before the operation.
2. The operation runs normally, writing intents and updating the projection.
3. `RestoreIntentDB(snapshot)` replaces the intent DB with the snapshot.
4. The projection is left dirty — `RebuildProjection` at the next
   `execute()` call cleans it up.

This allows operations to compute their full ChangeSet (including intent
writes) without persisting any state changes.

## 5. Design Principles

Three cross-cutting principles shape how intents are used.

### 5.1 Self-Sufficient Teardown

Intent records must contain every value needed for their reverse operation.
The spec may have changed between apply and remove; the intent captures what
was actually applied.

Examples:
- `ipvpn|VRFNAME` stores `l3vni` and `l3vni_vlan` so `UnbindIPVPN` can
  tear down transit VLAN infrastructure without looking up the IP-VPN spec.
- `interface|INTF` (apply-service) stores `vrf_name`, `vrf_type`, `l3vni`,
  `l3vni_vlan`, `ingress_acl`, `egress_acl`, `qos_policy`, etc. so
  `RemoveService` can tear down everything without re-resolving specs.
- `service|NAME` stores `route_policy_keys` so the last-user removal can
  delete all route policy entries without scanning the projection.

When adding a new forward operation that creates infrastructure, ask: "can
the reverse operation find everything it needs in the intent record alone?"

### 5.2 Content-Hashed Naming

Route policies (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) include an 8-character
SHA256 hash of their CONFIG_DB fields in the key name. This enables:

- **Idempotency:** spec unchanged → hash unchanged → no CONFIG_DB writes.
- **Blue-green migration:** spec changed → new hash → new objects created
  alongside old ones. Interfaces migrate one by one. Old objects deleted
  when the last consumer migrates.
- **Merkle cascade:** PREFIX_SET hashes are computed first, then ROUTE_MAP
  entries reference real PREFIX_SET names (including hashes). A content
  change at any level cascades through the hash chain automatically.

The `route_policy_keys` param on `service|NAME` captures the full set of
content-hashed keys. `RefreshService` uses `diffRoutePolicyKeyCSV` to find
stale keys (old hash) that need cleanup after a spec change.

### 5.3 DAG-Based Reference Counting

Shared objects (ACLs, services) use DAG children for reference counting
instead of projection scanning:

- **ACL lifecycle:** `acl|NAME` has `interface|INTF|acl|DIR` bindings as
  children. `removeSharedACL` checks `acl|NAME._children` — if non-empty,
  other interfaces still reference the ACL. Only when childless is the ACL
  deleted.
- **Service lifecycle:** `service|NAME` has `interface|INTF` bindings as
  children. `RemoveService` checks `service|NAME._children` — if other
  interfaces remain, shared objects (route policies, peer group) are kept.

This replaced projection scanning (`IntentsByPrefix`, `IntentsByParam` loops)
with O(1) child-list checks.

## 6. DAG Structure

The diagram below shows primary parent→child edges (solid lines). Several
intent types have conditional parents that vary by context — these are
documented in the per-intent tables in §7.

Source: `docs/diagrams/intent-dag-tree.dot`

```
                           ┌────────────────────────────────────────────────────────────────────────┐
                           │                                                                        │
                           │                                              ┌──────────────────────┐  │  ┌────────────────────┐     ┌────────────────────────┐
                           │                                              │ route|default|PREFIX │  │  │    service|NAME    │     │   interface|INTF|qos   │
                           │                                              └──────────────────────┘  │  └────────────────────┘     └────────────────────────┘
                           │                                                ▲                       │    ▲                          ▲
                           │                                                │                       │    │                          │
                           ▼                                                │                       │    │                          │
┌──────────────────┐     ┌───────────────┐┌─────────────────────────┐     ┌─────────────────────────────────────────────────┐     ┌───────────────────────────────────────────────────┐     ┌─────────────────────────┐
│ route|VRF|PREFIX │ ◀── │   vrf|NAME    ││    portchannel|NAME     │ ◀── │                                                 │ ──▶ │                  interface|INTF                   │ ──▶ │ interface|INTF|PROPERTY │
└──────────────────┘     └───────────────┘└─────────────────────────┘     │                                                 │     └───────────────────────────────────────────────────┘     └─────────────────────────┘
                           │                │                             │                                                 │       │                         │
                           │                │                             │                     device                      │       │                         │
                           ▼                ▼                             │                                                 │       ▼                         ▼
                         ┌───────────────┐┌─────────────────────────┐     │                                                 │     ┌────────────────────────┐┌─────────────────────────┐
                         │ ipvpn|VRFNAME ││ portchannel|NAME|MEMBER │     │                                                 │     │ interface|INTF|acl|DIR ││ interface|INTF|bgp-peer │
                         └───────────────┘└─────────────────────────┘     └─────────────────────────────────────────────────┘     └────────────────────────┘└─────────────────────────┘
                                                                            │                       │    │
                                                                            │                       │    │
                                                                            ▼                       │    ▼
                                                                          ┌──────────────────────┐  │  ┌────────────────────┐
                                                                          │       acl|NAME       │  │  │   evpn-peer|ADDR   │
                                                                          └──────────────────────┘  │  └────────────────────┘
                                                                            │                       │
                                                                            │                       │
                                                                            ▼                       │
                                                                          ┌──────────────────────┐  │  ┌────────────────────┐     ┌────────────────────────┐
                                                                          │    acl|NAME|RULE     │  └▶ │      vlan|ID       │ ──▶ │     macvpn|VLANID      │
                                                                          └──────────────────────┘     └────────────────────┘     └────────────────────────┘
                                                                                                         │
                                                                                                         │
                                                                                                         ▼
                                                                                                       ┌────────────────────┐
                                                                                                       │ interface|Vlan{ID} │
                                                                                                       └────────────────────┘
```

## 7. Intent Catalog

Each intent type is documented with its resource key pattern, operation
constant, parameters, parent rules, children, code locations, and
reconstruction behavior. Intent types are grouped by their position in the
DAG: root, infrastructure, overlay, service, interface, and sub-resource.
§8 describes how these intents are replayed during reconstruction; §9
traces concrete operations through multiple intent types end-to-end.
§10 maps intent types to source files; §11 provides a condensed summary.

### 7.1 Root

#### `device`

The singleton root of the DAG. All other intents descend from it.

| Field | Value |
|-------|-------|
| **Resource key** | `device` |
| **Operation** | `OpSetupDevice` (`"setup-device"`) |
| **Created by** | `SetupDevice()` in `baseline_ops.go` |
| **Deleted by** | Never (reprovision overwrites) |
| **Reconstruct** | `replayNodeStep` → `n.SetupDevice(ctx, opts)` |
| **skipInReconstruct** | No |

**Parents:** none (root).

**Children:** all top-level intents — `vlan|*`, `vrf|*`, `acl|*`,
`portchannel|*`, `evpn-peer|*`, `service|*`, `route|default|*`, and
`interface|*` when the interface has no VLAN/VRF parent.

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `hostname` | `opts.Fields` | Device hostname |
| `bgp_asn` | `opts.Fields` | BGP autonomous system number |
| `type` | `opts.Fields` | Device type |
| `hwsku` | `opts.Fields` | Hardware SKU |
| `source_ip` | `opts.SourceIP` | VTEP source IP (loopback address) |
| `rr_cluster_id` | `opts.RR` | Route reflector cluster ID |
| `rr_local_asn` | `opts.RR` | Route reflector local ASN |
| `rr_router_id` | `opts.RR` | Route reflector router ID |
| `rr_local_addr` | `opts.RR` | Route reflector local address |
| `rr_clients` | `opts.RR` | Comma-separated `ip:asn` pairs |
| `rr_peers` | `opts.RR` | Comma-separated `ip:asn` pairs |

`intentParamsToStepParams` has special handling: `source_ip` is promoted to
top-level, `rr_*` fields are reassembled into a nested `route_reflector`
sub-map, and remaining fields go into a `fields` sub-map.

---

### 7.2 Infrastructure

Infrastructure intents represent device-level resources (VLANs, VRFs,
PortChannels, ACLs) that service bindings and overlay intents depend on.
They are children of `device` and parents of overlay or interface intents.

#### `vlan|{ID}`

A VLAN resource. Created explicitly via `CreateVLAN` or implicitly by
`ApplyService` for bridged/IRB service types.

| Field | Value |
|-------|-------|
| **Resource key** | `"vlan|" + strconv.Itoa(vlanID)` |
| **Operation** | `OpCreateVLAN` (`"create-vlan"`) |
| **Created by** | `CreateVLAN()` in `vlan_ops.go` |
| **Deleted by** | `DeleteVLAN()` in `vlan_ops.go`; `RemoveService()` for service-created VLANs |
| **Reconstruct** | `replayNodeStep` → `n.CreateVLAN(ctx, vlanID, config)` |
| **skipInReconstruct** | No |

**Parents:** `["device"]`.

**Children:** `macvpn|{ID}`, `interface|Vlan{ID}` (IRB), `interface|*`
(bridged service bindings).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vlan_id` | arg | VLAN ID (integer as string) |
| `description` | `VLANConfig.Description` | Optional description |
| `vni` | `VLANConfig.L2VNI` | L2 VNI for EVPN (integer as string) |

---

#### `vrf|{NAME}`

A VRF resource. Created explicitly via `CreateVRF` or implicitly by
`ApplyService` for routed/IRB service types.

| Field | Value |
|-------|-------|
| **Resource key** | `"vrf|" + name` |
| **Operation** | `OpCreateVRF` (`"create-vrf"`) |
| **Created by** | `CreateVRF()` in `vrf_ops.go` |
| **Deleted by** | `DeleteVRF()` in `vrf_ops.go`; `RemoveService()` for per-interface VRFs |
| **Reconstruct** | `replayNodeStep` → `n.CreateVRF(ctx, name, VRFConfig{})` |
| **skipInReconstruct** | No |

**Parents:** `["device"]`.

**Children:** `ipvpn|{NAME}`, `route|{NAME}|*`, `interface|*` (routed
bindings), `interface|Vlan{ID}` (IRB with VRF).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `name` | arg | VRF name (canonical form, e.g., `Vrf_TRANSIT`) |

---

#### `portchannel|{NAME}`

A PortChannel (LAG) resource.

| Field | Value |
|-------|-------|
| **Resource key** | `"portchannel|" + name` |
| **Operation** | `OpCreatePortChannel` (`"create-portchannel"`) |
| **Created by** | `CreatePortChannel()` in `portchannel_ops.go` |
| **Deleted by** | `DeletePortChannel()` in `portchannel_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.CreatePortChannel(ctx, name, config)` |
| **skipInReconstruct** | No |

**Parents:** `["device"]`.

**Children:** `portchannel|{NAME}|{MEMBER}`, `interface|{NAME}` (when the
PortChannel interface is configured or has a service).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `name` | arg | PortChannel name (e.g., `PortChannel100`) |
| `members` | `PortChannelConfig.Members` | Comma-separated member interfaces |
| `mtu` | `PortChannelConfig.MTU` | MTU (integer as string) |
| `min_links` | `PortChannelConfig.MinLinks` | Minimum links (integer as string) |
| `fallback` | `PortChannelConfig.Fallback` | `"true"` if LACP fallback enabled |
| `fast_rate` | `PortChannelConfig.FastRate` | `"true"` if fast LACP rate |

`intentParamsToStepParams` splits `members` from comma-separated string back
into `[]any` for replay.

---

#### `acl|{NAME}`

An ACL table. Created explicitly via `CreateACL` or by `ApplyService` for
filter-backed service ACLs. The ACL intent serves as a shared-object
lifecycle root — its children (rules and bindings) keep it alive.

| Field | Value |
|-------|-------|
| **Resource key** | `"acl|" + name` |
| **Operation** | `OpCreateACL` (`"create-acl"`) |
| **Created by** | `CreateACL()` in `acl_ops.go`; `ApplyService()` in `service_ops.go` |
| **Deleted by** | `DeleteACL()` in `acl_ops.go`; `removeSharedACL()` in `service_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.CreateACL(ctx, name, config)` |
| **skipInReconstruct** | No |

**Parents:** `["device"]`.

**Children:** `acl|{NAME}|{RULE}` (rules), `interface|{INTF}|acl|{DIR}`
(bindings — dual-parent with `interface|{INTF}`).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `name` | arg | ACL table name (e.g., `PROTECT_RE_IN_1ED5F2C7`) |
| `type` | `ACLConfig.Type` | `"L3"` or `"L3V6"` |
| `stage` | `ACLConfig.Stage` | `"ingress"` or `"egress"` |
| `ports` | `ACLConfig.Ports` | Bound ports (optional) |
| `description` | `ACLConfig.Description` | Optional description |
| `rules` | service creation | Comma-separated rule names (service-created ACLs only) |

---

### 7.3 Overlay

Overlay intents bind EVPN constructs (IP-VPN, MAC-VPN) to infrastructure
resources and create EVPN peering sessions. They depend on infrastructure
intents (VRF, VLAN) and produce VXLAN tunnel maps and BGP EVPN configuration.

#### `ipvpn|{VRFNAME}`

IP-VPN binding on a VRF. Creates VXLAN tunnel map and EVPN VNI entries for
L3 overlay.

| Field | Value |
|-------|-------|
| **Resource key** | `"ipvpn|" + vrfName` |
| **Operation** | `OpBindIPVPN` (`"bind-ipvpn"`) |
| **Created by** | `BindIPVPN()` in `vrf_ops.go` |
| **Deleted by** | `UnbindIPVPN()` in `vrf_ops.go`; `RemoveService()` for service-bound IP-VPNs |
| **Reconstruct** | `replayNodeStep` → `n.BindIPVPN(ctx, vrfName, ipvpnName)` |
| **skipInReconstruct** | No |

**Parents:** `["vrf|" + vrfName]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vrf` | arg | VRF name |
| `ipvpn` | arg | IP-VPN spec name |
| `l3vni` | resolved | L3 VNI (integer as string) — stored for self-sufficient teardown |
| `l3vni_vlan` | resolved | Transit VLAN (integer as string) — stored for self-sufficient teardown |
| `route_targets` | resolved | Comma-separated route targets |

---

#### `macvpn|{VLANID}`

MAC-VPN binding on a VLAN. Creates VXLAN tunnel map, EVPN VNI, and ARP
suppression entries for L2 overlay.

| Field | Value |
|-------|-------|
| **Resource key** | `"macvpn|" + strconv.Itoa(vlanID)` |
| **Operation** | `OpBindMACVPN` (`"bind-macvpn"`) |
| **Created by** | `BindMACVPN()` in `evpn_ops.go` |
| **Deleted by** | `UnbindMACVPN()` in `evpn_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.BindMACVPN(ctx, vlanID, macvpnName)` |
| **skipInReconstruct** | No |

**Parents:** `["vlan|" + strconv.Itoa(vlanID)]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vlan_id` | arg | VLAN ID (integer as string) |
| `macvpn` | arg | MAC-VPN spec name |
| `vni` | resolved | L2 VNI (integer as string) |
| `arp_suppression` | resolved | `"true"` if ARP suppression enabled |

---

#### `evpn-peer|{ADDR}`

A BGP EVPN peer (overlay neighbor). These are loopback-to-loopback eBGP
sessions carrying L2VPN EVPN address family.

| Field | Value |
|-------|-------|
| **Resource key** | `"evpn-peer|" + neighborIP` |
| **Operation** | `OpAddBGPEVPNPeer` (`"add-bgp-evpn-peer"`) |
| **Created by** | `AddBGPEVPNPeer()` in `bgp_ops.go` |
| **Deleted by** | `RemoveBGPEVPNPeer()` in `bgp_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.AddBGPEVPNPeer(ctx, ip, asn, desc, evpn)` |
| **skipInReconstruct** | No |

**Parents:** `["device"]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `neighbor_ip` | arg | Peer IP address |
| `asn` | arg | Peer ASN (integer as string) |
| `description` | arg | Optional description |
| `evpn` | arg | `"true"` for L2VPN EVPN AF activation |

---

### 7.4 Service

#### `service|{NAME}`

Shared per-service lifecycle root. Owns CONFIG_DB objects shared across all
interfaces using the same service: route policies (ROUTE_MAP, PREFIX_SET,
COMMUNITY_SET) and BGP peer group (for shared/default VRF). Created by the
first `ApplyService` call for a service with BGP routing; deleted when the
last interface using that service is removed.

| Field | Value |
|-------|-------|
| **Resource key** | `"service|" + serviceName` |
| **Operation** | `OpDeployService` (`"deploy-service"`) |
| **Created by** | `ApplyService()` in `service_ops.go` (first interface for a service) |
| **Deleted by** | `RemoveService()` in `service_ops.go` (last interface for a service) |
| **Reconstruct** | Skipped — recreated as side effect of first `ApplyService` replay |
| **skipInReconstruct** | **Yes** |

**Parents:** `["device"]`.

**Children:** `interface|{INTF}` intents that use this service with BGP
routing. Managed automatically by DAG registration when interface intents
list `service|{NAME}` as a parent.

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `service_name` | arg | Service spec name |
| `route_map_in` | generated | Content-hashed import route map name (omitted when empty) |
| `route_map_out` | generated | Content-hashed export route map name (omitted when empty) |
| `route_policy_keys` | generated | Semicolon-separated `TABLE:KEY` strings (omitted when empty) |

**Lifecycle detail.** The service intent guards shared object creation and
deletion:

- **First interface:** `GetIntent("service|TRANSIT")` returns nil →
  `createPeerGroup=true`, generate route policies, write service intent.
- **Second interface:** service intent exists → `createPeerGroup=false`,
  route policies idempotent (content hash unchanged), update service intent
  params.
- **Remove (not last):** interface intent deleted, service intent stays
  (still has children).
- **Remove (last):** interface intent deleted, service intent has no
  children → delete route policies, delete peer group, delete service intent.
- **Refresh:** capture `route_policy_keys` from service intent before
  remove, capture again after reapply, diff to find stale keys via
  `diffRoutePolicyKeyCSV`.

For `vrf_type: interface`, each interface has its own VRF and peer group.
The peer group is created unconditionally (`createPeerGroup=true` always)
and destroyed with the VRF — the service intent still exists but does not
guard per-VRF peer groups.

---

### 7.5 Interface

Interface intents bind configuration to physical or logical ports. Three
operations produce `interface|{INTF}` records — `interface-init` (anchor),
`configure-interface` (explicit config), and `apply-service` (service
binding) — but only one can exist at a time for a given interface. The
operation field distinguishes them. Parents vary by configuration context
(see per-entry tables below). Reconstructed via `replayInterfaceStep` (§8).

#### `interface|{INTF}` — interface-init

Auto-created anchor for sub-resource operations on interfaces that have no
explicit configuration (no `ConfigureInterface`, no `ApplyService`). Ensures
the `interface|{INTF}` parent exists when `SetProperty`, `BindACL`,
`ApplyQoS`, or `AddBGPPeer` is called on an unconfigured interface.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name` |
| **Operation** | `OpInterfaceInit` (`"interface-init"`) |
| **Created by** | `ensureInterfaceIntent()` in `interface_ops.go` |
| **Deleted by** | Superseded when `ConfigureInterface` or `ApplyService` writes over the same key |
| **Reconstruct** | Skipped — recreated as side effect of sub-resource replay |
| **skipInReconstruct** | **Yes** |

**Parents:** `["device"]`, plus `"portchannel|" + i.name` if PortChannel.

**Children:** sub-resource intents that triggered its creation.

**Parameters:** empty map `{}`.

---

#### `interface|{INTF}` — configure-interface

Explicit interface configuration: bridged (VLAN member), routed (VRF + IP),
or plain IP.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name` |
| **Operation** | `OpConfigureInterface` (`"configure-interface"`) |
| **Created by** | `ConfigureInterface()` in `interface_ops.go` |
| **Deleted by** | `UnconfigureInterface()` in `interface_ops.go` |
| **Reconstruct** | `replayInterfaceStep` → `iface.ConfigureInterface(ctx, config)` |
| **skipInReconstruct** | No |

**Parents** (varies by configuration):

| Context | Parents |
|---------|---------|
| Bridged (VLAN set) | `["vlan\|{ID}"]` |
| Routed (VRF set, no VLAN) | `["vrf\|{NAME}"]` |
| IP-only (no VRF, no VLAN) | `["device"]` |

When the interface is a PortChannel, `"portchannel|{NAME}"` is appended as
an additional parent.

**Children:** `interface|{INTF}|bgp-peer`, `interface|{INTF}|qos`,
`interface|{INTF}|acl|{DIR}`, `interface|{INTF}|{PROPERTY}`.

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vlan_id` | `InterfaceConfig.VLAN` | VLAN ID for bridged mode (integer as string) |
| `tagged` | `InterfaceConfig.Tagged` | `"true"` or `"false"` for VLAN tagging |
| `vrf` | `InterfaceConfig.VRF` | VRF name for routed mode |
| `ip` | `InterfaceConfig.IP` | IP address for routed mode (CIDR) |

---

#### `interface|{INTF}` — apply-service

Service binding on an interface. This is the primary service lifecycle
record — it captures every value needed for teardown so that `RemoveService`
never needs to re-resolve specs.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name` |
| **Operation** | `OpApplyService` (`"apply-service"`) |
| **Created by** | `ApplyService()` in `service_ops.go` |
| **Deleted by** | `RemoveService()` in `service_ops.go` |
| **Reconstruct** | `replayInterfaceStep` → `iface.ApplyService(ctx, serviceName, opts)` |
| **skipInReconstruct** | No |

**Parents** (varies by service type):

| Service type | Parents |
|-------------|---------|
| bridged, evpn-bridged | `["vlan\|{ID}"]` |
| routed | `["vrf\|{NAME}"]` |
| irb, evpn-irb | `["vlan\|{ID}", "vrf\|{NAME}"]` |
| No VLAN, no VRF | `["device"]` |

When the service has BGP routing, `"service|{NAME}"` is appended. When the
interface is a PortChannel, `"portchannel|{NAME}"` is appended.

**Children:** `interface|{INTF}|qos` (when the service has a QoS policy).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `service_name` | arg | Service spec name |
| `service_type` | resolved | Service type (`routed`, `bridged`, `irb`, `evpn-bridged`, `evpn-irb`) |
| `ip_address` | `opts.IPAddress` | Interface IP (CIDR) |
| `vrf_name` | resolved | VRF name |
| `vrf_type` | resolved | `shared` or `interface` |
| `ipvpn` | resolved | IP-VPN spec name |
| `macvpn` | resolved | MAC-VPN spec name |
| `ingress_acl` | resolved | Ingress ACL table name |
| `egress_acl` | resolved | Egress ACL table name |
| `bgp_neighbor` | derived | Derived BGP neighbor IP |
| `bgp_peer_as` | `opts.PeerAS` | Resolved peer ASN |
| `peer_group` | derived | Peer group name (= service name when BGP) |
| `qos_policy` | resolved | QoS policy name |
| `vlan_id` | resolved/opts | VLAN ID |
| `l3vni` | resolved | L3 VNI from IP-VPN (for teardown) |
| `l3vni_vlan` | resolved | Transit VLAN from IP-VPN (for teardown) |
| `l2vni` | resolved | L2 VNI from MAC-VPN |
| `route_targets` | resolved | Comma-separated route targets |
| `redistribute_vrf` | derived | VRF redistribution flag |
| `anycast_ip` | resolved | SAG anycast IP |
| `anycast_mac` | resolved | SAG anycast MAC |
| `arp_suppression` | resolved | `"true"` if ARP suppression enabled |
| `route_reflector_client` | `opts.Params` | `"true"` if RR client (topology param) |
| `next_hop_self` | `opts.Params` | `"true"` if next-hop-self (topology param) |

`intentParamsToStepParams` selectively exports only: `service` (renamed from
`service_name`), `ip_address`, `peer_as` (renamed from `bgp_peer_as`),
`vlan_id`, `route_reflector_client`, `next_hop_self`. All other params are
re-resolved from specs at replay time.

---

#### `interface|Vlan{ID}` — configure-irb

IRB (Integrated Routing and Bridging) interface on a VLAN. Despite the
`interface|` prefix, this is dispatched as a **node-level** operation —
`intentInterface()` returns `""` for `OpConfigureIRB`.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + VLANName(vlanID)` (e.g., `"interface\|Vlan100"`) |
| **Operation** | `OpConfigureIRB` (`"configure-irb"`) |
| **Created by** | `ConfigureIRB()` in `vlan_ops.go` |
| **Deleted by** | `UnconfigureIRB()` in `vlan_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.ConfigureIRB(ctx, vlanID, config)` |
| **skipInReconstruct** | No |

**Parents:** `["vlan|{ID}"]`, plus `"vrf|{NAME}"` when VRF is set.

**Children:** none.

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vlan_id` | arg | VLAN ID (integer as string) |
| `vrf` | `IRBConfig.VRF` | VRF name |
| `ip_address` | `IRBConfig.IPAddress` | IP address (CIDR) |
| `anycast_mac` | `IRBConfig.AnycastMAC` | SAG anycast MAC |

---

### 7.6 Sub-Resources

Sub-resource intents are children of `interface|{INTF}`. They represent
configuration attached to an interface that has its own lifecycle independent
of the interface's primary binding.

#### `interface|{INTF}|bgp-peer`

Direct BGP peer on an interface (underlay, point-to-point). Distinct from
service-level BGP neighbors which use peer groups.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name + "\|bgp-peer"` |
| **Operation** | `OpAddBGPPeer` (`"add-bgp-peer"`) |
| **Created by** | `AddBGPPeer()` in `interface_bgp_ops.go` |
| **Deleted by** | `RemoveBGPPeer()` in `interface_bgp_ops.go` |
| **Reconstruct** | `replayInterfaceStep` → `iface.AddBGPPeer(ctx, config)` |
| **skipInReconstruct** | No |

**Parents:** `["interface|" + i.name]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `neighbor_ip` | `DirectBGPPeerConfig` | Peer IP address |
| `remote_as` | `DirectBGPPeerConfig` | Remote ASN (integer as string) |
| `description` | `DirectBGPPeerConfig` | Optional description |
| `multihop` | `DirectBGPPeerConfig` | eBGP multihop TTL (integer as string) |

---

#### `interface|{INTF}|qos`

QoS policy binding on an interface. Created explicitly via `ApplyQoS` or
implicitly by `ApplyService` when the service spec references a QoS policy.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name + "\|qos"` |
| **Operation** | `OpApplyQoS` (`"apply-qos"`) |
| **Created by** | `ApplyQoS()` in `qos_ops.go`; `ApplyService()` in `service_ops.go` |
| **Deleted by** | `RemoveQoS()` in `qos_ops.go`; `RemoveService()` in `service_ops.go` |
| **Reconstruct** | `replayInterfaceStep` → `iface.ApplyQoS(ctx, policyName, policy)` |
| **skipInReconstruct** | No |

**Parents:** `["interface|" + i.name]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `policy` | arg | QoS policy name |

---

#### `interface|{INTF}|acl|{DIR}`

ACL binding on an interface. Has **two parents**: the interface and the ACL
table. This dual-parent relationship is how the DAG tracks which interfaces
reference a shared ACL.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name + "\|acl\|" + direction` |
| **Operation** | `OpBindACL` (`"bind-acl"`) |
| **Created by** | `BindACL()` in `interface_ops.go` |
| **Deleted by** | `UnbindACL()` in `interface_ops.go`; `removeSharedACL()` in `service_ops.go` |
| **Reconstruct** | `replayInterfaceStep` → `iface.BindACL(ctx, aclName, direction)` |
| **skipInReconstruct** | No |

**Parents:** `["interface|" + i.name, "acl|" + aclName]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `acl_name` | arg | ACL table name |
| `direction` | arg | `"ingress"` or `"egress"` |

---

#### `interface|{INTF}|{PROPERTY}`

Interface property override (MTU, speed, admin status, description). Each
property is a separate intent so properties can be set and cleared
independently.

| Field | Value |
|-------|-------|
| **Resource key** | `"interface|" + i.name + "\|" + property` |
| **Operation** | `OpSetProperty` (`"set-property"`) |
| **Created by** | `SetProperty()` in `interface_ops.go` |
| **Deleted by** | `ClearProperty()` in `interface_ops.go`; `UnconfigureInterface()` cascade |
| **Reconstruct** | `replayInterfaceStep` → `iface.SetProperty(ctx, property, value)` |
| **skipInReconstruct** | No |

**Parents:** `["interface|" + i.name]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `property` | arg | Property name (`mtu`, `speed`, `admin-status`, `description`) |
| `value` | arg | Property value |

---

### 7.7 Infrastructure Sub-Resources

These intents are children of infrastructure intents (PortChannel, ACL) rather
than interface intents. They include PortChannel members, ACL rules, and
static routes. Reconstructed via `replayNodeStep` (§8).

#### `portchannel|{NAME}|{MEMBER}`

PortChannel member association.

| Field | Value |
|-------|-------|
| **Resource key** | `"portchannel|" + pcName + "\|" + member` |
| **Operation** | `OpAddPortChannelMember` (`"add-pc-member"`) |
| **Created by** | `AddPortChannelMember()` in `portchannel_ops.go` |
| **Deleted by** | `RemovePortChannelMember()` in `portchannel_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.AddPortChannelMember(ctx, pcName, name)` |
| **skipInReconstruct** | No |

**Parents:** `["portchannel|" + pcName]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `name` | arg | Member interface name |
| `portchannel` | arg | PortChannel name |

---

#### `acl|{NAME}|{RULE}`

ACL rule within an ACL table.

| Field | Value |
|-------|-------|
| **Resource key** | `"acl|" + tableName + "\|" + ruleName` |
| **Operation** | `OpAddACLRule` (`"add-acl-rule"`) |
| **Created by** | `AddACLRule()` in `acl_ops.go` |
| **Deleted by** | `DeleteACLRule()` in `acl_ops.go`; `removeSharedACL()` cascade |
| **Reconstruct** | `replayNodeStep` → `n.AddACLRule(ctx, aclName, ruleName, config)` |
| **skipInReconstruct** | No |

**Parents:** `["acl|" + tableName]`.

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `name` | arg | Rule name |
| `acl` | arg | ACL table name |
| `priority` | `ACLRuleConfig` | Priority (integer as string) |
| `action` | `ACLRuleConfig` | `FORWARD`, `DROP`, etc. |
| `src_ip` | `ACLRuleConfig` | Source IP prefix |
| `dst_ip` | `ACLRuleConfig` | Destination IP prefix |
| `protocol` | `ACLRuleConfig` | IP protocol |
| `src_port` | `ACLRuleConfig` | Source port |
| `dst_port` | `ACLRuleConfig` | Destination port |

---

#### `route|{VRF}|{PREFIX}`

Static route. Parent depends on VRF scope.

| Field | Value |
|-------|-------|
| **Resource key** | `"route|" + vrfName + "\|" + prefix` |
| **Operation** | `OpAddStaticRoute` (`"add-static-route"`) |
| **Created by** | `AddStaticRoute()` in `vrf_ops.go` |
| **Deleted by** | `RemoveStaticRoute()` in `vrf_ops.go` |
| **Reconstruct** | `replayNodeStep` → `n.AddStaticRoute(ctx, vrfName, prefix, nextHop, metric)` |
| **skipInReconstruct** | No |

**Parents:** `["vrf|" + vrfName]` for named VRFs; `["device"]` for default
VRF. The resource key uses whatever `vrfName` string the caller passes
(typically `"default"`, but `""` is also valid).

**Children:** none (leaf).

**Parameters:**

| Param | Source | Description |
|-------|--------|-------------|
| `vrf` | arg | VRF name (`"default"` for default VRF) |
| `prefix` | arg | Route prefix (CIDR) |
| `next_hop` | arg | Next-hop address |
| `metric` | arg | Route metric (integer as string, omitted when 0) |

## 8. Reconstruction

Reconstruction replays intent records (§7) to rebuild the CONFIG_DB
projection. This happens on connect (actuated mode, §4.1) and when building
abstract nodes for topology provisioning (§4.2). The pipeline is:

1. **Filter** — skip intents that are not actuated or are in
   `skipInReconstruct`.
2. **Topological sort** — Kahn's algorithm produces a deterministic order
   (parents before children, ties broken alphabetically by resource key).
3. **Replay** — each intent is converted to a `TopologyStep` via
   `IntentToStep` and executed via `ReplayStep`. `ReplayStep` dispatches to
   `replayNodeStep` or `replayInterfaceStep` based on URL structure.

`skipInReconstruct` contains two operations:

| Operation | Reason |
|-----------|--------|
| `interface-init` | Recreated as side effect when sub-resource operations first touch an interface |
| `deploy-service` | Recreated as side effect when the first `ApplyService` for a service replays |

These intents exist on the device but are excluded from the step list because
replaying them directly would conflict with their parent's side-effect
creation.

## 9. Worked Examples

### 9.1 Two-Interface Transit Service

This traces the intent operations for applying a `TRANSIT` service (routed,
with BGP) to two interfaces, then removing one.

#### Apply to Ethernet0

```
1. CreateVRF("Vrf_TRANSIT")
   → writeIntent("create-vrf", "vrf|Vrf_TRANSIT", {name: Vrf_TRANSIT}, ["device"])
   → device._children gains "vrf|Vrf_TRANSIT"

2. BindIPVPN("Vrf_TRANSIT", "IPVPN_TRANSIT")
   → writeIntent("bind-ipvpn", "ipvpn|Vrf_TRANSIT", {vrf, ipvpn, l3vni, l3vni_vlan}, ["vrf|Vrf_TRANSIT"])
   → vrf|Vrf_TRANSIT._children gains "ipvpn|Vrf_TRANSIT"

3. Service intent (first interface)
   → GetIntent("service|TRANSIT") = nil → createPeerGroup=true
   → addBGPRoutePolicies: create peer group + route maps
   → writeIntent("deploy-service", "service|TRANSIT",
       {service_name, route_map_in, route_map_out, route_policy_keys}, ["device"])
   → device._children gains "service|TRANSIT"

4. Interface intent
   → writeIntent("apply-service", "interface|Ethernet0",
       {service_name, service_type, vrf_name, ip_address, ...},
       ["vrf|Vrf_TRANSIT", "service|TRANSIT"])
   → vrf|Vrf_TRANSIT._children gains "interface|Ethernet0"
   → service|TRANSIT._children gains "interface|Ethernet0"
```

#### Apply to Ethernet4

```
5. CreateVRF("Vrf_TRANSIT")
   → writeIntent: vrf|Vrf_TRANSIT exists, parents match → idempotent update

6. BindIPVPN("Vrf_TRANSIT", "IPVPN_TRANSIT")
   → writeIntent: ipvpn|Vrf_TRANSIT exists, parents match → idempotent update

7. Service intent (second interface)
   → GetIntent("service|TRANSIT") ≠ nil → createPeerGroup=false
   → addBGPRoutePolicies: route maps idempotent (content hash unchanged)
   → writeIntent: service|TRANSIT exists, parents match → update params

8. Interface intent
   → writeIntent("apply-service", "interface|Ethernet4",
       {...}, ["vrf|Vrf_TRANSIT", "service|TRANSIT"])
   → vrf|Vrf_TRANSIT._children gains "interface|Ethernet4"
   → service|TRANSIT._children gains "interface|Ethernet4"
```

#### Remove from Ethernet0

```
9. isLastServiceUser check
   → service|TRANSIT._children = ["interface|Ethernet0", "interface|Ethernet4"]
   → "interface|Ethernet4" ≠ excludeKey → isLastServiceUser=false

10. Delete QoS sub-intent (if exists)
    → deleteIntent("interface|Ethernet0|qos")
    → interface|Ethernet0._children shrinks

11. Delete interface intent
    → deleteIntent("interface|Ethernet0")
    → vrf|Vrf_TRANSIT._children loses "interface|Ethernet0"
    → service|TRANSIT._children loses "interface|Ethernet0"

12. Not last user → skip route policy and peer group deletion
    → service|TRANSIT stays (still has "interface|Ethernet4" child)
    → VRF stays (still has children)
```

### 9.2 RefreshService with Spec Change (Blue-Green)

This traces what happens when the TRANSIT service spec changes (e.g., a
route policy rule is added) and `RefreshService` is called on Ethernet0.
Ethernet4 also has TRANSIT but is not refreshed yet.

```
1. Capture old route policy keys from service intent
   → si = GetIntent("service|TRANSIT")
   → oldRoutePolicyKeys = "ROUTE_MAP:TRANSIT_IMPORT_abc123;PREFIX_SET:TRANSIT_PFX_def456"

2. RemoveService(Ethernet0)
   → isLastServiceUser check: service|TRANSIT._children has Ethernet4 → false
   → Per-interface cleanup (BGP neighbor, VLAN member, etc.)
   → deleteIntent("interface|Ethernet0")
   → service|TRANSIT._children loses "interface|Ethernet0"
   → Route policies and peer group kept (not last user)

3. ApplyService(Ethernet0, "TRANSIT", ...) — re-resolves spec
   → Spec changed → new route policy content → new hashes
   → Service intent exists → createPeerGroup=false
   → addBGPRoutePolicies generates new content-hashed objects:
     ROUTE_MAP:TRANSIT_IMPORT_xyz789, PREFIX_SET:TRANSIT_PFX_uvw012
   → writeIntent: service|TRANSIT updated with new route_policy_keys
   → writeIntent: interface|Ethernet0 re-registered as child

4. Capture new route policy keys from service intent
   → newRoutePolicyKeys = "ROUTE_MAP:TRANSIT_IMPORT_xyz789;PREFIX_SET:TRANSIT_PFX_uvw012"

5. Diff old vs new
   → diffRoutePolicyKeyCSV(old, new) returns:
     "ROUTE_MAP:TRANSIT_IMPORT_abc123;PREFIX_SET:TRANSIT_PFX_def456"
   → These are stale — old hashes no longer referenced

6. BUT: Ethernet4 still has the old route maps in its BGP_PEER_GROUP_AF
   → The stale keys are deleted from CONFIG_DB
   → When Ethernet4 is refreshed, it will get the new hashes too

7. After both interfaces refresh:
   → service|TRANSIT.route_policy_keys has only new hashes
   → Old ROUTE_MAP/PREFIX_SET entries fully cleaned up
```

The blue-green window is steps 3-5: both old and new route policy objects
exist simultaneously in CONFIG_DB. The old objects are deleted in step 5.
In a multi-interface scenario, each `RefreshService` independently migrates
its interface to the new hashes and cleans up the old ones when they become
stale relative to the service intent's current keys.

## 10. Code Locations

| File | Intents |
|------|---------|
| `baseline_ops.go` | `device` |
| `vlan_ops.go` | `vlan\|ID`, `interface\|Vlan{ID}` |
| `vrf_ops.go` | `vrf\|NAME`, `ipvpn\|VRFNAME`, `route\|VRF\|PREFIX` |
| `acl_ops.go` | `acl\|NAME`, `acl\|NAME\|RULE` |
| `portchannel_ops.go` | `portchannel\|NAME`, `portchannel\|NAME\|MEMBER` |
| `bgp_ops.go` | `evpn-peer\|ADDR` |
| `interface_ops.go` | `interface\|INTF` (init, configure), `interface\|INTF\|acl\|DIR`, `interface\|INTF\|PROPERTY` |
| `interface_bgp_ops.go` | `interface\|INTF\|bgp-peer` |
| `qos_ops.go` | `interface\|INTF\|qos` |
| `service_ops.go` | `service\|NAME`, `interface\|INTF` (apply-service) |
| `intent_ops.go` | `writeIntent`, `deleteIntent`, `GetIntent`, `IntentsByPrefix`, `IntentsByParam` |
| `reconstruct.go` | `IntentsToSteps`, `ReplayStep`, `skipInReconstruct` |

## 11. Summary Table

| # | Resource Key | Op | Parents | Children | skipInReconstruct |
|---|---|---|---|---|---|
| 1 | `device` | `setup-device` | (root) | all top-level | No |
| 2 | `vlan\|ID` | `create-vlan` | `[device]` | macvpn, IRB, interfaces | No |
| 3 | `vrf\|NAME` | `create-vrf` | `[device]` | ipvpn, routes, interfaces, IRB | No |
| 4 | `acl\|NAME` | `create-acl` | `[device]` | rules, ACL bindings | No |
| 5 | `portchannel\|NAME` | `create-portchannel` | `[device]` | members, interfaces | No |
| 6 | `evpn-peer\|ADDR` | `add-bgp-evpn-peer` | `[device]` | (leaf) | No |
| 7 | `service\|NAME` | `deploy-service` | `[device]` | interfaces (BGP svc) | **Yes** |
| 8 | `route\|VRF\|PREFIX` | `add-static-route` | `[vrf\|NAME]` or `[device]` | (leaf) | No |
| 9 | `macvpn\|VLANID` | `bind-macvpn` | `[vlan\|ID]` | (leaf) | No |
| 10 | `ipvpn\|VRFNAME` | `bind-ipvpn` | `[vrf\|NAME]` | (leaf) | No |
| 11 | `interface\|Vlan{ID}` | `configure-irb` | `[vlan\|ID]` ± `[vrf\|NAME]` | (leaf) | No |
| 12 | `interface\|INTF` | `interface-init` | `[device]` ± `[portchannel]` | sub-resources | **Yes** |
| 13 | `interface\|INTF` | `configure-interface` | varies (§7.5) | sub-resources | No |
| 14 | `interface\|INTF` | `apply-service` | varies (§7.5) | qos | No |
| 15 | `interface\|INTF\|bgp-peer` | `add-bgp-peer` | `[interface\|INTF]` | (leaf) | No |
| 16 | `interface\|INTF\|qos` | `apply-qos` | `[interface\|INTF]` | (leaf) | No |
| 17 | `interface\|INTF\|acl\|DIR` | `bind-acl` | `[interface\|INTF, acl\|NAME]` | (leaf) | No |
| 18 | `interface\|INTF\|PROPERTY` | `set-property` | `[interface\|INTF]` | (leaf) | No |
| 19 | `portchannel\|NAME\|MEMBER` | `add-pc-member` | `[portchannel\|NAME]` | (leaf) | No |
| 20 | `acl\|NAME\|RULE` | `add-acl-rule` | `[acl\|NAME]` | (leaf) | No |
