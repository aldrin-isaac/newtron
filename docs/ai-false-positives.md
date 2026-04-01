# AI False Positives and Failure Modes

A catalog of mistakes Claude made during the newtron project — failures
flagged as "not an issue," failures to comply with explicit instructions,
and failures to follow design and architecture documents. Each entry records
the assumption that led to the failure and the mitigation that prevents
recurrence.

Date: 2026-03-29

---

## Failure Taxonomy

Failures fall into five categories. Each has a root cause pattern and a
structural mitigation. The categories are ordered by how much rework they
caused.

| # | Category | Root cause | Rework cost |
|---|----------|-----------|-------------|
| 1 | **Architecture Inversion** | Preserving existing code patterns when the architecture demands something different | Days |
| 2 | **Conceptual Misreading** | Interpreting a design principle to mean the opposite of what it says | Days |
| 3 | **Audit Blindness** | Checking one dimension of conformance while missing others | Hours |
| 4 | **Context Drift** | Losing architectural context after compaction or across agent boundaries | Hours |
| 5 | **Naming Dishonesty** | Keeping old names or dead code instead of writing what the architecture requires | Hours |
| 6 | **Stale Derivative Authority** | Following CLAUDE.md or plan files that have drifted from the authoritative repo documents they summarize | Hours–Days |

---

## Category 1: Architecture Inversion

The most expensive failure mode. Claude reads the architecture document,
then designs an implementation that preserves the current code's structure
instead of implementing what the architecture says. The architecture gets
bent to fit the code, not the other way around.

### 1.1 Provisioning and Reconciliation Are the Same Operation

**What happened**: Claude treated provisioning (topology.json to device) and
reconciliation (fix drift) as separate operations requiring separate code
paths — separate handlers, separate pipelines, separate delivery mechanisms.
The architecture states they are the same operation at different lifecycle
stages, sharing one pipeline: Intent → Replay → Render → Deliver.

**Assumption**: Existing code had separate `ProvisionDevice` and
`DetectDrift` + manual reconciliation paths. Claude assumed the new
architecture would preserve these as distinct operations and designed the
implementation around two separate pipelines.

**Cost**: 3 days of rework.

**Mitigation**: When the architecture says two things are the same, they are
the same. Do not create separate code paths for lifecycle stages of a single
operation. Quote the architecture section before designing. If the current
code has two paths where the architecture says one, delete both and write
the one.

### 1.2 CompositeConfig as Serialization Layer

**What happened**: Claude designed delivery around `CompositeConfig` /
`CompositeBuilder` — objects that serialize the abstract node's configDB
into an intermediate representation, then deliver that. The architecture
says the abstract node IS the intermediary — delivery reads the node's
configDB directly via `ExportEntries()`.

**Assumption**: The existing `BuildComposite` → `Deliver` pattern was
assumed to be preserved in the new architecture. Claude treated the abstract
node as a builder that produces a composite, rather than as the single
representation that the delivery layer reads directly.

**Cost**: Full redesign of the delivery path.

**Mitigation**: The abstract node is the single intermediary between intent
and device. Any pattern that serializes it into a degraded copy is a
violation. When designing delivery, the question is always: "does this read
the node directly, or does it copy the node's state into something else?"

### 1.3 Implementation Plans That Preserve Current Code Structure

**What happened**: Multiple implementation plans were designed by first
reading the current code, then mapping architecture concepts onto existing
functions and files. This produced plans that were "architecture-shaped
current code" — the right vocabulary applied to the wrong structure.

**Assumption**: The current implementation is a scaffold that the new
architecture builds on. In reality, the current implementation reflects
the OLD model. The new architecture requires starting from its own first
principles.

**Cost**: Plans had to be redesigned from scratch.

**Mitigation**: When designing implementation for an architecture change:
1. Start from the architecture doc
2. Design as if writing from scratch
3. Only then look at current code for reusable pieces
4. If current code pulls the design away from the architecture, delete it

