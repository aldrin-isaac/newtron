# Design Principles Update Plan — Resource-Centric Intent and Interface Forwarding Mode

## Summary

Three insights from the intent coverage gap analysis require updates to
the design principles:

1. **The resource is the unit of intent, not the operation.** §17 line
   1369 states "§19: one operation = one intent record." This is wrong.
   The intent model is resource-centric: one resource = one intent
   record. Multiple operations mutate the same record over the
   resource's lifetime. `CreateVLAN` creates `vlan|100`,
   `AddVLANMember` updates `vlan|100`, `DeleteVLAN` removes `vlan|100`.
   Three operations, one intent record.

2. **`configure-interface` handles both bridged and routed forwarding
   modes.** Joining a VLAN bridge domain (L2) and joining a VRF routing
   domain (L3) are the same operation: binding an interface to a
   forwarding domain. The current design treats them asymmetrically —
   `configure-interface` handles routed (with its own intent), while
   `add-vlan-member` handles bridged (with no intent). This creates the
   VLAN member intent gap. The fix is to unify both under
   `configure-interface`, giving bridged interfaces intent parity with
   routed interfaces. VLAN and VRF both become forwarding domain
   containers that interfaces join.

3. **Right-sizing intent records.** §17's coherence test determines
   which resources are independent intent bearers and which are tracked
   as membership in their parent's intent. The principles need to
   articulate this explicitly, along with the layered interface model
   (forwarding mode vs policy vs service).

## Conflicts to Resolve

### Conflict A: "one operation = one intent record" (§17 line 1369)

**Current text** (§17):
```
This principle connects to:
- §19: one operation = one intent record
```

**Problem**: The intent model is resource-centric. The intent key is the
resource (`vlan|100`, `device`, `Ethernet0`), not the operation. Multiple
operations create, evolve, and delete the same intent record:

- `CreateVLAN` creates `vlan|100` intent
- `AddVLANMember` updates `vlan|100` intent (adds member)
- `RemoveVLANMember` updates `vlan|100` intent (removes member)
- `DeleteVLAN` removes `vlan|100` intent

"One operation = one intent record" would require four separate records
for what is conceptually one resource's lifecycle. That contradicts §23
(bounded footprint — cost proportional to infrastructure, not operations
over time) and §19 itself ("bound to a resource").

**Fix**: Replace the bullet with: "§19: one resource = one intent record"

Also update §19's summary table entry (line 2528) from:
"One record structure for all managed resources — operation, name,
params, state lifecycle"
to clarify the resource-centric keying.

### Conflict B: §15 lists `AddVLANMember`/`RemoveVLANMember` as a symmetric pair

**Current text** (§15 pairs table, line 1166):
```
| `AddVLANMember` | `RemoveVLANMember` |
```

**Problem**: After this redesign, `AddVLANMember`/`RemoveVLANMember`
are no longer public API operations. They become internal methods
called by `ConfigureInterface` in bridged mode. Listing them as a
symmetric pair in the public operations table implies they are
standalone operations the operator invokes directly.

**Fix**: Remove the `AddVLANMember`/`RemoveVLANMember` row from the
pairs table. `ConfigureInterface`/`UnconfigureInterface` (already in the
table) covers both bridged and routed modes.

### Conflict C: §6 mentions "VLAN membership" as per-interface but doesn't connect it to `configure-interface`

**Current text** (§6 line 534-536):
```
VRF binding, VLAN membership, ACL application, QoS scheduling, BGP
peering — all are per-interface.
```

**Problem**: This correctly identifies VLAN membership as per-interface,
but the current implementation contradicts this by handling VLAN
membership as a VLAN-level operation (`add-vlan-member`) rather than an
interface-level operation (`configure-interface`). The principle is
right; the implementation (and other principles that reference the
implementation) need to catch up.

**Fix**: No change needed to §6 — it already says the right thing. The
other principles must be updated to be consistent with §6.

### Conflict D: §30 exception for "container membership" becomes partially obsolete

**Current text** (§30 line 2062-2063):
```
Exception: container membership (VLAN members, PortChannel members)
where the container is the subject.
```

