# Newtron Low-Level Design (LLD) v4

### What Changed

#### v2

| Area | Change |
|------|--------|
| **SSH Tunnel** | Added `pkg/device/tunnel.go` — SSHTunnel struct, NewSSHTunnel(host, user, pass, port), LocalAddr, Close; SSH port-forwarding to Redis when SSHUser+SSHPass present |
| **StateDB Client** | Added `pkg/device/statedb.go` — StateDB struct with 13 state tables, StateDBClient methods (GetPortState, GetBGPNeighborState, etc.) |
| **Device Struct** | Added `tunnel *SSHTunnel`, `stateClient *StateDBClient`, `StateDB *StateDB`, `mu sync.RWMutex` fields |
| **ResolvedProfile** | Added `SSHUser` and `SSHPass` fields for SSH tunnel credentials |
| **ConfigDB Tables** | Added NEWTRON_SERVICE_BINDING (service tracking), SAG_GLOBAL, BGP_GLOBALS, BGP_GLOBALS_AF, BGP_EVPN_VNI, QoS tables (Scheduler, Queue, WREDProfile, etc.) |
| **ConfigDBClient** | Added NULL sentinel convention documentation; expanded method signatures |
| **Redis Integration** | Rewrote connection logic: SSH tunnel when creds present, direct otherwise; dual-client architecture (CONFIG_DB + STATE_DB through one tunnel) |
| **Config Persistence** | Added new section: Redis changes are runtime-only; `config save -y` required for persistence |
| **Device Operations** | Added RunHealthChecks, Cleanup with CleanupSummary, MapL2VNI/MapL3VNI/UnmapVNI, CreateVTEP/DeleteVTEP, ApplyBaseline |
| **Interface Operations** | Added RefreshService, BindMACVPN/UnbindMACVPN, AddBGPNeighborWithConfig, expandPrefixList |
| **DeviceState** | Added BGPState, BGPNeighborState, EVPNState structs |
| **Package Structure** | Added `pkg/device/tunnel.go`, `pkg/device/statedb.go`, `cmd/labgen/`, `pkg/labgen/`, `pkg/configlet/` |
| **Testing Strategy** | Expanded with three-tier assertions, build tags, LabSonicNodes vs LabNodes, SSH tunnel pool, ResetLabBaseline |

**Lines:** 2730 (v1) → ~2530 (v2) | All v1 sections preserved; new sections added for SSH tunnel, StateDB, and config persistence.

#### v3

| Area | Change |
|------|--------|
| **New CONFIG_DB Tables** | Added 9 tables: ROUTE_REDISTRIBUTE, ROUTE_MAP, BGP_PEER_GROUP, BGP_PEER_GROUP_AF, BGP_GLOBALS_AF_NETWORK, BGP_GLOBALS_AF_AGGREGATE_ADDR, PREFIX_SET, COMMUNITY_SET, AS_PATH_SET |
| **Extended BGP Structs** | BGPGlobalsEntry: load_balance_mp_relax, rr_cluster_id, ebgp_requires_policy, default_ipv4_unicast, log_neighbor_changes, suppress_fib_pending; BGPGlobalsAFEntry: max_ebgp_paths, max_ibgp_paths; BGPNeighborEntry: peer_group, password; BGPNeighborAFEntry: allowas_in, route_map_in/out, prefix_list_in/out, default_originate, addpath_tx_all_paths |
| **Spec Types** | RoutingSpec struct: import/export community, prefix-list, redistribute flag; SiteSpec struct: ClusterID |
| **Platform Config** | New §3.7: SonicPlatformConfig, PortDefinition, platform.json parsing via SSH, GeneratePlatformSpec() |
| **Composite Types** | New §3.8: CompositeConfig, CompositeBuilder, CompositeMode, CompositeDeliveryResult |
| **ConfigDBClient** | Added PipelineSet, PipelineDelete, ReplaceAll methods for atomic batch operations |
| **Pipeline Operations** | New §6.6: Redis MULTI/EXEC pipeline semantics for composite delivery |
| **Device Operations** | Added SetBGPGlobals, SetupRouteReflector, ConfigurePeerGroup, DeletePeerGroup, AddRouteRedistribution, RemoveRouteRedistribution, AddRouteMap, DeleteRouteMap, AddPrefixSet, DeletePrefixSet, AddBGPNetwork, RemoveBGPNetwork, CreatePort, DeletePort, BreakoutPort, LoadPlatformConfig, GeneratePlatformSpec, DeliverComposite, ValidateComposite |
| **Interface Operations** | Added SetRouteMap; updated AddBGPNeighbor to support community/prefix-list from service routing spec |
| **Config Types** | Added BGPGlobalsConfig, SetupRouteReflectorConfig, PeerGroupConfig, RouteRedistributionConfig, RouteMapConfig, PrefixSetConfig, CreatePortConfig, BreakoutConfig |
| **Precondition Checker** | Added RequirePortAllowed, RequirePlatformLoaded, RequireNoExistingService, RequirePeerGroupExists |
| **Value Derivation** | Added redistribution defaults: service=yes, transit=no, loopback=always |
| **Permission System** | Added port.create, port.delete, bgp.configure, composite.deliver permissions |
| **Package Structure** | Added `pkg/device/platform.go`, `pkg/device/pipeline.go`, `pkg/network/composite.go` |

**Lines:** ~2530 (v2) → ~3150 (v3) | All v2 sections preserved and expanded.

#### v4

| Area | Change |
|------|--------|
| **Spool → Composite Rename** | Renamed all spool types, functions, and files: SpoolBuilder → CompositeBuilder, SpoolConfig → CompositeConfig, SpoolMode → CompositeMode, SpoolDeliveryResult → CompositeDeliveryResult, DeliverSpool → DeliverComposite, ValidateSpool → ValidateComposite, spool.go → composite.go |
| **Type Naming Cleanup** | Container types get SpecFile suffix: NetworkSpec → NetworkSpecFile, SiteSpec → SiteSpecFile, PlatformSpec → PlatformSpecFile. Individual types get Spec suffix: Service → ServiceSpec, Routing → RoutingSpec, Region → RegionSpec, Site → SiteSpec, IPVPNDef → IPVPNSpec, MACVPNDef → MACVPNSpec, PolicerDef → PolicerSpec, PlatformDef → PlatformSpec |
| **Topology Types** | New TopologySpecFile, TopologyDevice, TopologyDeviceConfig, TopologyInterface, TopologyLink types for topology.json specification |
| **Topology Loader** | New loadTopologySpec(), GetTopology(), validateTopology() in spec loader; returns nil if topology.json does not exist |
| **Network Accessors** | Added GetTopology, HasTopology, GetTopologyDevice, GetTopologyInterface methods on Network |
| **Topology Provisioner** | New TopologyProvisioner with ProvisionDevice, ProvisionInterface, GenerateDeviceComposite; new CompositeEntry type and generateServiceEntries function |
| **Permissions** | Renamed spool.deliver → composite.deliver; added topology.provision permission |
| **Package Structure** | Renamed `pkg/network/spool.go` → `pkg/network/composite.go`; added `pkg/network/topology.go` |

**Lines:** ~3150 (v3) → ~3400 (v4) | All v3 sections preserved; spool→composite rename, type naming cleanup, topology provisioning added.

---

## 1. Spec vs Config: Fundamental Architecture

Newtron maintains a strict separation between **specification** (declarative intent) and **configuration** (imperative device state):

| Aspect | Specification | Configuration |
|--------|---------------|---------------|
| **Nature** | Declarative - what you want | Imperative - what device uses |
| **Package** | `pkg/spec` | `pkg/device` (config_db) |
| **Files** | `specs/*.json` | Redis/config_db |
| **Content** | Policies, references | Concrete values |
| **Edited by** | Network architects | Auto-generated |

The `pkg/network` layer performs **translation**: interpreting specs with context to generate config.

## 2. Package Structure

