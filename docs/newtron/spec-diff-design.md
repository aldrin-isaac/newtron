# Spec-Diff: Separating "Behind" From "Drifted"

Status: **proposed** — justification, design, and decision record. No code implements
this today. A partial earlier attempt (#486 rung 0a) was built and removed; the
reasons are recorded in "What the first attempt got wrong."

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
semantics, and `DESIGN_PRINCIPLES_NEWTRON.md` §21 already records it as a known
tension.

## The comparison, stated precisely

Three states exist:

1. **Applied** — what this device was configured with, at the time it was configured.
2. **Actual** — the device's CONFIG_DB right now.
3. **Current** — what today's specs would produce for this device.

Newtron computes only one comparison, `Current ↔ Actual`, and calls the result
drift. The two causes separate cleanly only if `Applied` is available:

- `Applied ↔ Actual` — someone changed the device. **True drift.**
- `Applied ↔ Current` — someone changed a spec. **Behind.**

## Why the obvious implementation is not available

`Applied` cannot be reconstructed from the intent DB. This is a deliberate
property of the intent model, not an oversight:

- **Spec-derived values are re-derived, not stored** (§20 round-trip completeness:
  "values re-resolved from specs at replay time are re-derived, not stored"). An
  intent records the *decision* — "this interface binds MAC-VPN SERVERS" — not the
  derivation — "…whose VNI is 10200."
- The exception is `recorded()` params, frozen only where teardown needs
  self-sufficiency (§20). That set is small and exists for a different purpose.
- **There is no spec history.** §23 bounds CONFIG_DB footprint: a device that has
  run 50,000 operations carries the same footprint as one that has run 11. Storing
  past spec versions to replay against would violate that directly.

So replaying intents can only ever produce `Current`. The past is not recoverable
by replay, and making it recoverable means either recording every derived value
(§21 violation, unbounded field growth) or keeping spec history (§23 violation).

**This is the finding that shapes the design: any approach that tries to
reconstruct `Applied` per-field is fighting two principles at once.**

## What the first attempt got wrong

#486 rung 0a compared the intent DB before and after a replay: snapshot the
device's records, replay them through current specs, diff the two record sets. It
shipped as `Node.SpecDiff`, `GET …/intent/spec-diff`, a CLI verb, two public types
and a client method.

It could only ever see `recorded()` params — the small frozen set — because those
are the only spec-derived values written back into a record. A spec change that
altered CONFIG_DB through a non-recorded derivation was invisible to it. It
therefore answered a narrower question than its name implied, while presenting as
the general one.

It also changed no behaviour. The guard still froze writes on a spec edit, still
with the wrong message. The read existed; the thing that made the read worth
having did not.

Both are recorded here because the shape of the mistake is reusable: a diagnostic
that reports a *proxy* for the quantity of interest, shipped as though it reported
the quantity.

## Design

Accept that `Applied` is not recoverable per-field, and observe that it does not
need to be. The operator's questions are answerable at device granularity:

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

## Cost

| | Rung 0a (built, removed) | This design |
|---|---|---|
| New API endpoints | 1 | 0 |
| New CLI verbs | 1 | 0 |
| New public types | 2 | 0 (one field on an existing response) |
| New client methods | 1 | 0 |
| Internal machinery | ~150 lines + test | one stored value, one classifier, one guard branch |
| Behaviour changed | none | the write-guard defect is fixed |
| Question answered | recorded params that moved | is this device current, and if not, why |

## Assessment: bloat, or better architecture?

**Better architecture, and by a specific measure: it removes a meaning rather than
adding a verb.**

Newtron exposes five comparison reads — `drift`, `projection-diff`, `snapshot`,
`snapshot-diff`, plus reconcile's delta. Adding a sixth to answer "behind" would
be bloat, and that is precisely what rung 0a did. This design adds none. It takes
the one comparison operators already use and makes it say which of two things it
found.

The coherence argument is stronger than the feature argument:

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
the system cannot tell, and leaves the write-freeze in place. The digest is the
smallest thing that lets the system actually know.

**Recommendation: build it only when a spec edit against a live fleet is an
operation someone actually performs.** The defect is real but latent — it bites
whoever edits a spec while devices are provisioned. Until then, the tension is
correctly recorded in §21, and this document is the plan. Deferring is consistent
with how this project has treated speculative enhancements (resolution provenance,
saga): the design is written down, the trigger is named, the code is not written.

**Trigger to build:** the first time a spec edit on a provisioned network produces
the "device drifted from intents" refusal in an operator's workflow.

## Relationship to resolution provenance

A per-field answer — "*this* CONFIG_DB field differs because *that* spec field
changed" — requires tagging ChangeSet entries with the spec field that produced
them. That is the deferred resolution-provenance enhancement, and it is a strictly
larger undertaking than this design. It is not required for any of Q1–Q3, and
this design should not be used to justify it.
