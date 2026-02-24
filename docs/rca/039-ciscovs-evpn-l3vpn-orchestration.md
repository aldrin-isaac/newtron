# RCA-039: EVPN L3VPN (Type-5) Not Working on CiscoVS — Silicon One SAI Limitation

**Severity**: High
**Platform**: CiscoVS (Silicon One SAI, Palladium2 NGDP simulator)
**Status**: Won't fix — Silicon One SAI cannot create L3 DECAP tunnel map entries.
EVPN IRB L2 bridging (type-2, same subnet) works. L3 inter-subnet routing via L3VNI is also blocked by the same SAI limitation.

**Note (Feb 2026):** The `2node-incremental` suite referenced here has been replaced by `2node-primitive` (21 scenarios, 20/20 PASS on CiscoVS — the 21st is health-check which now also passes). EVPN IRB (type-2) works end-to-end. EVPN L3VPN (type-5) remains blocked by the Silicon One SAI limitation described here. EVPN IRB L3 inter-subnet routing (different VLANs via L3VNI) confirmed blocked — same SAI limitation (Feb 2026).

## Symptom

EVPN L3VPN (`host3-ping-host6`, `2node-l3vpn` suite) fails with 100% packet loss.
FRR shows VNI Up, BGP sessions are established, and APP_DB has correct EVPN type-5
route entries (with `vni_label`, `nexthop`, `router_mac`).  ASIC_DB has only an ENCAP
tunnel map entry (`VIRTUAL_ROUTER_ID_TO_VNI`) but **no DECAP entry**
(`VNI_TO_VIRTUAL_ROUTER_ID`) — VXLAN packets cannot be decapsulated.

## Root Cause — Silicon One SAI `la_switch_impl::initialize` failure

When `VRF|vni` is written to CONFIG_DB, the orchestration chain is:

1. **vrfmgrd** writes `VXLAN_VRF_TABLE|Vrf_l3evpn` to APP_DB
2. **VxlanVrfMapOrch** processes this entry and calls `sai_tunnel_api->create_tunnel_map_entry()`:
   - ENCAP (`VIRTUAL_ROUTER_ID_TO_VNI`) — **succeeds**
   - DECAP (`VNI_TO_VIRTUAL_ROUTER_ID`) — **fails** with:
     ```
     Failed to add dummy vxlan switch svi for decap VRF 0x180000000000001, VNI 50001.
     Leaba_Err: Invalid parameter was supplied: la_switch_impl::initialize
     ```

3. The exception causes the entire `addOperation()` to fail.  On retry, ENCAP
   hits `SAI_STATUS_ITEM_ALREADY_EXISTS` → `throw std::runtime_error` → infinite
   retry loop (1s interval), DECAP never reattempted.

**Net result**: Only ENCAP exists in ASIC_DB.  No DECAP → VXLAN packets arriving
with this L3VNI cannot be decapsulated → 100% packet loss.

The `dummy vxlan switch svi` is a Silicon One SAI internal concept.  Every L3VNI
requires a dedicated internal SVI for VXLAN decap routing.  The Palladium2 NGDP
simulator does not support creating this internal SVI, making `VNI_TO_VIRTUAL_ROUTER_ID`
tunnel map entries impossible on this platform.

## Why EVPN IRB (Type-2) Works

EVPN IRB uses `VNI_TO_VLAN_ID` (L2 DECAP) tunnel map entries created by
`VxlanTunnelMapOrch` when `VXLAN_TUNNEL_MAP|vtep1|map_{vni}_Vlan{id}` is written.
Silicon One SAI handles L2 DECAP correctly — decap routes through the VLAN bridge
domain, and the SVI bound to the VRF provides L3 routing.  This is the standard
EVPN IRB path and works end-to-end:

- **3node-dataplane**: 6/6 PASS (including evpn-l2-irb scenario)
- **2node-primitive**: 32/32 PASS

## EVPN IRB L3 Inter-Subnet Routing Also Blocked

While EVPN IRB L2 bridging (same VLAN, same subnet) works via `VNI_TO_VLAN_ID` DECAP,
**L3 inter-subnet routing through an L3VNI is also blocked** by the same SAI limitation.

EVPN symmetric IRB (different VLANs, different subnets, routed via Vrf with L3VNI)
requires:
1. L3VNI transit VLAN + SVI + VXLAN tunnel map (CONFIG_DB entries — newtron now generates these)
2. `VNI_TO_VIRTUAL_ROUTER_ID` DECAP entry in ASIC_DB (SAI call — **fails on Silicon One**)

FRR control plane is fully functional:
- VRF routing table shows inter-subnet routes via Vlan3998 (L3VNI transit)
- `show evpn vni {l3vni}` shows State: Up, SVI-If: Vlan3998, Local Vtep Ip correct
- Type-5 prefix routes (ip-prefix advertisements) exchanged via BGP EVPN

But ASIC-level L3 VXLAN forwarding fails:
- SAI error: `sai_tunnel.cpp:954: Failed to create tunnel map entry. index 3`
- Packets to local SVI (gateway ping) are trapped to CPU — work fine
- Packets needing L3 VRF routing via L3VNI stay in ASIC fast path — dropped

