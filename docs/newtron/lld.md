# Newtron Low-Level Design (LLD)

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md). For the network-level architecture, see [newtron HLD](hld.md). For the device connection layer (SSH tunnels, Redis clients), see [Device Layer LLD](device-lld.md).


---

## 1. Spec vs Config: Fundamental Architecture

Newtron separates **specification** (declarative intent in `pkg/spec`) from **configuration** (imperative device state in `pkg/device`). The `pkg/network` layer translates specs into config. See HLD §2 for the full rationale.

| Layer | Package | Data | Edited by |
|-------|---------|------|-----------|
| Specification | `pkg/spec` | `specs/*.json` — policies, references | Network architects |
| Translation | `pkg/network` | In-memory — ChangeSet generation | Auto (newtron) |
| Configuration | `pkg/device` | Redis CONFIG_DB — concrete values | Auto (newtron) |

## 2. Package Structure

```
newtron/
├── cmd/
│   ├── newtron/                     # CLI application
│   │   ├── main.go                  # Entry point, root command, context flags
│   │   ├── cmd_verbs.go             # Symmetric read/write verb commands (set, get, add-member, etc.)
│   │   ├── cmd_service.go           # Service subcommands
│   │   ├── cmd_interface.go         # Interface subcommands
│   │   ├── cmd_lag.go               # LAG subcommands
│   │   ├── cmd_vlan.go              # VLAN subcommands
│   │   ├── cmd_acl.go               # ACL subcommands
│   │   ├── cmd_evpn.go              # EVPN subcommands
│   │   ├── cmd_bgp.go               # BGP subcommands (direct/indirect neighbors)
│   │   ├── cmd_health.go            # Health check subcommands
│   │   ├── cmd_baseline.go          # Baseline subcommands
│   │   ├── cmd_audit.go             # Audit subcommands
│   │   ├── cmd_settings.go          # Settings management
│   │   ├── cmd_state.go             # State DB queries (bgp, evpn, lag, vrf)
│   │   ├── cmd_provision.go         # Topology provisioning commands
│   │   ├── interactive.go           # Interactive menu mode
│   │   └── shell.go                 # Interactive shell with readline
├── pkg/
│   ├── network/                     # OO hierarchy + spec->config translation
│   │   ├── network.go               # Top-level Network object (owns specs)
│   │   ├── device.go                # Device with parent reference to Network
│   │   ├── interface.go             # Interface with parent reference to Device
│   │   ├── interface_ops.go         # Operations as methods on Interface
│   │   ├── device_ops.go            # Operations as methods on Device
│   │   ├── changeset.go             # ChangeSet for tracking config changes
│   │   ├── composite.go            # CompositeBuilder, CompositeConfig, CompositeMode types
│   │   └── topology.go             # TopologyProvisioner, ProvisionDevice, ProvisionInterface
│   ├── spec/                        # Specification loading (declarative intent)
│   │   ├── types.go                 # Spec structs (NetworkSpecFile, ServiceSpec, etc.)
│   │   ├── loader.go                # JSON loading and validation
│   │   └── resolver.go              # Inheritance resolution
│   ├── device/                      # Low-level device connection (imperative config)
│   │   ├── device.go                # Device struct, Connect, Disconnect, Lock
│   │   ├── configdb.go              # CONFIG_DB (DB 4) mapping + client
│   │   ├── statedb.go               # STATE_DB (DB 6) mapping + client
│   │   ├── appldb.go               # APP_DB (DB 0) mapping + client
│   │   ├── asicdb.go               # ASIC_DB (DB 1) mapping + client
│   │   ├── verify.go               # VerifyChangeSet, verification types
│   │   ├── state.go                 # State loading from config_db
│   │   └── tunnel.go                # SSH tunnel for Redis access
│   ├── model/                       # Domain models
│   │   ├── interface.go
│   │   ├── lag.go
│   │   ├── vlan.go
│   │   ├── vrf.go
│   │   ├── evpn.go
│   │   ├── bgp.go
│   │   ├── acl.go
│   │   ├── policy.go
│   │   └── qos.go
│   ├── operations/                  # Precondition checking utilities
│   │   └── precondition.go          # PreconditionChecker, DependencyChecker
│   ├── health/                      # Health checks
│   │   └── checker.go
│   ├── audit/                       # Audit logging
│   │   ├── event.go                 # Event types (uses network.Change)
│   │   └── logger.go                # Logger implementation
│   ├── auth/                        # Authorization
│   │   ├── permission.go            # Permission definitions
│   │   └── checker.go               # Permission checking
│   ├── settings/                    # CLI user settings persistence
│   │   └── settings.go              # DefaultNetwork, DefaultDevice, SpecDir
│   ├── configlet/                   # Baseline configuration templates
│   │   ├── configlet.go             # Configlet struct, loading, listing
│   │   └── resolve.go               # Template variable resolution
│   └── util/                        # Utilities
│       ├── errors.go                # Custom error types
│       ├── ip.go                    # IP address utilities
│       ├── derive.go                # Value derivation
│       ├── range.go                 # Range parsing
│       └── log.go                   # Logging utilities
├── internal/
│   └── testutil/                    # E2E test infrastructure
│       └── lab.go                   # SSH tunnel pool, LabSonicNodes, ResetLabBaseline
├── specs/                           # Specification files (declarative intent)
│   ├── network.json                 # Services, filters, VPNs, regions
│   ├── site.json                    # Site topology
│   ├── platforms.json               # Hardware platform definitions
│   └── profiles/                    # Per-device profiles
├── configlets/                      # Baseline templates
│   ├── images/                      # SONiC-VS images
│   │   └── common/                  # Shared image config
│   └── topologies/                  # Topology definitions
└── docs/                            # Documentation
```

**Additional files:**

| File | Purpose |
|------|---------|
| `pkg/device/tunnel.go` | SSH tunnel for Redis access through QEMU VMs |
| `pkg/device/statedb.go` | STATE_DB (Redis DB 6) operational state access |

**Platform and pipeline files:**

| File | Purpose |
|------|---------|
| `pkg/device/platform.go` | SonicPlatformConfig struct, platform.json parsing via SSH, port validation, GeneratePlatformSpec |
| `pkg/device/pipeline.go` | Redis MULTI/EXEC pipeline client for atomic batch writes (PipelineSet, ReplaceAll) |
| `pkg/network/composite.go` | CompositeBuilder, CompositeConfig, CompositeMode types; offline composite CONFIG_DB generation and delivery |

**Topology files:**

| File | Purpose |
|------|---------|
| `pkg/network/topology.go` | TopologyProvisioner, ProvisionDevice, ProvisionInterface, generateServiceEntries |
| `pkg/network/qos.go` | generateQoSDeviceEntries, generateQoSInterfaceEntries, resolveServiceQoSPolicy |

## 3. Core Data Structures

### 3.1 Specification Types (`pkg/spec/types.go`)

These types define **declarative intent** - what you want, not how to achieve it.

```go
// NetworkSpecFile - Global network specification file (declarative)
type NetworkSpecFile struct {
    Version      string                       `json:"version"`
    SuperUsers   []string                     `json:"super_users"`
    UserGroups   map[string][]string          `json:"user_groups"`
    Permissions  map[string][]string          `json:"permissions"`
    GenericAlias map[string]string            `json:"generic_alias"`
    Regions      map[string]*RegionSpec       `json:"regions"`
    PrefixLists  map[string][]string          `json:"prefix_lists"`
    FilterSpecs  map[string]*FilterSpec       `json:"filter_specs"`
    Policers     map[string]*PolicerSpec      `json:"policers"`
    QoSPolicies  map[string]*QoSPolicy         `json:"qos_policies,omitempty"`
    QoSProfiles  map[string]*model.QoSProfile `json:"qos_profiles,omitempty"` // Legacy

    // Route policies for BGP import/export
    RoutePolicies map[string]*RoutePolicy `json:"route_policies,omitempty"`

    // VPN definitions (referenced by services)
    IPVPN  map[string]*IPVPNSpec  `json:"ipvpn"`  // IP-VPN (L3VNI, route targets)
    MACVPN map[string]*MACVPNSpec `json:"macvpn"` // MAC-VPN (VLAN, L2VNI)

    // Service definitions (reference ipvpn/macvpn by name)
    Services map[string]*ServiceSpec `json:"services"`
}

// IPVPNSpec defines IP-VPN parameters for L3 routing (Type-5 routes).
// Referenced by services via the "ipvpn" field.
//
// For vrf_type "shared": VRF name = {serviceName}
// For vrf_type "interface": VRF name = {serviceName}-{shortenedIntf}
type IPVPNSpec struct {
    Description string   `json:"description,omitempty"`
    L3VNI       int      `json:"l3_vni"`
    ImportRT    []string `json:"import_rt"`
    ExportRT    []string `json:"export_rt"`
}

// MACVPNSpec defines MAC-VPN parameters for L2 bridging (Type-2 routes).
// Referenced by services via the "macvpn" field.
type MACVPNSpec struct {
    Description    string `json:"description,omitempty"`
    VLAN           int    `json:"vlan"`
    L2VNI          int    `json:"l2_vni"`
    ARPSuppression bool   `json:"arp_suppression,omitempty"`
}

// RegionSpec - Regional network settings
type RegionSpec struct {
    ASNumber     int                 `json:"as_number"`
    ASName       string              `json:"as_name,omitempty"`
    PrefixLists  map[string][]string `json:"prefix_lists,omitempty"`
    GenericAlias map[string]string   `json:"generic_alias,omitempty"`
}

// SiteSpecFile - Site topology specification file (site.json)
// Sites only define topology (which devices are route reflectors)
// Device details (loopback_ip, etc.) come from individual profiles
type SiteSpecFile struct {
    Version string               `json:"version"`
    Sites   map[string]*SiteSpec `json:"sites"`
}

// SiteSpec - Site topology (device names only, never IPs)
type SiteSpec struct {
    Region          string   `json:"region"`
    RouteReflectors []string `json:"route_reflectors,omitempty"`
    ClusterID       string   `json:"cluster_id,omitempty"`        // v3: BGP RR cluster-id
}

// ServiceSpec defines an interface service type.
//
// Services bundle VPN references, routing, filters, QoS, and permissions
// into a reusable template that can be applied to interfaces.
//
// Service Types:
//   - "l3":  L3 routed interface (optional ipvpn and vrf_type; without ipvpn, uses global routing table)
//   - "l2":  L2 bridged interface (requires macvpn)
//   - "irb": Integrated routing and bridging (requires both ipvpn and macvpn)
//
// VRF Instantiation (vrf_type):
//   - "interface": Creates per-interface VRF named {serviceName}-{shortenedIntf}
//   - "shared":    Uses shared VRF named {serviceName}
//   - (omitted):   No VRF, uses global routing table (for l3 without EVPN)
type ServiceSpec struct {
    Description string `json:"description"`
    ServiceType string `json:"service_type"` // l2, l3, irb

    // VPN references (names from ipvpn/macvpn sections)
    IPVPN   string `json:"ipvpn,omitempty"`
    MACVPN  string `json:"macvpn,omitempty"`
    VRFType string `json:"vrf_type,omitempty"` // "interface" or "shared"

    // Routing protocol specification
    Routing *RoutingSpec `json:"routing,omitempty"`

    // Anycast gateway (for IRB services)
    AnycastGateway string `json:"anycast_gateway,omitempty"` // e.g., "10.1.100.1/24"
    AnycastMAC     string `json:"anycast_mac,omitempty"`     // e.g., "00:00:00:01:02:03"

    // Filters (references to filter_specs)
    IngressFilter string `json:"ingress_filter,omitempty"`
    EgressFilter  string `json:"egress_filter,omitempty"`

    // QoS
    QoSPolicy  string `json:"qos_policy,omitempty"`
    QoSProfile string `json:"qos_profile,omitempty"` // Legacy

    // Permissions (override global permissions for this service)
    Permissions map[string][]string `json:"permissions,omitempty"`
}

// RoutingSpec defines routing protocol specification for a service.
//
// For BGP services:
//   - Local AS is always from device profile (ResolvedProfile.ASNumber)
//   - Peer AS can be fixed (number), or "request" (provided at apply time)
//   - Peer IP is derived from interface IP for point-to-point links
type RoutingSpec struct {
    Protocol     string `json:"protocol"`                // "bgp", "static", or empty
    PeerAS       string `json:"peer_as,omitempty"`       // AS number, or "request"
    ImportPolicy string `json:"import_policy,omitempty"` // Reference to route_policies
    ExportPolicy string `json:"export_policy,omitempty"` // Reference to route_policies

    // v3 additions:
    ImportCommunity  string `json:"import_community,omitempty"`   // BGP community for import filtering
    ExportCommunity  string `json:"export_community,omitempty"`   // BGP community to attach on export
    ImportPrefixList string `json:"import_prefix_list,omitempty"` // prefix-list ref for import filtering
    ExportPrefixList string `json:"export_prefix_list,omitempty"` // prefix-list ref for export filtering
    Redistribute     *bool  `json:"redistribute,omitempty"`       // override default (service=true, transit=false)
}

// FilterSpec defines a reusable set of ACL rules.
type FilterSpec struct {
    Description string        `json:"description"`
    Type        string        `json:"type"` // L3, L3V6
    Rules       []*FilterRule `json:"rules"`
}

// FilterRule defines a single rule within a FilterSpec.
type FilterRule struct {
    Sequence      int    `json:"seq"`
    SrcPrefixList string `json:"src_prefix_list,omitempty"`
    DstPrefixList string `json:"dst_prefix_list,omitempty"`
    SrcIP         string `json:"src_ip,omitempty"`
    DstIP         string `json:"dst_ip,omitempty"`
    Protocol      string `json:"protocol,omitempty"`
    SrcPort       string `json:"src_port,omitempty"`
    DstPort       string `json:"dst_port,omitempty"`
    DSCP          string `json:"dscp,omitempty"`
    Action        string `json:"action"`
    CoS           string `json:"cos,omitempty"`
    Policer       string `json:"policer,omitempty"`
    Log           bool   `json:"log,omitempty"`
}

// PolicerSpec defines a rate limiter.
type PolicerSpec struct {
    Bandwidth string `json:"bandwidth"`        // e.g., "10m", "1g"
    Burst     string `json:"burst"`            // e.g., "1m"
    Action    string `json:"action,omitempty"` // drop, remark
}

// QoSPolicy defines a declarative queue policy.
// Lives in pkg/spec/types.go. Referenced by NetworkSpecFile.QoSPolicies.
// Array position = queue index = traffic class.
type QoSPolicy struct {
    Description string      `json:"description,omitempty"`
    Queues      []*QoSQueue `json:"queues"`
}

// QoSQueue defines a single queue within a QoS policy.
type QoSQueue struct {
    Name   string `json:"name"`
    Type   string `json:"type"`             // "dwrr" or "strict"
    Weight int    `json:"weight,omitempty"` // DWRR weight (percentage)
    DSCP   []int  `json:"dscp,omitempty"`   // DSCP values mapped to this queue
    ECN    bool   `json:"ecn,omitempty"`    // Enable ECN/WRED
}

// QoSProfile defines a legacy QoS configuration (backward compat).
// Lives in pkg/model/qos.go. Superseded by QoSPolicy.
type QoSProfile struct {
    Description  string `json:"description,omitempty"`
    SchedulerMap string `json:"scheduler_map"`
    DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
    TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
}

// RoutePolicy defines a BGP route policy for import/export filtering.
type RoutePolicy struct {
    Description string             `json:"description,omitempty"`
    Rules       []*RoutePolicyRule `json:"rules"`
}

// RoutePolicyRule defines a single rule within a RoutePolicy.
type RoutePolicyRule struct {
    Sequence     int              `json:"seq"`
    Action       string           `json:"action"` // permit, deny
    PrefixList   string           `json:"prefix_list,omitempty"`
    ASPathLength string           `json:"as_path_length,omitempty"`
    Community    string           `json:"community,omitempty"`
    Set          *RoutePolicySet  `json:"set,omitempty"`
}

// RoutePolicySet defines attributes to set on matching routes.
type RoutePolicySet struct {
    LocalPref int    `json:"local_pref,omitempty"`
    Community string `json:"community,omitempty"`
    MED       int    `json:"med,omitempty"`
}

// DeviceProfile - Per-device configuration
// Note: Region is derived from site.json based on the site field
type DeviceProfile struct {
    // REQUIRED - must be specified
    MgmtIP     string `json:"mgmt_ip"`
    LoopbackIP string `json:"loopback_ip"`
    Site       string `json:"site"` // Site name - region is derived from site.json

    // OPTIONAL OVERRIDES - if set, override region/global values
    ASNumber         *int   `json:"as_number,omitempty"`
    IsRouteReflector bool   `json:"is_route_reflector,omitempty"`

    // OPTIONAL - device-specific
    Platform        string              `json:"platform,omitempty"`
    MAC             string              `json:"mac,omitempty"`
    UnderlayASN     int                 `json:"underlay_asn,omitempty"`
    VLANPortMapping map[int][]string    `json:"vlan_port_mapping,omitempty"`
    GenericAlias    map[string]string   `json:"generic_alias,omitempty"`
    PrefixLists     map[string][]string `json:"prefix_lists,omitempty"`

    // OPTIONAL - SSH access for Redis tunnel
    SSHUser string `json:"ssh_user,omitempty"`
    SSHPass string `json:"ssh_pass,omitempty"`
    SSHPort int    `json:"ssh_port,omitempty"`  // Custom SSH port (newtlab)

    // OPTIONAL - newtlab overrides (see newtlab LLD §1.2)
    ConsolePort int    `json:"console_port,omitempty"` // Written by newtlab profile patching
    VMMemory    int    `json:"vm_memory,omitempty"`    // Override platform default
    VMCPUs      int    `json:"vm_cpus,omitempty"`      // Override platform default
    VMImage     string `json:"vm_image,omitempty"`     // Override platform default
    VMHost      string `json:"vm_host,omitempty"`      // Remote QEMU host
}

// ResolvedProfile - Fully resolved device profile
type ResolvedProfile struct {
    // From profile
    DeviceName string
    MgmtIP     string
    LoopbackIP string
    Region     string
    Site       string
    Platform   string

    // Resolved from inheritance
    ASNumber         int
    IsRouteReflector bool

    // Derived at runtime
    RouterID     string   // = LoopbackIP
    VTEPSourceIP string   // = LoopbackIP
    BGPNeighbors   []string // From site route_reflectors -> lookup loopback IPs

    // Merged maps (profile > region > global)
    GenericAlias map[string]string
    PrefixLists  map[string][]string

    // SSH access for Redis tunnel
    SSHUser string
    SSHPass string
    SSHPort int // Custom SSH port (0 = default 22)
}

// ServiceType constants
const (
    ServiceTypeL2  = "l2"
    ServiceTypeL3  = "l3"
    ServiceTypeIRB = "irb"
)

// VRFType constants
const (
    VRFTypeInterface = "interface" // Per-interface VRF: {serviceName}-{shortenedIntf}
    VRFTypeShared    = "shared"    // Shared VRF: {serviceName}
)

// TopologySpecFile represents the topology specification file (topology.json).
type TopologySpecFile struct {
    Version     string                     `json:"version"`
    Description string                     `json:"description,omitempty"`
    Devices     map[string]*TopologyDevice `json:"devices"`
    Links       []*TopologyLink            `json:"links,omitempty"`
    NewtLab       *NewtLabConfig               `json:"newtlab,omitempty"` // newtlab defaults (see newtlab LLD §1.3)
}

type TopologyDevice struct {
    DeviceConfig *TopologyDeviceConfig          `json:"device_config,omitempty"`
    Interfaces   map[string]*TopologyInterface  `json:"interfaces"`
}

type TopologyDeviceConfig struct {
    RouteReflector bool `json:"route_reflector,omitempty"`
}

type TopologyInterface struct {
    Link    string            `json:"link,omitempty"`
    Service string            `json:"service"`
    IP      string            `json:"ip,omitempty"`
    Params  map[string]string `json:"params,omitempty"`
}

type TopologyLink struct {
    A string `json:"a"` // "device:interface"
    Z string `json:"z"` // "device:interface"
}

// PlatformSpecFile - Platform definitions file (platforms.json)
type PlatformSpecFile struct {
    Platforms map[string]*PlatformSpec `json:"platforms"`
}

// PlatformSpec defines a hardware or virtual platform.
// Core fields are read by newtron; VM fields are read by newtlab (see newtlab LLD §1.1).
type PlatformSpec struct {
    // Core (read by newtron)
    HwSKU        string   `json:"hwsku"`
    Description  string   `json:"description,omitempty"`
    PortCount    int      `json:"port_count"`
    DefaultSpeed string   `json:"default_speed"`
    Breakouts    []string `json:"breakouts,omitempty"`
    Dataplane    string   `json:"dataplane,omitempty"` // "vpp", "barefoot", "" (none/vs)

    // VM (read by newtlab — see newtlab LLD §1.1 for full field docs)
    VMImage              string            `json:"vm_image,omitempty"`
    VMMemory             int               `json:"vm_memory,omitempty"`
    VMCPUs               int               `json:"vm_cpus,omitempty"`
    VMNICDriver          string            `json:"vm_nic_driver,omitempty"`
    VMInterfaceMap       string            `json:"vm_interface_map,omitempty"`
    VMInterfaceMapCustom map[string]int    `json:"vm_interface_map_custom,omitempty"` // SONiC name → QEMU NIC index (for "custom" map type)
    VMCPUFeatures        string            `json:"vm_cpu_features,omitempty"`
    VMCredentials        *VMCredentials    `json:"vm_credentials,omitempty"`
    VMBootTimeout        int               `json:"vm_boot_timeout,omitempty"`
}

// VMCredentials holds default login credentials for a platform's VMs.
type VMCredentials struct {
    User string `json:"user"`
    Pass string `json:"pass"`
}
```

