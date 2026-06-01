# RCA-035: CiscoVS vlanmgrd crashes at boot — missing MAC in DEVICE_METADATA

**Date:** 2026-02-19
**Platform:** SONiC CiscoVS (cisco-p200-32x100-vs, Palladium2)
**Image:** sonic-vs.img.gz from ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1
**Affected Component:** VLAN manager (vlanmgrd), VXLAN manager (vxlanmgrd)
**Affected Scenarios:** vlan-l2-bridge (L2 bridging over VLAN interfaces)
**Status:** RESOLVED (Feb 2026)

---

**Resolution (Feb 2026, refined May 2026):** This problem is solved by merge-based
`ReplaceAll` — `ConfigDBClient.ReplaceAll()` only deletes stale keys (present in DB
but absent from composite). Factory defaults — including MAC — survive provisioning
even when the composite doesn't explicitly set `DEVICE_METADATA|localhost.mac`.

The original Feb 2026 fix also included profile-based MAC generation via newtlab's
`PatchProfiles()`. That mechanism was removed in May 2026 along with all of
`pkg/newtlab/profile.go` (runtime profile mutation was a §27 violation — newtlab
writing into spec files newtron read). The merge-based `ReplaceAll` alone is
sufficient to keep the factory MAC in CONFIG_DB across provisioning, so the
PatchProfiles removal did not regress the vlanmgrd-no-MAC failure mode.

## Problem

`vlanmgrd` (the SONiC VLAN manager daemon) crashes immediately at boot on CiscoVS with error:
```
Runtime error: couldn't find MAC address of the device from config DB
```

After the initial crash, supervisord retries 4 times and then marks vlanmgrd as FATAL, stopping further restart attempts. This prevents L2 VLAN bridging from working for the remainder of the test.

`vxlanmgrd` also crashes with the same error (shares the same MAC lookup code path).

## Symptom

- `docker logs swss | grep vlan` shows 4 crash attempts followed by FATAL status
- `docker exec swss supervisorctl status vlanmgrd` returns FATAL
- L2 VLAN bridging is completely non-functional (no kernel VLAN interfaces, no VLAN entries in APP_DB or ASIC_DB)
- `redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'` shows no `mac` field

## Root Cause

The issue was a three-part failure:

### 1. Factory Image Incompleteness

The factory CiscoVS image contains the system MAC only in the persistent `/etc/sonic/config_db.json` file. The initial CONFIG_DB load may not reliably copy the MAC to Redis on all platforms.

### 2. Old Provisioning Wiped MAC Field

The old `ReplaceAll()` deleted ALL existing keys in touched tables before writing composite entries. Since the composite's `DEVICE_METADATA|localhost` entry didn't include a `mac` field, the factory MAC was permanently erased from Redis.

### 3. vlanmgrd Cannot Start Without MAC

vlanmgrd queries Redis CONFIG_DB for `DEVICE_METADATA|localhost.mac` at startup. Without it, the daemon crashes. It needs the MAC to set the bridge MAC address for all kernel VLAN interfaces.

## Why the MAC Is Required

vlanmgrd needs the bridge MAC address for correct L2 forwarding:
- ARP replies on VLAN interfaces must come from the device's MAC
- Bridged frames need correct source MAC for hardware forwarding
- STP requires consistent bridge identifiers

## Fix

### Current Solution (May 2026)

**Merge-based ReplaceAll** (`pkg/newtron/device/sonic/pipeline.go`):
- `ReplaceAll()` only deletes keys NOT present in the composite (stale keys)
- Factory defaults survive because HSet merges composite fields on top of existing keys
- Factory MAC stays in CONFIG_DB across provisioning — vlanmgrd starts cleanly

**Note on the deprecated PatchProfiles fix:** The Feb 2026 fix also included
`pkg/newtlab/profile.go`'s `PatchProfiles()`, which wrote a deterministic
`GenerateMAC(name, 0)` value into each device profile JSON at deploy time. That
mechanism was removed in May 2026 — runtime profile mutation was a §27 violation
(newtlab writing into spec files newtron read). Removing it did not regress this
RCA's failure mode: the merge-based `ReplaceAll` is independently sufficient.

**Config reload before provision** (`pkg/newtron/network/topology.go`):
- Best-effort `config reload -y` before composite delivery restores CONFIG_DB to saved baseline
- Ensures a clean starting point (no stale fields from previous provisions)

### Historical Workarounds (Deprecated)

The original fix used `ReadSystemMAC()` — an SSH call to read `/etc/sonic/config_db.json`
and inject the MAC into the composite before delivery. This method and the corresponding
boot patch `02-inject-mac.json` have been deleted. They are no longer needed because the
profile-based MAC and merge-based ReplaceAll solve the problem structurally.

## Affected Platforms

- **CiscoVS:** Confirmed affected (factory image has this defect)
- **VPP:** Unknown (different VLAN path)
- **Other SONiC platforms:** Potentially affected if they use CompositeOverwrite and have platform-initialized MAC only in the persistent file

## Lesson Learned

Critical factory metadata (MAC, hostname, platform) must survive provisioning. The correct fix is structural:
1. Generate and include the MAC in the composite (profile-based flow)
2. Use merge-based delivery that preserves factory defaults (ReplaceAll only deletes stale keys)

Runtime SSH hacks to read and re-inject factory values are fragile workarounds, not solutions.
