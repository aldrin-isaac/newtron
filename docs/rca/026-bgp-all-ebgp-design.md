# RCA-026: All-eBGP Design for SONiC EVPN Fabrics

**Date**: 2026-02-16
**Platform**: All (SONiC-VPP, CiscoVS)
**Severity**: High (blocks fabric convergence)
**Status**: Resolved

## Summary

SONiC's `local-as` BGP feature has incomplete frrcfgd support, preventing the traditional **iBGP overlay + eBGP underlay** design. Implemented **all-eBGP architecture** where both underlay and overlay use eBGP with unique AS numbers per device (`underlay_asn` from profile).

## Background: Standard EVPN Fabric Design

Best practice for EVPN/VXLAN fabrics (RFC 7938):

**Underlay**: Hop-by-hop eBGP
- Unique AS per device (or per pod/rack)
- Physical interface peering
- IPv4/IPv6 unicast AF
- Redistributes loopbacks for reachability

**Overlay**: iBGP multi-hop
- Single AS for entire fabric (zone AS)
- Loopback-to-loopback peering
- L2VPN EVPN AF
- Route reflectors for scale

**Traditional implementation** (requires `local-as`):
```
router bgp 64512                          # Zone AS (overlay)

 # Underlay eBGP with local-as
 neighbor 10.1.0.1 remote-as 65012
 neighbor 10.1.0.1 local-as 65011 no-prepend replace-as

 # Overlay iBGP (same AS)
 neighbor 10.0.0.12 remote-as 64512
```

## Problem: SONiC local-as is Broken

### Initial Symptom

After implementing the traditional model, overlay peerings stuck in "Connect" state with no route exchange on underlay.

### Investigation

CONFIG_DB configuration:
```json
"BGP_NEIGHBOR|default|10.1.0.1": {
  "asn": "65012",
  "local_asn": "65011"
}
```

FRR rendered config:
```
router bgp 64512
 neighbor 10.1.0.1 remote-as 65012
 neighbor 10.1.0.1 local-as 65011    # Missing: no-prepend replace-as!
```

### Root Cause

**Without `no-prepend replace-as`**, FRR prepends router AS to outbound routes:
- AS-PATH becomes: `65011 64512` (local-as + router-as)
- Peer (also running AS 64512) detects **AS-PATH loop**
- Routes rejected → no loopback reachability → overlay fails

**Manual fix worked**:
```bash
# Added via vtysh
neighbor 10.1.0.1 local-as 65011 no-prepend replace-as
```
→ BGP converged, routes exchanged ✓

**But**: SONiC CONFIG_DB's `local_asn` field is a simple integer, doesn't support `no-prepend replace-as` flags. frrcfgd cannot render them.

### Upstream Issue

Filed issue: SONiC frrcfgd doesn't support `local-as` modifiers
- CONFIG_DB schema needs extension for flags
- frrcfgd template needs update
- Not viable for immediate deployment

## Solution: All-eBGP Design

### Architecture

**Both underlay AND overlay use eBGP** with unique AS per device:

```
# leaf1 (AS 65011)
router bgp 65011                          # underlay_asn from profile
 bgp router-id 10.0.0.11

 # Underlay: hop-by-hop eBGP (physical interface)
 neighbor 10.1.0.1 remote-as 65012
 neighbor 10.1.0.1 update-source 10.1.0.0

 # Overlay: loopback-to-loopback eBGP (profile-driven)
 neighbor 10.0.0.12 remote-as 65012
 neighbor 10.0.0.12 update-source 10.0.0.11
 neighbor 10.0.0.12 ebgp-multihop 255

 address-family ipv4 unicast
  redistribute connected              # Loopback reachability
  neighbor 10.1.0.1 activate          # Underlay
  neighbor 10.0.0.12 activate         # Overlay
 exit-address-family

 address-family l2vpn evpn
  neighbor 10.0.0.12 activate
  neighbor 10.0.0.12 next-hop-unchanged  # Preserve VTEP!
  advertise-all-vni
 exit-address-family
```

### Key Design Elements

1. **Router AS = underlay_asn**
   - Profile field: `underlay_asn: 65011`
   - Fallback to zone AS if not set

2. **Overlay peering is profile-driven**
   - Profile field: `evpn.peers: ["leaf2"]`
   - Code looks up peer's loopback IP and underlay_asn
   - Naturally eBGP when peers have different underlay_asn

3. **Critical for EVPN**: `next-hop-unchanged`
   - Preserves VTEP address across eBGP hops
   - Without it, overlay would rewrite next-hop to spine's loopback
   - Breaks VXLAN encapsulation (wrong VTEP)

