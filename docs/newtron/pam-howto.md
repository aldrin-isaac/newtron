# User-to-Service PAM — Operational HOWTO

The PAM feature (auth-design.md L2b) authenticates remote operators
calling `bin/newt-server` over TCP. newt-server runs each request
through HTTP Basic + the host's PAM stack via `pam_authenticate` +
`pam_acct_mgmt`. On success the operator's Unix username flows into
`audit.Caller` with `verification_source: "pam"`. On failure:
HTTP 401.

`cmd/newt-server` is the only binary in this project that runs PAM
authentication. The three standalone server binaries
(`newtron-server`, `newtrun-server`, `newtlab-server`) are loopback
dev tools with no encryption and no authentication; use newt-server
for any deployment that needs verified identity.

## 1. When to use PAM authentication

PAM authentication applies whenever `bin/newt-server` accepts TCP
requests from operators:

| Listener type | PAM authentication applies? |
|---|---|
| Plain TCP (loopback or non-loopback) | **Yes** — without it, any process reaching the listener can act as any user. |
| Unix socket (auth-design.md L1) | **No** — kernel peer creds already identify the caller; the middleware skips PAM. |
| mTLS (auth-design.md L2a; `--tls-cert`/`--tls-key`/`--tls-ca` on `cmd/newt-server`) | **No** — the verified peer cert CN identifies the caller and the middleware skips PAM. |

The middleware skips automatically — no separate configuration
required. Operators set `--auth-pam-service=NAME` on `cmd/newt-server`
and PAM authentication activates for the TCP listener.

**Enable/disable per auth-design.md §2.4:** `--auth-pam-service` defaults
empty; with no flag set, TCP requests pass through unauthenticated
(pre-PAM behavior). Set it to a PAM service name to enable.

## 2. Configure PAM on the host

PAM (Pluggable Authentication Modules) is the Linux-standard
authentication framework. Operators configure PAM by writing
`/etc/pam.d/<service>` files. `cmd/newt-server` is the only binary
in this project that reads PAM — the standalone server binaries
(`newtron-server`, `newtrun-server`, `newtlab-server`) are
loopback dev tools with no encryption or authentication.

The examples below use the service name `newt-server`; operators
can pick any name and pass it via `--auth-pam-service`.

### 2a. Minimal config: local Unix accounts

```sh
# /etc/pam.d/newt-server
auth     required pam_unix.so
account  required pam_unix.so
```

`pam_unix` verifies against `/etc/passwd` + `/etc/shadow`. Any
local user can authenticate. Suitable for single-host deployments
where the team already has shell accounts on the engine host.

### 2b. With SSSD (LDAP / Active Directory)

```sh
# /etc/pam.d/newt-server
auth     required pam_sss.so
account  required pam_sss.so
```

`pam_sss` defers to the SSSD daemon which talks to LDAP / AD.
Suitable for org-wide deployments where the directory is the
source of truth for identities.

### 2c. With Kerberos

```sh
# /etc/pam.d/newt-server
auth     required pam_krb5.so
account  required pam_krb5.so
```

Suitable when the operator already runs a Kerberos realm.

### 2d. With pam_listfile to gate access

```sh
# /etc/pam.d/newt-server
auth     required pam_listfile.so item=user sense=allow file=/etc/newtron/operators onerr=fail
auth     required pam_unix.so
account  required pam_unix.so
```

`pam_listfile` rejects users not listed in `/etc/newtron/operators`.
The list is one username per line. Suitable for "any Unix user but
only these are allowed to operate newtron."

PAM is composable — these are starting points; operators arrange
modules as their security posture requires.

## 3. Start newt-server with PAM

`cmd/newt-server` accepts one flag for PAM:

```sh
bin/newt-server \
    --listen 0.0.0.0:18080 \
    --auth-pam-service newt-server \
    ...
```

The value is the name of the file under `/etc/pam.d/`. PAM
authentication on `newt-server` protects external operator traffic
to the composed listener; in-process inter-engine calls (newtrun →
newtron, newtlab → newtron) go through the same outer middleware
chain so the operator's Bearer or PAM identity flows through
unchanged.

## 4. Verify

Operators authenticate via HTTP Basic. From a CLI:

```sh
curl -u alice http://newtron-host:18080/newt-server/v1/health
Enter host password for user 'alice':
```

(The example uses plain HTTP because listener-side TLS isn't wired
into `cmd/newt-server` yet — see [`mtls-howto.md`](mtls-howto.md).
If newt-server sits behind a TLS-terminating reverse proxy, hit
`https://newtron-host/newt-server/v1/health` against the proxy.)

A correct password yields `{"status":"ok",...}`. A wrong password
yields:

```
HTTP/1.1 401 Unauthorized
authentication failed
```

