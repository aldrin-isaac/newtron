# Newtron API Layer Design

## Problem

The service layer (`pkg/newtron/service/`) is a stateless facade — each method
connects to a device, does one thing, disconnects. Clients that need persistent
state (shell, newtrun) bypass the service layer entirely and use `node.Node` /
`node.Interface` directly. This means:

- The service layer isn't the real API boundary
- CLI imports `spec`, `network`, `node`, `audit`, `settings` directly
- No shared lifecycle management — each client re-implements connect/lock/apply/verify/save
- ChangeSets and CONFIG_DB entries leak into client code
- The Network→Node→Interface hierarchy is not preserved at the API boundary

## Design: `package newtron` — The API

The API lives at `pkg/newtron/` and provides three domain types that mirror
the internal hierarchy:

```
newtron.Network   wraps  network.Network    (spec hierarchy, device registry)
newtron.Node      wraps  node.Node          (device connection, CONFIG_DB)
newtron.Interface wraps  node.Interface     (interface ops, service binding)
```

Same names, same hierarchy, same method names. The only difference is the
package. Clients import `newtron`, never `node`, `network`, `spec`, `sonic`,
`audit`, or `settings`.

### Architecture

```
┌──────────────────────────────────────────────────────────┐
│  Clients (CLI, shell, newtrun)                           │
│  Import: pkg/newtron only                                │
│  See: newtron.Network → newtron.Node → newtron.Interface │
│  Never see: ChangeSet, Entry, ConfigDB, sonic.*          │
└────────────┬─────────────────────────────────────────────┘
             │
┌────────────▼─────────────────────────────────────────────┐
│  API Layer (pkg/newtron/)                                 │
│  package newtron                                          │
│  Network, Node, Interface — thin wrappers                 │
│  Ops return error — Node accumulates changes internally   │
│  All response types owned here                            │
└────────────┬─────────────────────────────────────────────┘
             │  internal
┌────────────▼─────────────────────────────────────────────┐
│  Domain Layer (pkg/newtron/network/, network/node/)       │
│  network.Network, node.Node, node.Interface               │
│  ChangeSet, CompositeConfig, SpecProvider                 │
│  Same code paths for physical and abstract modes.         │
│  Unchanged by this design.                                │
└──────────────────────────────────────────────────────────┘
```

---

## Design Principles — All Preserved

| Principle | How It's Preserved |
|-----------|-------------------|
| **Network→Node→Interface** | `net.Connect()` → `*newtron.Node`, `n.Interface()` → `*newtron.Interface`. Explicit hierarchy. |
| **Interface is the point of service** | `iface.ApplyService()` lives on Interface. The interface is the entity being configured. |
| **Abstraction boundaries** | Interface knows its name — callers express intent, not identity. Same rule at both layers. |
| **Network is source of truth** | ConfigDB is ground reality. newtron.Node reads/writes it. No change. |
| **Single-owner principle** | `*_ops.go` files unchanged. Each CONFIG_DB table has one owner. |
| **Verb-first naming** | `n.CreateVLAN()`, `iface.ApplyService()`, `iface.SetVRF()`, `n.SetupEVPN()`. |
| **Operational symmetry** | Create↔Delete, Bind↔Unbind, Setup↔Teardown — all forward/reverse pairs preserved. |
| **Domain-intent naming** | `newtron.Node` not `SessionHandle`. `newtron.Interface` not `InterfaceProxy`. |
| **Abstract Node shared code paths** | `net.Abstract()` returns same `*newtron.Node`. Same ops, same delegation to `node.Node`. |
| **Separation of concerns** | API layer = lifecycle + type boundary. Domain layer = CONFIG_DB logic. |

---

## Spec Flow

Specs flow through the Network→Node→Interface hierarchy. The resolution
chain is entirely internal — clients see resolved results through ops methods
and read accessors, never raw `spec.*` types.

```
NetworkSpec (network.json)
  → ZoneSpec (per-zone overrides)
    → DeviceProfile (per-device overrides)
      → ResolvedSpecs (merged, all 8 maps)
        → SpecProvider on node.Node
          → node.Interface accesses via i.node.GetService(name)
```

When a client calls:
```go
iface.ApplyService(ctx, "transit", opts)
```

Internally:
1. `newtron.Interface.ApplyService` delegates to `node.Interface.ApplyService`
2. `node.Interface` calls `i.node.GetService("transit")` via SpecProvider
3. SpecProvider is `ResolvedSpecs` — merged from network + zone + profile
4. Returns the resolved `*spec.ServiceSpec` (never visible to client)
5. Service entries are generated from the spec

