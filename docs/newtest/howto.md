# newtest — HOWTO Guide

newtest is an E2E testing orchestrator for newtron and SONiC. It deploys
VM topologies via newtlab, provisions devices via newtron, and validates
the results.

For the architectural principles behind newtron, newtlab, and newtest, see [Design Principles](../DESIGN_PRINCIPLES.md).

---

## Prerequisites

### Required Tools

```bash
# Build newtron, newtlab, and newtest
go build ./cmd/newtron/
go build ./cmd/newtlab/
go build ./cmd/newtest/
```

### newtlab Must Be Working

newtest depends on newtlab for VM management. Ensure newtlab is set up:

```bash
# QEMU installed
qemu-system-x86_64 --version

# KVM available (recommended)
ls /dev/kvm

# VM images in place
ls ~/.newtlab/images/
```

See `docs/newtlab/howto.md` for newtlab setup details.

---

## Quick Start

```bash
# List available scenarios
newtest list

# Run a single scenario
newtest run -scenario bgp-underlay

# Run all scenarios
newtest run --all
```

---

## Test Topologies

newtest ships with pre-defined topologies in `newtest/topologies/`. These are
static spec directories checked into the repo — no generation needed.

### 2-Node (1 spine + 1 leaf)

```
newtest/topologies/2node/specs/
├── topology.json      # Devices, interfaces, links
├── network.json       # Services, filters, VPNs, regions
├── site.json          # Site topology, route reflectors
├── platforms.json     # Platform definitions with VM settings
└── profiles/
    ├── spine1.json    # newtlab writes ssh_port, console_port after deploy
    └── leaf1.json
```

Good for: basic BGP peering, interface config, service apply/remove,
health checks, configlet application.

### 4-Node (2 spines + 2 leaves)

```
newtest/topologies/4node/specs/
├── topology.json
├── network.json
├── site.json
├── platforms.json
└── profiles/
    ├── spine1.json
    ├── spine2.json
    ├── leaf1.json
    └── leaf2.json
```

Good for: route reflection, ECMP, EVPN, iBGP overlay, shared VRF,
full fabric provisioning.

---

## Test Scenarios

Scenarios are YAML files that define what to test against a deployed topology.
They live in `newtest/suites/` — either `2node-standalone/` (independent tests)
or `2node-incremental/` (dependency-ordered suite).

### Listing Scenarios

```bash
newtest list

Available scenarios:
  bgp-underlay    eBGP underlay sessions (4node, sonic-vpp)
  bgp-overlay     iBGP overlay with route reflection (4node, sonic-vpp)
  service-l3      L3 service apply/remove (2node, sonic-vpp)
  service-irb     IRB service with VXLAN (4node, sonic-vpp)
  service-l2      L2 VLAN extension (2node, sonic-vpp)
  health          Health checks after provisioning (2node, sonic-vpp)
  baseline        Configlet application (2node, sonic-vpp)
  full-fabric     Full fabric: underlay + overlay + services (4node, sonic-vpp)
```

### Scenario Format

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

  - name: verify-health
    action: verify-health
    devices: all
    expect:
      overall: ok
