# Auth Design — Layered Path to Production-Grade

## 1. Purpose

newtron's authorization code commits to an **entitlement pattern**
(spec-declared permissions, group-based grants, L5 `where`-clause
scoping, superuser bypass). Before this doc shipped, the runtime was inert —
no `main()` ever wired the Checker, so every `checkPermission`
short-circuited to "allowed." This doc charts the path from that
starting point to a production-grade auth subsystem where the
entitlement pattern is the design destination, **not** the design
starting point.

The path is **seven layers**. Each layer:

- Closes a specific gap that would otherwise fail a security audit.
- Ships as one PR. Each PR is independently reviewable.
- Has audit-criteria-met-when-this-layer-lands. The auditor signs off on
  the layer's scope without future layers existing.
- Builds on (but never breaks) the previous layer's contract.

This doc is the **L0** deliverable — the threat model and the layered plan.
The remaining six layers reference back here.

---

## 2. Threat Model

The auth subsystem defends against the threats listed in §2.1. Threats in
§2.2 are explicitly out of scope; an operator deploying newtron must
address them through other means (the network, the host, organizational
controls).

### 2.1 In Scope

The three engines run in one process (`cmd/newt-server`); inter-
engine calls are in-process Go function calls and never traverse a
network boundary. The remaining threat surface is the
operator-to-server channel: a human operator's CLI or browser
talking to `cmd/newt-server`. L2a (listener-side TLS) and L2b
(PAM) protect that channel; L2c (session keys) caches the PAM
result so the operator's CLI doesn't have to re-present
credentials per request.

| Threat | Surface | Layer that addresses it |
|---|---|---|
| **Insider misuse — accidental.** A teammate runs the wrong CLI against the wrong network and changes config they shouldn't have. | Operator-to-server | L1 (audit log catches), L3 (authorization gates) |
| **Insider misuse — deliberate.** A current team member with shell access tries to modify resources outside their role. | Operator-to-server | L3 + L5 (per-resource grants) |
| **Forensic accountability.** "Who deleted that VLAN at 03:42?" is answerable from the system itself. | Operator-to-server | L1 |
| **User impersonation across the wire.** Someone on the network sends requests claiming to be alice. | Operator-to-server | L2b (PAM for TCP; Unix peer creds for local; L2c session keys derived from L2b) |
| **Credential reuse across requests.** A long-running automation or browser session has to embed a password in every call, or proxy it through a separate session layer. | Operator-to-server | L2c (PAM-issued short-lived session keys) |
| **newt-server impersonation.** A rogue process on the network pretends to be `cmd/newt-server` and accepts operator traffic claiming to be the real service. | Operator-to-server | L2a (listener-side TLS — `--tls-cert`/`--tls-key`/`--tls-ca` on `cmd/newt-server`) |
| **Stale grants.** Former team member's permissions linger after they leave. | Operator-to-server | L6 (revocation) |
| **Coverage holes.** A new mutation method ships without a `checkPermission` call and bypasses authorization silently. | Both | L4 (coverage closure test) |
| **Secret leakage from spec.** Device profile passwords sit in plain JSON on disk and in version control. | Foundational | L0 (encryption at rest) |
| **Authorization decisions without trace.** A "deny" happened but nobody can prove it. | Both | L1 + L3 (audit emits allow + deny) |

### 2.2 Out of Scope

| Out-of-scope threat | Why | Operator mitigation |
|---|---|---|
| **Compromised CA / cert issuance pipeline.** | L2 trusts the operator's CA. Compromising the CA bypasses everything. | Operator runs the CA. Standard CA hygiene. |
| **Hostile actor with shell access on the newtron-server host.** | Auth runs in the server process; root on the host bypasses auth. | Standard host hardening. |
| **Hostile actor with read access to `network.json`.** | Permission grants and group memberships are visible to anyone who reads the spec. | File-system permissions on spec dir; private git repo. |
| **Denial of service.** Rate limits, request size limits, expensive-query throttling. | Auth doesn't address availability. | Reverse proxy or kernel limits. |
| **Side channels (timing, etc.).** | Constant-time comparison of permissions is overkill for this threat model. | Network isolation; not a multi-tenant SaaS. |
| **Supply chain.** | Go module pinning and reproducible builds are infrastructure concerns. | Standard SBOM + module pinning. |
| **Advanced identity providers (OIDC, SAML, LDAP, bearer tokens, federated SSO).** | The PAM stack already covers LDAP / Kerberos / local-account / SSSD flows through pluggable modules. Going beyond it requires a concrete deployment that demands it. | Operator configures PAM modules (`pam_ldap`, `pam_sss`, `pam_krb5`) for whatever identity backend they run. Revisit a native newtron OIDC/SAML/token mechanism only if PAM proves insufficient. |

### 2.3 Assumptions

Each layer assumes the previous layers are in place. Specifically:

- L3 (authorization) assumes L2 (verified identity from L2b PAM —
  or L2a listener-side TLS cert CN when wired). Without verified
  identity, authorization decides on a self-attested name — same
  security posture as no authorization.
- L5 (fine-grained grants) assumes L4 (universal coverage). Per-resource
  grants on some operations and no grants on others creates an
  exploitable asymmetry.
- L6 (revocation) assumes L3 (something to revoke). Spec reload without
  enforcement does nothing.

L2a (listener-side TLS) and L2b (PAM) are **independently
shippable** because they protect different properties of the same
operator-to-server channel — L2a protects the wire from
eavesdropping and verifies the server's identity to the operator
(plus optionally the operator's cert to the server); L2b verifies
the operator's identity from a password against PAM. Both are
wired into `cmd/newt-server`: L2a via `--tls-cert`/`--tls-key`/
`--tls-ca`, L2b via `--auth-pam-service`. Deployments that don't
ship certs continue to terminate TLS at a reverse proxy in front
of newt-server; the listener-side flags are an alternative, not a
replacement. The combined sub-layer L2 is treated as one in
subsequent layers' dependency lists.

This ordering is **mandatory**, not aesthetic. The audit criteria for each
layer fail if its dependencies are skipped.

### 2.4 Every Layer Is Enable/Disable-able

Every layer ships with its behavior gated by an explicit configuration
toggle, and the default value is "off / preserve current behavior."
Deployments adopt layers at their own pace. Specifically:

- **L0 encryption-at-rest:** `--secret-store=PATH` enables; absent
  means secrets stay plaintext (current behavior).
- **L1 audit log:** `--audit-log=PATH` enables a file logger; absent
  means events route to a no-op logger and nothing is written.
- **L1 Unix socket listener:** `--unix-socket=PATH` enables the
  listener alongside the TCP one; absent means TCP only.
- **L1 caller header:** `--audit-caller-header=NAME` configures the
  TCP fallback header name; empty disables header-based caller
  identity (Unix socket peer creds still work if that listener is
  configured).
- **L2a listener-side TLS:** `--tls-cert=PATH` + `--tls-key=PATH` on
  `cmd/newt-server` enable a TLS listener; the TCP listener serves
  HTTPS instead of HTTP. Adding `--tls-ca=PATH` requires every client
  to present a certificate that verifies against the CA pool (mTLS);
  the verified peer-cert Subject CN flows through
  `httputil.ServiceCertCNFromRequest` into the caller-middleware
  priority slot ahead of PAM. Empty flags preserve plain HTTP (the
  pre-L2a default); operators who don't ship certs can still
  terminate TLS at a reverse proxy in front of `cmd/newt-server` —
  see [`mtls-howto.md`](mtls-howto.md).
- **L2b user-to-service PAM:** `--auth-pam-service=NAME` enables;
  absent means TCP listener doesn't authenticate via PAM (caller
  identity stays self-attested via header per L1).
