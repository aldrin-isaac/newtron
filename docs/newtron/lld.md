# Newtron Low-Level Design (LLD)

This document covers **how** and **what fields** — type definitions, method signatures, CONFIG_DB schemas, HTTP API routes, and CLI command trees. For architecture and design rationale, see the [HLD](hld.md). For device-layer internals (SSH tunneling, Redis clients, write paths), see the [Device LLD](device-lld.md).

## 1. Package Structure

```
pkg/newtron/                          # Public API — types, wrappers
    types.go                          # All public types (WriteResult, ExecOpts, DeviceInfo, etc.)
    network.go                        # Network wrapper (public)
    node.go                           # Node wrapper (public)
    settings.go                       # UserSettings load/save
    settings/settings.go              # Settings file I/O

pkg/newtron/api/                      # HTTP server
    server.go                         # Server struct, Start/Stop, Register/Unregister
    actors.go                         # NetworkActor, NodeActor, connection caching
    handler.go                        # Route registration (buildMux), JSON helpers
    handler_node.go                   # Node operation handlers
    handler_network.go                # Network/spec operation handlers
    handler_interface.go              # Interface operation handlers
    handler_composite.go              # Composite generate/deliver/verify handlers
    types.go                          # HTTP request/response types, error mapping
    api_test.go                       # API completeness test

pkg/newtron/client/                   # HTTP client (used by CLI, newtrun)
    client.go                         # Client struct, New(), HTTP helpers
    network.go                        # Spec read/write operations
    node.go                           # Node read + write operations
    interface.go                      # Interface-scoped operations

pkg/newtron/network/                  # Network internals
    network.go                        # Network struct, Connect, spec accessors
    topology.go                       # Topology provisioner, GenerateDeviceComposite
    resolved_specs.go                 # ResolvedSpecs (SpecProvider implementation)

pkg/newtron/network/node/             # Node internals
    node.go                           # Node struct, Lock/Unlock, Execute
    interface.go                      # Interface struct, accessors
    changeset.go                      # ChangeSet, Apply, Verify, Preview
    precondition.go                   # PreconditionChecker
    composite.go                      # CompositeBuilder, DeliverComposite
    vlan_ops.go                       # VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL
    vrf_ops.go                        # VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT
    bgp_ops.go                        # BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF, etc.
    evpn_ops.go                       # VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP, etc.
    acl_ops.go                        # ACL_TABLE, ACL_RULE
    qos_ops.go                        # PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, etc.
    interface_ops.go                  # INTERFACE table — SetIP, RemoveIP, SetVRF, BindACL, UnbindACL, Set
    interface_bgp_ops.go              # Interface-level BGP neighbor management
    baseline_ops.go                   # LOOPBACK_INTERFACE
    portchannel_ops.go                # PORTCHANNEL, PORTCHANNEL_MEMBER
    service_ops.go                    # ApplyService, RemoveService, RefreshService,
                                      # NEWTRON_SERVICE_BINDING, ROUTE_MAP, PREFIX_SET, etc.
    service_gen.go                    # Service-to-entries translation
    cleanup_ops.go                    # Orphan cleanup (ACLs, VRFs, VNI mappings)
    qos.go                            # QoS policy → CONFIG_DB translation
    macvpn_ops.go                     # MAC-VPN bind/unbind
    health_ops.go                     # Health checks

pkg/newtron/spec/                     # Spec file I/O
    types.go                          # All spec types (ServiceSpec, IPVPNSpec, etc.)
    loader.go                         # Loader, SaveNetwork (atomic temp+rename)

pkg/newtron/device/sonic/             # SONiC device layer
    device.go                         # Device struct, Connect/Disconnect
    configdb.go                       # ConfigDB struct, ConfigDBClient, SCAN+parse
    configdb_parsers.go               # 42-entry table-driven parser registry
    statedb.go                        # StateDBClient, health checks
    statedb_parsers.go                # 13-entry table-driven parser registry
    appldb.go                         # AppDBClient, GetRoute (APP_DB)
    asicdb.go                         # AsicDBClient, GetRouteASIC (ASIC_DB)
    pipeline.go                       # Entry, PipelineSet, ReplaceAll
    platform.go                       # SonicPlatformConfig, PortDefinition
    types.go                          # RouteEntry, VerificationResult, ConfigChange

cmd/newtron/                          # CLI
    main.go                           # App struct, PersistentPreRunE, cobra setup
    cmd_service.go                    # service noun
    cmd_vlan.go                       # vlan noun
    cmd_vrf.go                        # vrf noun
    cmd_interface.go                  # interface noun
    cmd_bgp.go                        # bgp noun
    cmd_evpn.go                       # evpn noun
    cmd_lag.go                        # lag noun
    cmd_acl.go                        # acl noun
    cmd_qos.go                        # qos noun
    cmd_filter.go                     # filter noun
    cmd_provision.go                  # provision noun
    cmd_health.go                     # health noun
    cmd_device.go                     # device operations
    cmd_show.go                       # show noun
    cmd_platform.go                   # platform noun
    cmd_settings.go                   # settings noun
    cmd_audit.go                      # audit noun

cmd/newtron-server/                   # HTTP server binary
    main.go                           # Flag parsing, signal handling, graceful shutdown
```

### CONFIG_DB Table Ownership

Each CONFIG_DB table has exactly one owning file:

| File | Tables |
|------|--------|
| `vlan_ops.go` | VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL |
| `vrf_ops.go` | VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT |
| `bgp_ops.go` | BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, DEVICE_METADATA |
| `evpn_ops.go` | VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP, SUPPRESS_VLAN_NEIGH, BGP_EVPN_VNI |
| `acl_ops.go` | ACL_TABLE, ACL_RULE |
| `qos_ops.go` | PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE |
| `interface_ops.go` | INTERFACE |
| `baseline_ops.go` | LOOPBACK_INTERFACE |
| `portchannel_ops.go` | PORTCHANNEL, PORTCHANNEL_MEMBER |
| `service_ops.go` | NEWTRON_SERVICE_BINDING, ROUTE_MAP, PREFIX_SET, COMMUNITY_SET |

## 2. Spec File Types

Spec types live in `pkg/newtron/spec/types.go`. They define declarative intent — JSON files under the spec directory.

### 2.1 NetworkSpecFile

Top-level spec file (`network.json`).

```go
type NetworkSpecFile struct {
    Version     string               `json:"version"`
    SuperUsers  []string             `json:"super_users"`
    UserGroups  map[string][]string  `json:"user_groups"`
    Permissions map[string][]string  `json:"permissions"`
    Zones       map[string]*ZoneSpec `json:"zones"`
    OverridableSpecs                 // embedded
}
```

### 2.2 OverridableSpecs

Embedded in `NetworkSpecFile`, `ZoneSpec`, and `DeviceProfile`. Three-level inheritance: network → zone → node (lower wins).

```go
type OverridableSpecs struct {
    PrefixLists   map[string][]string      `json:"prefix_lists,omitempty"`
    Filters       map[string]*FilterSpec   `json:"filters,omitempty"`
    QoSPolicies   map[string]*QoSPolicy    `json:"qos_policies,omitempty"`
    RoutePolicies map[string]*RoutePolicy  `json:"route_policies,omitempty"`
    IPVPNs        map[string]*IPVPNSpec    `json:"ipvpns,omitempty"`
    MACVPNs       map[string]*MACVPNSpec   `json:"macvpns,omitempty"`
    Services      map[string]*ServiceSpec  `json:"services,omitempty"`
}
```

### 2.3 ServiceSpec

Defines a named service that can be applied to interfaces via `ApplyService`. The `service_type` determines which CONFIG_DB tables are written and what parameters are required at apply time (§3.6 `ApplyServiceOpts`).

```go
type ServiceSpec struct {
    Description    string              `json:"description"`
    ServiceType    string              `json:"service_type"`
    IPVPN          string              `json:"ipvpn,omitempty"`
    MACVPN         string              `json:"macvpn,omitempty"`
    VRFType        string              `json:"vrf_type,omitempty"`
    Routing        *RoutingSpec        `json:"routing,omitempty"`
    IngressFilter  string              `json:"ingress_filter,omitempty"`
    EgressFilter   string              `json:"egress_filter,omitempty"`
    QoSPolicy      string              `json:"qos_policy,omitempty"`
    Permissions    map[string][]string `json:"permissions,omitempty"`
}
```

**Service type constants** (`pkg/newtron/types.go`):

| Constant | Value | Requires |
|----------|-------|----------|
| `ServiceTypeRouted` | `"routed"` | IP address at apply time |
| `ServiceTypeBridged` | `"bridged"` | VLAN at apply time |
| `ServiceTypeIRB` | `"irb"` | VLAN + IP at apply time |
| `ServiceTypeEVPNRouted` | `"evpn-routed"` | `ipvpn` reference — **abandoned on CiscoVS/Silicon One** (RCA-039: L3VNI DECAP blocked by SAI) |
| `ServiceTypeEVPNBridged` | `"evpn-bridged"` | `macvpn` reference |
| `ServiceTypeEVPNIRB` | `"evpn-irb"` | Both `ipvpn` and `macvpn` references |

**VRF type values:** `"interface"` (per-interface VRF), `"shared"` (shared VRF from ipvpn name).

### 2.4 RoutingSpec

