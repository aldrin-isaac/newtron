# newtest — HOWTO Guide

newtest is an E2E testing orchestrator for newtron and SONiC. It deploys
VM topologies via newtlab, provisions devices via newtron, and validates
the results.

For architecture and design, see the [HLD](hld.md). For type definitions
and internals, see the [LLD](lld.md).

---

## Prerequisites & Build

### Required Tools

```bash
# Build all three binaries
go build -o bin/newtron ./cmd/newtron
go build -o bin/newtlab ./cmd/newtlab
go build -o bin/newtest ./cmd/newtest
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
# List available suites
newtest list

# Run all scenarios in a suite
newtest start 2node-incremental

# Run a single scenario from a suite
newtest start 2node-incremental --scenario boot-ssh

# Check progress
newtest status

# Pause after current scenario
newtest pause

# Resume
newtest start 2node-incremental

# Tear down when done
newtest stop
```

---

## Test Topologies

newtest ships with pre-defined topologies in `newtest/topologies/`. These are
static spec directories checked into the repo — no generation needed.

### 2-Node (1 spine + 1 leaf)

```
newtest/topologies/2node/specs/
├── topology.json      # Devices, interfaces, links
├── network.json       # Services, filters, VPNs, zones
├── platforms.json     # Platform definitions with VM settings
└── profiles/
    ├── spine1.json
    └── leaf1.json
```

Good for: basic BGP peering, interface config, service apply/remove,
health checks, baseline application.

### 4-Node (2 spines + 2 leaves)

```
newtest/topologies/4node/specs/
├── topology.json
├── network.json
├── platforms.json
└── profiles/
    ├── spine1.json
    ├── spine2.json
    ├── leaf1.json
    └── leaf2.json
```

Good for: route reflection, ECMP, EVPN, iBGP overlay, shared VRF,
full fabric provisioning.

See [HLD §4](hld.md) for topology diagrams and spec file details.

---

## Writing Scenarios

Scenarios are YAML files that define what to test against a deployed topology.
They live in `newtest/suites/` — either `2node-standalone/` (independent tests)
or `2node-incremental/` (dependency-ordered suite).

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

For the full list of 38 step actions, see [HLD §5](hld.md).

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
newtest start 2node-standalone --scenario my-test
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
dispatches to the appropriate method: `ip` -> `SetIP`, `vrf` -> `SetVRF`,
anything else -> `Set(property, value)`.

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

### 12. PortChannel (LAG) Operations

```yaml
# Create a PortChannel with members
- name: create-lag
  action: create-portchannel
  devices: [leaf1]
  params:
    name: PortChannel100
    members: [Ethernet1, Ethernet2]
    min_links: 1

# Add a member to existing PortChannel
- name: add-member
  action: add-portchannel-member
  devices: [leaf1]
  params:
    name: PortChannel100
    member: Ethernet3

# Remove a member from a PortChannel
- name: remove-member
  action: remove-portchannel-member
  devices: [leaf1]
  params:
    name: PortChannel100
    member: Ethernet1

# Delete the PortChannel
- name: delete-lag
  action: delete-portchannel
  devices: [leaf1]
  params:
    name: PortChannel100
```

### 13. VRF Operations

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

### 14. EVPN and VPN Operations

```yaml
# Set up EVPN overlay (VTEP + NVO + BGP EVPN in one step)
- name: setup-evpn
  action: setup-evpn
  devices: [leaf1]
  params:
    source_ip: "10.0.0.11"    # optional, defaults to loopback IP

# Bind an IP-VPN to a VRF (looks up VPN definition from network spec)
- name: bind-ipvpn
  action: bind-ipvpn
  devices: [leaf1]
  params:
    vrf: Vrf_customer
    ipvpn: customer-ipvpn

# Unbind IP-VPN from a VRF
- name: unbind-ipvpn
  action: unbind-ipvpn
  devices: [leaf1]
  params:
    vrf: Vrf_customer

# Bind a MAC-VPN to a VLAN (looks up VPN definition from network spec)
- name: bind-macvpn
  action: bind-macvpn
  devices: [leaf1]
  params:
    vlan_id: 200
    macvpn: customer-macvpn

# Unbind MAC-VPN from a VLAN
- name: unbind-macvpn
  action: unbind-macvpn
  devices: [leaf1]
  params:
    vlan_id: 200

# Configure SVI (VLAN interface)
- name: configure-svi
  action: configure-svi
  devices: [leaf1]
  params:
    vlan_id: 500
    vrf: Vrf_svi
    ip: "10.1.50.1/24"
```

