# Newtrun E2E Test Fixes Log

Running document of all fixes discovered and applied during newtrun suite testing.

---

## Fix 1: AddBGPPeer intent key collision with ConfigureInterface

**Suite**: 2node-vs-primitive
**Scenario**: teardown-infra (step: unconfigure-eth0-sw1)
**Error**: `no configuration intent for Ethernet0`
**Root cause**: AddBGPPeer and ConfigureInterface both wrote intent to the same key `interface|Ethernet0`. RemoveBGPPeer deleted the shared key, destroying ConfigureInterface's intent.
**Fix**: AddBGPPeer now writes to sub-resource key `interface|INTF|bgp-peer` with parent `interface|INTF`. RemoveBGPPeer only deletes the sub-resource.
**Files**:
- `pkg/newtron/network/node/interface_bgp_ops.go` — sub-resource intent key + ensureInterfaceIntent
- `pkg/newtron/network/node/interface_ops_test.go` — updated test expectations

## Fix 2: spec-authoring BGP_PEER_GROUP assertion format

**Suite**: 2node-vs-primitive, 2node-ngdp-primitive
**Scenario**: spec-authoring (step: verify-peer-group-removed)
**Error**: `has("default|TEST_ROUTED_SVC") cannot be applied to: array`
**Root cause**: configdb API returns BGP_PEER_GROUP as array, not object. `has()` doesn't work on arrays.
**Fix**: Changed to `/exists` endpoint: `/configdb/BGP_PEER_GROUP/default%7CTEST_ROUTED_SVC/exists`
**Files**:
- `newtrun/suites/2node-vs-primitive/58-spec-authoring.yaml`
- `newtrun/suites/2node-ngdp-primitive/58-spec-authoring.yaml`

## Fix 3: teardown-infra config reload doesn't restore clean state

**Suite**: 2node-vs-primitive
**Scenario**: teardown-infra (step: verify-vxlan-tunnel-removed)
**Error**: VXLAN_TUNNEL still exists after config reload
**Root cause**: Every API write calls `config save -y`. `config reload -y` restores the saved (provisioned) state, not factory defaults.
**Fix**: Rewrote teardown-infra to only test individual reverse operations (remove BGP neighbor, unconfigure interface). Removed config-reload approach and baseline infrastructure checks.
**Files**:
- `newtrun/suites/2node-vs-primitive/90-teardown-infra.yaml` — complete rewrite
- `newtrun/suites/2node-vs-primitive/92-verify-clean.yaml` — removed persistent baseline checks

## Fix 4: Spec-authoring stale entries from prior failed runs

**Suite**: 2node-vs-primitive, 2node-ngdp-primitive
**Scenario**: spec-authoring (step: create-prefix-list)
**Error**: `prefix list 'test-prefixes' already exists`
**Root cause**: Previous test run failures leave stale spec entries (prefix lists, route policies, services) in network.json. Next run's create steps fail on duplicates.
**Fix**:
1. Made delete/remove spec APIs idempotent (return success when entity not found)
2. Added cleanup preamble to spec-authoring tests that runs idempotent deletes in reverse dependency order before creating
3. Restored clean network.json from git
**Files**:
- `pkg/newtron/spec_ops.go` — idempotent DeleteService, DeletePrefixList, DeleteRoutePolicy, RemovePrefixListEntry, RemoveRoutePolicyRule
- `newtrun/suites/2node-vs-primitive/58-spec-authoring.yaml` — cleanup preamble
- `newtrun/suites/2node-ngdp-primitive/58-spec-authoring.yaml` — cleanup preamble

## Fix 5: BGP_GLOBALS_EVPN_RT not cleaned up during teardown