**Problem**: After this redesign, VLAN membership is handled by
`configure-interface` (the interface is the subject). Only PortChannel
membership remains as a case where the container is the subject. ACL
rules also remain container-subject.

**Fix**: Update to: "Exception: container membership (PortChannel
members, ACL rules) where the container is the subject. VLAN
membership is handled by `configure-interface` — the interface is the
subject (§6)."

Note: ACL rules were always implicitly container-subject (you add a
rule to an ACL table, not to an interface). The original text omitted
them because it focused on cross-entity membership. Making ACL rules
explicit improves clarity without changing behavior.

## Changes by Section

### §6 — The Interface Is the Point of Service

**No change to existing text.** §6 already correctly identifies VLAN
membership as per-interface. Add a new paragraph after the list of
per-interface concerns (after line 545) that makes explicit the
forwarding mode concept:

> **Forwarding mode is the interface's fundamental configuration.**
> An interface participates in a forwarding domain — either a bridge
> domain (VLAN, L2) or a routing domain (VRF, L3). Both are the same
> conceptual operation: binding the interface to a forwarding domain.
> Bridged and routed are symmetric — same operation, same intent
> lifecycle, same reverse. An interface that joins a VLAN's bridge
> table and an interface that joins a VRF's routing table are making
> the same commitment: "I forward packets in this domain."
>
> The interface configuration layers naturally:
>
> - **Physical** — port properties (mtu, speed, admin_status). Set
>   once, rarely changed. Baseline infrastructure (§15) — its reverse
>   is reprovision, not individual undo.
> - **Forwarding mode** — which domain the interface joins. Bridge
>   domain or routing domain. One operation, one intent record.
> - **Policy** — ACL filtering, QoS shaping. Independent lifecycle
>   from forwarding mode — changing a QoS policy does not require
>   re-specifying the forwarding domain.
> - **Service** — composite that orchestrates the layers above. The
>   operator's primary tool; the individual layers exist for granular
>   control outside of services.
>
> Each layer above physical passes the coherence test (§17) — it
> produces independently useful state, has its own intent record, and
> has a symmetric reverse (§15). Forwarding mode is prerequisite:
> an interface must be in a forwarding domain before policy can be
> applied. But the layers are not coupled — they change independently,
> at different rates, for different reasons.

### §15 — Symmetric Operations

**Edit the pairs table** (line 1166):

Remove:
```
| `AddVLANMember` | `RemoveVLANMember` |
```

The `ConfigureInterface`/`UnconfigureInterface` pair already covers
both bridged and routed modes. VLAN membership is now handled by
`configure-interface` in bridged mode.

Add after the pairs table (after line 1179), in the baseline operations
note:

> VLAN membership is not a standalone operation — it is a forwarding
> mode decision (§6). Joining a VLAN bridge domain and joining a VRF
> routing domain are the same operation on an interface; the pair
> `ConfigureInterface`/`UnconfigureInterface` covers both.
>
> PortChannel members and ACL rules remain standalone symmetric pairs
> because they are container-subject operations (§30) — the operator
> manages the container's children directly, not through an interface
> forwarding-mode decision.

### §17 — Operation Granularity

**Edit line 1369**:

Change:
```
- §19: one operation = one intent record
```
To:
```
- §19: one resource = one intent record
```

**Add a new subsection** after the existing text (after line 1371,
before the `---` separator) titled "Right-sizing intent records":

