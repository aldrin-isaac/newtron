# newtrun — High-Level Design

For the architectural principles behind newtron, newtlab, and newtrun, see [Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md).

## 1. Purpose

newtrun is an E2E testing framework that tests **composed network outcomes** — not individual features. The question is not "does VLAN creation work?" but "does the L3VPN service produce reachability across the EVPN overlay?" A feature test can pass while the composite multi-feature configuration fails due to ordering issues, missing glue config, or daemon interaction bugs. newtrun tests the thing that actually matters: the assembled result.

newtrun is a general-purpose orchestration engine, not strictly a test framework. Users write topologies and scenarios as YAML files and spec directories. The built-in suites that ship with the project are examples; any topology that newtlab can deploy and any operation that newtron-server exposes can be exercised by a newtrun scenario. Test scenarios are one category of work it runs; the browser frontend's compose-and-run flows are another.

newtrun observes devices exclusively through newtron's HTTP API — it never accesses Redis directly. newtron returns structured data; newtrun decides what "correct" means by correlating observations across devices.

## 2. Three Tools, Clear Boundaries

newtrun sits between two tools that each do one thing well. Understanding the boundaries prevents the common mistake of putting cross-device logic in newtron or device-level logic in newtrun.

| Tool | Responsibility | Knows About |
|------|----------------|-------------|
| **newtron** | Opinionated single-device automation: translate specs → CONFIG_DB; verify own writes; observe single-device routing state | Specs, device profiles, Redis (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB) |
| **newtlab** | Realize VM topologies: deploy QEMU VMs from newtron's topology.json, wire socket links across servers | topology.json, platforms.json, QEMU |
| **newtrun** | Orchestrate sequenced multi-step work: run scenarios, manage run lifecycle, surface progress over HTTP | Scenarios, topologies, run state, the substrate exposed by newtron's HTTP API |

**Verification principle.** If a tool changes the state of an entity, that same tool must be able to verify the change had the intended effect. newtron writes CONFIG_DB and configures routing — so newtron owns verification of those changes. newtrun builds on newtron's self-verification by adding the cross-device layer: using newtron to observe each device, then correlating observations across devices using topology context. newtrun never accesses Redis directly — it observes devices exclusively through the newtron-server HTTP API.

## 3. Architecture

newtrun is split into two binaries: a thin HTTP client (`bin/newtrun`) and a long-lived server (`bin/newtrun-server`). The server owns scenario execution; the client is the operator entry point.

```
                                                                                              SSH
                                                                                              (host VMs only)
                                                                    ┌────────────────────────────────────────────────────────────────────┐
                                                                    │                                                                    ▼
┌──────────────┐                  ┌────────────────────┐          ┌─────────────────────────┐                     ┌──────────┐         ┌─────────────────┐
│              │                  │                    │          │                         │                     │          │         │                 │
│ bin/newtrun  │  HTTP            │ bin/newtrun-server │          │         Runner          │                     │ newtlab  │         │    QEMU VMs     │
│ (CLI client) │  /api/runs etc   │      (engine)      │  spawn   │ (per run, in goroutine) │  Go API             │ (Go API) │  QEMU   │ (SONiC + hosts) │
│              │ ───────────────▶ │                    │ ───────▶ │                         │ ──────────────────▶ │          │ ──────▶ │                 │
└──────────────┘                  └────────────────────┘          └─────────────────────────┘                     └──────────┘         └─────────────────┘
                                                                    │                                                                    ▲
                                                                    │ HTTP                                                               │
                                                                    ▼                                                                    │
                                                                  ┌─────────────────────────┐                                            │
                                                                  │                         │                                            │
                                                                  │     newtron-server      │  SSH                                       │
                                                                  │       (HTTP API)        │  (SONiC switches)                          │
                                                                  │                         │ ───────────────────────────────────────────┘
                                                                  └─────────────────────────┘
```

*Diagram source: [`docs/diagrams/newtrun-architecture.dot`](../diagrams/newtrun-architecture.dot).*

### 3.1 Two binaries, two roles

**`bin/newtrun` — CLI client.** Parses flags, builds HTTP requests, talks to newtrun-server. Every command — state-changing or read-only — goes through the server. The CLI never reads `~/.newtron/newtrun/` or `newtrun/suites/` directly. The server is the single source of truth: in-memory run registry plus the freshest persisted state.json plus the on-disk suite YAMLs all sit behind one HTTP surface, and the CLI cannot inadvertently surface a stale snapshot the server hasn't blessed. If the server isn't reachable, every command exits with `newtrun-server is not running` and a hint to start it. For `start`, the CLI subscribes to the server's Server-Sent Events stream and renders scenario / step events as they arrive, then exits with a code reflecting the terminal SuiteEnd result.

**`bin/newtrun-server` — the engine.** A long-lived process. Owns the `Runner` instances that execute scenarios, the in-memory registry that tracks active runs, the persistent state files under `~/.newtron/newtrun/`, and the HTTP server that exposes all of it. Each `POST /api/runs` request constructs a Runner in a goroutine and returns immediately with the run's identity; subsequent reads and event subscriptions see the run's state as it progresses.