Spec authoring is a Network-level concern (specs are network-wide):
```go
net.ListServices()                         // all service specs in hierarchy
net.ShowService("transit")                 // → *newtron.ServiceDetail
net.CreateService(newtron.CreateServiceRequest{...})
```

The response types (`ServiceDetail`, `IPVPNDetail`, etc.) are owned by
`package newtron` — no `spec.*` types leak.

---

## Types

### newtron.Network

```go
// Network is the top-level context. It loads the spec hierarchy,
// resolves profiles, and provides access to devices.
//
// Network→Node→Interface is the domain hierarchy. Network owns
// spec authoring. Node owns device operations. Interface owns
// interface operations.
type Network struct {
    internal *network.Network   // unexported
    auth     *auth.Checker      // optional permission checker
}

// LoadNetwork creates a Network from a spec directory.
// Loads network.json, resolves zone→profile hierarchy.
func LoadNetwork(specDir string) (*Network, error)

// SetAuth configures permission checking for write operations.
func (net *Network) SetAuth(checker *auth.Checker)
```

### newtron.Node

```go
// Node is a device within a Network — either connected to a physical
// device (backed by Redis) or operating in abstract mode (shadow ConfigDB).
//
// Ops methods return error. Pending changes accumulate internally.
// Use PendingPreview() to see what would change, Commit() to apply,
// Rollback() to discard.
type Node struct {
    net      *Network
    internal *node.Node         // unexported
    abstract bool               // mirrors node.offline
    pending  []*node.ChangeSet  // accumulated, uncommitted changes
    history  []*node.ChangeSet  // committed changes (for re-verification)
}
```

### newtron.Interface

```go
// Interface is a scoped interface context within a Node.
// The interface is the point of service delivery — where abstract
// service intent meets physical infrastructure.
type Interface struct {
    node     *Node              // parent — preserves hierarchy
    internal *node.Interface    // unexported
}
```

### WriteResult

```go
// WriteResult is the outcome of a committed set of changes.
// All fields are value types owned by package newtron.
type WriteResult struct {
    Preview      string
    ChangeCount  int
    Applied      bool
    Verified     bool
    Saved        bool
    Verification *VerificationResult
}

type VerificationResult struct {
    Passed int
    Failed int
    Errors []VerificationError
}

type VerificationError struct {
    Table, Key, Field, Expected, Actual string
}
```

---

## How Connect Works

### Physical: `net.Connect()`

```go
net, _ := newtron.LoadNetwork(specDir)
n, _ := net.Connect(ctx, "leaf1")
defer n.Close()
```

Internally:
1. Network resolves "leaf1" → profile, SSH credentials, Redis address
2. Merges specs: network → zone → profile → `ResolvedSpecs`
3. Creates internal `node.Node` with `ResolvedSpecs` as `SpecProvider`
4. Establishes SSH tunnel + Redis connections
5. Loads ConfigDB from Redis
6. Wraps in `*newtron.Node{net: net, internal: node}`
7. Returns ready-to-use Node

The client doesn't see SSH tunnels, Redis connections, ConfigDB loading,
spec resolution, or profile merging.

### Abstract: `net.Abstract()`

```go
n, _ := net.Abstract("leaf1")
defer n.Close()  // no-op
```

Internally:
1. Network resolves "leaf1" → profile, specs (no SSH/Redis)
2. Creates `node.NewAbstract(sp, name, profile, resolved)` — empty shadow ConfigDB
3. Wraps in `*newtron.Node{abstract: true}`
4. Returns ready-to-use Node

Same ops, same methods. The mode difference is internal.

---

## Write Model: ChangeSets Are Server-Side

Ops methods return `error`. The Node accumulates changes internally.
The client controls the write lifecycle at the Node level.

### Ops Methods (return error)

```go
// Device-scoped (on newtron.Node):
err := n.CreateVLAN(ctx, 100, newtron.VLANConfig{Name: "Servers"})
err := n.SetupEVPN(ctx, sourceIP)

// Interface-scoped (on newtron.Interface):
err := iface.ApplyService(ctx, "transit", newtron.ApplyServiceOpts{IPAddress: "10.1.1.1/30"})
err := iface.SetVRF(ctx, "Vrf_CUST1")
```

