# RCA-016: Post-provision BGP routes stale until manual soft clear

**Note (Mar 2026):** The `ApplyFRRDefaults` mechanism and `refreshBGP` post-provision step referenced below were eliminated when frrcfgd unified mode was adopted. In unified mode, frrcfgd generates the full FRR configuration from CONFIG_DB (including `no bgp suppress-fib-pending`), so the timing issues described here no longer apply. The `restart-bgp` step in test suites handles the ASN change (RCA-019), and frrcfgd handles FRR config synchronization on startup.

## Symptom

After provisioning all 4 nodes in the 4-node topology, eBGP underlay sessions
were Established but exchanged zero or partial prefixes. Some peers showed
`PfxRcvd=0` despite the remote side claiming `PfxSnt=4`. Overlay sessions
remained in "Connect" because loopback routes were not propagated.

A manual `vtysh -c 'clear bgp * soft'` on all nodes immediately resolved
the issue — all prefixes were exchanged and overlay sessions established.

## Root Cause

When devices were provisioned in parallel, each device's post-provision sequence
(BGP restart -> 15s wait -> `ApplyFRRDefaults` -> `clear bgp * soft out`) ran
independently. The soft clear on Device A happened before Device B's BGP was
fully initialized, so A's re-advertisement had no effect on B.

By the time Device B completed its own soft clear, Device A's stale route
state was not refreshed. The routes remained "not yet sent" until the next
BGP timer event (ConnectRetry = 120s, Update timer), causing a multi-minute
convergence delay.

## Impact

- BGP convergence delayed by up to 120 seconds after provisioning
- Overlay sessions stuck in Connect until underlay routes propagate
- Intermittent: depends on provisioning order and timing

## Fix (Historical)

Two changes were made at the time, both since superseded:

### 1. Per-device: `ApplyFRRDefaults` -- `clear bgp * soft` (both directions)

`ApplyFRRDefaults()` originally ran `clear bgp * soft out` (outbound only).
This was changed to `clear bgp * soft` (both inbound and outbound) so each
device also reprocessed received routes after `no bgp suppress-fib-pending`
took effect.

### 2. Global: `refreshBGP` -- post-provision convergence pass

A post-provision BGP refresh step was added that ran `clear bgp * soft` on all
devices after provisioning completed, ensuring all devices re-advertised routes
after all peers were ready.

### Current state

Both mechanisms were eliminated when frrcfgd unified mode was adopted. In unified
mode, `no bgp suppress-fib-pending` is included in the generated FRR config from
the start, so routes are never suppressed during initial convergence. The
`restart-bgp` newtrun step (which restarts the BGP service after provisioning
writes the new ASN to CONFIG_DB) causes frrcfgd to regenerate the full FRR config,
and BGP converges normally without manual soft clears.

## Lesson

1. `suppress-fib-pending` in FRR suppresses route advertisement until the
   route is confirmed in the FIB. When disabling it (`no bgp suppress-fib-pending`),
   routes already received while it was active remain suppressed. A `clear bgp *
   soft` (both directions) is required to reprocess them.

2. When provisioning multiple BGP speakers, always include a global convergence
   step after all individual provisions complete. Per-device soft clears during
   provisioning are insufficient because peers may not be ready yet.
