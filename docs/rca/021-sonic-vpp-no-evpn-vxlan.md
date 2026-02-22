# RCA-021: SONiC VPP Does Not Support EVPN VXLAN

**Note (Feb 2026):** This VXLAN limitation is specific to the VPP platform. CiscoVS (Silicon One) fully supports EVPN VXLAN — the 3node-dataplane evpn-l2-irb scenario passes on CiscoVS. VPP remains blocked on sonic-platform-vpp#99.

## Symptom

VXLAN_TUNNEL, VXLAN_TUNNEL_MAP, and VXLAN_EVPN_NVO entries are written to CONFIG_DB and promoted to APP_DB, but:
- No SAI tunnel objects appear in ASIC_DB
- No kernel VXLAN device is created
- FRR shows 0 L2/L3 VNIs
- `show evpn vni` is empty even after the 180s MH startup delay expires

## Root Cause

The SONiC VPP platform (sonic-platform-vpp) does not yet support VXLAN tunnel offloading. The initial support PR ([sonic-net/sonic-platform-vpp#99](https://github.com/sonic-net/sonic-platform-vpp/pull/99), opened July 2024) implements `create/remove VxLAN tunnel offloading to VPP` and enables `vxlan_plugin.so`, but it has **not been merged** into the mainline image.

Without VPP tunnel support:
1. orchagent receives the VXLAN config from APP_DB but cannot create SAI tunnel objects
2. No kernel VXLAN device exists for zebra to discover
3. FRR never learns about VNIs, so no EVPN Type-2/3/5 routes are advertised

## What Works on SONiC VPP

| Feature | Status |
|---------|--------|
| L2 VLAN bridging (single switch) | Works |
| L3 IP routing via FRR/BGP | Works |
| VLAN member, SVI interfaces | Works |
| Interface IP configuration | Works |
| VXLAN tunnels | Not supported |
| EVPN L2VNI / L3VNI | Not supported |
| MAC-VPN / IP-VPN binding | Config accepted, no data plane effect |

## Impact on Testing

Data plane tests that require cross-switch L2 or L3 VXLAN cannot run on sonic-vpp. Alternative approaches:
- **L3 routing**: Use routed interfaces with BGP to test cross-switch IP connectivity
- **Single-switch L2**: Test VLAN bridging and SVI within a single leaf

## Workaround

None. Wait for sonic-platform-vpp#99 to be merged, or use a hardware-backed SONiC image with ASIC SAI that supports VXLAN.

## Related

- RCA-019: BGP ASN mismatch after provision
- RCA-020: SONiC VPP port count matches NIC count
