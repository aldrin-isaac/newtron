# newtest — High-Level Design

For the architectural principles behind newtron, vmlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

## 1. Purpose

newtest is an E2E testing orchestrator for newtron and SONiC. It tests
two things: that newtron's automation produces correct device state, and
that SONiC software on each device behaves correctly in its role (spine,
leaf, etc.). It uses vmlab to deploy VM topologies, runs newtron against
them, and validates results.

newtest is one orchestrator built on top of newtron and vmlab — not the
only one. Other orchestrators could be built for different purposes
(production deployment, CI/CD pipelines, compliance auditing). newtron's
observation primitives return structured data so that any orchestrator
can consume them.

```
┌──────────────────────────────────────────────────────────────────┐
│                            newtest                                │
│                                                                   │
│  1. Deploy topology        vmlab deploy -S specs/                │
│  2. Provision devices      newtron provision -S specs/ -d X -x   │
│  3. Validate results       CONFIG_DB, STATE_DB, data plane       │
│  4. Report pass/fail                                              │
│  5. Tear down              vmlab destroy                          │
└──────────────────────────────────────────────────────────────────┘
         │                              │
         ▼                              ▼
┌─────────────────┐          ┌─────────────────┐
│     vmlab       │          │    newtron      │
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
| **vmlab** | Realize VM topologies: deploy QEMU VMs from newtron's topology.json, wire socket links across servers | topology.json, platforms.json, QEMU |
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
│   ├── vmlab/            # VM topology management CLI
│   └── newtest/          # E2E testing CLI
├── pkg/
│   ├── network/          # newtron core (Device, Interface, CompositeBuilder,
│   │                     #   TopologyProvisioner)
│   ├── spec/             # Shared spec types
│   ├── device/           # Device connection layer (SSH tunnel, Redis)
│   ├── health/           # Health checker (interfaces, BGP, EVPN, LAG, VXLAN)
│   ├── audit/            # Audit logger (FileLogger, event filtering)
│   ├── configlet/        # Configlet loading and variable resolution
│   ├── vmlab/            # vmlab core library
│   └── newtest/          # newtest core library (scenario parser, runner,
│                         #   verifiers, report generator)
├── newtest/              # E2E test assets (replaces testlab/)
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
│   │           ├── topology.json
│   │           ├── network.json
│   │           ├── site.json
│   │           ├── platforms.json
│   │           └── profiles/
│   │               ├── spine1.json
│   │               ├── spine2.json
│   │               ├── leaf1.json
│   │               └── leaf2.json
│   ├── scenarios/        # Test scenario definitions
│   │   ├── bgp-underlay.yaml
│   │   ├── bgp-overlay.yaml
│   │   ├── service-l3.yaml
│   │   ├── service-irb.yaml
│   │   ├── health.yaml
│   │   └── full-fabric.yaml
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
    ├── vmlab/
    └── newtest/
```

### 3.1 Migration from testlab/

The existing `testlab/` directory contains containerlab-based test
infrastructure (topologies, specs, seed data, generated artifacts). newtest
replaces this:

| Current (testlab/) | Target (newtest/) | Notes |
|--------------------|--------------------|-------|
| `topologies/*.yml` (containerlab YAML) | `topologies/*/specs/` (newtron spec dirs) | Static spec dirs, no generation |
| `specs/` (hand-written) | Embedded in each topology dir | Each topology is self-contained |
| `seed/` (configdb.json, statedb.json) | Not needed | Provisioning generates config |
| `.generated/` (labgen output) | `.generated/` (runtime output only) | Reports, not specs |

The `testlab/` directory and `cmd/labgen/` are retained during the transition
period. Once all E2E tests have migrated to newtest scenarios, they can be
removed.

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
- `topology.json` — devices, interfaces, links (vmlab + newtron read)
- `network.json` — services, filters, VPNs, regions (newtron reads)
- `site.json` — site topology, route reflectors (newtron reads)
- `platforms.json` — platform definitions with VM settings (vmlab reads)
- `profiles/*.json` — per-device settings (vmlab writes ports, newtron reads)

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
    device: spine1
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
| `verify-config-db` | Assert specific CONFIG_DB table/key/field values (ad-hoc) | newtron `VerifyChangeSet` or direct Redis read |
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
  device: spine1
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
  device: leaf1
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

