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

_Will be populated as testing progresses..._

---

## Code Changes

(Summary of modifications for Opus review)

### Changes Made

_Will be populated as fixes are implemented..._

---

## Rollback Plan

- Branch: `ciscovs-2node-debug` (all work isolated)
- Main branch: clean, unchanged
- To rollback: `git checkout main && git branch -D ciscovs-2node-debug`

---

## Status

**Current Phase:** Initial setup
**Next Step:** Update 2node profiles to sonic-ciscovs, deploy lab