4. **Underlay provides loopback reachability**
   - `redistribute connected` advertises loopbacks
   - Overlay sessions establish over underlay-learned routes

## Implementation

### topology.go Changes

**Router AS** (line 121-126):
```go
// Router runs underlay_asn for all-eBGP design
routerAS := resolved.ASNumber  // zone AS by default
if resolved.UnderlayASN > 0 {
    routerAS = resolved.UnderlayASN  // Override with underlay_asn
}
metaFields["bgp_asn"] = fmt.Sprintf("%d", routerAS)
```

**Overlay neighbors** (lines 203-241):
```go
// Profile-driven EVPN peers
for _, peerName := range getEVPNPeerNames(tp.network, deviceName) {
    peerProfile, err := tp.network.loadProfile(peerName)

    // Use peer's underlay_asn (eBGP overlay)
    peerAS := peerProfile.UnderlayASN
    if peerAS == 0 {
        peerAS = tp.network.spec.Zones[peerProfile.Zone].ASNumber
    }

    cb.AddBGPNeighbor("default", peerProfile.LoopbackIP, map[string]string{
        "asn":           fmt.Sprintf("%d", peerAS),
        "local_addr":    resolved.LoopbackIP,
        "ebgp_multihop": "true",
    })

    // EVPN AF with next-hop-unchanged
    cb.AddBGPNeighborAF("default", peerProfile.LoopbackIP, "l2vpn_evpn", map[string]string{
        "admin_status":      "true",
        "nexthop_unchanged": "true",  // Critical!
    })
}
```

**Underlay neighbors** (lines 256-268):
```go
// Topology-driven hop-by-hop eBGP
for _, ti := range topoDev.Interfaces {
    peerProfile := tp.network.loadProfile(peerDeviceName)

    cb.AddBGPNeighbor("default", peerIP, map[string]string{
        "asn":        fmt.Sprintf("%d", peerProfile.UnderlayASN),
        "local_addr": localIP,
    })
}
```

**No local-as** - completely removed from underlay config.

### Route Reflector Adaptation

For topologies with route reflectors:
- RR also runs `underlay_asn` (or zone AS if not set)
- RR-to-client sessions are eBGP (different AS)
- Still uses route-reflector-client flag
- eBGP route reflector is non-standard but supported by FRR

## Validation

### Test Results

```
newtrun: 5 scenarios: 5 passed (1m56s)

[1/5] boot-provision ........ PASS (1m12s)
[2/5] l3-routing ............ PASS (18s)
[3/5] host-verification ..... PASS (10s)
[4/5] teardown-verify ....... PASS (16s)
[5/5] cleanup ............... PASS (<1s)
```

### BGP Convergence

```bash
# leaf1
Neighbor     V    AS    State/PfxRcd
10.0.0.12    4  65012   Established/3    # Overlay eBGP ✓
10.1.0.1     4  65012   Established/3    # Underlay eBGP ✓
```

### Loopback Reachability

```bash
# leaf1 can reach leaf2 loopback via underlay
show ip route 10.0.0.12
Routing entry for 10.0.0.12/32
  Known via "bgp", distance 20, metric 0
  * 10.1.0.1, via Ethernet0
```

## Comparison: iBGP vs All-eBGP

| Aspect | iBGP Overlay (Traditional) | All-eBGP (Implemented) |
|--------|---------------------------|------------------------|
| Router AS | Zone AS (64512) | underlay_asn (65011) |
| Overlay peering | iBGP (same AS) | eBGP (different AS) |
| Underlay peering | eBGP with local-as | eBGP (no local-as) |
| SONiC support | ❌ Requires local-as flags | ✅ Works as-is |
| Configuration | Complex (local-as) | Simple (natural eBGP) |
| AS-PATH | Hidden via local-as | Visible (clear path) |
| EVPN next-hop | Preserved (iBGP) | Requires next-hop-unchanged |
| Route reflectors | Standard iBGP RR | eBGP RR (non-standard) |
| Industry usage | Most common | Cumulus Linux, some cloud providers |

## Advantages of All-eBGP

1. **Works with SONiC CONFIG_DB**
   - No frrcfgd modifications needed
   - No upstream dependencies

2. **Simpler AS-PATH**
   - No hidden AS prepending
   - Easier troubleshooting (clear path)

3. **Profile-driven**
   - Overlay peering from `profile.evpn.peers`
   - AS from `profile.underlay_asn`
   - No hard-coded ASNs

