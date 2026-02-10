# RCA-013: Boot patch port naming mismatch (stride-4 vs sequential)

## Symptom

After deploying the 4-node topology, only `Ethernet0` appeared on each device.
`Ethernet1` was missing. Instead, a stale `PORT|Ethernet4` entry existed in
CONFIG_DB from the factory default configuration.

## Root Cause

Boot patch templates (`port_config.ini.tmpl`, `sonic_vpp_ifmap.ini.tmpl`,
`port_entries.tmpl`) hardcoded a stride-4 naming scheme using `mul $i 4`:

```
Ethernet{{mul $i 4}}  →  Ethernet0, Ethernet4, Ethernet8, ...
```

But the platform specification uses `vm_interface_map: "sequential"`, which
expects:

```
Ethernet0, Ethernet1, Ethernet2, ...
```

The 2-node topology masked this bug because the first port is always
`Ethernet0` regardless of stride — the mismatch only manifests starting with
the second port (`Ethernet4` vs `Ethernet1`).

## Impact

- Devices had wrong interface names — VPP mapped `bobm1` to `Ethernet4` but
  the topology provisioner configured `Ethernet1`
- All non-first interfaces were non-functional
- Only discovered when scaling from 2 to 4 nodes (more interfaces exposed)

## Fix

Added `PortStride` field to `PatchVars` in `pkg/newtlab/patch.go`:

```go
PortStride int // 1 for sequential, 4 for stride-4 (default)
```

`buildPatchVars()` sets it from the platform's `VMInterfaceMap`:

```go
portStride := 4
if platform.VMInterfaceMap == "sequential" {
    portStride = 1
}
```

All four templates updated to use `$.PortStride` instead of hardcoded `4`.

## Lesson

Never hardcode platform-specific constants in templates. Always parameterize
values that vary across platforms. Test with topologies that exercise more than
one interface per device — single-interface tests mask off-by-one and naming
scheme bugs.
