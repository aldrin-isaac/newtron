# newtrun — High-Level Design

For the architectural principles behind newtron, newtlab, and newtrun, see [Design Principles](../DESIGN_PRINCIPLES.md).

## 1. Purpose

newtrun is an E2E testing framework that tests **composed network
outcomes** — not individual features. The question is not "does VLAN
creation work?" but "does the L3VPN service produce reachability across
the EVPN overlay?" A feature test can pass while the composite
multi-feature configuration fails due to ordering issues, missing glue
config, or daemon interaction bugs. newtrun tests the thing that actually
matters: the assembled result.

newtrun is a general-purpose test framework. Users write their own
topologies and suites as YAML scenario files and spec directories — the
built-in suites that ship with the project are examples, not the
exhaustive set. Any topology that newtlab can deploy and any operation
that newtron exposes can be exercised by a newtrun scenario.

newtrun is one orchestrator built on top of newtron and newtlab — not the
only one. Other orchestrators could be built for different purposes
(production deployment, CI/CD pipelines, compliance auditing). newtrun
observes devices exclusively through newtron's HTTP API — it never
accesses Redis directly. newtron returns structured data; newtrun decides
what "correct" means by correlating observations across devices.

```
┌────────────┐                ┌────────────────────────┐         ┌────────────────┐
│            │                │                        │         │                │
│            │                │        newtrun         │         │                │
│  newtlab   │                │   1. Deploy topology   │         │ newtron-server │
│ Deploy VMs │                │ 2. Provision & operate │         │ SONiC ops via  │
│   (QEMU)   │                │  3. Validate results   │         │ HTTP REST API  │
│            │                │  4. Report pass/fail   │         │                │
│            │  newtlab API   │      5. Tear down      │  HTTP   │                │
│            │ ◀───────────── │                        │ ──────▶ │                │
└────────────┘                └────────────────────────┘         └────────────────┘
                                │
                                │ SSH
                                ▼
                              ┌────────────────────────┐
                              │                        │
                              │        Host VMs        │
                              │       SSH direct       │
                              │      (host-exec)       │
                              │                        │
                              └────────────────────────┘
```

---

## 2. Three Tools, Clear Boundaries

newtrun sits between two tools that each do one thing well. Understanding the boundaries prevents the common mistake of putting cross-device logic in newtron or device-level logic in newtrun.

| Tool | Responsibility | Knows About |
|------|---------------|-------------|
| **newtron** | Opinionated single-device automation: translate specs → CONFIG_DB; verify own writes; observe single-device routing state | Specs, device profiles, Redis (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB) |
| **newtlab** | Realize VM topologies: deploy QEMU VMs from newtron's topology.json, wire socket links across servers | topology.json, platforms.json, QEMU |
| **newtrun** | E2E test orchestration: decide what gets provisioned where, sequence steps, assert cross-device correctness | Test scenarios, topology-wide expected results |

**Verification principle**: If a tool changes the state of an entity, that same
tool must be able to verify the change had the intended effect. newtron writes
CONFIG_DB and configures routing — so newtron owns verification of those
changes. newtrun builds on newtron's self-verification by adding the
cross-device layer: using newtron (via HTTP) to observe each device, then
correlating observations across devices using topology context. newtrun never
accesses Redis directly — it observes devices exclusively through the
newtron-server HTTP API.

---

## 3. Architecture

The Runner is a pure orchestrator. It holds references to three external
systems but implements no device logic itself:

```
newtrun Runner
├── r.Client     (*client.Client)  → HTTP → newtron-server → SONiC switches
├── r.Lab        (*newtlab.Lab)    → newtlab → QEMU VMs (deploy/destroy/ensure)
└── r.HostConns  (map[string]*ssh.Client)  → host VMs → network namespaces
```

**All SONiC operations go through HTTP.** The Runner creates an HTTP client
(`pkg/newtron/client`), registers the network spec directory with the server,
and every subsequent operation — provisioning, VLAN creation, health checks,
route verification — is an HTTP request to newtron-server. The server manages
SSH connections to SONiC devices; newtrun never connects to them directly.

**Topology lifecycle goes through newtlab.** The Runner calls the newtlab Go
API to deploy, ensure, or destroy QEMU VM topologies. When running in
lifecycle mode (the `start` command), `EnsureTopology` reuses running VMs if
all nodes are healthy, avoiding a full redeploy between iterations.

**Host devices use direct SSH.** For data plane testing, the Runner
SSH-connects to host VMs and stores the connections in `r.HostConns`. The
`host-exec` action runs commands inside network namespaces on these hosts.
Host SSH connections bypass newtron-server entirely — these are plain Linux
VMs, not SONiC devices.

