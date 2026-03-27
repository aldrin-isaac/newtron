# Intent Coverage Audit Checklist

Date: 2026-03-24

## Rule

Every forward operation that writes CONFIG_DB entries MUST write an intent record
via `writeIntent`. Every reverse operation MUST read teardown data from the intent
record (via `GetIntent`) or delete the intent record (via `deleteIntent`). The
intent record must contain every value needed for teardown.

Baseline operations (setup-device, set-port-property) are exempt.

## Fixes Applied

### Fix #1: Interface.BindMACVPN / UnbindMACVPN — MISSING INTENT

**Gap**: `Interface.BindMACVPN` (macvpn_ops.go) wrote no intent. `Interface.UnbindMACVPN`
deleted no intent and scanned CONFIG_DB (`VXLANTunnelMap`, `SuppressVLANNeigh`).

**Fix**:
- `BindMACVPN` now calls `writeIntent` at resource `i.name+"|macvpn"` with `macvpn`,
  `vni`, and `arp_suppression` params (macvpn_ops.go:60-67)
- `UnbindMACVPN` now calls `GetIntent(i.name+"|macvpn")`, reads VNI for deterministic
  `deleteVniMapConfig` call, reads `arp_suppression` for `disableArpSuppressionConfig`,
  and calls `deleteIntent` (macvpn_ops.go:90-121)
- Removed dead `unbindMacvpnConfig` CONFIG_DB scanner
- Added `FieldARPSuppression` constant to configdb.go
- Added `OpBindMACVPN` case to `intentInterface` in reconstruct.go for reconstruction

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestBindMACVPN` PASS

### Fix #2: Node.BindMACVPN / UnbindMACVPN — INCOMPLETE INTENT

**Gap**: `Node.BindMACVPN` (evpn_ops.go) wrote intent but omitted VNI.
`Node.UnbindMACVPN` scanned `configDB.VXLANTunnelMap` instead of reading intent.

**Fix**:
- `BindMACVPN` intent now includes `sonic.FieldVNI` (evpn_ops.go:134)
- `UnbindMACVPN` now calls `GetIntent("macvpn|"+vlanID)`, reads VNI, uses
  `deleteVniMapConfig(vni, VLANName(vlanID))` instead of scanning (evpn_ops.go:143-163)
- Removed dead `unmapVniConfig` CONFIG_DB scanner

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestBindMACVPN` PASS

### Fix #3: AddACLRule / DeleteACLRule — MISSING INTENT

**Gap**: `AddACLRule` (acl_ops.go) wrote no intent. `DeleteACLRule` deleted no intent.
Individual ACL rules were invisible to the intent system.

**Fix**:
- `AddACLRule` now calls `updateIntent` on parent ACL record (`"acl|"+tableName`),
  appending rule name to `FieldRules` comma-separated list (acl_ops.go:236-239)
- `DeleteACLRule` now calls `updateIntent` to remove rule name from `FieldRules`
  (acl_ops.go:261-264)
- Re-added `FieldRules` constant to configdb.go (previously removed as "unused")

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestAddACLRule` PASS

### Fix #4: AddPortChannelMember / RemovePortChannelMember — MISSING INTENT

**Gap**: `AddPortChannelMember` (portchannel_ops.go) wrote no intent.
`RemovePortChannelMember` deleted no intent. Individual member changes were invisible.

**Fix**:
- `AddPortChannelMember` now calls `updateIntent` on parent PortChannel record
  (`"portchannel|"+pcName`), appending member to `FieldMembers` comma-separated
  list (portchannel_ops.go:146-150)
- `RemovePortChannelMember` now calls `updateIntent` to remove member from
  `FieldMembers` (portchannel_ops.go:166-170)

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestPortChannel` PASS

### Fix #5: RemoveQoS reads CONFIG_DB instead of intent

**Gap**: `ApplyQoS` (qos_ops.go) wrote intent at `i.name+"|qos"` with `qos_policy`.
`RemoveQoS` read policy name from `configDB.PortQoSMap` instead of intent.

**Fix**:
- `RemoveQoS` now calls `GetIntent(i.name+"|qos")`, reads `qos_policy` from intent,
  returns error if no intent found (qos_ops.go:79-86)
- Note: `unbindQos()` still scans CONFIG_DB for QUEUE entries (justified — queue
  count depends on policy, cascading scan is the only reliable approach)

