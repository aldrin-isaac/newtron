# Newtron High-Level Design (HLD)

## 1. Purpose

Newtron is an opinionated network automation tool for SONiC-based switches. It treats SONiC as what it is — a Redis database with daemons that react to table changes — and interacts with it accordingly. Where other tools SSH in and parse CLI output, newtron reads and writes CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB directly through an SSH-tunneled Redis client.

Newtron enforces network design intent expressed as declarative spec files while allowing many degrees of freedom for actual deployments. The specs define what the network *must* look like (services, filters, routing policies); newtron translates that intent into concrete CONFIG_DB entries using each device's context (IPs, AS numbers, platform capabilities).

For the architectural principles behind newtron, newtlab, and newtrun — including the object hierarchy, verification ownership, and DRY design — see [Design Principles](../DESIGN_PRINCIPLES.md).

## 2. Architecture

### 2.1 System Architecture

```
                                          ┌─────────────────────────────────┐
                                          │                                 │
                                          │               CLI               │
                                          │          (cmd/newtron)          │
                                          │           HTTP client           │
                                          │                                 │
                                          └─────────────────────────────────┘
                                            │
                                            │ HTTP
                                            │ (REST API)
                                            ▼
┌─────────────────────────┐               ┌─────────────────────────────────┐
│                         │               │                                 │
│         newtrun         │               │         newtron-server          │
│     (pkg/newtrun/)      │  HTTP         │       (pkg/newtron/api/)        │
│       HTTP client       │  (REST API)   │                                 │
│                         │ ────────────▶ │                                 │
└─────────────────────────┘               └─────────────────────────────────┘
                                            │
                                            │
                                            ▼
┌─────────────────────────┐               ┌─────────────────────────────────┐
│                         │               │                                 │
│        NodeActor        │               │          NetworkActor           │
│     (1 per device)      │               │         (1 per network)         │
│ caches *Node (SSH conn) │  manages      │          owns *Network          │
│                         │ ◀──────────── │                                 │
└─────────────────────────┘               └─────────────────────────────────┘
  │                                         │
  │                                         │
  │                                         ▼
  │                                       ┌─────────────────────────────────┐     ┌─────────────────────┐
  │                                       │                                 │     │                     │
  │                                       │          Network Layer          │     │                     │
  │                                       │     (pkg/newtron/network/)      │     │     Spec Layer      │
  │                                       │        Spec resolution,         │     │ (pkg/newtron/spec/) │
  │                                       │      topology provisioning      │     │                     │
  │                                       │                                 │ ──▶ │                     │
  │                                       └─────────────────────────────────┘     └─────────────────────┘
  │                                         │
  │                                         │
  │                                         ▼
  │                                       ┌─────────────────────────────────┐
  │                                       │                                 │
  │                                       │           Node Layer            │
  │                                       │   (pkg/newtron/network/node/)   │
  │                                       │        Node, Interface,         │
  │                         Connect()     │       ChangeSet, *_ops.go       │
  └─────────────────────────────────────▶ │                                 │
                                          └─────────────────────────────────┘
                                            │
                                            │
                                            ▼
                                          ┌─────────────────────────────────┐
                                          │                                 │
                                          │          Device Layer           │
                                          │   (pkg/newtron/device/sonic/)   │
                                          │    SSH Tunnel > ConfigDB(4)     │
                                          │ StateDB(6), AppDB(0), AsicDB(1) │
                                          │                                 │
                                          └─────────────────────────────────┘
                                            │
                                            │
                                            ▼
                                          ┌─────────────────────────────────┐
                                          │                                 │
                                          │          SONiC Switch           │
                                          │             (Redis)             │
                                          │                                 │
                                          └─────────────────────────────────┘
```

### 2.2 How the Pieces Fit Together

**newtron-server** is the central process. It manages network specs and device connections through a hierarchy of actors, and exposes all operations as an HTTP REST API. The CLI and newtrun are HTTP clients — they import `pkg/newtron/client/` and shared types, never internal packages.

The server uses an **actor model** with a containment hierarchy:

- A **NetworkActor** (one per registered network) owns a `*newtron.Network` and serializes spec operations. It also creates and manages **NodeActor** instances — one per device that has been accessed.
- Each **NodeActor** holds a reference to the parent's `*Network` (for `Connect()` and spec access) and caches the SSH connection (`*newtron.Node`) with a configurable idle timeout (default 5 minutes), eliminating ~200ms SSH overhead per request.
- Every request still refreshes CONFIG_DB from Redis (`Lock()` for writes, `Refresh()` for reads), so operations always see current device state.

HTTP handlers follow a two-step resolution: server → NetworkActor (by network ID) → NodeActor (by device name). Spec operations (service list, filter create) dispatch to the NetworkActor directly. Device operations (VLAN create, health check) dispatch to the NodeActor.

### 2.3 Layer Responsibilities