**No internal imports.** newtrun imports `pkg/newtron/client/` (HTTP
client), `pkg/newtlab/` (lab API), `pkg/newtron/` (public API types), and
shared utilities (`pkg/util`, `pkg/cli`). It never imports
`pkg/newtron/network/`, `pkg/newtron/network/node/`, or
`pkg/newtron/device/sonic/` — the internal implementation packages.

### 3.1 Server URL Resolution

The Runner resolves the newtron-server URL through a four-tier cascade:

1. `--server` CLI flag
2. `NEWTRON_SERVER` environment variable
3. `newtron.LoadSettings()` → `GetServerURL()`
4. `newtron.DefaultServerURL` (built-in default)

Network ID follows the same pattern (`--network-id`, `NEWTRON_NETWORK_ID`, settings, default).

---

## 4. Directory Structure

newtrun's code lives in three places: CLI commands (`cmd/newtrun/`), core library (`pkg/newtrun/`), and test assets (`newtrun/` at the repo root).

```
newtron/
├── cmd/newtrun/              # CLI commands
│   ├── main.go               # Root command, sentinel errors, exit code mapping
│   ├── helpers.go             # resolveDir, resolveSuite, suitesBaseDir
│   ├── cmd_start.go           # start command (+ deprecated run alias)
│   ├── cmd_pause.go           # pause command
│   ├── cmd_stop.go            # stop command
│   ├── cmd_status.go          # status command
│   ├── cmd_list.go            # list suites and scenarios
│   ├── cmd_suites.go          # suites (hidden alias for list)
│   ├── cmd_topologies.go      # topologies command
│   └── cmd_actions.go         # actions command (list/show step actions)
│
├── pkg/newtrun/               # Core library
│   ├── scenario.go            # Scenario, Step, StepAction constants, ExpectBlock
│   ├── parser.go              # ParseScenario, validation, dependency graph (Kahn's)
│   ├── runner.go              # Runner, RunOptions, iterateScenarios, connectDevices
│   ├── steps.go               # stepExecutor interface, all executor implementations
│   ├── steps_host.go          # host-exec executor, shellQuote, runSSHCommand
│   ├── deploy.go              # DeployTopology, EnsureTopology, DestroyTopology
│   ├── state.go               # RunState, ScenarioState, SuiteStatus, persistence
│   ├── progress.go            # ProgressReporter, consoleProgress, StateReporter
│   ├── errors.go              # InfraError, StepError, PauseError
│   ├── report.go              # ScenarioResult, StepResult, StepStatus, ReportGenerator
│   ├── state_test.go          # State management tests
│   └── newtrun_test.go        # Unit tests
│
├── newtrun/                   # Test assets
│   ├── topologies/            # Topology spec directories
│   │   ├── 2node-ngdp/specs/       # 2 switches + 6 hosts
│   │   ├── 2node-ngdp-service/specs/  # 2 switches + 8 hosts (service-annotated)
│   │   ├── 3node-ngdp/specs/       # 2 leaves + 2 hosts
│   │   └── 4node-ngdp/specs/       # 2 spines + 2 leaves
│   ├── suites/                # Test suite directories
│   │   ├── 2node-ngdp-primitive/   # 20 scenarios (disaggregated operations)
│   │   ├── 2node-ngdp-service/     # 6 scenarios (service lifecycle + dataplane)
│   │   ├── 3node-ngdp-dataplane/   # 8 scenarios (EVPN L2/L3 dataplane)
│   │   ├── simple-vrf-host/   # 5 scenarios (VRF + host reachability)
│   │   └── 1node-vs-basic/       # 4 scenarios (service lifecycle + VLAN/VRF)
│   └── .generated/            # Runtime output (gitignored)
│       └── report.md
```

Each topology is self-contained with its own spec directory. Each suite is a
directory of YAML scenario files. Users create new topologies and suites by
adding directories — no code changes required.

---

## 5. Test Topologies

Topologies are pre-defined spec directories checked into the repo. They
contain the full set of newtron spec files: `topology.json` (devices, links),
`network.json` (services, VPNs, filters), `platforms.json` (hardware
definitions), and `profiles/*.json` (per-device settings). newtrun reads them
directly — no generation step.

### 5.1 Built-In Topologies

