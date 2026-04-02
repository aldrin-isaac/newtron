# newtrun — HOWTO Guide

newtrun is an E2E testing orchestrator for SONiC network devices. It deploys
VM topologies via newtlab, runs test scenarios against a newtron-server, and
reports results — all driven by declarative YAML.

All device operations go through newtron-server over HTTP. newtrun never
connects to devices directly — it sends requests to the server, which manages
SSH tunnels and Redis connections to the SONiC switches. Host operations
(ping, iperf) use direct SSH to the host VMs.

```
┌────────────────┐     ┌──────────────┐     ┌─────────────┐     ┌────────────┐
│                │     │              │     │             │     │            │
│    newtrun     │     │   host VMs   │     │   network   │     │ data plane │
│                │     │ (direct SSH) │     │  namespace  │     │            │
│                │ ──▶ │              │ ──▶ │             │ ──▶ │            │
└────────────────┘     └──────────────┘     └─────────────┘     └────────────┘
  │
  │ HTTP
  ▼
┌────────────────┐     ┌──────────────┐     ┌─────────────┐
│                │     │              │     │             │
│ newtron-server │     │  SSH tunnel  │     │ SONiC Redis │
│  (HTTP :8080)  │     │              │     │  CONFIG_DB  │
│                │ ──▶ │              │ ──▶ │             │
└────────────────┘     └──────────────┘     └─────────────┘
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
bin/newtron-server --spec-dir newtrun/topologies/2node-vs/specs &

# 2. List available suites
bin/newtrun list

# 3. Run the 2node-vs-primitive suite (deploys topology automatically)
bin/newtrun start 2node-vs-primitive

# 4. Check progress from another terminal
bin/newtrun status

# 5. When done, tear down
bin/newtrun stop
```

The suite deploys a 2-switch topology (`switch1`, `switch2`) running
`sonic-vs`, provisions them through newtron-server, then runs 21
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
| `1node-vs` | 1 switch | Single switch (sonic-vs). Minimal topology for basic operations. |
| `2node-ngdp` | 2 switches + 6 hosts | General-purpose CiscoVS: BGP, VLANs, VRFs, services, ACLs, QoS, PortChannels. |
| `2node-ngdp-service` | 2 switches + 8 hosts | Service-focused CiscoVS: end-to-end service provisioning with data plane verification across more host endpoints. |
| `2node-vs` | 2 switches + 6 hosts | General-purpose sonic-vs: same test coverage as 2node-ngdp on community SONiC. |
| `2node-vs-service` | 2 switches + 8 hosts | Service-focused sonic-vs: service provisioning, drift detection, zombie intent testing. |
| `3node-ngdp` | 1 spine + 2 leaves + 2 hosts | EVPN/VXLAN and multi-hop: L2 bridging across a fabric, L3 inter-subnet routing via asymmetric IRB. |
| `4node-ngdp` | 2 spines + 2 leaves | Full fabric: ECMP, eBGP overlay, route reflection, shared VRFs. |

Each topology lives at `newtrun/topologies/<name>/specs/` containing
`topology.json`, `network.json`, `platforms.json`, and per-device profiles
in `profiles/`.

### Test Suites

| Suite | Topology | Scenarios | Description |
|-------|----------|-----------|-------------|
| `1node-vs-basic` | 1node-vs | 4 | Single-switch basics: service apply/remove, VLAN/VRF lifecycle, clean-state verification. |
| `2node-ngdp-primitive` | 2node-ngdp | 21 | Incremental CiscoVS: BGP, EVPN, VLANs, VRFs, services, ACLs, QoS, PortChannels — full teardown and clean-state verification. |
| `2node-ngdp-service` | 2node-ngdp-service | 6 | Service lifecycle on CiscoVS: provision, health, data plane, deprovision, verify clean. |
| `2node-vs-primitive` | 2node-vs | 21 | Incremental sonic-vs: same coverage as 2node-ngdp-primitive on community SONiC. |
| `2node-vs-service` | 2node-vs-service | 6 | Service lifecycle on sonic-vs: provision, health, data plane, deprovision, verify clean. |
| `2node-vs-drift` | 2node-vs-service | 7 | Drift detection: inject CONFIG_DB changes, detect drift (missing/extra/modified), reprovision to fix. |
| `2node-vs-zombie` | 2node-vs-service | 8 | Zombie intent: inject zombie state, verify write operations are blocked, resolve zombie, verify unblocked. |
| `3node-ngdp-dataplane` | 3node-ngdp | 8 | Data plane: L3 routing + EVPN L2 bridged + IRB across a 2-leaf fabric with host verification. |
| `simple-vrf-host` | 2node-ngdp | 4 | VRF basics: create VRF, bind interface, set IP, verify host reachability. |

