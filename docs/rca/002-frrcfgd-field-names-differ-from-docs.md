# RCA-002: frrcfgd field names differ from SONiC documentation

## Symptom

BGP neighbors configured via CONFIG_DB with field names from SONiC
documentation (`activate`, `route_reflector_client`, `next_hop_self`) were
silently ignored. FRR running config showed no corresponding configuration
applied — neighbors had no address family activation, no RR client setting,
and no next-hop-self.

## Root Cause

The `frrcfgd` daemon (which translates CONFIG_DB entries to FRR configuration)
uses **different field names** than what SONiC's public documentation and CLI
suggest. The actual field names are defined in Jinja2 templates at
`/usr/local/sonic/frrcfgd/` inside the BGP container:

| Documented Name | Actual frrcfgd Name | Table |
|----------------|---------------------|-------|
| `activate` | `admin_status` | BGP_NEIGHBOR_AF |
| `route_reflector_client` | `rrclient` | BGP_NEIGHBOR_AF |
| `next_hop_self` | `nhself` | BGP_NEIGHBOR_AF |

frrcfgd reads CONFIG_DB entries and renders them through Jinja2 templates.
If a field name doesn't match what the template expects, it is silently
ignored — no error, no warning in logs.

## Impact

- BGP overlay sessions failed to establish (no address family activation)
- Route reflector clients were not configured (no `rrclient` field)
- Required manual inspection of frrcfgd templates to discover correct names

## Fix

Updated all CONFIG_DB field names in the newtron topology provisioner
(`pkg/network/topology.go`) to use the actual frrcfgd-expected names:

```go
// BGP_NEIGHBOR_AF entries
"admin_status": "true",   // not "activate"
"rrclient":    "true",    // not "route_reflector_client"
"nhself":      "true",    // not "next_hop_self"
```

## Lesson

SONiC's CONFIG_DB field names are defined by the consuming daemon's templates,
not by documentation or CLI output. When a CONFIG_DB write has no visible
effect, read the Jinja2 templates in the consuming container to find the
actual expected field names:

```bash
docker exec bgp ls /usr/local/sonic/frrcfgd/
docker exec bgp cat /usr/local/sonic/frrcfgd/bgpd.conf.j2
```
