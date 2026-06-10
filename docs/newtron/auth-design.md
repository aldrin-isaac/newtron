# Auth Design — Layered Path to Production-Grade

## 1. Purpose

newtron's existing authorization code commits to an **entitlement pattern**
(spec-declared permissions, group-based grants, service-level overrides,
superuser bypass) but the runtime is inert: `SetAuth` is never called, so
every `checkPermission` short-circuits to "allowed." This doc charts the
path from that starting point to a production-grade auth subsystem where
the entitlement pattern is the design destination, **not** the design
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

The threat surface splits into two layers — **inter-service** (one
newtron component talking to another over the network) and
**user-to-service** (a human operator's CLI or browser talking to a
service). Both yield a verified caller identity that L3+ authorize
against, but the verification mechanism differs.

| Threat | Surface | Layer that addresses it |
|---|---|---|
| **Insider misuse — accidental.** A teammate runs the wrong CLI against the wrong network and changes config they shouldn't have. | User-to-service | L1 (audit log catches), L3 (authorization gates) |
| **Insider misuse — deliberate.** A current team member with shell access tries to modify resources outside their role. | User-to-service | L3 + L5 (per-resource grants) |
| **Forensic accountability.** "Who deleted that VLAN at 03:42?" is answerable from the system itself. | User-to-service | L1 |
| **User impersonation across the wire.** Someone on the network sends requests claiming to be alice. | User-to-service | L2b (PAM for TCP; Unix peer creds for local) |
| **Service impersonation.** A rogue process pretends to be newtlab-server (or another newtron component) and serves bogus responses, or accepts requests from one engine claiming to be another. | Inter-service | L2a (mTLS) |
| **Stale grants.** Former team member's permissions linger after they leave. | User-to-service | L6 (revocation) |
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

- L3 (authorization) assumes L2 (verified identity for users — L2b — and
  for services — L2a). Without verified identity, authorization decides
  on a self-attested name — same security posture as no authorization.
- L5 (fine-grained grants) assumes L4 (universal coverage). Per-resource
  grants on some operations and no grants on others creates an
  exploitable asymmetry.
- L6 (revocation) assumes L3 (something to revoke). Spec reload without
  enforcement does nothing.

L2a (inter-service mTLS) and L2b (user-to-service PAM) are
**independently shippable** because they protect different surfaces. A
deployment that runs only the composed `newt-server` binary (all engines
in one process; no inter-service network calls) can ship L2b without
L2a. A deployment running the three engines in separate processes needs
both. The combined sub-layer L2 is treated as one in subsequent layers'
dependency lists, but in scheduling terms it splits in half.

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
- **L2a inter-service mTLS:** `--tls-cert/--tls-key/--client-ca`
  enable; absent means plaintext HTTP between engines.
- **L2b user-to-service PAM:** `--auth-pam-service=NAME` enables;
  absent means TCP listener doesn't authenticate via PAM (caller
  identity stays self-attested via header per L1).
- **L3 authorization enforcement:** `--enforce-authorization=true`
  enables `SetAuth`; default `false` means `checkPermission` stays a
  no-op even though identity from L1/L2 is populated.
- **L4 coverage checks:** controlled by the same
  `--enforce-authorization` toggle as L3 — once L4 lands, every
  mutation has a check, but checks are bypassed uniformly when
  enforcement is off.
- **L5 fine-grained grants:** dictated by spec format. Old
  shorthand keeps working (it's syntactic sugar for the richer
  form); operators opt into per-resource grants by writing them.
- **L6 revocation + log integrity:** `--watch-spec=true` enables
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

