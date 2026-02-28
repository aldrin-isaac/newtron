# RCA-041: intfmgrd VRF_TABLE Race — VRF-Bound VLAN Interfaces Not Bound After HMSET Provisioning

**Severity**: Medium
**Platform**: All SONiC (confirmed on CiscoVS 202505)
**Status**: Fixed — use `config reload` instead of `restart bgp` after CompositeOverwrite

## Symptom

After HMSET-based provisioning (CompositeOverwrite), VLAN interfaces with `vrf_name`
in their `VLAN_INTERFACE` entry are NOT bound to the VRF in the kernel:

```
$ ip link show Vlan400
Vlan400@Bridge: <UP> ... mode DEFAULT  (NO "master Vrf_irb")
$ ip addr show Vlan400
inet6 fe80::... (NO IPv4 address)
```

CONFIG_DB and APP_DB both have the correct entries. Gateway ping to non-VRF SVIs works.
The VRF kernel device exists (`ip link show type vrf` shows Vrf_irb).

## Root Cause — VRF_TABLE vs VRF_OBJECT_TABLE Asymmetry

intfmgrd's `doIntfGeneralTask()` checks STATE_DB before processing VRF-bound interfaces:

```cpp
if (!vrf_name.empty() && !isIntfStateOk(vrf_name)) {
    return false;  // retry later
}
```

`isIntfStateOk()` queries `STATE_DB:VRF_TABLE|{vrf_name}`. If absent, retries on a
1-second SELECT_TIMEOUT loop.

vrfmgrd writes to two STATE_DB tables:
- **`VRF_OBJECT_TABLE|Vrf_irb`** — written on BOTH startup and runtime notification
- **`VRF_TABLE|Vrf_irb`** — written ONLY during startup (fresh daemon initialization)

During HMSET provisioning (daemons already running), vrfmgrd receives a CONFIG_DB
notification, creates the VRF kernel device, writes `VRF_OBJECT_TABLE`, but does NOT
write `VRF_TABLE`. intfmgrd checks `VRF_TABLE`, never finds it, and retries forever.

During `config reload`, all daemons restart from scratch. vrfmgrd's startup code path
writes BOTH tables. By the time intfmgrd's first retry fires (~1 second), `VRF_TABLE`
exists and VRF binding succeeds.

## Evidence

After HMSET provisioning:
```
STATE_DB keys: VRF_OBJECT_TABLE|Vrf_irb   (state: ok)
               # VRF_TABLE|Vrf_irb — MISSING
```

After config reload:
```
STATE_DB keys: VRF_OBJECT_TABLE|Vrf_irb   (state: ok)
               VRF_TABLE|Vrf_irb           (state: ok)    ← NOW PRESENT
```

## Fix

Replace `systemctl restart bgp` with `config reload -y` in the post-provision
sequence. `config reload` restarts ALL SONiC daemons from scratch, ensuring:

1. vrfmgrd writes VRF_TABLE (intfmgrd dependency)
2. bgpcfgd picks up new ASN (RCA-019)
3. All daemons process config from clean startup state

Changed files:
- `cmd/newtron/cmd_provision.go` — `ConfigReload()` instead of `RestartService("bgp")`
- `pkg/newtron/network/node/node.go` — new `ConfigReload()` method
- `pkg/newtrun/` — new `config-reload` action
- `newtrun/suites/2node-service/02-provision.yaml` — uses config-reload

## Related

- RCA-019: BGP ASN change requires container restart
- sonic-swss `intfmgr.cpp` — `doIntfGeneralTask()`, `isIntfStateOk()`
- sonic-swss `vrfmgr.cpp` — writes VRF_OBJECT_TABLE on notification, VRF_TABLE on startup
- sonic-swss issue #942 — IP set on VLAN/LAG not yet created (related race)
