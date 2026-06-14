# Inter-Service mTLS — Operational HOWTO

## Status: partial — see [auth-design.md §L2a](auth-design.md#l2a--inter-service-mtls) for design rationale.

The L2a inter-service mTLS feature has two halves:

1. **Identity-extraction infrastructure (shipped).** `pkg/httputil` extracts the verified peer cert CN from a TLS handshake and exposes it via `ServiceCertCNFromRequest` / `ServiceCertCNFromContext`. The newtron caller middleware (`pkg/newtron/api/caller_middleware.go`) reads it ahead of PAM and Unix peer creds. The audit verification source `service_cert_cn` records the cert CN as the caller identity.

2. **Listener-side wiring (not yet implemented).** No binary in this project currently accepts `--tls-cert` / `--tls-key` / `--tls-ca` flags. After the engine-composition refactor (PRs A–D), encryption is a property of the server boundary, so the right home for these flags is `cmd/newt-server`. They haven't been added yet.

## What this means for operators today

| Deployment | Recommendation |
|---|---|
| Production / external-traffic-facing newt-server | Terminate TLS at a reverse proxy (nginx, caddy, envoy) in front of `bin/newt-server`. The proxy holds the cert + key; `newt-server` listens on loopback or a Unix socket behind it. |
| Composed `bin/newt-server` only — single host, single port | mTLS between engines is a no-op (engines share one process; their cross-calls are Go function calls through `http.ServeHTTP`). External TLS termination at a proxy is the right hammer. |
| Three standalone binaries on one host | Loopback dev mode — no TLS by design (the binaries are dev tools). |
| Three standalone binaries on separate hosts | Not a supported deployment shape post-PR-B. The standalone binaries are loopback-default. Use `bin/newt-server` for production. |

## Audit log shape when L2a-listener-wiring lands

When the listener flags are added to `cmd/newt-server`, the audit log will gain entries with:

```json
{
  "verification_source": "service_cert_cn",
  "user": "<cert CN>",
  ...
}
```

Reviewers will tell mTLS-authenticated callers apart from PAM-authenticated ones by the `verification_source` field — both paths populate `audit.Caller` the same way.

## What the cert-CN priority does today

Even without listener-side TLS, the priority slot ahead of PAM is exercised by `TestCallerMiddleware_ServiceCertCNYieldsVerifiedCaller` and `TestCallerMiddleware_ServiceCertCNWinsOverEverything` in `pkg/newtron/api/caller_middleware_test.go` (both use the `httputil.WithServiceCertCNForTest` helper) so that when the listener side lands, the integration is mechanical — drop the `*tls.Config` into `httputil.NewServer(...)` and the rest works.

## Cross-references

- [`auth-design.md` §L2a](auth-design.md#l2a--inter-service-mtls) — design rationale + threat model.
- [`pkg/httputil/conn_creds.go`](../../pkg/httputil/conn_creds.go) — the `ServiceCertCN` type and context plumbing.
- [`pkg/newtron/api/caller_middleware.go`](../../pkg/newtron/api/caller_middleware.go) — the caller-priority chain that places service cert CN ahead of PAM.
- [`pam-howto.md`](pam-howto.md) — L2b user-to-service PAM (today's user-identity path).
