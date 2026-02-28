# newtlab Low-Level Design (LLD)

newtlab realizes network topologies as connected QEMU virtual machines for SONiC lab environments. This document covers `pkg/newtlab/` — the VM lifecycle, networking, and port management layer.

For the architectural principles behind newtron, newtlab, and newtrun, see [Design Principles](../DESIGN_PRINCIPLES.md). For the high-level architecture, see [newtlab HLD](hld.md). For the device connection layer used after VMs boot, see [Device Layer LLD](../newtron/device-lld.md).

---

## 1. Spec Type Extensions

Changes to existing newtron types in `pkg/newtron/spec/types.go`.

### 1.1 PlatformSpec — VM Fields

```go
// PlatformSpec defines a SONiC platform.
type PlatformSpec struct {
    // existing fields
    HWSKU        string   `json:"hwsku"`
    Description  string   `json:"description,omitempty"`
    PortCount    int      `json:"port_count"`
    DefaultSpeed string   `json:"default_speed"`
    Breakouts    []string `json:"breakouts,omitempty"`

    // newtlab VM fields
    VMImage        string         `json:"vm_image,omitempty"`
    VMMemory       int            `json:"vm_memory,omitempty"`
    VMCPUs         int            `json:"vm_cpus,omitempty"`
    VMNICDriver    string         `json:"vm_nic_driver,omitempty"`
    VMInterfaceMap       string         `json:"vm_interface_map,omitempty"`
    VMInterfaceMapCustom map[string]int `json:"vm_interface_map_custom,omitempty"` // SONiC name → QEMU NIC index (for "custom" map type)
    VMCPUFeatures  string         `json:"vm_cpu_features,omitempty"`
    VMCredentials  *VMCredentials `json:"vm_credentials,omitempty"`
    VMBootTimeout  int            `json:"vm_boot_timeout,omitempty"`
    Dataplane      string         `json:"dataplane,omitempty"`        // "vpp", "barefoot", "" (none/vs)
    VMImageRelease string         `json:"vm_image_release,omitempty"` // e.g. "202405", "202311" — selects release-specific boot patches
}

// VMCredentials holds default SSH credentials for a VM platform.
type VMCredentials struct {
    User string `json:"user"`
    Pass string `json:"pass"`
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `vm_image` | string | (required) | Path to QCOW2 base image |
| `vm_memory` | int | 4096 | RAM in MB |
| `vm_cpus` | int | 2 | vCPU count |
| `vm_nic_driver` | string | `"e1000"` | QEMU NIC model |
| `vm_interface_map` | string | `"stride-4"` | NIC-to-interface mapping scheme |
| `vm_interface_map_custom` | map[string]int | nil | Custom SONiC name→NIC index map (for `"custom"` scheme) |
| `vm_cpu_features` | string | `""` | QEMU `-cpu host,<features>` suffix |
| `vm_credentials` | *VMCredentials | nil | Default SSH login |
| `vm_boot_timeout` | int | 180 | Seconds to wait for SSH |
| `dataplane` | string | `""` | Dataplane type: `"vpp"`, `"barefoot"`, `""` (none/vs) |
| `vm_image_release` | string | `""` | Image release identifier for release-specific boot patches |

### 1.2 DeviceProfile — newtlab Fields

```go
type DeviceProfile struct {
    // ... existing fields ...

    // newtlab per-device overrides (read by newtlab)
    SSHPort     int    `json:"ssh_port,omitempty"`
    ConsolePort int    `json:"console_port,omitempty"`
    VMMemory    int    `json:"vm_memory,omitempty"`
    VMCPUs      int    `json:"vm_cpus,omitempty"`
    VMImage     string `json:"vm_image,omitempty"`
    VMHost      string `json:"vm_host,omitempty"`
}
```

| Field | Read/Write | Description |
|-------|------------|-------------|
| `ssh_port` | Write (newtlab) / Read (newtron) | Forwarded SSH port on host |
| `console_port` | Write (newtlab) | Serial console port on host |
| `vm_memory` | Read (newtlab) | Override platform memory |
| `vm_cpus` | Read (newtlab) | Override platform CPU count |
| `vm_image` | Read (newtlab) | Override platform disk image |
| `vm_host` | Read (newtlab) | Target host for multi-host deployment |

### 1.3 TopologySpecFile — newtlab Section

```go
type TopologySpecFile struct {
    Version     string                     `json:"version"`
    Description string                     `json:"description,omitempty"`
    Devices     map[string]*TopologyDevice `json:"devices"`
    Links       []*TopologyLink            `json:"links,omitempty"`
    NewtLab       *NewtLabConfig               `json:"newtlab,omitempty"`
}
```

### 1.4 NewtLabConfig

```go
// ServerConfig defines a server in the newtlab server pool.
type ServerConfig struct {
    Name     string `json:"name"`
    Address  string `json:"address"`
    MaxNodes int    `json:"max_nodes,omitempty"` // 0 = unlimited
}

