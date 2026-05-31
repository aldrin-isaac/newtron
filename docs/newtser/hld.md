# newtser — High-Level Design

For the architectural principles behind the newtron-project, see
[Design Principles](../DESIGN_PRINCIPLES_NEWTRON.md).

---

## 1. Purpose and Boundaries

newtser is the front-door HTTP server for the newtron-project service
set. It runs a small reverse proxy plus an in-memory service
registry: backend servers register themselves on startup; newtser
dispatches incoming requests to the right backend by the first path
segment of the URL.

The registry is data-driven. newtser knows nothing about the backends
it routes to beyond their name and upstream URL — adding a new app
means writing the app, not changing newtser.

newtser exists to solve three problems that surfaced as the project
grew from one backend to three:

- **One front port.** External consumers (newtcon, operator scripts,
  reverse proxies above newtser) target one address instead of
  remembering which port belongs to which service.
- **Self-identifying URLs.** With routes like `/newtron/v1/network/...`
  the path itself carries the service name. Operators reading logs
  or scripts don't have to cross-reference port numbers.
- **Pluggable architecture.** Any future newtron-project app — or a
  community/third-party server following the same protocol — registers
  with newtser and becomes available at `/<its-name>/v<n>/...` with no
  newtser code change.

---

## 2. Architecture

```
                ┌─────────────────────────────────────────────┐
                │              newtser :18080                 │
                │                                             │
                │   GET  /newtser/v1/health                   │
                │   GET  /newtser/v1/services                 │
                │   POST /newtser/v1/services                 │
                │   POST /newtser/v1/services/{name}/heartbeat│
                │   DELETE /newtser/v1/services/{name}        │
                │   *  → reverse-proxy by first path segment  │
                │                                             │
                │   in-memory Registry (map by service name)  │
                │   eviction loop (TTL 90s, 30s tick)         │
                └─────────────────────────────────────────────┘
                  ▲ register/heartbeat │  proxy →
                  │                    ▼
       ┌──────────┴──┐    ┌──────────┴──┐    ┌──────────┴──┐
       │   newtron-  │    │   newtrun-  │    │   newtlab-  │
       │   server    │    │   server    │    │   server    │
       │   :19080    │    │   :19081    │    │   :19082    │
       └─────────────┘    └─────────────┘    └─────────────┘
                  (loopback-only; not reachable except via newtser)
```

### 2.1 Reverse-proxy dispatch

newtser owns one route table:

| First path segment | Handler |
|---|---|
| `newtser` | Meta-routes (`/newtser/v1/health`, `/newtser/v1/services`, ...) |
| Any other | Reverse-proxy to the registered backend |

The proxy is `net/http/httputil.ReverseProxy` — streaming-friendly so
SSE passes through unchanged.

The full incoming path is forwarded to the backend. A request to
`newtser:18080/newtlab/v1/topologies` becomes
`localhost:19082/newtlab/v1/topologies` on the wire to newtlab-server.
Backends serve their own service-prefixed routes; newtser does not
rewrite paths.

### 2.2 Registry semantics

The Registry is a `map[string]*Service` guarded by an `RWMutex`. Each
entry holds:

- **Name** — first path segment dispatch key (e.g., `"newtron"`)
- **Version** — informational; the URL itself already carries the
  version segment (`/newtron/v1/...`)
- **Upstream** — the URL newtser proxies to (e.g., `http://127.0.0.1:19080`)
- **LastSeen** — updated by `POST /newtser/v1/services` (re-register)
  and by `POST /newtser/v1/services/{name}/heartbeat`

Re-registration is idempotent — same name, same upstream, refreshed
`LastSeen`. This is how backend restarts work: the new process
registers with the same name and effectively replaces the prior entry.

An eviction goroutine runs every 30s and removes entries whose
`LastSeen` is older than 90s (three heartbeat intervals). A backend
that crashes without deregistering is cleaned up within 90s.

### 2.3 Backend registration lifecycle

