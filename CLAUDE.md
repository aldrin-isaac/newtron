# Newtron Project — Claude Code Instructions

## Project Documentation

Read these before making design decisions or writing code in unfamiliar areas:

| Document | Path | Contents |
|----------|------|----------|
| newtron HLD | `docs/newtron/hld.md` | Architecture, verification primitives, Redis interaction model |
| newtron LLD | `docs/newtron/lld.md` | Type definitions, method signatures, package structure |
| Device LLD | `docs/newtron/device-lld.md` | CONFIG_DB/APP_DB/ASIC_DB/STATE_DB layer, SSH tunneling, ChangeSets |
| Pipeline Reference | `docs/newtron/unified-pipeline-architecture.md` | Unified pipeline: Intent → Replay → Render → [Deliver], end-to-end traces |
| newtron HOWTO | `docs/newtron/howto.md` | Operational patterns, provisioning flow |
| newtrun HLD | `docs/newtrun/hld.md` | E2E test framework design |
| newtrun LLD | `docs/newtrun/lld.md` | Step actions, suite mode, dependency ordering |
| newtrun HOWTO | `docs/newtrun/howto.md` | Writing scenarios, running suites |
| newtlab HLD | `docs/newtlab/hld.md` | VM orchestration, QEMU, bridge networking |
| newtlab LLD | `docs/newtlab/lld.md` | Deploy phases, state persistence, multi-host |
| newtlab HOWTO | `docs/newtlab/howto.md` | Deploying topologies, troubleshooting |
| RCA index | `docs/rca/` | root-cause analyses — SONiC pitfalls and workarounds |
| AI Instructions | `docs/ai-instructions.md` | Behavioral directives scoped by activity phase |
| Editing Guidelines | `docs/editing-guidelines.md` | Documentation prose principles scoped by document type |

When encountering a SONiC-specific issue (config reload, frrcfgd, orchagent, VPP), check `docs/rca/` first — there are 40 documented pitfalls with root causes and solutions.

## First-Run Setup

On the first session in a fresh clone, detect that the project has not been set up
(no `bin/` directory, no SONiC image at `~/.newtlab/images/sonic-vs.qcow2`) and
automatically run the full setup sequence. Give the user status updates as each
step progresses.

1. Check prerequisites: Go 1.24+, KVM (`/dev/kvm`), QEMU (`qemu-system-x86_64`)
2. Build: `make build`
3. Download the SONiC community image:
   ```
   mkdir -p ~/.newtlab/images
   curl -fSL "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.img.gz" \
     | gunzip > ~/.newtlab/images/sonic-vs.qcow2
   ```
4. Deploy a single-switch lab: `bin/newtlab deploy 1node-vs --monitor`
5. Start the server: `bin/newtron-server --spec-dir newtrun/topologies/1node-vs/specs &`
6. Run a first operation to prove it works:
   ```
   bin/newtron switch1 init
   bin/newtron switch1 service apply Ethernet0 transit --ip 10.1.0.0/31 --peer-as 65002
   ```
7. Read `docs/using-claude-with-newtron.md` to orient for ongoing work

If any prerequisite is missing, tell the user what to install and stop — don't
attempt partial setup.

