# Newtron Low-Level Design (LLD)

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md). For the network-level architecture, see [newtron HLD](hld.md). For the device connection layer (SSH tunnels, Redis clients), see [Device Layer LLD](device-lld.md).


---

## 1. Spec vs Config: Fundamental Architecture

Newtron separates **specification** (declarative intent in `pkg/newtron/spec`) from **configuration** (imperative device state in `pkg/newtron/device/sonic`). The `pkg/newtron/network` layer translates specs into config. See HLD §2 for the full rationale.

| Layer | Package | Data | Edited by |
|-------|---------|------|-----------|
| Specification | `pkg/newtron/spec` | `specs/*.json` — policies, references | Network architects |
| Translation | `pkg/newtron/network` | In-memory — ChangeSet generation | Auto (newtron) |
| Configuration | `pkg/newtron/device/sonic` | Redis CONFIG_DB — concrete values | Auto (newtron) |

## 2. Package Structure

```
newtron/
├── cmd/
│   ├── newtron/                     # CLI application (noun-group pattern)
│   │   ├── main.go                  # Entry point, root command, implicit device detection
│   │   ├── cmd_interface.go         # Interface subcommands (list/show/get/set)
│   │   ├── cmd_vlan.go              # VLAN subcommands (list/show/status/create/delete/add-interface/...)
│   │   ├── cmd_vrf.go               # VRF subcommands (list/show/status/create/delete/add-interface/bind-ipvpn/add-neighbor/add-route/...)
│   │   ├── cmd_lag.go               # LAG subcommands (list/show/status/create/delete/add-interface/remove-interface)
│   │   ├── cmd_acl.go               # ACL subcommands (list/show/create/delete/add-rule/delete-rule/bind/unbind)
│   │   ├── cmd_evpn.go              # EVPN overlay (setup/status/ipvpn/macvpn)
│   │   ├── cmd_bgp.go               # BGP status (visibility-only)
│   │   ├── cmd_service.go           # Service subcommands (list/show/get/apply/remove/refresh/create/delete)
│   │   ├── cmd_qos.go               # QoS subcommands (list/show/create/delete/add-queue/remove-queue/apply/remove)
│   │   ├── cmd_filter.go            # Filter subcommands (list/show/create/delete/add-rule/remove-rule)
│   │   ├── cmd_show.go              # Device show command
│   │   ├── cmd_device.go            # Device cleanup command
│   │   ├── cmd_health.go            # Health check subcommands
│   │   ├── cmd_audit.go             # Audit subcommands
│   │   ├── cmd_settings.go          # Settings management
│   │   ├── cmd_provision.go         # Topology provisioning commands
│   │   └── shell.go                 # Interactive shell with readline
├── pkg/
│   └── newtron/
│       ├── network/                     # Network struct, topology, spec->config translation
│       │   ├── network.go               # Top-level Network object (owns specs + spec persistence)
│       │   ├── topology.go              # TopologyProvisioner, ProvisionDevice, ProvisionInterface
│       │   └── node/                    # Node (formerly Device), Interface, operations
│       │       ├── node.go              # Node with SpecProvider interface, connection management
│       │       ├── interface.go         # Interface with parent reference to Node
│       │       ├── precondition.go      # PreconditionChecker, DependencyChecker
│       │       ├── changeset.go         # ChangeSet for tracking config changes
│       │       ├── composite.go         # CompositeBuilder, CompositeConfig, CompositeMode types
│       │       ├── acl_ops.go           # ACL operations (CreateACL, AddACLRule, BindACL)
│       │       ├── baseline_ops.go      # Loopback operations (ConfigureLoopback, RemoveLoopback)
│       │       ├── bgp_ops.go           # BGP operations (ConfigureBGP, BGP globals/AF)
│       │       ├── cleanup_ops.go       # Orphan cleanup (ACLs, VRFs, VNIs)
│       │       ├── evpn_ops.go          # EVPN operations (SetupEVPN, BindIPVPN, BindMACVPN)
│       │       ├── health_ops.go        # Health check operations (RunHealthChecks)
│       │       ├── interface_bgp_ops.go # Interface BGP operations (AddBGPNeighbor)
│       │       ├── interface_ops.go     # Interface operations (SetAdminStatus, SetIP)
│       │       ├── macvpn_ops.go        # MAC-VPN operations (BindMACVPN, UnbindMACVPN)
│       │       ├── portchannel_ops.go   # PortChannel operations (CreateLAG, DeleteLAG)
│       │       ├── qos.go               # QoS CONFIG_DB entry generation
│       │       ├── qos_ops.go           # QoS operations (ApplyQoS, RemoveQoS)
│       │       ├── service_gen.go       # Service CONFIG_DB entry generation
│       │       ├── service_ops.go       # Service operations (ApplyService, RemoveService)
│       │       ├── vlan_ops.go          # VLAN operations (CreateVLAN, DeleteVLAN)
│       │       └── vrf_ops.go           # VRF operations (CreateVRF, DeleteVRF)
│       ├── spec/                        # Specification loading (declarative intent)
│       │   ├── types.go                 # Spec structs (NetworkSpecFile, ServiceSpec, etc.)
│       │   └── loader.go                # JSON loading and validation
│       ├── device/
│       │   └── sonic/                   # SONiC Redis implementation + shared types
│       │       ├── types.go             # Entry, SSHTunnel, ConfigChange, RouteEntry, VerificationResult, etc.
│       │       ├── device.go            # Device struct, Connect, Disconnect, Lock
│       │       ├── configdb.go          # CONFIG_DB (DB 4) mapping + client
│       │       ├── configdb_parsers.go  # Table-driven parsers for CONFIG_DB entries
│       │       ├── statedb.go           # STATE_DB (DB 6) mapping + client
│       │       ├── statedb_parsers.go   # Table-driven parsers for STATE_DB entries
│       │       ├── appldb.go            # APP_DB (DB 0) mapping + client
│       │       ├── asicdb.go            # ASIC_DB (DB 1) mapping + client
│       │       └── pipeline.go          # Redis MULTI/EXEC pipeline client
│   ├── audit/                       # Audit logging
│   │   ├── event.go                 # Event types (uses node.Change, alias for sonic.ConfigChange)
│   │   └── logger.go                # Logger implementation
│   ├── auth/                        # Authorization
│   │   ├── permission.go            # Permission definitions
│   │   └── checker.go               # Permission checking
│   ├── settings/                    # CLI user settings persistence
│   │   └── settings.go              # DefaultNetwork, SpecDir, audit config
│   └── util/                        # Utilities
│       ├── derive.go                # Value derivation
│       ├── errors.go                # Custom error types
│       ├── ip.go                    # IP address utilities
│       ├── log.go                   # Logging utilities
│       └── strings.go               # String utilities
├── specs/                           # Specification files (declarative intent)
│   ├── network.json                 # Services, filters, VPNs, zones
│   └── profiles/                    # Per-device profiles
└── docs/                            # Documentation
```

**Additional files not shown in the tree:**

| File | Purpose |
|------|---------|
| `pkg/newtron/network/node/bgp_ops.go` | BGP global configuration (ConfigureBGP, BGP globals/AF) |
| `pkg/newtron/network/node/cleanup_ops.go` | Orphan cleanup (ACLs, VRFs, VNIs) |
| `pkg/newtron/network/node/macvpn_ops.go` | MAC-VPN operations (BindMACVPN, UnbindMACVPN) |
| `pkg/newtron/network/node/composite.go` | CompositeBuilder, CompositeConfig, CompositeMode types; offline composite CONFIG_DB generation and delivery |
| `pkg/newtron/network/topology.go` | TopologyProvisioner, ProvisionDevice, ProvisionInterface |
| `pkg/newtron/network/node/qos.go` | generateQoSDeviceEntries, generateQoSInterfaceEntries, resolveServiceQoSPolicy |

## 3. Core Data Structures

### 3.1 Specification Types (`pkg/newtron/spec/types.go`)

These types define **declarative intent** - what you want, not how to achieve it.

