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
| newtlab API | `docs/newtlab/api.md` | newtlab-server HTTP endpoint reference |
| newt-server | `docs/newt-server.md` | Aggregated HTTP entry point — composes the three engines on one port |
| RCA index | `docs/rca/` | root-cause analyses — SONiC pitfalls and workarounds |
| AI Instructions | `docs/ai-instructions.md` | Behavioral directives scoped by activity phase |
| Code Review | `docs/code-review.md` | Procedural rules for diff-review tasks — angles, false-positive filter, confidence rubric |
| Editing Guidelines | `docs/editing-guidelines.md` | Documentation prose principles scoped by document type |

When encountering a SONiC-specific issue (config reload, frrcfgd, orchagent, VPP), check `docs/rca/` first — there are 45 documented pitfalls with root causes and solutions.

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
   curl -fSL "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=202505&platform=vs&buildId=1057313&target=target/sonic-vs.img.gz" \
     | gunzip > ~/.newtlab/images/sonic-vs.qcow2
   ```

   (Pin to the `202505` stable branch — the same image `scripts/getting-started.sh` uses. The `master` build at any given moment may fail to boot or include behavior that hasn't been validated by the newtrun suites.)
4. Start the aggregated server: `bin/newt-server &` (runs newtron + newtrun + newtlab engines in one process on `:18080`; auto-discovers every `networks/<name>/topology.json` and registers each as a network; see [`docs/newt-server.md`](docs/newt-server.md)). Server must start before the deploy so `bin/newtlab deploy --monitor` can read live link telemetry — `newtlink` pushes per-link byte counters to newt-server every 5 seconds and the deploy monitor renders them. Wait ~2 seconds after the `&` then verify with `kill -0 <pid>` before proceeding.
5. Deploy a single-switch lab: `bin/newtlab deploy 1node-vs --monitor`
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
Reconstruction). Enforcement is machine-checked — this section says where.*

Every caller param that affects CONFIG_DB must survive the round-trip
(writeIntent → IntentsToSteps → ReplayStep); values re-resolved from specs at
replay time are re-derived, not stored. This is no longer a manual checklist:

- **The manifest** lives in `pkg/newtron/network/node/op_registry.go` — one
  `OpSpec` per operation declares its params (caller vs recorded), inverse
  verb, scope, replay, and export. The registry is the single owner of
  operation-level knowledge; there is no ReplayStep switch to keep in sync.
- **The enforcement** is `TestOpRoundTrip` (node package): drives every
  registered op with all caller params non-default, exports, replays onto a
  fresh node, and requires exact intent-DB + projection equality, manifest
  conformance, and export idempotence.

When adding an operation: write the op method (with `writeIntent`), add its
`OpSpec` to the registry, and add it to the round-trip sequence — the coverage
guard fails until you do, and a dropped param fails as a field-level diff.

## Platform Patching Principle

*See `DESIGN_PRINCIPLES_NEWTRON.md` §37 for the full principle and examples.*

**You MAY patch code that has a bug** — fix the broken behavior so it works as the
community intended. **You may NOT reinvent a feature** differently from how the
community intended it to work. The test: does the fix use the same CONFIG_DB signals
and perform the same intended actions? If it introduces a new table or replaces the
community mechanism, it's reinvention.

## Single Owner Per Data Object

*See `DESIGN_PRINCIPLES_NEWTRON.md` §27 for the full principle.*

Every data object that admits no multi-writer story has exactly one owner.
A CONFIG_DB table is one kind of data object; spec files, lab runtime state,
and test-run state are others. Two writers operating against an implicit
shared schema diverge silently the moment either evolves; the inconsistency
surfaces later, somewhere unrelated.

**CONFIG_DB tables — ownership map:**

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

**Cross-engine data objects:**

- **Specs** (network.json, topology.json, platforms.json, nodes/*.json, zones/*.json) — newtron is the owner; consumers reach the data through `/newtron/v1/networks/...`. Opening the JSON files from another engine is the §27 violation.
- **Lab runtime state** (LabState, NodeState, LinkState) — newtlab is the owner; consumers reach the data through `/newtlab/v1/labs/...`. Reading `~/.newtlab/labs/<name>/state.json` from another engine is the §27 violation.
- **Test run state** — newtrun is the owner; consumers call `/newtrun/v1/runs/...`.

When adding new state of any kind — new CONFIG_DB writes, new spec fields, new runtime allocations — check who already owns it. Never add a second writer.

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
table owner. Service application (`ApplyService` in `service_ops.go`) delegates
to the owning `*_ops.go` files for the per-table entries it needs.

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

Forward/reverse is one of three symmetry axes §15 covers ("Symmetry is an axis, not
a direction"): every write also needs a matching read (a dimension or field on a
write must reach every read/list/query — see `ai-instructions.md` §24), and every
load-time check needs a matching write-time check (the writer rejects what the
loader would reject, from one shared validator).

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

## CONFIG_DB Replace Semantics — Teardown vs In-Place

*Principle: `DESIGN_PRINCIPLES_NEWTRON.md` §48 (In-Place Update Is Delivered In
Place). This section adds the mechanics.*

A CONFIG_DB row-replace is one of two **opposite intents**, and the delivery
must match the intent (§48) — a SONiC daemon re-reads the key on each keyspace
notification, so whether it ever observes the key **absent** determines whether
state is torn down.

- **Teardown-replace** (`RefreshService`, re-provisioning) uses `DEL`+`HSET`:
  Redis `HSET` merges into an existing hash and does not remove old fields, so
  the `DEL` is required both to clear ghost fields AND so the daemon **sees the
  removal** and cleans up its internal state. Stripping the delete — as the
  former `DeduplicateRefresh` did — leaves ghosts and strands daemon state. Apply
  preserves the delete+add order.

- **In-place update** (`update-*`) uses `cs.Replace` — a **field diff** against
  the current row (`HSET` changed fields, `HDEL` removed fields, **never `DEL`
  the key**). The key is never absent, so the daemon observes an edit, not a
  remove+add: no BGP session flap, no FIB gap (measured — see `docs/rca/048`).
  Do NOT "fix" this by making `ChangeSet.Apply` batch same-key DEL+ADD atomically
  — that would silently make teardown-replace hitless and strand daemon state.
  The intent lives in the caller's verb (`cs.Replace` vs `cs.Deletes`+`cs.Adds`),
  not in an apply-layer heuristic.

- **Verification checks final state only.** `verifyConfigChanges` computes the
  last operation per key; a key deleted then re-added is verified as "should
  exist with new fields," not "should be deleted." (A `cs.Replace` field diff
  verifies the same way — the row should exist with the new fields.)

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

*See `DESIGN_PRINCIPLES_NEWTRON.md` §7 for the full principle, including the
network-floor invariant for scoped writes.*

Specs exist at the network level, independent of any device. `ResolvedSpecs` is a
per-node snapshot; `Get*` methods must fall through to `network.Get*` on miss.
In newtrun, network-level steps call `r.Client.*` directly (no `devices:` field).

**Scoped writes (network-floor invariant).** Specs are authored at network, zone,
or node scope via `scope`/`scope_instance` on the write endpoints (absent ⇒
network) — "flat at the boundary, hierarchical underneath." A resource may exist
at zone/node scope **only if it also exists at network scope** (an override rests
on a base), which keeps resolution total — no reference dangles from any device's
perspective. Consequences: forward ref checks stay network-scoped (unchanged);
deleting an override is free (consumers fall back to the base); deleting the
network base, or a zone/profile still holding overrides, is refused while
referenced anywhere or overridden below (`*util.ConflictError` → 409, bottom-up
per §15). Scope is read-only provenance on `GET /spec-instances` and is declared
in the schema (`scope` enum + scope-conditional `scope_instance` ref).

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
- `bin/newtlab`, `bin/newtlab-server`, `bin/newtron`, `bin/newtron-server`, `bin/newtrun`, `bin/newtrun-server`, `bin/newt-server`, `bin/newtlink` (all subcommands)

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

Before making any change to `service_ops.go`, `*_ops.go`, or any shared code path:
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
- `1node-vs-config`: VALIDATED — 25/25 PASS (loopback `--no-deploy`, ~2s); re-run **2026-07-02 against main @ post-#366** (the DRY sweep #356–#366) as ron with 12 identities cached — 25/25, confirming the auth/client/URL-resolution changes cause no regression on the CLI-config path; prior re-run **2026-07-01 against main @ post-#347** (per-file zones #346 + the `node`/`zone` CLI `-x` fix #347 landed) as ron under the enforced + `--audit`-enabled server — confirms per-file zones (`zones/<zone>.json`) cause no regression; original 2026-06-20 against main @ 7560efd. Re-run green at every step of the register-network → IPVPN/VRF-collapse → global-platforms arc (#245, #251, #249, #250, #252, #253, #256, #257, #258, #259). The suite grew from the original 13 to 25 scenarios; this is the current full set. Loopback mode has no cold-deploy/ASIC-convergence exposure (no real switch), so it's the fast regression gate for spec/wire-shape changes. **Running it under enforcement (two operational notes):** (1) *Operator identity.* Submit the run with `NEWTRON_USER=<user>` (e.g. ron) to select the operator among cached sessions. The old hard "multiple cached sessions" CLI bail is gone — since #357 an ambiguous cache resolves to an *empty* Bearer, not an error — but under `--enforce-authorization` an empty Bearer 401s, so an explicit `NEWTRON_USER` is still how you pick the operator when more than one session is cached. The suite's own `newtron-cli` action needs no session cache at all: the runner forwards the run's operator (or a scenario's `as:`) Bearer to the server-side exec via `NEWTRON_BEARER` (`Runner.scenarioBearer`, steps_cli.go). (2) *Binary on PATH.* That exec resolves the `newtron` binary from newt-server's `$PATH`; symlink the fresh build on (`ln -sf "$PWD/bin/newtron" ~/go/bin/newtron` — `~/go/bin` is already on PATH, resolved fresh per call, no restart needed) or the 2 `newtron-cli` scenarios (setup-device, topology-show) fail with `exec: "newtron": … not found` while the 9 HTTP scenarios pass and 14 dependents skip → a 9/2/14 split. Failing steps stream their name + message in the live summary since #417; `--junit out.xml` still carries the full untruncated detail.
- `1node-vs-architecture`: VALIDATED — 32/32 PASS (re-run 2026-05-31 against main @ ec16631 with bin/newt-server on :18080; 47m33s on the deployed 1node-vs lab)
- `2node-vs-primitive`: VALIDATED — 23/23 PASS **cold-deploy** (9m1s, 2026-07-06 against main @ post-#417, suite now 23 scenarios: the §48 dataplane continuity checks landed — 34-dataplane-update-bgp-peer (monotonic-uptime witness) and 36-dataplane-update-acl-rule (zero-loss drop-budget soak with a DROP backstop) — plus the #417 runner-edge adoptions live in the suite (cleanup: blocks, host-exec poll, device-scoped capture). Ran green cold 4× during the #415–#418 arc. The static-route and evpn-peer continuity checks do NOT live here: runtime STATIC_ROUTE creation needs the RCA-044 restart-replay and a second mid-suite bgp restart crash-loops bgp.service (static check → 2node-ngdp-primitive); the evpn witness is withdrawn entirely (RCA-049 addendum). Prior record: 21/21 PASS cold (7m26s, 2026-06-20 against main @ post-#261, Force10-S6000_vs, host shared with 3 labs / 19 VMs). First cold-deploy validation recorded against the global-platforms layout (#257 renamed the platform `sonic-vs` → `Force10-S6000_vs`; nodes updated). This run surfaced and fixed RCA-047: under heavy host contention a cold orchagent's SAI throughput slips past the `bridged` scenario's 60s ASIC poll; the fix (#261) raised that poll's budget to 180s (polls exit early on success, so warm runs are unaffected). **Process note (§42):** validate cold — `newtrun start` deploys the topology itself (lifecycle mode), so a warm re-run on an already-deployed lab masks the cold ASIC race and returns a false pass. Previously validated 21/21 on 2026-06-09 (5m47s, lighter host → the 60s budget held) against `feat/network-id-from-suite-topology` for #116. Validates the per-suite/per-lab network ID change: `newtlab deploy 2node-vs` registers newtron network as `id="2node-vs"` (not `"default"`); `newtrun start 2node-vs-primitive` derives `NetworkID="2node-vs"` from `suite.Network`; both reach the same registered slot end-to-end. Also surfaced a layering issue not captured in the original #116 design — newtlab-server held a single fixed-id newtron client at startup, so it couldn't serve per-lab IDs; fixed by changing `Config.NewtronClient` to `Config.NewtronClientFor func(networkID) SpecClient`, plus a fallback in `cmd/newtlab/main.go` `resolveTarget` for already-deployed labs whose `state.Dir` was empty. Previously validated 21/21 on 2026-06-07 against main @ 9139fd7 (post-#112 URL pluralization).
- `2node-vs-service`: VALIDATED — 7/7 PASS, twice consecutively (run 1: 2026-06-07 21:33, 4m50s; run 2 confirmation: 2026-06-08 19:22, 4m41s; both against main @ ffe03eb + scenario fixes in this commit, bin/newt-server :18080). The other six scenarios (boot-ssh, provision, verify-health, dataplane, deprovision, verify-clean) have been validated many times and were not affected by the URL pluralization (#113). The 7th scenario, rollout-admin-status, was added 2026-06-04 in PR #74 as a demonstration of the parameterized-scenario feature and never validated end-to-end against the dependency chain; the post-pluralization run surfaced two gaps in its design: (1) no `requires:` field, so the topo-sort ran it before `provision` (writeIntent: parent "device" does not exist); (2) no reverse step for the admin_status property it sets (deprovision: deleteIntent has children — §15 violation). Both fixed in this commit. The CLAUDE.md "6/6 PASS, 2026-05-31" measurement predates the 7th scenario's addition by four days; this twice-confirmed 7/7 is the first record of the 7-scenario suite running clean end-to-end.
- `2node-ngdp-primitive`: VALIDATED — 22/22 PASS **cold-deploy** (9m20s, 2026-07-06 against main @ post-#417, CiscoVS cisco-p200). Suite gained 35-dataplane-update-static-route (§48 zero-loss drop-budget soak; lives here because split of platform duties is empirical — see the 2node-vs entry) with the #417 cleanup:/poll adoptions. Ran green cold 3× during the #415–#418 arc. Note: these CiscoVS nodes run **unified frrcfgd** (`docker_routing_config_mode: unified` in the topology), not split bgpcfgd — RCA-044's split-mode framing does not describe this repo's fabrics (verified during RCA-049).
- `3node-ngdp-dataplane`: VALIDATED — 8/8 PASS **cold-deploy** (9m4s, 2026-07-06 against main @ post-#418, with the RCA-049 fixes live: canonical-struct decode in the update handlers + the BGPNeighborExists projection leg). This fabric's overlay is **leaf1↔leaf2 by design** (nodes/leaf{1,2}.json `evpn.peers`; the spine is underlay-only transit with NO VTEP/loopback overlay) — do not author tests that add/remove overlay peers here; every loopback pair is profile-owned and AddBGPEVPNPeer now refuses adoption (§27).
- `1node-vs-auth`: VALIDATED — 35/36 PASS / 0 FAIL / 1 SKIP (L2c-round-trip by design); re-run **2026-07-01 against main @ post-#348** (per-file zones #346 + node/zone CLI `-x` #347) — confirms per-file zones (`zones/<zone>.json`) cause no regression on the full L0→L6 auth arc; ran twice consistently, seconds each (loopback), under the running `--enforce-authorization --auth-pam-service newtron-test --audit --audit-integrity --spec-watch` server (extra global `--super-users aldrin,ron --dev-superuser=false` is harmless — `root` is super via the network's own `super_users:[root]`). Needs `newtron` on the server's PATH for the L6 `newtron-cli` audit-verify exec (`ln -sf "$PWD/bin/newtron" ~/go/bin/newtron`; see the config-suite caveat above). Original 2026-06-20 against main @ post-#255, `bin/newt-server --networks-base ... --platforms-base ... --audit --auth-pam-service newtron-test --enforce-authorization --audit-integrity --spec-watch`, completes in seconds (loopback). This run validated the IPVPN/VRF-name collapse follow-up (#255): the suite's 75-L4-vrf-permission-families scenarios were updated to the single-name bind-ipvpn wire shape (IPVPN name IS the SONiC VRF name, `^Vrf[A-Za-z0-9_]*$`), and a stale `/profiles` → `/nodes` route in 00-L0-secret-store-resolves (which had been 404-ing and skipping the whole dep chain) was fixed in the same PR. Requires `--audit` set for the L4-audit-read-gated scenarios; without it they 404. (Flag renamed from `--audit-log PATH` in #341 — audit is per-network now, no path.) Prior baseline: 32/33 on 2026-06-16 against main @ post-#192. **First end-to-end validation of this suite in the project's history** — the suite was authored in PR #139 (May 2026) but the actual run that PR #182 forced surfaced multiple pre-existing bugs (canonical-form where-patterns + the `bin/newtrun start` Bearer-forwarding gap closed in PR #186) plus added 5 new scenarios via PR #181 + 3 via PR #188. The 33 scenarios cover the full L0→L6 arc plus the new L4-vrf-families (vrf.bind/vrf.route/bgp.peer), L5-service-dimension (where:{service:"X"} on gateService-routed permissions), and L4-auth-read-gated (engage-when-configured `auth.read` permission). Reproducible via the 3-command operator-friendly recipe in `networks/1node-vs-auth/suites/1node-vs-auth/README.md` §Validation.
- `1node-vs-auth-deployed`: VALIDATED — 3/3 PASS, re-run **2026-06-30 against main @ post-#343** (the per-network audit arc #340–#343 landed) with `bin/newt-server --enforce-authorization --auth-pam-service newtron-test --audit --audit-integrity --super-users aldrin,ron --dev-superuser=false --spec-watch`, <1s on the already-deployed 1node-vs lab; first validated 3/3 on post-#336 (#337). Added in #337 as the **actuated** counterpart to `1node-vs-auth`: where that suite is loopback (`--no-deploy`, every scenario `?mode=topology` so the gate fires before any transport) and therefore never crosses the newtron→newtlab boundary, this one runs WITHOUT `--no-deploy` so device ops force SSH-port resolution from newtlab over HTTP loopback under PAM — the exact cross-engine path #335's internal service token fixed, previously guarded by nothing. Three scenarios: `00-cross-engine-ssh` (as ron, actuated `ssh-command echo ok` round-trips → internal newtlab lookup authenticated; a regressed service token surfaces as `newtlab (401)`), `10-cross-engine-health` (as ron, actuated `GET /health` connects + returns `oper_checks`), `20-enforcement-live` (as mallory/ungranted, actuated write DENIED → enforcement live, so 00/10 aren't vacuous). Non-mutating on the device (00/10 read; 20 denied pre-transport) and `EnsureTopology` reuses a running lab, so it's safe against an already-provisioned switch1 and doesn't need a cold deploy. Cache identities first: `sh networks/1node-vs/suites/1node-vs-auth-deployed/login-users.sh`; run `NEWTRON_USER=ron bin/newtrun start 1node-vs-auth-deployed`. Recipe in that suite's README.md §Validation. **Post-#343 the run also confirms per-network audit end-to-end on the actuated path:** the 4 events (ron ALLOW `ssh-command` + `authcheck:device.write`; mallory DENY `create-vlan` + `authcheck:vlan.create`) all land in `networks/1node-vs/audit/audit.log` with `network=1node-vs`, and `bin/newtron -N 1node-vs audit verify` reports the hash chain clean (4 entries) — proving both mutation and decision events route to the network's own log, not just under loopback. (Cross-engine calls in newt-server are HTTP loopback, not in-process Go calls — the #335 401 is the proof; #337 also corrected the auth-design.md/newt-server.md passages that still claimed in-process.)

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
