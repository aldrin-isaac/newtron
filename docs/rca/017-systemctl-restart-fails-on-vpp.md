# RCA-017: systemctl restart fails on SONiC VPP (write_standby.py)

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

Changed `RestartService()` in `pkg/device/device.go` from `systemctl restart`
to `docker restart`. Docker restart directly manages the container lifecycle
without going through systemd's ExecStopPost scripts.

```go
// Before:
output, err := d.tunnel.ExecCommand(fmt.Sprintf("sudo systemctl restart %s", name))

// After:
output, err := d.tunnel.ExecCommand(fmt.Sprintf("sudo docker restart %s", name))
```

This is consistent with the existing workaround documented in project memory:
"config reload breaks VPP syncd — use docker restart bgp instead."

## Lesson

Prefer `docker restart` over `systemctl restart` for SONiC service management.
systemd units in SONiC carry platform-specific hooks (Dual-ToR, warm reboot)
that may not work on all deployment types. Docker restart provides a reliable,
platform-agnostic alternative.