```go
type RoutingSpec struct {
    Protocol         string `json:"protocol"`           // "bgp", "static", ""
    PeerAS           string `json:"peer_as,omitempty"`  // number or "request"
    ImportPolicy     string `json:"import_policy,omitempty"`
    ExportPolicy     string `json:"export_policy,omitempty"`
    ImportCommunity  string `json:"import_community,omitempty"`
    ExportCommunity  string `json:"export_community,omitempty"`
    ImportPrefixList string `json:"import_prefix_list,omitempty"`
    ExportPrefixList string `json:"export_prefix_list,omitempty"`
    Redistribute     *bool  `json:"redistribute,omitempty"`
}
```

### 2.5 IPVPNSpec

```go
type IPVPNSpec struct {
    Description  string   `json:"description,omitempty"`
    VRF          string   `json:"vrf"`
    L3VNI        int      `json:"l3vni"`
    L3VNIVlan    int      `json:"l3vni_vlan,omitempty"`
    RouteTargets []string `json:"route_targets"`
}
```

### 2.6 MACVPNSpec

```go
type MACVPNSpec struct {
    Description    string   `json:"description,omitempty"`
    VlanID         int      `json:"vlan_id"`
    VNI            int      `json:"vni"`
    AnycastIP      string   `json:"anycast_ip,omitempty"`
    AnycastMAC     string   `json:"anycast_mac,omitempty"`
    RouteTargets   []string `json:"route_targets,omitempty"`
    ARPSuppression bool     `json:"arp_suppression,omitempty"`
}
```

### 2.7 FilterSpec

```go
type FilterSpec struct {
    Description string        `json:"description"`
    Type        string        `json:"type"`   // "ipv4" or "ipv6"
    Rules       []*FilterRule `json:"rules"`
}

type FilterRule struct {
    Sequence      int    `json:"seq"`
    Action        string `json:"action"`           // "permit" or "deny"
    SrcIP         string `json:"src_ip,omitempty"`
    DstIP         string `json:"dst_ip,omitempty"`
    SrcPrefixList string `json:"src_prefix_list,omitempty"`
    DstPrefixList string `json:"dst_prefix_list,omitempty"`
    Protocol      string `json:"protocol,omitempty"`
    SrcPort       string `json:"src_port,omitempty"`
    DstPort       string `json:"dst_port,omitempty"`
    DSCP          string `json:"dscp,omitempty"`
    CoS           string `json:"cos,omitempty"`
    Log           bool   `json:"log,omitempty"`
}
```

### 2.8 QoSPolicy

```go
type QoSPolicy struct {
    Description string      `json:"description,omitempty"`
    Queues      []*QoSQueue `json:"queues"`
}

type QoSQueue struct {
    Name   string `json:"name"`
    Type   string `json:"type"`              // "dwrr" or "strict"
    Weight int    `json:"weight,omitempty"`  // for DWRR
    DSCP   []int  `json:"dscp,omitempty"`    // DSCP values mapped to this queue
    ECN    bool   `json:"ecn,omitempty"`
}
```

### 2.9 RoutePolicy

```go
type RoutePolicy struct {
    Description string             `json:"description,omitempty"`
    Rules       []*RoutePolicyRule `json:"rules"`
}

type RoutePolicyRule struct {
    Sequence   int             `json:"seq"`
    Action     string          `json:"action"`  // "permit" or "deny"
    PrefixList string          `json:"prefix_list,omitempty"`
    Community  string          `json:"community,omitempty"`
    Set        *RoutePolicySet `json:"set,omitempty"`
}

type RoutePolicySet struct {
    LocalPref int    `json:"local_pref,omitempty"`
    Community string `json:"community,omitempty"`
    MED       int    `json:"med,omitempty"`
}
```

### 2.10 DeviceProfile

Per-device profile (`profiles/{device}.json`).

```go
type DeviceProfile struct {
    MgmtIP          string             `json:"mgmt_ip"`
    LoopbackIP      string             `json:"loopback_ip"`
    Zone            string             `json:"zone"`
    Platform        string             `json:"platform,omitempty"`
    ASNumber        *int               `json:"as_number,omitempty"`
    MAC             string             `json:"mac,omitempty"`
    UnderlayASN     int                `json:"underlay_asn,omitempty"`
    EVPN            *EVPNConfig        `json:"evpn,omitempty"`
    SSHUser         string             `json:"ssh_user,omitempty"`
    SSHPass         string             `json:"ssh_pass,omitempty"`
    SSHPort         int                `json:"ssh_port,omitempty"`
    VLANPortMapping map[int][]string   `json:"vlan_port_mapping,omitempty"`
    OverridableSpecs                   // embedded
    // Virtual host (non-switch device)
    HostIP          string             `json:"host_ip,omitempty"`
    HostGateway     string             `json:"host_gateway,omitempty"`
    // newtlab VM settings (not relevant to newtron operations)
    ConsolePort     int                `json:"console_port,omitempty"`
    VMMemory        int                `json:"vm_memory,omitempty"`
    VMCPUs          int                `json:"vm_cpus,omitempty"`
    VMImage         string             `json:"vm_image,omitempty"`
    VMHost          string             `json:"vm_host,omitempty"`
}

type EVPNConfig struct {
    Peers          []string `json:"peers,omitempty"`
    RouteReflector bool     `json:"route_reflector,omitempty"`
    ClusterID      string   `json:"cluster_id,omitempty"`
}
```

### 2.11 PlatformSpec

```go
type PlatformSpec struct {
    HWSKU               string         `json:"hwsku"`
    Description         string         `json:"description,omitempty"`
    DeviceType          string         `json:"device_type,omitempty"`
    PortCount           int            `json:"port_count"`
    DefaultSpeed        string         `json:"default_speed"`
    Breakouts           []string       `json:"breakouts,omitempty"`
    Dataplane            string         `json:"dataplane,omitempty"`
    UnsupportedFeatures  []string       `json:"unsupported_features,omitempty"`
    // newtlab VM settings
    VMImage              string         `json:"vm_image,omitempty"`
    VMMemory             int            `json:"vm_memory,omitempty"`
    VMCPUs               int            `json:"vm_cpus,omitempty"`
    VMNICDriver          string         `json:"vm_nic_driver,omitempty"`
    VMInterfaceMap       string         `json:"vm_interface_map,omitempty"`
    VMCPUFeatures        string         `json:"vm_cpu_features,omitempty"`
    VMCredentials        *VMCredentials `json:"vm_credentials,omitempty"`
    VMBootTimeout        int            `json:"vm_boot_timeout,omitempty"`
    VMImageRelease       string         `json:"vm_image_release,omitempty"`
}
```

### 2.12 TopologySpecFile

```go
type TopologySpecFile struct {
    Version     string                      `json:"version"`
    Platform    string                      `json:"platform,omitempty"`
    Description string                      `json:"description,omitempty"`
    Devices     map[string]*TopologyDevice  `json:"devices"`
    Links       []*TopologyLink             `json:"links,omitempty"`
    NewtLab     *NewtLabConfig              `json:"newtlab,omitempty"`
}

type TopologyDevice struct {
    DeviceConfig *TopologyDeviceConfig           `json:"device_config,omitempty"`
    Interfaces   map[string]*TopologyInterface   `json:"interfaces"`
    PortChannels map[string]*TopologyPortChannel `json:"portchannels,omitempty"`
}

type TopologyInterface struct {
    Link    string            `json:"link,omitempty"`
    Service string            `json:"service"`
    IP      string            `json:"ip,omitempty"`
    VRF     string            `json:"vrf,omitempty"`
    Params  map[string]string `json:"params,omitempty"`
}

type TopologyPortChannel struct {
    Members []string          `json:"members"`          // Physical interface names
    Service string            `json:"service,omitempty"`
    IP      string            `json:"ip,omitempty"`
    Params  map[string]string `json:"params,omitempty"`
}

type TopologyLink struct {
    A string `json:"a"`  // "switch1:Ethernet0"
    Z string `json:"z"`  // "switch2:Ethernet0"
}
```

## 3. Public API Types

All public types live in `pkg/newtron/types.go`. These are the serialization contract between the HTTP server and its clients (CLI, newtrun).

### 3.1 Execution and Write Result

Every write endpoint accepts `ExecOpts` (mapped from query parameters: `?dry_run=true` inverts `Execute`, `?no_save=true` sets `NoSave`) and returns a `WriteResult` with the outcome.

```go
type ExecOpts struct {
    Execute bool  // true = apply; false = dry-run preview
    NoSave  bool  // skip config save after apply
}

type WriteResult struct {
    Preview      string              `json:"preview,omitempty"`
    ChangeCount  int                 `json:"change_count"`
    Applied      bool                `json:"applied"`
    Verified     bool                `json:"verified"`
    Saved        bool                `json:"saved"`
    Verification *VerificationResult `json:"verification,omitempty"`
}

type VerificationResult struct {
    Passed int                 `json:"passed"`
    Failed int                 `json:"failed"`
    Errors []VerificationError `json:"errors,omitempty"`
}

type VerificationError struct {
    Table    string `json:"table"`
    Key      string `json:"key"`
    Field    string `json:"field"`
    Expected string `json:"expected"`
    Actual   string `json:"actual"`
}
```

### 3.2 Device Info and Interface Views