```go
// NetworkSpecFile - Global network specification file (declarative)
type NetworkSpecFile struct {
    Version      string                       `json:"version"`
    SuperUsers   []string                     `json:"super_users"`
    UserGroups   map[string][]string          `json:"user_groups"`
    Permissions  map[string][]string          `json:"permissions"`
    Zones      map[string]*ZoneSpec       `json:"zones"`
    PrefixLists  map[string][]string          `json:"prefix_lists"`
    Filters      map[string]*FilterSpec       `json:"filters"`
    QoSPolicies  map[string]*QoSPolicy         `json:"qos_policies,omitempty"`
    QoSProfiles  map[string]*QoSProfile `json:"qos_profiles,omitempty"` // Legacy

    // Route policies for BGP import/export
    RoutePolicies map[string]*RoutePolicy `json:"route_policies,omitempty"`

    // VPN definitions (referenced by services)
    IPVPNs  map[string]*IPVPNSpec  `json:"ipvpns"`  // IP-VPN (L3VNI, route targets)
    MACVPNs map[string]*MACVPNSpec `json:"macvpns"` // MAC-VPN (VLAN, L2VNI)

    // Service definitions (reference ipvpn/macvpn by name)
    Services map[string]*ServiceSpec `json:"services"`
}

// IPVPNSpec defines IP-VPN parameters for L3 routing (Type-5 routes).
// Referenced by services via the "ipvpn" field.
type IPVPNSpec struct {
    Description  string   `json:"description,omitempty"`
    VRF          string   `json:"vrf"`
    L3VNI        int      `json:"l3vni"`
    L3VNIVlan    int      `json:"l3vni_vlan,omitempty"` // Dedicated transit VLAN for L3VNI decap
    RouteTargets []string `json:"route_targets"`
}

// MACVPNSpec defines MAC-VPN parameters for L2 bridging (EVPN Type-2 routes).
// Referenced by services via the "macvpn" field.
// VlanID is the local bridge domain ID, identical on all devices.
type MACVPNSpec struct {
    Description    string   `json:"description,omitempty"`
    VlanID         int      `json:"vlan_id"`
    VNI            int      `json:"vni"`
    AnycastIP      string   `json:"anycast_ip,omitempty"`
    AnycastMAC     string   `json:"anycast_mac,omitempty"`
    RouteTargets   []string `json:"route_targets,omitempty"`
    ARPSuppression bool     `json:"arp_suppression,omitempty"`
}

// ZoneSpec - Zone network settings (embeds OverridableSpecs for zone-level overrides)
type ZoneSpec struct {
    OverridableSpecs // Embedded — zone-level overrides
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
    ServiceType string `json:"service_type"` // evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed

    // VPN references (names from ipvpn/macvpn sections)
    IPVPN   string `json:"ipvpn,omitempty"`
    MACVPN  string `json:"macvpn,omitempty"`
    VRFType string `json:"vrf_type,omitempty"` // "interface" or "shared"

    // Routing protocol specification
    Routing *RoutingSpec `json:"routing,omitempty"`

    // Filters (references to filters)
    IngressFilter string `json:"ingress_filter,omitempty"`
    EgressFilter  string `json:"egress_filter,omitempty"`

    // QoS
    QoSPolicy  string `json:"qos_policy,omitempty"`
    QoSProfile string `json:"qos_profile,omitempty"` // Legacy — kept for backward compat

    // Permissions (override global permissions for this service)
    Permissions map[string][]string `json:"permissions,omitempty"`
}

// RoutingSpec defines routing protocol specification for a service.
//
// For BGP services:
//   - Local AS is always from device profile (ResolvedProfile.UnderlayASN)
//   - Peer AS can be fixed (number), or "request" (provided at apply time)
//   - Peer IP is derived from interface IP for point-to-point links
type RoutingSpec struct {
    Protocol     string `json:"protocol"`                // "bgp", "static", or empty
    PeerAS       string `json:"peer_as,omitempty"`       // AS number, or "request"
    ImportPolicy string `json:"import_policy,omitempty"` // Reference to route_policies
    ExportPolicy string `json:"export_policy,omitempty"` // Reference to route_policies

    ImportCommunity  string `json:"import_community,omitempty"`   // BGP community for import filtering
    ExportCommunity  string `json:"export_community,omitempty"`   // BGP community to attach on export
    ImportPrefixList string `json:"import_prefix_list,omitempty"` // prefix-list ref for import filtering
    ExportPrefixList string `json:"export_prefix_list,omitempty"` // prefix-list ref for export filtering
    Redistribute     *bool  `json:"redistribute,omitempty"`       // override default (service=true, transit=false)
}

// FilterSpec defines a reusable set of ACL rules.
type FilterSpec struct {
    Description string        `json:"description"`
    Type        string        `json:"type"` // ipv4, ipv6 (translated to L3, L3V6 for CONFIG_DB)
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
    Log           bool   `json:"log,omitempty"`
}

// QoSPolicy defines a declarative queue policy.
// Lives in pkg/newtron/spec/types.go. Referenced by NetworkSpecFile.QoSPolicies.
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
// Lives in pkg/newtron/spec/types.go. Superseded by QoSPolicy.
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
type DeviceProfile struct {
    // REQUIRED - must be specified
    MgmtIP     string `json:"mgmt_ip"`
    LoopbackIP string `json:"loopback_ip"`
    Zone     string `json:"zone"` // Zone name - resolved from network.json zones

    // OPTIONAL OVERRIDES - if set, override zone/global values
    ASNumber *int         `json:"as_number,omitempty"`
    EVPN     *EVPNConfig  `json:"evpn,omitempty"` // EVPN peers, route reflector, cluster ID

    // OPTIONAL - device-specific
    Platform        string              `json:"platform,omitempty"`
    MAC             string              `json:"mac,omitempty"`
    UnderlayASN     int                 `json:"underlay_asn,omitempty"`
    VLANPortMapping map[int][]string    `json:"vlan_port_mapping,omitempty"`
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

// EVPNConfig - EVPN peer and route reflector configuration
type EVPNConfig struct {
    Peers           []string `json:"peers,omitempty"`            // BGP peer device names (loopbacks resolved at runtime)
    RouteReflector  bool     `json:"route_reflector,omitempty"`  // Is this device a route reflector?
    ClusterID       string   `json:"cluster_id,omitempty"`       // BGP RR cluster-id (defaults to loopback if empty)
}

// ResolvedProfile - Fully resolved device profile
type ResolvedProfile struct {
    // From profile
    DeviceName string
    MgmtIP     string
    LoopbackIP string
    Zone     string
    Platform   string

    // Resolved from inheritance
    IsRouteReflector bool
    ClusterID        string // RR cluster ID; from profile EVPN config or defaults to loopback IP

    // Derived at runtime
    RouterID        string            // = LoopbackIP
    VTEPSourceIP    string            // = LoopbackIP
    BGPNeighbors    []string          // From profile EVPN peers → lookup loopback IPs
    BGPNeighborASNs map[string]int    // peer loopback IP → peer UnderlayASN (for eBGP overlay)

    // From profile (optional)
    MAC string

    // SSH access (for Redis tunnel)
    SSHUser string
    SSHPass string
    SSHPort int // 0 means default (22)

    // newtlab runtime (written by newtlab, read by newtron)
    ConsolePort int

    // BGP AS number (required in all-eBGP design)
    UnderlayASN int
}

### OverridableSpecs

`OverridableSpecs` holds the 8 spec map types that participate in hierarchical resolution. Embedded by `NetworkSpecFile`, `ZoneSpec`, and `DeviceProfile`:

```go
type OverridableSpecs struct {
    PrefixLists   map[string][]string
    Filters       map[string]*FilterSpec
    QoSPolicies   map[string]*QoSPolicy
    QoSProfiles   map[string]*QoSProfile
    RoutePolicies map[string]*RoutePolicy
    IPVPNs        map[string]*IPVPNSpec
    MACVPNs       map[string]*MACVPNSpec
    Services      map[string]*ServiceSpec
}
```

### ResolvedSpecs (pkg/newtron/network)

`ResolvedSpecs` implements `node.SpecProvider` with pre-merged maps from all three hierarchy levels. Built at Node creation time in `buildResolvedSpecs()`:

```go
type ResolvedSpecs struct {
    merged  spec.OverridableSpecs  // pre-merged: network + zone + profile
    network *Network               // for GetPlatform() only
}
```

Resolution chain: `MergeMaps(network.X, zone.X, profile.X)` for each of the 8 map types. `GetPlatform()` delegates to `Network` (platforms don't participate in hierarchy).

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

The `pkg/newtron/spec/` types are a shared coupling surface — all three tools read from the same JSON files. This table shows which tool reads or writes each field group:

| Type | Field Group | newtron | newtlab | newtest |
|------|-------------|---------|-------|---------|
| `PlatformSpec` | Core (`hwsku`, `port_count`, `default_speed`) | Read | | |
| `PlatformSpec` | VM (`vm_image`, `vm_memory`, `vm_cpus`, `vm_nic_driver`, ...) | | Read | |
| `PlatformSpec` | `dataplane` | | | Read (skip verify-ping) |
| `PlatformSpec` | `vm_credentials` | | Read | |
| `DeviceProfile` | Core (`mgmt_ip`, `loopback_ip`, `platform`, `zone`) | Read | | |
| `DeviceProfile` | SSH (`ssh_user`, `ssh_pass`) | Read | | |
| `DeviceProfile` | `ssh_port`, `mgmt_ip` | Read | **Write** (profile patching) | |
| `DeviceProfile` | VM overrides (`vm_memory`, `vm_cpus`, `vm_image`) | | Read | |
| `TopologySpecFile` | Devices, links | Read (topology provisioner) | Read (VM deployment) | Read (scenario topology) |
| `TopologySpecFile` | `newtlab` config | | Read (VM defaults) | |
| `NetworkSpecFile` | Services, VPNs, filters, zones | Read | | |

**Key insight:** `DeviceProfile.ssh_port` and `DeviceProfile.mgmt_ip` are the only fields that newtlab **writes** — all other spec data flows from JSON files into the tools as read-only input. newtlab writes these into profile JSON during deployment (newtlab LLD §10), and newtron reads them in `sonic.Device.Connect()` (device LLD §5.1).

### 3.2 Object Hierarchy (`pkg/newtron/network/`, `pkg/newtron/network/node/`)

The system uses an object-oriented design with parent references, mirroring the original Perl architecture. This provides hierarchical access where child objects can access their parent's configuration:

```
Network (top-level)
    |
    +-- owns: NetworkSpecFile (services, filters, zones, etc.)
    +-- owns: PlatformSpecFile (hardware platform definitions)
    +-- owns: TopologySpecFile (topology specification, optional)
    +-- owns: Loader (spec file loading)
    |
    +-- creates: Node instances (in Network's context)
                     |
                     +-- has: parent reference to Network
                     +-- owns: DeviceProfile
                     +-- owns: ResolvedProfile
                     +-- delegates: sonic.Device (low-level Redis connection)
                     |
                     +-- creates: Interface instances (in Node's context)
                                      |
                                      +-- has: parent reference to Node
                                      +-- can access: Node -> Network -> Services, Filters, etc.
```

**Key Design Pattern: Parent References**

```go
// Network is the top-level object (pkg/newtron/network/network.go)
type Network struct {
    spec      *spec.NetworkSpecFile    // Services, filters, zones (declarative intent)
    platforms *spec.PlatformSpecFile   // Hardware platform definitions
    topology  *spec.TopologySpecFile   // Topology specification (optional)
    loader    *spec.Loader             // Spec file loading
    nodes     map[string]*Node         // Child objects
    mu        sync.RWMutex
}

// Node embeds a SpecProvider interface (pkg/newtron/network/node/node.go)
// SpecProvider is implemented by Network and provides access to network-level specs.
// This design avoids circular imports while giving Node direct access to GetService(),
// GetIPVPN(), GetMACVPN(), etc.
type Node struct {
    SpecProvider                     // Embedded interface — n.GetService() works directly
    name        string
    profile     *spec.DeviceProfile
    resolved    *spec.ResolvedProfile  // Resolved from inheritance
    interfaces  map[string]*Interface  // Child objects
    conn        *sonic.Device          // Low-level device connection
    configDB    *sonic.ConfigDB        // Cached config_db snapshot
    connected   bool
    locked      bool
    offline     bool                   // Abstract mode (no device connection)
    accumulated []sonic.Entry          // Entries accumulated in abstract mode
    mu          sync.RWMutex
}

// SpecProvider is the interface Node uses to access Network-level specs.
type SpecProvider interface {
    GetService(name string) (*spec.ServiceSpec, error)
    GetIPVPN(name string) (*spec.IPVPNSpec, error)
    GetMACVPN(name string) (*spec.MACVPNSpec, error)
    GetQoSPolicy(name string) (*spec.QoSPolicy, error)
    GetQoSProfile(name string) (*spec.QoSProfile, error)
    GetFilter(name string) (*spec.FilterSpec, error)
    GetPlatform(name string) (*spec.PlatformSpec, error)
    GetPrefixList(name string) ([]string, error)
    GetRoutePolicy(name string) (*spec.RoutePolicy, error)
    FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec)
}

// Lock acquires a distributed lock for this device via a Redis STATE_DB entry.
// Constructs holder identity from current user and hostname, acquires lock with
// default TTL (3600s), then refreshes CONFIG_DB cache to guarantee precondition
// checks see all changes made by prior lock holders.
func (n *Node) Lock() error

// Unlock releases the distributed lock.
func (n *Node) Unlock() error

// IsLocked returns true if this device is currently locked.
func (n *Node) IsLocked() bool

// IsConnected returns true if this device has an active connection.
func (n *Node) IsConnected() bool

// --- Node accessors and bridging ---

// Name returns the device name.
func (n *Node) Name() string { return n.name }

// ASNumber returns the device's underlay AS number from the resolved profile.
func (n *Node) ASNumber() int { return n.resolved.UnderlayASN }

// GetInterface returns an Interface by name from the node's interface map.
// Returns error if the interface name is not in the topology or CONFIG_DB.
func (n *Node) GetInterface(name string) (*Interface, error)

// InterfaceNames returns sorted names of all interfaces on this node.
func (n *Node) ListInterfaces() []string

// Connect establishes the connection to the device:
//   1. Creates sonic.Device with the resolved profile
//   2. Calls sonic.Device.Connect() (SSH tunnel + Redis clients)
//   3. Creates Interface objects from CONFIG_DB PORT and PORTCHANNEL tables
//   4. Sets n.configDB from the sonic.Device's CONFIG_DB snapshot
// Interface objects are lightweight (node + name only); all property
// accessors read on demand from the Node's ConfigDB/StateDB snapshots.
func (n *Node) Connect(ctx context.Context) error

// Disconnect closes the low-level connection and SSH tunnel.
func (n *Node) Disconnect() error

// Interface represents a network interface within the context of a Node.
//
// All state is read on demand from the Node's ConfigDB/StateDB snapshots
// (refreshed at Lock time), ensuring callers always see the latest data
// within a write episode. There are no cached fields — the struct is
// intentionally minimal.
type Interface struct {
    node *Node   // Parent reference — provides access to Node and Network
    name string  // Interface identity (e.g., "Ethernet0", "PortChannel100")
}

// --- Interface accessors (on-demand from ConfigDB/StateDB) ---

// Name returns the interface name (e.g. "Ethernet0").
func (i *Interface) Name() string { return i.name }

// Node returns the parent Node.
func (i *Interface) Node() *Node { return i.node }

// AdminStatus returns the administrative status from ConfigDB PORT/PORTCHANNEL.
func (i *Interface) AdminStatus() string

// OperStatus returns the operational status from StateDB PORT_TABLE/LAG_TABLE.
func (i *Interface) OperStatus() string

// Speed returns the interface speed. Prefers StateDB; falls back to ConfigDB.
func (i *Interface) Speed() string

// MTU returns the interface MTU. Prefers StateDB; falls back to ConfigDB.
func (i *Interface) MTU() int

// VRF returns the VRF this interface is bound to (from INTERFACE table).
func (i *Interface) VRF() string

// IPAddresses returns all IP addresses on this interface.
// Scans INTERFACE table for keys matching "name|ip/mask".
func (i *Interface) IPAddresses() []string

// HasService returns true if a service is currently bound to this interface.
func (i *Interface) HasService() bool { return i.ServiceName() != "" }

// ServiceName returns the bound service name from NEWTRON_SERVICE_BINDING.
func (i *Interface) ServiceName() string

// IngressACL/EgressACL return the bound ACL names.
// Prefer NEWTRON_SERVICE_BINDING; fall back to scanning ACL_TABLE.
func (i *Interface) IngressACL() string
func (i *Interface) EgressACL() string

// IsPortChannelMember returns true if this interface is a PortChannel member.
// Scans PORTCHANNEL_MEMBER table for keys containing this interface.
func (i *Interface) IsPortChannelMember() bool

// PortChannelParent returns the parent PortChannel name (if member).
func (i *Interface) PortChannelParent() string
```

**On-demand accessor pattern:**

Every accessor reads from the Node's ConfigDB or StateDB snapshot. There is no
`loadState()` function and no cached fields. This eliminates stale-field bugs
where an operation mutates CONFIG_DB but the Interface struct retains old values.

```go
// Example: VRF() reads from the live ConfigDB snapshot
func (i *Interface) VRF() string {
    configDB := i.node.ConfigDB()
    if configDB == nil { return "" }
    if intf, ok := configDB.Interface[i.name]; ok {
        return intf.VRFName
    }
    return ""
}
```

| Accessor | Source | Table/Key |
|----------|--------|-----------|
| `AdminStatus()` | ConfigDB | `PORT\|<name>` or `PORTCHANNEL\|<name>` |
| `OperStatus()` | StateDB | `PORT_TABLE\|<name>` or `LAG_TABLE\|<name>` |
| `Speed()` | StateDB → ConfigDB | `PORT_TABLE\|<name>` → `PORT\|<name>` |
| `MTU()` | StateDB → ConfigDB | `PORT_TABLE\|<name>` → `PORT\|<name>` |
| `VRF()` | ConfigDB | `INTERFACE\|<name>` → `vrf_name` |
| `IPAddresses()` | ConfigDB | `INTERFACE\|<name>\|<ip>` (key scan) |
| `ServiceName()` | ConfigDB | `NEWTRON_SERVICE_BINDING\|<name>` → `service_name` |
| `IngressACL()` | ConfigDB | `NEWTRON_SERVICE_BINDING` → `ACL_TABLE` fallback |
| `PortChannelParent()` | ConfigDB | `PORTCHANNEL_MEMBER` (key scan) |

**Accessing Network Specs from Interface:**

```go
// Interface accesses specs through the parent chain — no local caching
svc, _ := intf.Node().GetService("customer-l3")  // via SpecProvider
filter, _ := intf.Node().GetFilter(svc.IngressFilter)
asNum := intf.Node().ASNumber()  // from resolved profile
```

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

// --- Node connection ---

// ConnectNode retrieves a node by name, calls Connect() on it, and returns it.
// Convenience method used by CLI helpers — equivalent to GetNode + node.Connect.
func (n *Network) ConnectNode(ctx context.Context, name string) (*Node, error)

// --- Node and spec accessors ---

// ListNodes returns sorted names of all loaded nodes.
func (n *Network) ListNodes() []string

// GetNode returns a node.Node by name, or error if not found.
func (n *Network) GetNode(name string) (*Node, error)

// GetService returns a service spec by name from the network spec.
// Returns error if the service name does not exist.
func (n *Network) GetService(name string) (*spec.ServiceSpec, error)

// GetIPVPN returns an IP-VPN spec by name. Returns error if not found.
func (n *Network) GetIPVPN(name string) (*spec.IPVPNSpec, error)

// GetMACVPN returns a MAC-VPN spec by name. Returns error if not found.
func (n *Network) GetMACVPN(name string) (*spec.MACVPNSpec, error)

// GetFilter returns a filter spec by name. Returns error if not found.
func (n *Network) GetFilter(name string) (*spec.FilterSpec, error)

// GetQoSProfile returns a QoS profile by name. Returns error if not found.
func (n *Network) GetQoSProfile(name string) (*spec.QoSProfile, error)

// GetPlatform returns a platform spec by name, or error if not found.
func (n *Network) GetPlatform(name string) (*spec.PlatformSpec, error)
```

**Network Constructor (`pkg/newtron/network/network.go`):**

```go
// NewNetwork loads specs from the given directory and creates the Network.
//
// Initialization sequence:
//   1. Create Loader for specDir
//   2. Load network.json (required)
//   3. Load platforms.json (required)
//   4. Load profiles/*.json (one per device, required)
//   5. Load topology.json (optional — returns nil if absent)
//   6. Resolve profiles: for each device, merge profile + zone + global → ResolvedProfile
//   7. Validate topology (if loaded) — services, IPs, links
//   8. Create Node objects with resolved profiles, create Interface objects
//      from CONFIG_DB tables (populated later on Connect)
//
// Nodes are created but NOT connected — call Node.Connect() to
// establish SSH tunnels and load CONFIG_DB/STATE_DB.
func NewNetwork(specDir string) (*Network, error)
```

**Loader (`pkg/newtron/spec/loader.go`):**

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
func (l *Loader) GetPlatforms() *PlatformSpecFile
func (l *Loader) GetTopology() *TopologySpecFile // nil if topology.json absent
func (l *Loader) GetService(name string) (*ServiceSpec, error)
func (l *Loader) GetFilter(name string) (*FilterSpec, error)
func (l *Loader) GetPrefixList(name string) ([]string, error)

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

### 3.3 Low-Level Device Types (`pkg/newtron/device/sonic/device.go`)

The `sonic.Device` struct in `pkg/newtron/device/sonic` is the low-level representation that handles the actual Redis connection, while `node.Node` in `pkg/newtron/network/node` wraps it with the OO hierarchy.

```go
// Device represents a SONiC switch (low-level, imperative)
type Device struct {
    Name     string
    Profile  *spec.ResolvedProfile
    ConfigDB *ConfigDB                  // Snapshot of CONFIG_DB
    StateDB  *StateDB                   // Snapshot of STATE_DB

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
| `Lock()` / `Unlock()` | `mu.Lock()` | `locked` |

`Name`, `Profile` are set once at construction and never mutated — safe to read without lock. `ConfigDB` and `StateDB` snapshots are replaced (not mutated in place), so readers must hold `mu.RLock()` or coordinate with the caller. In practice, newtron operations are single-threaded per device (Lock→Apply→Unlock), so the mutex primarily guards against concurrent Connect/Disconnect.

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

```

### 3.4 ConfigDB Mapping (`pkg/newtron/device/sonic/configdb.go`)

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

    // Static Anycast Gateway
    SAG               map[string]map[string]string  `json:"SAG,omitempty"`
    SAGGlobal         map[string]map[string]string  `json:"SAG_GLOBAL,omitempty"`

    // BGP tables (CONFIG_DB-managed BGP)
    BGPNeighbor       map[string]BGPNeighborEntry   `json:"BGP_NEIGHBOR,omitempty"`
    BGPNeighborAF     map[string]BGPNeighborAFEntry `json:"BGP_NEIGHBOR_AF,omitempty"`
    BGPGlobals        map[string]BGPGlobalsEntry    `json:"BGP_GLOBALS,omitempty"`
    BGPGlobalsAF      map[string]BGPGlobalsAFEntry  `json:"BGP_GLOBALS_AF,omitempty"`
    BGPEVPNVNI        map[string]BGPEVPNVNIEntry    `json:"BGP_EVPN_VNI,omitempty"`

    // QoS tables
    Scheduler         map[string]SchedulerEntry     `json:"SCHEDULER,omitempty"`
    Queue             map[string]QueueEntry         `json:"QUEUE,omitempty"`
    WREDProfile       map[string]WREDProfileEntry   `json:"WRED_PROFILE,omitempty"`
    PortQoSMap        map[string]PortQoSMapEntry    `json:"PORT_QOS_MAP,omitempty"`
    DSCPToTCMap       map[string]map[string]string  `json:"DSCP_TO_TC_MAP,omitempty"`
    TCToQueueMap      map[string]map[string]string  `json:"TC_TO_QUEUE_MAP,omitempty"`

    // Extended BGP tables (frrcfgd — FRR management framework)
    BGPPeerGroup          map[string]BGPPeerGroupEntry         `json:"BGP_PEER_GROUP,omitempty"`
    BGPPeerGroupAF        map[string]BGPPeerGroupAFEntry       `json:"BGP_PEER_GROUP_AF,omitempty"`
    BGPGlobalsAFNet    map[string]BGPGlobalsAFNetEntry  `json:"BGP_GLOBALS_AF_NETWORK,omitempty"`
    BGPGlobalsAFAgg    map[string]BGPGlobalsAFAggEntry  `json:"BGP_GLOBALS_AF_AGGREGATE_ADDR,omitempty"`
    RouteRedistribute     map[string]RouteRedistributeEntry    `json:"ROUTE_REDISTRIBUTE,omitempty"`
    RouteMap              map[string]RouteMapEntry             `json:"ROUTE_MAP,omitempty"`
    PrefixSet             map[string]PrefixSetEntry            `json:"PREFIX_SET,omitempty"`
    CommunitySet          map[string]CommunitySetEntry         `json:"COMMUNITY_SET,omitempty"`
    ASPathSet             map[string]ASPathSetEntry             `json:"AS_PATH_SET,omitempty"`

    // Newtron custom table (NOT standard SONiC)
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
    // frrcfgd extensions:
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
// Fields: peer_group, ebgp_multihop, password
type BGPNeighborEntry struct {
    LocalAddr     string `json:"local_addr,omitempty"`
    Name          string `json:"name,omitempty"`
    ASN           string `json:"asn,omitempty"`
    HoldTime      string `json:"holdtime,omitempty"`
    KeepaliveTime string `json:"keepalive,omitempty"`
    AdminStatus   string `json:"admin_status,omitempty"`
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
    ServiceName     string `json:"service_name"`
    IPAddress       string `json:"ip_address,omitempty"`
    VRFName         string `json:"vrf_name,omitempty"`
    IPVPN           string `json:"ipvpn,omitempty"`
    MACVPN          string `json:"macvpn,omitempty"`
    IngressACL      string `json:"ingress_acl,omitempty"`
    EgressACL       string `json:"egress_acl,omitempty"`
    BGPNeighbor     string `json:"bgp_neighbor,omitempty"`      // BGP peer IP created by service
    QoSPolicy       string `json:"qos_policy,omitempty"`        // QoS policy name (for device-wide cleanup)
    VlanId          string `json:"vlan_id,omitempty"`           // VLAN ID used (for cleanup without macvpn)
    RedistributeVRF string `json:"redistribute_vrf,omitempty"`  // VRF where redistribution was overridden
    AppliedAt       string `json:"applied_at,omitempty"`
    AppliedBy       string `json:"applied_by,omitempty"`
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

// VLANMemberEntry represents a VLAN member interface in CONFIG_DB.
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

### 3.5 ConfigDB Client (`pkg/newtron/device/sonic/configdb.go`)

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
//   1. Cursor-based SCAN * in DB 4 (non-blocking, unlike KEYS *)
//   2. For each key, split on first "|" → table name + entry key
//   3. HGETALL per key → field map
//   4. Dispatch into table-driven parsers (configdb_parsers.go) which populate
//      the corresponding typed map in ConfigDB (e.g., "PORT" → configDB.Port[key] = PortEntry{...})
//   5. Return the populated ConfigDB
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

**Pipeline methods** (`pkg/newtron/device/sonic/pipeline.go`):

```go
// Entry is a single CONFIG_DB entry: table + key + fields.
// Used by config generators, composite builders, and pipeline delivery.
// This is the unified entry type — replaces the former CompositeEntry and TableChange types.
type Entry struct {
    Table  string
    Key    string
    Fields map[string]string
}

// PipelineSet writes multiple entries atomically via Redis MULTI/EXEC pipeline.
// Each Entry with non-nil Fields is written as HSET; nil Fields means DEL.
// Empty Fields (len 0) writes the NULL:NULL sentinel (SONiC convention).
func (c *ConfigDBClient) PipelineSet(changes []Entry) error

// ReplaceAll merges composite entries on top of existing CONFIG_DB, removing
// only stale keys not present in the composite. Factory defaults (mac, platform,
// hwsku from init_cfg.json; FEATURE, CRM, FLEX_COUNTER_TABLE, etc.) are preserved
// because we never delete keys that appear in our composite — HSet merges our
// fields on top of any surviving factory fields.
//
// Platform-managed tables (PORT) are merge-only — their keys are never deleted
// even if absent from the composite, since port config comes from port_config.ini.
//
// Algorithm:
//   1. Collect tables being replaced (excluding platform-managed merge-only tables)
//   2. For each affected table, KEYS <table>|* to find existing keys
//   3. Delete only stale keys (present in DB but absent from composite) via pipeline
//   4. Write all composite entries via PipelineSet (HSet merges fields)
func (c *ConfigDBClient) ReplaceAll(changes []Entry) error
```

### 3.6 ChangeSet Types (`pkg/newtron/network/node/changeset.go`)

Operations return ChangeSets that can be previewed or applied:

```go
// ChangeType represents the type of configuration change
type ChangeType string

const (
    ChangeAdd    ChangeType = "add"
    ChangeModify ChangeType = "modify"
    ChangeDelete ChangeType = "delete"
)

// Change is a type alias for sonic.ConfigChange. All external references
// to node.Change (audit/event.go, test helpers) resolve to ConfigChange.
type Change = sonic.ConfigChange

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

// Typed add methods — one per change type:
func (cs *ChangeSet) Add(table, key string, fields map[string]string)     // ChangeAdd
func (cs *ChangeSet) Update(table, key string, fields map[string]string)  // ChangeModify
func (cs *ChangeSet) Delete(table, key string)                            // ChangeDelete

// Batch bridges for config function output ([]sonic.Entry):
func (cs *ChangeSet) Adds(entries []sonic.Entry)                          // batch ChangeAdd
func (cs *ChangeSet) Updates(entries []sonic.Entry)                       // batch ChangeModify
func (cs *ChangeSet) Deletes(entries []sonic.Entry)                       // batch ChangeDelete

func (cs *ChangeSet) Merge(other *ChangeSet)                              // append other's changes
func (cs *ChangeSet) IsEmpty() bool
func (cs *ChangeSet) Preview() string // human-readable diff format: "+ TABLE|key field=value" / "- TABLE|key" / "~ TABLE|key field: old→new"

// Apply writes all changes in the ChangeSet to CONFIG_DB sequentially.
// Each change is written individually via ConfigDBClient.Set/Delete.
//
// Partial failure: If a write fails at index N, Apply sets cs.AppliedCount = N
// (changes 0..N-1 succeeded) and returns the error. Changes already written
// are NOT rolled back.
//
// On full success, cs.AppliedCount = len(cs.Changes).
func (cs *ChangeSet) Apply(n *Node) error

// Verify re-reads CONFIG_DB through a fresh client and confirms every entry
// in the ChangeSet was applied correctly. Not called by the standard CLI flow,
// but available for programmatic verification (e.g., newtest).
func (cs *ChangeSet) Verify(n *Node) error

// buildChangeSet wraps config function output into a ChangeSet.
// Bridges pure config functions (return []sonic.Entry) with the ChangeSet
// world used by primitives and composites.
func buildChangeSet(deviceName, operation string, config []sonic.Entry, changeType sonic.ChangeType) *ChangeSet

// op is a generic helper for simple CRUD operations. It runs precondition
// checks, calls the entry generator, and wraps the result in a ChangeSet.
// Use this for operations whose entire body is: preconditions → generate entries → done.
// Skip it for complex operations that need custom logic between precondition and return
// (e.g., ApplyService, RemoveService, SetupEVPN).
func (n *Node) op(name, resource string, changeType sonic.ChangeType,
    checks func(*PreconditionChecker), gen func() []sonic.Entry) (*ChangeSet, error)
```

**Three-layer pattern:** Operations follow a consistent layering:

1. **Config functions** — pure functions in each `*_ops.go` file that take `*sonic.ConfigDB` + identity parameters and return `[]sonic.Entry`. No side effects, no precondition checks, no ChangeSet construction. Example: `vlanDeleteConfig(configDB, vlanID) []sonic.Entry`.

2. **`op()` helper** — wraps the precondition → generate → ChangeSet pattern into a single call for simple CRUD operations. About 19 operations use it.

3. **Direct ChangeSet construction** — complex operations (ApplyService, RemoveService, SetupEVPN) that need custom logic between precondition checks and return build their ChangeSets directly, calling config functions from owning `*_ops.go` files.

### 3.6A Verification Types (`pkg/newtron/device/sonic/types.go`)

These types live in `pkg/newtron/device/sonic/types.go`. `AppDBClient.GetRoute()` returns `*RouteEntry` — placing them in `pkg/newtron/network/node` would create an import cycle. The `pkg/newtron/network/node` layer re-exports these types for convenience.

These types support the verification architecture: newtron observes single-device state and returns structured data; orchestrators (newtest) assert cross-device correctness.

**Consumers:** newtest's step executors are the primary consumers — `verifyProvisioningExecutor` reads `VerificationResult`, `verifyRouteExecutor` reads `RouteEntry`. See newtest LLD §7.5, §7.6.

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

### 3.7 Platform Config (`pkg/newtron/device/sonic/platform.go`)

Newtron reads the device's SONiC `platform.json` for port validation. The platform config is fetched via SSH and cached on `sonic.Device`.

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

The parsed result is cached as `sonic.Device.PlatformConfig` and is accessed via the sonic.Device layer.

### 3.8 Composite Types (`pkg/newtron/network/node/composite.go`)

Composite mode generates a composite CONFIG_DB offline and delivers it atomically.

```go
// CompositeMode defines how the composite is delivered to the device
type CompositeMode string

const (
    CompositeOverwrite CompositeMode = "overwrite" // Merge on top of CONFIG_DB, removing stale keys
    CompositeMerge     CompositeMode = "merge"     // Add entries to existing CONFIG_DB
)

// CompositeConfig represents a composite CONFIG_DB configuration
type CompositeConfig struct {
    Tables   map[string]map[string]map[string]string `json:"tables"`   // table -> key -> field -> value
    Metadata CompositeMetadata                       `json:"metadata"`
}

// CompositeMetadata contains provenance information for a composite config.
type CompositeMetadata struct {
    Timestamp   time.Time     `json:"timestamp"`
    NetworkName string        `json:"network_name,omitempty"`
    DeviceName  string        `json:"device_name,omitempty"`
    Mode        CompositeMode `json:"mode"`
    GeneratedBy string        `json:"generated_by,omitempty"`
    Description string        `json:"description,omitempty"`
}

// CompositeBuilder constructs composite configs offline using a builder pattern.
// All Add* methods accumulate entries without requiring a device connection.
type CompositeBuilder struct {
    tables   map[string]map[string]map[string]string
    metadata CompositeMetadata
}

func NewCompositeBuilder(deviceName string, mode CompositeMode) *CompositeBuilder

// AddEntries accepts output from config functions ([]sonic.Entry) — the primary way
// to add entries. Callers call config functions from owning *_ops.go files and pass
// the result here. No typed helpers (AddBGPGlobals, AddPeerGroup, etc.) — the config
// functions are the API.
func (cb *CompositeBuilder) AddEntries(entries []sonic.Entry) *CompositeBuilder
func (cb *CompositeBuilder) AddEntry(table, key string, fields map[string]string) *CompositeBuilder
func (cb *CompositeBuilder) SetDescription(desc string) *CompositeBuilder
func (cb *CompositeBuilder) SetGeneratedBy(by string) *CompositeBuilder
func (cb *CompositeBuilder) Build() *CompositeConfig

// CompositeDeliveryResult reports the outcome of delivering a composite config.
type CompositeDeliveryResult struct {
    Applied int           `json:"applied"` // entries successfully written
    Skipped int           `json:"skipped"` // entries skipped (already exist in merge)
    Failed  int           `json:"failed"`  // entries that failed to write
    Error   error         `json:"error,omitempty"`
    Mode    CompositeMode `json:"mode"`
}

// ApplyServiceOpts holds parameters for applying a service to an interface.
// Used by both Interface.ApplyService() and CompositeBuilder.AddService().
//
// Interface.ApplyService() returns a ChangeSet for preview; the caller applies
// via cs.Apply(). CompositeBuilder operates offline, collecting entries into a
// CompositeConfig without connecting to a device. Both use the same opts struct.
type ApplyServiceOpts struct {
    IPAddress string            // IP address for routed/IRB services (e.g., "10.1.1.1/30")
    VLAN      int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
    PeerAS    int               // BGP peer AS number (for services with routing.peer_as="request")
    Params    map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
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

All operations that return `*ChangeSet` compute the changes without writing. The caller previews the ChangeSet, then calls `cs.Apply(n)` to execute. Lock acquisition and unlock are the caller's responsibility.

**CLI execution pattern (via `withDeviceWrite` helper):**

```
Lock (+ CONFIG_DB refresh) → fn() returns ChangeSet → preview → if -x: Apply → Verify → optional SaveConfig → Unlock
```

The CLI calls `cs.Verify()` after every `Apply`. Verify opens a **fresh** Redis connection (independent of the write connection), re-reads every entry in the ChangeSet, and compares field-by-field. If verification fails (entries missing or mismatched), config is **not** saved — the operator is told to `config reload` to restore the last known-good state. This catches transient write failures (SSH tunnel instability, connection drops) that `Apply`'s per-entry error checking cannot detect.

The lock is scoped to a single operation — not held across multiple operations or for the duration of a session. This ensures:
1. Minimal lock hold time — only during the critical mutation window
2. No stale locks from long-running sessions
3. Clear failure semantics — if lock acquisition fails, the operation fails immediately

```go
// Pattern: operation returns ChangeSet, caller applies it.
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    n := i.Node()
    cs := NewChangeSet(n.Name(), "interface.apply-service")
    // ... build ChangeSet entries ...
    return cs, nil
}

// CLI caller pattern (withDeviceWrite):
//   dev.Lock()           // acquires distributed lock + refreshes CONFIG_DB
//   cs, err := intf.ApplyService(ctx, "customer-l3", opts)
//   fmt.Print(cs.Preview())
//   if executeMode {
//       cs.Apply(dev)    // writes to CONFIG_DB
//       dev.SaveConfig() // unless --no-save
//   }
//   dev.Unlock()
```

**Disconnect safety net:** `Node.Disconnect()` releases the lock if still held (e.g., after a panic during operation execution). This is a safety measure — normal operation always releases the lock within the operation method.

### 5.1 Interface Operations (`pkg/newtron/network/node/interface_ops.go`)

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
to avoid deleting shared resources still referenced by other interfaces:

```go
func (i *Interface) RemoveService(ctx context.Context) (*ChangeSet, error) {
    n := i.node
    // ... precondition checks ...

    binding := i.binding() // reads NEWTRON_SERVICE_BINDING
    svc, _ := n.GetService(binding.ServiceName)

    cs := NewChangeSet(n.Name(), "interface.remove-service")
    dc := NewDependencyChecker(n, i.name)

    // 1. Remove service binding (always safe — owned by this interface)
    cs.Delete("NEWTRON_SERVICE_BINDING", i.name)

    // 2. Remove IP binding entries (INTERFACE|<name>|<ip>)
    for key := range n.ConfigDB().Interface {
        if strings.HasPrefix(key, i.name+"|") {
            cs.Delete("INTERFACE", key)
        }
    }

    // 3. Remove BGP neighbors for this interface
    //    Delete BGP_NEIGHBOR_AF entries first (child before parent)
    //    DependencyChecker guards shared neighbors

    // 4. Remove ACLs if no other interfaces reference them
    //    dc.CanDeleteACL() → full delete (rules + table) vs port-list unbinding

    // 5. Remove VRF binding from INTERFACE base entry
    cs.Delete("INTERFACE", i.name)

    // 6. Remove VRF if no other interfaces use it
    //    dc.CanDeleteVRF() → cascade delete VRF + VXLAN_TUNNEL_MAP

    // 7. L2/IRB-specific: remove VLAN member, VLAN if empty
    //    dc.CanDeleteVLAN() → cascade delete VLAN + SVI + VNI map + ARP suppression

    // 8. Remove QoS bindings for this interface

    // 9. Remove route-policy artifacts (ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)

    return cs, nil
}
```

// RefreshService removes and reapplies the service with current definition.
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
// ACLs are shared - adds this interface to the ACL's binding list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error)

// UnbindACL removes an ACL binding from this interface.
// If last user, deletes the ACL; otherwise just removes from binding list.
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
    n := i.Node()
    ipAddr := opts.IPAddr

    if !n.IsConnected() {
        return nil, util.ErrNotConnected
    }

    // Get service definition from Network (via parent chain)
    svc, err := i.Node().GetService(serviceName)
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

    cs := NewChangeSet(n.Name(), "interface.apply-service")

    // ====================================================================
    // L3 service translation (ServiceType == "l3")
    // ====================================================================
    if svc.ServiceType == ServiceTypeL3 || svc.ServiceType == ServiceTypeIRB {
        vrfName := util.DeriveVRFName(svc.VRFType, serviceName, i.name)

        if svc.IPVPN != "" {
            // EVPN path: VRF with L3VNI, VXLAN tunnel map, BGP globals
            ipvpnDef, err := i.Node().GetIPVPN(svc.IPVPN)
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
        macvpnDef, err := i.Node().GetMACVPN(svc.MACVPN)
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
        macvpnDef, err := i.Node().GetMACVPN(svc.MACVPN)
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
        filterSpec, err := i.Node().GetFilter(svc.IngressFilter)
        if err != nil {
            return nil, fmt.Errorf("ingress filter %q: %w", svc.IngressFilter, err)
        }
        i.generateACLEntries(cs, aclName, filterSpec, "ingress")
    }
    if svc.EgressFilter != "" {
        aclName := util.DeriveACLName(serviceName, "out")
        filterSpec, err := i.Node().GetFilter(svc.EgressFilter)
        if err != nil {
            return nil, fmt.Errorf("egress filter %q: %w", svc.EgressFilter, err)
        }
        i.generateACLEntries(cs, aclName, filterSpec, "egress")
    }

    // ====================================================================
    // QoS binding (all service types)
    // ====================================================================
    if svc.QoSProfile != "" {
        qos := i.Node().GetQoSProfile(svc.QoSProfile)
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
        neighborIP, _ := util.DeriveNeighborIP(i.IPAddress())
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
// If the ACL already exists in CONFIG_DB, appends this interface to the binding list
// rather than creating a new ACL_TABLE entry.
func (i *Interface) generateACLEntries(cs *ChangeSet, aclName string, filter *spec.FilterSpec, stage string) {
    existing := i.Node().ConfigDB().ACLTable[aclName]
    if existing.Type != "" {
        // ACL exists — add this interface to binding list
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

**DependencyChecker** (`pkg/newtron/network/node/interface_ops.go`):

Used by `RemoveService` to safely clean up shared resources. Reference counting is done via CONFIG_DB scan — no separate tracking database.

```go
// DependencyChecker determines whether shared resources (ACLs, VLANs, services,
// IP-VPNs) can be safely deleted when a service is removed from an interface.
type DependencyChecker struct {
    node             *Node
    excludeInterface string
}

// NewDependencyChecker creates a dependency checker for the given interface.
func NewDependencyChecker(d *Node, excludeInterface string) *DependencyChecker {
    return &DependencyChecker{
        node:             d,
        excludeInterface: excludeInterface,
    }
}

// IsLastACLUser returns true if the given ACL has no remaining interface bindings
// after excluding the interface being removed.
func (dc *DependencyChecker) IsLastACLUser(aclName string) bool

// GetACLRemainingInterfaces returns a comma-separated list of interfaces
// still bound to the given ACL (excluding the interface being removed).
func (dc *DependencyChecker) GetACLRemainingInterfaces(aclName string) string

// IsLastVLANMember returns true if the interface being removed is the last
// member of the given VLAN.
func (dc *DependencyChecker) IsLastVLANMember(vlanID int) bool

// IsLastServiceUser returns true if no other interfaces use the given service
// after excluding the interface being removed.
func (dc *DependencyChecker) IsLastServiceUser(serviceName string) bool

// IsLastIPVPNUser returns true if no other interfaces use the given IP-VPN
// after excluding the interface being removed.
func (dc *DependencyChecker) IsLastIPVPNUser(ipvpnName string) bool
```

### 5.2 Node Operations (various `*_ops.go` files in `pkg/newtron/network/node/`)

Node operations are methods on the `Node` type. All operations return a `*ChangeSet` for preview/execution.

**Complete Method List:**

```go
// ============================================================================
// VLAN Management
// ============================================================================

func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANConfig) (*ChangeSet, error)
func (n *Node) DeleteVLAN(ctx context.Context, vlanID int) (*ChangeSet, error)
func (n *Node) AddVLANMember(ctx context.Context, vlanID int, interfaceName string, tagged bool) (*ChangeSet, error)