**Evidence**: `go test ./pkg/newtron/network/node/` all PASS

### Fix #6: UnconfigureIRB reads CONFIG_DB instead of intent

**Gap**: `ConfigureIRB` (vlan_ops.go) wrote intent at `"irb|"+vlanID` with `vlan_id`,
`vrf`, `ip_address`, `anycast_mac`. `UnconfigureIRB` used `destroySviConfig` which
scanned `configDB.VLANInterface`.

**Fix**:
- `UnconfigureIRB` now calls `GetIntent("irb|"+vlanID)`, reads `ip_address` for
  `VLAN_INTERFACE` IP entry deletion, reads `anycast_mac` for `SAG_GLOBAL` cleanup.
  Uses deterministic key construction instead of CONFIG_DB scan (vlan_ops.go:242-279)
- Note: SAG_GLOBAL is shared — still checks CONFIG_DB for other VLAN_INTERFACE base
  entries (justified — shared resource reference counting)
- Removed dead `destroySviConfig` CONFIG_DB scanner

**Evidence**: `go test ./pkg/newtron/network/node/` all PASS

### Fix #7: UnbindACL reads CONFIG_DB for direction instead of intent

**Gap**: `BindACL` (interface_ops.go) wrote intent at `i.name+"|acl|"+direction` with
`acl_name` and `direction`. `UnbindACL` derived direction from
`configDB.ACLTable[aclName].Stage` instead of reading intent.

**Fix**:
- `UnbindACL` now searches intent records at `i.name+"|acl|ingress"` and
  `i.name+"|acl|egress"` to find the matching ACL binding, reads direction from
  intent, returns error if no binding intent found (interface_ops.go:302-330)
- Note: Ports list CONFIG_DB read retained (justified — ports list is shared across
  interfaces, intent cannot predict concurrent bind/unbind by other callers)
- Updated `TestUnbindACLFromInterface` to set up intent record

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestUnbindACL` PASS

## Round 2 — Dual-Tracking and Destroy Intent-Driven

Date: 2026-03-25

### Fix #8: DeleteACL — destroy reads intent instead of CONFIG_DB

**Gap**: `deleteAclTableConfig` scanned `configDB.ACLRule` for rules belonging to the
ACL table, even though the parent ACL intent tracks rules via `FieldRules` CSV.

**Fix**: `deleteAclTableConfig` now reads `rules` from `acl|name` intent record,
splits CSV, and constructs deterministic `ACL_RULE` delete entries (acl_ops.go:280-296).

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestDeleteACL` PASS

### Fix #9: DeletePortChannel — destroy reads intent instead of CONFIG_DB

**Gap**: `destroyPortChannelConfig` scanned `configDB.PortChannelMember` for members,
even though the parent PortChannel intent tracks members via `FieldMembers` CSV.

**Fix**: `destroyPortChannelConfig` now reads `members` from `portchannel|name` intent
record (portchannel_ops.go:94-110).

**Evidence**: `go build ./...` PASS (no existing DeletePortChannel test)

### Fix #10: DeleteVLAN — destroy reads intent instead of CONFIG_DB

**Gap**: `destroyVlanConfig` scanned `configDB.VLANMember` and `configDB.VXLANTunnelMap`
for children. The VLAN intent record did not track members or VNI.

