# RCA-008: frrcfgd has no template support for certain BGP globals

**Status**: RESOLVED

> **Note (Feb 2026):** The `ApplyFRRDefaults` mechanism has been eliminated. FRR configuration is now handled by `frrcfgd` (unified mode) with a patched `frrcfgd.py.tmpl` that includes `newtron-vni-poll`. The timing issues described here no longer apply — frrcfgd handles FRR config synchronization automatically.

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

Added an `ApplyFRRDefaults()` method in the device layer that applies
these settings directly via `vtysh` after the BGP container restarts:

```go
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
    cmds := []string{
        "configure terminal",
        "router bgp " + asn,
        "no bgp ebgp-requires-policy",
        "no bgp suppress-fib-pending",
        "end",
        "write memory",
    }
    return n.RunVtysh(cmds)
}
```

This is called after `docker restart bgp` during provisioning.

## Lesson

SONiC's CONFIG_DB is not a complete configuration interface — some features
must be applied via `vtysh` directly. When a CONFIG_DB write has no effect,
check both the field name (RCA-002) and whether the template supports the
field at all. For unsupported fields, `vtysh` is the escape hatch, but
these settings won't survive a `config reload` (they need to be reapplied
after each BGP restart).
