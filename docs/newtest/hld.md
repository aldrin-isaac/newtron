# newtest — High-Level Design

## 1. Purpose

newtest is an E2E testing orchestrator that tests **composed network
outcomes** — not individual features. The question is not "does VLAN
creation work?" but "does the L3VPN service produce reachability across
the EVPN overlay?" A feature test can pass while the composite
multi-feature configuration fails due to ordering issues, missing glue
config, or daemon interaction bugs. newtest tests the thing that actually
matters: the assembled result.

It uses newtlab to deploy VM topologies, runs newtron against them, and
validates that newtron's automation produces correct device state and that
SONiC software on each device behaves correctly in its role (spine, leaf,
etc.).

newtest is one orchestrator built on top of newtron and newtlab — not the
only one. Other orchestrators could be built for different purposes
(production deployment, CI/CD pipelines, compliance auditing). newtest
observes devices exclusively through newtron's primitives (`GetRoute`,
`GetRouteASIC`, `VerifyChangeSet`) — it never accesses Redis directly.
newtron returns structured data; newtest decides what "correct" means by
correlating observations across devices.

```
┌──────────────────────────────────────────────────────────────────┐
│                            newtest                              │
│                                                                 │
│  1. Deploy topology        newtlab deploy -S specs/             │
│  2. Provision devices      newtron provision -S specs/ -d X -x  │
│  3. Validate results       CONFIG_DB, STATE_DB, data plane      │
│  4. Report pass/fail                                            │
│  5. Tear down              newtlab destroy                      │
└──────────────────────────────────────────────────────────────────┘
         │                              │
         ▼                              ▼
┌─────────────────┐          ┌─────────────────┐
│     newtlab     │          │    newtron       │
│ Deploy/manage   │          │ Provision        │
│ QEMU VMs        │          │ CONFIG_DB        │
└─────────────────┘          └─────────────────┘
```

The Runner holds a `*network.Network` object (not individual node references). Nodes are accessed via `r.Network.GetNode(name)`.

---

## 2. Three Tools, Clear Boundaries

| Tool | Responsibility | Knows About |
|------|---------------|-------------|
| **newtron** | Opinionated single-device automation: translate specs → CONFIG_DB; verify own writes; observe single-device routing state | Specs, device profiles, Redis (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB) |
| **newtlab** | Realize VM topologies: deploy QEMU VMs from newtron's topology.json, wire socket links across servers | topology.json, platforms.json, QEMU |
| **newtest** | E2E test orchestration: decide what gets provisioned where (devices, interfaces, services, parameters), sequence steps, assert cross-device correctness | Test scenarios, topology-wide expected results |

**Verification principle**: If a tool changes the state of an entity, that same
tool must be able to verify the change had the intended effect. newtron writes
CONFIG_DB and configures routing — so newtron owns verification of those
changes (`VerifyChangeSet`, `GetRoute`, `GetRouteASIC`). newtest builds on
newtron's self-verification by adding the cross-device layer: using newtron to
observe each device, then correlating observations across devices using
topology context. newtest never accesses Redis directly — it observes devices
exclusively through newtron's primitives.

---

## 3. Directory Structure