| Topology | Devices | Purpose |
|----------|---------|---------|
| **2node-ngdp** | switch1, switch2 + host1–host6 | Disaggregated primitive testing |
| **2node-ngdp-service** | switch1, switch2 + host1–host8 | Service lifecycle with dataplane verification |
| **3node-ngdp** | leaf1, leaf2 + host1, host2 | EVPN L2/L3 dataplane across two leaves |
| **4node-ngdp** | spine1, spine2, leaf1, leaf2 | Full fabric (route reflectors on spines) |

#### 2node-ngdp

Two switches with three inter-switch links and three hosts per switch:

```
                switch1 ─── Eth0 ─── switch2
                   │    ─── Eth4 ───    │
                   │    ─── Eth5 ───    │
                   │                    │
            Eth1 Eth2 Eth3       Eth1 Eth2 Eth3
             │    │    │          │    │    │
           host1 host2 host3   host4 host5 host6
```

No pre-configured services — interfaces are clean slates for disaggregated
operation testing.

#### 2node-ngdp-service

Same switch pair with service-annotated interfaces:

```
switch1:Eth0 ── transit ── switch2:Eth0
switch1:Eth1 ── local-irb ── host1      switch2:Eth1 ── local-irb ── host4
switch1:Eth2 ── local-bridge ── host2   switch2:Eth2 ── local-bridge ── host5
switch1:Eth3 ── l2-extend ── host3      switch2:Eth3 ── l2-extend ── host6
switch1:Eth4 ── overlay-irb-a ── host7  switch2:Eth4 ── overlay-irb-b ── host8
switch1:Eth5 ──────────────────────── switch2:Eth5   (inter-switch, no service)
```

Each interface has a pre-assigned service in the topology spec. Provisioning
applies all services atomically. The extra host pair (host7, host8) exercises
EVPN IRB overlay scenarios.

#### 3node-ngdp

Two leaves with a single transit link, one host per leaf:

```
leaf1 ── Eth0 (transit) ── leaf2
  │                          │
Eth1                       Eth1
  │                          │
host1                      host2
```

Exercises EVPN L2/L3 forwarding across a two-leaf fabric with real data
plane verification between hosts.

#### 4node-ngdp

Full-mesh Clos topology: two spines with `route_reflector: true`, two leaves:

```
spine1 ─── leaf1
  ╲   ╳   ╱
spine2 ─── leaf2
```

### 5.2 Spec Files

Each topology directory contains:

| File | Read By | Contents |
|------|---------|----------|
| `topology.json` | newtlab + newtron | Devices, interfaces, links, newtlab settings |
| `network.json` | newtron | Services, filters, VPNs, zones |
| `platforms.json` | newtlab | Platform definitions with VM settings |
| `profiles/*.json` | newtlab (writes ports) + newtron (reads) | Per-device settings, EVPN config |

### 5.3 Custom Topologies

The built-in topologies cover common patterns, but newtrun works with any
topology that newtlab can deploy. To create a custom topology:

1. Create a directory under `newtrun/topologies/<name>/specs/`
2. Add the standard spec files (`topology.json`, `network.json`, `platforms.json`, `profiles/`)
3. Reference it in scenario YAML: `topology: <name>`

---

## 6. Scenarios and Steps

A scenario is a YAML file that defines what to test against a deployed
topology. Scenarios are the unit of authorship — users write scenarios to
exercise specific network behaviors.

### 6.1 Scenario Structure

