# newtrun — HOWTO Guide

newtrun is an E2E testing orchestrator for SONiC network devices. It deploys VM topologies via newtlab, runs declarative YAML scenarios against newtron-managed switches, and reports results.

**Architecture in one paragraph.** `bin/newtrun` is a thin CLI client. The actual work — parsing scenarios, deploying topologies, executing steps, streaming progress — happens in the newtrun engine, which is hosted by `bin/newt-server` (the aggregated server running newtron, newtrun, and newtlab engines on `:18080`). The newtrun engine calls newtron via HTTP for every device operation; in the aggregated process those HTTP calls land in the in-process newtron engine. Every CLI command requires a server to be running; if it isn't, the CLI prints `newtrun-server is not running` and exits non-zero. See the [HLD](hld.md) for the architecture rationale, the [API reference](api.md) for endpoint shapes, and the [LLD](lld.md) for type definitions.

This guide is organized by how often you'll use each thing. Running an existing suite comes first (most common); writing your own scenarios from scratch comes later (less common); troubleshooting and command reference are at the end.

---

## Table of Contents

1. [Prerequisites & Build](#1-prerequisites--build)
2. [Quick Start](#2-quick-start)
3. [End-to-End Workflow](#3-end-to-end-workflow)
4. [Running an Existing Suite](#4-running-an-existing-suite)
5. [Monitoring a Run](#5-monitoring-a-run)
6. [Pausing and Resuming](#6-pausing-and-resuming)
7. [Stopping a Run](#7-stopping-a-run)
8. [Browsing Suites and Scenarios](#8-browsing-suites-and-scenarios)
9. [Authoring Scenarios](#9-authoring-scenarios)
10. [Writing Scenarios from Scratch](#10-writing-scenarios-from-scratch)
11. [Step Action Reference](#11-step-action-reference)
12. [Data Plane Tests](#12-data-plane-tests)
13. [CI/CD Integration](#13-cicd-integration)
14. [Troubleshooting](#14-troubleshooting)
15. [Command Reference](#15-command-reference)

---

## 1. Prerequisites & Build

You'll need:

- **Linux x86_64** with KVM (`/dev/kvm` readable by your user)
- **Go 1.24+** (`go version`)
- **QEMU** (`qemu-system-x86_64`)
- A **SONiC VM image** at `~/.newtlab/images/sonic-vs.qcow2`

Build the binaries:

```bash
make build
```

This produces `bin/newtron`, `bin/newtron-server`, `bin/newtlab`, `bin/newtlab-server`, `bin/newtlink`, `bin/newtrun`, `bin/newtrun-server`, and `bin/newt-server`.

Download a SONiC community image (one-time):

```bash
mkdir -p ~/.newtlab/images
curl -fSL "https://sonic-build.azurewebsites.net/newtrun/v1/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.img.gz" \
  | gunzip > ~/.newtlab/images/sonic-vs.qcow2
```

The [getting-started.sh](../../scripts/getting-started.sh) script automates all of the above plus a single-switch deploy and a first-suite walkthrough. Read on if you want to do it by hand.

---

## 2. Quick Start

The fastest path exercises the CLI's `--loopback` mode against an in-memory abstract node — no QEMU VMs required. Start newt-server and run the loopback suite:

```bash
# 1. Start newt-server (port 18080 — runs newtron, newtrun, newtlab in one process)
bin/newt-server --spec-dir newtrun/topologies/1node-vs/specs &

# 2. Run the loopback suite (--no-deploy skips lab deployment)
bin/newtrun start 1node-vs-config --no-deploy
```

For suites that need real VMs (e.g., `2node-vs-primitive`, `2node-vs-service`), deploy the lab first with `bin/newtlab deploy <topology> --monitor` and drop `--no-deploy`.

Expected output:

```
newtrun: started suite 1node-vs-config at 2026-05-30T10:00:00-07:00
newtrun: 17 scenarios

  [1/17] setup-device
          PASS (<1s)
  [2/17] topology-show
          PASS (<1s)
  ...
  [17/17] intent-projection
          PASS (<1s)

---
newtrun: 17 scenarios — 17 passed, 0 failed, 0 errored, 0 skipped (1s)
```

Exit code 0. Markdown report at `newtrun/.generated/report.md`.

**Why one server?**

`bin/newt-server` runs the newtron, newtrun, and newtlab engines in one process on `:18080`. The aggregator mounts each engine's HTTP handler on a shared mux and dispatches by URL prefix (`/newtron/v1/...`, `/newtrun/v1/...`, `/newtlab/v1/...`). See [`docs/newt-server.md`](../newt-server.md) for the rationale. For dev iteration on a single engine without restarting the others, use the standalone binaries (`bin/newtron-server`, `bin/newtrun-server`, `bin/newtlab-server`) on their loopback defaults (`:19080`, `:19081`, `:19082`).

---

## 3. End-to-End Workflow

The full lifecycle of a real test, derived from the 2node-vs-primitive suite (21 scenarios, ~7m on a fresh lab).

**Topology:** 2node-vs — two SONiC switches connected by a fabric link, six host VMs attached for data-plane testing. See `newtrun/topologies/2node-vs/specs/topology.json` for the exact wiring.

```bash
# 1. Deploy the lab (8 nodes; ~1 minute on first boot)
bin/newtlab deploy 2node-vs --monitor

# 2. Start newt-server pointing at this topology's specs
bin/newt-server --spec-dir newtrun/topologies/2node-vs/specs &

# 3. Run the suite (deploys hosts via SSH; ~7 minutes)
bin/newtrun start 2node-vs-primitive
```

While the suite runs, monitor in another terminal:

```bash
bin/newtrun status --suite 2node-vs-primitive --monitor
```

The dashboard refreshes every 2 seconds with per-scenario progress and per-step status. Press Ctrl-C to detach (the suite keeps running).

When the suite finishes, exit code is 0 (all PASS). State persists at `~/.newtron/newtrun/2node-vs-primitive/state.json`; the markdown report is at `newtrun/.generated/report.md`.

Tear down:

```bash
bin/newtrun stop 2node-vs-primitive            # cancel + destroy topology + clear state
pkill -f "bin/newt-server"                     # stop newt-server (one process)
```

---

## 4. Running an Existing Suite

### 4.1 Basic invocation

```bash
bin/newtrun start <suite-name> --server http://localhost:18080
```

The CLI POSTs to `newtrun-server`, which spawns a Runner goroutine and starts streaming SSE events back to the CLI. The CLI renders each event as it arrives and exits when the run terminates.

| Termination cause | Exit code | Stderr |
|-------------------|-----------|--------|
| All scenarios PASS | 0 | summary line |
| Any FAIL or ERROR | 1 | summary line |
| TCP connection dropped mid-stream | 2 | `infrastructure error: newtrun-server connection lost mid-run; check state.json for the last persisted status` |
| Stream closed cleanly but SuiteEnd never arrived (server reaped between events) | 2 | `infrastructure error: stream ended without SuiteEnd; the server may have been shut down mid-run` |
| SuiteEnd carried `status: aborted` (server SIGTERMed) | 2 | `infrastructure error: run was aborted (server shut down)` |

### 4.2 Suite selection

| Form | Behavior |
|------|----------|
| `newtrun start <name>` | Resolves under newtrun-server's `--suites-base` (default `newtrun/suites/<name>`). |
| `newtrun start --dir /path/to/suite` | Absolute path to a suite directory; mutually exclusive with the positional name. |

### 4.3 Scenario selection

By default, `newtrun start` runs every scenario in the suite. Three flags narrow that:

| Flag | Effect |
|------|--------|
| `--scenario <name>` | Run only the named scenario. |
| `--target <name>` | Run the minimum dependency chain (per `requires:`) reaching the named scenario. |
| (no flag) | All scenarios, topologically sorted by `requires` + `after`. |

```bash
# One scenario
bin/newtrun start 2node-vs-primitive --scenario boot-ssh --server http://localhost:18080

# Dependency chain up to a target
bin/newtrun start 2node-vs-primitive --target bridged --server http://localhost:18080
```

### 4.4 Other flags

| Flag | Meaning |
|------|---------|
| `--no-deploy` | Skip topology deployment and host SSH connections. Use for loopback suites (e.g., `1node-vs-config`) or when the lab is already up. |
| `--platform <name>` | Override the platform declared in `suite.yaml`. |
| `--junit <path>` | Write a JUnit XML report at `<path>` after the run finishes. |
| `--monitor` / `-m` | Replace the per-event terminal output with an auto-refreshing dashboard backed by `state.json`. |
| `--network-id <id>` | newtron network identifier (env: `NEWTRON_NETWORK_ID`). Empty by default — the server derives the id from `suite.Topology` so two suites against one newt-server don't compete for the `default` slot (#116). |
| `--server <url>` | newtron-server URL (env: `NEWTRON_SERVER`). Passed to every server-side scenario step. |

### 4.5 Preconditions

| Precondition | How to check | How to fix |
|--------------|-------------|------------|
| newt-server up on `:18080` | `curl http://localhost:18080/newt-server/v1/health` | Start with `bin/newt-server --spec-dir <path> &` |
| newtron route reachable (newt-server has loaded the network) | `curl http://localhost:18080/newtron/v1/networks` | Pass `--spec-dir <path>` when starting newt-server |
| Topology deployed (unless `--no-deploy`) | `bin/newtlab list` | `bin/newtlab deploy <topology> --monitor` |
| Suite name matches a directory under `--suites-base` | `bin/newtrun list` | Use the exact name or `--dir <path>` |
| No active run for the same suite | `bin/newtrun status --suite <name>` | Wait, pause, or `bin/newtrun stop <name>` |

If any precondition fails, the CLI exits with a clear message naming the missing piece.

### 4.6 Worked example: rerun a single scenario

You changed `evpn-bridged.yaml` and want to verify just that scenario:

```bash
bin/newtrun start 2node-vs-primitive \
    --scenario evpn-bridged \
    --server http://localhost:18080
```

If `evpn-bridged` has `requires: [evpn-verify]`, the dependency-graph validator allows running it solo (it doesn't auto-run dependencies). To get the dependency chain, use `--target` instead:

```bash
bin/newtrun start 2node-vs-primitive \
    --target evpn-bridged \
    --server http://localhost:18080
```

Now boot-ssh → setup-device → ... → evpn-verify → evpn-bridged run in order.

---

## 5. Monitoring a Run

### 5.1 Per-event streaming (default)

`newtrun start` subscribes to the SSE event stream and renders each event:

```
  [3/17] vlan-lifecycle
          PASS (1s)
  [4/17] vrf-lifecycle
          PASS (1s)
```

`-v` adds per-step output:

```
  [3/17] vlan-lifecycle
          [1/10] create-vlan PASS (<1s)
          [2/10] verify-vlan PASS (<1s)
          ...
          PASS (1s)
```

### 5.2 Status dashboard

`newtrun status` reads the run state via HTTP:

```bash
# All suites
bin/newtrun status

# One suite by name
bin/newtrun status --suite 2node-vs-primitive

# Filter by name substring
bin/newtrun status --suite ngdp

# Auto-refresh every 2s (implies --detail)
bin/newtrun status --suite 2node-vs-primitive --monitor

# JSON for scripting
bin/newtrun status --json
```

Example output for one suite:

```
newtrun: 2node-vs-primitive
  suite:     newtrun/suites/2node-vs-primitive
  topology:  2node-vs (deployed, 8 nodes running)
  platform:  default
  status:    running
  started:   2026-05-30 10:00:00 (5m ago)
  scenarios: 21 (375 steps total)

  #   SCENARIO           STEPS  STATUS  REQUIRES                      DURATION
  --  -----------------  -----  ------  ----------------------------  ---------------------------
  1   boot-ssh           3      PASS    —                             1m31s
  2   setup-device       19     PASS    boot-ssh                      9s
  3   bridged            15     PASS    boot-ssh                      51s
  4   routed             11     PASS    boot-ssh                      6s
  5   bgp-underlay       14     PASS    setup-device                  34s
  6   interface-props    12     PASS    setup-device                  3s
  ...
  18  teardown-overlay   26     —       evpn-irb, cross-switch
  19  service-lifecycle  33     running teardown-overlay              step 16/33: wait-after-prime
  20  teardown-infra     10     —       service-lifecycle
  21  verify-clean       52     —       teardown-infra

  progress: 18/21 passed, 2 pending
```

`--detail` (`-d`) expands each completed scenario to show per-step results below the table.

### 5.3 Streaming events directly

The CLI uses Server-Sent Events under the hood; you can subscribe yourself with curl for debugging:

```bash
curl -N http://localhost:18080/newtrun/v1/runs/2node-vs-primitive/events
```

Each frame has an `event: <type>` and `data: <json>` line. See the [API reference §5](api.md#5-run-events-sse) and [§10 SSE Event Reference](api.md#10-sse-event-reference) for the payload schemas.

---

## 6. Pausing and Resuming

Long suites can be paused at scenario boundaries. The Runner finishes the current scenario, then exits gracefully:

```bash
bin/newtrun pause 2node-vs-primitive
```

Output: `pausing suite 2node-vs-primitive; will stop after current scenario`.

The pause signal propagates via `state.json` — the server writes `status: pausing`, the Runner picks it up at the next iteration boundary. When the Runner exits, `status: paused` is the persisted state.

### 6.1 Resume from pause

A subsequent `newtrun start` for the same suite resumes from where pause left off:

```bash
bin/newtrun start 2node-vs-primitive --server http://localhost:18080
```

Scenarios that already passed are reported as SKIP with reason `already passed (resumed)`:

```
  [1/21] boot-ssh
          SKIP (<1s)
  [2/21] setup-device
          SKIP (<1s)
  ...
  [10/21] irb
          PASS (20s)
```

The Runner reads `state.json`, builds `opts.Completed[name] = PASS` for each previously-passed scenario, and sets `opts.Resume = true`. The pause boundary is scenario-level, not step-level — a scenario that was mid-execution restarts from scratch.

### 6.2 Pause vs stop

| Operation | What happens | When to use |
|-----------|--------------|-------------|
| `pause` | Runner exits at next scenario boundary; topology stays deployed; state persists. | You want to inspect intermediate state, debug, then continue. |
| `stop` | Runner's context is canceled (next ctx-check exits immediately); topology destroyed; state removed. | You want to abandon the run entirely. |

---

## 7. Stopping a Run

`newtrun stop` is a multi-step orchestration that ensures clean tear-down:

```bash
bin/newtrun stop 2node-vs-primitive
```

The CLI:

1. Reads the suite's state via HTTP to recover the topology + spec-dir.
2. POSTs to `/newtrun/v1/runs/{suite}/stop` — cancels the Runner's context.
3. Waits for the Runner goroutine to exit.
4. Calls the `newtlab.Lab.Destroy` Go API in-process to tear down VMs (no `bin/newtlab` subprocess; `cmd/newtrun/cmd_stop.go:51`).
5. Sends `DELETE /newtrun/v1/runs/{suite}` to clear the state file.

If the suite isn't currently active (no Registry entry, but state exists from a previous run), steps 2 and 3 are skipped — only the topology destroy and state cleanup run.

```
$ bin/newtrun stop 2node-vs-primitive
destroying topology 2node-vs...
suite 2node-vs-primitive stopped and cleaned up
```

The CLI exits 0 when every step succeeded. If any step fails (e.g., newtlab can't destroy because a VM never reached running state), the CLI reports which step failed and exits non-zero — the other cleanups still happen.

---

## 8. Browsing Suites and Scenarios

Every read goes through newtrun-server (strict Option A). The CLI has dedicated subcommands; the same data is available via the API reference.

```bash
# List suites visible to the server
bin/newtrun list

# List scenarios in a suite (dependency-ordered)
bin/newtrun list 2node-vs-primitive

# Hidden alias matching the API endpoint name
bin/newtrun suites

# List server-known topologies
bin/newtrun topologies

# Bootstrap a new empty topology directory (writes topology.json, platforms.json,
# network.json, and an empty profiles/ subdirectory under newtrun/topologies/<name>/specs/).
# The next step is to register it as a newtron network via POST /newtron/v1/networks
# with the printed spec_dir.
bin/newtrun topology create demo-1 --description "demo lab"
```

Example `newtrun list 2node-vs-primitive`:

```
Suite: 2node-vs-primitive (21 scenarios)

  #   SCENARIO           STEPS  TOPOLOGY  REQUIRES
  1   boot-ssh           3      2node-vs  -
  2   setup-device       19     2node-vs  boot-ssh
  3   bridged            15     2node-vs  boot-ssh
  4   routed             11     2node-vs  boot-ssh
  5   bgp-underlay       14     2node-vs  setup-device
  ...
  21  verify-clean       52     2node-vs  teardown-infra
```

To read a scenario's YAML body:

```bash
bin/newtrun scenario get 2node-vs-primitive bridged
```

Returns the raw YAML to stdout — pipe to `less`, `pbcopy`, or a file as needed.

---

## 9. Authoring Scenarios

Scenario CRUD is exposed over HTTP — you can author, edit, and delete suites and scenarios without filesystem access to newtrun-server's host. The CLI exposes the same endpoints under `newtrun suite` (suite-level lifecycle) and `newtrun scenario` (per-scenario operations).

### 9.1 Create a suite directory

```bash
bin/newtrun suite create my-experiment --topology 1node-vs
```

Creates `<suites-base>/my-experiment/` with a minimal `suite.yaml` declaring `name: my-experiment` and `topology: 1node-vs`. The name must match `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$` — no path separators, no dots. `--topology` is required. Returns 409 if the suite already exists.

For parameterized scenarios (production-rollout shape) edit `suite.yaml` directly to add `targets:` and `parameters:` blocks — see [§10.6 Parameterized scenarios](#106-parameterized-scenarios).

### 9.2 Add a scenario

Two ways:

```bash
# From stdin
cat <<'EOF' | bin/newtrun scenario put my-experiment hello
name: hello
description: smoke test
steps:
  - name: wait-one
    action: wait
    duration: 1s
EOF

# From a file
bin/newtrun scenario put my-experiment hello --file hello.yaml
```

Scenarios must NOT declare `topology:` or `platform:` — those live on `suite.yaml`. The server validates with `ParseScenarioBytes`, then `LoadSuite` enforces cross-suite invariants at run time. The body's `name:` field must match the URL `{name}`. If validation fails, the file is **never touched** — the existing scenario (if any) is preserved.

The on-disk file is written atomically via tempfile + rename. Concurrent readers (`newtrun list`, another `newtrun scenario get`) never see a partial write.

### 9.3 Update a scenario

`scenario put` is idempotent. Sending the same body twice produces the same on-disk state. A second PUT to an existing scenario returns 200 (vs 201 on create) and writes to the existing file in-place — preserving any operator-authored `NN-` lexical prefix in the filename.

### 9.4 Read a scenario

```bash
bin/newtrun scenario get my-experiment hello
```

Returns the raw YAML body. Resolution rule: tries `hello.yaml` first, falls back to `*-hello.yaml` (the lexical-prefix convention).

### 9.5 Delete a scenario

```bash
bin/newtrun scenario delete my-experiment hello
```

Removes the file. Same lookup rule as `get`.

### 9.6 Delete an empty suite

```bash
bin/newtrun suite delete my-experiment
```

**Refuses (409) if any files remain.** Delete the scenarios first — explicit destructive action at the scenario level rather than masked behind a directory rmdir.

### 9.7 Common scenarios

**Iterate on a single scenario.** Edit YAML locally, push to the server, rerun:

```bash
vim my-experiment/hello.yaml
bin/newtrun scenario put my-experiment hello --file my-experiment/hello.yaml
bin/newtrun start my-experiment --scenario hello --server http://localhost:18080
```

**Browser frontend (newtcon) author flow.** Same endpoints, different client. The frontend's YAML editor calls PUT after every save; validation feedback comes from the server's 400 response body when `ParseScenarioBytes` rejects the input.

---

## 10. Writing Scenarios from Scratch

A scenario is a YAML file with `name`, `description`, and a list of `steps` placed inside a suite directory whose `suite.yaml` declares `topology` (and optionally `platform`):

```yaml
name: my-first-scenario
description: |
  Apply a transit service on Ethernet0 and verify it sticks.

steps:
  - name: apply-transit
    action: newtron-cli
    devices: [switch1]
    command: "service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002 --loopback"

  - name: verify-binding
    action: newtron-cli
    devices: [switch1]
    command: "configdb query NEWTRON_INTENT interface|Ethernet0 --loopback"
    expect:
      jq: '.operation == "apply-service"'

  - name: remove-transit
    action: newtron-cli
    devices: [switch1]
    command: "service remove Ethernet0 --loopback"

  - name: verify-binding-removed
    action: newtron-cli
    devices: [switch1]
    command: "configdb exists NEWTRON_INTENT interface|Ethernet0 --loopback"
    expect:
      jq: '.exists == false'
```

The intent record at `NEWTRON_INTENT/interface|<port>` is the authoritative service binding (see [newtron HLD §Device Is Source of Reality](../newtron/hld.md)). It replaced the per-binding `NEWTRON_SERVICE_BINDING` table — there is no separate binding table; the apply-service intent record IS the binding.

### 10.1 Scenario fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Unique scenario identifier; must match `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$` and the URL `{name}` segment of CRUD endpoints. |
| `description` | no | Human-readable description shown in `newtrun list` and on the SuiteStart event. |
| `requires` | no | Names of scenarios that must pass first. Hard dependency — a missing prerequisite means this scenario is SKIPPED. |
| `after` | no | Soft ordering — run after these, regardless of their status. Used for cleanup scenarios that always run last. |
| `requires_features` | no | Platform feature flags. Scenario is SKIPPED if the platform doesn't declare them (e.g., `evpn-vxlan` on a platform without overlay support). |
| `repeat` | no | Run the step list N times in sequence. Used for soak/stability tests. |
| `steps` | yes | Ordered list of [Step](#step-fields) records. |

`topology` and `platform` are **suite-level** — declared in `suite.yaml`, not in individual scenarios. `LoadSuite` rejects any scenario that sets them.

### 10.2 Step fields

| Field | Used by | Description |
|-------|---------|-------------|
| `name` | all actions | Step identifier for logs and reports. |
| `action` | all | Discriminator — see [§11 Step Action Reference](#11-step-action-reference). |
| `devices` | newtron, newtron-cli, host-exec | YAML accepts `all` or a list. |
| `command` | newtron-cli, host-exec | Subprocess command line. `{{device}}` is replaced per device. |
| `url` | newtron | HTTP path on newtron-server. `{{device}}` is replaced per device. |
| `method` | newtron | HTTP method; defaults to GET. |
| `params` | newtron, batch | Request body (a YAML/JSON map). |
| `duration` | wait | Sleep duration (e.g., `30s`, `2m`). |
| `expect` | newtron, newtron-cli, host-exec | Response assertions. See [§10.3](#103-expect-assertions). |
| `poll` | newtron | Polling — retry until expect passes or timeout expires. |
| `batch` | newtron | Multiple HTTP calls grouped per device. |
| `headers` | newtron | Per-step HTTP headers (e.g. `X-Newtron-Caller: alice` to forge a caller identity for auth testing). Applies uniformly across the step including batched sub-calls — one step = one identity. See [§11.5 Per-step headers](#per-step-headers-auth-identity). |
| `expect_failure` | newtron | Invert pass/fail — assert the call fails. |

### 10.3 expect assertions

| Field | Honored by | Meaning |
|-------|-----------|---------|
| `jq` | newtron, newtron-cli | jq expression must evaluate to `true` against the response body (newtron) or stdout parsed as JSON (newtron-cli with `--json`). |
| `contains` | newtron-cli, host-exec | Substring match on combined stdout+stderr (host-exec) or subprocess output (newtron-cli, when no `jq` is set). |
| `success_rate` | host-exec | For ping output: parse "N% packet loss" and assert success rate ≥ this value (0.0–1.0). |
| `timeout` / `poll_interval` | (internal) | Used by the polling path; set via the YAML `poll:` block, not via `expect:`. |

When a jq assertion fails, the error message includes the expression and the actual value — useful for debugging without rerunning.

### 10.4 Dependency graph

Use `requires` to express hard ordering (the scenario can't run until prerequisites pass) and `after` to express soft ordering (the scenario runs after, regardless of prerequisite status):

```yaml
# Cleanup scenario: always run last, even if earlier scenarios failed
name: verify-clean
after: [setup-device, vlan-vrf, service-lifecycle]
```

The Runner topologically sorts scenarios at suite load time. Cycles fail at load time with a clear error.

### 10.5 Iteration with `repeat`

A scenario with `repeat: N` runs its step list N times in sequence:

```yaml
name: stability-soak
repeat: 50
steps:
  - name: apply-service
    action: newtron-cli
    command: "service apply Ethernet0 transit --loopback"
  - name: verify
    action: newtron-cli
    command: "interface show Ethernet0 --loopback"
    expect:
      jq: '.service == "transit"'
  - name: remove
    action: newtron-cli
    command: "service remove Ethernet0 --loopback"
```

`StepResult.Iteration` distinguishes results from each iteration; the first FAIL stops the scenario and reports the iteration number.

### 10.6 Parameterized scenarios

Parameterized scenarios are the **production-rollout shape**: one scenario template, expanded across a target matrix declared at the suite level, with knobs the operator can tune per run. The two scenario shapes coexist in the same suite — most cleanup / provisioning / verification scenarios stay embedded-target; the rollout-flavored ones opt into parameterization by using template tokens.

**Step 1 — declare the catalog in `suite.yaml`:**

```yaml
name: 2node-vs-service
topology: 2node-vs-service

targets:
  devices: [switch1, switch2]
  interfaces: [Ethernet0, Ethernet4]

parameters:
  admin_status:
    type: enum
    values: [up, down]
    default: up
  mtu:
    type: int
    min: 1500
    max: 9216
    default: 9100
```

Parameter declarations support two YAML forms:

- **Shorthand** — `mtu: 9100` infers `{type: int, default: 9100}`. Use for plain defaults.
- **Verbose** — explicit `type:` plus constraints. Use for enums, bounded integers, required-with-no-default.

Recognized types: `string`, `int`, `bool`, `enum`, `ipv4`, `cidr`. Each type validates its override at request time and rejects with HTTP 400 on type mismatch or constraint violation.

**Step 2 — write the scenario template:**

```yaml
name: rollout-admin-status
description: Set admin_status on every IP-bearing interface and verify.
requires: [verify-health]
steps:
  - name: set-admin-status
    action: newtron
    method: POST
    url: /nodes/{{target.device}}/interfaces/{{target.interface}}/set-property
    params:
      property: admin_status
      value: "{{param.admin_status}}"

  - name: verify-admin-status
    action: newtron
    method: GET
    url: /nodes/{{target.device}}/interfaces/{{target.interface}}
    expect:
      # No quotes around {{param.admin_status}} — the template engine
      # emits "up" (JSON-quoted) when admin_status is a string.
      jq: .admin_status == {{param.admin_status}}

  - name: clear-admin-status
    action: newtron
    method: POST
    url: /nodes/{{target.device}}/interfaces/{{target.interface}}/clear-property
    params:
      property: admin_status
```

The scenario uses `{{target.X}}` and `{{param.X}}` templates instead of step-level `devices:` selectors. The runner iterates the cross-product of suite-level `targets:` (here, 2 devices × 2 interfaces = 4 iterations), running every step once per binding.

Two structural points the template demonstrates beyond the parameterized shape:

- **`requires: [verify-health]`** — `set-property` against an interface requires the device's parent intent to exist, which `provision` (and its dependent `verify-health`) is responsible for setting up. Without the prereq, topological sort can place the rollout before provision and every iteration fails with `writeIntent "interface|...": parent "device" does not exist`. To reach the scenario plus its declared prereqs from the CLI, use the `--target` flag (it walks the dependency chain): `bin/newtrun start 2node-vs-service --target rollout-admin-status`.
- **`clear-admin-status` reverse step** — per §15 (Operational Symmetry), every forward action gets a reverse in the same scope. `set-property` writes a child intent `interface|Ethernet0|admin_status`; if nothing clears it, the suite's later `deprovision` scenario can't delete the parent interface (`deleteIntent: has children`). The reverse keeps the rollout self-contained.

**Iteration semantics differ from `repeat`.** Parameterized iterations are **continue-on-failure** — one failing binding does not skip the remaining bindings, so a rollout sees every failing target in one run. `repeat` remains fail-fast within each binding.

**Step 3 — invoke with overrides:**

```bash
# CLI: run the scenario plus its declared prereqs (boot-ssh → provision →
# verify-health → rollout-admin-status) with defaults from suite.yaml.
bin/newtrun start 2node-vs-service --target rollout-admin-status

# HTTP: override admin_status to "down" and limit to one interface.
curl -X POST http://localhost:18080/newtrun/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "suite":      "2node-vs-service",
    "scenario":   "rollout-admin-status",
    "targets":    { "interfaces": ["Ethernet0"] },
    "parameters": { "admin_status": "down" }
  }'
```

Per-run overrides replace (not merge with) the suite default for each key — omit a key to inherit the default.

**Template substitution is context-aware** — the engine emits encoded forms per consumption surface so you don't need to pre-escape. The author rules below cover the common author mistakes; the full encoding table lives in [LLD §3.3](../newtrun/lld.md#33-context-aware-substitution).

| Where you write the token | Author rule |
|---|---|
| `url:` path component or query | Use bare — do not pre-escape |
| `command:` (host-exec) | Use bare — do not add your own quotes around a `{{param.X}}` |
| `command:` (newtron-cli) | Use bare; substituted values **must not contain whitespace** — newtron-cli execs argv via `strings.Fields(command)` with no shell, so a `{{param.X}}` whose value contains a space splits into multiple argv elements |
| `expect.jq:` | Use bare — do not put surrounding `"..."` around a string `{{param.X}}`; the engine emits JSON-quoted form |
| `params:` value | Use bare — same string handling whether the value is one token or interpolated |
| `expect.contains:` | Use bare — no escaping is applied |

**Target values pass an identifier whitelist** (`^[A-Za-z0-9_-]+$`) at both parse time and at request-override time. Targets address infrastructure (device, interface names) — they are always identifiers. Attempted shell-injection / path-traversal values fail validation before substitution.

**When to use which shape:**

| Shape | Use when |
|---|---|
| Embedded-target (`devices:` selector + `{{device}}`) | Testing — the suite covers a known matrix and each step might address a different subset. |
| Parameterized (`{{target.X}}` + suite-level `targets:`) | Production rollout — one scenario template applied to multiple fleet slices with per-run overrides. Reports per-binding pass/fail. |

**Example output (4-iteration rollout, verbose CLI):**

```
newtrun: 1 scenarios, topology: 2node-vs-service, platform: sonic-vs

  #     SCENARIO                  STEPS
  1     rollout-admin-status      2

  [1/1]  rollout-admin-status
          [device=switch1 interface=Ethernet0] set-admin-status      PASS  (<1s)
          [device=switch1 interface=Ethernet0] verify-admin-status   PASS  (<1s)
          [device=switch1 interface=Ethernet4] set-admin-status      PASS  (<1s)
          [device=switch1 interface=Ethernet4] verify-admin-status   PASS  (<1s)
          [device=switch2 interface=Ethernet0] set-admin-status      PASS  (<1s)
          [device=switch2 interface=Ethernet0] verify-admin-status   PASS  (<1s)
          [device=switch2 interface=Ethernet4] set-admin-status      PASS  (<1s)
          [device=switch2 interface=Ethernet4] verify-admin-status   PASS  (<1s)
          PASS  (3s)
```

Per-iteration step results carry their `TargetBinding` on the SSE wire (`target_binding` field on `step_end` events) and in JUnit / markdown reports — operators see exactly which (device, interface) combination produced each pass or fail.

**What can go wrong (server returns 400 on the run request):**

| Cause | Example message |
|---|---|
| Override names a dimension that isn't in `suite.yaml` | `unknown target dimension "racks" (not declared in suite.yaml)` |
| Override value fails the target-identifier whitelist (`^[A-Za-z0-9_-]+$`) | `target "devices" value "switch1; rm -rf /": must match [A-Za-z0-9_-]+ (identifiers only)` |
| Override value's type doesn't match the parameter's declared type | `parameter "mtu": expected int, got string` |
| Override value is outside the parameter's declared range | `parameter "vlan_id": value 5000 above max 4094` |
| Override value isn't one of the enum's declared values | `parameter "admin_status": value "shutdown" not in [up down]` |
| Override names a parameter that isn't in `suite.yaml` | `unknown parameter "made_up" (not declared in suite.yaml)` |

A scenario that uses `{{target.X}}` but no matching dimension is declared in `suite.yaml` fails at suite-load time with `references {{target.X}} but suite.yaml has no Xs: dimension declared` — that's a YAML-author error, not a request-time one.

---

## 11. Step Action Reference

newtrun has seven actions. `topology-reconcile` and `verify-topology` handle provisioning and its post-condition check. `host-exec` runs commands on host VMs via SSH. `newtron` is the generic action that covers every newtron-server operation via HTTP. `newtron-cli` runs the newtron CLI as a subprocess (for loopback testing). `wait` is a context-aware sleep. `run-suite` invokes another suite as a single step — the composition primitive.

### 11.1 topology-reconcile

Delivers the topology projection to the device by calling `Reconcile(name, "topology", "", ExecOpts{Execute: true})` on newtron-server. Reconcile handles config reload, locking, full CONFIG_DB replacement, and SaveConfig internally — the executor makes a single API call.

```yaml
- name: provision-switches
  action: topology-reconcile
  devices: [switch1, switch2]
```

No additional fields. Reports the number of entries applied on success. Inline runs require explicit opt-in (`allow_reconcile: true` in the POST body) because reconcile can replace an entire device's intent state.

### 11.2 verify-topology

Computes drift between the device's CONFIG_DB and the topology projection by calling `IntentDrift(name, "topology")` on newtron-server. Zero drift entries means the device matches the expected state; any drift causes the step to fail with a count of entries that diverge.

```yaml
- name: verify-config
  action: verify-topology
  devices: [switch1, switch2]
```

No additional fields. Useful as the post-condition check after a `topology-reconcile` step — confirms the projection actually landed without surprise mutations from out-of-band tools.

### 11.3 wait

Context-aware sleep. Respects cancellation — if the suite is paused or stopped during a wait, the executor exits cleanly without consuming the full duration.

```yaml
- name: wait-convergence
  action: wait
  duration: 30s
```

The only required field is `duration`. No `devices` field needed.

### 11.4 host-exec

Runs a command inside a host device's network namespace via direct SSH. The namespace name matches the device name (e.g., `host1`, `host2`); newtlab creates the namespaces at deploy time.

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
| `command` | yes | Shell command. Compound commands (semicolons, pipes) work — the executor wraps in `sh -c`. |
| `expect.success_rate` | no | Parse ping output for packet loss. `0.8` = 80% of pings must succeed. |
| `expect.contains` | no | String match on combined stdout+stderr. |

Without `expect`, the step passes when the subprocess exits 0; a non-zero exit fails the step with the captured output as the message.

### 11.5 newtron — generic HTTP action

Makes HTTP calls to newtron-server. Replaces all the former dedicated actions (create-vlan, apply-service, verify-bgp, etc.) with a single mechanism. Three modes: one-shot, polling, batch.

#### URL templates

URLs start from the path after the network segment. The `newtron` action calls newtron-server, so the `/newtron/v1/networks/<id>` prefix is added automatically. Use `{{device}}` for per-device expansion:

```yaml
url: /nodes/{{device}}/health             # → /newtron/v1/networks/<id>/node/switch1/health
url: /nodes/{{device}}/bgp/check          # → /newtron/v1/networks/<id>/node/switch1/bgp/check
url: /nodes/{{device}}/create-vlan        # → /newtron/v1/networks/<id>/node/switch1/create-vlan
```

newtron-server uses RPC-style verb-in-URL routes for mutating calls (`create-vlan`, `delete-vlan`, `apply-service`, `remove-service`, etc.) and resource-style GETs for reads (`/vlans`, `/vlans/{id}`, `/interfaces/{name}`). Check the handler list at `pkg/newtron/api/handler.go` when authoring new scenarios — there is no REST collection endpoint for VLAN, service, or VRF mutations.

If the URL contains `{{device}}`, the call runs in parallel across target devices. If not, it runs once with no device scoping (network-level operations like creating specs).

#### URL encoding

CONFIG_DB keys use `|` as the table-key separator. In URLs, encode special characters:

| Character | Encoding | Example |
|-----------|----------|---------|
| `\|` | `%7C` | `VLAN_MEMBER/Vlan100%7CEthernet4` |
| `/` in key | `%2F` | `LOOPBACK_INTERFACE/Loopback0%7C10.0.0.1%2F32` |

#### One-shot mode

Default. Makes a single HTTP call per device.

```yaml
# GET (method defaults to GET)
- name: check-health
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/health

# POST with params (request body)
- name: create-vlan
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/create-vlan
  params: {id: 100}

# Removal is POST to the remove-* verb, not DELETE on the resource
- name: remove-service
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet12/remove-service
```

#### Polling mode

When `poll` is set, the call repeats at the given interval until `expect.jq` returns `true` or the timeout expires. Use polling for daemon-convergence checks:

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /nodes/{{device}}/bgp/check
  poll:
    timeout: 120s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

During polling, HTTP errors are treated as "not ready yet" — the action keeps polling rather than failing immediately. This handles the common case where the device is still booting.

#### Batch mode

`batch` runs a sequence of HTTP calls as a single step. Fails on the first error. If any URL contains `{{device}}`, the sequence runs per-device in parallel:

```yaml
- name: setup-vlan-with-members
  action: newtron
  devices: [switch1]
  batch:
    - method: POST
      url: /nodes/{{device}}/create-vlan
      params: {id: 200}
    - method: POST
      url: /nodes/{{device}}/interfaces/Ethernet4/configure-interface
      params: {vlan_id: 200, tagged: false}
    - method: POST
      url: /nodes/{{device}}/interfaces/Ethernet12/configure-interface
      params: {vlan_id: 200, tagged: false}
```

Batch calls do not support individual `expect` blocks — the batch succeeds if all calls return non-error responses.

#### Per-step headers (auth identity)

`headers:` attaches arbitrary HTTP headers to every outbound request the step makes. The primary use is forging a specific caller identity for auth testing — the value of `X-Newtron-Caller` becomes the verified caller on newtron-server when the L1 self-attested-header identity surface is enabled (see auth-design.md L1 + L3):

```yaml
# Verify that mallory (not in the spec-team group) is denied the
# create-service mutation under --enforce-authorization.
- name: deny-unprivileged-create
  action: newtron
  method: POST
  url: /create-service
  params: {name: svc-test, type: routed}
  headers:
    X-Newtron-Caller: mallory
  expect_failure: true

# The same call as alice (in spec-team) succeeds.
- name: allow-privileged-create
  action: newtron
  method: POST
  url: /create-service
  params: {name: svc-test, type: routed}
  headers:
    X-Newtron-Caller: alice
```

Headers apply uniformly across every call in the step — top-level call, batched sub-calls, polling retries. One step = one header set. Empty/absent `headers:` preserves the runner's default behavior: the operator's Bearer (extracted by `pkg/newtrun/api/runs.go: operatorBearer` from the inbound `/runs` request's Authorization header) is attached automatically; per-scenario `as: <user>` overrides it for every call the scenario makes.

Per-scenario identity (`as: <user>`) is the canonical way to test authorization-by-identity in a scenario. Per-step `headers:` is reserved for non-identity overrides — Bearers minted at runtime in a multi-step flow (e.g. capture from `/auth/login`, attach on the next step), or custom headers the suite author wants to send. See [`auth-design.md` §L2c](../newtron/auth-design.md#l2c--server-issued-session-keys-pam-backed) for the inbound/outbound identity flow.

#### jq expressions

`expect.jq` is a jq expression evaluated against the HTTP response body (parsed as JSON). The expression must produce `true`; any other value is a failure.

Common patterns:

```yaml
# Boolean field check
jq: '.exists == true'

# String field check
jq: '.tagging_mode == "untagged"'

# Array length check
jq: 'length > 0'

# All-pass check
jq: 'all(.[]; .status == "pass")'

# Combined checks
jq: 'length > 0 and all(.[]; .status == "pass")'

# Nested field access
jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'

# String containment
jq: '.output | contains("ok")'
```

When a jq assertion fails, the error message includes both the expression and the actual value.

#### expect_failure

Inverts pass/fail. Use to assert that an operation correctly refuses:

```yaml
- name: create-vlan-blocked-by-zombie
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/create-vlan
  params: {id: 999}
  expect_failure: true
```

If the HTTP call fails (as expected), the step passes. If it succeeds unexpectedly, the step fails.

### 11.6 newtron-cli — CLI subprocess action

Runs the `newtron` CLI binary as a subprocess. The device name is prepended as the first positional argument (matching the normal `newtron <device> <command>` pattern). When `expect.jq` is set, `--json` is appended automatically so the output is machine-parseable.

Use this for testing CLI commands directly, particularly in `--loopback` mode where no device connection is needed. The CLI binary must be in `$PATH`.

```yaml
# Device-scoped command
- name: create-vlan
  action: newtron-cli
  devices: [switch1]
  command: "vlan create 100 --name Servers --loopback"

# Verify with jq assertion (--json auto-appended)
- name: verify-vlan
  action: newtron-cli
  devices: [switch1]
  command: "vlan show 100 --loopback"
  expect:
    jq: '.name == "Servers"'

# Network-scoped command (no device)
- name: list-services
  action: newtron-cli
  command: "service list --loopback"
```

Use `--no-deploy` with `newtrun start` when running loopback suites — no topology deployment needed:

```bash
bin/newtrun start 1node-vs-config --no-deploy --server http://localhost:18080
```

### 11.7 run-suite — invoke another suite as a step

Composes scenarios across suites. The called suite runs in the parent's process — same goroutine pattern, same connections (Client, NewtlabClient, HostConns), same ProgressReporter — but with `NoDeploy=true` (the parent already deployed the topology) and the parent's discovered topology/platform reused. The step succeeds iff every child scenario completed cleanly (worst-status wins: `error > failed > skipped > passed`).

```yaml
- name: apply-then-verify
  action: run-suite
  suite: verify-service-health        # which suite to call (resolves under server's --suites-base)
  parameters:                         # parameter overrides for the called suite
    service: '{{param.service}}'
  targets:                            # target-dimension overrides
    devices: ['{{target.device}}']
    interfaces: ['{{target.interface}}']
```

| Field | Required | Meaning |
|-------|----------|---------|
| `suite` | yes | Sibling suite directory name (no path separators). Resolves under the server's `--suites-base`. |
| `parameters` | no | Per-parameter overrides that fill the called suite's `{{param.X}}` templates. Same shape as `POST /runs`' `parameters` body field. |
| `targets` | no | Per-dimension target overrides that fill the called suite's `{{target.X}}` templates. Same shape as `POST /runs`' `targets` body field. |

The parser rejects `parameters:` or `targets:` on any non-`run-suite` action — silently dropping them would surface as "step ran but nothing happened" at runtime.

**Recursion limit.** Each call increments a context-carried depth counter; the executor refuses to go past `MaxRunSuiteDepth` (default 5). Deep enough for realistic composition (setup → service → verify → drift-check → reconcile) without permitting accidental towers. If you genuinely need deeper composition, flatten the suite graph.

**Inline runs.** `run-suite` is **not** in the default-allowed action list for `POST /runs/inline` — recursive composition has too broad a blast radius for operator-click scenarios. Use file-backed suites.

**Concurrent collision.** v0 does not register child suites with the run registry, so two parent runs each invoking the same child suite proceed independently. Top-level `POST /runs` collision detection still applies.

### 11.8 Common operations

The `newtron` action covers every operation exposed by newtron-server. The recipes below show real endpoint shapes — copy the URL form, not the YAML structure. Verify against `pkg/newtron/newtrun/v1/handler.go` before authoring against newer endpoints.

**SSH command on a device:**

```yaml
- name: check-frr
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/ssh-command
  params: {command: "vtysh -c 'show ip bgp summary'"}
  expect:
    jq: '.output | contains("Established")'
```

**CONFIG_DB verification:**

```yaml
# Existence probe (separate endpoint — does not require the key to exist)
- name: check-loopback
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/configdb/LOOPBACK_INTERFACE/Loopback0/exists
  expect:
    jq: '.exists == true'

# Field values
- name: check-bgp-globals
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/configdb/BGP_GLOBALS/default
  expect:
    jq: '.local_asn == "65001" and .router_id == "10.0.0.1"'

# Verify intent record removed
- name: check-binding-removed
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/configdb/NEWTRON_INTENT/interface%7CEthernet4/exists
  expect:
    jq: '.exists == false'
```

**VLAN operations:**

```yaml
# Create
- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/create-vlan
  params: {id: 100}

# Add an untagged member (members are an interface-side operation,
# not a VLAN sub-resource)
- name: add-member
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet4/configure-interface
  params: {vlan_id: 100, tagged: false}

# Delete
- name: delete-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/delete-vlan
  params: {id: 100}
```

**VRF operations:**

```yaml
- name: create-vrf
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/create-vrf
  params: {name: Vrf_local}

- name: delete-vrf
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/delete-vrf
  params: {name: Vrf_local}
```

**Service lifecycle:**

```yaml
- name: apply-service
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet12/apply-service
  params: {service: l2-extend}

- name: remove-service
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet12/remove-service
```

**BGP verification (poll while sessions converge):**

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /nodes/{{device}}/bgp/check
  poll:
    timeout: 120s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

**Health checks (tolerate warn during convergence):**

```yaml
- name: verify-health
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/health
  poll:
    timeout: 60s
    interval: 5s
  expect:
    jq: '.oper_checks | all(.[]; .status == "pass" or .status == "warn")'
```

**Drift detection:**

```yaml
- name: check-drift
  action: newtron
  devices: [switch1]
  url: /nodes/{{device}}/intent/drift
  expect:
    jq: '.status == "clean"'
```

**Topology reconcile (high-impact — gated by `allow_reconcile` in inline runs):**

```yaml
- name: reconcile-switch1
  action: topology-reconcile
  devices: [switch1]
```

**Response-capture (carry a value from one response into a later step):**

A `newtron` step's `capture:` map binds variable names to JQ expressions that run against the (envelope-unwrapped) response body. The captured values land in a scenario-iteration-scoped map on the runner; later steps reference them as `{{captured.NAME}}` in `url`, `params`, `headers`, or `expect.jq`. Same substitution rules as `{{target.X}}` / `{{param.X}}` — URL values are path/query-escaped, JSON params keep their typed value when the field is ENTIRELY one `{{captured.X}}` token.

```yaml
- name: login
  action: newtron
  method: POST
  url: /auth/login
  headers:
    Authorization: "Basic YWxpY2U6Y29ycmVjdC1wYXNzd29yZA=="
  capture:
    session_key: .key

- name: use-key
  action: newtron
  method: POST
  url: /create-zone
  params: {name: zone-bearer-auth}
  headers:
    Authorization: "Bearer {{captured.session_key}}"

- name: logout
  action: newtron
  method: POST
  url: /auth/logout
  headers:
    Authorization: "Bearer {{captured.session_key}}"
```

Rules and limits:

- The captured map is **iteration-scoped**: a fresh empty map at the start of every iteration of a parameterized scenario; cross-scenario carry is not supported. A scenario that needs write-then-read on the same value puts both steps in itself.
- Capture runs only on **successful single-call** newtron steps. The parser rejects `capture:` on `batch:` and `poll:` steps (no single response body to extract from) and on non-`newtron` actions.
- A `{{captured.NAME}}` reference whose key has not been written yet fails the step with an "undefined captured reference" error — surface ordering bugs at the call site rather than silently substituting an empty string.

---

## 12. Data Plane Tests

Data plane tests verify that packets actually traverse the fabric — not just that CONFIG_DB was written correctly. They require host endpoints that can generate and receive traffic.

### 12.1 Host devices

Host devices are first-class topology devices deployed by newtlab. Each host runs in its own network namespace on a shared QEMU VM (VM coalescing for efficiency). newtlab creates the namespaces at deploy time — test scenarios don't manage namespace lifecycle.

Multiple hosts may share a single QEMU VM. For example, the 2node-vs topology coalesces 6 hosts into one VM; 2node-vs-service coalesces 8.

### 12.2 host-exec for data plane

`host-exec` runs commands inside a host's network namespace via direct SSH. The namespace is automatically set to match the device name:

```yaml
- name: ping-across-fabric
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8
```

Compound commands work — the executor wraps in `sh -c` so pipelines run inside the namespace.

### 12.3 Worked example: L2 bridging test

Adapted from the 2node-vs-primitive suite (`newtrun/suites/2node-vs-primitive/10-bridged.yaml`) — the real scenario adds a third tagged member and polls ASIC_DB before the host setup; the example below trims those for readability and focuses on the L2-bridging path itself. The 2node-vs topology wires host1→`switch1:Ethernet4` and host3→`switch1:Ethernet12` (Force10-S6000 stride-4 port naming). The scenario creates VLAN 100, adds the two host-facing ports as untagged members, and verifies L2 connectivity between the hosts:

```yaml
# Create VLAN
- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/create-vlan
  params: {id: 100}

# Add members via the interface verb (no /vlans/{id}/member collection endpoint)
- name: configure-host1-port
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet4/configure-interface
  params: {vlan_id: 100, tagged: false}

- name: configure-host3-port
  action: newtron
  devices: [switch1]
  method: POST
  url: /nodes/{{device}}/interfaces/Ethernet12/configure-interface
  params: {vlan_id: 100, tagged: false}

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

### 12.4 Platform requirements

Data plane tests require a platform with a non-empty `dataplane` field in `platforms.json`. If `dataplane` is empty, host-related actions report SKIP instead of failing — the same suite can run against multiple platforms with varying dataplane support.

---

## 13. CI/CD Integration

### 13.1 Exit codes

| Code | Meaning |
|------|---------|
| 0 | All scenarios passed |
| 1 | One or more scenarios failed (test failure) |
| 2 | Infrastructure error — connection lost mid-run, run aborted (server SIGTERM), or pre-flight check failed (newtrun-server unreachable, lab not deployed, etc.) |

The split between 1 and 2 matters for CI: code 1 means "the tests caught something — investigate"; code 2 means "the infrastructure broke — retry might pass". A CI script that retries on transient failures should only retry exit code 2.

### 13.2 JUnit XML

Pass `--junit <path>` to write a JUnit XML report after the run finishes:

```bash
bin/newtrun start 2node-vs-primitive \
    --junit results.xml \
    --server http://localhost:18080
```

The XML has one `<testsuite>` per scenario, with `<testcase>` children for each step. Failed steps include `<failure>` elements with the assertion message.

### 13.3 Markdown report

Every `newtrun start` writes `newtrun/.generated/report.md` after the run finishes. The format is a single table:

```markdown
# newtrun Report — 2026-05-30 10:07:11

| Scenario | Topology | Platform | Result | Duration | Note |
|----------|----------|----------|--------|----------|------|
| boot-ssh | 2node-vs | sonic-vs | PASS | 1m31s |  |
| setup-device | 2node-vs | sonic-vs | PASS | 9s |  |
...
```

### 13.4 GitHub Actions example

The 2node-vs-primitive suite uses host-exec steps, so the runner host needs KVM/QEMU and the lab must be deployed before the suite starts. Self-hosted runners with `/dev/kvm` access are required — `ubuntu-latest` hosted runners cannot deploy.

```yaml
- name: Start newt-server + deploy lab
  run: |
    bin/newt-server --spec-dir newtrun/topologies/2node-vs/specs &
    sleep 2
    bin/newtlab deploy 2node-vs --monitor

- name: Run suite
  run: |
    bin/newtrun start 2node-vs-primitive --junit results.xml
  timeout-minutes: 30

- name: Upload JUnit
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtrun-junit
    path: results.xml

- name: Upload markdown report
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: newtrun-report
    path: newtrun/.generated/report.md
```

The `if: always()` ensures the reports upload even when the suite fails. The test failures show up in the JUnit XML; the markdown report is for human review.

---

## 14. Troubleshooting

### 14.1 `newtrun-server is not running`

The CLI's `requireServer` probe (`GET /newtrun/v1/health`) returned a connection error. Either the server isn't running, or you're pointing at the wrong URL.

```bash
# Is it running?
pgrep -f "bin/newtrun-server"

# Is the URL correct?
curl http://localhost:18080/newtrun/v1/health

# Override the URL
bin/newtrun start <suite> --newtrun-server http://other-host:18080
# or
NEWTRUN_SERVER=http://other-host:18080 bin/newtrun start <suite>
```

Start the server if it isn't running:

```bash
bin/newtrun-server &
```

### 14.2 `infrastructure error: run was aborted (server shut down)`

The Runner's SuiteEnd payload carried `status: aborted`. This happens when newtrun-server is SIGTERMed mid-run — the Runner's context is canceled, `iterateScenarios` exits at the next ctx-check, and the finalizer writes `status: aborted` to state.json. The CLI maps this to exit code 2 so CI scripts don't mistake it for a test failure.

To investigate: read `~/.newtron/newtrun/<suite>/state.json` — `scenarios[].status` shows which scenarios completed before the cancel.

### 14.3 `infrastructure error: newtrun-server connection lost mid-run`

The SSE stream from newtrun-server died before SuiteEnd arrived. The TCP connection closed without flushing the terminal event — caused by SIGKILL, network loss, or the server process exiting abnormally. The on-disk state may be incomplete; check what got persisted before the kill.

### 14.4 `topology node has unsaved intents`

newtron-server's running-state for a switch has uncommitted intent changes from a previous session. Reload to discard, or save to commit:

```bash
# Discard (old session left orphaned state)
bin/newtron switch1 intent reload --server http://localhost:18080   # implicitly --topology

# Commit (only if you want to keep the changes)
bin/newtron switch1 intent save --topology --server http://localhost:18080
```

Triggers: a previous `newtrun start` that was killed before its terminal save, an interactive `bin/newtron` session that exited without saving, or a crash during apply.

### 14.5 Scenario fails with `<kind> '<name>' already exists`

Real error forms include `service 'SVC_X' already exists`, `route policy 'RP_FOO' already exists`, `QoS policy 'Q_BAR' already exists`, `filter 'F_BAZ' already exists`, `prefix list 'PL_X' already exists` (`pkg/newtron/spec_ops.go`). Earlier runs created specs that newtron-server persisted to `network.json` and never cleaned up. Check `git status newtrun/topologies/<name>/specs/network.json` — if it shows changes you didn't make, revert and restart newtron-server:

```bash
git checkout HEAD -- newtrun/topologies/<name>/specs/network.json
pkill -KILL -f "bin/newtron-server" && sleep 1
bin/newtron-server --spec-dir newtrun/topologies/<name>/specs &
```

### 14.6 BGP verification times out

BGP sessions take time to establish after provisioning. Increase the poll timeout in the scenario:

```yaml
- name: verify-bgp
  action: newtron
  devices: all
  url: /nodes/{{device}}/bgp/check
  poll:
    timeout: 180s    # was 120s
    interval: 5s
  expect:
    jq: 'length > 0 and all(.[]; .status == "pass")'
```

If the timeout is hit repeatedly, the underlying BGP peering is misconfigured — check `bin/newtron switch1 bgp show` for what newtron-server sees and `vtysh -c 'show ip bgp summary'` on the device for what FRR sees.

### 14.7 `no SSH connection for host device "host1"`

The Runner skipped device connection setup because `--no-deploy` was set. host-exec steps need the SSH connections; remove `--no-deploy`:

```bash
bin/newtrun start <suite> --server http://localhost:18080
# instead of
bin/newtrun start <suite> --no-deploy --server http://localhost:18080
```

`--no-deploy` is for loopback suites and offline testing. Suites that touch hosts need the runner to connect.

### 14.8 Suite fails immediately with `409 Conflict`

Another run for the same suite is already active in the registry:

```bash
bin/newtrun status --suite <name>
```

If status shows `running` but no Runner goroutine is live in the server, `reconcileStaleStatus` (`pkg/newtrun/newtrun/v1/runs.go`) rewrites the response to `aborted` on read — but the on-disk state file still says `running`. Force-clear:

```bash
bin/newtrun stop <name>
# or
curl -X DELETE http://localhost:18080/newtrun/v1/runs/<name>
```

### 14.9 Scenario fails at deploy (newtlab couldn't bring up VMs)

The Runner's `deployTopology` call failed before any scenario could run — `SuiteEnd` carries `status: aborted` with a `DeployError`. Common causes:

```bash
# Is the platform image present?
ls ~/.newtlab/images/

# Are the QEMU ports free? (shipped topologies use port_base 10000/12000/13000)
ss -tlnp | grep -E ':1[023]00[0-9]'

# Any leftover VMs?
bin/newtlab status
bin/newtlab destroy <topology>     # tears down VMs, removes overlay disks, cleans state
```

Once the lab is reusable, retry. If the image is missing, see [§1 Prerequisites & Build](#1-prerequisites--build) for the SONiC image download.

### 14.10 Provisioning fails on a specific device

A `topology-reconcile` or `setup-device` step on one device returned an error but the rest of the run kept going. Open the run state — `scenarios[].steps[].details[]` lists per-device messages — to find which device failed.

```bash
# Is the device reachable through newtron-server?
curl http://localhost:18080/newtron/v1/networks/<id>/node/switch1/health

# SSH in for a closer look
bin/newtlab ssh switch1
sudo journalctl -u swss --since "10 minutes ago"
```

### 14.11 Health checks fail (oper_checks not converging)

Health checks verify interfaces, BGP, EVPN, LAG, and VXLAN. Use polling so the check retries during daemon convergence rather than failing on the first attempt (the `verify-health` recipe in [§11.8](#118-common-operations) shows the polling pattern). If polling still times out, SSH in and inspect:

```bash
bin/newtlab ssh switch1
show interfaces status
vtysh -c "show ip bgp summary"
vtysh -c "show evpn vni"
```

### 14.12 Data plane tests fail (host can't reach host)

Only platforms with a non-empty `dataplane` field in `platforms.json` support packet forwarding. Host-related actions auto-skip on platforms without it — so a failure here means a real connectivity problem.

```bash
# Confirm the platform claims dataplane
grep -A 2 '"sonic-' newtrun/topologies/<name>/specs/platforms.json | grep dataplane

# SSH to the host's namespace and probe
bin/newtlab ssh host1
ip addr show eth0
ip route
ping -c 1 <peer-ip>
```

### 14.13 Wrong interface names in CONFIG_DB verification

CONFIG_DB checks fail with `key does not exist` even though the apply step succeeded. Usually the topology spec uses interface names that don't match the device's HWSKU port stride:

| HWSKU | Stride | First four ports |
|-------|--------|------------------|
| Force10-S6000 (sonic-vs) | 4 | Ethernet0, Ethernet4, Ethernet8, Ethernet12 |
| Cisco P200-32x100 (CiscoVS) | 1 | Ethernet0, Ethernet1, Ethernet2, Ethernet3 |
| Cisco 8101-P4 (Gibraltar) | 1 | Ethernet0, Ethernet1, Ethernet2, Ethernet3 |

If the topology JSON ports don't match the HWSKU stride, the device renames or rejects them. Update `newtrun/topologies/<name>/specs/topology.json` to use the stride that matches the platform's HWSKU.

---

## 15. Command Reference

State-changing and read-only commands; all require newtrun-server. See [api.md](api.md) for endpoint-level reference.

### 15.1 Lifecycle

| Command | Purpose |
|---------|---------|
| `newtrun start <suite> [--scenario <name> \| --target <name>]` | Start (or resume) a run. With neither flag, runs all scenarios in dependency order. |
| `newtrun pause <suite>` | Request graceful pause at next scenario boundary. |
| `newtrun stop <suite>` | Cancel runner, destroy topology, clear state. |
| `newtrun status [--suite <pattern>] [--monitor] [--detail] [--json]` | Read run state. |

### 15.2 Suite management

| Command | Purpose |
|---------|---------|
| `newtrun list` | List suite directories. |
| `newtrun list <suite>` | List scenarios in a suite (dependency-ordered). |
| `newtrun suites` | Hidden alias of `list`. |
| `newtrun suite create <name>` | Create an empty suite directory. |
| `newtrun suite delete <name>` | Delete an empty suite directory (refuses if scenarios remain). |

### 15.3 Scenario management

| Command | Purpose |
|---------|---------|
| `newtrun scenario list <suite>` | Same data as `newtrun list <suite>`. |
| `newtrun scenario get <suite> <name>` | Print scenario YAML to stdout. |
| `newtrun scenario put <suite> <name> [--file <path>]` | Create or update from file or stdin. |
| `newtrun scenario delete <suite> <name>` | Delete a scenario file. |

### 15.4 Discovery

| Command | Purpose |
|---------|---------|
| `newtrun topologies` | List topology directories visible to the server. |
| `newtrun topology create <name> [--description <text>]` | Bootstrap a new topology directory (seeds topology.json, platforms.json, network.json, and an empty profiles/). Prints the spec_dir to pass to `POST /newtron/v1/networks`. |
| `newtrun actions` | List the six supported step actions (derived from `pkg/newtrun.StepAction`). `newtrun actions <name>` shows required fields, device semantics, and a YAML example. Mirrors [§11 Step Action Reference](#11-step-action-reference). |
| `newtrun version` | Print build version. |

### 15.5 Flags on `start`

| Flag | Meaning |
|------|---------|
| `--scenario <name>` | Run a single scenario. |
| `--target <name>` | Run the minimum dependency chain reaching this scenario. |
| `--dir <path>` | Suite directory path (alternative to positional name). |
| `--platform <name>` | Override platform from `suite.yaml`. |
| `--no-deploy` | Skip topology deployment + host SSH (loopback or pre-deployed lab). |
| `--monitor`, `-m` | Auto-refreshing dashboard instead of per-event log. |
| `--junit <path>` | JUnit XML report path. |
| `--server <url>` | newtron-server URL (env: `NEWTRON_SERVER`; default: `http://localhost:18080`). |
| `--network-id <id>` | newtron network identifier (env: `NEWTRON_NETWORK_ID`). Empty by default — the server derives the id from `suite.Topology` so two suites against one newt-server don't compete for the `default` slot (#116). |

`-v` / `--verbose` is a global flag (see §15.6) — it affects `start` (per-step output during a run) and `status` (more detail in the dashboard).

### 15.6 Global flags

| Flag | Meaning |
|------|---------|
| `--newtrun-server <url>` | newtrun-server URL (env: `NEWTRUN_SERVER`; default: `http://127.0.0.1:18080`). |
| `-v` / `--verbose` | Verbose output (where applicable). |

---

*Source-traced against `cmd/newtrun/` and the `bin/newtrun --help` output. CLI command names, flag names, and exit-code mappings are exact. YAML examples are runnable against the suites in `newtrun/suites/`. If you find a discrepancy, the code is the authority — please open an issue or PR.*
