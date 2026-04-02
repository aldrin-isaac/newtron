# newtlab — HOWTO Guide

newtlab realizes network topologies as connected QEMU virtual machines. It
reads newtron's spec files (`topology.json`, `platforms.json`, `profiles/`)
and manages VM lifecycle without root privileges.

For the architectural principles behind newtron, newtlab, and newtrun, see
[Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md).

---

## Prerequisites

### Required Software

```bash
# QEMU with KVM support
sudo apt install qemu-system-x86 qemu-utils

# socat for serial console access
sudo apt install socat

# Verify KVM is available (recommended for performance)
ls /dev/kvm
```

Without KVM, QEMU falls back to TCG emulation — functional but an order of
magnitude slower. SONiC VMs that boot in 2 minutes under KVM can take 20+
minutes under TCG.

### VM Images

Download or build SONiC VM images and place them in `~/.newtlab/images/`:

```bash
mkdir -p ~/.newtlab/images

# CiscoVS (Cisco Silicon One virtual PFE — full dataplane)
cp sonic-ciscovs.qcow2 ~/.newtlab/images/

# VPP (VPP-based forwarding — no EVPN VXLAN support)
cp sonic-vpp.qcow2 ~/.newtlab/images/
```

The exact image path is configured per-platform in `platforms.json` (§3).

---

## Quick Start

Deploy a topology, SSH in, and tear down — the minimal path:

```bash
# 1. Build newtlab
go build -o bin/newtlab ./cmd/newtlab

# 2. Deploy VMs from a spec directory
bin/newtlab deploy -S newtrun/topologies/2node-ngdp/specs

# 3. Provision CONFIG_DB on all switches
bin/newtlab provision -S newtrun/topologies/2node-ngdp/specs

# 4. SSH to a device
bin/newtlab ssh switch1

# 5. Tear down
bin/newtlab destroy
```

Steps 2 and 3 can be combined:

```bash
bin/newtlab deploy -S newtrun/topologies/2node-ngdp/specs --provision
```

The rest of this guide covers each step in detail with all flags, example
output, and troubleshooting.

---

## End-to-End Workflow

This walkthrough deploys the `2node-ngdp-service` topology — two CiscoVS switches
with eight virtual hosts — and exercises the full lifecycle: deploy, provision,
status, data plane test, and teardown.

**Deploy with provisioning:**

```bash
newtlab deploy -S newtrun/topologies/2node-ngdp-service/specs --provision
```

```
Deploying VMs...
  [bridges] starting 10 bridge workers
  [start] booting 3 VMs
  [start] booted hostvm-0 (pid 54320)
  [start] booted switch1 (pid 54321)
  [start] booted switch2 (pid 54322)
  [bootstrap] configuring 3 nodes via serial
  [patch] applying boot patches
  [hosts] provisioning host namespaces
  [ready] all nodes ready

✓ Deployed 2node-ngdp-service (11 nodes)

  NODE      STATUS   SSH PORT  CONSOLE
  host1     running  13000     12000
  host2     running  13000     12000
  host3     running  13000     12000
  host4     running  13000     12000
  host5     running  13000     12000
  host6     running  13000     12000
  host7     running  13000     12000
  host8     running  13000     12000
  hostvm-0  running  13000     12000
  switch1   running  13008     12008
  switch2   running  13009     12009

Provisioning devices...
✓ Provisioning complete
```

The 8 virtual hosts share a single QEMU VM (`hostvm-0`) and thus share
its SSH and console ports. Switches get their own ports (indices 8 and 9 in
the sorted device list, so `ssh_port_base + 8` and `ssh_port_base + 9`).

**Check status:**

```bash
newtlab status 2node-ngdp-service
```

```
Lab: 2node-ngdp-service (deployed 2026-03-05 10:30:00)
Spec dir: /home/user/newtrun/topologies/2node-ngdp-service/specs

  NODE      TYPE                   STATUS   IMAGE              SSH    CONSOLE  PID
  host1     vhost:hostvm-0/host1   running  alpine-testhost    13000  12000    54320
  host2     vhost:hostvm-0/host2   running  alpine-testhost    13000  12000    54320
  host3     vhost:hostvm-0/host3   running  alpine-testhost    13000  12000    54320
  host4     vhost:hostvm-0/host4   running  alpine-testhost    13000  12000    54320
  host5     vhost:hostvm-0/host5   running  alpine-testhost    13000  12000    54320
  host6     vhost:hostvm-0/host6   running  alpine-testhost    13000  12000    54320
  host7     vhost:hostvm-0/host7   running  alpine-testhost    13000  12000    54320
  host8     vhost:hostvm-0/host8   running  alpine-testhost    13000  12000    54320
  hostvm-0  host-vm                running  alpine-testhost    13000  12000    54320
  switch1   switch                 running  sonic-ciscovs      13008  12008    54321
  switch2   switch                 running  sonic-ciscovs      13009  12009    54322

  LINK                                     STATUS     A→Z       Z→A       SESSIONS
  switch1:Ethernet0 ↔ switch2:Ethernet0    connected  24.5 KB   24.5 KB   2
  switch1:Ethernet1 ↔ host1:eth0           connected  1.2 KB    0 B       2
  switch1:Ethernet5 ↔ switch2:Ethernet5    connected  0 B       0 B       2
```