```

### Step Actions

| Action | What It Does | Implemented By |
|--------|-------------|----------------|
| `provision` | Run `newtron provision -d <device> -x` for each device | newtron |
| `verify-provisioning` | Verify CONFIG_DB matches expected state from provisioning | newtron `VerifyChangeSet` |
| `verify-config-db` | Assert specific CONFIG_DB table/key/field values (ad-hoc) | newtron Redis read |
| `verify-state-db` | Assert STATE_DB entries match (polls with timeout) | newtron STATE_DB read |
| `verify-bgp` | Check BGP neighbor state via STATE_DB | newtron `RunHealthChecks` |
| `verify-health` | Run health checks (interfaces, BGP, EVPN, LAG, VXLAN) | newtron `RunHealthChecks` |
| `verify-route` | Check route exists on device with expected next-hops | newtron `GetRoute` / `GetRouteASIC` |
| `verify-ping` | Ping between devices (requires `dataplane: true`) | **newtest native** |
| `apply-service` | Apply a named service to a device interface | newtron |
| `remove-service` | Remove a service from a device interface | newtron |
| `apply-baseline` | Apply a configlet baseline to a device | newtron |
| `ssh-command` | Run arbitrary command via SSH, check output | newtest native |
| `wait` | Pause for a specified duration | newtest native |
| `restart-service` | Restart a SONiC service (e.g., `bgp`, `swss`) | newtron |
| `apply-frr-defaults` | Apply FRR runtime defaults via vtysh | newtron |
| `set-interface` | Set interface property (mtu, description, admin-status, ip, vrf) | newtron |
| `create-vlan` | Create a VLAN | newtron |
| `delete-vlan` | Delete a VLAN | newtron |
| `add-vlan-member` | Add an interface to a VLAN | newtron |
| `create-vrf` | Create a VRF | newtron |
| `delete-vrf` | Delete a VRF | newtron |
| `create-vtep` | Create a VXLAN tunnel endpoint | newtron |
| `delete-vtep` | Delete a VTEP | newtron |
| `map-l2vni` | Map a VLAN to a L2 VNI | newtron |
| `map-l3vni` | Map a VRF to a L3 VNI | newtron |
| `unmap-vni` | Remove a VNI mapping | newtron |
| `configure-svi` | Configure a VLAN interface (SVI) | newtron |
| `bgp-add-neighbor` | Add a BGP neighbor (direct or loopback) | newtron |
| `bgp-remove-neighbor` | Remove a BGP neighbor | newtron |
| `refresh-service` | Refresh a service binding on an interface | newtron |
| `cleanup` | Run device cleanup to remove orphaned resources | newtron |

Most verification steps delegate to newtron's built-in methods (see newtron
HLD §4.9). newtest provides the orchestration — which device, what parameters,
pass/fail reporting. Steps marked **newtest native** require capabilities
newtron doesn't have (cross-device data plane, arbitrary SSH with output
matching).

---

## Running Tests

### Run a Specific Scenario

```bash
newtest run -scenario bgp-underlay
```

This does everything automatically:
1. Deploys the 4-node topology via newtlab
2. Waits for all VMs to boot (SSH ready)
3. Provisions all devices via newtron
4. Waits for protocol convergence
5. Verifies BGP sessions are Established (polls STATE_DB)
6. Verifies CONFIG_DB entries on leaves
7. Runs health checks on all devices
8. Reports results
9. Destroys the topology

### Run All Scenarios

```bash
newtest run --all
```

Runs every scenario in `newtest/suites/2node-standalone/` sequentially. Each gets its own
topology deploy/destroy cycle.

### Override Platform

```bash
# Use sonic-vs instead of the scenario's default
newtest run -scenario bgp-underlay --platform sonic-vs
```

Data plane tests (`verify-ping`) are automatically skipped when the platform
has `dataplane: false` in `platforms.json`.

### Verbose Output

```bash
newtest run -scenario bgp-underlay -v
```

Shows newtron provisioning output, Redis commands, STATE_DB polling progress,
and full verification details.

---

## Keeping Topologies Running

### Inspect After Tests

```bash
newtest run -scenario bgp-underlay --keep
```

The topology stays up after tests complete. You can SSH into devices to
debug:

```bash
newtlab ssh leaf1
newtlab console spine1
```

Clean up when done:

```bash
newtlab destroy
```

### Run Against Existing Topology

If you already have a topology deployed (from newtlab or a previous `--keep`
run):

```bash
# Deploy topology manually
newtlab deploy -S newtest/topologies/4node/specs/

# Run tests without deploy/destroy
newtest run -scenario bgp-underlay --no-deploy

# Iterate: modify scenario, run again
newtest run -scenario bgp-underlay --no-deploy

# Clean up when done
newtlab destroy
```

This is useful for iterating on test scenarios without waiting for VM boot
each time.

---

## Test Output

### Console Output

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

### Test Report

After running scenarios, newtest writes a report to
`newtest/.generated/report.md`:

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

## Verification Tiers

newtest validates at four tiers, delegating to newtron for single-device checks:

| Tier | What | Owner | newtron Method | Failure Mode |
|------|------|-------|---------------|-------------|
| **CONFIG_DB** | Redis entries match ChangeSet | newtron | `VerifyChangeSet` | Hard fail |
| **APP_DB / ASIC_DB** | Routes installed by FRR / ASIC | newtron | `GetRoute`, `GetRouteASIC` | Observation |
| **Operational state** | BGP sessions, interface health | newtron | `RunHealthChecks` | Observation |
| **Cross-device / data plane** | Route propagation, ping | newtest | Composes newtron primitives | Topology-dependent |

When using `sonic-vs` (no dataplane), data-plane tests are automatically
skipped based on the platform's `dataplane: false` in `platforms.json`.

---

## Writing Custom Scenarios

### 1. Choose a Topology

Pick `2node` or `4node` based on what you need to test.

### 2. Create a Scenario File

```yaml
# newtest/suites/2node-standalone/my-test.yaml
name: my-test
description: Test custom L3 service
topology: 2node
platform: sonic-vpp