Backends carry a `--newtser <url>` flag (empty by default = standalone,
no registration). When set, the backend constructs a
`newtser.Registration` at startup; the `Registration` runs a goroutine
that:

1. POSTs to `/newtser/v1/services` to register, retrying with
   exponential backoff (1s, 2s, 4s, 8s, capped at 30s) until success
   or shutdown. Newtser-not-up-yet is not an error — the keepalive
   retries until it comes online.
2. Heartbeats every 30s via `POST /newtser/v1/services/{name}/heartbeat`.
3. If a heartbeat returns 404 (the registration was evicted), the
   keepalive re-registers from step 1.
4. On graceful shutdown, sends best-effort
   `DELETE /newtser/v1/services/{name}` so the registry shows the
   service is gone immediately rather than waiting for eviction.

### 2.4 Failure modes

| Scenario | Operator-visible outcome |
|---|---|
| newtser down | Consumers get connection refused at `:18080`. Backends keep retrying registration on backoff. When newtser comes up, all backends re-register within ~30s. |
| Backend down (graceful) | `DELETE /services/{name}` runs; registry shows the service gone immediately; consumers get 503 until the backend comes back. |
| Backend crashes (no deregister) | Eviction loop removes the entry within 90s; until then, consumers see 502 Bad Gateway (newtser proxies to a dead upstream). |
| Two backends register the same name | Second registration overwrites the first; previous backend continues to serve loopback traffic but no longer receives newtser-routed requests. |
| Registration data invalid | newtser returns 400 with the validation error; the backend logs and retries with backoff. |

---

## 3. Position in the Three-Tool Model

newtser is the fourth program in the project, but it has a deliberately
narrower scope than the three engines:

- **newtron**, **newtrun**, **newtlab** are *engines* — each owns a
  domain (devices / test scenarios / lab VMs) and has internal
  state, persistent files, lifecycle logic.
- **newtser** is *infrastructure* — no domain knowledge, no
  persistent state. Its job is to make the three engines look like
  one HTTP surface.

A consumer interacting with newtser experiences the project as a single
HTTP API:

```
GET /newtron/v1/network                  ← newtron-server
GET /newtrun/v1/runs                     ← newtrun-server
POST /newtlab/v1/topologies/2node-vs/deploy ← newtlab-server
GET /newtser/v1/services                 ← newtser itself
```

The service prefix is the only thing the consumer needs to know.

---

## 4. Configuration

### 4.1 newtser flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18080` | Bind address. Non-loopback values trigger a startup warning that newtser has no built-in authentication. |
| `--eviction-interval` | `30s` | How often to scan for stale registrations. |
| `--eviction-max-age` | `90s` | Registrations older than this are evicted. |

### 4.2 Backend flags (per server)

| Flag | Default | Meaning |
|------|---------|---------|
| `--newtser` | `""` | URL of newtser. Empty = standalone, no registration. Set to `http://127.0.0.1:18080` for the standard local-dev stack. |
| `--listen` / `--addr` | loopback `:1908x` | The backend's own listen address. With `--newtser` set, this is the upstream URL the backend registers (and is reachable only via newtser). Without `--newtser`, this is the direct external port. |

### 4.3 Recommended port assignments

| Component | Port |
|---|---|
| newtser (the only externally-visible port) | `18080` |
| newtron-server (loopback) | `19080` |
| newtrun-server (loopback) | `19081` |
| newtlab-server (loopback) | `19082` |

---

## 5. Authentication

newtser v0 has no built-in authentication, matching the convention of
the other servers. Operators that need TLS or auth wrap newtser with a
reverse proxy. The single-port surface makes this simpler than having
to terminate TLS at three different services.

---

## 6. Not in Scope

- **Load balancing across replicas.** newtser holds one upstream per
  service name. The HA / scale-out story is a follow-up.
- **mTLS to backends.** newtser proxies plain HTTP to loopback
  backends. Cross-host deployments where the link between newtser and
  the backend leaves the box should add mTLS in a follow-up.
- **Service mesh features** (circuit breakers, retries, distributed
  tracing). newtser is a registry + proxy, not a control plane.
