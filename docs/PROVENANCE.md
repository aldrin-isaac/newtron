# Provenance

## 1. Purpose

This document is a curated record of how newtron came into existence. It
captures the framing under which the project was undertaken, the directing
prompts that produced its architecture, and the major design decisions
those prompts crystallized. It is not a transcript and not a complete
history; it is a curated artifact intended to make the method of
production transparent to anyone who reads the repository.

The curation itself — choosing what is consequential, paraphrasing the
seed prompts, organizing the decisions into a coherent architectural
narrative — is part of the human authorship of the project.

## 2. Origin: an exercise in recollection

newtron was undertaken during medical recovery, beginning at the start of
2026. The recovery imposed weeks of physical inactivity. The project was
the way evenings and weekends were filled. It was undertaken as an
exercise in recollection: a structured attempt to write down general
professional experience in network engineering and network automation
that had accumulated over a career, before any more of it faded.

The substance of the project was sourced exclusively from the author's
own held knowledge — methodology, design taste, pattern recognition, and
architectural instincts built over years of practice. No external
materials of any kind were consulted as inputs to the directing prompts:
no prior-employer documents, no prior-employer code, no current-employer
documents, no current-employer code, no third-party references, no
copy-pasted snippets from anywhere.

The single explicit exception is documented external references for the
SONiC platform itself — publicly-available SONiC community documentation,
public schemas (in particular the `sonic-yang-models` repository for
CONFIG_DB validation), and public source from the `sonic-net` GitHub
organization — consulted only where they were needed to ground the
implementation against the target platform's actual behavior. Where this
boundary applied, the consultation was the platform's public material,
not anyone's private operational knowledge.

What the project exercises is therefore general professional knowledge:
an individual's accumulated skills, training, and architectural taste,
applied to a public open-source platform. Under California public policy,
that body of general skills, knowledge, training, and experience belongs
to the employee and travels with them between roles. This document is a
plain-English statement of that fact, not a legal brief; it is recorded
here so the substantive basis of the project is unambiguous to any
future reader.

## 3. Method: conversational AI-assisted production

The working method was conversational. The author directed a single
Claude instance (Anthropic's Claude Code, on a personal Claude Pro
subscription tied to an individual Anthropic account) through natural-
language prompts that stated general industry concepts, architectural
intentions, and review judgments. Design and architectural decisions —
what the system should do, how it should be structured, what the
abstractions and invariants are — were the human's. Syntactic
implementation — the Go code that realizes those decisions — was
predominantly produced by the AI under direction.

The implementation language is Go. The author had not previously used Go
at any employer or on any personal project. There is therefore no prior
Go implementation, by the author or by anyone associated with the
author, that the current code could be derived from. The Go in this
repository was written conversationally during this project, against the
architectural decisions described below.

## 4. Public-from-inception

