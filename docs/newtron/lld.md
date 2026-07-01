# Newtron Low-Level Design (LLD)

This document covers **how** and **what fields** — type definitions, method signatures, CONFIG_DB schemas, HTTP API routes, and CLI command trees. For architecture and design rationale, see the [HLD](hld.md). For device-layer internals (SSH tunneling, Redis clients, write paths), see the [Device LLD](device-lld.md). For the full pipeline specification with end-to-end traces, see the [Unified Pipeline Architecture](unified-pipeline-architecture.md).

## 1. Package Structure

```
pkg/newtron/                          # Public API — all external consumers import this package only
    types.go                          # All public types (WriteResult, ExecOpts, DeviceInfo, etc.)
    network.go                        # Network wrapper
    node.go                           # Node wrapper
    interface.go                      # Interface wrapper
    spec_ops.go                       # Spec authoring operations
    platform_ops.go                   # Platform operations
    node_spec_ops.go                    # Node spec and zone operations
    audit.go                          # Audit log integration
    settings.go                       # UserSettings load/save
    settings/settings.go              # Settings file I/O
    audit/                            # Audit log writer, query, rotation
```

```
pkg/newtron/api/                      # HTTP server — actor model, JSON handlers, middleware
    server.go                         # Server struct, Start/Stop, Register/Unregister
    actors.go                         # networkEntity, NodeActor, connection caching
    handler.go                        # Route registration (buildMux), JSON helpers
    handler_node.go                   # Node operation handlers
    handler_network.go                # Network/spec operation handlers
    handler_interface.go              # Interface operation handlers
    middleware.go                     # Recovery, logging, request ID, timeout, mode
    mode.go                           # Topology vs actuated mode resolution
    types.go                          # HTTP request/response types, error mapping
    api_test.go                       # API completeness test
```

```
pkg/newtron/client/                   # HTTP client — used by CLI and newtrun
    client.go                         # Client struct, New(), HTTP helpers
    network.go                        # Spec read/write operations
    node.go                           # Node read + write operations
    interface.go                      # Interface-scoped operations
```

```
pkg/newtron/network/                  # Network internals — spec resolution, topology provisioning
    network.go                        # Network struct, spec accessors, getSpec[V] generic helper
    topology.go                       # TopologyProvisioner: BuildAbstractNode, SaveDeviceIntents
    resolved_specs.go                 # ResolvedSpecs (SpecProvider implementation)
```

```
pkg/newtron/network/node/             # Node internals — all operations live here

    # --- Core machinery ---
    node.go                           # Node struct, ConnectTransport, Lock/Unlock, RebuildProjection
    interface.go                      # Interface struct, read accessors
    changeset.go                      # ChangeSet: Add, Delete, Prepend, Merge, Apply, Verify
    precondition.go                   # PreconditionChecker (fluent builder)

    # --- Intent lifecycle ---
    intent_ops.go                     # writeIntent, deleteIntent, renderIntent
    reconstruct.go                    # IntentsToSteps, ReplayStep

    # --- Operations (intent-wrapping methods that call config generators) ---
    service_ops.go                    # ApplyService, RemoveService, RefreshService
    vlan_ops.go                       # CreateVLAN, DeleteVLAN, ConfigureIRB, UnconfigureIRB
    vrf_ops.go                        # CreateVRF, DeleteVRF, BindIPVPN, UnbindIPVPN, static routes
    bgp_ops.go                        # ConfigureBGP, AddBGPEVPNPeer, ConfigureRouteReflector
    evpn_ops.go                       # SetupVXLAN, TeardownVXLAN, BindMACVPN, UnbindMACVPN
    acl_ops.go                        # CreateACL, DeleteACL, AddACLRule, DeleteACLRule
    qos_ops.go                        # BindQoS, UnbindQoS (on Interface)
    interface_ops.go                  # ConfigureInterface, UnconfigureInterface, SetProperty, etc.
    interface_bgp_ops.go              # AddBGPPeer, RemoveBGPPeer (on Interface)
    baseline_ops.go                   # SetupDevice, ConfigureLoopback, RemoveLoopback
    portchannel_ops.go                # CreatePortChannel, DeletePortChannel, member management
    health_ops.go                     # CheckBGPSessions, CheckInterfaceOper

    # --- Config generators (pure functions: params → []sonic.Entry) ---
    service_gen.go                    # generateServiceEntries (spec → CONFIG_DB translation)
    service_config.go                 # Service-specific config: ROUTE_MAP, PREFIX_SET, COMMUNITY_SET
    vlan_config.go                    # VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL
    vrf_config.go                     # VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT
    bgp_config.go                     # BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF, BGP_PEER_GROUP, etc.
    vxlan_config.go                   # VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP
    acl_config.go                     # ACL_TABLE, ACL_RULE
    qos_config.go                     # PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, SCHEDULER, WRED_PROFILE
    qos_query.go                      # QoS reference counting (isQoSPolicyReferenced)
    interface_config.go               # INTERFACE table
    baseline_config.go                # LOOPBACK_INTERFACE, DEVICE_METADATA
    portchannel_config.go             # PORTCHANNEL, PORTCHANNEL_MEMBER
```

```
pkg/newtron/device/sonic/             # SONiC device layer — Redis clients, schema, wire format

    # --- Device and connection ---
    device.go                         # Device struct, SSH tunnel lifecycle
    types.go                          # Entry, ConfigChange, DriftEntry, VerificationResult, Intent

    # --- CONFIG_DB (projection + delivery) ---
    configdb.go                       # ConfigDB typed structs, ApplyEntries, ExportEntries, ExportRaw
    configdb_parsers.go               # configTableHydrators registry (33 typed + 9 merge parsers)
    configdb_diff.go                  # DiffConfigDB (projection vs actual comparison)
    configdb_order.go                 # CONFIG_DB write ordering (dependency-aware)
    pipeline.go                       # PipelineSet, ReplaceAll (Redis write paths)
    schema.go                         # YANG-derived schema validation (fail-closed)
    yang/constraints.md               # YANG source reference for schema constraints

    # --- Other Redis databases (read-only) ---
    statedb.go                        # StateDB client, health check queries
    statedb_parsers.go                # StateDB table parsers (13 entries)
    appldb.go                         # AppDB client (route reads)
    asicdb.go                         # AsicDB client (SAI object reads)
```

```
pkg/newtron/spec/                     # Spec types and file I/O
    types.go                          # All spec types (ServiceSpec, NodeSpec, TopologySpecFile, etc.)
    loader.go                         # Load/save network.json, profiles, platforms, topology
    schema_meta.go                    # FieldMeta + SchemaMeta + reflection-based tag extractor
    schema_registry.go                # init() registers every spec authoring kind for /schema endpoints
```

Field metadata for UIs (form labels, tooltips, enum lists, refs to other kinds,
client-side validation) lives as struct tags on the spec types themselves:

| Tag | Purpose |
|-----|---------|
| `label:` | Human form-field label |
| `tooltip:` | Hover/help text |
| `enum:` | Comma-separated allowed values for fixed-vocabulary fields |
| `ref:` | Names another spec kind (UI renders a dropdown of existing names) |
| `item_kind:` | Element kind for arrays/maps of objects (overrides reflect inference) |
| `pattern:` | Regex the value must match |
| `min:` / `max:` | Inclusive bounds for int fields |
| `format:` | Semantic hint — `cidr`, `ipv4`, `ipv6`, `mac`, `asn` |
| `immutable:"true"` | Value is fixed at create time; UI suppresses edit affordance |

The `/newtron/v1/schema` and `/newtron/v1/schema/{kind}` endpoints expose this
metadata plus per-kind URL templates (`SchemaPaths`) and identity metadata
(`Identifier`, `ParentRef`) registered in `schema_registry.go`. UIs consume the
combined document to drive every kind's CRUD without hardcoded mappings (§27).

`Identifier` names the field that addresses one row — usually `name` for
top-level kinds; `seq` / `queue_id` / `prefix` for sub-rules. For top-level
kinds and `QoSQueue`, the identifier doesn't appear on the spec struct (it
lives in the create-X request body or, for queues, the array position); the
registry supplies an `IdentifierField *FieldMeta` that the schema builder
prepends to the field list.

`ParentRef` (sub-rules only) names the wire field a sub-rule's request body
uses to identify its parent — e.g. `add-filter-rule` takes `{filter: "<name>",
...}`, so `FilterRule.ParentRef = "filter"`.

`SchemaPaths` carries List / Show / Create / Update / Delete URL templates
(with `{netID}` and `{name}` placeholders). Read-only kinds (PlatformSpec)
omit Create/Update/Delete; sub-rule kinds omit List/Show; PrefixListEntry
omits Update (per §47 the prefix IS the entry, no other mutable fields);
embedded-only kinds (RoutingSpec, RoutePolicySet, EVPNConfig) omit the entire
`paths` object via `json:"paths,omitzero"`.

Conditional-required predicates (`required_when`) live as Go literals on
`SchemaRegistration.RequiredWhen` — a `map[wireFieldName]*RequiredWhen`. The
shape is structured (atomic with `Field` + one of `Equals` / `NotEquals` /
`In` / `NotIn`, or combinator with `AllOf` / `AnyOf`), not a DSL string, so
every UI walks the same JSON tree without a parser. Newtron validates every
field reference against the sample struct at registration time and panics on
typos — a misspelled `Field: "servce_type"` fails server start, not silently
in the UI. The server does not evaluate `required_when` at request time; the
existing 400-on-missing-required behaviour is the back-stop. ServiceSpec
carries the canonical example (`ipvpn` required when `service_type` is
`evpn-irb` or `evpn-routed`; `macvpn` required when `service_type` is
`evpn-irb` or `evpn-bridged`).

```
cmd/newtron/                          # CLI — one file per noun, dispatched via commands map
    main.go                           # App struct, commands map, dispatch
    cmd_service.go   cmd_vlan.go      cmd_vrf.go       cmd_bgp.go
    cmd_evpn.go      cmd_acl.go       cmd_qos.go       cmd_interface.go
    cmd_lag.go       cmd_filter.go    cmd_intent.go     cmd_health.go
    cmd_show.go      cmd_init.go      cmd_profile.go    cmd_zone.go
    cmd_platform.go  cmd_settings.go  cmd_preferences.go cmd_audit.go
    cmd_device.go
```

### CONFIG_DB Table Ownership

Each CONFIG_DB table has exactly one owning file (see [HLD §4](hld.md) for rationale). Config generators in the owning file are the sole writers; composites (`service_gen.go`, `topology.go`) call these generators and merge ChangeSets.

| Owner | Tables |
|-------|--------|
| `vlan_config.go` | VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL |
| `vrf_config.go` | VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT |
| `bgp_config.go` | BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF, BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, DEVICE_METADATA, BGP_PEER_GROUP, BGP_PEER_GROUP_AF |
| `vxlan_config.go` | VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP, SUPPRESS_VLAN_NEIGH, BGP_EVPN_VNI |
| `acl_config.go` | ACL_TABLE, ACL_RULE |
| `qos_config.go` | PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE |
| `interface_config.go` | INTERFACE |
| `baseline_config.go` | LOOPBACK_INTERFACE |
| `portchannel_config.go` | PORTCHANNEL, PORTCHANNEL_MEMBER |
| `intent_ops.go` | NEWTRON_INTENT |
| `service_config.go` | ROUTE_MAP, PREFIX_SET, COMMUNITY_SET |

---

## 2. Spec File Types

Specs are the declarative layer — JSON files under `/etc/newtron/` (or the configured spec directory) that describe what the network should look like. Operations accept spec names and resolve them at runtime; callers never pre-resolve specs. For how specs participate in the hierarchical resolution chain, see [HLD §4.1](hld.md).

### 2.1 NetworkSpecFile