### 3.2 The Runner

A Runner is a per-run orchestrator. Server-side, one Runner exists per in-flight run; each lives in its own goroutine with its own context. The Runner holds references to three external systems but implements no device logic itself:

| Field | Type | Talks to |
|-------|------|----------|
| `r.Client` | `*newtron-client.Client` | newtron-server over HTTP |
| `r.Lab` | `*newtlab.Lab` | newtlab Go API (deploy / destroy / ensure topologies) |
| `r.HostConns` | `map[string]*ssh.Client` | host VMs over SSH (for data-plane testing) |

**All SONiC operations go through HTTP.** The Runner creates a newtron HTTP client, registers the network spec directory with newtron-server, and every subsequent operation — provisioning, service lifecycle, health checks, route verification — is an HTTP request. newtron-server manages SSH connections to SONiC devices; newtrun never connects to them directly.

**Topology lifecycle goes through newtlab.** `EnsureTopology` reuses running VMs if all nodes are healthy, avoiding a full redeploy between iterations.

**Host devices use direct SSH.** The `host-exec` action runs commands inside network namespaces on host VMs. These are plain Linux VMs, not SONiC devices.

**No internal newtron imports.** newtrun imports `pkg/newtron/client/` (HTTP client), `pkg/newtlab/` (lab API), `pkg/newtron/` (public types), and shared utilities. It never imports `pkg/newtron/network/`, `pkg/newtron/network/node/`, or `pkg/newtron/device/sonic/`.

### 3.3 The run registry and concurrency

The server tracks active runs in an in-memory `RunRegistry` keyed by run identity. The identity is the suite name for file-backed runs or a fresh UUID for inline runs.

**Concurrency rules:**

- **Same-suite re-run blocked.** Two `POST /api/runs` requests for the same suite collide on the registry key; the second returns `409 Conflict` with the active run's age in the error message.
- **Different suites concurrent.** No contention between distinct suites.
- **Inline runs always concurrent.** Each `POST /api/runs/inline` allocates a fresh UUID; UUIDs never collide.

When `newtrun-server` shuts down, the registry cancels every in-flight runner's context and waits up to 5 seconds for them to drain before the HTTP listener stops.

### 3.4 URL resolution

The CLI resolves the newtrun-server URL through a three-tier cascade: `--newtrun-server` flag → `NEWTRUN_SERVER` environment variable → built-in default (`http://127.0.0.1:8081`). The server resolves the newtron-server URL it talks to per-request: the `newtron_server` field on the `POST /api/runs` body wins, otherwise the server's built-in default (`http://127.0.0.1:8080`) applies. The server binary currently has no CLI flag or env var for overriding that default — operators who need a non-default newtron-server set it per request, or build a wrapper.

The two servers have different default bind addresses. `newtrun-server` defaults to loopback (`127.0.0.1:8081`); non-loopback values trigger a startup warning that there is no built-in authentication. `newtron-server` defaults to all interfaces on port `8080` so single-node lab automation can reach it from inside containers and VMs without a flag — operators that need to restrict exposure pass `--addr 127.0.0.1:8080` explicitly. Neither server has built-in TLS or authentication; operators who need either wrap the server with a reverse proxy.

## 4. Directory Structure

newtrun's code lives in three places: CLI client (`cmd/newtrun/`), server entry point (`cmd/newtrun-server/`), and core library (`pkg/newtrun/` plus `pkg/newtrun/api/` and `pkg/newtrun/client/`). Test assets live at the repo root under `newtrun/`.