Never start from "what does the current code do" when the architecture
says "here is what the code should do."

---

## Category 2: Conceptual Misreading

Claude reads a design principle and applies it in a way that contradicts
its intent. The words are understood; the meaning is inverted.

### 2.1 "Device Is Source of Reality" Interpreted as "Accommodate Drift"

**What happened**: Claude interpreted "device is source of reality" to mean
newtron should read actual CONFIG_DB state and operate on top of whatever
exists — including drift. The principle actually means: the device's own
NEWTRON_INTENT records are the authoritative state. External CONFIG_DB edits
are drift from what the intents declare. Newtron detects drift and either
reconciles back to intent or halts. It never accommodates drift.

**Assumption**: "Reality" means "whatever CONFIG_DB currently contains."
The correct reading: "reality" means "the device's own intent records,
which define what should be configured."

**Cost**: Days of design work that had to be reversed.

**Mitigation**: When a principle has a precise definition in the codebase
(CLAUDE.md, architecture doc), use that definition — not a natural-language
interpretation. "Device is source of reality" has a 15-line definition in
CLAUDE.md. Read it. If the definition and the intuitive reading conflict,
the definition wins.

### 2.2 Platform Patching Principle — Inventing Instead of Fixing

**What happened**: When frrcfgd's `vrf_handler` was found to be broken
(not programming VNI into zebra), Claude designed a custom `NEWTRON_VNI`
CONFIG_DB table and a scanner that reads it — inventing a parallel mechanism
instead of patching the broken standard one. The Platform Patching Principle
says: you MAY patch bugs (make the standard path work), you may NOT reinvent
features (create a parallel mechanism).

**Assumption**: Since the standard mechanism is broken, a custom mechanism
is needed. The correct approach: poll the same standard signal (`VRF` table)
and perform the same intended action (`vtysh vrf X; vni N`) — same
mechanism, polling instead of pub/sub.

**Cost**: The custom table was built, tested, and then deleted when the
principle was enforced.

**Mitigation**: Before designing any workaround:
1. What is the standard SONiC mechanism?
2. What specific bug prevents it from working?
3. Can we make the standard mechanism work (poll, retry, fix the daemon)?
4. Only if all standard-mechanism approaches fail: escalate, don't invent.

---

## Category 3: Audit Blindness

Claude runs an audit against architectural requirements but checks only
one dimension of conformance, missing equally important dimensions that
the architecture specifies.

### 3.1 Function Audit: 12 Functions Wrongly Marked PASS

**What happened**: The function audit (2026-03-29) dispatched 4 agents to
verify 299 functions against the unified pipeline architecture. The agents
checked whether functions read the intent DB vs the projection (the Phase
0-7 work). They did not check:

- **Config method contract** (§2): "by return, both the intent DB and the
  projection are updated" — 6 config methods were missing `render(cs)` calls
- **Reconstruction param completeness** (§4): "every param that affects
  CONFIG_DB output must be stored in the intent record" — 1 inline
  `writeIntent` call was missing 5 required params

The audit marked all 12 affected functions as PASS. A second-pass audit
using 3 targeted agents (one per dimension) found all 7 violations.

**Functions wrongly marked PASS**:

| Function | File | What was missed |
|----------|------|----------------|
| `DeleteVRF` | vrf_ops.go | Missing `render(cs)` — projection retained deleted entries |
| `UnbindIPVPN` | vrf_ops.go | Missing `render(cs)` — projection retained IP-VPN entries |
| `AddBGPPeer` | interface_bgp_ops.go | Missing `render(cs)` — BGP entries never in projection |
| `TeardownVTEP` | evpn_ops.go | Missing `render(cs)` |
| `RemoveBGPGlobals` | bgp_ops.go | Missing `render(cs)` |
| `RemoveIP` | interface_ops.go | Missing `render(cs)` |
| `generateServiceEntries` (ingress ACL) | service_ops.go | Intent stored `{rules}` only; reconstruction needs `name`, `type`, `stage`, `ports`, `description` |
| `generateServiceEntries` (egress ACL) | service_ops.go | Same as above |

