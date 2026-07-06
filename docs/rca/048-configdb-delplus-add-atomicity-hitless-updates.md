# RCA-048: CONFIG_DB row-replace (DEL+ADD) is non-atomic in the per-operation apply path — a race window that can flap SONiC daemons

**Severity**: Medium — a latent availability risk in hitless-update claims, not a live regression on an idle lab.
**Platform**: sonic-vs (Force10-S6000, 202505 stable) — measured. Mechanism applies to any SONiC config daemon.
**Status**: **Fixed** (2026-07-04) — the four `update-*` verbs now deliver a
field-level diff via `cs.Replace` (§48): `HSET` changed/added fields, `HDEL` only
removed ones, **never `DEL` the key**. No delete window, so no race. The
continuity-check scenario `34-dataplane-update-bgp-peer.yaml` passes green on the
actuated `2node-vs` lab (below). The rejected alternative — a blanket atomic
same-key DEL+ADD in `ChangeSet.Apply` — is documented under *What must be fixed*
and deliberately not taken (it would strip `RefreshService`'s teardown
observability).

## Summary

Newtron's `update-*` verbs (#227: `update-acl-rule`, `update-static-route`,
`update-bgp-peer`, `update-bgp-evpn-peer`) replace a CONFIG_DB row with a `DEL`
of the old row followed by an `HSET` of the new one (`cs.Deletes(...)` +
`cs.Adds(...)`; the §"CONFIG_DB Replace Semantics" pattern chosen to avoid ghost
fields). `ChangeSet.Apply` sends those to Redis as **two separate round-trips** —
`client.Del(key).Result()` then `client.HSet(key, fields).Result()` — **not** a
single `MULTI/EXEC`. (The atomic `PipelineSet`/`TxPipeline` exists, but is used
only by the **drift-reconcile** path, `ApplyDrift` — not by the per-operation
verbs.)

Between those two round-trips there is a brief window where the key is **absent**.
SONiC config daemons react to a keyspace notification by **re-reading the key's
current state**; a daemon whose async handler runs in that window sees the key
gone and **tears state down** — a BGP session flap (`no neighbor …`), a FIB gap,
an ACL leak/deny window. Whether that happens is a **race against the sub-
millisecond DEL→HSET gap**:

- On an idle lab the window is small enough that it reliably wins: an ESTABLISHED
  underlay eBGP session held across **20/20** `update-bgp-peer` field changes
  (uptime climbed 13s → 472s, never reset).
- Widen the window and it reliably loses: the *same* DEL+ADD issued as two
  separate Redis transactions with a 1s gap flapped the session (uptime 555s → 5s).

So the `update-*` verbs are **hitless on a stable, steady-state session but not
provably so, and they reliably flap a freshly-established one** — the "closes the
blip" guarantee rests on a timing race that a young session (still negotiating
AFI/SAFI) loses. This is not a corner case: it is exactly the provision-then-tweak
sequence.

## Symptom

Same key, same ESTABLISHED session, differing only in the DEL→HSET gap:

| Application of the DEL+ADD | BGP session |
|---|---|
| `update-bgp-peer` — DEL then HSET back-to-back (sub-ms), ×20 | ESTABLISHED throughout, 0 flaps |
| DEL then HSET as two transactions with a 1s gap | flap: `Last reset … No AFI/SAFI activated for peer`, uptime → ~5s |

## Root Cause

`ChangeSet.Apply` (`pkg/newtron/network/node/changeset.go`) iterates the
ChangeSet and issues one synchronous Redis command per change:

```go
for _, change := range cs.Changes {
    case ChangeTypeDelete: client.DeleteWithReply(...)  // client.Del(key).Result()
    case ChangeTypeAdd/Modify: client.SetWithReply(...) // client.HSet(key, ...).Result()
}
```

A same-key DEL+ADD is therefore `Del(key)` **round-trip completes**, then a
separate `HSet(key, …)` — with the key momentarily deleted in between. SONiC
daemons (frrcfgd/bgpcfgd, fpmsyncd, aclorch) use the `ConfigDBConnector`
notification-then-re-read pattern (see RCA-045), so a daemon that reads the key
during the gap observes a **delete** and tears down. The gap is normally far
shorter than the daemon's notification-dispatch latency, which is why it usually
goes unnoticed — but it is a race, not a guarantee.

## What must be fixed to update SONiC without availability hits

The invariant for a hitless CONFIG_DB row-replace: **a SONiC daemon must never
observe the key in a deleted state.** The fix is DESIGN_PRINCIPLES_NEWTRON §48
(In-Place Update Is Delivered In Place): **`update-*` is delivered as a
field-level diff (`cs.Replace`) — `HSET` the changed/added fields, `HDEL` only
the removed ones, never `DEL` the key.** No `DEL` means no window at all, it
still avoids the ghost-field problem the whole-row DEL was chosen for, and it
leaves unchanged sibling rows (e.g. `BGP_NEIGHBOR_AF`) untouched — which also
removes the "No AFI/SAFI activated" reset a full re-create causes.