With `--audit-log` configured (auth-design.md L1), each
authenticated request shows up in the JSON-lines audit log with:

```json
{
  "user": "alice",
  "verification_source": "pam",
  "operation": "POST /newtron/v1/networks/default/...",
  ...
}
```

## 5. Concurrent layers

PAM authentication composes with the other identity sources:

| Listener type for this request | What identifies the caller |
|---|---|
| Unix socket | `SO_PEERCRED` (L1) — PAM is skipped |
| mTLS-protected TCP | Verified peer cert CN (L2a) — PAM is skipped |
| Plain TCP with HTTP Basic | PAM-verified Unix username (L2b) |
| Plain TCP with `Authorization: Bearer <key>` | Username resolved by the L2c session-key store; PAM is skipped (the original `/auth/login` was PAM-authenticated) |
| Plain TCP without Basic auth or Bearer | 401 from PAMMiddleware before any handler runs |

The priority order is fixed in `pkg/newtron/api/caller_middleware.go`:
cert CN > Unix peer creds > PAM > session-key Bearer > self-attested
header. A reviewer reading the audit log can always tell which path
provided the identity by the `verification_source` field
(`service_cert_cn`, `unix_peer_creds`, `pam`, `session_key`,
`self_attested_header`).

## 6. Threat model — what PAM authentication addresses, what it doesn't

**Addressed**:

- *User impersonation on the TCP listener.* Without valid PAM
  credentials, requests are rejected at HTTP 401. The handler never
  runs; the audit log records the rejection if logging is enabled.
- *Identity attribution for the audit log.* PAM-authenticated
  requests carry the PAM-verified username in `audit.Caller` — a
  reviewer can answer "who did this?" without trusting any header.
- *Composability with directory backends.* By delegating to PAM,
  newtron inherits whatever the operator's identity backend already
  provides (LDAP via `pam_sss`, Kerberos via `pam_krb5`, local Unix
  via `pam_unix`, etc.). newtron does not ship a parallel user
  database.

**Not addressed by PAM authentication**:

- *Authorization.* PAM authentication verifies who the user is, not
  what they're allowed to do. Authorization enforcement (auth-design.md
  L3) runs when `--enforce-authorization` is set — the PAM-verified
  username flows through `auth.Context.Caller` and the spec-declared
  grants in `network.json` decide what the user may do. See
  [`authorization-howto.md`](authorization-howto.md).
- *Brute-force protection.* PAM modules like `pam_faillock` /
  `pam_tally2` provide rate-limiting; newtron does not add an HTTP-
  layer rate limiter. Operators configure that at the PAM level or
  via a fronting reverse proxy.
- *Password transit security.* HTTP Basic sends credentials base64-
  encoded but not encrypted. **PAM authentication without TLS is insecure.**
  `cmd/newt-server` accepts `--tls-cert` / `--tls-key` / `--tls-ca`
  flags (and the shared `NEWTRON_TLS_CERT` / `NEWTRON_TLS_KEY` /
  `NEWTRON_TLS_CA` env vars) to serve the listener over TLS — see
  [`mtls-howto.md`](mtls-howto.md). Alternatives: terminate TLS at a
  reverse proxy in front of newt-server, OR restrict the listener
  to loopback / VPN / Unix socket.