### 14a. VRF Interface Binding

```yaml
# Add an interface to a VRF
- name: add-vrf-intf
  action: add-vrf-interface
  devices: [leaf1]
  params:
    vrf: Vrf_customer
    interface: Ethernet2

# Remove an interface from a VRF
- name: remove-vrf-intf
  action: remove-vrf-interface
  devices: [leaf1]
  params:
    vrf: Vrf_customer
    interface: Ethernet2
```

### 14b. Static Routes

```yaml
# Add a static route
- name: add-route
  action: add-static-route
  devices: [leaf1]
  params:
    vrf: Vrf_customer
    prefix: "10.99.0.0/24"
    next_hop: "192.168.1.254"
    metric: 100    # optional, defaults to 0

# Remove a static route
- name: remove-route
  action: remove-static-route
  devices: [leaf1]
  params:
    vrf: Vrf_customer
    prefix: "10.99.0.0/24"
```

### 14c. VLAN Member Removal

```yaml
# Remove an interface from a VLAN
- name: remove-member
  action: remove-vlan-member
  devices: [leaf1]
  params:
    vlan_id: 100
    interface: Ethernet2
```

### 14d. QoS Operations

```yaml
# Apply a QoS policy to an interface (looks up policy from network spec)
- name: apply-qos
  action: apply-qos
  devices: [leaf1]
  params:
    interface: Ethernet1
    qos_policy: 4q-customer

# Remove QoS policy from an interface
- name: remove-qos
  action: remove-qos
  devices: [leaf1]
  params:
    interface: Ethernet1
```

### 15. BGP Neighbor Operations

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

### 16. Service Refresh and Cleanup

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

### 17. Incremental Suite Scenarios

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

---

## Running Tests

### Run All Scenarios in a Suite

```bash
newtest start 2node-incremental
```

This deploys the topology via newtlab (or reuses an existing one), runs all
scenarios in dependency order, and leaves the topology running. Scenarios
with `requires` dependencies are skipped if their prerequisites failed.

The suite name resolves to `newtest/suites/2node-incremental/`. You can also
pass a full path with `--dir`:

```bash
newtest start --dir newtest/suites/2node-incremental
```

### Run a Specific Scenario

```bash
newtest start 2node-incremental --scenario boot-ssh
```

### Override Platform

```bash
# Use sonic-vs instead of the scenario's default
newtest start 2node-incremental --platform sonic-vs
```

Data plane tests (`verify-ping`) are automatically skipped when the platform
has `dataplane: ""` in `platforms.json`.

### Override Topology

```bash
newtest start 2node-incremental --topology 4node
```

### Verbose Output

```bash
newtest start 2node-incremental -v
```

Shows per-step results with timing, failure details, and device-level messages.

### JUnit Output

```bash
newtest start 2node-incremental --junit results.xml
```

---

## Suite Lifecycle

newtest manages suite runs as stateful lifecycles. State is persisted at
`~/.newtron/newtest/<suite>/state.json` so you can pause, resume, and monitor
suites across terminal sessions.

### Start a Suite

```bash
newtest start 2node-incremental
```

This:
1. Acquires a lock (prevents concurrent runs of the same suite)
2. Deploys the topology via `EnsureTopology` (reuses if already running)
3. Runs scenarios in dependency order
4. Persists state after each scenario completes
5. Reports results

### Check Progress

From another terminal:

```bash
newtest status
```

Output:

```
newtest: 2node-incremental
  topology:  2node (deployed, 2 nodes running)
  platform:  sonic-vpp
  status:    running (pid 12345)
  started:   2026-02-14 10:30:00 (5m ago)

  #  SCENARIO              STATUS   REQUIRES  DURATION
  1  boot-ssh              PASS     —         3s
  2  provision             PASS     boot-ssh  12s
  3  verify-provisioning   running  provision step 2/3: verify-health
  4  bgp-underlay          —        provision —
  5  service-lifecycle     —        provision —

  progress: 2/5 passed
```

