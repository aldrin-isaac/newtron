# Using Claude Code with newtron

A kickstart guide for the human + Claude Code partnership on newtron.

This document does not replace the architecture docs, HOWTOs, or behavioral
directives — it orients both readers toward them and describes how to work
together effectively. For any concept mentioned here, the authoritative source
is cited inline. When this document and an authoritative source conflict, the
authoritative source wins (see `docs/ai-instructions.md` §20).

## The Document Hierarchy

Before either reader does anything, both need the same map:

| Document | What it is | Path |
|----------|-----------|------|
| Design Principles | Why newtron works the way it does | `docs/DESIGN_PRINCIPLES_NEWTRON.md` |
| Pipeline Architecture | The unified pipeline: Intent → Replay → Render → Deliver | `docs/newtron/unified-pipeline-architecture.md` |
| AI Instructions | Behavioral directives for Claude Code | `docs/ai-instructions.md` |
| Editing Guidelines | Prose principles scoped by document type | `docs/editing-guidelines.md` |
| CLAUDE.md | Project-specific rules (derivative of the above) | `CLAUDE.md` |
| newtron HLD/LLD/HOWTO | Architecture, types, operations | `docs/newtron/` |
| newtrun HLD/LLD/HOWTO | E2E test framework | `docs/newtrun/` |
| newtlab HLD/LLD/HOWTO | VM orchestration | `docs/newtlab/` |
| RCA Index | Root-cause analyses for SONiC pitfalls | `docs/rca/` |

**Precedence:** authoritative repo documents > CLAUDE.md > plan files > memory
records. CLAUDE.md is a derivative — it summarizes the design principles and
architecture for quick reference, but the sources it summarizes are the authority.

---

# Part 1: For the Human

You set the direction. Claude executes within constraints you define. The
constraints — `ai-instructions.md` and `CLAUDE.md` — are what make the
partnership work. Without them, Claude writes plausible code that violates
your system's design. With them, Claude traces a bug through six layers of
abstraction in a single session, runs mechanical conformance audits you'd never
have patience for, and catches symmetry violations in operations you wrote
months ago. The discipline pays for itself — but you have to hold the line.

## 1.1 Before You Start

Read the architecture docs yourself. You cannot correct Claude if you don't know
what "correct" looks like:

1. `docs/DESIGN_PRINCIPLES_NEWTRON.md` — the "why"
2. `docs/newtron/unified-pipeline-architecture.md` — the "how"
3. `docs/ai-instructions.md` — the behavioral contract you are both bound by

Verify that `CLAUDE.md` reflects the current architecture. Claude reads it on
every session start — stale summaries produce stale work.

## 1.2 What Goes Wrong

Before the categories of work, here is what actually goes wrong — real mistakes
from real sessions. These are the failure modes you'll learn to recognize.

**Architecture-shaped current code.** Claude reads the architecture doc, reads
the current code, and designs something that preserves the current code's
structure under new vocabulary. The plan looks right but implements the old
model. You'll recognize it when a plan feels too easy — when nothing needs to be
deleted. Fix: "Design from the architecture as if no code exists"
(`ai-instructions.md` §18).

**Speculative assertions.** Claude states "this function calls X" without having
read the code. You'll recognize it when Claude answers too quickly about
unfamiliar code. Fix: "Did you grep for the call site?"

**Single-instance fixes.** Claude fixes a bug in one operation without searching
for the same pattern in other operations. You'll recognize it when the same
class of bug appears a second time in the same session. Fix: "Search for all
instances before implementing."

**Making tests pass.** Claude changes test expectations or adds production
workarounds to make a failing test pass, rather than diagnosing whether the test
or the server is wrong. You'll recognize it when Claude proposes changing a test
assertion without first explaining what the server should be doing. Fix: "Which
is wrong — the test or the server?"

**Hack accumulation.** Claude added a polling workaround for a frrcfgd bug that
scanned a custom table instead of the standard VRF table — valid mechanics but a
reinvention of the feature's signal path. The upstream cause (frrcfgd's broken
vrf_handler) was the real problem. You'll recognize it when a fix adds code
instead of changing code. Fix: `ai-instructions.md` §4 — "If I removed the
upstream cause, would this code be unnecessary?"

**Context drift after compaction.** Claude loses precise definitions during
compression and works from approximate summaries. The drift is subtle — same
words, looser meaning. You'll recognize it when Claude's reasoning feels right
but its conclusions are slightly off. Fix: "Re-read ai-instructions and the
architecture doc."

## 1.3 Categories of Work

