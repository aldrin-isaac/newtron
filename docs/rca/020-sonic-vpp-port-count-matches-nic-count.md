# RCA-020: SONiC VPP Port Count Matches NIC Count

## Symptom

Interface `Ethernet2` exists in CONFIG_DB but not in STATE_DB. Operations referencing Ethernet2 succeed at the Redis level but have no effect on the data plane. EVPN VNIs don't appear in FRR, VXLAN tunnels don't form.

## Root Cause

SONiC VPP creates exactly as many data-plane ports as there are VPP-managed interfaces, which equals the number of QEMU data NICs (NICs beyond NIC 0, which is management). Port naming is sequential:

| Data NICs | Ports created |
|-----------|--------------|
| 2         | Ethernet0, Ethernet1 |
| 3         | Ethernet0, Ethernet1, Ethernet2 |
| 4         | Ethernet0, Ethernet1, Ethernet2, Ethernet3 |

newtlab allocates NICs based on the number of links connected to a device in the topology. A device with 2 links gets 2 data NICs → Ethernet0 and Ethernet1 only. Referencing Ethernet2 in this configuration is invalid — the port doesn't exist in VPP.

## Fix

Use sequential port names starting from Ethernet0 that match the actual NIC count. For a device with 2 links, use Ethernet0 (fabric) and Ethernet1 (host-facing), not Ethernet0 and Ethernet2.

## Topology Design Rule

In newtlab topologies, interface names must be sequential and match the NIC allocation:
- First link → Ethernet0
- Second link → Ethernet1
- Third link → Ethernet2
- etc.

Gaps in interface numbering (e.g., Ethernet0 + Ethernet2 with no Ethernet1) will cause the higher-numbered port to not exist in the data plane.

## Related

- RCA-010: QEMU PCI slot offset for data NICs
- RCA-013: Boot patch port stride mismatch
