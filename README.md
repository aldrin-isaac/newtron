# newtron

> **Note:** **newtron** is a personal project that demonstrates the
> architectural ideas in [DESIGN_PRINCIPLES.md](docs/DESIGN_PRINCIPLES.md)
> by building software that lives by them, validated against virtual
> topologies. It is not trying to be a complete network automation
> solution — features exist where they exercise the principles. It is
> not intended for production use.
>
> **Provenance & rights.** **newtron** is a personal project by Aldrin
> Isaac, built on personal time with personal equipment and personal
> tools. The ideas it explores were accumulated and put to practice long
> before my current role and are unrelated to my employer's business.
> Copyright © 2025-2026 Aldrin Isaac. All rights reserved — no license
> is granted for use, copy, modification, or redistribution. See
> [NOTICE](NOTICE) for details.

## Origin

At the start of 2026, retinal detachment surgery put me on the bench
— and on my right side, fifty minutes of every hour, on doctor's
orders. Weeks of no exertion meant I was useless around the house,
with evenings and weekends to fill. So I started a side project —
partly to dust off network-automation muscles I hadn't flexed in a
while, partly to learn what AI-driven development actually feels like
by building something real.

The output is [DESIGN_PRINCIPLES.md](docs/DESIGN_PRINCIPLES.md) —
opinionated network automation, distilled from a career of doing this
work and applied, fresh, to SONiC.

If you've ever wrestled with the gap between what your automation
thinks the network is and what it actually is, this might resonate.
There's more than one way to bake a cake — this is my recipe.

A happy side effect: the proof-of-concept code is also a low-friction
way to kick the tires with SONiC.

<p align="center">
  <sub>Ron, the Newt</sub>
  <br/>
  <img src="newt.png" alt="Ron, the Newt — the newtron mascot" width="280"/>
</p>

**newtron** explores safe, opinionated network automation for SONiC.
Every unit of configuration gets one pattern — validated before writing,
applied atomically, verified after, and reversible by design. **newtron**
is also about making SONiC accessible to more people.

Every piece of SONiC configuration — a VLAN, a BGP session, a service
binding, an ACL — can be configured many ways. **newtron** offers one
pattern for each. The pattern is the opinion. What you build from those
patterns — the topology, the overlays, the scale — is your design.

**newtron** doesn't just generate configuration — it delivers each
primitive safely. Every CONFIG_DB entry is validated against SONiC's
YANG schema before it reaches the device — invalid values never land.
Entries are applied atomically — partial state never accumulates.
Every write is verified by re-reading what was written — silent
failures don't go unnoticed. Each operation records what it did so
the reverse can undo it cleanly — even if the spec has changed or
other operations have modified the device since.

## Have 10 Minutes? See It Work

Requires Linux x86_64, Go 1.24+, KVM/QEMU, and ~2 GB disk for the SONiC image.

```bash
scripts/getting-started.sh
```

The script walks you through downloading the SONiC community VM image,
building **newtron**, deploying a single-switch lab, and applying your first
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

By default, **newtron** shows what it _would_ write to CONFIG_DB — every table, key, and field:

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
  [ADD] NEWTRON_INTENT|Ethernet0                         → map[operation:apply-service service_name:transit ...]

DRY-RUN: No changes applied. Use -x to execute.
```

These aren't templates rendered from Jinja. **newtron** computed them by running
its operations against the device's profile — AS 65001, loopback 10.0.0.1,
the transit service spec, and the /31 address you provided. The same code
path runs online against a live device or offline for topology provisioning.

Add `-x` to execute. **newtron** writes atomically, re-reads to verify, then persists:

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

## Using Claude Code with newtron

[Claude Code](https://claude.com/claude-code) can set up the entire environment
for you — build, download the SONiC image, deploy a lab, and run your first
operation — with status updates along the way.

```bash
# Install Claude Code
npm install -g @anthropic-ai/claude-code

# Sign in to your Claude account
claude login