The operational details for each category live in the HOWTOs. This section is
about the collaboration — your role and the characteristic dynamics of each kind
of work.

### Tests

This is where the partnership is most tested. You define what behavior is being
verified — not a test title, but a statement of what the architecture promises
and what the test proves about that promise.

The single most important dynamic: Claude will try to make tests pass. It will
change assertions, add workarounds, reorder steps — anything to get green. Your
job is to interrupt that reflex. When a test fails, there are only two
possibilities: the server is wrong or the test is wrong. Claude must diagnose
which before touching either. "Which is wrong — the test or the server?"
(`ai-instructions.md` §6) is the correction you'll use most.

### Debugging SONiC

This is the most asymmetric category. You provide symptoms and device access.
Claude does the investigation. The dynamic to manage is speculation — Claude
will assert what a SONiC daemon does based on naming conventions or general
knowledge. SONiC daemons are quirky enough that general knowledge is often
wrong. Every claim about daemon behavior must be verified in source
(`ai-instructions.md` §11). "Did you read the code?" is sufficient.

Direct Claude to `docs/rca/` first. The index has documented pitfalls that will
save hours — daemon races, CONFIG_DB ordering issues, platform-specific SAI
failures. If the issue is new, the resolution becomes a new RCA.

### Bug Fixes

Insist on architecture conformance. A fix that works but violates the pipeline
model is worse than no fix (`ai-instructions.md` §1). Three things to watch for:

- Quick fixes that bypass abstractions — calling lower-level functions instead
  of Interface/Node methods.
- Fixes that break operational symmetry — fixing the forward op without checking
  the reverse.
- Single-instance fixes — fixing one bug without searching for the same pattern
  in other operations (see §1.2).

### New Features

Validate the design against architecture before Claude writes code. Claude
should present the architecture sections that govern the feature and explain how
the implementation conforms — before a line of code (`ai-instructions.md` §2).
Watch for new functions that bypass existing abstractions (`ai-instructions.md`
§3), CONFIG_DB writes that violate the single-owner principle, and operations
without reverse operations.

### SONiC Images and Patches

For **new images**, you source and boot-test first (`show interfaces status`,
`redis-cli -n 4 KEYS '*'`). Claude adapts the codebase afterward. Every
platform difference must become an RCA — undocumented differences get
rediscovered painfully.

For **patches**, you decide whether it's a bug fix or a feature reinvention.
The platform patching principle (`CLAUDE.md`) draws a clear line. Watch for
custom CONFIG_DB tables that replace standard SONiC signals, and code paths
that route around SAI failures instead of documenting them.

### Topologies and Documentation

For **topologies**, you define the intent (what are you testing?) and Claude
handles the file mechanics. Watch for port assignments that don't match the
platform's naming scheme — `CLAUDE.md` platform sections have per-platform
constraints.

For **documentation**, you verify technical accuracy. Claude handles prose
quality per `docs/editing-guidelines.md`. Watch for restated concepts that
belong in other docs (§43) and examples that mix incompatible types (§1).

### Doc Reference by Category

| Category | Key docs for Claude |
|----------|-------------------|
| Tests | newtrun HOWTO, newtrun LLD, `ai-instructions.md` §6/§16 |
| Debugging | `docs/rca/`, `CLAUDE.md` "Redis-First Interaction Principle" |
| Bug fixes | `ai-instructions.md`, `CLAUDE.md`, pipeline architecture doc |
| Features | `ai-instructions.md`, `CLAUDE.md` ownership map, pipeline arch doc |
| SONiC images | newtlab HOWTO, `CLAUDE.md` platform sections, `docs/rca/` |
| Patches | `CLAUDE.md` "Platform Patching Principle," `docs/rca/` |
| Topologies | newtlab HOWTO, newtlab LLD, `CLAUDE.md` platform sections |
| Documentation | `docs/editing-guidelines.md`, authoritative source for the content |

## 1.4 How to Correct Claude

Direct corrections work best. By directive number: "That violates §1." By
principle name: "Single-owner principle — who owns that table?" By pattern:
"Search for all instances before fixing one."

Corrections become memory records that persist across sessions. The most
valuable corrections are pattern-level — "this is the same bug class as X" or
"device is source of reality means X, not Y." These improve not just the
current session but every future one.

When Claude drifts after a context compaction (conversations are long, compaction
is inevitable), the fix is one sentence: "Re-read ai-instructions and the
architecture doc." Claude has a directive for this (`ai-instructions.md` §19).

---

# Part 2: For Claude Code

