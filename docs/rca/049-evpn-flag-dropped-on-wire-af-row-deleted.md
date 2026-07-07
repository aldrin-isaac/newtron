# RCA-049: sonic-vs frrcfgd deactivates the l2vpn evpn AF when a BGP_NEIGHBOR_AF row is rewritten at runtime — identical-fields HSET is not a no-op

**Severity**: High for any runtime mutation of an EVPN peer — the session drops and stays Idle.
**Platform**: sonic-vs (Force10-S6000, 202505 stable, unified frrcfgd) — measured. Split-mode bgpcfgd (CiscoVS) not separately measured.
**Status**: **Fixed in newtron** (2026-07-06) — `cs.Replace` now skips rows whose new fields equal the projection's, so an in-place update never rewrites an unchanged sibling row and never triggers this path. The frrcfgd behavior itself remains a platform pitfall.

## Summary

A description-only `update-bgp-evpn-peer` regenerated the peer's full entry
set — `BGP_NEIGHBOR` (changed: `name`) and `BGP_NEIGHBOR_AF default|<ip>|l2vpn_evpn`
(identical: `admin_status: "true"`) — and `cs.Replace` emitted a Replace for
both, including an **identical-fields HSET** on the AF row. frrcfgd's runtime
handling of that AF-row notification emitted `no neighbor <ip> activate` under
`address-family l2vpn evpn` (visible in `show running-config`), FRR sent a
NOTIFICATION to the peer, and the l2vpn-only session went **Idle permanently**.

The same daemon handles the identical row correctly during **startup replay**
(the session establishes fine after `restart-bgp`) — the deactivation happens
only on the **runtime** reprocess path. This is the same replay-vs-runtime
asymmetry family as RCA-044/RCA-045.

## Symptom

- `show bgp l2vpn evpn summary`: peer vanishes from the AF summary.
- `show bgp neighbors <ip> json`: `bgpState: Idle`, `lastResetDueTo: "BGP Notification send"`.
- `show running-config`: `no neighbor <ip> activate` under `address-family l2vpn evpn`,
  while the neighbor's other stanzas (remote-as, peer-group, the updated
  description) are correct — proving frrcfgd processed the runtime writes.

## Root cause (newtron side)

`ChangeSet.Replace` emitted a `ChangeReplace` for **every** entry the update
verb regenerated, even entries identical to the projection. An HSET that
changes nothing still fires a keyspace notification, and a daemon that
re-reads-and-reapplies on notification treats it as an event. §48's own text
promised "the diff leaves unchanged sibling rows (e.g. `BGP_NEIGHBOR_AF`)
untouched" — the implementation did not deliver the promise until now.

## Fix

`cs.Replace` skips entries whose new fields equal the projection's current
fields (`maps.Equal`): no write, no notification, no daemon reprocess. A
description-only peer update now touches exactly one row, `BGP_NEIGHBOR` —
whose runtime field handling is demonstrably safe (the `update-bgp-peer`
continuity check has held across every run). Pinned by
`TestReplace_SkipsUnchangedRows` (node package) and on the wire by the
`dataplane-update-evpn-peer` continuity check.

## Finding 2 — the trigger is the BGP_NEIGHBOR row itself; the verb is unusable at runtime on this platform

With the `cs.Replace` skip in place (the AF row genuinely untouched on a
description-only update), the session STILL went Idle with the same
`no neighbor <ip> activate`: unified frrcfgd's runtime handler for a
**BGP_NEIGHBOR row change** rebuilds the neighbor stanza destructively for an
EVPN peer-group member — deactivating the l2vpn AF it cannot see on its
runtime path. Contrast: the same rewrite on a plain (group-less, ipv4)
neighbor is handled incrementally — the update-bgp-peer continuity check
(34-) passes on every run.

Consequence: **update-bgp-evpn-peer cannot be used at runtime on sonic-vs**,
no matter how newtron delivers it. The newtron-side skip fix remains correct
and necessary on its own terms (§48's unchanged-sibling-rows clause), but it
cannot rescue this platform path.

## Finding 3 — interface-IP EVPN peers cannot establish anywhere: the peer group's update-source is the design speaking

Relocating the continuity check to CiscoVS (split bgpcfgd, runtime-capable)
with a synthetic session on a spare inter-switch /31 also failed — both sides
stuck in `Connect` with correct l2vpn AF and working ICMP. Cause: EVPN peers
join the `EVPN` peer group, which sets `update-source <loopback>` — so the
TCP SYN leaves sourced from the loopback while the far side expects the
interface IP. This is not a bug: §26/§ BGP architecture defines overlay peers
as **loopback-to-loopback**; an interface-IP EVPN peer contradicts the
design, and the group's update-source enforces it.

The valid construction for the continuity witness is a NEW loopback pair —
possible only in a fabric with a third loopback: on 3node-ngdp, leaf1↔leaf2
(the fabric only peers leaves to the spine, so that session is genuinely new
and intent-backed, reachable via spine underlay). Follow-up: author the check
in `3node-ngdp-dataplane` with that construction (verify the EVPN group's
multihop/TTL admits the 2-hop loopback path).

Until then: update-bgp-peer (34-) proves the §48 delivery on BGP_NEIGHBOR
rows end-to-end; update-bgp-evpn-peer shares that delivery path and is
covered at the substrate level (TestOpRoundTrip, TestReplace_SkipsUnchangedRows,
device_ops_test ChangeReplace assertions).

## Found by

The §48 evpn continuity check (`46-dataplane-update-evpn-peer.yaml`) on its
first live execution — the check exists to catch exactly this: a delivery
that is in-place at the CONFIG_DB layer but session-affecting after daemon
translation.

## Related

- RCA-044/RCA-045 — unified frrcfgd replay-vs-runtime asymmetries (neighbor
  creation requires write-before-restart; field updates reach FRR at runtime).
- DESIGN_PRINCIPLES_NEWTRON §48 — the in-place delivery principle whose
  "unchanged sibling rows" clause this fix makes literal.
- `pkg/newtron/network/node/changeset.go` (`Replace`).
