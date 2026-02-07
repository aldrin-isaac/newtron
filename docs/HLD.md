# Newtron High-Level Design (HLD) v5

### What Changed

#### v2

| Area | Change |
|------|--------|
| **Device Connection** | Added SSH-tunneled Redis access architecture; port 6379 is not forwarded by QEMU — SSH tunnel via port 22 is the only path to Redis |
| **StateDB** | Added StateDB (Redis DB 6) as a read-only operational state source alongside ConfigDB (DB 4); non-fatal connection failure semantics |
| **Config Persistence** | Added documentation of runtime-vs-persistent config model; `config save -y` operator responsibility |
| **Verification Strategy** | Added three-tier verification: CONFIG_DB hard fail, ASIC_DB topology-dependent, data-plane soft fail |
| **Lab Architecture** | Added labgen pipeline overview (topology YAML → config_db.json, frr.conf, clab.yml, specs) |
| **Testing Architecture** | Added unit/integration/E2E tier documentation with build tag separation |
| **Security Model** | Added SSH transport security, InsecureIgnoreHostKey for labs, permission levels |
| **Design Decisions** | Added six numbered decisions with rationale (SSH tunnel, dry-run default, build tags, preconditions, fresh connections, non-fatal StateDB) |
| **Layer Diagram** | Updated to show SSH tunnel and StateDB components in the Device Layer |
| **Glossary** | Added Connection Types, Redis Databases (DB 1/4/6), and Operations terms |

**Lines:** 583 (v1) → 1019 (v2) | All v1 sections preserved and expanded.

#### v3

| Area | Change |
|------|--------|
| **BGP Management** | Switched from split-config FRR (`frr.conf`) to FRR management framework (`frrcfgd`) — full BGP underlay, IPv6, and EVPN overlay managed through CONFIG_DB |
| **Route Reflection** | `SetupRouteReflector` replaces `SetupBGPEVPN` — activates all three address families (ipv4_unicast, ipv6_unicast, l2vpn_evpn), sets cluster-id, next-hop-self, RR client |
| **New CONFIG_DB Tables** | Added 9 tables: ROUTE_REDISTRIBUTE, ROUTE_MAP, BGP_PEER_GROUP, BGP_PEER_GROUP_AF, BGP_GLOBALS_AF_NETWORK, BGP_GLOBALS_AF_AGGREGATE_ADDR, PREFIX_SET, COMMUNITY_SET, AS_PATH_SET |
| **Extended BGP Fields** | BGP_GLOBALS: load_balance_mp_relax, rr_cluster_id, ebgp_requires_policy, etc.; BGP_NEIGHBOR: peer_group, password; BGP_NEIGHBOR_AF: route_map_in/out, prefix_list_in/out, allowas_in, addpath |
| **Composite Mode** | Offline composite CONFIG_DB generation with two delivery modes: overwrite (full replace) and merge (interface-level services only); Redis pipeline for atomic delivery |
| **Port Creation** | Validated PORT entry creation against SONiC's `platform.json`; breakout support; `LoadPlatformConfig()` and `GeneratePlatformSpec()` device methods |
| **Routing Spec** | Added import/export community, prefix-list, redistribute flag to Service.Routing |
| **SiteSpec** | Added `cluster_id` field for BGP route reflector cluster ID |
| **Execution Modes** | Added composite mode alongside dry-run and execute |
| **Design Decisions** | Added five new decisions: frrcfgd over bgpcfgd/split, composite merge restrictions, platform.json validation, redistribution defaults, single SetupRouteReflector |
| **Lab Architecture** | `frr.conf` no longer generated; port creation replaces labgen PORT logic; composite replaces sequential configlet merge |

**Lines:** 1019 (v2) → ~1340 (v3) | All v2 sections preserved and expanded.

#### v4

| Area | Change |
|------|--------|
| **Type Renames** | Renamed spec container types: `NetworkSpec` → `NetworkSpecFile`, `SiteSpec` → `SiteSpecFile`, `PlatformSpec` → `PlatformSpecFile`; renamed definition types: `IPVPNDef` → `IPVPNSpec`, `MACVPNDef` → `MACVPNSpec`, `PolicerDef` → `PolicerSpec`, `PlatformDef` → `PlatformSpec`; renamed `Service` → `ServiceSpec`, `Routing` → `RoutingSpec`, `Region` → `RegionSpec`, `Site` → `SiteSpec` |
| **Spool → Composite** | Renamed "spool" terminology to "composite" throughout: `SpoolBuilder` → `CompositeBuilder`, `SpoolConfig` → `CompositeConfig`, `SpoolMode` → `CompositeMode`, `DeliverSpool` → `DeliverComposite`, `ValidateSpool` → `ValidateComposite`, `spool.go` → `composite.go` |
| **Topology Provisioning** | New §4.8: automated device provisioning from `topology.json` specs — full-device overwrite or per-interface service application; two modes: `ProvisionDevice` (offline build + atomic delivery) and `ProvisionInterface` (connect + interrogate + apply) |
| **topology.json** | New optional spec file declaring complete desired state: devices, interfaces, service bindings, links, and per-interface parameters (IP, peer_as) |
| **Execution Modes** | Added topology provisioning mode (§12.4) alongside dry-run, execute, and composite |
| **Design Decisions** | Added two new decisions: topology-first provisioning over imperative scripts (§17.12), full-device vs per-interface modes (§17.13) |

**Lines:** ~1340 (v3) → ~1560 (v4) | All v3 sections preserved and expanded.

#### v5

| Area | Change |
|------|--------|
| **Verification Architecture** | New §4.9: architectural principle ("if a tool changes state, it must verify the intended effect"), VerifyChangeSet for CONFIG_DB diff, GetRoute/GetRouteASIC for routing state observation, verification summary table |
| **Execution Modes** | New §12.5: `--verify` flag for post-execution CONFIG_DB verification; standalone `newtron verify` command for checking already-provisioned devices |
| **Verification Strategy** | Rewritten §13: four-tier strategy with explicit ownership — Tier 1-3 owned by newtron (CONFIG_DB assertion, APP_DB/ASIC_DB observation, health checks), Tier 4 owned by newtest (cross-device, data plane) |
| **Device Layer** | Added AppDBClient (Redis DB 0) and AsicDBClient (Redis DB 1) alongside existing ConfigDBClient (DB 4) and StateDBClient (DB 6) |
| **Design Decisions** | New §17.14: Built-in verification primitives over external test assertions — ChangeSet-based verification is universal across all operations; routing state observations return data not verdicts |
| **Glossary** | Added APP_DB (DB 0), VerifyChangeSet, GetRoute, GetRouteASIC terms; updated ChangeSet definition to include verification contract role |

**Lines:** ~1560 (v4) → ~1700 (v5) | All v4 sections preserved; verification architecture added.

---

## 1. Executive Summary

Newtron is a network automation CLI tool for managing SONiC-based network switches. It provides a service-oriented approach to network configuration, combining interface settings, EVPN/VXLAN overlays, ACL filters, and QoS policies into reusable service definitions.

### Key Features

- **SONiC-Native**: Direct integration with SONiC's config_db via Redis
- **Service-Oriented**: Pre-defined service templates for consistent configuration
- **EVPN/VXLAN**: Modern overlay networking replacing traditional MPLS L3VPN
- **Full BGP via frrcfgd**: Underlay, IPv6, and L2/L3 EVPN overlays managed through CONFIG_DB using SONiC's FRR management framework
- **Multi-AF Route Reflection**: Single `SetupRouteReflector` operation configures IPv4, IPv6, and L2VPN EVPN address families with cluster-id
- **Composite Mode**: Offline composite config generation with atomic delivery (overwrite or merge)
- **Topology Provisioning**: Automated device provisioning from topology.json specs — full-device overwrite or per-interface service application
- **Platform-Validated Port Creation**: PORT entries validated against SONiC's on-device `platform.json`
- **Built-In Verification**: ChangeSet-based CONFIG_DB verification, routing state observation via APP_DB/ASIC_DB, health checks — single-device primitives that orchestrators compose for fabric-wide assertions
- **Safety-First**: Dry-run by default with explicit execution flag
- **Airtight Validation**: Comprehensive precondition checking prevents misconfigurations
- **Audit Trail**: All changes logged with user, timestamp, and device context
- **Access Control**: Permission-based operations at service and action levels
- **SSH-Tunneled Redis**: Secure access to device Redis through SSH port forwarding

## 2. Spec vs Config: The Fundamental Distinction

Newtron enforces a clear separation between **specification (intent)** and **configuration (device state)**:

### 2.1 Specification (Declarative Intent)

Specs describe **what you want** - they are declarative, abstract, and policy-driven.