Part 1 tells the human what to watch for. This part tells you what to do — the
same categories of work, from your side.

## 2.1 Foundation

Before any work on newtron, read these documents in order. Do not skip any.
Do not work from CLAUDE.md summaries — read the authoritative sources.

1. `docs/ai-instructions.md` — your behavioral contract
2. `docs/DESIGN_PRINCIPLES_NEWTRON.md` — why newtron works the way it does
3. `docs/newtron/unified-pipeline-architecture.md` — the pipeline model
4. `CLAUDE.md` — project-specific rules (derivative of the above)

After reading, you should be able to answer these questions (answers are in the
cited docs, not restated here):

- What is intent round-trip completeness? (`DESIGN_PRINCIPLES_NEWTRON.md` §20, mechanical check in `CLAUDE.md`)
- What does "device is source of reality" actually mean? (`DESIGN_PRINCIPLES_NEWTRON.md` §1, §5)
- What is the single-owner principle? (`DESIGN_PRINCIPLES_NEWTRON.md` §27, ownership map in `CLAUDE.md`)
- What is the pipeline? (architecture doc)

### The Behavioral Contract

`ai-instructions.md` is a specification, not a suggestion. The directives are
organized by work phase (IMPL, TEST, PLAN, REVIEW, ALL) — see the scope key
in the doc itself. Reference directives by number when explaining decisions.

### Document Freshness

Every document — HLD, LLD, HOWTO, CLAUDE.md — is updated after the code changes
that motivate it. The lag is structural: code lands first, documentation follows
when someone gets to it. Often, intent lives only in the running conversation and
has not been captured anywhere yet.

The full precedence hierarchy (`ai-instructions.md` §20):

**running context > code > architecture/design docs > CLAUDE.md > plans > memory**

- **Running context is the leading edge.** Directives the user gives in the current
  conversation — design decisions, corrections, new constraints — are the highest
  priority. They represent intent that has not yet been persisted anywhere. When
  running context conflicts with any document or code, running context wins.
- **Code is the source of reality.** When a document says one thing and the code does
  another, investigate — but default to trusting the code.
- **Design and architecture docs capture intent** — the closest thing to a
  specification, but they too can be stale.
- **CLAUDE.md is a derivative summary** and the most likely to be stale. Verify
  claims against the authoritative source it cites.
- **Before citing any document as justification**, read the actual code to confirm
  the document still reflects reality (`ai-instructions.md` §11).

### Context Management

- **Post-compaction:** Immediately re-read `ai-instructions.md`, the pipeline
  architecture doc, and the active plan before continuing work
  (`ai-instructions.md` §19).
- **Source precedence:** Running context > code > authoritative repo docs > CLAUDE.md >
  plan files > memory records (`ai-instructions.md` §20).
- **Memory:** Store feedback (corrections, confirmed approaches) and project
  state (decisions, deadlines). Do not store code patterns, git history, or
  debugging solutions — these belong in code, commits, and RCAs respectively.

### Model Routing and Agent Dispatch

Use Opus for architectural decisions, audits, planning, code review, and
cross-file refactoring. Dispatch Sonnet agents for read-only research, known
mechanical edits, build/test cycles, and doc updates where changes are already
specified (`ai-instructions.md` §17).

Every dispatched agent must receive the behavioral directives, relevant
architecture sections, and the implementation plan. An agent with only
mechanical instructions will make changes that are mechanically correct but
architecturally wrong.

## 2.2 Working with SONiC

newtron is a Redis-centric system. All device interaction goes through SONiC
Redis databases — see `CLAUDE.md` "Redis-First Interaction Principle" and
"SONiC Redis Databases" for the DB map and rules.

`docs/rca/` is institutional knowledge — check it before debugging any SONiC
issue. When you resolve a new issue, document it as an RCA so the index grows.

Three platforms are in use (CiscoVS, sonic-vs, VPP), each with different
capabilities and constraints. See the `CLAUDE.md` platform sections for
specifics. Before writing CONFIG_DB entries for any SONiC feature, follow
`CLAUDE.md` "Feature Implementation Protocol." Before patching a SONiC daemon,
read `CLAUDE.md` "Platform Patching Principle."

## 2.3 How to Think About newtron Work

The rules in `ai-instructions.md` and `CLAUDE.md` are not a checklist to scan
after the work is done — they are the way you think while doing the work. Here
is what that looks like across every category.

### Adding a Feature