```yaml
name: provision
description: Provision switches and verify BGP convergence
topology: 2node-ngdp-service
requires: [boot-ssh]

steps:
  - name: provision-switches
    action: provision
    devices: [switch1, switch2]

  - name: config-reload
    action: config-reload
    devices: [switch1, switch2]

  - name: wait-reload
    action: wait
    duration: 45s

  - name: verify-bgp
    action: verify-bgp
    devices: [switch1, switch2]
    expect:
      state: Established
      timeout: 120s
      poll_interval: 5s
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique scenario identifier |
| `description` | No | Human-readable purpose |
| `topology` | Yes | Topology directory name |
| `platform` | No | Override platform (default from topology) |
| `requires` | No | Scenarios that must pass before this one runs |
| `after` | No | Ordering constraint (run after, but no pass/fail gate) |
| `requires_features` | No | Platform features required (skip if unsupported) |
| `repeat` | No | Run all steps N times (fail-fast on first failure) |
| `steps` | Yes | Ordered list of step actions |

**Dependency modes:** `requires` means "skip this scenario if the named
scenario did not pass." `after` means "run this scenario after the named one,
regardless of its outcome." Both participate in topological sort for execution
ordering.

### 6.2 Step Actions

Every step has an `action` that maps to a `stepExecutor` in the Runner. Most
actions call `r.Client.X()` over HTTP to newtron-server. Host actions use
direct SSH (run `newtrun actions` for the full list).

**Pattern:** Each action is a struct implementing `Execute(ctx, r, step)
*StepOutput`. Device-targeting actions use one of three multi-device helpers
that iterate over `step.Devices`, executing the operation per-device and
aggregating results. All three helpers automatically skip host devices (only
SONiC switches receive newtron operations).

#### Actions by Category

**Provisioning** — Full device setup and configuration reload.

| Action | Description |
|--------|-------------|
| `provision` | Generate and deliver device composite (6-step sequence) |
| `configure-loopback` | Configure loopback interface |
| `remove-loopback` | Remove loopback configuration |
| `configure-bgp` | Write BGP globals from device profile |
| `apply-frr-defaults` | Apply FRR runtime defaults |
| `config-reload` | Reload SONiC configuration (restart bgp + apply pending) |

**Verification** — Observe device state, with optional polling.

| Action | Description |
|--------|-------------|
| `verify-provisioning` | Verify CONFIG_DB matches composite ChangeSet |
| `verify-config-db` | Assert CONFIG_DB table/key/field values |
| `verify-state-db` | Assert STATE_DB entries (polling) |
| `verify-bgp` | Check BGP sessions reach expected state (polling) |
| `verify-health` | Health check: CONFIG_DB intent + BGP + interfaces [^1] |
| `verify-route` | Check route in APP_DB or ASIC_DB (polling) |
| `verify-ping` | Data plane ping, resolved from device info |

**Service lifecycle** — Apply, remove, and refresh interface services.

| Action | Description |
|--------|-------------|
| `apply-service` | Apply named service to device interface |
| `remove-service` | Remove service binding from interface |
| `refresh-service` | Full remove + reapply cycle |

**VLAN** — L2 domain management.

| Action | Description |
|--------|-------------|
| `create-vlan` / `delete-vlan` | VLAN lifecycle |
| `add-vlan-member` / `remove-vlan-member` | Interface membership |
| `configure-svi` / `remove-svi` | Switched Virtual Interface (L3 on VLAN) |

**VRF** — Virtual routing instances.

| Action | Description |
|--------|-------------|
| `create-vrf` / `delete-vrf` | VRF lifecycle |
| `add-vrf-interface` / `remove-vrf-interface` | Bind/unbind interface |

**BGP** — Neighbor management.

| Action | Description |
|--------|-------------|
| `bgp-add-neighbor` / `bgp-remove-neighbor` | Add/remove peer (interface or loopback) |
| `remove-bgp-globals` | Remove BGP instance and globals |

**EVPN** — Overlay setup and VPN bindings.

| Action | Description |
|--------|-------------|
| `setup-evpn` / `teardown-evpn` | VTEP + NVO + BGP EVPN lifecycle |
| `bind-ipvpn` / `unbind-ipvpn` | L3 VPN binding to VRF |
| `bind-macvpn` / `unbind-macvpn` | L2 VPN binding to VLAN |

**ACL** — Access control lists.

| Action | Description |
|--------|-------------|
| `create-acl-table` / `delete-acl-table` | ACL table lifecycle |
| `add-acl-rule` / `delete-acl-rule` | Rule management |
| `bind-acl` / `unbind-acl` | Bind/unbind ACL to interface |

**QoS** — Quality of service.

| Action | Description |
|--------|-------------|
| `apply-qos` / `remove-qos` | Apply/remove QoS policy on interface |

**Interface** — Property management.

| Action | Description |
|--------|-------------|
| `set-interface` | Set properties (mtu, ip, vrf, admin-status) |
| `remove-ip` | Remove IP address from interface |

**PortChannel** — Link aggregation.

| Action | Description |
|--------|-------------|
| `create-portchannel` / `delete-portchannel` | LAG lifecycle |
| `add-portchannel-member` / `remove-portchannel-member` | Member management |

**Static routing**

| Action | Description |
|--------|-------------|
| `add-static-route` / `remove-static-route` | Static route in VRF |

**Network-level spec authoring** — Create and modify specs without touching devices.
These actions operate at network scope (no `devices:` field). They call
`r.Client.*` directly — the spec exists in the network, not on any device.

| Action | Description |
|--------|-------------|
| `create-service` / `delete-service` | Service spec lifecycle |
| `create-prefix-list` / `delete-prefix-list` | Prefix list spec lifecycle |
| `add-prefix-entry` / `remove-prefix-entry` | Add/remove prefix in list |
| `create-route-policy` / `delete-route-policy` | Route policy spec lifecycle |
| `add-route-policy-rule` / `remove-route-policy-rule` | Add/remove rule in policy |

**Infrastructure** — Utility actions.

| Action | Description |
|--------|-------------|
| `wait` | Wait for specified duration |
| `ssh-command` | Run command via SSH, check output |
| `host-exec` | Run command in host network namespace |
| `restart-service` | Restart a SONiC service (bgp, swss) |
| `cleanup` | Remove orphaned CONFIG_DB resources |

[^1]: `verify-health` is a single-shot read — it does not poll. Use a `wait` step before `verify-health` if convergence time is needed.

### 6.3 Distinctive Actions

Most actions follow a uniform pattern: resolve devices, call `r.Client.X()`,
check result. A few deserve additional explanation.

**`provision`** executes a 6-step sequence per device:

1. `GenerateDeviceComposite` — build CONFIG_DB offline (HTTP POST)
2. Store returned composite handle in `r.Composites[device]`
3. `DeliverComposite` — atomic write to Redis (HTTP POST)
4. `VerifyComposite` — re-read CONFIG_DB and diff (HTTP POST)
5. Save configuration to disk
6. Report change count

**`verify-ping`** resolves the target IP from the device's profile information
(loopback IP), then runs a ping from each specified device. On platforms
without a dataplane (`dataplane: ""` in platform definition), the step
automatically returns SKIP instead of FAIL.

**`host-exec`** runs a command inside a network namespace on a host VM. The
namespace name equals the device name (e.g., `host1`). The command is
wrapped as `ip netns exec <device> sh -c '<command>'`, with single quotes
escaped to handle compound commands with pipes and semicolons.

**`set-interface`** dispatches three ways depending on parameters: `ip`
parameter → set IP address, `vrf` parameter → bind to VRF, otherwise → set
interface property (mtu, admin-status, description).

### 6.4 Custom Suites

The built-in suites demonstrate patterns for different testing strategies:

- **Incremental suites** (2node-ngdp-primitive, 2node-ngdp-service, 3node-ngdp-dataplane):
  Ordered scenarios with `requires` chaining. A shared topology deployed once.
  Scenarios build on each other (boot → configure → verify → teardown).

Users write new suites by creating a directory of YAML files. Any combination
of the registered actions can appear in steps. Custom topologies work with custom
suites — the only constraint is that the `topology:` field names a directory
under `newtrun/topologies/`.

---

## 7. Verification Tiers

Verification spans four tiers across two owners. newtron provides single-device
primitives; newtrun orchestrates them across devices and adds data-plane testing.

| Tier | What | Owner | Method | Failure Mode |
|------|------|-------|--------|-------------|
| **CONFIG_DB** | Redis entries match ChangeSet | **newtron** | via HTTP: composite verify | Hard fail (assertion) |
| **APP_DB / ASIC_DB** | Routes installed by FRR / ASIC | **newtron** | via HTTP: `VerifyRoute` | Observation (data) |
| **Operational state** | BGP sessions, interface health | **newtron** | via HTTP: `VerifyHealth` | Observation (report) |
| **Cross-device / data plane** | Route propagation, ping | **newtrun** | Composes newtron primitives | Topology-dependent |

The first three tiers execute on newtron-server — newtrun sends an HTTP
request and receives structured results. The fourth tier is newtrun's own
contribution: it correlates observations from multiple devices to determine
cross-device correctness.

### 7.1 Platform-Aware Test Skipping

Platforms declare capabilities in `platforms.json` (e.g., `dataplane: "vpp"`
or `dataplane: ""`). Scenarios can declare `requires_features` — if the
deployed platform lacks a required feature, the scenario is skipped with
`SKIP` status rather than failing. Individual steps like `verify-ping` also
check platform capabilities and auto-skip on control-plane-only platforms.

---

## 8. Execution Model

### 8.1 Dispatch Pipeline

When the Runner receives a set of scenarios to execute:

1. **Parse**: YAML files → `Scenario` structs. The parser validates that each
   step uses only fields appropriate for its action type, checked against a
   per-action validation table.

2. **Sort**: If any scenario declares `requires` or `after`, all scenarios are
   topologically sorted using Kahn's algorithm. Cycles are rejected.

3. **Mode selection**: If all scenarios share the same topology, the Runner
   enters **shared mode** — deploy once, connect once, run all. If topologies
   differ, **independent mode** — each scenario gets its own deploy/connect
   cycle.

4. **Iterate**: For each scenario in order:
   - **Resume check**: Skip already-passed scenarios when resuming a paused run
   - **Pause check**: If another process set the suite to "pausing," stop here
   - **Requires check**: Skip if a required scenario did not pass
   - **Feature check**: Skip if platform lacks required features
   - **Execute**: Run all steps sequentially

5. **Step dispatch**: Each step → `executors[action].Execute(ctx, r, step)`.
   The executor returns a `StepOutput` containing per-device results.

### 8.2 Multi-Device Helpers

Three helper functions handle the common pattern of running an operation
across multiple devices:

| Helper | Pattern | Used By |
|--------|---------|---------|
| `executeForDevices` | Run once per device, collect results | All mutating operations |
| `checkForDevices` | Single-shot observation per device | `verify-health` |
| `pollForDevices` | Retry with timeout/interval per device | `verify-bgp`, `verify-route`, `verify-state-db` |

All three automatically skip host devices — they check `r.HostConns[name]`
and return SKIP for any device that is a host. Only SONiC switches receive
newtron operations through these helpers.

### 8.3 Repeat Mode

When a scenario sets `repeat: N`, the Runner executes all steps in a loop
for N iterations. Execution stops on the first iteration that produces a
failure. The report shows which iteration failed (e.g., "failed on iteration
7/10").

### 8.4 Signal Handling

The Runner installs a SIGINT handler at the start of each shared/independent
run. On SIGINT, the current step completes (no mid-step interruption), then
the context is cancelled and the scenario terminates gracefully.

---

## 9. Suite Lifecycle

newtrun tracks suite state across process boundaries, enabling pause/resume
and multi-command workflows.

### 9.1 State Machine

```
                                       ┌───────────────────┐
                                       │                   │
                                       │       start       │
                                       │                   │
                                       └───────────────────┘
                                         │
                                         │
                                         ▼
