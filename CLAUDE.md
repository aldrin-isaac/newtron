# Newtron Project — Claude Code Instructions

## Project Documentation

Read these before making design decisions or writing code in unfamiliar areas:

| Document | Path | Contents |
|----------|------|----------|
| newtron HLD | `docs/newtron/hld.md` | Architecture, verification primitives, Redis interaction model |
| newtron LLD | `docs/newtron/lld.md` | Type definitions, method signatures, package structure |
| Device LLD | `docs/newtron/device-lld.md` | CONFIG_DB/APP_DB/ASIC_DB/STATE_DB layer, SSH tunneling, ChangeSets |
| newtron HOWTO | `docs/newtron/howto.md` | Operational patterns, provisioning flow |
| newtest HLD | `docs/newtest/hld.md` | E2E test framework design |
| newtest LLD | `docs/newtest/lld.md` | Step actions, suite mode, dependency ordering |
| newtest HOWTO | `docs/newtest/howto.md` | Writing scenarios, running suites |
| newtlab HLD | `docs/newtlab/hld.md` | VM orchestration, QEMU, bridge networking |
| newtlab LLD | `docs/newtlab/lld.md` | Deploy phases, state persistence, multi-host |
| newtlab HOWTO | `docs/newtlab/howto.md` | Deploying topologies, troubleshooting |
| RCA index | `docs/rca/` | 18 root-cause analyses — SONiC pitfalls and workarounds |

When encountering a SONiC-specific issue (config reload, frrcfgd, orchagent, VPP), check `docs/rca/` first — there are 18 documented pitfalls with root causes and solutions.

## Platform Patching Principle

When a platform (CiscoVS, VPP, etc.) has a bug that prevents a SONiC feature from working:

- **You MAY patch code that has a bug** — fix the broken behavior so it works as the community intended.
- **You may NOT reinvent a feature differently from how the community intended it to work.**

Concretely: if frrcfgd's `vrf_handler` has a bug that prevents it from programming VNI into zebra, you MAY add a polling fallback that reads the **same standard signal** (`VRF` table) and performs the **same intended action** (`vtysh vrf X; vni N`) — this is a valid bug fix. But do NOT invent a custom CONFIG_DB table (like `NEWTRON_VNI`) that replaces the standard signal, or change what CONFIG_DB entries callers write to accommodate the workaround. Invented table schemas interact unpredictably with community code and create maintenance debt.

When a CiscoVS SAI call fails: document it as an RCA, and fix it at the SAI layer if possible. Do not route around it by repurposing unrelated SAI paths (e.g., shadow VLANs to work around broken VNI_TO_VIRTUAL_ROUTER_ID).

## Single-Owner Principle for CONFIG_DB Tables (DRY)

Each CONFIG_DB table MUST have exactly one owner — a single file/function responsible
for writing and deleting entries in that table. Composites (ApplyService, SetupEVPN,
ApplyBaseline, topology provisioner) MUST call the owning primitives and merge their
ChangeSets rather than constructing entries inline.

This applies at every layer: if `vlan_ops.go` owns `VLAN` table writes, then
`service_gen.go` must call into `vlan_ops.go`, not duplicate the entry construction.

Target ownership map:

```
vlan_ops.go        → VLAN, VLAN_MEMBER, VLAN_INTERFACE, SAG_GLOBAL
vrf_ops.go         → VRF, STATIC_ROUTE, BGP_GLOBALS_EVPN_RT
bgp_ops.go         → BGP_GLOBALS, BGP_NEIGHBOR, BGP_NEIGHBOR_AF,
                      BGP_GLOBALS_AF, ROUTE_REDISTRIBUTE, DEVICE_METADATA
evpn_ops.go        → VXLAN_TUNNEL, VXLAN_EVPN_NVO, VXLAN_TUNNEL_MAP,
                      SUPPRESS_VLAN_NEIGH, BGP_EVPN_VNI
acl_ops.go         → ACL_TABLE, ACL_RULE
qos_ops.go         → PORT_QOS_MAP, QUEUE, DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP,
                      SCHEDULER, WRED_PROFILE
interface_ops.go   → INTERFACE
baseline_ops.go    → LOOPBACK_INTERFACE
portchannel_ops.go → PORTCHANNEL, PORTCHANNEL_MEMBER
service_ops.go     → NEWTRON_SERVICE_BINDING, ROUTE_MAP, PREFIX_SET,
                      COMMUNITY_SET
```

Current violations are tracked in `docs/refactor-items.md` item 5. When adding new
CONFIG_DB writes, always check the ownership map — never add a second writer.

## Separation of Concerns — File-Level Ownership

Code should be organized so that a reader can guess where a feature is implemented
by looking at file names alone.

Rules:
1. **composite.go** = delivery mechanics (CompositeBuilder, CompositeConfig, DeliverComposite).
   No CONFIG_DB table or key format knowledge.
2. **topology.go** = topology-driven provisioning orchestration.
   Calls config functions from `node/` but never constructs CONFIG_DB keys inline.
