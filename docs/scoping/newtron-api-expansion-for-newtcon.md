# newtron HTTP API expansion for newtcon

**Status:** scoped, pending implementation. Operator-driven; the newtcon
autonomous team does not touch newtron, so all five issues are implemented
manually using normal Claude Code assistance.

**Scope date:** 2026-05-26. This document is a point-in-time scope of a
batch of five newtron-side HTTP API gaps filed by newtcon under the
Gap-Handling Protocol. Update as the work lands; archive once all five
issues close.

## Purpose

newtcon (`github.com/aldrin-isaac/newtcon`) consumes newtron only over
HTTP — no Go imports, no subprocess invocations, no direct Redis access.
When newtcon needs functionality newtron's HTTP API does not yet expose,
the gap is filed against newtron (newtron-repo issue, not newtcon-repo),
and newtcon's contract marks the affected `manual_equivalent.newtron_http`
field as `pending_newtron_gap` with a forward link to the newtron issue.

This document scopes the five newtron-side gaps currently open, with
implementations specified to a level where the file locations, type
names, method names, route paths, and response shapes are concrete
enough to execute. The implementations must remain consistent with
`DESIGN_PRINCIPLES_NEWTRON.md` and read as though the same engineer who
wrote newtron's existing code wrote them — same patterns, same
vocabulary, same boundary discipline.

## Common thread — all nine align to §46

