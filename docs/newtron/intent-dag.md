# Intent DAG Architecture

## 1. Purpose

Every NEWTRON_INTENT record participates in a directed acyclic graph (DAG)
that encodes structural dependencies between configuration resources. The DAG
answers three questions:

1. **Can I create this resource?** — All parents must exist.
2. **Can I delete this resource?** — All children must be gone.
3. **Does anything depend on this resource?** — Read `_children`.

The DAG replaced:
- `DependencyChecker` CONFIG_DB scanning for reference counting
- `stepPriority` map for reconstruction ordering
- Dual-tracking CSV fields (`members`, `rules`) on parent intents
- Manual precondition checks that verify prerequisite resources exist

## 2. Intent Record Structure

Every NEWTRON_INTENT record stores domain parameters alongside two DAG
metadata fields:

```
NEWTRON_INTENT|interface|Ethernet0 → {
    operation:  "configure-interface"
    state:      "actuated"
    vlan_id:    "100"
    tagged:     "false"
    _parents:   "vlan|100"                              ← DAG: member of VLAN 100
    _children:  "interface|Ethernet0|qos,interface|Ethernet0|mtu"  ← DAG: sub-resources
}
```

| Field | Type | Lifecycle | Writer |
|-------|------|-----------|--------|
| `_parents` | CSV of resource keys | Set at creation, immutable, cleared at deletion | `writeIntent` of this record |
| `_children` | CSV of resource keys | Empty at creation, grows/shrinks as children register/deregister | `writeIntent`/`deleteIntent` of child records |

The `_parents` and `_children` fields are stored as regular fields in the
NEWTRON_INTENT Redis hash. The underscore prefix distinguishes them from
domain parameters. `NewIntent` strips them from `Params` into dedicated
fields; `ToFields` serializes them back.

`_parents` and `_children` are added to `intentIdentityFields` so they are
excluded from the `Params` map — they are DAG metadata, not domain parameters.

### 2.1 Kind-Prefixed Resource Keys

Every intent resource key follows the pattern `kind|identity`:

```
device                              kind=device  identity=(singleton)
vlan|100                            kind=vlan    identity=100
vrf|CUSTOMER                        kind=vrf     identity=CUSTOMER
acl|EDGE_IN                         kind=acl     identity=EDGE_IN
interface|Ethernet0                 kind=interface  identity=Ethernet0
interface|Ethernet0|qos             kind=interface  identity=Ethernet0|qos
macvpn|100                          kind=macvpn  identity=100
route|CUSTOMER|10.0.0.0/8           kind=route   identity=CUSTOMER|10.0.0.0/8
```

The kind is always the first segment (before the first `|`). Splitting on the
first pipe extracts it uniformly. The `device` singleton has no pipe —
`strings.SplitN("device", "|", 2)` returns a single-element slice `["device"]`.
Code that extracts kind must handle this: if the split produces one element,
the kind is the entire key and the identity is empty (singleton).

**Why this matters.** NEWTRON_INTENT is a single flat Redis hash namespace — all
resource types share one table. Unlike CONFIG_DB, where the table name itself
carries the type (`ACL_TABLE`, `VLAN`, `VRF`), intent keys have no table-level
discriminator. The kind prefix **is** the type discriminator. Without it, code
that processes intent keys needs external knowledge or special-case logic to
determine what a key represents.

The kind prefix enables:

- **Uniform parseability.** `strings.SplitN(key, "|", 2)` extracts `[kind, rest]`
  for every key in the table. No regex, no interface-name detection heuristics.
- **Kind-based filtering.** `newtron intent tree switch1 vlan` finds all keys
  where kind=`vlan`. `newtron intent tree switch1 interface` finds all
  interface-scoped intents. The §12 CLI feature depends on this.
- **Self-describing keys.** Reading `interface|Ethernet0|qos` tells you what it
  is. Reading `Ethernet0|qos` requires you to know that `Ethernet0` is an
  interface name and not a kind prefix.
- **No special cases.** Without the prefix, interface keys are the sole exception
  to the `kind|identity` convention. Every piece of code that parses intent keys
  — tree walk, health checks, reconstruction, orphan detection — would need an
  `else if looksLikeInterfaceName(key)` branch. One exception invites more.
- **Consistent with the Unified Naming Convention** (CLAUDE.md §31). The CONFIG_DB
  convention says "the table name carries the object kind" so keys don't repeat
  it. NEWTRON_INTENT has one table for all kinds — therefore the key must carry
  the kind.

## 3. Invariants

These invariants hold at all times. Violation indicates a code bug.

**I1 — Acyclicity.** The parent graph contains no cycles.

**I2 — Bidirectional consistency.** If child C lists parent P in `C._parents`,
then P lists C in `P._children`. Conversely, if P lists C in `P._children`,
then C lists P in `C._parents`.

**I3 — Referential integrity.** Every resource key in `_parents` and
`_children` points to an existing intent record.

**I4 — Parent existence on creation.** `writeIntent` for a child fails if any
declared parent does not have an existing intent record.

**I5 — Child absence on deletion.** `deleteIntent` for a parent fails if
`_children` is non-empty.

**I6 — Child-only relationship writes.** Only a child's `writeIntent` or
`deleteIntent` modifies the `_children` field of a parent. No other code
path writes to `_children`. Parents never modify their own `_children`.

**I7 — Parent immutability (of children).** A parent never writes, updates,
or deletes a child's intent record.

**I8 — DAG metadata exclusivity.** `_parents` and `_children` are exclusively
managed by `writeIntent` and `deleteIntent`. No other code path modifies
these fields. (`updateIntent` was eliminated — see §4.3.)

### Proof: Cycles Are Structurally Impossible

`_parents` is set at creation time (I4) and never modified afterward.
`writeIntent` on an existing key requires the same parents (idempotent
update — §4.1) or rejects the call; it never changes `_parents`. The only
way to introduce a cycle A→B→...→A would be to create A with B as a parent
while B (directly or transitively) already has A as a parent. But:

- If A already exists and B lists A as an ancestor, then creating A with
  parent B requires the same parents A already has (idempotent update). Since
  A already exists, its parents are fixed — they cannot be changed to include B.
- If A does not exist, then B cannot list A as an ancestor (I3 — no dangling
  references). Creating A with parent B is safe.

In either case, no cycle can form. `_parents` immutability plus I4 and I5
make cycles structurally impossible without runtime cycle detection.

## 4. Core Operations

### 4.1 writeIntent(cs, operation, resource, params, parents)

Creates a new intent record and registers with all declared parents.

```go
func (n *Node) writeIntent(cs *ChangeSet, op, resource string, params map[string]string, parents []string) error
```

The `parents` parameter may be nil or empty for root resources.

**Preconditions:**
- Every resource key in `parents` has an existing intent record (I4)
- If intent already exists at `resource`: `parents` must equal the existing
  `_parents` (same-parent idempotent update). Different parents → error.