| Property | Description |
|----------|-------------|
| **Nature** | Declarative - describes desired state |
| **Location** | `specs/` directory, `pkg/spec` package |
| **Format** | JSON files (network.json, site.json, profiles/*.json) |
| **Content** | Service definitions, filter policies, VPN parameters |
| **Lifecycle** | Created by network architects, versioned in git |

**Example - Service Spec:**
```json
{
  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface",
      "service_type": "l3",
      "ipvpn": "customer-vpn",
      "vrf_type": "interface",
      "routing": {
        "protocol": "bgp",
        "peer_as": "request",
        "import_policy": "customer-import",
        "export_policy": "customer-export"
      },
      "ingress_filter": "customer-edge-in",
      "egress_filter": "customer-edge-out"
    }
  }
}
```

This spec declares **intent**: "Customer interfaces should use BGP with these policies and filters." It doesn't specify concrete values like peer IPs or ACL rule numbers.

### 2.2 Configuration (Imperative Device State)

Config is **what the device uses** - imperative, concrete, and device-specific.

| Property | Description |
|----------|-------------|
| **Nature** | Imperative - specific commands/entries |
| **Location** | SONiC config_db (Redis), generated at runtime |
| **Format** | config_db table entries |
| **Content** | Specific IPs, ACL rules, BGP neighbors |
| **Lifecycle** | Generated when applying specs to interfaces |

**Example - Generated Config (config_db):**
```json
{
  "VRF": {
    "customer-l3-Ethernet0": { "vni": "10001" }
  },
  "INTERFACE": {
    "Ethernet0": { "vrf_name": "customer-l3-Ethernet0" },
    "Ethernet0|10.1.1.1/30": {}
  },
  "BGP_NEIGHBOR": {
    "10.1.1.2": {
      "asn": "65100",
      "local_asn": "13908",
      "local_addr": "10.1.1.1"
    }
  },
  "ACL_TABLE": {
    "customer-l3-Ethernet0-in": { "type": "L3", "stage": "ingress", "ports": ["Ethernet0"] }
  },
  "ACL_RULE": {
    "customer-l3-Ethernet0-in|RULE_100": { "SRC_IP": "0.0.0.0/8", "PACKET_ACTION": "DROP" }
  }
}
```

### 2.3 Translation: Spec -> Config

The translation layer interprets specs in context to generate config:

```
+-----------------------------------------------------------------------+
|                        SPEC (Declarative)                             |
|  "Use BGP with peer_as=request, import_policy=customer-import"        |
+-----------------------------------------------------------------------+
                                  |
                                  | + Context:
                                  |   - Interface: Ethernet0
                                  |   - IP: 10.1.1.1/30
                                  |   - Device AS: 13908
                                  |   - User-provided peer AS: 65100
                                  v
+-----------------------------------------------------------------------+
|                      TRANSLATION (pkg/network)                        |
|  - Derive peer IP from interface IP (10.1.1.2 for /30)               |
|  - Generate VRF name: {service}-{interface}                           |
|  - Expand filter-spec into ACL rules                                  |
|  - Apply route policy to BGP neighbor                                 |
+-----------------------------------------------------------------------+
                                  |
                                  v
+-----------------------------------------------------------------------+
|                       CONFIG (Imperative)                              |
|  BGP_NEIGHBOR: 10.1.1.2 -> {asn: 65100, local_addr: 10.1.1.1}       |
|  VRF: customer-l3-Ethernet0 -> {vni: 10001}                          |
|  ACL_RULE: customer-l3-Ethernet0-in|RULE_100 -> {action: DROP}       |
+-----------------------------------------------------------------------+
```

### 2.4 Key Principles

1. **Specs are minimal**: Only declare what's needed; operational details are derived
2. **Specs are reusable**: Same service spec applies to any interface
3. **Config is concrete**: Every value is explicit and device-specific
4. **Config is derived**: Generated from spec + context, never hand-edited
5. **Single source of truth**: Each fact exists in exactly one place

### 2.5 What Belongs Where

| In Spec (Declarative) | Derived at Runtime |
|-----------------------|--------------------|
| Service type (l2, l3, irb) | VRF name |
| VPN reference (ipvpn, macvpn) | Peer IP (from interface IP) |
| Routing protocol (bgp, static) | ACL table name |
| Peer AS policy ("request" or fixed) | ACL rule sequence numbers |
| Filter-spec reference | Local AS (from device profile) |
| Route policy references | Router ID (from loopback IP) |
| Import/export policies | BGP neighbor entries |

## 3. Architecture Overview

### 3.1 Layer Diagram

```
+------------------+     +------------------+     +------------------+
|                  |     |                  |     |                  |
|   CLI Layer      |---->|  Network Layer   |---->|  SONiC Switch    |
|   (cmd/newtron)  |     |  (pkg/network)   |     |  (Redis/config_db)|
|                  |     |                  |     |                  |
+------------------+     +------------------+     +------------------+
           |                    |
           v                    +-----------+-----------+
   +---------------+            |           |           |
   |CompositeBuilder|           v           v           v
   | (offline      |     +----------+ +----------+ +----------+
   |  composite    |     | Spec     | | Device   | | Model    |
   |  config gen)  |     | Layer    | | Layer    | | Layer    |
   +---------------+     +----------+ +----------+ +----------+
   +---------------+
   |Topology       |
   |Provisioner    |
   | (automated    |
   |  device prov) |
   +---------------+
                                |           |
                                v           |
                         +----------+      |
                         | Audit    |      |
                         | Layer    |      |
                         +----------+      |
                                           v
             +-----------------------------------------+
             |         Device Layer                     |
             |  (pkg/device)                            |
             |                                          |
             |  +---------------------------+           |
             |  | SSH Tunnel (port 22)      |           |
             |  | (when SSHUser+SSHPass set)|           |
             |  +---------------------------+           |
             |           |                              |
             |           v                              |
             |  +---------------------------+           |
             |  | ConfigDBClient (DB 4)     |           |
             |  | + PipelineClient (v3)     |           |
             |  +---------------------------+           |
             |  +---------------------------+           |
             |  | StateDBClient  (DB 6)     |           |
             |  +---------------------------+           |
             |  +---------------------------+           |
             |  | PlatformConfig (v3)       |           |
             |  | (from device platform.json|           |
             |  |  via SSH)                 |           |
             |  +---------------------------+           |
             +-----------------------------------------+
```

### 3.2 Object Hierarchy (OO Design)

The system uses an object-oriented design with parent references, mirroring the original Perl architecture:

```
+------------------------------------------------------------------+
|                         Network                                   |
|  (top-level object - owns all specs)                             |
|                                                                   |
|  - NetworkSpecFile (services, filters, regions, permissions)     |
|  - SiteSpecFile (site topology, route reflector names)           |
|  - PlatformSpecFile (hardware platform definitions)              |
|                                                                   |
|  Methods: GetService(), GetFilterSpec(), GetRegion(), etc.       |
+------------------------------------------------------------------+
         |
         | creates (with parent reference)
         v
+------------------------------------------------------------------+
|                         Device                                    |
|  (created in Network's context)                                  |
|                                                                   |
|  - parent: *Network  <-- key: access to all Network specs        |
|  - DeviceProfile                                                 |
|  - ResolvedProfile (after inheritance)                           |
|  - ConfigDB (from Redis) <-- actual device config                |
|                                                                   |
|  Methods: Network(), ASNumber(), BGPNeighbors(), etc.            |
+------------------------------------------------------------------+
         |
         | creates (with parent reference)
         v
+------------------------------------------------------------------+
|                        Interface                                  |
|  (created in Device's context)                                   |
|                                                                   |
|  - parent: *Device   <-- key: access to Device AND Network       |
|  - Interface state (from config_db)                              |
|                                                                   |
|  Methods: Device(), Network(), HasService(), etc.                |
+------------------------------------------------------------------+
```

## 4. Component Description

### 4.1 CLI Layer (`cmd/newtron`)

The command-line interface built with Cobra framework.

**CLI Pattern (Object-Oriented):**
```
newtron -n <network> -d <device> -i <interface> <verb> [args] [-x]
```

**Context Flags (Object Selection):**
| Flag | Description | Object Level |
|------|-------------|--------------|
| `-n, --network` | Network specs name | Network object |
| `-d, --device` | Target device name | Device object |
| `-i, --interface` | Target interface/LAG/VLAN | Interface object |

**Interface Name Formats:**
| Short Form | Full Form (SONiC) |
|------------|-------------------|
| `Eth0` | `Ethernet0` |
| `Po100` | `PortChannel100` |
| `Vl100` | `Vlan100` |
| `Lo0` | `Loopback0` |

### 4.2 Network Layer (`pkg/network`)

The top-level object hierarchy that provides OO access to specs and devices.

**Key Types:**
| Type | Description |
|------|-------------|
| `Network` | Top-level object; owns specs, creates Devices |
| `Device` | Device with parent reference to Network; creates Interfaces |
| `Interface` | Interface with parent reference to Device |

### 4.3 Spec Layer (`pkg/spec`)

Loads and resolves specifications from JSON files.

**Spec Types:**
| Type | Description |
|------|-------------|
| `NetworkSpecFile` | Services, filters, VPNs, regions, permissions |
| `SiteSpecFile` | Site topology, route reflector assignments |
| `PlatformSpecFile` | Hardware platform definitions (HWSKU, ports) |
| `TopologySpecFile` | (optional) Topology: devices, interfaces, service bindings, links |
| `DeviceProfile` | Per-device settings (mgmt_ip, loopback_ip, site, ssh_user, ssh_pass) |
| `ResolvedProfile` | Profile after inheritance resolution |

**v3 additions to spec types:**

| Type | Addition | Description |
|------|----------|-------------|
| `RoutingSpec` | `ImportCommunity` | BGP community for import filtering |
| `RoutingSpec` | `ExportCommunity` | BGP community to attach on export |
| `RoutingSpec` | `ImportPrefixList` | Prefix-list reference for import filtering |
| `RoutingSpec` | `ExportPrefixList` | Prefix-list reference for export filtering |
| `RoutingSpec` | `Redistribute` | Override default redistribution (service=true, transit=false) |
| `SiteSpec` | `ClusterID` | BGP route reflector cluster ID (defaults to spine loopback if unset) |

Community and prefix-list compose as AND conditions — a route must match both to be accepted.

**Key Distinction**: The spec layer handles **declarative intent**. It never contains concrete device configuration - only policy definitions and references.

### 4.4 Device Layer (`pkg/device`)

Low-level connection management for SONiC switches.

**Key Components:**
- `Device`: Holds Redis connections (ConfigDBClient, StateDBClient), optional SSHTunnel, lock state, and thread-safe mutex
- `ConfigDB`: SONiC config_db structure (PORT, VLAN, VRF, ACL_TABLE, etc.)
- `ConfigDBClient`: Redis client wrapper for CONFIG_DB (Redis DB 4)
- `StateDB`: SONiC state_db structure (PortTable, LAGTable, BGPNeighborTable, etc.)
- `StateDBClient`: Redis client wrapper for STATE_DB (Redis DB 6)
- `AppDBClient` (v5): Redis client wrapper for APP_DB (Redis DB 0) — routing state from FRR/fpmsyncd
- `AsicDBClient` (v5): Redis client wrapper for ASIC_DB (Redis DB 1) — SAI objects from orchagent
- `SSHTunnel`: SSH port-forward tunnel for Redis access through port 22
- `PlatformConfig` (v3): Parsed representation of SONiC's `platform.json`, cached on device; provides port definitions, lane assignments, supported speeds, and breakout modes for port validation
- `CompositeBuilder` (v3): Builder for offline composite CONFIG_DB generation (in `pkg/network/composite.go`)
- `PipelineClient` (v3): Redis MULTI/EXEC pipeline for atomic multi-entry writes (used by composite delivery)

**Key Distinction**: The device layer handles **imperative config**. It reads/writes the actual device state.

### 4.5 Translation (in `pkg/network`)

The translation from spec to config happens in `Interface.ApplyService()` and related methods:

```go
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    // 1. Load spec (declarative)
    svc, _ := i.Network().GetService(serviceName)

    // 2. Translate with context
    vrfName := deriveVRFName(svc, i.name)           // spec + context -> config
    peerIP, _ := util.DeriveNeighborIP(opts.IPAddress)  // derive from interface IP
    localAS := i.Device().Resolved().ASNumber       // from device profile

    // 3. Generate config (imperative)
    cs.Add("VRF", vrfName, ChangeAdd, nil, vrfConfig)
    cs.Add("BGP_NEIGHBOR", peerIP, ChangeAdd, nil, neighborConfig)
    cs.Add("ACL_TABLE", aclName, ChangeAdd, nil, aclConfig)

    return cs, nil
}
```

### 4.6 BGP Management Architecture

#### 4.6.1 SONiC BGP Management Modes

SONiC supports three BGP management modes:

| Mode | Config Flag | Daemon | Description |
|------|------------|--------|-------------|
| **Split** | `frr_split_config_enabled=true` | FRR reads `frr.conf` | CONFIG_DB BGP tables ignored. Direct FRR config file management. |
| **Legacy unified** | (default) | `bgpcfgd` | Jinja templates translate CONFIG_DB to FRR. Limited coverage: missing peer groups, route-maps, redistribute, max-paths, cluster-id. |
| **FRR management framework** | `frr_mgmt_framework_config=true` | `frrcfgd` | Full CONFIG_DB → FRR translation. Supports all needed features. Stable since SONiC 202305+. |

The lab runs SONiC 202411 (Gibraltar). Newtron v3 switches from split mode to frrcfgd, enabling full BGP management through CONFIG_DB.

#### 4.6.2 Why frrcfgd

The FRR management framework (`frrcfgd`) provides:
- Complete CONFIG_DB coverage for BGP features (peer groups, route-maps, redistribute, prefix-lists, community-sets, AS-path filters)
- Declarative model that aligns with newtron's spec → config translation
- Atomic CONFIG_DB writes that frrcfgd translates to FRR commands
- No direct `frr.conf` file management needed
- Consistent management of underlay, IPv6, and EVPN overlay through a single mechanism

#### 4.6.3 New CONFIG_DB Tables (9 tables)

| Table | Key Format | Purpose |
|-------|-----------|---------|
| `ROUTE_REDISTRIBUTE` | `vrf\|src_protocol\|dst_protocol\|address_family` | Connected/static redistribution into BGP (dst_protocol is always `bgp`) |
| `ROUTE_MAP` | `map_name\|seq` | Route-map rules with match/set clauses |
| `BGP_PEER_GROUP` | `peer_group_name` | BGP peer group templates |
| `BGP_PEER_GROUP_AF` | `peer_group_name\|address_family` | Per-AF settings for peer groups |
| `BGP_GLOBALS_AF_NETWORK` | `vrf\|address_family\|prefix` | BGP `network` statement |
| `BGP_GLOBALS_AF_AGGREGATE_ADDR` | `vrf\|address_family\|prefix` | BGP aggregate-address |
| `PREFIX_SET` | `set_name\|seq` | IP prefix lists for route-map match |
| `COMMUNITY_SET` | `set_name` | BGP community lists |
| `AS_PATH_SET` | `set_name` | AS-path regex filters |

#### 4.6.4 Extended Fields on Existing Tables

**BGP_GLOBALS** (`default` or VRF name):
- `load_balance_mp_relax` — multipath relax (needed for ECMP across neighbor ASNs)
- `rr_cluster_id` — route reflector cluster ID
- `ebgp_requires_policy` — disable mandatory eBGP policy (FRR 8.x default)
- `default_ipv4_unicast` — disable auto-activation of IPv4 unicast
- `log_neighbor_changes` — log neighbor state transitions
- `suppress_fib_pending` — suppress routes until FIB install confirmed

**BGP_GLOBALS_AF** (`vrf|address_family`):
- `max_ebgp_paths` — maximum ECMP paths for eBGP
- `max_ibgp_paths` — maximum ECMP paths for iBGP

**BGP_NEIGHBOR**:
- `peer_group` — assign neighbor to peer group template
- `ebgp_multihop` — TTL for multihop eBGP
- `password` — MD5 authentication

**BGP_NEIGHBOR_AF**:
- `allowas_in` — allow local AS in received path
- `route_map_in` / `route_map_out` — inbound/outbound route-maps
- `prefix_list_in` / `prefix_list_out` — prefix filters
- `default_originate` — advertise default route to neighbor
- `addpath_tx_all_paths` — send all paths to neighbor

#### 4.6.5 SetupRouteReflector

`SetupRouteReflector` replaces the v2 `SetupBGPEVPN` operation (which only activated `l2vpn_evpn`). The new operation configures full multi-AF route reflection:

```
SetupRouteReflector(ctx, neighbors []string)
  |
  +-- BGP_GLOBALS "default":
  |     local_asn, router_id, rr_cluster_id (from SiteSpec)
  |
  +-- For each neighbor:
  |     BGP_NEIGHBOR: asn, local_addr (loopback), admin_status
  |     BGP_NEIGHBOR_AF "<ip>|ipv4_unicast":
  |       activate, route_reflector_client, next_hop_self
  |     BGP_NEIGHBOR_AF "<ip>|ipv6_unicast":
  |       activate, route_reflector_client, next_hop_self
  |     BGP_NEIGHBOR_AF "<ip>|l2vpn_evpn":
  |       activate, route_reflector_client
  |
  +-- BGP_GLOBALS_AF "default|ipv4_unicast": max_ibgp_paths
  +-- BGP_GLOBALS_AF "default|ipv6_unicast": max_ibgp_paths
  +-- BGP_GLOBALS_AF "default|l2vpn_evpn": advertise-all-vni
  |
  +-- ROUTE_REDISTRIBUTE "default|connected|bgp|ipv4": (loopback + service subnets)
  +-- ROUTE_REDISTRIBUTE "default|connected|bgp|ipv6": (if IPv6 enabled)
```

#### 4.6.6 Route Redistribution Defaults

Redistribution follows opinionated defaults:
- **Service interfaces**: redistribute connected subnets into BGP (default)
- **Loopback**: always redistributed (needed for BGP router-id reachability)
- **Transit interfaces**: NOT redistributed by default (fabric underlay uses direct BGP)
- Service spec flag `redistribute` controls per-service override

#### 4.6.7 New BGP Operations

**Device-level operations:**

| Operation | Description |
|-----------|-------------|
| `SetBGPGlobals` | Configure BGP global settings (ASN, router-id, flags) |
| `SetupRouteReflector` | Full RR setup: all 3 AFs, cluster-id, RR client, next-hop-self |
| `ConfigurePeerGroup` | Create/update a BGP peer group template |
| `DeletePeerGroup` | Remove a peer group |
| `AddRouteRedistribution` | Redistribute connected/static into BGP |
| `RemoveRouteRedistribution` | Remove redistribution |
| `AddRouteMap` | Create route-map with match/set rules |
| `DeleteRouteMap` | Remove route-map |
| `AddPrefixSet` | Create prefix list for route-map matching |
| `DeletePrefixSet` | Remove prefix list |
| `AddBGPNetwork` | Add BGP `network` statement |
| `RemoveBGPNetwork` | Remove BGP `network` statement |

**Interface-level operations:**

| Operation | Description |
|-----------|-------------|
| `SetRouteMap` | Bind route-map to BGP neighbor in/out direction |

### 4.7 Composite Mode

#### 4.7.1 Concept

Composite mode generates a composite CONFIG_DB configuration offline (without connecting to a device), then delivers it to the device as a single atomic operation once the device is up. This replaces the configlet-based baseline approach from labgen with a newtron-native mechanism.

#### 4.7.2 Two Delivery Modes

| Mode | Behavior | Use case |
|------|----------|----------|
| **Overwrite** | Replace entire CONFIG_DB with composite content | Initial device provisioning, lab setup |
| **Merge** | Add entries to existing CONFIG_DB | Incremental service deployment |

#### 4.7.3 Merge Restrictions

Merge is **only supported for interface-level service configuration**, and **only if the target interface has no existing service binding** (checked via NEWTRON_SERVICE_BINDING table). This prevents:
- Overwriting incompatible configurations
- Partial merges that leave the device in an inconsistent state
- Conflicts between existing and new service configs

Merge can be used for:
- Applying a service to an interface (creates VRF, ACLs, IP, BGP, EVPN entries)
- Removing a service from an interface (deletes the same entries)

#### 4.7.4 Architecture

```
CompositeBuilder (offline)
    |
    +-- AddBGPGlobals(...)
    +-- AddPeerGroup(...)
    +-- AddPortConfig(...)
    +-- AddService(interface, service, opts)
    +-- AddRouteRedistribution(...)
    |
    v
CompositeConfig (composite CONFIG_DB)
    |
    +-- Tables: map[table]map[key]map[field]string
    +-- Metadata: timestamp, network, device, mode
    |
    v
Device.DeliverComposite(composite, mode)
    |
    +-- Overwrite: config reload-from-composite (Redis MULTI/pipeline)
    +-- Merge: validate → pipeline write (atomic)
```

#### 4.7.5 Redis Pipeline for Atomic Delivery

Current `ApplyChanges()` writes entries sequentially (one HSet per field). Composite delivery uses a Redis pipeline:

```
Pipeline:
  MULTI
  HSET TABLE|key1 field1 val1 field2 val2 ...
  HSET TABLE|key2 field1 val1 ...
  DEL TABLE|key3
  EXEC
```

This ensures either all changes apply or none do. The pipeline also improves performance for large changesets (many round-trips → one round-trip).

### 4.8 Topology Provisioning

#### 4.8.1 Concept

Topology provisioning automates device configuration from a `topology.json` spec file. Instead of manually applying services to interfaces, the topology spec declares the complete desired state — which services bind to which interfaces, with all required parameters (IP addresses, peer AS numbers). The provisioner generates and delivers the configuration automatically.

#### 4.8.2 Two Provisioning Modes

| Mode | Method | Delivery | Device Connection | Use Case |
|------|--------|----------|-------------------|----------|
| **Full device** | `ProvisionDevice` | `CompositeOverwrite` | Connect only for delivery | Initial provisioning, lab setup |
| **Per-interface** | `ProvisionInterface` | `ApplyService` | Full connect + interrogate | Add service to one interface on running device |

#### 4.8.3 Full Device Provisioning Flow

```
ProvisionDevice("leaf1-dc1")
  |
  +-- 1. Load device profile + resolve (no device connection)
  +-- 2. Create CompositeBuilder(deviceName, CompositeOverwrite)
  +-- 3. Generate device-level entries:
  |     PORT, LOOPBACK_INTERFACE, DEVICE_METADATA,
  |     BGP_GLOBALS, VXLAN_TUNNEL, BGP_NEIGHBOR (RRs),
  |     ROUTE_REDISTRIBUTE
  +-- 4. For each interface in topology:
  |     Look up ServiceSpec → generate CONFIG_DB entries
  |     (VRF, ACL, IP, BGP neighbor, EVPN mappings,
  |      NEWTRON_SERVICE_BINDING)
  +-- 5. Build CompositeConfig
  +-- 6. Connect → Lock → DeliverComposite → Unlock → Disconnect
```

#### 4.8.4 Per-Interface Provisioning

Per-interface provisioning reads the service name and parameters from topology.json but uses the standard `Interface.ApplyService()` path. This means it:
- Connects to the device and loads current state
- Validates preconditions (interface exists, no existing service binding)
- Applies the service with full dependency checking

This mode is used for incremental changes to a running device — adding a service to one interface while leaving the rest untouched.

#### 4.8.5 topology.json Structure

```json
{
  "version": "1.0",
  "description": "DC1 lab topology",
  "devices": {
    "leaf1-dc1": {
      "device_config": { "route_reflector": false },
      "interfaces": {
        "Ethernet0": {
          "link": "spine1-dc1:Ethernet0",
          "service": "fabric-underlay",
          "ip": "10.10.1.1/31"
        },
        "Ethernet4": {
          "service": "customer-l3",
          "ip": "10.1.1.1/30",
          "params": { "peer_as": "65100" }
        }
      }
    }
  },
  "links": [
    {"a": "spine1-dc1:Ethernet0", "z": "leaf1-dc1:Ethernet0"}
  ]
}
```

- `devices` keys are device names (must match profiles in `profiles/`)
- `device_config` is optional (e.g., `route_reflector: true` for spines)
- `interfaces` keys are full SONiC interface names
- `service` references a service from network.json
- `ip` is the IP address to assign (equivalent to `--ip` at CLI)
- `params` holds service-specific parameters (e.g., `peer_as`)
- `links` section is optional, used for validation (both ends must be defined)

### 4.9 Verification Architecture

#### 4.9.1 Principles

**If a tool changes the state of an entity, that same tool must be able to verify the change had the intended effect.** Verification is not a separate concern from provisioning — it is the completion of provisioning. An operation that cannot confirm its own result is half an operation. The caller should not need a second tool to find out if the first tool worked.

This is why newtron owns verification for everything it changes:
- newtron writes CONFIG_DB entries → newtron verifies they landed (`VerifyChangeSet`)
- newtron configures BGP and redistribution → newtron can read the resulting routes from APP_DB (`GetRoute`) to confirm the intended local effect
- newtron sets up EVPN/VXLAN → newtron can check ASIC_DB (`GetRouteASIC`) to confirm orchagent programmed the route

Orchestrators like newtest then build on newtron's self-verification: they use newtron to confirm each device's local state, and add the cross-device layer that newtron cannot see (did the route propagate to the neighbor).

**For cross-device observations**: newtron returns structured data, not verdicts. If a check requires knowing what another device should have, it belongs in the orchestrator.

#### 4.9.2 ChangeSet Verification

Every mutating operation (`ApplyService`, `CreateVLAN`, `SetupRouteReflector`, `DeliverComposite`, etc.) returns a `ChangeSet` — a list of CONFIG_DB entries that were written. The ChangeSet is the operation's own contract for what the device state should look like after execution.

`VerifyChangeSet` re-reads CONFIG_DB through a fresh connection and confirms every entry in the ChangeSet was applied correctly:

```go
// VerifyChangeSet re-reads CONFIG_DB and confirms every entry in the
// ChangeSet was applied. Returns a VerificationResult listing any
// missing or mismatched entries.
func (d *Device) VerifyChangeSet(ctx context.Context, cs *ChangeSet) (*VerificationResult, error)
```

This works for all operations — disaggregated (`CreateVLAN`) or composite (`DeliverComposite`) — because they all produce ChangeSets.

```go
// VerificationResult reports ChangeSet verification outcome.
type VerificationResult struct {
    Passed   int                // entries that matched
    Failed   int                // entries missing or mismatched
    Errors   []VerificationError // details of each failure
}

type VerificationError struct {
    Table    string
    Key      string
    Field    string
    Expected string
    Actual   string // "" if missing
}
```

#### 4.9.3 Routing State Observation

newtron provides read access to a device's routing tables. These are observation primitives — they return structured data, not pass/fail verdicts.

**APP_DB (Redis DB 0)** — routes installed by FRR via `fpmsyncd`:

```go
// RouteEntry represents a route read from a device's routing table.
type RouteEntry struct {
    Prefix   string    // "10.1.0.0/31"
    VRF      string    // "default", "Vrf-customer"
    Protocol string    // "bgp", "connected", "static"
    NextHops []NextHop
    Source   RouteSource // AppDB or AsicDB
}

type NextHop struct {
    IP        string // "10.0.0.1" (or "0.0.0.0" for connected)
    Interface string // "Ethernet0", "Vlan500"
}

// GetRoute reads a route from APP_DB (Redis DB 0).
// Returns nil if the prefix is not present (not an error — route
// may not have converged yet).
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
```

**ASIC_DB (Redis DB 1)** — routes programmed in the ASIC by `orchagent`:

```go
// GetRouteASIC reads a route from ASIC_DB by resolving the SAI
// object chain (SAI_ROUTE_ENTRY → SAI_NEXT_HOP_GROUP → SAI_NEXT_HOP).
// Returns nil if not programmed in ASIC.
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
```

APP_DB tells you what FRR computed. ASIC_DB tells you what the hardware (or ASIC simulator) actually installed. The gap between them is `orchagent` processing.

These primitives are building blocks for external orchestrators (newtest) that have topology-wide context. For example, newtest can connect to spine1 via newtron and call `GetRoute("default", "10.1.0.0/31")` to confirm that leaf1's connected subnet arrived via BGP with the expected next-hop. newtron provides the read; newtest knows what to expect.

#### 4.9.4 Health Checks

The existing `RunHealthChecks()` method (§4.4) provides operational status: BGP sessions Established, interfaces oper-up, LAG members active, VXLAN tunnels healthy. These are local device observations that complement ChangeSet verification and routing state queries.

#### 4.9.5 What newtron Does NOT Verify

newtron operates on a single device. It cannot verify:
- **Route propagation** — "did leaf1's route arrive at spine1?" (requires connecting to spine1)
- **Fabric convergence** — "do all devices agree on routing state?" (requires connecting to all devices)
- **Data-plane forwarding** — "can traffic flow between VMs?" (requires multi-device packet injection)

These require topology-wide context and belong in the orchestrator (newtest).

#### 4.9.6 Verification Summary

| Capability | Method | Database | Returns |
|-----------|--------|----------|---------|
| CONFIG_DB writes landed | `VerifyChangeSet(cs)` | CONFIG_DB (DB 4) | Pass/fail with diff |
| Route installed by FRR | `GetRoute(vrf, prefix)` | APP_DB (DB 0) | RouteEntry or nil |
| Route programmed in ASIC | `GetRouteASIC(vrf, prefix)` | ASIC_DB (DB 1) | RouteEntry or nil |
| Operational health | `RunHealthChecks()` | STATE_DB (DB 6) | Health report |

## 5. Device Connection Architecture

### 5.1 Connection Flow

When `Device.Connect()` is called, the connection path depends on whether SSH credentials are present in the device's `ResolvedProfile`:

```
                    Connect(ctx)
                        |
                        v
              SSHUser + SSHPass set?
                 /              \
               yes                no
                |                  |
                v                  v
    NewSSHTunnel(host,         addr = "<MgmtIP>:6379"
      user, pass, port)        (direct Redis)
    addr = tunnel.LocalAddr()
    ("127.0.0.1:<random>")
                \                /
                 \              /
                  v            v
          NewConfigDBClient(addr)  -- connects to Redis DB 4
          client.Connect()
          client.GetAll()          -- loads full CONFIG_DB
                  |
                  v
          NewStateDBClient(addr)   -- connects to Redis DB 6
          stateClient.Connect()    -- non-fatal on failure
          stateClient.GetAll()     -- loads STATE_DB
          PopulateDeviceState()    -- merges state from both DBs
```

**With SSH tunnel (production/E2E)**: SSH credentials (`ssh_user`, `ssh_pass`) are present in the device profile. The tunnel dials the device on port 22 and forwards a local random port to `127.0.0.1:6379` inside the device. Redis on SONiC listens only on localhost; port 6379 is not forwarded by QEMU, so SSH is the only path in.

**Without SSH tunnel (integration tests)**: No SSH credentials in the profile. The `MgmtIP` points directly at a standalone Redis container (`newtron-test-redis`). This mode is used by integration tests (`-tags integration`) where a plain Redis container is seeded with test fixtures, avoiding the need for a running SONiC device.

### 5.2 SSH Tunnel Implementation

The `SSHTunnel` struct (`pkg/device/tunnel.go`) implements a TCP port forwarder over SSH:

```
                  Newtron Process                         SONiC Device
           +----------------------------+         +-------------------------+
           |                            |         |                         |
           |  ConfigDBClient            |   SSH   |                         |
           |      |                     |  (tcp)  |                         |
           |      v                     | ------> |   sshd (:22)            |
           |  127.0.0.1:<random-port>   |         |      |                  |
           |      |                     |         |      v                  |
           |  net.Listener.Accept()     |         |  127.0.0.1:6379         |
           |      |                     |         |      (Redis)            |
           |  io.Copy <-> io.Copy       |         |                         |
           |  (bidirectional forward)   |         |                         |
           +----------------------------+         +-------------------------+
```

1. `NewSSHTunnel(host, user, pass, port)` dials `host:port` (default 22) with password auth
2. Opens `net.Listen("tcp", "127.0.0.1:0")` to get a random local port
3. Starts `acceptLoop()` goroutine that accepts local connections
4. Each accepted connection spawns `forward()` which calls `sshClient.Dial("tcp", "127.0.0.1:6379")` and runs two `io.Copy` goroutines for bidirectional data transfer
5. `LocalAddr()` returns the local address (e.g. `"127.0.0.1:54321"`) that ConfigDBClient and StateDBClient connect to
6. `Close()` signals `done`, closes the listener, waits for all goroutines via `sync.WaitGroup`, and closes the SSH client

### 5.3 Disconnect Sequence

`Device.Disconnect()` tears down in order:

1. Release device lock if held
2. Close ConfigDBClient (Redis connection)
3. Close StateDBClient (Redis connection)
4. Close SSHTunnel (if present): stops accept loop, waits for goroutines, closes SSH session

## 6. StateDB Access

### 6.1 Overview

STATE_DB (Redis DB 6) provides read-only operational state that supplements the configuration view from CONFIG_DB. The device layer reads STATE_DB to populate the `DeviceState` struct which provides a merged view of configuration and operational data.

### 6.2 Available State Data

| State Type | Source Table | Information |
|------------|-------------|-------------|
| Interface operational status | `PORT_TABLE` | `oper_status` (up/down), speed, MTU |
| LAG state | `LAG_TABLE` | Active/inactive members, operational status |
| LAG member state | `LAG_MEMBER_TABLE` | Per-member LACP state |
| BGP neighbor state | `BGP_NEIGHBOR_TABLE` | Session state (Established/Idle), prefix counts, uptime |
| VXLAN tunnel state | `VXLAN_TUNNEL_TABLE` | VTEP operational status, remote VTEPs |

### 6.3 Non-Fatal Connection Failure

STATE_DB connection failure is intentionally non-fatal. If `StateDBClient.Connect()` or `GetAll()` fails, the device logs a warning and continues operating with `StateDB == nil`. This means:

- The system can still read and write CONFIG_DB
- Operational state queries (e.g. `GetInterfaceOperState()`) return errors indicating state_db is not loaded
- `HasStateDB()` returns `false`, allowing callers to check availability before querying

This design supports environments where STATE_DB may not be fully populated (e.g. early boot, minimal test fixtures).

### 6.4 State Refresh

`RefreshState(ctx)` reloads only STATE_DB without touching CONFIG_DB or re-establishing the connection. `Reload(ctx)` reloads both CONFIG_DB and STATE_DB. Both methods call `PopulateDeviceState()` to re-merge the state view.

## 7. Config Persistence

### 7.1 Runtime vs Persistent Configuration

SONiC uses a dual-state configuration model:

```
+---------------------+                    +---------------------+
|  /etc/sonic/         |   SONiC boot       |   Redis             |
|  config_db.json      | ----------------> |   CONFIG_DB (DB 4)  |
|  (persistent)        |   loads into       |   (runtime)         |
+---------------------+                    +---------------------+
                                                     ^
                                                     |
                                              Newtron writes here
                                              (runtime only)
```

- **Redis CONFIG_DB** is the runtime configuration store. SONiC daemons (orchagent, bgpcfgd, etc.) subscribe to Redis keyspace notifications and react to changes in real time.
- **`/etc/sonic/config_db.json`** is the persistent configuration file. SONiC reads this file on boot and writes its contents into Redis.

### 7.2 Newtron's Role

Newtron writes exclusively to Redis CONFIG_DB. These changes take effect immediately because SONiC daemons process them in real time. However, these changes are **ephemeral** - they are lost if the switch reboots.

To persist changes across reboots, the operator must run `config save -y` on the SONiC device, which writes the current Redis state to `/etc/sonic/config_db.json`. Persistence is the operator's responsibility; Newtron does not invoke `config save`.

### 7.3 Implications

| Scenario | Behavior |
|----------|----------|
| Newtron writes to Redis | Change takes effect immediately (SONiC daemons react) |
| Switch reboots without `config save` | Changes are lost; device returns to last saved config |
| `config save -y` after Newtron changes | Changes are persisted to config_db.json |
| SONiC boots | Loads config_db.json into Redis; starts daemons |

### 7.4 Composite Mode and Config Persistence

Composite mode writes to the same runtime Redis CONFIG_DB. The persistence model is identical: composite-delivered changes take effect immediately but are lost on reboot unless `config save -y` is run.

For **overwrite** mode (initial provisioning), the expectation is that the operator runs `config save -y` after delivery to establish the baseline persistent config. For **merge** mode (incremental service deployment), the same `config save` responsibility applies.

## 8. Service Model

Services are the primary abstraction - they bundle intent into reusable templates.

### 8.1 Service Spec Structure

```json
{
  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface",
      "service_type": "l3",
      "ipvpn": "customer-vpn",
      "vrf_type": "interface",
      "routing": {
        "protocol": "bgp",
        "peer_as": "request",
        "import_policy": "customer-import",
        "export_policy": "customer-export"
      },
      "ingress_filter": "customer-edge-in",
      "egress_filter": "customer-edge-out"
    }
  }
}
```

**What's in the spec (intent):**
- Service type (l2, l3, irb)
- VPN reference (name of ipvpn/macvpn definition)
- VRF instantiation policy (per-interface or shared)
- Routing protocol and policies
- Filter references (names, not rules)
- QoS profile reference
- Anycast gateway (for IRB)

**What's NOT in the spec (derived at runtime):**
- Peer IP (derived from interface IP)
- VRF name (generated from service + interface)
- ACL table names (generated)
- Specific ACL rule numbers (from filter-spec expansion)

### 8.2 Routing Spec

The `routing` section declares routing intent:

```json
{
  "routing": {
    "protocol": "bgp",
    "peer_as": "request",
    "import_policy": "customer-import",
    "export_policy": "customer-export",
    "import_community": "65000:100",
    "export_community": "65000:200",
    "import_prefix_list": "customer-prefixes",
    "export_prefix_list": "customer-export-prefixes",
    "redistribute": true
  }
}
```

| Field | Values | Description |
|-------|--------|-------------|
| `protocol` | `"bgp"`, `"static"`, `""` | Routing protocol |
| `peer_as` | Number or `"request"` | Peer AS (fixed or user-provided) |
| `import_policy` | Policy name | BGP import route policy |
| `export_policy` | Policy name | BGP export route policy |
| `import_community` | Community string | BGP community for import filtering (v3) |
| `export_community` | Community string | BGP community to attach on export (v3) |
| `import_prefix_list` | Prefix-list name | Prefix-list reference for import filtering (v3) |
| `export_prefix_list` | Prefix-list name | Prefix-list reference for export filtering (v3) |
| `redistribute` | `true` / `false` / omit | Override default redistribution behavior (v3) |

**v3 filtering composition**: Community and prefix-list can be used together. They compose as AND conditions — a route must match both the community and the prefix-list to be accepted. Policies are defined in NetworkSpecFile (`route_policies`, `prefix_lists`), referenced by name in the RoutingSpec struct.

**v3 redistribution defaults**:
- Service interfaces: redistribute connected subnets into BGP (default `true`)
- Transit interfaces: do NOT redistribute (default `false`)
- Loopback: always redistributed
- Set `redistribute` explicitly to override the default for any service

**Derived at runtime:**
- Peer IP (from interface IP for /30 or /31)
- Local AS (from device profile)
- Local address (interface IP)

### 8.3 Service Types

| Type | Description | Generated Config |
|------|-------------|------------------|
| `l2` | VLAN bridging | VLAN, L2VNI mapping |
| `l3` | Routed interface | VRF, L3VNI mapping, BGP neighbor |
| `irb` | VLAN with routing | VLAN + VRF + SVI + anycast gateway |

### 8.4 VRF Instantiation (vrf_type)

| Value | VRF Name | Use Case |
|-------|----------|----------|
| `"interface"` | `{service}-{interface}` | Per-customer isolation |
| `"shared"` | ipvpn definition name | Multiple interfaces share one VRF |
| (omitted) | Global routing table | Transit (no EVPN) |

## 9. Specification Files

### 9.1 File Structure

```
/etc/newtron/
+-- specs/
|   +-- network.json      # Services, filters, VPNs, regions
|   +-- site.json         # Site topology
|   +-- platforms.json    # Hardware platform definitions
|   +-- topology.json     # (optional) Topology spec for automated provisioning
|   +-- profiles/         # Per-device profiles
|       +-- leaf1-ny.json
|       +-- leaf2-ny.json
|       +-- spine1-ny.json
+-- configlets/           # Baseline templates
```

### 9.2 Single Source of Truth

| Data | Source | Used By |
|------|--------|---------|
| `loopback_ip` | Device profile | BGP router-id, VTEP source |
| `mgmt_ip` | Device profile | Redis connection (or SSH tunnel target) |
| `as_number` | Region (or profile override) | BGP local AS |
| `route_reflectors` | site.json | BGP neighbor derivation |
| `ssh_user` / `ssh_pass` | Device profile | SSH tunnel for Redis access |

### 9.3 PlatformSpec Auto-Generation (v3)

The newtron `PlatformSpec` is an abstraction (HWSKU, port_count, default_speed, breakouts). Detailed per-port lane/speed information comes from the device's own `platform.json` at runtime. A new `GeneratePlatformSpec()` device method creates/updates the PlatformSpec from the device's platform.json on first connect to a new device model:

```
Device.GeneratePlatformSpec()
  |
  +-- SSH: cat /usr/share/sonic/device/<platform>/platform.json
  +-- Parse: extract port count, default speed, breakout modes
  +-- Return: spec.PlatformSpec ready to write to platforms.json
```

This should be run at least on first connect to a new device model, and can be used to prime the spec system for a new hardware platform.

### 9.4 SiteSpec ClusterID (v3)

The `SiteSpec` struct gains a `cluster_id` field for BGP route reflector cluster ID:

| Field | Description |
|-------|-------------|
| `cluster_id` | BGP RR cluster-id. If not set, defaults to the spine's loopback IP. |

`SetupRouteReflector` reads `cluster_id` from SiteSpec. If not set, it falls back to the current labgen behavior (spine loopback IP).

### 9.5 Spec Inheritance

```
Global (network.json)
    | overrides
Region (network.json -> regions.{name})
    | overrides
Device Profile (profiles/{device}.json)
    | resolves to
ResolvedProfile (runtime)
```

| Field | Global | Region | Profile | Resolved |
|-------|--------|--------|---------|----------|
| `as_number` | - | 13908 | (not set) | **13908** |
| `as_number` | - | 13908 | 65535 | **65535** |
| `affinity` | "flat" | "east" | "west" | **"west"** |

## 10. EVPN/VXLAN Architecture

**v3 change**: The BGP EVPN underlay is now managed via CONFIG_DB through frrcfgd instead of direct `frr.conf` files. `SetupRouteReflector` replaces `SetupBGPEVPN`, activating all three address families (ipv4_unicast, ipv6_unicast, l2vpn_evpn) rather than just l2vpn_evpn. The underlay, IPv6, and overlay are now managed through a single consistent mechanism.

### 10.1 Overlay Design

```
         Spine (Route Reflector)
              /        \
             /          \
        Leaf-1          Leaf-2
        L3VNI: 3001     L3VNI: 3001  (same VRF)
        L2VNI: 1001     L2VNI: 1001  (same VLAN)
           |               |
        VLAN 100        VLAN 100
        SVI: 10.1.100.1 SVI: 10.1.100.1 (anycast gateway)
```

### 10.2 Route Types

| Type | Name | Purpose |
|------|------|---------|
| Type-2 | MAC/IP Advertisement | L2 MAC learning |
| Type-3 | Inclusive Multicast | BUM traffic |
| Type-5 | IP Prefix | L3 routing |

## 11. Security Model

### 11.1 Transport Security

Redis on SONiC has no authentication and listens only on `127.0.0.1:6379`. Port 6379 is not forwarded by QEMU in virtual environments and is not exposed on the management interface. SSH is the transport security layer: all Redis access from Newtron goes through an SSH tunnel authenticated with username/password credentials stored in the device profile. The SSH tunnel ensures that Redis traffic is encrypted in transit and access is gated by SSH authentication.

In integration test environments, a standalone Redis container is used without SSH (direct TCP on port 6379). This is acceptable because the test Redis contains only synthetic fixtures, not production data.

### 11.2 Permission Levels

| Permission | Description |
|------------|-------------|
| `service.apply` | Apply services |
| `service.remove` | Remove services |
| `interface.configure` | Configure interfaces |
| `lag.create` | Create LAGs |
| `vlan.create` | Create VLANs |
| `acl.modify` | Modify ACLs |
| `bgp.modify` | Modify BGP |
| `all` | Superuser |

### 11.3 Audit Logging

All operations logged with:
- Timestamp, User, Device
- Operation name
- Changes made
- Success/failure
- Execution mode (dry-run or execute)

## 12. Execution Modes

### 12.1 Dry-Run (Default)

- Validation and preview
- No changes written
- Safe for verification

### 12.2 Execute (`-x` flag)

- Validation first
- Changes written to config_db
- Audit log entry created

### 12.3 Composite (v3)

- Offline composite CONFIG_DB generation (no device connection required)
- CompositeBuilder constructs the configuration programmatically
- Delivery is a separate step: `Device.DeliverComposite(composite, mode)`
- Two delivery modes: **overwrite** (full replace) or **merge** (interface services only)
- Delivery uses Redis pipeline (MULTI/EXEC) for atomic application
- Audit log entry created on delivery

| Mode | Online? | Device Required? | Atomic? |
|------|---------|-----------------|---------|
| Dry-run | Yes | Yes (reads CONFIG_DB) | N/A |
| Execute | Yes | Yes (reads + writes) | Per-entry |
| Composite generate | No | No | N/A |
| Composite deliver | Yes | Yes (writes) | Yes (pipeline) |

### 12.4 Topology Provisioning (v4)

- Automated device configuration from `topology.json` spec file
- Two modes: **full device** (`ProvisionDevice`) and **per-interface** (`ProvisionInterface`)
- Full device mode builds a `CompositeConfig` offline and delivers via `CompositeOverwrite`
- Per-interface mode connects to the device and uses the standard `ApplyService` path
- Topology spec declares complete desired state: devices, interfaces, service bindings, parameters

| Mode | Online? | Device Required? | Atomic? |
|------|---------|-----------------|---------|
| ProvisionDevice | No (build) + Yes (deliver) | Yes (for delivery) | Yes (pipeline) |
| ProvisionInterface | Yes | Yes (reads + writes) | Per-entry |

### 12.5 Verify (`--verify` flag) (v5)

The `--verify` flag can be appended to any execute-mode operation. After writing changes, newtron reconnects (fresh CONFIG_DB read) and runs `VerifyChangeSet` against the ChangeSet that was just applied:

```
newtron -d leaf1 interface Ethernet2 apply-service customer-l3 -x --verify
newtron provision -S specs/ -d leaf1 -x --verify
```

Standalone verification against an already-provisioned device:

```
newtron verify -S specs/ -d leaf1
```

The standalone `verify` command loads the topology spec, rebuilds the expected CompositeConfig for the device, connects, and diffs against the live CONFIG_DB.

| Mode | Online? | Device Required? | What It Checks |
|------|---------|-----------------|----------------|
| `--verify` (with `-x`) | Yes | Yes (re-reads CONFIG_DB) | ChangeSet entries landed |
| `verify` (standalone) | Yes | Yes (reads CONFIG_DB) | Full device state matches topology spec |

## 13. Verification Strategy

Newtron provides built-in verification primitives for single-device state. The architectural principle:

> **newtron exposes single-device state as structured data — not verdicts. The only assertion newtron makes is about its own writes.**
>
> When adding verification to newtron, return data, not judgments. If a check requires knowing what another device should have, it belongs in the orchestrator (newtest).

Verification spans four tiers across two owners:

### 13.1 Tier 1: CONFIG_DB Verification — newtron (Hard Fail)

**What it checks**: Operations wrote the correct entries to CONFIG_DB (Redis DB 4).

**Why it's reliable**: CONFIG_DB is under Newtron's direct control. When Newtron writes `VLAN|Vlan500` to Redis, it is either there or it is not. There is no intermediate processing layer.

**newtron primitive**: `Device.VerifyChangeSet(cs)` — re-reads CONFIG_DB through a fresh connection, diffs against the ChangeSet. Returns `VerificationResult` with pass count, fail count, and per-entry errors.

This works for every operation (disaggregated or composite) because they all produce ChangeSets. It is the only assertion newtron makes — checking its own writes.

**Failure mode**: Hard fail. If CONFIG_DB does not contain what was written, it is a bug in Newtron.

### 13.2 Tier 2: APP_DB / Routing State — newtron (Observation Only)

**What it checks**: Routes installed by FRR via `fpmsyncd` into APP_DB (Redis DB 0).

**newtron primitives**: `Device.GetRoute(vrf, prefix)` and `Device.GetRouteASIC(vrf, prefix)` return structured `RouteEntry` data (prefix, protocol, next-hops) — or nil if the route is not present. These are observation methods, not assertions. newtron does not know whether a given route is "correct" — that depends on what other devices are advertising, which requires topology-wide context.

**How orchestrators use it**: newtest connects to a remote device via newtron and calls `GetRoute` to check that an expected route arrived. For example, after provisioning leaf1, newtest connects to spine1 and verifies that leaf1's connected subnet appears in spine1's APP_DB with the expected next-hop. newtron provides the read; newtest knows what to expect.

**ASIC_DB (Redis DB 1)**: `GetRouteASIC` resolves the SAI object chain (`SAI_ROUTE_ENTRY` → `SAI_NEXT_HOP_GROUP` → `SAI_NEXT_HOP`) to confirm the ASIC actually programmed the route. The gap between APP_DB and ASIC_DB is `orchagent` processing. On SONiC-VS with the NGDP simulator, complex routes (VXLAN, ECMP) may not make it to ASIC_DB.

### 13.3 Tier 3: Operational State — newtron (Observation Only)

**What it checks**: BGP session state, interface oper-status, LAG health, VXLAN/EVPN state via STATE_DB (Redis DB 6).

**newtron primitive**: `Device.RunHealthChecks()` returns a health report with per-subsystem status (ok, warning, critical). This is local device health — not fabric correctness.

**SONiC-VS behavior**: BGP sessions establish reliably. Interface oper-status works for simple topologies. EVPN/VXLAN health depends on `orchagent` convergence.

### 13.4 Tier 4: Cross-Device and Data-Plane Verification — newtest

**What it checks**: Route propagation across devices, fabric-wide convergence, actual packet forwarding.

**Why it belongs in newtest, not newtron**: These checks require connecting to multiple devices and correlating their state. newtron operates on a single device and has no concept of "the fabric." newtest owns the topology and can connect to any device.

**Cross-device route verification**: newtest uses newtron's `GetRoute` primitive on multiple devices. For example: connect to spine1, call `GetRoute("default", "10.1.0.0/31")`, assert the next-hop matches leaf1's interface IP from the topology spec.

**Data-plane verification**: Ping between VMs through the SONiC fabric. The NGDP ASIC emulator in SONiC-VS does not forward packets through VXLAN tunnels, so data-plane tests soft-fail on VS. On real hardware or VPP images, this tier would be a hard fail.

### 13.5 Tier Summary

| Tier | What | Owner | newtron Method | Failure Mode |
|------|------|-------|---------------|-------------|
| CONFIG_DB | Redis entries match ChangeSet | **newtron** | `VerifyChangeSet(cs)` | Hard fail (assertion) |
| APP_DB / ASIC_DB | Routes installed by FRR / ASIC | **newtron** | `GetRoute()`, `GetRouteASIC()` | Observation (data, not verdict) |
| Operational state | BGP sessions, interface health | **newtron** | `RunHealthChecks()` | Observation (health report) |
| Cross-device / data plane | Route propagation, ping | **newtest** | Composes newtron primitives | Topology-dependent |

## 14. Lab Architecture

### 14.1 Overview

The `testlab/` directory contains everything needed to run Newtron against virtual SONiC devices using containerlab.

### 14.2 Lab Generation Pipeline

The `labgen` tool (`pkg/labgen`) generates all artifacts from a single topology YAML file:

```
  topology YAML                    Generated Artifacts
  (testlab/topologies/)            (testlab/.generated/)

+---------------------+           +-- spine-leaf.clab.yml    (containerlab topology)
| spine-leaf.yml      |           +-- specs/
|  - nodes            | -------->  |   +-- network.json       (Newtron specs)
|  - links            |   labgen   |   +-- site.json
|  - role_defaults    |            |   +-- platforms.json
|  - defaults         |            |   +-- profiles/
+---------------------+            |       +-- leaf1.json     (with ssh_user/ssh_pass)
                                   |       +-- leaf2.json
  configlets/                      |       +-- spine1.json
  (templates per role)             |       +-- spine2.json
                                   +-- leaf1/
                                   |   +-- config_db.json     (baseline SONiC config)
                                   |   +-- frr.conf           (FRR routing config)
                                   +-- leaf2/
                                   |   +-- config_db.json
                                   |   +-- frr.conf
                                   +-- spine1/
                                   |   +-- config_db.json
                                   |   +-- frr.conf
                                   +-- spine2/
                                       +-- config_db.json
                                       +-- frr.conf
```

### 14.3 Topology YAML

A topology file defines nodes, links, and role defaults:

```yaml
name: spine-leaf
defaults:
  image: vrnetlab/cisco_sonic:ngdp-202411
  username: cisco
  password: cisco123
  platform: vs-platform
nodes:
  spine1:
    role: spine
    loopback_ip: "10.0.0.1"
  leaf1:
    role: leaf
    loopback_ip: "10.0.0.11"
  server1:
    role: server
    image: nicolaka/netshoot:latest
links:
  - endpoints: ["spine1:Ethernet0", "leaf1:Ethernet0"]
  - endpoints: ["leaf1:Ethernet2", "server1:eth1"]
role_defaults:
  spine:
    - sonic-baseline
    - sonic-evpn-spine
  leaf:
    - sonic-baseline
    - sonic-evpn-leaf
```

### 14.4 Node Types

| Role | Implementation | Purpose |
|------|---------------|---------|
| `spine` | vrnetlab QEMU VM running SONiC-VS | Route reflector, BGP underlay |
| `leaf` | vrnetlab QEMU VM running SONiC-VS | ToR switch with VTEP, services |
| `server` | Linux container (nicolaka/netshoot) | End-host for data-plane testing |

**vrnetlab**: SONiC-VS images are run inside QEMU, wrapped in Docker containers by vrnetlab. The QEMU VM exposes SSH on port 22 (mapped to the container's management IP). Redis runs inside the VM on `127.0.0.1:6379` and is not port-forwarded.

### 14.5 Generated Artifacts

- **`config_db.json`**: Per-device baseline configuration assembled from configlet templates. Applied by the `role_defaults` list. Contains port configuration, BGP neighbors, VXLAN tunnel, ACLs, etc.
- **`*.clab.yml`**: Containerlab topology file that defines nodes, images, links, and bind-mounts for config files.
- **`specs/`**: Newtron specification files generated from the topology, including device profiles with `ssh_user`/`ssh_pass` fields populated from topology defaults.

**v3 changes to lab generation:**
- **`frr.conf` no longer generated**: BGP configuration is now managed entirely through CONFIG_DB via frrcfgd. The FRR management framework translates CONFIG_DB BGP tables to FRR configuration at runtime.
- **Port creation replaces labgen PORT logic**: Port entries are now created through newtron's port creation operations (validated against `platform.json`) instead of being hardcoded in labgen config_db.json templates.
- **Composite replaces sequential configlet merge**: Instead of merging individual configlet templates into config_db.json, the lab generator can use CompositeBuilder to construct a composite config offline, then deliver it atomically via `DeliverComposite` in overwrite mode.

## 15. Testing Architecture

### 15.1 Test Tiers

Newtron uses Go build tags to separate three test tiers:

| Tier | Build Tag | Redis Source | SONiC Devices | Purpose |
|------|-----------|-------------|---------------|---------|
| Unit | (none) | None | None | Pure logic: IP derivation, spec parsing, ACL expansion |
| Integration | `-tags integration` | Standalone container | None | Redis read/write: ConfigDB loading, StateDB queries, operations |
| E2E | `-tags e2e` | SSH-tunneled (in-device) | Full containerlab lab | End-to-end: operations against real SONiC-VS devices |

### 15.2 Unit Tests

Run with `go test ./...` (no build tags). Test pure functions and struct logic without any external dependencies. Examples: IP address derivation, prefix list expansion, spec inheritance resolution, ACL rule generation.

### 15.3 Integration Tests

Run with `go test -tags integration ./...`. Require a standalone Redis container (`newtron-test-redis`) started via `docker-compose`:

```yaml
services:
  redis:
    image: redis:7-alpine
    container_name: newtron-test-redis
    command: redis-server --databases 16 --save ""
```

Integration tests discover the container's IP via `docker inspect`, connect directly to `<ip>:6379` (no SSH), and seed Redis with JSON fixture files (`testlab/seed/configdb.json`, `testlab/seed/statedb.json`). The `testutil` package provides `SetupConfigDB(t)`, `SetupStateDB(t)`, and `SetupBothDBs(t)` helpers that flush and re-seed the relevant databases before each test.

The `TestProfile()` fixture function creates a `ResolvedProfile` pointing at the test Redis IP with no `SSHUser`/`SSHPass`, so `Device.Connect()` takes the direct-Redis path.

### 15.4 E2E Tests

Run with `go test -tags e2e ./test/e2e/`. Require a running containerlab topology (`make lab-start`).

**Key patterns**:

- **SSH-tunneled Redis**: E2E tests connect through the normal `Network -> Device -> Connect` path, which establishes SSH tunnels. Device profiles in `.generated/specs/profiles/` include `ssh_user`/`ssh_pass` from the topology defaults.

- **Shared SSH tunnel pool**: The testutil package maintains a pool of SSH tunnels (`labTunnels` map) indexed by node name, reused across tests to avoid repeated tunnel setup. Tunnels are closed by `CloseLabTunnels()` in `TestMain` after all tests complete.

- **Fresh connections for verification**: After executing an operation, E2E tests create a **new** `LabConnectedDevice(t, nodeName)` to read CONFIG_DB. This ensures verification reads the actual Redis state, not a cached in-memory copy.

- **Device locking**: Mutating operations require `LabLockedDevice(t, nodeName)` which connects, locks, and registers cleanup to unlock and disconnect.

- **Baseline reset**: `TestMain` calls `ResetLabBaseline()` before running any tests. This SSHes into every SONiC node and deletes known stale CONFIG_DB keys from previous test runs, preventing `vxlanmgrd`/`orchagent` crashes.

- **Three-tier assertion strategy**: Tests use CONFIG_DB assertions (hard fail), ASIC_DB polling (topology-dependent), and data-plane ping (soft fail). See Section 13.

- **Test cleanup**: Each test registers cleanup functions via `t.Cleanup()` that either use reverse operations (e.g., `DeleteVLANOp`) via fresh connections, or raw Redis DEL commands for entries without dedicated reverse operations.

- **Test tracking and reporting**: `testutil.Track(t, category, nodes)` records test outcomes. `TestMain` writes an E2E report to `testlab/.generated/e2e-report.md` after the suite completes.

## 16. Summary: Spec vs Config

| Aspect | Spec (pkg/spec) | Config (config_db) |
|--------|-----------------|-------------------|
| **Nature** | Declarative intent | Imperative state |
| **Content** | Policy, references | Concrete values |
| **Location** | JSON files in specs/ | Redis database |
| **Lifecycle** | Version controlled | Generated at runtime |
| **Example** | `"peer_as": "request"` | `"asn": "65100"` |
| **Example** | `"ingress_filter": "customer-in"` | `ACL_RULE\|RULE_100` |
| **Edited by** | Network architects | Never (auto-generated) |

The system maintains this separation to enable:
1. **Consistency**: Same spec applied to different interfaces yields predictable config
2. **Reusability**: Specs are templates, not device-specific
3. **Auditability**: Intent is documented separately from implementation
4. **Flexibility**: Implementation details can change without changing intent

## 17. Design Decisions

### 17.1 SSH Tunnel vs Direct Redis

**Decision**: Access device Redis through an SSH tunnel (port 22) rather than exposing Redis directly.

**Rationale**: SONiC's Redis has no authentication. Exposing port 6379 on the management interface would allow unauthenticated access to the entire device configuration. SSH provides authentication, encryption, and is already available on every SONiC device. The QEMU virtual machines used by vrnetlab do not forward port 6379, making SSH the only available path. The SSH tunnel approach uses a random local port (`127.0.0.1:0`) to avoid port conflicts when managing multiple devices concurrently.

### 17.2 Dry-Run by Default

**Decision**: All operations default to dry-run mode. The `-x` flag is required for execution.

**Rationale**: Network changes are high-impact. A typo in a VRF name or a misapplied ACL can cause outages. Defaulting to dry-run means the operator always sees what will happen before it happens. This eliminates "fat-finger" mistakes from accidental execution.

### 17.3 Go Build Tags for Test Isolation

**Decision**: Use `//go:build integration` and `//go:build e2e` build tags to separate test tiers.

**Rationale**: Unit tests must run instantly with zero infrastructure. Integration tests need only a Redis container. E2E tests need a full containerlab topology with QEMU VMs (minutes to start, gigabytes of RAM). Build tags ensure `go test ./...` runs only fast unit tests by default, while CI pipelines can selectively enable heavier tiers.

### 17.4 Precondition Checking Before Execution

**Decision**: Every operation validates preconditions (interface exists, VLAN not in use, VRF present) before writing any changes.

**Rationale**: SONiC does not reject invalid CONFIG_DB entries at write time. Writing an invalid entry (e.g. a VLAN member referencing a non-existent VLAN) silently succeeds in Redis but causes orchagent errors or crashes. Newtron checks all preconditions in-memory before issuing any Redis writes, turning silent corruption into clear error messages.

### 17.5 Fresh Connections for Verification

**Decision**: E2E tests create a fresh device connection to verify operation results rather than reading from the same connection.

**Rationale**: After `Device.ApplyChanges()`, the in-memory `ConfigDB` struct is reloaded from Redis. However, to verify that changes actually persisted to Redis (not just to an in-memory cache), verification uses `LabConnectedDevice(t, nodeName)` which creates a new `Network`, loads a fresh profile, establishes a new SSH tunnel, and reads CONFIG_DB from scratch. This catches bugs where changes appear applied in-memory but were not written to Redis.

### 17.6 Non-Fatal StateDB

**Decision**: STATE_DB connection failure is logged as a warning; the system continues operating.

**Rationale**: STATE_DB provides supplementary operational visibility (interface oper-status, BGP session state, route counts) but is not required for configuration management. During device boot, STATE_DB may be empty or unavailable. Making it non-fatal allows Newtron to configure devices even when operational state is not yet available.

### 17.7 frrcfgd over bgpcfgd/Split Config

**Decision**: Use SONiC's FRR management framework (`frrcfgd`) for BGP management instead of `bgpcfgd` or split config (`frr.conf`).

**Rationale**: Split config requires managing `frr.conf` files directly, which sits outside CONFIG_DB and cannot be managed atomically with other SONiC tables. `bgpcfgd` uses Jinja templates with limited feature coverage — missing peer groups, route-maps, redistribute, max-paths, and cluster-id. `frrcfgd` provides full CONFIG_DB → FRR translation, supports all needed features, and has been stable since SONiC 202305+. This aligns with newtron's CONFIG_DB-centric architecture where all device state flows through Redis.

### 17.8 Composite Merge Restrictions

**Decision**: Merge mode is only supported for interface-level service configuration, and only when the target interface has no existing service binding.

**Rationale**: Unrestricted merge creates a combinatorial explosion of conflict scenarios: what happens when a composite sets a VRF that conflicts with an existing VRF? What if ACL rules overlap? Rather than implementing complex merge conflict resolution, the restriction limits merge to the well-defined case of adding a service to an unbound interface. For full device reprovisioning, overwrite mode replaces everything atomically. This keeps the merge semantics simple and predictable.

### 17.9 Platform.json Port Validation

**Decision**: Port creation requires validation against the device's `platform.json`, fetched via SSH at runtime.

**Rationale**: SONiC does not validate PORT entries at write time — writing a port with invalid lanes or unsupported speeds silently succeeds in Redis but causes syncd/orchagent failures. The `platform.json` file (located at `/usr/share/sonic/device/<platform>/platform.json`) is the authoritative source for valid port configurations on each hardware platform. Validating against it at creation time catches invalid configurations before they reach the ASIC layer.

### 17.10 Redistribution Defaults (Opinionated)

**Decision**: Service interfaces redistribute connected by default; transit interfaces do not; loopback is always redistributed.

**Rationale**: In a spine-leaf fabric, the underlay uses direct BGP peering on transit links — redistributing transit connected subnets into BGP creates redundant routes and potential loops. Service interfaces need their subnets in BGP for reachability across the fabric. Loopback must always be redistributed because it's the BGP router-id and VTEP source. These defaults match standard DC fabric design; the `redistribute` flag allows per-service override for edge cases.

### 17.11 Single SetupRouteReflector vs Per-AF Granularity

**Decision**: Provide a single `SetupRouteReflector` operation that configures all three address families (ipv4_unicast, ipv6_unicast, l2vpn_evpn) at once, rather than per-AF operations.

**Rationale**: In our DC fabric, a route reflector always reflects all three AFs — there is no operational scenario where you'd want IPv4 but not IPv6, or IPv6 but not EVPN. Separate per-AF operations would require the operator to make three calls for the standard case and create risk of partial configuration (e.g., forgetting to enable ipv6_unicast). The single operation enforces the architectural invariant that all AFs are configured together. Individual BGP operations (`AddBGPNeighbor`, `SetBGPGlobals`) still exist for fine-grained control outside the RR pattern.

### 17.12 Topology-First Provisioning over Imperative Scripts

**Decision**: Provide a declarative `topology.json` spec for device provisioning rather than relying on imperative CLI scripts or sequential command execution.

**Rationale**: Imperative provisioning scripts are fragile — they encode ordering assumptions, require error handling at each step, and drift from the desired state over time. A declarative topology spec describes the end state (which services bind to which interfaces with what parameters), and the provisioner handles the translation to CONFIG_DB entries. This aligns with newtron's spec-vs-config philosophy: the topology file is intent, the generated CONFIG_DB is implementation. It also enables idempotent reprovisioning — running `ProvisionDevice` again produces the same result regardless of current device state (in overwrite mode).

### 17.13 Full-Device vs Per-Interface Provisioning Modes

**Decision**: Provide two distinct provisioning modes — `ProvisionDevice` (offline build + atomic overwrite) and `ProvisionInterface` (online connect + apply) — rather than a single unified mode.

**Rationale**: Full-device provisioning is an offline operation that builds the entire CONFIG_DB without connecting to the device, then delivers it atomically. This is ideal for initial provisioning and lab setup where the device starts from a clean state. Per-interface provisioning connects to a running device, validates preconditions, and applies a single service — this is the safe path for incremental changes on production devices where you need dependency checking and conflict detection. Combining both into one mode would either sacrifice the offline capability of full-device provisioning or skip the safety checks of per-interface provisioning.

### 17.14 Built-In Verification Primitives over External Test Assertions

**Decision**: Verification methods (`VerifyChangeSet`, `GetRoute`, `GetRouteASIC`) live on the `Device` object rather than in test helpers or external tools.

**Rationale**: If a tool changes the state of an entity, that same tool must be able to verify the change had the intended effect. The caller should not need a second tool to find out if the first tool worked. newtron writes CONFIG_DB, configures BGP, sets up EVPN — so newtron must be able to confirm these changes produced the intended local effects (entries landed, routes appeared, ASIC programmed).

The ChangeSet-based approach is universal: it works for disaggregated operations (`CreateVLAN`, `AddBGPNeighbor`) and composite operations (`DeliverComposite`) alike, because they all produce ChangeSets. No per-operation verify methods are needed.

Routing state observations (`GetRoute`, `GetRouteASIC`) extend this: newtron configured BGP redistribution, so newtron can read APP_DB to confirm the route appeared locally. This is still single-device self-verification — confirming the intended effect of newtron's own change. Cross-device assertions (did the route reach the neighbor) require topology-wide context and belong in the orchestrator (newtest).

---

## Appendix A: Glossary

### Core Terminology

| Term | Definition |
|------|------------|
| **Spec (Specification)** | Declarative intent describing what you want. Stored in JSON files, version controlled. Never contains concrete device values. |
| **Config (Configuration)** | Imperative device state describing what the device uses. Stored in config_db/Redis. Generated at runtime from specs. |
| **Service** | A reusable template bundling VPN, filters, QoS, and permissions. Applied to interfaces to configure them consistently. |

### Entity Types

| Term | Definition |
|------|------------|
| **Network** | Top-level object representing the entire network. Owns all specs and provides access to devices. |
| **Device** | A specific switch instance with its own IP, site membership, and profile. Represents a physical or virtual SONiC switch. |
| **Platform** | Hardware type definition (HWSKU, port count, speeds). Describes what kind of switch hardware is supported. |
| **Interface** | A port on a device (physical, LAG, VLAN, loopback). Services are applied to interfaces. |

### Spec Types (Go structs)

| Type | File | Purpose |
|------|------|---------|
| `NetworkSpecFile` | `network.json` | Global network settings: services, filters, VPNs, regions, prefix lists |
| `SiteSpecFile` | `site.json` | Site topology: which devices are route reflectors |
| `PlatformSpecFile` | `platforms.json` | Hardware definitions: HWSKU, port counts, breakout modes |
| `TopologySpecFile` | `topology.json` | (optional) Devices, interfaces, service bindings, links |
| `DeviceProfile` | `profiles/{name}.json` | Per-device settings: IPs, site membership, SSH credentials, overrides |
| `ResolvedProfile` | (runtime) | Fully resolved device values after inheritance |

### Service Types

| Type | L2VNI | L3VNI | SVI | Use Case |
|------|-------|-------|-----|----------|
| `l2` | Yes | No | No | VLAN extension (bridging only) |
| `l3` | No | Yes | No | Routed interface (no VLAN) |
| `irb` | Yes | Yes | Yes | Integrated routing and bridging |

### VPN Terminology

| Term | Definition |
|------|------------|
| **IPVPN** | IP-VPN definition for L3 routing. Contains L3VNI and route targets. |
| **MACVPN** | MAC-VPN definition for L2 bridging. Contains VLAN, L2VNI, ARP suppression. |
| **L2VNI** | Layer 2 VNI for VXLAN bridging (MAC learning, BUM traffic). |
| **L3VNI** | Layer 3 VNI for VXLAN routing (inter-subnet traffic). |
| **VRF** | Virtual Routing and Forwarding instance for traffic isolation. |
| **Route Target (RT)** | BGP extended community controlling VPN route import/export. |

### VRF Types

| Type | VRF Naming | Use Case |
|------|------------|----------|
| `interface` | `{service}-{interface}` | Per-interface isolation (e.g., `customer-Ethernet0`) |
| `shared` | `{ipvpn-name}` | Multiple interfaces share one VRF |

### Inheritance

| Term | Definition |
|------|------------|
| **Inheritance Chain** | Resolution order: Profile > Region > Global. First non-empty value wins. |
| **Override** | A value in a profile that replaces the inherited region/global value. |
| **Derived Value** | A value computed at runtime (e.g., BGP neighbors from route reflector loopbacks). |

### Connection Types

| Term | Definition |
|------|------------|
| **SSH Tunnel** | Port-forwarding tunnel through SSH port 22. Used to reach Redis (127.0.0.1:6379) inside SONiC devices. Created when `ssh_user` and `ssh_pass` are present in the device profile. |
| **Direct Redis** | Direct TCP connection to Redis on `<MgmtIP>:6379`. Used by integration tests with a standalone Redis container. No SSH credentials in the device profile. |

### Redis Databases

| DB Number | Name | Purpose |
|-----------|------|---------|
| 0 | APP_DB | Routes installed by FRR/fpmsyncd (read by Newtron via `GetRoute`) |
| 1 | ASIC_DB | SAI objects programmed by orchagent (read by Newtron via `GetRouteASIC`) |
| 4 | CONFIG_DB | Device configuration (read/write by Newtron) |
| 6 | STATE_DB | Operational state (read-only by Newtron via `RunHealthChecks`) |

### Operations

| Term | Definition |
|------|------------|
| **Dry-Run** | Preview mode (default). Shows what would change without applying. |
| **Execute (`-x`)** | Apply mode. Actually writes changes to device config_db. |
| **ChangeSet** | Collection of pending changes to be previewed or applied. Also serves as the verification contract — `VerifyChangeSet` diffs the ChangeSet against live CONFIG_DB. |
| **VerifyChangeSet** | Re-reads CONFIG_DB through a fresh connection and confirms every entry in the ChangeSet was applied correctly. The only assertion newtron makes. |
| **GetRoute** | Reads a route from APP_DB (DB 0) and returns structured data (prefix, protocol, next-hops). Observation primitive — returns data, not a verdict. |
| **GetRouteASIC** | Reads a route from ASIC_DB (DB 1) by resolving SAI object chain. Confirms the ASIC programmed the route. |
| **Audit Log** | Record of all executed changes with user, timestamp, and details. |
| **Device Lock** | Exclusive lock acquired before mutating operations. Prevents concurrent modifications. |
| **Baseline Reset** | Pre-test cleanup that deletes stale CONFIG_DB entries from previous test runs on all SONiC nodes. |

### BGP Management (v3)

| Term | Definition |
|------|------------|
| **frrcfgd** | SONiC's FRR management framework daemon. Translates CONFIG_DB BGP tables to FRR commands. Enabled via `frr_mgmt_framework_config=true` in DEVICE_METADATA. |
| **Peer Group** | BGP peer group template. Neighbors assigned to a peer group inherit its settings. Managed via BGP_PEER_GROUP table. |
| **Route-Map** | Ordered set of match/set rules for BGP route filtering and attribute modification. Managed via ROUTE_MAP table. |
| **Prefix-Set** | IP prefix list used in route-map match clauses. Managed via PREFIX_SET table. |
| **Community-Set** | BGP community list for matching or setting community attributes. Managed via COMMUNITY_SET table. |
| **Redistribute** | Injection of connected or static routes into BGP. Controlled per-service via `redistribute` flag with opinionated defaults. |
| **Cluster-ID** | BGP route reflector cluster identifier. Prevents routing loops when multiple RRs exist in the same cluster. Set in SiteSpec or defaults to spine loopback. |

### Composite Mode (v3)

| Term | Definition |
|------|------------|
| **Composite** | A composite CONFIG_DB configuration generated offline, delivered to a device as a single atomic operation. |
| **Overwrite Mode** | Composite delivery mode that replaces the entire CONFIG_DB with composite content. Used for initial provisioning. |
| **Merge Mode** | Composite delivery mode that adds entries to existing CONFIG_DB. Restricted to interface-level services with no existing binding. |
| **CompositeBuilder** | Builder pattern for constructing composite configs offline without device connection. |
| **CompositeConfig** | The composite CONFIG_DB representation with metadata (timestamp, network, device, mode). |
| **Redis Pipeline** | MULTI/EXEC transaction used by composite delivery for atomic application of multiple CONFIG_DB changes. |

### Port Management (v3)

| Term | Definition |
|------|------------|
| **platform.json** | SONiC device file at `/usr/share/sonic/device/<platform>/platform.json`. Defines available ports, lane assignments, supported speeds, and breakout modes. |
| **Port Creation** | Creating a PORT entry in CONFIG_DB, validated against platform.json to ensure valid lanes, speeds, and no breakout conflicts. |
| **Breakout** | Splitting a high-speed port into multiple lower-speed ports (e.g., 1x100G → 4x25G). Creates child ports, removes parent. |
| **PlatformConfig** | Parsed representation of platform.json cached on device. Used for port validation. |
| **GeneratePlatformSpec** | Device method that creates a `spec.PlatformSpec` from the device's platform.json, for priming specs on first connect to new hardware. |

### Topology Provisioning (v4)

| Term | Definition |
|------|------------|
| **topology.json** | Optional spec file declaring complete desired state: devices, interfaces, service bindings, links, and per-interface parameters. |
| **ProvisionDevice** | Full-device provisioning mode. Builds a CompositeConfig offline from topology.json, delivers via CompositeOverwrite. No device connection required during build. |
| **ProvisionInterface** | Per-interface provisioning mode. Reads service and parameters from topology.json, connects to the device, and uses the standard ApplyService path with full precondition checking. |
| **TopologySpecFile** | Go struct representing the parsed topology.json file. Contains device definitions, interface bindings, and link declarations. |
