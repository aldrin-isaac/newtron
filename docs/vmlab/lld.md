# vmlab Low-Level Design (LLD)

vmlab orchestrates QEMU virtual machines for SONiC lab environments. This document covers `pkg/vmlab/` — the VM lifecycle, networking, and port management layer. For the high-level architecture, see [vmlab HLD](hld.md). For the device connection layer used after VMs boot, see [Device Layer LLD](../newtron/device-lld.md).

---

## 1. Spec Type Extensions

Changes to existing newtron types in `pkg/spec/types.go`.

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

    // vmlab VM fields
    VMImage        string         `json:"vm_image,omitempty"`
    VMMemory       int            `json:"vm_memory,omitempty"`
    VMCPUs         int            `json:"vm_cpus,omitempty"`
    VMNICDriver    string         `json:"vm_nic_driver,omitempty"`
    VMInterfaceMap       string         `json:"vm_interface_map,omitempty"`
    VMInterfaceMapCustom map[string]int `json:"vm_interface_map_custom,omitempty"` // SONiC name → QEMU NIC index (for "custom" map type)
    VMCPUFeatures  string         `json:"vm_cpu_features,omitempty"`
    VMCredentials  *VMCredentials `json:"vm_credentials,omitempty"`
    VMBootTimeout  int            `json:"vm_boot_timeout,omitempty"`
    Dataplane      bool           `json:"dataplane,omitempty"`
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
| `dataplane` | bool | false | Whether image has a dataplane (VPP, MEMORY) |

### 1.2 DeviceProfile — vmlab Fields