3. **Each `*_ops.go`** = sole owner of its CONFIG_DB tables' entry construction
   (as defined in the Single-Owner Principle ownership map).
4. **service_gen.go** = service-to-entries translation. Calls config functions from
   owning `*_ops.go` files.

## Redis-First Interaction Principle

newtron is a Redis-centric system. All device interaction MUST go through SONiC Redis databases (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB). See `docs/newtron/hld.md` for the full interaction model.

When Redis does not expose the required data or operation, CLI/SSH commands may be used **only as documented workarounds**. Every such call site MUST be tagged:

```go
// CLI-WORKAROUND(id): <what this does>.
// Gap: <what Redis/SONiC lacks>.
// Resolution: <what upstream change would eliminate this>.
```

- **Workaround** — Redis could provide this but doesn't today. Tag with `CLI-WORKAROUND`.
- **Inherent** — Will always require CLI (e.g., `config save`, `docker restart`, filesystem reads). No tag needed, but add a brief comment explaining why CLI is required.

Before adding any `session.Run()`, `ExecCommand()`, or shell command construction in `pkg/newtron/device/` or `pkg/newtron/network/`:

1. Check if the data is available in CONFIG_DB, APP_DB, ASIC_DB, or STATE_DB
2. If it is, use the Redis path
3. If it isn't, add the `CLI-WORKAROUND` tag with a resolution path
4. Never normalize CLI calls — they are exceptions, not the standard interaction model

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
- `bin/newtlab`, `bin/newtron`, `bin/newtest`, `bin/newtlink` (all subcommands)

### Make
- `make build`, `make test`, `make lint`

### Misc
- `ls`, `stat`, `file`, `wc`, `chmod`, `ln`
- `pgrep`, `pkill`, `ps`
- `ssh`, `sshpass`, `ssh-keygen`, `nc`, `socat`, `curl`, `ping`
- `qemu-img info`, `qemu-img convert`

### Web Access
- `WebSearch` (always allowed)
- `WebFetch` for: `github.com`, `raw.githubusercontent.com`, `containerlab.dev`, `hackmd.io`, `sonic.software`, `deepwiki.com`, `r12f.com`

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

## Regression Prevention

**Never break a feature that was previously working.**

Before making any change to `service_gen.go`, `*_ops.go`, or any shared code path:
1. List which service types and test scenarios exercise that code path
2. Verify that changes to the broken path do not affect the working paths
3. Run `go test ./...` after every change and confirm all previously passing tests still pass
4. If a change is required that affects a shared code path, explicitly document which
   working features are at risk and how they are protected

Tracking what was working (update this as test suites are validated):
- `evpn-bridged`: WORKS — 2node-incremental, 3node-dataplane (evpn-l2-irb L2 path)
- `routed`, `irb`, `bridged`: WORKS — 2node-incremental
- `evpn-irb` (L2 path): WORKS — 3node-dataplane evpn-l2-irb
- `evpn-irb` (L3 routing), `evpn-routed`: NOT YET WORKING — under active development

## Feature Implementation Protocol (SONiC CONFIG_DB)

Before writing any CONFIG_DB entries to implement a SONiC feature:

1. **CLI-first research**: Find the SONiC CLI command that enables the feature. Read the
   sonic-utilities / sonic-mgmt-framework source to see exactly what CONFIG_DB tables and
   fields those commands write, in what order, and what pre/post steps they take.

2. **Empirical verification**: On a freshly deployed (clean) SONiC node, configure the
   feature using only SONiC CLI commands (NOT newtron). Verify it works end-to-end.
   Then capture the resulting CONFIG_DB state (`redis-cli -n 4 KEYS '*'` etc.) as the
   ground truth.

3. **Framework audit**: Read the relevant SONiC daemon source (vrfmgrd, vxlanmgrd,
   orchagent, frrcfgd) to understand how each CONFIG_DB entry is processed, what
   APP_DB / ASIC_DB entries it creates, and what ordering constraints exist.

4. **Implement in newtron**: Make newtron write the same CONFIG_DB entries in the same
   order as the CLI path. Do not invent alternative CONFIG_DB layouts without explicit
   user authorization.

5. **Targeted test first**: Create a targeted newtest suite (like `2node-l3vpn`) that
   tests only the specific feature. Debug and pass it before integrating into composite
   suites (like `2node-primitive`).

**Never assume a CONFIG_DB path works without first verifying it via CLI on a real device.**

## Model Escalation

If using Claude Sonnet and no resolution is reached within 15 minutes, switch to
Claude Opus 4.6 (model ID: `claude-opus-4-6`) for architectural decisions and debugging.

## Testing Protocol

- **Always start tests on a freshly deployed topology.** Destroy and redeploy before running
  any test suite. Never attempt to reuse a topology that has run previous tests or has
  manually applied state. This ensures a clean, reproducible baseline.

## User Preferences

- Never compact away the last 5 prompts and responses during context compression.
- When agents are running, include a status line in each response showing count, model (opus/sonnet), and status. Example: **Agents: 2 running** (1 opus, 1 sonnet) / 3 completed. Omit when no agents are active.
