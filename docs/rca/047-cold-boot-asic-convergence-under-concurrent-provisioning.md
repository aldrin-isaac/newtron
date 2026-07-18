# RCA-047: Cold-boot ASIC convergence races the test poll under concurrent provisioning

## Summary

On a freshly-deployed lab, the `bridged` scenario of `2node-vs-primitive`
(and `2node-ngdp-primitive`) intermittently fails at
`poll-asic-vlanmember-ready`:

```
jq assertion failed: expression ".output | ... | tonumber >= 2" evaluated to false
```

The CONFIG_DB, APP_DB, and STATE_DB all carry the three VLAN_MEMBER
entries, but `ASIC_DB:SAI_OBJECT_TYPE_VLAN_MEMBER` has 0–1 when the
60s poll budget expires. The SAI objects *do* get programmed — just
later than 60s under this specific load. It is a test-timing race,
not a SONiC defect or a newtron regression.

## Symptom

Cold-deploy `2node-vs`, run `2node-vs-primitive`:

```
PASS    1m31s boot-ssh
PASS       9s setup-device
FAIL     1m3s bridged          ← poll-asic-vlanmember-ready, 60s budget
  ...
SKIP        evpn-bridged / irb / teardown-local / ...   (cascade — depend on bridged)
```

Inspecting switch1 immediately after the failure:

```
CONFIG_DB  VLAN_MEMBER|Vlan100|Ethernet4,8,12   ← present (3)
APP_DB     VLAN_MEMBER_TABLE:Vlan100:...        ← present (3)
STATE_DB   VLAN_MEMBER_TABLE|Vlan100|...        ← present (3)
ASIC_DB    SAI_OBJECT_TYPE_VLAN_MEMBER          ← 0
```

STATE_DB present + ASIC_DB absent means vlanmgrd finished its work
(kernel bridge + STATE_DB), but orchagent had not yet created the
SAI VLAN_MEMBER objects.

## Root Cause

**orchagent SAI-programming throughput on a cold-booted switch is low
enough that concurrent multi-scenario provisioning pushes the first
ASIC-verification scenario past its 60s poll budget.**

Two facts pin this down:

1. **In isolation, convergence is fast even cold.** Cold-deploy, then
   apply *only* setup-device + VLAN + 3 members via direct API calls
   and sample ASIC_DB every 15s:

   ```
   T+15s: ASIC_DB VLAN_MEMBER = 3
   T+30s ... T+210s: 3   (stable)
   ```

   3 entries by T+15s, no stall, no error.

2. **In the suite, ~8 scenarios fire concurrently.** Every
   early-suite scenario is gated only on `boot-ssh` or `setup-device`
   — `bridged`, `routed`, `bgp-underlay`, `interface-props`,
   `acl-lifecycle`, `qos-lifecycle`, `spec-authoring`, `static-route`.
   newtrun runs independent scenarios in parallel, so the moment SSH
   comes up they flood the two switches at once. setup-device alone is
   a large SAI batch (VRF, loopback, VXLAN tunnel, EVPN NVO, BGP
   globals). `bridged` is the **first** scenario to *verify* ASIC
   state, so its poll is the one that runs while the cold orchagent is
   still draining that concurrent backlog.

The difference between the passing isolated run and the failing suite
run is purely the concurrent SAI load on a cold orchagent.

## Why it surfaced now

Not because cold-deploy was untested — it is the mandated mode. The
suite runs in lifecycle mode (`newtrun start` deploys the topology
itself, then runs, then `stop` tears down — `runner.go:250`), and
DESIGN_PRINCIPLES §42 requires it: "Always start tests on a freshly
deployed topology. Prior state corrupts the convergence baseline."
The howto documents the suite as "~7m on a fresh lab"
(`docs/newtrun/howto.md:102`), consistent with a cold run.

