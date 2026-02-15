# newtlab — HOWTO Guide

newtlab realizes network topologies as connected QEMU virtual machines. It
reads newtron's spec files (`topology.json`, `platforms.json`, `profiles/`)
and manages VM lifecycle without root privileges.

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

---

## Prerequisites

### Required Software

```bash
# QEMU with KVM support
sudo apt install qemu-system-x86 qemu-utils

# Verify KVM is available (recommended for performance)
ls /dev/kvm

# netcat for console access
sudo apt install netcat-openbsd
```

### VM Images

Download or build SONiC VM images and place them in `~/.newtlab/images/`:

```bash
mkdir -p ~/.newtlab/images

# SONiC VS (control plane only, no dataplane)
cp sonic-vs.qcow2 ~/.newtlab/images/

# SONiC VPP (full dataplane with VPP forwarding)
cp sonic-vpp.qcow2 ~/.newtlab/images/
```

The image path is configured per-platform in `platforms.json`.

---

## Quick Start

```bash
# 1. Deploy VMs from a spec directory
newtlab deploy -S specs/

# 2. Provision devices via newtron
newtlab provision -S specs/

# 3. Access devices
newtlab ssh leaf1
newtlab console spine1

# 4. Tear down
newtlab destroy
```

Or combine deploy and provision:

```bash
newtlab deploy -S specs/ --provision
```

---

## Step-by-Step Workflow

### Deploy VMs

```bash
newtlab deploy -S specs/
```

Output:
```
Deploying VMs...
  Creating overlay disks...
    ✓ spine1.qcow2
    ✓ leaf1.qcow2
  Starting VMs...
    ✓ spine1 (PID 12345, SSH :40000, console :30000)
    ✓ leaf1 (PID 12346, SSH :40001, console :30001)
  Waiting for SSH...
    ✓ spine1 ready
    ✓ leaf1 ready
  Patching profiles...

✓ Deployed spine-leaf (2 nodes)

  Node      SSH                          Console
  ────────  ───────────────────────────  ─────────────────────
  spine1    ssh -p 40000 admin@localhost nc localhost 30000
  leaf1     ssh -p 40001 admin@localhost nc localhost 30001
```

### Provision Devices

```bash
newtlab provision -S specs/
```

This calls newtron for each device:
```
Provisioning...
  ✓ spine1: newtron provision -S specs/ -d spine1 -x
  ✓ leaf1: newtron provision -S specs/ -d leaf1 -x
```

### Combined Deploy + Provision

```bash
newtlab deploy -S specs/ --provision
```

---

## Accessing VMs

### SSH

```bash
newtlab ssh leaf1
# Or directly:
ssh -p 40001 admin@localhost
```

### Console

```bash
newtlab console spine1
# Or directly:
nc localhost 30000
```

---

## Status

```bash
newtlab status

Lab: spine-leaf
State: running
Spec dir: /home/user/specs

  Node      Status    PID     SSH Port  Console Port  Platform
  ────────  ────────  ──────  ────────  ────────────  ─────────
  spine1    running   12345   40000     30000         sonic-vpp
  leaf1     running   12346   40001     30001         sonic-vpp

Links (1):
  spine1:Ethernet0 <-> leaf1:Ethernet0  (port 20000, active)
```

---

## Managing Nodes

```bash
newtlab stop leaf1      # Stop (preserves disk)
newtlab start leaf1     # Start
```

---

## Spec Configuration

### topology.json newtlab Section

The `newtlab` section in `topology.json` controls port allocation and
multi-host settings:

```json
{
  "devices": { "..." : "..." },
  "links": [ "..." ],
  "newtlab": {
    "link_port_base": 20000,
    "console_port_base": 30000,
    "ssh_port_base": 40000,
    "servers": [
      { "name": "server-a", "address": "192.168.1.10", "max_nodes": 4 },
      { "name": "server-b", "address": "192.168.1.11", "max_nodes": 4 }
    ]
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Base port for socket links |
| `console_port_base` | 30000 | Base port for serial consoles |
| `ssh_port_base` | 40000 | Base port for SSH forwarding |
| `servers` | (none) | Server pool for auto-placement (see Multi-Host section) |
| `hosts` | (none) | Legacy host name → IP mapping (use `servers` instead) |

### platforms.json — Multi-Platform Support

Define multiple platforms for different SONiC images:

```json
{
  "platforms": {
    "sonic-vs": {
      "hwsku": "Force10-S6000",
      "description": "SONiC Virtual Switch (control plane only)",
      "port_count": 32,
      "default_speed": "40000",
      "vm_image": "~/.newtlab/images/sonic-vs.qcow2",
      "vm_memory": 4096,
      "vm_cpus": 2,
      "vm_nic_driver": "e1000",
      "vm_interface_map": "stride-4",
      "vm_credentials": { "user": "admin", "pass": "YourPaSsWoRd" },
      "vm_boot_timeout": 180
    },
    "sonic-vpp": {
      "hwsku": "Force10-S6000",
      "description": "SONiC with VPP dataplane (full forwarding)",
      "port_count": 32,
      "default_speed": "40000",
      "vm_image": "~/.newtlab/images/sonic-vpp.qcow2",
      "vm_memory": 4096,
      "vm_cpus": 4,
      "vm_nic_driver": "virtio-net-pci",
      "vm_interface_map": "sequential",
      "vm_cpu_features": "+sse4.2",
      "vm_credentials": { "user": "admin", "pass": "YourPaSsWoRd" },
      "vm_boot_timeout": 180,
      "dataplane": "vpp",
      "vm_image_release": "202405"
    }
  }
}
```

#### VM-Specific Platform Fields

| Field | Description |
|-------|-------------|
| `vm_image` | Path to QCOW2 disk image |
| `vm_memory` | Memory in MB (default: 4096) |
| `vm_cpus` | Number of vCPUs (default: 2) |
| `vm_nic_driver` | QEMU NIC driver: `e1000`, `virtio-net-pci` |
| `vm_interface_map` | How NIC index maps to SONiC interface names |
| `vm_cpu_features` | QEMU CPU feature flags (e.g., `+sse4.2` for VPP) |
| `vm_credentials` | Default login credentials |
| `vm_boot_timeout` | Seconds to wait for SSH readiness |
| `dataplane` | Dataplane type: `"vpp"`, `"barefoot"`, `""` (none/vs) |
| `vm_image_release` | Image release (e.g., `"202405"`) — selects release-specific boot patches |

#### Interface Maps

Different SONiC images map QEMU NIC index to interface names differently:

| Map Type | NIC 1 | NIC 2 | NIC 3 | NIC 4 | Used By |
|----------|-------|-------|-------|-------|---------|
| `sequential` | Ethernet0 | Ethernet1 | Ethernet2 | Ethernet3 | VPP |
| `stride-4` | Ethernet0 | Ethernet4 | Ethernet8 | Ethernet12 | VS, Cisco |
| `custom` | (explicit mapping) | | | | Vendor-specific |

NIC 0 is always reserved for management.

### Profile VM Overrides

Override per device in `profiles/<device>.json`:

```json
{
  "platform": "sonic-vpp",
  "vm_memory": 8192,
  "vm_cpus": 4,
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd"
}
```

### Resolution Order

VM configuration resolves (first non-empty wins):

1. **Profile override** (`profiles/<device>.json`)
2. **Platform default** (`platforms.json`)
3. **Built-in default** (4096 MB, 2 CPUs, `e1000`, `stride-4`)

Credentials resolve:
1. Profile `ssh_user`/`ssh_pass`
2. Platform `vm_credentials`

---

## Profile Patching

After deploying VMs, newtlab updates profiles so newtron can connect:

```json
// Before newtlab deploy
{
  "mgmt_ip": "PLACEHOLDER",
  "platform": "sonic-vpp"
}