```
newtron/
├── cmd/
│   ├── newtron/            # Device provisioning CLI
│   ├── newtlab/            # VM topology management CLI
│   └── newtest/            # E2E testing CLI
│       ├── main.go         # Root command, sentinel errors, exit code mapping
│       ├── helpers.go      # resolveDir, resolveSuite, suitesBaseDir
│       ├── cmd_start.go    # start (and deprecated run) subcommand
│       ├── cmd_pause.go    # pause subcommand
│       ├── cmd_stop.go     # stop subcommand
│       ├── cmd_status.go   # status subcommand
│       ├── cmd_list.go     # list subcommand (suites and scenarios)
│       ├── cmd_suites.go   # suites subcommand (hidden alias for list)
│       └── cmd_topologies.go # topologies subcommand
├── pkg/
│   ├── newtron/
│   │   ├── network/        # newtron core (Device, Interface, CompositeBuilder,
│   │   │                   #   TopologyProvisioner)
│   │   ├── spec/           # Shared spec types
│   │   ├── device/         # Device connection layer (SSH tunnel, Redis)
│   │   ├── health/         # Health checker (interfaces, BGP, EVPN, LAG, VXLAN)
│   │   └── audit/          # Audit logger (FileLogger, event filtering)
│   ├── newtlab/            # newtlab core library
│   └── newtest/            # newtest core library
│       ├── scenario.go     # Scenario, Step, StepAction, ExpectBlock types
│       ├── parser.go       # ParseScenario, validation, dependency graph
│       ├── runner.go       # Runner, RunOptions, iterateScenarios
│       ├── steps.go        # stepExecutor interface, 38 executor implementations
│       ├── deploy.go       # DeployTopology, EnsureTopology, DestroyTopology
│       ├── state.go        # RunState, ScenarioState, SuiteStatus, persistence
│       ├── progress.go     # ProgressReporter, consoleProgress, StateReporter
│       ├── errors.go       # InfraError, StepError, PauseError
│       ├── report.go       # ScenarioResult, StepResult, StepStatus, ReportGenerator
│       └── newtest_test.go # Unit tests
├── newtest/                # E2E test assets
│   ├── topologies/
│   │   ├── 2node/
│   │   │   └── specs/
│   │   │       ├── topology.json
│   │   │       ├── network.json
│   │   │       ├── platforms.json
│   │   │       └── profiles/
│   │   │           ├── spine1.json
│   │   │           └── leaf1.json
│   │   └── 4node/
│   │       └── specs/
│   │           └── ...
│   ├── suites/
│   │   ├── 2node-standalone/
│   │   │   └── *.yaml
│   │   └── 2node-incremental/
│   │       ├── 00-boot-ssh.yaml
│   │       ├── 01-provision.yaml
│   │       ├── ...
│   │       └── 30-qos-apply-remove.yaml
│   ├── images/             # VM images or symlinks
│   └── .generated/         # Runtime output (gitignored)
│       └── report.md
└── docs/
    ├── newtlab/
    └── newtest/
```

### 3.1 Directory Layout

Each topology is self-contained with its own spec directory:

| Path | Purpose |
|------|---------|
| `topologies/*/specs/` | newtron spec dirs per topology |
| `suites/2node-standalone/` | Standalone scenario YAML files |
| `suites/2node-incremental/` | Incremental test suite with dependency ordering |
| `.generated/` | Runtime output (reports, logs) |

---

## 4. Test Topologies

### 4.1 2-Node (1 spine + 1 leaf)

```
spine1 ── Ethernet0 ─── Ethernet0 ── leaf1
```

Tests: basic BGP peering, interface configuration, service apply/remove,
health checks, baseline application, CONFIG_DB writes.

### 4.2 4-Node (2 spines + 2 leaves)

```
spine1 ─── leaf1
  ╲   ╳   ╱
spine2 ─── leaf2
```

Tests: route reflection, ECMP, EVPN, iBGP overlay, shared VRF across
leaves, multi-path, full fabric provisioning.

### 4.3 Spec Files Are Static

Test topologies are pre-defined spec directories checked into the repo. No
generation step — newtest reads them directly. This ensures tests are
reproducible and version-controlled.

Each topology directory contains the full set of newtron specs:
- `topology.json` — devices, interfaces, links (newtlab + newtron read)
- `network.json` — services, filters, VPNs, zones (newtron reads)
- `platforms.json` — platform definitions with VM settings (newtlab reads)
- `profiles/*.json` — per-device settings (newtlab writes ports, newtron reads; EVPN config includes route reflectors and cluster ID)

---

## 5. Test Scenarios

A scenario defines what to test against a deployed topology:

```yaml
# newtest/suites/2node-standalone/bgp-underlay.yaml
name: bgp-underlay
description: Verify eBGP underlay sessions establish
topology: 4node
platform: sonic-vpp

steps:
  - name: provision-all
    action: provision
    devices: all

  - name: wait-convergence
    action: wait
    duration: 30s

  - name: verify-provisioning
    action: verify-provisioning
    devices: all

  - name: verify-underlay-route
    action: verify-route
    devices: [spine1]
    prefix: "10.1.0.0/31"
    vrf: default
    expect:
      protocol: bgp
      nexthop_ip: "10.1.0.1"
      source: app_db
      timeout: 60s

  - name: verify-health
    action: verify-health
    devices: all
    expect:
      overall: ok
```

### 5.1 Step Actions

| Action | Description | Implemented By |
|--------|-------------|----------------|
| `provision` | Run `newtron provision -d <device> -x` | newtron |
| `verify-provisioning` | Verify CONFIG_DB matches expected state from provisioning | newtron `VerifyChangeSet` |
| `verify-config-db` | Assert specific CONFIG_DB table/key/field values (ad-hoc) | Direct CONFIG_DB read via newtron's `ConfigDBClient` |
| `verify-state-db` | Assert STATE_DB entries match expected values (with polling) | newtron STATE_DB read |
| `verify-bgp` | Check BGP neighbor state via STATE_DB | newtron `RunHealthChecks` |
| `verify-health` | Run health checks (interfaces, BGP, EVPN, LAG, VXLAN) [^1] | newtron `RunHealthChecks` |
| `verify-route` | Check a specific route exists on a device with expected next-hops | newtron `GetRoute` / `GetRouteASIC` |
| `verify-ping` | Data plane ping between devices (requires `dataplane: true`) | **newtest native** |
| `apply-service` | Apply a named service to a device interface | newtron |
| `remove-service` | Remove a service from a device interface | newtron |
| `apply-baseline` | Apply a configlet baseline to a device | newtron |
| `ssh-command` | Run arbitrary command via SSH, check output | newtest native |
| `wait` | Wait for specified duration | newtest native |
| `restart-service` | Restart a SONiC service (e.g., `bgp`, `swss`) | newtron `Device.RestartService()` |
| `apply-frr-defaults` | Apply FRR runtime defaults (ebgp_requires_policy, clear bgp) | newtron `Device.ApplyFRRDefaults()` |
| `set-interface` | Set interface property (mtu, description, admin-status, ip, vrf) | newtron `Interface.Set/SetIP/SetVRF` |
| `create-vlan` | Create a VLAN | newtron `Device.CreateVLAN()` |
| `delete-vlan` | Delete a VLAN | newtron `Device.DeleteVLAN()` |
| `add-vlan-member` | Add an interface to a VLAN as tagged/untagged member | newtron `Device.AddVLANMember()` |
| `create-vrf` | Create a VRF | newtron `Device.CreateVRF()` |
| `delete-vrf` | Delete a VRF | newtron `Device.DeleteVRF()` |
| `setup-evpn` | Set up EVPN overlay (VTEP + NVO + BGP EVPN) | newtron `Device.SetupEVPN()` |
| `add-vrf-interface` | Bind an interface to a VRF | newtron `Device.AddVRFInterface()` |
| `remove-vrf-interface` | Remove an interface from a VRF | newtron `Device.RemoveVRFInterface()` |
| `bind-ipvpn` | Bind an IP-VPN to a VRF | newtron `Device.BindIPVPN()` |
| `unbind-ipvpn` | Unbind an IP-VPN from a VRF | newtron `Device.UnbindIPVPN()` |
| `bind-macvpn` | Bind a MAC-VPN to a VLAN | newtron `Device.BindMACVPN()` |
| `unbind-macvpn` | Unbind a MAC-VPN from a VLAN | newtron `Device.UnbindMACVPN()` |
| `add-static-route` | Add a static route to a VRF | newtron `Device.AddStaticRoute()` |
| `remove-static-route` | Remove a static route from a VRF | newtron `Device.RemoveStaticRoute()` |
| `remove-vlan-member` | Remove an interface from a VLAN | newtron `Device.RemoveVLANMember()` |
| `apply-qos` | Apply a QoS policy to an interface | newtron `Device.ApplyQoS()` |
| `remove-qos` | Remove QoS policy from an interface | newtron `Device.RemoveQoS()` |
| `configure-svi` | Configure a Switched Virtual Interface (VLAN interface) | newtron `Device.ConfigureSVI()` |
| `bgp-add-neighbor` | Add a BGP neighbor (direct or loopback-based) | newtron `Interface.AddBGPNeighbor` / `Device.AddLoopbackBGPNeighbor` |
| `bgp-remove-neighbor` | Remove a BGP neighbor | newtron `Interface.RemoveBGPNeighbor` / `Device.RemoveBGPNeighbor` |
| `refresh-service` | Refresh a service binding on an interface | newtron `Interface.RefreshService()` |
| `cleanup` | Run device cleanup to remove orphaned resources | newtron `Device.Cleanup()` |

