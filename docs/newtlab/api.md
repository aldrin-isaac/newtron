# newtlab HTTP API Reference

`newtlab-server` exposes a thin HTTP wrapper around the same `pkg/newtlab/` Go API that powers `bin/newtlab`. It exists so consumers like the newtcon browser frontend can deploy and observe lab topologies without dropping to a shell. For the architecture this server fits into, see the [HLD](hld.md); for the canonical types it serializes (`LabState`, `NodeState`, `LinkState`), see [`pkg/newtlab/state.go`](../../pkg/newtlab/state.go).

## Table of Contents

1. [Conventions](#1-conventions)
2. [Typical Workflow](#2-typical-workflow)
3. [Server Management](#3-server-management)
4. [Topology Lifecycle](#4-topology-lifecycle)
5. [Node Control](#5-node-control)
6. [Events (SSE)](#6-events-sse)
7. [Types Reference](#7-types-reference)

---

## 1. Conventions

### Base URL

Default: `http://127.0.0.1:18082`. Override with `--listen <addr>` on the server. Non-loopback binds emit a startup warning — there is no built-in authentication.

### Envelope

Every JSON response is wrapped in:

```json
{ "data": <payload> }
```

or, on error:

```json
{ "error": "<message>" }
```

Mirrors the convention used by `newtron-server` and `newtrun-server`.

### Status codes

| Code | Meaning |
|------|---------|
| 200  | Synchronous operation completed successfully. |
| 202  | Asynchronous operation accepted; observe progress via `/events` or poll `/status`. |
| 400  | Malformed body or missing path parameter. |
| 404  | Topology or node not found. |
| 409  | Another deploy is already in flight for the same topology. |
| 500  | Internal error (newtlab returned an error from the wrapped Go API). |

### Path parameters

- `{name}` — topology name. Resolved under `--topologies-base/<name>/specs/` for `topology.json` lookup.
- `{node}` — device name as defined in `topology.json` (e.g., `switch1`, `host1`).

---

## 2. Typical Workflow

Source: [`docs/diagrams/newtlab-api-workflow.dot`](../diagrams/newtlab-api-workflow.dot). Re-render with `graph-easy --from=dot --boxart < docs/diagrams/newtlab-api-workflow.dot`.

```
┌───────────────────────────────┐
│                               │
│      1. List topologies       │
│     (GET /api/v1/topologies)     │
│                               │
└───────────────────────────────┘
  │
  │
  ▼
┌───────────────────────────────┐
│                               │
│     2. Deploy a topology      │
│ (POST /topologies/{n}/deploy) │
│                               │
└───────────────────────────────┘
  │
  │ 202
  ▼
┌───────────────────────────────┐
│                               │
│ 3. Subscribe to phase events  │
│ (GET /topologies/{n}/events)  │
│                               │
└───────────────────────────────┘
  │
  │
  ▼
┌───────────────────────────────┐
│                               │
│    4. Verify final status     │
│ (GET /topologies/{n}/status)  │
│                               │
└───────────────────────────────┘
```

---

## 3. Server Management

### `GET /api/v1/health`

Returns server status. No authentication, no side effects.

```json
{ "data": { "status": "ok", "version": "0.1.0-dev" } }
```

---

## 4. Topology Lifecycle

### Endpoint summary

| Method | Path | Status | Purpose |
|--------|------|--------|---------|
| `GET` | `/api/v1/topologies` | 200 | List labs known to newtlab |
| `GET` | `/api/v1/topologies/{name}/status` | 200 / 404 | Read `LabState` |
| `POST` | `/api/v1/topologies/{name}/deploy` | 202 / 404 / 409 | Start an async deploy |
| `POST` | `/api/v1/topologies/{name}/destroy` | 200 / 404 | Tear down VMs (synchronous) |
| `POST` | `/api/v1/topologies/{name}/provision` | 200 / 404 | Run the post-deploy provisioning pass |
| `GET` | `/api/v1/topologies/{name}/events` | 200 (SSE) | Subscribe to deploy phase events |

### `GET /api/v1/topologies` — list deployed labs

Returns one entry per lab with a state directory under `~/.newtlab/labs/`. Running and stopped labs are both included; call `/status` for per-node state.

**Response:**

```json
{
  "data": [
    { "name": "1node-vs" },
    { "name": "2node-vs-service" }
  ]
}
```

### `GET /api/v1/topologies/{name}/status` — read LabState

Returns the canonical [`LabState`](../../pkg/newtlab/state.go) for a deployed topology, including per-node PID / status / phase / SSH port / console port, link wiring, and bridge metadata.

**Response (excerpt):**

```json
{
  "data": {
    "name": "1node-vs",
    "created": "2026-05-26T22:43:32.345-07:00",
    "spec_dir": "/path/to/newtrun/topologies/1node-vs/specs",
    "ssh_key_path": "/home/.../1node-vs/lab.key",
    "nodes": {
      "switch1": {
        "pid": 1083981,
        "status": "running",
        "ssh_port": 13000,
        "console_port": 12000
      }
    },
    "links": null
  }
}
```

**Error:** 404 if the topology doesn't exist under the configured `--topologies-base`.

### `POST /api/v1/topologies/{name}/deploy` — async deploy

Starts an asynchronous deploy. Returns 202 immediately; subscribe to `/events` for phase updates, or poll `/status` for terminal state.

**Request (optional body or query parameters):**

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `provision` | bool | `false` | Run the post-deploy provisioning pass after VMs boot. |
| `force` | bool | `false` | Destroy any existing deployment of the same lab before starting. |
| `host` | string | `""` | Filter deployment to the named newtlab host (multi-host labs). |
| `parallel` | int | `1` | Parallelism for the provisioning pass (only honored when `provision=true`). |

All four are also accepted as query parameters (`?provision=true&force=true`) so the simplest invocation needs no body.

**Response (202):**

```json
{
  "data": {
    "topology": "1node-vs",
    "started": "2026-05-31T08:55:41-07:00"
  }
}
```

**Errors:**
- 404 — no `topology.json` under `<topologies-base>/<name>/specs/`.
- 409 — another deploy of `{name}` is already in flight. The error message includes the in-flight start time.

### `POST /api/v1/topologies/{name}/destroy`

Synchronously tears down the lab: stops every QEMU node, removes overlay disks, stops bridge workers, deletes the state directory. Returns when the operation completes.

**Response:** `{ "data": { "topology": "<name>", "status": "destroyed" } }`

### `POST /api/v1/topologies/{name}/provision`

Runs the post-deploy provisioning pass on an already-deployed lab. Synchronous.

**Query parameters:**
- `parallel` (int, default 1) — number of devices to provision concurrently.

---

## 5. Node Control

### `POST /api/v1/topologies/{name}/nodes/{node}/start`

Restarts a stopped node by re-spawning its QEMU process from `state.json`. Synchronous.

### `POST /api/v1/topologies/{name}/nodes/{node}/stop`

Sends SIGTERM to a running node's QEMU process. Synchronous.

**Response (both):**

```json
{ "data": { "topology": "<name>", "node": "<node>", "status": "started" } }
```

(with `"stopped"` for the stop endpoint)

---

## 6. Events (SSE)

### `GET /api/v1/topologies/{name}/events`

Subscribes to the phase event stream for `{name}`. Standard `text/event-stream` format. The stream stays open until the client disconnects or the server shuts down. A 30-second heartbeat comment line (`:` prefix) keeps proxies and load balancers from idling out the connection.

### Event types

| `event` field | When emitted | `data` payload |
|---|---|---|
| `phase` | `Lab.OnProgress(phase, detail)` fires during deploy or destroy | `PhasePayload` (see [§7](#7-types-reference)) |
| `complete` | deploy goroutine returns successfully | empty |
| `error` | deploy goroutine returns an error | `ErrorPayload` |

### Wire format example

```
: subscribed to 1node-vs

event: phase
data: {"phase":"boot","detail":"switch1"}

event: phase
data: {"phase":"patching","detail":"switch1"}

event: complete
data: null

```

Subscribers connecting mid-deploy see events from the moment of subscription forward — there is no replay. To recover earlier state, call `GET /status` and reconcile.

### Drop-on-full semantics

If a slow subscriber's buffer (64 events) fills up, additional events are dropped for that subscriber. Other subscribers still receive every event. The canonical state on disk (`state.json`) is always source of truth — clients that miss SSE events can recover by polling `/status`.

---

## 7. Types Reference

### `PhasePayload`

```json
{
  "phase": "boot",
  "detail": "switch1"
}
```

`phase` and `detail` mirror the arguments to `newtlab.Lab.OnProgress(phase, detail string)` directly. The set of phase strings is whatever `pkg/newtlab/` emits today (e.g., `boot`, `bootstrap`, `patching`, `bridges`, `ready`) — the server passes them through verbatim so SSE consumers don't go stale when newtlab adds a phase.

### `ErrorPayload`

```json
{ "message": "newtlab: deploy failed: ..." }
```

### `LabState`, `NodeState`, `LinkState`, `BridgeState`

Mirrors of the canonical types in [`pkg/newtlab/state.go`](../../pkg/newtlab/state.go). Returned by `GET /status`. Per DESIGN_PRINCIPLES_NEWTRON §46 (Wire Shape Mirrors Canonical Types), the wire shape is the in-memory shape — fields are not renamed or restructured at the HTTP boundary.