// After newtlab deploy
{
  "mgmt_ip": "127.0.0.1",
  "ssh_port": 40000,
  "console_port": 30000,
  "platform": "sonic-vpp"
}
```

newtron reads `ssh_port` from the profile to connect on the correct port
instead of the default 22.

---

## Platform Boot Patches

Some SONiC images have platform-specific initialization issues that newtlab
automatically patches after boot. Patches are selected by the platform's
`dataplane` and optionally `vm_image_release` fields.

### How It Works

During deploy, after VMs boot and SSH is available, newtlab:

1. Looks up patches for the platform's dataplane type (e.g., `vpp`)
2. Applies `always/` patches (present for every image of this type)
3. Applies release-specific patches if `vm_image_release` matches a subdirectory

### Example: VPP patches

The VPP dataplane has known issues with QEMU-based deployment:
- The factory default hook runs twice, clobbering port configuration
- `port_config.ini` and `sonic_vpp_ifmap.ini` are generated empty

newtlab ships with built-in patches that fix these automatically. No user
action is required — setting `"dataplane": "vpp"` in `platforms.json` is
sufficient.

### When Release-Specific Patches Are Needed

If a specific SONiC image build has a unique bug that's fixed in later releases,
add `vm_image_release` to the platform definition:

```json
{
    "sonic-vpp": {
        "dataplane": "vpp",
        "vm_image_release": "202405",
        "vm_image": "~/.newtlab/images/sonic-vpp-202405.qcow2"
    }
}
```

newtlab will apply the always patches plus any patches in `patches/vpp/202405/`.

### Troubleshooting

If boot patches fail:
```bash
# Check deploy output for "boot patch" errors
# Patches run via SSH, so SSH must be working first
newtlab ssh <node>

# Verify CONFIG_DB has PORT entries (VPP)
redis-cli -n 4 keys "PORT|Ethernet*"

# Check if factory hook was disabled (VPP)
ls /etc/config-setup/factory-default-hooks.d/
```

---

## Multi-Host

### Server Pool (Recommended)

Define servers in `topology.json` with capacity constraints. newtlab
auto-places nodes across servers:

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
| `address` | Yes | IP address |
| `max_nodes` | No | Maximum VMs on this server (0 = unlimited) |

**Auto-placement:** Nodes are spread across servers to minimize maximum load.
No `vm_host` configuration needed — newtlab distributes automatically.

**Pinning (optional):** To force a specific node onto a specific server,
set `vm_host` in `profiles/<device>.json`:

```json
{
  "vm_host": "server-a"
}
```

Pinned nodes are validated against the server list and count toward capacity.
Unpinned nodes are distributed across remaining capacity.

### Legacy hosts Map

The older `hosts` map is still supported for backward compatibility:

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

With `hosts`, you must manually set `vm_host` in every device profile.
Prefer `servers` for new topologies.

### Deploy Per Host

```bash
# On server-a
newtlab deploy -S specs/ --host server-a

# On server-b
newtlab deploy -S specs/ --host server-b

