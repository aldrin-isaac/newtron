# newtron

<p align="center">
  <img src="newt.png" alt="Newt — the newtron mascot" width="280"/>
</p>

newtron is an opinionated automation tool for SONiC network devices. It reads declarative spec files that describe what the network should look like — services, routing policy, EVPN fabrics — and translates them into CONFIG_DB entries on each device through an SSH tunnel. It operates on one device at a time, verifies its own writes, and exposes observation primitives (routes, ASIC state, health) as structured data for any orchestrator to consume.

The goal is a single tool that can provision a SONiC device from a spec directory and prove the result is correct — without requiring the operator to inspect Redis, read FRR output, or trust that automation "probably worked."

## What newtron Provisions

newtron manages SONiC devices through CONFIG_DB. A device's full configuration — interfaces, VLANs, VRFs, BGP neighbors, EVPN overlays, ACLs, QoS — is expressed as spec files and translated into CONFIG_DB entries using each device's profile, platform, and site context.

The object model is hierarchical: `Network > Device > Interface`. A Network holds specs (services, filters, VPNs, sites, platforms). A Device holds its profile, resolved config, and Redis connections. An Interface holds its service bindings. Methods live on the smallest object that has the context to execute them — `ApplyService` lives on Interface because the interface's identity is part of the translation context; `VerifyChangeSet` lives on Device because it needs the Redis connection.

newtron reads `network.json` (services, VPNs, filters, routing policy), `site.json` (regions, AS numbers), `platforms.json` (platform capabilities), and per-device `profiles/` (loopback IP, role, credentials). It does not need `topology.json` — that file belongs to newtlab and newtest.

Key operations:

- **Service provisioning** — L2, L3, transit, and IRB services applied per-interface. Each service spec defines routing protocol, VPN binding, ingress/egress filters, and route policy. newtron derives concrete values (peer IP from interface IP, VRF name from service+interface, ACL rules from filter specs) using device context.
- **BGP management** — Full underlay and overlay BGP via SONiC's frrcfgd framework. eBGP neighbors configured per-interface through service specs. iBGP overlay with multi-AF route reflection (IPv4, IPv6, L2VPN EVPN). FRR defaults applied via vtysh for features frrcfgd doesn't support.
- **EVPN/VXLAN** — VTEP creation, L2/L3 VNI mapping, SVI configuration, EVPN neighbor activation. Replaces traditional MPLS L3VPN.
- **Composite provisioning** — Build a full device configuration offline from specs, then deliver it as a single atomic Redis pipeline. Used for initial provisioning; individual operations used for incremental changes.

Every mutating operation produces a **ChangeSet** — an ordered list of CONFIG_DB mutations with table, key, operation type, and old/new values. The ChangeSet serves as dry-run preview, execution receipt, and verification contract. `VerifyChangeSet` re-reads CONFIG_DB through a fresh connection and diffs against the ChangeSet. One verification method works for every operation.

All commands default to **dry-run**. Add `-x` to execute. This is an architectural constraint — because every operation must compute its ChangeSet without applying it, translation logic is always separate from execution logic.

## Testing Infrastructure

Proving newtron works requires running it against real SONiC software. This repository includes two supporting tools that provide that infrastructure.

### newtlab — VM Topology Orchestration

newtlab deploys QEMU virtual machines and wires them into topologies. It reads the same spec files as newtron — `topology.json` for device layout, `platforms.json` for VM defaults, `profiles/` for per-device overrides.

Key capabilities:

- **No privileged access** — no root, no sudo, no Docker, no Linux bridges or TAP interfaces. VMs connect through userspace socket links.
- **Multi-host deployment** — topologies can span multiple servers. Define a server pool in `topology.json` with capacity constraints; newtlab auto-places VMs using a spread algorithm that minimizes maximum load per server. Pinned VMs respect explicit placement; unpinned VMs are distributed automatically. All controlled from a single spec directory.
- **newtlink bridge agents** — each inter-VM link is served by a bridge worker (a goroutine in the newtlink process) that listens on two TCP ports and bridges Ethernet frames between them. Both QEMU VMs connect outbound to newtlink — no startup ordering, no listen/connect asymmetry. Cross-host links work identically: the local endpoint connects to `127.0.0.1`, the remote endpoint connects across the network. Bridge workers are load-balanced across hosts using a deterministic algorithm that each server computes independently.
- **Per-link telemetry** — bridge workers track byte counters and session state per link. `newtlab bridge-stats` aggregates counters from all hosts (local via Unix socket, remote via TCP) into a single table.
- **Platform boot patches** — different SONiC images have platform-specific initialization quirks. A declarative patch framework (JSON descriptors + Go templates) handles post-boot fixups without Go code changes. Patches are organized by dataplane and release version; adding support for a new platform means adding a directory of descriptors.
- **Port conflict detection** — before starting any process, newtlab probes all allocated ports (SSH, console, link, bridge stats) across all hosts and reports every conflict in a single error.
- **Multiple SONiC images** — platform definitions in `platforms.json` support VS (control plane only), VPP (full forwarding), Cisco 8000, and vendor images, each with their own NIC driver, interface mapping, CPU features, and credentials.

After deployment, newtlab patches device profiles with SSH and console ports so newtron can connect. On destroy, it restores original profiles. The spec directory is the only coordination surface between the tools.

### newtest — E2E Test Orchestrator

newtest deploys a topology (via newtlab), provisions devices (via newtron), then runs test scenarios that assert correctness — both on individual devices and across the fabric.

Key capabilities:

- **YAML scenario format** — each test is a sequence of steps with an action, target devices, parameters, and optional assertions. 39 step actions cover the full range: provisioning, BGP, EVPN, VLAN/VRF/VTEP lifecycle, interface configuration, health checks, data plane verification, service churn.
- **Incremental suites** — scenarios declare dependencies (`requires: [provision, bgp-converge]`) and execute in topological order. If a dependency fails, all dependents are skipped. A shared deployment is reused across the suite — deploy once, run 31 scenarios.
- **Repeat/stress mode** — `repeat: N` on a scenario runs it N times with per-iteration fail-fast and concise console output for identifying intermittent failures.
- **Cross-device assertions** — newtest is the only program that connects to multiple devices simultaneously. It can verify that a route configured on spine1 actually arrives in leaf1's APP_DB, that BGP sessions reach Established state on both ends, that data plane forwarding works end-to-end.
- **Report generation** — console output with ANSI formatting, markdown reports, and JUnit XML for CI integration.
- **Live progress** — a ProgressReporter interface provides real-time callbacks at suite, scenario, and step lifecycle points.

newtest observes devices exclusively through newtron's primitives (`GetRoute`, `GetRouteASIC`, `VerifyChangeSet`). It never accesses Redis directly.

## Verification Model

newtron's verification primitives fall into two categories — assertions about its own work, and observations of device state:

| Tier | Question | What newtron does |
|------|----------|-------------------|
| CONFIG_DB | Did my writes land? | Asserts — re-reads and diffs against ChangeSet |
| APP_DB | Is the route present locally? | Observes — returns `RouteEntry` with ECMP nexthops |
| ASIC_DB | Are SAI objects programmed? | Observes — resolves full OID chain |
| Operational | Is the device healthy? | Observes — returns structured health report |
| Cross-device | Did the route propagate? | Not newtron's job — orchestrators compose observations |

The rule: return data, not judgments. A method that returns a `RouteEntry` is useful to any caller. A method that returns true/false for "is this route correct?" encodes assumptions that break when the calling context changes. newtron provides the mechanism to check; orchestrators decide what's correct.

## Specs

Spec files describe network intent as declarative constraints. newtron resolves them into device-specific CONFIG_DB entries using each device's profile (loopback IP, AS number, role), platform (port count, HWSKU, dataplane capabilities), and site (regions, cluster IDs).

The same spec applied to different devices produces different config. The same spec applied twice to the same device produces identical config — this is what makes provisioning idempotent.

