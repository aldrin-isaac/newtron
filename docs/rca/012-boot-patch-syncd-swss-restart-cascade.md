# RCA-012: Boot patch syncd restart cascades into swss failure

## Symptom

After boot patch `02-port-config.json` applied its `post_commands`, the `swss`
container entered `start-limit-hit` and refused to start. The `syncd` container
restarted successfully, but swss was permanently stuck.

Additionally, using `docker restart bgp` (as documented in RCA-001) caused the
same `start-limit-hit` failure because it bypassed systemd service management.

## Root Cause

Two related issues:

**1. syncd/swss dependency cascade:**
SONiC's `swss` container uses `docker-wait-any-rs` to monitor `syncd`. When
`systemctl restart syncd` stops syncd, docker-wait-any-rs detects the exit and
swss stops itself. If swss restarts before syncd is fully ready, it fails again,
triggering systemd's rate-limiting (`start-limit-hit`).

**2. docker restart bypasses systemd:**
`docker restart <service>` operates at the Docker API level, bypassing systemd
entirely. SONiC's `docker-rs` process detects the Docker-level restart as a
service failure, and systemd's restart counter increments. After enough "failures",
systemd applies `start-limit-hit` and refuses further restarts.

## Impact

- Boot patch application left VMs with non-functional dataplane (no swss/syncd)
- BGP container restarts during provisioning could kill the service permanently
- Required full VM reboot to recover from start-limit-hit state

## Fix

**Boot patch post_commands** (`patches/vpp/always/02-port-config.json`):
```json
"post_commands": [
    "sudo config save -y",
    "sudo systemctl restart syncd",
    "sleep 10",
    "sudo systemctl reset-failed swss || true",
    "sudo systemctl restart swss"
]
```

Key elements: sleep between syncd and swss restarts, `reset-failed` to clear
systemd's rate-limit counter before restarting swss.

**Service restarts during provisioning** (`pkg/newtron/device/sonic/device.go`):
Changed `RestartService` from `docker restart` to `systemctl restart` to work
with systemd's service management rather than against it.

## Lesson

On SONiC, always use `systemctl restart` instead of `docker restart` for
container services. Docker-level operations bypass systemd's lifecycle tracking,
causing false failure detection. When restarting dependent services (syncd →
swss), add explicit delays and `reset-failed` to prevent cascade failures.
