# Binding Self-Sufficiency: Convergence Analysis

This document identifies design aspects in newtron's service binding and
teardown paths that don't fully converge on the binding self-sufficiency
principle established in DESIGN_PRINCIPLES.md §5:

> The binding record must contain every value the reverse path needs to
> tear down what the forward path created.

## Status: ALL GAPS CLOSED (March 2026)

All identified gaps have been fixed by adding 7 self-sufficiency fields to
NEWTRON_INTENT: `service_type`, `vrf_type`, `l2vni`, `anycast_ip`,
`anycast_mac`, `arp_suppression`, `bgp_peer_as`.

RemoveService and RefreshService now read all values from the binding record.
Legacy fallback to spec re-resolution exists for bindings created before this
change, but new bindings are fully self-sufficient.

---

## Gaps Fixed

### 1. RemoveService re-derived macvpnDef values from specs — FIXED

**Was:** RemoveService resolved MAC-VPN spec to get `AnycastIP`, `AnycastMAC`,
`ARPSuppression`, `VNI` (L2VNI). If specs changed between apply and remove,
teardown used wrong values.

**Fix:** Added `l2vni`, `anycast_ip`, `anycast_mac`, `arp_suppression` to the
binding. RemoveService reads these directly. Legacy fallback to macvpn spec
for bindings without the new fields.

### 2. RemoveService re-derived decision fields from specs — FIXED

**Was:** RemoveService looked up `svc.ServiceType` and `svc.VRFType` from the
service spec to decide which teardown paths to execute (canBridge, canRoute,
hasIRB, interface vs shared VRF). If the service spec was deleted or changed,
entire VLAN/VRF cleanup blocks were skipped — massive config leakage.

**Fix:** Added `service_type` and `vrf_type` to the binding. RemoveService
derives canBridge/canRoute/hasIRB from the stored service type. Legacy fallback
to service spec for bindings without the new fields.

### 3. RefreshService lost PeerAS for `peer_as: "request"` services — FIXED

**Was:** RefreshService called `ApplyService` with `PeerAS: 0` after remove.
For services with `peer_as: "request"`, this caused ApplyService to fail —
the BGP neighbor was deleted and never recreated.

**Fix:** Added `bgp_peer_as` to the binding. RefreshService reads it and passes
it as `ApplyServiceOpts.PeerAS`. Also preserves `VLAN` for local IRB/bridged
types.

### 4. RefreshService lost VLAN for local IRB/bridged services — FIXED

**Was:** RefreshService called `ApplyService` with `VLAN: 0`. For local IRB
and bridged services (without macvpn), this caused ApplyService to fail.

**Fix:** RefreshService reads `vlan_id` from the binding and passes it as
`ApplyServiceOpts.VLAN`.

### 5. IsLastAnycastMACUser re-resolved macvpn specs — FIXED (bonus)

**Was:** `DependencyChecker.IsLastAnycastMACUser()` re-resolved macvpn specs
from all bindings to check AnycastMAC. If a macvpn spec was deleted, it could
incorrectly report "last user" even when other bindings still had anycast MACs.

**Fix:** Reads `anycast_mac` from binding directly. Legacy fallback to macvpn
spec for bindings without the field.

---

## Not a Gap (Intentional Design)

### UnbindIPVPN Transit VLAN Discovery

`UnbindIPVPN()` scans `VXLAN_TUNNEL_MAP` to find transit VLANs. This is
mitigated by the reference guard — by the time UnbindIPVPN runs, services
have been removed and transit VLAN infrastructure has already been cleaned
up by RemoveService (which uses binding values). The fragile scan path is
only exercised for standalone VRF unbind on VRFs with no service bindings.

### DependencyChecker.IsLast*() Scans

These scan CONFIG_DB for shared resource lifecycle decisions. They read
CURRENT state (who else references this resource right now), not historical
values. Correct behavior.

### i.IPAddresses() Reads

Reads current device state to clean up whatever IPs are actually assigned.
Correct behavior — the device is ground truth for current IPs.

### deleteRoutePoliciesConfig Scan

Uses deterministic naming convention (`svc-{name}-`) to find route policy
entries. No value storage needed — the prefix is stable and correct.

---

## Summary

| Gap | Status | Fields Added |
|-----|--------|-------------|
| macvpnDef values (L2VNI, AnycastIP/MAC, ARP) | FIXED | `l2vni`, `anycast_ip`, `anycast_mac`, `arp_suppression` |
| Decision fields (ServiceType, VRFType) | FIXED | `service_type`, `vrf_type` |
| RefreshService PeerAS | FIXED | `bgp_peer_as` |
| RefreshService VLAN | FIXED | (uses existing `vlan_id`) |
| IsLastAnycastMACUser | FIXED | (uses `anycast_mac`) |
