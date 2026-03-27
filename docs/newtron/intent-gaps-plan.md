# Intent Coverage Gaps — Resolution Plan

## Problem Statement

Three categories of API surface change device state without intent
recording:

1. **Exposed sub-operations of setup-device** (9 endpoints): Can be
   called directly, bypassing the `"device"` intent record. Invisible
   to drift detection and reconstruction.

2. **Container membership mutations** (6 endpoints): `add-vlan-member`,
   `remove-vlan-member`, `add-acl-rule`, `remove-acl-rule`,
   `add-portchannel-member`, `remove-portchannel-member` write CONFIG_DB
   without updating the parent container's intent record. A newtron-driven
   membership change appears as drift on the next assessment because
   reconstruction can't reproduce it.

3. **`apply-frr-defaults`** runs vtysh to set FRR runtime defaults. It is
   baseline device initialization that belongs inside `setup-device`, not
   a standalone API endpoint.

## Design Principle Review

### §17 — Operation Granularity: Coherent, Not Minimal

> "An operation is the smallest unit that leaves the device in a
> consistent, independently useful state."

The sub-operations (`configure-loopback`, `configure-bgp`, `setup-vtep`,
`configure-route-reflector`, `set-device-metadata`) fail this test. A
device after `configure-loopback` alone has an IP on lo but no routing
protocol to advertise it. §17 explicitly calls this out:

> "A device after `configure-loopback` alone has an IP address on lo
> but no routing protocol to advertise it. That doesn't make sense."

They should not be exposed as API surfaces.

The reverse primitives (`remove-loopback`, `remove-bgp`, `teardown-vtep`)
are tested by newtrun teardown suites but are not meaningful standalone
operations. Per §15: "baseline operations' collective reverse is
reprovision, not individual teardown." These can remain as internal
methods for testing but should not be public API.

### §19 — Unified Intent Model

> "Every newtron-managed resource deserves the same treatment: a
> persistent record of what was applied."

Container membership mutations violate this. `add-vlan-member` writes
VLAN_MEMBER to CONFIG_DB but produces no intent trace. The device
reality changes; the intent record doesn't. This is the gap.

### §20 — On-Device Intent Is Sufficient for Reconstruction

> "Intent records must be self-sufficient for reconstruction of expected
> state."

If a VLAN's intent records only `{vlan_id: 100}` but the device has
members Ethernet0 and Ethernet4, reconstruction produces a memberless
VLAN. The diff shows two "extra" VLAN_MEMBER entries — false drift.

### §22 — Dual-Purpose Intent: Members Are Resolved Params

Members and rules stored in intent are **resolved params** — they are the
current state of the container's membership, derived from the sequence of
add/remove calls, not from the original `create-vlan` user input. Per §22,
resolved params serve teardown and reconstruction; user params serve
Snapshot.

This means:
- **Snapshot exports `create-vlan` with `members` included.** Unlike
  service intents where resolved params (L3VNI, route maps) are stripped
  and re-resolved from specs, container members have no spec to resolve
  from — they ARE the operator's intent. A VLAN member added via
  `add-vlan-member` is a user decision, not a spec derivation. Snapshot
  must include them or the round-trip loses membership state.
- **Teardown reads members from the intent** but DeleteVLAN also scans
  ConfigDB for actual members (§15 cascading destroy). The intent is the
  reconstruction source; ConfigDB is the teardown source. This is
  consistent with §5: "the device is reality."

### §15 — Symmetric Operations

§15 lists `AddVLANMember`/`RemoveVLANMember` as a symmetric pair. This
is correct at the CONFIG_DB level. But neither side updates intent. The
symmetry exists for CONFIG_DB operations but not for intent lifecycle —
which §19 and §20 demand. This plan closes that gap.

### §11 — ChangeSet as Universal Contract

> "Every mutating operation produces a ChangeSet."

Intent updates for membership mutations MUST go through the ChangeSet —
not as shadow-only updates. The intent update entry is added to the same
ChangeSet that carries the VLAN_MEMBER/ACL_RULE write, making it
atomic, previewable (dry-run), and verifiable.