4. **Deterministic**
   - Each device has unique AS
   - No AS-PATH loop detection issues
   - Routes always accepted (no same-AS rejection)

## Trade-offs

1. **Not "best practice"**
   - RFC 7938 recommends iBGP overlay
   - Most vendor docs show iBGP design

2. **EVPN next-hop handling**
   - Must configure `next-hop-unchanged`
   - FRR default rewrites next-hop for eBGP
   - Would break VXLAN encapsulation

3. **Route reflector complexity**
   - eBGP route reflectors are non-standard
   - Requires different AS per RR
   - Or full-mesh leaf-to-leaf (no RR)

4. **AS number consumption**
   - Need unique AS per device
   - Private AS range (64512-65534): 1023 ASNs
   - 4-byte AS if more needed

## Alternative Considered: Patch frrcfgd

**Option**: Add `no-prepend replace-as` to frrcfgd template

**Implementation**:
```python
# frrcfgd template patch
if 'local_asn' in neighbor:
    conf += f" neighbor {ip} local-as {neighbor['local_asn']} no-prepend replace-as\n"
```

**Why not**:
- Upstream SONiC dependency
- Unknown timeline for merge
- May break existing deployments expecting current behavior
- All-eBGP works today without patches

## Lessons Learned

### 1. Validate SONiC CONFIG_DB Capabilities

Before designing a feature, verify **full round-trip**:
- CONFIG_DB schema supports all required fields
- frrcfgd renders all necessary FRR directives
- FRR configuration works as intended

Don't assume standard FRR features are fully supported in SONiC.

### 2. eBGP Overlay is Legitimate

All-eBGP is not a hack—it's used in production:
- **Cumulus Linux**: Default EVPN design (unnumbered eBGP)
- **AWS**: VPC routing uses eBGP throughout
- **Facebook**: All-eBGP fabric design

The "best practice" iBGP overlay is **one pattern**, not the **only pattern**.

### 3. Profile-Driven Design is Flexible

By deriving overlay AS from `profile.underlay_asn`:
- Same code supports both iBGP (when all peers have same AS)
- And eBGP (when peers have different AS)
- No hard-coded model assumption

The design adapts to the profile configuration.

### 4. next-hop-unchanged is Critical

For eBGP EVPN overlay:
```
neighbor X next-hop-unchanged
```

Without this, FRR rewrites next-hop to the advertising router's loopback, breaking VXLAN (wrong VTEP address).

CONFIG_DB field: `nexthop_unchanged: "true"` in BGP_NEIGHBOR_AF table.

## Future Considerations

### Multi-AS Route Reflectors

If using route reflectors with all-eBGP:

**Option 1**: RR per AS group
- RR has same AS as its clients
- Multiple RRs in fabric (one per AS group)

**Option 2**: eBGP RR (current)
- RR has unique AS
- Reflects routes across AS boundaries
- Non-standard but works in FRR

**Option 3**: BGP confederation
- Sub-AS per device
- Confederation AS for fabric-wide
- Complex, rarely used in DC

### Hybrid Designs

For multi-datacenter:
- iBGP within each DC (same zone AS)
- eBGP across DCs (different zone AS)
- Profile `underlay_asn` only set for cross-DC devices

### Migration from iBGP

If SONiC fixes local-as support:
- Add `underlay_asn` to profiles
- Code already falls back to zone AS if not set
- Can migrate gradually per-device

## Related Issues

- **RCA-019**: BGP local-as conflicts (initial discovery that local-as is broken)
- **RCA-025**: CiscoVS dataplane connectivity (MAC address uniqueness)

## References

- FRR BGP documentation: https://docs.frrouting.org/en/latest/bgp.html
- RFC 7938: Use of BGP for Routing in Large-Scale Data Centers
- Cumulus Linux EVPN design: https://docs.nvidia.com/networking-ethernet-software/cumulus-linux/Network-Virtualization/Ethernet-Virtual-Private-Network-EVPN/
- SONiC BGP CONFIG_DB schema: https://github.com/sonic-net/SONiC/blob/master/doc/mgmt/SONiC_Management_Framework_Schema.md

## Conclusion

The all-eBGP design is a **pragmatic solution** to SONiC's local-as limitations. It:
- ✅ Works with existing SONiC CONFIG_DB
- ✅ Passes all integration tests
- ✅ Maintains profile-driven architecture
- ✅ Follows established industry patterns

While not the RFC 7938 "best practice" iBGP overlay, it is a proven, production-ready design that achieves the same goals without requiring upstream SONiC modifications.