```go
type DeviceProfile struct {
    // ... existing fields ...

    // vmlab per-device overrides (read by vmlab)
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
| `ssh_port` | Write (vmlab) / Read (newtron) | Forwarded SSH port on host |
| `console_port` | Write (vmlab) | Serial console port on host |
| `vm_memory` | Read (vmlab) | Override platform memory |
| `vm_cpus` | Read (vmlab) | Override platform CPU count |
| `vm_image` | Read (vmlab) | Override platform disk image |
| `vm_host` | Read (vmlab) | Target host for multi-host deployment |

### 1.3 TopologySpecFile — vmlab Section

```go
type TopologySpecFile struct {
    Version     string                     `json:"version"`
    Description string                     `json:"description,omitempty"`
    Devices     map[string]*TopologyDevice `json:"devices"`
    Links       []*TopologyLink            `json:"links,omitempty"`
    VMLab       *VMLabConfig               `json:"vmlab,omitempty"`
}
```

### 1.4 VMLabConfig

```go
// VMLabConfig holds vmlab orchestration settings from topology.json.
type VMLabConfig struct {
    LinkPortBase    int               `json:"link_port_base,omitempty"`
    ConsolePortBase int               `json:"console_port_base,omitempty"`
    SSHPortBase     int               `json:"ssh_port_base,omitempty"`
    Hosts           map[string]string `json:"hosts,omitempty"`
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `link_port_base` | 20000 | Starting TCP port for socket links |
| `console_port_base` | 30000 | Starting port for serial consoles |
| `ssh_port_base` | 40000 | Starting port for SSH forwarding |
| `hosts` | (none) | Host name → IP address for multi-host (**Phase 3 — not implemented**) |

**Multi-host deployment (Phase 3 placeholder):** The `hosts` map and `DeviceProfile.VMHost` fields are defined in the spec types for forward compatibility but are **not implemented**. The current implementation assumes single-host deployment:
- All QEMU processes run on the local machine
- `mgmt_ip` is always `127.0.0.1`
- Link socket connections use `127.0.0.1`
- PID management uses local process signals
- State tracking uses local filesystem (`~/.vmlab/`)

Multi-host support (remote QEMU launch via SSH, cross-host socket links, distributed state) is deferred to Phase 3.

---

## 2. SSH Port Support (newtron change)

### 2.1 tunnel.go Change

```go
// NewSSHTunnel dials SSH on host:<port> and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port == 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error)
```

Implementation change in `pkg/device/tunnel.go`:

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
pkg/vmlab/
├── vmlab.go          # Lab type, Deploy, Destroy, Status
├── node.go           # NodeConfig, resolved VM settings
├── qemu.go           # QEMU command builder, process management
├── link.go           # Link wiring, port allocation
├── iface_map.go      # Interface mapping (sequential, stride-4, custom)
├── disk.go           # COW overlay disk creation
├── boot.go           # SSH boot wait, readiness check
├── profile.go        # Profile patching (write ssh_port, console_port, mgmt_ip)
├── state.go          # state.json persistence
└── vmlab_test.go     # Unit tests

cmd/vmlab/
├── main.go           # Entry point, root command
├── cmd_deploy.go     # deploy subcommand
├── cmd_destroy.go    # destroy subcommand
├── cmd_status.go     # status subcommand
├── cmd_ssh.go        # ssh subcommand
├── cmd_console.go    # console subcommand
├── cmd_stop.go       # stop/start subcommands
└── cmd_provision.go  # provision subcommand (calls newtron)
```

---

## 4. Core Types (`pkg/vmlab/`)

### 4.1 Lab — Top-Level Orchestrator (`vmlab.go`)

```go
// Lab is the top-level vmlab orchestrator. It reads newtron spec files,
// resolves VM configuration, and manages QEMU processes.
type Lab struct {
    Name     string                         // lab name (from spec dir basename)
    SpecDir  string                         // path to spec directory
    StateDir string                         // ~/.vmlab/labs/<name>/
    Topology *spec.TopologySpecFile         // parsed topology.json
    Platform *spec.PlatformSpecFile         // parsed platforms.json
    Profiles map[string]*spec.DeviceProfile // per-device profiles
    Config   *VMLabConfig                   // from topology.json vmlab section
    Nodes    map[string]*NodeConfig         // resolved VM configs (keyed by device name)
    Links    []*LinkConfig                  // resolved link configs
    State    *LabState                      // runtime state (PIDs, ports, status)
    Force    bool                           // --force flag: destroy existing before deploy
}

// VMLabConfig mirrors spec.VMLabConfig with resolved defaults.
type VMLabConfig struct {
    LinkPortBase    int               // default: 20000
    ConsolePortBase int               // default: 30000
    SSHPortBase     int               // default: 40000
    Hosts           map[string]string // host name → IP
}
```

**Key methods:**

```go
// NewLab loads specs from specDir and returns a configured Lab.
// Initialization:
//   1. Set Name from filepath.Base(specDir)
//   2. Set StateDir to ~/.vmlab/labs/<name>/
//   3. Load topology.json, platforms.json, profiles/*.json
//   4. Resolve VMLabConfig with defaults (link_port_base=20000, etc.)
//   5. Resolve NodeConfig for each device (profile > platform > defaults)
//   6. Allocate ports (SSH, console per node) and links
// After NewLab, l.Nodes and l.Links are populated. Deploy() uses them
// to build QEMU commands and start processes.
func NewLab(specDir string) (*Lab, error)

// Deploy creates overlay disks, starts QEMU processes, waits for SSH,
// and patches profiles. Full deployment flow (see §12).
// Reads l.Force to handle stale state.
func (l *Lab) Deploy() error

// Destroy kills QEMU processes, removes overlays, cleans state,
// and restores profiles. Full teardown flow (see §13).
// Can be called with only Name populated (loads SpecDir from state.json).
func (l *Lab) Destroy() error

// Status returns the current state by loading state.json, then live-checking
// each PID via IsRunning() to update node status. Returns fresh LabState.
func (l *Lab) Status() (*LabState, error)

// FilterHost removes nodes not assigned to the given host name.
// Nodes without a vm_host profile field are retained (single-host mode).
// Also removes links that reference filtered-out nodes.
func (l *Lab) FilterHost(host string)

// Stop stops a single node by PID (SIGTERM then SIGKILL after 10s).
// Updates state.json to mark node as "stopped".
func (l *Lab) Stop(nodeName string) error

// Start restarts a stopped node by rebuilding the QEMU command and
// launching the process. Waits for SSH readiness. Updates state.json
// with new PID.
func (l *Lab) Start(nodeName string) error
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
    Index     int    // QEMU NIC index (0=mgmt, 1..N=data)
    NetdevID  string // "eth0", "eth1", ...
    Interface string // SONiC interface name ("Ethernet0", etc.) or "mgmt"
    LinkPort  int    // TCP socket port (0 for mgmt — uses user-mode networking)
    Listen    bool   // true = -netdev socket,listen=:PORT
    RemoteIP  string // connect target IP (127.0.0.1 or host IP from hosts map)
}
```

### 4.4 LinkConfig — Resolved Link (`link.go`)

```go
// LinkConfig represents a resolved link between two device NICs.
type LinkConfig struct {
    A    LinkEndpoint
    Z    LinkEndpoint
    Port int // TCP port for this link
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
// LabState is persisted to ~/.vmlab/labs/<name>/state.json.
type LabState struct {
    Name    string                  `json:"name"`
    Created time.Time               `json:"created"`
    SpecDir string                  `json:"spec_dir"`
    Nodes   map[string]*NodeState   `json:"nodes"`
    Links   []*LinkState            `json:"links"`
}

// NodeState tracks per-node runtime state.
type NodeState struct {
    PID            int    `json:"pid"`
    Status         string `json:"status"`          // "running", "stopped", "error"
    SSHPort        int    `json:"ssh_port"`
    ConsolePort    int    `json:"console_port"`
    OriginalMgmtIP string `json:"original_mgmt_ip"` // saved before patching, restored on destroy
}

// LinkState tracks per-link allocation.
type LinkState struct {
    A    string `json:"a"`    // "device:interface"
    Z    string `json:"z"`    // "device:interface"
    Port int    `json:"port"`
}
```

**State directory layout:**

```
~/.vmlab/labs/<name>/
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
| `-enable-kvm` | auto-detect `/dev/kvm` | `-enable-kvm` |
| `-drive file=<overlay>,if=virtio,format=qcow2` | `StateDir/disks/<name>.qcow2` | |
| `-nographic` | always | `-nographic` |
| `-serial tcp::<console_port>,server,nowait` | `Node.ConsolePort` | `-serial tcp::30000,server,nowait` |
| `-monitor unix:<mon_socket>,server,nowait` | `StateDir/qemu/<name>.mon` | |
| `-pidfile <pid_file>` | `StateDir/qemu/<name>.pid` | |
| `-netdev user,id=mgmt,hostfwd=tcp::<ssh_port>-:22` | `Node.SSHPort` | |
| `-device <nic_driver>,netdev=mgmt` | `Node.NICDriver` | `-device e1000,netdev=mgmt` |

Per data NIC (index 1..N):

| Argument | Listen Side | Connect Side |
|----------|-------------|--------------|
| `-netdev` | `socket,id=ethN,listen=:PORT` | `socket,id=ethN,connect=IP:PORT` |
| `-device` | `<nic_driver>,netdev=ethN` | `<nic_driver>,netdev=ethN` |

### 5.3 Process Management

```go
// StartNode launches the QEMU process for a node.
// Redirects stdout/stderr to logs/<name>.log.
// Returns after process is started (does not wait for boot).
func StartNode(node *NodeConfig, stateDir string) (int, error)

// StopNode sends SIGTERM to the QEMU process, then SIGKILL after 10s.
func StopNode(pid int) error

// IsRunning checks if a QEMU process is alive by PID.
func IsRunning(pid int) bool
```

### 5.4 Helper Functions

```go
// kvmAvailable returns true if /dev/kvm exists and is writable.
// Falls back to TCG (software emulation) if KVM is not available.
func kvmAvailable() bool

// probePort attempts net.Listen on the given port to check availability.
// Immediately closes the listener. Returns error if the port is in use.
func probePort(port int) error

// destroyExisting tears down a stale deployment found via state.json.
// Called by Deploy() when --force is set and a previous lab state exists.
// Wraps Lab.Destroy() on the loaded state.
func (l *Lab) destroyExisting(existing *LabState) error
```

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
// using the given interface map scheme.
//
// Examples (stride-4):
//   ResolveNICIndex("stride-4", "Ethernet0")  → 1
//   ResolveNICIndex("stride-4", "Ethernet4")  → 2
//   ResolveNICIndex("stride-4", "Ethernet8")  → 3
//
// Examples (sequential):
//   ResolveNICIndex("sequential", "Ethernet0") → 1
//   ResolveNICIndex("sequential", "Ethernet1") → 2
func ResolveNICIndex(interfaceMap, interfaceName string) (int, error)

// ResolveInterfaceName returns the SONiC interface name for a QEMU NIC index.
// Inverse of ResolveNICIndex.
//
// Examples (stride-4):
//   ResolveInterfaceName("stride-4", 1) → "Ethernet0"
//   ResolveInterfaceName("stride-4", 2) → "Ethernet4"
func ResolveInterfaceName(interfaceMap string, nicIndex int) string
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
// port allocations and NIC index assignments.
func AllocateLinks(
    links []*spec.TopologyLink,
    nodes map[string]*NodeConfig,
    config *VMLabConfig,
) ([]*LinkConfig, error)
```

**Algorithm:**

1. Iterate `topology.json` links in order
2. Assign port: `config.LinkPortBase + linkIndex`
3. Resolve NIC index for each endpoint via `ResolveNICIndex(node.InterfaceMap, iface)`
4. Determine listen vs connect: A side listens, Z side connects
5. For cross-host links: resolve connect IP from `config.Hosts[nodeZ.Host]`
6. For same-host links: connect IP = `127.0.0.1`
7. Attach NIC configs to corresponding `NodeConfig.NICs` slice
8. Return `[]*LinkConfig`

### 7.2 Port Allocation Summary

```
Link ports:      link_port_base + link_index      (20000, 20001, ...)
Console ports:   console_port_base + node_index   (30000, 30001, ...)
SSH ports:       ssh_port_base + node_index        (40000, 40001, ...)
```

Node index is determined by sorted device name order (deterministic).

### 7.3 NIC Assembly

After link allocation, each `NodeConfig.NICs` contains:

| Index | NetdevID | Role | Netdev Type |
|-------|----------|------|-------------|
| 0 | `mgmt` | Management | `user` (hostfwd SSH) |
| 1 | `eth1` | Data (first link) | `socket` (listen or connect) |
| 2 | `eth2` | Data (second link) | `socket` (listen or connect) |
| ... | ... | ... | ... |

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
`~/.vmlab/labs/<name>/disks/<device>.qcow2`.

`~` in `vm_image` paths is expanded to `$HOME` at resolution time.

---

## 9. Boot Wait (`boot.go`)

```go
// WaitForSSH polls SSH connectivity to host:port with the given credentials.
// Returns nil when SSH login succeeds, or error if timeout is reached.
// Polls every 5 seconds.
func WaitForSSH(host string, port int, user, pass string, timeout time.Duration) error
```

**Flow:**

1. Loop until timeout
2. Attempt `ssh.Dial("tcp", host:port, config)` with 5s dial timeout
3. On success: open session, run `echo ready`, close, return nil
4. On failure: sleep 5s, retry
5. On timeout: return `fmt.Errorf("SSH timeout after %s for %s:%d", timeout, host, port)`

---

## 10. Profile Patching (`profile.go`)

### 10.1 PatchProfiles

```go
// PatchProfiles updates device profile JSON files with vmlab runtime values.
// Called after successful VM deployment.
func PatchProfiles(lab *Lab) error
```

**Per-device patch:**

1. Read profile JSON from `<specDir>/profiles/<device>.json`
2. Unmarshal into `map[string]interface{}` (preserves all existing fields)
3. Save `OriginalMgmtIP` from current profile into `NodeState` (for restore on destroy)
4. Set `mgmt_ip` = `"127.0.0.1"` (single-host) or host IP (multi-host)
4. Set `ssh_port` = node's allocated SSH port
5. Set `console_port` = node's allocated console port
6. Set `ssh_user` / `ssh_pass` from resolved credentials (if not already set)
7. Marshal back with `json.MarshalIndent` (4-space indent)
8. Write to same path

### 10.2 RestoreProfiles

```go
// RestoreProfiles removes vmlab-written fields from profiles.
// Called during destroy to clean up.
func RestoreProfiles(lab *Lab) error
```

Removes: `ssh_port`, `console_port`. Resets `mgmt_ip` to the original value saved in `NodeState.OriginalMgmtIP` (captured during `PatchProfiles` before overwriting).

---

## 11. State Management (`state.go`)

```go
// LabDir returns the state directory path for a lab name.
//   ~/.vmlab/labs/<name>/
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
            return fmt.Errorf("vmlab: lab %s already deployed (created %s); use --force to redeploy",
                l.Name, existing.Created.Format(time.RFC3339))
        }
        // --force: destroy existing deployment first
        l.destroyExisting(existing)
    }

    // 1. Load specs
    topology, platforms, profiles := loadSpecs(l.SpecDir)

    // 2. Resolve vmlab config (with defaults)
    l.Config = resolveVMLabConfig(topology.VMLab)

    // 3. Resolve node configs (profile > platform > defaults)
    for name, device := range topology.Devices {
        profile := profiles[name]
        platform := platforms.Platforms[profile.Platform]
        l.Nodes[name] = ResolveNodeConfig(name, profile, platform)
    }

    // 4. Allocate ports (SSH, console per node; link ports per link)
    allocatePorts(l.Nodes, l.Config)  // sets SSHPort, ConsolePort on each node

    // 4a. Port conflict detection: probe each allocated port with net.Listen
    // to verify it's available before starting QEMU. This catches conflicts
    // with other vmlab instances or unrelated services early, rather than
    // failing mid-deploy with an opaque QEMU error.
    for _, node := range l.Nodes {
        for _, port := range []int{node.SSHPort, node.ConsolePort} {
            if err := probePort(port); err != nil {
                return fmt.Errorf("vmlab: port %d already in use: %w", port, err)
            }
        }
    }

    // 5. Allocate links (assigns NICs, ports, listen/connect)
    l.Links = AllocateLinks(topology.Links, l.Nodes, l.Config)

    // 6. Create state directory (~/.vmlab/labs/<name>/)
    os.MkdirAll(l.StateDir+"/qemu", 0755)
    os.MkdirAll(l.StateDir+"/disks", 0755)
    os.MkdirAll(l.StateDir+"/logs", 0755)

    // 7. Create overlay disks
    for name, node := range l.Nodes {
        overlay := l.StateDir + "/disks/" + name + ".qcow2"
        CreateOverlay(node.Image, overlay)
    }

    // 8. Build and start QEMU commands
    //    Start listen-side nodes first, then connect-side nodes
    for _, name := range sortedListenFirst(l.Nodes, l.Links) {
        pid := StartNode(l.Nodes[name], l.StateDir)
        l.State.Nodes[name] = &NodeState{PID: pid, Status: "running", ...}
    }

    // 9. Wait for SSH readiness (parallel, per node)
    var wg sync.WaitGroup
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

    // 10. Patch profiles (write ssh_port, console_port, mgmt_ip)
    PatchProfiles(l)

    // 11. Save state
    SaveState(l.State)

    return nil
}
```

**Start order:** Nodes with listen-side NICs start before nodes with
connect-side NICs. Within each group, nodes start in sorted name order.
This ensures socket listeners are ready before connectors attempt to dial.

**Mid-deploy failure recovery:** If a QEMU process fails to start (step 8)
or SSH readiness times out (step 9), Deploy() still saves state (step 11)
with the failed node marked as `"error"`. This leaves a valid state.json on
disk so that `vmlab destroy` can clean up all started nodes. The caller sees
a non-nil error. To retry, the user runs `vmlab destroy` then `vmlab deploy`
(or `vmlab deploy --force`, which calls `destroyExisting` automatically).
Partial deploys do **not** auto-rollback — explicit destroy is required.

---

## 13. Destroy Flow

Step-by-step pseudocode for `Lab.Destroy()`:

```go
func (l *Lab) Destroy() error {
    // 1. Load state
    state := LoadState(l.Name)

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

    // 3. Restore profiles (remove ssh_port, console_port, reset mgmt_ip)
    if err := RestoreProfiles(l); err != nil {
        errs = append(errs, fmt.Errorf("restore profiles: %w", err))
    }

    // 4. Remove state directory (includes overlay disks, logs, PID files)
    if err := RemoveState(l.Name); err != nil {
        errs = append(errs, fmt.Errorf("remove state: %w", err))
    }

    if len(errs) > 0 {
        return fmt.Errorf("vmlab: destroy had %d errors: %v", len(errs), errs)
    }
    return nil
}
```

### 13.1 Lab.Provision()

Called by `vmlab deploy --provision` or `vmlab provision`. Shells out to `newtron provision` for each device:

```go
// Provision runs newtron provisioning for all (or specified) devices in the lab.
// parallel controls concurrency: 1 = sequential, >1 = concurrent with semaphore.
func (l *Lab) Provision(parallel int) error {
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

**Why shell out:** vmlab is a separate binary from newtron. Rather than importing newtron's `pkg/network` (which would create a circular dependency), vmlab invokes the `newtron` CLI. This keeps the tools loosely coupled — vmlab only needs the `newtron` binary on PATH.

---

## 14. CLI Implementation (`cmd/vmlab/`)

### 14.1 Command Tree

Same Cobra pattern as `cmd/newtron/`. Root command with subcommands.

```go
// main.go
func main() {
    rootCmd := &cobra.Command{Use: "vmlab"}
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
    )

    rootCmd.Execute()
}
```

### 14.2 Subcommands

| Command | File | Flags | Description |
|---------|------|-------|-------------|
| `vmlab deploy` | `cmd_deploy.go` | `-S` (required), `--host`, `--force`, `--provision`, `--parallel` | Deploy VMs from spec |
| `vmlab destroy` | `cmd_destroy.go` | `--force` | Stop and remove all VMs |
| `vmlab status` | `cmd_status.go` | (none) | Show VM status table |
| `vmlab ssh <node>` | `cmd_ssh.go` | (none) | SSH to a VM |
| `vmlab console <node>` | `cmd_console.go` | (none) | Attach to serial console |
| `vmlab stop <node>` | `cmd_stop.go` | (none) | Stop a VM (preserves disk) |
| `vmlab start <node>` | `cmd_stop.go` | (none) | Start a stopped VM |
| `vmlab provision` | `cmd_provision.go` | `--device` | Run newtron provisioning |

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
            lab, err := vmlab.NewLab(specDir)
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
return fmt.Errorf("vmlab: start node %s: %w", name, err)
return fmt.Errorf("vmlab: create overlay %s: %w", overlayPath, err)
return fmt.Errorf("vmlab: allocate links: port %d conflict: %w", port, err)
```

### 15.2 Error Conditions

| Condition | Error | Recovery |
|-----------|-------|----------|
| No `vm_image` resolved | `"no vm_image for device %s (check platform or profile)"` | Set vm_image in platform or profile |
| Base image not found | `"vm_image not found: %s"` | Download or fix path |
| KVM not available | Warning logged, falls back to TCG | Install KVM or accept slower emulation |
| Port conflict | `"port %d already in use"` | Change port base or stop conflicting process |
| SSH boot timeout | `"SSH timeout after %s for %s:%d"` | Increase vm_boot_timeout or check image |
| QEMU process crash | `"QEMU exited with code %d (see %s)"` | Check logs/<node>.log |
| State not found | `"lab %s not found (no state.json)"` | Run deploy first |
| Profile read error | `"reading profile %s: %w"` | Check file permissions |

---

## Cross-References

### References to newtron LLD

| newtron LLD Section | How vmlab Relates |
|----------------------|-------------------|
| §3.1 `PlatformSpec` | vmlab adds VM fields (`vm_image`, `vm_memory`, etc.) to the shared spec type — see §1.1 |
| §3.1 `DeviceProfile` | vmlab adds per-device overrides (`ssh_port`, `console_port`) — see §1.2 |
| §3.1 `TopologySpecFile` | vmlab reads topology for device list, links, and VM host assignments |

### References to device LLD

| Device LLD Section | How vmlab Relates |
|--------------------|-------------------|
| §1 SSH Tunnel | Tunnel reads `SSHPort` written by vmlab profile patching (§10) |
| §5.1 `Device.Connect()` | Connection reads `SSHUser`/`SSHPass`/`SSHPort` from profiles that vmlab patches |

### References to newtest LLD

| newtest LLD Section | How vmlab Relates |
|---------------------|-------------------|
| §6.1 `DeployTopology` | newtest wraps `vmlab.NewLab()` + `vmlab.Lab.Deploy()` — see §4.1 |
| §6.2 `DestroyTopology` | newtest wraps `vmlab.Lab.Destroy()` — see §4.1 |
| §6.3 Platform Capability Check | newtest reads `PlatformSpec.Dataplane` (§1.1) to skip verify-ping |
| §4.5 Device connection | newtest relies on vmlab profile patching (§10) before connecting devices |

---

## Appendix A: Changelog

#### v6

| Area | Change |
|------|--------|
| **Multi-host Phase 3** | Marked `hosts` and `VMHost` as Phase 3 placeholder; documented single-host assumptions (§1.4) |
| **Stale State** | Deploy checks `state.json`, requires `--force` to overwrite (destroys first) (§12) |
| **RestoreProfiles** | Added `OriginalMgmtIP` to `NodeState`; saved before patching, restored on destroy (§4.5, §10) |
| **Custom Interface Map** | Added `vm_interface_map_custom` field to `PlatformSpec` for explicit SONiC→NIC mapping (§1.1, §6.1) |
| **Port Conflict Detection** | `net.Listen` probe before QEMU start (§12) |
| **QEMU Binary** | Documented `qemu-system-x86_64` from PATH (§5.1) |
| **Destroy Error Handling** | Continue-on-error with collected failures (§13) |