steps:
  - name: provision
    action: provision
    devices: all

  - name: wait-for-convergence
    action: wait
    duration: 30s

  - name: check-metadata
    action: verify-config-db
    devices: [leaf1]
    table: DEVICE_METADATA
    key: "localhost"
    expect:
      fields:
        hostname: leaf1
        type: LeafRouter

  - name: check-bgp
    action: verify-bgp
    devices: all
    expect:
      state: Established
      timeout: 60s

  - name: check-health
    action: verify-health
    devices: all
    expect:
      overall: ok
```

> **Note:** `verify-health` is a single-shot read — it does not poll. Use a
> `wait` step before `verify-health` if convergence time is needed.

### 3. Run It

```bash
newtest run -scenario my-test
```

### 4. Verify Provisioning Results

For standard provisioning verification, use `verify-provisioning` — it calls
newtron's `VerifyChangeSet` which automatically checks all CONFIG_DB tables
against the ChangeSet produced by provisioning. The ChangeSet uses
last-write-wins accumulation per device — if multiple operations write to the
same table/key, the final value is what gets verified:

```yaml
# Preferred: verify all CONFIG_DB entries from provisioning automatically
- name: verify-all
  action: verify-provisioning
  devices: all
```

For ad-hoc assertions on specific keys (e.g., after an `ssh-command` step, or
to check a key that isn't part of standard provisioning), use `verify-config-db`:

```yaml
# Ad-hoc: assert minimum number of entries in a table
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

# Assert specific field values on a key
- name: check-globals
  action: verify-config-db
  devices: [leaf1]
  table: BGP_GLOBALS
  key: "default"
  expect:
    fields:
      local_asn: "65101"
      router_id: "10.0.0.11"

