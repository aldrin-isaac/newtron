# Design Principles

newtron is a configuration management system for SONiC — the
open-source network operating system that runs on white-box switches
from dozens of vendors. SONiC is unusual among network operating
systems: its entire device configuration lives in a Redis database
called CONFIG_DB. Every VLAN, every BGP session, every interface
binding is a Redis hash. SONiC daemons subscribe to keyspace
notifications on these hashes and program the forwarding ASIC in
response. To configure a SONiC device is to write Redis entries in
the correct format, in the correct order, and verify that the daemons
downstream acted on them correctly. This is both SONiC's power — any
tool that can talk Redis can configure the switch — and its danger:
Redis accepts anything, validates nothing, and the consequences of a
bad write surface minutes later in a daemon log, a silent packet drop,
or an unrecoverable state.

But the hardest problem isn't SONiC-specific. Every configuration
management system eventually faces the same structural problem: it
maintains two representations of a device. One
for what the device should look like — the intent, the desired state,
the template output. Another for what the device does look like — the
live state, the actual CONFIG_DB, the ground truth read back from
Redis. Two data structures, two code paths, one for computing what
should exist and another for reading what does exist, compared by a
third code path that understands neither as well as the code that
produced them.

This is the architecture of drift. Not drift as a bug to be fixed, but
drift as the structural consequence of maintaining parallel
representations that must stay synchronized through every code change,
every edge case, every midnight hotfix. Terraform has this problem.
Kubernetes has this problem. Every system that separates "desired" from
"actual" into distinct types or stores has this problem, because the
separation is itself the source of the divergence it tries to detect.

newtron's central insight is that intent and reality are the same object
viewed from different starting points. The Node is that object. An
offline Node initialized from specs and profiles IS the expected
state — intent before actualization. An online Node loaded from a live
device's CONFIG_DB IS the actual state — reality as it exists. Same
type, same methods, same preconditions, same validation. From this
single design decision — one object, two modes — delivery guarantees,
offline provisioning, drift detection, and crash recovery all follow
as structural consequences rather than independent features.

This document explains the principles behind that architecture — not as
a reference, but as a narrative. Part I states the thesis: the Node,
the properties it produces, and the contract that keeps the system
sound as it grows. Part II establishes the domain model — how newtron
sees SONiC, how it treats device state, and where services live.
Part III describes the opinions: one pattern per primitive, consistently
enforced. Part IV defines the delivery contract — schema validation,
atomic application, post-write verification, symmetric reversal.
Part V explains what the Node records and why intent must be
self-sufficient. Part VI covers shared objects and policy lifecycles.
Part VII shows how the code reflects the model. Part VIII covers
working conventions.

Not all principles carry the same weight. Some are convictions specific
to this project — ways of thinking about the Node, device reality,
isolation, and platform relationships that shaped newtron's
architecture from the ground up. Others are established engineering
practices — single ownership, pure functions, API boundaries — that
newtron subscribes to and enforces rigorously. A few are style
preferences where reasonable alternatives exist. The summary table at
the end marks which is which.

Read this before the HLDs and LLDs. It explains *why* things are the
way they are.

---

# Part I: The Thesis

Three principles define what newtron is and what it promises. Everything
else in this document follows from them.

## 1. The Node — Intent and Reality in One Object

Drift is not a bug in the reconciliation logic. It is the structural
consequence of maintaining parallel representations — one for intent,
one for reality — with separate code paths that must stay synchronized
forever. The Node eliminates the duality at the root. It does not
bridge intent and reality — it *is* both, depending on how it is
initialized.

The Node operates in two modes:

- **Online mode**: ConfigDB loaded from Redis at connection time.
  Preconditions check live device state. ChangeSets apply to Redis. The
  Node *is* the device — its ConfigDB is the device's CONFIG_DB, read
  and written through an SSH-tunneled Redis connection.

- **Offline mode**: Shadow ConfigDB starts empty. Operations build
  desired state. Entries accumulate for composite export. The Node *is*
  the intent — its ConfigDB is what the device should look like once
  the intent is delivered. Intent is inherently offline; delivery is
  what brings it online.

Same type, same methods, same preconditions. The topology provisioner
creates an offline Node and calls the same methods the CLI uses
against an online Node:

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

The shadow ConfigDB is not a mock — it is what makes the code path
genuinely identical. Without it, preconditions would have nothing to
check in offline mode. Applying a service needs to verify the VTEP
exists. Binding an interface to a VRF needs to verify the VRF was
created. The options without a shadow are to skip the checks — breaking
the one-code-path guarantee — or to maintain a separate tracking
mechanism, which creates the second representation this design
eliminates. The shadow simulates the state transitions a real device
would undergo, so preconditions in offline mode are real preconditions
— not stubs.

After every operation, the shadow is updated, making each operation's
output visible to subsequent preconditions:

- Create the VRF "CUSTOMER" — shadow now has `VRF|CUSTOMER`
- Bind an interface to "CUSTOMER" — precondition `VRFExists` passes
- Configure the VXLAN tunnel endpoint — shadow now has `VXLAN_TUNNEL`
- Apply a service on the interface — precondition `VTEPConfigured` passes

An offline Node with all intents actuated IS an online Node's expected
state. An online Node's loaded CONFIG_DB IS what an offline Node would
produce if initialized from the same specs and profile. The two modes
are not analogous — they are literally the same computation, differing
only in where the initial state comes from and where the final state
goes.

This is the thesis. Delivery guarantees, offline provisioning, drift
detection, intent recording — all follow from a system where intent and
reality share a type, a code path, and a set of invariants.

### Provisioning vs operations

Two modes of the same object yield two modes of use — not as separate
systems, but as different initializations of the same computation.

**Provisioning** — what the industry calls Day-1 or build provisioning
— is the one operation where intent replaces reality entirely. An
offline Node builds the complete desired state — every VLAN, every VRF,
every BGP session, every service binding — by running the same methods
in the same order that an operator would run interactively. The
accumulated entries are then delivered as a single composite,
overwriting whatever the device had before. This is the only path where
newtron asserts authority over device state.

