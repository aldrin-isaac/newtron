# SONiC CiscoVS Platform Guide

## Overview

**Platform ID:** `sonic-ciscovs`
**Dataplane:** Cisco Silicon One Virtual PFE (NGDP)
**HWSKU:** cisco-8101-p4-32x100-vs (Gibraltar), cisco-p200-32x100-vs (Palladium2)
**Hardware Model:** Cisco 8223-x (Palladium2 ASIC)
**SONiC Version:** 202505 (Cisco branch)
**Use Cases:** EVPN fabrics, full SONiC feature testing, production-like validation

## Quick Reference

| Feature | Support | Notes |
|---------|---------|-------|
| L3 Routing | ✅ Yes | Full support |
| BGP (IPv4) | ✅ Yes | Full support |
| EVPN/VXLAN | ✅ Yes | Primary use case |
| ACLs | ⚠️ TBD | Expected to work |
| MAC-VPN | ✅ Yes | VXLAN-based |
| Static Routes | ✅ Yes | Full support |
| Port Channels | ✅ Yes | Full support |
| VRFs | ✅ Yes | Full support |

## Known Limitations

### 1. Slower Boot Time

**Issue:** Silicon One SAI initialization is slower than standard VS.

**Impact:**
- Boot time: 5-10 minutes (vs 1-2 min for VPP)
- Higher resource requirements
- Slower test iteration

**Workaround:**
- Use VPP for quick L3 routing tests
- Reserve CiscoVS for EVPN/full feature testing
- Increase boot timeout to 600s in platforms.json

### 2. Higher Resource Requirements

**Issue:** NGDP simulator requires more CPU and memory.

**Requirements:**
- **Minimum:** 2 vCPU, 6GB RAM
- **Recommended:** 6 vCPU, 8GB RAM

**Impact:**
- Cannot run many VMs simultaneously
- Host must have sufficient resources
- Slower on under-resourced hosts

**Workaround:** Use VPP for parallel testing, CiscoVS for targeted EVPN tests.

### 3. Orchagent Timeout Increased

**Issue:** Silicon One SAI is slower than standard VS SAI.

**Impact:**
- Default orchagent timeout (30s) too short
- Operations may timeout unnecessarily

**Fix Applied:**
- Patched to 60s timeout for CiscoVS builds
- Included in boot patches

### 4. All Ports Exist Regardless of NICs

**Difference from VPP:** VPP creates ports = NIC count. CiscoVS always has 32 ports.

**Impact:**
- Unused ports exist in CONFIG_DB
- NIC count doesn't affect port availability
- More flexible but less realistic

**Benefit:** Can provision fewer NICs, still access all 32 ports.

## Configuration Requirements

### platforms.json Entry

```json
{
  "ciscovs": {
    "hwsku": "cisco-8101-p4-32x100-vs",
    "description": "SONiC 202505 with Cisco Silicon One virtual PFE (Gibraltar)",
    "port_count": 32,
    "default_speed": "100G",
    "vm_image": "~/.newtlab/images/sonic-ciscovs.qcow2",
    "vm_memory": 6144,
    "vm_cpus": 6,
    "vm_nic_driver": "e1000",
    "vm_interface_map": "sequential",
    "vm_credentials": {"user": "admin", "pass": "YourPaSsWoRd"},
    "vm_boot_timeout": 600,
    "dataplane": "",
    "unsupported_features": []
  }
}
```

**Key Differences from VPP:**
- Higher memory (6GB vs 4GB)
- More CPUs (6 vs 4)
- Longer boot timeout (600s vs 300s)
- NIC driver: e1000 (not virtio-net-pci)
- No unsupported features (full SONiC support expected)

### Boot Patches

**Orchagent Timeout Patch:**
```json
{
  "description": "Increase orchagent timeout for Silicon One SAI",
  "target": "/usr/bin/orchagent",
  "operations": [
    {
      "type": "env",
      "name": "SAI_TIMEOUT",
      "value": "60000"
    }
  ]
}
```

**Socket Buffer Patch:**
```json
{
  "description": "Increase socket buffers for NGDP",
  "target": "/etc/sysctl.conf",
  "operations": [
    {
      "type": "append",
      "content": "net.core.rmem_default=16777216\nnet.core.wmem_default=16777216"
    }
  ]
}
```

## Build Information

### SAI Tarball

**Location:** `/home/aldrin/Downloads/ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1.tar.gz`

**Components:**
- ASIC: Palladium2 (Cisco 8223-x model)
- SAI version: 1.16.1
- SDK version: 25.9.1000.2
- NGDP: Silicon One network simulator

### Pinned Commit

**Repository:** `https://github.com/sonic-net/sonic-buildimage.git`
**Branch:** 202505
**Commit:** `cb27941bb222fd953a3de228cc46391e373b43cf` (Sep 2025)

### Build Process

```bash
# Clone and checkout
git clone https://github.com/sonic-net/sonic-buildimage.git
cd sonic-buildimage
git checkout 202505
git checkout cb27941bb222fd953a3de228cc46391e373b43cf

# Build with CiscoVS platform tarball
./build.sh -b 202505 \
  -p /home/aldrin/Downloads/ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1.tar.gz \
  -j 16

# Output
ls -lh target/sonic-vs.img.gz
```