**Definitions in this document are specifications, not suggestions.** When a
section defines a term with precise meaning (e.g., "device is source of
reality," "intent round-trip completeness"), that definition is binding — it
overrides any natural-language interpretation of the phrase. Read the full
definition before applying the concept. If the intuitive reading and the
precise definition conflict, the precise definition wins.

**This file is a derivative, not the authority.** CLAUDE.md summarizes
principles from authoritative documents (`docs/newtron/unified-pipeline-architecture.md`,
`docs/DESIGN_PRINCIPLES_NEWTRON.md`, `docs/ai-instructions.md`). When providing
architectural context to agents, always include the authoritative source — never
paraphrase from CLAUDE.md into agent prompts. When CLAUDE.md and an authoritative
document conflict, the authoritative document is correct and CLAUDE.md is stale.
See `docs/ai-instructions.md` §20 for the full precedence hierarchy.

## Device Is Source of Reality

*See `DESIGN_PRINCIPLES_NEWTRON.md` §1, §5, §19, §20 for the thesis, intent model,
and reconstruction principle.*

In actuated mode, the device's own NEWTRON_INTENT records ARE the authoritative state.
The projection is derived by replaying intents. External CONFIG_DB edits are drift.

Key implications:
- **Intent DB is the decision substrate** for all operational logic (preconditions,
  idempotency, queries). The projection exists solely for delivery and drift detection.
- **Drift guard refuses writes** when device CONFIG_DB diverges from projection.
- **Reconcile eliminates drift** — full (ReplaceAll) or delta (ApplyDrift).
- **Intent records must be self-sufficient** for reverse operations — never re-resolve
  specs at removal time.
- **InitDevice is the one exception** — reads actual CONFIG_DB before any intents exist.
- **No brownfield.** newtron owns the full CONFIG_DB for any node it manages.

## Intent Round-Trip Completeness

*Principle: `DESIGN_PRINCIPLES_NEWTRON.md` §20 (On-Device Intent Is Sufficient for
Reconstruction). This section adds the mechanical verification procedure.*

Every param that affects CONFIG_DB output must complete the full round-trip:

1. **writeIntent** stores it in the intent record
2. **intentParamsToStepParams** exports it to the topology step
3. **ReplayStep** passes it back to the same method

When adding or modifying a `writeIntent` call:

1. List every argument and option field the method uses in CONFIG_DB writes
2. For each one from caller arguments (not profile/spec resolution), verify it
   is stored in the intent params
3. Verify the corresponding `ReplayStep` case reads it and passes it to the method
4. Verify `intentParamsToStepParams` exports it (or the default pass-through handles it)

Values re-resolved from specs or profiles at replay time do NOT need to be stored —
they are re-derived. But values from caller arguments (opts, config structs) MUST be
stored because they are the only source at reconstruction time.

**This is not a guideline — it is a mechanical check. If a param affects CONFIG_DB and
isn't in the intent, that's a bug.**

## Platform Patching Principle

*See `DESIGN_PRINCIPLES_NEWTRON.md` §37 for the full principle and examples.*

**You MAY patch code that has a bug** — fix the broken behavior so it works as the
community intended. **You may NOT reinvent a feature** differently from how the
community intended it to work. The test: does the fix use the same CONFIG_DB signals
and perform the same intended actions? If it introduces a new table or replaces the
community mechanism, it's reinvention.

## Single-Owner Principle for CONFIG_DB Tables (DRY)

*See `DESIGN_PRINCIPLES_NEWTRON.md` §27 for the full principle.*

Each CONFIG_DB table MUST have exactly one owner. Composites call the owning
primitives and merge their ChangeSets rather than constructing entries inline.

Target ownership map:

```
vlan_ops.go        → VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL
vrf_ops.go         → VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT
bgp_ops.go         → BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF,
                      BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, DEVICE_METADATA,
                      BGP_PEER_GROUP, BGP_PEER_GROUP_AF
evpn_ops.go        → VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP,
                      SUPPRESS_VLAN_NEIGH, BGP_EVPN_VNI
acl_ops.go         → ACL_TABLE, ACL_RULE
qos_ops.go         → PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP,
                      SCHEDULER, WRED_PROFILE
interface_ops.go   → INTERFACE
baseline_ops.go    → LOOPBACK_INTERFACE
portchannel_ops.go → PORTCHANNEL, PORTCHANNEL_MEMBER
intent_ops.go      → NEWTRON_INTENT
service_ops.go     → ROUTE_MAP, PREFIX_SET, COMMUNITY_SET
```

When adding new CONFIG_DB writes, always check the ownership map — never add a second writer.

## The Interface Is the Point of Service

*See `DESIGN_PRINCIPLES_NEWTRON.md` §6 for the full principle.*

The interface is the point of service delivery, unit of lifecycle (apply/remove/refresh),
unit of state (one binding or none), and unit of isolation. `ApplyService` lives on
Interface, not Device. Interface delegates to Device for infrastructure.

## Respect Abstraction Boundaries

*See `DESIGN_PRINCIPLES_NEWTRON.md` §30 for the full principle.*

When an abstraction exists (Interface, Node), callers MUST use it. Interface-scoped
operations are methods on Interface. Config methods belong to the object they describe.
Node convenience methods delegate, not duplicate. Abstractions > raw code efficiency.

## Pipeline-First Explanations

When explaining how something works, describe its position in the pipeline: what
feeds it, what it feeds, and which stage it belongs to. Never describe a component
in isolation. The pipeline reference is `docs/newtron/unified-pipeline-architecture.md`.
One pipeline: Intent → Replay → Render → [Deliver].

## Domain-Intent Naming

*See `DESIGN_PRINCIPLES_NEWTRON.md` §32 for the full principle.*

Function names describe **domain intent**, not implementation. CONFIG_DB concepts
("Entry", "Sub", "Config") belong in comments, not names. Examples: `bindVrf` not
`interfaceBaseConfig`; `assignIpAddress` not `interfaceIPSubEntry`.

## Abstract Node — Same Code Path, Three States

*See `DESIGN_PRINCIPLES_NEWTRON.md` §1, §2 for the thesis and properties.
See `docs/newtron/unified-pipeline-architecture.md` §8 for projection rebuild.*

The Node operates in three states — same code path, different initialization:

- **Topology offline**: from topology.json, projection starts empty
- **Topology online**: connected, projection from intent replay
- **Actuated online**: intents from device's NEWTRON_INTENT, drift guard active

Key implementation entry points:
- `NewAbstract()` — empty projection, no actuatedIntent
- `InitFromDeviceIntent()` — actuated mode from device NEWTRON_INTENT
- `RebuildProjection(ctx)` — called in `execute()` before every operation
- `Execute(ctx, opts, fn)` — Lock/Unlock wrapper, dry-run via intent snapshot/restore
- `Reconcile()` — full (ExportEntries + ReplaceAll) or delta (Drift + ApplyDrift)

## Separation of Concerns — File-Level Ownership

*See `DESIGN_PRINCIPLES_NEWTRON.md` §28 for the full principle.*

A reader should guess where a feature is implemented by looking at file names.
`topology.go` = orchestration (never inline CONFIG_DB keys). Each `*_ops.go` = sole
table owner. `service_gen.go` = service-to-entries translation via owning `*_ops.go`.

## Verb-First Naming

*See `DESIGN_PRINCIPLES_NEWTRON.md` §32 (includes §16 verb vocabulary).*

Action names MUST put the verb first: `createVlan` not `vlanCreate`. Verb vocabulary:
create/delete/destroy/enable/disable/bind/unbind/assign/unassign/update/generate.
Noun-only names are reserved for types, constructors, and key helpers.

## Operational Symmetry

*See `DESIGN_PRINCIPLES_NEWTRON.md` §15 for the full principle.*

Every forward action MUST have a reverse. Forward + reverse added in the same commit.
Reverse operations must be **reference-aware** — scan for remaining consumers before
deleting shared resources. Baseline operations (`setup-*`, `set-*`) are the sole
exception: their collective reverse is Reconcile.

## Public API Boundary Design

*See `DESIGN_PRINCIPLES_NEWTRON.md` §33 for the full principle.*

`pkg/newtron/` is the public API; `network/`, `network/node/`, `device/sonic/` are
internal. All external consumers import only `pkg/newtron/`. Public types use domain
vocabulary; operations accept names (strings), API resolves specs internally. When
adding new API surface: define public type in `types.go`, add method to the
appropriate wrapper, convert internal types at the boundary.

## Redis-First Interaction Principle

*Principle: `DESIGN_PRINCIPLES_NEWTRON.md` §4. This section adds the tagging procedure.*

All device interaction MUST go through SONiC Redis databases. When CLI/SSH is
unavoidable, tag the call site:

```go
// CLI-WORKAROUND(id): <what this does>.
// Gap: <what Redis/SONiC lacks>.
// Resolution: <what upstream change would eliminate this>.
```

- **Workaround** — Redis could provide this but doesn't today. Tag with `CLI-WORKAROUND`.
- **Inherent** — Will always require CLI (e.g., `config save`, `docker restart`). No tag needed.

Before adding any `session.Run()` or `ExecCommand()`: check Redis first, use the
Redis path if available, tag the CLI path if not.

## CONFIG_DB Replace Semantics (DEL+HSET)

Redis `HSET` merges fields into an existing hash — it does NOT remove old fields.
Any operation that replaces a key's content (RefreshService, re-provisioning) MUST
`DEL` the key first, then `HSET` the new fields. Without the `DEL`, stale fields
from the previous state persist as ghost data.

This has two consequences:

- **Apply must preserve delete+add sequences.** When a ChangeSet contains both a
  delete and a subsequent add for the same key (e.g., RefreshService = remove + apply),
  both operations must be sent to Redis in order. SONiC daemons see the delete
  notification (tear down old state), then the add notification (create new state).
  Stripping the intermediate delete — as the former `DeduplicateRefresh` did — leaves
  stale fields and prevents daemons from cleaning up internal state.

- **Verification checks final state only.** When verifying a merged ChangeSet,
  `verifyConfigChanges` computes the last operation per key. A key that was deleted
  then re-added is verified as "should exist with new fields" — not as "should be
  deleted". The apply sequence handles intermediate state; the verifier only cares
  about the end result.

## CONFIG_DB Schema Validation (YANG-Derived)

*Principle: `DESIGN_PRINCIPLES_NEWTRON.md` §13. This section adds the YANG workflow
and hydrator completeness rule.*

Schema is fail-closed — unknown tables/fields are errors. YANG is the authority for
value constraints. When adding a new CONFIG_DB table or field:

1. Fetch the YANG model from `sonic-yang-models/yang-models/`
2. Add to `schema.go` with a `// YANG:` comment citing the source
3. Update `yang/constraints.md` with the new table/field
4. Add test cases in `schema_test.go`

**Hydrator field completeness.** Every field written by a config generator must exist
in three places: `schema.go` (validation), the typed struct in `configdb.go`
(representation), and the hydrator in `configdb_parsers.go` (wire → struct). A field
missing from the hydrator is silently dropped during projection rebuild, causing false
drift on a correctly-configured device. Missing any one is a bug.

## CONFIG_DB Write Ordering and Daemon Settling

*See `DESIGN_PRINCIPLES_NEWTRON.md` §18 for the full principle, dependency chains,
and the RCA-037 narrative.*

Config functions return entries in dependency order (parents before children). Reverse
operations delete in opposite order. Never use `time.Sleep` between writes — ordering
is structural. Daemon settling is verified by `pollUntil` polling, not sleeps. Daemon
races are documented as RCAs.

## Unified Naming Convention for CONFIG_DB Keys

*See `DESIGN_PRINCIPLES_NEWTRON.md` §36 for rationale.*

ALL UPPERCASE, `[A-Z0-9_]` only. Hyphens → underscores. No redundant kind in key
(table name carries it). Numeric IDs concatenated: `VNI1001`, `VLAN100`.

## Normalize at the Boundary

*See `DESIGN_PRINCIPLES_NEWTRON.md` §36 for rationale.*

Names normalized **once, at spec load time**. After loading, all map keys, cross-references,
and CONFIG_DB key names are canonical. Operations code never calls `NormalizeName()`.

## Definition Is Network-Scoped; Execution Is Device-Scoped

*See `DESIGN_PRINCIPLES_NEWTRON.md` §7 for the full principle.*

Specs exist at the network level, independent of any device. `ResolvedSpecs` is a
per-node snapshot; `Get*` methods must fall through to `network.Get*` on miss.
In newtrun, network-level steps call `r.Client.*` directly (no `devices:` field).

## Policy vs Infrastructure — Shared Object Lifecycles

*See `DESIGN_PRINCIPLES_NEWTRON.md` §24, §25 for the full principle and content-hashed
naming mechanism.*

Policy objects (ACL_TABLE, ROUTE_MAP, PREFIX_SET, COMMUNITY_SET) are created on first
reference and deleted when the last consumer is removed. Content-hashed naming: 8-char
SHA256 of generated fields in the key name. Dependent objects use bottom-up Merkle hashing.

## BGP Peer Groups — Native Sharing Mechanism

*See `DESIGN_PRINCIPLES_NEWTRON.md` §26 for the full principle.*

Service BGP neighbors reference a `BGP_PEER_GROUP` named after the service. Peer groups
are created on first `ApplyService` and deleted with the last consumer. Topology-level
underlay peers do NOT use peer groups.

## Allowed Commands

These are routine project commands that do not require confirmation:

### Go Toolchain
- `go build -o bin/<tool> ./cmd/<tool>`
- `go test ./... -count=1` (and per-package variants)
- `go vet ./...`
- `go run`, `go mod tidy`, `go get`, `go list`, `go doc`, `go version`

### Git
- `git status`, `git diff`, `git log`, `git add`, `git commit`, `git push`
- `git mv`, `git rm`, `git format-patch`, `git reset`, `git am`

### Project Binaries
- `bin/newtlab`, `bin/newtron`, `bin/newtrun`, `bin/newtlink` (all subcommands)

### Make
- `make build`, `make test`, `make lint`, `make tools`

### Misc
- `ls`, `stat`, `file`, `wc`, `chmod`, `ln`
- `pgrep`, `pkill`, `ps`
- `ssh`, `sshpass`, `ssh-keygen`, `nc`, `socat`, `curl`, `ping`
- `qemu-img info`, `qemu-img convert`

### Web Access
- `WebSearch` (always allowed)
- `WebFetch` for: `github.com`, `raw.githubusercontent.com`, `containerlab.dev`, `hackmd.io`, `sonic.software`, `deepwiki.com`, `r12f.com`

## Diagrams

ASCII diagrams in markdown files are generated with `graph-easy`, never hand-drawn.

### Setup

- Install: `make tools` (installs graph-easy to `~/perl5/`; works on Linux and macOS)
- Source files: `docs/diagrams/*.dot` (Graphviz DOT syntax)
- Render: `graph-easy --from=dot --boxart < file.dot`

### Workflow

1. Edit the `.dot` source file in `docs/diagrams/`
2. Render with `graph-easy --from=dot --boxart < file.dot`
3. Paste the rendered output into the markdown code block
4. Commit both the `.dot` source and the rendered output

### Box Padding

Graph::Easy has no padding attribute. Control box size with whitespace in node names:

- **Vertical padding**: `\n` adds blank lines above/below the label.
  Use `\n` before the text for top padding, `\n\n` after for bottom padding.
- **Horizontal padding**: spaces inside the label widen the box.
- **Standard pattern**: `"\n  Label  \n\n"` gives 1 blank line top, 1 blank line
  bottom, 2 spaces on each side.
- **Multiline labels**: `"\n  Line 1  \n  Line 2  \n\n"` (e.g., newtron + (client)).
- All boxes in a diagram should use the same padding pattern for consistency.

### Layout Control

Graph::Easy supports layout hints that DOT alone cannot express. When the DOT
`rankdir` and edge ordering aren't sufficient, use Graph::Easy's native syntax
(`.ge` files) with these attributes:

- **Same-row placement**: `[ A ] { rank: same; } [ B ] { rank: same; }`
- **Edge port control**: `[ A ] --> { start: east; end: west; } [ B ]` forces
  a horizontal connection (right side of A to left side of B).
  Compass directions: `north`, `south`, `east`, `west`.

### Rules

- Never hand-draw ASCII art — the tool handles all alignment.
- Every diagram in a markdown file must have a corresponding source file in
  `docs/diagrams/`. The source file is the record of intent; the rendered
  ASCII in the markdown is the output.

## Build Convention

Always `go build -o bin/<tool> ./cmd/<tool>` before testing — `go run` compiles to a temp dir and breaks sibling binary resolution.

## Static Analysis

golangci-lint is not installed. Use `go vet` for static analysis.

## Model Routing

Use the primary model (Opus) for:
- Architectural decisions, audits, and planning
- Determining what to change and why
- Code review and correctness reasoning

Dispatch subagents with `model: "sonnet"` for:
- Applying known edits across files (renames, import path updates, deletions)
- Running build/test/commit cycles
- Grep/read research tasks with clear search criteria
- Doc updates where the changes are already specified

## Greenfield — No Backwards Compatibility

*See `DESIGN_PRINCIPLES_NEWTRON.md` §40, §41 for the full principle.*

No compatibility shims, no API versioning, no deprecated aliases. Delete, don't
deprecate. Exception: multi-SONiC-release support is multi-platform, not backwards
compatibility (§41).

## Regression Prevention

**Never break a feature that was previously working.**

Before making any change to `service_gen.go`, `*_ops.go`, or any shared code path:
1. List which service types and test scenarios exercise that code path
2. Verify that changes to the broken path do not affect the working paths
3. Run `go test ./...` after every change and confirm all previously passing tests still pass
4. If a change is required that affects a shared code path, explicitly document which
   working features are at risk and how they are protected

Tracking what was working (update this as test suites are validated):
- `evpn-bridged`: WORKS — 2node-ngdp-primitive, 3node-ngdp-dataplane (evpn-l2-irb L2 path)
- `routed`, `irb`, `bridged`: WORKS — 2node-ngdp-primitive
- `evpn-irb`: WORKS — 3node-ngdp-dataplane evpn-l2-irb (L2 + L3 inter-subnet via asymmetric IRB)
- `evpn-routed`: ABANDONED on CiscoVS/Silicon One (RCA-039, L3VNI DECAP blocked)
- `CLI lifecycle (1node-vs-config)`: WORKS — 13/13 scenarios (loopback mode, Apr 2026)
- `1node-vs-architecture`: VALIDATED — 32/32 PASS (Apr 2026)
- `2node-vs-primitive`: VALIDATED — 21/21 PASS (Apr 2026)
- `2node-vs-service`: VALIDATED — 6/6 PASS (Apr 2026)

## Feature Implementation Protocol (SONiC CONFIG_DB)

*See `DESIGN_PRINCIPLES_NEWTRON.md` §38 for the full principle and rationale.*

Before writing CONFIG_DB entries for a SONiC feature: (1) find the SONiC CLI path and
read its source, (2) configure via CLI on a real device and capture CONFIG_DB ground
truth, (3) read the daemon source for processing order, (4) implement the same entries
in the same order, (5) create a targeted newtrun suite before composite integration.
**Never assume a CONFIG_DB path works without CLI verification on a real device.**

## Model Escalation

If using Claude Sonnet and no resolution is reached within 15 minutes, switch to
Claude Opus 4.6 (model ID: `claude-opus-4-6`) for architectural decisions and debugging.

## Testing Protocol

*See `DESIGN_PRINCIPLES_NEWTRON.md` §42 for the full principle.*

Always start on a freshly deployed topology. Polling checks must not pass vacuously
(zero items = keep polling). Test timeouts must account for CONFIG_DB entry count.

## Documentation Freshness Protocol

When updating docs after schema/API changes (renamed fields, new types, changed CLI flags):

1. **Don't rely on targeted grep alone.** Grepping for known stale patterns (`"l3_vni"`,
   `--import-rt`) gives false confidence — it misses prose descriptions, glossary tables,
   Go code examples, incomplete flag lists, and contradictory statements.

2. **After the initial fix pass, dispatch a full-file audit agent** that reads the entire
   document end-to-end and checks every reference against the ground truth schema. Provide
   the complete ground truth (all field names, all CLI flags, all type values) so the agent
   can catch staleness in any form — not just the patterns you already know are wrong.

**CLAUDE.md freshness**: After updating an architecture doc, verify that the CLAUDE.md
sections summarizing the updated content still match. CLAUDE.md is a derivative (see
meta-instruction at top of file). An architecture doc revision that adds mechanisms,
renames concepts, or changes the pipeline model makes CLAUDE.md stale unless both are
updated together. This is a step in the architecture update workflow — not a deferred
TODO. See `docs/ai-instructions.md` §20.

3. **Fix everything the audit finds before committing.** One commit, fully clean.

## User Preferences

- Never compact away the last 5 prompts and responses during context compression.
- When agents are running, include a status line in each response showing count, model (opus/sonnet), and status. Example: **Agents: 2 running** (1 opus, 1 sonnet) / 3 completed. Omit when no agents are active.