```
specs/
├── network.json         # Services, VPNs, filters, routing policy    ← newtron
├── site.json            # Site topology, regions, AS numbers          ← newtron
└── profiles/            # Per-device: loopback IP, role, SSH port     ← newtron + newtlab
    ├── spine1.json
    └── leaf1.json

# Test topologies (newtest/topologies/*/) include additional specs:
# ├── platforms.json   # Platform capabilities, VM defaults          ← newtron + newtlab
# └── topology.json    # Devices, interfaces, links                  ← newtlab + newtest
```

## Quick Start

Requires Go 1.24+.

```bash
make build                # → bin/newtron, bin/newtlab, bin/newtest, bin/newtlink
```

### Provision a device

```bash
newtron -n specs/ -d spine1 provision                   # Preview full provisioning
newtron -n specs/ -d spine1 provision -x                # Execute it
newtron -n specs/ -d spine1 verify                      # Verify CONFIG_DB matches
newtron -n specs/ -d leaf1 -i Ethernet0 apply-service   # Single interface
newtron -n specs/ -d spine1 get-route 10.0.0.1/32       # Query APP_DB
newtron -n specs/ -d spine1 health                      # Health checks
```

### Deploy a test topology

```bash
newtlab deploy -S specs/          # Deploy QEMU VMs, wire links
newtlab status                    # Check VM and link state
newtlab ssh spine1                # SSH into a device
newtlab bridge-stats              # Live link telemetry
newtlab provision specs/          # Provision all devices
newtlab destroy                   # Tear down
```

### Deploy across multiple servers

```bash
# topology.json defines the server pool:
#   "servers": [
#     { "name": "server-a", "address": "10.0.0.1", "max_nodes": 4 },
#     { "name": "server-b", "address": "10.0.0.2", "max_nodes": 4 }
#   ]

# Each server deploys its own nodes (deterministic placement, no coordination):
newtlab deploy -S specs/ --host server-a    # on server-a
newtlab deploy -S specs/ --host server-b    # on server-b

# Control everything from one host:
newtlab status                              # shows all nodes across all servers
newtlab ssh leaf1                           # SSH to any node regardless of host
```

### Run E2E tests

```bash
newtest start --dir newtest/suites/2node-incremental    # 31-scenario incremental suite
newtest start --scenario health.yaml                    # Single scenario
newtest list                                            # Discover available suites
newtest topologies                                      # List topologies with device/link counts
```

## Repository Layout

```
cmd/
  newtron/       Device provisioning and verification CLI
  newtlab/       VM orchestration CLI
  newtest/       E2E test runner CLI
  newtlink/      Bridge traffic agent (standalone, deployed by newtlab to remote hosts)

pkg/
  cli/           Shared CLI formatting
  newtest/       Scenario parser, dependency ordering, 39 step executors,
                 progress reporting, JUnit/markdown output
  newtlab/       QEMU, multi-host placement, socket bridges, port probing,
                 boot patch framework
  newtron/
    audit/       Audit event logging
    auth/        Permission checking and user authorization
    device/      Shared device types
      sonic/     SONiC device layer — SSH tunnels, Redis DB 0/1/4/6, locking
    health/      Health checks
    network/     Network type, topology graph, spec access
      node/      Node and Interface types, all operations, composite provisioning
    settings/    Settings resolution (flag > env > file)
    spec/        Spec types and loader
  util/          Errors, logging, IP/string helpers
  version/       Build version info

specs/           Network and topology specifications
newtest/
  topologies/    Test topologies (2node, 4node)
  suites/        Test suites (31 incremental + 9 standalone scenarios)
```

## Documentation

[Design Principles](docs/DESIGN_PRINCIPLES.md) explains the philosophy behind the system — the boundaries between programs, the object model, why verification works the way it does, and the spec-vs-config separation. Read it first.

| | HLD | LLD | HOWTO |
|-|-----|-----|-------|
| **newtron** | [Design](docs/newtron/hld.md) | [Types & Methods](docs/newtron/lld.md) | [Usage](docs/newtron/howto.md) |
| **newtlab** | [Design](docs/newtlab/hld.md) | [Types & Methods](docs/newtlab/lld.md) | [Usage](docs/newtlab/howto.md) |
| **newtest** | [Design](docs/newtest/hld.md) | [Types & Methods](docs/newtest/lld.md) | [Usage](docs/newtest/howto.md) |

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```