Suites use dependency ordering (`requires`/`after`) — scenarios run in
the declared order and skip if prerequisites failed.

---

## 4. Running Tests

Running tests is the most common newtrun operation. newtrun resolves the
suite name to a directory under `newtrun/suites/`, deploys the topology
via newtlab, then runs scenarios in dependency order against newtron-server.

### Run All Scenarios in a Suite

```bash
newtrun start 2node-vs-primitive
```

This deploys the topology (or reuses an existing one), runs all scenarios
in dependency order, and leaves the topology running. The suite name
resolves to `newtrun/suites/2node-vs-primitive/`. You can also pass a full
path with `--dir`:

```bash
newtrun start --dir newtrun/suites/2node-vs-primitive
```

### Run a Single Scenario

```bash
newtrun start 2node-vs-primitive --scenario boot-ssh
```

### Override Platform

```bash
newtrun start 2node-ngdp-primitive --platform sonic-ciscovs
```

### Override Topology

```bash
newtrun start 2node-ngdp-service --topology 2node-ngdp
```

### Server URL

newtrun resolves the server URL in this order: `--server` flag >
`NEWTRON_SERVER` environment variable > settings file > default
(`http://localhost:8080`).

```bash
# Explicit server
newtrun start 2node-vs-primitive --server http://10.1.0.5:8080

# Or via environment
export NEWTRON_SERVER=http://10.1.0.5:8080
newtrun start 2node-vs-primitive
```

### Network ID

The network identifier tells newtron-server which spec set to use.
Resolution order: `--network-id` flag > `NEWTRON_NETWORK_ID` environment
variable > settings file > default.

```bash
newtrun start 2node-vs-primitive --network-id lab1
```

### Verbose Output

```bash
newtrun start 2node-vs-primitive -v
```

Shows per-step results with timing, failure details, and device-level
messages.

### JUnit Output

```bash
newtrun start 2node-vs-primitive --junit results.xml
```

---

## 5. Suite Lifecycle

Suites are stateful — newtrun persists progress at
`~/.newtron/newtrun/<suite>/state.json` so you can pause, resume, and
monitor across terminal sessions.

### Start a Suite

```bash
newtrun start 2node-vs-primitive
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
newtrun: 2node-vs-primitive
  topology:  2node-vs (deployed, 2 nodes running)
  platform:  sonic-vs
  status:    running (pid 12345)
  started:   2026-03-14 10:30:00 (5m ago)

  #  SCENARIO              STEPS  STATUS   REQUIRES             DURATION
  1  boot-ssh              2      PASS     —                    3s
  2  loopback              3      PASS     boot-ssh             5s
  3  bridged               15     running  boot-ssh             step 2/15: add-vlan-member
  4  irb                   5      —        bridged              —
  5  routed                8      —        boot-ssh             —

  progress: 2/21 passed
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
newtrun status --suite 2node-vs
```

### Pause a Running Suite

```bash
newtrun pause
```

The runner finishes the current scenario, then stops. The topology stays
deployed. State is saved as `paused`.

### Resume a Paused Suite

```bash
newtrun start 2node-vs-primitive
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
newtrun start 2node-vs-primitive

# Terminal 2: check progress
newtrun status --monitor

# Terminal 2: pause after current scenario
newtrun pause

# Terminal 1: runner finishes current scenario and exits
# "paused after 3 scenarios; resume with: newtrun start 2node-vs-primitive"

# Later: resume
newtrun start 2node-vs-primitive

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
steps. Here is a real scenario from the 2node-ngdp-primitive suite:

```yaml
# newtrun/suites/2node-ngdp-primitive/00-boot-ssh.yaml
name: boot-ssh
description: Verify SSH reachability on both switches
topology: 2node-ngdp
requires: []