Top-level container loaded from `network.json`. Defines network-wide specs, zones, and access control.

```go
type NetworkSpecFile struct {
    Version     string                    `json:"version"`
    SuperUsers  []string                  `json:"super_users,omitempty"`
    UserGroups  map[string][]string       `json:"user_groups,omitempty"`
    Permissions map[string][]string       `json:"permissions,omitempty"`  // action → allowed groups
    Zones       map[string]*ZoneSpec      `json:"zones,omitempty"`
    OverridableSpecs                      // embedded: 7 spec maps
}
```

### 2.2 OverridableSpecs

Embedded in `NetworkSpecFile`, `ZoneSpec`, and `NodeSpec` to enable the three-level hierarchy (network → zone → node). Lower-level wins on name collision.

```go
type OverridableSpecs struct {
    PrefixLists   map[string][]string          `json:"prefix_lists,omitempty"`
    Filters       map[string]*FilterSpec       `json:"filters,omitempty"`
    QoSPolicies   map[string]*QoSPolicy        `json:"qos_policies,omitempty"`
    RoutePolicies map[string]*RoutePolicy       `json:"route_policies,omitempty"`
    IPVPNs        map[string]*IPVPNSpec         `json:"ipvpns,omitempty"`
    MACVPNs       map[string]*MACVPNSpec        `json:"macvpns,omitempty"`
    Services      map[string]*ServiceSpec       `json:"services,omitempty"`
}
```

### 2.3 ServiceSpec

The primary abstraction — bundles VPN, routing, filter, and QoS intent into a reusable template applied to interfaces. Service types span local and overlay use cases (see [HLD §4.2](hld.md) for the six service types and their requirements).

```go
type ServiceSpec struct {
    Description   string       `json:"description,omitempty"`
    ServiceType   string       `json:"service_type"`   // routed|bridged|irb|evpn-routed|evpn-bridged|evpn-irb
    IPVPN         string       `json:"ipvpn,omitempty"`
    MACVPN        string       `json:"macvpn,omitempty"`
    VRFType       string       `json:"vrf_type,omitempty"`       // "interface" (default) or "shared"
    Routing       *RoutingSpec `json:"routing,omitempty"`
    IngressFilter string       `json:"ingress_filter,omitempty"` // filter-spec reference
    EgressFilter  string       `json:"egress_filter,omitempty"`
    QoSPolicy     string       `json:"qos_policy,omitempty"`     // qos-policy reference
    Permissions   map[string][]string `json:"permissions,omitempty"` // action → allowed groups
}
```

**Service type → spec requirements:**

| Type | IPVPN | MACVPN | VRF | VLAN | Routing | VXLAN |
|------|-------|--------|-----|------|---------|-------|
| `routed` | — | — | per-interface | — | optional | — |
| `bridged` | — | — | — | at apply | — | — |
| `irb` | — | — | per-interface | at apply | optional | — |
| `evpn-routed` | required | — | from ipvpn | — | optional | L3VNI |
| `evpn-bridged` | — | required | — | from macvpn | — | L2VNI |
| `evpn-irb` | required | required | from ipvpn | from macvpn | optional | L2+L3 VNI |

### 2.4 RoutingSpec

Defines BGP routing parameters within a service. Used by `service_gen.go` to generate BGP_NEIGHBOR, BGP_PEER_GROUP, ROUTE_MAP, and PREFIX_SET entries.

```go
type RoutingSpec struct {
    Protocol         string `json:"protocol"`                    // "bgp"
    PeerAS           string `json:"peer_as,omitempty"`           // fixed ASN or "request"
    ImportPolicy     string `json:"import_policy,omitempty"`     // route-policy reference
    ExportPolicy     string `json:"export_policy,omitempty"`
    ImportCommunity  string `json:"import_community,omitempty"`
    ExportCommunity  string `json:"export_community,omitempty"`
    ImportPrefixList string `json:"import_prefix_list,omitempty"`
    ExportPrefixList string `json:"export_prefix_list,omitempty"`
    Redistribute     *bool  `json:"redistribute,omitempty"`      // connected route redistribution
}
```

When `PeerAS` is `"request"`, the caller must provide the peer AS at apply time via `ApplyServiceOpts.PeerAS`.

### 2.5 IPVPNSpec

IP-VPN definition for L3 overlay routing. The VRF name is derived from the IPVPN name at apply time (canonical uppercase).

```go
type IPVPNSpec struct {
    Description  string   `json:"description,omitempty"`
    VRF          string   `json:"vrf,omitempty"`           // explicit VRF name (rare)
    L3VNI        int      `json:"l3vni"`                   // L3 VNI for VXLAN tunnel
    L3VNIVlan    int      `json:"l3vni_vlan,omitempty"`    // transit VLAN for L3VNI
    RouteTargets []string `json:"route_targets,omitempty"` // import/export RTs
}
```

### 2.6 MACVPNSpec

MAC-VPN definition for L2 overlay bridging. `VlanID` is the local bridge domain; `VNI` is the VXLAN tunnel identifier.

```go
type MACVPNSpec struct {
    Description    string   `json:"description,omitempty"`
    VlanID         int      `json:"vlan_id"`                 // local bridge domain VLAN
    VNI            int      `json:"vni"`                     // L2 VNI for VXLAN tunnel
    AnycastIP      string   `json:"anycast_ip,omitempty"`    // SAG virtual IP
    AnycastMAC     string   `json:"anycast_mac,omitempty"`   // SAG virtual MAC
    RouteTargets   []string `json:"route_targets,omitempty"`
    ARPSuppression bool     `json:"arp_suppression,omitempty"`
}
```

### 2.7 FilterSpec

ACL filter definition. Rules are expanded into ACL_TABLE + ACL_RULE entries at apply time, with content-hashed table names for blue-green migration (see [HLD §4.2](hld.md) on shared policy objects).

```go
type FilterSpec struct {
    Description string        `json:"description,omitempty"`
    Type        string        `json:"type"`  // "ipv4" or "ipv6" (mapped to L3/L3V6 at CONFIG_DB boundary)
    Rules       []*FilterRule `json:"rules,omitempty"`
}

type FilterRule struct {
    Sequence      int    `json:"seq"`
    SrcPrefixList string `json:"src_prefix_list,omitempty"` // prefix-list reference (expanded)
    DstPrefixList string `json:"dst_prefix_list,omitempty"`
    SrcIP         string `json:"src_ip,omitempty"`
    DstIP         string `json:"dst_ip,omitempty"`
    Protocol      string `json:"protocol,omitempty"`
    SrcPort       string `json:"src_port,omitempty"`
    DstPort       string `json:"dst_port,omitempty"`
    DSCP          string `json:"dscp,omitempty"`
    Action        string `json:"action"`    // "permit" or "deny" (mapped to FORWARD/DROP at CONFIG_DB boundary)
    CoS           string `json:"cos,omitempty"`
}
```

### 2.8 QoSPolicy

Defines DSCP-to-queue mapping and scheduling. Bound to interfaces via `BindQoS`; generates DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, QUEUE, and PORT_QOS_MAP entries.

```go
type QoSPolicy struct {
    Description string      `json:"description,omitempty"`
    Queues      []*QoSQueue `json:"queues"`
}

type QoSQueue struct {
    Name   string `json:"name"`
    Type   string `json:"type"`             // "dwrr" or "strict" (mapped to SCHEDULER type at CONFIG_DB boundary)
    Weight int    `json:"weight,omitempty"` // WRR weight
    DSCP   []int  `json:"dscp,omitempty"`   // DSCP values mapped to this queue
    ECN    bool   `json:"ecn,omitempty"`    // enable WRED/ECN
}
```

### 2.9 RoutePolicy

Route policy definition for BGP import/export filtering. Generates ROUTE_MAP and PREFIX_SET entries with content-hashed names.

```go
type RoutePolicy struct {
    Description string             `json:"description,omitempty"`
    Rules       []*RoutePolicyRule `json:"rules,omitempty"`
}

type RoutePolicyRule struct {
    Sequence   int             `json:"seq"`
    Action     string          `json:"action"`                // "permit" or "deny"
    PrefixList string          `json:"prefix_list,omitempty"` // prefix-list reference
    Community  string          `json:"community,omitempty"`
    Set        *RoutePolicySet `json:"set,omitempty"`
}

type RoutePolicySet struct {
    LocalPref int    `json:"local_pref,omitempty"`
    Community string `json:"community,omitempty"`
    MED       int    `json:"med,omitempty"`
}
```

### 2.10 NodeSpec

Per-device configuration that combines identity (IPs, ASN, zone), connectivity (SSH), and EVPN peering into a single file. Lives in `nodes/{node}.json`.

```go
type NodeSpec struct {
    MgmtIP      string          `json:"mgmt_ip"`
    LoopbackIP  string          `json:"loopback_ip"`
    Zone        string          `json:"zone"`
    EVPN        *EVPNConfig     `json:"evpn,omitempty"`
    MAC         string          `json:"mac,omitempty"`
    Platform    string          `json:"platform"`
    SSHUser     string          `json:"ssh_user,omitempty"`
    SSHPass     string          `json:"ssh_pass,omitempty"`
    SSHPort     int             `json:"ssh_port,omitempty"`
    ConsolePort int             `json:"console_port,omitempty"`
    VMMemory    string          `json:"vm_memory,omitempty"`
    VMCPUs      int             `json:"vm_cpus,omitempty"`
    VMImage     string          `json:"vm_image,omitempty"`
    VMHost      string          `json:"vm_host,omitempty"`
    UnderlayASN int             `json:"underlay_asn"`
    HostIP      string          `json:"host_ip,omitempty"`
    HostGateway string          `json:"host_gateway,omitempty"`
    OverridableSpecs            // embedded: node-level spec overrides
}

type EVPNConfig struct {
    Peers          []string `json:"peers,omitempty"`
    RouteReflector bool     `json:"route_reflector,omitempty"`
    ClusterID      string   `json:"cluster_id,omitempty"`
}
```

At load time, the spec loader resolves profiles into `ResolvedNodeSpec` — a flattened snapshot with derived values (router ID from loopback, VTEP source IP, BGP neighbor ASNs from peer loopback lookups).

```go
type ResolvedNodeSpec struct {
    DeviceName      string
    MgmtIP          string
    LoopbackIP      string
    Zone            string
    Platform        string
    IsRouteReflector bool
    ClusterID       string
    RouterID        string           // derived from LoopbackIP
    VTEPSourceIP    string           // derived from LoopbackIP
    BGPNeighbors    []string         // EVPN peer loopback IPs
    BGPNeighborASNs map[string]int   // peer IP → ASN (from peer profile)
    MAC             string
    SSHUser, SSHPass string
    SSHPort, ConsolePort int
    UnderlayASN     int
}
```

### 2.11 PlatformSpec

Hardware type definition. Maps HWSKU to port layout, VM parameters, and feature support.

```go
type PlatformSpec struct {
    HWSKU               string        `json:"hwsku"`
    Description         string        `json:"description,omitempty"`
    DeviceType          string        `json:"device_type,omitempty"`
    PortCount           int           `json:"port_count"`
    DefaultSpeed        string        `json:"default_speed"`
    Breakouts           []string      `json:"breakouts,omitempty"`
    Ports               []PortSpec    `json:"ports,omitempty"` // name → nic_index inventory (newtlab/lld.md §5.3)
    VMImage             string        `json:"vm_image,omitempty"`
    VMMemory            string        `json:"vm_memory,omitempty"`
    VMCPUs              int           `json:"vm_cpus,omitempty"`
    VMNICDriver         string        `json:"vm_nic_driver,omitempty"`
    VMCPUFeatures       string        `json:"vm_cpu_features,omitempty"`
    VMCredentials       *VMCredentials `json:"vm_credentials,omitempty"`
    VMBootTimeout       int           `json:"vm_boot_timeout,omitempty"`
    VMImageRelease      string        `json:"vm_image_release,omitempty"`
    Dataplane           string        `json:"dataplane,omitempty"`
    UnsupportedFeatures []string      `json:"unsupported_features,omitempty"`
}
```

