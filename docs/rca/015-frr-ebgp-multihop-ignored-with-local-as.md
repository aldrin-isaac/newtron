# RCA-015: FRR silently ignores ebgp-multihop when local-as equals remote-as

**Status**: SUPERSEDED by RCA-026

> **Note (Feb 2026):** This analysis is superseded by the all-eBGP design documented in RCA-026. The iBGP+local-as approach was abandoned; all topologies now use eBGP overlay.

## Symptom

Inter-RR BGP sessions (spine1 ↔ spine2) in the 4-node topology would not
establish. FRR debug logs showed `Connect failed 113(No route to host)`.
tcpdump confirmed the BGP TCP SYN had **TTL=1**, causing it to expire at
the intermediate leaf (2-hop path: spine → leaf → spine).

Manual TCP connections from the same source IP to the same destination
port 179 succeeded (TTL=64 by default). `neighbor X ebgp-multihop 255`
was accepted by vtysh without error but did not appear in `show run bgpd`.

## Root Cause

The overlay BGP design uses `local-as` to present the regional overlay AS:

```
router bgp 65001               ← underlay AS
  neighbor 10.0.0.2 remote-as 64512
  neighbor 10.0.0.2 local-as 64512
```

FRR has **inconsistent** internal classification for this configuration:

1. **For TTL/socket**: FRR compares the router's global AS (65001) with the
   neighbor's remote-as (64512). They differ → **external link** → TTL=1.

2. **For ebgp-multihop**: FRR compares local-as (64512) with remote-as
   (64512). They match → **iBGP for this peer** → ebgp-multihop is silently
   ignored (no-op for iBGP).

This creates a catch-22: the session uses eBGP TTL=1 but the command to
increase TTL is rejected because FRR thinks it's iBGP.

## Impact

- All multi-hop overlay sessions (spine ↔ spine) permanently blocked
- Single-hop overlay sessions (leaf ↔ spine) unaffected (TTL=1 is enough)
- Only manifests in topologies with >1 hop between overlay peers

## Fix

Changed `ApplyFRRDefaults` in `pkg/newtron/network/node/node.go` to use `ttl-security`
instead of `ebgp-multihop`. Unlike `ebgp-multihop`, `ttl-security` is
accepted regardless of eBGP/iBGP classification:

```go
// Before (silently ignored by FRR):
cmds += fmt.Sprintf(" -c 'neighbor %s ebgp-multihop 255'", ip)

// After (works regardless of FRR's internal classification):
cmds += fmt.Sprintf(
    " -c 'neighbor %s ttl-security hops 10'"+
        " -c 'neighbor %s disable-connected-check'",
    ip, ip)
```

`ttl-security hops 10` sets outgoing TTL=255 and requires incoming TTL≥245.
`disable-connected-check` allows non-directly-connected eBGP peers.

## Lesson

FRR's `local-as` directive creates inconsistent internal classification:
the session may be "external" for some checks (TTL, AS path) but "internal"
for others (multihop applicability). When `local-as == remote-as`,
`ebgp-multihop` is a no-op. Use `ttl-security hops N` as the reliable
alternative — it sets TTL=255 unconditionally and works for both eBGP and
iBGP sessions.
