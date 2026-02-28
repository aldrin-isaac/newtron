# newtlab — High-Level Design

For the architectural principles behind newtron, newtlab, and newtrun, see [Design Principles](../DESIGN_PRINCIPLES.md).

## 1. Purpose

newtlab realizes network topologies as connected QEMU virtual machines,
wired together through **userspace socket bridges** — not kernel networking.
It reads newtron's spec files (`topology.json`, `platforms.json`,
`profiles/*.json`) and brings the topology to life — deploying VMs
(primarily SONiC) and wiring them across one or more servers. No root,
no Linux bridges, no veth pairs, no network namespaces, no Docker.

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
│  └── network.json     ← newtron reads: services, VPNs, filters      │
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
         └────────────│ newtrun  │────────────┘
                      │ (E2E)   │
                      └──────────┘
```

This is a deliberate alternative to kernel-level wiring (veth pairs, Linux
bridges, tc rules). A userspace bridge knows exactly how many bytes crossed
each link, because it handles every frame. Rate monitoring, tap-to-wireshark,
fault injection — all are straightforward extensions of the bridge loop.
Kernel networking is powerful but opaque; when a link breaks, you debug
iptables rules and bridge state. When a newtlink bridge has a problem, you
look at one process.

Benefits:
- **Observable** — per-link byte counters, session state, and bridge stats aggregated across hosts
- **Debuggable** — one userspace process per host, not kernel bridge/iptables state
- **Multi-host native** — cross-host links work identically to local links via TCP
- **Unprivileged** — no root, no kernel modules, no Docker
- **No startup ordering** — both VMs connect outbound to newtlink
- **Multi-platform** — VS, VPP, Cisco 8000, vendor images with per-platform boot patches

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
| `servers` | (none) | Server pool for auto-placement (see §5.7) |
| `hosts` | (none) | Legacy host name → IP mapping (use `servers` instead) |

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
      "vm_boot_timeout": 180,
      "dataplane": "vpp",
      "vm_image_release": "202405"
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
| `dataplane` | Dataplane type: `"vpp"`, `"barefoot"`, `""` (none/vs) |
| `vm_image_release` | Image release identifier — selects release-specific boot patches |

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
  "zone": "amer",
  "platform": "sonic-vpp",
  "ssh_user": "admin",
  "ssh_pass": "YourPaSsWoRd",
  "evpn": {
    "peers": ["10.0.0.2", "10.0.0.3"],
    "route_reflector": true
  },
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

## 5. Link Networking — newtlink Bridge Agent

### 5.1 How It Works

Each link in `topology.json` is realized by a **newtlink bridge worker** — a
goroutine that listens on two TCP ports (one per endpoint) and bridges
Ethernet frames between them. Both QEMU VMs connect *outbound* to newtlink;
newtlink never connects to QEMU.

```
Link: { "a": "spine1:Ethernet0", "z": "leaf1:Ethernet0" }

newtlink worker (per link):
  Listen on :20000  (A-side port)
  Listen on :20001  (Z-side port)
  Accept one connection on each
  Bridge frames: A↔Z

spine1 QEMU:
  -netdev socket,id=eth1,connect=127.0.0.1:20000
  -device <nic_driver>,netdev=eth1

leaf1 QEMU:
  -netdev socket,id=eth1,connect=127.0.0.1:20001
  -device <nic_driver>,netdev=eth1
```

newtlab uses the platform's `vm_interface_map` to determine which QEMU NIC index
corresponds to each SONiC interface name.

#### Why newtlink instead of direct QEMU sockets?

QEMU's built-in `-netdev socket` supports a listen/connect model where one VM
listens and the other connects. This has three problems:

1. **Startup ordering** — the listen-side VM must start before the connect-side
   VM, creating a dependency graph that complicates deployment.
2. **Asymmetry** — each side of a link uses different QEMU arguments (listen vs
   connect), so link wiring must track which side is which.
3. **Cross-host divergence** — local links use `127.0.0.1` while cross-host
   links need the remote host's IP, requiring different implementations.

newtlink eliminates all three: both VMs always `connect` to a known local port,
newtlink handles the bridging, and the same model works identically for local
and cross-host links (newtlink just listens on `0.0.0.0` instead of `127.0.0.1`
for cross-host endpoints).

### 5.2 Port Allocation

Each link consumes **two** ports (one per endpoint):

```
Link ports:      link_port_base + (link_index * 2)         A-side
                 link_port_base + (link_index * 2) + 1     Z-side