steps:
  - name: ssh-echo-switch1
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/ssh-command
    params: {command: "echo ok"}
    poll:
      timeout: 120s
      interval: 5s
    expect:
      jq: '.output | contains("ok")'

  - name: ssh-echo-switch2
    action: newtron
    devices: [switch2]
    method: POST
    url: /node/{{device}}/ssh-command
    params: {command: "echo ok"}
    poll:
      timeout: 120s
      interval: 5s
    expect:
      jq: '.output | contains("ok")'
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
and action-specific fields. newtrun has exactly 5 actions:

| Action | Purpose |
|--------|---------|
| `topology-reconcile` | Deliver topology projection to device via Reconcile |
| `verify-topology` | Verify device matches topology projection (zero drift) |
| `wait` | Context-aware sleep |
| `host-exec` | Run command in host network namespace via direct SSH |
| `newtron` | Generic HTTP call to newtron-server (replaces all former dedicated actions) |

**Step fields:**

| Field | Type | Used by | Description |
|-------|------|---------|-------------|
| `name` | string | all | Step identifier for output |
| `action` | string | all | Which executor to run (`topology-reconcile`, `verify-topology`, `wait`, `host-exec`, `newtron`) |
| `devices` | selector | all except `wait` | Device selector: `all`, `[switch1]`, `[switch1, switch2]` |
| `duration` | duration | `wait` | How long to wait (e.g., `30s`, `2m`) |
| `command` | string | `host-exec` | Shell command to run inside the host namespace |
| `params` | map | `newtron` | Request body for POST/PUT/DELETE requests |
| `method` | string | `newtron` | HTTP method: `GET` (default), `POST`, `PUT`, `DELETE` |
| `url` | string | `newtron` | URL template with `{{device}}` placeholder |
| `poll` | object | `newtron` | Polling config: `{timeout: 120s, interval: 5s}` |
| `batch` | list | `newtron` | Sequential list of HTTP calls |
| `expect` | object | `host-exec`, `newtron` | Assertion block (see below) |
| `expect_failure` | bool | all | Expect the step to fail — inverts pass/fail (applied by the runner after execution) |

### 6.3 Expect Block

The `expect` block defines assertions. Available fields depend on the
action:

**For `newtron` action:**

| Field | Type | Description |
|-------|------|-------------|
| `jq` | string | jq expression evaluated against the HTTP response body. Must return boolean `true` to pass. |

**For `host-exec` action:**

| Field | Type | Description |
|-------|------|-------------|
| `success_rate` | float | Fraction of successful pings (e.g., `0.8` = 80%). Parses ping output for packet loss. |
| `contains` | string | String match on combined stdout+stderr |

Without `expect`, `host-exec` checks the exit code only. The `newtron`
action checks that the HTTP response is non-error.

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
topology: 2node-vs-service
requires: [provision]
repeat: 10

steps:
  - name: apply-svc
    action: newtron
    devices: [switch1]
    method: POST
    url: /node/{{device}}/interface/Ethernet3/service
    params: {service: l2-extend}

  - name: remove-svc
    action: newtron
    devices: [switch1]
    method: DELETE
    url: /node/{{device}}/interface/Ethernet3/service

  - name: verify-clean
    action: newtron
    devices: [switch1]
    url: /node/{{device}}/configdb/NEWTRON_INTENT/Ethernet3/exists
    expect:
      jq: '.exists == false'
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
# List all actions
newtrun actions

# Show detail for a specific action
newtrun actions topology-reconcile
newtrun actions newtron
```

newtrun has 5 actions: `topology-reconcile`, `verify-topology`, `wait`,
`host-exec`, and `newtron`. The `newtron` action is the general-purpose
action — it makes HTTP calls to newtron-server and covers every
operation that the old dedicated actions (create-vlan, apply-service,
verify-bgp, etc.) used to handle individually.

---

## 7. Step Action Reference

newtrun has exactly 5 actions. `topology-reconcile` and `verify-topology`
handle provisioning and verification. `host-exec` runs commands on host VMs
via SSH. `newtron` is the generic action that covers everything else via HTTP
calls to newtron-server.

### 7.1 topology-reconcile

Delivers the topology projection to the device by calling
`Reconcile(name, "topology", "", ExecOpts{Execute: true})`. Reconcile
handles config reload, locking, full CONFIG_DB replacement, and
SaveConfig internally — the executor makes a single API call.

```yaml
- name: provision-switches
  action: topology-reconcile
  devices: [switch1, switch2]
