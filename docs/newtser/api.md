# newtser HTTP API Reference

newtser is the front-door HTTP server for the newtron-project. This
file documents the routes newtser serves itself. Every other path on
newtser's port is reverse-proxied to a registered backend; see the
backend's own API doc (e.g., [`docs/newtron/api.md`](../newtron/api.md))
for those.

For the design, see [HLD](hld.md).

## Table of Contents

1. [Conventions](#1-conventions)
2. [Server Management](#2-server-management)
3. [Service Registry](#3-service-registry)
4. [Reverse Proxy](#4-reverse-proxy)

---

## 1. Conventions

### Base URL

Default: `http://127.0.0.1:18080`. Override with `--listen <addr>`.

### Envelope

JSON responses use the standard newtron-project envelope:

```json
{ "data": <payload> }     // success
{ "error": "<message>" }  // failure
```

### Status codes

| Code | Meaning |
|------|---------|
| 200 | Read or refresh succeeded |
| 201 | Registration created |
| 204 | Deregistration succeeded (no body) |
| 400 | Malformed request body or invalid registration data |
| 404 | Service not found (heartbeat / deregister) — caller may need to re-register |
| 503 | Reverse-proxy target service not registered |
| 502 | Reverse-proxy target unreachable (backend down between heartbeats) |

---

## 2. Server Management

### `GET /newtser/v1/health`

Returns newtser's status.

**Response:**

```json
{ "data": { "status": "ok", "version": "0.1.0-dev" } }
```

Probes / load balancers can use this without affecting the registry.

---

## 3. Service Registry

### `GET /newtser/v1/services`

Lists every registered service.

**Response:**

```json
{
  "data": [
    {
      "name": "newtron",
      "version": "v1",
      "upstream": "http://127.0.0.1:19080",
      "last_seen": "2026-05-31T11:05:37-07:00"
    },
    {
      "name": "newtlab",
      "version": "v1",
      "upstream": "http://127.0.0.1:19082",
      "last_seen": "2026-05-31T11:05:39-07:00"
    }
  ]
}
```

Sorted by `name`.

### `POST /newtser/v1/services`

Register or re-register a service. Idempotent — calling again with the
same `name` overwrites the prior entry.

**Request body:**

```json
{
  "name": "newtron",
  "version": "v1",
  "upstream": "http://127.0.0.1:19080"
}
```

| Field | Required | Validation |
|-------|----------|-----------|
| `name` | yes | `[a-z0-9-]+`. The string is the first path segment for dispatch. Reserved names: `newtser`. |
| `version` | no | Informational only — the URL path itself carries the version (`/newtron/v1/...`). |
| `upstream` | yes | Must start with `http://` or `https://`. |

**Response (201 Created):**

```json
{
  "data": {
    "name": "newtron",
    "version": "v1",
    "upstream": "http://127.0.0.1:19080",
    "last_seen": "2026-05-31T11:05:37-07:00"
  }
}
```

**Errors:** 400 for invalid name or malformed body.

### `POST /newtser/v1/services/{name}/heartbeat`

Refresh `last_seen` without changing other fields. Backends call this
every 30s to keep their registration alive (eviction TTL is 90s).

**Response (200):** the refreshed `Service` record (same shape as `GET`).

**Errors:**
- 404 — service was not registered (or was evicted). The backend
  treats this as a signal to re-register via `POST /newtser/v1/services`.

### `DELETE /newtser/v1/services/{name}`

Deregister a service. Backends call this on graceful shutdown.

**Response (204):** empty body.

**Errors:** 404 if the service wasn't registered. The caller may treat
404 as success — the desired state ("not registered") is achieved.

---

## 4. Reverse Proxy

Any request whose first path segment is **not** `newtser` is
dispatched to the registered backend.

### Dispatch rule

```
GET /<service>/<rest...>
  ↓
Look up "<service>" in the registry.
  ↓
If found: reverse-proxy the entire request (path, method, headers,
body) to <upstream>/<service>/<rest...>. Path is not rewritten.
  ↓
If not found: return 503 with envelope explaining that the service
isn't registered (operator should check `GET /newtser/v1/services`).
```

### SSE

`net/http/httputil.ReverseProxy` preserves chunked transfer encoding
and flushes responses incrementally, so Server-Sent Event streams
pass through newtser unchanged. Subscribers connect to
`newtser:18080/newtlab/v1/topologies/{name}/events` and receive
events with the same framing newtlab-server emits.

### Connection pooling

newtser caches one `ReverseProxy` per upstream URL, so the HTTP
connection pool between newtser and each backend is preserved across
requests. Short-lived HTTP calls between newtcon and newtron-server
go through one persistent TCP connection.

### Path rewriting

None. The backend serves the same `/<service>/<version>/...` path
the client sent. This is deliberate: the backend's logs show the
same paths an operator sees in newtcon's network tab, and the
"forwarded path == requested path" invariant means there is no
mental indirection when debugging.