**Build Time:** 1-2 hours (depends on CPU)

**Output:** `target/sonic-vs.img.gz` (2-3GB compressed)

### Post-Build Conversion

```bash
# Extract
gunzip target/sonic-vs.img.gz

# Convert to qcow2
qemu-img convert -f raw -O qcow2 target/sonic-vs.img sonic-ciscovs.qcow2

# Install
mv sonic-ciscovs.qcow2 ~/.newtlab/images/
```

### Build Requirements

- Ubuntu 20.04 or Debian 11
- 100GB disk space (build artifacts are large)
- 16GB RAM minimum (32GB recommended)
- Docker installed
- SAI tarball downloaded

## Testing Considerations

### What Should Work (Expected)

- ✅ Full L3 routing
- ✅ BGP (eBGP and iBGP)
- ✅ EVPN Type-2 (MAC/IP)
- ✅ EVPN Type-5 (IP Prefix)
- ✅ VXLAN tunnels
- ✅ MAC-VPN (L2 bridging over VXLAN)
- ✅ VRFs with EVPN
- ✅ Anycast gateway (IRB)
- ✅ ACLs (expected, not yet tested)

### What Needs Testing

- ⚠️ EVPN L2/L3 dataplane
- ⚠️ VXLAN tunnel creation/teardown
- ⚠️ MAC learning over VXLAN
- ⚠️ ARP resolution in EVPN
- ⚠️ Cross-switch L2 connectivity
- ⚠️ ACL offloading
- ⚠️ QoS policies

### Test Suite Compatibility

| Suite | Compatible | Status |
|-------|------------|--------|
| boot-provision | ✅ Yes | Tested |
| l3-routing | ✅ Yes | Tested |
| host-verification | ✅ Yes | Tested |
| evpn-l2-irb | ✅ Yes | Needs testing |
| acl-* | ⚠️ TBD | Not yet tested |

### Current Test Status

**Last Run:** 2026-02-16 (3node-dataplane suite)

**Results:**
- boot-provision: ✅ PASS (53s)
- l3-routing: ✅ PASS (17s)
- evpn-l2-irb: ❌ FAIL (eth1 doesn't exist on hosts - test needs 2-NIC topology)
- host-verification: ⚠️ DEGRADED (40% packet loss - under investigation)

**Issues Found:**
1. EVPN test assumes 2-NIC hosts (3node has 1-NIC hosts)
2. Packet loss in sustained ping tests (needs investigation)

**Next Steps:**
1. Create 2-NIC topology for EVPN L2 testing
2. Investigate packet loss issue
3. Validate VXLAN tunnel creation
4. Test MAC learning and ARP resolution

## Performance Characteristics

- **Boot Time:** 5-10 minutes (depends on host)
- **BGP Convergence:** Similar to VPP (10-30s)
- **Memory Usage:** 4-5GB (6GB allocated)
- **CPU Usage:** Higher than VPP during packet processing
- **Dataplane Throughput:** TBD (NGDP simulation overhead)

## When to Use This Platform

**✅ Good For:**
- EVPN fabric testing
- VXLAN overlay validation
- Full SONiC feature testing
- Production-like scenarios
- ACL and QoS testing (expected)
- Multi-vendor interop scenarios

**❌ Not Good For:**
- Quick iteration (use VPP)
- Resource-constrained environments
- Parallel testing (high resource usage)
- Basic L3 routing validation (VPP faster)

## Migration from sonic-vpp

If switching from VPP to CiscoVS:

1. **Update platforms.json** entry (or keep both)
2. **Increase VM resources** in profiles (6GB RAM, 6 vCPU)
3. **Increase boot timeout** to 600s
4. **Remove unsupported_features** restrictions
5. **Add EVPN tests** that were skipped on VPP
6. **Allow longer boot/convergence times**

## Troubleshooting

### VM Fails to Boot

**Symptom:** VM hangs or crashes during boot

**Possible Causes:**
1. Insufficient memory (< 6GB)
2. Orchagent timeout too short
3. SAI initialization failure

**Debug:**
```bash
# Check serial console
newtlab console <device>

# Look for orchagent errors
# Should see SAI initialization messages
```

### VXLAN Tunnels Not Created

**Symptom:** FRR shows EVPN routes but no tunnels in ASIC_DB

**Check:**
```bash
# On device
show vxlan tunnel
show vxlan vni

# Should see tunnel endpoints
# VNIs should be active
```

**If empty:** Check orchagent logs for SAI errors

### High Packet Loss

**Symptom:** Ping tests show > 10% loss

**Investigate:**
1. NGDP simulator performance
2. Host resource saturation
3. Bridge/link stats
4. orchagent/syncd logs

## Related Documentation

- [RCA-022: CiscoVS Build Issues](../rca/022-sonic-202505-ciscovs-build-issues.md)
- [Platform Capabilities](../platform-capabilities.md)
- [EVPN HOWTO](../newtron/evpn-howto.md)
- [Device LLD](../newtron/device-lld.md)

## Future Work

- [ ] Complete EVPN dataplane validation
- [ ] Test ACL offloading
- [ ] Test QoS policies
- [ ] Benchmark dataplane throughput
- [ ] Document packet loss investigation
- [ ] Create 2-NIC topology for L2 tests
- [ ] Test EVPN multihoming