```
newtron/
├── cmd/
│   ├── newtron/                     # CLI application
│   │   ├── main.go                  # Entry point, root command, context flags
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
│   │   ├── cmd_state.go             # State DB access
│   │   └── interactive.go           # Interactive menu mode
│   └── labgen/                      # Lab topology generator CLI (v2)
│       └── main.go                  # Generates clab YAML + specs from templates
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
│   │   ├── statedb.go               # STATE_DB (DB 6) mapping + client (v2)
│   │   ├── state.go                 # State loading from config_db
│   │   └── tunnel.go                # SSH tunnel for Redis access (v2)
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
│   ├── operations/                  # Legacy/shared operation utilities
│   │   └── precondition.go          # Precondition checking utilities
│   ├── health/                      # Health checks
│   │   └── checker.go
│   ├── audit/                       # Audit logging
│   │   ├── event.go                 # Event types
│   │   └── logger.go                # Logger implementation
│   ├── auth/                        # Authorization
│   │   ├── permission.go            # Permission definitions
│   │   └── checker.go               # Permission checking
│   └── util/                        # Utilities
│       ├── errors.go                # Custom error types
│       ├── ip.go                    # IP address utilities
│       ├── derive.go                # Value derivation
│       ├── range.go                 # Range parsing
│       └── log.go                   # Logging utilities
├── internal/
│   └── testutil/                    # E2E test infrastructure (v2)
│       └── lab.go                   # SSH tunnel pool, LabSonicNodes, ResetLabBaseline
├── specs/                           # Specification files (declarative intent)
│   ├── network.json                 # Services, filters, VPNs, regions
│   ├── site.json                    # Site topology
│   ├── platforms.json               # Hardware platform definitions
│   └── profiles/                    # Per-device profiles
├── configlets/                      # Baseline templates
├── testlab/                         # Lab topology definitions
│   ├── images/                      # SONiC-VS images
│   │   └── common/                  # Shared image config
│   └── topologies/                  # Topology templates for labgen
└── docs/                            # Documentation
```

**v2 additions** to package structure:

| File | Purpose |
|------|---------|
| `pkg/device/tunnel.go` | SSH tunnel for Redis access through QEMU VMs |
| `pkg/device/statedb.go` | STATE_DB (Redis DB 6) operational state access |
| `cmd/labgen/main.go` | Lab topology generator CLI |
| `internal/testutil/lab.go` | E2E test infrastructure: SSH tunnel pool, node discovery |

**v3 additions** to package structure:

| File | Purpose |
|------|---------|
| `pkg/device/platform.go` | SonicPlatformConfig struct, platform.json parsing via SSH, port validation, GeneratePlatformSpec |
| `pkg/device/pipeline.go` | Redis MULTI/EXEC pipeline client for atomic batch writes (PipelineSet, PipelineDelete, ReplaceAll) |
| `pkg/network/composite.go` | CompositeBuilder, CompositeConfig, CompositeMode types; offline composite CONFIG_DB generation and delivery |

**v4 additions** to package structure:

| File | Purpose |
|------|---------|
| `pkg/network/composite.go` | Renamed from `spool.go`; CompositeBuilder, CompositeConfig, CompositeMode types |
| `pkg/network/topology.go` | TopologyProvisioner, ProvisionDevice, ProvisionInterface, generateServiceEntries |

## 3. Core Data Structures

### 3.1 Specification Types (`pkg/spec/types.go`)

These types define **declarative intent** - what you want, not how to achieve it.

