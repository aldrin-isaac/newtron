# RCA-028: newtlab Assigns Identical MACs to All Data NICs on Coalesced Host VMs

**Status**: Fixed (initial fix was incomplete — see RCA-032 for the corrected approach)
**Component**: `pkg/newtlab/link.go`
**Affected**: 2node topology (any topology with >1 data NIC per host VM)
**Discovered**: 2026-02-19

---

## Symptom

L2 bridging between two hosts coalesced into the same QEMU VM fails silently. ARP resolves
one way but not the other:

```
host2# arping -c 3 192.168.200.3
Sent 3 probe(s)
Received 0 response(s)     ← host3's ARP reply never arrives

host3# arp -n
192.168.200.2 at 52:54:00:4a:09:12  ← host3 sees host2 fine
```

Switch FDB flips between ports:

```
switch1# bridge fdb show
52:54:00:4a:09:12 dev Ethernet2 vlan 200   ← oscillates to Ethernet3 after host3 sends
```

---

## Root Cause

`AllocateLinks` in `pkg/newtlab/link.go` called `GenerateMAC(nodeName, 0)` for every NIC,
hardcoding index 0 regardless of the actual NIC index:

```go
// Before fix — all NICs on the same node share one MAC
MAC: GenerateMAC(nodeA.Name, 0),
MAC: GenerateMAC(nodeZ.Name, 0),
```

`GenerateMAC` hashes the node name and index to produce a stable MAC. With index 0 for every
call, all data NICs on a node got identical MACs. The management NIC also used index 0 and was
assigned the same MAC via a separate path.

For the 2node topology, `hostvm-0` hosts 6 data NICs (one per host namespace) plus 1 management
NIC — all with MAC `52:54:00:4a:09:12`. Switch sees:

- ARP broadcast from host2 via Ethernet2 → learns MAC→Ethernet2
- host3 sends ARP reply via Ethernet3 → MAC→Ethernet3 (overwrites)
- Switch forwards the reply back to Ethernet3, not Ethernet2 → host2 never sees it

The 3node topology was unaffected because each host was a separate QEMU VM (no coalescing),
so each host had only one data NIC and the duplicate MAC was never triggered.

---

## Why 3node Was Unaffected

In 3node, hosts are **not** coalesced: host1, host2 each get their own QEMU VM. A VM with
only one data NIC only ever assigns NIC index 0 — so all NICs got the "same" MAC, but each
was on a separate switch port from a different physical VM. No FDB flapping occurred.

In 2node, all 6 hosts are coalesced into `hostvm-0` (one QEMU VM). Multiple NICs share a
single VM, so the kernel assigns them to separate veth pairs. When all get the same MAC,
any L2 switch with multiple host-facing ports sees source-MAC collisions.

---

## Fix

The fix must be applied differently for SONiC switches vs. host VMs:

- **SONiC switches** require all interfaces to share the same system MAC (see RCA-032).
  The original `GenerateMAC(name, 0)` was actually correct for switch nodes.
- **Host VMs** are Linux machines and require unique MACs per NIC.

The initial fix of using `GenerateMAC(node.Name, lc.NICIndex)` universally was therefore
wrong for SONiC switch nodes — it gave each switch data NIC a different MAC, violating the
system MAC constraint.

The corrected fix introduces `dataNICMAC()` in `link.go` that branches on `DeviceType`:

```go
func dataNICMAC(node *NodeConfig, nicIndex int) string {
    if node.DeviceType == "host-vm" {
        return GenerateMAC(node.Name, nicIndex) // unique per NIC for Linux VMs
    }
    return GenerateMAC(node.Name, 0) // SONiC: all interfaces share system MAC
}
```

**Requires lab redeploy** — QEMU NIC MACs are baked into the VM launch command and cannot be
changed on running VMs.

---

## Verification

After fix, QEMU command for `hostvm-0` shows distinct MACs per NIC:

```
virtio-net-pci,netdev=mgmt,mac=52:54:00:4a:09:12,...
virtio-net-pci,netdev=eth1,mac=52:54:00:XX:XX:XX,...   ← unique
virtio-net-pci,netdev=eth2,mac=52:54:00:YY:YY:YY,...   ← unique
...
```

L2 ping between host namespaces succeeds; FDB entries are stable.