┌───────────────────┐                  ┌───────────────────┐
│                   │                  │                   │
│ complete / failed │  scenario ends   │      running      │
│                   │ ◀─────────────── │                   │ ◀┐
└───────────────────┘                  └───────────────────┘  │
                                         │                    │
                                         │ pause              │
                                         ▼                    │
                                       ┌───────────────────┐  │
                                       │                   │  │
                                       │      pausing      │  │
                                       │                   │  │ start
                                       └───────────────────┘  │
                                         │                    │
                                         │ current scenario   │
                                         │ ends               │
                                         ▼                    │
                                       ┌───────────────────┐  │
                                       │                   │  │
                                       │      paused       │  │
                                       │                   │ ─┘
                                       └───────────────────┘
```

| Status | Meaning |
|--------|---------|
| `running` | Suite is actively executing scenarios (PID recorded) |
| `pausing` | Pause requested; will stop after current scenario completes |
| `paused` | Suite stopped between scenarios; topology still deployed |
| `complete` | All scenarios finished; no failures or errors |
| `failed` | Suite finished with failures or errors |
| `aborted` | Runner process died unexpectedly |

### 9.2 State Persistence

State is persisted at `~/.newtron/newtrun/<suite>/state.json`. The file
contains suite metadata, runner PID, suite status, and per-scenario status
with current step and duration. State is updated after every scenario start,
scenario end, and step start — enabling real-time progress monitoring via
`newtrun status`.

### 9.3 Lifecycle Commands

| Command | What It Does |
|---------|-------------|
| `start` | Deploy topology (or reuse via `EnsureTopology`), run scenarios, save state |
| `pause` | Signal running suite to stop after current scenario |
| `stop` | Destroy topology, remove suite state |
| `status` | Show suite progress, per-scenario status, current step |

### 9.4 Concurrency Control

`AcquireLock` prevents concurrent runs of the same suite. It checks if an
existing state file records a live PID (via `kill -0`). If the PID is alive,
`start` refuses to run. If the PID is dead (crash/kill), the lock is
considered stale and a new run proceeds.

---

## 10. Host Devices and Data Plane

Data plane tests require endpoints that can generate and receive packets.
newtrun uses **host devices** — Alpine Linux VMs defined alongside switches in
`topology.json`.

### 10.1 VM Coalescing

Multiple host devices are coalesced into a single QEMU VM to reduce resource
overhead. For example, host1 through host6 in the 2node-ngdp topology share a
single VM (`hostvm-0`). Inside the VM, each host gets its own **network
namespace** matching its device name. newtlab creates the namespaces at deploy
time — test scenarios do not manage namespace lifecycle.

```
┌───────────────────┐         ┌────────────────────────────────┐         ┌───────────────────┐
│                   │         │                                │         │                   │
│     newtlink      │         │   hostvm-0 (Alpine Linux VM)   │         │     newtlink      │
│ (to switch1:Eth2) │         │ ns:host1 | ns:host2 | ns:host3 │         │ (to switch1:Eth3) │
│                   │  eth2   │         eth1 eth2 eth3         │  eth3   │                   │
│                   │ ◀────── │                                │ ──────▶ │                   │
└───────────────────┘         └────────────────────────────────┘         └───────────────────┘
  │                             │                                          │
  │                             │ eth1                                     │
  ▼                             ▼                                          ▼
