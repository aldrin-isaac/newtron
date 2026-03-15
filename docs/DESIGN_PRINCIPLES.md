# Design Principles

Network automation tools have a habit of solving the easy problem
brilliantly and ignoring the hard one. Generating configuration from
templates is a solved problem — Jinja, YANG, intent engines, there is
no shortage of ways to produce the right CONFIG_DB entries. The hard
problem is what happens next: delivering those entries so that every
write lands,
nothing is left half-applied, and the operation can be undone cleanly
months later by someone who wasn't there when it was applied. Most
tools treat delivery as someone else's problem. newtron treats it as
*the* problem.

This document explains the thirty-eight principles behind that choice
— not as a reference, but as a narrative. Part I states the thesis.
Part II establishes the domain model. Part III describes the
operational contract that keeps the promise. Part IV explains how
shared objects coexist with independent lifecycles. Part V shows how
the code reflects the model. Part VI covers working conventions.
Each section builds on what came before.

Not all principles carry the same weight. Some are convictions
specific to this project — ways of thinking about delivery, device
reality, isolation, and platform relationships that shaped newtron's
architecture. Others are established engineering practices — single
ownership, pure functions, API boundaries — that newtron subscribes
to and enforces. A few are style preferences where reasonable
alternatives exist. The summary table at the end marks which is
which.

Read this before the HLDs and LLDs. It explains *why* things are the
way they are.

---

# Part I: The Thesis

Three principles define what newtron is and what it promises. Everything
else in this document follows from them.

## 1. The Opinion Is in the Pattern

Most opinionated tools prescribe the building — the topology, the
architecture, the scale. newtron prescribes the bricks.

Every piece of SONiC configuration — a VLAN, a BGP session, a service
binding, an ACL rule — can be configured many ways in CONFIG_DB. An
operator choosing between them isn't making a topology decision. They're
making a *primitive* decision — how this one unit of configuration should
look on the device. newtron makes that decision once, for every
primitive, and gives the operator everything else back.

The opinions live at the smallest possible level — the individual
CONFIG_DB entry pattern — not at the network level. newtron does not
prescribe a network topology. It does not tell you how many spines to
deploy or where to place your overlays. It prescribes how each unit of
configuration should look. What you build from those units — the
topology, the overlays, the scale — is your design.

This produces two distinct layers of architecture:

- **Configuration architecture** — one pattern per primitive. How a VLAN
  is structured, how a BGP neighbor is established, how a service binds
  to an interface. These patterns can evolve — all-eBGP today, new
  routing models tomorrow — but at any point in time, each primitive has
  exactly one pattern.

- **Topology architecture** — the operator's composition. Spine-leaf,
  hub-spoke, single overlay, multiple overlays, two nodes or two hundred.
  newtron constrains the building blocks, not the building.

Consistent primitives compose into a coherent network. This is not a
hope — it is a structural consequence. When every VLAN follows the same
pattern, every BGP session the same model, every service binding the
same lifecycle, the pieces fit together because they were shaped by the
same hand. Coherence is not imposed from above; it emerges from below.

---

## 2. Delivery Over Generation

The config management industry has spent two decades perfecting
generation. Templates, Jinja, YANG models, intent engines — there is no
shortage of ways to *produce* configuration. The problem of *delivering*
it safely to a device — so that every entry lands, nothing is left half-
applied, and the operation can be undone cleanly — remains largely
unsolved.

Without delivery guarantees, configuration degrades. Not immediately,
and not obviously, but inevitably:

- **Partial applies leave orphaned entries.** A multi-entry operation
  fails partway through. The entries that landed have no owner. Nothing
  knows how to find them, nothing knows how to clean them up. They
  accumulate in CONFIG_DB, silently corrupting device state.

- **Overlapping writes munge shared state.** Two operations write to the
  same CONFIG_DB table without coordination. Fields from one overwrite
  fields from the other. Neither operation's intent is fully realized on
  the device. The problem is invisible until a daemon crashes or traffic
  blackholes.

- **Blind teardown corrupts what it doesn't understand.** Removal
  inspects current device state to guess what to delete. But if other
  operations have added entries since the original apply, teardown cannot
  distinguish its entries from theirs. It either removes too much
  (breaking other services) or too little (leaving orphans).

These are not edge cases. They are the steady state of any system that
treats delivery as someone else's problem. newtron's delivery pipeline
addresses each one directly:

1. **Validated against schema.** Every CONFIG_DB entry passes YANG-derived
   constraint checking before reaching the device. A typo in a field name
   is caught at the point of write, not when a daemon silently ignores
   the entry thirty seconds later.

2. **Applied atomically.** Every mutating operation produces a ChangeSet
   — a complete, ordered description of what will change — computed fully
   before any Redis write occurs. Dry-run is the default; execution is
   opt-in. Because the description is complete before the first write,
   the outcome is always knowable: either every entry landed, or the
   ChangeSet tells you exactly which did and which didn't.

3. **Verified by re-reading.** After execution, newtron re-reads every
   entry it wrote and diffs against the ChangeSet. If anything is missing
   or different, you know immediately — not when a health check fails an
   hour later.

4. **Reversible by construction.** Every forward operation records what
   it did — on the device, as a service binding. Teardown reconstructs
   from what was actually applied, not from current device state that may
   have changed since. No guessing, no scanning, no "does this entry
   belong to me?"

These guarantees are properties of the pipeline, not of any specific
primitive. When a new primitive is added, it inherits them automatically.
When an existing primitive changes, they remain. The primitives are the
variable; the delivery contract is the invariant.

---

## 3. Faithful Enforcement

Every capability a system learns is new surface area for failure. A
tool that manages VLANs can break in one way. Add BGP, and it can break
in two. Add ACLs, EVPN, QoS, LAGs, VRFs, static routes — each
primitive multiplies the surface for partial applies, stale state, and
orphaned entries. Growth and reliability are naturally opposed.

Most systems accept this tradeoff implicitly. Each new feature gets its
own verification logic, its own cleanup path, its own error handling.
The first five features maintain discipline. By feature fifteen, the
verification for feature three has drifted from the rest. By feature
thirty, nobody remembers what feature eight's rollback was supposed to
do. Reliability erodes not because anyone chose to let it — but because
per-feature reliability doesn't scale.

The only way out is to make reliability a property of the *pipeline*,
not of each feature that passes through it. newtron's enforcement
contract — schema validation, atomic application, post-write
verification, symmetric reversal — is not a feature list. It is the
structural invariant that every primitive inherits by virtue of
producing a ChangeSet. When a new primitive is added, it gets these
guarantees automatically. When an existing primitive changes, the
guarantees remain. The primitives are the variable; the delivery
contract is the invariant.

The opinions (§1) define what each primitive looks like. The delivery
pipeline (§2) ensures each primitive arrives safely. Together they form
the enforcement contract — the reason newtron can accumulate capability
without accumulating fragility.

newtron is never done — it is always acquiring new primitives, not
converging on a final set. The enforcement contract is what keeps that
growth sound.

---

# Part II: The Domain Model

Before describing how newtron operates, we must establish what it
operates *on* — how it sees SONiC, how it treats device state, and where
services live in its object model. These five principles are the
premises that every operation in Part III assumes.

## 4. SONiC Is a Database — Treat It as One

Every layer of indirection between a tool and the system it manages is
a layer where information is lost. CLI output is a rendered view — it
shows what the developer chose to show, formatted how they chose to
format it, with errors they chose to surface. The database is the data
itself.

SONiC's architecture is a set of Redis databases — CONFIG_DB (desired
state), APP_DB (computed routes from FRR), ASIC_DB (SAI forwarding
objects), STATE_DB (operational telemetry) — with daemons that react to
table changes. This is not an implementation detail; it is the
architecture. The databases are the source of truth for what the device
is doing. The daemons are reactive processors that transform one
database's state into another's.

newtron interacts with SONiC exclusively through Redis. CONFIG_DB writes
go through a native Go Redis client over an SSH-tunneled connection —
not through SONiC's `config` CLI commands. Route verification reads APP_DB
directly. ASIC programming checks traverse ASIC_DB's SAI object chain.
Health checks read STATE_DB.

The alternative — SSHing in and parsing CLI output — is fragile in ways
that compound silently. `show ip route` output varies between SONiC
releases; a tool that parses it must be patched for every release.
`config vlan add` returns exit code 0 even when it silently fails; a
tool that trusts the exit code believes the VLAN was created when it
wasn't. Text parsing adds a translation layer between what the device
knows and what the tool sees — and every translation layer is a place
where meaning can be lost, reformatted, or silently dropped. Redis
eliminates that layer: the data structures *are* the interface. A hash
in CONFIG_DB is the configuration, not a description of it.

