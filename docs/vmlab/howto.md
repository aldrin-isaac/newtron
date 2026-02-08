# vmlab — HOWTO Guide

vmlab realizes network topologies as connected QEMU virtual machines. It
reads newtron's spec files (`topology.json`, `platforms.json`, `profiles/`)
and manages VM lifecycle without root privileges.

For the architectural principles behind newtron, vmlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

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

Download or build SONiC VM images and place them in `~/.vmlab/images/`:

```bash
mkdir -p ~/.vmlab/images

# SONiC VS (control plane only, no dataplane)
cp sonic-vs.qcow2 ~/.vmlab/images/

# SONiC VPP (full dataplane with VPP forwarding)
cp sonic-vpp.qcow2 ~/.vmlab/images/
```

The image path is configured per-platform in `platforms.json`.

---

## Quick Start

```bash
# 1. Deploy VMs from a spec directory
vmlab deploy -S specs/

# 2. Provision devices via newtron
vmlab provision -S specs/

# 3. Access devices
vmlab ssh leaf1
vmlab console spine1

# 4. Tear down
vmlab destroy
```

Or combine deploy and provision:

```bash
vmlab deploy -S specs/ --provision
```

---

## Step-by-Step Workflow

### Deploy VMs

```bash
vmlab deploy -S specs/
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
vmlab provision -S specs/
```

This calls newtron for each device:
```
Provisioning...
  ✓ spine1: newtron provision -S specs/ -d spine1 -x
  ✓ leaf1: newtron provision -S specs/ -d leaf1 -x
```

### Combined Deploy + Provision

```bash
vmlab deploy -S specs/ --provision
```

---

## Accessing VMs

### SSH

```bash
vmlab ssh leaf1
# Or directly:
ssh -p 40001 admin@localhost
```

### Console

```bash
vmlab console spine1
# Or directly:
nc localhost 30000
```

---

## Status

```bash
vmlab status

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
vmlab stop leaf1      # Stop (preserves disk)
vmlab start leaf1     # Start
vmlab reset leaf1     # Hard reset via QEMU monitor
```

---

## Snapshots

```bash
vmlab snapshot --name baseline    # Create
vmlab snapshot --list             # List
vmlab restore --name baseline     # Restore
```

---

## Spec Configuration

### topology.json vmlab Section

The `vmlab` section in `topology.json` controls port allocation:

```json
{
  "devices": { "..." : "..." },
  "links": [ "..." ],
  "vmlab": {
    "link_port_base": 20000,
    "console_port_base": 30000,
    "ssh_port_base": 40000
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Base port for socket links |
| `console_port_base` | 30000 | Base port for serial consoles |
| `ssh_port_base` | 40000 | Base port for SSH forwarding |
| `hosts` | (none) | Host name → IP mapping for multi-host |

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
      "vm_image": "~/.vmlab/images/sonic-vs.qcow2",
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
      "vm_image": "~/.vmlab/images/sonic-vpp.qcow2",
      "vm_memory": 4096,
      "vm_cpus": 4,
      "vm_nic_driver": "virtio-net-pci",
      "vm_interface_map": "sequential",
      "vm_cpu_features": "+sse4.2",
      "vm_credentials": { "user": "admin", "pass": "YourPaSsWoRd" },
      "vm_boot_timeout": 180
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

After deploying VMs, vmlab updates profiles so newtron can connect:

```json
// Before vmlab deploy
{
  "mgmt_ip": "PLACEHOLDER",
  "platform": "sonic-vpp"
}

// After vmlab deploy
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

## Multi-Host

> **Phase 3 — Not yet implemented.** The configuration format below is defined
> for forward compatibility. Single-host deployment is the current mode.

### Configure in topology.json

```json
{
  "vmlab": {
    "hosts": {
      "server-a": "192.168.1.10",
      "server-b": "192.168.1.11"
    }
  }
}
```

### Assign VMs to Hosts

In `profiles/<device>.json`:

```json
{
  "vm_host": "server-a"
}
```

### Deploy Per Host

```bash
# On server-a
vmlab deploy -S specs/ --host server-a

# On server-b
vmlab deploy -S specs/ --host server-b

# Provision from anywhere
vmlab provision -S specs/
```

Cross-host links use TCP sockets. The listening side binds `0.0.0.0:<port>`,
the connecting side uses the host IP from the `hosts` map.

---

## Troubleshooting

### VM Won't Start

```bash
cat ~/.vmlab/labs/<topology>/logs/spine1.log
```

Common causes:
- Image not found (check `vm_image` path in platforms.json)
- KVM not available (falls back to TCG, much slower)
- Port already in use (`ss -tlnp | grep 40000`)

### Can't SSH

```bash
vmlab status                    # Check if running
nc -zv localhost 40000          # Check port
vmlab console spine1            # Try console
```

### Links Not Working

```bash
ss -tlnp | grep 20000           # Check listener
vmlab ssh spine1
ip link show                    # Check interfaces inside VM
```

Check that the platform's `vm_interface_map` matches the SONiC image type.
Wrong mapping means interfaces exist but are numbered differently than
expected.

### Wrong Interface Names

If `show interfaces status` in SONiC shows different names than expected,
the `vm_interface_map` may be wrong for the image:
- `sonic-vs` uses `stride-4` (Ethernet0, Ethernet4, Ethernet8, ...)
- `sonic-vpp` uses `sequential` (Ethernet0, Ethernet1, Ethernet2, ...)

---

## Command Reference

```
vmlab - VM orchestration for network topologies

Commands:
  vmlab deploy -S <specs>        Deploy VMs from topology.json (--force to redeploy)
  vmlab provision -S <specs>     Provision devices via newtron
  vmlab destroy                  Stop and remove all VMs
  vmlab status                   Show VM status
  vmlab ssh <node>               SSH to a VM
  vmlab console <node>           Attach to serial console
  vmlab stop <node>              Stop a VM (preserves disk)
  vmlab start <node>             Start a stopped VM
  vmlab snapshot --name <name>   Create snapshot
  vmlab restore --name <name>    Restore from snapshot

Options:
  -S, --specs <dir>     Spec directory (required for deploy/provision)
  --provision           Provision after deploy
  --host <name>         Multi-host: only deploy nodes for this host
  --parallel <n>        Parallel provisioning
  --force               Force destroy even if inconsistent
  -v, --verbose         Verbose output
```