Internally, each ops method:
1. Delegates to the internal `node.Node` / `node.Interface` method
2. Gets back a `*node.ChangeSet` (server-side only)
3. Appends it to `n.pending`
4. Returns only the error

```go
func (n *Node) CreateVLAN(ctx context.Context, id int, config VLANConfig) error {
    cs, err := n.internal.CreateVLAN(ctx, id, node.VLANConfig{Name: config.Name})
    if err != nil {
        return err
    }
    n.pending = append(n.pending, cs)
    return nil
}

func (i *Interface) ApplyService(ctx context.Context, service string, opts ApplyServiceOpts) error {
    cs, err := i.internal.ApplyService(ctx, service, node.ApplyServiceOpts{IPAddress: opts.IPAddress})
    if err != nil {
        return err
    }
    i.node.pending = append(i.node.pending, cs)
    return nil
}
```

### Preview, Commit, Rollback

```go
// PendingPreview returns a formatted preview of all uncommitted changes.
func (n *Node) PendingPreview() string

// PendingCount returns the number of pending CONFIG_DB changes.
func (n *Node) PendingCount() int

// Commit applies and verifies all pending changes against the device.
// Moves pending → history. Returns WriteResult.
// Abstract mode: no-op (shadow already updated during ops).
func (n *Node) Commit(ctx context.Context) (*WriteResult, error)

// Rollback discards all pending changes without applying.
// Physical: no Redis writes happened, just clears the list.
// Abstract: shadow was already updated (append-only by design), no-op.
func (n *Node) Rollback()
```

---

## Three Client Patterns

### CLI — One-Shot via Network Convenience

```go
net, _ := newtron.LoadNetwork(specDir)

// Ephemeral: connect → lock → op → commit → save → close
result, err := net.ApplyService(ctx, newtron.ApplyServiceRequest{
    Device:    "leaf1",
    Interface: "Ethernet0",
    Service:   "transit",
    IPAddress: "10.1.1.1/30",
}, newtron.ExecOpts{Execute: true})

fmt.Print(result.Preview)
```

### Shell — Interactive Two-Phase

```go
net, _ := newtron.LoadNetwork(specDir)
n, _ := net.Connect(ctx, "leaf1")
defer n.Close()

// Select interface
iface, _ := n.Interface("Ethernet0")

// Write with preview/confirm
n.Lock()
err := iface.ApplyService(ctx, "transit", newtron.ApplyServiceOpts{})
fmt.Println(n.PendingPreview())
if confirm() {
    result, _ := n.Commit(ctx)
    n.Save(ctx)
}
n.Unlock()
```

### newtrun — Automated Multi-Device

```go
net, _ := newtron.LoadNetwork(specDir)

// Connect all devices at startup
nodes := make(map[string]*newtron.Node)
for _, name := range deviceNames {
    n, _ := net.Connect(ctx, name)
    nodes[name] = n
}

// Write step
n := nodes["leaf1"]
result, _ := n.Execute(ctx, newtron.ExecOpts{Execute: true, NoSave: true},
    func(ctx context.Context) error {
        iface, _ := n.Interface("Ethernet0")
        return iface.ApplyService(ctx, "transit", newtron.ApplyServiceOpts{})
    })

// Later: verify all changes applied so far
vr, _ := n.VerifyCommitted(ctx)
```

---

## Network API

### Node Construction

```go
func (net *Network) Connect(ctx context.Context, device string) (*Node, error)
func (net *Network) Abstract(device string) (*Node, error)
func (net *Network) ListNodes() []string
```

### Spec Authoring (network-level concern)

