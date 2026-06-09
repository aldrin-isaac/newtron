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

| Threat | Layer that addresses it |
|---|---|
| **Insider misuse — accidental.** A teammate runs the wrong CLI against the wrong network and changes config they shouldn't have. | L1 (audit log catches), L3 (authorization gates) |
| **Insider misuse — deliberate.** A current team member with shell access tries to modify resources outside their role. | L3 + L5 (per-resource grants) |
| **Forensic accountability.** "Who deleted that VLAN at 03:42?" is answerable from the system itself. | L1 |
| **Caller impersonation across the wire.** Someone on the network sends requests claiming to be alice. | L2 (mTLS or Unix peer creds) |
| **Stale grants.** Former team member's permissions linger after they leave. | L6 (revocation) |
| **Coverage holes.** A new mutation method ships without a `checkPermission` call and bypasses authorization silently. | L4 (coverage closure test) |
| **Secret leakage from spec.** Device profile passwords sit in plain JSON on disk and in version control. | L6 (encryption at rest) |
| **Authorization decisions without trace.** A "deny" happened but nobody can prove it. | L1 + L3 (audit emits allow + deny) |

### 2.2 Out of Scope

| Out-of-scope threat | Why | Operator mitigation |
|---|---|---|
| **Compromised CA / cert issuance pipeline.** | L2 trusts the operator's CA. Compromising the CA bypasses everything. | Operator runs the CA. Standard CA hygiene. |
| **Hostile actor with shell access on the newtron-server host.** | Auth runs in the server process; root on the host bypasses auth. | Standard host hardening. |
| **Hostile actor with read access to `network.json`.** | Permission grants and group memberships are visible to anyone who reads the spec. | File-system permissions on spec dir; private git repo. |
| **Denial of service.** Rate limits, request size limits, expensive-query throttling. | Auth doesn't address availability. | Reverse proxy or kernel limits. |
| **Side channels (timing, etc.).** | Constant-time comparison of permissions is overkill for this threat model. | Network isolation; not a multi-tenant SaaS. |
| **Supply chain.** | Go module pinning and reproducible builds are infrastructure concerns. | Standard SBOM + module pinning. |

### 2.3 Assumptions

Each layer assumes the previous layers are in place. Specifically:

- L3 (authorization) assumes L2 (verified identity). Without verified
  identity, authorization decides on a self-attested name — same security
  posture as no authorization.
- L5 (fine-grained grants) assumes L4 (universal coverage). Per-resource
  grants on some operations and no grants on others creates an exploitable
  asymmetry.
- L6 (revocation) assumes L3 (something to revoke). Spec reload without
  enforcement does nothing.

This ordering is **mandatory**, not aesthetic. The audit criteria for each
layer fail if its dependencies are skipped.

---

## 3. Goal State

Production-grade auth in newtron means **every authenticated caller can do
only what the spec explicitly grants them, and every decision is on the
record**. Concretely, the goal state has these properties — these are the
criteria a security review must be able to verify:

1. **Verified identity.** Every request carries a caller identity that the
   server verified at the transport layer (not self-attested in a header).
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
8. **Secret hygiene.** Device profile passwords are encrypted at rest;
   plaintext exists only in process memory while in use.

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
| `pkg/newtron/auth/Context` (resource context) | 4 dimensions, only `Resource` populated | L3 adds `Caller`; L5 populates `Device`/`Service`/`Interface` |
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

### L0 — Threat Model & Layered Plan (this doc)

**Goal.** Articulate threat model, goal state, layered path. Establish the
contract subsequent layers respect.

**Audit criterion met when this layer lands.** A reviewer can name the
threats the system defends against and the threats out of scope, and
trace each in-scope threat to a layer that addresses it.

**Dependencies.** None.

**Scope of changes.** This doc plus HLD §9 cross-reference.

### L1 — Tamper-Evident Operation Audit Log

**Goal.** Every spec/profile/device mutation produces an audit record with
caller (self-attested in this layer, marked as such), operation, target,
timestamp, and outcome. Records are written to the existing
`pkg/newtron/audit/` log with no enforcement decisions made — the system
behaves identically except that "who did what" is now answerable.

**Audit criterion met when this layer lands.** "Who deleted VLAN 100 on
switch1 at 03:42?" has a definitive answer from the log. The log is
append-only and tamper-evident at the file-system level. Caller identity
is honestly labeled as "self-attested via header" in the log entry — a
reviewer can tell which entries are based on verified identity (none yet)
versus the operator's self-claim (all, for now).

**Dependencies.** L0.

**Scope of changes.** Caller-identity header parsing middleware in
`pkg/newtron/api/` (no decision, just propagation). Audit-emit calls at
every existing `checkPermission` site and at every Node-level mutation
entry point. `pkg/newtron/audit/` integration to receive the events.
`network.json` gains optional `audit_log_path` and `audit_caller_header`
fields.