| Scenario | Topology | What It Tests |
|----------|----------|---------------|
| `bgp-underlay` | 4node | eBGP sessions, underlay routes, redistribute connected |
| `bgp-overlay` | 4node | iBGP sessions, route reflection, L2VPN EVPN AF |
| `service-l3` | 2node | L3 service apply/remove, per-interface VRF, ACL filters |
| `service-irb` | 4node | IRB service, shared VRF, VXLAN mapping, anycast gateway |
| `service-l2` | 2node | L2 VLAN extension, VXLAN tunnel map |
| `health` | 2node | Health checks pass after provisioning |
| `baseline` | 2node | Configlet application, variable resolution |
| `full-fabric` | 4node | End-to-end: underlay + overlay + services + health + data plane |

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
      "vm_image": "~/.vmlab/images/sonic-vs.qcow2",
      "vm_nic_driver": "e1000",
      "vm_interface_map": "stride-4",
      "dataplane": false
    },
    "sonic-vpp": {
      "hwsku": "Force10-S6000",
      "vm_image": "~/.vmlab/images/sonic-vpp.qcow2",
      "vm_nic_driver": "virtio-net-pci",
      "vm_interface_map": "sequential",
      "dataplane": true
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
2. `vmlab deploy -S newtest/topologies/4node/specs/`
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
6. `vmlab destroy`

### 7.2 Run All Scenarios

```bash
newtest run --all
```

### 7.3 Keep Topology Running

```bash
newtest run -scenario bgp-underlay --keep
# Topology stays up after tests for manual inspection
# Clean up later:
vmlab destroy
```

### 7.4 Run Against Existing Topology

```bash
# Deploy separately
vmlab deploy -S newtest/topologies/4node/specs/

# Run tests without deploy/destroy
newtest run -scenario bgp-underlay --no-deploy

# Clean up when done
vmlab destroy
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
  --topology <name>      Override topology (default: from scenario)
  --platform <name>      Override platform (default: from scenario)
  --keep                 Don't destroy topology after tests
  --no-deploy            Skip deploy (use existing topology)
  --parallel <n>         Provision n devices in parallel
  -v, --verbose          Verbose output
```

---

## 11. Relationship to Existing E2E Tests

newtron has a mature Go-based E2E test suite in `test/e2e/` with 11 test
files covering:

| Test File | Coverage |
|-----------|----------|
| `provisioning_test.go` | TopologyProvisioner: validate, generate composite, deliver |
| `service_test.go` | L2, L3, IRB service apply/remove, shared VRF, filter ACLs |
| `bgp_test.go` | BGP globals, route redistribution, route maps, BGP networks |
| `health_test.go` | Health checker: all checks, per-type, after config changes |
| `audit_test.go` | Audit logger: creation, filtering, event types |
| `baseline_test.go` | Configlet loading, resolution, application |
| `operations_test.go` | VLAN, LAG, interface operations |
| `connectivity_test.go` | Device connectivity and Redis access |
| `multidevice_test.go` | Cross-device operations |
| `dataplane_test.go` | L2/L3 forwarding via server containers |

These tests use containerlab (requires root) and Docker (for SONiC containers
and server containers). They rely on the rich test helper library in
`internal/testutil/lab.go` which provides:
- SSH tunnel pool for Redis access through containerlab nodes
- CONFIG_DB/STATE_DB assertion helpers (`AssertConfigDBEntry`,
  `AssertConfigDBEntryExists`, `AssertConfigDBEntryAbsent`)
- STATE_DB polling (`PollStateDB`, `WaitForASICVLAN`)
- Device connection helpers (`LabConnectedDevice`, `LabDevice`)
- Cleanup infrastructure (`ResetLabBaseline`, `LabCleanupChanges`)
- Server container management (`ServerPing`, `ServerConfigureInterface`)

### 11.1 How newtest Relates

newtest replaces the containerlab + Docker infrastructure with vmlab + QEMU
while preserving the same verification patterns:

| Aspect | Go E2E tests (test/e2e/) | newtest |
|--------|-------------------------|---------|
| **VM management** | containerlab (root required) | vmlab (no root) |
| **SONiC image** | Docker containers | QEMU VMs (VS, VPP, vendor) |
| **Test definition** | Go code | Declarative YAML scenarios |
| **Verification** | Go assert helpers | Same patterns, YAML-driven |
| **Data plane** | Server containers + ping | VPP forwarding + ping |
| **Config assertions** | `AssertConfigDBEntry()` (test helper) | `verify-provisioning` → newtron `VerifyChangeSet` |
| **Route verification** | `PollStateDB()` (test helper) | `verify-route` → newtron `GetRoute` |
| **Health checks** | `dev.RunHealthChecks()` | `verify-health` → newtron `RunHealthChecks` |
| **Audience** | Developers (`go test -tags e2e`) | Developers + CI/CD |

### 11.2 Migration Path

The Go E2E tests and newtest coexist during the transition:

1. **Phase 1**: newtest runs alongside Go E2E tests. Both test suites are valid.
2. **Phase 2**: New tests are written as newtest scenarios. Go E2E tests are
   maintained but not expanded.
3. **Phase 3**: Go E2E tests are migrated to newtest scenarios one by one.
4. **Phase 4**: `testlab/`, `cmd/labgen/`, and `test/e2e/` are removed.

---

## 12. Implementation Phases

### Phase 1: Core
- Scenario YAML parser
- Deploy/provision/destroy lifecycle via vmlab
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