| Layer | Package | Driven By | Responsibility |
|-------|---------|-----------|----------------|
| **Public API** | `pkg/newtron/` | Both actors | Entry point; wraps internal types in domain vocabulary. Write methods capture ChangeSets internally and return `error`. |
| **Network** | `pkg/newtron/network/` | NetworkActor | Spec loading, spec resolution (network→zone→node inheritance), topology provisioning. Creates Nodes via `Connect()`. |
| **Node** | `pkg/newtron/network/node/` | NodeActor | Node, Interface, ChangeSet, all `*_ops.go` operations. CONFIG_DB reads and writes. |
| **Spec** | `pkg/newtron/spec/` | Network layer | JSON file I/O for specs (network.json, profiles, platforms, topology). |
| **Device** | `pkg/newtron/device/sonic/` | Node layer | SSH tunnel, Redis clients (ConfigDB, StateDB, AppDB, AsicDB). Pure connection infrastructure — no domain logic. |

### 2.4 Object Hierarchy

The system uses an object-oriented design with parent references. The governing principle: **a method belongs to the smallest object that has all the context to execute it.**

```
┌────────────────────┐
│                    │
│      Network       │
│  (owns all specs)  │
│                    │
└────────────────────┘
  │
  │ creates
  │ (parent ref)
  ▼
┌────────────────────┐
│                    │
│        Node        │
│  (device handle)   │
│                    │
└────────────────────┘
  │
  │ creates
  │ (parent ref)
  ▼
┌────────────────────┐
│                    │
│     Interface      │
│ (interface handle) │
│                    │
└────────────────────┘
```

Whatever configuration can be right-shifted to the interface level, should be. eBGP neighbors are interface-specific — they derive from the interface's IP and the service's peer AS — so they are created by `Interface.ApplyService()`. Route reflector peering is device-specific — it derives from the device's role and zone topology — so it lives on `Node.SetupEVPN()`.

### 2.5 Key Types

| Type | Package | Layer | Description |
|------|---------|-------|-------------|
| `newtron.Network` | `pkg/newtron/` | Public API | Entry point; wraps `network.Network` |
| `newtron.Node` | `pkg/newtron/` | Public API | Device handle with pending change management; wraps `node.Node` |
| `newtron.Interface` | `pkg/newtron/` | Public API | Interface handle; wraps `node.Interface` |
| `network.Network` | `pkg/newtron/network/` | Network | Spec loading, topology provisioning, `Connect()` |
| `node.Node` | `pkg/newtron/network/node/` | Node | Redis connection, CONFIG_DB operations, all `*_ops.go` |
| `node.Interface` | `pkg/newtron/network/node/` | Node | Interface-scoped operations (ApplyService, etc.) |

### 2.6 End-to-End Request Walkthrough

A concrete trace of `newtron leaf1 vlan create 100 --name servers -x` from keystroke to Redis:

```
CLI (cmd/newtron)
  │  Sends POST /network/default/node/leaf1/vlan
  │  Body: {"id": 100, "name": "servers"}
  │  Query: ?execute=true
  │
  ▼
newtron-server (pkg/newtron/api/)
  │  handleVLANCreate():
  │    server.getNetwork("default")        → NetworkActor
  │    networkActor.getNodeActor("leaf1")   → NodeActor
  │
  ▼
NodeActor
  │  Dispatches via connectAndExecute:
  │
  │  1. getNode()        — returns cached *Node (or Connect() if first request)
  │  2. node.Execute()   — Lock (refresh ConfigDB from Redis)
  │                        → CreateVLAN(100, "servers") returns ChangeSet
  │                        → Commit: Apply ChangeSet to Redis (HSET VLAN|Vlan100 ...)
  │                                  Verify: re-read CONFIG_DB, diff against ChangeSet
  │                        → Save: SSH `config save -y`
  │                        → Unlock
  │  3. Reset idle timer  — connection stays cached for next request
  │
  ▼
Response → CLI
  WriteResult{ChangeCount: 3, Applied: true, Verification: passed}
  CLI prints: "Changes applied successfully."
```

A spec-only operation like `newtron service list` is simpler: CLI sends GET to server, server dispatches to NetworkActor (no NodeActor involved), NetworkActor reads specs from `*Network`, returns the list.

## 3. Spec vs Config

Newtron enforces a clear separation between **specification** (declarative intent) and **configuration** (imperative device state). This distinction governs every design decision.

### 3.1 Specification (Intent)

Specs describe **what you want**. They are declarative, abstract, and policy-driven.

```json
{
  "services": {
    "customer-l3": {
      "service_type": "evpn-routed",
      "ipvpn": "customer-vpn",
      "vrf_type": "interface",
      "routing": { "protocol": "bgp", "peer_as": "request" },
      "ingress_filter": "customer-edge-in"
    }
  }
}
```