The node table shows TYPE to distinguish switches, the coalesced host-vm,
and virtual hosts (which show their parent VM and namespace). The link table
shows bridge stats — traffic counters and session count per link.

**SSH to a switch, verify BGP:**

```bash
newtlab ssh switch1
```

```
admin@switch1:~$ show ip bgp summary
...
```

**SSH to a virtual host, verify connectivity:**

```bash
newtlab ssh host1
```

```
host1:~# ping -c 3 10.100.1.1
PING 10.100.1.1 (10.100.1.1): 56 data bytes
64 bytes from 10.100.1.1: seq=0 ttl=64 time=1.234 ms
...
```

`newtlab ssh host1` SSHes to the parent VM and enters the `host1` network
namespace transparently — the user never interacts with `hostvm-0` directly.

**Data plane test between hosts:**

```bash
# Terminal 1: start iperf3 server on host1 (connected to switch1)
newtlab ssh host1
host1:~# iperf3 -s

# Terminal 2: run iperf3 client from host4 (connected to switch2)
newtlab ssh host4
host4:~# iperf3 -c 10.100.1.10 -t 5
```

Traffic flows: host4 → switch2 → switch1 → host1. This exercises the
full data plane across both switches and the newtlink bridge network.

**Tear down:**

```bash
newtlab destroy 2node-ngdp-service
```

```
Destroying lab 2node-ngdp-service...
  [stop] stopping hostvm-0
  [stop] stopping switch1
  [stop] stopping switch2
  [bridges] stopping bridge workers

✓ Lab 2node-ngdp-service destroyed
```

Destroy kills the 3 QEMU processes (virtual hosts are killed implicitly when
their parent VM stops), stops bridge workers, restores profiles, and removes
all state.

---

## Spec File Configuration

newtlab reads three configuration sources: `topology.json` for the lab
layout, `platforms.json` for VM images and settings, and `profiles/<device>.json`
for per-device overrides. All three live in the same spec directory passed
via `-S`.

### topology.json — newtlab Section

The `newtlab` key controls port allocation and multi-host settings. newtron
ignores this section; it is newtlab-specific.

