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
| `25-L2c-disabled-routes` | L2c | When the server runs without `--auth-pam-service`, `POST /auth/login` and `POST /auth/logout` return 404 — the disabled-path safety contract. Also pins that the new `sessionkey.Middleware` doesn't break header-mode identity: a request with `Authorization: Bearer <anything>` plus `X-Newtron-Caller: alice` still resolves as alice. |
| `26-L2c-round-trip` | L2c | The full session-key arc end-to-end: PAM-authenticated `/auth/login` mints a key; a mutation under `Authorization: Bearer <key>` succeeds; `/auth/logout` revokes; the same Bearer on the same mutation 401s. Requires PAM + a real OS account (see §"L2c round-trip operator setup"). Skipped by default — the suite's `alice_basic_auth` parameter is empty unless the operator supplies it. |
| `30-L4-node-mutations-gated` | L4 | Same shape on Node-level mutations (vlan.create, vrf.create, acl.create) via `?mode=topology`. |
| `40-L5-resource-scoping` | L5 | alice's `service.apply` grant scopes to `resource: "transit-*"`; she can apply transit-1, denied on vpn-east. bob's grant is the inverse. |
| `50-L6-audit-verify` | L6 | `bin/newtron audit verify /tmp/1node-vs-auth-audit.jsonl` walks the chain and confirms it's intact end-to-end. |
| `60-L3-permission-families` | L3 | Handler categories the original suite skips: super_user bypass (root sails through every check), profile/zone/topology mutations (gated on spec.author but routed through different handlers than services). |
| `70-L4-permission-families` | L4 | Node-mutation perm families the original suite skips: lag.create (create-portchannel), evpn.modify (add-bgp-evpn-peer, prerequisite setup-device included), interface.modify (interface set-property), device.write (setup-device denied for mallory). |
| `80-L5-dimensions` | L5 | The three `where` dimensions beyond `resource`: **field** (architects scoped to `!permissions,!user_groups,!super_users` matches services but not the grant table itself — the §3 criterion 9 meta-authorization separation), **interface** (intf-isaac scoped to `interface: "Ethernet0"` can bind ACLs on Eth0 but is denied on Eth4), **device** (dev-dora scoped to `device: switch1` is allowed; mallory still denied without any group). |

## What it deliberately does NOT cover

These verifications can't fit the current newtrun suite model:

- **L2a inter-service mTLS** — N/A for `newt-server` (single process, no inter-engine network calls).
- **L2b user-to-service PAM** — requires host PAM configuration (`/etc/pam.d/newt-server`) and a real OS account; the suite can forge `X-Newtron-Caller` but not real Basic-auth credentials. The `26-L2c-round-trip` scenario exercises a PAM-authenticated `/auth/login` and so does cover one L2b flow, but operator setup is required (see below).
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

## L2c round-trip operator setup

The `26-L2c-round-trip` scenario exercises a real PAM-authenticated
`/auth/login`, so it needs three things the rest of the suite does not:

**Single-mode workflow.** Per-step identity uses the runner's
multi-user session cache (`as: <user>` on each step picks up the
Bearer the operator pre-cached via `newtron auth login --user <user>`).
The full suite runs cleanly in PAM-only mode in one server
invocation; no header-mode/PAM-mode split is needed.

Two pieces wire this together:

1. `--auth-pam-service` on the server enables PAM verification at
   `/auth/login`.
2. `login-all.sh` (in this suite directory) is a small helper that
   logs in as every identity any scenario references via `as:`,
   pre-caching one Bearer per user in `~/.newtron/sessions/`. The
   operator submitting the run is one of those identities; the
   runner forwards their Bearer as the default credential and
   per-step `as:` switches to whichever cached user the scenario
   names.

Re-run `login-all.sh` after every newt-server restart — the
server-side session-key store is in-memory by design, so cached
Bearers go stale across restarts.

### 1. PAM service file

Create `/etc/pam.d/newtron-test`:

```
auth required pam_unix.so
account required pam_unix.so
```

(`pam_unix` authenticates against `/etc/passwd` / `/etc/shadow`; for
LDAP / SSSD / Kerberos see `docs/newtron/pam-howto.md` §2.)

### 2. OS accounts the suite knows

Every identity any scenario references via `as:` needs a real OS /
directory account the PAM stack can authenticate. The suite's
`network.json` grants are keyed by these names:

- `alice`, `dave` (spec-team)
- `bob`, `charlie` (ops)
- `arch-anna` (architects)
- `iam-ian` (iam-team)
- `intf-isaac` (intf-ops)
- `dev-dora` (device-team)
- `mallory` (no group — used for denial-path tests)
- `root` (super_user — bypasses every check)

Create them:

```sh
for u in alice dave bob charlie arch-anna iam-ian intf-isaac dev-dora mallory; do
    sudo useradd -m -s /usr/sbin/nologin "$u"
done
# pam_permit ignores passwords, so any string works
```

If using `pam_unix` instead of `pam_permit`, set a known password on
each account and pass it to `login-all.sh` via
`NEWTRON_TEST_PASSWORD=mypassword sh login-all.sh`.

### 3. Start `newt-server` in PAM-only mode

```sh
PATH="$(pwd)/bin:$PATH" bin/newt-server \
    --spec-dir newtrun/topologies/1node-vs-auth/specs \
    --net-id 1node-vs-auth \
    --secret-store /tmp/1node-vs-auth-secrets.json \
    --audit-log /tmp/1node-vs-auth-audit.jsonl \
    --auth-pam-service newtron-test \
    --enforce-authorization \
    --audit-log-integrity \
    --spec-watch &
```

- `--auth-pam-service` engages L2b PAM + auto-engages L2c session
  keys at `/newt-server/v1/auth/login` and `/newt-server/v1/auth/logout`.
- `--audit-caller-header` is **deliberately omitted** — every
  identity is now a real PAM-verified session, no self-attested
  header path needed.
- Adjust `--session-key-ttl` if you want a TTL other than the
  default 8h.
- The runner's identity is the operator's identity. Whoever
  submits the run (`bin/newtrun start ...` with `NEWTRON_USER`
  set, or any other consumer hitting `/newtrun/v1/runs`) carries
  an Authorization Bearer; the runner extracts it from the
  inbound request and attaches it on every outbound newtron call.
  Per-step `as: <user>` in a scenario switches to that user's
  cached Bearer for the one step (the multi-user session cache
  the operator populated via `login-all.sh`).

### 4. Pre-cache a session for every test identity

```sh
sh newtrun/suites/1node-vs-auth/login-all.sh
```

The helper logs in as alice, bob, mallory, and the rest of the cast,
producing one cache file per identity in `~/.newtron/sessions/`.
Override the password via `NEWTRON_TEST_PASSWORD=… sh login-all.sh`
if your PAM stack expects something other than the script's default.

**Re-run after every newt-server restart.** The server-side
session-key store is in-memory by design — restarts wipe it, and
cached Bearers go stale immediately. The next run will fail with
401s until the cache is refreshed.

### 5. Run the suite

```sh
bin/newtrun start 1node-vs-auth --no-deploy \
    --network-id 1node-vs-auth \
    --param alice_basic_auth=$(echo -n 'alice:thepassword' | base64)
```

All 11 scenarios pass in one server invocation. The `--target` form
still works to run a dependency chain:

```sh
bin/newtrun start 1node-vs-auth --no-deploy \
    --target L2c-round-trip \
    --param alice_basic_auth=$(echo -n 'alice:thepassword' | base64)
```

`--target` runs the dependency chain leading to the named scenario.
The full suite now passes 11/11 in PAM mode (per-step `as: <user>`
impersonation replaced the header-mode spoofing the scenarios used
before).

## Expected outcome

All 11 scenarios pass on first run. If any fail:

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
