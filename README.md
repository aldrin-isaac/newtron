# newtron

<p align="center">
  <img src="newt.png" alt="Newt — the newtron mascot" width="280"/>
</p>

newtron is a SONiC automation system that provisions and verifies EVPN/VXLAN fabrics by treating SONiC devices as Redis databases — reading and writing CONFIG_DB, APP_DB, ASIC_DB, and STATE_DB directly through an SSH-tunneled Redis client.

> **Status:** Research demonstrator — not production-hardened. Validated against Cisco Silicon One virtual switches with a growing suite of E2E scenarios. Use it to learn SONiC internals, test automation ideas, or build something better.

## Validated

All three primary test suites pass on Cisco Silicon One (CiscoVS Palladium2):

| Suite | Scenarios | Coverage |
|-------|-----------|----------|
| **2node-primitive** | 20/20 | Health, BGP underlay+overlay, VLAN/VRF/VTEP lifecycle, service apply/remove/refresh, port channels, ACLs, QoS |
| **2node-service** | 6/6 | Full service lifecycle: provision → health → data plane → deprovision → verify clean teardown |
| **3node-dataplane** | 6/6 | L3 routing, EVPN L2 bridging over VXLAN tunnels, inter-subnet IRB forwarding |

32 scenarios, zero failures. EVPN VXLAN L2 bridging verified end-to-end between virtual switches running Cisco Silicon One SAI. Additional suites and scenarios are straightforward to add — newtest's YAML format and extensible step actions cover the full operational surface.

## Quick Start

**Prerequisites:** Go 1.24+. newtlab requires KVM for VM acceleration (`/dev/kvm`).

```bash
make build                    # → bin/newtron, bin/newtlab, bin/newtest, bin/newtlink
```

### Provision a device

```bash
newtron -S specs/ spine1 provision              # Preview full provisioning (dry-run)
newtron -S specs/ spine1 provision -x           # Execute
newtron -S specs/ spine1 health check           # Health checks (BGP, interfaces, VTEP)
newtron -S specs/ spine1 show                   # Show device details
newtron -S specs/ leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
```

All commands default to **dry-run**. Add `-x` to execute. The first positional argument is the device name (`spine1`, `leaf1`).

### Deploy a virtual lab

newtlab looks for QCOW2 base images in `~/.newtlab/images/`:

```bash
mkdir -p ~/.newtlab/images
cp sonic-ciscovs.qcow2 ~/.newtlab/images/     # Cisco Silicon One VS
cp alpine-testhost.qcow2 ~/.newtlab/images/   # lightweight test host
```

```bash
newtlab deploy -S specs/         # Deploy QEMU VMs, wire links via userspace bridges
newtlab status                   # Check VM and link state
newtlab ssh spine1               # SSH into a device
newtlab bridge-stats             # Live per-link telemetry
newtlab destroy                  # Tear down
```

### Run E2E tests

```bash
newtest start --dir newtest/suites/2node-primitive    # Full suite (20 scenarios)
newtest start --scenario health.yaml                  # Single scenario
newtest list                                          # Discover available suites
```

## What newtron Configures

newtron manages SONiC devices through CONFIG_DB. A device's full configuration — interfaces, VLANs, VRFs, BGP, EVPN overlays, ACLs, QoS — is expressed as spec files and translated into CONFIG_DB entries using each device's profile, platform, and zone context.

- **Interfaces** — physical ports, VLANs, SVIs, PortChannels, loopbacks
- **VRFs** — creation, interface binding, L3VNI mapping, static routes
- **BGP** — underlay eBGP (per-interface), overlay iBGP (multi-AF: IPv4, IPv6, L2VPN EVPN)
- **EVPN/VXLAN** — VTEP creation, L2/L3 VNI mapping, EVPN neighbor activation, ARP suppression, anycast gateways
- **Services** — abstract service definitions (bridged, routed, IRB, EVPN variants) applied per-interface with automatic derivation of peer IPs, VRF names, and ACL bindings
- **ACLs** — template-based filter specs with variable expansion
- **QoS** — DSCP→TC→Queue mappings, scheduler profiles, WRED curves
- **Routing Policy** — prefix-sets, community-sets, route-maps applied to BGP neighbors

Every CONFIG_DB table has a **single owning file** — one source of truth for entry construction. No duplication across service types or composite operations.

## Design Philosophy

Three architectural choices define newtron:

**Redis-first.** SONiC's entire state lives in Redis databases. CONFIG_DB holds desired state, APP_DB holds computed routes, ASIC_DB holds forwarding state, STATE_DB holds operational telemetry. newtron reads and writes these databases directly — not by running `config` commands over SSH and parsing the output. Reads return typed table entries, not regex-parsed text. Writes go through Redis commands, not CLI wrappers. Verification is exact: re-read what you wrote through a fresh connection and diff field-by-field. CLI commands are used only for operations Redis cannot express (like `config save`), and each call site is tagged for future elimination.