// RemoveVLANMember deletes a VLAN_MEMBER entry for the given VLAN and interface.
func (n *Node) RemoveVLANMember(ctx context.Context, vlanID int, interfaceName string) (*ChangeSet, error)

// ============================================================================
// PortChannel (LAG) Management
// ============================================================================

func (n *Node) CreatePortChannel(ctx context.Context, name string, opts PortChannelConfig) (*ChangeSet, error)
func (n *Node) DeletePortChannel(ctx context.Context, name string) (*ChangeSet, error)
func (n *Node) AddPortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error)
func (n *Node) RemovePortChannelMember(ctx context.Context, pcName, member string) (*ChangeSet, error)

// ============================================================================
// VRF Management
// ============================================================================

func (n *Node) CreateVRF(ctx context.Context, name string, opts VRFConfig) (*ChangeSet, error)
func (n *Node) DeleteVRF(ctx context.Context, name string) (*ChangeSet, error)

// AddVRFInterface binds an interface to a VRF by setting vrf_name on the
// INTERFACE table entry. Fails if the interface is already in a different VRF.
func (n *Node) AddVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error)

// RemoveVRFInterface removes an interface from a VRF by clearing vrf_name
// from the INTERFACE table entry.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrfName, intfName string) (*ChangeSet, error)