### Pause a Running Suite

```bash
newtest pause
```

The runner finishes the current scenario, then stops. The topology stays
deployed. State is saved as `paused`.

### Resume a Paused Suite

```bash
newtest start 2node-incremental
```

newtest detects the paused state and resumes from where it left off. Already
completed scenarios are not re-run.

### Stop and Tear Down

```bash
newtest stop
```

This destroys the topology and removes all state. The suite must be paused
or completed first — `stop` refuses to kill a running process.

### Full Lifecycle Example

```bash
# Terminal 1: start the suite
newtest start 2node-incremental

# Terminal 2: check progress
newtest status

# Terminal 2: pause after current scenario
newtest pause

# Terminal 1: runner finishes current scenario and exits
# "paused after 3/5 scenarios; resume with: newtest start 2node-incremental"

# Later: resume
newtest start 2node-incremental

# When done: tear down
newtest stop
```

---

## Test Output & Reports

### Console Output (Normal)

```
newtest: 5 scenarios, topology: 2node, platform: sonic-vpp

  #     SCENARIO              STEPS
  1     boot-ssh              2
  2     provision             3
  3     verify-provisioning   4
  4     bgp-underlay          5
  5     service-lifecycle     6

  [1/5]  boot-ssh .................. PASS  (3s)
  [2/5]  provision ................. PASS  (12s)
  [3/5]  verify-provisioning ....... PASS  (8s)
  [4/5]  bgp-underlay .............. PASS  (25s)
  [5/5]  service-lifecycle ......... FAIL  (18s)

---
newtest: 5 scenarios: 4 passed, 1 failed  (1m06s)

  FAILED:
    [5]  service-lifecycle
         step "check-binding" (verify-config-db): leaf1: key not found
```

### Console Output (Verbose)

With `-v`, each step within a scenario is shown:

```
  [3/5]  verify-provisioning
          [1/4] verify-spine1 ........ PASS  (2s)
          [2/4] verify-leaf1 ......... PASS  (2s)
          [3/4] verify-health ........ PASS  (3s)
          [4/4] verify-bgp ........... PASS  (1s)
          PASS  (8s)
```

### Markdown Report

After a suite completes, newtest writes a report to
`newtest/.generated/report.md` with a summary table and failure/skip details.

### JUnit XML

```bash
newtest start 2node-incremental --junit results.xml
```

Produces a JUnit XML file compatible with CI systems (GitHub Actions,
Jenkins, GitLab CI).

---

## Writing Data Plane Tests

Data plane tests require host endpoints that can generate and receive traffic. newtest uses **host** devices (Alpine Linux VMs) that are provisioned by newtlab during topology deployment. Each host device runs in its own network namespace, eliminating the need for runtime namespace management in test scenarios.

### Host Execution Model

Host namespaces are created by newtlab during deploy — not by test scenarios. Each host device name corresponds to its network namespace. Tests use `host-exec` to run commands directly within these namespaces.

Use `host-exec` to run commands in a host's namespace. The `devices` field specifies the host device, and the namespace is automatically set to match the device name:

```yaml
# Ping across VXLAN overlay from host1
- name: ping-host2
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.1.100.20"
  expect:
    success_rate: 0.8
```

The `expect` field can use `success_rate` (fraction of successful pings) or `contains` (regex pattern match on output):

```yaml
# Verify iperf3 throughput exceeds 1 Gbps
- name: check-bandwidth
  action: host-exec
  devices: [host1]
  command: "iperf3 -c 10.1.100.20 -t 5"
  expect:
    contains: "\\d\\.\\d+ Gbits/sec"
    timeout: 15s
```

### Example: L2 VXLAN Test

```yaml
name: l2-vxlan-ping
description: Test L2 VXLAN connectivity across EVPN overlay
topology: 3node-dataplane
platform: sonic-vpp

steps:
  # Provision fabric
  - name: provision-all
    action: provision
    devices: [spine1, leaf1, leaf2]

  - name: wait-convergence
    action: wait
    duration: 45s

  # Test connectivity (hosts already provisioned by newtlab)
  - name: ping-host1-to-host2
    action: host-exec
    devices: [host1]
    command: "ping -c 5 -W 2 10.1.100.20"
    expect:
      success_rate: 0.8
      timeout: 15s

  - name: ping-host2-to-host1
    action: host-exec
    devices: [host2]
    command: "ping -c 5 -W 2 10.1.100.10"
    expect:
      success_rate: 0.8
      timeout: 15s
```

