# newtlab Low-Level Design (LLD)

newtlab realizes network topologies as connected QEMU virtual machines for
SONiC lab environments. This document covers `pkg/newtlab/` — the VM lifecycle,
networking, and port management layer. For architectural motivation (why
userspace bridges, why boot patches, why server pool placement), see [newtlab
HLD](hld.md). For the device connection layer used after VMs boot, see [Device
Layer LLD](../newtron/device-lld.md).

---

## 1. Package Layout

Each source file owns a cohesive subsystem. A reader can guess where a feature
is implemented by looking at the file name.

| File | Responsibility | Key types/functions |
|------|---------------|---------------------|
| `newtlab.go` | Lab orchestrator — NewLab, Deploy, Destroy, Status, Stop, Start, Provision | `Lab`, `HostVMGroup` |
| `node.go` | Node config resolution, MAC generation | `NodeConfig`, `NICConfig`, `ResolveNodeConfig`, `GenerateMAC` |
| `link.go` | Link allocation, NIC assignment, worker placement, bridge workers | `VMLabConfig`, `LinkConfig`, `LinkEndpoint`, `HostMapping`, `AllocateLinks`, `PlaceWorkers`, `Bridge`, `BridgeWorker` |
| `bridge.go` | Bridge config serialization, process management, stats | `BridgeConfig`, `BridgeLink`, `BridgeStats`, `LinkStats`, `WriteBridgeConfig`, `RunBridgeFromFile`, `QueryBridgeStats` |
| `iface_map.go` | Interface name → QEMU NIC index resolution | `ResolveNICIndex` |
| `qemu.go` | QEMU command builder, node start/stop/running checks | `QEMUCommand`, `StartNode`, `StopNode`, `IsRunning` |
| `boot.go` | Serial console bootstrap, SSH key generation, SSH readiness | `BootstrapNetwork`, `BootstrapHostNetwork`, `WaitForSSH`, `GenerateLabSSHKey` |
| `patch.go` | Boot patch framework — resolve, render, apply | `BootPatch`, `FilePatch`, `RedisPatch`, `PatchVars`, `ResolveBootPatches`, `ApplyBootPatches` |
| `profile.go` | Profile patching (post-deploy) and restoration (destroy) | `PatchProfiles`, `RestoreProfiles` |
| `placement.go` | Server pool node placement | `PlaceNodes` |
| `probe.go` | Port conflict detection (local and remote) | `PortAllocation`, `CollectAllPorts`, `ProbeAllPorts` |
| `disk.go` | Overlay disk creation, remote state dir management | `CreateOverlay`, `CreateOverlayRemote` |
| `remote.go` | SSH/SCP helpers, newtlink upload, home dir caching | `sshCommand`, `scpCommand`, `uploadNewtlink` |
| `shell.go` | Shell quoting for remote commands | `shellQuote`, `singleQuote`, `quoteArgs` |
| `state.go` | State persistence (save/load/remove/list) | `LabState`, `NodeState`, `BridgeState`, `LinkState` |

---

## 2. Spec Type Extensions

newtlab extends existing spec types in `pkg/newtron/spec/types.go` with VM
configuration fields. These types are defined once in the spec package and
consumed by newtlab during resolution (§3.4).

### 2.1 PlatformSpec — VM Fields

Platform-level defaults for all devices of a given SONiC platform. Consumed
by `ResolveNodeConfig` (§3.3) to provide defaults when device profiles don't
override.

```go
type PlatformSpec struct {
    // SONiC platform fields (omitted for brevity)
    HWSKU        string   `json:"hwsku"`
    PortCount    int      `json:"port_count"`
    DefaultSpeed string   `json:"default_speed"`
    DeviceType   string   `json:"device_type,omitempty"` // "switch" (default) or "host"

    // newtlab VM fields
    VMImage              string         `json:"vm_image,omitempty"`
    VMMemory             int            `json:"vm_memory,omitempty"`
    VMCPUs               int            `json:"vm_cpus,omitempty"`
    VMNICDriver          string         `json:"vm_nic_driver,omitempty"`
    VMInterfaceMap       string         `json:"vm_interface_map,omitempty"`
    VMInterfaceMapCustom map[string]int `json:"vm_interface_map_custom,omitempty"`
    VMCPUFeatures        string         `json:"vm_cpu_features,omitempty"`
    VMCredentials        *VMCredentials `json:"vm_credentials,omitempty"`
    VMBootTimeout        int            `json:"vm_boot_timeout,omitempty"`
    Dataplane            string         `json:"dataplane,omitempty"`
    VMImageRelease       string         `json:"vm_image_release,omitempty"`
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `vm_image` | string | (required) | Path to QCOW2 base image |
| `vm_memory` | int | 4096 | RAM in MB |
| `vm_cpus` | int | 2 | vCPU count |
| `vm_nic_driver` | string | `"e1000"` | QEMU NIC model (`e1000`, `virtio-net-pci`) |
| `vm_interface_map` | string | `"stride-4"` | NIC-to-interface mapping scheme (§5.3) |
| `vm_interface_map_custom` | map[string]int | nil | Custom SONiC name → NIC index (for `"custom"` scheme) |
| `vm_cpu_features` | string | `""` | QEMU `-cpu host,<features>` suffix |
| `vm_credentials` | *VMCredentials | nil | Image-baked login credentials |
| `vm_boot_timeout` | int | 180 | Seconds to wait for boot |
| `dataplane` | string | `""` | Selects boot patch directory: `"vpp"`, `"ciscovs"`, `""` |
| `vm_image_release` | string | `""` | Release-specific boot patches (e.g., `"202405"`) |
| `device_type` | string | `"switch"` | `"host"` triggers VM coalescing (§3.6) |

### 2.2 VMCredentials

Image-baked login credentials for serial console bootstrap (§7.1) and SSH
password fallback.

```go
type VMCredentials struct {
    User string `json:"user"` // user baked into the image (e.g., "aldrin" for CiscoVS)
    Pass string `json:"pass"` // image default password
}
```

Used as `ConsoleUser`/`ConsolePass` during serial bootstrap (§7.1) and as SSH
password fallback when the device profile doesn't specify `ssh_pass`.

### 2.3 DeviceProfile — newtlab Fields

Per-device overrides that take priority over platform defaults. The newtlab
fields on DeviceProfile serve two purposes: some are **read** by newtlab during
resolution, others are **written** by `PatchProfiles` (§8.1) after deploy so
that newtron can discover SSH connectivity.

```go
type DeviceProfile struct {
    // ... existing fields (mgmt_ip, loopback_ip, zone, evpn, etc.) ...
    OverridableSpecs // embedded — node-level spec overrides (services, filters, etc.)
    ASNumber *int    `json:"as_number,omitempty"` // override underlay ASN

    // Read by newtlab (user-set)
    VMMemory int    `json:"vm_memory,omitempty"`
    VMCPUs   int    `json:"vm_cpus,omitempty"`
    VMImage  string `json:"vm_image,omitempty"`
    VMHost   string `json:"vm_host,omitempty"` // server pool target

    // Written by PatchProfiles (§8.1) / read by newtron Device Layer LLD §1
    SSHPort     int    `json:"ssh_port,omitempty"`
    ConsolePort int    `json:"console_port,omitempty"`
    MAC         string `json:"mac,omitempty"` // deterministic system MAC

    // Read by newtlab (SSH credentials)
    SSHUser string `json:"ssh_user,omitempty"`
    SSHPass string `json:"ssh_pass,omitempty"`

    // Virtual host fields (auto-derived if omitted)
    HostIP      string `json:"host_ip,omitempty"`
    HostGateway string `json:"host_gateway,omitempty"`
}
```

`SSHPort` defaults to 0 in profiles without newtlab patching. newtron's
`NewSSHTunnel` treats port 0 as port 22, maintaining backward compatibility
with profiles that predate newtlab.

### 2.4 TopologySpecFile.NewtLab

The optional `newtlab` key in `topology.json` provides orchestration overrides.

```go
type TopologySpecFile struct {
    Version  string                     `json:"version"`
    Platform string                     `json:"platform,omitempty"`
    Devices  map[string]*TopologyDevice `json:"devices"`
    Links    []*TopologyLink            `json:"links,omitempty"`
    NewtLab  *NewtLabConfig             `json:"newtlab,omitempty"`
}
```

### 2.5 NewtLabConfig and ServerConfig

Port base values and server pool configuration for multi-host deployments.
Resolved into `VMLabConfig` (§3.2) by `resolveNewtLabConfig()`.

```go
type NewtLabConfig struct {
    LinkPortBase    int               `json:"link_port_base,omitempty"`
    ConsolePortBase int               `json:"console_port_base,omitempty"`
    SSHPortBase     int               `json:"ssh_port_base,omitempty"`
    Hosts           map[string]string `json:"hosts,omitempty"`   // legacy: name → IP
    Servers         []*ServerConfig   `json:"servers,omitempty"` // server pool
}

