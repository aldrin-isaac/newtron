# newt-server

`bin/newt-server` is the aggregated HTTP entry point for the
newtron-project. It runs every engine (newtron, newtrun, newtlab) in
one process on one port. Mount-time composition by Go function call;
no IPC, no registration protocol, no reverse proxy between engines.

For the engine designs, see [`docs/newtron/hld.md`](newtron/hld.md),
[`docs/newtrun/hld.md`](newtrun/hld.md),
[`docs/newtlab/hld.md`](newtlab/hld.md). For the routes each engine
serves, see the corresponding `api.md`.

## Routes

newt-server's mux dispatches by URL prefix:

| Prefix | Handler |
|---|---|
| `/newtron/v1/...` | newtron engine (full surface from [`docs/newtron/api.md`](newtron/api.md)) |
| `/newtrun/v1/...` | newtrun engine (full surface from [`docs/newtrun/api.md`](newtrun/api.md)) |
| `/newtlab/v1/...` | newtlab engine (full surface from [`docs/newtlab/api.md`](newtlab/api.md)) |
| `/newt-server/v1/health` | newt-server's own health probe |

The engines' `Handler()` methods return their full mux + middleware
chain; newt-server's outer mux only routes by prefix. No path
rewriting: the same paths a consumer sees in the URL bar reach the
engine handler unchanged.

## Configuration

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18080` | Bind address. Non-loopback values trigger a startup warning; newt-server has no built-in authentication. |
| `--spec-dir` | `""` | Forwarded to newtron — auto-register as the `default` network. |
| `--net-id` | `default` | Network ID for `--spec-dir`. |
| `--idle-timeout` | `5m` | newtron SSH connection idle timeout. |
| `--suites-base` | `newtrun/suites` | Forwarded to newtrun. |
| `--topologies-base` | `newtrun/topologies` | Shared by newtrun + newtlab. |

## When to use newt-server vs the standalone binaries

| Scenario | Run |
|---|---|
| Production / lab host / first-run path | `bin/newt-server` |
| Iterating on one engine's code | `bin/<engine>-server` (rebuild + restart just that one without disturbing the others' in-memory state) |

Same engine code in both cases — `pkg/<engine>/api/` is the source of
truth. The binaries are thin choosers: which engines does this process
expose.

## Composition shape

```go
// cmd/newt-server/main.go (paraphrased)
mux := http.NewServeMux()
mux.Handle("/newtron/v1/", newtronSrv.Handler())
mux.Handle("/newtrun/v1/", newtrunSrv.Handler())
mux.Handle("/newtlab/v1/", newtlabSrv.Handler())
mux.HandleFunc("/newt-server/v1/health", health)
srv := httputil.NewServer(mux, logger, ...)
srv.Start(":18080")
```

A request to `:18080/newtlab/v1/topologies` hits the mux, matches the
`/newtlab/v1/` prefix, and runs the newtlab handler — same goroutine
call stack as the HTTP request. No JSON marshaling between engines,
no localhost TCP, no IPC.

## Why this shape (and not a service mesh)

The first draft of this aggregation work was a separate process
called `newtser` that ran a registry + reverse proxy and required
backends to register over HTTP at startup. That design added ~700
lines of infrastructure (registry, proxy, heartbeat, eviction,
retry, deregister) for capabilities the project does not need at
this scale: cross-host backends, third-party plugins, independent
process upgrade, language-agnostic registration. It was discarded
in favor of the composition shape documented above.

`DESIGN_PRINCIPLES_NEWTRON.md` §40.1 codifies the rule: prefer
composition over service registration at single-process scale. Today
the project is single-machine; everything runs in one process group;
engines ship from one repo. Composition is enough.

If the project later deploys engines on separate hosts, the right
answer is a real service mesh (NATS, gRPC, Envoy) — not a
half-built HTTP registry that mimics one on loopback. This document
marks the choice explicitly so the next person reading the code
knows the decision was deliberate, not accidental.
