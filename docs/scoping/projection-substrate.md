# Cluster A — Projection substrate

**Status:** scoped, pending implementation. Part of the
[newtcon-driven gaps batch](newtcon-driven-gaps.md); see the parent
doc for the unifying §46 principle, style invariants, and
cross-cluster phasing.

## Scope

Three issues, all instances of §46 applied to newtron's **projection
substrate** — the typed `RawConfigDB`-shaped state derived from
intent replay (`DESIGN_PRINCIPLES_NEWTRON.md` §1, §21):

- [newtron#5](https://github.com/aldrin-isaac/newtron/issues/5) — Per-Node projection read endpoint
- [newtron#4](https://github.com/aldrin-isaac/newtron/issues/4) — Projection diff (before-vs-after) endpoint
- [newtron#6](https://github.com/aldrin-isaac/newtron/issues/6) — Per-service projection slice endpoint

## Shared load-bearing primitives

All three issues share the same internal primitives — already
present in newtron, never re-derived:

- **`ConfigDB.ExportEntries() sonic.RawConfigDB`** — returns the
  typed projection. #5 exposes it directly; #4 and #6 capture
  before/after snapshots around in-memory replay.
- **`sonic.DiffConfigDB(expected, actual, ownedTables) []DriftEntry`**
  — canonical entry-level diff. Reused as the diff vocabulary
  across all three endpoints. §11 (ChangeSet is the Universal
  Contract) and §46 (HTTP API Boundary) together forbid inventing
  parallel `ProjectionEntry` or `SliceEntry` types.
- **`Node.SnapshotIntentDB()` / `RestoreIntentDB(snap)`** —
  in-memory snapshot/restore used by #4 and #6 to apply ops in
  dry-run without device interaction. Preserves §20 (intent records
  are sufficient for reconstruction) by restoring the intent DB to
  its pre-call state.

## Implementation order (within this cluster)

1. **#5 first** — trivial, foundational. Exposes the projection
   read primitive #4 and #6 conceptually build on. (#4 and #6 can
   compute the same substrate independently, but #5 first minimizes
   rework.)
2. **#4 next** — moderate; adds the snapshot/restore-with-ops loop.
3. **#6 last** — moderate; reuses #4's replay-diff technique with
   inverse staging ("remove the service's intents" instead of "stage
   additions").

---

## 1. `newtron#5` — Per-Node projection read endpoint

_Landed on branch `impl/phase-1-newtron-substrate-gaps` (Phase 1 batch)._

### Principle check

**§46 (load-bearing):** the `Projection` is canonical substrate
that represents "what newtron believes this device should look
like" (`§1`). Today it is only exposed via per-resource summary
views (`/vlans`, `/vrfs`, `/interfaces`) and via device-side CONFIG_DB
raw reads (which read the device, not the projection). §46 requires
the canonical typed form directly. This endpoint adds it.

**§1, §21 support:** the projection is rebuilt from intent replay
(`§21`); exposing it as a typed read is what `§1` invites operators
to reason about. The method reads `n.configDB.ExportEntries()`,
which is the same primitive already used internally by
`reconcileFull` (`node.go:420`) — no new state, no new
representation.

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
operations section (currently around line 1037, alongside
`handleTree`, `handleDrift`, `handleReconcile`):

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
mux.HandleFunc("GET /networks/{netID}/nodes/{device}/intent/projection", s.handleProjection)
```

**Tests:** `pkg/newtron/api/api_test.go` — assert the endpoint
returns the projection shape; round-trip with a known intent set
produces the expected per-table-per-key entries; empty Node returns
empty map.

**Estimated effort:** trivial. One method, one handler, one route,
one test.

---

## 2. `newtron#4` — Projection diff (before-vs-after) endpoint

_Landed on branch `impl/phase-4-projection-diff` (Phase 4 batch). Implemented
via Node.ProjectionDiff(ctx, ops): snapshots intent DB, clears actuatedIntent
to bypass the Lock guard during in-memory replay, runs ReplayStep over the
hypothetical ops, captures the resulting projection, then restores intent DB
+ projection (re-rebuild from snapshot) so the Node's observable state is
unchanged. Returns ProjectionDiffResult{before, after, diff} with diff in the
canonical sonic.DriftEntry vocabulary (§11). Operationalizes operator-
philosophy invariant #4 (show before do) at the substrate level._

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
used by `handleSave` and the intent/reconcile path. Locate the exact
name during implementation — `handleSave` (`handler_node.go:1111`)
builds `[]spec.TopologyStep` from `n.Tree().Steps`, so the apply path
consumes that shape.

**Request type** in `pkg/newtron/api/types.go`:

```go
// ProjectionDiffRequest is the body for POST .../intent/projection-diff.
// Operations carry the same TopologyStep shape consumed by intent/save.
type ProjectionDiffRequest struct {
    Operations []spec.TopologyStep `json:"operations"`
}
```

**HTTP handler** in `handler_node.go`, in the Intent operations
section:

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
skips the delivery pipeline, matching the in-memory-only nature of
the diff. Matches `handleReload` (`handler_node.go:1146`).

**Route** in `handler.go`:

```go
mux.HandleFunc("POST /networks/{netID}/nodes/{device}/intent/projection-diff", s.handleProjectionDiff)
```

**Tests:** empty ops list returns `Before == After` with empty
`Diff`; a single `CreateVLAN` produces one `Added` `DriftEntry` on
the `VLAN` table; the intent DB after the call equals the intent DB
before (snapshot/restore correctness).

**Estimated effort:** moderate. Reuses `SnapshotIntentDB`,
`RestoreIntentDB`, `ExportEntries`, `DiffConfigDB`, and the internal
step-application path.

---

## 3. `newtron#6` — Per-service projection slice endpoint

_Landed on branch `impl/phase-3-service-projection` (Phase 3 batch).
Implemented via the replay-diff technique on each Node: snapshot intent DB,
trim the service's apply-service intents, rebuild projection from the trimmed
set, diff against the full projection. Handler iterates over NodeActors (the
api-layer cache of built nodes) since `net.devices` is only populated by
explicit GetNode calls. Operationalizes operator-philosophy invariant #5
(why-mode at service scope) per the §11 diff vocabulary._

### Principle check

**§46 (load-bearing):** the per-service projection slice is the
canonical "service contribution to the network" substrate. Today,
deriving it requires stitching across N intent-tree calls + N
projection re-derivations — exactly the cross-endpoint stitching
§46 rejects. This endpoint surfaces the canonical slice directly.

**§6 supports:** Interface is the point of service; service-first
exposure aligns with `CLAUDE.md` §Design Principles. **§11
supports:** slice shape reuses `DriftEntry` per §46 rule 3 (one
typed diff vocabulary).

The slice is computed via the replay-diff technique using existing
`SnapshotIntentDB` / `RestoreIntentDB` / `RebuildProjection` /
`DiffConfigDB`. No new primitives, no parallel diff vocabulary.

### Implementation strategy

The slice is "the projection rows that exist because the named
service is bound on this Node." Computed by:

1. Snapshot intent DB.
2. Remove the service's intents (those under the `service|{name}`
   prefix in the intent DB).
3. Rebuild projection from the trimmed intent DB.
4. Diff the original projection against the trimmed projection.
5. The `Added` entries in the diff (relative to the trimmed
   baseline) are the service's contribution.
6. Restore.

Same snapshot/replay/restore pattern as `ProjectionDiff` (#4),
applied differently: instead of staging additions, stage removals;
instead of returning the staged-additions diff, return the inverse
(what's lost when the service is removed = what the service
contributes).

### Implementation

**Type** in `pkg/newtron/network/network.go` (network-level
concept, since a service is network-scoped):

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
    ne := s.requireNetwork(w, r)
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

(The exact network-level actor method name — `read` here is
illustrative — should match what `handler_network.go` already uses;
verify against existing network-level handlers during
implementation.)

**Route** in `handler.go`, alongside other
`/networks/{netID}/services/...` routes:

```go
mux.HandleFunc("GET /networks/{netID}/services/{service}/projection", s.handleServiceProjection)
```

**Tests:** for each service kind (`routed`, `bridged`,
`evpn-bridged`, `evpn-irb`), assert the slice contains the expected
projection rows (e.g., for `evpn-bridged`: VLAN, VLAN_INTERFACE,
VXLAN_TUNNEL_MAP, VRF, BGP_NEIGHBOR, ROUTE_REDISTRIBUTE,
COMMUNITY_SET, ROUTE_MAP, ACL_TABLE). Restrict-by-node and
restrict-by-table filters round-trip correctly.

**Estimated effort:** moderate. The replay-diff implementation is
non-trivial — must walk the right intents, exclude only the
service's, rebuild correctly. Test thoroughly across service types.