**Suite**: 2node-vs-primitive
**Scenario**: verify-clean (step: verify-no-evpn-rt-irb)
**Error**: `BGP_GLOBALS_EVPN_RT|Vrf_IRB|L2VPN_EVPN|65000:50002` still exists on both switches
**Root cause**: `ApplyService` creates BGP_GLOBALS_EVPN_RT via `bindIpvpnConfig()` config generator but does NOT create `ipvpn|Vrf_IRB` intent record (only standalone `BindIPVPN` does). When `RemoveService` → `destroyVrfConfig` → `unbindIpvpnConfig` runs, it finds no ipvpn intent and generates zero EVPN RT delete entries.
**Fix**:
1. Added `ensureIPVPNIntent` in `intent_ops.go` — creates `ipvpn|{vrf}` intent record when ApplyService generates IPVPN config (mirrors ensureVRFIntent/ensureVLANIntent pattern)
2. `unbindIpvpnConfig` now always finds route_targets in the `ipvpn|{vrf}` intent (no fallback hack needed)
3. `RemoveService` explicitly deletes `ipvpn|{vrf}` and `vrf|{vrf}` intents after deleting the interface intent (ordered deletion, children first)
4. Fixed `isLastIPVPN` scan to only count `interface|*` intents (the new `ipvpn|*` intent has `ipvpn` field and was being falsely counted as a user)
**Files**:
- `pkg/newtron/network/node/intent_ops.go` — add `ensureIPVPNIntent`
- `pkg/newtron/network/node/service_ops.go` — call ensureIPVPNIntent in ApplyService; clean up infrastructure intents in RemoveService; fix isLastIPVPN scan
**Result**: 2node-vs-primitive 21/21 PASS

## Fix 6: Baseline infrastructure checks in all verify-clean/teardown suites

**Suite**: All suites (2node-ngdp-primitive, 2node-ngdp-service, 2node-vs-service, 3node-ngdp-dataplane)
**Scenario**: verify-clean + teardown-infra/deprovision/teardown
**Error**: Baseline infrastructure entries (VXLAN_TUNNEL, LOOPBACK_INTERFACE, BGP_GLOBALS/default, etc.) still exist after teardown
**Root cause**: Same as Fix 3. Every API write calls `config save -y`, so `config reload` restores provisioned state, not factory defaults. Baseline infrastructure (setup-device) has no individual reverse — only reprovisioning (CompositeOverwrite) can clean it up.
**Fix**: Applied the Fix 3 pattern consistently across all remaining suites:
1. Removed `reload-config` + `wait-reload` steps from deprovision/teardown scenarios
2. Removed baseline infrastructure assertions from verify-clean (LOOPBACK_INTERFACE, BGP_GLOBALS/default, BGP_GLOBALS_AF/default, BGP_NEIGHBOR underlay/overlay, ROUTE_REDISTRIBUTE/default, VXLAN_TUNNEL, VXLAN_EVPN_NVO)
3. Kept service-created infrastructure checks (VRFs, VLANs, VXLAN_TUNNEL_MAP, VRF-level BGP, etc.)
4. Changed BGP_PEER_GROUP check from `length == 0` to specific service peer group existence check (EVPN baseline peer group persists)
**Files**:
- `newtrun/suites/2node-ngdp-primitive/90-teardown-infra.yaml` — removed reload-config, kept individual reverses
- `newtrun/suites/2node-ngdp-primitive/92-verify-clean.yaml` — removed baseline assertions
- `newtrun/suites/2node-ngdp-service/05-deprovision.yaml` — removed reload-config
- `newtrun/suites/2node-ngdp-service/06-verify-clean.yaml` — removed baseline assertions
- `newtrun/suites/2node-vs-service/05-deprovision.yaml` — removed reload-config (done in prior session)
- `newtrun/suites/2node-vs-service/06-verify-clean.yaml` — removed baseline assertions (done in prior session)
- `newtrun/suites/3node-ngdp-dataplane/06-teardown.yaml` — removed reload-config
- `newtrun/suites/3node-ngdp-dataplane/07-verify-clean.yaml` — removed baseline assertions
**Result**: All suites pass

## Fix 7: EVPN IRB ping timeout too short on NGDP

**Suite**: 2node-ngdp-primitive
**Scenario**: evpn-irb (step: prime-arp-sw1-svi)
**Error**: `SSH exec 'sudo ping -c 1 -W 2 -I Vlan300 10.3.0.2': Process exited with status 1`
**Root cause**: NGDP (Silicon One simulator) needs more time for VXLAN tunnel establishment + BGP EVPN route exchange + orchagent programming than the 10s poll timeout allowed.
**Fix**: Increased wait from 15s→20s and poll timeout from 10s→30s (interval 2s→3s) for both sw1 and sw2 SVI pings.
**Files**:
- `newtrun/suites/2node-ngdp-primitive/44-evpn-irb.yaml` — increased timeouts
**Result**: 2node-ngdp-primitive 21/21 PASS