[^1]: `verify-health` is a single-shot read — it does not poll. Use a `wait` step before `verify-health` if convergence time is needed.

Steps implemented by newtron call newtron's built-in methods on the Device
object. newtest provides the orchestration (which device, what parameters,
pass/fail reporting) but the observation/assertion logic is in newtron.

Steps marked **newtest native** require capabilities newtron doesn't have:
cross-device data plane (ping), or arbitrary SSH commands with output matching.

### 5.2 Verification Tiers

Verification spans four tiers across two owners. newtron provides single-device
primitives; newtest orchestrates them across devices and adds data-plane testing.

| Tier | What | Owner | newtron Method | Failure Mode |
|------|------|-------|---------------|-------------|
| **CONFIG_DB** | Redis entries match ChangeSet | **newtron** | `VerifyChangeSet(cs)` | Hard fail (assertion) |
| **APP_DB / ASIC_DB** | Routes installed by FRR / ASIC | **newtron** | `GetRoute()`, `GetRouteASIC()` | Observation (data) |
| **Operational state** | BGP sessions, interface health | **newtron** | `RunHealthChecks()` | Observation (report) |
| **Cross-device / data plane** | Route propagation, ping | **newtest** | Composes newtron primitives | Topology-dependent |

### 5.3 Platform-Aware Test Skipping

Platforms declare their capabilities in `platforms.json`:

```json
{
  "platforms": {
    "sonic-vs": {
      "hwsku": "Force10-S6000",
      "vm_image": "~/.newtlab/images/sonic-vs.qcow2",
      "vm_nic_driver": "e1000",
      "vm_interface_map": "stride-4",
      "dataplane": ""
    },
    "sonic-vpp": {
      "hwsku": "Force10-S6000",
      "vm_image": "~/.newtlab/images/sonic-vpp.qcow2",
      "vm_nic_driver": "virtio-net-pci",
      "vm_interface_map": "sequential",
      "dataplane": "vpp"
    }
  }
}
```

When `dataplane` is empty, steps that require a data plane (`verify-ping`) are
automatically skipped with a `SKIP` result instead of `FAIL`.

### 5.4 Built-In Scenarios

Scenarios are organized in two ways:

**Standalone scenarios** (`newtest/suites/2node-standalone/`) — independent tests, each with its own deploy/destroy cycle.

**Incremental suites** (`newtest/suites/2node-incremental/`) — ordered tests with dependency chaining (`requires` field). A suite shares a single topology deployment. Scenarios run in topological order; if a dependency fails, dependent scenarios are skipped.

#### 2node-incremental Suite

The `newtest/suites/2node-incremental/` suite contains 31 scenarios that incrementally test all newtron operations on a 2-node (spine1 + leaf1) topology:

| # | Scenario | Requires | What It Tests |
|---|----------|----------|---------------|
| 00 | `boot-ssh` | — | VM boot and SSH connectivity |
| 01 | `provision` | boot-ssh | Full device provisioning |
| 02 | `bgp-converge` | provision | eBGP underlay + iBGP overlay convergence |
| 03 | `route-propagation` | bgp-converge | Loopback route visible on remote device |
| 04 | `interface-set` | provision | Interface property changes (mtu, description, admin-status) |
| 05 | `interface-ip-vrf` | provision | Interface IP and VRF assignment |
| 06 | `vlan-lifecycle` | provision | VLAN create, add member, delete |
| 07 | `vrf-lifecycle` | provision | VRF create, delete |
| 08 | `evpn-setup` | provision | EVPN overlay setup (VTEP + NVO + BGP EVPN) |
| 09 | `evpn-vpn-binding` | provision | IP-VPN + MAC-VPN bind/unbind |
| 10 | `svi-configure` | provision | VLAN interface (SVI) creation |
| 11 | `bgp-loopback-neighbor` | provision | Add/remove loopback BGP peer |
| 12 | `bgp-direct-neighbor` | provision | Add/remove direct eBGP peer on interface |
| 13 | `state-db-port` | provision | STATE_DB port status verification |
| 14 | `apply-baseline` | provision | Configlet baseline application |
| 15 | `device-health` | bgp-converge | Health checks after convergence |
| 16 | `service-transit` | bgp-converge | Transit service with FRR defaults |
| 17 | `ping-loopback` | route-propagation | Data plane ping between loopbacks |
| 18 | `service-l3` | vrf-lifecycle | L3 service apply/verify/remove |
| 19 | `service-l2` | vlan-lifecycle | L2 service apply/verify/remove |
| 20 | `service-remove` | service-l3, service-l2 | Service removal and cleanup verification |
| 21 | `refresh-service` | service-remove | Service refresh preserves binding |
| 22 | `verify-provisioning` | refresh-service | ChangeSet verification after apply |
| 23 | `service-churn` | verify-provisioning | Stress test: 10x apply/remove cycle |
| 24 | `cleanup` | service-churn | Cleanup and verify no orphaned resources |
| 25 | `qos-l3-service` | vrf-lifecycle | 4q-customer QoS: DSCP map, schedulers, port binding, cleanup |
| 26 | `qos-datacenter` | vlan-lifecycle | 8q-datacenter QoS with ECN/WRED profile verification |
| 27 | `vrf-interface-binding` | vrf-lifecycle | VRF interface add/remove |
| 28 | `static-route` | vrf-lifecycle | Static route add/remove in VRF |
| 29 | `vlan-member-remove` | vlan-lifecycle | VLAN member removal |
| 30 | `qos-apply-remove` | provision | QoS policy apply and remove on interface |

**Standalone scenarios** (`newtest/suites/2node-standalone/`) include `evpn-overlay` — an end-to-end overlay test that exercises EVPN setup, VPN binding, and traffic verification in a single scenario.

---

## 6. Workflow

### 6.1 Single Scenario

```bash
newtest start 2node-incremental --scenario boot-ssh
```

Internally:
1. Resolve scenario from suite directory
2. Deploy topology via `EnsureTopology` (reuses if already running)
3. Connect to all devices via newtron
4. Execute steps in order
5. Report results
6. Leave topology running (use `newtest stop` to tear down)

### 6.2 Full Suite

```bash
newtest start 2node-incremental
```

Deploys the topology once, runs all scenarios in dependency order (topological
sort), and skips scenarios whose dependencies failed. The topology stays running
after completion.

### 6.3 Resume Paused Suite

```bash
# Resume a previously paused suite
newtest start 2node-incremental
```

When `start` detects a paused state for the suite, it automatically resumes.
Already-passed scenarios are skipped; execution picks up at the first scenario
that has not yet passed.

### 6.4 Keep Topology for Iteration

```bash
newtest start 2node-incremental --scenario boot-ssh
# topology stays up; modify scenario and re-run:
newtest start 2node-incremental --scenario boot-ssh
# clean up when done:
newtest stop
```

Since `start` always uses `EnsureTopology`, the topology is reused across
successive invocations. This enables fast iteration without waiting for VM boot.