### 3.1A Spec Type Ownership

The `pkg/spec/` types are a shared coupling surface — all three tools read from the same JSON files. This table shows which tool reads or writes each field group:

| Type | Field Group | newtron | newtlab | newtest |
|------|-------------|---------|-------|---------|
| `PlatformSpec` | Core (`hwsku`, `port_count`, `default_speed`) | Read | | |
| `PlatformSpec` | VM (`vm_image`, `vm_memory`, `vm_cpus`, `vm_nic_driver`, ...) | | Read | |
| `PlatformSpec` | `dataplane` | | | Read (skip verify-ping) |
| `PlatformSpec` | `vm_credentials` | | Read | |
| `DeviceProfile` | Core (`mgmt_ip`, `loopback_ip`, `platform`, `site`) | Read | | |
| `DeviceProfile` | SSH (`ssh_user`, `ssh_pass`) | Read | | |
| `DeviceProfile` | `ssh_port`, `mgmt_ip` | Read | **Write** (profile patching) | |
| `DeviceProfile` | VM overrides (`vm_memory`, `vm_cpus`, `vm_image`) | | Read | |
| `TopologySpecFile` | Devices, links | Read (topology provisioner) | Read (VM deployment) | Read (scenario topology) |
| `TopologySpecFile` | `newtlab` config | | Read (VM defaults) | |
| `NetworkSpecFile` | Services, VPNs, filters, regions | Read | | |
| `SiteSpecFile` | Site topology, route reflectors | Read | | |

**Key insight:** `DeviceProfile.ssh_port` and `DeviceProfile.mgmt_ip` are the only fields that newtlab **writes** — all other spec data flows from JSON files into the tools as read-only input. newtlab writes these into profile JSON during deployment (newtlab LLD §10), and newtron reads them in `Device.Connect()` (device LLD §5.1).

### 3.2 Object Hierarchy (`pkg/network/`)

The system uses an object-oriented design with parent references, mirroring the original Perl architecture. This provides hierarchical access where child objects can access their parent's configuration:

```
Network (top-level)
    |
    +-- owns: NetworkSpecFile (services, filters, regions, etc.)
    +-- owns: SiteSpecFile (site topology)
    +-- owns: PlatformSpecFile (hardware platform definitions)
    +-- owns: TopologySpecFile (topology specification, optional)
    +-- owns: Loader (spec file loading)
    |
    +-- creates: Device instances (in Network's context)
                     |
                     +-- has: parent reference to Network
                     +-- owns: DeviceProfile
                     +-- owns: ResolvedProfile
                     +-- delegates: device.Device (low-level Redis connection)
                     |
                     +-- creates: Interface instances (in Device's context)
                                      |
                                      +-- has: parent reference to Device
                                      +-- can access: Device -> Network -> Services, Filters, etc.
```

**Key Design Pattern: Parent References**

```go
// Network is the top-level object
type Network struct {
    spec      *spec.NetworkSpecFile    // Services, filters, regions (declarative intent)
    sites     *spec.SiteSpecFile       // Site topology
    platforms *spec.PlatformSpecFile   // Hardware platform definitions
    topology  *spec.TopologySpecFile   // Topology specification (optional)
    loader    *spec.Loader             // Spec file loading
    devices   map[string]*Device       // Child objects
    mu        sync.RWMutex
}

// Device has parent reference to Network
type Device struct {
    network    *Network               // Parent reference
    name       string
    profile    *spec.DeviceProfile
    resolved   *spec.ResolvedProfile  // Resolved from inheritance
    interfaces map[string]*Interface  // Child objects
    conn       *device.Device         // Low-level device connection
    configDB   *device.ConfigDB       // Cached config_db snapshot
    connected  bool
    locked     bool
    mu         sync.RWMutex
}

// Lock acquires a distributed lock for this device via a Redis STATE_DB entry
// on the device itself (NEWTRON_LOCK|<deviceName>). Uses SET NX + EX for atomic
// acquisition with automatic TTL-based expiry.
// Not re-entrant — returns util.ErrDeviceLocked on contention.
func (d *Device) Lock() error {
    holder := fmt.Sprintf("%s@%s", currentUser(), hostname())
    return d.conn.Lock(holder)
}

// Unlock releases the distributed lock by deleting the NEWTRON_LOCK entry
// from STATE_DB on the device.
func (d *Device) Unlock() error {
    return d.conn.Unlock()
}

// IsLocked returns true if this device is currently locked by this process.
func (d *Device) IsLocked() bool {
    return d.conn.IsLocked()
}

// LockHolder returns the current lock holder string (e.g. "aldrin@workstation1")
// and acquisition time, or empty string if unlocked. Reads STATE_DB on the device.
// Status: not yet implemented. Depends on StateDBClient.GetLockHolder (device-lld §2.3).
func (d *Device) LockHolder() (holder string, acquired time.Time, err error) {
    return d.conn.LockHolder()
}

// IsConnected returns true if this device has an active connection.
func (d *Device) IsConnected() bool { return d.connected }

// SetConnected sets the connected state. Test helper only.
// Status: not yet implemented.
func (d *Device) SetConnected(v bool) { d.connected = v }

// SetLocked sets the locked state. Test helper only.
// Status: not yet implemented.
func (d *Device) SetLocked(v bool) { d.locked = v }

// --- Device accessors and bridging ---

// Name returns the device name.
func (d *Device) Name() string { return d.name }

// Network returns the parent Network.
func (d *Device) Network() *Network { return d.network }

// ASNumber returns the device's AS number from the resolved profile.
func (d *Device) ASNumber() int { return d.resolved.ASNumber }

// Underlying returns the low-level device.Device for Redis operations.
// Panics if not connected (use Connect() first).
func (d *Device) Underlying() *device.Device { return d.conn }

// GetInterface returns an Interface by name from the device's interface map.
// Returns error if the interface name is not in the topology or CONFIG_DB.
func (d *Device) GetInterface(name string) (*Interface, error)

// InterfaceNames returns sorted names of all interfaces on this device.
func (d *Device) ListInterfaces() []string

// Connect establishes the connection to the device:
//   1. Creates device.Device with the resolved profile
//   2. Calls device.Device.Connect() (SSH tunnel + Redis clients)
//   3. Populates Interface objects from CONFIG_DB PORT table and
//      NEWTRON_SERVICE_BINDING table (service bindings)
//   4. Sets d.configDB from the device.Device's CONFIG_DB snapshot
// After Connect, all Interface fields (adminStatus, vrf, ipAddresses,
// serviceName, etc.) are populated from CONFIG_DB.
func (d *Device) Connect(ctx context.Context) error

// Disconnect closes the low-level connection and SSH tunnel.
func (d *Device) Disconnect() error

// Interface has parent reference to Device
type Interface struct {
    device        *Device             // Parent reference
    name          string

    // Current state (from config_db)
    adminStatus   string
    operStatus    string
    speed         string
    mtu           int
    vrf           string
    ipAddresses   []string

    // Service binding (from NEWTRON_SERVICE_BINDING table)
    serviceName   string
    serviceIP     string
    serviceVRF    string
    serviceIPVPN  string
    serviceMACVPN string
    ingressACL    string
    egressACL     string
    lagMember     string
}

// --- Interface accessors ---

// Name returns the interface name (e.g. "Ethernet0").
func (i *Interface) Name() string { return i.name }

// Device returns the parent Device.
func (i *Interface) Device() *Device { return i.device }

// HasService returns true if a service is currently bound to this interface.
func (i *Interface) HasService() bool { return i.serviceName != "" }

// ServiceName returns the bound service name, or "" if none.
func (i *Interface) ServiceName() string { return i.serviceName }

// IPAddresses returns all IP addresses on this interface.
func (i *Interface) IPAddresses() []string
```

**Interface population from CONFIG_DB:**

During `Device.Connect()`, interfaces are populated by scanning CONFIG_DB tables:

| Interface field | Source table | Key/field |
|----------------|-------------|-----------|
| `adminStatus` | `PORT\|<name>` | `admin_status` |
| `speed` | `PORT\|<name>` | `speed` |
| `mtu` | `PORT\|<name>` | `mtu` |
| `vrf` | `INTERFACE\|<name>` | `vrf_name` |
| `ipAddresses` | `INTERFACE\|<name>\|<ip>` | key existence (IP keys) |
| `serviceName` | `NEWTRON_SERVICE_BINDING\|<name>` | `service_name` |
| `serviceIP` | `NEWTRON_SERVICE_BINDING\|<name>` | `ip` |
| `serviceVRF` | `NEWTRON_SERVICE_BINDING\|<name>` | `vrf_name` |
| `serviceIPVPN` | `NEWTRON_SERVICE_BINDING\|<name>` | `ipvpn` |
| `serviceMACVPN` | `NEWTRON_SERVICE_BINDING\|<name>` | `macvpn` |
| `ingressACL` | `NEWTRON_SERVICE_BINDING\|<name>` | `ingress_acl` |
| `egressACL` | `NEWTRON_SERVICE_BINDING\|<name>` | `egress_acl` |

**Accessing Network Specs from Interface**

```go
// Interface can access Network-level specs through parent chain
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    // Access Network through Device parent
    svc, err := i.Device().Network().GetService(serviceName)
    if err != nil {
        return nil, err
    }

    // Access Device properties
    asNum := i.Device().ASNumber()

    // Access filter spec from Network
    filter, _ := i.Device().Network().GetFilterSpec(svc.IngressFilter)

    // ... apply configuration
}
```

**Convenience Methods**

```go
// Interface provides convenience method for Network access
func (i *Interface) Network() *Network {
    return i.device.Network()
}

// Usage: simplifies access pattern
svc, _ := intf.Network().GetService("customer-l3")
```

**Benefits of This Design**

1. **Natural Access Pattern**: Objects access their context naturally without passing config separately
2. **Encapsulation**: Specs are owned by the right object level
3. **Inheritance**: Properties cascade from Network -> Device -> Interface
4. **No Duplication**: Specs loaded once at Network level, accessed everywhere
5. **Mirrors Original**: Same pattern as the original Perl implementation

**Network Accessors:**

```go
// GetTopology returns the topology spec, or nil if no topology.json exists.
func (n *Network) GetTopology() *spec.TopologySpecFile

// HasTopology returns true if a topology spec has been loaded.
func (n *Network) HasTopology() bool

// GetTopologyDevice returns a topology device by name.
func (n *Network) GetTopologyDevice(name string) (*spec.TopologyDevice, error)

// GetTopologyInterface returns a topology interface for a given device and interface name.
func (n *Network) GetTopologyInterface(device, intf string) (*spec.TopologyInterface, error)

// --- Device connection ---

// ConnectDevice retrieves a device by name, calls Connect() on it, and returns it.
// Convenience method used by CLI helpers — equivalent to GetDevice + dev.Connect.
func (n *Network) ConnectDevice(ctx context.Context, name string) (*Device, error)

// --- Device and spec accessors ---

// DeviceNames returns sorted names of all loaded devices.
func (n *Network) ListDevices() []string

// GetDevice returns a network.Device by name, or error if not found.
func (n *Network) GetDevice(name string) (*Device, error)

// GetService returns a service spec by name from the network spec.
// Returns error if the service name does not exist.
func (n *Network) GetService(name string) (*spec.ServiceSpec, error)

// GetIPVPN returns an IP-VPN spec by name. Returns error if not found.
func (n *Network) GetIPVPN(name string) (*spec.IPVPNSpec, error)

// GetMACVPN returns a MAC-VPN spec by name. Returns error if not found.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error)

// GetFilterSpec returns a filter spec by name. Returns error if not found.
func (n *Network) GetFilterSpec(name string) (*spec.FilterSpec, error)

// GetQoSProfile returns a QoS profile by name. Returns error if not found.
func (n *Network) GetQoSProfile(name string) (*model.QoSProfile, error)

// GetPlatform returns a platform spec by name, or error if not found.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error)
```

**Network Constructor (`pkg/network/network.go`):**

```go
// NewNetwork loads specs from the given directory and creates the Network.
//
// Initialization sequence:
//   1. Create Loader for specDir
//   2. Load network.json (required)
//   3. Load site.json (required)
//   4. Load platforms.json (required)
//   5. Load profiles/*.json (one per device, required)
//   6. Load topology.json (optional — returns nil if absent)
//   7. Resolve profiles: for each device, merge profile + region + global → ResolvedProfile
//   8. Validate topology (if loaded) — services, IPs, links
//   9. Create Device objects with resolved profiles, create Interface objects
//      from CONFIG_DB tables (populated later on Connect)
//
// Devices are created but NOT connected — call Device.Connect() to
// establish SSH tunnels and load CONFIG_DB/STATE_DB.
func NewNetwork(specDir string) (*Network, error)
```

**Loader (`pkg/spec/loader.go`):**

```go
// Loader reads and parses spec files from a directory.
type Loader struct {
    specDir  string
    profiles map[string]*DeviceProfile  // keyed by device name (filename stem)
}

// NewLoader creates a Loader for the given spec directory.
func NewLoader(specDir string) *Loader

// Load loads all spec files from the spec directory.
// Profiles are loaded from specDir/profiles/*.json; each file stem is the device name.
// After Load(), use getter methods to access the parsed results.
func (l *Loader) Load() error

// Getter methods (available after Load):
func (l *Loader) GetNetwork() *NetworkSpecFile
func (l *Loader) GetSite() *SiteSpecFile
func (l *Loader) GetPlatforms() *PlatformSpecFile
func (l *Loader) GetTopology() *TopologySpecFile // nil if topology.json absent
func (l *Loader) GetService(name string) (*ServiceSpec, error)
func (l *Loader) GetFilterSpec(name string) (*FilterSpec, error)
func (l *Loader) GetPrefixList(name string) ([]string, error)
func (l *Loader) GetPolicer(name string) (*PolicerSpec, error)

// loadTopologySpec loads topology.json from the spec directory.
// Returns (nil, nil) if topology.json does not exist — topology is optional.
func (l *Loader) loadTopologySpec() (*TopologySpecFile, error)

// validateTopology validates the topology spec:
//   - Device profiles must exist for all referenced devices
//   - Service names must exist in the network spec
//   - IP addresses must be valid CIDR notation
//   - Link endpoints must reference valid device:interface pairs
func (l *Loader) validateTopology(
    topology *TopologySpecFile,
    network *NetworkSpecFile,
    profiles map[string]*DeviceProfile,
) error
```

### 3.3 Low-Level Device Types (`pkg/device/device.go`)

The `device.Device` struct in `pkg/device` is the low-level representation that handles the actual Redis connection, while `network.Device` in `pkg/network` wraps it with the OO hierarchy.

```go
// Device represents a SONiC switch (low-level, imperative)
type Device struct {
    Name     string
    Profile  *spec.ResolvedProfile
    ConfigDB *ConfigDB                  // Snapshot of CONFIG_DB
    StateDB  *StateDB                   // Snapshot of STATE_DB
    State    *DeviceState               // Parsed operational state

    // Redis connections
    client      *ConfigDBClient         // CONFIG_DB (DB 4) client
    stateClient *StateDBClient          // STATE_DB (DB 6) client
    applClient  *AppDBClient            // APP_DB (DB 0) client
    asicClient  *AsicDBClient           // ASIC_DB (DB 1) client
    tunnel      *SSHTunnel              // SSH tunnel (nil if direct)
    connected   bool
    locked      bool
    lockHolder  string                     // "user@host" set on Lock()

    // Mutex for thread safety — see contract below
    mu sync.RWMutex
}
```

**Thread safety contract for `Device`:**

| Method | Lock type | What it protects |
|--------|-----------|-----------------|
| `Connect()` | `mu.Lock()` | `connected`, `tunnel`, all client fields |
| `Disconnect()` | `mu.Lock()` | `connected`, `tunnel`, all client fields |
| `LoadState()` | `mu.Lock()` | `ConfigDB`, `State` |
| `ApplyChanges()` | `mu.Lock()` | `ConfigDB` (reloaded after write) |
| `ApplyChangesPipelined()` | `mu.Lock()` | `ConfigDB` (reloaded after write) |
| `Lock()` / `Unlock()` | `mu.Lock()` | `locked`, `lockHolder` |
| `GetRoute()` / `GetRouteASIC()` | no lock | Read-only on dedicated clients; safe without mutex |
| `VerifyChangeSet()` | no lock | Uses a fresh temporary ConfigDBClient (no shared state) |

`Name`, `Profile` are set once at construction and never mutated — safe to read without lock. `ConfigDB` and `StateDB` snapshots are replaced (not mutated in place), so readers must hold `mu.RLock()` or coordinate with the caller. In practice, newtron operations are single-threaded per device (Lock→Apply→Verify→Unlock), so the mutex primarily guards against concurrent Connect/Disconnect.

