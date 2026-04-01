# RCA-046: sonic-vs config reload crashes swss/syncd

## Summary

On sonic-vs (202505, Force10-S6000), `config reload -y` can leave swss (orchagent)
and syncd in a permanently exited state. Once down, they do not auto-restart and
`config reload` refuses to run because it requires SwSS uptime > 120s.

## Symptom

After Reconcile (which calls `config reload -y`):
- `swss` container: `Exited (0)`
- `syncd` container: `Exited (0)`
- No Ethernet kernel interfaces (orchagent creates them from PORT table)
- No ASIC_DB programming (VLAN SAI objects, bridge port members)
- Redis CONFIG_DB still accessible (database container stays up)
- `config reload -y` fails: "SwSS container is not ready. Retry later"

Newtron's Reconcile succeeds (ReplaceAll writes to Redis) because the database
container is up. But without orchagent, CONFIG_DB entries are never programmed
to the ASIC.

## Root Cause

`config reload -y` on sonic-vs runs `systemctl restart` on all SONiC service
containers. On the 202505 build with Force10-S6000 HWSKU, swss and syncd
sometimes exit during this process and their systemd units don't restart them.

The `config reload` command checks SwSS uptime > 120s before proceeding (to
avoid reloading during initial startup). When swss is down, this check fails
permanently — creating a chicken-and-egg loop where config reload needs swss
running, but swss is down because of a previous config reload.

## Impact

- Reconcile appears to succeed (200 response, ReplaceAll writes OK)
- All subsequent operations that depend on ASIC programming fail
- Tests that verify ASIC_DB or kernel interface state fail
- Tests that use SSH commands on Ethernet interfaces fail

## Workaround

Manual restart of swss and syncd:
```bash
sudo systemctl restart syncd
sudo systemctl restart swss
```

Wait ~60s for orchagent to process PORT table entries and create kernel interfaces.

## Resolution Path

Options (not yet implemented):
1. **Skip config reload on sonic-vs**: Platform-specific flag to skip config reload.
   Rely on ReplaceAll + daemon runtime notifications for CONFIG_DB processing.
2. **Post-reload health check**: After config reload (success or failure), verify
   swss is running. If not, restart it explicitly.
3. **Force flag**: Use `config reload -y -f` to skip the SwSS readiness check.
   Risk: may cause further issues if SwSS is genuinely not ready.

## Affected Platforms

- sonic-vs 202505 (Force10-S6000)
- NOT observed on CiscoVS (different container orchestration)

## Related

- RCA-001: config reload breaks VPP syncd (same class of issue, different platform)
- RCA-045: sonic-vs frrcfgd workarounds (frrcfgd runtime notifications don't work)
