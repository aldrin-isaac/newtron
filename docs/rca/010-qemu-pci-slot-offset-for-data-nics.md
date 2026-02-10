# RCA-010: QEMU PCI Slot Offset for Data NICs

## Symptom

After deploy + boot patches, VPP syncd starts but shows no DPDK interfaces
(`vppctl show interface` returns only `local0`). The `sonic_vpp_ifmap.ini` and
`syncd_vpp_env` files are generated correctly by boot patches, but VPP binds
to the wrong PCI device.

## Root Cause

`QEMUPCIAddrs()` computed data NIC PCI addresses starting at slot 3
(`0000:00:03.0`), but on the i440FX bus QEMU uses:

| Slot | Device |
|------|--------|
| 0 | Host bridge (Intel 440FX) |
| 1 | ISA bridge, IDE, ACPI (PIIX3/PIIX4) |
| 2 | VGA controller |
| 3 | **Management NIC** (first `-device` argument) |
| 4 | **First data NIC** (second `-device` argument) |
| 5 | Second data NIC, etc. |

So `0000:00:03.0` is the management NIC, not the first data NIC. VPP tried
to bind the mgmt NIC via DPDK, which failed silently (the device is already
in use by the kernel for SSH).

## Fix

Changed `QEMUPCIAddrs()` from `3+i` to `4+i`:

```go
// Before (wrong):
addrs[i] = fmt.Sprintf("0000:00:%02x.0", 3+i)

// After (correct):
addrs[i] = fmt.Sprintf("0000:00:%02x.0", 4+i)
```

File: `pkg/newtlab/patch.go`

## Verification

```bash
# lspci on the VM confirms slot assignment:
# 00:03.0 = mgmt NIC, 00:04.0 = data NIC
lspci | grep Ethernet

# After fix, syncd_vpp_env contains:
# VPP_DPDK_PORTS=0000:00:04.0
# VPP binds the correct data NIC
docker exec syncd vppctl show interface
```
