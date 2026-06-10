# Inter-Service mTLS — Operational HOWTO

The mTLS feature (auth-design.md L2a) protects inter-service HTTP
calls between the three standalone engine binaries:

- `newtron-server` ↔ `newtlab-server` (newtron asks newtlab for SSH
  port allocations)
- `newtlab-server` ↔ `newtron-server` (newtlab reads specs)
- `newtrun-server` ↔ `newtlab-server` (newtrun runs lab deploy /
  destroy through newtlab)
- `newtrun-server` ↔ `newtron-server` (newtrun reads topology and
  drives device operations)

When all four flags are configured on all three binaries, every
cross-process call uses mTLS. The audit log on the receiving side
records `verification_source: "service_cert_cn"` and the cert's
Subject CN as the caller identity.

## 1. When to use L2a

| Deployment | L2a status |
|---|---|
| Composed `newt-server` (all engines in one process) | **No-op** — cross-engine calls are in-process Go calls. The same `--tls-cert` flag could secure external traffic to the composed listener, but inter-engine isolation isn't a feature of this layout. |
| Three standalone binaries on one host, talking via localhost | L2a is **valuable but not strictly required** — localhost-only TCP is hard to MITM. Recommended if the host has other users or services. |
| Three standalone binaries on separate hosts (multi-host lab) | L2a is **required**. Without it, any process on the network can impersonate any engine. |

## 2. Flag inventory

The three standalone server binaries take the same three flags:

| Binary | Flags |
|---|---|
| `newtron-server` | `--tls-cert`, `--tls-key`, `--tls-ca` |
| `newtlab-server` | `--tls-cert`, `--tls-key`, `--tls-ca` |
| `newtrun-server` | `--tls-cert`, `--tls-key`, `--tls-ca` |

Each binary's `--tls-cert` + `--tls-key` serves a dual role: it is the
server cert presented to incoming peer connections AND the client
cert presented when this engine dials another engine. This is the
typical service-mesh pattern; it keeps the operator workflow at one
cert per engine.

`--tls-ca` is the CA bundle used for two checks:

- Verifying the *incoming* client cert (mTLS on this engine's
  listener).
- Verifying the *outgoing* server cert when this engine dials
  another.

Both directions trust the same CA. When you set all three flags on
all three binaries, every cross-engine call ends up with mTLS.

**Enable/disable per `auth-design.md` §2.4:** all three flags default
empty. With no flags set, the binaries serve plain HTTP and clients
dial plain HTTP — the pre-L2a behavior is preserved exactly.

## 3. Set up a small CA

The example below uses `openssl` to produce a CA, three engine
certs, and the matching keys. Adapt to your CA tooling of choice
(cfssl, smallstep CA, your existing org CA, etc.) — newtron does not
care which CA backend produces the PEM files.

```sh
mkdir -p ~/.newtron/tls && cd ~/.newtron/tls
chmod 700 .

# Root CA (long-lived, kept offline if possible)
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -x509 -new -key ca.key -days 3650 -out ca.pem \
    -subj "/CN=newtron-lab-ca"

# Per-engine certs. The CN is the identity that shows up in the
# audit log on the receiving side, so use a name that means
# something: e.g., "newtron-server", "newtlab-server",
# "newtrun-server".
for engine in newtron-server newtlab-server newtrun-server; do
    openssl ecparam -name prime256v1 -genkey -noout -out ${engine}.key
    openssl req -new -key ${engine}.key -out ${engine}.csr \
        -subj "/CN=${engine}"
    openssl x509 -req -in ${engine}.csr -CA ca.pem -CAkey ca.key \
        -CAcreateserial -days 365 \
        -extensions extfile -extfile <(printf '%s' "
            subjectAltName=DNS:localhost,DNS:${engine},IP:127.0.0.1
            keyUsage=critical,digitalSignature,keyEncipherment
            extendedKeyUsage=serverAuth,clientAuth
        ") -out ${engine}.pem
    rm ${engine}.csr
done

chmod 600 *.key *.pem
ls -l
```

You end up with:

- `ca.pem` — the CA to point every engine at via `--tls-ca`
- `ca.key` — keep this somewhere safe; not needed by any engine at
  runtime
- `<engine>.pem` + `<engine>.key` — per-engine cert pair

## 4. Start the engines