// BindIPVPN maps a VRF to an IP-VPN by setting the L3VNI on the VRF entry
// and creating a VXLAN_TUNNEL_MAP entry for the VNI-to-VRF mapping.
func (n *Node) BindIPVPN(ctx context.Context, vrfName string, ipvpnDef *spec.IPVPNSpec) (*ChangeSet, error)

// UnbindIPVPN removes the L3VNI mapping from a VRF: clears the VNI from
// the VRF entry and deletes the corresponding VXLAN_TUNNEL_MAP entry.
func (n *Node) UnbindIPVPN(ctx context.Context, vrfName string) (*ChangeSet, error)

// AddStaticRoute creates a static route in the STATIC_ROUTE table for
// the given VRF, prefix, and next-hop.
func (n *Node) AddStaticRoute(ctx context.Context, vrfName, prefix, nextHop string, metric int) (*ChangeSet, error)

// RemoveStaticRoute deletes a static route from the STATIC_ROUTE table.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrfName, prefix string) (*ChangeSet, error)

// ============================================================================
// ACL Management
// ============================================================================

func (n *Node) CreateACLTable(ctx context.Context, name string, opts ACLTableConfig) (*ChangeSet, error)
func (n *Node) DeleteACLTable(ctx context.Context, name string) (*ChangeSet, error)
func (n *Node) AddACLRule(ctx context.Context, tableName, ruleName string, opts ACLRuleConfig) (*ChangeSet, error)
func (n *Node) UnbindACLFromInterface(ctx context.Context, aclName, interfaceName string) (*ChangeSet, error)