```go
// Lock acquires a distributed lock on this device by writing a NEWTRON_LOCK
// entry to STATE_DB (Redis DB 6) with SET NX + EX semantics.
// The lock key is NEWTRON_LOCK|<deviceName>; the value contains the holder
// identity and timestamp. TTL provides automatic expiry if the client crashes.
// Returns ErrDeviceLocked (including current holder) if another process holds the lock.
//
// Delegation: Lock stores the holder in d.lockHolder and calls
// d.stateClient.AcquireLock(d.Name, holder, ttlSeconds). The holder string
// is used by Unlock() without requiring the caller to pass it again.
func (d *Device) Lock(holder string, ttlSeconds int) error

// Unlock releases the distributed lock by deleting the NEWTRON_LOCK entry
// from STATE_DB. Only succeeds if the current process holds the lock
// (compares d.lockHolder). Returns error if not locked.
func (d *Device) Unlock() error

// IsLocked returns true if this process holds the lock on this device.
func (d *Device) IsLocked() bool

// LockHolder reads STATE_DB to return the current lock holder and acquisition time.
// Returns ("", zero, nil) if no lock is held.
func (d *Device) LockHolder() (holder string, acquired time.Time, err error)

// DeviceState holds the current operational state of the device
type DeviceState struct {
    Interfaces   map[string]*InterfaceState
    PortChannels map[string]*PortChannelState
    VLANs        map[int]*VLANState
    VRFs         map[string]*VRFState
    BGP          *BGPState
    EVPN         *EVPNState
}

// InterfaceState represents interface operational state
type InterfaceState struct {
    Name        string
    AdminStatus string
    OperStatus  string
    Speed       string
    MTU         int
    VRF         string
    IPAddresses []string
    Service     string
    IngressACL  string
    EgressACL   string
    LAGMember   string        // Parent LAG if member
}

// PortChannelState represents LAG operational state
type PortChannelState struct {
    Name          string
    AdminStatus   string
    OperStatus    string
    Members       []string
    ActiveMembers []string
}

// VLANState represents VLAN operational state
type VLANState struct {
    ID         int
    Name       string
    OperStatus string
    Ports      []string
    SVIStatus  string
    L2VNI      int
}

// VRFState represents VRF operational state
type VRFState struct {
    Name       string
    State      string
    Interfaces []string
    L3VNI      int
    RouteCount int
}

// BGPState represents BGP operational state
type BGPState struct {
    LocalAS   int
    RouterID  string
    Neighbors map[string]*BGPNeighborState
}

// BGPNeighborState represents BGP neighbor state
type BGPNeighborState struct {
    Address  string
    RemoteAS int
    State    string
    PfxRcvd  int
    PfxSent  int
    Uptime   string
}

// EVPNState represents EVPN operational state
type EVPNState struct {
    VTEPState   string
    RemoteVTEPs []string
    VNICount    int
    Type2Routes int
    Type5Routes int
}
```

### 3.4 ConfigDB Mapping (`pkg/device/configdb.go`)

The ConfigDB struct mirrors SONiC's config_db.json structure. In v2, this has been expanded significantly with BGP globals, QoS, and the custom NEWTRON_SERVICE_BINDING table.

```go
// ConfigDB mirrors SONiC's config_db.json structure
type ConfigDB struct {
    // Standard SONiC tables
    DeviceMetadata    map[string]map[string]string  `json:"DEVICE_METADATA,omitempty"`
    Port              map[string]PortEntry          `json:"PORT,omitempty"`
    VLAN              map[string]VLANEntry          `json:"VLAN,omitempty"`
    VLANMember        map[string]VLANMemberEntry    `json:"VLAN_MEMBER,omitempty"`
    VLANInterface     map[string]map[string]string  `json:"VLAN_INTERFACE,omitempty"`
    Interface         map[string]InterfaceEntry     `json:"INTERFACE,omitempty"`
    PortChannel       map[string]PortChannelEntry   `json:"PORTCHANNEL,omitempty"`
    PortChannelMember map[string]map[string]string  `json:"PORTCHANNEL_MEMBER,omitempty"`
    LoopbackInterface map[string]map[string]string  `json:"LOOPBACK_INTERFACE,omitempty"`
    VRF               map[string]VRFEntry           `json:"VRF,omitempty"`
    VXLANTunnel       map[string]VXLANTunnelEntry   `json:"VXLAN_TUNNEL,omitempty"`
    VXLANTunnelMap    map[string]VXLANMapEntry      `json:"VXLAN_TUNNEL_MAP,omitempty"`
    VXLANEVPNNVO      map[string]EVPNNVOEntry       `json:"VXLAN_EVPN_NVO,omitempty"`
    SuppressVLANNeigh map[string]map[string]string  `json:"SUPPRESS_VLAN_NEIGH,omitempty"`
    ACLTable          map[string]ACLTableEntry      `json:"ACL_TABLE,omitempty"`
    ACLRule           map[string]ACLRuleEntry       `json:"ACL_RULE,omitempty"`
    ACLTableType      map[string]ACLTableTypeEntry  `json:"ACL_TABLE_TYPE,omitempty"`
    RouteTable        map[string]RouteEntry         `json:"ROUTE_TABLE,omitempty"`

    // v2: Static Anycast Gateway
    SAG               map[string]map[string]string  `json:"SAG,omitempty"`
    SAGGlobal         map[string]map[string]string  `json:"SAG_GLOBAL,omitempty"`

    // v2: BGP tables (CONFIG_DB-managed BGP)
    BGPNeighbor       map[string]BGPNeighborEntry   `json:"BGP_NEIGHBOR,omitempty"`
    BGPNeighborAF     map[string]BGPNeighborAFEntry `json:"BGP_NEIGHBOR_AF,omitempty"`
    BGPGlobals        map[string]BGPGlobalsEntry    `json:"BGP_GLOBALS,omitempty"`
    BGPGlobalsAF      map[string]BGPGlobalsAFEntry  `json:"BGP_GLOBALS_AF,omitempty"`
    BGPEVPNVNI        map[string]BGPEVPNVNIEntry    `json:"BGP_EVPN_VNI,omitempty"`

    // v2: QoS tables
    Scheduler         map[string]SchedulerEntry     `json:"SCHEDULER,omitempty"`
    Queue             map[string]QueueEntry         `json:"QUEUE,omitempty"`
    WREDProfile       map[string]WREDProfileEntry   `json:"WRED_PROFILE,omitempty"`
    PortQoSMap        map[string]PortQoSMapEntry    `json:"PORT_QOS_MAP,omitempty"`
    DSCPToTCMap       map[string]map[string]string  `json:"DSCP_TO_TC_MAP,omitempty"`
    TCToQueueMap      map[string]map[string]string  `json:"TC_TO_QUEUE_MAP,omitempty"`
    Policer           map[string]PolicerEntry       `json:"POLICER,omitempty"`

    // v3: Extended BGP tables (frrcfgd — FRR management framework)
    BGPPeerGroup          map[string]BGPPeerGroupEntry         `json:"BGP_PEER_GROUP,omitempty"`
    BGPPeerGroupAF        map[string]BGPPeerGroupAFEntry       `json:"BGP_PEER_GROUP_AF,omitempty"`
    BGPGlobalsAFNet    map[string]BGPGlobalsAFNetEntry  `json:"BGP_GLOBALS_AF_NETWORK,omitempty"`
    BGPGlobalsAFAgg    map[string]BGPGlobalsAFAggEntry  `json:"BGP_GLOBALS_AF_AGGREGATE_ADDR,omitempty"`
    RouteRedistribute     map[string]RouteRedistributeEntry    `json:"ROUTE_REDISTRIBUTE,omitempty"`
    RouteMap              map[string]RouteMapEntry             `json:"ROUTE_MAP,omitempty"`
    PrefixSet             map[string]PrefixSetEntry            `json:"PREFIX_SET,omitempty"`
    CommunitySet          map[string]CommunitySetEntry         `json:"COMMUNITY_SET,omitempty"`
    ASPathSet             map[string]ASPathSetEntry             `json:"AS_PATH_SET,omitempty"`

    // v2: Newtron custom table (NOT standard SONiC)
    NewtronServiceBinding map[string]ServiceBindingEntry `json:"NEWTRON_SERVICE_BINDING,omitempty"`
}

// --- ConfigDB generic accessors (for newtest verify-config-db) ---

// GetTableKeys returns all keys for a table name (e.g., "BGP_NEIGHBOR").
// Uses reflect to find the struct field matching the JSON tag, then returns
// the map keys. Returns nil for unknown tables.
func (db *ConfigDB) GetTableKeys(table string) []string

// GetEntry returns the fields of a table|key as map[string]string.
// Uses the JSON struct tags to find the right map, then marshals the entry
// to map[string]string via the json tags. Returns nil if not found.
func (db *ConfigDB) GetEntry(table, key string) map[string]string
```

**ConfigDB Entry Types (v2 additions):**

```go
// BGPGlobalsEntry represents global BGP settings for a VRF
type BGPGlobalsEntry struct {
    RouterID            string `json:"router_id,omitempty"`
    LocalASN            string `json:"local_asn,omitempty"`
    ConfedID            string `json:"confed_id,omitempty"`
    ConfedPeers         string `json:"confed_peers,omitempty"`
    GracefulRestart     string `json:"graceful_restart,omitempty"`
    // v3 additions (frrcfgd):
    LoadBalanceMPRelax  string `json:"load_balance_mp_relax,omitempty"`  // multipath relax for ECMP
    RRClusterID         string `json:"rr_cluster_id,omitempty"`          // route reflector cluster ID
    EBGPRequiresPolicy  string `json:"ebgp_requires_policy,omitempty"`   // disable mandatory eBGP policy
    DefaultIPv4Unicast  string `json:"default_ipv4_unicast,omitempty"`   // disable auto IPv4 unicast activation
    LogNeighborChanges  string `json:"log_neighbor_changes,omitempty"`   // log neighbor state transitions
    SuppressFIBPending  string `json:"suppress_fib_pending,omitempty"`   // suppress routes until FIB confirmed
}

// BGPGlobalsAFEntry represents BGP address-family settings
// Key format: "vrf_name|address_family" (e.g., "Vrf_CUST1|l2vpn_evpn")
type BGPGlobalsAFEntry struct {
    AdvertiseAllVNI    string `json:"advertise-all-vni,omitempty"`
    AdvertiseDefaultGW string `json:"advertise-default-gw,omitempty"`
    AdvertiseSVIIP     string `json:"advertise-svi-ip,omitempty"`
    AdvertiseIPv4      string `json:"advertise_ipv4_unicast,omitempty"`
    AdvertiseIPv6      string `json:"advertise_ipv6_unicast,omitempty"`
    RD                 string `json:"rd,omitempty"`
    RTImport           string `json:"rt_import,omitempty"`
    RTExport           string `json:"rt_export,omitempty"`
    RTImportEVPN       string `json:"route_target_import_evpn,omitempty"`
    RTExportEVPN       string `json:"route_target_export_evpn,omitempty"`
    // v3 additions:
    MaxEBGPPaths       string `json:"max_ebgp_paths,omitempty"`  // maximum ECMP paths for eBGP
    MaxIBGPPaths       string `json:"max_ibgp_paths,omitempty"`  // maximum ECMP paths for iBGP
}

// BGPEVPNVNIEntry represents per-VNI EVPN settings
// Key format: "vrf_name|vni" (e.g., "Vrf_CUST1|10001")
type BGPEVPNVNIEntry struct {
    RD                 string `json:"rd,omitempty"`
    RTImport           string `json:"route_target_import,omitempty"`
    RTExport           string `json:"route_target_export,omitempty"`
    AdvertiseDefaultGW string `json:"advertise_default_gw,omitempty"`
}

// BGPNeighborEntry represents a BGP neighbor
// Key format: "vrf|neighbor_ip" (e.g., "default|10.0.0.2", "Vrf_CUST1|10.0.0.2")
// v3: added peer_group, ebgp_multihop, password fields
type BGPNeighborEntry struct {
    LocalAddr     string `json:"local_addr,omitempty"`
    Name          string `json:"name,omitempty"`
    ASN           string `json:"asn,omitempty"`
    HoldTime      string `json:"holdtime,omitempty"`
    KeepaliveTime string `json:"keepalive,omitempty"`
    AdminStatus   string `json:"admin_status,omitempty"`
    // v3 additions:
    PeerGroup    string `json:"peer_group,omitempty"`
    EBGPMultihop string `json:"ebgp_multihop,omitempty"`
    Password     string `json:"password,omitempty"`
}

// BGPNeighborAFEntry represents per-neighbor address-family settings
// Key format: "vrf|neighbor_ip|address_family" (e.g., "default|10.0.0.2|l2vpn_evpn")
type BGPNeighborAFEntry struct {
    Activate             string `json:"activate,omitempty"`
    RouteReflectorClient string `json:"route_reflector_client,omitempty"`
    NextHopSelf          string `json:"next_hop_self,omitempty"`
    SoftReconfiguration  string `json:"soft_reconfiguration,omitempty"`
    // v3 additions:
    AllowasIn            string `json:"allowas_in,omitempty"`            // allow local AS in received path
    RouteMapIn           string `json:"route_map_in,omitempty"`          // inbound route-map
    RouteMapOut          string `json:"route_map_out,omitempty"`         // outbound route-map
    PrefixListIn         string `json:"prefix_list_in,omitempty"`        // inbound prefix filter
    PrefixListOut        string `json:"prefix_list_out,omitempty"`       // outbound prefix filter
    DefaultOriginate     string `json:"default_originate,omitempty"`     // advertise default route
    AddpathTxAllPaths    string `json:"addpath_tx_all_paths,omitempty"`  // send all paths
}

// ServiceBindingEntry tracks service bindings applied by newtron.
// Key format: interface name (e.g., "Ethernet0", "PortChannel100", "Vlan100")
// This provides explicit tracking of what service was applied, enabling
// proper removal and refresh without relying on naming conventions.
type ServiceBindingEntry struct {
    ServiceName string `json:"service_name"`
    IPAddress   string `json:"ip_address,omitempty"`
    VRFName     string `json:"vrf_name,omitempty"`
    IPVPN       string `json:"ipvpn,omitempty"`
    MACVPN      string `json:"macvpn,omitempty"`
    IngressACL  string `json:"ingress_acl,omitempty"`
    EgressACL   string `json:"egress_acl,omitempty"`
    AppliedAt   string `json:"applied_at,omitempty"`
    AppliedBy   string `json:"applied_by,omitempty"`
}

// SchedulerEntry represents a QoS scheduler
type SchedulerEntry struct {
    Type   string `json:"type"`             // DWRR, STRICT
    Weight string `json:"weight,omitempty"` // For DWRR
}

// QueueEntry represents a queue configuration
type QueueEntry struct {
    Scheduler   string `json:"scheduler,omitempty"`
    WREDProfile string `json:"wred_profile,omitempty"`
}

// WREDProfileEntry represents a WRED drop profile
type WREDProfileEntry struct {
    GreenMinThreshold     string `json:"green_min_threshold,omitempty"`
    GreenMaxThreshold     string `json:"green_max_threshold,omitempty"`
    GreenDropProbability  string `json:"green_drop_probability,omitempty"`
    YellowMinThreshold    string `json:"yellow_min_threshold,omitempty"`
    YellowMaxThreshold    string `json:"yellow_max_threshold,omitempty"`
    YellowDropProbability string `json:"yellow_drop_probability,omitempty"`
    RedMinThreshold       string `json:"red_min_threshold,omitempty"`
    RedMaxThreshold       string `json:"red_max_threshold,omitempty"`
    RedDropProbability    string `json:"red_drop_probability,omitempty"`
    ECN                   string `json:"ecn,omitempty"`
}

// PortQoSMapEntry represents QoS map binding for a port
type PortQoSMapEntry struct {
    DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
    TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
}

// PolicerEntry represents a rate limiter
type PolicerEntry struct {
    MeterType    string `json:"meter_type,omitempty"`
    Mode         string `json:"mode,omitempty"`
    CIR          string `json:"cir,omitempty"`
    CBS          string `json:"cbs,omitempty"`
    PIR          string `json:"pir,omitempty"`
    PBS          string `json:"pbs,omitempty"`
    GreenAction  string `json:"green_action,omitempty"`
    YellowAction string `json:"yellow_action,omitempty"`
    RedAction    string `json:"red_action,omitempty"`
}
```

**BGP ConfigDB Entry Types (frrcfgd tables):**

```go
// RouteRedistributeEntry represents route redistribution config
// Key format: "vrf|src_protocol|dst_protocol|address_family" (e.g., "default|connected|bgp|ipv4")
type RouteRedistributeEntry struct {
    RouteMap string `json:"route_map,omitempty"` // optional route-map filter
}

// RouteMapEntry represents a route-map rule
// Key format: "map_name|seq" (e.g., "ALLOW_LOOPBACK|10")
type RouteMapEntry struct {
    Action         string `json:"route_operation"`              // permit, deny
    MatchPrefixSet string `json:"match_prefix_set,omitempty"`   // prefix-set reference
    MatchCommunity string `json:"match_community,omitempty"`    // community-set reference
    MatchASPath    string `json:"match_as_path,omitempty"`      // as-path-set reference
    MatchNextHop   string `json:"match_next_hop,omitempty"`     // next-hop match
    SetLocalPref   string `json:"set_local_pref,omitempty"`     // set local-preference
    SetCommunity   string `json:"set_community,omitempty"`      // set community
    SetMED         string `json:"set_med,omitempty"`            // set MED
    SetNextHop     string `json:"set_next_hop,omitempty"`       // set next-hop
}

// BGPPeerGroupEntry represents a BGP peer group template
// Key format: "peer_group_name" (e.g., "SPINE_PEERS")
type BGPPeerGroupEntry struct {
    ASN         string `json:"asn,omitempty"`
    LocalAddr   string `json:"local_addr,omitempty"`
    AdminStatus string `json:"admin_status,omitempty"`
    HoldTime    string `json:"holdtime,omitempty"`
    Keepalive   string `json:"keepalive,omitempty"`
    Password    string `json:"password,omitempty"`
}

// BGPPeerGroupAFEntry represents per-AF settings for a peer group
// Key format: "peer_group_name|address_family" (e.g., "SPINE_PEERS|ipv4_unicast")
type BGPPeerGroupAFEntry struct {
    Activate             string `json:"activate,omitempty"`
    RouteReflectorClient string `json:"route_reflector_client,omitempty"`
    NextHopSelf          string `json:"next_hop_self,omitempty"`
    RouteMapIn           string `json:"route_map_in,omitempty"`
    RouteMapOut          string `json:"route_map_out,omitempty"`
    SoftReconfiguration  string `json:"soft_reconfiguration,omitempty"`
}

// BGPGlobalsAFNetEntry represents a BGP network statement
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/24")
type BGPGlobalsAFNetEntry struct {
    Policy string `json:"policy,omitempty"` // Optional route-map
}

// BGPGlobalsAFAggEntry represents a BGP aggregate-address
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/8")
type BGPGlobalsAFAggEntry struct {
    SummaryOnly string `json:"summary_only,omitempty"`
    ASSet       string `json:"as_set,omitempty"`
}

// PrefixSetEntry represents an IP prefix list entry
// Key format: "set_name|seq" (e.g., "LOOPBACKS|10")
type PrefixSetEntry struct {
    IPPrefix     string `json:"ip_prefix"`                  // IP prefix (e.g., "10.0.0.0/8")
    Action       string `json:"action"`                     // permit, deny
    MaskLenRange string `json:"masklength_range,omitempty"` // e.g., "24..32"
}

// CommunitySetEntry represents a BGP community list
// Key format: "set_name" (e.g., "CUSTOMER_COMMUNITIES")
type CommunitySetEntry struct {
    SetType         string `json:"set_type,omitempty"`          // standard, expanded
    MatchAction     string `json:"match_action,omitempty"`
    CommunityMember string `json:"community_member,omitempty"`  // Comma-separated communities
}

// ASPathSetEntry represents an AS-path regex filter
// Key format: "set_name" (e.g., "SHORT_PATHS")
type ASPathSetEntry struct {
    ASPathMember string `json:"as_path_member,omitempty"` // Regex pattern
}
```

**ConfigDB Entry Types (core SONiC tables) — only newtron-used fields:**

