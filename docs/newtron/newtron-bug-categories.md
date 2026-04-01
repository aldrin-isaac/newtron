# Newtron Bug Categories

Taxonomy of bugs discovered during E2E test execution, with examples from
`test-run-fixes.md`. Each category describes a failure pattern, explains
why the pattern exists, and identifies where to look when a new bug matches.

---

## 1. Reconstruction Invariant Violation

**Pattern**: The projection after `RebuildProjection` (intent replay) differs
from the projection after the original operations. Entries present on the
device are missing from the reconstructed projection → false drift.

**Root cause**: A config method conditionally generates entries based on
sub-operation freshness or transient state that doesn't survive reconstruction.
During replay, intents execute in topological order — earlier intents have
already populated the projection, so sub-operations that check intent
existence return "already exists" and skip entry generation.

**Signature**: Drift guard fires with "extra on device" for entries that
newtron itself created. The entry is correct and expected — the projection
is wrong.

**Where to look**: Any `if !subCS.IsEmpty() { ... }` guard, or any
conditional entry generation that depends on whether a sub-operation
"just created" something vs "it already existed."

**Fix pattern**: Remove the freshness guard. Generate entries unconditionally
when the intent/spec says they should exist. `render(cs)` handles upserts
safely — duplicate entries are harmless.

**Should this affect other ops?** Yes — any composite operation that creates
infrastructure via intent-idempotent sub-operations and conditionally adds
entries based on sub-operation return. Searched all `IsEmpty()` guards in
the node package — Fix 18 was the only instance. But the broader principle
applies: never gate entry generation on whether a sub-operation "did work."

| Fix | Example |
|-----|---------|
| 18 | ARP suppression gated on `!vlanCS.IsEmpty()` — VLAN already existed during replay |

---

## 2. RebuildProjection State Management

**Pattern**: `RebuildProjection` fails to correctly manage Node state flags
(`unsavedIntents`, `actuatedIntent`) or pre-populates state that causes
replay methods to behave differently than during original execution.

**Root cause**: `RebuildProjection` reuses the same Node object and config
methods as interactive execution, but operates in a different context
(reconstruction vs. mutation). State flags set by side effects of config
methods must be saved/restored or suppressed during replay.

**Signature**: Mode-switch guards fire incorrectly ("unsaved intents" when
there are none), preconditions fail ("not locked" during replay), or config
methods return empty ChangeSets ("intent already exists") because the
intent DB was pre-populated.

**Where to look**: `node.go:RebuildProjection`, flags set by `writeIntent`,
`precondition()` checks.

**Fix pattern**: Save/restore transient flags across replay. Suppress mode
checks (like `actuatedIntent`) during reconstruction. Never pre-populate
the intent DB before replay — let config methods build it naturally.

**Should this affect other ops?** No — this category is specific to
`RebuildProjection`. All four fixes were discovered together during the
initial implementation. The category is "closed" unless `RebuildProjection`
logic changes.

| Fix | Example |
|-----|---------|
| 1 | `unsavedIntents` not cleared after replay |
| 2 | `actuatedIntent` not suppressed during replay → precondition failures |
| 3 | Pre-populated intent DB caused config methods to skip rendering |
| 7 | Fix 1 was too broad — cleared real unsaved state from pre-rebuild mutations |

---

## 3. Intent Round-Trip Incompleteness

**Pattern**: An intent record doesn't store enough params for its
`ReplayStep` case to reproduce the same CONFIG_DB entries. After
reconstruction, entries are missing → false drift or broken operations.

