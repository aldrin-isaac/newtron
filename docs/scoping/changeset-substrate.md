# Cluster B — ChangeSet substrate

**Status:** scoped, pending implementation. Part of the
[newtcon-driven gaps batch](newtcon-driven-gaps.md); see the parent
doc for the unifying §46 principle, style invariants, and
cross-cluster phasing.

**Deep-dive outcome (2026-05-26):** The cluster's three issues were
re-evaluated against the three tests (§46 substrate / newtcon-
derivability / cost-value). Net effect:

- **`newtron#11`** — keep as scoped. The §46 paradigm case; trivial
  newtron-side addition; newtcon cannot derive without parsing
  free-text or reimplementing render.
- **`newtron#12`** — **DEFERRED INDEFINITELY.** Not load-bearing for
  any newtcon surface (enriches Report Bug but doesn't enable it);
  §33 reconciliation discipline is non-trivial ongoing tax for
  marginal enrichment. Status: OPEN as tracked consideration.
  Re-evaluate if operators struggle to identify methods from
  substrate alone after the Report Bug surface goes live. See
  the [deferral comment on newtron#12](https://github.com/aldrin-isaac/newtron/issues/12#issuecomment-4551056191).
- **`newtron#19`** — **NARROWED.** Option A (`per_write[]`) trimmed to
  a single field — `VerificationError.DeviceResponse string` — on
  existing `VerificationResult.Errors[]`. The full `per_write[]`
  array is ~60% redundant with #11 + existing Verification given
  per-Node atomicity. Option B (SSE streaming) **deferred
  indefinitely** — not substrate per §46, UX-only benefit, polling
  on Option A's data is functional. See the
  [narrowing comment on newtron#19](https://github.com/aldrin-isaac/newtron/issues/19#issuecomment-4551057150).

The per-issue sections below retain the original full design for the
audit trail; the deferred/narrowed status is recorded at the top of
each affected section.

## Scope

Three issues, all instances of §46 applied to newtron's **ChangeSet
substrate** — the canonical typed delta vocabulary newtron uses
internally to drive Redis writes and verify them
(`DESIGN_PRINCIPLES_NEWTRON.md` §11). Organized into two
sub-clusters by the moment of substrate surfacing:

**Sub-cluster B1 — Response-time exposure** (ChangeSet entries
surfaced in the terminal `WriteResult` JSON, after the write
completes):

- [newtron#11](https://github.com/aldrin-isaac/newtron/issues/11) — Structured `ChangeSet` in `WriteResult`
- [newtron#12](https://github.com/aldrin-isaac/newtron/issues/12) — Call-site provenance on ChangeSet entries (verbose mode)

**Sub-cluster B2 — Apply-time surfacing** (ChangeSet entries
surfaced *during* the write, as each substrate operation lands;
per-substrate-operation granularity + optional Server-Sent Events
streaming):

- [newtron#19](https://github.com/aldrin-isaac/newtron/issues/19) — Per-substrate-operation surfacing on write endpoints (`per_write[]` + SSE streaming variant)

## Shared load-bearing primitives

- **`sonic.ConfigChange`** (in `pkg/newtron/device/sonic/types.go`)
  — the canonical typed entry: Table, Key, Type (add/modify/delete),
  Fields. Already JSON-tagged. #11 exposes this type directly in
  HTTP responses; #12 adds a `Source` field to it (captured at
  emission time, serialized via verbose mode only).
- **`ChangeSet.Changes []Change`** (in
  `pkg/newtron/network/node/changeset.go`, aliased to
  `[]sonic.ConfigChange`) — the universal contract per §11. #11
  exposes it via `WriteResult.Changes`; #12 enriches each entry
  with generation provenance.
- **All `WriteResult`-construction call sites** — every handler that
  runs a ChangeSet builds a `WriteResult`. Both #11 and #12 touch
  this same set of sites. Implementing them sequentially in one
  PR pair minimizes call-site churn.

## Implementation order (within this cluster, post deep-dive)

1. **#11** — trivial, additive. Adds `Changes []sonic.ConfigChange`
   field to `WriteResult`. Populated from `cs.Changes` at every
   construction site. Sub-cluster B1. **(Phase 1.)**
2. **#19 (narrowed)** — small. Add `VerificationError.DeviceResponse
   string` field to `VerificationResult.Errors[]`. Could fold into
   #11's PR. **(Phase 1, with #11.)**
3. **#12** — **deferred indefinitely.** Not implemented now.
4. **#19 SSE variant** — **deferred indefinitely.** Not implemented now.

The original three-step phasing (#11 → #12 → #19) collapses to a
single small PR pair: #11 plus the #19-narrowed `DeviceResponse`
field. #12 and the #19 SSE variant remain tracked but unscheduled.

---

## 1. `newtron#11` — Structured `ChangeSet` in `WriteResult`

### Principle check

**§46 (load-bearing):** `WriteResult.Preview` is a derivative string
rendering of `ChangeSet.Changes`. §46 requires the canonical form
(typed `Changes` array) alongside; this change adds it. The
summary `Preview` is retained for CLI rendering — exactly the
"additive evolution" model §46 prescribes.

**§11 supports:** ChangeSet is the Universal Contract; serializing
`Changes` directly gives consumers the same single-representation
substrate newtron uses internally for write and verify. No parallel
format invented; no internal type leaked.

### Implementation

**File:** `pkg/newtron/types.go` (existing home of `WriteResult`).

**Change:** add `Changes` field to existing `WriteResult`:

```go
// WriteResult wraps the outcome of a configuration write operation.
type WriteResult struct {
    Preview      string               `json:"preview,omitempty"`
    Changes      []sonic.ConfigChange `json:"changes,omitempty"` // NEW
    ChangeCount  int                  `json:"change_count"`
    Applied      bool                 `json:"applied"`
    Verified     bool                 `json:"verified"`
    Saved        bool                 `json:"saved"`
    Verification *VerificationResult  `json:"verification,omitempty"`
}
```

`sonic.ConfigChange` is in `pkg/newtron/device/sonic/types.go` and
is already JSON-tagged (`json:"table"`, `json:"key"`, `json:"type"`,
`json:"fields,omitempty"`). No new types needed.

**Callers:** every site that constructs a `WriteResult` after
running a `ChangeSet`. Search `WriteResult{` across
`pkg/newtron/api/` (and any non-API constructor sites). For each:

```go
result := WriteResult{
    Preview:     cs.Preview(),       // existing
    Changes:     cs.Changes,         // NEW — direct assignment; cs.Changes is []Change aliased to []sonic.ConfigChange
    ChangeCount: len(cs.Changes),    // existing
    // ... rest unchanged
}
```

**Tests:** extend `pkg/newtron/api/api_test.go` to assert `Changes`
is populated and matches the per-table-per-key-per-field shape on at
least one apply path (e.g., `POST /networks/{n}/nodes/{d}/vlans`
create-vlan, which has well-known ChangeSet output).

**Estimated effort:** single PR. One field, one assignment per
construction site, one test.

---

## 2. `newtron#12` — Call-site provenance on ChangeSet entries (verbose mode)

> **Status (2026-05-26): DEFERRED INDEFINITELY.** The §46 substrate
> test passes (`Source` is canonical metadata captured at emission),
> but the operator-value test does not — no newtcon surface
> absolutely requires `Source`. The Report Bug surface (newtcon#42)
> is enriched by it but functions without it; operators can identify
> methods by reading diffs. The §33 reconciliation discipline
> (verbose-mode opt-in must be guarded against future drift) is
> non-trivial ongoing tax for marginal enrichment. Issue remains
> OPEN as tracked consideration; re-evaluate if the Report Bug
> surface, once live, shows operators consistently struggling to
> identify methods from substrate alone. See the [deferral comment
> on newtron#12](https://github.com/aldrin-isaac/newtron/issues/12#issuecomment-4551056191).
> The full design below is preserved for the audit trail.

### Principle check

**§46 (load-bearing, with caveat):** `Source` is the canonical
provenance substrate newtron captures at emission time but does not
expose. §46 says to expose canonical substrate; this endpoint does
so. **The caveat** is §33: exposing internal-package method names
in HTTP responses puts internal Go symbols in the public surface.
§46 and §33 must be reconciled here — they do so via opt-in
verbose mode.

**§33 reconciliation:** the default response shape does NOT include
`Source` (preserving §33's internal/public boundary). Verbose
responses do (honoring §46's substrate exposure). The `json:"-"`
tag on `ConfigChange.Source` is the load-bearing line for the
reconciliation; the separate `VerboseConfigChange` type is the
explicit opt-in surface.

This reconciliation is consistent with both principles **if and
only if** the verbose-mode opt-in is preserved. A future change
that promotes `Source` into default responses would re-violate §33
and must be rejected at that time.

**§1, §20, §27, §13, §14, §15 unaffected:** `Source` is
generation-time metadata about the emitting Go method, not a new
representation alongside intent and reality, not a CONFIG_DB write,
not part of YANG schema, not stored on the device.

This issue requires explicit operator acceptance of the verbose-mode
resolution before implementation.

### Implementation

**New file** `pkg/newtron/network/node/source.go`:

```go
package node

import (
    "fmt"
    "runtime"
    "strings"
)

// callerSite returns "pkg/newtron/.../file.go:line FuncName" for the
// call site at the given depth. Used at ChangeSet.add() time to attach
// generation provenance to each ConfigChange. The captured Source is
// exposed via verbose-mode HTTP responses only — see api.md §verbose.
func callerSite(skip int) string {
    pc, file, line, ok := runtime.Caller(skip)
    if !ok {
        return ""
    }
    file = trimModulePath(file)
    name := ""
    if fn := runtime.FuncForPC(pc); fn != nil {
        name = trimModulePath(fn.Name())
    }
    return fmt.Sprintf("%s:%d %s", file, line, name)
}

const modulePath = "github.com/aldrin-isaac/newtron/"

func trimModulePath(s string) string {
    return strings.TrimPrefix(s, modulePath)
}
```

**Field on `sonic.ConfigChange`** in
`pkg/newtron/device/sonic/types.go`:

```go
type ConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   ChangeType        `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
    Source string            `json:"-"`  // captured at emission; serialized via verbose-mode view only
}
```

The `json:"-"` tag is the boundary keeper — default JSON output
omits `Source` regardless of value. This is the load-bearing line
for principle consistency; preserve it.

**Capture site** in `pkg/newtron/network/node/changeset.go`:

```go
// add appends a change of any type (internal use by buildChangeSet, op).
// Captures the call site at depth 3 (callerSite + add + Add/Update/Delete),
// so Source lands at the *_ops.go method that emitted the change.
func (cs *ChangeSet) add(table, key string, changeType sonic.ChangeType, fields map[string]string) {
    cs.Changes = append(cs.Changes, Change{
        Table:  table,
        Key:    key,
        Type:   changeType,
        Fields: fields,
        Source: callerSite(3),
    })
}
```

**Verbose view type** in `pkg/newtron/types.go`:

```go
// VerboseConfigChange is a ConfigChange with Source serialized. Returned
// only when the caller has explicitly requested verbose mode
// (?verbose=true). The default response shape (sonic.ConfigChange with
// Source tagged json:"-") preserves the public/internal API boundary;
// the verbose shape opts into exposing newtron-internal call sites for
// operator debugging and PR-quality bug reports.
type VerboseConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   sonic.ChangeType  `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
    Source string            `json:"source,omitempty"`
}

func toVerboseChanges(in []sonic.ConfigChange) []VerboseConfigChange {
    out := make([]VerboseConfigChange, len(in))
    for i, c := range in {
        out[i] = VerboseConfigChange{
            Table: c.Table, Key: c.Key, Type: c.Type, Fields: c.Fields, Source: c.Source,
        }
    }
    return out
}
```

**Handler integration:** every handler that emits
`WriteResult.Changes` checks `r.URL.Query().Get("verbose") == "true"`
and either returns the default shape or substitutes
`[]VerboseConfigChange` for the `Changes` field. Cleaner
alternative: a wrapper `VerboseWriteResult` returned only on
verbose request. Pick during implementation.

**Tests:** verify default response (`?verbose=false` or absent)
does NOT contain `"source"` in the JSON body (grep-based assertion);
verify `?verbose=true` does; verify the captured `Source` points at
the emitting `_ops.go` file (e.g.,
`pkg/newtron/network/node/vlan_ops.go` for a VLAN-create operation).

**Estimated effort:** single PR. Capture mechanism is ~30 lines;
default-vs-verbose response shaping touches every handler that
returns `WriteResult.Changes` (the same set updated by #11).

---

## 3. `newtron#19` — Per-substrate-operation surfacing on write endpoints (`per_write[]` + SSE streaming variant)

_Narrowed scope (`VerificationError.DeviceResponse` field) landed on Phase 1
batch._

_Option A (`WriteResult.PerWrite []sonic.PerSubstrateOp`) landed on branch
`impl/phase-2a-per-write-substrate` (Phase 2a). Closes the substrate side
of #19 — operationalizes operator-philosophy invariant #1 (no black boxes)
and bullets 1+2 of the Concrete success vision through the JSON variant.
The earlier "Option A deferred" verdict was revised after re-reading the
philosophy text + the newtcon contract, which together establish that
`per_write[]` is the substrate and SSE is one delivery mode for it._

_Option B (SSE wire variant) remains sequentially deferred — see the
[2026-05-27 reopen comment on newtron#19](https://github.com/aldrin-isaac/newtron/issues/19)
for the re-evaluation trigger._

_Companion landed: write-handler error envelope fix (newtron#21) — typed
`*WriteResult` survives 409 responses to `VerificationFailedError`._

> **Status (2026-05-26 historical; superseded by Phase 2a above): NARROWED + SSE DEFERRED.**
>
> - **Option A (`per_write[]`)** — trimmed to a single field. After
>   newtron#11 lands, `Changes` + `Applied` + `Verification.Errors[]`
>   provide ≈60% of what `per_write[]` would carry. Per-Node
>   atomicity means `per_write[*].result` is uniform for CONFIG_DB
>   writes within one bundle (all `applied` or all `rejected`),
>   fully captured by `Applied`. The only piece of new substrate not
>   derivable from existing fields is the **verbatim device response
>   per failed write** — added as a new
>   **`VerificationError.DeviceResponse string`** field on existing
>   `VerificationResult.Errors[]` entries. Do not add a full
>   `per_write[]` array.
> - **Option B (SSE streaming)** — deferred indefinitely. Streaming
>   is operational timing, not substrate per §46. UX-only benefit;
>   polling against the existing/scoped endpoint is functional.
>   newtcon's streaming surface (contracted in newtcon PR #50)
>   gracefully degrades to polling.
>
> See the [narrowing comment on newtron#19](https://github.com/aldrin-isaac/newtron/issues/19#issuecomment-4551057150).
> The full Option A and Option B design below is preserved for the
> audit trail; the narrowed implementation reduces to a single field
> addition.

### Principle check

**§46 (load-bearing):** the per-substrate-operation data exists in
newtron's call stack at three layers (`ChangeSet.Changes`,
`PipelineSet`'s `[]redis.Cmder` from `Exec`, `verifyConfigChanges`'s
per-entry results) but is discarded by the time it reaches the wire.
The wire today returns aggregate `WriteResult.ChangeCount` —
exactly the "summary instead of canonical" pattern §46 explicitly
rejects. Surfacing each entry's outcome is the principle in action.

**§11 supports:** ChangeSet is the Universal Contract. The
`Changes []sonic.ConfigChange` array from `#11` is the
per-substrate-operation primitive; `per_write[]` extends each entry
with the *outcome* of that substrate operation
(`applied`/`rejected`/`skipped`, the verbatim device response, the
timing).

**§13 supports:** "Prevent Bad Writes, Don't Just Detect Them." A
per-write rejection's verbatim device response is exactly the
substrate-grade information §13 demands — what the device or daemon
said, not a paraphrase.

**§Pipeline §7** (Device I/O: Transient Observation): Apply,
Verify, Drift, Observe are individual Device I/O Operations. The
streaming variant (Option B) surfaces them as individual events at
the granularity the architecture itself models.

### Per-Node atomicity honesty (binding)

Per `DESIGN_PRINCIPLES_NEWTRON.md` §11 and §13, every per-Node
bundle commits via Redis `TxPipeline` (`MULTI`/`EXEC`); writes all
land or none do. The `per_write[]` ordering MUST reflect this:

- Within one per-Node bundle, every `redis_write` / `redis_delete`
  entry carries the same `result` (all `"applied"` or all
  `"rejected"`).
- A mix of `"applied"` and `"rejected"` for CONFIG_DB writes within
  one bundle is a **contract violation** — that would teach a
  non-atomic model that newtron does not have.
- `daemon_wait` and `verify_read` entries are post-EXEC Device I/O
  Operations and MAY have mixed results (one verify may fail while
  others pass without contradicting per-Node atomicity).

The Architect and Architecture Reviewer must verify this discipline
in the implementation PR.

### Implementation (Option A — per_write[] in WriteResult)

**Type** in `pkg/newtron/types.go`, near the existing `WriteResult`:

```go
// PerSubstrateOp represents one Device I/O operation within a per-Node
// write bundle. Per §Pipeline §7, Apply, Verify, Drift, Observe are
// individual Device I/O operations; per_write[] surfaces each one with
// its outcome, the verbatim device response, and the timing.
type PerSubstrateOp struct {
    Seq            int               `json:"seq"`
    Kind           string            `json:"kind"`              // "redis_write" | "redis_delete" | "daemon_wait" | "verify_read"
    Table          string            `json:"table,omitempty"`
    Key            string            `json:"key,omitempty"`
    Fields         map[string]string `json:"fields,omitempty"`
    Result         string            `json:"result"`            // "applied" | "rejected" | "skipped"
    DeviceResponse string            `json:"device_response"`   // verbatim from device or daemon log
    At             time.Time         `json:"at"`
}
```

**Field on `WriteResult`** (additive, extends #11's `Changes`):

```go
type WriteResult struct {
    Preview      string               `json:"preview,omitempty"`
    Changes      []sonic.ConfigChange `json:"changes,omitempty"`   // from #11
    PerWrite     []PerSubstrateOp     `json:"per_write,omitempty"` // NEW from #19
    ChangeCount  int                  `json:"change_count"`
    Applied      bool                 `json:"applied"`
    Verified     bool                 `json:"verified"`
    Saved        bool                 `json:"saved"`
    Verification *VerificationResult  `json:"verification,omitempty"`
}
```

**Capture sites** in:

- `pkg/newtron/network/node/changeset.go` `(cs *ChangeSet) Apply(n *Node)`
  (line 218) — the per-entry `client.Set` / `client.Delete` loop.
  Capture each iteration's table/key/fields/op-kind plus the
  wire-level error (or nil) from the Redis client into a
  `[]PerSubstrateOp` slice. On `TxPipeline`-driven apply, the
  per-Cmder results from `Exec`'s `[]redis.Cmder` populate the
  per-entry `Result` and `DeviceResponse` fields. Per-Node
  atomicity: if `Exec` returns a single error for the whole pipeline,
  every `redis_write` / `redis_delete` entry gets the same `"rejected"`
  result and the bundle's error as `DeviceResponse`.
- `pkg/newtron/network/node/changeset.go` `(cs *ChangeSet) Verify(n *Node)`
  (line 258) — the per-entry verify-read loop. Each entry becomes a
  `verify_read` `PerSubstrateOp` with its own `Result` and any
  `DeviceResponse` from a failed read.
- `pkg/newtron/device/sonic/pipeline.go` `PipelineSet` /
  `PipelineWriteByDrift` (lines 11, 47) — return the per-Cmder
  results rather than only the aggregate error. The current
  signature discards `[]redis.Cmder`; the new signature returns it
  alongside.

**Construction sites** for `WriteResult`:

Every site that builds a `WriteResult` after running a `ChangeSet`
populates `PerWrite` from the now-captured per-entry slice. Same
set of sites updated by #11 — the field is additive at the same
sites.

### Implementation (Option B — SSE streaming variant)

**Endpoint negotiation:** existing write routes admit
`Accept: text/event-stream`. When set, the response is an SSE stream
of `event: substrate_op` events per Device I/O Operation, terminated
by an `event: write_complete` event carrying the aggregate
`WriteResult` (with `per_write[]` populated).

**Implementation:** the capture sites from Option A are also the
emission sites for Option B. The pattern:

```go
// At each capture site:
op := PerSubstrateOp{...}
collected = append(collected, op)            // Option A — accumulate
if streamWriter != nil {                     // Option B — also emit live
    writeSSEEvent(streamWriter, "substrate_op", op)
}
```

`streamWriter` is non-nil only when the request asked for
`text/event-stream`; otherwise the SSE path is dormant and Option A
behavior is unchanged.

**Per-Node atomicity in streaming:** the SSE consumer sees
`substrate_op` events for `redis_write` and `redis_delete` only
**after `Exec` returns** — newtron cannot honestly stream them
in-progress because the `TxPipeline`'s atomicity means no individual
write has "landed" until the pipeline commits. Pre-`Exec`, the
events are not emitted; at the post-`Exec` moment, the events are
emitted in `Seq` order with `Result` determined by the pipeline's
outcome. `daemon_wait` and `verify_read` events stream as they
happen (these are not subject to per-Node atomicity).

The architecture's atomicity model is preserved at the wire — the
stream cannot teach a false partial-success model for the atomic
phase.

### Tests

- A successful apply produces a `WriteResult` with `per_write[]`
  populated, every `redis_write` / `redis_delete` carrying
  `result: "applied"`.
- A bundle whose pipeline `Exec` fails produces `per_write[]` where
  every CONFIG_DB write entry carries the same `result: "rejected"`
  and the same `DeviceResponse` (the pipeline-level error). No
  mixed-result CONFIG_DB writes within one bundle.
- A bundle that applies successfully but whose post-`Exec` verify
  fails on some entries produces mixed `verify_read` results,
  consistent with the atomicity model.
- The SSE variant (if implemented) emits each `substrate_op` event
  with the same payload shape as the corresponding Option A
  `per_write[]` entry, terminated by `write_complete`.

**Estimated effort:** moderate-to-substantive.

- Option A alone: moderate. Touches `ChangeSet.Apply`, `Verify`, the
  pipeline wrappers, and every `WriteResult` construction site.
  Reuses existing primitives; no new pipeline logic.
- Option B (SSE streaming): substantive. Adds SSE infrastructure
  (content negotiation, streaming response handling, client-side
  reconnect semantics) that newtron's HTTP layer doesn't currently
  have. Worth landing in a second PR after Option A.

### Composes with #12 (verbose-mode call-site provenance)

When #12 lands, each `PerSubstrateOp` gains an optional `source`
field (verbose-mode opt-in only, per the §33 boundary). Until #12
lands, the `source` field is absent. The two gaps compose additively;
neither blocks the other.

### Out of scope

- A `WriteResult` field redesign beyond the additive `per_write[]`.
- Per-write metrics / instrumentation export (separate concern from
  operator-facing visibility).
- Backward-incompatible changes to existing `WriteResult` consumers.
