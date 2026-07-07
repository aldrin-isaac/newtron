# RCA-049: runtime EVPN-peer updates killed the session — the wire dropped the evpn flag twice, and newtron deleted its own AF row

**Severity**: High for any runtime mutation of an EVPN peer — the session dropped and stayed Idle.
**Platform**: reproduced identically on sonic-vs (Force10-S6000, 202505, unified frrcfgd) and CiscoVS (cisco-p200, unified frrcfgd). Platform-independent — the defect was newtron's.
**Status**: **Fixed** (2026-07-06, three layers): `BGPNeighborConfig` gained the `EVPN` field and both public wrappers pass it (PR #415); the `update-bgp-evpn-peer` HTTP handler decodes the canonical struct instead of an anonymous shadow that silently dropped the flag (this RCA's root cause); `cs.Replace` skips rows whose fields are unchanged (§48's "unchanged sibling rows stay untouched" made literal).

## Summary

A description-only `update-bgp-evpn-peer` with `evpn: true` on the wire killed
the l2vpn evpn session on every platform tried. `show running-config` showed
`no neighbor <ip> activate` under `address-family l2vpn evpn`; the peer went
`Idle` (`lastResetDueTo: "BGP Notification send"`) and never recovered.

`redis-cli MONITOR` during a live update produced the decisive evidence — the
delete came from **newtron itself**:

```
"hset" "BGP_NEIGHBOR|default|10.0.0.12" "admin_status" "up" "asn" "65012" "name" ... "peer_group_name" "EVPN"
"del"  "BGP_NEIGHBOR_AF|default|10.0.0.12|l2vpn_evpn"        ← the kill
```

## Root cause — the evpn flag was dropped at TWO wire layers

The internal `UpdateBGPEVPNPeer(ctx, ip, asn, description, evpn bool)` needs
the flag to regenerate the peer's entry set (with `evpn=true` the set includes
the `BGP_NEIGHBOR_AF ... l2vpn_evpn` row). Two independent boundary defects
starved it:

1. **The public wrappers hardcoded `evpn=false`** — `BGPNeighborConfig` had no
   field to carry it (fixed in PR #415).
2. **The HTTP handler used an anonymous shadow struct** —
   `{neighbor_ip, remote_as, description}` — so even after (1), the wire's
   `evpn: true` was silently discarded before it reached the (fixed) wrapper.

With `evpn=false`, the regenerated entry set lacks the AF row. `cs.Replace`
then does exactly what §48 specifies for a key the entity no longer has: it
**deletes** `BGP_NEIGHBOR_AF|<vrf>|<ip>|l2vpn_evpn`. The delivery layer was
correct; it was told the truth about the wrong entry set.

## frrcfgd is vindicated

Both earlier drafts of this RCA blamed the platform daemon ("destructive
runtime reprocess of the BGP_NEIGHBOR row"). The frrcfgd source
(`sonic-frr-mgmt-framework/frrcfgd/frrcfgd.py`, 202505) refutes that: the
runtime handler is **incremental** (commands generated only for changed
fields) and **read-only against CONFIG_DB**; `no neighbor X activate` is
emitted only when `admin_status` goes false or the AF row is **deleted**
(OP_DELETE renders every mapped command in its `no` form). frrcfgd faithfully
translated the delete newtron sent. Raw redis pokes on the BGP_NEIGHBOR row
(single-field and full-row HSETs) confirm: no AF deactivation.

## What each fix layer contributes

- **Handler decodes `newtron.BGPNeighborConfig`** (root cause): the flag
  reaches the internal method; the regenerated entry set keeps the AF row;
  no delete is emitted. The interface `update-bgp-peer` handler had the same
  anonymous-shadow pattern and was de-twinned in the same commit (§25: a
  shadow of a canonical struct diverges silently the moment the canonical
  grows a field).
- **`cs.Replace` skips unchanged rows**: with the handler fixed, the AF row
  regenerates identical to the projection — the skip means a description-only
  update touches exactly one row (`BGP_NEIGHBOR`), delivering §48's
  "unchanged sibling rows stay untouched" clause literally. Pinned by
  `TestReplace_SkipsUnchangedRows`.
- **`BGPNeighborConfig.EVPN` + wrappers**: the flag exists on the wire at all.

## Findings that stand

- **Interface-IP EVPN peers cannot establish** (both sides stuck `Connect`,
  ICMP fine): EVPN peers join the `EVPN` peer group, whose
  `update-source <loopback>` sources the TCP SYN from the loopback while the
  far side expects the interface IP. Not a bug — §26 defines overlay peers as
  loopback-to-loopback and the group enforces it. The valid continuity-witness
  construction is a **new loopback pair** (3node-ngdp leaf1↔leaf2 — the
  fabric only peers leaves to the spine). Validated: the leaf1↔leaf2 session
  establishes and holds.
- **sonic-vs runtime neighbor CREATION still needs the RCA-044
  write-before-restart replay**, and repeated mid-suite BGP restarts
  destabilize `bgp.service` (observed crash-loop after a second restart) —
  which is why the witness lives in the 3node CiscoVS suite rather than
  2node-vs, independent of this RCA's (refuted) daemon-blame.

## Addendum — the witness's own construction error, and the §27 gap it exposed

With the handler fixed, the 3node witness PASSED (session Established through
the in-place update, uptime monotonic) — the §48 delivery for
update-bgp-evpn-peer is wire-proven. But the pass itself was unsound: the
3node fabric's overlay is **leaf1↔leaf2 by design** (`nodes/leaf1.json
evpn.peers: [leaf2]`; the spine is underlay-only transit with no VTEP), so
the "new loopback pair" was actually the fabric's own profile-created
session — run 1's captured uptime (111s, predating the add) was the tell,
read too late. The witness's add silently double-owned the fabric's
BGP_NEIGHBOR row, and its cleanup remove then **amputated the fabric's
overlay peer** — the projection (which re-derives the peer from the profile
on every rebuild) expected rows the device no longer had, and the drift
guard correctly blocked every subsequent write.

Root cause of the double-ownership: `BGPNeighborExists` checked discrete
intents only (`evpn-peer|`, interface `bgp-peer`) — profile-owned overlay
peers are a device-intent **sub-operation** with no discrete intent record,
invisible to the check. Fixed: the check now also consults the projection's
BGP_NEIGHBOR table (the projection is derived from intent replay, so a row
there IS intent-owned). Pinned by `TestAddBGPEVPNPeer_RefusesProfileOwnedRow`.

Witness disposition: **withdrawn from the suites.** With the refusal in
place, no fabric in this repository can host it — every loopback belongs to
a profile-designed overlay (2node: switch1↔switch2; 3node: leaf1↔leaf2;
spine has no loopback), and adopting a profile-owned session is exactly what
the fix forbids. The one-shot wire proof stands (recorded in the PR); the
repeatable regression gates are the loopback wire test
(`1node-vs-config/39` — the AF row must survive the update) and the
substrate tests. A future fabric with an operator-added overlay peer (one
outside the profile design) is the natural permanent home.

## Process lesson

Two layers of the same boundary (wrapper, handler) dropped the same flag, and
two RCA drafts blamed the platform before `redis MONITOR` + a daemon-source
read produced the truth. The §38 protocol (read the daemon source before
concluding platform behavior) applied to a *config* daemon, not just SAI.
Then the corrected witness passed against the wrong session — captured
uptime larger than the session's age was sitting in the first run's
evidence, unread. Verify the fixture is yours before celebrating the
mechanism.
Follow-up filed: a conformance sweep asserting every handler request struct
covers the canonical params of the op it fronts — the wire-decode layer is
round-trip surface, and TestOpRoundTrip does not see it.

## Related

- RCA-044/RCA-045 — unified frrcfgd replay-vs-runtime asymmetries (these
  remain true; they were the *first two* suspects here and are not the cause).
- DESIGN_PRINCIPLES_NEWTRON §48 — in-place delivery; §26 — EVPN peer groups;
  §25 (ai-instructions) — single owner, no shadow structs.
- `pkg/newtron/network/node/changeset.go` (`Replace`),
  `pkg/newtron/api/handler_node.go` (`handleUpdateBGPEVPNPeer`),
  `pkg/newtron/api/handler_interface.go` (`handleUpdateBGPPeer`).
