# Newtron High-Level Design (HLD)

## 1. Purpose

Newtron is an opinionated network automation tool for SONiC-based switches, built on the premise that SONiC is a Redis database with daemons that react to table changes — and should be treated as one. Where other SONiC tools SSH in and parse CLI output, newtron reads and writes CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB directly through an SSH-tunneled Redis client. It enforces a network design intent — expressed as declarative spec files — while allowing many degrees of freedom within those constraints for actual deployments. The specs define what the network *must* look like (services, filters, routing policies); newtron translates that intent into concrete CONFIG_DB entries using each device's context (IPs, AS numbers, platform capabilities).

For the architectural principles behind newtron, newtlab, and newtest — including the object hierarchy, verification ownership, and DRY design — see [Design Principles](../DESIGN_PRINCIPLES.md).

### Key Features

- **Redis-First**: All device interaction through native Go Redis client — CONFIG_DB writes, APP_DB route reads, ASIC_DB SAI chain traversal, STATE_DB health checks — not CLI command parsing. CLI workarounds are tagged exceptions, not the norm
- **Typed Domain Model**: `Network > Node > Interface` object hierarchy where operations live on the smallest object that has the context to execute them — not connection wrappers with external helper functions
- **Built-In Referential Integrity**: Precondition validation on every write operation prevents invalid CONFIG_DB state (nonexistent VRFs, conflicting service bindings, LAG member collisions) — application-level constraints for a database that has none
- **Noun-Group CLI**: Unified `newtron <device> <noun> <action> [args] [-x]` pattern with implicit device detection — first argument is the device name unless it matches a known command
- **Service-Oriented**: Pre-defined service templates for consistent configuration
- **VRF Management**: First-class VRF noun with 13 subcommands — owns interfaces, BGP neighbors, static routes, and IP-VPN bindings
- **Spec Authoring**: CLI-authored definitions (services, IP-VPNs, MAC-VPNs, QoS policies, filters) persist atomically to network.json
- **EVPN/VXLAN**: Modern overlay networking with idempotent composite setup and IP-VPN/MAC-VPN spec authoring
- **Full BGP via frrcfgd**: Underlay, IPv6, and L2/L3 EVPN overlays managed through CONFIG_DB using SONiC's FRR management framework
- **Multi-AF Route Reflection**: BGP neighbor and EVPN configuration via `AddLoopbackBGPNeighbor` and `SetupEVPN` methods supporting IPv4, IPv6, and L2VPN EVPN address families
- **Composite Mode**: Offline composite config generation with atomic delivery (overwrite or merge)
- **Topology Provisioning**: Automated device provisioning from topology.json specs — full-device overwrite or per-interface service application
- **Platform-Validated Port Creation**: PORT entries validated against SONiC's on-device `platform.json`
- **Built-In Verification**: ChangeSet-based CONFIG_DB verification, routing state observation via APP_DB/ASIC_DB, health checks — single-device primitives that orchestrators compose for fabric-wide assertions
- **Per-Noun Status**: Each resource noun (`vlan`, `vrf`, `lag`, `bgp`, `evpn`) owns its own `status` subcommand combining CONFIG_DB config with operational state
- **Safety-First**: Dry-run by default with explicit execution flag
- **Audit Trail**: All changes logged with user, timestamp, and device context
- **Access Control**: Permission-based operations at service and action levels

## 2. Spec vs Config: The Fundamental Distinction

Newtron enforces a clear separation between **specification (intent)** and **configuration (device state)**:

### 2.1 Specification (Declarative Intent)

Specs describe **what you want** - they are declarative, abstract, and policy-driven.