// ============================================================================
// EVPN/VTEP Management
// ============================================================================

// SetupEVPN is an idempotent composite that configures the full EVPN stack:
// VXLAN_TUNNEL (VTEP), VXLAN_EVPN_NVO, and BGP EVPN address family.
// sourceIP defaults to the device's loopback IP if empty.
// Creates entries only if they don't already exist (idempotent).
func (n *Node) SetupEVPN(ctx context.Context, sourceIP string) (*ChangeSet, error)

// AddLoopbackBGPNeighbor adds an indirect BGP neighbor using loopback as update-source.
// This is used for iBGP or multi-hop eBGP sessions.
func (n *Node) AddLoopbackBGPNeighbor(ctx context.Context, neighborIP string, asn int, description string, evpn bool) (*ChangeSet, error)

// RemoveBGPNeighbor removes a BGP neighbor from the device.
// This works for both direct (interface-level) and indirect (loopback-level) neighbors.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error)

// ============================================================================
// Health Checks and Maintenance
// ============================================================================

// RunHealthChecks runs health checks on the device.
// checkType is a filter: "bgp", "interfaces", "evpn", "lag", "vxlan", or "all".
func (n *Node) RunHealthChecks(ctx context.Context, checkType string) ([]HealthCheckResult, error)

// ConfigureLoopback configures the loopback interface on the device.
func (n *Node) ConfigureLoopback(ctx context.Context) (*ChangeSet, error)

