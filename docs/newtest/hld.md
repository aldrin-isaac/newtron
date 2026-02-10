# newtest — High-Level Design

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

## 1. Purpose

newtest is an E2E testing orchestrator for newtron and SONiC. It tests
two things: that newtron's automation produces correct device state, and
that SONiC software on each device behaves correctly in its role (spine,
leaf, etc.). It uses newtlab to deploy VM topologies, runs newtron against
them, and validates results.

newtest is one orchestrator built on top of newtron and newtlab — not the
only one. Other orchestrators could be built for different purposes
(production deployment, CI/CD pipelines, compliance auditing). newtron's
observation primitives return structured data so that any orchestrator
can consume them.

```
┌──────────────────────────────────────────────────────────────────┐
│                            newtest                                │
│                                                                   │
│  1. Deploy topology        newtlab deploy -S specs/                │
│  2. Provision devices      newtron provision -S specs/ -d X -x   │
│  3. Validate results       CONFIG_DB, STATE_DB, data plane       │
│  4. Report pass/fail                                              │
│  5. Tear down              newtlab destroy                          │
└──────────────────────────────────────────────────────────────────┘
         │                              │
         ▼                              ▼
┌─────────────────┐          ┌─────────────────┐
│     newtlab       │          │    newtron      │
│ Deploy/manage   │          │ Provision       │
│ QEMU VMs        │          │ CONFIG_DB       │
└─────────────────┘          └─────────────────┘
```

The Runner holds a `*network.Network` object (not individual device references). Devices are accessed via `r.Network.GetDevice(name)`.

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
exclusively through newtron's primitives. See newtron HLD §4.9 and §13.

---

## 3. Directory Structure

```
newtron/
├── cmd/
│   ├── newtron/          # Device provisioning CLI
│   ├── newtlab/            # VM topology management CLI
│   └── newtest/          # E2E testing CLI
├── pkg/
│   ├── network/          # newtron core (Device, Interface, CompositeBuilder,
│   │                     #   TopologyProvisioner)
│   ├── spec/             # Shared spec types
│   ├── device/           # Device connection layer (SSH tunnel, Redis)
│   ├── health/           # Health checker (interfaces, BGP, EVPN, LAG, VXLAN)
│   ├── audit/            # Audit logger (FileLogger, event filtering)
│   ├── configlet/        # Configlet loading and variable resolution
│   ├── newtlab/            # newtlab core library
│   └── newtest/          # newtest core library (scenario parser, runner,
│                         #   verifiers, report generator)
├── newtest/              # E2E test assets
│   ├── topologies/
│   │   ├── 2node/
│   │   │   └── specs/
│   │   │       ├── topology.json
│   │   │       ├── network.json
│   │   │       ├── site.json
│   │   │       ├── platforms.json
│   │   │       └── profiles/
│   │   │           ├── spine1.json
│   │   │           └── leaf1.json
│   │   └── 4node/
│   │       └── specs/
│   │           └── ...
│   ├── scenarios/        # Standalone scenario definitions
│   │   └── *.yaml
│   ├── suites/           # Incremental test suites (dependency-ordered)
│   │   └── 2node-incremental/
│   │       ├── 00-boot-ssh.yaml
│   │       ├── 01-provision.yaml
│   │       ├── ...
│   │       └── 24-cleanup.yaml
│   ├── images/           # VM images or symlinks
│   └── .generated/       # Runtime output (gitignored)
│       └── report.md
├── configlets/           # Baseline config templates (shared with newtron)
│   ├── sonic-baseline.json
│   ├── sonic-evpn.json
│   ├── sonic-evpn-leaf.json
│   ├── sonic-evpn-spine.json
│   ├── sonic-acl-copp.json
│   └── sonic-qos-8q.json
└── docs/
    ├── newtlab/
    └── newtest/
```

### 3.1 Directory Layout

Each topology is self-contained with its own spec directory:

| Path | Purpose |
|------|---------|
| `topologies/*/specs/` | newtron spec dirs per topology |
| `scenarios/` | Standalone scenario YAML files |
| `suites/*/` | Incremental test suites with dependency ordering |
| `.generated/` | Runtime output (reports, logs) |

---

## 4. Test Topologies

### 4.1 2-Node (1 spine + 1 leaf)

```
spine1 ── Ethernet0 ─── Ethernet0 ── leaf1
```