**Operations** — Day-2 in industry parlance — are mutations against
existing reality. An online Node loads the device's current CONFIG_DB,
checks preconditions against what actually exists, computes a delta,
and applies it. The device's state before the operation is the starting
point — not a spec file, not a template, not a desired-state store. If
someone edited CONFIG_DB directly between operations, the online Node
sees that edit as the new reality and operates on it without complaint.

The same methods run in both cases. The same preconditions fire. The
same schema validation catches invalid entries. Only initialization and
delivery differ: offline Nodes start empty and export composites;
online Nodes start from Redis and apply ChangeSets. This is not a
convenience — it is the guarantee. A feature added to one mode is
immediately available in the other, because there is no other. A bug
fixed in one mode is fixed in both, because there is only one code path
to fix.

---

## 2. Three Properties of One Code Path

The one-code-path design produces three structural properties that
would otherwise need to be built and maintained as independent features
— with independent bugs, independent test suites, and independent
drift.

**1. Delivery guarantees.** Because preview and execution share the
same code path, the ChangeSet that previews an operation IS the
ChangeSet that executes it — same object, not a copy, not a
re-derivation. Preview and execution cannot diverge because there is
nothing to diverge. (See §11 for the ChangeSet mechanism.)

**2. Offline provisioning.** The offline Node computes without writing
to a device, so a complete device configuration can be built in memory
and delivered later as a single atomic operation. This is not a second
system — it is the same system in offline mode. Adding a new feature
to the incremental path automatically makes it available in the
topology provisioner, because the provisioner calls the same methods
on an offline Node. A service type that works interactively works in
full-device provisioning on the same day, exercising the same code,
validated by the same preconditions. (See §12.)

**3. Drift detection.** Comparing what a device should look like against
what it does look like normally requires a separate "expected state"
representation — a desired-state store, a state file, a journal of
applied operations. In newtron, the expected state IS an offline Node
initialized from the device's specs, profile, and intent records.
Reconstruct the offline Node, load the online Node from Redis,
compare the two ConfigDBs, and the diff is the drift. No journal, no
state file, no reconciliation engine — the expected state is computed
from the same code path that would produce it on a real device. If the
code path is correct for deployment, it is correct for drift detection,
because it is the same code path. (See §19.)

These three properties reinforce each other. Delivery guarantees mean
that what was previewed is what was applied — so drift detection can
trust the expected state. Offline provisioning uses the same code path
as reconstruction — so drift detection is exactly as precise as
deployment. And drift detection closes the loop — the consequences of
any divergence between intent and reality are immediately visible,
whether that divergence came from a failed apply, an external edit, or
a daemon that rewrote a table.

A system that maintained these as independent features would need three
implementations kept in sync. A system where they are consequences of a
single design decision — one code path, two modes — gets them for free
and cannot lose one without losing the architecture.

---

## 3. The Enforcement Contract

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
not of each feature that passes through it. The Node's operation method
is where the pipeline lives — preconditions, schema validation,
ChangeSet production, shadow update, intent recording. Every mutating
operation flows through this pipeline. The one-code-path design (§1) is
what makes this possible: because offline and online modes share the
same pipeline, a guarantee proven in one mode holds in both. The
pipeline is not an aspiration documented above the code — it is the
code.

Concretely, the pipeline enforces four guarantees — schema validation,
atomic application, post-write verification, and symmetric reversal —
for every mutating operation, regardless of which primitive produced
it. §10 explains why each guarantee matters. §11–§16 describe the
machinery that implements them. The point here is structural: every
guarantee is a property of the pipeline, not of any specific primitive.
When a new primitive is added, it inherits them automatically. When an
existing primitive changes, they remain. The primitives are the
variable; the delivery contract is the invariant.

The opinions (§9) define what each primitive looks like. The delivery
pipeline (§10) ensures each primitive arrives safely. Together they
form the enforcement contract — the reason newtron can accumulate
capability without accumulating fragility.

newtron is never done — it is always acquiring new primitives, not
converging on a final set. The enforcement contract is what keeps that
growth sound.

---

# Part II: The Domain Model

The Node mediates between intent and reality — but what does reality
look like, and where does intent come from? These five principles
establish the domain model: SONiC as a database, specs as intent,
CONFIG_DB as reality, the interface as the point of service, and the
scope boundaries that keep each tool's failure domain contained.

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

newtron interacts with SONiC exclusively through Redis — because Redis
*is* the data, not a description of it. CONFIG_DB writes
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

- **Service operations** trust the intent record as ground reality.
  `ApplyService` reads CONFIG_DB for idempotency filtering on shared
  infrastructure (does the VLAN or VRF already exist?). `RemoveService`
  reads the NEWTRON_INTENT record — not CONFIG_DB tables, not
  specs — to determine what to tear down.

### Intent records are ground reality for teardown

NEWTRON_INTENT records live on the device, not in spec files.
When an operation is applied to a resource, newtron writes an intent
record to CONFIG_DB that captures exactly what was applied — which
VLANs, VRFs, ACLs, and VNIs were created for that operation.

The intent record is the sole input for teardown. `RemoveService` does not
re-derive the removal from the spec, because the spec may have changed
between apply and remove. What matters is what was *actually applied*.
For example, EVPN overlay parameters (L3 VNI, its transit VLAN) are
stored in the intent record so removal can tear down overlay infrastructure
without looking up the VPN spec — which may have changed since the
service was applied.

When adding a new forward operation that creates infrastructure, the
question to ask is: *can the reverse operation find everything it needs
in the intent record alone?* If not, the intent record is incomplete.

### Why newtron is not a reconciler — and why it is a superset of one

A reconciler runs a continuous loop: read desired state, read actual
state, compute delta, apply delta. Terraform's `plan` + `apply` is
the manual version of this loop. Kubernetes controllers are the
automated version. Both require a single canonical source of desired
state to diff the device against.

newtron does not run a reconciliation loop — but it has every
capability a reconciler has. Reconstruction (§19) produces expected
state from specs and intent records. Drift detection (§2) diffs
expected against actual. Reprovision (§19) delivers the fix through
the same pipeline that created the state. This is Terraform's
`plan` + `apply` cycle, using the same code path that provisions
and operates the device.

What newtron adds beyond a reconciler:

- **Incremental operations with domain-aware reversal.** Terraform
  diffs full state. newtron also has operation-level primitives
  (`ApplyService` / `RemoveService`) with intent-tracked reversal —
  forward and reverse are symmetric domain operations (§15), not
  mechanical state diffs.

- **Crash recovery.** Terraform's state file can diverge from reality
  after a crash — `terraform import` and manual state surgery are the
  recovery paths. newtron's intent lives on the device, detected
  automatically on `Lock()`, with mechanical rollback via
  `dispatchReverse()` (§17).

- **On-device state.** Terraform stores state in a file or remote
  backend, separate from the target. newtron's intent records live on
  the device. No external state to lose, corrupt, or diverge.

- **Same code path online and offline.** Terraform's plan runs against
  provider APIs. newtron's offline Node runs the same methods
  that run online — the composite IS the plan, generated by the same
  code that executes incremental operations.

For incremental operations, no single canonical desired state exists.
The "desired state" is the device's current state plus the requested
change, and the current state can only be read from the device itself.
This is why newtron does not run a reconciliation loop — but it is not
why newtron lacks reconciliation capabilities. It has them. It uses
them on demand (drift detection, reprovision), not continuously.

Two opinionated architectures cannot converge on the same device.
newtron's device-reality checks minimize harm — they don't accommodate
existing config from a different architectural model.

**The device is the reality; specs are the intent; operations transform
reality using intent. Reconciliation is available; it is not the
operating mode.**

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
return structured data, not judgments (§14), so any orchestrator can
make its own decisions.

Good automation development requires a virtual twin — the ability to
stand up a faithful replica of the target network running real SONiC
software and exercise every primitive against it. Without this, you
are testing against documentation, not behavior (§35). newtlab
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

# Part III: The Opinions

A Node without opinions is a general-purpose Redis writer — capable of
anything, guaranteeing nothing about consistency across primitives.
These two principles define what the Node enforces: one pattern per
primitive, and a delivery pipeline that makes every pattern stick.

## 9. The Opinion Is in the Pattern

The Node enforces opinions — but not at the level most opinionated
tools choose. It does not prescribe a topology. It prescribes the
bricks.

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

## 10. Delivery Over Generation

Because the Node unifies intent and reality in a single code path —
specs flow in through SpecProvider, device state flows in through
ConfigDB, and every mutation flows out through a ChangeSet — the
delivery guarantees are not bolted on after the fact. They are
structural properties of the pipeline described in §3.

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
   it did — on the device, as an intent record. Teardown reconstructs
   from what was actually applied, not from current device state that may
   have changed since. No guessing, no scanning, no "does this entry
   belong to me?"

These guarantees are properties of the pipeline, not of any specific
primitive. When a new primitive is added, it inherits them automatically.
When an existing primitive changes, they remain. The pipeline absorbs
growth; individual primitives do not need to earn their own reliability.

---

# Part IV: The Delivery Contract

Opinions and delivery guarantees are promises. Promises without
machinery are aspirations. These six principles describe the concrete
mechanisms — the ChangeSet that makes every operation previewable,
executable, and verifiable; the validation that prevents bad writes
from reaching the device; the symmetric operations that ensure
nothing newtron creates becomes permanent debris; and the write
ordering that respects SONiC's invisible dependency graph.

## 11. The ChangeSet Is the Universal Contract

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

## 12. Dry-Run as First-Class Mode

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
of the same constraint that makes dry-run work. The offline Node
(§1) exists because of this forced separation — computation that never
touches Redis can run against a shadow ConfigDB just as well as a real
one. The constraint that makes preview possible is the same constraint
that makes offline mode possible.

**Preview first. Execute deliberately. The same code does both — and
the constraint that makes preview possible is what makes offline
provisioning possible.**

---

## 13. Prevent Bad Writes, Don't Just Detect Them

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

## 14. Verify Your Writes; Observe Everything Else

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

## 15. Symmetric Operations — What You Create, You Can Remove

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
functions that construct CONFIG_DB entries (see §26):

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
can be safely removed. Dependency checking now scans the node's intent
collection (actuated intents) in addition to CONFIG_DB, ensuring that
shared resource removal accounts for all declared consumers — not just
what happens to be present in the database at scan time. Rollback is
therefore an orchestrator concern: if an orchestrator applies services to
three interfaces and the second fails, it calls `RemoveService` on the
first — not "reverse the first ChangeSet." newtron provides
reference-aware building blocks; the orchestrator decides when to
invoke them.

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

## 16. Write Ordering and Daemon Settling

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

# Part V: What the Node Records

Operations flow through the Node and produce CONFIG_DB entries. But
the Node must also remember what it has done — for crash recovery, for
drift detection, for teardown months later by someone who wasn't there
when the operation was applied. These four principles govern what the
Node records and why.

## 17. Unified Intent Model

CONFIG_DB entries are ephemeral explanations of what the device should
do right now. They say nothing about who created them, why, or what
else was created alongside them. An operator who returns six months
later to remove a service finds entries but no provenance — no way to
know which entries belong to that service, which are shared with
others, and which were edited by hand after the original apply. Without
a record of what was intended, teardown is guesswork and drift
detection is impossible.

The intent record fills this gap. It is a composite of primitives,
bound to a resource, with a state lifecycle. The resource might be an
interface (service intent), a device (baseline intent), or a VRF (routing
intent) — the record structure is the same. Operation identifies which
composite was applied. Name references the spec that was consumed, if any.
Params carry the resolved values that were actually written — the ground
reality for teardown and reconstruction.

Intent records move through three states. *Unrealized* means declared but
not yet applied — the intent exists as a record of what should happen, but
no CONFIG_DB entries have been written for it. *In-flight* means actuation
is in progress — the record has been persisted, the ChangeSet is being
applied, but the operation has not completed. If the process crashes
during this state, the intent becomes a zombie: the record survives but
the operation is incomplete, and the next operator can detect and recover
from it. *Actuated* means the operation completed successfully — the
CONFIG_DB entries exist and match what the intent record describes.

There is no type discriminator field that says "this is a service intent"
or "this is a VRF intent." The Operation field (e.g., `apply-service`,
`configure-bgp`, `setup-evpn`) identifies the composite that was applied,
and the code path for that operation knows what Params to expect. Adding a
new operation type requires no schema change to the intent record — only a
new Operation value and the corresponding forward/reverse implementations.

