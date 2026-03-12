# newtron

> **Note:** newtron is a research project — an opinionated network
> architecture for SONiC, validated against virtual topologies. It is not
> intended for production use.

<p align="center">
  <sub>Ron, the Newt</sub>
  <br/>
  <img src="newt.png" alt="Ron, the Newt — the newtron mascot" width="280"/>
</p>

newtron is opinionated network architecture for SONiC, delivered as
software.

Spec files define the architecture: services describe what an interface
does (transit peering, L2 bridging, IRB, EVPN overlay), VPN specs define
overlay parameters, route policies and filters control path selection.
Device profiles identify each switch — AS number, loopback IP, platform,
EVPN peers. When you apply a service to an interface, newtron resolves
the spec against the device's profile and writes the resulting CONFIG_DB
entries through SONiC's native Redis interface — validated against YANG
constraints, applied atomically, and verified by re-reading every entry.

The architecture is self-enforcing. Every CONFIG_DB write passes schema
validation — invalid values and unknown fields are rejected before
reaching the device. Every forward operation has a symmetric reverse —
apply and remove, create and delete — with service bindings on the device
that guarantee clean teardown even if specs change between apply and
remove. Online operations and offline provisioning run the same code
path, so there is no drift between what you preview and what gets
applied. These aren't optional safety features bolted onto an automation
tool. They are how the architecture maintains its own integrity.

## The Architecture

Six service types bind to interfaces — the interface is the point of
service delivery, the unit of lifecycle, and the unit of isolation:

| Type | What it creates | Overlay |
|------|----------------|---------|
| `routed` | BGP neighbor + IP (optionally in a per-interface VRF) | — |
| `bridged` | VLAN + VLAN member | MAC-VPN (local) |
| `irb` | VLAN + SVI + IP (optionally in a VRF) | MAC-VPN (local) |
| `evpn-bridged` | VLAN + VNI mapping + ARP suppression | MAC-VPN (EVPN) |
| `evpn-irb` | VLAN + VNI + SVI + anycast GW + L3VNI + VRF | IP-VPN + MAC-VPN |
| `evpn-routed` | VRF + L3VNI + BGP neighbor | IP-VPN |

Spec files declare network-level intent — services, VPN parameters, route
policies, filters, QoS — without reference to any specific device. newtron
resolves them using each device's profile (loopback IP, AS number, EVPN
peers), platform (port count, HWSKU), and zone. The same spec applied to
different devices produces different CONFIG_DB entries. The same spec
applied twice to the same device produces identical entries.

```
specs/
├── network.json        # Services, VPNs, filters, routing policy, QoS
├── platforms.json      # Platform capabilities, VM defaults
└── profiles/           # Per-device: loopback IP, ASN, zone, EVPN peers
    ├── spine1.json
    └── leaf1.json
```

Specs resolve hierarchically: network → zone → node (lower-level wins).

VPN specs define overlay parameters:

```json
{
  "ipvpns": {
    "irb-vrf": { "vrf": "Vrf_IRB", "l3vni": 50400, "l3vni_vlan": 3998,
                 "route_targets": ["65000:50400"] }
  },
  "macvpns": {
    "extend-vlan300": { "vlan_id": 300, "vni": 10300,
                        "route_targets": ["65000:300"], "arp_suppression": true }
  }
}
```

Every mutating operation produces a **ChangeSet** — an ordered list of
CONFIG_DB mutations that serves as dry-run preview, execution receipt, and
verification contract. All commands default to dry-run. Add `-x` to execute.

## Have 10 Minutes? See It Work

Requires Linux x86_64, Go 1.24+, KVM/QEMU, and ~2 GB disk for the SONiC image.

```bash
scripts/getting-started.sh
```

The script walks you through downloading the SONiC community VM image,
building newtron, deploying a single-switch lab, and applying your first
service — step by step, with explanations at each stage.

Or run the steps yourself:

```bash
# 1. Get the SONiC community image
mkdir -p ~/.newtlab/images
curl -fSL "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.img.gz" \
  | gunzip > ~/.newtlab/images/sonic-vs.qcow2

# 2. Build
make build

# 3. Deploy a single-switch lab
bin/newtlab deploy 1node-vs --monitor  # live status during deploy

# 4. Start the server, initialize the device, and apply a service
bin/newtron-server --spec-dir newtrun/topologies/1node-vs/specs &
bin/newtron switch1 init
bin/newtron switch1 service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002
```

By default, newtron shows what it _would_ write to CONFIG_DB — every table, key, and field:

```
Operation: interface.apply-service
Device: switch1
Changes to CONFIG_DB:
  [UPD] DEVICE_METADATA|localhost                        → map[bgp_asn:65001 type:LeafRouter]
  [ADD] BGP_GLOBALS|default                              → map[local_asn:65001 router_id:10.0.0.1 ...]
  [ADD] BGP_GLOBALS_AF|default|ipv4_unicast              → map[]
  [ADD] ROUTE_REDISTRIBUTE|default|connected|bgp|ipv4    → map[]
  [ADD] INTERFACE|Ethernet0                              → map[NULL:NULL]
  [ADD] INTERFACE|Ethernet0|10.1.0.0/31                  → map[NULL:NULL]
  [ADD] BGP_PEER_GROUP|default|TRANSIT                   → map[admin_status:true]
  [ADD] BGP_PEER_GROUP_AF|default|TRANSIT|ipv4_unicast   → map[]
  [ADD] BGP_NEIGHBOR|default|10.1.0.1                    → map[asn:65002 local_addr:10.1.0.0 admin_status:up peer_group_name:TRANSIT]
  [ADD] BGP_NEIGHBOR_AF|default|10.1.0.1|ipv4_unicast    → map[admin_status:true]
  [ADD] NEWTRON_SERVICE_BINDING|Ethernet0                → map[service_name:transit ...]

DRY-RUN: No changes applied. Use -x to execute.
```

These aren't templates rendered from Jinja. newtron computed them by running
its operations against the device's profile — AS 65001, loopback 10.0.0.1,
the transit service spec, and the /31 address you provided. The same code
path runs online against a live device or offline for composite provisioning.

Add `-x` to execute. newtron writes atomically, re-reads to verify, then persists:

```
$ bin/newtron switch1 service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002 -x

Changes applied successfully.
Verifying... OK (11/11 entries verified)
Config saved.
```

Tear down when done:

```bash
bin/newtlab destroy 1node-vs
```

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

Both paths converge on the same SONiC devices. newtlab creates QEMU VMs
running SONiC and wires them with newtlink; newtron-server connects to
those same VMs via SSH-tunneled Redis. You can also point newtron-server at
hardware switches or third-party labs — newtlab is only needed for local
virtual topologies.

## Quick Start

### 1. Build

Requires Go 1.24+.

```bash
make build              # → bin/newtron, bin/newtron-server, bin/newtlab, bin/newtrun, bin/newtlink
```

### 2. Explore without VMs

Start the server with a shipped topology's specs and explore — no SONiC devices needed:

```bash
bin/newtron-server --spec-dir newtrun/topologies/2node-vs/specs &

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

Place a SONiC image in `~/.newtlab/images/`. The getting-started script
downloads the community sonic-vs image automatically; for multi-switch
topologies, build a [SONiC image](https://github.com/sonic-net/sonic-buildimage)
with a dataplane-capable platform (e.g., Cisco Silicon One):

```bash
mkdir -p ~/.newtlab/images
cp sonic-vs.qcow2 ~/.newtlab/images/            # community image (L2/L3, no EVPN dataplane)
cp alpine-testhost.qcow2 ~/.newtlab/images/      # lightweight test host (optional)
```

Deploy VMs and wire the topology:

```bash
bin/newtlab deploy 2node-vs
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
bin/newtrun start --dir newtrun/suites/2node-vs-primitive    # Full primitive test suite
```

### 6. Tear down

```bash
bin/newtlab destroy                             # Stop VMs, clean up
```

See the [newtlab HOWTO](docs/newtlab/howto.md) and [newtrun HOWTO](docs/newtrun/howto.md) for detailed guides.

## Verification

newtron distinguishes between asserting its own work and observing device state:

| Tier | Question | What newtron does |
|------|----------|-------------------|
| CONFIG_DB | Did my writes land? | **Asserts** — re-reads and diffs against ChangeSet |
| APP_DB | Is the route present? | **Observes** — returns `RouteEntry` with ECMP nexthops |
| ASIC_DB | Is it programmed in hardware? | **Observes** — resolves full SAI OID chain |
| STATE_DB | Is the device healthy? | **Observes** — returns structured health report |
| Cross-device | Did the route propagate? | **Not newtron's job** — orchestrators compose observations |

newtron returns structured data — a `RouteEntry`, a health report — not
pass/fail verdicts. The one exception is ChangeSet verification: newtron
asserts that its own writes landed correctly. Everything else is observation
that callers interpret according to their own correctness criteria.

## Testing Infrastructure

Proving the architecture works requires running it against real SONiC
software. Two supporting tools provide that infrastructure.

### newtlab — VM Topology Orchestration

newtlab deploys QEMU virtual machines and wires them into topologies using
**userspace networking** — no root, no Linux bridges, no kernel namespaces,
no Docker. Every packet between VMs passes through newtlink, a Go bridge
process that handles each Ethernet frame in userspace.

- **Userspace socket links** — each inter-VM link is a bridge worker that
  listens on two TCP ports and bridges Ethernet frames between them.
- **Per-link telemetry** — bridge workers track byte counters and session
  state per link.
- **Multi-host deployment** — topologies span multiple servers. Define a
  server pool in `topology.json` with capacity constraints; newtlab
  auto-places VMs using a spread algorithm.
- **No privileged access** — no root, no sudo, no kernel modules.
- **Auto port allocation** — newtlab probes ports at allocation time and
  automatically resolves conflicts, allowing multiple topologies to
  coexist on the same host.
- **Platform boot patches** — a declarative patch framework handles
  platform-specific SONiC initialization without Go code changes.

### newtrun — E2E Test Framework

newtrun executes YAML test scenarios against newtron-server. Each scenario
is a sequence of steps drawn from a vocabulary of actions — provisioning,
CONFIG_DB/STATE_DB verification, service lifecycle, BGP, EVPN, VLANs, VRFs,
ACLs, QoS, static routing, host commands, and more.

```yaml
name: vrf-lifecycle
description: Create VRF, add static route, verify, tear down
topology: 2node-vs
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