type ServerConfig struct {
    Name     string `json:"name"`
    Address  string `json:"address"`
    MaxNodes int    `json:"max_nodes,omitempty"` // 0 = unlimited
}
```

Resolved by `resolveNewtLabConfig()` into `VMLabConfig` (§3.2) with defaults
applied. When `Servers` is populated, `Hosts` is auto-derived from server
addresses.

---

## 3. Core Types

These types live in `pkg/newtlab/` and represent the runtime data model that
drives VM creation, networking, and lifecycle management.

### 3.1 Lab

The top-level orchestrator. Created by `NewLab()` (§4.1), consumed by
`Deploy()`, `Destroy()`, `Status()`, `Stop()`, `Start()`, and `Provision()`.

```go
type Lab struct {
    Name         string
    SpecDir      string
    StateDir     string
    Topology     *spec.TopologySpecFile
    Platform     *spec.PlatformSpecFile
    Profiles     map[string]*spec.DeviceProfile
    Config       *VMLabConfig
    Nodes        map[string]*NodeConfig
    Links        []*LinkConfig
    State        *LabState
    HostVMs      []*HostVMGroup
    Force        bool
    DeviceFilter []string

    OnProgress func(phase, detail string) // optional progress callback
}
```

### 3.2 VMLabConfig

Resolved form of `spec.NewtLabConfig` (§2.5) with defaults applied. Consumed
by `NewLab()` (§4.1) for port base arithmetic, `AllocateLinks()` (§5.2) for
port assignment, and `PlaceNodes()` (§9.1) for server pool access.

```go
type VMLabConfig struct {
    LinkPortBase    int               // default: 20000
    ConsolePortBase int               // default: 30000
    SSHPortBase     int               // default: 40000
    Hosts           map[string]string // host name → IP
    Servers         []*spec.ServerConfig
}
```

### 3.3 NodeConfig and NICConfig

`NodeConfig` is the fully resolved VM configuration for a single device.
Built by `ResolveNodeConfig()` in `node.go`. `NICConfig` is appended during
link allocation (§5.2).

```go
type NodeConfig struct {
    Name         string
    Platform     string
    DeviceType   string     // "switch" (default) or "host" — from platform; "host-vm" set by coalescing (§3.6)
    Image        string     // QCOW2 path
    Memory       int        // MB
    CPUs         int
    NICDriver    string     // "e1000" or "virtio-net-pci"
    InterfaceMap string     // "stride-4", "sequential", "linux", "custom"
    CPUFeatures  string
    SSHUser      string
    SSHPass      string
    ConsoleUser  string     // from platform vm_credentials
    ConsolePass  string
    BootTimeout  int        // seconds
    Host         string     // server pool target ("" = local)
    SSHPort      int        // allocated by NewLab
    ConsolePort  int        // allocated by NewLab
    NICs         []NICConfig
}

type NICConfig struct {
    Index       int    // QEMU NIC index (0=mgmt, 1..N=data)
    NetdevID    string // "mgmt", "eth1", "eth2", ...
    Interface   string // SONiC interface name ("Ethernet0") or "mgmt"
    ConnectAddr string // "IP:PORT" — connects to bridge worker
    MAC         string // deterministic MAC (§3.7)
}
```

### 3.4 Resolution Order

`ResolveNodeConfig()` merges values from three layers. For each field, the
first non-zero value wins.

| Field | Profile | Platform | Built-in Default |
|-------|---------|----------|-----------------|
| Image | `vm_image` | `vm_image` | **error** |
| Memory | `vm_memory` | `vm_memory` | 4096 |
| CPUs | `vm_cpus` | `vm_cpus` | 2 |
| NICDriver | — | `vm_nic_driver` | `"e1000"` |
| InterfaceMap | — | `vm_interface_map` | `"stride-4"` |
| CPUFeatures | — | `vm_cpu_features` | `""` |
| SSHUser | `ssh_user` | — | `"admin"` |
| SSHPass | `ssh_pass` | `vm_credentials.pass` | `""` |
| ConsoleUser | — | `vm_credentials.user` | `""` |
| ConsolePass | — | `vm_credentials.pass` | `""` |
| BootTimeout | — | `vm_boot_timeout` | 180 |
| Host | `vm_host` | — | `""` (local) |
| DeviceType | — | `device_type` | `""` (switch) |

After resolution, `NewLab()` allocates sequential SSH and console ports:
`SSHPort = SSHPortBase + i`, `ConsolePort = ConsolePortBase + i` where `i` is
the device's index in the sorted device name list.

**NIC 0 is always management.** `ResolveNodeConfig()` creates the management
NIC entry. Data NICs (index 1..N) are appended later by `AllocateLinks()`
(§5.2).

### 3.5 LinkConfig and LinkEndpoint

Represent a resolved link between two device NICs. Built by `AllocateLinks()`
(§5.2), consumed by `QEMUCommand.Build()` (§6.1) and bridge worker setup.

```go
type LinkConfig struct {
    A          LinkEndpoint
    Z          LinkEndpoint
    APort      int    // bridge worker A-side TCP port
    ZPort      int    // bridge worker Z-side TCP port
    ABind      string // "127.0.0.1" or "0.0.0.0"
    ZBind      string
    WorkerHost string // host running the bridge ("" = local)
}

type LinkEndpoint struct {
    Device    string // device name
    Interface string // SONiC interface name
    NICIndex  int    // QEMU NIC index (from interface map)
}
```

### 3.6 HostVMGroup and HostMapping

Virtual hosts (devices with `device_type: "host"`) are coalesced into shared
QEMU VMs during `NewLab()`. Each group of host devices on the same `vm_host`
becomes a single VM with network namespaces inside it. For architectural
motivation, see HLD §6.

```go
type HostVMGroup struct {
    VMName  string         // synthetic name (e.g., "hostvm-0")
    Hosts   []string       // sorted logical host names
    NICBase map[string]int // host name → base NIC index on VM
}