**Assumption**: If a function reads intents instead of the projection, it
conforms to the architecture. This is one of three requirements. The
architecture specifies three contracts: (1) intent reads, (2) render called,
(3) reconstruction completeness. Checking only one is a 33% audit.

**Severity**: 3 of the 7 bugs were HIGH severity (active code paths called
from API/CLI). The incomplete ACL intent would break reconstruction for
any device with service-generated ACLs.

**Mitigation**: Every architecture audit must explicitly enumerate ALL
dimensions being checked before starting. For the unified pipeline:
1. Operational reads use intent DB, not projection
2. Every config method calls `render(cs)` before return
3. Every `writeIntent` stores all params needed for reconstruction
4. Every `ReplayStep` case passes stored params back to the method

Dispatch one agent per dimension, not one agent for all dimensions. A
single agent checking "does this conform?" gravitates toward the most
visible dimension and misses the others.

---

## Category 4: Context Drift

Claude loses architectural context — either through context window
compaction or across agent dispatch boundaries — and reverts to
pattern-matching against the code it can see.

### 4.1 Post-Compaction Drift

**What happened**: After context compaction events, Claude continued
working but with degraded understanding of the architecture. Work drifted
from the architecture and violated directives that were explicit in the
now-compacted context. Renames were incomplete, names didn't match the
architecture's vocabulary, and implementation choices contradicted sections
of the architecture doc that were no longer in context.

**Assumption**: The compaction summary preserves sufficient architectural
context. It does not — summaries lose the precise definitions and
mechanical rules that govern implementation.

**Mitigation**: After any compaction, immediately re-read:
1. `docs/ai-instructions.md`
2. `docs/newtron/unified-pipeline-architecture.md`
3. The active implementation plan

This is now a standing directive in CLAUDE.md feedback memory.

### 4.2 Agents Without Architectural Context

**What happened**: Agents dispatched to make changes received only
mechanical specifications ("rename X to Y in these files") without the
architecture doc or ai-instructions. The agents made substitutions that
preserved old patterns — using old vocabulary, keeping dead names, not
applying the naming principles from ai-instructions §5.

**Assumption**: Mechanical tasks don't need architectural context. They
do — even a rename requires understanding WHY the name is changing to
ensure the new name is correct, not just different.

**Mitigation**: Every dispatched agent must receive:
1. The behavioral directives (`ai-instructions.md`)
2. The authoritative architecture doc
3. The implementation plan/tracker

If an agent can't receive these (context too large), the task should be
broken into smaller pieces, not stripped of context.

---

## Category 5: Naming Dishonesty

Claude preserves existing names, dead code, or compatibility shims instead
of writing what the architecture requires. The result compiles but
misrepresents what the code does.

### 5.1 Dead Names and Compatibility Shims

**What happened**: During refactors, Claude kept old function names with
redirects to new implementations, added `_old` suffixes, or left dead code
with `// removed` comments. The greenfield principle (CLAUDE.md) says:
delete, don't deprecate. If something is unused, remove it completely.

**Assumption**: Preserving old names avoids breaking things. In a greenfield
project with no installed base, there is nothing to break. Old names are
lies about what the code does.

**Mitigation**: CLAUDE.md's greenfield principle: "No compatibility shims.
When a format or API changes, change it everywhere in one commit. No
dual-format detection, no deprecated aliases, no `_old` renames."

### 5.2 "Shadow" Terminology After Architecture Rename

**What happened**: The architecture renamed `applyShadow` → `render` and
`shadow configDB` → `projection` to reflect the intent-first model (intents
are primary; CONFIG_DB is derived, not shadowed). Claude made the function
renames but left "shadow" in comments, variable names, and documentation
prose — defeating the purpose of the rename.