```
newtron/
├── cmd/
│   ├── newtrun/                  # CLI client (thin HTTP-client surface)
│   │   ├── main.go               # Root command, --newtrun-server flag, --verbose
│   │   ├── clientutil.go         # newClient factory, requireServer probe
│   │   ├── helpers.go            # resolveSuite, resolveTopologyFromState
│   │   ├── cmd_start.go          # POST /api/runs + SSE event renderer
│   │   ├── cmd_pause.go          # POST /api/runs/{suite}/pause
│   │   ├── cmd_stop.go           # multi-step orchestration: stop + destroy + delete
│   │   ├── cmd_status.go         # GET /api/runs + /api/runs/{suite} display
│   │   ├── cmd_list.go           # list suites and scenarios via GET /api/suites/...
│   │   ├── cmd_suites.go         # GET /api/suites
│   │   ├── cmd_scenario.go       # scenario CRUD + suite create/delete subcommands
│   │   ├── cmd_topologies.go     # GET /api/topologies
│   │   ├── cmd_actions.go        # static action vocabulary help
│   │   └── scenario_e2e_test.go  # CLI→server E2E: scenario lifecycle, bad-YAML rejection
│   └── newtrun-server/           # Server entry point
│       └── main.go               # --listen, --suites-base, --topologies-base
│
├── pkg/newtrun/                  # Engine (the orchestration core)
│   ├── scenario.go               # Scenario, Step, StepAction, ExpectBlock, BatchCall
│   ├── parser.go                 # ParseScenario, ParseScenarioBytes, ValidateDependencyGraph
│   ├── runner.go                 # Runner, RunOptions, Run(ctx, opts)
│   ├── steps.go                  # stepExecutor interface, multi-device helpers
│   ├── steps_newtron.go          # newtron action: URL expansion, jq, polling, batch
│   ├── steps_cli.go              # newtron-cli action: subprocess execution
│   ├── steps_host.go             # host-exec action: SSH command execution
│   ├── deploy.go                 # Deploy/Ensure/Destroy via newtlab
│   ├── state.go                  # RunState, ScenarioState, StepState (with DeviceOps)
│   │                             #   suite + _inline namespaces; LoadAnyRunState
│   ├── progress.go               # ProgressReporter (7 callbacks), consoleProgress, StateReporter
│   ├── errors.go                 # InfraError, StepError, PauseError
│   └── report.go                 # ScenarioResult, StepResult, ReportGenerator
│
├── pkg/newtrun/api/              # HTTP server package
│   ├── server.go                 # Server, Config, route registration, handleHealth, shared response helpers
│   ├── middleware.go             # withRequestID, withLogger, withRecovery
│   ├── runs.go                   # ALL run endpoints (start/inline/pause/stop/delete/list/get/events) + reconcileStaleStatus
│   ├── suites.go                 # GET/POST/DELETE /api/suites + list scenarios + nameRE validation
│   ├── scenarios.go              # GET/PUT/DELETE per-scenario; ParseScenarioBytes gate + atomic write
│   ├── topologies.go             # GET /api/topologies
│   ├── registry.go               # RunRegistry, RegistryEntry, AlreadyRunningError
│   ├── safety.go                 # InlineSafetyPolicy, SafetyViolation
│   ├── reporter.go               # HTTPReporter (implements ProgressReporter)
│   ├── broker.go                 # EventBroker (SSE multiplexer, drop-on-full)
│   └── types.go                  # APIResponse, EventType, payload types, request shapes
│
├── pkg/newtrun/client/           # HTTP client (used by CLI and future browser-side adapter)
│   └── client.go                 # All client methods + StreamEvents SSE parser
│
└── newtrun/                      # E2E test assets (repo root)
    ├── topologies/               # Per-topology spec directories
    └── suites/                   # Per-suite scenario YAMLs
```

The split between `pkg/newtrun/`, `pkg/newtrun/api/`, and `pkg/newtrun/client/` enforces a one-way import direction: `client` → `api` → `newtrun`. The engine package is HTTP-agnostic; the server package adapts the engine to HTTP; the client package consumes the HTTP surface.

## 5. Scenarios and Steps

A scenario is a YAML file that defines what to run against a deployed topology. Scenarios are the unit of authorship — users write scenarios to exercise specific network behaviors or to encode operator workflows.

### 5.1 Scenario structure

```yaml
name: provision
description: Provision switches and verify BGP convergence
topology: 2node-ngdp           # topology directory name
platform: 8101-32fh-vs         # optional platform override
requires: [boot-ssh]            # other scenarios that must pass first
after: [other-name]             # ordering only, no pass/fail gate
requires_features: [acl]        # platform features needed (skip if unsupported)
repeat: 5                       # stress mode (default 1)
steps:
  - name: provision-all
    action: topology-reconcile
    devices: all
  - name: verify-bgp
    action: newtron
    devices: all
    method: GET
    url: /node/{{device}}/bgp/check
    poll: { timeout: 90s, interval: 5s }
    expect: { jq: '.data | all(.status == "established")' }
```

A scenario is one YAML file under a suite directory. A **suite** is a directory of scenarios that share a topology. Files within a suite are processed in directory order unless `requires` / `after` declare dependencies; with dependencies, a topological sort produces the execution order.

### 5.2 Step actions

Six built-in actions:

| Action | Purpose |
|--------|---------|
| `topology-reconcile` | Provision a device via newtron's `/intent/reconcile?mode=topology` |
| `verify-topology` | Confirm device CONFIG_DB matches the topology spec |
| `wait` | Sleep for a duration (test-time delay; not for production scenarios) |
| `host-exec` | Run a shell command inside a network namespace on a host VM |
| `newtron` | Make an arbitrary newtron-server HTTP call with optional polling, batch, and jq expectations |
| `newtron-cli` | Run the newtron CLI as a subprocess (used for testing CLI behavior specifically) |

The `newtron` action is the most flexible. URLs use Go template syntax (`{{device}}`, `{{network}}`) expanded per target. Polling, batched call sequences, and `jq` expectations on the response let one action cover most operational and verification patterns.

### 5.3 Inline scenarios

Browser frontends submit scenarios inline through `POST /api/runs/inline` rather than authoring suite directories. The YAML body is parsed by the same `ParseScenarioBytes` the file-backed parser uses, then validated against the **inline safety policy** before the Runner starts:

- **Self-contained**: `requires` and `after` rejected — inline scenarios stand alone.
- **Action allow-list**: defaults to `newtron` and `wait` only. `host-exec` and `newtron-cli` are excluded by default because they shell out.
- **URL allow-list**: when configured, the `newtron` action's URL must match a registered prefix.
- **Topology-reconcile gate**: rejected unless the request opts in (`allow_reconcile: true`).
- **Wall-time budget**: default 60 seconds, configurable per request.