type HostMapping struct {
    VMName  string // parent VM name
    NICBase int    // first NIC index for this host's links
}
```

The coalescing algorithm in `coalesceHostVMs()`:

1. Identifies all `NodeConfig` entries with `DeviceType == "host"`.
2. Groups them by `VMHost` (physical server), sorts for determinism.
3. Creates one synthetic `NodeConfig` per group (`hostvm-0`, `hostvm-1`, ...) using the first host's config as template, with `DeviceType = "host-vm"`.
4. Computes NIC base indices: NIC 0 = mgmt, then each host gets a contiguous range of NICs sized by its link count.
5. Removes individual host `NodeConfig` entries, adds the synthetic VM entry.
6. Builds a `HostMapping` map for use by `AllocateLinks()` — when a link references a coalesced host, AllocateLinks remaps the device name to the VM and computes the NIC index as `NICBase + ethIdx`.

### 3.7 GenerateMAC

Deterministic MAC address generation ensures stable MAC addresses across
reboots. Uses QEMU's OUI prefix (`52:54:00`) with the last 3 octets derived
from SHA-256 of `"nodeName-nicIndex"`.

```go
func GenerateMAC(nodeName string, nicIndex int) string
```

SONiC switches require all data NICs to share the same system MAC (the switch's
`DEVICE_METADATA|localhost|mac`). So for switches, all NICs use
`GenerateMAC(name, 0)`. Host VMs need unique MACs per NIC to avoid L2 flapping,
so they use `GenerateMAC(name, nicIndex)`. This logic lives in `dataNICMAC()`
in `link.go`.

---

## 4. Lab Lifecycle

The Lab lifecycle is a two-phase process: `NewLab` resolves all spec files into
a fully planned deployment (types, ports, placements) without starting any
processes, then `Deploy` executes that plan phase by phase. `Destroy`, `Stop`,
`Start`, and `Status` operate on the state that `Deploy` persisted.

### 4.1 NewLab

`NewLab(specDir)` loads all spec files and produces a fully resolved `Lab`
ready for deployment. It does not start any processes.

Steps:

1. Resolve `specDir` to absolute path. Derive lab name from parent directory (e.g., `newtrun/topologies/2node/specs` → `"2node"`).
2. Load and parse `topology.json`, `platforms.json`.
3. If `topology.Links` is empty, derive links from `interface.link` fields via `DeriveLinksFromInterfaces()`.
4. Load `profiles/<device>.json` for each device in the topology.
5. Resolve `VMLabConfig` from `topology.NewtLab` with defaults.
6. For each device (sorted by name): resolve platform, call `ResolveNodeConfig()`, allocate SSH/console ports.
7. Coalesce host devices into shared VMs (`coalesceHostVMs()`, §3.6).
8. Auto-place nodes across server pool if configured (`PlaceNodes()`, §9.1).
9. Allocate links (`AllocateLinks()`, §5.2) — assigns NIC indices, TCP ports, worker hosts, and appends NIC entries to NodeConfigs.

### 4.2 Deploy

`Lab.Deploy(ctx)` creates VMs and waits for them to be SSH-reachable.
The context is checked at each major phase for cancellation.

**Phase 1 — Pre-checks:**
- Check for stale state; if `--force`, destroy existing.
- Collect all port allocations (`CollectAllPorts`, §9.2) and probe for conflicts (`ProbeAllPorts`).
- Create local state directories: `qemu/`, `disks/`, `logs/`.
- Generate lab-specific Ed25519 SSH key pair (`GenerateLabSSHKey`).

**Phase 2 — Initialize state:**
- Build initial `LabState` with link entries. Save immediately.
- Create remote state directories on unique remote hosts.

**Phase 3 — Create overlay disks:**
- Local: `qemu-img create -f qcow2 -b <base> -F qcow2 <overlay>`.
- Remote: same command via SSH (`CreateOverlayRemote`).

**Phase 4 — Start bridge workers** (`setupBridges`):
- Group links by `WorkerHost`.
- Per host: serialize `BridgeConfig` (§5.5) → start `newtlink` process (local or remote) → wait for all link ports to accept connections.
- Stats ports follow the scheme in §5.1.

**Phase 5 — Boot VMs** (`startNodes`):
- For each node (sorted): build `QEMUCommand`, start local or remote.
- Continues on error — remaining nodes are still started.

**Phase 6 — Bootstrap** (`bootstrapNodes`):
- Phase 6a (parallel): Serial console bootstrap. Switches: login, bring up eth0, DHCP, create SSH user (`BootstrapNetwork`, §7.1). Host VMs: wait for login prompt only (`BootstrapHostNetwork`, §7.2).
- Phase 6b (parallel): Wait for SSH readiness (`WaitForSSH`, §7.3).
- Phase 6c (sequential, best-effort): Inject lab SSH public key via SSH.

**Phase 7 — Boot patches** (`applyNodePatches`):
- For each switch node (parallel): resolve and apply platform boot patches (`ApplyBootPatches`, §7.4).
- Patch device profiles (`PatchProfiles`, §8.1).

**Phase 8 — Host namespaces** (`provisionHostNamespaces`, if HostVMs exist):
- SSH into each coalesced VM.
- For each logical host: create network namespace, move data NIC into namespace, rename to `eth0`, assign IP and default route (§4.6).

**Phase 9 — Finalize:**
- Create virtual host state entries (mirrors parent VM's PID/status).
- Save final state. Report progress.

**Failure recovery:** If a QEMU process fails to start (Phase 5) or SSH
readiness times out (Phase 6), Deploy still saves state with the failed node
marked as `"error"`. This leaves a valid `state.json` so that
`newtlab destroy` can clean up all started nodes. Partial deploys do **not**
auto-rollback — explicit destroy is required.

### 4.3 Destroy

`Lab.Destroy(ctx)` tears down a deployed lab:

1. Load state from `state.json`.
2. Kill QEMU processes (skip virtual host entries — killed with parent VM).
3. Stop bridge processes per host (`stopAllBridges`).
4. Clean up remote state directories.
5. Restore profiles (`RestoreProfiles`, §8.2).
6. Remove local state directory.

Uses continue-on-error: collects all failures rather than stopping at first.
A failed process kill should not prevent profile restoration or state cleanup.
Returns all errors aggregated.

### 4.4 Status

`Lab.Status()` loads state, then live-checks each PID via `IsRunning()`. Nodes
whose process has exited have their status updated from `"running"` to
`"stopped"`.

### 4.5 Stop and Start

`Lab.Stop(ctx, nodeName)` sends SIGTERM (then SIGKILL after 10s) to a node's
QEMU process and marks it `"stopped"` in state.

`Lab.Start(ctx, nodeName)` re-reads specs via `NewLab()`, restores the node's
allocated ports from state, starts QEMU (or detects a still-running process),
and waits for SSH.

Both operate on a single named node. `StopByName`/`StartByName` are convenience
wrappers that only require lab name and node name.

`Lab.FilterHost(host)` removes nodes not assigned to the given host. Nodes
without a `vm_host` field are retained (single-host mode). Matching nodes have
their `Host` cleared so they run locally. Links referencing filtered-out nodes
are also removed. Used by the `deploy --host` flag to run a subset of a
multi-host topology on one server.

### 4.6 Host Namespace Provisioning

After coalesced host VMs boot, `provisionHostNamespaces()` SSHs into each VM
and runs a script per logical host:

```
ip netns add <hostName>
mkdir -p /etc/netns/<hostName>
echo 'export PS1="<hostName>:\w# "' > /etc/netns/<hostName>/profile
ip link set eth<nicBase> netns <hostName>
ip netns exec <hostName> ip link set eth<nicBase> name eth0
ip netns exec <hostName> ip link set eth0 up
ip netns exec <hostName> ip link set lo up
ip netns exec <hostName> ip addr add <hostIP> dev eth0
ip netns exec <hostName> ip route add default via <gateway>
```

IP derivation when `host_ip` is not set in the profile:
- Finds the peer switch interface IP from topology `interface.link` fields.
- For /31: toggles even↔odd (RFC 3021).
- For /30: switch IP + 1.
- For /24+: offset pattern (.10, .20, .30...).

---

## 5. Link Networking

Link networking connects QEMU VMs through newtlink bridge workers using TCP
sockets. This section covers the port allocation formulas, NIC index resolution,
worker placement algorithm, and the bridge process that ties them together.
For architectural motivation (why userspace bridges instead of kernel networking),
see HLD §4.

### 5.1 Port Allocation Scheme

All TCP ports are deterministic from base values and device/link indices.
Ranges are non-overlapping.

```
LinkPortBase (default 20000):
    Link A/Z ports:   LinkPortBase + i*2, LinkPortBase + i*2 + 1
    Bridge stats:     LinkPortBase - 1, LinkPortBase - 2, ... (one per worker host)