The repository has been public since its first commit. The initial
commit (`2053eff`, 2026-02-01, "Initial commit: newtron network
automation tool") was pushed to a public GitHub repository on a personal
account, and every state of the artifact thereafter has been a public
state. There has been no pre-publication private development period.

This matters for provenance. The Git commit history on GitHub —
attributed throughout to a personal email (aldrin.isaac@gmail.com),
hosted on a third-party platform, with timestamps the author cannot
unilaterally alter — is itself the contemporaneous, tamper-evident
record of the project's development. Anyone reading this repository in
the future can see for themselves what was added when, in what order,
and how the architecture evolved.

Recent commits are additionally signed with the author's SSH key
registered as a Signing Key on GitHub, so that GitHub's "Verified" badge
attests to the cryptographic identity of the committer for those
commits. Earlier commits were unsigned at the time they were made; the
GitHub-hosted history is the primary provenance evidence for them.

## 5. Directing prompts: the architectural seed

This section curates the most consequential directing prompts — the
ones that set or substantially redirected the architecture. Prompts are
paraphrased. The original full conversation transcripts are not
reproduced here; what is preserved is the architectural turning point
each prompt produced.

Where a directing prompt is not preserved precisely in the author's
recollection, that is noted in the entry rather than fabricated.

### 5.1 The seed: opinionated SONiC network automation

**Prompt (paraphrased):** Build a network automation system for SONiC
that takes one opinionated stance on each unit of configuration —
validated before write, applied atomically, verified after, reversible
by design — as a personal exercise in recollecting a career's worth of
network-automation methodology.

**Decision:** This framed the project's purpose: not a complete
automation product, not a comparison against existing tools, but a
study of a particular stance on automation, applied end-to-end to a
single open platform. It is the framing that makes "opinionated" the
operative word and that fixes SONiC as the platform.

### 5.2 Implementation language

**Prompt (paraphrased):** Use Go, accepting that this is the author's
first Go project; lean on the AI to produce idiomatic Go from
architecturally-stated intent.

**Decision:** Go was chosen because it is the lingua franca of the
SONiC ecosystem (sonic-mgmt and adjacent tooling), produces single
static binaries that are operationally simple to deliver, and has a
type system strong enough to encode the architectural distinctions the
project relied on. The choice is also consequential for provenance:
the author had no prior Go body of work, so nothing in this repository
could be derived from a private corpus.

### 5.3 Design-principles document as authority

**Prompt (paraphrased):** Establish a single design-principles document
that is the authoritative statement of what the system stands for; the
implementation must derive from the document, not the other way
around.

**Decision:** `DESIGN_PRINCIPLES.md` (and the newtron-specific
companion `DESIGN_PRINCIPLES_NEWTRON.md`) are treated as authoritative.
CLAUDE.md and other operational instructions are derivative summaries.
When the document and the code disagree, the document is the target;
the code is corrected. This produced the rule that architecture drives
implementation, not the reverse — a discipline that is itself recorded
as a project memory.

### 5.4 The Node abstraction: one object, three states

**Prompt (paraphrased):** Treat intent and reality as the same object
viewed from different starting points; a Node initialized from specs
*is* the expected state, and a Node whose projection is rebuilt from
on-device intent records *is* the expected state verified against
reality.

**Decision:** The Node became the central abstraction. The same code
path supports three initialization modes — offline (from specs),
connected (specs + device reads), actuated (intents from
NEWTRON_INTENT on the device). This eliminated the structural source
of drift that arises from maintaining parallel intent and reality
representations under separate code paths. The thesis is recorded as
DESIGN_PRINCIPLES_NEWTRON §1.

### 5.5 ChangeSet: validate, write, verify, record, reverse

**Prompt (paraphrased):** Every device-affecting operation must produce
a ChangeSet that is validated against schema before apply, applied
atomically, verified by re-reading what was written, and recorded so
the operation can be reversed cleanly.

**Decision:** The ChangeSet became the universal currency of
device-affecting operations. The validate–write–verify–record–reverse
sequence is the delivery contract. Verification is done by diffing the
re-read state against the ChangeSet, not by asserting domain-level
correctness — that distinction (observation primitive, not assertion
primitive) is itself a separate architectural decision.

### 5.6 Verification primitives: observe, do not assert

**Prompt (paraphrased):** newtron observes single-device state and
returns structured data; cross-device correctness is the test
orchestrator's job, not the device tool's.

**Decision:** Verification methods (`GetRoute`, `GetRouteASIC`, etc.)
return data structures, not pass/fail verdicts. The single assertion
the device tool makes is `cs.Verify(n)` — that its own writes landed,
diffed against the ChangeSet. Cross-device assertions (route
propagation, fabric convergence, data plane) live in the test
orchestrator (newtrun). This boundary is recorded as a project memory
and as the v5 verification architecture.

### 5.7 newtlab: a userspace VM topology orchestrator

**Prompt (paraphrased):** Build a topology orchestrator that runs SONiC
VMs entirely in userspace, without containerlab, without root, with
clear separation between the lab tool and the network-automation tool.

**Decision:** `newtlab` was built around QEMU/KVM, with `newtlink` as
the bridge agent for multi-host link distribution. Topologies are
described declaratively. The decision keeps the testbed reproducible
on a personal machine and avoids dependence on external infrastructure
or operations teams.

### 5.8 newtrun: scenario-driven test orchestration

**Prompt (paraphrased):** Tests are scenarios written in a structured
DSL (initially YAML, later step-action JSON), composed into suites
with explicit dependency ordering, executed against a deployed
topology. The test runner asserts cross-device behavior; the device
tool does not.

**Decision:** `newtrun` (originally `newtest`, renamed `2026-02-28`)
is a YAML/JSON-scenario-driven orchestrator. Each step is a typed
action. Suites compose scenarios in dependency order. This is the
substrate that exercises every architectural claim newtron makes.

### 5.9 Public API boundary

**Prompt (paraphrased):** Expose newtron to external consumers (CLI,
HTTP server, future orchestrators) through a single public package,
`pkg/newtron/`, which uses domain vocabulary; everything else is
internal.

**Decision:** The `pkg/newtron/` boundary was established (commit
`ddddc56`, 2026-02-14). Public types use domain language; operations
take names (strings) and the package resolves specs internally.
Verification artifacts (`CompositeInfo`, ChangeCount, etc.) are opaque
handles or summary counts — never internal data structures.

### 5.10 Single-owner CONFIG_DB tables

**Prompt (paraphrased):** Each CONFIG_DB table has exactly one owning
file in the codebase. Composite operations call owning primitives and
merge their ChangeSets; they do not construct CONFIG_DB entries
inline.

**Decision:** This produced the table-ownership map (recorded in
CLAUDE.md). It is the structural basis of the project's "guess where
a feature lives by the file name" principle and eliminates a class of
bug where two different code paths write the same table with
diverging shapes.

### 5.11 Intent records on the device

**Prompt (paraphrased):** When newtron operates on a device, it should
write an intent record to a NEWTRON_INTENT table on that device's
CONFIG_DB describing what was done. On a future operation, the device
itself is the source of truth for "what this device thinks newtron
told it to be."

**Decision:** The intent records are written on every successful
operation. They are self-sufficient for reverse operations (no
re-resolution against specs at removal time). They make the device
its own authoritative provenance source: an actuated Node rebuilds
its projection by replaying its own on-device intent records. The
unified intent model (commit `b597e1f`, 2026-03-15) collapsed an
earlier dual NEWTRON_SERVICE_BINDING + NEWTRON_INTENT design into a
single table.

### 5.12 Content-hashed shared policy objects

**Prompt (paraphrased):** Shared policy objects (ACL_TABLE, ROUTE_MAP,
PREFIX_SET, COMMUNITY_SET) should be named after the SHA256 of their
generated content, so two services that produce identical policy
share one object naturally; refcount-style lifecycle then drops the
object only when the last consumer is removed.

**Decision:** Content-hashed naming was implemented in commits
`0f8d061`, `4259fdb`, `b86697a` (2026-03-09). Dependent objects use
bottom-up Merkle hashing. This eliminates the "what should we name
shared policies?" problem and makes lifecycle automatic.

### 5.13 YANG-derived schema validation

**Prompt (paraphrased):** Every CONFIG_DB write must be validated
against a schema derived from SONiC's published YANG models, with
fail-closed semantics — unknown tables or fields are errors, not
warnings.

**Decision:** Implemented commit `47fd736` (2026-03-08). The schema
catches the entire class of bug where a malformed write passes Redis
(which validates nothing) and surfaces as a daemon error minutes
later. Adding new tables requires citing the YANG source in the
schema — an explicit discipline recorded in CLAUDE.md.

### 5.14 Operational symmetry

**Prompt (paraphrased):** For every forward action there must be a
reverse, added in the same commit; reverse operations must be
reference-aware (scan for remaining consumers before deleting shared
resources).

**Decision:** The principle is recorded as DESIGN_PRINCIPLES_NEWTRON
§15 and tracked across the codebase. It is the structural basis for
why content-hashed shared objects can be safely shared (their
lifecycle is reference-counted), why composite teardown is safe
(every primitive has a reverse), and why baseline operations are the
explicitly-noted exception (their collective reverse is reconcile).

### 5.15 The intent-first unification

**Prompt (paraphrased):** The architecture has accumulated several
parallel mechanisms — composite operations, primitive operations,
service bindings, intent records, projection rebuilds. Collapse them
into a single unified pipeline so there is exactly one path from
intent to device.

**Decision:** The intent-first / intent-DAG architecture (commits
`50b82f9` and `931cd50`, 2026-03-26 / 2026-03-31) collapsed parallel
mechanisms into a single pipeline: Intent → Replay → Render →
[Deliver]. The same pipeline serves online operations, offline
provisioning, and reconcile. This is documented in
`docs/newtron/unified-pipeline-architecture.md` and is treated as
authoritative; CLAUDE.md is its derivative.

### 5.16 RCAs as a permanent record

**Prompt (paraphrased):** Every non-trivial debugging investigation
that surfaces a SONiC-platform behavior, a daemon race, or a
reproducible pitfall produces a Root-Cause Analysis (RCA) document
checked into the repository. RCAs are permanent; they are how the
project teaches.

**Decision:** The `docs/rca/` directory grew to ~40 RCAs as the
project encountered SONiC-specific behaviors. Each RCA names the
phenomenon, gives the root cause, and records the resolution. They
are the shared institutional memory of the project.

### 5.17 Greenfield: no backwards compatibility

**Prompt (paraphrased):** No compatibility shims, no API versioning,
no deprecated aliases. When something changes, delete the old; do
not keep both.

**Decision:** Recorded as DESIGN_PRINCIPLES_NEWTRON §40, §41. Every
rename in the commit history (newtest→newtrun, vmlab→newtlab,
Device→Node, etc.) is a clean replacement, not a coexistence.
Multi-SONiC-release support is the one allowed exception, framed as
multi-platform support rather than backwards compatibility.

---

Some directing prompts behind smaller architectural moves —
naming-convention normalization, the introduction of distributed
locking, the migration to graph-easy for diagrams, the rebuild of
documentation diagrams from `.dot` source files — are not preserved
verbatim in the author's recollection. The decisions themselves are
recorded in commit messages and in CLAUDE.md; the seed prompts that
produced them have not been precisely reconstructed and are not
fabricated here.

## 6. Major design decisions and their rationale

These are the architectural opinions newtron takes a position on.
They are exactly the kinds of opinions that constitute general
professional knowledge — judgments about how to build automation,
not specifics of any particular operator's network.

### 6.1 Specs are intent; the device is reality

Specifications describe what the network should look like. The
device's own state — its CONFIG_DB, its NEWTRON_INTENT records, its
running daemons — is what exists. Operations transform reality using
intent. This framing rejects the common pattern of maintaining
intent and reality in parallel external stores synchronized by
reconcilers.

### 6.2 The same Node operates online, offline, and actuated

The Node abstraction does not branch by mode. Initialization
differs; the methods that compute desired state, validate it, and
turn it into ChangeSets are identical. This is the structural
guarantee that offline provisioning and connected operations cannot
diverge.

### 6.3 Intent persists on the device itself

newtron writes its intent records to the device's CONFIG_DB
(NEWTRON_INTENT). The device is its own authoritative provenance
source. This means a fresh newtron process on a new host can pick
up an existing actuated device by reading its intent records — no
external state store is required, and no synchronization between
external store and device can drift.

### 6.4 Validate, write, verify, record, reverse

Every device-affecting operation runs the same five-step contract.
Verification is structural — re-read what was written, diff against
the ChangeSet — not domain-level. This is the difference between
"the write landed" and "the network is correct"; only the former is
the device tool's responsibility.

### 6.5 Cross-device assertions belong to the orchestrator

newtron observes single-device state and returns data. Whether
routes propagated across a fabric, whether the data plane carries
traffic, whether two devices' views of an EVPN agree — those are
the test orchestrator's concern. Keeping the device tool's
verification surface free of cross-device assertions keeps it
composable and avoids embedding network-shape assumptions in the
device library.

### 6.6 One pattern per unit of configuration

For each unit of configuration — a VLAN, a BGP neighbor, a
service-on-interface binding, an ACL rule — newtron offers one
pattern. The pattern is the opinion. What the operator builds from
those patterns is the operator's design. The opinions live at the
smallest level (the individual CONFIG_DB entry), not at the
network level.

### 6.7 Operational symmetry — every forward has a reverse

A forward action without a reverse is a leak. Reverse operations
are added in the same commit as their forwards. Reference-aware
reverses prevent shared-resource overdeletion. Baseline
("setup-*", "set-*") operations are the explicit exception — their
collective reverse is reconcile.

### 6.8 Single-owner CONFIG_DB tables

Each CONFIG_DB table has exactly one owning module. Composites do
not construct CONFIG_DB entries inline; they call the owning
primitive and merge ChangeSets. This produces the property that a
reader can guess where a feature is implemented from the file
name, and eliminates a class of bug where divergent code paths
write the same table inconsistently.

### 6.9 Content-hashed shared objects

Shared policy objects are named after a hash of their generated
content. Identical policies produce one object naturally; lifecycle
is reference-counted. This eliminates an entire category of naming
problem — what to call shared ACLs, route maps, prefix sets — and
makes garbage collection automatic.

### 6.10 Redis-first device interaction

All device interaction goes through SONiC's Redis databases
(CONFIG_DB, APP_DB, ASIC_DB, STATE_DB). Where CLI/SSH is unavoidable
(e.g., `config save`, `docker restart`), the call site is tagged
explicitly with the gap and the upstream change that would
eliminate it. CLI scraping is structurally fragile; Redis is the
durable interface SONiC publishes.

### 6.11 Schema as fail-closed contract

CONFIG_DB writes are validated against a schema derived from
SONiC's published YANG models. Unknown tables or fields are errors,
not warnings. Adding a table requires citing its YANG source. The
schema is the project's structural defense against the worst
property of CONFIG_DB: that Redis accepts anything.

### 6.12 Pipeline-first explanations

Every architectural component is explained by its position in the
pipeline — what feeds it, what it feeds, which stage it belongs to.
There is exactly one pipeline: Intent → Replay → Render → [Deliver].
A component description that does not state the pipeline position
is incomplete.

### 6.13 Greenfield, no compatibility shims

When something is replaced, the old goes. No deprecated aliases, no
versioned APIs, no coexistence. Multi-SONiC-release support is
multi-platform, not backwards compatibility. This keeps the
codebase legible and prevents the slow accretion of dead code that
characterizes long-lived systems.

### 6.14 Public API boundary

External consumers — the CLI, the HTTP server, future orchestrators
— import only `pkg/newtron/`. Public types use domain vocabulary;
the public surface accepts names and resolves specs internally.
Internal packages (`network/`, `network/node/`, `device/sonic/`)
are not part of the contract.

### 6.15 Documentation is part of the architecture

The HLD, LLD, and HOWTO documents for newtron, newtrun, and
newtlab are kept current with the code. CLAUDE.md is a derivative
summary, not the authority. RCAs are permanent. The design
principles documents are the target the implementation derives
from, not a description of where it landed.

## 7. What is and isn't in here

This document is curated rather than exhaustive. The complete
conversation history that produced the codebase is not reproduced;
the architectural turning points are. Smaller directing prompts —
naming touch-ups, individual bug fixes, prose edits — are not
listed. Their record is the commit history.

Supplemental private evidence is preserved by the author: Claude
Code conversation transcripts where retained, the personal
Anthropic account and its billing records, and provenance from the
personal device and personal accounts on which the work was done.
This evidence is available to anyone with legitimate cause to ask,
under a mutually-agreed confidentiality protocol where appropriate.

This document will be updated as the project evolves. Future
material architectural changes will be added to sections 5 and 6 in
the same format. Smaller revisions will appear only as commits.