The nine gaps are not independent design decisions. Each is an
instance of the same principle: **newtron's HTTP API should expose
its canonical in-memory substrate types directly, not derivatives,
summaries, or opaque handles.** This is codified as
[`DESIGN_PRINCIPLES_NEWTRON.md` §46](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
("HTTP API Boundary — Wire Shape Mirrors Substrate").

Re-reading the nine issues through the §46 lens, grouped by the
substrate cluster they touch:

| Issue | Cluster | Today's response | Canonical substrate the principle says to expose |
|-------|---------|-----------------|--------------------------------------------------|
| `#11` | B. ChangeSet | `WriteResult.Preview` (string) + `change_count` | `ChangeSet.Changes` (`[]sonic.ConfigChange`) |
| `#12` | B. ChangeSet | ChangeSet entries without generation provenance | `Source` field on each entry (verbose-only, per §33 boundary) |
| `#5` | A. Projection | Per-resource summary views, or device CONFIG_DB raw reads | `Projection` (output of `ExportEntries`) |
| `#4` | A. Projection | Free-text preview string + entry count for hypothetical mutations | Before-`Projection` + After-`Projection` + `[]DriftEntry` |
| `#6` | A. Projection | Per-device intent records, or spec definitions, requiring N-call stitching | Per-Node `[]DriftEntry` slice for the service |
| `#14` | C. Topology spec | `GET /topology/node` returning names only (`[]string`) | Full `TopologySpecFile` (devices + links + metadata) |
| `#15` | C. Topology spec | YAML hand-edit + `/reload`; no CRUD verbs | `TopologyDevice` directly, via typed `create-node`/`delete-node`/`update-node` |
| `#16` | C. Topology spec | YAML hand-edit + `/reload`; no CRUD verbs | `TopologyLink` directly, via typed `create-link`/`delete-link` |
| `#13` | D. Cross-target reasoning | No exposure of cross-Node dependency graph | New computed substrate: dependency edges derived from service-spec resolution |

The unifying citation in each per-issue principle check below is §46,
with other principles (§1, §7, §11, §16, §21, §33) cited as
supporting context per cluster.

**Cluster summary:**

- **Cluster A — Projection substrate (`#4`, `#5`, `#6`):** all share
  `ConfigDB.ExportEntries` + `sonic.DiffConfigDB` +
  `SnapshotIntentDB`/`RestoreIntentDB` as the load-bearing primitives.
  Tight coupling — `#4` and `#6` are replay-diff techniques built
  on `#5`'s read primitive.
- **Cluster B — ChangeSet substrate (`#11`, `#12`):** share
  `sonic.ConfigChange` and the WriteResult-construction call sites.
  `#12`'s verbose mode attaches to the `Changes` field `#11`
  introduces.
- **Cluster C — Topology spec substrate (`#14`, `#15`, `#16`):**
  share `spec.Loader.SaveTopology` (already exists, atomic
  temp+rename) + `spec.TopologySpecFile`/`TopologyDevice`/
  `TopologyLink` + `validateTopology`. `#15` and `#16` share
  the `SaveTopology` write call site exactly; `#14` is the read
  side.
- **Cluster D — Cross-target reasoning (`#13`):** standalone;
  requires building net-new substrate (the cross-Node dependency
  graph derived from how service-spec resolution links one Node's
  `BGP_NEIGHBOR` to another Node's `loopback_ip`, etc.). The
  heaviest substantive work in the queue.

The 2026-05-26 batch extends the original five-issue scope with
`#13`, `#14`, `#15`, and `#16`. The original five (`#4`, `#5`, `#6`,
`#11`, `#12`) are §46 applied to the **runtime layer** (ChangeSet,
projection, drift). The new four extend §46 to the **spec layer**
(topology.json — `#14`/`#15`/`#16`) and to a **net-new substrate
type** (cross-Node dependency graph — `#13`).

Auditing the existing newtron HTTP API surface against §46 surfaced
only one runtime-layer violation in the original five issues:
`WriteResult.Preview` without `Changes`, which `#11` addresses. The
topology-layer gaps (`#14`, `#15`, `#16`) are spec-layer violations:
the typed substrate exists internally, only HTTP exposure is
missing. `§46` is largely descriptive of how newtron already
operates; codifying it locks the discipline in for future API
additions.

## Style invariants

These apply to every implementation in this batch:

- **Handlers** live in `pkg/newtron/api/handler_node.go` (node-scoped)
  or `pkg/newtron/api/handler_network.go` (network-scoped), grouped
  with semantically related handlers. Handler signature is
  `func (s *Server) handleXxx(w http.ResponseWriter, r *http.Request)`,
  using `requireNodeActor` (or the network equivalent) for actor
  resolution and `writeJSON` / `writeError` for response.
- **Routes** are registered in `pkg/newtron/api/handler.go`
  (`buildMux()`) with HTTP-method-prefixed paths:
  `"GET /network/{netID}/node/{device}/intent/..."`. Intent operations
  live under `/intent/`; CONFIG_DB reads under `/configdb/`; service
  reads under `/service/{service}/`.
- **Public Node methods** are PascalCase verbs returning
  `(value, error)` or just `value`. No `Get*` prefix; the method's
  noun-return communicates the read. Examples already present in
  `pkg/newtron/network/node/node.go`: `Tree`, `Drift`, `Intents`,
  `Reconcile`, `RebuildProjection`, `ConfigDB`.
- **Domain types** live in `pkg/newtron/network/node/node.go` near
  related types (e.g., `ReconcileResult`, `ReconcileOpts`).
- **Diff vocabulary**: `sonic.DriftEntry` is the canonical entry-delta
  type, defined in `pkg/newtron/device/sonic/configdb_diff.go` and used
  by `DiffConfigDB`. Do not invent parallel `ChangeEntry`, `DiffEntry`,
  or per-feature diff types. `§11` (ChangeSet is the Universal Contract)
  binds the project to one diff representation.
- **Actor patterns** in `handler_node.go`:
  - `nodeActor.connectAndRead(ctx, fn)` for reads that need transport.
  - `nodeActor.do(ctx, fn)` for in-memory mutations that hold the lock
    but skip the delivery pipeline.
  - `nodeActor.execute(ctx, fn)` for full delivery
    (validate → write → verify → record).
- **Query parameters** for opt-in behavior follow the existing
  convention: `?dry_run=true`, `?no_save=true`, `?verbose=true`. Not
  body fields; not headers; not URL-embedded mode segments.

## Implementation order

Suggested phased ordering. Each phase is a natural PR boundary;
within a phase, issues either share a PR (when they share a call
site) or can be batched into one PR (when each is trivial). The
phasing minimizes coupling cost and front-loads the highest
unblock-value-per-effort items.

| Phase | Issue | Cluster | Complexity | Unblocks (newtcon-side) |
|-------|-------|---------|------------|--------------------------|
| **1** | `newtron#11` — Structured `ChangeSet` in `WriteResult` | B | trivial | broad newtcon coverage; per-write granularity |
| **1** | `newtron#5` — Per-Node projection read endpoint | A | trivial | Provenance surface; observation-history poller |
| **1** | `newtron#14` — Full topology read endpoint | C | trivial | graphical topology viz; spec-authoring readback |
| **2** | `newtron#15` — Topology node CRUD | C | moderate | spec-authoring (topology) — node lifecycle |
| **2** | `newtron#16` — Topology link CRUD | C | moderate | spec-authoring (topology) — link lifecycle |
| **3** | `newtron#4` — Projection diff (before-vs-after) | A | moderate | Workbench dry-run; pre-commit diff |
| **4** | `newtron#6` — Per-service projection slice | A | moderate | service-first projection views |
| **5** | `newtron#12` — Call-site provenance (verbose mode) | B | moderate (pending operator decision) | "Report this is the bug" affordance |
| **6** | `newtron#13` — Cross-target dependency exposure | D | substantive (net-new substrate) | safe-vs-fragile multi-target classification |

**Phasing rationale:**

- **Phase 1** — three trivial reads, one PR. Each is ~10–20 lines of
  new code, all §46-aligned, all unblock substantive newtcon-side
  work. No coupling between them; batched only for review efficiency.
- **Phase 2** — `#15` + `#16` in one PR. They share the
  `Loader.SaveTopology` call site, the same Network method-layer
  pattern, and the same validation hook. Splitting them across two
  PRs would create churn in the same files. `#14` (Phase 1)
  precedes Phase 2 logically — operators read after they mutate —
  but the implementations don't depend on each other.
- **Phase 3** — `#4` (projection diff) builds on the snapshot/restore
  primitives `#5` (Phase 1) makes visible. Single PR.
- **Phase 4** — `#6` (per-service slice) reuses `#4`'s replay-diff
  technique with the inverse staging ("remove the service's intents"
  instead of "stage additions"). Single PR.
- **Phase 5** — `#12` (verbose-mode call-site provenance) requires
  the operator decision on §33 reconciliation in the issue body
  before implementation; defers naturally to after most other work.
- **Phase 6** — `#13` is the heaviest item: net-new substrate (the
  cross-Node dependency graph), independent of the other gaps,
  warrants its own PR with substantial design.

---

## 1. `newtron#11` — Structured `ChangeSet` in `WriteResult`

### Principle check

**§46 (load-bearing):** `WriteResult.Preview` is a derivative string
rendering of `ChangeSet.Changes`. §46 requires the canonical form
(typed `Changes` array) alongside; this change adds it. The summary
`Preview` is retained for CLI rendering — exactly the "additive
evolution" model §46 prescribes.

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

`sonic.ConfigChange` is in `pkg/newtron/device/sonic/types.go` and is
already JSON-tagged (`json:"table"`, `json:"key"`, `json:"type"`,
`json:"fields,omitempty"`). No new types needed.

**Callers:** every site that constructs a `WriteResult` after running a
`ChangeSet`. Search `WriteResult{` across `pkg/newtron/api/` (and any
non-API constructor sites). For each:

```go
result := WriteResult{
    Preview:     cs.Preview(),       // existing
    Changes:     cs.Changes,         // NEW — direct assignment; cs.Changes is []Change aliased to []sonic.ConfigChange
    ChangeCount: len(cs.Changes),    // existing
    // ... rest unchanged
}
```

**Tests:** extend `pkg/newtron/api/api_test.go` to assert `Changes` is
populated and matches the per-table-per-key-per-field shape on at least
one apply path (e.g., `POST /network/{n}/node/{d}/vlan` create-vlan,
which has well-known ChangeSet output).

**Estimated effort:** single PR. One field, one assignment per construction
site, one test.

---

## 2. `newtron#5` — Per-Node projection read endpoint

### Principle check

**§46 (load-bearing):** the `Projection` is the canonical substrate
that represents "what newtron believes this device should look like"
(`§1`). Today it is only exposed via per-resource summary views
(`/vlan`, `/vrf`, `/interface`) and via CONFIG_DB raw reads (which
read the device, not the projection). §46 requires the canonical
typed form to be available directly. This endpoint adds it.

**§1, §21 support:** the projection is rebuilt from intent replay
(`§21`); exposing it as a typed read is what `§1` invites operators
to reason about. The method reads `n.configDB.ExportEntries()`, which
is the same primitive already used internally by `reconcileFull`
(`node.go:420`) — no new state, no new representation.

### Implementation

**Node method** in `pkg/newtron/network/node/node.go`, placed in the
read-side method block near `Tree`, `Drift`, `Intents`:

```go
// Projection returns the per-table per-key per-field expected state
// derived from intent replay. This is the substrate that represents
// "what newtron believes this device should look like" — compare
// against device reads (ConfigDB, QueryConfigDB) to see drift.
//
// The projection is the rendered effect of the intent DB; intent
// records are its inputs. See DESIGN_PRINCIPLES_NEWTRON.md §1, §21.
func (n *Node) Projection() sonic.RawConfigDB {
    return n.configDB.ExportEntries()
}
```

**HTTP handler** in `pkg/newtron/api/handler_node.go`, in the Intent
operations section (currently around line 1037, alongside `handleTree`,
`handleDrift`, `handleReconcile`):

```go
func (s *Server) handleProjection(w http.ResponseWriter, r *http.Request) {
    _, nodeActor := s.requireNodeActor(w, r)
    if nodeActor == nil {
        return
    }
    val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
        return n.Projection(), nil
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

**Route** in `pkg/newtron/api/handler.go`, alongside the existing
`/intent/...` routes:

```go
mux.HandleFunc("GET /network/{netID}/node/{device}/intent/projection", s.handleProjection)
```

**Tests:** `pkg/newtron/api/api_test.go` — assert the endpoint returns
the projection shape; round-trip with a known intent set produces the
expected per-table-per-key entries; empty Node returns empty map.

**Estimated effort:** single PR. One method, one handler, one route,
one test.

---

## 3. `newtron#4` — Projection diff (before-vs-after) endpoint

### Principle check

**§46 (load-bearing):** the existing dry-run path returns a free-text
preview + entry count. §46 requires the typed diff substrate. This
endpoint provides `Before` and `After` typed projections plus a
`[]DriftEntry` diff — three canonical types side-by-side. Rule 3 of
§46 ("one typed diff vocabulary") is honored by reusing `DriftEntry`
rather than inventing a parallel diff type.

**§1, §21 support:** the projection is the canonical substrate (`§1`)
derivable from intent replay (`§21`); diffing two projections is the
canonical "what would change" answer.

**§20 preserved:** the snapshot/restore mechanism uses existing
`SnapshotIntentDB` / `RestoreIntentDB`. Intent records are not
extended; the actual intent DB after the call equals the intent DB
before.

### Implementation

**Type** in `pkg/newtron/network/node/node.go`, alongside
`ReconcileResult`:

```go
// ProjectionDiff reports the change to the projection that a set of
// hypothetical operations would produce. Before and After are the typed
// projections; Diff is the entry-level delta in the same shape Drift
// uses, so consumers have one diff vocabulary across drift and preview.
type ProjectionDiff struct {
    Before sonic.RawConfigDB  `json:"before"`
    After  sonic.RawConfigDB  `json:"after"`
    Diff   []sonic.DriftEntry `json:"diff"`
}
```

**Node method** in `node.go`, near `Reconcile`:

```go
// ProjectionDiff applies the given operations on top of the current
// intent DB in-memory only (no device write), captures the resulting
// projection, and restores the intent DB. Returns the before/after
// projections and the entry-level delta in the same shape DiffConfigDB
// produces for drift.
//
// The intent DB is snapshotted before apply and restored after, so the
// Node's observable state is unchanged when this method returns.
func (n *Node) ProjectionDiff(ctx context.Context, ops []spec.TopologyStep) (*ProjectionDiff, error) {
    before := n.configDB.ExportEntries()
    snap := n.SnapshotIntentDB()
    defer n.RestoreIntentDB(snap)

    for _, op := range ops {
        if err := n.applyStep(ctx, op); err != nil {
            return nil, err
        }
    }

    after := n.configDB.ExportEntries()
    diff := sonic.DiffConfigDB(after, before, sonic.OwnedTables())
    return &ProjectionDiff{Before: before, After: after, Diff: diff}, nil
}
```

**Note on `applyStep`:** newtron's internal step-application function
that `/execute` and `handleSave` rely on. Locate the exact name during
implementation — `handleSave` (`handler_node.go:1111`) builds
`[]spec.TopologyStep` from `n.Tree().Steps`, so the apply path consumes
that shape.

**Request type** in `pkg/newtron/api/types.go`:

```go
// ProjectionDiffRequest is the body for POST .../intent/projection-diff.
// Operations are in the same shape as POST .../execute consumes.
type ProjectionDiffRequest struct {
    Operations []spec.TopologyStep `json:"operations"`
}
```

**HTTP handler** in `handler_node.go`, in the Intent operations section:

```go
func (s *Server) handleProjectionDiff(w http.ResponseWriter, r *http.Request) {
    _, nodeActor := s.requireNodeActor(w, r)
    if nodeActor == nil {
        return
    }
    var req ProjectionDiffRequest
    if err := decodeJSON(r, &req); err != nil {
        writeError(w, err)
        return
    }
    val, err := nodeActor.do(r.Context(), func() (any, error) {
        return nodeActor.node.ProjectionDiff(r.Context(), req.Operations)
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

Uses `do` (not `execute`) — acquires the lock for serialization but
skips the delivery pipeline, matching the in-memory-only nature of the
diff. Matches `handleReload` (`handler_node.go:1146`).

**Route** in `handler.go`:

```go
mux.HandleFunc("POST /network/{netID}/node/{device}/intent/projection-diff", s.handleProjectionDiff)
```

**Tests:** empty ops list returns `Before == After` with empty `Diff`;
a single `CreateVLAN` produces one `Added` `DriftEntry` on the `VLAN`
table; the intent DB after the call equals the intent DB before
(snapshot/restore correctness).

**Estimated effort:** single PR. Reuses `SnapshotIntentDB`,
`RestoreIntentDB`, `ExportEntries`, `DiffConfigDB`, and the internal
step-application path.

---

## 4. `newtron#12` — Call-site provenance on ChangeSet entries (verbose mode)

### Principle check

**§46 (load-bearing, with caveat):** `Source` is the canonical
provenance substrate newtron captures at emission time but does not
expose. §46 says to expose canonical substrate; this endpoint does so.
**The caveat** is §33: exposing internal-package method names in HTTP
responses puts internal Go symbols in the public surface. §46 and §33
must be reconciled here — they do so via opt-in verbose mode.

**§33 reconciliation:** the default response shape does NOT include
`Source` (preserving §33's internal/public boundary). Verbose responses
do (honoring §46's substrate exposure). The `json:"-"` tag on
`ConfigChange.Source` is the load-bearing line for the reconciliation;
the separate `VerboseConfigChange` type is the explicit opt-in surface.

This reconciliation is consistent with both principles **if and only
if** the verbose-mode opt-in is preserved. A future change that
promotes `Source` into default responses would re-violate §33 and
must be rejected at that time.

**§1, §20, §27, §13, §14, §15 unaffected:** `Source` is generation-
time metadata about the emitting Go method, not a new representation
alongside intent and reality, not a CONFIG_DB write, not part of YANG
schema, not stored on the device.

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

**Field on `sonic.ConfigChange`** in `pkg/newtron/device/sonic/types.go`:

```go
type ConfigChange struct {
    Table  string            `json:"table"`
    Key    string            `json:"key"`
    Type   ChangeType        `json:"type"`
    Fields map[string]string `json:"fields,omitempty"`
    Source string            `json:"-"`  // captured at emission; serialized via verbose-mode view only
}
```

The `json:"-"` tag is the boundary keeper — default JSON output omits
`Source` regardless of value. This is the load-bearing line for
principle consistency; preserve it.

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

**Handler integration:** every handler that emits `WriteResult.Changes`
checks `r.URL.Query().Get("verbose") == "true"` and either returns the
default shape or substitutes `[]VerboseConfigChange` for the `Changes`
field. Cleaner alternative: a wrapper `VerboseWriteResult` returned only
on verbose request. Pick during implementation.

**Tests:** verify default response (`?verbose=false` or absent) does NOT
contain `"source"` in the JSON body (grep-based assertion); verify
`?verbose=true` does; verify the captured `Source` points at the
emitting `_ops.go` file (e.g., `pkg/newtron/network/node/vlan_ops.go`
for a VLAN-create operation).

**Estimated effort:** single PR. Capture mechanism is ~30 lines; default-
vs-verbose response shaping touches every handler that returns
`WriteResult.Changes` (the same set updated by `newtron#11`).

---

## 5. `newtron#6` — Per-service projection slice endpoint

### Principle check

**§46 (load-bearing):** the per-service projection slice is the
canonical "service contribution to the network" substrate. Today,
deriving it requires stitching across N intent-tree calls + N
projection re-derivations — exactly the cross-endpoint stitching §46
rejects. This endpoint surfaces the canonical slice directly.

**§6 supports:** Interface is the point of service; service-first
exposure aligns with `CLAUDE.md` §Design Principles. **§11 supports:**
slice shape reuses `DriftEntry` per §46 rule 3 (one typed diff
vocabulary).

The slice is computed via the replay-diff technique using existing
`SnapshotIntentDB` / `RestoreIntentDB` / `RebuildProjection` /
`DiffConfigDB`. No new primitives, no parallel diff vocabulary.

### Implementation strategy

The slice is "the projection rows that exist because the named service
is bound on this Node." Computed by:

1. Snapshot intent DB.
2. Remove the service's intents (those under the `service|{name}`
   prefix in the intent DB).
3. Rebuild projection from the trimmed intent DB.
4. Diff the original projection against the trimmed projection.
5. The `Added` entries in the diff (relative to the trimmed baseline)
   are the service's contribution.
6. Restore.

This is the same snapshot/replay/restore pattern as `ProjectionDiff`
(`newtron#4`), applied differently: instead of staging additions, stage
removals; instead of returning the staged-additions diff, return the
inverse (what's lost when the service is removed = what the service
contributes).

### Implementation

**Type** in `pkg/newtron/network/network.go` (network-level concept,
since a service is network-scoped):

```go
// ServiceProjection reports the projection rows that exist on each Node
// because the named service is bound there. Per-Node slices are derived
// by removing the service's intents from the Node's intent DB,
// rebuilding the projection from the trimmed intent set, and diffing
// against the original projection. The "Added" entries in that diff
// (relative to the trimmed baseline) are the service's contribution.
type ServiceProjection struct {
    Service string                  `json:"service"`
    Nodes   []ServiceProjectionNode `json:"nodes"`
}

type ServiceProjectionNode struct {
    Node string             `json:"node"`
    Diff []sonic.DriftEntry `json:"diff"`
}

// ServiceProjectionOpts narrows the slice.
type ServiceProjectionOpts struct {
    Nodes  []string // restrict to these nodes (optional; default = all binders)
    Tables []string // restrict to these tables (optional; default = all owned)
}
```

**Network method** in `network.go`:

```go
// ServiceProjection returns, for each Node that binds the named service,
// the projection rows contributed by that service. Per Node: snapshot
// the intent DB, remove service|{name} intents, rebuild projection,
// diff against the original; the rows present in the original but not
// in the trimmed projection are the service's contribution. Order:
// alphabetical by Node name.
func (net *Network) ServiceProjection(ctx context.Context, service string, opts ServiceProjectionOpts) (*ServiceProjection, error) {
    // implementation iterates net.nodesBindingService(service) per the existing service-binding lookup
}
```

**HTTP handler** in `pkg/newtron/api/handler_network.go`:

```go
func (s *Server) handleServiceProjection(w http.ResponseWriter, r *http.Request) {
    netActor := s.requireNetworkActor(w, r)
    if netActor == nil {
        return
    }
    service := r.PathValue("service")
    opts := ServiceProjectionOpts{
        Nodes:  r.URL.Query()["node"],   // repeatable
        Tables: r.URL.Query()["table"],  // repeatable
    }
    val, err := netActor.read(r.Context(), func(net *newtron.Network) (any, error) {
        return net.ServiceProjection(r.Context(), service, opts)
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

(The exact network-level actor method name — `read` here is illustrative
— should match what `handler_network.go` already uses; verify against
existing network-level handlers during implementation.)

**Route** in `handler.go`, alongside other `/network/{netID}/service/...`
routes:

```go
mux.HandleFunc("GET /network/{netID}/service/{service}/projection", s.handleServiceProjection)
```

**Tests:** for each service kind (`routed`, `bridged`, `evpn-bridged`,
`evpn-irb`), assert the slice contains the expected projection rows
(e.g., for `evpn-bridged`: VLAN, VLAN_INTERFACE, VXLAN_TUNNEL_MAP, VRF,
BGP_NEIGHBOR, ROUTE_REDISTRIBUTE, COMMUNITY_SET, ROUTE_MAP, ACL_TABLE).
Restrict-by-node and restrict-by-table filters round-trip correctly.

**Estimated effort:** single PR. The replay-diff implementation is
non-trivial — must walk the right intents, exclude only the service's,
rebuild correctly. Test thoroughly across service types.

---

## 6. `newtron#14` — Full topology read endpoint

### Principle check

**§46 (load-bearing):** the `TopologySpecFile` is canonical
substrate that the spec loader already builds in memory. Today's
`GET /topology/node` returns device names only (`[]string`) — the
"summary instead of canonical" pattern §46 explicitly rejects.
Exposing the full typed substrate directly is the resolution.

**§7 supports:** topology is a network-scoped definition newtron
owns; a typed-read endpoint is the minimum substrate-visibility for
that definition.

### Implementation

**Node/Network method** — `Network.GetTopology()` already exists
and returns `*spec.TopologySpecFile`. No new method needed.

**HTTP handler** in `pkg/newtron/api/handler_network.go`, alongside
the existing `handleTopologyDeviceNames`:

```go
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
    na := s.requireNetwork(w, r)
    if na == nil {
        return
    }
    val, err := na.do(r.Context(), func() (any, error) {
        topo := na.net.GetTopology()
        if topo == nil {
            return nil, &newtron.NotFoundError{Resource: "topology", Name: ""}
        }
        return topo, nil
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

Returns `*spec.TopologySpecFile` with existing JSON tags (devices,
links, metadata).

**Route** in `pkg/newtron/api/handler.go`, alongside the existing
`/topology/node` route:

```go
mux.HandleFunc("GET /network/{netID}/topology", s.handleTopology)
```

**Tests:** assert round-trip JSON shape matches `TopologySpecFile`;
404 with `Error.kind="not_found"` when `HasTopology()` is false.

**Estimated effort:** trivial. One handler, one route. Reuses
existing `GetTopology()`.

---

## 7. `newtron#15` — Topology node CRUD (`create-node`, `delete-node`, `update-node`)

### Principle check

**§46 (load-bearing):** the typed `TopologyDevice` is canonical
substrate. Today, mutating it requires a YAML hand-edit + `reload`
— the "no typed verb for an existing substrate" pattern §46
rejects via rule 1 ("canonical first").

**§7 supports:** topology nodes are network-scoped definitions;
the existing verb pattern (`create-service`, `delete-profile`)
extends naturally to topology.

**§16 (verb vocabulary):** `create-node`, `delete-node`,
`update-node` fit the existing `verb-noun` form newtron uses
throughout.

### Implementation

**New Network methods** in `pkg/newtron/network/network.go`,
mirroring the existing `SaveProfile`/`SaveZone`/`SaveService`
pattern:

```go
// AddTopologyDevice creates a device entry in the topology spec.
// Returns ConflictError if a device with this name already exists.
// Validates against existing validateTopology rules (profile ref).
// Persists atomically via spec.Loader.SaveTopology.
func (n *Network) AddTopologyDevice(name string, device *spec.TopologyDevice) error

// DeleteTopologyDevice removes a device from the topology spec.
// Returns NotFoundError if no device with this name exists.
// Returns ConflictError if any link still references the device
// (operator must delete the referring links first, or call with
// force=true to cascade — see open-question note below).
func (n *Network) DeleteTopologyDevice(name string, force bool) error

// UpdateTopologyDevice replaces a device entry. Same validation
// as Add; same persistence path.
func (n *Network) UpdateTopologyDevice(name string, device *spec.TopologyDevice) error
```

Each calls `loader.SaveTopology(spec)` after mutation; failure
unwinds the in-memory mutation before returning.

**HTTP handlers** in `pkg/newtron/api/handler_network.go`,
mirroring the `handleCreateService` / `handleDeleteService` /
etc. patterns:

```go
func (s *Server) handleCreateTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, decode body { name, device },
    // call na.net.AddTopologyDevice, return device or error
}
func (s *Server) handleDeleteTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, name, force query param,
    // call na.net.DeleteTopologyDevice, return {"deleted": name}
}
func (s *Server) handleUpdateTopologyNode(w http.ResponseWriter, r *http.Request) {
    // parse netID, name, decode body,
    // call na.net.UpdateTopologyDevice, return device
}
```

**Routes** in `pkg/newtron/api/handler.go`:

```go
mux.HandleFunc("POST /network/{netID}/topology/create-node", s.handleCreateTopologyNode)
mux.HandleFunc("DELETE /network/{netID}/topology/node/{name}", s.handleDeleteTopologyNode)
mux.HandleFunc("PUT /network/{netID}/topology/node/{name}", s.handleUpdateTopologyNode)
```

**Request types** in `pkg/newtron/api/types.go`:

```go
type TopologyNodeCreateRequest struct {
    Name   string                `json:"name"`
    Device *spec.TopologyDevice  `json:"device"`
}
```

`Update` takes a `*spec.TopologyDevice` body directly (the name is
in the URL path).

**Tests:** round-trip create+read; duplicate-name → 409; deletion
of name referenced by a link → 409 (unless `?force=true`);
validation failure (unknown profile reference) → 400 with
substrate-level rejection reason; in-memory state updated post-CRUD
without requiring `reload`.

**Estimated effort:** moderate. Persistence (`SaveTopology`) and
validation (`validateTopology`) already exist. Gap is the
Network-method layer + handlers. Implement together with `#16`
(same PR).

**Open question:** `?force=true` on `delete-node` to cascade-delete
referring links — defer to Architect during implementation; not
filing a separate gap for it.

---

## 8. `newtron#16` — Topology link CRUD (`create-link`, `delete-link`)

### Principle check

Same as `#15`: §46 (canonical `TopologyLink` substrate exposed
directly), §7 (network-scoped definition), §16 (verb vocabulary).

### Implementation

**New Network methods**, mirroring `#15`:

```go
// AddTopologyLink adds a link to the topology spec.
// Returns ConflictError if an equivalent link (unordered {A,Z})
// already exists. Validates endpoints (both devices must exist;
// both interfaces must be declared on their respective devices).
func (n *Network) AddTopologyLink(link *spec.TopologyLink) error

// DeleteTopologyLink removes a link from the topology spec. Match
// is unordered: {a:X, z:Y} matches {a:Y, z:X}. Returns
// NotFoundError if no matching link.
func (n *Network) DeleteTopologyLink(link *spec.TopologyLink) error
```

Both invoke `loader.SaveTopology(spec)` after mutation.

**HTTP handlers** in `handler_network.go`:

```go
func (s *Server) handleCreateTopologyLink(w http.ResponseWriter, r *http.Request) {
    // decode body as *spec.TopologyLink, call AddTopologyLink, return link
}
func (s *Server) handleDeleteTopologyLink(w http.ResponseWriter, r *http.Request) {
    // decode body as *spec.TopologyLink, call DeleteTopologyLink, return {"deleted": link}
}
```

**Routes:**

```go
mux.HandleFunc("POST /network/{netID}/topology/create-link", s.handleCreateTopologyLink)
mux.HandleFunc("DELETE /network/{netID}/topology/link", s.handleDeleteTopologyLink)
```

`DELETE` takes the body convention (avoids URL-escaping
`device:interface` strings). Alternative path-param form is noted
in the issue body as an open question for the Architect.

**Tests:** create + read; duplicate detection on A/Z swap; deletion
matches unordered pair; validation failures with substrate-level
rejection reasons (unknown device, undeclared interface).

**Estimated effort:** moderate. Same effort profile as `#15`; one
PR for both.

---

## 9. `newtron#13` — Cross-target dependency exposure

### Principle check

**§46 (load-bearing, applied to new substrate):** the cross-Node
dependency graph is substrate that newtron **can** compute
internally (it emerges from service-spec resolution — `apply-service`
on switch1 writes a `BGP_NEIGHBOR` whose `peer_as` is resolved from
switch2's `bgp_as`) but does not today expose. §46 says when
substrate is computable internally, expose it directly. The novelty
here is that the substrate is **derived**, not stored — requiring
new internal computation. This is in scope for §46: rule 1
("canonical first") applies whether the substrate is stored or
derived.

**Operator-philosophy invariant #9** (confidence and limits
explicit): without this substrate, every multi-target preview is
either over-cautious ("always fragile") or dishonest ("always
safe"). Neither teaches the operator the real domain shape.

**Operator-philosophy invariant #1** (no black boxes): a
categorical safe/fragile/unknown label is exactly the kind of
summary the philosophy rejects. The dependency edges must be
exposed.

**Newtron §15** (operational symmetry) motivates: reverse symmetry
is the recovery primitive for partial success, but the operator
must know they are *in* a partial-success situation.

### Implementation

This is the substantive item in the queue. New internal substrate
required:

- A **dependency-graph computation** that walks the resolved
  service-spec graph and identifies cross-Node references
  (BGP peer resolution, route-policy cross-references,
  EVPN peer-group consistency requirements).
- An HTTP endpoint that returns the dependency edges relevant to a
  proposed multi-target operation, with substrate-level rationale
  per edge.

Proposed shape (refined during implementation):

```
POST /network/{netID}/dependency-graph
Body: {
  "operation": "apply | refresh | remove",
  "service": "transit",
  "targets": [...]
}
Response 200: {
  "edges": [
    {
      "from": { "node": "switch1", "binding": "transit on Ethernet0" },
      "to":   { "node": "switch2", "binding": "transit on Ethernet0" },
      "kind": "bgp_peer_resolution",
      "rationale": "switch1's BGP_NEIGHBOR.peer_as resolves to switch2's bgp_as; partial commit creates session-half mismatch"
    },
    ...
  ],
  "classification": "safe | fragile | unknown",
  "classification_rationale": "<substrate-grounded explanation>"
}
```

The internal computation walks service-spec resolution at preview
time (no device interaction; pure intent-replay logic). Reusable
from the `/api/preview`-equivalent surfaces.

**Tests:** synthetic topologies with known dependencies (independent
service applications → "safe"; coordinated BGP fabric change →
"fragile"); rationale string contains substrate-level edge names.

**Estimated effort:** substantive. The dependency-graph derivation
is the bulk; the HTTP surface is straightforward once the
derivation works. Standalone PR; no shared call sites with the
other issues.

**Implementation hint:** the existing service-spec resolution code
(in `service_gen.go` and per-noun ops) already does the field-by-
field resolution that produces these dependencies. A
read-side wrapper that re-runs resolution in "trace edges"
mode rather than "produce ChangeSet" mode would expose the same
information without duplicating logic.

---

## Summary table

| # | Cluster | Title | Files touched | New types | Principle gates |
|---|---------|-------|---------------|-----------|-----------------|
| 11 | B | Structured `ChangeSet` in `WriteResult` | `pkg/newtron/types.go` + `WriteResult` callers | none (reuses `sonic.ConfigChange`) | §46, §11 strengthened |
| 5 | A | Per-Node projection read | `pkg/newtron/network/node/node.go`, `pkg/newtron/api/handler_node.go`, `pkg/newtron/api/handler.go` | none (reuses `sonic.RawConfigDB`) | §46, §1, §21 |
| 14 | C | Full topology read | `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go` | none (reuses `spec.TopologySpecFile`) | §46, §7 |
| 15 | C | Topology node CRUD | `pkg/newtron/network/network.go`, `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go`, `pkg/newtron/api/types.go` | `TopologyNodeCreateRequest` (reuses `spec.TopologyDevice`) | §46, §7, §16 |
| 16 | C | Topology link CRUD | `pkg/newtron/network/network.go`, `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go` | none (reuses `spec.TopologyLink`) | §46, §7, §16 |
| 4 | A | Projection diff (before-vs-after) | `pkg/newtron/network/node/node.go`, `pkg/newtron/api/handler_node.go`, `pkg/newtron/api/handler.go`, `pkg/newtron/api/types.go` | `ProjectionDiff`, `ProjectionDiffRequest` | §46, §1, §21 (reuses `DriftEntry`) |
| 6 | A | Per-service projection slice | `pkg/newtron/network/network.go`, `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go` | `ServiceProjection`, `ServiceProjectionNode`, `ServiceProjectionOpts` | §46, §6 (reuses `DriftEntry`) |
| 12 | B | Call-site provenance (verbose) | `pkg/newtron/network/node/source.go` (new), `pkg/newtron/device/sonic/types.go`, `pkg/newtron/network/node/changeset.go`, `pkg/newtron/types.go`, all `WriteResult.Changes` callers | `VerboseConfigChange` | §46, §33 — verbose-only resolution required |
| 13 | D | Cross-target dependency exposure | new derivation module (TBD location), `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go`, `pkg/newtron/api/types.go` | `DependencyGraph`, `DependencyEdge`, `DependencyGraphRequest` | §46 applied to derived substrate; ophil #1, #9 |

## Closing each issue

When a PR lands implementing one of these:

- The PR description must include "Closes aldrin-isaac/newtron#N".
- Update this document's status block at the top (date, which issues
  remain open).
- Add a one-line note in the per-issue section ("Landed in commit X,
  PR #N").
- When all five are closed, this document is archived (rename to
  `*.archived.md` or move to `docs/scoping/archive/`).

## Cross-references

- newtron principles: `../DESIGN_PRINCIPLES_NEWTRON.md`, especially
  [`§46`](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
  (HTTP API Boundary — Wire Shape Mirrors Substrate; the unifying
  principle for this batch),
  [`§11`](../DESIGN_PRINCIPLES_NEWTRON.md#11-the-changeset-is-the-universal-contract)
  (ChangeSet is the Universal Contract),
  [`§33`](../DESIGN_PRINCIPLES_NEWTRON.md#33-public-api-boundary--types-express-intent-not-implementation)
  (Public API Boundary — Types Express Intent, Not Implementation).
- newtron pipeline: `../newtron/unified-pipeline-architecture.md`.
- newtcon Gap-Handling Protocol (the originating discipline):
  `../../../newtcon/CLAUDE.md` §Gap-Handling Protocol.
- Source issues (original 2026-05-26 batch):
  - <https://github.com/aldrin-isaac/newtron/issues/4>
  - <https://github.com/aldrin-isaac/newtron/issues/5>
  - <https://github.com/aldrin-isaac/newtron/issues/6>
  - <https://github.com/aldrin-isaac/newtron/issues/11>
  - <https://github.com/aldrin-isaac/newtron/issues/12>
- Source issues (extended 2026-05-26 batch):
  - <https://github.com/aldrin-isaac/newtron/issues/13> — cross-target dependency
  - <https://github.com/aldrin-isaac/newtron/issues/14> — full topology read
  - <https://github.com/aldrin-isaac/newtron/issues/15> — topology node CRUD
  - <https://github.com/aldrin-isaac/newtron/issues/16> — topology link CRUD
