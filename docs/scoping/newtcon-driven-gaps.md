# newtcon-driven gaps — parent scope doc

**Status:** scoped, pending implementation. Operator-driven; the
newtcon autonomous team does not touch newtron, so all gaps in
this batch are implemented manually using normal Claude Code
assistance.

**Scope date:** 2026-05-26 (original batch of 5; extended same day
with 4 more). This document is the **parent** for a cluster-split
scoping structure; see [`README.md`](README.md) for the directory
layout. Update each cluster doc as its issues land; archive the
whole `docs/scoping/` tree once every issue closes.

## Purpose

newtcon (`github.com/aldrin-isaac/newtcon`) consumes newtron only
over HTTP — no Go imports, no subprocess invocations, no direct
Redis access. When newtcon needs functionality newtron's HTTP API
does not yet expose, the gap is filed against newtron (newtron-repo
issue, not newtcon-repo), and newtcon's contract marks the affected
`manual_equivalent.newtron_http` field as `pending_newtron_gap`
with a forward link to the newtron issue.

This document scopes nine such gaps, organized into four
**clusters** by shared underlying primitive. Each cluster has its
own scoping document with per-issue implementation detail. The
parent document (this one) covers what is shared across clusters:
the unifying §46 principle, style invariants, cross-cluster
phasing, status protocol, and the standalone Cluster D appendix.

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
| `#13` | D. Cross-target reasoning | No exposure of cross-Node dependency edges | New computed substrate: typed dependency edges derived from service-spec resolution (edges only — classification is newtcon-side policy, not newtron substrate) |

The unifying citation in each per-issue principle check is §46,
with other principles (§1, §7, §11, §16, §21, §33) cited as
supporting context per cluster.

## Clusters

Four clusters, each grouping issues by the load-bearing internal
primitive they share. Each non-trivial cluster has a dedicated
scoping document with full per-issue implementation detail.

| Cluster | Issues | Shared load-bearing primitive | Scope doc |
|---------|--------|-------------------------------|-----------|
| **A. Projection substrate** | #4, #5, #6 | `ConfigDB.ExportEntries` + `sonic.DiffConfigDB` + `SnapshotIntentDB`/`RestoreIntentDB` | [`projection-substrate.md`](projection-substrate.md) |
| **B. ChangeSet substrate** | #11, #12 | `sonic.ConfigChange` + all `WriteResult`-construction sites | [`changeset-substrate.md`](changeset-substrate.md) |
| **C. Topology spec substrate** | #14, #15, #16 | `spec.Loader.SaveTopology` + `spec.TopologySpecFile`/`TopologyDevice`/`TopologyLink` + `validateTopology` | [`topology-spec-substrate.md`](topology-spec-substrate.md) |
| **D. Cross-target reasoning** | #13 | (net-new: cross-Node dependency edges derived from service-spec resolution) | Appendix at the end of this document — kept here because the cluster contains a single issue |

The original five-issue batch (`#4`, `#5`, `#6`, `#11`, `#12`) is
§46 applied to the **runtime layer** (ChangeSet, projection, drift).
The extended four-issue batch (`#13`, `#14`, `#15`, `#16`) extends
§46 to the **spec layer** (topology.json — `#14`/`#15`/`#16`) and
to a **net-new substrate type** (cross-Node dependency edges —
`#13`).

Auditing the existing newtron HTTP API surface against §46 surfaced
only one runtime-layer violation in the original five issues:
`WriteResult.Preview` without `Changes`, which `#11` addresses.
Every other runtime endpoint already returns typed substrate. The
topology-layer gaps (`#14`, `#15`, `#16`) are spec-layer
violations: typed substrate exists internally; only HTTP exposure
is missing. §46 is largely descriptive of how newtron already
operates; codifying it locks the discipline in for future API
additions.

## Style invariants (apply to every cluster)

- **Handlers** live in `pkg/newtron/api/handler_node.go` (node-
  scoped) or `pkg/newtron/api/handler_network.go` (network-scoped),
  grouped with semantically related handlers. Handler signature is
  `func (s *Server) handleXxx(w http.ResponseWriter, r *http.Request)`,
  using `requireNodeActor` (or the network equivalent) for actor
  resolution and `writeJSON` / `writeError` for response.
- **Routes** are registered in `pkg/newtron/api/handler.go`
  (`buildMux()`) with HTTP-method-prefixed paths:
  `"GET /network/{netID}/node/{device}/intent/..."`. Intent operations
  live under `/intent/`; CONFIG_DB reads under `/configdb/`; service
  reads under `/service/{service}/`; topology operations under
  `/topology/`.
- **Public Node methods** are PascalCase verbs returning
  `(value, error)` or just `value`. No `Get*` prefix; the method's
  noun-return communicates the read. Examples already present in
  `pkg/newtron/network/node/node.go`: `Tree`, `Drift`, `Intents`,
  `Reconcile`, `RebuildProjection`, `ConfigDB`.