ConsolePortBase (default 30000):
    Serial console:   ConsolePortBase + nodeIndex
SSHPortBase (default 40000):
    SSH forwarding:   SSHPortBase + nodeIndex
```

### 5.2 AllocateLinks

`AllocateLinks()` in `link.go` transforms topology links into resolved
`LinkConfig` entries.

```go
func AllocateLinks(
    links []*spec.TopologyLink,
    nodes map[string]*NodeConfig,
    config *VMLabConfig,
    hostMap map[string]HostMapping,
) ([]*LinkConfig, error)
```

For each topology link:

1. Parse `"device:interface"` endpoints.
2. If a device is in `hostMap` (coalesced host), remap to the parent VM and compute NIC index as `NICBase + ethIdx`.
3. Otherwise, resolve NIC index via `ResolveNICIndex()` (§5.3) using the device's `InterfaceMap`.
4. Assign TCP ports: `APort = LinkPortBase + i*2`, `ZPort = LinkPortBase + i*2 + 1`.

After all links are created:

5. Assign bridge worker hosts via `PlaceWorkers()` (§5.4).
6. Compute bind addresses: `"127.0.0.1"` if VM is on the same host as the worker, `"0.0.0.0"` otherwise.
7. Compute connect addresses: `"127.0.0.1:<port>"` for co-located, `"<workerIP>:<port>"` for cross-host.
8. Append `NICConfig` entries to each node's NICs array.

### 5.3 Interface Map Resolution

`ResolveNICIndex()` in `iface_map.go` converts a SONiC interface name to a
QEMU NIC index. Data NICs start at index 1 (NIC 0 is management — see §3.4).

| Scheme | Formula | Example |
|--------|---------|---------|
| `sequential` | `EthernetN` → `N + 1` | Ethernet0 → 1, Ethernet1 → 2, Ethernet2 → 3 |
| `stride-4` | `EthernetN` → `N/4 + 1` (N must be divisible by 4) | Ethernet0 → 1, Ethernet4 → 2, Ethernet8 → 3 |
| `linux` | `ethN` → `N` | eth1 → 1, eth2 → 2 |
| `custom` | Direct lookup in `VMInterfaceMapCustom` | (arbitrary mapping) |

`sequential` is used by platforms where data ports are Ethernet0, 1, 2, ...
(e.g., CiscoVS, VPP). `stride-4` is the built-in default where ports are
Ethernet0, 4, 8, ... (legacy VS images). `linux` is used by host devices with
`ethN` naming.

### 5.4 Worker Placement

`PlaceWorkers()` assigns a `WorkerHost` for each link's bridge worker.
The result determines which host runs each `newtlink` process (§5.5).

- **Same-host links:** Worker runs on the devices' shared host.
- **Cross-host links:** Greedy assignment to the host with fewer cross-host workers. Alphabetical tie-breaking for determinism.

This gets within 1 of optimal balance for any host pair. For 3+ hosts,
balancing is pairwise — each cross-host link only considers its two endpoint
hosts.

### 5.5 Bridge Config and Process

Bridge workers run as separate `newtlink` processes. Each process reads a
`bridge.json` config and opens TCP listeners for all assigned links.

```go
type BridgeConfig struct {
    Links     []BridgeLink `json:"links"`
    StatsAddr string       `json:"stats_addr,omitempty"`
}

type BridgeLink struct {
    APort int    `json:"a_port"`
    ZPort int    `json:"z_port"`
    ABind string `json:"a_bind"`
    ZBind string `json:"z_bind"`
    A     string `json:"a"` // display label
    Z     string `json:"z"`
}
```

Process lifecycle:
- **Local:** `WriteBridgeConfig()` serializes config → `startBridgeProcess()` spawns `newtlink <configPath>` (detached process group).
- **Remote:** `buildBridgeConfig()` → upload config JSON + `newtlink` binary via SSH → `startBridgeProcessRemote()` starts via `nohup`.
- `RunBridgeFromFile()` is the newtlink entry point: reads config, starts workers, writes PID file, opens Unix + TCP stats listeners, blocks on SIGTERM/SIGINT.

### 5.6 BridgeWorker Runtime and Stats

Each `BridgeWorker` manages one link's TCP bridge.

```go
type BridgeWorker struct {
    Link      *LinkConfig
    aListener net.Listener
    zListener net.Listener
    aToZBytes atomic.Int64
    zToABytes atomic.Int64
    sessions  atomic.Int64
    connected atomic.Bool
}
```

The `run()` loop: accept A connection, accept Z connection, bridge with
bidirectional `io.Copy` using `countingWriter` for byte counting. When either
side disconnects, the loop re-accepts (survives VM restart). The `Bridge`
struct holds all workers and provides `Stop()` and `Stats()`.

Stats queries:
- `QueryBridgeStats(addr)` connects to Unix socket or TCP and decodes `BridgeStats`.
- `QueryAllBridgeStats(labName)` aggregates stats from all bridge processes in a lab (one per unique worker host).
- Legacy fallback: if state has no `Bridges` map but has `BridgePID`, queries the local Unix socket.

```go
type BridgeStats struct {
    Links []LinkStats `json:"links"`
}

