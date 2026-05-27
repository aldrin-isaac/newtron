# docs/scoping/

Scope documents for batches of newtron work that have been scoped
but not yet implemented. Each batch may be a single doc or, when
the batch is large enough, a parent doc plus per-cluster docs.

## Current batches

### newtcon-driven gaps (2026-05-26)

Ten newtron HTTP API gaps filed by newtcon under the Gap-Handling
Protocol. All ten are instances of `DESIGN_PRINCIPLES_NEWTRON.md`
§46 ("HTTP API Boundary — Wire Shape Mirrors Substrate"); they are
grouped into four clusters by shared underlying primitive.

- **[`newtcon-driven-gaps.md`](newtcon-driven-gaps.md)** — parent:
  unifying §46 principle, style invariants, cross-cluster phasing,
  status protocol, batch closure record.
- **[`projection-substrate.md`](projection-substrate.md)** —
  Cluster A: `newtron#4`, `#5`, `#6`. Share
  `ConfigDB.ExportEntries`, `DiffConfigDB`, snapshot/restore.
- **[`changeset-substrate.md`](changeset-substrate.md)** —
  Cluster B: `newtron#11`, `#12`, `#19`. Share `sonic.ConfigChange`
  and `WriteResult`-construction call sites. Organized into two
  sub-clusters: B1 (response-time exposure — `#11`, `#12`) and B2
  (apply-time surfacing — `#19`, the per-substrate-operation
  `per_write[]` + SSE streaming variant).
- **[`topology-spec-substrate.md`](topology-spec-substrate.md)** —
  Cluster C: `newtron#14`, `#15`, `#16`. Share
  `spec.Loader.SaveTopology`, `TopologySpecFile`,
  `validateTopology`.
- **[`device-reality-substrate.md`](device-reality-substrate.md)** —
  Cluster D: `newtron#17`. Wraps the existing internal
  `ConfigDBClient.GetRawOwnedTables` over HTTP as a typed
  single-snapshot read. Foundational for newtcon's
  observation-history poller (newtcon#37).

A would-be fifth cluster — cross-target reasoning (originally
filed as `newtron#13`) — was closed as wontfix after operator
review identified the framing as wrong. The substrate that would
compose the classification (per-target ChangeSets, topology, intent
records, service definitions, zone configurations) is exposed by
existing endpoints plus the Cluster A/B/C/D gaps. Fragility
classification is a newtcon-side concern tracked as
[newtcon#22](https://github.com/aldrin-isaac/newtcon/issues/22).
See the closure record at the end of `newtcon-driven-gaps.md`.

Separately tracked (not in cluster scoping; operator-side cleanup):

- [`newtron#20`](https://github.com/aldrin-isaac/newtron/issues/20) — doc-wide audit of `docs/newtron/api.md` for
  documented-but-unwired endpoints and config fields. Will close
  `newtron#18` as duplicate when the audit lands. Not a substrate
  gap; not in any cluster.

## Reading order

If you are picking up implementation work:

1. Start with `newtcon-driven-gaps.md` (the parent) for the
   unifying principle, style invariants, and the cross-cluster
   phased order. Note the phase and cluster of the issue you are
   working on.
2. Open the corresponding cluster doc for the per-issue
   implementation detail (Go types, method signatures, handler
   bodies, route registrations, tests, complexity estimate).
3. Verify the issue is still OPEN on GitHub before starting; the
   per-issue sections add a one-line "Landed in commit X, PR #N"
   note when work merges.

## Archiving

When every issue in a batch closes, the batch's docs are moved
under `docs/scoping/archive/{date}-{batch-name}/`. The archive
preserves the audit trail (operator reasoning, principle citations,
implementation hints) without cluttering the active scoping space.