This declares intent: "Customer interfaces should use EVPN L3 overlay with BGP, this IP-VPN, and this filter." It doesn't specify peer IPs, VRF names, VNIs, or ACL rule numbers — those are derived at runtime from the IP-VPN definition and device context.

### 3.2 Configuration (Device State)

Config is **what the device uses**. It is imperative, concrete, and device-specific, generated at runtime by combining a spec with context (interface, IP address, device profile).

```json
{
  "VRF": { "customer-l3-Ethernet0": { "vni": "10001" } },
  "VXLAN_TUNNEL_MAP": {
    "vtep1|map_10001": { "vni": "10001", "vrf": "customer-l3-Ethernet0" }
  },
  "INTERFACE": {
    "Ethernet0": { "vrf_name": "customer-l3-Ethernet0" },
    "Ethernet0|10.1.1.1/30": {}
  },
  "BGP_NEIGHBOR": {
    "10.1.1.2": { "asn": "65100", "local_asn": "64512", "local_addr": "10.1.1.1" }
  },
  "ACL_TABLE": {
    "customer-l3-Ethernet0-in": { "type": "L3", "stage": "ingress", "ports": ["Ethernet0"] }
  }
}
```

The VNI (`10001`) comes from the IP-VPN definition `customer-vpn`, not from the service spec. The VRF name is derived from `{service}-{interface}` because `vrf_type` is `"interface"`.

### 3.3 Translation

The translation layer interprets specs in context to generate config:

```
                                                 ┌───────────────────────────────────────┐
                                                 │                                       │
                                                 │                Context                │
                                                 │ Interface: Ethernet0, IP: 10.1.1.1/30 │
                                                 │   Device AS: 64512, Peer AS: 65100    │
                                                 │   IP-VPN customer-vpn: L3VNI 10001    │
                                                 │                                       │
                                                 └───────────────────────────────────────┘
                                                   │
                                                   │
                                                   ▼
┌──────────────────────────────────────────┐     ┌───────────────────────────────────────┐
│                                          │     │                                       │
│            Spec (declarative)            │     │                                       │
│     evpn-routed, ipvpn=customer-vpn,     │     │              Translation              │
│ peer_as=request, filter=customer-edge-in │     │                                       │
│                                          │ ──▶ │                                       │
└──────────────────────────────────────────┘     └───────────────────────────────────────┘
                                                   │
                                                   │
                                                   ▼
                                                 ┌───────────────────────────────────────┐
                                                 │                                       │
                                                 │          Config (imperative)          │
                                                 │        VRF, VXLAN_TUNNEL_MAP,         │
                                                 │        BGP_NEIGHBOR, ACL_TABLE        │
                                                 │                                       │
                                                 └───────────────────────────────────────┘
```

Translation follows a three-layer pattern in the Node Layer:

1. **Config functions** — pure functions in each `*_ops.go` file that return `[]sonic.Entry`. No side effects.
2. **`service_gen.go`** — translates a service spec into CONFIG_DB entries by calling config functions from owning `*_ops.go` files.
3. **Operations** — methods on Interface/Node that run preconditions, call generators, and wrap results in a ChangeSet.

### 3.4 What Belongs Where

| In Spec (Declarative) | Derived at Runtime |
|-----------------------|--------------------|
| Service type (routed, bridged, irb, evpn-*) | VRF name |
| VPN reference (ipvpn, macvpn) | Peer IP (from interface IP) |
| Routing protocol (bgp, static) | ACL table name |
| Peer AS policy ("request" or fixed) | ACL rule sequence numbers |
| Filter-spec reference | Local AS (from device profile) |
| Route policy references | Router ID (from loopback IP) |

### 3.5 Hierarchical Spec Resolution

Specs participate in a three-level hierarchy: **network → zone → node**. Each level can define or override any of the 7 overridable spec types (services, filters, IP-VPNs, MAC-VPNs, QoS policies, route policies, prefix lists).

Resolution is **union with lower-level-wins**: if the same spec name exists at multiple levels, the most specific level wins (node > zone > network). Specs at different levels with different names are all visible.

```
┌──────────────────────────┐
│                          │
│         Network          │
│      (network.json)      │
│                          │
└──────────────────────────┘
  │
  │ overrides
  ▼
┌──────────────────────────┐
│                          │
│           Zone           │
│      (zones.{name})      │
│                          │
└──────────────────────────┘
  │
  │ overrides
  ▼
┌──────────────────────────┐
│                          │
│          Node            │
│ (profiles/{device}.json) │
│                          │
└──────────────────────────┘
  │
  │ resolves to
  ▼
┌──────────────────────────┐
│                          │
│     ResolvedSpecs        │
│        (runtime)         │
│                          │
└──────────────────────────┘
```

At runtime, `buildResolvedSpecs()` merges all three levels into a `ResolvedSpecs` snapshot per node. This snapshot implements the `SpecProvider` interface used by all node operations — lookups fall through from node to zone to network until a match is found.

### 3.6 Spec File Structure