The Node intermediates all intent. On connect, the node loads existing
intent records from CONFIG_DB. Mutations (apply, remove, refresh) update
the node's intent collection as part of the operation. In offline mode,
intent records accumulate alongside shadow CONFIG_DB entries and are
exportable to topology composites. This makes the node the single point
of intent truth for its device — whether online or offline, whether
connected or computing.

Rollback operates at the intent level, not the ChangeSet level. To roll
back an operation, the orchestrator calls the domain-level reverse for that
intent (RemoveService, RemoveQoS, etc.). Shared resources are handled by
the same dependency-aware logic described in §15: the reverse operation
scans actuated intents for remaining consumers before deleting shared
infrastructure. Mechanical ChangeSet reversal remains unsafe for the same
reason it has always been unsafe — it lacks the sharing context that only
domain operations possess.

This unifies the former NEWTRON_SERVICE_BINDING with the general
NEWTRON_INTENT model. Service bindings were always intent records — they
captured what was applied, served as the sole input for teardown, and
carried enough information for reconstruction. The unified model simply
recognizes that every newtron-managed resource deserves the same
treatment: a persistent record of what was applied, surviving crashes,
sufficient for both reversal and drift detection, bounded by
infrastructure rather than time.

---

## 18. On-Device Intent Is Sufficient for Reconstruction

The device carries enough intent to reconstruct its expected CONFIG_DB
state. `NEWTRON_INTENT` records which operations are applied to
which resources, with all parameters needed for both teardown and
reconstruction. Combined with current specs, the intent record tells you
exactly what CONFIG_DB entries should exist. No external history, no
journal replay, no off-device state needed.

This closes the gap between provisioning (topology-defined baseline)
and the evolved device (post-provision operations). The topology gives
you the baseline — `GenerateDeviceComposite()` with current specs
produces the expected CONFIG_DB after provisioning. The intent records give
you everything that happened since — each one carries enough
information to replay the operation on an offline Node and produce
the incremental CONFIG_DB entries. Together, they reconstruct the full
expected state at any point in the device's lifetime.

The existing principle "intent records must be self-sufficient for reverse
operations" (§15) extends here: **intent records must be self-sufficient for
reconstruction of expected state.** Teardown is one consumer; drift
detection is another. Same data, different purpose.

This makes a specific demand on intent record design: every field needed to
regenerate the expected CONFIG_DB entries must be stored in the intent record.
When adding a new forward operation, ask not only "can the reverse find
everything it needs in the intent record alone?" but also "can reconstruction
regenerate the expected entries from the intent record alone?" If a future
forward operation creates infrastructure that the intent record can't
reconstruct, drift detection breaks silently — and there is no test
that catches it until someone asks "why doesn't drift show this entry?"

**The device carries its own intent — not as history, but as living
records that evolve as operations are applied and removed.**

---

## 19. Reconstruct, Don't Record

§11 through §16 govern the lifecycle of a single operation: build a
ChangeSet, preview it, validate it, apply it, verify it, reverse it.
But an operator eventually asks a question that spans the device's
lifetime: "does this device match what I intended?" This principle
answers that question without introducing an append-only journal or any
mechanism that grows with time.

The question "what should this device look like?" is always answerable
from three persistent inputs: specs (on disk), device profile (on disk),
and intent records (in CONFIG_DB). Reconstructing expected state from
these inputs — by creating an offline Node from specs and intents,
running the same code paths that provisioning and operations use (§1)
— is cheaper, simpler, and more correct than maintaining a
chronological journal and replaying it. Reconstruction works because
the offline Node IS the expected state: the same operations that
produced the real CONFIG_DB entries produce identical entries when run
against a shadow ConfigDB.

A journal is a second copy of information that already exists in a more
authoritative form. Specs change; the journal doesn't know. Profiles
change; the journal doesn't know. The reconstruction approach uses
current specs by definition — there is no stale copy to diverge.

This principle extends §14 (verify writes, observe the rest) from
immediate to historical. newtron can verify the cumulative effect of
all its writes against current intent at any time — not just immediately
after each operation — by reconstructing expected state and diffing
against actual CONFIG_DB.

CONFIG_DB contains intent — what the device should look like — not
history. The in-flight intent record (§17) is intent: "I am currently
doing X." Completed operation history is not intent. It belongs in
structured logging or an external store, not in the device's
configuration database.

**Derive expected state from authoritative sources; don't maintain a
parallel record of it.**

### Remediation is reprovision

Drift detection produces a precise diff — missing entries, extra entries,
modified fields. The natural question is: why not apply a surgical fix?
Add the missing entry. Delete the extra one. Correct the modified field.

Because a surgical reconciler is a second write path. It would construct
CONFIG_DB entries outside the Node's operation pipeline — without
ChangeSet tracking, without schema validation, without precondition
checks, without intent recording. It would bypass the one-code-path
thesis to fix a problem that the one-code-path thesis detected. The
cure would undermine the diagnostic.

Drift remediation is reprovision: reconstruct expected state from specs
and profile (§1), deliver it as a `CompositeOverwrite` (§10), verify
it landed (§14). The same code path that detected the drift produces
the fix. No second system.

Reprovision is disruptive on SONiC. `CompositeOverwrite` performs
`DEL` + `HSET` for every owned entry, and SONiC's keyspace notification
model fires on every write — even when the value is unchanged. Every
subscribing daemon tears down and rebuilds internal state for every
entry it watches. A clean device that isn't drifted still reconverges
fully. This is not newtron's limitation — it is SONiC's notification
model. Any tool that writes to CONFIG_DB faces the same constraint:
Terraform, Ansible, a raw `redis-cli` script. The disruption is
proportional to the platform's inability to distinguish "unchanged
write" from "mutation," not to the tool's architecture.

The architecture already produces the artifact that non-disruptive
reconciliation needs: a complete expected-state composite, generated
by the same code path that would provision the device from scratch. If
SONiC supported diff-aware commit — writing desired state and notifying
daemons only on actual changes, as JunOS does with `commit` — the same
`CompositeOverwrite` path would become non-disruptive without code
changes. The code path is ready. The platform is not yet.

---

## 20. Bounded Device Footprint

