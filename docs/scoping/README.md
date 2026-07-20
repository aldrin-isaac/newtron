# docs/scoping/

Scope documents for batches of newtron work that have been scoped
but not yet implemented. Each batch may be a single doc or, when
the batch is large enough, a parent doc plus per-cluster docs.

## Active batches

None. When a batch is scoped, its parent (and any per-cluster docs)
land here until every issue closes, then move to `archive/` per the
protocol below.

## Archived batches

### newtcon-driven gaps (2026-05-26) — closed 2026-07-20

Ten newtron HTTP API gaps filed by newtcon under the Gap-Handling
Protocol, all instances of `DESIGN_PRINCIPLES_NEWTRON.md` §46 ("HTTP
API Boundary — Wire Shape Mirrors Canonical Types"), grouped into four
clusters (A: projection substrate `#4`/`#5`/`#6`; B: changeset
substrate `#11`/`#12`/`#19`; C: topology-spec substrate `#14`/`#15`/`#16`;
D: device-reality substrate `#17`). A would-be fifth cluster
(cross-target reasoning, `#13`) was closed wontfix. All ten issues are
now closed; the batch was archived with its full audit trail (operator
reasoning, §46 citations, per-issue implementation detail) to
[`archive/2026-05-26-newtcon-driven-gaps/`](archive/2026-05-26-newtcon-driven-gaps/).

## Reading order

If you are picking up implementation work on an active batch:

1. Start with the batch's parent doc for the unifying principle, style
   invariants, and the cross-cluster phased order. Note the phase and
   cluster of the issue you are working on.
2. Open the corresponding cluster doc for the per-issue implementation
   detail (Go types, method signatures, handler bodies, route
   registrations, tests, complexity estimate).
3. Verify the issue is still OPEN on GitHub before starting; the
   per-issue sections add a one-line "Landed in commit X, PR #N" note
   when work merges.

## Archiving

When every issue in a batch closes, the batch's docs are moved under
`docs/scoping/archive/{date}-{batch-name}/`. The archive preserves the
audit trail (operator reasoning, principle citations, implementation
hints) without cluttering the active scoping space.
