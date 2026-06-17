# newt-server

`bin/newt-server` is the aggregated HTTP entry point for the
newtron-project. It runs every engine (newtron, newtrun, newtlab) in
one process on one port via mux composition — each engine's
`pkg/<name>/api/` exports an `http.Handler`; newt-server mounts each
on a shared mux and wraps the result with the project's outer
identity middleware.

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
| `/newt-server/v1/auth/login` | Session-key mint — POST with HTTP Basic, returns an opaque session key (auth-design.md §L2c). Returns 404 when `--auth-pam-service` is not set. |
| `/newt-server/v1/auth/logout` | Session-key revoke — POST with `Authorization: Bearer <key>`. Idempotent. Returns 404 when `--auth-pam-service` is not set. |

The engines' `Handler()` methods return their full mux + middleware
chain; newt-server's outer mux routes by prefix only. Paths are not
rewritten: the URL the consumer sends reaches the engine handler
unchanged.

## Authentication boundary

newt-server is the only binary in this project that exposes the three
engine APIs over HTTP. Identity middleware (L2b PAM, L2c session-key
Bearer) lives at this boundary, not in any per-engine API package —
the engines themselves trust newt-server's outer middleware chain to
populate the request context.

When `--auth-pam-service` is set on newt-server, the composition
constructs:

- an in-memory L2c session-key store (`pkg/httputil/sessionkey.Store`),
- a `sessionkey.Middleware(store)` that recognizes
  `Authorization: Bearer <key>` and attaches the verified username
  to the request context,
- an `httputil.PAMMiddleware(authenticator)` that verifies HTTP
  Basic credentials against the named PAM service,
- the `POST /newt-server/v1/auth/login` + `/auth/logout` route
  pair.

The two middlewares wrap the entire composed mux uniformly so the
verified username (from L2c Bearer or L2b PAM Basic) reaches every
engine's request context. The newtron engine's `callerMiddleware`
(`pkg/newtron/api/caller_middleware.go`) reads the context value and
tags `audit.Caller` with the verification source for newtron's
mutation audit log. The newtrun and newtlab engines don't tag
audit callers today — their handlers operate on opaque test-run
state and lab-state objects rather than spec resources gated by
the L3 authorization layer; identity reaches them through the
same context but is not currently emitted to a per-engine audit
log.

The runner (newtrun engine) extracts the inbound Bearer from the
`/newtrun/v1/runs` request's Authorization header and attaches it on
every outbound newtron call (auth-design.md §L2c "Identity
forwarding through engines"). The operator's identity flows through
the in-process loopback unchanged. Per-scenario `as: <user>` in a
scenario overrides this default for every call that scenario makes.

## Configuration

### Composition

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen` | `127.0.0.1:18080` | Bind address. Non-loopback values trigger a startup warning. |
| `--idle-timeout` | `5m` | newtron SSH connection idle timeout. |
| `--networks-base` | `networks` | Root of the network tree. At boot, every `<base>/<name>/specs/topology.json` is auto-registered as a network with `id=<name>`. Also drives newtrun's suite discovery (`<base>/<name>/suites/<suite>/`) and newtlab's lab-spec resolution. One flag, three engines. |
| `--scaffold-root` | `""` | Forwarded to newtron. When set, `POST /newtron/v1/networks` accepts `scaffold:true` requests without `spec_dir` and lays them out under `<root>/<id>`. Empty disables this mode — UI clients fall back to passing `spec_dir` explicitly. |

### Identity (auth-design.md §L1-L2c)

| Flag | Default | Meaning |
|------|---------|---------|
| `--auth-pam-service` | `""` | PAM service name under `/etc/pam.d/` that authenticates Basic-auth requests on the TCP listener (§L2b). Engages the session-key store + `/auth/login`/`/auth/logout` routes (§L2c). Empty disables identity middleware entirely. |
| `--session-key-ttl` | `8h` | Absolute lifetime of session keys minted at `/auth/login`. Negative disables L2c even when PAM is on. |
| `--audit-caller-header` | `""` | HTTP header read by caller-extraction middleware as the self-attested L1 fallback. Typical: `X-Newtron-Caller`. Empty disables header-based identity. |
| `--unix-socket` | `""` | Path to a Unix-domain socket listener alongside the TCP one. Carries L1 peer-cred identity (kernel-verified UID → username). Empty disables. |

### Authorization (auth-design.md §L3)

| Flag | Default | Meaning |
|------|---------|---------|
| `--enforce-authorization` | `false` | Engages the `network.json` permissions map at runtime. When false, the inert `checkPermission` calls record identity but make no decisions. |
| `--spec-watch` | `false` | Watch every registered network's spec directory; on settled change (1s debounce) automatically reload the network so revoked grants take effect without an explicit `/reload`. |

### Audit + secrets (auth-design.md §L0, §L1, §L6)

| Flag | Default | Meaning |
|------|---------|---------|
| `--audit-log` | `""` | File path for the mutation audit log. Empty disables audit emission. |
| `--audit-log-integrity` | `false` | Hash-chain each audit entry so tampering is detectable via `bin/newtron audit verify`. Requires `--audit-log`. |
| `--secret-store` | `""` | File path for the operator-managed secret store. When set, `${secret:KEY}` references in spec values resolve at network load. Empty disables — references become hard errors at load. |

### TLS (auth-design.md §L2a)

| Flag | Default | What |
|---|---|---|
| `--tls-cert` | `""` | Server certificate (PEM). When set together with `--tls-key`, the TCP listener serves HTTPS. Empty disables — plain HTTP (operators can still terminate TLS at a reverse proxy in front of newt-server). |
| `--tls-key` | `""` | Server private key (PEM) matching `--tls-cert`. Required when `--tls-cert` is set. |
| `--tls-ca` | `""` | Client-CA PEM bundle. When set with `--tls-cert`/`--tls-key`, requires every client to present a certificate that verifies against the CA pool (mTLS); the peer cert CN becomes the caller identity with priority over PAM and the self-attested header. Empty leaves TLS one-way. |

See [`docs/newtron/mtls-howto.md`](newtron/mtls-howto.md) for the operator walkthrough.

## One server, three engines

`pkg/<engine>/api/` is the source of truth for each engine's HTTP
surface. `cmd/newt-server` is the only binary that mounts them on
a TCP listener — it instantiates each `api.Server`, composes their
muxes by path prefix, wraps the result with the L2a TLS / L2b PAM /
L2c session-key middleware chain, and serves the result on a single
port. Iterating on an engine means rebuilding `cmd/newt-server` and
restarting; the engine code itself has no main package.

The auth boundary lives only at `cmd/newt-server` because encryption
and identity are properties of the server boundary, not of any
individual engine. Engine packages can be embedded into other host
processes (in-process tests, future composed deployments) without
re-implementing the auth chain — they just run without one.

## Reasons for one process

- Three engines, one repo, one machine: the composition is small.
- One URL for every client (newtcon, operator scripts, external integrations) — no service-to-port map on the consumer side.
- One auth boundary — the operator's identity is verified once at the outer middleware and flows through every in-process inter-engine call unchanged.
- Scaling cost is deferred until scaling is a requirement.