```go
// Services
func (net *Network) ListServices() ([]ServiceSummary, error)
func (net *Network) ShowService(name string) (*ServiceDetail, error)
func (net *Network) CreateService(req CreateServiceRequest) error
func (net *Network) DeleteService(name string) error

// IPVPNs
func (net *Network) ListIPVPNs() (map[string]*IPVPNDetail, error)
func (net *Network) ShowIPVPN(name string) (*IPVPNDetail, error)
func (net *Network) CreateIPVPN(req CreateIPVPNRequest) error
func (net *Network) DeleteIPVPN(name string) error

// MACVPNs
func (net *Network) ListMACVPNs() (map[string]*MACVPNDetail, error)
func (net *Network) ShowMACVPN(name string) (*MACVPNDetail, error)
func (net *Network) CreateMACVPN(req CreateMACVPNRequest) error
func (net *Network) DeleteMACVPN(name string) error

// QoS Policies
func (net *Network) ListQoSPolicies() ([]string, error)
func (net *Network) ShowQoSPolicy(name string) (*QoSPolicyDetail, error)
func (net *Network) CreateQoSPolicy(req CreateQoSPolicyRequest) error
func (net *Network) DeleteQoSPolicy(name string) error
func (net *Network) AddQoSQueue(req AddQoSQueueRequest) error
func (net *Network) RemoveQoSQueue(policy string, queueID int) error

// Filters
func (net *Network) ListFilters() ([]string, error)
func (net *Network) ShowFilter(name string) (*FilterDetail, error)
func (net *Network) CreateFilter(req CreateFilterRequest) error
func (net *Network) DeleteFilter(name string) error
func (net *Network) AddFilterRule(req AddFilterRuleRequest) error
func (net *Network) RemoveFilterRule(filter string, seq int) error

// Route Policies
func (net *Network) ListRoutePolicies() ([]string, error)
func (net *Network) ListPrefixLists() ([]string, error)

// Platforms
func (net *Network) ListPlatforms() (map[string]*PlatformDetail, error)
func (net *Network) ShowPlatform(name string) (*PlatformDetail, error)
func (net *Network) GetAllFeatures() []string
func (net *Network) GetFeatureDependencies(feature string) []string
func (net *Network) GetUnsupportedDueTo(feature string) []string
func (net *Network) PlatformSupportsFeature(platform, feature string) bool
```

### Ephemeral Convenience Methods (CLI one-shot)

Every write operation has a Network-level convenience method that creates
an ephemeral Node internally:

```go
func (net *Network) ApplyService(ctx context.Context, req ApplyServiceRequest, opts ExecOpts) (*WriteResult, error)
func (net *Network) RemoveService(ctx context.Context, req RemoveServiceRequest, opts ExecOpts) (*WriteResult, error)
func (net *Network) CreateVLAN(ctx context.Context, req CreateVLANRequest, opts ExecOpts) (*WriteResult, error)
// ... all write operations
```

Implementation pattern:
```go
func (net *Network) ApplyService(ctx context.Context, req ApplyServiceRequest, opts ExecOpts) (*WriteResult, error) {
    n, err := net.Connect(ctx, req.Device)
    if err != nil { return nil, err }
    defer n.Close()
    return n.Execute(ctx, opts, func(ctx context.Context) error {
        iface, err := n.Interface(req.Interface)
        if err != nil { return err }
        return iface.ApplyService(ctx, req.Service, req.Opts)
    })
}
```

### Provisioning

```go
func (net *Network) GenerateDeviceComposite(device string) (*CompositeInfo, error)
func (net *Network) ProvisionDevices(ctx context.Context, devices []string, opts ExecOpts) ([]ProvisionResult, error)
```

---

## Node API

### Lifecycle

```go
func (n *Node) Name() string
func (n *Node) IsAbstract() bool
func (n *Node) Lock() error
func (n *Node) Unlock()
func (n *Node) Save(ctx context.Context) error
func (n *Node) Refresh(ctx context.Context) error
func (n *Node) RefreshWithRetry(ctx context.Context, timeout time.Duration) error
func (n *Node) Close() error

// One-shot write: lock → fn → commit → save → unlock.
func (n *Node) Execute(ctx context.Context, opts ExecOpts,
    fn func(ctx context.Context) error) (*WriteResult, error)
```

### Pending Change Management

```go
func (n *Node) PendingPreview() string
func (n *Node) PendingCount() int
func (n *Node) Commit(ctx context.Context) (*WriteResult, error)
func (n *Node) Rollback()
func (n *Node) VerifyCommitted(ctx context.Context) (*VerificationResult, error)
```

### Interface Access

```go
func (n *Node) Interface(name string) (*Interface, error)
func (n *Node) ListInterfaces() []string
```

### Device-Level Write Ops

All return `error`. Node accumulates changes internally.