```go
type DeviceInfo struct {
    Name             string   `json:"name"`
    MgmtIP           string   `json:"mgmt_ip"`
    LoopbackIP       string   `json:"loopback_ip"`
    Platform         string   `json:"platform"`
    Zone             string   `json:"zone"`
    BGPAS            int      `json:"bgp_as"`
    RouterID         string   `json:"router_id"`
    VTEPSourceIP     string   `json:"vtep_source_ip"`
    BGPNeighbors     []string `json:"bgp_neighbors"`
    InterfaceCount   int      `json:"interfaces"`
    PortChannelCount int      `json:"port_channels"`
    VLANCount        int      `json:"vlans"`
    VRFCount         int      `json:"vrfs"`
}

type InterfaceSummary struct {
    Name        string   `json:"name"`
    AdminStatus string   `json:"admin_status"`
    OperStatus  string   `json:"oper_status"`
    IPAddresses []string `json:"ip_addresses,omitempty"`
    VRF         string   `json:"vrf,omitempty"`
    Service     string   `json:"service,omitempty"`
}

type InterfaceDetail struct {
    Name        string   `json:"name"`
    AdminStatus string   `json:"admin_status"`
    OperStatus  string   `json:"oper_status"`
    Speed       string   `json:"speed"`
    MTU         int      `json:"mtu"`
    IPAddresses []string `json:"ip_addresses,omitempty"`
    VRF         string   `json:"vrf,omitempty"`
    Service     string   `json:"service,omitempty"`
    PCMember    bool     `json:"pc_member"`
    PCParent    string   `json:"pc_parent,omitempty"`
    IngressACL  string   `json:"ingress_acl,omitempty"`
    EgressACL   string   `json:"egress_acl,omitempty"`
    PCMembers   []string `json:"pc_members,omitempty"`
    VLANMembers []string `json:"vlan_members,omitempty"`
}
```

### 3.3 Resource Views

Returned by the node read endpoints in §4.5. Each type corresponds to a resource noun's `list` or `show` response.

```go
type VLANStatusEntry struct {
    ID          int               `json:"id"`
    Name        string            `json:"name,omitempty"`
    L2VNI       int               `json:"l2_vni,omitempty"`
    SVI         string            `json:"svi,omitempty"`
    MemberCount int               `json:"member_count"`
    MemberNames []string          `json:"members,omitempty"`
    MACVPN      string            `json:"macvpn,omitempty"`
    MACVPNInfo  *VLANMACVPNDetail `json:"macvpn_detail,omitempty"`
}

type VLANMACVPNDetail struct {
    Name           string `json:"name,omitempty"`
    L2VNI          int    `json:"l2_vni,omitempty"`
    ARPSuppression bool   `json:"arp_suppression"`
}

type VRFDetail struct {
    Name         string             `json:"name"`
    L3VNI        int                `json:"l3_vni,omitempty"`
    Interfaces   []string           `json:"interfaces,omitempty"`
    BGPNeighbors []BGPNeighborEntry `json:"bgp_neighbors,omitempty"`
}

type ACLTableDetail struct {
    Name        string        `json:"name"`
    Type        string        `json:"type"`
    Stage       string        `json:"stage"`
    Interfaces  string        `json:"interfaces"`
    Description string        `json:"description,omitempty"`
    Rules       []ACLRuleInfo `json:"rules"`
}

type BGPStatusResult struct {
    LocalAS    int                 `json:"local_as"`
    RouterID   string              `json:"router_id"`
    LoopbackIP string              `json:"loopback_ip"`
    Neighbors  []BGPNeighborStatus `json:"neighbors,omitempty"`
    EVPNPeers  []string            `json:"evpn_peers,omitempty"`
}

type EVPNStatusResult struct {
    VTEPs       map[string]string `json:"vteps,omitempty"`
    NVOs        map[string]string `json:"nvos,omitempty"`
    VNIMappings []VNIMapping      `json:"vni_mappings,omitempty"`
    L3VNIVRFs   []L3VNIEntry      `json:"l3vni_vrfs,omitempty"`
    VTEPStatus  string            `json:"vtep_status,omitempty"`
    RemoteVTEPs []string          `json:"remote_vteps,omitempty"`
    VNICount    int               `json:"vni_count"`
}

type LAGStatusEntry struct {
    Name          string   `json:"name"`
    AdminStatus   string   `json:"admin_status"`
    OperStatus    string   `json:"oper_status,omitempty"`
    Members       []string `json:"members"`
    ActiveMembers []string `json:"active_members"`
    MTU           int      `json:"mtu,omitempty"`
}

type HealthReport struct {
    Device      string              `json:"device"`
    Status      string              `json:"status"` // "healthy", "degraded", "unhealthy"
    ConfigCheck *VerificationResult `json:"config_check,omitempty"`
    OperChecks  []HealthCheckResult `json:"oper_checks,omitempty"`
}
```

### 3.4 Composite Types

`CompositeInfo` is an opaque handle with a defined lifecycle: `GenerateComposite` creates it, the caller stores it, then passes it to `DeliverComposite` or `VerifyComposite`. The `internal` field is never serialized — the HTTP server stores the composite data server-side keyed by a handle string.

```go
type CompositeInfo struct {
    DeviceName string         `json:"device_name"`
    EntryCount int            `json:"entry_count"`
    Tables     map[string]int `json:"tables"`
    internal   any            // opaque — never serialized
}

type CompositeMode string

const (
    CompositeOverwrite CompositeMode = "overwrite"
    CompositeMerge     CompositeMode = "merge"
)

type DeliveryResult struct {
    Applied int `json:"applied"`
    Skipped int `json:"skipped"`
    Failed  int `json:"failed"`
}
```

### 3.5 Route Types

```go
type RouteEntry struct {
    Prefix   string         `json:"prefix"`
    VRF      string         `json:"vrf"`
    Protocol string         `json:"protocol"`
    NextHops []RouteNextHop `json:"next_hops,omitempty"`
    Source   string         `json:"source"` // "APP_DB" or "ASIC_DB"
}

type RouteNextHop struct {
    Address   string `json:"address"`
    Interface string `json:"interface"`
}
```

### 3.6 Config Request Types

Used as request bodies for the node write endpoints in §4.6 and interface endpoints in §4.8.

```go
type VLANConfig struct {
    VlanID      int
    Description string
}

type SVIConfig struct {
    VlanID     int
    VRF        string
    IPAddress  string
    AnycastMAC string
}

type VRFConfig struct {
    Name string
}

type BGPNeighborConfig struct {
    VRF         string `json:"vrf,omitempty"`
    Interface   string `json:"interface,omitempty"`
    RemoteAS    int    `json:"remote_as,omitempty"`
    NeighborIP  string `json:"neighbor_ip,omitempty"`
    Description string `json:"description,omitempty"`
}

type ApplyServiceOpts struct {
    IPAddress string
    VLAN      int
    PeerAS    int
    Params    map[string]string
}

type PortChannelConfig struct {
    Name     string
    Members  []string
    MinLinks int
    FastRate bool
    Fallback bool
    MTU      int
}
```

### 3.7 Spec Authoring Requests

Used as request bodies for the network spec write endpoints in §4.3. These create or modify spec files on disk via the server.

```go
type CreateServiceRequest struct {
    Name, Type, IPVPN, MACVPN, VRFType string
    QoSPolicy, IngressFilter, EgressFilter, Description string
}

type CreateIPVPNRequest struct {
    Name         string
    L3VNI        int
    VRF          string
    RouteTargets []string
    Description  string
}

type CreateMACVPNRequest struct {
    Name           string
    VNI            int
    VlanID         int
    AnycastIP      string
    AnycastMAC     string
    RouteTargets   []string
    ARPSuppression bool
    Description    string
}

type CreateFilterRequest struct {
    Name, Type, Description string
}

type AddFilterRuleRequest struct {
    Filter   string
    Sequence int
    Action   string
    SrcIP, DstIP, SrcPrefixList, DstPrefixList string
    Protocol, SrcPort, DstPort, DSCP, CoS string
    Log bool
}

type CreateDeviceProfileRequest struct {
    Name, MgmtIP, LoopbackIP, Zone string
    Platform, MAC, SSHUser, SSHPass string
    UnderlayASN, SSHPort int
    EVPN *CreateEVPNConfigRequest
}

type CreateEVPNConfigRequest struct {
    Peers          []string
    RouteReflector bool
    ClusterID      string
}

type CreateZoneRequest struct {
    Name string
}
```

### 3.8 Settings

```go
type UserSettings struct {
    DefaultNetwork  string `json:"default_network,omitempty"`
    SpecDir         string `json:"spec_dir,omitempty"`
    DefaultSuite    string `json:"default_suite,omitempty"`
    TopologiesDir   string `json:"topologies_dir,omitempty"`
    AuditLogPath    string `json:"audit_log_path,omitempty"`
    AuditMaxSizeMB  int    `json:"audit_max_size_mb,omitempty"`
    AuditMaxBackups int    `json:"audit_max_backups,omitempty"`
    ServerURL       string `json:"server_url,omitempty"`
    NetworkID       string `json:"network_id,omitempty"`
}
```

**Defaults:**

