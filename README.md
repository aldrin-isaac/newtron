# newtron

> **Note:** newtron is a SONiC automation demonstrator — a research and learning project exploring Redis-first device management, declarative provisioning, and E2E verification against virtual SONiC topologies. It is not intended for production use.

<p align="center">
  <img src="newt.png" alt="Newt — the newtron mascot" width="280"/>
</p>

newtron is a SONiC automation system built on a simple premise: SONiC is a Redis database with daemons that react to table changes — so treat it as one.

Where other tools SSH into SONiC devices and parse CLI output, newtron connects directly to CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB through an SSH-tunneled Redis client. Where other tools lack referential integrity (SONiC's CONFIG_DB accepts contradictory entries without complaint), newtron validates preconditions before every write — preventing misconfiguration at the source. Where other tools model devices as connection targets, newtron uses a typed object hierarchy (`Network > Node > Interface`) where operations live on the smallest object that has the context to execute them.

The result: a single tool that provisions SONiC devices from declarative specs, verifies its own writes, and exposes observation primitives as structured data for any orchestrator to consume.

## Design Philosophy

Three architectural choices define newtron:

**Redis-first.** SONiC's entire state lives in Redis databases. CONFIG_DB holds desired state, APP_DB holds computed routes, ASIC_DB holds forwarding state, STATE_DB holds operational telemetry. newtron reads and writes these databases directly using a native Go Redis client — not by running `config` commands over SSH and parsing the output. This makes reads precise (typed table entries, not regex-parsed text), writes ordered and verifiable (single HSET per key; composites delivered atomically via Redis pipeline), and verification exact (Apply → Verify → Save by re-reading what was written). CLI commands are used only for operations Redis cannot express (like `config save`), and each one is tagged for future elimination.

**A domain model, not a connection wrapper.** `Network > Node > Interface` is a typed object hierarchy where each level holds the context for its operations. A Network holds specs (services, VPNs, filters). A Node holds its device profile, resolved config, and Redis connections. An Interface holds its service bindings. `Interface.ApplyService()` reaches up through the parent chain to resolve everything it needs — the device's AS number, the network's service spec, the interface's IP — and produces a ChangeSet. No external function orchestrates this. The object has the full context.

**Built-in referential integrity.** CONFIG_DB has no constraints — you can write a VLAN member for a nonexistent VLAN, a BGP neighbor in a nonexistent VRF, or a service on a LAG member. SONiC daemons will silently fail or produce undefined behavior. Every newtron write operation runs precondition validation first: interface exists, not a LAG member, VRF exists, VTEP exists for EVPN, no conflicting service. The tool structurally cannot produce invalid CONFIG_DB state.

## What newtron Provisions

newtron manages SONiC devices through CONFIG_DB. A device's full configuration — interfaces, VLANs, VRFs, BGP neighbors, EVPN overlays, ACLs, QoS — is expressed as spec files and translated into CONFIG_DB entries using each device's profile, platform, and site context. Each CONFIG_DB table has a single owning file — one source of truth for how entries are constructed, no duplication across composites or service types.

newtron reads `network.json` (services, VPNs, filters, routing policy, zones), `platforms.json` (platform capabilities), and per-device `profiles/` (loopback IP, zone, EVPN peering, credentials). It does not need `topology.json` — that file belongs to newtlab and newtest.

Key operations:

- **Service provisioning** — L2, L3, transit, and IRB services applied per-interface. Each service spec defines routing protocol, VPN binding, ingress/egress filters, and route policy. newtron derives concrete values (peer IP from interface IP, VRF name from service+interface, ACL rules from filter specs) using device context.
- **BGP management** — Full underlay and overlay BGP via SONiC's frrcfgd framework. eBGP neighbors configured per-interface through service specs. eBGP overlay (loopback-to-loopback) for multi-AF route exchange (IPv4, IPv6, L2VPN EVPN). FRR defaults applied via vtysh for features frrcfgd doesn't support.
- **EVPN/VXLAN** — VTEP creation, L2/L3 VNI mapping, SVI configuration, EVPN neighbor activation. Replaces traditional MPLS L3VPN.
- **Composite provisioning** — Full-device configuration generated offline by running the same operations (`ConfigureBGP`, `SetupEVPN`, `iface.ApplyService`) against a shadow ConfigDB. No template engine — the code that configures a live device also generates offline composites. `BuildComposite()` exports accumulated entries for atomic delivery via Redis pipeline. Delivery merges composite entries on top of existing CONFIG_DB, preserving factory defaults while removing stale keys.

Every mutating operation produces a **ChangeSet** — an ordered list of CONFIG_DB mutations with table, key, operation type, and field values. The ChangeSet serves as dry-run preview, execution receipt, and verification contract. `ChangeSet.Verify()` re-reads CONFIG_DB through a fresh connection and diffs against the ChangeSet. One verification method works for every operation.

All commands default to **dry-run**. Add `-x` to execute. This is an architectural constraint — because every operation must compute its ChangeSet without applying it, translation logic is always separate from execution logic.

## Testing Infrastructure

Proving newtron works requires running it against real SONiC software. This repository includes two supporting tools that provide that infrastructure.

### newtlab — VM Topology Orchestration

newtlab deploys QEMU virtual machines and wires them into topologies using **userspace networking** — no root, no Linux bridges, no kernel namespaces, no Docker. Every packet between VMs passes through a Go bridge process (newtlink), which means the interconnect is transparent, observable, and portable.

This is a deliberate alternative to kernel-level wiring (veth pairs, Linux bridges, tc rules). A userspace bridge knows exactly how many bytes crossed each link, because it handles every frame. Rate monitoring, tap-to-wireshark, fault injection — all are straightforward extensions of the bridge loop. Kernel networking is powerful but opaque; when a link breaks, you debug iptables rules and bridge state. When a newtlink bridge has a problem, you look at one process.

Key capabilities:

- **Userspace socket links** — each inter-VM link is a bridge worker (a goroutine in the newtlink process) that listens on two TCP ports and bridges Ethernet frames between them. Both QEMU VMs connect outbound to newtlink — no startup ordering, no listen/connect asymmetry.
- **Per-link telemetry** — bridge workers track byte counters and session state per link. `newtlab bridge-stats` aggregates counters from all hosts into a single table.
- **Multi-host deployment** — topologies span multiple servers. Cross-host links work identically to local links: the local endpoint connects to `127.0.0.1`, the remote endpoint connects across the network. Define a server pool in `topology.json` with capacity constraints; newtlab auto-places VMs using a spread algorithm. All controlled from a single spec directory.
- **No privileged access** — no root, no sudo, no kernel modules. VMs need KVM for performance, but the interconnect is pure userspace.
- **Platform boot patches** — different SONiC images have platform-specific initialization quirks. A declarative patch framework (JSON descriptors + Go templates) handles post-boot fixups without Go code changes.
- **Port conflict detection** — before starting any process, newtlab probes all allocated ports across all hosts and reports every conflict in a single error.
- **Multiple SONiC images** — platform definitions support VS (control plane only), VPP (full forwarding), Cisco 8000, and vendor images, each with their own NIC driver, interface mapping, CPU features, and credentials.

After deployment, newtlab patches device profiles with SSH ports, console ports, and deterministic system MACs so newtron can connect and provision correctly. On destroy, it restores original profiles. The spec directory is the only coordination surface between the tools.

### newtest — E2E Test Orchestrator

newtest tests **composed network outcomes**, not individual features. The question is not "does VLAN creation work?" — it's "does the L3VPN service produce reachability across the overlay?" A feature test can pass while the composite multi-feature configuration fails due to ordering issues, missing glue config, or daemon interaction bugs. newtest tests the thing that actually matters: the assembled result.

newtest deploys a topology (via newtlab), provisions devices (via newtron), then runs scenarios that assert correctness — both on individual devices and across the fabric. It observes devices through newtron's primitives (health, BGP/EVPN/VLAN/VRF status, ChangeSet verification) — it never accesses Redis directly.

Key capabilities:

- **YAML scenario format** — each test is a sequence of steps with an action, target devices, parameters, and optional assertions. Step actions cover the full range: provisioning, BGP, EVPN, VLAN/VRF/VTEP lifecycle, interface configuration, ACL management, health checks, data plane verification, service churn.
- **Incremental suites** — scenarios declare dependencies (`requires: [provision, bgp-converge]`) and execute in topological order. If a dependency fails, all dependents are skipped. A shared deployment is reused across the suite — deploy once, run a suite of scenarios.
- **Cross-device assertions** — newtest is the only program that connects to multiple devices simultaneously. Scenarios can assert cross-device outcomes (e.g., that a route configured on spine1 shows up in leaf1's APP_DB, that BGP sessions reach Established on both ends, that data plane forwarding works end-to-end).
- **Repeat/stress mode** — `repeat: N` on a scenario runs it N times with per-iteration fail-fast and concise console output for identifying intermittent failures.
- **Report generation** — console output with ANSI formatting, markdown reports, and JUnit XML for CI integration.

### Validated

All three test suites pass on Cisco Silicon One (CiscoVS Palladium2):

| Suite | Result | Coverage |
|-------|--------|----------|
| 2node-primitive | 20/20 | Health, BGP, VLAN/VRF/VTEP lifecycle, service apply/remove, port channels, ACLs, QoS |
| 2node-service | 6/6 | Full service lifecycle: provision, health, dataplane, deprovision, verify-clean |
| 3node-dataplane | 6/6 | L3 routing, EVPN L2 bridging over VXLAN tunnels |

32 scenarios, zero failures. EVPN VXLAN L2 bridging verified end-to-end between virtual switches running Cisco Silicon One SAI.

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

Spec files describe network intent as declarative constraints. newtron resolves them into device-specific CONFIG_DB entries using each device's profile (loopback IP, AS number, role), platform (port count, HWSKU, dataplane capabilities), and site (zones, cluster IDs).

The same spec applied to different devices produces different config. The same spec applied twice to the same device produces identical config — this is what makes provisioning idempotent.

```
specs/
├── network.json         # Services, VPNs, filters, routing policy    ← newtron
└── profiles/            # Per-device: loopback IP, zone, EVPN, SSH ← newtron + newtlab
    ├── spine1.json
    └── leaf1.json

# Test topologies (newtest/topologies/*/) include additional specs:
# ├── platforms.json   # Platform capabilities, VM defaults          ← newtron + newtlab
# └── topology.json    # Devices, interfaces, links                  ← newtlab + newtest
```

## Quick Start

Requires Go 1.24+. newtlab requires KVM for VM acceleration (`/dev/kvm`).

```bash
make build                # → bin/newtron, bin/newtlab, bin/newtest, bin/newtlink
```

### VM images

newtlab looks for QCOW2 base images in `~/.newtlab/images/`, referenced by `vm_image` in `platforms.json`:

```bash
mkdir -p ~/.newtlab/images
cp sonic-ciscovs.qcow2 ~/.newtlab/images/    # Cisco Silicon One VS
cp alpine-testhost.qcow2 ~/.newtlab/images/  # lightweight test host
```

At deploy time, newtlab creates copy-on-write overlay disks — base images are never modified. All runtime state lives under `~/.newtlab/labs/<topology>/`:

```
~/.newtlab/
├── images/                          # Base QCOW2 images (user-provided)
│   ├── sonic-ciscovs.qcow2
│   └── alpine-testhost.qcow2
├── labs/                            # Per-lab runtime state (created by deploy)
│   └── 2node-service/
│       ├── state.json               # PIDs, ports, node status
│       ├── disks/                   # COW overlay disks
│       │   ├── switch1.qcow2
│       │   └── switch2.qcow2
│       ├── logs/                    # QEMU console logs
│       └── bridge.json              # newtlink bridge assignments
└── bin/                             # newtlink binary (uploaded to remote hosts)
```

`newtlab destroy` cleans up the lab directory. Base images are never touched.

### Provision a device

```bash
newtron -S specs/ spine1 provision                                      # Preview full provisioning
newtron -S specs/ spine1 provision -x                                   # Execute (Apply → Verify → Save)
newtron -S specs/ spine1 show                                           # Show derived config + counts
newtron -S specs/ spine1 health check                                   # Health checks
newtron -S specs/ leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
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
    device/
      sonic/     SONiC connection manager — SSH tunnels, Redis DB 0/1/4/6, locking
    network/     Network type, topology graph, spec access
      node/      Node and Interface types, all operations, composite provisioning
    settings/    Settings resolution (flag > env > file)
    spec/        Spec types and loader
  util/          Errors, logging, IP/string helpers
  version/       Build version info

specs/           Network and topology specifications
newtest/
  topologies/    Test topologies (2node, 2node-service, 3node, 4node)
  suites/        Test suites (5 suites, 46 scenarios)
```

## Documentation

[Design Principles](docs/DESIGN_PRINCIPLES.md) explains the philosophy behind the system — the boundaries between programs, the object model, why verification works the way it does, and the spec-vs-config separation. Read it first.

| | HLD | LLD | HOWTO |
|-|-----|-----|-------|
| **newtron** | [Design](docs/newtron/hld.md) | [Types & Methods](docs/newtron/lld.md) | [Usage](docs/newtron/howto.md) |
| **newtlab** | [Design](docs/newtlab/hld.md) | [Types & Methods](docs/newtlab/lld.md) | [Usage](docs/newtlab/howto.md) |
| **newtest** | [Design](docs/newtest/hld.md) | [Types & Methods](docs/newtest/lld.md) | [Usage](docs/newtest/howto.md) |

[RCA Index](docs/rca/) — 40+ root-cause analyses documenting SONiC platform bugs, daemon interactions, and workarounds discovered during development. Covers frrcfgd, orchagent, vlanmgrd, SAI behavior, and CiscoVS/VPP platform-specific issues. This is institutional knowledge about SONiC internals that doesn't exist in upstream documentation.

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```