- **L2c server-issued session keys:** auto-engaged whenever L2b is
  configured — the `/auth/login` and `/auth/logout` routes only
  mount when `--auth-pam-service` is set. `--session-key-ttl=DUR`
  tunes the absolute lifetime of each minted key (default `8h`);
  no separate enable/disable flag because session keys without PAM
  have no credential to derive from.
- **L3 authorization enforcement:** `--enforce-authorization=true`
  invokes `Network.EnableAuthorization` at every `RegisterNetwork` /
  `ReloadNetwork`; default `false` means `checkPermission` stays a
  no-op even though identity from L1/L2 is populated.
- **L4 coverage checks:** controlled by the same
  `--enforce-authorization` toggle as L3. Every mutation has a
  check (verified by `TestAPICompleteness`); checks are bypassed
  uniformly when enforcement is off.
- **L5 fine-grained grants:** dictated by spec format. Old
  shorthand keeps working (it's syntactic sugar for the richer
  form); operators opt into per-resource grants by writing them.
- **L6 revocation + log integrity:** `--spec-watch=true` enables
  the file watcher; `--audit-log-integrity=true` enables the hash
  chain. Default `false`.

Two properties this contract guarantees:

1. **A fresh deployment behaves identically to today.** No flag, no
   change. This protects existing operators (and the project's
   integration tests) from being broken by the layered rollout.
2. **Layers can be adopted independently.** An operator who only
   needs the audit log (L1) can ship it without identity
   verification (L2) or enforcement (L3). An operator on a small
   trusted team can pick the audit log + Unix socket and skip the
   rest indefinitely.

The flags are reviewed during each layer's PR for shape and naming
consistency.

---

## 3. Goal State

Production-grade auth in newtron means **every authenticated caller can do
only what the spec explicitly grants them, and every decision is on the
record**. Concretely, the goal state has these properties — these are the
criteria a security review must be able to verify:

1. **Verified identity on the operator-to-server channel.**
   Requests to `cmd/newt-server` carry a caller identity verified
   via Unix socket peer creds (local), listener-side TLS cert CN
   (when wired), PAM authentication (TCP, fresh credentials), or
   a PAM-issued session key (TCP, cached PAM proof within its
   TTL). The server does not accept self-attested headers as
   authoritative in this goal state.
2. **Closed coverage.** Every mutation method in `pkg/newtron/` checks
   permission before acting. Reads stay ungated.
3. **Per-resource granularity.** Permissions can be scoped to device,
   service, and interface dimensions — not just to verbs.
4. **Spec-declared grants.** Permissions live in `network.json`, version-
   controlled with the rest of the topology. No runtime registry.
5. **Two-tier evaluation.** Service-level grants override global grants;
   superusers bypass both. Already in the Checker; preserved.
6. **Forensic completeness.** Every authorization decision (allow and
   deny) and every spec mutation appears in the audit log with caller,
   action, target, decision, timestamp.
7. **Revocable.** A spec change that removes a grant takes effect within
   a bounded interval, without server restart.
8. **Secret hygiene.** Device profile passwords are encrypted at rest
   (L0 foundation); plaintext exists only in process memory while in
   use. Without this, an attacker who reads the spec directory has
   device-admin access regardless of any later layer's enforcement.
9. **Meta-authority is separable.** "Who can grant access to others"
   is its own permission dimension, not collapsed into "who can edit
   spec files." A service architect can author services without also
   being able to add themselves to a group; an IAM operator can
   manage groups and grants without also being able to edit service
   specs. `super_users` remains the sole bypass — and is itself a
   gated field, not a global escape hatch for anyone with
   `spec.author`. (Lands as part of L5's `where` dimension; see §5
   "Meta-Authorization: Who Can Grant Access.")

The current entitlement pattern (Checker, Permission, Context,
`network.json` permissions map, groups, superusers) **is the destination
shape**. The layered path extends that pattern; it does not replace it.

---

## 4. The Current Entitlement Pattern (What Stays)

These elements are the design destination. They land in production-grade
form unchanged, with the listed additions:

| Element | Current shape | Production-grade additions |
|---|---|---|
| `pkg/newtron/auth/Permission` (verb constants) | 20 constants, 5 referenced | L4 adds `device.write` family; L5 adds dimension-bearing variants if needed |
| `pkg/newtron/auth/Context` (resource context) | 4 dimensions, only `Resource` populated | L3 adds `Caller` (Unix username from L1 Unix socket or L2b PAM; cert CN from L2a listener-side TLS); L5 populates `Device`/`Service`/`Interface` |
| `pkg/newtron/auth/Checker` (decision engine) | Two-tier eval, group fallback, superuser bypass | L5 extends to evaluate dimension constraints |
| `network.json` `permissions` map | `action → [groups]` global | L5 extends entry value to support `{ groups, where: {...} }`; per-service scoping via `where: { service: "..." }` (replaces the retired per-service override — see #165) |
| `network.json` `super_users` | List of usernames who bypass | Unchanged |
| `network.json` `user_groups` | Group name → user list | Unchanged |
| `Network.checkPermission` call sites | 26 sites in `spec_ops`/`profile_ops` | L4 expands coverage to Node ops |

What went away during L1–L3 (and what remains for L4–L5):

- The misleading "Permission-based access control" claim in `cmd/newtron/main.go` package comment — addressed alongside L3.
- The `Network.SetAuth` method being callable but never called — replaced in L3 by `Network.EnableAuthorization`, which is invoked from `api.Server` when `--enforce-authorization` is set. The standalone `SetAuth` was removed (§40).
- The `WithDevice` / `WithService` / `WithInterface` setters being unused — L5 populates them.
- The 15 Permission constants that no proposed coverage uses — L4 prunes them when they're confirmed unused under the new coverage rules.

---

## 5. Layered Path

### L0 — Foundation: Threat Model, Plan, and Secret Hygiene

L0 is the foundation every later layer depends on. It has two
deliverables: the threat model and layered plan (the doc), and
encryption-at-rest of secrets in the spec directory. Without the second
piece, every later layer's enforcement is undermined — an attacker who
reads `profiles/*.json` has the device-admin password, so authorization
checks at the HTTP layer don't matter.

**Goal.** (1) Articulate the threat model, goal state, and layered
path; establish the contract subsequent layers respect. (2) Move device
profile passwords out of plaintext in the spec directory. Secrets are
referenced from `profiles/*.json` by a key name; resolved at process
load time from an operator-configured secret store (file-based KMS,
age-encrypted file, or an interface the operator implements).

**Audit criterion met when this layer lands.** (1) A reviewer can name
the threats the system defends against and the threats out of scope,
and trace each in-scope threat to a layer that addresses it. (2) "Where
are device passwords stored?" → not in `profiles/*.json`. A `grep -r
password newtrun/topologies/` returns only key-references, not
plaintext.

**Dependencies.** None.

**Scope of changes.** The design doc and HLD cross-reference are
shipped. The encryption work is a separate PR that introduces
`pkg/newtron/secret/` with a `Store` interface, a file-based default
backend, and `--secret-store=PATH` config in
`cmd/newtron-server`/`cmd/newt-server`. Migration tooling for existing
plaintext profiles ships alongside.

**Status.** Both deliverables shipped. The threat-model doc is this
file. The encryption-at-rest deliverable is `pkg/newtron/secret/`
(`Store` interface + `FileStore` backend) wired into network load via
`Config.SecretStore` and the `--secret-store=PATH` server flag.
Operator-facing CLI: `bin/newtron secrets put|get|list|delete`.
Reference syntax `${secret:KEY}` in `profiles/*.json` and
`platforms.json` values. See [`secret-store.md`](secret-store.md)
for the operational HOWTO. The shipped `FileStore` writes plaintext
to a mode-0600 file outside the version-controlled spec directory;
an age-encrypted backend (or any other operator-supplied
implementation of `Store`) plugs into the same interface without
contract changes.

### L1 — Tamper-Evident Operation Audit Log

**Goal.** Every spec/profile/device mutation produces an audit record
with caller, operation, target, timestamp, and outcome. Records are
written to the existing `pkg/newtron/audit/` log with no enforcement
decisions made — the system behaves identically except that "who did
what" is now answerable.

Identity in this layer is **opportunistically verified**, by surface:

- **Local CLI over a Unix socket listener:** `SO_PEERCRED` gives the
  connecting process's UID. Resolved to a username via `getpwuid`. This
  identity is **verified** — the OS authenticated the user at login;
  the kernel attests the UID. L1 ships this verified path for free
  because no auth dance is needed.
- **Remote CLI / newtcon over TCP:** identity is **self-attested** in
  this layer (header carried by the request). The audit log entry
  records `verification_source: "self_attested_header"` so a reviewer
  can tell which entries are trustworthy. TCP entries become
  verified only when L2b lands.

**Audit criterion met when this layer lands.** "Who deleted VLAN 100 on
switch1 at 03:42?" has a definitive answer from the log when the
operator ran the CLI against the Unix socket; the answer is "alice
claimed" rather than "alice proven" when the operator ran the CLI
against TCP. Either way the log distinguishes the two cases via
`verification_source`. The log is append-only and tamper-evident at the
file-system level (hash chain is L6).

**Dependencies.** L0.

**Scope of changes.** Unix socket listener support in
`pkg/httputil/server.go` (binds alongside TCP; cmd flags
`--unix-socket /path`). `SO_PEERCRED` extraction middleware in
`pkg/newtron/api/`. Caller-identity header parsing middleware as the
TCP fallback (clearly labeled self-attested). Audit-emit calls at
every existing `checkPermission` site and at every Node-level mutation
entry point. `pkg/newtron/audit/` integration to receive the events.
Server config gains `audit_log_path` and `audit_caller_header` fields.

**Independent value.** Catches insider misuse after the fact even
before authentication is wired up. Operators using the Unix socket path
already get verified identity in the log; TCP operators get a record of
their self-claim that's only useful for forensics, not for trust — but
forensics is real value.

### L2 — Transport Authentication

L2 has two independent sub-layers because they protect different
surfaces with different mechanisms. Either can ship before the other;
both are required before L3 can rely on universally-verified identity.

#### L2a — Listener-Side TLS (Operator-to-Server)

**Goal.** When external operators and external clients (newtcon, CI
runners, automation) talk to `cmd/newt-server` over TCP, the listener
authenticates them via mTLS: a peer cert verified against a configured
CA. Identity = cert CN. The cert-CN extraction infrastructure mounts at
the outer middleware so it composes with PAM and Unix peer creds
uniformly.

The three engines run in one process (`cmd/newt-server`); inter-
engine calls are in-process Go function calls — no network
boundary between engines to protect. An earlier separate-process
deployment shape (each engine in its own server binary, talking
to siblings over TCP) was retired when the engines were composed;
the standalone engine binaries today are loopback dev tools, not
a production deployment shape. L2a in the current architecture
protects the operator-to-server boundary, not inter-engine.

**Audit criterion met when this layer lands.** "Can a non-CA-trusted
client open a connection to `cmd/newt-server`?" → no. A reviewer
verifies by inspecting the CA-cert configuration on the deployment,
attempting a connection with a non-trusted cert (handshake fails),
and checking that the audit log shows
`verification_source: "service_cert_cn"` on successful
mTLS-authenticated requests.

**Dependencies.** L0, L1.

**Scope of changes.** The cert-CN extraction infrastructure
(`pkg/httputil.ServiceCertCNFromRequest`, `ServiceCertCNFromContext`,
the `VerificationServiceCertCN` audit verification source, the
priority slot ahead of PAM in `pkg/newtron/api/caller_middleware.go`)
landed first so the L3 authorization layer could treat cert CN as
a verified identity uniformly with PAM and Unix peer creds. The
listener-side wiring shipped in #175 — `--tls-cert`/`--tls-key`/
`--tls-ca` flags on `cmd/newt-server` plumb into
`httputil.LoadServerTLSConfig` and the `TLSConfig` server option;
when all three are set, every TCP connection completes a mTLS
handshake and the verified peer cert CN flows into the request
context for the identity-extraction middleware. Operators who
prefer to terminate TLS at a reverse proxy continue to do so; the
listener-side flags are an alternative, not a replacement. The
standalone engine binaries are loopback dev tools with no TLS by
design.

**Independent value.** Blocks both eavesdropping on the
operator-to-server channel and impersonation of newt-server by a
rogue process. The
cert-CN identity slot already wired into caller_middleware gives
operators an mTLS-cert-based identity path that composes with PAM
and Unix peer creds uniformly, so the typical service-mesh pattern
(operator → newt-server via mTLS with the operator's cert as
identity) becomes available without further code changes once the
flags ship.

#### L2b — User-to-Service Authentication via PAM

**Goal.** For TCP listeners, the server authenticates remote operators
against the host's PAM stack. The HTTP middleware extracts HTTP Basic
credentials, drives `pam_authenticate`, and on success populates the
request's verified identity with the resulting Unix username. On
failure: 401. Token-based session reuse (a PAM-issued short-lived key
the client carries on subsequent requests instead of re-presenting
Basic auth) is a separate concern handled in L2c.

PAM is the only identity mechanism in this layer. It already covers the
realistic deployment-side identity backends (`pam_unix` for local
accounts, `pam_ldap` / `pam_sss` for directory-integrated, `pam_krb5`
for Kerberos). Operators configure their existing PAM stack; newtron
doesn't ship a parallel identity registry.

Once L2b lands, the Unix socket path from L1 and the PAM path from L2b
yield the same shape of verified identity (a Unix username); the
authorization layer in L3 doesn't need to distinguish them.

**Audit criterion met when this layer lands.** "Are user identities on
the TCP listener verified?" → yes, via the operator's PAM stack. A
reviewer verifies by inspecting the PAM config (`/etc/pam.d/newt-server`),
confirming that the server rejects unauthenticated TCP requests, and
checking that the audit log shows
`verification_source: "pam"` with the PAM service name and the
authenticated username.

**Dependencies.** L0, L1.

**Scope of changes.** PAM bindings via `cgo` and a Go PAM helper
package (the existing community libraries are reviewable; pick one in
this layer). HTTP authentication middleware mounted at the outer layer of
`cmd/newt-server`; the standalone engine binaries carry no PAM
middleware. `--auth-pam-service=NAME` flag on `cmd/newt-server`
(default empty — empty disables PAM authentication entirely;
operators pick a service name like `newt-server` matching the
`/etc/pam.d/` file). Audit log records
`verification_source: "pam"` and the PAM service name. Operational
HOWTO on setting up `/etc/pam.d/newt-server` (start with `pam_unix`;
mention `pam_ldap`/`pam_sss`/`pam_krb5` for integrated deployments).

**Independent value.** Blocks user impersonation on TCP. Audit log
records become trustworthy for TCP operators, matching what the Unix
socket path already gives.

**Why not OIDC / SAML / federated bearer tokens here.** PAM is the
well-known Linux entry point for identity; it composes with whatever
the operator already runs. Adding a native OIDC/SAML mechanism is a
feature, not a security fix — it'd be additive surface area without
closing a threat that PAM doesn't already close. Deferred to "if a
deployment requires it"; explicitly out of scope per §2.2. Note that
*server-issued* short-lived session tokens (L2c) are not federation —
PAM remains the credential, the token just amortizes the cost of
re-verifying it on every request.

#### L2c — Server-Issued Session Keys (PAM-Backed)

**Goal.** A successful PAM authentication (L2b) mints a short-lived
opaque key the client carries on subsequent requests as
`Authorization: Bearer <key>`. The key resolves to the same verified
Unix username the original PAM auth produced; downstream identity,
authorization, and audit layers consume it identically. The key is
revocable on demand (`POST /auth/logout`) and expires automatically
after a configurable TTL.

Why this layer exists separately from L2b:

- **Cost.** `pam_authenticate` against `pam_sss` or `pam_krb5` hits a
  directory or KDC per request. A 60-call orchestration burst costs 60
  round-trips through the identity stack. The session key amortizes
  the cost of one PAM call across many requests within its TTL.
- **Browser ergonomics.** A Web UI cannot prompt for a password before
  every backend call. With L2c, the UI authenticates once at sign-in,
  caches the key in a `Secure; HttpOnly; SameSite=Strict` cookie or
  in-memory store, and presents it on every subsequent call. L2b alone
  forces either browser-prompted Basic auth on every navigation or a
  separate proxy-layer session.
- **Programmatic clients.** A long-running automation embeds a key
  obtained at start-up rather than a password embedded in env vars or
  config. Leaked-credential blast radius shrinks from "permanent
  password until rotated by hand" to "session until logout or TTL
  expiry."

**Wire shape.**

```
POST /newt-server/v1/auth/login
Authorization: Basic <base64(user:pass)>

→ 200 OK
{
  "key":        "<43-char URL-safe base64, 256 bits of entropy>",
  "expires_at": "2026-06-11T08:00:00Z",
  "user":       "alice"
}
```

```
POST /newt-server/v1/auth/logout
Authorization: Bearer <key>

→ 204 No Content
```

Every other newtron endpoint accepts either `Authorization: Basic …`
(driving L2b's per-request PAM path) or `Authorization: Bearer …`
(driving L2c's key-lookup path). A client picks one; mixing is not
required.

**Verified identity flow.** When a request carries
`Authorization: Bearer <key>` and the key is present and unexpired in
the store, the middleware attaches the stored username to the request
context tagged `VerificationSessionKey`. `callerMiddleware` picks it
up exactly the way it picks up `VerificationPAM`. L3/L4/L5 gating and
L1 audit logging behave identically — the user is the user.

**Identity forwarding through engines.** When the operator's
inbound request reaches an engine that itself makes outbound
HTTP calls to a sibling engine, the engine forwards the same
Bearer rather than minting its own. Today this matters for the
newtrun runner: every `POST /newtrun/v1/runs` carries the
operator's Bearer; `pkg/newtrun/api/runs.go` extracts the key via
`sessionkey.BearerToken` and assigns it to
`Runner.OperatorBearer`; the runner's outbound newtron client
attaches `Authorization: Bearer <key>` on every call via
`pkg/newtron/client.WithBearer`. The composed `newt-server`
outer middleware verifies the same key again on the in-process
loopback, attaches the same username to the request context, and
the newtron engine's `callerMiddleware` tags `audit.Caller`
identically. The operator's identity is the runner's identity;
the runner has no daemon-side credential of its own.

Per-scenario `as: <user>` overrides the operator's default
Bearer for every outbound newtron call that scenario makes via
the multi-user session cache (`Runner.UserSessions`) populated by
the CLI at start-run time. One scenario, one verified identity:
authorization-testing flows that need multiple identities author
one scenario per identity and connect them with `requires:`. The
scenario engine attaches `Authorization: Bearer <other-user-key>`
as a per-request header that the `bearerRoundTripper` respects,
short-circuiting the default Bearer for the scenario's calls.
This is how the 1node-vs-auth suite drives authorization-by-
identity tests (alice allowed, mallory denied) without dropping
back to self-attested headers.

**Storage model.** In-memory `map[key]→{user, expires_at}` protected
by a mutex, with a background sweeper that drops expired entries. No
disk persistence: a server restart invalidates all keys. This is
intentional — restarting `newt-server` is operator-visible and
expected to terminate sessions; persistence would introduce a
credential file on disk that has to be protected as carefully as the
secret store. The Web UI / CLI client reconnects with a fresh
`auth/login`.

**TTL and rotation.** Default TTL is 8 hours, configured via
`--session-key-ttl`. TTL is *absolute* — using a key does not extend
its lifetime. A client whose session would outlive the TTL re-logs in
to mint a new key. This is deliberate: sliding expiration means a
compromised key with steady use becomes effectively immortal; absolute
expiration bounds the worst case.

**Revocation paths.**

- **Voluntary.** `POST /auth/logout` removes the key from the store.
  204 even if the key is already absent (idempotent).
- **TTL expiry.** Background sweeper runs every minute; expired keys
  are dropped. Any in-flight request with an expired key gets 401 on
  the next lookup.
- **Operator-driven user revocation.** Removing a user from
  `network.json`'s `user_groups` (L6 spec-watch) revokes the user's
  *authorization* on the next reload — their cached session key still
  resolves to a valid identity, but every gated request 403s because
  the grant table no longer mentions them. To revoke the *key* too,
  the operator restarts `newt-server` (drops the in-memory store) or
  removes the user from the OS identity backend (PAM-stack mutation
  doesn't auto-invalidate already-issued keys, since L2c never calls
  back into PAM after issuance — accepted limitation; operators
  needing tighter binding can shorten `--session-key-ttl`).

**Audit semantics.** Three new event shapes:

- `auth.login` success — `verification_source: "pam"` (the
  authentication that ran was PAM), user populated, operation
  `auth.login`, `Success: true`. The L1 audit log records the moment
  a session began.
- `auth.login` failure — `verification_source: "unknown"`, user
  populated with the attempted username, `Success: false`, error
  populated. Reviewers can detect credential-stuffing.
- Subsequent gated requests under a session key —
  `verification_source: "session_key"`, user populated from the store
  entry. Reviewers can join `auth.login` to subsequent operations by
  matching the user + a time window bounded by `expires_at`.
- `auth.logout` — `verification_source: "session_key"`, operation
  `auth.logout`, `Success: true`. End of session marker.

The L6 hash-chain integrity check covers all of these uniformly;
session events are not special-cased.

**Audit criterion met when this layer lands.** "Can a reviewer match
every audited operation under a session key to a prior PAM
authentication?" → yes. The `auth.login` event precedes every
`session_key`-verified operation under the same user, bounded in time
by the configured TTL.

**Dependencies.** L0, L1, L2b. Without L2b there is no live
authenticator to call from `/auth/login`; the routes are mounted
unconditionally but their handlers return 404 when L2c is disabled
(no `--auth-pam-service` configured, or `--session-key-ttl` set
negative). The 404 distinguishes "L2c disabled" from "wrong URL"
without requiring a server restart's worth of route-table
reshuffling on an enable/disable flip.

**Scope of changes.** Transport-level package
`pkg/httputil/sessionkey/` owns the in-memory store (`store.go`),
the Bearer-recognition middleware + identity context-key
(`middleware.go`), and the `/auth/login` + `/auth/logout` HTTP
handlers (`handlers.go`). The package lives under `pkg/httputil/`
rather than under any engine because authentication is a property
of the server boundary, not of any individual engine.

Composition site: `cmd/newt-server` mounts the Store, the
Bearer/PAM middleware chain, and the routes
`POST /newt-server/v1/auth/login` + `/auth/logout` at the outer
layer wrapping every engine mux. The standalone server binaries
(`cmd/newtron-server`, `cmd/newtrun-server`, `cmd/newtlab-server`)
have no identity middleware — they are loopback dev tools with no
encryption and no authentication; use `cmd/newt-server` for any
deployment that needs L2b/L2c. Engines see the verified username
through `sessionkey.UsernameFromContext` (L2c) or
`httputil.PAMUsernameFromContext` (L2b) on the request context;
`pkg/newtron/api/caller_middleware.go` reads either source the
same way and populates `audit.Caller` with the correct
verification source.

`httputil.SkipBasicAuthFromContext` / `WithSkipBasicAuth` is the
cross-package signal between the Bearer middleware and the PAM
middleware — kept generic so the same hook can be reused for any
future server-issued credential without `httputil` learning
newtron-specific concepts. New audit verification source
`VerificationSessionKey = "session_key"`. Flags
`--auth-pam-service` (L2b) and `--session-key-ttl` (L2c, default
`8h`) live on `cmd/newt-server`. Operational howto entry in
`docs/newtron/pam-howto.md` covering the login/logout flow.

Identity-forwarding surface: `sessionkey.BearerToken(authHeader)
(token, ok)` is the single parser of the wire shape — used by
`sessionkey.Middleware` on the inbound side and by
`pkg/newtrun/api/runs.go:operatorBearer` to populate
`pkg/newtrun.Runner.OperatorBearer` on the outbound side.
`pkg/newtron/client.WithBearer(key)` is the outbound attach
point; the underlying `bearerRoundTripper` respects any
caller-set Authorization header so per-scenario `as: <user>`
overrides compose cleanly with the default-Bearer layer.

**Independent value.** Cuts per-request PAM cost from "directory hit
every call" to "directory hit per session." Unblocks browser clients
(Web UI) without introducing a separate proxy-layer session
mechanism. Programmatic automation gets revocable credentials with a
bounded lifetime instead of embedded passwords.

**Why this isn't OIDC/SAML.** The token is opaque, server-side, and
not a federation primitive. There is no claim signing, no JWKS, no
inter-domain trust. It's a cache of "PAM said this user is real, N
minutes ago" — the same primitive as a Linux PAM session, just
exposed through HTTP.

**Limitations accepted at this layer.**

- *One auth scope across all engines.* The store, middleware, and
  handlers live at `cmd/newt-server`'s outer boundary
  (`POST /newt-server/v1/auth/login` + `/auth/logout`) and wrap
  every engine mux uniformly. A single key minted there is valid
  against every engine's routes — `/newtron/v1/*`, `/newtrun/v1/*`,
  `/newtlab/v1/*` — in the same process. The token has no scope
  narrower than "this server"; per-engine restrictions are
  authorization concerns (L3), not authentication.
- *No refresh tokens.* Sliding-expiry refresh logic would re-introduce
  the immortal-token problem `--session-key-ttl` was set up to bound.
  Operators needing longer sessions raise the TTL; operators needing
  shorter sessions lower it.
- *No PAM-side liveness check after issuance.* Once minted, a key is
  good until logout or TTL. Disabling a user in the directory does
  not retroactively invalidate their key — only restart or TTL does.
  Accepted; documented in the howto under "tightening revocation."

### L3 — Authorization Enforcement (Wire Up the Existing Checker)

**Goal.** `Network.EnableAuthorization` is invoked at every
`RegisterNetwork` / `ReloadNetwork` when `--enforce-authorization`
is set. The verified identity established by the previous layers —
Unix username (from L1 Unix socket or L2b PAM) or service cert CN
(from L2a) — flows through `auth.Context.Caller` (new field) into
the existing `Checker.Check`. The 26 existing `checkPermission`
call sites become live gates. Denied operations return HTTP 403
with a `*newtron.AuthorizationError` carrying typed
`caller`/`permission`/`resource` on the response `data` field
(§46). Every decision (allow or deny) is appended to the audit log
via `audit.LogDecision` with the verification source from L1/L2 and
Operation prefixed `authcheck:`.

Service-account identities (cert CNs when listener-side TLS lands;
operator-supplied service-account names today) and human-user
identities (Unix usernames) share the same `Context.Caller` field
but typically map to different `network.json` grants: service
accounts map to `super_users` so they can do anything they're
called upon to do; human users map to specific permission grants.
This is a spec convention, not a code distinction — the Checker
treats both identically. The three engines run in one process under
`cmd/newt-server` and call each other in-process, so service-account
identities are not used for inter-engine authentication — they exist
for external automation that talks to `cmd/newt-server` as a service.

**Audit criterion met when this layer lands.** "Are the permission
grants in `network.json` enforced at runtime?" → yes, for the
operations already gated (`spec.author`, `qos.create`,
`filter.create` and their delete counterparts). The test
`TestAuthorizationActuallyEnforces` in
`pkg/newtron/api/authorization_test.go` enables enforcement and
asserts a non-permitted caller gets 403 on each gated endpoint, plus
that a permitted caller succeeds.

**Dependencies.** L0, L1, L2.

**Scope of changes.** `auth.Context.Caller` field. Public method
`Network.EnableAuthorization` that constructs an `auth.Checker`
bound to the live `NetworkSpecFile`. `api.Config.EnforceAuthorization`
+ `--enforce-authorization` flag on `cmd/newtron-server` and
`cmd/newt-server`. `Checker.Check` reads `ctx.Caller` (the
`currentUser` field was removed entirely per §40). `ctx
context.Context` plumbed through the 25 public `Network`
spec/profile mutation methods so the verified caller travels from
`audit.CallerFromContext(ctx)` into the auth check. New typed error
`*newtron.AuthorizationError` translated to 403 at the wire by
`httpStatusFromError` and surfaced as `Data` by `writeError`. New
`audit.LogDecision` helper. New test file
`pkg/newtron/api/authorization_test.go`. New operational HOWTO
`docs/newtron/authorization-howto.md`.

**Independent value.** Spec-authoring grants are real. The
spec-mutation call sites stop lying. Node and Interface operations
land in L4 (the next layer); the L3 coverage is honest about its
scope.

### L4 — Coverage Closure (Every Mutation Gated)

**Goal.** Every mutation method on `Network`, `Node`, and `Interface`
has a `checkPermission` call before any state change. The
`TestAPICompleteness` test from PR #127 grows a new dimension:
"every mutation method must appear in `authorizedMethods` with a
named permission, OR in `unauthorizedExcept` with a documented reason
(e.g., `RestartService` is operational, not authorization-gated)."

Node-level write operations gate on the verb-family permissions
already defined for the L3 constants (`vlan.create`, `lag.delete`,
etc.) — what were "15 unused constants" before L4
became the spec vocabulary for the per-device verbs. L4 adds three
new constants: `acl.create` and `acl.delete` for symmetry with the
create/delete pattern (the pre-L4 family had only `acl.modify`),
and `device.write` as the catch-all for operational mutations
(`setup-device`, `init-device`, `config-reload`, `restart-service`,
`exec-command`, `save`, `reconcile`) whose verb is a device-state
operation rather than a config-table mutation.

**Audit criterion met when this layer lands.** "Can any unauthorized
caller mutate any state?" → no. A reviewer can answer by reading the
`TestAPICompleteness` rules and trusting the test that enforces them.
The reviewer doesn't have to audit every method individually.

**Dependencies.** L0, L1, L2, L3.

**Scope of changes.** New Permission constants in
`pkg/newtron/auth/permission.go`. `checkPermission` added to every
Node mutation method. `TestAPICompleteness` grows the new dimension.
HLD §9 updated.

**Independent value.** Closes the largest current gap (zero gates on
device operations). After this layer, all mutations are gated at the
verb level, even if granularity is still all-or-nothing.

### L5 — Fine-Grained Per-Resource Grants

**Goal.** `network.json permissions` map values become richer:

```json
{
  "permissions": {
    "device.write": [
      { "groups": ["edge-operators"], "where": { "device": "edge-*" } },
      { "groups": ["spine-operators"], "where": { "device": "spine-*" } }
    ],
    "service.apply": [
      { "groups": ["operators"], "where": { "service": "transit-*" } }
    ]
  }
}
```

The handler middleware populates `Context.Device`, `Context.Service`,
`Context.Interface` from the URL path. `Checker.Check` evaluates the
`where` clauses against the populated context. Globs supported on
specific dimensions; explicit lists for others.

The old shorthand syntax (`"action": ["groups"]`) continues to work —
it's syntactic sugar for `[{ "groups": [...], "where": {} }]` — so
existing specs keep working. This is the only place a "compat shim"
exists in the auth subsystem; it's load-bearing because the shorthand
is the obviously-right form for "no constraints needed."

#### Granularity of Rule-Modification Gates

Four spec-mutation surfaces operate on rules inside a container
object: filter rules, prefix-list entries, route-policy rules, and
QoS queues. Their gates stamp `Resource = <container name>`, not
`Resource = <rule identifier>`:

| Operation | Permission | `Resource` stamped |
|---|---|---|
| `AddFilterRule` / `RemoveFilterRule` | `spec.author` | filter name |
| `AddPrefixListEntry` / `RemovePrefixListEntry` | `spec.author` | prefix-list name |
| `AddRoutePolicyRule` / `RemoveRoutePolicyRule` | `spec.author` | route-policy name |
| `AddQoSQueue` / `RemoveQoSQueue` | `spec.author` | qos-policy name |

A `where: {resource: "<container>"}` clause scopes the entire
container — "alice can edit any rule in filter-A" is expressible;
"alice can edit only rule index 5 of filter-A" is not.

**Why this is the right granularity.** Rule indices are unstable —
inserting a rule at any position shifts every subsequent index.
A grant authored against index 5 would silently scope a different
rule the moment any earlier rule changes. Container-level scoping
sidesteps the instability.

**Operator alternative** for finer scoping: split the container.
Two filters that each grant to a different team are expressible;
one filter with per-rule grants is not.

#### Meta-Authorization: Who Can Grant Access

Before L5, "who can grant access to others" is implicit and coarse:

- `super_users` bypass all checks, including any check that would
  guard changes to the permissions map itself.
- `spec.author` is the only gate on spec mutation. Because
  `network.json` is a spec file, and the `permissions` /
  `user_groups` / `super_users` fields live inside it, anyone with
  `spec.author` can edit who else has access.

This collapses two distinct responsibilities — **service architect**
(writes services, profiles, topology) and **IAM operator** (decides
who's in which group, who has what permission) — into one role.
Acceptable for a small trusted team; not acceptable for a deployment
with separate security ownership.

L5's `where` clauses absorb this dimension cleanly. The spec-field
constraint is one more case in the matcher, not a new permission
constant:

```json
{
  "permissions": {
    "spec.author": [
      { "groups": ["service-architects"],
        "where": { "field": "!permissions,!user_groups,!super_users" } },
      { "groups": ["iam-team"],
        "where": { "field": "permissions,user_groups" } }
    ]
  }
}
```

`super_users` continues to bypass everything — by design — and is
itself protected by the same `where: { field: "super_users" }`
clause. The bootstrap path remains "edit `network.json` on disk and
restart"; once running, only configured grantees can change the
grant table.

The L5 implementation ships these dimensions in `auth.Context`:
`Device`, `Service`, `Interface`, `Resource`, and `Field` (the
meta-authorization dimension). Spec/profile/topology mutation
methods populate `Field` with the top-level area being mutated
(`"services"`, `"profiles"`, `"topology"`, etc.). Node and
Interface mutation gates populate `Device` and `Interface` from
the URL path.

**Audit criterion met when this layer lands.** "Can alice modify
VLANs only on edge switches?" → yes, expressible in `network.json`,
enforced at runtime, audited on every decision. "Can bob manage who
has access without also being able to edit service specs?" → yes,
via the `field` dimension on `spec.author`.

**Dependencies.** L0–L4.

**Scope of changes.** Spec type for permission entries (struct or
union for old/new form). Loader migration handling. Checker evaluation
of `where` clauses, including the `field` dimension for meta-auth.
Handler-side population of Context dimensions (handlers know device
from URL path; spec-mutation handlers know the field path being
written). Audit log records the populated context dimensions in every
decision entry.

**Independent value.** Real role separation along two axes:
operational scope (which devices/services) and authority scope (who
can grant). Least-privilege is expressible without forking spec
files per role.

### L6 — Operational Hardening (Revocation + Audit Log Integrity)

**Goal.** Two operational properties that production deployments
require but development doesn't. (Secret hygiene moved to L0 — it's
foundational, not operational.)

1. **Revocation.** Removing a grant from `network.json` takes effect
   within a bounded interval without server restart. The existing
   `ReloadNetwork` HTTP endpoint already supports this; the L6
   spec-file watcher triggers it automatically on file change, plus
   a documented operational pattern for "revoke alice."
2. **Audit log integrity.** L1's audit log is append-only at the
   application level but a determined attacker with file write access
   could rewrite it. L6 adds optional hash-chain log integrity: each
   record carries a hash of the previous, so tampering is detectable
   after-the-fact even if the file is mutable.

Secret rotation falls out for free once L0 encryption ships: rotating a
device password becomes "change the secret store entry"; specs don't
need to change.

**Audit criterion met when this layer lands.** "How do I revoke
alice's access?" → documented procedure with a bounded time-to-effect.
"Can audit log entries be tampered with undetectably?" → no, with
integrity enabled.

**Dependencies.** L0–L5.

**Scope of changes.** Spec file watcher in `pkg/newtron/network/`
(`SpecWatcher`, fsnotify-backed, 1s debounce). Hash chain in
`pkg/newtron/audit/` (`Event.PrevHash`, `Event.ID = SHA256(prev_hash
|| canonical_json)`, `Verify` walks the file and reports the first
broken position). `--spec-watch` and `--audit-log-integrity` flags
on `cmd/newtron-server` and `cmd/newt-server`. CLI verifier:
`bin/newtron audit verify <path>`. Both halves default off per
§2.4. New HOWTO sections on the daily revoke flow and the
verification workflow.

**Independent value.** The system can be operated by a real team
day-to-day without "wait for the next deploy window to revoke that
person."

---

## 6. Current State (What's Actually There Today)

Per editing-guidelines §11 ("Document What Is, Not What's Intended"):

- The auth code in `pkg/newtron/auth/` and the 26 `checkPermission`
  call sites are wired live as of L3. Every spec/profile mutation
  method (`CreateService`, `DeleteProfile`, …) accepts `ctx
  context.Context` as its first parameter; `checkPermission` reads
  the verified caller from `audit.CallerFromContext(ctx)`, populates
  `auth.Context.Caller`, and delegates to the Checker. Denials
  surface as `*newtron.AuthorizationError` → HTTP 403, with the
  typed `Caller`/`Permission`/`Resource` shape on the response
  `Data` field (§46).
- Enforcement is gated by `--enforce-authorization` (off by
  default) per §2.4. Off → `Network.EnableAuthorization` is never
  called → `net.auth` stays nil → every `checkPermission` is a
  no-op (pre-L3 behavior). On → `EnableAuthorization` is invoked
  for each registered network at `RegisterNetwork` and
  `ReloadNetwork` time, binding the Checker to the live
  `NetworkSpecFile` so subsequent in-process mutations
  (`CreateService`, etc.) take effect against the same grant table
  the Checker sees.
- **L1 audit log is shipping.** The identity-extraction and audit-
  emission middlewares (`pkg/newtron/api/caller_middleware.go`,
  `pkg/newtron/api/audit_middleware.go`) live inside the newtron
  engine's handler chain, which runs both under `cmd/newt-server`
  (production / aggregated deployment) and `cmd/newtron-server`
  (standalone dev tool). In standalone mode the only identity
  surface is the L1 self-attested `--audit-caller-header` (and
  Unix peer creds when `--unix-socket` is set); standalone
  newtron-server has no PAM or session-key middleware (those live
  at `cmd/newt-server`'s outer boundary). Behavior is toggled by
  `--audit-log`, `--audit-caller-header`, and `--unix-socket` flags
  per §2.4; default values (all empty) preserve the pre-L1 behavior.
  With `--audit-log` set, every POST/PUT/DELETE produces one Event
  with caller, method+URL as Operation, success/error from response
  status, and a duration. **L3 adds per-`checkPermission` decision
  events** via `audit.LogDecision` from inside
  `Network.checkPermission`; the Event's Operation is
  `authcheck:<permission>` so reviewers can filter for authorization
  decisions, and the verification source (from L1/L2) is recorded
  alongside the caller.
- Every shipped `network.json` has `super_users: null`,
  `user_groups: null`, `permissions: null`. Operators author grants
  per their deployment; the shipped fixtures don't ship grants.
- The Permission constants now have 22 references across spec, profile,
  topology, Node, and Interface mutations. L4 added `PermACLCreate`,
  `PermACLDelete`, and `PermDeviceWrite` for symmetry and to cover
  operational mutations that don't fit a create/modify/delete verb on
  a domain noun.
- The URL-derivable Context dimensions (`Device`, `Interface`) are
  populated by the `gate` helpers on Node and Interface and read by
  L5's `where` clauses. `Service` is populated by
  Interface.ApplyService / RemoveService. `Field` (added in L5)
  is populated by spec/profile/topology mutation methods with the
  top-level area being mutated.

Both L0 deliverables are shipped (this doc + the secret store).
L1 audit log is shipped. **L2a listener-side TLS is shipped** —
the cert-CN extraction infrastructure, the audit verification
source, and the listener-side flags
(`--tls-cert`/`--tls-key`/`--tls-ca` on `cmd/newt-server`) are
all wired. Deployments that prefer reverse-proxy termination
continue to do so; in-process inter-engine calls in the composed
binary have no separate TLS surface. L2b
user-to-service PAM is shipped — `--auth-pam-service=NAME` flag on
`cmd/newt-server` (the only binary with PAM); the standalone
engine binaries are loopback dev tools with no authentication. The
`PAMMiddleware` in `pkg/httputil` enforces HTTP Basic +
`pam_authenticate` on TCP requests that don't already carry a verified
identity (Unix peer creds from L1, mTLS cert CN from L2a), mounted at
the outer layer of `cmd/newt-server`. The cgo-backed `PAMAuthenticator`
lives in `pkg/httputil/pamauth` (separate package so non-PAM consumers
don't pull in cgo). The newtron caller middleware reads
`PAMUsernameFromContext` and tags `audit.Caller` with
`VerificationPAM`. Operational doc: [`pam-howto.md`](pam-howto.md).

**L3 authorization enforcement is shipped.** The `--enforce-
authorization` flag on `newtron-server` and `newt-server`
engages `Network.EnableAuthorization` at every `RegisterNetwork`
and `ReloadNetwork`. Per-decision audit events
(`Operation: "authcheck:<permission>"`) join the L1 request-level
events in the audit log when both `--enforce-authorization` and
`--audit-log` are set. Denials surface as HTTP 403 with the typed
`AuthorizationError` payload on the response `Data` field
(`caller`, `permission`, `resource`). Test:
`TestAuthorizationActuallyEnforces` in
`pkg/newtron/api/authorization_test.go`.

**L4 coverage closure is shipped.** Every public mutation method
on `*Network`, `*Node`, and `*Interface` now calls a gate before
any state-touching code. Network methods call `checkPermission`
directly; Node methods call `(*Node).gate(ctx, perm, resource)`;
Interface methods call `(*Interface).gate(ctx, perm, resource)`.
Both gate helpers populate `auth.Context.Device` (and `Interface`
on the interface helper) so L5 can later read them in `where`
clauses. New constants: `PermACLCreate`, `PermACLDelete`,
`PermDeviceWrite` (the operational catch-all). The five topology-
mutation methods (`AddTopologyDevice`, `DeleteTopologyDevice`,
`UpdateTopologyDevice`, `AddTopologyLink`, `DeleteTopologyLink`)
were L3 gaps that L4 closes — they live in `network.go` not
`spec_ops.go`, so L3's spec-ops sweep missed them. `TestAPI-
Completeness` grows a new dimension: every HTTP-exposed method
must appear in either `authorizedMethods` (with a Permission
constant) or `readOnlyMethods` (with a documented reason); the
compiler enforces the permission constant exists, and the test
enforces classification. Tests:
`TestAuthorizationL4_NodeMutationsGated` and
`TestAuthorizationL4_InterfaceMutationsGated`.

The shipped topology specs in `newtrun/topologies/` continue to
carry plaintext passwords — those 58 instances are operator-
migration work, not server work; the operator's workflow is
documented in [`secret-store.md`](secret-store.md). Until those
plaintexts get migrated to references, `grep -r ssh_pass
newtrun/topologies/` still returns plaintexts; the L0 audit
criterion ("no plaintext in spec dir") is met for any *operator-
configured* deployment but not for the in-tree test fixtures.

**L5 fine-grained per-resource grants is shipped.** The
`network.json permissions` map values accept both the legacy flat
shorthand (`["group1", "group2"]`) and the new typed form
(`[{"groups": [...], "where": {...}}]`) on the wire — a custom
`spec.PermissionGrants` `UnmarshalJSON` discriminates by peeking
at the first array element. The shorthand collapses to a single
`PermissionGrant` with an empty `Where`, which `Checker` then
treats as "match every Context" (the legacy behavior preserved as
syntactic sugar).

`Checker.Check` walks the grants in declaration order; first match
wins. A grant matches when (1) the caller is in one of `grant.Groups`
and (2) the `grant.Where` clause is satisfied by the populated
`auth.Context`. `where` dimensions are `device`, `service`,
`interface`, `resource`, `field` — unknown dimensions fail closed.

The pattern matcher (`pkg/newtron/auth/where_match.go`) handles
exact, glob (`edge-*`), comma-OR (`edge-1,edge-2`), bang-prefix
exclusion (`!permissions`), and combinations. Audit decision
events grow `Resource` and `Field` fields recording the full
context dimensions a reviewer needs to reconstruct each match.

Meta-authorization separation is now expressible. The §3 criterion 9
"can bob manage who has access without also editing service
specs" test scenario lives in
`TestAuthorizationL5_MetaAuthorizationField`. The §3 criterion 3
"can alice modify devices only in her rack" scenario lives in
`TestAuthorizationL5_PerDeviceScoping`. Tests:
`pkg/newtron/spec/permission_grant_test.go`,
`pkg/newtron/auth/where_match_test.go`,
`pkg/newtron/api/authorization_test.go` (`TestAuthorizationL5_*`).

**L6 operational hardening is shipped.** The auth arc is complete.

Revocation half: `--spec-watch=true` engages an fsnotify-backed
watcher on every registered network's spec directory
(`pkg/newtron/network/watcher.go`). On settled change (1s
debounce; configurable via `NewSpecWatcher`), the watcher invokes
`Server.ReloadNetwork(id)` which re-binds the auth checker to the
new grant table. Removing alice from a group in `network.json`
then takes effect within the debounce window without any explicit
`/reload` call.

Audit log integrity half: `--audit-log-integrity=true` switches the
FileLogger to a hash-chained mode. Each emitted event is populated
with `PrevHash` (the previous entry's `ID`) and `ID` (computed as
`SHA256(prev_hash || canonical_json_of_event_with_zero_id)`).
Tampering with any past entry breaks the chain at that point and
every subsequent link. Operators run `bin/newtron audit verify
<path>` periodically (cron or post-incident); exit code 1 + line
number on stderr signals tampering, exit 0 verifies clean. The
chain head is recovered from the file's last well-formed entry on
startup, so the chain continues across server restarts. Pre-L6
entries (with empty `ID`) are skipped during verification so a log
that pre-dates the upgrade still parses.

Both halves default off per §2.4. With `--spec-watch=false` the
operator continues to POST `/reload`; with
`--audit-log-integrity=false` the FileLogger writes entries with
empty `ID` exactly as before L6. Tests:
`pkg/newtron/network/watcher_test.go` (3 tests),
`pkg/newtron/audit/integrity_test.go` (5 tests).

The L0 plaintext-password migration remains as the only out-of-arc
operator-side cleanup: `grep -r ssh_pass newtrun/topologies/`
still returns the in-tree test fixtures' plaintexts because the
operator workflow to migrate them lives outside the server code
path. See [`secret-store.md`](secret-store.md).

---

## 7. Cross-References

- HLD §9 (Security) is the operator-facing summary; it points here
  for the open work and the layered plan.
- `pkg/newtron/auth/permission.go`, `checker.go` — the code that
  embodies the entitlement pattern this doc keeps as the goal.
- `network.json` schema in `pkg/newtron/spec/types.go`:
  `NetworkSpecFile.{SuperUsers,UserGroups,Permissions}` — the fields
  that drive the Checker. (Per-service scoping uses L5
  `where: {service: "..."}` clauses on global grants; the embedded
  `ServiceSpec.Permissions` field was retired in #165.)
- `pkg/newtron/audit/` — the audit log target L1 wires up.
- DESIGN_PRINCIPLES_NEWTRON §33 (Public API Boundary) — the layered
  changes keep `pkg/newtron/auth/` as a public package; internal
  types live in `pkg/newtron/network/node/` and don't cross into
  auth decisions.
