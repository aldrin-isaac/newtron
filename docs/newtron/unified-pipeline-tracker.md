# Unified Pipeline + Intent-Idempotent Primitives — Implementation Tracker

Implementation tracker for the plan at `.claude/plans/graceful-coalescing-shell.md`.
Each task maps to a specific function/struct change. Status: `[ ]` pending, `[~]` in progress, `[x]` resolved.

**Governing principles** (implementation-conformance.md):
- Composites MUST call owning primitives and merge their ChangeSets
- Each CONFIG_DB table MUST have exactly one owner
- For every forward action there MUST be a reverse action
- Intent records are written first, removed last — authoritative signal
- Same code path, different initialization (online vs offline)

---

## Phase 1: Unify op() and applyShadow()

### T1. `changeset.go` — `op()` (lines 163-167)
- [x] Remove `if n.offline` guard around `configDB.ApplyEntries` + accumulation
- [x] Replace with `n.applyShadow(cs)` after `buildChangeSet` — handles add/delete correctly
- [x] Fixes pre-existing delete bug: `ApplyEntries` was called for `ChangeDelete` (added instead of removed)

### T2. `node.go` — `applyShadow()` (lines 245-258)
- [x] Remove `!n.offline` early return (keep `cs == nil` check)
- [x] Move `n.accumulated = append(...)` inside per-change loop, gated on `n.offline`
- [x] Result: always updates configDB; only accumulates for offline mode

---

## Phase 2: Intent-Idempotent Primitives

### T3. `vlan_ops.go` — `CreateVLAN()` (line 144)
- [x] Add intent idempotency guard at function top:
      `if n.GetIntent("vlan|" + strconv.Itoa(vlanID)) != nil { return NewChangeSet(...), nil }`
- [x] Remove `RequireVLANNotExists` precondition (line 149)

### T4. `vrf_ops.go` — `CreateVRF()` (line 146)
- [x] Add intent idempotency guard at function top:
      `if n.GetIntent("vrf|" + name) != nil { return NewChangeSet(...), nil }`
- [x] Remove `RequireVRFNotExists` precondition (line 148)

### T5. `vrf_ops.go` — `BindIPVPN()` (line 227)
- [x] Add intent idempotency guard at function top:
      `if n.GetIntent("ipvpn|" + vrfName) != nil { return NewChangeSet(...), nil }`

### T6. `evpn_ops.go` — `BindMACVPN()` (line 106)
- [x] Add intent idempotency guard at function top:
      `if n.GetIntent("macvpn|" + strconv.Itoa(vlanID)) != nil { return NewChangeSet(...), nil }`

---

## Phase 3: Refactor ApplyService

### T7. `service_ops.go` — ApplyService: add `canBridge` (after line 137)
- [x] Add `canBridge` computation after `isOverlay`

### T8. `service_ops.go` — ApplyService: delete `generateServiceEntries` call (lines 193-216)
- [x] Delete `baseEntries, err := i.generateServiceEntries(ServiceEntryParams{...})` block
- [x] Delete all references to `baseEntries` variable

### T9. `service_ops.go` — ApplyService: pre-generate BGP entries (replaces lines 265-275)
- [x] Add BGP entry pre-generation BEFORE binding params construction
- [x] Delete old `baseEntries` peer AS scan (lines 265-275)

### T10. `service_ops.go` — ApplyService: replace ensure*Intent with primitives (lines 395-411)
- [x] Delete 3 `ensure*Intent` calls
- [x] Replace with infrastructure primitive calls (CreateVLAN, CreateVRF, BindIPVPN)
- [x] Each primitive result merged into `cs` via `cs.Merge()`
- [x] Verify ordering: primitives BEFORE interface intent write (I4 parent check)

### T11. `service_ops.go` — ApplyService: replace filter loop with direct calls (lines 442-499)
- [x] Delete the `for _, e := range baseEntries` filter loop
- [x] Add per-interface entries switch (with `vlanID > 0` guards)
- [x] Add `cs.Adds(bgpEntries)` for pre-generated BGP entries
- [x] Add ACL handling with platform check (`skipACL`)
- [x] Add QoS handling (bindQos + GenerateDeviceQoSConfig)
- [x] Delete old QoS device-wide block — now integrated

### T12. `service_gen.go` — `generateBGPPeeringConfig` signature change (line 214)
- [x] Change to individual params (dropped `ServiceEntryParams`)
- [x] Update function body to use individual params

### T13. `service_gen.go` — `generateServiceEntries` update (line 196)
- [x] Update internal call to unpack `ServiceEntryParams`
- [x] Add `// Test helper` comments to `generateServiceEntries` and `ServiceEntryParams`

### T14. `service_gen.go` → `acl_ops.go` — move `mapFilterType` (lines 22-30)
- [x] Move `mapFilterType` function from `service_gen.go` to `acl_ops.go`
- [x] Verified: callers in service_ops.go and service_gen.go (same package)

---

## Phase 4: Delete ensure*Intent

### T15. `intent_ops.go` — delete `ensureVLANIntent` (lines 189-198)
- [x] Deleted function body and signature

### T16. `intent_ops.go` — delete `ensureVRFIntent` (lines 201-211)
- [x] Deleted function body and signature

### T17. `intent_ops.go` — delete `ensureIPVPNIntent` (lines 213-235)
- [x] Deleted function body and signature + cleaned unused imports

---

## Phase 5: Verification

### T18. Build verification
- [x] `go build ./... && go vet ./...` passes

### T19. Unit tests
- [x] `go test ./... -count=1` passes — ALL previously passing tests still pass
- [x] `TestCreateVLAN_AlreadyExists` renamed to `TestCreateVLAN_IntentIdempotent` — tests new behavior

### T20. Grep verification
- [x] `grep -r ensureVLANIntent` — zero hits in Go files
- [x] `grep -r ensureVRFIntent` — zero hits in Go files
- [x] `grep -r ensureIPVPNIntent` — zero hits in Go files
- [x] `grep -r generateServiceEntries` — only in `service_gen.go` + `service_gen_test.go` (+ comment in `service_ops.go`)

### T21. Post-implementation conformance audit
- [x] Composites call primitives: ApplyService calls CreateVLAN/CreateVRF/BindIPVPN
- [x] Single CONFIG_DB owner: all entries from owning files' functions
- [x] Operational symmetry: all forward primitives have reverse counterparts
- [x] Intent self-sufficiency: primitives store complete params
- [x] Intent round-trip: primitives' intent params → export → ReplayStep → same method
- [x] Same code path: op()/applyShadow() unified; only accumulation differs
- [x] No unnecessary new functions
- [x] Not a hack: idempotency is the contract, not a workaround