**A blanket "make same-key DEL+ADD atomic in `ChangeSet.Apply`" is the wrong
fix and is rejected.** `RefreshService` *relies* on the daemon observing the
DELETE to clean up its internal state (§48; §11 replace-semantics); making all
DEL+ADD atomic would silently strip that observability and strand daemon state.
The intent (edit vs teardown) must live in the caller's verb, not an apply-layer
heuristic. `RefreshService` keeps `cs.Deletes`+`cs.Adds`; `update-*` switches to
`cs.Replace`. (An atomic same-key `MULTI/EXEC` would close the window too, but it
cannot be applied blanketly for that reason, and it needlessly re-creates
unchanged sibling rows — the field diff is both safer and more surgical.)

Additional invariant, independent of the above: **the change must not be
inherently session-affecting.** A `remote-as` change renegotiates a BGP session
regardless of how it is written — that is BGP semantics, not a fixable blip. A
hitless update path only closes the *spurious* blip (cosmetic/attribute changes).

Until one of these lands, the accurate statement is: `update-*` verbs are
hitless in practice on an unloaded device, but a busy device (higher daemon
scheduling latency, or contention widening the DEL→HSET gap) can flap. The
drift-reconcile path (`ApplyDrift` → `PipelineSet`) is already atomic and is not
affected.

## Affected Platforms

- **Measured**: sonic-vs Force10-S6000, 202505 stable, underlay eBGP switch1↔switch2.
- **Mechanism**: any SONiC config daemon consuming a table via `ConfigDBConnector`
  (notification-then-re-read) — frrcfgd/bgpcfgd (BGP_NEIGHBOR), fpmsyncd
  (STATIC_ROUTE), aclorch (ACL_RULE). CiscoVS not separately measured.

## Related

- **#227** — the `update-*` verbs whose hitless claim this qualifies.
- **RCA-045** — frrcfgd notification handling (re-read on notification); see the
  re-verification note there (runtime field updates reach FRR without a restart
  on this image).
- **DESIGN_PRINCIPLES_NEWTRON.md §48** (In-Place Update Is Delivered In Place) —
  the principle this RCA validates; **CLAUDE.md §"CONFIG_DB Replace Semantics —
  Teardown vs In-Place"** — the mechanics (`cs.Replace` field diff vs teardown
  `DEL`+`HSET`).
- `pkg/newtron/network/node/changeset.go` (`ChangeSet.Replace` / `Apply`'s
  `ChangeTypeReplace` case — the in-place field-diff path the fix adds) vs
  `pkg/newtron/device/sonic/pipeline.go` (`PipelineSet` — the atomic path used by
  drift-reconcile).

## Validation

Measured on the deployed `2node-vs` lab, switch1↔switch2 underlay eBGP session
(neighbor 10.1.0.1), 2026-07-04:

- **Per-op `update-bgp-peer` on a STABLE session** (DEL+HSET back-to-back): 20
  consecutive field changes on a session up 60s–470s — ESTABLISHED throughout,
  uptime climbed, **0 flaps** (the race won every time).
- **Per-op `update-bgp-peer` on a FRESH session** (~7s uptime, still settling
  AFI/SAFI right after bring-up): **flapped** — the continuity-check scenario
  (`34-dataplane-update-bgp-peer.yaml`) captured uptime-before = 7000ms and the
  after-uptime did not exceed it (reset). The race **reliably loses** while a
  session is young. This is the practically-important case: provision a device
  and immediately adjust a BGP peer and the session drops.
- **DEL then HSET with a 1s gap** (deliberately widened window): flap on the first
  run (555s → 5s), reliably.
- Same key, same session — the variables are the DEL→HSET gap and the session's
  maturity. Session maturity matters because a young session's AFI/SAFI is still
  negotiating, so it is more sensitive to the brief neighbor-absent window.

**Post-fix (2026-07-04, `cs.Replace` field diff):** the same continuity-check
scenario, run through the full `boot-ssh → setup-device → bgp-underlay →
dataplane-update-bgp-peer` chain on a freshly provisioned `2node-vs` lab, **passes
green**: after a 30s settle the in-place `description` change is delivered as a
pure `HSET` merge (no `DEL`, no removed fields), the underlay session stays
ESTABLISHED, and its uptime keeps climbing past the captured pre-update value (no
reset). The unit substrate confirms the delivery shape: the `update-*` ChangeSets
now emit a single `[replace]` change and **no `[delete]` of the key**
(`assertNoChangeOfType(…, ChangeDelete)` in `device_ops_test.go` /
`interface_ops_test.go`). What was a race the young session lost is now a
structural guarantee — there is no window to lose.

**Methodology note:** two prior conclusions were corrected by re-verification
(per the troubleshooting methodology — verify, repeat, read the code): first an
over-hasty "the verb flaps → claim refuted" (a false alarm on a session already
destabilized by an unrelated frrcfgd restart); then "the verb is atomic via
PipelineSet → guaranteed hitless" (wrong — `ChangeSet.Apply` uses individual
`Del`/`HSet`, not `PipelineSet`). The measured behavior plus the apply-path code
settle it: practically hitless on an idle device, a race window in principle,
fixable by atomic apply or a field-level diff.