┌───────────────────┐         ┌────────────────────────────────┐         ┌───────────────────┐
│                   │         │                                │         │                   │
│   switch1:Eth2    │         │            newtlink            │         │   switch1:Eth3    │
│                   │         │       (to switch1:Eth1)        │         │                   │
│                   │         │                                │         │                   │
└───────────────────┘         └────────────────────────────────┘         └───────────────────┘
                                │
                                │
                                ▼
                              ┌────────────────────────────────┐
                              │                                │
                              │          switch1:Eth1          │
                              │                                │
                              └────────────────────────────────┘
```

### 10.2 Host Actions

The `host-exec` executor:
1. Looks up the SSH connection from `r.HostConns[device]`
2. Wraps the command: `ip netns exec <device> sh -c '<command>'`
3. Executes via SSH, captures output
4. Checks expectations: `success_rate` (ping parse), `contains` (string match), or bare exit code

Example scenario step:
```yaml
- name: ping-host3-to-host6
  action: host-exec
  devices: [host3]
  command: "ping -c 10 -W 2 192.168.3.20"
  expect:
    success_rate: 0.80
```

### 10.3 Automatic Host Skipping

The three multi-device helpers (§8.2) automatically skip host devices. When a
step targets `all` devices, SONiC operations run only on switches — hosts are
silently skipped with a SKIP result. This means `devices: all` is safe for
operations like `provision` or `verify-bgp` even when the topology includes
hosts.

---

## 11. Output and Reporting

newtrun produces three output formats: real-time console progress, a markdown summary report, and optional JUnit XML for CI integration.

### 11.1 Console Output

Non-verbose mode shows one line per scenario with dot-padded status:

```
newtrun: 20 scenarios, topology: 2node-ngdp, platform: sonic-cisco-8000

  #     SCENARIO                STEPS
  1     boot-ssh                2
  2     loopback                4
  ...

  [1/20]  boot-ssh ............. PASS  (3s)
  [2/20]  loopback ............. PASS  (8s)
  [3/20]  bridged .............. PASS  (15s)
  ...

