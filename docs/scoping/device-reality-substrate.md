# Cluster D — Device-reality substrate

**Status:** scoped, pending implementation. Part of the
[newtcon-driven gaps batch](newtcon-driven-gaps.md); see the parent
doc for the unifying §46 principle, style invariants, and
cross-cluster phasing.

## Scope

One issue (so far), instance of §46 applied to **device-reality
substrate** — the actual, current CONFIG_DB state on a connected
device as distinct from newtron's projection (which represents what
newtron *believes* should be on the device).

- [newtron#17](https://github.com/aldrin-isaac/newtron/issues/17) — Per-Node raw CONFIG_DB snapshot read endpoint

## Why a separate cluster from projection (Cluster A)

newtron carries two substrate types side-by-side:

- **Projection** (Cluster A) — the typed `RawConfigDB` derived
  internally from intent replay (`ConfigDB.ExportEntries()`). What
  newtron believes should be on the device.
- **Device reality** (this cluster) — the typed `RawConfigDB` read
  directly from the device's CONFIG_DB
  (`ConfigDBClient.GetRawOwnedTables(ctx)`). What is actually on
  the device right now.

Both have the same Go type (`sonic.RawConfigDB`); they have
different sources, different freshness semantics, and different
operator-facing meanings. Drift is precisely the diff between them
(`sonic.DiffConfigDB`). Cluster A exposes the projection; Cluster D
exposes reality.

Conflating them into one cluster would obscure the substrate
distinction. Keeping them separate makes the substrate-vs-reality
duality (`DESIGN_PRINCIPLES_NEWTRON.md` §1) visible at the scope-doc
level.

## Shared load-bearing primitive

- **`ConfigDBClient.GetRawOwnedTables(ctx) sonic.RawConfigDB`** —
  already exists at `pkg/newtron/device/sonic/configdb_diff.go:247`.
  newtron uses this internally for every drift detection
  (`Node.Drift` and `Node.reconcileDelta` both call it; the result
  is consumed to compute drift and then discarded). The proposed
  endpoint exposes the call's result directly.

## Implementation order (within this cluster)

Trivial. Single endpoint that wraps the existing internal primitive.
Phase 1 in the parent's cross-cluster ordering (batched with #11,
#5, #14 — all trivial reads, one PR).

---

## 1. `newtron#17` — Per-Node raw CONFIG_DB snapshot read

### Principle check

**§46 (load-bearing):** the typed `RawConfigDB` is canonical
substrate that the spec-and-sonic layer already produces internally.
Today, newtron exposes only per-table-per-key reads (`/configdb/{table}`,
`/configdb/{table}/{key}`); consumers needing a full snapshot must
stitch ~hundreds of HTTP calls per Node and lose any internal
consistency guarantee across the stitch. §46 rule 4 ("wire shape
mirrors in-memory shape") demands the canonical bulk read.

**§1 supports:** intent and reality are the same object viewed from
different starting points. Reading reality is a first-class
operation; surfacing it as substrate is the principle in action.

### Implementation

**Node method** in `pkg/newtron/network/node/node.go`, alongside the
existing intent-side reads (`Tree`, `Drift`, `Intents`):

```go
// ConfigDBSnapshot returns the device's actual CONFIG_DB state across
// all newtron-owned tables (or all tables if ownedOnly is false).
// This is a single internally-consistent snapshot computed by
// iterating OwnedTables() and stitching per-table reads; the result
// is the same RawConfigDB shape Drift uses internally.
//
// Auto-connects transport if needed.
func (n *Node) ConfigDBSnapshot(ctx context.Context, ownedOnly bool) (sonic.RawConfigDB, error) {
    if n.conn == nil {
        if err := n.ConnectTransport(ctx); err != nil {
            return nil, fmt.Errorf("connecting transport: %w", err)
        }
    }
    if ownedOnly {
        return n.conn.Client().GetRawOwnedTables(ctx)
    }
    return n.conn.Client().GetRawAllTables(ctx)
}
```

Reuses the existing `GetRawOwnedTables` (and an analogous
`GetRawAllTables` if not present — verify during implementation).

**HTTP handler** in `pkg/newtron/api/handler_node.go`, in the
CONFIG_DB read section (near `handleQueryConfigDB`,
`handleConfigDBTableKeys`):

```go
func (s *Server) handleConfigDBSnapshot(w http.ResponseWriter, r *http.Request) {
    _, nodeActor := s.requireNodeActor(w, r)
    if nodeActor == nil {
        return
    }
    ownedOnly := r.URL.Query().Get("owned_only") != "false"  // default true
    val, err := nodeActor.connectAndRead(r.Context(), func(n *newtron.Node) (any, error) {
        return n.ConfigDBSnapshot(r.Context(), ownedOnly)
    })
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, val)
}
```

**Route** in `pkg/newtron/api/handler.go`, alongside the existing
`/configdb/...` routes:

```go
mux.HandleFunc("GET /network/{netID}/node/{device}/configdb", s.handleConfigDBSnapshot)
```

Path is intentionally the un-suffixed `/configdb` (returning the
whole CONFIG_DB), distinct from `/configdb/{table}` (one table) and
`/configdb/{table}/{key}` (one entry).

**Response shape** (per issue body): the bare `sonic.RawConfigDB`
JSON. Optionally extend with a wrapper carrying `node`, `network`,
`read_at`, `owned_tables`, and the `configdb` payload if the
wrapper-form is preferred during implementation; the canonical
substrate is the inner `configdb` field.

**Optional query parameter `table` (repeatable):** restrict to one
or more tables. Defers; the bare `owned_only=true|false` covers the
primary need.

**Tests:** assert that the round-trip JSON shape matches
`RawConfigDB`; assert that on a Node with a known fixture, the
snapshot contains expected tables and entries; assert that
`owned_only=false` returns strictly more tables than the default
(when non-owned tables exist on the device).

**Estimated effort:** trivial. One Node method (thin wrapper),
one handler, one route. Reuses the existing internal primitive
exactly. Batched into Phase 1 with `newtron#11`, `newtron#5`, and
`newtron#14`.

### newtcon-side consequence

Unblocks newtcon#37 (observation-history persistence layer). The
poller calls this endpoint at minutes cadence per Node and stores
the returned `RawConfigDB` snapshot in newtcon's SQLite history
store, computing diffs between consecutive snapshots via the same
`sonic.DiffConfigDB` algorithm newtron uses for drift. Until this
gap closes, the poller cannot operate honestly (stitching N×M reads
gives an inconsistent snapshot).
