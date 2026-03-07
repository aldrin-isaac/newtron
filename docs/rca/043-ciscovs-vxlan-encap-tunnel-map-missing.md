# RCA-043: CiscoVS VXLAN L2 ENCAP Tunnel Map Entry Missing

**Date**: 2026-03-06
**Platform**: CiscoVS (Silicon One Palladium2)
**Status**: Open — sonic-swss/orchagent issue

## Symptom

EVPN L2 (evpn-bridged/evpn-irb) data-plane fails on CiscoVS. Hosts on the same
VLAN across VXLAN cannot communicate. ARP resolution fails with "Destination Host
Unreachable". `show mac` shows 0 entries despite FRR EVPN control plane being
fully operational (type-2 routes exchanged, remote VTEP oper_up, VNI state Up).

## Root Cause

SONiC `VxlanTunnelOrch` (in orchagent) creates only the **DECAP** tunnel map entry
(`SAI_TUNNEL_MAP_TYPE_VNI_TO_VLAN_ID`: VNI 10100 → VLAN 100) but never creates
the corresponding **ENCAP** tunnel map entry (`SAI_TUNNEL_MAP_TYPE_VLAN_ID_TO_VNI`:
VLAN 100 → VNI 10100).

Without the ENCAP entry, the Silicon One SAI cannot encapsulate L2 frames from
VLAN 100 into VXLAN packets with VNI 10100. BUM flooding (ARP broadcasts) and
unicast forwarding over VXLAN both fail — no frames leave the VXLAN tunnel.

## Evidence

From `sairedis.rec` on leaf1:

```
# 4 tunnel map containers created ✓
c|TUNNEL_MAP:0x3e1|TYPE=VNI_TO_VLAN_ID        # DECAP L2
c|TUNNEL_MAP:0x3e2|TYPE=VLAN_ID_TO_VNI        # ENCAP L2 (container only)
c|TUNNEL_MAP:0x3e3|TYPE=VNI_TO_VIRTUAL_ROUTER_ID  # DECAP L3
c|TUNNEL_MAP:0x3e4|TYPE=VIRTUAL_ROUTER_ID_TO_VNI  # ENCAP L3

# P2MP tunnel with both ENCAP and DECAP mappers ✓
c|TUNNEL:0x3e5|DECAP_MAPPERS=0x3e1,0x3e3|ENCAP_MAPPERS=0x3e2,0x3e4|SRC_IP=10.0.0.11|PEER_MODE=P2MP

# Only 1 tunnel map ENTRY created — DECAP only ✗
c|TUNNEL_MAP_ENTRY:0x3e7|TYPE=VNI_TO_VLAN_ID|MAP=0x3e1|VLAN=100|VNI=10100

# P2P tunnel to remote VTEP ✓
c|TUNNEL:0x3e8|DST_IP=10.0.0.12|PEER_MODE=P2P

# Bridge port for P2P tunnel ✓
c|BRIDGE_PORT:0x3e9|TYPE=TUNNEL|TUNNEL_ID=0x3e8|FDB_LEARNING=DISABLE

# Remote FDB entry ✓
c|FDB_ENTRY:{"mac":"52:54:00:B7:26:55"}|TYPE=STATIC|BRIDGE_PORT=0x3e9|ENDPOINT_IP=10.0.0.12
```

**Missing**: `c|TUNNEL_MAP_ENTRY|TYPE=VLAN_ID_TO_VNI|MAP=0x3e2|VLAN=100|VNI=10100`

## Diagnostic Verification

```bash
# ARP arrives at switch but is never VXLAN-encapsulated
sudo tcpdump -i Ethernet1 arp      # sees ARP requests from host ✓
sudo tcpdump -i Ethernet0 udp port 4789  # 0 packets ✗
sudo tcpdump -i vtep1-100          # 0 packets ✗

# Control plane is correct
vtysh -c 'show evpn vni 10100'     # VNI Up, 2 MACs, 2 ARPs, remote VTEP flood HER
vtysh -c 'show evpn mac vni 10100' # local + remote MACs learned ✓
show vxlan remotevtep               # oper_up ✓

# ASIC_DB has FDB but not ENCAP entry
redis-cli -n 1 keys '*FDB*'        # remote MAC FDB entry exists ✓
redis-cli -n 1 keys '*TUNNEL_MAP_ENTRY*'  # only 1 entry (DECAP) ✗

# APP_DB is fully populated by vxlanmgrd and fdbsyncd
redis-cli -n 0 keys '*VXLAN*'      # tunnel, map, NVO, remote_vni, fdb all present ✓
```

## Why DECAP Works But ENCAP Doesn't

On some hardware SAIs (e.g., Broadcom), the ENCAP VNI may be inferred from the
VLAN membership + tunnel association without requiring an explicit ENCAP tunnel
map entry. On CiscoVS (Silicon One NGDP), the SAI appears to require explicit
ENCAP entries — the ENCAP mapper container (VLAN_ID_TO_VNI) is created and
attached to the tunnel, but contains zero entries, making it a null mapper.

## Layer

sonic-swss `orchagent/vxlanorch.cpp` — `VxlanTunnelOrch::addOperation` processes
`VXLAN_TUNNEL_MAP_TABLE` from APP_DB and creates SAI tunnel map entries. It only
creates the DECAP entry. The ENCAP entry creation is either missing or conditional
on SAI capability flags that CiscoVS doesn't advertise.

## Resolution Path

1. File upstream issue against sonic-swss to have `VxlanTunnelOrch::addOperation`
   create both ENCAP and DECAP tunnel map entries when processing a tunnel map.
2. Alternatively, investigate whether the CiscoVS SAI `create_tunnel_map_entry`
   call for ENCAP type (VLAN_ID_TO_VNI) is supported — it may fail silently.
3. As a workaround, a custom patch could directly create the ENCAP entry via
   SAI after the DECAP entry is created. This is a **valid bug fix** per the
   Platform Patching Principle (same signal, same action, bug fix at the SAI/orch layer).

## Impact

- All EVPN L2 data-plane tests fail (evpn-bridged, evpn-irb L2 path)
- EVPN control plane unaffected (routes exchanged correctly)
- L3 routing (non-VXLAN) unaffected
- Affects CiscoVS only; may not affect hardware platforms

## Previous "6/6 PASS" Explanation

MEMORY.md recorded "3node-dataplane: 6/6 PASS (Feb 2026)". This was before the
newtron-server API migration. The `verify-ping` step executor used
`r.Client.SSHCommand()` for all devices, which failed for host devices with
"device 'host1' is a host (no SONiC)". The error was not caught properly and
the test may have passed vacuously. After fixing `verify-ping` to use direct SSH
for hosts (Mar 2026), the actual EVPN data-plane failure was exposed.