// Cleanup identifies and removes orphaned configurations.
// cleanupType can be: "acl", "vrf", "vni", or "" for all.
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error)

// ============================================================================
// QoS Management
// ============================================================================

// ApplyQoS configures per-interface QoS: creates SCHEDULER, DSCP_TO_TC_MAP,
// TC_TO_QUEUE_MAP, QUEUE, and PORT_QOS_MAP entries from a QoS policy.
func (n *Node) ApplyQoS(ctx context.Context, intfName, policyName string, policy *spec.QoSPolicy) (*ChangeSet, error)

// RemoveQoS removes QoS configuration from an interface: deletes QUEUE
// and PORT_QOS_MAP entries for the interface.
func (n *Node) RemoveQoS(ctx context.Context, intfName string) (*ChangeSet, error)

// ============================================================================
// Query Methods (no ChangeSet returned)
// ============================================================================

// ListVLANs returns VLAN IDs present in CONFIG_DB.
func (n *Node) ListVLANs() []int

// ListVRFs returns VRF names present in CONFIG_DB.
func (n *Node) ListVRFs() []string

// ListPortChannels returns PortChannel names present in CONFIG_DB.
func (n *Node) ListPortChannels() []string

// ListInterfaces returns interface names (Ethernet*, PortChannel*, Loopback*).
func (n *Node) ListInterfaces() []string

// GetOrphanedACLs returns ACL tables not bound to any interface.
func (n *Node) GetOrphanedACLs() []string

// VTEPSourceIP returns the VTEP source IP (loopback address).
func (n *Node) VTEPSourceIP() string

// ============================================================================
// Verification Methods
// ============================================================================

// Verify (ChangeSet method) re-reads CONFIG_DB through a fresh connection and
// confirms every entry in the ChangeSet was applied. The actual verification is
// performed by calling n.verifyConfigChanges() and storing the result
// in cs.Verification.
//
// Pattern: cs.Verify(n) — method on ChangeSet, not Node.
func (cs *ChangeSet) Verify(n *Node) error

// GetRoute reads a route from APP_DB (Redis DB 0) via the AppDBClient.
// Parses the comma-separated nexthop/ifname fields into []NextHop.
// Returns nil RouteEntry (not error) if the prefix is not present.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

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
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)

// ============================================================================
// Spec Persistence (methods on Network, not Node)
// ============================================================================

// These methods create/delete definitions in network.json. Used by CLI
// spec-authoring commands (evpn ipvpn create, qos create, filter create,
// service create, etc.). Each method updates the in-memory spec, then
// calls loader.SaveNetwork() which writes atomically via temp+rename.

// SaveIPVPN creates or updates an IP-VPN definition in network.json.
func (n *Network) SaveIPVPN(name string, def *spec.IPVPNSpec) error

// DeleteIPVPN removes an IP-VPN definition from network.json.
func (n *Network) DeleteIPVPN(name string) error

// SaveMACVPN creates or updates a MAC-VPN definition in network.json.
func (n *Network) SaveMACVPN(name string, def *spec.MACVPNSpec) error

// DeleteMACVPN removes a MAC-VPN definition from network.json.
func (n *Network) DeleteMACVPN(name string) error

// SaveQoSPolicy creates or updates a QoS policy definition in network.json.
func (n *Network) SaveQoSPolicy(name string, def *spec.QoSPolicy) error

// DeleteQoSPolicy removes a QoS policy definition from network.json.
func (n *Network) DeleteQoSPolicy(name string) error

// SaveFilter creates or updates a filter spec definition in network.json.
func (n *Network) SaveFilter(name string, def *spec.FilterSpec) error

// DeleteFilter removes a filter spec definition from network.json.
func (n *Network) DeleteFilter(name string) error

// SaveService creates or updates a service definition in network.json.
func (n *Network) SaveService(name string, def *spec.ServiceSpec) error

// DeleteService removes a service definition from network.json.
// Fails if the service is currently applied to any interface (checks
// all devices' NEWTRON_SERVICE_BINDING tables).
func (n *Network) DeleteService(name string) error

// ============================================================================
// BGP Management
// ============================================================================

// BGP operations are available at two levels:
// - Node-level: AddLoopbackBGPNeighbor, RemoveBGPNeighbor (indirect/iBGP using loopback)
// - Interface-level: AddBGPNeighbor (direct/eBGP using link IP)
//
// See evpn_ops.go for AddLoopbackBGPNeighbor implementation.
// See interface_bgp_ops.go for Interface.AddBGPNeighbor implementation.

// LoadPlatformConfig fetches and caches platform.json from the device via SSH.
func (n *Node) LoadPlatformConfig(ctx context.Context) error

// GeneratePlatformSpec creates a spec.PlatformSpec from the device's platform.json.
// Used for priming the spec system on first connect to new hardware.
func (n *Node) GeneratePlatformSpec(ctx context.Context) (*spec.PlatformSpec, error)

// ============================================================================
// Composite Delivery
// ============================================================================

// DeliverComposite delivers a composite config to the device and generates
// a ChangeSet for verification. The ChangeSet generation differs by mode:
//
// Overwrite mode:
//   1. Snapshot current CONFIG_DB (pre-state)
//   2. ReplaceAll (flush + pipeline write)
//   3. For each entry in composite: ChangeSet entry = ChangeAdd
//   4. For each entry in pre-snapshot missing from composite: ChangeSet entry = ChangeDelete
//
// Merge mode:
//   1. Snapshot current CONFIG_DB (pre-state)
//   2. Diff: only entries that differ from pre-state are included
//   3. Entries with same table|key but different field values = conflict error
//   4. Entries with same table|key and same values = skipped (no-op)
//   5. PipelineSet only the differing entries
//
func (n *Node) DeliverComposite(composite *CompositeConfig, mode CompositeMode) (*CompositeDeliveryResult, error)

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
    Type        string // ipv4, ipv6 (translated to L3, L3V6 for CONFIG_DB)
    Stage       string // ingress, egress
    Description string
    Ports       string // Comma-separated interface names (maps to CONFIG_DB ACL_TABLE.ports)
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
    ClusterID    string   // from profile EVPN config; defaults to loopback if empty
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

### 5.4 Topology Provisioning Operations (`pkg/newtron/network/topology.go`)

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

// GenerateServiceEntries produces CONFIG_DB entries (as sonic.Entry values)
// for applying a service to an interface.  Delegates to config functions
// from the owning *_ops.go files — does not construct entries inline.
func GenerateServiceEntries(sp SpecProvider, p ServiceEntryParams) ([]sonic.Entry, error)

// generateQoSDeviceEntries produces device-wide CONFIG_DB entries for a QoS policy:
// DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER (per queue), WRED_PROFILE (if ECN).
func generateQoSDeviceEntries(policyName string, policy *spec.QoSPolicy) []sonic.Entry

// generateQoSInterfaceEntries produces per-interface CONFIG_DB entries:
// PORT_QOS_MAP (bracket-refs to maps) and QUEUE entries (bracket-refs to schedulers).
func generateQoSInterfaceEntries(policyName string, policy *spec.QoSPolicy, interfaceName string) []sonic.Entry

// resolveServiceQoSPolicy checks QoSPolicy first, falls back to legacy QoSProfile.
func resolveServiceQoSPolicy(n *Network, svc *spec.ServiceSpec) (string, *spec.QoSPolicy)
```

## 6. Precondition Checker

### 6.1 Implementation (`pkg/newtron/network/node/precondition.go`)

```go
// PreconditionChecker validates operation preconditions
type PreconditionChecker struct {
    node      *Node
    operation string
    resource  string
    errors    []error
}

