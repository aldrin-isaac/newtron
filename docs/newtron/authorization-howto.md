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
| Inter-service (newtlab calling newtron) | **Yes, but** — service identities (cert CN) typically map to `super_users` since the engines need broad authority to function. |

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
    "vrf.modify":       ["ops"],
    "vrf.delete":       ["ops"],
    "acl.create":       ["ops"],
    "acl.modify":       ["ops"],
    "acl.delete":       ["ops"],
    "lag.create":       ["ops"],
    "lag.modify":       ["ops"],
    "lag.delete":       ["ops"],
    "evpn.modify":      ["ops"],
    "service.apply":    ["ops"],
    "service.remove":   ["ops"],
    "interface.modify": ["ops"],

    "device.write":     ["ops"]
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

**Permission families** (auth-design.md L3 + L4):

| Family | What it gates |
|---|---|
| `spec.author` | Service/IPVPN/MACVPN/profile/zone/topology mutations on `network.json` and `topology.json` |
| `qos.create` / `qos.modify` / `qos.delete` | QoS policy spec + per-interface QoS apply |
| `filter.create` / `filter.delete` | Filter (ACL spec) authoring |
| `vlan.create` / `vlan.modify` / `vlan.delete` | Per-device VLAN + IRB configuration |
| `vrf.create` / `vrf.modify` / `vrf.delete` | Per-device VRF + IPVPN bind/unbind + static routes + per-interface BGP peers |
| `acl.create` / `acl.modify` / `acl.delete` | Per-device ACL CRUD + per-interface ACL bind/unbind |
| `lag.create` / `lag.modify` / `lag.delete` | PortChannel CRUD + member add/remove |
| `evpn.modify` | EVPN BGP peers + MACVPN bind/unbind |
| `service.apply` / `service.remove` | Per-interface service application |
| `interface.modify` | Per-interface property set/clear, configure/unconfigure |
| `device.write` | Operational mutations: `setup-device`, `init-device`, `config-reload`, `restart-service`, `exec-command`, `save`, `reconcile` |

## 3. Enable enforcement

On `newtron-server` or `newt-server`:

```sh
bin/newt-server \
  --spec-dir newtrun/topologies/1node-vs/specs \
  --audit-log /var/log/newtron-audit.jsonl \
  --audit-caller-header X-Newtron-Caller \
  --enforce-authorization
```

The five flags above engage the mutation audit log with header identity
(auth-design.md L1) and authorization enforcement (auth-design.md L3).
For production deployments add `--unix-socket`,
`--tls-cert`/`--tls-key`/`--tls-ca` (auth-design.md L2a), and
`--auth-pam-service` (auth-design.md L2b) so the identity surfaces are
verified rather than self-attested.

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

## 6. Revoking access

Today the authorization enforcer reads the grant table when
`EnableAuthorization` is called. To revoke a grant after startup:

1. Edit `network.json` on disk (remove the user from the group, or
   the group from the permission entry).
2. Call `POST /newtron/v1/networks/<id>/reload` — the server reloads
   the spec from disk and rebuilds the network. The fresh
   `EnableAuthorization` call binds the new grant table.

The window between the file edit and the reload is the revocation
SLA; a future layer (auth-design.md L6) adds an automatic file
watcher to shrink it.

## 7. Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Every call returns 403 even for super_users | `network.json` has `null` super_users + an empty `permissions` map; nothing matches. | Author at least one grant or a non-empty super_users list. |
| Same caller works on one engine, gets 403 on another | The engines have different `network.json` files; the spec is per-engine, not global. | Ensure both engines load the same spec dir (or share a registered network ID). |
| A new service's grants don't take effect | The authorization enforcer binds the Checker to the live spec at `EnableAuthorization` time. In-process spec mutations are observed through the same pointer, so this should not happen — but `Reload` is the safe path if grants don't engage. | `POST /newtron/v1/networks/<id>/reload`. |
| Decision audit entries are missing | `--audit-log` not set. | Add `--audit-log=/path/to/file.jsonl`. |
| 403 with empty `Caller` in the response data | No identity surface configured: no Unix socket, no mTLS, no PAM, no `--audit-caller-header`. | Engage at least one identity source per the audit log / mTLS / PAM HOWTOs. |

## Related

- [auth-design.md](auth-design.md) §5 L3 — design rationale and
  layered context.
- [pam-howto.md](pam-howto.md) — user identity verification via
  PAM (L2b).
- [mtls-howto.md](mtls-howto.md) — service identity verification
  via mTLS (L2a).
- [secret-store.md](secret-store.md) — encrypted-at-rest device
  credentials (L0).
