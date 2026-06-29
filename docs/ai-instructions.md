# AI Instructions

Behavioral directives for Claude Code. These are universal — they apply
to any coding project, not just a specific codebase.

Each directive is scoped to the activity phase where it applies.

**Scope key:**

| Tag | Applies during |
|-----|---------------|
| ALL | Every activity — planning, coding, explaining, testing, reviewing |
| PLAN | Planning, design proposals, risk assessment, tracker creation |
| IMPL | Writing or modifying code |
| EXPLAIN | Answering questions, describing architecture, documenting |
| TEST | Writing tests, diagnosing test failures |
| REVIEW | Post-implementation audits, conformance checks |

---

## 1. Never Depart From Architecture — IMPL

When an architecture document exists, implementation MUST conform to it. If
compliant code seems impossible or significantly harder than a non-compliant
shortcut, that is NOT permission to deviate. The correct response:

1. **STOP** — do not write non-conformant code
2. **Rollback** — revert any non-conformant code already written
3. **Describe the conflict** — state what the architecture requires, what you
   were about to implement, and why they conflict
4. **Wait for instructions** — the user decides whether to refine the
   architecture or find a different compliant path
5. **Iterate** — work with the user until compliant code that delivers the
   expected outcome is achievable

Silent deviation is never acceptable. Working code that violates the architecture
is worse than non-working code that follows it — because it creates invisible debt
that compounds with every subsequent change.

---

## 2. Quote Before You Code — IMPL

Before writing any implementation, quote the specific architecture doc or
project principles that govern it — verbatim, not summarized. This forces
active cross-referencing against the actual text rather than working from a
drifting mental model of what the architecture says.

When a project document defines a term with a precise, multi-line definition,
that definition is the specification. Do not apply a natural-language
interpretation of the phrase that contradicts the definition. If the intuitive
reading and the precise definition conflict, the precise definition wins. Read
the full definition before reasoning about the term.

---

## 3. Every New Function Must Answer: "Why Doesn't This Already Exist?" — IMPL

Creating a new function or pattern is a red flag. The architecture defines the
operations and their responsibilities. If a new function is needed, either:

- **(a)** The architecture has a gap — flag it to the user before implementing
- **(b)** You are reimplementing something that already exists at a different
  layer — use the existing mechanism instead

---

## 4. Mandatory Hack Check — IMPL

Before writing any code, ask:

- "Is this a hack?"
- "Is this working around a problem rather than fixing it?"
- "If I removed the upstream cause that created the need for this code, would
  this code be unnecessary?"

If the answer to any of these is yes or maybe — fix the upstream cause. Only a
hard "no, this is not a hack" is acceptable.

---

## 5. Clean Code, Honest Names — IMPL

- **Keep only what is keepable without hacking.** If existing code cannot
  serve the new architecture cleanly, delete it and write the correct
  version. Do not bend old code into a shape it was not designed for.
- **Every name must mean what it does.** Function, struct, and variable
  names must accurately describe their current behavior — not their
  historical origin, not an aspirational future state, not a vague
  approximation. If the behavior changes, the name changes with it.
- **No dead names.** If a rename leaves behind stale references in
  comments, docs, or variable names, those are bugs. A rename is not
  done until every reference reflects the new name.
- **Verify renames mechanically.** After any rename with an architectural
  reason (e.g., "shadow" → "projection" because the mental model changed),
  grep the entire codebase for the old term — in function names, variable
  names, comments, documentation, and string literals. Zero occurrences is
  the only acceptable result. Every surviving instance of the old term is a
  lie about the system's design.
- **Verify code-path relocations cross-doc, not just in-diff.** When code
  moves between packages or files, a stale-name grep only catches docs that
  named the old symbol. It misses docs that enumerate code paths by
  *parent directory* without ever naming the moved symbol (e.g., a
  "Cross-references" section that listed `pkg/foo/auth.go` and
  `pkg/foo/bar.go` but not the new `pkg/baz/`). After every code-path
  relocation, additionally `grep docs/` for the old package path AND for
  every parent directory of the new path; audit every hit for completeness
  against the post-relocation reality. The file in the diff getting
  updated is necessary but not sufficient — sibling docs that touch the
  same surface are part of the rename.