The variable that surfaced the race is **host load**, not cold-vs-warm.
The 2026-06-09 baseline cold run passed at 5m47s on a lightly-loaded
host (one lab). This session ran the cold deploy with three labs
(19 VMs) already sharing the host — enough extra CPU contention to
slow the cold orchagent's SAI throughput past the 60s `bridged` poll,
and to stretch every convergence-dependent scenario (cold run here:
7m26s vs the baseline's 5m47s). The race was always latent in the
60s budget; heavier host contention is what crossed it.

A process note worth recording: several manual re-runs during
investigation were done *warm* (re-running on an already-deployed
lab), which violates §42 and returned false 21/21 passes — the warm
pipeline masks the race entirely. The protocol-compliant cold runs
are the ones that exposed it. Honor §42 when validating: a warm pass
proves nothing about the cold path the suite actually runs.

Neither the IPVPN/VRF-name collapse (#253) nor the global-platforms
work (#257) touched the ASIC poll path; the platform-rename diff is
cosmetic to this race.

## Workaround

Bump the two `bridged` ASIC-readiness polls from 60s to 180s on both
`2node-vs-primitive` and `2node-ngdp-primitive`. A poll exits as soon
as its condition is met, so the larger budget is free on warm runs
(still ~15s) and only absorbs the cold-boot-under-concurrency worst
case. `bridged` is the only scenario that needs it — later
ASIC-verifying scenarios (`irb`, `portchannel`, `service-lifecycle`,
`cross-switch`) run after the pipeline has warmed.

A warmup gate that polls a *pre-VLAN* SAI object
(`SAI_OBJECT_TYPE_VIRTUAL_ROUTER` + `SAI_OBJECT_TYPE_PORT`) was tried
and rejected: those objects are present within ~1s of boot, so the
gate passes immediately and predicts nothing about post-VLAN
convergence speed. The bottleneck is orchagent throughput *after* the
VLAN config is written, which has no pre-write signal to gate on.

## Resolution Path

1. Until the budget bump proves insufficient on heavier hosts, the
   poll-timeout workaround is the fix — convergence is reliable, only
   its latency varies.
2. A deeper fix would serialize the cold-boot-sensitive scenarios
   (run `bridged` before the concurrent provisioning flood, or gate
   the parallel scenarios behind `bridged`). Not done — it slows every
   run to defend a cold-only edge case the budget bump already covers.

## Affected Platforms

- sonic-vs 202505 (Force10-S6000_vs)
- Cisco virtual PFE (cisco-p200-32x100-vs) — same suite shape; the
  cold orchagent throughput characteristic is platform-general, so
  the same budget bump is applied to `2node-ngdp-primitive`.

## Related

- RCA-046: sonic-vs config reload crashes swss/syncd (different
  trigger — config-reload-induced crash vs. cold-boot convergence
  latency — but both manifest as "ASIC_DB not programmed despite
  CONFIG_DB writes succeeding").
- RCA-001 / RCA-012: swss/syncd lifecycle pitfalls on VPP.
- Issue #260: tracks this finding.

## Validation

Cold-deploy `2node-vs` + `2node-vs-primitive` after the budget bump:
**21/21 PASS** (7m26s, 2026-06-20, Force10-S6000_vs, 3 labs / 19 VMs
sharing the host). `bridged` completed in 51s — within the old 60s
budget on that run, but the 180s ceiling is the margin that absorbs
the variance the cold-boot race produces under heavier concurrency.

## Addendum — host degradation, reboot reset, and the diagnostic trap (2026-07-17)

A second, harder instance during the PR #441 (kind-aware `/status`)
validation records three things the original writeup does not: a
wedged-not-just-slow failure mode, an **unexplained** reboot recovery
(mechanism not found — see the correction below), and a diagnostic trap.

### The stall can be effectively permanent within a run, not merely late

The original framing — "the SAI objects *do* get programmed, just later
than the budget" — did not always hold this session: in the failing runs
orchagent stalled at **exactly 1 of 3** SAI bridge ports and stayed there.
Sampled repeatedly over 8+ minutes, `ASIC_DB` held 1
`SAI_OBJECT_TYPE_VLAN_MEMBER` / 1 `SAI_OBJECT_TYPE_BRIDGE_PORT`, with
orchagent **idle at 0% CPU** — no retry storm (`swss.rec` ~500 lines,
`sairedis.rec` ~3k), no SAI error, CONFIG_DB/APP_DB fully correct. It was
not draining a backlog; it was wedged, and the 240s poll budget did not
save it.

Distinguish from the RIF-starvation variant (orchagent *spinning*, tens of
thousands of retry lines starving the vlan-member consumer): that one is
busy, this one is idle. Same symptom at the poll (`ASIC_DB VLAN_MEMBER <
2`), opposite daemon state — check orchagent CPU + log volume to tell them
apart.

### A reboot restored convergence — but the mechanism is unidentified

The pass rate shifted sharply across a reboot: **0 of 3** cold `bridged`
runs passed before (branch and `main` alike; stopping the co-tenant peer
lab did **not** help), and **~4 of 5** passed after — 51s cold on four
consecutive commits, with one more flake before a rerun cleared the full
25/25. So the reboot demonstrably helped, and the lever is a reboot, not a
code change or a further poll-budget bump.

**But do not read a resource-leak mechanism into that.** A follow-up audit
of the host after the session's many deploy/destroy cycles found the
teardown clean: **zero orphaned `qemu`/`newtlink` processes, no socket or
FD accumulation, 1 TIME_WAIT total, no ephemeral-port pressure.** newtlab's
links are `-netdev socket,connect=` — no host TAP/bridge/veth/netns — so
there is little to leak, and what there is (processes + their sockets) was
reaped. An earlier draft of this addendum blamed "accumulated bridge/tap/
KVM state / memory pressure"; that is **not supported** — there was no such
accumulation to find. What a reboot actually cleared is unknown: candidates
are unobservable host state (KVM/kernel internals, page-cache/writeback
backlog) that leaves no process/socket signature, or the "100% before" was
a short unlucky streak of an intrinsically intermittent flake over-read as
"degradation" (note the post-reboot run *also* flaked once). Recorded as an
honest open question, not a mechanism.

(The teardown audit did surface real but *latent* §15 completeness gaps —
state-ledger-driven kills with no orphan sweep, unconditional state removal
on a failed kill, and no reboot/reality reconcile. They did not fire this
session; tracked in issue #444.)

### The diagnostic trap: a degraded-host A/B falsely implicates the diff

Because a degraded host fails *everything*, a branch-vs-`main` comparison
run on it is worthless — and actively misleading. A degraded-host `main`
run failed identically to the branch, which briefly "confirmed" a
pre-existing regression that did not exist. The bisect only converged after
the reboot, and even then required confirming reproducibility (the branch
flaked once, n=1, before a rerun cleared it 25/25).

Discipline for the next time an ASIC-poll scenario fails with a diff in
flight:

1. **Reboot before any branch-vs-`main` A/B.** Never trust a comparison on
   a host that has been churning labs for hours.
2. **Confirm reproducibility with a rerun before blaming the diff.** One
   `bridged` failure is noise, not a regression.
3. **Compare the emitted projection, not just pass/fail.** A byte-identical
   CONFIG_DB (`VLAN` / `VLAN_MEMBER` / `PORT`) across the good and bad
   commits proves the diff cannot be the cause — the difference is device
   timing, not newtron output. Here the suspect change was read-surface
   only (`/status`) and provably never reached the `configure-interface`
   write path.

Cost of ignoring this: most of a session spent bisecting a phantom
regression that a single post-reboot rerun would have exonerated.
