# RCA-035: CiscoVS vlanmgrd crashes at boot — missing MAC in DEVICE_METADATA

**Date:** 2026-02-19
**Platform:** SONiC CiscoVS (cisco-p200-32x100-vs, Palladium2)
**Image:** sonic-vs.img.gz from ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1
**Affected Component:** VLAN manager (vlanmgrd), VXLAN manager (vxlanmgrd)
**Affected Scenarios:** vlan-l2-bridge (L2 bridging over VLAN interfaces)

---

**Note (Feb 2026):** The manual MAC injection workaround described here has been superseded by the boot patch `02-inject-mac.json` (in `pkg/newtlab/patches/ciscovs/always/`), which injects the MAC address at deploy time before SONiC containers start. The `device-init` step in test suites handles this automatically.

## Problem

`vlanmgrd` (the SONiC VLAN manager daemon) crashes immediately at boot on CiscoVS with error:
```
Runtime error: couldn't find MAC address of the device from config DB
```

After the initial crash, supervisord retries 4 times and then marks vlanmgrd as FATAL, stopping further restart attempts. This prevents L2 VLAN bridging from working for the remainder of the test.

`vxlanmgrd` also crashes with the same error (shares the same MAC lookup code path).

## Symptom

- `docker logs swss | grep vlan` shows 4 crash attempts followed by FATAL status:
  ```
  2026-02-19 10:34:21.123456789Z INFO vlanmgrd: vlanmgrd started
  2026-02-19 10:34:21.234567890Z ERROR vlanmgrd: Runtime error: couldn't find MAC address of the device from config DB
  [repeated 3 more times...]
  2026-02-19 10:34:25.567890123Z FATAL vlanmgrd: Exited too quickly (process log may have details)
  ```

- `docker exec swss supervisorctl status vlanmgrd` returns:
  ```
  vlanmgrd                         FATAL   Exited too quickly (process log may have details)
  ```

- L2 VLAN bridging is completely non-functional:
  - No kernel VLAN interfaces created (e.g., no `vlan100`)
  - No VLAN entries in APP_DB or ASIC_DB
  - ARP fails between hosts on the same VLAN with "Destination Host Unreachable"

- `redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'` shows no `mac` field:
  ```
  1) "hostname"
  2) "switch1"
  ```

- `/etc/sonic/config_db.json` (the factory config file) **does contain** the MAC address:
  ```json
  "DEVICE_METADATA": {
    "localhost": {
      "mac": "22:42:12:0a:c1:5f",
      "hostname": "switch1"
    }
  }
  ```

## Root Cause

The issue is a three-part failure:

### 1. Factory Image Incompleteness

The factory CiscoVS image contains the system MAC only in the persistent `/etc/sonic/config_db.json` file, **not** in the in-memory CONFIG_DB (Redis DB 4) that vlanmgrd reads at startup.

The factory image's `/etc/sonic/config_db.json` is loaded at first boot to populate the initial CONFIG_DB state, but the `DEVICE_METADATA.mac` entry is not copied to Redis for some reason (possibly a CONFIG_DB schema mismatch or loading bug in the CiscoVS buildimage).

### 2. Provisioning Overwrites MAC Field

When newtron provisions the device using `CompositeOverwrite` mode:
1. It builds a composite CONFIG_DB by merging zone specs, device profiles, and service templates
2. The composite includes a `DEVICE_METADATA|localhost` table entry with hostname and other metadata, **but the MAC field is not present** (because newtron doesn't know to include it)
3. This composite is delivered to the device via `ConfigDBClear()` + `ConfigDBSet()`
4. The in-memory CONFIG_DB is completely overwritten, permanently erasing any MAC that might have existed

After provisioning, `redis-cli -n 4 HGETALL 'DEVICE_METADATA|localhost'` shows **no mac field at all**.

### 3. vlanmgrd Cannot Start

At any point after the MAC is missing (either factory state or post-provision), when vlanmgrd starts:
1. It queries Redis CONFIG_DB for `DEVICE_METADATA|localhost.mac`
2. The field is absent, so vlanmgrd receives `nil` / empty string
3. vlanmgrd logs the error and crashes with status code indicating FATAL configuration error
4. supervisord restarts it up to 4 times, then gives up

**Why the MAC is required:** vlanmgrd needs to set the bridge MAC address for all kernel VLAN interfaces to match the physical device's MAC. This is required for correct L2 forwarding behavior (VLAN interfaces must have the same MAC as the device so that:
- ARP replies on VLAN interfaces appear to come from the same device
- Bridged frames are processed with correct source MAC for hardware forwarding
- Multi-homing and STP work correctly with consistent bridge identifiers).

## Why This Didn't Manifest Earlier

- **3node topology:** Tests primarily use L3 routing (EVPN L3VPN and IP routing). L2 VLAN bridging is not heavily tested on 3node, so the crash went unnoticed.
- **VPP platform:** VPP uses a different VLAN forwarding path and does not rely on vlanmgrd for bridging (vlanmgrd behavior on VPP was not investigated).

## Fix

The fix is implemented in two parts:

### Part 1: Restore MAC During Provisioning

**File:** `pkg/newtron/network/topology.go` in `ProvisionDevice()`

After building the composite CONFIG_DB and before delivering it to the device:
1. Read the system MAC from the factory `/etc/sonic/config_db.json` file via SSH
2. Inject it into `composite.Tables["DEVICE_METADATA"]["localhost"]["mac"]`
3. Deliver the patched composite