Tests: basic BGP peering, interface configuration, service apply/remove,
health checks, configlet application, CONFIG_DB writes.

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
- `network.json` — services, filters, VPNs, regions (newtron reads)
- `site.json` — site topology, route reflectors (newtron reads)
- `platforms.json` — platform definitions with VM settings (newtlab reads)
- `profiles/*.json` — per-device settings (newtlab writes ports, newtron reads)

---

## 5. Test Scenarios

A scenario defines what to test against a deployed topology:

```yaml
# newtest/scenarios/bgp-underlay.yaml
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
| `add-vlan-member` | Add port to a VLAN as tagged/untagged member | newtron `Device.AddVLANMember()` |
| `create-vrf` | Create a VRF | newtron `Device.CreateVRF()` |
| `delete-vrf` | Delete a VRF | newtron `Device.DeleteVRF()` |
| `create-vtep` | Create a VXLAN tunnel endpoint (VTEP) | newtron `Device.CreateVTEP()` |
| `delete-vtep` | Delete a VTEP | newtron `Device.DeleteVTEP()` |
| `map-l2vni` | Map a VLAN to a L2 VNI via VXLAN | newtron `Device.MapL2VNI()` |
| `map-l3vni` | Map a VRF to a L3 VNI via VXLAN | newtron `Device.MapL3VNI()` |
| `unmap-vni` | Remove a VNI mapping | newtron `Device.UnmapVNI()` |
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

### 5.2 verify-config-db Detail

The `verify-config-db` action supports multiple assertion styles:

```yaml
# Assert minimum entry count in a table
- name: check-neighbors
  action: verify-config-db
  devices: [leaf1]
  table: BGP_NEIGHBOR
  expect:
    min_entries: 2

# Assert a specific key exists
- name: check-loopback
  action: verify-config-db
  devices: [leaf1]
  table: LOOPBACK_INTERFACE
  key: "Loopback0|10.0.0.11/32"
  expect:
    exists: true

# Assert specific field values
- name: check-metadata
  action: verify-config-db
  devices: [leaf1]
  table: DEVICE_METADATA
  key: "localhost"
  expect:
    fields:
      hostname: leaf1
      type: LeafRouter