# Assert a key does NOT exist (after removal)
- name: check-removed
  action: verify-config-db
  devices: [leaf1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet2"
  expect:
    exists: false
```

### 5. Verify STATE_DB with Polling

STATE_DB entries converge asynchronously. Use `verify-state-db` with a
timeout:

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

Or use the `verify-bgp` shorthand which polls BGP_NEIGHBOR state for all
peers:

```yaml
- name: check-bgp
  action: verify-bgp
  devices: all
  expect:
    state: Established
    timeout: 120s
```

### 6. Verify Routes on Remote Devices

Use `verify-route` to check that a route propagated to a remote device. This
calls newtron's `GetRoute` (APP_DB) or `GetRouteASIC` (ASIC_DB) on the target
device and asserts the expected prefix, protocol, and next-hops:

```yaml
# Verify underlay route: leaf1's connected subnet arrived at spine1
- name: check-underlay
  action: verify-route
  devices: [spine1]
  prefix: "10.1.0.0/31"
  vrf: default
  expect:
    protocol: bgp
    nexthop_ip: "10.1.0.1"
    source: app_db
    timeout: 60s

# Verify ASIC programmed the route (not just FRR)
- name: check-asic
  action: verify-route
  devices: [leaf1]
  prefix: "10.0.0.1/32"
  vrf: default
  expect:
    source: asic_db
    timeout: 30s
```

newtest determines the expected values from the topology spec — newtron's
`GetRoute` only reads the device's routing table and returns a
`*device.RouteEntry` (prefix, VRF, protocol, next-hops, source).

### 7. Apply and Remove Services

```yaml
# Apply a service to an interface
- name: apply-l3
  action: apply-service
  devices: [leaf1]
  interface: Ethernet2
  service: customer-l3
  params:
    ip: "192.168.1.1/24"

# Verify it was written
- name: check-binding
  action: verify-config-db
  devices: [leaf1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet2"
  expect:
    fields:
      service_name: customer-l3

# Remove the service
- name: remove-l3
  action: remove-service
  devices: [leaf1]
  interface: Ethernet2

# Verify it was cleaned up
- name: check-removed
  action: verify-config-db
  devices: [leaf1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet2"
  expect:
    exists: false
```

### 8. Apply Baselines

```yaml
- name: apply-evpn-baseline
  action: apply-baseline
  devices: [leaf1]
  configlet: sonic-evpn-leaf
  vars:
    hostname: leaf1
    loopback_ip: "10.0.0.11"
    router_id: "10.0.0.11"
```

### 9. Use SSH Commands for Custom Checks

```yaml
- name: check-vtysh
  action: ssh-command
  devices: [leaf1]
  command: "vtysh -c 'show ip bgp summary'"
  expect:
    contains: "Established"

- name: check-redis
  action: ssh-command
  devices: [leaf1]
  command: "redis-cli -n 4 KEYS 'BGP_NEIGHBOR*'"
  expect:
    contains: "BGP_NEIGHBOR"
```

### 10. Set Interface Properties

Use `set-interface` to change interface attributes. The `params.property` field
dispatches to the appropriate method: `ip` → `SetIP`, `vrf` → `SetVRF`,
anything else → `Set(property, value)`.

```yaml
# Change MTU
- name: set-mtu
  action: set-interface
  devices: [leaf1]
  interface: Ethernet1
  params:
    property: mtu
    value: "9000"

# Set IP address
- name: set-ip
  action: set-interface
  devices: [leaf1]
  interface: Ethernet1
  params:
    property: ip
    value: "192.168.99.1/24"

# Bind interface to VRF
- name: set-vrf
  action: set-interface
  devices: [leaf1]
  interface: Ethernet1
  params:
    property: vrf
    value: Vrf_test
```

### 11. VLAN Operations

```yaml
# Create a VLAN
- name: create-vlan
  action: create-vlan
  devices: [leaf1]
  params:
    vlan_id: 100

# Add an interface as tagged member
- name: add-member
  action: add-vlan-member
  devices: [leaf1]
  params:
    vlan_id: 100
    interface: Ethernet2

# Verify the VLAN was created
- name: verify-vlan
  action: verify-config-db
  devices: [leaf1]
  table: VLAN
  key: "Vlan100"
  expect:
    exists: true

# Delete the VLAN
- name: delete-vlan
  action: delete-vlan
  devices: [leaf1]
  params:
    vlan_id: 100
```

### 12. VRF Operations

```yaml
# Create a VRF
- name: create-vrf
  action: create-vrf
  devices: [leaf1]
  params:
    vrf: Vrf_test

# Verify VRF exists
- name: verify-vrf
  action: verify-config-db
  devices: [leaf1]
  table: VRF
  key: "Vrf_test"
  expect:
    exists: true

# Delete the VRF
- name: delete-vrf
  action: delete-vrf
  devices: [leaf1]
  params:
    vrf: Vrf_test
```

### 13. VXLAN / EVPN Operations

```yaml
# Create VTEP
- name: create-vtep
  action: create-vtep
  devices: [leaf1]
  params:
    source_ip: "10.0.0.11"

# Map L2 VNI (VLAN to VNI)
- name: map-l2vni
  action: map-l2vni
  devices: [leaf1]
  params:
    vlan_id: 200
    vni: 10200

# Map L3 VNI (VRF to VNI)
- name: map-l3vni
  action: map-l3vni
  devices: [leaf1]
  params:
    vrf: Vrf_evpn
    vni: 20001

# Unmap a VNI
- name: unmap-vni
  action: unmap-vni
  devices: [leaf1]
  params:
    vni: 10200

# Configure SVI (VLAN interface)
- name: configure-svi
  action: configure-svi
  devices: [leaf1]
  params:
    vlan_id: 500
    vrf: Vrf_svi
    ip: "10.1.50.1/24"

# Delete VTEP
- name: delete-vtep
  action: delete-vtep
  devices: [leaf1]
```

### 14. BGP Neighbor Operations

```yaml
# Add loopback-based BGP neighbor
- name: add-loopback-peer
  action: bgp-add-neighbor
  devices: [leaf1]
  params:
    neighbor_ip: "10.0.0.99"
    remote_asn: 65099

# Add direct (interface-based) BGP neighbor
- name: add-direct-peer
  action: bgp-add-neighbor
  devices: [leaf1]
  interface: Ethernet1
  params:
    neighbor_ip: "10.1.1.0"
    remote_asn: 65001

# Remove a BGP neighbor
- name: remove-peer
  action: bgp-remove-neighbor
  devices: [leaf1]
  params:
    neighbor_ip: "10.0.0.99"
```

### 15. Service Refresh and Cleanup

```yaml
# Refresh a service (re-apply without remove)
- name: refresh
  action: refresh-service
  devices: [leaf1]
  interface: Ethernet1

# Run cleanup to remove orphaned resources
- name: cleanup
  action: cleanup
  devices: [leaf1]
```

### 16. Incremental Suite Scenarios

Scenarios in a suite directory use `requires` to declare dependencies:

```yaml
# newtest/suites/2node-incremental/07-vrf-lifecycle.yaml
name: vrf-lifecycle
description: Create and delete a VRF via newtron API
topology: 2node
platform: sonic-vpp
requires: [provision]    # won't run until 01-provision passes

steps:
  - name: create-vrf
    action: create-vrf
    devices: [leaf1]
    params:
      vrf: Vrf_test
  # ...
```

Stress testing with `repeat`:

```yaml
name: service-churn
description: Stress test service apply/remove cycles
topology: 2node
platform: sonic-vpp
requires: [verify-provisioning]
repeat: 10    # run all steps 10 times

steps:
  - name: apply-customer-l3
    action: apply-service
    devices: [leaf1]
    interface: Ethernet1
    service: customer-l3
    params:
      ip: "192.168.1.1/24"
  - name: remove-customer-l3
    action: remove-service
    devices: [leaf1]
    interface: Ethernet1
```

Run a suite:

```bash
newtest run --dir newtest/suites/2node-incremental
```

---

## Parallel Provisioning

For topologies with many nodes, provision in parallel:

```bash
newtest run -scenario full-fabric --parallel 4
```

This provisions up to 4 devices simultaneously during `provision` steps.

---

## CI/CD Integration

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed |
| 2 | Infrastructure error (VM boot failure, etc.) |

### GitHub Actions Example

```yaml
- name: Run newtest
  run: |
    newtest run --all
  timeout-minutes: 30

- name: Upload report
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtest-report
    path: newtest/.generated/report.md
```

### JUnit XML Output

For CI systems that parse JUnit:

```bash
newtest run --all --junit newtest/.generated/results.xml
```

---

## Troubleshooting

### Scenario Fails at Deploy

newtlab couldn't start VMs. Check:

```bash
# Is the image available?
ls ~/.newtlab/images/

# Are ports free?
ss -tlnp | grep 40000

# Any leftover VMs?
newtlab status
newtlab destroy --force
```

### Provisioning Fails

newtron couldn't configure a device. Check:

```bash
# Is the VM reachable?
newtlab ssh leaf1

# Run newtron manually with verbose output
newtron provision -S newtest/topologies/2node/specs/ -d leaf1 -x -v
```

### BGP Verification Times Out

BGP sessions may take time to establish after provisioning. Try:

```yaml
# Increase timeout in your scenario:
  - name: check-bgp
    action: verify-bgp
    devices: all
    expect:
      state: Established
      timeout: 180s      # default is 120s
      poll_interval: 5s
```

Or SSH in and check manually:

```bash
newtlab ssh spine1
vtysh -c "show ip bgp summary"
```

### Health Checks Fail

Health checks run 5 built-in checks (interfaces, BGP, EVPN, LAG, VXLAN).
If one fails:

```bash
# Run verbose to see which check failed
newtest run -scenario health -v

# SSH in and inspect
newtlab ssh leaf1
show interfaces status
vtysh -c "show ip bgp summary"
```

### Data Plane Tests Fail

Only `sonic-vpp` images support actual packet forwarding. If using `sonic-vs`:

```bash
# Override platform to skip data plane tests
newtest run -scenario full-fabric --platform sonic-vs
```

### Wrong Interface Names

If CONFIG_DB verification fails on interface entries, check that the
platform's `vm_interface_map` in `platforms.json` matches the SONiC image:
- `sonic-vs`: `stride-4` (Ethernet0, Ethernet4, Ethernet8, ...)
- `sonic-vpp`: `sequential` (Ethernet0, Ethernet1, Ethernet2, ...)

The topology spec's interface names must match the mapping.

---

## Command Reference

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
  --junit <path>         Write JUnit XML results
  -v, --verbose          Verbose output
```