**Independent value.** Catches insider misuse after the fact. Operators
can answer accountability questions today, even before authentication
exists.

### L2 — Transport Authentication (mTLS + Unix Socket Peer Creds)

**Goal.** Server only accepts connections where the caller's identity is
verified at the transport. Two mechanisms cover all deployment topologies:

- **mTLS** (for remote callers): server config points to a CA cert; client
  must present a valid cert; identity = the CN.
- **Unix socket peer credentials** (for local callers): server can bind to
  a Unix socket in addition to TCP; identity = the OS user that owns the
  connecting process, verified via `SO_PEERCRED`.

The header-based identity from L1 remains supported but is **demoted**
when L2 is configured: the audit log now records "verified-identity from
cert CN" or "verified-identity from peer creds" alongside any
self-attested header value. Mismatches are logged.

**Audit criterion met when this layer lands.** "Are caller identities
verified?" → yes. A reviewer can verify by looking at the audit log:
verified-identity entries have a verification source field (`cert_cn`,
`peer_uid`); self-attested entries are clearly distinguished.

**Dependencies.** L0, L1.

**Scope of changes.** TLS listener configuration in
`cmd/newtron-server` and `cmd/newt-server`. Unix socket listener
support in `pkg/httputil/server.go`. Identity extraction middleware in
`pkg/newtron/api/`. Audit log fields gain `verification_source`.
Documentation in `docs/newtron/hld.md` and a new operational HOWTO
section.

**Independent value.** Blocks impersonation on the wire. Audit log
records become trustworthy. Even without authorization enforcement
(L3), real identity is logged.

### L3 — Authorization Enforcement (Wire Up the Existing Checker)

**Goal.** `SetAuth` is called at server bootstrap. Verified identity from
L2 flows through `auth.Context.Caller` (new field) into the existing
`Checker.Check`. The 26 existing `checkPermission` call sites become
live gates. Denied operations return HTTP 403 with a structured error.
Every decision (allow or deny) is appended to the audit log from L1
with the verification source from L2.

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

**Audit criterion met when this layer lands.** "Can alice modify
VLANs only on edge switches?" → yes, expressible in `network.json`,
enforced at runtime, audited on every decision.

**Dependencies.** L0–L4.

**Scope of changes.** Spec type for permission entries (struct or
union for old/new form). Loader migration handling. Checker evaluation
of `where` clauses. Handler-side population of Context dimensions
(handlers know device from URL path). Audit log records the populated
context dimensions in every decision entry.

**Independent value.** Real role separation. Least-privilege is
expressible without forking spec files per role.

### L6 — Operational Hardening (Revocation, Rotation, Secret Hygiene)

**Goal.** Three operational properties that production deployments
require but development doesn't:

1. **Revocation.** Removing a grant from `network.json` takes effect
   within a bounded interval without server restart. The existing
   `ReloadNetwork` HTTP endpoint already supports this — L6 adds a
   spec-file watcher that triggers reload automatically on file
   change, plus a documented operational pattern for "revoke alice."
2. **Secret rotation.** Device profile passwords currently live in
   `profiles/*.json` in plaintext. L6 introduces an encrypted-secrets
   path: passwords reference a key in a separate secret store
   (file-based KMS, age-encrypted, or operator-supplied), decrypted
   on demand in process memory only. Rotation is changing the secret
   store entry; specs don't need to change.
3. **Audit log integrity.** L1's audit log is append-only at the
   application level but a determined attacker with file write access
   could rewrite it. L6 adds optional hash-chain log integrity: each
   record carries a hash of the previous, so tampering is detectable
   after-the-fact even if the file is mutable.

**Audit criterion met when this layer lands.** "How do I revoke
alice's access?" → documented procedure with a bounded time-to-effect.
"Where are device passwords stored?" → not in version-controlled spec.
"Can audit log entries be tampered with undetectably?" → no, with
integrity enabled.

**Dependencies.** L0–L5.

**Scope of changes.** Spec file watcher in `pkg/newtron/network/`.
Secret store interface in a new `pkg/newtron/secret/` package with
file-based default backend. Hash chain in `pkg/newtron/audit/`. New
HOWTO section on operational procedures.

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
- No HTTP authentication middleware exists. The server accepts any
  request from any caller.
- The audit log package `pkg/newtron/audit/` exists but is not wired
  into authorization decisions.
- Every shipped `network.json` has `super_users: null`,
  `user_groups: null`, `permissions: null`. No operator has authored
  permission grants because there is nothing to grant.
- Out of 20 Permission constants, 5 are referenced (in spec/profile
  authoring); 15 are declared but never used.
- All three URL-derivable Context dimensions (`Device`, `Service`,
  `Interface`) have unused setters; only `Resource` is ever populated.

This document is L0. The layers L1–L6 are proposed; none has shipped.

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