**A domain model, not a connection wrapper.** `Network > Node > Interface` is a typed object hierarchy where operations live on the smallest object that has the context to execute them. A Network holds specs (services, VPNs, filters). A Node holds its device profile, resolved config, and Redis connections. An Interface holds its service bindings. `Interface.ApplyService()` reaches up through the parent chain to resolve everything it needs — device AS number, service spec, interface IP — and produces a ChangeSet. No external function orchestrates this. The object has the full context.

**Built-in referential integrity.** CONFIG_DB has no constraints — you can write a VLAN member for a nonexistent VLAN, a BGP neighbor in a nonexistent VRF, or a service on a LAG member. SONiC daemons will silently fail or produce undefined behavior. Every newtron write operation runs precondition validation first: interface exists, not a LAG member, VRF exists, VTEP exists for EVPN, no conflicting service. The tool structurally cannot produce invalid CONFIG_DB state.

## Verification Model

Every mutating operation produces a **ChangeSet** — an ordered list of CONFIG_DB mutations with table, key, operation type, and field values. The ChangeSet serves as dry-run preview, execution receipt, and verification contract.

After applying changes, newtron verifies writes by re-reading CONFIG_DB through a fresh Redis connection and diffing against the ChangeSet. If verification fails, config is not saved — `config reload` restores the last known-good state. One verification method works for every operation.

Beyond CONFIG_DB writes, newtron provides observation primitives across all four Redis databases:

| Tier | Question | What newtron does |
|------|----------|-------------------|
| CONFIG_DB | Did my writes land? | Asserts — re-reads and diffs against ChangeSet |
| APP_DB | Is the route present locally? | Observes — returns `RouteEntry` with ECMP nexthops |
| ASIC_DB | Are SAI objects programmed? | Observes — resolves full OID chain |
| Operational | Is the device healthy? | Observes — returns structured health report |
| Cross-device | Did the route propagate? | Not newtron's job — orchestrators compose observations |

The rule: return data, not judgments. A method that returns a `RouteEntry` is useful to any caller. A method that returns true/false for "is this route correct?" encodes assumptions that break when the calling context changes. newtron provides the mechanism to check; orchestrators decide what's correct.

## Composite Provisioning

To provision a device from `topology.json`, newtron creates an **abstract Node** — a Node with an in-memory CONFIG_DB instead of a Redis connection — then runs the exact same operations the CLI uses:

```go
n := node.NewAbstract(specs, "spine1", profile, resolved)
n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
n.ConfigureBGP(ctx)                       // writes to in-memory CONFIG_DB
n.SetupEVPN(ctx, loopbackIP)              // same code path as live operations
iface, _ := n.GetInterface("Ethernet0")
iface.ApplyService(ctx, "transit", opts)  // same
composite := n.BuildComposite()           // export accumulated entries
```

No template engine. No separate code path. The code that configures a live device IS the topology provisioner. `BuildComposite()` exports accumulated entries for atomic delivery via Redis pipeline. Delivery merges composite entries on top of existing CONFIG_DB, preserving factory defaults while removing stale keys.

## Testing Infrastructure

Proving newtron works requires running it against real SONiC software. This repository includes two tools that provide that infrastructure.

### newtlab — VM Topology Orchestration

newtlab deploys QEMU virtual machines and wires them into topologies using **userspace networking** — no root, no Linux bridges, no kernel namespaces, no Docker. Every packet between VMs passes through a Go bridge process (newtlink), which means the interconnect is transparent, observable, and portable.

Key capabilities:

- **Userspace socket links** — each inter-VM link is a bridge worker (a goroutine in the newtlink process) that bridges Ethernet frames between two TCP ports. Both QEMU VMs connect outbound to newtlink — no startup ordering, no listen/connect asymmetry.
- **Per-link telemetry** — bridge workers track byte counters and session state per link. `newtlab bridge-stats` aggregates counters from all hosts into a single table.
- **Multi-host deployment** — topologies span multiple servers. Cross-host links work identically to local links. Define a server pool in `topology.json` with capacity constraints; newtlab auto-places VMs using a spread algorithm.
- **No privileged access** — no root, no sudo, no kernel modules. VMs need KVM for performance, but the interconnect is pure userspace.
- **Platform boot patches** — different SONiC images have platform-specific initialization quirks. A declarative patch framework handles post-boot fixups without Go code changes.
- **Multiple SONiC images** — platform definitions support Cisco Silicon One VS, VPP, and vendor images, each with their own NIC driver, interface mapping, and credentials.

### newtest — E2E Test Orchestrator

newtest tests **composed network outcomes**, not individual features. The question is not "does VLAN creation work?" — it's "does the L3VPN service produce reachability across the overlay?" A feature test can pass while the composite multi-feature configuration fails due to ordering issues, missing glue config, or daemon interaction bugs. newtest tests the thing that actually matters: the assembled result.

Key capabilities:

- **YAML scenario format** — each test is a sequence of steps with an action, target devices, parameters, and optional assertions. Step actions cover provisioning, BGP, EVPN, VLAN/VRF/VTEP lifecycle, interface configuration, ACL management, health checks, data plane verification, and service churn — new actions are added as operations are added to newtron.
- **Incremental suites** — scenarios declare dependencies (`requires: [provision, bgp-converge]`) and execute in topological order. If a dependency fails, all dependents are skipped.
- **Cross-device assertions** — newtest connects to multiple devices simultaneously, verifying that routes configured on one device arrive in another's APP_DB, that BGP sessions reach Established on both ends, and that data plane forwarding works end-to-end.
- **Repeat/stress mode** — `repeat: N` on a scenario runs it N times for identifying intermittent failures.
- **Report generation** — console output with ANSI formatting, markdown reports, and JUnit XML for CI integration.

## Specs

Spec files describe network intent as declarative constraints. newtron resolves them into device-specific CONFIG_DB entries using each device's profile (loopback IP, AS number, role), platform (port count, HWSKU, dataplane capabilities), and zone (peers, cluster IDs).

The same spec applied to different devices produces different config. The same spec applied twice to the same device produces identical config — this is what makes provisioning idempotent.

```
specs/
├── network.json         # Services, VPNs, filters, routing policy, zones
└── profiles/            # Per-device: loopback IP, zone, EVPN, SSH
    ├── spine1.json
    └── leaf1.json
```

## How newtron Compares

| Tool | Writes | Reads | Verification | Referential Integrity |
|------|--------|-------|--------------|----------------------|
| **newtron** | Redis (CONFIG_DB) | Typed table entries | Built-in: re-read and diff | Precondition checking |
| **Ansible** | SSH + CLI | CLI parsing | Separate playbook / `assert` | None |
| **NAPALM** | SSH + CLI | Structured getters | Optional compare | None |
| **containerlab** | N/A (lab orchestration) | N/A | N/A | N/A |

containerlab and newtron are complementary — containerlab deploys multi-vendor lab topologies, newtron provisions and verifies SONiC devices. newtron includes its own lab tool (newtlab) for QEMU-based SONiC topologies with userspace networking.

## What You Won't Find Here

- **Production hardening** — no HA, no session persistence, active development
- **Desired-state reconciliation** — no Terraform-style planner. The network is the source of truth; newtron mutates reality, not a desired-state model
- **Multi-vendor support** — SONiC only (though the Redis-first approach generalizes to any database-backed NOS)
- **GUI** — terminal-first, structured output for any frontend to consume

## Repository Layout

```
cmd/
  newtron/       Device provisioning and verification CLI (17 noun groups)
  newtlab/       VM orchestration CLI
  newtest/       E2E test runner CLI
  newtlink/      Bridge traffic agent (deployed by newtlab to remote hosts)

pkg/
  newtron/
    network/
      node/      Node and Interface types, all config operations, composite provisioning
    device/
      sonic/     SONiC connection manager — SSH tunnels, Redis DB 0/1/4/6, locking
    spec/        Spec types and loader
    audit/       Audit event logging
    auth/        Permission checking
    settings/    Settings resolution (flag > env > file)
  newtlab/       QEMU, multi-host placement, socket bridges, boot patches
  newtest/       Scenario parser, dependency ordering, step executors, JUnit/markdown output
  cli/           Shared CLI formatting
  util/          Errors, logging, IP/string helpers
  version/       Build version info

newtest/
  topologies/    Test topologies (2node, 2node-service, 3node, 4node)
  suites/        Test suites and scenarios
```

## Documentation

[Design Principles](docs/DESIGN_PRINCIPLES.md) explains the philosophy behind the system — the boundaries between programs, the object model, why verification works the way it does, and the spec-vs-config separation. Read it first.

| | HLD | LLD | HOWTO |
|-|-----|-----|-------|
| **newtron** | [Architecture](docs/newtron/hld.md) | [Types & Methods](docs/newtron/lld.md) | [Usage](docs/newtron/howto.md) |
| **newtlab** | [VM Orchestration](docs/newtlab/hld.md) | [Types & Methods](docs/newtlab/lld.md) | [Usage](docs/newtlab/howto.md) |
| **newtest** | [E2E Testing](docs/newtest/hld.md) | [Types & Methods](docs/newtest/lld.md) | [Usage](docs/newtest/howto.md) |

[RCA Index](docs/rca/) — root-cause analyses documenting SONiC daemon bugs, platform-specific workarounds, and configuration pitfalls discovered during development. Covers frrcfgd, orchagent, vlanmgrd, vrfmgrd, intfmgrd, SAI behavior, and CiscoVS/VPP platform issues. This collection grows as new issues are encountered — it's institutional knowledge about SONiC internals that doesn't exist in upstream documentation.

## Building

```bash
make build          # Build for current platform → bin/
make test           # Unit tests
make coverage       # Coverage report
make cross          # Cross-compile: linux/darwin × amd64/arm64
make install        # Build + install newtlink variants for remote upload
```

## Contributing

Not accepting external contributions at this time — this is a research project exploring ideas. Fork it, learn from it, build something better.

File issues for bugs or questions.
