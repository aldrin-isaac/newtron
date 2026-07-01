# 1node-vs-auth-deployed

Auth-enforced regression guard for the **newtron→newtlab cross-engine
call** (#335), run **actuated** against a deployed device.

## What it guards, and why the loopback suites can't

`newt-server` runs newtron, newtrun, and newtlab in one process, but they
reach each other over **HTTP loopback** through the shared PAM-gated
listener — not in-process Go calls. So when authorization is enforced,
newt-server's own cross-engine calls need a credential; #335 gave it an
internal service token (`sessionkey.Store.MintService`), and named the
`newt-server` service identity a global super-user.

The canonical failure #335 fixed: with auth on, `newtron switch1 init`
(or any device op) failed at `newtlab ... (401): authentication required`
— newtron's internal request to newtlab for switch1's SSH port was
unauthenticated.

`1node-vs-auth` proves every L0–L6 gate, but **every** scenario there uses
`?mode=topology`, deliberately firing the permission gate *before* any
device transport. It therefore never opens a device connection and never
makes the cross-engine call. Nothing in loopback can regress-guard #335.

This suite runs its scenarios **without** `?mode=topology`, against a
deployed lab, so newtron must resolve switch1's SSH port from newtlab
under enforcement. A regressed service token surfaces immediately as a
401 on that internal lookup.

## Scenarios

| # | Scenario | As | Asserts |
|---|----------|-----|---------|
| 00 | `cross-engine-ssh-reachable` | ron (super-user) | actuated `ssh-command echo ok` round-trips — the internal newtlab port lookup authenticated |
| 10 | `cross-engine-health-read` | ron | actuated `GET /health` connects and produces `oper_checks` (same cross-engine path, native op) |
| 20 | `enforcement-live-ungranted-denied` | mallory (ungranted) | actuated device write is **denied** — enforcement is live, so 00/10 aren't passing vacuously |

The suite is non-mutating on the device (00/10 read; 20 is denied before
transport), so it's safe to run against an already-provisioned switch1.

## Identities: why `ron` and `mallory` are nologin accounts

The suite drives two PAM identities, and **both are `nologin` OS accounts
by design**:

| Identity | Role | Shell |
|----------|------|-------|
| `ron`     | global super-user (listed in `--super-users`); drives the positive cross-engine scenarios 00/10 | `/usr/sbin/nologin` |
| `mallory` | authenticated but ungranted; scenario 20 expects her actuated write to be denied | `/usr/sbin/nologin` |

Neither is a human operator — they exist only to authenticate to the
newtron API through PAM (HTTP Basic → the `--auth-pam-service` stack).
Making them `nologin` **decouples "can authenticate to newtron" from "can
get a shell on the host"**: the same credential a scenario uses to obtain
a Bearer must not also grant interactive host access. A service/test
identity that could `ssh` into the box would widen the blast radius of a
leaked test password far beyond the API surface the suite exercises.

**Dev PAM vs real PAM.** The dev service `newtron-test` is `pam_permit.so`
— it accepts any user/password *without consulting the OS account
database*, so the scenarios pass even if these accounts don't exist. But a
real deployment authenticates against `pam_unix`/`pam_sss`, where each
identity must be a real OS account with a password. Create them `nologin`
so the account is usable for PAM auth yet never for login:

```sh
# Super-user service account (password matches the memory: ron:ronthenewt).
sudo useradd --shell /usr/sbin/nologin --comment 'newtron super-user (service account)' ron
echo 'ron:ronthenewt' | sudo chpasswd

# Ungranted negative-test identity.
sudo useradd --shell /usr/sbin/nologin mallory
echo 'mallory:test123' | sudo chpasswd
```

Under the dev `pam_permit` service the passwords above are ignored; under a
real PAM module they must match what `login-users.sh` sends (set
`NEWTRON_TEST_PASSWORD`, or give both accounts the same password). `ron`'s
super-user power comes from `--super-users ron` on newt-server, **not** from
the OS account — the account only carries the PAM credential.

## Validation

```sh
# 0. Ensure the ron + mallory nologin accounts exist (see "Identities"
#    above). Skippable only under the dev pam_permit service, which
#    doesn't consult the OS account database.

# 1. newt-server under enforcement (both aldrin and ron are super-users):
PATH="$PWD/bin:$PATH" bin/newt-server \
  --enforce-authorization --auth-pam-service newtron-test \
  --audit --audit-integrity \
  --super-users aldrin,ron --dev-superuser=false --spec-watch &

# 2. Deploy the lab (registers newtron network id "1node-vs"):
bin/newtlab deploy 1node-vs --monitor

# 3. Cache ron + mallory sessions (dev PAM accepts any password):
sh networks/1node-vs/suites/1node-vs-auth-deployed/login-users.sh

# 4. Run actuated (NO --no-deploy):
NEWTRON_USER=ron bin/newtrun start 1node-vs-auth-deployed
```

Expected: **3 scenarios — 3 passed**. `NEWTRON_USER=ron` gives the runner
a valid Bearer to reach the PAM-gated `/newtrun/v1/runs` endpoint; each
scenario's `as:` overrides the identity for its own newtron calls.
