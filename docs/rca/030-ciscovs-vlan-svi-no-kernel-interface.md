# RCA-030: CiscoVS Does Not Create VLAN SVI Kernel Interfaces

**Status**: Workaround applied (removed kernel-level SVI verification from scenarios)
**Component**: SONiC CiscoVS platform / `vlanmgrd`
**Affected**: Any scenario that checks for `VlanN` as a Linux kernel netdev
**Discovered**: 2026-02-19

---

## Symptom

After creating a VLAN and SVI in SONiC CONFIG_DB on CiscoVS, the corresponding kernel
interface does not appear:

```bash
# On standard SONiC (e.g., VPP or Mellanox)
$ ip addr show Vlan500
500: Vlan500@Bridge: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 9100 ...
    inet 10.1.50.1/24 brd 10.1.50.255 scope global Vlan500

# On CiscoVS
$ ip addr show dev Vlan500
Device "Vlan500" does not exist.
```

No `VLAN_TABLE` entry appears in STATE_DB either:

```
127.0.0.1:6379[6]> KEYS VLAN_TABLE*
(empty array)
```

---

## Root Cause

Standard SONiC's `vlanmgrd` creates a Linux bridge VLAN interface (`VlanN`) when VLAN config
is written to CONFIG_DB. This interface appears in the kernel netdev stack and is visible via
`ip link show`. STATE_DB `VLAN_TABLE` is populated by `vlanmgrd` once the interface is up.

On CiscoVS (Silicon One NGDP), `orchagent` programs VLAN membership and SVI routing entirely
via SAI APIs into the NGDP forwarding engine. The kernel-side VLAN interface creation
(`vlanmgrd` path) appears to be incomplete or disabled in the CiscoVS build.

**Exception**: VLAN interfaces that participate in EVPN L2 bridging **do** appear as kernel
netdevs on CiscoVS. For example, after `apply-service evpn-bridged` on VLAN 200:

```
108: Vlan200@Bridge: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 9100 ...
```

This suggests the kernel SVI creation is triggered by the EVPN service path (e.g., when FRR
adds the interface to an EVPN VNI), not by the basic VLAN config path.

---

## Impact

Scenarios that verify SVI kernel IP assignment (e.g., `ip addr show VlanN | grep <ip>`) will
fail on CiscoVS. Affected:

- `10-svi-configure.yaml`: `wait-svi-kernel-propagation` (5s wait) + `verify-svi-kernel-ip`

---

## Fix

Removed the kernel SVI interface verification steps from `10-svi-configure.yaml`. The scenario
still verifies:

- CONFIG_DB entries: `VLAN|VlanN`, `VLAN_INTERFACE|VlanN`, `VLAN_INTERFACE|VlanN|ip`
- These confirm the provisioner wrote correct config; kernel propagation is platform-specific

---

## Distinguishing from VPP (RCA-021)

VPP (sonic-platform-vpp) has no EVPN VXLAN support at all (sonic-platform-vpp#99 unmerged).
CiscoVS has EVPN VXLAN but lacks kernel VLAN SVI interfaces for plain (non-EVPN) VLANs.
These are different limitations from different root causes.

---

## Long-Term Resolution

Two options:
1. Verify VLAN SVI operational state via `VLAN_TABLE` in STATE_DB once CiscoVS populates it
2. Use `GetRouteASIC` / SAI route table to confirm SVI reachability instead of kernel netdev check

The correct L3 SVI verification is that the IP is reachable (ping from switch loopback to SVI IP),
not that the kernel interface exists. Update `svi-configure` to use ping-based verification.