type LinkStats struct {
    A, Z      string // endpoint labels
    APort     int
    ZPort     int
    AToZBytes int64
    ZToABytes int64
    Sessions  int64
    Connected bool
}
```

---

## 6. QEMU Management

QEMU is the VM hypervisor. These functions translate `NodeConfig` (§3.3) into
`qemu-system-x86_64` command lines, manage process lifecycle (start, stop,
running checks), and handle overlay disk creation.

### 6.1 QEMUCommand.Build

`QEMUCommand` in `qemu.go` constructs a `qemu-system-x86_64` invocation from
`NodeConfig` (§3.3) and `NICConfig` (§3.3) entries built by `AllocateLinks()`
(§5.2).

```go
type QEMUCommand struct {
    Node     *NodeConfig
    StateDir string // absolute for local, "." for remote
    KVM      bool
}
```

`Build()` generates arguments:

| Argument | Value |
|----------|-------|
| `-m` | `Node.Memory` |
| `-smp` | `Node.CPUs` |
| `-cpu` | `host` (+ CPUFeatures if set) |
| `-enable-kvm` | if `KVM=true` |
| `-drive` | `file=<stateDir>/disks/<name>.qcow2,if=virtio,format=qcow2` |
| `-display none` | headless (not `-nographic` — avoids ttyS1 conflict) |
| `-serial` | `tcp::<ConsolePort>,server,nowait` |
| `-boot c` | boot from disk (not PXE) |
| `-monitor` | `unix:<stateDir>/qemu/<name>.mon,server,nowait` |
| `-pidfile` | `<stateDir>/qemu/<name>.pid` |
| NIC 0 (mgmt) | `-netdev user,id=mgmt,hostfwd=tcp::<SSHPort>-:22` + `-device <NICDriver>,netdev=mgmt,mac=<MAC>,romfile=` |
| NIC 1..N (data) | `-netdev socket,id=<id>,connect=<ConnectAddr>` + `-device <NICDriver>,netdev=<id>,mac=<MAC>,romfile=` |

Data NICs are sorted by `Index` before emitting to ensure kernel `ethN`
assignment matches the NIC index (QEMU enumerates NICs in PCI order).
`romfile=` is empty on all NICs to suppress PXE boot attempts on data ports,
which delays startup and can interfere with VPP.

### 6.2 StartNode / StopNode / IsRunning

```go
func StartNode(node *NodeConfig, stateDir, hostIP string) (int, error)
func StopNode(pid int, hostIP string) error
func IsRunning(pid int, hostIP string) bool
```

Each function dispatches to local or remote based on `hostIP`:
- **Local:** `exec.Command` with `Setpgid: true` for process detachment. Logs to `<stateDir>/logs/<name>.log`. Stop via SIGTERM → 10s → SIGKILL. Running check via `Signal(0)`.
- **Remote:** Builds command with relative paths from `~/.newtlab/labs/<labName>`, runs via `nohup ... & echo $!`. Stop/check via SSH `kill`.

### 6.3 Overlay Disks

`CreateOverlay(baseImage, overlayPath)` runs `qemu-img create -f qcow2 -b <base> -F qcow2 <overlay>`. `CreateOverlayRemote()` runs the same command via SSH with `shellQuote()` for safe path handling.

### 6.4 KVM Detection

`kvmAvailable()` checks if `/dev/kvm` is writable. Local starts use the check;
remote starts assume KVM is available (`KVM: true`).

---

## 7. Boot Sequence

After QEMU launches a VM, the boot sequence brings it from "kernel running" to
"SSH-reachable and patched." This involves serial console bootstrap (the only
way into a VM before SSH exists), SSH key injection, and platform-specific boot
patches. For why deploy-time patching instead of image baking, see HLD §7.

### 7.1 BootstrapNetwork — Switch VMs

`BootstrapNetwork()` in `boot.go` connects to the serial console via TCP and
prepares the VM for SSH access. Uses `VMCredentials` (§2.2) for console login.

```go
func BootstrapNetwork(ctx context.Context, consoleHost string, consolePort int,
    consoleUser, consolePass, sshUser, sshPass string, timeout time.Duration) error
```

Steps:
1. Poll TCP until the console port accepts connections.
2. Wait for the `login:` prompt (VM may still be booting).
3. Log in with `consoleUser`/`consolePass` (the credentials baked into the image).
4. `sudo ip link set eth0 up` + `sudo dhclient eth0` (QEMU user-mode networking requires DHCP).
5. If `sshUser != consoleUser`, create the SSH user with `useradd -m -s /bin/bash -G sudo,docker` and set password via `chpasswd`.
6. Log out.

Timeout and context cancellation are checked at each wait point.

### 7.2 BootstrapHostNetwork — Host VMs

```go
func BootstrapHostNetwork(ctx context.Context, consoleHost string, consolePort int,
    consoleUser, consolePass string, timeout time.Duration) error
```

Simpler than `BootstrapNetwork`: connects to the serial console and waits
for the `login:` prompt. It does **not** log in — Alpine Linux (the host VM
image) auto-starts `dhcpcd` and `sshd` during init. Periodically sends `\r\n`
to trigger the prompt.

### 7.3 WaitForSSH and SSH Key Injection

```go
func WaitForSSH(ctx context.Context, host string, port int, user, pass string,
    timeout time.Duration) error
```

Polls SSH connectivity every 5 seconds: dials, opens a session, runs
`echo ready`, verifies success. Returns when SSH responds or timeout expires.

`GenerateLabSSHKey()` creates an Ed25519 key pair, saves the private key to
`<stateDir>/lab.key` (mode 0600), and returns the public key in
`authorized_keys` format. `injectSSHKeyViaSSH()` appends the public key to
`~/.ssh/authorized_keys` on the target VM.

### 7.4 Boot Patch Framework

Boot patches are declarative JSON files embedded in the `newtlab` binary via
`//go:embed patches`. They customize VMs after boot (port configuration,
CONFIG_DB seeding, daemon restarts). For architectural motivation, see HLD §7.

```go
type BootPatch struct {
    Description  string      `json:"description"`
    PreCommands  []string    `json:"pre_commands,omitempty"`
    DisableFiles []string    `json:"disable_files,omitempty"`
    Files        []FilePatch `json:"files,omitempty"`
    Redis        []RedisPatch `json:"redis,omitempty"`
    PostCommands []string    `json:"post_commands,omitempty"`
}

type FilePatch struct {
    Template string `json:"template"` // Go template filename
    Dest     string `json:"dest"`     // target path (may contain {{.HWSkuDir}})
}

type RedisPatch struct {
    DB       int    `json:"db"`       // Redis database number
    Template string `json:"template"` // Go template → redis-cli commands
}
```

**Resolution order** (`ResolveBootPatches(dataplane, release)`):
1. `patches/<dataplane>/always/*.json` (sorted by filename).
2. `patches/<dataplane>/<release>/*.json` (sorted, if release is set).

**Template variables** (`PatchVars`, built by `buildPatchVars()`):

| Variable | Source |
|----------|--------|
| `NumPorts` | Count of data NICs (Index > 0) |
| `PCIAddrs` | Deterministic PCI addresses: `0000:00:04.0`, `0000:00:05.0`, ... |
| `PortStride` | 1 for sequential, 4 for stride-4 |
| `HWSkuDir` | `/usr/share/sonic/device/x86_64-kvm_x86_64-r0/<HWSKU>` |
| `PortSpeed` | Parsed from `platform.DefaultSpeed` (default 25000) |
| `Platform` | Platform name from NodeConfig |
| `Dataplane` | Dataplane string from PlatformSpec |
| `Release` | Release string from PlatformSpec |

`QEMUPCIAddrs(dataNICs)` generates PCI addresses: QEMU assigns slot 3 to the
management NIC, data NICs start at slot 4 (`0000:00:04.0`, `0000:00:05.0`,
...). Used by CiscoVS port config patches.

Template functions: `mul(a, b int)`, `add(a, b int)`.

**Example: VPP port config patch** (`patches/vpp/always/02-port-config.json`):