> ### Right-sizing intent records
>
> The coherence test determines not only what constitutes an operation
> but what constitutes an independent intent record. If a mutation
> doesn't produce independently useful state, it doesn't get its own
> record — it updates the intent of the resource it belongs to.
>
> The resource is the unit of intent (§19). Operations create, evolve,
> and delete a resource's intent record over its lifetime. The record
> captures the resource's current state — not a log of operations that
> acted on it.
>
> Three categories:
>
> - **Standalone resources** pass the coherence test and have
>   independent lifecycles. VLANs, VRFs, ACLs, PortChannels,
>   interfaces, overlays, static routes, the device baseline. Each
>   gets one intent record.
>
>   IRB is standalone but distinct from the forwarding mode pattern.
>   An IRB creates a routed interface on a bridge domain — it
>   requires both VLAN and VRF to exist, and spans both. It is not
>   an interface choosing bridged or routed; it is the entity that
>   bridges L2 and L3. This is why IRB has its own operation and
>   intent, separate from the interface forwarding mode.
>
> - **Structurally subordinate children** fail the coherence test —
>   they are not independently useful without their container. An ACL
>   rule without its ACL table is a fragment of nothing. A bonded port
>   without its LAG has surrendered its independent forwarding role.
>   These are tracked in the parent's intent, which evolves as
>   children are added and removed.
>
> - **Forwarding domain members** — interfaces that join a VLAN or
>   VRF — pass the coherence test. An interface in a VLAN is
>   independently useful; it forwards packets in that bridge domain.
>   These get their own intent record. The container doesn't need to
>   know its members because the members know which container they
>   joined.
>
> The distinction between the last two: is the relationship a
> forwarding decision or a structural subordination? A forwarding
> decision is an interface-level choice — the interface retains its
> identity and chooses which domain to participate in. The interface
> is the subject; the container is context. Structural subordination
> is the opposite — the child surrenders its identity to become a
> component of the parent. The container is the subject; the child
> is the component.

### §19 — Unified Intent Model

**Add a paragraph** after "bound to a resource" (after line 1510):

> The resource is the unit of intent. The intent key is the resource
> — not the operation that created it. Multiple operations act on the
> same resource over its lifetime: creation writes the intent, deletion
> removes it, mutations in between evolve it. For containers with
> structurally subordinate children (§17), child mutations update the
> parent's intent — an ACL rule added to a table evolves the table's
> record. Forwarding domain members (§6, §17) carry their own intent
> on the interface — the container doesn't track them. The intent
> record captures the resource's current state — what should exist
> now — not a journal of operations. This keeps intent O(resources)
> per device (§23), not O(operations over time).

**Update the summary table entry** (line 2528):

Change:
```
| 19 | Unified intent model | One record structure for all managed resources — operation, name, params, state lifecycle; the Node intermediates all intent | C |
```
To:
```
| 19 | Unified intent model | One record per managed resource — keyed by resource, evolved by operations, carrying params for teardown and reconstruction; the Node intermediates all intent | C |
```

### §20 — On-Device Intent Is Sufficient for Reconstruction

**Add after the final paragraph** (after line 1590, before `---`):

> **Right-sizing intent for reconstruction.** An intent record must
> contain everything needed to reproduce its resource's CONFIG_DB
> entries. For simple resources, the creation parameters suffice. For
> containers with structurally subordinate children (§17) — ACL rules,
> PortChannel members — the intent must include the current membership
> state. Without it, reconstruction produces an empty container and the
> diff shows false drift on a correctly configured device.
>
> Forwarding domain membership is the opposite case. VLAN members and
> VRF interfaces carry their own intent (§6, §17), so the container's
> intent stays simple — just the container's own properties. Members
> are reconstructed from their own intents, not from the container's.

### §22 — Dual-Purpose Intent

**Add a note** after the two existing categories (after line 1698):

> **Container membership fields** — ACL rules, PortChannel members —
> are user params. The operator chose them explicitly; they are not
> derived from spec resolution. Snapshot emits them because there is
> no spec to re-derive them from. Teardown reads them for
> reconstruction but also scans CONFIG_DB for actual members (§5:
> device is reality).

### §23 — Bounded Device Footprint

**No text change needed.** The existing text at line 1732 already says:
"The intent record is O(resources) per device — one per managed resource
(interface, VRF, overlay)."

Container membership tracking (ACL rules, PortChannel members in parent
intent) keeps the record count O(resources). The record size grows with
the number of members, not with the number of operations — proportional
to infrastructure, consistent with §23.

### §30 — Respect Abstraction Boundaries

**Edit line 2062-2063**:

Change:
```
Exception: container membership (VLAN members, PortChannel members)
where the container is the subject.
```
To:
```
Exception: container membership (PortChannel members, ACL rules) where
the container is the subject. VLAN membership is handled by
`configure-interface` — the interface is the subject (§6).
```

## Sections NOT Changed

| Section | Why unchanged |
|---------|---------------|
| §1 | Abstract node description unaffected |
| §2-4 | Code path, enforcement, SONiC-as-database unaffected |
| §5 | Already correct ("device is reality") |
| §7-10 | Scope, opinion, delivery unaffected |
| §11-14 | ChangeSet, dry-run, schema, verification unaffected |
| §16 | Verb vocabulary unaffected (`configure-*` already in table) |
| §18 | Write ordering unaffected |
| §21 | Reconstruction approach unaffected |
| §24-29 | Shared objects, hashing, peer groups, single-owner, cohesion, pure functions unaffected |
| §31-42 | Isolation, naming, API, transport, import, normalize, platform, observe, DRY, greenfield, multi-version, testing unaffected |

## Cross-Reference Verification

After all changes, verify these cross-references hold:

1. §17 → §19: "one resource = one intent record" (updated)
2. §17 → §15: "operation boundary defines what has a symmetric reverse" (unchanged, still true)
3. §17 → §11: "one operation = one ChangeSet" (unchanged, still true — each operation produces one ChangeSet; the intent record may be updated by multiple ChangeSets over time, but each operation still produces exactly one)
4. §19 references §23: O(resources) not O(operations) (consistent)
5. §20 references §15: intent sufficient for reconstruction (consistent — container intents include members for reconstruction; teardown still scans ConfigDB per §5)
6. §22 references §20 and §21: user params for snapshot, resolved params for teardown (consistent — container members are user params, emitted by Snapshot because they are explicit operator decisions with no spec to re-derive from)
7. §6 mentions VLAN membership as per-interface (consistent with configure-interface handling bridged mode)
8. §15 pairs table has ConfigureInterface/UnconfigureInterface covering both modes (consistent)
9. §30 exception updated to exclude VLAN members (consistent with §6)

## Code Changes Required

This plan updates design principles only. The principles describe the
target architecture. The following code changes are required to align
the implementation with these principles — they are tracked separately
in `intent-gaps-plan.md` and not part of this document's scope:

1. **`interface_ops.go`**: Extend `ConfigureInterface` to support
   bridged mode (VLAN membership). Currently only handles routed mode
   (VRF + IP). The `InterfaceConfig` struct needs a bridged-mode
   variant (VLAN ID, tagging mode). In bridged mode,
   `ConfigureInterface` calls `AddVLANMember` (internal method in
   `vlan_ops.go` — single-owner of VLAN_MEMBER table) and writes an
   interface-level intent record. `UnconfigureInterface` handles the
   reverse for both modes. `interface_ops.go` never constructs
   VLAN_MEMBER entries — it delegates to `vlan_ops.go`.

2. **`vlan_ops.go`**: `AddVLANMember`/`RemoveVLANMember` remain as
   internal methods (single-owner of VLAN_MEMBER table writes). They
   are no longer public API operations — no HTTP endpoint, no CLI
   command, no client method. They are called by `ConfigureInterface`
   in bridged mode. Intent is written by `ConfigureInterface`, not by
   `AddVLANMember`.

3. **`handler.go` / API routes + public API wrappers + CLI**: Remove
   `add-vlan-member` and `remove-vlan-member` public endpoints from
   `handler.go`, the corresponding public API methods from
   `pkg/newtron/node.go` and `pkg/newtron/client/node.go`, and the
   CLI subcommands from `cmd/newtron/cmd_vlan.go`. The internal
   methods in `vlan_ops.go` remain.

4. **`reconstruct.go`**: `ReplayStep` for `configure-interface` must
   handle both bridged and routed mode parameters. In bridged mode,
   replay calls `ConfigureInterface` which calls `AddVLANMember`
   internally — same code path as the forward operation. **Important**:
   this replaces the intent-gaps-plan Gap 3a's approach of replaying
   VLAN members inside the `create-vlan` ReplayStep case. VLAN member
   replay happens via `configure-interface`, not via `create-vlan`.
   The `create-vlan` ReplayStep case remains simple (creates the VLAN
   container only, no member replay).