1. **Verified identity, per surface.** User-to-service requests carry a
   caller identity verified via Unix socket peer creds (local) or PAM
   authentication (TCP). Inter-service requests carry an identity
   verified via mTLS (cert CN). Neither surface accepts self-attested
   headers as authoritative.
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
| `pkg/newtron/auth/Context` (resource context) | 4 dimensions, only `Resource` populated | L3 adds `Caller` (Unix username from L1 Unix socket or L2b PAM; cert CN from L2a for inter-service); L5 populates `Device`/`Service`/`Interface` |
| `pkg/newtron/auth/Checker` (decision engine) | Two-tier eval, group fallback, superuser bypass | L5 extends to evaluate dimension constraints |
| `network.json` `permissions` map | `action → [groups]` global + per-service override | L5 extends entry value to support `{ groups, where: {...} }` |
| `network.json` `super_users` | List of usernames who bypass | Unchanged |
| `network.json` `user_groups` | Group name → user list | Unchanged |
| Service-level `ServiceSpec.Permissions` | Same shape as global, overrides global | Unchanged |
| `Network.checkPermission` call sites | 26 sites in `spec_ops`/`profile_ops` | L4 expands coverage to Node ops |

What goes away (during L1–L3):

- The misleading "Permission-based access control" claim in `cmd/newtron/main.go` package comment.
- The `Network.SetAuth` method being callable but never called (L3 wires it).
- The `WithDevice` / `WithService` / `WithInterface` setters being unused (L5 populates them).
- The 15 Permission constants that no proposed coverage uses (L4 prunes them when they're confirmed unused under the new coverage rules).

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
- **Inter-service calls (engine → engine) over TCP:** identity is
  similarly self-attested in this layer. Becomes verified when L2a
  lands.

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

#### L2a — Inter-Service mTLS

**Goal.** When newtron components talk to each other across processes
(newtron-server → newtlab-server, newtrun-server → newtron-server,
etc.), both ends present X.509 certificates and verify each other against
a configured CA. Identity = cert CN. Self-attested headers between
services are rejected when mTLS is configured.

This applies only when engines are deployed in **separate processes**.
The composed `newt-server` binary mounts all three engines on one mux in
one process; inter-service calls there are in-process Go function calls
and don't traverse a network boundary. L2a is no-op for the composed
deployment.

**Audit criterion met when this layer lands.** "Can a rogue process
impersonate newtlab-server to newtron-server?" → no. A reviewer verifies
by inspecting the CA-cert configuration, confirming that all three
engine binaries refuse plaintext or untrusted-cert connections, and
checking that the audit log shows `verification_source: "service_cert_cn"`
for cross-engine calls.

**Dependencies.** L0, L1.

**Scope of changes.** TLS listener configuration in
`cmd/newtron-server`, `cmd/newtrun-server`, `cmd/newtlab-server`. Client-
side mTLS support in `pkg/newtron/client`, `pkg/newtrun/client`,
`pkg/newtlab/client`. Configuration flags for CA cert path, server cert,
server key. Cert-CN extraction middleware in each engine's `api/`
package. Audit log records `verification_source: "service_cert_cn"`.
Operational HOWTO on running a small CA.

**Independent value.** Blocks service impersonation. Cross-engine calls
become trustworthy. Even without user-side authentication (L2b), the
inter-engine substrate is sound.

#### L2b — User-to-Service Authentication via PAM

**Goal.** For TCP listeners, the server authenticates remote operators
against the host's PAM stack. The HTTP middleware extracts credentials
(HTTP Basic or a short-lived token issued by a PAM-backed login
endpoint — to be picked during this layer), drives `pam_authenticate`,
and on success populates the request's verified identity with the
resulting Unix username. On failure: 401.

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
reviewer verifies by inspecting the PAM config (`/etc/pam.d/newtron-server`),
confirming that the server rejects unauthenticated TCP requests, and
checking that the audit log shows
`verification_source: "pam"` with the PAM service name and the
authenticated username.

**Dependencies.** L0, L1.

**Scope of changes.** PAM bindings via `cgo` and a Go PAM helper
package (the existing community libraries are reviewable; pick one in
this layer). HTTP authentication middleware in `pkg/newtron/api/`,
`pkg/newtrun/api/`, `pkg/newtlab/api/`. Configuration flag for the PAM
service name (default `newtron-server`). Audit log records
`verification_source: "pam"` and the PAM service name. Operational
HOWTO on setting up `/etc/pam.d/newtron-server` (start with `pam_unix`;
mention `pam_ldap`/`pam_sss`/`pam_krb5` for integrated deployments).

**Independent value.** Blocks user impersonation on TCP. Audit log
records become trustworthy for TCP operators, matching what the Unix
socket path already gives.

**Why not OIDC / SAML / bearer tokens here.** PAM is the well-known
Linux entry point for identity; it composes with whatever the operator
already runs. Adding a native OIDC/SAML/token mechanism is a feature,
not a security fix — it'd be additive surface area without closing a
threat that PAM doesn't already close. Deferred to "if a deployment
requires it"; explicitly out of scope per §2.2.

### L3 — Authorization Enforcement (Wire Up the Existing Checker)

**Goal.** `SetAuth` is called at server bootstrap. The verified identity
established by the previous layers — Unix username (from L1 Unix
socket or L2b PAM) or service cert CN (from L2a) — flows through
`auth.Context.Caller` (new field) into the existing `Checker.Check`.
The 26 existing `checkPermission` call sites become live gates. Denied
operations return HTTP 403 with a structured error. Every decision
(allow or deny) is appended to the audit log from L1 with the
verification source from L1/L2.

Service-to-service identities (cert CNs) and user identities (Unix
usernames) share the same `Context.Caller` field but typically map to
different `network.json` grants: services map to `super_users` so they
can do anything they're called upon to do (newtlab-server reaching out
to newtron-server during a deploy needs broad authority); users map to
specific permission grants. This is a spec convention, not a code
distinction — the Checker treats both identically.