| Property | Description |
|----------|-------------|
| **Nature** | Declarative - describes desired state |
| **Location** | `specs/` directory, `pkg/newtron/spec` package |
| **Format** | JSON files (network.json, profiles/*.json) |
| **Content** | Service definitions, filter policies, VPN parameters |
| **Lifecycle** | Created by network architects, versioned in git |

**Example - Service Spec:**
```json
{
  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface",
      "service_type": "routed",
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
      "local_asn": "64512",
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

### 2.3 Hierarchical Spec Resolution

Specs participate in a three-level hierarchy: **network → zone → node**. Each level can define or override any of the 8 overridable spec types:

| Spec Type | JSON Field | Description |
|-----------|-----------|-------------|
| Services | `services` | Interface service templates |
| Filters | `filters` | ACL rule sets |
| IP-VPNs | `ipvpns` | L3VPN definitions |
| MAC-VPNs | `macvpns` | L2VPN definitions |
| QoS Policies | `qos_policies` | Queue scheduling policies |
| QoS Profiles | `qos_profiles` | Legacy scheduler mappings |
| Route Policies | `route_policies` | BGP import/export policies |
| Prefix Lists | `prefix_lists` | IP prefix sets |

Resolution is a **union with lower-level-wins**: if the same spec name exists at multiple levels, the most specific level wins (node > zone > network). Specs at different levels with different names are all visible — a zone can add new services without duplicating network-level ones.

**Not participating** in hierarchy (global-only): `zones`, `permissions`, `user_groups`, `super_users`, `version`, `platforms`.

### 2.4 Translation: Spec -> Config

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
                                  |   - Device AS: 64512
                                  |   - User-provided peer AS: 65100
                                  v
+-----------------------------------------------------------------------+
|                      TRANSLATION (pkg/newtron/network)                |
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

### 2.5 Key Principles

1. **Specs are minimal**: Only declare what's needed; operational details are derived
2. **Specs are reusable**: Same service spec applies to any interface
3. **Config is concrete**: Every value is explicit and device-specific
4. **Config is derived**: Generated from spec + context, never hand-edited
5. **Single source of truth**: Each fact exists in exactly one place

### 2.6 What Belongs Where

| In Spec (Declarative) | Derived at Runtime |
|-----------------------|--------------------|
| Service type (routed, bridged, irb, evpn-*) | VRF name |
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
|   (cmd/newtron)  |     |(pkg/newtron/network)|  |  (Redis/config_db)|
|                  |     |                  |     |                  |
+------------------+     +------------------+     +------------------+
           |                    |
           v                    +-----------+-----------+
   +---------------+            |           |
   |CompositeBuilder|           v           v
   | (offline      |     +----------+ +----------+
   |  composite    |     | Spec     | | Device   |
   |  config gen)  |     | Layer    | | Layer    |
   +---------------+     +----------+ +----------+
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
             |  (pkg/newtron/device/sonic)              |
             |                                          |
             |  +---------------------------+           |
             |  | SSH Tunnel (port 22)      |           |
             |  | (when SSHUser+SSHPass set)|           |
             |  +---------------------------+           |
             |           |                              |
             |           v                              |
             |  +---------------------------+           |
             |  | ConfigDBClient (DB 4)     |           |
             |  | + PipelineClient          |           |
             |  +---------------------------+           |
             |  +---------------------------+           |
             |  | StateDBClient  (DB 6)     |           |
             |  +---------------------------+           |
             |  +---------------------------+           |
             |  | PlatformConfig            |           |
             |  | (from device platform.json|           |
             |  |  via SSH)                 |           |
             |  +---------------------------+           |
             +-----------------------------------------+
```

### 3.2 Object Hierarchy (OO Design)

The system uses an object-oriented design with parent references. The governing principle: **a method belongs to the smallest object that has all the context to execute it**. If an operation needs the interface name, the device profile, and the network specs, it lives on Interface (which can reach all three through its parent chain). If it only needs the Redis connection, it lives on Node.

A related principle: **whatever configuration can be right-shifted to the interface level, should be**. eBGP neighbors are interface-specific — they derive from the interface's IP and the service's peer AS — so they are created by `Interface.ApplyService()`. Route reflector peering (iBGP toward spines) is device-specific — it derives from the device's role and site topology — so it lives on `Node.AddLoopbackBGPNeighbor()` and `Node.SetupEVPN()`. Interface-level configuration is more composable, more independently testable, and easier to reason about.

```
+------------------------------------------------------------------+
|                         Network                                   |
|  (top-level object - owns all specs)                             |
|                                                                   |
|  - NetworkSpecFile (services, filters, zones, permissions)     |
|  - PlatformSpecFile (hardware platform definitions)              |
|                                                                   |
|  Methods: GetService(), GetFilter(), GetZone(), etc.       |
+------------------------------------------------------------------+
         |
         | creates (with parent reference)
         v
+------------------------------------------------------------------+
|                          Node                                     |
|  (created in Network's context)                                  |
|                                                                   |
|  - parent: *Network  <-- key: access to all Network specs        |
|  - DeviceProfile                                                 |
|  - ResolvedProfile (after inheritance)                           |
|  - ConfigDB (from Redis) <-- actual device config                |
|                                                                   |
|  Methods: ASNumber(), BGPNeighbors(), Tunnel(),                  |
|           StateDBClient(), ConfigDBClient(), StateDB(),          |
|           AddLoopbackBGPNeighbor(), SetupEVPN(),                 |
|           SaveConfig(), RestartService(), ApplyFRRDefaults(),    |
|           GetRoute(), GetRouteASIC(), GetNeighbor(),             |
|           VerifyComposite(), etc.                                |
+------------------------------------------------------------------+
         |
         | creates (with parent reference)
         v
+------------------------------------------------------------------+
|                        Interface                                  |
|  (created in Node's context)                                     |
|                                                                   |
|  - parent: *Node     <-- key: access to Node AND Network         |
|  - Interface state (from config_db)                              |
|                                                                   |
|  Methods: Node(), Network(), HasService(),                       |
|           ApplyService(), RemoveService(), RefreshService()      |
+------------------------------------------------------------------+
```

## 4. Component Description

### 4.1 CLI Layer (`cmd/newtron`)

The command-line interface is built with the Cobra framework using a **noun-group** pattern.

#### 4.1.1 CLI Pattern

```
newtron <device> <noun> <action> [args] [-x]
```

The first argument is the device name unless it matches a known command. This lets users write `newtron leaf1 vlan list` instead of `newtron -d leaf1 vlan list`. If no device is needed (e.g., spec authoring commands), the device argument is omitted: `newtron service list`.

**Context Flags:**
| Flag | Description |
|------|-------------|
| `-n, --network` | Network specs name |
| `-d, --device` | Target device name (alternative to positional first arg) |
| `-S, --specs` | Specification directory |

**Write Flags:**
| Flag | Description |
|------|-------------|
| `-x, --execute` | Execute changes (default is dry-run preview) |
| `-s, --save` | Save config after changes (requires `-x`; runs `config save -y`) |

**Output Flags:**
| Flag | Description |
|------|-------------|
| `--json` | JSON output |
| `-v, --verbose` | Verbose/debug output |

#### 4.1.2 Two Command Scopes

Commands fall into two categories based on whether they need a device connection:

| Scope | Target | Needs Device | Examples |
|-------|--------|-------------|----------|
| **Device-required** | CONFIG_DB on a SONiC switch | Yes (`-d` or implicit) | `vlan create`, `vrf add-neighbor`, `service apply`, `evpn setup` |
| **No-device** | network.json specs (local file) | No | `service list`, `evpn ipvpn create`, `qos create`, `filter list` |

Device-required commands connect to the device via SSH tunnel, acquire a distributed lock, execute the operation, and release the lock. No-device commands read or write the local network.json spec file without any device connection.

#### 4.1.3 The `withDeviceWrite` Helper

All device-level write commands use the `withDeviceWrite` helper, which encapsulates the standard connect-lock-execute-print-unlock pattern:

```go
func withDeviceWrite(fn func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error)) error {
    // 1. requireDevice(ctx) — connect to device via SSH tunnel
    // 2. dev.Lock() — acquire distributed STATE_DB lock
    // 3. fn(ctx, dev) — execute the operation, returns ChangeSet
    // 4. Print ChangeSet preview
    // 5. If -x: Apply ChangeSet, optionally save config
    //    If not -x: Print dry-run notice
    // 6. defer dev.Unlock() + dev.Disconnect()
}
```

This ensures every write command follows the same lifecycle: connect, lock, call the operation function, print the changeset, and handle execute/dry-run — with guaranteed cleanup.

#### 4.1.4 Full Command Tree

```
Resource Commands
+-- interface
|   +-- list
|   +-- show <interface>
|   +-- get <interface> <property>
|   +-- set <interface> <property> <value>
|   +-- list-acls <interface>
|   +-- list-members <interface>
+-- vlan
|   +-- list
|   +-- show <vlan-id>
|   +-- status
|   +-- create <vlan-id> [--name] [--description]
|   +-- delete <vlan-id>
|   +-- add-interface <vlan-id> <interface> [--tagged]
|   +-- remove-interface <vlan-id> <interface>
|   +-- configure-svi <vlan-id> [--vrf] [--ip] [--anycast-gw]
|   +-- bind-macvpn <vlan-id> <macvpn-name>
|   +-- unbind-macvpn <vlan-id>
+-- vrf
|   +-- list
|   +-- show <vrf-name>
|   +-- status
|   +-- create <vrf-name>
|   +-- delete <vrf-name>
|   +-- add-interface <vrf-name> <interface>
|   +-- remove-interface <vrf-name> <interface>
|   +-- bind-ipvpn <vrf-name> <ipvpn-name>
|   +-- unbind-ipvpn <vrf-name>
|   +-- add-neighbor <vrf-name> <interface> <remote-asn> [--neighbor] [--description]
|   +-- remove-neighbor <vrf-name> <interface|ip>
|   +-- add-route <vrf-name> <prefix> <next-hop> [--metric]
|   +-- remove-route <vrf-name> <prefix>
+-- lag
|   +-- list
|   +-- show <lag-name>
|   +-- status
|   +-- create <lag-name> --members <...> [--min-links] [--mode] [--fast-rate] [--mtu]
|   +-- delete <lag-name>
|   +-- add-interface <lag-name> <interface>
|   +-- remove-interface <lag-name> <interface>
+-- bgp
|   +-- status
+-- evpn
|   +-- setup [--source-ip]
|   +-- status
|   +-- ipvpn
|   |   +-- list
|   |   +-- show <name>
|   |   +-- create <name> --l3vni <vni> [--route-targets] [--description]
|   |   +-- delete <name>
|   +-- macvpn
|       +-- list
|       +-- show <name>
|       +-- create <name> --vni <vni> --vlan-id <id> [--anycast-ip <ip>] [--anycast-mac <mac>] [--route-targets <rt>...] [--arp-suppress] [--description]
|       +-- delete <name>
+-- qos
|   +-- list
|   +-- show <policy-name>
|   +-- create <policy-name> [--description]
|   +-- delete <policy-name>
|   +-- add-queue <policy-name> <queue-id> --type <dwrr|strict> [--weight] [--dscp] [--name] [--ecn]
|   +-- remove-queue <policy-name> <queue-id>
|   +-- apply <interface> <policy-name>
|   +-- remove <interface>
+-- filter
|   +-- list
|   +-- show <filter-name>
|   +-- create <filter-name> --type <ipv4|ipv6> [--description]
|   +-- delete <filter-name>
|   +-- add-rule <filter-name> --priority <N> --action <permit|deny> [match flags...]
|   +-- remove-rule <filter-name> <priority>
+-- acl
|   +-- list / show / create / delete
|   +-- add-rule / delete-rule
|   +-- bind / unbind
+-- service
|   +-- list
|   +-- show <service-name>
|   +-- create <service-name> --type <routed|bridged|irb|evpn-routed|evpn-bridged|evpn-irb> [--ipvpn] [--macvpn] [--vrf-type]
|   |   [--vlan] [--qos-policy] [--ingress-filter] [--egress-filter] [--description]
|   +-- delete <service-name>
|   +-- apply <interface> <service> [--ip] [--peer-as]
|   +-- remove <interface>
|   +-- get <interface>
|   +-- refresh <interface>
+-- baseline
    +-- list / show / apply

Device Operations
+-- show
+-- provision [-d <device>] [-x] [-s]
+-- health check [--check <name>]
+-- device
    +-- cleanup [--type]

Configuration & Meta
+-- settings
+-- audit
+-- version
```

#### 4.1.5 Interface Name Formats

| Short Form | Full Form (SONiC) |
|------------|-------------------|
| `Eth0` | `Ethernet0` |
| `Po100` | `PortChannel100` |
| `Vl100` | `Vlan100` |
| `Lo0` | `Loopback0` |

### 4.2 Network Layer (`pkg/newtron/network`, `pkg/newtron/network/node`)

The top-level object hierarchy that provides OO access to specs and devices.

**Key Types:**
| Type | Description |
|------|-------------|
| `Network` | Top-level object; owns specs, creates Nodes |
| `Node` | Node with parent reference to Network; creates Interfaces |
| `Interface` | Interface with parent reference to Node |

### 4.3 Spec Layer (`pkg/newtron/spec`)

Loads and resolves specifications from JSON files.

**Spec Types:**
| Type | Description |
|------|-------------|
| `NetworkSpecFile` | Services, filters, VPNs, zones, permissions |
| `PlatformSpecFile` | Hardware platform definitions (HWSKU, ports) |
| `TopologySpecFile` | (optional) Topology: devices, interfaces, service bindings, links |
| `DeviceProfile` | Per-device settings (mgmt_ip, loopback_ip, site, ssh_user, ssh_pass) |
| `ResolvedProfile` | Profile after inheritance resolution |

**Key Distinction**: The spec layer handles **declarative intent**. It never contains concrete device configuration - only policy definitions and references.

### 4.4 Device Layer (`pkg/newtron/device/sonic`)

Pure connection management for SONiC switches. `sonic.Device` is a connection manager — it owns the SSH tunnel and Redis client lifecycle but contains no domain logic. All domain operations (applying changes, verifying config, reading routes, running SSH commands) live in `node/`.

**Key Components:**
- `sonic.Device`: Connection manager — holds Redis clients (ConfigDBClient, StateDBClient, AppDBClient, AsicDBClient), optional SSHTunnel, distributed lock state, and thread-safe mutex. Exposes client accessors (`Client()`, `StateClient()`, `AppDBClient()`, `AsicDBClient()`, `Tunnel()`, `ConnAddr()`) for `node.Node` to use
- `ConfigDB`: SONiC config_db structure (PORT, VLAN, VRF, ACL_TABLE, etc.)
- `ConfigDBClient`: Redis client wrapper for CONFIG_DB (Redis DB 4)
- `StateDB`: SONiC state_db structure (PortTable, LAGTable, BGPNeighborTable, etc.)
- `StateDBClient`: Redis client wrapper for STATE_DB (Redis DB 6)
- `AppDBClient`: Redis client wrapper for APP_DB (Redis DB 0) — routing state from FRR/fpmsyncd
- `AsicDBClient`: Redis client wrapper for ASIC_DB (Redis DB 1) — SAI objects from orchagent
- `SSHTunnel`: SSH port-forward tunnel for Redis access through port 22
- `PlatformConfig`: Parsed representation of SONiC's `platform.json`, cached on device; provides port definitions, lane assignments, supported speeds, and breakout modes for port validation
- `CompositeBuilder`: Builder for offline composite CONFIG_DB generation (in `pkg/newtron/network/node/composite.go`)
- `PipelineClient`: Redis MULTI/EXEC pipeline for atomic multi-entry writes (used by composite delivery)

**Key Distinction**: The device layer handles **connection infrastructure**. Domain operations (CONFIG_DB writes, verification, route reads, SSH commands) live in `node/`.

### 4.5 Translation (in `pkg/newtron/network/node`)

The translation from spec to config happens in `Interface.ApplyService()` and related methods:

```go
func (i *Interface) ApplyService(ctx context.Context, serviceName string, opts ApplyServiceOpts) (*ChangeSet, error) {
    // 1. Load spec (declarative)
    svc, _ := i.Network().GetService(serviceName)

    // 2. Translate with context
    vrfName := deriveVRFName(svc, i.name)           // spec + context -> config
    peerIP, _ := util.DeriveNeighborIP(opts.IPAddress) // derive from interface IP
    localAS := i.Node().Resolved().ASNumber           // from device profile

    // 3. Generate config (imperative)
    cs.Add("VRF", vrfName, ChangeAdd, nil, vrfConfig)
    cs.Add("BGP_NEIGHBOR", peerIP, ChangeAdd, nil, neighborConfig)
    cs.Add("ACL_TABLE", aclName, ChangeAdd, nil, aclConfig)

    return cs, nil
}
```

The CLI uses the `withDeviceWrite` helper (see section 4.1.3) to handle the connect-lock-execute-print lifecycle. The operation function returns a ChangeSet; `withDeviceWrite` prints it and applies it if `-x` is set.

```
newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
```

**RemoveService** — `RemoveService(ctx)` is the reverse of ApplyService. It removes all CONFIG_DB entries created by ApplyService for the bound service on the interface. Uses DependencyChecker to protect shared resources (VRFs, ACLs, VLANs) that may be referenced by other interfaces.

```
newtron leaf1 service remove Ethernet0 -x
```

**RefreshService** — `RefreshService(ctx)` re-reads the service spec, diffs the expected config against the current CONFIG_DB, and applies only the delta. This is used when a service spec is updated and the device needs to converge to the new definition without a full remove/apply cycle.

```
newtron leaf1 service refresh Ethernet0 -x
```

### 4.6 BGP Management Architecture

#### 4.6.1 BGP CLI: Visibility Only

BGP is a visibility-only noun in the CLI. It has a single `status` subcommand that provides a unified view of BGP configuration and operational state:

```
newtron leaf1 bgp status
```

The `status` view combines:
- **Local BGP identity** — AS number, router ID, loopback IP (from device profile)
- **Configured neighbors** — from CONFIG_DB `BGP_NEIGHBOR` table, classified as direct (interface-level, different local_addr) or indirect (loopback-based)
- **Operational state** — from STATE_DB `BGP_NEIGHBOR_TABLE`, showing session state (Established/Idle), prefix counts, and uptime
- **Expected EVPN neighbors** — from site config (route reflector loopbacks)

All BGP peer management is handled through other nouns:
- **`vrf add-neighbor`** — creates eBGP or iBGP neighbors with per-interface context (local_addr from interface IP, auto-derived neighbor IP for /30 and /31 subnets)
- **`vrf remove-neighbor`** — removes a BGP neighbor from a VRF
- **`evpn setup`** — creates overlay iBGP EVPN sessions with route reflectors from site config

This separation reflects SONiC's data model: BGP neighbors belong to routing contexts (VRFs), and the VRF is the natural owner of neighbor lifecycle. The BGP noun provides read-only visibility across all VRFs.

#### 4.6.2 SONiC BGP Management Modes

SONiC supports three BGP management modes:

| Mode | Config Flag | Daemon | Description |
|------|------------|--------|-------------|
| **Split** | `frr_split_config_enabled=true` | FRR reads `frr.conf` | CONFIG_DB BGP tables ignored. Direct FRR config file management. |
| **Legacy unified** | (default) | `bgpcfgd` | Jinja templates translate CONFIG_DB to FRR. Limited coverage: missing peer groups, route-maps, redistribute, max-paths, cluster-id. |
| **FRR management framework** | `frr_mgmt_framework_config=true` | `frrcfgd` | Full CONFIG_DB → FRR translation. Supports all needed features. Stable since SONiC 202305+. |

The lab runs SONiC 202411 (Gibraltar). Newtron uses frrcfgd, enabling full BGP management through CONFIG_DB.

#### 4.6.3 Why frrcfgd

The FRR management framework (`frrcfgd`) provides:
- Complete CONFIG_DB coverage for BGP features (peer groups, route-maps, redistribute, prefix-lists, community-sets, AS-path filters)
- Declarative model that aligns with newtron's spec → config translation
- Atomic CONFIG_DB writes that frrcfgd translates to FRR commands
- No direct `frr.conf` file management needed
- Consistent management of underlay, IPv6, and EVPN overlay through a single mechanism

#### 4.6.4 CONFIG_DB BGP Tables (9 tables)

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

#### 4.6.5 Extended Fields on Existing Tables

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

#### 4.6.6 BGP Route Reflection

BGP route reflection is configured through two `Node` methods:

- `AddLoopbackBGPNeighbor(ctx, neighborIP, asn, description, evpn)` — adds an iBGP neighbor on the loopback interface with optional EVPN address family activation
- `SetupEVPN(ctx, sourceIP)` — creates the VXLAN tunnel (VTEP) and activates EVPN address family in BGP

The CLI equivalent is `evpn setup`, which combines VTEP creation and EVPN activation. BGP neighbors are added via `vrf add-neighbor` or implicitly during topology provisioning.

#### 4.6.7 Route Redistribution Defaults

Redistribution follows opinionated defaults:
- **Service interfaces**: redistribute connected subnets into BGP (default)
- **Loopback**: always redistributed (needed for BGP router-id reachability)
- **Transit interfaces**: NOT redistributed by default (fabric underlay uses direct BGP)
- Service spec flag `redistribute` controls per-service override

#### 4.6.8 BGP Network-Layer Operations

These operations are methods on `node.Node` and `node.Interface`. They are called by the topology provisioner and by CLI commands (via `withDeviceWrite`), not exposed as standalone CLI verbs:

**Node-level operations:**

| Operation | Description |
|-----------|-------------|
| `AddLoopbackBGPNeighbor` | Add iBGP neighbor on loopback with optional EVPN AF |
| `RemoveBGPNeighbor` | Remove BGP neighbor by IP |
| `BGPNeighborExists` | Check if BGP neighbor exists |
| `BGPConfigured` | Check if BGP is configured |
| `SetupEVPN` | Create VTEP and activate EVPN address family |

**Interface-level operations:**

BGP neighbor configuration for eBGP peering is handled by `Interface.ApplyService()` when a service spec includes `routing.protocol = "bgp"`. The service spec drives neighbor creation, route-maps, and redistribution policies.

### 4.6a VRF Architecture

VRF is a first-class noun in the CLI with 13 subcommands. It represents a Virtual Routing and Forwarding instance in CONFIG_DB and serves as the routing context that owns interfaces, BGP neighbors, static routes, and IP-VPN bindings.

#### 4.6a.1 VRF as CONFIG_DB Entity

A VRF is a row in the `VRF` CONFIG_DB table. VRF ownership is expressed through foreign-key references in other tables:

| Owned Resource | CONFIG_DB Table | Foreign Key |
|----------------|----------------|-------------|
| Interfaces | `INTERFACE` | `vrf_name` field on `INTERFACE\|<name>` |
| BGP Neighbors | `BGP_NEIGHBOR` | `local_addr` derived from VRF interface IP |
| Static Routes | `STATIC_ROUTE` | `vrf\|prefix\|nexthop` key format |
| IP-VPN Binding | `VXLAN_TUNNEL_MAP` | L3VNI mapping for the VRF |

#### 4.6a.2 Dependency Chain

Operations on a VRF follow a dependency chain — each step requires the preceding one:

```
VRF exists
  +-- bind interfaces (add-interface)
  |     +-- add BGP neighbors (add-neighbor, requires interface IP)
  |     +-- add static routes (add-route)
  +-- bind IP-VPN (bind-ipvpn, requires VTEP from evpn setup)
```

#### 4.6a.3 Preconditions

| Operation | Preconditions |
|-----------|---------------|
| `create` | VRF name not already in use |
| `delete` | No interfaces bound, no BGP neighbors, no VNI mapping |
| `add-interface` | VRF exists, interface exists, interface not already in another VRF |
| `remove-interface` | Interface is bound to this VRF |
| `bind-ipvpn` | VRF exists, IP-VPN definition exists in network.json, VTEP configured |
| `unbind-ipvpn` | VRF has an IP-VPN binding |
| `add-neighbor` | VRF exists, interface bound to VRF, interface has IP address |
| `remove-neighbor` | Neighbor exists in CONFIG_DB |
| `add-route` | VRF exists |
| `remove-route` | Route exists in VRF |

#### 4.6a.4 Auto-Derived Neighbor IP

For `vrf add-neighbor`, if the `--neighbor` flag is omitted, the neighbor IP is auto-derived from the interface's IP address:

| Interface Prefix | Derivation | Example |
|-----------------|------------|---------|
| `/30` | XOR last 2 bits with 0x03 | `10.1.1.1/30` → neighbor `10.1.1.2` |
| `/31` | XOR last bit | `10.0.0.0/31` → neighbor `10.0.0.1` |
| Other | Must provide `--neighbor` explicitly | `/24`, `/29`, etc. |

This aligns with standard point-to-point link addressing conventions.

#### 4.6a.5 CLI Examples

```
newtron leaf1 vrf create Vrf_CUST1 -x
newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet4 -x
newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet4 65100 -x
newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x
newtron leaf1 vrf status
newtron leaf1 vrf show Vrf_CUST1
```

### 4.6b EVPN Overlay System

EVPN has been restructured from individual VNI-mapping commands into a higher-level overlay system with two concerns: device-level overlay setup and spec-level VPN definition authoring.

#### 4.6b.1 EVPN Setup (Idempotent Composite)

The `evpn setup` command is an idempotent composite that configures the full EVPN stack in one shot:

```
newtron leaf1 evpn setup -x
newtron leaf1 evpn setup --source-ip 10.0.0.10 -x
```

The operation performs three steps, skipping any that are already configured:

1. **VXLAN Tunnel Endpoint (VTEP)** — creates `VXLAN_TUNNEL` entry with source IP (defaults to device loopback IP)
2. **EVPN NVO** — creates `VXLAN_EVPN_NVO` entry referencing the VTEP
3. **BGP EVPN sessions** — creates `BGP_NEIGHBOR` entries for route reflectors from site config, with `l2vpn_evpn` address family activated

Idempotency means `evpn setup` can be run multiple times safely — it checks for existing entries before creating new ones.

#### 4.6b.2 EVPN Status

The `evpn status` command combines configuration and operational state in a single view:

```
newtron leaf1 evpn status
```

Output includes VTEP configuration, EVPN NVO, VNI mappings (L2/L3 with resource type), VRFs with L3VNI, and operational state (VTEP status, VNI count, Type-2/Type-5 route counts, remote VTEPs).

#### 4.6b.3 IP-VPN and MAC-VPN Spec Authoring

`ipvpn` and `macvpn` are sub-nouns under `evpn` for managing VPN definitions in network.json. These are spec authoring commands that do not require a device connection:

```
newtron evpn ipvpn list
newtron evpn ipvpn create customer-vpn --l3vni 10001 --route-targets 65000:10001 -x
newtron evpn ipvpn delete customer-vpn -x

newtron evpn macvpn list
newtron evpn macvpn create servers-vlan100 --vni 1100 --vlan-id 100 --arp-suppress -x
newtron evpn macvpn delete servers-vlan100 -x
```

**IP-VPN** (L3 VPN) contains: L3VNI, import/export route targets, description.
**MAC-VPN** (L2 VPN) contains: VNI, VLAN ID, anycast IP, anycast MAC, route targets, ARP suppression flag, description.

The MAC-VPN definition contains a `vlan_id` field — the local bridge domain ID. This allows the same MAC-VPN definition to carry the VLAN binding, with the anycast IP/MAC enabling distributed anycast gateway functionality. VLANs bind to MAC-VPNs via `vlan bind-macvpn`, which brings the VNI and anycast gateway from the definition.

#### 4.6b.4 CONFIG_DB Tables

| Table | Key | Purpose |
|-------|-----|---------|
| `VXLAN_TUNNEL` | Tunnel name (e.g., `vtep1`) | VTEP source IP |
| `VXLAN_EVPN_NVO` | NVO name (e.g., `nvo1`) | References source VTEP |
| `VXLAN_TUNNEL_MAP` | `tunnel\|map_name` | VNI → VLAN (L2) or VNI → VRF (L3) mapping |
| `BGP_NEIGHBOR` | Neighbor IP | EVPN peers (RRs from site config) |
| `BGP_NEIGHBOR_AF` | `ip\|l2vpn_evpn` | EVPN AF activation and attributes |

### 4.6c Spec Authoring

Several CLI nouns support **spec authoring** — creating, modifying, and deleting definitions in network.json without connecting to a device.

#### 4.6c.1 Authorable Definitions

| Noun | Subcommands | Definition Type |
|------|-------------|----------------|
| `evpn ipvpn` | `list`, `show`, `create`, `delete` | IP-VPN (L3VNI, route targets) |
| `evpn macvpn` | `list`, `show`, `create`, `delete` | MAC-VPN (VNI, VLAN ID, anycast IP/MAC, route targets, ARP suppression) |
| `qos` | `list`, `show`, `create`, `delete`, `add-queue`, `remove-queue` | QoS policy (queues, DSCP mappings) |
| `filter` | `list`, `show`, `create`, `delete`, `add-rule`, `remove-rule` | Filter spec (ACL template rules) |
| `service` | `list`, `show`, `create`, `delete` | Service (type, VPN refs, filters, QoS) |

#### 4.6c.2 Persistence

Spec authoring commands persist changes to network.json via atomic write:

1. Write to a temporary file in the same directory
2. `os.Rename` the temp file over the original (atomic on POSIX)
3. Update the in-memory cache so subsequent reads within the same session see the new data

This ensures no partial writes — the file is either fully updated or untouched.

#### 4.6c.3 Dependency Checking on Delete

Delete operations check for references before removing a definition:

| Definition | Cannot delete if... |
|------------|-------------------|
| IP-VPN | Any service references it via `ipvpn` field |
| MAC-VPN | Any service references it via `macvpn` field |
| QoS policy | Any service references it via `qos_policy` field |
| Filter | Any service references it via `ingress_filter` or `egress_filter` field |
| Service | (No cross-reference check — operator must remove from interfaces first) |

#### 4.6c.4 Permission

All spec authoring create/delete operations require `PermSpecAuthor`. Read operations (list, show) have no permission requirement.

### 4.6d Per-Noun Status

Each resource noun that has operational state owns a `status` subcommand that combines CONFIG_DB configuration with STATE_DB operational data in a single view. This replaces the old `state` command that was a separate top-level command.

| Noun | `status` Shows |
|------|---------------|
| `vlan status` | All VLANs with ID, name, L2VNI, SVI status, member count, MAC-VPN binding |
| `vrf status` | All VRFs with interface count, neighbor count, L3VNI, IP-VPN binding |
| `lag status` | All LAGs with member count, min-links, oper status, LACP state |
| `bgp status` | Local identity, neighbor summary, configured neighbors table, operational state table |
| `evpn status` | VTEP config, NVO, VNI mappings, VRFs with L3VNI, operational state |

The `status` subcommand always requires a device connection (`-d` flag or implicit device name).

### 4.7 Composite Mode

#### 4.7.1 Concept

Composite mode generates a composite CONFIG_DB configuration offline (without connecting to a device), then delivers it to the device as a single atomic operation once the device is up. This provides a newtron-native mechanism for bulk configuration delivery.

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
Node.DeliverComposite(composite, mode)
    |
    +-- Overwrite: config reload-from-composite (Redis MULTI/pipeline)
    +-- Merge: validate → pipeline write (atomic)
```

#### 4.7.5 Redis Pipeline for Atomic Delivery

`ChangeSet.Apply()` writes entries sequentially (one HSet per field) via `ConfigDBClient.Set/Delete`. Composite delivery uses a Redis pipeline for atomicity:

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
  +-- 6. Connect → DeliverComposite → Disconnect
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

Every mutating operation (`ApplyService`, `RemoveService`, `RefreshService`, `CreateVLAN`, `AddLoopbackBGPNeighbor`, `SetupEVPN`, `DeliverComposite`, etc.) returns a `ChangeSet` — a list of CONFIG_DB entries that were written. The ChangeSet is the operation's own contract for what the device state should look like after execution.

`VerifyChangeSet` re-reads CONFIG_DB through a fresh connection and confirms every entry in the ChangeSet was applied correctly:

```go
// Verify re-reads CONFIG_DB and confirms every entry in the
// ChangeSet was applied. Returns a VerificationResult listing any
// missing or mismatched entries.
func (cs *ChangeSet) Verify(n *Node) error
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
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
```

**ASIC_DB (Redis DB 1)** — routes programmed in the ASIC by `orchagent`:

```go
// GetRouteASIC reads a route from ASIC_DB by resolving the SAI
// object chain (SAI_ROUTE_ENTRY → SAI_NEXT_HOP_GROUP → SAI_NEXT_HOP).
// Returns nil if not programmed in ASIC.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error)
```

APP_DB tells you what FRR computed. ASIC_DB tells you what the hardware (or ASIC simulator) actually installed. The gap between them is `orchagent` processing.

These primitives are building blocks for external orchestrators (newtest) that have topology-wide context. For example, newtest can connect to spine1 via newtron and call `GetRoute("default", "10.1.0.0/31")` to confirm that leaf1's connected subnet arrived via BGP with the expected next-hop. newtron provides the read; newtest knows what to expect.

#### 4.9.4 Health Checks

Health checks are composed from two Node-level primitives: `CheckBGPSessions(ctx)` (verifies BGP neighbor states) and `CheckInterfaceOper(interfaces)` (verifies interface oper-up status). The `TopologyProvisioner.VerifyDeviceHealth(ctx, deviceName)` method composes both checks together with CONFIG_DB intent verification. These are local device observations that complement ChangeSet verification and routing state queries.

#### 4.9.5 What newtron Does NOT Verify

newtron operates on a single device. It cannot verify:
- **Route propagation** — "did leaf1's route arrive at spine1?" (requires connecting to spine1)
- **Fabric convergence** — "do all devices agree on routing state?" (requires connecting to all devices)
- **Data-plane forwarding** — "can traffic flow between VMs?" (requires multi-device packet injection)

These require topology-wide context and belong in the orchestrator (newtest).

#### 4.9.6 Verification Summary

| Capability | Method | Database | Returns |
|-----------|--------|----------|---------|
| CONFIG_DB writes landed | `cs.Verify(n)` | CONFIG_DB (DB 4) | Pass/fail with diff |
| Route installed by FRR | `GetRoute(vrf, prefix)` | APP_DB (DB 0) | RouteEntry or nil |
| Route programmed in ASIC | `GetRouteASIC(vrf, prefix)` | ASIC_DB (DB 1) | RouteEntry or nil |
| Operational health | `VerifyDeviceHealth(ctx, deviceName)` | STATE_DB (DB 6) | Health report |

### 4.10 CONFIG_DB Cache Architecture

newtron maintains an in-memory snapshot of CONFIG_DB (`node.Node.configDB`) loaded at `Connect()` time. This section documents the cache lifecycle, the invariant that governs it, and its limitations.

#### 4.10.1 Shared-Device Reality

A SONiC device is a shared resource. CONFIG_DB (Redis DB 4) can be modified by:
- Other newtron instances (coordinated via STATE_DB distributed lock)
- Admins running `redis-cli` or SONiC `config` commands
- Automation tools (Ansible, Salt, etc.)
- SONiC daemons (frrcfgd, orchagent, etc.)

The distributed STATE_DB lock only coordinates newtron instances cooperatively. It does not prevent external actors from writing to CONFIG_DB at any time.

#### 4.10.2 Why the Cache Exists

newtron reads CONFIG_DB for two purposes:
1. **Write-path precondition checks** (~10 methods inside `ExecuteOp` lock scope): `VRFExists`, `VLANExists`, `VTEPExists`, `BGPNeighborExists`, `ACLTableExists`, etc. These are advisory safety checks — they catch common mistakes (creating a duplicate VRF, adding a member to a non-existent VLAN) but cannot prevent all race conditions with external actors.
2. **Read-only queries** (no lock needed): `checkBGP`, `checkInterfaces`, `ListVLANs`, `ListVRFs`, etc. These are informational — they display or reason about what's configured.

Without a cache, every precondition check would require a Redis round-trip. For composite operations that check multiple preconditions, this adds up. The cache batches all table data into a single `GetAll()` call.

#### 4.10.3 The Episode Model

An **episode** is a time-boxed unit of work that reads the cache. Every episode must start with a fresh cache — no episode should depend on cache left behind by a prior episode.

| Episode type | How it refreshes | Examples |
|-------------|-----------------|---------|
| **Write episode** | `Lock()` refreshes after acquiring distributed lock | `ExecuteOp` (all mutating operations) |
| **Read-only episode** | `Refresh()` at the start | `CheckBGPSessions` / `CheckInterfaceOper`, CLI show commands |
| **Composite episode** | `Refresh()` after delivery | Composite provisioning path |
| **Initial episode** | `Connect()` loads initial snapshot | First use after connection |

Within an episode, the cache is a consistent snapshot. Between episodes, the cache must be considered stale.

#### 4.10.4 Invariant

*Every episode begins with a fresh CONFIG_DB snapshot. Within an episode, the cache is read from that snapshot. No episode relies on cache from a prior episode.*

#### 4.10.5 Cache Lifecycle

| Event | Cache action | What it sees |
|-------|-------------|-----------|
| `Connect()` | Initial `GetAll()` | Redis state at connection time |
| `Lock()` | `GetAll()` + rebuild interfaces | All changes by any actor up to lock time |
| Inside `ExecuteOp` `fn()` | Precondition reads from cache | Snapshot from Lock |
| `Apply()` | Writes to Redis only (no reload) | Cache stale (episode ending) |
| `Unlock()` | No cache action | Episode over |
| `Refresh()` | `GetAll()` + rebuild interfaces | Redis state at call time (starts a new read-only episode) |
| `CheckBGPSessions()` | `Refresh()` at entry | Fresh snapshot for health checks |

#### 4.10.6 Known Limitation

Between a refresh and the code that reads the cache, an external actor can modify CONFIG_DB. This is an inherent race condition in any system without database-level transactional isolation. The precondition checks are **advisory safety nets** — they reduce the risk of harmful changes but cannot prevent race conditions with non-newtron actors. This is acceptable because:
- In lab/test environments, newtron is typically the sole CONFIG_DB writer
- In production, coordinated change windows reduce concurrent modification risk
- The alternative (Redis WATCH/MULTI transactions) would require fundamental architectural changes for marginal benefit

## 5. Device Connection Architecture

### 5.1 Connection Flow

When `Node.Connect()` is called (which creates a `sonic.Device` and calls its `Connect()` method), the connection path depends on whether SSH credentials are present in the device's `ResolvedProfile`:

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
          stateClient.GetAll()     -- loads STATE_DB snapshot
                  |
                  v
          NewAppDBClient(addr)     -- connects to Redis DB 0
          applClient.Connect()     -- non-fatal on failure
                  |
                  v
          NewAsicDBClient(addr)    -- connects to Redis DB 1
          asicClient.Connect()     -- non-fatal on failure
```

**With SSH tunnel (production/E2E)**: SSH credentials (`ssh_user`, `ssh_pass`) are present in the device profile. The tunnel dials the device on port 22 and forwards a local random port to `127.0.0.1:6379` inside the device. Redis on SONiC listens only on localhost; port 6379 is not forwarded by QEMU, so SSH is the only path in.

**Without SSH tunnel (integration tests)**: No SSH credentials in the profile. The `MgmtIP` points directly at a standalone Redis container (`newtron-test-redis`). This mode is used by integration tests (`-tags integration`) where a plain Redis container is seeded with test fixtures, avoiding the need for a running SONiC device.

### 5.2 SSH Tunnel Implementation

The `SSHTunnel` struct (`pkg/newtron/device/sonic/types.go`) implements a TCP port forwarder over SSH:

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

`Node.Disconnect()` (which delegates to `sonic.Device.Disconnect()`) tears down in order:

1. Release device lock if held (safety net — operations release locks after verify)
2. Close ConfigDBClient (Redis DB 4)
3. Close StateDBClient (Redis DB 6)
4. Close AppDBClient (Redis DB 0)
5. Close AsicDBClient (Redis DB 1)
6. Close SSHTunnel (if present): stops accept loop, waits for goroutines, closes SSH session

## 6. StateDB Access

### 6.1 Overview

STATE_DB (Redis DB 6) provides read-only operational state that supplements the configuration view from CONFIG_DB. A `StateDB` snapshot is loaded at connect time, and the `StateDBClient` is available for fresh targeted queries (e.g., `GetBGPNeighborState`, `GetEntry`, `GetNeighbor`).

CLI status commands (`bgp status`, `vrf status`, `evpn status`) read operational state directly from `StateDBClient` for fresh data rather than relying on the connect-time snapshot.

### 6.2 Available State Data

| State Type | Source Table | Information |
|------------|-------------|-------------|
| Interface operational status | `PORT_TABLE` | `oper_status` (up/down), speed, MTU |
| LAG state | `LAG_TABLE` | Active/inactive members, operational status |
| LAG member state | `LAG_MEMBER_TABLE` | Per-member LACP state |
| BGP neighbor state | `BGP_NEIGHBOR_TABLE` | Session state (Established/Idle), prefix counts, uptime |
| VXLAN tunnel state | `VXLAN_TUNNEL_TABLE` | VTEP operational status, remote VTEPs |

### 6.3 Non-Fatal Connection Failure

STATE_DB connection failure is intentionally non-fatal. If `StateDBClient.Connect()` or `GetAll()` fails, the device logs a warning and continues operating with `StateDB == nil` and `StateDBClient == nil`. This means:

- The system can still read and write CONFIG_DB
- `Node.StateDBClient()` returns nil, allowing callers to check availability before querying
- `Node.StateDB()` returns nil if the snapshot was not loaded

This design supports environments where STATE_DB may not be fully populated (e.g. early boot, minimal test fixtures).

### 6.4 Access Patterns

Operational state is accessed through Node-level accessors:

| Accessor | Returns | Use Case |
|----------|---------|----------|
| `Node.StateDB()` | `*sonic.StateDB` (snapshot) | Interface oper-status during loadState |
| `Node.StateDBClient()` | `*sonic.StateDBClient` | Fresh targeted queries (BGP state, VRF state, neighbor entries) |
| `Node.StateDBClient().GetBGPNeighborState(vrf, ip)` | `*BGPNeighborStateEntry` | BGP session state for a specific neighbor |
| `Node.StateDBClient().GetEntry(table, key)` | `map[string]string` | Generic STATE_DB table/key lookup |
| `Node.StateDBClient().GetNeighbor(iface, ip)` | `*NeighEntry` | ARP/NDP neighbor entry |

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

Services are the primary abstraction - they bundle intent into reusable templates. Services can be created and deleted via CLI spec authoring (`service create`, `service delete`) in addition to being defined directly in network.json.

### 8.1 Service Spec Structure

```json
{
  "services": {
    "customer-l3": {
      "description": "L3 routed customer interface",
      "service_type": "routed",
      "ipvpn": "customer-vpn",
      "vrf_type": "interface",
      "vlan": 0,
      "qos_policy": "8q-datacenter",
      "ingress_filter": "customer-edge-in",
      "egress_filter": "customer-edge-out",
      "routing": {
        "protocol": "bgp",
        "peer_as": "request",
        "import_policy": "customer-import",
        "export_policy": "customer-export"
      }
    },
    "access-vlan": {
      "description": "Campus access VLAN with edge filtering",
      "service_type": "bridged",
      "ingress_filter": "campus-edge-in",
      "qos_policy": "campus-4q"
    },
    "local-irb": {
      "description": "Management VLAN with local routing",
      "service_type": "irb",
      "vrf_type": "shared",
      "ingress_filter": "mgmt-protect",
      "egress_filter": "mgmt-egress",
      "qos_policy": "mgmt-2q"
    }
  }
}
```

**What's in the spec (intent):**
- Service type (routed, bridged, irb, evpn-routed, evpn-bridged, evpn-irb)
- VPN reference (name of ipvpn/macvpn definition)
- VRF instantiation policy (per-interface or shared)
- Routing protocol and policies
- Filter references (names, not rules)
- QoS policy reference (declarative queue definitions)

**What's NOT in the spec (derived at runtime):**
- Peer IP (derived from interface IP)
- VRF name (generated from service + interface)
- ACL table names (generated)
- Specific ACL rule numbers (from filter-spec expansion)

### 8.1a Service CLI Authoring

Services can be created and deleted via CLI without editing JSON directly:

```
newtron service create customer-l3 --type routed --ipvpn cust-vpn --vrf-type shared \
  --qos-policy 8q-datacenter --ingress-filter customer-in --description "Customer L3 VPN" -x

newtron service create access-vlan --type bridged --ingress-filter campus-edge-in \
  --qos-policy campus-4q --description "Campus access VLAN" -x

newtron service create local-irb --type irb --vrf-type shared \
  --ingress-filter mgmt-protect --egress-filter mgmt-egress \
  --qos-policy mgmt-2q --description "Management VLAN with local routing" -x

newtron service delete customer-l3 -x
```

The `create` command accepts all ServiceSpec fields as flags:

| Flag | Description |
|------|-------------|
| `--type` | Service type: `routed`, `bridged`, `irb`, `evpn-routed`, `evpn-bridged`, or `evpn-irb` (required) |
| `--ipvpn` | IP-VPN reference name |
| `--macvpn` | MAC-VPN reference name |
| `--vrf-type` | VRF instantiation: `interface` or `shared` |
| `--vlan` | VLAN ID for L2/IRB services |
| `--qos-policy` | QoS policy name |
| `--ingress-filter` | Ingress filter spec name |
| `--egress-filter` | Egress filter spec name |
| `--description` | Service description |

The `delete` command verifies the service exists but does not check whether it is currently applied to interfaces — the operator must remove it from interfaces first using `service remove`.

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
| `import_community` | Community string | BGP community for import filtering |
| `export_community` | Community string | BGP community to attach on export |
| `import_prefix_list` | Prefix-list name | Prefix-list reference for import filtering |
| `export_prefix_list` | Prefix-list name | Prefix-list reference for export filtering |
| `redistribute` | `true` / `false` / omit | Override default redistribution behavior |

**Filtering composition**: Community and prefix-list can be used together. They compose as AND conditions — a route must match both the community and the prefix-list to be accepted. Policies are defined in NetworkSpecFile (`route_policies`, `prefix_lists`), referenced by name in the RoutingSpec struct.

**Redistribution defaults**:
- Service interfaces: redistribute connected subnets into BGP (default `true`)
- Transit interfaces: do NOT redistribute (default `false`)
- Loopback: always redistributed
- Set `redistribute` explicitly to override the default for any service

**Derived at runtime:**
- Peer IP (from interface IP for /30 or /31)
- Local AS (from device profile)
- Local address (interface IP)

### 8.3 Service Types

| Type            | Description                            | Requires                              |
|-----------------|----------------------------------------|---------------------------------------|
| `routed`        | L3 routed interface (local)            | IP address at apply time              |
| `bridged`       | L2 bridged interface (local)           | VLAN at apply time                    |
| `irb`           | Integrated routing and bridging (local)| VLAN + IP at apply time               |
| `evpn-routed`   | L3 routed with EVPN overlay            | `ipvpn` reference                     |
| `evpn-bridged`  | L2 bridged with EVPN overlay           | `macvpn` reference                    |
| `evpn-irb`      | IRB with EVPN overlay + anycast GW     | Both `ipvpn` and `macvpn` references  |

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
|   +-- network.json      # Services, filters, VPNs, zones
|   +-- platforms.json    # Hardware platform definitions
|   +-- topology.json     # (optional) Topology spec for automated provisioning
|   +-- profiles/         # Per-device profiles
|       +-- leaf1-ny.json
|       +-- leaf2-ny.json
|       +-- spine1-ny.json
```

### 9.2 Single Source of Truth

| Data | Source | Used By |
|------|--------|---------|
| `loopback_ip` | Device profile | BGP router-id, VTEP source |
| `mgmt_ip` | Device profile | Redis connection (or SSH tunnel target) |
| `underlay_asn` | Device profile | BGP local AS |
| `route_reflectors` | Device profile EVPN config | BGP neighbor derivation |
| `ssh_user` / `ssh_pass` | Device profile | SSH tunnel for Redis access |

### 9.3 PlatformSpec Auto-Generation
The newtron `PlatformSpec` is an abstraction (HWSKU, port_count, default_speed, breakouts). Detailed per-port lane/speed information comes from the device's own `platform.json` at runtime. A new `GeneratePlatformSpec()` device method creates/updates the PlatformSpec from the device's platform.json on first connect to a new device model:

```
Device.GeneratePlatformSpec()
  |
  +-- SSH: cat /usr/share/sonic/device/<platform>/platform.json
  +-- Parse: extract port count, default speed, breakout modes
  +-- Return: spec.PlatformSpec ready to write to platforms.json
```

This should be run at least on first connect to a new device model, and can be used to prime the spec system for a new hardware platform.

### 9.4 ClusterID Configuration
BGP route reflector cluster ID is configured in the device profile's `evpn` section:

| Field | Description |
|-------|-------------|
| `cluster_id` | BGP RR cluster-id. If not set, defaults to the spine's loopback IP. |

Route reflector configuration reads `cluster_id` from the device profile's EVPN config. If not set, it defaults to the spine's loopback IP.

### 9.5 Spec Inheritance

```
Global (network.json)
    | overrides
Zone (network.json -> zones.{name})
    | overrides
Device Profile (profiles/{device}.json)
    | resolves to
ResolvedProfile (runtime)
```

| Field | Global | Zone | Profile | Resolved |
|-------|--------|--------|---------|----------|
| `underlay_asn` | - | 64512 | (not set) | **64512** |
| `underlay_asn` | - | 64512 | 65535 | **65535** |

## 10. EVPN/VXLAN Architecture

BGP EVPN is managed via CONFIG_DB through frrcfgd. The topology provisioner uses `AddLoopbackBGPNeighbor` and `SetupEVPN` methods, while the CLI uses `evpn setup`, to activate the overlay stack. VNIs are never manipulated directly through CLI commands — they are properties of VPN definitions (IP-VPN defines L3VNI, MAC-VPN defines L2VNI) and are brought into CONFIG_DB through binding operations:

- **L2VNI**: `vlan bind-macvpn <vlan-id> <macvpn-name>` reads the L2VNI from the MAC-VPN definition and creates the `VXLAN_TUNNEL_MAP` entry
- **L3VNI**: `vrf bind-ipvpn <vrf-name> <ipvpn-name>` reads the L3VNI from the IP-VPN definition and creates the mapping

The `evpn setup` command is an idempotent composite that creates the VTEP, NVO, and BGP EVPN sessions in one shot (see section 4.6b.1).

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

## 10a. QoS Policy Architecture

QoS policies are self-contained queue definitions from which newtron derives all CONFIG_DB tables. This makes QoS consistent with how filters, VPNs, and routing already work — the spec declares intent, newtron translates it to CONFIG_DB entries.

QoS policies are CLI-authorable via the `qos` noun (see section 4.6c). The `qos apply` and `qos remove` subcommands provide surgical per-interface QoS override that takes precedence over service-managed QoS.

### 10a.1 Spec Model

A QoS policy defines 1-8 queues. Array position = queue index = traffic class. Unmapped DSCP values default to queue 0.

```json
"qos_policies": {
  "8q-datacenter": {
    "description": "8-queue datacenter policy with ECN on lossless queues",
    "queues": [
      { "name": "best-effort",   "type": "dwrr", "weight": 20, "dscp": [0] },
      { "name": "bulk",          "type": "dwrr", "weight": 15, "dscp": [8, 10, 12, 14] },
      { "name": "lossless",      "type": "dwrr", "weight": 10, "dscp": [3, 4], "ecn": true },
      { "name": "voice",         "type": "strict",             "dscp": [46] },
      { "name": "network-ctrl",  "type": "strict",             "dscp": [56] }
    ]
  }
}
```

Services reference policies by name: `"qos_policy": "8q-datacenter"`.

### 10a.1a QoS CLI Authoring and Application

**Spec authoring** (no device needed):
```
newtron qos create 4q-customer --description "4-queue customer policy" -x
newtron qos add-queue 4q-customer 0 --type dwrr --weight 50 --dscp 0 --name best-effort -x
newtron qos add-queue 4q-customer 1 --type dwrr --weight 30 --dscp 46 --name voice -x
newtron qos add-queue 4q-customer 2 --type strict --dscp 48 --name network-ctrl -x
newtron qos delete 4q-customer -x
```

**Per-interface override** (device required):
```
newtron leaf1 qos apply Ethernet0 4q-customer -x
newtron leaf1 qos remove Ethernet0 -x
```

The `qos apply` command writes PORT_QOS_MAP and QUEUE entries for the interface, overriding any QoS inherited from the service definition. The `qos remove` command deletes these per-interface entries, reverting to service-managed QoS (if any).

### 10a.2 CONFIG_DB Derivation

From one policy `P` applied to interface `Ethernet0`:

| Table | Key | Derived From | Scope |
|---|---|---|---|
| `DSCP_TO_TC_MAP\|P` | All 64 DSCPs → TC index | `queues[N].dscp` arrays, gaps → "0" | Device-wide |
| `TC_TO_QUEUE_MAP\|P` | Identity (TC N → Queue N) | Array position | Device-wide |
| `SCHEDULER\|P.N` | Type + weight | `queues[N].type/weight` | Device-wide |
| `WRED_PROFILE\|P.ecn` | ECN defaults | Created if any queue has `ecn: true` | Device-wide |
| `PORT_QOS_MAP\|Ethernet0` | Bracket-ref to maps | Per-interface binding | Per-interface |
| `QUEUE\|Ethernet0\|N` | Bracket-ref to scheduler (+WRED) | Per queue per port | Per-interface |

### 10a.3 Two-Phase Apply

**Device-wide tables** (DSCP maps, schedulers, WRED) are created once per policy per device — either by the topology provisioner during `GenerateDeviceComposite()`, or idempotently by `ApplyService()` on first use.

**Per-interface bindings** (PORT_QOS_MAP, QUEUE) are created for each interface that uses the policy.

## 10b. Filter Architecture

Newtron distinguishes between **spec-level filter templates** (in network.json) and **device-level ACL instances** (in CONFIG_DB). The `filter` CLI manages templates; the `acl` CLI manages device instances.

### 10b.1 Two-Level Design

| Level | CLI Noun | Storage | Scope | Purpose |
|-------|----------|---------|-------|---------|
| **Filter** (template) | `filter` | network.json | Network-wide | Reusable rule definitions referenced by services |
| **ACL** (instance) | `acl` | CONFIG_DB | Per-device | Concrete ACL_TABLE + ACL_RULE entries on the device |

### 10b.2 Filter → Service → ACL Instantiation

When a service is applied to an interface, the instantiation chain is:

```
filter spec (network.json)
  +-- referenced by service (ingress_filter / egress_filter)
       +-- service applied to interface (service apply)
            +-- ACL_TABLE created: "{service}-{interface}-{in|out}"
            +-- ACL_RULE entries created: one per filter rule, with derived priority
```

The filter spec defines rules abstractly (match conditions + action). The service spec references filters by name. When the service is applied, newtron instantiates the filter rules as concrete ACL entries on the device, scoped to the specific interface.

### 10b.3 Filter CLI (Spec Authoring)

```
newtron filter list
newtron filter show customer-edge-in
newtron filter create customer-edge-in --type ipv4 --description "Customer ingress filter" -x
newtron filter add-rule customer-edge-in --priority 100 --action deny --src-ip 0.0.0.0/8 -x
newtron filter add-rule customer-edge-in --priority 200 --action deny --src-ip 10.0.0.0/8 -x
newtron filter add-rule customer-edge-in --priority 1000 --action permit -x
newtron filter remove-rule customer-edge-in 100 -x
newtron filter delete customer-edge-in -x
```

Filter types correspond to ACL table types:
| Filter Type | ACL Table Type | Description |
|------------|----------------|-------------|
| `ipv4` | `L3` | IPv4 ACL |
| `ipv6` | `L3V6` | IPv6 ACL |

### 10b.4 ACL CLI (Device Instances)

The `acl` CLI operates on device-level ACL_TABLE and ACL_RULE entries directly. It is unchanged from prior versions. Both manually-created ACLs (`acl create`) and service-instantiated ACLs (from filter specs) appear in `acl list`. Service-instantiated ACLs follow the naming convention `{service}-{interface}-{direction}`.

## 11. Security Model

### 11.1 Transport Security

Redis on SONiC has no authentication and listens only on `127.0.0.1:6379`. Port 6379 is not forwarded by QEMU in virtual environments and is not exposed on the management interface. SSH is the transport security layer: all Redis access from Newtron goes through an SSH tunnel authenticated with username/password credentials stored in the device profile. The SSH tunnel ensures that Redis traffic is encrypted in transit and access is gated by SSH authentication.

In integration test environments, a standalone Redis container is used without SSH (direct TCP on port 6379). This is acceptable because the test Redis contains only synthetic fixtures, not production data.

### 11.2 Permission Levels

| Permission | Description |
|------------|-------------|
| `service.apply` | Apply services to interfaces |
| `service.remove` | Remove services from interfaces |
| `interface.configure` | Configure interface properties |
| `lag.create` | Create LAGs |
| `vlan.create` | Create/delete VLANs |
| `vlan.modify` | Modify VLAN membership |
| `acl.modify` | Modify ACLs (create/delete/bind/unbind rules) |
| `bgp.modify` | Modify BGP configuration |
| `evpn.modify` | EVPN setup and VNI operations |
| `vrf.create` | Create VRFs |
| `vrf.modify` | Modify VRF bindings (interfaces, neighbors, routes) |
| `vrf.delete` | Delete VRFs |
| `vrf.view` | View VRF details |
| `spec.author` | Create/delete definitions in network.json (IP-VPN, MAC-VPN, QoS, filter, service) |
| `filter.create` | Create filter specs |
| `filter.modify` | Add/remove filter rules |
| `filter.delete` | Delete filter specs |
| `filter.view` | View filter details |
| `qos.create` | Create QoS policies |
| `qos.delete` | Delete QoS policies |
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

### 12.3 Composite
- Offline composite CONFIG_DB generation (no device connection required)
- CompositeBuilder constructs the configuration programmatically
- Delivery is a separate step: `Node.DeliverComposite(composite, mode)`
- Two delivery modes: **overwrite** (full replace) or **merge** (interface services only)
- Delivery uses Redis pipeline (MULTI/EXEC) for atomic application
- Audit log entry created on delivery

| Mode | Online? | Device Required? | Atomic? |
|------|---------|-----------------|---------|
| Dry-run | Yes | Yes (reads CONFIG_DB) | N/A |
| Execute | Yes | Yes (reads + writes) | Per-entry |
| Composite generate | No | No | N/A |
| Composite deliver | Yes | Yes (writes) | Yes (pipeline) |

### 12.4 Topology Provisioning
- Automated device configuration from `topology.json` spec file
- Two modes: **full device** (`ProvisionDevice`) and **per-interface** (`ProvisionInterface`)
- Full device mode builds a `CompositeConfig` offline and delivers via `CompositeOverwrite`
- Per-interface mode connects to the device and uses the standard `ApplyService` path
- Topology spec declares complete desired state: devices, interfaces, service bindings, parameters

| Mode | Online? | Device Required? | Atomic? |
|------|---------|-----------------|---------|
| ProvisionDevice | No (build) + Yes (deliver) | Yes (for delivery) | Yes (pipeline) |
| ProvisionInterface | Yes | Yes (reads + writes) | Per-entry |

### 12.5 Verify (`--verify` flag)
The `--verify` flag can be appended to any execute-mode operation. After writing changes, newtron reconnects (fresh CONFIG_DB read) and runs `VerifyChangeSet` against the ChangeSet that was just applied:

```
newtron leaf1 service apply Ethernet2 customer-l3 --ip 10.1.1.1/30 -x --verify
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

**newtron primitive**: `cs.Verify(n)` — re-reads CONFIG_DB through a fresh connection, diffs against the ChangeSet. Returns `VerificationResult` with pass count, fail count, and per-entry errors.

This works for every operation (disaggregated or composite) because they all produce ChangeSets. It is the only assertion newtron makes — checking its own writes.

**Failure mode**: Hard fail. If CONFIG_DB does not contain what was written, it is a bug in Newtron.

### 13.2 Tier 2: APP_DB / Routing State — newtron (Observation Only)

**What it checks**: Routes installed by FRR via `fpmsyncd` into APP_DB (Redis DB 0).

**newtron primitives**: `Node.GetRoute(vrf, prefix)` and `Node.GetRouteASIC(vrf, prefix)` return structured `RouteEntry` data (prefix, protocol, next-hops) — or nil if the route is not present. These are observation methods, not assertions. newtron does not know whether a given route is "correct" — that depends on what other devices are advertising, which requires topology-wide context.

**How orchestrators use it**: newtest connects to a remote device via newtron and calls `GetRoute` to check that an expected route arrived. For example, after provisioning leaf1, newtest connects to spine1 and verifies that leaf1's connected subnet appears in spine1's APP_DB with the expected next-hop. newtron provides the read; newtest knows what to expect.

**ASIC_DB (Redis DB 1)**: `GetRouteASIC` resolves the SAI object chain (`SAI_ROUTE_ENTRY` → `SAI_NEXT_HOP_GROUP` → `SAI_NEXT_HOP`) to confirm the ASIC actually programmed the route. The gap between APP_DB and ASIC_DB is `orchagent` processing. On SONiC-VS with the NGDP simulator, complex routes (VXLAN, ECMP) may not make it to ASIC_DB.

### 13.3 Tier 3: Operational State — newtron (Observation Only)

**What it checks**: BGP session state, interface oper-status, LAG health, VXLAN/EVPN state via STATE_DB (Redis DB 6).

**newtron primitive**: `TopologyProvisioner.VerifyDeviceHealth(ctx, deviceName)` composes `Node.CheckBGPSessions(ctx)` and `Node.CheckInterfaceOper(interfaces)` to return a health report with per-subsystem status. This is local device health — not fabric correctness.

**SONiC-VS behavior**: BGP sessions establish reliably. Interface oper-status works for simple topologies. EVPN/VXLAN health depends on `orchagent` convergence.

### 13.4 Tier 4: Cross-Device and Data-Plane Verification — newtest

**What it checks**: Route propagation across devices, fabric-wide convergence, actual packet forwarding.

**Why it belongs in newtest, not newtron**: These checks require connecting to multiple devices and correlating their state. newtron operates on a single device and has no concept of "the fabric." newtest owns the topology and can connect to any device.

**Cross-device route verification**: newtest uses newtron's `GetRoute` primitive on multiple devices. For example: connect to spine1, call `GetRoute("default", "10.1.0.0/31")`, assert the next-hop matches leaf1's interface IP from the topology spec.

**Data-plane verification**: Ping between VMs through the SONiC fabric. The NGDP ASIC emulator in SONiC-VS does not forward packets through VXLAN tunnels, so data-plane tests soft-fail on VS. On real hardware or VPP images, this tier would be a hard fail.

### 13.5 Tier Summary

| Tier | What | Owner | newtron Method | Failure Mode |
|------|------|-------|---------------|-------------|
| CONFIG_DB | Redis entries match ChangeSet | **newtron** | `cs.Verify(n)` | Hard fail (assertion) |
| APP_DB / ASIC_DB | Routes installed by FRR / ASIC | **newtron** | `GetRoute()`, `GetRouteASIC()` | Observation (data, not verdict) |
| Operational state | BGP sessions, interface health | **newtron** | `VerifyDeviceHealth()` | Observation (health report) |
| Cross-device / data plane | Route propagation, ping | **newtest** | Composes newtron primitives | Topology-dependent |

## 14. Lab Architecture

Lab environments use **newtlab** for VM orchestration (see `docs/newtlab/`).

## 15. Testing Architecture

### 15.1 Test Tiers

| Tier | How | Purpose |
|------|-----|---------|
| Unit | `go test ./...` | Pure logic: IP derivation, spec parsing, ACL expansion |
| E2E | newtest framework | Full stack: newtlab VMs, SSH tunnel, real SONiC |

### 15.2 Unit Tests

Run with `go test ./...` (no build tags). Test pure functions and struct logic without any external dependencies. Examples: IP address derivation, prefix list expansion, spec inheritance resolution, ACL rule generation.

### 15.3 E2E Tests (newtest)

E2E testing uses the newtest framework (see `docs/newtest/`). Patterns and SONiC-specific
learnings from the legacy Go-based e2e tests are captured in `docs/newtest/e2e-learnings.md`.

## 16. Summary: Spec vs Config

| Aspect | Spec (pkg/newtron/spec) | Config (config_db) |
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
| **Interface** | A logical network endpoint on a device (physical Ethernet, LAG, VLAN, loopback). Services are applied to interfaces. |

### Spec Types (Go structs)

| Type | File | Purpose |
|------|------|---------|
| `NetworkSpecFile` | `network.json` | Global network settings: services, filters, VPNs, zones, prefix lists |
| `PlatformSpecFile` | `platforms.json` | Hardware definitions: HWSKU, port counts, breakout modes |
| `TopologySpecFile` | `topology.json` | (optional) Devices, interfaces, service bindings, links |
| `DeviceProfile` | `profiles/{name}.json` | Per-device settings: IPs, site membership, SSH credentials, overrides |
| `ResolvedProfile` | (runtime) | Fully resolved device values after inheritance |

### Service Types

| Type | VNI | L3VNI | SVI | Use Case |
|------|-----|-------|-----|----------|
| `routed` | No | No | No | Local L3 routed interface |
| `bridged` | No | No | No | Local L2 bridged interface |
| `irb` | No | No | Yes | Local integrated routing and bridging |
| `evpn-routed` | No | Yes | No | L3 routed with EVPN overlay |
| `evpn-bridged` | Yes | No | No | L2 bridged with EVPN overlay |
| `evpn-irb` | Yes | Yes | Yes | IRB with EVPN overlay + anycast GW |

### VPN Terminology

| Term | Definition |
|------|------------|
| **IPVPN** | IP-VPN definition for L3 routing. Contains L3VNI and route targets. CLI-authorable via `evpn ipvpn create/delete`. |
| **MACVPN** | MAC-VPN definition for L2 bridging. Contains VNI, VLAN ID, anycast IP, anycast MAC, route targets, and ARP suppression flag. CLI-authorable via `evpn macvpn create/delete`. VLANs bind to MAC-VPNs via `vlan bind-macvpn`. |
| **L2VNI** | Layer 2 VNI for VXLAN bridging (MAC learning, BUM traffic). |
| **L3VNI** | Layer 3 VNI for VXLAN routing (inter-subnet traffic). |
| **VRF** | Virtual Routing and Forwarding instance for traffic isolation. First-class CLI noun with 13 subcommands: owns interfaces, BGP neighbors, static routes, and IP-VPN bindings. |
| **Route Target (RT)** | BGP extended community controlling VPN route import/export. |

### VRF Types

| Type | VRF Naming | Use Case |
|------|------------|----------|
| `interface` | `{service}-{interface}` | Per-interface isolation (e.g., `customer-Ethernet0`) |
| `shared` | `{ipvpn-name}` | Multiple interfaces share one VRF |

### Inheritance

| Term | Definition |
|------|------------|
| **Inheritance Chain** | Resolution order: Profile > Zone > Global. First non-empty value wins. |
| **Override** | A value in a profile that replaces the inherited zone/global value. |
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
| 6 | STATE_DB | Operational state (read-only by Newtron via `VerifyDeviceHealth` / `CheckBGPSessions`) |

### CLI Architecture
| Term | Definition |
|------|------------|
| **Noun-Group CLI** | CLI pattern `newtron <device> <noun> <action> [args] [-x]`. All commands are organized by resource noun (vlan, vrf, bgp, etc.). |
| **Implicit Device** | The first argument is treated as a device name if it does not match a known command. Equivalent to `-d <device>`. |
| **withDeviceWrite** | CLI helper that encapsulates connect → lock → execute → print changeset → apply/dry-run → unlock → disconnect for all device-level write commands. |
| **Spec Authoring** | CLI-authored definitions (IP-VPN, MAC-VPN, QoS, filter, service) that persist to network.json via atomic write (temp file + rename). No device connection required. |
| **Per-Noun Status** | Each resource noun owns its `status` subcommand combining CONFIG_DB config with operational state from STATE_DB. Replaces the old `state` command. |
| **Filter vs ACL** | A filter is a spec-level template in network.json (reusable across devices); an ACL is a device-level CONFIG_DB instance. `service apply` instantiates filters as ACLs. |

### Operations

| Term | Definition |
|------|------------|
| **Dry-Run** | Preview mode (default). Shows what would change without applying. |
| **Execute (`-x`)** | Apply mode. Actually writes changes to device config_db. |
| **Save (`-s`)** | Persist runtime CONFIG_DB to `/etc/sonic/config_db.json` after execute. Requires `-x`. |
| **ChangeSet** | Collection of pending changes to be previewed or applied. Also serves as the verification contract — `cs.Verify(n)` diffs the ChangeSet against live CONFIG_DB. |
| **ChangeSet.Verify(n \*Node)** | Re-reads CONFIG_DB through a fresh connection and confirms every entry in the ChangeSet was applied correctly. The only assertion newtron makes. |
| **GetRoute** | Reads a route from APP_DB (DB 0) and returns structured data (prefix, protocol, next-hops). Observation primitive — returns data, not a verdict. |
| **GetRouteASIC** | Reads a route from ASIC_DB (DB 1) by resolving SAI object chain. Confirms the ASIC programmed the route. |
| **Audit Log** | Record of all executed changes with user, timestamp, and details. |
| **Device Lock** | Per-operation distributed lock in STATE_DB (Redis) with TTL. Each mutating operation acquires the lock, applies changes, verifies, and releases. Prevents concurrent modifications to the same device. |
| **Baseline Reset** | Pre-test cleanup that deletes stale CONFIG_DB entries from previous test runs on all SONiC nodes. |

### BGP Management
| Term | Definition |
|------|------------|
| **frrcfgd** | SONiC's FRR management framework daemon. Translates CONFIG_DB BGP tables to FRR commands. Enabled via `frr_mgmt_framework_config=true` in DEVICE_METADATA. |
| **Peer Group** | BGP peer group template. Neighbors assigned to a peer group inherit its settings. Managed via BGP_PEER_GROUP table. |
| **Route-Map** | Ordered set of match/set rules for BGP route filtering and attribute modification. Managed via ROUTE_MAP table. |
| **Prefix-Set** | IP prefix list used in route-map match clauses. Managed via PREFIX_SET table. |
| **Community-Set** | BGP community list for matching or setting community attributes. Managed via COMMUNITY_SET table. |
| **Redistribute** | Injection of connected or static routes into BGP. Controlled per-service via `redistribute` flag with opinionated defaults. |
| **Cluster-ID** | BGP route reflector cluster identifier. Prevents routing loops when multiple RRs exist in the same cluster. Set in device profile EVPN config or defaults to spine loopback. |

### Composite Mode
| Term | Definition |
|------|------------|
| **Composite** | A composite CONFIG_DB configuration generated offline, delivered to a device as a single atomic operation. |
| **Overwrite Mode** | Composite delivery mode that replaces the entire CONFIG_DB with composite content. Used for initial provisioning. |
| **Merge Mode** | Composite delivery mode that adds entries to existing CONFIG_DB. Restricted to interface-level services with no existing binding. |
| **CompositeBuilder** | Builder pattern for constructing composite configs offline without device connection. |
| **CompositeConfig** | The composite CONFIG_DB representation with metadata (timestamp, network, device, mode). |
| **Redis Pipeline** | MULTI/EXEC transaction used by composite delivery for atomic application of multiple CONFIG_DB changes. |

### Port Management
| Term | Definition |
|------|------------|
| **platform.json** | SONiC device file at `/usr/share/sonic/device/<platform>/platform.json`. Defines available ports, lane assignments, supported speeds, and breakout modes. |
| **Port Creation** | Creating a PORT entry in CONFIG_DB, validated against platform.json to ensure valid lanes, speeds, and no breakout conflicts. |
| **Breakout** | Splitting a high-speed port into multiple lower-speed ports (e.g., 1x100G → 4x25G). Creates child ports, removes parent. |
| **PlatformConfig** | Parsed representation of platform.json cached on device. Used for port validation. |
| **GeneratePlatformSpec** | Device method that creates a `spec.PlatformSpec` from the device's platform.json, for priming specs on first connect to new hardware. |

### Topology Provisioning
| Term | Definition |
|------|------------|
| **topology.json** | Optional spec file declaring complete desired state: devices, interfaces, service bindings, links, and per-interface parameters. |
| **ProvisionDevice** | Full-device provisioning mode. Builds a CompositeConfig offline from topology.json, delivers via CompositeOverwrite. No device connection required during build. |
| **ProvisionInterface** | Per-interface provisioning mode. Reads service and parameters from topology.json, connects to the device, and uses the standard ApplyService path with full precondition checking. |
| **TopologySpecFile** | Go struct representing the parsed topology.json file. Contains device definitions, interface bindings, and link declarations. |