```go
// NetworkSpecFile - Global network specification file (declarative)
type NetworkSpecFile struct {
    Version      string                       `json:"version"`
    LockDir      string                       `json:"lock_dir"`
    SuperUsers   []string                     `json:"super_users"`
    UserGroups   map[string][]string          `json:"user_groups"`
    Permissions  map[string][]string          `json:"permissions"`
    GenericAlias map[string]string            `json:"generic_alias"`
    Regions      map[string]*RegionSpec       `json:"regions"`
    PrefixLists  map[string][]string          `json:"prefix_lists"`
    FilterSpecs  map[string]*FilterSpec       `json:"filter_specs"`
    Policers     map[string]*PolicerSpec      `json:"policers"`
    QoSProfiles  map[string]*model.QoSProfile `json:"qos_profiles,omitempty"`
    CoSClasses   map[string]*model.CoSClass   `json:"cos_classes,omitempty"`

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
// For vrf_type "shared": VRF name = ipvpn definition name
// For vrf_type "interface": VRF name = {service}-{interface}
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
    Affinity     string              `json:"affinity,omitempty"`
    Sites        map[string]*SiteRef `json:"sites,omitempty"`
    PrefixLists  map[string][]string `json:"prefix_lists,omitempty"`
    GenericAlias map[string]string   `json:"generic_alias,omitempty"`
}

// SiteRef references a site within a region
type SiteRef struct {
    SiteIP          string   `json:"site_ip,omitempty"`
    RouteReflectors []string `json:"route_reflectors,omitempty"`
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
//   - "l3":  L3 routed interface (requires ipvpn, optional vrf_type)
//   - "l2":  L2 bridged interface (requires macvpn)
//   - "irb": Integrated routing and bridging (requires both ipvpn and macvpn)
//
// VRF Instantiation (vrf_type):
//   - "interface": Creates per-interface VRF named {service}-{interface}
//   - "shared":    Uses shared VRF named after the ipvpn definition
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
    QoSProfile string `json:"qos_profile,omitempty"`
    TrustDSCP  bool   `json:"trust_dscp,omitempty"`

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
    Affinity         string `json:"affinity,omitempty"`
    IsRouter         *bool  `json:"is_router,omitempty"`
    IsBridge         *bool  `json:"is_bridge,omitempty"`
    IsBorderRouter   bool   `json:"is_border_router,omitempty"`
    IsRouteReflector bool   `json:"is_route_reflector,omitempty"`

    // OPTIONAL - device-specific
    Platform        string              `json:"platform,omitempty"`
    VLANPortMapping map[int][]string    `json:"vlan_port_mapping,omitempty"`
    GenericAlias    map[string]string   `json:"generic_alias,omitempty"`
    PrefixLists     map[string][]string `json:"prefix_lists,omitempty"`

    // OPTIONAL - SSH access for Redis tunnel (v2)
    SSHUser string `json:"ssh_user,omitempty"`
    SSHPass string `json:"ssh_pass,omitempty"`
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
    Affinity         string
    IsRouter         bool
    IsBridge         bool
    IsBorderRouter   bool
    IsRouteReflector bool

    // Derived at runtime
    RouterID       string   // = LoopbackIP
    VTEPSourceIP   string   // = LoopbackIP
    VTEPSourceIntf string   // = "Loopback0"
    BGPNeighbors   []string // From site route_reflectors -> lookup loopback IPs

    // Merged maps (profile > region > global)
    GenericAlias map[string]string
    PrefixLists  map[string][]string

    // SSH access for Redis tunnel (v2)
    SSHUser string
    SSHPass string
}

// ServiceType constants
const (
    ServiceTypeL2  = "l2"
    ServiceTypeL3  = "l3"
    ServiceTypeIRB = "irb"
)

// VRFType constants
const (
    VRFTypeInterface = "interface" // Per-interface VRF: {service}-{interface}
    VRFTypeShared    = "shared"    // Shared VRF: name = ipvpn definition name
)

// TopologySpecFile represents the topology specification file (topology.json).
type TopologySpecFile struct {
    Version     string                     `json:"version"`
    Description string                     `json:"description,omitempty"`
    Devices     map[string]*TopologyDevice `json:"devices"`
    Links       []*TopologyLink            `json:"links,omitempty"`
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
```

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
```

**Accessing Network Specs from Interface**

```go
// Interface can access Network-level specs through parent chain
func (i *Interface) ApplyService(ctx context.Context, serviceName, ipAddr string) (*ChangeSet, error) {
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

**Network Accessors (v4):**

```go
// GetTopology returns the topology spec, or nil if no topology.json exists.
func (n *Network) GetTopology() *spec.TopologySpecFile

// HasTopology returns true if a topology spec has been loaded.
func (n *Network) HasTopology() bool

// GetTopologyDevice returns a topology device by name.
func (n *Network) GetTopologyDevice(name string) (*spec.TopologyDevice, error)

// GetTopologyInterface returns a topology interface for a given device and interface name.
func (n *Network) GetTopologyInterface(device, intf string) (*spec.TopologyInterface, error)
```

**Topology Loader (v4, `pkg/spec/loader.go`):**

```go
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
    StateDB  *StateDB                   // Snapshot of STATE_DB (v2)
    State    *DeviceState               // Parsed operational state

    // Redis connections
    client      *ConfigDBClient         // CONFIG_DB (DB 4) client
    stateClient *StateDBClient          // STATE_DB (DB 6) client (v2)
    tunnel      *SSHTunnel              // SSH tunnel (nil if direct) (v2)
    connected   bool
    locked      bool
    lockFiles   []string

    // Mutex for thread safety
    mu sync.RWMutex
}

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

// BGPState represents BGP operational state (v2)
type BGPState struct {
    LocalAS   int
    RouterID  string
    Neighbors map[string]*BGPNeighborState
}

// BGPNeighborState represents BGP neighbor state (v2)
type BGPNeighborState struct {
    Address  string
    RemoteAS int
    State    string
    PfxRcvd  int
    PfxSent  int
    Uptime   string
}

// EVPNState represents EVPN operational state (v2)
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
    BGPGlobalsAFNetwork   map[string]BGPGlobalsAFNetworkEntry  `json:"BGP_GLOBALS_AF_NETWORK,omitempty"`
    BGPGlobalsAFAggAddr   map[string]BGPGlobalsAFAggAddrEntry  `json:"BGP_GLOBALS_AF_AGGREGATE_ADDR,omitempty"`
    RouteRedistribute     map[string]RouteRedistributeEntry    `json:"ROUTE_REDISTRIBUTE,omitempty"`
    RouteMap              map[string]RouteMapEntry             `json:"ROUTE_MAP,omitempty"`
    PrefixSet             map[string]PrefixSetEntry            `json:"PREFIX_SET,omitempty"`
    CommunitySet          map[string]CommunitySetEntry         `json:"COMMUNITY_SET,omitempty"`
    ASPathSet             map[string]ASPathSetEntry             `json:"AS_PATH_SET,omitempty"`

    // v2: Newtron custom table (NOT standard SONiC)
    NewtronServiceBinding map[string]ServiceBindingEntry `json:"NEWTRON_SERVICE_BINDING,omitempty"`
}
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
// Key format: "neighbor_ip" (e.g., "10.0.0.2")
// v3: added peer_group, ebgp_multihop, password fields
type BGPNeighborEntry struct {
    ASN          string `json:"asn,omitempty"`
    LocalASN     string `json:"local_asn,omitempty"`
    LocalAddr    string `json:"local_addr,omitempty"`
    Description  string `json:"description,omitempty"`
    AdminStatus  string `json:"admin_status,omitempty"`
    // v3 additions:
    PeerGroup    string `json:"peer_group,omitempty"`      // assign to peer group template
    EBGPMultihop string `json:"ebgp_multihop,omitempty"`   // TTL for multihop eBGP
    Password     string `json:"password,omitempty"`         // MD5 authentication
}

// BGPNeighborAFEntry represents per-neighbor address-family settings
// Key format: "neighbor_ip|address_family" (e.g., "10.0.0.2|l2vpn_evpn")
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

**v3 ConfigDB Entry Types (frrcfgd tables):**

```go
// RouteRedistributeEntry represents route redistribution config
// Key format: "vrf|src_protocol|address_family" (e.g., "default|connected|ipv4")
type RouteRedistributeEntry struct {
    RouteMap string `json:"route_map,omitempty"` // optional route-map filter
}

// RouteMapEntry represents a route-map rule
// Key format: "map_name|seq" (e.g., "ALLOW_LOOPBACK|10")
type RouteMapEntry struct {
    Action        string `json:"action"`                     // permit, deny
    MatchPrefix   string `json:"match_prefix,omitempty"`     // prefix-set reference
    MatchCommunity string `json:"match_community,omitempty"` // community-set reference
    MatchASPath   string `json:"match_as_path,omitempty"`    // as-path-set reference
    SetLocalPref  string `json:"set_local_pref,omitempty"`   // set local-preference
    SetCommunity  string `json:"set_community,omitempty"`    // set community
    SetMED        string `json:"set_med,omitempty"`          // set MED
}

// BGPPeerGroupEntry represents a BGP peer group template
// Key format: "peer_group_name" (e.g., "SPINE_PEERS")
type BGPPeerGroupEntry struct {
    ASN          string `json:"asn,omitempty"`
    LocalASN     string `json:"local_asn,omitempty"`
    Description  string `json:"description,omitempty"`
    AdminStatus  string `json:"admin_status,omitempty"`
    EBGPMultihop string `json:"ebgp_multihop,omitempty"`
    Password     string `json:"password,omitempty"`
}

// BGPPeerGroupAFEntry represents per-AF settings for a peer group
// Key format: "peer_group_name|address_family" (e.g., "SPINE_PEERS|ipv4_unicast")
type BGPPeerGroupAFEntry struct {
    Activate             string `json:"activate,omitempty"`
    RouteReflectorClient string `json:"route_reflector_client,omitempty"`
    NextHopSelf          string `json:"next_hop_self,omitempty"`
    RouteMapIn           string `json:"route_map_in,omitempty"`
    RouteMapOut          string `json:"route_map_out,omitempty"`
    PrefixListIn         string `json:"prefix_list_in,omitempty"`
    PrefixListOut        string `json:"prefix_list_out,omitempty"`
    AllowasIn            string `json:"allowas_in,omitempty"`
    DefaultOriginate     string `json:"default_originate,omitempty"`
    AddpathTxAllPaths    string `json:"addpath_tx_all_paths,omitempty"`
}

// BGPGlobalsAFNetworkEntry represents a BGP network statement
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/24")
type BGPGlobalsAFNetworkEntry struct {
    RouteMap string `json:"route_map,omitempty"` // optional route-map
}

// BGPGlobalsAFAggAddrEntry represents a BGP aggregate-address
// Key format: "vrf|address_family|prefix" (e.g., "default|ipv4_unicast|10.0.0.0/8")
type BGPGlobalsAFAggAddrEntry struct {
    SummaryOnly string `json:"summary_only,omitempty"`
    ASSet       string `json:"as_set,omitempty"`
}

// PrefixSetEntry represents an IP prefix list entry
// Key format: "set_name|seq" (e.g., "LOOPBACKS|10")
type PrefixSetEntry struct {
    Action      string `json:"action"`                   // permit, deny
    Prefix      string `json:"prefix"`                   // IP prefix (e.g., "10.0.0.0/8")
    MaskLenLow  string `json:"mask_len_low,omitempty"`   // min mask length (ge)
    MaskLenHigh string `json:"mask_len_high,omitempty"`  // max mask length (le)
}

// CommunitySetEntry represents a BGP community list
// Key format: "set_name" (e.g., "CUSTOMER_COMMUNITIES")
type CommunitySetEntry struct {
    Action    string `json:"action"`              // permit, deny
    Community string `json:"community"`           // community value(s)
    MatchMode string `json:"match_mode,omitempty"` // any, all, exact
}

// ASPathSetEntry represents an AS-path regex filter
// Key format: "set_name" (e.g., "SHORT_PATHS")
type ASPathSetEntry struct {
    Action  string `json:"action"`   // permit, deny
    ASPath  string `json:"as_path"`  // regex pattern
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
| BGP_NEIGHBOR | `10.0.0.2` | BGP neighbor config |
| BGP_NEIGHBOR_AF | `10.0.0.2\|l2vpn_evpn` | Per-neighbor address family |
| BGP_GLOBALS | `default` or VRF name | Global BGP settings per VRF |
| BGP_GLOBALS_AF | `default\|l2vpn_evpn` | BGP address family settings |
| BGP_EVPN_VNI | `Vrf_CUST1\|10001` | Per-VNI EVPN settings |
| ACL_TABLE | `CUSTOMER-IN` | ACL table config |
| ACL_RULE | `CUSTOMER-IN\|RULE_1` | ACL rule config |
| SCHEDULER | `scheduler.0` | QoS scheduler |
| QUEUE | `Ethernet0\|0` | Queue binding |
| WRED_PROFILE | `WRED_GREEN` | WRED drop profile |
| PORT_QOS_MAP | `Ethernet0` | Port QoS map binding |
| DSCP_TO_TC_MAP | `DSCP_TO_TC` | DSCP to traffic class map |
| TC_TO_QUEUE_MAP | `TC_TO_QUEUE` | Traffic class to queue map |
| POLICER | `POLICER_1M` | Rate limiter |
| ROUTE_REDISTRIBUTE | `default\|connected\|bgp\|ipv4` | Route redistribution config (v3) |
| ROUTE_MAP | `ALLOW_LOOPBACK\|10` | Route-map rules (v3) |
| BGP_PEER_GROUP | `SPINE_PEERS` | BGP peer group templates (v3) |
| BGP_PEER_GROUP_AF | `SPINE_PEERS\|ipv4_unicast` | Per-AF peer group settings (v3) |
| BGP_GLOBALS_AF_NETWORK | `default\|ipv4_unicast\|10.0.0.0/24` | BGP network statement (v3) |
| BGP_GLOBALS_AF_AGGREGATE_ADDR | `default\|ipv4_unicast\|10.0.0.0/8` | BGP aggregate-address (v3) |
| PREFIX_SET | `LOOPBACKS\|10` | IP prefix list entries (v3) |
| COMMUNITY_SET | `CUSTOMER_COMMUNITIES` | BGP community lists (v3) |
| AS_PATH_SET | `SHORT_PATHS` | AS-path regex filters (v3) |
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
func (c *ConfigDBClient) GetAll() (*ConfigDB, error)
func (c *ConfigDBClient) Get(table, key string) (map[string]string, error)
func (c *ConfigDBClient) Exists(table, key string) (bool, error)

// Set writes a table entry. If fields is empty, a "NULL":"NULL" sentinel is
// written so the Redis key is actually created (SONiC convention for
// field-less entries like PORTCHANNEL_MEMBER or INTERFACE IP keys).
func (c *ConfigDBClient) Set(table, key string, fields map[string]string) error

func (c *ConfigDBClient) Delete(table, key string) error
func (c *ConfigDBClient) DeleteField(table, key, field string) error
```

The `Set` method handles the SONiC convention for entries that have no fields (such as IP address bindings or member entries). These require a `"NULL":"NULL"` sentinel hash field to create the Redis key, because SONiC's subscriber infrastructure relies on key existence.

**v3 pipeline methods** (in `pkg/device/pipeline.go`):

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

// PipelineSet writes multiple entries atomically via Redis MULTI/EXEC pipeline.
// Used by composite delivery for atomic multi-entry writes.
func (c *ConfigDBClient) PipelineSet(changes []TableChange) error

// PipelineDelete deletes multiple entries atomically via Redis MULTI/EXEC pipeline.
func (c *ConfigDBClient) PipelineDelete(keys []TableKey) error

// ReplaceAll flushes CONFIG_DB and writes the entire config atomically.
// Used by composite overwrite mode.
// Executes: FLUSHDB + pipeline of all HSET commands in a single MULTI/EXEC.
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
    Device    string    `json:"device"`
    Operation string    `json:"operation"`
    Timestamp time.Time `json:"timestamp"`
    Changes   []Change  `json:"changes"`
}

func NewChangeSet(device, operation string) *ChangeSet
func (cs *ChangeSet) Add(table, key string, changeType ChangeType, oldValue, newValue map[string]string)
func (cs *ChangeSet) IsEmpty() bool
func (cs *ChangeSet) String() string
func (cs *ChangeSet) Apply(d *Device) error
func (cs *ChangeSet) Rollback(d *Device) error
```

### 3.7 Platform Config (`pkg/device/platform.go`) (v3)

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

### 3.8 Composite Types (`pkg/network/composite.go`) (v3)

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
    Applied int    `json:"applied"`   // entries successfully written
    Skipped int    `json:"skipped"`   // entries skipped (merge conflict)
    Failed  int    `json:"failed"`    // entries that failed to write
    Error   error  `json:"error,omitempty"`
}
```

## 4. SSH Tunnel (`pkg/device/tunnel.go`)

SONiC devices in the lab run inside QEMU VMs managed by containerlab. Redis listens on `127.0.0.1:6379` inside the VM, but QEMU SLiRP networking does not forward port 6379. The SSH tunnel solves this by forwarding a random local port through SSH to the in-VM Redis.

### 4.1 When Tunnels Are Used

| Scenario | SSH Tunnel | Direct Redis |
|----------|-----------|--------------|
| Lab E2E tests (SONiC-VS in QEMU) | Yes - port 6379 not forwarded | No |
| Integration tests (standalone Redis) | No | Yes - Redis exposed directly |
| Production (if ever) | Would use proper auth | N/A |

The decision is made in `Device.Connect()` based on the presence of `SSHUser` and `SSHPass` in the resolved profile. When these fields are empty, a direct `<mgmt_ip>:6379` connection is used. This allows integration tests to run against a standalone Redis instance without SSH.

### 4.2 SSHTunnel Implementation

```go
// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
    localAddr string         // "127.0.0.1:<port>"
    sshClient *ssh.Client
    listener  net.Listener
    done      chan struct{}
    wg        sync.WaitGroup
}