```
specs/
├── network.json      # Services, filters, VPNs, zones, permissions
├── platforms.json    # Hardware platform definitions
├── topology.json     # (optional) Topology for automated provisioning
└── profiles/         # Per-device profiles
    ├── leaf1-ny.json
    └── spine1-ny.json
```

## 4. newtron-server

### 4.1 Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8080` | Listen address |
| `--spec-dir` | (none) | Spec directory to auto-register as a network on startup |
| `--net-id` | `default` | Network ID for the auto-registered spec directory |
| `--idle-timeout` | `5m` | SSH connection idle timeout. `0` = default (5m). Negative = disable caching |

### 4.2 Actor Model

| Actor | Scope | Owns | Manages | One Per |
|-------|-------|------|---------|---------|
| `NetworkActor` | Spec operations | `*newtron.Network` | All `NodeActor` instances for its devices | Registered network |
| `NodeActor` | Device operations | Cached `*newtron.Node`, `*Network` ref | — | Device name |

`NetworkActor` is the parent actor. It serializes spec reads and writes (service create/delete, filter authoring) and creates `NodeActor` instances on demand when a device is first accessed. Each `NodeActor` receives a `*Network` reference from its parent (used for `Connect()` and spec resolution) and serializes all device operations (VLAN create, service apply, health check). The actor model naturally queues concurrent requests — essential when operations involve SSH round-trips.

### 4.3 Connection Caching

Each `NodeActor` caches the SSH connection (`*newtron.Node`) between requests:

1. **First request**: `Connect()` establishes SSH tunnel + loads CONFIG_DB. Connection cached.
2. **Subsequent requests**: Reuse cached connection. `Lock()` (writes) or `Refresh()` (reads) reloads CONFIG_DB from Redis.
3. **Idle timeout**: After `idleTimeout` of no requests (default 5 minutes), the cached connection is closed automatically.
4. **Error recovery**: If `Refresh()` or `Lock()` fails (SSH tunnel died), the connection is closed. Next request reconnects.

This saves ~200ms per request while maintaining the episodic caching invariant: every operation begins with fresh CONFIG_DB state.

### 4.4 HTTP API Design

Every public API method has a 1:1 HTTP endpoint. Three helper patterns dispatch requests to the NodeActor:

| Pattern | Server-Side Lifecycle | Used By |
|---------|----------------------|---------|
| `connectAndRead` | getNode → Refresh → fn | Show, list, status, health commands |
| `connectAndLocked` | getNode → Lock → fn → Unlock | DeliverComposite, direct Redis writes |
| `connectAndExecute` | getNode → Execute(Lock→fn→Commit→Save→Unlock) | All mutating operations |

### 4.5 Network Registration

Networks are registered at startup (via `--spec-dir`) or at runtime (via HTTP). Each registration loads the spec directory into a `*newtron.Network` and creates a `NetworkActor`. Multiple networks can be registered simultaneously.

A registered network's specs can be reloaded from disk via `POST /network/{netID}/reload` without restarting the server. This uses an atomic stop-and-replace pattern: the server stops the old NetworkActor (draining all NodeActors and SSH connections), reloads specs from the stored spec directory, and creates a fresh NetworkActor. SSH connections reconnect lazily on the next request. This is the same proven pattern as unregister+register, avoiding the race conditions that a hot-swap of `na.net` would introduce.

## 5. CLI Layer

The CLI (`cmd/newtron`) is an HTTP client built with the Cobra framework. It imports `pkg/newtron/client/` and shared types — never internal packages. All device operations are HTTP requests to newtron-server.

### 5.1 Command Pattern

```
newtron <device> <noun> <action> [args] [-x]
```

The first argument is the device name unless it matches a known command. This lets users write `newtron leaf1 vlan list` instead of `newtron -D leaf1 vlan list`.

### 5.2 Flags

**Context:**

| Flag | Description |
|------|-------------|
| `-D, --device` | Target device name (alternative to positional first arg) |
| `--server` | newtron-server URL (default `http://localhost:8080`, env `NEWTRON_SERVER`) |
| `-N, --network-id` | Network ID on the server (default `default`, env `NEWTRON_NETWORK_ID`) |

**Write:**

| Flag | Description |
|------|-------------|
| `-x, --execute` | Execute changes (default is dry-run preview). Save runs automatically after apply. |
| `--no-save` | Skip `config save -y` after execute (requires `-x`) |

**Output:**

| Flag | Description |
|------|-------------|
| `--json` | JSON output |
| `-v, --verbose` | Verbose/debug output |

### 5.3 Two Command Scopes

| Scope | Target | Needs Device | Examples |
|-------|--------|-------------|----------|
| **Device-required** | CONFIG_DB on a SONiC switch (via server) | Yes | `vlan create`, `service apply`, `evpn setup` |
| **No-device** | network.json specs (via server) | No | `service list`, `evpn ipvpn create`, `filter list` |