```go
// VLAN
func (n *Node) CreateVLAN(ctx context.Context, id int, config VLANConfig) error
func (n *Node) DeleteVLAN(ctx context.Context, id int) error
func (n *Node) AddVLANMember(ctx context.Context, id int, iface string, tagged bool) error
func (n *Node) RemoveVLANMember(ctx context.Context, id int, iface string) error
func (n *Node) ConfigureSVI(ctx context.Context, id int, config SVIConfig) error
func (n *Node) RemoveSVI(ctx context.Context, id int) error

// VRF
func (n *Node) CreateVRF(ctx context.Context, name string, config VRFConfig) error
func (n *Node) DeleteVRF(ctx context.Context, name string) error
func (n *Node) AddVRFInterface(ctx context.Context, vrf, iface string) error
func (n *Node) RemoveVRFInterface(ctx context.Context, vrf, iface string) error

// IPVPN
func (n *Node) BindIPVPN(ctx context.Context, vrf, ipvpn string) error
func (n *Node) UnbindIPVPN(ctx context.Context, vrf string) error

// BGP
func (n *Node) ConfigureBGP(ctx context.Context) error
func (n *Node) RemoveBGPGlobals(ctx context.Context) error
func (n *Node) AddBGPNeighbor(ctx context.Context, config BGPNeighborConfig) error
func (n *Node) RemoveBGPNeighbor(ctx context.Context, ip string) error

// Static Routes
func (n *Node) AddStaticRoute(ctx context.Context, vrf, prefix, nexthop string, metric int) error
func (n *Node) RemoveStaticRoute(ctx context.Context, vrf, prefix string) error

// EVPN
func (n *Node) SetupEVPN(ctx context.Context, sourceIP string) error
func (n *Node) TeardownEVPN(ctx context.Context) error

// ACL
func (n *Node) CreateACLTable(ctx context.Context, name string, config ACLTableConfig) error
func (n *Node) DeleteACLTable(ctx context.Context, name string) error
func (n *Node) AddACLRule(ctx context.Context, acl string, seq int, config ACLRuleConfig) error
func (n *Node) RemoveACLRule(ctx context.Context, acl string, seq int) error

// PortChannel
func (n *Node) CreatePortChannel(ctx context.Context, name string, config PortChannelConfig) error
func (n *Node) DeletePortChannel(ctx context.Context, name string) error
func (n *Node) AddPortChannelMember(ctx context.Context, pc, member string) error
func (n *Node) RemovePortChannelMember(ctx context.Context, pc, member string) error

// Baseline
func (n *Node) ConfigureLoopback(ctx context.Context) error
func (n *Node) RemoveLoopback(ctx context.Context) error

// Device metadata
func (n *Node) SetDeviceMetadata(ctx context.Context, fields map[string]string) error

// QoS
func (n *Node) ApplyQoS(ctx context.Context, iface, policy string) error
func (n *Node) RemoveQoS(ctx context.Context, iface string) error

// Cleanup
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*CleanupSummary, error)
```

### Device-Level Read Ops

```go
func (n *Node) DeviceInfo() (*DeviceInfo, error)
func (n *Node) ShowVLAN(id int) (*VLANDetail, error)
func (n *Node) ShowVRF(name string) (*VRFDetail, error)
func (n *Node) ShowPortChannel(name string) (*PortChannelDetail, error)
func (n *Node) ListVLANs() ([]int, error)
func (n *Node) ListVRFs() ([]string, error)
func (n *Node) ListPortChannels() ([]string, error)
func (n *Node) HealthCheck(ctx context.Context) (*HealthReport, error)
func (n *Node) ShowBGPNeighbors() ([]BGPNeighborInfo, error)
func (n *Node) ShowEVPN() (*EVPNDetail, error)
func (n *Node) ListACLTables() ([]string, error)
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
func (n *Node) CheckBGPSessions(ctx context.Context) ([]HealthCheckResult, error)
```

### SSH / Device Management (physical only)

```go
func (n *Node) ExecCommand(ctx context.Context, cmd string) (string, error)
func (n *Node) ConfigReload(ctx context.Context) error
func (n *Node) ApplyFRRDefaults(ctx context.Context) error
func (n *Node) RestartService(ctx context.Context, name string) error
```

### Abstract Mode Only

```go
func (n *Node) RegisterPort(name string, fields map[string]string)
func (n *Node) BuildComposite() *CompositeInfo
```

### Composite Delivery (physical)

```go
func (n *Node) DeliverComposite(ctx context.Context, composite *CompositeInfo, mode CompositeMode) (*DeliveryResult, error)
func (n *Node) VerifyComposite(ctx context.Context, composite *CompositeInfo) (*VerificationResult, error)
```

---

## Interface API

### Read

