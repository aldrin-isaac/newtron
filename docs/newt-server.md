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
| `--topologies-base` | `newtrun/topologies` | Used by newtlab for lab-spec resolution (the on-disk path it walks to find a `topology.json` for a deploy). |
| `--scaffold-root` | `""` | Forwarded to newtron. When set, `POST /newtron/v1/networks` accepts `scaffold:true` requests without `spec_dir` and lays them out under `<root>/<id>`. Empty disables this mode — UI clients fall back to passing `spec_dir` explicitly. |

## newt-server vs standalone binaries

| Scenario | Run |
|---|---|
| Production / lab host / first-run path | `bin/newt-server` |
| Iterating on one engine's code | `bin/<engine>-server` — rebuild and restart just that engine without disturbing the others' in-memory state |

`pkg/<engine>/api/` is the source of truth in both cases. The binaries
are thin choosers — which engines does this process expose.

## Reasons for one process

- Three engines, one repo, one machine: the composition is small.
- One URL for every client (newtcon, operator scripts, external integrations) — no service-to-port map on the consumer side.
- One place for TLS and SSO to terminate when they land.
- Scaling cost is deferred until scaling is a requirement.
