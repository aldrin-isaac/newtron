# Authorization Enforcement — Operational HOWTO

The authorization feature (auth-design.md L3) gates spec-mutation
endpoints on `newtron-server` and `newt-server` against the
`permissions` map in `network.json`. With it on, a caller verified
by L1 (Unix peer creds or self-attested header) or L2 (mTLS cert
CN, PAM-verified username) must hold the matching grant — otherwise
the engine returns HTTP 403 instead of acting.

L3 sits on top of L1 + L2: L1/L2 answer "who is calling," L3
answers "may they do this." Without L1/L2 supplying an identity,
L3 fails closed — every check denies. The two-layer pairing is the
intended deployment shape.

## 1. When to use L3

L3 applies wherever an operator can mutate spec state through the
HTTP surface (services, profiles, filters, QoS policies, route
policies, prefix lists, zones). Today's 26 gated call sites cover
the spec-authoring surface; Node-level write operations (creating
VLANs, applying services to interfaces) remain ungated — that's
L4.

| Deployment | L3 applies? |
|---|---|
| Single operator, loopback-only | Optional — the OS already gates who can reach 127.0.0.1. |
| Multi-operator, shared engine host | **Yes** — distinguishes who may author specs from who may only read. |
| Inter-service (newtlab calling newtron) | **Yes, but** — service identities (cert CN) typically map to `super_users` since the engines need broad authority to function. |

**Enable/disable per auth-design.md §2.4:** `--enforce-authorization`
defaults `false`; with no flag set, the 26 `checkPermission` call
sites are no-ops (pre-L3 behavior). Set it to `true` to enable.

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
    "spec.author":   ["spec-team"],
    "qos.create":    ["spec-team", "ops"],
    "qos.delete":    ["spec-team"],
    "filter.create": ["spec-team"],
    "filter.delete": ["spec-team"]
  }
}
```

- **`super_users`** bypass every check. Used for the boot-and-recover
  account and for inter-service identities that need broad authority
  to function (e.g., newtlab's cert CN when it calls newtron during
  deploy).
- **`user_groups`** name reusable membership sets.
- **`permissions`** maps each permission to the groups or direct
  usernames that hold it. The `"all"` wildcard key, if present,
  grants every permission to its listed groups.

Service-level overrides live under `services.<name>.permissions`
and tighten the global grant: an operator must satisfy the more
restrictive of the two.

## 3. Enable L3

On `newtron-server` or `newt-server`:

```sh
bin/newt-server \
  --spec-dir newtrun/topologies/1node-vs/specs \
  --audit-log /var/log/newtron-audit.jsonl \
  --audit-caller-header X-Newtron-Caller \
  --enforce-authorization
```

The five flags above engage L1 (audit log + header identity) and
L3 (enforcement). For production deployments add `--unix-socket`,
`--tls-cert`/`--tls-key`/`--tls-ca` (L2a), and `--auth-pam-service`
(L2b) so the identity surfaces are verified rather than self-
attested.

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

A no-identity request (the L1/L2 fallthrough case):

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

(The `id` field is currently empty for every audit event — L1 has
not yet wired ID generation. The other zero-valued fields —
`device`, `changes`, `execute_mode`, `dry_run`, `duration` — are
shared shape with L1 request-level events; decision events leave
them at their zero values.)

To filter for just decisions:

```sh
grep '"authcheck:' /var/log/newtron-audit.jsonl
```

A denial entry has `"success": false` and an `error` field with
the human-readable reason. The pre-decision L1 event for the same
request (`Operation: "POST /newtron/v1/networks/.../create-service"`)
appears in the same log file — match them by timestamp + user.

## 6. Revoking access

Today L3 reads the grant table when `EnableAuthorization` is
called. To revoke a grant after startup:

1. Edit `network.json` on disk (remove the user from the group, or
   the group from the permission entry).
2. Call `POST /newtron/v1/networks/<id>/reload` — the server reloads
   the spec from disk and rebuilds the network. The fresh
   `EnableAuthorization` call binds the new grant table.

The window between the file edit and the reload is the revocation
SLA; L6 adds an automatic file watcher to shrink it.

## 7. Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Every call returns 403 even for super_users | `network.json` has `null` super_users + an empty `permissions` map; nothing matches. | Author at least one grant or a non-empty super_users list. |
| Same caller works on one engine, gets 403 on another | The engines have different `network.json` files; the spec is per-engine, not global. | Ensure both engines load the same spec dir (or share a registered network ID). |
| A new service's grants don't take effect | L3 binds the Checker to the live spec at `EnableAuthorization` time. In-process spec mutations are observed through the same pointer, so this should not happen — but `Reload` is the safe path if grants don't engage. | `POST /newtron/v1/networks/<id>/reload`. |
| Decision audit entries are missing | `--audit-log` not set. | Add `--audit-log=/path/to/file.jsonl`. |
| 403 with empty `Caller` in the response data | No identity surface configured: no Unix socket, no mTLS, no PAM, no `--audit-caller-header`. | Engage at least one identity source per the L1/L2 HOWTOs. |

## Related

- [auth-design.md](auth-design.md) §5 L3 — design rationale and
  layered context.
- [pam-howto.md](pam-howto.md) — user identity verification via
  PAM (L2b).
- [mtls-howto.md](mtls-howto.md) — service identity verification
  via mTLS (L2a).
- [secret-store.md](secret-store.md) — encrypted-at-rest device
  credentials (L0).
