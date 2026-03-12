# RCA-017: systemctl restart fails on SONiC VPP (write_standby.py)

**Note (Feb 2026):** The advice to use `docker restart bgp` applies to VPP only. On CiscoVS, SONiC systemd manages containers — use `systemctl restart bgp` instead. `docker restart` on CiscoVS kills the container and systemd does not bring it back. See frrcfgd boot patch (`patches/ciscovs/always/00-frrcfgd-mode.json`).

## Symptom

`systemctl restart bgp` fails on SONiC VPP images with exit code 1, even
though the BGP container itself restarts successfully. The service enters
"failed" state and subsequent `systemctl restart` attempts fail until a
`systemctl reset-failed bgp` is issued.

## Root Cause

SONiC's systemd unit for BGP includes an `ExecStopPost` script
(`/usr/local/bin/write_standby.py --shutdown bgp`) designed for Dual-ToR
deployments. On non-Dual-ToR systems (including all VPP/VS images), this
script exits with status 1, which systemd interprets as a service failure.

The `ExecStopPost` failure masks the fact that the container stopped and
started correctly.

## Impact

- `RestartService("bgp")` via `systemctl` fails on all VPP/VS deployments
- Test suites using `restart-service` step action fail at the provisioning stage
- Cascading failure: all scenarios depending on `provision` are skipped

## Fix

Changed `RestartService()` in `pkg/newtron/network/node/node.go` from `systemctl restart`
to `docker restart`. Docker restart directly manages the container lifecycle
without going through systemd's ExecStopPost scripts.

```go
// Before:
output, err := n.Tunnel().ExecCommand(fmt.Sprintf("sudo systemctl restart %s", name))

// After:
output, err := n.Tunnel().ExecCommand(fmt.Sprintf("sudo docker restart %s", name))
```

This is consistent with the existing workaround documented in project memory:
"config reload breaks VPP syncd — use docker restart bgp instead."

## Lesson

Service restart behavior is platform-specific:

- **VPP:** Use `docker restart` — `systemctl restart` triggers `write_standby.py`
  failure (this RCA). `docker restart` bypasses systemd hooks.
- **CiscoVS:** Use `systemctl restart` — systemd manages the container lifecycle.
  `docker restart` kills the container and systemd does not bring it back.

When adding service restart calls, ensure the code path selects the correct
method for the platform.