```sh
# Engine 1: newtron-server
bin/newtron-server \
    --listen 0.0.0.0:19080 \
    --tls-cert ~/.newtron/tls/newtron-server.pem \
    --tls-key  ~/.newtron/tls/newtron-server.key \
    --tls-ca   ~/.newtron/tls/ca.pem \
    --newtlab-server https://newtlab-host:19082 \
    --spec-dir /etc/newtron/lab &

# Engine 2: newtlab-server
bin/newtlab-server \
    --listen 0.0.0.0:19082 \
    --tls-cert ~/.newtron/tls/newtlab-server.pem \
    --tls-key  ~/.newtron/tls/newtlab-server.key \
    --tls-ca   ~/.newtron/tls/ca.pem \
    --newtron-server https://newtron-host:19080 &

# Engine 3: newtrun-server
bin/newtrun-server \
    --listen 0.0.0.0:19081 \
    --tls-cert ~/.newtron/tls/newtrun-server.pem \
    --tls-key  ~/.newtron/tls/newtrun-server.key \
    --tls-ca   ~/.newtron/tls/ca.pem \
    --newtlab-server https://newtlab-host:19082 &
```

Each engine logs `<label>-server listening on <addr> (https)` at
startup — the `(https)` confirms TLS is active.

## 5. Verify

Use `curl` with the same CA + a client cert from your CA to confirm
the listener accepts authenticated callers:

```sh
curl --cacert ~/.newtron/tls/ca.pem \
     --cert   ~/.newtron/tls/test-client.pem \
     --key    ~/.newtron/tls/test-client.key \
     https://newtron-host:19080/newtron/v1/health
```

A client without a cert, or with one signed by a different CA,
should be rejected at the TLS handshake. The server log line for
those attempts says `TLS handshake error from <addr>: tls: client
didn't provide a certificate` or `tls: bad certificate`.

If you have `--audit-log` configured (auth-design.md L1), each
successful cross-engine call shows up in the JSON-lines audit log
with:

```json
{
  "user": "newtlab-server",
  "verification_source": "service_cert_cn",
  "operation": "POST /newtron/v1/networks/default/...",
  ...
}
```

## 6. Threat model — what L2a addresses, what it doesn't

**Addressed**:

- *Service impersonation across the wire.* Without a valid cert,
  no process can claim to be a peer engine. The kernel + TLS stack
  drop the connection at the handshake; the audit log doesn't
  even record the attempt past `TLS handshake error`.
- *Tampering / passive listening on inter-service traffic.* TLS
  encrypts the channel; the operator's network doesn't have to be
  trusted to be a transport.
- *Cross-engine identity attribution.* Receiving engines log the
  caller's verified CN; a reviewer can answer "which engine asked
  for this?" without trusting any header.

**Not addressed in L2a**:

- *Authorization.* L2a verifies who the caller is, not what they're
  allowed to do. L3 enforces the entitlement pattern when
  `--enforce-authorization` is set — the verified cert CN flows
  through `auth.Context.Caller` and the spec-declared grants in
  `network.json` decide what the peer may do. Without
  `--enforce-authorization`, every verified peer can call every
  endpoint. (See [`authorization-howto.md`](authorization-howto.md).)
- *Cert revocation / rotation.* The shipped pattern reloads on
  server restart. Per-cert rotation requires restarting the engine
  holding that cert. CRL / OCSP support is not implemented;
  operators bound the blast radius with short cert lifetimes (the
  example above uses 365-day engine certs).
- *Identity in the composed `newt-server` binary.* Inter-engine calls
  there are in-process Go function calls, not network calls. L2a
  has nothing to enforce; the same flags do nothing useful in that
  topology.

## 7. Cross-references

- [`auth-design.md`](auth-design.md) — L2a in the layered auth plan
- [`authorization-howto.md`](authorization-howto.md) — L3
  authorization enforcement (the next layer in the arc)
- [`hld.md`](hld.md) §9 — operator-facing security framing
- [`secret-store.md`](secret-store.md) — L0 secret store (cert and
  key paths are filesystem references, not secret-store keys —
  they're paths to PEM files, not secret material)
- `pkg/httputil/tls.go` — `LoadServerTLSConfig` /
  `LoadClientTLSConfig` source
- `pkg/httputil/conn_creds.go` — `ServiceCertCNFromRequest` source
