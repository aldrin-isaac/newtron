# User-to-Service PAM — Operational HOWTO

The PAM feature (auth-design.md L2b) authenticates remote operators
calling newtron / newtlab / newtrun engines over TCP. Each engine
runs the request through HTTP Basic + the host's PAM stack via
`pam_authenticate` + `pam_acct_mgmt`. On success the operator's
Unix username flows into `audit.Caller` with
`verification_source: "pam"`. On failure: HTTP 401.

L2b pairs symmetrically with L2a (inter-service mTLS): together they
close both transport-authentication surfaces. Once both layers are
active, every request reaching an engine carries a verified
identity — operator (PAM-verified) or peer engine (cert CN).

## 1. When to use L2b

L2b applies whenever an engine accepts TCP requests from operators.
The composed `newt-server` binary, the three standalone engine
binaries, and any topology that exposes a non-loopback listener all
benefit:

| Listener type | L2b applies? |
|---|---|
| Plain TCP (loopback or non-loopback) | **Yes** — without it, any process reaching the listener can act as any user. |
| Unix socket (L1) | **No** — kernel peer creds already identify the caller; the middleware skips PAM. |
| mTLS (L2a) | **No** — the verified peer cert CN identifies the caller; the middleware skips PAM. |

The middleware skips automatically — no separate configuration
required. Operators just set `--auth-pam-service=NAME` on the TCP
side and L2b activates for that surface.

**Enable/disable per auth-design.md §2.4:** `--auth-pam-service` defaults
empty; with no flag set, TCP requests pass through unauthenticated
(pre-L2b behavior). Set it to a PAM service name to enable.

## 2. Configure PAM on the host

PAM (Pluggable Authentication Modules) is the Linux-standard
authentication framework. Operators configure PAM by writing
`/etc/pam.d/<service>` files — each engine reads its own service
config independently. Multiple engines can share one service
config or have separate ones (e.g., to gate `newtrun-server`
behind a stricter group requirement).

### 2a. Minimal config: local Unix accounts

```sh
# /etc/pam.d/newtron-server
auth     required pam_unix.so
account  required pam_unix.so
```

`pam_unix` verifies against `/etc/passwd` + `/etc/shadow`. Any
local user can authenticate. Suitable for single-host deployments
where the team already has shell accounts on the engine host.

### 2b. With SSSD (LDAP / Active Directory)

```sh
# /etc/pam.d/newtron-server
auth     required pam_sss.so
account  required pam_sss.so
```

`pam_sss` defers to the SSSD daemon which talks to LDAP / AD.
Suitable for org-wide deployments where the directory is the
source of truth for identities.

### 2c. With Kerberos

```sh
# /etc/pam.d/newtron-server
auth     required pam_krb5.so
account  required pam_krb5.so
```

Suitable when the operator already runs a Kerberos realm.

### 2d. With pam_listfile to gate access

```sh
# /etc/pam.d/newtron-server
auth     required pam_listfile.so item=user sense=allow file=/etc/newtron/operators onerr=fail
auth     required pam_unix.so
account  required pam_unix.so
```

`pam_listfile` rejects users not listed in `/etc/newtron/operators`.
The list is one username per line. Suitable for "any Unix user but
only these are allowed to operate newtron."

PAM is composable — these are starting points; operators arrange
modules as their security posture requires.

## 3. Start the engines with PAM

Each standalone server binary takes one new flag:

```sh
bin/newtron-server \
    --listen 0.0.0.0:19080 \
    --auth-pam-service newtron-server \
    ...

bin/newtlab-server \
    --listen 0.0.0.0:19082 \
    --auth-pam-service newtlab-server \
    ...

bin/newtrun-server \
    --listen 0.0.0.0:19081 \
    --auth-pam-service newtrun-server \
    ...
```

Each value is the name of the file under `/etc/pam.d/`. Engines may
share one PAM service ("newtron") or have separate ones; the
example above uses per-engine services so operators can gate them
differently.

The composed `bin/newt-server` accepts the same flag and applies
PAM authentication to its single combined TCP listener. There is no
inter-engine authentication concern there (the engines share one
process) — L2b on `newt-server` protects external operator traffic
to the composed listener.

## 4. Verify

Operators authenticate via HTTP Basic. From a CLI:

```sh
curl -u alice https://newtron-host:19080/newtron/v1/health
Enter host password for user 'alice':
```

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

L2b composes with the other identity sources:

| Listener type for this request | What identifies the caller |
|---|---|
| Unix socket | `SO_PEERCRED` (L1) — PAM is skipped |
| mTLS-protected TCP | Verified peer cert CN (L2a) — PAM is skipped |
| Plain TCP with HTTP Basic | PAM-verified Unix username (L2b) |
| Plain TCP without Basic auth | 401 from PAMMiddleware before any handler runs |

The priority order is fixed in the caller middleware: cert CN > peer
creds > PAM > self-attested header. A reviewer reading the audit log
can always tell which path provided the identity by the
`verification_source` field.

## 6. Threat model — what L2b addresses, what it doesn't

**Addressed**:

- *User impersonation on the TCP listener.* Without valid PAM
  credentials, requests are rejected at HTTP 401. The handler never
  runs; the audit log records the rejection if logging is enabled.
- *Identity attribution for the audit log.* L2b-authenticated
  requests carry the PAM-verified username in `audit.Caller` — a
  reviewer can answer "who did this?" without trusting any header.
- *Composability with directory backends.* By delegating to PAM,
  newtron inherits whatever the operator's identity backend already
  provides (LDAP via `pam_sss`, Kerberos via `pam_krb5`, local Unix
  via `pam_unix`, etc.). newtron does not ship a parallel user
  database.

**Not addressed in L2b**:

- *Authorization.* L2b verifies who the user is, not what they're
  allowed to do. L3 enforces the entitlement pattern when
  `--enforce-authorization` is set — the PAM-verified username flows
  through `auth.Context.Caller` and the spec-declared grants in
  `network.json` decide what the user may do. See
  [`authorization-howto.md`](authorization-howto.md).
- *Brute-force protection.* PAM modules like `pam_faillock` /
  `pam_tally2` provide rate-limiting; newtron does not add an HTTP-
  layer rate limiter. Operators configure that at the PAM level or
  via a fronting reverse proxy.
- *Password transit security.* HTTP Basic sends credentials base64-
  encoded but not encrypted. **L2b without TLS is insecure.** Combine
  with L2a (`--tls-cert`/`--tls-key`/`--tls-ca`) for the listener,
  OR put a TLS-terminating reverse proxy in front, OR restrict the
  listener to loopback / VPN / Unix socket.
- *Session reuse.* Each request goes through `pam_authenticate`
  independently — there's no cookie or token. Suitable for CLI /
  programmatic use; not yet suitable for browser sessions (a future
  addition could add a PAM-issued short-lived token endpoint).

## 7. Cross-references

- [`auth-design.md`](auth-design.md) — L2b in the layered auth plan
- [`authorization-howto.md`](authorization-howto.md) — L3
  authorization enforcement (the next layer in the arc)
- [`hld.md`](hld.md) §9 — operator-facing security framing
- [`mtls-howto.md`](mtls-howto.md) — L2a inter-service mTLS (pair
  L2a + L2b for the full transport-authentication picture)
- `pkg/httputil/auth.go` — `Authenticator` interface + `PAMMiddleware`
- `pkg/httputil/pamauth/authenticator.go` — cgo-backed `PAMAuthenticator`
- `pkg/newtron/api/caller_middleware.go` — PAM username
  priority + audit caller binding
