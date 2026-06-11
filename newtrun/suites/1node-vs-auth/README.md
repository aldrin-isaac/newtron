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
| `30-L4-node-mutations-gated` | L4 | Same shape on Node-level mutations (vlan.create, vrf.create, acl.create) via `?mode=topology`. |
| `40-L5-resource-scoping` | L5 | alice's `service.apply` grant scopes to `resource: "transit-*"`; she can apply transit-1, denied on vpn-east. bob's grant is the inverse. |
| `50-L6-audit-verify` | L6 | `bin/newtron audit verify /tmp/1node-vs-auth-audit.jsonl` walks the chain and confirms it's intact end-to-end. |

## What it deliberately does NOT cover

These verifications can't fit the current newtrun suite model:

- **L2a inter-service mTLS** — N/A for `newt-server` (single process, no inter-engine network calls).
- **L2b user-to-service PAM** — requires host PAM configuration (`/etc/pam.d/newtron-server`) and a real OS account; the suite can forge `X-Newtron-Caller` but not real Basic-auth credentials.
- **L6 spec-watch** — requires editing `network.json` mid-suite to observe auto-reload. There's no `local-exec` step action today (deferred follow-up).
- **L6 audit tamper detection** — requires modifying a log entry mid-suite to confirm verify catches it. Same `local-exec` gap.

Operators verify these manually with a small shell session; pattern in §5 below.

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