```go
func (i *Interface) Name() string
func (i *Interface) AdminStatus() string
func (i *Interface) OperStatus() string
func (i *Interface) Speed() string
func (i *Interface) MTU() string
func (i *Interface) IPAddresses() []string
func (i *Interface) VRF() string
func (i *Interface) ServiceName() string
func (i *Interface) HasService() bool
func (i *Interface) Description() string
func (i *Interface) IngressACL() string
func (i *Interface) EgressACL() string
func (i *Interface) IsPortChannelMember() bool
func (i *Interface) PortChannelParent() string
func (i *Interface) PortChannelMembers() []string
func (i *Interface) VLANMembers() []string
```

### Write (return error, Node accumulates)

```go
func (i *Interface) ApplyService(ctx context.Context, service string, opts ApplyServiceOpts) error
func (i *Interface) RemoveService(ctx context.Context) error
func (i *Interface) RefreshService(ctx context.Context) error
func (i *Interface) SetIP(ctx context.Context, ip string) error
func (i *Interface) RemoveIP(ctx context.Context, ip string) error
func (i *Interface) SetVRF(ctx context.Context, vrf string) error
func (i *Interface) BindACL(ctx context.Context, acl, direction string) error
func (i *Interface) UnbindACL(ctx context.Context, acl, direction string) error
func (i *Interface) BindMACVPN(ctx context.Context, macvpn string) error
func (i *Interface) UnbindMACVPN(ctx context.Context) error
func (i *Interface) AddBGPNeighbor(ctx context.Context, config BGPNeighborConfig) error
func (i *Interface) RemoveBGPNeighbor(ctx context.Context, ip string) error
func (i *Interface) Set(ctx context.Context, property, value string) error
```

---

## Abstract Mode: Shared Code Paths

| node.Node Mechanism | newtron.Node Equivalent | What Happens |
|---|---|---|
| `NewAbstract()` | `net.Abstract(device)` | Empty shadow ConfigDB, offline=true |
| `New() + Connect()` | `net.Connect(ctx, device)` | SSH + Redis, ConfigDB from Redis |
| `precondition()` skips connected/locked | Unchanged — newtron.Node delegates to node.Node | |
| `op()` offline: ApplyEntries + accumulate | Unchanged — same delegation, newtron.Node captures ChangeSet | |
| `applyShadow(cs)` | Unchanged — same delegation | |
| `RegisterPort()` | `n.RegisterPort(name, fields)` → `node.RegisterPort()` | |
| `BuildComposite()` | `n.BuildComposite()` → wraps `node.BuildComposite()` as `*CompositeInfo` | |

**No changes to any `*_ops.go` file.** Every Node and Interface method
works exactly as before. The API layer is pure lifecycle + type boundary.

### topology.go — Unchanged

topology.go is internal (`pkg/newtron/network/`). It continues to use
`node.NewAbstract()` directly. The API layer wraps the *result* of
provisioning, not the internal machinery.

```go
// pkg/newtron/provision.go
func (net *Network) GenerateDeviceComposite(device string) (*CompositeInfo, error) {
    tp := network.NewTopologyProvisioner(net.internal)
    composite, err := tp.GenerateDeviceComposite(device)
    if err != nil { return nil, err }
    return wrapComposite(composite), nil
}
```

---

## Standalone Operations

Settings and audit are not network-scoped or device-scoped.
They are package-level functions:

```go
// Settings
func LoadSettings() (*UserSettings, error)
func SaveSettings(us *UserSettings) error
func SettingsPath() string

// Audit
func QueryAuditLog(filter AuditFilter) ([]AuditEvent, error)
```

---

## Response Types (all owned by package newtron)