**Audit criterion met when this layer lands.** "Are the permission
grants in `network.json` enforced at runtime?" → yes, for the operations
already gated (`spec.author`, `qos.create`, `filter.create` and their
delete counterparts). A test (`TestAuthorizationActuallyEnforces`) flips
SetAuth on, asserts a non-permitted caller gets 403 on each gated
endpoint, asserts a permitted caller succeeds.

**Dependencies.** L0, L1, L2.

**Scope of changes.** `auth.Context.Caller` field. Middleware that
populates `Context` from L2's verified identity. `Network.SetAuth` is
invoked from `pkg/newtron/api/Server` construction. `Checker.Check`
reads `ctx.Caller` instead of `c.currentUser`. New typed error
`*newtron.AuthorizationError` translated to 403 at the wire. New test
file `pkg/newtron/api/authorization_test.go`.

**Independent value.** Spec-authoring grants are real. The existing
26 call sites stop lying. Node-level operations remain ungated for
now — that's L4's job — but the existing coverage is honest.

### L4 — Coverage Closure (Every Mutation Gated)

**Goal.** Every mutation method on `Network`, `Node`, and `Interface`
has a `checkPermission` call before any state change. The
`TestAPICompleteness` test from PR #127 grows a new dimension:
"every mutation method must appear in `authorizedMethods` with a
named permission, OR in `unauthorizedExcept` with a documented reason
(e.g., `RestartService` is operational, not authorization-gated)."

Node-level write operations get a new permission family:
`device.write` (broad, catches everything new by default) plus
finer-grained constants for operations where reviewers want
distinction (e.g., `device.write.dataplane`, `device.write.runtime`).
The 15 unused Permission constants get pruned where they don't fit
the new coverage rules; replacements get added where they do.

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

If meta-authority separation is needed before L5 ships, the smallest
intermediate is splitting `spec.author` into `spec.author.permissions`
and `spec.author.everything-else` — two new constants and one
refactor pass on the spec-mutation handlers. The full L5 dimension
model is strictly more expressive.

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
   `ReloadNetwork` HTTP endpoint already supports this — L6 adds a
   spec-file watcher that triggers reload automatically on file
   change, plus a documented operational pattern for "revoke alice."
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

**Scope of changes.** Spec file watcher in `pkg/newtron/network/`.
Hash chain in `pkg/newtron/audit/`. New HOWTO section on operational
procedures.

