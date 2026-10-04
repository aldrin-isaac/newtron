# Spec-Diff: Separating "Behind" From "Drifted"

Status: **proposed** — justification, three candidate designs, an implementation
plan for Design C, and decision record. No code implements any of them today. A
partial earlier attempt (#486 rung 0a) was built and removed; the reasons are
recorded in "What the first attempt got wrong."

## The defect this fixes

Newtron refuses writes to a device whose CONFIG_DB diverges from its projection.
The guard lives in `Node.Lock`:

```go
if n.actuatedIntent && len(n.configDB.NewtronIntent) > 0 {
    expected := n.configDB.ExportRaw()
    actual, _ := n.conn.Client().GetRawOwnedTables(ctx)
    drift := sonic.DiffConfigDB(expected, actual, sonic.OwnedTables())
    if len(drift) > 0 {
        return fmt.Errorf("device drifted from intents (%d entries) — reconcile first", ...)
    }
}
```

`expected` is the projection that `execute()` rebuilds before every operation, and
`RebuildProjection` replays the device's intents **through the current specs**. So
the comparison is *current-spec projection* vs *device*.

That conflates two events with opposite correct responses:

| Event | What happened | Correct response |
|---|---|---|
| **Drifted** | Someone edited CONFIG_DB outside newtron | Refuse writes; reconcile the device back |
| **Behind** | Someone edited a *spec*; the device was never touched | Nothing is wrong with the device — it is stale. Refresh it |

Today both produce the same outcome: writes refused, with the message *"device
drifted from intents."* When the cause is a spec edit that message is false — the
device did not drift. Nothing was edited on it. The intent moved.

The operational consequence: **editing a spec freezes writes to every device bound
to it.** The operator's remedy is `reconcile`, which bypasses the drift guard and
pushes the full projection — so the spec change reaches the fleet as a side effect
of an operation named "fix drift," with no point at which the system says *"this
device is behind; applying will change these fields."* Spec authoring and drift
repair are different acts, and newtron currently cannot tell them apart or name
them differently.

This is not a missing diagnostic. It is a correctness defect in the guard's
semantics, and `DESIGN_PRINCIPLES_NEWTRON.md` already records it as a known gap — in
the thesis introduction ("Both surface as drift, and the guard blocks on both") and
in the "Reconstruction and device state" subsection.

## The comparison, stated precisely

Three states exist:

1. **Applied** — what this device was configured with, at the time it was configured.
2. **Actual** — the device's CONFIG_DB right now.
3. **Current** — what today's specs would produce for this device, rendered by
   today's newtron.

Newtron computes only one comparison, `Current ↔ Actual`, and calls the result
drift. The two causes separate cleanly only if `Applied` is available:

- `Applied ↔ Actual` — someone changed the device. **True drift.**
- `Applied ↔ Current` — someone changed a spec, or newtron changed how it renders
  one. **Behind.**

The second cause of "behind" is easy to miss, because no operator acted. A newtron
release that renders the same spec differently moves `Current` for every device it
touches: #516 took a QoS binding from one `QUEUE` row per policy queue to one per
platform queue, so every port bound before it looked different from replay the
moment the new release ran, with no spec edited and no device touched. Replay goes
through the code as well as the specs, so a code change is a spec change as far as
`Current` is concerned.

## Why replay cannot produce `Applied`

Reconstruction replays each intent against *current* specs, so what it produces is
`Current` by construction. `Applied` is not a state the intent DB can be replayed
into:

- **Spec-derived values are re-derived, not stored** (§20 round-trip completeness:
  "values re-resolved from specs at replay time are re-derived, not stored"). An
  intent records the *decision* — "this interface binds MAC-VPN SERVERS" — not the
  derivation — "…whose VNI is 10200."
- **There is no spec history.** §23 bounds CONFIG_DB footprint to infrastructure,
  never to operations over time. Storing past spec versions to replay against would
  violate that directly.

`recorded()` params do not close the gap, and the reason is worth stating precisely
because the manifest does not say it. Whether a recorded param survives a rebuild is
decided **per operation, by its `Replay` closure** — not by the manifest, and not by
one rule:

- `bind-ipvpn` threads `vrf_name` back into the operation, so that value is carried
  forward unchanged. Its own registry comment says why: interface-mode binds a
  per-interface VRF, so the name is not re-derivable.
- The same manifest declares `l3_vni`, `l3_vni_vlan` and `route_targets` as recorded,
  and the closure does **not** thread them. `BindIPVPN` resolves them from the IP-VPN
  spec on every replay, and `writeIntent` replaces the record wholesale (DEL+HSET,
  #228), so the new values overwrite the old.
- `apply-service` threads none of its recorded params: `exportApplyService` emits
  caller fields only and drops the rest, with the comment "Only export caller params
  needed for replay, not recorded state."

So a record holds a mixture — some values as applied, some as the specs read them
most recently — and nothing in the manifest distinguishes the two. `SourceRecorded`'s
own comment ("re-resolved during replay") describes the majority case, not an
invariant.

**The consequence that shapes the designs below:** replay cannot produce `Applied`,
so a diagnostic built on replay reports whichever recorded params happen to be
recomputed. `Applied` is not recoverable by replay — but it *is* recordable at
delivery, which is the second design.

## What the first attempt got wrong

#486 rung 0a compared the intent DB before and after a replay: snapshot the
device's records, replay them through current specs, diff the two record sets. It
shipped as `Node.SpecDiff`, `GET …/intent/spec-diff`, a CLI verb, two public types
and a client method.

It could only ever see the recorded params its own replay *recomputed* — the subset
whose `Replay` closure does not thread them forward. A spec change that altered
CONFIG_DB through a non-recorded derivation was invisible to it, and so was one
whose value the closure carries forward unchanged. It therefore answered a narrower
question than its name implied, while presenting as the general one.

It also changed no behaviour. The guard still froze writes on a spec edit, still
with the wrong message. The read existed; the thing that made the read worth
having did not.

Both are recorded here because the shape of the mistake is reusable: a diagnostic
that reports a *proxy* for the quantity of interest, shipped as though it reported
the quantity.

## Design A — the device-level answer

Accept that `Applied` is not recoverable by replay, and observe that the operator's
questions are answerable at device granularity without it. This design is presented
first because it is the smallest change that fixes the guard's behaviour; Design B
answers more and costs more.

The operator's questions:

> Q1. Is this device current with the specs?
> Q2. If I refresh it, what changes?
> Q3. Did someone edit this device behind my back?

**Q2 is already answered.** If the device has not been edited, `Current ↔ Actual`
— today's drift — *is* the pending refresh delta. The entries are exactly the
fields a refresh would write.

**Q1 is answerable with one stored value.** `spec.DiskDigest(specDir)` already
exists: a content hash over every spec file in a network directory, used by the
reload path, with `syncedDigest` tracking the last load-or-write. Record on the
device the digest that was in effect when it was last provisioned or reconciled.

**Q3 then follows without per-field provenance**, because the digest resolves the
ambiguity that drift alone cannot:

| Digest | Drift | Meaning | Response |
|---|---|---|---|
| same | empty | Device current | Proceed |
| same | non-empty | **Drifted** — specs unchanged, so the device was edited | Refuse; reconcile |
| differs | empty | Spec changed but does not affect this device | Proceed; re-stamp digest |
| differs | non-empty | **Behind** — pending spec change | Allow with warning, or require explicit refresh |

The coarse, network-wide digest never produces a false "behind," because the
classification requires drift to be non-empty: a spec edit touching other devices
leaves this one with an empty drift and is reported as current.

### What gets built

1. **Stamp the applied digest.** One value per device, written where intents are
   written, updated on provision and on reconcile. Bounded per §23 — one field,
   updated in place, never a history.
2. **Classify in the existing read.** `Drift` returns its entries plus the cause.
   No new endpoint, no new CLI verb, no new public type beyond a field on the
   existing drift response.
3. **Correct the guard.** `Lock` refuses on `Drifted`. On `Behind` it does not
   claim the device drifted; it names the real condition and points at refresh.
4. **Name the operation that resolves it.** "Behind" is repaired by bringing the
   device up to current specs. Reconcile already delivers the projection; what is
   missing is the honest name and the "show before do" moment — the drift entries
   are the preview, and they already exist.

### Known limitation, accepted

If a spec changed **and** someone edited the device, the compound case classifies
as "behind" and the device edit is not separately reported. A refresh then
overwrites the unauthorized edit. That is the desired outcome under "no
brownfield — newtron owns the full CONFIG_DB for any node it manages," so the
degradation is safe.

A per-spec digest set (the specs a device's intents actually reference, each
hashed) would narrow the coarse network digest and make "differs + empty drift"
rarer. It is a refinement, not a prerequisite, and should not be built until the
coarse form proves insufficient.

### Known limitation: a newtron release looks like drift

The digest hashes the spec files, and a newtron release changes none of them. When
a release renders a spec differently, the digest is unchanged and drift is
non-empty, so the device classifies as **Drifted**: the guard refuses and tells the
operator the device was edited, when only newtron changed. Unlike the compound case
above, this degradation is not safe — it misnames the cause, on every device the
change touches, at once.

Stamping newtron's own identity beside the digest would close it: a different build
then reads as "differs", which classifies as Behind. That only works if every
release that can change rendering carries a distinct identity, which a development
build reporting `dev` does not.

## Design B — stamp applied values at delivery

`Applied` is not recoverable by replay. It is recordable at delivery.

Recorded params are written today when an operation is *constructed*, and a rebuild
rewrites them from whatever the specs say at that moment. The change is to write them
when the ChangeSet is *delivered*, and to carry them forward unchanged through
reconstruction. Rows continue to come from the generators against current specs, so a
spec edit still moves expected state for every device that references it — §21 is
untouched. What changes is that the record stops tracking the specs and starts
tracking the device.

Both comparisons then become computable per field:

- `Applied ↔ Actual` — the record against the device. **True drift.**
- `Applied ↔ Current` — the record against a replay through current specs. **Behind.**

### Two goals, and only one of them needs this

**Teardown exactness.** A reverse must remove what its forward delivered, which means
knowing the extent of it — how many queue rows, how many address families. This does
**not** need applied state: a reverse clears the namespace it owns instead of
recomputing its extent, which needs only the resource's identity and the bound the
schema already validates every write against (`DESIGN_PRINCIPLES.md` §15). Design B is
not required for it, and should not be justified by it.

**The three-way diff.** This is the goal Design B exists for, and its cost is a
closure: the recorded set must cover every spec-derived value a generator consumes.
It does not today. A QoS policy's queue count reaches `QUEUE` rows without ever
entering a record — the count is read from the spec and consumed directly by the
generator. Every such value would have to be recorded for `Applied` to be complete,
and an incomplete `Applied` is the rung-0a mistake again: a proxy presented as the
quantity.

That closure is bounded — per operation, by the number of spec-derived inputs its
generators read — and constant over a device's lifetime, so **§23 is not the
obstacle**. The obstacle is §21's grain: each value recorded is a value no longer
re-derived, and therefore one more place where the record and the specs can disagree
with nothing to notice it.

### What it would take

- **Carry applied values through reconstruction.** `RebuildProjectionFromIntents`
  starts from an empty intent DB and repopulates it by replay, so the values cannot be
  read back from the live record — they must travel in the exported step. The export
  drops recorded params today, deliberately.
- **Teach `writeIntent` the difference.** It replaces a record wholesale (DEL+HSET,
  #228) so that dropped params do not orphan. It would need to distinguish values
  computed now from values carried forward, and the manifest's `caller`/`recorded`
  split is not that distinction — see "Why replay cannot produce `Applied`".
- **Re-stamp on reconcile.** Reconcile is the moment the device matches the
  projection; without a re-stamp the record stays behind the device it describes, and
  the next comparison reports a divergence the reconcile just resolved.
- **Decide what a partial delivery stamps.** `ChangeSet.Apply` is a sequential
  per-entry loop outside the composite path, so a ChangeSet that applies some entries
  and fails on others is reachable.

### Relationship to Design A

They are alternatives, not stages. A stamped record answers Q1–Q3 per field, which is
everything the digest answers and more, so building the digest first would be work to
discard. The digest's advantage is entirely its size: one stored value, one
classifier, one guard branch, and no change to how records are written.

Design B also classifies a newtron release correctly with no extra mechanism.
`Applied` is what was delivered; `Current` is a replay through today's specs and
today's code. A rendering change moves `Current` and leaves `Applied` alone, so it
reads as Behind, never as Drifted — the limitation Design A needs a build identity
to work around does not arise.

## Design C — a digest of each intent's inputs

Design A answers per device and Design B per field. Between them sits a question
neither answers cheaply and an operator asks first: **which of this device's
operations are behind?** Not "the switch drifted", not "these forty fields
differ", but "TRANSIT on Ethernet0 and Ethernet4 is behind its spec".

Stamp each intent record, when it is written, with a digest of the inputs that
determine what it renders. When the guard finds drift, recompute each device
record's digest from the inputs as they stand now and compare:

| Stored vs current digest | Drift on the device | Meaning | Resolution |
|---|---|---|---|
| same for every record | non-empty | **Drifted** — the device changed after delivery | reconcile |
| differs for some records | non-empty | **Behind** — those records' inputs moved | reconcile applies the spec changes |
| missing on some records, none differs | non-empty | **Unclassified** — those records predate stamping | reconcile, which stamps them |
| any | empty | current — some inputs may have moved without changing any row | proceed |

It records no applied *values*, so Design B's closure — every spec-derived value a
generator consumes — does not arise, and neither does §21's grain: the digest is
never fed to a generator, so it cannot disagree with the specs in a way that changes
what is rendered. It needs four things, and each is a way to get a confidently wrong
answer if missed.

1. **The inputs are a function of the record, declared once and proved by test.** A
   service intent depends on its service, and through it on a filter, prefix lists,
   route policies, a QoS policy, IP-VPN and MAC-VPN specs — each possibly overridden at
   the zone or the node — and on node values: loopback, ASN, the EVPN peers derived from
   other nodes' specs and the topology, the platform. A digest of the service spec
   alone misses an edit to its filter. Two existing declarations give the transitive
   set without a per-operation list: the operation registry declares which of a
   record's params names a spec, and the `ref:"…"` tags declare every spec-to-spec
   reference (`spec/references.go`). Node values are digested whole — coarse, since a
   loopback change then marks every intent on the node, but never wrong in the safe
   direction.

   Collecting what an operation reads at run time looks simpler and is wrong. A
   composite `apply-service` writes `vlan|300`; a rebuild re-creates `vlan|300`
   through its own `create-vlan` step. One record would get two answers, one per
   path, and every composite-created record would read as behind forever. A function
   of the record gives one answer on every path. What run-time reads are good for is
   checking the declaration: a test replays every registered operation and fails if a
   spec is read that no record's declared inputs cover.
2. **newtron's build identity is part of the digest.** Otherwise a release that
   renders the same specs differently (the second cause of "behind", above) reads as
   "same digest, drift present" and is reported as drift. That requires every
   rendering-changing release to carry a distinct identity.
3. **The stamp is written with the record and read back from the device.** The one
   writer of intent records stamps each record it writes, on the live path and in
   replay alike. The device keeps the stamp from the record's last delivery. The guard
   compares the device's stored stamps — never the rebuilt records, which a rebuild
   re-stamps from current inputs and so always match. Nothing has to be carried
   through reconstruction.
4. **Drift rows are not attributed to intents.** The projection does not record
   which intent produced a row — that is the resolution-provenance question, deferred.
   The classification is therefore per device: it names the records whose inputs
   moved, and reports "behind" when any did. The service projection's technique —
   remove one intent, replay, diff — could attribute rows later, at the cost of a
   replay per intent examined.

The compound case Design A accepts persists here: an input that moved **and** a hand
edit on the same device classify as Behind, and the reconcile overwrites the edit —
safe, for the same reason.

**Relationship to A and B.** Design C is the "per-spec digest set" Design A names as
a refinement, moved from the device onto each intent and made to cover what an
intent's rendering depends on. It answers which operations are behind but not which
values changed; the drift entries supply the values. It is an alternative to both,
not a stage between them: building it and then B would discard it, as building A and
then B would.

## Design C — implementation plan

Nothing here is built; the plan waits on the trigger named in "Assessment". Its
file, function and line references were checked against main when it was written.

### What it delivers

When the drift guard refuses a write, the refusal says why, as a typed **409**
instead of today's untyped error (which `httpStatusFromError` maps to 500):

- **drifted** — every record's stored digest equals its current one, so the device
  changed after delivery. That is a hand edit, or a delivery that failed part-way
  (a commit that stopped between a config delete and its intent delete leaves the
  same signal). The message does not claim which.
- **behind** — these named records' inputs (specs, node values, or the build) moved
  since they were written. Reconcile applies the change.
- **unclassified** — some device records carry no digest (written before this
  lands, or left by an interrupted write), so the cause cannot be told.

Node status gains the list of behind records. The guard still refuses in every
case; nothing about when it refuses changes.

The resolution is `reconcile`, not a per-service refresh: `RefreshService` runs
through `Execute` and therefore through the same guard.

### The digest

`inputDigest(operation, params, view)` — one pure function, in a new
`network/node/input_digest.go`:

```
SHA-256( build ‖ node values ‖ for each spec in the closure, sorted: kind, name, JSON(value) or null )
```

truncated to 16 hex characters.

- **Build** — `version.Version` and `version.GitCommit`. A plain `go build` leaves
  both at `dev`/`unknown`, so a rendering change between two such builds reads as
  drifted, as it does today; Makefile builds carry a distinct identity.
- **Node values** — the resolved node spec except `SSHUser`/`SSHPass`, plus the
  node's platform spec with `Credentials` removed (credentials are plaintext after
  secret resolution, and the node package reads the platform through
  `GetPlatform(resolved.Platform)` in `evpn_ops.go` and `service_ops.go`). A
  reflection test fails when a `ResolvedNodeSpec` field is added without being
  classified as included or excluded.
- **Closure** — start from the record's params that name a spec, then follow every
  `ref:` tag in each spec reached (`CollectRefs`). A missing spec contributes `null`,
  so a record whose spec was deleted reads as behind with no separate orphan
  handling.
- `encoding/json` sorts map keys; no spec type in the closure has unexported or
  `json:"-"` fields, so the encoding is complete and deterministic.
- A view never changes within an operation, so each spec's encoding and the node
  values are hashed once per view and reused by every record a rebuild writes.

**Declarations.** `ParamSpec` in `op_registry.go` gains the kind of spec a param
names, using the `kind:` vocabulary of `OverridableSpecs`. Five params name a spec:

| Operation | Param | Kind |
|---|---|---|
| `apply-service` | `service_name` | `ServiceSpec` |
| `bind-macvpn` | `macvpn` | `MACVPNSpec` |
| `bind-ipvpn` | `ipvpn` | `IPVPNSpec` |
| `create-acl` | `filter` | `FilterSpec` |
| `bind-qos` | `policy` | `QoSPolicy` |

Every other spec lookup in the node package is reached through these by a `ref:` tag
(a service's IP-VPN, MAC-VPN, filters, QoS policy, route policies, prefix lists; a
filter rule's or route-policy rule's prefix lists) or is the platform. The lookups
were enumerated: `vrf_ops.go:136`, `qos_ops.go:73,195,326`, `evpn_ops.go:33,44`,
`service_ops.go:208–1200`, `op_registry.go:295`. One crosses records:
`bindMemberQoS` renders a member port's QoS from the irb binding's policy, which the
binding's closure covers. `FindMACVPNByVNI` has no production caller.

**Spec lookup by kind.** The closure walk looks specs up by `(kind, name)`. That
lookup belongs to `OverridableSpecs`, which already owns the kind vocabulary (its
`kind:` tags and `EachSpec`); `ResolvedSpecs` holds its merged view as one, and
exposes the lookup through `SpecProvider`. The unit-test provider gains the same
method.

### Where it is stamped

In `writeIntent`, the only writer of intent records (`intent_ops.go`): the record's
fields gain `input_digest = inputDigest(op, params, view)` before the change is
queued and rendered. That one line covers every delivery:

- **Live writes** — the stamp travels in the same ChangeSet as the record. It is
  truthful: the guard ran on this operation with an empty diff (or there were no
  intents yet), so after the commit every row the record owns is what its current
  inputs render.
- **Reconcile** — full and delta export the rebuilt in-memory records, which replay
  wrote through `writeIntent`, so they carry current digests.

The field is not added to `Intent` or to `ToFields`. Parent registration rewrites a
parent from `ToFields()` (an HSET merge), so leaving the field out preserves the
parent's stamp on the device and in memory; putting it in would rewrite the old
stamp in the same commit that might re-stamp it. `NewIntent` strips it from params
(an identity key), so it never reaches a topology step or `topology.json`.

**Records the operation did not write keep their old stamp**, even when their inputs
moved without changing any row. A later hand edit on such a device is then reported
as behind — over-reporting in the safe direction, cleared by the next reconcile or
write of that record. Re-stamping them would cost every write a read of the
device's intent records and a write per stale record; the plan does not pay for it.

**Delta reconcile** delivers intent records before `ApplyDrift`, as today. If
`ApplyDrift` then fails, current stamps sit over unpatched rows and a later refusal
says "drifted"; the failed reconcile already reported its error, and the "drifted"
message names an incomplete delivery as a possible cause.

### The guard

`Lock` keeps its diff. When the diff is non-empty it reads the device's
`NEWTRON_INTENT` (one read, on the refusal path only) and returns
`classifyDrift(entries, deviceRecords, digestFn)` — a pure function, unit-testable
without a device:

- `behind` = device records whose stored digest is set and differs from
  `inputDigest` of that record now; sorted. `unstamped` = records with no digest.
- `behind` non-empty → cause **behind**: *"device is behind its specs: N intent(s)
  changed inputs since delivery (first five, then "and K more") — M entries differ;
  reconcile to apply the spec changes"*.
- else `unstamped > 0` → cause **unclassified**, today's message.
- else → cause **drifted**, today's message unchanged — the drift suite asserts its
  substring (`2node-vs-drift-actuated/08-verify-guard.yaml`).

`util.DriftError{Device, Entries, Cause, Behind, Unstamped}`, aliased in
`pkg/newtron/types.go` like `ConflictError`; `httpStatusFromError` maps it to 409 and
`writeError` puts it in the response's `data`, as it does for `AuthorizationError`.

**Node status** gains `behind_intents`, filled after the existing rebuild and drift
read when the node is actuated and the diff is non-empty, through one public
delegating method so the guard and status share one classifier.

### Other changes

- `intent snapshot-diff` and newtrun's `verify-snapshot` (`DiffIntentRecords`)
  ignore `input_digest`. The snapshot catches residual or missing intent *content*;
  without this, a newtron upgrade followed by one write would report every record
  in a saved baseline as changed. The field name is one exported constant in
  `util/intent_diff.go`, used by both `DiffIntentRecords` and `intentIdentityFields`.
- The NEWTRON_INTENT schema lists the field explicitly (`schema.go`,
  `yang/constraints.md`, `schema_test.go`), although the table allows extra fields.
- Write results include the stamp in each written record's fields; no extra changes
  are emitted.

### Tests

Each is watched failing against a deliberately broken variant before it counts.

| Test | Proves | Fails against |
|---|---|---|
| Read coverage | Replaying the round-trip sequence reads no spec outside the union of the records' declared closures, and every spec kind the node package looks up is exercised (non-vacuous) | a removed param declaration |
| Sensitivity | Editing a spec the fixture uses changes the digest of exactly the records whose closure holds it; a loopback or build change moves every record | a closure that skips `ref:` tags |
| Determinism | Twenty rebuilds of the fixture give identical digests | unsorted closure |
| Field classification | Every `ResolvedNodeSpec` field is classified; SSH and platform credentials never move the digest | a new unclassified field |
| `TestOpRoundTrip` | Live and replayed records carry equal digests, composites included (the digest key joins its envelope fields) | digesting the live operation's reads |
| `classifyDrift` | drifted, behind, unclassified (alone and alongside behind) | — |
| API | `DriftError` → 409 with `data` | — |
| Snapshot diff | Records differing only in the digest compare equal; a params difference still shows | — |
| Budget | `TestRebuildProjectionBudget` with service-bearing intents, so the closure walk is measured | — |

The round-trip fixture needs a service whose routing names route policies and prefix
lists, so the coverage test reaches those lookups.

### Suites (cold)

- A new scenario in `2node-vs-drift-actuated`, after `12-verify-unblocked`:
  `update-macvpn` on `EXTEND_VLAN300` with its full body but `arp_suppression: false`
  (it replaces the whole definition and nothing refuses an in-use macvpn; `BindMACVPN`
  renders `SUPPRESS_VLAN_NEIGH` from it) → a write on the switch carrying `EBRD` is
  refused with *"behind its specs"* → node status lists behind records → reconcile →
  the write succeeds → restore the macvpn → reconcile → drift empty for
  `14-teardown`.
- Re-run: both drift suites; 2node-vs/ngdp-primitive and 2node-vs-service (snapshot
  and reconcile-provision paths); 2node-ngdp-service; 1node-vs-config (loopback);
  1node-vs-basic and 1node-vs-architecture (they read write results).

### Documents changed with it

This document's status; `DESIGN_PRINCIPLES_NEWTRON.md` (the guard and the
"Reconstruction and device state" gap); `unified-pipeline-architecture.md`;
`device-lld.md`, `hld.md`, `lld.md`; `api.md` (409, payload, `behind_intents`, the
field); CLAUDE.md's summary row; a full-file audit afterwards. newtcon is told in the
same session: the status code (already announced as coming), the payload, the
status field, the record field.

### Size

About 200–250 production lines: the digest and closure walk, the kind lookup, five
registry declarations, one line in `writeIntent`, the classifier and its error type,
the API mapping and status field. No per-operation code changes.

### Limitations accepted

- Node values are coarse: a loopback change marks every record behind.
- Rendering-neutral spec fields (descriptions) mark records behind until they are
  next written or reconciled.
- A drift-excluded change (DEVICE_METADATA, PORT) is invisible to the guard, as it is
  today.

## Cost

| | Rung 0a (built, removed) | Design A (digest) | Design B (stamping) | Design C (per-intent digest) |
|---|---|---|---|---|
| New API endpoints | 1 | 0 | 0 | 0 |
| New CLI verbs | 1 | 0 | 0 | 0 |
| New public types | 2 | 0 (a field on an existing response) | 0 (fields on an existing response) | 0 (fields on an existing response) |
| New client methods | 1 | 0 | 0 | 0 |
| Internal machinery | ~150 lines + test | one stored value, one classifier, one guard branch | a change to how every record is written, plus the recorded-set closure | one digest function over declared inputs, one stamp in the intent writer, one classifier (~200–250 lines) |
| Behaviour changed | none | the write-guard defect is fixed | the write-guard defect is fixed | the write-guard defect is fixed |
| Question answered | recorded params its replay recomputed | is this device current, and if not, why | which fields differ, and whether the device or the spec moved | which operations are behind |
| Granularity | partial, per param | per device | per field | per intent |

## Assessment: bloat, or better architecture?

**Better architecture, and by a specific measure: it removes a meaning rather than
adding a verb.**

Newtron exposes five comparison reads — `drift`, `projection-diff`, `snapshot`,
`snapshot-diff`, plus reconcile's delta. Adding a sixth to answer "behind" would
be bloat, and that is precisely what rung 0a did. None of the designs here adds one:
both take the comparison operators already use and make it say which of two things
it found.

The coherence argument is stronger than the feature argument, and it holds for
any of the designs:

- **"Drift" becomes a single concept again.** Today it means "the device diverged
  from expectations" where expectations silently include spec edits. Afterward it
  means "the device was changed outside newtron" — one cause, one remedy. An
  overloaded term is a §13 problem (same concept, same name) and this retires it.
- **It restores a principle the code currently contradicts.** "The device is the
  source of reality; specs are intent" implies that an intent edit and a reality
  edit are different events. The guard treats them identically. Naming them apart
  brings the implementation back to the thesis.
- **It makes the system more predictable, not more capable.** The operator-visible
  change is that spec authoring stops producing a false accusation against a
  device and stops silently freezing the fleet.

The honest counterweight: the *entire* operational benefit could be approximated
by improving the guard's error message to hedge ("device diverged — a spec change
or a device edit"). That is nearly free and dishonest in a smaller way: it admits
the system cannot tell, and leaves the write-freeze in place. Design A is the
smallest thing that lets the system actually know.

**Between the designs.** Design A is the right first build if the guard's
behaviour is the only thing that matters: it is small, it changes no record-writing
path, and its coarse network digest never produces a false "behind" because the
classification requires drift to be non-empty. Design C is the right build if the
operator's question is "which operations are behind?" — it names the operations, and
it is the only design that does so without recording applied values. Design B is the
right build if the per-field question is being asked — "which fields will a refresh
change?" — or if applied state is wanted for its own sake. Each is an alternative;
building one and then another discards the first. None is required for teardown
exactness (see Design B, "Two goals").

**Recommendation: build none until a spec edit against a live fleet is an
operation someone actually performs.** The defect is real but latent — it bites
whoever edits a spec while devices are provisioned. Until then, the gap is recorded
in "Reconstruction and device state", and this document is the plan. Deferring is consistent
with how this project has treated speculative enhancements (resolution provenance,
saga): the design is written down, the trigger is named, the code is not written.

**Trigger to build:** the first time a spec edit on a provisioned network produces
the "device drifted from intents" refusal in an operator's workflow.

## Three questions, three mechanisms

The three are distinct, and conflating them is how rung 0a shipped a proxy for the
quantity it named:

| Question | Mechanism | Status |
|---|---|---|
| Is this device current with its specs? | a stored digest of the spec directory (Design A) | proposed |
| *Which operations* are behind? | a digest of each intent's declared inputs, stamped when the record is written (Design C) | proposed; implementation planned |
| *Which* fields differ, and did the device or the spec move? | applied values stamped at delivery (Design B) | proposed |
| *Why* does this field differ — which spec field produced it? | ChangeSet entries tagged with the spec field that produced them | deferred (resolution provenance) |

The rows are ordered by refinement, not by dependency — a later one does not need an
earlier one built. Nor does any of them imply the next.
Design B does not require provenance: comparing values tells the operator what will
change without attributing the cause. This document should not be used to justify
provenance, and provenance should not be used to justify any design here. Design C
as planned names records without attributing rows to them; attributing rows is a
narrower question than provenance (which spec *field* produced a row) and is
answerable by replay without it.
