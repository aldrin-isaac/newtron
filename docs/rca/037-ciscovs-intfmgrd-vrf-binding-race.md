# RCA-037: intfmgrd VRF Binding Race at Provision Time

## Status
Documented workaround in place. Upstream fix would require intfmgrd to retry VRF binding
when the kernel VRF device is not yet present.

## Symptom

After provisioning a device where both a VRF and a VRF-bound interface are written to
CONFIG_DB simultaneously, the kernel does not reflect the binding:

```bash
# Expected after provision:
$ ip link show Ethernet2
Ethernet2: ... master CUSTOMER ...

# Actual:
$ ip link show Ethernet2
Ethernet2: ... (no master field — not bound to any VRF)

$ ip addr show Ethernet2
Ethernet2: ... (no IPv4 address)
```

Observed on CiscoVS 2node topology where `switch2:Ethernet2` is provisioned into the
CUSTOMER VRF at topology deploy time. Despite `VRF|CUSTOMER` and `INTERFACE|Ethernet2`
(with `vrf_name=CUSTOMER`) being present in CONFIG_DB, the kernel VRF binding and IP
assignment are absent after provision.

Downstream effect: `sudo ping -I Ethernet2 <host_ip>` fails because `ping -I` requires a
source IP on the interface, which is missing.

## Root Cause

SONiC provisioning writes all CONFIG_DB entries in a single Redis pipeline (atomic batch).
Two SONiC daemons process the relevant entries independently:

- **vrfmgrd**: processes `VRF|CUSTOMER` → creates the kernel VRF device (`ip link add CUSTOMER type vrf table ...`)
- **intfmgrd**: processes `INTERFACE|Ethernet2` (with `vrf_name=CUSTOMER`) → calls `ip link set Ethernet2 master CUSTOMER`

Both daemons receive their entries from the same keyspace notification event burst. The
race condition occurs when:

1. intfmgrd processes `INTERFACE|Ethernet2` before vrfmgrd has created the CUSTOMER kernel VRF device
2. `ip link set Ethernet2 master CUSTOMER` fails because CUSTOMER does not yet exist in the kernel
3. **intfmgrd does not retry** the VRF binding after vrfmgrd creates the device
4. The IP entry `INTERFACE|Ethernet2|10.10.1.1/31` is never programmed (depends on VRF binding)

The result is that Ethernet2 remains in the default VRF with no IP in the kernel,
even though CONFIG_DB is fully correct.

## Contrast: Why Vrf_dp_test Works

In `35-vrf-routing.yaml`, the VRF and interface entries are written sequentially (separate
newtron operations), not in a single pipeline. By the time intfmgrd processes
`INTERFACE|Ethernet3`, vrfmgrd has already created `Vrf_dp_test` in the kernel. No race.

## Timing

The race is most likely on fresh boots where multiple CONFIG_DB entries are processed
simultaneously. On CiscoVS, orchagent and the SAI Silicon One layer add additional latency
(60-90s total for full VRF interface initialization) which amplifies the race window.

## Fix

Kernel-level workaround in `newtest/suites/2node-incremental/01-provision.yaml`:

```yaml
- name: fix-vrf-kernel-binding-switch2
  action: ssh-command
  devices: [switch2]
  command: >-
    for i in 1 2 3 4 5 6; do
      ip link show type vrf | grep -q CUSTOMER && break
      sleep 5
    done;
    ip link show Ethernet2 | grep -q 'master CUSTOMER' ||
      sudo ip link set Ethernet2 master CUSTOMER;
    ip addr show Ethernet2 | grep -q '10.10.1.1' ||
      sudo ip addr add 10.10.1.1/31 dev Ethernet2;
    true
```

The step:
1. Waits for vrfmgrd to create the CUSTOMER kernel VRF device (polls up to 30s)
2. Checks whether intfmgrd already set `master CUSTOMER` on Ethernet2
3. If not, forces the kernel VRF binding directly via `ip link set`
4. Checks whether intfmgrd already assigned the IP
5. If not, forces the IP assignment via `ip addr add`

This step runs after `start-vlan-daemons` and before `prime-ngdp-arp-customer-vrf`.
The subsequent prime-arp step requires a source IP on Ethernet2 to send ARP requests.

## Timing Fix for vrf-routing

Related: `35-vrf-routing.yaml` replaced the fixed `wait-kernel-propagation: 30s` with
a polling step `wait-ethernet3-ip` that polls for `ip addr show Ethernet3` to report
`172.16.1.1`, up to 90s. This avoids the test failing when orchagent/intfmgrd take
longer than 30s to initialize a new VRF interface on CiscoVS.

## Upstream Resolution

The correct fix would be for intfmgrd to subscribe to netlink VRF device creation events
and retry any pending VRF interface bindings when a new kernel VRF device appears. This
would eliminate the race without requiring external workarounds.

Filed/tracked in: (no upstream issue filed yet)