### §27 — Single-Owner Principle

NEWTRON_INTENT writes are owned by `intent_ops.go`. The existing helpers
`writeIntent` and `deleteIntent` live there. The new `updateIntent`
helper also lives there. Container operation files (`vlan_ops.go`,
`acl_ops.go`, `portchannel_ops.go`) call the helper — they never write
NEWTRON_INTENT directly.

### §13 — Schema Validation

NEWTRON_INTENT already uses `AllowExtra: true` in the schema (variable
params per operation). The new `members` and `rules` fields pass through
as extra fields. No schema change needed — this is the mechanism the
schema was designed for (intent-plan.md Phase 0f: "Allow all other fields
without validation — params vary by operation type").

### New Principle: Container Intent Tracks Membership (addition to §20)

§17 defines the coherence test for *whether an operation is standalone*.
§20 demands intent records sufficient for reconstruction. Neither
addresses what happens when a mutation targets a child of an existing
intent-bearing resource. This is the gap.

**Proposed addition to §20 (after "The device carries its own intent")**:

> **Container intents track membership.** When an operation adds or
> removes a member of a container resource (VLAN member, ACL rule,
> PortChannel member), the container's intent record is updated to
> reflect the change. The intent record is the reconstruction source —
> if it doesn't include members, reconstruction produces an empty
> container, and the diff shows false drift.
>
> Membership mutations are not standalone intent-bearing operations —
> they update the existing container intent. A VLAN member added via
> `add-vlan-member` updates the `vlan|{id}` intent's `members` field.
> An ACL rule added via `add-acl-rule` updates the `acl|{name}` intent's
> `rules` field. This extends the portchannel pattern: `create-portchannel`
> already stores members in the intent params; `add-portchannel-member`
> updates the same field.
>
> The coherence test from §17 confirms: a VLAN member is not
> independently useful without its VLAN. An ACL rule is not independently
> useful without its ACL table. These are children, not peers — they
> belong in the parent's intent record, not in their own.
>
> The intent update goes through the ChangeSet (§11) — atomic with the
> CONFIG_DB write, previewable via dry-run, verifiable post-apply. The
> `updateIntent` helper in `intent_ops.go` is the sole writer (§27).

Cross-references: §11 (ChangeSet), §17 (coherence test), §22
(resolved params), §27 (single owner).

## Resolution

### Gap 1: Remove exposed sub-operation endpoints

**Rationale**: §17 says these are not coherent operations. The
intent-plan.md Phase 1 Step 2 says they should not be public. They
remain exposed because the cleanup was never done.

**Changes**:

1. **Remove 9 API routes** from `handler.go`:
   - `POST .../configure-loopback`
   - `POST .../remove-loopback`
   - `POST .../configure-bgp`
   - `POST .../remove-bgp`
   - `POST .../setup-vtep`
   - `POST .../teardown-vtep`
   - `POST .../configure-route-reflector`
   - `POST .../set-device-metadata`
   - `POST .../apply-frr-defaults`

   **Not removed**: `remove-bgp-evpn-peer` stays — `add-bgp-evpn-peer`
   is a standalone intent-producing operation (writes `evpn-peer|{ip}`
   intent). Its reverse must remain as a public endpoint (§15).

2. **Remove 9 handler functions** from `handler_node.go`.

3. **Remove 9 public API wrappers** from `pkg/newtron/node.go`:
   - `ConfigureLoopback`, `RemoveLoopback`
   - `ConfigureBGP`, `RemoveBGPGlobals`
   - `SetupVTEP`, `TeardownVTEP`
   - `ConfigureRouteReflector`
   - `SetDeviceMetadata`
   - `ApplyFRRDefaults`

4. **Remove 9 client methods** from `pkg/newtron/client/node.go`.

5. **Remove all newtrun YAML references** to sub-operation endpoints.
   The 7 affected YAML suite files are listed below in the "Affected
   newtrun suite files" table. No dedicated action metadata files
   exist — the YAML suite rewrites are the only newtrun changes.

6. **Internal methods remain** on `node.Node`. `SetupDevice` calls them.
   They are implementation details, not public API.

7. **Update `ReplayStep` dispatcher**: Remove cases for
   `configure-loopback`, `configure-bgp`, `setup-vtep`,
   `configure-route-reflector`, `set-device-metadata`. These are the
   5 forward sub-operations called internally by `SetupDevice`, not as
   standalone replay operations. The reverse operations (`remove-loopback`,
   `remove-bgp`, `teardown-vtep`) and `apply-frr-defaults` have no
   `ReplayStep` cases — they were never replay targets (reconstruction
   only replays forward operations).

**Teardown suite migration**: The `90-teardown-infra.yaml` files in
2node-ngdp-primitive and 2node-vs-primitive call `teardown-vtep`,
`remove-bgp-peer`, `unconfigure-interface`, `remove-bgp`, and
`remove-loopback` as individual API calls.

After removing sub-operation endpoints:

- `teardown-vtep`, `remove-bgp`, `remove-loopback` become unavailable
  via the API.
- `remove-bgp-peer` (interface-level underlay) and
  `unconfigure-interface` remain — they are intent-producing standalone
  operations with their own reverse pairs.

**Affected newtrun suite files** (7 files use removed endpoints):

| File | Removed ops used |
|------|------------------|
| `newtrun/suites/2node-ngdp-primitive/90-teardown-infra.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/2node-vs-primitive/90-teardown-infra.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/2node-ngdp-service/05-deprovision.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/2node-vs-service/05-deprovision.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/3node-ngdp-dataplane/06-teardown.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/2node-vs-drift/12-teardown.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |
| `newtrun/suites/2node-vs-zombie/12-teardown.yaml` | `teardown-vtep`, `remove-bgp`, `remove-loopback` |

Note: `remove-bgp-peer` (interface-level underlay) and
`unconfigure-interface` remain available — they are intent-producing
standalone operations with their own reverse pairs.

**Migration path for teardown suites**: Two options.

Option A: Replace piecewise teardown with reprovision. The suite calls
`deliver-composite` with an empty/baseline composite, verifying that
reprovision replaces the entire device state. This aligns with §15
("remediation is reprovision"). Note: reprovision involves a full
convergence cycle which is slower than piecewise teardown — test
timeouts may need adjustment.

Option B: Keep teardown suites but route them through internal test
infrastructure (direct Go test calls) rather than the HTTP API. The
teardown primitives exist as Node methods — they just aren't public
API. Go integration tests can call them directly.

Recommendation: Option A for newtrun E2E suites (tests the mechanism
operators actually use). Option B for Go-level unit tests that exercise
individual teardown primitives.

**Cross-reference with pending newtrun suite refactor plan**: A
separate plan (graceful-coalescing-shell) renames
`40-evpn-setup.yaml` → `40-evpn-verify.yaml` and
`02-loopback.yaml` → `02-setup-device.yaml` in primitive suites.
Those renames do NOT affect teardown files. However, if both plans
are executed together, ensure `requires:` references remain consistent.

**No external consumers exist** beyond newtrun and the CLI. The HTTP
API has no third-party clients (§40: greenfield, no installed base).

### Gap 2: Remove redundant apply-frr-defaults endpoint

**Rationale**: FRR defaults (`no bgp ebgp-requires-policy`, `no bgp
suppress-fib-pending`) are baseline device initialization. Every device
needs them. They should not be a separate API call.

**Analysis**: `SetupDevice` produces a ChangeSet of CONFIG_DB entries.
`ApplyFRRDefaults` runs vtysh over SSH. Abstract/offline mode has no SSH.
However, `ConfigureBGP` (in `bgp_ops.go`) **already writes these fields**
to BGP_GLOBALS — the vtysh endpoint is redundant:
```go
cs.Updates(CreateBGPGlobalsConfig("default", resolved.UnderlayASN, resolved.RouterID, map[string]string{
    "ebgp_requires_policy": "false",
    "suppress_fib_pending": "false",
    "log_neighbor_changes": "true",
}))
```

The same fields also appear in `ConfigureRouteReflector` (evpn_ops.go)
and `SetupProvisionBGP` (service_ops.go). No code change needed —
the CONFIG_DB entries are already correct.

**What remains**:

1. **Remove `apply-frr-defaults`** — already listed in Gap 1's route
   removal list. No additional code change beyond the removal.

2. **If vtysh workaround is still needed** on some platforms where
   frrcfgd doesn't process these fields (sonic-vs template gap,
   RCA-008), the online execution path in `pkg/newtron/node.go` can
   run the vtysh commands after the ChangeSet is applied. Tag with
   `CLI-WORKAROUND(frr-defaults)`. Abstract mode skips it — the
   CONFIG_DB entries are present in the shadow.

3. **No additional intent param needed** — reconstruction replays
   `SetupDevice` which writes the same BGP_GLOBALS entries.

### Gap 3: Container intents track membership

**Rationale**: §20 demands intent records sufficient for reconstruction.
Container intents that omit their members fail this test.

**Design**: When a membership mutation occurs, the operation calls
`updateIntent` (owned by `intent_ops.go`, per §27) which reads the
existing container intent, updates the membership field, and adds the
updated intent as a ChangeSet entry (per §11). This keeps the flat
intent model (no parent/child linking per §19) — the container intent
simply grows.

**Scope rule: only update intents that newtron created.** If
`add-vlan-member` targets a VLAN with no `vlan|{id}` intent (VLAN
created outside newtron — manually, by ApplyService, or pre-existing),
the intent update is skipped. We don't retroactively create intent
records for resources we didn't create via standalone operations.

This means:
- Standalone `create-vlan` + `add-vlan-member` → intent tracks members.
  Reconstruction produces complete VLAN with members. No false drift.
- ApplyService creates VLAN internally → no `vlan|{id}` intent.
  Subsequent `add-vlan-member` on that VLAN → no intent update.
  This is correct: the service intent owns the VLAN. If the operator
  adds a member to a service-owned VLAN, they're mutating outside
  newtron's intent model. The drift detector will see the extra member
  as genuine drift — because it IS drift relative to the service intent.
- VLAN exists from factory/manual → no intent. `add-vlan-member` → no
  intent update. Consistent: newtron doesn't own this VLAN.

**Standalone membership mutations on service-owned or manual containers
are intentionally invisible to the intent model.** They surface as
drift on the next assessment. This is correct behavior per §5: the
device is reality, and the mutation is a real change that the intent
model didn't authorize. The operator chose to mutate outside the intent
boundary — the consequence is visible drift.

**No retroactive intent creation.** `add-vlan-member` on a VLAN without
a `vlan|{id}` intent silently skips the intent update and succeeds
(the CONFIG_DB write still happens). It does not error. The membership
add is a valid device mutation — it just isn't tracked by intent.

**Concurrency**: If two operations race (one creating the VLAN intent,
another adding a member), the skip is benign. The member exists in
CONFIG_DB (reality); it may or may not appear in the intent. The next
`add-vlan-member` or reconstruction will reconcile. This is not a
correctness bug — it's a visibility delay in a rare race condition.

#### 3a. VLAN members

**Current `create-vlan` intent**: `{vlan_id: 100}`

**After**: `{vlan_id: 100, members: "Ethernet0:untagged,Ethernet4:tagged"}`

Format: `interface:mode` pairs, comma-separated. Same convention as
portchannel `members` field. Parsing: `strings.Split` on comma, then
on colon.

**Changes to `vlan_ops.go`**:

- `CreateVLAN`: Unchanged (intent has empty/absent `members` field).
- `AddVLANMember`: After producing the VLAN_MEMBER ChangeSet entry,
  call `n.updateIntent(cs, "vlan|"+id, updates)` where updates adds
  the new member to the `members` field. The `updateIntent` helper
  (in `intent_ops.go`) reads the existing intent, merges the update,
  and adds the full intent as a ChangeSet entry.
- `RemoveVLANMember`: Same pattern — call `updateIntent` to remove
  the member from `members`. When the last member is removed, the
  `members` field becomes `""` (empty string). The field is NOT deleted
  from the intent map — no key deletion, only updates. `parseVLANMembers("")`
  returns nil (zero members), which is correct for reconstruction.
- `DeleteVLAN`: Unchanged (deletes intent entirely via `deleteIntent`;
  `destroyVlanConfig` scans ConfigDB for members as before).

**Changes to `reconstruct.go`**:

- `ReplayStep` for `create-vlan`: After `n.CreateVLAN(ctx, vlanID, cfg)`,
  parse `members` from step params. For each member, call
  `n.AddVLANMember(ctx, vlanID, iface, tagged)`. Each `AddVLANMember`
  call updates the shadow ConfigDB (offline mode accumulates entries
  automatically) and updates the intent's `members` field via
  `updateIntent`.
- `intentParamsToStepParams` for `create-vlan`: Pass `members` through
  to step params.

**Member parsing helper**:

```go
// parseVLANMembers parses "Ethernet0:untagged,Ethernet4:tagged" into
// member configs. Returns nil for empty/absent input (zero members).
func parseVLANMembers(s string) []struct{ Name string; Tagged bool } {
    if s == "" {
        return nil
    }
    // Split on comma, then each entry on colon
    // "Ethernet0:untagged" → {Name: "Ethernet0", Tagged: false}
    // "Ethernet4:tagged"   → {Name: "Ethernet4", Tagged: true}
}
```

**Intent field lifecycle**: `CreateVLAN` writes the intent with no
`members` field. Each `AddVLANMember` call updates the intent via
`updateIntent` to append to `members`. The `members` field grows as
members are added. When Snapshot exports the intent, `members` is
included in the step params. When ReplayStep replays, it calls
`CreateVLAN` (which writes a fresh intent with no members), then calls
`AddVLANMember` for each parsed member (which progressively builds up
the `members` field in the intent). The round-trip is complete.

#### 3b. ACL rules

**Current `create-acl` intent**: `{name: PROTECT_RE, type: L3, stage: INGRESS}`

**After**: Add `rules` field with compact JSON:
```
rules = [{"name":"RULE_1","priority":"1","action":"ACCEPT","src_ip":"10.0.0.0/8"}]
```

JSON is necessary because ACL rules have variable multi-field entries
(priority, action, src_ip, dst_ip, l4_src_port, etc.). A
comma-separated format cannot represent this. VLAN members have a
fixed two-field structure (interface:mode) where comma-separation works.
ACL rules do not.

The JSON string lives inside the flat `map[string]string` intent — it
is a string value, not nested structure. This is consistent with §19
(flat intent map) — the intent record stores strings; the
interpretation is per-operation. This is the only field in the intent
model that uses JSON. The alternatives (one intent per rule, or a
custom encoding) are worse: one-per-rule violates §17 (rule is not
independently useful), and a custom encoding is fragile.

**All field values in the JSON are strings** (matching CONFIG_DB's
string-only field model). No numeric or boolean JSON types.

**Parsing helpers** (in `acl_ops.go`):

```go
func marshalACLRules(rules []ACLRuleConfig) string   // -> JSON string
func unmarshalACLRules(s string) []ACLRuleConfig      // JSON string ->
```

Round-trip property: `unmarshal(marshal(rules)) == rules`. Tested
explicitly. Empty/absent `rules` field parses as nil slice (zero rules).

**Changes to `acl_ops.go`**:

- `AddACLRule`: After producing ACL_RULE ChangeSet entry, read existing
  `acl|{name}` intent, parse `rules`, **check for duplicate rule name**
  (error if exists — no silent overwrites), append new rule, serialize
  back, call `n.updateIntent(cs, "acl|"+name, updates)`.
- `DeleteACLRule`: Same — parse, remove by rule name, serialize,
  updateIntent.
- `DeleteACL`: Unchanged (deletes intent entirely via `deleteIntent`;
  `deleteAclTableConfig` scans ConfigDB for actual rules as before).

**Changes to `reconstruct.go`**:

- `ReplayStep` for `create-acl`: After `n.CreateACL`, parse `rules`
  from step params. For each rule, call `n.AddACLRule`.
- `intentParamsToStepParams`: Pass `rules` through.

#### 3c. PortChannel members

**Already partially correct.** `CreatePortChannel` stores `members` in
the intent at creation time. `ReplayStep` replays them.

**Existing bug**: `AddPortChannelMember` and `RemovePortChannelMember`
do NOT update the `portchannel|{name}` intent. Post-creation membership
changes are invisible to reconstruction. This is the same bug as VLANs
and ACLs — the difference is that PortChannel creation already tracks
initial members, so only post-creation mutations are lost.

**Fix**: Same pattern — `AddPortChannelMember` calls `updateIntent` to
append to `members`, `RemovePortChannelMember` removes from `members`.

### Gap 3 — Priority and ReplayStep ordering

Members are replayed inline during the `create-*` ReplayStep case.
No new priority entries needed:
- `create-vlan` (priority 30): members replayed inline
- `create-acl` (priority 40): rules replayed inline
- `create-portchannel` (priority 45): members already replayed inline

### Gap 3 — The `updateIntent` helper (§27, §11 compliant)

Lives in `intent_ops.go` — the sole owner of NEWTRON_INTENT writes.

```go
// updateIntent reads the existing intent for a resource, merges the
// provided field updates, and writes the complete merged record as a
// DEL+HSET pair in the ChangeSet. If the resource has no existing
// intent (resource not created by newtron), the update is skipped.
//
// DEL+HSET is required (CLAUDE.md "CONFIG_DB Replace Semantics"):
// Redis HSET merges fields — it does NOT remove old fields. When the
// update removes a field (e.g., last member removed, members field
// becomes empty), HSET alone leaves the stale field. DEL first, then
// HSET with the complete new field set.
//
// This goes through the ChangeSet (§11) for atomicity, dry-run
// preview, and post-apply verification.
func (n *Node) updateIntent(cs *ChangeSet, resource string, updates map[string]string) {
    fields, ok := n.configDB.NewtronIntent[resource]
    if !ok {
        return // resource not managed by newtron — skip silently
    }
    merged := maps.Clone(fields)
    for k, v := range updates {
        merged[k] = v  // always set — use "" for empty, never delete keys
    }
    cs.Delete("NEWTRON_INTENT", resource)
    cs.Add("NEWTRON_INTENT", resource, merged)

    // Shadow update for offline/abstract mode: n.op() applies config
    // entries but updateIntent is called AFTER op() returns, so the
    // shadow doesn't see the intent update automatically. Apply it
    // directly — same pattern as writeIntent and deleteIntent.
    //
    // Only the Add entry is accumulated (not the Delete). BuildComposite
    // cares about final state, not intermediate deletes. The DEL+HSET
    // pair above is for the online Redis path where HSET merges fields.
    if n.offline {
        n.configDB.DeleteEntry("NEWTRON_INTENT", resource)
        entry := sonic.Entry{Table: "NEWTRON_INTENT", Key: resource, Fields: merged}
        n.configDB.ApplyEntries([]sonic.Entry{entry})
        n.accumulated = append(n.accumulated, entry)
    }
}
```

**Bug: `updateIntent` must handle offline shadow updates.** Both
`writeIntent` and `deleteIntent` in `intent_ops.go` have explicit
`if n.offline { ... }` blocks. Without the offline block, a second
`AddVLANMember` call on the same VLAN in abstract mode would read
stale intent from the shadow (first member missing from the `members`
field). The offline block ensures sequential `updateIntent` calls see
each other's effects.

**DEL+HSET**: The helper always writes the complete merged field set
preceded by a DEL. SONiC daemons don't subscribe to NEWTRON_INTENT —
the intermediate delete is invisible to daemon processing. The shadow
ConfigDB sees the final state after both entries are applied.

**No field deletion**: Callers always set fields to explicit values
(empty string for "no members" is fine — `members: ""`). The merged
map never has keys removed, only updated. This avoids ambiguity
between "field removed" and "field absent from HSET args."

Callers in `vlan_ops.go`, `acl_ops.go`, `portchannel_ops.go` call this
helper — they never write NEWTRON_INTENT directly (§27).

### Teardown semantics: Intent vs ConfigDB (§5, §15)

**Deletion operations continue to scan ConfigDB**, not the intent record,
to find members to remove. This is correct per §5 ("the device is
reality"). If someone manually added a VLAN member via redis-cli, the
intent record doesn't know about it, but `destroyVlanConfig` finds it
in ConfigDB and removes it.

The intent record serves **reconstruction** (§20, §21). ConfigDB serves
**teardown** (§5, §15). Different purposes, same resource.

### Bounded footprint (§23)

Intent records grow with the number of members, not with the number of
operations. A VLAN with 50 members has a ~1KB `members` string. A VLAN
that was modified 100 times still has the same final `members` state.
This is proportional to infrastructure, not time — §23 is satisfied.

## File Changes Summary

| File | Action | What |
|------|--------|------|
| `api/handler.go` | Edit | Remove 9 sub-operation routes |
| `api/handler_node.go` | Edit | Remove 9 handler functions |
| `pkg/newtron/node.go` | Edit | Remove 9 public wrappers |
| `pkg/newtron/client/node.go` | Edit | Remove 9 client methods |
| `node/intent_ops.go` | Edit | Add `updateIntent` helper |
| `node/vlan_ops.go` | Edit | AddVLANMember/RemoveVLANMember call updateIntent |
| `node/acl_ops.go` | Edit | AddACLRule/DeleteACLRule call updateIntent; add marshal/unmarshal; fix DeleteACLRule to use n.op() (Bug A) |
| `node/portchannel_ops.go` | Edit | AddPortChannelMember/RemovePortChannelMember call updateIntent; fix OperationParams asymmetry (Bug B) |
| `node/reconstruct.go` | Edit | Remove sub-op cases; add member/rule replay in create-vlan/create-acl |
| `node/reconstruct_test.go` | Edit | Tests for member/rule replay round-trip |
| `node/intent_test.go` | Edit | Tests for updateIntent, member format parsing |
| `DESIGN_PRINCIPLES_NEWTRON.md` | Edit | Add container membership principle to §20 |
| `docs/newtron/intent-plan.md` | Edit | Update to reflect sub-op removal and container membership |
| `newtrun/suites/2node-ngdp-primitive/90-teardown-infra.yaml` | Rewrite | Replace piecewise baseline teardown |
| `newtrun/suites/2node-vs-primitive/90-teardown-infra.yaml` | Rewrite | Replace piecewise baseline teardown |
| `newtrun/suites/2node-ngdp-service/05-deprovision.yaml` | Rewrite | Replace sub-op teardown steps |
| `newtrun/suites/2node-vs-service/05-deprovision.yaml` | Rewrite | Replace sub-op teardown steps |
| `newtrun/suites/3node-ngdp-dataplane/06-teardown.yaml` | Rewrite | Replace sub-op teardown steps |
| `newtrun/suites/2node-vs-drift/12-teardown.yaml` | Rewrite | Replace sub-op teardown steps |
| `newtrun/suites/2node-vs-zombie/12-teardown.yaml` | Rewrite | Replace sub-op teardown steps |

Note: `cmd/newtron/` has no references to sub-operations — no changes needed.
Note: `baseline_ops.go` already writes `ebgp_requires_policy` and
`suppress_fib_pending` via `ConfigureBGP` — no changes needed.

## Verification Criteria

1. `go build ./... && go vet ./... && go test ./... -count=1` passes
2. Zero API routes for sub-operations of setup-device or apply-frr-defaults
3. `AddVLANMember` produces a ChangeSet with both VLAN_MEMBER and
   updated NEWTRON_INTENT entries (verified via `cs.Changes`)
4. `AddACLRule` produces a ChangeSet with both ACL_RULE and updated
   NEWTRON_INTENT entries
5. `AddPortChannelMember` produces a ChangeSet with both
   PORTCHANNEL_MEMBER and updated NEWTRON_INTENT entries
6. Round-trip test: create-vlan + add-vlan-member → Snapshot → replay
   on fresh abstract node → identical CONFIG_DB (VLAN + VLAN_MEMBER +
   NEWTRON_INTENT with members)
7. Round-trip test: create-acl + add-acl-rule → Snapshot → replay →
   identical CONFIG_DB (ACL_TABLE + ACL_RULE + NEWTRON_INTENT with rules)
8. `apply-frr-defaults` endpoint no longer exists
9. `setup-device` writes `ebgp_requires_policy: false` and
   `suppress_fib_pending: false` to BGP_GLOBALS
10. `add-vlan-member` on a VLAN with no intent record (service-created)
    produces a ChangeSet with VLAN_MEMBER only (no NEWTRON_INTENT update)
11. `DeleteVLAN` still scans ConfigDB for actual members (not just intent)
12. ACL rules JSON round-trip: `unmarshal(marshal(rules)) == rules`
13. Grep for sub-operation URLs (`configure-loopback`, `configure-bgp`,
    `setup-vtep`, `teardown-vtep`, `remove-bgp`, `remove-loopback`,
    `set-device-metadata`, `configure-route-reflector`,
    `apply-frr-defaults`) in all YAML suite files — zero hits in any step
14. `DeleteACLRule` on an abstract node updates the shadow ConfigDB
    (Bug A fix verified)
15. `RemovePortChannelMember` ChangeSet includes `OperationParams`
    (Bug B fix verified)

## Pre-existing Bugs to Fix

These bugs exist independently of the intent gaps. They MUST be fixed
as part of this work — not worked around.

### Bug A: `DeleteACLRule` bypasses `n.op()` — no shadow update in abstract mode

**File**: `acl_ops.go`, line 257

`DeleteACLRule` constructs a fresh `NewChangeSet` without going through
`n.op()`. In offline/abstract mode, `n.op()` is what calls
`applyShadow(cs)` to update the shadow ConfigDB and append to
`n.accumulated`. Without it, `DeleteACLRule` on an abstract node:
- Does NOT remove the ACL_RULE from shadow ConfigDB
- Does NOT append the delete entry to `n.accumulated`
- Subsequent operations see stale rule in shadow

**Fix**: Refactor `DeleteACLRule` to use the `n.op()` pattern, same as
`AddACLRule` and all other mutating operations. The delete entry goes
through the same ChangeSet pipeline, with shadow update in abstract mode.

**Note**: Other reverse operations (`UnconfigureIRB`, `DeleteVRF`,
`UnbindIPVPN`) also bypass `n.op()` with the same pattern. These are
unaffected because reconstruction doesn't replay reverse operations —
they never run in abstract mode. `DeleteACLRule` is different: it
removes a rule from an existing ACL whose intent IS tracked, so it
runs during the forward lifecycle where abstract mode matters.

### Bug B: `RemovePortChannelMember` missing `OperationParams`

**File**: `portchannel_ops.go`, lines 150–163

`AddPortChannelMember` sets `cs.OperationParams` with the member name.
`RemovePortChannelMember` does not. This asymmetry means CLI dry-run
output for member removal lacks context (no param describing which
member was removed).

**Fix**: Add `cs.OperationParams = map[string]string{"member": memberName}`
to `RemovePortChannelMember`, matching `AddPortChannelMember`.

## What This Plan Does NOT Do

- **No new intent-bearing operations.** Membership mutations update
  existing container intents — they don't create new ones.
- **No parent/child intent linking.** Flat model preserved per §19.
- **No retroactive intent creation.** `add-vlan-member` on a VLAN
  without a `vlan|{id}` intent (service-created, manual, factory)
  skips the intent update. Newtron only tracks what it created.
- **No changes to teardown logic.** `DeleteVLAN` scans ConfigDB for
  actual members. The intent update serves reconstruction, not teardown.
- **No changes to service_ops.go.** Service-created infrastructure is
  tracked by the service intent. Container intent updates apply to
  standalone operations only.
- **No journal or history.** Intent records are current state, not
  append-only logs. Per §21: "derive expected state from authoritative
  sources; don't maintain a parallel record of it."
