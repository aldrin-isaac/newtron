# Authorization Enforcement — Operational HOWTO

The authorization enforcement feature (auth-design.md L3) gates
spec-mutation endpoints on `newtron-server` and `newt-server` against
the `permissions` map in `network.json`. With it on, a caller verified
by identity verification (Unix peer creds or self-attested header for
audit log identity, or mTLS cert CN / PAM-verified username for
transport authentication) must hold the matching grant — otherwise
the engine returns HTTP 403 instead of acting.

Authorization enforcement sits on top of identity verification (audit
log identity + transport authentication): identity verification answers
"who is calling," authorization enforcement answers "may they do this."
Without identity verification supplying an identity, authorization
enforcement fails closed — every check denies. The two-layer pairing
is the intended deployment shape.

## 1. When to use authorization enforcement

Authorization enforcement applies wherever an operator can mutate
state through newtron's HTTP surface. Coverage as of L4 is full:
spec mutations (services, profiles, filters, QoS policies, route
policies, prefix lists, zones), topology mutations (devices, links),
Node-level operations (VLANs, VRFs, ACLs, port-channels, EVPN
peers), and Interface-level operations (service apply, ACL bind,
BGP peers, property set/clear, QoS apply) all gate before any
state change. Operational mutations like `setup-device`,
`init-device`, `config-reload`, `restart-service`, `exec-command`,
`save`, and `reconcile` gate behind the catch-all `device.write`.

| Deployment | Authorization enforcement applies? |
|---|---|
| Single operator, loopback-only | Optional — the OS already gates who can reach 127.0.0.1. |
| Multi-operator, shared engine host | **Yes** — distinguishes who may author specs from who may only read. |
| External automation (CI runner, newtcon Web UI, scripted operator tooling) | **Yes** — automation that needs broad authority typically authenticates via a service-account identity mapped to `super_users` in `network.json`; the same enforcement gate applies as for human operators. |

**Enable/disable per auth-design.md §2.4:** `--enforce-authorization`
defaults `false`; with no flag set, the 26 `checkPermission` call
sites are no-ops (pre-enforcement behavior). Set it to `true` to enable.

## 2. Author the grants in `network.json`

The grant table lives in `network.json` next to the topology. Three
top-level keys carry the inputs:

```json
{
  "super_users": ["root", "newtlab-server"],
  "user_groups": {
    "spec-team": ["alice", "bob"],
    "ops": ["charlie"]
  },
  "permissions": {
    "spec.author":      ["spec-team"],
    "qos.create":       ["spec-team", "ops"],
    "qos.delete":       ["spec-team"],
    "qos.modify":       ["spec-team", "ops"],
    "filter.create":    ["spec-team"],
    "filter.delete":    ["spec-team"],

    "vlan.create":      ["ops"],
    "vlan.modify":      ["ops"],
    "vlan.delete":      ["ops"],
    "vrf.create":       ["ops"],
    "vrf.bind":         ["ops"],
    "vrf.route":        ["ops"],
    "vrf.delete":       ["ops"],
    "bgp.peer":         ["ops"],
    "acl.create":       ["ops"],
    "acl.modify":       ["ops"],
    "acl.delete":       ["ops"],
    "lag.create":       ["ops"],
    "lag.modify":       ["ops"],
    "lag.delete":       ["ops"],
    "evpn.peer":        ["ops"],
    "evpn.macvpn":      ["ops"],
    "service.apply":    ["ops"],
    "service.remove":   ["ops"],
    "interface.modify": ["ops"],

    "device.write":     ["ops"]
  }
}
```

