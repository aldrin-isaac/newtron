# RCA-025: CiscoVS Dataplane Connectivity Issues

**Date:** 2026-02-16
**Platform:** SONiC 202505 with Cisco Silicon One Virtual PFE (Palladium2)
**Status:** RESOLVED (Feb 2026)

## Summary

During initial CiscoVS bring-up, L2/dataplane connectivity failed between directly connected interfaces. ARP resolution did not work, and packets could not traverse the NSIM virtual ASIC despite correct CONFIG_DB configuration. The issue was resolved through a combination of fixes to BGP configuration, boot sequencing, and platform-specific initialization. The 3node-ngdp-dataplane suite now passes 6/6 including evpn-l2-irb. ARP suppression, VXLAN tunnels, and MAC learning all work correctly on Silicon One Palladium2.

## Root Cause

The dataplane failures had multiple contributing causes, resolved incrementally:

1. **BGP local_asn bug** (commit 832237e) -- BGP containers were crash-looping due to an invalid `local_asn` field in CONFIG_DB, preventing any routing from converging.
2. **FRR ttl-security conflict** (commit a72e8ce) -- `ApplyFRRDefaults` injected a `ttl-security` setting that conflicted with `ebgp-multihop`, causing FRR to reject the peer configuration.
3. **BGP container restart timing** -- The factory FRR config (AS 65100) required a `systemctl restart bgp` after provisioning a new ASN. The original 15-second wait was insufficient; increasing to 30 seconds allowed the container to fully reinitialize.
4. **NSIM virtual ASIC initialization** -- The syncd container's boot scripts (veth-creator, tc-creator, set-promiscuous) logged exit status 1 warnings, but the infrastructure they created was functional. The L2 forwarding path through NSIM required all upstream fixes to be in place before the dataplane could operate end-to-end.

## CiscoVS Port Mapping Architecture

For reference, CiscoVS uses this port mapping chain:

```
External Network <-> eth1-32 <-> (tc redirect) <-> swveth1-32 <-> veth1-32 <-> NSIM (Virtual ASIC) <-> SONiC (Ethernet0-31)
```

Key components: veth pairs (veth1-32 / swveth1-32), bidirectional tc redirects (eth1 / swveth1), and the NSIM + nsim_kernel daemons providing Silicon One SAI simulation.

## What Fixed It

The fixes were cumulative -- each resolved a layer of the problem:

| Fix | Commit/Change | Effect |
|-----|---------------|--------|
| BGP local_asn removal | 832237e | Eliminated BGP container crash-loops |
| ttl-security conflict | a72e8ce | FRR accepted eBGP peer configuration |
| BGP restart wait increase | 15s to 30s | Container fully reinitialized before verification |
| Platform boot sequence | Upstream scripts | NSIM dataplane forwarded packets once upper layers converged |

Once all BGP and configuration issues were resolved, the NSIM virtual ASIC forwarded packets correctly without any platform-specific workarounds.

## Validation

**3node-ngdp-dataplane: 6/6 PASS**

- L3 underlay routing between leaf1, leaf2, and spine
- EVPN L2 IRB (intra-subnet and inter-subnet) via VXLAN
- ARP suppression, MAC learning, and VXLAN tunnel encapsulation all functional on Palladium2

## Related RCAs

- RCA-024: CiscoVS factory FRR config invalid (resolved)
- RCA-022: CiscoVS build issues (resolved)
- RCA-019: BGP restart required after provision (documented workaround)

## References

- CiscoVS port mapping scripts: `platform/ciscovs/docker-syncd-ciscovs/scripts/`
- Build commit: cb27941bb222fd953a3de228cc46391e373b43cf (202505 branch)
- SAI version: 1.16.1, SDK 25.9.1000.2 (Palladium2)