# Clone and start
git clone https://github.com/aldrin-isaac/newtron.git && cd newtron && claude
```

Claude Code reads the project configuration on startup, detects a fresh clone,
and walks through the full setup automatically. When it's done, you have a
built project, a running SONiC lab, and an oriented AI assistant ready to work.

## Explore Without VMs

You can explore **newtron**'s specs and dry-run output without deploying
any SONiC devices. Build, start the server with a shipped topology's
specs, and browse:

```bash
make build
bin/newtron-server --spec-dir newtrun/topologies/2node-vs/specs &

bin/newtron service list                     # List defined services
bin/newtron switch1 show                     # Show device profile
bin/newtron switch1 --topology intent drift  # Compare topology intent vs device
```

The same operations are available as HTTP endpoints:

```bash
curl localhost:8080/network/default/service                  # List services
curl localhost:8080/network/default/node/switch1/interface     # List interfaces
curl localhost:8080/network/default/topology/node              # List devices
```

## How It Works

Spec files describe what the network should look like. Services define
what an interface does (transit peering, L2 bridging, IRB, EVPN overlay).
VPN specs define overlay parameters. Route policies and filters control
path selection. Device profiles identify each switch — AS number, loopback
IP, platform, EVPN peers. When you apply a service to an interface,
**newtron** resolves the spec against the device's profile to produce
device-specific CONFIG_DB entries.

```
specs/
├── network.json        # Services, VPNs, filters, routing policy, QoS
├── platforms.json      # Platform capabilities, VM defaults
└── profiles/           # Per-device: loopback IP, ASN, zone, EVPN peers
    ├── spine1.json
    └── leaf1.json