When asked to add a VRF feature, your first action is not to write code. It is
to quote the CLAUDE.md sections that govern VRF operations — the ownership map
(who owns the VRF table?), the naming convention (verb-first: `createVrf`, not
`vrfCreate`), the operational symmetry rule (what is the reverse of this
operation?). Then check whether the function already exists at a different layer
(`ai-instructions.md` §3). Then run the hack check (`ai-instructions.md` §4).
Only then write code. After writing, run a conformance audit
(`ai-instructions.md` §9) — enumerate every dimension, check each independently.

`CLAUDE.md` defines the mechanical checks: single-owner principle, operational
symmetry, interface as point of service, verb-first naming, domain-intent
naming, CONFIG_DB schema validation. Read the precise rules there — not here.

### Debugging SONiC

The goal is to perfect both the automation and SONiC itself. SONiC automation
is pointless without a SONiC that actually works and works well. newtron's
test infrastructure — newtlab VMs, newtrun scenarios, the RCA index — is also
an environment for developers to find and fix issues in SONiC.

When asked to debug a BGP session failure, your first action is to search
`docs/rca/` for matching symptoms. If nothing matches, use observation
primitives — `GetRoute`, `QueryConfigDB`, health checks (see
`docs/newtron/howto.md`) — to gather facts. Do not hypothesize about what
frrcfgd or bgpcfgd does based on function names. Read the daemon source
(`ai-instructions.md` §11). When the root cause is found, write an RCA before
moving on.

After CONFIG_DB writes, daemons need settling time. Use `pollUntil`, never
`time.Sleep`. When a daemon ignores an entry because a prerequisite wasn't
ready, that's a daemon race — and daemon races are RCA material. If no
resolution within 15 minutes on Sonnet, switch to Opus
(`ai-instructions.md` §17).

### Fixing a Bug

The first diagnostic question is "which architectural principle was violated?" —
not "what is the quickest fix?" (`ai-instructions.md` §6). Before fixing,
search for all instances of the same pattern — distinct domain patterns
(binding/unbinding, adding/removing members, setting properties) may share the
same implementation bug. Fix all in one pass.

For intent-related bugs, use the mechanical check in `CLAUDE.md` "Intent
Round-Trip Completeness." When fixing a forward operation, check the reverse too
(`CLAUDE.md` "Operational Symmetry"). Before changing shared code, follow
`CLAUDE.md` "Regression Prevention."

### Fixing a Test

When a test fails, the human will push back if you try to make it pass instead
of diagnosing the root cause. They are right to. Determine whether the test
expectation or the server behavior is wrong before changing either
(`ai-instructions.md` §6). If the server is wrong, fix the server per the
architecture. If the test is wrong, explain why the expectation was incorrect.
Never change both simultaneously — that hides which was actually broken. Never
weaken an assertion. Never add a production workaround for a test.

Read `docs/newtrun/howto.md` §7 and `docs/newtrun/lld.md` for the step action
vocabulary. Follow `CLAUDE.md` "Testing Protocol" for test discipline.

### New SONiC Image

When the human provides a new image, they will have already verified basic boot
and operations. Your job is what comes after: investigate the platform-specific
behavior (port naming scheme, HWSKU, daemon versions, CONFIG_DB factory state),
compare against `CLAUDE.md` platform sections, and adapt the codebase.
Document every difference as an RCA and update the relevant `CLAUDE.md` platform
section. Undocumented differences get rediscovered — by you, in a future
session, with no memory of the first time.

Start with the newtlab HOWTO (`docs/newtlab/howto.md`) for deployment
procedures. For topologies, the key constraints are platform-specific (see
§2.2). Read `docs/newtlab/lld.md` for deploy internals. Start minimal — a
topology that doesn't boot wastes more time than building incrementally.

### SONiC Patch

Read `CLAUDE.md` "Platform Patching Principle" before writing patch code. The
human decides whether it's a bug fix or a feature reinvention — but you are
responsible for not crossing the line accidentally. Check `docs/rca/` for
existing documentation. If the RCA has a resolution path, follow it. If not,
create a new RCA documenting the bug, the patch, and the upstream resolution
path.

### Updating Documentation

`docs/editing-guidelines.md` is the prose standard — read it before editing any
document, and check which scope tags apply. After schema or API changes, follow
`CLAUDE.md` "Documentation Freshness Protocol." After updating any architecture
doc, verify CLAUDE.md still matches (`ai-instructions.md` §20).

The human will catch technical inaccuracies. That's their role. Yours is to
catch structural violations — restated concepts (§43), drifting counts (§24),
inconsistent formatting (§38). Between the two of you, every dimension gets
checked.

---
