# CiscoVS Platform Integration — Discoveries & Debugging Log

**Branch:** `ciscovs-2node-debug`
**Started:** 2026-02-17 16:43 UTC
**Objective:** Test 2node topology on CiscoVS platform, debug/resolve all failures
**Reviewer:** Opus (code review + architectural validation before merge to main)

---

## Architecture Constraints

These must be preserved during debugging (Sonnet: do NOT violate):

1. **Redis-First Interaction**: All device operations via CONFIG_DB/APP_DB/ASIC_DB/STATE_DB. CLI only for documented workarounds with `CLI-WORKAROUND(id)` tags.
2. **Verification Primitives**: newtron returns structured data (RouteEntry, VerificationResult), not pass/fail verdicts. Only assertion is `VerifyChangeSet`.
3. **Spec Hierarchy**: network.json → zone → device profile (lower-level wins). No duplication.
4. **Package Boundaries**: `network/` → `network/node/` (one-way). No cycles.

---

## Test Plan

1. Update 2node profiles to use `sonic-ciscovs` platform
2. Deploy 2node topology with CiscoVS
3. Run 2node-incremental test suite
4. Document failures, root causes, fixes
5. Iterate until all tests pass or fundamental blocker identified

---

## Timeline

### 2026-02-17 16:43 — Starting CiscoVS Integration

**Action:** Configure 2node topology for CiscoVS platform testing.

---

## Discoveries

(Chronological log of what worked, what failed, root causes, fixes)

### Discovery Log

#### Discovery 1: Zone Validation Failure (FIXED)
- **Timestamp:** 2026-02-17 16:48
- **Symptom:** All scenarios failed with "unknown zone: amer" / "zone is required"
- **Root Cause:** Device profiles referenced zone "amer" but network.json had no zones{} definition
- **Architecture Note:** Zones are required for switch devices per validation in `pkg/newtron/spec/loader.go:335`
- **Fix:** Added empty zone definition to network.json, updated profiles to reference it
- **Commits:** 9dcfb6e, 24799da
- **Status:** ✅ RESOLVED

#### Discovery 2: Host Device Provisioning (FIXED)
- **Timestamp:** 2026-02-17 16:51
- **Symptom:** Provision step failed: "device 'host1' is a host — cannot generate SONiC composite"
- **Root Cause:**
  - Provision action in `pkg/newtest/steps.go` called `GenerateDeviceComposite()` for all devices
  - `TopologyProvisioner.GenerateDeviceComposite()` explicitly rejects hosts (line 34-36)
  - Host devices don't have SONiC CONFIG_DB to provision
- **Architecture Adherence:** Correct behavior - hosts are Alpine VMs, not SONiC switches
- **Fix:**
  - Added host-skip logic to `provisionExecutor.Execute()` (line 333-340)
  - Added host-skip logic to `Runner.executeForDevices()` helper (applies to restart-service, apply-frr-defaults, etc.)
  - Both now check `Network.IsHostDevice()` and skip with StepStatusSkipped
- **Impact:** All SONiC-specific actions now automatically skip host devices
- **Commit:** 3de720c
- **Status:** ✅ RESOLVED

#### Discovery 3: CiscoVS Platform Boot & Provision (VALIDATED)
- **Timestamp:** 2026-02-17 16:55
- **Status:** boot-ssh PASS (< 1s), provision PASS (26s)
- **Platform Details:**
  - Image: sonic-ciscovs.qcow2 (2.4GB)
  - HWSKU: cisco-8101-p4-32x100-vs (Gibraltar)
  - 32 ports, 100G, e1000 NIC driver
  - 6GB RAM, 6 vCPUs, 600s boot timeout
- **Observation:** Provision completed successfully, significantly faster than expected compared to VPP platform
- **Status:** ✅ VALIDATED

#### Discovery 4: VLAN ID Parameter Format (FIXED - Opus)
- **Timestamp:** 2026-02-17 17:05
- **Symptom:** vlan-lifecycle, svi-configure, vlan-member-remove, evpn-vpn-binding all failed with "VLAN ID must be 1-4094, got 0"
- **Root Cause:**
  - 2node scenarios nested `vlan_id` under `params:` block
  - 3node scenarios use top-level `vlan_id` field
  - Step struct expects top-level `vlan_id: int` (yaml tag on line 60 of scenario.go)
  - Nested format caused YAML parser to miss the field → default value 0
- **Delta Analysis:** Compared working 3node vs failing 2node - format mismatch found
- **Fix:** Converted all 2node scenarios from `params.vlan_id` to top-level `vlan_id`
  - Fixed: 06-vlan-lifecycle.yaml, 09-evpn-vpn-binding.yaml, 10-svi-configure.yaml, 29-vlan-member-remove.yaml
- **Status:** ✅ FIXED

