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

#### Discovery 3: CiscoVS Platform Boot & Provision (IN PROGRESS)
- **Timestamp:** 2026-02-17 16:55
- **Status:** boot-ssh PASS (< 1s), provision PASS (26s), bgp-converge RUNNING (step 2/2: verify-bgp-all)
- **Platform Details:**
  - Image: sonic-ciscovs.qcow2 (2.4GB)
  - HWSKU: cisco-8101-p4-32x100-vs (Gibraltar)
  - 32 ports, 100G, e1000 NIC driver
  - 6GB RAM, 6 vCPUs, 600s boot timeout
- **Observation:** Provision completed successfully, significantly faster than expected compared to VPP platform
- **Next:** Waiting for BGP convergence verification

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

**Current Phase:** Test execution - CiscoVS 2node-incremental suite
**Progress:** 2/32 scenarios passed (boot-ssh, provision), bgp-converge running
**Next Step:** Monitor BGP convergence, debug any failures in remaining scenarios
**Test Started:** 2026-02-17 16:55
**Monitoring:** Opus will spawn Sonnet agent to continue unattended monitoring and debugging