Every newtron-owned record in CONFIG_DB must have a cost proportional
to the device's physical infrastructure (ports, interfaces, VLANs) or
bounded by a fixed constant — never proportional to the number of
operations performed over time.

CONFIG_DB is operational infrastructure: `config save` serializes it,
`config reload` deserializes it, daemons scan it at startup. Unbounded
growth degrades device operations for data that no SONiC daemon will
ever consume.

The intent record is O(resources) per device — one per managed resource
(interface, VRF, overlay). The rollback history is O(1) per device —
capped at 10 entries, oldest evicted. Neither grows with the number of
operations performed over the device's lifetime.

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

# Part VI: Shared Objects and Policy

Parts I–V treat each operation as self-contained: one ChangeSet, one
interface, one service. But CONFIG_DB resources are not always
self-contained. ACLs, route maps, prefix sets, and peer groups are
shared — created by one operation, consumed by many, and dangerous to
delete before the last consumer is gone. These three principles govern
how shared objects coexist with independent lifecycles.

## 21. Policy vs Infrastructure — Shared Objects Have Independent Lifecycles

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

The separation also enables content-hashed naming (§22) — because
policy objects have identities independent of any interface, their
names can encode their content, allowing automatic change detection and
blue-green updates without touching every consumer simultaneously.

---

## 22. Content-Hashed Naming — Version Shared Objects by What They Write

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

## 23. BGP Peer Groups — The Protocol's Native Sharing Mechanism

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

# Part VII: Code Architecture

Parts I–VI describe what the system guarantees. These nine principles
encode those guarantees into code structure — file boundaries, method
placement, type hierarchies — so that the architecture is a property
of the code, not an intention documented above it.

## 24. Single-Owner CONFIG_DB Tables

The ChangeSet (§11) is the single representation of what an operation
does. That guarantee breaks if two files construct entries for the same
table with different field sets — the ChangeSet faithfully records
whichever construction site ran, and the inconsistency surfaces as a
daemon failure on a different platform, hours later.

Each CONFIG_DB table has exactly one owner — a single file responsible
for constructing, writing, and deleting entries in that table.
Composites call the owning primitives and merge their ChangeSets.

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
service_ops.go     → NEWTRON_INTENT, ROUTE_MAP, PREFIX_SET,
                      COMMUNITY_SET
```

**One file owns each table; everyone else calls the owner.** When the
table format changes, the change propagates through callers, not beside
them.

---

## 25. File-Level Feature Cohesion

§24 governs writes. This principle governs the entire feature — reads,
writes, types, existence checks. All code for a feature belongs in one
file. `GetVLAN` and `VLANInfo` belong in `vlan_ops.go` just as much as
`CreateVLAN` does.

Four file roles enforce the boundary:

- **`composite.go`** = delivery mechanics only (§10). No CONFIG_DB
  table or key format knowledge.
- **`topology.go`** = provisioning orchestration (§1). Calls config
  functions but never constructs CONFIG_DB keys inline.
- **Each `*_ops.go`** = sole owner of its feature.
- **`service_gen.go`** = service-to-entries translation. Calls config
  functions from owning `*_ops.go` files and merges their output.

**If you want to understand a feature, read one file. If you want to
change a table format, change one file.**

---

## 26. Pure Config Functions — Separate Generation from Orchestration

The offline Node (§1) works because entry construction has no side
effects. If a config function opened connections or checked
preconditions, it couldn't run offline — and the one-code-path thesis
would break. Purity is not a style preference; it is what makes
offline mode possible.

Config functions take identity parameters and CONFIG_DB state, return
`[]sonic.Entry`, and do nothing else. Three forms:

- **Package-level functions** for stateless construction:
  `createVlanConfig(vlanID, ...)`, `createVrfConfig(vrfName)`.
- **Node methods** for config-scanning teardown that must read the
  correct device's state (§28): `n.destroyVlanConfig(vlanID)`,
  `n.unbindIpvpnConfig(vrfName)`.
- **Interface methods** for tables where the interface is the subject
  (§27): `i.bindVrf(vrfName)`, `i.assignIpAddress(ipAddr)`.

Operations call these functions and wrap the result:

```go
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, ...) (*ChangeSet, error) {
    return n.op("create-vlan", vlanName, ChangeAdd,
        func(pc *PreconditionChecker) { pc.RequireVLANNotExists(vlanID) },
        func() []sonic.Entry { return createVlanConfig(vlanID, opts) },
    )
}
```

The same config function is called by online operations, the offline
topology provisioner, and delete operations. Change the table
format once; all paths update — because there is only one path.

**Generate entries in pure functions; orchestrate them in operations.**

---

## 27. Respect Abstraction Boundaries

§6 says the Interface is the point of service. This principle says
callers must use it. A caller that bypasses Interface to pass an
interface name as a string to a free function has two paths to the
same outcome — the path through the abstraction, which carries its
guarantees, and the path around it, which carries none. The second
path works until the edge case the abstraction was designed to prevent.

**Rule 1: If an operation is scoped to an interface, it is a method on
Interface.** `i.bindVrf(vrfName)` not `interfaceVRFConfig(intfName,
vrfName)`. Exception: container membership (VLAN members, PortChannel
members) where the container is the subject.

**Rule 2: Config methods belong to the object they describe.** The
object provides its own identity; callers express intent, not identity.

**Rule 3: Node convenience methods delegate, not duplicate.**
`Node.ApplyQoS(intfName, ...)` resolves a name to an Interface and
calls `iface.ApplyQoS(...)`. It never re-implements the operation.

**If an object knows its own identity, callers must not re-supply it.**

---

## 28. Node as Device Isolation Boundary

The Node (§1) is not just the unit of intent — it is the unit of
isolation. Every Node instance owns its own `configDB`, Redis
connection, interface map, and resolved specs. Config-scanning
functions are Node methods, not free functions that take a database
handle — so `n.destroyVrfConfig()` *always* scans the correct device.
A free function requires the caller to pass the right `configDB`; a
Node method makes the wrong device structurally impossible.

```go
iface1, _ := node1.GetInterface("Ethernet0")  // switch1's Ethernet0
iface2, _ := node2.GetInterface("Ethernet0")  // switch2's Ethernet0