Feature dependency management: `GetAllFeatures()`, `GetFeatureDependencies(feature)`, and `GetUnsupportedDueTo(baseFeature)` provide the feature dependency graph. `PlatformSpec.SupportsFeature(feature)` checks both direct and transitive unsupported features.

### 2.12 TopologySpecFile

Defines the abstract topology — which devices exist, what ports they have, and what links connect them. Lives in `topologies/{name}/topology.json`.

```go
type TopologySpecFile struct {
    Version     string                          `json:"version"`
    Platform    string                          `json:"platform,omitempty"` // default platform
    Description string                          `json:"description,omitempty"`
    Devices     map[string]*TopologyNode      `json:"nodes"`
    Links       []TopologyLink                  `json:"links,omitempty"`
    NewtLab     *NewtLabConfig                  `json:"newtlab,omitempty"`
}

type TopologyNode struct {
    Steps []TopologyStep         `json:"steps,omitempty"`
    Ports map[string]*PortConfig `json:"ports,omitempty"` // keyed by port name
}

// PortConfig — operator-configurable PORT-table fields for one port, mirroring
// the YANG-derived PORT constraints (device/sonic/schema.go). Registered as the
// "PortConfig" schema kind so a universal UI renders the config form; converted
// to CONFIG_DB string fields at the boundary via Fields().
type PortConfig struct {
    AdminStatus string `json:"admin_status,omitempty"` // enum up,down
    MTU         int    `json:"mtu,omitempty"`          // 68..9216
    Speed       string `json:"speed,omitempty"`        // enum 1G..400G
    Description string `json:"description,omitempty"`
}

type TopologyStep struct {
    URL    string         `json:"url"`
    Params map[string]any `json:"params,omitempty"`
}

type TopologyLink struct {
    A string `json:"a"`  // "device:port"
    Z string `json:"z"`
}
```

Steps use the same URL format as the HTTP API (e.g., `/setup-device`, `/interfaces/Ethernet0/apply-service`). `IntentsToSteps` converts the flat NEWTRON_INTENT map back into this format for persistence.

---

## 3. Public API Types

All public types live in `pkg/newtron/types.go`. External consumers (CLI, newtrun, newtron-server HTTP handlers) import only `pkg/newtron/` — never internal packages. Public types use domain vocabulary; internal types reflect implementation. Boundary conversions strip implementation details.

### 3.1 Execution and Write Result

Used as request options and response types for all mutating operations (§4.6–4.8). `WriteResult` is the §46 wire-shape mirror of newtron's internal ChangeSet substrate — `Changes` is the canonical typed form (every `sonic.ConfigChange` the operation produced), `Preview` is the human-readable rendering of the same data, and `DeviceOps` records the per-device-operation outcomes captured during Apply and Verify.

```go
type ExecOpts struct {
    Execute bool   // true = apply to Redis; false = dry-run preview
    NoSave  bool   // skip config save after apply
}

type WriteResult struct {
    Preview      string                 `json:"preview,omitempty"`     // dry-run: ChangeSet preview text
    Changes      []sonic.ConfigChange   `json:"changes,omitempty"`     // §46 canonical substrate — every CONFIG_DB add/modify/delete
    DeviceOps    []sonic.DeviceOp `json:"device_ops,omitempty"`   // one entry per Redis HSET/DEL during Apply + one verify_read per Change
    ChangeCount  int                    `json:"change_count"`
    Applied      bool                   `json:"applied"`
    Verified     bool                   `json:"verified"`
    Saved        bool                   `json:"saved"`
    Verification *VerificationResult    `json:"verification,omitempty"` // set whenever verify ran (success or failure); absent on dry-run
}

type VerificationResult struct {
    Passed int                 `json:"passed"`
    Failed int                 `json:"failed"`
    Errors []VerificationError `json:"errors,omitempty"`
}

type VerificationError struct {
    Table          string `json:"table"`
    Key            string `json:"key"`
    Field          string `json:"field"`
    Expected       string `json:"expected"`
    Actual         string `json:"actual"`                     // "" if missing
    DeviceResponse string `json:"device_response,omitempty"`  // verbatim device-side reply at mismatch detection (§46)
}

```

`VerificationFailedError` and `ConflictError` are defined in §3.11 with the rest of the error types.

### 3.2 Device Info and Interface Views

Returned by node and interface read endpoints (§4.5).

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

Returned by resource-specific read endpoints (§4.5). Each type builds its response from intent records, not from the projection — consistent with the intent-first architecture.

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

type VRFDetail struct {
    Name         string             `json:"name"`
    L3VNI        int                `json:"l3_vni,omitempty"`
    Interfaces   []string           `json:"interfaces,omitempty"`
    BGPNeighbors []BGPNeighborEntry `json:"bgp_neighbors,omitempty"`
}

type VRFStatusEntry struct {
    Name       string `json:"name"`
    L3VNI      int    `json:"l3_vni,omitempty"`
    Interfaces int    `json:"interfaces"`
    State      string `json:"state,omitempty"`
}

type ACLTableSummary struct {
    Name       string `json:"name"`
    Type       string `json:"type"`
    Stage      string `json:"stage"`
    Interfaces string `json:"interfaces"`
    RuleCount  int    `json:"rule_count"`
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
```

### 3.4 Health and Drift Types

Returned by health check (§4.5) and intent drift endpoints (§4.7).

```go
type HealthReport struct {
    Device      string              `json:"device"`
    Status      string              `json:"status"`  // "pass", "warn", "fail"
    ConfigCheck *ConfigDriftResult  `json:"config_check,omitempty"`
    OperChecks  []HealthCheckResult `json:"oper_checks,omitempty"`
}

type HealthCheckResult struct {
    Check   string `json:"check"`   // "bgp", "interface-oper"
    Status  string `json:"status"`  // "pass", "warn", "fail"
    Message string `json:"message"`
}

type DriftEntry struct {
    Table    string            `json:"table"`
    Key      string            `json:"key"`
    Type     string            `json:"type"`  // "missing", "extra", "modified"
    Expected map[string]string `json:"expected,omitempty"`
    Actual   map[string]string `json:"actual,omitempty"`
}

type ReconcileResult struct {
    Mode     string `json:"mode"`               // "full" or "delta"
    Applied  int    `json:"applied"`             // total entries touched
    Missing  int    `json:"missing,omitempty"`   // entries added (delta only)
    Extra    int    `json:"extra,omitempty"`      // entries removed (delta only)
    Modified int    `json:"modified,omitempty"`  // entries corrected (delta only)
    Message  string `json:"message,omitempty"`
}
```

### 3.5 Route Types

Returned by routing observation endpoints (§4.5). These are building blocks — newtron provides the read; the caller decides correctness.

```go
type RouteEntry struct {
    Prefix   string         `json:"prefix"`
    VRF      string         `json:"vrf"`
    Protocol string         `json:"protocol"`
    NextHops []RouteNextHop `json:"next_hops,omitempty"`
    Source   string         `json:"source"`  // "APP_DB" or "ASIC_DB"
}

type RouteNextHop struct {
    Address   string `json:"address"`
    Interface string `json:"interface"`
}
```

### 3.6 Intent Types

Used by intent tree (§4.7) and the internal intent model. The `Intent` type is the fundamental unit — it binds a desired state to a device resource.

```go
type IntentTreeNode struct {
    Resource  string            `json:"resource"`
    Operation string            `json:"operation"`
    Params    map[string]string `json:"params,omitempty"`
    Children  []IntentTreeNode  `json:"children,omitempty"`
    Leaf      bool              `json:"leaf,omitempty"`
}

type Intent struct {
    Resource   string            `json:"resource"`
    Operation  string            `json:"operation"`
    Name       string            `json:"name,omitempty"`
    State      IntentState       `json:"state"`
    Parents    []string          `json:"parents,omitempty"`
    Children   []string          `json:"children,omitempty"`
    Params     map[string]string `json:"params,omitempty"`
    // ... lifecycle and audit fields
}

type IntentState string  // "unrealized", "in-flight", "actuated"
```

### 3.7 Config Request Types

Used as request bodies for node write operations (§4.6). Each type captures exactly the parameters the operation needs — no extra fields, no optional-but-ignored fields.

```go
type VLANConfig struct {
    VlanID      int
    Description string
    L2VNI       int
}

type VRFConfig struct {
    Name string
}

type InterfaceConfig struct {
    VRF    string  // routed mode
    IP     string  // IP in CIDR
    VLAN   int     // bridged mode
    Tagged bool    // tagged membership
}

type BGPNeighborConfig struct {
    VRF         string `json:"vrf,omitempty"`
    Interface   string `json:"interface,omitempty"`
    RemoteAS    int    `json:"remote_as,omitempty"`
    NeighborIP  string `json:"neighbor_ip,omitempty"`
    Description string `json:"description,omitempty"`
    Multihop    int    `json:"multihop,omitempty"`
}

type ApplyServiceOpts struct {
    IPAddress string            // IP for routed/IRB services
    VLAN      int               // VLAN ID for local types
    PeerAS    int               // BGP peer AS (when routing.peer_as="request")
    Params    map[string]string // topology params (peer_as, route_reflector_client, etc.)
}

type SetupDeviceOpts struct {
    Fields   map[string]string   `json:"fields,omitempty"`
    SourceIP string              `json:"source_ip,omitempty"`
    RR       *RouteReflectorOpts `json:"route_reflector,omitempty"`
}

type PortChannelConfig struct {
    Name     string
    Members  []string
    MinLinks int
    FastRate bool
    Fallback bool
    MTU      int
}

// ProjectionDiffRequest is the body for POST .../intent/projection-diff.
// Operations carry the same TopologyStep shape consumed by intent/save —
// the diff handler replays them on a snapshot of the projection without
// touching the device, returning before/after RawConfigDB plus the entry-
// level delta as `sonic.DriftEntry` (canonical §11 vocab).
type ProjectionDiffRequest struct {
    Operations []spec.TopologyStep `json:"operations"`
}
```

### 3.8 Spec Authoring Requests

Used as request bodies for network spec write operations (§4.3).

```go
type CreateServiceRequest struct {
    Name          string                `json:"name"`
    Type          string                `json:"type"`
    IPVPN         string                `json:"ipvpn,omitempty"`
    MACVPN        string                `json:"macvpn,omitempty"`
    VRFType       string                `json:"vrf_type,omitempty"`
    QoSPolicy     string                `json:"qos_policy,omitempty"`
    IngressFilter string                `json:"ingress_filter,omitempty"`
    EgressFilter  string                `json:"egress_filter,omitempty"`
    Description   string                `json:"description,omitempty"`
    Routing       *CreateServiceRouting `json:"routing,omitempty"`
}

type CreateIPVPNRequest struct {
    Name         string   `json:"name"`
    L3VNI        int      `json:"l3vni"`
    VRF          string   `json:"vrf,omitempty"`
    RouteTargets []string `json:"route_targets,omitempty"`
    Description  string   `json:"description,omitempty"`
}

type CreateMACVPNRequest struct {
    Name           string   `json:"name"`
    VNI            int      `json:"vni"`
    VlanID         int      `json:"vlan_id,omitempty"`
    AnycastIP      string   `json:"anycast_ip,omitempty"`
    AnycastMAC     string   `json:"anycast_mac,omitempty"`
    RouteTargets   []string `json:"route_targets,omitempty"`
    ARPSuppression bool     `json:"arp_suppression,omitempty"`
    Description    string   `json:"description,omitempty"`
}

type CreateFilterRequest struct {
    Name        string `json:"name"`
    Type        string `json:"type"`
    Description string `json:"description,omitempty"`
}

type CreateQoSPolicyRequest struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
}