# Provision from anywhere
newtlab provision -S specs/
```

Cross-host links are handled by newtlink bridge processes. Each host runs
its own bridge process for the links assigned to it by the load-balancing
placement algorithm. For a link between `spine1` (server-a) and `leaf1`
(server-b), the bridge worker runs on whichever host has fewer assigned
workers (ties broken alphabetically). It listens on `127.0.0.1` for the
local VM and `0.0.0.0` for the remote VM, then bridges frames between them.
The remote VM connects to the bridge host's IP.

Each bridge process also exposes a TCP stats endpoint for telemetry queries.
Use `newtlab status <topology>` to see aggregated counters from all hosts.

---

## Multi-User Environments

When multiple developers deploy to the same server pool, follow these
conventions to avoid conflicts:

### Port Bases

Each user should configure distinct port bases in their `topology.json`.
Since newtlab allocates ports sequentially from the base, spacing bases
by 1000 provides ample room:

| User | link_port_base | console_port_base | ssh_port_base |
|------|----------------|-------------------|---------------|
| Alice | 20000 | 30000 | 40000 |
| Bob | 21000 | 31000 | 41000 |
| Carol | 22000 | 32000 | 42000 |

### Lab Naming

Lab names should be unique across all users of a server pool. Prefix
with your username to avoid collisions:

```json
{
  "name": "alice-spine-leaf",
  "devices": { "..." : "..." }
}
```

Each lab writes its state to `~/.newtlab/labs/<name>/` on the host, so
unique names ensure no overlap.

### Port Conflict Detection

`ProbeAllPorts` runs automatically during deploy and checks all allocated
ports (SSH, console, link, bridge stats) on every host. If another user's
lab occupies a port, deploy fails with a clear error message identifying
the conflicting port and host.

### Shared newtlink Binary

The uploaded `newtlink` binary at `~/.newtlab/bin/newtlink` on remote
servers is shared across all users of the same Unix account. Before
uploading, newtlab checks the remote version — if it matches the local
build, the upload is skipped. This means the last user to deploy gets
their version installed, but since all newtlink versions are backward-
compatible with the bridge config format, this is safe.

To use a specific newtlink version, set `$NEWTLAB_BIN_DIR` to a directory
containing your cross-compiled binaries.

---

## Troubleshooting

### VM Won't Start

```bash
cat ~/.newtlab/labs/<topology>/logs/spine1.log
```

Common causes:
- Image not found (check `vm_image` path in platforms.json)
- KVM not available (falls back to TCG, much slower)
- Port already in use — newtlab probes all allocated ports (SSH, console,
  link, bridge stats) before deploy. If you see "port conflicts", stop the
  conflicting process or change port bases in `topology.json`.

### Can't SSH

```bash
newtlab status                    # Check if running
nc -zv localhost 40000          # Check port
newtlab console spine1            # Try console
```

### Links Not Working

```bash
# Check newtlink listeners (two ports per link)
ss -tlnp | grep 20000
ss -tlnp | grep 20001

# Check inside VM
newtlab ssh spine1
ip link show                    # Check interfaces
```

Common causes:
- newtlink not running — check that deploy completed successfully. In
  multi-host mode, each host runs its own bridge process; check
  `newtlab status --bridge-stats` to see which bridges are reachable.
- Port conflict — another process on the newtlink port range
- Wrong `vm_interface_map` for the SONiC image type (interfaces exist but
  numbered differently than expected)
- Remote bridge not started — newtlab auto-uploads the `newtlink` binary;
  check `~/.newtlab/bin/newtlink --version` on the remote host

### Bridge Stats

Check bridge link counters (aggregated across all hosts):

```bash
newtlab status <topology>

LINK                                     A→Z          Z→A          SESSIONS  CONNECTED
────────────────────────────────────────  ────────────  ────────────  ─────────  ─────────
spine1:Ethernet0 ↔ leaf1:Ethernet0      1.2 MB       856.0 KB     3         yes
spine1:Ethernet4 ↔ leaf2:Ethernet0      2.5 MB       1.1 MB       2         yes
```

If a bridge on a remote host is unreachable, `status` returns an error
for that host. Check that the remote bridge process is running:

```bash
ssh server-b "ps aux | grep newtlink"
```

### Wrong Interface Names

If `show interfaces status` in SONiC shows different names than expected,
the `vm_interface_map` may be wrong for the image:
- `sonic-vs` uses `stride-4` (Ethernet0, Ethernet4, Ethernet8, ...)
- `sonic-vpp` uses `sequential` (Ethernet0, Ethernet1, Ethernet2, ...)

---

## Command Reference

```
newtlab - VM orchestration for network topologies

Commands:
  newtlab deploy -S <specs>        Deploy VMs from topology.json (--force to redeploy)
  newtlab provision -S <specs>     Provision devices via newtron
  newtlab destroy                  Stop and remove all VMs
  newtlab status                   Show VM status
  newtlab list                     List all deployed labs
  newtlab ssh <node>               SSH to a VM
  newtlab console <node>           Attach to serial console
  newtlab stop <node>              Stop a VM (preserves disk)
  newtlab start <node>             Start a stopped VM

Options:
  -S, --specs <dir>     Spec directory (required for deploy/provision)
  --provision           Provision after deploy
  --host <name>         Multi-host: only deploy nodes for this host
  --parallel <n>        Parallel provisioning
  --force               Force destroy even if inconsistent
  -v, --verbose         Verbose output
```