```go
// PortEntry represents a physical port in CONFIG_DB.
// Key format: "Ethernet0", "Ethernet4", etc.
type PortEntry struct {
    AdminStatus string `json:"admin_status,omitempty"` // "up", "down"
    MTU         string `json:"mtu,omitempty"`          // "9100"
    Speed       string `json:"speed,omitempty"`        // "100000" (Mbps)
    Autoneg     string `json:"autoneg,omitempty"`      // "on", "off"
    FEC         string `json:"fec,omitempty"`          // "rs", "fc", "none"
    Lanes       string `json:"lanes,omitempty"`        // "0,1,2,3"
    Alias       string `json:"alias,omitempty"`        // "Eth1/1"
    Description string `json:"description,omitempty"`
    Index       string `json:"index,omitempty"`        // physical port index
}

// VLANEntry represents a VLAN in CONFIG_DB.
// Key format: "Vlan100", "Vlan200", etc.
type VLANEntry struct {
    VlanID      string `json:"vlanid"`                   // "100"
    Description string `json:"description,omitempty"`
    AdminStatus string `json:"admin_status,omitempty"`   // "up", "down"
    DHCPServers string `json:"dhcp_servers,omitempty"`   // comma-separated
    MTU         string `json:"mtu,omitempty"`
}

// VLANMemberEntry represents a VLAN member port in CONFIG_DB.
// Key format: "Vlan100|Ethernet0", "Vlan100|PortChannel100"
type VLANMemberEntry struct {
    TaggingMode string `json:"tagging_mode"` // "tagged", "untagged"
}

// InterfaceEntry represents a routed interface in CONFIG_DB.
// Key format: "Ethernet0" (base entry with VRF) or "Ethernet0|10.1.1.1/30" (IP binding)
// IP binding entries have no fields — they use the NULL:NULL sentinel convention.
type InterfaceEntry struct {
    VRFName     string `json:"vrf_name,omitempty"`  // VRF binding (base entry only)
    NATZone     string `json:"nat_zone,omitempty"`  // NAT zone identifier
    ProxyArp    string `json:"proxy_arp,omitempty"` // "enabled", "disabled"
    MPLSEnabled string `json:"mpls,omitempty"`      // "enable", "disable"
}

// PortChannelEntry represents a LAG in CONFIG_DB.
// Key format: "PortChannel100"
type PortChannelEntry struct {
    AdminStatus string `json:"admin_status,omitempty"` // "up", "down"
    MTU         string `json:"mtu,omitempty"`
    MinLinks    string `json:"min_links,omitempty"`
    Fallback    string `json:"fallback,omitempty"`     // "true", "false"
    FastRate    string `json:"fast_rate,omitempty"`    // "true", "false"
    LACPKey     string `json:"lacp_key,omitempty"`     // LACP aggregation key
    Description string `json:"description,omitempty"`
}

// VRFEntry represents a VRF in CONFIG_DB.
// Key format: "customer-l3-Eth0", "customer-l3"
type VRFEntry struct {
    VNI string `json:"vni,omitempty"` // L3VNI (e.g., "10001")
}

// VXLANTunnelEntry represents a VTEP in CONFIG_DB.
// Key format: "vtep1" (typically only one per device)
type VXLANTunnelEntry struct {
    SrcIP string `json:"src_ip"` // VTEP source IP (loopback address)
}

// VXLANMapEntry represents a VNI-to-VLAN or VNI-to-VRF mapping.
// Key format: "vtep1|map_{vni}_{target}" (e.g., "vtep1|map_10001_Vrf_CUST1" or "vtep1|map_10700_Vlan700")
type VXLANMapEntry struct {
    VNI  string `json:"vni"`            // VNI number (e.g., "10001")
    VLAN string `json:"vlan,omitempty"` // VLAN name for L2VNI (e.g., "Vlan700")
    VRF  string `json:"vrf,omitempty"`  // VRF name for L3VNI (e.g., "Vrf_CUST1")
}

// EVPNNVOEntry represents the EVPN NVO (Network Virtualization Overlay).
// Key format: "nvo1" (typically only one per device)
type EVPNNVOEntry struct {
    SourceVTEP string `json:"source_vtep"` // Reference to VXLAN_TUNNEL key (e.g., "vtep1")
}

// ACLTableEntry represents an ACL table in CONFIG_DB.
// Key format: "customer-l3-in", "customer-l3-out"
type ACLTableEntry struct {
    Type        string `json:"type"`                  // "L3", "L3V6", "MIRROR"
    Stage       string `json:"stage"`                 // "ingress", "egress"
    PolicyDesc  string `json:"policy_desc,omitempty"`
    Ports       string `json:"ports"`                 // Comma-separated: "Ethernet0,Ethernet4"
}

// ACLRuleEntry represents a single ACL rule in CONFIG_DB.
// Key format: "customer-l3-in|RULE_10" (table_name|rule_name)
type ACLRuleEntry struct {
    Priority    string `json:"PRIORITY"`
    PacketAction string `json:"PACKET_ACTION"`          // "FORWARD", "DROP"
    SrcIP       string `json:"SRC_IP,omitempty"`
    DstIP       string `json:"DST_IP,omitempty"`
    IPProtocol  string `json:"IP_PROTOCOL,omitempty"`   // "6" (TCP), "17" (UDP)
    L4SrcPort   string `json:"L4_SRC_PORT,omitempty"`
    L4DstPort   string `json:"L4_DST_PORT,omitempty"`
    DSCP        string `json:"DSCP,omitempty"`
}

// ACLTableTypeEntry represents a custom ACL table type definition.
// Key format: "L3" or custom names
type ACLTableTypeEntry struct {
    Matches string `json:"matches,omitempty"` // Comma-separated match fields
    Actions string `json:"actions,omitempty"` // Comma-separated action types
}
```

**Complete ConfigDB table inventory:**

| Table | Key Format | Purpose |
|-------|-----------|---------|
| DEVICE_METADATA | `localhost` | Hostname, platform, BGP ASN |
| PORT | `Ethernet0` | Physical port config |
| PORTCHANNEL | `PortChannel100` | LAG config |
| PORTCHANNEL_MEMBER | `PortChannel100\|Ethernet0` | LAG membership |
| VLAN | `Vlan100` | VLAN config |
| VLAN_MEMBER | `Vlan100\|Ethernet0` | VLAN membership |
| VLAN_INTERFACE | `Vlan100` or `Vlan100\|10.1.1.1/24` | SVI config and IPs |
| INTERFACE | `Ethernet0` or `Ethernet0\|10.1.1.1/30` | Interface config and IPs |
| LOOPBACK_INTERFACE | `Loopback0` or `Loopback0\|10.0.0.1/32` | Loopback config |
| VRF | `Vrf_CUST1` | VRF config with optional VNI |
| VXLAN_TUNNEL | `vtep1` | VTEP source IP |
| VXLAN_TUNNEL_MAP | `vtep1\|map_10001_Vrf_CUST1` | VNI to VLAN/VRF mapping |
| VXLAN_EVPN_NVO | `nvo1` | EVPN NVO referencing source VTEP |
| SUPPRESS_VLAN_NEIGH | `Vlan100` | ARP suppression per VLAN |
| SAG | varies | Static anycast gateway per interface |
| SAG_GLOBAL | `global` | Global SAG MAC address |
| BGP_NEIGHBOR | `default\|10.0.0.2` | BGP neighbor config |
| BGP_NEIGHBOR_AF | `default\|10.0.0.2\|l2vpn_evpn` | Per-neighbor address family |
| BGP_GLOBALS | `default` or VRF name | Global BGP settings per VRF |
| BGP_GLOBALS_AF | `default\|l2vpn_evpn` | BGP address family settings |
| BGP_EVPN_VNI | `Vrf_CUST1\|10001` | Per-VNI EVPN settings |
| ACL_TABLE | `customer-l3-in` | ACL table config |
| ACL_RULE | `customer-l3-in\|RULE_1` | ACL rule config |
| SCHEDULER | `scheduler.0` | QoS scheduler |
| QUEUE | `Ethernet0\|0` | Queue binding |
| WRED_PROFILE | `WRED_GREEN` | WRED drop profile |
| PORT_QOS_MAP | `Ethernet0` | Port QoS map binding |
| DSCP_TO_TC_MAP | `DSCP_TO_TC` | DSCP to traffic class map |
| TC_TO_QUEUE_MAP | `TC_TO_QUEUE` | Traffic class to queue map |
| POLICER | `POLICER_1M` | Rate limiter |
| ROUTE_REDISTRIBUTE | `default\|connected\|bgp\|ipv4` | Route redistribution config |
| ROUTE_MAP | `ALLOW_LOOPBACK\|10` | Route-map rules |
| BGP_PEER_GROUP | `SPINE_PEERS` | BGP peer group templates |
| BGP_PEER_GROUP_AF | `SPINE_PEERS\|ipv4_unicast` | Per-AF peer group settings |
| BGP_GLOBALS_AF_NETWORK | `default\|ipv4_unicast\|10.0.0.0/24` | BGP network statement |
| BGP_GLOBALS_AF_AGGREGATE_ADDR | `default\|ipv4_unicast\|10.0.0.0/8` | BGP aggregate-address |
| PREFIX_SET | `LOOPBACKS\|10` | IP prefix list entries |
| COMMUNITY_SET | `CUSTOMER_COMMUNITIES` | BGP community lists |
| AS_PATH_SET | `SHORT_PATHS` | AS-path regex filters |
| NEWTRON_SERVICE_BINDING | `Ethernet0` | Newtron service tracking (custom) |

### 3.5 ConfigDB Client (`pkg/device/configdb.go`)

The ConfigDBClient wraps a Redis client configured for CONFIG_DB (DB 4).

```go
// ConfigDBClient wraps Redis client for config_db access
type ConfigDBClient struct {
    client *redis.Client
    ctx    context.Context
}

// NewConfigDBClient creates a new config_db client
func NewConfigDBClient(addr string) *ConfigDBClient {
    return &ConfigDBClient{
        client: redis.NewClient(&redis.Options{
            Addr: addr,
            DB:   4, // CONFIG_DB
        }),
        ctx: context.Background(),
    }
}

func (c *ConfigDBClient) Connect() error
func (c *ConfigDBClient) Close() error

// GetAll reads the entire CONFIG_DB into a typed ConfigDB struct.
//
// Algorithm:
//   1. KEYS * in DB 4 (acceptable for lab tool; SCAN would be premature)
//   2. For each key, split on first "|" → table name + entry key
//   3. HGETALL per key → field map
//   4. Switch on table name to dispatch into the corresponding typed
//      map in ConfigDB (e.g., "PORT" → configDB.Port[key] = PortEntry{...})
//   5. Unknown tables are stored in configDB.Extra[table][key]
//   6. Return the populated ConfigDB
func (c *ConfigDBClient) GetAll() (*ConfigDB, error)
func (c *ConfigDBClient) Get(table, key string) (map[string]string, error)
func (c *ConfigDBClient) Exists(table, key string) (bool, error)

// GetTableKeys returns all keys in a table via KEYS command.
// E.g., GetTableKeys("BGP_NEIGHBOR") returns ["default|10.0.0.1", "default|10.0.0.2"].
func (c *ConfigDBClient) GetTableKeys(table string) ([]string, error)

// GetEntry is an alias for Get — returns (fields, error) for a table|key.
// Returns (nil, nil) if the entry does not exist.
func (c *ConfigDBClient) GetEntry(table, key string) (map[string]string, error)

// Set writes a table entry. If fields is empty, a "NULL":"NULL" sentinel is
// written so the Redis key is actually created (SONiC convention for
// field-less entries like PORTCHANNEL_MEMBER or INTERFACE IP keys).
func (c *ConfigDBClient) Set(table, key string, fields map[string]string) error

func (c *ConfigDBClient) Delete(table, key string) error
func (c *ConfigDBClient) DeleteField(table, key, field string) error
```

The `Set` method handles the SONiC convention for entries that have no fields (such as IP address bindings or member entries). These require a `"NULL":"NULL"` sentinel hash field to create the Redis key, because SONiC's subscriber infrastructure relies on key existence.

**Pipeline methods** (`pkg/device/pipeline.go`):

```go
// TableChange represents a single table entry change for pipeline operations
type TableChange struct {
    Table  string
    Key    string
    Fields map[string]string // nil = delete sentinel
}

// TableKey identifies a table entry for deletion
type TableKey struct {
    Table string
    Key   string
}

// PipelineSet writes and deletes multiple entries atomically via Redis MULTI/EXEC pipeline.
// sets are applied as HSET commands; dels are applied as DEL commands.
// Used by composite delivery and ChangeSet.Apply for atomic multi-entry writes.
func (c *ConfigDBClient) PipelineSet(sets []TableChange, dels []TableKey) error

// ReplaceAll flushes CONFIG_DB and writes the entire config atomically.
// Used by composite overwrite mode.
//
// Algorithm:
//   1. MULTI
//   2. FLUSHDB — clear all existing CONFIG_DB entries
//   3. Iterate ConfigDB struct fields via reflection on json tags:
//      for each non-nil map field (e.g., Port, VLAN, VRF, BGPNeighbor...),
//      for each entry in the map, HSET "TABLE|key" with field values
//   4. EXEC — atomic commit of flush + all writes
//
// The entire operation is a single MULTI/EXEC transaction, so CONFIG_DB
// is never in a partially-written state.
func (c *ConfigDBClient) ReplaceAll(config *ConfigDB) error
```

### 3.6 ChangeSet Types (`pkg/network/changeset.go`)

Operations return ChangeSets that can be previewed or applied:

```go
// ChangeType represents the type of configuration change
type ChangeType string

const (
    ChangeAdd    ChangeType = "add"
    ChangeModify ChangeType = "modify"
    ChangeDelete ChangeType = "delete"
)

// Change represents a single configuration change
type Change struct {
    Table     string            `json:"table"`
    Key       string            `json:"key"`
    Type      ChangeType        `json:"type"`
    OldValue  map[string]string `json:"old_value,omitempty"`
    NewValue  map[string]string `json:"new_value,omitempty"`
}

// ChangeSet is a collection of changes returned by operations
type ChangeSet struct {
    Device       string              `json:"device"`
    Operation    string              `json:"operation"`
    Timestamp    time.Time           `json:"timestamp"`
    Changes      []Change            `json:"changes"`
    AppliedCount int                 `json:"applied_count"` // number of changes successfully written by Apply(); 0 before Apply()
    Verification *VerificationResult `json:"verification,omitempty"` // populated after apply+verify in execute mode
}

func NewChangeSet(device, operation string) *ChangeSet
func (cs *ChangeSet) Add(table, key string, changeType ChangeType, oldValue, newValue map[string]string)
func (cs *ChangeSet) IsEmpty() bool
func (cs *ChangeSet) String() string // human-readable diff format: "+ TABLE|key field=value" / "- TABLE|key" / "~ TABLE|key field: old→new"

// Apply writes all changes in the ChangeSet to CONFIG_DB sequentially.
// Each change is written individually via ConfigDBClient.Set/Delete.
//
// Partial failure: If a write fails at index N, Apply sets cs.AppliedCount = N
// (changes 0..N-1 succeeded) and returns the error. Changes already written
// are NOT rolled back — the caller can use cs.AppliedCount to determine which
// changes succeeded and call cs.Rollback() if needed.
//
// On full success, cs.AppliedCount = len(cs.Changes).
//
// The `d` parameter is the low-level device.Device (accessed via network.Device.Underlying()).
func (cs *ChangeSet) Apply(d *device.Device) error

// Rollback applies the inverse of each applied change in reverse order.
// Status: not yet implemented. No Rollback code exists.
// (changes 0..AppliedCount-1):
//   - ChangeAdd → delete the table/key
//   - ChangeModify → restore OldValue (Set with OldValue fields)
//   - ChangeDelete → recreate with OldValue (Set with OldValue fields)
//
// Rollback is best-effort: it attempts ALL inverse operations, collecting
// errors into a combined errors.Join() error. It does not stop on first
// failure. The caller should verify device state after rollback.
func (cs *ChangeSet) Rollback(d *device.Device) error
```

### 3.6A Verification Types (`pkg/device/verify.go`)

These types live in `pkg/device/verify.go` because `AppDBClient.GetRoute()` returns `*RouteEntry` — placing them in `pkg/network` would create an import cycle (`pkg/device` → `pkg/network`). The `pkg/network` layer re-exports these types for convenience.

These types support the v5 verification architecture: newtron observes single-device state and returns structured data; orchestrators (newtest) assert cross-device correctness.

> **Status: not yet implemented.** `VerificationResult`, `VerificationError`, `RouteEntry`,
> `NextHop`, and `RouteSource` types have no code in `pkg/device/verify.go` (file does not exist).
> `VerifyChangeSet`, `GetRoute`, and `GetRouteASIC` methods are also not yet implemented.
> See also device-lld §3 (AppDBClient), §4 (AsicDBClient), §5.7 (verification methods).

**Consumers:** newtest's step executors are the primary consumers — `verifyProvisioningExecutor` reads `VerificationResult`, `verifyRouteExecutor` reads `RouteEntry`. See newtest LLD §5.4, §5.9.

```go
// VerificationResult reports ChangeSet verification outcome.
type VerificationResult struct {
    Passed int                 // entries that matched
    Failed int                 // entries missing or mismatched
    Errors []VerificationError // details of each failure
}

type VerificationError struct {
    Table    string
    Key      string
    Field    string
    Expected string
    Actual   string // "" if missing
}

// RouteSource indicates which Redis database a route was read from.
type RouteSource string

const (
    RouteSourceAppDB  RouteSource = "APP_DB"
    RouteSourceAsicDB RouteSource = "ASIC_DB"
)

// RouteEntry represents a route read from a device's routing table.
type RouteEntry struct {
    Prefix   string      // "10.1.0.0/31"
    VRF      string      // "default", "Vrf-customer"
    Protocol string      // "bgp", "connected", "static"
    NextHops []NextHop
    Source   RouteSource // AppDB or AsicDB
}

type NextHop struct {
    IP        string // "10.0.0.1" (or "0.0.0.0" for connected)
    Interface string // "Ethernet0", "Vlan500"
}
```

### 3.7 Platform Config (`pkg/device/platform.go`)

Newtron reads the device's SONiC `platform.json` for port validation. The platform config is fetched via SSH and cached on `device.Device`.

```go
// SonicPlatformConfig represents a parsed SONiC platform.json file.
// Located at /usr/share/sonic/device/<platform>/platform.json on the device.
type SonicPlatformConfig struct {
    Interfaces map[string]*PortDefinition `json:"interfaces"`
}

// PortDefinition describes a physical port from platform.json.
type PortDefinition struct {
    Index         int      `json:"index"`
    Lanes         string   `json:"lanes"`                    // e.g., "0,1,2,3"
    Alias         string   `json:"alias,omitempty"`          // e.g., "Eth1/1"
    Speed         int      `json:"speed"`                    // default speed in Mbps
    Speeds        []int    `json:"speeds,omitempty"`         // all supported speeds
    BreakoutModes []string `json:"breakout_modes,omitempty"` // e.g., ["4x25G", "2x50G", "1x100G"]
}

// CreatePortConfig holds options for port creation
type CreatePortConfig struct {
    Name  string            // e.g., "Ethernet0"
    Speed int               // speed in Mbps
    Lanes string            // lane assignment (must match platform.json)
    FEC   string            // FEC mode (rs, fc, none)
    MTU   int               // MTU (default 9100)
    Extra map[string]string // additional PORT fields
}
```

**Platform.json access path:**
```
/usr/share/sonic/device/<DEVICE_METADATA.localhost.platform>/platform.json
```

The parsed result is cached as `device.Device.PlatformConfig`. `LoadPlatformConfig()` fetches and caches it. `GeneratePlatformSpec()` creates a `spec.PlatformSpec` from the parsed data for priming the spec system on first connect to a new hardware platform.

### 3.8 Composite Types (`pkg/network/composite.go`)

Composite mode generates a composite CONFIG_DB offline and delivers it atomically.