type CreateRoutePolicyRequest struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
}

type CreatePrefixListRequest struct {
    Name     string   `json:"name"`
    Prefixes []string `json:"prefixes,omitempty"`
}

type CreateDeviceProfileRequest struct {
    Name        string                  `json:"name"`
    MgmtIP      string                  `json:"mgmt_ip"`
    LoopbackIP  string                  `json:"loopback_ip"`
    Zone        string                  `json:"zone"`
    Platform    string                  `json:"platform,omitempty"`
    UnderlayASN int                     `json:"underlay_asn,omitempty"`
    SSHUser     string                  `json:"ssh_user,omitempty"`
    SSHPass     string                  `json:"ssh_pass,omitempty"`
    SSHPort     int                     `json:"ssh_port,omitempty"`
    EVPN        *CreateEVPNConfigRequest `json:"evpn,omitempty"`
}
```

### 3.9 Spec Detail Response Types

Returned by spec read endpoints (§4.2). API views of spec objects — they expose the fields relevant to consumers without leaking internal representation.

```go
type ServiceDetail struct {
    Name          string `json:"name"`
    Description   string `json:"description,omitempty"`
    ServiceType   string `json:"service_type"`
    IPVPN         string `json:"ipvpn,omitempty"`
    MACVPN        string `json:"macvpn,omitempty"`
    VRFType       string `json:"vrf_type,omitempty"`
    QoSPolicy     string `json:"qos_policy,omitempty"`
    IngressFilter string `json:"ingress_filter,omitempty"`
    EgressFilter  string `json:"egress_filter,omitempty"`
}

type IPVPNDetail struct {
    Name         string   `json:"name"`
    Description  string   `json:"description,omitempty"`
    VRF          string   `json:"vrf"`
    L3VNI        int      `json:"l3vni"`
    RouteTargets []string `json:"route_targets"`
}

type MACVPNDetail struct {
    Name           string   `json:"name"`
    Description    string   `json:"description,omitempty"`
    AnycastIP      string   `json:"anycast_ip,omitempty"`
    AnycastMAC     string   `json:"anycast_mac,omitempty"`
    VNI            int      `json:"vni"`
    VlanID         int      `json:"vlan_id"`
    RouteTargets   []string `json:"route_targets,omitempty"`
    ARPSuppression bool     `json:"arp_suppression,omitempty"`
}

type PlatformDetail struct {
    Name                string   `json:"name"`
    HWSKU               string   `json:"hwsku"`
    Description         string   `json:"description,omitempty"`
    DeviceType          string   `json:"device_type,omitempty"`
    Dataplane           string   `json:"dataplane,omitempty"`
    DefaultSpeed        string   `json:"default_speed"`
    PortCount           int      `json:"port_count"`
    UnsupportedFeatures []string `json:"unsupported_features,omitempty"`
}

type DeviceProfileDetail struct {
    Name        string      `json:"name"`
    MgmtIP      string      `json:"mgmt_ip"`
    LoopbackIP  string      `json:"loopback_ip"`
    Zone        string      `json:"zone"`
    Platform    string      `json:"platform,omitempty"`
    UnderlayASN int         `json:"underlay_asn,omitempty"`
    EVPN        *EVPNDetail `json:"evpn,omitempty"`
}
```

### 3.10 Settings and Audit Types

```go
type UserSettings struct {
    DefaultNetwork string `json:"default_network,omitempty"`
    Dir            string `json:"dir,omitempty"`
    ServerURL      string `json:"server_url,omitempty"`
    NetworkID      string `json:"network_id,omitempty"`
    // ... other fields
    // (No audit settings: audit is per-network and server-side —
    //  enabled by --audit on cmd/newt-server, stored in each network's
    //  own folder. The CLI reads it via the server's per-network
    //  endpoints, so it needs no client-side audit path/rotation.)
}

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
}
```

### 3.11 Error Types

Returned by handlers when operations fail. The middleware maps these to HTTP status codes (see §4).

```go
type NotFoundError struct {
    Resource string
    Name     string
}  // → 404

type ValidationError struct {
    Field   string
    Message string
}  // → 400

// VerificationFailedError is returned by Node.Commit when post-apply verify
// detected a mismatch. Result is the typed WriteResult — including DeviceOps,
// Verification.Errors (with DeviceResponse), and Changes — so the 409 wire
// envelope surfaces the full substrate, not just a stringified summary (§46).
type VerificationFailedError struct {
    Device  string
    Passed  int
    Failed  int
    Total   int
    Message string
    Result  *WriteResult
}  // → 409 Conflict, with Result as the envelope's `data` field