- **Domain types** live in `pkg/newtron/network/node/node.go` near
  related types (e.g., `ReconcileResult`, `ReconcileOpts`).
- **Diff vocabulary**: `sonic.DriftEntry` is the canonical entry-
  delta type, defined in `pkg/newtron/device/sonic/configdb_diff.go`
  and used by `DiffConfigDB`. Do not invent parallel `ChangeEntry`,
  `DiffEntry`, or per-feature diff types. §11 (ChangeSet is the
  Universal Contract) binds the project to one diff representation.
- **Actor patterns** in `handler_node.go`:
  - `nodeActor.connectAndRead(ctx, fn)` for reads that need
    transport.
  - `nodeActor.do(ctx, fn)` for in-memory mutations that hold the
    lock but skip the delivery pipeline.
  - `nodeActor.execute(ctx, fn)` for full delivery
    (validate → write → verify → record).
- **Query parameters** for opt-in behavior follow the existing
  convention: `?dry_run=true`, `?no_save=true`, `?verbose=true`.
  Not body fields; not headers; not URL-embedded mode segments.

## Implementation order (across clusters)

Suggested phased ordering. Each phase is a natural PR boundary;
within a phase, issues either share a PR (when they share a call
site) or can be batched into one PR (when each is trivial). The
phasing minimizes coupling cost and front-loads the highest
unblock-value-per-effort items.

| Phase | Issue | Cluster | Complexity | Unblocks (newtcon-side) |
|-------|-------|---------|------------|--------------------------|
| **1** | `newtron#11` | B | trivial | broad newtcon coverage; per-write granularity |
| **1** | `newtron#5` | A | trivial | Provenance surface; observation-history poller |
| **1** | `newtron#14` | C | trivial | graphical topology viz; spec-authoring readback |
| **2** | `newtron#15` | C | moderate | spec-authoring (topology) — node lifecycle |
| **2** | `newtron#16` | C | moderate | spec-authoring (topology) — link lifecycle |
| **3** | `newtron#4` | A | moderate | Workbench dry-run; pre-commit diff |
| **4** | `newtron#6` | A | moderate | service-first projection views |
| **5** | `newtron#12` | B | moderate (pending operator decision) | "Report this is the bug" affordance |
| **6** | `newtron#13` | D | substantive (net-new substrate) | safe-vs-fragile multi-target classification |

**Phasing rationale:**

- **Phase 1** — three trivial reads, one PR. Each is ~10–20 lines
  of new code, all §46-aligned, all unblock substantive newtcon-
  side work. No coupling between them; batched only for review
  efficiency.
- **Phase 2** — `#15` + `#16` in one PR. They share the
  `Loader.SaveTopology` call site, the same Network method-layer
  pattern, and the same validation hook. Splitting them across two
  PRs would create churn in the same files. `#14` (Phase 1)
  precedes Phase 2 logically — operators read after they mutate —
  but the implementations don't depend on each other.
- **Phase 3** — `#4` (projection diff) builds on the snapshot/
  restore primitives `#5` (Phase 1) makes visible. Single PR.