Until these code changes are implemented, the principles describe the
target state and the implementation has a known gap. The gap is
documented in `intent-gaps-plan.md` Gap 3a (superseded by this plan's
interface-centric design).

## Impact on intent-gaps-plan.md

**These revisions must be applied in the same commit as the design
principles changes** — having the two documents disagree on container
member classification or VLAN member intent placement creates
contradictory guidance.

- **Gap 3a (VLAN members)**: **SUPERSEDED.** The intent-gaps-plan
  originally proposed tracking VLAN members in the VLAN's intent record
  (VLAN-centric approach). This plan supersedes that with the
  interface-centric approach: `configure-interface` handles bridged
  mode, giving VLAN members their own interface-level intent records.
  The VLAN's intent stays simple (`{vlan_id: 100}`) — it does not
  track its members.

  Gap 3a must be **rewritten**, not just marked superseded. The
  following artifacts from the current Gap 3a are now incorrect and
  must be removed or replaced:
  - `parseVLANMembers` helper (VLAN intent no longer stores members)
  - `AddVLANMember` calling `updateIntent` on `vlan|{id}` (members
    are in the interface's intent, not the VLAN's)
  - `AddVLANMember`/`RemoveVLANMember` as public API operations
    (removed from public API; internal methods in `vlan_ops.go`
    remain — single-owner of VLAN_MEMBER table)
  - `create-vlan` ReplayStep replaying members (members are replayed
    via `configure-interface` ReplayStep in bridged mode)
  - The entire "member tracking lifecycle" for VLANs

  The replacement: `configure-interface` in bridged mode calls
  `AddVLANMember` internally (`vlan_ops.go` — single-owner of
  VLAN_MEMBER table) and creates an interface-level intent keyed to
  the interface name, with the VLAN ID in the params. Reconstruction
  replays each interface's intent through the same code path.

  `add-vlan-member`/`remove-vlan-member` endpoints must be deleted
  (operations no longer exist — see Gap 1 or new section below).

- **Gap 3b (ACL rules)**: Unchanged. ACL rules are container-tracked
  children. The `updateIntent` pattern applies.

- **Gap 3c (PortChannel members)**: Unchanged. Same container-tracked
  pattern.

- **Gap 1 (or new section)**: `add-vlan-member` and
  `remove-vlan-member` must be removed from the public API (HTTP
  endpoints, CLI commands, client methods). The internal methods in
  `vlan_ops.go` remain — single-owner of VLAN_MEMBER table writes.
  Gap 1 covers setup-device sub-operations; VLAN member endpoints are
  a different category (not sub-ops of setup-device but operations
  subsumed by `configure-interface`). If Gap 1's scope is strictly
  setup-device sub-ops, create a new section for these removals.

- **§22 classification**: The intent-gaps-plan heading at line 64,
  "§22 — Dual-Purpose Intent: Members Are Resolved Params", must be
  renamed to "Members Are User Params." The body text (lines 64-83)
  uses "resolved params" language throughout and must be rewritten to
  use "user params" — container membership fields are explicit operator
  decisions (`add-acl-rule`, `add-portchannel-member`), not values
  derived from spec resolution.

## Verification Criteria

1. No instance of "one operation = one intent record" remains in the
   document
2. §17 says "one resource = one intent record"
3. §19 explicitly states resources are the intent key, not operations
4. §15 pairs table does not list `AddVLANMember`/`RemoveVLANMember`
5. §6 includes the forwarding mode / layered interface model
6. §20 distinguishes container-tracked children (included in parent
   intent for reconstruction) from forwarding-domain members (own
   intent via configure-interface, not in container's intent)
7. §22 classifies container members as user params (explicit operator decisions)
8. §30 exception excludes VLAN members
9. §19 summary table entry updated to reflect resource-centric keying
10. All cross-references in the document are consistent
11. No principle contradicts another