```json
{
    "description": "Fix empty port_config.ini from broken factory hook",
    "pre_commands": [
        "while pgrep -f config-setup >/dev/null 2>&1; do sleep 2; done"
    ],
    "disable_files": [
        "/etc/config-setup/factory-default-hooks.d/10-01-vpp-cfg-init"
    ],
    "files": [
        { "template": "port_config.ini.tmpl", "dest": "{{.HWSkuDir}}/port_config.ini" }
    ],
    "redis": [
        { "db": 4, "template": "port_entries.tmpl" }
    ],
    "post_commands": [
        "sudo config save -y",
        "sudo systemctl restart syncd"
    ]
}
```

Template (`port_config.ini.tmpl`) using `PCIAddrs` and `PortStride`:

```
# name  lanes  alias  index  speed
{{- range $i, $_ := .PCIAddrs}}
Ethernet{{mul $i 4}}  {{mul $i 4}}  Ethernet{{mul $i 4}}  {{$i}}  {{$.PortSpeed}}
{{- end}}
```

**Application** (`ApplyBootPatches()`): SSH into the VM, then for each patch
in order: (1) execute pre_commands, (2) rename disable_files to `.disabled`,
(3) render and upload file templates via SSH stdin pipe, (4) render redis
templates into `redis-cli -n <DB>` commands and execute, (5) execute
post_commands.

---

## 8. Profile Patching

Profile patching bridges newtlab and newtron: newtlab writes runtime values
(`ssh_port`, `mgmt_ip`, `mac`) into `DeviceProfile` (§2.3) JSON files so that
newtron's SSH tunnel (see [Device LLD §1](../newtron/device-lld.md)) can
discover how to connect to each device.

### 8.1 PatchProfiles

`PatchProfiles(lab)` updates device profile JSON files with newtlab runtime
values after successful VM deployment. Called during Deploy phase 7.

For each device (skipping `host-vm` synthetic nodes):
1. Read `profiles/<name>.json` into `spec.DeviceProfile`.
2. Save original `mgmt_ip` in state for restore.
3. Set `mgmt_ip` to `127.0.0.1` (local) or the host's IP (remote).
4. Set `ssh_port`, `console_port`, `mac` (deterministic from `GenerateMAC(name, 0)`).
5. Set `ssh_user`/`ssh_pass` only if the profile didn't already have them.
6. Write back as indented JSON.

For virtual hosts: patches with the parent VM's SSH port and console port so
newtron can reach them.

### 8.2 RestoreProfiles

`RestoreProfiles(lab)` reverses patching during `Destroy()`:
1. Restore original `mgmt_ip` from saved state.
2. Zero out `ssh_port`, `console_port`, `mac`.
3. `ssh_user`/`ssh_pass` are **not** removed — they may have been user-set before deploy.

---

## 9. Server Pool and Port Probing

Multi-host deployment support. `PlaceNodes()` runs during `NewLab()` (§4.1
step 8) to assign devices to servers. `ProbeAllPorts()` runs during Deploy
Phase 1 (§4.2) to detect conflicts before starting any processes.

### 9.1 PlaceNodes

`PlaceNodes()` in `placement.go` assigns unpinned nodes to servers using
spread placement (minimize maximum load). If `Servers` is empty, it's a no-op
(single-host mode).

Algorithm (two phases):
1. **Validate pinned nodes:** Nodes with `Host != ""` are counted against their server's capacity. Error if the server is unknown or over `MaxNodes`.
2. **Place unpinned nodes:** Sort servers by `(count asc, name asc)`. Assign each unpinned node to the least-loaded server with remaining capacity.

Both phases iterate node names in sorted order for deterministic placement.

Internally uses `serverLoad{server *spec.ServerConfig, count int}` to track
per-server load. Reduces to round-robin when all servers are equal capacity
and no nodes are pinned.

### 9.2 Port Conflict Detection

```go
type PortAllocation struct {
    Host    string // host name ("" = local)
    HostIP  string // resolved IP ("" = 127.0.0.1 for local)
    Port    int
    Purpose string // e.g. "spine1 SSH", "link spine1:Ethernet0 A-side"
}
```

`CollectAllPorts(lab)` gathers all TCP port allocations into `[]PortAllocation`:
- SSH and console ports per node.
- Link A/Z ports per link.
- Bridge stats ports per worker host.

`ProbeAllPorts(allocations)` checks every port is free:
- **Local:** `net.Listen("tcp", ":<port>")` — fails if port is in use.
- **Remote:** Single SSH per host running `ss -tlnH '( sport = :P1 or sport = :P2 ... )'`. Parses output to identify conflicts.

Returns a multi-error listing all conflicts with purpose annotations.

---

## 10. State Persistence

### 10.1 State Types

Used by `Deploy`, `Destroy`, `Status`, `Stop`, `Start`, and the CLI status
display. Defined in `state.go`.

```go
type LabState struct {
    Name       string
    Created    time.Time
    SpecDir    string
    SSHKeyPath string                  // lab Ed25519 private key path
    Nodes      map[string]*NodeState
    Links      []*LinkState
    BridgePID  int                     // deprecated: legacy single-bridge
    Bridges    map[string]*BridgeState // host → bridge info
}

type NodeState struct {
    PID            int
    Status         string // "running", "stopped", "error"
    Phase          string // deploy phase: "booting", "bootstrapping", "patching"
    DeviceType     string // "", "host", "host-vm"
    Image          string
    SSHPort        int
    ConsolePort    int
    OriginalMgmtIP string
    Host           string // host name
    HostIP         string // resolved IP
    SSHUser        string
    VMName         string // virtual hosts: parent VM
    Namespace      string // virtual hosts: netns name
}

type BridgeState struct {
    PID       int
    HostIP    string
    StatsAddr string // "host:port" for TCP stats
}

type LinkState struct {
    A, Z       string // "device:interface"
    APort      int
    ZPort      int
    WorkerHost string
}
```

### 10.2 State Directory Layout

```
~/.newtlab/
└── labs/
    └── <lab-name>/
        ├── state.json          # LabState
        ├── lab.key             # Ed25519 private key
        ├── bridge.json         # BridgeConfig (local bridge)
        ├── bridge.pid          # newtlink PID
        ├── bridge.sock         # Unix socket for stats queries
        ├── disks/
        │   ├── spine1.qcow2   # overlay disks
        │   └── leaf1.qcow2
        ├── qemu/
        │   ├── spine1.mon     # QEMU monitor sockets
        │   ├── spine1.pid
        │   └── ...
        └── logs/
            ├── bridge.log
            ├── spine1.log     # QEMU stdout/stderr
            └── ...
```

Remote hosts mirror this structure under `~/.newtlab/labs/<lab-name>/`.

### 10.3 Functions

State directory management used by Lab lifecycle methods (§4.2–§4.5).

| Function | Description |
|----------|-------------|
| `LabDir(name)` | Returns `~/.newtlab/labs/<name>` |
| `SaveState(state)` | JSON marshal → `state.json` (creates dir if needed) |
| `LoadState(name)` | Read + unmarshal `state.json` |
| `RemoveState(name)` | `os.RemoveAll` the lab directory |
| `ListLabs()` | Reads `~/.newtlab/labs/` directory entries |

---

## 11. Remote Operations