```go
// Service type constants
const (
    ServiceTypeEVPNIRB     = "evpn-irb"
    ServiceTypeEVPNBridged = "evpn-bridged"
    ServiceTypeEVPNRouted  = "evpn-routed"
    ServiceTypeIRB         = "irb"
    ServiceTypeBridged     = "bridged"
    ServiceTypeRouted      = "routed"
)

// Spec details
type ServiceDetail struct { ... }
type IPVPNDetail struct { ... }
type MACVPNDetail struct { ... }
type QoSPolicyDetail struct { ... }
type FilterDetail struct { ... }
type PlatformDetail struct { ... }

// Device reads
type DeviceInfo struct { ... }
type VLANDetail struct { ... }
type VRFDetail struct { ... }
type PortChannelDetail struct { ... }
type BGPNeighborInfo struct { ... }
type EVPNDetail struct { ... }
type RouteEntry struct { ... }

// Health
type HealthReport struct { ... }
type HealthCheckResult struct { ... }

// Composite
type CompositeInfo struct {
    DeviceName string
    EntryCount int
    Tables     map[string]int
    internal   *node.CompositeConfig  // unexported
}

type CompositeMode int
const (
    CompositeOverwrite CompositeMode = iota
    CompositeMerge
)

type DeliveryResult struct { Applied int }
type CleanupSummary struct { ... }

// Config types (for write ops)
type VLANConfig struct { Name string }
type SVIConfig struct { ... }
type VRFConfig struct { ... }
type BGPNeighborConfig struct { ... }
type ACLTableConfig struct { ... }
type ACLRuleConfig struct { ... }
type PortChannelConfig struct { ... }
type ApplyServiceOpts struct { IPAddress string }

// Exec options
type ExecOpts struct {
    Execute bool
    NoSave  bool
}

// Audit & Settings
type AuditFilter struct { ... }
type AuditEvent struct { ... }
type UserSettings struct { ... }
```

---

## Client Migration

### CLI (cmd/newtron/)

```go
// main.go — before:
app.net, _ = network.NewNetwork(specDir)
app.svc = service.New(app.net, app.permChecker)
result, _ := app.svc.ApplyService(ctx, req, opts)
fmt.Print(result.ChangeSet.Preview())

// main.go — after:
app.net, _ = newtron.LoadNetwork(specDir)
app.net.SetAuth(app.permChecker)
result, _ := app.net.ApplyService(ctx, req, opts)
fmt.Print(result.Preview)
```

All `cmd_*.go` switch to `newtron.*` types:
- `spec.ServiceTypeEVPNIRB` → `newtron.ServiceTypeEVPNIRB`
- `*spec.QoSPolicy` → `*newtron.QoSPolicyDetail`
- `*network.HealthReport` → `*newtron.HealthReport`
- `audit.Query(filter)` → `newtron.QueryAuditLog(filter)`
- `settings.Load()` → `newtron.LoadSettings()`

### Shell (cmd/newtron/shell.go)

```go
type Shell struct {
    node      *newtron.Node       // was: dev *node.Node
    currentIF *newtron.Interface  // was: currentIntf *node.Interface
}

s.node, _ = app.net.Connect(ctx, deviceName)
defer s.node.Close()

s.currentIF, _ = s.node.Interface("Ethernet0")
fmt.Println(s.currentIF.AdminStatus())

s.node.Lock()
err := s.currentIF.ApplyService(ctx, "transit", newtron.ApplyServiceOpts{})
fmt.Println(s.node.PendingPreview())
if confirm() {
    result, _ := s.node.Commit(ctx)
    s.node.Save(ctx)
}
s.node.Unlock()
```

### newtrun (pkg/newtrun/)

```go
type Runner struct {
    Net   *newtron.Network
    Nodes map[string]*newtron.Node
}

// Startup
r.Net, _ = newtron.LoadNetwork(specDir)
for _, name := range deviceNames {
    r.Nodes[name], _ = r.Net.Connect(ctx, name)
}

// Write step
n := r.Nodes["leaf1"]
result, _ := n.Execute(ctx, newtron.ExecOpts{Execute: true, NoSave: true},
    func(ctx context.Context) error {
        iface, _ := n.Interface("Ethernet0")
        return iface.ApplyService(ctx, "transit", newtron.ApplyServiceOpts{})
    })

// Verify
vr, _ := n.VerifyCommitted(ctx)
```

---

## Package Layout

```
pkg/newtron/                  ← package newtron (THE API)
  network.go                  ← Network type, LoadNetwork, Connect, Abstract
  node.go                     ← Node type, lifecycle, pending/commit, device ops
  interface.go                ← Interface type, ops, reads
  types.go                    ← response types, config types, constants
  spec_ops.go                 ← spec CRUD methods on Network
  platform_ops.go             ← platform feature methods on Network
  provision.go                ← provisioning methods on Network
  ephemeral_ops.go            ← convenience one-shot methods on Network
  audit.go                    ← QueryAuditLog (standalone)
  settings.go                 ← LoadSettings, SaveSettings (standalone)
  network/                    ← internal: network.Network, topology
  network/node/               ← internal: node.Node, node.Interface, *_ops.go
  device/sonic/               ← internal: ConfigDB, SSH, Redis
  spec/                       ← internal: spec types
  audit/                      ← internal: audit implementation
  settings/                   ← internal: settings implementation
  auth/                       ← internal: permission checking
```

