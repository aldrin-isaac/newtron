# RCA-004: iBGP overlay sessions require ebgp_multihop

## Symptom

iBGP overlay sessions between route reflectors and clients refused to
establish. FRR logs showed the peers being rejected with "eBGP multihop"
errors, despite the sessions being configured as iBGP (using the regional
overlay AS number).

## Root Cause

The network design uses a split-AS model:

- **Underlay**: eBGP with `underlay_asn` (unique per device or region)
- **Overlay**: iBGP with a shared regional AS for route reflection

The FRR `router bgp <asn>` command uses the `underlay_asn` as the local AS.
When an overlay neighbor is configured with `remote-as <regional_as>`, FRR
compares it to the router's local AS (`underlay_asn`). Since they differ,
FRR treats the session as **eBGP**, not iBGP.

For eBGP sessions between loopback addresses (not directly connected), FRR
requires `ebgp-multihop` to be set. Without it, the TTL check fails and the
session is rejected.

## Impact

- No iBGP overlay convergence — route reflectors couldn't peer with clients
- All overlay services (EVPN, VPN) blocked

## Fix

Added `ebgp_multihop: true` to all overlay BGP neighbor entries in the
topology provisioner:

```go
// Overlay neighbors are loopback-based and use a different AS than
// the router's local AS, so FRR treats them as eBGP. They need
// ebgp-multihop to work across multiple hops.
neighborAF["ebgp_multihop"] = "true"
```

## Lesson

In split-AS designs where the overlay AS differs from the underlay AS, FRR
classifies overlay sessions as eBGP even though they're logically iBGP.
Loopback-to-loopback eBGP sessions always need `ebgp-multihop`. This is a
common gotcha when mixing eBGP underlay with iBGP overlay in the same FRR
instance.

**Update (RCA-015):** FRR silently ignores `ebgp-multihop` when `local-as`
equals `remote-as` on the neighbor. The reliable workaround is
`ttl-security hops N` + `disable-connected-check`. See RCA-015 for details.