func NewPreconditionChecker(d *Node, operation, resource string) *PreconditionChecker

func (p *PreconditionChecker) RequireConnected() *PreconditionChecker
func (p *PreconditionChecker) RequireLocked() *PreconditionChecker
func (p *PreconditionChecker) RequireInterfaceExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireInterfaceNotPortChannelMember(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireVLANExists(id int) *PreconditionChecker
func (p *PreconditionChecker) RequireVLANNotExists(id int) *PreconditionChecker
func (p *PreconditionChecker) RequireVRFExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireVRFNotExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequirePortChannelExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequirePortChannelNotExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireVTEPConfigured() *PreconditionChecker
func (p *PreconditionChecker) RequireACLTableExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireACLTableNotExists(name string) *PreconditionChecker
func (p *PreconditionChecker) Check(condition bool, precondition, details string) *PreconditionChecker

func (p *PreconditionChecker) Result() error {
    if len(p.errors) == 0 {
        return nil
    }
    if len(p.errors) == 1 {
        return p.errors[0]
    }
    // Combine errors
    msgs := make([]string, len(p.errors))
    for i, e := range p.errors {
        msgs[i] = e.Error()
    }
    return util.NewValidationError(msgs...)
}

// RequireLocked is part of PreconditionChecker, not sonic.Device.
// The Node.precondition() method creates a PreconditionChecker which
// calls RequireConnected and RequireLocked to verify device state.
func (p *PreconditionChecker) RequireLocked() *PreconditionChecker {
    if !p.node.locked {
        p.errors = append(p.errors, util.NewPreconditionError(
            p.operation, p.resource, "device must be locked", "use Lock() first"))
    }
    return p
}
```

## 7. Value Derivation

### 7.1 Auto-Derived Values (`pkg/util/derive.go`)

```go
// DerivedValues contains auto-computed values
type DerivedValues struct {
    NeighborIP  string
    VRFName     string
    Description string
    ACLPrefix   string
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

### 7.3 Specification Resolution (Network internal method)

```go
// ResolveProfile applies inheritance: profile > zone > global
// Zone is read directly from profile.Zone field
func ResolveProfile(
    deviceName string,
    profile *DeviceProfile,
    network *NetworkSpecFile,
    loadProfile func(string) (*DeviceProfile, error),
) *ResolvedProfile {
    // Get zone from profile
    zoneName := profile.Zone
    zone := network.Zones[zoneName]

    r := &ResolvedProfile{
        DeviceName: deviceName,
        MgmtIP:     profile.MgmtIP,
        LoopbackIP: profile.LoopbackIP,
        Zone:     zoneName,
        Platform:   profile.Platform,
        SSHUser:    profile.SSHUser,
        SSHPass:    profile.SSHPass,
    }

    // Underlay ASN from profile
    r.UnderlayASN = profile.UnderlayASN

    // Router ID and VTEP from loopback
    r.RouterID = profile.LoopbackIP
    r.VTEPSourceIP = profile.LoopbackIP

    // EVPN configuration: route reflector status and cluster ID
    if profile.EVPN != nil {
        r.IsRouteReflector = profile.EVPN.RouteReflector
        r.ClusterID = profile.EVPN.ClusterID
        if r.ClusterID == "" {
            r.ClusterID = profile.LoopbackIP // default to loopback
        }

        // BGP neighbors: lookup peer profiles to get their loopback IPs
        r.BGPNeighbors = []string{}
        for _, peerName := range profile.EVPN.Peers {
            if peerName == deviceName { continue }
            if peerProfile, err := loadProfile(peerName); err == nil {
                r.BGPNeighbors = append(r.BGPNeighbors, peerProfile.LoopbackIP)
            }
        }
    }

    // Merge maps: profile > zone > global
    r.PrefixLists = mergeMaps(network.PrefixLists, zone.PrefixLists, profile.PrefixLists)

    return r
}
```

## 8. Audit Logging

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

## 9. Permission System

### 9.1 Permission Definitions (`pkg/auth/permission.go`)

```go
type Permission string

// Write permissions — enforced via checkExecutePermission() in CLI write commands.
// Read/view operations are always allowed (no permission check in dry-run/preview mode).
const (
    PermServiceApply  Permission = "service.apply"
    PermServiceRemove Permission = "service.remove"

    PermInterfaceModify Permission = "interface.modify"

    PermLAGCreate Permission = "lag.create"
    PermLAGModify Permission = "lag.modify"
    PermLAGDelete Permission = "lag.delete"

    PermVLANCreate Permission = "vlan.create"
    PermVLANModify Permission = "vlan.modify"
    PermVLANDelete Permission = "vlan.delete"

    PermACLModify Permission = "acl.modify"

    PermEVPNModify Permission = "evpn.modify"

    PermQoSCreate Permission = "qos.create"
    PermQoSModify Permission = "qos.modify"
    PermQoSDelete Permission = "qos.delete"

    PermVRFCreate Permission = "vrf.create"
    PermVRFModify Permission = "vrf.modify"
    PermVRFDelete Permission = "vrf.delete"

    PermDeviceCleanup Permission = "device.cleanup"

    PermSpecAuthor Permission = "spec.author"

    PermFilterCreate Permission = "filter.create"
    PermFilterDelete Permission = "filter.delete"
)
```

### 9.2 Permission Checker (`pkg/auth/checker.go`)

```go
// Context provides context for permission checks — which service,
// device, and interface are being operated on.
type Context struct {
    Device    string
    Service   string
    Interface string
    Resource  string
}

// Checker validates user permissions.
// Current user is inferred from the OS at construction time.
type Checker struct {
    network     *spec.NetworkSpecFile
    currentUser string
}

func NewChecker(network *spec.NetworkSpecFile) *Checker

// Check verifies if the current user has a permission.
// No user parameter — uses currentUser from construction.
func (c *Checker) Check(permission Permission, ctx *Context) error {
    // 1. Superusers bypass all checks
    if c.isSuperUser(c.currentUser) {
        return nil
    }

    // 2. Check service-specific permissions first (via checkPermissionMap)
    if ctx != nil && ctx.Service != "" {
        if svc, ok := c.network.Services[ctx.Service]; ok {
            if allowed := c.checkServicePermission(c.currentUser, permission, svc); allowed {
                return nil
            }
        }
    }

    // 3. Check global permissions (via checkPermissionMap)
    if c.checkGlobalPermission(c.currentUser, permission) {
        return nil
    }

    return &PermissionError{...}
}

// checkPermissionMap checks whether username has the given permission in permMap.
// It first checks the "all" wildcard key, then the specific permission key.
func (c *Checker) checkPermissionMap(username string, permission Permission, permMap map[string][]string) bool {
    // Check for "all" permission first
    if groups, ok := permMap["all"]; ok {
        if c.userInGroups(username, groups) {
            return true
        }
    }
    // Check specific permission
    groups, ok := permMap[string(permission)]
    if !ok {
        return false
    }
    return c.userInGroups(username, groups)
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

### 11.1 Noun-Group CLI Pattern

The CLI uses a noun-group pattern where resources are top-level commands and actions are subcommands:

```
newtron <device> <noun> <action> [args] [-x]
```

**Implicit device detection:** The first argument is treated as a device name unless it matches a registered command. This lets users write `newtron leaf1 vlan list` instead of `newtron -d leaf1 vlan list`.

**Two command scopes:**
- **Device-required:** Commands that operate on CONFIG_DB (interface, vlan, vrf, lag, acl, evpn, bgp, qos apply/remove). Require a device name.
- **No-device:** Commands that operate on network.json specs (service list, evpn ipvpn list, qos list, filter list, settings). Work without a device.

### 11.2 Root Command (`cmd/newtron/main.go`)

```go
var rootCmd = &cobra.Command{
    Use:   "newtron",
    Short: "SONiC Network Configuration Tool",
}

var (
    networkName string // -n, --network
    deviceName  string // -d, --device

    specDir     string // -S, --specs
    executeMode bool   // -x, --execute
    noSave      bool   //     --no-save (requires -x)
    verbose     bool   // -v, --verbose
    jsonOutput  bool   //     --json
)

func main() {
    // Implicit device name: if the first arg is not a known command or flag,
    // treat it as a device name.
    if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") && !isKnownCommand(os.Args[1]) {
        os.Args = append([]string{os.Args[0], "-d", os.Args[1]}, os.Args[2:]...)
    }
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

// isKnownCommand checks if a string matches a registered top-level command name.
func isKnownCommand(name string) bool {
    for _, cmd := range rootCmd.Commands() {
        if cmd.Name() == name { return true }
        for _, alias := range cmd.Aliases {
            if alias == name { return true }
        }
    }
    return name == "help" || name == "completion"
}

func init() {
    // Context flags (object selectors)
    rootCmd.PersistentFlags().StringVarP(&networkName, "network", "n", "", "Network name")
    rootCmd.PersistentFlags().StringVarP(&deviceName, "device", "d", "", "Device name")

    // Global option flags
    rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "Specification directory")
    rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

    // Write flags (-x/--no-save) registered per noun-group parent (PersistentFlags)
    for _, cmd := range []*cobra.Command{
        interfaceCmd, vlanCmd, lagCmd, aclCmd, evpnCmd, bgpCmd,
        vrfCmd, serviceCmd, baselineCmd, deviceCmd, qosCmd, filterCmd,
    } {
        addWriteFlags(cmd)
        addOutputFlags(cmd)
    }

    // Command Groups
    rootCmd.AddGroup(
        &cobra.Group{ID: "resource", Title: "Resource Commands:"},
        &cobra.Group{ID: "device", Title: "Device Operations:"},
        &cobra.Group{ID: "meta", Title: "Configuration & Meta:"},
    )

    // Resource Commands (noun-groups)
    for _, cmd := range []*cobra.Command{
        interfaceCmd, vlanCmd, lagCmd, aclCmd, evpnCmd, bgpCmd,
        vrfCmd, serviceCmd, baselineCmd, qosCmd, filterCmd,
    } {
        cmd.GroupID = "resource"
        rootCmd.AddCommand(cmd)
    }

    // Device Operations
    for _, cmd := range []*cobra.Command{showCmd, provisionCmd, healthCmd, deviceCmd} {
        cmd.GroupID = "device"
        rootCmd.AddCommand(cmd)
    }

    // Configuration & Meta
    for _, cmd := range []*cobra.Command{settingsCmd, auditCmd, platformCmd, versionCmd} {
        cmd.GroupID = "meta"
        rootCmd.AddCommand(cmd)
    }

    // Premature commands (hidden)
    rootCmd.AddCommand(shellCmd)
}
```

**Flag notes:**
- `-S` is `--specs` (uppercase S for specification directory)
- `--no-save` opts out of the default save-after-execute behavior (requires `-x`)
- No `-i` (interface) flag — interface names are positional args within noun commands
- `-x` / `--execute` and `--no-save` are inherited via PersistentFlags on each noun-group parent

**Helper functions:**

```go
// requireDevice ensures a device is specified via -d flag or implicit first arg.
func requireDevice(ctx context.Context) (*node.Node, error) {
    if deviceName == "" {
        return nil, fmt.Errorf("device required: use -d <device> flag")
    }
    return net.ConnectNode(ctx, deviceName)
}

// withDeviceWrite handles boilerplate for device-level write commands.
// The callback receives a connected, locked node and returns a changeset.
// If changeset is nil, the helper returns nil (command handled its own output).
// If changeset is non-nil, the helper prints it and handles execute/dry-run.
func withDeviceWrite(fn func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error)) error {
    ctx := context.Background()
    dev, err := requireDevice(ctx)
    if err != nil {
        return err
    }
    defer dev.Disconnect()

    if err := dev.Lock(); err != nil {
        return fmt.Errorf("locking device: %w", err)
    }
    defer dev.Unlock()

    changeSet, err := fn(ctx, dev)
    if err != nil {
        return err
    }
    if changeSet == nil {
        return nil
    }

    fmt.Print(changeSet.Preview())

    if executeMode {
        return executeAndSave(ctx, changeSet, dev)
    }
    printDryRunNotice()
    return nil
}
```

The `withDeviceWrite` pattern eliminates repeated connect-lock-execute boilerplate across all write commands. Every noun-group write action (e.g., `vlan create`, `vrf add-neighbor`, `evpn setup`) delegates through this helper.

### 11.3 Noun-Group Command Mapping

Each noun has its own set of write and read actions. Interface names, VLAN IDs, VRF names, and other natural keys are positional arguments within the noun command.

| Noun | Write Actions | Read Actions |
|------|--------------|-------------|
| `interface` | `set <intf> <prop> <val>` | `list`, `show <intf>`, `get <intf> <prop>`, `list-acls`, `list-members` |
| `vlan` | `create`, `delete`, `add-interface`, `remove-interface`, `configure-svi`, `bind-macvpn`, `unbind-macvpn` | `list`, `show <id>`, `status` |
| `vrf` | `create`, `delete`, `add-interface`, `remove-interface`, `bind-ipvpn`, `unbind-ipvpn`, `add-neighbor`, `remove-neighbor`, `add-route`, `remove-route` | `list`, `show <name>`, `status` |
| `lag` | `create`, `delete`, `add-interface`, `remove-interface` | `list`, `show <name>`, `status` |
| `evpn` | `setup` | `status` |
| `evpn ipvpn` | `create`, `delete` | `list`, `show <name>` |
| `evpn macvpn` | `create`, `delete` | `list`, `show <name>` |
| `bgp` | (none -- visibility only) | `status` |
| `qos` | `create`, `delete`, `add-queue`, `remove-queue`, `apply`, `remove` | `list`, `show <name>` |
| `filter` | `create`, `delete`, `add-rule`, `remove-rule` | `list`, `show <name>` |
| `service` | `create`, `delete`, `apply`, `remove`, `refresh` | `list`, `show <name>`, `get <intf>` |
| `acl` | `create`, `delete`, `add-rule`, `delete-rule`, `bind`, `unbind` | `list`, `show <name>` |

**Naming convention:** Member operations use `add-interface`/`remove-interface` (not `add-member`), making the target entity explicit.

### 11.4 VRF Neighbor Commands

Neighbor management lives in the `vrf` noun group, not in `bgp`. The `bgp` noun is visibility-only (status command).

```
newtron leaf1 vrf add-neighbor <vrf-name> <interface> <remote-asn> [-x]
newtron leaf1 vrf remove-neighbor <vrf-name> <interface> [-x]
```

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

**BGP visibility:** `bgp status` is the only BGP CLI command. It shows a unified view combining local identity, configured neighbors, and operational state from STATE_DB.

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
                "use `service remove` first, then reapply with new settings",
                property)
        }
    }
    cs := NewChangeSet(i.Node().Name(), "interface.set")
    // ...
    return cs, nil
}
```

