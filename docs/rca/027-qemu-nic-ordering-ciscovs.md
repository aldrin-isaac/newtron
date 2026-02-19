# RCA-027: QEMU NIC ordering mismatch breaks CiscoVS data plane for >2 data ports

## Summary

When a SONiC device has 3 or more data interfaces, newtlab's QEMU command appended data
NICs in link-processing order rather than NIC index order. QEMU enumerates PCI devices in
command-line order, so the kernel assigns `eth0`, `eth1`, `eth2`, … based on position in
the QEMU command, **not** on the `id=ethN` label. CiscoVS's TC mirred rules map `ethN ↔
swvethN ↔ vethN ↔ EthernetN-1` by kernel interface name. When the kernel's `eth1` was not
the NIC connected to switch port Ethernet0, all data-plane traffic was silently misdirected.

## Symptom

- ARP fails between directly-connected SONiC switches even though `show interfaces status`
  shows both ports as up and newtlab link status shows "connected".
- `ip neigh show` reports `FAILED` for the peer IP.
- `tcpdump -i EthernetN` captures 0 packets even during active arping (TAP interface
  processes via syncd — tcpdump on TAP doesn't see traffic the same way as hardware NICs).
- Interface TX byte counters on `EthernetN` do increment (syncd queues the packets to the
  TAP fd), but they never reach the peer.
- 3node topology (2 data ports per switch) was unaffected: with only 2 data NICs added in
  the order NIC1, NIC2, the kernel naming happened to match by coincidence.

## Root Cause

`link.go:AllocateLinks` appends `NICConfig` entries to `node.NICs` in topology link
processing order, which depends on Go map iteration (non-deterministic) and link ordering
in the topology file. With 4 data interfaces on switch1 in the 2node topology, the order
was: NIC4 (Ethernet3), NIC3 (Ethernet2), NIC1 (Ethernet0), NIC2 (Ethernet1).

`qemu.go:Build` iterated over `node.NICs` in slice order without sorting, producing a
QEMU command with data NICs in the wrong PCI slot order:

```
-netdev socket,id=eth4,...  ← kernel eth1 (2nd NIC)
-netdev socket,id=eth3,...  ← kernel eth2
-netdev socket,id=eth1,...  ← kernel eth3 (should be eth1 for Ethernet0!)
-netdev socket,id=eth2,...  ← kernel eth4
```

CiscoVS's `tc-create.sh` (run inside syncd at boot) installs TC mirred rules:
```
eth1 ↔ swveth1 ↔ veth1 → NGDP port 0 (Ethernet0)
eth2 ↔ swveth2 ↔ veth2 → NGDP port 1 (Ethernet1)
...
```

With the wrong NIC order, kernel `eth1` was wired to Ethernet3's socket bridge port, so
Ethernet0's traffic was sent out the wrong physical path.

## Fix

`pkg/newtlab/qemu.go`: Sort `node.NICs` by `Index` before iterating to build the QEMU
command. This ensures NIC index 1 → 2nd QEMU device → kernel `eth1` → `swveth1` →
Ethernet0, for any number of data interfaces.

```go
sortedNICs := make([]NICConfig, len(q.Node.NICs))
copy(sortedNICs, q.Node.NICs)
sort.Slice(sortedNICs, func(i, j int) bool { return sortedNICs[i].Index < sortedNICs[j].Index })
for _, nic := range sortedNICs {
    ...
}
```

## Why 3-node was unaffected

3node leaf switches have only Ethernet0 and Ethernet1 (2 data ports). The link-processing
order happened to produce NIC1 (Ethernet0) before NIC2 (Ethernet1), matching the correct
kernel naming. The 2node topology's 4-port switches exposed the bug because the random
map iteration order placed NIC4 first.

## Detection

This class of bug is detectable by verifying the QEMU command before launch:
`assert sorted([nic.Index for nic in data_nics]) == [nic.Index for nic in data_nics_in_cmd_order]`