// NewSSHTunnel dials SSH on host:port and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port is 0, defaults to 22.
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error)

// LocalAddr returns the local address (e.g. "127.0.0.1:54321") that forwards
// to Redis inside the SSH host.
func (t *SSHTunnel) LocalAddr() string

// Close stops the listener, closes the SSH connection, and waits for
// all forwarding goroutines to finish.
func (t *SSHTunnel) Close() error
```

**How it works:**

1. `ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)` establishes the SSH connection with password auth
2. `net.Listen("tcp", "127.0.0.1:0")` opens a local listener on a random available port
3. A background goroutine (`acceptLoop`) accepts incoming local connections
4. Each accepted connection is forwarded via `sshClient.Dial("tcp", "127.0.0.1:6379")`
5. Bidirectional `io.Copy` relays data between the local and remote connections
6. `Close()` signals the done channel, closes the listener, waits for goroutines, then closes SSH

**Security note:** `HostKeyCallback: ssh.InsecureIgnoreHostKey()` is used because this is a lab/test environment only. SONiC-VS VMs regenerate host keys on each boot.

```go
func NewSSHTunnel(host, user, pass string, port int) (*SSHTunnel, error) {
    if port == 0 {
        port = 22
    }
    config := &ssh.ClientConfig{
        User: user,
        Auth: []ssh.AuthMethod{
            ssh.Password(pass),
        },
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
    }

    sshClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)
    if err != nil {
        return nil, fmt.Errorf("SSH dial %s: %w", host, err)
    }

    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        sshClient.Close()
        return nil, fmt.Errorf("local listen: %w", err)
    }

    t := &SSHTunnel{
        localAddr: listener.Addr().String(),
        sshClient: sshClient,
        listener:  listener,
        done:      make(chan struct{}),
    }

    t.wg.Add(1)
    go t.acceptLoop()

    return t, nil
}

func (t *SSHTunnel) forward(local net.Conn) {
    defer t.wg.Done()
    defer local.Close()

    remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
    if err != nil {
        return
    }
    defer remote.Close()

    done := make(chan struct{}, 2)
    go func() { io.Copy(remote, local); done <- struct{}{} }()
    go func() { io.Copy(local, remote); done <- struct{}{} }()
    <-done
}
```

## 5. StateDB (`pkg/device/statedb.go`)

STATE_DB (Redis DB 6) contains the operational/runtime state of the device, separate from configuration. Where CONFIG_DB represents what you asked for, STATE_DB represents what the system is actually doing.

### 5.1 StateDB Struct

```go
// StateDB mirrors SONiC's state_db structure (Redis DB 6)
type StateDB struct {
    PortTable         map[string]PortStateEntry         `json:"PORT_TABLE,omitempty"`
    LAGTable          map[string]LAGStateEntry          `json:"LAG_TABLE,omitempty"`
    LAGMemberTable    map[string]LAGMemberStateEntry    `json:"LAG_MEMBER_TABLE,omitempty"`
    VLANTable         map[string]VLANStateEntry         `json:"VLAN_TABLE,omitempty"`
    VRFTable          map[string]VRFStateEntry          `json:"VRF_TABLE,omitempty"`
    VXLANTunnelTable  map[string]VXLANTunnelStateEntry  `json:"VXLAN_TUNNEL_TABLE,omitempty"`
    BGPNeighborTable  map[string]BGPNeighborStateEntry  `json:"BGP_NEIGHBOR_TABLE,omitempty"`
    InterfaceTable    map[string]InterfaceStateEntry    `json:"INTERFACE_TABLE,omitempty"`
    NeighTable        map[string]NeighStateEntry        `json:"NEIGH_TABLE,omitempty"`
    FDBTable          map[string]FDBStateEntry          `json:"FDB_TABLE,omitempty"`
    RouteTable        map[string]RouteStateEntry        `json:"ROUTE_TABLE,omitempty"`
    TransceiverInfo   map[string]TransceiverInfoEntry   `json:"TRANSCEIVER_INFO,omitempty"`
    TransceiverStatus map[string]TransceiverStatusEntry `json:"TRANSCEIVER_STATUS,omitempty"`
}
```

### 5.2 State Entry Types

```go
type PortStateEntry struct {
    AdminStatus  string `json:"admin_status,omitempty"`
    OperStatus   string `json:"oper_status,omitempty"`
    Speed        string `json:"speed,omitempty"`
    MTU          string `json:"mtu,omitempty"`
    LinkTraining string `json:"link_training,omitempty"`
}

type LAGStateEntry struct {
    OperStatus string `json:"oper_status,omitempty"`
    Speed      string `json:"speed,omitempty"`
    MTU        string `json:"mtu,omitempty"`
}

type LAGMemberStateEntry struct {
    OperStatus     string `json:"oper_status,omitempty"`
    CollectingDist string `json:"collecting_distributing,omitempty"`
    Selected       string `json:"selected,omitempty"`
    ActorPortNum   string `json:"actor_port_num,omitempty"`
    PartnerPortNum string `json:"partner_port_num,omitempty"`
}