The CLI is a thin rendering layer. The server handles all lifecycle management — connection, locking, pending changes, commit, verification, and save. The CLI sends the request, receives the result, and formats it for display.

### 5.4 Interface Name Formats

| Short Form | Full Form (SONiC) |
|------------|-------------------|
| `Eth0` | `Ethernet0` |
| `Po100` | `PortChannel100` |
| `Vl100` | `Vlan100` |
| `Lo0` | `Loopback0` |

## 6. Service Model

Services are the primary abstraction — they bundle intent into reusable templates.

### 6.1 Service Types

| Type | Description | Requires |
|------|-------------|----------|
| `routed` | L3 routed interface (local) | IP address at apply time |
| `bridged` | L2 bridged interface (local) | VLAN at apply time |
| `irb` | Integrated routing and bridging (local) | VLAN + IP at apply time |
| `evpn-routed` | L3 routed with EVPN overlay | `ipvpn` reference |
| `evpn-bridged` | L2 bridged with EVPN overlay | `macvpn` reference |
| `evpn-irb` | IRB with EVPN overlay + anycast GW | Both `ipvpn` and `macvpn` references |

### 6.2 Service Spec Structure

```json
{
  "customer-l3": {
    "description": "L3 routed customer interface with EVPN overlay",
    "service_type": "evpn-routed",
    "ipvpn": "customer-vpn",
    "vrf_type": "interface",
    "qos_policy": "8q-datacenter",
    "ingress_filter": "customer-edge-in",
    "egress_filter": "customer-edge-out",
    "routing": {
      "protocol": "bgp",
      "peer_as": "request",
      "import_policy": "customer-import",
      "export_policy": "customer-export"
    }
  }
}
```

**In the spec (intent):** service type, VPN reference, VRF policy, routing protocol, filter references, QoS policy reference.

**NOT in the spec (derived at runtime):** peer IP, VRF name, ACL table names, ACL rule numbers, local AS, router ID.

### 6.3 VRF Instantiation

| `vrf_type` | VRF Name | Use Case |
|------------|----------|----------|
| `"interface"` | `{service}-{interface}` | Per-customer isolation |
| `"shared"` | ipvpn definition name | Multiple interfaces share one VRF |
| (omitted) | Global routing table | Transit (no EVPN) |

### 6.4 Routing Spec

The `routing` section declares routing intent:

| Field | Values | Description |
|-------|--------|-------------|
| `protocol` | `"bgp"`, `"static"`, `""` | Routing protocol |
| `peer_as` | Number or `"request"` | Peer AS (fixed or user-provided) |
| `import_policy` / `export_policy` | Policy name | BGP route policy |
| `import_community` / `export_community` | Community string | BGP community filtering |
| `import_prefix_list` / `export_prefix_list` | Prefix-list name | Prefix-list filtering |
| `redistribute` | `true` / `false` / omit | Override default redistribution |

**Redistribution defaults:** Service interfaces redistribute connected into BGP (default `true`). Transit interfaces do not (default `false`). Loopback always redistributed.

### 6.5 Service Operations

- **ApplyService** — translates spec + context into CONFIG_DB entries, applying them to the interface. Creates VRF, ACL, IP, BGP neighbor, EVPN mappings, service binding.
- **RemoveService** — reverse of ApplyService. Reads the binding record to determine what was applied. Uses DependencyChecker to protect shared resources (VRFs, ACLs) referenced by other interfaces.
- **RefreshService** — full remove+reapply cycle. The two ChangeSets merge, preserving intermediate DEL operations (required because Redis HSET merges fields, so DEL is needed to remove stale fields).

## 7. Resource Nouns

Each major CONFIG_DB resource is a CLI noun with read and write subcommands.

### 7.1 VRF

First-class noun with 13 subcommands. Owns interfaces, BGP neighbors, static routes, and IP-VPN bindings.

**Dependency chain:**
```
VRF exists
  ├── bind interfaces (add-interface)
  │     ├── add BGP neighbors (add-neighbor, requires interface IP)
  │     └── add static routes (add-route)
  └── bind IP-VPN (bind-ipvpn, requires VTEP from evpn setup)
```

**Auto-derived neighbor IP:** For `add-neighbor`, if `--neighbor` is omitted, the neighbor IP is derived from the interface's IP: `/30` XORs last 2 bits, `/31` XORs last bit.

### 7.2 EVPN Overlay

Two concerns: device-level overlay setup and spec-level VPN definition authoring.

**`evpn setup`** — idempotent composite that configures the full EVPN stack:
1. VXLAN Tunnel Endpoint (VTEP) with source IP (defaults to device loopback)
2. EVPN NVO referencing the VTEP
3. BGP EVPN sessions from device profile, with L2VPN EVPN AF activated

