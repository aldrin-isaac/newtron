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

## Validation

```sh
# 1. newt-server under enforcement (both aldrin and ron are super-users):
PATH="$PWD/bin:$PATH" bin/newt-server \
  --enforce-authorization --auth-pam-service newtron-test \
  --audit-log /tmp/newt-audit.log --audit-log-integrity \
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
