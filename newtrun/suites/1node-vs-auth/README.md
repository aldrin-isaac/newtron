# 1node-vs-auth — end-to-end auth-arc exercise

This suite drives the auth arc (L0–L6) through real HTTP scenarios
against a `newt-server` running with the full auth flag set. It
verifies what unit tests can't: that the flags actually take effect
in a deployed binary, that grants in `network.json` propagate to
runtime decisions, and that the audit log ends up where operators
expect.

## What it covers

| Scenario | Layer | Verification |
|---|---|---|
| `00-L0-secret-store-resolves` | L0 | `${secret:KEY}` in `profiles/switch1.json` resolves at network load; profile read returns the unresolved value never reaches the wire. |
| `10-L1-audit-log-entries` | L1 | A handful of mutations as different callers; operator inspects the audit log file to see one entry per request with caller=alice/bob in the user field. |
| `20-L3-spec-mutations-gated` | L3 | Per perm family (spec.author, qos.create, filter.create): denied caller gets 403, allowed caller succeeds. Plus the empty-caller fail-closed check. |
| `25-L2c-disabled-routes` | L2c | When the server runs without `--auth-pam-service` (as in this suite), `POST /auth/login` and `POST /auth/logout` return 404 — the disabled-path safety contract. Also pins that the new `withSessionKey` middleware doesn't break header-mode identity: a request with `Authorization: Bearer <anything>` plus `X-Newtron-Caller: alice` still resolves as alice. |
| `30-L4-node-mutations-gated` | L4 | Same shape on Node-level mutations (vlan.create, vrf.create, acl.create) via `?mode=topology`. |
| `40-L5-resource-scoping` | L5 | alice's `service.apply` grant scopes to `resource: "transit-*"`; she can apply transit-1, denied on vpn-east. bob's grant is the inverse. |
| `50-L6-audit-verify` | L6 | `bin/newtron audit verify /tmp/1node-vs-auth-audit.jsonl` walks the chain and confirms it's intact end-to-end. |
| `60-L3-permission-families` | L3 | Handler categories the original suite skips: super_user bypass (root sails through every check), profile/zone/topology mutations (gated on spec.author but routed through different handlers than services). |
| `70-L4-permission-families` | L4 | Node-mutation perm families the original suite skips: lag.create (create-portchannel), evpn.modify (add-bgp-evpn-peer, prerequisite setup-device included), interface.modify (interface set-property), device.write (setup-device denied for mallory). |
| `80-L5-dimensions` | L5 | The three `where` dimensions beyond `resource`: **field** (architects scoped to `!permissions,!user_groups,!super_users` matches services but not the grant table itself — the §3 criterion 9 meta-authorization separation), **interface** (intf-isaac scoped to `interface: "Ethernet0"` can bind ACLs on Eth0 but is denied on Eth4), **device** (dev-dora scoped to `device: switch1` is allowed; mallory still denied without any group). |

## What it deliberately does NOT cover

These verifications can't fit the current newtrun suite model:

- **L2a inter-service mTLS** — N/A for `newt-server` (single process, no inter-engine network calls).
- **L2b user-to-service PAM** — requires host PAM configuration (`/etc/pam.d/newtron-server`) and a real OS account; the suite can forge `X-Newtron-Caller` but not real Basic-auth credentials.
- **L2c session-key round trip** — L2c only engages with L2b configured, and `newt-server` has no `--auth-pam-service` flag today. Once `newt-server` gains the PAM flag, the round-trip can be expressed as a suite scenario using newtrun's response-capture: `POST /auth/login` captures `.key` as `session_key`, the next mutation step's headers carry `Authorization: Bearer {{captured.session_key}}`, `POST /auth/logout` revokes, and a final mutation with the same captured key asserts 401. The `25-L2c-disabled-routes` scenario covers what's testable today (against the PAM-less `newt-server`); the round-trip is in "Manual verifications" below.
- **L6 spec-watch** — requires editing `network.json` mid-suite to observe auto-reload. There's no `local-exec` step action today (deferred follow-up).
- **L6 audit tamper detection** — requires modifying a log entry mid-suite to confirm verify catches it. Same `local-exec` gap.

Operators verify these manually with a small shell session; pattern below.

## Design observation surfaced by L5 dimensions

`Node.Save()` gates on `device.write`. Any Node-level mutation that triggers a save at the end (the default for most write ops) requires the caller to hold `device.write` regardless of the specific permission the mutation itself gates on. The L5-dimensions scenario uses `?no_save=true` on the `bind-acl` steps so `intf-isaac` — who has `acl.modify` scoped to `interface: "Ethernet0"` but NOT `device.write` — can demonstrate the interface dimension cleanly.

In a production deployment with `--enforce-authorization` engaged, fine-grained per-interface or per-resource grants only let an operator MUTATE; they need `device.write` (broad or scoped) to PERSIST. Whether that's the right design or an artifact of L4 catch-all coverage is its own discussion (cross-cutting auth principle vs. operator ergonomics) — out of scope for this suite, but the pattern is worth knowing about when authoring grants for real deployments.

## Operator setup

### 1. Place the secret store on disk

```sh
cp newtrun/topologies/1node-vs-auth/specs/secrets.json /tmp/1node-vs-auth-secrets.json
chmod 600 /tmp/1node-vs-auth-secrets.json
```

### 2. Start `newt-server` with the full flag set

The L6-audit-verify scenario invokes `bin/newtron` as a subprocess
from inside `newt-server`. The subprocess inherits the server's
`$PATH`, so the operator must put `./bin` on PATH before starting
the server:

```sh
PATH="$(pwd)/bin:$PATH" bin/newt-server \
    --spec-dir newtrun/topologies/1node-vs-auth/specs \
    --net-id 1node-vs-auth \
    --secret-store /tmp/1node-vs-auth-secrets.json \
    --audit-log /tmp/1node-vs-auth-audit.jsonl \
    --audit-caller-header X-Newtron-Caller \
    --enforce-authorization \
    --audit-log-integrity \
    --spec-watch &
```

Flag-by-flag:

- `--secret-store` engages L0 `${secret:KEY}` resolution.
- `--audit-log` writes audit events to the named file (L1).
- `--audit-caller-header` accepts the `X-Newtron-Caller` HTTP header as
  the caller identity (L1 self-attested). Required for the suite —
  every mutation step sends this header.
- `--enforce-authorization` engages L3 + L4 gating. Without it, every
  step would 200 regardless of caller; the `expect_failure: true`
  assertions would all flip.
- `--audit-log-integrity` hash-chains every audit event so L6's
  `audit verify` has something to verify (L6).
- `--spec-watch` enables file-change-driven reload (L6 revocation half).
  Not exercised by suite scenarios; required for manual revoke testing.

### 3. Run the suite

```sh
bin/newtrun start 1node-vs-auth --no-deploy
```

`--no-deploy` skips the lab deployment — every scenario uses
`?mode=topology` so no SONiC VM is required.

## Expected outcome

All 6 scenarios pass on first run. If any fail:

- L3/L4 scenarios failing for an *allowed* caller → check
  `network.json` grants and operator's `--enforce-authorization` flag.
- L3/L4 scenarios *not* failing for `mallory` → enforcement isn't on.
  Verify `--enforce-authorization` was passed.
- L0 failing → check `--secret-store` path and that `secrets.json`
  has the `switch1_ssh_pass` key.
- L6 failing with "verified 0 entries" → `--audit-log-integrity` was
  not set, or `--audit-log` path doesn't match
  `/tmp/1node-vs-auth-audit.jsonl`.

## Manual verifications for the deferred items

### L2b PAM

Configure `/etc/pam.d/newtron-server` per `docs/newtron/pam-howto.md`,
restart `newt-server` with `--auth-pam-service=newtron-server` (without
`--audit-caller-header` so PAM is the only identity surface), and run:

```sh
# Wrong password: 401
curl -u alice:wrong -X POST http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-service \
    -d '{"name":"pam-test","type":"routed"}'

# Right password: caller verified via PAM, then L3 enforcement applies
curl -u alice:correct-password -X POST http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-service \
    -d '{"name":"pam-test","type":"routed"}'
```

### L6 spec-watch

With `--spec-watch` set, edit `network.json` to remove alice from
spec-team, save, and within ~1 second a fresh request as alice gets
403:

```sh
# Before edit: 201
curl -X POST -H "X-Newtron-Caller: alice" \
    http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-service \
    -d '{"name":"before-revoke","type":"routed"}'

# Edit network.json: remove "alice" from spec-team. Save.
# After ~1s debounce, watcher fires:
journalctl -u newt-server | tail
# spec-watcher: reloaded network '1node-vs-auth' after change at /path/to/specs

# After edit: 403
curl -X POST -H "X-Newtron-Caller: alice" \
    http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-service \
    -d '{"name":"after-revoke","type":"routed"}'
```

### L2c session-key round trip

Requires `newtron-server` (not `newt-server`, which has no PAM flag
today) running with both `--auth-pam-service` and a local OS account
to test against. Pattern:

```sh
# 1. Mint a key via PAM-backed login. The Basic-auth credentials are
#    whatever PAM accepts (local account / LDAP / etc.).
KEY=$(curl -sS -u alice:correct-password -X POST \
    http://localhost:18080/newtron/v1/auth/login \
    | jq -r .key)

# 2. Use the key on a real mutation. No password on the wire — just
#    the Bearer token + the verified identity it resolves to.
curl -sS -X POST -H "Authorization: Bearer $KEY" \
    http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-zone \
    -d '{"name":"zone-bearer"}'

# 3. Revoke explicitly.
curl -sS -X POST -H "Authorization: Bearer $KEY" \
    http://localhost:18080/newtron/v1/networks/1node-vs-auth/auth/logout
# 204 No Content

# 4. Same key now 401s.
curl -sS -X POST -H "Authorization: Bearer $KEY" \
    http://localhost:18080/newtron/v1/networks/1node-vs-auth/create-zone \
    -d '{"name":"zone-after-logout"}'
# 401 invalid or expired session key
```

The audit log carries `verification_source: "pam"` for the login
event and `verification_source: "session_key"` for every subsequent
gated request. Operators can join the two by user + time window
bounded by the key's `expires_at`.

### L6 tamper detection

Use a hex editor or `sed -i` to modify any field of a past audit
log entry. Run `bin/newtron audit verify /tmp/1node-vs-auth-audit.jsonl`.
Expected:

```
audit chain broken at line N: id hash mismatch (entry content modified)
```

Exit code 1.

## Related

- [`docs/newtron/auth-design.md`](../../../docs/newtron/auth-design.md) — the layered design every scenario references.
- [`docs/newtron/authorization-howto.md`](../../../docs/newtron/authorization-howto.md) — operator-facing howto for L3/L4/L5.
- [`docs/newtron/secret-store.md`](../../../docs/newtron/secret-store.md) — L0 ops.
