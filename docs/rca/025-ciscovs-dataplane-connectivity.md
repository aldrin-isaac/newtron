# RCA-025: CiscoVS Dataplane Connectivity Issues

**Date:** 2026-02-16
**Platform:** SONiC 202505 with Cisco Silicon One Virtual PFE (Palladium2)
**Status:** 🔍 **IN PROGRESS**

## Summary

After successfully fixing BGP container stability issues (RCA-024), the 3node dataplane test now fails at BGP peering verification due to L2/dataplane connectivity problems. Packets cannot traverse between Ethernet0 interfaces on leaf1 ↔ leaf2, despite correct configuration.

## Timeline

1. **Fixed**: BGP local_asn bug (commit 832237e) - containers no longer crash
2. **Fixed**: ApplyFRRDefaults ttl-security conflict (commit a72e8ce) - FRR config correct
3. **Fixed**: BGP container stability - using systemctl restart, increased wait time to 30s
4. **Current**: L2 connectivity failing - ARP resolution fails between directly connected interfaces

## Environment

- **Topology**: 3node (leaf1 ↔ leaf2 underlay link via Ethernet0)
- **Expected connectivity**: leaf1 Ethernet0 (10.1.0.0/31) ↔ leaf2 Ethernet0 (10.1.0.1/31)
- **Symptom**: `ping 10.1.0.1` from leaf1 → 100% packet loss, ARP shows `10.1.0.1 FAILED`

## Investigation

### Port Mapping Architecture

CiscoVS uses a complex port mapping chain:

```
External Network ↔ eth1-32 ↔ (tc redirect) ↔ swveth1-32 ↔ veth1-32 ↔ NSIM (Virtual ASIC) ↔ SONiC (Ethernet0-31)
```

**Key components:**
1. **veth-create.sh**: Creates veth pairs (veth1-32 ↔ swveth1-32) ✅ WORKING
2. **tc-create.sh**: Sets up bidirectional tc redirect (eth1 ↔ swveth1) ✅ WORKING
3. **nsim**: Silicon One network simulator daemon ✅ RUNNING
4. **nsim_kernel**: NSIM kernel interface ✅ RUNNING

### Configuration Status

| Component | Status | Details |
|-----------|--------|---------|
| Interface IPs | ✅ Configured | Loopback0: 10.0.0.11/32, Ethernet0: 10.1.0.0/31 |
| FRR config | ✅ Correct | BGP ASN 65011, neighbors configured with ebgp-multihop |
| BGP containers | ✅ Stable | All daemons running, no crashes |
| CONFIG_DB | ✅ Correct | No invalid local_asn, proper ebgp_multihop settings |
| veth pairs | ✅ Created | swveth1-32 ↔ veth1-32 exist, MTU 9100, UP state |
| tc redirects | ✅ Configured | eth1 ↔ swveth1 bidirectional mirred egress redirect |
| NSIM daemons | ✅ Running | nsim and nsim_kernel processes active |
| ASIC_DB ports | ✅ Exist | SAI port objects present in ASIC_DB |

### Boot Script Issues

Syncd container logs show setup script failures:

```
WARN exited: veth-creator (exit status 1; not expected)
WARN exited: tc-creator (exit status 1; not expected)
WARN exited: set-promiscuous (exit status 1; not expected)
```

Despite exit status 1, the scripts **did** create the necessary infrastructure (veth pairs and tc redirects exist). The failures may indicate:
- Scripts running multiple times (idempotency issues)
- Pre-existing resources causing errors
- set-promiscuous script failing (but interfaces ARE in PROMISC mode)

## Root Cause Hypothesis

The port mapping infrastructure appears correct, but L2 forwarding through the NSIM virtual ASIC is not working. Possible causes:

1. **NSIM initialization incomplete**: The nsim_kernel module may not have fully initialized the dataplane
2. **Port admin state**: ASIC ports may not be administratively enabled
3. **SAI object state**: Port objects exist but may not be in forwarding state
4. **MAC learning disabled**: Virtual ASIC may not be learning MAC addresses
5. **HWSKU mismatch**: cisco-p200-32x100-vs may have platform-specific requirements

## Next Steps

1. **Check ASIC port admin state**:
   ```bash
   redis-cli -n 1 HGET "ASIC_STATE:SAI_OBJECT_TYPE_PORT:oid:..." SAI_PORT_ATTR_ADMIN_STATE
   ```

2. **Verify NSIM kernel module**:
   ```bash
   lsmod | grep nsim
   dmesg | grep -i nsim
   ```

3. **Check syncd SAI initialization**:
   ```bash
   grep -i "sai.*init\|port.*create" /var/log/syslog
   ```

4. **Test with simpler topology**: Single link between two VMs to isolate NSIM behavior

5. **Compare with working VPP platform**: Identify CiscoVS-specific initialization requirements

## Workarounds Attempted

- ✅ Fixed BGP container crashes (local_asn bug)
- ✅ Fixed ApplyFRRDefaults (removed ttl-security conflict)
- ✅ Increased BGP restart wait time (15s → 30s)
- ⏸️ Manual tc-create.sh execution (unnecessary - already configured)

## Related Issues

- RCA-024: CiscoVS factory FRR config invalid (fixed)
- RCA-022: CiscoVS build issues (resolved)
- RCA-019: BGP restart required after provision (documented)

## Progress Metrics

**From initial CiscoVS attempt to current state:**

| Milestone | Status | Notes |
|-----------|--------|-------|
| Image build | ✅ Complete | 2.36GB qcow2 with Palladium2 SAI |
| VM boot | ✅ Working | 600s timeout, 6GB RAM, 6 vCPU |
| SSH access | ✅ Working | aldrin/YourPaSsWoRd credentials |
| Provision | ✅ Working | CONFIG_DB writes successful |
| BGP containers | ✅ Stable | No more crash-loops |
| FRR config | ✅ Correct | ebgp-multihop rendered properly |
| L2 dataplane | ❌ **BLOCKED** | **ARP failing, no connectivity** |

**Test progression:**
- Initial: Failed at apply-frr-defaults (BGP container crash)
- After fixes: Failed at verify-bgp (L2 connectivity issue)
- **40+ seconds saved** in test time due to container stability

## Recommendations

1. **Short-term**: Document CiscoVS dataplane issue as known limitation
2. **Medium-term**: Investigate NSIM/SAI initialization sequence
3. **Long-term**: Consider alternative CiscoVS HWSKUs (Gibraltar, GR2) if Palladium2 has simulator bugs
4. **Fallback**: Use sonic-vpp platform for E2E tests until CiscoVS dataplane is resolved

## References

- CiscoVS port mapping scripts: `platform/ciscovs/docker-syncd-ciscovs/scripts/`
- Build commit: cb27941bb222fd953a3de228cc46391e373b43cf (202505 branch)
- SAI version: 1.16.1, SDK 25.9.1000.2 (Palladium2)