```go
// CompositeMode defines how the composite is delivered to the device
type CompositeMode string

const (
    CompositeOverwrite CompositeMode = "overwrite" // Replace entire CONFIG_DB
    CompositeMerge     CompositeMode = "merge"     // Add entries to existing CONFIG_DB
)

// CompositeConfig represents a composite CONFIG_DB configuration
type CompositeConfig struct {
    Tables   map[string]map[string]map[string]string `json:"tables"`   // table -> key -> field -> value
    Metadata CompositeMetadata                       `json:"metadata"`
}

// CompositeMetadata holds provenance information for a composite config
type CompositeMetadata struct {
    Timestamp string        `json:"timestamp"`
    Network   string        `json:"network"`
    Device    string        `json:"device"`
    Mode      CompositeMode `json:"mode"`
    Version   string        `json:"version"`
}

// CompositeBuilder constructs composite configs offline
type CompositeBuilder struct {
    config *CompositeConfig
}

func NewCompositeBuilder(network, device string, mode CompositeMode) *CompositeBuilder

func (cb *CompositeBuilder) AddBGPGlobals(entry BGPGlobalsEntry) *CompositeBuilder
func (cb *CompositeBuilder) AddPeerGroup(name string, entry BGPPeerGroupEntry) *CompositeBuilder
func (cb *CompositeBuilder) AddPortConfig(name string, entry PortEntry) *CompositeBuilder
func (cb *CompositeBuilder) AddService(intf, service string, opts ApplyServiceOpts) *CompositeBuilder
func (cb *CompositeBuilder) AddRouteRedistribution(vrf, protocol, af string, entry RouteRedistributeEntry) *CompositeBuilder
func (cb *CompositeBuilder) AddEntry(table, key string, fields map[string]string) *CompositeBuilder
func (cb *CompositeBuilder) Build() *CompositeConfig

// CompositeDeliveryResult reports the outcome of composite delivery
type CompositeDeliveryResult struct {
    Applied  int        `json:"applied"`           // entries successfully written
    Skipped  int        `json:"skipped"`           // entries skipped (merge conflict)
    Failed   int        `json:"failed"`            // entries that failed to write
    Error    error      `json:"error,omitempty"`
    ChangeSet *ChangeSet `json:"changeset,omitempty"` // generated ChangeSet for verification
}

// ApplyServiceOpts holds the parameters for applying a service within
// a composite context. Used by CompositeBuilder.AddService().
//
// ApplyServiceOpts holds parameters for applying a service to an interface.
// Used by both Interface.ApplyService() and CompositeBuilder.AddService().
//
// Interface.ApplyService() returns a ChangeSet for preview; the caller applies
// via cs.Apply(). CompositeBuilder operates offline, collecting entries into a
// CompositeConfig without connecting to a device. Both use the same opts struct.
type ApplyServiceOpts struct {
    IPAddr string // IP address (CIDR) to assign, e.g. "10.1.1.1/30"
    PeerAS int    // Peer AS number (for BGP services with peer_as="request")
}
```

## 4. Device Connection Layer

The device connection layer — SSH tunnels, Redis clients (CONFIG_DB, STATE_DB, APP_DB, ASIC_DB), connection flow, write paths, and config persistence — is documented in a separate document:

**See [Device Layer LLD](device-lld.md).**

Summary of what's covered there:

| Device LLD Section | Topic |
|--------------------|-------|
| §1 SSH Tunnel | Port-forwarding through SSH to in-VM Redis |
| §2 StateDB | STATE_DB (DB 6) operational state access |
| §3 APP_DB | APP_DB (DB 0) route table reads for verification |
| §4 ASIC_DB | ASIC_DB (DB 1) SAI object chain resolution |
| §5 Redis Integration | Connection flow, write paths (sequential + pipeline), disconnect |
| §6 Config Persistence | Runtime-only semantics, `config save -y` |

## 5. Operation Implementations (Methods on Objects)

Operations are methods on the objects they operate on. This follows true OO design where operations belong to their objects rather than being separate Command pattern structs.

### Execution Model

All operations that return `*ChangeSet` compute the changes without writing. The caller previews the ChangeSet, then calls `cs.Apply(d.Underlying())` to execute. Lock acquisition, verification, and unlock are the caller's responsibility.

**Caller execution pattern:**

```
Lock → cs.Apply(d.Underlying()) → VerifyChangeSet → Unlock → return
```

The lock is scoped to a single operation — not held across multiple operations or for the duration of a session. This ensures:
1. Minimal lock hold time — only during the critical mutation + verification window
2. No stale locks from long-running sessions
3. Clear failure semantics — if lock acquisition fails, the operation fails immediately

```go
// Pattern: operation returns ChangeSet, caller applies it.
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    d := i.Device()
    cs := NewChangeSet(d.Name(), "interface.apply-service")
    // ... build ChangeSet entries ...
    return cs, nil
}

// Caller applies the ChangeSet:
//   cs, err := intf.ApplyService(ctx, "customer-l3", opts)
//   // preview cs.String() ...
//   d.Lock()
//   cs.Apply(d.Underlying())
//   d.Underlying().VerifyChangeSet(ctx, cs)
//   d.Unlock()
```

**Disconnect safety net:** `Device.Disconnect()` releases the lock if still held (e.g., after a panic during operation execution). This is a safety measure — normal operation always releases the lock within the operation method.

### 5.1 Interface Operations (`pkg/network/interface_ops.go`)

Interface operations are methods on the `Interface` type. All operations return a `*ChangeSet` for preview/execution.

**Complete Method List:**

```go
// ============================================================================
// Service Management
// ============================================================================

// ApplyService applies a service definition to this interface.
// Creates VRF, ACLs, IP configuration, and service binding tracking.
// Returns the ChangeSet for preview; caller applies via cs.Apply().
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error)

// RemoveService removes the service from this interface.
// Uses DependencyChecker to safely clean up shared resources (ACLs, VRFs).
// Returns the ChangeSet for preview; caller applies via cs.Apply().
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error)
```

**RemoveService pseudocode** — reverse of ApplyService, using DependencyChecker
(§5.5) to avoid deleting shared resources still referenced by other interfaces:

```go
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error) {
    d := i.Device()
    if d.Underlying() == nil {
        return nil, util.ErrNotConnected
    }

    binding, ok := d.Underlying().ConfigDB.NewtronServiceBinding[i.name]
    if !ok {
        return nil, fmt.Errorf("no service bound to %s", i.name)
    }

    svc, err := i.Network().GetService(binding.ServiceName)
    if err != nil {
        return nil, fmt.Errorf("service spec %q: %w", binding.ServiceName, err)
    }

    cs := NewChangeSet(d.Name(), "interface.remove-service")
    dc := NewDependencyChecker(d.Underlying().ConfigDB)

    // 1. Remove service binding (always safe — owned by this interface)
    cs.Add("NEWTRON_SERVICE_BINDING", i.name, ChangeDelete,
        map[string]string{"service": binding.ServiceName}, nil)

    // 2. Remove IP binding entries (INTERFACE|<name>|<ip>)
    for key := range d.Underlying().ConfigDB.Interface {
        if strings.HasPrefix(key, i.name+"|") {
            cs.Add("INTERFACE", key, ChangeDelete, nil, nil)
        }
    }

    // 3. Remove BGP neighbors for this interface
    neighborIP, _ := i.DeriveNeighborIP()
    if neighborIP != "" {
        vrfName := util.DeriveVRFName(svc.VRFType, binding.ServiceName, i.name)
        bgpKey := fmt.Sprintf("%s|%s", vrfName, neighborIP)
        // Delete BGP_NEIGHBOR_AF entries first (child before parent)
        for afKey := range d.Underlying().ConfigDB.BGPNeighborAF {
            if strings.HasPrefix(afKey, bgpKey+"|") {
                cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeDelete, nil, nil)
            }
        }
        if dc.CanDeleteBGPNeighbor(bgpKey) {
            cs.Add("BGP_NEIGHBOR", bgpKey, ChangeDelete, nil, nil)
        }
    }

    // 4. Remove ACLs if no other ports reference them
    aclInName := util.DeriveACLName(binding.ServiceName, "in")
    aclOutName := util.DeriveACLName(binding.ServiceName, "out")
    for _, aclName := range []string{aclInName, aclOutName} {
        if dc.CanDeleteACL(aclName, i.name) {
            // No other ports reference this ACL — delete rules then table
            for ruleKey := range d.Underlying().ConfigDB.ACLRule {
                if strings.HasPrefix(ruleKey, aclName+"|") {
                    cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
                }
            }
            cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
        } else {
            // Other ports still reference this ACL — just remove this interface from ports list
            existing := d.Underlying().ConfigDB.ACLTable[aclName]
            ports := strings.Split(existing.Ports, ",")
            filtered := removeFromSlice(ports, i.name)
            cs.Add("ACL_TABLE", aclName, ChangeModify,
                map[string]string{"ports": existing.Ports},
                map[string]string{"ports": strings.Join(filtered, ",")})
        }
    }

    // 5. Remove VRF binding from INTERFACE base entry
    cs.Add("INTERFACE", i.name, ChangeDelete, nil, nil)

    // 6. Remove VRF if no other interfaces use it
    vrfName := util.DeriveVRFName(svc.VRFType, binding.ServiceName, i.name)
    if dc.CanDeleteVRF(vrfName, i.name) {
        cs.Add("VRF", vrfName, ChangeDelete, nil, nil)
        // Also remove VXLAN_TUNNEL_MAP for L3VNI if present
    }

    // 7. L2/IRB-specific: remove VLAN member, VLAN if empty
    if svc.ServiceType == ServiceTypeL2 || svc.ServiceType == ServiceTypeIRB {
        vlanName := util.DeriveVLANName(binding.ServiceName)
        memberKey := fmt.Sprintf("%s|%s", vlanName, i.name)
        cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)
        if dc.CanDeleteVLAN(vlanName, i.name) {
            cs.Add("VLAN", vlanName, ChangeDelete, nil, nil)
            // Remove VXLAN_TUNNEL_MAP for L2VNI
        }
    }

    // 8. Remove QoS/PORT_QOS_MAP entry for this interface
    cs.Add("PORT_QOS_MAP", i.name, ChangeDelete, nil, nil)

    // Deletion order within the ChangeSet is children-before-parents:
    //   IP entries before INTERFACE, MEMBER before VLAN, AF before NEIGHBOR

    return cs, nil
}
```

// RefreshService reapplies the service to sync with current definition.
// Compares current interface config with current service spec and
// generates changes to synchronize (updated filters, QoS, etc.).
// Returns the ChangeSet for preview; caller applies via cs.Apply().
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error)

// ============================================================================
// Property Setting
// ============================================================================

// Set sets a single property on this interface.
// Supported properties: mtu, speed, admin-status, description
func (i *Interface) Set(ctx context.Context, property, value string) (*ChangeSet, error)

// SetIP configures an IP address on this interface.
func (i *Interface) SetIP(ctx context.Context, ipAddr string) (*ChangeSet, error)

// SetVRF binds this interface to a VRF.
func (i *Interface) SetVRF(ctx context.Context, vrfName string) (*ChangeSet, error)

// Configure applies multiple configuration options at once.
func (i *Interface) Configure(ctx context.Context, opts InterfaceConfig) (*ChangeSet, error)

// ============================================================================
// ACL Binding
// ============================================================================

// BindACL binds an ACL to this interface.
// ACLs are shared - adds this interface to the ACL's ports list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error)

// UnbindACL removes an ACL binding from this interface.
// If last user, deletes the ACL; otherwise just removes from ports list.
func (i *Interface) UnbindACL(ctx context.Context, aclName string) (*ChangeSet, error)

// ============================================================================
// LAG/VLAN Member Management
// ============================================================================

// AddMember adds a member interface to this LAG or VLAN.
func (i *Interface) AddMember(ctx context.Context, memberIntf string, tagged bool) (*ChangeSet, error)

// RemoveMember removes a member interface from this LAG or VLAN.
func (i *Interface) RemoveMember(ctx context.Context, memberIntf string) (*ChangeSet, error)

// ============================================================================
// BGP Neighbor Management (Direct Peering)
// ============================================================================

// AddBGPNeighbor adds a direct BGP neighbor on this interface.
// Uses interface IP as update-source. Auto-derives neighbor for /30, /31.
func (i *Interface) AddBGPNeighbor(ctx context.Context, cfg DirectBGPNeighborConfig) (*ChangeSet, error)

// AddBGPNeighborWithConfig adds a BGP neighbor with extended config options.
func (i *Interface) AddBGPNeighborWithConfig(ctx context.Context, cfg BGPNeighborConfig) (*ChangeSet, error)

// RemoveBGPNeighbor removes a direct BGP neighbor from this interface.
func (i *Interface) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error)

// DeriveNeighborIP derives the BGP neighbor IP from this interface's IP.
// Only works for point-to-point links (/30 or /31 subnets).
func (i *Interface) DeriveNeighborIP() (string, error)

// ============================================================================
// Route-Map Binding
// ============================================================================

// SetRouteMap binds a route-map to a BGP neighbor on this interface.
// Direction is "in" or "out". Updates BGP_NEIGHBOR_AF route_map_in/out field.
func (i *Interface) SetRouteMap(ctx context.Context, neighborIP, direction, routeMapName string) (*ChangeSet, error)

// ============================================================================
// MAC-VPN Binding (for VLAN interfaces)
// ============================================================================

// BindMACVPN binds this VLAN interface to a MAC-VPN definition.
// Configures L2VNI mapping and ARP suppression.
func (i *Interface) BindMACVPN(ctx context.Context, macvpnName string, macvpnDef *spec.MACVPNSpec) (*ChangeSet, error)

// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
func (i *Interface) UnbindMACVPN(ctx context.Context) (*ChangeSet, error)
```

**ApplyService pseudocode** (shows full translation rules per service type):

```go
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    d := i.Device()
    ipAddr := opts.IPAddr

    if !d.IsConnected() {
        return nil, util.ErrNotConnected
    }

    // Get service definition from Network (via parent chain)
    svc, err := i.Network().GetService(serviceName)
    if err != nil {
        return nil, fmt.Errorf("service not found: %w", err)
    }

    // Validate service type
    validTypes := map[string]bool{
        ServiceTypeL2: true, ServiceTypeL3: true, ServiceTypeIRB: true,
    }
    if !validTypes[svc.ServiceType] {
        return nil, fmt.Errorf("unsupported service type %q (must be l2, l3, or irb)", svc.ServiceType)
    }

    cs := NewChangeSet(d.Name(), "interface.apply-service")

    // ====================================================================
    // L3 service translation (ServiceType == "l3")
    // ====================================================================
    if svc.ServiceType == ServiceTypeL3 || svc.ServiceType == ServiceTypeIRB {
        vrfName := util.DeriveVRFName(svc.VRFType, serviceName, i.name)

        if svc.IPVPN != "" {
            // EVPN path: VRF with L3VNI, VXLAN tunnel map, BGP globals
            ipvpnDef, err := i.Network().GetIPVPN(svc.IPVPN)
            if err != nil {
                return nil, fmt.Errorf("IPVPN %q: %w", svc.IPVPN, err)
            }

            // 1. VRF
            cs.Add("VRF", vrfName, ChangeAdd, nil, map[string]string{
                "vni": fmt.Sprintf("%d", ipvpnDef.L3VNI),
            })

            // 2. VXLAN_TUNNEL_MAP for L3VNI → VRF
            mapKey := fmt.Sprintf("vtep1|map_%d_%s", ipvpnDef.L3VNI, vrfName)
            cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
                "vni":  fmt.Sprintf("%d", ipvpnDef.L3VNI),
                "vrf":  vrfName,
            })

            // 3. INTERFACE: VRF binding + IP address
            cs.Add("INTERFACE", i.name, ChangeAdd, nil, map[string]string{
                "vrf_name": vrfName,
            })

            // 4. BGP_GLOBALS_AF for the VRF (L3VNI route targets)
            cs.Add("BGP_GLOBALS_AF", fmt.Sprintf("%s|ipv4_unicast", vrfName), ChangeAdd, nil, map[string]string{
                "advertise_ipv4_unicast": "true",
            })
            cs.Add("BGP_GLOBALS_AF", fmt.Sprintf("%s|l2vpn_evpn", vrfName), ChangeAdd, nil, map[string]string{
                "advertise-all-vni":            "true",
                "route_target_import_evpn":     strings.Join(ipvpnDef.ImportRT, ","),
                "route_target_export_evpn":     strings.Join(ipvpnDef.ExportRT, ","),
            })
        } else {
            // Non-EVPN path: no VRF (global routing table), no VXLAN
            // INTERFACE binding with no vrf_name — uses default VRF
            cs.Add("INTERFACE", i.name, ChangeAdd, nil, nil)
        }

        if ipAddr != "" {
            cs.Add("INTERFACE", fmt.Sprintf("%s|%s", i.name, ipAddr), ChangeAdd, nil, nil)
        }
    }

    // ====================================================================
    // L2 service translation (ServiceType == "l2" or IRB L2 portion)
    // ====================================================================
    if svc.ServiceType == ServiceTypeL2 || svc.ServiceType == ServiceTypeIRB {
        macvpnDef, err := i.Network().GetMACVPN(svc.MACVPN)
        if err != nil {
            return nil, fmt.Errorf("MACVPN %q: %w", svc.MACVPN, err)
        }

        // 1. VLAN
        vlanName := fmt.Sprintf("Vlan%d", macvpnDef.VLAN)
        cs.Add("VLAN", vlanName, ChangeAdd, nil, map[string]string{
            "vlanid": fmt.Sprintf("%d", macvpnDef.VLAN),
        })

        // 2. VLAN_MEMBER
        // L2 services use untagged (access port); IRB uses tagged (trunk port)
        taggingMode := "untagged"
        if svc.ServiceType == ServiceTypeIRB {
            taggingMode = "tagged"
        }
        cs.Add("VLAN_MEMBER", fmt.Sprintf("%s|%s", vlanName, i.name), ChangeAdd, nil, map[string]string{
            "tagging_mode": taggingMode,
        })

        // 3. VXLAN_TUNNEL_MAP for L2VNI → VLAN
        mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
        cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
            "vni":  fmt.Sprintf("%d", macvpnDef.L2VNI),
            "vlan": vlanName,
        })

        // 4. SUPPRESS_VLAN_NEIGH (ARP suppression)
        if macvpnDef.ARPSuppression {
            cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeAdd, nil, map[string]string{
                "suppress": "on",
            })
        }
    }

    // ====================================================================
    // IRB additions (ServiceType == "irb")
    // ====================================================================
    if svc.ServiceType == ServiceTypeIRB {
        macvpnDef, err := i.Network().GetMACVPN(svc.MACVPN)
        if err != nil {
            return nil, fmt.Errorf("MACVPN %q (IRB): %w", svc.MACVPN, err)
        }
        vlanName := fmt.Sprintf("Vlan%d", macvpnDef.VLAN)
        vrfName := util.DeriveVRFName(svc.VRFType, serviceName, i.name)

        // 1. VLAN_INTERFACE: SVI with VRF binding
        cs.Add("VLAN_INTERFACE", vlanName, ChangeAdd, nil, map[string]string{
            "vrf_name": vrfName,
        })

        // 2. Anycast gateway (SAG)
        if svc.AnycastGateway != "" {
            cs.Add("VLAN_INTERFACE", fmt.Sprintf("%s|%s", vlanName, svc.AnycastGateway),
                ChangeAdd, nil, nil)
            cs.Add("SAG", fmt.Sprintf("%s|IPv4", vlanName), ChangeAdd, nil, map[string]string{
                "gwip": strings.Split(svc.AnycastGateway, "/")[0],
            })
            if svc.AnycastMAC != "" {
                cs.Add("SAG_GLOBAL", "global", ChangeAdd, nil, map[string]string{
                    "gateway_mac": svc.AnycastMAC,
                })
            }
        }
    }

    // ====================================================================
    // ACL translation (all service types)
    // ====================================================================
    if svc.IngressFilter != "" {
        aclName := util.DeriveACLName(serviceName, "in")
        filterSpec, err := i.Network().GetFilterSpec(svc.IngressFilter)
        if err != nil {
            return nil, fmt.Errorf("ingress filter %q: %w", svc.IngressFilter, err)
        }
        i.generateACLEntries(cs, aclName, filterSpec, "ingress")
    }
    if svc.EgressFilter != "" {
        aclName := util.DeriveACLName(serviceName, "out")
        filterSpec, err := i.Network().GetFilterSpec(svc.EgressFilter)
        if err != nil {
            return nil, fmt.Errorf("egress filter %q: %w", svc.EgressFilter, err)
        }
        i.generateACLEntries(cs, aclName, filterSpec, "egress")
    }

    // ====================================================================
    // QoS binding (all service types)
    // ====================================================================
    if svc.QoSProfile != "" {
        qos := i.Network().GetQoSProfile(svc.QoSProfile)
        cs.Add("PORT_QOS_MAP", i.name, ChangeAdd, nil, map[string]string{
            "dscp_to_tc_map": fmt.Sprintf("[DSCP_TO_TC_MAP|%s]", qos.DSCPToTCMap),
            "tc_to_queue_map": fmt.Sprintf("[TC_TO_QUEUE_MAP|%s]", qos.TCToQueueMap),
        })
    }

    // ====================================================================
    // BGP neighbor (for services with routing)
    // ====================================================================
    if svc.Routing != nil && svc.Routing.Protocol == "bgp" {
        vrfName := util.DeriveVRFName(svc.VRFType, serviceName, i.name)
        neighborIP, _ := i.DeriveNeighborIP()
        peerAS := svc.Routing.PeerAS
        cs.Add("BGP_NEIGHBOR", fmt.Sprintf("%s|%s", vrfName, neighborIP), ChangeAdd, nil, map[string]string{
            "asn":        peerAS,
            "local_asn":  fmt.Sprintf("%d", d.ASNumber()),
            "local_addr": strings.Split(ipAddr, "/")[0],
        })
        cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("%s|%s|ipv4_unicast", vrfName, neighborIP), ChangeAdd, nil, map[string]string{
            "activate": "true",
        })
    }

    // ====================================================================
    // Service binding (always, for tracking)
    // ====================================================================
    bindingFields := map[string]string{
        "service_name": serviceName,
        "ip_address":   ipAddr,
        "applied_at":   time.Now().Format(time.RFC3339),
        "applied_by":   os.Getenv("USER"),
    }
    if svc.IPVPN != "" {
        bindingFields["ipvpn"] = svc.IPVPN
        bindingFields["vrf_name"] = util.DeriveVRFName(svc.VRFType, serviceName, i.name)
    }
    if svc.MACVPN != "" {
        bindingFields["macvpn"] = svc.MACVPN
    }
    cs.Add("NEWTRON_SERVICE_BINDING", i.name, ChangeAdd, nil, bindingFields)

    return cs, nil
}
```

**ACL entry generation helper:**

```go
// generateACLEntries adds ACL_TABLE + ACL_RULE entries to the ChangeSet.
// If the ACL already exists in CONFIG_DB, appends this interface to the ports list
// rather than creating a new ACL_TABLE entry.
func (i *Interface) generateACLEntries(cs *ChangeSet, aclName string, filter *spec.FilterSpec, stage string) {
    existing := i.Device().Underlying().ConfigDB.ACLTable[aclName]
    if existing.Type != "" {
        // ACL exists — add this interface to ports list
        ports := strings.Split(existing.Ports, ",")
        if !contains(ports, i.name) {
            ports = append(ports, i.name)
        }
        cs.Add("ACL_TABLE", aclName, ChangeModify,
            map[string]string{"ports": existing.Ports},
            map[string]string{"ports": strings.Join(ports, ",")})
    } else {
        // New ACL
        cs.Add("ACL_TABLE", aclName, ChangeAdd, nil, map[string]string{
            "type":        filter.Type,
            "stage":       stage,
            "description": filter.Description,
            "ports":       i.name,
        })
    }

    // ACL_RULE entries (one per filter rule)
    for _, rule := range filter.Rules {
        ruleKey := fmt.Sprintf("%s|RULE_%d", aclName, rule.Sequence)
        fields := map[string]string{
            "PRIORITY":     fmt.Sprintf("%d", 1000-rule.Sequence),
            "PACKET_ACTION": strings.ToUpper(rule.Action),
        }
        if rule.SrcIP != "" { fields["SRC_IP"] = rule.SrcIP }
        if rule.DstIP != "" { fields["DST_IP"] = rule.DstIP }
        if rule.Protocol != "" { fields["IP_PROTOCOL"] = rule.Protocol }
        if rule.SrcPort != "" { fields["L4_SRC_PORT"] = rule.SrcPort }
        if rule.DstPort != "" { fields["L4_DST_PORT"] = rule.DstPort }
        if rule.DSCP != "" { fields["DSCP"] = rule.DSCP }
        cs.Add("ACL_RULE", ruleKey, ChangeAdd, nil, fields)
    }
}
```

**DependencyChecker** (`pkg/network/interface_ops.go`):

Used by `RemoveService` to safely clean up shared resources. Reference counting is done via CONFIG_DB scan — no separate tracking database.

```go
// DependencyChecker determines whether shared resources (ACLs, VRFs, VLANs,
// VNI mappings, BGP neighbors) can be safely deleted when a service is removed
// from an interface. It scans CONFIG_DB to count remaining references.
type DependencyChecker struct {
    configDB *device.ConfigDB
}

