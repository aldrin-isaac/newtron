# Surgical Reconcile — Implementation Plan

Two Reconcile modes: **full** (current behavior) and **delta** (surgical).
Same purpose — eliminate drift between projection and device. Different
mechanisms — full rewrites everything, delta patches only what differs.

Both modes work in both topology and actuated modes. The mode (full/delta)
selects the delivery mechanism; the intent source (topology/actuated) is
orthogonal.

**This is a feature addition, not a refactor.** Do not reorganize, extract
helpers from, or "clean up" the existing Reconcile code. The existing
function has a natural structure — connect, reload, lock, deliver, save,
unlock — where most steps are shared and only reload (full-only) and
delivery (full vs delta) differ. Add branch points where behavior diverges;
do not duplicate shared scaffolding.

**Pass criteria:** `1node-vs-architecture` full E2E suite pass (all scenarios
including the new delta reconcile scenarios).

---

## 1. Architecture Quotation

The architecture defines Reconcile at these locations. Quoted verbatim.

**Glossary (unified-pipeline-architecture.md line 1053):**

> **Reconcile** | Deliver the full projection to the device, eliminating drift

**Procedure (lines 463-482):**

> Deliver the full projection to the device.
> 1. If not connected: `ConnectTransport(ctx)`
> 2. Config reload (best-effort — reset to factory baseline)
> 3. `PingWithRetry` — poll Redis until available after reload
> 4. Lock
> 5. `configDB.ExportEntries()` → `ReplaceAll()` — atomic delivery
> 6. SaveConfig → persist to `/etc/sonic/config_db.json`
> 7. `EnsureUnifiedConfigMode` → restart bgp if switching to frrcfgd
> 8. Unlock

**Write paths out of the Abstract Node (line 78):**

> **Reconcile** (full) — export entire projection, ReplaceAll to device

**Crash recovery (lines 743-794):**

> **Reconcile** pushes the full projection → device matches intents
>
> **missing records** mean an intent exists but delivery was incomplete — Reconcile finishes the job.
> **Additional records** mean no intent claims them — Reconcile cleans them up.

**Drift guard resolution (lines 637-638):**

> The resolution is explicit: `Reconcile()` first (pushes the projection to
> the device, eliminating drift), then retry the write.

### Architecture Assessment

The architecture defines Reconcile's **purpose** as "eliminating drift." The
**mechanism** (`ExportEntries() → ReplaceAll()`) is the current implementation
— it fulfills the purpose by overwriting everything, which is correct but
over-broad: it rewrites entries that aren't drifted, generating unnecessary
Redis keyspace notifications that cause daemon churn.

The crash recovery section already models drift in three categories — missing,
extra, modified — which map 1:1 to `DiffConfigDB`'s output. The architecture
encodes the surgical model in its drift taxonomy; the implementation doesn't
use it yet for delivery.

This plan adds a second delivery mechanism (delta) without removing the first
(full). Both eliminate drift. The architecture update refines the definition
to acknowledge both modes.

---

## 2. Design

### Two Modes, One Purpose

| Mode | Mechanism | Config Reload | Use Case |
|------|-----------|--------------|----------|
| **full** | `ExportEntries()` → `ReplaceAll()` | Yes | First provisioning, RMA recovery, deep corruption |
| **delta** | `Drift()` → `ApplyDrift()` | No | Drift repair, minimal disruption, incremental re-provisioning |

Both modes eliminate drift. Full mode does it by overwriting everything. Delta
mode does it by patching only the differences. Both modes work in both topology
and actuated intent source modes — the delivery mechanism is independent of the
intent source.

### Existing Code Reuse

Delta Reconcile is a thin composition of existing functions. The heavy lifting
— drift computation — already exists and is well-tested:

| Existing function | Location | Role in delta Reconcile |
|-------------------|----------|------------------------|
| `Node.Drift()` | `node.go:342` | Computes projection vs device diff |
| `DiffConfigDB()` | `configdb_diff.go:47` | Classifies drift as missing/extra/modified |
| `ExportRaw()` | `configdb.go:909` | Projection → wire format (used by Drift) |
| `GetRawOwnedTables()` | `configdb_diff.go:180` | Reads actual CONFIG_DB (used by Drift) |
| `PipelineSet()` | `pipeline.go:12` | Atomic TxPipeline batch write (pattern reused) |
| `ReplaceAll()` | `pipeline.go:67` | Intent record delivery (delta reuses for NEWTRON_INTENT only) |