Console ports:   console_port_base + node_index            (30000, 30001, ...)
SSH ports:       ssh_port_base + node_index                (40000, 40001, ...)
```

Example for two links:
```
Link 0: A-side :20000, Z-side :20001
Link 1: A-side :20002, Z-side :20003
```

### 5.3 Cross-Host Links

For cross-host links, the newtlink worker runs on one of the two hosts (the
A-side host by convention). The A-side port listens on `127.0.0.1` (local VM
connects to it), while the Z-side port listens on `0.0.0.0` (remote VM connects
across the network).

```
Link: spine1 (server-a) ↔ leaf1 (server-b)

newtlink on server-a:
  Listen 127.0.0.1:20000  (spine1 connects locally)
  Listen 0.0.0.0:20001    (leaf1 connects from server-b)

spine1 QEMU (server-a):
  -netdev socket,id=eth1,connect=127.0.0.1:20000

leaf1 QEMU (server-b):
  -netdev socket,id=eth1,connect=192.168.1.10:20001
```

The `servers` list in `topology.json` provides IP addresses (the legacy
`hosts` map is also supported):

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

Profile can optionally pin a VM to a specific host:
```json
{ "vm_host": "server-a" }
```

Unpinned VMs are auto-placed across the server pool (see §5.7).

For same-host links, both ports listen on `127.0.0.1`.

### 5.4 Worker Placement (Multi-Host)

Each newtlink worker must run on one of its link's two endpoint hosts — never
a third host, which would add an unnecessary network hop. A cross-host link
always costs exactly one network hop regardless of which endpoint host runs the
worker (one side is local, the other crosses the network). So placement is
about **load balancing**, not latency.

In multi-host mode, each host runs `newtlab deploy --host X` independently.
All instances read the same `topology.json`, so the placement algorithm must
be **deterministic from the topology alone** — no coordination needed.

**Placement rules:**

1. **Local link** (both VMs on same host): worker runs on that host.
2. **Cross-host link**: assigned to the endpoint host with fewer cross-host
   workers so far (greedy, iterating links in sorted order). Ties broken by
   host name sort order.

Since every host computes the same sorted link order and the same greedy
assignment, each host's newtlab instance knows exactly which cross-host
link workers it owns — and starts only those.

**Example:** 2 spines on host-A, 4 leaves on host-B, full-mesh links:

```
Link 0: spine1↔leaf1  → host-A has 0, host-B has 0 → assign host-A (tie, A < B)
Link 1: spine1↔leaf2  → host-A has 1, host-B has 0 → assign host-B
Link 2: spine1↔leaf3  → host-A has 1, host-B has 1 → assign host-A
Link 3: spine1↔leaf4  → host-A has 2, host-B has 1 → assign host-B
Link 4: spine2↔leaf1  → host-A has 2, host-B has 2 → assign host-A
Link 5: spine2↔leaf2  → host-A has 3, host-B has 2 → assign host-B
Link 6: spine2↔leaf3  → host-A has 3, host-B has 3 → assign host-A
Link 7: spine2↔leaf4  → host-A has 4, host-B has 3 → assign host-B
Result: 4 workers on host-A, 4 on host-B (perfectly balanced)
```

For pairwise balancing with 3+ hosts, the same greedy rule applies
independently to each host pair. Each cross-host link only considers the two
endpoint hosts — the global count doesn't matter since traffic is pairwise.

### 5.5 Multi-Host Bridge Distribution

In single-host mode, all bridge workers run in one process. In multi-host
mode, each host runs its own bridge process for the links assigned to it
by the worker placement algorithm (§5.4).

```
2-host topology:

server-a                         server-b
┌────────────────────┐           ┌────────────────────┐
│ Bridge process     │           │ Bridge process     │
│  • spine1↔leaf1    │           │  • spine1↔leaf2    │
│  • spine2↔leaf1    │           │  • spine2↔leaf2    │
│                    │           │                    │
│ Stats TCP :19999   │           │ Stats TCP :19998   │
│ Stats Unix sock    │           │                    │
└────────────────────┘           └────────────────────┘
```

**Bridge process lifecycle:**
- **Local host:** spawned as a child process via `newtlab bridge <lab>`
- **Remote host:** started via SSH: `nohup newtlab bridge <lab> &`
- **Bridge config:** each host's bridge process reads `bridge.json` from
  `~/.newtlab/labs/<name>/bridge.json`, containing only its assigned links

**Stats port allocation:** Each bridge process exposes a TCP stats endpoint
for remote queries, allocated from the link port space counting downward:

```
Stats ports:     link_port_base - 1 - host_index
                 (where host_index = 0 for local, 1 for first remote, etc.)

Example with link_port_base=20000 and 2 hosts:
  Local bridge:   127.0.0.1:19999   (also has Unix socket for local queries)
  Remote bridge:  0.0.0.0:19998     (TCP only)
```

The local bridge also listens on a Unix socket (`bridge.sock`) for backward
compatibility with single-host deployments.

**Stats aggregation:** `newtlab status <topology>` queries all bridge processes
and merges their counters into a single table. Local bridges are queried
via Unix socket; remote bridges via TCP.

### 5.6 Platform Boot Patches

Different SONiC images have platform-specific initialization quirks that must
be patched after boot but before provisioning. Rather than hardcoding per-platform
Go code, newtlab uses a **declarative boot patch framework**: JSON descriptors
paired with Go templates.

**Patch directory structure:**

```
patches/
└── vpp/
    ├── always/                      # Applied to every VPP image
    │   ├── 01-disable-factory-hook.json
    │   └── 02-port-config.json
    └── 202405/                      # Applied only when vm_image_release = "202405"
        └── 01-specific-fix.json