**Root cause**: `writeIntent` omits a param that affects CONFIG_DB output,
or the operation is wrongly listed in `skipInReconstruct` (assuming a
parent operation would recreate it, when it doesn't).

**Signature**: After reconstruction, specific CONFIG_DB entries are missing.
The intent exists in the intent DB but produces fewer entries than the
original operation. Or: the intent is in `skipInReconstruct` and never
replays at all.

**Where to look**: `writeIntent` call — compare stored params against all
arguments used by the config function. `reconstruct.go:skipInReconstruct` —
verify the parent operation actually recreates child intents.
`reconstruct.go:ReplayStep` — verify all params are passed through.

**Fix pattern**: Add missing params to `writeIntent`. Remove from
`skipInReconstruct` if the parent doesn't recreate it. This is a mechanical
check per CLAUDE.md "Intent Round-Trip Completeness."

**Should this affect other ops?** Yes — every `writeIntent` call site is a
potential instance. The mechanical check (CLAUDE.md rule) should catch
these at implementation time. After Fixes 13-14, only `OpInterfaceInit`
remains in `skipInReconstruct`.

**Variant 3b: Intent-based query incompleteness** — CONFIG_DB entries reconstruct
correctly (sub-operation re-derives them from profile/spec), but intent-based
queries assume explicit child intents exist. The intent DB is complete for
reconstruction but incomplete for queries.

| Fix | Example |
|-----|---------|
| 13 | ACL rules — `OpAddACLRule` wrongly in skipInReconstruct, params incomplete |
| 14 | PortChannel members — `OpAddPortChannelMember` wrongly in skipInReconstruct |
| 19 | CheckBGPSessions expected `evpn-peer\|{ip}` intents for SetupVTEP-created overlay peers — none exist; peers derived from device intent + resolved profile |

---

## 4. Operational Symmetry Violation

**Pattern**: A forward config function creates CONFIG_DB entries that its
reverse doesn't clean up. Stale entries accumulate and trigger the drift
guard on subsequent operations.

**Root cause**: Forward function creates entry X; reverse function deletes
entries A, B, C but misses X. Or: entry X was added to the forward path
after the reverse was written, and the reverse was never updated.

**Signature**: Drift guard fires with "extra on device" for an entry that
was created by a prior newtron operation and not cleaned up by its reverse.

**Where to look**: Compare the `create*`/`bind*` config function against
its `delete*`/`unbind*` counterpart. Every entry in the forward path must
have a corresponding deletion in the reverse path.

**Fix pattern**: Add the missing deletion to the reverse function. Place
it in correct dependency order (children before parents).

**Should this affect other ops?** Yes — every forward/reverse pair is a
potential instance. Mechanical audit: for each `*Config()` function, list
all tables it writes, then verify the reverse function deletes all of them.

| Fix | Example |
|-----|---------|
| 17 | `bindIpvpnConfig` creates `BGP_GLOBALS|{vrf}` but `unbindIpvpnConfig` didn't delete it |

---

## 5. Transport Guard Missing

**Pattern**: An I/O operation (Redis read/write, SSH command) fails with
"not connected" in offline/topology mode, instead of being a no-op.

**Root cause**: The operation doesn't check `n.conn == nil` before
attempting device I/O. In the intent-first architecture, I/O operations
must be no-ops when no transport exists — the projection is built from
intents, not from device reads.

**Signature**: Error "not connected" or "ErrNotConnected" during topology
operations that should work without a device.

**Where to look**: Any method that calls `n.conn.Client()`,
`n.conn.Session()`, or `n.conn.ConfigDB` without a nil guard.

**Fix pattern**: Add `if n.conn == nil { return nil }` at the top of
I/O-boundary methods.

**Should this affect other ops?** No — after Fixes 4 and 5, all I/O
boundary methods (`Verify`, `Apply`, `SaveConfig`, `Lock`, `Unlock`) have
transport guards. This category is "closed" unless new I/O methods are added.

| Fix | Example |
|-----|---------|
| 4 | `ChangeSet.Verify()` didn't guard on nil conn |
| 5 | `SaveConfig()` didn't guard on nil conn |

---

## 6. Reconcile/Delivery Correctness

**Pattern**: `Reconcile` (ReplaceAll) doesn't fully deliver the projection
to the device, or doesn't clean up all stale entries.

**Root cause**: `ReplaceAll` operates on a set of "owned tables" that
doesn't include all tables newtron manages, or it only cleans tables
that have entries in the delivery set (missing empty tables that need
to be zeroed).

**Signature**: After reconcile, device has stale entries from a prior state,
or specific tables were not cleaned.

**Where to look**: `sonic/pipeline.go:ReplaceAll`, `OwnedTables()`,
`ExportEntries()`.

**Fix pattern**: Ensure ReplaceAll scans ALL owned tables for cleanup
(not just those with entries), and that the owned-tables list includes
all tables newtron manages.

**Should this affect other ops?** No — Reconcile is a single code path.
Fixes 9 and 10 addressed the two ways it could miss entries. Category
is "closed" unless new tables are added to the schema.

| Fix | Example |
|-----|---------|
| 9 | ReplaceAll only scanned tables present in delivery entries — empty tables not cleaned |
| 10 | NEWTRON_INTENT excluded from owned tables → stale intents survived config reload |

---

## 7. Drift Guard Edge Case

**Pattern**: Drift guard fires incorrectly — either on a fresh device with
no intents, or fails to fire when it should.

**Root cause**: The drift guard compares projection vs device CONFIG_DB.
Edge cases: empty projection (no intents yet) vs non-empty factory
CONFIG_DB, or tables that should be excluded from comparison.

**Signature**: First write to a fresh device fails with "device has
drifted" even though no intents were ever written.

**Where to look**: `node.go:Lock()` drift guard logic. Check the
"no intents" short-circuit.

**Fix pattern**: Skip drift guard when the intent DB is empty — there's
nothing to drift from.

| Fix | Example |
|-----|---------|
| 12 | Fresh device with factory CONFIG_DB entries triggered drift guard before any intents existed |

---

## 8. Concurrency / Deadlock

**Pattern**: Operations hang (504 timeout) due to lock contention.

**Root cause**: A non-reentrant lock (Go's `sync.RWMutex`) is acquired
by a method that calls another method which also tries to acquire the
same lock. The actor pattern already serializes access, making the mutex
redundant and harmful.

**Signature**: 504 Gateway Timeout on operations that previously worked.
Happens when call chains cross method boundaries that each acquire locks.

**Where to look**: `sync.Mutex` or `sync.RWMutex` on objects owned by
an actor. If the actor already serializes access, the mutex is redundant.

**Fix pattern**: Remove the redundant mutex. The actor IS the
synchronization mechanism.

**Should this affect other ops?** No — Fix 16 removed the mutex entirely.
Category is "closed" (no mutexes remain on Node).

| Fix | Example |
|-----|---------|
| 16 | `GetInterface` → write lock → `InterfaceExists` → read lock → deadlock |

---

## 9. Test Scenario Dependency

**Pattern**: Test scenario fails because it depends on state created by
another scenario that isn't in its `requires` chain, or steps within a
scenario are in the wrong order.

**Root cause**: Missing `requires` entry, or step ordering assumes state
that doesn't exist when running with `--target` (which only runs the
dependency chain, not the full suite).

**Signature**: "does not exist" or "already exists" errors when running
with `--target` but not when running the full suite.

**Where to look**: Scenario YAML `requires:` field. Verify every resource
the scenario tears down or reads was created by a scenario in its
transitive requires chain.

**Fix pattern**: Add missing requires, or reorder steps.

| Fix | Example |
|-----|---------|
| 6 | Drift-guard scenario steps in wrong order for unsaved-intent guard |
| 8 | Intent-reload scenario steps in wrong order |
| 15 | setup-device needed reconcile steps for clean baseline |
| — | teardown-overlay missing `cross-switch` in requires (Fix 18 session) |

---

## 10. Topology Node Lifecycle

**Pattern**: Topology node construction fails on a valid edge case
(e.g., zero steps after clear+save).

**Root cause**: Constructor assumes certain invariants (at least one step)
that don't hold for all valid states.

**Where to look**: `BuildAbstractNode`, `BuildTopologyNode`,
`BuildEmptyTopologyNode`.

| Fix | Example |
|-----|---------|
| 11 | `BuildAbstractNode` required ≥1 step; clear+save produces zero steps |

---

## 11. Hydrator Replace Semantics

**Pattern**: Two operations write to the same CONFIG_DB `table|key`. The
`configTableHydrators` registry replaces the entire typed struct on each write.
If the second write has fewer fields than the first, fields from the first write
are silently dropped.

**Sub-pattern**: A struct missing fields causes those fields to be silently
dropped during hydration, even when the schema accepts them. During
`RebuildProjection`, the hydrator reads ChangeSet field values into the struct
— fields without struct counterparts are discarded. `ExportEntries` exports only
struct fields, so the reconstructed projection is missing those values.

**Root cause**: `configTableHydrators` uses full-struct replacement (not field
merge). Any partial write or missing struct field causes silent field loss. This
is invisible during the original operation (the ChangeSet is correct) but
surfaces during `RebuildProjection` (the hydrator loses fields) or when a second
operation re-writes the same key (overwriting the first write's fields).

**Signature**:

- BGP sessions fail with "peerState: Policy" — `ebgp_requires_policy: false`
  was stripped by a later write to the same key.
- Drift guard fires with "missing on device" for fields that were written during
  the original operation but are absent from the reconstructed projection.

**Where to look**:

- Any two config functions that write to the same `table|key`. Search for calls
  to the same key-constructor in different `*_ops.go` files.
- Any `BGPGlobalsAFEntry`, `BGPGlobalsEntry`, or other typed struct — if a field
  exists in `schema.go` but not in the struct, it will be dropped during
  `RebuildProjection`.
- `configdb_parsers.go` hydrator entries — verify every field written by any
  config function has a corresponding struct field and hydrator mapping.

**Fix pattern**:

- (a) **Remove redundant writes** — ensure each `table|key` is written once,
  by its owning function, with all required fields. Sub-operations must not
  re-write keys already created by the owning function (Single-Owner Principle).
- (b) **Add missing struct fields** — every field that any config function writes
  to a table must have a corresponding field in the typed struct AND the hydrator.
- (c) **Long-term**: consider merge hydrators for tables that are legitimately
  written by multiple operations, to avoid accidental field loss.

**Should this affect other ops?** Yes — any pair of operations that write to the
same key is a potential instance of pattern (a). Any config function that writes
fields not in the struct is a potential instance of pattern (b). Audit:
cross-reference all `cs.Add`/`cs.Update` call sites with `configdb_parsers.go`
struct fields.

| Fix | Example |
|-----|---------|
| 20 | `SetupVTEP` re-wrote `BGP_GLOBALS\|default` with fewer fields, stripping `ebgp_requires_policy: false` written by `ConfigureBGP` |
| 21 | `BGPGlobalsAFEntry` missing `RedistributeConnected`/`RedistributeStatic` → silently dropped during `RebuildProjection` |

---

## Cross-Cutting Analysis

**Did any category's failure pattern also affect other ops that weren't tested?**

| Category | Affects other ops? | Evidence |
|----------|-------------------|----------|
| 1. Reconstruction invariant | Searched — Fix 18 was the only `IsEmpty()` guard | No other ops use this pattern |
| 3. Intent round-trip | Every `writeIntent` is a potential instance | Mechanical check per CLAUDE.md catches these |
| 4. Operational symmetry | Every forward/reverse pair | Audit: all current pairs verified |
| 7. Drift guard | Single code path in Lock() | Edge case specific to "no intents" |
| 8. Deadlock | Single mutex removed entirely | Category closed |
| 11. Hydrator replace semantics | Any two ops writing the same key; any struct missing fields | Audit: cross-reference `cs.Add`/`cs.Update` sites with struct fields |

Categories 2, 5, 6, 9, 10 are structural/lifecycle — they affect specific
code paths, not a pattern repeated across operations.