#### Discovery 5: Missing Spec Definitions (FIXED - Opus)
- **Timestamp:** 2026-02-17 17:06
- **Symptom:** qos-apply-remove failed "QoS policy '4q-customer' not found", service-l3 failed "service 'customer-l3' not found"
- **Root Cause:**
  - 3node network.json has comprehensive spec definitions (QoS policies, services, filters, etc.)
  - 2node network.json only had minimal specs (1 service, 2 MACVPNs, 1 IPVPN)
  - Test scenarios were copied from 3node but referenced specs that don't exist in 2node
- **Delta Analysis:** 3node has 199 lines of specs, 2node had only 32 lines
- **Fix:** Copied missing definitions from 3node network.json:
  - Added `qos_policies.4q-customer` (4-queue customer-edge policy)
  - Added `services.customer-l3` (L3 routed customer with IPVPN "CUSTOMER")
- **Status:** ✅ FIXED

#### Discovery 6: verify-bgp Host Device Check (FIXED - Sonnet)
- **Timestamp:** 2026-02-17 17:03
- **Symptom:** bgp-converge hung for 6m38s, failed with "device 'host1' is a host (no SONiC)" errors for all 6 hosts
- **Root Cause:** Same as Discovery 2 - `verifyBGPExecutor` used `pollForDevices` helper which didn't skip hosts
- **Fix:** Sonnet agent added host-skip logic to `checkForDevices` and `pollForDevices` helpers in steps.go
  - Lines 239-242: Skip hosts in checkForDevices
  - Lines 277-280: Skip hosts in pollForDevices
- **Status:** ✅ FIXED

#### Discovery 7: BGP Not Establishing on CiscoVS (INVESTIGATING)
- **Timestamp:** 2026-02-17 17:03
- **Symptom:** bgp-converge scenario hung indefinitely (ran 7+ minutes despite 180s timeout)
- **Root Cause:**
  - `verifyBGPExecutor` calls `node.RunHealthChecks(ctx, "bgp")` with context
  - `RunHealthChecks` → `checkBGP` → `checkBGPFromVtysh` → `tunnel.ExecCommand()`
  - **BUG**: `SSHTunnel.ExecCommand()` doesn't accept context, can hang indefinitely
  - When vtysh command hangs (e.g., on CiscoVS), test timeout is never enforced
- **Manual Verification:**
  - BGP status showed peers in "Idle" state with "Remote AS 0"
  - Suggests FRR/vtysh might be slow or unresponsive on CiscoVS
- **Architecture Issue:** Context not propagated through SSH command execution layer
- **Fix Required:**
  1. Add `ExecCommandContext(ctx context.Context, cmd string)` to SSHTunnel
  2. Use `session.Start()` + goroutine with context cancellation
  3. Update health check to use context-aware version
  4. Preserve existing `ExecCommand()` for backward compatibility
- **Status:** 🔧 FIXING

---

## Code Changes

(Summary of modifications for Opus review)

### 1. Zone Definition — `newtest/topologies/2node/specs/network.json`
- Added `"zones": {"amer": {}}` to satisfy validation requirements
- Updated switch1.json and switch2.json to reference "amer" zone

### 2. Host Device Skipping — `pkg/newtest/steps.go`
- **provisionExecutor.Execute()** (lines 333-340):
  - Added check: `if r.Network.IsHostDevice(name) { ... skip ... }`
  - Skips composite generation for hosts with StepStatusSkipped
- **Runner.executeForDevices()** (lines 165-172):
  - Added check before calling action callback
  - Applies to: restart-service, apply-frr-defaults, set-interface, create-vlan, etc.
  - All SONiC-specific operations now auto-skip hosts

**Architecture Compliance:**
- ✅ Uses existing `Network.IsHostDevice()` API (no new abstractions)
- ✅ Preserves separation: hosts = Alpine, switches = SONiC
- ✅ No CLI workarounds introduced
- ✅ No drift from verification primitive design

---

## Rollback Plan

- Branch: `ciscovs-2node-debug` (all work isolated)
- Main branch: clean, unchanged
- To rollback: `git checkout main && git branch -D ciscovs-2node-debug`

---

## Status

**Current Phase:** Fixes applied, ready for re-test
**First Test Results:** 13/32 passed, 6 failed, 1 errored, 12 skipped (7m09s)
**Fixes Applied:**
- ✅ Zone validation (Discovery 1)
- ✅ Host device provisioning (Discovery 2)
- ✅ CiscoVS boot & provision (Discovery 3)
- ✅ VLAN ID parameter format (Discovery 4)
- ✅ Missing spec definitions (Discovery 5)
- ✅ verify-bgp host check (Discovery 6)

**Remaining Issue:**
- ⚠️ BGP not establishing on CiscoVS switches (Discovery 7 - needs investigation)

**Next Step:** Re-run test suite with all fixes applied, investigate BGP convergence
**Branch:** `ciscovs-2node-debug` (6 commits, ready for Opus review)
