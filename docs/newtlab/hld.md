# newtlab — High-Level Design

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

## 1. Purpose

newtlab realizes network topologies as connected QEMU virtual machines. It
reads newtron's spec files (`topology.json`, `platforms.json`,
`profiles/*.json`) and brings the topology to life — deploying VMs
(primarily SONiC) and wiring them together using socket-based links
across one or more servers. No root, no bridges, no Docker.

newtlab doesn't define the topology or touch device configuration — it
makes the topology physically exist. After deployment, it patches device
profiles with SSH and console ports so newtron can connect.

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Shared Spec Directory                         │
│  specs/                                                              │
│  ├── topology.json    ← newtlab reads: devices, links, newtlab settings │
│  ├── platforms.json   ← newtlab reads: VM defaults (image, memory)    │
│  ├── profiles/*.json  ← newtlab reads/writes: VM overrides, ports     │
│  ├── network.json     ← newtron reads: services, VPNs, filters      │
│  └── site.json        ← newtron reads: site topology                │
└─────────────────────────────────────────────────────────────────────┘
         │                                    │
         ▼                                    ▼
┌─────────────────────┐            ┌─────────────────────┐
│       newtlab         │            │      newtron        │
│                     │            │                     │
│ • Deploy QEMU VMs   │            │ • Provision devices │
│ • Socket networking │            │ • Write CONFIG_DB   │
│ • Patch profiles    │            │ • BGP, EVPN, ACLs   │
└─────────────────────┘            └─────────────────────┘
         ▲                                    ▲
         │            ┌──────────┐            │
         └────────────│ newtest  │────────────┘
                      │ (E2E)   │
                      └──────────┘
```

Benefits:
- No root/sudo privileges required
- No Linux bridges, TAP interfaces, or veth pairs
- No Docker or container runtime needed
- Single source of truth for topology (topology.json)
- Native multi-host support via TCP sockets
- Supports multiple SONiC image types (VS, VPP, vendor)

---

## 2. Workflow

### 2.1 Deploy

```bash
newtlab deploy -S specs/
```

### 2.2 Interact

```bash
newtlab status
newtlab ssh leaf1
newtlab console spine1
```

### 2.3 Tear Down

```bash
newtlab destroy
```

---

## 3. Spec Files (Shared with newtron)

newtlab reads from the same spec directory as newtron. No new files required.

### 3.1 topology.json

newtlab reads `devices` and `links` (same as newtron), plus an optional `newtlab`
section for orchestration settings:

```json
{
  "version": "1.0",
  "devices": {
    "spine1": {
      "interfaces": {
        "Ethernet0": { "link": "leaf1:Ethernet0", "service": "fabric-underlay", "ip": "10.1.0.0/31" }
      }
    },
    "leaf1": {
      "interfaces": {
        "Ethernet0": { "link": "spine1:Ethernet0", "service": "fabric-underlay", "ip": "10.1.0.1/31" }
      }
    }
  },
  "links": [
    { "a": "spine1:Ethernet0", "z": "leaf1:Ethernet0" }
  ],
  "newtlab": {
    "link_port_base": 20000,
    "console_port_base": 30000,
    "ssh_port_base": 40000,
    "hosts": {
      "server-a": "192.168.1.10",
      "server-b": "192.168.1.11"
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Base port for socket links |
| `console_port_base` | 30000 | Base port for serial consoles |
| `ssh_port_base` | 40000 | Base port for SSH forwarding |
| `hosts` | (none) | Host name → IP mapping for multi-host |

### 3.2 platforms.json

newtlab reads VM defaults from platform definitions. Multiple platforms can be
defined to support different SONiC images:

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
      "vm_boot_timeout": 180
    },
    "sonic-cisco-8000": {
      "hwsku": "cisco-8101-p4-32x100-vs",
      "description": "Cisco 8000 SONiC NGDP image",
      "port_count": 32,
      "default_speed": "100000",
      "vm_image": "~/.newtlab/images/sonic-cisco.qcow2",
      "vm_memory": 8192,
      "vm_cpus": 4,
      "vm_nic_driver": "e1000",
      "vm_interface_map": "stride-4",
      "vm_credentials": { "user": "cisco", "pass": "cisco123" },
      "vm_boot_timeout": 300
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

Different SONiC images map QEMU NIC index to SONiC interface names differently:

| Map Type | NIC 1 | NIC 2 | NIC 3 | NIC 4 | Notes |
|----------|-------|-------|-------|-------|-------|
| `sequential` | Ethernet0 | Ethernet1 | Ethernet2 | Ethernet3 | VPP |
| `stride-4` | Ethernet0 | Ethernet4 | Ethernet8 | Ethernet12 | VS, Cisco |
| `custom` | (explicit) | | | | Vendor-specific |

NIC 0 is always management.

### 3.3 profiles/*.json

newtlab reads per-device overrides and writes runtime ports after deployment:

```json
{
  "mgmt_ip": "127.0.0.1",
  "loopback_ip": "10.0.0.1",
  "site": "lab-site",
  "platform": "sonic-vpp",
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd",
  "vm_memory": 8192,
  "vm_host": "server-a",
  "ssh_port": 40000,
  "console_port": 30000
}
```

| Field | Read/Write | Description |
|-------|------------|-------------|
| `platform` | Read | Platform name (for VM defaults) |
| `ssh_user`, `ssh_pass` | Read | SSH credentials (overrides platform) |
| `vm_image` | Read | Override platform's vm_image |
| `vm_memory` | Read | Override platform's vm_memory |
| `vm_cpus` | Read | Override platform's vm_cpus |
| `vm_host` | Read | Host to run this VM (multi-host) |
| `mgmt_ip` | Write | Set to 127.0.0.1 (or host IP) |
| `ssh_port` | Write | Assigned SSH forwarded port |
| `console_port` | Write | Assigned console port |

---

## 4. Resolution Order

VM configuration resolves (first non-empty wins):

1. **Profile override** (`profiles/<device>.json`)
2. **Platform default** (`platforms.json`)
3. **Built-in default** (4096 MB, 2 CPUs, `e1000`, `stride-4`, error if no image)

Credentials resolve:
1. Profile `ssh_user`/`ssh_pass`
2. Platform `vm_credentials`

---

## 5. Socket-Based Networking

### 5.1 How It Works

Each link in `topology.json` becomes a TCP socket pair:

```
Link: { "a": "spine1:Ethernet0", "z": "leaf1:Ethernet0" }

spine1 QEMU:
  -netdev socket,id=eth0,listen=:20000
  -device <nic_driver>,netdev=eth0

leaf1 QEMU:
  -netdev socket,id=eth0,connect=127.0.0.1:20000
  -device <nic_driver>,netdev=eth0
```

newtlab uses the platform's `vm_interface_map` to determine which QEMU NIC index
corresponds to each SONiC interface name.

### 5.2 Port Allocation

```
Link ports:      link_port_base + link_index      (20000, 20001, ...)
Console ports:   console_port_base + node_index   (30000, 30001, ...)
SSH ports:       ssh_port_base + node_index       (40000, 40001, ...)
```

### 5.3 Cross-Host Links

For multi-host, newtlab uses the `hosts` map to resolve IPs:

```json
{
  "newtlab": {
    "hosts": { "server-a": "192.168.1.10", "server-b": "192.168.1.11" }
  }
}
```

Profile assigns VM to host:
```json
{ "vm_host": "server-a" }
```

spine1 (server-a) listens on `0.0.0.0:20000`, leaf1 (server-b) connects to `192.168.1.10:20000`.

> **Note:** Multi-host is Phase 3 (not yet implemented). The fields are defined for forward compatibility.

---

## 6. Profile Patching

After deploying VMs, newtlab updates profiles so newtron can connect:

```json
// Before newtlab deploy
{
  "mgmt_ip": "PLACEHOLDER",
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd"
}

// After newtlab deploy
{
  "mgmt_ip": "127.0.0.1",
  "ssh_port": 40000,
  "console_port": 30000,
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd"
}
```

On destroy, newtlab restores the original `mgmt_ip` from state.json (`original_mgmt_ip`), removing `ssh_port` and `console_port`.

---

## 7. newtron Integration

### 7.1 SSH Port Support

newtron needs one small change: support custom SSH port from profiles.

**pkg/spec/types.go**:
```go
type DeviceProfile struct {
    // ... existing fields ...
    SSHPort int `json:"ssh_port,omitempty"`
}
```

**pkg/device/tunnel.go**:
```go
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
    if port == 0 {
        port = 22
    }
    addr := fmt.Sprintf("%s:%d", host, port)
    // ...
}
```

Backward compatible — profiles without `ssh_port` use port 22.

---

## 8. State Management

### 8.1 Lab State Directory

```
~/.newtlab/labs/<topology-name>/
├── state.json           # Running state
├── qemu/
│   ├── spine1.pid
│   └── spine1.mon       # QEMU monitor socket
├── disks/
│   └── spine1.qcow2     # COW overlay
└── logs/
    └── spine1.log
```

### 8.2 state.json

```json
{
  "name": "spine-leaf",
  "created": "2026-02-05T10:30:00Z",
  "spec_dir": "/home/user/specs",
  "nodes": {
    "spine1": {
      "pid": 12345,
      "status": "running",
      "ssh_port": 40000,
      "console_port": 30000,
      "original_mgmt_ip": "PLACEHOLDER"
    }
  },
  "links": [
    { "a": "spine1:Ethernet0", "z": "leaf1:Ethernet0", "port": 20000 }
  ]
}
```

---

## 9. CLI

```
newtlab - VM orchestration for network topologies

Commands:
  newtlab deploy -S <specs>        Deploy VMs from topology.json (--force to redeploy)
  newtlab destroy                  Stop and remove all VMs
  newtlab status                   Show VM status
  newtlab ssh <node>               SSH to a VM
  newtlab console <node>           Attach to serial console
  newtlab stop <node>              Stop a VM (preserves disk)
  newtlab start <node>             Start a stopped VM
  newtlab provision -S <specs>     Provision devices via newtron
  newtlab snapshot --name <name>   Create snapshot
  newtlab restore --name <name>    Restore from snapshot

Options:
  -S, --specs <dir>     Spec directory (required for deploy/provision)
  --provision           Provision devices after deploy
  --parallel <n>        Parallel provisioning threads
  --host <name>         Multi-host: only deploy nodes for this host
  --force               Force destroy even if inconsistent
  -v, --verbose         Verbose output
```

---

## 10. Comparison with Containerlab

| Aspect | newtlab | containerlab |
|--------|-------|--------------|
| Root required | No | Yes |
| Host networking | None | Bridges, veth, tc |
| Multi-host | Native (TCP) | Complex (tunnels) |
| Topology source | topology.json | clab.yml |
| Image flexibility | Platform-based | Per-node kind |
| Profile patching | Automatic | Manual/setup.sh |

---

## 11. Implementation Phases

### Phase 1: Core
- Read topology.json, platforms.json, profiles/*.json
- Single-host QEMU deployment with platform-based image selection
- Interface map support (sequential, stride-4)
- Socket links from topology.json links
- Profile patching (mgmt_ip, ssh_port, console_port)
- CLI: deploy, destroy, status, ssh, console

### Phase 2: Polish
- Boot timeout and health check
- NIC driver and CPU feature support
- Improved error messages

### Phase 3: Multi-Host (not yet implemented)
- vm_host in profiles
- hosts map in topology.json newtlab section
- Cross-host socket links
- Per-host deployment (`--host`)

### Phase 4: Advanced
- Snapshot/restore
- Image management