// ConflictError indicates a mutation refused due to references from other
// entities (e.g., deleting a profile with topology devices still bound to
// it). Per DESIGN_PRINCIPLES §15, cascade is explicit: callers pass
// `force=true` to override. Re-exported as a type alias from `pkg/util` so
// the internal network layer and the public API share one type.
type ConflictError = util.ConflictError
```

**HTTP status mapping** (`httpStatusFromError` in `api/types.go`):

| Error Type | HTTP Status |
|-----------|-------------|
| `NotFoundError` | 404 Not Found |
| `ValidationError` | 400 Bad Request |
| `VerificationFailedError` | 409 Conflict (envelope `data` is the typed `WriteResult`) |
| `ConflictError` | 409 Conflict (e.g., delete refused on remaining references) |
| `notRegisteredError` | 404 Not Found |
| `alreadyRegisteredError` | 409 Conflict |
| `context.DeadlineExceeded` | 504 Gateway Timeout |
| all others | 500 Internal Server Error |

---

## 4. HTTP API Reference

All routes follow the pattern `/networks/{netID}/...` for spec operations and `/networks/{netID}/nodes/{node}/...` for device operations. The `?mode=topology` query parameter selects topology mode (offline abstract node); default is actuated mode (online device). Write operations use `?execute=true` to apply (default is dry-run preview).

Middleware chain (outer → inner): `withRecovery` → `withLogger` → `withRequestID` → `withTimeout(5min)` → `withMode` → mux.

### 4.1 Server Management

| Method | Path | Handler | Purpose |
|--------|------|---------|---------|
| POST | `/network` | `handleRegisterNetwork` | Register a network (loads spec dir) |
| GET | `/network` | `handleListNetworks` | List registered networks |
| POST | `/networks/{netID}/unregister` | `handleUnregisterNetwork` | Unregister a network |
| POST | `/networks/{netID}/reload` | `handleReloadNetwork` | Reload network specs from disk |

### 4.2 Network Spec Reads

List/show pairs for all spec types. Response types from §3.9.

| Method | Path | Response Type |
|--------|------|---------------|
| GET | `/networks/{netID}/services` | `[]ServiceDetail` |
| GET | `/networks/{netID}/services/{name}` | `ServiceDetail` |
| GET | `/networks/{netID}/services/{name}/projection` | `map[string]RawConfigDB` (per-Node projection slice the service contributes, replay-diff) |
| GET | `/networks/{netID}/ipvpns` | `[]IPVPNDetail` |
| GET | `/networks/{netID}/ipvpns/{name}` | `IPVPNDetail` |
| GET | `/networks/{netID}/macvpns` | `[]MACVPNDetail` |
| GET | `/networks/{netID}/macvpns/{name}` | `MACVPNDetail` |
| GET | `/networks/{netID}/qos-policies` | `[]QoSPolicyDetail` |
| GET | `/networks/{netID}/qos-policies/{name}` | `QoSPolicyDetail` |
| GET | `/networks/{netID}/filters` | `[]FilterDetail` |
| GET | `/networks/{netID}/filters/{name}` | `FilterDetail` |
| GET | `/networks/{netID}/route-policies` | `[]RoutePolicyDetail` |
| GET | `/networks/{netID}/route-policies/{name}` | `RoutePolicyDetail` |
| GET | `/networks/{netID}/prefix-lists` | `[]PrefixListDetail` |
| GET | `/networks/{netID}/prefix-lists/{name}` | `PrefixListDetail` |
| GET | `/networks/{netID}/platforms` | `[]PlatformDetail` |
| GET | `/networks/{netID}/platforms/{name}` | `PlatformDetail` |
| GET | `/networks/{netID}/profiles` | `[]DeviceProfileDetail` |
| GET | `/networks/{netID}/nodes/{name}` | `DeviceProfileDetail` |
| GET | `/networks/{netID}/zones` | `[]ZoneDetail` |
| GET | `/networks/{netID}/zones/{name}` | `ZoneDetail` |
| GET | `/networks/{netID}/nodes/{node}/host-connection` | `HostConnection` |
| GET | `/networks/{netID}/topology` | `TopologySpecFile` (full topology — devices, links, metadata) |
| GET | `/networks/{netID}/topology/nodes` | `[]string` (device names) |
| GET | `/networks/{netID}/features` | Feature list |
| GET | `/networks/{netID}/features/{name}/dependency` | Feature dependencies |
| GET | `/networks/{netID}/features/{name}/unsupported-due-to` | Transitive unsupported |
| GET | `/networks/{netID}/platforms/{name}/supports/{feature}` | `bool` |

### 4.3 Network Spec Writes

RPC-style POST endpoints. Each creates, updates, or deletes a spec object (or one of its sub-collection items) and persists to disk. Request types from §3.8.

> **Ground truth is `pkg/newtron/api/handler.go` (route registration) + `pkg/newtron/api/handler_network.go` (decode targets).** The table below is a snapshot updated as endpoints are added; if it disagrees with the handler source, the handler wins.
>
> Two patterns appear in the Update verbs:
> - Full-replacement Updates on top-level specs (the **#152 family**) reuse their `Create*Request` type — the request body fully replaces the prior spec under the same name. The handler decodes the Create type even though the action is Update.
> - Per-item Updates on sub-collections (rules, queues, entries) use a dedicated `Update*Request` type that adds an optional `new_seq` / `new_queue_id` / required `new_prefix` field for renumbering or value-swap.

**Scoped writes.** Every write request body below embeds `ScopeSelector`
(`scope` + `scope_instance`; absent ⇒ network) and the `delete-`/`remove-` verbs
carry the same two fields, so any overridable kind or sub-rule can be authored at
network, zone, or node scope — "flat at the boundary, hierarchical underneath."
Internal write methods take leading `(scope, instance string)` and route to the
target container via `withWriteTarget` (network/zone → `network.json` under
`keyNetworkSpec`; node → `nodes/<name>.json` via `loader.MutateProfile`, which
reads raw-from-disk so secret-resolved values are never written back). The
**network-floor invariant** (DESIGN_PRINCIPLES_NEWTRON §7) governs integrity: an
override requires a network base, so forward ref checks stay network-scoped,
override deletes are free, and network-base / container deletes are refused while
referenced or overridden below. Scope is also read-only provenance on
`GET /spec-instances` (`[]SpecInstance{kind, name, scope, scope_instance}`) and is
declared in the schema (`scope` enum + scope-conditional `scope_instance` ref via
`ref_when`).

| Method | Path | Request Type |
|--------|------|-------------|
| POST | `.../create-service` | `CreateServiceRequest` |
| POST | `.../update-service` | `CreateServiceRequest` (full-replacement; #152) |
| POST | `.../delete-service` | `{name}` |
| POST | `.../create-ipvpn` | `CreateIPVPNRequest` |
| POST | `.../update-ipvpn` | `CreateIPVPNRequest` (full-replacement; #152) |
| POST | `.../delete-ipvpn` | `{name}` |
| POST | `.../create-macvpn` | `CreateMACVPNRequest` |
| POST | `.../update-macvpn` | `CreateMACVPNRequest` (full-replacement; #152) |
| POST | `.../delete-macvpn` | `{name}` |
| POST | `.../create-qos-policy` | `CreateQoSPolicyRequest` |
| POST | `.../update-qos-policy` | `CreateQoSPolicyRequest` (full-replacement; preserves `queues` sub-collection — see §5 of `api.md`; #152) |
| POST | `.../delete-qos-policy` | `{name}` |
| POST | `.../add-qos-queue` | `AddQoSQueueRequest` |
| POST | `.../update-qos-queue` | `UpdateQoSQueueRequest` — atomic per-queue mutation; optional `new_queue_id` renumbers (#211) |
| POST | `.../remove-qos-queue` | `{policy, queue_id}` |
| POST | `.../create-filter` | `CreateFilterRequest` |
| POST | `.../update-filter` | `CreateFilterRequest` (full-replacement; preserves `rules` sub-collection; #152) |
| POST | `.../delete-filter` | `{name}` |
| POST | `.../add-filter-rule` | `AddFilterRuleRequest` |
| POST | `.../update-filter-rule` | `UpdateFilterRuleRequest` — atomic per-rule mutation; optional `new_seq` renumbers (#209) |
| POST | `.../remove-filter-rule` | `{filter, seq}` |
| POST | `.../create-prefix-list` | `CreatePrefixListRequest` |
| POST | `.../update-prefix-list` | `CreatePrefixListRequest` (full-replacement; `prefixes` is in the request shape so Update replaces it; #152) |
| POST | `.../delete-prefix-list` | `{name}` |
| POST | `.../add-prefix-list-entry` | `AddPrefixListEntryRequest` |
| POST | `.../remove-prefix-list-entry` | `{prefix_list, prefix}` |
| POST | `.../create-route-policy` | `CreateRoutePolicyRequest` |
| POST | `.../update-route-policy` | `CreateRoutePolicyRequest` (full-replacement; preserves `rules` sub-collection; #152) |
| POST | `.../delete-route-policy` | `{name}` |
| POST | `.../add-route-policy-rule` | `AddRoutePolicyRuleRequest` |
| POST | `.../update-route-policy-rule` | `UpdateRoutePolicyRuleRequest` — atomic per-rule mutation; optional `new_seq` renumbers (#210) |
| POST | `.../remove-route-policy-rule` | `{policy, seq}` |
| POST | `.../create-node` | `CreateDeviceProfileRequest` |
| POST | `.../update-node` | `CreateDeviceProfileRequest` (full-replacement; #152) |
| POST | `.../delete-node` | `{name, force}` — `force=true` cascades through topology devices referencing the profile (§15 cascade-refusal pattern); default refuses with 409 `ConflictError` |
| POST | `.../create-zone` | `CreateZoneRequest` |
| POST | `.../update-zone` | `CreateZoneRequest` (full-replacement; #152) |
| POST | `.../delete-zone` | `{name}` |
| POST | `.../topology/create-node` | `TopologyNodeCreateRequest` — creates topology device (auto-creates matching profile by filename) |
| DELETE | `.../topology/nodes/{name}` | `?force=true` to cascade-delete the matching profile + remove links wired to this device; default refuses if links remain (409) |
| PUT | `.../topology/nodes/{name}` | `TopologyNode` body — replaces device metadata; profile-update cascade enforces single-source-of-truth |
| POST | `.../topology/create-link` | `*TopologyLink` body — adds link to topology |
| DELETE | `.../topology/links/{node}/{interface}` | Removes link by single-endpoint identification (a port participates in at most one link) |

All paths above are prefixed with `/networks/{netID}`.

### 4.4 Device Init

| Method | Path | Purpose |
|--------|------|---------|
| POST | `.../nodes/{node}/init-device` | Initialize device (write DEVICE_METADATA, restart bgp, config save) |

### 4.5 Node Reads

Response types from §3.2–3.5. These dispatch via `connectAndRead` — the actor calls `RebuildProjection` → Ping → fn.

| Method | Path | Response Type |
|--------|------|---------------|
| GET | `.../nodes/{node}/info` | `DeviceInfo` |
| GET | `.../nodes/{node}/interfaces` | `[]InterfaceSummary` |
| GET | `.../nodes/{node}/interfaces/{name}` | `InterfaceDetail` |
| GET | `.../nodes/{node}/interfaces/{name}/binding` | `ServiceBindingDetail` |
| GET | `.../nodes/{node}/vlans` | `[]VLANStatusEntry` |
| GET | `.../nodes/{node}/vlans/{id}` | `VLANStatusEntry` |
| GET | `.../nodes/{node}/vrfs` | `[]VRFStatusEntry` |
| GET | `.../nodes/{node}/vrfs/{name}` | `VRFDetail` |
| GET | `.../nodes/{node}/acls` | `[]ACLTableSummary` |
| GET | `.../nodes/{node}/acls/{name}` | `ACLTableDetail` |
| GET | `.../nodes/{node}/bgp/status` | `BGPStatusResult` |
| GET | `.../nodes/{node}/bgp/check` | `[]HealthCheckResult` |
| GET | `.../nodes/{node}/evpn/status` | `EVPNStatusResult` |
| GET | `.../nodes/{node}/health` | `HealthReport` |
| GET | `.../nodes/{node}/lags` | `[]LAGStatusEntry` |
| GET | `.../nodes/{node}/lags/{name}` | `LAGStatusEntry` |
| GET | `.../nodes/{node}/neighbors` | `[]NeighEntry` |
| GET | `.../nodes/{node}/routes/{vrf}/{prefix...}` | `RouteEntry` |
| GET | `.../nodes/{node}/routes-asic/{prefix...}` | `RouteEntry` |
| GET | `.../nodes/{node}/configdb` | `sonic.RawConfigDB` — single internally-consistent CONFIG_DB snapshot (one round-trip per table). `?owned_only=false` returns every schema-known table (§46) |
| GET | `.../nodes/{node}/configdb/{table}` | `[]string` (keys) |
| GET | `.../nodes/{node}/configdb/{table}/{key}` | `map[string]string` |
| GET | `.../nodes/{node}/configdb/{table}/{key}/exists` | `{exists: bool}` |
| GET | `.../nodes/{node}/statedb/{table}/{key}` | `map[string]string` |

All paths prefixed with `/networks/{netID}`.

### 4.6 Node Writes

Dispatch via `connectAndExecute` — the actor calls `RebuildProjection` → `Execute(Lock → fn → Commit/Restore → Unlock)`. Response: `WriteResult`. Request types from §3.7.

| Method | Path | Operation |
|--------|------|-----------|
| POST | `.../nodes/{node}/setup-device` | `SetupDevice` — metadata + loopback + BGP + VTEP |
| POST | `.../nodes/{node}/create-vlan` | `CreateVLAN` |
| POST | `.../nodes/{node}/delete-vlan` | `DeleteVLAN` |
| POST | `.../nodes/{node}/configure-irb` | `ConfigureIRB` |
| POST | `.../nodes/{node}/unconfigure-irb` | `UnconfigureIRB` |
| POST | `.../nodes/{node}/create-vrf` | `CreateVRF` |
| POST | `.../nodes/{node}/delete-vrf` | `DeleteVRF` (cascading destroy) |
| POST | `.../nodes/{node}/bind-ipvpn` | `BindIPVPN` |
| POST | `.../nodes/{node}/unbind-ipvpn` | `UnbindIPVPN` |
| POST | `.../nodes/{node}/add-static-route` | `AddStaticRoute` |
| POST | `.../nodes/{node}/update-static-route` | `UpdateStaticRoute` — atomic per-route field mutation; key (vrf, prefix) is immutable (§47, #227) |
| POST | `.../nodes/{node}/remove-static-route` | `RemoveStaticRoute` |
| POST | `.../nodes/{node}/create-acl` | `CreateACL` |
| POST | `.../nodes/{node}/delete-acl` | `DeleteACL` |
| POST | `.../nodes/{node}/add-acl-rule` | `AddACLRule` |
| POST | `.../nodes/{node}/update-acl-rule` | `UpdateACLRule` — atomic per-rule field mutation; key (table, rule_name) is immutable (§47, #227) |
| POST | `.../nodes/{node}/remove-acl-rule` | `DeleteACLRule` |
| POST | `.../nodes/{node}/create-portchannel` | `CreatePortChannel` |
| POST | `.../nodes/{node}/delete-portchannel` | `DeletePortChannel` |
| POST | `.../nodes/{node}/add-portchannel-member` | `AddPortChannelMember` |
| POST | `.../nodes/{node}/remove-portchannel-member` | `RemovePortChannelMember` |
| POST | `.../nodes/{node}/bind-macvpn` | `BindMACVPN` |
| POST | `.../nodes/{node}/unbind-macvpn` | `UnbindMACVPN` |
| POST | `.../nodes/{node}/add-bgp-evpn-peer` | `AddBGPEVPNPeer` |
| POST | `.../nodes/{node}/update-bgp-evpn-peer` | `UpdateBGPEVPNPeer` — atomic per-overlay-peer field mutation; key (default, neighbor_ip) is immutable (§47, #227) |
| POST | `.../nodes/{node}/remove-bgp-evpn-peer` | `RemoveBGPEVPNPeer` |
| POST | `.../nodes/{node}/reload-config` | `ConfigReload` (SONiC config reload) |
| POST | `.../nodes/{node}/save-config` | `SaveConfig` (SONiC config save) |
| POST | `.../nodes/{node}/restart-daemon` | `RestartService` |
| POST | `.../nodes/{node}/ssh-command` | SSH command execution |

### 4.7 Intent Operations

Operations on the expected state. These operate on the abstract node's intent DB and projection (see [HLD §3.4](hld.md)).

| Method | Path | Response | Purpose |
|--------|------|----------|---------|
| GET | `.../nodes/{node}/intent/projection` | `sonic.RawConfigDB` | Per-Node projection (from intent replay). The decision substrate for newtron-owned tables |
| POST | `.../nodes/{node}/intent/projection-diff` | `{before, after, diff}` | Pre-commit preview: replays a `ProjectionDiffRequest.Operations` set on a snapshot of the projection without touching the device. `diff` is `[]sonic.DriftEntry` (canonical §11 vocab) |
| GET | `.../nodes/{node}/intent/tree` | `IntentTreeNode` | Read intent DAG |
| GET | `.../nodes/{node}/intent/drift` | `[]DriftEntry` | Compare projection vs device CONFIG_DB; empty array ≡ all newtron writes actualized |
| POST | `.../nodes/{node}/intent/reconcile` | `ReconcileResult` | Push projection to device. Query params: `mode=topology` (intent source), `reconcile=full\|delta` (delivery mechanism, default: topology→full, actuated→delta), `dry_run=true`, `no_save=true` |
| POST | `.../nodes/{node}/intent/save` | — | Persist intents to topology.json |
| POST | `.../nodes/{node}/intent/reload` | — | Reload from topology.json (topology only) |
| POST | `.../nodes/{node}/intent/clear` | — | Clear all intents (topology only) |

### 4.8 Interface Operations

Scoped to a specific interface. Dispatch via `connectAndExecute`. Response: `WriteResult`.

| Method | Path | Operation |
|--------|------|-----------|
| POST | `.../interfaces/{name}/apply-service` | `ApplyService` |
| POST | `.../interfaces/{name}/remove-service` | `RemoveService` |
| POST | `.../interfaces/{name}/refresh-service` | `RefreshService` |
| POST | `.../interfaces/{name}/configure-interface` | `ConfigureInterface` (trunk-tagged: additive per-VLAN intent, #224) |
| POST | `.../interfaces/{name}/remove-trunk-vlan` | `RemoveTrunkVLAN` — atomic single-VLAN strip from trunk, body `{vlan_id}` (#224) |
| POST | `.../interfaces/{name}/unconfigure-interface` | `UnconfigureInterface` |
| POST | `.../interfaces/{name}/set-property` | `SetProperty` |
| POST | `.../interfaces/{name}/clear-property` | `ClearProperty` |
| POST | `.../interfaces/{name}/bind-acl` | `BindACL` |
| POST | `.../interfaces/{name}/unbind-acl` | `UnbindACL` |
| POST | `.../interfaces/{name}/add-bgp-peer` | `AddBGPPeer` |
| POST | `.../interfaces/{name}/update-bgp-peer` | `UpdateBGPPeer` — atomic per-peer field mutation; key (vrf, neighbor_ip) is immutable (§47, #227) |
| POST | `.../interfaces/{name}/remove-bgp-peer` | `RemoveBGPPeer` |
| POST | `.../interfaces/{name}/bind-qos` | `BindQoS` |
| POST | `.../interfaces/{name}/unbind-qos` | `UnbindQoS` |

All paths prefixed with `/networks/{netID}/node/{node}`.

---

## 5. CONFIG_DB Table Reference

All CONFIG_DB writes go through `ChangeSet.Validate()` before reaching Redis, enforced by `render(cs)` at the point entries enter the projection. The schema is defined in `schema.go` with constraints derived from SONiC YANG models. Unknown tables and unknown fields are validation errors (fail-closed).

### 5.1 Core Tables

| Table | Key Format | Fields | Owner |
|-------|-----------|--------|-------|
| `PORT` | `Ethernet{N}` | admin_status, speed, mtu, fec, description, alias, lanes, index | RegisterPort |
| `VLAN` | `Vlan{N}` | vlanid, description | `vlan_config.go` |
| `VLAN_MEMBER` | `Vlan{N}\|{intf}` | tagging_mode | `vlan_config.go` |
| `VLAN_INTERFACE` | `Vlan{N}` / `Vlan{N}\|{ip/mask}` | vrf_name, (empty for IP) | `vlan_config.go` |
| `VRF` | `{name}` | vni | `vrf_config.go` |
| `INTERFACE` | `{intf}` / `{intf}\|{ip/mask}` | vrf_name, (empty for IP) | `interface_config.go` |
| `LOOPBACK_INTERFACE` | `Loopback0` / `Loopback0\|{ip/32}` | (empty for IP) | `baseline_config.go` |
| `PORTCHANNEL` | `PortChannel{N}` | admin_status, mtu, min_links, fast_rate, fallback | `portchannel_config.go` |
| `PORTCHANNEL_MEMBER` | `PortChannel{N}\|{intf}` | NULL:NULL | `portchannel_config.go` |
| `STATIC_ROUTE` | `{vrf}\|{prefix}` | nexthop, ifname, distance | `vrf_config.go` |
| `DEVICE_METADATA` | `localhost` | hostname, bgp_asn, type, hwsku, mac, docker_routing_config_mode, frr_mgmt_framework_config | `baseline_config.go`, `bgp_config.go` |

Note: DEVICE_METADATA uses field-level merge in `applyEntry` — both `SetDeviceMetadata` and `ConfigureBGP` write to the same key without stomping each other's fields.

### 5.2 VXLAN/EVPN Tables

| Table | Key Format | Fields | Owner |
|-------|-----------|--------|-------|
| `VXLAN_TUNNEL` | `vtep` | src_ip | `vxlan_config.go` |
| `VXLAN_EVPN_NVO` | `nvo` | source_vtep | `vxlan_config.go` |
| `VXLAN_TUNNEL_MAP` | `vtep\|map_{VNI}_{resource}` | vni, vlan | `vxlan_config.go` |
| `SUPPRESS_VLAN_NEIGH` | `Vlan{N}` | suppress | `vxlan_config.go` |
| `BGP_EVPN_VNI` | `{vrf}\|{l3vni}` | (empty) | `vxlan_config.go` |

### 5.3 BGP Tables

| Table | Key Format | Fields |
|-------|-----------|--------|
| `BGP_GLOBALS` | `{vrf\|default}` | local_asn, router_id, ebgp_requires_policy, always_compare_med, graceful_restart_enable, load_balance_mp_relax, holdtime, keepalive, rr_clnt_to_clnt_reflection, coalesce_time, route_map_process_delay |
| `BGP_GLOBALS_AF` | `{vrf\|default}\|{afi_safi}` | max_ebgp_paths, max_ibgp_paths, ebgp_route_import_policy, ibgp_route_import_policy, advertise_all_vni, route_map_in, route_map_out, soft_reconfiguration_in, route_reflector_allow_outbound_policy, maximum_paths, maximum_paths_ibgp |
| `BGP_NEIGHBOR` | `{vrf\|default}\|{ip}` | local_asn, asn, local_addr, name, admin_status, peer_group_name, ebgp_multihop |
| `BGP_NEIGHBOR_AF` | `{vrf\|default}\|{ip}\|{afi_safi}` | admin_status, soft_reconfiguration_in, route_map_in, route_map_out, allow_own_as, rrclient, unchanged_nexthop |
| `BGP_PEER_GROUP` | `{vrf\|default}\|{name}` | local_asn, asn, local_addr, name, admin_status, ebgp_multihop |
| `BGP_PEER_GROUP_AF` | `{vrf\|default}\|{name}\|{afi_safi}` | admin_status, soft_reconfiguration_in, route_map_in, route_map_out, allow_own_as, unchanged_nexthop, rrclient |
| `ROUTE_REDISTRIBUTE` | `{vrf\|default}\|{afi}\|connected` | (empty) |
| `BGP_GLOBALS_EVPN_RT` | `{vrf}\|route_target\|{rt}` | (empty) |

### 5.4 ACL Tables

| Table | Key Format | Fields |
|-------|-----------|--------|
| `ACL_TABLE` | `{name}` | policy_desc, type, stage, ports |
| `ACL_RULE` | `{table}\|{rule}` | PRIORITY, PACKET_ACTION, SRC_IP, DST_IP, IP_PROTOCOL, L4_SRC_PORT, L4_DST_PORT, ETHER_TYPE, DSCP |

### 5.5 QoS Tables

| Table | Key Format | Fields |
|-------|-----------|--------|
| `DSCP_TO_TC_MAP` | `{name}\|{dscp}` | tc |
| `TC_TO_QUEUE_MAP` | `{name}\|{tc}` | queue |
| `SCHEDULER` | `{name}` | type, weight |
| `QUEUE` | `{intf}\|{q}` | scheduler |
| `PORT_QOS_MAP` | `{intf}` | dscp_to_tc_map, tc_to_queue_map |
| `WRED_PROFILE` | `{name}` | ecn, green_min_threshold, green_max_threshold, green_drop_probability, yellow_min_threshold, yellow_max_threshold, yellow_drop_probability, red_min_threshold, red_max_threshold, red_drop_probability |

### 5.6 Policy Tables

Content-hashed names enable blue-green migration when specs change. See [HLD §4.2](hld.md) for the content-hashing mechanism.

| Table | Key Format | Fields |
|-------|-----------|--------|
| `ROUTE_MAP` | `{name}\|{seq}` | route_operation, match_prefix_set, match_community, set_local_pref, set_community, set_med |
| `PREFIX_SET` | `{name}\|{seq}` | prefix, action, ge, le |
| `COMMUNITY_SET` | `{name}` | community_member, match_action |

### 5.7 Anycast Gateway

| Table | Key Format | Fields |
|-------|-----------|--------|
| `SAG_GLOBAL` | `IP` | gateway_mac |

### 5.8 Newtron Custom Table

| Table | Key Format | Fields |
|-------|-----------|--------|
| `NEWTRON_INTENT` | `{resource}` | `operation`, `_parents`, `_children`, plus operation-specific params |

NEWTRON_INTENT is newtron's bookkeeping — not a standard SONiC table. The `resource` key identifies the intent target (e.g., `device`, `vlan|100`, `interface|Ethernet0`). `operation` names the operation that created the intent. `_parents` and `_children` encode the intent DAG for dependency tracking. All other fields are operation-specific parameters stored for reconstruction and teardown (see [HLD §3.2](hld.md) on intent round-trip completeness).

**Identity fields** (present on every intent):

| Field | Purpose |
|-------|---------|
| `state` | Lifecycle: `unrealized`, `in-flight`, `actuated` |
| `operation` | What created this intent: `apply-service`, `setup-evpn`, `configure-bgp`, etc. |
| `name` | Spec reference (e.g., `transit`), empty if none |
| `holder`, `created` | Who created the intent and when |
| `applied_at`, `applied_by` | Audit metadata for last actualization |

**Operation-specific param fields** (stored for teardown and reconstruction):

| Field | Purpose |
|-------|---------|
| `service_name` | Applied service name |
| `service_type` | Service type for teardown path selection |
| `ip_address` | Applied IP address |
| `vrf_name` | Applied VRF name |
| `vrf_type` | `"interface"` or `"shared"` |
| `ipvpn`, `macvpn` | VPN spec references |
| `l3vni`, `l3vni_vlan` | L3VNI values for VRF teardown |
| `l2vni` | L2VNI for VXLAN_TUNNEL_MAP cleanup |
| `ingress_acl`, `egress_acl` | Content-hashed ACL names |
| `bgp_neighbor`, `bgp_peer_as` | BGP peer for RefreshService |
| `qos_policy` | QoS policy name |
| `anycast_ip`, `anycast_mac` | Anycast values for SVI/SAG cleanup |
| `arp_suppression` | ARP suppression flag |
| `redistribute_vrf` | VRF where redistribution was overridden |

The intent record is self-sufficient for reverse operations — every value needed for teardown is stored in the record's params, never re-resolved from specs (see [HLD §3.2](hld.md) on intent round-trip completeness).

---

## 6. Internal Implementation

This section covers the mechanisms inside `pkg/newtron/network/node/` — the config method pattern, ChangeSet lifecycle, preconditions, intent DAG operations, and the execute lifecycle. For the pipeline architecture these pieces serve, see [HLD §3](hld.md) and [Unified Pipeline Architecture](unified-pipeline-architecture.md).

### 6.1 ChangeSet

The ChangeSet is the unit of work — a collection of pending CONFIG_DB changes with metadata. Every mutating operation returns a ChangeSet; the caller decides what happens with it.

```go
type ChangeSet struct {
    Device          string
    Operation       string
    Timestamp       time.Time
    Changes         []Change              // ordered list of CONFIG_DB mutations
    AppliedCount    int
    Verification    *sonic.VerificationResult
    OperationParams map[string]string
    ReverseOp       string
}
```

**Mutation methods:**

| Method | What it does |
|--------|-------------|
| `Add(table, key, fields)` | Append an add entry |
| `Update(table, key, fields)` | Append a modify entry |
| `Delete(table, key)` | Append a delete entry |
| `Adds(entries)` | Append multiple adds from `[]sonic.Entry` |
| `Prepend(table, key, fields)` | Insert at the front (used by `writeIntent` — intent records first) |
| `Merge(other)` | Append all changes from another ChangeSet |

**Lifecycle methods:**

| Method | What it does |
|--------|-------------|
| `IsEmpty()` | True if no changes |
| `Preview()` | Human-readable diff text (used for dry-run output) |
| `Apply(n)` | Write changes to Redis via `PipelineSet`. No-op if `n.conn == nil` |
| `Verify(n)` | Re-read CONFIG_DB, compare against changes. Stores result in `cs.Verification` |

**Preview format:** `+ TABLE|key field=value` (add), `- TABLE|key` (delete), `~ TABLE|key field: old→new` (modify). Used for dry-run output in `WriteResult.Preview`.

The `validate()` method (internal) runs schema validation via `schema.ValidateChanges(cs.Changes)` — called by `render(cs)` at the point entries enter the projection. `Apply` does not re-validate.

### 6.2 Entry

The wire format — what config generators return and what Redis speaks:

```go
type Entry struct {
    Table  string
    Key    string
    Fields map[string]string
}
```

Field-less entries (empty `Fields` map) are valid — SONiC uses them for IP assignments (`LOOPBACK_INTERFACE|Loopback0|10.0.0.1/32`), portchannel members, route targets, and similar patterns where the key IS the data.

### 6.3 Config Function Pattern

Each `*_ops.go` file follows a three-layer pattern. The config generator is a pure function; intent management and rendering happen in the wrapping method.

**Layer 1 — Config generator** (in `*_config.go`): Pure function, no side effects. Takes parameters, returns `[]sonic.Entry`.

```go
// vlan_config.go
func createVlanConfig(vlanID int, description string) []sonic.Entry {
    return []sonic.Entry{
        {Table: "VLAN", Key: fmt.Sprintf("Vlan%d", vlanID),
         Fields: map[string]string{"vlanid": strconv.Itoa(vlanID), ...}},
    }
}
```

**Layer 2 — `op()` wrapper** (in `changeset.go`): Runs preconditions, calls the generator, builds the ChangeSet, calls `render(cs)` to validate and update the projection.

```go
cs, err := n.op(sonic.OpCreateVLAN, vlanResource(100), ChangeAdd,
    func(pc *PreconditionChecker) {
        pc.Check(100 >= 1 && 100 <= 4094, "valid VLAN ID",
            "must be 1-4094, got 100")
    },
    func() []sonic.Entry {
        return createVlanConfig(100, opts)
    },
    "device.delete-vlan",
)
```

**Layer 3 — Intent-wrapping method** (in `*_ops.go`): Checks idempotency, calls `op()`, calls `writeIntent()`, returns the ChangeSet.

```go
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, opts VLANOpts) (*ChangeSet, error) {
    resource := fmt.Sprintf("vlan|%d", vlanID)
    if n.GetIntent(resource) != nil {
        return NewChangeSet(n.Name(), "create-vlan"), nil  // idempotent
    }
    cs, err := n.op("create-vlan", resource, sonic.ChangeTypeAdd, checks, gen)
    if err != nil { return nil, err }
    if err := writeIntent(cs, "create-vlan", resource, params, []string{"device"}); err != nil {
        return nil, err
    }
    return cs, nil
}
```

#### Worked Example: CreateVLAN End-to-End

Tracing `POST /newtron/v1/networks/default/node/leaf1/create-vlan` with `{"id": 100, "name": "servers"}` and `?execute=true` through every layer:

```
1. HTTP layer (handler_node.go)
   handleCreateVLAN parses JSON body → VLANConfig{VlanID: 100, Description: "servers"}
   Resolves networkEntity → NodeActor
   Calls connectAndExecute(fn)

