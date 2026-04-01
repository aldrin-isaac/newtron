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

Project instruction files (CLAUDE.md), plan files, and memory records are
derivatives — they summarize or reference authoritative documents (architecture
docs, design principles, code). When a derivative is automatically loaded into
context and the authoritative source requires a tool call to read, the
derivative wins by default. This is a bug, not a feature.

Rules:
- **Architecture docs > CLAUDE.md summaries.** CLAUDE.md may summarize
  architectural principles for quick reference. When acting on such a summary,
  verify it against the authoritative source before proceeding. If they
  conflict, the authoritative source is correct and CLAUDE.md is stale.
- **Current code > plan file assumptions.** Plan files capture design decisions
  at a point in time. Before executing a plan phase, re-read the architecture
  sections the phase references. If the architecture has changed since the plan
  was written, update the plan before executing.
- **Repo documents > memory records.** Memory records are lossy snapshots.
  Before acting on a memory record that names a file, function, or
  architectural claim, verify it against the current repo state.

The hierarchy: **authoritative repo documents > CLAUDE.md > plan files > memory**.
When in doubt, read the source.

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