type BGPNeighborStateEntry struct {
    State           string `json:"state,omitempty"`
    RemoteAS        string `json:"remote_asn,omitempty"`
    LocalAS         string `json:"local_asn,omitempty"`
    PeerGroup       string `json:"peer_group,omitempty"`
    PfxRcvd         string `json:"prefixes_received,omitempty"`
    PfxSent         string `json:"prefixes_sent,omitempty"`
    MsgRcvd         string `json:"msg_rcvd,omitempty"`
    MsgSent         string `json:"msg_sent,omitempty"`
    Uptime          string `json:"uptime,omitempty"`
    HoldTime        string `json:"holdtime,omitempty"`
    KeepaliveTime   string `json:"keepalive,omitempty"`
    ConnectRetry    string `json:"connect_retry,omitempty"`
    LastResetReason string `json:"last_reset_reason,omitempty"`
}

type VXLANTunnelStateEntry struct {
    SrcIP      string `json:"src_ip,omitempty"`
    OperStatus string `json:"operstatus,omitempty"`
}

type FDBStateEntry struct {
    Port       string `json:"port,omitempty"`
    Type       string `json:"type,omitempty"`
    VNI        string `json:"vni,omitempty"`
    RemoteVTEP string `json:"remote_vtep,omitempty"`
}

type RouteStateEntry struct {
    NextHop   string `json:"nexthop,omitempty"`
    Interface string `json:"ifname,omitempty"`
    Protocol  string `json:"protocol,omitempty"`
}

type TransceiverInfoEntry struct {
    Vendor          string `json:"vendor_name,omitempty"`
    Model           string `json:"model,omitempty"`
    SerialNum       string `json:"serial_num,omitempty"`
    HardwareVersion string `json:"hardware_version,omitempty"`
    Type            string `json:"type,omitempty"`
    MediaInterface  string `json:"media_interface,omitempty"`
}

type TransceiverStatusEntry struct {
    Present     string `json:"present,omitempty"`
    Temperature string `json:"temperature,omitempty"`
    Voltage     string `json:"voltage,omitempty"`
    TxPower     string `json:"tx_power,omitempty"`
    RxPower     string `json:"rx_power,omitempty"`
}
```

### 5.3 StateDBClient

```go
// StateDBClient wraps Redis client for state_db access (DB 6)
type StateDBClient struct {
    client *redis.Client
    ctx    context.Context
}

func NewStateDBClient(addr string) *StateDBClient
func (c *StateDBClient) Connect() error
func (c *StateDBClient) Close() error
func (c *StateDBClient) GetAll() (*StateDB, error)
func (c *StateDBClient) GetPortState(name string) (*PortStateEntry, error)
func (c *StateDBClient) GetLAGState(name string) (*LAGStateEntry, error)
func (c *StateDBClient) GetLAGMemberState(lag, member string) (*LAGMemberStateEntry, error)
func (c *StateDBClient) GetBGPNeighborState(vrf, neighbor string) (*BGPNeighborStateEntry, error)
func (c *StateDBClient) GetVXLANTunnelState(name string) (*VXLANTunnelStateEntry, error)
func (c *StateDBClient) GetRemoteVTEPs() ([]string, error)
func (c *StateDBClient) GetRouteCount(vrf string) (int, error)
func (c *StateDBClient) GetFDBCount(vlan int) (int, error)
func (c *StateDBClient) GetTransceiverInfo(port string) (*TransceiverInfoEntry, error)
func (c *StateDBClient) GetTransceiverStatus(port string) (*TransceiverStatusEntry, error)
```

### 5.4 PopulateDeviceState

The `PopulateDeviceState` function merges data from STATE_DB and CONFIG_DB to build the unified `DeviceState`:

```go
// PopulateDeviceState fills DeviceState from StateDB data
func PopulateDeviceState(state *DeviceState, stateDB *StateDB, configDB *ConfigDB) {
    // Populate interface state from PORT_TABLE + CONFIG_DB VRF bindings
    for name, portState := range stateDB.PortTable {
        intfState := &InterfaceState{
            Name:        name,
            AdminStatus: portState.AdminStatus,
            OperStatus:  portState.OperStatus,
            Speed:       portState.Speed,
        }
        if portState.MTU != "" {
            intfState.MTU, _ = strconv.Atoi(portState.MTU)
        }
        if configDB != nil {
            if intfEntry, ok := configDB.Interface[name]; ok {
                intfState.VRF = intfEntry.VRFName
            }
        }
        state.Interfaces[name] = intfState
    }

    // Populate PortChannel state from LAG_TABLE + LAG_MEMBER_TABLE
    // ... (active members from member state "selected" field)

    // Populate BGP state
    state.BGP = &BGPState{Neighbors: make(map[string]*BGPNeighborState)}
    if configDB != nil {
        if globals, ok := configDB.BGPGlobals["default"]; ok {
            state.BGP.LocalAS, _ = strconv.Atoi(globals.LocalASN)
            state.BGP.RouterID = globals.RouterID
        }
    }

    // Populate EVPN state from VXLAN_TUNNEL_TABLE
    // Distinguishes local VTEP (exists in configDB.VXLANTunnel) from remote VTEPs
    state.EVPN = &EVPNState{}
    for name, tunnelState := range stateDB.VXLANTunnelTable {
        if configDB != nil {
            if _, ok := configDB.VXLANTunnel[name]; ok {
                state.EVPN.VTEPState = tunnelState.OperStatus // Local VTEP
            } else {
                state.EVPN.RemoteVTEPs = append(state.EVPN.RemoteVTEPs, name)
            }
        }
    }
}
```

## 6. Redis Integration

### 6.1 Connection (`pkg/device/device.go`)

The connection logic uses SSH tunnels when `SSHUser` and `SSHPass` are present in the resolved profile. When these are absent (e.g., integration tests with standalone Redis), a direct connection is made.

```go
func (d *Device) Connect(ctx context.Context) error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if d.connected {
        return nil
    }

    var addr string
    if d.Profile.SSHUser != "" && d.Profile.SSHPass != "" {
        tun, err := NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass, d.Profile.SSHPort)
        if err != nil {
            return fmt.Errorf("SSH tunnel to %s: %w", d.Name, err)
        }
        d.tunnel = tun
        addr = tun.LocalAddr()
    } else {
        addr = fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
    }

    // Connect to CONFIG_DB (DB 4)
    d.client = NewConfigDBClient(addr)
    if err := d.client.Connect(); err != nil {
        return fmt.Errorf("connecting to config_db on %s: %w", d.Name, err)
    }

    // Load config_db
    var err error
    d.ConfigDB, err = d.client.GetAll()
    if err != nil {
        d.client.Close()
        return fmt.Errorf("loading config_db from %s: %w", d.Name, err)
    }

    // Connect to STATE_DB (DB 6)
    d.stateClient = NewStateDBClient(addr)
    if err := d.stateClient.Connect(); err != nil {
        // State DB connection failure is non-fatal - log warning and continue
        util.WithDevice(d.Name).Warnf("Failed to connect to state_db: %v", err)
    } else {
        d.StateDB, err = d.stateClient.GetAll()
        if err != nil {
            util.WithDevice(d.Name).Warnf("Failed to load state_db: %v", err)
        } else {
            PopulateDeviceState(d.State, d.StateDB, d.ConfigDB)
        }
    }

    d.connected = true
    return nil
}
```

**Key points:**
- Both CONFIG_DB and STATE_DB share the same Redis address (same tunnel)
- STATE_DB failure is non-fatal: the device remains usable for config operations
- The `ConfigDB` and `StateDB` snapshots are loaded in full on connect
- A single SSH tunnel multiplexes both DB 4 and DB 6 connections

### 6.2 State Loading (`pkg/device/state.go`)

State is loaded from CONFIG_DB for structural information (VRF bindings, VLAN members, ACL bindings), and from STATE_DB for operational state (oper_status, BGP sessions, LACP state).

```go
func (d *Device) LoadState(ctx context.Context) error {
    if err := d.RequireConnected(); err != nil {
        return err
    }

    d.mu.Lock()
    defer d.mu.Unlock()

    var err error
    d.ConfigDB, err = d.client.GetAll()
    if err != nil {
        return fmt.Errorf("loading config_db: %w", err)
    }

    d.State.Interfaces = d.parseInterfaces()
    d.State.PortChannels = d.parsePortChannels()
    d.State.VLANs = d.parseVLANs()
    d.State.VRFs = d.parseVRFs()

    return nil
}
```

### 6.3 Writing Changes

```go
// ApplyChanges writes a set of changes to config_db via Redis
func (d *Device) ApplyChanges(changes []ConfigChange) error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.connected {
        return util.ErrNotConnected
    }
    if !d.locked {
        return fmt.Errorf("device must be locked for changes")
    }

    for _, change := range changes {
        var err error
        switch change.Type {
        case ChangeTypeAdd, ChangeTypeModify:
            err = d.client.Set(change.Table, change.Key, change.Fields)
        case ChangeTypeDelete:
            err = d.client.Delete(change.Table, change.Key)
        }
        if err != nil {
            return fmt.Errorf("applying change to %s|%s: %w", change.Table, change.Key, err)
        }
    }

    // Reload config_db to reflect changes
    d.ConfigDB, _ = d.client.GetAll()

    return nil
}
```

### 6.3.1 Pipeline-Based Write Path (v3)

Alongside the sequential `ApplyChanges()` path, v3 adds a pipeline-based write path for atomic multi-entry operations:

```go
// ApplyChangesPipelined writes a set of changes atomically via Redis MULTI/EXEC.
// Used when atomicity is required (composite delivery, bulk operations).
func (d *Device) ApplyChangesPipelined(changes []ConfigChange) error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.connected || !d.locked {
        return fmt.Errorf("device must be connected and locked")
    }

    // Convert changes to pipeline format
    var sets []TableChange
    var dels []TableKey
    for _, c := range changes {
        switch c.Type {
        case ChangeTypeAdd, ChangeTypeModify:
            sets = append(sets, TableChange{Table: c.Table, Key: c.Key, Fields: c.Fields})
        case ChangeTypeDelete:
            dels = append(dels, TableKey{Table: c.Table, Key: c.Key})
        }
    }

    // Execute atomically
    if len(sets) > 0 {
        if err := d.client.PipelineSet(sets); err != nil {
            return fmt.Errorf("pipeline set: %w", err)
        }
    }
    if len(dels) > 0 {
        if err := d.client.PipelineDelete(dels); err != nil {
            return fmt.Errorf("pipeline delete: %w", err)
        }
    }

    d.ConfigDB, _ = d.client.GetAll()
    return nil
}
```

**When to use each write path:**

| Path | Method | Use Case |
|------|--------|----------|
| Sequential | `ApplyChanges()` | Normal operations (dry-run preview, individual changes) |
| Pipeline | `ApplyChangesPipelined()` | Composite delivery, bulk operations requiring atomicity |
| Full replace | `ReplaceAll()` | Composite overwrite mode (flush + pipeline write) |

### 6.4 Disconnect with Tunnel Cleanup

```go
func (d *Device) Disconnect() error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.connected {
        return nil
    }

    if d.locked {
        if err := d.unlock(); err != nil {
            util.WithDevice(d.Name).Warnf("Failed to release lock: %v", err)
        }
    }

    if d.client != nil {
        d.client.Close()
    }

    if d.stateClient != nil {
        d.stateClient.Close()
    }

    // Close SSH tunnel last (after Redis clients)
    if d.tunnel != nil {
        d.tunnel.Close()
        d.tunnel = nil
    }

    d.connected = false
    return nil
}
```

### 6.5 SONiC Redis Database Layout

SONiC uses multiple Redis databases within a single Redis instance:

| DB | Name | Purpose | Newtron Access |
|----|------|---------|----------------|
| 0 | APPL_DB | Application state (routes, neighbors) | Read (health checks) |
| 1 | ASIC_DB | ASIC-programmed state (SAI objects) | Read (convergence tests) |
| 2 | COUNTERS_DB | Interface/port counters | Not used |
| 3 | LOGLEVEL_DB | Logging configuration | Not used |
| 4 | CONFIG_DB | Configuration (ports, VLANs, BGP, etc.) | **Read/Write** |
| 5 | FLEX_COUNTER_DB | Flexible counters | Not used |
| 6 | STATE_DB | Operational state (oper_status, BGP state) | **Read** |

### 6.6 Pipeline Operations (v3)

Composite delivery and bulk operations use Redis pipelines for atomicity and performance.

**Redis MULTI/EXEC semantics:**

```
MULTI                           -- start transaction
HSET BGP_GLOBALS|default router_id 10.0.0.1 local_asn 65000
HSET BGP_NEIGHBOR|10.0.0.2 asn 65000 local_addr 10.0.0.1
HSET BGP_NEIGHBOR_AF|10.0.0.2|ipv4_unicast activate true
DEL ROUTE_MAP|OLD_MAP|10
EXEC                            -- execute atomically
```

**Why pipelines:**
- **Atomicity**: Either all changes apply or none do. Prevents partial config states that could cause SONiC daemon issues.
- **Performance**: Single round-trip vs one per entry. A composite with 200 entries takes 1 round-trip instead of 200.
- **Consistency**: SONiC daemons see the complete change set at once via keyspace notifications, rather than processing entries one at a time.

**Error handling:**
- If any command in the pipeline fails, the entire MULTI/EXEC transaction is discarded
- The pipeline returns per-command results; the wrapper checks all results and returns the first error
- On pipeline failure, CONFIG_DB is not reloaded (no changes were applied)

**ReplaceAll for overwrite mode:**
```go
func (c *ConfigDBClient) ReplaceAll(config *ConfigDB) error {
    // 1. FLUSHDB — clear all keys in DB 4
    // 2. Build pipeline of all HSET commands from config
    // 3. MULTI/EXEC the pipeline
    // This is used by composite overwrite mode only
}
```

## 7. Config Persistence

Redis changes made by newtron are **runtime only**. They take effect immediately because SONiC daemons subscribe to CONFIG_DB changes, but they do not survive a device reboot.

To persist configuration across reboots, the SONiC command `config save -y` must be run inside the VM. This writes the current CONFIG_DB contents to `/etc/sonic/config_db.json`, which is loaded at boot.

**Implications for testing:**

| Test Type | Persistence | Cleanup Strategy |
|-----------|------------|------------------|
| Unit tests | N/A (no Redis) | N/A |
| Integration tests | Ephemeral (standalone Redis) | Fresh Redis per test |
| E2E lab tests | Runtime only (SONiC-VS) | `ResetLabBaseline()` deletes stale keys |

E2E tests rely on ephemeral configuration. The `ResetLabBaseline()` function (section 12.5) cleans known stale keys before each test suite run. Tests do not call `config save -y`, so a simple VM restart restores the baseline.

## 8. Operation Implementations (Methods on Objects)

Operations are methods on the objects they operate on. This follows true OO design where operations belong to their objects rather than being separate Command pattern structs.

### 8.1 Interface Operations (`pkg/network/interface_ops.go`)

Interface operations are methods on the `Interface` type. All operations return a `*ChangeSet` for preview/execution.

**Complete Method List:**

```go
// ============================================================================
// Service Management
// ============================================================================