## What Stays the Same

- **All `*_ops.go` files** — unchanged. Domain logic.
- **`node.go`** — unchanged. Constructors, precondition, op, applyShadow.
- **`changeset.go`** — unchanged. ChangeSet type and methods.
- **`composite.go`** — unchanged. CompositeBuilder, CompositeConfig.
- **`network/network.go`** — unchanged. Spec loading, device registry.
- **`topology.go`** — unchanged. Abstract node provisioning.
- **All spec types** — unchanged. Used internally.
- **`sonic/` package** — unchanged.

## What Changes

| File | Change |
|------|--------|
| `pkg/newtron/network.go` | **NEW** — newtron.Network wrapping network.Network |
| `pkg/newtron/node.go` | **NEW** — newtron.Node wrapping node.Node, pending/history |
| `pkg/newtron/interface.go` | **NEW** — newtron.Interface wrapping node.Interface |
| `pkg/newtron/types.go` | **NEW** — all response types, config types, constants |
| `pkg/newtron/spec_ops.go` | **NEW** — spec CRUD on Network |
| `pkg/newtron/platform_ops.go` | **NEW** — platform features on Network |
| `pkg/newtron/provision.go` | **NEW** — provisioning on Network |
| `pkg/newtron/ephemeral_ops.go` | **NEW** — one-shot convenience on Network |
| `pkg/newtron/audit.go` | **NEW** — wraps audit package |
| `pkg/newtron/settings.go` | **NEW** — wraps settings package |
| `pkg/newtron/service/` | **DELETED** — replaced by pkg/newtron/ API |
| `cmd/newtron/main.go` | Use newtron.LoadNetwork, newtron types |
| `cmd/newtron/shell.go` | Use *newtron.Node + *newtron.Interface |
| `cmd/newtron/cmd_*.go` | Use newtron types; remove all internal imports |
| `cmd/newtron/cmd_provision.go` | Use n.ExecCommand; *newtron.CompositeInfo |
| `pkg/newtrun/runner.go` | Use newtron.Network + map[string]*newtron.Node |
| `pkg/newtrun/steps.go` | Use n.Execute instead of dev.ExecuteOp |

## Target Import State

| File | Imports |
|------|---------|
| `cmd/newtron/main.go` | `newtron`, `auth` (construction), stdlib |
| `cmd/newtron/cmd_*.go` | `newtron` + stdlib + `cli` + `util` |
| `cmd/newtron/shell.go` | `newtron` + stdlib + `cli` |
| `pkg/newtrun/runner.go` | `newtron` + stdlib |
| `pkg/newtrun/steps.go` | `newtron` + stdlib |

## Verification

```bash
go build -o bin/newtron ./cmd/newtron
go build -o bin/newtrun ./cmd/newtrun
go build -o bin/newtlab ./cmd/newtlab
go vet ./...
go test ./... -count=1

# No leaked imports in clients:
grep -rn 'network/node' cmd/newtron/       # empty
grep -rn 'newtron/spec' cmd/newtron/       # empty
grep -rn 'newtron/network"' cmd/newtron/   # empty
grep -rn 'newtron/audit' cmd/newtron/      # empty
grep -rn 'newtron/settings' cmd/newtron/   # empty
grep -rn 'network/node' pkg/newtrun/       # empty
grep -rn 'newtron/spec' pkg/newtrun/       # empty
```

## Implementation Order

1. **pkg/newtron/types.go** — Response types, config types, constants
2. **pkg/newtron/network.go** — Network type, LoadNetwork, SetAuth, ListNodes
3. **pkg/newtron/node.go** — Node type, lifecycle, pending/commit/rollback, all device ops
4. **pkg/newtron/interface.go** — Interface type, all interface ops, read methods
5. **pkg/newtron/spec_ops.go** — Spec CRUD on Network (convert returns)
6. **pkg/newtron/platform_ops.go** — Platform features on Network
7. **pkg/newtron/provision.go** — Provisioning on Network
8. **pkg/newtron/ephemeral_ops.go** — One-shot convenience on Network
9. **pkg/newtron/audit.go, settings.go** — Standalone wrappers
10. **cmd/newtron/** — Migrate all CLI files to newtron types
11. **pkg/newtrun/** — Migrate runner and steps
12. **Delete pkg/newtron/service/** — replaced
13. **Verify** — Build, vet, test, grep
