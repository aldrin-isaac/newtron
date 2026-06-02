# newtrun HTTP API Reference

The newtrun HTTP server (`newtrun-server`) is the canonical access point for every newtrun operation. The CLI (`bin/newtrun`) is a thin client over this surface; the browser frontend (newtcon) consumes the same endpoints. This document is the complete API reference: every endpoint, every request/response shape, every status code, every SSE event type.

**Audience:** Developers writing HTTP clients that drive newtrun — building tooling, integrating with CI/CD, or extending the browser frontend.

**Relationship to other docs:**

| Doc | Answers | When to read |
|-----|---------|--------------|
| [HLD](hld.md) | What and why | Understand the architecture, the run lifecycle, the SSE design |
| [LLD](lld.md) | How and what fields | Read or modify the server code |
| [HOWTO](howto.md) | When and in what order | Operate the system via the CLI |
| **This doc** | What endpoint, what params, what response | Write an HTTP client |

---

## Table of Contents

1. [Conventions](#1-conventions)
2. [Typical Workflow](#2-typical-workflow)
3. [Server Management](#3-server-management)
4. [Suite-Backed Run Lifecycle](#4-suite-backed-run-lifecycle)
5. [Run Events (SSE)](#5-run-events-sse)
6. [Inline Runs](#6-inline-runs)
7. [Suite Management](#7-suite-management)
8. [Scenario Authoring](#8-scenario-authoring)
9. [Topologies](#9-topologies)
10. [SSE Event Reference](#10-sse-event-reference)
11. [Types Reference](#11-types-reference)

---

## 1. Conventions

### Base URL

`http://127.0.0.1:18080` is the standard URL — `bin/newt-server` hosts the newtrun engine on that port alongside newtron and newtlab. For dev iteration, `bin/newtrun-server` listens on `127.0.0.1:19081` directly. Clients select between the two via `--newtrun-server <url>` or `NEWTRUN_SERVER`.

Non-loopback binds require an explicit `--listen` value on the chosen server and emit a startup warning that there is no built-in authentication. Wrap with a reverse proxy if you need TLS or auth.

### Envelope

All JSON responses follow a single envelope:

```json
{ "data": <payload>, "error": "<message>" }
```

`data` is populated on success; `error` is populated on failure. Exactly one is non-empty per response. Status code is the source of truth for success/failure; clients check the status before reading `data` or `error`.

### Status codes

| Code | Meaning |
|------|---------|
| 200  | Success with a JSON payload in `data` |
| 201  | Resource created (suite, scenario, run accepted) |
| 202  | Run accepted for asynchronous execution |
| 204  | Success with no body (delete operations) |
| 400  | Malformed request body, invalid name, or violated constraint (e.g. body name doesn't match URL) |
| 404  | Resource not found |
| 405  | Method not allowed for this path |
| 409  | Conflict — suite already exists, run already active, suite not empty |
| 500  | Server-side error (filesystem, internal) |

### Content types

- Request bodies: `application/json` for create/start endpoints; `application/yaml` (or any text) for scenario PUT, which is raw YAML.
- Response bodies: `application/json` for everything except `GET /newtrun/v1/suites/{suite}/scenarios/{name}` (raw YAML, `application/yaml`) and `GET /newtrun/v1/runs/{suite}/events` (Server-Sent Events, `text/event-stream`).

### Name constraints

Suite and scenario names match `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`. Path traversal characters (`/`, `.`, `..`) are rejected with 400 before the handler does any filesystem work.

---

## 2. Typical Workflow

Source: `docs/diagrams/newtrun-api-workflow.dot`. Re-render with `graph-easy --from=dot --boxart < docs/diagrams/newtrun-api-workflow.dot`.

```
┌────────────────────────────────┐
│                                │
│      1. Author scenarios       │
│           (PUT YAML)           │
│                                │
└────────────────────────────────┘
  │
  │
  ▼
┌────────────────────────────────┐
│                                │
│    2. List / inspect suites    │
│       (GET /newtrun/v1/suites)        │
│                                │
└────────────────────────────────┘
  │
  │
  ▼
┌────────────────────────────────┐
│                                │
│         3. Start a run         │
│     (POST /newtrun/v1/runs → 202)     │
│                                │
└────────────────────────────────┘
  │
  │
  ▼
┌────────────────────────────────┐
│                                │
│   4. Subscribe to SSE events   │
│ (GET /newtrun/v1/runs/{suite}/events) │
│                                │
└────────────────────────────────┘
  │
  │
  ▼
┌────────────────────────────────┐
│                                │
│    5. Read final state.json    │
│    (GET /newtrun/v1/runs/{suite})     │
│                                │
└────────────────────────────────┘
```

The HLD ([§8 Execution Model](hld.md)) explains why each step exists. For browser-side clients, an alternative is to skip SSE and poll `GET /newtrun/v1/runs/{suite}` — coarser but simpler.

---

## 3. Server Management

### `GET /newtrun/v1/health`

Returns server status. No authentication, no side effects, safe to call from probes and load balancers.

**Request:** no body.
**Response:** 200 with `data` being a `HealthResponse`.

```json
{ "data": { "status": "ok", "version": "0.1.0-dev" } }
```

A non-200 from `/newtrun/v1/health` means newtrun-server is not the process answering on that port (or is mid-shutdown). The CLI's `requireServer` probe uses this endpoint to decide whether to print "newtrun-server is not running".

---

## 4. Suite-Backed Run Lifecycle

A "suite-backed run" is keyed by a suite name (the directory under `suites_base`). The server tracks at most one active run per suite via the in-memory `RunRegistry`. Inline runs ([§6](#6-inline-runs)) use a separate UUID-keyed namespace and do not conflict.

### Endpoint summary

| Method | Path | Status | Purpose |
|--------|------|--------|---------|
| `POST` | `/newtrun/v1/runs` | 202 / 404 / 409 | Start or resume a run |
| `GET` | `/newtrun/v1/runs` | 200 | List runs visible on disk |
| `GET` | `/newtrun/v1/runs/{suite}` | 200 / 404 | Read one run's full state |
| `DELETE` | `/newtrun/v1/runs/{suite}` | 200 / 409 | Remove a terminal run's state |
| `POST` | `/newtrun/v1/runs/{suite}/pause` | 200 / 404 | Request graceful pause |
| `POST` | `/newtrun/v1/runs/{suite}/stop` | 200 / 404 | Cancel the runner's context |
| `GET` | `/newtrun/v1/runs/{suite}/events` | 200 (SSE) | Subscribe to progress events ([§5](#5-run-events-sse)) |

### `POST /newtrun/v1/runs` — start or resume a run

Constructs a `Runner` in a goroutine, returns 202 immediately. If a previous run for the same suite reached `paused` state, the server populates `opts.Resume = true` and `opts.Completed[name] = PASS` for each previously-passed scenario; the Runner then skips them and resumes from the next unprocessed one.

If another run holds the suite's registry slot, the response is 409 Conflict with the active run's start time in the error message.

**Request:** `application/json`, body is a `StartRunRequest`:

| Field | Type | Required | Meaning |
|-------|------|----------|---------|
| `suite` | string | one of suite/suite_dir | Suite name under server's `suites_base`. Mutually exclusive with `suite_dir`. The named directory must contain a `suite.yaml` declaring `name` + `topology`. |
| `suite_dir` | string | one of suite/suite_dir | Absolute path to a suite directory. Mutually exclusive with `suite`. |
| `scenario` | string | no | Run only the named scenario. Mutually exclusive with `target` and `all`. |
| `target` | string | no | Run the minimal dependency chain reaching this scenario. |
| `all` | bool | no | Run all scenarios. Defaults to true when none of `scenario` / `target` / `all` are set. |
| `platform` | string | no | Override the suite's platform declaration. |
| `no_deploy` | bool | no | Skip topology deployment + host SSH connection setup. Use only for loopback or fully external-lab runs. |
| `verbose` | bool | no | Reserved for verbosity hints. |
| `newtron_server` | string | no | newtron-server URL the Runner should target. Overrides the server's default. |
| `network_id` | string | no | Network identifier passed to newtron operations. |
| `junit_path` | string | no | If set, the CLI writes a JUnit XML report there after the run finishes. The server-side runner does not use this field directly — it's a CLI-only hint. |
| `targets` | object | no | Per-dimension overrides of the suite's `targets:` block — `map[string][]string`. Keys must match dimensions declared in `suite.yaml`; values must satisfy the target-value whitelist (`^[A-Za-z0-9_-]+$`). Omitted keys inherit the suite default. Used by parameterized scenarios. |
| `parameters` | object | no | Per-name overrides of the suite's `parameters:` block — `map[string]any`. Keys must match parameters declared in `suite.yaml`; values are validated against each parameter's `ParameterSpec` (type and constraints). Omitted keys inherit the declared default. Used by parameterized scenarios. |

**Parameterized run example.** A suite that declares targets/parameters in `suite.yaml` can be reshaped per request via `targets` and `parameters`:

```json
{
  "suite":      "2node-vs-service",
  "scenario":   "rollout-admin-status",
  "targets":    { "interfaces": ["Ethernet0"] },
  "parameters": { "admin_status": "down" }
}
```

The server resolves overrides against the suite catalog (LoadSuite → EffectiveTargets / EffectiveParameters); failures return 400 with a descriptive error (unknown dimension, identifier-whitelist violation, type mismatch, enum out-of-range, etc.).

**Response:** 202 with a `StartRunResponse`:

```json
{
  "data": {
    "suite": "1node-vs-config",
    "started": "2026-05-29T19:59:14.698-07:00"
  }
}
```

**Error responses:**

- 400 — missing both `suite` and `suite_dir`; both set; malformed body.
- 404 — `suite_dir` doesn't exist on the server.
- 409 — registry already has an entry for this suite.

### `GET /newtrun/v1/runs` — list runs

Returns one `RunInfo` per suite that has a `state.json` on disk under `~/.newtron/newtrun/`. Stale `running` / `pausing` states get reconciled to `aborted` on the fly when the registry has no live entry (the [HLD §9.3 server-restart honesty](hld.md) rule).

**Response:** 200, `data` is an array of `RunInfo`:

```json
{
  "data": [
    {
      "suite": "2node-vs-primitive",
      "topology": "2node-vs",
      "status": "complete",
      "started":  "2026-05-29T19:47:52-07:00",
      "updated":  "2026-05-29T19:55:03-07:00",
      "finished": "2026-05-29T19:55:03-07:00"
    },
    {
      "suite": "1node-vs-basic",
      "topology": "1node-vs",
      "status": "aborted",
      "started":  "2026-05-28T15:57:23-07:00",
      "updated":  "2026-05-28T16:02:11-07:00"
    }
  ]
}
```

`finished` is omitted when the run was aborted mid-flight (no clean terminal write). Empty array when nothing has been run.

### `GET /newtrun/v1/runs/{suite}` — read one run

Returns the full `RunState` ([§11](#11-types-reference)) including every scenario and step. Same reconcile-stale-status logic as the list endpoint.

**Response:** 200 with `data` being a `RunState`; 404 if no state file matches `{suite}` in either the suite or `_inline` namespace.

### `DELETE /newtrun/v1/runs/{suite}` — remove state

Removes the suite's state directory from disk. Refuses (409) if the run is still active in the registry; call `/stop` first.

**Response:** 200 with `{"data": {"status": "deleted"}}` or 409 on active run.

### `POST /newtrun/v1/runs/{suite}/pause` — graceful pause

Writes `state.Status = pausing` to disk. The Runner picks up the pause signal at the next scenario boundary via `CheckPausing` and exits with `PauseError`. The eventual `state.Status` becomes `paused`. A subsequent `POST /newtrun/v1/runs` for the same suite resumes from the next unprocessed scenario.

Returns 200 immediately; the actual pause lands asynchronously.

**Response:** 200 with `{"data": {"status": "pausing"}}` or 404 if no active run.

### `POST /newtrun/v1/runs/{suite}/stop` — cancel runner

Cancels the run's context. The Runner's `iterateScenarios` exits via the ctx-check at the next iteration and emits `SuiteEnd` with `status = aborted` ([HLD §9.3](hld.md), implemented in PR #35). The `state.json` `status` becomes `aborted`.

**Response:** 200 with `{"data": {"status": "stopping"}}` or 404 if no active run.

---

## 5. Run Events (SSE)

### `GET /newtrun/v1/runs/{suite}/events`

Opens a Server-Sent Events stream of progress events for the named run. The connection stays open until the client disconnects, the run terminates, or the server shuts down.

**Headers sent by server:**

- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `Connection: keep-alive`

**Initial frame** is a comment line confirming subscription:

```
: subscribed to 1node-vs-config
```

**Heartbeat** every 30 seconds prevents intermediaries from timing out the connection during quiet periods:

```
: heartbeat
```

**Event frames** follow the standard SSE format. `event:` is the [event type](#10-sse-event-reference); `data:` is a JSON payload:

```
event: scenario_end
data: {"name":"setup-device","status":"PASS","duration":"1s","steps":[...],"index":0,"total":17}
```

**Drop-on-full semantics:** the broker's per-subscriber channel is buffered (64 events). If a slow consumer fills the buffer, additional events for that subscriber are silently dropped — SSE is best-effort delivery. Each event still reaches subscribers that are keeping up.

**Late-subscribe race:** if a run completes before the client subscribes, no events arrive at all. The client should fall back to `GET /newtrun/v1/runs/{suite}` to read the terminal state. The CLI's `start` command tracks whether `SuiteEnd` was ever seen and treats "no SuiteEnd" as an infrastructure error (exit 2 with the "connection lost" message).

---

## 6. Inline Runs

### `POST /newtrun/v1/runs/inline`

Submits a single scenario as inline YAML — no suite directory, no state-persistence in the suite namespace. The server allocates a fresh UUID, runs the scenario in a goroutine, and persists state under `~/.newtron/newtrun/_inline/<uuid>/`. Used by the browser frontend's compose-and-run flow and by automation that doesn't want to write to the suites tree.

**Safety policy:** inline runs go through `InlineSafetyPolicy`. The defaults block `topology-reconcile` (override via the `allow_reconcile: true` body field) and impose a 60-second wall-time budget (override via `timeout_seconds: N`). The `AllowedURLPrefixes` field exists for restricting `newtron` action URLs, but is **not populated by default** — operators who want URL restriction must build a wrapper that sets `cfg.InlineURLPrefix` before constructing the Server. As shipped, inline scenarios may call any newtron-server URL the server is configured to reach.

**Request:** `application/json`, body is an `InlineRunRequest`:

| Field | Type | Required | Meaning |
|-------|------|----------|---------|
| `scenario_yaml` | string | yes | The full scenario YAML body (same shape as a file under a suite directory). |
| `newtron_server` | string | no | Override the server's default newtron-server URL for this run only. |
| `timeout_seconds` | int | no | Override the safety policy's wall-time budget for this run only. `0` keeps the policy default (60s). |
| `allow_reconcile` | bool | no | Opt in to permitting the `topology-reconcile` action for this scenario. Default `false` is the high-impact gate. |

**Response:** 202 with an `InlineRunResponse`:

```json
{
  "data": {
    "run_id": "8a7b6c5d-1234-4abc-9def-0123456789ab",
    "started": "2026-05-29T20:15:00-07:00"
  }
}
```

The `run_id` becomes the `{suite}` path parameter for subsequent calls to `/newtrun/v1/runs/{suite}`, `/events`, etc. The inline and suite namespaces share the same handler routes — the server resolves both via `LoadAnyRunState`.

**Error responses:**

- 400 — missing `scenario_yaml`, invalid YAML, or violates safety policy.

---

## 7. Suite Management

| Method | Path | Status | Purpose |
|--------|------|--------|---------|
| `GET` | `/newtrun/v1/suites` | 200 | List suite directories |
| `POST` | `/newtrun/v1/suites` | 201 / 400 / 409 | Create a suite directory with its `suite.yaml` manifest |
| `DELETE` | `/newtrun/v1/suites/{suite}` | 204 / 404 / 409 | Delete a suite (must have no scenarios) |
| `GET` | `/newtrun/v1/suites/{suite}/scenarios` | 200 / 404 | List scenarios in a suite |

### `GET /newtrun/v1/suites`

Returns the names of immediate subdirectories under `suites_base`. Missing base directory returns an empty array, not 404.

**Response:** 200 with `data` being a `SuitesResponse`:

```json
{ "data": { "suites": ["1node-vs-config", "1node-vs-basic", "2node-vs-primitive"] } }
```

### `POST /newtrun/v1/suites`

Creates the suite directory and writes a minimal `suite.yaml` manifest containing the supplied `name` + `topology`. After creation the operator can `PUT` scenarios into the suite or edit `suite.yaml` directly to add `targets:` / `parameters:` for parameterized scenarios.

**Request:** `application/json`, body is a `CreateSuiteRequest`:

```json
{
  "name":     "my-new-suite",
  "topology": "2node-vs-service"
}
```

| Field | Type | Required | Meaning |
|-------|------|----------|---------|
| `name` | string | yes | Suite identifier; matches `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`. |
| `topology` | string | yes | Topology this suite targets; written into `suite.yaml`. |

**Response:** 201 with `{"data": {"name": "my-new-suite"}}`. 400 on invalid name or missing topology, 409 if the directory already exists.

### `DELETE /newtrun/v1/suites/{suite}`

Removes the suite directory and its `suite.yaml`. **Refuses (409) if any scenario files remain** — `suite.yaml` is the suite-create handshake and doesn't count against the "still has scenarios" check, but any other `*.yaml` file blocks deletion. newtcon's UX is expected to delete scenarios individually first so the destructive action is explicit at the scenario level.

**Response:** 204 on success, 404 if the suite doesn't exist, 409 if it has scenarios.

### `GET /newtrun/v1/suites/{suite}/scenarios`

Returns the scenarios in a suite as summaries — `ScenarioSummary` ([§11](#11-types-reference)). Used by `newtrun list <suite>` and by the browser frontend's suite picker. Scenarios are topologically sorted by `requires`/`after` if any are present.

**Response:** 200 with a `SuiteScenariosResponse`:

```json
{
  "data": {
    "suite": "1node-vs-basic",
    "topology": "1node-vs",
    "scenarios": [
      {
        "name": "boot-ssh",
        "topology": "1node-vs",
        "platform": "sonic-vs",
        "step_count": 1
      },
      {
        "name": "setup-device",
        "topology": "1node-vs",
        "platform": "sonic-vs",
        "step_count": 2,
        "requires": ["boot-ssh"]
      }
    ]
  }
}
```

404 if the suite directory doesn't exist.

---

## 8. Scenario Authoring

| Method | Path | Content-Type | Status |
|--------|------|-------------|--------|
| `GET` | `/newtrun/v1/suites/{suite}/scenarios/{name}` | `application/yaml` (response) | 200 / 404 |
| `PUT` | `/newtrun/v1/suites/{suite}/scenarios/{name}` | `text/*` (request) | 200 / 201 / 400 |
| `DELETE` | `/newtrun/v1/suites/{suite}/scenarios/{name}` | — | 204 / 404 |

The PUT path is the one with real behavior; the other two are uniform.

### `GET /newtrun/v1/suites/{suite}/scenarios/{name}` — read raw YAML

Resolves the on-disk file by either exact `{name}.yaml` or `*-{name}.yaml` (the lexical-prefix convention). Returns the raw bytes — no envelope.

**Response:** 200 with `Content-Type: application/yaml` and the YAML body; 404 if no file matches.

### `PUT /newtrun/v1/suites/{suite}/scenarios/{name}` — create or update

Body is raw YAML. The server validates with `ParseScenarioBytes` (the same parser the rest of the framework uses) AND asserts the body's `name:` field matches the URL `{name}`. If either fails, the file is **never touched**. On success, the file is written atomically (same-directory tempfile + rename(2)) so concurrent readers never observe a partial write.

**File naming:** a fresh scenario lands at `{suite_dir}/{name}.yaml`. An update to a scenario authored on disk with a lexical prefix (e.g. `06-perwrite-actuated.yaml`) is written in-place to that file, preserving the prefix.

**Response:** 201 on create, 200 on update, with `{"data": {"suite": "...", "name": "...", "path": "..."}}`. 400 on invalid YAML or name mismatch, 404 if the suite directory doesn't exist.

### `DELETE /newtrun/v1/suites/{suite}/scenarios/{name}`

Removes the file. Same lookup rule as GET.

**Response:** 204 on success, 404 if no file matches.

---

## 9. Topologies

### `GET /newtrun/v1/topologies`

Returns the topology names discoverable under `topologies_base`. Missing base directory returns an empty array. Read-only; topology authoring is out of scope (issue #33 explicitly excluded it).

**Response:** 200 with `data` being a `TopologiesResponse`:

```json
{ "data": { "topologies": ["1node-vs", "2node-vs", "2node-vs-service"] } }
```

---

## 10. SSE Event Reference

The stream from `/newtrun/v1/runs/{suite}/events` carries seven event types in this order:

```
suite_start  →  (scenario_start  →  (step_start  →  step_end)*  →  scenario_end)*  →  suite_end
```

`step_progress` events may interleave between `step_start` and `step_end` when external producers (currently: none in this repo; the per-device-op SSE consumer is upstream-deferred) call `StepProgress`.

### `suite_start`

Sent once at the start of the run with the scenario roster.

```json
{
  "scenarios": [
    {
      "name": "setup-device",
      "topology": "1node-vs",
      "platform": "sonic-vs",
      "step_count": 6
    }
  ]
}
```

### `scenario_start`

Sent at the start of each scenario.

```json
{ "name": "setup-device", "index": 0, "total": 17 }
```

### `step_start`

Sent at the start of each step.

```json
{
  "scenario": "setup-device",
  "name": "verify-device-metadata",
  "action": "newtron-cli",
  "index": 1,
  "total": 6
}
```

### `step_progress`

Sent between `step_start` and `step_end` per device operation. Currently no producers in this repo; the field reservation is for the upstream-deferred per-device-op SSE consumer.

```json
{
  "scenario": "setup-device",
  "step": "create-vlan-with-changes",
  "action": "newtron-cli",
  "index": 0,
  "op": {
    "seq": 0,
    "kind": "redis_write",
    "table": "VLAN",
    "key": "Vlan100",
    "fields": { "vlanid": "100" },
    "result": "applied",
    "at": "2026-05-30T10:00:00-07:00"
  }
}
```

### `step_end`

Sent at the end of each step with the result.

```json
{
  "scenario": "setup-device",
  "index": 1,
  "total": 6,
  "result": {
    "name": "verify-device-metadata",
    "action": "newtron-cli",
    "status": "PASS",
    "duration": "<1s"
  }
}
```

### `scenario_end`

Sent at the end of each scenario with the full per-step result list.

```json
{
  "name": "setup-device",
  "topology": "1node-vs",
  "platform": "sonic-vs",
  "status": "PASS",
  "duration": "1s",
  "steps": [ /* StepResultPayload, one per step */ ],
  "index": 0,
  "total": 17
}
```

### `suite_end`

Sent exactly once at the end of the run. The `status` field distinguishes terminal modes; see [HLD §9.3 (server-restart honesty)](hld.md#93-server-restart-honesty).

```json
{
  "results": [ /* ScenarioEndPayload, one per scenario */ ],
  "status": "complete",
  "duration": "1s"
}
```

`status` values: `complete`, `failed`, `aborted`, `paused`.

---

## 11. Types Reference

### `RunState`

The complete record for one run. Returned by `GET /newtrun/v1/runs/{suite}` ([§4](#4-suite-backed-run-lifecycle)).

```json
{
  "suite": "1node-vs-config",
  "suite_dir": "newtrun/suites/1node-vs-config",
  "topology": "1node-vs",
  "platform": "sonic-vs",
  "status": "complete",
  "started": "2026-05-29T19:59:14-07:00",
  "updated": "2026-05-29T20:05:21-07:00",
  "finished": "2026-05-29T20:05:21-07:00",
  "scenarios": [ /* ScenarioState, one per scenario */ ]
}
```

`target` (string) is omitted when the run executed all scenarios; it carries the `--target <scenario>` filter when one was supplied. `pid` is no longer populated by the server (suite-backed runs are owned by goroutines, not OS processes) — older state files may still carry it but new ones omit it.

`status` values: `running`, `pausing`, `paused`, `complete`, `aborted`, `failed`. The reconcile rule in [`§4 GET /newtrun/v1/runs/{suite}`](#get-apirunssuite--read-one-run) may rewrite `running` / `pausing` to `aborted` on the wire when the registry has no live entry.

### `RunInfo`

The list-view summary of a run. Returned by `GET /newtrun/v1/runs` ([§4](#4-suite-backed-run-lifecycle)).

```json
{
  "suite": "...",
  "topology": "...",
  "status": "complete",
  "started": "...",
  "updated": "...",
  "finished": "..."
}
```

### `ScenarioSummary`

The per-scenario summary used in `suite_start` events and `GET /newtrun/v1/suites/{suite}/scenarios`.

Fields: `name`, `description`, `topology`, `platform`, `step_count`, `requires`.

### `HealthResponse`, `SuitesResponse`, `TopologiesResponse`

Single-field wrappers around the relevant array or scalar. See examples in [§3](#3-server-management), [§7](#7-suite-management), [§9](#9-topologies).

### Event payloads

`SuiteStartPayload`, `ScenarioStartPayload`, `StepStartPayload`, `StepProgressPayload`, `StepEndPayload`, `ScenarioEndPayload`, `SuiteEndPayload` — see the inline examples in [§10](#10-sse-event-reference).

---

*This document was source-traced against `pkg/newtrun/newtrun/v1/server.go` (route table), `runs.go`, `suites.go`, `scenarios.go`, `topologies.go`, and `types.go`. Every endpoint claim was verified by reading the handler. If you find a discrepancy, the code is the authority — please open an issue or PR.*