```

**What you can configure.** VLANs, VRFs, ACLs, QoS, LAGs, EVPN overlays,
static routes, route policies, prefix filters. Each has one pattern — one
opinion about how that unit of configuration should look in CONFIG_DB.
Routing currently uses all-eBGP — hop-by-hop for the underlay,
loopback-to-loopback for EVPN peers. ASN assignment is per-profile:
every leaf can have a unique ASN, or switches in a spine tier can share one.

**Every service binds to an interface.** The interface is where abstract
intent meets physical infrastructure — the unit of lifecycle (apply, remove,
refresh), the unit of state (one service binding or none), and the unit of
isolation (services on different interfaces don't affect each other).

**Every forward operation has a reverse.** Apply and remove. Create and
delete. Bind and unbind. Service bindings stored on the device record
exactly what was applied, so removal always reconstructs the teardown —
even if the spec has changed since. Without this, CONFIG_DB entries
accumulate with no way to clean them up.

**Every write records an intent.** From those records, **newtron** derives
what CONFIG_DB *should* look like. `intent drift` compares that expected
state against the actual device. `intent reconcile` closes the gap.
External CONFIG_DB edits are detected as drift — not silently accepted.

**Shared resources are named by their content.** ACL tables, route maps,
and prefix sets get names derived from what they contain. If the spec
hasn't changed, the name hasn't changed, and a refresh is a no-op. If the
spec changes, the resource gets a new name — both versions coexist while
interfaces migrate one by one.

## Architecture

**One object, three states.** Most automation systems maintain two
representations of a device — one for intent, one for reality — and
synchronize them by hope. **newtron** uses one: the Node, a software
object that represents a device. Initialized from specs, it is the
desired state. Connected to a live device, it is the desired state
verified against reality. Rebuilt from intent records stored on the
device itself, it recovers after a crash. Same type, same methods,
same code path.

**Intent lives on the device.** Every operation records what it did
on the device itself — not in an external store. After a crash, a
reboot, or a lost connection, the device's own records are sufficient
to reconstruct the expected state. Those intents can also be persisted
back to the topology — newtron's offline representation of the desired
network — so that a device that loses its configuration can be
recovered from stored intents. The common example is RMA: replace a
switch, replay its intents, and the new device converges to the same
state as the old one.

**Redis-first.** All device interaction goes through SONiC's Redis
databases. CONFIG_DB writes use a native Go Redis client over SSH-
tunneled connections — not SONiC `config` commands. Route verification
reads APP_DB. ASIC programming checks traverse ASIC_DB. Health checks
read STATE_DB. Device shell access is a documented exception, not a
normalized path.

**One code path.** Online operations against a live device and offline
topology provisioning run the same code. There is no template engine,
no separate provisioning pipeline — the operations *are* the
provisioning.

## Verification

Every mutating operation produces a **ChangeSet** — an ordered list of
CONFIG_DB mutations that serves as dry-run preview, execution receipt,
and verification contract. After execution, **newtron** re-reads every
entry it wrote and diffs against the ChangeSet. If anything is missing
or wrong, you know immediately.

Beyond its own writes, **newtron** observes but does not judge. It reads
APP_DB routes, resolves ASIC_DB SAI chains, and returns structured health
reports from STATE_DB — but these are data, not verdicts. Cross-device
assertions (did the route propagate? is the fabric converged?) belong to
the test orchestrator, not to the device tool. **newtron** gives you the
observations; you decide what they mean.

## Testing Infrastructure

Proving the primitives work requires running them against real SONiC
software. Beyond testing newtron itself, this infrastructure helps
discover SONiC issues before they show up on a real network —
though platform-dependent behavior still requires real devices.

**newtlab** deploys QEMU virtual machines and wires them into topologies
using userspace networking — no root, no Linux bridges, no Docker. Every
packet between VMs passes through **newtlink**, a Go bridge that handles
Ethernet frames in userspace. Topologies can span multiple servers.

**newtrun** executes YAML test scenarios against **newtron-server** — each
scenario is a sequence of HTTP calls to **newtron-server** (provision a device,
check BGP sessions, verify CONFIG_DB entries) and host commands (ping across
VMs, run traffic generators) that exercise the primitives end-to-end.

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

### What Has Been Validated

Shipped test suites, validated on the free community sonic-vs image:

| Suite | What it tests |
|-------|---------------|
| 1node-vs-architecture | Intent DAG, drift detection, reconciliation, dry-run, operational symmetry |
| 1node-vs-config | CLI lifecycle: apply/remove/refresh services in loopback mode |
| 2node-vs-primitive | Disaggregated operations: VLAN/VRF lifecycle, service apply/remove, BGP, LAGs, ACLs, QoS, static routing |
| 2node-vs-service | Full service lifecycle: provision → health → dataplane → deprovision → verify-clean |
| 2node-vs-drift | Drift detection and delta reconciliation |
| 2node-vs-drift-actuated | Actuated mode: intent replay, drift guard, full reconciliation |

Every platform bug encountered along the way is documented in
[`docs/rca/`](docs/rca/) — root-cause analyses covering frrcfgd,
orchagent, SAI, and platform quirks. Each RCA is something learned
by building.

## System Overview

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

Both paths converge on the same SONiC devices. **newtlab** creates QEMU VMs
running SONiC and wires them with **newtlink**; **newtron-server** connects to
those same VMs via SSH-tunneled Redis. You can also point **newtron-server** at
hardware switches or third-party labs — **newtlab** is only needed for local
virtual topologies.

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
    device/sonic/   SSH tunnels, Redis DB 0/1/4/6, CONFIG_DB parsers, locking
    network/        Network type, topology graph, spec resolution
      node/         Node + Interface types, all operations, topology provisioning
    settings/       Settings resolution (flag > env > file)
    spec/           Spec types and loader
  newtlab/          QEMU, multi-host placement, socket bridges, boot patches
  newtrun/          Scenario parser, dependency ordering, step executors, reporting
  cli/              Shared CLI formatting (tables, colors, progress)
  util/             Errors, logging, IP/string helpers

newtrun/
  topologies/       Test topologies (1node-vs, 2node-vs, 2node-vs-service)
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

Additional references: [Device Layer LLD](docs/newtron/device-lld.md) (SSH tunnels, Redis clients, CONFIG_DB types) · [API Reference](docs/newtron/api.md) · [RCA Index](docs/rca/) (SONiC platform analyses — frrcfgd, orchagent, SAI)

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```