**Key points:**
- No `setup-host-endpoint` or `teardown-host-endpoint` actions needed
- Host devices are first-class topology devices deployed by newtlab
- Namespace lifecycle is infrastructure concern, not test concern
- Tests simply specify which host to run commands on via `devices: [hostN]`

### Platform Requirements

Data plane tests require a platform with `dataplane` set in `platforms.json`:

```json
{
  "sonic-vpp": {
    "dataplane": "vpp",
    "..."
  }
}
```

If `dataplane` is empty (e.g., `sonic-vs`), host-related step actions are automatically skipped with a `SKIP` result instead of failing.

---

## CI/CD Integration

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed |
| 2 | Infrastructure error (VM boot failure, SSH unreachable, etc.) |

### GitHub Actions Example

```yaml
- name: Run newtest
  run: |
    newtest start 2node-incremental --junit results.xml
  timeout-minutes: 30

- name: Upload JUnit results
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtest-results
    path: results.xml

- name: Upload report
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtest-report
    path: newtest/.generated/report.md
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
newtest start 2node-standalone --scenario health -v

# SSH in and inspect
newtlab ssh leaf1
show interfaces status
vtysh -c "show ip bgp summary"
```

### Data Plane Tests Fail

Only `sonic-vpp` images support actual packet forwarding. If using `sonic-vs`:

```bash
# Override platform to skip data plane tests
newtest start 2node-incremental --platform sonic-vs
```

### Wrong Interface Names

If CONFIG_DB verification fails on interface entries, check that the
platform's `vm_interface_map` in `platforms.json` matches the SONiC image:
- `sonic-vs`: `stride-4` (Ethernet0, Ethernet4, Ethernet8, ...)
- `sonic-vpp`: `sequential` (Ethernet0, Ethernet1, Ethernet2, ...)

The topology spec's interface names must match the mapping.

### Suite Already Running

If you see `suite X already running (pid Y)` but no runner is active:

```bash
# Check if the process is actually alive
ps -p <pid>

# If not, the state is stale — stop and retry
newtest stop
newtest start <suite>
```

---

## Command Reference

### `newtest start [suite]`

Deploy topology (if needed), run scenarios, leave topology running.

```
newtest start 2node-incremental                        # all scenarios
newtest start 2node-incremental --scenario boot-ssh    # single scenario
newtest start --dir path/to/suite                      # explicit path
```

If a previous run was paused, `start` resumes from where it left off.

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (alternative to positional arg) |
| `--scenario <name>` | Run a single scenario (default: all) |
| `--topology <name>` | Override topology |
| `--platform <name>` | Override platform |
| `--junit <path>` | Write JUnit XML results |
| `-v, --verbose` | Verbose output |

### `newtest pause`

Signal the running suite to stop after the current scenario completes.
The topology stays deployed and state is saved as `paused`.

```
newtest pause
newtest pause --dir <path>
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (auto-detected if omitted) |

### `newtest stop`

Destroy the topology and remove suite state. Refuses if the suite has a
running process — use `newtest pause` first.

```
newtest stop
newtest stop --dir <path>
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (auto-detected if omitted) |

### `newtest status`

Show suite run status. Without `--dir`, shows all suites with state.

```
newtest status                 # all suites
newtest status --dir <path>    # specific suite
newtest status --json          # machine-readable output
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory |
| `--json` | JSON output |

### `newtest list [suite]`

Without arguments, lists all available test suites. With a suite name,
lists the scenarios in that suite with dependency order.

```
newtest list                       # show all suites
newtest list 2node-incremental     # show scenarios in suite
newtest list --dir path/to/suite   # explicit path
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (alternative to positional arg) |

### `newtest topologies`

List available topologies with device and link counts.

```
newtest topologies
```

No flags.

### `newtest version`

Print version information.

```
newtest version
```

No flags.