**Actions:**
1. Check if intent already exists at `resource`:
   - If exists and `parents` match existing `_parents` → idempotent update:
     replace params, preserve `_parents` and `_children`, skip parent
     registration (child is already in each parent's `_children`). Go to step 3.
   - If exists and `parents` differ → return error (caller must delete and
     recreate to change parents)
2. For each parent P in `parents` (new intent only):
   - Read P's intent record from configDB
   - If P does not exist → return error (I4 violated)
   - Append `resource` to P's `_children` CSV
   - Add updated P intent record to ChangeSet (via `cs.Add`)
   - In offline mode: apply P update to shadow configDB immediately
3. Build intent fields with `_parents` = CSV of `parents`,
   `_children` = existing children (idempotent update) or "" (new)
4. Add intent record to ChangeSet (via `cs.Prepend`)
5. In offline mode: apply to shadow configDB

**Post-conditions:**
- Intent record exists at `resource` with `_parents` set
- Each parent's `_children` includes `resource`
- I2 holds

**Error format:**
```
writeIntent "interface|Ethernet0": parent "vlan|100" does not exist
```

### 4.2 deleteIntent(cs, resource)

Removes an intent record and deregisters from all parents.

```go
func (n *Node) deleteIntent(cs *ChangeSet, resource string) error
```

**Preconditions:**
- Intent record exists at `resource`
- `_children` is empty (I5)

**Actions:**
1. Read own intent record
2. If `_children` is non-empty → return error (I5 violated)
3. For each parent P in `_parents`:
   - Read P's intent record from configDB
   - Remove `resource` from P's `_children` CSV
   - Add updated P intent record to ChangeSet
   - In offline mode: apply P update to shadow configDB immediately
4. Add delete entry for own intent to ChangeSet (via `cs.Delete`)
5. In offline mode: delete from shadow configDB

**Post-conditions:**
- No intent record at `resource`
- No parent lists `resource` in `_children`
- I2 and I3 hold

**Error format:**
```
deleteIntent "vlan|100": has children [interface|Ethernet0, macvpn|100]
```

The error message lists all children, giving the caller explicit information
about what must be removed first.

### 4.3 updateIntent — Eliminated

`updateIntent` was eliminated. All intent modifications go through
`writeIntent` (idempotent update with same parents) or `deleteIntent` +
`writeIntent` (parent change). Domain parameters that change are handled
by delete-and-recreate.

## 5. Authority Model

### 5.1 Children declare parents

When an operation calls `writeIntent`, it passes the `parents` parameter.
The operation code determines its own parents from the arguments it received:

```go
// ConfigureInterface knows it depends on the VLAN
resource := "interface|" + i.name
parents := []string{"vlan|" + strconv.Itoa(cfg.VLAN)}
n.writeIntent(cs, sonic.OpConfigureInterface, resource, params, parents)
```

The parent does not participate in this decision.

### 5.2 Children register with parents

`writeIntent` auto-appends the child to each parent's `_children`. No
parent code is involved. The parent's `_children` field grows passively.

### 5.3 Children deregister from parents

`deleteIntent` auto-removes the child from each parent's `_children`. No
parent code is involved. The parent's `_children` field shrinks passively.

### 5.4 Parents are passive

A parent intent record never calls `writeIntent` or `deleteIntent` targeting
a child resource key. Parents only read their own `_children` (implicitly,
via `deleteIntent`'s emptiness check).

### 5.5 Orchestrators are not parents

`RemoveService` is an orchestrator that calls each sub-operation's reverse
method. Each reverse method deletes its own intent (the child deletes itself
via `deleteIntent`). The orchestrator invokes the child's own domain method —
it does not reach into the child's intent record.

```
RemoveService orchestrates:
  1. iface.RemoveQoS()     → sub-resource deletes own intent, deregisters from interface
  2. iface.UnbindACL()     → binding deletes own intent, deregisters from interface + ACL
  3. n.deleteIntent("interface|Ethernet0")  → interface intent deleted (children already gone)
```

The distinction: the orchestrator *calls methods*. The parent *is a record*.
Methods may trigger `deleteIntent` on child records, but the parent record
itself never writes to children.

### 5.6 No cascade deletes

`deleteIntent` on a parent with non-empty `_children` fails. Period.

The caller must explicitly remove all children first, in the correct order,
using each child's own reverse domain method. There is no "force delete" or
"delete with cascade" option.

This is intentional: cascade deletes would require the parent to know how to
tear down each child type — violating the single-responsibility principle and
creating a second code path for every teardown operation.

**Implementation note**: `cascadeDeleteChildren` was a temporary helper that
violated this principle. It has been eliminated. `deleteIntent` failure is
the sole enforcement mechanism — callers must handle the error by removing
children explicitly in the correct order.

## 6. Ordering

### 6.1 Creation: Top-Down

Parents are created before children. Enforced mechanically by I4: `writeIntent`
fails if a declared parent does not exist.

```
CreateVLAN(100)                     → vlan|100, _parents: [device]
ConfigureInterface(Eth0, VLAN=100)  → interface|Ethernet0, _parents: [vlan|100]  ✓
Node.BindMACVPN(100, 20100)         → macvpn|100, _parents: [vlan|100]  ✓
```

Attempting child-first:
```
ConfigureInterface(Eth0, VLAN=100)  → ERROR: parent vlan|100 does not exist
```

### 6.2 Deletion: Bottom-Up

Children are removed before parents. Enforced mechanically by I5: `deleteIntent`
fails if `_children` is non-empty.

```
DeleteVLAN(100)                     → ERROR: children [interface|Ethernet0, macvpn|100]
UnconfigureInterface(Eth0)          → deletes interface|Ethernet0, deregisters from vlan|100
UnbindMACVPN(100)                   → deletes macvpn|100, deregisters from vlan|100
DeleteVLAN(100)                     → OK: _children = [] → deletes vlan|100  ✓
```

### 6.3 Reconstruction Ordering

`IntentsToSteps` performs a topological sort of the DAG to determine
reconstruction order:

1. Read all NEWTRON_INTENT records
2. Build the DAG from `_parents`/`_children` fields
3. Topological sort (Kahn's algorithm): parents before children
4. Replay steps in topological order

Ties within the same topological level are broken by resource key
(deterministic). This automatically handles new operation types — no
manually maintained priority numbers are needed.

### 6.4 Order Enforcement Subsumed by the DAG

| Former mechanism | DAG replacement |
|---|---|
| `stepPriority` map | Topological sort of DAG |
| `pc.RequireVLANExists()` | I4: parent must exist |
| `pc.RequireVRFExists()` | I4: parent must exist |
| `pc.RequireACLExists()` | I4: parent must exist |
| `DependencyChecker.IsLastVRFUser()` | Check `vrf|NAME._children` emptiness |
| `DependencyChecker.IsLastACLUser()` | Check `acl|NAME._children` emptiness |
| `DependencyChecker.IsLastVLANUser()` | Check `vlan|ID._children` emptiness |

**What remains independent of the DAG:**
- CONFIG_DB entry ordering within a ChangeSet (Redis/SONiC daemon processing
  order — leafref dependencies between CONFIG_DB entries)
- State-based preconditions (device connected, locked, admin_status)
- `RefreshService` CONFIG_DB scanning (observation of ground truth, not
  teardown — justified exception per intent-audit-checklist.md)

## 7. Dual-Tracking Elimination

The system uses `_children` exclusively for parent-child relationship
tracking. The former dual-tracking approach — where child operations called
`updateIntent` on the parent to maintain CSV fields (`members`, `rules`,
`vni`) — has been eliminated. Parent destroy operations do not enumerate
children; children must be removed first (§5.6). The parent's destroy
function reduces to deleting its own CONFIG_DB entry and its own intent record.

| Former dual-tracking field | On parent | Replaced by |
|---|---|---|
| `members` | `vlan\|ID` | `_children` |
| `vni` | `vlan\|ID` | `_children` (VNI in child intent) |
| `rules` | `acl\|NAME` | `_children` |
| `members` | `portchannel\|NAME` | `_children` |
| `route_policy_keys` | `interface\|INTF` | `_children` |

The `_children` field provides the same information (which resources depend
on me) without domain-specific CSV formats.

**Consequence: simpler destroy functions.** `destroyVlanConfig` is now
"delete VLAN" — not "read members from intent, generate VLAN_MEMBER deletes,
read VNI, generate VXLAN_TUNNEL_MAP deletes, delete VLAN". The VLAN_MEMBER
and VXLAN_TUNNEL_MAP entries are already deleted when the children are removed.

## 8. Destroy Function Simplification

Every destroy function follows the same pattern:

```go
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
    resource := "vlan|" + strconv.Itoa(vlanID)
    cs := NewChangeSet(n.Name(), "delete-vlan")
    // Check DAG first — refuse if children exist (I5)
    if err := n.deleteIntent(cs, resource); err != nil {
        return nil, err  // "has children [interface|Ethernet0, macvpn|100]"
    }
    // Only add CONFIG_DB deletes after DAG check passes
    cs.Delete("VLAN", VLANName(vlanID))
    n.applyShadow(cs)
    return cs, nil
}
```

`deleteIntent` returns an error if `_children` is non-empty (I5). The caller
must handle the error — which means if children exist, no CONFIG_DB
modifications are made.

The function does not enumerate members, VNI mappings, IRBs, or any other
child resources. If any children still exist, `deleteIntent` returns an error
listing them. The caller must remove children first using their own domain
reverse methods.

This applies uniformly to all container resources:

| Resource | Current destroy logic | DAG destroy logic |
|---|---|---|
| VLAN | Read members, VNI from intent; generate child deletes | Delete VLAN entry + own intent |
| ACL | Read rules from intent; generate ACL_RULE deletes | Delete ACL_TABLE entry + own intent |
| PortChannel | Read members from intent; generate PC_MEMBER deletes | Delete PORTCHANNEL entry + own intent |
| VRF | Delete VRF entry + intent (current — already simple) | Same |

## 9. Composite Operations

Composite operations (ApplyService, SetupDevice) orchestrate multiple
primitive operations within a single method call.

### 9.1 ApplyService

ApplyService creates infrastructure (VLAN, VRF if not existing), configures
the interface, and sets up service-specific resources (BGP, ACLs, route
policies):

- Infrastructure intents (`vlan|ID`, `vrf|NAME`) are roots or have their own
  parent relationships, created via their respective methods
- The interface intent (`interface|INTF`) declares infrastructure as parents:
  `_parents: ["vlan|ID", "vrf|NAME"]` (varies by service type — see §10.7)
- Interface sub-resource intents (QoS, ACL bindings) declare the interface
  intent as parent (§10.11.1)

This creates a multi-level tree per service application:

```
vlan|100                           vrf|CUSTOMER
    ↑                                  ↑
    └──── interface|Ethernet0 ─────────┘
              (apply-service)
              ↑               ↑
              │               └── interface|Ethernet0|acl|ingress
              └── interface|Ethernet0|qos
```

The interface intent IS the service intent — `ApplyService` writes to
`interface|INTF` (§10.7). There is no separate service key; the interface is
the point of service delivery.

RemoveService orchestrates bottom-up: remove sub-resource children (QoS,
ACL bindings), then remove the interface intent (which deregisters from VLAN,
VRF), then conditionally delete VLAN/VRF if no remaining children.

### 9.2 Same-ChangeSet Parent-Child Creation

When a composite operation creates a parent and child in the same ChangeSet,
the parent must be visible in configDB before the child's `writeIntent` runs
(I4). Two approaches:

**Approach A — Sequential shadow updates.** After writing the parent intent
entry to the ChangeSet, apply it to the shadow immediately (offline mode
already does this in `writeIntent`). The child's subsequent `writeIntent`
sees the parent in configDB.

This works when the composite calls primitive methods sequentially:
```go
csVLAN, _ := n.CreateVLAN(ctx, 100, ...)  // writes vlan|100 intent, applyShadow
csSvc, _  := iface.ApplyService(ctx, ...)  // writeIntent sees vlan|100 in shadow
cs.Merge(csVLAN, csSvc)
```

**Approach B — Pending intent tracking.** `writeIntent` checks both configDB
AND a "pending" set of intents added to the current ChangeSet. This handles
the case where parent and child are created within a single method's ChangeSet.

The choice between approaches is an implementation decision. The invariant is
the same: parent must be visible (in configDB or pending) when child
`writeIntent` executes.

### 9.3 SetupDevice — Universal Root

`device` is the sole root of the entire DAG. Every other intent record is a
direct or transitive child of `device`. This provides two guarantees:

1. **The entire intent tree is discoverable from a single entry point.**
   Reading `device._children`, then recursively reading each child's
   `_children`, enumerates every intent record on the device.

2. **Baseline cannot be torn down while resources exist.**
   `deleteIntent("device")` refuses if any children remain — which means
   VLANs, VRFs, ACLs, services, etc. must all be removed first.

SetupDevice writes the `device` intent as a root (no parents). Baseline
sub-operations (loopback, BGP globals, VTEP) do not write individual intent
records — their collective reverse is reprovision (CompositeOverwrite), not
individual teardown.

SetupDevice cannot be "redone" incrementally. To change baseline
configuration, reprovision the device.

### 9.4 CompositeOverwrite Bypasses the DAG

CompositeOverwrite (reprovision) replaces the entire CONFIG_DB atomically —
it DELs every key and HSETs the new state, including all NEWTRON_INTENT
records. This is the one operation that bypasses DAG enforcement:

- It does not call `deleteIntent` for existing records
- It does not check `_children` emptiness
- It does not deregister from parents

Instead, the abstract Node that builds the composite runs SetupDevice,
CreateVLAN, ApplyService etc. in order, accumulating fresh intent records
with correct DAG links. The composite delivery replaces everything —
including the DAG — atomically.

This is consistent with CLAUDE.md: "Provisioning (CompositeOverwrite) is
the one operation where intent replaces reality."

## 10. Complete Intent Record Catalog

This section is the authoritative reference for every NEWTRON_INTENT record
in the system. It is organized **by intent record** — for each resource key
pattern, every operation that creates, updates, or deletes that intent is
listed.

Notation:
- **Create** = `writeIntent` (creates the record)
- **Delete** = `deleteIntent` (removes the record)
- **Read** = `GetIntent` (reads the record for teardown — listed to show which reverse ops consume the intent)

---

### 10.1 `device`

Universal root. Every other intent is a direct or transitive descendant.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | setup-device | `SetupDevice` | baseline_ops.go:48 |

**Parents**: `[]` (root — no parents)
**Reverse**: reprovision (CompositeOverwrite). No individual reverse operation.
`deleteIntent("device")` refuses if any children exist.

---

### 10.2 `vlan|ID`

VLAN container. Children include interface bindings (configure-interface,
apply-service in bridged mode), MAC-VPN bindings, and IRB configs.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | create-vlan | `CreateVLAN` | vlan_ops.go |
| Read | (destroy) | `destroyVlanConfig` | vlan_ops.go |
| Delete | delete-vlan | `DeleteVLAN` | vlan_ops.go |

**Parents**: `[device]`
**Children**: interface bindings, MAC-VPN bindings, IRB configs — tracked via `_children`.

---

### 10.3 `vrf|NAME`

VRF container. Children include interface bindings (configure-interface,
apply-service in routed/IRB mode), IP-VPN bindings, and static routes.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | create-vrf | `CreateVRF` | vrf_ops.go:154 |
| Delete | delete-vrf | `DeleteVRF` | vrf_ops.go:184 |

**Parents**: `[device]`

---

### 10.4 `acl|NAME`

ACL table. Can be created standalone or by `ApplyService` for shared filter ACLs.
Children include ACL rules and interface ACL bindings.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | create-acl | `CreateACL` | acl_ops.go |
| Create | apply-service (shared ACL) | `ApplyService` | service_ops.go |
| Read | (destroy) | `deleteAclTableConfig` | acl_ops.go |
| Delete | delete-acl | `DeleteACL` | acl_ops.go |
| Delete | remove-service (last ACL user) | `removeSharedACL` | service_ops.go |

**Parents**: `[device]`
**Note**: `CreateACL` and `ApplyService` both write the same resource key format.
Two creators for one intent — the ACL is shared infrastructure. `RemoveService`
deletes the ACL intent only when it is the last consumer (via `_children` check).
Per-rule child intents (`acl|NAME|RULE`, §10.16) exist for the "is last user"
ACL teardown path.

---

### 10.5 `portchannel|NAME`

PortChannel container. Children include member interface intents.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | create-portchannel | `CreatePortChannel` | portchannel_ops.go |
| Read | (destroy) | `destroyPortChannelConfig` | portchannel_ops.go |
| Delete | delete-portchannel | `DeletePortChannel` | portchannel_ops.go |

**Parents**: `[device]`
**Note**: Per-member child intents (`portchannel|NAME|MEMBER`, §10.17) encode
membership via `_children`.

---

### 10.6 `evpn-peer|ADDR`

EVPN overlay BGP peer (loopback-to-loopback eBGP). Leaf intent — no children.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | add-bgp-evpn-peer | `AddBGPEVPNPeer` | bgp_ops.go:466 |
| Delete | remove-bgp-evpn-peer | `RemoveBGPEVPNPeer` | bgp_ops.go:487 |

**Parents**: `[device]`

---

### 10.7 `interface|INTF`

The interface anchor intent. Two operations write intent directly at
`interface|INTF` (e.g., `"interface|Ethernet0"`): `ConfigureInterface` and
`ApplyService`. `AddBGPPeer` creates a sub-resource intent at
`interface|INTF|bgp-peer` (§10.18) and uses `ensureInterfaceIntent` to
lazily create the anchor if it does not already exist (§10.11.1).

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | configure-interface | `ConfigureInterface` | interface_ops.go |
| Read | unconfigure-interface | `UnconfigureInterface` | interface_ops.go |
| Delete | unconfigure-interface | `UnconfigureInterface` | interface_ops.go |
| Create | apply-service | `ApplyService` | service_ops.go |
| Read | remove-service | `RemoveService` (via `deleteRoutePoliciesFromIntent`) | service_ops.go |
| Delete | remove-service | `RemoveService` | service_ops.go |
| Create | interface-init (lazy anchor) | `ensureInterfaceIntent` | interface_ops.go |

**Parents** (vary by operation and service type):
- configure-interface bridged: `[vlan|ID]`
- configure-interface routed: `[vrf|NAME]`
- configure-interface IP-only: `[device]`
- apply-service bridged/evpn-bridged: `[vlan|ID]`
- apply-service routed: `[vrf|NAME]`
- apply-service irb/evpn-irb: `[vlan|ID, vrf|NAME]`
- interface-init (lazy anchor): `[device]`

**Mutual exclusivity**: An interface has exactly one role at a time. Changing
roles requires the caller to remove the current intent (via the appropriate
reverse operation — `UnconfigureInterface`, `RemoveService`, or
`RemoveBGPPeer`) before applying a new one. The reverse operation calls
`deleteIntent`, which deregisters from the old parents. The new forward
operation calls `writeIntent`, which registers with the new parents. This
sequence preserves DAG consistency — there is no in-place overwrite that
would leave stale parent `_children` entries.

---

### 10.8 `interface|INTF|acl|DIR`

ACL binding on an interface (ingress or egress). The direction is part of the
resource key (e.g., `interface|Ethernet0|acl|ingress`).

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | bind-acl | `BindACL` | interface_ops.go:294 |
| Read | unbind-acl | `UnbindACL` (scans ingress+egress) | interface_ops.go:329 |
| Delete | unbind-acl | `UnbindACL` | interface_ops.go:351 |

**Parents**: `[interface|INTF, acl|NAME]` — multi-parent. The interface must
have an intent and the ACL table must exist before a binding can be created.
Neither parent can be deleted while the binding exists.

---

### 10.9 `interface|INTF|qos`

QoS policy binding on an interface.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | apply-qos | `Interface.ApplyQoS` | qos_ops.go:26 |
| Read | remove-qos | `Interface.RemoveQoS` | qos_ops.go:81 |
| Delete | remove-qos | `Interface.RemoveQoS` | qos_ops.go:94 |

**Parents**: `[interface|INTF]`

---

### 10.10 `interface|INTF|macvpn`

Interface-level MAC-VPN binding (binds the physical interface to the VXLAN
tunnel map). Distinct from the node-level `macvpn|VLANID` intent.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | bind-macvpn | `Interface.BindMACVPN` | macvpn_ops.go:68 |
| Read | unbind-macvpn | `Interface.UnbindMACVPN` | macvpn_ops.go:89 |
| Delete | unbind-macvpn | `Interface.UnbindMACVPN` | macvpn_ops.go:108 |

**Parents**: `[interface|INTF]`

---

### 10.11 `interface|INTF|PROPERTY`

Port property intent (mtu, speed, admin_status, description). Each property
gets its own intent record (e.g., `interface|Ethernet0|mtu`, `interface|Ethernet0|speed`).

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | set-port-property | `SetProperty` | interface_ops.go:373 |
| Create (overwrite) | set-port-property | `SetProperty` (called again with new value) | interface_ops.go:373 |

**Parents**: `[interface|INTF]`
**Reverse**: `SetProperty` is its own reverse — call it again with the new or
default value. `writeIntent` on an existing key with the same
parents is treated as an idempotent update: params are replaced, `_parents`
and `_children` are preserved, and no parent re-registration occurs (the
child is already in the parent's `_children`). If a `writeIntent` call
targets an existing key with *different* parents, it is an error — the caller
must delete and recreate. `SetProperty` always uses the same parent
(`interface|INTF`), so the idempotent path applies. These are persistent
leaves that remain until reprovision.

### 10.11.1 Interface Intent as Anchor

The `interface|INTF` intent is the anchor for all interface-scoped
sub-resources. QoS, ACL bindings, MAC-VPN bindings, and port properties
cannot exist without an interface intent — there is no purpose in configuring
properties on an interface that the intent system doesn't know about.

The interface intent is created by `ConfigureInterface`, `ApplyService`, or
`ensureInterfaceIntent` (§10.7). Sub-resource operations (`ApplyQoS`,
`BindACL`, `SetProperty`, `AddBGPPeer`, etc.) require the interface intent
to exist and declare it as their parent. `UnconfigureInterface` /
`RemoveService` / `RemoveBGPPeer` must first remove all sub-resources
(I5 — `deleteIntent` refuses if children exist), then delete the interface
intent.

Sub-resource operations that can run before `ConfigureInterface` or
`ApplyService` use `ensureInterfaceIntent` to lazily create the anchor. This
writes an `OpInterfaceInit` intent — a distinct operation tag that is skipped
during reconstruction (the sub-resource operation's own replay re-invokes
`ensureInterfaceIntent`).

**Tree rendering**: When a parent resource (like `vlan|100`) lists an
interface as a child (membership), the tree display shows the interface as
a leaf — the interface's own children (properties, QoS, etc.) are not shown
under the parent's subtree. They belong to the interface's subtree (§12.3.1).

---

### 10.12 `macvpn|VLANID`

Node-level MAC-VPN binding (maps VLAN to VNI via VXLAN tunnel). Creates
VXLAN_TUNNEL_MAP and SUPPRESS_VLAN_NEIGH entries.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | bind-macvpn | `Node.BindMACVPN` | evpn_ops.go:131 |
| Read | unbind-macvpn | `Node.UnbindMACVPN` | evpn_ops.go:160 |
| Delete | unbind-macvpn | `Node.UnbindMACVPN` | evpn_ops.go:171 |

**Parents**: `[vlan|ID]`

---

### 10.13 `irb|VLANID`

IRB (Integrated Routing and Bridging) configuration on a VLAN. Creates
VLAN_INTERFACE IP entries and SAG_GLOBAL anycast MAC.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | configure-irb | `ConfigureIRB` | vlan_ops.go:233 |
| Read | unconfigure-irb | `UnconfigureIRB` | vlan_ops.go:253 |
| Delete | unconfigure-irb | `UnconfigureIRB` | vlan_ops.go:286 |

**Parents**: `[vlan|ID]` always; `[vlan|ID, vrf|NAME]` when VRF is specified.
`ConfigureIRB` creates VLAN_INTERFACE entries with a `vrf_name` binding — the
IRB depends on the VRF existing. When no VRF is specified, the IRB is a
pure L2 SVI and depends only on the VLAN.

---

### 10.14 `ipvpn|VRFNAME`

IP-VPN binding on a VRF. Creates L3VNI mapping, transit VLAN, and route targets.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | bind-ipvpn | `BindIPVPN` | vrf_ops.go:245 |
| Read | unbind-ipvpn | `unbindIpvpnConfig` | vrf_ops.go:266 |
| Read | unbind-ipvpn | `UnbindIPVPN` | vrf_ops.go:313 |
| Delete | unbind-ipvpn | `UnbindIPVPN` | vrf_ops.go:340 |

**Parents**: `[vrf|NAME]`

---

### 10.15 `route|VRF|PREFIX`

Static route in a VRF. Leaf intent — no children.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | add-static-route | `AddStaticRoute` | vrf_ops.go:369 |
| Delete | remove-static-route | `RemoveStaticRoute` | vrf_ops.go:390 |

**Parents**: `[vrf|NAME]`

---

### 10.16 `acl|NAME|RULE`

ACL rule. Each rule has its own intent record as a child of the ACL table.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | add-acl-rule | `AddACLRule` | acl_ops.go |
| Delete | delete-acl-rule | `DeleteACLRule` | acl_ops.go |

**Parents**: `[acl|NAME]`

---

### 10.17 `portchannel|NAME|MEMBER`

PortChannel member. Each member has its own intent record as a child of the
PortChannel container. The kind prefix matches the parent container's kind.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | add-pc-member | `AddPortChannelMember` | portchannel_ops.go |
| Delete | remove-pc-member | `RemovePortChannelMember` | portchannel_ops.go |

**Parents**: `[portchannel|NAME]`

---

### 10.18 `interface|INTF|bgp-peer`

BGP peer binding on an interface. Stores neighbor IP and remote AS for
self-sufficient teardown. `AddBGPPeer` calls `ensureInterfaceIntent` to
lazily create the anchor (§10.11.1) before writing this sub-resource intent.

| Action | Operation | Function | File |
|--------|-----------|----------|------|
| Create | add-bgp-peer | `AddBGPPeer` | interface_bgp_ops.go |
| Read | remove-bgp-peer | `RemoveBGPPeer` | interface_bgp_ops.go |
| Delete | remove-bgp-peer | `RemoveBGPPeer` | interface_bgp_ops.go |

**Parents**: `[interface|INTF]`

---

### 10.19 Summary Table

All 18 intent resource keys at a glance:

| # | Resource Key | Parents | Create | Delete |
|---|---|---|---|---|
| 1 | `device` | `[]` | SetupDevice | (reprovision) |
| 2 | `vlan\|ID` | `[device]` | CreateVLAN | DeleteVLAN |
| 3 | `vrf\|NAME` | `[device]` | CreateVRF | DeleteVRF |
| 4 | `acl\|NAME` | `[device]` | CreateACL, ApplyService | DeleteACL, removeSharedACL |
| 5 | `portchannel\|NAME` | `[device]` | CreatePortChannel | DeletePortChannel |
| 6 | `evpn-peer\|ADDR` | `[device]` | AddBGPEVPNPeer | RemoveBGPEVPNPeer |
| 7 | `interface\|INTF` | varies | ConfigureInterface, ApplyService, ensureInterfaceIntent | UnconfigureInterface, RemoveService |
| 8 | `interface\|INTF\|acl\|DIR` | `[interface\|INTF, acl\|NAME]` | BindACL | UnbindACL |
| 9 | `interface\|INTF\|qos` | `[interface\|INTF]` | ApplyQoS | RemoveQoS |
| 10 | `interface\|INTF\|macvpn` | `[interface\|INTF]` | Interface.BindMACVPN | Interface.UnbindMACVPN |
| 11 | `interface\|INTF\|PROPERTY` | `[interface\|INTF]` | SetProperty | SetProperty (self-reverse) |
| 12 | `macvpn\|VLANID` | `[vlan\|ID]` | Node.BindMACVPN | Node.UnbindMACVPN |
| 13 | `irb\|VLANID` | `[vlan\|ID]` or `[vlan\|ID, vrf\|NAME]` | ConfigureIRB | UnconfigureIRB |
| 14 | `ipvpn\|VRFNAME` | `[vrf\|NAME]` | BindIPVPN | UnbindIPVPN |
| 15 | `route\|VRF\|PREFIX` | `[vrf\|NAME]` | AddStaticRoute | RemoveStaticRoute |
| 16 | `acl\|NAME\|RULE` | `[acl\|NAME]` | AddACLRule | DeleteACLRule |
| 17 | `portchannel\|NAME\|MEMBER` | `[portchannel\|NAME]` | AddPortChannelMember | RemovePortChannelMember |
| 18 | `interface\|INTF\|bgp-peer` | `[interface\|INTF]` | AddBGPPeer | RemoveBGPPeer |

### 10.20 Visual DAG

```
                               ┌──────────┐
                               │  device  │
                               └─┬──┬──┬──┘
                   ┌─────────────┤  │  ├─────────────────┐
                   │             │  │  │                  │
                   ▼             ▼  │  ▼                  ▼
              ┌──────────┐  ┌──────┴───┐  ┌──────────┐  ┌────────────────┐
              │ vlan|100 │  │vrf|CUST  │  │acl|EDGE  │  │portchannel|PC1 │
              └─┬──┬──┬──┘  └──┬──┬───┘  └──┬──┬────┘  └──┬──┬─────────┘
                │  │  │        │  │         │  │           │  │
                ▼  ▼  ▼        ▼  ▼         ▼  ▼           ▼  ▼
         interface macvpn irb interface ipvpn acl|  ←──┐  portchannel| portchannel|
         |Eth0 *  |100  |100 |Eth1 * |CUST  EDGE|     │  PC1|Eth8    PC1|Eth12
           │                               RULE  ─────┘
           ├─ interface|Eth0|qos          _10
           ├─ interface|Eth0|acl|ingress  acl|EDGE|
           ├─ interface|Eth0|bgp-peer     RULE_20
           └─ interface|Eth0|mtu
                                route|
                                CUST|        evpn-peer|10.0.0.2
                                10/8

              * = interface|INTF key is shared by configure-interface
                  and apply-service; add-bgp-peer creates at
                  interface|INTF|bgp-peer (sub-resource, §10.18)

              Interface sub-resources (qos, acl binding, bgp-peer,
              macvpn, property) are children of their interface intent
              (§10.11.1).

              interface|Eth0|acl|ingress has TWO parents:
              interface|Eth0 (the interface) AND acl|EDGE (the ACL table).
              Tree display shows it under interface|Eth0 (same kind);
              under acl|EDGE it appears as a leaf (§12.3.1).

              irb|100 may have TWO parents: vlan|100 AND vrf|CUST
              (when VRF is specified — §10.13). Shown under vlan|100
              only for visual simplicity.

              Every key starts with its kind: device, interface, vlan, vrf,
              acl, portchannel, macvpn, irb, ipvpn, route, evpn-peer
```

### 10.21 Multi-Parent Example

A service application depends on both VLAN and VRF infrastructure:

```
device
├── vlan|100
│       ↑
│       └──── interface|Ethernet0 (apply-service) ─┐
│                                                    │
└── vrf|CUSTOMER ────────────────────────────────────┘
         ↑
    (_parents: "vlan|100,vrf|CUSTOMER")
```

Both parents must exist before the service intent can be created. Neither
parent can be deleted while the service intent exists. RemoveService deletes
the service intent, which deregisters from both parents. If VLAN 100 and VRF
CUSTOMER have no other children, they can then be deleted.

### 10.22 Single-Root Discoverability

The entire intent tree is reachable from `device`:

```go
func WalkIntents(configDB *sonic.ConfigDB, visit func(resource string, intent *sonic.Intent)) {
    var walk func(resource string)
    walk = func(resource string) {
        fields := configDB.NewtronIntent[resource]
        if fields == nil { return }
        intent := sonic.NewIntent(resource, fields)
        visit(resource, intent)
        for _, child := range parseCSV(fields["_children"]) {
            walk(child)
        }
    }
    walk("device")
}
```

This enables:
- **Full device intent dump**: walk from `device`, print each record
- **Dependency analysis**: for any resource, walk parents to root
- **Orphan detection**: any intent NOT reachable from `device` is orphaned
- **DAG visualization**: walk produces a tree suitable for display

### 10.23 Completeness Verification

Every `writeIntent` call site in the codebase must appear in §10.1–§10.18.
Every `deleteIntent` call site must appear as a Delete action. Verify with:

```
grep -n 'writeIntent\|deleteIntent' pkg/newtron/network/node/*_ops.go
```

If a call site is not in this catalog, it is either a bug or a missing entry.

## 11. Health Checks

### 11.1 Bidirectional Consistency (I2)

For each intent record:
- For each P in `_parents`: verify P exists and P.`_children` contains self
- For each C in `_children`: verify C exists and C.`_parents` contains self

Violation indicates a partial write (crash between ChangeSet entries) or a
bug. Repair: add missing back-references, or remove stale forward-references.

### 11.2 Referential Integrity (I3)

For each intent record:
- For each P in `_parents`: verify P exists as an intent record
- For each C in `_children`: verify C exists as an intent record

Orphaned references (pointing to non-existent intents) indicate a child was
deleted without proper deregistration. Repair: remove the stale reference
from the parent's `_children`.

### 11.3 Orphan Detection

Find intent records with `_parents` listing a resource that doesn't exist.
These are "orphaned children" — they claim a parent that was deleted without
proper enforcement of I5. Repair depends on context: the child may need to
be deleted, or the parent may need to be recreated.

### 11.4 Health Check Implementation

A `ValidateIntentDAG(configDB)` function iterates all NEWTRON_INTENT records,
checks all three conditions above, and returns a list of violations. This
runs as part of device health checks and can be invoked manually.

## 12. CLI: `newtron intent tree`

### 12.1 Synopsis

```
newtron intent tree <device> [<resource-kind>[:<resource>]]
```

Displays the intent DAG as a tree, rooted at `device` or scoped to a
specific resource kind or resource.

### 12.2 Forms

**Full tree** — walk from `device`, print entire DAG:
```
$ newtron intent tree switch1
device (setup-device)
├── vlan|100 (create-vlan)
│   ├── interface|Ethernet0 (configure-interface) vlan_id=100 tagged=false
│   ├── macvpn|100 (bind-macvpn) vni=20100
│   └── irb|100 (configure-irb) vrf=CUSTOMER ip_address=10.10.100.1/24
├── vrf|CUSTOMER (create-vrf)
│   ├── interface|Ethernet4 (configure-interface) vrf=CUSTOMER ip=10.0.1.0/31
│   ├── ipvpn|CUSTOMER (bind-ipvpn) l3vni=1001 l3vni_vlan=1001
│   └── route|CUSTOMER|10.0.0.0/8 (add-static-route) next_hop=10.0.1.1
├── acl|EDGE_IN (create-acl)
│   ├── acl|EDGE_IN|RULE_10 (add-acl-rule)
│   ├── acl|EDGE_IN|RULE_20 (add-acl-rule)
│   └── interface|Ethernet0|acl|ingress (bind-acl) acl_name=EDGE_IN
└── evpn-peer|10.0.0.2 (add-bgp-evpn-peer) asn=65002
```

**Filter by resource kind** — show only resources matching the kind prefix
and their subtrees:
```
$ newtron intent tree switch1 vlan
vlan|100 (create-vlan)
├── interface|Ethernet0 (configure-interface) vlan_id=100 tagged=false
├── macvpn|100 (bind-macvpn) vni=20100
└── irb|100 (configure-irb) vrf=CUSTOMER ip_address=10.10.100.1/24

vlan|200 (create-vlan)
└── interface|Ethernet8 (configure-interface) vlan_id=200 tagged=true
```

**Filter by specific resource** — show one resource and its subtree:
```
$ newtron intent tree switch1 vlan:100
vlan|100 (create-vlan)
├── interface|Ethernet0 (configure-interface) vlan_id=100 tagged=false
├── macvpn|100 (bind-macvpn) vni=20100
└── irb|100 (configure-irb) vrf=CUSTOMER ip_address=10.10.100.1/24
```

**Filter by interface** — show an interface and its children:
```
$ newtron intent tree switch1 interface:Ethernet0
interface|Ethernet0 (configure-interface) vlan_id=100 tagged=false
├── interface|Ethernet0|qos (apply-qos) policy=STRICT_PRIORITY
├── interface|Ethernet0|acl|ingress (bind-acl) acl_name=EDGE_IN
└── interface|Ethernet0|mtu (set-port-property) mtu=9100
```

**Ancestors mode** — show the path from a resource to the root:
```
$ newtron intent tree switch1 vlan:100 --ancestors
device (setup-device)
└── vlan|100 (create-vlan)
    ├── interface|Ethernet0 (configure-interface) vlan_id=100 tagged=false
    ├── macvpn|100 (bind-macvpn) vni=20100
    └── irb|100 (configure-irb) vrf=CUSTOMER ip_address=10.10.100.1/24
```

### 12.3 Output Format

Each line contains:

```
<tree-prefix> <resource-key> (<operation>) [<key>=<value> ...]
```

- **tree-prefix**: `├──`, `└──`, `│` connectors (Unicode box-drawing)
- **resource-key**: the NEWTRON_INTENT key (e.g., `vlan|100`, `interface|Ethernet0`)
- **operation**: the `operation` field from the intent record
- **params**: selected domain params displayed inline (omit `_parents`,
  `_children`, `state`, `operation`, lifecycle fields)

### 12.3.1 Multi-Parent Rendering

A DAG node with multiple parents appears as a child under each parent in a
full tree walk. To avoid redundant subtree expansion, the tree renderer
applies this rule:

**A child that has a different kind than its parent is rendered as a leaf
(no recursion into its children).** The child's own children are only shown
when the child is the root of the displayed subtree.

Example: `interface|Ethernet0` is a child of `vlan|100` (membership). When
displaying `vlan|100`'s subtree, the interface appears as a leaf — its
children (qos, acl binding, mtu) are not shown because they belong to the
interface's story, not the VLAN's. To see the interface's children, query
the interface directly: `newtron intent tree switch1 interface:Ethernet0`.

Similarly, `interface|Ethernet0|acl|ingress` is a child of both
`interface|Ethernet0` and `acl|EDGE_IN`. It appears as a leaf under
`acl|EDGE_IN` but with its full subtree under `interface|Ethernet0`.

### 12.4 Resource Kind Mapping

The `<resource-kind>` argument maps to resource key prefixes:

| Kind | Prefix match | Example |
|---|---|---|
| `device` | `device` | `device` |
| `vlan` | `vlan\|` | `vlan\|100`, `vlan\|200` |
| `vrf` | `vrf\|` | `vrf\|CUSTOMER` |
| `acl` | `acl\|` | `acl\|EDGE_IN`, `acl\|EDGE_IN\|RULE_10` |
| `portchannel` | `portchannel\|` | `portchannel\|PC1`, `portchannel\|PC1\|Ethernet8` |
| `evpn-peer` | `evpn-peer\|` | `evpn-peer\|10.0.0.2` |
| `interface` | `interface\|` | `interface\|Ethernet0`, `interface\|Ethernet0\|qos` |
| `ipvpn` | `ipvpn\|` | `ipvpn\|CUSTOMER` |
| `macvpn` | `macvpn\|` | `macvpn\|100` |
| `irb` | `irb\|` | `irb\|100` |
| `route` | `route\|` | `route\|CUSTOMER\|10.0.0.0/8` |

When `<resource-kind>:<resource>` is given, the resource key is constructed
as `<kind>\|<resource>` (e.g., `vlan:100` → `vlan\|100`).

### 12.5 Implementation

The tree walk is a depth-first traversal of `_children`, with cycle
detection as a safety net (should never trigger given I1, but guards
against corrupted data):

```go
func printIntentTree(configDB *sonic.ConfigDB, resource string, prefix string, last bool, visited map[string]bool) {
    if visited[resource] { return }  // cycle guard
    visited[resource] = true

    fields := configDB.NewtronIntent[resource]
    intent := sonic.NewIntent(resource, fields)

    connector := "├── "
    if last { connector = "└── " }
    fmt.Printf("%s%s%s (%s) %s\n", prefix, connector, resource, intent.Operation, formatParams(intent.Params))

    childPrefix := prefix + "│   "
    if last { childPrefix = prefix + "    " }

    // Multi-parent rendering (§12.3.1): children with a different kind than
    // this resource are rendered as leaves — no recursion into their subtrees.
    myKind := intentKind(resource)
    children := parseCSV(fields["_children"])
    sort.Strings(children)  // deterministic output
    for i, child := range children {
        childKind := intentKind(child)
        if childKind != myKind {
            // Different kind → leaf only (membership relationship)
            printIntentLeaf(configDB, child, childPrefix, i == len(children)-1)
        } else {
            printIntentTree(configDB, child, childPrefix, i == len(children)-1, visited)
        }
    }
}

// intentKind extracts the kind prefix from a resource key.
// "interface|Ethernet0|qos" → "interface", "device" → "device"
func intentKind(resource string) string {
    parts := strings.SplitN(resource, "|", 2)
    return parts[0]
}
```

### 12.6 API Endpoint

```
GET /node/{device}/intent/tree[?kind=vlan&resource=100&ancestors=true]
```

Returns the same tree structure as JSON for programmatic access:

```json
{
  "resource": "vlan|100",
  "operation": "create-vlan",
  "params": {"vlan_id": "100"},
  "children": [
    {
      "resource": "interface|Ethernet0",
      "operation": "configure-interface",
      "params": {"vlan_id": "100", "tagged": "false"},
      "children": []
    }
  ]
}
```

## 13. Reconstruction

### 13.1 Replay Uses the DAG

`IntentsToSteps` uses Kahn's algorithm for topological sorting:

1. Parse all intent records, build adjacency list from `_parents`/`_children`
2. Topological sort (Kahn's algorithm)
3. Convert each intent to a step in topological order
4. Replay against abstract Node

Each replayed operation calls `writeIntent` with appropriate parents, which
re-establishes the DAG in the abstract Node's shadow configDB.

### 13.2 `_parents` and `_children` in Topology Steps

`intentParamsToStepParams` does NOT export `_parents` or `_children` to
topology step parameters. These are DAG metadata maintained by `intent_ops.go`,
not operation arguments.

During replay, each operation derives its parents from its own arguments:
`ConfigureInterface(VLAN=100)` knows to declare parent `vlan|100`. The
parent relationship is a function of the operation's semantics, not a stored
parameter that needs to be passed through.

### 13.3 `NewIntent` Handling

`NewIntent` (which parses flat CONFIG_DB fields into an Intent struct) extracts
`_parents` and `_children` into dedicated fields on the Intent struct, just
as it extracts `operation`, `state`, and `name`. These fields are added to
`intentIdentityFields` so they are excluded from `Params`.

## 14. Schema

`_parents` and `_children` are added to the NEWTRON_INTENT table in
`schema.go`:

```go
"_parents":  {Type: FieldString, AllowEmpty: true},  // CSV of parent resource keys
"_children": {Type: FieldString, AllowEmpty: true},  // CSV of child resource keys
```

No YANG model exists for NEWTRON_INTENT (it is newtron-specific). Constraints
are derived from newtron usage patterns, consistent with the existing schema
entry comment.

## 15. Atomicity

### 15.1 Within a ChangeSet

`writeIntent` and `deleteIntent` add multiple entries to a single ChangeSet:
the child's own intent entry plus updates to each parent's `_children`. In
physical mode, the ChangeSet is applied to Redis as a pipeline — all entries
are sent together. In abstract mode, entries are applied to the shadow
sequentially.

### 15.2 Partial Pipeline Delivery

Within a single ChangeSet, `writeIntent` adds multiple entries to the Redis
pipeline: updates to each parent's `_children` plus the child's own intent
record. If the process crashes mid-pipeline (e.g., parent `_children` updated
but child intent not yet written, or vice versa), I2 may be temporarily
violated. The health check (§11) detects and repairs this.

Between ChangeSets in a composite operation (e.g., `ApplyService` creating
infrastructure then service intent), a crash can leave the first ChangeSet
applied and the second unapplied. This is an existing concern with
multi-ChangeSet operations, not specific to the DAG — the health check
handles both cases.

### 15.3 Concurrent Access

newtron uses lock-based concurrency control (Lock/Unlock on the Node). All
operations on a Node are serialized while the lock is held. Cross-node
operations do not share intent records (intent records are per-device). No
concurrent-write conflicts arise.

## 16. Architecture Characteristics

| Aspect | Mechanism |
|---|---|
| Graph structure | Single-rooted DAG (`device` is sole root) |
| Relationship tracking | `_parents`/`_children` fields on every intent record |
| Reference counting | Read `_children` field emptiness |
| Reconstruction ordering | Topological sort of DAG (Kahn's algorithm) |
| Prerequisite checks | `writeIntent` I4 enforcement (parent must exist) |
| Destroy functions | Delete own entry only; refuse if children exist (I5) |
| Sub-resource tracking | Own intent record per sub-resource |
| Baseline teardown | `deleteIntent("device")` refuses if any children exist |
| Reprovision | CompositeOverwrite bypasses DAG, rebuilds it atomically |
| Discoverability | Walk from `device` — reachable = valid, unreachable = orphan |
| `writeIntent` on existing key | Idempotent update if same parents; error if different parents |
| `deleteIntent` behavior | Refuse if `_children` non-empty; deregister from parents |
| `updateIntent` | Eliminated (§4.3) |

## 17. Unified Pipeline Integration

The intent DAG integrates with the abstract Node's unified pipeline — the
same code path serves both physical (online) and abstract (offline) modes.

### 17.1 Same Code Path, Different Initialization

`op()` and `applyShadow()` are unified across both modes. In physical mode,
`op()` applies the ChangeSet to Redis; in abstract mode, it updates the
shadow configDB. The DAG operations (`writeIntent`, `deleteIntent`) work
identically in both modes — they read parents from configDB (real or shadow),
update `_children`, and write the intent record.

### 17.2 ExportEntries Replaces Accumulated

`ExportEntries()` replaces the former `accumulated` slice. The shadow configDB
IS the state — `ExportEntries()` reads the final configDB state and exports
it for composite delivery. Intent records (including DAG metadata) are part
of this export.

### 17.3 Intent-Idempotent Primitives

**Shared infrastructure primitives are intent-idempotent**: if the intent
exists, the resource is managed — return an empty ChangeSet. This is a
2-line guard at the top of each shared infrastructure primitive:

```go
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, ...) (*ChangeSet, error) {
    if n.hasIntent("vlan|" + strconv.Itoa(vlanID)) {
        return NewChangeSet(n.Name(), "create-vlan"), nil  // already managed
    }
    // ... proceed with creation
}
```

Shared infrastructure primitives that use this guard:
- `CreateVLAN`, `CreateVRF`, `BindIPVPN`, `BindMACVPN` (Node-level)
- `CreatePortChannel`, `CreateACL`, `ConfigureIRB`

Per-entity primitives (`ConfigureInterface`, `AddBGPPeer`, `BindACL`) do not
need the guard — mutual exclusivity or per-entity uniqueness prevents double
creation.

### 17.4 No cascadeDeleteChildren

`cascadeDeleteChildren` was eliminated. I5 enforcement (`deleteIntent` fails
if `_children` is non-empty) is the sole mechanism for ensuring correct
teardown ordering. Callers handle the error by removing children explicitly
in the correct order, using each child's own reverse domain method.

## 18. Design Principles

Three principles fall out of this architecture.

### 18.1 Intent DAG — Structural Dependencies Replace Scanning

A VLAN has members. An ACL has rules. A VRF has routes and interfaces. A
PortChannel has bonded ports. These parent-child relationships are knowable
at the moment the child is created — an interface that joins VLAN 100 knows
it depends on VLAN 100. The information exists; the question is where it
lives.

Before the DAG, it lived nowhere persistent. `DeleteVLAN` would scan
CONFIG_DB for VLAN_MEMBER entries. `DependencyChecker` would scan CONFIG_DB
tables to count remaining consumers of a VRF or ACL. Reconstruction would
consult a `stepPriority` map to determine operation ordering. Each mechanism
reimplemented the same structural knowledge — which resources depend on
which — in a different form, in a different file, with different failure
modes.

The intent DAG makes these relationships first-class. Every NEWTRON_INTENT
record carries two fields: `_parents` (what I depend on) and `_children`
(what depends on me). When an interface joins VLAN 100, its `writeIntent`
call declares `vlan|100` as a parent. The VLAN's `_children` field grows to
include the interface — automatically, as a side effect of `writeIntent`, not
through a separate tracking mechanism. When the interface leaves,
`deleteIntent` removes it from the VLAN's `_children`. The VLAN never
participates in either decision.

From these two fields, three capabilities follow mechanically:

**Creation ordering** is enforced by I4: `writeIntent` refuses if a declared
parent does not exist. An interface cannot join a VLAN that hasn't been
created. A QoS binding cannot be applied to an interface that has no intent.
No precondition check code is needed — the invariant is checked once, in
`writeIntent`, for every resource type.

**Deletion ordering** is enforced by I5: `deleteIntent` refuses if
`_children` is non-empty. A VLAN cannot be deleted while interfaces are
members. An ACL cannot be deleted while rules or bindings exist. No
`DependencyChecker` scan is needed — the answer is in the record.

**Reconstruction ordering** is a topological sort of the DAG. Parents before
children, deterministic within each level. No manually maintained priority
map is needed. When a new operation type is added, its reconstruction order
is determined by its parents, not by a number someone remembers to update.

Eight invariants govern the DAG (§3). The critical design decision is that
**children declare parents; parents are passive.** A child's `writeIntent`
registers with parents. A child's `deleteIntent` deregisters from parents.
Parents never write to children. This makes relationship maintenance
unidirectional and eliminates the coordination problem of bidirectional
updates across independent operations.

The DAG subsumes three ad-hoc mechanisms:

| Replaced mechanism | Replacement |
|---|---|
| `DependencyChecker` CONFIG_DB scanning | Read `_children` emptiness |
| `stepPriority` manual ordering | Topological sort of DAG |
| Dual-tracking CSV fields on parent intents | Per-child intent records with `_parents` links |

Dual-tracking — where child operations called `updateIntent` on the parent
to maintain CSV fields like `members` and `rules` — was eliminated. Each
child (ACL rule, PortChannel member, VLAN member) has its own intent record.
The parent's `_children` field replaces the CSV. The parent's destroy function
is "refuse if children exist" — children are removed first, by their own
domain reverse methods.

**Cascade deletes do not exist.** `deleteIntent` on a parent with children
fails. The caller must remove children explicitly, in the correct order,
using each child's own reverse method. This is intentional: cascade deletes
would require the parent to know how to tear down each child type, violating
single responsibility and creating a second code path for every teardown
operation.

**CompositeOverwrite bypasses the DAG.** Reprovision replaces the entire
CONFIG_DB atomically — including all intent records. It does not call
`deleteIntent` or check `_children`. The abstract Node that builds the
composite runs operations in order, accumulating fresh intent records with
correct DAG links. This is consistent with CLAUDE.md: provisioning is the
one operation where intent replaces reality.

### 18.2 Kind-Prefixed Intent Keys

NEWTRON_INTENT is a single flat Redis hash namespace. CONFIG_DB tables carry
their type in the table name — `VLAN`, `VRF`, `ACL_TABLE` — but intent keys
share one table. The question every piece of code that processes intent keys
must answer: what kind of resource does this key represent?

Without a convention, the answer requires heuristics. Does `Ethernet0|qos`
start with a kind prefix or an interface name? Is `CUSTOMER` a VRF name or
an ACL name? Code that parses intent keys devolves into
`if looksLikeInterfaceName(key)` branches — each one a special case that
invites more special cases.

The kind prefix eliminates the question. Every intent key follows
`kind|identity`:

```
device                        kind=device  (singleton)
vlan|100                      kind=vlan    identity=100
interface|Ethernet0           kind=interface  identity=Ethernet0
interface|Ethernet0|qos       kind=interface  identity=Ethernet0|qos
acl|EDGE_IN|RULE_10           kind=acl     identity=EDGE_IN|RULE_10
```

The kind is always the first segment before the first `|`.
`strings.SplitN(key, "|", 2)` extracts `[kind, rest]` for every key in the
table. No regex, no name-detection heuristics, no special cases.

This enables kind-based filtering (`newtron intent tree switch1 vlan`),
self-describing keys (reading `interface|Ethernet0|qos` tells you what it is
without external knowledge), and uniform parsing across tree walks, health
checks, reconstruction, and orphan detection.

The convention is consistent with the Unified Naming Convention (CLAUDE.md):
CONFIG_DB tables carry the type in the table name, so keys don't repeat it.
NEWTRON_INTENT has one table for all types — therefore the key must carry the
type.

**Every intent key starts with its kind. No exceptions.**

### 18.3 Interface Intent as Anchor

An interface that the intent system doesn't know about has no business
hosting sub-resources that the intent system tracks. A QoS binding on an
unmanaged port, an ACL on an interface with no intent record, a property
change that has no parent to deregister from — these are orphans from
creation, invisible to dependency enforcement, unremovable by any reverse
operation that walks the DAG.

The rule: **sub-resource intents require an interface intent as parent.** The
interface intent (`interface|INTF`) is created by `ConfigureInterface`,
`ApplyService`, or `AddBGPPeer` — whichever operation first gives the
interface a role. Sub-resource operations (`ApplyQoS`, `BindACL`,
`SetProperty`, `Interface.BindMACVPN`) declare `interface|INTF` as parent.
If the interface has no intent, the sub-resource's `writeIntent` fails (I4).

This creates a two-level tree per interface:

```
interface|Ethernet0 (configure-interface)
├── interface|Ethernet0|qos (apply-qos)
├── interface|Ethernet0|acl|ingress (bind-acl)
└── interface|Ethernet0|mtu (set-port-property)
```

Teardown respects the tree: `UnconfigureInterface` must first remove all
sub-resources (the DAG enforces this via I5), then delete the interface
intent. The interface is both the point of service and the anchor of
sub-resource intent.

**Multi-parent rendering.** Some sub-resources have multiple parents. An ACL
binding (`interface|Ethernet0|acl|ingress`) depends on both the interface and
the ACL table — neither can be deleted while the binding exists. When
displaying the DAG as a tree, a child with a different kind than its display
parent is rendered as a leaf — its own children are shown only in its own
subtree. The VLAN's tree shows its member interfaces as leaves; the
interface's tree shows its full sub-resource hierarchy. This prevents
redundant subtree expansion without losing information — query the child
directly to see its full story.