| Field | Method | Default |
|-------|--------|---------|
| `SpecDir` | `GetSpecDir()` | `/etc/newtron` |
| `ServerURL` | `GetServerURL()` | `http://localhost:8080` |
| `NetworkID` | `GetNetworkID()` | `"default"` |
| `AuditMaxSizeMB` | `GetAuditMaxSizeMB()` | `10` |
| `AuditMaxBackups` | `GetAuditMaxBackups()` | `10` |

### 3.9 Error Types

```go
type NotFoundError struct {
    Resource string
    Name     string
}

type ValidationError struct {
    Field   string
    Message string
}

type VerificationFailedError struct {
    Device  string
    Passed  int
    Failed  int
    Total   int
    Message string
}
```

HTTP status mapping (server-side `httpStatusFromError`):

| Error Type | HTTP Status |
|------------|------------|
| `*NotFoundError` | 404 |
| `*ValidationError` | 400 |
| `*VerificationFailedError` | 409 |
| `*notRegisteredError` | 404 |
| `*alreadyRegisteredError` | 409 |
| `context.DeadlineExceeded` | 504 |
| (other) | 500 |

## 4. HTTP API Reference

All routes are registered in `pkg/newtron/api/handler.go` via `buildMux()`. Request bodies use config types from §3.6–3.7; response bodies use view types from §3.2–3.5. Write endpoints accept `ExecOpts` (§3.1) via query parameters and return `WriteResult`. Every response uses the `APIResponse` envelope:

```go
type APIResponse struct {
    Data  any    `json:"data,omitempty"`
    Error string `json:"error,omitempty"`
}
```

Write operations accept `?dry_run=true` and `?no_save=true` query parameters.

### 4.1 Server Management

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `/network` | `RegisterNetworkRequest` | `NetworkInfo` |
| `GET` | `/network` | — | `[]NetworkInfo` |
| `DELETE` | `/network/{netID}` | — | — |
| `POST` | `/network/{netID}/reload` | — | `{"status":"reloaded"}` |

### 4.2 Network Spec Reads

| Method | Path | Response |
|--------|------|----------|
| `GET` | `/network/{netID}/service` | `[]ServiceDetail` |
| `GET` | `/network/{netID}/service/{name}` | `ServiceDetail` |
| `GET` | `/network/{netID}/ipvpn` | `[]IPVPNDetail` |
| `GET` | `/network/{netID}/ipvpn/{name}` | `IPVPNDetail` |
| `GET` | `/network/{netID}/macvpn` | `[]MACVPNDetail` |
| `GET` | `/network/{netID}/macvpn/{name}` | `MACVPNDetail` |
| `GET` | `/network/{netID}/qos-policy` | `[]QoSPolicyDetail` |
| `GET` | `/network/{netID}/qos-policy/{name}` | `QoSPolicyDetail` |
| `GET` | `/network/{netID}/filter` | `[]FilterDetail` |
| `GET` | `/network/{netID}/filter/{name}` | `FilterDetail` |
| `GET` | `/network/{netID}/platform` | `[]PlatformDetail` |
| `GET` | `/network/{netID}/platform/{name}` | `PlatformDetail` |
| `GET` | `/network/{netID}/route-policy` | route policies |
| `GET` | `/network/{netID}/prefix-list` | prefix lists |
| `GET` | `/network/{netID}/profile` | `[]string` (profile names) |
| `GET` | `/network/{netID}/profile/{name}` | `DeviceProfileDetail` |
| `GET` | `/network/{netID}/zone` | `[]string` (zone names) |
| `GET` | `/network/{netID}/zone/{name}` | `ZoneDetail` |
| `GET` | `/network/{netID}/topology/node` | `[]string` (device names) |
| `GET` | `/network/{netID}/host/{name}` | `HostProfile` |
| `GET` | `/network/{netID}/feature` | all features |
| `GET` | `/network/{netID}/feature/{name}/dependency` | feature dependencies |
| `GET` | `/network/{netID}/feature/{name}/unsupported-due-to` | unsupported reasons |
| `GET` | `/network/{netID}/platform/{name}/supports/{feature}` | `bool` |

### 4.3 Network Spec Writes

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `/network/{netID}/service` | `CreateServiceRequest` | `ServiceDetail` |
| `DELETE` | `/network/{netID}/service/{name}` | — | — |
| `POST` | `/network/{netID}/ipvpn` | `CreateIPVPNRequest` | `IPVPNDetail` |
| `DELETE` | `/network/{netID}/ipvpn/{name}` | — | — |
| `POST` | `/network/{netID}/macvpn` | `CreateMACVPNRequest` | `MACVPNDetail` |
| `DELETE` | `/network/{netID}/macvpn/{name}` | — | — |
| `POST` | `/network/{netID}/qos-policy` | `CreateQoSPolicyRequest` | `QoSPolicyDetail` |
| `DELETE` | `/network/{netID}/qos-policy/{name}` | — | — |
| `POST` | `/network/{netID}/qos-policy/{name}/queue` | `AddQoSQueueRequest` | `QoSPolicyDetail` |
| `DELETE` | `/network/{netID}/qos-policy/{name}/queue/{id}` | — | — |
| `POST` | `/network/{netID}/filter` | `CreateFilterRequest` | `FilterDetail` |
| `DELETE` | `/network/{netID}/filter/{name}` | — | — |
| `POST` | `/network/{netID}/filter/{name}/rule` | `AddFilterRuleRequest` | `FilterDetail` |
| `DELETE` | `/network/{netID}/filter/{name}/rule/{seq}` | — | — |
| `POST` | `/network/{netID}/profile` | `CreateDeviceProfileRequest` | `DeviceProfileDetail` |
| `DELETE` | `/network/{netID}/profile/{name}` | — | — |
| `POST` | `/network/{netID}/zone` | `CreateZoneRequest` | `ZoneDetail` |
| `DELETE` | `/network/{netID}/zone/{name}` | — | — |

### 4.4 Provisioning

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `/network/{netID}/provision` | `ProvisionRequest` | `ProvisionResult` |
| `POST` | `/network/{netID}/composite/{device}` | — | `CompositeHandleResponse` |
| `POST` | `/network/{netID}/init/{device}?force=true` | — | `{"status": "initialized"}` or `{"status": "already_initialized"}` |

### 4.5 Node Reads

| Method | Path | Response |
|--------|------|----------|
| `GET` | `.../node/{device}/info` | `DeviceInfo` |
| `GET` | `.../node/{device}/interface` | `[]InterfaceSummary` |
| `GET` | `.../node/{device}/interface/{name}` | `InterfaceDetail` |
| `GET` | `.../node/{device}/interface/{name}/binding` | `ServiceBindingDetail` |
| `GET` | `.../node/{device}/vlan` | `[]VLANStatusEntry` |
| `GET` | `.../node/{device}/vlan/{id}` | `VLANStatusEntry` |
| `GET` | `.../node/{device}/vrf` | `[]VRFStatusEntry` |
| `GET` | `.../node/{device}/vrf/{name}` | `VRFDetail` |
| `GET` | `.../node/{device}/acl` | `[]ACLTableSummary` |
| `GET` | `.../node/{device}/acl/{name}` | `ACLTableDetail` |
| `GET` | `.../node/{device}/bgp/status` | `BGPStatusResult` |
| `GET` | `.../node/{device}/bgp/check` | BGP session check |
| `GET` | `.../node/{device}/evpn/status` | `EVPNStatusResult` |
| `GET` | `.../node/{device}/health` | `HealthReport` |
| `GET` | `.../node/{device}/lag` | `[]LAGStatusEntry` |
| `GET` | `.../node/{device}/lag/{name}` | LAG detail |
| `GET` | `.../node/{device}/neighbor` | `[]NeighEntry` |
| `GET` | `.../node/{device}/route/{vrf}/{prefix...}` | `RouteEntry` |
| `GET` | `.../node/{device}/route-asic/{prefix...}` | `RouteEntry` |
| `GET` | `.../node/{device}/configdb/{table}` | `[]string` (keys) |
| `GET` | `.../node/{device}/configdb/{table}/{key}` | `map[string]string` |
| `GET` | `.../node/{device}/configdb/{table}/{key}/exists` | `bool` |
| `GET` | `.../node/{device}/statedb/{table}/{key}` | `map[string]string` |

All node read paths use `prefix: /network/{netID}`.