```

No additional fields. Reports the number of entries applied on success.

### 7.2 verify-topology

Verifies that the device CONFIG_DB matches the topology projection by
calling `IntentDrift(name, "topology")`. Zero drift entries means the
device is in the expected state. Any drift entries cause the step to
fail with a count of entries that diverge.

```yaml
- name: verify-config
  action: verify-topology
  devices: [switch1, switch2]
```

No precondition on a prior step — drift is computed directly from the
topology projection on the server.

### 7.3 wait

Context-aware sleep. Respects cancellation — if the suite is paused
during a wait, it exits cleanly.

```yaml
- name: wait-convergence
  action: wait
  duration: 30s
```

The only required field is `duration`. No `devices` field needed.

### 7.4 host-exec

Runs a command inside a host device's network namespace via direct SSH.
The namespace name matches the device name (e.g., `host1`, `host2`).
newtlab creates these namespaces at deploy time.

```yaml
- name: ping-host3
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8
```

**Fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `command` | yes | Shell command to run. Compound commands (semicolons, pipes) work — the executor wraps in `sh -c`. |
| `expect.success_rate` | no | Parse ping output for packet loss. `0.8` means 80% of pings must succeed. |
| `expect.contains` | no | String match on combined stdout+stderr. |

Without `expect`, the step checks the exit code only.

**Examples:**

```yaml
# Ping with success rate threshold
- name: ping-test
  action: host-exec
  devices: [host1]
  command: "ping -c 5 -W 2 10.100.0.3"
  expect:
    success_rate: 1.0

# String match on output
- name: check-interface
  action: host-exec
  devices: [host1]
  command: "ip addr show eth0"
  expect:
    contains: "10.100.0.1"

# Compound command
- name: configure-host
  action: host-exec
  devices: [host1]
  command: "ip addr add 10.100.0.1/24 dev eth0 2>/dev/null || true"
```

### 7.5 newtron — Generic HTTP Action

The `newtron` action makes HTTP calls to newtron-server. It replaces all
the former dedicated actions (create-vlan, apply-service, verify-bgp,
verify-config-db, setup-evpn, etc.) with a single generic mechanism.

The action has three modes: one-shot, polling, and batch.

#### URL Templates

URLs start from the path after the network segment. The `/network/<id>`
prefix is added automatically. Use `{{device}}` as a placeholder for
per-device expansion:

```yaml
url: /node/{{device}}/vlan           # expands to /network/<id>/node/switch1/vlan
url: /node/{{device}}/health         # expands to /network/<id>/node/switch1/health
url: /node/{{device}}/bgp/check      # expands to /network/<id>/node/switch1/bgp/check
```

If the URL contains `{{device}}`, the call runs in parallel across all
target devices. If not, it runs once with no device scoping (for
network-level operations like creating specs).

#### URL Encoding

CONFIG_DB keys use `|` as the table-key separator. In URLs, encode
special characters:

| Character | Encoding | Example |
|-----------|----------|---------|
| `\|` | `%7C` | `VLAN_MEMBER/Vlan100%7CEthernet1` |
| `/` in key | `%2F` | `LOOPBACK_INTERFACE/Loopback0%7C10.0.0.1%2F32` |

```yaml
# Verify a VLAN_MEMBER entry (pipe in key)
- name: verify-member
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/configdb/VLAN_MEMBER/Vlan100%7CEthernet1
  expect:
    jq: '.tagging_mode == "untagged"'
```

#### One-Shot Mode

The default mode. Makes a single HTTP call per device.

```yaml
# GET request (method defaults to GET)
- name: check-health
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/health

# POST with params (request body)
- name: create-vlan
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan
  params: {id: 100}

# DELETE request
- name: remove-service
  action: newtron
  devices: [switch1]
  method: DELETE
  url: /node/{{device}}/interface/Ethernet3/service
```

#### Polling Mode

When `poll` is set, the call repeats at the given interval until the
`expect.jq` expression returns `true` or the timeout expires. Use
polling for operations that depend on daemon convergence (BGP sessions,
ASIC programming, health checks).

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /node/{{device}}/bgp/check
  poll:
    timeout: 120s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

During polling, HTTP errors are treated as "not ready yet" — the action
keeps polling rather than failing immediately. This handles the common
case where the device is still booting or a daemon hasn't started
processing entries.

```yaml
# Poll SSH reachability during boot
- name: ssh-echo
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/ssh-command
  params: {command: "echo ok"}
  poll:
    timeout: 120s
    interval: 5s
  expect:
    jq: '.output | contains("ok")'

