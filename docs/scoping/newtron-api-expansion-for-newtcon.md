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

## Common thread — all five align to §46

The five gaps are not independent design decisions. Each is an
instance of the same principle: **newtron's HTTP API should expose
its canonical in-memory substrate types directly, not derivatives,
summaries, or opaque handles.** This is codified as
[`DESIGN_PRINCIPLES_NEWTRON.md` §46](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
("HTTP API Boundary — Wire Shape Mirrors Substrate").

Re-reading the five issues through the §46 lens:

| Issue | Today's response | Canonical substrate the principle says to expose |
|-------|-----------------|--------------------------------------------------|
| `#11` | `WriteResult.Preview` (string) + `change_count` | `ChangeSet.Changes` (`[]sonic.ConfigChange`) |
| `#5` | Per-resource summary views, or device CONFIG_DB raw reads | `Projection` (the output of `ExportEntries`) |
| `#4` | Free-text preview string + entry count for hypothetical mutations | Before-`Projection` + After-`Projection` + `[]DriftEntry` |
| `#6` | Per-device intent records, or spec definitions, requiring N-call stitching | Per-Node `[]DriftEntry` slice for the service |
| `#12` | ChangeSet entries without generation provenance | `Source` field (verbose-only, per §33 boundary) |

The unifying citation in each per-issue principle check below is §46,
with the other principles (§1, §11, §21, §33) cited as supporting
context.

Auditing the existing newtron HTTP API surface against §46 surfaced
only one further violation: `WriteResult.Preview` without `Changes`,
which `#11` already addresses. Every other endpoint already returns
typed substrate. §46 is largely descriptive of how newtron already
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

| Order | Issue | Complexity | Unblocks (newtcon-side) |
|-------|-------|------------|-------------------------|
| 1 | `newtron#11` — Structured `ChangeSet` in `WriteResult` | trivial | broad newtcon coverage; per-write granularity |
| 2 | `newtron#5` — Per-Node projection read endpoint | trivial | Provenance surface; observation-history poller |
| 3 | `newtron#4` — Projection diff (before-vs-after) | moderate | Workbench dry-run; pre-commit diff |
| 4 | `newtron#12` — Call-site provenance (verbose mode) | moderate | "Report this is the bug" affordance |
| 5 | `newtron#6` — Per-service projection slice | moderate | service-first projection views |

`newtron#11` and `newtron#5` are foundational and trivial; do them first.
`newtron#4` builds on the snapshot/restore primitives `newtron#5` makes
visible. `newtron#12` requires an operator decision (verbose-mode
resolution) before implementation. `newtron#6` reuses the replay-diff
technique from `newtron#4` and is last.

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

## Summary table

| # | Title | Files touched | New types | Principle gates |
|---|-------|---------------|-----------|-----------------|
| 11 | Structured `ChangeSet` in `WriteResult` | `pkg/newtron/types.go` + `WriteResult` callers | none (reuses `sonic.ConfigChange`) | §11 strengthened |
| 5 | Per-Node projection read | `pkg/newtron/network/node/node.go`, `pkg/newtron/api/handler_node.go`, `pkg/newtron/api/handler.go` | none (reuses `sonic.RawConfigDB`) | §1, §21 |
| 4 | Projection diff (before-vs-after) | `pkg/newtron/network/node/node.go`, `pkg/newtron/api/handler_node.go`, `pkg/newtron/api/handler.go`, `pkg/newtron/api/types.go` | `ProjectionDiff`, `ProjectionDiffRequest` | §1, §21, §11 (reuses `DriftEntry`) |
| 12 | Call-site provenance (verbose) | `pkg/newtron/network/node/source.go` (new), `pkg/newtron/device/sonic/types.go`, `pkg/newtron/network/node/changeset.go`, `pkg/newtron/types.go`, all `WriteResult.Changes` callers | `VerboseConfigChange` | public/internal boundary — verbose-only resolution required |
| 6 | Per-service projection slice | `pkg/newtron/network/network.go`, `pkg/newtron/api/handler_network.go`, `pkg/newtron/api/handler.go` | `ServiceProjection`, `ServiceProjectionNode`, `ServiceProjectionOpts` | §6, §11 (reuses `DriftEntry`) |

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
- Source issues:
  - <https://github.com/aldrin-isaac/newtron/issues/4>
  - <https://github.com/aldrin-isaac/newtron/issues/5>
  - <https://github.com/aldrin-isaac/newtron/issues/6>
  - <https://github.com/aldrin-isaac/newtron/issues/11>
  - <https://github.com/aldrin-isaac/newtron/issues/12>
