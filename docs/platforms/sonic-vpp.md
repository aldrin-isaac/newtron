# SONiC VPP Platform Guide

## Overview

**Platform ID:** `sonic-vpp`
**Dataplane:** VPP (Vector Packet Processing)
**HWSKU:** Force10-S6000
**SONiC Version:** 202505 (community branch)
**Use Cases:** L3 routing tests, BGP validation, basic SONiC functionality

## Quick Reference

| Feature | Support | Notes |
|---------|---------|-------|
| L3 Routing | ✅ Yes | Full support |
| BGP (IPv4) | ✅ Yes | Works well |
| EVPN/VXLAN | ❌ No | VPP SAI tunnel offloading not merged |
| ACLs | ❌ No | Not supported in VPP dataplane |
| MAC-VPN | ❌ No | Depends on VXLAN |
| Static Routes | ✅ Yes | Full support |
| Port Channels | ✅ Yes | Works |
| VRFs | ✅ Yes | Works |

## Known Limitations

### 1. No EVPN VXLAN Support

**Issue:** VPP SAI tunnel offloading is not merged upstream.

**Impact:**
- CONFIG_DB accepts VXLAN configuration
- ASIC_DB never programs tunnels
- FRR shows 0 VNIs
- Cannot test EVPN L2/L3 overlays

**Workaround:** Use sonic-ciscovs platform for EVPN testing.

**Reference:** RCA-020, sonic-platform-vpp#99

### 2. No swss Restart

**Issue:** Restarting swss/syncd breaks the VM permanently.

**Impact:**
- Cannot reload configuration dynamically
- Cannot recover from swss crashes
- Must redeploy VM for swss issues

**Workaround:** Avoid restarting swss. Use `config reload` sparingly.

**Reference:** RCA-001

### 3. BGP Restart Required After Provision

**Issue:** Factory FRR has AS 65100; bgpcfgd can't change running ASN.

**Impact:**
- After initial provision, BGP uses wrong ASN
- Must restart BGP container to pick up new ASN from CONFIG_DB

**Workaround:**
```bash
# After provisioning with new ASN
docker restart bgp
# Wait 15-30s for BGP to stabilize
```

**Automation:** Included in newtlab bootstrap phase (restart-service: bgp)

**Reference:** RCA-019

### 4. Port Count = NIC Count

**Issue:** Data ports are sequential Ethernet0..N matching QEMU NIC count.

**Impact:**
- Cannot have gaps in port numbering
- NIC count must match topology interface count
- Extra NICs create unused ports

**Workaround:** Provision exactly N+1 NICs for N data ports (NIC0 = mgmt)

**Reference:** RCA-020

### 5. SetIP Requires Base Entry

**Issue:** SONiC requires both base and IP entries in CONFIG_DB.

**Impact:**
```json
// Both entries required:
"INTERFACE|Ethernet1": {}                    // base entry
"INTERFACE|Ethernet1|10.1.1.1/30": {}        // IP entry
```

**Workaround:** newtron always writes base entry first in SetIP operation.

**Reference:** device-lld.md, SetIP implementation

### 6. No ACL Support

**Issue:** VPP dataplane doesn't support ACL offloading.

**Impact:**
- Cannot test filtering, QoS ingress classification
- Platform marked with `unsupported_features: ["acl"]`

**Workaround:** Use sonic-ciscovs for ACL testing.

## Configuration Requirements

### platforms.json Entry

```json
{
  "sonic-vpp": {
    "hwsku": "Force10-S6000",
    "description": "SONiC VPP virtual switch",
    "port_count": 32,
    "default_speed": "100G",
    "vm_image": "~/.newtlab/images/sonic-vpp.qcow2",
    "vm_memory": 4096,
    "vm_cpus": 4,
    "vm_nic_driver": "virtio-net-pci",
    "vm_interface_map": "sequential",
    "vm_cpu_features": "+sse4.2",
    "vm_credentials": {"user": "admin", "pass": "YourPaSsWoRd"},
    "vm_boot_timeout": 300,
    "dataplane": "vpp",
    "unsupported_features": ["acl", "macvpn", "evpn-vxlan"]
  }
}
```