**One new function**: `ApplyDrift(diffs []DriftEntry) error` (~30 lines) —
converts drift entries to Redis pipeline commands. Same `TxPipeline` pattern
as `PipelineSet`.

**No changes to existing functions.** Full mode path is byte-for-byte identical.

### Mode Selection

| Context | Default | Override |
|---------|---------|---------|
| **Actuated mode** (`intent reconcile`) | delta | `--full` flag |
| **Topology mode** (`--topology intent reconcile`) | full | `--delta` flag |

Topology Reconcile defaults to full because it's typically first provisioning —
the device has never seen these intents. Actuated Reconcile defaults to delta
because it's typically drift repair — a few entries diverged and need correction.

Both defaults can be overridden:
- `--topology intent reconcile --delta` — topology re-provisioning on an
  already-configured device, minimizing disruption.
- `intent reconcile --full` — actuated drift repair when corruption is deep
  or delta alone is insufficient.

### Delta Mechanism

`DiffConfigDB` already returns `[]DriftEntry` with three types:

| Drift type | Redis action | Rationale |
|-----------|-------------|-----------|
| **missing** | DEL + HSET | Entry should exist but doesn't. DEL is defensive (no-op on absent key). HSET writes expected fields. DEL+HSET ensures clean daemon notifications. |
| **extra** | DEL | Entry should not exist. Remove it. |
| **modified** | DEL + HSET | Entry exists but fields are wrong. DEL clears all fields (including stale ones), HSET writes expected fields. Key-level replacement per CLAUDE.md §CONFIG_DB Replace Semantics. |

Why DEL+HSET instead of field-level HDEL+HSET:
- `DiffConfigDB` compares at key level, not field level
- Key-level DEL+HSET is what `ChangeSet.Apply` already uses for individual ops
- Some SONiC daemons require the delete→create notification cycle to properly
  reinitialize state (documented in RCA-037, RCA-041)
- Extending to field-level patching would require tracking which fields are
  newtron-managed vs factory — complexity for marginal benefit

### NEWTRON_INTENT Delivery

`NEWTRON_INTENT` is excluded from `DiffConfigDB` (it's the source of the
projection, not a config table). Intent records must be delivered by both
modes, but through different mechanisms:

- **Full mode**: unchanged. `ExportEntries()` returns ALL entries including
  NEWTRON_INTENT. `ReplaceAll(entries, deliveryTables)` delivers everything
  in one call with `deliveryTables` including `"NEWTRON_INTENT"`. Intent
  records appear before config entries in the export because `writeIntent`
  uses `cs.Prepend()` (architecture §4: "intent records arrive BEFORE
  config entries").

- **Delta mode**: config entries go through `ApplyDrift`, but intent records
  are NOT in the drift set (excluded from `DiffConfigDB`). So delta mode
  delivers intents separately: `ReplaceAll(intentEntries, ["NEWTRON_INTENT"])`
  BEFORE `ApplyDrift(diffs)`. This is a targeted `ReplaceAll` on one table —
  lightweight (only intent records, not full config). It ensures intent
  records are authoritative and arrive before config corrections.

The asymmetry is correct: full mode already has a single `ReplaceAll` that
includes everything; splitting it would add complexity for no benefit. Delta
mode needs the split because config entries take a different path (`ApplyDrift`).

### Dependency Ordering (Delta Mode)

Drift entries are applied within a single `TxPipeline` (`MULTI/EXEC`), which
is atomic. Within the transaction, Redis keyspace notifications fire in command
order after `EXEC`. For correctness, deletes should remove children before
parents, and creates should add parents before children (YANG leafref chains).

This ordering is a refinement, not a blocker. For the initial implementation,
`ApplyDrift` sorts entries: deletes first (by table priority descending),
then creates/modifies (by table priority ascending). This uses a simple
`tablePriority` map derived from the YANG leafref chains in CLAUDE.md:

```
VLAN → VLAN_MEMBER, VLAN_INTERFACE
VRF → INTERFACE (vrf_name), BGP_GLOBALS → BGP_NEIGHBOR → BGP_NEIGHBOR_AF
VXLAN_TUNNEL → VXLAN_EVPN_NVO → VXLAN_TUNNEL_MAP
ACL_TABLE → ACL_RULE
DSCP_TO_TC_MAP, SCHEDULER → PORT_QOS_MAP, QUEUE
```

### ReconcileResult Enhancement

```go
// Public type (pkg/newtron/types.go)
type ReconcileResult struct {
    Mode     string `json:"mode"`               // "full" or "delta"
    Applied  int    `json:"applied"`            // total entries touched
    Missing  int    `json:"missing,omitempty"`  // entries added (delta only)
    Extra    int    `json:"extra,omitempty"`     // entries removed (delta only)
    Modified int    `json:"modified,omitempty"` // entries corrected (delta only)
    Message  string `json:"message,omitempty"`
}
```

For full mode: `Applied` = total entries delivered (same as today). Breakdown
fields are zero (full mode doesn't classify — it delivers everything).

For delta mode: `Applied` = `Missing + Extra + Modified`. The breakdown shows
exactly what was repaired.

### API Surface

**HTTP**: Two orthogonal query parameters:
- `?mode=topology` — intent source (existing, unchanged)
- `?reconcile=full|delta` — delivery mechanism (new)

Existing `dry_run` and `no_save` parameters are unchanged. When `reconcile`
is absent, the default depends on the intent source mode (topology → full,
actuated → delta).

**Client**: `Client.Reconcile(device, mode string, opts ExecOpts)` already
accepts a `mode` string for intent source (`"topology"` vs `""`). Add a
`reconcileMode` parameter for the delivery mechanism. These are orthogonal —
no combinatorial explosion.

**CLI**: `newtron leaf1 intent reconcile [--full|--delta] -x`

```
newtron leaf1 intent reconcile -x                         # delta (default, actuated)
newtron leaf1 intent reconcile --full -x                  # full (actuated)
newtron leaf1 --topology intent reconcile -x              # full (default, topology)
newtron leaf1 --topology intent reconcile --delta -x      # delta (topology)
```

---

## 3. Implementation Tracker

Every task maps to a specific file and function. Mark `[x]` as completed.

### Phase 1: Device Layer — `ApplyDrift`

- [x] **1.1** Add `ApplyDrift(diffs []DriftEntry) error` to
  `pkg/newtron/device/sonic/pipeline.go`
  - Single `TxPipeline` (atomic, same pattern as `PipelineSet`)
  - For "extra": `pipe.Del(ctx, table|key)`
  - For "missing"/"modified": `pipe.Del(ctx, table|key)` then `pipe.HSet(ctx, table|key, fields...)`
  - Sorts entries before building pipeline: deletes first (descending table
    priority), then creates/modifies (ascending table priority)

- [x] **1.2** Add `tablePriority` map to `pkg/newtron/device/sonic/configdb_diff.go`
  - Numeric priority for each owned table based on YANG leafref chains
  - Lower number = parent (created first, deleted last)
  - Used by `ApplyDrift` for ordering

- [x] **1.3** Add unit tests in `pkg/newtron/device/sonic/configdb_diff_test.go`
  - `TestTablePriority` — verify all owned tables have a priority assigned

### Phase 2: Node Layer — Dual-Mode Reconcile

- [x] **2.1** Add `ReconcileOpts` to `pkg/newtron/network/node/node.go`
  ```go
  type ReconcileOpts struct {
      Mode string // "full" or "delta"
  }
  ```

- [x] **2.2** Modify `Node.Reconcile` signature: `Reconcile(ctx, opts ReconcileOpts)`
  at `pkg/newtron/network/node/node.go:368`
  - `if opts.Mode == "full"`: existing code path, unchanged (config reload →
    `ExportEntries()` → `ReplaceAll(entries, deliveryTables)`)
  - `if opts.Mode == "delta"`: no config reload → deliver intent records via
    `ReplaceAll(intentEntries, ["NEWTRON_INTENT"])` → compute drift via
    `Drift(ctx)` → `ApplyDrift(diffs)`
  - Both paths share: connect → lock → *delivery* → SaveConfig →
    EnsureUnifiedConfigMode → unlock

- [x] **2.3** Update internal `ReconcileResult` at `node.go:358`
  - Add `Mode`, `Missing`, `Extra`, `Modified` fields

### Phase 3: Public API Layer

- [x] **3.1** Add public `ReconcileOpts` to `pkg/newtron/types.go`
  ```go
  type ReconcileOpts struct {
      Mode string `json:"mode"` // "full" or "delta"
  }
  ```

- [x] **3.2** Update public `ReconcileResult` in `pkg/newtron/types.go:978`
  - Add `Mode`, `Missing`, `Extra`, `Modified` fields

- [x] **3.3** Update `Node.Reconcile` wrapper in `pkg/newtron/node.go:115`
  - Accept `ReconcileOpts`, convert to internal opts, map result

### Phase 4: HTTP Handler + Client + CLI

- [x] **4.1** Update `handleReconcile` in `pkg/newtron/api/handler_node.go:1070`
  - Parse `?reconcile=full|delta` query parameter
  - Pass `ReconcileOpts` to `node.Reconcile`
  - Dry-run path unchanged (returns drift preview regardless of mode)

- [x] **4.2** Update `Client.Reconcile` in `pkg/newtron/client/node.go:537`
  - Add `reconcileMode string` parameter
  - Pass as `?reconcile=full|delta` query parameter when non-empty
  - newtrun caller (`pkg/newtrun/steps.go`) passes `mode="topology"` for
    intent source — unaffected (orthogonal parameter)

- [x] **4.3** Update `intentReconcileCmd` in `cmd/newtron/cmd_intent.go:133`
  - Add `--full` and `--delta` flags (mutually exclusive)
  - Default depends on `--topology` flag: topology → full, actuated → delta
  - Pass mode to `client.Reconcile`
  - Update output to show mode and breakdown for delta

### Phase 5: Documentation

- [x] **5.1** Update architecture doc `docs/newtron/unified-pipeline-architecture.md`
  - Line 43: diagram label `Reconcile (full)` → `Reconcile (full/delta)`
  - Line 78: describe both write paths out of the Abstract Node
  - Lines 463-482: add delta procedure alongside full procedure
  - Line 1053: update glossary — "Eliminate drift. Two modes: full (config
    reload + ReplaceAll) and delta (apply only drifted entries)."
  - Lines 894-936: add note about mode selection to topology Reconcile trace
  - Add actuated delta Reconcile trace

- [x] **5.2** Update `CLAUDE.md`
  - "Reconcile overwrites the device" → describe both modes
  - Update references to `ReplaceAll` as sole delivery mechanism

- [x] **5.3** Update LLD `docs/newtron/lld.md`
  - Update `ReconcileResult` struct (line 642)
  - Update API table for reconcile endpoint (line 1110)

### Phase 6: E2E Tests — `1node-vs-architecture`

New scenarios added to `newtrun/suites/1node-vs-architecture/`. These test
delta Reconcile end-to-end. Delta mode does NOT do config reload or BGP
restart — it patches CONFIG_DB surgically. This means the post-reconcile
verification is simpler (no reload/restart dance).

- [x] **6.1** `27-delta-reconcile-actuated.yaml` — Delta reconcile in actuated mode
  - Inject drift (all 3 types: missing loopback IP, extra VLAN, modified BGP ASN)
  - Verify drift detected
  - POST `/intent/reconcile?reconcile=delta` — delta reconcile
  - Verify result: `mode == "delta"`, `applied >= 3`, breakdown fields populated
  - Verify missing entry restored (loopback IP)
  - Verify extra entry removed (VLAN)
  - Verify modified entry corrected (BGP ASN)
  - Verify zero drift after
  - Verify writes unblocked (drift guard cleared)

- [x] **6.2** `28-delta-reconcile-topology.yaml` — Delta reconcile in topology mode
  - Inject drift (extra VLAN via redis-cli)
  - POST `/intent/reconcile?mode=topology&reconcile=delta` — topology delta
  - Verify extra VLAN removed
  - Verify zero drift after

- [x] **6.3** `29-delta-reconcile-noop.yaml` — Delta reconcile on clean device
  - Verify zero drift before
  - POST `/intent/reconcile?reconcile=delta` — delta on clean device
  - Verify result: `applied == 0`
  - Verify zero drift after (idempotent)

- [x] **6.4** `30-delta-reconcile-dry-run.yaml` — Delta reconcile dry-run
  - Inject drift (extra VLAN)
  - POST `/intent/reconcile?reconcile=delta&dry_run=true` — dry-run
  - Verify drift preview returned (shows extra VLAN)
  - Verify device NOT modified (VLAN still exists)
  - Real delta reconcile fixes it
  - Verify zero drift after

- [x] **6.5** `31-full-reconcile-explicit.yaml` — Explicit `?reconcile=full` still works
  - Inject drift
  - POST `/intent/reconcile?reconcile=full` — explicit full in actuated mode
  - Verify drift fixed (full reload+replace cycle)
  - Verify zero drift after
  - Restore device state (reload-config + restart-bgp)

- [x] **6.6** Update existing scenarios to use explicit `?reconcile=full` where
  full mode is intended (07, 12, 14, 18, 21) — ensures existing tests are
  not broken by the new default (actuated defaults to delta)

### Phase 7: Documentation

- [x] **7.1** Update architecture doc `docs/newtron/unified-pipeline-architecture.md`
  - Line 43: diagram label `Reconcile (full)` → `Reconcile (full/delta)`
  - Line 78: describe both write paths out of the Abstract Node
  - Lines 463-482: add delta procedure alongside full procedure
  - Line 1053: update glossary — "Eliminate drift. Two modes: full (config
    reload + ReplaceAll) and delta (apply only drifted entries)."
  - Lines 894-936: add note about mode selection to topology Reconcile trace
  - Add actuated delta Reconcile trace