---
newtrun: 20 scenarios: 20 passed  (6m30s)
```

Verbose mode (`-v`) shows per-step detail within each scenario.

### 11.2 Markdown Report

Written to `newtrun/.generated/report.md` after each run:

```markdown
# newtrun Report — 2026-03-03 10:30:00

| Scenario | Topology | Platform | Result | Duration | Note |
|----------|----------|----------|--------|----------|------|
| boot-ssh | 2node-ngdp | sonic-cisco-8000 | PASS | 3s | |
| loopback | 2node-ngdp | sonic-cisco-8000 | PASS | 8s | |

## Failures

(none)
```

For repeated scenarios, the Note column shows iteration counts (e.g., "10
iterations" or "failed on iteration 7/10").

### 11.3 JUnit XML

For CI systems that parse JUnit XML. Each `ScenarioResult` maps to a
`<testsuite>`, each `StepResult` maps to a `<testcase>`.

```bash
newtrun start 2node-ngdp-primitive --junit results.xml
```

---

## 12. End-to-End Walkthrough

A concrete trace of `newtrun start 2node-ngdp-service` from command line to final
report:

```
CLI (cmd/newtrun/cmd_start.go)
  │
  │ 1. Resolve suite directory: newtrun/suites/2node-ngdp-service/
  │ 2. Check for paused state → LoadRunState("2node-ngdp-service")
  │ 3. AcquireLock → write PID to state.json
  │ 4. Resolve server URL (--server > env > settings > default)
  │ 5. Create Runner, assign ServerURL, NetworkID, Progress reporter
  │
  ▼
Runner.Run(opts)
  │
  │ 6. ParseAllScenarios → 6 scenarios
  │ 7. ValidateDependencyGraph → topological sort
  │ 8. sharedTopology → "2node-ngdp-service" (all scenarios agree)
  │
  ▼