// NewDependencyChecker creates a DependencyChecker from the current CONFIG_DB snapshot.
func NewDependencyChecker(configDB *device.ConfigDB) *DependencyChecker {
    return &DependencyChecker{configDB: configDB}
}

// CanDeleteACL returns true if the ACL has no remaining port bindings
// after removing the given interface. Checks ACL_TABLE[aclName].ports.
func (dc *DependencyChecker) CanDeleteACL(aclName, removingInterface string) bool {
    entry, ok := dc.configDB.ACLTable[aclName]
    if !ok {
        return false // ACL doesn't exist
    }
    ports := strings.Split(entry.Ports, ",")
    remaining := 0
    for _, p := range ports {
        if strings.TrimSpace(p) != removingInterface {
            remaining++
        }
    }
    return remaining == 0
}

// CanDeleteVRF returns true if no other interfaces are bound to this VRF.
// Scans INTERFACE table for vrf_name matches, excluding the removing interface.
func (dc *DependencyChecker) CanDeleteVRF(vrfName, removingInterface string) bool {
    for intfName, entry := range dc.configDB.Interface {
        if intfName == removingInterface {
            continue
        }
        if entry.VRFName == vrfName {
            return false
        }
    }
    return true
}

// CanDeleteVLAN returns true if no members remain in the VLAN
// after removing the given interface. Checks VLAN_MEMBER table.
// Also checks VLAN_INTERFACE (SVI bindings) — VLAN cannot be deleted
// if an SVI with IP addresses exists.
func (dc *DependencyChecker) CanDeleteVLAN(vlanName, removingInterface string) bool {
    prefix := vlanName + "|"
    for key := range dc.configDB.VLANMember {
        if strings.HasPrefix(key, prefix) {
            member := strings.TrimPrefix(key, prefix)
            if member != removingInterface {
                return false
            }
        }
    }
    // Check for SVI (VLAN_INTERFACE entries)
    if _, hasSVI := dc.configDB.VLANInterface[vlanName]; hasSVI {
        return false
    }
    return true
}

// CanDeleteVNIMapping returns true if no VRF or VLAN references the given VNI.
// Scans VXLAN_TUNNEL_MAP for entries mapping to this VNI.
func (dc *DependencyChecker) CanDeleteVNIMapping(vni string) bool {
    for _, entry := range dc.configDB.VXLANTunnelMap {
        if entry.VNI == vni {
            return false
        }
    }
    return true
}

// CanDeleteBGPNeighbor returns true if the BGP neighbor is not referenced
// by any BGP_NEIGHBOR_AF entries. Scans BGP_NEIGHBOR_AF table for keys
// prefixed with the neighbor IP.
func (dc *DependencyChecker) CanDeleteBGPNeighbor(neighborIP string) bool {
    prefix := neighborIP + "|"
    for key := range dc.configDB.BGPNeighborAF {
        if strings.HasPrefix(key, prefix) {
            return false
        }
    }
    return true
}

// CanDeleteServiceBinding returns true if the NEWTRON_SERVICE_BINDING
// entry exists for the given interface. This is always safe to delete
// when RemoveService is called — it's the binding itself, not a shared resource.
func (dc *DependencyChecker) CanDeleteServiceBinding(interfaceName string) bool {
    _, exists := dc.configDB.NewtronServiceBinding[interfaceName]
    return exists
}
```

### 5.2 Device Operations (`pkg/network/device_ops.go`)

Device operations are methods on the `Device` type. All operations return a `*ChangeSet` for preview/execution.

**Complete Method List:**

```go
// ============================================================================
// VLAN Management
// ============================================================================

func (d *Device) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error)
func (d *Device) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error)
func (d *Device) AddVLANMember(ctx context.Context, vlanID int, port string, tagged bool) (*ChangeSet, error)

// ============================================================================
// PortChannel (LAG) Management
// ============================================================================

func (d *Device) CreatePortChannel(ctx context.Context, name string, opts PortChannelConfig) (*ChangeSet, error)
func (d *Device) DeletePortChannel(ctx context.Context, name string) (*ChangeSet, error)
func (d *Device) AddPortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error)
func (d *Device) RemovePortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error)

// ============================================================================
// VRF Management
// ============================================================================

func (d *Device) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error)
func (d *Device) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error)

// ============================================================================
// ACL Management
// ============================================================================

func (d *Device) CreateACLTable(ctx context.Context, name string, opts ACLTableConfig) (*ChangeSet, error)
func (d *Device) DeleteACLTable(ctx context.Context, name string) (*ChangeSet, error)
func (d *Device) AddACLRule(ctx context.Context, tableName string, rule ACLRuleEntry) (*ChangeSet, error)
func (d *Device) UnbindACLFromPort(ctx context.Context, aclName, port string) (*ChangeSet, error)

// ============================================================================
// EVPN/VTEP Management
// ============================================================================

func (d *Device) CreateVTEP(ctx context.Context, opts VTEPConfig) (*ChangeSet, error)
func (d *Device) DeleteVTEP(ctx context.Context, name string) (*ChangeSet, error)
// Deprecated: use SetupRouteReflector instead.
func (d *Device) SetupBGPEVPN(ctx context.Context, neighbors []string) (*ChangeSet, error)
func (d *Device) AddLoopbackBGPNeighbor(ctx context.Context, cfg LoopbackBGPNeighborConfig) (*ChangeSet, error)
func (d *Device) MapL2VNI(ctx context.Context, vlanID, vni int) (*ChangeSet, error)
func (d *Device) MapL3VNI(ctx context.Context, vrfName string, vni int) (*ChangeSet, error)
func (d *Device) UnmapVNI(ctx context.Context, vni int) (*ChangeSet, error)

// ============================================================================
// Health Checks and Maintenance
// ============================================================================

// RunHealthChecks runs health checks on the device.
// checks is a variadic filter: "bgp", "interfaces", "evpn", "lag", "vxlan".
// If no checks are specified, all checks run.
func (d *Device) RunHealthChecks(ctx context.Context, checks ...string) ([]HealthCheckResult, error)

// ApplyBaseline applies a baseline configlet to the device.
// vars is a list of "key=value" strings for template substitution.
func (d *Device) ApplyBaseline(ctx context.Context, configletName string, vars []string) (*ChangeSet, error)

// Cleanup identifies and removes orphaned configurations.
// cleanupType can be: "acl", "vrf", "vni", or "" for all.
func (d *Device) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error)

// ============================================================================
// Query Methods (no ChangeSet returned)
// ============================================================================

// ListVLANs returns VLAN IDs present in CONFIG_DB.
func (d *Device) ListVLANs() []int

// ListVRFs returns VRF names present in CONFIG_DB.
func (d *Device) ListVRFs() []string

// ListPortChannels returns PortChannel names present in CONFIG_DB.
func (d *Device) ListPortChannels() []string

// ListInterfaces returns interface names (Ethernet*, PortChannel*, Loopback*).
func (d *Device) ListInterfaces() []string

// ListACLTables returns ACL table names present in CONFIG_DB.
func (d *Device) ListACLTables() []string

// ListBGPNeighbors returns BGP neighbor IPs from CONFIG_DB.
func (d *Device) ListBGPNeighbors() []string

// GetOrphanedACLs returns ACL tables not bound to any interface.
func (d *Device) GetOrphanedACLs() []string

// VTEPSourceIP returns the VTEP source IP (loopback address).
func (d *Device) VTEPSourceIP() string

// --- Client accessors (for newtest executors that need direct DB access) ---

// StateDBClient returns the STATE_DB client for direct queries.
// Returns nil if STATE_DB connection failed (non-fatal — see device LLD §5.1).
func (d *Device) StateDBClient() *StateDBClient

// SSHClient returns the underlying ssh.Client for opening command sessions.
// Returns nil if no SSH tunnel (direct connection mode).
func (d *Device) SSHClient() *ssh.Client {
    if d.tunnel == nil {
        return nil
    }
    return d.tunnel.SSHClient()
}

// ============================================================================
// Verification Methods
// Status: not yet implemented. No verify.go exists in pkg/network/.
// ============================================================================

// VerifyChangeSet re-reads CONFIG_DB through a fresh connection and confirms
// every entry in the ChangeSet was applied. For each Change in the ChangeSet:
//   - ChangeAdd/ChangeModify: reads the table/key, asserts every field in NewValue
//     is present with the same value (superset match — Redis may have additional
//     fields set by SONiC daemons or prior operations; these are ignored). If a
//     NewValue field is missing or has a different value in Redis, the entry is
//     marked as failed.
//   - ChangeDelete: reads the table/key, asserts the key does not exist in Redis.
//     If the key still exists (regardless of field values), the entry is marked
//     as failed.
// Returns a VerificationResult listing any missing or mismatched entries.
//
// "Fresh connection" means: creates a new ConfigDBClient on the existing SSH
// tunnel (same tunnel.LocalAddr()), performs an independent GetAll() read,
// and compares against the ChangeSet. This avoids reading from the cached
// d.ConfigDB that was updated by Apply() — the fresh read confirms that
// Redis actually persisted the writes. The temporary client is closed after
// verification.
func (d *Device) VerifyChangeSet(ctx context.Context, cs *ChangeSet) (*VerificationResult, error)

// GetRoute reads a route from APP_DB (Redis DB 0) via the AppDBClient.
// Parses the comma-separated nexthop/ifname fields into []NextHop.
// Returns nil RouteEntry (not error) if the prefix is not present.
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain: SAI_ROUTE_ENTRY → SAI_NEXT_HOP_GROUP → SAI_NEXT_HOP.
// Returns nil RouteEntry (not error) if not programmed in ASIC.
//
// Algorithm (SAI OID chain resolution):
//   1. Lookup SAI_ROUTE_ENTRY in ASIC_DB by JSON key containing vrf OID + prefix
//   2. Read the "SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID" field → next_hop_group OID
//   3. If OID type is SAI_NEXT_HOP (single path): read IP from that OID, return
//   4. If OID type is SAI_NEXT_HOP_GROUP (ECMP):
//      a. SCAN for SAI_NEXT_HOP_GROUP_MEMBER entries with matching group OID
//      b. For each member, read "SAI_NEXT_HOP_GROUP_MEMBER_ATTR_NEXT_HOP_ID"
//      c. For each next_hop OID, read "SAI_NEXT_HOP_ATTR_IP" → nexthop IP
//   5. Assemble NextHop list and return RouteEntry
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

// ============================================================================
// BGP Management
// ============================================================================

// SetBGPGlobals configures BGP global settings (ASN, router-id, flags).
func (d *Device) SetBGPGlobals(ctx context.Context, cfg BGPGlobalsConfig) (*ChangeSet, error)

// SetupRouteReflector performs full RR setup: all 3 AFs, cluster-id, RR client, next-hop-self.
// Replaces SetupBGPEVPN (which only did l2vpn_evpn).
//
// Pseudo-code:
//   BGP_GLOBALS "default": local_asn, router_id, rr_cluster_id
//   For each neighbor:
//     BGP_NEIGHBOR "default|<ip>": asn, local_addr, admin_status
//     BGP_NEIGHBOR_AF "default|<ip>|ipv4_unicast": activate, rr_client, next_hop_self
//     BGP_NEIGHBOR_AF "default|<ip>|ipv6_unicast": activate, rr_client, next_hop_self
//     BGP_NEIGHBOR_AF "default|<ip>|l2vpn_evpn": activate, rr_client
//   BGP_GLOBALS_AF "default|ipv4_unicast": max_ibgp_paths
//   BGP_GLOBALS_AF "default|ipv6_unicast": max_ibgp_paths
//   BGP_GLOBALS_AF "default|l2vpn_evpn": advertise-all-vni
//   ROUTE_REDISTRIBUTE "default|connected|bgp|ipv4": (loopback + service subnets)
//   ROUTE_REDISTRIBUTE "default|connected|bgp|ipv6": (if IPv6 enabled)
func (d *Device) SetupRouteReflector(ctx context.Context, cfg SetupRouteReflectorConfig) (*ChangeSet, error)

// ConfigurePeerGroup creates or updates a BGP peer group template.
func (d *Device) ConfigurePeerGroup(ctx context.Context, name string, cfg PeerGroupConfig) (*ChangeSet, error)

// DeletePeerGroup removes a BGP peer group.
func (d *Device) DeletePeerGroup(ctx context.Context, name string) (*ChangeSet, error)

// AddRouteRedistribution redistributes connected/static into BGP.
func (d *Device) AddRouteRedistribution(ctx context.Context, cfg RouteRedistributionConfig) (*ChangeSet, error)

// RemoveRouteRedistribution removes redistribution.
func (d *Device) RemoveRouteRedistribution(ctx context.Context, vrf, protocol, af string) (*ChangeSet, error)

// AddRouteMap creates a route-map with match/set rules.
func (d *Device) AddRouteMap(ctx context.Context, name string, cfg RouteMapConfig) (*ChangeSet, error)

// DeleteRouteMap removes a route-map (all sequences).
func (d *Device) DeleteRouteMap(ctx context.Context, name string) (*ChangeSet, error)

// AddPrefixSet creates a prefix list for route-map matching.
func (d *Device) AddPrefixSet(ctx context.Context, name string, cfg PrefixSetConfig) (*ChangeSet, error)

// DeletePrefixSet removes a prefix list (all sequences).
func (d *Device) DeletePrefixSet(ctx context.Context, name string) (*ChangeSet, error)

// AddBGPNetwork adds a BGP network statement.
func (d *Device) AddBGPNetwork(ctx context.Context, vrf, af, prefix string) (*ChangeSet, error)

// RemoveBGPNetwork removes a BGP network statement.
func (d *Device) RemoveBGPNetwork(ctx context.Context, vrf, af, prefix string) (*ChangeSet, error)

// ============================================================================
// Port Management (v3 — platform.json validated)
// ============================================================================

// CreatePort creates a PORT entry validated against platform.json.
func (d *Device) CreatePort(ctx context.Context, name string, cfg CreatePortConfig) (*ChangeSet, error)

// DeletePort removes a PORT entry.
func (d *Device) DeletePort(ctx context.Context, name string) (*ChangeSet, error)

// BreakoutPort applies a breakout mode (creates child ports, removes parent).
func (d *Device) BreakoutPort(ctx context.Context, name string, cfg BreakoutConfig) (*ChangeSet, error)

// LoadPlatformConfig fetches and caches platform.json from the device via SSH.
func (d *Device) LoadPlatformConfig(ctx context.Context) error

