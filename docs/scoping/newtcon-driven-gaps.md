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

This document scopes ten such gaps, organized into four
**clusters** by shared underlying primitive. Each cluster has its
own scoping document with per-issue implementation detail. The
parent document (this one) covers what is shared across clusters:
the unifying §46 principle, style invariants, cross-cluster
phasing, and status protocol.

## Common thread — all ten align to §46

The ten gaps are not independent design decisions. Each is an
instance of the same principle: **newtron's HTTP API should expose
its canonical in-memory substrate types directly, not derivatives,
summaries, or opaque handles.** This is codified as
[`DESIGN_PRINCIPLES_NEWTRON.md` §46](../DESIGN_PRINCIPLES_NEWTRON.md#46-http-api-boundary--wire-shape-mirrors-substrate)
("HTTP API Boundary — Wire Shape Mirrors Canonical Types").

Re-reading the ten issues through the §46 lens, grouped by the
substrate cluster they touch:

| Issue | Cluster | Today's response | Canonical substrate the principle says to expose |
|-------|---------|-----------------|--------------------------------------------------|
| `#11` | B. ChangeSet | `WriteResult.Preview` (string) + `change_count` | `ChangeSet.Changes` (`[]sonic.ConfigChange`) |
| `#12` | B. ChangeSet | ChangeSet entries without generation provenance | `Source` field on each entry (verbose-only, per §33 boundary) |
| `#19` | B. ChangeSet | Aggregate `WriteResult` only; per-substrate-op data discarded | `per_write[]` of `PerSubstrateOp` + optional SSE streaming variant |
| `#5` | A. Projection | Per-resource summary views, or device CONFIG_DB raw reads | `Projection` (output of `ExportEntries`) |
| `#4` | A. Projection | Free-text preview string + entry count for hypothetical mutations | Before-`Projection` + After-`Projection` + `[]DriftEntry` |
| `#6` | A. Projection | Per-device intent records, or spec definitions, requiring N-call stitching | Per-Node `[]DriftEntry` slice for the service |
| `#14` | C. Topology spec | `GET /topology/node` returning names only (`[]string`) | Full `TopologySpecFile` (devices + links + metadata) |
| `#15` | C. Topology spec | YAML hand-edit + `/reload`; no CRUD verbs | `TopologyDevice` directly, via typed `create-node`/`delete-node`/`update-node` |
| `#16` | C. Topology spec | YAML hand-edit + `/reload`; no CRUD verbs | `TopologyLink` directly, via typed `create-link`/`delete-link` |
| `#17` | D. Device reality | N×M stitched per-table-per-key reads, no internal-consistency guarantee | Full `RawConfigDB` snapshot from `GetRawOwnedTables` |

The unifying citation in each per-issue principle check is §46,
with other principles (§1, §7, §11, §16, §21, §33) cited as
supporting context per cluster.

## Clusters

Four clusters, each grouping issues by the load-bearing internal
primitive they share. Each cluster has a dedicated scoping document
with full per-issue implementation detail.

| Cluster | Issues | Shared load-bearing primitive | Scope doc |
|---------|--------|-------------------------------|-----------|
| **A. Projection substrate** | #4, #5, #6 | `ConfigDB.ExportEntries` + `sonic.DiffConfigDB` + `SnapshotIntentDB`/`RestoreIntentDB` | [`projection-substrate.md`](projection-substrate.md) |
| **B. ChangeSet substrate** | #11, #12, #19 | `sonic.ConfigChange` + all `WriteResult`-construction sites; #19 extends with `per_write[]` and optional SSE streaming | [`changeset-substrate.md`](changeset-substrate.md) |
| **C. Topology spec substrate** | #14, #15, #16 | `spec.Loader.SaveTopology` + `spec.TopologySpecFile`/`TopologyDevice`/`TopologyLink` + `validateTopology` | [`topology-spec-substrate.md`](topology-spec-substrate.md) |
| **D. Device-reality substrate** | #17 | `ConfigDBClient.GetRawOwnedTables` (already used internally for every drift detection) | [`device-reality-substrate.md`](device-reality-substrate.md) |

The original five-issue batch (`#4`, `#5`, `#6`, `#11`, `#12`) is
§46 applied to the **runtime layer** (ChangeSet, projection, drift).
The extended batches (`#14`–`#19`) extend §46 to:

- **The spec layer** (topology.json — `#14`/`#15`/`#16`, Cluster C).
- **Device-reality reads** (raw CONFIG_DB snapshot — `#17`, new Cluster D).
- **Apply-time substrate surfacing** (`per_write[]` + SSE — `#19`, extends Cluster B).

A would-be cluster — cross-target reasoning, originally filed as
`newtron#13` — was closed as wontfix after operator review identified
the framing as wrong. The fragility-under-partial-success concern is
correlation between operator-chosen batched intents, not a property
of newtron's substrate. Classification lives wholly in newtcon,
tracked as
[newtcon#22](https://github.com/aldrin-isaac/newtcon/issues/22). The
substrates that compose the classification (per-target ChangeSets,
topology, intent records, service definitions, zone configurations)
are exposed via existing endpoints and the already-scoped Cluster A,
B, C, D gaps. See
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
| **1** | `newtron#19` (narrowed) | B | small | per-write verbatim device response; folds into #11's PR |
| **1** | `newtron#5` | A | trivial | Provenance surface; observation-history poller |
| **1** | `newtron#14` | C | trivial | graphical topology viz; spec-authoring readback |
| **1** | `newtron#17` | D | trivial | observation-history poller (full snapshot read) |
| **2** | `newtron#15` | C | moderate | spec-authoring (topology) — node lifecycle |
| **2** | `newtron#16` | C | moderate | spec-authoring (topology) — link lifecycle |
| **3** | `newtron#4` | A | moderate | Workbench dry-run; pre-commit diff |
| **4** | `newtron#6` | A | moderate | service-first projection views |
| — | `newtron#12` | B | (deferred indefinitely; 2026-05-26 deep-dive) | not load-bearing for any newtcon surface |
| — | `newtron#19` SSE variant | B | (deferred indefinitely; 2026-05-26 deep-dive) | UX-only; newtcon polls |

**Phasing rationale:**

- **Phase 1** — four trivial reads, one PR. Each is ~10–20 lines
  of new code, all §46-aligned, all unblock substantive newtcon-
  side work. No coupling between them; batched only for review
  efficiency. `#17` (raw CONFIG_DB snapshot) joins this phase because
  it's the same trivial-wrap-of-existing-primitive shape — newtron
  already calls `GetRawOwnedTables` internally during every drift
  detection; the gap is purely HTTP exposure.
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
- **Cluster B post-deep-dive (2026-05-26).** The Phase 5 (`#12`) and
  Phase 6 (`#19`) entries from the original phasing were
  re-evaluated and dropped or trimmed:
  - **`#12` deferred indefinitely** — not load-bearing for any
    newtcon surface; enriches the Report Bug pattern but doesn't
    enable it; §33 reconciliation tax not justified by marginal
    value. Tracked, not scheduled.
  - **`#19` narrowed to a small field addition** — `per_write[]` is
    ~60% redundant with #11 + Verification.Errors given per-Node
    atomicity. Reduced to `VerificationError.DeviceResponse string`
    on existing `VerificationResult.Errors[]`. Folds into #11's PR
    (now Phase 1). The original `per_write[]` array design is not
    implemented.
  - **`#19` SSE variant deferred indefinitely** — streaming is
    operational timing, not substrate per §46; UX-only benefit;
    polling against the existing/scoped endpoint is functional.
    Substantial newtron infrastructure expansion not justified.
  - See the [Cluster B deep-dive outcome at the top of
    `changeset-substrate.md`](changeset-substrate.md) and the
    in-issue deferral/narrowing comments on
    [newtron#12](https://github.com/aldrin-isaac/newtron/issues/12#issuecomment-4551056191)
    and [newtron#19](https://github.com/aldrin-isaac/newtron/issues/19#issuecomment-4551057150).

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
  (HTTP API Boundary — Wire Shape Mirrors Canonical Types; the unifying
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
- Source issues (extended 2026-05-26 batch — spec layer):
  - <https://github.com/aldrin-isaac/newtron/issues/14> — full topology read
  - <https://github.com/aldrin-isaac/newtron/issues/15> — topology node CRUD
  - <https://github.com/aldrin-isaac/newtron/issues/16> — topology link CRUD
- Source issues (extended 2026-05-26 batch — device reality + apply-time surfacing):
  - <https://github.com/aldrin-isaac/newtron/issues/17> — per-Node raw CONFIG_DB snapshot read (Cluster D, new)
  - <https://github.com/aldrin-isaac/newtron/issues/19> — per-substrate-operation surfacing on write endpoints (Cluster B, sub-cluster B2)
- Closed (wontfix) from this batch:
  - <https://github.com/aldrin-isaac/newtron/issues/13> — cross-target dependency (misframed; classification lives in newtcon#22)
- Expected to close as duplicate when the doc-wide audit lands:
  - <https://github.com/aldrin-isaac/newtron/issues/18> — per-Node bulk intent records read (route documented but not registered; subsumed by `newtron#20` doc-wide audit)