**Implementation:**
```go
// In ProvisionDevice, after building composite, before ConfigDBSet:
systemMAC := node.ReadSystemMAC()
if systemMAC != "" {
    if _, ok := composite.Tables["DEVICE_METADATA"]; !ok {
        composite.Tables["DEVICE_METADATA"] = make(map[string]map[string]string)
    }
    if _, ok := composite.Tables["DEVICE_METADATA"]["localhost"]; !ok {
        composite.Tables["DEVICE_METADATA"]["localhost"] = make(map[string]string)
    }
    composite.Tables["DEVICE_METADATA"]["localhost"]["mac"] = systemMAC
}
// Now deliver composite via ConfigDBSet
```

**Why read from `/etc/sonic/config_db.json`:** The factory image writes the platform-initialized MAC to this file at build time. This file persists across all provisioning runs (newtron never calls `config save`, so it doesn't overwrite the file). Therefore, `config_db.json` is the canonical source for the factory MAC.

**Helper methods:**
- `pkg/newtron/device/sonic/device.go`: Added `ReadSystemMAC()` method
  ```go
  func (d *Device) ReadSystemMAC() string {
      // SSH cat /etc/sonic/config_db.json, parse JSON, extract
      // DEVICE_METADATA.localhost.mac, return it
  }
  ```

- `pkg/newtron/network/node/node.go`: Added `ReadSystemMAC() string` delegating to the sonic.Device

### Part 2: Restart vlanmgrd (Since It's Already FATAL)

**File:** `newtest/suites/2node-incremental/01-provision.yaml` after provisioning step

After provisioning completes (and the MAC has been injected), supervisord has already crashed vlanmgrd 4 times and marked it FATAL. Writing the MAC alone doesn't auto-restart it. So the test explicitly restarts vlanmgrd:

```bash
sudo docker exec swss supervisorctl start vlanmgrd
```

This will:
1. Clear the FATAL flag
2. Start vlanmgrd (it will now find the MAC in Redis)
3. Create all kernel VLAN interfaces with the correct MAC
4. Populate APP_DB and ASIC_DB

## Code Changes

### `pkg/newtron/device/sonic/device.go`

Added `ReadSystemMAC()` method to read the factory MAC from `/etc/sonic/config_db.json`:

```go
func (d *Device) ReadSystemMAC() string {
    // Connect via SSH, read /etc/sonic/config_db.json, parse JSON,
    // extract DEVICE_METADATA.localhost.mac, return it
    // If file doesn't exist or field is missing, return empty string
    // (this is not an error — some platforms may have MAC in different location)
}
```

### `pkg/newtron/network/node/node.go`

Added delegating method:

```go
func (n *Node) ReadSystemMAC() string {
    if n.Device == nil {
        return ""
    }
    return n.Device.ReadSystemMAC()
}
```

### `pkg/newtron/network/topology.go`

Modified `ProvisionDevice()` to inject MAC after composite generation:

```go
// After composite is built, before delivery:
systemMAC := node.ReadSystemMAC()
if systemMAC != "" {
    if _, ok := composite.Tables["DEVICE_METADATA"]; !ok {
        composite.Tables["DEVICE_METADATA"] = make(map[string]map[string]string)
    }
    if _, ok := composite.Tables["DEVICE_METADATA"]["localhost"]; !ok {
        composite.Tables["DEVICE_METADATA"]["localhost"] = make(map[string]string)
    }
    composite.Tables["DEVICE_METADATA"]["localhost"]["mac"] = systemMAC
}
```

### `newtest/suites/2node-incremental/01-provision.yaml`

After provisioning step, added restart of vlanmgrd:

```yaml
steps:
  - provision
  - action: shell
    commands:
      - docker exec swss supervisorctl start vlanmgrd
```

## Why This Works

- **Factory file persists:** `/etc/sonic/config_db.json` is never modified by newtron (only in-memory CONFIG_DB is written via `ConfigDBSet`). So the platform-initialized MAC is always available to read.
- **Injection is safe:** Even if the MAC is not found (returns empty string), the code skips injection. Platforms that don't have this issue won't be affected.
- **Restart is idempotent:** Running `supervisorctl start vlanmgrd` on a running vlanmgrd is a no-op; on a FATAL vlanmgrd it restarts it.
- **Timing is correct:** The MAC is injected **before** vlanmgrd first reads CONFIG_DB (which happens when supervisord starts it after the test puts it in the start command).

## Affected Platforms

- **CiscoVS:** Confirmed affected (factory image has this defect)
- **VPP:** Unknown (not investigated; different VLAN path)
- **Other SONiC platforms:** Potentially affected if they:
  1. Use CompositeOverwrite provisioning
  2. Have platform-initialized MAC only in `/etc/sonic/config_db.json`, not in the in-memory CONFIG_DB at boot
  3. Rely on vlanmgrd for L2 bridging

## Lesson Learned

When a factory SONiC image has critical metadata (MAC, hostname, etc.) that is needed at runtime, verify that it is:
1. Present in the in-memory CONFIG_DB (not just the persistent file)
2. Preserved across provisioning operations (read from persistent file and re-inject if needed)

For provisioning frameworks like newtron that use ConfigDBOverwrite, always read critical factory metadata from the persistent `/etc/sonic/config_db.json` and inject it into the composite before delivery. This ensures that provisioning doesn't accidentally erase factory-initialized values that the system daemons depend on.