// GeneratePlatformSpec creates a spec.PlatformSpec from the device's platform.json.
// Used for priming the spec system on first connect to new hardware.
func (d *Device) GeneratePlatformSpec(ctx context.Context) (*spec.PlatformSpec, error)

// ============================================================================
// Composite Delivery
// ============================================================================

// DeliverComposite delivers a composite config to the device and generates
// a ChangeSet for verification. The ChangeSet generation differs by mode:
//
// Overwrite mode:
//   1. Snapshot current CONFIG_DB (pre-state)
//   2. ReplaceAll (flush + pipeline write)
//   3. For each entry in composite: ChangeSet entry = ChangeAdd with OldValue from pre-snapshot
//   4. For each entry in pre-snapshot missing from composite: ChangeSet entry = ChangeDelete
//
// Merge mode:
//   1. Snapshot current CONFIG_DB (pre-state)
//   2. Diff: only entries that differ from pre-state are included
//   3. Entries with same table|key but different field values = conflict error
//   4. Entries with same table|key and same values = skipped (no-op)
//   5. PipelineSet only the differing entries
//
func (d *Device) DeliverComposite(ctx context.Context, composite *CompositeConfig, mode CompositeMode) (*CompositeDeliveryResult, error)

// ValidateComposite validates a composite config before delivery (dry-run).
// Returns errors for any conflicts or invalid entries.
func (d *Device) ValidateComposite(ctx context.Context, composite *CompositeConfig, mode CompositeMode) error

// Composite merge conflict rules:
//
// When mode=merge, DeliverComposite compares each composite entry against
// the current CONFIG_DB:
//   - Same table|key, same field values → skipped (already present, counted as Skipped)
//   - Same table|key, different field values → conflict error (delivery aborted)
//   - New table|key → applied normally (counted as Applied)
//
// Conflict example: composite has BGP_GLOBALS|default with router_id="10.0.0.1"
// but CONFIG_DB already has BGP_GLOBALS|default with router_id="10.0.0.2".
// This is a conflict because merge mode does not overwrite existing values.
// The caller should use overwrite mode or resolve the conflict before retrying.
//
// TOCTOU note: The pre-state snapshot used for diff comparison is read before
// the PipelineSet write. Another process could modify CONFIG_DB between the
// snapshot and the pipeline execution, causing the diff to be stale. This is
// acceptable for lab use (single operator). Production use would need CAS
// (compare-and-swap) or optimistic locking on the snapshot version.
```

### 5.3 Operation Configuration Types

```go
// VLANConfig holds configuration options for CreateVLAN
type VLANConfig struct {
    Name        string
    Description string
    L2VNI       int
}

// PortChannelConfig holds configuration options for CreatePortChannel
type PortChannelConfig struct {
    Members  []string
    MTU      int
    MinLinks int
    Fallback bool
    FastRate bool
}

// VRFConfig holds configuration options for CreateVRF
type VRFConfig struct {
    L3VNI    int
    ImportRT []string
    ExportRT []string
}

// VTEPConfig holds configuration options for CreateVTEP
type VTEPConfig struct {
    SourceIP string // VTEP source IP (typically loopback)
    UDPPort  int    // UDP port (default 4789)
}

// ACLTableConfig holds configuration options for CreateACLTable
type ACLTableConfig struct {
    Type        string   // L3, L3V6, MIRROR
    Stage       string   // ingress, egress
    Description string
    Ports       []string
}

// LoopbackBGPNeighborConfig holds configuration for loopback-based BGP neighbors
type LoopbackBGPNeighborConfig struct {
    NeighborIP  string
    RemoteAS    int
    Description string
    EVPN        bool
}

// InterfaceConfig holds configuration options for Interface.Configure
type InterfaceConfig struct {
    Description string
    MTU         int
    Speed       string
    AdminStatus string
}

// DirectBGPNeighborConfig holds configuration for a direct BGP neighbor
type DirectBGPNeighborConfig struct {
    NeighborIP  string // Auto-derived for /30, /31 if empty
    RemoteAS    int
    Description string
    Password    string
    BFD         bool
    Multihop    int   // eBGP multihop TTL (0 = directly connected)
}

// BGPNeighborConfig holds configuration for adding a BGP neighbor (extended)
type BGPNeighborConfig struct {
    NeighborIP  string
    RemoteASN   int
    Passive     bool   // Wait for incoming connection
    TTL         int    // eBGP multihop TTL
    Description string
}

// HealthCheckResult represents the result of a single health check
type HealthCheckResult struct {
    Check   string `json:"check"`
    Status  string `json:"status"`  // "pass", "warn", "fail"
    Message string `json:"message"`
}

// CleanupSummary provides details about orphaned resources found
type CleanupSummary struct {
    OrphanedACLs        []string
    OrphanedVRFs        []string
    OrphanedVNIMappings []string
}
```

**BGP and topology operation configuration types:**

```go
// BGPGlobalsConfig holds configuration for SetBGPGlobals
type BGPGlobalsConfig struct {
    VRF                string // "default" or VRF name
    LocalASN           int
    RouterID           string
    RRClusterID        string // route reflector cluster ID
    LoadBalanceMPRelax bool   // multipath relax for ECMP
    EBGPRequiresPolicy bool   // disable mandatory eBGP policy
    DefaultIPv4Unicast bool   // disable auto IPv4 unicast
    LogNeighborChanges bool
    SuppressFIBPending bool
}

// SetupRouteReflectorConfig holds configuration for SetupRouteReflector
type SetupRouteReflectorConfig struct {
    Neighbors    []string // neighbor IPs (loopback addresses)
    LocalASN     int
    RouterID     string
    ClusterID    string   // from SiteSpec; defaults to loopback if empty
    MaxIBGPPaths int      // ECMP paths for iBGP
    IPv6Enabled  bool     // enable ipv6_unicast AF
}

// PeerGroupConfig holds configuration for ConfigurePeerGroup
type PeerGroupConfig struct {
    ASN          int
    LocalASN     int
    Description  string
    EBGPMultihop int
    Password     string
    AFs          map[string]BGPPeerGroupAFEntry // per-AF settings
}

// RouteRedistributionConfig holds configuration for AddRouteRedistribution
type RouteRedistributionConfig struct {
    VRF       string // "default" or VRF name
    Protocol  string // "connected", "static"
    AF        string // "ipv4", "ipv6"
    RouteMap  string // optional route-map filter
}

// RouteMapConfig holds configuration for AddRouteMap
type RouteMapConfig struct {
    Rules []RouteMapEntry // ordered by sequence number
}

// PrefixSetConfig holds configuration for AddPrefixSet
type PrefixSetConfig struct {
    Entries []PrefixSetEntry // ordered by sequence number
}

// BreakoutConfig holds configuration for BreakoutPort
type BreakoutConfig struct {
    Mode string // e.g., "4x25G", "2x50G", "1x100G"
}
```

### 5.4 Topology Provisioning Operations (`pkg/network/topology.go`)

```go
// TopologyProvisioner generates and delivers config from topology specs.
type TopologyProvisioner struct {
    network *Network
}

func NewTopologyProvisioner(network *Network) (*TopologyProvisioner, error)
func (tp *TopologyProvisioner) ValidateTopologyDevice(deviceName string) error
func (tp *TopologyProvisioner) GenerateDeviceComposite(deviceName string) (*CompositeConfig, error)
func (tp *TopologyProvisioner) ProvisionDevice(ctx context.Context, deviceName string) (*CompositeDeliveryResult, error)
func (tp *TopologyProvisioner) ProvisionInterface(ctx context.Context, deviceName, interfaceName string) (*ChangeSet, error)

// CompositeEntry represents a single entry within a composite config for generation.
type CompositeEntry struct {
    Table  string
    Key    string
    Fields map[string]string
}

// generateServiceEntries generates CompositeEntry values for a service applied to an interface.
func generateServiceEntries(
    network *Network,
    deviceName, interfaceName, serviceName, ipAddr string,
) ([]CompositeEntry, error)

// generateQoSDeviceEntries produces device-wide CONFIG_DB entries for a QoS policy:
// DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER (per queue), WRED_PROFILE (if ECN).
func generateQoSDeviceEntries(policyName string, policy *spec.QoSPolicy) []CompositeEntry

// generateQoSInterfaceEntries produces per-interface CONFIG_DB entries:
// PORT_QOS_MAP (bracket-refs to maps) and QUEUE entries (bracket-refs to schedulers).
func generateQoSInterfaceEntries(policyName string, policy *spec.QoSPolicy, interfaceName string) []CompositeEntry

// resolveServiceQoSPolicy checks QoSPolicy first, falls back to legacy QoSProfile.
func resolveServiceQoSPolicy(n *Network, svc *spec.ServiceSpec) (string, *spec.QoSPolicy)
```

## 6. Precondition Checker

### 6.1 Implementation (`pkg/operations/precondition.go`)

```go
// PreconditionChecker validates operation preconditions
type PreconditionChecker struct {
    device    *device.Device
    operation string
    resource  string
    errors    []error
}

func NewPreconditionChecker(d *device.Device, op, resource string) *PreconditionChecker

func (pc *PreconditionChecker) Check(condition bool, requirement, message string) {
    if !condition {
        pc.errors = append(pc.errors, util.NewPreconditionError(
            pc.operation, pc.resource, requirement, message))
    }
}

func (pc *PreconditionChecker) Result() error {
    if len(pc.errors) == 0 {
        return nil
    }
    if len(pc.errors) == 1 {
        return pc.errors[0]
    }
    return util.NewValidationErrors(pc.errors)
}
```

**Precondition methods:**

```go
func (pc *PreconditionChecker) RequireConnected()
func (pc *PreconditionChecker) RequireInterfaceExists(intf string)
func (pc *PreconditionChecker) RequireInterfaceNotLAGMember(intf string)
func (pc *PreconditionChecker) RequireVTEPConfigured()
func (pc *PreconditionChecker) RequireBGPConfigured()
func (pc *PreconditionChecker) RequireVRFExists(vrf string)
func (pc *PreconditionChecker) RequireVLANExists(vlanID int)
func (pc *PreconditionChecker) RequireACLTableExists(name string)
func (pc *PreconditionChecker) RequireFilterSpecExists(name string)

// v3 precondition methods:
func (pc *PreconditionChecker) RequirePortAllowed(portName string)     // port name exists in platform.json
func (pc *PreconditionChecker) RequirePlatformLoaded()                 // PlatformConfig has been fetched
func (pc *PreconditionChecker) RequireNoExistingService(intf string)   // no service binding on interface (for merge)
func (pc *PreconditionChecker) RequirePeerGroupExists(name string)     // peer group exists in BGP_PEER_GROUP
```

The `device.Device` also provides inline precondition checks:

```go
func (d *Device) RequireConnected() error {
    if !d.IsConnected() {
        return util.NewPreconditionError("operation", d.Name, "device must be connected", "")
    }
    return nil
}

func (d *Device) RequireLocked() error {
    if !d.connected {
        return util.NewPreconditionError("operation", d.Name, "device must be connected", "")
    }
    if !d.locked {
        return util.NewPreconditionError("operation", d.Name, "device must be locked for changes", "use Lock() first")
    }
    return nil
}
```

## 7. Value Derivation

### 7.1 Auto-Derived Values (`pkg/util/derive.go`)

```go
// DerivedValues contains auto-computed values
type DerivedValues struct {
    NeighborIP    string
    NetworkAddr   string
    BroadcastAddr string
    SubnetMask    int
    VRFName       string
    ACLNameBase   string
    Description   string
}

// DeriveFromInterface computes values from interface, IP, and service name.
func DeriveFromInterface(intf, ipWithMask, serviceName string) (*DerivedValues, error)

// DeriveVRFName returns the VRF name based on VRF type.
// Uses shortened interface names for compact VRF names.
//   vrf_type "interface" → {serviceName}-{shortenedIntf}
//     Example: DeriveVRFName("interface", "customer-l3", "Ethernet0") → "customer-l3-Eth0"
//   vrf_type "shared"    → {serviceName}
//     Example: DeriveVRFName("shared", "customer-l3", "Ethernet0") → "customer-l3"
//   default              → {serviceName}-{shortenedIntf} (same as interface)
func DeriveVRFName(vrfType, serviceName, interfaceName string) string {
    switch vrfType {
    case VRFTypeInterface:
        return serviceName + "-" + SanitizeForName(ShortenInterfaceName(interfaceName))
    case VRFTypeShared:
        return serviceName
    default:
        return serviceName + "-" + SanitizeForName(ShortenInterfaceName(interfaceName))
    }
}

// DeriveACLName returns the ACL table name for a service filter.
// ACLs are per-service, not per-interface.
//   Example: DeriveACLName("customer-l3", "in") → "customer-l3-in"
func DeriveACLName(serviceName, direction string) string {
    return serviceName + "-" + direction
}

// ComputeNeighborIP returns peer IP for point-to-point subnets
func ComputeNeighborIP(localIP string, maskLen int) string {
    ip := net.ParseIP(localIP).To4()
    if ip == nil {
        return ""
    }

    switch maskLen {
    case 31: // RFC 3021 point-to-point
        if ip[3]&1 == 0 {
            ip[3]++
        } else {
            ip[3]--
        }
    case 30: // Traditional point-to-point
        lastOctet := ip[3] & 0x03
        if lastOctet == 1 {
            ip[3]++
        } else if lastOctet == 2 {
            ip[3]--
        } else {
            return ""
        }
    default:
        return ""
    }

    return ip.String()
}

// NormalizeInterfaceName converts short interface names to full SONiC format.
func NormalizeInterfaceName(name string) string

// ShortenInterfaceName converts full SONiC names to short form (for display).
func ShortenInterfaceName(name string) string
```

**Interface name mappings:**

| Short | Full (SONiC) |
|-------|-------------|
| `eth0` | `Ethernet0` |
| `po100` | `PortChannel100` |
| `vl100` | `Vlan100` |
| `lo0` | `Loopback0` |
| `mgmt0` | `Management0` |

### 7.2 Route Redistribution Defaults

When applying a service with BGP routing, redistribution of connected subnets into BGP follows opinionated defaults. These defaults can be overridden per-service using the `redistribute` flag in the Routing spec.

| Interface Type | Default Redistribute | Rationale |
|---------------|---------------------|-----------|
| Service interfaces (l3, irb) | `true` — redistribute connected | Service subnets need reachability across fabric |
| Transit interfaces (no service) | `false` — do NOT redistribute | Underlay uses direct BGP peering; redistributing creates redundant routes |
| Loopback | Always redistributed | BGP router-id and VTEP source must be reachable |

**Override logic:**
```go
func shouldRedistribute(svc *spec.ServiceSpec, intfType string) bool {
    if svc.Routing != nil && svc.Routing.Redistribute != nil {
        return *svc.Routing.Redistribute // explicit override
    }
    // Default: service interfaces yes, transit no
    return svc.ServiceType != "" // has a service type = service interface
}
```

### 7.3 Specification Resolution (`pkg/spec/resolver.go`)

```go
// ResolveProfile applies inheritance: profile > region > global
// Note: Region is derived from site.json, not stored in profile
func ResolveProfile(
    deviceName string,
    profile *DeviceProfile,
    network *NetworkSpecFile,
    siteSpec *SiteSpecFile,
    loadProfile func(string) (*DeviceProfile, error),
) *ResolvedProfile {
    // Get site - determines the region (single source of truth)
    site := siteSpec.Sites[profile.Site]
    regionName := site.Region
    region := network.Regions[regionName]

    r := &ResolvedProfile{
        DeviceName: deviceName,
        MgmtIP:     profile.MgmtIP,
        LoopbackIP: profile.LoopbackIP,
        Region:     regionName,
        Site:       profile.Site,
        Platform:   profile.Platform,
        SSHUser:    profile.SSHUser,  // v2: pass through SSH credentials
        SSHPass:    profile.SSHPass,  // v2: pass through SSH credentials
    }

    // AS Number: profile > region
    if profile.ASNumber != nil {
        r.ASNumber = *profile.ASNumber
    } else if region != nil {
        r.ASNumber = region.ASNumber
    }

    // Router ID and VTEP from loopback
    r.RouterID = profile.LoopbackIP
    r.VTEPSourceIP = profile.LoopbackIP

    // BGP neighbors: lookup route reflector profiles to get their loopback IPs
    r.BGPNeighbors = []string{}
    for _, rrName := range site.RouteReflectors {
        if rrName == deviceName { continue }
        if rrProfile, err := loadProfile(rrName); err == nil {
            r.BGPNeighbors = append(r.BGPNeighbors, rrProfile.LoopbackIP)
        }
    }

    // Merge maps: profile > region > global
    r.GenericAlias = mergeMaps(network.GenericAlias, region.GenericAlias, profile.GenericAlias)
    r.PrefixLists = mergeMaps(network.PrefixLists, region.PrefixLists, profile.PrefixLists)

    return r
}
```

## 8. Audit Logging (Phase 2)

> **Phase 2 — deferred.** Operations currently log actions, success/failure via standard `slog` logging. A structured audit trail (AuditLogger, AuditEvent, FileAuditLogger) and permission checking (PermissionChecker) are designed but deferred to Phase 2. The types below define the target interface.

### 8.1 Event Types (`pkg/audit/event.go`)

```go
type AuditEvent struct {
    ID          string              `json:"id"`
    Timestamp   time.Time           `json:"timestamp"`
    User        string              `json:"user"`
    Device      string              `json:"device"`
    Operation   string              `json:"operation"`
    Service     string              `json:"service,omitempty"`
    Interface   string              `json:"interface,omitempty"`
    Changes     []Change            `json:"changes"`
    Success     bool                `json:"success"`
    Error       string              `json:"error,omitempty"`
    ExecuteMode bool                `json:"execute_mode"`
}
```

### 8.2 Logger Interface (`pkg/audit/logger.go`)

```go
// AuditFilter specifies criteria for querying audit events.
type AuditFilter struct {
    User      string    // filter by user (empty = all)
    Device    string    // filter by device name (empty = all)
    Operation string    // filter by operation (empty = all)
    Since     time.Time // events after this time (zero = no lower bound)
    Until     time.Time // events before this time (zero = no upper bound)
    Limit     int       // max events to return (0 = no limit)
}

type AuditLogger interface {
    Log(ctx context.Context, event AuditEvent) error
    Query(filter AuditFilter) ([]AuditEvent, error)
    Close() error
}

// FileAuditLogger logs to JSON lines file
type FileAuditLogger struct {
    path string
    file *os.File
    mu   sync.Mutex
}
```

## 9. Permission System (Phase 2)

> **Phase 2 — deferred.** Permission checking is designed but not yet integrated into the operation call path. Operations currently execute without permission checks. The types below define the target interface.

### 9.1 Permission Definitions (`pkg/auth/permission.go`)

```go
type Permission string

const (
    PermServiceApply  Permission = "service.apply"
    PermServiceRemove Permission = "service.remove"
    PermLagCreate     Permission = "lag.create"
    PermLagModify     Permission = "lag.modify"
    PermLagDelete     Permission = "lag.delete"
    PermVlanCreate    Permission = "vlan.create"
    PermVlanDelete    Permission = "vlan.delete"
    PermAclModify     Permission = "acl.modify"
    PermEvpnModify    Permission = "evpn.modify"
    PermQosModify     Permission = "qos.modify"
    PermBaselineApply Permission = "baseline.apply"
    // v3 additions:
    PermPortCreate       Permission = "port.create"
    PermPortDelete       Permission = "port.delete"
    PermBGPConfigure     Permission = "bgp.configure"
    PermCompositeDeliver Permission = "composite.deliver"
    // v4 additions:
    PermTopologyProvision Permission = "topology.provision"
    PermAll               Permission = "all"
)
```

### 9.2 Permission Checker (`pkg/auth/checker.go`)

```go
// PermContext provides context for permission checks — which service,
// device, and interface are being operated on.
type PermContext struct {
    Service   string // Service name being applied/removed (empty for non-service ops)
    Device    string // Target device name
    Interface string // Target interface name (empty for device-level ops)
}