2. Actor layer (actors.go)
   execute(ctx, fn):
     ensureActuatedIntent(ctx) → InitFromDeviceIntent if first request
     RebuildProjection(ctx) → re-read NEWTRON_INTENT from Redis, rebuild projection

   connectAndExecute → node.Execute(ctx, opts, fn):
     Lock(ctx) → Redis SETNX + drift guard
     snapshot := SnapshotIntentDB()

3. Node layer (vlan_ops.go)
   fn(ctx) → CreateVLAN(ctx, 100, opts):
     GetIntent("vlan|100") → nil (not yet created)

     op(OpCreateVLAN, "vlan|100", ChangeAdd, ...):
       precondition: Check(100 in 1..4094, "valid VLAN ID") ✓
       gen: createVlanConfig(100, "servers") → [
         {VLAN, "Vlan100", {vlanid: "100", description: "servers"}}
       ]
       render(cs): schema.Validate() → OK; configDB.VLAN["Vlan100"] updated

     writeIntent(cs, "create-vlan", "vlan|100",
       {vlan_id: "100", description: "servers"}, ["device"]):
       cs.Prepend("NEWTRON_INTENT", "vlan|100", {operation: "create-vlan", ...})
       renderIntent → configDB.NewtronIntent["vlan|100"] updated