**Fix**: `destroyVlanConfig` now reads `members` and `vni` from `vlan|ID` intent record
(vlan_ops.go:172-202). Dual-tracking added:
- `ConfigureInterface` bridged mode: `updateIntent` on VLAN to add member (interface_ops.go:163-167)
- `UnconfigureInterface` bridged mode: `updateIntent` on VLAN to remove member (interface_ops.go:225-230)
- `ApplyService` with VLAN: `updateIntent` on VLAN to add member (service_ops.go:487-493)
- `RemoveService` with VLAN: `updateIntent` on VLAN to remove member (service_ops.go:1120-1125)
- `Node.BindMACVPN`: `updateIntent` on VLAN to add VNI (evpn_ops.go:137-140)
- `Node.UnbindMACVPN`: `updateIntent` on VLAN to clear VNI (evpn_ops.go:166-169)

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestDeleteVLAN` PASS

### Fix #11: BindIPVPN — route targets not in intent

**Gap**: `BindIPVPN` wrote intent with `l3vni` and `l3vni_vlan` but NOT `route_targets`.
`unbindIpvpnConfig` scanned `configDB.BGPGlobalsEVPNRT` for VRF-prefixed entries.

**Fix**:
- `BindIPVPN` now stores `route_targets` CSV in intent (vrf_ops.go:242)
- `unbindIpvpnConfig` reads route targets from `ipvpn|vrfName` intent record (vrf_ops.go:268-280)
- Added `FieldRouteTargets` constant to configdb.go

**Evidence**: `go build ./...` PASS

### Fix #12: ApplyService — ACL intent for service-created ACLs

**Gap**: When `ApplyService` created a new ACL via `addACLRulesFromFilterSpec`, no ACL
intent record was written. `removeSharedACL` → `deleteAclTableConfig` would find no
intent and produce no rule delete entries.

**Fix**:
- `addACLRulesFromFilterSpec` now returns rule names (service_ops.go:912)
- `ApplyService` writes ACL intent with `rules` CSV when creating a new ACL (service_ops.go:439-443)
- `removeSharedACL` now calls `deleteIntent` for the ACL when it's the last user (service_ops.go:970)

**Evidence**: `go test ./pkg/newtron/network/node/ -run TestRemoveService_SharedACL` PASS

### Fix #13: RemoveService — route policies read intent instead of CONFIG_DB

**Gap**: `deleteRoutePoliciesConfig` scanned `configDB.RouteMap`, `configDB.PrefixSet`,
and `configDB.CommunitySet` by service name prefix.

**Fix**:
- `addBGPRoutePolicies` now collects all route policy keys (`TABLE:key`) and returns
  them in `bgpRoutePolicyResult.routePolicyKeys` (service_ops.go:607-614)
- `ApplyService` stores keys as `route_policy_keys` in service intent (service_ops.go:487)
- `RemoveService` calls `deleteRoutePoliciesFromIntent(i.name)` which reads keys from
  intent (service_ops.go:886-911, called at service_ops.go:1119)
- `scanRoutePoliciesByPrefix` retains CONFIG_DB scan for RefreshService only (ground
  truth diff — justified because RefreshService needs to know what actually exists)

**Evidence**: `go test ./pkg/newtron/... -count=1` all PASS

## Dual-Tracking Principle

Child operations are authoritative updaters of parent container intent records:

| Child Operation | Updates Parent Intent | Field |
|----------------|----------------------|-------|
| ConfigureInterface (bridged) | `vlan|ID` | members |
| UnconfigureInterface (bridged) | `vlan|ID` | members |
| ApplyService (bridged/IRB) | `vlan|ID` | members |
| RemoveService (bridged/IRB) | `vlan|ID` | members |
| Node.BindMACVPN | `vlan|ID` | vni |
| Node.UnbindMACVPN | `vlan|ID` | vni |
| AddACLRule | `acl|name` | rules |
| DeleteACLRule | `acl|name` | rules |
| AddPortChannelMember | `portchannel|name` | members |
| RemovePortChannelMember | `portchannel|name` | members |

Single-table principle is preserved: all intent writes go through `writeIntent`,
`updateIntent`, or `deleteIntent` in `intent_ops.go`.

## Operations NOT Fixed (Justified)

### DependencyChecker — shared resource reference counting

`DependencyChecker` scans CONFIG_DB to determine if shared resources (VRFs, ACLs,
VLANs, peer groups) have other consumers. This is architecturally necessary — it
answers "does anyone else use this?" which requires scanning all consumers.

### UnconfigureIRB — SAG_GLOBAL shared resource check

SAG_GLOBAL is a singleton shared across all IRBs. `UnconfigureIRB` checks
`configDB.VLANInterface` for other SVIs before removing SAG_GLOBAL. This is justified:
the VLAN intent tracks its own IRB, but cannot know about other VLANs' IRBs.

### RefreshService — scanRoutePoliciesByPrefix

RefreshService needs ground truth from CONFIG_DB (what route policies actually exist
on the device) to compute diffs for blue-green migration. This is observation, not
teardown — the scan answers "what exists now?" not "what did I create?".

### SetIP/RemoveIP, SetVRF — building blocks without intent

Low-level building blocks called by `ConfigureInterface` and `ApplyService`, which DO
write intent. Standalone use is for debugging/manual intervention, not production flows.

### TeardownVTEP, RemoveBGPGlobals, RemoveLoopback — baseline exempt

Sub-operations of `SetupDevice`. Their collective reverse is reprovision
(CompositeOverwrite). No individual intent tracking needed.

## Full Audit Matrix

| File | Forward Op | Reverse Op | Intent written? | Intent read on reverse? | Status |
|------|-----------|------------|-----------------|------------------------|--------|
| macvpn_ops.go | Interface.BindMACVPN | Interface.UnbindMACVPN | Yes (Fix #1) | Yes (Fix #1) | FIXED |
| evpn_ops.go | Node.BindMACVPN | Node.UnbindMACVPN | Yes+VNI (Fix #2) + dual-tracks VLAN | Yes (Fix #2) | FIXED |
| acl_ops.go | AddACLRule | DeleteACLRule | Yes via updateIntent (Fix #3) | Yes via updateIntent (Fix #3) | FIXED |
| portchannel_ops.go | AddPortChannelMember | RemovePortChannelMember | Yes via updateIntent (Fix #4) | Yes via updateIntent (Fix #4) | FIXED |
| qos_ops.go | ApplyQoS | RemoveQoS | Yes | Yes (Fix #5) | FIXED |
| vlan_ops.go | ConfigureIRB | UnconfigureIRB | Yes | Yes (Fix #6) | FIXED |
| interface_ops.go | BindACL | UnbindACL | Yes | Yes (Fix #7) | FIXED |
| acl_ops.go | CreateACL | DeleteACL | Yes | Yes — reads rules from intent (Fix #8) | FIXED |
| portchannel_ops.go | CreatePortChannel | DeletePortChannel | Yes | Yes — reads members from intent (Fix #9) | FIXED |
| vlan_ops.go | CreateVLAN | DeleteVLAN | Yes | Yes — reads members+VNI from intent (Fix #10) | FIXED |
| vrf_ops.go | BindIPVPN | UnbindIPVPN | Yes + route_targets (Fix #11) | Yes — reads RTs from intent | FIXED |
| service_ops.go | ApplyService | RemoveService | Yes + route_policy_keys (Fix #13) | Yes — reads policy keys from intent | FIXED |
| service_ops.go | ApplyService (ACL) | RemoveService (ACL) | Yes — writes ACL intent (Fix #12) | Yes — reads from ACL intent | FIXED |
| vrf_ops.go | CreateVRF | DeleteVRF | Yes | deleteIntent only | OK |
| vrf_ops.go | AddStaticRoute | RemoveStaticRoute | Yes | deleteIntent only | OK |
| bgp_ops.go | AddBGPEVPNPeer | RemoveBGPEVPNPeer | Yes | deleteIntent only | OK |
| interface_ops.go | ConfigureInterface | UnconfigureInterface | Yes + dual-tracks VLAN | Yes (GetIntent) | OK |
| interface_bgp_ops.go | AddBGPPeer | RemoveBGPPeer | Yes | Yes (GetIntent) | OK |
| baseline_ops.go | SetupDevice | (reprovision) | Yes | N/A | EXEMPT |
| baseline_ops.go | ConfigureLoopback | RemoveLoopback | No | No | EXEMPT |
| bgp_ops.go | ConfigureBGP | RemoveBGPGlobals | No | No | EXEMPT |
| evpn_ops.go | SetupVTEP | TeardownVTEP | No | No | EXEMPT |

## Dead Code Removed

| File | Function | Reason |
|------|----------|--------|
| evpn_ops.go | `unmapVniConfig` | Replaced by intent-driven `deleteVniMapConfig` call |
| macvpn_ops.go | `unbindMacvpnConfig` | Replaced by intent-driven teardown |
| vlan_ops.go | `destroySviConfig` | Replaced by intent-driven `UnconfigureIRB` |
| service_ops.go | `deleteRoutePoliciesConfig` | Split into `deleteRoutePoliciesFromIntent` (intent) + `scanRoutePoliciesByPrefix` (refresh) |

## Verification

```
go build ./...           # PASS
go vet ./...             # PASS
go test ./... -count=1   # All newtron packages PASS
                         # Only pre-existing flaky TestBridgeStats in newtlab fails
```