// ApplyService applies a service definition to this interface.
// Creates VRF, ACLs, IP configuration, and service binding tracking.
func (i *Interface) ApplyService(ctx context.Context, serviceName, ipAddr string) (*ChangeSet, error)

// RemoveService removes the service from this interface.
// Uses DependencyChecker to safely clean up shared resources (ACLs, VRFs).
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error)

// RefreshService reapplies the service to sync with current definition.
// Compares current interface config with current service spec and
// generates changes to synchronize (updated filters, QoS, etc.).
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
// Route-Map Binding (v3)
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

**Example Implementation (ApplyService):**

```go
func (i *Interface) ApplyService(ctx context.Context, serviceName, ipAddr string) (*ChangeSet, error) {
    d := i.Device()

    if !d.IsConnected() {
        return nil, fmt.Errorf("device not connected")
    }
    if !d.IsLocked() {
        return nil, fmt.Errorf("device not locked")
    }

    // Get service definition from Network (via parent chain)
    svc, err := i.Network().GetService(serviceName)
    if err != nil {
        return nil, fmt.Errorf("service not found: %w", err)
    }

    cs := NewChangeSet(d.Name(), "interface.apply-service")

    // Create VRF if needed (for L3/IRB with per-interface VRF)
    if (svc.ServiceType == "l3" || svc.ServiceType == "irb") && svc.VRFType == "interface" {
        vrfName := util.DeriveVRFName(svc.VRFType, serviceName, i.name)
        cs.Add("VRF", vrfName, ChangeAdd, nil, map[string]string{
            "vni": fmt.Sprintf("%d", ipvpnDef.L3VNI),
        })
    }

    // Generate ACLs from filter specs (per-service, shared across interfaces)
    if svc.IngressFilter != "" {
        aclName := util.DeriveACLName(serviceName, "in")
        // Check if ACL exists - add interface to ports or create new
    }

    // Record service binding for tracking
    cs.Add("NEWTRON_SERVICE_BINDING", i.name, ChangeAdd, nil, bindingFields)

    return cs, nil
}
```

### 8.2 Device Operations (`pkg/network/device_ops.go`)

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
func (d *Device) AddACLRule(ctx context.Context, tableName string, rule ACLRule) (*ChangeSet, error)
func (d *Device) UnbindACLFromPort(ctx context.Context, aclName, port string) (*ChangeSet, error)

// ============================================================================
// EVPN/VTEP Management
// ============================================================================