### 4.6 Node Writes

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `.../node/{device}/vlan` | `VLANCreateRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/vlan/{id}` | — | `WriteResult` |
| `POST` | `.../node/{device}/vlan/{id}/member` | `VLANMemberRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/vlan/{id}/member/{iface}` | — | `WriteResult` |
| `POST` | `.../node/{device}/svi` | `SVIConfigureRequest` | `WriteResult` |
| `POST` | `.../node/{device}/remove-svi` | `RemoveSVIRequest` | `WriteResult` |
| `POST` | `.../node/{device}/vrf` | `VRFCreateRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/vrf/{name}` | — | `WriteResult` |
| `POST` | `.../node/{device}/vrf/{name}/interface` | `VRFInterfaceRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/vrf/{name}/interface/{iface}` | — | `WriteResult` |
| `POST` | `.../node/{device}/vrf/{name}/bind-ipvpn` | `BindIPVPNRequest` | `WriteResult` |
| `POST` | `.../node/{device}/vrf/{name}/unbind-ipvpn` | — | `WriteResult` |
| `POST` | `.../node/{device}/vrf/{name}/route` | `StaticRouteRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/vrf/{name}/route/{prefix...}` | — | `WriteResult` |
| `POST` | `.../node/{device}/acl` | `ACLCreateRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/acl/{name}` | — | `WriteResult` |
| `POST` | `.../node/{device}/acl/{name}/rule` | `ACLRuleAddRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/acl/{name}/rule/{rule}` | — | `WriteResult` |
| `POST` | `.../node/{device}/portchannel` | `PortChannelCreateRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/portchannel/{name}` | — | `WriteResult` |
| `POST` | `.../node/{device}/portchannel/{name}/member` | `PortChannelMemberRequest` | `WriteResult` |
| `DELETE` | `.../node/{device}/portchannel/{name}/member/{iface}` | — | `WriteResult` |
| `POST` | `.../node/{device}/configure-bgp` | — | `WriteResult` |
| `POST` | `.../node/{device}/setup-evpn` | `SetupEVPNRequest` | `WriteResult` |
| `POST` | `.../node/{device}/teardown-evpn` | — | `WriteResult` |
| `POST` | `.../node/{device}/configure-loopback` | — | `WriteResult` |
| `POST` | `.../node/{device}/remove-loopback` | — | `WriteResult` |
| `POST` | `.../node/{device}/add-bgp-neighbor` | `BGPNeighborConfig` | `WriteResult` |
| `POST` | `.../node/{device}/remove-bgp-neighbor` | `{ip}` | `WriteResult` |
| `POST` | `.../node/{device}/remove-bgp-globals` | — | `WriteResult` |
| `POST` | `.../node/{device}/apply-frr-defaults` | — | `WriteResult` |
| `POST` | `.../node/{device}/restart-service` | `RestartServiceRequest` | — |
| `POST` | `.../node/{device}/set-metadata` | `SetDeviceMetadataRequest` | `WriteResult` |
| `POST` | `.../node/{device}/refresh` | — | — |
| `POST` | `.../node/{device}/apply-qos` | `NodeApplyQoSRequest` | `WriteResult` |
| `POST` | `.../node/{device}/remove-qos` | `NodeRemoveQoSRequest` | `WriteResult` |
| `POST` | `.../node/{device}/verify-committed` | — | `VerificationResult` |
| `POST` | `.../node/{device}/config-reload` | — | — |
| `POST` | `.../node/{device}/save-config` | — | — |
| `POST` | `.../node/{device}/cleanup` | `CleanupRequest` | `CleanupSummary` |
| `POST` | `.../node/{device}/ssh-command` | `SSHCommandRequest` | `SSHCommandResponse` |
| `POST` | `.../node/{device}/execute` | `ExecuteRequest` | `WriteResult` |

### 4.7 Node Composite Operations

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `.../node/{device}/composite/generate` | — | `CompositeHandleResponse` |
| `POST` | `.../node/{device}/composite/deliver` | `CompositeHandleRequest` | `DeliveryResult` |
| `POST` | `.../node/{device}/composite/verify` | `CompositeHandleRequest` | `VerificationResult` |

### 4.8 Interface Operations

| Method | Path | Body | Response |
|--------|------|------|----------|
| `POST` | `.../interface/{name}/apply-service` | `ApplyServiceRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/remove-service` | — | `WriteResult` |
| `POST` | `.../interface/{name}/refresh-service` | — | `WriteResult` |
| `POST` | `.../interface/{name}/set-ip` | `SetIPRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/remove-ip` | `RemoveIPRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/set-vrf` | `SetVRFRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/bind-acl` | `BindACLRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/unbind-acl` | `UnbindACLRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/bind-macvpn` | `BindMACVPNRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/unbind-macvpn` | — | `WriteResult` |
| `POST` | `.../interface/{name}/add-bgp-neighbor` | `BGPNeighborConfig` | `WriteResult` |
| `POST` | `.../interface/{name}/remove-bgp-neighbor` | `{ip}` | `WriteResult` |
| `POST` | `.../interface/{name}/set` | `InterfaceSetRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/apply-qos` | `ApplyQoSRequest` | `WriteResult` |
| `POST` | `.../interface/{name}/remove-qos` | — | `WriteResult` |

Interface paths are prefixed with `.../node/{device}/`.

## 5. CONFIG_DB Table Reference

Every table newtron reads or writes, with key format and fields.

### 5.1 Core Tables

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `DEVICE_METADATA` | `localhost` | Hostname, platform, ASN, unified config mode | `hostname`, `platform`, `hwsku`, `bgp_asn`, `docker_routing_config_mode`, `frr_mgmt_framework_config` |
| `PORT` | `Ethernet0` | Physical port | `admin_status`, `mtu`, `speed`, `lanes`, `alias`, `description`, `index`, `fec`, `autoneg` |
| `PORTCHANNEL` | `PortChannel100` | LAG | `admin_status`, `mtu`, `min_links`, `fallback`, `fast_rate`, `lacp_key`, `description` |
| `PORTCHANNEL_MEMBER` | `PortChannel100\|Ethernet0` | LAG membership | NULL:NULL sentinel |
| `VLAN` | `Vlan100` | VLAN | `vlanid`, `description`, `admin_status`, `mtu`, `dhcp_servers` |
| `VLAN_MEMBER` | `Vlan100\|Ethernet0` | VLAN membership | `tagging_mode` (`tagged`, `untagged`) |
| `VLAN_INTERFACE` | `Vlan100` or `Vlan100\|10.1.1.1/24` | SVI and IPs | Base: `vrf_name`. IP sub: NULL:NULL sentinel |
| `INTERFACE` | `Ethernet0` or `Ethernet0\|10.1.1.1/30` | Interface and IPs | Base: `vrf_name`, `proxy_arp`. IP sub: NULL:NULL sentinel |
| `LOOPBACK_INTERFACE` | `Loopback0` or `Loopback0\|10.0.0.1/32` | Loopback and IPs | NULL:NULL sentinel |
| `VRF` | `Vrf_CUST1` | VRF | `vni` (L3VNI, optional) |
| `STATIC_ROUTE` | `Vrf_CUST1\|10.0.0.0/24` | Static routes | `nexthop`, `ifname`, `distance` |

### 5.2 VXLAN/EVPN Tables

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `VXLAN_TUNNEL` | `vtep1` | VTEP source | `src_ip` |
| `VXLAN_TUNNEL_MAP` | `vtep1\|VNI{vni}_{target}` | VNI mapping | `vni`, `vlan` (L2VNI) or `vrf` (L3VNI) |
| `VXLAN_EVPN_NVO` | `nvo1` | EVPN NVO | `source_vtep` |
| `SUPPRESS_VLAN_NEIGH` | `Vlan100` | ARP suppression | `suppress` (`on`) |

### 5.3 BGP Tables

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `BGP_GLOBALS` | `default` or VRF name | Global BGP settings | `router_id`, `local_asn`, `load_balance_mp_relax`, `ebgp_requires_policy`, `default_ipv4_unicast`, `log_neighbor_changes`, `suppress_fib_pending` |
| `BGP_GLOBALS_AF` | `{vrf}\|{af}` | BGP address family | `advertise-all-vni`, `advertise_ipv4_unicast`, `max_ebgp_paths`, `route_target_import_evpn`, `route_target_export_evpn` |
| `BGP_NEIGHBOR` | `{vrf}\|{ip}` | BGP neighbor | `asn`, `local_addr`, `name`, `admin_status`, `holdtime`, `keepalive`, `peer_group`, `ebgp_multihop` |
| `BGP_NEIGHBOR_AF` | `{vrf}\|{ip}\|{af}` | Per-neighbor AF | `activate`, `next_hop_self`, `soft_reconfiguration`, `route_map_in`, `route_map_out`, `allowas_in`, `default_originate` |
| `BGP_EVPN_VNI` | `{vrf}\|{vni}` | Per-VNI EVPN | `rd`, `route_target_import`, `route_target_export` |
| `BGP_PEER_GROUP` | `SPINE_PEERS` | Peer group template | `asn`, `local_addr`, `admin_status`, `holdtime` |
| `BGP_PEER_GROUP_AF` | `SPINE_PEERS\|ipv4_unicast` | Per-AF peer group | `activate`, `route_map_in`, `route_map_out`, `next_hop_self` |
| `ROUTE_REDISTRIBUTE` | `{vrf}\|{src}\|{dst}\|{af}` | Redistribution | `route_map` |
| `ROUTE_MAP` | `{name}\|{seq}` | Route-map rule | `route_operation`, `match_prefix_set`, `set_local_pref`, `set_community`, `set_med` |
| `PREFIX_SET` | `{name}\|{seq}` | Prefix list entry | `ip_prefix`, `action`, `masklength_range` |
| `COMMUNITY_SET` | `{name}` | Community list | `set_type`, `match_action`, `community_member` |

**Address family values:** `ipv4_unicast`, `ipv6_unicast`, `l2vpn_evpn`.

### 5.4 ACL Tables

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `ACL_TABLE` | `customer-l3-in` | ACL table | `type` (`L3`, `L3V6`), `stage` (`ingress`, `egress`), `ports`, `policy_desc` |
| `ACL_RULE` | `customer-l3-in\|RULE_10` | ACL rule | `PRIORITY`, `PACKET_ACTION` (`FORWARD`, `DROP`), `SRC_IP`, `DST_IP`, `IP_PROTOCOL`, `L4_SRC_PORT`, `L4_DST_PORT`, `DSCP` |

