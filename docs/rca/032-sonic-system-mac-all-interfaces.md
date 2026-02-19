# RCA-032: SONiC Requires All Interfaces to Share the Same System MAC

**Status**: Documented (no fix needed — newtlab was already correct for switch nodes before RCA-028)
**Component**: `pkg/newtlab/link.go`, `pkg/newtlab/node.go`
**Affects**: Any topology generator that assigns per-NIC MACs to SONiC switch nodes
**Discovered**: 2026-02-19 (during RCA-028 post-mortem)

---

## Rule

**All data interfaces on a SONiC switch node must share the same system MAC address.**
This MAC is derived from the management NIC (eth0) and is used by SONiC as the device identity
for control-plane and data-plane operations.

**Different SONiC nodes must have different system MACs** (otherwise the fabric cannot distinguish them).

---

## Why SONiC Requires a Shared System MAC

SONiC uses the system MAC as the router identity in several places:

1. **EVPN Router MAC**: The system MAC is advertised as the `Router MAC` extended community
   in EVPN type-2 (MAC/IP) and type-5 (IP prefix) routes. Remote PEs use it to encapsulate
   VXLAN traffic destined for this device.

2. **ARP/ND on SVIs**: Replies to ARP and ND requests on SVI interfaces use the system MAC as
   the source MAC. If SVI interfaces had different MACs from the system MAC, ARP tables on
   connected hosts would become inconsistent.

3. **LACP / MCLAG**: Port-channel negotiation uses the system MAC as the bridge identifier.
   A consistent system MAC across all member ports is required by 802.3ad.

4. **FDB self-learning**: The kernel bridge uses a single MAC for self-entries. Multiple MACs
   on the same bridge (one per interface) would create conflicting self-entries.

In hardware SONiC platforms, all front-panel ports share the ASIC's system MAC regardless of
NIC indexing. QEMU VMs must replicate this behaviour.

---

## Contrast: Host VMs Need Unique Per-NIC MACs

Virtual host VMs (`host-vm` DeviceType) are regular Linux VMs running network namespaces.
They are NOT SONiC devices. Linux interfaces are independent netdevs — each must have a
unique MAC. Duplicate MACs on multiple interfaces in the same kernel cause:

- L2 switch FDB oscillation (see RCA-028)
- ARP table corruption
- Silent traffic blackholing

---

## Implementation in newtlab

`node.go` / `ResolveNodeConfig`: management NIC always uses `GenerateMAC(name, 0)`.
This is the "system MAC" — the anchor that all data NICs must match on switch nodes.

`link.go` / `dataNICMAC()`:

```go
func dataNICMAC(node *NodeConfig, nicIndex int) string {
    if node.DeviceType == "host-vm" {
        return GenerateMAC(node.Name, nicIndex) // unique per NIC for Linux VMs
    }
    // SONiC switch: all data NICs share the system MAC (same as index 0).
    return GenerateMAC(node.Name, 0)
}
```

`GenerateMAC(name, 0)` is stable — the same node name always produces the same system MAC
regardless of which link is being wired up.

---

## How the RCA-028 Fix Was Incomplete

RCA-028 correctly identified that host VM NICs need unique MACs. However, the initial fix
changed `AllocateLinks` to use `GenerateMAC(node.Name, lc.NICIndex)` for **all** node types,
including SONiC switches. This gave each data NIC on a switch a different MAC — violating the
system MAC constraint.

The corrected fix (this RCA) introduces `dataNICMAC()` which branches on `DeviceType`:

| DeviceType | MAC assignment | Reason |
|------------|----------------|--------|
| `switch`   | `GenerateMAC(name, 0)` | SONiC system MAC — same for all interfaces |
| `host-vm`  | `GenerateMAC(name, nicIndex)` | Linux netdev — unique per interface |

---

## Verification

After deploy, confirm all data NICs on a switch share the same MAC:

```bash
# Inside the SONiC switch VM
$ ip link show Ethernet0
Ethernet0: <BROADCAST,MULTICAST,UP,LOWER_UP> ... ether 52:54:00:xx:yy:zz ...

$ ip link show Ethernet1
Ethernet1: <BROADCAST,MULTICAST,UP,LOWER_UP> ... ether 52:54:00:xx:yy:zz ...
# ^ same MAC as Ethernet0
```

Compare with the management NIC:

```bash
$ ip link show eth0
eth0: ... ether 52:54:00:xx:yy:zz ...
# ^ must match the data NIC MACs
```