# Poll health checks with pass/warn tolerance
- name: verify-health
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/health
  poll:
    timeout: 60s
    interval: 5s
  expect:
    jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'
```

#### Batch Mode

The `batch` field runs a sequence of HTTP calls as a single step. The
batch fails on the first error. If any batch call URL contains
`{{device}}`, the entire sequence runs per-device in parallel.

```yaml
- name: setup-vlan-with-members
  action: newtron
  devices: [switch1]
  batch:
    - method: POST
      url: /node/{{device}}/vlan
      params: {id: 200}
    - method: POST
      url: /node/{{device}}/vlan/200/member
      params: {interface: Ethernet1, tagged: false}
    - method: POST
      url: /node/{{device}}/vlan/200/member
      params: {interface: Ethernet3, tagged: false}
```

Batch calls do not support individual `expect` blocks — the batch
succeeds if all calls return non-error responses.

#### jq Expressions

The `expect.jq` field is a jq expression evaluated against the HTTP
response body (parsed as JSON). The expression must produce a single
boolean `true` to pass. Any other value (including `false`, `null`, a
string, or a number) is a failure.

Common patterns:

```yaml
# Boolean field check
jq: '.exists == true'

# String field check
jq: '.tagging_mode == "untagged"'

# Array length check
jq: 'length > 0'

# All-pass check (every element satisfies a condition)
jq: 'all(.[]; .status == "pass")'

# Combined checks
jq: 'length > 0 and all(.[]; .status == "pass")'

# Nested field access
jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'

# String containment
jq: '.output | contains("ok")'

# Drift detection
jq: '.status == "drifted" and (.missing | length) == 1 and (.extra | length) == 1'
```

When a jq assertion fails, the error message includes the expression and
the actual value — useful for debugging without re-running the scenario.

#### expect_failure

The `expect_failure` flag inverts pass/fail. Use it to assert that an
operation fails — for example, verifying that a zombie intent blocks
write operations:

```yaml
- name: create-vlan-blocked-by-zombie
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan
  params: {id: 999}
  expect_failure: true
```

If the HTTP call fails (as expected), the step passes. If it succeeds
unexpectedly, the step fails.

#### Common Operations

The `newtron` action covers every operation exposed by newtron-server.
Here are representative examples organized by domain:

**SSH commands:**

```yaml
- name: check-frr
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/ssh-command
  params: {command: "vtysh -c 'show ip bgp summary'"}
  expect:
    jq: '.output | contains("Established")'
```

**CONFIG_DB verification:**

```yaml
# Check key existence
- name: check-loopback
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/configdb/LOOPBACK_INTERFACE/Loopback0/exists
  expect:
    jq: '.exists == true'

# Read and check field values
- name: check-bgp-globals
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/configdb/BGP_GLOBALS/default
  expect:
    jq: '.local_asn == "65001" and .router_id == "10.0.0.1"'

# Verify key does NOT exist (after removal)
- name: check-removed
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/configdb/NEWTRON_INTENT/Ethernet3/exists
  expect:
    jq: '.exists == false'
```

**VLAN operations:**

```yaml
# Create VLAN
- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan
  params: {id: 100}

# Add untagged member
- name: add-member
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan/100/member
  params: {interface: Ethernet1, tagged: false}

# Delete VLAN
- name: delete-vlan100
  action: newtron
  devices: [switch1]
  method: DELETE
  url: /node/{{device}}/vlan/100
```

**VRF operations:**

```yaml
# Create VRF
- name: create-vrf
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vrf
  params: {name: Vrf_local}

# Delete VRF
- name: delete-vrf
  action: newtron
  devices: [switch1]
  method: DELETE
  url: /node/{{device}}/vrf/Vrf_local
```

**Service lifecycle:**

```yaml
# Apply service to interface
- name: apply-service
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/interface/Ethernet3/service
  params: {service: l2-extend}

# Remove service from interface
- name: remove-service
  action: newtron
  devices: [switch1]
  method: DELETE
  url: /node/{{device}}/interface/Ethernet3/service
