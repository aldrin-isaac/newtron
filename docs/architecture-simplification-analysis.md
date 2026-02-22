# Architecture Simplification Analysis

This document evaluates the four architectural simplifications proposed in
the February 2026 architecture review against the 20 design principles
documented in `docs/DESIGN_PRINCIPLES.md`. Each proposal is assessed for
which principles it would satisfy better, which it would weaken, and
whether the line savings justify the tradeoff.

## The Four Proposed Simplifications

| # | Proposal | Lines Saved |
|---|----------|-------------|
| S1 | Merge `network/` and `network/node/` into one package | ~330 |
| S2 | Replace PreconditionChecker fluent builder with plain if-statements | ~150 |
| S3 | Replace CompositeBuilder with direct map construction | ~80 |
| S4 | Unify entry types (CompositeEntry, Change, ConfigChange, TableChange) | ~150 |

Total: ~710 lines across 8,800+ LOC in `node/` alone.

---

## S1: Merge network/ and node/ packages

The argument: SpecProvider + ResolvedSpecs (130 lines of accessor
boilerplate) exist solely to break a circular import. Node and Network
are "one conceptual unit" — Network creates Nodes, passes them specs,
orchestrates operations. The package split adds complexity without
adding safety.

### Principles it would violate

**#13 Import Direction — Dependencies Flow One Way.**
This is a direct hit. The current two-package split gives a
compiler-enforced guarantee: nothing in `node/` can import `network/`.
Topology orchestration code in `network/topology.go` cannot leak into
node-level operations, and node-level operations cannot reach up into
Network state. Merging eliminates this boundary entirely. In a single
package, any function can call any other function — the Go compiler
cannot enforce layering within a package. The principle says "when you
change a Node method, you know the blast radius is `node/` plus its
callers." In a merged package, the blast radius of any change is the
entire package.

**#9 Hierarchical Spec Resolution.**
The principle says the design "cleanly separates two concerns: what
specs exist (the hierarchy, owned by Network) and what specs this node
sees (the merged view, owned by Node via SpecProvider)." The
SpecProvider interface is the enforcement mechanism. In a merged
package, a developer writing a node operation could bypass the resolved
view and query the hierarchy directly — reading zone-level specs,
checking network-level overrides. Nothing would prevent it. The current
design makes this architecturally impossible.

**#3 Two Tools and an Orchestrator (by analogy).**
The principle that "each program owns exactly one level of abstraction"
applies within programs too. Network owns "which devices, which specs,
what order" (orchestration). Node owns "what to do with this device"
(primitive). These are different abstraction levels. The package
boundary enforces the same separation within newtron that the program
boundary enforces across newtron/newtlab/newtest.

### Principles it would satisfy better

**#10 DRY — Across Programs.**
The 10-method SpecProvider interface and its ResolvedSpecs
implementation are ~130 lines of accessor boilerplate. Each method is
a one-line map lookup. If the interface serves no purpose beyond
breaking a circular import, this is genuine redundancy.

### Assessment

The architecture review undervalues the SpecProvider interface. It is
not just an import-cycle breaker — it is the contract that separates
hierarchy resolution from node operations. The 130 lines buy:

1. **Compiler-enforced import direction** (#13) — the single strongest
   structural guarantee in the codebase
2. **Testability** — node operations can be tested with a mock
   SpecProvider, no Network needed
3. **Hierarchy encapsulation** (#9) — nodes see resolved specs, never
   the hierarchy itself
4. **Bounded blast radius** — changes to Network orchestration provably
   cannot affect Node operations

**Verdict: Reject.** The 130 lines of SpecProvider boilerplate are the
cost of a compiler-enforced architectural boundary. This is the one
simplification that would weaken the design.

---

## S2: PreconditionChecker → plain if-statements

The argument: 197-line fluent builder collects errors but `Result()`
returns only the first error. Equivalent to 3-line early-return
if-statements.

### Principles the current design satisfies

**#6 Prevent Bad Writes.**
The principle says "preconditions are built into the operation, not
bolted on by the caller." Both the fluent builder and if-statements
satisfy this — the checks run at the start of the operation either way.
But the builder provides a consistent pattern that is visible and
discoverable. Every operation starts with
`n.precondition(op, resource).Require...().Result()`. The pattern is
grep-able. With if-statements, each operation reimplements the pattern
slightly differently — same semantics, less consistency.

**#10 DRY.**
Every operation needs `RequireConnected()` and `RequireLocked()`. The
builder DRYs these up via `n.precondition()` which adds them
automatically. With if-statements, you would either duplicate the
connection/lock checks in every operation, or extract helper
functions — which is just reimplementing the builder in a different
style.

The builder also provides structured error messages with operation and
resource context (`"precondition failed for create-vlan on Vlan100:
VLAN already exists"`). With if-statements, each operation would need
to format its own error messages, which tends toward inconsistency.

### Principles the proposed change would satisfy better

**No over-engineering (CLAUDE.md principle).**
If `Result()` truly returns only the first error, the builder's error
accumulation is unused machinery. Plain if-statements with early
returns are simpler, have less indirection, and are more idiomatic Go.

### Assessment

The architecture review has a valid point: if `Result()` returns the
first error, the fluent builder is heavier than needed. But the builder
provides two real benefits: DRY common checks and a consistent,
discoverable pattern.

If `Result()` were changed to return all errors, the builder's design
would be fully justified. As-is, it is a mild case of anticipatory
design — not harmful, but not pulling its full weight either. Either
approach satisfies the principles.

**Verdict: Neutral.** Either approach works. The builder has a mild DRY
advantage and provides consistent error formatting. If-statements are
more idiomatic Go. Neither choice violates a design principle.

---

## S3: CompositeBuilder → direct map construction

The argument: the builder wraps
`map[string]map[string]map[string]string`. `AddEntry` is just map
insertion with nil-check. `Build()` stamps metadata. No safety benefit
over direct construction.

### Principles the current design satisfies

**#12 File-Level Feature Cohesion.**
The builder keeps composite construction knowledge inside
`composite.go`. `topology.go` calls
`cb.AddEntries(node.VTEPConfig(...))` without knowing the three-level
map structure. Without the builder, `topology.go` would need to do
`config.Tables[table][key] = fields` with nil-checks at each level —
that is composite internals leaking into orchestration code. This is
schema leakage, which #12 explicitly prohibits.

**#14 Dry-Run as a First-Class Mode.**
The builder produces a `CompositeConfig` that can be inspected
(dry-run) or delivered (execute). The builder pattern naturally
separates "build the config" from "deliver the config." Direct map
construction would still need a CompositeConfig type for
preview/delivery — the builder just makes the construction phase
cleaner.

### Principles the proposed change would satisfy

**No over-engineering (CLAUDE.md).**
80 lines is small. But the builder is also small — simple methods,
clear purpose. The architecture review claims "no safety benefit," but
the encapsulation benefit is real: callers do not touch the nested map
directly.

### Assessment

The architecture review undervalues `AddEntries([]CompositeEntry)`.
This method is the bridge between config function output and composite
structure. It accepts the output of `VTEPConfig()`,
`BGPNeighborConfig()`, etc. and inserts them into the composite without
exposing the internal representation. Without it, every caller needs to
know the map structure.

The builder also provides `Build()` which stamps metadata (timestamp,
device name, mode) — ensuring provenance is always recorded. Direct
construction would make metadata optional, which means someone would
eventually forget it.

80 lines for that abstraction is a good trade.

**Verdict: Reject.** The builder prevents schema leakage (#12), ensures
metadata provenance (#17), and costs only 80 lines. The architecture
review is wrong that there is "no safety benefit" — the encapsulation
is the safety benefit.

---

## S4: Unify entry types

The argument: CompositeEntry, Change, ConfigChange, TableChange are all
"CONFIG_DB entry" with different wrapping. Conversion functions
(`configToChangeSet`, `ToTableChanges`, `ToConfigChanges`) are pure
glue. A single `Entry{Table, Key, Fields}` type would eliminate ~150
lines.

### What each type actually represents

| Type | Stage | Has Change Type? | Has Old Value? | Package |
|------|-------|:---:|:---:|---------|
| CompositeEntry | Config function output | No | No | node/ |
| Change | Planned/executed mutation | Yes (Add/Modify/Delete) | Yes | node/ |
| ConfigChange | Device-layer operation | Yes | Yes | device/sonic/ |
| TableChange | Pipeline delivery format | No | No | device/sonic/ |

### Principles the current design satisfies

**#17 The ChangeSet Is the Universal Contract.**
The ChangeSet contains `Change` objects, which carry change semantics
(type, old/new values) that raw entries do not have. `CompositeEntry`
is "what should exist." `Change` is "what mutation to perform." These
are different concepts. Collapsing them into one type would lose the
semantic distinction between "here are the entries" and "here is the
delta."

**#13 Import Direction.**
CompositeEntry lives in `node/`. ConfigChange lives in
`device/sonic/`. The package boundary enforces that config functions in
`node/` do not depend on device-layer types. A unified Entry type would
need to live in one package — either `node/` imports `device/` types
(already true, fine) or `device/` imports `node/` types (would reverse
the dependency).

### Principles the proposed change would satisfy better

**#10 DRY.**
The conversion functions are genuine redundancy. `configToChangeSet`
wraps entries as Add changes. `toDeviceChanges` translates Change to
ConfigChange (same fields, different struct). `ToTableChanges` strips
change types. These are boilerplate that a unified type would eliminate.

### Assessment

The types are not truly identical. Two of the four conversion functions
add meaningful information:
- `configToChangeSet`: adds change type, device name, operation context
- `ToConfigChanges`: stamps all entries as ChangeTypeAdd

Two are closer to pure glue:
- `toDeviceChanges`: field-for-field copy between Change and ConfigChange
- `ToTableChanges`: drops change type from ConfigChange

A **partial unification** would capture most of the benefit: merge
CompositeEntry and TableChange (both are Table+Key+Fields with no
change semantics) into a single `Entry` type, but keep `Change` as
distinct (it carries mutation semantics the others do not). This would
eliminate `ToTableChanges` and simplify `configToChangeSet`, saving
~80 lines while preserving the semantic distinction.

Full unification — collapsing all four types into one — would lose the
meaningful distinction between "an entry that should exist" and "a
mutation with before/after state." That distinction is what makes the
ChangeSet a contract (#17), not just a list.

**Verdict: Partial accept.** Unify CompositeEntry and TableChange into
a single `Entry{Table, Key, Fields}` type. Keep `Change` as a distinct
type that wraps Entry with change semantics (Add/Modify/Delete, old
value). This saves ~80 lines while preserving the semantic distinction
that #17 depends on.

---

## Summary Matrix

| Principle | S1 (merge pkgs) | S2 (if-stmts) | S3 (no builder) | S4 (unify types) |
|-----------|:---:|:---:|:---:|:---:|
| #3 Abstraction levels | worse | — | — | — |
| #6 Prevent bad writes | — | slightly worse | — | — |
| #9 Hierarchical resolution | **worse** | — | — | — |
| #10 DRY | better | slightly worse | — | better |
| #12 Feature cohesion | — | — | **worse** | partial better |
| #13 Import direction | **violated** | — | — | needs care |
| #14 Dry-run first-class | — | — | slightly worse | — |
| #17 ChangeSet contract | — | — | — | worse if full |

## Verdicts

| Proposal | Lines Saved | Verdict | Rationale |
|----------|:-----------:|---------|-----------|
| S1 Merge packages | 330 | **Reject** | Violates #13, weakens #9; SpecProvider is not boilerplate — it is a compiler-enforced architectural boundary |
| S2 Plain if-statements | 150 | **Neutral** | Either approach works; builder has mild DRY advantage and consistent error formatting |
| S3 Remove CompositeBuilder | 80 | **Reject** | Prevents schema leakage per #12; encapsulation is the safety benefit |
| S4 Unify entry types | 150 | **Partial accept** | Unify CompositeEntry + TableChange (~80 lines saved); keep Change distinct per #17 |

The current design satisfies the 20 principles well. Of the four
proposed simplifications, one (S1) would actively damage architectural
guarantees, one (S3) would cause schema leakage, one (S2) is a genuine
toss-up, and one (S4) has a valid partial form. The architecture review
correctly identified that ~500 lines could be saved, but underestimated
the principled reasons those lines exist.