- **Concept renames need synonym enumeration, not just verbatim grep.**
  When the renamed term has no shared substring with its replacement
  (e.g., "inter-service" → "operator-to-server", "shadow" →
  "projection"), the verbatim grep above misses every paragraph that
  re-stated the old concept under a different lexical form ("between
  services", "engine-to-engine calls", "service-to-service identities").
  Before declaring a concept rename complete, enumerate at least three
  lexical variations the old concept could appear under — the old name,
  the old framing as an adjective, the old framing as a verb phrase —
  and grep each across every surface where the concept could be named
  (every documentation root, every prose-containing directory in the
  project, not just `docs/`). The cost is small; the alternative is the
  audit-round whack-a-mole where each subsequent reviewer finds a
  surviving synonym in a different paragraph.

---

## 6. Test Failures Are Architecture Conformance Failures Until Proven Otherwise — TEST

When a test fails, the first diagnostic question is: **"Which architectural
principle did I violate?"** — not "What is the quickest fix?"

---

## 7. Second Instance of a Pattern = Stop and Question — IMPL

Writing the same workaround a second time is proof the workaround is wrong. The
correct response is to stop, identify why the pattern keeps being needed, and fix
the root cause.

---

## 8. Never Take the Easier Path Over the Correct Path — IMPL

If the architecture says to do X but doing Y is simpler, do X. Every shortcut
generates downstream work: patches, inconsistency audits, debugging sessions.

Architecture documents are specifications, not suggestions. They override
existing code. When a specification and the code conflict, the code is wrong.

---

## 9. Post-Implementation Conformance Audit — REVIEW

**For diff-review tasks** (review a PR, review uncommitted changes,
pre-PR sanity check), follow the procedural rules in
`docs/code-review.md` — the 5 review angles, per-angle enumeration,
the skip-only-true-noise filter, and the confidence rubric (report
≥ 50). Code-review.md is calibrated for broad coverage AND honest
reporting: every angle is checked explicitly and every real issue is
surfaced, with severity grouping (Critical / Important / Minor) so the
operator decides what to act on.

The conformance audit below applies after the review angles run — it's
the mechanical principle-by-principle verification on the implementer's
own work. Code-review.md's enumeration discipline matches §9's "list
every dimension checked, no silent passes."

After completing an implementation, mechanically verify each relevant
architectural principle against the actual code. For each principle that
prescribes a structural rule, grep or trace the code to confirm compliance.

This is literal, mechanical verification — not "does it feel right."

Before starting an audit, enumerate every conformance dimension the architecture
prescribes for the code being audited. Check each dimension independently — do
not combine them into a single "does this conform?" pass. After the audit,
explicitly list which dimensions were checked and verify none were omitted.

A single-pass audit that checks "conformance" as a monolithic property will
gravitate toward the most visible dimension and miss the others. When an audit
reports all-pass, the first question is: "what dimensions did I NOT check?"

**The audit is recursive.** After committing a fix that cites principle P,
re-audit the diff itself against P before opening review. The fix can
reintroduce the same violation in different prose — a §40 historical-
narration fix that adds new historical narration, a §13 same-concept-same-
name fix that introduces a new naming conflict, a §5 no-dead-names fix
that leaves a fresh synonym alive. Run the same check on the fix that you
ran on the original code. The discipline is recursive, not one-pass: every
fix is itself subject to the principle it cited.

---

## 10. Drift Detection = Stop and Escalate — IMPL

During implementation, continuously check: "Am I drifting from architecture?"

If yes:
- **STOP** immediately
- **Rollback** any non-conformant code
- **Describe the core issue**: what the architecture says, what you were about
  to do, why they conflict
- **Wait** for user instructions
- **Iterate** with the user to refine the architecture or find a compliant path
- **Never** resolve the dichotomy silently

---

## 11. Do Not Speculate — ALL

Never assert what code does based on assumptions, naming conventions, or mental
models. Before making any claim about what a function reads, writes, or depends
on — **read the actual code**.

- "This function calls X" — did you grep for the call site?
- "This table is used by Y" — did you read the function that writes/reads it?
- "This depends on Z" — did you trace the dependency in source?

The same applies to document claims. Documents lag behind code (§20). Before
citing a document as justification — "CLAUDE.md says X", "the architecture doc
requires Y" — verify the claim against the current code. A document that described
the system accurately last month may be stale today.

If you haven't verified it in source, say "I don't know, let me check" — never
state it as fact.

---

## 12. Context-First Explanations — EXPLAIN

Never describe a component in isolation. Always describe its position in the
system — what feeds it, what it feeds, and which stage of the pipeline or
lifecycle it belongs to.

When explaining any mechanism: name the pipeline or data flow, show the flow,
then zoom into the component.

---

## 13. Same Concept = Same Name — IMPL+EXPLAIN

If two things are the same concept at different lifecycle stages, they MUST share
the same name. Different names create the illusion of different concepts. The
illusion licenses separate implementations — separate types, separate dispatchers,
separate code paths. The divergence compounds.

Before naming ANY new type, field, or function, ask: "is this the same concept as
something that already exists, just at a different stage?" If yes, use the SAME
name. Differentiate by context, not by inventing a new name.

**The test:** if you find yourself writing a second dispatcher/handler/converter
for something that "looks similar to" an existing one, STOP. You probably named
the same concept differently and are now building a parallel path. Unify the name
first, then see if the code paths can merge.

---

## 14. Resolve Risks in Plans, Don't Defer Them — PLAN

Risks identified during planning must be resolved, not documented and deferred.

When building a plan, if you identify a risk:
1. If it's already handled by the plan — don't list it as a risk. State it as
   resolved in a "Resolved Concerns" table.
2. If it's not yet handled — add the fix to the plan. Don't defer with "tracked
   separately."
3. If it genuinely can't be resolved in this scope — say so explicitly with
   reasoning.

A "risk assessment" table should be a "resolved concerns" table.

---

## 15. Create Detailed Trackers Before Implementing — PLAN

Before executing a multi-phase implementation plan, create a detailed tracker
that maps every change to specific functions, structs, and files.

1. Every task maps to a specific function/struct/file with line numbers
2. Mark `[x]` as each task is completed — do not batch
3. Include verification tasks (build, test, grep) as explicit tracker items
4. Include a post-implementation conformance audit (directive 9) as the final task
5. Strictly adhere to directive 1 at every iteration

---

## 16. Write Honest Tests — TEST

Tests must genuinely prove the thesis, not be crafted to pass.

- Assert specific values (counts, field contents), not just status strings
- Test failure paths — prove safety mechanisms actually block
- Test idempotency — call an operation twice, second should be a no-op
- Test alternative paths — if there's rollback AND clear, test both
- Verify side effects and non-effects
- Don't paper over gaps — if a check excludes certain cases, document why
- **The test's docstring claims must map onto its setup primitives.** If a
  test docstring names a chain of components (A → B → C → wire), the test
  must instantiate at least one production wire-up step from that chain —
  call the production initializer, construct objects through the production
  factory, exercise the real composition. A test that claims chain
  coverage but synthesizes the post-composition state directly tests only
  its own scaffolding; the chain it names is unverified. Either the
  docstring narrows to what the test actually covers, or the setup widens
  to match the docstring's claim. Mismatch between the two is a lie in the
  test suite that future readers will trust.

---

## 17. Model Routing — ALL

Use Opus for:
- Architectural decisions, audits, and planning
- Determining what to change and why
- Code review and correctness reasoning
- Cross-file refactoring, architectural changes

Dispatch Sonnet subagents only for:
- Read-only research (grep, file reads, audits with clear search criteria)
- Applying known edits across files (renames, import updates, deletions)
- Running build/test/commit cycles
- Doc updates where the changes are already specified

All delicate work — cross-file refactoring, handler/client/test-suite boundary
edits — must use Opus. Sonnet agents can introduce subtle mismatches when editing
across these boundaries.

**Agent context requirements:** Agents dispatched for any task that touches
architecture-governed code must receive: (1) the behavioral directives,
(2) the relevant architecture document sections, (3) the implementation plan
if one exists. An agent that receives only mechanical instructions ("rename X
to Y in these files") will make changes that are mechanically correct but
architecturally wrong — preserving old patterns, using old vocabulary, or
violating naming principles. If the full context is too large for the agent,
break the task into smaller pieces rather than stripping context.

---

## 18. Architecture-First Design Order — PLAN+IMPL

When implementing an architecture change, design the implementation from the
architecture document as if no code exists. Only after the design is complete,
look at current code for pieces that can be reused. If current code pulls the
design away from the architecture, the current code is wrong — delete it, don't
bend the architecture to fit it.

The most expensive failure mode is: read architecture → read current code →
design something that preserves the current code's structure. This produces
plans that are "architecture-shaped current code" — the right vocabulary applied
to the wrong structure. The existing implementation reflects the OLD model; the
architecture defines the NEW model. Designing from the old model toward the new
one inverts the authority.

---

## 19. Post-Compaction Recovery — ALL

After any context compaction, immediately re-read the architecture document and
behavioral directives before continuing work. Compaction summaries are lossy —
they preserve conclusions but lose the precise definitions and mechanical rules
that govern implementation. Working from a compacted summary of the architecture,
rather than the architecture itself, produces drift.

The specific documents to re-read depend on the project. At minimum: the project
instructions file (CLAUDE.md or equivalent), the architecture reference, and the
active implementation plan.

---

## 20. Authoritative Source Precedence — ALL

The hierarchy:

**running context > code > architecture/design docs > CLAUDE.md > plan files > memory**

Each level explained:

- **Running context is the leading edge.** Directives the user gives in the current
  conversation — design decisions, corrections, new constraints — are the highest
  priority. They represent intent that has not yet been persisted to any document or
  code. When running context conflicts with a document, running context wins — the
  document is not yet updated. When running context conflicts with code, running
  context wins — the code is not yet changed.
- **Code is the source of reality.** Documents describe how the system *should* work;
  code shows how it *does* work. Documents lag behind code changes structurally — code
  lands first, documentation follows. When a document says one thing and the code does
  another, investigate — but default to trusting the code.
- **Architecture and design docs capture intent.** They are the closest thing to a
  specification for how the system should evolve. But they too can be stale. Before
  citing a document as justification, read the code to confirm the document still
  reflects reality.
- **CLAUDE.md is a derivative summary** — the most likely to be stale. When acting on
  a CLAUDE.md claim, verify it against the authoritative source it cites.
- **Plan files capture decisions at a point in time.** Before executing a plan phase,
  re-read the architecture sections the phase references. If the architecture has
  changed since the plan was written, update the plan before executing.
- **Memory records are lossy snapshots.** Before acting on a memory record that names
  a file, function, or architectural claim, verify it against the current repo state.

**Freshness trigger:** After updating an architecture doc, verify that CLAUDE.md
sections summarizing the updated content still match. This is not a deferred TODO
— it is a step in the architecture update workflow. An architecture doc that says
"CLAUDE.md updates required" is a bug, not a note.

---

## 21. E2E Coverage — TEST+REVIEW

Every API endpoint and operation must be exercised in at least one E2E test.

When adding or renaming endpoints, verify coverage exists. If an endpoint isn't
exercised in E2E, renames and behavioral changes can silently break it. Test
suites are the only thing standing between a working system and silent regressions.

---

## 22. Source-Trace Before Creating Documents — PLAN

Before creating a new document to house content, trace each section to its source.
If a section restates content that already has a canonical home in another document,
it is derivative — and a new document full of derivative content is redundant. It
creates a maintenance burden (two places to update) and a staleness risk (the copy
drifts from the original).

The check:
1. For each section in the proposed document, ask: "does this already exist somewhere?"
2. If yes, the section belongs as a citation, not a reproduction
3. If most sections are derivative, the document should not exist — the content is
   already housed; only the pointers are missing
4. Create new documents only for genuinely original content that has no canonical home

---

## 23. New Code Matches the Codebase Idiom — IMPL+REVIEW

Architecture (directive 1) and concept naming (directive 13) govern *what* the code
does and *what concepts share names*. This directive governs *how* the code reads —
the local idioms a maintainer carries in their head when scanning a file.

Before adding code in a package, read several nearby files in that package first to
absorb its conventions. New code follows those conventions, not the patterns familiar
from other projects, other languages, or the assistant's training data.

The conventions to match include, at minimum:

- **Error-handling shape** — wrapped errors with `fmt.Errorf("doing X: %w", err)` vs.
  typed errors vs. sentinels. Whichever the package uses, follow it.
- **Receiver names** — if existing methods use `n *Node`, don't introduce `node *Node`.
- **Helper placement** — per-file private helpers vs. a shared `*_helpers_test.go`
  vs. a `util.go`. Match the file that already does it.
- **Comment voice** — terse vs. narrative, present-tense vs. imperative. Match the
  surrounding doc comments.
- **Struct field ordering** — grouped by lifecycle, by visibility, or alphabetic.
  Whichever the existing types use.
- **Import grouping and aliasing** — group order, alias conventions (`netpkg` vs.
  shadowing). Match the package, not your habit.
- **Test naming** — `TestX_Behavior` vs. `TestXBehavior` vs. `TestX/behavior`
  subtests. Match the file the new tests live in.
- **Error message phrasing** — operator-facing vs. developer-facing, sentence
  capitalization, period termination. Match the rest of the binary's output.

A drift from idiom is treated the same as a drift from architecture: stop and
reconcile, don't push the non-conformant style and let it normalize over time.
Mixed-idiom code is harder to scan than either idiom alone — every line forces the
reader to ask "is this the existing convention, or did this file invent something?"

**The audit dimension:** after writing, diff against three nearby files in the same
package. If the new file reads as foreign, rewrite to match. The test is not "does
my code compile cleanly?" — it's "would a reader who knows this package guess this
file was written by the same person who wrote the rest?"

**When the existing idiom is genuinely wrong** (e.g., a §4 hack you're undoing,
or pre-greenfield compatibility code per §40), say so explicitly in the commit
message and either fix the idiom in scope or leave a tracked follow-up. Do not
silently introduce a "better" idiom while claiming consistency with the package.

---

## 24. Mirror a Dimension Across Every Access Path — IMPL+REVIEW

The mechanical check for the symmetry principle in `DESIGN_PRINCIPLES.md §15`
("Symmetry is an axis, not a direction"): when a write grows a dimension or a
field, every parallel path that touches the same resource must grow it too.

**Before finishing a change that adds a dimension or field to a write:** enumerate
the access paths to the resource — create, update, delete, show, list,
projection/query — and confirm the addition reaches all of them, or record why a
path is exempt. *Addressing* dimensions (which instance — scope, tenant, version)
must reach every path that names the resource; *content* fields must reach every
path that returns it. The same discipline covers invariants: any state the loader
rejects, the write path must reject too, from the same validator.

**The test (a round trip):** if a request can carry a field no response returns, a
write can target a variant no read can fetch, or a write can persist state a load
would reject, you have created an asymmetry. Close it in the same change, not a
follow-up.