cs1, _ := iface1.ApplyService(ctx, "transit", opts)  // scans switch1's configDB
cs2, _ := iface2.ApplyService(ctx, "transit", opts)  // scans switch2's configDB
```

No shared mutable state crosses the Node boundary. A multi-device
orchestrator is purely an iteration concern — loop over Nodes, call
self-contained methods on each.

**Every device-scoped operation is a Node method. Cross-device
coordination belongs in the orchestrator, not in the Node.**

### The device is its own lock coordinator

newtron's lock lives on the device's own Redis — STATE_DB, the
ephemeral state database that clears on reboot. The lock and the data
it protects share the same Redis instance: reachable together or
unreachable together. No external lock service, no split-brain between
lock manager and target device.

STATE_DB is the right home — locks are operational state, not
configuration. A rebooted device has no active sessions; its locks
should not survive the reboot. CONFIG_DB persists across reboots;
STATE_DB does not.

This follows directly from single-device scope (§8). Each device is
its own coordination domain. Two newtron instances targeting different
devices need no mutual awareness. Two targeting the same device
coordinate through that device's Redis. The coordination topology
mirrors the operational topology: per-device, not centralized.

---

## 29. Verb-First, Domain-Intent Naming

Symmetric operations (§15) require that every forward action has a
reverse. If functions are named after infrastructure
(`interfaceBaseConfig`, `vlanSubEntry`), the forward/reverse
relationship is invisible — you can't tell from the name whether
`interfaceBaseConfig(intfName, nil)` creates, deletes, or modifies.
Domain-intent naming makes the symmetry legible:
`i.bindVrf` / `i.unbindVrf`, `createVlanConfig` / `deleteVlanConfig`,
`enableArpSuppression` / `disableArpSuppression`.

**Verbs come first.** The verb vocabulary maps to operational symmetry:
`create`/`delete` for entities, `destroy` for cascading teardown,
`enable`/`disable` for behaviors, `bind`/`unbind` for relationships,
`assign`/`unassign` for values, `generate` for composite production.
Noun-only names are reserved for types and constructors.

**Names describe domain intent.** `i.bindVrf(vrfName)` — a network
engineer understands it without knowing CONFIG_DB table names.
`interfaceBaseConfig(intfName, map[string]string{"vrf_name": vrfName})`
describes what it does to the database, not what it means.

---

## 30. Public API Boundary — Types Express Intent, Not Implementation

`pkg/newtron/` is the public API. `network/`, `node/`, and
`device/sonic/` are internal. All external consumers — CLI, newtrun,
newtron-server — import only `pkg/newtron/`.

This boundary exists because the ChangeSet (§11), the Node (§1), and
the device layer (§4) must be free to evolve without breaking
consumers. When newtrun imported internal packages directly, every
internal refactor — a field rename, a method signature change, a type
reorder — broke the orchestrator. The internal and external code were
coupled through types that exposed implementation, not intent.

Public types use domain vocabulary: `RouteNextHop.Address`,
`WriteResult.ChangeCount`. Internal types reflect implementation:
`NextHop.IP`, `ChangeSet.Changes`. The boundary conversion strips
implementation details and maps to domain names.

Five rules:

1. **Orchestrators are API consumers, not insiders.** Extend the API;
   don't bypass it.
2. **Operations accept names; the API resolves specs internally.**
3. **Verification tokens are opaque.** `CompositeInfo` flows as a
   handle. Callers never inspect internal state.
4. **Write results report outcomes, not internals.**
5. **Public types are domain types, not wrappers.**

The boundary applies uniformly. Every type that crosses it gets a
conversion function — including types whose fields happen to be
identical today. This is operational symmetry (§15) applied to the
type system: every type at a boundary gets boundary treatment, even
if the treatment is trivial today. Simple cases become complex; the
structure must already be in place when they do.

---

## 31. Transparent Transport — The Middle Layer Has No Logic

The Node (§1) and the ChangeSet (§11) are where the guarantees live.
The HTTP transport between them and the caller is a mechanical
translation: decode JSON → construct closure → send to per-device
actor → encode result. No business logic. No typed message structs.
No dispatch tables. Adding a new endpoint requires one handler
function — nothing else changes.

The server serializes access to each device via per-device channels
(§28). SSH connections are reused within an idle timeout. CONFIG_DB is
refreshed every request (§32) — the tunnel is reused; the device state
is never assumed.

**Every layer with logic is a layer that can drift. The transport has
no logic.**

---

## 32. Import Direction, Interface State, and Episodic Caching

Three structural rules that each prevent a class of silent bug.

### Import direction — dependencies flow one way

`network/` imports `network/node/`, never the reverse. The
`SpecProvider` interface (§7) breaks what would otherwise be a circular
dependency: Network implements it; Node accepts it at creation time.
When you change a Node method, the blast radius is `node/` plus its
callers. When you change a Network method, Node code is provably
untouched — the import direction guarantees it.

### On-demand Interface state — no cached fields

The Interface (§6) has exactly two fields: a parent pointer and the
interface name. All accessors read on demand from the Node's ConfigDB
snapshot. A previous design cached 15 fields — fifteen opportunities
per operation for a mutation to leave stale state behind. The
on-demand design has zero.

### Episodic caching — fresh snapshot per unit of work

The Node (§1) caches CONFIG_DB to batch precondition checks. The
freshness rule: every self-contained unit of work — an **episode** —
begins with a fresh snapshot. No episode relies on cache from a prior
episode.

- **Write episodes**: `Lock()` refreshes the cache.
- **Read-only episodes**: `Refresh()` at the start.
- **Composite episodes**: `Refresh()` after delivery.

This is not transactional isolation. The distributed lock coordinates
newtron instances but cannot prevent external writes. Precondition
checks are advisory safety nets, not guarantees.

---

# Part VIII: Working Conventions

Seven conventions that prevent the slow erosion of Parts I–VII.

## 33. Normalize at the Boundary

Content-hashed naming (§22) requires that two code paths computing the
same hash from the same spec get identical results. If one path sees
`"protect-re"` and another sees `"PROTECT_RE"`, the hashes diverge and
blue-green migration breaks silently. Boundary normalization is the
precondition.

newtron normalizes names once, at spec load time: ALL UPPERCASE,
hyphens → underscores, `[A-Z0-9_]` only. After loading, every map key
(`Services["TRANSIT"]`), every cross-reference
(`ServiceSpec.IngressFilter = "PROTECT_RE"`), and every name that flows
into CONFIG_DB key construction is already canonical. Operations code
never calls `NormalizeName()`.

---

## 34. Platform Patching — Fix Bugs, Don't Reinvent Features

newtron is Redis-first (§4). Every workaround that invents a custom
CONFIG_DB table or a parallel code path replaces the community-intended
mechanism with one that community daemons can't see, that breaks across
SONiC upgrades, and that outlives the bug by years.

The test: **does the fix use the same CONFIG_DB signals and perform the
same intended actions?** If yes, it's a valid bug fix. If it introduces
a new table or replaces the community mechanism, it's reinvention.

Valid: `newtron-vni-poll` reads the standard `VRF` table and performs
`vtysh vrf X; vni N`. Same signal, same action, polling instead of
pub/sub.

Invalid: a custom `NEWTRON_VNI` table. Callers write to a non-standard
table. Community daemons never see it. When SONiC fixes the bug, the
custom table remains as permanent debt.

**Patch what's broken; don't build parallel infrastructure around it.**

---

## 35. Observe Behavior, Don't Trust Schemas

Write ordering (§16) exists because SONiC daemons have implicit
dependencies that no schema documents. orchagent silently ignores a
VLAN entry missing `admin_status`. vrfmgrd crashes if VRF arrives
before its VLAN interface. frrcfgd processes BGP entries only at
startup, not runtime. The gap between what the schema permits and what
the system does is where the hardest bugs live.

Before implementing a SONiC feature:

1. **Find the CLI path.** Read SONiC CLI source for tables, fields, order.
2. **Run it on a real device.** Configure via CLI. Capture CONFIG_DB.
3. **Read the daemon source.** Understand processing order and APP_DB output.
4. **Implement.** Same entries, same order.
5. **Test in isolation.** Focused suite before composite integration.

**Schema tells you what's valid. Behavior tells you what works.**

---

## 36. DRY Across Programs

Single ownership (§24) applies within a program. This convention
extends it across program boundaries: one spec directory (newtlab,
newtron, newtrun all read from it), one connection mechanism
(SSH-tunneled Redis), one verification mechanism (the ChangeSet), one
platform definition (`platforms.json`), one profile per device
(newtlab writes runtime fields *into the same profile* newtron reads).
Every time a capability is duplicated across programs, the copies
drift.

---

## 37. Greenfield — Backwards Compatibility Is a Non-Goal

newtron has no installed base. When a format or API changes, change it
everywhere in one commit. No compatibility shims, no deprecated
aliases, no dual-format detection. The public API (§30) has one
version: current. `newtron init` scrubs factory state once; after init,
no operation checks for legacy formats.

**Write code for the system as it is today, not as it was yesterday.**

Exception: SONiC releases change schemas and daemon behavior. newtron
must support multiple releases — this is multi-platform support (§38),
not backwards compatibility.

---

## 38. Multi-Version Readiness — Version Differences as Data, Not Code

Version differences should be **data** — schema deltas, capability
tables, field mappings — consumed by the same code path. Pure config
functions (§26) that take a version-keyed schema table produce
version-correct entries without branching.

Three boundaries make this possible: all Redis through Device (§4),
all operations through Node (§28), all entry construction in pure
functions (§26). These boundaries exist for other good reasons. This
principle says: **do not erode them.** The seams that make
multi-version possible are the same seams that make the architecture
clean.

---

## 39. Testing Discipline

Drift detection (§2) and verification (§14) depend on observing
device state after daemons have processed CONFIG_DB entries. SONiC's
asynchronous daemons make this observation unreliable without
discipline.

### Verification must not pass vacuously

A check that finds zero items must **fail**, not pass. Zero results
means the daemon hasn't processed entries yet — not that all checks
passed.

```go
// WRONG — passes when results is empty
for _, hc := range results {
    if hc.Status != "pass" { return false }
}
return true