### 5.5 QoS Tables

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `PORT_QOS_MAP` | `Ethernet0` | Port QoS binding | `dscp_to_tc_map`, `tc_to_queue_map` |
| `QUEUE` | `Ethernet0\|0` | Queue binding | `scheduler`, `wred_profile` |
| `SCHEDULER` | `scheduler.0` | Scheduler | `type` (`DWRR`, `STRICT`), `weight` |
| `WRED_PROFILE` | `WRED_GREEN` | WRED profile | `green_min_threshold`, `green_max_threshold`, `green_drop_probability`, `ecn` |
| `DSCP_TO_TC_MAP` | `DSCP_TO_TC` | DSCP → TC map | `{dscp_val}` → `{tc_val}` |
| `TC_TO_QUEUE_MAP` | `TC_TO_QUEUE` | TC → queue map | `{tc_val}` → `{queue_id}` |

### 5.6 Anycast Gateway

| Table | Key Format | Purpose | Fields |
|-------|-----------|---------|--------|
| `SAG` | `Vlan100\|IPv4` | Per-interface SAG | `gwip` |
| `SAG_GLOBAL` | `IPv4` | Global SAG MAC | `gwmac` |

### 5.7 Custom Table

| Table | Key Format | Purpose |
|-------|-----------|---------|
| `NEWTRON_SERVICE_BINDING` | Interface name (`Ethernet0`) | Service tracking |

**ServiceBinding fields:**

| Field | Purpose |
|-------|---------|
| `service_name` | Applied service name |
| `service_type` | Service type for teardown path |
| `ip_address` | Applied IP |
| `vrf_name` | Applied VRF |
| `vrf_type` | `"interface"` or `"shared"` |
| `ipvpn`, `macvpn` | VPN references |
| `l3vni`, `l3vni_vlan` | L3VNI values (for VRF teardown) |
| `l2vni` | L2VNI (for VXLAN_TUNNEL_MAP cleanup) |
| `ingress_acl`, `egress_acl` | ACL names |
| `bgp_neighbor`, `bgp_peer_as` | BGP peer (for RefreshService) |
| `qos_policy` | QoS policy name |
| `anycast_ip`, `anycast_mac` | Anycast values (for SVI/SAG cleanup) |
| `arp_suppression` | ARP suppression flag |
| `redistribute_vrf` | VRF where redistribution was overridden |
| `applied_at`, `applied_by` | Audit metadata |

The binding is self-sufficient for reverse operations — every value needed for teardown is stored in the binding, never re-resolved from specs.

## 6. Internal Implementation

Every write operation follows this path through the system:

```
┌──────────────┐     ┌─────────────────────┐     ┌───────────────┐              ┌────────────────────┐     ┌──────────────────────┐     ┌─────────────────────┐
│              │     │                     │     │               │              │                    │     │                      │     │                     │
│              │     │     middleware      │     │    handler    │              │                    │     │     Node.Execute     │     │        fn()         │
│ HTTP request │     │  recovery / logger  │     │ params, body, │              │     NodeActor      │     │ Lock > fn() > Commit │     │  op() > config fn   │
│              │     │ requestID / timeout │     │   ExecOpts    │              │ .connectAndExecute │     │   > Save > Unlock    │     │     > ChangeSet     │
│              │     │                     │     │               │  serialize   │                    │     │                      │     │ > CONFIG_DB entries │
│              │ ──▶ │                     │ ──▶ │               │ ───────────▶ │                    │ ──▶ │                      │ ──▶ │                     │
└──────────────┘     └─────────────────────┘     └───────────────┘              └────────────────────┘     └──────────────────────┘     └─────────────────────┘
```

Read operations follow the same path but use `connectAndRead`, which calls `Refresh()` (reload CONFIG_DB from Redis) instead of Lock/Execute. §6.3 includes a worked example tracing `CreateVLAN` through every layer.

### 6.1 ChangeSet

Operations compute changes without writing. The caller previews, then the server applies.

```go
type ChangeSet struct {
    Device       string
    Operation    string
    Timestamp    time.Time
    Changes      []Change
    AppliedCount int
    Verification *VerificationResult
}

func NewChangeSet(device, operation string) *ChangeSet
func (cs *ChangeSet) Add(table, key string, fields map[string]string)
func (cs *ChangeSet) Update(table, key string, fields map[string]string)
func (cs *ChangeSet) Delete(table, key string)
func (cs *ChangeSet) Adds(entries []sonic.Entry)      // batch
func (cs *ChangeSet) Deletes(entries []sonic.Entry)    // batch
func (cs *ChangeSet) Merge(other *ChangeSet)
func (cs *ChangeSet) IsEmpty() bool
func (cs *ChangeSet) Preview() string
func (cs *ChangeSet) Apply(n *Node) error
func (cs *ChangeSet) Verify(n *Node) error
```

**Preview format:** `+ TABLE|key field=value` (add), `- TABLE|key` (delete), `~ TABLE|key field: old→new` (modify).

**Apply:** Writes each change individually to CONFIG_DB via `ConfigDBClient.Set/Delete`. On failure at index N, `AppliedCount = N`. Already-written changes are NOT rolled back.

**Verify:** Opens a fresh Redis connection, re-reads every entry in the ChangeSet, diffs field-by-field. For keys with multiple operations (e.g., RefreshService: delete then add), only the final state is verified.

### 6.2 Entry

The unified CONFIG_DB entry type used by config functions and pipeline delivery:

```go
type Entry struct {
    Table  string
    Key    string
    Fields map[string]string  // nil = delete, empty = NULL:NULL sentinel
}
```

### 6.3 Config Function Pattern

Operations follow a three-layer pattern:

**Layer 1 — Config functions.** Pure functions in `*_ops.go` files that return `[]sonic.Entry`. No side effects, no preconditions, no Node access. Each function owns specific CONFIG_DB tables (§1 ownership map). Example: `createVlanConfig(vlanID int, opts VLANConfig) []sonic.Entry`.

**Layer 2 — `op()` helper.** Wraps the precondition → generate → ChangeSet pattern:

```go
func (n *Node) op(name, resource string, changeType sonic.ChangeType,
    checks func(*PreconditionChecker), gen func() []sonic.Entry) (*ChangeSet, error)
```

Runs preconditions (connected + locked + caller-supplied checks), calls the generator, wraps entries in a ChangeSet. In offline mode (abstract Node), also updates the shadow ConfigDB and accumulates entries for composite export.

**Layer 3 — Direct construction.** Complex operations (ApplyService, RemoveService, SetupEVPN) build ChangeSets directly, calling config functions from multiple owning files and merging results.

#### Worked Example: CreateVLAN

Trace of `POST /network/default/node/switch1/vlan {"id":100,"description":"Customer"}`:

```
handleCreateVLAN (handler_node.go)
  decodeJSON → VLANCreateRequest{ID:100, Description:"Customer"}
  execOpts(r) → ExecOpts{Execute:true}   (?dry_run absent → Execute=true)
  nodeActor.connectAndExecute(ctx, opts, fn)
    │
    │ [actor goroutine — serialized, one-at-a-time]
    │
    getNode(ctx) → cached *Node (or new SSH connection)
    node.Execute(ctx, opts, fn):
      Lock()
        Redis SETNX newtron:lock:switch1 (distributed lock)
        CONFIG_DB refresh → rebuild Interface map
      fn(ctx):
        n.CreateVLAN(ctx, 100, VLANConfig{Description:"Customer"})
          n.op("create-vlan", "Vlan100", ChangeAdd, checks, gen)
            checks(pc):
              vlanID ∈ [1,4094] ✓
              RequireVLANNotExists(100) ✓  (reads in-memory ConfigDB cache)
            gen():
              createVlanConfig(100, opts)
              → [{Table:"VLAN", Key:"Vlan100",
                  Fields:{"vlanid":"100", "description":"Customer"}}]
            buildChangeSet(entries, ChangeAdd)
          n.appendPending(cs)
      Commit():
        Apply:  redis-cli -n 4 HSET "VLAN|Vlan100" vlanid 100 description Customer
        Verify: fresh connection → HGETALL "VLAN|Vlan100" → fields match ✓
      Save(): SSH sudo config save -y
      Unlock(): release distributed lock
      → WriteResult{ChangeCount:1, Applied:true, Verified:true, Saved:true}
  writeJSON(w, 201, result)
    → {"data":{"change_count":1,"applied":true,"verified":true,"saved":true}}
```

The same `createVlanConfig` function runs in both online mode (through `op()` → ChangeSet → Redis) and offline mode (abstract Node building a composite). In offline mode, `op()` applies entries to the shadow ConfigDB so subsequent operations see prior state — same code path, different initialization.

### 6.4 PreconditionChecker