These helpers support multi-host deployment. They are used by Deploy (§4.2)
for cross-host bridge workers (§5.4), remote QEMU management (§6.2), boot
patches (§7.4), and newtlink binary distribution.

### 11.1 SSH and SCP Helpers

`sshCommand(hostIP, remoteCmd)` and `scpCommand(hostIP, localPath, remotePath)`
in `remote.go` build `exec.Cmd` with standard options
(`StrictHostKeyChecking=no`, `ConnectTimeout=10`).

### 11.2 newtlink Upload

`uploadNewtlink(hostIP)` manages the bridge binary on remote hosts:

1. Check if remote `~/.newtlab/bin/newtlink --version` matches local version.
2. If mismatch: detect remote architecture via `uname -s -m`, find cross-compiled binary (`newtlink-<goos>-<goarch>`), SCP upload, `chmod +x`.

Binary search order: `$NEWTLAB_BIN_DIR` → executable directory → `~/.newtlab/bin/`.

### 11.3 Shell Quoting

`shellQuote(path)` preserves tilde expansion (`~/foo` → `~/'foo'`).
`singleQuote(s)` wraps in single quotes with internal quote escaping.
`quoteArgs(args)` applies `singleQuote` to each element.

### 11.4 Home Directory Caching

`getHomeDir()` wraps `os.UserHomeDir()` with `sync.Once` caching.
`expandHome(path)` replaces leading `~/` with the home directory.
`unexpandHome(path)` replaces leading `$HOME/` with `~/` for remote commands.

---

## 12. CLI Implementation

The `newtlab` CLI in `cmd/newtlab/` wraps the `Lab` lifecycle methods (§4) with
topology resolution, progress display, and SSH/console access. Each subcommand
is one file.

### 12.1 Command Structure

The `newtlab` CLI in `cmd/newtlab/` uses cobra. Each command file is one
subcommand.

| Command | File | Args | Description |
|---------|------|------|-------------|
| `list` | `main.go` | none | Show topologies and deployment status |
| `deploy` | `cmd_deploy.go` | `[topology]` | Deploy VMs, optional `--provision`, `--force`, `--host`, `--parallel` |
| `destroy` | `cmd_destroy.go` | `[topology]` | Kill VMs, remove overlays, clean state |
| `status` | `cmd_status.go` | `[topology]` | Show node/link status with live bridge stats |
| `ssh` | `cmd_ssh.go` | `<node>` | SSH to a VM (or `ip netns exec` for virtual hosts) |
| `console` | `cmd_console.go` | `<node>` | Serial console via socat/telnet |
| `stop` | `cmd_stop.go` | `<node>` | Stop a single VM |
| `start` | `cmd_stop.go` | `<node>` | Start a stopped VM |
| `provision` | `cmd_provision.go` | `[topology]` | Run newtron provisioning, optional `--device`, `--parallel` |
| `version` | `main.go` | none | Print version info |

Global flags: `-S <dir>` (spec directory override), `-v` (verbose).

### 12.2 Topology Resolution

`resolveTarget(args)` resolves both lab name and spec directory from three
sources in priority order:

1. `-S` flag → use as spec dir, derive name via `NewLab()`.
2. Positional argument → check deployed labs by name, then try as topology name under `topologiesBaseDir()`.
3. Auto-detect → if exactly one lab is deployed, use it.

`topologiesBaseDir()` resolves from: `$NEWTRUN_TOPOLOGIES` → `settings.TopologiesDir` → `"newtrun/topologies"`.

### 12.3 Node Search

`findNodeState(nodeName)` in `cmd_ssh.go` searches all deployed labs for a
node by name. Returns the lab state, lab name, and error. Allows `ssh` and
`stop`/`start` commands to work without specifying a topology when the node
name is unambiguous.

### 12.4 Status Display

`showLabDetail(labName)` prints node and link tables.

Node table columns: `NODE`, `TYPE`, `STATUS`, `IMAGE`, `SSH`, `CONSOLE`, `PID`.
Adds `HOST` column when any node is on a remote host.

`TYPE` values:
- `switch` — default.
- `host-vm` — coalesced host VM.
- `vhost:<vmName>/<namespace>` — virtual host within a coalesced VM.

Link table: calls `QueryAllBridgeStats()` to enrich links with live stats
(connected status, byte counts, session counts). Includes `HOST` column when
any link has a non-local worker.

### 12.5 SSH and Console

`ssh` command: resolves the node via `findNodeState()`. For virtual hosts
(`VMName` set), executes `ssh ... ip netns exec <namespace> bash`. For regular
nodes, executes `ssh -p <port> <user>@<host>`. Uses the lab SSH key
(`-i <keyPath>`) if available in state.

`console` command: connects to `<host>:<consolePort>` via `socat -,rawer TCP:...`
or falls back to `telnet`. Both use `syscall.Exec` to replace the newtlab
process.

---

## 13. Worked Example: Deploy 2node

Traces `newtlab deploy 2node --provision` through every subsystem at the
function-call level. The 2node topology has two switches (switch1, switch2),
six virtual hosts (host1–host6), and nine links (three switch-to-switch, six
switch-to-host) on `sonic-ciscovs` platform. The trace focuses on the switch
deploy path — hosts follow the coalescing path (§3.6, §4.6).

### Phase 0 — CLI dispatch

`cmd_deploy.go` → `resolveSpecDir(["2node"])` → `resolveTopologyDir("2node")`
→ `"newtrun/topologies/2node/specs"`.

### Phase 1 — NewLab

`NewLab("newtrun/topologies/2node/specs")`:

1. `absDir` = `/home/aldrin/src/newtron/newtrun/topologies/2node/specs`, name = `"2node"`.
2. Load `topology.json` — 8 devices (2 switches, 6 hosts), 9 links derived from `interface.link` fields.
3. Load `platforms.json` — platform `sonic-ciscovs`: `sequential` interface map, `e1000` NIC driver, 8192 MB memory, 6 CPUs, 600s boot timeout. Platform `alpine-host`: `linux` interface map, `device_type: "host"`.
4. Load profiles for all 8 devices.
5. Resolve `VMLabConfig` from `topology.NewtLab`: link=10000, console=12000, ssh=13000.
6. Resolve nodes (sorted: host1..host6, switch1, switch2):
   - `ResolveNodeConfig("switch1", profile, platform)` → Memory=8192, CPUs=6, NICDriver=`e1000`, InterfaceMap=`sequential`.
   - Hosts resolve with `DeviceType="host"`, InterfaceMap=`linux`.
   - SSH and console ports allocated sequentially by sorted device index.
7. `coalesceHostVMs()` (§3.6): 6 hosts → 1 synthetic `hostvm-0` (DeviceType=`"host-vm"`). NIC base indices computed per host. Individual host NodeConfigs removed.
8. No servers → skip placement.
9. `AllocateLinks()`:
   - 3 switch-to-switch links: `ResolveNICIndex("sequential", "Ethernet0")` → NIC 1. Ports assigned from `LinkPortBase + i*2`.
   - 6 switch-to-host links: host endpoints remapped via `hostMap` to `hostvm-0` with computed NIC indices.
   - `PlaceWorkers()`: all devices local → all workers local (`WorkerHost=""`).
   - NIC entries appended to each node.

### Phase 2 — Deploy

`Lab.Deploy(ctx)`:

1. **Pre-checks:** `CollectAllPorts()` → 25 allocations (3 SSH + 3 console + 18 link + 1 bridge stats). `ProbeAllPorts()` checks all free.
2. **State init:** `LabState{Name: "2node", SpecDir: ..., Nodes: {}}`, 9 link state entries. `SaveState()`.
3. **Overlay disks:** `CreateOverlay(platform.VMImage, ~/.newtlab/labs/2node/disks/switch1.qcow2)` for switch1, switch2, and hostvm-0 (3 VMs).
4. **Bridges:** `WriteBridgeConfig()` → `bridge.json` with 9 links, stats addr `127.0.0.1:9999`. `startBridgeProcess()` → `newtlink ~/.newtlab/labs/2node/bridge.json`. Wait for ports 10000–10017.
5. **Boot VMs:** `StartNode(switch1, stateDir, "")` → `QEMUCommand.Build()` → `qemu-system-x86_64 -m 8192 -smp 6 -cpu host -enable-kvm -drive file=.../switch1.qcow2,... -serial tcp::12006,server,nowait -netdev user,id=mgmt,hostfwd=tcp::13006-:22 -device e1000,... -netdev socket,id=eth1,connect=127.0.0.1:10000 ...`. Same for switch2 and hostvm-0.
6. **Bootstrap:** Switches: `BootstrapNetwork(ctx, "127.0.0.1", 12006, "aldrin", "...", "admin", "...", 600s)` — serial login, eth0 DHCP, create admin user. Host VM: `BootstrapHostNetwork(ctx, "127.0.0.1", 12000, ...)` — wait for login prompt only (Alpine auto-starts DHCP+SSH). Then `WaitForSSH()` for all 3 VMs. Inject lab SSH key.
7. **Patches:** `ResolveBootPatches("ciscovs", "")` → `patches/ciscovs/always/*.json` for switches. `buildPatchVars()` → `{NumPorts: 6, PCIAddrs: [...], PortStride: 1, ...}`. `ApplyBootPatches()` via SSH. Host VMs have no patches (no dataplane set).
8. **PatchProfiles:** switch1.json gets `mgmt_ip: "127.0.0.1"`, `ssh_port: 13006`, `console_port: 12006`, `mac: "52:54:00:..."`. Virtual host profiles patched with parent VM's ports.
9. **Host namespaces:** `provisionHostNamespaces()` SSHs into hostvm-0, creates 6 network namespaces (host1..host6), moves data NICs, assigns IPs via auto-derivation (§4.6).

### Phase 3 — Provision

`Lab.Provision(ctx, parallel)` runs newtron provisioning with semaphore-bounded
concurrency. Shells out to `newtron provision -S <specDir> -D <name> -x`
rather than importing `pkg/newtron/network` — this keeps newtlab and newtron
loosely coupled (newtlab only needs the `newtron` binary on PATH).

- Goroutine per device, bounded by `parallel` semaphore.
- For each switch (host devices skipped): `exec.CommandContext(ctx, "newtron", "provision", "-S", specDir, "-D", "switch1", "-x")`.
- Collects errors from all goroutines, returns `errors.Join`.
- Post-provision: 5s delay → `refreshBGP()` → SSH to each switch → `vtysh -c 'clear bgp * soft'`.

---

## 14. Tests

Unit tests in `pkg/newtlab/` validate each subsystem in isolation. The primary
test file `newtlab_test.go` covers the core types and algorithms; additional
`*_test.go` files test specific subsystems.

| Test group | What it validates |
|-----------|-------------------|
| Interface map | `ResolveNICIndex` for stride-4, sequential, custom, linux. Edge cases (non-divisible index, missing key). |
| Node resolution | Profile > platform > default cascade. Missing image error. Management NIC always created. |
| Link allocation | Port sequence formula. NIC index assignment. ConnectAddr computation. |
| State persistence | Save/load round-trip. Not-found error. ListLabs. RemoveState cleanup. |
| FilterHost | Remote node removal. Local host clearing. Link pruning. Unhosted node retention. |
| Worker placement | Same-host → shared. Cross-host → balanced with alphabetical tie-breaking. |
| Bridge workers | Bidirectional data forwarding. Byte counting. Session tracking. Listen failure. |
| Bridge stats | Local stats (in-memory). TCP stats (remote simulation). Multi-bridge aggregation. Legacy Unix socket fallback. |
| Disk helpers | `expandHome` / `unexpandHome` round-trip. |
| QEMU | KVM flag inclusion. Relative path handling for remote. |

Additional test files: `patch_test.go` (boot patch resolution and template
rendering), `placement_test.go` (server pool placement), `probe_test.go`
(port conflict detection), `remote_test.go` (uname parsing, newtlink binary
naming), `cmd/newtlab/main_test.go` (humanBytes, topoCounts,
resolveTopologyDir).

---

## 15. Error Conditions

All errors use `fmt.Errorf` with `%w` wrapping. The `"newtlab:"` prefix
identifies the package origin.

| Condition | Error | Recovery |
|-----------|-------|----------|
| No `vm_image` resolved | `"no vm_image for device %s (check platform or profile)"` | Set `vm_image` in platform or profile |
| Base image not found | `"vm_image not found: %s"` | Download or fix path |
| KVM not available | Warning logged, falls back to TCG | Install KVM or accept slower emulation |
| Port conflict | `"port conflicts:\n  <purpose>: port N in use"` | Change port base or stop conflicting process |
| Pinned to unknown server | `"node X pinned to unknown server Y"` | Fix `vm_host` or add server to `servers` list |
| Server over capacity | `"no server capacity for node X"` | Increase `max_nodes` or add servers |
| SSH boot timeout | `"SSH timeout after %s for %s:%d"` | Increase `vm_boot_timeout` or check image |
| QEMU process crash | `"QEMU exited with code %d (see %s)"` | Check `logs/<node>.log` |
| State not found | `"lab %s not found (no state.json)"` | Run deploy first |
| Profile read error | `"reading profile %s: %w"` | Check file permissions |
| Lab already deployed | `"lab %s already deployed (created %s); use --force"` | Destroy first or use `--force` |

---

## 16. Cross-References

| This LLD section | Related document | Relationship |
|-----------------|-----------------|--------------|
| §2.1 `PlatformSpec` VM fields | [newtron LLD](../newtron/lld.md) §3.1 | newtlab extends PlatformSpec |
| §2.3 `DeviceProfile` newtlab fields | [newtron LLD](../newtron/lld.md) §3.1 | newtlab reads/writes profile fields |
| §2.4 `TopologySpecFile` | [newtron LLD](../newtron/lld.md) §3.1 | newtlab reads topology |
| §8.1 `PatchProfiles` | [Device LLD](../newtron/device-lld.md) §1 | Patched `SSHPort`/`MgmtIP` read by SSH tunnel |
| §8.1 `PatchProfiles` | [Device LLD](../newtron/device-lld.md) §5.1 | `SSHUser`/`SSHPass`/`SSHPort` used by `Device.Connect()` |
| §4.2 Deploy, §4.3 Destroy | [newtrun LLD](../newtrun/lld.md) §6.1–§6.2 | newtrun wraps `NewLab()` + `Deploy()` / `Destroy()` |
| §2.1 `PlatformSpec.Dataplane` | [newtrun LLD](../newtrun/lld.md) §6.3 | Platform capability check reads dataplane field |
| §8.1 Profile patching | [newtrun LLD](../newtrun/lld.md) §4.5 | Device connection relies on patched profiles |
