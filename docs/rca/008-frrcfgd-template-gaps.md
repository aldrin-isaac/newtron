# RCA-008: frrcfgd has no template support for certain BGP globals

**Status**: RESOLVED

> **Note (Mar 2026):** The `ApplyFRRDefaults` mechanism referenced in this RCA was eliminated when frrcfgd unified mode was adopted. FRR configuration is now handled by `frrcfgd` (unified mode) with boot patches that cover the template gaps described here.

## Symptom

Setting `ebgp_requires_policy` and `suppress_fib_pending` in CONFIG_DB's
BGP_GLOBALS table had no effect. FRR running config still showed the
defaults (ebgp policy required, fib pending suppression active), causing
eBGP sessions to reject routes and route installation to be delayed.

## Root Cause

The `frrcfgd` Jinja2 templates for BGP_GLOBALS simply don't have template
variables for these fields. Unlike the BGP_NEIGHBOR fields (RCA-002) where
the names were different, these fields have **no template support at all**.
Writing them to CONFIG_DB is silently ignored because frrcfgd's template
never reads them.

Inspecting `/usr/local/sonic/frrcfgd/bgpd.conf.j2` confirmed that the
template only supports a subset of BGP global configuration knobs. The
missing features must be applied directly to FRR via `vtysh`.

## Impact

- eBGP sessions rejected all routes due to default `ebgp-requires-policy`
- Route installation delayed by `suppress-fib-pending` waiting for
  hardware ACK that never comes in a virtual environment

## Fix

Initially worked around via an `ApplyFRRDefaults()` method that applied settings
directly via `vtysh` after BGP container restarts. This mechanism was later
eliminated when frrcfgd unified mode was adopted.

**Current approach (frrcfgd unified mode):** The boot patch
(`patches/ciscovs/always/00-frrcfgd-mode.json`) sets `docker_routing_config_mode=unified`
and `frr_mgmt_framework_config=true`. In unified mode, frrcfgd generates the full
FRR configuration from CONFIG_DB, including `no bgp ebgp-requires-policy` and
`no bgp suppress-fib-pending` when the corresponding CONFIG_DB fields are set.
The template gaps that originally caused this issue are covered by the patched
`frrcfgd.py.tmpl`.

## Lesson

SONiC's CONFIG_DB is not a complete configuration interface in all modes —
some frrcfgd templates lack support for certain fields. When a CONFIG_DB write
has no effect, check both the field name (RCA-002) and whether the template
supports the field at all. The adoption of frrcfgd unified mode with patched
templates resolved this class of issues for newtron. For deployments still using
split mode, `vtysh` remains the escape hatch, but those settings won't survive
a `config reload` (they need to be reapplied after each BGP restart).
