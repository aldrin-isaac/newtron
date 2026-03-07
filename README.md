# newtron

> **Note:** newtron is a SONiC automation demonstrator — a research and learning project exploring Redis-first device management, declarative provisioning, and E2E verification against virtual SONiC topologies. It is not intended for production use.

<p align="center">
  <sub>Ron, the Newt</sub>
  <br/>
  <img src="newt.png" alt="Ron, the Newt — the newtron mascot" width="280"/>
</p>

SONiC's defining characteristic is that it stores everything in Redis. CONFIG_DB holds desired configuration. APP_DB holds computed routes. ASIC_DB holds what's programmed in hardware. STATE_DB holds operational telemetry. Daemons subscribe to table changes and react. The entire system is a set of Redis databases with subscriber processes — and newtron treats it as one.

newtron connects to CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB through a native Go Redis client tunneled over SSH. It reads typed table entries instead of parsing CLI output. It writes ordered CONFIG_DB mutations via Redis pipeline instead of running shell commands. It validates preconditions before every write and verifies the result by re-reading what it wrote. CLI commands are used only for operations Redis cannot express (like `config save`), and each is tagged for future elimination.

The result is a typed object hierarchy (`Network > Node > Interface`) that provisions SONiC devices from declarative specs, prevents invalid configurations structurally, and exposes every Redis database as structured data for any orchestrator to consume.

## System Overview

Five programs, two subsystems:

| Program | What it does |
|---------|-------------|
| **newtron-server** | HTTP API server. Loads specs, connects to SONiC devices via SSH/Redis, exposes all operations as REST endpoints. The brain. |
| **newtron** | CLI client. Human interface to newtron-server — every command is an HTTP call. |
| **newtlab** | VM orchestrator. Deploys QEMU virtual machines and wires them into topologies. |
| **newtlink** | Userspace bridge agent. Bridges Ethernet frames between VMs over TCP sockets. Deployed by newtlab. |
| **newtrun** | E2E test runner. Executes YAML test scenarios against newtron-server. |

```
                            ┌────────────────┐         ┌────────────┐
                            │                │         │            │
                            │    newtron     │         │ test suite │
                            │    (client)    │         │            │
                            │                │         │            │
                            └────────────────┘         └────────────┘
                              │                          │
                              │ HTTP                     │
                              ∨                          ∨
┌─────────┐                 ┌────────────────┐         ┌────────────┐
│         │                 │                │         │            │
│  specs  │                 │ newtron-server │  HTTP   │  newtrun   │
│         │ ──────────────> │                │ <────── │            │
└─────────┘                 └────────────────┘         └────────────┘
  │                           │
  │                           │ SSH+Redis
  ∨                           ∨
┌─────────┐                 ┌────────────────┐
│         │                 │                │
│ newtlab │  deploy, wire   │    SONiC VM    │
│         │ ──────────────> │                │
└─────────┘                 └────────────────┘
```

Both paths converge on the same SONiC devices. newtlab creates QEMU VMs running SONiC and wires them with newtlink; newtron-server connects to those same VMs via SSH-tunneled Redis. You can also point newtron-server at hardware switches or third-party labs — newtlab is only needed for local virtual topologies.

## Have 10 Minutes? See It Work

Requires Linux x86_64, Go 1.24+, KVM/QEMU, and ~2 GB disk for the SONiC image.

```bash
scripts/getting-started.sh
```

The script walks you through downloading the SONiC community VM image, building newtron, deploying a single-switch lab, and applying your first service — step by step, with explanations at each stage.

Or run the steps yourself:

```bash
# 1. Get the SONiC community image
mkdir -p ~/.newtlab/images
curl -fSL "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.img.gz" \
  | gunzip > ~/.newtlab/images/sonic-vs.qcow2

# 2. Build
make build

# 3. Deploy a single-switch lab
bin/newtlab deploy 1node --monitor  # live status during deploy

# 4. Start the server and apply a service
bin/newtron-server --spec-dir newtrun/topologies/1node/specs &
bin/newtron switch1 service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002
```

By default, newtron shows what it _would_ write to CONFIG_DB — every table, key, and field:

```
Operation: interface.applyService
Device: switch1
Changes:
  [ADD] INTERFACE|Ethernet0                              → map[NULL:NULL]
  [ADD] INTERFACE|Ethernet0|10.1.0.0/31                  → map[NULL:NULL]
  [ADD] BGP_NEIGHBOR|default|10.1.0.1                    → map[asn:65002 local_addr:10.1.0.0 admin_status:up]
  [ADD] BGP_NEIGHBOR_AF|default|10.1.0.1|ipv4_unicast    → map[admin_status:true]
  [ADD] NEWTRON_SERVICE_BINDING|Ethernet0                → map[service_name:transit ...]

DRY-RUN: No changes applied. Use -x to execute.
```

Every entry names a real CONFIG_DB table and key. `INTERFACE|Ethernet0` enables IP routing on the port. `BGP_NEIGHBOR|default|10.1.0.1` is exactly what `frrcfgd` subscribes to — it will configure FRR with a BGP peer at 10.1.0.1 in the default VRF. (`NULL:NULL` is SONiC's sentinel for entries with no fields, like IP bindings.)

There is no template engine — newtron computed these entries by running the same code path it uses online, using the device's AS number (65001), the interface's IP (10.1.0.0, so peer is 10.1.0.1), and the service spec.

Add `-x` to execute. newtron writes atomically, re-reads to verify, then persists:

```
$ bin/newtron switch1 service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002 -x

Changes applied successfully.
Verifying... OK (5/5 entries verified)
Config saved.
```

Tear down when done:

```bash
bin/newtlab destroy 1node
```

## Quick Start

### 1. Build

Requires Go 1.24+.

```bash
make build              # → bin/newtron, bin/newtron-server, bin/newtlab, bin/newtrun, bin/newtlink
```

### 2. Explore without VMs

Start the server with a shipped topology's specs and explore — no SONiC devices needed:

```bash
bin/newtron-server --spec-dir newtrun/topologies/2node/specs &

bin/newtron service list                    # List defined services
bin/newtron show switch1                    # Show device profile
bin/newtron switch1 provision               # Preview full composite (dry-run)
```

The same operations are available as HTTP endpoints:

```bash
curl localhost:8080/network/default/service                  # List services
curl localhost:8080/network/default/platform                 # List platforms
curl localhost:8080/network/default/topology/node            # List devices in topology
```

### 3. Deploy a lab (requires KVM + SONiC image)

Build a [SONiC VPP image](https://github.com/sonic-net/sonic-buildimage) and place it in `~/.newtlab/images/`:

```bash
mkdir -p ~/.newtlab/images
cp sonic-vpp.qcow2 ~/.newtlab/images/
cp alpine-testhost.qcow2 ~/.newtlab/images/    # lightweight test host (optional)
```

Deploy VMs and wire the topology:

```bash
bin/newtlab deploy 2node
bin/newtlab status                              # Check VM and link state
bin/newtlab ssh switch1                         # SSH into a switch
```

### 4. Provision and operate

With the server still running and VMs deployed:

```bash
bin/newtron switch1 provision -x                # Apply full composite config
bin/newtron switch1 health check                # Operational health check
bin/newtron switch1 bgp status                  # BGP neighbor table
bin/newtron switch1 service apply Ethernet4 transit --ip 10.1.0.0/31 --peer-as 65002 -x
```

Or via HTTP:

```bash
curl localhost:8080/network/default/node/switch1/health       # Health check
curl localhost:8080/network/default/node/switch1/interface     # List interfaces
```

### 5. Run the E2E test suite

```bash
bin/newtrun start --dir newtrun/suites/2node-primitive    # Full primitive test suite
```

### 6. Tear down

```bash
bin/newtlab destroy                             # Stop VMs, clean up
```

See the [newtlab HOWTO](docs/newtlab/howto.md) and [newtrun HOWTO](docs/newtrun/howto.md) for detailed guides.

## Architecture

Three architectural choices define newtron:

**Redis-first.** newtron talks to four Redis databases (DB 0, 1, 4, 6) through a single SSH-tunneled connection. CONFIG_DB reads return typed Go structs (`BGPNeighborEntry`, `VLANEntry`, `VRFEntry`) — not CLI text to parse. CONFIG_DB writes are atomic Redis pipeline transactions — not sequential `config` commands that can leave partial state. Verification re-reads through a fresh Redis connection and diffs field-by-field. The 42-entry CONFIG_DB parser registry and 13-entry STATE_DB parser registry mean every table has typed access.

**A domain model, not a connection wrapper.** `Network > Node > Interface` is an object hierarchy where each level holds the context for its operations. An Interface knows its name, its parent Node's AS number, and its Network's service specs. `Interface.ApplyService("transit")` resolves everything internally and produces a ChangeSet. No external function orchestrates this — the object has the full context. The same hierarchy works offline: `node.NewAbstract()` creates a shadow Node where operations accumulate entries against an in-memory CONFIG_DB. `BuildComposite()` exports them for atomic delivery. One code path for live operations and offline provisioning.

**Built-in referential integrity.** CONFIG_DB has no constraints — you can write a VLAN member for a nonexistent VLAN, a BGP neighbor in a nonexistent VRF, a service on a LAG member. SONiC daemons silently fail or produce undefined behavior. newtron validates preconditions before every write: interface exists and is not a LAG member, VRF exists, VTEP is configured for EVPN, no conflicting service binding. The tool structurally cannot produce invalid CONFIG_DB state.

## What newtron Configures

A device's full configuration — interfaces, VLANs, VRFs, BGP, EVPN overlays, ACLs, QoS, route policies — is expressed as spec files and translated into CONFIG_DB entries. Spec files describe intent; newtron resolves them using each device's profile (loopback IP, AS number, EVPN peers), platform (port count, HWSKU), and zone.

**Six service types** bind to interfaces:

| Type | What it creates | VPN reference |
|------|----------------|---------------|
| `routed` | BGP neighbor + IP (optionally in a per-interface VRF) | — |
| `bridged` | VLAN + VLAN member | MAC-VPN (local) |
| `irb` | VLAN + SVI + IP (optionally in a VRF) | MAC-VPN (local) |
| `evpn-bridged` | VLAN + VNI mapping + ARP suppression | MAC-VPN (EVPN) |
| `evpn-irb` | VLAN + VNI + SVI + anycast GW + L3VNI + VRF | IP-VPN + MAC-VPN |
| `evpn-routed` | VRF + L3VNI + BGP neighbor | IP-VPN |

VPN specs define the overlay parameters:

```json
{
  "ipvpns": {
    "irb-vrf": { "vrf": "Vrf_irb", "l3vni": 50400, "l3vni_vlan": 3998,
                 "route_targets": ["65000:50400"] }
  },
  "macvpns": {
    "extend-vlan300": { "vlan_id": 300, "vni": 10300,
                        "route_targets": ["65000:300"], "arp_suppression": true }
  }
}
```

**Composite provisioning** generates a device's full config offline using the same operations the CLI uses. `ConfigureBGP`, `SetupEVPN`, `iface.ApplyService` all run against a shadow CONFIG_DB. `BuildComposite()` exports the accumulated entries. Delivery via `ReplaceAll` merges composite entries on top of existing CONFIG_DB, preserving factory defaults while removing stale keys — all through a single Redis pipeline transaction. No template engine, no config drift between online and offline paths.

Every mutating operation produces a **ChangeSet** — an ordered list of CONFIG_DB mutations. The ChangeSet serves triple duty: dry-run preview, execution receipt, and verification contract. All commands default to dry-run. Add `-x` to execute.

## Verification

newtron distinguishes between asserting its own work and observing device state:

| Tier | Question | What newtron does |
|------|----------|-------------------|
| CONFIG_DB | Did my writes land? | **Asserts** — re-reads and diffs against ChangeSet |
| APP_DB | Is the route present? | **Observes** — returns `RouteEntry` with ECMP nexthops |
| ASIC_DB | Is it programmed in hardware? | **Observes** — resolves full SAI OID chain |
| STATE_DB | Is the device healthy? | **Observes** — returns structured health report |
| Cross-device | Did the route propagate? | **Not newtron's job** — orchestrators compose observations |

A route query illustrates the principle. Ask newtron whether spine1 has a BGP route to 10.20.1.0/31:

```
GET /network/default/node/spine1/route/default/10.20.1.0%2F31

{"prefix":"10.20.1.0/31", "vrf":"default", "protocol":"bgp",
 "next_hops":[{"address":"10.0.0.2"}], "source":"APP_DB"}
```

newtron returns the route entry — prefix, VRF, protocol, next-hops, and which database answered. It does not return pass/fail. An orchestrator that needs "spine1 has a BGP route to 10.20.1.0/31 via 10.0.0.2" composes that assertion from the data. A different orchestrator with different correctness criteria uses the same observation. The mechanism is reusable; the judgment is the caller's.

## Testing Infrastructure

Proving newtron works requires running it against real SONiC software. Two supporting tools provide that infrastructure.

### newtlab — VM Topology Orchestration

newtlab deploys QEMU virtual machines and wires them into topologies using **userspace networking** — no root, no Linux bridges, no kernel namespaces, no Docker. Every packet between VMs passes through newtlink, a Go bridge process that handles each Ethernet frame in userspace.

This is a deliberate alternative to kernel-level wiring (veth pairs, Linux bridges, tc rules). A userspace bridge knows exactly how many bytes crossed each link, because it handles every frame. Rate monitoring, tap-to-wireshark, fault injection — all are straightforward extensions of the bridge loop. Kernel networking is powerful but opaque; when a link breaks, you debug iptables rules and bridge state. When a newtlink bridge has a problem, you look at one process.

- **Userspace socket links** — each inter-VM link is a bridge worker (a goroutine in the newtlink process) that listens on two TCP ports and bridges Ethernet frames between them. Both VMs connect outbound to newtlink — no startup ordering, no listen/connect asymmetry.
- **Per-link telemetry** — bridge workers track byte counters and session state per link. `newtlab bridge-stats` aggregates counters from all hosts into a single table.
- **Multi-host deployment** — topologies span multiple servers. Cross-host links work identically to local links: the local endpoint connects to `127.0.0.1`, the remote endpoint connects across the network. Define a server pool in `topology.json` with capacity constraints; newtlab auto-places VMs using a spread algorithm.
- **No privileged access** — no root, no sudo, no kernel modules. VMs need KVM for performance; the interconnect is pure userspace.
- **Port conflict detection** — before starting any process, newtlab probes all allocated ports across all hosts and reports every conflict in a single error.
- **Platform boot patches** — different SONiC images have platform-specific initialization quirks. A declarative patch framework (JSON descriptors + Go templates) handles post-boot fixups without Go code changes.
- **Multiple SONiC images** — platform definitions in `platforms.json` declare NIC driver, interface mapping, CPU features, and credentials per platform. Test topologies use VPP and CiscoVS (Cisco Silicon One); the framework handles any SONiC image.

### newtrun — E2E Test Framework

newtrun is a YAML-based test framework. Each **scenario** is a sequence of
**steps** drawn from a vocabulary of actions — provisioning, CONFIG_DB/STATE_DB
verification, service lifecycle, BGP, EVPN, VLANs, VRFs, ACLs, QoS, static
routing, host commands, and more. newtrun deploys a topology, provisions
devices, executes steps in order, and reports pass/fail with JUnit XML output.

Scenarios declare dependencies (`requires:`), so suites run in correct order
with automatic skip propagation on failure. Users write their own scenarios
and suites — the shipped suites are examples of the framework, not the
framework itself.

```yaml
name: vrf-lifecycle
description: Create VRF, add static route, verify, tear down
topology: 2node
steps:
  - name: create-vrf
    action: create-vrf
    devices: [switch2]
    vrf: Vrf_local

  - name: add-static-route
    action: add-static-route
    devices: [switch2]
    vrf: Vrf_local
    prefix: "10.99.0.0/24"
    params:
      next_hop: "10.20.1.0"

  - name: verify-route
    action: verify-config-db
    devices: [switch2]
    table: STATIC_ROUTE
    key: "Vrf_local|10.99.0.0/24"
    expect:
      fields:
        nexthop: "10.20.1.0"

  - name: delete-vrf
    action: delete-vrf
    devices: [switch2]
    vrf: Vrf_local
```

Use `newtrun actions` for the full action reference, or `newtrun actions <name>`
for details on any specific action.

```
$ newtrun start --dir newtrun/suites/2node-service

newtrun: 6 scenarios, topology: 2node-service, platform: ciscovs

  [1/6]  boot-ssh ...............  PASS  (3s)
  [2/6]  provision ..............  PASS  (1m47s)
  [3/6]  verify-health ..........  PASS  (12s)
  [4/6]  dataplane ..............  PASS  (45s)
  [5/6]  deprovision ............  PASS  (18s)
  [6/6]  verify-clean ...........  PASS  (8s)

newtrun: 6 scenarios: 6 passed  (2m38s)
```

### Validated

All shipped test suites pass on Cisco Silicon One (CiscoVS Palladium2):

| Suite | What it tests |
|-------|---------------|
| 2node-primitive | Disaggregated operations: VLAN/VRF/VTEP lifecycle, service apply/remove, BGP, LAGs, ACLs, QoS, static routing |
| 2node-service | Full service lifecycle: provision → health → dataplane → deprovision → verify-clean |
| 3node-dataplane | Spine-leaf fabric: L3 routing, EVPN L2 bridging, EVPN asymmetric IRB (inter-subnet routing via VXLAN) |

EVPN VXLAN verified end-to-end: L2 bridging across switches and inter-subnet
routing via asymmetric IRB, both running on Cisco Silicon One SAI.

Every platform bug encountered along the way is documented in [`docs/rca/`](docs/rca/) — 39 root-cause analyses covering frrcfgd, orchagent, SAI, and CiscoVS/VPP quirks. When SONiC does something unexpected, the answer is probably already there.

## Specs

Spec files describe network intent as declarative constraints. newtron resolves them into device-specific CONFIG_DB entries using each device's profile, platform, and zone. The same spec applied to different devices produces different config. The same spec applied twice to the same device produces identical config.

```
specs/
├── network.json        # Services, VPNs, filters, routing policy, QoS   ← newtron
├── platforms.json      # Platform capabilities, VM defaults              ← newtron + newtlab
└── profiles/           # Per-device: loopback IP, ASN, zone, EVPN peers ← newtron + newtlab
    ├── spine1.json
    └── leaf1.json
```

Specs resolve hierarchically: network → zone → node (lower-level wins). A zone can override a network-level service; a node can override a zone-level filter. Platforms are global-only.

## Repository Layout

```
cmd/
  newtron/          Device provisioning and verification CLI
  newtron-server/   HTTP API server (transport layer over pkg/newtron)
  newtlab/          VM orchestration CLI
  newtrun/          E2E test runner CLI
  newtlink/         Bridge traffic agent (deployed to remote hosts by newtlab)

pkg/
  newtron/
    *.go            Public API: Network, Node, Interface, types
    api/            HTTP server: actors, handlers, middleware
    device/sonic/   SSH tunnels, Redis DB 0/1/4/6, 42 CONFIG_DB parsers, locking
    network/        Network type, topology graph, spec resolution
      node/         Node + Interface types, all operations, composite provisioning
    settings/       Settings resolution (flag > env > file)
    spec/           Spec types and loader
  newtlab/          QEMU, multi-host placement, socket bridges, boot patches
  newtrun/          Scenario parser, dependency ordering, step executors, reporting
  cli/              Shared CLI formatting (tables, colors, progress)
  util/             Errors, logging, IP/string helpers

newtrun/
  topologies/       Test topologies (1node, 2node, 2node-service, 3node, 4node)
  suites/           Test suites and scenarios (YAML)

docs/
  newtron/          HLD, LLD, device LLD, API reference, HOWTO
  newtlab/          HLD, LLD, HOWTO
  newtrun/          HLD, LLD, HOWTO
  rca/              39 root-cause analyses of SONiC platform bugs and workarounds
```

## Documentation

[Design Principles](docs/DESIGN_PRINCIPLES.md) explains the philosophy — program boundaries, object model, verification, spec-vs-config. Read it first.

| | HLD | LLD | HOWTO |
|-|-----|-----|-------|
| **newtron** | [Architecture](docs/newtron/hld.md) | [Types & Methods](docs/newtron/lld.md) | [Usage](docs/newtron/howto.md) |
| **newtlab** | [Architecture](docs/newtlab/hld.md) | [Types & Methods](docs/newtlab/lld.md) | [Usage](docs/newtlab/howto.md) |
| **newtrun** | [Architecture](docs/newtrun/hld.md) | [Types & Methods](docs/newtrun/lld.md) | [Usage](docs/newtrun/howto.md) |

Additional references: [Device Layer LLD](docs/newtron/device-lld.md) (SSH tunnels, Redis clients, CONFIG_DB types) · [API Reference](docs/newtron/api.md) (123 HTTP endpoints) · [RCA Index](docs/rca/) (39 SONiC platform analyses — frrcfgd, orchagent, SAI, CiscoVS/VPP)

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```