- **`super_users`** bypass every check. Used for the boot-and-recover
  account and for service-account identities that need broad authority
  to function (e.g., a CI runner's cert CN when it drives a deploy).
- **`user_groups`** name reusable membership sets.
- **`permissions`** maps each permission to the groups or direct
  usernames that hold it. The `"all"` wildcard key, if present,
  grants every permission to its listed groups.

Per-service scoping uses L5 `where: {service: "<pattern>"}` clauses
on global grants — see §6 below. Example:

```json
"permissions": {
  "service.apply": [
    { "groups": ["transit-team"], "where": { "service": "TRANSIT_*" } },
    { "groups": ["dci-team"],     "where": { "service": "DCI_*" } }
  ]
}
```

Service names are normalized at runtime (uppercase, hyphens →
underscores) before the gate runs — see §"Where-pattern canonical
form" below.

The pre-L5 embedded `services.<name>.permissions` field was
retired in #165 — one authorization table per network, not one per
spec (DPN §27).

**Permission families** (auth-design.md L3 + L4):

| Family | What it gates |
|---|---|
| `spec.author` | Service/IPVPN/MACVPN/profile/zone/topology mutations on `network.json` and `topology.json` |
| `qos.create` / `qos.modify` / `qos.delete` | QoS policy spec + per-interface QoS apply |
| `filter.create` / `filter.delete` | Filter (ACL spec) authoring |
| `vlan.create` / `vlan.modify` / `vlan.delete` | Per-device VLAN + IRB configuration |
| `vrf.create` / `vrf.delete` | Per-device VRF CRUD |
| `vrf.bind` | IPVPN bind/unbind on a VRF; Resource = VRF name |
| `vrf.route` | Static route add/remove inside a VRF; Resource = VRF name |
| `bgp.peer` | Per-interface direct BGP peer add/remove; Resource = peer IP |
| `acl.create` / `acl.modify` / `acl.delete` | Per-device ACL CRUD + per-interface ACL bind/unbind |
| `lag.create` / `lag.modify` / `lag.delete` | PortChannel CRUD + member add/remove |
| `evpn.peer` | EVPN BGP peer add/remove; Resource = peer IP |
| `evpn.macvpn` | MACVPN bind/unbind; Resource = `VLAN<id>` |
| `service.apply` / `service.remove` | Per-interface service application |
| `interface.modify` | Per-interface property set/clear, configure/unconfigure |
| `device.write` | Operational mutations: `setup-device`, `init-device`, `config-reload`, `restart-service`, `exec-command`, `save`, `reconcile` |

## 3. Enable enforcement

On `newtron-server` or `newt-server`:

```sh
bin/newt-server \
  --audit-log /var/log/newtron-audit.jsonl \
  --audit-caller-header X-Newtron-Caller \
  --enforce-authorization
```

(Networks are auto-discovered from `./networks/` — point at a custom
tree with `--networks-base <path>` if needed.)

The five flags above engage the mutation audit log with header identity
(auth-design.md L1) and authorization enforcement (auth-design.md L3).
For production deployments add `--unix-socket` and `--auth-pam-service`
(auth-design.md L2b) so the identity surfaces are verified rather than
self-attested. For TLS, add `--tls-cert`/`--tls-key`/`--tls-ca`
(auth-design.md L2a) so the TCP listener serves mTLS and the cert CN
becomes the caller identity; alternatively terminate TLS at a reverse
proxy in front of `cmd/newt-server`. See [`mtls-howto.md`](mtls-howto.md).

## 4. Verify

A caller in `spec-team`:

```sh
curl -X POST http://127.0.0.1:18080/newtron/v1/networks/default/create-service \
  -H "X-Newtron-Caller: alice" \
  -H "Content-Type: application/json" \
  -d '{"name":"svc-a","type":"routed"}'
# → 201 Created
```

A caller outside any group with `spec.author`:

```sh
curl -X POST http://127.0.0.1:18080/newtron/v1/networks/default/create-service \
  -H "X-Newtron-Caller: mallory" \
  -H "Content-Type: application/json" \
  -d '{"name":"svc-b","type":"routed"}'
# → 403 Forbidden
# {"data":{"caller":"mallory","permission":"spec.author","resource":"svc-b"},
#  "error":"authorization denied: mallory lacks spec.author on svc-b"}
```

A no-identity request (no identity surface configured — no Unix socket peer creds, no mTLS cert CN, no PAM, no caller header):

```sh
curl -X POST http://127.0.0.1:18080/newtron/v1/networks/default/create-service \
  -H "Content-Type: application/json" \
  -d '{"name":"svc-c","type":"routed"}'
# → 403 Forbidden  (empty Caller is denied)
```

## 5. Decision events in the audit log

With both `--audit-log` and `--enforce-authorization` set, every
permission check writes one JSON-line event:

```json
{
  "id": "",
  "timestamp": "2026-06-10T14:21:08.412Z",
  "user": "alice",
  "verification_source": "self_attested_header",
  "device": "",
  "operation": "authcheck:spec.author",
  "service": "svc-a",
  "changes": null,
  "success": true,
  "execute_mode": false,
  "dry_run": false,
  "duration": 0
}
```

(The `id` field is currently empty for every audit event — the audit
log implementation has not yet wired ID generation. The other
zero-valued fields — `device`, `changes`, `execute_mode`, `dry_run`,
`duration` — are shared shape with request-level audit events;
decision events leave them at their zero values.)

To filter for just decisions:

```sh
grep '"authcheck:' /var/log/newtron-audit.jsonl
```

A denial entry has `"success": false` and an `error` field with
the human-readable reason. The pre-decision request-level audit event
for the same request (`Operation: "POST /newtron/v1/networks/.../create-service"`)
appears in the same log file — match them by timestamp + user.

## 6. Fine-grained per-resource grants

The legacy `"permission": ["group"]` shorthand grants the listed
groups everywhere — every device, every service, every interface.
Real deployments need to scope: "edge-team can write to edge-*
devices only; spine-team can write to spine-* only." The typed
grant form expresses that:

```json
{
  "permissions": {
    "device.write": [
      { "groups": ["edge-team"],  "where": { "device": "edge-*" } },
      { "groups": ["spine-team"], "where": { "device": "spine-*" } }
    ],
    "service.apply": [
      { "groups": ["transit-team"], "where": { "service": "TRANSIT_*" } }
    ]
  }
}
```

Each entry is `{groups, where}`. The where clause restricts when
the grant applies — every named dimension must match (AND across
dimensions). An empty or absent `where` matches every context (the
legacy behavior — useful for "all-rooms" grants that don't need
scoping).

The two forms can mix freely. A permission can list legacy entries
alongside typed ones, the unmarshaller handles both. Round-trip
preserves the form: a permission with no scoping emits as a flat
list; a scoped one emits as objects.

**Pattern syntax — one matcher across all dimensions:**

| Pattern | Matches |
|---|---|
| `"edge-1"` | exact |
| `"edge-*"` | glob (suffix wildcard; `*` allowed only at end) |
| `"edge-1,edge-2"` | comma list — value matches any item (OR) |
| `"!permissions"` | exclusion (bang prefix) — value must NOT match this |
| `"!permissions,!user_groups"` | exclusion list — value must NOT match any item (AND none of these) |
| `"edge-*,!edge-broken"` | mixed — value matches `edge-*` AND not `edge-broken` |

When the pattern consists only of excludes, it reads as "anything
except these" — the shape the meta-authorization scenario below uses.

**Dimensions you can scope on:**

| Dimension | What populates it |
|---|---|
| `device` | The device being acted on (URL `/nodes/{device}/*`, or the device name passed to topology mutations) |
| `service` | The service being applied or mutated (URL `/services/{name}`, or `service` field in interface apply-service body) |
| `interface` | The interface being mutated (URL `/interfaces/{name}/*`) |
| `resource` | The generic identifier of the thing being acted on — populated alongside the more specific dimension when applicable |
| `field` | The top-level spec area the mutation touches: `services`, `profiles`, `topology`, `permissions`, `user_groups`, `super_users`, `qos_policies`, `filters`, `prefix_lists`, `route_policies`, `ipvpns`, `macvpns`, `zones`. Used for meta-authorization (below). |

Unknown dimensions in a `where` clause **fail closed** — a typo
like `"devic": "edge-*"` denies the request rather than silently
matching everything. This keeps the grant table honest.

**Where-pattern canonical form.** Patterns for the `service` and
`resource` dimensions must be authored in **canonical form** —
uppercase letters with hyphens replaced by underscores
(`util.NormalizeName`). `Interface.gateService` normalizes the
operator-supplied service name before stamping it on
`Context.Service` / `Context.Resource`, so a runtime gate sees
`TRANSIT_1` even when the operator submitted `transit-1`. A
`where: {service: "transit-*"}` clause never matches and the grant
becomes inert. The `device` and `interface` dimensions pass through
raw — their patterns match device / interface names as they appear
in topology and spec files (`edge-*`, `Ethernet0`).

This is why every example above uses `TRANSIT_*` / `DCI_*` for
service-dimension patterns and `edge-*` / `spine-*` for
device-dimension patterns.

**Granularity of rule-modification gates.** Four spec surfaces
operate on rules inside a container object — filter rules,
prefix-list entries, route-policy rules, and QoS queues. Their
gates stamp `Resource = <container name>`, not
`Resource = <rule identifier>`:

| Operation | `Resource` stamped |
|---|---|
| `AddFilterRule` / `RemoveFilterRule` | filter name |
| `AddPrefixListEntry` / `RemovePrefixListEntry` | prefix-list name |
| `AddRoutePolicyRule` / `RemoveRoutePolicyRule` | route-policy name |
| `AddQoSQueue` / `RemoveQoSQueue` | qos-policy name |

So `where: {resource: "filter-A"}` authorizes editing **every**
rule inside `filter-A` — per-rule scoping (e.g., "only rule index 5")
is not expressible. This is deliberate: rule indices shift when
earlier rules are inserted, so an index-scoped grant would
silently re-target after any edit. The container is the stable
scope.

If a deployment needs finer-grained authority, split the container:
two filters that each grant to a different team are expressible;
one filter with per-rule grants is not.

## 7. Meta-authorization — separating who can grant access

`spec.author` gates every spec mutation, including changes to the
`permissions`, `user_groups`, and `super_users` fields. Without
scoping, the role "can author services" collapses with "can grant
access to others." Real deployments separate them via the `field`
dimension:

```json
{
  "permissions": {
    "spec.author": [
      { "groups": ["service-architects"],
        "where": { "field": "!permissions,!user_groups,!super_users" } },
      { "groups": ["iam-team"],
        "where": { "field": "permissions,user_groups,super_users" } }
    ]
  }
}
```

A service architect (carol) can author services, profiles, topology,
QoS policies, filters, route policies — everything except the grant
table itself. An IAM operator (dave) can manage groups, super_users,
and the permissions map but cannot mutate service specs.

`super_users` continue to bypass every check — they're the bootstrap
and recovery role. Treat the `super_users` list itself as a
field-scoped resource that only the IAM team can edit via the example
above.

## 8. Inspecting the active grant table

`network.json` on disk is the source of truth, but a reload between
edits and the next operator question can put the live table out of
sync with the file briefly. To read what the auth checker is
actually enforcing right now:

```sh
curl -s http://newt-server/newtron/v1/networks/default/authorization | jq .data
```

The response is the same three fields (`user_groups`,
`permissions`, `super_users`) `network.json` carries, in the same
wire form — shorthand `["group", ...]` when a grant has no scope,
typed `[{"groups": [...], "where": {...}}]` when it does. An
operator can copy a `permissions` block from the response directly
into `network.json` and the loader will accept it unchanged. See
[api.md GET /authorization](api.md#get-newtronv1networksnetidauthorization)
for the full type reference.

The endpoint is gated by `auth.read` under an **engage-when-configured**
mechanism: if no `auth.read` entry exists in the loaded grant
table, the endpoint stays ungated — preserves the zero-ceremony
quickstart and any deployment that took the inspector for granted
as readable. The moment an operator adds the first entry, the gate
engages.

```json
"permissions": {
  "auth.read": [
    { "groups": ["iam-team", "audit-team"] },
    { "groups": ["service-architects"],
      "where": { "field": "!permissions,!user_groups,!super_users" } }
  ]
}
```

Why gate it at all: the response carries identity-policy material —
which groups exist, who's in them, which permissions are scoped to
which groups, what the where-clauses are. In a zero-trust
deployment, this telegraphs to an attacker which targets to phish.
For coarse-trust deployments where every authenticated identity is
trusted to introspect, the default ungated behavior is correct;
nothing changes.

The `field` where-dimension composes the same way it does on
`spec.author` (§7): scope `auth.read` to `!permissions` and a
caller without the permissions-block grant is denied even though
they're in a group. v1 is full-or-nothing: any `where` clause that
doesn't match all three spec-fields the response carries
(`super_users,user_groups,permissions`) denies. Partial redaction
(returning `user_groups` without `permissions`) is filed as a v2
follow-up.

Super-users continue to bypass `auth.read` like every other
permission — read the grant table from a super-user session if
you're locked out and need to diagnose the gate.

## 9. Revoking access

The authorization enforcer reads the grant table when
`EnableAuthorization` is called. Two flows revoke access after
startup:

**Manual revoke (no watcher).** When the server runs without
`--spec-watch`:

1. Edit `network.json` on disk (remove the user from the group, or
   the group from the permission entry).
2. Call `POST /newtron/v1/networks/<id>/reload`. The server reloads
   the spec and rebinds the auth checker to the new grant table.

**Automatic revoke (`--spec-watch=true`).** When the server runs
with `--spec-watch`, the file edit alone is enough:

1. Edit `network.json` on disk.
2. Wait ~1 second. The watcher detects the change, debounces rapid
   editor saves, and calls `ReloadNetwork` automatically. The
   server logs `spec-watcher: reloaded network 'id' after change at <path>`.

Subsequent requests from the revoked caller return HTTP 403.

The 1-second debounce absorbs editor save sequences (write + rename
+ write is typical) so a single grant edit produces one reload, not
one per fsnotify event. For deployments that need a tighter SLA,
construct `network.SpecWatcher` with a smaller debounce — the API
isn't exposed through a flag because most deployments prefer the
default.

The watcher also fires on changes to the `profiles/` subdirectory,
catching device-profile JSON rotations as part of the same revoke
flow.

## 10. Audit log integrity

With `--audit-log` set, every mutation and every authorization
decision appends to a JSON-lines file. With `--audit-log-integrity`
ALSO set, each entry carries a hash chain:

- `Event.PrevHash` — the previous entry's `ID`
- `Event.ID` — `SHA256(prev_hash || canonical_json_of_event_with_zero_id)`

Tampering with any past entry — modifying a field, deleting an
entry, reordering — breaks the chain at the tampered position and
every subsequent link.

**Verify periodically:**

```sh
bin/newtron audit verify /var/log/newtron-audit.jsonl
# verified 18342 entries; chain head = 7c0a2bf9d3e1c5a8...

# After a tamper:
bin/newtron audit verify /var/log/newtron-audit.jsonl
# audit chain broken at line 1428: id hash mismatch (entry content modified)
# (exits 1)
```

The exact reason depends on the tamper shape: modifying any field of
an existing entry produces `id hash mismatch (entry content
modified)`; deleting or inserting an entry produces `prev_hash
mismatch (got "<hash>", expected "<hash>")`.

The verifier returns:

| Exit | Meaning |
|------|---------|
| `0` | chain clean (or file missing — nothing to tamper) |
| `1` | tamper detected; line number + reason printed to stderr |
| `2` | I/O or argument error |

Run on a daily cron, after a suspected intrusion, or before exporting
logs for a security review.

**Chain across restarts.** The FileLogger recovers the chain head
from the file's last well-formed entry on startup, so the chain
continues across server restarts. A multi-lifecycle log verifies as
one chain end to end.

**Pre-integrity entries.** A log that pre-dates the
`--audit-log-integrity` upgrade carries empty `ID` and `PrevHash`
fields. The verifier skips those entries and resumes the chain
expectation once it sees a non-empty `ID`. Operators can switch on
integrity mid-stream without invalidating the historical log.

**HTTP equivalents.** The same data is reachable via two HTTP
endpoints — useful for operator UIs that want to render a
browsable event timeline and a chain-integrity badge:

```sh
# Paged, filtered event list
curl -s 'http://newt-server/newtron/v1/networks/default/audit/events?user=alice&since=2026-06-01T00:00:00Z' \
  | jq .data

# Chain-integrity status
curl -s http://newt-server/newtron/v1/networks/default/audit/integrity | jq .data
# {
#   "chain_head_hash": "7c0a2bf9d3e1c5a8...",
#   "entry_count":     18342,
#   "break_at":        0,
#   "break_reason":    "",
#   "verified_at":     "2026-06-17T07:30:00Z"
# }
```

Both endpoints are gated by `audit.read` under the **engage-
when-configured** mechanism (same pattern as `auth.read`, §8):
no `audit.read` entry in the grant table means the endpoints stay
ungated; the first entry engages the gate. The `field`
where-dimension splits the surface: `where:{field:"audit_events"}`
grants event-list reads only; `where:{field:"audit_integrity"}`
grants the chain-status read only; empty `where` grants both.

```json
"permissions": {
  "audit.read": [
    { "groups": ["iam-team", "audit-team"] },
    { "groups": ["sre-team"],
      "where": { "field": "audit_integrity" } }
  ]
}
```

The first form covers an audit/compliance role; the second carves
out an SRE rotation that wants the tamper tripwire without
reading event content. See [api.md §Audit log](api.md#audit-log)
for the full endpoint reference.

## 11. Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Every call returns 403 even for super_users | `network.json` has `null` super_users + an empty `permissions` map; nothing matches. | Author at least one grant or a non-empty super_users list. |
| Same caller works on one engine, gets 403 on another | The engines have different `network.json` files; the spec is per-engine, not global. | Ensure both engines load the same spec dir (or share a registered network ID). |
| A new service's grants don't take effect | The authorization enforcer binds the Checker to the live spec at `EnableAuthorization` time. In-process spec mutations are observed through the same pointer, so this should not happen — but `Reload` is the safe path if grants don't engage. | `POST /newtron/v1/networks/<id>/reload`. |
| Decision audit entries are missing | `--audit-log` not set. | Add `--audit-log=/path/to/file.jsonl`. |
| 403 with empty `Caller` in the response data | No identity surface configured: no Unix socket, no mTLS, no PAM, no `--audit-caller-header`. | Engage at least one identity source per the audit log / mTLS / PAM HOWTOs. |
| `network.json` edits aren't auto-reloaded | `--spec-watch` not set; the operator must `POST /reload`. | Add `--spec-watch=true`, or POST `/newtron/v1/networks/<id>/reload` explicitly. |
| `audit verify` reports a broken chain | Either a real tamper, or pre-integrity entries before `--audit-log-integrity` was engaged. | Inspect the entry at the reported line. Pre-integrity entries have empty `id`; they're skipped by the verifier. A non-empty `id` whose hash doesn't reproduce means the entry was modified on disk. |
| `--audit-log-integrity` does nothing | `--audit-log` is empty. Integrity has nothing to hash without a log target. | Add `--audit-log=/path/to/file.jsonl`. The server logs a warning at startup when only one of the two is set. |

## Related

- [auth-design.md](auth-design.md) §5 L3 — design rationale and
  layered context.
- [pam-howto.md](pam-howto.md) — user identity verification via
  PAM (L2b).
- [mtls-howto.md](mtls-howto.md) — operator-to-server listener-side
  TLS (L2a).
- [secret-store.md](secret-store.md) — encrypted-at-rest device
  credentials (L0).