4. Execute layer (node.go)
   opts.Execute = true → Commit(ctx):
     cs.Apply(n) → PipelineSet to Redis:
       HSET NEWTRON_INTENT "vlan|100" ...  (first — prepended)
       HSET VLAN "Vlan100" ...
     cs.Verify(n) → re-read CONFIG_DB, compare → all match ✓

   SaveConfig(ctx) → SSH "config save -y"
   Unlock()

5. Response
   WriteResult{ChangeCount: 2, Applied: true, Verified: true, Saved: true}
   → JSON → HTTP 200 → CLI prints "Changes applied successfully."
```

### 6.4 PreconditionChecker

Fluent builder that validates operation prerequisites against the intent DB. Created by `precondition(operation, resource)` — in actuated mode, `RequireConnected` and `RequireLocked` are automatically added.

```go
n.precondition("delete-vlan", "vlan|100").
    RequireVLANExists(100).
    Result()
```

**Available checks** (all check the intent DB via `GetIntent`, not CONFIG_DB):

| Method | Checks |
|--------|--------|
| `RequireConnected()` | Transport connection exists |
| `RequireLocked()` | Device lock held |
| `RequireInterfaceExists(name)` | Interface registered in node |
| `RequireInterfaceNotPortChannelMember(name)` | No portchannel intent claims this interface |
| `RequireVLANExists(id)` | Intent `vlan\|{id}` exists (for delete/modify) |
| `RequireVRFExists(name)` | Intent `vrf\|{name}` exists (for delete/modify) |
| `RequirePortChannelExists(name)` / `RequirePortChannelNotExists(name)` | Intent for portchannel exists/absent |
| `RequireVTEPConfigured()` | Intent `vtep` exists (VXLAN tunnel set up) |
| `RequireACLTableExists(name)` / `RequireACLTableNotExists(name)` | Intent for ACL table exists/absent |
| `Check(condition, precondition, details)` | Arbitrary boolean check |

### 6.5 Intent DAG Operations

Intent records form a directed acyclic graph via `_parents` and `_children` fields. The DAG enforces structural dependencies — a VLAN can't be deleted while services reference it.

**Write path** (`intent_ops.go`, all internal to package):

| Function | What it does |
|----------|-------------|
| `writeIntent(cs, op, resource, params, parents)` | Create intent record; verify all parents exist (I4); register as child of each parent; `cs.Prepend` puts intent first in ChangeSet |
| `deleteIntent(cs, resource)` | Remove intent record; refuse if children exist (I5); deregister from parents' `_children` |
| `renderIntent(entry)` | Immediately apply intent entry to in-memory configDB so subsequent `writeIntent` calls within the same operation can see parents |

**DAG invariants:**

| Rule | Enforcement |
|------|------------|
| I4: Parent must exist before child creation | `writeIntent` checks each parent via `GetIntent` |
| I5: Children must be deleted before parent | `deleteIntent` checks `_children` field |

Bidirectional consistency, referential integrity, and orphan absence are
all implicit consequences of `writeIntent`/`deleteIntent`'s discipline
about registering children on both sides and refusing dangling references.

### 6.6 Reconstruct: IntentsToSteps and ReplayStep

These two functions are the bridge between intent records and config methods. They are used by `RebuildProjection` (every operation) and `InitFromDeviceIntent` (first connection in actuated mode).

**`IntentsToSteps(intents) []TopologyStep`** — Converts the flat NEWTRON_INTENT map into an ordered slice of topology steps using Kahn's topological sort on the `_parents`/`_children` DAG. Filters out `interface-init` and `deploy-service` intents (auto-created as side effects). Ties broken alphabetically for determinism.

**`ReplayStep(ctx, n, step) error`** — Dispatches a single topology step to the appropriate Node or Interface method. Parses the step URL:

| URL pattern | Dispatch |
|-------------|----------|
| `/setup-device` | `n.SetupDevice(ctx, opts)` |
| `/create-vlan` | `n.CreateVLAN(ctx, id, opts)` |
| `/create-vrf` | `n.CreateVRF(ctx, name, opts)` |
| `/bind-ipvpn` | `n.BindIPVPN(ctx, vrf, ipvpn)` |
| `/bind-macvpn` | `n.BindMACVPN(ctx, vlan, macvpn)` |
| `/create-portchannel` | `n.CreatePortChannel(ctx, config)` |
| `/add-pc-member` | `n.AddPortChannelMember(ctx, pc, member)` |
| `/create-acl` | `n.CreateACL(ctx, config)` |
| `/add-acl-rule` | `n.AddACLRule(ctx, config)` |
| `/configure-irb` | `n.ConfigureIRB(ctx, config)` |
| `/add-bgp-evpn-peer` | `n.AddBGPEVPNPeer(ctx, peer)` |
| `/add-static-route` | `n.AddStaticRoute(ctx, vrf, prefix, nexthop)` |
| `/interfaces/{name}/apply-service` | `iface.ApplyService(ctx, service, opts)` |
| `/interfaces/{name}/configure-interface` | `iface.ConfigureInterface(ctx, config)` |
| `/interfaces/{name}/add-bgp-peer` | `iface.AddBGPPeer(ctx, config)` |
| `/interfaces/{name}/set-property` | `iface.SetProperty(ctx, prop, value)` |
| `/interfaces/{name}/bind-acl` | `iface.BindACL(ctx, acl, direction)` |
| `/interfaces/{name}/bind-qos` | `iface.BindQoS(ctx, policy, spec)` |

`RebuildProjection` (in `node.go`) and `InitFromDeviceIntent` (in
`node_actuated.go`) call `IntentsToSteps + ReplayStep` directly against
the Node's own configDB — that is the live "intent → projection" path.

**`IntentToStep(resource, fields) TopologyStep`** — Converts a single intent record back to a step. Uses `intentParamsToStepParams` to map flat intent field names back to structured step params (handles special cases: `setup-device` RR params, `apply-service` field renames, `create-portchannel` member list serialization).

### 6.7 Node Execute Lifecycle

All operations flow through `execute()` in the actor layer — the single entry point.

```
execute(ctx, fn)
  ├─ Mode dispatch:
  │    if topology mode: ensureTopologyIntent()
  │    if actuated mode: ensureActuatedIntent(ctx) → InitFromDeviceIntent on first request
  │
  ├─ RebuildProjection(ctx)
  │    ├─ if connected: fresh intents from Redis NEWTRON_INTENT
  │    ├─ else: intents from in-memory configDB.NewtronIntent
  │    ├─ ports := configDB.ExportPorts()
  │    ├─ configDB = NewConfigDB()          ← fresh projection
  │    ├─ RegisterPort() for each port
  │    ├─ configDB.NewtronIntent = intents
  │    ├─ IntentsToSteps(intents) → topological sort
  │    └─ ReplayStep() for each → intent DB + projection rebuilt
  │
  └─ fn(ctx, node) → operation-specific logic
```

**Read path** (`connectAndRead`): `fn` calls `Ping` then the read operation (list, show, status, drift).

**Write path** (`connectAndExecute`): `fn` calls `Execute(ctx, opts, writeFn)`:

```
Execute(ctx, opts, fn)
  ├─ Lock(ctx)               ← Redis SETNX + drift guard (actuated mode)
  ├─ snapshot := SnapshotIntentDB()
  ├─ fn(ctx)                 ← config methods: writeIntent + op() + render
  │
  ├─ if error or dry-run:
  │    RestoreIntentDB(snapshot) → intent DB restored
  │    (dirty projection cleaned by next RebuildProjection)
  │
  ├─ if opts.Execute:
  │    Commit(ctx):
  │      cs.Apply(n)  → PipelineSet to Redis (NEWTRON_INTENT first)
  │      cs.Verify(n) → re-read CONFIG_DB, compare
  │    SaveConfig(ctx) → SSH "config save -y" (unless NoSave)
  │
  └─ Unlock()