**IP-VPN and MAC-VPN** — spec authoring sub-nouns under `evpn`. No device connection required.
- **IP-VPN** (L3 VPN): VRF name, L3VNI, L3VNI VLAN (transit), route targets
- **MAC-VPN** (L2 VPN): VNI, VLAN ID, anycast IP/MAC, route targets, ARP suppression

VNIs are properties of VPN definitions, brought into CONFIG_DB through binding operations:
- L2VNI: `vlan bind-macvpn <vlan-id> <macvpn-name>`
- L3VNI: `vrf bind-ipvpn <vrf-name> <ipvpn-name>`

### 7.3 BGP

Visibility-only noun with a single `status` subcommand. Combines local identity, configured neighbors, operational state, and expected EVPN peers. All BGP peer management is through other nouns (`vrf add-neighbor`, `evpn setup`).

SONiC BGP is managed via **frrcfgd** (FRR management framework) — full CONFIG_DB → FRR translation. This gives newtron complete BGP management through CONFIG_DB for underlay, IPv6, and L2/L3 EVPN overlays.

### 7.4 Spec Authoring Nouns

Several nouns support creating, modifying, and deleting definitions in network.json:

| Noun | Definition Type |
|------|----------------|
| `evpn ipvpn` | IP-VPN (L3VNI, route targets) |
| `evpn macvpn` | MAC-VPN (VNI, VLAN ID, anycast IP/MAC) |
| `qos` | QoS policy (queues, DSCP mappings) |
| `filter` | Filter spec (ACL template rules) |
| `service` | Service (type, VPN refs, filters, QoS) |

Spec changes persist atomically (write to temp file, then `os.Rename`). Deletes check for cross-references (e.g., cannot delete an IP-VPN referenced by a service).

### 7.5 Per-Noun Status

Each resource noun with operational state owns a `status` subcommand combining CONFIG_DB config with STATE_DB operational data:

| Noun | `status` Shows |
|------|---------------|
| `vlan status` | All VLANs with ID, name, L2VNI, SVI status, member count, MAC-VPN binding |
| `vrf status` | All VRFs with interface count, neighbor count, L3VNI, IP-VPN binding |
| `lag status` | All LAGs with member count, min-links, oper status, LACP state |
| `bgp status` | Local identity, neighbor summary, configured/operational state |
| `evpn status` | VTEP config, NVO, VNI mappings, VRFs with L3VNI, operational state |

### 7.6 QoS

QoS policies are self-contained queue definitions from which newtron derives all CONFIG_DB tables (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE, PORT_QOS_MAP, QUEUE). Services reference policies by name. Device-wide tables are created once per policy; per-interface bindings are per-port.

`qos apply` / `qos remove` provide surgical per-interface QoS override that takes precedence over service-managed QoS.

### 7.7 Filters and ACLs

Two-level design:
- **Filter** (template) — spec-level reusable rule definitions in network.json, managed via `filter` noun
- **ACL** (instance) — device-level CONFIG_DB entries, managed via `acl` noun

When a service is applied, filter templates are instantiated as ACLs scoped to the specific interface. Filter types (`ipv4` → `L3`, `ipv6` → `L3V6`) map to ACL table types.

## 8. Composite Mode and Topology Provisioning

### 8.1 Composite Mode

Composite mode generates a CONFIG_DB configuration offline (without connecting to a device), then delivers it atomically.

| Mode | Behavior | Use Case |
|------|----------|----------|
| **Overwrite** | Merge composite on top of CONFIG_DB (stale keys removed, factory defaults preserved) | Initial provisioning |
| **Merge** | Add entries to existing CONFIG_DB. Only for interface-level services with no existing binding. | Incremental deployment |

Delivery uses a Redis pipeline (MULTI/EXEC) for atomicity — either all changes apply or none do.

### 8.2 Topology Provisioning

Topology provisioning automates device configuration from a `topology.json` spec. Two modes:

| Mode | Method | Delivery | Use Case |
|------|--------|----------|----------|
| **Full device** | `ProvisionDevice` | CompositeOverwrite | Initial provisioning |
| **Per-interface** | `ProvisionInterface` | `ApplyService` | Add service to running device |

Full-device provisioning builds a CompositeConfig offline by creating an abstract Node and calling the same methods the CLI uses (`ConfigureBGP`, `SetupEVPN`, `iface.ApplyService`). The abstract Node accumulates entries in a shadow ConfigDB. The result is delivered to the device as a single atomic write.

### 8.3 Abstract Node

The Node operates in two modes with the same code path:

- **Physical mode** (`offline=false`): ConfigDB loaded from Redis at Connect/Lock time. ChangeSet applied to Redis.
- **Abstract mode** (`offline=true`): Shadow ConfigDB starts empty, operations build desired state. Entries accumulate for composite export via `BuildComposite()`.

This eliminates topology.go constructing CONFIG_DB entries inline — it calls the same primitives as the online path.

## 9. Device Connection

### 9.1 Connection Flow

