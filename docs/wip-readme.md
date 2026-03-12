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

newtron makes specific choices about how a SONiC network should be built.
These aren't configuration options — they are the architecture.

**The interface is the point of service.** Every service — transit peering,
L2 bridging, EVPN overlay — binds to an interface. The interface is where
abstract intent meets physical infrastructure. It is the unit of lifecycle
(apply, remove, refresh), the unit of state (one service binding or none),
and the unit of isolation (services on different interfaces are independent).
You don't configure a device globally and hope the right things land on the
right ports. You apply a service to an interface and get exactly the CONFIG_DB
entries that interface needs.

**All-eBGP, everywhere.** The underlay is hop-by-hop eBGP between directly
connected interfaces. The overlay is loopback-to-loopback eBGP between EVPN
peers. No iBGP, no route reflectors, no full-mesh scaling problems. Each
switch has its own AS number. This is a deliberate simplification — eBGP
is the only peering model, which means every BGP session has the same
operational characteristics regardless of whether it carries IPv4 prefixes
or EVPN routes.

**Specs are network-scoped; execution is device-scoped.** Service specs,
VPN parameters, route policies, and filters are defined once at the network
level — they describe what an interface should do, not how any particular
switch should be configured. When you apply a service, newtron resolves
the spec against the device's profile (AS number, loopback IP, EVPN peers)
to produce device-specific CONFIG_DB entries. The same spec applied to
different devices produces different entries. The same spec applied twice to
the same device produces identical entries.

```
specs/
├── network.json        # Services, VPNs, filters, routing policy, QoS
├── platforms.json      # Platform capabilities, VM defaults
└── profiles/           # Per-device: loopback IP, ASN, zone, EVPN peers
    ├── spine1.json
    └── leaf1.json
```

**The device is the source of truth.** Spec files are intent. Once
configuration is applied, the device's CONFIG_DB is what matters. If someone
edits CONFIG_DB directly — via CLI, Redis, or another tool — that is the
new reality. newtron reads device state before every operation, and mutates
what it finds. It does not try to reconcile the device back to spec. This
is not Terraform; there is no desired-state diff. There is the device, and
there is the change you are asking for.

**Content-hashed policy objects.** Shared resources like ACL tables, route
maps, and prefix sets are named with an 8-character hash of their content.
If the spec hasn't changed, the hash hasn't changed, and a refresh is a
no-op. If the spec changes, the new version gets a new name — both exist
simultaneously while interfaces migrate one by one. No coordinated
switchover, no ordering dependencies, no window where half the interfaces
have the old policy and half have the new one.

**Operational symmetry.** Every forward operation has a reverse. Apply and
remove. Create and delete. Bind and unbind. Service bindings stored on the
device record exactly what was applied, so removal can always reconstruct
the teardown — even if the spec has changed since the service was applied.
This is not a nice-to-have. Without it, CONFIG_DB entries accumulate with
no way to clean them up, and the device drifts from anything anyone intended.

**One code path.** Online operations against a live device and offline
composite provisioning run the same code. An abstract node starts with an
empty shadow CONFIG_DB and accumulates entries as you call the same methods
used by the CLI. The result is a composite that can be delivered to a device
in one atomic operation. There is no template engine, no separate
provisioning pipeline — the operations *are* the provisioning.

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

Every mutating operation produces a **ChangeSet** — an ordered list of
CONFIG_DB mutations. The ChangeSet is the dry-run preview (what will
change), the execution receipt (what did change), and the verification
contract (what to check). After execution, newtron re-reads every entry it
wrote and diffs against the ChangeSet. If anything is missing or wrong,
you know immediately.

Beyond its own writes, newtron observes but does not judge. It reads
APP_DB routes, resolves ASIC_DB SAI chains, and returns structured health
reports from STATE_DB — but these are data, not verdicts. Cross-device
assertions (did the route propagate? is the fabric converged?) belong to
the test orchestrator, not to the device tool. newtron gives you the
observations; you decide what they mean.

## Testing Infrastructure

Proving the architecture works requires running it against real SONiC
software.

**newtlab** deploys QEMU virtual machines and wires them into topologies
using userspace networking — no root, no Linux bridges, no Docker. Every
packet between VMs passes through newtlink, a Go bridge that handles
Ethernet frames in userspace. Topologies can span multiple servers.

**newtrun** executes YAML test scenarios against newtron-server — each
scenario is a sequence of steps (provision, verify CONFIG_DB, apply service,
check BGP, ping across VMs, tear down) that exercise the architecture
end-to-end.

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

All shipped test suites pass on community sonic-vs and Cisco Silicon One
(CiscoVS Palladium2):

| Suite | Platform | What it tests |
|-------|----------|---------------|
| 2node-vs-primitive | sonic-vs | Disaggregated operations: VLAN/VRF lifecycle, service apply/remove, BGP, LAGs, ACLs, QoS, static routing |
| 2node-vs-service | sonic-vs | Full service lifecycle: provision → health → dataplane → deprovision → verify-clean |
| 2node-ngdp-primitive | CiscoVS | Same as vs-primitive, plus EVPN VTEP lifecycle |
| 2node-ngdp-service | CiscoVS | Same as vs-service, with EVPN overlay services |
| 3node-ngdp-dataplane | CiscoVS | Spine-leaf fabric: L3 routing, EVPN L2 bridging, EVPN asymmetric IRB |

The vs suites run on the free community sonic-vs image — no proprietary
platform needed. EVPN VXLAN is verified end-to-end on CiscoVS with Cisco
Silicon One SAI.

Every platform bug encountered along the way is documented in
[`docs/rca/`](docs/rca/) — 40+ root-cause analyses covering frrcfgd,
orchagent, SAI, and platform quirks.

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