```go
type PreconditionChecker struct {
    node      *Node
    operation string
    resource  string
    errors    []error
}

func (p *PreconditionChecker) RequireConnected() *PreconditionChecker
func (p *PreconditionChecker) RequireLocked() *PreconditionChecker
func (p *PreconditionChecker) RequireVLANExists(id int) *PreconditionChecker
func (p *PreconditionChecker) RequireVLANNotExists(id int) *PreconditionChecker
func (p *PreconditionChecker) RequireVRFExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireVRFNotExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequirePortChannelExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequirePortChannelNotExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireVTEPConfigured() *PreconditionChecker
func (p *PreconditionChecker) RequireACLTableExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireInterfaceExists(name string) *PreconditionChecker
func (p *PreconditionChecker) RequireInterfaceNotPortChannelMember(name string) *PreconditionChecker
func (p *PreconditionChecker) Check(condition bool, precondition, details string) *PreconditionChecker
func (p *PreconditionChecker) Result() error
```

All checks read from the in-memory ConfigDB cache — no Redis round-trips. Multiple errors are collected and returned as `ValidationErrors`.

When `offline=true` (abstract Node), `RequireConnected` and `RequireLocked` are skipped.

### 6.5 DependencyChecker

Used by `RemoveService` to safely clean up shared resources. Scans CONFIG_DB for remaining consumers:

```go
type DependencyChecker struct {
    node             *Node
    excludeInterface string  // interface being removed
}

func (dc *DependencyChecker) IsLastACLUser(aclName string) bool
func (dc *DependencyChecker) GetACLRemainingInterfaces(aclName string) string
func (dc *DependencyChecker) IsLastVLANMember(vlanID int) bool
func (dc *DependencyChecker) IsLastServiceUser(serviceName string) bool
func (dc *DependencyChecker) IsLastIPVPNUser(ipvpnName string) bool
```

### 6.6 Node Execute Lifecycle

The public API method `Node.Execute()` wraps the full write lifecycle:

```
Lock
│  Acquires distributed Redis lock (SETNX with TTL)
│  Refreshes CONFIG_DB cache from Redis
│  Rebuilds Interface map from refreshed CONFIG_DB
│
├─ fn(ctx)
│    Caller's operations run here. Each op (CreateVLAN, ApplyService, etc.)
│    calls op() or builds a ChangeSet directly. ChangeSets append to n.pending.
│    No Redis writes yet — operations only compute changes.
│
├─ [dry-run: opts.Execute == false]
│    Returns WriteResult{Preview: cs.Preview(), ChangeCount: N}
│    Rollback() discards pending ChangeSets. No Commit, no Save.
│
├─ Commit
│    Apply: iterates each pending ChangeSet's Changes, writes each entry
│           to CONFIG_DB via Set/Delete. On failure at index N,
│           AppliedCount = N. Already-written changes are NOT rolled back.
│    Verify: fresh Redis connection, re-reads every entry, diffs field-by-field.
│            Keys with multiple ops (delete+add) — only final state verified.
│
├─ Save (unless opts.NoSave)
│    SSH: sudo config save -y
│
└─ Unlock
     Releases distributed Redis lock
```

On `fn()` error or Commit failure, `Rollback()` clears pending ChangeSets but does NOT undo already-applied Redis writes — partial writes are possible. The actor's `connectAndExecute` also calls `Rollback()` on error but keeps the cached connection alive for future requests.

### 6.7 Value Derivation

All values below are derived at `ApplyService` time and stored in `NEWTRON_SERVICE_BINDING` for teardown. Composite generation (abstract mode) derives identical values through the same code path. Auto-derived values from interface context:

| Value | Derivation |
|-------|-----------|
| VRF name (interface type) | `{service}-{shortenedInterface}` (e.g., `customer-l3-Eth0`) |
| VRF name (shared type) | ipvpn spec's VRF name |
| ACL table name | `{service}-{direction}` (e.g., `customer-l3-in`) |
| Neighbor IP (/31) | XOR last bit of local IP |
| Neighbor IP (/30) | XOR host bits (1→2, 2→1) |
| Router ID | Loopback IP from profile |
| VTEP source IP | Loopback IP from profile |

**Interface name normalization:**

| Short | Full (SONiC) |
|-------|-------------|
| `Eth0` | `Ethernet0` |
| `Po100` | `PortChannel100` |
| `Vl100` | `Vlan100` |
| `Lo0` | `Loopback0` |

### 6.8 Spec Resolution

Hierarchical resolution: profile > zone > global. `ResolvedSpecs` implements `node.SpecProvider`:

```go
type SpecProvider interface {
    GetService(name string) (*spec.ServiceSpec, error)
    GetIPVPN(name string) (*spec.IPVPNSpec, error)
    GetMACVPN(name string) (*spec.MACVPNSpec, error)
    GetFilter(name string) (*spec.FilterSpec, error)
    GetQoSPolicy(name string) (*spec.QoSPolicy, error)
    GetRoutePolicy(name string) (*spec.RoutePolicy, error)
    GetPrefixList(name string) ([]string, error)
}
```

Resolution is union with lower-level wins: if `"customer-l3"` exists at network and device levels, the device-level definition wins.

### 6.9 Composite Delivery

```go
type CompositeBuilder struct {
    tables   map[string]map[string]map[string]string
    metadata CompositeMetadata
}

func NewCompositeBuilder(deviceName string, mode CompositeMode) *CompositeBuilder
func (cb *CompositeBuilder) AddEntries(entries []sonic.Entry) *CompositeBuilder
func (cb *CompositeBuilder) AddEntry(table, key string, fields map[string]string) *CompositeBuilder
func (cb *CompositeBuilder) Build() *CompositeConfig
```

**Overwrite mode:** `ReplaceAll` — collects tables from composite, finds stale keys in CONFIG_DB, deletes stale keys, writes all composite entries via pipeline. Platform-managed tables (PORT) are merge-only.

**Merge mode:** Diffs each composite entry against current CONFIG_DB. Same key + same values = skip. Same key + different values = conflict error. New key = apply.

### 6.10 Middleware Chain

Registered in `handler.go`, applied outermost to innermost:

| Layer | Implementation | Purpose |
|-------|---------------|---------|
| `withRecovery` | `defer/recover` | Catches panics; logs stack trace, returns HTTP 500 |
| `withLogger` | `statusWriter` wrapper | Captures status code; logs `METHOD /path STATUS duration` |
| `withRequestID` | Atomic counter | Sets `X-Request-ID` header + request context value |
| `withTimeout` | `context.WithTimeout` | 5-minute request deadline on context |
| `ServeMux` | Go 1.22 pattern router | Method+path matching (`GET /network/{netID}/...`) |

Handlers extract path params via `r.PathValue("device")`, decode JSON bodies via `decodeJSON(r, &req)`, and read write options via `execOpts(r)` which maps `?dry_run=true` → `Execute: false` and `?no_save=true` → `NoSave: true`. Responses go through `writeJSON(w, status, data)` which wraps the result in `APIResponse{Data: data}`, or `writeError(w, err)` which maps Go errors to HTTP status codes (§3.9).

### 6.11 Actor Serialization

Each device gets a `NodeActor` — a single goroutine that serializes all operations to that device. The cached `*Node` (SSH connection) is only accessed from this goroutine, eliminating mutex contention:

```
┌──────────────────┐             ┌─────────────────┐                  ┌─────────────────┐           ┌──────────────────┐
│                  │             │                 │                  │                 │           │                  │
│ NodeActor.do(fn) │             │ request channel │                  │ actor goroutine │           │ response channel │
│                  │  sends fn   │                 │  one at a time   │  (executes fn)  │  result   │                  │
│                  │ ──────────▶ │                 │ ───────────────▶ │                 │ ────────▶ │                  │
└──────────────────┘             └─────────────────┘                  └─────────────────┘           └──────────────────┘
```

**Connection caching:** `getNode()` returns the cached `*Node` or creates a new SSH tunnel. After each operation completes, a 5-minute idle timer (`DefaultIdleTimeout`) resets. On timeout, the connection closes. On SSH failure mid-operation, the connection is dropped and the next request reconnects.

**Three access patterns:**

| Method | Use Case | Flow |
|--------|----------|------|
| `connectAndRead` | All GET handlers | `getNode → Refresh → fn(node)` — drops connection on Refresh failure |
| `connectAndExecute` | All write handlers | `getNode → node.Execute(opts, fn)` — calls Rollback on error, keeps connection |
| `connectAndLocked` | Composite deliver | `getNode → Lock → fn(node) → Unlock` — direct Redis writes outside ChangeSet model |

`connectAndRead` calls `Refresh()` to reload CONFIG_DB from Redis before reading, ensuring reads always reflect the latest device state. `connectAndExecute` delegates the full Lock/Commit/Save/Unlock lifecycle to `Node.Execute()` (§6.6). `connectAndLocked` is used for operations like `DeliverComposite` that write directly to Redis via pipeline without the ChangeSet model.

### 6.12 Shared Policy Objects

When services reference filters, route policies, or prefix lists, newtron
creates CONFIG_DB objects (ACL_TABLE, ROUTE_MAP, PREFIX_SET, COMMUNITY_SET)
that may be shared across multiple interfaces using the same service.

**Content-hashed naming.** Shared policy object keys include an 8-character
SHA256 hash of their generated CONFIG_DB fields:

```
ACL_TABLE|PROTECT_RE_IN_1ED5F2C7
ROUTE_MAP|CUSTOMER_IMPORT_A3B7C1D4|10
PREFIX_SET|RFC1918_E9F2A1B3
```

