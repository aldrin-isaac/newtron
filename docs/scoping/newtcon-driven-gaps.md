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

This document scopes eight such gaps, organized into three
**clusters** by shared underlying primitive. Each cluster has its
own scoping document with per-issue implementation detail. The
parent document (this one) covers what is shared across clusters:
the unifying §46 principle, style invariants, cross-cluster
phasing, and status protocol.

## Common thread — all eight align to §46

The eight gaps are not independent design decisions. Each is an
instance of the same principle: **newtron's HTTP API should expose
its canonical in-memory substrate types directly, not derivatives,
summaries, or opaque handles.** This is codified as
[`DESIGN_PRINCIPLES_NEWTRON.md` §46](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
("HTTP API Boundary — Wire Shape Mirrors Substrate").

Re-reading the eight issues through the §46 lens, grouped by the
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

The unifying citation in each per-issue principle check is §46,
with other principles (§1, §7, §11, §16, §21, §33) cited as
supporting context per cluster.

## Clusters

Three clusters, each grouping issues by the load-bearing internal
primitive they share. Each cluster has a dedicated scoping document
with full per-issue implementation detail.

| Cluster | Issues | Shared load-bearing primitive | Scope doc |
|---------|--------|-------------------------------|-----------|
| **A. Projection substrate** | #4, #5, #6 | `ConfigDB.ExportEntries` + `sonic.DiffConfigDB` + `SnapshotIntentDB`/`RestoreIntentDB` | [`projection-substrate.md`](projection-substrate.md) |
| **B. ChangeSet substrate** | #11, #12 | `sonic.ConfigChange` + all `WriteResult`-construction sites | [`changeset-substrate.md`](changeset-substrate.md) |
| **C. Topology spec substrate** | #14, #15, #16 | `spec.Loader.SaveTopology` + `spec.TopologySpecFile`/`TopologyDevice`/`TopologyLink` + `validateTopology` | [`topology-spec-substrate.md`](topology-spec-substrate.md) |

The original five-issue batch (`#4`, `#5`, `#6`, `#11`, `#12`) is
§46 applied to the **runtime layer** (ChangeSet, projection, drift).
The extended three-issue batch (`#14`, `#15`, `#16`) extends §46 to
the **spec layer** (topology.json).

A fourth would-be cluster — cross-target reasoning, originally filed
as `newtron#13` — was closed as wontfix after operator review
identified the framing as wrong. The fragility-under-partial-success
concern is correlation between operator-chosen batched intents, not
a property of newtron's substrate. Classification lives wholly in
newtcon, tracked as
[newtcon#22](https://github.com/aldrin-isaac/newtcon/issues/22). The
substrates that compose the classification (per-target ChangeSets,
topology, intent records, service definitions, zone configurations)
are exposed via existing endpoints and the already-scoped Cluster A,
B, C gaps. See
[`newtron#13`'s closing comment](https://github.com/aldrin-isaac/newtron/issues/13)
for the full reasoning and a suggested shape for a *different*
follow-up gap (resolution provenance) that may be worth filing.

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

## Status protocol

Each issue carries a status visible in the GitHub issue queue:

- **OPEN** — scoped (this document), pending implementation.
- **OPEN with PR linked** — implementation in flight.
- **CLOSED** — landed. The cluster doc adds a one-line note ("Landed
  in commit X, PR #N") in the relevant per-issue section.

When all eight issues close, the entire `docs/scoping/` tree is
archived: rename to `docs/scoping/archive/2026-05-26-newtcon-gaps/`
or similar. The closing protocol applies per cluster doc as well
as to this parent.

## Closed issues from this batch

- **`newtron#13`** — cross-target dependency exposure (closed
  wontfix 2026-05-26 after operator review identified the framing
  as wrong). The fragility-under-partial-success concern is
  correlation between operator-chosen batched intents, not newtron
  substrate; classification lives wholly in newtcon
  ([newtcon#22](https://github.com/aldrin-isaac/newtcon/issues/22)).
  See [the closing comment](https://github.com/aldrin-isaac/newtron/issues/13)
  for the full reasoning and a suggested shape for a *different*
  follow-up gap (resolution provenance) that may be worth filing
  if newtron's intent records do not currently carry attribution to
  the spec field each resolved value came from.

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
  - <https://github.com/aldrin-isaac/newtron/issues/14> — full topology read
  - <https://github.com/aldrin-isaac/newtron/issues/15> — topology node CRUD
  - <https://github.com/aldrin-isaac/newtron/issues/16> — topology link CRUD
- Closed (wontfix) from this batch:
  - <https://github.com/aldrin-isaac/newtron/issues/13> — cross-target dependency (misframed; classification lives in newtcon#22)