- **Phase 4** — `#6` (per-service slice) reuses `#4`'s replay-diff
  technique with the inverse staging ("remove the service's
  intents" instead of "stage additions"). Single PR.
- **Phase 5** — `#12` (verbose-mode call-site provenance) requires
  the operator decision on §33 reconciliation (verbose-mode opt-in)
  in the issue body before implementation; defers naturally to
  after most other work.
- **Phase 6** — `#13` is the heaviest item: net-new substrate (the
  cross-Node dependency graph), independent of the other gaps,
  warrants its own PR with substantial design.

## Status protocol

Each issue carries a status visible in the GitHub issue queue:

- **OPEN** — scoped (this document), pending implementation.
- **OPEN with PR linked** — implementation in flight.
- **CLOSED** — landed. The cluster doc adds a one-line note ("Landed
  in commit X, PR #N") in the relevant per-issue section.

When all nine issues close, the entire `docs/scoping/` tree is
archived: rename to `docs/scoping/archive/2026-05-26-newtcon-gaps/`
or similar. The closing protocol applies per cluster doc as well
as to this parent.

## Cluster D appendix — `newtron#13` (cross-target dependency exposure)

Cluster D currently contains a single issue. Rather than splitting
out a one-entry cluster doc, the per-issue detail lives here. If a
second cross-target gap arrives, split `cross-target-reasoning.md`
out of this appendix at that time.

### Principle check

**§46 (load-bearing, applied to net-new substrate):** the cross-
Node dependency graph is substrate that newtron **can** compute
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

### Refined scope — substrate only, not classification

The originally-filed issue body proposed returning
`classification: "safe | fragile | unknown"` alongside the edges.
**Operator review (2026-05-26) refined this:** classification is
operator-facing policy, not newtron substrate. Per §46 rule 1
("canonical first, summary second"), `classification: "fragile"` is
a summary of richer substrate (the typed edges with their kinds).
Putting summary into newtron's response duplicates the §46
violation pattern.

newtron exposes the typed dependency **edges** with `kind`. newtcon
(or any consumer) applies the classification rule — typically "any
edge of kind `bgp_peer_resolution` implies fragility" — at the
consumer side, where the operator-facing policy lives. The
classification is therefore newtcon-side concern, tracked under
newtcon issue #22 ("Preview should distinguish safe-under-partial-
success from fragile multi-target operations"). newtron's job is to
provide the substrate that makes honest classification possible.

The issue body needs updating to reflect this refinement; see
[Open follow-up](#open-follow-up) below.

### Implementation

This is the substantive item in the queue. New internal substrate
required:

- A **dependency-edge computation** that walks the resolved service-
  spec graph and identifies cross-Node references (BGP peer
  resolution, route-policy cross-references, EVPN peer-group
  consistency requirements).
- An HTTP endpoint that returns the dependency edges relevant to a
  proposed multi-target operation, with substrate-level rationale
  per edge. **No classification field in the response.**

Proposed shape:

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
  ]
}
```

No `classification`, no `classification_rationale`. The consumer
applies the policy.

The internal computation walks service-spec resolution at preview
time (no device interaction; pure intent-replay logic). Reusable
from the `/api/preview`-equivalent surfaces.

**Tests:** synthetic topologies with known dependencies (independent
service applications → empty edges list; coordinated BGP fabric
change → edges with `bgp_peer_resolution` kind); rationale string
contains substrate-level edge names.

**Estimated effort:** substantive. The dependency-edge derivation
is the bulk; the HTTP surface is straightforward once the
derivation works. Standalone PR; no shared call sites with the
other issues.

**Implementation hint:** the existing service-spec resolution code
(in `service_gen.go` and per-noun ops) already does the field-by-
field resolution that produces these dependencies. A read-side
wrapper that re-runs resolution in "trace edges" mode rather than
"produce ChangeSet" mode would expose the same information without
duplicating logic.

### Open follow-up

`newtron#13`'s issue body still describes the
classification-included shape. After this scope-doc revision lands,
add a comment on `newtron#13` referencing this section and noting
the scope refinement (substrate-only, classification moves to
newtcon-side policy). The classifier work is tracked separately as
[newtcon#22](https://github.com/aldrin-isaac/newtcon/issues/22).

## Cross-references

- newtron principles: [`../DESIGN_PRINCIPLES_NEWTRON.md`](../DESIGN_PRINCIPLES_NEWTRON.md), especially
  [§46](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
  (HTTP API Boundary — Wire Shape Mirrors Substrate; the unifying
  principle for this batch),
  [§11](../DESIGN_PRINCIPLES_NEWTRON.md#11-the-changeset-is-the-universal-contract)
  (ChangeSet is the Universal Contract),
  [§33](../DESIGN_PRINCIPLES_NEWTRON.md#33-public-api-boundary--types-express-intent-not-implementation)
  (Public API Boundary — Types Express Intent, Not Implementation),
  [§7](../DESIGN_PRINCIPLES_NEWTRON.md#7-definition-is-network-scoped-execution-is-device-scoped)
  (Definition Is Network-Scoped).
- newtron pipeline: [`../newtron/unified-pipeline-architecture.md`](../newtron/unified-pipeline-architecture.md).
- newtcon Gap-Handling Protocol (the originating discipline):
  `../../../newtcon/CLAUDE.md` §Gap-Handling Protocol.
- Cluster scope docs:
  - [`projection-substrate.md`](projection-substrate.md) — Cluster A (`#4`, `#5`, `#6`).
  - [`changeset-substrate.md`](changeset-substrate.md) — Cluster B (`#11`, `#12`).
  - [`topology-spec-substrate.md`](topology-spec-substrate.md) — Cluster C (`#14`, `#15`, `#16`).
- Source issues (original 2026-05-26 batch):
  - <https://github.com/aldrin-isaac/newtron/issues/4>
  - <https://github.com/aldrin-isaac/newtron/issues/5>
  - <https://github.com/aldrin-isaac/newtron/issues/6>
  - <https://github.com/aldrin-isaac/newtron/issues/11>
  - <https://github.com/aldrin-isaac/newtron/issues/12>
- Source issues (extended 2026-05-26 batch):
  - <https://github.com/aldrin-isaac/newtron/issues/13> — cross-target dependency (refined to edges-only)
  - <https://github.com/aldrin-isaac/newtron/issues/14> — full topology read
  - <https://github.com/aldrin-isaac/newtron/issues/15> — topology node CRUD
  - <https://github.com/aldrin-isaac/newtron/issues/16> — topology link CRUD
