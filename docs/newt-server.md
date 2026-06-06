# newt-server

`bin/newt-server` is the aggregated HTTP entry point for the
newtron-project. It runs every engine (newtron, newtrun, newtlab) in
one process on one port via mux composition — each engine's
`pkg/<name>/api/` exports an `http.Handler`; newt-server mounts each
on a shared mux.

For engine designs see [`docs/newtron/hld.md`](newtron/hld.md),
[`docs/newtrun/hld.md`](newtrun/hld.md),
[`docs/newtlab/hld.md`](newtlab/hld.md). For the routes each engine
serves see the corresponding `api.md`.

## Routes

| Prefix | Handler |
|---|---|
| `/newtron/v1/...` | newtron engine ([`docs/newtron/api.md`](newtron/api.md)) |
| `/newtrun/v1/...` | newtrun engine ([`docs/newtrun/api.md`](newtrun/api.md)) |
| `/newtlab/v1/...` | newtlab engine ([`docs/newtlab/api.md`](newtlab/api.md)) |
| `/newt-server/v1/health` | newt-server's own health probe |

The engines' `Handler()` methods return their full mux + middleware
chain; newt-server's outer mux routes by prefix only. Paths are not
rewritten: the URL the consumer sends reaches the engine handler
unchanged.

## Configuration

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18080` | Bind address. Non-loopback values trigger a startup warning; no built-in authentication. |
| `--spec-dir` | `""` | Forwarded to newtron — auto-register as the `default` network. |
| `--net-id` | `default` | Network ID for `--spec-dir`. |
| `--idle-timeout` | `5m` | newtron SSH connection idle timeout. |
| `--suites-base` | `newtrun/suites` | Forwarded to newtrun. |
| `--topologies-base` | `newtrun/topologies` | Shared by newtrun and newtlab. |

## newt-server vs standalone binaries

| Scenario | Run |
|---|---|
| Production / lab host / first-run path | `bin/newt-server` |
| Iterating on one engine's code | `bin/<engine>-server` — rebuild and restart just that engine without disturbing the others' in-memory state |

`pkg/<engine>/api/` is the source of truth in both cases. The binaries
are thin choosers — which engines does this process expose.

## Inter-engine calls inside one process

newtlab depends on newtron's spec data (§27 — newtron owns spec files).
In the standalone `bin/newtlab-server`, that dependency is satisfied by
an HTTP client pointed at a separate `bin/newtron-server`. In `newt-
server` the two engines share a process, so the cheaper and safer wiring
is to read newtron's `*Network` directly.

That is exactly what `cmd/newt-server` does: it constructs an
`inprocSpecClient` (in `cmd/newt-server/inproc_spec_client.go`) that
satisfies `newtlab.SpecClient` by resolving the `*newtron.Network` from
the in-process `api.Server` registry at call time, then reading
`GetTopology` / `ListPlatforms` / `ShowProfile` directly. There is no
HTTP request, no actor channel hop, no `http.Client` timeout.

The split-process binaries continue to use the HTTP client — same
interface, different transport.

### Why not HTTP loopback?

newtron's `NetworkActor` serializes operations on a single goroutine. A
loopback HTTP call made from inside one of newtron's actor closures (for
example, `/host/{name}` resolving an SSH port through newtlab) had to
queue on the same actor channel that was running the outer closure. The
inner request timed out at the `http.Client`'s 30 s deadline every time —
see issue #97 for the trace. PR #96 bypassed the actor on the three
read-only spec endpoints (the immediate unblock); this in-process
accessor removes the cycle entirely in `newt-server` mode.

A read-only spec endpoint must stay outside `NetworkActor.do()` to keep
this property under split-process operation too. `TestNetworkActor_LoopbackHTTPDoesNotDeadlock`
in `pkg/newtron/api/actor_reentrancy_test.go` pins that contract.

## Reasons for one process

- Three engines, one repo, one machine: the composition is small.
- One URL for every client (newtcon, operator scripts, external integrations) — no service-to-port map on the consumer side.
- One place for TLS and SSO to terminate when they land.
- Scaling cost is deferred until scaling is a requirement.
