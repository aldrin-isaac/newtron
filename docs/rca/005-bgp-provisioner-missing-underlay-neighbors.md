# RCA-005: BGP provisioner missing underlay neighbors and RR clients

## Symptom

After provisioning, eBGP underlay sessions were not created between directly
connected devices. Route reflectors discovered other RRs but had no client
BGP_NEIGHBOR entries — leaf devices were invisible to the RR overlay.

## Root Cause

Two separate gaps in the topology provisioner:

### Gap 1: No eBGP underlay neighbors for transit service

The `transit` service in `network.json` defines point-to-point links with IP
addresses but has no `routing` block (unlike `fabric-underlay` which has
explicit BGP neighbor definitions). The provisioner only created BGP neighbors
from explicit `routing.bgp.neighbors` entries — it never auto-generated eBGP
underlay neighbors from topology link adjacencies.

For transit links, the remote peer AS and IP should be inferred from the
topology: the peer IP is the other end of the /31 link, and the peer AS is
the remote device's `underlay_asn`.

### Gap 2: Route reflector missing client entries

The `addRouteReflectorEntries()` function only looked at other route
reflectors to create iBGP mesh peers. It never iterated non-RR devices
(leaves) to create RR-client BGP_NEIGHBOR entries. Route reflectors need
explicit neighbor entries for every client in the overlay.

## Impact

- No eBGP underlay convergence — devices couldn't exchange routes
- No iBGP overlay clients — route reflectors only peered with each other
- Both gaps had to be fixed before any BGP session could establish

## Fix

### Fix 1: Auto-generate eBGP underlay neighbors

Added logic to the topology provisioner that iterates all topology links. For
each link where the local device has an `underlay_asn`, it creates a
BGP_NEIGHBOR entry for the remote peer:

```go
// For each topology link, if both sides have underlay_asn,
// create eBGP neighbor entries automatically
for _, link := range topology.Links {
    remoteASN := topology.Devices[remoteDevice].UnderlayASN
    // Create BGP_NEIGHBOR with remote-as = remoteASN
    // Create BGP_NEIGHBOR_AF with admin_status = true
}
```

### Fix 2: Discover RR clients from topology

Updated `addRouteReflectorEntries()` to iterate **all** topology devices
(not just other RRs) and create overlay BGP_NEIGHBOR entries for non-RR
devices with loopback IPs:

```go
// For each non-RR device with a loopback, create an RR-client neighbor
for name, device := range topology.Devices {
    if device.Role == "route-reflector" { continue } // skip other RRs
    if device.LoopbackIP == "" { continue }
    // Create BGP_NEIGHBOR with rrclient=true
}
```

## Lesson

BGP provisioners must handle implicit adjacencies (from topology links), not
just explicit neighbor definitions. For eBGP underlay, the topology graph
itself defines the neighbor relationships. For iBGP overlay with route
reflectors, the RR must have explicit client entries for every non-RR device
in the topology — these won't appear in any per-device config, they must be
derived from the full topology view.