// CORRECT
if len(results) == 0 { return false }
for _, hc := range results {
    if hc.Status != "pass" { return false }
}
return true
```

### Convergence budget

Each CONFIG_DB entry extends the post-provisioning convergence window.
Before adding a new entry type, count entries per service × per
interface × per device. If the total exceeds test timing margins,
increase preemptively.

Always start tests on a freshly deployed topology. Prior state
corrupts the convergence baseline — the same vacuous-truth problem
from a different angle.

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

§13's fail-closed schema means unknown tables and fields are errors.
This creates friction when adding new CONFIG_DB tables — the developer
must update `schema.go` before any write works. The friction is
intentional. Adding a CONFIG_DB table is a significant act — it changes
what newtron writes to devices — and should require the developer to
also declare the constraints. The cost is a few minutes per table; the
benefit is catching field-name typos at write time instead of
investigating daemon logs at 2 AM.

### Single owner and composite operations

§24 says one file owns each table. But composite operations like
`ApplyService` touch a dozen tables. Composites don't own tables — they
*call* the owning functions and merge the results. `service_gen.go`
calls `createVlanConfig()`, `createVrfConfig()`, `i.bindVrf()`. It
never constructs a VLAN entry inline. The ownership is preserved through
composition, not violated by it.

### Mechanical reversal vs domain reversal

The ChangeSet (§11) records every mutation, which might suggest
mechanical reversal — "just undo the ChangeSet." But §15 insists that
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

§19 reconstructs expected state from current specs. §5 says the device
is reality — not specs. The resolution: reconstruction answers a
different question. §5 says "what exists on the device is ground truth
for operations." §19 says "what *should* exist on the device is
derivable from specs + bindings." The first governs how operations
behave (read CONFIG_DB, act on what's there). The second enables drift
detection (compare what's there against what should be there). Neither
overrides the other — they answer different questions about the same
device.

An honest edge case: if specs change after provisioning — a new VLAN
range, a different route policy, an updated QoS profile —
reconstruction produces the *new* expected state while the device still
has the *old* applied state. A two-way comparison (expected vs actual)
would flag this as drift, but the device hasn't drifted — the specs
evolved. The data model already supports distinguishing these cases:
the intent record captures what was applied; current specs capture
what *would* be applied now; the device captures what's actually there.
A three-way comparison — intent record vs device (true drift) and
intent record vs reconstruction (spec evolution) — would separate
"someone edited CONFIG_DB" from "the spec changed since last apply."
The data exists; the three-way comparison is not yet built.

### Bounded footprint and rollback history

§20 says CONFIG_DB cost must not grow with time. But the rollback
history (§4.6 of the intent design) stores up to 10 completed commits.
The resolution: 10 is a fixed constant, not a function of time. A
device that has run 50,000 operations has the same 10 history entries
as one that has run 11. The bound is structural — enforced by eviction,
not by policy or operator discipline.

### Greenfield and multi-version

§37 says no backwards compatibility. §38 says support multiple SONiC
releases. §37 applies to newtron's own code (types, APIs, key schemas).
§38 applies to the SONiC platform underneath. Supporting 202411 and
202505 is multi-platform support, like a compiler targeting multiple
architectures. There is no "old newtron format" to maintain — only
multiple current SONiC schemas to produce.

### Thesis vs delivery framing

§1 says the Node — unifying intent and reality in one object — is the
central concept. §10 says delivery is what newtron treats as "the
problem." The resolution: delivery is the *externally visible*
promise; the Node is the *architectural mechanism* that keeps it. The
thesis explains why the promise is keepable — one code path means
delivery guarantees are structural, not aspirational. The promise
explains why the thesis matters to operators — they care that their
writes land safely, not that the internal architecture is elegant.

---

# Summary

Legend: **C** = conviction (specific to this project) · **P** = established practice (newtron subscribes) · **S** = style preference

| # | Principle | One-Line Rule | |
|---|-----------|---------------|-|
| 1 | The Node — intent and reality in one object | Intent and reality are the same type viewed from different starting points; the Node is that type | C |
| 2 | Three properties of one code path | Delivery, offline provisioning, and drift detection are structural consequences, not independent features | C |
| 3 | The enforcement contract | Per-feature reliability doesn't scale; make reliability a property of the pipeline | C |
| 4 | SONiC is a database | Every layer of indirection between tool and system is a layer where information is lost | C |
| 5 | Specs are intent; device is reality | The device is the authority after application; newtron accommodates other writers but requires its baseline | C |
| 6 | Interface is the point of service | What you bind services to becomes your unit of lifecycle, state, and failure | C |
| 7 | Network-scoped definition, device-scoped execution | Define once at the broadest scope; the two lifecycles must not be coupled | C |
| 8 | Scope boundaries | newtron owns single-device configuration delivery; mixing abstraction levels entangles failure domains | C |
| 9 | The opinion is in the pattern | newtron constrains the building blocks, not the building | C |
| 10 | Delivery over generation | Generation is solved; delivery — validate, apply atomically, verify, reverse — is not | C |
| 11 | The ChangeSet is universal | Three representations of "what this operation does" will diverge; one representation cannot | C |
| 12 | Dry-run as first-class | The constraint that makes preview safe is the same one that makes offline provisioning possible | C |
| 13 | Prevent bad writes | A bad write that lands is already damage; prevent it before it reaches the device | C |
| 14 | Verify writes, observe the rest | Assert what you know (your own writes); observe what you don't (the network); return data, not judgments | C |
| 15 | Symmetric operations | A config database without reverse operations only accumulates; never enter a state you can't recover from; use structural proof (lock + intent) over heuristic detection (staleness timers) | C |
| 16 | Write ordering and daemon settling | The database is flat but its consumers are not; config functions encode dependency order in the slice | C |
| 17 | Unified intent model | One record structure for all managed resources — operation, name, params, state lifecycle; the Node intermediates all intent | C |
| 18 | On-device intent sufficiency | The device carries enough intent (intent records) to reconstruct expected state; intent record design must serve both teardown and reconstruction | C |
| 19 | Reconstruct, don't record | Derive expected state from authoritative sources (specs + intent records); CONFIG_DB is for intent, not history | C |
| 20 | Bounded device footprint | CONFIG_DB cost must be proportional to infrastructure or bounded by a constant, never proportional to operations over time | C |
| 21 | Policy vs infrastructure | Infrastructure is 1:1 with interface; policy objects are shared, created on first reference, deleted on last | C |
| 22 | Content-hashed naming | The name carries proof of its content; two code paths agree without calling each other | C |
| 23 | BGP peer groups | N individual updates scale linearly; BGP's native template mechanism makes it O(1) | C |
| 24 | Single-owner tables | If one file owns a table, inconsistency is structurally impossible | P |
| 25 | File-level cohesion | Organize by feature, not by layer — a feature scattered across files is a reconstruction, not a location | S |
| 26 | Pure config functions | Generate entries in pure functions; orchestrate them in operations | P |
| 27 | Respect abstraction boundaries | An abstraction that exists but is not used is worse than no abstraction at all | P |
| 28 | Node as isolation boundary | The most dangerous multi-device bugs are operations that silently target the wrong device | C |
| 29 | Verb-first, domain-intent naming | Systems absorb infrastructure vocabulary; name things after the domain, not the database | S |
| 30 | Public API boundary | Every internal refactor broke the orchestrator — until the type boundary separated intent from implementation; a boundary justified by one type applies uniformly to all | P |
| 31 | Transparent transport | Optimize where the bottleneck is; everything else should be as thin as possible | S |
| 32 | Import direction, interface state, episodic caching | Three principles that each prevent a specific class of silent bug | P |
| 33 | Normalize at the boundary | Normalize once at system boundaries; trust canonical form inside | P |
| 34 | Platform patching | Patch what's broken using the same signals and actions; don't build parallel infrastructure | C |
| 35 | Observe behavior, don't trust schemas | Schema tells you what's valid; behavior tells you what works; only observation reveals both | C |
| 36 | DRY across programs | Every capability exists in exactly one place, even across program boundaries | P |
| 37 | Greenfield | Write code for the system as it is today, not as it was yesterday | C |
| 38 | Multi-version readiness | Version differences should be data, not code; preserve the seams that make this possible | C |
| 39 | Testing discipline | Verification must not pass vacuously; convergence budget scales with entry count | C |
