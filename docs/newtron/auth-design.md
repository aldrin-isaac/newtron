# Auth Design — Open Exploration

## 1. Purpose

newtron's authorization subsystem is **partially built and intentionally
inert at runtime**. This document exists because the gap between the code's
appearance and its behavior is itself an architectural problem — anyone
reading `spec_ops.go` will see ~25 `checkPermission` calls and reasonably
conclude that authorization is being enforced. It isn't, and the reasons
why deserve to be written down rather than discovered through grep.

This doc is for two audiences:

1. **A future contributor** trying to understand why the auth code exists
   and what to do with it.
2. **A future security reviewer** asking "what's the authorization model?"
   so they can answer "what's enforced" without reverse-engineering
   the call graph.

This is a *design exploration* doc, not a reference. The reference for
what's enforced today is short: nothing is. Everything else here is open.

---

## 2. Current Enforcement Status

**The auth subsystem is wired into the code but never activated at runtime.**

- `Network.auth` field exists. `Network.SetAuth(checker)` is the only way
  to populate it. **`SetAuth` has zero callers across the codebase** — not
  in any `main()`, not in any test that exercises the server end-to-end.
- `Network.checkPermission(perm, ctx)` returns `nil` when `net.auth` is
  nil. Since `SetAuth` is never called, every check is an unconditional
  no-op.
- ~25 invocations of `net.checkPermission(auth.PermSpecAuthor, ...)`
  exist in `spec_ops.go` and `profile_ops.go`. Every one of them
  short-circuits to "allowed" at runtime.