### 6.5 Existing Topology

```bash
# Deploy separately
newtlab deploy -S newtest/topologies/4node/specs/

# Run tests without deploy/destroy (deprecated run command supports --no-deploy)
newtest run 2node-standalone --scenario bgp-underlay --no-deploy

# Clean up when done
newtlab destroy
```

---

## 7. Suite Lifecycle

newtest tracks suite state across process boundaries, enabling pause/resume
and multi-command workflows.

### 7.1 State Machine

```
            start
              │
              ▼
          ┌────────┐      pause       ┌─────────┐
          │running │───────────────▶│ pausing │
          └───┬────┘                  └────┬────┘
              │                            │
              │  (scenario ends)           │  (current scenario ends)
              │                            │
              ▼                            ▼
     ┌──────────────┐              ┌────────┐       start
     │complete/failed│              │ paused │──────────────▶ running
     └──────────────┘              └────────┘
```

| Status | Meaning |
|--------|---------|
| `running` | Suite is actively executing scenarios (PID recorded) |
| `pausing` | Pause requested; will stop after current scenario completes |
| `paused` | Suite stopped between scenarios; topology still deployed |
| `complete` | All scenarios finished; no failures or errors |
| `failed` | Suite finished with failures or errors |
| `aborted` | Runner process died unexpectedly |

### 7.2 State Persistence

State is persisted at `~/.newtron/newtest/<suite>/state.json`. The file
contains:
- Suite metadata (name, directory, topology, platform)
- Runner PID (for liveness checks)
- Suite status
- Per-scenario status, current step, duration, skip reason

State is updated after every scenario start, scenario end, and step start,
enabling real-time progress monitoring via `newtest status`.

### 7.3 Lifecycle Commands

| Command | What It Does |
|---------|-------------|
| `start` | Deploy topology (or reuse via `EnsureTopology`), run scenarios, save state |
| `pause` | Signal running suite to stop after current scenario |
| `stop` | Destroy topology, remove suite state |
| `status` | Show suite progress, per-scenario status, current step |

### 7.4 Concurrency Control

`AcquireLock` prevents concurrent runs of the same suite. It checks if an
existing state file records a live PID (via `kill -0`). If the PID is alive,
`start` refuses to run. If the PID is dead (crash/kill), the lock is
considered stale and a new run proceeds.

---

## 8. Output & Reporting

### 8.1 Console Output

Non-verbose mode shows one line per scenario with dot-padded status:

```
newtest: 31 scenarios, topology: 2node, platform: sonic-vpp

  #     SCENARIO                STEPS
  1     boot-ssh                2
  2     provision               5
  ...

  [1/31]  boot-ssh ............. PASS  (3s)
  [2/31]  provision ............ PASS  (12s)
  [3/31]  bgp-converge ........ PASS  (45s)
  ...

---
newtest: 31 scenarios: 28 passed, 2 failed, 1 skipped  (4m30s)
```

Verbose mode (`-v`) shows per-step detail within each scenario.

### 8.2 Markdown Report

Written to `newtest/.generated/report.md` after each run:

```markdown
# newtest Report — 2026-02-14 10:30:00

| Scenario | Topology | Platform | Result | Duration | Note |
|----------|----------|----------|--------|----------|------|
| boot-ssh | 2node | sonic-vpp | PASS | 3s | |
| provision | 2node | sonic-vpp | PASS | 12s | |
| service-churn | 2node | sonic-vpp | PASS | 25s | 10 iterations |

## Failures

### full-fabric
Step verify-ping (verify-ping): leaf1 → leaf2 ping failed
```

### 8.3 JUnit XML

For CI systems that parse JUnit XML. Each `ScenarioResult` maps to a
`<testsuite>`, each `StepResult` maps to a `<testcase>`.

```bash
newtest start 2node-incremental --junit results.xml
```

---

## 9. Data Plane Verification

### 9.1 Test Host Concept