# Assert a key does NOT exist (after removal)
- name: check-removed
  action: verify-config-db
  devices: [leaf1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet2"
  expect:
    exists: false
```

### 5.3 verify-state-db Detail

STATE_DB verification polls with a timeout since state converges
asynchronously:

```yaml
- name: check-bgp-state
  action: verify-state-db
  devices: [leaf1]
  table: BGP_NEIGHBOR
  key: "default|10.0.0.1"
  expect:
    fields:
      state: Established
    timeout: 120s
    poll_interval: 5s
```

### 5.4 verify-provisioning Detail

The `verify-provisioning` action calls newtron's `VerifyChangeSet` method,
which re-reads CONFIG_DB and diffs against the ChangeSet produced by
provisioning. This verifies all CONFIG_DB tables automatically — no need to
manually specify table/key/field assertions:

```yaml
# Verify entire provisioning result (preferred for standard provisioning)
- name: verify-all-config
  action: verify-provisioning
  devices: all
```

Use `verify-config-db` (§5.2) for ad-hoc assertions on specific keys —
e.g., after an `ssh-command` step or to check a key that isn't part of a
standard provisioning ChangeSet.

### 5.5 verify-route Detail

The `verify-route` action uses newtron's `GetRoute` or `GetRouteASIC`
primitives to check that a specific prefix exists in a device's routing table
with expected attributes. These methods return a `*device.RouteEntry` (from
`pkg/device/verify.go`) containing prefix, VRF, protocol, next-hops, and
source (APP_DB or ASIC_DB):

```yaml
# Verify underlay route: leaf1's subnet arrived at spine1 via BGP
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

# Verify ASIC-level route installation
- name: verify-asic-route
  action: verify-route
  devices: [leaf1]
  prefix: "10.0.0.1/32"
  vrf: default
  expect:
    source: asic_db
    timeout: 30s
```

`verify-route` requires topology-wide context — newtest determines which
device to query and what next-hop to expect based on the topology spec.
newtron's `GetRoute` provides the single-device read; newtest provides the
assertion logic.

### 5.6 Built-In Scenarios

Scenarios are organized in two ways:

**Standalone scenarios** (`newtest/scenarios/`) — independent tests, each with its own deploy/destroy cycle.

**Incremental suites** (`newtest/suites/`) — ordered tests with dependency chaining (`requires` field). A suite shares a single topology deployment. Scenarios run in topological order; if a dependency fails, dependent scenarios are skipped.

#### 2node-incremental Suite

The `newtest/suites/2node-incremental/` suite contains 25 scenarios that incrementally test all newtron operations on a 2-node (spine1 + leaf1) topology:

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
| 08 | `vtep-lifecycle` | provision | VTEP create, delete |
| 09 | `evpn-vni-mapping` | provision | L2VNI + L3VNI map/unmap with VTEP |
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

Run the suite with:

```bash
newtest run --dir newtest/suites/2node-incremental
```

---

## 6. Verification Tiers

Verification spans four tiers across two owners. newtron provides single-device
primitives; newtest orchestrates them across devices and adds data-plane testing.

| Tier | What | Owner | newtron Method | Failure Mode |
|------|------|-------|---------------|-------------|
| **CONFIG_DB** | Redis entries match ChangeSet | **newtron** | `VerifyChangeSet(cs)` | Hard fail (assertion) |
| **APP_DB / ASIC_DB** | Routes installed by FRR / ASIC | **newtron** | `GetRoute()`, `GetRouteASIC()` | Observation (data) |
| **Operational state** | BGP sessions, interface health | **newtron** | `RunHealthChecks()` | Observation (report) |
| **Cross-device / data plane** | Route propagation, ping | **newtest** | Composes newtron primitives | Topology-dependent |

### 6.1 What newtest Adds Beyond newtron

newtest's value is in orchestration — deciding what gets applied where,
with what parameters, in what order:

1. **Multi-device orchestration** — Run `VerifyChangeSet` or `RunHealthChecks`
   on all devices in the topology, aggregate results, report "3/4 passed"
2. **Multi-interface orchestration** — Apply different services to different
   interfaces on the same device, with different parameters. newtron applies
   one service to one interface; newtest decides which combinations to test.
3. **Cross-device route assertions** — Connect to spine1 via newtron, call
   `GetRoute("default", "10.1.0.0/31")`, assert next-hop matches leaf1's IP
   from the topology spec. newtron provides the read; newtest provides the
   expected value.
4. **Data-plane testing** — Ping between VMs through the fabric. newtron has
   no concept of inter-device packet forwarding.
5. **Scenario sequencing** — Provision, wait, verify, apply service, verify
   again, remove service, verify removal. The step orchestration is newtest's
   job.
6. **Platform-aware skipping** — Skip data-plane tests when the platform has
   `dataplane: false`. This is a test-framework concern.

### 6.2 Platform-Aware Test Skipping

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

When `dataplane: false`, steps that require a data plane (`verify-ping`) are
automatically skipped with a "SKIP" result instead of "FAIL".

---

## 7. Workflow

### 7.1 Run a Specific Scenario

```bash
newtest run -scenario bgp-underlay
```

Internally:
1. Read scenario → determine topology (4node) and platform (sonic-vpp)
2. `newtlab deploy -S newtest/topologies/4node/specs/`
3. Wait for all VMs to boot (SSH ready)
4. Execute steps in order:
   - `newtron provision -S specs/ -d spine1 -x`
   - `newtron provision -S specs/ -d spine2 -x`
   - `newtron provision -S specs/ -d leaf1 -x`
   - `newtron provision -S specs/ -d leaf2 -x`
   - Wait 30s for convergence
   - Verify provisioning on all devices (newtron `VerifyChangeSet`)
   - Verify underlay routes propagated (newtron `GetRoute` on spines)
   - Run health checks on all devices (newtron `RunHealthChecks`)
5. Report results
6. `newtlab destroy`

### 7.2 Run All Scenarios

```bash
newtest run --all
```

### 7.2a Run an Incremental Suite

```bash
newtest run --dir newtest/suites/2node-incremental
```

Suite mode deploys the topology once, runs scenarios in dependency order,
and skips scenarios whose dependencies failed.

### 7.3 Keep Topology Running

```bash
newtest run -scenario bgp-underlay --keep
# Topology stays up after tests for manual inspection
# Clean up later:
newtlab destroy
```

### 7.4 Run Against Existing Topology

```bash
# Deploy separately
newtlab deploy -S newtest/topologies/4node/specs/

# Run tests without deploy/destroy
newtest run -scenario bgp-underlay --no-deploy

# Clean up when done
newtlab destroy
```

---

## 8. Output

```
$ newtest run -scenario bgp-underlay

newtest: bgp-underlay (4node topology, sonic-vpp)

Deploying topology...
  ✓ spine1 (SSH :40000)
  ✓ spine2 (SSH :40001)
  ✓ leaf1 (SSH :40002)
  ✓ leaf2 (SSH :40003)

Running steps...
  [1/5] provision-all
    ✓ spine1 provisioned (3.2s)
    ✓ spine2 provisioned (2.9s)
    ✓ leaf1 provisioned (3.1s)
    ✓ leaf2 provisioned (3.0s)

  [2/5] wait-convergence
    ✓ 30s elapsed

  [3/5] verify-provisioning
    ✓ spine1: 24/24 CONFIG_DB entries verified
    ✓ spine2: 24/24 CONFIG_DB entries verified
    ✓ leaf1: 31/31 CONFIG_DB entries verified
    ✓ leaf2: 31/31 CONFIG_DB entries verified

  [4/5] verify-underlay-route
    ✓ spine1: 10.1.0.0/31 via 10.1.0.1 (bgp, APP_DB, polled 3 times)

  [5/5] verify-health
    ✓ spine1: overall ok (5 checks passed)
    ✓ spine2: overall ok (5 checks passed)
    ✓ leaf1: overall ok (5 checks passed)
    ✓ leaf2: overall ok (5 checks passed)

Destroying topology...
  ✓ All VMs stopped

PASS: bgp-underlay (5/5 steps passed, 68s)
```

---

## 9. Test Report

newtest writes a report to `newtest/.generated/report.md`:

```markdown
# newtest Report — 2026-02-05 10:30:00

| Scenario | Topology | Platform | Result | Duration |
|----------|----------|----------|--------|----------|
| bgp-underlay | 4node | sonic-vpp | PASS | 68s |
| bgp-overlay | 4node | sonic-vpp | PASS | 72s |
| service-l3 | 2node | sonic-vpp | PASS | 45s |
| service-irb | 4node | sonic-vpp | PASS | 55s |
| service-l2 | 2node | sonic-vpp | PASS | 38s |
| health | 2node | sonic-vpp | PASS | 30s |
| baseline | 2node | sonic-vpp | PASS | 35s |
| full-fabric | 4node | sonic-vpp | FAIL | 140s |

## Failures

### full-fabric
Step 7 (verify-ping): leaf1 → leaf2 ping failed
  Expected: 5/5 packets received
  Got: 0/5 packets received

## Skipped

### full-fabric (when run with --platform sonic-vs)
Step 7 (verify-ping): SKIP — platform sonic-vs has dataplane: false
```

---

## 10. CLI

```
newtest - E2E testing for newtron

Commands:
  newtest run                      Run test scenarios
  newtest list                     List available scenarios
  newtest topologies               List available topologies

Run Options:
  -scenario <name>       Run specific scenario
  --all                  Run all scenarios
  --dir <path>           Run incremental suite from directory
  --topology <name>      Override topology (default: from scenario)
  --platform <name>      Override platform (default: from scenario)
  --keep                 Don't destroy topology after tests
  --no-deploy            Skip deploy (use existing topology)
  --parallel <n>         Provision n devices in parallel
  -v, --verbose          Verbose output
```

---

## 11. Legacy E2E Learnings

The legacy Go-based E2E test suite (`test/e2e/`, `internal/testutil/`) has been removed.
Patterns and SONiC-specific knowledge from those tests are captured in
`docs/newtest/e2e-learnings.md` for reference when implementing newtest scenarios.

---

## 12. Implementation Phases

### Phase 1: Core
- Scenario YAML parser
- Deploy/provision/destroy lifecycle via newtlab
- CONFIG_DB verification (`verify-config-db`)
- CLI: `run`, `list`
- Pre-defined 2-node and 4-node topologies with full specs
- `apply-service` and `remove-service` step actions
- `apply-baseline` step action

### Phase 2: State Verification
- STATE_DB polling with timeout (`verify-state-db`)
- BGP state verification (`verify-bgp`)
- Health check verification (`verify-health`)
- Platform-aware test skipping (`dataplane: true/false`)

### Phase 3: Data Plane and Polish
- Data plane ping verification (`verify-ping`, VPP only)
- `--all` for running all scenarios
- Test report generation (`newtest/.generated/report.md`)
- `--keep` and `--no-deploy` modes
- Parallel provisioning (`--parallel`)

### Phase 4: CI/CD
- Exit codes for CI integration (0 = pass, 1 = fail, 2 = infra error)
- JUnit XML output (`--junit`)
- GitHub Actions workflow example
