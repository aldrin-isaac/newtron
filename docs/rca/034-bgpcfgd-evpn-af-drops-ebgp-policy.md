# RCA-034: bgpcfgd Drops `no bgp ebgp-requires-policy` When L2VPN EVPN AF Is Added

**Status**: RESOLVED

**Note (Feb 2026):** The `ApplyFRRDefaults` mechanism has been eliminated. FRR configuration is now managed by `frrcfgd` (unified mode) via a patched `frrcfgd.py.tmpl`. The timing and idempotency issues described here no longer apply. The `2node-incremental` suite has been replaced by `2node-primitive`.

**Component**: bgpcfgd (FRR config daemon), `newtrun/suites/2node-primitive/08-evpn-setup.yaml`
**Affected**: CiscoVS 202505 builds; any topology that runs setup-evpn after provision
**Discovered**: 2026-02-19

---

## Symptom

After running `setup-evpn` (which adds `BGP_NEIGHBOR_AF|l2vpn_evpn` to CONFIG_DB),
BGP routes on switch2 show `(Policy)` annotation, and the overlay BGP session on switch1
enters `Connect` state:

```
switch2# show bgp summary
Neighbor     V  AS    MsgRcvd MsgSent  Up/Down  State/PfxRcd
10.0.0.1     4  65001  12      10      00:00:30  Connect     (Policy)
```

`device-health` fails:

```
FAIL  switch1: overall failed (2 passed, 1 failed)
  bgp_check: FAIL — peer 10.0.0.2 state=Connect (expected Established)
```

---

## Root Cause

bgpcfgd monitors CONFIG_DB and regenerates the entire FRR BGP config (`frr.conf`)
whenever a BGP-related table changes. When `setup-evpn` writes `BGP_NEIGHBOR_AF` entries
for `l2vpn_evpn`, bgpcfgd triggers a full config regeneration.

The regenerated config is rendered from bgpcfgd's Jinja2 templates. On CiscoVS 202505,
these templates do **not** include `no bgp ebgp-requires-policy` even when
`BGP_GLOBALS|default.ebgp_requires_policy = "false"` is set in CONFIG_DB:

```
# BGP_GLOBALS has:
# ebgp_requires_policy = "false"

# But regenerated frr.conf has:
# router bgp 65002
#   bgp log-neighbor-changes
#   no bgp suppress-fib-pending
#   ! ← NO "no bgp ebgp-requires-policy" here
```

FRR's default behaviour is to require explicit route-maps on all eBGP sessions
(`bgp ebgp-requires-policy`). Without `no bgp ebgp-requires-policy`, FRR rejects
all route advertisements from eBGP peers until route-maps are configured.

`apply-frr-defaults` (called during provision) writes `no bgp ebgp-requires-policy`
via vtysh, but bgpcfgd's subsequent regeneration overwrites this when `setup-evpn` runs.

### bgpcfgd Regeneration Delay

bgpcfgd processes CONFIG_DB changes asynchronously. On CiscoVS with NGDP, regeneration
takes **30–40 seconds** after the CONFIG_DB change (observed: frr.conf modified ~34s
after the `BGP_NEIGHBOR_AF` write). This delay is longer than the time between
`evpn-setup` (test 08) and `device-health` (test 15) in some runs.

---

## Why This Wasn't Caught by bgp-converge

`device-health` requires `bgp-converge` (test 02). By the time `bgp-converge` passes,
`setup-evpn` (test 08) has not yet run. The sequence is:

```
01-provision       → apply-frr-defaults sets "no bgp ebgp-requires-policy"
02-bgp-converge    → BGP converges with policy disabled ✓
...
08-evpn-setup      → bgpcfgd regenerates frr.conf, drops the policy setting
   (bgpcfgd takes ~34s to regenerate; tests 09-14 run in the meantime)
15-device-health   → BGP in Connect/(Policy) state ✗
```

`device-health` sees the broken BGP state because it only depends on `bgp-converge`
(which passed at step 02), not on `evpn-setup`.

---

## Fix

Add `apply-frr-defaults` to `08-evpn-setup.yaml` after `setup-evpn`, with a 40-second
wait to allow bgpcfgd to finish its config regeneration first:

```yaml
  # bgpcfgd regenerates frr.conf when BGP_NEIGHBOR_AF|l2vpn_evpn is written to
  # CONFIG_DB. The regenerated template drops 'no bgp ebgp-requires-policy'
  # (bgpcfgd template limitation, see RCA-034). Wait for regeneration to complete,
  # then re-apply FRR defaults.
  - name: wait-bgpcfgd-regenerate
    action: wait
    duration: 40s

  - name: reapply-frr-defaults
    action: apply-frr-defaults
    devices: all
```

The 40-second wait ensures bgpcfgd has finished regenerating before `apply-frr-defaults`
re-writes `no bgp ebgp-requires-policy` via vtysh.

---

## Why `ebgp_requires_policy: false` in CONFIG_DB Is Ignored

The bgpcfgd frrcfgd template for CiscoVS 202505 branch does not translate
`BGP_GLOBALS.ebgp_requires_policy = "false"` into `no bgp ebgp-requires-policy`
in the rendered frr.conf. This is a known gap in the frrcfgd template compared
to the standard SONiC VS builds.

The upstream fix would be to add the translation to the frrcfgd template:

```jinja
{% if bgp_globals.ebgp_requires_policy == 'false' %}
 no bgp ebgp-requires-policy
{% endif %}
```

Until this is fixed upstream, the workaround is to re-apply via vtysh after each
bgpcfgd regeneration event.

---

## Affected Scenarios

Any scenario that calls `setup-evpn` followed by a BGP state check:
- `08-evpn-setup.yaml` → `15-device-health.yaml` (direct dependency chain)

Scenarios that don't check BGP state after EVPN setup are unaffected.

---

## Verification

After fix, `device-health` passes with all BGP sessions in Established state:

```
PASS  switch1: overall passed (3/3)
  bgp_check: PASS — all peers Established
PASS  switch2: overall passed (3/3)
  bgp_check: PASS — all peers Established
```