The hash is computed by `util.ContentHash()` from the entry's field values.
Dependent objects use bottom-up Merkle hashing: PREFIX_SET hashes are computed
first, then ROUTE_MAP entries reference real PREFIX_SET names (including hashes),
so a content change cascades through the hash chain.

**Lifecycle:** Created on first reference (first `ApplyService` using the
spec), deleted when the last consumer is removed (`RemoveService` scans
CONFIG_DB for remaining consumers). On spec change → hash change → blue-green
migration: new object created alongside old, interface migrated, old object
deleted if no remaining consumers.

**BGP peer groups.** Services with BGP routing create a `BGP_PEER_GROUP` named
after the service (not content-hashed — the group identity is the service name,
not its content). Peer group created on first apply, deleted when last consumer
removed.

**Ownership:** `service_ops.go` owns ROUTE_MAP, PREFIX_SET, COMMUNITY_SET.
`acl_ops.go` owns ACL_TABLE, ACL_RULE. `bgp_ops.go` owns BGP_PEER_GROUP,
BGP_PEER_GROUP_AF.

## 7. Permission System

Permission types are defined for future enforcement. Read operations are always allowed.

```go
type Permission string

const (
    PermServiceApply    Permission = "service.apply"
    PermServiceRemove   Permission = "service.remove"
    PermInterfaceModify Permission = "interface.modify"
    PermLAGCreate       Permission = "lag.create"
    PermLAGModify       Permission = "lag.modify"
    PermLAGDelete       Permission = "lag.delete"
    PermVLANCreate      Permission = "vlan.create"
    PermVLANModify      Permission = "vlan.modify"
    PermVLANDelete      Permission = "vlan.delete"
    PermACLModify       Permission = "acl.modify"
    PermEVPNModify      Permission = "evpn.modify"
    PermQoSCreate       Permission = "qos.create"
    PermQoSModify       Permission = "qos.modify"
    PermQoSDelete       Permission = "qos.delete"
    PermVRFCreate       Permission = "vrf.create"
    PermVRFModify       Permission = "vrf.modify"
    PermVRFDelete       Permission = "vrf.delete"
    PermDeviceCleanup   Permission = "device.cleanup"
    PermSpecAuthor      Permission = "spec.author"
    PermFilterCreate    Permission = "filter.create"
    PermFilterDelete    Permission = "filter.delete"
)
```

**Current status:** Permission types and spec-level definitions exist (`network.json` `permissions`, `ServiceSpec.Permissions`) but are not enforced at the HTTP layer. The server has no authentication middleware — it is designed for trusted-network deployment (localhost or VPN). Domain-level preconditions (connected, locked, VLAN exists, interface not already bound) are enforced via `PreconditionChecker` (§6.4). User-level authorization is planned for a future iteration.

**Intended resolution order:** superuser bypass → service-specific permissions → global permissions.

## 8. Audit Logging

Every write operation (execute mode, not dry-run) emits an `AuditEvent` to the audit log. The server writes events; the CLI's `audit list` command reads them.

```go
type AuditEvent struct {
    ID          string        `json:"id"`
    Timestamp   string        `json:"timestamp"`
    User        string        `json:"user"`
    Device      string        `json:"device"`
    Operation   string        `json:"operation"`
    Service     string        `json:"service,omitempty"`
    Interface   string        `json:"interface,omitempty"`
    Changes     []AuditChange `json:"changes"`
    Success     bool          `json:"success"`
    Error       string        `json:"error,omitempty"`
    ExecuteMode bool          `json:"execute_mode"`
    DryRun      bool          `json:"dry_run"`
    Duration    string        `json:"duration"`
    ClientIP    string        `json:"client_ip,omitempty"`
    SessionID   string        `json:"session_id,omitempty"`
}

type AuditChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   string            `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
}
```

Stored as JSON lines in the audit log file. Max size and rotation controlled by `UserSettings.AuditMaxSizeMB` and `AuditMaxBackups`.

## 9. CLI Command Reference

The CLI is an HTTP client using `pkg/newtron/client/`. All operations route through newtron-server.

### 9.1 Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--device` | `-D` | Target device (also positional first arg) |
| `--server` | — | Server URL (default `http://localhost:8080`, env `NEWTRON_SERVER`) |
| `--network-id` | `-N` | Network ID (default `default`, env `NEWTRON_NETWORK_ID`) |
| `--specs` | `-S` | Spec directory |
| `--verbose` | `-v` | Verbose output |
| `--json` | — | JSON output |

**Per noun-group:**

| Flag | Short | Description |
|------|-------|-------------|
| `--execute` | `-x` | Execute changes (default dry-run) |
| `--no-save` | — | Skip `config save -y` after execute (requires `-x`) |

### 9.2 Resource Nouns

| Noun | Write Actions | Read Actions |
|------|--------------|-------------|
| `interface` | `set <intf> <prop> <val>` | `list`, `show <intf>` |
| `vlan` | `create`, `delete`, `add-interface`, `remove-interface`, `configure-svi`, `bind-macvpn`, `unbind-macvpn` | `list`, `show <id>`, `status` |
| `vrf` | `create`, `delete`, `add-interface`, `remove-interface`, `bind-ipvpn`, `unbind-ipvpn`, `add-neighbor`, `remove-neighbor`, `add-route`, `remove-route` | `list`, `show <name>`, `status` |
| `lag` | `create`, `delete`, `add-interface`, `remove-interface` | `list`, `show <name>`, `status` |
| `evpn` | `setup` | `status` |
| `evpn ipvpn` | `create`, `delete` | `list`, `show <name>` |
| `evpn macvpn` | `create`, `delete` | `list`, `show <name>` |
| `bgp` | — | `status` |
| `qos` | `create`, `delete`, `add-queue`, `remove-queue`, `apply`, `remove` | `list`, `show <name>` |
| `filter` | `create`, `delete`, `add-rule`, `remove-rule` | `list`, `show <name>` |
| `service` | `create`, `delete`, `apply`, `remove`, `refresh` | `list`, `show <name>`, `get <intf>` |
| `acl` | `create`, `delete`, `add-rule`, `delete-rule`, `bind`, `unbind` | `list`, `show <name>` |

### 9.3 Device Operations

| Command | Description |
|---------|-------------|
| `show` | Device summary (info, interfaces, VLANs, VRFs) |
| `init [--force]` | Enable unified config mode (frrcfgd) for newtron management |
| `provision` | Generate + deliver composite from topology |
| `health` | Device health report |
| `device config-reload` | Reload CONFIG_DB from disk |
| `device save-config` | Persist runtime CONFIG_DB to disk |
| `device cleanup` | Remove orphaned ACLs/VRFs/VNI mappings |

### 9.4 Meta Commands

| Command | Description |
|---------|-------------|
| `settings list` | Show current settings |
| `settings set <key> <val>` | Set a setting |
| `settings get <key>` | Get a setting |
| `audit list` | Query audit log |
| `platform list` | List platforms |
| `platform show <name>` | Show platform details |

### 9.5 VRF Neighbor Auto-Derivation

| Subnet | Behavior |
|--------|----------|
| `/30` | Neighbor IP auto-derived (other host address) |
| `/31` | Neighbor IP auto-derived (RFC 3021) |
| `/29` or larger | `--neighbor-ip` required |

### 9.6 Service Immutability

Once applied, structural config is immutable. To change VRF, IP, ACLs, EVPN mappings, or QoS — remove and reapply.

Mutable (while service is bound): `admin-status`, `cost-in`, `cost-out`.

## 10. Testing

### 10.1 API Completeness Test

`TestAPICompleteness` in `pkg/newtron/api/api_test.go` uses `reflect` to enumerate every exported method on `*newtron.Network`, `*newtron.Node`, and `*newtron.Interface`. Each method must appear in either:

- **`coveredMethods`** — has a corresponding HTTP endpoint
- **`excludedMethods`** — intentionally not exposed (lifecycle/internal: Lock, Unlock, Close, Rollback, Name, IsAbstract, etc.) with a comment explaining why

Any method in neither set fails the test — this prevents new public methods from being added without HTTP endpoints.

### 10.2 Unit Tests

Pure computation tests with no external dependencies:

| Area | Function | Covers |
|------|----------|--------|
| IP math | `ComputeNeighborIP` | /30 and /31 neighbor derivation |
| Name normalization | `NormalizeInterfaceName` | Eth0→Ethernet0, Po100→PortChannel100 |
| Spec resolution | `buildResolvedSpecs` | Three-level merge: network → zone → node (lower wins) |
| ACL expansion | `createAclConfig` | Filter rules → ACL_TABLE + ACL_RULE entries |
| ChangeSet | `Preview` | Format for add/delete/modify operations |

### 10.3 E2E Tests

E2E testing is covered by newtrun test suites (see [newtrun HLD](../newtrun/hld.md)). Validated suites:

| Suite | Topology | Steps | Covers |
|-------|----------|-------|--------|
| `2node-ngdp-primitive` | 2node-ngdp | 20 | All service types, VLANs, VRFs, BGP, ACLs, health checks |
| `2node-ngdp-service` | 2node-ngdp-service | 6 | Provision → health → dataplane → deprovision → verify-clean |
| `3node-ngdp-dataplane` | 3node-ngdp | 6 | L3 routing, EVPN L2 IRB, cross-device traffic verification |