The HLD has been honest about this since the auth code was introduced
(see `docs/newtron/hld.md` §11 on Security: "permission types exist in
code but are not enforced at the HTTP layer. The server has no
authentication middleware — it is designed for trusted-network deployment
(localhost or VPN)"). This document is what that one sentence expands into.

---

## 3. What the Code Already Asserts

Even unwired, the existing code commits to five design positions. Read as
exploration rather than as half-built feature, these are the answers the
project has *already* given to authorization questions:

### 3.1 Permissions Live in the Spec, Not in Code

`network.json` carries the authorization configuration:

```json
{
  "super_users": ["aldrin"],
  "user_groups": {
    "operators": ["alice", "bob"],
    "spec-authors": ["alice", "charlie"]
  },
  "permissions": {
    "all": ["super_users"],
    "service.apply": ["operators"],
    "spec.author": ["spec-authors"]
  }
}
```

Authorization is **declarative, version-controlled, code-reviewable**. There
is no runtime registry, no separate IAM system, no LDAP integration. The same
git workflow that gates spec changes gates permission changes.

### 3.2 Two-Tier Evaluation: Service-Specific Overrides Global

`ServiceSpec.Permissions` can override `NetworkSpec.Permissions` for a
specific service. A user might have global `service.apply` but the
`transit-peering` service grants `service.apply` to a narrower group. The
Checker tries service-specific first, then falls through to global.

The implicit position: *authorization is contextual to what you're operating
on, not just what you're doing*.

### 3.3 Wildcard "all" + Specific Keys

`permissions: { "all": [...], "specific.action": [...] }` — the `"all"`
key grants every permission to its members. Used for admin-class groups
without enumerating every permission constant.

### 3.4 Groups with Literal-User Fallback

`checkPermissionMap` checks if the username appears directly in the allowed
list OR as a member of a named group in that list. No separate "user
resource" — group membership is just a list of usernames, and a permission
grant is just a list of group-or-user names.

### 3.5 Superusers Bypass Everything

`isSuperUser` is checked before any permission evaluation. Superusers are
defined in `network.json:super_users` and never traverse the permission map.

This is explicit, not implicit — there's no special "root" permission, no
backdoor wildcard role. Superuser bypass is a single named check at the top
of `Checker.checkUser`.

---

## 4. Open Questions

These are the questions the current code does *not* answer. Each is genuine
architectural work; each has a proposed direction, presented as something to
push back on, not as a settled decision.

### 4.1 Q: Who is the caller?

**Current state.** `Checker.currentUser` is set once at `NewChecker` time
from `os/user.Current()`. That's the *server process's* OS user. In a
multi-user HTTP deployment, every request is attributed to the server's
account regardless of who actually made the request.

**Why it matters.** Without a per-request caller identity, the whole
permission model collapses. There's no point in checking "does X have
permission Y" if X is always "the server."

**Options:**

| Option | Wire format | Tradeoffs |
|---|---|---|
| Reverse-proxy header | `X-Newtron-Caller: alice` from a trusted upstream (nginx + OAuth, oauth2-proxy, etc.) | Zero in-process crypto. Operator chooses auth mechanism. Trust depends on listener binding to loopback or a private interface. Matches "trusted-network deployment" framing. |
| HTTP Basic Auth | `Authorization: Basic ...` decoded by middleware | Self-contained. Requires the server to hold credentials (or PAM/SSSD lookup). Adds password management to project scope. |
| mTLS client certs | TLS client cert verified, identity from cert CN | Strongest. Requires CA infrastructure on top of newtron, which is a large operator burden. |
| Bearer token | `Authorization: Bearer ...` with a token registry | Requires an issuer. Adds token lifecycle to project scope. |
| Unix socket peer creds | `SO_PEERCRED` on a Unix listener | Local-only. Doesn't generalize to remote operators. |

**Proposed direction.** Reverse-proxy header (`X-Newtron-Caller`) as the
default; fall back to `$USER` (or `os/user.Current()`) when no header is
present. This matches the HLD's "trusted-network deployment" framing: the
operator's network already has auth infrastructure (whatever it is); newtron
trusts a header from it. For the standalone-loopback dev/single-user case,
the env fallback gives a real identity without any setup. The header name
is configurable via a server flag (`--caller-header`); empty disables
header-based auth.

This puts the operator in charge of the *authentication* mechanism (which
varies across deployments) while letting newtron own the *authorization*
mechanism (which is what we're exploring).

### 4.2 Q: How does identity flow from HTTP to checkPermission?

**Current state.** The handler signature `func(n *newtron.Node) (any, error)`
doesn't carry caller identity. There's no plumbing between "the HTTP middleware
knows who's calling" and "checkPermission deep in spec_ops needs to know."

**Options:**

| Option | Cost | Drawback |
|---|---|---|
| Thread `Caller` param through every Network/Node method | High (many signatures change) | Visible everywhere; can't be forgotten. |
| `context.Context` value (request-scoped) | Low | Caller can forget to propagate context; loose typing. |
| Extend `auth.Context` to carry `Caller` | Low | Existing type with the right name; just add a field. |
| Mutable `Network.currentCaller` field | Zero | Wrong — a server handles concurrent requests. |

**Proposed direction.** Extend `auth.Context` to carry an explicit `Caller`
field. The handler middleware sets it once at request entry; all
`checkPermission(perm, ctx)` calls already take an `*auth.Context`, so the
plumbing reaches every check site without signature changes. The Checker
reads `ctx.Caller` instead of `c.currentUser`.

This positions `auth.Context` as the request-scoped authorization envelope —
caller identity + resource context together — rather than just a resource
descriptor.

### 4.3 Q: What populates Context.Device/Service/Interface?

**Current state.** `auth.Context` has four dimensions: `Device`, `Service`,
`Interface`, `Resource`. All 25 callsites use `WithResource(name)` only. The
Device/Service/Interface setters exist (`WithDevice`, `WithService`,
`WithInterface`) but have zero callers tree-wide.

**Why it matters.** The dimensions encode an architectural claim:
authorization is contextual to multiple axes (device, service type, interface),
not just to the name of the thing. If the claim is true, the dimensions are
required for fine-grained grants like "alice can modify VLANs on switch1
only." If the claim is false, the three unused setters are aspirational
surface and should go.

**Two coherent stances:**

- **Stance A — Decision context.** The dimensions exist *because authorization
  decisions need them*. A grant like "operators may apply transit-service on
  edge devices only" requires the check to know which device and which
  service. Populating the dimensions is mandatory; the Checker uses them in
  the allow/deny logic.

- **Stance B — Audit context.** The dimensions exist *for the audit log*, not
  for the decision. The allow/deny logic considers only `Caller` and
  `Permission`. The `Device`/`Service`/`Interface` get logged when a decision
  is made, so the audit trail records "alice applied transit-service to
  switch1:Ethernet0" rather than just "alice did service.apply." Populating
  them is recommended but not required for the check itself.

**Proposed direction.** Stance B, for now. Per-resource permission grants
(stance A) are a real feature, and shipping them requires the spec to grow
syntax — `permissions: { "service.apply": { "groups": ["operators"],
"devices": ["edge-*"], "services": ["transit-*"] } }` or similar. That's a
substantial spec change with its own design considerations (glob matching?
explicit lists? deny lists?). Better to commit to stance A only when there's
a concrete use case driving it. Until then: the dimensions are audit
metadata, and the spec stays simple.

Concretely: keep `WithDevice`/`WithService`/`WithInterface` as setters;
add a contract comment that they're for audit context only; have callers
populate them where the data is cheaply available (handler knows the device
from URL path).

### 4.4 Q: Why do spec_ops/profile_ops have checks but Node ops don't?

**Current state.** ~25 checks in spec/profile authoring. Zero checks in
Node-level write operations: `CreateVLAN`, `CreateVRF`, `BindIPVPN`,
`ConfigureIRB`, `AddBGPEVPNPeer`, `BindACL`, `ApplyService`, and ~30 others
all execute with no authorization gate.

The existing split — "authoring is checked, operation isn't" — is a
historical accident (the checks were added when spec authoring was the
feature under consideration), not a considered design.

**The architectural question.** Is "spec authoring" stricter than "device
operation," and if so, why?

**Three positions:**

- **Position A — Yes, asymmetric.** Spec authoring modifies the
  version-controlled source of truth; it's like committing code. Device
  operation applies an already-approved spec; it's like a deploy. Different
  populations naturally have different scopes — the team that approves
  changes is smaller than the team that operates the network. Authoring
  gates make sense; operation gates are noise.

- **Position B — No, symmetric.** Both authoring and operation are mutating
  actions against shared state. Both deserve authorization. The "deploy
  vs. commit" analogy breaks down because deploying a bad service on the
  wrong device causes the same outage as authoring the bad service in the
  first place. Both should be gated.

- **Position C — Differentiated.** Authoring is gated as today. Operation
  is gated by *device*, not by operation type — a single `device.write`
  permission per device. So "alice can operate on edge switches, bob can't
  touch the spine cluster" is expressible without 30 separate operation
  permissions.

**Proposed direction.** Position C. The authoring/operation distinction is
real (positions A's intuition is correct), but a wide-open operation surface
is wrong (position B's intuition is also correct). The synthesis:

- Spec authoring keeps fine-grained per-resource permissions (`spec.author`,
  `qos.create`, `filter.create`, etc.) — already there, stays.
- Device operation gets a single `device.write` permission, checked uniformly
  at the start of every Node-level write method. Either you can operate on a
  device or you can't.
- Per-device differentiation comes from spec, not from code: `network.json`
  grants `device.write` to specific groups; service-level overrides further
  refine.

Read operations stay ungated (already the case; documented design).

### 4.5 Q: What does authorization mean in a "trusted-network" model?

**Current state.** HLD says "trusted-network deployment." Code carries
permission constants for device-write operations. These statements look
contradictory.

**The reconciliation:** "Trusted network" addresses the *threat model*,
not the *authorization model*. They're orthogonal axes:

- *Threat model*: who can reach the endpoint? Trusted-network means "we
  trust the transport: no MITM, no unauthenticated impersonation at the
  network layer." Operator handles this with VPN/loopback/private
  interfaces. newtron doesn't ship TLS or token issuance because the
  operator already has those.
- *Authorization model*: among the people who *can* reach the endpoint,
  who's allowed to do what? Even in a small trusted org, the network
  operator and the spec author are usually different people with
  different scopes.

"Trusted network = trusted for transport, not trusted for authorization."

**Proposed direction.** Adopt that framing explicitly. Update HLD §11 to
say: "newtron has no built-in transport authentication or TLS — it relies
on the operator's network (loopback, VPN, mTLS proxy, etc.) to authenticate
the caller. Once the caller is identified, newtron enforces per-user
authorization against the spec-declared permission map." Then this auth
exploration is no longer contradictory with the HLD — it's the layer above
trust-the-network.

---

## 5. Path Forward

The exploration moves forward through a small arc of PRs. Each one closes
some of the open questions above; the doc gets edited (not appended to) as
positions settle.

1. **PR 1 (this doc).** Articulate the positions and the questions.
2. **PR 2 — Truth-up the surface to the design.** Resolve §4.3 (drop the
   misleading Context setters or commit to populating them) and §4.4
   (commit to position C — add `device.write`). Delete the 15 unused
   `Permission` constants that no proposed direction needs (the
   per-resource VLAN/VRF/ACL/LAG/EVPN/Interface modify permissions get
   subsumed under `device.write`; `service.apply` and `service.remove`
   become the same). Delete the misleading "Permission-based access
   control" doc claim in `cmd/newtron/main.go`. After this PR the code's
   surface matches the design positions, even though enforcement is
   still inert.
3. **PR 3 — Wire identity through.** Implement §4.1 (reverse-proxy header
   + `$USER` fallback) and §4.2 (extend `auth.Context` with `Caller`).
   Server bootstrap calls `SetAuth`. Now `checkPermission` actually
   runs. The 25 existing call sites become live gates.
4. **PR 4 — Close coverage.** Add `checkPermission(auth.PermDeviceWrite,
   ...)` to every Node-level write method per §4.4 position C. Verify via
   the existing `TestAPICompleteness` pattern that no write op is missed.
5. **PR 5+ — Iterate as the design questions surface.** Per-resource
   grants (stance A from §4.3) gets implemented only if a concrete use
   case requires it.

Each PR is small and reversible. None of them require deleting the
exploration — they advance it.

---

## 6. What This Document Doesn't Cover

- **The audit log.** Authorization decisions deserve to be recorded
  somewhere; that's a separate design question (where? what fields?
  what retention?). The audit log exists today as `pkg/newtron/audit/`
  but isn't tied to authorization. Reconciling them is its own doc.
- **Spec field schema for per-resource grants.** Stance A from §4.3
  needs a spec design — glob matching vs explicit lists, deny rules,
  precedence with global grants. Not in scope until a use case appears.
- **Token-based deployment.** §4.1 explicitly rules out bearer tokens
  for the default path. If newtron ever ships a hosted multi-tenant
  story (currently not on the roadmap), tokens come back in scope.
- **Roles and RBAC.** The current model is permission-based (verbs).
  A role-based model (nouns: "operator," "auditor," "admin") is a
  different design. Could be layered on top of permissions in spec
  syntax later; not required to make permissions work.