```

**BGP verification:**

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /node/{{device}}/bgp/check
  poll:
    timeout: 120s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

**Health checks:**

```yaml
- name: verify-health
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/health
  poll:
    timeout: 60s
    interval: 5s
  expect:
    jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'
```

**Config reload:**

```yaml
- name: config-reload
  action: newtron
  devices: [switch1, switch2]
  method: POST
  url: /node/{{device}}/config-reload
```

**Drift detection:**

```yaml
- name: check-drift
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/drift
  expect:
    jq: '.status == "clean"'
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

Multiple hosts may share a single QEMU VM. For example, the 2node-ngdp
topology coalesces host1-host6 across two host VMs (the 2node-ngdp-service
topology has host1-host8). Each host's network namespace provides
isolation.

### host-exec for Data Plane

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
- `contains`: string match on combined stdout+stderr

Without `expect`, the step checks the exit code only.

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

From the 2node-ngdp-primitive suite — creates a VLAN, adds untagged members
on a single switch, and verifies L2 connectivity between two hosts:

```yaml
# Create VLAN and add untagged members
- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan
  params: {id: 100}

- name: add-host1-port
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan/100/member
  params: {interface: Ethernet1, tagged: false}

- name: add-host3-port
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/vlan/100/member
  params: {interface: Ethernet3, tagged: false}

# Wait for ASIC programming
- name: wait-bridge
  action: wait
  duration: 45s

# Configure host IPs
- name: host1-ip
  action: host-exec
  devices: [host1]
  command: "ip addr add 10.100.0.1/24 dev eth0 2>/dev/null || true"

- name: host3-ip
  action: host-exec
  devices: [host3]
  command: "ip addr add 10.100.0.3/24 dev eth0 2>/dev/null || true"

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
newtrun: 21 scenarios, topology: 2node-vs, platform: sonic-vs

  [1/21]  boot-ssh .................. PASS  (3s)
  [2/21]  loopback .................. PASS  (5s)
  [3/21]  bridged ................... PASS  (18s)
  ...
  [21/21] verify-clean .............. PASS  (12s)

---
newtrun: 21 scenarios: 21 passed  (3m45s)
```

Verbose mode (`-v`) shows per-step results within each scenario:

```
  [3/21]  bridged
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
newtrun start 2node-vs-primitive --junit results.xml
```

### GitHub Actions Example

```yaml
- name: Run newtrun
  run: |
    bin/newtrun start 2node-vs-primitive --junit results.xml
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
bin/newtron-server --spec-dir newtrun/topologies/2node-vs/specs &

# Override the URL
newtrun start 2node-vs-primitive --server http://localhost:8080
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

BGP sessions may take time to establish after provisioning. Increase
the poll timeout in your scenario:

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /node/{{device}}/bgp/check
  poll:
    timeout: 180s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

Or SSH in and check manually:

```bash
newtlab ssh switch1
vtysh -c "show ip bgp summary"
```

### Health Checks Fail

Health checks verify interfaces, BGP, EVPN, LAG, and VXLAN. Use
polling so the check retries during daemon convergence rather than
failing on the first attempt:

```yaml
- name: verify-health
  action: newtron
  devices: [switch1]
  url: /node/{{device}}/health
  poll:
    timeout: 60s
    interval: 5s
  expect:
    jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'
```

If a check still fails, SSH in and inspect:

```bash
newtlab ssh switch1
show interfaces status
vtysh -c "show ip bgp summary"
```

### Data Plane Tests Fail

Only platforms with `dataplane` set in `platforms.json` support actual
packet forwarding. Check:

```bash
# Verify the platform has dataplane support
cat newtrun/topologies/2node-ngdp/specs/platforms.json | grep dataplane
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
newtrun start 2node-vs-primitive                        # all scenarios
newtrun start 2node-vs-primitive --scenario boot-ssh    # single scenario
newtrun start --dir path/to/suite                       # explicit path
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
newtrun status --suite 2node-vs   # filter by name
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
newtrun list 2node-vs-primitive    # show scenarios in suite
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

List all step actions. With an action name, shows detailed parameter
info and example YAML.

```
newtrun actions                    # list all 5 actions
newtrun actions newtron            # show detail for the generic action
```

No flags.

### `newtrun version`

Print version information.

```
newtrun version
```

No flags.