### Boot Patches

Located in `patches/vpp/always/`:
- Interface mapping fixes
- VPP-specific configuration

No release-specific patches needed for 202505.

## Testing Considerations

### What Works Well
- ✅ L3 routing between switches
- ✅ BGP peering (eBGP and iBGP)
- ✅ Static routes
- ✅ VRF provisioning
- ✅ Basic interface operations
- ✅ Host connectivity (via default VRF)

### What Doesn't Work
- ❌ EVPN VXLAN (no tunnel offloading)
- ❌ ACLs (not supported in VPP)
- ❌ MAC-VPN (depends on VXLAN)
- ❌ L2 bridging over VXLAN

### Test Suite Compatibility

| Suite | Compatible | Notes |
|-------|------------|-------|
| boot-provision | ✅ Yes | Full support |
| l3-routing | ✅ Yes | Primary use case |
| host-verification | ✅ Yes | Works well |
| evpn-l2-irb | ❌ No | Requires VXLAN |
| acl-* | ❌ No | ACLs not supported |

### Test Design Tips

1. **Use L3 routing instead of EVPN** for dataplane tests on VPP
2. **Declare platform requirements** in scenarios: `requires_features: ["evpn-vxlan"]` to skip incompatible tests
3. **Allow 30s after BGP restart** for convergence
4. **Avoid config reload** - redeploy instead if needed
5. **Match NIC count to topology** - VPP ports = QEMU NICs

## Build Information

### Image Location
`~/.newtlab/images/sonic-vpp.qcow2`

### Build Process
```bash
# Clone SONiC buildimage
git clone https://github.com/sonic-net/sonic-buildimage.git
cd sonic-buildimage

# Checkout 202505 branch
git checkout 202505

# Build VPP platform
make configure PLATFORM=vs
make SONIC_BUILD_JOBS=4 target/sonic-vs.img.gz

# Convert to qcow2
gunzip target/sonic-vs.img.gz
qemu-img convert -f raw -O qcow2 target/sonic-vs.img sonic-vpp.qcow2
```

### Build Requirements
- Ubuntu 20.04 or Debian 11
- 50GB disk space
- 8GB RAM minimum
- Docker installed

## Performance Characteristics

- **Boot Time:** 60-120s (depends on host)
- **BGP Convergence:** 10-30s after restart
- **Memory Usage:** 2-3GB (4GB allocated)
- **CPU Usage:** Low (1-2 cores active)

## When to Use This Platform

**✅ Good For:**
- L3 routing tests
- BGP protocol validation
- Interface configuration testing
- Quick iteration (faster than CiscoVS)
- Basic SONiC API validation

**❌ Not Good For:**
- EVPN fabric testing
- VXLAN overlays
- ACL/filtering tests
- Production-like scenarios
- Full SONiC feature validation

## Migration Path

If your test requires unsupported features:

1. **Check platform capabilities** in `platforms.json`
2. **Declare requirements** in scenario: `requires_features: ["evpn-vxlan"]`
3. **Switch to sonic-ciscovs** platform for EVPN tests
4. **Keep VPP tests** for L3 routing (faster execution)

## Related Documentation

- [RCA-001: swss restart breaks VPP](../rca/001-sonic-vpp-swss-restart-fatal.md)
- [RCA-019: BGP restart after provision](../rca/019-sonic-vpp-bgp-asn-change-requires-restart.md)
- [RCA-020: Port count matches NIC count](../rca/020-sonic-vpp-port-count-matches-nic-count.md)
- [Platform Capabilities](../platform-capabilities.md)
- [Device LLD](../newtron/device-lld.md)