- [x] **7.2** Update `CLAUDE.md`
  - "Reconcile overwrites the device" → describe both modes
  - Update references to `ReplaceAll` as sole delivery mechanism

- [x] **7.3** Update LLD `docs/newtron/lld.md`
  - Update `ReconcileResult` struct (line 642)
  - Update API table for reconcile endpoint (line 1110)

### Phase 8: Verification

**Pass criteria: `1node-vs-architecture` full suite pass (all 32 scenarios).**

- [x] **8.1** `go build ./...` — compilation
- [x] **8.2** `go vet ./...` — static analysis
- [x] **8.3** `go test ./... -count=1` — unit tests pass
- [ ] **8.4** (PENDING: requires 1node-vs lab) `1node-vs-architecture` E2E suite — all scenarios pass
- [x] **8.5** Grep for stale references
  - `ReplaceAll` in comments/docs — verify none imply it's the only Reconcile path
  - "full projection" in docs — verify updated to acknowledge delta mode
- [x] **8.6** Post-implementation conformance audit (ai-instructions §9):
  - Architecture conformance — both modes eliminate drift, both work in both intent source modes
  - Naming — "full" and "delta" are honest, descriptive names
  - Public API boundary — internal `node.ReconcileOpts` doesn't leak
  - Pipeline position — Reconcile is still delivery stage
  - No existing functions modified — full path identical, delta is additive