```

**Resolution order:**
1. `patches/<dataplane>/always/*.json` — always applied for this dataplane
2. `patches/<dataplane>/<release>/*.json` — applied only when `vm_image_release` matches

Within each directory, patches are applied in filename sort order (hence the numeric prefix convention).

**Template variables** are computed from what newtlab already knows — QEMU NIC
count, deterministic PCI addresses, platform HWSKU, port speed. No SSH-based
discovery inside the VM is needed.

**Each patch descriptor** specifies:
- `pre_commands` — shell commands to run before applying files (e.g., wait for a process to exit)
- `disable_files` — paths to rename to `.disabled` (e.g., broken init hooks)
- `files` — Go templates rendered with patch variables and uploaded to the VM
- `redis` — Go templates rendered into redis-cli commands for CONFIG_DB writes
- `post_commands` — shell commands to run after (e.g., restart services)

**Adding support for a new platform** requires no Go code changes — create a
directory under `patches/` with descriptors and templates. When an upstream
image fixes a bug, delete the release-specific patch directory.

### 5.7 Server Pool Auto-Placement

Deploying large topologies (20+ nodes) across multiple servers requires
assigning each node to a host. Manually setting `vm_host` in every profile is
tedious and error-prone. The **server pool** automates this.

**Configuration:** Define a `servers` list in the `newtlab` section of
`topology.json`:

```json
{
  "newtlab": {
    "servers": [
      { "name": "server-a", "address": "10.0.0.1", "max_nodes": 4 },
      { "name": "server-b", "address": "10.0.0.2", "max_nodes": 4 }
    ]
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Server name (used for `vm_host` pinning and state) |
| `address` | Yes | IP address (replaces `hosts` map) |
| `max_nodes` | No | Max VMs on this server (0 = unlimited) |

**Placement algorithm — spread (minimize maximum load):**

1. **Phase 1 (pinned nodes):** Nodes with `vm_host` set in their profile are
   validated against the server list and count toward capacity. Error if pinned
   to an unknown server or the server is over capacity.

2. **Phase 2 (unpinned nodes):** Remaining nodes are placed one at a time onto
   the server with the fewest nodes so far. Ties broken by server name sort
   order. Error if no server has capacity.

Iteration is deterministic (sorted by device name) — given the same
`topology.json`, every host computes the same placement independently. This
is required for `--host X` deploys where each server runs `newtlab deploy`
without coordination.

**Example:** 6 nodes, 2 servers (max 4 each), `leaf1` pinned to `server-a`:

```
Pinned:    leaf1 → server-a (count: a=1, b=0)
Unpinned:  leaf2 → server-b (a=1, b=0 → pick b)
           leaf3 → server-a (a=1, b=1 → tie, pick a)
           spine1 → server-b (a=2, b=1 → pick b)
           spine2 → server-a (a=2, b=2 → tie, pick a)
           spine3 → server-b (a=3, b=2 → pick b)
Result:    server-a: 3, server-b: 3
```

**Backward compatibility:** If no `servers` field is present, behavior is
identical to before — `hosts` map is used directly, and `vm_host` must be
set manually in each profile.

### 5.8 Virtual Hosts with VM Coalescing

Topologies can include first-class virtual hosts (e.g., `host1`, `host2`) defined as devices with platform `alpine-host` (which has `device_type: "host"`). Unlike switches, which each get their own QEMU instance, **multiple host devices are coalesced into a shared VM** to reduce resource overhead.

**VM coalescing model:**
- All host devices in the topology are grouped by platform
- Each group shares a single QEMU VM (e.g., `hostvm-0`)
- Inside the VM, each host gets its own **network namespace** created at deploy time
- Namespaces are pre-configured with veth pairs for connectivity
- Host IPs are auto-derived from switch-side interface IPs (or manually specified via `host_ip`/`host_gateway` in the device profile)

**Namespace provisioning** (`provisionHostNamespaces()`):
- For each host device (e.g., `host1`, `host2`):
  - Create namespace: `ip netns add host1`
  - Create veth pair: `veth-host1` (parent) ↔ `eth1` (inside namespace)
  - Assign IP address from topology or profile
  - Set default route via `host_gateway` (if specified)
- All network namespaces share the parent VM's management interface (eth0)

**SSH transparency:**
- `newtlab ssh host1` automatically enters the correct namespace
- Implementation: SSH to parent VM (`hostvm-0`), then `ip netns exec host1 sh`
- From the user's perspective, each host appears as a separate SSH target

**Status display:**
- `newtlab status` shows a TYPE column: `switch`, `host-vm`, `vhost:hostvm-0/host1`
- Virtual hosts (vhosts) display their parent VM and namespace
- The parent VM (`hostvm-0`) appears once as type `host-vm`

**Simplified boot sequence** (BootstrapHostNetwork):
- Waits for login prompt (no DHCP setup, no user creation)
- Assumes pre-configured credentials (Alpine cloud-init or preseed)
- No FRR, no SONiC-specific init

**Skipped operations:**
- `Provision()` — hosts are not provisioned via newtron
- `refreshBGP()` — no BGP on hosts

**Interface map:** Host platforms use the `"linux"` interface map, where QEMU NIC index N maps to `ethN` (NIC 0 = eth0 management, NIC 1+ = data interfaces).

Example platform definition:

```json
{
  "alpine-host": {
    "description": "Alpine Linux virtual host (coalesced into shared VM)",
    "device_type": "host",
    "vm_image": "~/.newtlab/images/alpine-host.qcow2",
    "vm_memory": 512,
    "vm_cpus": 2,
    "vm_nic_driver": "virtio-net-pci",
    "vm_interface_map": "linux",
    "vm_credentials": { "user": "root", "pass": "newtron" },
    "vm_boot_timeout": 30
  }
}
```

**NodeState tracking** (for virtual hosts):
- `VMName` field: parent VM name (e.g., `"hostvm-0"`)
- `Namespace` field: namespace name (e.g., `"host1"`)
- Switch devices leave these fields empty

Host devices are connected to leaf switches via newtlink the same way switches connect to each other. The difference is behavioral — hosts share a VM with namespaces for isolation, skip provisioning, and are accessed via `newtlab ssh <host>` for data plane testing.

### 5.9 Port Conflict Detection

Before starting any QEMU or bridge process, newtlab probes **all** allocated
ports to detect conflicts early. This catches issues that would otherwise
cause cryptic failures mid-deploy.

**Ports probed:**
- SSH ports (one per node, on the node's host)
- Console ports (one per node, on the node's host)
- Link ports (two per link — A-side and Z-side, on the bridge worker's host)
- Bridge stats ports (one per bridge worker host)

**Local ports** are tested with `net.Listen` — if the port can be bound, it's
free. **Remote ports** are tested via a single SSH connection per host running
`ss -tlnH` to check for listening sockets.

All conflicts are reported in a single multi-error message listing every
conflict with its purpose (e.g., "spine1 SSH: port 40000 in use").

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

**pkg/newtron/spec/types.go**:
```go
type DeviceProfile struct {
    // ... existing fields ...
    SSHPort int `json:"ssh_port,omitempty"`
}
```

**pkg/newtron/device/sonic/types.go**:
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
      "original_mgmt_ip": "PLACEHOLDER",
      "host": "server-a",
      "host_ip": "192.168.1.10"
    }
  },
  "links": [
    { "a": "spine1:Ethernet0", "z": "leaf1:Ethernet0", "a_port": 20000, "z_port": 20001, "worker_host": "server-a" }
  ],
  "bridges": {
    "": { "pid": 54321, "stats_addr": "127.0.0.1:19999" },
    "server-b": { "pid": 54322, "host_ip": "192.168.1.11", "stats_addr": "192.168.1.11:19998" }
  }
}
```

The `bridges` map tracks per-host bridge processes (see §5.5). The key is
the host name (`""` for local). `bridge_pid` is retained for backward
compatibility with older state files but is superseded by `bridges`.

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
  newtlab list                     List all deployed labs

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

### Phase 1: Core (done)
- Read topology.json, platforms.json, profiles/*.json
- Single-host QEMU deployment with platform-based image selection
- Interface map support (sequential, stride-4)
- Direct socket links from topology.json links (listen/connect model)
- Profile patching (mgmt_ip, ssh_port, console_port)
- CLI: deploy, destroy, status, ssh, console

### Phase 2: Polish (done)
- Boot timeout and health check
- NIC driver and CPU feature support
- Improved error messages

### Phase 3: Multi-Host (done)
- vm_host in profiles
- hosts map in topology.json newtlab section
- Cross-host socket links (direct listen/connect model)
- Per-host deployment (`--host`)
- Remote QEMU launch via SSH (`StartNodeRemote`/`StopNodeRemote`)

### Phase 4: newtlink Bridge Agent (done)
- Replace direct listen/connect sockets with newtlink bridge workers
- Each link gets a goroutine that listens on two ports and bridges frames
- Both QEMU VMs use `-netdev socket,connect=...` (no listen mode)
- Eliminates startup ordering dependency
- Unifies local and cross-host link handling
- newtlink process lifecycle tied to Lab (start before VMs, stop after)
- Per-link byte counters and session tracking
- Multi-host bridge distribution: one bridge process per WorkerHost
- TCP stats transport for remote bridge queries
- `status` aggregates counters from all hosts (bridge stats shown by default)
- Legacy `bridge_pid` preserved for backward compatibility

### Phase 5: Operations (done)
- Server pool auto-placement — spread algorithm with capacity constraints (§5.7)
- Comprehensive port conflict detection — all ports (SSH, console, link, stats), local and remote (§5.8)
- Platform boot patches — declarative patch framework (§5.6)

### Phase 6: Advanced (planned)
- Snapshot/restore (not yet implemented)
- Image management (not yet implemented)
