# Listener-Side TLS (Operator-to-Server) — Operational HOWTO

L2a listener-side TLS on `cmd/newt-server` is engaged by three flags:

- `--tls-cert=PATH` — server certificate (PEM).
- `--tls-key=PATH` — server private key (PEM).
- `--tls-ca=PATH` — client-CA PEM bundle. Optional; when set, requires mTLS.

When `--tls-cert` is empty, the TCP listener stays on plain HTTP — the pre-L2a behavior. Set `--tls-cert` + `--tls-key` together to enable HTTPS; add `--tls-ca` to also require client certificates.

See [`auth-design.md` §L2a](auth-design.md#l2a--listener-side-tls-operator-to-server) for the threat-model rationale.

## Three deployment shapes

| Shape | Configuration |
|---|---|
| Plain HTTP (loopback default) | No `--tls-*` flags. The TCP listener accepts plain HTTP. Identity comes from PAM (`--auth-pam-service`), the Unix socket (`--unix-socket`), or the self-attested header (`--audit-caller-header`). |
| TLS-only (server-auth) | `--tls-cert` + `--tls-key`. The TCP listener serves HTTPS; clients verify the server cert against their trust store. Identity continues to come from PAM / Unix peer creds / the header — the cert authenticates the **server**, not the client. |
| mTLS (server-auth + client-auth) | `--tls-cert` + `--tls-key` + `--tls-ca`. Every client must present a certificate that verifies against the configured CA pool. The verified peer cert's Subject Common Name becomes the caller identity, taking priority over PAM, the Unix socket, and the self-attested header — see the caller priority chain at `pkg/newtron/api/caller_middleware.go`. |

Operators who prefer reverse-proxy termination (nginx/caddy/envoy in front of `newt-server`) continue to do so — `--tls-cert` flags are an alternative, not a replacement. The proxy holds the cert + key; `newt-server` listens on loopback behind it; the proxy injects `--audit-caller-header` to surface the identity it authenticated.

## Identity flow under mTLS

```
TLS handshake → verified peer cert CN
              → r.TLS.VerifiedChains[0][0].Subject.CommonName
              → httputil.ServiceCertCNFromRequest
              → caller-middleware (priority slot ahead of PAM)
              → audit.Caller{Username: <CN>, Source: "service_cert_cn"}
              → permissions checker (L3 reads Caller from audit context)
```

Audit log entries from mTLS-authenticated callers carry:

```json
{
  "verification_source": "service_cert_cn",
  "user": "<cert CN>",
  ...
}
```

Reviewers tell mTLS-authenticated callers apart from PAM-authenticated ones by the `verification_source` field — both paths populate `audit.Caller` the same way, and L3 authorization treats them uniformly through the entitlement table.

## Bringing it up

1. **Generate a server cert + private key** signed by a CA the operator controls. Common pattern: an internal PKI's intermediate CA issues a cert for the newt-server host's DNS name; the same intermediate signs operator client certs.

2. **(Optional) Generate operator client certs** — one cert per identity that needs to reach newt-server over mTLS. The cert's Common Name becomes the operator's username in audit + authorization.

3. **Start `cmd/newt-server`:**

   ```sh
   bin/newt-server \
     --listen 0.0.0.0:18443 \
     --tls-cert /etc/newt-server/server.crt \
     --tls-key /etc/newt-server/server.key \
     --tls-ca /etc/newt-server/operators-ca.crt \
     --audit-log /var/log/newt-server-audit.jsonl \
     --enforce-authorization \
     --spec-dir /etc/newt-server/specs
   ```

4. **Dial with a client cert:**

   ```sh
   curl --cacert /etc/newt-server/server-trust.crt \
        --cert /etc/operator-alice.crt \
        --key /etc/operator-alice.key \
        https://newt-server.example.com:18443/newtron/v1/networks
   ```

5. **Verify the audit log:**

   ```sh
   grep '"verification_source":"service_cert_cn"' /var/log/newt-server-audit.jsonl
   ```

   Every authenticated request appears with `user: "alice"` (from the cert CN).

## Failure modes

| Symptom | Diagnosis |
|---|---|
| Server fails to start with `httputil: TLS cert "..." provided but key file is empty` | `--tls-cert` was set without `--tls-key`. Both are required together. |
| Server fails to start with `httputil: loading TLS cert/key: ...` | Cert / key file is missing, unreadable, malformed PEM, or the two don't match. The startup error is fail-fast intentionally — operators fix the configuration before any connection is accepted. |
| Server fails to start with `httputil: loading client CA pool: ...` | `--tls-ca` points at an unreadable file or one with no valid PEM blocks. |
| Client gets a TLS handshake error | mTLS is on (`--tls-ca` set) and the client either presented no cert or presented one not signed by the configured CA. The handler never runs; nothing is logged at the application layer. |
| Client connects but audit log shows `verification_source: "pam"` instead of `service_cert_cn` | The client connected without a client cert (TLS-only, not mTLS). Either `--tls-ca` isn't set on the server, or the operator's client config omitted the cert. |

## Cross-references

- [`auth-design.md` §L2a](auth-design.md#l2a--listener-side-tls-operator-to-server) — design rationale + threat model.
- [`pkg/httputil/tls.go`](../../pkg/httputil/tls.go) — `LoadServerTLSConfig` / `LoadClientTLSConfig`.
- [`pkg/httputil/conn_creds.go`](../../pkg/httputil/conn_creds.go) — `ServiceCertCN` and context plumbing.
- [`pkg/newtron/api/caller_middleware.go`](../../pkg/newtron/api/caller_middleware.go) — caller-priority chain (cert CN > Unix peer creds > PAM > session key > self-attested header).
- [`pam-howto.md`](pam-howto.md) — L2b user-to-service PAM (alternative identity path for password-based deployments).