### 11.6 Service Refresh

When a service definition changes (e.g., filter-spec updated in `network.json`), interfaces using that service can be synchronized via remove+reapply:

```go
func (i *Interface) RefreshService(ctx context.Context) (*ChangeSet, error) {
    n := i.node

    if err := n.precondition("refresh-service", i.name).Result(); err != nil {
        return nil, err
    }
    if !i.HasService() {
        return nil, fmt.Errorf("interface %s has no service to refresh", i.name)
    }

    // Capture binding values before RemoveService records the delete
    b := i.binding()
    serviceName := b.ServiceName
    serviceIP := b.IPAddress

    // Remove the current service
    removeCS, err := i.RemoveService(ctx)
    if err != nil {
        return nil, fmt.Errorf("removing old service: %w", err)
    }

    // Clear the binding from the ConfigDB cache so ApplyService's
    // HasService() check passes. The delete change is already recorded
    // above; this cache mutation only affects reads within this episode.
    configDB := n.ConfigDB()
    delete(configDB.NewtronServiceBinding, i.name)

    // Reapply the service with current definition
    applyCS, err := i.ApplyService(ctx, serviceName, ApplyServiceOpts{IPAddress: serviceIP})
    if err != nil {
        return nil, fmt.Errorf("reapplying service: %w", err)
    }

    // Merge the change sets
    cs := NewChangeSet(n.Name(), "interface.refresh-service")
    cs.Merge(removeCS)
    cs.Merge(applyCS)

    return cs, nil
}
```

**Refresh semantics:**
- **Remove+reapply**: RefreshService does not perform a field-by-field diff. It fully removes the existing service (via `RemoveService`) and reapplies it (via `ApplyService`), producing a merged ChangeSet.
- **Cache manipulation**: The binding is deleted from the in-memory ConfigDB cache between remove and reapply so that `ApplyService`'s `HasService()` precondition check passes. The actual CONFIG_DB delete is already recorded in `removeCS`.
- **PeerAS limitation**: PeerAS is set to 0 on refresh since the BGP neighbor was already configured by the original apply. For services with `peer_as: "request"`, this means the neighbor AS is not re-prompted.

### 11.7 Orphan Cleanup

The `cleanup` command removes orphaned configurations from the device. Philosophy: only active configurations should exist on the device.

```go
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error) {
    cs := NewChangeSet(n.name, "device.cleanup")
    summary := &CleanupSummary{}
    configDB := n.ConfigDB()

    // Find orphaned ACLs (no interfaces bound)
    if cleanupType == "" || cleanupType == "acl" {
        for aclName, acl := range configDB.ACLTable {
            if acl.Ports == "" {
                summary.OrphanedACLs = append(summary.OrphanedACLs, aclName)
                cs.Deletes(n.deleteAclTableConfig(aclName))
            }
        }
    }

    // Find orphaned VRFs (no interfaces bound)
    if cleanupType == "" || cleanupType == "vrf" {
        for vrfName := range configDB.VRF {
            if vrfName == "default" { continue }
            hasUsers := false
            for intfName, intf := range configDB.Interface {
                if strings.Contains(intfName, "|") { continue }
                if intf.VRFName == vrfName { hasUsers = true; break }
            }
            if !hasUsers {
                summary.OrphanedVRFs = append(summary.OrphanedVRFs, vrfName)
                cs.Deletes(createVrfConfig(vrfName))
            }
        }
    }

    // Find orphaned VNI mappings (VRF or VLAN doesn't exist)
    if cleanupType == "" || cleanupType == "vni" {
        for mapKey, mapping := range configDB.VXLANTunnelMap {
            orphaned := false
            if mapping.VRF != "" {
                if _, ok := configDB.VRF[mapping.VRF]; !ok { orphaned = true }
            }
            if mapping.VLAN != "" {
                if _, ok := configDB.VLAN[mapping.VLAN]; !ok { orphaned = true }
            }
            if orphaned {
                summary.OrphanedVNIMappings = append(summary.OrphanedVNIMappings, mapKey)
                cs.Deletes(deleteVniMapByKeyConfig(mapKey))
            }
        }
    }

    return cs, summary, nil
}
```

**Cleanup Types:**

| Type | Description |
|------|-------------|
| `acl` | ACL tables not bound to any interface |
| `vrf` | VRFs with no interface bindings |
| `vni` | VNI mappings for deleted VLANs/VRFs |
| (empty) | All of the above |

### 11.8 Settings Persistence

User settings are stored in `~/.newtron/settings.json`:

```go
type Settings struct {
    DefaultNetwork  string `json:"default_network,omitempty"`
    SpecDir         string `json:"spec_dir,omitempty"`
    DefaultSuite    string `json:"default_suite,omitempty"`     // Default --dir for newtest run
    TopologiesDir   string `json:"topologies_dir,omitempty"`    // Base directory for newtest topologies
    AuditLogPath    string `json:"audit_log_path,omitempty"`    // Override default audit log path
    AuditMaxSizeMB  int    `json:"audit_max_size_mb,omitempty"` // Max audit log size in MB (default: 10)
    AuditMaxBackups int    `json:"audit_max_backups,omitempty"` // Max rotated log files (default: 10)
}
```

### 11.9 Operational Query Commands

These commands are read-only and do not require `-x`:

**Per-noun status commands** — each noun group has its own `status` subcommand that queries STATE_DB for operational state:

| Command | Description |
|---------|-------------|
| `vlan status` | All VLANs with operational state (SVI up/down, member count) |
| `vrf status` | All VRFs with interface and neighbor counts |
| `lag status` | All LAGs with member active/standby state |
| `bgp status` | All BGP neighbors with operational state (Established/Idle, pfx rcvd/sent, uptime) |
| `evpn status` | VTEP state, VNI mappings, remote VTEPs |

**Audit commands** (`cmd_audit.go`) — query audit log:

| Command | Description |
|---------|-------------|
| `audit list` | List audit events with filters (`--device`, `--user`, `--last`, `--limit`, `--failures`) |

**Shell command** (`shell.go`) — interactive REPL:

| Command | Description |
|---------|-------------|
| `shell` | Enter interactive shell with device connection reuse, tab completion, and command history |

### 11.10 Config Persistence (`--no-save`)

By default, `executeAndSave` calls `ChangeSet.Verify()` after a successful `Apply()`, then `Device.SaveConfig()` to persist changes across reboots. If verification fails (entries missing or field mismatches), config is **not** saved — the partial state remains in runtime CONFIG_DB only, and `config reload` restores the last saved state. The `--no-save` flag (requires `-x`) skips the persistence step:

```
newtron leaf1 interface set Ethernet0 mtu 9000 -x             # execute + save (default)
newtron leaf1 interface set Ethernet0 mtu 9000 -x --no-save   # execute without saving
```

Without `-x`, changes are previewed only (dry-run). `--no-save` without `-x` is an error.

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


