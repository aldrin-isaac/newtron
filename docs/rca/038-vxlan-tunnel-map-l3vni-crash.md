# RCA-038: VXLAN_TUNNEL_MAP L3VNI Causes vxlanmgrd Crash and EVPN L3VPN Failure

**Severity**: Critical
**Platform**: CiscoVS (Silicon One SAI), likely affects all platforms
**Status**: Fixed

## Symptom

EVPN L3VPN scenarios (`evpn-l3-routing`, `evpn-irb`) fail. Cross-VRF host pings have 0% success.

FRR shows no L3VNI configured in the VRF:

```
switch1# show bgp vrf Vrf_l3evpn l2vpn evpn
No BGP prefixes displayed, [0] exist
(no vni block in router bgp output)
```

Syslog shows vxlanmgrd crash during BindIPVPN:

```
May 19 05:55:25.863783 switch1 ERR syncd#SDK: VNI mapping 'vtep1:evpn_map_50001_Vrf_l3evpn'
  update vrf Vrf_l3evpn, vni 50001: SAI_STATUS_ITEM_ALREADY_EXISTS
May 19 05:55:25.863783 switch1 ERR supervisord: ... Process 'vxlanmgrd' exited unexpectedly.
  Terminating supervisor 'swss'.
```

## Root Cause

`BindIPVPN` was writing **two** CONFIG_DB entries for the same L3VNI tunnel:

1. `VRF|{vrf}` with `vni: {l3vni}` → **vrfmgrd** processes this, writes L3VNI mapping to
   APP_DB, orchagent creates the SAI VXLAN tunnel map entry.

2. `VXLAN_TUNNEL_MAP|vtep1|map_{l3vni}_{vrf}` with `vrf`/`vni` fields → **vxlanmgrd**
   processes this, also attempts to create the same SAI VXLAN tunnel map entry.

Both paths result in an `SAI_CREATE_TUNNEL_MAP_ENTRY` call for the same VNI. Silicon One
SAI returns `SAI_STATUS_ITEM_ALREADY_EXISTS` on the second create. The CiscoVS SAI
propagates this as a fatal error rather than ignoring it, causing vxlanmgrd to crash.
supervisord then terminates the entire swss container, which cascades to kill the bgp
container (containing frrcfgd).

frrcfgd restarts ~84 seconds later, **after** all BindIPVPN CONFIG_DB writes are already
complete. frrcfgd init replays BGP_GLOBALS and BGP_NEIGHBOR entries but does NOT replay
VRF|vni via vtysh (it only processes new events after subscribe). As a result, zebra never
gets `vrf Vrf_l3evpn; vni 50001` and the L3VNI mapping is never established.

## Secondary Bugs (Fixed Simultaneously)

### Wrong field names in BGP_GLOBALS_AF (frrcfgd uses hyphens, not underscores)

frrcfgd's `global_af_key_map` uses hyphen-separated field names:

- `advertise-ipv4-unicast` → `advertise ipv4 unicast`  (NOT `advertise_ipv4_unicast`)

The original code used underscores (`advertise_ipv4_unicast`), so frrcfgd never generated
`advertise ipv4 unicast` in the VRF's `address-family l2vpn evpn` block.

### Wrong table for route targets (BGP_GLOBALS_EVPN_RT, not BGP_GLOBALS_AF fields)

frrcfgd processes route targets via a dedicated `BGP_GLOBALS_EVPN_RT` table handler, NOT
via fields in `BGP_GLOBALS_AF`. The original code used bgpcfgd-style fields:
`route_target_import_evpn` / `route_target_export_evpn` in BGP_GLOBALS_AF. These fields
are unknown to frrcfgd's runtime incremental handler and were silently ignored.

## Fix

In `pkg/newtron/network/node/vrf_ops.go`, `BindIPVPN`:

1. **Removed `VXLAN_TUNNEL_MAP` write**: `VRF|vni` alone is sufficient. vrfmgrd handles
   the L3VNI SAI programming via the APP_DB path. vxlanmgrd is for L2VNI bridge-domain
   mappings, not VRF-level L3VNI.

2. **Fixed BGP_GLOBALS_AF field**: `advertise_ipv4_unicast` → `advertise-ipv4-unicast`
   (hyphen) to match frrcfgd's `global_af_key_map`.

3. **Replaced BGP_EVPN_VNI with BGP_GLOBALS_EVPN_RT**: Route targets are now written as
   individual `BGP_GLOBALS_EVPN_RT|{vrf}|L2VPN_EVPN|{rt}` entries with
   `route-target-type: "both"`. frrcfgd's `bgp_globals_evpn_rt_handler` (line 3030 in
   frrcfgd.py) processes this and generates `route-target both {rt}` in the VRF's
   `address-family l2vpn evpn` block.

Also added `BGPGlobalsEVPNRT` field to `sonic.ConfigDB` struct with a corresponding
table parser, enabling `UnbindIPVPN` to discover and clean up RT entries by VRF prefix.

## Verification

After fix:
- vxlanmgrd no longer crashes during BindIPVPN
- FRR shows correct L3VNI in VRF:
  ```
  router bgp 65001 vrf Vrf_l3evpn
    address-family l2vpn evpn
      advertise ipv4 unicast
      route-target both 65001:50001
  ```
- `evpn-l3-routing` and `evpn-irb` scenarios pass in 2node-primitive suite

## Related

- frrcfgd table hierarchy: `VRF` → zebra vni, `BGP_GLOBALS_EVPN_RT` → BGP route-targets
- frrcfgd uses hyphen field names; bgpcfgd uses underscores — these are NOT interchangeable
- sonic-buildimage issue #21177: L3 EVPN broken after FRR 10.0.1 upgrade (same root cause)
- RCA-001: swss restart permanently breaks CiscoVS VMs