**Assumption**: The rename is about function names only. It is about the
mental model. "Shadow" implies the device is primary and in-memory state
is secondary. "Projection" reflects that intents are primary and CONFIG_DB
is derived. Every occurrence of "shadow" in the intent-first context is a
lie about the data flow direction.

**Mitigation**: When an architecture rename has a stated reason ("reflects
that intents are primary"), the reason applies everywhere the old term
appears — not just in function signatures. Grep for the old term after
renaming and eliminate every occurrence.

---

## Category 6: Stale Derivative Authority

Claude prioritizes sources that are automatically loaded into context —
CLAUDE.md, plan files, memory files — over authoritative documents in the
repo that must be explicitly read. When a derivative source (CLAUDE.md
summary, plan file assumption, memory record) has drifted from the
authoritative source it was derived from (architecture doc, design
principles, actual code), Claude follows the stale derivative because it's
already in context. The authoritative source requires a tool call to access,
so it loses by default.

### 6.1 CLAUDE.md Summaries Drift from Architecture Docs

**What happened**: CLAUDE.md contains summaries of architectural principles
from `docs/newtron/unified-pipeline-architecture.md` and
`docs/DESIGN_PRINCIPLES_NEWTRON.md`. When the architecture doc was updated
(e.g., during the intent-first redesign), the CLAUDE.md summary was not
always updated in the same commit. Claude read the stale CLAUDE.md summary
(automatically loaded) and followed it, even when it contradicted the
current architecture doc (which required an explicit Read to access).

**Assumption**: What's in context is current. CLAUDE.md is always loaded,
so its statements are trusted as authoritative. But CLAUDE.md is a
derivative — it summarizes the architecture doc. The architecture doc is
the authority. When they diverge, the architecture doc is right and
CLAUDE.md is stale.

**Cost**: Implementation decisions based on outdated architectural
understanding. Ranges from hours (if caught during review) to days (if
the stale assumption cascades through a multi-phase plan).

**Mitigation**: When CLAUDE.md or a plan file makes a claim about the
architecture, verify it against the authoritative source before acting on
it. CLAUDE.md is an index and a set of project-specific rules — not a
substitute for reading the architecture doc. Plan files capture design
decisions at a point in time — they may predate architecture revisions.

### 6.2 Plan Files Retain Assumptions from Before Architecture Changes

**What happened**: Plan files (in `.claude/plans/`) are written during the
planning phase and reference architecture sections as they existed at
planning time. When the architecture is revised between planning and
execution — or between execution phases — the plan file retains the old
assumptions. Claude follows the plan file (loaded automatically) rather
than re-reading the architecture doc to check whether the plan's
assumptions still hold.

**Assumption**: The plan is derived from the architecture, so following the
plan is following the architecture. This is true at writing time but false
after the architecture is revised. Plans are snapshots; the architecture
is the living document.

**Mitigation**: Before executing any plan phase, re-read the architecture
sections the phase references. If the architecture has changed, update the
plan before executing. Never execute a plan step whose architectural
assumptions haven't been verified against the current source.

### 6.3 CLAUDE.md Missing Three Architecture Mechanisms (Live Example)

**What happened**: During the ai-false-positives exercise itself (2026-03-29),
Claude was asked whether CLAUDE.md's rules would cause architecture violations.
Cross-checking against the authoritative architecture doc revealed three
mechanisms described in the architecture doc (§8) that CLAUDE.md's "Abstract
Node" section did not mention:

1. **`RebuildProjection(ctx)`** — re-reads intents from device, rebuilds
   projection from scratch before every operation. CLAUDE.md described
   projection freshness as only coming from `render(cs)` within config
   methods. Following CLAUDE.md would produce stale projections between
   operations.
2. **`Execute(ctx, opts, fn)`** — Lock → snapshot → fn → commit-or-restore
   → Unlock. Enables dry-run support. CLAUDE.md didn't describe this
   pattern at all.
3. **`InitFromDeviceIntent()`** — the actuated mode constructor. CLAUDE.md
   named `NewAbstract()` for topology mode but omitted the actuated
   counterpart.

**Assumption**: CLAUDE.md's "Abstract Node" section was complete. It was
not — the architecture doc had been updated with `RebuildProjection` and
`Execute` but CLAUDE.md was not updated in the same pass.

**Cost**: Would have caused incorrect implementation of the actor layer —
missing projection freshness, missing dry-run, incomplete constructor model.
Caught before implementation only because the user explicitly asked for the
cross-check.

**Root cause**: No process ensures CLAUDE.md is updated when the architecture
doc changes. The architecture doc's §12 even flagged "CLAUDE.md updates
required" — but no one acted on it until the explicit cross-check.

**Mitigation**: After any architecture doc update, run a CLAUDE.md freshness
check: read CLAUDE.md sections that summarize the updated architecture and
verify they still match. This must be a step in the architecture update
workflow, not a deferred TODO.

---

## Meta-Pattern: Why These Failures Recur

Every failure in this document shares one root cause: **Claude optimizes
for preserving what exists over implementing what is specified.** This
manifests as:

- Reading architecture, then looking at code, then designing something
  that keeps the code's structure (Category 1)
- Interpreting principles in the way that requires fewer code changes
  (Category 2)
- Auditing the dimension that the current code already satisfies
  (Category 3)
- Losing the specification and falling back to pattern-matching against
  visible code (Category 4)
- Keeping old names to avoid touching more files (Category 5)
- Trusting a stale summary already in context over the authoritative
  source that requires a tool call to read (Category 6)

The universal mitigation: **architecture documents are specifications,
not suggestions.** They override existing code. When a specification and
the code conflict, the code is wrong.

---

## How to Use Claude to Prevent These Failures

### Before starting work

1. **Point Claude at the architecture doc explicitly.** "Read
   unified-pipeline-architecture.md §N before implementing." Without
   this, Claude will read the code and infer the architecture — and the
   inference will match the OLD architecture.

2. **State which dimensions to check.** "Verify: (a) intent reads,
   (b) render called, (c) reconstruction completeness." Without explicit
   dimensions, Claude checks the most obvious one and stops.

3. **Require architecture quotes.** "Quote the section that governs this
   change before writing code." This forces Claude to find and read the
   relevant specification before implementing.

### During work

4. **After compaction, demand re-reads.** "Re-read ai-instructions.md
   and the architecture doc before continuing." Compaction summaries are
   lossy; the precise definitions that govern implementation are the
   first things lost.

5. **Give agents full context.** Never dispatch an agent with only
   mechanical instructions. Include the architecture doc and directives.
   Mechanical agents without architectural context produce
   architecture-violating mechanical changes.

6. **Challenge "PASS" verdicts.** When an audit reports everything passes,
   ask: "what dimensions did you NOT check?" Claude will name them.
   Then check those dimensions explicitly.

### After work

7. **Run multi-dimensional audits.** One agent per conformance dimension.
   A single agent checking "does this conform?" will check one dimension
   thoroughly and miss the others.

8. **Grep for old terminology after renames.** If a rename has a stated
   reason, grep for the old term and verify zero occurrences. Claude will
   rename the function and leave the old term in 30 other places.

9. **Require honesty about audit failures.** When bugs are found that an
   audit missed, demand that the audit document be updated to show what
   was missed and why. Without this, the audit document is a false record
   of conformance.

10. **Distrust in-context summaries.** When CLAUDE.md or a plan file
    makes a claim about the architecture, tell Claude to verify it against
    the authoritative source. "Read the architecture doc to confirm that
    claim before proceeding." Claude will trust what's already in context
    over what requires a tool call — force the tool call.
