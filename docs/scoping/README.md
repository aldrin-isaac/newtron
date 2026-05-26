# docs/scoping/

Scope documents for batches of newtron work that have been scoped
but not yet implemented. Each batch may be a single doc or, when
the batch is large enough, a parent doc plus per-cluster docs.

## Current batches

### newtcon-driven gaps (2026-05-26)

Nine newtron HTTP API gaps filed by newtcon under the Gap-Handling
Protocol. All nine are instances of `DESIGN_PRINCIPLES_NEWTRON.md`
§46 ("HTTP API Boundary — Wire Shape Mirrors Substrate"); they are
grouped into four clusters by shared underlying primitive.

- **[`newtcon-driven-gaps.md`](newtcon-driven-gaps.md)** — parent:
  unifying §46 principle, style invariants, cross-cluster phasing,
  status protocol, Cluster D appendix (`newtron#13`).
- **[`projection-substrate.md`](projection-substrate.md)** —
  Cluster A: `newtron#4`, `#5`, `#6`. Share
  `ConfigDB.ExportEntries`, `DiffConfigDB`, snapshot/restore.
- **[`changeset-substrate.md`](changeset-substrate.md)** —
  Cluster B: `newtron#11`, `#12`. Share `sonic.ConfigChange` and
  `WriteResult`-construction call sites.
- **[`topology-spec-substrate.md`](topology-spec-substrate.md)** —
  Cluster C: `newtron#14`, `#15`, `#16`. Share
  `spec.Loader.SaveTopology`, `TopologySpecFile`,
  `validateTopology`.

The Cluster D issue (`newtron#13`, cross-target dependency
exposure) is a single-issue cluster and lives as an appendix at the
end of `newtcon-driven-gaps.md` rather than in its own cluster
doc. If a second cross-target gap arrives, split out as
`cross-target-reasoning.md` at that time.

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
