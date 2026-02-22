# RCA-001: config reload breaks VPP syncd

## Symptom

After running `config reload -y` on a SONiC-VPP device, the `syncd` container
enters a FATAL restart loop and never recovers. The device becomes unusable —
all dataplane and control plane services fail.

## Root Cause

`config reload -y` flushes the entire CONFIG_DB and restarts **all** services,
including `syncd`. In SONiC-VPP, the syncd container relies on VPP dataplane
state that cannot survive a full restart. When syncd restarts from scratch, it
attempts to re-initialize VPP but crashes because the VPP process state is
inconsistent with a fresh syncd init sequence.

This is specific to SONiC-VPP — standard SONiC-VS does not have a real syncd
and tolerates config reload.

## Impact

- Phase 2 (Provision) blocked: the newtron `-s` flag triggered `config reload`
  after writing CONFIG_DB entries, crashing the device every time.
- Required manual VM restart to recover.

## Fix

Changed the newtron `-s` flag behavior in `pkg/newtron/network/node/node.go` from
`config reload -y` to `config save -y` followed by `docker restart bgp`.
This only restarts the BGP container (which is what needs the config applied)
without touching syncd or other services.

```go
// Before (broken for VPP):
// ssh: "sudo config reload -y"

// After (safe for all platforms):
// ssh: "sudo config save -y"
// ssh: "sudo systemctl restart bgp"
```

**Update (RCA-012):** The original fix used `docker restart bgp`, which was
later found to bypass systemd and trigger `start-limit-hit`. The correct
approach is `systemctl restart bgp`. See RCA-012 for details.

## Lesson

Never use `config reload` on SONiC-VPP. Prefer targeted service restarts
(`systemctl restart <service>`) over full config reloads. Always use
`systemctl` instead of `docker restart` to work with SONiC's systemd service
management. When writing a provisioning tool that supports multiple SONiC
platforms, assume the most fragile platform and use the safest restart strategy.
