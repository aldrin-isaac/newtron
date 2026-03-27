# Implementation Conformance Protocol

## Core Rule: Never Depart From Architecture

When an architecture document exists, all implementation MUST conform to it. If
compliant code seems impossible or significantly harder than a non-compliant
alternative, that is NOT permission to deviate. The correct response:

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

## Origin

This protocol was established after a pattern of architectural drift in the Intent
DAG implementation. The CLAUDE.md Single-Owner Principle explicitly states:

> Composites (ApplyService, SetupDevice, topology provisioner) MUST call the
> owning primitives and merge their ChangeSets rather than constructing entries
> inline.

The implementation deviated: `service_gen.go` called config generators (entry
producers) instead of primitives (operation wrappers that create ChangeSets with
intent records). This bypassed the intent creation that `op()` provides. Rather
than fixing the deviation, `ensure*Intent` functions were introduced to
retroactively create the missing intent records — reimplementing what the
primitives already do, but inconsistently and incompletely.

Each `ensure*Intent` function was evidence of the same root cause. Three functions
meant three violations of the same principle. The correct fix was always: make the
composite call the primitives. The incorrect fix was: patch each missing intent
individually.

## Eight Directives

### 1. Quote Before You Code

Before writing any implementation, quote the specific CLAUDE.md or architecture
doc principles that govern it — verbatim, not summarized. This forces active
cross-referencing against the actual text rather than working from a drifting
mental model of what the architecture says.

### 2. Every New Function Must Answer: "Why Doesn't This Already Exist?"

Creating a new function or pattern is a red flag. The architecture defines the
operations and their responsibilities. If a new function is needed, either:

- **(a)** The architecture has a gap — flag it to the user before implementing
- **(b)** You are reimplementing something that already exists at a different
  layer — use the existing mechanism instead

Example: `ensure*Intent` reimplemented what `op()` already provides. The correct
question was: "primitives already create intents through `op()` — why am I
creating intents in a separate code path?"

### 3. Mandatory Hack Check On Every Implementation Decision

Before writing any code, ask:

- "Is this a hack?"
- "Is this working around a problem rather than fixing it?"
- "If I removed the upstream cause that created the need for this code, would
  this code be unnecessary?"

If the answer to any of these is yes or maybe — fix the upstream cause. Only a
hard "no, this is not a hack" is acceptable.

### 4. Test Failures Are Architecture Conformance Failures Until Proven Otherwise

When a test fails, the first diagnostic question is: **"Which architectural
principle did I violate?"** — not "What is the quickest fix?"

Example: a missing intent record during teardown → the architecture says
composites call primitives → the composite is calling config generators instead →
fix the composite. Not: add a retroactive intent creation function.

### 5. Second Instance of a Pattern = Stop and Question the Pattern

Writing the same workaround a second time is proof the workaround is wrong. The
correct response is to stop, identify why the pattern keeps being needed, and fix
the root cause.

Example: `ensureVLANIntent`, then `ensureVRFIntent`, then `ensureIPVPNIntent` —
three instances of the same pattern. Each one was an opportunity to recognize:
"this pattern exists because the creation path doesn't create intents. Fix the
creation path."

### 6. Never Take the Easier Path Over the Correct Path

If the architecture says to do X but doing Y is simpler, do X. Every shortcut
generates downstream work: patches, inconsistency audits, debugging sessions,
this protocol document.

### 7. Post-Implementation Conformance Audit

After completing an implementation, mechanically verify each relevant CLAUDE.md
principle against the actual code:

- "Composites MUST call owning primitives" → grep the composite for direct
  config generator calls → if found, non-conformant
- "Each CONFIG_DB table MUST have exactly one owner" → grep for table writes
  outside the owning file → if found, non-conformant
- "For every forward action there MUST be a reverse action" → list all forward
  actions, verify each has a reverse → if missing, non-conformant

This is literal, mechanical verification — not "does it feel right."

### 8. DO NOT SPECULATE

Never assert what code does based on assumptions, naming conventions, or mental
models. Before making any claim about what a function reads, writes, or depends
on — **read the actual code**. Every wrong answer that could have been prevented
by reading the source is a violation of this directive.

- "Zombie uses NEWTRON_INTENT" → did you read `WriteIntent`/`ReadIntent`? What key does it actually write?
- "History relies on intent records" → did you read `WriteHistory`/`ReadHistory`? What table?
- "This function calls X" → did you grep for the call site?

If you haven't verified it in source, say "I don't know, let me check" — never
state it as fact.

### 9. Drift Detection = Stop and Escalate

During implementation, continuously check: "Am I drifting from architecture?"

If yes:
- **STOP** immediately
- **Rollback** any non-conformant code
- **Describe the core issue**: what the architecture says, what you were about
  to do, why they conflict
- **Wait** for user instructions
- **Iterate** with the user to refine the architecture or find a compliant path
- **Never** resolve the dichotomy silently
