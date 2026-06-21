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
run is purely the concurrent SAI load on a cold orchagent. Warm-lab
runs (the historical 5m47s baseline, where `boot-ssh` reports 0s
because the lab was already up) never hit it — the pipeline was warm
and the backlog already drained.

## Why it surfaced now

The suite was historically validated against a *warm* lab (re-running
on an already-deployed topology). Cold-deploy timing was never
separately exercised, so the race stayed latent. It became visible
when a cold-deploy verification was run explicitly, with three labs
(19 VMs) sharing the host — enough concurrent boot/provisioning load
to expose the cold orchagent's lower throughput.

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
