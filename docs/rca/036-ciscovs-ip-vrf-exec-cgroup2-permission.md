# RCA-036: `ip vrf exec` Fails over SSH without sudo (cgroup2 Permission Denied)

## Status
Documented workaround in place. Upstream fix would require SONiC SSH session cgroup configuration.

**Note (Feb 2026):** The `2node-incremental` suite has been replaced by `2node-primitive` (21 scenarios, all passing on CiscoVS). References to `2node-incremental` in this document refer to the predecessor suite.

## Symptom

Running `ip vrf exec <VRF> <command>` over an SSH session (without sudo) fails with:

```
mkdir failed for /sys/fs/cgroup/unified/system.slice/ssh.service/vrf: Permission denied
```

The exit code is non-zero. When the command is wrapped with `; true` or `|| true`, the error is
silently masked, making the step appear to succeed while actually doing nothing.

Observed in 2node-incremental test suite provisioning steps that tried to prime the CiscoVS NGDP
ARP responder for VRF interfaces using `ip vrf exec CUSTOMER ping -c 1 -I EthernetX <host_ip>`.

## Root Cause

`ip vrf exec` works by:
1. Creating a cgroup subdirectory under the system slice for the VRF process isolation
2. Moving the spawned process into that cgroup
3. Setting the network namespace to the VRF

Step 1 requires write access to `/sys/fs/cgroup/unified/system.slice/ssh.service/`, which
is owned by the SSH service and not writable by regular (non-root) users. SSH sessions
running as a regular user (e.g., `admin`) cannot create subdirectories in the systemd SSH
service cgroup hierarchy.

This is a Linux cgroup2 permission model constraint. SSH sessions run under `system.slice/ssh.service`,
and the session user does not have write permission to create VRF cgroup subdirectories there.

## Impact

- Any `ip vrf exec <VRF> ping` call in test YAML steps fails silently when masked with `|| true`
- NGDP ARP priming steps that relied on `ip vrf exec` produced no outbound ARPs
- VRF interface reachability tests failed because NGDP ARP responder was never activated

## Fix

Use `sudo ping -I <interface> <host_ip>` instead of `ip vrf exec <VRF> ping`.

The `-I <interface>` flag tells ping to use a specific interface as the source, which achieves
the same NGDP ARP priming effect without needing VRF exec context:
- The ARP request is sent out `<interface>`, which is bound to the VRF
- NGDP sees the outgoing ARP and activates the ARP responder for that VRF interface IP
- sudo is required because raw socket creation for ping needs elevated privileges

### Applied Changes

`newtrun/suites/2node-primitive/01-provision.yaml` — `prime-ngdp-arp-customer-vrf` step:
```bash
# Before (broken):
ip vrf exec CUSTOMER ping -c 1 -W 5 10.10.1.0

# After (fixed):
sudo ping -c 1 -W 5 -I Ethernet2 10.10.1.0
```

`newtrun/suites/2node-primitive/35-vrf-routing.yaml` — `switch2-prime-arp` step:
```bash
# Before (broken):
ip vrf exec Vrf_dp_test ping -c 1 -W 5 -I Ethernet3 172.16.1.2

# After (fixed):
sudo ping -c 1 -W 5 -I Ethernet3 172.16.1.2
```

## Prevention

Do not use `ip vrf exec` in SSH-based test steps without `sudo`. The recommended pattern
for ARP priming from the switch side is always `sudo ping -I <interface> <target_ip>`.
