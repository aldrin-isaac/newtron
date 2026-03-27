# Phase 5: Eliminate Parallel Mechanisms — Tracker

Implementation tracker for Phase 5 of the unified pipeline plan at
`.claude/plans/graceful-coalescing-shell.md`.

Status: `[ ]` pending, `[~]` in progress, `[x]` resolved.

---

## Phase 5A: Eliminate `accumulated`

### T1. `structToFields` helper (`configdb.go`)
- [x] **T1.1** Add `structToFields(v any) map[string]string` — reflection-based serializer using json tags
- [x] **T1.2** Zero-value fields omitted (matches config function behavior)

### T2. `ExportEntries` method (`configdb.go`)
- [x] **T2.1** Add `ExportEntries() []Entry` method on ConfigDB
- [x] **T2.2** Switch statement mirroring `applyEntry` — one case per typed table
- [x] **T2.3** Raw `map[string]string` tables: iterate map, copy fields directly
- [x] **T2.4** Add `reflect` import

### T3. Update `BuildComposite` (`node.go`)
- [x] **T3.1** Replace `n.accumulated` iteration with `n.configDB.ExportEntries()`

### T4. Delete `accumulated` field and logic (`node.go`)
- [x] **T4.1** Remove `accumulated []sonic.Entry` from Node struct
- [x] **T4.2** Remove accumulation from `applyShadow`
- [x] **T4.3** Remove accumulation from `RegisterPort`
- [x] **T4.4** Delete `AddEntries` method
- [x] **T4.5** Update `applyShadow` doc comment — remove "accumulates entries" language

### T5. Simplify `applyIntentToShadow` (`intent_ops.go`)
- [x] **T5.1** Remove `if n.offline { n.accumulated = append(...) }` block
- [x] **T5.2** Update doc comment — remove "accumulated for composite export" language

### T6. Tests (`configdb_test.go`)
- [x] **T6.1** `TestExportEntries_RoundTrip` — apply entries, export, verify fields match
- [x] **T6.2** `TestStructToFields` — direct test of reflection helper

---

## Phase 5B: Eliminate `cascadeDeleteChildren`

### T7. Delete `cascadeDeleteChildren` (`intent_ops.go`)
- [x] **T7.1** Delete `cascadeDeleteChildren` function

### T8. Remove call from `UnconfigureInterface` (`interface_ops.go`)
- [x] **T8.1** Remove `cascadeDeleteChildren` call
- [x] **T8.2** Remove associated comment

### T9. Remove call from `RemoveService` (`service_ops.go`)
- [x] **T9.1** Remove `cascadeDeleteChildren` call
- [x] **T9.2** Remove associated comment
- [x] **T9.3** Add explicit ACL binding intent deletion in `removeSharedACL` non-last-user branch (I5 compliance)

---

## Phase 5C: True Up Architecture

### T10. Rename `pc|` → `portchannel|` (`portchannel_ops.go`)
- [x] **T10.1** Update `AddPortChannelMember` writeIntent: `"pc|"` → `"portchannel|"`
- [x] **T10.2** Update `RemovePortChannelMember` deleteIntent: `"pc|"` → `"portchannel|"`

### T11. Update `reconstruct.go`
- [x] **T11.1** No changes needed — replay goes through CreatePortChannel which calls AddPortChannelMember

### T12. Update `device_ops_test.go`
- [x] **T12.1** Update test fixtures: `"pc|"` → `"portchannel|"` in intent keys

### T13. Add intent-idempotent guards to missing shared infrastructure primitives
- [x] **T13.1** `CreatePortChannel` — add intent guard
- [x] **T13.2** `CreateACL` — add intent guard
- [x] **T13.3** `ConfigureIRB` — add intent guard

### T14. Update `docs/newtron/intent-dag.md`
- [x] **T14.1** §4.1/§4.2: Remove "Signature change" framing
- [x] **T14.2** §4.3: Delete `updateIntent` section (eliminated)
- [x] **T14.3** §5.6: Add implementation note about cascadeDeleteChildren elimination
- [x] **T14.4** §6.3: Update tense (topological sort is implemented)
- [x] **T14.5** §7: Update dual-tracking elimination to present tense
- [x] **T14.6** §10.2: Remove `updateIntent` rows from vlan table
- [x] **T14.7** §10.4: Remove `updateIntent` rows from acl table
- [x] **T14.8** §10.5: Remove `updateIntent` rows from portchannel table
- [x] **T14.9** §10.7: Update `add-bgp-peer` to reflect sub-resource pattern
- [x] **T14.10** Add new §10.X for `interface|INTF|bgp-peer`
- [x] **T14.11** §10.11.1: Document `ensureInterfaceIntent` / `OpInterfaceInit`
- [x] **T14.12** §10.17: Rename `pc|` → `portchannel|` in catalog
- [x] **T14.13** §10.18: Add row 18 (`interface|INTF|bgp-peer`), update count
- [x] **T14.14** §13.1: Update reconstruction to present tense
- [x] **T14.15** §16: Reframe as architecture characteristics
- [x] **T14.16** Add unified pipeline integration section
- [x] **T14.17** Document intent-idempotent primitive principle
- [x] **T14.18** General tense pass throughout document

---

## Phase 5D: Verification

### T15. Build verification
- [x] **T15.1** `go build ./... && go vet ./...`

### T16. Test verification
- [x] **T16.1** `go test ./... -count=1` — all previously passing tests still pass

### T17. Grep verification
- [x] **T17.1** `grep -rn 'accumulated' pkg/newtron/network/node/` — zero hits
- [x] **T17.2** `grep -rn 'cascadeDeleteChildren' pkg/newtron/` — zero hits
- [x] **T17.3** `grep -rn '"pc|"' pkg/newtron/` — zero hits (renamed to `portchannel|`)
- [x] **T17.4** `grep -rn 'applyIntentToShadow' pkg/newtron/network/node/intent_ops.go` — single-line body