The browser composer / workbench / inbox surfaces submit inline scenarios in response to operator clicks. Each click is one one-shot scenario; safety guardrails enforce that operator-generated scenarios cannot, for instance, shell out to arbitrary commands.

## 6. Test Topologies

Topologies are pre-defined spec directories checked into the repo. Each contains the full newtron spec set: `topology.json`, `network.json`, `platforms.json`, `profiles/*.json`. newtrun reads them directly — no generation step.

### 6.1 Built-in topologies

| Topology | Devices | Purpose |
|----------|---------|---------|
| **1node-vs** | switch1 | Single-switch basic operations (sonic-vs) |
| **1node-vjunos** | r1 | Single vJunos-router smoke tests (opennetconf via `--newtlab`) |
| **2node-ngdp** | switch1, switch2 + host1–host6 | Disaggregated primitive testing |
| **2node-ngdp-service** | switch1, switch2 + host1–host8 | Service lifecycle with dataplane verification |
| **2node-vjunos** | r1, r2 across two parallel links | Aggregate / ECMP scenarios on vJunos-router |
| **2node-vs** | switch1, switch2 + host1–host6 | Disaggregated primitive testing (sonic-vs) |
| **2node-vs-service** | switch1, switch2 + host1–host8 | Service lifecycle, drift, orphan cleanup (sonic-vs) |
| **3node-ngdp** | spine, leaf1, leaf2 + host1, host2 | EVPN L2/L3 dataplane across a two-leaf fabric |
| **4node-ngdp** | spine1, spine2, leaf1, leaf2 | Full fabric (route reflectors on spines) |

#### 2node-ngdp

Two switches with three inter-switch links and three hosts per switch (source: `docs/diagrams/newtrun-topology-2node-ngdp.dot`):

```
                  ┌─────────────────────────┐
                  │                         │
                  │          host3          │
                  │                         │
                  └─────────────────────────┘
                    │
                    │ Eth3
                    │
┌───────┐         ┌─────────────────────────┐         ┌───────┐
│       │         │                         │         │       │
│ host1 │  Eth1   │         switch1         │  Eth2   │ host2 │
│       │ ─────── │                         │ ─────── │       │
└───────┘         └─────────────────────────┘         └───────┘
                    │
                    │ Eth0 / Eth4 / Eth5
                    │ (3 inter-switch links)
                    │
┌───────┐         ┌─────────────────────────┐         ┌───────┐
│       │         │                         │         │       │
│ host5 │  Eth2   │         switch2         │  Eth3   │ host6 │
│       │ ─────── │                         │ ─────── │       │
└───────┘         └─────────────────────────┘         └───────┘
                    │
                    │ Eth1
                    │
                  ┌─────────────────────────┐
                  │                         │
                  │          host4          │
                  │                         │
                  └─────────────────────────┘
```

No pre-configured services — interfaces are clean slates for disaggregated operation testing.

#### 2node-ngdp-service

Same switch pair with service-annotated interfaces. Each interface has a pre-assigned service in the topology spec; provisioning applies all services atomically. The extra host pair (host7/host8) exercises EVPN IRB overlay scenarios.

| switch1 port | service | host | switch2 port | service | host |
|--------------|---------|------|--------------|---------|------|
| Eth0 | transit | (peer Eth0) | Eth0 | transit | (peer Eth0) |
| Eth1 | local-irb | host1 | Eth1 | local-irb | host4 |
| Eth2 | local-bridge | host2 | Eth2 | local-bridge | host5 |
| Eth3 | l2-extend | host3 | Eth3 | l2-extend | host6 |
| Eth4 | overlay-irb-a | host7 | Eth4 | overlay-irb-b | host8 |
| Eth5 | — (inter-switch, no service) | (peer Eth5) | Eth5 | — | (peer Eth5) |

#### 2node-vs / 2node-vs-service

Sonic-vs variants of the 2node-ngdp topologies. Same logical structure, using the community sonic-vs platform (Force10-S6000 HWSKU, stride-4 port naming: Ethernet0, Ethernet4, …). The vs-service topology is shared by three suites — service lifecycle, drift detection, and orphan cleanup — each exercising different aspects of the same provisioned state.

#### 3node-ngdp

One spine connecting two leaves, one host per leaf (source: `docs/diagrams/newtrun-topology-3node-ngdp.dot`):

```
┌───────┐         ┌───────┐
│       │         │       │
│ leaf2 │  Eth1   │ spine │
│       │ ─────── │       │
└───────┘         └───────┘
  │                 │
  │ Eth1            │ Eth0
  │                 │
┌───────┐         ┌───────┐
│       │         │       │
│ host2 │         │ leaf1 │
│       │         │       │
└───────┘         └───────┘
                    │
                    │ Eth1
                    │
                  ┌───────┐
                  │       │
                  │ host1 │
                  │       │
                  └───────┘
```