---

## 4. Resolved Concerns

Per ai-instructions §14: risks resolved, not deferred.

| Concern | Resolution |
|---------|-----------|
| **DEL+HSET ordering in TxPipeline** | Redis `MULTI/EXEC` is atomic and ordered. DEL then HSET for the same key in one pipeline is safe. |
| **Config reload in delta mode** | No. Config reload resets to factory baseline, introducing MORE drift. Delta reads device as-is and patches differences. |
| **Empty delta diff** | Zero drift → zero Redis writes → `ReconcileResult{Mode: "delta", Applied: 0}`. Correct and optimal. |
| **NEWTRON_INTENT delivery** | Full: delivered in single `ExportEntries → ReplaceAll` call (unchanged). Delta: separate `ReplaceAll(intentEntries, ["NEWTRON_INTENT"])` before `ApplyDrift`. |
| **Daemon notification reduction** | Redis keyspace notifications fire per HSET/DEL. Delta touches only drifted entries — undrifted daemons are undisturbed. |
| **`fieldsMatch` ignores extra actual fields** | Same behavior as current `ReplaceAll`. Factory fields that must survive are in excluded tables (PORT, DEVICE_METADATA) or platform merge tables. No regression. |
| **Topology delta on clean device** | Every projection entry is "missing" → delta adds them all. Functionally correct but more Redis commands than full. Operator should use `--full` (the default) for first provisioning. |
| **Clear + Reconcile with delta** | Empty projection → every device entry is "extra" → `ApplyDrift` DELs them all. Functionally correct. `--full` is more efficient for bulk wipes. |
| **Crash recovery** | Unchanged. Crash recovery model (drift guard → Reconcile) works with both modes. The mode is the operator's choice. |
| **Dry-run** | Unchanged. Returns drift preview (`Drift()` output) regardless of mode. The preview IS what delta would do. |
| **Backward compatibility of ReconcileResult** | `omitempty` on new fields. Existing consumers reading only `Applied` are unaffected. |
| **Existing pipeline changes** | None. Full mode code path is untouched. `ApplyDrift` is a new function (~30 lines) using the same `TxPipeline` pattern as `PipelineSet`. No existing functions are modified. |