```
$ newtrun start --dir newtrun/suites/2node-vs-service

newtrun: 6 scenarios, topology: 2node-vs-service, platform: sonic-vs

  [1/6]  boot-ssh ...............  PASS  (3s)
  [2/6]  provision ..............  PASS  (1m47s)
  [3/6]  verify-health ..........  PASS  (12s)
  [4/6]  dataplane ..............  PASS  (45s)
  [5/6]  deprovision ............  PASS  (18s)
  [6/6]  verify-clean ...........  PASS  (8s)

newtrun: 6 scenarios: 6 passed  (2m38s)
```

### Validated

All shipped test suites pass on community sonic-vs and Cisco Silicon One (CiscoVS Palladium2):

| Suite | Platform | What it tests |
|-------|----------|---------------|
| 2node-vs-primitive | sonic-vs | Disaggregated operations: VLAN/VRF lifecycle, service apply/remove, BGP, LAGs, ACLs, QoS, static routing |
| 2node-vs-service | sonic-vs | Full service lifecycle: provision → health → dataplane → deprovision → verify-clean |
| 2node-ngdp-primitive | CiscoVS | Same as vs-primitive, plus EVPN VTEP lifecycle |
| 2node-ngdp-service | CiscoVS | Same as vs-service, with EVPN overlay services |
| 3node-ngdp-dataplane | CiscoVS | Spine-leaf fabric: L3 routing, EVPN L2 bridging, EVPN asymmetric IRB (inter-subnet routing via VXLAN) |

EVPN VXLAN verified end-to-end on CiscoVS: L2 bridging across switches and
inter-subnet routing via asymmetric IRB, both running on Cisco Silicon One SAI.
The vs suites run on the free community sonic-vs image — no proprietary platform needed.

Every platform bug encountered along the way is documented in
[`docs/rca/`](docs/rca/) — root-cause analyses covering frrcfgd, orchagent,
SAI, and CiscoVS/VPP quirks. When SONiC does something unexpected, the
answer is probably already there.

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
  topologies/       Test topologies (1node-vs, 2node-vs, 2node-vs-service, 2node-ngdp, 2node-ngdp-service, 3node-ngdp, 4node-ngdp)
  suites/           Test suites and scenarios (YAML)

docs/
  newtron/          HLD, LLD, device LLD, API reference, HOWTO
  newtlab/          HLD, LLD, HOWTO
  newtrun/          HLD, LLD, HOWTO
  rca/              Root-cause analyses of SONiC platform bugs and workarounds
```

## Documentation

[Design Principles](docs/DESIGN_PRINCIPLES.md) explains the philosophy — program boundaries, object model, verification, spec-vs-config. Read it first.

| | HLD | LLD | HOWTO |
|-|-----|-----|-------|
| **newtron** | [Architecture](docs/newtron/hld.md) | [Types & Methods](docs/newtron/lld.md) | [Usage](docs/newtron/howto.md) |
| **newtlab** | [Architecture](docs/newtlab/hld.md) | [Types & Methods](docs/newtlab/lld.md) | [Usage](docs/newtlab/howto.md) |
| **newtrun** | [Architecture](docs/newtrun/hld.md) | [Types & Methods](docs/newtrun/lld.md) | [Usage](docs/newtrun/howto.md) |

Additional references: [Device Layer LLD](docs/newtron/device-lld.md) (SSH tunnels, Redis clients, CONFIG_DB types) · [API Reference](docs/newtron/api.md) · [RCA Index](docs/rca/) (SONiC platform analyses — frrcfgd, orchagent, SAI, CiscoVS/VPP)

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```