runShared(ctx, scenarios, "2node-ngdp-service", opts)
  │
  │ 9. EnsureTopology("newtrun/topologies/2node-ngdp-service/specs/")
  │    newtlab checks if VMs running → deploys fresh if needed
  │
  │ 10. connectDevices:
  │     a. client.New(serverURL, networkID)
  │     b. client.RegisterNetwork(specDir) → HTTP POST → server loads specs
  │     c. client.TopologyDeviceNames() → [host1..host8, switch1, switch2]
  │     d. For each host device → SSH connect → r.HostConns["host1"] = conn
  │     e. SONiC devices NOT pre-connected (server connects on demand)
  │
  ▼
iterateScenarios → for each of the 6 scenarios in order:
  │
  │ 11. boot-ssh: ssh-command "echo ok" on switch1, switch2
  │     → r.Client.SSHCommand("switch1", "echo ok") → HTTP → server → SSH → device
  │
  │ 12. provision: per device:
  │     a. r.Client.GenerateDeviceComposite("switch1")
  │        → HTTP POST → server builds composite offline → returns handle UUID
  │     b. r.Composites["switch1"] = handle
  │     c. r.Client.DeliverComposite("switch1", handle)
  │        → HTTP POST → server writes to Redis atomically
  │     d. r.Client.VerifyComposite("switch1", handle)
  │        → HTTP POST → server re-reads CONFIG_DB, diffs against ChangeSet
  │
  │ 13. verify-health: r.Client.VerifyHealth("switch1")
  │     → HTTP GET → server checks CONFIG_DB, BGP, interfaces → report
  │
  │ 14. dataplane: host-exec steps
  │     → SSH to hostvm-0 → "ip netns exec host3 sh -c 'ping ...'"
  │     → parse success rate from ping output
  │
  │ 15. deprovision: remove services, teardown BGP/EVPN
  │ 16. verify-clean: verify CONFIG_DB returns to baseline
  │
  ▼
Results
  │
  │ 17. Determine final status (complete or failed)
  │ 18. SaveRunState → state.json
  │ 19. WriteMarkdown → newtrun/.generated/report.md
  │ 20. Exit code: 0 (all passed), 1 (failures), 2 (infra error)
```

---

## 13. CLI Reference

```
newtrun — E2E testing for newtron

Commands:
  start [suite]        Start or resume a test suite
  pause                Pause after current scenario
  stop                 Destroy topology and clean up state
  status               Show suite run status
  list [suite]         List suites or scenarios
  topologies           List available topologies
  actions [action]     List step actions or show action detail
  version              Print version information

Global Flags:
  -v, --verbose        Verbose output
```

### 13.1 start

```
newtrun start [suite] [flags]

Flags:
  --dir <path>           Directory containing scenario YAML files
  --scenario <name>      Run specific scenario (default: all)
  --topology <name>      Override topology
  --platform <name>      Override platform
  --junit <path>         JUnit XML output path
  --server <url>         newtron-server URL (env: NEWTRON_SERVER)
  --network-id <id>      Network identifier (env: NEWTRON_NETWORK_ID)
```

The suite argument can be a name (resolved under `newtrun/suites/`) or a
directory path. If a previous run was paused, `start` resumes automatically.

In lifecycle mode, the topology is deployed via `EnsureTopology` (reuses if
running) and kept running after completion. Use `stop` to tear down.

### 13.2 pause

```
newtrun pause [flags]

Flags:
  --dir <path>           Suite directory (auto-detected if omitted)
```

Signals the running suite to stop after the current scenario completes. The
topology remains deployed. Resume with `start`.

### 13.3 stop

```
newtrun stop [flags]

Flags:
  --dir <path>           Suite directory (auto-detected if omitted)
```

Destroys the deployed topology and removes suite state. Refuses to stop a suite
with a running process — use `pause` first.

### 13.4 status

```
newtrun status [flags]

Flags:
  --dir <path>           Suite directory
  --json                 JSON output
```

Without `--dir`, shows all suites with state. With `--dir`, shows detailed
status including per-scenario progress and current step.

### 13.5 list

```
newtrun list [suite] [flags]

Flags:
  --dir <path>           Directory containing scenario YAML files
```

Without arguments, lists all available suites. With a suite name, lists the
scenarios in that suite in dependency order.

### 13.6 actions

```
newtrun actions [action]
```

Without arguments, lists all registered step actions. With an action name,
shows the action's description and required parameters.

### 13.7 topologies

```
newtrun topologies
```

Lists available topologies from `newtrun/topologies/`.

### 13.8 version

```
newtrun version
```

Prints version and git commit.

### 13.9 Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed (or unknown error) |
| 2 | Infrastructure error (VM boot failure, SSH connection failure) |