This means on CiscoVS:
- **EVPN IRB L2 path (same subnet)**: WORKS
- **EVPN IRB L3 path (inter-subnet via L3VNI)**: BLOCKED (same SAI limitation)
- **EVPN L3VPN (type-5, evpn-routed)**: BLOCKED (same SAI limitation)

## Attempted Workarounds (All Failed)

### 1. L3VNI Transit VLAN

Created a dedicated transit VLAN (Vlan3999) with `VXLAN_TUNNEL_MAP` providing
L2 DECAP (`VNI_TO_VLAN_ID`) before writing `VRF|vni`.  Intended decap path:
VNI → Vlan3999 → SVI (VRF member) → VRF routing.

**Result**: `VxlanVrfMapOrch` detected the existing L2 mapping via
`isVniVlanMapExists()`, **removed the L2 DECAP** (`VNI_TO_VLAN_ID`), then
failed creating L3 DECAP — leaving no DECAP entry at all.

### 2. NVO Disable/Enable Trick

Disabled `VXLAN_EVPN_NVO` before writing `VRF|vni` to prevent vrfmgrd from
writing `VXLAN_VRF_TABLE` (since vrfmgrd checks NVO existence).  Re-enabled
NVO after VRF|vni was set.

**Result**: Timing issues — `VXLAN_VRF_TABLE` still got written before NVO
deletion propagated.  VxlanVrfMapOrch still ran, removed L2 DECAP, failed on
L3 DECAP.

### 3. NEWTRON_VNI Custom Table (Discarded Earlier)

Custom CONFIG_DB table to avoid writing `VRF|vni`.  Without `VRF|vni`,
`VRFOrch` never sets `l3_vni` → `RouteOrch` never programs type-5 routes.
Also violated Platform Patching Principle.

### 4. Shadow VLAN DECAP (Discarded Earlier)

Required NOT writing `VRF|vni` — same `RouteOrch` l3_vni=0 problem.

## Resolution

**Abandon EVPN L3VPN (evpn-routed) on CiscoVS/Silicon One.**  Use EVPN IRB
for all L3 EVPN use cases.  The `BindIPVPN`/`UnbindIPVPN` code is retained
structurally for future use when/if:

- The Silicon One SAI community fixes `la_switch_impl::initialize` for L3 DECAP
- sonic-swss PR #3908 (merged Jan 2026) replaces `throw` with
  `handleSaiCreateStatus()` for ITEM_ALREADY_EXISTS — not in our CiscoVS
  build (pinned to Sep 2025 commit `badf36fa`)

## Supporting Details

### frrcfgd vrf_handler Not Firing (Mitigated)

`vrf_handler` in frrcfgd.py is wired to `VRF` table keyspace events but never
fires on CiscoVS.  Other handlers (BGP_GLOBALS, BGP_GLOBALS_AF) work fine.
Root cause of the specific vrf_handler silence is unknown.

**Mitigation**: `newtron-vni-poll` background thread in patched `frrcfgd.py.tmpl`
polls CONFIG_DB `VRF` table every 5s and programs zebra VNI via vtysh.  This
successfully gets FRR VNI to `Up` state and generates type-5 NLRIs.

### ASIC_DB State After Failed Orchestration

```
TUNNEL_MAP_ENTRY (1 entry only):
  tunnel_map_type: VIRTUAL_ROUTER_ID_TO_VNI  (ENCAP)
  vni: 50001
  # NO VNI_TO_VIRTUAL_ROUTER_ID (DECAP) entry
```

### Syslog Error (orchagent)

```
NOTICE swss#orchagent: :- addOperation: ... SAI_STATUS_ITEM_ALREADY_EXISTS
ERR    swss#orchagent: Failed to add dummy vxlan switch svi for decap VRF ..., VNI 50001.
                       Leaba_Err: Invalid parameter was supplied: la_switch_impl::initialize
```

## Files Changed

- `pkg/newtron/network/node/vrf_ops.go` — `BindIPVPN`/`UnbindIPVPN` (retained, structurally sound)
- `pkg/newtron/network/node/evpn_ops.go` — Experimental `PrepareL3VNI`/`CleanupL3VNI`/
  `DisableEVPNNVO`/`EnableEVPNNVO` added and removed
- `pkg/newtlab/patches/ciscovs/always/frrcfgd.py.tmpl` — `newtron-vni-poll` retained
- `newtest/suites/2node-l3vpn/` — Deleted (experimental)
- `newtest/suites/2node-primitive/70-evpn-l3-routing.yaml` — Deleted (referenced removed actions)

## Related

- RCA-038: `VXLAN_TUNNEL_MAP` with `vrf` field → vxlanmgrd crash (`std::out_of_range`)
- sonic-swss PR #3908: Fixes ITEM_ALREADY_EXISTS handling (not in our build)
- CiscoVS buildimage pin: `cb27941bb` (202505 branch, sonic-swss at `badf36fa`)
