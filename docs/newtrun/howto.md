# newtrun — HOWTO Guide

newtrun is an E2E testing orchestrator for SONiC network devices. It deploys
VM topologies via newtlab, runs test scenarios against a newtron-server, and
reports results — all driven by declarative YAML.

All device operations go through newtron-server over HTTP. newtrun never
connects to devices directly — it sends requests to the server, which manages
SSH tunnels and Redis connections to the SONiC switches. Host operations
(ping, iperf) use direct SSH to the host VMs.

```
newtrun ──→ newtron-server (HTTP :8080) ──→ SSH tunnel ──→ SONiC Redis
  │                                                          CONFIG_DB
  └──→ host VMs (direct SSH) ──→ network namespace ──→ data plane
```

For architecture and design, see the [HLD](hld.md). For type definitions
and internals, see the [LLD](lld.md).

---

## Table of Contents

1. [Prerequisites & Build](#1-prerequisites--build)
2. [Quick Start](#2-quick-start)
3. [Topologies & Suites](#3-topologies--suites)
4. [Running Tests](#4-running-tests)
5. [Suite Lifecycle](#5-suite-lifecycle)
6. [Writing Scenarios](#6-writing-scenarios)
7. [Step Action Reference](#7-step-action-reference)
8. [Data Plane Tests](#8-data-plane-tests)
9. [CI/CD Integration](#9-cicd-integration)
10. [Troubleshooting](#10-troubleshooting)
11. [Command Reference](#11-command-reference)

---

## 1. Prerequisites & Build

```bash
# Build all three binaries
go build -o bin/newtron-server ./cmd/newtron-server
go build -o bin/newtlab ./cmd/newtlab
go build -o bin/newtrun ./cmd/newtrun
```

newtrun depends on newtlab for VM management and newtron-server for device
operations. Ensure both are set up:

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

## 2. Quick Start

This is the minimum path from zero to a passing test suite.

```bash
# 1. Start newtron-server (must be running before newtrun)
bin/newtron-server --specs newtrun/topologies/2node/specs &

# 2. List available suites
bin/newtrun list

# 3. Run the 2node-primitive suite (deploys topology automatically)
bin/newtrun start 2node-primitive

# 4. Check progress from another terminal
bin/newtrun status

# 5. When done, tear down
bin/newtrun stop
```

The suite deploys a 2-switch topology (`switch1`, `switch2`) running
`sonic-ciscovs`, provisions them through newtron-server, then runs 20
scenarios covering BGP, EVPN, VLANs, VRFs, services, ACLs, QoS, and
PortChannels — all with full teardown and clean-state verification.

---

## 3. Topologies & Suites

newtrun ships with pre-defined topologies in `newtrun/topologies/` and
test suites in `newtrun/suites/`. Use `newtrun topologies` and
`newtrun list` to see what's available.

### Topologies

| Topology | Devices | Description |
|----------|---------|-------------|
| `2node` | 2 switches + 6 hosts | General-purpose: BGP, VLANs, VRFs, services, ACLs, QoS, PortChannels. The workhorse topology. |
| `2node-service` | 2 switches + 8 hosts | Service-focused: end-to-end service provisioning with data plane verification across more host endpoints. |
| `3node` | 2 leaves + 2 hosts | EVPN/VXLAN and multi-hop: L2 bridging across a fabric, L3 inter-subnet routing via asymmetric IRB. |
| `4node` | 2 spines + 2 leaves | Full fabric: ECMP, eBGP overlay, route reflection, shared VRFs. |

Each topology lives at `newtrun/topologies/<name>/specs/` containing
`topology.json`, `network.json`, `platforms.json`, and per-device profiles
in `profiles/`.

### Test Suites

| Suite | Topology | Scenarios | Description |
|-------|----------|-----------|-------------|
| `2node-primitive` | 2node | 20 | Incremental: BGP, EVPN, VLANs, VRFs, services, ACLs, QoS, PortChannels — full teardown and clean-state verification. |
| `2node-service` | 2node-service | 6 | Service lifecycle: provision → health → data plane → deprovision → verify clean. |
| `2node-standalone` | varies | 9 | Independent scenarios (no ordering): baseline, BGP, EVPN, services. Good for testing individual features. |
| `3node-dataplane` | 3node | 6 | Data plane: L3 routing + EVPN L2 IRB across a 2-leaf fabric with host verification. |
| `simple-vrf-host` | 2node | 5 | VRF basics: create VRF, bind interface, set IP, verify host reachability. |

Suites in the `2node-primitive` and `2node-service` directories use
dependency ordering (`requires`/`after`) — scenarios run in the declared
order and skip if prerequisites failed. Standalone suites have independent
scenarios that can run individually with `--scenario`.

---

## 4. Running Tests

Running tests is the most common newtrun operation. newtrun resolves the
suite name to a directory under `newtrun/suites/`, deploys the topology
via newtlab, then runs scenarios in dependency order against newtron-server.

### Run All Scenarios in a Suite

```bash
newtrun start 2node-primitive
```

This deploys the topology (or reuses an existing one), runs all scenarios
in dependency order, and leaves the topology running. The suite name
resolves to `newtrun/suites/2node-primitive/`. You can also pass a full
path with `--dir`:

```bash
newtrun start --dir newtrun/suites/2node-primitive
```

### Run a Single Scenario

```bash
newtrun start 2node-primitive --scenario boot-ssh
```

### Override Platform

```bash
newtrun start 2node-primitive --platform sonic-ciscovs
```

### Override Topology

```bash
newtrun start 2node-service --topology 2node
```

### Server URL

newtrun resolves the server URL in this order: `--server` flag >
`NEWTRON_SERVER` environment variable > settings file > default
(`http://localhost:8080`).

```bash
# Explicit server
newtrun start 2node-primitive --server http://10.1.0.5:8080

# Or via environment
export NEWTRON_SERVER=http://10.1.0.5:8080
newtrun start 2node-primitive
```

### Network ID

The network identifier tells newtron-server which spec set to use.
Resolution order: `--network-id` flag > `NEWTRON_NETWORK_ID` environment
variable > settings file > default.

```bash
newtrun start 2node-primitive --network-id lab1
```

### Verbose Output

```bash
newtrun start 2node-primitive -v
```

Shows per-step results with timing, failure details, and device-level
messages.

### JUnit Output

```bash
newtrun start 2node-primitive --junit results.xml
```

---

## 5. Suite Lifecycle

Suites are stateful — newtrun persists progress at
`~/.newtron/newtrun/<suite>/state.json` so you can pause, resume, and
monitor across terminal sessions.

### Start a Suite

```bash
newtrun start 2node-primitive
```

This:
1. Acquires a lock (prevents concurrent runs of the same suite)
2. Deploys the topology via `EnsureTopology` (reuses if all nodes running)
3. Runs scenarios in dependency order
4. Persists state after each scenario completes
5. Reports results

### Check Progress

From another terminal:

```bash
newtrun status
```

Output:

```
newtrun: 2node-primitive
  topology:  2node (deployed, 2 nodes running)
  platform:  sonic-ciscovs
  status:    running (pid 12345)
  started:   2026-02-14 10:30:00 (5m ago)

  #  SCENARIO              STEPS  STATUS   REQUIRES             DURATION
  1  boot-ssh              2      PASS     —                    3s
  2  loopback              3      PASS     boot-ssh             5s
  3  bridged               15     running  boot-ssh             step 2/15: add-vlan-member
  4  irb                   5      —        bridged              —
  5  routed                8      —        boot-ssh             —

  progress: 2/20 passed
```

Use `--detail` to see per-step status within each scenario:

```bash
newtrun status --detail
```

Use `--monitor` for auto-refreshing display (every 2 seconds, implies
`--detail`):

```bash
newtrun status --monitor
```

Filter by suite name with `--suite`:

```bash
newtrun status --suite 2node
```

### Pause a Running Suite

```bash
newtrun pause
```

The runner finishes the current scenario, then stops. The topology stays
deployed. State is saved as `paused`.

### Resume a Paused Suite

```bash
newtrun start 2node-primitive
```

newtrun detects the paused state and resumes from where it left off.
Already completed scenarios are not re-run.

### Stop and Tear Down

```bash
newtrun stop
```

This destroys the topology and removes all state. The suite must be
paused or completed first — `stop` refuses to kill a running process.

### Full Lifecycle Example

```bash
# Terminal 1: start the suite
newtrun start 2node-primitive

# Terminal 2: check progress
newtrun status --monitor

# Terminal 2: pause after current scenario
newtrun pause

# Terminal 1: runner finishes current scenario and exits
# "paused after 3 scenarios; resume with: newtrun start 2node-primitive"

# Later: resume
newtrun start 2node-primitive

# When done: tear down
newtrun stop
```

---

## 6. Writing Scenarios

This is the framework — the YAML schema and step machinery are the same
whether you're testing a 2-node lab or a 100-node production fabric. The
shipped suites are examples of this framework, not the framework itself.

### 6.1 Scenario Format

A scenario is a YAML file containing metadata and an ordered list of
steps. Here is a real scenario from the 2node-primitive suite:

```yaml
# newtrun/suites/2node-primitive/00-boot-ssh.yaml
name: boot-ssh
description: Verify SSH reachability on both switches (factory boot baseline)
topology: 2node
requires: []

steps:
  - name: ssh-echo-switch1
    action: ssh-command
    devices: [switch1]
    command: "echo ok"
    expect:
      contains: "ok"

  - name: ssh-echo-switch2
    action: ssh-command
    devices: [switch2]
    command: "echo ok"
    expect:
      contains: "ok"
```

**Scenario fields:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Scenario identifier (used for `--scenario` filter and `requires` references) |
| `description` | string | Human-readable purpose |
| `topology` | string | Which topology to deploy (from `newtrun/topologies/`) |
| `platform` | string | Optional. Overridden by `--platform` flag |
| `requires` | list | Hard dependencies — scenario is skipped if any prerequisite failed |
| `after` | list | Soft ordering — run after these scenarios, but don't skip if they failed |
| `requires_features` | list | Platform feature gate — scenario skipped if platform lists feature in `unsupported_features` |
| `repeat` | int | Run all steps N times (stress testing) |
| `steps` | list | Ordered list of Step objects |

### 6.2 Step Structure

Each step has an action (what to do), a device selector (where to do it),
and action-specific fields. Fields are divided into two groups: top-level
fields used directly by the step executor, and the `params` map for
action-specific extras.

**Top-level fields** (used directly by the step executor):

| Field | Used by | Description |
|-------|---------|-------------|
| `name` | all | Step identifier for output |
| `action` | all | Which executor to run (56 available) |
| `devices` | most | Device selector: `all`, `[switch1]`, `[switch1, switch2]` |
| `duration` | wait | How long to wait |
| `table` | verify-config-db, verify-state-db | CONFIG_DB/STATE_DB table name |
| `key` | verify-config-db, verify-state-db | Table key |
| `prefix` | verify-route | Route prefix (e.g., `10.1.0.0/31`) |
| `vrf` | verify-route | VRF name (e.g., `default`) |
| `interface` | apply-service, remove-service, refresh-service, set-interface, add-vlan-member, remove-vlan-member, bind-acl, unbind-acl, remove-ip | Interface name |
| `service` | apply-service, restart-service | Service name from network spec |
| `command` | ssh-command, host-exec | Shell command to run |
| `target` | verify-ping | Target device name |
| `count` | verify-ping | Ping count |
| `vlan_id` | create-vlan, delete-vlan, add-vlan-member, remove-vlan-member, configure-svi, remove-svi, bind-macvpn, unbind-macvpn | VLAN ID (integer) |
| `tagging` | add-vlan-member | `tagged` or `untagged` |
| `expect` | verify-\*, ssh-command, host-exec | Assertion block |

**Params map** (action-specific extras under `params:`):

| Key | Used by | Description |
|-----|---------|-------------|
| `ip` | apply-service, remove-ip | IP address (e.g., `192.168.1.1/24`) |
| `property` | set-interface | Property name (`ip`, `vrf`, `mtu`, etc.) |
| `value` | set-interface | Property value |
| `source_ip` | setup-evpn | VTEP source IP (optional — falls back to profile loopback) |
| `ipvpn` | bind-ipvpn | IP-VPN spec name from network spec |
| `macvpn` | bind-macvpn | MAC-VPN spec name from network spec |
| `qos_policy` | apply-qos | QoS policy name from network spec |
| `next_hop` | add-static-route | Next-hop IP |
| `name` | create-acl-table, delete-acl-table, add-acl-rule, delete-acl-rule, bind-acl, unbind-acl | ACL table name |
| `rule` | add-acl-rule, delete-acl-rule | ACL rule name (e.g., `RULE_10`) |
| `action` | add-acl-rule | ACL rule action (`DROP`, `FORWARD`) |
| `direction` | bind-acl | ACL binding direction (`ingress`, `egress`) |
| `neighbor_ip` | bgp-add-neighbor, bgp-remove-neighbor | BGP peer IP |
| `remote_asn` | bgp-add-neighbor | BGP peer ASN |

Different actions expect different params — use `newtrun actions <action>`
for the definitive parameter reference.

### 6.3 Expect Block

The `expect` block defines assertions for verification and command
actions. Available fields:

| Field | Type | Used by | Description |
|-------|------|---------|-------------|
| `exists` | bool | verify-config-db | Assert key exists (`true`) or does not exist (`false`) |
| `fields` | map | verify-config-db, verify-state-db | Assert field values match (e.g., `hostname: switch1`) |
| `min_entries` | int | verify-config-db | Assert table has at least N entries |
| `timeout` | duration | verify-state-db, verify-bgp, verify-route, verify-ping, host-exec | Poll until condition met or timeout (default: 120s for BGP/state, 60s for routes, 30s for ping) |
| `poll_interval` | duration | verify-state-db, verify-bgp, verify-route | Time between polls (default: 5s) |
| `state` | string | verify-bgp | Expected BGP peer state (default: `Established`) |
| `protocol` | string | verify-route | Expected route protocol (e.g., `bgp`) |
| `nexthop_ip` | string | verify-route | Expected next-hop IP |
| `source` | string | verify-route | Route source: `app_db` (default) or `asic_db` |
| `success_rate` | float | host-exec, verify-ping | Fraction of successful pings (default: 1.0) |
| `contains` | string | ssh-command, host-exec | Regex match on command output |

### 6.4 Dependency Ordering

Scenarios declare dependencies with `requires` (hard) and `after` (soft):

```yaml
# Hard dependency: skip this scenario if "provision" failed
name: verify-health
requires: [provision]

# Soft ordering: run after verify-health, but don't skip if it failed
name: dataplane
requires: [provision]
after: [verify-health]
```

`requires: []` (empty list) means no dependencies — the scenario runs
first in the suite. newtrun topologically sorts scenarios by their
dependency graph.

### 6.5 Stress Testing with Repeat

The `repeat` field runs all steps N times — useful for churn tests that
exercise apply/remove cycles to detect resource leaks or state corruption:

```yaml
name: service-churn
description: Stress test service apply/remove cycles
topology: 2node
requires: [provision]
repeat: 10

steps:
  - name: apply-svc
    action: apply-service
    devices: [switch1]
    interface: Ethernet3
    service: l2-extend

  - name: remove-svc
    action: remove-service
    devices: [switch1]
    interface: Ethernet3

  - name: verify-clean
    action: verify-config-db
    devices: [switch1]
    table: NEWTRON_SERVICE_BINDING
    key: "Ethernet3"
    expect:
      exists: false
```

Each iteration runs all three steps. If any step fails, the scenario
stops — the repeat count is a maximum, not a guarantee.

### 6.6 Feature Gates

The `requires_features` field lets scenarios self-skip on platforms that
don't support certain features:

```yaml
# Only runs if platform supports evpn-vxlan and macvpn
name: evpn-l2-irb
requires_features: [evpn-vxlan, macvpn]
```

The platform's `unsupported_features` list in `platforms.json` controls
which features are unavailable. If any required feature is unsupported,
the scenario reports `SKIP` with a reason.

### 6.7 Discovering Actions

```bash
# List all actions by category
newtrun actions

# Show params, prerequisites, and example YAML for a specific action
newtrun actions provision
newtrun actions verify-config-db
newtrun actions apply-service
```

---

## 7. Step Action Reference

Every action maps to a step executor. Most call newtron-server over HTTP.
Host actions use direct SSH. Use `newtrun actions <action>` for the
definitive parameter reference — the listing below covers categories and
representative examples.

### 7.1 Provisioning & BGP

Actions: `provision`, `configure-loopback`, `remove-loopback`,
`configure-bgp`, `remove-bgp-globals`, `apply-frr-defaults`,
`config-reload`

Provisioning writes the full device composite (from topology specs) to
CONFIG_DB. BGP actions configure underlay routing. `config-reload`
restarts the BGP container to pick up ASN changes and applies VRF table
entries — it replaces the separate `restart-service bgp` approach used in
earlier topologies.

```yaml
# Provision both switches from topology spec
- name: provision-switches
  action: provision
  devices: [switch1, switch2]

# Reload config (restarts BGP, applies VRF table)
- name: reload
  action: config-reload
  devices: [switch1, switch2]

# Write BGP globals from device profile
- name: configure-bgp
  action: configure-bgp
  devices: [switch1, switch2]

# Configure loopback from profile (e.g., 10.0.0.1/32)
- name: configure-loopback
  action: configure-loopback
  devices: all
```

### 7.2 Verification

Actions: `verify-provisioning`, `verify-config-db`, `verify-state-db`,
`verify-bgp`, `verify-health`, `verify-route`, `verify-ping`

Verification actions read device state and assert conditions.
`verify-provisioning` uses the ChangeSet from the most recent provision
to automatically check all CONFIG_DB entries. The others are ad-hoc
assertions on specific tables, keys, or protocols.

**verify-config-db** has three modes — exists check, field match, and
minimum entry count:

```yaml
# Assert a key exists
- name: check-loopback
  action: verify-config-db
  devices: [switch1]
  table: LOOPBACK_INTERFACE
  key: "Loopback0"
  expect:
    exists: true

# Assert specific field values
- name: check-bgp-globals
  action: verify-config-db
  devices: [switch1]
  table: BGP_GLOBALS
  key: "default"
  expect:
    fields:
      local_asn: "65001"
      router_id: "10.0.0.1"

# Assert a key does NOT exist (after removal)
- name: check-removed
  action: verify-config-db
  devices: [switch1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet2"
  expect:
    exists: false

# Assert minimum entries in a table
- name: check-neighbors
  action: verify-config-db
  devices: [switch1]
  table: BGP_NEIGHBOR
  expect:
    min_entries: 2
```

**verify-bgp** polls all BGP sessions until they reach the expected state:

```yaml
- name: check-bgp
  action: verify-bgp
  devices: all
  expect:
    state: Established
    timeout: 120s
    poll_interval: 5s
```

**verify-route** checks the routing table (APP_DB or ASIC_DB):

```yaml
- name: check-underlay-route
  action: verify-route
  devices: [switch1]
  prefix: "10.1.0.0/31"
  vrf: default
  expect:
    protocol: bgp
    source: app_db
    timeout: 60s
```

**verify-health** reads health checks from the device (a single-shot
read, not polling — use a `wait` step before it if convergence time is
needed):

```yaml
- name: wait-convergence
  action: wait
  duration: 30s

- name: check-health
  action: verify-health
  devices: all
```

### 7.3 Service Lifecycle

Actions: `apply-service`, `remove-service`, `refresh-service`

Services are the primary abstraction — they bind a service definition
from the network spec to an interface, creating all necessary VLANs,
VRFs, EVPN mappings, and bindings in one operation.

```yaml
# Apply a service to an interface
- name: apply-svc
  action: apply-service
  devices: [switch1]
  interface: Ethernet3
  service: l2-extend

# Refresh (re-apply without remove — picks up spec changes)
- name: refresh-svc
  action: refresh-service
  devices: [switch1]
  interface: Ethernet3

# Remove the service (reverse of apply)
- name: remove-svc
  action: remove-service
  devices: [switch1]
  interface: Ethernet3

# Verify removal
- name: verify-removed
  action: verify-config-db
  devices: [switch1]
  table: NEWTRON_SERVICE_BINDING
  key: "Ethernet3"
  expect:
    exists: false
```

### 7.4 VLAN

Actions: `create-vlan`, `delete-vlan`, `add-vlan-member`,
`remove-vlan-member`, `configure-svi`, `remove-svi`

Note: `vlan_id` is a **top-level** step field, not under `params`.

```yaml
# Create VLAN 100
- name: create-vlan
  action: create-vlan
  devices: [switch1]
  vlan_id: 100

# Add interface as untagged member
- name: add-member
  action: add-vlan-member
  devices: [switch1]
  vlan_id: 100
  interface: Ethernet1
  tagging: untagged

# Configure SVI (VLAN interface with IP)
- name: configure-svi
  action: configure-svi
  devices: [switch1]
  vlan_id: 100

# Delete VLAN
- name: delete-vlan
  action: delete-vlan
  devices: [switch1]
  vlan_id: 100
```

### 7.5 VRF

Actions: `create-vrf`, `delete-vrf`, `add-vrf-interface`,
`remove-vrf-interface`

```yaml
# Create a VRF
- name: create-vrf
  action: create-vrf
  devices: [switch2]
  vrf: Vrf_local

# Add interface to VRF
- name: add-intf
  action: add-vrf-interface
  devices: [switch2]
  vrf: Vrf_local
  interface: Ethernet1

# Remove interface from VRF
- name: remove-intf
  action: remove-vrf-interface
  devices: [switch2]
  vrf: Vrf_local
  interface: Ethernet1

# Delete VRF
- name: delete-vrf
  action: delete-vrf
  devices: [switch2]
  vrf: Vrf_local
```

### 7.6 EVPN & VPN

Actions: `setup-evpn`, `teardown-evpn`, `bind-ipvpn`, `unbind-ipvpn`,
`bind-macvpn`, `unbind-macvpn`

`setup-evpn` creates the VTEP, NVO, and overlay eBGP sessions with
L2VPN EVPN address-family. The `source_ip` param is optional — if
omitted, it reads the loopback IP from the device profile.

```yaml
# Set up EVPN overlay
- name: setup-evpn
  action: setup-evpn
  devices: [switch1]

# Bind a MAC-VPN to a VLAN (vlan_id is top-level)
- name: bind-macvpn
  action: bind-macvpn
  devices: [switch1]
  vlan_id: 300
  params:
    macvpn: vlan300

# Bind an IP-VPN to a VRF
- name: bind-ipvpn
  action: bind-ipvpn
  devices: [switch1]
  vrf: Vrf_irb
  params:
    ipvpn: evpn-irb-vrf

# Teardown EVPN (reverse of setup-evpn)
- name: teardown-evpn
  action: teardown-evpn
  devices: [switch1, switch2]
```

### 7.7 ACL

Actions: `create-acl-table`, `add-acl-rule`, `delete-acl-rule`,
`delete-acl-table`, `bind-acl`, `unbind-acl`

Full ACL lifecycle — create table, add rules, bind to interface, then
reverse:

```yaml
- name: create-acl
  action: create-acl-table
  devices: [switch1]
  params:
    name: TEST_ACL

- name: add-rule
  action: add-acl-rule
  devices: [switch1]
  params:
    name: TEST_ACL
    rule: RULE_10
    action: DROP

- name: bind-acl
  action: bind-acl
  devices: [switch1]
  interface: Ethernet10
  params:
    name: TEST_ACL
    direction: ingress

- name: unbind-acl
  action: unbind-acl
  devices: [switch1]
  interface: Ethernet10
  params:
    name: TEST_ACL

- name: delete-rule
  action: delete-acl-rule
  devices: [switch1]
  params:
    name: TEST_ACL
    rule: RULE_10

- name: delete-acl
  action: delete-acl-table
  devices: [switch1]
  params:
    name: TEST_ACL
```

### 7.8 QoS

Actions: `apply-qos`, `remove-qos`

```yaml
- name: apply-qos
  action: apply-qos
  devices: [switch1]
  interface: Ethernet11
  params:
    qos_policy: test-4q

- name: remove-qos
  action: remove-qos
  devices: [switch1]
  interface: Ethernet11
```

### 7.9 Interface & PortChannel

Actions: `set-interface`, `remove-ip`, `create-portchannel`,
`delete-portchannel`, `add-portchannel-member`, `remove-portchannel-member`

`set-interface` dispatches based on `params.property`: `ip` calls SetIP,
`vrf` calls SetVRF, anything else calls Set(property, value).

```yaml
# Set IP address
- name: set-ip
  action: set-interface
  devices: [switch1]
  interface: Ethernet0
  params:
    property: ip
    value: "10.1.0.0/31"

# Set MTU
- name: set-mtu
  action: set-interface
  devices: [switch1]
  interface: Ethernet10
  params:
    property: mtu
    value: "9100"

# Remove IP address
- name: remove-ip
  action: remove-ip
  devices: [switch1]
  interface: Ethernet0
  params:
    ip: "10.1.0.0/31"

# Create PortChannel with members
- name: create-lag
  action: create-portchannel
  devices: [switch1]
  params:
    name: PortChannel1
    members: [Ethernet4, Ethernet5]
    min_links: 1

# Delete PortChannel
- name: delete-lag
  action: delete-portchannel
  devices: [switch1]
  params:
    name: PortChannel1
```

### 7.10 Static Routing

Actions: `add-static-route`, `remove-static-route`

```yaml
- name: add-route
  action: add-static-route
  devices: [switch2]
  vrf: Vrf_local
  prefix: "10.99.0.0/24"
  params:
    next_hop: "10.20.1.0"

- name: remove-route
  action: remove-static-route
  devices: [switch2]
  vrf: Vrf_local
  prefix: "10.99.0.0/24"
```

### 7.11 BGP Neighbors

Actions: `bgp-add-neighbor`, `bgp-remove-neighbor`

Add or remove individual BGP peers. Useful for testing peering changes
without full reprovisioning. The `interface` field is optional — if
provided, the neighbor is added as a direct (interface-based) peer;
if omitted, as a loopback-based peer.

```yaml
# Add a loopback-based BGP neighbor
- name: add-loopback-peer
  action: bgp-add-neighbor
  devices: [switch1]
  params:
    neighbor_ip: "10.0.0.99"
    remote_asn: 65099

# Add a direct (interface-based) BGP neighbor
- name: add-direct-peer
  action: bgp-add-neighbor
  devices: [switch1]
  interface: Ethernet1
  params:
    neighbor_ip: "10.1.1.0"
    remote_asn: 65001

# Remove a BGP neighbor
- name: remove-peer
  action: bgp-remove-neighbor
  devices: [switch1]
  params:
    neighbor_ip: "10.0.0.99"
```

### 7.12 Host & Utility

Actions: `host-exec`, `ssh-command`, `wait`, `cleanup`,
`restart-service`

```yaml
# Run a command on a host device (in its network namespace)
- name: ping-test
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8

# Run a command on a switch via SSH
- name: check-vtysh
  action: ssh-command
  devices: [switch1]
  command: "vtysh -c 'show ip bgp summary'"
  expect:
    contains: "Established"

# Wait for convergence
- name: wait-convergence
  action: wait
  duration: 30s

# Run cleanup to remove orphaned resources
- name: cleanup
  action: cleanup
  devices: [switch1, switch2]

# Restart a SONiC service (e.g., bgp)
- name: restart-bgp
  action: restart-service
  devices: all
  service: bgp
```

---

## 8. Data Plane Tests

Data plane tests verify that packets actually traverse the fabric — not
just that CONFIG_DB was written correctly. They require host endpoints
that can generate and receive traffic.

### Host Devices

Host devices are first-class topology devices deployed by newtlab. Each
host runs in its own network namespace on a shared QEMU VM (VM
coalescing). newtlab creates the namespaces at deploy time — test
scenarios don't need to manage namespace lifecycle.

Multiple hosts may share a single QEMU VM. For example, the 2node
topology coalesces host1-host6 across two host VMs (the 2node-service
topology has host1-host8). Each host's network namespace provides
isolation.

### host-exec

`host-exec` runs commands inside a host's network namespace via direct
SSH. The namespace is automatically set to match the device name:

```yaml
- name: ping-across-fabric
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8
```

Compound commands (semicolons, pipes) work correctly — the executor
wraps the command in `sh -c` to ensure the entire pipeline runs inside
the namespace.

The `expect` block supports two modes:
- `success_rate`: parses ping output for packet loss (e.g., `0.8` means
  80% of pings must succeed)
- `contains`: regex match on combined stdout+stderr

Without `expect`, the step checks the exit code only.

### verify-ping

`verify-ping` is a higher-level action that resolves the target device's
IP from DeviceInfo and runs a ping test:

```yaml
- name: ping-host2
  action: verify-ping
  devices: [host1]
  target: host2
  count: 5
  expect:
    success_rate: 0.8
    timeout: 30s
```

### Platform Requirements

Data plane tests require a platform with a non-empty `dataplane` field
in `platforms.json`:

```json
{
  "sonic-ciscovs": {
    "dataplane": "ciscovs",
    ...
  }
}
```

If `dataplane` is empty, host-related actions automatically report
`SKIP` instead of failing. This lets the same suite run on platforms
with and without data plane support.

### Example: L2 Bridging Test

From the 2node-primitive suite — creates a VLAN, adds untagged members
on a single switch, and verifies L2 connectivity between two hosts:

```yaml
# Create VLAN and add untagged members
- name: create-vlan100
  action: create-vlan
  devices: [switch1]
  vlan_id: 100

- name: add-host1-port
  action: add-vlan-member
  devices: [switch1]
  vlan_id: 100
  interface: Ethernet1
  tagging: untagged

- name: add-host3-port
  action: add-vlan-member
  devices: [switch1]
  vlan_id: 100
  interface: Ethernet3
  tagging: untagged

# Verify L2 connectivity
- name: host1-ping-host3
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8
```

---

## 9. CI/CD Integration

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed |
| 2 | Infrastructure error (VM boot failure, SSH unreachable, etc.) |

### Console Output

Normal mode shows scenario-level results:

```
newtrun: 20 scenarios, topology: 2node, platform: sonic-ciscovs

  [1/20]  boot-ssh .................. PASS  (3s)
  [2/20]  loopback .................. PASS  (5s)
  [3/20]  bridged ................... PASS  (18s)
  ...
  [20/20] verify-clean .............. PASS  (12s)

---
newtrun: 20 scenarios: 20 passed  (3m45s)
```

Verbose mode (`-v`) shows per-step results within each scenario:

```
  [3/20]  bridged
          [1/15] create-vlan100 ......... PASS  (1s)
          [2/15] add-host1-port ......... PASS  (<1s)
          ...
          [15/15] host3-ping-host1 ...... PASS  (3s)
          PASS  (18s)
```

### Markdown Report

After a suite completes, newtrun writes a report to
`newtrun/.generated/report.md` with a summary table and failure/skip
details.

### JUnit XML

```bash
newtrun start 2node-primitive --junit results.xml
```

### GitHub Actions Example

```yaml
- name: Run newtrun
  run: |
    bin/newtrun start 2node-primitive --junit results.xml
  timeout-minutes: 30

- name: Upload JUnit results
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtrun-results
    path: results.xml

- name: Upload report
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtrun-report
    path: newtrun/.generated/report.md
```

---

## 10. Troubleshooting

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

### newtron-server Not Reachable

If steps fail with connection errors, the server may not be running or
the URL is wrong:

```bash
# Is the server running?
pgrep -f newtron-server

# Is the URL correct?
curl http://localhost:8080/health

# Start it manually
bin/newtron-server --specs newtrun/topologies/2node/specs &

# Override the URL
newtrun start 2node-primitive --server http://localhost:8080
```

### Provisioning Fails

newtron-server couldn't configure a device. Check:

```bash
# Is the VM reachable through the server?
curl http://localhost:8080/api/v1/nodes/switch1/health

# SSH to the device directly for debugging
newtlab ssh switch1
```

### BGP Verification Times Out

BGP sessions may take time to establish after provisioning. Try:

```yaml
# Increase timeout in your scenario
- name: check-bgp
  action: verify-bgp
  devices: all
  expect:
    state: Established
    timeout: 180s
    poll_interval: 5s
```

Or SSH in and check manually:

```bash
newtlab ssh switch1
vtysh -c "show ip bgp summary"
```

### Health Checks Fail

Health checks run 5 built-in checks (interfaces, BGP, EVPN, LAG,
VXLAN). If one fails:

```bash
# Run verbose to see which check failed
newtrun start 2node-primitive --scenario boot-ssh -v

# SSH in and inspect
newtlab ssh switch1
show interfaces status
vtysh -c "show ip bgp summary"
```

### Data Plane Tests Fail

Only platforms with `dataplane` set in `platforms.json` support actual
packet forwarding. Check:

```bash
# Verify the platform has dataplane support
cat newtrun/topologies/2node/specs/platforms.json | grep dataplane
```

Host-related actions auto-skip on platforms without data plane support,
so failures indicate a real connectivity problem. SSH to the host to
debug:

```bash
newtlab ssh host1
ip addr show
ping -c 1 <target-ip>
```

### Wrong Interface Names

If CONFIG_DB verification fails on interface entries, check that the
platform's `vm_interface_map` in `platforms.json` matches the SONiC
image:
- `sonic-ciscovs`: `sequential` (Ethernet0, Ethernet1, Ethernet2, ...)

The topology spec's interface names must match the mapping.

### Suite Already Running

If you see `suite X already running (pid Y)` but no runner is active:

```bash
# Check if the process is actually alive
ps -p <pid>

# If not, the state is stale — stop and retry
newtrun stop
newtrun start <suite>
```

---

## 11. Command Reference

### `newtrun start [suite]`

Deploy topology (if needed), run scenarios, leave topology running.

```
newtrun start 2node-primitive                        # all scenarios
newtrun start 2node-primitive --scenario boot-ssh    # single scenario
newtrun start --dir path/to/suite                    # explicit path
```

If a previous run was paused, `start` resumes from where it left off.

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (alternative to positional arg) |
| `--scenario <name>` | Run a single scenario (default: all) |
| `--topology <name>` | Override topology |
| `--platform <name>` | Override platform |
| `--server <url>` | newtron-server URL (env: `NEWTRON_SERVER`) |
| `--network-id <id>` | Network identifier (env: `NEWTRON_NETWORK_ID`) |
| `--junit <path>` | Write JUnit XML results |
| `-v, --verbose` | Verbose output |

### `newtrun pause`

Signal the running suite to stop after the current scenario completes.
The topology stays deployed and state is saved as `paused`.

```
newtrun pause
newtrun pause --dir <path>
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (auto-detected if omitted) |

### `newtrun stop`

Destroy the topology and remove suite state. Refuses if the suite has a
running process — use `newtrun pause` first.

```
newtrun stop
newtrun stop --dir <path>
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (auto-detected if omitted) |

### `newtrun status`

Show suite run status. Without flags, shows all suites with state.

```
newtrun status                    # all suites
newtrun status --dir <path>       # specific suite
newtrun status --suite 2node      # filter by name
newtrun status --detail           # per-step status
newtrun status --monitor          # auto-refresh (every 2s, implies --detail)
newtrun status --json             # machine-readable output
```

| Flag | Short | Description |
|------|-------|-------------|
| `--dir <path>` | | Suite directory |
| `--suite <name>` | `-s` | Filter suites by name (substring, case-insensitive) |
| `--detail` | `-d` | Show per-step timing and status |
| `--monitor` | `-m` | Auto-refresh every 2s (implies `--detail`) |
| `--json` | | JSON output |

### `newtrun list [suite]`

Without arguments, lists all available test suites. With a suite name,
lists the scenarios in that suite with dependency order.

```
newtrun list                       # show all suites
newtrun list 2node-primitive       # show scenarios in suite
newtrun list --dir path/to/suite   # explicit path
```

| Flag | Description |
|------|-------------|
| `--dir <path>` | Suite directory (alternative to positional arg) |

### `newtrun topologies`

List available topologies with device and link counts.

```
newtrun topologies
```

No flags.

### `newtrun actions [action]`

List all step actions organized by category. With an action name, shows
detailed parameter info and example YAML.

```
newtrun actions                    # list all 56 actions by category
newtrun actions provision          # show detail for one action
```

No flags.

### `newtrun version`

Print version information.

```
newtrun version
```

No flags.