When Redis cannot express an operation (persisting config to disk,
restarting daemons, reading platform files), CLI commands are used as
documented exceptions — each tagged `CLI-WORKAROUND` with a note on
what upstream change would eliminate the workaround. The goal is to
reduce these over time, not normalize them. Every CLI call in the
codebase is either an inherent exception (the operation requires the
filesystem) or a temporary workaround (Redis could provide this but
doesn't yet). There is no third category.

---

## 5. Specs Are Intent; The Device Is Reality

Terraform owns its state file. Kubernetes owns its etcd. They can be
reconcilers because they are the sole writer — if state drifts, it
drifted from *their* truth, and they can push it back. The sanest
architecture for any configuration system is a single owner.

newtron would prefer to be that owner. But it does not assume it is.
Admins edit CONFIG_DB directly. SONiC daemons write to it. Factory
images leave artifacts in it. Other tools modify it. The architecture
must not break when this happens — not because multi-writer is the
goal, but because the real world is not always the ideal case. newtron
is designed to do no harm: it tracks what it wrote, cleans up what it
created, and leaves everything else untouched.

The paired framing that follows from this governs every operation:

**Specs** define what services, policies, and overlays are available.
They are the vocabulary of the network — "a service called transit
has eBGP peering with an ingress filter" — describing *how* each
primitive should behave, not *where* it should be applied. Which
interface gets which service is the operator's decision, made at
apply time via newtron's CLI or HTTP API. Specs can live in
version-controlled JSON files, or be pushed to newtron at runtime by
an external system (a CMDB, a provisioning portal) via its API.
newtron does not mandate where specs come from — only that they are
loaded into its running state before an operation references them.

**CONFIG_DB** is what exists on the device, whether correct or not. It
is imperative — "VRF|Vrf-customer-Ethernet0 exists with vni=3001." It
uses concrete values: IPs, VLAN IDs, AS numbers. It lives in Redis on
each device and is produced at apply time — though admins and other
tools can and do edit it directly.

The translation from spec to CONFIG_DB entries uses device context to
derive concrete values. Each device has a **profile** — its identity in
the network: AS number, loopback IP, EVPN peers, platform type. When
a service spec says `"peer_as": "request"`, that means the AS number
is supplied by the operator at apply time (via `--peer-as` on the CLI,
or from a topology file during provisioning). A filter reference says
`"ingress_filter": "customer-in"` — newtron expands this into numbered
ACL rules from the filter definition.

This separation enables two properties that matter:

1. **The same spec applied to different devices produces different
   config** — because the concrete values come from each device's
   context, not from the spec itself.

2. **The same spec applied twice to the same device produces identical
   config** — because the translation is deterministic. This is what
   makes reprovisioning idempotent.

### After application, the device is the authority

Once configuration is applied, the device CONFIG_DB is ground reality.
If an admin edits CONFIG_DB directly — via the SONiC CLI, Redis, or
another tool — that edit is the new reality. newtron does not fight it.
There is no background process watching for drift. There is no
reconciliation loop. There is the device, and there is the change you
are asking for.

Parts of the device's CONFIG_DB may have been written by newtron. Other
parts may have been written by an admin, by another tool, or left over
from a factory image. newtron operates on what it finds, not on what it
expects to find.

Different operation types interact with this reality differently:

- **Provisioning** is the one exception where intent replaces reality.
  It computes a complete device configuration from specs and profile,
  then writes it to CONFIG_DB as a single atomic operation — removing
  stale keys while preserving factory defaults (MAC, platform metadata,
  port config). This is the initial act of establishing reality from
  intent.

- **Basic operations** (CreateVLAN, ConfigureBGP) read CONFIG_DB to check
  preconditions — "does this VLAN already exist?" — but generate entries
  from specs and profile, not from device state.

- **Service operations** trust the binding record as ground reality.
  `ApplyService` reads CONFIG_DB for idempotency filtering on shared
  infrastructure (does the VLAN or VRF already exist?). `RemoveService`
  reads the NEWTRON_SERVICE_BINDING record — not CONFIG_DB tables, not
  specs — to determine what to tear down.

### Bindings are ground reality for teardown

NEWTRON_SERVICE_BINDING records live on the device, not in spec files.
When a service is applied to an interface, newtron writes a binding
record to CONFIG_DB that captures exactly what was applied — which
VLANs, VRFs, ACLs, and VNIs were created for that service.

The binding is the sole input for teardown. `RemoveService` does not
re-derive the removal from the spec, because the spec may have changed
between apply and remove. What matters is what was *actually applied*.
For example, EVPN overlay parameters (L3 VNI, its transit VLAN) are
stored in the binding so removal can tear down overlay infrastructure
without looking up the VPN spec — which may have changed since the
service was applied.

When adding a new forward operation that creates infrastructure, the
question to ask is: *can the reverse operation find everything it needs
in the binding alone?* If not, the binding is incomplete.

### Why newtron is not a reconciler

A reconciler needs a single canonical source of desired state to diff
the device against. For incremental operations, no such canonical source
exists. The "desired state" of the device is its current state plus the
requested change, and the current state can only be read from the device
itself.

And two opinionated architectures cannot converge on the same device.
newtron's device-reality checks minimize harm — they don't accommodate
existing config from a different architectural model.

**The device is the reality; specs are the intent; operations transform
reality using intent.**

### Baseline prerequisites are non-negotiable

newtron accommodates other writers — but it requires a device baseline.
SONiC supports two modes for BGP configuration: unified mode, where
CONFIG_DB entries flow through SONiC daemons to FRR, and split mode,
where vtysh configures FRR directly. newtron is Redis-first (§4). It
writes BGP configuration to CONFIG_DB and depends on daemons to relay
those entries. Split mode breaks this path — vtysh bypasses CONFIG_DB
entirely.

Unified mode is non-negotiable. This is the one place where coexistence
of two configuration approaches is refused. `newtron init` establishes
the baseline: unified mode enabled, factory artifacts cleaned,
platform-specific patches applied. After init, newtron accommodates
other writers within the established baseline. It will not accommodate
a writer that changes the baseline itself.

Other baseline requirements may emerge as new primitives require them.
The principle is the same: state the prerequisites, establish them once
at initialization, and accommodate everything else.

---

## 6. The Interface Is the Point of Service

Every service delivery system must choose what it binds to. The choice
is consequential: whatever entity you choose becomes your unit of
lifecycle, your unit of state, and your unit of failure. Bind services
to the device, and every service change is a device-wide operation —
you cannot apply a service to one port without reasoning about all
ports. Bind to the topology, and every change is network-wide — you
cannot reason about one device independently. Bind to the interface,
and each port is independently manageable: one service binding, one
lifecycle, one blast radius.

This is not a code-organization choice. It is the fundamental
abstraction of the domain. A network *is*, at its core, services
applied on interfaces. Routing policy attaches to an interface. VRF
binding, VLAN membership, ACL application, QoS scheduling, BGP
peering — all are per-interface. The interface is where abstract intent
meets physical infrastructure:

- **The point of service delivery** — where specs bind to physical ports
- **The unit of service lifecycle** — apply, remove, refresh happen
  per-interface
- **The unit of state** — each interface has exactly one service binding
  (or none)
- **The unit of isolation** — services on Ethernet0 and Ethernet4 are
  independent

Because the interface is the unit of service in the domain, it is the
unit of service in the code. `ApplyService` lives on Interface — not
on Node, not on Network — because the interface is the entity being
configured, the point where a service becomes real. `VerifyChangeSet`
lives on Node because the node holds the Redis connection.
`GetService` lives on Network because services are network-wide
definitions independent of any device.

```
┌─────────────────────────────────────────────────┐
│                                                 │
│                     Network                     │
│                   owns: specs                   │
│      GetService(), GetFilter(), GetZone()       │
│                                                 │
└─────────────────────────────────────────────────┘
  │
  │ parent ref
  │ (spec lookup)
  ▼
┌─────────────────────────────────────────────────┐
│                                                 │
│                      Node                       │
│  owns: profile, resolved specs, Redis, ConfigDB │
│    ConfigureBGP(), SetupEVPN(), CreateVLAN()    │
│                                                 │
└─────────────────────────────────────────────────┘
  │
  │ parent ref
  ▼
┌─────────────────────────────────────────────────┐
│                                                 │
│                    Interface                    │
│  owns: interface identity (name + parent node)  │
│   ApplyService(), RemoveService(), ApplyQoS()   │
│    SetIP(), SetVRF(), BindACL(), UnbindACL()    │
│                                                 │
└─────────────────────────────────────────────────┘
```

The general principle: **a method belongs to the smallest object that
has all the context to execute it.** When `Interface.ApplyService()` is
called, the interface reaches up to the Node for the AS number, up to
the Network for the service spec, and combines them
with its own identity to produce CONFIG_DB entries. No external
function orchestrates this — the object has everything it needs through
its parent chain.

Interface delegates to Node for infrastructure (Redis connections,
CONFIG_DB cache, specs) just as a VLAN interface on a real switch
delegates to the forwarding ASIC for packet processing. The delegation
does not make Interface a forwarding layer — it makes Interface a
logical point of attachment that the underlying infrastructure services.

Whatever can be right-shifted to the interface level, should be. BGP
is the clearest example. eBGP neighbors are interface-specific — they
derive from the interface's IP and the service's peer AS, so they
belong to interface configuration via `ApplyService`. Overlay peering
is device-specific — it derives from the device's role, so it belongs
to device-level setup. The rule is the same as for methods: push
configuration down to the most specific entity that fully determines
it. Interface-level config is more composable, more independently
testable, and easier to reason about than device-level config that
happens to mention an interface.

---

## 7. Definition Is Network-Scoped; Execution Is Device-Scoped

The "transit" service defines what eBGP peering looks like — peer
group, route policy, filter references. It says nothing about which
interface, which switch, which AS number, or which loopback IP. When
an operator applies it to Ethernet0 on switch1, the device context
turns that abstract definition into concrete CONFIG_DB entries. The
definition belongs to the network; the binding belongs to the operator.

This is not organizational tidiness. It determines whether two
lifecycles — defining what services exist and executing them on
devices — can evolve independently. A service defined inline on each
device means that changing the service means changing every device. A
device that can't be configured until every service it might use is
defined means the device lifecycle is held hostage by the definition
lifecycle. Both directions of coupling are unnecessary if definition
lives at the network level and execution at the device level.

A service can be defined before any device connects. A device can
consume a service defined after it connected. Neither layer should
require the other to be in a particular state for its own operations
to succeed.

### Hierarchical spec resolution

Seven spec maps — Services, Filters, IPVPNs, MACVPNs, QoSPolicies,
RoutePolicies, and PrefixLists — are defined in a three-level
hierarchy: network → zone → node (device profile). **Lower level
wins.** A service defined at the node level overrides the same-named
service at the zone level, which overrides the network level.

The architect defines standard templates at the network level —
available to every device. A zone ("datacenter-east") can specialize.
An individual device can override further. You never copy-paste a full
spec at every level; you only define what differs.

**Platforms are global-only.** Platform definitions describe hardware
capabilities — HWSKU, port count, NIC driver — not network intent.
They have no meaningful per-zone or per-node variation.

The merge is performed once at startup, producing a resolved view for
each device. This cleanly separates two concerns: **what specs exist**
(the three-level hierarchy) and **what specs does this device see**
(the merged view). Device-level code does not know about zones,
networks, or override logic. It asks for a service by name and gets
the right definition — already resolved.

### The snapshot problem and live fallback

Decoupling definition from execution creates a timing question. Each
device receives a merged snapshot of its specs at connection time.
Specs added to the network after the snapshot — and this is not
hypothetical, since the API can add specs at runtime — would be
invisible to every connected device until the server restarts.

The resolution: spec lookups check the device's merged snapshot first
(preserving override semantics — device profile wins over zone wins
over network). On miss, they fall through to the network-level
definitions. The hierarchy stays intact for overrides; the network
level stays open for additions:

```
device.GetService("TRANSIT")
  1. Check merged snapshot → found (profile override) → return it
  2. Miss → fall through to network.GetService("TRANSIT") → found
  3. Miss at both levels → "service not found" error
```

Every spec lookup must include the network fallback. A snapshot-only
lookup is a bug.

**Define once at the broadest applicable scope; override only where
necessary; resolve once at node creation.**

---

## 8. Scope Boundaries — What newtron Owns

A tool that deploys infrastructure *and* configures devices *and*
orchestrates multi-device workflows has three jobs — and a refactor to
any one of them can break the other two. The blast radius of a change
is the entire tool, because the abstraction levels are entangled inside
a single process. This is the default architecture of most automation
systems, and it is the reason most automation systems are fragile.

newtron owns single-device configuration delivery — one scope, one
failure domain. Each operation targets one device, translating specs
into CONFIG_DB entries through an SSH-tunneled Redis connection. newtron
never talks to two devices at once. Multi-device coordination is not
its job.

newtron is a client-server system. The server (newtron-server) loads
specs, maintains device connections, and exposes all operations as an
HTTP API. The CLI is one thin client; orchestrators are another kind
of client. Multi-device coordination — deciding what to apply, where,
in what order — belongs to orchestrators that consume the same API.
newtrun, the project's E2E test orchestrator, is one: it provisions
devices through newtron, then asserts correctness across the fabric.
newtron's observation primitives (`GetRoute`, `RunHealthChecks`)
return structured data, not judgments (§12), so any orchestrator can
make its own decisions.

Good automation development requires a virtual twin — the ability to
stand up a faithful replica of the target network running real SONiC
software and exercise every primitive against it. Without this, you
are testing against documentation, not behavior (§30). newtlab
provides this: QEMU VMs wired into topologies that newtron configures.
The virtual twin is separate infrastructure — it validates the
automation, it is not the automation.

### Integration through the spec directory

The natural instinct when integrating tools is to connect them with
APIs — RPC calls, shared libraries, service registries. newtron's
integration model avoids all of these. Tools communicate through the
spec directory — a set of JSON files describing the network, its
devices, and its services:

- Infrastructure tools write connectivity details (`ssh_port`,
  `console_port`, `mgmt_ip`) into device profile files.
- newtron reads those profiles and uses them to connect.
- Orchestrators invoke newtron's API, passing spec references by name.

This means no shared libraries (a change to newtron's internal types
does not require rebuilding anything else), no runtime coordination
(tools don't need to be alive at the same time), and no service
discovery (read a file, not an endpoint). The spec directory is the
integration surface. Each tool is a separate binary with a separate
failure domain.

---

# Part III: The Operational Contract

Part I made a promise: every primitive, delivered safely. Part II
established the domain model — SONiC as a database, the device as
reality, the interface as the point of service. These principles
are the machinery that keeps the promise on real devices — from a
single operation's ChangeSet (§9) through to drift detection across
the device's lifetime (§36–38).

## 9. The ChangeSet Is the Universal Contract

Most systems preview changes with one mechanism, execute them with
another, and verify them with a third. The three mechanisms are built
at different times, by different developers, for different purposes —
and they inevitably diverge. Preview says the operation will create
five entries; execution creates six. Execution succeeds; verification
checks a different set of fields than execution wrote. These are not
exotic bugs. They are the natural consequence of maintaining three
separate representations of "what this operation does."

The ChangeSet collapses all three into one object. Every mutating
operation produces a ChangeSet — an ordered list of CONFIG_DB mutations
with table, key, operation type, old value, and new value:

1. **Dry-run preview** — display what would change before anything is
   written. The ChangeSet *is* the preview.
2. **Execution receipt** — the same ChangeSet drives the Redis writes.
   What was previewed is what gets written.
3. **Verification contract** — `VerifyChangeSet` re-reads CONFIG_DB and
   diffs against the same ChangeSet. What was written is what gets
   verified.

The three representations cannot diverge because they are one
representation. Creating a VLAN produces a ChangeSet with one entry.
Applying a service produces a ChangeSet with a dozen entries.
Delivering a composite config produces a ChangeSet with hundreds of
entries. `VerifyChangeSet` handles all of them identically — because it
doesn't know or care what operation produced the ChangeSet. If it
produces a ChangeSet, it's automatically previewable, executable, and
verifiable. Adding a new operation never requires adding a new
verification method.

A ChangeSet is atomic within a single newtron invocation. If an
orchestrator makes multiple invocations and the second fails, deciding
whether to roll back the first is the orchestrator's responsibility.
newtron provides the mechanism (each ChangeSet can be reversed through
domain operations); the orchestrator decides the policy.

### Replace semantics require DEL+HSET

Redis `HSET` merges fields into an existing hash — it does not remove
old fields. Any operation that replaces a key's content
(`RefreshService`, re-provisioning) must `DEL` the key first, then
`HSET` the new fields. Without the `DEL`, stale fields from the
previous state persist as ghost data. For example, if a service binding
previously had `qos_policy=gold` and the new service definition drops
QoS, an `HSET` leaves the old `qos_policy` field intact — only
`DEL`+`HSET` gives a clean replacement.

Apply must preserve delete+add sequences in order. Verification checks
final state only — a key that was deleted then re-added is verified as
"should exist with new fields."

---

## 10. Dry-Run as First-Class Mode

Every mutating operation supports dry-run as the **default behavior**.
The `-x` flag is required to execute. Without it, operations preview
what would change and return.

This is not just a safety feature — it is an architectural constraint
that shapes how every operation is written. An operation that can
preview its changes without executing them *must* separate computation
from execution. The ChangeSet must be fully resolved — every table,
every key, every field — before any Redis write occurs. You cannot
write an operation that "figures out what to do as it goes," because
dry-run mode would have nowhere to stop.

This forced separation produces a structural side effect that wasn't
planned but proved essential: offline provisioning. Because newtron can
compute a full device configuration without connecting to a device —
it's just spec translation — it can build a complete configuration in
memory and deliver it later as a single atomic operation. Offline
provisioning is not a second code path bolted on later; it falls out
of the same constraint that makes dry-run work.

**Preview first. Execute deliberately. The same code does both — and
the constraint that makes preview possible is what makes offline
provisioning possible.**

---

## 11. Prevent Bad Writes, Don't Just Detect Them

Catching bad writes after they land is too late. The SONiC daemon
that ingests an invalid entry may crash, silently corrupt state, or
enter an unrecoverable mode before any post-write check can run. The
debugging session that follows works backward from effects
("orchagent is restarting") to causes ("a VLAN ID was out of
range"), a process that can take hours and provides no guarantee of
finding the root cause.

The only reliable solution is to prevent the write from reaching the
device at all. newtron does this at two distinct levels — one for
business logic, one for data format — and both must pass before a
single byte reaches Redis:

### Preconditions enforce business logic

Every mutating operation runs precondition validation before the
ChangeSet is even generated:

```go
n.precondition("apply-service", ifName).
    RequireInterfaceExists(ifName).
    RequireInterfaceNotLAGMember(ifName).
    RequireNoExistingService(ifName).
    RequireVTEPExists().  // if EVPN service
    Result()
```

Preconditions are built into the operation, not bolted on by the caller.
You cannot call `ApplyService` and skip the checks — they run as the
first step of the operation. This is application-level referential
integrity for a database that has none.

For removal operations, newtron scans CONFIG_DB to determine if shared
resources (VRFs, VLANs, ACLs) are still referenced by other service
bindings before deleting them. A VRF used by three interfaces isn't
removed until the last interface unbinds from it.

### Schema validation enforces data format

Every ChangeSet passes through validation before any Redis write.
newtron maintains an internal schema derived from SONiC YANG models
that checks types, ranges, enums, and patterns for every table and
field:

```go
func (cs *ChangeSet) Apply(n *Node) error {
    // ... precondition checks (business logic) ...
    if err := cs.Validate(); err != nil {
        return err  // schema violations → no writes at all
    }
    // ... Redis writes ...
}
```

The schema is **fail-closed**. Unknown table → error. Unknown field →
error. Every table newtron writes must have a schema entry. Adding a
write to a new CONFIG_DB table without declaring its schema produces a
validation failure — catching misspelled field names at the point of
write, not when a SONiC daemon silently ignores the entry thirty
seconds later.

**YANG is the authority** for value constraints. Ranges, enums, and
patterns in newtron's schema must match the SONiC YANG model. When
they diverge, the deviation is documented with an explanation.

**Preconditions and schema validation together make the invalid states
unrepresentable at the API level.** The operator who passes a bad value
gets an immediate, specific error — not a daemon crash thirty seconds
later.

### Two kinds of refusal — and they are not the same

Pre-operation checks refuse work for two fundamentally different
reasons. The first: "the resource you're targeting doesn't exist." The
VLAN isn't in CONFIG_DB. The interface isn't on the device. The VRF was
never created. This is a `PreconditionError` — the operation's subject
is absent.

The second: "the resource exists but can't be safely modified." The
VRF has interfaces still bound to it. The IP-VPN has service bindings
that reference it. The operation's subject is present, but acting on
it would damage other consumers. This is a domain error — a plain
`fmt.Errorf` that describes the conflict.

The two must return different error types because callers respond to
them differently. During normal interactive use, both are errors — the
operator sees a message and adjusts. But during automated recovery
(rolling back a crashed operation), "doesn't exist" means the forward
operation never created the resource — skip it, nothing to undo. "Has
active consumers" means something the recovery path didn't expect —
stop and let the operator investigate.

Every "resource not found" check — whether in the `PreconditionChecker`
(`RequireVLANExists`), a lookup method (`GetInterface`), or an inline
existence check — must return `PreconditionError`. This is not a style
choice. Any code path that needs to distinguish "missing" from
"conflicting" depends on the error type to make that distinction. A
lookup that returns `fmt.Errorf("not found")` is indistinguishable from
a lookup that returns `fmt.Errorf("still in use")` — and a recovery
path that cannot tell them apart must treat both as fatal, losing the
ability to gracefully skip missing resources.

---

## 12. Verify Your Writes; Observe Everything Else

A tool that writes CONFIG_DB entries and a tool that checks whether
routes propagated across a fabric are doing fundamentally different
things — even though both are "verification." The first is checking its
own work against a known contract. The second is interpreting a
distributed system's emergent behavior. Conflating them — building a
tool that both writes config and asserts cross-device correctness —
forces the tool to encode assumptions about the entire network. Those
assumptions break the moment the network changes in a way the tool
didn't anticipate.

newtron draws the line at what it can *know* versus what it can only
*see*.

**Assertions** check newtron's own work. `VerifyChangeSet` re-reads
every CONFIG_DB entry newtron just wrote and diffs against the
ChangeSet. If anything is missing or different, it's a bug in newtron
or a device anomaly. There is exactly one assertion primitive — because
there is exactly one write mechanism (the ChangeSet). This assertion
is absolute: newtron knows what it wrote, so it can verify with
certainty.

**Observations** return device state as structured data. `GetRoute`
returns a route entry (or nil). `GetRouteASIC` returns a resolved SAI
chain. `RunHealthChecks` returns a health report. These methods don't
know what the "correct" answer is — they report what they see and let
the caller decide what it means.

The reason observations cannot be assertions: "Is this route correct?"
depends on what other devices are advertising, what the fabric topology
looks like, what filters are in play, whether another operator just
changed something. newtron operates on a single device. It has no
visibility into the network-wide state that would make a route
"correct" or "incorrect." Only an orchestrator that sees multiple
devices can make that judgment.

This creates a clean four-tier verification hierarchy:

| Tier | Question | Who Answers |
|------|----------|-------------|
| CONFIG_DB | "Did my writes land?" | newtron (assertion) |
| APP_DB | "Did the route appear locally?" | newtron (observation) |
| Operational | "Is the device healthy?" | newtron (observation) |
| Cross-device | "Did the route reach the neighbor?" | orchestrator |

Orchestrators compose newtron's primitives across devices — they never
re-implement them. When newtrun needs to check CONFIG_DB, it calls
`VerifyChangeSet`. When it needs to read a route, it calls `GetRoute`.

**Return data, not judgments.** A method that returns a `RouteEntry` is
useful to any caller. A method that returns `true`/`false` for "is this
route correct?" encodes assumptions about what "correct" means —
assumptions that break when the calling context changes.

---

## 13. Symmetric Operations — What You Create, You Can Remove

A configuration database without reverse operations only accumulates.
State grows monotonically. Given enough operations over enough time,
the device becomes unknowable — crusted with orphaned entries that no
one remembers creating and no tool knows how to remove. This is not a
hypothetical failure mode; it is the default trajectory of every network
device managed by tools that can create but not cleanly destroy.

Every mutating operation in newtron must have a corresponding reverse.
If newtron can create a VRF, it must be able to delete that VRF. If it
can apply a service, it must be able to remove that service. If it can
bind an ACL to an interface, it must be able to unbind it. No
CONFIG_DB state created by newtron should require a human with
`redis-cli` to clean up.

Symmetry is harder than it looks. CONFIG_DB entries have dependencies:
a VRF references interfaces, a VLAN references members, an ACL
references bound ports. A `DeleteVLAN` that leaves orphaned
`VLAN_MEMBER` entries is worse than no delete at all — the orphaned
entries cause silent failures in SONiC daemons that are nearly
impossible to diagnose. Deletion must understand the dependency graph
just as deeply as creation does.

The symmetry extends to composite operations. `SetupEVPN` creates the
VTEP, NVO, and tunnel map entries; `TeardownEVPN` removes all of them.
`ApplyService` creates VRFs, VLANs, ACLs, BGP neighbors, and a service
binding; `RemoveService` reads the binding and removes everything that
was created, checking whether shared resources are still in use before
deleting them.

The current operation pairs:

| Create | Remove |
|--------|--------|
| `CreateVLAN` | `DeleteVLAN` |
| `AddVLANMember` | `RemoveVLANMember` |
| `CreateVRF` | `DeleteVRF` |
| `AddVRFInterface` | `RemoveVRFInterface` |
| `BindIPVPN` | `UnbindIPVPN` |
| `CreatePortChannel` | `DeletePortChannel` |
| `SetupEVPN` | `TeardownEVPN` |
| `ApplyService` | `RemoveService` |
| `ApplyQoS` | `RemoveQoS` |
| `BindACL` | `UnbindACL` |
| `AddBGPNeighbor` | `RemoveBGPNeighbor` |
| `BindMACVPN` | `UnbindMACVPN` |

RefreshService is not a pair — it combines removal and reapplication
as a single operation. When a service's spec changes after it was
applied, RefreshService tears down the old configuration using the
binding (exactly as RemoveService would) and applies the new
definition in its place.

When adding a new operation that creates CONFIG_DB state, the
corresponding removal operation is not optional — it is part of the
feature. Ship both or ship neither.

The symmetry extends down to the config generator layer — the pure
functions that construct CONFIG_DB entries (see §20):

| Forward verb | Reverse verb | Example |
|-------------|-------------|---------|
| `create*` | `delete*` / `destroy*` | `createVlanConfig` / `n.destroyVlanConfig` |
| `enable*` | `disable*` | `enableArpSuppression` / `disableArpSuppression` |
| `bind*` | `unbind*` | `bindIpvpn` / `n.unbindIpvpn` |
| `assign*` | `unassign*` | `i.assignIpAddress` / removal via key |

`destroy*` is reserved for cascading teardowns that scan ConfigDB for
dependent entries (members, VNI mappings, etc.) and remove them all.
Simple `delete*` removes a single entry. When adding a new forward
config generator, the reverse must be added in the same commit.

### Shared resources make reversal a domain problem

A VRF serves multiple services. A filter binds to multiple interfaces.
A QoS policy applies to several ports. Creation handles sharing via
idempotency — `ApplyService` checks whether the VRF already exists
before creating it. But removal cannot simply invert the creation: if
two services share a VRF and one is removed, the VRF must stay.

This is why **mechanical ChangeSet reversal is unsafe.** A ChangeSet
records low-level CONFIG_DB mutations (HSET, DEL) but not the sharing
context in which they were made. Reversing those mutations would delete
a VRF that two services share, remove a filter still bound to another
interface, or tear down a VTEP that other overlays depend on. Every
removal path scans CONFIG_DB for remaining consumers before deleting
shared resources — a domain judgment that no mechanical reversal can
replicate.

Only domain-level reverse operations (`RemoveService`, `UnbindACL`,
`RemoveQoS`) have the context to determine whether a shared resource
can be safely removed. Rollback is therefore an orchestrator concern:
if an orchestrator provisions three interfaces and the second fails, it
calls `RemoveService` on the first — not "reverse the first ChangeSet."
newtron provides reference-aware building blocks; the orchestrator
decides when to invoke them.

**If newtron creates it, newtron must be able to remove it. No orphans,
no manual cleanup, no `redis-cli` required.**

### Never enter a state you can't recover from

Symmetric operations guarantee that every forward action has a reverse.
But the reverse only helps if you can find what needs reversing. If a
process crashes mid-apply, the in-memory ChangeSet is lost — the
reverse operations exist but nobody knows to call them.

The intent record solves this: before applying, record what you intend
to do. If the process dies, the record survives. But this only works
if the record is actually written. If the intent write fails and you
proceed anyway, you've entered a state where crash recovery is
impossible — the device may have partial CONFIG_DB writes, but no
record of what was attempted. The next operator has no breadcrumb to
follow; they're left with `redis-cli` and guesswork.

The rule: **if the safety net cannot be established, do not create a
situation that needs one.** A failed intent write aborts the operation.
This is not excessive caution — it is the minimum condition for
recoverability. Proceeding without the intent is proceeding with the
assumption that nothing will go wrong, which is exactly the assumption
that crash recovery exists to challenge.

### Structural proof over heuristic detection

When detecting whether a previous operation left orphaned state, use
structural facts — not heuristic thresholds.

The original intent detection used a staleness heuristic: read the
intent, compare its last activity timestamp against the TTL, declare
it "zombie" if the TTL expired. This worked most of the time, but
introduced questions: What if the TTL is too short? What if the clock
is skewed? What if the process is just slow?

The structural proof is simpler: **if you hold the lock and an intent
exists, the previous holder is dead.** The intent lifecycle is
WriteIntent → Apply → DeleteIntent → Unlock. If the intent still
exists when a new process acquires the lock, the previous process
crashed between WriteIntent and DeleteIntent. Lock acquisition is the
proof. No timer, no threshold, no edge cases.

This pattern applies beyond intent records. Anywhere a heuristic
(timeout, polling interval, retry count) is used to detect a condition,
ask first: is there a structural fact that already proves it? A lock
that was acquired proves the previous holder released or expired. A
file that exists proves it was written. A process that responds proves
it's alive. Structural proofs are binary — they are either true or
false. Heuristics have thresholds, and thresholds have edge cases.

---

## 14. Write Ordering and Daemon Settling

CONFIG_DB is a flat key-value store, but its consumers are not. The
daemons that react to CONFIG_DB changes — orchagent, vlanmgrd, vrfmgrd,
intfmgrd, bgpcfgd, frrcfgd — impose an invisible dependency graph on
entries that Redis itself knows nothing about. Write entries out of
order and Redis reports success. The daemon silently ignores the entry,
crashes, or enters an unrecoverable state. The database accepts it; the
system rejects it — and the rejection is silent.

### The dependency chain

SONiC YANG schemas define cross-table references — a CONFIG_DB entry
that references another table cannot be processed until the referenced
entry exists. These references create a directed dependency graph.
The critical chains:

```
VLAN  ──→  VLAN_MEMBER  ──→  (interface must exist)
VLAN  ──→  VLAN_INTERFACE  ──→  VRF (via vrf_name reference)
VRF   ──→  BGP_GLOBALS  ──→  BGP_NEIGHBOR  ──→  BGP_NEIGHBOR_AF
VRF   ──→  INTERFACE (via vrf_name)
VXLAN_TUNNEL  ──→  VXLAN_EVPN_NVO  ──→  VXLAN_TUNNEL_MAP
ACL_TABLE  ──→  ACL_RULE
SCHEDULER  ──→  QUEUE (via bracket-ref)
DSCP_TO_TC_MAP  ──→  PORT_QOS_MAP (via bracket-ref)
```

### Structural ordering, not timing hacks

Write ordering is enforced structurally — by the order entries appear in
the slice returned by config functions — not by inserting `time.Sleep`
between writes:

- Config functions return entries in dependency order. Callers
  concatenate them in the correct sequence.
- Composite delivery sends entries to Redis as a single MULTI/EXEC
  transaction, preserving dependency order.
- Incremental operations write entries to Redis sequentially in
  dependency order.

There are no `time.Sleep` calls in the write path. If a developer feels
the need to add a sleep between CONFIG_DB writes, it means the ordering
is wrong or the daemon has a bug — both of which deserve investigation,
not a timing band-aid.

### Daemon settling time

Redis accepts entries instantly, but daemons need time to process them.
When a MULTI/EXEC transaction commits hundreds of entries atomically,
every subscribed daemon receives a burst of keyspace notifications
simultaneously. SONiC devices — despite running the same NOS — vary
significantly in daemon timing. The same CONFIG_DB write can settle in
under a second on one device and take thirty seconds on another,
depending on the hardware platform, the ASIC, and the SONiC release.

| Daemon | Operation | Typical Latency |
|--------|-----------|-----------------|
| vrfmgrd | VRF → kernel netdev | <1s – 5s |
| intfmgrd | Interface VRF binding | 1–30s |
| orchagent | VLAN/VRF/EVPN → SAI | 60–90s+ |
| bgpcfgd | BGP config → FRR | <1s |
| frrcfgd | VRF VNI → FRR | 1–2s |

These latencies matter in two contexts:

1. **Post-provisioning convergence.** After `DeliverComposite` writes
   the full config, daemons need time to process everything. Test suites
   handle this with polling-based health checks (`pollUntil` with
   configurable timeout and interval), not hardcoded sleeps.

2. **Inter-daemon races.** When two daemons process related entries from
   the same atomic commit, one may finish before the other creates a
   prerequisite. These are documented as RCAs with root causes and
   workarounds.

**Write ordering is a compile-time property: config functions encode it
in the slice they return. Daemon settling is a runtime property: test
suites verify it with polling, not sleeps.**

**When adding a new CONFIG_DB table:**

1. Identify its YANG dependencies — what must exist before it.
2. Place its entries after the dependency in the creation order.
3. Place its deletion before the dependency in the removal order.
4. If tests reveal a daemon race, document it as an RCA. Do not add
   `time.Sleep` to the write path.

---

## 36. Reconstruct, Don't Record

§9 through §14 govern the lifecycle of a single operation: build a
ChangeSet, preview it, validate it, apply it, verify it, reverse it.
But an operator eventually asks a question that spans the device's
lifetime: "does this device match what I intended?" The remaining three
principles in Part III answer that question without introducing an
append-only journal or any mechanism that grows with time.

The question "what should this device look like?" is always answerable
from three persistent inputs: specs (on disk), device profile (on disk),
and service bindings (in CONFIG_DB). Reconstructing expected state from
these inputs — using the same abstract Node code path that provisioning
uses (§23) — is cheaper, simpler, and more correct than maintaining a
chronological journal and replaying it.

A journal is a second copy of information that already exists in a more
authoritative form. Specs change; the journal doesn't know. Profiles
change; the journal doesn't know. The reconstruction approach uses
current specs by definition — there is no stale copy to diverge.

This principle extends §12 (verify writes, observe the rest) from
immediate to historical. newtron can verify the cumulative effect of
all its writes against current intent at any time — not just immediately
after each operation — by reconstructing expected state and diffing
against actual CONFIG_DB.

CONFIG_DB contains intent — what the device should look like — not
history. The in-flight intent record (§13) is intent: "I am currently
doing X." Completed operation history is not intent. It belongs in
structured logging or an external store, not in the device's
configuration database.

**Derive expected state from authoritative sources; don't maintain a
parallel record of it.**

---

## 37. On-Device Intent Is Sufficient for Reconstruction

The device carries enough intent to reconstruct its expected CONFIG_DB
state. `NEWTRON_SERVICE_BINDING` records which services are applied to
which interfaces, with all parameters needed for both teardown and
reconstruction. Combined with current specs, the binding tells you
exactly what CONFIG_DB entries should exist. No external history, no
journal replay, no off-device state needed.

This closes the gap between provisioning (topology-defined baseline)
and the evolved device (post-provision operations). The topology gives
you the baseline — `GenerateDeviceComposite()` with current specs
produces the expected CONFIG_DB after provisioning. The bindings give
you everything that happened since — each one carries enough
information to replay the operation on an abstract Node and produce
the incremental CONFIG_DB entries. Together, they reconstruct the full
expected state at any point in the device's lifetime.

The existing principle "bindings must be self-sufficient for reverse
operations" (§13) extends here: **bindings must be self-sufficient for
reconstruction of expected state.** Teardown is one consumer; drift
detection is another. Same data, different purpose.

This makes a specific demand on binding design: every field needed to
regenerate the expected CONFIG_DB entries must be stored in the binding.
When adding a new forward operation, ask not only "can the reverse find
everything it needs in the binding alone?" but also "can reconstruction
regenerate the expected entries from the binding alone?" If a future
forward operation creates infrastructure that the binding can't
reconstruct, drift detection breaks silently — and there is no test
that catches it until someone asks "why doesn't drift show this entry?"

**The device carries its own intent — not as history, but as living
records that evolve as operations are applied and removed.**

---

## 38. Bounded Device Footprint

Every newtron-owned record in CONFIG_DB must have a cost proportional
to the device's physical infrastructure (ports, interfaces, VLANs) or
bounded by a fixed constant — never proportional to the number of
operations performed over time.

CONFIG_DB is operational infrastructure: `config save` serializes it,
`config reload` deserializes it, daemons scan it at startup. Unbounded
growth degrades device operations for data that no SONiC daemon will
ever consume.

The intent record is O(1) per device — at most one in-flight operation.
The rollback history is O(1) per device — capped at 10 entries, oldest
evicted. Service bindings are O(interfaces) — proportional to physical
ports, not to time. None grow with the number of operations performed
over the device's lifetime.

This principle killed the append-only journal design: after seven years
of operations, CONFIG_DB would be dominated by thousands of history
entries that no SONiC daemon reads, slowing every `config save`,
`config reload`, and `KEYS *` scan. The fix was not to add compaction
or archival — it was to recognize that history does not belong in the
configuration database at all. Audit history belongs in structured
logging or an external store. CONFIG_DB is for intent.

When adding a new newtron-owned CONFIG_DB table, ask: **"does the entry
count grow with time or with infrastructure?"** If the answer is time,
either cap it with a fixed bound (rolling history) or move it out of
CONFIG_DB entirely.

---

# Part IV: Shared Objects and Policy

Parts I–III treat each operation as self-contained: one ChangeSet, one
interface, one service. But CONFIG_DB resources are not always
self-contained. ACLs, route maps, prefix sets, and peer groups are
shared — created by one operation, consumed by many, and dangerous to
delete before the last consumer is gone. These three principles govern
how shared objects coexist with independent lifecycles.

## 15. Policy vs Infrastructure — Shared Objects Have Independent Lifecycles

Some CONFIG_DB entries exist for a single interface and die with it.
Others are shared across the network and must outlive any individual
consumer. These are fundamentally different objects with fundamentally
different lifecycles, and conflating them — as most config management
systems do — forces a choice between two failure modes: premature
deletion (removing an ACL that another interface still needs) or
permanent leakage (never removing anything for fear of breaking a
consumer).

newtron resolves this by recognizing three distinct kinds of CONFIG_DB
entry, each with its own identity model and lifecycle:

| Category | Identity | Lifecycle | Examples |
|----------|----------|-----------|----------|
| **Infrastructure** | Per-interface | Created/destroyed with service apply/remove | INTERFACE IP, BGP_NEIGHBOR, VRF binding |
| **Policy** | User-named + content hash | Shared across services, independent lifecycle | ACL_TABLE, ROUTE_MAP, PREFIX_SET |
| **Binding** | Per-interface | Created/destroyed with service apply/remove | ACL ports field, peer group route_map_in |

The distinction is not taxonomic. It drives the implementation of
every create and every delete.

Infrastructure is 1:1 with an interface's service binding — it exists
because the interface needs it and dies when the interface is done with
it. A BGP neighbor for transit peering on Ethernet0 exists because
Ethernet0 has a transit service. Remove the service, remove the
neighbor. No ambiguity, no scanning.

Policy objects are N:1 — many interfaces reference the same ACL or
route map. This changes every lifecycle question. Creation must be
idempotent: the second interface that needs `PROTECT_RE_IN` must not
re-create it. Deletion must be reference-aware: the first interface
removed must not destroy what three others still depend on. The policy
exists because the *network* needs it, not because any single
interface does.

Bindings connect the two. An ACL's `ports` field lists the interfaces
that reference it. A peer group AF's `route_map_in` names the policy
object. Bindings are per-interface entries that point to shared
objects — they are created and destroyed with the interface, but what
they point to has an independent lifecycle.

The lifecycle rules follow from the identity model: infrastructure
entries are created on `ApplyService` and deleted on `RemoveService`.
Policy objects are created on first reference and deleted when the
*last* reference is removed — they persist across individual service
removals as long as at least one consumer remains. This is not
reference counting as an optimization; it is the only correct
behavior. An ACL that protects four interfaces must survive the
removal of three.

The separation also enables content-hashed naming (§16) — because
policy objects have identities independent of any interface, their
names can encode their content, allowing automatic change detection and
blue-green updates without touching every consumer simultaneously.

---

## 16. Content-Hashed Naming — Version Shared Objects by What They Write

Naming is a coordination problem. Two independent code paths — the
forward path that creates a policy object and the reverse path that
deletes it hours or days later — must agree on the same name without
ever calling each other. They share no state. They share no function
calls. They agree only by naming convention. This is inherently
fragile — unless the name itself carries proof of its content.

Shared policy objects (ACLs, route maps, prefix sets, community sets)
include an 8-character content hash in their CONFIG_DB key name:

```
ACL_TABLE|PROTECT_RE_IN_1ED5F2C7
ROUTE_MAP|ALLOW_CUST_IMPORT_A1B2C3D4|10
PREFIX_SET|RFC1918_5F2A8B3E|10
```

The hash is computed from the **generated CONFIG_DB fields** — the
actual key-value pairs that would be written to Redis — not the spec
definition. Sorted keys, sorted entries, SHA256, first 4 bytes as
uppercase hex. This means:

- Future newtron versions that add new CONFIG_DB fields automatically
  produce different hashes (correct — new fields = new content).
- No separate "canonical form" to maintain, no version field to forget
  to bump.
- The hash is literally "what would this policy write to Redis?"

When policy objects reference each other, their hashes cascade
bottom-up. PREFIX_SET hashes are computed first, then ROUTE_MAP
entries reference those hashed PREFIX_SET names. A prefix list content
change propagates through the chain automatically — ROUTE_MAP gets a
new hash because one of its referenced objects changed. The cascade
stops at the peer group, where it becomes a field update rather than
a name change.

This enables zero-disruption policy updates. Spec unchanged → hash
unchanged → refreshing the service is a no-op for that object. Spec
changed → new hash → new object created alongside old → interfaces
migrate one by one → old object deleted when last consumer migrates.

### Hash placement: always suffix, never prefix

The content hash is always a **suffix** on the object name:
`{SERVICE}_{DIRECTION}_{HASH}`, not `{HASH}_{SERVICE}_{DIRECTION}`.
This is a deliberate coupling constraint between two independent code
paths.

The forward path (`createRoutePolicy`) generates entries with hashed
names. The reverse path (`deleteRoutePoliciesConfig`) scans CONFIG_DB
for entries whose key starts with `{serviceName}_` — a prefix scan.
These two code paths never call each other; they agree on names only by
convention.

If the hash is a suffix, the prefix scan works:

```
ROUTE_MAP|TRANSIT_IMPORT_A1B2C3D4|10     ← starts with "TRANSIT_" ✓
PREFIX_SET|TRANSIT_IMPORT_PL_10_F3E2|10   ← starts with "TRANSIT_" ✓
```

If the hash were a prefix, the scan would silently match nothing:

```
ROUTE_MAP|A1B2C3D4_TRANSIT_IMPORT|10     ← does NOT start with "TRANSIT_" ✗
```

The failure mode is particularly dangerous: the forward path (create)
works fine; only the reverse path (delete) breaks — silently leaking
CONFIG_DB entries that accumulate over time and can never be cleaned up.
This breakage would only manifest when a service is removed, which might
not happen in testing for weeks.

**Content hashes are always the last component of a generated name.** The
service name prefix is the anchor that connects the forward and reverse
paths.

### Stale hash cleanup during RefreshService

When a spec changes and `RefreshService` runs, old-hash policy objects
become orphaned. `RemoveService` (called internally) skips shared policy
deletion if other interfaces still use the service. `ApplyService`
creates new-hash objects. The old objects would never be cleaned up by
normal lifecycle.

`RefreshService` solves this with a post-merge scan: after the
remove+apply cycle, it reads existing route policy objects from CONFIG_DB
(Redis in online mode, shadow in offline mode), compares against the set
of objects just created by the apply phase, and deletes the difference.
This is safe because all interfaces sharing a service use the same spec
→ the same hashes, and the shared peer group AF was already updated to
reference new route map names.

---

## 17. BGP Peer Groups — The Protocol's Native Sharing Mechanism

When ten interfaces use the same transit service with BGP routing and
the route map changes, the naive approach writes ten `BGP_NEIGHBOR_AF`
updates — ten Redis writes, ten keyspace notifications, ten frrcfgd
processing events. At a hundred interfaces, it's a hundred. The update
count scales linearly with the number of consumers, and each update is
a window where some neighbors have the old policy and others have the
new one.

BGP already solved this problem. Peer groups are the protocol's native
template inheritance mechanism. newtron creates a `BGP_PEER_GROUP`
named after the service; neighbors reference it; shared attributes
(route maps, admin status) live on `BGP_PEER_GROUP_AF`:

```
BGP_PEER_GROUP|TRANSIT                  → { admin_status: up }
BGP_PEER_GROUP_AF|TRANSIT|ipv4_unicast  → { route_map_in: ..., route_map_out: ... }
BGP_NEIGHBOR|default|10.1.0.1           → { peer_group: TRANSIT, asn: 65002 }
```

When a route map hash changes, one update to the peer group AF
propagates to all neighbors. One write instead of N. One notification
event instead of N. No window where half the neighbors have diverged.

Peer groups are created on first `ApplyService` for a service with BGP
routing, and deleted when the last interface using that service is
removed. Topology-level underlay peers (spine-leaf links) do NOT use
peer groups — each has unique attributes derived from the specific link.

---

# Part V: Code Architecture

A codebase that embodies the right principles in the wrong structure
will lose them. Someone adds a VLAN entry in a second file because
it's convenient. Someone bypasses the Interface to pass an interface
name as a string. Someone writes a config-scanning function as a free
function that takes `configDB` as a parameter — and in a multi-device
context, the wrong `configDB` is passed silently. Each shortcut is
small. Together they erode the guarantees Parts I–IV describe.

These ten principles encode Parts I–IV into code structure — file
boundaries, method placement, type hierarchies — so that the
guarantees are properties of the code, not just intentions documented
above it.

## 18. Single-Owner CONFIG_DB Tables

In a typed language, the compiler prevents two functions from writing
incompatible values to the same structure. CONFIG_DB has no such
protection — it accepts whatever you write, with whatever field names
and value encodings you choose, and the inconsistency surfaces at
runtime, in a daemon log, hours later.

The deeper problem is that the inconsistency is *invisible at the
point of introduction*. Developer A writes `VLAN|Vlan100` with
`vlanid` and `admin_status` in `service_gen.go`. Developer B writes
`VLAN|Vlan100` with just `vlanid` in `topology.go` — omitting
`admin_status` because "it defaults to up anyway." On some platforms
it does. On others, the daemon sees a missing field and silently
ignores the entry. Both paths work in isolation. They produce
different entries for the same table. The bug appears only when both
paths are exercised against the same device, on a platform where the
default doesn't hold — and the debugging session traces through two
files, two commit histories, and two sets of assumptions about what
the fields should be. Single ownership eliminates this class of bug
at the source: if only one file constructs entries for a table,
inconsistency between construction sites is structurally impossible.

Each CONFIG_DB table has exactly one owner — a single file and
function responsible for constructing, writing, and deleting entries
in that table. Composite operations (ApplyService, SetupEVPN,
ConfigureLoopback, topology provisioning) call the owning primitives
and merge their ChangeSets rather than constructing entries inline.

The ownership map:

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
service_ops.go     → NEWTRON_SERVICE_BINDING, ROUTE_MAP, PREFIX_SET,
                      COMMUNITY_SET
```

When the `VLAN` table format changes, you change `vlan_ops.go`. Every
caller — `service_gen.go`, `topology.go`, the CLI — calls into
`vlan_ops.go` and gets the updated format automatically. The change
propagates through callers, not beside them.

This applies at every layer. If `vlan_ops.go` owns `VLAN` table writes,
then `service_gen.go` must call into `vlan_ops.go`, not duplicate the
entry construction. Convenience is not a justification for a second
writer. **One file owns each table; everyone else calls the owner.**

---

## 19. File-Level Feature Cohesion

Every codebase faces a choice of organizing axis: by layer (all reads
together, all writes together, all types together) or by feature (all
VLAN code together, all BGP code together). Organizing by layer keeps
each file internally consistent — but forces a reader who wants to
understand one feature to find it across three files, mentally merge
them, and hope they haven't missed a fourth. The feature exists in the
codebase but not in any single place a reader can point to. It is a
reconstruction, not a location.

newtron organizes by feature. All code for a feature — types, reads,
writes, existence checks, list operations — belongs in one file.
`GetVLAN` and `VLANInfo` belong in `vlan_ops.go` just as much as
`CreateVLAN` does. This is stronger than §18: single ownership governs
*writes*; feature cohesion governs the entire feature — reads, writes,
types, and all.

Four file roles enforce the boundary:

- **`composite.go`** = delivery mechanics only. No CONFIG_DB table or
  key format knowledge.
- **`topology.go`** = provisioning orchestration. Calls config functions
  but never constructs CONFIG_DB keys inline.
- **Each `*_ops.go`** = sole owner of its feature. One file per feature;
  one feature per file.
- **`service_gen.go`** = service-to-entries translation. Calls config
  functions from owning `*_ops.go` files and merges their output.

**If you want to understand a feature, read one file. If you want to
change a table format, change one file.**

---

## 20. Pure Config Functions — Separate Generation from Orchestration

An operation that constructs CONFIG_DB entries, checks preconditions,
opens connections, and applies writes in one function cannot be tested
without a live device, cannot be reused in a different context, and
cannot be read without mentally separating "what entries does this
produce?" from "what else does this do?" The entanglement makes every
simple question expensive to answer.

Entry construction is extracted into **pure config functions** —
functions that take identity parameters and CONFIG_DB state, return
`[]sonic.Entry`, and have no side effects. They don't check
preconditions, don't build ChangeSets, don't log, and don't connect to
devices. They answer one question: "what entries does this operation
produce?"

Config functions come in three forms, each driven by a different need:

- **Package-level functions** for stateless entry construction where the
  subject is not an interface: `createVlanConfig(vlanID, ...)`,
  `createVrfConfig(vrfName)`, `CreateBGPNeighborConfig(peerIP, ...)`.
  These take only the values needed to construct the entry — no device
  state.

- **Node methods** for config-scanning functions that need to read
  ConfigDB to determine what to produce: `n.destroyVlanConfig(vlanID)`,
  `n.destroyVrfConfig(vrfName, l3vni)`, `n.unbindIpvpnConfig(vrfName)`.
  These scan `n.configDB` to find dependent entries for cascading
  teardown. Making them Node methods ensures they always scan the correct
  device's state. See §22.

- **Interface methods** for tables where the interface is the subject:
  `i.bindVrf(vrfName)`, `i.assignIpAddress(ipAddr)`, `i.bindQos(...)`.
  These use `i.name` and `i.node`, eliminating the interface name
  parameter that a free function would require. See §21.

Operations call these functions and wrap the result:

```go
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, ...) (*ChangeSet, error) {
    return n.op("create-vlan", vlanName, ChangeAdd,
        func(pc *PreconditionChecker) { pc.RequireVLANNotExists(vlanID) },
        func() []sonic.Entry { return createVlanConfig(vlanID, opts) },
    )
}
```

This layering serves three purposes:

1. **Testability.** Config functions can be unit-tested with a fake
   ConfigDB — no connections, locks, or preconditions.

2. **Reuse.** The same config function is called by online operations,
   offline composite provisioning, and delete operations. Change the
   table format once; all paths update.

3. **Clarity.** A reader can see exactly what entries an operation
   produces, without wading through orchestration.

**Generate entries in pure functions; orchestrate them in operations.**

---

## 21. Respect Abstraction Boundaries

An abstraction that exists but is not used is worse than no
abstraction at all. It creates two paths to the same outcome — the
path through the abstraction, which carries its guarantees
(precondition checks, identity derivation, isolation), and the path
around it, which carries none. The second path works in testing. It
works in the common case. It fails silently in the edge case the
abstraction was designed to prevent — and by the time someone
discovers the failure, the bypass is load-bearing in three call sites
and can't be removed without a refactor.

This principle is the structural enforcement of §6 (The Interface Is
the Point of Service). §6 says *where* methods belong; this principle
says *callers must use them*.

**Rule 1: If an operation is scoped to an interface, it is a method on
Interface.** The Interface knows its own name — requiring callers to pass
it is an abstraction leak. `i.bindVrf(vrfName)` not
`interfaceVRFConfig(intfName, vrfName)`.

Exception: container membership operations (VLAN members, PortChannel
members) where the container is the subject.

**Rule 2: Config methods belong to the object they describe.** The
object provides its own identity; callers express intent, not identity.

**Rule 3: Node convenience methods delegate, not duplicate.**
`Node.ApplyQoS(intfName, ...)` resolves a name string to an Interface
and calls `iface.ApplyQoS(...)`. It never re-implements the operation.

**Rule 4: No "absolute blocker" for `i.node` access.** Interface methods
that need ConfigDB or SpecProvider use `i.node.ConfigDB()` or `i.node`.
Needing external data is not a reason to avoid being a method — it's
the reason the parent pointer exists.

**Abstractions exist to be used, not bypassed. If an object knows its
own identity, callers must not re-supply it.**

---

## 22. Node as Device Isolation Boundary

In a multi-device system, the most dangerous bugs are the silent ones:
an operation that scans the wrong device's CONFIG_DB, a precondition
check that reads switch1's state while configuring switch2. These bugs
produce valid-looking output — just for the wrong device. They pass
tests that exercise single-device paths and surface only in production,
under multi-device orchestration.

The Node eliminates this class of bug by construction. Every Node
instance owns its own `configDB`, Redis connection, interface map, and
resolved specs. Config-scanning functions are Node methods, not free
functions that take `configDB` as a parameter — so the method *always*
operates on `n.configDB`. A free function like
`destroyVrf(configDB, vrfName, l3vni)` requires the caller to pass the
correct `configDB`; a Node method makes the wrong device impossible.

```go
node1, _ := network.ConnectNode(ctx, "switch1")
node2, _ := network.ConnectNode(ctx, "switch2")

iface1, _ := node1.GetInterface("Ethernet0")  // switch1's Ethernet0
iface2, _ := node2.GetInterface("Ethernet0")  // switch2's Ethernet0

cs1, _ := iface1.ApplyService(ctx, "transit", opts)  // scans switch1's configDB
cs2, _ := iface2.ApplyService(ctx, "transit", opts)  // scans switch2's configDB
```

No shared mutable state crosses the Node boundary. A multi-device
orchestrator is purely an iteration concern — loop over Nodes, call
self-contained methods on each. The Node provides the isolation; the
orchestrator provides the coordination.

**Every device-scoped operation is a Node method. Cross-device
coordination belongs in the orchestrator, not in the Node.**

### The device is its own lock coordinator

Most distributed systems coordinate through a central service — etcd,
ZooKeeper, a database row lock. The lock manager is separate from the
data it protects, which creates a category of failure: the lock service
is reachable but the target device is not, or vice versa. Split-brain
scenarios, stale locks, and coordination infrastructure that must
itself be highly available.

newtron's lock lives on the device's own Redis — specifically STATE_DB,
the ephemeral state database that clears on reboot. When a Node
acquires a lock, it writes a key to the same Redis instance that holds
the CONFIG_DB it is about to modify. No external service, no network
partition between lock and data. If newtron cannot reach the device's
Redis, it cannot reach the lock — and it also cannot write CONFIG_DB,
so there is nothing to coordinate. The lock and the data share a fate:
reachable together or unreachable together.

STATE_DB is the right home for locks — they are operational state, not
configuration. A device that reboots has no active sessions; its locks
should not survive the reboot. CONFIG_DB persists across reboots (via
`config save`); STATE_DB does not. A lock in CONFIG_DB would require
explicit cleanup after every unexpected restart.

This follows directly from single-device scope (§8). newtron never
coordinates across devices — each device is its own coordination
domain. Two newtron instances operating on different devices need no
mutual awareness. Two instances targeting the same device coordinate
through that device's Redis. The coordination topology mirrors the
operational topology: per-device, not centralized.

---

## 23. Abstract Node — Same Code Path, Different Initialization

Most systems that support both offline provisioning and online
operations maintain two code paths — one that generates config into
files, another that applies config to live devices. The two paths
drift. A bug fixed in the online path isn't fixed in the offline path.
A new feature added to provisioning isn't available for incremental
operations. The drift is invisible until a provisioned device behaves
differently from an incrementally configured one.

The Node operates in two modes:

- **Physical mode** (`offline=false`): ConfigDB loaded from Redis.
  Preconditions enforce connected+locked. ChangeSets apply to Redis.
- **Abstract mode** (`offline=true`): Shadow ConfigDB starts empty.
  Operations build desired state. Entries accumulate for composite export.

Same code path, different initialization. The topology provisioner
creates an abstract Node and calls the same methods the CLI uses:

```go
n := node.NewAbstract(specs, name, profile, resolved)
n.RegisterPort("Ethernet0", map[string]string{"admin_status": "up"})
n.ConfigureLoopback(ctx)
n.ConfigureBGP(ctx)
n.SetupEVPN(ctx, loopbackIP)
iface, _ := n.GetInterface("Ethernet0")
iface.ApplyService(ctx, "transit", opts)   // VTEP precondition passes ✓
composite := n.BuildComposite()            // export all accumulated entries
```

This eliminates the need for topology.go to construct CONFIG_DB entries
inline. It calls the same Interface and Node methods, and the shadow
enforces the same ordering constraints that a physical device would.

### Why the shadow matters

Without the shadow, the abstract node has no state for preconditions
to check. `SetVRF("CUSTOMER")` needs to verify that the VRF exists.
`ApplyService` needs to verify that the VTEP is configured. On a
physical device, these preconditions read CONFIG_DB. On an abstract
node with no shadow, there is nothing to read — so preconditions must
be skipped, and the one-code-path guarantee breaks. The abstract node
becomes a different code path that happens to call the same functions
but with weaker guarantees.

`applyShadow(cs)` updates the shadow ConfigDB after every operation,
making each operation's output visible to subsequent preconditions:

- `CreateVRF("CUSTOMER")` → shadow now has `VRF|CUSTOMER`
- `iface.SetVRF(ctx, "CUSTOMER")` → precondition `VRFExists` passes ✓
- `SetupEVPN(ctx, ...)` → shadow now has `VXLAN_TUNNEL`
- `iface.ApplyService(ctx, ...)` → precondition `VTEPConfigured` passes ✓

The shadow preserves correctness by simulating the state transitions a
real device would undergo — so the operations are genuinely identical,
not "identical except we skip the checks."

### Provisioning vs operations

- **Provisioning** (`CompositeOverwrite`): intent replaces reality. The
  abstract node builds the complete desired state, then merges it on top
  of CONFIG_DB.
- **Operations** (`ChangeSet.Apply`): mutations against existing reality.
  The physical node reads current state, applies a delta, and verifies.

The abstract node eliminates a second code path. Whether building a complete
device configuration for composite delivery or applying an incremental change
to a live device, the same methods run — only initialization and delivery
differ.

**Same code path, different initialization. The Interface is the point
of service in both modes.**

---

## 24. Verb-First, Domain-Intent Naming

Systems absorb the vocabulary of their infrastructure. A team that
writes SQL all day names functions after queries. A team that works
with gRPC names functions after message types. A team that talks to
Redis all day starts naming things after Redis: `interfaceSubEntry`,
`vlanBaseConfig`, `bgpTableUpdate`. The infrastructure vocabulary
displaces the domain vocabulary, and the code stops describing what
it *means* and starts describing what it *does to the database*.

The damage is subtle but cumulative. `interfaceBaseConfig(intfName,
nil)` requires opening the function to discover it enables IP routing.
`i.enableIpRouting()` tells you before you look. Multiply that by 40+
config functions and the gap between a codebase that's readable and
one that's merely navigable becomes the gap between a system you can
reason about and one you must trace through.

Two rules resist the drift:

**Rule 1: Verbs come first.** `createVlanConfig`, not `vlanCreate`.
`i.bindVrf(vrfName)`, not `i.vrfBinding(vrfName)`. The verb vocabulary
is deliberate — `create`/`delete` for single entities,
`destroy` for cascading teardown, `enable`/`disable` for behaviors,
`bind`/`unbind` for relationships, `assign`/`unassign` for values,
`generate` for composite entry production. Noun-only names are reserved
for types and constructors.

**Rule 2: Names describe domain intent, not infrastructure mechanics.**
`i.bindVrf(vrfName)` reads as "bind this interface to a VRF" — the
verb plus the receiver is the complete sentence. A network engineer
understands it without knowing CONFIG_DB table names.
`interfaceBaseConfig(intfName, map[string]string{"vrf_name": vrfName})`
says "write these fields to the INTERFACE table" — implementation
detail, not intent.

**Name things after the domain. The infrastructure is an implementation
detail — and implementation details belong in implementations, not in
names.**

---

## 25. Public API Boundary — Types Express Intent, Not Implementation

`pkg/newtron/` is the public API. `network/`, `node/`, and
`device/sonic/` are internal. All external consumers — CLI, newtrun,
newtron-server — import only `pkg/newtron/`.

This boundary was learned the hard way. newtrun — the E2E test
orchestrator — originally imported three internal packages directly.
It constructed `node.ChangeSet` objects, resolved specs via
`network.GetIPVPN()`, accessed `sonic.RouteEntry` structs, and called
`dev.ConfigDBClient().Get()` for verification. The code worked. Then
an internal refactor renamed `sonic.NextHop.IP` to
`sonic.NextHop.Address` — and newtrun broke. A field reorder in
`ChangeSet` broke newtrun. A method signature change in `network`
broke newtrun. Every improvement to the internals required a
corresponding fix to the orchestrator. The internal and external code
were coupled through shared types that exposed implementation, not
intent.

The migration to a public API drew a type boundary. Public types use
domain vocabulary: `RouteNextHop.Address` (a network address),
`WriteResult.ChangeCount` (what happened). Internal types reflect
implementation: `NextHop.IP` (a Redis field), `ChangeSet.Changes` (a
list of Redis commands). The boundary conversion strips
implementation details and maps to domain names. After the migration,
internal refactors stopped breaking the orchestrator — because the
orchestrator no longer knew or cared about internal types.

Five rules crystallized from that migration:

1. **Orchestrators are API consumers, not insiders.** Extend the API;
   don't bypass it with internal imports.
2. **Operations accept names; the API resolves specs.** Callers pass
   string identifiers of intent. The API resolves internally.
3. **Verification tokens are opaque.** `CompositeInfo` flows as an
   opaque handle. Callers never inspect internal state.
4. **Write results report outcomes, not internals.** Raw ChangeSets and
   Redis commands never cross the boundary.
5. **Public types are domain types, not wrappers.** They are designed
   for what callers need to know, not mirrored from internal types.

**Public types expose what callers need; internal types expose what the
implementation needs. The boundary conversion strips and maps as
needed.**

### Uniform boundaries

A structural boundary applies uniformly or not at all. If one type
needs a conversion function at the public API boundary
(`NextHop.IP` → `RouteNextHop.Address`), every type at that boundary
gets a conversion function — including types whose fields happen to
be identical today (`VerificationResult`, `OperationIntent`,
`NeighEntry`).

The temptation is to use type aliases for "trivial" types and
conversion functions for "interesting" ones. This creates an
inconsistent boundary: some types pass through unchanged, others
go through a conversion layer. A reader can't predict the pattern.
And when a trivial type later needs a vocabulary change, the alias
must be replaced with a conversion function — changing every
callsite. The uniform boundary absorbs this: the conversion
function already exists; only its body changes.

This is operational symmetry (§13) applied to the type system.
Symmetric operations says: every create must have a remove, even
if removal is trivial today. Uniform boundaries says: every type
at a boundary gets boundary conversion, even if the conversion is
trivial today. Both resist the temptation to skip structure for
simple cases. Simple cases become complex; the structure must
already be in place when they do.

---

## 26. Transparent Transport — The Middle Layer Has No Logic

Before adding complexity to a layer, ask: **where is the bottleneck?**
If the transport layer contributes nanoseconds and the downstream
operation takes hundreds of milliseconds, every abstraction you add
to the transport — typed message structs, dispatch tables,
intermediate representations — is coordination overhead with zero
latency benefit. You are not optimizing; you are creating places for
drift.

newtron's operations are gated by SSH connections and Redis round-trips.
The HTTP transport is a mechanical translation: decode JSON → construct
closure → send to actor → encode result. There is no business logic
in the transport layer. The handler is the glue — there is nothing
else in the middle.

The alternative — typed message structs for each of over a hundred
operations, dispatch routing tables, intermediate representation
layers — would create as many coordination points between the transport
and the domain. Instead, adding a new endpoint requires one handler
function. No new message types, no dispatch table entries, no
infrastructure changes.

The server serializes access to each device via per-device channels —
one device's operation cannot interleave with another's. SSH connections
are reused within an idle timeout
(default 5 minutes). But CONFIG_DB is refreshed every request (§27) —
the SSH tunnel is reused; the device state is never assumed.

**Optimize where the bottleneck is. Everything else should be as thin
as possible — because every layer with logic is a layer that can
drift.**

---

## 27. Import Direction, Interface State, and Episodic Caching

Three principles that each prevent a specific class of silent bug.

### Import direction — dependencies flow one way

`network/` imports `network/node/`, never the reverse. This is not a
Go convention. It is a blast-radius constraint. If Node could import
Network, a Node operation could call a Network method — and now a
change to how Network resolves specs can break how Node configures
BGP. The two packages would be one package in two directories: any
change to either requires understanding both.

The `SpecProvider` interface breaks what would otherwise be a circular
dependency. Network implements it; Node accepts it at creation time.
The dependency flows through an abstraction, not a concrete type.
When you change a Node method, the blast radius is `node/` plus its
callers. When you change a Network method, Node code is provably
untouched — the import direction guarantees it.

**Dependencies flow from orchestration to primitives, never backward.
Interfaces break the cycles.**

### On-demand Interface state — no cached fields

The Interface struct contains exactly two fields: a parent pointer and
the interface name. All property accessors — `AdminStatus()`, `VRF()`,
`IPAddresses()` — read on demand from the Node's ConfigDB snapshot.

The previous design had 15 cached fields. The bug it invited: an
operation mutates CONFIG_DB (via `cs.Apply()`), then a subsequent read
within the same session returns the cached value — stale. The fix
required remembering to update the cache after every mutation, in
every code path, for every field. Fifteen opportunities to forget,
per operation. The on-demand design has zero cached fields to go
stale.

**Read state from the snapshot, not from cached fields. Snapshots are
refreshed per episode; fields go stale within one.**

### Episodic caching — fresh snapshot per unit of work

newtron caches CONFIG_DB to batch precondition checks into a single
`GetAll()` call rather than a Redis round-trip per check. But a cache
that outlives its purpose becomes a source of stale reads. The
freshness rule:

> Every self-contained unit of work — an **episode** — begins with a
> fresh CONFIG_DB snapshot. No episode relies on cache from a prior
> episode.

- **Write episodes**: `Lock()` refreshes the cache.
- **Read-only episodes**: `Refresh()` at the start.
- **Composite episodes**: `Refresh()` after delivery.

The refresh happens at the *start* of the next episode, not the end
of the current one. Between episodes the cache may be stale — and
that's fine, because no code reads it there. This is the key design
choice: refresh where it serves the reader, not where it follows the
writer.

This is not transactional isolation. The distributed lock coordinates
newtron instances but cannot prevent external writes. Precondition
checks are advisory safety nets — they catch common mistakes but
cannot prevent all race conditions.

**Cache freshness is a property of episodes, not of individual
reads.**

---

# Part VI: Working Conventions

Architecture without discipline drifts. The principles above describe
what the system should be; these conventions describe how to keep it
there — how to name things so they stay parseable, how to patch
platform bugs without inventing parallel infrastructure, how to
verify that a CONFIG_DB path works before committing to it, and what
testing discipline a daemon-driven system requires. They are less
dramatic than the architectural principles, but they prevent the kind
of slow erosion that turns a well-designed system into one that merely
used to be.

## 28. Normalize at the Boundary

Every system that accepts external input faces a choice: normalize it
at every point of use, or normalize it once at the boundary and trust
canonical form inside. The first approach scatters conversion logic
throughout the codebase — and every call site that forgets to normalize
is a latent bug. The second approach concentrates the conversion in one
place and eliminates the category entirely.

newtron normalizes names once, at spec load time. The spec loader
converts all map keys and cross-references to canonical form (ALL
UPPERCASE, hyphens → underscores, `[A-Z0-9_]` only) before returning
specs to callers. After loading, every map key
(`Services["TRANSIT"]`), every cross-reference
(`ServiceSpec.IngressFilter = "PROTECT_RE"`), and every name that flows
into CONFIG_DB key construction is already canonical. Operations code
never calls `NormalizeName()`.

The specific convention — uppercase, underscores, no redundant kind in
key (`ACL_TABLE|PROTECT_RE_IN_1ED5F2C7`, not
`ACL_TABLE|ACL_PROTECT_RE_IN_1ED5F2C7`), numeric IDs with type prefix
(`VNI1001`, `VLAN100`) — is newtron's choice. The principle behind it
is universal: **validate and normalize at system boundaries; trust
canonical form inside the boundary.**

---

## 29. Platform Patching — Fix Bugs, Don't Reinvent Features

When a platform has a bug, the temptation is to route around it — invent
a custom table, add a parallel code path, work around the broken daemon
entirely. This always feels faster than fixing the actual bug. And it
always becomes technical debt that outlives the bug by years.

The test is simple: **does the fix use the same CONFIG_DB signals and
perform the same intended actions?** If yes, it's a valid bug fix. If it
introduces a new table or a new code path replacing the
community-intended mechanism, it's reinvention.

Valid: `newtron-vni-poll` reads the standard `VRF` table and performs the
same `vtysh vrf X; vni N` action. Polling instead of pub/sub is an
implementation detail, not a reinvention.

Invalid: inventing a custom `NEWTRON_VNI` table. Callers must write to a
non-standard table. Community daemons won't see it. When SONiC fixes
the bug, the custom table remains as permanent debt.

SONiC is a large community project. Invented mechanisms interact
unpredictably with community daemons, break across SONiC upgrades, and
create maintenance debt that compounds with every platform. **Patch
what's broken; don't build parallel infrastructure around it.**

---

## 30. Observe Behavior, Don't Trust Schemas

Documentation describes what a system *accepts*. Only observation
reveals what it *does with what it accepts*. A schema says the VLAN ID
field is an integer from 1 to 4094. It does not say that orchagent
silently ignores the entry if `admin_status` is missing. It does not
say that vrfmgrd crashes if the VRF entry arrives before the VLAN
interface it references. It does not say that frrcfgd processes BGP
entries only on startup, not at runtime. The gap between "what the
schema permits" and "what the system actually does" is where the
hardest bugs live — and no amount of documentation reading will close
it.

Before writing any CONFIG_DB entries to implement a SONiC feature:

1. **Find the CLI path.** Read the SONiC CLI source to see what tables
   and fields it writes, in what order.
2. **Run it on a real device.** On a clean device, configure via SONiC
   CLI. Verify end-to-end. Capture CONFIG_DB state as ground truth.
3. **Read the daemon source.** Understand processing order, implicit
   dependencies, and what gets emitted to APP_DB.
4. **Implement.** Write the same entries in the same order.
5. **Test in isolation.** Create a focused test suite before integrating
   into composite suites.

The anti-pattern: read the schema, guess the entries, debug from daemon
logs. The logs tell you *that* something failed; the SONiC CLI path shows
*what* the correct sequence is.

**Schema tells you what's valid. Behavior tells you what works. Only
observation reveals both simultaneously.**

---

## 31. DRY Across Programs

*This is a hygiene convention that extends standard DRY across
newtron's program boundaries.*

Every capability exists in exactly one place, even across programs:
one spec directory (newtlab, newtron, and newtrun all read from it),
one connection mechanism (SSH-tunneled Redis in the device layer), one
verification mechanism (the ChangeSet), one platform definition
(`platforms.json`), one profile per device (newtlab writes runtime
fields *into the same profile* newtron reads).

The anti-pattern: an orchestrator implementing its own CONFIG_DB reader
"because it needs a slightly different format." Every time a capability
is duplicated, the copies drift.

---

## 32. Greenfield — Backwards Compatibility Is a Non-Goal

Compatibility code is the single largest source of accidental complexity
in mature systems. Every `if oldFormat { ... } else { ... }` branch
doubles the test surface and forces every reader to understand both
paths — a cost that compounds with every format change. In a greenfield
system with no installed base, this complexity is entirely self-inflicted.

newtron has no installed base. When a format or API changes, change it
everywhere in one commit. No compatibility shims, no deprecated aliases,
no dual-format detection. If something is unused, delete it. If something
moved, update all references. The public API has one version: current.
All consumers updated in the same commit.

Operations assume a clean, initialized device. `newtron init` is the
one-time boundary where factory state is scrubbed. After init, no
operation checks for or works around legacy formats.

**Write code for the system as it is today, not as it was yesterday.**

### Exception: SONiC release differences

This principle applies to newtron's own code. It does **not** apply to
the SONiC platform. SONiC releases change schemas, daemon behavior, and
YANG models. newtron must support multiple releases — this is
multi-platform support, analogous to a compiler targeting multiple
architectures. Not backwards compatibility.

---

## 33. Multi-Version Readiness — Version Differences as Data, Not Code

When a system must support multiple versions of a platform, the
default approach is `if version >= X { ... } else { ... }` scattered
across every affected code path. Each branch doubles the test matrix.
Each new version adds another clause. The version checks accumulate
until no one can confidently answer "what does this code do on
platform Y?" without tracing every conditional.

The alternative: version differences should be **data** — schema
deltas, capability tables, field mappings — consumed by the same code
path. A config function that takes a version-keyed schema table
produces version-correct entries without branching.

Three architectural boundaries make this possible in newtron:

1. **All Redis through Device.** Version-aware schema resolution can be
   introduced in one package.
2. **All operations through Node.** Every operation has access to the
   detected version through the existing chain.
3. **Config functions are pure.** Adding a version parameter lets them
   produce different entries without changing their structure.

These boundaries exist today for other good reasons (§4, §22, §20).
This principle says: **do not erode them.** Do not add direct Redis
calls outside `device/sonic/`. Do not bypass Node. Do not put schema
knowledge in the transport layer. The seams that make multi-version
possible are the same seams that make the architecture clean.

---

## 34. Testing Discipline

The most dangerous test bug is the one that passes. A verification
check that finds zero items reports "all items pass" — vacuously true,
logically correct, operationally catastrophic. The daemon hasn't
processed entries yet, but the test says everything is fine. The test
suite goes green. The real failure surfaces in production.

SONiC's asynchronous, latency-variable daemons make this class of bug
endemic. Three disciplines prevent it.

### Verification must not pass vacuously

A check that finds zero items to verify must **fail**, not pass. Zero
results means the precondition isn't met — the daemon hasn't processed
entries yet. It does not mean "all checks passed."

```go
// WRONG — passes when results is empty
for _, hc := range results {
    if hc.Status != "pass" { return false }
}
return true  // "all pass" — but zero items were checked

// CORRECT
if len(results) == 0 { return false }
for _, hc := range results {
    if hc.Status != "pass" { return false }
}
return true
```

This class of bug is insidious because it passes in testing — the poll
returns "success" instantly because the precondition hasn't happened yet.

### Observation lag

When a polling check passes but a subsequent observation contradicts it,
the observation is stale — not the poll. Add a brief settle delay
between them. This is a read-side concern in the test suite, not a
`time.Sleep` in the write path.

### Convergence budget

Each CONFIG_DB entry extends the post-provisioning convergence window.
Before adding a new table or entry type, count the entries it adds per
service × per interface × per device. Multiply by the daemon's per-entry
latency. If the total exceeds the test suite's timing margin, increase
the margin preemptively — don't discover it from flaky tests.

Always start tests on a freshly deployed topology. Prior state from
previous test runs corrupts the convergence baseline — the same
vacuous-truth problem from a different angle. A "clean" device that
still has entries from a prior run may pass precondition checks it
should fail, or converge faster than a truly fresh device because the
daemons already processed half the entries.

---

## 35. Documentation Freshness — Audit, Don't Grep

Grep finds what you already know is wrong. It cannot find what you don't
know is wrong — prose descriptions using different wording, glossary
tables, code examples with old field names, contradictory statements
where one section says the old name and another says the new name.

After a schema or API change, the protocol is: initial grep pass →
full-file audit against complete ground truth → one commit, fully clean.

This was learned the hard way. A documentation update that grepped for
four known stale patterns shipped with twelve remaining stale references
that a full-file audit caught afterward. The grep gave confidence the
job was done; the audit proved it wasn't.

**Grep finds what you already know is wrong; audits find what you don't
know is wrong.**

---

# Tensions and Resolutions

A coherent system of principles is not a system without tensions.
Several principles pull in different directions. None are contradictions
— but a reader who encounters one principle without understanding its
boundary with another will misapply it. These tensions are worth naming.

### Intent vs reality and provisioning

§5 establishes that the device is the authority after application. But
provisioning (CompositeOverwrite) is the one operation where intent
replaces reality wholesale. The resolution: provisioning is the initial
act of establishing reality from intent — explicitly bounded as the one
exception. After that moment, all operations mutate the established
reality. The exception proves the rule by being limited to a single,
well-defined operation.

### Fail-closed schema and extensibility

§11's fail-closed schema means unknown tables and fields are errors.
This creates friction when adding new CONFIG_DB tables — the developer
must update `schema.go` before any write works. The friction is
intentional. Adding a CONFIG_DB table is a significant act — it changes
what newtron writes to devices — and should require the developer to
also declare the constraints. The cost is a few minutes per table; the
benefit is catching field-name typos at write time instead of
investigating daemon logs at 2 AM.

### Single owner and composite operations

§18 says one file owns each table. But composite operations like
`ApplyService` touch a dozen tables. Composites don't own tables — they
*call* the owning functions and merge the results. `service_gen.go`
calls `createVlanConfig()`, `createVrfConfig()`, `i.bindVrf()`. It
never constructs a VLAN entry inline. The ownership is preserved through
composition, not violated by it.

### Mechanical reversal vs domain reversal

The ChangeSet (§9) records every mutation, which might suggest
mechanical reversal — "just undo the ChangeSet." But §13 insists that
only domain-level reverse operations are safe, because of shared
resources. The ChangeSet serves verification and preview — "did the
write land?" and "what will change?" — not reversal. Reversal uses
domain operations (`RemoveService`, `DeleteVLAN`) that understand
sharing context. Conflating them would be unsafe.

### Coexistence and baseline prerequisites

§5 says newtron accommodates other writers. It also says baseline
prerequisites are non-negotiable. The resolution: the baseline is a
precondition, not an ongoing constraint. `newtron init` establishes it
once. After that, other writers can modify CONFIG_DB freely within the
established baseline. newtron accommodates their writes. It does not
accommodate a writer that disables unified mode or changes the baseline
itself.

### Reconstruction and device reality

§36 reconstructs expected state from current specs. §5 says the device
is reality — not specs. The resolution: reconstruction answers a
different question. §5 says "what exists on the device is ground truth
for operations." §36 says "what *should* exist on the device is
derivable from specs + bindings." The first governs how operations
behave (read CONFIG_DB, act on what's there). The second enables drift
detection (compare what's there against what should be there). Neither
overrides the other — they answer different questions about the same
device.

### Bounded footprint and rollback history

§38 says CONFIG_DB cost must not grow with time. But the rollback
history (§4.6 of the intent design) stores up to 10 completed commits.
The resolution: 10 is a fixed constant, not a function of time. A
device that has run 50,000 operations has the same 10 history entries
as one that has run 11. The bound is structural — enforced by eviction,
not by policy or operator discipline.

### Greenfield and multi-version

§32 says no backwards compatibility. §33 says support multiple SONiC
releases. §32 applies to newtron's own code (types, APIs, key schemas).
§33 applies to the SONiC platform underneath. Supporting 202411 and
202505 is multi-platform support, like a compiler targeting multiple
architectures. There is no "old newtron format" to maintain — only
multiple current SONiC schemas to produce.

---

# Summary

Legend: **C** = conviction (specific to this project) · **P** = established practice (newtron subscribes) · **S** = style preference

| # | Principle | One-Line Rule | |
|---|-----------|---------------|-|
| 1 | The opinion is in the pattern | newtron constrains the building blocks, not the building | C |
| 2 | Delivery over generation | Generation is solved; delivery — validate, apply atomically, verify, reverse — is not | C |
| 3 | Faithful enforcement | Per-feature reliability doesn't scale; make reliability a property of the pipeline | C |
| 4 | SONiC is a database | Every layer of indirection between tool and system is a layer where information is lost | C |
| 5 | Specs are intent; device is reality | The device is the authority after application; newtron accommodates other writers but requires its baseline | C |
| 6 | Interface is the point of service | What you bind services to becomes your unit of lifecycle, state, and failure | C |
| 7 | Network-scoped definition, device-scoped execution | Define once at the broadest scope; the two lifecycles must not be coupled | C |
| 8 | Scope boundaries | newtron owns single-device configuration delivery; mixing abstraction levels entangles failure domains | C |
| 9 | The ChangeSet is universal | Three representations of "what this operation does" will diverge; one representation cannot | C |
| 10 | Dry-run as first-class | The constraint that makes preview safe is the same one that makes offline provisioning possible | C |
| 11 | Prevent bad writes | A bad write that lands is already damage; prevent it before it reaches the device | C |
| 12 | Verify writes, observe the rest | Assert what you know (your own writes); observe what you don't (the network); return data, not judgments | C |
| 13 | Symmetric operations | A config database without reverse operations only accumulates; never enter a state you can't recover from; use structural proof (lock + intent) over heuristic detection (staleness timers) | C |
| 14 | Write ordering and daemon settling | The database is flat but its consumers are not; config functions encode dependency order in the slice | C |
| 15 | Policy vs infrastructure | Infrastructure is 1:1 with interface; policy objects are shared, created on first reference, deleted on last | C |
| 16 | Content-hashed naming | The name carries proof of its content; two code paths agree without calling each other | C |
| 17 | BGP peer groups | N individual updates scale linearly; BGP's native template mechanism makes it O(1) | C |
| 18 | Single-owner tables | If one file owns a table, inconsistency is structurally impossible | P |
| 19 | File-level cohesion | Organize by feature, not by layer — a feature scattered across files is a reconstruction, not a location | S |
| 20 | Pure config functions | Generate entries in pure functions; orchestrate them in operations | P |
| 21 | Respect abstraction boundaries | An abstraction that exists but is not used is worse than no abstraction at all | P |
| 22 | Node as isolation boundary | The most dangerous multi-device bugs are operations that silently target the wrong device | C |
| 23 | Abstract Node | Two code paths for online and offline will drift; one code path with different initialization cannot | C |
| 24 | Verb-first, domain-intent naming | Systems absorb infrastructure vocabulary; name things after the domain, not the database | S |
| 25 | Public API boundary | Every internal refactor broke the orchestrator — until the type boundary separated intent from implementation; a boundary justified by one type applies uniformly to all | P |
| 26 | Transparent transport | Optimize where the bottleneck is; everything else should be as thin as possible | S |
| 27 | Import direction, interface state, episodic caching | Three principles that each prevent a specific class of silent bug | P |
| 28 | Normalize at the boundary | Normalize once at system boundaries; trust canonical form inside | P |
| 29 | Platform patching | Patch what's broken using the same signals and actions; don't build parallel infrastructure | C |
| 30 | Observe behavior, don't trust schemas | Schema tells you what's valid; behavior tells you what works; only observation reveals both | C |
| 31 | DRY across programs | Every capability exists in exactly one place, even across program boundaries | P |
| 32 | Greenfield | Write code for the system as it is today, not as it was yesterday | C |
| 33 | Multi-version readiness | Version differences should be data, not code; preserve the seams that make this possible | C |
| 34 | Testing discipline | Verification must not pass vacuously; convergence budget scales with entry count | C |
| 35 | Documentation freshness | Grep finds what you know is wrong; audits find what you don't know is wrong | P |
| 36 | Reconstruct, don't record | Derive expected state from authoritative sources (specs + bindings); CONFIG_DB is for intent, not history | C |
| 37 | On-device intent sufficiency | The device carries enough intent (bindings) to reconstruct expected state; binding design must serve both teardown and reconstruction | C |
| 38 | Bounded device footprint | CONFIG_DB cost must be proportional to infrastructure or bounded by a constant, never proportional to operations over time | C |