func (d *Device) CreateVTEP(ctx context.Context, opts VTEPConfig) (*ChangeSet, error)
func (d *Device) DeleteVTEP(ctx context.Context, name string) (*ChangeSet, error)
func (d *Device) SetupBGPEVPN(ctx context.Context, neighbors []string) (*ChangeSet, error)
func (d *Device) AddLoopbackBGPNeighbor(ctx context.Context, cfg LoopbackBGPNeighborConfig) (*ChangeSet, error)
func (d *Device) MapL2VNI(ctx context.Context, vlanID, vni int) (*ChangeSet, error)
func (d *Device) MapL3VNI(ctx context.Context, vrfName string, vni int) (*ChangeSet, error)
func (d *Device) UnmapVNI(ctx context.Context, vni int) (*ChangeSet, error)

// ============================================================================
// Health Checks and Maintenance
// ============================================================================

// RunHealthChecks runs health checks on the device.
// checkType can be: "bgp", "interfaces", "evpn", "lag", or "" for all.
func (d *Device) RunHealthChecks(ctx context.Context, checkType string) ([]HealthCheckResult, error)

// ApplyBaseline applies a baseline configlet to the device.
// vars is a list of "key=value" strings for template substitution.
func (d *Device) ApplyBaseline(ctx context.Context, configletName string, vars []string) (*ChangeSet, error)

// Cleanup identifies and removes orphaned configurations.
// cleanupType can be: "acl", "vrf", "vni", or "" for all.
func (d *Device) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error)

// ============================================================================
// Query Methods (no ChangeSet returned)
// ============================================================================

func (d *Device) ListVLANs() []int
func (d *Device) ListVRFs() []string
func (d *Device) ListPortChannels() []string
func (d *Device) ListInterfaces() []string
func (d *Device) ListACLTables() []string
func (d *Device) ListBGPNeighbors() []string
func (d *Device) GetOrphanedACLs() []string
func (d *Device) VTEPSourceIP() string

// ============================================================================
// BGP Management (v3 — frrcfgd)
// ============================================================================

// SetBGPGlobals configures BGP global settings (ASN, router-id, flags).
func (d *Device) SetBGPGlobals(ctx context.Context, cfg BGPGlobalsConfig) (*ChangeSet, error)

// SetupRouteReflector performs full RR setup: all 3 AFs, cluster-id, RR client, next-hop-self.
// Replaces SetupBGPEVPN (which only did l2vpn_evpn).
//
// Pseudo-code:
//   BGP_GLOBALS "default": local_asn, router_id, rr_cluster_id
//   For each neighbor:
//     BGP_NEIGHBOR: asn, local_addr, admin_status
//     BGP_NEIGHBOR_AF "<ip>|ipv4_unicast": activate, rr_client, next_hop_self
//     BGP_NEIGHBOR_AF "<ip>|ipv6_unicast": activate, rr_client, next_hop_self
//     BGP_NEIGHBOR_AF "<ip>|l2vpn_evpn": activate, rr_client
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
// Composite Delivery (v3)
// ============================================================================

// DeliverComposite delivers a composite config to the device.
// mode=overwrite: ReplaceAll (flush + pipeline write)
// mode=merge: validate no conflicts, then PipelineSet
func (d *Device) DeliverComposite(ctx context.Context, composite *CompositeConfig, mode CompositeMode) (*CompositeDeliveryResult, error)

// ValidateComposite validates a composite config before delivery (dry-run).
// Returns errors for any conflicts or invalid entries.
func (d *Device) ValidateComposite(ctx context.Context, composite *CompositeConfig, mode CompositeMode) error
```

### 8.3 Operation Configuration Types

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

**v3 operation configuration types:**

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

### 8.4 Topology Provisioning Operations (`pkg/network/topology.go`) (v4)

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
```

## 9. Precondition Checker

### 9.1 Implementation (`pkg/operations/precondition.go`)

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
func (pc *PreconditionChecker) RequireLocked()
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

## 10. Value Derivation

### 10.1 Auto-Derived Values (`pkg/util/derive.go`)

```go
// DerivedValues contains auto-computed values
type DerivedValues struct {
    NeighborIP    string
    NetworkAddr   string
    BroadcastAddr string
    SubnetMask    string
    VRFName       string
    ACLNameBase   string
    Description   string
}

// DeriveFromInterface computes values from interface and IP
func DeriveFromInterface(intf, ipWithMask string, svc *spec.ServiceSpec) *DerivedValues

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

### 10.2 Route Redistribution Defaults (v3)

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

### 10.3 Specification Resolution (`pkg/spec/resolver.go`)

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

    // Affinity: profile > region > default
    r.Affinity = coalesce(profile.Affinity, region.Affinity, "flat")

    // Router ID and VTEP from loopback
    r.RouterID = profile.LoopbackIP
    r.VTEPSourceIP = profile.LoopbackIP
    r.VTEPSourceIntf = "Loopback0"

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

## 11. Audit Logging

### 11.1 Event Types (`pkg/audit/event.go`)

```go
type AuditEvent struct {
    ID          string              `json:"id"`
    Timestamp   time.Time           `json:"timestamp"`
    User        string              `json:"user"`
    Device      string              `json:"device"`
    Operation   string              `json:"operation"`
    Service     string              `json:"service,omitempty"`
    Interface   string              `json:"interface,omitempty"`
    Changes     []operations.Change `json:"changes"`
    Success     bool                `json:"success"`
    Error       string              `json:"error,omitempty"`
    ExecuteMode bool                `json:"execute_mode"`
    DryRun      bool                `json:"dry_run"`
}
```

### 11.2 Logger Interface (`pkg/audit/logger.go`)

```go
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

## 12. Permission System

### 12.1 Permission Definitions (`pkg/auth/permission.go`)

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

### 12.2 Permission Checker (`pkg/auth/checker.go`)

```go
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

## 13. Error Types

### 13.1 Custom Errors (`pkg/util/errors.go`)

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

## 14. CLI Implementation

### 14.1 OO CLI Design Pattern

The CLI follows a true object-oriented design where:
- **Context flags** (`-n`, `-d`, `-i`) select the object (like `this` in OOP)
- **Command verbs** are methods on that object

```
newtron -n <network> -d <device> -i <interface> <verb> [args] [-x]
         |--------------------|---------------------|   |------|
                Object Selection                    Method Call
```

### 14.2 Root Command (`cmd/newtron/main.go`)

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
    rootCmd.PersistentFlags().BoolVarP(&verboseMode, "verbose", "v", false, "Verbose output")
    rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

    // Command registration
    rootCmd.AddCommand(listCmd, showCmd, settingsCmd)           // Network-level
    rootCmd.AddCommand(createCmd, deleteCmd, healthCmd)         // Device-level
    rootCmd.AddCommand(setCmd, applyServiceCmd, removeServiceCmd) // Interface-level
    rootCmd.AddCommand(addMemberCmd, removeMemberCmd)
    rootCmd.AddCommand(bindAclCmd, unbindAclCmd)
    rootCmd.AddCommand(interactiveCmd, versionCmd)
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

### 14.3 Symmetric Read/Write Operations

| Write Verb | Read Verb | Description |
|------------|-----------|-------------|
| `set <prop> <val>` | `get <prop>` | Property access |
| `apply-service` / `remove-service` / `refresh-service` | `get-service` | Service binding |
| `add-member` / `remove-member` | `list-members` | Collection membership |
| `bind-acl` / `unbind-acl` | `list-acls` | ACL binding |
| `map-l2vni` / `unmap-l2vni` | `get-l2vni` | VNI mapping |
| `add-bgp-neighbor` / `remove-bgp-neighbor` | `list-bgp-neighbors` | BGP neighbors |

### 14.4 BGP Commands

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

### 14.5 Service Immutability Model

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

### 14.6 Service Refresh

When a service definition changes (e.g., filter-spec updated in `network.json`), interfaces using that service can be synchronized:

```go
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
    svc, err := i.Network().GetService(i.serviceName)
    if err != nil {
        return nil, err
    }

    cs := NewChangeSet(i.Device().Name(), "interface.refresh-service")

    // Compare ingress filter: check if filter-spec content has changed
    if svc.IngressFilter != "" {
        currentACL := i.GetBoundACL("ingress")
        filterSpec, _ := i.Network().GetFilterSpec(svc.IngressFilter)
        if i.aclNeedsUpdate(currentACL, filterSpec) {
            // Create new ACL with updated rules
            // Unbind old ACL
            // Mark old ACL for cleanup (if orphaned)
        }
    }

    // Similar logic for egress filter, QoS profile, etc.
    return cs, nil
}
```

### 14.7 Orphan Cleanup

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

### 14.8 Settings Persistence

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

## 15. Testing Strategy

### 15.1 Three-Tier Testing Architecture

Newtron uses three tiers of tests, each with different scope and infrastructure requirements:

| Tier | Build Tag | Infrastructure | Speed | What It Tests |
|------|-----------|---------------|-------|---------------|
| Unit | `unit` | None | Fast (~1s) | Pure logic: IP math, name normalization, spec resolution |
| Integration | `integration` | Standalone Redis | Medium (~10s) | Redis operations: CONFIG_DB read/write, state loading |
| E2E | `e2e` | Lab (containerlab + SONiC-VS) | Slow (~5min) | Full stack: SSH tunnel, real SONiC, ASIC convergence |

### 15.2 Unit Tests

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

### 15.3 Integration Tests

Integration tests use a standalone Redis instance (no SSH tunnel). The `SSHUser`/`SSHPass` fields are left empty in test profiles, causing `Device.Connect()` to use the direct `<ip>:6379` path.

```go
//go:build integration