Exercises EVPN L2/L3 forwarding across a two-leaf fabric with real data-plane verification between hosts.

#### 4node-ngdp

Full-mesh Clos topology: two spines with `route_reflector: true`, two leaves.

### 6.2 Spec files

Each topology directory contains:

| File | Read By | Contents |
|------|---------|----------|
| `topology.json` | newtlab + newtron | Devices, interfaces, links, newtlab settings |
| `network.json` | newtron | Services, filters, VPNs, zones |
| `platforms.json` | newtlab | Platform definitions with VM settings |
| `profiles/*.json` | newtlab + newtron | Per-device settings, EVPN config |

### 6.3 Custom topologies

The built-in topologies cover common patterns; newtrun works with any topology newtlab can deploy. Create a directory under `newtrun/topologies/<name>/specs/`, add the standard spec files, reference it from scenario YAML.

## 7. Verification Tiers

newtrun's four verification tiers match the layers data flows through. Each tier reads a different substrate, and one tool owns each:

| Tier | What | Substrate | Owner |
|------|------|-----------|-------|
| **Validation** | YANG schema, type rules | CONFIG_DB schema (newtron) | newtron |
| **Apply** | Writes landed in CONFIG_DB | CONFIG_DB (newtron's verify pass) | newtron |
| **Convergence** | Daemons settled, sessions up | STATE_DB / APP_DB / ASIC_DB (newtron health) | newtron |
| **Reachability** | Data plane forwarding correctly | Live traffic between host VMs | newtrun |

Tiers 1–3 are newtron concerns surfaced through `health`, `bgp/check`, `evpn/status`, etc. newtrun queries them via HTTP. Tier 4 is what newtrun uniquely owns: real packets between host VMs that prove the configured state actually forwards.

### 7.1 Platform-aware test skipping

Each scenario can declare `requires_features: [acl, macvpn, ...]`. The platform's `supports.json` lists what it implements. Scenarios with unsupported feature requirements skip cleanly without failing the suite. This lets the same suite run against multiple platforms (sonic-vs vs CiscoVS) and surface only the relevant scenarios.

## 8. Execution Model

The Runner is a per-run orchestrator that lives inside the server. Each `POST /api/runs` request constructs one Runner in its own goroutine, with its own context, its own newtron client, its own lab and host connections.

### 8.1 The run lifecycle

```
                                              ┌──────────────────────────┐
                                              │                          │
                                              │  CLI / browser frontend  │  event stream
                                              │                          │ ◀─────────────────┐
                                              └──────────────────────────┘                   │
                                                │                                            │
                                                │                                            │
                                                ▼                                            │
                                              ┌──────────────────────────┐                   │
                                              │                          │                   │
                                              │      POST /api/runs      │                   │
                                              │   or /api/runs/inline    │                   │
                                              │                          │                   │
                                              └──────────────────────────┘                   │
                                                │                                            │
                                                │                                            │
                                                ▼                                            │
                                              ┌──────────────────────────┐                   │
                                              │                          │                   │
                                              │ RunRegistry.Acquire(key) │                   │
                                              │                          │                   │
                                              └──────────────────────────┘                   │
                                                │                                            │
                                                │ initial                                    │
                                                ▼                                            │
                                              ┌──────────────────────────┐                   │
                                              │                          │                   │
                                              │       SaveRunState       │                   │
                                              │    (suite or _inline)    │                   │
                                              │                          │ ◀┐                │
                                              └──────────────────────────┘  │                │
                                                │                           │                │
                                                │                           │                │
                                                ▼                           │                │
┌──────────────────────────────┐              ┌──────────────────────────┐  │                │
│                              │              │                          │  │                │
│    ProgressReporter chain    │              │     spawn goroutine:     │  │                │
│ StateReporter + HTTPReporter │  callbacks   │  Runner.Run(ctx, opts)   │  │ final          │
│                              │ ◀─────────── │                          │  │                │
└──────────────────────────────┘              └──────────────────────────┘  │                │
  │                                             │                           │                │
  │ publish                                     │ terminal                  │                │
  ▼                                             ▼                           │                │
┌──────────────────────────────┐              ┌──────────────────────────┐  │                │
│                              │              │                          │  │                │
│         EventBroker          │              │     finalizeRunState     │  │                │
│                              │              │         Release          │  │                │
│                              │              │                          │ ─┘                │
└──────────────────────────────┘              └──────────────────────────┘                   │
  │                                                                                          │
  │ multiplex                                                                                │
  ▼                                                                                          │
┌──────────────────────────────┐                                                             │
│                              │                                                             │
│  GET /api/runs/{id}/events   │                                                             │
│      (SSE subscribers)       │                                                             │
│                              │ ────────────────────────────────────────────────────────────┘
└──────────────────────────────┘
```

*Diagram source: [`docs/diagrams/newtrun-run-lifecycle.dot`](../diagrams/newtrun-run-lifecycle.dot).*

The flow:

1. **Client submits.** `POST /api/runs` (file-backed) or `POST /api/runs/inline` (with scenario YAML in the body) hits the server.
2. **Registry acquire.** The server reserves the run key (suite name or fresh UUID). Same-suite collision returns 409 immediately.
3. **State persisted.** Initial `RunState` written to `~/.newtron/newtrun/<key>/state.json` (suite namespace) or `~/.newtron/newtrun/_inline/<uuid>/state.json` (inline namespace).
4. **Runner spawned.** A goroutine constructs a Runner, attaches the reporter chain (HTTPReporter wrapping StateReporter), and calls `Runner.Run(ctx, opts)`. The HTTP handler returns 202 immediately with the run identity.
5. **Reporter callbacks fire.** Each suite/scenario/step start and end emits a callback. The chain persists per-callback state changes to disk and publishes events to the EventBroker.
6. **Events multiplexed.** The EventBroker fans events out to SSE subscribers. Clients see events as they happen.
7. **Run finalizes.** When the Runner returns, `finalizeRunState` writes the terminal status and the registry releases the key.

### 8.2 Scenario iteration

Inside a Runner, scenarios execute in dependency order (topologically sorted from `requires` / `after`). Scenarios with failed requirements skip. At every scenario boundary, the Runner checks two signals: the context (cancellation from server shutdown or stop request) and the file-based pause flag (`CheckPausing` reads the state file).

Each scenario iterates its steps sequentially. Steps with `devices: all` or a device list fan out to per-device execution; per-device results aggregate into one StepResult.

### 8.3 The reporter chain

Every Runner uses a chain of `ProgressReporter` implementations, each forwarding callbacks to the next via an `Inner` field:

```
HTTPReporter (publishes events to the EventBroker)
    │
    └─→ StateReporter (persists each callback to state.json)
            │
            └─→ (nil; server-side runners do not write to the terminal)
```

When the same scenario is run via the CLI client, the client subscribes to the server's SSE stream and renders events to the terminal locally — the server itself does not write to stdout.

The seven `ProgressReporter` callbacks: `SuiteStart`, `ScenarioStart`, `StepStart`, `StepProgress`, `StepEnd`, `ScenarioEnd`, `SuiteEnd`. `StepProgress` fires when a producer emits a per-device-operation event (currently no producer is shipping in this repo; the activation depends on newtron-server emitting SSE on its apply endpoints).

## 9. Suite Lifecycle

A "run" goes through a small set of named states. The state machine differs slightly for suite-keyed runs (which can be paused and resumed) and inline runs (which are one-shot).

### 9.1 State machine

Source: `docs/diagrams/newtrun-suite-statemachine.dot`. Re-render with `graph-easy --from=dot --boxart < docs/diagrams/newtrun-suite-statemachine.dot`.

```
                                               ┌────────────────────┐
                                               │                    │
                                               │       start        │
                                               │                    │
                                               └────────────────────┘
                                                 │
                                                 │ POST /api/runs
                                                 ▼
┌───────────────────┐                          ┌──────────────────────────────────────────────────────────────────────┐
│                   │                          │                                                                      │
│ complete / failed │  all scenarios end       │                               running                                │
│                   │ ◀─────────────────────── │                                                                      │
└───────────────────┘                          └──────────────────────────────────────────────────────────────────────┘
                                                 │                     │                     ▲
                                                 │ pause               │                     │ POST /api/runs (resume)
                                                 ▼                     │                     │
┌───────────────────┐                          ┌────────────────────┐  │                     │
│                   │                          │                    │  │                     │
│      paused       │  current scenario ends   │      pausing       │  │                     │
│                   │ ◀─────────────────────── │                    │  │                     │
└───────────────────┘                          └────────────────────┘  │ stop / ctx cancel   │
  │                                              │                     │                     │
  │                                              │ stop / ctx cancel   │                     │
  │                                              ▼                     │                     │
  │                                            ┌────────────────────┐  │                     │
  │                                            │                    │  │                     │
  │                                            │      aborted       │  │                     │
  │                                            │                    │ ◀┘                     │
  │                                            └────────────────────┘                        │
  │                                                                                          │
  └──────────────────────────────────────────────────────────────────────────────────────────┘
```

- **running**: Runner goroutine is active in the server's process.
- **pausing**: Pause was requested. Runner picks up the signal at the next scenario boundary and transitions to paused.
- **paused**: Runner exited cleanly between scenarios. State preserved; `POST /api/runs` against the same suite resumes from where it stopped.
- **complete**: All scenarios ran successfully.
- **failed**: At least one scenario failed.
- **aborted**: Context cancellation (stop endpoint or server shutdown) before completion.

### 9.2 Lifecycle commands

| Verb | Endpoint | What it does |
|------|----------|--------------|
| Start | `POST /api/runs` | Create a new run, or resume a paused suite |
| Pause | `POST /api/runs/{id}/pause` | Write `pausing` to state; Runner exits cleanly between scenarios |
| Stop | `POST /api/runs/{id}/stop` | Cancel the Runner's context immediately |
| Delete | `DELETE /api/runs/{id}` | Remove persistent state (rejected while active) |
| Read | `GET /api/runs/{id}` | Current `RunState` |
| Stream | `GET /api/runs/{id}/events` | SSE event stream |

The CLI verbs (`newtrun start`, `newtrun pause`, `newtrun stop`) translate one-to-one to these endpoints. `newtrun stop` additionally calls `newtlab.Destroy` to tear down the topology before sending `DELETE`.

### 9.3 Server-restart honesty

When `newtrun-server` shuts down (signal or crash), the registry releases its in-memory state. Any active runs leave their state files behind in `running` status. The next server startup does not automatically reconcile these — a run marked `running` whose Runner no longer exists is stale. A cleanup pass to mark such runs `abandoned` on startup is tracked as a follow-on item.

## 10. Host Devices and Data Plane

newtrun's distinctive verification tier reads the data plane: real ICMP between host VMs, real iperf throughput, real BGP/EVPN distribution observed end-to-end.

### 10.1 VM coalescing

Each topology declares some number of host VMs (`host1`, `host2`, …). newtlab does not deploy one QEMU process per host; it groups hosts on shared VMs to save resources, then creates a network namespace per host inside the shared VM. From newtrun's perspective each "host" is independently addressable, but multiple hosts on the same shared VM share its kernel and SSH daemon.

### 10.2 Host actions

The `host-exec` action takes a `devices` selector and a shell command. The Runner SSH-connects to the host's containing VM (caching the connection per VM), runs the command inside the host's network namespace, captures stdout/stderr. Common use cases: `ping`, `iperf`, `tcpdump`, `ip route show`.

### 10.3 Automatic host skipping

A scenario that targets `host1` runs against the topology declarations, not the deployed VMs. If the topology declares no host of that name, the scenario skips with a clear reason instead of failing. This lets the same scenario file work against topologies of different sizes (1node-vs has no hosts; 2node-ngdp has six; 3node-ngdp has two).

## 11. Output and Reporting

The server is the source of truth for run state. Multiple consumers can read the state simultaneously through different APIs.

### 11.1 Live observation via SSE

`GET /api/runs/{id}/events` opens a Server-Sent Events stream. The CLI's `newtrun start` subscribes and renders one line per scenario / step / suite event to the terminal. Browser frontends subscribe through the same endpoint. The stream's initial event is a comment line confirming subscription; heartbeats every 30 seconds prevent intermediaries from timing out the connection.

### 11.2 Persistent state file

`~/.newtron/newtrun/<key>/state.json` (suite namespace) or `~/.newtron/newtrun/_inline/<uuid>/state.json` (inline namespace) is updated after every callback. The file is a complete `RunState` snapshot — operators can `cat` it directly or fetch it via `GET /api/runs/{id}`. Mid-flight, the file reflects the current step's status; after termination, it reflects the final result.

### 11.3 Reports and live monitor

After a CLI-driven `start` run finishes, the CLI writes a markdown report to `newtrun/.generated/report.md` summarizing scenario status, duration, and per-step results. The `--junit <path>` flag additionally writes a JUnit XML report at the named path, suitable for CI consumption. Both reports are built from the SSE event stream the CLI already subscribed to — they are not separate API calls.

`newtrun start --monitor` replaces the per-event terminal renderer with an auto-refreshing dashboard backed by `~/.newtron/newtrun/<suite>/state.json`. The SSE subscription still runs in the background so the run's pass/fail/error status can be reflected in the CLI's exit code, but the operator's view is the dashboard rather than the event log. `newtrun status --monitor` opens the same dashboard against an already-running suite without starting one.

## 12. End-to-End Walkthrough

A concrete trace of `bin/newtrun start 2node-ngdp-primitive` from operator keystroke to terminal output.

### 12.1 Operator runs the CLI

```
$ NEWTRUN_SERVER=http://127.0.0.1:8081 bin/newtrun start 2node-ngdp-primitive
```

`cmd_start.go`:
1. Reads the persistent `--server` flag and the `NEWTRUN_SERVER` env var, settles on `http://127.0.0.1:8081`.
2. Constructs a `client.Client` targeting that URL.
3. Probes `GET /api/health` to confirm the server is running. If not, exits with a "start newtrun-server first" hint.
4. Sends `POST /api/runs` with body `{"suite": "2node-ngdp-primitive", "all": true, ...}`.
5. Subscribes to `GET /api/runs/2node-ngdp-primitive/events` (SSE).
6. For each event received, renders it to the terminal.

### 12.2 Server accepts the request

`pkg/newtrun/api/runs.go` `handleStartRun`:
1. Decodes the `StartRunRequest`.
2. Calls `s.registry.Acquire("2node-ngdp-primitive")`. If another run holds the key, returns 409.
3. Constructs a `RunState`, calls `SaveRunState`.
4. Builds the reporter chain: `HTTPReporter` (RunKey: "2node-ngdp-primitive", publishes to `s.broker`) wrapping `StateReporter`.
5. Constructs a `newtrun.Runner`, attaches the reporter, sets `runner.ServerURL` to the configured newtron-server URL.
6. Creates a cancellable context, stores `cancel` on the registry entry.
7. Spawns a goroutine that calls `runner.Run(ctx, opts)`.
8. Returns `202 Accepted` with `{"suite": "2node-ngdp-primitive", "started": "..."}`.

### 12.3 Server-side Runner executes

`pkg/newtrun/runner.go` `Run`:
1. Parses every scenario YAML under `newtrun/suites/2node-ngdp-primitive/`.
2. Topologically sorts scenarios by `requires` / `after`.
3. Connects to newtron-server to learn the topology name and spec dir.
4. Calls `SuiteStart` — every reporter forwards the event.
5. Deploys the topology via newtlab (`DeployTopology`).
6. For each scenario in order: `ScenarioStart`, iterate steps, `ScenarioEnd`. Steps dispatch through `stepExecutor` interface implementations.
7. After all scenarios: `SuiteEnd` with aggregate results.

### 12.4 Events flow to the CLI

For each `Reporter` callback:
1. The HTTPReporter constructs an `Event` with a typed payload.
2. The Event is published to `s.broker.Publish("2node-ngdp-primitive", event)`.
3. The broker fans out to every subscriber's channel.
4. The CLI's SSE handler receives the event, decodes it, prints a line.

### 12.5 Run finalizes

When `Runner.Run` returns:
1. The goroutine calls `finalizeRunState` which writes the terminal status to disk.
2. `s.registry.Release(...)` closes the entry's `Done` channel and removes the key.
3. The final `SuiteEnd` event reaches the CLI.
4. The CLI cancels its SSE context, exits with code 0 (success), 1 (test failure), or 2 (infrastructure error).

A run that the operator paused with `bin/newtrun pause` follows the same flow but the Runner exits at the next scenario boundary with a `PauseError`. `finalizeRunState` marks state `paused`. A subsequent `bin/newtrun start` request resumes from the next-unprocessed scenario.

## 13. CLI Reference

Every command except `actions` and `version` requires newtrun-server to be running. The CLI never reads run state, suite YAMLs, or topology specs from disk on its own — it asks the server. When the server is unreachable, the CLI exits non-zero with `newtrun-server is not running` and the start hint. The endpoints each command calls are documented in [api.md](api.md).

| Command | Endpoint(s) | Notes |
|---------|-------------|-------|
| `newtrun start <suite>` | `POST /api/runs` + SSE | Streams events; exits on terminal SuiteEnd. Resumes if the suite is paused. |
| `newtrun pause <suite>` | `POST /api/runs/{suite}/pause` | Returns when the pause signal lands; Runner exits between scenarios |
| `newtrun stop <suite>` | `GET` + `POST /stop` + newtlab.Destroy + `DELETE` | Multi-step: cancel runner, destroy topology, clean state |
| `newtrun status [suite]` | `GET /api/runs` + `GET /api/runs/{suite}` | All suites or one; `--monitor` auto-refreshes |
| `newtrun list [suite]` | `GET /api/suites` + `GET /api/suites/{suite}/scenarios` | Lists suites; with a suite name lists its scenarios |
| `newtrun suites` | `GET /api/suites` | Lists suite directories under the server's suites base |
| `newtrun suite create <name>` | `POST /api/suites` | Creates an empty suite directory on the server |
| `newtrun suite delete <name>` | `DELETE /api/suites/{suite}` | Deletes an empty suite directory; 409 if scenarios remain |
| `newtrun scenario list <suite>` | `GET /api/suites/{suite}/scenarios` | Same data as `list <suite>` |
| `newtrun scenario get <suite> <name>` | `GET /api/suites/{suite}/scenarios/{name}` | Prints raw scenario YAML to stdout |
| `newtrun scenario put <suite> <name>` | `PUT /api/suites/{suite}/scenarios/{name}` | Creates or updates a scenario from `--file` or stdin; validated via ParseScenarioBytes |
| `newtrun scenario delete <suite> <name>` | `DELETE /api/suites/{suite}/scenarios/{name}` | Deletes a scenario file |
| `newtrun topologies` | `GET /api/topologies` | Lists topology directories under the server's topologies base |
| `newtrun actions` | static | Help text describing the action vocabulary |
| `newtrun version` | static | Build version |

`newtrun start` flags:

| Flag | Meaning |
|------|---------|
| `--dir <path>` | Run the suite at the given directory (replaces the positional name) |
| `--scenario <name>` | Run a single scenario by name |
| `--target <name>` | Run the minimum dependency chain that reaches the named scenario |
| `--platform <name>` | Override the platform from the scenario YAML |
| `--no-deploy` | Skip topology deployment (loopback / offline mode) |
| `--monitor`, `-m` | Render an auto-refreshing dashboard instead of the per-event log |
| `--junit <path>` | Write a JUnit XML report to the named path |
| `--server <url>` | newtron-server URL (env: `NEWTRON_SERVER`) — passed to the server-side Runner |
| `--network-id <id>` | newtron network identifier (env: `NEWTRON_NETWORK_ID`) |

Global flags: `--newtrun-server <url>` (newtrun-server URL; env: `NEWTRUN_SERVER`), `-v / --verbose` (more terminal output).

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | At least one scenario failed |
| 2 | Infrastructure error (deploy / connection / etc.) |
| Other | Standard signal codes |
