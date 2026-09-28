# Spec-Diff: Separating "Behind" From "Drifted"

Status: **proposed** — justification, two candidate designs, and decision record.
No code implements either today. A partial earlier attempt (#486 rung 0a) was built
and removed; the reasons are recorded in "What the first attempt got wrong."

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
3. **Current** — what today's specs would produce for this device.

Newtron computes only one comparison, `Current ↔ Actual`, and calls the result
drift. The two causes separate cleanly only if `Applied` is available:

- `Applied ↔ Actual` — someone changed the device. **True drift.**
- `Applied ↔ Current` — someone changed a spec. **Behind.**

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

**The consequence that shapes both designs below:** replay cannot produce `Applied`,
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

## Cost

| | Rung 0a (built, removed) | Design A (digest) | Design B (stamping) |
|---|---|---|---|
| New API endpoints | 1 | 0 | 0 |
| New CLI verbs | 1 | 0 | 0 |
| New public types | 2 | 0 (a field on an existing response) | 0 (fields on an existing response) |
| New client methods | 1 | 0 | 0 |
| Internal machinery | ~150 lines + test | one stored value, one classifier, one guard branch | a change to how every record is written, plus the recorded-set closure |
| Behaviour changed | none | the write-guard defect is fixed | the write-guard defect is fixed |
| Question answered | recorded params its replay recomputed | is this device current, and if not, why | which fields differ, and whether the device or the spec moved |
| Granularity | partial, per param | per device | per field |

## Assessment: bloat, or better architecture?

**Better architecture, and by a specific measure: it removes a meaning rather than
adding a verb.**

Newtron exposes five comparison reads — `drift`, `projection-diff`, `snapshot`,
`snapshot-diff`, plus reconcile's delta. Adding a sixth to answer "behind" would
be bloat, and that is precisely what rung 0a did. Neither design here adds one:
both take the comparison operators already use and make it say which of two things
it found.

The coherence argument is stronger than the feature argument, and it holds for
either design:

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

**Between the two designs.** Design A is the right first build if the guard's
behaviour is the only thing that matters: it is small, it changes no record-writing
path, and its coarse network digest never produces a false "behind" because the
classification requires drift to be non-empty. Design B is the right build if the
per-field question is being asked — "which fields will a refresh change?" — or if
applied state is wanted for its own sake. Building A and then B means discarding A.
Neither is required for teardown exactness (see Design B, "Two goals").

**Recommendation: build neither until a spec edit against a live fleet is an
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
| *Which* fields differ, and did the device or the spec move? | applied values stamped at delivery (Design B) | proposed |
| *Why* does this field differ — which spec field produced it? | ChangeSet entries tagged with the spec field that produced them | deferred (resolution provenance) |

The rows are ordered by refinement, not by dependency — a later one does not need an
earlier one built. Nor does any of them imply the next.
Design B does not require provenance: comparing values tells the operator what will
change without attributing the cause. This document should not be used to justify
provenance, and provenance should not be used to justify either design here.