```

### 6.8 Data Representations

Data exists in three forms as it moves through the system:

| Form | Where | Purpose |
|------|-------|---------|
| Intent record | `configDB.NewtronIntent` | Primary state — what should be configured |
| Typed struct | `configDB.VLAN`, `configDB.VRF`, etc. | Projection — rendered from intent replay |
| `map[string]string` | Redis hashes, `Entry.Fields` | Wire format — what Redis speaks |

Three mechanisms bridge these:

| Mechanism | Direction | Where it runs |
|-----------|-----------|---------------|
| `configTableHydrators` | wire → struct | `render` (all paths), `GetAll` (device read) |
| `structToFields` | struct → wire | `ExportEntries` / `ExportRaw` (Reconcile, Drift) |
| `schema.Validate` | wire → pass/fail | `render` (all paths, both modes) |

The hydrator registry (`configdb_parsers.go`) is the central bridge — 33 typed parsers for tables with structured fields, 9 merge parsers for tables where the key carries the data (IP assignments, route targets, portchannel members). `ExportEntries` is the reverse path — it reads typed structs and calls `structToFields` (reflection on json tags) to produce `[]Entry` for delivery.

**Hydrator field completeness**: A field that exists in config generator output but is missing from the typed struct or hydrator is silently dropped during hydration — the projection loses it, and `ExportEntries` never exports it. This causes false drift on a correctly-configured device. Every field written by a config generator must exist in three places: `schema.go` (validation), the typed struct in `configdb.go` (representation), and the hydrator in `configdb_parsers.go` (wire → struct).

### 6.9 Spec Resolution

`buildResolvedSpecs()` in `network.go` merges the three-level hierarchy (network → zone → node) into a per-node `ResolvedSpecs` snapshot. This snapshot implements the `SpecProvider` interface that all node operations use for spec lookups:

```go
type SpecProvider interface {
    GetService(name string) *spec.ServiceSpec
    GetIPVPN(name string) *spec.IPVPNSpec
    GetMACVPN(name string) *spec.MACVPNSpec
    GetFilter(name string) *spec.FilterSpec
    GetQoSPolicy(name string) *spec.QoSPolicy
    GetRoutePolicy(name string) *spec.RoutePolicy
    GetPrefixList(name string) []string
}
```

Lookups fall through: node-level checked first, then zone, then network. Specs added via the API after snapshot time are invisible in the snapshot — all `Get*` methods on `ResolvedSpecs` fall through to `network.Get*` on miss (the live network, not the snapshot).

Names are normalized once at spec load time (uppercase, hyphens → underscores). Operations code never calls `NormalizeName()`.

### 6.10 Value Derivation

All values below are derived at `ApplyService` time and stored in NEWTRON_INTENT params for teardown. Abstract mode (topology provisioning) derives identical values through the same code path.

| Value | Derivation |
|-------|-----------|
| VRF name (interface type) | `{SERVICE}_{SHORT_INTF}` (e.g., `TRANSIT_ETH0`) |
| VRF name (shared type) | IPVPN spec's VRF name directly |
| ACL table name | `{FILTER}_{DIRECTION}_{HASH}` (e.g., `PROTECT_RE_IN_A1B2C3D4`) |
| Neighbor IP (/31) | XOR last bit of local IP |
| Neighbor IP (/30) | XOR host bits (1→2, 2→1) |
| Router ID | Loopback IP from node spec |
| VTEP source IP | Loopback IP from node spec |

**Interface name normalization** (short forms used in derived names):

| Short | Full (SONiC) |
|-------|-------------|
| `Eth0` | `Ethernet0` |
| `Po100` | `PortChannel100` |
| `Vl100` | `Vlan100` |
| `Lo0` | `Loopback0` |

### 6.11 Shared Policy Objects

CONFIG_DB entries fall into three categories based on lifecycle:

| Category | Identity | Lifecycle |
|----------|----------|-----------|
| **Infrastructure** | Per-interface | Created/destroyed with service apply/remove |
| **Policy** | User-named + content hash | Shared across services, independent lifecycle |
| **Binding** | Per-interface | Created/destroyed with service apply/remove |

Policy objects (ACL_TABLE, ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) are created on first reference and deleted when the last reference is removed. Content-hashed naming: shared policy objects include an 8-character SHA256 hash of their generated CONFIG_DB fields in the key name. Spec unchanged → hash unchanged → `RefreshService` is a no-op for that object. Spec changed → new hash → new object alongside old, interfaces migrate one by one.

Dependent objects use bottom-up Merkle hashing: PREFIX_SET hashes computed first, then ROUTE_MAP entries reference real PREFIX_SET names (including hashes), so a content change cascades through the hash chain automatically.

### 6.12 Middleware Chain

HTTP middleware applied to all routes (outer → inner):

| Middleware | Purpose |
|-----------|---------|
| `withRecovery` | Panic recovery → 500 response |
| `withLogger` | Request/response logging |
| `withRequestID` | Unique ID per request (X-Request-ID header) |
| `withTimeout(5min)` | Context deadline |
| `withMode` | Resolves `?mode=topology` query param into context |

### 6.13 Actor Serialization

Each `NodeActor` serializes access to its cached `*Node` — only one operation runs at a time per device. The actor also manages the SSH connection lifecycle:

- **Idle timeout** (default 5 minutes): Connection closed when no requests arrive within the timeout window, eliminating persistent SSH sessions for inactive devices.
- **Connection caching**: `ensureActuatedIntent` / `ensureTopologyIntent` run once to construct the node; subsequent requests reuse the cached node.
- **Graceful disconnect**: `DisconnectTransport()` on timeout, `Disconnect()` on unregister.

---

## 7. Permission System

Permission types are defined covering service operations, resource CRUD, spec authoring, and device cleanup. Read/view operations have no permission requirement.

**Current status:** Permission types exist in code but are not enforced at the HTTP layer. The server has no authentication middleware — it is designed for trusted-network deployment (localhost or VPN). When authentication is added, the defined permission types provide the granularity framework.

---

## 8. Audit Logging

Every mutating operation is logged to a rotating JSON-lines audit log. The `audit/` package provides append-only writing with size-based rotation.

```go
type AuditEvent struct {
    ID          string        `json:"id"`
    Timestamp   string        `json:"timestamp"`
    User        string        `json:"user"`
    Device      string        `json:"device"`
    Operation   string        `json:"operation"`
    Changes     []AuditChange `json:"changes"`
    Success     bool          `json:"success"`
    ExecuteMode bool          `json:"execute_mode"`
    DryRun      bool          `json:"dry_run"`
    Duration    string        `json:"duration"`
}
```

Audit events capture: who did what, on which device, what CONFIG_DB entries were modified, whether it succeeded, and whether it was a dry-run or execute. Dry-run events are logged for auditability — they show what was previewed even though no changes were applied.

Configuration: `UserSettings.AuditLogPath` (default: `{dir}/audit.log`), `AuditMaxSizeMB` (default: 10), `AuditMaxBackups` (default: 10).

---

## 9. CLI Command Reference

The CLI (`cmd/newtron/`) is an HTTP client — it sends requests to newtron-server and formats responses. All device state manipulation goes through the HTTP API; the CLI never imports internal packages.

### 9.1 Global Flags

| Flag | Purpose | Default |
|------|---------|---------|
| `-s, --server` | Server URL | `http://localhost:18080` |
| `-n, --network` | Network ID | `default` |
| `-x, --execute` | Apply changes (vs dry-run) | false |
| `--no-save` | Skip config save after apply | false |
| `--topology` | Use topology mode (offline abstract node) | false |

### 9.2 Resource Nouns

| Noun | Subcommands | Scope |
|------|-------------|-------|
| `service` | `list`, `show`, `create`, `delete`, `apply`, `remove`, `refresh` | Network (CRUD), Interface (apply/remove/refresh) |
| `vlan` | `list`, `show`, `create`, `delete` | Node |
| `vrf` | `list`, `show`, `create`, `delete`, `add-interface`, `remove-interface`, `add-neighbor`, `remove-neighbor`, `bind-ipvpn`, `unbind-ipvpn`, `add-static-route`, `remove-static-route`, `status` | Node |
| `bgp` | `status` | Node |
| `evpn` | `setup`, `status`, `ipvpn` (sub-noun), `macvpn` (sub-noun) | Node (setup/status), Network (ipvpn/macvpn CRUD) |
| `acl` | `list`, `show`, `create`, `delete`, `add-rule`, `remove-rule`, `bind`, `unbind` | Node |
| `qos` | `list`, `show`, `create`, `delete`, `add-queue`, `remove-queue`, `apply`, `remove` | Network (CRUD), Interface (apply/remove) |
| `filter` | `list`, `show`, `create`, `delete`, `add-rule`, `remove-rule` | Network |
| `interface` | `list`, `show`, `configure`, `unconfigure`, `set`, `clear`, `binding` | Node |
| `lag` | `list`, `show`, `create`, `delete`, `add-member`, `remove-member` | Node |
| `device` | `setup` | Node |
| `intent` | `tree`, `drift`, `reconcile`, `save`, `reload`, `clear` | Node |
| `health` | (default) | Node |
| `init` | (default) | Node |
| `show` | (default — device info) | Node |
| `platform` | `list`, `show` | Network |
| `profile` | `list`, `show`, `create`, `delete` | Network |
| `zone` | `list`, `show`, `create`, `delete` | Network |
| `settings` | `show`, `set` | Local |
| `audit` | `list`, `show` | Local |

### 9.3 VRF Neighbor Auto-Derivation

When adding a BGP neighbor to a VRF, the CLI auto-derives values from the interface context:

- **Peer IP**: Derived from the interface's IP address (flip the last octet for /31 subnets)
- **Remote AS**: From the service spec's `routing.peer_as` (if `"request"`, the caller provides it)
- **Local address**: The interface's own IP
- **Description**: The neighbor IP (default fallback — bgpcfgd requires a non-empty `name` field)

### 9.4 Service Immutability

Services are immutable once created. To change a service definition:
1. Create a new service with the desired spec
2. `refresh-service` on each interface to migrate to the new spec
3. Delete the old service

`RefreshService` = full remove + reapply cycle. The two ChangeSets merge, preserving intermediate DEL operations (required because Redis HSET merges fields — DEL is needed to remove stale fields before re-HSET).

---

## 10. Testing

### 10.1 API Completeness Test

`api_test.go` verifies that every route in `buildMux` has a corresponding client method. This prevents silent API drift — adding a server endpoint without a client method (or vice versa) fails the test.

### 10.2 Unit Tests

`go test ./...` covers pure logic: IP derivation, spec parsing, ACL expansion, schema validation, intent DAG consistency, config generator output, ChangeSet merging, and architecture invariants (the architecture test verifies that internal types don't leak through the public API).

### 10.3 E2E Tests

The newtrun framework runs full-stack tests against newtlab VMs with real SONiC. See [newtrun HLD](../newtrun/hld.md) for framework design and [newtrun HOWTO](../newtrun/howto.md) for writing scenarios.

---

## 11. Cross-References

| Topic | Document |
|-------|----------|
| Architecture, design rationale, pipeline overview | [HLD](hld.md) |
| Full pipeline specification with end-to-end traces | [Unified Pipeline Architecture](unified-pipeline-architecture.md) |
| Device-layer internals (SSH tunneling, Redis clients) | [Device LLD](device-lld.md) |
| Intent DAG hierarchy and record format | [Intent DAG Architecture](intent-dag-architecture.md) |
| Architectural principles | [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md) |
| Operational procedures (CLI usage, provisioning) | [HOWTO](howto.md) |
| SONiC pitfalls and workarounds | [RCA Index](../rca/) |