When SSH credentials (`ssh_user`, `ssh_pass`) are present in the device profile, an SSH tunnel forwards a local random port to `127.0.0.1:6379` inside the device. Redis on SONiC listens only on localhost — SSH is the only path in.

```
┌────────────────────┐          ┌────────────┐            ┌────────────────┐
│                    │          │            │            │                │
│   ConfigDBClient   │          │            │            │   sshd (:22)   │
│ 127.0.0.1:<random> │          │ SSH Tunnel │            │ 127.0.0.1:6379 │
│                    │  local   │            │  forward   │    (Redis)     │
│                    │ ───────▶ │            │ ─────────▶ │                │
└────────────────────┘          └────────────┘            └────────────────┘
```

Four Redis clients are established: ConfigDB (DB 4), StateDB (DB 6), AppDB (DB 0), AsicDB (DB 1). StateDB/AppDB/AsicDB failures are non-fatal — the system can still read/write CONFIG_DB.

Without SSH credentials (integration tests), the address points directly at a standalone Redis container.

### 9.2 CONFIG_DB Cache

newtron maintains an in-memory snapshot of CONFIG_DB loaded at connection time. The cache enables precondition checks without per-check Redis round-trips.

**Episode model:** An episode is a time-boxed unit of work that reads the cache. Every episode starts with a fresh cache:

| Episode | How it refreshes |
|---------|-----------------|
| Write | `Lock()` refreshes after acquiring distributed lock |
| Read-only | `Refresh()` at the start |
| Initial | `Connect()` loads initial snapshot |

**Invariant:** *Every episode begins with a fresh CONFIG_DB snapshot. No episode relies on cache from a prior episode.*

**Limitation:** Between refresh and code that reads the cache, an external actor can modify CONFIG_DB. Precondition checks are advisory safety nets, not transactional guarantees. Acceptable because newtron is typically the sole CONFIG_DB writer in lab/production environments.

### 9.3 Config Persistence

SONiC uses a dual-state model: Redis CONFIG_DB (runtime, immediate) and `/etc/sonic/config_db.json` (persistent, loaded at boot). Newtron writes to Redis; `config save -y` persists to disk. This runs automatically after every `-x` execution unless `--no-save` is used.

## 10. Verification

### 10.1 Principle

**If a tool changes the state of an entity, that same tool must be able to verify the change had the intended effect.** Verification is the completion of provisioning, not a separate concern.

**For cross-device observations:** newtron returns structured data, not verdicts. If a check requires knowing what another device should have, it belongs in the orchestrator (newtrun).

### 10.2 Four Tiers

| Tier | What | Owner | Method | Failure Mode |
|------|------|-------|--------|-------------|
| **CONFIG_DB** | Redis entries match ChangeSet | newtron | `cs.Verify(n)` | Hard fail (assertion) |
| **APP_DB/ASIC_DB** | Routes installed by FRR/ASIC | newtron | `GetRoute()`, `GetRouteASIC()` | Observation (data) |
| **Operational state** | BGP sessions, interface health | newtron | `VerifyDeviceHealth()` | Observation (report) |
| **Cross-device** | Route propagation, ping | newtrun | Composes newtron primitives | Topology-dependent |

### 10.3 ChangeSet Verification

Every mutating operation produces a ChangeSet. `cs.Verify(n)` re-reads CONFIG_DB through a fresh connection and diffs against the ChangeSet. This works for all operations — disaggregated or composite — because they all produce ChangeSets. It is the only assertion newtron makes: checking its own writes.

The public API wraps this: `Node.Execute()` runs Lock → fn → Commit (which applies + verifies) → Save → Unlock. ChangeSet is never exposed outside `pkg/newtron/`.

### 10.4 Routing State Observation

- **`GetRoute(vrf, prefix)`** — reads APP_DB (DB 0). Returns `RouteEntry` with prefix, protocol, next-hops. Nil if not present.
- **`GetRouteASIC(vrf, prefix)`** — reads ASIC_DB (DB 1) via SAI object chain resolution. Confirms ASIC programming.

APP_DB shows what FRR computed. ASIC_DB shows what the hardware installed. The gap is orchagent processing. These are building blocks for orchestrators — newtron provides the read; newtrun knows what to expect.

### 10.5 Health Checks

`CheckBGPSessions()` verifies BGP neighbor states. `CheckInterfaceOper()` verifies interface oper-up status. `VerifyDeviceHealth()` composes both into a health report. These are local device observations.

## 11. Execution Modes

| Mode | Online? | Device Required? | Atomic? |
|------|---------|-----------------|---------|
| Dry-run (default) | Yes | Yes (reads CONFIG_DB) | N/A |
| Execute (`-x`) | Yes | Yes (reads + writes) | Per-entry |
| Composite generate | No | No | N/A |
| Composite deliver | Yes | Yes (writes) | Yes (pipeline) |
| ProvisionDevice | No (build) + Yes (deliver) | Yes (for delivery) | Yes (pipeline) |
| ProvisionInterface | Yes | Yes (reads + writes) | Per-entry |

