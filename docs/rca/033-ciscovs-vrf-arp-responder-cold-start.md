# RCA-033: CiscoVS NGDP VRF ARP Responder Requires Outbound Packet to Activate

**Status**: Fixed (prime-arp step in vrf-routing scenario)

**Note (Feb 2026):** The `2node-incremental` suite has been replaced by `2node-primitive` (21 scenarios, all passing on CiscoVS). References to `2node-incremental` in this document refer to the predecessor suite.

**Component**: CiscoVS NGDP (Silicon One network simulator), `newtest/suites/2node-incremental/35-vrf-routing.yaml`
**Affected**: Any test that configures a VRF interface IP and expects a host to reach it immediately
**Discovered**: 2026-02-19

---

## Symptom

After creating a VRF, binding an interface, and assigning an IP address, a host on the
connected segment cannot ping the switch gateway:

```
host6# ping -c 3 172.16.1.1
From 172.16.1.2 icmp_seq=1 Destination Host Unreachable
From 172.16.1.2 icmp_seq=2 Destination Host Unreachable
From 172.16.1.2 icmp_seq=3 Destination Host Unreachable
```

The switch CAN ping the host immediately:

```
switch2# ping -c 1 -I Ethernet3 172.16.1.2
PING 172.16.1.2 (172.16.1.2) from 172.16.1.1 Ethernet3: 56(84) bytes of data.
64 bytes from 172.16.1.2: icmp_seq=1 ttl=64 time=1.2 ms

--- 172.16.1.2 ping statistics ---
1 packets transmitted, 1 received, 0% packet loss
```

After switch2 pings host6 once, host6 can ping 172.16.1.1 successfully.

---

## Root Cause

CiscoVS uses the Silicon One NGDP (next-generation dataplane) simulator for packet
forwarding. When a new VRF interface IP is programmed via CONFIG_DB → orchagent → SAI,
NGDP initialises the routing entry for the subnet but does **not** immediately activate
the ARP responder for the interface.

The ARP responder is activated lazily on first egress: when NGDP forwards the first
outbound ARP request via the VRF interface, it registers the interface with the ARP
subsystem and enables inbound ARP reply processing.

Until the first outbound ARP:
- NGDP ignores inbound ARP requests for the VRF interface IP
- Hosts receive no ARP reply → "Destination Host Unreachable"

After the first outbound ARP (switch pings host):
- NGDP processes host's ARP request for the gateway IP
- ARP reply sent → host learns switch MAC → ping succeeds

This is asymmetric: the switch can always initiate ARP (NGDP actively sends ARP for
destinations it wants to reach), but does not passively respond to ARP until it has
sent at least one ARP via the interface.

---

## Impact

Any scenario that:
1. Creates a VRF interface IP on CiscoVS
2. Immediately has a host ping the switch gateway

…will fail with "Destination Host Unreachable" even if the CONFIG_DB programming is
correct and the wait time is generous (15s was observed to be insufficient).

Non-VRF interfaces (provisioned at startup with full NGDP initialisation) are not
affected. Only freshly-created VRF interfaces show this behaviour.

---

## Fix

After configuring the host's IP, add a step where the **switch pings the host** first
(prime-ARP). This sends an outbound ARP from NGDP via the VRF interface, activating
the ARP responder. The host's subsequent ping to the switch gateway then succeeds.

In `35-vrf-routing.yaml`:

```yaml
# Prime ARP from the switch side first (CiscoVS NGDP cold-start, see RCA-033).
# NGDP's ARP responder for a new VRF interface only activates after the interface
# sends at least one outgoing ARP request.
- name: switch2-prime-arp
  action: ssh-command
  devices: [switch2]
  command: "ping -c 1 -W 5 -I Ethernet3 172.16.1.2"

# host6 pings switch2's Ethernet3 gateway (verifies VRF L3 forwarding works).
- name: host6-ping-gateway
  action: host-exec
  devices: [host6]
  command: "ping -c 3 -W 2 172.16.1.1"
  expect:
    success_rate: 0.8
```

The `switch2-prime-arp` step itself succeeds (verifying NGDP can reach the host), and
its success guarantees that `host6-ping-gateway` will also succeed.

---

## Why This Wasn't Caught in 3node

The 3node topology does not have a scenario that creates a fresh VRF interface IP and
immediately tests host → switch ARP. The vrf-routing scenario is 2node-specific.

---

## Workaround

If adding a prime-ARP step is not possible (e.g., in a different test framework),
increasing the wait time to 60+ seconds may help on some NGDP versions, but is not
reliable. The deterministic fix is the prime-ARP step.