Data plane tests require endpoints that can generate and receive packets. newtest uses **testhost** devices — Alpine Linux VMs with multiple NICs connected to leaf port channels via newtlink. These hosts run network namespaces for L2 isolation (one per NIC) and provide standard tools (ping, iperf3, tcpdump, hping3) for testing.

```
┌────────────────────────────────────────┐
│  testhost1 (Alpine Linux VM)          │
│  ┌──────────────┬──────────────────┐   │
│  │ ns-eth1      │ ns-eth2          │   │
│  │ (192.168.1.2)│ (192.168.2.2)    │   │
│  └──────┬───────┴──────┬───────────┘   │
│         │              │               │
│      eth1           eth2              │
└─────────┼──────────────┼───────────────┘
          │              │
     newtlink        newtlink
          │              │
   ┌──────┴──────┐  ┌───┴──────┐
   │ leaf1:Po1   │  │ leaf2:Po1│
   └─────────────┘  └──────────┘
```

### 9.2 Step Actions for Host Testing

| Action | Description |
|--------|-------------|
| `host-exec` | Execute a command in a namespace on a host device (namespace = device name) |

**Note:** Namespaces are created by newtlab during topology deployment (infrastructure concern). Test scenarios do not manage namespace lifecycle — they only execute commands within pre-existing namespaces using `host-exec`.

### 9.3 Host-Based Test Suites

The **3node-dataplane** suite validates:
- **L2 VXLAN connectivity** — ping across EVPN MAC-VPN overlay with untagged members
- **L3 VRF routing** — ping across EVPN IP-VPN overlay with routed interfaces
- **ACL filtering** — verify that deny/permit rules drop/pass traffic

Host namespaces are provisioned by newtlab during topology deployment. Tests use `host-exec` to run commands (ping/iperf3/tcpdump) directly within these namespaces without setup or teardown steps.

---

## 10. CLI Reference

```
newtest - E2E testing for newtron

Commands:
  start [suite]        Start or resume a test suite
  pause                Pause after current scenario
  stop                 Destroy topology and clean up state
  status               Show suite run status
  list [suite]         List suites or scenarios
  topologies           List available topologies
  version              Print version information

Global Flags:
  -v, --verbose        Verbose output
```

### 9.1 start

```
newtest start [suite] [flags]

Flags:
  --dir <path>           Directory containing scenario YAML files
  --scenario <name>      Run specific scenario (default: all)
  --topology <name>      Override topology
  --platform <name>      Override platform
  --junit <path>         JUnit XML output path
```

The suite argument can be a name (resolved under `newtest/suites/`) or a
directory path. If a previous run was paused, `start` resumes automatically.

In lifecycle mode (`start`), the topology is deployed via `EnsureTopology`
(reuses if running) and kept running after completion. Use `stop` to tear down.

### 9.2 pause

```
newtest pause [flags]

Flags:
  --dir <path>           Suite directory (auto-detected if omitted)
```

Signals the running suite to stop after the current scenario completes. The
topology remains deployed. The suite can be resumed with `start`.

### 9.3 stop

```
newtest stop [flags]

Flags:
  --dir <path>           Suite directory (auto-detected if omitted)
```

Destroys the deployed topology and removes suite state. Refuses to stop a suite
with a running process — use `pause` first.

### 9.4 status

```
newtest status [flags]

Flags:
  --dir <path>           Suite directory
  --json                 JSON output
```

Without `--dir`, shows all suites with state. With `--dir`, shows detailed
status for a specific suite including per-scenario progress, current step,
and topology liveness.

### 9.5 list

```
newtest list [suite] [flags]

Flags:
  --dir <path>           Directory containing scenario YAML files
```

Without arguments, lists all available suites. With a suite name, lists the
scenarios in that suite in dependency order.

### 9.6 topologies

```
newtest topologies
```

Lists available topologies from `newtest/topologies/`.

### 9.7 version

```
newtest version
```

Prints version and git commit.

### 9.8 Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed (or unknown error) |
| 2 | Infrastructure error (VM boot failure, SSH connection failure) |