## 12. Security

### 12.1 Transport

Redis on SONiC has no authentication and listens only on localhost. SSH is the transport security layer — all Redis access goes through an SSH tunnel with password credentials from the device profile. In integration tests, a standalone Redis container is used without SSH.

### 12.2 Permission Levels

Permission types are defined covering service operations, resource CRUD, spec authoring, and device cleanup (see the LLD for the full permission table). Read/view operations have no permission requirement.

**Current status:** Permission types exist in code but are not enforced at the HTTP layer. The server has no authentication middleware — it is designed for trusted-network deployment (localhost or VPN). Domain-level preconditions (VLAN exists, interface not already bound, etc.) are enforced. User-level authorization is planned for a future iteration.

### 12.3 Audit Logging

All operations logged with timestamp, user, device, operation name, changes made, success/failure, and execution mode.

## 13. Testing

| Tier | How | Purpose |
|------|-----|---------|
| Unit | `go test ./...` | Pure logic: IP derivation, spec parsing, ACL expansion |
| E2E | newtrun framework | Full stack: newtlab VMs, SSH tunnel, real SONiC |

E2E testing uses the newtrun framework (see `docs/newtrun/`).

## Appendix A: Glossary

### Core

| Term | Definition |
|------|------------|
| **Spec** | Declarative intent describing what you want. JSON files, version controlled. Never contains concrete device values. |
| **Config** | Imperative device state. Redis CONFIG_DB entries, generated at runtime from specs. |
| **Service** | Reusable template bundling VPN, filters, QoS. Applied to interfaces for consistent configuration. |
| **ChangeSet** | Collection of pending CONFIG_DB changes. Serves as the verification contract — `cs.Verify(n)` diffs against live CONFIG_DB. |

### Architecture

| Term | Definition |
|------|------------|
| **newtron-server** | Central HTTP server (`cmd/newtron-server`). Owns `NetworkActor` instances; device connections owned by `NodeActor` instances within each `NetworkActor`. |
| **NetworkActor** | Parent actor that owns a `*newtron.Network`, serializes spec operations, and creates/manages `NodeActor` instances. One per registered network. |
| **NodeActor** | Child actor (created by `NetworkActor`) that holds a `*Network` reference from its parent, serializes device operations, and caches `*newtron.Node` (SSH connection) with idle timeout. One per device. |
| **Connection Caching** | SSH connections reused across requests within idle timeout (default 5m). CONFIG_DB refreshed every request via `Lock()` (writes) or `Refresh()` (reads). Only the SSH tunnel persists. |

### Entities

| Term | Definition |
|------|------------|
| **Network** | Top-level object. Owns all specs, provides access to devices. |
| **Node** | Device handle. Holds parent *Network reference, ConfigDB cache, device profile. |
| **Interface** | Interface handle. Holds parent *Node reference and interface name. Point of service delivery. |
| **Platform** | Hardware type definition (HWSKU, port count, speeds). |

### VPN

| Term | Definition |
|------|------------|
| **IPVPN** | IP-VPN definition for L3 routing. Contains L3VNI and route targets. |
| **MACVPN** | MAC-VPN definition for L2 bridging. Contains VNI, VLAN ID, anycast IP/MAC, route targets. |
| **L2VNI** | Layer 2 VNI for VXLAN bridging. |
| **L3VNI** | Layer 3 VNI for VXLAN routing. |
| **VRF** | Virtual Routing and Forwarding instance. First-class CLI noun: owns interfaces, BGP neighbors, static routes, IP-VPN bindings. |
| **Route Target (RT)** | BGP extended community controlling VPN route import/export. |

### Redis Databases

| DB | Name | Purpose |
|----|------|---------|
| 0 | APP_DB | Routes installed by FRR/fpmsyncd (read via `GetRoute`) |
| 1 | ASIC_DB | SAI objects programmed by orchagent (read via `GetRouteASIC`) |
| 4 | CONFIG_DB | Device configuration (read/write) |
| 6 | STATE_DB | Operational state (read-only via health checks) |

### Operations

| Term | Definition |
|------|------------|
| **Dry-Run** | Preview mode (default). Shows what would change without applying. |
| **Execute (`-x`)** | Apply mode. Writes changes to CONFIG_DB. |
| **Save** | Persist runtime CONFIG_DB to `/etc/sonic/config_db.json` after execute. `--no-save` to skip. |
| **Device Lock** | Per-operation distributed lock in STATE_DB with TTL. Prevents concurrent modifications. |
| **frrcfgd** | SONiC's FRR management framework daemon. Translates CONFIG_DB BGP tables to FRR commands. |
| **Composite** | Offline CONFIG_DB configuration delivered atomically via Redis pipeline. |
| **Abstract Node** | Offline Node with shadow ConfigDB. Same code path as physical, different initialization. |
