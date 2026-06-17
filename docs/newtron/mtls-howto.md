# Listener-Side TLS (Operator-to-Server) — Operational HOWTO

L2a listener-side TLS is configured by three values shared between
`cmd/newt-server` and the in-repo CLIs (`bin/newtron`, `bin/newtrun`,
`bin/newtlab`):

| Source | What |
|---|---|
| `NEWTRON_TLS_CERT` env var | Path to a PEM certificate. On the server, this is the server cert; on a CLI, the client cert (mTLS identity). |
| `NEWTRON_TLS_KEY` env var | Path to the matching PEM private key. |
| `NEWTRON_TLS_CA` env var | Path to a CA bundle. On the server, the trust anchor for verifying client certs; on a CLI, the trust anchor for verifying the server cert. |

The server also accepts equivalent flags `--tls-cert`/`--tls-key`/`--tls-ca` that override the env vars when set; the CLIs read env only. **Unset everywhere → plain HTTP** (pre-L2a behavior).

This is the "automatic" deployment shape: one `export` line in the operator's shell profile (or systemd unit) drives every binary in the repo. See [`auth-design.md` §L2a](auth-design.md#l2a--listener-side-tls-operator-to-server) for the threat-model rationale.

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

3. **Server host — set env, start `cmd/newt-server`:**

   ```sh
   # /etc/systemd/system/newt-server.service (env block) or operator shell
   export NEWTRON_TLS_CERT=/etc/newt-server/server.crt
   export NEWTRON_TLS_KEY=/etc/newt-server/server.key
   export NEWTRON_TLS_CA=/etc/newt-server/operators-ca.crt

   bin/newt-server \
     --listen 0.0.0.0:18443 \
     --audit-log /var/log/newt-server-audit.jsonl \
     --enforce-authorization \
     --networks-base /etc/newt-server/networks
   ```

   No `--tls-*` flags needed; env vars are picked up.

4. **Operator workstation — set env, run CLI:**

   ```sh
   export NEWTRON_TLS_CERT=$HOME/.newtron/alice.crt
   export NEWTRON_TLS_KEY=$HOME/.newtron/alice.key
   export NEWTRON_TLS_CA=$HOME/.newtron/ca.crt
   export NEWTRON_SERVER=https://newt-server.example.com:18443

   bin/newtron leaf1 service list
   ```

   The CLI picks up env, builds a client TLS config, presents alice's cert, dials the server. No CLI flags needed.

5. **Optional curl-based smoke test (no CLI involved):**

   ```sh
   curl --cacert $NEWTRON_TLS_CA \
        --cert $NEWTRON_TLS_CERT \
        --key $NEWTRON_TLS_KEY \
        $NEWTRON_SERVER/newtron/v1/networks
   ```

6. **Verify the audit log:**

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