// NewtLabConfig holds newtlab orchestration settings from topology.json.
type NewtLabConfig struct {
    LinkPortBase    int               `json:"link_port_base,omitempty"`
    ConsolePortBase int               `json:"console_port_base,omitempty"`
    SSHPortBase     int               `json:"ssh_port_base,omitempty"`
    Hosts           map[string]string `json:"hosts,omitempty"`   // legacy: kept for backward compat
    Servers         []*ServerConfig   `json:"servers,omitempty"` // server pool for auto-placement
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Starting TCP port for socket links |
| `console_port_base` | 30000 | Starting port for serial consoles |
| `ssh_port_base` | 40000 | Starting port for SSH forwarding |
| `servers` | (none) | Server pool for auto-placement (see §7.9) |
| `hosts` | (none) | Legacy host name → IP mapping (use `servers` instead) |

**Server pool (§7.9):** When `servers` is present, the `Hosts` map is
auto-derived from it (`server.Name` → `server.Address`). Nodes are
auto-placed across servers by `PlaceNodes` (see §7.9). Nodes pinned via
`DeviceProfile.VMHost` are validated against the server list.

**Legacy multi-host:** When only `hosts` is present (no `servers`), behavior
is unchanged — `DeviceProfile.VMHost` assigns VMs manually.

Remote QEMU processes are launched via SSH
(`StartNodeRemote`/`StopNodeRemote`). newtlink bridge workers handle
cross-host links by binding the remote-side port on `0.0.0.0` so the remote
VM can connect across the network (see HLD §5.3).

---

## 2. SSH Port Support (newtron change)

### 2.1 sonic/types.go Change

```go
// NewSSHTunnel dials SSH on host:<port> and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port == 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error)
```

Implementation change in `pkg/newtron/device/sonic/types.go`:

```go
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
    if port == 0 {
        port = 22
    }

    config := &ssh.ClientConfig{
        User: user,
        Auth: []ssh.AuthMethod{ssh.Password(pass)},
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
    }

    sshClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)
    // ... rest unchanged ...
}
```

### 2.2 device.go Call Site Update

```go
// In Device.Connect():
tun, err := NewSSHTunnel(
    d.Profile.MgmtIP,
    d.Profile.SSHUser,
    d.Profile.SSHPass,
    d.Profile.SSHPort,  // new parameter — 0 means default port 22
)
```

`ResolvedProfile` gets one new field:

```go
type ResolvedProfile struct {
    // ... existing fields ...
    SSHPort int  // From DeviceProfile.SSHPort (0 = default 22)
}
```

Backward compatible: existing profiles without `ssh_port` resolve to 0, which
defaults to port 22 in `NewSSHTunnel`.

---

## 3. Package Structure

```
pkg/newtlab/
├── newtlab.go        # Lab type, Deploy, Destroy, Status
├── node.go           # NodeConfig, resolved VM settings
├── qemu.go           # QEMU command builder, process management
├── link.go           # Link wiring, port allocation, bridge workers
├── bridge.go         # Bridge config, process lifecycle, stats queries
├── placement.go      # Server pool auto-placement (PlaceNodes)
├── probe.go          # Comprehensive port conflict detection (CollectAllPorts, ProbeAllPorts)
├── iface_map.go      # Interface mapping (sequential, stride-4, custom)
├── disk.go           # COW overlay disk creation
├── boot.go           # SSH boot wait, serial bootstrap
├── patch.go          # Boot patch engine (ApplyBootPatches, patch descriptors)
├── profile.go        # Profile patching (write ssh_port, console_port, mgmt_ip)
├── remote.go         # Remote host operations via SSH
├── shell.go          # Shell command execution helpers
├── state.go          # state.json persistence
├── newtlab_test.go   # Unit tests
└── patches/          # Platform boot patches (//go:embed)
    ├── vpp/
    │   ├── always/
    │   │   ├── 01-disable-factory-hook.json
    │   │   └── 02-port-config.json
    │   └── 202405/
    │       └── 01-specific-fix.json
    └── ciscovs/
        └── always/
            ├── 00-frrcfgd-mode.json         # Enable unified FRR mode + restart bgp
            ├── 01-frrcfgd-vni-bootstrap.json # L3VNI poll thread for frrcfgd
            └── 02-wait-swss.json             # Wait for SwSS container readiness

cmd/newtlab/
├── main.go              # Entry point, root command
├── cmd_deploy.go        # deploy subcommand
├── cmd_destroy.go       # destroy subcommand
├── cmd_status.go        # status subcommand (link stats shown by default)
├── cmd_ssh.go           # ssh subcommand
├── cmd_console.go       # console subcommand
├── cmd_stop.go          # stop/start subcommands
├── cmd_provision.go     # provision subcommand (calls newtron)
└── exec.go              # External command execution helpers
```

---

## 4. Core Types (`pkg/newtlab/`)

### 4.1 Lab — Top-Level Orchestrator (`newtlab.go`)

```go
// Lab is the top-level newtlab orchestrator. It reads newtron spec files,
// resolves VM configuration, and manages QEMU processes.
type Lab struct {
    Name         string                         // lab name (from spec dir basename)
    SpecDir      string                         // path to spec directory
    StateDir     string                         // ~/.newtlab/labs/<name>/
    Topology     *spec.TopologySpecFile         // parsed topology.json
    Platform     *spec.PlatformSpecFile         // parsed platforms.json
    Profiles     map[string]*spec.DeviceProfile // per-device profiles
    Config       *VMLabConfig                   // from topology.json newtlab section
    Nodes        map[string]*NodeConfig         // resolved VM configs (keyed by device name)
    HostVMs      []*HostVMGroup                 // coalesced host VMs (virtual hosts)
    Links        []*LinkConfig                  // resolved link configs
    State        *LabState                      // runtime state (PIDs, ports, status)
    Force        bool                           // --force flag: destroy existing before deploy
    DeviceFilter []string                       // if non-empty, only provision these devices
    OnProgress   func(phase, detail string)    // optional callback for deploy/destroy progress
}

// VMLabConfig mirrors spec.NewtLabConfig with resolved defaults.
type VMLabConfig struct {
    LinkPortBase    int                  // default: 20000
    ConsolePortBase int                  // default: 30000
    SSHPortBase     int                  // default: 40000
    Hosts           map[string]string    // host name → IP
    Servers         []*spec.ServerConfig // server pool (nil = single-host mode)
}
```

**Key methods:**

```go
// NewLab loads specs from specDir and returns a configured Lab.
// Initialization:
//   1. Set Name from filepath.Base(specDir)
//   2. Set StateDir to ~/.newtlab/labs/<name>/
//   3. Load topology.json, platforms.json, profiles/*.json
//   4. Resolve NewtLabConfig with defaults (link_port_base=20000, etc.)
//      When servers is present, derive Hosts map from server list.
//   5. Resolve NodeConfig for each device (profile > platform > defaults)
//   6. Auto-place nodes across server pool if configured (PlaceNodes, §7.9)
//   7. Allocate ports (SSH, console per node) and links
// After NewLab, l.Nodes and l.Links are populated. Deploy() uses them
// to build QEMU commands and start processes.
func NewLab(specDir string) (*Lab, error)

// Deploy creates overlay disks, starts QEMU processes, waits for SSH,
// and patches profiles. Full deployment flow (see §12).
// Reads l.Force to handle stale state.
func (l *Lab) Deploy(ctx context.Context) error

// Destroy kills QEMU processes, removes overlays, cleans state,
// and restores profiles. Full teardown flow (see §13).
// Can be called with only Name populated (loads SpecDir from state.json).
func (l *Lab) Destroy(ctx context.Context) error

// Status returns the current state by loading state.json, then live-checking
// each PID via IsRunning() to update node status. Returns fresh LabState.
func (l *Lab) Status() (*LabState, error)

// FilterHost removes nodes not assigned to the given host name.
// Nodes without a vm_host profile field are retained (single-host mode).
// Also removes links that reference filtered-out nodes.
func (l *Lab) FilterHost(host string)

// Stop stops a single node by PID (SIGTERM then SIGKILL after 10s).
// Updates state.json to mark node as "stopped".
func (l *Lab) Stop(ctx context.Context, nodeName string) error

// Start restarts a stopped node by rebuilding the QEMU command and
// launching the process. Waits for SSH readiness. Updates state.json
// with new PID.
func (l *Lab) Start(ctx context.Context, nodeName string) error

// StopByName loads a lab by name and stops the specified node.
// Convenience function for CLI usage.
func StopByName(ctx context.Context, labName, nodeName string) error

// StartByName loads a lab by name and starts the specified node.
// Convenience function for CLI usage.
func StartByName(ctx context.Context, labName, nodeName string) error
```

### 4.2 NodeConfig — Resolved VM Configuration (`node.go`)

```go
// NodeConfig holds the fully resolved VM configuration for a single device.
// Values are resolved from profile > platform > built-in defaults.
type NodeConfig struct {
    Name          string
    Platform      string
    Image         string     // resolved: profile > platform > error
    Memory        int        // resolved: profile > platform > 4096
    CPUs          int        // resolved: profile > platform > 2
    NICDriver     string     // resolved: platform > "e1000"
    InterfaceMap  string     // resolved: platform > "stride-4"
    CPUFeatures   string     // resolved: platform > ""
    SSHUser       string     // resolved: profile ssh_user > platform credentials
    SSHPass       string     // resolved: profile ssh_pass > platform credentials
    BootTimeout   int        // resolved: platform > 180
    Host          string     // from profile vm_host
    SSHPort       int        // allocated
    ConsolePort   int        // allocated
    NICs          []NICConfig // ordered: NIC0=mgmt, NIC1..N=data
    DeviceType    string     // "switch" or "host", from platform
    ConsoleUser   string     // resolved: platform vm_credentials user
    ConsolePass   string     // resolved: platform vm_credentials pass
}
```

#### 4.2.1 Resolution Function

```go
// ResolveNodeConfig builds a NodeConfig for a device by merging
// profile overrides with platform defaults and built-in fallbacks.
// Returns error if no vm_image can be resolved.
func ResolveNodeConfig(
    name string,
    profile *spec.DeviceProfile,
    platform *spec.PlatformSpec,
) (*NodeConfig, error)
```

**Resolution order (first non-empty wins):**

| Field | Profile | Platform | Default |
|-------|---------|----------|---------|
| Image | `vm_image` | `vm_image` | error |
| Memory | `vm_memory` | `vm_memory` | 4096 |
| CPUs | `vm_cpus` | `vm_cpus` | 2 |
| NICDriver | | `vm_nic_driver` | `"e1000"` |
| InterfaceMap | | `vm_interface_map` | `"stride-4"` |
| CPUFeatures | | `vm_cpu_features` | `""` |
| SSHUser | `ssh_user` | `vm_credentials.user` | |
| SSHPass | `ssh_pass` | `vm_credentials.pass` | |
| BootTimeout | | `vm_boot_timeout` | 180 |
| Host | `vm_host` | | `""` (local) |

### 4.3 NICConfig — Per-NIC QEMU Configuration (`node.go`)

```go
// NICConfig represents a single QEMU NIC attachment.
type NICConfig struct {
    Index      int    // QEMU NIC index (0=mgmt, 1..N=data)
    NetdevID   string // "eth0", "eth1", ...
    Interface  string // SONiC interface name ("Ethernet0", etc.) or "mgmt"
    ConnectAddr string // "IP:PORT" — newtlink endpoint to connect to (empty for mgmt)
    MAC        string // hardware MAC address assigned to this NIC (empty = QEMU auto)
}
```

All data NICs use `-netdev socket,connect=<ConnectAddr>`. The newtlink bridge
worker listens on the target port before QEMU starts. Management NIC (index 0)
uses `-netdev user` with SSH port forwarding — `ConnectAddr` is empty.

### 4.4 LinkConfig — Resolved Link (`link.go`)

```go
// LinkConfig represents a resolved link between two device NICs.
// Each link has two newtlink ports — one per endpoint.
type LinkConfig struct {
    A         LinkEndpoint
    Z         LinkEndpoint
    APort     int    // newtlink listen port for A-side
    ZPort     int    // newtlink listen port for Z-side
    ABind     string // newtlink bind address for A-side ("127.0.0.1" or "0.0.0.0")
    ZBind     string // newtlink bind address for Z-side ("127.0.0.1" or "0.0.0.0")
    WorkerHost string // host that runs this link's bridge worker (empty = local)
}

// LinkEndpoint identifies one side of a link.
type LinkEndpoint struct {
    Device    string // device name
    Interface string // SONiC interface name (e.g. "Ethernet0")
    NICIndex  int    // QEMU NIC index (after interface map resolution)
}
```

### 4.5 LabState — Persisted State (`state.go`)

```go
// LabState is persisted to ~/.newtlab/labs/<name>/state.json.
type LabState struct {
    Name       string                   `json:"name"`
    Created    time.Time                `json:"created"`
    SpecDir    string                   `json:"spec_dir"`
    SSHKeyPath string                   `json:"ssh_key_path,omitempty"`
    Nodes      map[string]*NodeState    `json:"nodes"`
    Links      []*LinkState             `json:"links"`
    BridgePID  int                      `json:"bridge_pid,omitempty"` // deprecated: use Bridges
    Bridges    map[string]*BridgeState  `json:"bridges,omitempty"`   // host ("" = local) → bridge info
}

// NodeState tracks per-node runtime state.
type NodeState struct {
    PID            int    `json:"pid"`
    Status         string `json:"status"`            // "running", "stopped", "error"
    Phase          string `json:"phase,omitempty"`
    DeviceType     string `json:"device_type,omitempty"`
    Image          string `json:"image,omitempty"`
    SSHPort        int    `json:"ssh_port"`
    SSHUser        string `json:"ssh_user,omitempty"`
    ConsolePort    int    `json:"console_port"`
    OriginalMgmtIP string `json:"original_mgmt_ip"`  // saved before patching, restored on destroy
    Host           string `json:"host,omitempty"`     // host name (empty = local)
    HostIP         string `json:"host_ip,omitempty"`  // host IP address (empty = 127.0.0.1)
    VMName         string `json:"vm_name,omitempty"`
    Namespace      string `json:"namespace,omitempty"`
}

// BridgeState tracks a per-host bridge process.
type BridgeState struct {
    PID       int    `json:"pid"`
    HostIP    string `json:"host_ip,omitempty"` // "" for local
    StatsAddr string `json:"stats_addr"`        // "host:port" for TCP stats queries
}

// LinkState tracks per-link allocation.
type LinkState struct {
    A          string `json:"a"`                     // "device:interface"
    Z          string `json:"z"`                     // "device:interface"
    APort      int    `json:"a_port"`                // newtlink A-side port
    ZPort      int    `json:"z_port"`                // newtlink Z-side port
    WorkerHost string `json:"worker_host,omitempty"` // host running the bridge worker
}
```

`BridgePID` is retained for backward compatibility with state files created
before multi-host bridge distribution. `Destroy` checks `Bridges` first,
falling back to `BridgePID` for legacy state files.

**State directory layout:**

```
~/.newtlab/labs/<name>/
├── state.json           # LabState
├── qemu/
│   ├── spine1.pid       # QEMU PID file
│   └── spine1.mon       # QEMU monitor socket
├── disks/
│   └── spine1.qcow2     # COW overlay
└── logs/
    └── spine1.log        # QEMU stdout/stderr
```

---

## 5. QEMU Command Builder (`qemu.go`)

### 5.1 QEMUCommand

```go
// QEMUCommand builds a QEMU invocation for a single node.
type QEMUCommand struct {
    Node     *NodeConfig
    StateDir string      // lab state directory
    KVM      bool        // if true, adds -enable-kvm to the command line
}

// Build returns an exec.Cmd ready to start the QEMU process.
// Binary: `qemu-system-x86_64` resolved from PATH.
func (q *QEMUCommand) Build() *exec.Cmd
```

### 5.2 QEMU Argument Map

`Build()` produces arguments in the following order:

| Argument | Source | Example |
|----------|--------|---------|
| `-m <memory>` | `Node.Memory` | `-m 4096` |
| `-smp <cpus>` | `Node.CPUs` | `-smp 2` |
| `-cpu host` or `-cpu host,<features>` | `Node.CPUFeatures` | `-cpu host,+sse4.2` |
| `-enable-kvm` | `KVM` field set by caller (caller checks `/dev/kvm`) | `-enable-kvm` |
| `-drive file=<overlay>,if=virtio,format=qcow2` | `StateDir/disks/<name>.qcow2` | |
| `-nographic` | always | `-nographic` |
| `-serial tcp::<console_port>,server,nowait` | `Node.ConsolePort` | `-serial tcp::30000,server,nowait` |
| `-monitor unix:<mon_socket>,server,nowait` | `StateDir/qemu/<name>.mon` | |
| `-pidfile <pid_file>` | `StateDir/qemu/<name>.pid` | |
| `-netdev user,id=mgmt,hostfwd=tcp::<ssh_port>-:22` | `Node.SSHPort` | |
| `-device <nic_driver>,netdev=mgmt` | `Node.NICDriver` | `-device e1000,netdev=mgmt` |

Per data NIC (index 1..N) — all NICs connect to newtlink:

| Argument | Value |
|----------|-------|
| `-netdev` | `socket,id=ethN,connect=IP:PORT` |
| `-device` | `<nic_driver>,netdev=ethN,romfile=` |

Every data NIC uses `connect=` to dial the newtlink bridge worker's listening
port for that endpoint. `romfile=` suppresses PXE ROM loading.

> **Note:** `romfile=` is needed on all NICs to prevent PXE boot attempts on
> data ports, which delays startup and can interfere with VPP.

### 5.3 Process Management

```go
// StartNode launches the QEMU process for a node.
// Redirects stdout/stderr to logs/<name>.log.
// Returns after process is started (does not wait for boot).
func StartNode(node *NodeConfig, stateDir, hostIP string) (int, error)

// StopNode sends SIGTERM to the QEMU process, then SIGKILL after 10s.
func StopNode(pid int, hostIP string) error

// IsRunning checks if a QEMU process is alive by PID.
func IsRunning(pid int, hostIP string) bool
```

### 5.4 Helper Functions

```go
// kvmAvailable returns true if /dev/kvm exists and is writable.
// Falls back to TCG (software emulation) if KVM is not available.
func kvmAvailable() bool

// destroyExisting tears down a stale deployment found via state.json.
// Called by Deploy() when --force is set and a previous lab state exists.
// Wraps Lab.Destroy() on the loaded state.
func (l *Lab) destroyExisting(existing *LabState) error
```

Port conflict detection has been moved to `probe.go` (see §7.10).

---

## 6. Interface Mapping (`iface_map.go`)

### 6.1 Map Types

| Map Type | Formula | NIC 1 | NIC 2 | NIC 3 | NIC 4 | Platforms |
|----------|---------|-------|-------|-------|-------|-----------|
| `sequential` | NIC N → Ethernet(N-1) | Ethernet0 | Ethernet1 | Ethernet2 | Ethernet3 | VPP |
| `stride-4` | NIC N → Ethernet((N-1)*4) | Ethernet0 | Ethernet4 | Ethernet8 | Ethernet12 | VS, Cisco |
| `custom` | explicit map from `vm_interface_map_custom` | (varies) | | | | Vendor |

NIC 0 is always management — data NICs start at index 1.

### 6.2 Functions

```go
// ResolveNICIndex returns the QEMU NIC index for a SONiC interface name
// using the given interface map scheme. When interfaceMap is "custom",
// the customMap parameter is used for direct lookup; otherwise it is ignored.
//
// Examples (stride-4):
//   ResolveNICIndex("stride-4", "Ethernet0", nil)  → 1
//   ResolveNICIndex("stride-4", "Ethernet4", nil)  → 2
//   ResolveNICIndex("stride-4", "Ethernet8", nil)  → 3
//
// Examples (sequential):
//   ResolveNICIndex("sequential", "Ethernet0", nil) → 1
//   ResolveNICIndex("sequential", "Ethernet1", nil) → 2
//
// Examples (custom):
//   ResolveNICIndex("custom", "Ethernet0", map[string]int{"Ethernet0": 3}) → 3
func ResolveNICIndex(interfaceMap, interfaceName string, customMap map[string]int) (int, error)

// ResolveInterfaceName returns the SONiC interface name for a QEMU NIC index.
// Inverse of ResolveNICIndex. When interfaceMap is "custom", the customMap
// parameter is used for reverse lookup; otherwise it is ignored.
//
// Examples (stride-4):
//   ResolveInterfaceName("stride-4", 1, nil) → "Ethernet0"
//   ResolveInterfaceName("stride-4", 2, nil) → "Ethernet4"
//
// Examples (custom):
//   ResolveInterfaceName("custom", 3, map[string]int{"Ethernet0": 3}) → "Ethernet0"
func ResolveInterfaceName(interfaceMap string, nicIndex int, customMap map[string]int) string
```

### 6.3 Parsing

```go
// parseEthernetIndex extracts the numeric index from "EthernetN".
// Returns -1 if the name is not a valid Ethernet interface.
func parseEthernetIndex(name string) int
```

---

## 7. Link Wiring (`link.go`)

### 7.1 AllocateLinks

```go
// AllocateLinks resolves topology links into LinkConfig entries with
// port allocations and NIC index assignments. Each link gets two ports
// (one per endpoint) for the newtlink bridge worker.
func AllocateLinks(
    links []*spec.TopologyLink,
    nodes map[string]*NodeConfig,
    config *NewtLabConfig,
    hostMap map[string]HostMapping,
) ([]*LinkConfig, error)
```

**Algorithm:**

1. Iterate `topology.json` links in sorted order (deterministic for multi-host)
2. Assign two ports per link: `config.LinkPortBase + (linkIndex * 2)` for A-side, `+1` for Z-side
3. Resolve NIC index for each endpoint via `ResolveNICIndex(node.InterfaceMap, iface)`
4. Determine worker placement (see §7.5):
   - Same-host link: worker host = shared host
   - Cross-host link: greedy assignment to the endpoint host with fewer cross-host workers
5. Determine bind addresses based on worker placement:
   - Local endpoint (VM on same host as worker): bind `127.0.0.1`
   - Remote endpoint (VM on different host): bind `0.0.0.0`
6. Determine connect addresses for QEMU:
   - VM on worker host: connect `127.0.0.1:<port>`
   - VM on remote host: connect `<worker-host-IP>:<port>`
7. Attach NIC configs to corresponding `NodeConfig.NICs` slice
8. Return `[]*LinkConfig`

### 7.2 Port Allocation Summary

Each link uses two consecutive ports (one per endpoint):

```
Link ports:      link_port_base + (link_index * 2)       A-side
                 link_port_base + (link_index * 2) + 1   Z-side
Console ports:   console_port_base + node_index           (30000, 30001, ...)
SSH ports:       ssh_port_base + node_index               (40000, 40001, ...)
```

Node index is determined by sorted device name order (deterministic).

### 7.3 NIC Assembly

After link allocation, each `NodeConfig.NICs` contains:

| Index | NetdevID | Role | Netdev Type |
|-------|----------|------|-------------|
| 0 | `mgmt` | Management | `user` (hostfwd SSH) |
| 1 | `eth1` | Data (first link) | `socket` (connect to newtlink) |
| 2 | `eth2` | Data (second link) | `socket` (connect to newtlink) |
| ... | ... | ... | ... |

### 7.4 newtlink Bridge Worker (`link.go`)

```go
// BridgeWorker manages a single link's newtlink bridge.
// It listens on two ports, accepts one connection on each,
// and copies Ethernet frames bidirectionally until one side closes.
type BridgeWorker struct {
    Link      *LinkConfig
    aListener net.Listener     // listening on ABind:APort
    zListener net.Listener     // listening on ZBind:ZPort
    aToZBytes atomic.Int64     // A→Z byte counter
    zToABytes atomic.Int64     // Z→A byte counter
    sessions  atomic.Int64     // connection pair count
    connected atomic.Bool      // true when both sides connected
}

// Bridge holds all bridge workers and provides lifecycle and stats access.
type Bridge struct {
    workers []*BridgeWorker
    wg      sync.WaitGroup
}

// Stop closes all listeners and waits for goroutines to finish.
func (b *Bridge) Stop()

// Stats returns a snapshot of all bridge worker counters.
func (b *Bridge) Stats() BridgeStats

// StartBridgeWorkers opens TCP listeners for all links and spawns bridge
// goroutines. Returns a Bridge that provides Stop() and Stats().
func StartBridgeWorkers(links []*LinkConfig) (*Bridge, error)
```

**Bridge worker lifecycle:**

1. `StartBridgeWorkers` opens TCP listeners for every link endpoint (2 per link)
2. If any listener fails (port conflict), all opened listeners are closed and
   an error is returned — no partial state
3. For each link, a goroutine is spawned that:
   a. Accepts exactly one connection on each listener (A and Z)
   b. Spawns two `io.Copy` goroutines: A→Z and Z→A (with byte counting via `countingWriter`)
   c. When either copy returns (connection closed), closes both connections
   d. Re-accepts new connections (handles QEMU restart without newtlink restart)
4. `Stop()` closes all listeners and waits for goroutines to exit

**Frame bridging** uses `io.Copy` — QEMU's socket netdev sends/receives raw
Ethernet frames prefixed with a 4-byte length header. `io.Copy` is transparent
to this framing since it operates on the byte stream.

**Reconnection:** If a QEMU VM is stopped and restarted (`newtlab stop`/`start`),
the bridge worker detects the closed connection and loops back to accept a new one.
This means newtlink workers survive VM restarts without needing to be restarted.

### 7.6 Bridge Config and Stats (`bridge.go`)

```go
// BridgeConfig is the serialized link configuration read by the bridge process.
type BridgeConfig struct {
    Links     []BridgeLink `json:"links"`
    StatsAddr string       `json:"stats_addr,omitempty"` // TCP listen addr for remote stats queries
}

// BridgeLink holds the bind/port config for one link's bridge worker.
type BridgeLink struct {
    APort int    `json:"a_port"`
    ZPort int    `json:"z_port"`
    ABind string `json:"a_bind"`
    ZBind string `json:"z_bind"`
    A     string `json:"a"` // display label, e.g. "spine1:Ethernet0"
    Z     string `json:"z"` // display label, e.g. "leaf1:Ethernet0"
}

// BridgeStats is the telemetry snapshot returned over Unix socket or TCP.
type BridgeStats struct {
    Links []LinkStats `json:"links"`
}

// LinkStats holds telemetry counters for a single bridge link.
type LinkStats struct {
    A         string `json:"a"`
    Z         string `json:"z"`
    APort     int    `json:"a_port"`
    ZPort     int    `json:"z_port"`
    AToZBytes int64  `json:"a_to_z_bytes"`
    ZToABytes int64  `json:"z_to_a_bytes"`
    Sessions  int64  `json:"sessions"`
    Connected bool   `json:"connected"`
}
```

### 7.7 Bridge Process Lifecycle (`bridge.go`)

```go
// WriteBridgeConfig serializes link config to bridge.json in the state dir.
func WriteBridgeConfig(stateDir string, links []*LinkConfig, statsAddr string) error

// startBridgeProcess spawns a newtlink bridge process locally.
func startBridgeProcess(labName, stateDir string) (int, error)

// startBridgeProcessRemote starts a bridge process on a remote host via SSH.
// Creates remote state dir, writes bridge.json via stdin, starts via nohup.
func startBridgeProcessRemote(labName, hostIP string, configJSON []byte) (int, error)

// stopBridgeProcessRemote kills a bridge process on a remote host via SSH.
func stopBridgeProcessRemote(pid int, hostIP string) error
```

### 7.8 Stats Queries (`bridge.go`)

```go
// QueryBridgeStats connects to a running bridge's stats endpoint.
// The addr is either a Unix socket path (starts with "/") or a TCP
// address ("host:port").
func QueryBridgeStats(addr string) (*BridgeStats, error)

// QueryAllBridgeStats aggregates stats from all bridge processes in a lab.
// Loads state.json, queries each bridge in Bridges map, merges results.
// For local bridges, prefers Unix socket if available.
// Falls back to legacy single-bridge Unix socket if Bridges map is empty.
func QueryAllBridgeStats(labName string) (*BridgeStats, error)
```

### 7.5 Worker Placement (`link.go`)

```go
// PlaceWorkers assigns each cross-host link to an endpoint host using
// greedy load balancing. Local links are assigned to their shared host.
// The assignment is deterministic: links are processed in sorted order
// and ties are broken by host name sort order.
//
// Mutates LinkConfig.WorkerHost in place. Single-host deployments
// set all WorkerHost fields to empty string (local).
func PlaceWorkers(links []*LinkConfig, nodes map[string]*NodeConfig)
```

**Algorithm:**

```
hostCount := map[string]int{}  // cross-host workers assigned to each host

for each link in sorted order:
    hostA := nodes[link.A.Device].Host
    hostZ := nodes[link.Z.Device].Host

    if hostA == hostZ:
        link.WorkerHost = hostA          // local link — trivial
        continue

    // Cross-host: assign to endpoint host with fewer workers
    if hostCount[hostA] < hostCount[hostZ]:
        link.WorkerHost = hostA
    else if hostCount[hostZ] < hostCount[hostA]:
        link.WorkerHost = hostZ
    else:
        // Tie: pick lexicographically smaller host name
        link.WorkerHost = min(hostA, hostZ)

    hostCount[link.WorkerHost]++
```

This gets within 1 of optimal balance for any host pair. For 3+ hosts,
balancing is pairwise — each cross-host link only considers its two endpoint
hosts. This is correct because traffic is pairwise (the worker bridges frames
between exactly two hosts, so only those two hosts' load matters).

`FilterHost` uses `WorkerHost` to determine which bridge workers a given host
is responsible for. A host starts workers for:
- All local links (both VMs on this host)
- Cross-host links where `WorkerHost` equals this host

### 7.9 Server Pool Auto-Placement (`placement.go`)

```go
// PlaceNodes assigns unpinned nodes to servers using a spread algorithm
// that minimizes maximum load across servers. Pinned nodes (Host != "")
// are validated against the server list and count toward capacity.
//
// If servers is empty, PlaceNodes is a no-op (single-host mode).
func PlaceNodes(nodes map[string]*NodeConfig, servers []*spec.ServerConfig) error
```

**Algorithm:**

```
Phase 1 — Pinned nodes (sorted by name):
    for each node with Host != "":
        find matching server in servers list
        error if server not found or over capacity
        increment server's count

Phase 2 — Unpinned nodes (sorted by name):
    for each node with Host == "":
        sort servers by (count asc, name asc)
        pick first server with available capacity
        set node.Host = server.Name
        increment server's count
        error if no server has capacity
```

**Properties:**
- **Deterministic**: sorted iteration produces the same result from any host
- **Spread**: minimizes maximum load across servers
- **Respects capacity**: `max_nodes=0` means unlimited
- **Reduces to round-robin** when all servers are equal and no nodes are pinned

**Internal types:**

```go
// serverLoad tracks current node count for a server during placement.
type serverLoad struct {
    server *spec.ServerConfig
    count  int
}
```

**Integration point:** Called in `NewLab` after `ResolveNodeConfig` (which
reads `profile.VMHost` into `nc.Host`) but before `AllocateLinks` (which
needs final host assignments for worker placement).

### 7.10 Port Conflict Detection (`probe.go`)

```go
// PortAllocation describes a single TCP port allocation in the lab.
type PortAllocation struct {
    Host    string // host name ("" = local)
    HostIP  string // resolved IP ("" = 127.0.0.1 for local)
    Port    int
    Purpose string // e.g. "spine1 SSH", "link spine1:Ethernet0 A-side"
}

// CollectAllPorts gathers all TCP port allocations for the lab:
// SSH ports, console ports, link A/Z ports, and bridge stats ports.
func CollectAllPorts(lab *Lab) []PortAllocation

// ProbeAllPorts checks that all allocated ports are free.
// Local ports: net.Listen test. Remote ports: SSH + ss.
// Returns a multi-error listing all conflicts.
func ProbeAllPorts(allocations []PortAllocation) error

// probePortLocal attempts net.Listen on the given port to check availability.
// Returns error if the port is in use.
func probePortLocal(port int) error

// probePortsRemote checks port availability on a remote host via SSH + ss.
// Executes a single SSH session per host: ss -tlnH '( sport = :P1 or sport = :P2 ... )'
// Returns a map of port → error for ports that are in use.
func probePortsRemote(hostIP string, ports []int) map[int]error

// allocateBridgeStatsPorts returns the stats port for each bridge worker host.
// Mirrors the allocation logic in Deploy(): counting down from LinkPortBase - 1.
func allocateBridgeStatsPorts(lab *Lab) map[string]int
```

**`CollectAllPorts`** gathers:
1. SSH and console ports for each node (on the node's host)
2. Link A/Z ports for each link (on the bridge worker's host)
3. Bridge stats ports for each unique worker host

**`ProbeAllPorts`** groups allocations by host:
- **Local** (`Host == ""`): each port tested with `net.Listen`
- **Remote**: one SSH connection per host, running `ss -tlnH` with a filter
  for all allocated ports on that host. Any output means a conflict.

All conflicts are collected and returned as a single sorted multi-error.

---

## 8. Disk Management (`disk.go`)

```go
// CreateOverlay creates a QCOW2 copy-on-write overlay backed by baseImage.
// The overlay is written to overlayPath.
//
// Runs: qemu-img create -f qcow2 -b <baseImage> -F qcow2 <overlayPath>
func CreateOverlay(baseImage, overlayPath string) error

// RemoveOverlay deletes an overlay disk file.
func RemoveOverlay(overlayPath string) error
```

The base image is never modified. Each VM gets its own overlay in
`~/.newtlab/labs/<name>/disks/<device>.qcow2`.

`~` in `vm_image` paths is expanded to `$HOME` at resolution time.

---

## 9. Boot (`boot.go`)

### 9.1 WaitForSSH

```go
// WaitForSSH polls SSH connectivity to host:port with the given credentials.
// Returns nil when SSH login succeeds, or error if timeout is reached.
// Polls every 5 seconds.
func WaitForSSH(ctx context.Context, host string, port int, user, pass string, timeout time.Duration) error
```

**Flow:**

1. Loop until timeout
2. Attempt `ssh.Dial("tcp", host:port, config)` with 5s dial timeout
3. On success: open session, run `echo ready`, close, return nil
4. On failure: sleep 5s, retry
5. On timeout: return `fmt.Errorf("SSH timeout after %s for %s:%d", timeout, host, port)`

### 9.2 BootstrapNetwork

```go
// BootstrapNetwork connects to the serial console and prepares the VM for SSH access.
// Steps:
//  1. Wait for login prompt (VM may still be booting)
//  2. Log in using consoleUser/consolePass (the user baked into the image)
//  3. Bring up eth0 with DHCP (QEMU user-mode networking requires this)
//  4. If sshUser differs from consoleUser, create the SSH user with sudo + bash access
//  5. Log out
func BootstrapNetwork(ctx context.Context, consoleHost string, consolePort int, consoleUser, consolePass, sshUser, sshPass string, timeout time.Duration) error
```

**Flow:**

1. TCP connect to `consoleHost:consolePort` (serial console)
2. Use `readUntil` helper to wait for `login:` prompt (with timeout)
3. Send `consoleUser` + `consolePass` to log in
4. Run `sudo ip link set eth0 up` and `sudo dhclient eth0`
5. If `sshUser != consoleUser`: create SSH user via `useradd -m -s /bin/bash`, set password, add to sudo group
6. Log out with `exit`

### 9.3 Platform Boot Patches (`patch.go`)

Different SONiC images have platform-specific initialization quirks that must
be patched after boot but before provisioning. Rather than hardcoding per-platform
Go code, newtlab uses a **declarative boot patch framework**: JSON descriptors
paired with Go templates, embedded at compile time via `//go:embed`.

#### Patch Descriptor

```go
// BootPatch defines a declarative patch to apply after VM boot.
// Patch descriptors are JSON files under patches/<dataplane>/.
type BootPatch struct {
    Description  string       `json:"description"`
    PreCommands  []string     `json:"pre_commands,omitempty"`  // shell commands to run before files
    DisableFiles []string     `json:"disable_files,omitempty"` // paths to rename to .disabled
    Files        []FilePatch  `json:"files,omitempty"`         // render template → upload to VM
    Redis        []RedisPatch `json:"redis,omitempty"`         // render template → redis-cli pipeline
    PostCommands []string     `json:"post_commands,omitempty"` // shell commands to run after everything
}

// FilePatch renders a Go template and writes the result to a path on the VM.
type FilePatch struct {
    Template string `json:"template"` // filename relative to patch directory
    Dest     string `json:"dest"`     // absolute path on VM (supports Go template expansion)
}

// RedisPatch renders a Go template into redis-cli commands and executes them.
type RedisPatch struct {
    DB       int    `json:"db"`       // Redis database number (e.g. 4 for CONFIG_DB)
    Template string `json:"template"` // renders to one redis-cli command per line
}
```

#### Template Variables

```go
// PatchVars holds the variables available to all boot patch templates.
// Computed from NodeConfig and PlatformSpec — no VM-side discovery needed.
type PatchVars struct {
    NumPorts  int      // number of data NICs (from link allocation)
    PCIAddrs  []string // deterministic QEMU PCI addresses (from QEMUPCIAddrs)
    HWSkuDir  string   // "/usr/share/sonic/device/x86_64-kvm_x86_64-r0/<HWSKU>"
    PortSpeed int      // from PlatformSpec.DefaultSpeed (parsed to int)
    Platform  string   // platform name (e.g. "sonic-vpp")
    Dataplane string   // "vpp", "barefoot", "" (from PlatformSpec.Dataplane)
    Release   string   // from PlatformSpec.VMImageRelease
}

// QEMUPCIAddrs returns deterministic PCI addresses for data NICs.
// QEMU assigns PCI slots sequentially starting from a known base.
// For N data NICs: 00:03.0, 00:04.0, 00:05.0, ... (slot = 3 + index).
func QEMUPCIAddrs(dataNICs int) []string
```

Key design point: **all template variables are derived from what newtlab already
knows** (QEMU config, platform spec, link allocation). No SSH-based discovery
inside the VM. This eliminates the multi-source PCI fallback logic in the
current `FixVPPConfig`.

#### Patch Resolution

```go
// ResolveBootPatches returns the ordered list of patches for a platform.
// Resolution order:
//   1. patches/<dataplane>/always/*.json  (sorted by filename)
//   2. patches/<dataplane>/<release>/*.json (sorted by filename, if release != "")
// Patches are loaded from //go:embed at compile time.
// Returns nil if no patches exist for the dataplane (no error).
func ResolveBootPatches(dataplane, release string) ([]*BootPatch, error)
```

#### Patch Engine

```go
// ApplyBootPatches applies all resolved patches to a VM via SSH.
// For each patch in order:
//   1. Execute pre_commands
//   2. Rename disable_files to .disabled
//   3. Render and upload file templates
//   4. Render and execute redis templates
//   5. Execute post_commands
// All commands run via SSH. Template rendering uses Go text/template.
// Returns on first error (patches are ordered, later patches may depend on earlier ones).
func ApplyBootPatches(ctx context.Context, host string, port int, user, pass string, patches []*BootPatch, vars *PatchVars) error
```

#### Example: VPP port config patch

Descriptor (`patches/vpp/always/02-port-config.json`):
```json
{
    "description": "Fix empty port_config.ini and sonic_vpp_ifmap.ini from broken factory hook",
    "pre_commands": [
        "while pgrep -f config-setup >/dev/null 2>&1; do sleep 2; done"
    ],
    "disable_files": [
        "/etc/config-setup/factory-default-hooks.d/10-01-vpp-cfg-init"
    ],
    "files": [
        { "template": "syncd_vpp_env.tmpl", "dest": "/etc/sonic/vpp/syncd_vpp_env" },
        { "template": "port_config.ini.tmpl", "dest": "{{.HWSkuDir}}/port_config.ini" },
        { "template": "sonic_vpp_ifmap.ini.tmpl", "dest": "{{.HWSkuDir}}/sonic_vpp_ifmap.ini" }
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

Template example (`patches/vpp/always/port_config.ini.tmpl`):
```
# name  lanes  alias  index  speed
{{- range $i, $_ := .PCIAddrs}}
Ethernet{{mul $i 4}}  {{mul $i 4}}  Ethernet{{mul $i 4}}  {{$i}}  {{$.PortSpeed}}
{{- end}}
```

---

## 10. Profile Patching (`profile.go`)

### 10.1 PatchProfiles

```go
// PatchProfiles updates device profile JSON files with newtlab runtime values.
// Called after successful VM deployment.
func PatchProfiles(lab *Lab) error
```

**Per-device patch:**

1. Read profile JSON from `<specDir>/profiles/<device>.json`
2. Unmarshal into `map[string]interface{}` (preserves all existing fields)
3. Save `OriginalMgmtIP` from current profile into `NodeState` (for restore on destroy)
4. Set `mgmt_ip` = `"127.0.0.1"` (single-host) or host IP (multi-host)
5. Set `ssh_port` = node's allocated SSH port
6. Set `console_port` = node's allocated console port
7. Set `ssh_user` / `ssh_pass` from resolved credentials (if not already set)
8. Set `mac` = `GenerateMAC(name, 0)` — deterministic MAC using QEMU OUI `52:54:00` + SHA256 of device name. Flows through profile → resolved specs → composite → `DEVICE_METADATA|localhost.mac`
9. Marshal back with `json.MarshalIndent` (4-space indent)
10. Write to same path

### 10.2 RestoreProfiles

```go
// RestoreProfiles removes newtlab-written fields from profiles.
// Called during destroy to clean up.
func RestoreProfiles(lab *Lab) error
```

Removes: `ssh_port`, `console_port`, `mac`. Resets `mgmt_ip` to the original value saved in `NodeState.OriginalMgmtIP` (captured during `PatchProfiles` before overwriting).

---

## 11. State Management (`state.go`)

```go
// LabDir returns the state directory path for a lab name.
//   ~/.newtlab/labs/<name>/
func LabDir(name string) string

// SaveState writes lab state to state.json in the lab directory.
func SaveState(state *LabState) error

// LoadState reads lab state from state.json. Returns error if not found.
func LoadState(name string) (*LabState, error)

// RemoveState deletes the entire lab state directory.
func RemoveState(name string) error

// ListLabs returns names of all labs with state directories.
func ListLabs() ([]string, error)
```

---

## 12. Deploy Flow

**Deploy pseudocode** (helper functions like `loadSpecs`, `allocatePorts`, `sortedListenFirst` are internal — shown here to illustrate the orchestration flow):

```go
func (l *Lab) Deploy() error {
    // 0. Check for stale state — if state.json exists, a previous deployment
    // was not properly destroyed. Require --force to overwrite.
    if existing, err := LoadState(l.Name); err == nil && existing != nil {
        if !l.Force {
            return fmt.Errorf("newtlab: lab %s already deployed (created %s); use --force to redeploy",
                l.Name, existing.Created.Format(time.RFC3339))
        }
        // --force: destroy existing deployment first
        l.destroyExisting(existing)
    }

    // 1. Load specs
    topology, platforms, profiles := loadSpecs(l.SpecDir)

    // 2. Resolve newtlab config (with defaults)
    l.Config = resolveNewtLabConfig(topology.NewtLab)

    // 3. Resolve node configs (profile > platform > defaults)
    for name, device := range topology.Devices {
        profile := profiles[name]
        platform := platforms.Platforms[profile.Platform]
        l.Nodes[name] = ResolveNodeConfig(name, profile, platform)
    }

    // 4. Allocate ports (SSH, console per node; link ports per link)
    allocatePorts(l.Nodes, l.Config)  // sets SSHPort, ConsolePort on each node

    // 4a. Auto-place nodes across server pool (if configured)
    //     Runs after ResolveNodeConfig reads profile.VMHost into nc.Host,
    //     so pinned nodes are already set. PlaceNodes fills in the rest.
    if len(l.Config.Servers) > 0 {
        if err := PlaceNodes(l.Nodes, l.Config.Servers); err != nil {
            return nil, err
        }
    }

    // 5. Allocate links (assigns NICs, ports, listen/connect)
    l.Links = AllocateLinks(topology.Links, l.Nodes, l.Config)

    // 5a. Comprehensive port conflict detection (§7.10)
    //     Probes SSH, console, link, and bridge stats ports — local and remote.
    allocs := CollectAllPorts(l)
    if err := ProbeAllPorts(allocs); err != nil {
        return fmt.Errorf("newtlab: port conflicts:\n%w", err)
    }

    // 6. Create state directory (~/.newtlab/labs/<name>/)
    os.MkdirAll(l.StateDir+"/qemu", 0755)
    os.MkdirAll(l.StateDir+"/disks", 0755)
    os.MkdirAll(l.StateDir+"/logs", 0755)

    // 7. Create overlay disks
    for name, node := range l.Nodes {
        overlay := l.StateDir + "/disks/" + name + ".qcow2"
        CreateOverlay(node.Image, overlay)
    }

    // 7a. Start newtlink bridge processes (per-host)
    //     Group links by WorkerHost and start a separate bridge per host.
    //     Each bridge gets a stats TCP port: LinkPortBase - 1 - hostIndex.
    hostLinks := groupByWorkerHost(l.Links)
    l.State.Bridges = map[string]*BridgeState{}
    statsPortNext := l.Config.LinkPortBase - 1
    for _, host := range sortedHosts(hostLinks) {
        statsPort := statsPortNext; statsPortNext--
        if host == "" {
            // Local: write bridge.json, spawn child process
            WriteBridgeConfig(l.StateDir, hostLinks[host], statsBindAddr)
            pid := startBridgeProcess(l.Name, l.StateDir)
            l.State.Bridges[""] = &BridgeState{PID: pid, StatsAddr: "127.0.0.1:<port>"}
            l.State.BridgePID = pid  // back-compat
        } else {
            // Remote: marshal config JSON, SSH to host, start via nohup
            pid := startBridgeProcessRemote(l.Name, hostIP, configJSON)
            l.State.Bridges[host] = &BridgeState{PID: pid, HostIP: hostIP, StatsAddr: "..."}
        }
        // Wait for bridge listeners (link ports + stats port)
        for _, lc := range hostLinks[host] {
            waitForPort(probeHost, lc.APort, 10*time.Second)
            waitForPort(probeHost, lc.ZPort, 10*time.Second)
        }
    }

    // 8. Build and start QEMU commands
    //    No startup ordering needed — all VMs connect outbound to newtlink,
    //    which is already listening. Start nodes in sorted name order.
    for _, name := range sortedNodeNames(l.Nodes) {
        pid := StartNode(l.Nodes[name], l.StateDir)
        l.State.Nodes[name] = &NodeState{PID: pid, Status: "running", ...}
    }

    // 9a. Bootstrap network (parallel, per node)
    //     Serial console: login, bring up eth0, DHCP, create SSH user
    var wg sync.WaitGroup
    for name, node := range l.Nodes {
        wg.Add(1)
        go func(name string, node *NodeConfig) {
            defer wg.Done()
            BootstrapNetwork(consoleHost, node.ConsolePort,
                node.ConsoleUser, node.ConsolePass, node.SSHUser, node.SSHPass,
                time.Duration(node.BootTimeout)*time.Second)
        }(name, node)
    }
    wg.Wait()

    // 9b. Wait for SSH readiness (parallel, per node)
    for name, node := range l.Nodes {
        wg.Add(1)
        go func(name string, node *NodeConfig) {
            defer wg.Done()
            err := WaitForSSH("127.0.0.1", node.SSHPort, node.SSHUser, node.SSHPass,
                time.Duration(node.BootTimeout)*time.Second)
            if err != nil {
                l.State.Nodes[name].Status = "error"
            }
        }(name, node)
    }
    wg.Wait()

    // 9c. Apply platform boot patches (parallel, per node)
    //     Resolve patches for platform's dataplane + image release,
    //     compute template vars from NodeConfig, apply via SSH.
    for name, node := range l.Nodes {
        platform := platforms.Platforms[profiles[name].Platform]
        patches := ResolveBootPatches(platform.Dataplane, platform.VMImageRelease)
        if len(patches) > 0 {
            wg.Add(1)
            go func(name string, node *NodeConfig, patches []*BootPatch) {
                defer wg.Done()
                vars := buildPatchVars(node, platform)
                if err := ApplyBootPatches(sshHost, node.SSHPort, node.SSHUser, node.SSHPass, patches, vars); err != nil {
                    log.Printf("newtlab: boot patches for %s: %v", name, err)
                    l.State.Nodes[name].Status = "error"
                }
            }(name, node, patches)
        }
    }
    wg.Wait()

    // 10. Patch profiles (write ssh_port, console_port, mgmt_ip)
    PatchProfiles(l)

    // 11. Save state
    SaveState(l.State)

    return nil
}
```

**`resolveNewtLabConfig`** fills defaults for nil or zero-value fields.
When `servers` is present, derives the `Hosts` map from the server list:

```go
// resolveNewtLabConfig returns a VMLabConfig with defaults applied.
// Takes the optional *spec.NewtLabConfig from topology.json (may be nil).
func resolveNewtLabConfig(cfg *spec.NewtLabConfig) *VMLabConfig {
    resolved := &VMLabConfig{
        LinkPortBase:    20000,
        ConsolePortBase: 30000,
        SSHPortBase:     40000,
    }
    if cfg != nil {
        if cfg.LinkPortBase != 0    { resolved.LinkPortBase = cfg.LinkPortBase }
        if cfg.ConsolePortBase != 0 { resolved.ConsolePortBase = cfg.ConsolePortBase }
        if cfg.SSHPortBase != 0     { resolved.SSHPortBase = cfg.SSHPortBase }
        if len(cfg.Servers) > 0 {
            resolved.Servers = cfg.Servers
            resolved.Hosts = make(map[string]string, len(cfg.Servers))
            for _, s := range cfg.Servers {
                resolved.Hosts[s.Name] = s.Address
            }
        } else {
            resolved.Hosts = cfg.Hosts
        }
    }
    return resolved
}
```

**Start order:** With newtlink, there is no startup ordering constraint.
All newtlink bridge processes are started first (step 7a) — one per host,
each with its assigned links. QEMU processes then start in sorted name order.
Each VM connects outbound to its newtlink ports, which are already listening.

**Mid-deploy failure recovery:** If a QEMU process fails to start (step 8)
or SSH readiness times out (step 9), Deploy() still saves state (step 11)
with the failed node marked as `"error"`. This leaves a valid state.json on
disk so that `newtlab destroy` can clean up all started nodes. The caller sees
a non-nil error. To retry, the user runs `newtlab destroy` then `newtlab deploy`
(or `newtlab deploy --force`, which calls `destroyExisting` automatically).
Partial deploys do **not** auto-rollback — explicit destroy is required.

---

## 13. Destroy Flow

Step-by-step pseudocode for `Lab.Destroy()`:

```go
func (l *Lab) Destroy() error {
    // 1. Load state — also recover SpecDir from persisted state so that
    //    profile restoration can find the original spec files.
    state := LoadState(l.Name)
    l.SpecDir = state.SpecDir

    // Continue-on-error: collect all failures rather than stopping at the first.
    // This ensures maximum cleanup — a failed process kill should not prevent
    // profile restoration or state cleanup.
    var errs []error

    // 2. Kill QEMU processes by PID
    for name, node := range state.Nodes {
        if IsRunning(node.PID) {
            if err := StopNode(node.PID); err != nil {
                errs = append(errs, fmt.Errorf("stop %s (pid %d): %w", name, node.PID, err))
            }
        }
    }

    // 2a. Stop newtlink bridge processes (per-host)
    if len(state.Bridges) > 0 {
        for host, bs := range state.Bridges {
            if bs.HostIP != "" {
                stopBridgeProcessRemote(bs.PID, bs.HostIP)
            } else if isRunningLocal(bs.PID) {
                stopNodeLocal(bs.PID)
            }
        }
    } else if state.BridgePID > 0 && isRunningLocal(state.BridgePID) {
        stopNodeLocal(state.BridgePID) // legacy fallback
    }

    // 3. Restore profiles (remove ssh_port, console_port, reset mgmt_ip)
    if err := RestoreProfiles(l); err != nil {
        errs = append(errs, fmt.Errorf("restore profiles: %w", err))
    }

    // 4. Remove state directory (includes overlay disks, logs, PID files)
    if err := RemoveState(l.Name); err != nil {
        errs = append(errs, fmt.Errorf("remove state: %w", err))
    }

    if len(errs) > 0 {
        return fmt.Errorf("newtlab: destroy had %d errors: %v", len(errs), errs)
    }
    return nil
}
```

### 13.1 Lab.Provision()

Called by `newtlab deploy --provision` or `newtlab provision`. Shells out to `newtron provision` for each device:

```go
// Provision runs newtron provisioning for all (or specified) devices in the lab.
// parallel controls concurrency: 1 = sequential, >1 = concurrent with semaphore.
func (l *Lab) Provision(ctx context.Context, parallel int) error {
    state := LoadState(l.Name)

    sem := make(chan struct{}, parallel)
    var mu sync.Mutex
    var errs []error

    var wg sync.WaitGroup
    for name := range state.Nodes {
        wg.Add(1)
        go func(name string) {
            defer wg.Done()
            sem <- struct{}{}        // acquire semaphore
            defer func() { <-sem }() // release

            // Shell out: newtron provision -S <specDir> -d <name> -x
            cmd := exec.Command("newtron", "provision", "-S", l.SpecDir, "-d", name, "-x")
            output, err := cmd.CombinedOutput()
            if err != nil {
                mu.Lock()
                errs = append(errs, fmt.Errorf("provision %s: %w\n%s", name, err, output))
                mu.Unlock()
            }
        }(name)
    }
    wg.Wait()

    if len(errs) > 0 {
        return errors.Join(errs...)
    }
    return nil
}
```

**Why shell out:** newtlab is a separate binary from newtron. Rather than importing newtron's `pkg/network` (which would create a circular dependency), newtlab invokes the `newtron` CLI. This keeps the tools loosely coupled — newtlab only needs the `newtron` binary on PATH.

---

## 14. CLI Implementation (`cmd/newtlab/`)

### 14.1 Command Tree

Same Cobra pattern as `cmd/newtron/`. Root command with subcommands.

```go
// main.go
func main() {
    rootCmd := &cobra.Command{Use: "newtlab"}
    rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "spec directory")
    rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

    rootCmd.AddCommand(
        newDeployCmd(),
        newDestroyCmd(),
        newStatusCmd(),
        newSSHCmd(),
        newConsoleCmd(),
        newStopCmd(),
        newStartCmd(),
        newProvisionCmd(),
        newVersionCmd(),
    )

    rootCmd.Execute()
}
```

### 14.2 Subcommands

| Command | File | Flags | Description |
|---------|------|-------|-------------|
| `newtlab deploy` | `cmd_deploy.go` | `-S` (required), `--host`, `--force`, `--provision`, `--parallel` | Deploy VMs from spec |
| `newtlab destroy` | `cmd_destroy.go` | `--force` | Stop and remove all VMs |
| `newtlab status` | `cmd_status.go` | (none) | Show VM status table |
| `newtlab ssh <node>` | `cmd_ssh.go` | (none) | SSH to a VM |
| `newtlab console <node>` | `cmd_console.go` | (none) | Attach to serial console |
| `newtlab stop <node>` | `cmd_stop.go` | (none) | Stop a VM (preserves disk) |
| `newtlab start <node>` | `cmd_stop.go` | (none) | Start a stopped VM |
| `newtlab provision` | `cmd_provision.go` | `--device` | Run newtron provisioning |
| `newtlab list` | `main.go` | (none) | List all deployed labs |

### 14.3 deploy

```go
func newDeployCmd() *cobra.Command {
    var host string
    var force bool
    var provision bool
    var parallel int

    cmd := &cobra.Command{
        Use:   "deploy",
        Short: "Deploy VMs from topology.json",
        RunE: func(cmd *cobra.Command, args []string) error {
            lab, err := newtlab.NewLab(specDir)
            if err != nil {
                return err
            }
            lab.Force = force
            if host != "" {
                lab.FilterHost(host) // only deploy nodes for this host
            }
            if err := lab.Deploy(); err != nil {
                return err
            }
            if provision {
                return lab.Provision(parallel)
            }
            return nil
        },
    }

    cmd.Flags().StringVar(&host, "host", "", "deploy only nodes for this host")
    cmd.Flags().BoolVar(&force, "force", false, "force deploy even if already running")
    cmd.Flags().BoolVar(&provision, "provision", false, "provision devices after deploy")
    cmd.Flags().IntVar(&parallel, "parallel", 1, "parallel provisioning threads")
    return cmd
}
```

### 14.4 status

Output format:

```
Lab: spine-leaf (deployed 2026-02-05 10:30:00)

NODE        STATUS    SSH PORT    CONSOLE    PID
spine1      running   40000       30000      12345
leaf1       running   40001       30001      12346

LINK                              PORT     STATUS
spine1:Ethernet0 ↔ leaf1:Ethernet0   20000    connected
```

### 14.5 ssh

```go
// Execs into: ssh -o StrictHostKeyChecking=no -p <port> <user>@127.0.0.1
func newSSHCmd() *cobra.Command
```

### 14.6 console

```go
// Connects to serial console via: telnet 127.0.0.1 <console_port>
// Or uses socat: socat -,rawer TCP:127.0.0.1:<console_port>
func newConsoleCmd() *cobra.Command
```

### 14.7 provision

```go
// Runs newtron provisioning for the lab.
// Equivalent to: newtron provision -S <specDir> [--device <name>]
func newProvisionCmd() *cobra.Command
```

---

## 15. Error Handling

### 15.1 Error Patterns

All errors use `fmt.Errorf` with `%w` wrapping for context:

```go
return fmt.Errorf("newtlab: start node %s: %w", name, err)
return fmt.Errorf("newtlab: create overlay %s: %w", overlayPath, err)
return fmt.Errorf("newtlab: allocate links: port %d conflict: %w", port, err)
```

### 15.2 Error Conditions

| Condition | Error | Recovery |
|-----------|-------|----------|
| No `vm_image` resolved | `"no vm_image for device %s (check platform or profile)"` | Set vm_image in platform or profile |
| Base image not found | `"vm_image not found: %s"` | Download or fix path |
| KVM not available | Warning logged, falls back to TCG | Install KVM or accept slower emulation |
| Port conflict | `"port conflicts:\n  <purpose>: port N in use"` | Change port base or stop conflicting process |
| Pinned to unknown server | `"node X pinned to unknown server Y"` | Fix `vm_host` or add server to `servers` list |
| Server over capacity | `"no server capacity for node X"` | Increase `max_nodes` or add servers |
| SSH boot timeout | `"SSH timeout after %s for %s:%d"` | Increase vm_boot_timeout or check image |
| QEMU process crash | `"QEMU exited with code %d (see %s)"` | Check logs/<node>.log |
| State not found | `"lab %s not found (no state.json)"` | Run deploy first |
| Profile read error | `"reading profile %s: %w"` | Check file permissions |

---

## Cross-References

### References to newtron LLD

| newtron LLD Section | How newtlab Relates |
|----------------------|-------------------|
| §3.1 `PlatformSpec` | newtlab adds VM fields (`vm_image`, `vm_memory`, etc.) to the shared spec type — see §1.1 |
| §3.1 `DeviceProfile` | newtlab adds per-device overrides (`ssh_port`, `console_port`) — see §1.2 |
| §3.1 `TopologySpecFile` | newtlab reads topology for device list, links, and VM host assignments |

### References to device LLD

| Device LLD Section | How newtlab Relates |
|--------------------|-------------------|
| §1 SSH Tunnel | Tunnel reads `SSHPort` written by newtlab profile patching (§10) |
| §5.1 `Device.Connect()` | Connection reads `SSHUser`/`SSHPass`/`SSHPort` from profiles that newtlab patches |

### References to newtrun LLD

| newtrun LLD Section | How newtlab Relates |
|---------------------|-------------------|
| §6.1 `DeployTopology` | newtrun wraps `newtlab.NewLab()` + `newtlab.Lab.Deploy()` — see §4.1 |
| §6.2 `DestroyTopology` | newtrun wraps `newtlab.Lab.Destroy()` — see §4.1 |
| §6.3 Platform Capability Check | newtrun reads `PlatformSpec.Dataplane` (§1.1) to skip verify-ping |
| §4.5 Device connection | newtrun relies on newtlab profile patching (§10) before connecting devices |