```json
{
  "devices": { "..." : "..." },
  "newtlab": {
    "link_port_base": 10000,
    "console_port_base": 12000,
    "ssh_port_base": 13000,
    "servers": [
      { "name": "server-a", "address": "192.168.1.10", "max_nodes": 4 },
      { "name": "server-b", "address": "192.168.1.11", "max_nodes": 4 }
    ]
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Base TCP port for newtlink bridge workers. Link *i* uses ports `base + i*2` and `base + i*2 + 1`. |
| `console_port_base` | 30000 | Base TCP port for QEMU serial consoles. Device *i* (sorted) uses `base + i`. |
| `ssh_port_base` | 40000 | Base TCP port for QEMU SSH forwarding. Device *i* (sorted) uses `base + i`. |
| `servers` | *(none)* | Server pool for multi-host deployment (§10). |
| `hosts` | *(none)* | Legacy: server name → IP map. Use `servers` for new topologies. |

### platforms.json — VM Settings

Each platform defines the SONiC image, VM resources, and interface mapping:

```json
{
  "platforms": {
    "sonic-ciscovs": {
      "hwsku": "cisco-p200-32x100-vs",
      "port_count": 32,
      "default_speed": "100G",
      "vm_image": "~/.newtlab/images/sonic-ciscovs.qcow2",
      "vm_memory": 8192,
      "vm_cpus": 6,
      "vm_nic_driver": "e1000",
      "vm_interface_map": "sequential",
      "vm_credentials": { "user": "aldrin", "pass": "YourPaSsWoRd" },
      "vm_boot_timeout": 600,
      "dataplane": "ciscovs"
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `vm_image` | *(required)* | Path to QCOW2 base image. Tilde-expanded. |
| `vm_memory` | 4096 | Memory in MB. |
| `vm_cpus` | 2 | Number of vCPUs. |
| `vm_nic_driver` | `"e1000"` | QEMU NIC driver: `"e1000"` or `"virtio-net-pci"`. |
| `vm_interface_map` | `"stride-4"` | How NIC index maps to SONiC interface names. |
| `vm_cpu_features` | `""` | QEMU CPU feature flags (e.g., `"+sse4.2"` for VPP). |
| `vm_credentials` | *(none)* | Image-baked username and password. |
| `vm_boot_timeout` | 180 | Seconds to wait for SSH readiness. |
| `dataplane` | `""` | Selects boot patch directory: `"ciscovs"`, `"vpp"`, or `""` (none). |
| `vm_image_release` | `""` | Selects release-specific boot patches (e.g., `"202505"`). |
| `device_type` | `""` | Set to `"host"` for virtual host platforms (§9). |

**Example: VPP platform** (different driver and CPU features):

```json
{
  "sonic-vpp": {
    "hwsku": "Force10-S6000",
    "port_count": 32,
    "default_speed": "40000",
    "vm_image": "~/.newtlab/images/sonic-vpp.qcow2",
    "vm_memory": 4096,
    "vm_cpus": 4,
    "vm_nic_driver": "virtio-net-pci",
    "vm_interface_map": "sequential",
    "vm_cpu_features": "+sse4.2",
    "vm_credentials": { "user": "admin", "pass": "YourPaSsWoRd" },
    "vm_boot_timeout": 300,
    "dataplane": "vpp"
  }
}
```

VPP requires `virtio-net-pci` (not `e1000`) and the `+sse4.2` CPU feature
flag. CiscoVS uses `e1000` and needs no special CPU features.

#### Interface Maps

Different SONiC images map QEMU NIC indices to interface names differently.
NIC 0 is always reserved for management.

| Map Type | NIC 1 | NIC 2 | NIC 3 | NIC 4 | Used By |
|----------|-------|-------|-------|-------|---------|
| `sequential` | Ethernet0 | Ethernet1 | Ethernet2 | Ethernet3 | CiscoVS, VPP |
| `stride-4` | Ethernet0 | Ethernet4 | Ethernet8 | Ethernet12 | Legacy VS |
| `linux` | eth1 | eth2 | eth3 | eth4 | Alpine host VMs |
| `custom` | *(caller-provided map, not yet configurable via platform spec)* | | | | Vendor-specific |

### Profile VM Overrides

Individual devices can override VM resources in `profiles/<device>.json`:

```json
{
  "platform": "sonic-ciscovs",
  "vm_memory": 8192,
  "vm_cpus": 4,
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd"
}
```

### Resolution Order

VM configuration resolves per-field (first non-zero wins):

| Setting | Profile | Platform | Built-in Default |
|---------|---------|----------|------------------|
| Image | `vm_image` | `vm_image` | *(error)* |
| Memory | `vm_memory` | `vm_memory` | 4096 MB |
| CPUs | `vm_cpus` | `vm_cpus` | 2 |
| NIC driver | — | `vm_nic_driver` | `"e1000"` |
| Interface map | — | `vm_interface_map` | `"stride-4"` |
| CPU features | — | `vm_cpu_features` | `""` |
| SSH user | `ssh_user` | — | `"admin"` |
| SSH password | `ssh_pass` | `vm_credentials.pass` | `""` |
| Boot timeout | — | `vm_boot_timeout` | 180s |

NIC driver, interface map, CPU features, and boot timeout are platform-only —
profiles cannot override them.

---

## Deploying a Lab

Deploy creates QEMU VMs from spec files, sets up inter-VM links via newtlink
bridge workers, and waits for all devices to become SSH-reachable.

**Preconditions:**
- Spec directory exists with `topology.json`, `platforms.json`, and `profiles/`
- VM images referenced in `platforms.json` exist at the specified paths
- KVM available (or accept slow TCG fallback)
- Port ranges are free (newtlab probes all ports before starting)

**Command:**

```bash
newtlab deploy -S <specs> [flags]
```

| Flag | Description |
|------|-------------|
| `--force` | Destroy existing lab first, then deploy fresh. |
| `--provision` | Run newtron provisioning after deploy completes. |
| `--parallel <n>` | Parallel provisioning threads (only with `--provision`). Default: 1. |
| `--host <name>` | Multi-host mode: deploy only nodes assigned to this server (§10). |

**Example output** (2-switch topology with virtual hosts):

```
Deploying VMs...
  [bridges] starting 9 bridge workers
  [start] booting 3 VMs
  [start] booted hostvm-0 (pid 54320)
  [start] booted switch1 (pid 54321)
  [start] booted switch2 (pid 54322)
  [bootstrap] configuring 3 nodes via serial
  [patch] applying boot patches
  [hosts] provisioning host namespaces
  [ready] all nodes ready

✓ Deployed 2node-ngdp (9 nodes)

  NODE      STATUS   SSH PORT  CONSOLE
  host1     running  13000     12000
  host2     running  13000     12000
  host3     running  13000     12000
  host4     running  13000     12000
  host5     running  13000     12000
  host6     running  13000     12000
  hostvm-0  running  13000     12000
  switch1   running  13006     12006
  switch2   running  13007     12007
```

Progress phases: `[bridges]` starts newtlink workers, `[start]` launches QEMU
and reports each node's PID, `[bootstrap]` configures management networking
via serial console and waits for SSH, `[patch]` applies platform boot patches,
`[hosts]` provisions network namespaces inside coalesced host VMs (§9),
`[ready]` signals completion. Virtual hosts share the parent VM's SSH and
console ports — see §9 for details.

**Deploy with provisioning:**

```bash
newtlab deploy -S specs/ --provision
```

This runs the full deploy, then automatically provisions each switch via
newtron (equivalent to running `newtlab provision` separately).

**What can go wrong:**

- **Port conflict**: newtlab probes all allocated ports (SSH, console, link,
  bridge stats) before starting any VMs. If another process occupies a port,
  deploy fails with a clear error identifying the conflicting port. Fix: stop
  the conflicting process or change port bases in `topology.json`.

- **Image not found**: check `vm_image` in `platforms.json`. Tilde (`~`) is
  expanded.

- **Stale lab**: if a previous deploy left state behind (`~/.newtlab/labs/<name>/`),
  deploy refuses to overwrite. Use `--force` to destroy and redeploy, or run
  `newtlab destroy` first.

- **Boot timeout**: CiscoVS images need 600s boot timeout (the Silicon One PFE
  takes several minutes to initialize). VPP needs 300s. If SSH readiness
  times out, increase `vm_boot_timeout` in `platforms.json`.

**What deploy does internally:**

1. Creates overlay disks (copy-on-write over base images)
2. Starts newtlink bridge worker processes for inter-VM links
3. Launches QEMU for each node
4. Bootstraps management networking (eth0 via console)
5. Waits for SSH readiness on each node
6. Generates and injects an Ed25519 SSH key (passwordless access)
7. Applies platform-specific boot patches (§12)
8. Patches device profiles with allocated ports (so newtron can connect)
9. Provisions virtual host namespaces if hosts are defined (§9)

---

## Provisioning Devices

Provisioning writes the CONFIG_DB composite (derived from spec files) to each
switch via newtron. This configures BGP, interfaces, services, and all other
SONiC subsystems.

**Preconditions:**
- Lab is deployed and all switches are SSH-reachable
- Spec files contain the device profiles and network configuration

**Command:**

```bash
newtlab provision <topology> [flags]
newtlab provision -S <specs> [flags]
```

| Flag | Description |
|------|-------------|
| `--device <name>` | Provision only this device. |
| `--parallel <n>` | Parallel provisioning threads. Default: 1. |

**Example:**

```bash
# Provision all switches
newtlab provision -S specs/

# Provision a single device
newtlab provision -S specs/ --device switch1

# Parallel provisioning (4 threads)
newtlab provision -S specs/ --parallel 4
```

```
Provisioning devices...
✓ Provisioning complete
```

Provisioning runs `newtron <device> --topology intent reconcile -x` for each
switch — a topology-mode full reconcile that replays the topology.json steps
and delivers the resulting CONFIG_DB projection to the device.
Host and host-vm devices are skipped (they have no CONFIG_DB).

After all devices are provisioned, newtlab waits 5 seconds and then runs
`vtysh -c 'clear bgp * soft'` on every switch to ensure BGP sessions refresh
with the new configuration.

**What can go wrong:**

- **SSH unreachable**: check `newtlab status` — the device may have crashed
  or timed out during boot patches.
- **Provision error**: newtron reports the error. Common causes: missing spec
  fields, invalid service type, prerequisite CONFIG_DB state not present (e.g.,
  EVPN setup requires loopback IP in profile).

---

## Checking Status

### Lab Overview

Without arguments, `newtlab status` shows all deployed labs:

```bash
newtlab status
```

If no labs are deployed, this prints `no deployed labs`.

### Detailed Lab Status

With a topology name or `-S` flag, `newtlab status` shows per-node and
per-link details:

```bash
newtlab status <topology>
newtlab status -S <specs>
```

**Node table:**

```
Lab: 2node-ngdp (deployed 2026-03-01 14:30:00)
Spec dir: /home/user/newtrun/topologies/2node-ngdp/specs

  NODE      TYPE                   STATUS   IMAGE              SSH    CONSOLE  PID
  host1     vhost:hostvm-0/host1   running  alpine-testhost    13000  12000    54320
  host2     vhost:hostvm-0/host2   running  alpine-testhost    13000  12000    54320
  hostvm-0  host-vm                running  alpine-testhost    13000  12000    54320
  switch1   switch                 running  sonic-ciscovs      13006  12006    54321
  switch2   switch                 running  sonic-ciscovs      13007  12007    54322
```

Columns: `NODE`, `TYPE`, `STATUS`, `IMAGE`, `SSH`, `CONSOLE`, `PID`.
In multi-host mode, a `HOST` column is added after `STATUS`.

STATUS values: `running` (green), `stopped` (yellow), `error` (red), or a
boot phase like `booting`, `bootstrapping`, `patching` (yellow) if the VM
is still initializing.

TYPE values: `switch` (default), `host-vm` (coalesced host VM), or
`vhost:<vmname>/<namespace>` for logical virtual hosts.

IMAGE shows the base image filename with extensions stripped. Displays `—`
if empty.

**Link table:**

```
  LINK                                STATUS     A→Z        Z→A        SESSIONS
  switch1:Ethernet0 ↔ switch2:Ethernet0  connected  1.2 MB     856.0 KB   3
```

Columns: `LINK`, `STATUS`, `A→Z`, `Z→A`, `SESSIONS`.
In multi-host mode, a `HOST` column is added after `STATUS`.

STATUS values: `connected` (green), `waiting` (yellow — bridge running but
peer not connected), or `—` (no bridge stats available).

Traffic counters use human-readable units (B, KB, MB, GB).

**Flags:**

| Flag | Description |
|------|-------------|
| `--json` | Machine-readable JSON output. |

### Lab Inventory

`newtlab list` shows all topologies and their deployment status:

```bash
newtlab list
```

```
  TOPOLOGY        DEVICES  LINKS  STATUS    NODES
  2node-ngdp      2        1      deployed  2/2 running
  2node-ngdp-service   10       8      deployed  3/3 running
  3node-ngdp      3        4      —
```

STATUS values: `deployed` (green), `degraded` (yellow — some nodes stopped),
`stopped` (yellow — all nodes stopped), or `—` (not deployed).

NODES shows `N/M running` for deployed and degraded labs, or `0/M` for
stopped labs.

---

## Accessing VMs

### SSH to Switches

```bash
newtlab ssh <node>
```

This finds the node across all deployed labs (or within a specific lab if
`-S` is provided) and opens an interactive SSH session:

```bash
newtlab ssh switch1
```

Behind the scenes, this runs:
```
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -i <lab-ssh-key> -o PasswordAuthentication=no \
    -p <ssh-port> admin@127.0.0.1
```

The default SSH user is `admin` for switches. When a lab SSH key exists
(generated during deploy), it is used for passwordless authentication.

### SSH to Virtual Hosts

```bash
newtlab ssh host1
```

For virtual hosts (network namespaces inside a shared VM), newtlab SSHes
to the parent host-vm and enters the namespace:

```
ssh ... -t -p <ssh-port> root@127.0.0.1 "ip netns exec host1 bash -l"
```

The default SSH user for virtual hosts is `root`.

### Direct SSH (Without newtlab)

You can SSH directly using the allocated port:

```bash
# Find the port
newtlab status <topology>

# Connect
ssh -p 13006 admin@127.0.0.1
```

The port comes from `newtlab status` (the SSH column). For remote hosts in
multi-host mode, replace `127.0.0.1` with the server IP.

### Console Access

For debugging boot issues when SSH is not yet available:

```bash
newtlab console <node>
```

This connects to the QEMU serial console via `socat` (preferred) or `telnet`:

```bash
# socat (preferred) — detach with Ctrl+C
socat -,rawer TCP:127.0.0.1:<console-port>

# telnet (fallback) — detach with Ctrl+]
telnet 127.0.0.1 <console-port>
```

### Lab SSH Key

Deploy generates an Ed25519 SSH key pair stored in the lab state directory
(`~/.newtlab/labs/<name>/id_ed25519`). This key is injected into every VM
during bootstrap, enabling passwordless SSH. `newtlab ssh` uses this key
automatically.

If SSH key injection fails (e.g., the VM's SSH server doesn't accept key
auth), newtlab falls back to password authentication using the credentials
from the platform or profile.

**What can go wrong:**

- **Node not found**: `newtlab ssh` searches all deployed labs. If the node
  name doesn't match any deployed device, you get an error listing available
  nodes. Use the exact name from `topology.json`.

- **Connection refused**: the VM may still be booting. Check `newtlab status`
  for the node's status and phase. Use `newtlab console` to see the boot
  output.

- **Permission denied**: if the lab SSH key was not injected (e.g., key
  injection failed during deploy), try with explicit password:
  `ssh -p <port> admin@127.0.0.1` and enter the platform password.

---

## Node Management

### Stop a Node

Stopping a node kills the QEMU process but preserves the overlay disk:

```bash
newtlab stop <node>
```

```
✓ Stopped switch1
```

The VM's disk state is preserved. CONFIG_DB, filesystem changes, and
installed packages survive the stop. Restarting resumes from the saved
disk.

### Start a Stopped Node

```bash
newtlab start <node>
```

```
Starting switch1...
✓ Started switch1
```

Start launches QEMU using the existing overlay disk and waits for SSH
readiness before returning. Boot patches are NOT re-applied — the disk
already has the patched state from the original deploy.

Bridge workers for links involving this node must still be running (they
were started during the original deploy and run independently of QEMU
processes).

### Destroy a Lab

Destroy tears down the entire lab — kills all VMs, stops all bridge workers,
cleans up state:

```bash
newtlab destroy <topology>
newtlab destroy              # auto-selects if only one lab deployed
```

```
Destroying lab 2node-ngdp...
  [stop] stopping hostvm-0
  [stop] stopping switch1
  [stop] stopping switch2
  [bridges] stopping bridge workers

✓ Lab 2node-ngdp destroyed
```

**What destroy cleans up:**
- Kills all QEMU processes (switch VMs and host VMs)
- Stops all newtlink bridge worker processes
- Cleans up remote host state (in multi-host mode)
- Restores device profiles to pre-deploy state (removes `ssh_port`,
  `console_port`, `mac`; restores original `mgmt_ip`)
- Deletes the overlay disks and lab state directory
  (`~/.newtlab/labs/<name>/`)

**What destroy does NOT clean up:**
- Base VM images (these are shared, read-only)
- The `~/.newtlab/images/` directory
- SSH user/password fields added to profiles

**What stop/start preserves vs. destroy:**

| | Stop + Start | Destroy + Deploy |
|---|---|---|
| CONFIG_DB state | Preserved | Fresh from provisioning |
| Boot patches | Preserved on disk | Re-applied |
| SSH key | Preserved | New key generated |
| Bridge workers | Kept running | Stopped and restarted |
| Overlay disk | Preserved | Deleted and recreated |

---

## Virtual Hosts

Virtual hosts are lightweight Alpine Linux VMs that act as data plane test
endpoints. Unlike switches, multiple virtual hosts share a single QEMU
instance via network namespace coalescing, reducing resource overhead.

### How It Works

- Multiple host devices (e.g., `host1`, `host2`) are coalesced into a single
  QEMU VM named `hostvm-0`
- Each host runs in its own network namespace inside the VM
- Namespaces are created at deploy time with pre-configured IPs
- From the user's perspective, each host appears as a separate SSH target

### Building the Alpine Image

```bash
tools/build-alpine-testhost.sh
```

This creates `~/.newtlab/images/alpine-testhost.qcow2` with:

- **Packages:** iproute2, iperf3, tcpdump, hping3, curl, nc, socat, bash, vim
- **Image:** 512 MB QCOW2 disk (grows as needed), Alpine Linux, virtio drivers
- **Config:** SSH enabled, cloud-init disabled, serial console on ttyS0
- **Credentials:** root / root (matching the `alpine-host` platform definition)

### Adding Hosts to a Topology

**1. Define the host platform in `platforms.json`:**

```json
{
  "alpine-host": {
    "description": "Alpine Linux test host",
    "device_type": "host",
    "vm_image": "~/.newtlab/images/alpine-testhost.qcow2",
    "vm_memory": 256,
    "vm_cpus": 1,
    "vm_nic_driver": "virtio-net-pci",
    "vm_interface_map": "linux",
    "vm_credentials": { "user": "root", "pass": "root" },
    "vm_boot_timeout": 60
  }
}
```

The key field is `"device_type": "host"` — this triggers VM coalescing.

**2. Add host devices to `topology.json`:**

```json
{
  "devices": {
    "switch1": {
      "interfaces": {
        "Ethernet0": { "link": "switch2:Ethernet0", "ip": "10.1.0.0/31" },
        "Ethernet1": { "link": "host1:eth0", "ip": "10.100.1.1/24" }
      }
    },
    "host1": {
      "type": "host",
      "interfaces": {
        "eth0": { "link": "switch1:Ethernet1" }
      }
    }
  }
}
```

Host interfaces use Linux naming (`eth0`, `eth1`). The `"type": "host"` marker
identifies the device for coalescing.

**3. Create host profiles (`profiles/host1.json`):**

```json
{
  "mgmt_ip": "127.0.0.1",
  "platform": "alpine-host",
  "ssh_user": "root",
  "ssh_pass": "root"
}
```

### IP Address Assignment

Virtual hosts need IPs for their data plane interfaces. newtlab supports two
methods:

**Auto-derivation (recommended):** newtlab reads the switch-side interface
IP from `topology.json`. For a link `switch1:Ethernet1 ↔ host1:eth0`, if
`switch1:Ethernet1` has IP `10.100.1.1/24`:

| Prefix length | Host IP derivation |
|---|---|
| `/31` | Toggle the last bit (e.g., `.0` ↔ `.1`) |
| `/30` | Switch IP + 1 |
| `/24` or wider | Offset based on host index: `(index+1) * 10` on last octet |

The switch-side IP becomes the default gateway.

**Manual override:** Set `host_ip` and `host_gateway` in the profile:

```json
{
  "platform": "alpine-host",
  "ssh_user": "root",
  "ssh_pass": "root",
  "host_ip": "10.100.1.100/24",
  "host_gateway": "10.100.1.1"
}
```

### Deploy and Access

```bash
# Deploy (creates shared VM + namespaces automatically)
newtlab deploy -S specs/

# SSH to a virtual host (enters namespace transparently)
newtlab ssh host1

# Inside the namespace
ip addr show eth0
ping 10.100.1.1    # Ping the switch gateway
```

### Status Display

Virtual hosts appear in `newtlab status` with their type showing the parent
VM and namespace:

```
  NODE      TYPE                   STATUS   IMAGE              SSH    CONSOLE  PID
  host1     vhost:hostvm-0/host1   running  alpine-testhost    13000  12000    54320
  host2     vhost:hostvm-0/host2   running  alpine-testhost    13000  12000    54320
  hostvm-0  host-vm                running  alpine-testhost    13000  12000    54320
  switch1   switch                 running  sonic-ciscovs      13006  12006    54321
  switch2   switch                 running  sonic-ciscovs      13007  12007    54322
```

Virtual hosts share the parent VM's SSH port, console port, and PID.
The PID column shows the parent VM's PID (since virtual hosts are network
namespaces inside that VM, not separate processes).

### Data Plane Testing

Virtual hosts exist for end-to-end data plane validation:

```bash
# On host1
iperf3 -s

# On host2
iperf3 -c 10.100.1.10 -t 10
```

Or via newtrun scenarios using the `host-exec` step action (see
`docs/newtrun/howto.md`).

---

## Multi-Host Deployment

For topologies larger than a single server can handle, newtlab distributes
VMs across a pool of servers.

### Server Pool Configuration

Define servers in `topology.json`:

```json
{
  "newtlab": {
    "servers": [
      { "name": "server-a", "address": "192.168.1.10", "max_nodes": 4 },
      { "name": "server-b", "address": "192.168.1.11", "max_nodes": 4 }
    ]
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Server name (referenced by `vm_host` for pinning) |
| `address` | Yes | IP address for cross-host TCP connections |
| `max_nodes` | No | Maximum VMs on this server (0 = unlimited) |

**Legacy `hosts` map:** The older `hosts` field (server name → IP map) is
still supported for backward compatibility, but requires manual `vm_host` in
every device profile. Prefer `servers` for new topologies:

```json
{
  "newtlab": {
    "hosts": {
      "server-a": "192.168.1.10",
      "server-b": "192.168.1.11"
    }
  }
}
```

### Auto-Placement

When servers are defined, newtlab auto-places nodes across them to minimize
maximum load. No manual `vm_host` configuration needed.

### Node Pinning

To force a specific node onto a specific server, set `vm_host` in the
device profile:

```json
{
  "platform": "sonic-ciscovs",
  "vm_host": "server-a"
}
```

Pinned nodes are validated against the server list and count toward capacity.
Unpinned nodes are distributed across remaining capacity.

### Deploy Per Host

Each server runs its own `newtlab deploy` with the `--host` flag:

```bash
# On server-a
newtlab deploy -S specs/ --host server-a

# On server-b
newtlab deploy -S specs/ --host server-b

# Provision from anywhere (connects to each device via its host IP)
newtlab provision -S specs/
```

### Cross-Host Links

For links between nodes on different servers, newtlink bridge workers handle
the traffic forwarding. Each bridge worker listens on `127.0.0.1` for its
local VM and on `0.0.0.0` for the remote VM, bridging frames between them.

Bridge workers are assigned to hosts to minimize cross-host worker count
(ties broken alphabetically by server name). Each bridge also exposes a TCP
stats endpoint for telemetry.

### Status Across Hosts

`newtlab status` in multi-host mode adds a `HOST` column to both node and
link tables:

```
  NODE     TYPE    STATUS   HOST           IMAGE           SSH    CONSOLE  PID
  switch1  switch  running  192.168.1.10   sonic-ciscovs   13006  12006    54321
  switch2  switch  running  192.168.1.11   sonic-ciscovs   13007  12007    54322
```

Nodes deployed locally show `local` in the HOST column.

**What can go wrong:**

- **Remote SSH failure**: newtlab SSHes to remote servers to create
  directories, upload newtlink, and manage VMs. Ensure passwordless SSH
  access to all servers.

- **newtlink version mismatch**: the `newtlink` binary at
  `~/.newtlab/bin/newtlink` on remote servers is shared across all users.
  newtlab checks the remote version — if it matches the local build, upload
  is skipped. If not, it uploads the local version.

- **Bridge not reachable**: if `newtlab status` shows `—` for a link's status,
  the bridge worker on the remote host may have stopped. Check the remote
  host with `ssh server-b "pgrep -la newtlink"`.

---

## Multi-User Environments

When multiple developers deploy to the same server, follow these conventions
to avoid port conflicts.

### Port Base Conventions

Each user should configure distinct port bases in `topology.json`. Spacing
bases by 1000 provides ample room:

| User | link_port_base | console_port_base | ssh_port_base |
|------|----------------|-------------------|---------------|
| Alice | 20000 | 30000 | 40000 |
| Bob | 21000 | 31000 | 41000 |
| Carol | 22000 | 32000 | 42000 |

### Lab Naming

Each lab writes state to `~/.newtlab/labs/<name>/` on the host. Use unique
topology names to ensure no overlap. Prefix with your username to avoid
collisions:

```json
{
  "name": "alice-spine-leaf",
  "devices": { "..." : "..." }
}
```

The topology name comes from the `topology.json` filename's parent directory
or from the `name` field if present.

### Port Conflict Detection

`ProbeAllPorts` runs automatically during deploy and checks all allocated
ports (SSH, console, link, bridge stats) on every host. If another process
occupies a port, deploy fails with a clear error message identifying the
conflicting port and host.

### Shared newtlink Binary

The uploaded `newtlink` binary at `~/.newtlab/bin/newtlink` on remote
servers is shared across all users of the same Unix account. Before
uploading, newtlab checks the remote version — if it matches the local
build, the upload is skipped. To use a specific newtlink version, set
`$NEWTLAB_BIN_DIR` to a directory containing your cross-compiled binaries.

---

## Platform Boot Patches

Some SONiC images have platform-specific initialization issues that newtlab
automatically patches after boot. Patches are embedded in the newtlab binary
and selected by the platform's `dataplane` field.

### How Patches Are Selected

1. newtlab reads the platform's `dataplane` value (e.g., `"ciscovs"`, `"vpp"`)
2. Loads all patches from `patches/<dataplane>/always/` (sorted by filename)
3. If `vm_image_release` is set, appends patches from
   `patches/<dataplane>/<release>/`
4. Applies each patch in order via SSH to the booted VM

### What Patches Can Do

Each patch is a JSON file that can:
- Run shell commands (`pre_commands`, `post_commands`)
- Disable files by renaming them to `.disabled`
- Upload rendered file templates (with Go `text/template` variables)
- Execute Redis commands against CONFIG_DB or other databases

Template variables available in patches include port count, PCI addresses,
HWSKU directory, port speed, and platform/dataplane/release identifiers.

### Built-In Patches

**CiscoVS** (`patches/ciscovs/always/`):
- Enables frrcfgd unified mode and restarts BGP service
- Patches frrcfgd with VNI bootstrap fix
- Waits for swss container readiness

**VPP** (`patches/vpp/always/`):
- Disables factory default hook (prevents config clobbering)
- Generates port configuration files and PORT entries in CONFIG_DB

No user action is required — setting the correct `dataplane` value in
`platforms.json` is sufficient.

### Troubleshooting Boot Patches

If boot patches fail, deploy reports the error during the `[patch]` phase.

```bash
# Try console access (patches run after SSH is available)
newtlab console <node>

# Check if the VM booted successfully
newtlab status

# Verify CONFIG_DB has expected entries (CiscoVS)
ssh -p <ssh-port> admin@127.0.0.1
redis-cli -n 4 keys "PORT|Ethernet*"

# Check if factory hook was disabled (VPP)
ssh -p <ssh-port> admin@127.0.0.1
ls /etc/config-setup/factory-default-hooks.d/
```

---

## Troubleshooting

### VM Won't Start

```bash
# Check QEMU log
cat ~/.newtlab/labs/<topology>/logs/<node>.log
```

Common causes:
- **Image not found** — verify `vm_image` path in `platforms.json`. Tilde is
  expanded.
- **KVM not available** — check `ls /dev/kvm`. Without KVM, QEMU uses TCG
  (much slower). The VM may time out during boot.
- **Port already in use** — newtlab probes all ports before deploy. If ports
  are in use, deploy fails with a clear error. Stop the conflicting process
  or change port bases.

### Can't SSH

```bash
# Check if the VM is running
newtlab status

# Test the port directly
nc -zv 127.0.0.1 <ssh-port>

# Use console as fallback
newtlab console <node>
```

Common causes:
- **VM still booting** — check status phase. CiscoVS can take 5+ minutes.
- **SSH service not started** — use console to check `systemctl status ssh`.
- **Wrong credentials** — the lab SSH key should handle auth. If key injection
  failed, try the platform password manually.

### Links Not Working

```bash
# Check bridge worker status
newtlab status <topology>
# Look at the link table — connected/waiting/—

# Check inside the VM
newtlab ssh <node>
show interfaces status    # SONiC command
ip link show              # Linux command
```

Common causes:
- **Bridge not running** — bridge workers are started during deploy. If the
  deploy didn't complete, bridges may not be running. Check bridge ports
  directly:
  ```bash
  ss -tlnp | grep <link_port_base>
  ```
- **Wrong interface map** — if `show interfaces status` shows different
  interface names than expected, the `vm_interface_map` in `platforms.json`
  doesn't match the image. CiscoVS and VPP use `sequential`; legacy VS uses
  `stride-4`.
- **Port conflict** — another process on the newtlink port range.
- **Remote bridge not started** — in multi-host mode, check the remote
  newtlink binary: `ssh server-b "~/.newtlab/bin/newtlink --version"`.

### Wrong Interface Names

If `show interfaces status` shows different Ethernet numbering than expected:

- CiscoVS uses `sequential` → Ethernet0, Ethernet1, Ethernet2, ...
- VPP uses `sequential` → Ethernet0, Ethernet1, Ethernet2, ...
- Legacy VS uses `stride-4` → Ethernet0, Ethernet4, Ethernet8, ...

Set the correct `vm_interface_map` in `platforms.json` for your image.

### Boot Patches Failed

Check the deploy output for `[patch]` phase errors. Patches run via SSH,
so the VM must be SSH-reachable first. If SSH works but patches fail:

- **CiscoVS**: frrcfgd patch requires the `bgp` container to be running.
  Check `docker ps` on the switch.
- **VPP**: port config patch requires `syncd` and `swss` containers. These
  may take time to start.

---

## Cross-References

| Document | Path | Contents |
|----------|------|----------|
| newtlab HLD | `docs/newtlab/hld.md` | Architecture: VM lifecycle, bridge networking, state model |
| newtlab LLD | `docs/newtlab/lld.md` | Type definitions, deploy phases, placement algorithm |
| newtron HOWTO | `docs/newtron/howto.md` | Device operations: services, BGP, EVPN, VRFs |
| newtrun HOWTO | `docs/newtrun/howto.md` | Writing and running E2E test scenarios |