**Independent value.** The system can be operated by a real team
day-to-day without "wait for the next deploy window to revoke that
person."

---

## 6. Current State (What's Actually There Today)

Per editing-guidelines §11 ("Document What Is, Not What's Intended"):

- The auth code in `pkg/newtron/auth/` and the 26 `checkPermission`
  call sites exist and pass tests, but **enforcement is inert** —
  `Network.SetAuth` is never called from any `main()`, so `net.auth`
  is always nil, so every `checkPermission` returns nil.
- **L1 audit log is shipping in parallel with this doc state.** The
  identity-extraction and audit-emission middlewares
  (`pkg/newtron/api/caller_middleware.go`,
  `pkg/newtron/api/audit_middleware.go`) are in the chain on
  `newtron-server` and `newt-server`. Behavior is toggled by
  `--audit-log`, `--audit-caller-header`, and `--unix-socket` flags
  per §2.4; default values (all empty) preserve the pre-L1 behavior.
  With `--audit-log` set, every POST/PUT/DELETE produces one Event
  with caller (Unix peer-creds-verified, header-self-attested, or
  no-caller-attached), method+URL as Operation, success/error from
  response status, and a duration. Per-`checkPermission` granular
  decision audit is **not yet emitted** — it has no information
  content until L3 wires authorization (every check returns nil
  today), so the design-doc bullet about emitting at every
  `checkPermission` site is deferred to L3.
- Every shipped `network.json` has `super_users: null`,
  `user_groups: null`, `permissions: null`. No operator has authored
  permission grants because there is nothing to grant.
- Out of 20 Permission constants, 5 are referenced (in spec/profile
  authoring); 15 are declared but never used.
- All three URL-derivable Context dimensions (`Device`, `Service`,
  `Interface`) have unused setters; only `Resource` is ever populated.

Both L0 deliverables are shipped (this doc + the secret store).
L1 audit log is shipped. L2a inter-service mTLS is shipped. L2b
user-to-service PAM is shipped — `--auth-pam-service=NAME` flag on
each standalone engine binary; the `PAMMiddleware` in
`pkg/httputil` enforces HTTP Basic + `pam_authenticate` on TCP
requests that don't already carry a verified identity (Unix peer
creds from L1, mTLS cert CN from L2a). The cgo-backed
`PAMAuthenticator` lives in `pkg/httputil/pamauth` (separate
package so non-PAM consumers don't pull in cgo). The newtron caller
middleware reads `PAMUsernameFromContext` and tags
`audit.Caller` with `VerificationPAM`. Operational doc:
[`pam-howto.md`](pam-howto.md). The shipped topology specs in
`newtrun/topologies/` continue to carry plaintext passwords —
those 58 instances are operator-migration work, not server work;
the operator's workflow is documented in
[`secret-store.md`](secret-store.md). Until those plaintexts get
migrated to references, `grep -r ssh_pass newtrun/topologies/` still
returns plaintexts; the L0 audit criterion ("no plaintext in spec
dir") is met for any *operator-configured* deployment but not for
the in-tree test fixtures.

L3–L6 remain proposed; none has shipped.

---

## 7. Cross-References

- HLD §9 (Security) is the operator-facing summary; it points here
  for the open work and the layered plan.
- `pkg/newtron/auth/permission.go`, `checker.go` — the code that
  embodies the entitlement pattern this doc keeps as the goal.
- `network.json` schema in `pkg/newtron/spec/types.go`:
  `NetworkSpecFile.{SuperUsers,UserGroups,Permissions}` and
  `ServiceSpec.Permissions` — the fields that drive the Checker.
- `pkg/newtron/audit/` — the audit log target L1 wires up.
- DESIGN_PRINCIPLES_NEWTRON §33 (Public API Boundary) — the layered
  changes keep `pkg/newtron/auth/` as a public package; internal
  types live in `pkg/newtron/network/node/` and don't cross into
  auth decisions.