- *Session reuse.* `pam_authenticate` runs on every request by
  default — no cookie, no token. For browser clients and long-running
  automations this gets expensive. **L2c (session keys)** layers on
  top: a successful PAM auth at `POST /auth/login` mints an opaque
  short-lived bearer token the client carries on subsequent calls.
  See [§7 below](#7-session-keys-l2c) and
  [`auth-design.md` §L2c](auth-design.md).

## 7. Session keys (L2c)

When `--auth-pam-service` is set on `cmd/newt-server`, two routes
live at the server boundary alongside the per-request PAM flow:

```
POST /newt-server/v1/auth/login         (Authorization: Basic …)
POST /newt-server/v1/auth/logout        (Authorization: Bearer …)
```

These are server-wide, not engine-scoped — one key works against
every engine mounted on this newt-server (`/newtron/v1/*`,
`/newtrun/v1/*`, `/newtlab/v1/*`). The standalone server binaries
(`newtron-server`, `newtrun-server`, `newtlab-server`) do not mount
these routes; they are loopback dev tools with no encryption or
authentication.

`/auth/login` runs PAM exactly as L2b would for any other endpoint;
on success it returns a JSON body with a random 256-bit opaque key,
the absolute expiry timestamp, and the verified username:

```sh
curl -X POST -u alice:correct-password \
    http://localhost:18080/newt-server/v1/auth/login
# {"key":"…43 chars…","expires_at":"2026-06-11T08:00:00Z","user":"alice"}
```

The client then uses the key on every subsequent request:

```sh
curl -H "Authorization: Bearer …" \
    http://localhost:18080/newtron/v1/networks/...
```

The key expires after `--session-key-ttl` (default `8h`). Using a
key does **not** extend its lifetime — the expiry is absolute. To
revoke immediately, call `/auth/logout`:

```sh
curl -X POST -H "Authorization: Bearer …" \
    http://localhost:18080/newt-server/v1/auth/logout
# 204 No Content
```

Logout is idempotent. Revoking a key that was never issued, or one
that already expired, still returns 204 — the operator's intent
("this key must not work") is satisfied either way.

**Server restart invalidates every key.** The store is in-memory by
design — persistence would introduce a credential file with the
same protection class as `--secret-store` and is out of scope. A
restart is operator-visible, so clients re-logging-in after one is
the expected behavior.

**Tightening revocation.** A user disabled in the directory keeps
working under any pre-existing key until that key expires or logs
out, because L2c does not call back into PAM after issuance.
Operators who need tighter binding lower `--session-key-ttl`. The
revocation half of L6 (`--spec-watch`) still removes the user's
*authorization* (grants in `network.json`) on the next reload, so a
revoked-but-still-logged-in user gets 403 on every gated request
even while their key is technically still valid.

**Disabling L2c without disabling L2b.** Pass `--session-key-ttl=-1`
to suppress session keys even when PAM is on. `/auth/login` and
`/auth/logout` then 404, and every request hits PAM directly. Use
when audit semantics require "every request authenticated against
the live directory" — a tradeoff with the per-request cost.

### Per-user CLI session cache

For human operators at terminals, the `newtron`, `newtrun`, and
`newtlab` CLIs share a per-user session cache under
`~/.newtron/sessions/` — one file per (user, server) pair, named
`<user>@<host>.json` (mode `0600`). One `newtron auth login` mints
a key and persists it; every subsequent CLI invocation reads the
cache and carries `Authorization: Bearer <key>` automatically.

```sh
newtron auth login
# Username (for http://localhost:18080): alice
# Password:
# Logged in as alice (server http://localhost:18080); session expires Thu, 12 Jun 2026 02:00:00 PDT.
# Session cached at /home/alice/.newtron/sessions/alice@localhost.json (mode 0600).

# Now every CLI uses the cached key — no further prompts.
newtron service list
newtrun start 2node-vs-primitive
newtlab status

newtron auth status
# User:       alice
# Server:     http://localhost:18080
# Expires:    Thu, 12 Jun 2026 02:00:00 PDT (in 7h59m)
# Cached at:  /home/alice/.newtron/sessions/alice@localhost.json

newtron auth logout
# Logged out.
```

The cache stores `{server, user, key, expires_at}` per file — same
fields the server returns from `/auth/login`. Re-login replaces the
existing cached session for the same (user, server) pair.
The file mode is strictly `0600`; if it ever drifts (e.g. someone
`chmod 644`s it), `LoadSession` returns an error and the CLI refuses to use the
credential rather than silently sending a key anyone on the host
could have tampered with.

**Identity flows through the request.** When a CLI like
`newtrun start` posts to `/newtrun/v1/runs`, it carries the
operator's Bearer (from `~/.newtron/sessions/`). The runner
extracts the Bearer from the inbound Authorization header and
attaches it on every outbound newtron call — the operator's
identity is the runner's identity. Per-scenario `as: <user>`
switches that scenario's outbound calls to the named user's
cached Bearer (the operator pre-cached it via
`newtron auth login --user <user>`).
Scripted automation submits a run the same way: POST to
`/newt-server/v1/auth/login` with HTTP Basic, persist the key,
post to `/newtrun/v1/runs` with that key on the Authorization
header.

## 8. Cross-references

- [`auth-design.md`](auth-design.md) — L2b in the layered auth plan
- [`authorization-howto.md`](authorization-howto.md) — L3
  authorization enforcement (the next layer in the arc)
- [`hld.md`](hld.md) §9 — operator-facing security framing
- [`mtls-howto.md`](mtls-howto.md) — L2a listener-side TLS (pair
  L2a + L2b for the full transport-authentication picture)
- `pkg/httputil/auth.go` — `Authenticator` interface + `PAMMiddleware`
- `pkg/httputil/pamauth/authenticator.go` — cgo-backed `PAMAuthenticator`
- `pkg/httputil/sessionkey/` — L2c in-memory key store
  (`store.go`), Bearer middleware + identity context-key
  (`middleware.go`), and the `/auth/login` + `/auth/logout`
  HTTP handlers (`handlers.go`). The package is engine-neutral;
  `cmd/newt-server` mounts it at the outer chain.
- `pkg/newtron/api/caller_middleware.go` — caller-identity
  priority chain that reads PAM (L2b) and session-key (L2c)
  usernames off the request context and tags the audit caller