func TestApplyServiceOp_Validate(t *testing.T) {
    dev := &device.Device{
        State: &device.DeviceState{
            Interfaces: map[string]*device.InterfaceState{
                "Ethernet0": {Name: "Ethernet0"},
            },
        },
    }
    dev.SetConnected(true)
    dev.SetLocked(true)

    // ... validate operation against real Redis
}
```

### 15.4 E2E Lab Tests (`internal/testutil/lab.go`)

E2E tests run against a live containerlab topology with SONiC-VS nodes. The test infrastructure provides:

**Node Discovery:**

```go
// LabNodes discovers running lab nodes and their IPs via containerlab inspect.
func LabNodes(t *testing.T) []LabNode

// LabSonicNodes returns only non-server nodes (SONiC devices with profiles and Redis).
// Server nodes are filtered by checking for profile file existence.
func LabSonicNodes(t *testing.T) []LabNode
```

This distinction matters because lab topologies may include both SONiC switches (which have Redis and profiles) and server containers (which are Linux boxes for data-plane testing). Only SONiC nodes have profiles and can accept newtron connections.

**SSH Tunnel Pool:**

E2E tests share SSH tunnels across test cases to avoid the overhead of establishing a new SSH session for each test.

```go
var (
    labTunnelsMu sync.Mutex
    labTunnels   map[string]*device.SSHTunnel
)

// labTunnelAddr returns a Redis address for a lab node.
// Establishes and caches an SSH tunnel when SSH credentials are in the profile.
func labTunnelAddr(t *testing.T, nodeName, nodeIP string) string

// CloseLabTunnels closes all shared SSH tunnels. Call from TestMain after m.Run().
func CloseLabTunnels()
```

**Assertion Helpers:**

```go
// AssertConfigDBEntry verifies a Redis hash in CONFIG_DB has the expected fields.
func AssertConfigDBEntry(t *testing.T, name, table, key string, expectedFields map[string]string)

// AssertConfigDBEntryExists checks that a key exists in CONFIG_DB.
func AssertConfigDBEntryExists(t *testing.T, name, table, key string)

// AssertConfigDBEntryAbsent checks that a key does NOT exist in CONFIG_DB.
func AssertConfigDBEntryAbsent(t *testing.T, name, table, key string)

// LabStateDBEntry reads a hash from STATE_DB (DB 6).
func LabStateDBEntry(t *testing.T, name, table, key string) map[string]string

// PollStateDB polls a STATE_DB entry until the expected value appears.
func PollStateDB(ctx context.Context, t *testing.T, name, table, key, field, want string) error
```

**Convergence Testing:**

The three-tier assertion model for E2E tests:

1. **CONFIG_DB (hard fail)**: Verify the key was written. If this fails, the operation itself failed.
2. **ASIC_DB (soft convergence)**: Poll ASIC_DB for orchagent processing. Uses timeout with retry.
3. **Data-plane (skip if unavailable)**: Ping between server containers. Skipped if server nodes are not present.

```go
// WaitForASICVLAN polls ASIC_DB for a VLAN entry with the given vid.
// Returns nil once a SAI_OBJECT_TYPE_VLAN entry with a matching
// SAI_VLAN_ATTR_VLAN_ID is found, or an error if the context expires.
func WaitForASICVLAN(ctx context.Context, t *testing.T, name string, vlanID int) error {
    client := LabRedisClient(t, name, 1) // ASIC_DB
    want := fmt.Sprintf("%d", vlanID)

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("timeout waiting for VLAN %d in ASIC_DB on %s", vlanID, name)
        default:
        }

        keys, err := client.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
        if err == nil {
            for _, key := range keys {
                vid, _ := client.HGet(ctx, key, "SAI_VLAN_ATTR_VLAN_ID").Result()
                if vid == want {
                    return nil
                }
            }
        }
        time.Sleep(1 * time.Second)
    }
}
```

**Server Container Helpers (Data-plane testing):**

```go
// ServerConfigureInterface configures an IP address on a server container's interface.
func ServerConfigureInterface(t *testing.T, serverName, iface, ipCIDR, gateway string)

// ServerPing pings targetIP from a server container. Returns true if packet received.
// On failure, runs ip addr/ip route/arp diagnostics.
func ServerPing(t *testing.T, serverName, targetIP string, count int) bool

// EnsureServerTools verifies network tools (ping, ip) are on the server container.
func EnsureServerTools(t *testing.T, serverName string)
```

### 15.5 ResetLabBaseline

E2E tests can leave stale CONFIG_DB entries from previous runs. These stale entries (especially VXLAN/VRF entries) can crash `vxlanmgrd` or `orchagent` when they try to process entries that reference deleted resources.

`ResetLabBaseline()` is called from `TestMain` before `m.Run()`:

```go
// staleE2EKeys lists CONFIG_DB keys that E2E tests may create.
var staleE2EKeys = []string{
    "VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700",
    "VLAN|Vlan700",
    "VRF|Vrf_e2e_irb",
    "INTERFACE|Ethernet2|10.90.1.1/30",
    "ACL_TABLE|E2E_TEST_ACL",
    // ... all known test-created keys
}

// ResetLabBaseline deletes stale CONFIG_DB entries from all SONiC nodes.
func ResetLabBaseline() error {
    // 1. Discover all SONiC nodes via containerlab inspect
    // 2. Read SSH credentials from each node's profile
    // 3. Build redis-cli DEL commands for all stale keys
    // 4. Execute in parallel on all nodes via SSH
    // 5. Sleep 5 seconds for orchagent to process deletions
}
```

**Cleanup Ordering:**

The `staleE2EKeys` list is ordered with dependencies last (e.g., VXLAN_TUNNEL_MAP before VLAN, INTERFACE IP keys before INTERFACE base keys). This prevents SONiC daemons from processing partial state.

### 15.6 Test Lifecycle Helpers

```go
// LabConnectedDevice connects to a lab node via the normal network path.
// Registers t.Cleanup to disconnect.
func LabConnectedDevice(t *testing.T, name string) *network.Device

// LabLockedDevice connects to and locks a lab node.
// Registers t.Cleanup to unlock and disconnect.
func LabLockedDevice(t *testing.T, name string) *network.Device

// LabCleanupChanges registers a cleanup that reconnects and applies undo changes.
// Uses a fresh device connection since the test's device may have stale cache.
func LabCleanupChanges(t *testing.T, nodeName string, fn func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error))

// LabContext returns a context with a 2-minute timeout for SONiC-VS operations.
func LabContext(t *testing.T) context.Context
```

### 15.7 E2E Test Structure Example

```go
//go:build e2e

func TestMain(m *testing.M) {
    if err := testutil.ResetLabBaseline(); err != nil {
        fmt.Fprintf(os.Stderr, "WARNING: %v\n", err)
    }
    code := m.Run()
    testutil.CloseLabTunnels()
    os.Exit(code)
}

func TestCreateVLAN(t *testing.T) {
    testutil.SkipIfNoLab(t)
    dev := testutil.LabLockedDevice(t, "leaf1")
    ctx := testutil.LabContext(t)

    // Register cleanup BEFORE creating
    testutil.LabCleanupChanges(t, "leaf1", func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
        return dev.DeleteVLAN(ctx, 500)
    })

    // Create VLAN
    cs, err := dev.CreateVLAN(ctx, 500, network.VLANConfig{Description: "test"})
    require.NoError(t, err)
    require.NoError(t, cs.Apply(dev))

    // Tier 1: CONFIG_DB assertion (hard fail)
    testutil.AssertConfigDBEntry(t, "leaf1", "VLAN", "Vlan500", map[string]string{
        "vlanid": "500",
    })

    // Tier 2: ASIC_DB convergence (soft, with timeout)
    asicCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    err = testutil.WaitForASICVLAN(asicCtx, t, "leaf1", 500)
    if err != nil {
        t.Logf("ASIC convergence: %v (continuing)", err)
    }
}
```
