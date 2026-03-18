# newtlab — High-Level Design

For the architectural principles behind newtron, newtlab, and newtrun, see
[Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md).

---

## 1. Purpose and Boundaries

newtlab realizes network topologies as connected QEMU virtual machines,
wired together through **userspace socket bridges** — not kernel networking.
It reads newtron's spec files (`topology.json`, `platforms.json`,
`profiles/*.json`) and brings the topology to life: deploying VMs (primarily
SONiC), wiring them across one or more servers, and patching device profiles
so newtron can connect. No root, no Linux bridges, no veth pairs, no Docker.

newtlab occupies a specific position in the three-tool model:

- **newtlab** makes the topology physically exist — VMs, networking, boot
  patches, profile patching. It does not define the topology (that's the spec
  files) or touch device configuration (that's newtron).
- **newtron** configures devices — CONFIG_DB writes, BGP, EVPN, services,
  ACLs. It reads profiles that newtlab has patched with SSH ports but knows
  nothing about QEMU or bridges.
- **newtrun** orchestrates end-to-end test scenarios. It calls newtlab for
  deployment and newtron for device operations, but implements neither.

The boundary between newtlab and newtron is **profile patching**. newtlab
writes `ssh_port`, `console_port`, and `mgmt_ip` into device profiles;
newtron reads those profiles to connect. The two programs communicate through
files — no shared libraries, no IPC, no API calls between them.

```
              ┌────────────────────────────────┐
              │                                │
              │     Shared Spec Directory      │
              │ topology.json, platforms.json, │
              │ profiles/*.json, network.json  │
              │                                │ ─┐
              └────────────────────────────────┘  │
                │                                 │
                │ reads/writes                    │
                ▼                                 │
┌─────────┐     ┌────────────────────────────────┐  │
│         │     │                                │  │
│         │     │            newtlab             │  │
│ newtrun │     │        Deploy QEMU VMs         │  │
│  (E2E)  │     │       Socket networking        │  │ reads
│         │     │         Patch profiles         │  │
│         │ ──▶ │                                │  │
└─────────┘     └────────────────────────────────┘  │
  │               │                                 │
  │               │ profiles                        │
  │               │ (files)                         │
  │               ▼                                 │
  │             ┌────────────────────────────────┐  │
  │             │                                │  │
  │             │            newtron             │  │
  │             │       Provision devices        │  │
  │             │        Write CONFIG_DB         │  │
  │             │        BGP, EVPN, ACLs         │  │
  └───────────▶ │                                │ ◀┘
                └────────────────────────────────┘
```

**Benefits of the newtlab approach:**

- **Observable** — per-link byte counters, session state, and bridge stats
  aggregated across hosts. Every frame passes through userspace; nothing is
  invisible.
- **Debuggable** — one userspace process per host, not kernel bridge/iptables
  state. When a link breaks, you look at one process.
- **Multi-host native** — cross-host links work identically to local links
  via TCP sockets.
- **Unprivileged** — no root, no kernel modules, no Docker.
- **No startup ordering** — both VMs connect outbound to the bridge; neither
  needs to start first.
- **Multi-platform** — VS, VPP, Cisco 8000, vendor images, each with
  platform-specific boot patches applied at deploy time.

---

## 2. Architecture

A deployed newtlab topology consists of three layers: the spec directory
(input), the newtlab runtime (QEMU processes and bridge workers), and the
patched profiles (output that enables newtron).

```
┌────────────────────────────┐                ┌───────────────────────────────────────┐
│        spine1 QEMU         │                │        newtlink Bridge Process        │
│ mgmt: :40000, cons: :30000 │  TCP connect   │        worker: spine1 <> leaf1        │
│        overlay disk        │ <───────────── │    :20000 (A-side) :20001 (Z-side)    │
└────────────────────────────┘                └───────────────────────────────────────┘
  │                                             │
  │                                             │ TCP connect
  │                                             ∨
  │                                           ┌───────────────────────────────────────┐
  │                                           │              leaf1 QEMU               │
  │                                           │      mgmt: :40001, cons: :30001       │
  │                                           │             overlay disk              │
  │                                           └───────────────────────────────────────┘
  │                                             │
  │                                             │
  │                                             ∨
  │                                           ┌───────────────────────────────────────┐
  │                                           │                 State                 │
  │                                           │        ~/.newtlab/labs/<name>/        │
  └─────────────────────────────────────────> │ state.json, qemu/*.pid, disks/, logs/ │
                                              └───────────────────────────────────────┘
```

```
┌────────────────────────┐           ┌───────────────────────────┐     ┌─────────────────┐
│                        │           │                           │     │                 │
│ Spec Directory (input) │           │ Patched Profiles (output) │     │                 │
│     topology.json      │           │   profiles/spine1.json    │     │  newtron reads  │
│     platforms.json     │           │ + ssh_port, console_port  │     │   (to connect   │
│    profiles/*.json     │  deploy   │         + mgmt_ip         │     │ via SSH tunnel) │
│      network.json      │  patch    │                           │     │                 │
│                        │ ────────▶ │                           │ ──▶ │                 │
└────────────────────────┘           └───────────────────────────┘     └─────────────────┘
```

The diagram shows the key runtime relationships: both VMs connect *outbound*
to the bridge worker (not to each other), the bridge owns the link, and each
VM exposes management (SSH forwarding) and console (serial TCP) ports. The
state directory tracks everything needed for destroy, stop/start, and status.

**Component relationships:**

- **newtlab** reads spec files, creates QEMU processes and bridge workers,
  writes patched profiles, and maintains state.
- **newtlink** is the bridge agent — a set of goroutines within the newtlab
  bridge process. Each link gets one worker that bridges Ethernet frames
  between two TCP listeners. The bridge process starts before VMs (workers
  must be listening when QEMU tries to connect at boot).
- **newtron** is invoked as a sibling binary (`newtron provision`) during
  the optional provisioning step. newtlab finds it adjacent to its own
  binary or on PATH.
- **State directory** (`~/.newtlab/labs/<name>/`) holds everything needed to
  manage the lab after deployment: PIDs, overlay disks, logs, bridge config,
  and `state.json`.

---

## 3. Spec File Contract

newtlab shares a spec directory with newtron. Each tool reads the files it
needs; newtlab also writes to profiles after deployment. No new files are
required — the same directory structure serves both tools.

**What newtlab reads:**

| File | What newtlab uses |
|------|-------------------|
| `topology.json` | `devices` (names, interfaces), `links` (endpoint pairs), `newtlab` section (port bases, servers) |
| `platforms.json` | VM defaults per platform: image path, memory, CPUs, NIC driver, interface map, credentials, boot timeout, dataplane type, image release |
| `profiles/<device>.json` | Per-device overrides: platform selection, SSH credentials, VM resource overrides, host pinning (`vm_host`) |

newtlab reads `devices` and `links` from the same `topology.json` that
newtron reads, plus an optional `newtlab` section for orchestration settings:

```json
{
  "devices": {
    "spine1": {
      "interfaces": {
        "Ethernet0": { "link": "leaf1:Ethernet0", "ip": "10.1.0.0/31" }
      }
    },
    "leaf1": {
      "interfaces": {
        "Ethernet0": { "link": "spine1:Ethernet0", "ip": "10.1.0.1/31" }
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
      { "name": "server-a", "address": "192.168.1.10", "max_nodes": 4 }
    ]
  }
}
```

The `newtlab` section is the only part of `topology.json` that newtron
ignores. Everything else — devices, links, interface definitions — is shared.

**What newtlab writes** (after deployment):

| Profile field | Value | Purpose |
|---------------|-------|---------|
| `mgmt_ip` | `127.0.0.1` (local) or host IP (remote) | Where newtron connects |
| `ssh_port` | Allocated from `ssh_port_base` | SSH forwarded port for this VM |
| `console_port` | Allocated from `console_port_base` | Serial console TCP port |
| `MAC` | Deterministic from device name | Management NIC MAC address |

MAC addresses are derived from a SHA256 hash of the device name, using the
QEMU OUI prefix `52:54:00`. The same device name always produces the same
MAC, so ARP caches and DHCP leases survive stop/start and redeployment.
For switch devices, all data NICs share the management MAC — SONiC requires
a uniform system MAC across all interfaces.

On destroy, newtlab restores the original `mgmt_ip` and removes the runtime
fields — profiles return to their pre-deploy state.

**Interface maps.** Different SONiC platforms map QEMU NIC indices to
interface names differently. The platform's `vm_interface_map` field
controls this mapping:

| Map | NIC 1 | NIC 2 | NIC 3 | NIC 4 | Used by |
|-----|-------|-------|-------|-------|---------|
| `stride-4` | Ethernet0 | Ethernet4 | Ethernet8 | Ethernet12 | VS (built-in default) |
| `sequential` | Ethernet0 | Ethernet1 | Ethernet2 | Ethernet3 | VPP, CiscoVS |
| `linux` | eth1 | eth2 | eth3 | eth4 | Alpine hosts |

NIC 0 is always the management interface (`eth0`). newtlab uses the
interface map to determine which QEMU NIC index carries each topology link.

**Resolution order** for VM configuration (first non-empty wins):

1. Profile override (`profiles/<device>.json`)
2. Platform default (`platforms.json`)
3. Built-in default (4096 MB, 2 CPUs, `e1000`, `stride-4`)

Credentials resolve: profile `ssh_user`/`ssh_pass` → platform
`vm_credentials`. The LLD documents the complete resolution table with every
field.

**Relationship to newtron:** newtron reads profiles and `network.json` but
never reads `topology.json` or `platforms.json`. newtlab reads topology and
platforms but never reads `network.json`. newtlab imports newtron's spec
types for reading shared files, but has no dependency on newtron's device
operations or network model — the spec directory and the patched profiles
are the only coupling points.

---

## 4. Userspace Socket Networking

The link networking model is the single most important architectural decision
in newtlab. Every other choice — multi-host distribution, observability,
unprivileged operation — flows from it.

### Why Not Kernel Networking

The standard approach for lab networking is kernel-level: Linux bridges, veth
pairs, tc rules, iptables. This works, but it has properties that make it a
poor fit for a lab orchestrator:

- **Requires root.** Creating bridges, veth pairs, and network namespaces is a
  privileged operation. Running a SONiC lab shouldn't require root on the host.
- **Opaque.** When a link breaks, you debug bridge state, iptables rules, and
  tc configurations across multiple kernel subsystems. The data path is
  invisible — the kernel forwards frames without any userspace insight into
  what's happening.
- **Multi-host divergence.** Local links use bridges; cross-host links need
  tunnels (VXLAN, GRE, or userspace proxies). The two paths have different
  failure modes, different debugging, and different setup.

Userspace socket bridges avoid all three: no root (TCP sockets are
unprivileged), full visibility (every frame passes through a goroutine with
counters), and uniform local/remote behavior (TCP sockets work the same way
regardless of whether the peer is on the same host or across a network).

### The newtlink Bridge Model

Each link in `topology.json` becomes a **newtlink bridge worker** — a
goroutine that listens on two TCP ports (one per endpoint) and copies
Ethernet frames between them. Both QEMU VMs connect *outbound* to the
bridge; the bridge never connects to QEMU.

```
┌─────────────────────────────────────┐
│                                     │
│             leaf1 QEMU              │
│       connect=127.0.0.1:20001       │
│                                     │
└─────────────────────────────────────┘
  │
  │ connect
  ▼
┌─────────────────────────────────────┐
│                                     │
│            Listen :20001            │
│              (Z-side)               │
│                                     │
└─────────────────────────────────────┘
  ▲
  │
  │
┌─────────────────────────────────────┐
│                                     │
│       newtlink bridge worker        │
│ spine1:Ethernet0 <> leaf1:Ethernet0 │
│                                     │
└─────────────────────────────────────┘
  │
  └──────────────────────────────────────┐
                                         │
┌─────────────────────────────────────┐  │
│                                     │  │
│             spine1 QEMU             │  │
│       connect=127.0.0.1:20000       │  │
│                                     │  │
└─────────────────────────────────────┘  │
  │                                      │
  │ connect                              │
  ▼                                      │
┌─────────────────────────────────────┐  │
│                                     │  │
│            Listen :20000            │  │
│              (A-side)               │  │
│                                     │ ◀┘
└─────────────────────────────────────┘
```

The worker accepts one connection on each side, then enters a bidirectional
copy loop. If either VM disconnects (reboot, stop/start), the worker loops
back to re-accept — surviving VM restarts without newtlab intervention.

### Why newtlink Instead of Direct QEMU Sockets

QEMU's built-in `-netdev socket` supports a listen/connect model where one VM
listens and the other connects. This has three problems:

1. **Startup ordering** — the listen-side VM must start before the connect-side
   VM, creating a dependency graph that complicates parallel deployment.
2. **Asymmetry** — each side of a link uses different QEMU arguments (listen vs
   connect), so link wiring must track which side is which.
3. **Cross-host divergence** — local links use `127.0.0.1` while cross-host
   links need the remote host's IP, requiring different code paths.

newtlink eliminates all three: both VMs always `connect` to a known port,
newtlink handles the bridging, and the same model works for local and
cross-host links. For cross-host endpoints, newtlink just listens on
`0.0.0.0` instead of `127.0.0.1`.

### Cross-Host Links

For cross-host links, the bridge worker runs on one of the two endpoint hosts
(assigned by the worker placement algorithm — see §5). The local-side port
listens on `127.0.0.1`; the remote-side port listens on `0.0.0.0` so the
remote VM can connect across the network.

```
┌──────────────────────┐
│                      │
│      leaf1 QEMU      │
│      (server-b)      │
│                      │
└──────────────────────┘
  │
  │ connect=
  │ 192.168.1.10:20001
  ▼
┌──────────────────────┐
│                      │
│    0.0.0.0:20001     │
│       (remote)       │
│                      │
└──────────────────────┘
  ▲
  │
  │
┌──────────────────────┐
│                      │
│ newtlink on server-a │
│                      │
└──────────────────────┘
  │
  └───────────────────────┐
                          │
┌──────────────────────┐  │
│                      │  │
│     spine1 QEMU      │  │
│      (server-a)      │  │
│                      │  │
└──────────────────────┘  │
  │                       │
  │ local                 │
  ▼                       │
┌──────────────────────┐  │
│                      │  │
│   127.0.0.1:20000    │  │
│       (local)        │  │
│                      │ ◀┘
└──────────────────────┘
```

A cross-host link always costs exactly one network hop regardless of which
endpoint host runs the worker — one side is local, the other crosses the
network. Worker placement is about load balancing, not latency.

### Observability

Every bridge worker maintains per-link counters: bytes A→Z, bytes Z→A,
session count, and connection state — because every Ethernet frame passes
through the worker's copy loop.

Each bridge process exposes a stats endpoint. The local bridge listens on
both a Unix socket (fast path for `newtlab status` on the same host) and a
TCP port; remote bridges listen on TCP only. `newtlab status` queries all
bridge processes across all hosts and merges their counters into a single
table — giving a topology-wide view of traffic flow and link health without
any central collector.

---

## 5. Multi-Host Deployment

Topologies larger than a single server can support are distributed across a
**server pool** — a list of servers defined in `topology.json` with
addresses and optional capacity limits.

### Server Pool Configuration

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

Each server has a name (for identification and host pinning), an IP address
(for cross-host connections), and an optional `max_nodes` capacity limit.

### Node Placement

Nodes are assigned to servers using a **spread algorithm** that minimizes
the maximum load:

1. **Pinned nodes first.** Nodes with `vm_host` in their profile are placed
   on the named server and count toward its capacity.
2. **Unpinned nodes second.** Each remaining node goes to the server with
   the fewest nodes so far. Ties broken by server name.

Iteration is deterministic (sorted by device name) — given the same
`topology.json`, every host computes the same placement independently. This
is essential because each server runs `newtlab deploy --host X` without
coordination.

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

If no `servers` field is present, newtlab falls back to the legacy `hosts`
map, and `vm_host` must be set manually in each profile.

### The --host Model

In multi-host mode, each server runs `newtlab deploy --host <name>`
independently. The key insight: every instance reads the same spec files and
computes the *full* topology — placement, bridge assignments, port
allocation — then acts only on its own portion. Because the algorithms are
deterministic (sorted inputs, greedy with alphabetical tie-breaking), every
host arrives at the same answer independently. No coordination protocol, no
leader election, no shared state. The topology spec file *is* the
coordination mechanism.

### Bridge Worker Placement

Each bridge worker must run on one of its link's two endpoint hosts — never
a third host. For cross-host links, the worker is assigned to the endpoint
host with fewer cross-host workers so far (greedy, iterating links in sorted
order, ties broken alphabetically). Since every host computes the same
sorted order and the same greedy assignment, each instance knows exactly
which workers it owns.

**Example:** 2 spines on host-A, 4 leaves on host-B, full-mesh links:

```
Link 0: spine1↔leaf1  → A has 0, B has 0 → assign A (tie, A < B)
Link 1: spine1↔leaf2  → A has 1, B has 0 → assign B
Link 2: spine1↔leaf3  → A has 1, B has 1 → assign A
Link 3: spine1↔leaf4  → A has 2, B has 1 → assign B
Link 4: spine2↔leaf1  → A has 2, B has 2 → assign A
Link 5: spine2↔leaf2  → A has 3, B has 2 → assign B
Link 6: spine2↔leaf3  → A has 3, B has 3 → assign A
Link 7: spine2↔leaf4  → A has 4, B has 3 → assign B
Result: 4 workers on host-A, 4 on host-B (perfectly balanced)
```

Each host runs one bridge process containing all its assigned workers. The
bridge process is started before any QEMU processes (workers must be
listening before VMs try to connect).

---

## 6. Virtual Host Coalescing

Topologies often include virtual hosts for data-plane testing — Linux VMs
that act as traffic endpoints connected to leaf switches. Running each host
as a separate QEMU instance wastes resources: a topology with 8 hosts would
need 8 VMs, each consuming memory and CPU for what is essentially a network
namespace with an IP address.

### The Coalescing Model

newtlab **coalesces** host devices into shared QEMU VMs:

- All devices with `device_type: "host"` (from their platform) are grouped.
- Each group shares a single QEMU VM (named `hostvm-0`, `hostvm-1`, etc.).
- Inside the VM, each logical host gets its own **network namespace** with
  a dedicated data interface, IP address, and default route.
- Network namespaces are created at deploy time, not test time.

The result: 8 logical hosts consume 1 VM instead of 8. Each host has full
network isolation (separate routing table, separate interface) but shares
the kernel and management interface with its siblings.

### IP Auto-Derivation

Host IPs are derived from the switch-side topology interface that the host
connects to, eliminating manual IP assignment for most topologies:

- For `/31` links: the host gets the other address in the pair.
- For `/30` links: the host gets the switch IP + 1.
- For `/24` and wider: the host gets an offset based on host index.

Manual override is available via `host_ip` and `host_gateway` in the device
profile.

### What's Different About Hosts

Host devices follow a simpler lifecycle than switches:

- **Simplified boot.** Host VMs (Alpine Linux) auto-start DHCP and SSH —
  newtlab just waits for the login prompt, with no serial console login, no
  manual DHCP, no user creation.
- **No provisioning.** Hosts have no CONFIG_DB; `newtlab provision` and
  `newtron` skip them entirely.
- **No BGP refresh.** Hosts don't run FRR; the post-provision soft-clear
  skips them.
- **Linux interface map.** Host platforms use `"linux"` mapping where QEMU
  NIC index N maps directly to `ethN` (NIC 0 = eth0 management, NIC 1+ =
  data interfaces), unlike switches which use `stride-4` or `sequential`.

Host devices connect to leaf switches via newtlink the same way switches
connect to each other — the networking model is identical. The differences
are all behavioral.

### SSH Transparency

From the operator's perspective, each virtual host appears as a separate SSH
target:

```bash
newtlab ssh host1    # → SSH to hostvm-0, then: ip netns exec host1 bash
```

The `newtlab ssh` command detects virtual hosts and automatically enters the
correct namespace. `newtlab status` shows virtual hosts with their parent VM:

```
NODE      TYPE                   STATUS   IMAGE        SSH     CONSOLE  PID
switch1   switch                 running  sonic-cisco  :40000  :30000   12345
switch2   switch                 running  sonic-cisco  :40001  :30001   12346
hostvm-0  host-vm                running  alpine-host  :40002  :30002   12347
host1     vhost:hostvm-0/host1   running  -            -       -        -
host2     vhost:hostvm-0/host2   running  -            -       -        -
```

---

## 7. Platform Boot Patches

Different SONiC images have platform-specific initialization quirks — config
mode flags that need setting, factory hooks that need disabling, port
configurations that differ from what the image ships with. These need fixing
after boot but before provisioning.

### Why Patch at Deploy Time

The alternative is to bake fixes into the base image — maintain a custom
build per platform with all patches applied. This has two problems:

- **Build coupling.** Every upstream image update requires rebuilding with
  patches reapplied. Patches accumulate; tracking which patches are still
  needed is error-prone.
- **Release skew.** A patch that fixes a bug in release X may break release Y.
  The fix must be conditional on the release, which is a deployment concern,
  not a build concern.

Deploy-time patching keeps base images stock. When an upstream release fixes
a bug, you delete the patch descriptor — no rebuild required.

### Mechanism

The boot patch framework is declarative: JSON descriptors paired with Go
templates, organized by dataplane and optionally by release. Descriptors and
templates are embedded in the newtlab binary at build time — newtlab is a
single binary with no external files to manage or lose. Patches travel with
the binary; deploying a new newtlab version automatically deploys its
patches.

```
patches/
├── ciscovs/
│   └── always/
│       ├── 00-frrcfgd-mode.json        descriptor
│       ├── 00-frrcfgd-mode.tmpl        template (if needed)
│       └── ...
└── vpp/
    ├── always/
    │   ├── 01-disable-factory-hook.json
    │   └── 02-port-config.json
    └── 202405/                          release-specific
        └── 01-specific-fix.json
```

**Resolution order:**
1. `patches/<dataplane>/always/*.json` — applied to every image of this
   dataplane type, sorted by filename.
2. `patches/<dataplane>/<release>/*.json` — applied only when the platform's
   `vm_image_release` matches, sorted by filename, after always patches.

Numeric prefixes (`00-`, `01-`, `02-`) control ordering within each
directory.

**Each descriptor** can specify any combination of:
- `pre_commands` — shell commands run before applying changes
- `disable_files` — paths renamed to `.disabled`
- `files` — Go templates rendered with platform variables and uploaded to the VM
- `redis` — Go templates rendered into `redis-cli` commands for CONFIG_DB writes
- `post_commands` — shell commands run after all changes

**Template variables** are computed from what newtlab already knows: QEMU NIC
count, deterministic PCI addresses, platform HWSKU, port speed. No
SSH-based discovery inside the VM is needed.

### Adding a New Platform

Adding support for a new platform or fixing a new platform bug requires no
Go code changes. Create a directory under `patches/` with descriptors and
templates. When an upstream image fixes the bug, delete the patch directory.
The LLD documents the full descriptor schema and template variable reference.

---

## 8. Deploy Lifecycle

Deployment is an ordered sequence of phases, each building on the previous.
The design prioritizes two properties: **continue on error** (a single VM
failure should not prevent the rest of the topology from deploying) and
**always save state** (state is persisted immediately after creation so
`destroy` can clean up even after a partial deploy).

### Phase Overview

| Phase | What happens | Why |
|-------|-------------|-----|
| **Preflight** | Probe all allocated ports for conflicts | Catch issues before any process starts |
| **State** | Create state directory, generate Ed25519 SSH keypair, save initial state | State must exist before any process starts so destroy can clean up; the SSH key eliminates password prompts for subsequent operations (provisioning, newtrun test steps) |
| **Disks** | Create COW overlay disks from base images | Overlay disks let multiple labs share a base image; destroy just deletes overlays |
| **Bridges** | Start bridge processes, wait for listeners | Bridge workers must be listening before VMs connect |
| **Start** | Launch QEMU processes (parallel) | Each VM connects to its bridge ports on start |
| **Bootstrap** | Serial console login, DHCP, SSH readiness wait, key injection | The chicken-and-egg of VM bootstrap: SSH requires networking, but bringing up networking requires running commands on the VM. The serial console is the only way in before SSH exists. newtlab logs in via serial, runs `dhclient eth0` to get mgmt networking, then switches to SSH for everything else. |
| **Patch** | Apply platform boot patches, write patched profiles | Fix platform quirks, then enable newtron to connect |
| **Hosts** | Create network namespaces in coalesced VMs | Only runs when the topology includes virtual hosts |

If `--provision` is passed, an additional provisioning phase shells out to
the `newtron` binary for each switch device (parallel, with configurable
concurrency), followed by a BGP soft-clear (see §10).

### Overlay Disks

Each VM gets a COW (copy-on-write) overlay disk backed by the platform's
base image. This means:

- Multiple labs can share one base image without conflict.
- Destroying a lab just deletes the overlay — the base image is untouched.
- `stop` + `start` preserves the VM's disk state (the overlay persists).

### Error Strategy

Most deploy phases use **continue-on-error**: if one VM fails to boot or
patch, the rest of the topology still deploys. State tracks per-node status
(`running`, `error`) so the operator can see what succeeded and what didn't.
Port conflict detection is the exception — conflicts are reported as a single
multi-error listing every conflict, and deployment does not proceed.

---

## 9. State and Recovery

newtlab persists all runtime state to a lab directory. This is the single
source of truth for what's running — newtlab never discovers QEMU processes
by scanning the system.

### State Directory

```
~/.newtlab/labs/<name>/
├── state.json           # Lab state: nodes, links, bridges, ports
├── lab.key              # Ed25519 SSH key (generated per lab)
├── bridge.json          # Bridge worker configuration
├── qemu/
│   ├── spine1.pid       # QEMU PID file
│   └── spine1.mon       # QEMU monitor socket
├── disks/
│   └── spine1.qcow2    # COW overlay disk
└── logs/
    └── spine1.log       # QEMU stdout/stderr
```

### state.json

Tracks the complete runtime state of the lab:

```json
{
  "name": "2node-ngdp",
  "spec_dir": "/home/user/newtrun/topologies/2node-ngdp/specs",
  "nodes": {
    "switch1": {
      "pid": 12345, "status": "running",
      "ssh_port": 40000, "console_port": 30000,
      "original_mgmt_ip": "PLACEHOLDER",
      "host": "", "host_ip": ""
    }
  },
  "links": [
    { "a": "switch1:Ethernet0", "z": "switch2:Ethernet0",
      "a_port": 20000, "z_port": 20001, "worker_host": "" }
  ],
  "bridges": {
    "": { "pid": 54321, "stats_addr": "127.0.0.1:19999" }
  }
}
```

The `bridges` map is keyed by host name (`""` for local). Each node records
its `original_mgmt_ip` so destroy can restore the profile. The LLD documents
the complete field set.

### Lifecycle Operations

- **Deploy** creates the state directory and populates it incrementally.
  State is saved after each phase so partial deploys are always destroyable.
- **Destroy** kills QEMU processes, stops bridges, removes overlay disks,
  restores profiles, and deletes the state directory. Virtual host entries
  are skipped — they are killed with their parent VM.
- **Stop** / **Start** kill and relaunch individual QEMU processes. The
  overlay disk persists across stop/start, preserving the VM's state.
- **List** scans `~/.newtlab/labs/` for deployed topologies and shows their
  status.

---

## 10. newtron Integration

newtlab and newtron are separate binaries that communicate through files.
This loose coupling is deliberate — newtlab imports newtron's spec types
for reading shared files, but has no dependency on newtron's device
operations, CONFIG_DB logic, or network model. The two tools can be built
and updated independently.

### Profile Patching Is the Interface

The entire integration surface is a handful of profile fields. newtlab
writes them after deployment; newtron reads them when connecting to devices:

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

Profiles without `ssh_port` default to port 22 — backward compatible with
profiles that were never deployed by newtlab. On destroy, newtlab restores
the original `mgmt_ip` and removes the runtime fields.

### Provisioning Shells Out

When `--provision` is passed, newtlab invokes `newtron provision -S <specs>
-D <device> -x` as a subprocess for each switch device. This is intentional:

- **No library coupling.** newtlab depends on spec types for reading
  topology files, but has no dependency on newtron's device operations,
  CONFIG_DB logic, or network model.
- **Independent versioning.** newtlab and newtron can be built and updated
  independently. A newtron change never requires rebuilding newtlab.
- **Binary resolution.** newtlab looks for the `newtron` binary adjacent to
  its own binary first, then falls back to PATH.

### Post-Provision BGP Refresh

After parallel provisioning completes, newtlab waits briefly then issues
`vtysh -c 'clear bgp * soft'` on every device. This handles a race
condition: when devices are provisioned in parallel, some peers may not have
finished configuring BGP when others try to establish sessions. The soft
clear causes all devices to re-attempt peering after everyone is configured.

---

## 11. CLI

newtlab's CLI provides topology lifecycle management and device access.

| Command | Description |
|---------|-------------|
| `newtlab deploy [topology]` | Deploy VMs from spec files |
| `newtlab destroy [topology]` | Stop all VMs, remove state |
| `newtlab status [topology]` | Show node and link status with live bridge stats |
| `newtlab ssh <node>` | SSH to a VM (namespace-aware for virtual hosts) |
| `newtlab console <node>` | Attach to serial console via socat/telnet |
| `newtlab stop <node>` | Stop a VM (preserves overlay disk) |
| `newtlab start <node>` | Start a stopped VM |
| `newtlab provision [topology]` | Provision devices via newtron |
| `newtlab list` | List all deployed labs |

**Key flags:** `-S <dir>` (spec directory), `--provision` (deploy +
provision), `--host <name>` (multi-host mode), `--force` (redeploy over
existing), `--parallel <n>` (provisioning concurrency), `-v` (verbose
output).

**Topology resolution** (when no `-S` flag):
1. Positional argument matching a deployed lab by name
2. Positional argument resolved under the topologies directory
3. Auto-detect: exactly one deployed lab

The LLD documents the complete flag set and topology resolution logic.

---

## 12. End-to-End Walkthrough

This traces `newtlab deploy 2node-ngdp --provision` through the architecture,
focusing on the moments where layers interact in non-obvious ways.

### Spec Resolution and Port Planning

newtlab resolves `2node-ngdp` to `newtrun/topologies/2node-ngdp/specs/`. It loads
`topology.json` (two switches, links between them), `platforms.json`
(CiscoVS platform: `sequential` interface map, `e1000` NIC driver, 8 GB
memory), and both device profiles. Configuration resolution merges profile
overrides with platform defaults; port allocation assigns SSH, console, and
link ports by sorted device index.

**Non-obvious detail:** link allocation must resolve the interface map
*before* building QEMU commands. The topology says `spine1:Ethernet0 ↔
leaf1:Ethernet0`. With `sequential`, Ethernet0 is NIC index 1 (not 0 — NIC 0
is management). The interface map determines which QEMU `-netdev socket`
argument carries this link. A wrong mapping means traffic goes to the wrong
port — the VMs boot fine but can't reach each other.

### Bridge Before VMs

The bridge process starts first — its workers begin listening on link ports
(`:20000`, `:20001`). This ordering is mandatory: QEMU's `-netdev
socket,connect=127.0.0.1:20000` tries to connect immediately at VM boot. If
the bridge isn't listening yet, QEMU retries internally, but some platforms
treat connection failure as a fatal NIC error. Starting bridges first
eliminates the race entirely.

### The Serial Console Chicken-and-Egg

QEMU processes launch for both switches. Each VM boots from its COW overlay
disk. But at this point the VMs have no management networking — they're
isolated QEMU instances with forwarded SSH ports that nothing is listening
on yet.

This is the bootstrap chicken-and-egg: newtlab needs SSH to configure the
VM, but SSH requires networking, and bringing up networking requires running
commands *on the VM*. The serial console — a raw TCP connection to QEMU's
emulated serial port — is the only way in. newtlab connects to the serial
console, logs in with the platform's console credentials, runs
`ip link set eth0 up` and `dhclient eth0` to get DHCP on the management
NIC, and creates the SSH user if needed. Only then does SSH become reachable.

Bootstrap runs in parallel across all VMs. After SSH is up, newtlab injects
the lab's Ed25519 key — subsequent operations (boot patches, provisioning,
newtrun test steps) use key-based auth with no password prompts.

### Boot Patches and the Handoff to newtron

Platform boot patches are applied via SSH — for CiscoVS, this means
switching to unified FRR config mode (required for frrcfgd to manage all
protocols), installing the VNI bootstrap helper, and waiting for the swss
container to be ready. These run AFTER SSH is up but BEFORE profile patching
— patches may change the SSH credentials or service state that subsequent
steps depend on.

After patches complete, newtlab writes runtime values into the device
profiles: `mgmt_ip`, `ssh_port`, `console_port`. This is the handoff point:
from here on, newtron can connect to the devices using the standard profile
path. newtlab's job (physical infrastructure) is done; newtron's job (device
configuration) begins.

### Provisioning and the Parallel Race

Because `--provision` was passed, newtlab invokes `newtron provision` for
each switch as a subprocess. newtron reads the patched profiles, connects
via SSH tunnel, and writes CONFIG_DB entries for BGP, EVPN, loopback, and
topology interfaces. Both switches provision in parallel.

**Non-obvious problem:** switch1 finishes provisioning and its BGP daemon
tries to peer with switch2. But switch2 is still being provisioned — its
BGP configuration doesn't exist yet. The session attempt fails and enters a
backoff timer. By the time switch2 finishes, switch1's BGP daemon is
waiting on a retry timer that could be tens of seconds.

The post-provision BGP soft-clear (§10) solves this: after all devices
finish, newtlab tells every BGP daemon to re-attempt all sessions
immediately. Sessions establish within seconds instead of waiting for
backoff expiry.

### Result

Two SONiC VMs are running, connected through a newtlink bridge, with BGP
sessions established and EVPN overlay configured. `newtlab status` shows
both nodes as `running` with live bridge stats showing bytes flowing across
the link. newtron can connect to either device using the patched profile
ports. newtrun can orchestrate test scenarios against the topology.

---

## 13. Cross-References

| Document | What it covers |
|----------|---------------|
| [newtlab LLD](lld.md) | Types, functions, deploy phases in detail, port formulas, interface map resolution, complete CLI flags |
| [newtron HLD](../newtron/hld.md) | Device configuration architecture, CONFIG_DB interaction, verification model |
| [newtrun HLD](../newtrun/hld.md) | End-to-end test framework, step actions, suite mode |
| [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md) | Architectural philosophy behind all three tools |
| [newtlab HOWTO](howto.md) | Deploying topologies, troubleshooting, multi-host setup |