type PermissionChecker struct {
    spec *spec.NetworkSpecFile
}

func (pc *PermissionChecker) Check(user string, perm Permission, ctx PermContext) error {
    // 1. Superusers bypass all checks
    if contains(pc.spec.SuperUsers, user) {
        return nil
    }

    // 2. Get user's groups
    userGroups := pc.getUserGroups(user)

    // 3. Check service-specific permissions first
    if ctx.Service != "" {
        if svc := pc.spec.Services[ctx.Service]; svc != nil {
            if svc.Permissions != nil {
                if allowed := svc.Permissions[string(perm)]; len(allowed) > 0 {
                    if hasOverlap(userGroups, allowed) {
                        return nil
                    }
                    return ErrPermissionDenied
                }
            }
        }
    }

    // 4. Check global permissions
    if allowed := pc.spec.Permissions[string(perm)]; len(allowed) > 0 {
        if hasOverlap(userGroups, allowed) {
            return nil
        }
    }

    // 5. Check for 'all' permission
    if allowed := pc.spec.Permissions["all"]; len(allowed) > 0 {
        if hasOverlap(userGroups, allowed) {
            return nil
        }
    }

    return ErrPermissionDenied
}
```

## 10. Error Types

### 10.1 Custom Errors (`pkg/util/errors.go`)

```go
// PreconditionError indicates a precondition was not met
type PreconditionError struct {
    Operation   string
    Resource    string
    Requirement string
    Message     string
}

func (e *PreconditionError) Error() string {
    return fmt.Sprintf("%s on %s: %s required - %s",
        e.Operation, e.Resource, e.Requirement, e.Message)
}

// ValidationError indicates invalid input
type ValidationError struct {
    Field   string
    Value   interface{}
    Message string
}

// DependencyError indicates a missing dependency
type DependencyError struct {
    Operation  string
    Dependency string
    Message    string
}

// ErrDeviceLocked is returned when Lock() cannot acquire the distributed lock
// because another holder has it. Wraps the holder identity (e.g. "aldrin@workstation1").
var ErrDeviceLocked = errors.New("device is locked by another process")

// ErrNotConnected is returned when an operation requires a connected device.
var ErrNotConnected = errors.New("device not connected")

// InUseError indicates a resource is in use
type InUseError struct {
    Resource string
    UsedBy   string
    Message  string
}

// ValidationErrors holds multiple validation errors
type ValidationErrors struct {
    Errors []error
}

func (e *ValidationErrors) Error() string {
    var sb strings.Builder
    sb.WriteString("multiple validation errors:\n")
    for i, err := range e.Errors {
        sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
    }
    return sb.String()
}
```

## 11. CLI Implementation

### 11.1 OO CLI Design Pattern

The CLI follows a true object-oriented design where:
- **Context flags** (`-n`, `-d`, `-i`) select the object (like `this` in OOP)
- **Command verbs** are methods on that object

```
newtron -n <network> -d <device> -i <interface> <verb> [args] [-x]
         |--------------------|---------------------|   |------|
                Object Selection                    Method Call
```

### 11.2 Root Command (`cmd/newtron/main.go`)

```go
var rootCmd = &cobra.Command{
    Use:   "newtron",
    Short: "Network automation CLI for SONiC switches",
}

var (
    networkName   string  // -n, --network
    deviceName    string  // -d, --device
    interfaceName string  // -i, --interface
    specDir       string  // -s, --specs
    executeMode   bool    // -x, --execute
    verboseMode   bool    // -v, --verbose
    jsonOutput    bool    //     --json
)

func init() {
    // Context flags (object selectors)
    rootCmd.PersistentFlags().StringVarP(&networkName, "network", "n", "", "Network spec name")
    rootCmd.PersistentFlags().StringVarP(&deviceName, "device", "d", "", "Target device name")
    rootCmd.PersistentFlags().StringVarP(&interfaceName, "interface", "i", "", "Target interface")

    // Operation flags
    rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "s", "", "Specification directory")
    rootCmd.PersistentFlags().BoolVarP(&executeMode, "execute", "x", false, "Execute changes (default: dry-run)")
    rootCmd.PersistentFlags().BoolVarP(&saveMode, "save", "", false, "Save config after execution")
    rootCmd.PersistentFlags().BoolVarP(&verboseMode, "verbose", "v", false, "Verbose output")
    rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

    // Command registration — OO verb commands (symmetric read/write)
    rootCmd.AddCommand(showCmd, getCmd, listCmd)                          // Read verbs
    rootCmd.AddCommand(getServiceCmd, listMembersCmd, listAclsCmd)       // Read verbs (cont.)
    rootCmd.AddCommand(getMacvpnCmd, getL2VniCmd, listBgpCmd)            // Read verbs (cont.)
    rootCmd.AddCommand(setCmd, createCmd, deleteCmd)                      // Write verbs
    rootCmd.AddCommand(applyServiceCmd, removeServiceCmd, refreshServiceCmd) // Service verbs
    rootCmd.AddCommand(addMemberCmd, removeMemberCmd)                    // Membership verbs
    rootCmd.AddCommand(bindAclCmd, unbindAclCmd)                         // ACL verbs
    rootCmd.AddCommand(bindMacvpnCmd, unbindMacvpnCmd)                   // MAC-VPN verbs
    rootCmd.AddCommand(addBgpCmd, removeBgpCmd)                          // BGP verbs
    rootCmd.AddCommand(healthCheckVerbCmd, applyBaselineCmd)             // Device operations
    rootCmd.AddCommand(cleanupCmd, configureSVICmd)                      // Device operations (cont.)

    // Command group subcommands
    rootCmd.AddCommand(settingsCmd, serviceCmd, interfaceCmd)
    rootCmd.AddCommand(lagCmd, vlanCmd, aclCmd, evpnCmd, bgpCmd)
    rootCmd.AddCommand(healthCmd, baselineCmd, provisionCmd)
    rootCmd.AddCommand(auditCmd, stateCmd)                               // Operational queries
    rootCmd.AddCommand(interactiveCmd, shellCmd, versionCmd)
}
```

**Helper functions for context-based object resolution:**

```go
func requireDevice(ctx context.Context) (*network.Device, error) {
    if deviceName == "" {
        return nil, fmt.Errorf("device required: use -d <device> flag")
    }
    return net.ConnectDevice(ctx, deviceName)
}

func requireInterface(ctx context.Context) (*network.Device, *network.Interface, error) {
    if deviceName == "" {
        return nil, nil, fmt.Errorf("device required: use -d <device> flag")
    }
    if interfaceName == "" {
        return nil, nil, fmt.Errorf("interface required: use -i <interface> flag")
    }
    dev, err := net.ConnectDevice(ctx, deviceName)
    if err != nil {
        return nil, nil, err
    }
    intf, err := dev.GetInterface(interfaceName)
    if err != nil {
        return nil, nil, err
    }
    return dev, intf, nil
}
```

### 11.3 Symmetric Read/Write Operations

| Write Verb | Read Verb | Description |
|------------|-----------|-------------|
| `set <prop> <val>` | `get <prop>` | Property access |
| `apply-service` / `remove-service` / `refresh-service` | `get-service` | Service binding |
| `add-member` / `remove-member` | `list-members` | Collection membership |
| `bind-acl` / `unbind-acl` | `list-acls` | ACL binding |
| `bind-macvpn` / `unbind-macvpn` | `get-macvpn` | MAC-VPN binding |
| `map-l2vni` / `unmap-l2vni` | `get-l2vni` | VNI mapping |
| `add-bgp-neighbor` / `remove-bgp-neighbor` | `list-bgp-neighbors` | BGP neighbors |

### 11.4 BGP Commands

BGP neighbors are added at different levels:
- **Interface level** (`-i`): Direct neighbors using link IP as update-source
- **Device level** (`-d` only): Loopback-sourced neighbors (e.g., EVPN peers)

**Neighbor IP Auto-Derivation:**

| Subnet | Behavior |
|--------|----------|
| /30 | Neighbor IP auto-derived (other host address) |
| /31 | Neighbor IP auto-derived (RFC 3021) |
| /29 or larger | `--neighbor-ip` required (fail if omitted) |

**Security Constraints (Non-Negotiable):**

| Constraint | Value | Rationale |
|------------|-------|-----------|
| TTL | 1 | Hardcoded for all direct neighbors (GTSM) |
| Subnet validation | Required | Neighbor IP must be on interface subnet |
| Mutual exclusion | Enforced | `--passive` and `--neighbor-ip` cannot be used together |

### 11.5 Service Immutability Model

Once a service is applied to an interface, the configuration is divided into two categories:

**Structural Configuration (Immutable):**
- VRF binding
- IP address
- ACLs (ingress/egress filters)
- EVPN mappings (L2VNI, L3VNI)
- Route targets
- QoS profile

To change structural config, remove the service and reapply with different settings.

**Operational Configuration (Mutable):**

| Parameter | Description | Command |
|-----------|-------------|---------|
| `admin-status` | Interface up/down | `set admin-status up/down` |
| `cost-in` | BGP inbound cost (via local-pref) | `set cost-in <value>` |
| `cost-out` | BGP outbound cost (via AS-path prepend) | `set cost-out <value>` |

```go
func (i *Interface) Set(ctx context.Context, property, value string) (*ChangeSet, error) {
    if i.HasService() {
        switch property {
        case "admin-status", "cost-in", "cost-out":
            // Allowed - these are operational
        default:
            return nil, fmt.Errorf(
                "property %q is immutable when service is bound; "+
                "use remove-service first, then reapply with new settings",
                property)
        }
    }
    cs := NewChangeSet(i.Device().Name(), "interface.set")
    // ...
    return cs, nil
}
```

### 11.6 Service Refresh

When a service definition changes (e.g., filter-spec updated in `network.json`), interfaces using that service can be synchronized:

```go
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
    svc, err := i.Network().GetService(i.serviceName)
    if err != nil {
        return nil, err
    }

    cs := NewChangeSet(i.Device().Name(), "interface.refresh-service")

    // --- Step 1: Rebuild expected config from current spec ---
    // Generate the full set of CONFIG_DB entries that ApplyService would
    // produce today (using current spec + current interface context).
    expected := i.generateServiceConfig(svc)

    // --- Step 2: Read current config from CONFIG_DB ---
    // For each table/key that this service owns (tracked via
    // NEWTRON_SERVICE_BINDING metadata), read the current fields.
    current := i.readOwnedEntries()

    // --- Step 3: Diff expected vs current, field by field ---
    // Comparison is per-field within each table|key. Sequence numbers
    // (ACL_RULE) are compared by value, not by position — rule identity
    // is the sequence number itself (e.g., RULE_100).
    for _, entry := range expected {
        cur, exists := current[entry.TableKey()]
        if !exists {
            // Entry missing from device — add it
            cs.Add(entry.Table, entry.Key, ChangeAdd, nil, entry.Fields)
        } else {
            // Entry exists — compare field by field
            changed := map[string]string{}
            for field, val := range entry.Fields {
                if cur[field] != val {
                    changed[field] = val
                }
            }
            if len(changed) > 0 {
                cs.Add(entry.Table, entry.Key, ChangeModify, cur, entry.Fields)
            }
        }
    }

    // --- Step 4: Detect orphaned entries (in current but not expected) ---
    // Entries that exist on the device but are no longer in the spec
    // are deleted. This handles: removed ACL rules, removed route-maps,
    // changed VRF type (old VRF no longer needed).
    for tableKey, fields := range current {
        if _, needed := expected.Lookup(tableKey); !needed {
            table, key := splitTableKey(tableKey)
            cs.Add(table, key, ChangeDelete, fields, nil)
        }
    }

    return cs, nil
}
```

**Diff algorithm invariants:**
- **Identity**: Each CONFIG_DB entry is identified by `table|key`. Two entries are "the same" if they share the same table and key.
- **Field comparison**: Fields are compared by string equality. A field present in expected but missing in current is an addition. A field present in current but missing in expected within the same key is left untouched (service may not own all fields in a shared key).
- **ACL rules**: Rule identity is the sequence number (`RULE_100`). If the spec adds `RULE_150` and removes `RULE_200`, the diff produces one ChangeAdd and one ChangeDelete. Rules are never reordered — sequence numbers are stable identifiers.
- **Shared resources**: If the VRF name changed (e.g., `vrf_type` changed in spec), the old VRF appears in the orphan detection step. DependencyChecker is consulted before deleting shared resources — if another interface still uses the VRF, it is not deleted.
- **Ownership boundary**: Only entries tracked in `NEWTRON_SERVICE_BINDING` metadata for this interface are compared. Entries created by other services or by the platform are not touched.

### 11.7 Orphan Cleanup

The `cleanup` command removes orphaned configurations from the device. Philosophy: only active configurations should exist on the device.

```go
func (d *Device) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error) {
    cs := NewChangeSet(d.name, "device.cleanup")

    if cleanupType == "" || cleanupType == "acls" {
        orphanedACLs := d.findOrphanedACLs()
        for _, aclName := range orphanedACLs {
            for _, ruleName := range d.getACLRules(aclName) {
                cs.Add("ACL_RULE", fmt.Sprintf("%s|%s", aclName, ruleName), ChangeDelete, nil, nil)
            }
            cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
        }
    }

    if cleanupType == "" || cleanupType == "vrfs" {
        orphanedVRFs := d.findOrphanedVRFs()
        for _, vrfName := range orphanedVRFs {
            if vni := d.getVRFL3VNI(vrfName); vni > 0 {
                mapKey := fmt.Sprintf("vtep1|map_%d_%s", vni, vrfName)
                cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
            }
            cs.Add("VRF", vrfName, ChangeDelete, nil, nil)
        }
    }

    if cleanupType == "" || cleanupType == "vnis" {
        orphanedVNIs := d.findOrphanedVNIMappings()
        for _, mapKey := range orphanedVNIs {
            cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
        }
    }

    return cs, summary, nil
}
```

**Cleanup Types:**

| Type | Description |
|------|-------------|
| `acls` | ACL tables not bound to any interface |
| `vrfs` | VRFs with no interface bindings |
| `vnis` | VNI mappings for deleted VLANs/VRFs |
| (empty) | All of the above |

### 11.8 Settings Persistence

User settings are stored in `~/.newtron/settings.json`:

```go
type Settings struct {
    DefaultNetwork   string `json:"default_network,omitempty"`
    DefaultDevice    string `json:"default_device,omitempty"`
    SpecDir          string `json:"spec_dir,omitempty"`
    LastDevice       string `json:"last_device,omitempty"`
    ExecuteByDefault bool   `json:"execute_by_default,omitempty"`
}
```

### 11.9 Operational Query Commands

These commands are read-only and do not require `-x`:

**State commands** (`cmd_state.go`) — query operational state from STATE_DB:

| Command | Description |
|---------|-------------|
| `state show` | Full device state summary (interfaces, LAGs, VLANs, VRFs) |
| `state bgp` | BGP neighbor states from STATE_DB |
| `state evpn` | EVPN/VXLAN state (VTEP, remote VTEPs, VNI count) |
| `state lag` | LAG member states (selected, collecting/distributing) |
| `state vrf` | VRF operational states |

**Audit commands** (`cmd_audit.go`) — query audit log:

| Command | Description |
|---------|-------------|
| `audit list` | List audit events with filters (`--device`, `--user`, `--last`, `--limit`, `--failures`) |

**Shell command** (`shell.go`) — interactive REPL:

| Command | Description |
|---------|-------------|
| `shell` | Enter interactive shell with device connection reuse, tab completion, and command history |

### 11.10 Config Persistence (`--save`)

The `--save` flag (in addition to `-x`) persists changes to disk after execution:

```
newtron -d leaf1 -i Ethernet0 set mtu 9000 -x --save
```

This calls `Device.SaveConfig()` after a successful `ChangeSet.Apply()`, running `sudo config save -y` on the device. Without `--save`, changes are applied to the running CONFIG_DB but may be lost on reboot.

## 12. Testing Strategy

### 12.1 Three-Tier Testing Architecture

Newtron uses three tiers of tests, each with different scope and infrastructure requirements:

| Tier | Infrastructure | Speed | What It Tests |
|------|---------------|-------|---------------|
| Unit | None | Fast (~1s) | Pure logic: IP math, name normalization, spec resolution |
| E2E | newtlab + newtest | Slow (~5min) | Full stack: SSH tunnel, real SONiC, ASIC convergence |

### 12.2 Unit Tests

Unit tests validate pure computation with no external dependencies.

```go
// pkg/util/ip_test.go
func TestComputeNeighborIP(t *testing.T) {
    tests := []struct {
        localIP  string
        mask     int
        expected string
    }{
        {"10.1.1.1", 30, "10.1.1.2"},
        {"10.1.1.2", 30, "10.1.1.1"},
        {"10.1.1.0", 31, "10.1.1.1"},
        {"10.1.1.1", 31, "10.1.1.0"},
        {"10.1.1.1", 24, ""},  // Not point-to-point
    }

    for _, tt := range tests {
        result := ComputeNeighborIP(tt.localIP, tt.mask)
        if result != tt.expected {
            t.Errorf("ComputeNeighborIP(%s, %d) = %s, want %s",
                tt.localIP, tt.mask, result, tt.expected)
        }
    }
}
```

### 12.3 E2E Testing (newtest)

E2E testing uses the newtest framework (see `docs/newtest/`). Patterns and learnings
from the legacy Go-based e2e tests are captured in `docs/newtest/e2e-learnings.md`.

---

## Cross-References

### References to Device Layer LLD

| Device LLD Section | How This LLD Uses It |
|--------------------|----------------------|
| §1 SSH Tunnel | Used by `Device.Connect()` when SSHUser/SSHPass present |
| §2 StateDB | Populated on connect; read by health checks (§5.2) |
| §3 APP_DB | Read by `Device.GetRoute()` — observation primitive (§5.2) |
| §4 ASIC_DB | Read by `Device.GetRouteASIC()` — observation primitive (§5.2) |
| §5.1 Connection | Entry point for all device operations; shared tunnel for all DBs |
| §5.3 Write paths | Used by `ChangeSet.Apply()` and composite delivery (§5.2) |
| §5.8 Pipeline ops | Used by `DeliverComposite()` for atomic writes |

### References to newtlab LLD

| newtlab LLD Section | How This LLD Uses It |
|--------------------|----------------------|
| §1.1 PlatformSpec VM fields | newtron ignores VM fields; they're newtlab-only (see §3.1A ownership table) |
| §1.2 DeviceProfile.SSHPort | Read by `Device.Connect()` (device LLD §5.1) |
| §10 Profile patching | newtlab writes `ssh_port`/`mgmt_ip` that newtron reads at connect time |

### References to newtest LLD

| newtest LLD Section | How This LLD Relates |
|---------------------|----------------------|
| §4.5 Device connection | newtest calls `Device.Connect()` after newtlab deploy |
| §5 Step executors | Executors call newtron device operations (§5.2) and verification methods |
| §5.2 provisionExecutor | Calls `TopologyProvisioner.ProvisionDevice()` (§5.4) |
| §5.9 verifyRouteExecutor | Calls `Device.GetRoute()` / `GetRouteASIC()` (§5.2) |

### References to newtron HLD

| HLD Section | LLD Section |
|-------------|-------------|
| §2 Spec vs Config | §1 Architecture, §3.1 Spec types |
| §4 Component description | §5 Operation implementations |
| §4.9 Verification architecture | §3.6A Verification types |
| §12 Execution modes | §11 CLI implementation |
| §13 Verification strategy | §3.6A types, §5.2 verification methods |

